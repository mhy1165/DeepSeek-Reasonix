package builtin

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"reasonix/internal/safety/sandbox"
	"reasonix/internal/state/sessiontemp"
	"reasonix/internal/tools/jobs"
	"reasonix/internal/tools/shellrun"
)

// backgroundCommand owns the replayable part of a local shell launch. Its
// environment and sandbox decision are snapshots from the approved call; a
// restart creates a fresh process and session-temp lease from those same
// values rather than reinterpreting command text.
type backgroundCommand struct {
	b           bash
	baseCtx     context.Context
	p           bashParams
	sh          sandbox.Shell
	prepared    sandbox.Prepared
	env         []string
	workDir     string
	sessionTemp *sessiontemp.Manager
}

func shellLaunchRecipe(sh sandbox.Shell, command, cwd string) jobs.LaunchRecipe {
	desc := shellrun.DescriptorFromShell(sh)
	return jobs.LaunchRecipe{
		Runner: "bash", CWD: cwd, Command: command,
		Shell: jobs.ShellRecipe{
			Kind: sh.Kind.String(), Path: sh.Path, Args: sh.Args(), Version: desc.ShellVersion,
			Drives: sh.Drives,
		},
	}
}

func relativeLaunchCWD(root, cwd string) string {
	rootAbs, rootErr := filepath.Abs(root)
	cwdAbs, cwdErr := filepath.Abs(cwd)
	if rootErr != nil || cwdErr != nil {
		return "."
	}
	rel, err := filepath.Rel(rootAbs, cwdAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "."
	}
	return filepath.ToSlash(rel)
}

func (c *backgroundCommand) restart(jobCtx context.Context, out io.Writer) (string, error) {
	prepared, lease, err := c.freshPrepared()
	if err != nil {
		return "", err
	}
	return c.run(jobCtx, out, prepared, lease)
}

func (c *backgroundCommand) run(jobCtx context.Context, out io.Writer, prepared sandbox.Prepared, lease *sessiontemp.Lease) (string, error) {
	if lease != nil {
		defer lease.Release()
	}
	env := applyEnvOverrides(c.env, prepared.EnvOverrides)
	if err := checkCommandLine(prepared.Argv); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(jobCtx, prepared.Argv[0], prepared.Argv[1:]...)
	cmd.Dir = c.workDir
	cmd.Env = env
	cmd.WaitDelay = bashWaitDelay
	cmd.Stdout = out
	cmd.Stderr = out
	tracked, runErr := runShellProcess(jobCtx, cmd, c.sh, c.p.Command,
		shouldTrackShellProcess(prepared.Wrapped, c.sh, c.p.Command, c.p.PreserveBackgroundProcesses))
	if shouldReapAfterRun(jobCtx, c.sh, c.p.Command, c.p.PreserveBackgroundProcesses) {
		reapShellProcess(cmd, tracked)
	}
	c.b.writeEgressNote(out, prepared.EgressToken)
	return "", jobExit(normalizeBashRunError(jobCtx, runErr, c.p.PreserveBackgroundProcesses))
}

func (c *backgroundCommand) freshPrepared() (sandbox.Prepared, *sessiontemp.Lease, error) {
	var lease *sessiontemp.Lease
	sessionDir := ""
	if c.sessionTemp != nil {
		next, err := c.sessionTemp.Acquire()
		if err != nil {
			return sandbox.Prepared{}, nil, fmt.Errorf("%w: %w", errSessionTemp, err)
		}
		lease = next
		sessionDir = next.Dir()
	}
	spec := c.b.sb
	spec.SessionTemp = sessionDir
	prepared := sandbox.Prepared{SessionTemp: sessionDir}
	if c.prepared.Wrapped {
		prepared.Argv, prepared.Wrapped = bashSandboxCommand(spec, c.sh, c.p.Command)
		if !prepared.Wrapped {
			if lease != nil {
				lease.Release()
			}
			return sandbox.Prepared{}, nil, fmt.Errorf("sandbox could not wrap the restarted command")
		}
	} else {
		prepared.Argv = unconfinedShellArgv(c.sh, c.p.Command)
	}
	prepared.LinuxSandboxed = prepared.Wrapped && sessionDir != "" && runtime.GOOS == "linux"
	prepared.EnvOverrides = sandbox.SessionTempEnv(sessionDir, prepared.LinuxSandboxed)
	if err := refuseLinkedGitMetadata(prepared.Wrapped, spec, lease); err != nil {
		if lease != nil {
			lease.Release()
		}
		return sandbox.Prepared{}, nil, err
	}
	c.b.routeEgress(c.baseCtx, &prepared)
	return prepared, lease, nil
}
