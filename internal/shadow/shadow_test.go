package shadow_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestSnapshotNeverStoresItsOwnFolder(t *testing.T) {
	repo := testutil.NewRepo(t)
	// A work tree .gitignore that re-includes the folder holding the private
	// repository must not make turnback snapshot itself.
	repo.Write(".gitignore", "!/.turnback/\n")
	repo.Write("a.txt", "x\n")
	r, gitDir := open(t, repo)
	tree := snapshot(t, r)
	for _, f := range files(t, repo, gitDir, tree) {
		if strings.HasPrefix(f, ".turnback/") {
			t.Fatalf("snapshot contains %s", f)
		}
	}
}

func TestAFileThatBecomesIgnoredLeavesTheSnapshot(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("config.local", "token=old\n") // untracked and not ignored yet
	r, gitDir := open(t, repo)
	if got := files(t, repo, gitDir, snapshot(t, r)); !reflect.DeepEqual(got, []string{"config.local"}) {
		t.Fatalf("first snapshot = %q", got)
	}
	repo.Write(".gitignore", "config.local\n")
	repo.Write("config.local", "token=SECRET\n")
	tree := snapshot(t, r)
	if got := files(t, repo, gitDir, tree); !reflect.DeepEqual(got, []string{".gitignore"}) {
		t.Errorf("snapshot after ignoring = %q, want only .gitignore", got)
	}
	// The new content was never read into the private repository.
	if out, err := execGit(repo.Dir, "--git-dir="+gitDir, "grep", "-q", "SECRET", tree); err == nil {
		t.Errorf("the ignored file's new content is in the snapshot: %s", out)
	}
}

func TestSnapshotIncludesTrackedFilesThatMatchAnIgnorePattern(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".gitignore", "*.lock\n")
	repo.Write("deps.lock", "v1\n")
	repo.Git("add", "-f", "deps.lock")
	repo.Commit("initial")
	r, gitDir := open(t, repo)
	before := snapshot(t, r)
	if got := files(t, repo, gitDir, before); !reflect.DeepEqual(got, []string{".gitignore", "deps.lock"}) {
		t.Fatalf("snapshot = %q, want the force-added deps.lock too", got)
	}
	repo.Write("deps.lock", "v2\n")
	changes, err := r.Changes(before, snapshot(t, r))
	if err != nil || len(changes) != 1 || changes[0].Path != "deps.lock" {
		t.Errorf("changes = %+v, %v; want deps.lock modified", changes, err)
	}
}

func TestANestedRepositoryWithoutCommitsDoesNotBreakSnapshots(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "x\n")
	if err := os.MkdirAll(repo.Path("web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := execGit(repo.Path("web"), "init", "-q"); err != nil {
		t.Fatalf("git init web: %v %s", err, out)
	}
	repo.Write("web/index.html", "<h1>hi</h1>\n")
	r, gitDir := open(t, repo)
	tree, err := r.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot with a commitless nested repository: %v", err)
	}
	if got := files(t, repo, gitDir, tree); !reflect.DeepEqual(got, []string{"a.txt"}) {
		t.Errorf("snapshot = %q, want only a.txt", got)
	}
}

func TestSnapshotFollowsAFileReplacedByAFolderAndBack(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("utils", "a file\n")
	repo.Write("lib/a.ts", "a\n")
	repo.Commit("initial") // both are tracked by the user's repository
	r, gitDir := open(t, repo)
	snapshot(t, r)

	repo.Remove("utils")
	repo.Write("utils/index.ts", "export {}\n")
	repo.Remove("lib/a.ts")
	repo.Remove("lib")
	repo.Write("lib", "now a file\n")
	tree, err := r.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot after swapping files and folders: %v", err)
	}
	if got := files(t, repo, gitDir, tree); !reflect.DeepEqual(got, []string{"lib", "utils/index.ts"}) {
		t.Errorf("snapshot = %q", got)
	}
}

func TestSnapshotFollowsAFolderReplacedByASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need extra rights on Windows")
	}
	repo := testutil.NewRepo(t)
	repo.Write("config/app.ini", "a=1\n")
	repo.Commit("initial")
	shared := testutil.TempDir(t)
	if err := os.WriteFile(filepath.Join(shared, "app.ini"), []byte("a=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, gitDir := open(t, repo)
	snapshot(t, r)
	if err := os.RemoveAll(repo.Path("config")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, repo.Path("config")); err != nil {
		t.Fatal(err)
	}
	tree, err := r.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot after replacing a folder with a symlink: %v", err)
	}
	if got := files(t, repo, gitDir, tree); !reflect.DeepEqual(got, []string{"config"}) {
		t.Errorf("snapshot = %q, want only the symlink", got)
	}
}

func TestStaleLocksAreClearedAndFreshOnesKept(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	r, gitDir := open(t, repo)
	if _, err := r.Snapshot(); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	for _, name := range []string{"index.lock", "refs/turns/7.lock"} {
		p := filepath.Join(gitDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(filepath.Join(gitDir, "refs", "turns", "7.lock"), old, old); err != nil {
		t.Fatal(err)
	}

	r.ClearStaleLocks(time.Minute)
	if _, err := os.Stat(filepath.Join(gitDir, "refs", "turns", "7.lock")); err == nil {
		t.Error("an hour-old ref lock was kept")
	}
	if _, err := os.Stat(filepath.Join(gitDir, "index.lock")); err != nil {
		t.Error("a fresh index lock was removed")
	}
	r.ClearStaleLocks(0)
	if _, err := os.Stat(filepath.Join(gitDir, "index.lock")); err == nil {
		t.Error("the index lock was kept")
	}
	if _, err := r.Snapshot(); err != nil {
		t.Errorf("Snapshot after clearing the locks: %v", err)
	}
}

func TestTheFirstSnapshotCopiesCommittedFilesAsOnePack(t *testing.T) {
	repo := testutil.NewRepo(t)
	for i := 0; i < 50; i++ {
		repo.Write(fmt.Sprintf("src/file%02d.txt", i), fmt.Sprintf("content %d\n", i))
	}
	repo.Commit("initial")
	repo.Write("src/file07.txt", "edited\n") // one file differs from the index
	r, gitDir := open(t, repo)
	tree, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	loose := 0
	for _, dir := range listDirs(t, filepath.Join(gitDir, "objects")) {
		if len(dir) == 2 {
			entries, _ := os.ReadDir(filepath.Join(gitDir, "objects", dir))
			loose += len(entries)
		}
	}
	// The edited file and the trees are new; the 49 committed files come
	// from the pack.
	if loose > 5 {
		t.Errorf("%d loose objects after the first snapshot, want the committed files packed", loose)
	}
	listed := repo.Git("--git-dir="+gitDir, "ls-tree", "-r", tree)
	if !strings.Contains(listed, "src/file49.txt") {
		t.Fatalf("snapshot misses files:\n%s", listed)
	}
	if got := repo.Git("--git-dir="+gitDir, "cat-file", "-p", tree+":src/file07.txt"); got != "edited" {
		t.Errorf("src/file07.txt in the snapshot = %q, want the file on disk", got)
	}
	if got := repo.Git("--git-dir="+gitDir, "cat-file", "-p", tree+":src/file08.txt"); got != "content 8" {
		t.Errorf("src/file08.txt in the snapshot = %q", got)
	}
}

func listDirs(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}
