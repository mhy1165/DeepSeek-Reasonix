package jobs

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/contract/event"
)

type restartCompletionObserver chan CompletionEvent

func (o restartCompletionObserver) OnJobCompletion(_ context.Context, ev CompletionEvent) error {
	o <- ev
	return nil
}

func TestRestartForSessionStopsTheOldJobBeforeStartingTheReplacement(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	firstStarted := make(chan struct{})
	firstStop := make(chan struct{})
	secondStarted := make(chan struct{})
	secondStop := make(chan struct{})
	runNumber := 0

	restartableRun := func(ctx context.Context, _ io.Writer) (string, error) {
		runNumber++
		switch runNumber {
		case 1:
			close(firstStarted)
			<-firstStop
		case 2:
			close(secondStarted)
			select {
			case <-secondStop:
			case <-ctx.Done():
			}
		}
		return "", nil
	}
	first := m.StartForSession("session", "bash", "dev server", restartableRun, WithRestart(restartableRun))
	<-firstStarted

	type restartResult struct {
		view View
		err  error
	}
	restarted := make(chan restartResult, 1)
	go func() {
		view, err := m.RestartForSession("session", first.ID)
		restarted <- restartResult{view: view, err: err}
	}()

	select {
	case <-secondStarted:
		t.Fatal("replacement started while the old job was still running")
	case <-time.After(30 * time.Millisecond):
	}

	close(firstStop)
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement did not start after the old job exited")
	}

	got := <-restarted
	if got.err != nil {
		t.Fatalf("RestartForSession: %v", got.err)
	}
	if got.view.ID == "" || got.view.ID == first.ID {
		t.Fatalf("replacement id = %q, want a new non-empty id", got.view.ID)
	}
	if !got.view.Restartable {
		t.Fatal("replacement lost its restart capability")
	}

	close(secondStop)
}

func TestRestartForSessionRefusesAJobWithNoRestartRecipe(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	started := make(chan struct{})
	job := m.StartForSession("session", "task", "sub-agent", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		return "", nil
	})
	<-started

	if _, err := m.RestartForSession("session", job.ID); !errors.Is(err, ErrRestartUnavailable) {
		t.Fatalf("RestartForSession error = %v, want ErrRestartUnavailable", err)
	}
}

func TestRestartForSessionRefusesAJobThatHasAlreadyEnded(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	job := m.StartForSession("session", "bash", "echo", func(context.Context, io.Writer) (string, error) {
		return "", nil
	}, WithRestart(func(context.Context, io.Writer) (string, error) {
		return "", nil
	}))
	<-job.done

	if _, err := m.RestartForSession("session", job.ID); !errors.Is(err, ErrRestartNotRunning) {
		t.Fatalf("RestartForSession error = %v, want ErrRestartNotRunning", err)
	}
}

func TestRestartForSessionDoesNotStartAReplacementWhenTheOldJobWillNotStop(t *testing.T) {
	m := NewManager(event.Discard)
	m.teardownGrace = 30 * time.Millisecond
	defer m.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	replacementStarted := make(chan struct{}, 1)
	var runs atomic.Int32
	run := func(context.Context, io.Writer) (string, error) {
		if runs.Add(1) == 1 {
			close(started)
			<-release
		} else {
			replacementStarted <- struct{}{}
		}
		return "", nil
	}
	job := m.StartForSession("session", "bash", "dev server", run, WithRestart(run))
	<-started

	if _, err := m.RestartForSession("session", job.ID); !errors.Is(err, ErrRestartTimeout) {
		t.Fatalf("RestartForSession error = %v, want ErrRestartTimeout", err)
	}
	select {
	case <-replacementStarted:
		t.Fatal("replacement started after the old job missed the restart deadline")
	default:
	}
	if !m.KillForSession("session", job.ID) {
		t.Fatal("a job that missed the restart deadline could no longer be stopped")
	}
	close(release)
	<-job.done
}

func TestRestartForSessionDoesNotQueueACompletionForTheStoppedAttempt(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	completed := make(restartCompletionObserver, 1)
	m.SetCompletionObserver(completed)

	started := make(chan struct{})
	stop := make(chan struct{})
	replacementRunning := make(chan struct{})
	runNumber := 0
	run := func(ctx context.Context, _ io.Writer) (string, error) {
		runNumber++
		if runNumber == 1 {
			close(started)
			select {
			case <-stop:
			case <-ctx.Done():
			}
			return "", nil
		}
		close(replacementRunning)
		<-ctx.Done()
		return "", nil
	}
	first := m.StartForSession("session", "bash", "dev server", run, WithRestart(run))
	<-started

	next, err := m.RestartForSession("session", first.ID)
	if err != nil {
		t.Fatalf("RestartForSession: %v", err)
	}
	<-replacementRunning
	select {
	case ev := <-completed:
		t.Fatalf("restart queued a completion for the replaced attempt: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
	if !m.KillForSession("session", next.ID) {
		t.Fatal("replacement was not running")
	}
}
