package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"reasonix/internal/contract/event"
	"reasonix/internal/contract/tool"
	"reasonix/internal/safety/sandbox"
	"reasonix/internal/state/sessionstore"
	"reasonix/internal/state/sessiontask"
	"reasonix/internal/state/store"
	"reasonix/internal/tools/jobs"
)

var (
	ErrTaskCommandNotFound         = errors.New("session task command not found")
	ErrTaskCommandRunning          = errors.New("session task command is already running")
	ErrTaskCommandUnavailable      = errors.New("session task command execution is unavailable")
	ErrTaskCommandInvalid          = errors.New("session task command is invalid")
	ErrTaskCommandExists           = errors.New("session task command already exists")
	ErrTaskCommandNoDurableSession = errors.New("session task commands require a durable session")
)

type TaskCommandStarter func(context.Context, *jobs.Manager, string, sessiontask.Command) (jobs.View, error)
type TaskCommandInspector func(sessiontask.Command) string

type TaskCommandView struct {
	sessiontask.Command
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailableReason,omitempty"`
}

type taskCommandState struct {
	mu        sync.Mutex
	path      string
	sessionID string
	registry  *sessiontask.Registry
	loadErr   error
}

func (c *Controller) TaskCommands() []TaskCommandView {
	reg, err := c.loadTaskCommands()
	if err != nil {
		return nil
	}
	out := make([]TaskCommandView, len(reg.Commands))
	for i := range reg.Commands {
		command := cloneTaskCommand(reg.Commands[i])
		reason := ""
		if c.taskCmdInspector != nil {
			reason = strings.TrimSpace(c.taskCmdInspector(command))
		}
		out[i] = TaskCommandView{
			Command:           command,
			Available:         reason == "",
			UnavailableReason: reason,
		}
	}
	return out
}

func (c *Controller) TaskCommandsError() string {
	_, err := c.loadTaskCommands()
	switch {
	case err == nil, errors.Is(err, ErrTaskCommandNoDurableSession):
		return ""
	default:
		return "task_command.registry_unavailable"
	}
}

func (c *Controller) StartTaskCommand(ctx context.Context, id string) (jobs.View, error) {
	if c == nil || c.jobs == nil || c.taskCommandStarter == nil {
		return jobs.View{}, ErrTaskCommandUnavailable
	}
	reg, err := c.loadTaskCommands()
	if err != nil {
		return jobs.View{}, err
	}
	command, ok := taskCommandByID(reg.Commands, id)
	if !ok {
		return jobs.View{}, ErrTaskCommandNotFound
	}
	release, ok := c.jobs.ReserveCommandStartForSession(c.parentSessionID(), id)
	if !ok {
		return jobs.View{}, ErrTaskCommandRunning
	}
	defer release()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = sandbox.WithEscapeApprover(ctx, sandboxEscapeApprover{c: c})
	return c.taskCommandStarter(ctx, c.jobs, c.parentSessionID(), cloneTaskCommand(command))
}

func (c *Controller) RegisterTaskCommand(ctx context.Context, req tool.TaskCommandRegistration) (string, error) {
	if c == nil || c.jobs == nil {
		return "", ErrTaskCommandUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	recipe, ok := c.jobs.RecipeForSession(c.parentSessionID(), req.JobID)
	if !ok {
		return "", fmt.Errorf("%w: no replayable recipe for %s", ErrTaskCommandInvalid, req.JobID)
	}
	if _, ok := c.jobs.ViewForSession(c.parentSessionID(), req.JobID); !ok {
		return "", fmt.Errorf("%w: no job %s", ErrTaskCommandInvalid, req.JobID)
	}
	reg, err := c.loadTaskCommands()
	if err != nil {
		return "", err
	}
	next := cloneRegistry(reg)
	id := taskCommandID(req.Name, recipe.Command)
	if _, exists := taskCommandByID(reg.Commands, id); exists && !req.Overwrite {
		return "", fmt.Errorf("%w: %s", ErrTaskCommandExists, id)
	}
	command := sessiontask.Command{
		ID: id, Name: strings.TrimSpace(req.Name),
		Runner: recipe.Runner, CWD: recipe.CWD, Command: recipe.Command,
		Shell: sessiontask.Shell{
			Kind: recipe.Shell.Kind, Path: recipe.Shell.Path,
			Args: append([]string(nil), recipe.Shell.Args...), Version: recipe.Shell.Version,
			Drives: recipe.Shell.Drives,
		},
		SourceJobID: req.JobID,
	}
	if err := next.Upsert(command); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := c.commitTaskCommandRegistration(req.JobID, id, reg, next); err != nil {
		return "", err
	}
	c.sink.Emit(event.Event{
		Kind: event.Notice, Level: event.LevelInfo,
		Text: "Registered session task command " + command.Name + " (" + id + ").",
	})
	return id, nil
}

func (c *Controller) commitTaskCommandRegistration(jobID, commandID string, previous, next *sessiontask.Registry) error {
	if err := c.saveTaskCommands(next); err != nil {
		return err
	}
	if c.jobs.SetCommandID(c.parentSessionID(), jobID, commandID) {
		return nil
	}
	if err := c.saveTaskCommands(previous); err != nil {
		return errors.Join(fmt.Errorf("%w: job disappeared before association", ErrTaskCommandInvalid), err)
	}
	return fmt.Errorf("%w: job disappeared before association", ErrTaskCommandInvalid)
}

func (c *Controller) loadTaskCommands() (*sessiontask.Registry, error) {
	path := c.SessionPath()
	if strings.TrimSpace(path) == "" {
		return nil, ErrTaskCommandNoDurableSession
	}
	sessionID := sessionstore.BranchID(path)
	c.taskCommands.mu.Lock()
	defer c.taskCommands.mu.Unlock()
	if c.taskCommands.path == path && c.taskCommands.registry != nil {
		return c.taskCommands.registry, c.taskCommands.loadErr
	}
	reg, err := sessiontask.Load(store.SessionTaskCommands(path), sessionID)
	c.taskCommands.path = path
	c.taskCommands.sessionID = sessionID
	c.taskCommands.registry = reg
	c.taskCommands.loadErr = err
	return reg, err
}

func (c *Controller) saveTaskCommands(reg *sessiontask.Registry) error {
	path := c.SessionPath()
	if strings.TrimSpace(path) == "" {
		return ErrTaskCommandNoDurableSession
	}
	if err := reg.Save(store.SessionTaskCommands(path)); err != nil {
		return err
	}
	c.taskCommands.mu.Lock()
	c.taskCommands.path = path
	c.taskCommands.sessionID = sessionstore.BranchID(path)
	c.taskCommands.registry = cloneRegistry(reg)
	c.taskCommands.loadErr = nil
	c.taskCommands.mu.Unlock()
	return nil
}

var taskCommandSlug = regexp.MustCompile(`[^a-z0-9]+`)

func taskCommandID(name, command string) string {
	slug := strings.Trim(taskCommandSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		slug = "task"
	}
	sum := sha256.Sum256([]byte(name + "\x00" + command))
	return slug + "-" + hex.EncodeToString(sum[:4])
}

func taskCommandByID(commands []sessiontask.Command, id string) (sessiontask.Command, bool) {
	for _, command := range commands {
		if command.ID == id {
			return command, true
		}
	}
	return sessiontask.Command{}, false
}

func cloneRegistry(reg *sessiontask.Registry) *sessiontask.Registry {
	next := *reg
	next.Commands = make([]sessiontask.Command, len(reg.Commands))
	for i := range reg.Commands {
		next.Commands[i] = cloneTaskCommand(reg.Commands[i])
	}
	return &next
}

func cloneTaskCommand(command sessiontask.Command) sessiontask.Command {
	command.Shell.Args = append([]string(nil), command.Shell.Args...)
	return command
}

// RegisteredTasks lists this conversation's task commands together with the job
// currently carrying each one. The agent gets the stable command id here, so it
// never has to guess at a job number that changes on every start.
func (c *Controller) RegisteredTasks(ctx context.Context) ([]tool.TaskCommandStatus, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if c == nil || c.jobs == nil {
		return nil, ErrTaskCommandUnavailable
	}
	views := c.TaskCommands()
	session := c.parentSessionID()
	out := make([]tool.TaskCommandStatus, 0, len(views))
	for _, view := range views {
		// TaskCommandView embeds sessiontask.Command, so the definition is reached
		// through view.Command rather than through promoted fields.
		definition := view.Command
		status := tool.TaskCommandStatus{
			ID:                definition.ID,
			Name:              definition.Name,
			CWD:               definition.CWD,
			Command:           definition.Command,
			Available:         view.Available,
			UnavailableReason: view.UnavailableReason,
		}
		if jobID, ok := c.jobs.CurrentIDForCommandSession(session, view.ID); ok {
			status.JobID = jobID
			if current, ok := c.jobs.ViewForSession(session, jobID); ok {
				status.JobStatus = current.Status
			}
		}
		out = append(out, status)
	}
	return out, nil
}

// StartRegisteredTask launches a registered command through the same host-owned
// recipe the desktop uses, so an agent-started service is indistinguishable from
// one started by a person: same shell, same working directory, same single
// running slot.
func (c *Controller) StartRegisteredTask(ctx context.Context, id string) (tool.TaskCommandRun, error) {
	view, err := c.StartTaskCommand(ctx, id)
	if err != nil {
		return tool.TaskCommandRun{}, err
	}
	return tool.TaskCommandRun{CommandID: id, JobID: view.ID}, nil
}

// RestartRegisteredTask replaces whatever attempt carries the command: in-place
// handover when the job kept its recipe, otherwise stop and start from the
// definition, which is a recipe too. Which of the two happens is a host detail.
func (c *Controller) RestartRegisteredTask(ctx context.Context, id string) (tool.TaskCommandRun, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return tool.TaskCommandRun{}, err
	}
	if c == nil || c.jobs == nil {
		return tool.TaskCommandRun{}, ErrTaskCommandUnavailable
	}
	reg, err := c.loadTaskCommands()
	if err != nil {
		return tool.TaskCommandRun{}, err
	}
	command, ok := taskCommandByID(reg.Commands, id)
	if !ok {
		return tool.TaskCommandRun{}, fmt.Errorf("%w: %s", ErrTaskCommandNotFound, id)
	}
	if c.taskCmdInspector != nil {
		if reason := strings.TrimSpace(c.taskCmdInspector(cloneTaskCommand(command))); reason != "" {
			return tool.TaskCommandRun{}, fmt.Errorf("%w: %s", ErrTaskCommandUnavailable, reason)
		}
	}
	previous, ok := c.jobs.CurrentIDForCommandSession(c.parentSessionID(), id)
	if !ok {
		return tool.TaskCommandRun{}, fmt.Errorf("%w: %s has no job to restart; start it instead", ErrTaskCommandNotFound, id)
	}
	view, restartErr := c.RestartJob(previous)
	if restartErr == nil {
		return tool.TaskCommandRun{CommandID: id, JobID: view.ID, PreviousJobID: previous}, nil
	}
	if !errors.Is(restartErr, jobs.ErrRestartUnavailable) && !errors.Is(restartErr, jobs.ErrRestartNotRunning) {
		return tool.TaskCommandRun{}, restartErr
	}
	// No in-place handover available: the old attempt still has to leave the
	// command's single running slot before the definition can start again.
	_ = c.CancelJob(previous)
	deadline := time.Now().Add(taskCommandRestartGrace)
	for {
		run, startErr := c.StartRegisteredTask(ctx, id)
		if startErr == nil {
			run.PreviousJobID = previous
			return run, nil
		}
		if !errors.Is(startErr, ErrTaskCommandRunning) || time.Now().After(deadline) {
			return tool.TaskCommandRun{}, startErr
		}
		select {
		case <-ctx.Done():
			return tool.TaskCommandRun{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// taskCommandRestartGrace bounds how long a restart waits for the old process
// tree to leave the command's running slot before starting again.
const taskCommandRestartGrace = 8 * time.Second

// StopRegisteredTask stops whatever job currently carries the command and
// returns that job id. Stopping the current attempt, rather than a remembered job
// id, is what keeps this working after a person restarts the task by hand.
func (c *Controller) StopRegisteredTask(ctx context.Context, id string) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	if c == nil || c.jobs == nil {
		return "", ErrTaskCommandUnavailable
	}
	reg, err := c.loadTaskCommands()
	if err != nil {
		return "", err
	}
	if _, ok := taskCommandByID(reg.Commands, id); !ok {
		return "", fmt.Errorf("%w: %s", ErrTaskCommandNotFound, id)
	}
	jobID, ok := c.jobs.CurrentIDForCommandSession(c.parentSessionID(), id)
	if !ok {
		return "", fmt.Errorf("%w: %s has no job to stop", ErrTaskCommandNotFound, id)
	}
	if !c.CancelJob(jobID) {
		return "", fmt.Errorf("%w: job %s could not be stopped", ErrTaskCommandUnavailable, jobID)
	}
	return jobID, nil
}

// RemoveTaskCommand deletes a registered definition. A command with a job still
// running is refused: deleting the recipe would leave a live process behind a
// row the panel no longer draws, so the person stops it first, then deletes.
func (c *Controller) RemoveTaskCommand(ctx context.Context, id string) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if c == nil || c.jobs == nil {
		return ErrTaskCommandUnavailable
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: empty command id", ErrTaskCommandInvalid)
	}
	reg, err := c.loadTaskCommands()
	if err != nil {
		return err
	}
	if _, ok := taskCommandByID(reg.Commands, id); !ok {
		return fmt.Errorf("%w: %s", ErrTaskCommandNotFound, id)
	}
	if jobID, ok := c.jobs.CurrentIDForCommandSession(c.parentSessionID(), id); ok {
		if view, ok := c.jobs.ViewForSession(c.parentSessionID(), jobID); ok && view.Status == string(jobs.Running) {
			return fmt.Errorf("%w: %s is still running as %s", ErrTaskCommandRunning, id, jobID)
		}
	}
	next := cloneRegistry(reg)
	if !next.Remove(id) {
		return fmt.Errorf("%w: %s", ErrTaskCommandNotFound, id)
	}
	if err := c.saveTaskCommands(next); err != nil {
		return err
	}
	c.sink.Emit(event.Event{
		Kind: event.Notice, Level: event.LevelInfo,
		Text: "Removed session task command " + id + ".",
	})
	return nil
}

var (
	_ tool.TaskCommandRegistrar = (*Controller)(nil)
	_ tool.TaskCommandControl   = (*Controller)(nil)
)
