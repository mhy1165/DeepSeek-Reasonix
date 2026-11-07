package storage

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/config"
)

// A plan answers "what would this cost" while the path is still being typed, so
// it must not touch the disk. It used to create the target to prove writability,
// which left a folder behind for every keystroke: D:\Documents\t, then te, then
// tes, none of them asked for.
func TestPlanMoveDoesNotCreateTheTarget(t *testing.T) {
	home := stateHome(t)
	seedState(t, home)
	target := filepath.Join(testenv.TempDir(t), "typed", "one", "word")

	plan := PlanMove(t.Context(), config.RootState, target)
	if !plan.OK() {
		t.Fatalf("plan refused: %+v", plan.Refusals)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), "one")); !os.IsNotExist(err) {
		t.Fatalf("planning created %q", filepath.Join(filepath.Dir(target), "one"))
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("planning created the target %q", target)
	}
}

// A missing target still has to answer the question the panel asks: is there
// space, and can this path be written at all. Both are answered from the nearest
// folder that exists.
func TestPlanMoveReadsFreeSpaceFromTheNearestExistingFolder(t *testing.T) {
	home := stateHome(t)
	seedState(t, home)
	target := filepath.Join(testenv.TempDir(t), "not-yet", "there")

	plan := PlanMove(t.Context(), config.RootState, target)
	if plan.Free <= 0 {
		t.Fatal("a missing target was not given a free-space reading from its parent")
	}
}

// An existing file where a folder is expected is refused, not created over.
func TestPlanMoveRefusesAFileAsTheTarget(t *testing.T) {
	home := stateHome(t)
	seedState(t, home)
	file := filepath.Join(testenv.TempDir(t), "a-file")
	write(t, file, 16)

	plan := PlanMove(t.Context(), config.RootState, file)
	if plan.OK() {
		t.Fatalf("a file planned as a target: %+v", plan)
	}
}
