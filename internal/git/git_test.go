package git_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Waiivyy/turnback/internal/git"
	"github.com/Waiivyy/turnback/internal/testutil"
)

func TestRunIgnoresInheritedRepositoryVariables(t *testing.T) {
	a := testutil.NewRepo(t)
	b := testutil.NewRepo(t)
	// git exports these to hooks; turnback must not follow them elsewhere.
	t.Setenv("GIT_DIR", filepath.Join(b.Dir, ".git"))
	t.Setenv("GIT_WORK_TREE", b.Dir)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(b.Dir, "bogus-index"))

	r := git.Runner{Dir: a.Dir}
	top, err := r.Run("rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	// git prints forward slashes, also on Windows.
	if got := filepath.FromSlash(strings.TrimSpace(top)); got != a.Dir {
		t.Errorf("toplevel = %q, want %q", got, a.Dir)
	}
	index, err := r.Run("rev-parse", "--git-path", "index")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(index); got != ".git/index" {
		t.Errorf("index path = %q, want .git/index", got)
	}
}

func TestRunReportsGitFailures(t *testing.T) {
	repo := testutil.NewRepo(t)
	_, err := git.Runner{Dir: repo.Dir}.Run("rev-parse", "--verify", "no-such-branch")
	var gitErr *git.Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("err = %v, want *git.Error", err)
	}
	if gitErr.ExitCode != 128 {
		t.Errorf("exit code = %d, want 128", gitErr.ExitCode)
	}
	if !strings.Contains(err.Error(), "fatal:") {
		t.Errorf("error %q does not carry git's message", err)
	}
}

func TestRunInputFeedsStdin(t *testing.T) {
	repo := testutil.NewRepo(t)
	out, err := git.Runner{Dir: repo.Dir}.RunInput([]byte("hello\n"), "hash-object", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	// Well-known blob id of "hello\n".
	if got := strings.TrimSpace(string(out)); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Errorf("hash = %q", got)
	}
}

func TestLocateFromSubdirectory(t *testing.T) {
	repo := testutil.NewRepo(t)
	sub := repo.Path("src/http")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	loc, err := git.Locate(sub)
	if err != nil {
		t.Fatal(err)
	}
	if loc.Root != repo.Dir {
		t.Errorf("root = %q, want %q", loc.Root, repo.Dir)
	}
	if loc.Prefix != "src/http" {
		t.Errorf("prefix = %q, want src/http", loc.Prefix)
	}
}

func TestLocateAtRootHasEmptyPrefix(t *testing.T) {
	repo := testutil.NewRepo(t)
	loc, err := git.Locate(repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if loc.Prefix != "" {
		t.Errorf("prefix = %q, want empty", loc.Prefix)
	}
}

func TestLocateOutsideARepository(t *testing.T) {
	testutil.Isolate(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(testutil.TempDir(t)))
	_, err := git.Locate(testutil.TempDir(t))
	if !errors.Is(err, git.ErrNotRepository) {
		t.Errorf("err = %v, want ErrNotRepository", err)
	}
}

func TestCommittedComparesWorkingFilesWithHEADByContent(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Git("config", "core.autocrlf", "true")
	for _, f := range []string{"clean.txt", "modified.txt", "staged.txt", "deleted.txt", "odd [name].txt", "hidden.txt", "script.sh"} {
		repo.Write(f, "committed\n")
	}
	repo.Write("crlf.txt", "one\ntwo\n")
	if runtime.GOOS != "windows" {
		if err := os.Symlink("clean.txt", repo.Path("link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("clean.txt", repo.Path("moved-link")); err != nil {
			t.Fatal(err)
		}
	}
	repo.Commit("initial")

	repo.Write("modified.txt", "changed\n")
	repo.Write("staged.txt", "changed\n")
	repo.Git("add", "staged.txt")
	repo.Remove("deleted.txt")
	repo.Write("untracked.txt", "new\n")
	repo.Write("crlf.txt", "one\r\ntwo\r\n") // git normalizes this back to what HEAD has
	// git status hides changes to skip-worktree files; content does not lie.
	repo.Git("update-index", "--skip-worktree", "hidden.txt")
	repo.Write("hidden.txt", "a local secret\n")
	want := map[string]bool{
		"clean.txt": true, "odd [name].txt": true, "crlf.txt": true, "missing.txt": true,
		"modified.txt": false, "staged.txt": false, "deleted.txt": false, "untracked.txt": false,
		"hidden.txt": false,
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(repo.Path("script.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
		repo.Remove("moved-link")
		if err := os.Symlink("odd [name].txt", repo.Path("moved-link")); err != nil {
			t.Fatal(err)
		}
		want["script.sh"], want["link"], want["moved-link"] = false, true, false
	}
	var asked []string
	for p := range want {
		asked = append(asked, p)
	}
	got, err := git.Committed(repo.Dir, asked)
	if err != nil {
		t.Fatal(err)
	}
	for p, w := range want {
		if got[p] != w {
			t.Errorf("Committed(%q) = %v, want %v", p, got[p], w)
		}
	}
}

func TestCommittedWithoutAnyCommit(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "x\n")
	got, err := git.Committed(repo.Dir, []string{"a.txt", "missing.txt"})
	if err != nil || got["a.txt"] || !got["missing.txt"] {
		t.Errorf("Committed = %v, %v; want a.txt uncommitted and missing.txt committed", got, err)
	}
}

func TestDetachedCommandsStillRun(t *testing.T) {
	repo := testutil.NewRepo(t)
	out, err := git.Runner{Dir: repo.Dir, Detach: true}.RunInput([]byte("hello\n"), "hash-object", "--stdin")
	if err != nil || strings.TrimSpace(string(out)) != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Errorf("detached run = %q, %v", out, err)
	}
}

func TestCommittedHandlesNamesThatStartWithAQuote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not allow quotes in file names")
	}
	repo := testutil.NewRepo(t)
	repo.Write(`"a.txt"`, "committed\n")
	repo.Write(`"draft notes.txt`, "committed\n")
	repo.Commit("initial")
	repo.Write(`"a.txt"`, "edited, not saved\n")
	repo.Write(`"draft notes.txt`, "edited, not saved\n")
	// A decoy that holds exactly what HEAD has for "a.txt".
	repo.Write("a.txt", "committed\n")

	got, err := git.Committed(repo.Dir, []string{`"a.txt"`, `"draft notes.txt`})
	if err != nil {
		t.Fatal(err)
	}
	if got[`"a.txt"`] || got[`"draft notes.txt`] {
		t.Errorf("Committed = %v, want both edited files reported as not committed", got)
	}
}
