package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/contract/tool"
	"reasonix/internal/safety/sandbox"
	"reasonix/internal/state/sessiontask"
	"reasonix/internal/tools/jobs"
	"reasonix/internal/tools/shellrun"
)

// canRestartBackgroundCommand says whether a started command kept a process tree
// the host can hand over in place. A command that asked to keep its background
// processes cannot: the tree outlives the job that would be replaced.
func canRestartBackgroundCommand(sh sandbox.Shell, p bashParams) bool {
	if p.PreserveBackgroundProcesses {
		return false
	}
	return sh.Kind != sandbox.ShellBash || !hasExplicitBackgroundKeepalive(p.Command)
}

type TaskCommandStarter func(context.Context, *jobs.Manager, string, sessiontask.Command) (jobs.View, error)
type TaskCommandInspector func(sessiontask.Command) string

// NewTaskCommandStarter exposes the bound bash tool's host-owned launcher to the
// controller without letting the controller construct shell commands itself.
func NewTaskCommandStarter(t tool.Tool) (TaskCommandStarter, bool) {
	starter, ok := t.(interface {
		startTaskCommand(context.Context, *jobs.Manager, string, sessiontask.Command) (jobs.View, error)
	})
	if !ok {
		return nil, false
	}
	return starter.startTaskCommand, true
}

// NewTaskCommandInspector exposes the same preflight checks used by the stored
// launcher, so a missing shell or working directory is visible before Start.
func NewTaskCommandInspector(t tool.Tool) (TaskCommandInspector, bool) {
	inspector, ok := t.(interface {
		taskCommandUnavailable(sessiontask.Command) string
	})
	if !ok {
		return nil, false
	}
	return inspector.taskCommandUnavailable, true
}

func (b bash) taskCommandUnavailable(command sessiontask.Command) string {
	if command.Runner != "bash" {
		return fmt.Sprintf("unsupported task runner %q", command.Runner)
	}
	if b.workDir == "" {
		return "task command workspace is unavailable"
	}
	if _, err := confinedTaskCWD(b.workDir, command.CWD); err != nil {
		return err.Error()
	}
	if _, err := storedShellMetadata(command.Shell); err != nil {
		return err.Error()
	}
	return ""
}

func (b bash) startTaskCommand(ctx context.Context, jm *jobs.Manager, parentSession string, command sessiontask.Command) (jobs.View, error) {
	if jm == nil {
		return jobs.View{}, fmt.Errorf("background jobs are unavailable")
	}
	if command.Runner != "bash" {
		return jobs.View{}, fmt.Errorf("unsupported task runner %q", command.Runner)
	}
	root := b.workDir
	if root == "" {
		return jobs.View{}, fmt.Errorf("task command workspace is unavailable")
	}
	cwd, err := confinedTaskCWD(root, command.CWD)
	if err != nil {
		return jobs.View{}, err
	}
	sh, err := storedShell(command.Shell)
	if err != nil {
		return jobs.View{}, err
	}
	p := bashParams{Command: command.Command, Description: command.Name, RunInBackground: true}
	args, _ := json.Marshal(p)
	prepared, _, lease, err := b.prepareGuardedLaunch(ctx, sh, p, args)
	if err != nil {
		return jobs.View{}, err
	}
	holdLease := true
	defer func() {
		if holdLease && lease != nil {
			lease.Release()
		}
	}()
	cmdEnv := applyEnvOverrides(shellrun.CommandEnv(ctx), append(shellEnvOverrides(b.sb.ShellEnv), prepared.EnvOverrides...))
	if err := checkCommandLine(prepared.Argv); err != nil {
		return jobs.View{}, err
	}
	launch := &backgroundCommand{
		b: b, baseCtx: ctx, p: p, sh: sh, prepared: prepared, env: cmdEnv,
		workDir: cwd, sessionTemp: b.sessionTempManager(ctx),
	}
	options := []jobs.StartOption{
		jobs.WithPresentation(command.Name, command.Command),
		jobs.WithLaunchRecipe(shellLaunchRecipe(sh, command.Command, command.CWD)),
		jobs.WithCommandID(command.ID),
	}
	// In-place handover is an optimisation, not the definition of what restart
	// means: a job that cannot hand over is replaced from this recipe instead.
	if canRestartBackgroundCommand(sh, p) {
		options = append(options, jobs.WithRestart(launch.restart))
	}
	job := jm.StartForSession(parentSession, "bash", command.Name, func(jobCtx context.Context, out io.Writer) (string, error) {
		return launch.run(jobCtx, out, prepared, lease)
	}, options...)
	holdLease = false
	view, ok := jm.ViewForSession(parentSession, job.ID)
	if !ok {
		return jobs.View{}, fmt.Errorf("started task command is unavailable")
	}
	return view, nil
}

func confinedTaskCWD(root, rel string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	cwd := filepath.Join(root, filepath.FromSlash(rel))
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	within, err := filepath.Rel(root, cwd)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("task command cwd escapes the workspace")
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("task command cwd: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("task command cwd is not a directory: %s", cwd)
	}
	return cwd, nil
}

func storedShell(recipe sessiontask.Shell) (sandbox.Shell, error) {
	sh, err := storedShellMetadata(recipe)
	if err != nil {
		return sandbox.Shell{}, err
	}
	if len(recipe.Args) > 0 {
		if _, err := sandbox.VerifyLauncher(recipe.Kind, recipe.Path, recipe.Args); err != nil {
			return sandbox.Shell{}, fmt.Errorf("stored shell cannot start: %w", err)
		}
	} else if err := sandbox.VerifyShell(recipe.Kind, recipe.Path); err != nil {
		return sandbox.Shell{}, fmt.Errorf("stored shell cannot start: %w", err)
	}
	return sh, nil
}

func storedShellMetadata(recipe sessiontask.Shell) (sandbox.Shell, error) {
	var kind sandbox.ShellKind
	switch recipe.Kind {
	case "bash":
		kind = sandbox.ShellBash
	case "powershell":
		kind = sandbox.ShellPowerShell
	default:
		return sandbox.Shell{}, fmt.Errorf("unsupported stored shell %q", recipe.Kind)
	}
	if info, err := os.Stat(recipe.Path); err != nil || info.IsDir() {
		if err == nil {
			err = fmt.Errorf("path is a directory")
		}
		return sandbox.Shell{}, fmt.Errorf("stored shell is unavailable: %w", err)
	}
	stored := sandbox.Shell{Kind: kind, Path: recipe.Path, Drives: recipe.Drives}.WithArgs(recipe.Args)
	return stored, nil
}
