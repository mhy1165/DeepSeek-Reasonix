package jobs

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrRestartNotRunning  = errors.New("background job is not running")
	ErrRestartUnavailable = errors.New("background job has no restart recipe")
	ErrRestartInProgress  = errors.New("background job restart is already in progress")
	ErrRestartTimeout     = errors.New("old background job did not stop before the restart deadline")
)

// RestartForSession stops a running job, waits for its process tree to unwind,
// then starts a new job from the same host-owned recipe. The replacement gets a
// fresh id and output artifact; presentation and the restart capability carry
// forward so the new job can be restarted again.
func (m *Manager) RestartForSession(parentSession, id string) (View, error) {
	j := m.get(parentSession, id)
	if j == nil {
		return View{}, fmt.Errorf("%w: %s", ErrRestartNotRunning, id)
	}

	j.mu.Lock()
	if j.status != Running {
		select {
		case <-j.done:
			j.mu.Unlock()
			return View{}, fmt.Errorf("%w: %s", ErrRestartNotRunning, id)
		default:
			j.mu.Unlock()
			return View{}, fmt.Errorf("%w: %s", ErrRestartInProgress, id)
		}
	}
	if j.launch.restart.run == nil {
		j.mu.Unlock()
		return View{}, fmt.Errorf("%w: %s", ErrRestartUnavailable, id)
	}
	if j.launch.restart.requested {
		j.mu.Unlock()
		return View{}, fmt.Errorf("%w: %s", ErrRestartInProgress, id)
	}
	j.launch.restart.requested = true
	recipe := j.launch.restart.run
	registration := j.launch.registration
	kind, label := j.Kind, j.Label
	present := j.launch.present
	cancel, done := j.cancel, j.done
	j.status = Killed
	j.mu.Unlock()

	cancel()
	timer := time.NewTimer(max(0, m.teardownGrace))
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		j.mu.Lock()
		select {
		case <-done:
			j.mu.Unlock()
		default:
			j.launch.restart.requested = false
			j.status = Running
			j.mu.Unlock()
			return View{}, fmt.Errorf("%w: %s", ErrRestartTimeout, id)
		}
	}

	next := m.StartForSession(parentSession, kind, label, recipe,
		WithPresentation(present.Description, present.Detail),
		WithRestart(recipe),
		WithLaunchRecipe(registration.recipe),
		WithCommandID(registration.commandID),
	)
	j.mu.Lock()
	j.launch.replacedBy = next.ID
	j.mu.Unlock()
	return next.view(), nil
}

func (j *Job) view() View {
	j.mu.Lock()
	defer j.mu.Unlock()
	return View{
		ID: j.ID, Kind: j.Kind, Label: j.Label,
		Description: j.launch.present.Description, Detail: j.launch.present.Detail,
		Status: string(j.status), StartedAt: j.times.started,
		CommandID:   j.launch.registration.commandID,
		Restartable: j.launch.restart.run != nil,
		Recipe:      launchRecipePointer(j.launch.registration.recipe),
	}
}
