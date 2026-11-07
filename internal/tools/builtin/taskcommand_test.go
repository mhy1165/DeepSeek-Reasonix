package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/contract/tool"
)

type recordingTaskCommandRegistrar struct {
	got tool.TaskCommandRegistration
}

func (r *recordingTaskCommandRegistrar) RegisterTaskCommand(_ context.Context, cmd tool.TaskCommandRegistration) (string, error) {
	r.got = cmd
	return "auth", nil
}

func TestRegisterTaskCommandUsesOnlyJobAndMetadata(t *testing.T) {
	reg := &recordingTaskCommandRegistrar{}
	ctx := tool.WithTaskCommandRegistrar(context.Background(), reg)
	out, err := (registerTaskCommand{}).Execute(ctx, json.RawMessage(
		`{"job_id":"bash-31","name":"auth"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if reg.got.JobID != "bash-31" || reg.got.Name != "auth" {
		t.Fatalf("registration = %+v", reg.got)
	}
	if !strings.Contains(out, "auth") {
		t.Fatalf("output = %q", out)
	}
}

func TestRegisterTaskCommandRefusesWithoutRegistrar(t *testing.T) {
	if _, err := (registerTaskCommand{}).Execute(context.Background(), json.RawMessage(
		`{"job_id":"bash-31","name":"auth"}`,
	)); err == nil {
		t.Fatal("tool ran without a registrar")
	}
}

// fakeTaskCommandControl records the command ids the tool addressed, so the test
// asserts the tool never reaches for a job id.
type fakeTaskCommandControl struct {
	tasks     []tool.TaskCommandStatus
	started   []string
	restarted []string
	stopped   []string
}

func (f *fakeTaskCommandControl) RegisteredTasks(context.Context) ([]tool.TaskCommandStatus, error) {
	return f.tasks, nil
}

func (f *fakeTaskCommandControl) StartRegisteredTask(_ context.Context, id string) (tool.TaskCommandRun, error) {
	f.started = append(f.started, id)
	return tool.TaskCommandRun{CommandID: id, JobID: "bash-70"}, nil
}

func (f *fakeTaskCommandControl) RestartRegisteredTask(_ context.Context, id string) (tool.TaskCommandRun, error) {
	f.restarted = append(f.restarted, id)
	return tool.TaskCommandRun{CommandID: id, JobID: "bash-71", PreviousJobID: "bash-68"}, nil
}

func (f *fakeTaskCommandControl) StopRegisteredTask(_ context.Context, id string) (string, error) {
	f.stopped = append(f.stopped, id)
	return "bash-71", nil
}

func TestTaskCommandListsRegisteredCommandsWithTheirCurrentJob(t *testing.T) {
	fake := &fakeTaskCommandControl{tasks: []tool.TaskCommandStatus{
		{ID: "auth-9210-ab6aaa2d", Name: "auth", CWD: "backend/RuoYi-Cloud",
			Command: "java -jar ruoyi-auth.jar", JobID: "bash-65", JobStatus: "running", Available: true},
		{ID: "build-1a2b3c4d", Name: "构建 auth", CWD: "backend/RuoYi-Cloud",
			Command: "mvn -pl ruoyi-auth install", Available: false, UnavailableReason: "task command cwd: not a directory"},
	}}
	ctx := tool.WithTaskCommandControl(context.Background(), fake)
	out, err := (taskCommand{}).Execute(ctx, json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"auth-9210-ab6aaa2d", "bash-65", "running", "build-1a2b3c4d", "unavailable="} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}
}

func TestTaskCommandAddressesCommandsNotJobs(t *testing.T) {
	fake := &fakeTaskCommandControl{}
	ctx := tool.WithTaskCommandControl(context.Background(), fake)
	if _, err := (taskCommand{}).Execute(ctx, json.RawMessage(
		`{"action":"restart","command_id":"auth-9210-ab6aaa2d"}`,
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := (taskCommand{}).Execute(ctx, json.RawMessage(
		`{"action":"stop","command_id":"auth-9210-ab6aaa2d"}`,
	)); err != nil {
		t.Fatal(err)
	}
	if len(fake.restarted) != 1 || fake.restarted[0] != "auth-9210-ab6aaa2d" {
		t.Fatalf("restarted = %v", fake.restarted)
	}
	if len(fake.stopped) != 1 || fake.stopped[0] != "auth-9210-ab6aaa2d" {
		t.Fatalf("stopped = %v", fake.stopped)
	}
}

func TestTaskCommandRequiresACommandIDForActions(t *testing.T) {
	ctx := tool.WithTaskCommandControl(context.Background(), &fakeTaskCommandControl{})
	if _, err := (taskCommand{}).Execute(ctx, json.RawMessage(`{"action":"start"}`)); err == nil {
		t.Fatal("start ran without command_id")
	}
	if _, err := (taskCommand{}).Execute(ctx, json.RawMessage(`{"action":"restart"}`)); err == nil {
		t.Fatal("restart ran without command_id")
	}
	if _, err := (taskCommand{}).Execute(ctx, json.RawMessage(`{"action":"delete","command_id":"x"}`)); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestTaskCommandRefusesWithoutControl(t *testing.T) {
	if _, err := (taskCommand{}).Execute(context.Background(), json.RawMessage(`{"action":"list"}`)); err == nil {
		t.Fatal("tool ran without a control")
	}
	if (taskCommand{}).ProviderVisible(context.Background()) {
		t.Fatal("task_command reported visible without a control")
	}
}

func TestTaskCommandStartReportsTheNewJob(t *testing.T) {
	fake := &fakeTaskCommandControl{}
	ctx := tool.WithTaskCommandControl(context.Background(), fake)
	out, err := (taskCommand{}).Execute(ctx, json.RawMessage(
		`{"action":"start","command_id":"gateway-8082-2c090883"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.started) != 1 || fake.started[0] != "gateway-8082-2c090883" {
		t.Fatalf("started = %v", fake.started)
	}
	if !strings.Contains(out, "bash-70") || !strings.Contains(out, "command_id=") {
		t.Fatalf("start output = %q", out)
	}
}
