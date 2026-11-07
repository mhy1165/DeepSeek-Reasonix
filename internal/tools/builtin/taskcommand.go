package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"reasonix/internal/contract/tool"
)

func init() {
	tool.RegisterBuiltin(registerTaskCommand{})
	tool.RegisterBuiltin(taskCommand{})
}

type registerTaskCommand struct{}

func (registerTaskCommand) Name() string { return "register_task_command" }

func (registerTaskCommand) Description() string {
	return "Save a running or completed background job as a replayable task command. The host copies the command, working directory, runner, and shell from the job; give only a display name. Requires confirmation."
}

func (registerTaskCommand) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string","description":"job whose recipe to save"},"name":{"type":"string","description":"display name"}},"required":["job_id","name"]}`)
}

func (registerTaskCommand) ReadOnly() bool { return false }

func (registerTaskCommand) ProviderVisible(ctx context.Context) bool {
	_, ok := tool.TaskCommandRegistrarFrom(ctx)
	return ok
}

func (registerTaskCommand) Unavailable(context.Context) tool.Refusal {
	return tool.Refusal{
		Code:    "task_command.registration_unavailable",
		Message: "task command registration is unavailable in this context",
	}
}

func (registerTaskCommand) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var req tool.TaskCommandRegistration
	if err := json.Unmarshal(args, &req); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if req.JobID == "" || req.Name == "" {
		return "", fmt.Errorf("job_id and name are required")
	}
	registrar, ok := tool.TaskCommandRegistrarFrom(ctx)
	if !ok {
		return "", fmt.Errorf("task command registration is unavailable in this context")
	}
	id, err := registrar.RegisterTaskCommand(ctx, req)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Registered session task command %q as %q.", req.Name, id), nil
}

// taskCommand operates the conversation's registered task commands. A job id is
// not a stable handle for a task — every start allocates a new bash-N — so this
// tool addresses the registered command id instead.

type taskCommand struct{ noJobs }

func (taskCommand) Name() string { return "task_command" }

func (taskCommand) Description() string {
	return "Operate registered task commands by command_id: list, start, restart, stop. A job id goes stale on restart; a command id does not. Output: bash_output(command_id=...)."
}

func (taskCommand) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list","start","restart","stop"],"description":"list|start|restart|stop"},"command_id":{"type":"string","description":"command id from action=list; required for start/restart/stop"}},"required":["action"]}`)
}

func (taskCommand) ReadOnly() bool { return false }

func (taskCommand) ProviderVisible(ctx context.Context) bool {
	_, ok := tool.TaskCommandControlFrom(ctx)
	return ok
}

func (taskCommand) Unavailable(context.Context) tool.Refusal {
	return tool.Refusal{
		Code:    "task_command.unavailable",
		Message: "registered task commands are unavailable in this context",
	}
}

func (taskCommand) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var req struct {
		Action    string `json:"action"`
		CommandID string `json:"command_id"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	control, ok := tool.TaskCommandControlFrom(ctx)
	if !ok {
		return "", fmt.Errorf("registered task commands are unavailable in this context")
	}
	switch strings.TrimSpace(req.Action) {
	case "list":
		return listRegisteredTasks(ctx, control)
	case "start", "restart", "stop":
	default:
		return "", fmt.Errorf("action must be list, start, restart, or stop")
	}
	id := strings.TrimSpace(req.CommandID)
	if id == "" {
		return "", fmt.Errorf("command_id is required for action=%s; call action=list first", req.Action)
	}
	switch req.Action {
	case "start":
		run, err := control.StartRegisteredTask(ctx, id)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Started %s as %s. Read its output with bash_output(command_id=%q).",
			run.CommandID, run.JobID, run.CommandID), nil
	case "restart":
		run, err := control.RestartRegisteredTask(ctx, id)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Restarted %s: %s replaced by %s. Read its output with bash_output(command_id=%q).",
			run.CommandID, run.PreviousJobID, run.JobID, run.CommandID), nil
	default:
		jobID, err := control.StopRegisteredTask(ctx, id)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Stopped %s for %s. Start it again with task_command(action=start, command_id=%q).",
			jobID, id, id), nil
	}
}

func listRegisteredTasks(ctx context.Context, control tool.TaskCommandControl) (string, error) {
	tasks, err := control.RegisteredTasks(ctx)
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "No task commands are registered for this conversation.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d registered task command(s):", len(tasks))
	for _, task := range tasks {
		state := "not running"
		switch {
		case task.JobID != "" && task.JobStatus != "":
			state = task.JobID + " " + task.JobStatus
		case task.JobID != "":
			state = task.JobID
		}
		fmt.Fprintf(&b, "\n- command_id=%s name=%q cwd=%s state=%s",
			task.ID, task.Name, displayCWD(task.CWD), state)
		if task.Command != "" {
			fmt.Fprintf(&b, " command=%q", clipText(task.Command, 160))
		}
		if !task.Available {
			fmt.Fprintf(&b, " unavailable=%q", task.UnavailableReason)
		}
	}
	b.WriteString("\nUse bash_output(command_id=...) for output; restart replaces whatever is running now.")
	return b.String(), nil
}

func displayCWD(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return "."
	}
	return cwd
}

// clipText keeps a listing readable: a task command may be a whole shell
// script, and the list is for choosing a command id, not for replaying it.
func clipText(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "…"
}
