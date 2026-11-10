package tool

import "context"

type TaskCommandRegistration struct {
	JobID string `json:"job_id"`
	Name  string `json:"name"`
	// Overwrite is set only by the desktop after its explicit confirmation.
	Overwrite bool `json:"-"`
}

type TaskCommandRegistrar interface {
	RegisterTaskCommand(context.Context, TaskCommandRegistration) (string, error)
}

// TaskCommandStatus is one registered task as the agent sees it: the definition,
// plus the job currently carrying it out. JobID is empty when nothing is running,
// which is the state between a stop and the next start.
type TaskCommandStatus struct {
	ID      string
	Name    string
	CWD     string
	Command string
	JobID   string
	// JobStatus is the carrying job's status, empty when JobID is empty.
	JobStatus         string
	Available         bool
	UnavailableReason string
}

// TaskCommandRun reports the job a start or restart produced. PreviousJobID is
// the attempt a restart replaced and stays empty for a start.
type TaskCommandRun struct {
	CommandID     string
	JobID         string
	PreviousJobID string
}

// TaskCommandControl drives this conversation's registered task commands. A
// command is addressed by its stable registered id, never by a job id: every
// start allocates a new job, so a job id is stale the moment the task restarts.
type TaskCommandControl interface {
	RegisteredTasks(context.Context) ([]TaskCommandStatus, error)
	StartRegisteredTask(context.Context, string) (TaskCommandRun, error)
	RestartRegisteredTask(context.Context, string) (TaskCommandRun, error)
	// StopRegisteredTask returns the job id it stopped.
	StopRegisteredTask(context.Context, string) (string, error)
}

type taskCommandRegistrarKey struct{}

func WithTaskCommandRegistrar(ctx context.Context, registrar TaskCommandRegistrar) context.Context {
	return context.WithValue(ctx, taskCommandRegistrarKey{}, registrar)
}

func TaskCommandRegistrarFrom(ctx context.Context) (TaskCommandRegistrar, bool) {
	registrar, ok := ctx.Value(taskCommandRegistrarKey{}).(TaskCommandRegistrar)
	return registrar, ok && registrar != nil
}

type taskCommandControlKey struct{}

func WithTaskCommandControl(ctx context.Context, control TaskCommandControl) context.Context {
	return context.WithValue(ctx, taskCommandControlKey{}, control)
}

func TaskCommandControlFrom(ctx context.Context) (TaskCommandControl, bool) {
	control, ok := ctx.Value(taskCommandControlKey{}).(TaskCommandControl)
	return control, ok && control != nil
}
