package sessiontask

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryRoundTripAndRejectsAnotherSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.task-commands.json")
	reg := New("session-a")
	if err := reg.Upsert(Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: "backend/RuoYi-Cloud", Command: "java -jar auth.jar",
		Shell: Shell{
			Kind: "pwsh",
			Path: `C:\Program Files\PowerShell\7\pwsh.exe`,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Commands) != 1 || loaded.Commands[0].Command != "java -jar auth.jar" {
		t.Fatalf("loaded = %+v", loaded.Commands)
	}
	if loaded.Commands[0].CreatedAt.IsZero() || loaded.Commands[0].UpdatedAt.IsZero() {
		t.Fatal("timestamps were not assigned")
	}
	if _, err := Load(path, "session-b"); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("Load another session error = %v, want ErrSessionMismatch", err)
	}
}

func TestRegistryUpdatesOneIDAndRejectsTheSameRecipeUnderAnotherID(t *testing.T) {
	reg := New("session")
	base := Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: "backend/RuoYi-Cloud", Command: "java -jar auth.jar",
		Shell: Shell{Kind: "pwsh", Path: "pwsh.exe"},
	}
	if err := reg.Upsert(base); err != nil {
		t.Fatal(err)
	}
	base.Name = "auth service"
	if err := reg.Upsert(base); err != nil {
		t.Fatal(err)
	}
	if len(reg.Commands) != 1 || reg.Commands[0].Name != "auth service" {
		t.Fatalf("commands = %+v", reg.Commands)
	}

	duplicate := base
	duplicate.ID = "auth-copy"
	if err := reg.Upsert(duplicate); !errors.Is(err, ErrDuplicateCommand) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateCommand", err)
	}
}

func TestRegistrySaveRejectsAFileLoadCouldNotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.task-commands.json")
	reg := New("session")
	for i := range MaxCommands {
		id := fmt.Sprintf("command-%02d", i)
		if err := reg.Upsert(Command{
			ID: id, Name: id,
			Runner: "bash", CWD: ".",
			Command: id + strings.Repeat("x", MaxCommandBytes-len(id)),
			Shell:   Shell{Kind: "bash", Path: "/bin/bash"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Save(path); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("Save error = %v, want ErrInvalidCommand", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized registry reached disk: %v", err)
	}
}

func TestRegistryValidatesDefinitions(t *testing.T) {
	valid := Command{
		ID: "auth", Name: "auth", Runner: "bash",
		CWD: "backend/RuoYi-Cloud", Command: "java -jar auth.jar",
		Shell: Shell{Kind: "pwsh", Path: "pwsh.exe"},
	}
	tests := []struct {
		name string
		edit func(*Command)
	}{
		{"empty command", func(c *Command) { c.Command = " " }},
		{"empty cwd", func(c *Command) { c.CWD = "" }},
		{"absolute cwd", func(c *Command) { c.CWD = `C:\src` }},
		{"escaping cwd", func(c *Command) { c.CWD = "../outside" }},
		{"empty shell", func(c *Command) { c.Shell.Path = "" }},
		{"empty runner", func(c *Command) { c.Runner = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := New("session")
			c := valid
			tt.edit(&c)
			if err := reg.Upsert(c); !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("Upsert error = %v, want ErrInvalidCommand", err)
			}
		})
	}
}

// Registries written before the command type was dropped carry a "kind" field.
// Dropping the field must not strand those files: unknown keys are ignored, and
// the command keeps working as a plain replay recipe.
func TestLoadIgnoresALegacyKindField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.task-commands.json")
	legacy := []byte(`{"schemaVersion":1,"sessionId":"session","commands":[{"id":"auth","name":"auth","kind":"service","runner":"bash","cwd":".","command":"java -jar auth.jar","shell":{"kind":"powershell","path":"pwsh.exe","args":[]},"createdAt":"2026-11-04T00:00:00Z","updatedAt":"2026-11-04T00:00:00Z"}]}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(path, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Commands) != 1 || reg.Commands[0].ID != "auth" || reg.Commands[0].Command != "java -jar auth.jar" {
		t.Fatalf("legacy registry = %+v", reg.Commands)
	}
	if err := reg.Save(path); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustRead(t, path)), `"kind":"service"`) {
		t.Fatal("saving kept the retired command type")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLoadPreservesMalformedSidecar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.task-commands.json")
	original := []byte(`{"schemaVersion":1,"sessionId":"session","commands":[`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, "session"); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("Load malformed JSON error = %v, want ErrInvalidCommand", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("malformed sidecar changed: %q, %v", got, err)
	}
}

func TestCommandSignatureIncludesShellAndWorkingDirectory(t *testing.T) {
	cmd := Command{
		ID: "one", Name: "one", Runner: "bash",
		CWD: "frontend", Command: "npm run build",
		Shell: Shell{Kind: "bash", Path: "/bin/bash", Args: []string{"-l"}},
	}
	otherCWD := cmd
	otherCWD.CWD = "backend"
	otherShell := cmd
	otherShell.Shell.Path = `C:\Program Files\Git\bin\bash.exe`
	if commandSignature(cmd) == commandSignature(otherCWD) {
		t.Fatal("signature ignored cwd")
	}
	if commandSignature(cmd) == commandSignature(otherShell) {
		t.Fatal("signature ignored shell")
	}
	if !strings.Contains(commandSignature(cmd), "bash") {
		t.Fatal("signature omitted runner/shell identity")
	}
}
