package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Waiivyy/turnback/internal/testutil"
)

func TestStartThenEndRecordsATurn(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Commit("initial")

	code, stdout, stderr := run(t, repo.Dir, "start", "-m", "Add a second line")
	if code != 0 {
		t.Fatalf("start: exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "Recording a new turn: Add a second line") {
		t.Errorf("start output = %q", stdout)
	}

	repo.Write("a.txt", "one\ntwo\n")
	repo.Write("b.txt", "new\n")
	code, stdout, stderr = run(t, repo.Dir, "end")
	if code != 0 {
		t.Fatalf("end: exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"Recorded turn 1: Add a second line",
		"M  a.txt  +1",
		"A  b.txt  +1",
		"2 files changed, +2 -0",
		"See it with 'turnback show 1', undo it with 'turnback undo 1'.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("end output lacks %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(repo.Path(".turnback/turns/0001.json")); err != nil {
		t.Errorf("turn file missing: %v", err)
	}
}

func TestStartExplainsFirstUse(t *testing.T) {
	repo := testutil.NewRepo(t)
	_, stdout, _ := run(t, repo.Dir, "start")
	if !strings.Contains(stdout, "Initialized turnback in .turnback/") {
		t.Errorf("first start output = %q", stdout)
	}
	run(t, repo.Dir, "end", "--discard")
	_, stdout, _ = run(t, repo.Dir, "start")
	if strings.Contains(stdout, "Initialized") {
		t.Errorf("second start repeats the first-use note: %q", stdout)
	}
}

func TestEndWithoutStartSaysHowToStart(t *testing.T) {
	repo := testutil.NewRepo(t)
	code, _, stderr := run(t, repo.Dir, "end")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "no turn is being recorded") || !strings.Contains(stderr, "turnback start") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestStartTwiceSaysHowToFinish(t *testing.T) {
	repo := testutil.NewRepo(t)
	run(t, repo.Dir, "start")
	code, _, stderr := run(t, repo.Dir, "start")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "already being recorded") || !strings.Contains(stderr, "turnback end --discard") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestEndWithNothingChanged(t *testing.T) {
	repo := testutil.NewRepo(t)
	run(t, repo.Dir, "start")
	code, stdout, _ := run(t, repo.Dir, "end")
	if code != 0 || !strings.Contains(stdout, "Nothing changed since 'turnback start'") {
		t.Errorf("exit %d, output %q", code, stdout)
	}
}

func TestEndDiscard(t *testing.T) {
	repo := testutil.NewRepo(t)
	run(t, repo.Dir, "start")
	repo.Write("a.txt", "x\n")
	code, stdout, _ := run(t, repo.Dir, "end", "--discard")
	if code != 0 || !strings.Contains(stdout, "Discarded the turn") {
		t.Errorf("exit %d, output %q", code, stdout)
	}
}

func TestStatusWhileRecording(t *testing.T) {
	repo := testutil.NewRepo(t)
	run(t, repo.Dir, "start", "-m", "Refactor config")
	repo.Write("config.go", "package config\n")
	code, stdout, _ := run(t, repo.Dir, "status")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"Recording a turn", "Refactor config", "1 file changed so far", "A  config.go"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, stdout)
		}
	}
}

func TestStatusBeforeFirstUse(t *testing.T) {
	repo := testutil.NewRepo(t)
	_, stdout, _ := run(t, repo.Dir, "status")
	if !strings.Contains(stdout, "Nothing recorded in this repository yet") {
		t.Errorf("status = %q", stdout)
	}
	if _, err := os.Stat(repo.Path(".turnback")); err == nil {
		t.Error("status created .turnback/, but it should not write anything")
	}
}

func TestOutsideARepository(t *testing.T) {
	testutil.Isolate(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", testutil.TempDir(t))
	code, _, stderr := run(t, testutil.TempDir(t), "start")
	if code != 1 || !strings.Contains(stderr, "not inside a git repository") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestUnknownOptionIsAUsageError(t *testing.T) {
	repo := testutil.NewRepo(t)
	code, _, stderr := run(t, repo.Dir, "start", "--bogus")
	if code != 2 || !strings.Contains(stderr, "unknown option --bogus") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestCommandHelp(t *testing.T) {
	for _, args := range [][]string{{"start", "--help"}, {"help", "start"}} {
		code, stdout, _ := run(t, t.TempDir(), args...)
		if code != 0 || !strings.HasPrefix(stdout, "Usage: turnback start") {
			t.Errorf("%v: exit %d, output %q", args, code, stdout)
		}
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{70 * time.Second, "1 minute ago"},
		{12 * time.Minute, "12 minutes ago"},
		{65 * time.Minute, "1 hour ago"},
		{5 * time.Hour, "5 hours ago"},
		{30 * time.Hour, "1 day ago"},
		{72 * time.Hour, "3 days ago"},
	}
	for _, c := range cases {
		if got := ago(now, now.Add(-c.d)); got != c.want {
			t.Errorf("ago(%v) = %q, want %q", c.d, got, c.want)
		}
	}
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	if got := ago(now, old); got != "on 2026-01-02" {
		t.Errorf("ago(old) = %q, want %q", got, "on 2026-01-02")
	}
}
