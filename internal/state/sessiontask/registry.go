// Package sessiontask stores replayable task commands for one conversation.
package sessiontask

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"reasonix/internal/base/fileutil"
)

const (
	SchemaVersion   = 1
	MaxCommands     = 64
	MaxNameRunes    = 80
	MaxCommandBytes = 64 << 10
	MaxFileBytes    = 4 << 20
)

var (
	ErrSessionMismatch  = errors.New("task command registry belongs to another session")
	ErrDuplicateCommand = errors.New("task command recipe is already registered")
	ErrInvalidCommand   = errors.New("invalid task command")

	idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

type Shell struct {
	Kind    string   `json:"kind"`
	Path    string   `json:"path"`
	Args    []string `json:"args"`
	Version string   `json:"version,omitempty"`
	Drives  string   `json:"drives,omitempty"`
}

type Command struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Runner      string    `json:"runner"`
	CWD         string    `json:"cwd"`
	Command     string    `json:"command"`
	Shell       Shell     `json:"shell"`
	SourceJobID string    `json:"sourceJobId,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Registry struct {
	SchemaVersion int       `json:"schemaVersion"`
	SessionID     string    `json:"sessionId"`
	Commands      []Command `json:"commands"`
}

func New(sessionID string) *Registry {
	return &Registry{SchemaVersion: SchemaVersion, SessionID: sessionID, Commands: []Command{}}
}

func Load(path, sessionID string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return New(sessionID), nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("%w: file is too large", ErrInvalidCommand)
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("%w: decode task commands: %v", ErrInvalidCommand, err)
	}
	if reg.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: schemaVersion %d", ErrInvalidCommand, reg.SchemaVersion)
	}
	if reg.SessionID != sessionID {
		return nil, fmt.Errorf("%w: got %q, want %q", ErrSessionMismatch, reg.SessionID, sessionID)
	}
	if err := reg.validate(); err != nil {
		return nil, err
	}
	if reg.Commands == nil {
		reg.Commands = []Command{}
	}
	return &reg, nil
}

func (r *Registry) Save(path string) error {
	if r == nil || strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: empty registry or path", ErrInvalidCommand)
	}
	if err := r.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > MaxFileBytes {
		return fmt.Errorf("%w: file is too large", ErrInvalidCommand)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(path, data, 0o600)
}

func (r *Registry) Upsert(c Command) error {
	if r == nil {
		return fmt.Errorf("%w: nil registry", ErrInvalidCommand)
	}
	if err := validateCommand(c); err != nil {
		return err
	}
	now := time.Now()
	signature := commandSignature(c)
	for i := range r.Commands {
		existing := &r.Commands[i]
		if existing.ID != c.ID && commandSignature(*existing) == signature {
			return fmt.Errorf("%w: %s", ErrDuplicateCommand, existing.ID)
		}
	}
	for i := range r.Commands {
		if r.Commands[i].ID != c.ID {
			continue
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = r.Commands[i].CreatedAt
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		c.UpdatedAt = now
		r.Commands[i] = c
		return nil
	}
	if len(r.Commands) >= MaxCommands {
		return fmt.Errorf("%w: more than %d commands", ErrInvalidCommand, MaxCommands)
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	r.Commands = append(r.Commands, c)
	return nil
}

// Remove deletes one command by id and reports whether it was there. Deleting a
// definition never touches a running job: the job keeps its own artifact and
// stays stoppable from the job panel, and only the replay recipe goes.
func (r *Registry) Remove(id string) bool {
	if r == nil {
		return false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	kept := make([]Command, 0, len(r.Commands))
	removed := false
	for _, command := range r.Commands {
		if command.ID == id {
			removed = true
			continue
		}
		kept = append(kept, command)
	}
	if removed {
		r.Commands = kept
	}
	return removed
}

func (r *Registry) validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schemaVersion %d", ErrInvalidCommand, r.SchemaVersion)
	}
	if strings.TrimSpace(r.SessionID) == "" {
		return fmt.Errorf("%w: empty session id", ErrInvalidCommand)
	}
	if len(r.Commands) > MaxCommands {
		return fmt.Errorf("%w: more than %d commands", ErrInvalidCommand, MaxCommands)
	}
	ids := map[string]bool{}
	signatures := map[string]string{}
	for _, c := range r.Commands {
		if err := validateCommand(c); err != nil {
			return err
		}
		if ids[c.ID] {
			return fmt.Errorf("%w: duplicate id %q", ErrInvalidCommand, c.ID)
		}
		ids[c.ID] = true
		signature := commandSignature(c)
		if id, exists := signatures[signature]; exists {
			return fmt.Errorf("%w: %s", ErrDuplicateCommand, id)
		}
		signatures[signature] = c.ID
	}
	return nil
}

func validateCommand(c Command) error {
	if !idPattern.MatchString(c.ID) {
		return fmt.Errorf("%w: invalid id %q", ErrInvalidCommand, c.ID)
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(c.Name)); n == 0 || n > MaxNameRunes {
		return fmt.Errorf("%w: name must be 1-%d characters", ErrInvalidCommand, MaxNameRunes)
	}
	if strings.TrimSpace(c.Runner) == "" {
		return fmt.Errorf("%w: runner is required", ErrInvalidCommand)
	}
	rawCWD := strings.TrimSpace(c.CWD)
	cwd := filepath.Clean(rawCWD)
	if rawCWD == "" || filepath.IsAbs(cwd) || cwd == ".." || strings.HasPrefix(cwd, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: cwd must stay inside the workspace", ErrInvalidCommand)
	}
	if strings.TrimSpace(c.Command) == "" || len(c.Command) > MaxCommandBytes {
		return fmt.Errorf("%w: command must be 1-%d bytes", ErrInvalidCommand, MaxCommandBytes)
	}
	if strings.TrimSpace(c.Shell.Kind) == "" || strings.TrimSpace(c.Shell.Path) == "" {
		return fmt.Errorf("%w: shell kind and path are required", ErrInvalidCommand)
	}
	return nil
}

func commandSignature(c Command) string {
	return strings.Join([]string{
		c.Runner,
		c.Shell.Kind,
		c.Shell.Path,
		strings.Join(c.Shell.Args, "\x00"),
		c.Shell.Drives,
		filepath.Clean(c.CWD),
		c.Command,
	}, "\x00")
}
