package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Waiivyy/turnback/internal/testutil"
)

// record records one turn through the CLI, running edit in between.
func record(t *testing.T, repo *testutil.Repo, desc string, edit func()) {
	t.Helper()
	if code, _, stderr := run(t, repo.Dir, "start", "-m", desc); code != 0 {
		t.Fatalf("start: %s", stderr)
	}
	edit()
	if code, _, stderr := run(t, repo.Dir, "end"); code != 0 {
		t.Fatalf("end: %s", stderr)
	}
}

// twoTurns sets up a repository with two recorded turns.
func twoTurns(t *testing.T) *testutil.Repo {
	t.Helper()
	repo := testutil.NewRepo(t)
	repo.Write("src/http/client.go", "package http\n\nfunc Get() {}\n")
	repo.Commit("initial")
	record(t, repo, "Add retry logic", func() {
		repo.Write("src/http/client.go", "package http\n\nfunc Get() { retry() }\n")
		repo.Write("src/http/retry.go", "package http\n\nfunc retry() {}\n")
	})
	record(t, repo, "Write the README", func() {
		repo.Write("README.md", "# demo\n")
	})
	return repo
}

func TestLogListsTurnsNewestFirst(t *testing.T) {
	repo := twoTurns(t)
	code, stdout, stderr := run(t, repo.Dir, "log")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("log output has %d lines, want header + 2 rows:\n%s", len(lines), stdout)
	}
	for i, want := range [][]string{
		{"ID", "WHEN", "FILES", "CHANGES", "DESCRIPTION"},
		{"2", "today", "1", "+1 -0", "Write the README"},
		{"1", "today", "2", "+4 -1", "Add retry logic"},
	} {
		for _, field := range want {
			if !strings.Contains(lines[i], field) {
				t.Errorf("line %d = %q, want it to contain %q", i, lines[i], field)
			}
		}
	}
}

func TestLogFileFilterIsRelativeToTheCurrentDirectory(t *testing.T) {
	repo := twoTurns(t)
	code, stdout, stderr := run(t, repo.Path("src/http"), "log", "--file", "retry.go")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Add retry logic") || strings.Contains(stdout, "Write the README") {
		t.Errorf("log --file retry.go =\n%s", stdout)
	}
}

func TestLogSince(t *testing.T) {
	repo := twoTurns(t)
	_, stdout, _ := run(t, repo.Dir, "log", "--since", "1h")
	if !strings.Contains(stdout, "Add retry logic") {
		t.Errorf("log --since 1h =\n%s", stdout)
	}
	_, stdout, _ = run(t, repo.Dir, "log", "--since", "2099-01-01")
	if !strings.Contains(stdout, "No turns match") {
		t.Errorf("log --since 2099-01-01 =\n%s", stdout)
	}
	code, _, stderr := run(t, repo.Dir, "log", "--since", "next tuesday")
	if code != 2 || !strings.Contains(stderr, `cannot read "next tuesday"`) {
		t.Errorf("bad --since: exit %d, stderr %q", code, stderr)
	}
}

func TestLogBeforeAnyTurns(t *testing.T) {
	repo := testutil.NewRepo(t)
	_, stdout, _ := run(t, repo.Dir, "log")
	if !strings.Contains(stdout, "No turns recorded yet") {
		t.Errorf("log = %q", stdout)
	}
}

func TestLogMentionsTheTurnInProgress(t *testing.T) {
	repo := twoTurns(t)
	run(t, repo.Dir, "start")
	_, stdout, _ := run(t, repo.Dir, "log")
	if !strings.Contains(stdout, "A turn is being recorded") {
		t.Errorf("log during a turn =\n%s", stdout)
	}
}

func TestLogJSON(t *testing.T) {
	repo := twoTurns(t)
	_, stdout, _ := run(t, repo.Dir, "log", "--json")
	var turns []struct {
		ID    int `json:"id"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(stdout), &turns); err != nil {
		t.Fatalf("log --json is not JSON: %v\n%s", err, stdout)
	}
	if len(turns) != 2 || turns[0].ID != 2 || turns[1].Files[1].Path != "src/http/retry.go" {
		t.Errorf("log --json = %+v", turns)
	}
}

func TestShowPrintsHeaderFilesAndDiff(t *testing.T) {
	repo := twoTurns(t)
	code, stdout, stderr := run(t, repo.Dir, "show", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		"turn 1  Add retry logic",
		"M  src/http/client.go  +1 -1",
		"A  src/http/retry.go   +3",
		"2 files changed, +4 -1",
		"diff --git a/src/http/client.go b/src/http/client.go",
		"+func Get() { retry() }",
		"+func retry() {}",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("show 1 lacks %q:\n%s", want, stdout)
		}
	}
}

func TestShowWithoutAnIdShowsTheLatestTurn(t *testing.T) {
	repo := twoTurns(t)
	_, stdout, _ := run(t, repo.Dir, "show")
	if !strings.Contains(stdout, "turn 2  Write the README") {
		t.Errorf("show =\n%s", stdout)
	}
}

func TestShowOneFile(t *testing.T) {
	repo := twoTurns(t)
	_, stdout, _ := run(t, repo.Dir, "show", "1", "--file", "src/http/retry.go")
	if !strings.Contains(stdout, "+func retry() {}") || strings.Contains(stdout, "diff --git a/src/http/client.go") {
		t.Errorf("show --file =\n%s", stdout)
	}
	// The file list matches the diff below it.
	if strings.Contains(stdout, "M  src/http/client.go") || !strings.Contains(stdout, "Showing 1 of 2 files") {
		t.Errorf("show --file header =\n%s", stdout)
	}
	code, _, stderr := run(t, repo.Dir, "show", "1", "--file", "nope.go")
	if code != 1 || !strings.Contains(stderr, "turn 1 did not change nope.go") {
		t.Errorf("show --file nope.go: exit %d, stderr %q", code, stderr)
	}
}

func TestShowStatOmitsTheDiff(t *testing.T) {
	repo := twoTurns(t)
	_, stdout, _ := run(t, repo.Dir, "show", "1", "--stat")
	if !strings.Contains(stdout, "A  src/http/retry.go") || strings.Contains(stdout, "diff --git") {
		t.Errorf("show --stat =\n%s", stdout)
	}
}

func TestShowJSON(t *testing.T) {
	repo := twoTurns(t)
	_, stdout, _ := run(t, repo.Dir, "show", "2", "--json")
	var turn struct {
		ID   int    `json:"id"`
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal([]byte(stdout), &turn); err != nil {
		t.Fatalf("show --json is not JSON: %v\n%s", err, stdout)
	}
	if turn.ID != 2 || !strings.Contains(turn.Diff, "+# demo") {
		t.Errorf("show --json = %+v", turn)
	}
}

func TestShowUnknownTurn(t *testing.T) {
	repo := twoTurns(t)
	code, _, stderr := run(t, repo.Dir, "show", "9")
	if code != 1 || !strings.Contains(stderr, "there is no turn 9") || !strings.Contains(stderr, "turnback log") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	code, _, stderr = run(t, repo.Dir, "show", "banana")
	if code != 2 || !strings.Contains(stderr, `"banana" is not a turn id`) {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 9, 30, 15, 30, 0, 0, time.Local)
	midnight := time.Date(2026, 9, 30, 0, 0, 0, 0, time.Local)
	cases := map[string]time.Time{
		"90s":                  now.Add(-90 * time.Second),
		"45m":                  now.Add(-45 * time.Minute),
		"2h":                   now.Add(-2 * time.Hour),
		"2 hours ago":          now.Add(-2 * time.Hour),
		"3d":                   now.Add(-72 * time.Hour),
		"1w":                   now.Add(-7 * 24 * time.Hour),
		"today":                midnight,
		"yesterday":            midnight.AddDate(0, 0, -1),
		"2026-09-28":           time.Date(2026, 9, 28, 0, 0, 0, 0, time.Local),
		"2026-09-28 14:05":     time.Date(2026, 9, 28, 14, 5, 0, 0, time.Local),
		"2026-09-28T14:05":     time.Date(2026, 9, 28, 14, 5, 0, 0, time.Local),
		"2026-09-28T14:05:00Z": time.Date(2026, 9, 28, 14, 5, 0, 0, time.UTC),
	}
	for in, want := range cases {
		got, err := parseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseSince("soon", now); err == nil {
		t.Error("parseSince(soon) succeeded")
	}
}

func TestDiffColorsHunkLinesByTheirFirstCharacter(t *testing.T) {
	patch := "diff --git a/q.sql b/q.sql\n" +
		"--- a/q.sql\n" +
		"+++ b/q.sql\n" +
		"@@ -1,2 +1 @@ SELECT\n" +
		"--- an old comment\n" +
		" SELECT 1;\n"
	var out bytes.Buffer
	env := &Env{Stdout: &out, Color: true}
	writeDiff(env, patch)
	got := out.String()
	for _, want := range []string{
		bold + "--- a/q.sql" + reset,               // file header
		red + "--- an old comment" + reset,         // removed line that looks like a header
		cyan + "@@ -1,2 +1 @@" + reset + " SELECT", // hunk header, context uncolored
		"\n SELECT 1;\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("colored diff lacks %q:\n%q", want, got)
		}
	}
}

func TestPagerReceivesTheOutput(t *testing.T) {
	repo := twoTurns(t)
	paged := filepath.Join(testutil.TempDir(t), "paged.txt")
	var stdout, stderr bytes.Buffer
	env := &Env{Stdout: &stdout, Stderr: &stderr, Dir: repo.Dir, Now: time.Now, Pager: "cat > '" + paged + "'"}
	if code := Run(env, []string{"log"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	b, err := os.ReadFile(paged)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Add retry logic") || stdout.Len() != 0 {
		t.Errorf("pager got %q, stdout got %q", b, stdout.String())
	}
}
