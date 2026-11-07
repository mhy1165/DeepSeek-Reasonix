package control

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/event"
	"reasonix/internal/state/sessionstore"
	"reasonix/internal/tools/jobs"
)

func TestRestartJobStaysInsideTheOwningSession(t *testing.T) {
	manager := jobs.NewManager(event.Discard)
	t.Cleanup(manager.Close)
	pathA := filepath.Join(testenv.TempDir(t), "session-a.jsonl")
	pathB := filepath.Join(testenv.TempDir(t), "session-b.jsonl")
	controllerA := New(Options{Jobs: manager})
	controllerB := New(Options{Jobs: manager})
	controllerA.sessionPath = pathA
	controllerB.sessionPath = pathB

	run := func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", nil
	}
	job := manager.StartForSession(sessionstore.BranchID(pathA), "bash", "dev server",
		run, jobs.WithRestart(run))

	if _, err := controllerB.RestartJob(job.ID); !errors.Is(err, jobs.ErrRestartNotRunning) {
		t.Fatalf("controller B restart error = %v, want the job to be invisible", err)
	}
	next, err := controllerA.RestartJob(job.ID)
	if err != nil {
		t.Fatalf("controller A RestartJob: %v", err)
	}
	if next.ID == job.ID || !next.Restartable {
		t.Fatalf("replacement = %+v, want a new restartable job", next)
	}
	_ = manager.KillForSession(sessionstore.BranchID(pathA), next.ID)
}
