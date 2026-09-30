package app_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/store"
	"github.com/Waiivyy/turnback/internal/testutil"
)

// A repository can ship files inside .turnback. turnback must not act on
// them: a planted lock symlink would make it overwrite the link's target.
func TestRepositoriesThatTrackTurnbackFilesAreRefused(t *testing.T) {
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := testutil.NewRepo(t)
	repo.Write("README.md", "hi\n")
	if err := os.MkdirAll(repo.Path(".turnback"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, repo.Path(".turnback/lock")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	repo.Commit("innocent")

	_, err := app.Open(repo.Dir, clock())
	var tracked *app.TrackedStateError
	if !errors.As(err, &tracked) || tracked.Path != ".turnback/lock" {
		t.Fatalf("Open() = %v, want a TrackedStateError naming .turnback/lock", err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "precious\n" {
		t.Errorf("the link's target now holds %q", got)
	}
}

func TestTrackedTurnbackFilesInAnyCaseAreRefused(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".TurnBack/turns/0001.json", `{"id": 1}`)
	repo.Commit("planted")
	var tracked *app.TrackedStateError
	if _, err := app.Open(repo.Dir, clock()); !errors.As(err, &tracked) {
		t.Fatalf("Open() = %v, want a TrackedStateError", err)
	}
}

func TestUntrackedLinksInTheStoreAreRefused(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	repo.Commit("initial")
	if err := os.Symlink(t.TempDir(), repo.Path(".turnback")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	var unsafe *store.UnsafeError
	if _, err := app.Open(repo.Dir, clock()); !errors.As(err, &unsafe) {
		t.Fatalf("Open() = %v, want an UnsafeError", err)
	}
}

func TestFilesNamedLikeTheStoreElsewhereAreFine(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("docs/.turnback/notes.md", "fine\n")
	repo.Write(".turnbackrc", "fine\n")
	repo.Commit("initial")
	if _, err := app.Open(repo.Dir, clock()); err != nil {
		t.Errorf("Open() = %v", err)
	}
}
