package jobs

import (
	"context"
	"io"
	"testing"

	"reasonix/internal/contract/event"
)

func TestRecipeForSessionCopiesTheHostOwnedLaunchRecipe(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	recipe := LaunchRecipe{
		Runner: "bash", CWD: ".", Command: "java -jar auth.jar",
		Shell: ShellRecipe{
			Kind: "pwsh", Path: `C:\Program Files\PowerShell\7\pwsh.exe`,
			Args: []string{"-NoProfile"}, Version: "7+",
		},
	}
	job := m.StartForSession("session", "bash", "auth", func(context.Context, io.Writer) (string, error) {
		return "", nil
	}, WithLaunchRecipe(recipe))
	<-job.done

	got, ok := m.RecipeForSession("session", job.ID)
	if !ok {
		t.Fatal("recipe not found")
	}
	if got.Command != recipe.Command || got.Shell.Path != recipe.Shell.Path || len(got.Shell.Args) != 1 {
		t.Fatalf("recipe = %+v", got)
	}
	if _, ok := m.RecipeForSession("other", job.ID); ok {
		t.Fatal("recipe crossed the session boundary")
	}
}

func TestRunningJobViewCarriesItsLaunchRecipe(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	started := make(chan struct{})
	recipe := LaunchRecipe{
		Runner: "bash", CWD: "backend/RuoYi-Cloud", Command: "java -jar auth.jar",
		Shell: ShellRecipe{Kind: "pwsh", Path: `C:\Program Files\PowerShell\7\pwsh.exe`, Version: "7+"},
	}
	job := m.StartForSession("session", "bash", "auth", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		return "", nil
	}, WithLaunchRecipe(recipe))
	<-started

	views := m.RunningForSession("session")
	if len(views) != 1 || views[0].ID != job.ID {
		t.Fatalf("running views = %+v", views)
	}
	got := views[0].Recipe
	if got == nil || got.CWD != recipe.CWD || got.Command != recipe.Command || got.Shell.Path != recipe.Shell.Path {
		t.Fatalf("view recipe = %+v, want %+v", got, recipe)
	}
}

func TestCommandIDSurvivesRestart(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()
	started := make(chan struct{})
	run := func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		return "", nil
	}
	recipe := LaunchRecipe{Runner: "bash", CWD: ".", Command: "go run server", Shell: ShellRecipe{Kind: "bash", Path: "bash"}}
	first := m.StartForSession("session", "bash", "server", run,
		WithLaunchRecipe(recipe), WithRestart(run), WithCommandID("server"))
	<-started

	next, err := m.RestartForSession("session", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.CommandID != "server" {
		t.Fatalf("restart command id = %q, want server", next.CommandID)
	}
	if !m.SetCommandID("session", next.ID, "server") {
		t.Fatal("replacement was not addressable")
	}
	_ = m.KillForSession("session", next.ID)
}
