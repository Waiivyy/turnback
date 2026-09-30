package shadow_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Waiivyy/turnback/internal/shadow"
	"github.com/Waiivyy/turnback/internal/testutil"
)

func open(t *testing.T, repo *testutil.Repo) (*shadow.Repo, string) {
	t.Helper()
	gitDir := repo.Path(".turnback/git")
	r, err := shadow.Open(repo.Dir, gitDir)
	if err != nil {
		t.Fatal(err)
	}
	return r, gitDir
}

func snapshot(t *testing.T, r *shadow.Repo) string {
	t.Helper()
	tree, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// files lists the paths stored in a snapshot tree, read with plain git.
func files(t *testing.T, repo *testutil.Repo, gitDir, tree string) []string {
	t.Helper()
	out := repo.Git("--git-dir="+gitDir, "ls-tree", "-r", "--name-only", tree)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func TestSnapshotCapturesTrackedAndNewFilesButNotIgnoredOnes(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".gitignore", "ignored/\n*.log\n")
	repo.Write("a.txt", "one\n")
	repo.Commit("initial")
	repo.Write("a.txt", "one\ntwo\n")
	repo.Write("new.txt", "brand new\n")
	repo.Write("ignored/secret.txt", "token\n")
	repo.Write("debug.log", "noise\n")

	r, gitDir := open(t, repo)
	tree := snapshot(t, r)

	want := []string{".gitignore", "a.txt", "new.txt"}
	if got := files(t, repo, gitDir, tree); !reflect.DeepEqual(got, want) {
		t.Errorf("snapshot files = %q, want %q", got, want)
	}
	if got := repo.Git("--git-dir="+gitDir, "cat-file", "blob", tree+":a.txt"); got != "one\ntwo" {
		t.Errorf("a.txt in snapshot = %q, want the modified content", got)
	}
}

func TestSnapshotHonorsInfoExcludeAndRepositoryExcludesFile(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("keep.txt", "x\n")
	repo.Write("local-only.txt", "x\n")
	repo.Write("scratch.tmp", "x\n")
	repo.Write(".git/info/exclude", "local-only.txt\n")
	excludes := filepath.Join(testutil.TempDir(t), "excludes")
	if err := os.WriteFile(excludes, []byte("*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo.Git("config", "core.excludesFile", excludes)

	r, gitDir := open(t, repo)
	tree := snapshot(t, r)

	if got := files(t, repo, gitDir, tree); !reflect.DeepEqual(got, []string{"keep.txt"}) {
		t.Errorf("snapshot files = %q, want only keep.txt", got)
	}
}

func TestSnapshotLeavesTheUsersRepositoryUntouched(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Commit("initial")
	repo.Write("a.txt", "one\nstaged\n")
	repo.Git("add", "a.txt")
	repo.Write("a.txt", "one\nstaged\nunstaged\n")
	repo.Write("untracked.txt", "u\n")

	indexHash := func() string {
		b, err := os.ReadFile(repo.Path(".git/index"))
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%x", sha256.Sum256(b))
	}
	beforeIndex := indexHash()
	beforeRefs := repo.Git("for-each-ref")
	beforeStatus := repo.Git("status", "--porcelain")

	r, _ := open(t, repo)
	snapshot(t, r)

	if indexHash() != beforeIndex {
		t.Error("the user's index file changed")
	}
	if got := repo.Git("for-each-ref"); got != beforeRefs {
		t.Errorf("refs changed:\n%s\nwant:\n%s", got, beforeRefs)
	}
	if got := repo.Git("stash", "list"); got != "" {
		t.Errorf("stash list = %q, want empty", got)
	}
	// status also proves .turnback/ does not show up as untracked, as long
	// as the caller ignores it; here it is excluded only for the snapshot,
	// so compare against status with .turnback filtered out.
	var lines []string
	for _, l := range strings.Split(repo.Git("status", "--porcelain"), "\n") {
		if !strings.Contains(l, ".turnback") {
			lines = append(lines, l)
		}
	}
	if got := strings.Join(lines, "\n"); got != beforeStatus {
		t.Errorf("status = %q, want %q", got, beforeStatus)
	}
}

func TestSnapshotStoresExactBytesDespiteConversionSettings(t *testing.T) {
	repo := testutil.NewRepo(t)
	testutil.SetGlobalConfig(t, "core.autocrlf", "true")
	testutil.SetGlobalConfig(t, "filter.upper.clean", "tr a-z A-Z")
	repo.Write(".gitattributes", "*.txt text=auto\n*.up filter=upper\n")
	repo.Write("crlf.txt", "one\r\ntwo\r\n")
	repo.Write("shout.up", "quiet\n")

	r, gitDir := open(t, repo)
	tree := snapshot(t, r)

	blob := func(path string) string {
		out, err := execGit(repo.Dir, "--git-dir="+gitDir, "cat-file", "blob", tree+":"+path)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := blob("crlf.txt"); got != "one\r\ntwo\r\n" {
		t.Errorf("crlf.txt stored as %q, want the CRLF bytes unchanged", got)
	}
	if got := blob("shout.up"); got != "quiet\n" {
		t.Errorf("shout.up stored as %q, want it unfiltered", got)
	}
}

func TestChangesBetweenSnapshots(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n2\n3\n")
	repo.Write("b.txt", "going away\n")
	repo.Write("c.txt", "a file long enough\nthat git can tell\nit was renamed\n")
	repo.Commit("initial")
	r, _ := open(t, repo)
	before := snapshot(t, r)

	repo.Write("a.txt", "1\ntwo\n3\n4\n")
	repo.Remove("b.txt")
	repo.Write("d.txt", repo.Read("c.txt"))
	repo.Remove("c.txt")
	repo.Write("dir with space/ünï.txt", "x\ny\n")
	repo.Write("img.bin", "\x00\x01\x02binary")
	after := snapshot(t, r)

	got, err := r.Changes(before, after)
	if err != nil {
		t.Fatal(err)
	}
	want := []shadow.Change{
		{Status: "M", Path: "a.txt", Added: 2, Deleted: 1},
		{Status: "D", Path: "b.txt", Added: 0, Deleted: 1},
		{Status: "R", Path: "d.txt", OldPath: "c.txt"},
		{Status: "A", Path: "dir with space/ünï.txt", Added: 2},
		{Status: "A", Path: "img.bin", Binary: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("changes =\n%+v\nwant\n%+v", got, want)
	}
}

func TestPatchIsAStandardPatchWhateverTheDiffSettings(t *testing.T) {
	repo := testutil.NewRepo(t)
	testutil.SetGlobalConfig(t, "diff.noprefix", "true")
	testutil.SetGlobalConfig(t, "diff.external", "false")
	repo.Write("a.txt", "one\n")
	r, _ := open(t, repo)
	before := snapshot(t, r)
	repo.Write("a.txt", "one\ntwo\n")
	after := snapshot(t, r)

	patch, err := r.Patch(before, after)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"diff --git a/a.txt b/a.txt", "--- a/a.txt", "+++ b/a.txt", "+two"} {
		if !strings.Contains(patch, want) {
			t.Errorf("patch lacks %q:\n%s", want, patch)
		}
	}
}

func TestRecordedSnapshotsSurviveGarbageCollection(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "precious\n")
	r, gitDir := open(t, repo)
	tree := snapshot(t, r)
	commit, err := r.Commit(tree, "", "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetRef("refs/turns/1", commit); err != nil {
		t.Fatal(err)
	}
	repo.Write("a.txt", "changed\n")
	snapshot(t, r) // leaves the old blob referenced only by the kept tree

	repo.Git("--git-dir="+gitDir, "gc", "-q", "--prune=now")

	if got := repo.Git("--git-dir="+gitDir, "cat-file", "blob", tree+":a.txt"); got != "precious" {
		t.Errorf("blob after gc = %q", got)
	}
}

func TestCountFiles(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "x\n")
	repo.Write("sub/b.txt", "y\n")
	r, _ := open(t, repo)
	n, err := r.CountFiles(snapshot(t, r))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("CountFiles = %d, want 2", n)
	}
}

func TestSnapshotRefusesToStoreItsOwnFolder(t *testing.T) {
	repo := testutil.NewRepo(t)
	// A work tree .gitignore outranks info/exclude, so this re-includes the
	// folder holding the private repository.
	repo.Write(".gitignore", "!/.turnback/\n")
	r, _ := open(t, repo)

	_, err := r.Snapshot()
	if err == nil || !strings.Contains(err.Error(), ".turnback/") {
		t.Fatalf("Snapshot error = %v, want a refusal naming .turnback/", err)
	}
}
