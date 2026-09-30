package app_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/store"
	"github.com/Waiivyy/turnback/internal/testutil"
)

var words = []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}

// text joins lines into file content.
func text(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

// replaced returns a copy of lines with the given 0-based lines replaced.
func replaced(lines []string, changes map[int]string) []string {
	out := append([]string{}, lines...)
	for i, s := range changes {
		out[i] = s
	}
	return out
}

// turn records one turn made by edit and returns it.
func turn(t *testing.T, a *app.App, desc string, edit func()) *store.Turn {
	t.Helper()
	start(t, a, app.StartOptions{Description: desc})
	edit()
	res := end(t, a, app.EndOptions{})
	if res.Turn == nil {
		t.Fatalf("turn %q recorded no changes", desc)
	}
	return res.Turn
}

func planUndo(t *testing.T, a *app.App, id int, paths ...string) *app.UndoPlan {
	t.Helper()
	tr, err := a.Store.Turn(id)
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.PlanUndo(tr, app.UndoOptions{Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func applyUndo(t *testing.T, a *app.App, p *app.UndoPlan, force bool) *store.Turn {
	t.Helper()
	u, err := a.ApplyUndo(p, force)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func actions(p *app.UndoPlan) map[string]string {
	out := map[string]string{}
	for _, f := range p.Files {
		out[f.Path] = f.Action
	}
	return out
}

func planned(t *testing.T, p *app.UndoPlan, path string) app.UndoFile {
	t.Helper()
	for _, f := range p.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("plan has no entry for %s: %+v", path, p.Files)
	return app.UndoFile{}
}

func TestUndoRevertsEveryKindOfChange(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Write("old.txt", "bye\n")
	repo.Write("keep.txt", "keep\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Mixed changes", func() {
		repo.Write("a.txt", "one\ntwo\n")
		repo.Write("new.txt", "new\n")
		repo.Remove("old.txt")
	})

	p := planUndo(t, a, 1)
	want := map[string]string{"a.txt": app.UndoRevert, "new.txt": app.UndoDelete, "old.txt": app.UndoRestore}
	if got := actions(p); !reflect.DeepEqual(got, want) {
		t.Errorf("actions = %v, want %v", got, want)
	}
	if len(p.Conflicts()) != 0 || p.Writes() != 3 {
		t.Errorf("conflicts = %v, writes = %d", p.Conflicts(), p.Writes())
	}
	u := applyUndo(t, a, p, false)

	if got := repo.Read("a.txt"); got != "one\n" {
		t.Errorf("a.txt = %q", got)
	}
	if repo.Exists("new.txt") {
		t.Error("new.txt still exists")
	}
	if got := repo.Read("old.txt"); got != "bye\n" {
		t.Errorf("old.txt = %q", got)
	}
	if got := repo.Read("keep.txt"); got != "keep\n" {
		t.Errorf("keep.txt = %q", got)
	}
	if u.ID != 2 || u.Kind != store.KindUndo || u.Undoes == nil || u.Undoes.Turn != 1 {
		t.Errorf("undo turn = %+v", u)
	}
	if u.Description != "Undo turn 1: Mixed changes" {
		t.Errorf("undo description = %q", u.Description)
	}
	if got := paths(u.Files); !reflect.DeepEqual(got, []string{"M a.txt", "D new.txt", "A old.txt"}) {
		t.Errorf("undo turn files = %q", got)
	}
}

func TestUndoKeepsLaterChangesToTheSameFile(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("f.txt", text(words...))
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Shout two", func() { repo.Write("f.txt", text(replaced(words, map[int]string{1: "TWO"})...)) })
	turn(t, a, "Shout nine", func() {
		repo.Write("f.txt", text(replaced(words, map[int]string{1: "TWO", 8: "NINE"})...))
	})

	p := planUndo(t, a, 1)
	f := planned(t, p, "f.txt")
	if f.Action != app.UndoMerge || !reflect.DeepEqual(f.Later, []int{2}) || f.Outside {
		t.Errorf("f.txt plan = %+v, want a merge that names turn 2", f)
	}
	applyUndo(t, a, p, false)
	if got, want := repo.Read("f.txt"), text(replaced(words, map[int]string{8: "NINE"})...); got != want {
		t.Errorf("f.txt =\n%s\nwant\n%s", got, want)
	}
}

func TestUndoKeepsYourOwnEditsMadeBetweenTurns(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("f.txt", text(words...))
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Shout two", func() { repo.Write("f.txt", text(replaced(words, map[int]string{1: "TWO"})...)) })
	repo.Write("f.txt", text(replaced(words, map[int]string{1: "TWO", 4: "FIVE"})...)) // by hand
	turn(t, a, "Unrelated", func() { repo.Write("other.txt", "x\n") })

	p := planUndo(t, a, 1)
	f := planned(t, p, "f.txt")
	if f.Action != app.UndoMerge || !f.Outside || len(f.Later) != 0 || f.Dirty {
		t.Errorf("f.txt plan = %+v, want a clean merge that mentions edits outside turns", f)
	}
	applyUndo(t, a, p, false)
	if got, want := repo.Read("f.txt"), text(replaced(words, map[int]string{4: "FIVE"})...); got != want {
		t.Errorf("f.txt =\n%s\nwant\n%s", got, want)
	}
}

func TestOverlappingLaterEditIsAConflictAndNothingIsWritten(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("f.txt", text(words...))
	repo.Write("g.txt", "g\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Two files", func() {
		repo.Write("f.txt", text(replaced(words, map[int]string{4: "FIVE"})...))
		repo.Write("g.txt", "G\n")
	})
	turn(t, a, "Refine five", func() { repo.Write("f.txt", text(replaced(words, map[int]string{4: "Five!"})...)) })

	p := planUndo(t, a, 1)
	f := planned(t, p, "f.txt")
	if f.Action != app.UndoConflict || !reflect.DeepEqual(f.Later, []int{2}) {
		t.Errorf("f.txt plan = %+v, want a conflict naming turn 2", f)
	}
	for _, want := range []string{"<<<<<<< now", "Five!", "=======", "five", ">>>>>>> before turn 1"} {
		if !strings.Contains(f.Markers, want) {
			t.Errorf("conflict markers lack %q:\n%s", want, f.Markers)
		}
	}
	if planned(t, p, "g.txt").Action != app.UndoRevert {
		t.Errorf("g.txt plan = %+v", planned(t, p, "g.txt"))
	}
	if _, err := a.ApplyUndo(p, true); !errors.Is(err, app.ErrConflicts) {
		t.Fatalf("ApplyUndo err = %v, want ErrConflicts", err)
	}
	// All or nothing: the clean file was not reverted either.
	if got := repo.Read("g.txt"); got != "G\n" {
		t.Errorf("g.txt = %q, want it untouched", got)
	}
	if got := repo.Read("f.txt"); got != text(replaced(words, map[int]string{4: "Five!"})...) {
		t.Errorf("f.txt changed:\n%s", got)
	}
}

func TestEditsTouchingTheUndoneLinesConflict(t *testing.T) {
	repo := testutil.NewRepo(t)
	six := words[:6]
	repo.Write("f.txt", text(six...))
	repo.Commit("initial")
	a := openApp(t, repo)
	withInsert := []string{"one", "two", "three", "inserted", "four", "five", "six"}
	turn(t, a, "Insert a line", func() { repo.Write("f.txt", text(withInsert...)) })
	turn(t, a, "Edit the next line", func() {
		repo.Write("f.txt", text(replaced(withInsert, map[int]string{4: "FOUR"})...))
	})
	if f := planned(t, planUndo(t, a, 1), "f.txt"); f.Action != app.UndoConflict {
		t.Errorf("f.txt plan = %+v, want a conflict for an edit right next to the undone line", f)
	}
}

func TestFileCreatedByTheTurnAndEditedLaterConflicts(t *testing.T) {
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)
	turn(t, a, "Create", func() { repo.Write("new.txt", "v1\n") })
	turn(t, a, "Edit", func() { repo.Write("new.txt", "v2\n") })
	f := planned(t, planUndo(t, a, 1), "new.txt")
	if f.Action != app.UndoConflict || !strings.Contains(f.Reason, "created by turn 1") {
		t.Errorf("new.txt plan = %+v", f)
	}
}

func TestFileChangedByTheTurnAndDeletedLaterConflicts(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("f.txt", "v1\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Change", func() { repo.Write("f.txt", "v2\n") })
	turn(t, a, "Delete", func() { repo.Remove("f.txt") })
	f := planned(t, planUndo(t, a, 1), "f.txt")
	if f.Action != app.UndoConflict || !strings.Contains(f.Reason, "deleted") {
		t.Errorf("f.txt plan = %+v", f)
	}
}

func TestFileDeletedByTheTurnAndRecreatedLaterConflicts(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("f.txt", "original\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Delete", func() { repo.Remove("f.txt") })
	turn(t, a, "Recreate", func() { repo.Write("f.txt", "something else\n") })
	f := planned(t, planUndo(t, a, 1), "f.txt")
	if f.Action != app.UndoConflict || !strings.Contains(f.Reason, "created again") {
		t.Errorf("f.txt plan = %+v", f)
	}
}

func TestUndoingTwiceLeavesNothingToDo(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Change", func() { repo.Write("a.txt", "two\n") })
	applyUndo(t, a, planUndo(t, a, 1), false)

	p := planUndo(t, a, 1)
	if p.Writes() != 0 || planned(t, p, "a.txt").Action != app.UndoSkip {
		t.Errorf("second plan = %+v", p.Files)
	}
}

func TestUndoOneFileOfATurn(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "a1\n")
	repo.Write("b.txt", "b1\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Both", func() {
		repo.Write("a.txt", "a2\n")
		repo.Write("b.txt", "b2\n")
	})
	p := planUndo(t, a, 1, "a.txt")
	if got := actions(p); !reflect.DeepEqual(got, map[string]string{"a.txt": app.UndoRevert}) {
		t.Errorf("actions = %v", got)
	}
	u := applyUndo(t, a, p, false)
	if repo.Read("a.txt") != "a1\n" || repo.Read("b.txt") != "b2\n" {
		t.Errorf("a.txt = %q, b.txt = %q", repo.Read("a.txt"), repo.Read("b.txt"))
	}
	if u.Undoes == nil || !reflect.DeepEqual(u.Undoes.Paths, []string{"a.txt"}) {
		t.Errorf("undoes = %+v", u.Undoes)
	}
	if u.Description != "Undo a.txt from turn 1: Both" {
		t.Errorf("description = %q", u.Description)
	}
}

func TestUndoOfAnUndoPutsTheTurnBack(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Change", func() {
		repo.Write("a.txt", "two\n")
		repo.Write("b.txt", "added\n")
	})
	u := applyUndo(t, a, planUndo(t, a, 1), false)
	redo := applyUndo(t, a, planUndo(t, a, u.ID), false)

	if repo.Read("a.txt") != "two\n" || repo.Read("b.txt") != "added\n" {
		t.Errorf("after undoing the undo: a.txt = %q, b.txt exists = %v", repo.Read("a.txt"), repo.Exists("b.txt"))
	}
	if redo.Description != "Redo turn 1: Change" || redo.Undoes.Turn != u.ID {
		t.Errorf("redo turn = %+v", redo)
	}
}

func TestUndoARename(t *testing.T) {
	repo := testutil.NewRepo(t)
	doc := "a document long enough\nfor git to notice\nthat it was renamed\n"
	repo.Write("old.md", doc)
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Rename", func() {
		repo.Write("new.md", doc)
		repo.Remove("old.md")
	})
	// Asking for the new name undoes both halves of the rename.
	p := planUndo(t, a, 1, "new.md")
	if got := actions(p); !reflect.DeepEqual(got, map[string]string{"new.md": app.UndoDelete, "old.md": app.UndoRestore}) {
		t.Errorf("actions = %v", got)
	}
	applyUndo(t, a, p, false)
	if repo.Exists("new.md") || repo.Read("old.md") != doc {
		t.Errorf("new.md exists = %v, old.md = %q", repo.Exists("new.md"), repo.Read("old.md"))
	}
}

func TestUndoBinaryFiles(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("logo.bin", "\x00\x01\x02 version 1")
	repo.Write("icon.bin", "\x00\x01\x02 icon 1")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Change both", func() {
		repo.Write("logo.bin", "\x00\x01\x02 version 2")
		repo.Write("icon.bin", "\x00\x01\x02 icon 2")
	})
	turn(t, a, "Change icon again", func() { repo.Write("icon.bin", "\x00\x01\x02 icon 3") })

	p := planUndo(t, a, 1)
	if f := planned(t, p, "icon.bin"); f.Action != app.UndoConflict || !strings.Contains(f.Reason, "binary") {
		t.Errorf("icon.bin plan = %+v", f)
	}
	applyUndo(t, a, planUndo(t, a, 1, "logo.bin"), false)
	if got := repo.Read("logo.bin"); got != "\x00\x01\x02 version 1" {
		t.Errorf("logo.bin = %q", got)
	}
}

func TestUndoTheExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no executable bit on Windows")
	}
	repo := testutil.NewRepo(t)
	repo.Write("run.sh", "#!/bin/sh\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Make it executable", func() {
		if err := os.Chmod(repo.Path("run.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
	})
	applyUndo(t, a, planUndo(t, a, 1), false)
	fi, err := os.Stat(repo.Path("run.sh"))
	if err != nil || fi.Mode()&0o111 != 0 {
		t.Errorf("run.sh mode = %v, %v; want not executable", fi.Mode(), err)
	}
}

func TestUndoASymlinkChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need extra rights on Windows")
	}
	repo := testutil.NewRepo(t)
	if err := os.Symlink("target-a", repo.Path("link")); err != nil {
		t.Fatal(err)
	}
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Repoint", func() {
		repo.Remove("link")
		if err := os.Symlink("target-b", repo.Path("link")); err != nil {
			t.Fatal(err)
		}
	})
	applyUndo(t, a, planUndo(t, a, 1), false)
	if got, err := os.Readlink(repo.Path("link")); err != nil || got != "target-a" {
		t.Errorf("link -> %q, %v; want target-a", got, err)
	}
}

func TestUnsavedEditsBlockTheUndoUnlessForced(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("f.txt", text(words...))
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Shout two", func() { repo.Write("f.txt", text(replaced(words, map[int]string{1: "TWO"})...)) })
	unsaved := text(replaced(words, map[int]string{1: "TWO", 8: "NINE"})...)
	repo.Write("f.txt", unsaved) // neither committed nor recorded

	p := planUndo(t, a, 1)
	if f := planned(t, p, "f.txt"); !f.Dirty || f.Action != app.UndoMerge {
		t.Errorf("f.txt plan = %+v, want a merge flagged dirty", f)
	}
	var dirty *app.DirtyError
	if _, err := a.ApplyUndo(p, false); !errors.As(err, &dirty) || !reflect.DeepEqual(dirty.Paths, []string{"f.txt"}) {
		t.Fatalf("ApplyUndo err = %v, want DirtyError for f.txt", err)
	}
	if repo.Read("f.txt") != unsaved {
		t.Fatal("f.txt changed although the undo was refused")
	}

	u := applyUndo(t, a, p, true)
	if got, want := repo.Read("f.txt"), text(replaced(words, map[int]string{8: "NINE"})...); got != want {
		t.Errorf("after forced undo f.txt =\n%s\nwant\n%s", got, want)
	}
	// The unsaved edit was saved first: undoing the undo brings it all back.
	applyUndo(t, a, planUndo(t, a, u.ID), false)
	if repo.Read("f.txt") != unsaved {
		t.Errorf("after undoing the forced undo f.txt =\n%s\nwant\n%s", repo.Read("f.txt"), unsaved)
	}
}

func TestCommittedEditsAreNotUnsaved(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("f.txt", text(words...))
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Shout two", func() { repo.Write("f.txt", text(replaced(words, map[int]string{1: "TWO"})...)) })
	repo.Write("f.txt", text(replaced(words, map[int]string{1: "TWO", 8: "NINE"})...))
	repo.Commit("my edit")

	p := planUndo(t, a, 1)
	if planned(t, p, "f.txt").Dirty {
		t.Error("a committed edit was flagged as unsaved")
	}
	applyUndo(t, a, p, false)
	if got, want := repo.Read("f.txt"), text(replaced(words, map[int]string{8: "NINE"})...); got != want {
		t.Errorf("f.txt =\n%s\nwant\n%s", got, want)
	}
}

func TestUndoNeverOverwritesAnIgnoredFile(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("config.local", "from the agent\n") // untracked, not ignored yet
	a := openApp(t, repo)
	turn(t, a, "Delete it", func() { repo.Remove("config.local") })
	repo.Write(".gitignore", "config.local\n")
	repo.Write("config.local", "SECRET=1\n")

	p := planUndo(t, a, 1)
	if f := planned(t, p, "config.local"); f.Action != app.UndoSkip || !strings.Contains(f.Reason, "ignores it") {
		t.Errorf("config.local plan = %+v, want it skipped because git ignores it now", f)
	}
	if _, err := a.ApplyUndo(p, true); !errors.Is(err, app.ErrNothingToUndo) {
		t.Errorf("ApplyUndo err = %v, want ErrNothingToUndo", err)
	}
	if got := repo.Read("config.local"); got != "SECRET=1\n" {
		t.Errorf("config.local = %q, want it untouched", got)
	}
}

func TestUndoRefusesWhileATurnIsBeingRecorded(t *testing.T) {
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)
	tr := turn(t, a, "Add", func() { repo.Write("a.txt", "x\n") })
	start(t, a, app.StartOptions{})
	var active *app.SessionActiveError
	if _, err := a.PlanUndo(tr, app.UndoOptions{}); !errors.As(err, &active) {
		t.Errorf("err = %v, want SessionActiveError", err)
	}
}

func TestFilesChangedAfterThePlanAreNotOverwritten(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Change", func() { repo.Write("a.txt", "two\n") })
	p := planUndo(t, a, 1)
	repo.Write("a.txt", "three, typed while reading the preview\n")

	var changed *app.ChangedError
	if _, err := a.ApplyUndo(p, true); !errors.As(err, &changed) || !reflect.DeepEqual(changed.Paths, []string{"a.txt"}) {
		t.Fatalf("err = %v, want ChangedError for a.txt", err)
	}
	if got := repo.Read("a.txt"); got != "three, typed while reading the preview\n" {
		t.Errorf("a.txt = %q", got)
	}
}

func TestUnrelatedEditsAfterThePlanAreKept(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Change", func() { repo.Write("a.txt", "two\n") })
	p := planUndo(t, a, 1)
	repo.Write("b.txt", "written meanwhile\n")

	u := applyUndo(t, a, p, false)
	if repo.Read("a.txt") != "one\n" || repo.Read("b.txt") != "written meanwhile\n" {
		t.Errorf("a.txt = %q, b.txt = %q", repo.Read("a.txt"), repo.Read("b.txt"))
	}
	if got := paths(u.Files); !reflect.DeepEqual(got, []string{"M a.txt"}) {
		t.Errorf("undo turn files = %q, want only a.txt", got)
	}
}

func TestUndoPutsAFileBackWhereAFolderReplacedIt(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("utils.ts", "export const a = 1;\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Split utils", func() {
		repo.Remove("utils.ts")
		repo.Write("utils/index.ts", "export const a = 2;\n")
	})
	applyUndo(t, a, planUndo(t, a, 1), false)
	if repo.Read("utils.ts") != "export const a = 1;\n" || repo.Exists("utils") {
		t.Errorf("utils.ts = %q, utils/ exists = %v", repo.Read("utils.ts"), repo.Exists("utils"))
	}
}

func TestPreviewShowsTheDiffTheUndoWouldApply(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Add a line", func() { repo.Write("a.txt", "one\ntwo\n") })
	p := planUndo(t, a, 1)
	for _, want := range []string{"diff --git a/a.txt b/a.txt", " one", "-two"} {
		if !strings.Contains(p.Preview, want) {
			t.Errorf("preview lacks %q:\n%s", want, p.Preview)
		}
	}
	if got := repo.Read("a.txt"); got != "one\ntwo\n" {
		t.Errorf("planning changed a.txt to %q", got)
	}
}

func TestSubmodulesAreLeftAlone(t *testing.T) {
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)
	turn(t, a, "Vendor a library", func() {
		lib := repo.Path("vendor/lib")
		if err := os.MkdirAll(lib, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "lib"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = lib
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		repo.Write("a.txt", "x\n")
	})
	p := planUndo(t, a, 1)
	if f := planned(t, p, "vendor/lib"); f.Action != app.UndoSkip || !strings.Contains(f.Reason, "submodule") {
		t.Errorf("vendor/lib plan = %+v", f)
	}
	applyUndo(t, a, p, false)
	if repo.Exists("a.txt") || !repo.Exists("vendor/lib/.git") {
		t.Errorf("a.txt exists = %v, vendor/lib/.git exists = %v", repo.Exists("a.txt"), repo.Exists("vendor/lib/.git"))
	}
}

func TestAnUndoThatCannotFinishChangesNothingAndLeavesNoTrace(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	repo := testutil.NewRepo(t)
	repo.Write("ro/b.txt", "b1\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Change b, add notes", func() {
		repo.Write("ro/b.txt", "b2\n")
		repo.Write("zz-notes.md", "notes\n")
	})
	p := planUndo(t, a, 1)
	if err := os.Chmod(repo.Path("ro"), 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(repo.Path("ro"), 0o755)

	var aborted *app.UndoAbortedError
	if _, err := a.ApplyUndo(p, false); !errors.As(err, &aborted) {
		t.Fatalf("ApplyUndo err = %v, want UndoAbortedError", err)
	}
	if repo.Read("zz-notes.md") != "notes\n" || repo.Read("ro/b.txt") != "b2\n" {
		t.Errorf("files changed: zz-notes.md exists = %v, ro/b.txt = %q", repo.Exists("zz-notes.md"), repo.Read("ro/b.txt"))
	}
	if turns, _ := a.Store.Turns(); len(turns) != 1 {
		t.Errorf("%d turns recorded, want only the original one", len(turns))
	}
	if refs := repo.Git("--git-dir=.turnback/git", "for-each-ref", "--format=%(refname)"); refs != "refs/turns/1" {
		t.Errorf("private refs = %q, want only refs/turns/1", refs)
	}

	// Once the folder is writable again the undo works, and no turn id was used up.
	os.Chmod(repo.Path("ro"), 0o755)
	u := applyUndo(t, a, planUndo(t, a, 1), false)
	if u.ID != 2 || repo.Read("ro/b.txt") != "b1\n" || repo.Exists("zz-notes.md") {
		t.Errorf("retry: turn %d, ro/b.txt = %q, zz-notes.md exists = %v", u.ID, repo.Read("ro/b.txt"), repo.Exists("zz-notes.md"))
	}
}

func TestUndoLeavesAFileAloneOnceGitIgnoresIt(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("app.js", "v1\n")
	repo.Git("add", "app.js")
	repo.Git("commit", "-q", "-m", "initial")
	repo.Write("config.local", "a\nb\nc\n") // untracked, not ignored yet
	a := openApp(t, repo)
	turn(t, a, "Tweak both", func() {
		repo.Write("app.js", "v2\n")
		repo.Write("config.local", "a\nB\nc\n")
	})
	repo.Write(".gitignore", "config.local\n")
	repo.Write("config.local", "a\nB\nc\nSECRET=hunter2\n")
	repo.Git("add", ".gitignore")
	repo.Git("commit", "-q", "-m", "ignore config.local")

	p := planUndo(t, a, 1)
	if f := planned(t, p, "config.local"); f.Action != app.UndoSkip || !strings.Contains(f.Reason, "ignored") {
		t.Errorf("config.local plan = %+v, want it skipped as ignored", f)
	}
	u := applyUndo(t, a, p, false)
	if repo.Read("app.js") != "v1\n" {
		t.Errorf("app.js = %q", repo.Read("app.js"))
	}
	if got := repo.Read("config.local"); got != "a\nB\nc\nSECRET=hunter2\n" {
		t.Errorf("config.local = %q, want it untouched", got)
	}
	if listed := repo.Git("--git-dir=.turnback/git", "ls-tree", "-r", "--name-only", u.Before); strings.Contains(listed, "config.local") {
		t.Errorf("the ignored file was read into the snapshot before the undo:\n%s", listed)
	}
}

func TestUndoCoversTrackedFilesThatMatchAnIgnorePattern(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".gitignore", "*.lock\n")
	repo.Write("deps.lock", "v1\n")
	repo.Write("app.js", "v1\n")
	repo.Git("add", "-f", "deps.lock")
	repo.Commit("initial")
	a := openApp(t, repo)
	tr := turn(t, a, "Bump", func() {
		repo.Write("deps.lock", "v2\n")
		repo.Write("app.js", "v2\n")
	})
	if got := paths(tr.Files); !reflect.DeepEqual(got, []string{"M app.js", "M deps.lock"}) {
		t.Errorf("turn files = %q, want deps.lock recorded too", got)
	}
	applyUndo(t, a, planUndo(t, a, 1), false)
	if repo.Read("deps.lock") != "v1\n" || repo.Read("app.js") != "v1\n" {
		t.Errorf("deps.lock = %q, app.js = %q", repo.Read("deps.lock"), repo.Read("app.js"))
	}
}

func TestSkipWorktreeEditsStillCountAsUnsaved(t *testing.T) {
	repo := testutil.NewRepo(t)
	lines := "1\n2\n3\n4\n5\n6\n7\n8\n"
	repo.Write("settings.json", lines)
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Change line 2", func() { repo.Write("settings.json", strings.Replace(lines, "2\n", "TWO\n", 1)) })
	repo.Commit("keep the turn")
	repo.Git("update-index", "--skip-worktree", "settings.json")
	repo.Write("settings.json", strings.Replace(strings.Replace(lines, "2\n", "TWO\n", 1), "8\n", "MY-LOCAL-TOKEN\n", 1))
	if got := repo.Git("status", "--porcelain"); got != "" {
		t.Fatalf("expected git status to hide the edit, got %q", got)
	}

	p := planUndo(t, a, 1)
	if f := planned(t, p, "settings.json"); !f.Dirty {
		t.Errorf("settings.json plan = %+v, want it flagged as unsaved", f)
	}
	var dirty *app.DirtyError
	if _, err := a.ApplyUndo(p, false); !errors.As(err, &dirty) {
		t.Errorf("ApplyUndo err = %v, want DirtyError", err)
	}
}

func TestUndoAndShowATurnTooBigForACommandLine(t *testing.T) {
	if testing.Short() {
		t.Skip("creates thousands of files")
	}
	repo := testutil.NewRepo(t)
	repo.Write("keep.txt", "k\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	// 3,000 paths of about 800 characters: 2.4 MB, more than macOS or
	// Linux accept as command-line arguments.
	deep := strings.TrimSuffix(strings.Repeat("deeply-nested-generated-folder-level/", 21), "/")
	tr := turn(t, a, "Generate a client", func() {
		for i := 0; i < 3000; i++ {
			repo.Write(fmt.Sprintf("generated/%s/model_%04d.ts", deep, i), "export {}\n")
		}
	})
	diff, err := a.Diff(tr, []string{"generated"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	for _, want := range []string{"model_0000.ts", "model_2999.ts"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff lacks %s", want)
		}
	}
	p := planUndo(t, a, 1, "generated")
	if p.Writes() != 3000 {
		t.Fatalf("plan writes %d files, want 3000", p.Writes())
	}
	applyUndo(t, a, p, false)
	if repo.Exists("generated") || repo.Read("keep.txt") != "k\n" {
		t.Errorf("generated/ exists = %v, keep.txt = %q", repo.Exists("generated"), repo.Read("keep.txt"))
	}
}

func TestAFolderOfIgnoredFilesBlocksARestoreAtPlanTime(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".gitignore", "*.log\n")
	repo.Write("utils", "a file\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Make utils a folder", func() {
		repo.Remove("utils")
		repo.Write("utils/index.ts", "export {}\n")
	})
	repo.Write("utils/debug.log", "ignored noise\n")

	f := planned(t, planUndo(t, a, 1), "utils")
	if f.Action != app.UndoConflict || !strings.Contains(f.Reason, "folder") {
		t.Errorf("utils plan = %+v, want a conflict about the folder in the way", f)
	}
}

// caseInsensitive reports whether dir's file system ignores case.
func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "CaseProbe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(probe)
	_, err := os.Lstat(filepath.Join(dir, "caseprobe"))
	return err == nil
}

func TestACaseOnlyNameClashIsExplained(t *testing.T) {
	repo := testutil.NewRepo(t)
	if !caseInsensitive(t, repo.Dir) {
		t.Skip("the file system tells upper and lower case apart")
	}
	repo.Write("Makefile", "all:\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	turn(t, a, "Drop the Makefile", func() { repo.Remove("Makefile") })
	turn(t, a, "Add a lowercase one", func() { repo.Write("makefile", "build:\n") })

	f := planned(t, planUndo(t, a, 1), "Makefile")
	if f.Action != app.UndoConflict || !strings.Contains(f.Reason, "makefile") || !strings.Contains(f.Reason, "case") {
		t.Errorf("Makefile plan = %+v, want a conflict naming makefile and case", f)
	}
}
