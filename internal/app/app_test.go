package app_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/git"
	"github.com/Waiivyy/turnback/internal/store"
	"github.com/Waiivyy/turnback/internal/testutil"
)

var t0 = time.Date(2026, 9, 30, 14, 0, 0, 0, time.UTC)

// clock returns a fake clock that advances one minute per call.
func clock() func() time.Time {
	now := t0
	return func() time.Time {
		now = now.Add(time.Minute)
		return now
	}
}

func openApp(t *testing.T, repo *testutil.Repo) *app.App {
	t.Helper()
	a, err := app.Open(repo.Dir, clock())
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func start(t *testing.T, a *app.App, opts app.StartOptions) {
	t.Helper()
	if _, err := a.Start(opts); err != nil {
		t.Fatal(err)
	}
}

func end(t *testing.T, a *app.App, opts app.EndOptions) *app.Ended {
	t.Helper()
	res, err := a.End(opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func paths(files []store.File) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Status+" "+f.Path)
	}
	return out
}

func TestTurnContainsOnlyChangesMadeBetweenStartAndEnd(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Write("old.txt", "bye\n")
	repo.Commit("initial")
	a := openApp(t, repo)

	// Turn 1, by the agent.
	start(t, a, app.StartOptions{})
	repo.Write("a.txt", "one\ntwo\n")
	end(t, a, app.EndOptions{})

	// A manual edit between turns must not be attributed to turn 2.
	repo.Write("manual.txt", "mine\n")

	start(t, a, app.StartOptions{})
	repo.Write("new.txt", "fresh\n")
	repo.Remove("old.txt")
	res := end(t, a, app.EndOptions{})

	if res.Turn == nil || res.Turn.ID != 2 {
		t.Fatalf("turn = %+v, want turn 2", res.Turn)
	}
	want := []string{"A new.txt", "D old.txt"}
	if got := paths(res.Turn.Files); !reflect.DeepEqual(got, want) {
		t.Errorf("turn 2 files = %q, want %q", got, want)
	}
	saved, err := a.Store.Turn(2)
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(saved.Files); !reflect.DeepEqual(got, want) {
		t.Errorf("saved turn 2 files = %q, want %q", got, want)
	}
	if !saved.StartedAt.Before(saved.EndedAt) {
		t.Errorf("times out of order: %v, %v", saved.StartedAt, saved.EndedAt)
	}
	patch, err := a.Store.Patch(2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "+fresh") || strings.Contains(patch, "mine") {
		t.Errorf("patch has the wrong content:\n%s", patch)
	}
}

func TestRecordedTurnCanBeInspectedWithPlainGit(t *testing.T) {
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)
	start(t, a, app.StartOptions{})
	repo.Write("a.txt", "x\n")
	end(t, a, app.EndOptions{})

	got := repo.Git("--git-dir=.turnback/git", "diff", "--name-status", "refs/turns/1^", "refs/turns/1")
	if got != "A\ta.txt" {
		t.Errorf("git diff of refs/turns/1 = %q, want %q", got, "A\ta.txt")
	}
}

func TestEndWithoutChangesRecordsNothing(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "x\n")
	a := openApp(t, repo)
	start(t, a, app.StartOptions{})
	res := end(t, a, app.EndOptions{})
	if res.Turn != nil {
		t.Errorf("turn = %+v, want none", res.Turn)
	}
	if turns, _ := a.Store.Turns(); len(turns) != 0 {
		t.Errorf("%d turns saved, want 0", len(turns))
	}
	if sess, _ := a.Store.Session(); sess != nil {
		t.Error("session still open after end")
	}
}

func TestStartRefusesWhileATurnIsBeingRecorded(t *testing.T) {
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)
	start(t, a, app.StartOptions{})
	_, err := a.Start(app.StartOptions{})
	var active *app.SessionActiveError
	if !errors.As(err, &active) {
		t.Errorf("err = %v, want SessionActiveError", err)
	}
}

func TestStartCanRecordTheTurnInProgressFirst(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	repo.Commit("initial")
	a := openApp(t, repo)

	// A turn that never ended, as when an agent is stopped midway.
	start(t, a, app.StartOptions{Agent: "first"})
	repo.Write("a.txt", "2\n")

	res, err := a.Start(app.StartOptions{Agent: "second", EndOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Closed == nil || res.Closed.Turn == nil || res.Closed.Turn.ID != 1 {
		t.Fatalf("closed = %+v, want turn 1 recorded", res.Closed)
	}
	repo.Write("b.txt", "new\n")
	second := end(t, a, app.EndOptions{})

	first, err := a.Store.Turn(1)
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(first.Files); !reflect.DeepEqual(got, []string{"M a.txt"}) || first.Agent != "first" {
		t.Errorf("turn 1: files %v, agent %q; want [M a.txt], first", got, first.Agent)
	}
	if second.Turn == nil || second.Turn.ID != 2 {
		t.Fatalf("second turn = %+v, want turn 2", second.Turn)
	}
	if got := paths(second.Turn.Files); !reflect.DeepEqual(got, []string{"A b.txt"}) || second.Turn.Agent != "second" {
		t.Errorf("turn 2: files %v, agent %q; want [A b.txt], second", got, second.Turn.Agent)
	}
}

func TestStartRecordsNothingForAnUnchangedTurnInProgress(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	a := openApp(t, repo)
	start(t, a, app.StartOptions{})

	res, err := a.Start(app.StartOptions{EndOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Closed == nil || res.Closed.Turn != nil {
		t.Errorf("closed = %+v, want the turn in progress closed without a turn", res.Closed)
	}
	if turns, _ := a.Store.Turns(); len(turns) != 0 {
		t.Errorf("%d turns saved, want 0", len(turns))
	}
	if sess, _ := a.Store.Session(); sess == nil {
		t.Error("no turn is being recorded after start")
	}
}

func TestEndWithoutStartFails(t *testing.T) {
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)
	if _, err := a.End(app.EndOptions{}); !errors.Is(err, app.ErrNoSession) {
		t.Errorf("err = %v, want ErrNoSession", err)
	}
}

func TestDiscardDropsTheTurn(t *testing.T) {
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)
	start(t, a, app.StartOptions{})
	repo.Write("a.txt", "x\n")
	res := end(t, a, app.EndOptions{Discard: true})
	if !res.Discarded || res.Turn != nil {
		t.Errorf("result = %+v, want discarded and no turn", res)
	}
	if turns, _ := a.Store.Turns(); len(turns) != 0 {
		t.Errorf("%d turns saved, want 0", len(turns))
	}
	// A new turn can start right away.
	start(t, a, app.StartOptions{})
}

func TestDescriptionFromEndBeatsStartBeatsSummary(t *testing.T) {
	cases := []struct {
		name, atStart, atEnd, want string
	}{
		{"summary", "", "", "Add a.txt"},
		{"start", "from start", "", "from start"},
		{"end", "from start", "from end", "from end"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := testutil.NewRepo(t)
			a := openApp(t, repo)
			start(t, a, app.StartOptions{Description: c.atStart, Agent: "aider"})
			repo.Write("a.txt", "x\n")
			res := end(t, a, app.EndOptions{Description: c.atEnd})
			if res.Turn.Description != c.want {
				t.Errorf("description = %q, want %q", res.Turn.Description, c.want)
			}
			if res.Turn.Agent != "aider" {
				t.Errorf("agent = %q, want aider", res.Turn.Agent)
			}
		})
	}
}

func TestStatusShowsChangesSoFar(t *testing.T) {
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)

	st, err := a.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Initialized || st.Session != nil {
		t.Errorf("status before first use = %+v", st)
	}

	start(t, a, app.StartOptions{})
	repo.Write("a.txt", "x\n")
	st, err = a.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Session == nil || len(st.Pending) != 1 || st.Pending[0].Path != "a.txt" {
		t.Errorf("status during a turn = %+v", st)
	}
	end(t, a, app.EndOptions{})
	st, err = a.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Session != nil || st.Turns != 1 || st.Latest == nil || st.Latest.ID != 1 {
		t.Errorf("status after a turn = %+v", st)
	}
}

func TestOpenOutsideARepository(t *testing.T) {
	testutil.Isolate(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", testutil.TempDir(t))
	if _, err := app.Open(testutil.TempDir(t), time.Now); !errors.Is(err, git.ErrNotRepository) {
		t.Errorf("err = %v, want git.ErrNotRepository", err)
	}
}

func TestSummarize(t *testing.T) {
	cases := []struct {
		files []store.File
		want  string
	}{
		{[]store.File{{Status: "M", Path: "src/http/client.go"}}, "Update src/http/client.go"},
		{[]store.File{{Status: "A", Path: "retry.go"}}, "Add retry.go"},
		{[]store.File{{Status: "D", Path: "old/x.txt"}}, "Delete old/x.txt"},
		{[]store.File{{Status: "R", OldPath: "docs/old.md", Path: "docs/new.md"}}, "Rename docs/old.md to docs/new.md"},
		{[]store.File{{Status: "M", Path: "a/one.go"}, {Status: "M", Path: "b/two.go"}}, "Update one.go and two.go"},
		{[]store.File{{Status: "A", Path: "x/retry.go"}, {Status: "M", Path: "x/client.go"}, {Status: "M", Path: "go.mod"}},
			"Change retry.go, client.go and go.mod"},
		{[]store.File{{Status: "M", Path: "src/index.ts"}, {Status: "M", Path: "test/index.ts"}},
			"Update src/index.ts and test/index.ts"},
		{[]store.File{{Status: "A", Path: "a"}, {Status: "A", Path: "b"}, {Status: "A", Path: "c"}, {Status: "A", Path: "d"}, {Status: "A", Path: "e"}},
			"Add 5 files: a, b, c and 2 more"},
	}
	for _, c := range cases {
		if got := app.Summarize(c.files); got != c.want {
			t.Errorf("Summarize(%v) = %q, want %q", c.files, got, c.want)
		}
	}
}

func TestStatusShowsFilesGitBeganIgnoringAsSuch(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("app.js", "v1\n")
	repo.Write(".env", "SECRET=1\n") // untracked and not ignored, so recorded
	repo.Git("add", "app.js")
	repo.Git("commit", "-qm", "initial")
	a := openApp(t, repo)
	start(t, a, app.StartOptions{Description: "Ignore the env file"})
	repo.Write(".gitignore", ".env\n")

	st, err := a.Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range st.Pending {
		if f.Path == ".env" && !f.Ignored {
			t.Errorf("status shows .env as %+v, want it marked as ignored now, as end records it", f)
		}
	}
	res := end(t, a, app.EndOptions{})
	for _, f := range res.Turn.Files {
		if f.Path == ".env" && !f.Ignored {
			t.Errorf("end recorded .env as %+v", f)
		}
	}
}
