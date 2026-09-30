package app_test

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/store"
	"github.com/Waiivyy/turnback/internal/testutil"
)

// recordTurns records one turn per step, each making the step's edits.
func recordTurns(t *testing.T, repo *testutil.Repo, a *app.App, steps ...func()) {
	t.Helper()
	for _, step := range steps {
		start(t, a, app.StartOptions{})
		step()
		end(t, a, app.EndOptions{})
	}
}

func ids(turns []*store.Turn) []int {
	var out []int
	for _, t := range turns {
		out = append(out, t.ID)
	}
	return out
}

func TestResolveTurn(t *testing.T) {
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)
	if _, err := a.ResolveTurn("last"); !errors.Is(err, app.ErrNoTurns) {
		t.Errorf("last with no turns: err = %v, want ErrNoTurns", err)
	}
	recordTurns(t, repo, a,
		func() { repo.Write("a.txt", "1\n") },
		func() { repo.Write("b.txt", "2\n") },
	)
	for ref, want := range map[string]int{"1": 1, "#2": 2, "last": 2, "latest": 2} {
		turn, err := a.ResolveTurn(ref)
		if err != nil || turn.ID != want {
			t.Errorf("ResolveTurn(%q) = %v, %v; want turn %d", ref, turn, err, want)
		}
	}
	if _, err := a.ResolveTurn("9"); !errors.Is(err, store.ErrTurnNotFound) {
		t.Errorf("ResolveTurn(9) err = %v, want ErrTurnNotFound", err)
	}
	if _, err := a.ResolveTurn("banana"); err == nil || !strings.Contains(err.Error(), `"banana" is not a turn id`) {
		t.Errorf("ResolveTurn(banana) err = %v", err)
	}
}

func TestRepoPathIsRelativeToWhereTurnbackRuns(t *testing.T) {
	repo := testutil.NewRepo(t)
	if err := os.MkdirAll(repo.Path("src/http"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(repo.Path("src/http"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"client.go":                "src/http/client.go",
		"../main.go":               "src/main.go",
		".":                        "src/http",
		"../..":                    "",
		repo.Path("docs/guide.md"): "docs/guide.md",
		"./nested/../retry.go":     "src/http/retry.go",
	}
	for arg, want := range cases {
		got, err := a.RepoPath(arg)
		if err != nil || got != want {
			t.Errorf("RepoPath(%q) = %q, %v; want %q", arg, got, err, want)
		}
	}
	if _, err := a.RepoPath("../../../elsewhere.txt"); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("path outside the repository: err = %v", err)
	}
}

func TestLogFiltersBySinceFilesAndLimit(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("docs/old.md", "a long enough document\nto be detected\nas a rename later\n")
	repo.Commit("initial")
	a := openApp(t, repo) // the fake clock advances a minute per call
	recordTurns(t, repo, a,
		func() { repo.Write("src/a.go", "package a\n") },                                           // turn 1
		func() { repo.Write("README.md", "hi\n") },                                                 // turn 2
		func() { repo.Write("src/b.go", "package b\n") },                                           // turn 3
		func() { repo.Write("docs/new.md", repo.Read("docs/old.md")); repo.Remove("docs/old.md") }, // turn 4
	)
	all, err := a.Log(app.LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(all); !reflect.DeepEqual(got, []int{4, 3, 2, 1}) {
		t.Fatalf("all turns = %v", got)
	}

	cases := []struct {
		name string
		opts app.LogOptions
		want []int
	}{
		{"since the end of turn 2", app.LogOptions{Since: all[2].EndedAt}, []int{4, 3, 2}},
		{"directory", app.LogOptions{Paths: []string{"src"}}, []int{3, 1}},
		{"file", app.LogOptions{Paths: []string{"src/b.go"}}, []int{3}},
		{"old name of a renamed file", app.LogOptions{Paths: []string{"docs/old.md"}}, []int{4}},
		{"several paths", app.LogOptions{Paths: []string{"README.md", "src/a.go"}}, []int{2, 1}},
		{"whole repository", app.LogOptions{Paths: []string{""}}, []int{4, 3, 2, 1}},
		{"similar prefix is not a directory match", app.LogOptions{Paths: []string{"sr"}}, nil},
		{"limit", app.LogOptions{Limit: 2}, []int{4, 3}},
	}
	for _, c := range cases {
		got, err := a.Log(c.opts)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(ids(got), c.want) {
			t.Errorf("%s: turns = %v, want %v", c.name, ids(got), c.want)
		}
	}
}

func TestDiffCanBeLimitedToFilesAndKeepsRenamesWhole(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("old.md", "a long enough document\nto be detected\nas a rename\n")
	repo.Write("src/[id].tsx", "one\n")
	repo.Write("src/i.tsx", "one\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	recordTurns(t, repo, a, func() {
		repo.Write("new.md", repo.Read("old.md"))
		repo.Remove("old.md")
		repo.Write("src/[id].tsx", "two\n")
		repo.Write("src/i.tsx", "two\n")
	})
	turn, err := a.ResolveTurn("1")
	if err != nil {
		t.Fatal(err)
	}

	full, err := a.Diff(turn, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rename from old.md", "src/[id].tsx", "src/i.tsx"} {
		if !strings.Contains(full, want) {
			t.Errorf("full diff lacks %q:\n%s", want, full)
		}
	}

	// Brackets are literal, not a glob that would also match src/i.tsx.
	one, err := a.Diff(turn, []string{"src/[id].tsx"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(one, "b/src/[id].tsx") || strings.Contains(one, "src/i.tsx") {
		t.Errorf("diff limited to src/[id].tsx =\n%s", one)
	}

	// Asking for the new name still shows the rename, not an added file.
	renamed, err := a.Diff(turn, []string{"new.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(renamed, "rename from old.md") || !strings.Contains(renamed, "rename to new.md") {
		t.Errorf("diff limited to new.md =\n%s", renamed)
	}

	var notIn *app.NotInTurnError
	if _, err := a.Diff(turn, []string{"untouched.go"}); !errors.As(err, &notIn) {
		t.Errorf("diff of an untouched file: err = %v, want NotInTurnError", err)
	}
}
