package agent

import (
	"context"

	"reasonix/internal/contract/tool"
)

// SetTaskCommandRegistrar installs the parent session's task-command writer
// during assembly, before any run starts. Sub-agent contexts deliberately do
// not receive it.
func (a *Agent) SetTaskCommandRegistrar(registrar tool.TaskCommandRegistrar) {
	if a == nil {
		return
	}
	a.svc.taskCommands = registrar
}

// SetTaskCommandControl installs the parent session's task-command driver
// (list/start/restart/stop) during assembly. Sub-agent contexts deliberately do
// not receive it, so a child agent cannot start or stop the conversation's
// registered services.
func (a *Agent) SetTaskCommandControl(control tool.TaskCommandControl) {
	if a == nil {
		return
	}
	a.svc.taskCommandControl = control
}

// taskCommandContext stamps the bindings onto the turn context, which is what
// builds the model's schema. A sub-agent stamps an explicit absence instead, so
// a registry it was handed cannot reach the parent's commands.
func (a *Agent) taskCommandContext(ctx context.Context) context.Context {
	if a.subagentDepth == 0 && a.svc.taskCommands != nil {
		ctx = tool.WithTaskCommandRegistrar(ctx, a.svc.taskCommands)
	} else {
		ctx = tool.WithTaskCommandRegistrar(ctx, nil)
	}
	if a.subagentDepth == 0 && a.svc.taskCommandControl != nil {
		return tool.WithTaskCommandControl(ctx, a.svc.taskCommandControl)
	}
	return tool.WithTaskCommandControl(ctx, nil)
}
