package cli

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Waiivyy/turnback/internal/testutil"
)

// runTyping runs a command in a terminal-like environment where the user
// types input at any prompt.
func runTyping(t *testing.T, dir, input string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := &Env{
		Stdin: strings.NewReader(input), Stdout: &stdout, Stderr: &stderr,
		Dir: dir, Now: time.Now, Interactive: true,
	}
	code := Run(env, args)
	return code, stdout.String(), stderr.String()
}

// oneTurn records a turn that appends "two" to a.txt and adds b.txt.
func oneTurn(t *testing.T) *testutil.Repo {
	t.Helper()
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Commit("initial")
	record(t, repo, "Add a line and a file", func() {
		repo.Write("a.txt", "one\ntwo\n")
		repo.Write("b.txt", "new\n")
	})
	return repo
}

func TestUndoIsADryRunByDefault(t *testing.T) {
	repo := oneTurn(t)
	code, stdout, stderr := run(t, repo.Dir, "undo", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		"Undo turn 1: Add a line and a file",
		"M  a.txt  put back the version from before turn 1",
		"D  b.txt  delete it: turn 1 created it",
		"-two",
		"Dry run: nothing was changed. To apply it, run 'turnback undo 1 --yes'.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	if repo.Read("a.txt") != "one\ntwo\n" || !repo.Exists("b.txt") {
		t.Error("a dry run changed files")
	}
}

func TestUndoWithYesApplies(t *testing.T) {
	repo := oneTurn(t)
	code, stdout, stderr := run(t, repo.Dir, "undo", "1", "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if repo.Read("a.txt") != "one\n" || repo.Exists("b.txt") {
		t.Errorf("a.txt = %q, b.txt exists = %v", repo.Read("a.txt"), repo.Exists("b.txt"))
	}
	for _, want := range []string{"Undid turn 1: 2 files changed, +0 -2.", "Recorded as turn 2.", "'turnback undo 2'"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	_, log, _ := run(t, repo.Dir, "log")
	if !strings.Contains(log, "Undo turn 1: Add a line and a file") {
		t.Errorf("log does not list the undo:\n%s", log)
	}
}

func TestUndoAsksBeforeApplyingInATerminal(t *testing.T) {
	repo := oneTurn(t)
	_, stdout, stderr := runTyping(t, repo.Dir, "n\n", "undo", "1")
	// The question goes to stderr, so it stays visible when stdout is redirected.
	if !strings.Contains(stderr, "Apply this undo? [y/N]") || strings.Contains(stdout, "Apply this undo?") {
		t.Errorf("prompt: stdout %q, stderr %q", stdout, stderr)
	}
	if !strings.Contains(stdout, "Nothing was changed.") {
		t.Errorf("output after answering no:\n%s", stdout)
	}
	if repo.Read("a.txt") != "one\ntwo\n" {
		t.Fatal("answering no still changed a.txt")
	}
	code, _, stderr := runTyping(t, repo.Dir, "y\n", "undo", "1")
	if code != 0 || repo.Read("a.txt") != "one\n" {
		t.Errorf("answering yes: exit %d, a.txt = %q, stderr %q", code, repo.Read("a.txt"), stderr)
	}
}

func TestDryRunWinsOverYes(t *testing.T) {
	repo := oneTurn(t)
	_, stdout, _ := run(t, repo.Dir, "undo", "1", "--yes", "--dry-run")
	if !strings.Contains(stdout, "Dry run") || repo.Read("a.txt") != "one\ntwo\n" {
		t.Errorf("--dry-run --yes applied the undo:\n%s", stdout)
	}
}

func TestUndoReportsConflictsAndWritesNothing(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("f.txt", "a\n\nb\nc\n")
	repo.Write("g.txt", "g\n")
	repo.Commit("initial")
	record(t, repo, "Change b", func() {
		repo.Write("f.txt", "a\n\nB\nc\n")
		repo.Write("g.txt", "G\n")
	})
	record(t, repo, "Change B again", func() { repo.Write("f.txt", "a\n\nB!\nc\n") })

	code, stdout, stderr := run(t, repo.Dir, "undo", "1", "--yes")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	for _, want := range []string{
		"!  f.txt  conflict: later edits overlap the lines turn 1 changed",
		"Also changed later by turn 2.",
		"<<<<<<< now",
		"B!",
		">>>>>>> before turn 1",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "cannot undo turn 1 cleanly, so nothing was changed") || !strings.Contains(stderr, "--file") {
		t.Errorf("stderr = %q", stderr)
	}
	if repo.Read("g.txt") != "G\n" || repo.Read("f.txt") != "a\n\nB!\nc\n" {
		t.Error("files changed despite the conflict")
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.TrimRight(line, " ") != line {
			t.Errorf("line has trailing spaces: %q", line)
		}
	}
}

func TestUndoRefusesToRewriteUnsavedEditsWithoutForce(t *testing.T) {
	repo := testutil.NewRepo(t)
	lines := "1\n2\n3\n4\n5\n6\n7\n8\n"
	repo.Write("f.txt", lines)
	repo.Commit("initial")
	record(t, repo, "Change line 2", func() { repo.Write("f.txt", strings.Replace(lines, "2\n", "TWO\n", 1)) })
	// An edit far from line 2, neither committed nor recorded.
	unsaved := strings.Replace(strings.Replace(lines, "2\n", "TWO\n", 1), "7\n", "SEVEN\n", 1)
	repo.Write("f.txt", unsaved)

	code, stdout, stderr := run(t, repo.Dir, "undo", "1", "--yes")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout, "(unsaved changes)") {
		t.Errorf("plan does not flag the unsaved file:\n%s", stdout)
	}
	if !strings.Contains(stderr, "f.txt has changes that are neither committed nor recorded") || !strings.Contains(stderr, "--force") {
		t.Errorf("stderr = %q", stderr)
	}
	if repo.Read("f.txt") != unsaved {
		t.Fatal("f.txt changed without --force")
	}

	code, _, stderr = run(t, repo.Dir, "undo", "1", "--yes", "--force")
	if want := strings.Replace(lines, "7\n", "SEVEN\n", 1); code != 0 || repo.Read("f.txt") != want {
		t.Errorf("--force: exit %d, f.txt = %q, want %q, stderr %q", code, repo.Read("f.txt"), want, stderr)
	}
}

func TestUndoOneFileWithTheFileFlag(t *testing.T) {
	repo := oneTurn(t)
	code, stdout, _ := run(t, repo.Dir, "undo", "last", "--file", "b.txt")
	if code != 0 || !strings.Contains(stdout, "run 'turnback undo last --file b.txt --yes'") {
		t.Errorf("exit %d, output:\n%s", code, stdout)
	}
	run(t, repo.Dir, "undo", "last", "--file", "b.txt", "--yes")
	if repo.Exists("b.txt") || repo.Read("a.txt") != "one\ntwo\n" {
		t.Errorf("b.txt exists = %v, a.txt = %q", repo.Exists("b.txt"), repo.Read("a.txt"))
	}
}

func TestUndoWhenThereIsNothingLeftToUndo(t *testing.T) {
	repo := oneTurn(t)
	run(t, repo.Dir, "undo", "1", "--yes")
	code, stdout, _ := run(t, repo.Dir, "undo", "1", "--yes")
	if code != 0 || !strings.Contains(stdout, "Nothing to undo: every file is already as it was before turn 1.") {
		t.Errorf("exit %d, output:\n%s", code, stdout)
	}
}

func TestUndoNeedsATurn(t *testing.T) {
	repo := oneTurn(t)
	code, _, stderr := run(t, repo.Dir, "undo")
	if code != 2 || !strings.Contains(stderr, "turnback undo 3") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestUndoWarnsWhenLaterTurnsMayDependOnIt(t *testing.T) {
	repo := oneTurn(t)
	record(t, repo, "Use the new file", func() { repo.Write("c.txt", "uses b\n") })
	_, stdout, _ := run(t, repo.Dir, "undo", "1")
	if !strings.Contains(stdout, "Note: turn 2 came after turn 1.") {
		t.Errorf("output lacks the warning about later turns:\n%s", stdout)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "Note:") || strings.Contains(line, "run your tests") {
			if len(line) > 76 {
				t.Errorf("note line is %d characters, want it wrapped at 76: %q", len(line), line)
			}
		}
	}
}

func TestAnUndoThatCannotFinishSaysNothingChanged(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	repo := testutil.NewRepo(t)
	repo.Write("ro/b.txt", "b1\n")
	repo.Commit("initial")
	record(t, repo, "Change b", func() { repo.Write("ro/b.txt", "b2\n") })
	if err := os.Chmod(repo.Path("ro"), 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(repo.Path("ro"), 0o755)

	code, _, stderr := run(t, repo.Dir, "undo", "1", "--yes")
	if code != 1 || !strings.Contains(stderr, "the undo stopped and nothing was changed") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if repo.Read("ro/b.txt") != "b2\n" {
		t.Error("ro/b.txt changed")
	}
}

func TestUndoSummaryCountsEveryFileWritten(t *testing.T) {
	repo := testutil.NewRepo(t)
	doc := "a document long enough\nfor git to notice\nthat it was renamed\n"
	repo.Write("old.md", doc)
	repo.Commit("initial")
	record(t, repo, "Rename", func() {
		repo.Write("new.md", doc)
		repo.Remove("old.md")
	})
	_, stdout, _ := run(t, repo.Dir, "undo", "1", "--yes")
	if !strings.Contains(stdout, "Undid turn 1: 2 files changed") {
		t.Errorf("summary for undoing a rename:\n%s", stdout)
	}
}
