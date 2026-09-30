package shadow_test

import (
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Waiivyy/turnback/internal/shadow"
	"github.com/Waiivyy/turnback/internal/testutil"
)

func TestMerge3KeepsEditsThatDoNotOverlap(t *testing.T) {
	repo := testutil.NewRepo(t)
	r, _ := open(t, repo)
	base := "one\ntwo\nthree\nfour\nfive\nsix\nseven\n"
	current := "one\ntwo\nthree\nfour\nfive\nsix\nSEVEN\n" // a later edit at the end
	other := "one\nTWO\nthree\nfour\nfive\nsix\nseven\n"   // the other side's edit near the top
	merged, conflicts, err := r.Merge3([]byte(current), []byte(base), []byte(other), [3]string{"now", "turn", "before"})
	if err != nil {
		t.Fatal(err)
	}
	if conflicts != 0 || string(merged) != "one\nTWO\nthree\nfour\nfive\nsix\nSEVEN\n" {
		t.Errorf("merged = %q with %d conflicts", merged, conflicts)
	}
}

func TestMerge3ReportsOverlappingEdits(t *testing.T) {
	repo := testutil.NewRepo(t)
	r, _ := open(t, repo)
	merged, conflicts, err := r.Merge3([]byte("a\nNOW\nc\n"), []byte("a\nTURN\nc\n"), []byte("a\nb\nc\n"),
		[3]string{"now", "turn 1", "before turn 1"})
	if err != nil {
		t.Fatal(err)
	}
	if conflicts != 1 {
		t.Errorf("conflicts = %d, want 1", conflicts)
	}
	for _, want := range []string{"<<<<<<< now\nNOW\n=======\nb\n>>>>>>> before turn 1\n"} {
		if !strings.Contains(string(merged), want) {
			t.Errorf("merged lacks %q:\n%s", want, merged)
		}
	}
}

func TestBuildTreeAndTreeDiff(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("keep.txt", "keep\n")
	repo.Write("change.txt", "old\n")
	repo.Write("drop.txt", "drop\n")
	r, gitDir := open(t, repo)
	base := snapshot(t, r)

	blob, err := r.WriteBlob([]byte("new\n"))
	if err != nil {
		t.Fatal(err)
	}
	added, err := r.WriteBlob([]byte("#!/bin/sh\n"))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := r.BuildTree(base, map[string]*shadow.Entry{
		"change.txt":    {Mode: "100644", ID: blob},
		"drop.txt":      nil,
		"bin/script.sh": {Mode: "100755", ID: added},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := files(t, repo, gitDir, tree); !reflect.DeepEqual(got, []string{"bin/script.sh", "change.txt", "keep.txt"}) {
		t.Errorf("tree files = %q", got)
	}

	diff, err := r.TreeDiff(base, tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff) != 3 {
		t.Fatalf("TreeDiff = %+v, want 3 paths", diff)
	}
	byPath := map[string]shadow.EntryChange{}
	for _, c := range diff {
		byPath[c.Path] = c
	}
	if c := byPath["bin/script.sh"]; c.Old != nil || c.New == nil || c.New.Mode != "100755" || c.New.ID != added {
		t.Errorf("added entry = %+v", c)
	}
	if c := byPath["drop.txt"]; c.Old == nil || c.New != nil {
		t.Errorf("removed entry = %+v", c)
	}
	if c := byPath["change.txt"]; c.Old == nil || c.New == nil || c.New.ID != blob {
		t.Errorf("changed entry = %+v", c)
	}
	content, err := r.ReadBlob(blob)
	if err != nil || string(content) != "new\n" {
		t.Errorf("ReadBlob = %q, %v", content, err)
	}
	only, err := r.TreeDiff(base, tree, "drop.txt")
	if err != nil || len(only) != 1 || only[0].Path != "drop.txt" {
		t.Errorf("TreeDiff limited to drop.txt = %+v, %v", only, err)
	}
}

func TestCheckoutWritesOnlyChangedPaths(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("keep.txt", "keep\n")
	repo.Write("change.txt", "old\n")
	repo.Write("drop.txt", "drop\n")
	r, _ := open(t, repo)
	from := snapshot(t, r)
	blob, _ := r.WriteBlob([]byte("new\n"))
	to, err := r.BuildTree(from, map[string]*shadow.Entry{"change.txt": {Mode: "100644", ID: blob}, "drop.txt": nil})
	if err != nil {
		t.Fatal(err)
	}
	// An unrelated file changed after the snapshot must survive.
	repo.Write("keep.txt", "edited meanwhile\n")

	if err := r.Checkout(from, to); err != nil {
		t.Fatal(err)
	}
	if got := repo.Read("change.txt"); got != "new\n" {
		t.Errorf("change.txt = %q", got)
	}
	if repo.Exists("drop.txt") {
		t.Error("drop.txt still exists")
	}
	if got := repo.Read("keep.txt"); got != "edited meanwhile\n" {
		t.Errorf("keep.txt = %q, want the edit made after the snapshot", got)
	}
}

func TestCheckoutRefusesToOverwriteAFileThatChangedOnDisk(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "old\n")
	r, _ := open(t, repo)
	from := snapshot(t, r)
	blob, _ := r.WriteBlob([]byte("new\n"))
	to, _ := r.BuildTree(from, map[string]*shadow.Entry{"a.txt": {Mode: "100644", ID: blob}})
	repo.Write("a.txt", "someone else's edit, same size\n")

	if err := r.Checkout(from, to); err == nil {
		t.Fatal("Checkout succeeded over a file that changed on disk")
	}
	if got := repo.Read("a.txt"); got != "someone else's edit, same size\n" {
		t.Errorf("a.txt = %q, want it untouched", got)
	}
}

func TestCheckoutNeverOverwritesAnIgnoredFile(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".gitignore", "secret.env\n")
	r, _ := open(t, repo)
	from := snapshot(t, r)
	blob, _ := r.WriteBlob([]byte("restored\n"))
	to, _ := r.BuildTree(from, map[string]*shadow.Entry{"secret.env": {Mode: "100644", ID: blob}})
	repo.Write("secret.env", "TOKEN=abc\n")

	if err := r.Checkout(from, to); err == nil {
		t.Fatal("Checkout overwrote an ignored file")
	}
	if got := repo.Read("secret.env"); got != "TOKEN=abc\n" {
		t.Errorf("secret.env = %q, want it untouched", got)
	}
}

func TestIndexTreeMatchesTheLastSnapshotOrCheckout(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "x\n")
	r, _ := open(t, repo)
	tree := snapshot(t, r)
	got, err := r.IndexTree()
	if err != nil || got != tree {
		t.Errorf("IndexTree = %q, %v; want %q", got, err, tree)
	}
	if paths, err := r.Paths(tree); err != nil || !reflect.DeepEqual(paths, []string{"a.txt"}) {
		t.Errorf("Paths = %q, %v", paths, err)
	}
	blob, _ := r.WriteBlob([]byte("y\n"))
	next, _ := r.BuildTree(tree, map[string]*shadow.Entry{"a.txt": {Mode: "100644", ID: blob}, "b.txt": {Mode: "100644", ID: blob}})
	if err := r.Checkout(tree, next); err != nil {
		t.Fatal(err)
	}
	if got, err := r.IndexTree(); err != nil || got != next {
		t.Errorf("IndexTree after Checkout = %q, %v; want %q", got, err, next)
	}
}

func TestCheckoutReplacesAFileWithAFolderAndBack(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("utils.ts", "export const a = 1;\n")
	repo.Write("keep.ts", "keep\n")
	r, _ := open(t, repo)
	fileTree := snapshot(t, r)
	blob, _ := r.WriteBlob([]byte("export const a = 2;\n"))
	folderTree, err := r.BuildTree(fileTree, map[string]*shadow.Entry{
		"utils.ts":       nil,
		"utils/index.ts": {Mode: "100644", ID: blob},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Checkout(fileTree, folderTree); err != nil {
		t.Fatalf("file to folder: %v", err)
	}
	if got := repo.Read("utils/index.ts"); got != "export const a = 2;\n" || repo.Exists("utils.ts") {
		t.Errorf("after file to folder: utils/index.ts = %q, utils.ts exists = %v", got, repo.Exists("utils.ts"))
	}

	snapshot(t, r) // the private index must match the tree we start from
	if err := r.Checkout(folderTree, fileTree); err != nil {
		t.Fatalf("folder to file: %v", err)
	}
	if got := repo.Read("utils.ts"); got != "export const a = 1;\n" || repo.Exists("utils") {
		t.Errorf("after folder to file: utils.ts = %q, utils exists = %v", got, repo.Exists("utils"))
	}
	if got := repo.Read("keep.ts"); got != "keep\n" {
		t.Errorf("keep.ts = %q", got)
	}
}

func TestCheckoutPutsEverythingBackWhenANewFileIsBlocked(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".gitignore", "blocked.txt\n")
	repo.Write("a.txt", "original\n")
	repo.Write("gone.txt", "still here\n")
	r, _ := open(t, repo)
	from := snapshot(t, r)
	changed, _ := r.WriteBlob([]byte("changed\n"))
	added, _ := r.WriteBlob([]byte("new\n"))
	to, err := r.BuildTree(from, map[string]*shadow.Entry{
		"a.txt":       {Mode: "100644", ID: changed},
		"gone.txt":    nil,
		"blocked.txt": {Mode: "100644", ID: added},
		"fresh.txt":   {Mode: "100644", ID: added},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo.Write("blocked.txt", "an ignored file of the user's\n")

	if err := r.Checkout(from, to); err == nil {
		t.Fatal("Checkout succeeded although an ignored file was in the way")
	}
	for path, want := range map[string]string{
		"a.txt":       "original\n",
		"gone.txt":    "still here\n",
		"blocked.txt": "an ignored file of the user's\n",
	} {
		if got := repo.Read(path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if repo.Exists("fresh.txt") {
		t.Error("fresh.txt was created although the checkout failed")
	}
}

func TestCheckoutRefusesToWriteThroughASymlinkedFolder(t *testing.T) {
	repo := testutil.NewRepo(t)
	outside := testutil.TempDir(t)
	repo.Write("a.txt", "a\n")
	r, _ := open(t, repo)
	from := snapshot(t, r)
	blob, _ := r.WriteBlob([]byte("escaped\n"))
	to, _ := r.BuildTree(from, map[string]*shadow.Entry{"vendor/lib.go": {Mode: "100644", ID: blob}})
	// "vendor" is ignored and points outside the repository.
	repo.Write(".git/info/exclude", "vendor\n")
	if err := os.Symlink(outside, repo.Path("vendor")); err != nil {
		t.Fatal(err)
	}

	if err := r.Checkout(from, to); err == nil {
		t.Fatal("Checkout wrote through a symlinked folder")
	}
	if _, err := os.Stat(outside + "/lib.go"); err == nil {
		t.Error("a file was written outside the repository")
	}
}

// readOnly makes dir unwritable for the rest of the test. Permission tests
// are meaningless for root and differ on Windows, so they are skipped there.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
}

func TestCheckoutPutsBackEarlierChangesWhenAWriteFails(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("ro/b.txt", "b2\n")
	repo.Write("zz-notes.md", "notes\n")
	r, _ := open(t, repo)
	from := snapshot(t, r)
	old, _ := r.WriteBlob([]byte("b1\n"))
	// read-tree deletes zz-notes.md first, then fails to rewrite ro/b.txt.
	to, err := r.BuildTree(from, map[string]*shadow.Entry{"ro/b.txt": {Mode: "100644", ID: old}, "zz-notes.md": nil})
	if err != nil {
		t.Fatal(err)
	}
	readOnly(t, repo.Path("ro"))

	err = r.Checkout(from, to)
	var failed *shadow.CheckoutError
	if !errors.As(err, &failed) || !failed.Restored {
		t.Fatalf("Checkout err = %v, want a CheckoutError with everything restored", err)
	}
	if got := repo.Read("zz-notes.md"); got != "notes\n" {
		t.Errorf("zz-notes.md = %q, want it put back", got)
	}
	if got := repo.Read("ro/b.txt"); got != "b2\n" {
		t.Errorf("ro/b.txt = %q", got)
	}
}

func TestCheckoutNoticesADeletionTheSystemRefused(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "a2\n")
	repo.Write("ro/new.txt", "g\n")
	r, _ := open(t, repo)
	from := snapshot(t, r)
	old, _ := r.WriteBlob([]byte("a1\n"))
	to, err := r.BuildTree(from, map[string]*shadow.Entry{"a.txt": {Mode: "100644", ID: old}, "ro/new.txt": nil})
	if err != nil {
		t.Fatal(err)
	}
	// git only warns when it cannot delete a file, and exits 0.
	readOnly(t, repo.Path("ro"))

	err = r.Checkout(from, to)
	var failed *shadow.CheckoutError
	if !errors.As(err, &failed) || !failed.Restored {
		t.Fatalf("Checkout err = %v, want a CheckoutError with everything restored", err)
	}
	if !strings.Contains(err.Error(), "ro/new.txt") {
		t.Errorf("error %q does not name the file that could not be deleted", err)
	}
	if got := repo.Read("a.txt"); got != "a2\n" {
		t.Errorf("a.txt = %q, want the change rolled back", got)
	}
}

func TestCheckoutRemovesFoldersItCreatedWhenItRollsBack(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("ro/keep.txt", "k\n")
	r, _ := open(t, repo)
	from := snapshot(t, r)
	blob, _ := r.WriteBlob([]byte("new\n"))
	to, err := r.BuildTree(from, map[string]*shadow.Entry{
		"newdir/sub/f.txt": {Mode: "100644", ID: blob},
		"ro/g.txt":         {Mode: "100644", ID: blob},
	})
	if err != nil {
		t.Fatal(err)
	}
	readOnly(t, repo.Path("ro"))

	var failed *shadow.CheckoutError
	if err := r.Checkout(from, to); !errors.As(err, &failed) || !failed.Restored {
		t.Fatalf("Checkout err = %v, want a CheckoutError with everything restored", err)
	}
	if repo.Exists("newdir") {
		t.Error("newdir/ was left behind by the rollback")
	}
}

func TestAnObstacleFoundDuringCheckoutIsNotReportedAsDamage(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("old.txt", "old\n")
	r, _ := open(t, repo)
	from := snapshot(t, r)
	blob, _ := r.WriteBlob([]byte("new\n"))
	to, err := r.BuildTree(from, map[string]*shadow.Entry{"old.txt": nil, "new/file.txt": {Mode: "100644", ID: blob}})
	if err != nil {
		t.Fatal(err)
	}
	// A folder where the new file must go passes the early check (phase 1
	// might empty it) but not the strict one.
	if err := os.MkdirAll(repo.Path("new/file.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	var failed *shadow.CheckoutError
	if err := r.Checkout(from, to); !errors.As(err, &failed) || !failed.Restored {
		t.Fatalf("Checkout err = %v, want everything restored", err)
	}
	if repo.Read("old.txt") != "old\n" {
		t.Error("old.txt was not put back")
	}
}
