package builtin

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/contract/event"
	"reasonix/internal/safety/sandbox"
	"reasonix/internal/state/sessiontask"
	"reasonix/internal/tools/jobs"
)

func TestStoredTaskLauncherGivesEveryCommandTheSameRestartCapability(t *testing.T) {
	sh := sandbox.ResolveShell("", "", nil)
	command := "sleep 30"
	if sh.Kind == sandbox.ShellPowerShell {
		command = "Start-Sleep -Seconds 30"
	}
	m := jobs.NewManager(event.Discard)
	defer m.Close()
	b := bash{shell: sh, workDir: t.TempDir()}
	if starter, ok := NewTaskCommandStarter(b); !ok || starter == nil {
		t.Fatal("bound bash tool did not expose its task starter")
	}
	shell := sessiontask.Shell{
		Kind: sh.Kind.String(), Path: sh.Path, Args: sh.Args(),
	}

	service := sessiontask.Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: ".", Command: command, Shell: shell,
	}
	view, err := b.startTaskCommand(context.Background(), m, "session", service)
	if err != nil {
		t.Fatalf("service start: %v", err)
	}
	if view.CommandID != "auth" || !view.Restartable {
		t.Fatalf("service view = %+v", view)
	}
	if !m.KillForSession("session", view.ID) {
		t.Fatal("service was not running")
	}

	build := sessiontask.Command{
		ID: "build", Name: "build", Runner: "bash",
		CWD: ".", Command: command, Shell: shell,
	}
	view, err = b.startTaskCommand(context.Background(), m, "session", build)
	if err != nil {
		t.Fatalf("one-shot start: %v", err)
	}
	if view.CommandID != "build" || !view.Restartable {
		t.Fatalf("build view = %+v; whether a job can hand over in place is not a command type", view)
	}
	if !m.KillForSession("session", view.ID) {
		t.Fatal("one-shot was not running")
	}
}

func TestTaskCommandInspectorReportsMissingWorkingDirectoryAndShell(t *testing.T) {
	sh := sandbox.ResolveShell("", "", nil)
	root := t.TempDir()
	b := bash{shell: sh, workDir: root}
	inspector, ok := NewTaskCommandInspector(b)
	if !ok || inspector == nil {
		t.Fatal("bound bash tool did not expose its task-command inspector")
	}
	shell := sessiontask.Shell{Kind: sh.Kind.String(), Path: sh.Path, Args: sh.Args()}

	missingCWD := sessiontask.Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: "missing-folder", Command: "echo auth", Shell: shell,
	}
	if reason := inspector(missingCWD); !strings.Contains(reason, "cwd") || !strings.Contains(reason, "missing-folder") {
		t.Fatalf("missing cwd reason = %q", reason)
	}

	missingShell := missingCWD
	missingShell.CWD = "."
	missingShell.Shell.Path = root + "/missing-shell"
	if reason := inspector(missingShell); !strings.Contains(reason, "shell") || !strings.Contains(reason, "missing-shell") {
		t.Fatalf("missing shell reason = %q", reason)
	}
}

func TestTaskLaunchRecipeKeepsWorkingDirectoryAndShellDiagnostics(t *testing.T) {
	sh := sandbox.ResolveShell("", "", nil)
	sh.Drives = `C:\ is /mnt/c/`
	recipe := shellLaunchRecipe(sh, "go test ./...", "backend/RuoYi-Cloud")
	if recipe.CWD != "backend/RuoYi-Cloud" || recipe.Shell.Drives != sh.Drives {
		t.Fatalf("launch recipe = %+v", recipe)
	}
	stored, err := storedShell(sessiontask.Shell{
		Kind: recipe.Shell.Kind, Path: recipe.Shell.Path, Args: recipe.Shell.Args,
		Version: recipe.Shell.Version, Drives: recipe.Shell.Drives,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Drives != sh.Drives {
		t.Fatalf("stored shell drives = %q, want %q", stored.Drives, sh.Drives)
	}
}
