package git_test

import (
	"errors"
	"os"
	"path/filepath"
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
	if got := strings.TrimSpace(top); got != a.Dir {
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

func TestUncommittedFindsEveryKindOfUncommittedChange(t *testing.T) {
	repo := testutil.NewRepo(t)
	for _, f := range []string{"clean.txt", "modified.txt", "staged.txt", "deleted.txt", "odd [name].txt"} {
		repo.Write(f, "committed\n")
	}
	repo.Commit("initial")
	repo.Write("modified.txt", "changed\n")
	repo.Write("staged.txt", "changed\n")
	repo.Git("add", "staged.txt")
	repo.Remove("deleted.txt")
	repo.Write("untracked.txt", "new\n")

	asked := []string{"clean.txt", "modified.txt", "staged.txt", "deleted.txt", "untracked.txt", "odd [name].txt", "missing.txt"}
	got, err := git.Uncommitted(repo.Dir, asked)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"modified.txt": true, "staged.txt": true, "deleted.txt": true, "untracked.txt": true}
	if len(got) != len(want) {
		t.Errorf("Uncommitted = %v, want %v", got, want)
	}
	for p := range want {
		if !got[p] {
			t.Errorf("%s not reported as uncommitted", p)
		}
	}
}

func TestUncommittedWithoutAnyCommit(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "x\n")
	got, err := git.Uncommitted(repo.Dir, []string{"a.txt"})
	if err != nil || !got["a.txt"] {
		t.Errorf("Uncommitted = %v, %v; want a.txt uncommitted", got, err)
	}
}

func TestDetachedCommandsStillRun(t *testing.T) {
	repo := testutil.NewRepo(t)
	out, err := git.Runner{Dir: repo.Dir, Detach: true}.RunInput([]byte("hello\n"), "hash-object", "--stdin")
	if err != nil || strings.TrimSpace(string(out)) != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Errorf("detached run = %q, %v", out, err)
	}
}
