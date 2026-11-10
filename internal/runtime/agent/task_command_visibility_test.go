package agent

import (
	"context"
	"testing"

	"reasonix/internal/contract/tool"
)

type stubTaskCommandControl struct{}

func (stubTaskCommandControl) RegisteredTasks(context.Context) ([]tool.TaskCommandStatus, error) {
	return nil, nil
}

func (stubTaskCommandControl) StartRegisteredTask(context.Context, string) (tool.TaskCommandRun, error) {
	return tool.TaskCommandRun{}, nil
}

func (stubTaskCommandControl) RestartRegisteredTask(context.Context, string) (tool.TaskCommandRun, error) {
	return tool.TaskCommandRun{}, nil
}

func (stubTaskCommandControl) StopRegisteredTask(context.Context, string) (string, error) {
	return "", nil
}

type stubTaskCommandRegistrar struct{}

func (stubTaskCommandRegistrar) RegisterTaskCommand(context.Context, tool.TaskCommandRegistration) (string, error) {
	return "", nil
}

// providerVisibleNames is what the model actually receives as its tool list.
func providerVisibleNames(ctx context.Context) map[string]bool {
	reg := tool.NewRegistry()
	for _, builtin := range tool.Builtins() {
		reg.Add(builtin)
	}
	out := map[string]bool{}
	for _, schema := range reg.ProviderSchemas(ctx) {
		out[schema.Name] = true
	}
	return out
}

// The run context is what builds the provider schema, so a capability stamped
// only on the per-call context is invisible to the model and every call to it
// comes back as a refusal. That is exactly how task_command shipped dead once.
func TestRunContextMakesTaskCommandToolsProviderVisible(t *testing.T) {
	a := &Agent{}
	a.svc.taskCommands = stubTaskCommandRegistrar{}
	a.svc.taskCommandControl = stubTaskCommandControl{}

	names := providerVisibleNames(a.withAgentContext(context.Background()))
	for _, want := range []string{"task_command", "register_task_command"} {
		if !names[want] {
			t.Fatalf("%s is not in the schema the model receives", want)
		}
	}
}

// A sub-agent must not inherit the parent's registry access through the context
// it was handed, which is why the bindings are stamped as an explicit absence.
func TestSubagentRunContextDropsTaskCommandTools(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{subagentDepth: 1}}
	a.svc.taskCommands = stubTaskCommandRegistrar{}
	a.svc.taskCommandControl = stubTaskCommandControl{}

	parent := tool.WithTaskCommandControl(
		tool.WithTaskCommandRegistrar(context.Background(), stubTaskCommandRegistrar{}),
		stubTaskCommandControl{},
	)
	names := providerVisibleNames(a.withAgentContext(parent))
	for _, hidden := range []string{"task_command", "register_task_command"} {
		if names[hidden] {
			t.Fatalf("sub-agent received %s", hidden)
		}
	}
}
