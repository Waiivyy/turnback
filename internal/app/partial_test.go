package app

import (
	"errors"
	"testing"
	"time"

	"github.com/Waiivyy/turnback/internal/shadow"
	"github.com/Waiivyy/turnback/internal/testutil"
)

// A rollback that cannot put everything back is rare and hard to provoke on
// purpose, so this drives the recording step directly: whatever an
// interrupted undo changed must become a turn that undoes cleanly.
func TestAPartialUndoIsRecordedAndCanBeTakenBack(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "a1\n")
	repo.Write("b.txt", "b1\n")
	repo.Commit("initial")
	a, err := Open(repo.Dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(StartOptions{}); err != nil {
		t.Fatal(err)
	}
	repo.Write("a.txt", "a2\n")
	repo.Write("b.txt", "b2\n")
	res, err := a.End(EndOptions{Description: "Change both"})
	if err != nil {
		t.Fatal(err)
	}

	sh, unlock, err := a.begin()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sh.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	before, err := sh.Commit(tree, "", "before")
	if err != nil {
		t.Fatal(err)
	}
	repo.Write("a.txt", "a1\n") // the undo got this far before it stopped
	cause := &shadow.CheckoutError{Cause: errors.New("disk full"), Leftover: []string{"b.txt"}}
	partial, err := a.recordPartial(sh, res.Turn, nil, before, cause)
	unlock()
	if err != nil {
		t.Fatal(err)
	}

	if partial.Description != "Partial undo of turn 1: Change both" || partial.Undoes.Turn != 1 {
		t.Errorf("partial turn = %+v", partial)
	}
	if len(partial.Files) != 1 || partial.Files[0].Path != "a.txt" {
		t.Errorf("partial turn files = %+v, want only a.txt", partial.Files)
	}
	p, err := a.PlanUndo(partial, UndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyUndo(p, false); err != nil {
		t.Fatal(err)
	}
	if repo.Read("a.txt") != "a2\n" || repo.Read("b.txt") != "b2\n" {
		t.Errorf("after undoing the partial undo: a.txt = %q, b.txt = %q", repo.Read("a.txt"), repo.Read("b.txt"))
	}
}
