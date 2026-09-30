package app_test

import (
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/testutil"
)

// Files that are tracked although an ignore pattern matches them, such as a
// force-added .env.example, are no longer tracked once a turn renames them
// with git mv or removes them with git rm. An undo must not write them back
// where git ignores them, since no later snapshot would see them, and it
// must never lose them either.

func TestUndoingARenameFromAnIgnoredNameKeepsTheRename(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".gitignore", ".env*\n")
	repo.Write(".env.example", "DATABASE_URL=postgres://localhost/app\n")
	repo.Write("app.js", "v1\n")
	repo.Git("add", ".gitignore", "app.js")
	repo.Git("add", "-f", ".env.example")
	repo.Git("commit", "-qm", "initial")
	a := openApp(t, repo)
	turn(t, a, "Rename the example env file", func() {
		repo.Git("mv", ".env.example", ".env.sample")
		repo.Write("app.js", "v2\n")
	})

	p := planUndo(t, a, 1)
	if f := planned(t, p, ".env.example"); f.Action != app.UndoSkip || !strings.Contains(f.Reason, "ignores it") {
		t.Errorf(".env.example plan = %+v, want it skipped as ignored", f)
	}
	if f := planned(t, p, ".env.sample"); f.Action != app.UndoSkip || !strings.Contains(f.Reason, "renamed .env.example to it") {
		t.Errorf(".env.sample plan = %+v, want it kept with the rename", f)
	}
	applyUndo(t, a, p, false)
	if got := repo.Read(".env.sample"); got != "DATABASE_URL=postgres://localhost/app\n" {
		t.Errorf(".env.sample = %q, want it kept", got)
	}
	if got := repo.Read("app.js"); got != "v1\n" {
		t.Errorf("app.js = %q, want the rest of the turn undone", got)
	}
}

func TestAGitRmOfAnIgnoredNameIsLeftAsItIs(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".gitignore", "*.lock\n")
	repo.Write("deps.lock", "v1\n")
	repo.Git("add", ".gitignore")
	repo.Git("add", "-f", "deps.lock")
	repo.Git("commit", "-qm", "initial")
	a := openApp(t, repo)
	turn(t, a, "Drop the lock file", func() { repo.Git("rm", "-q", "deps.lock") })

	if f := planned(t, planUndo(t, a, 1), "deps.lock"); f.Action != app.UndoSkip || !strings.Contains(f.Reason, "ignores it") {
		t.Errorf("deps.lock plan = %+v, want it skipped as ignored", f)
	}
}

func TestUndoingACaseOnlyRenameOfAnIgnoredName(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".gitignore", "*.local\n")
	repo.Write("Settings.local", "debug=true\n")
	repo.Git("add", ".gitignore")
	repo.Git("add", "-f", "Settings.local")
	repo.Git("commit", "-qm", "initial")
	a := openApp(t, repo)
	turn(t, a, "Lower-case the settings file", func() { repo.Git("mv", "Settings.local", "settings.local") })

	applyUndo(t, a, planUndo(t, a, 1), false)
	var kept []string
	for _, name := range names(t, repo.Dir) {
		if strings.EqualFold(name, "settings.local") {
			kept = append(kept, name)
		}
	}
	// On a case-insensitive disk both spellings are the same tracked file,
	// so the rename is undone. Elsewhere the old name is ignored now, so the
	// rename stays. Either way exactly one copy remains.
	want := "settings.local"
	if caseInsensitive(t, repo.Dir) {
		want = "Settings.local"
	}
	if !reflect.DeepEqual(kept, []string{want}) {
		t.Fatalf("files = %q, want only %s", kept, want)
	}
	if got := repo.Read(want); got != "debug=true\n" {
		t.Errorf("%s = %q", want, got)
	}
}

func TestAnIgnoredFileWhereARenamedFileWouldReturnIsLeftAlone(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".gitignore", ".env*\n")
	repo.Write(".env.example", "EXAMPLE=1\n")
	repo.Git("add", ".gitignore")
	repo.Git("add", "-f", ".env.example")
	repo.Git("commit", "-qm", "initial")
	a := openApp(t, repo)
	turn(t, a, "Rename the example env file", func() { repo.Git("mv", ".env.example", ".env.sample") })
	repo.Write(".env.example", "MINE=1\n") // untracked now, so ignored

	p := planUndo(t, a, 1)
	if got := actions(p); got[".env.example"] != app.UndoSkip || got[".env.sample"] != app.UndoSkip {
		t.Errorf("plan = %v, want both names left alone", got)
	}
	if _, err := a.ApplyUndo(p, true); !errors.Is(err, app.ErrNothingToUndo) {
		t.Errorf("ApplyUndo err = %v, want ErrNothingToUndo", err)
	}
	if repo.Read(".env.example") != "MINE=1\n" || repo.Read(".env.sample") != "EXAMPLE=1\n" {
		t.Errorf(".env.example = %q, .env.sample = %q; want both untouched", repo.Read(".env.example"), repo.Read(".env.sample"))
	}
}

// writes reports whether the plan changes the file on disk.
func writes(f app.UndoFile) bool { return f.Action != app.UndoSkip && f.Action != app.UndoConflict }

// symlinkOrSkip makes a symbolic link, or skips the test where that is not
// possible.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

func TestAFolderReplacedByASymlinkCanBeRecordedAndUndone(t *testing.T) {
	shared := t.TempDir()
	if err := os.WriteFile(shared+"/app.ini", []byte("shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := testutil.NewRepo(t)
	repo.Write("config/app.ini", "local\n")
	repo.Write("main.go", "package main\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Edit the config", func() { repo.Write("config/app.ini", "local, edited\n") })

	// The next turn swaps the folder for a link to a shared one.
	start(t, a, app.StartOptions{Description: "Share the config"})
	if err := os.RemoveAll(repo.Path("config")); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, shared, repo.Path("config"))
	res := end(t, a, app.EndOptions{})
	if res.Turn == nil {
		t.Fatal("the turn recorded nothing")
	}

	// Undoing the first turn must not write through the link.
	p := planUndo(t, a, 1)
	if f := planned(t, p, "config/app.ini"); writes(f) {
		t.Errorf("config/app.ini plan = %+v, want nothing written through the link", f)
	}
	if got, _ := os.ReadFile(shared + "/app.ini"); string(got) != "shared\n" {
		t.Errorf("the shared file now holds %q", got)
	}
}

func TestAFolderReplacedByASubmoduleCanBeRecordedAndUndone(t *testing.T) {
	lib := testutil.NewRepo(t)
	lib.Write("f.c", "int lib;\n")
	lib.Commit("lib")
	repo := testutil.NewRepo(t)
	repo.Write("vendor/lib/f.c", "int vendored;\n")
	repo.Write("main.c", "int main;\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Patch the vendored copy", func() { repo.Write("vendor/lib/f.c", "int vendored, patched;\n") })

	start(t, a, app.StartOptions{Description: "Use the library as a submodule"})
	repo.Git("rm", "-rqf", "vendor/lib")
	repo.Git("-c", "protocol.file.allow=always", "submodule", "add", "-q", lib.Dir, "vendor/lib")
	if res := end(t, a, app.EndOptions{}); res.Turn == nil {
		t.Fatal("the turn recorded nothing")
	}

	p := planUndo(t, a, 1)
	if f := planned(t, p, "vendor/lib/f.c"); writes(f) {
		t.Errorf("vendor/lib/f.c plan = %+v, want nothing written inside the submodule", f)
	}
	if got := repo.Read("vendor/lib/f.c"); got != "int lib;\n" {
		t.Errorf("the submodule's file now holds %q", got)
	}
}

func TestNamesThatLookLikePathspecMagicWork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not allow : in file names")
	}
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Add notes", func() {
		repo.Write(":!notes", "remember\n")
		repo.Write(":(top)todo", "later\n")
	})
	repo.Remove(":!notes")
	// A later ignore rule and an untracked copy make git's view differ from
	// the disk, which is when turnback asks git what it ignores.
	repo.Write(".gitignore", ":(top)todo\n")

	p := planUndo(t, a, 1)
	got := actions(p)
	if got[":!notes"] != app.UndoSkip || got[":(top)todo"] != app.UndoSkip {
		t.Errorf("plan = %v, want both skipped", got)
	}
	if f := planned(t, p, ":(top)todo"); !strings.Contains(f.Reason, "ignores") {
		t.Errorf(":(top)todo plan = %+v, want it skipped as ignored", f)
	}
}
