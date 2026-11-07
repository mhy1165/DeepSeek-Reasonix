package jobs

import (
	"strings"
	"sync"
)

type ShellRecipe struct {
	Kind    string   `json:"kind"`
	Path    string   `json:"path"`
	Args    []string `json:"args"`
	Version string   `json:"version,omitempty"`
	Drives  string   `json:"drives,omitempty"`
}

type LaunchRecipe struct {
	Runner  string      `json:"runner"`
	CWD     string      `json:"cwd"`
	Command string      `json:"command"`
	Shell   ShellRecipe `json:"shell"`
}

type registrationState struct {
	recipe    LaunchRecipe
	commandID string
}

// WithLaunchRecipe records the host-owned launch semantics a person may later
// save as a session task. Presentation text is never used as this recipe.
func WithLaunchRecipe(recipe LaunchRecipe) StartOption {
	return func(j *Job) {
		recipe.Shell.Args = append([]string(nil), recipe.Shell.Args...)
		j.launch.registration.recipe = recipe
	}
}

// WithCommandID links a job to the session task command that launched it.
func WithCommandID(id string) StartOption {
	return func(j *Job) {
		j.launch.registration.commandID = id
	}
}

// RecipeForSession returns a copy of a job's replayable recipe. Restart aliases
// resolve to the current attempt.
func (m *Manager) RecipeForSession(parentSession, id string) (LaunchRecipe, bool) {
	j := m.get(parentSession, id)
	if j == nil {
		return LaunchRecipe{}, false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return usableLaunchRecipe(j.launch.registration.recipe)
}

func usableLaunchRecipe(recipe LaunchRecipe) (LaunchRecipe, bool) {
	if recipe.Runner == "" || recipe.Command == "" || recipe.Shell.Path == "" {
		return LaunchRecipe{}, false
	}
	recipe.Shell.Args = append([]string(nil), recipe.Shell.Args...)
	return recipe, true
}

func launchRecipePointer(recipe LaunchRecipe) *LaunchRecipe {
	recipe, ok := usableLaunchRecipe(recipe)
	if !ok {
		return nil
	}
	return &recipe
}

// SetCommandID links an already-running ad hoc job to its saved definition.
func (m *Manager) SetCommandID(parentSession, id, commandID string) bool {
	j := m.get(parentSession, id)
	if j == nil {
		return false
	}
	j.mu.Lock()
	j.launch.registration.commandID = commandID
	j.mu.Unlock()
	return true
}

// ViewForSession returns one job snapshot, including completed jobs still in
// the manager's table.
func (m *Manager) ViewForSession(parentSession, id string) (View, bool) {
	j := m.get(parentSession, id)
	if j == nil {
		return View{}, false
	}
	return j.view(), true
}

// ReserveCommandStartForSession atomically reserves the single running slot of
// a registered command. A stopped job still occupies the slot until its process
// tree has left, matching the running surface the desktop displays.
func (m *Manager) ReserveCommandStartForSession(parentSession, commandID string) (release func(), ok bool) {
	if strings.TrimSpace(commandID) == "" {
		return func() {}, false
	}
	parentSession = strings.TrimSpace(parentSession)
	key := jobKey(parentSession, commandID)
	m.mu.Lock()
	for _, jobKey := range m.order {
		j := m.jobs[jobKey]
		if j == nil || !sessionMatches(parentSession, j.SessionID) {
			continue
		}
		select {
		case <-j.done:
			continue
		default:
		}
		j.mu.Lock()
		linked := j.launch.registration.commandID == commandID
		j.mu.Unlock()
		if linked {
			m.mu.Unlock()
			return func() {}, false
		}
	}
	if m.commandStarts[key] {
		m.mu.Unlock()
		return func() {}, false
	}
	m.commandStarts[key] = true
	m.mu.Unlock()

	var once sync.Once
	release = func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.commandStarts, key)
			m.mu.Unlock()
		})
	}
	return release, true
}

// CurrentIDForCommandSession returns the newest job attached to a registered
// command, including a job a person started from the desktop.
func (m *Manager) CurrentIDForCommandSession(parentSession, commandID string) (string, bool) {
	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return "", false
	}
	parentSession = strings.TrimSpace(parentSession)
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.order) - 1; i >= 0; i-- {
		j := m.jobs[m.order[i]]
		if j == nil || !sessionMatches(parentSession, j.SessionID) {
			continue
		}
		j.mu.Lock()
		matched := j.launch.registration.commandID == commandID
		id := j.ID
		j.mu.Unlock()
		if matched {
			return id, true
		}
	}
	return "", false
}
