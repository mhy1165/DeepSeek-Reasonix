package control

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/tool"
	"reasonix/internal/safety/sandbox"
	"reasonix/internal/state/sessionstore"
	"reasonix/internal/state/sessiontask"
	"reasonix/internal/state/store"
	"reasonix/internal/tools/jobs"
)

func TestStartRegisteredServiceCreatesOneRunningJobPerCommand(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	manager := jobs.NewManager(event.Discard)
	t.Cleanup(manager.Close)
	command := sessiontask.Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: ".", Command: "java -jar auth.jar",
		Shell: sessiontask.Shell{Kind: "powershell", Path: "pwsh.exe"},
	}
	reg := sessiontask.New(sessionstore.BranchID(path))
	if err := reg.Upsert(command); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(store.SessionTaskCommands(path)); err != nil {
		t.Fatal(err)
	}

	starter := func(_ context.Context, m *jobs.Manager, parent string, cmd sessiontask.Command) (jobs.View, error) {
		job := m.StartForSession(parent, "bash", cmd.Name, func(ctx context.Context, _ io.Writer) (string, error) {
			<-ctx.Done()
			return "", nil
		}, jobs.WithCommandID(cmd.ID), jobs.WithRestart(func(context.Context, io.Writer) (string, error) {
			return "", nil
		}))
		view, ok := m.ViewForSession(parent, job.ID)
		if !ok {
			return jobs.View{}, errors.New("started job disappeared")
		}
		return view, nil
	}
	ctrl := New(Options{
		Jobs: manager, Sink: event.Discard, SessionDir: dir, SessionPath: path,
		WorkspaceRoot: dir, TaskCommandStarter: starter,
	})
	t.Cleanup(ctrl.Close)

	first, err := ctrl.StartTaskCommand(t.Context(), "auth")
	if err != nil || first.CommandID != "auth" {
		t.Fatalf("start = %+v, %v", first, err)
	}
	if _, err := ctrl.StartTaskCommand(t.Context(), "auth"); !errors.Is(err, ErrTaskCommandRunning) {
		t.Fatalf("second start error = %v, want ErrTaskCommandRunning", err)
	}
}

func TestStartRegisteredCommandReservesBeforeLaunching(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	manager := jobs.NewManager(event.Discard)
	t.Cleanup(manager.Close)
	command := sessiontask.Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: ".", Command: "java -jar auth.jar",
		Shell: sessiontask.Shell{Kind: "powershell", Path: "pwsh.exe"},
	}
	reg := sessiontask.New(sessionstore.BranchID(path))
	if err := reg.Upsert(command); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(store.SessionTaskCommands(path)); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	starter := func(_ context.Context, m *jobs.Manager, parent string, cmd sessiontask.Command) (jobs.View, error) {
		n := calls.Add(1)
		if n == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		job := m.StartForSession(parent, "bash", cmd.Name, func(ctx context.Context, _ io.Writer) (string, error) {
			<-ctx.Done()
			return "", nil
		}, jobs.WithCommandID(cmd.ID))
		view, ok := m.ViewForSession(parent, job.ID)
		if !ok {
			return jobs.View{}, errors.New("started job disappeared")
		}
		return view, nil
	}
	ctrl := New(Options{
		Jobs: manager, Sink: event.Discard, SessionDir: dir, SessionPath: path,
		WorkspaceRoot: dir, TaskCommandStarter: starter,
	})
	t.Cleanup(ctrl.Close)

	firstResult := make(chan error, 1)
	go func() {
		_, err := ctrl.StartTaskCommand(t.Context(), "auth")
		firstResult <- err
	}()
	<-firstStarted
	if _, err := ctrl.StartTaskCommand(t.Context(), "auth"); !errors.Is(err, ErrTaskCommandRunning) {
		t.Fatalf("concurrent start error = %v, want ErrTaskCommandRunning", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("starter calls = %d, want 1", got)
	}
	close(releaseFirst)
	if err := <-firstResult; err != nil {
		t.Fatalf("first start: %v", err)
	}
}

func TestStartRegisteredCommandCarriesTheSessionApprovalContext(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	reg := sessiontask.New(sessionstore.BranchID(path))
	if err := reg.Upsert(sessiontask.Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: ".", Command: "java -jar auth.jar",
		Shell: sessiontask.Shell{Kind: "powershell", Path: "pwsh.exe"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(store.SessionTaskCommands(path)); err != nil {
		t.Fatal(err)
	}

	var hasApprover bool
	starter := func(ctx context.Context, _ *jobs.Manager, _ string, _ sessiontask.Command) (jobs.View, error) {
		_, hasApprover = sandbox.EscapeApproverFrom(ctx)
		return jobs.View{ID: "bash-7", Status: string(jobs.Running)}, nil
	}
	ctrl := New(Options{
		Jobs: jobs.NewManager(event.Discard), Sink: event.Discard,
		SessionDir: dir, SessionPath: path, WorkspaceRoot: dir, TaskCommandStarter: starter,
	})
	t.Cleanup(ctrl.Close)

	if _, err := ctrl.StartTaskCommand(t.Context(), "auth"); err != nil {
		t.Fatal(err)
	}
	if !hasApprover {
		t.Fatal("task command start lost the session's sandbox escape approver")
	}
}

func TestStartRegisteredCommandHonorsCallerCancellation(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	reg := sessiontask.New(sessionstore.BranchID(path))
	if err := reg.Upsert(sessiontask.Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: ".", Command: "java -jar auth.jar",
		Shell: sessiontask.Shell{Kind: "powershell", Path: "pwsh.exe"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(store.SessionTaskCommands(path)); err != nil {
		t.Fatal(err)
	}

	starter := func(ctx context.Context, _ *jobs.Manager, _ string, _ sessiontask.Command) (jobs.View, error) {
		return jobs.View{}, ctx.Err()
	}
	ctrl := New(Options{
		Jobs: jobs.NewManager(event.Discard), Sink: event.Discard,
		SessionDir: dir, SessionPath: path, WorkspaceRoot: dir, TaskCommandStarter: starter,
	})
	t.Cleanup(ctrl.Close)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := ctrl.StartTaskCommand(ctx, "auth"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled start error = %v, want context.Canceled", err)
	}
}

func TestTaskCommandsExposePreflightUnavailability(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	reg := sessiontask.New(sessionstore.BranchID(path))
	if err := reg.Upsert(sessiontask.Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: "missing", Command: "java -jar auth.jar",
		Shell: sessiontask.Shell{Kind: "powershell", Path: "missing-pwsh.exe"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(store.SessionTaskCommands(path)); err != nil {
		t.Fatal(err)
	}

	ctrl := New(Options{
		Sink: event.Discard, SessionDir: dir, SessionPath: path,
		TaskCommandInspector: func(sessiontask.Command) string {
			return "stored shell is unavailable: missing-pwsh.exe"
		},
	})
	t.Cleanup(ctrl.Close)
	commands := ctrl.TaskCommands()
	if len(commands) != 1 || commands[0].Available || !strings.Contains(commands[0].UnavailableReason, "missing-pwsh.exe") {
		t.Fatalf("task command views = %+v", commands)
	}
}

func TestTaskCommandsReportAndRetryACorruptRegistry(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	sidecar := store.SessionTaskCommands(path)
	if err := os.WriteFile(sidecar, []byte(`{"schemaVersion":1,"sessionId":`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctrl := New(Options{Sink: event.Discard, SessionDir: dir, SessionPath: path})
	t.Cleanup(ctrl.Close)

	if commands := ctrl.TaskCommands(); len(commands) != 0 {
		t.Fatalf("corrupt registry commands = %+v", commands)
	}
	if got := ctrl.TaskCommandsError(); got != "task_command.registry_unavailable" {
		t.Fatalf("registry error = %q", got)
	}

	reg := sessiontask.New(sessionstore.BranchID(path))
	if err := reg.Upsert(sessiontask.Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: ".", Command: "java -jar auth.jar",
		Shell: sessiontask.Shell{Kind: "powershell", Path: "pwsh.exe"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(sidecar); err != nil {
		t.Fatal(err)
	}
	if got := ctrl.TaskCommandsError(); got != "" {
		t.Fatalf("registry error after repair = %q", got)
	}
	if commands := ctrl.TaskCommands(); len(commands) != 1 || commands[0].ID != "auth" {
		t.Fatalf("commands after repair = %+v", commands)
	}
}

func TestRegisterTaskCommandCopiesRecipeAndSurvivesSessionReload(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	manager := jobs.NewManager(event.Discard)
	t.Cleanup(manager.Close)
	parent := sessionstore.BranchID(path)
	started := make(chan struct{})
	job := manager.StartForSession(parent, "bash", "auth", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		return "", nil
	}, jobs.WithLaunchRecipe(jobs.LaunchRecipe{
		Runner: "bash", CWD: ".", Command: "java -jar auth.jar",
		Shell: jobs.ShellRecipe{Kind: "powershell", Path: `C:\pwsh.exe`, Version: "7+"},
	}), jobs.WithRestart(func(context.Context, io.Writer) (string, error) {
		return "", nil
	}))
	<-started

	ctrl := New(Options{Jobs: manager, Sink: event.Discard, SessionDir: dir, SessionPath: path})
	t.Cleanup(ctrl.Close)
	id, err := ctrl.RegisterTaskCommand(context.Background(), tool.TaskCommandRegistration{
		JobID: job.ID, Name: "auth",
	})
	if err != nil {
		t.Fatal(err)
	}
	view, _ := manager.ViewForSession(parent, job.ID)
	if view.CommandID != id {
		t.Fatalf("job command id = %q, want %q", view.CommandID, id)
	}

	reopened := New(Options{Jobs: manager, Sink: event.Discard, SessionDir: dir, SessionPath: path})
	t.Cleanup(reopened.Close)
	commands := reopened.TaskCommands()
	if len(commands) != 1 || commands[0].ID != id || commands[0].Command.Command != "java -jar auth.jar" {
		t.Fatalf("reloaded commands = %+v", commands)
	}
	_ = manager.KillForSession(parent, job.ID)
}

func TestRegisterTaskCommandRequiresConfirmationToOverwriteAnExistingID(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	manager := jobs.NewManager(event.Discard)
	t.Cleanup(manager.Close)
	parent := sessionstore.BranchID(path)
	started := make(chan struct{})
	job := manager.StartForSession(parent, "bash", "auth", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		return "", nil
	}, jobs.WithLaunchRecipe(jobs.LaunchRecipe{
		Runner: "bash", CWD: ".", Command: "java -jar auth.jar",
		Shell: jobs.ShellRecipe{Kind: "powershell", Path: `C:\pwsh.exe`},
	}), jobs.WithRestart(func(context.Context, io.Writer) (string, error) {
		return "", nil
	}))
	<-started
	ctrl := New(Options{Jobs: manager, Sink: event.Discard, SessionDir: dir, SessionPath: path})
	t.Cleanup(ctrl.Close)

	req := tool.TaskCommandRegistration{JobID: job.ID, Name: "auth"}
	if _, err := ctrl.RegisterTaskCommand(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.RegisterTaskCommand(t.Context(), req); !errors.Is(err, ErrTaskCommandExists) {
		t.Fatalf("repeat registration error = %v, want ErrTaskCommandExists", err)
	}
	req.Overwrite = true
	if _, err := ctrl.RegisterTaskCommand(t.Context(), req); err != nil {
		t.Fatalf("confirmed overwrite: %v", err)
	}
	if commands := ctrl.TaskCommands(); len(commands) != 1 {
		t.Fatalf("commands = %+v", commands)
	}
	_ = manager.KillForSession(parent, job.ID)
}

func TestTaskCommandRegistrationRollsBackWhenTheJobDisappears(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	manager := jobs.NewManager(event.Discard)
	t.Cleanup(manager.Close)
	ctrl := New(Options{Jobs: manager, Sink: event.Discard, SessionDir: dir, SessionPath: path})
	t.Cleanup(ctrl.Close)

	previous := sessiontask.New(sessionstore.BranchID(path))
	next := sessiontask.New(sessionstore.BranchID(path))
	if err := next.Upsert(sessiontask.Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: ".", Command: "java -jar auth.jar",
		Shell: sessiontask.Shell{Kind: "powershell", Path: "pwsh.exe"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.commitTaskCommandRegistration("missing-job", "auth", previous, next); !errors.Is(err, ErrTaskCommandInvalid) {
		t.Fatalf("registration error = %v, want ErrTaskCommandInvalid", err)
	}
	if commands := ctrl.TaskCommands(); len(commands) != 0 {
		t.Fatalf("registration was not rolled back: %+v", commands)
	}
}

func TestRegisterTaskCommandHonorsCallerCancellation(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	manager := jobs.NewManager(event.Discard)
	t.Cleanup(manager.Close)
	parent := sessionstore.BranchID(path)
	started := make(chan struct{})
	job := manager.StartForSession(parent, "bash", "auth", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		return "", nil
	}, jobs.WithLaunchRecipe(jobs.LaunchRecipe{
		Runner: "bash", CWD: ".", Command: "java -jar auth.jar",
		Shell: jobs.ShellRecipe{Kind: "powershell", Path: `C:\pwsh.exe`},
	}), jobs.WithRestart(func(context.Context, io.Writer) (string, error) {
		return "", nil
	}))
	<-started
	ctrl := New(Options{Jobs: manager, Sink: event.Discard, SessionDir: dir, SessionPath: path})
	t.Cleanup(ctrl.Close)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := ctrl.RegisterTaskCommand(ctx, tool.TaskCommandRegistration{
		JobID: job.ID, Name: "auth",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled registration error = %v, want context.Canceled", err)
	}
	_ = manager.KillForSession(parent, job.ID)
}

func TestTaskCommandsRejectAnotherSessionsSidecar(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	reg := sessiontask.New("another-session")
	if err := reg.Save(store.SessionTaskCommands(path)); err != nil {
		t.Fatal(err)
	}
	ctrl := New(Options{Sink: event.Discard, SessionDir: dir, SessionPath: path})
	t.Cleanup(ctrl.Close)
	if commands := ctrl.TaskCommands(); len(commands) != 0 {
		t.Fatalf("foreign sidecar loaded: %+v", commands)
	}
}

func TestTaskCommandIDIsStableAndFilesystemSafe(t *testing.T) {
	a := taskCommandID("构建 auth", "go build ./...")
	b := taskCommandID("构建 auth", "go build ./...")
	if a != b || !strings.Contains(a, "-") {
		t.Fatalf("ids = %q, %q", a, b)
	}
	if strings.ContainsAny(a, `\/: `) {
		t.Fatalf("unsafe id %q", a)
	}
}

func TestTaskCommandsRequireDurableSession(t *testing.T) {
	ctrl := New(Options{Sink: event.Discard})
	t.Cleanup(ctrl.Close)
	if _, err := ctrl.loadTaskCommands(); !errors.Is(err, ErrTaskCommandNoDurableSession) {
		t.Fatalf("error = %v", err)
	}
}

// taskCommandHarness builds a controller whose starter mirrors the shipped one:
// a service gets a restart recipe, a one-shot does not, and both link the job to
// the registered command id.
func taskCommandHarness(t *testing.T, commands ...sessiontask.Command) *Controller {
	t.Helper()
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	manager := jobs.NewManager(event.Discard)
	t.Cleanup(manager.Close)
	reg := sessiontask.New(sessionstore.BranchID(path))
	for _, command := range commands {
		if err := reg.Upsert(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Save(store.SessionTaskCommands(path)); err != nil {
		t.Fatal(err)
	}
	starter := func(_ context.Context, m *jobs.Manager, parent string, cmd sessiontask.Command) (jobs.View, error) {
		run := func(ctx context.Context, _ io.Writer) (string, error) {
			<-ctx.Done()
			return "", nil
		}
		options := []jobs.StartOption{jobs.WithCommandID(cmd.ID), jobs.WithRestart(run)}
		job := m.StartForSession(parent, "bash", cmd.Name, run, options...)
		view, ok := m.ViewForSession(parent, job.ID)
		if !ok {
			return jobs.View{}, errors.New("started job disappeared")
		}
		return view, nil
	}
	ctrl := New(Options{
		Jobs: manager, Sink: event.Discard, SessionDir: dir, SessionPath: path,
		WorkspaceRoot: dir, TaskCommandStarter: starter,
	})
	t.Cleanup(ctrl.Close)
	return ctrl
}

func serviceCommand(id, name string) sessiontask.Command {
	return sessiontask.Command{
		ID: id, Name: name, Runner: "bash",
		CWD: ".", Command: "java -jar " + name + ".jar",
		Shell: sessiontask.Shell{Kind: "powershell", Path: "pwsh.exe"},
	}
}

func TestRegisteredTasksReportTheJobCarryingEachCommand(t *testing.T) {
	ctrl := taskCommandHarness(t, serviceCommand("auth", "auth"), sessiontask.Command{
		ID: "build", Name: "build", Runner: "bash",
		CWD: ".", Command: "mvn install",
		Shell: sessiontask.Shell{Kind: "powershell", Path: "pwsh.exe"},
	})
	if _, err := ctrl.StartRegisteredTask(t.Context(), "auth"); err != nil {
		t.Fatal(err)
	}

	tasks, err := ctrl.RegisteredTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]tool.TaskCommandStatus{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	running, ok := byID["auth"]
	if !ok || running.JobID == "" || running.JobStatus != string(jobs.Running) {
		t.Fatalf("auth status = %+v, want a running job", running)
	}
	idle, ok := byID["build"]
	if !ok || idle.JobID != "" || idle.JobStatus != "" {
		t.Fatalf("build status = %+v, want no job", idle)
	}
}

func TestStopRegisteredTaskStopsTheCurrentJob(t *testing.T) {
	ctrl := taskCommandHarness(t, serviceCommand("auth", "auth"))
	started, err := ctrl.StartRegisteredTask(t.Context(), "auth")
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := ctrl.StopRegisteredTask(t.Context(), "auth")
	if err != nil {
		t.Fatal(err)
	}
	if stopped != started.JobID {
		t.Fatalf("stopped %q, want %q", stopped, started.JobID)
	}
	if _, err := ctrl.StopRegisteredTask(t.Context(), "auth"); err == nil {
		t.Fatal("stopping a stopped command should report there is nothing to stop")
	}
	tasks, err := ctrl.RegisteredTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].JobStatus == string(jobs.Running) {
		t.Fatalf("status after stop = %+v, want a non-running job", tasks)
	}
}

func TestRestartRegisteredTaskReplacesTheRunningJob(t *testing.T) {
	ctrl := taskCommandHarness(t, serviceCommand("auth", "auth"))
	started, err := ctrl.StartRegisteredTask(t.Context(), "auth")
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := ctrl.RestartRegisteredTask(t.Context(), "auth")
	if err != nil {
		t.Fatal(err)
	}
	if restarted.PreviousJobID != started.JobID {
		t.Fatalf("previous = %q, want %q", restarted.PreviousJobID, started.JobID)
	}
	if restarted.JobID == started.JobID || restarted.JobID == "" {
		t.Fatalf("replacement = %q, want a new job", restarted.JobID)
	}
	tasks, err := ctrl.RegisteredTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].JobID != restarted.JobID || tasks[0].JobStatus != string(jobs.Running) {
		t.Fatalf("status after restart = %+v, want the replacement running", tasks)
	}
}

// A job restored from disk carries no restart recipe, which used to make restart
// fail with "not restartable". The registered definition is a recipe too, so the
// restart must still work: stop the old attempt, then start from the definition.
func TestRestartRegisteredTaskFallsBackToTheRegisteredDefinition(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "session.jsonl")
	manager := jobs.NewManager(event.Discard)
	t.Cleanup(manager.Close)
	reg := sessiontask.New(sessionstore.BranchID(path))
	if err := reg.Upsert(serviceCommand("auth", "auth")); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(store.SessionTaskCommands(path)); err != nil {
		t.Fatal(err)
	}
	var starts int
	starter := func(_ context.Context, m *jobs.Manager, parent string, cmd sessiontask.Command) (jobs.View, error) {
		starts++
		job := m.StartForSession(parent, "bash", cmd.Name, func(ctx context.Context, _ io.Writer) (string, error) {
			<-ctx.Done()
			return "", nil
		}, jobs.WithCommandID(cmd.ID))
		view, ok := m.ViewForSession(parent, job.ID)
		if !ok {
			return jobs.View{}, errors.New("started job disappeared")
		}
		return view, nil
	}
	ctrl := New(Options{
		Jobs: manager, Sink: event.Discard, SessionDir: dir, SessionPath: path,
		WorkspaceRoot: dir, TaskCommandStarter: starter,
	})
	t.Cleanup(ctrl.Close)

	first, err := ctrl.StartRegisteredTask(t.Context(), "auth")
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := ctrl.RestartRegisteredTask(t.Context(), "auth")
	if err != nil {
		t.Fatalf("restart without a job recipe: %v", err)
	}
	if restarted.PreviousJobID != first.JobID || restarted.JobID == first.JobID || restarted.JobID == "" {
		t.Fatalf("restart = %+v, want %s replaced by a new job", restarted, first.JobID)
	}
	if starts != 2 {
		t.Fatalf("starter ran %d times, want 2", starts)
	}
	tasks, err := ctrl.RegisteredTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].JobID != restarted.JobID || tasks[0].JobStatus != string(jobs.Running) {
		t.Fatalf("status after restart = %+v, want the replacement running", tasks)
	}
}

func TestRestartRegisteredTaskWithoutAJobPointsAtStart(t *testing.T) {
	ctrl := taskCommandHarness(t, serviceCommand("auth", "auth"))
	if _, err := ctrl.RestartRegisteredTask(t.Context(), "auth"); !errors.Is(err, ErrTaskCommandNotFound) {
		t.Fatalf("restart idle error = %v, want ErrTaskCommandNotFound", err)
	}
}

func TestRemoveTaskCommandDeletesTheDefinition(t *testing.T) {
	ctrl := taskCommandHarness(t, serviceCommand("auth", "auth"), serviceCommand("gateway", "gateway"))
	if err := ctrl.RemoveTaskCommand(t.Context(), "auth"); err != nil {
		t.Fatal(err)
	}
	tasks, err := ctrl.RegisteredTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "gateway" {
		t.Fatalf("registry after remove = %+v, want only gateway", tasks)
	}
	// The sidecar must agree with the view, or the row comes back on reload.
	reg, err := ctrl.loadTaskCommands()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := taskCommandByID(reg.Commands, "auth"); ok {
		t.Fatal("removed command is still in the saved registry")
	}
	if err := ctrl.RemoveTaskCommand(t.Context(), "auth"); !errors.Is(err, ErrTaskCommandNotFound) {
		t.Fatalf("second remove error = %v, want ErrTaskCommandNotFound", err)
	}
}

func TestRemoveTaskCommandRefusesWhileItIsRunning(t *testing.T) {
	ctrl := taskCommandHarness(t, serviceCommand("auth", "auth"))
	started, err := ctrl.StartRegisteredTask(t.Context(), "auth")
	if err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RemoveTaskCommand(t.Context(), "auth"); !errors.Is(err, ErrTaskCommandRunning) {
		t.Fatalf("remove running error = %v, want ErrTaskCommandRunning", err)
	}
	if _, err := ctrl.StopRegisteredTask(t.Context(), "auth"); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RemoveTaskCommand(t.Context(), "auth"); err != nil {
		t.Fatalf("remove after stop: %v", err)
	}
	if _, ok := ctrl.jobs.ViewForSession(ctrl.parentSessionID(), started.JobID); !ok {
		t.Fatal("removing the definition discarded the job that had already run")
	}
}
