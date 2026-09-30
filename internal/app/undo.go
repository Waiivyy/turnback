package app

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Waiivyy/turnback/internal/git"
	"github.com/Waiivyy/turnback/internal/shadow"
	"github.com/Waiivyy/turnback/internal/store"
)

// What an undo does with one file.
const (
	UndoRevert   = "revert"   // put back the version from before the turn
	UndoRestore  = "restore"  // bring back a file the turn deleted
	UndoDelete   = "delete"   // remove a file the turn created
	UndoMerge    = "merge"    // take the turn's change out, keep later changes
	UndoSkip     = "skip"     // nothing to do
	UndoConflict = "conflict" // cannot be undone automatically
)

var (
	// ErrConflicts means at least one file cannot be undone cleanly.
	ErrConflicts = errors.New("some files cannot be undone cleanly")
	// ErrNothingToUndo means every file already matches its state before the turn.
	ErrNothingToUndo = errors.New("nothing to undo")
)

// DirtyError means the undo would rewrite files whose current changes are
// neither committed nor recorded in any turn.
type DirtyError struct {
	Paths []string
}

func (e *DirtyError) Error() string {
	return "changes that are neither committed nor recorded: " + strings.Join(e.Paths, ", ")
}

// ChangedError means files the undo would write changed after it was
// planned.
type ChangedError struct {
	Paths []string
}

func (e *ChangedError) Error() string {
	return "changed after the undo was planned: " + strings.Join(e.Paths, ", ")
}

// UndoOptions configures PlanUndo.
type UndoOptions struct {
	Paths []string // only undo files at or under these repository paths
}

// UndoFile is what an undo does with one file.
type UndoFile struct {
	Path    string
	Action  string // one of the Undo constants
	Reason  string // why the file is skipped or in conflict
	Later   []int  // later turns that changed the file too
	Outside bool   // the file also changed outside any recorded turn since
	Markers string // for text conflicts, the conflicting regions with markers
	Dirty   bool   // has changes that are neither committed nor recorded
}

func (f UndoFile) writes() bool {
	return f.Action != UndoSkip && f.Action != UndoConflict
}

// UndoPlan is a dry run of an undo: what it would do to each file and the
// diff it would apply. ApplyUndo carries it out.
type UndoPlan struct {
	Turn    *store.Turn
	Paths   []string // the paths the undo is limited to, if any
	Files   []UndoFile
	Preview string // diff from the current files to the result
	Later   []int  // turns recorded after this one, apart from undos of it

	current string   // snapshot the plan was made against
	target  string   // tree of the result
	writes  []string // paths the undo writes
}

// Conflicts returns the files that cannot be undone cleanly.
func (p *UndoPlan) Conflicts() []UndoFile {
	var out []UndoFile
	for _, f := range p.Files {
		if f.Action == UndoConflict {
			out = append(out, f)
		}
	}
	return out
}

// Writes returns how many files the undo would change.
func (p *UndoPlan) Writes() int { return len(p.writes) }

// DirtyPaths returns the files the undo would rewrite that have changes
// which are neither committed nor recorded.
func (p *UndoPlan) DirtyPaths() []string {
	var out []string
	for _, f := range p.Files {
		if f.Dirty && f.writes() {
			out = append(out, f.Path)
		}
	}
	return out
}

// PlanUndo works out what undoing t would do to the current working tree,
// without changing any file.
func (a *App) PlanUndo(t *store.Turn, opts UndoOptions) (*UndoPlan, error) {
	sh, unlock, err := a.begin()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if sess, err := a.Store.Session(); err != nil {
		return nil, err
	} else if sess != nil {
		return nil, &SessionActiveError{Session: sess}
	}
	current, err := sh.Snapshot()
	if err != nil {
		return nil, err
	}
	return a.plan(sh, t, current, opts.Paths)
}

func (a *App) plan(sh *shadow.Repo, t *store.Turn, current string, limit []string) (*UndoPlan, error) {
	p := &UndoPlan{Turn: t, Paths: limit, current: current}
	var scope []string
	if len(limit) > 0 {
		files := FilesTouching(t, limit)
		if len(files) == 0 {
			return nil, &NotInTurnError{Turn: t, Paths: limit}
		}
		for _, f := range files {
			scope = append(scope, f.Path)
			if f.OldPath != "" {
				scope = append(scope, f.OldPath)
			}
		}
	}
	changes, err := sh.TreeDiff(t.Before, t.After, scope...)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		return p, nil
	}
	paths := make([]string, len(changes))
	for i, c := range changes {
		paths[i] = c.Path
	}
	since, err := sh.TreeDiff(t.After, current, paths...)
	if err != nil {
		return nil, err
	}
	now := make(map[string]*shadow.Entry)
	changedSince := make(map[string]bool)
	for _, c := range since {
		now[c.Path], changedSince[c.Path] = c.New, true
	}

	// A path missing from the current snapshot may be deleted, or ignored
	// by git by now; git says which. Ignored paths are never written: a file
	// written there would be outside every later snapshot, so the undo could
	// not be undone.
	var absent []string
	for _, c := range changes {
		if cur := c.New; (changedSince[c.Path] && now[c.Path] == nil) || (!changedSince[c.Path] && cur == nil) {
			absent = append(absent, c.Path)
		}
	}
	ignored, err := git.Ignored(a.Root, absent)
	if err != nil {
		return nil, err
	}

	// Pass 1: decide what to do with each file on its own.
	targets := make(map[string]*shadow.Entry)
	unrestored := make(map[string]bool) // files the turn deleted that stay deleted
	for _, c := range changes {
		cur := c.New
		if changedSince[c.Path] {
			cur = now[c.Path]
		}
		f := UndoFile{Path: c.Path}
		if ignored[c.Path] {
			if c.Old != nil && c.New == nil {
				unrestored[c.Path] = true
			}
			f.Action, f.Reason = UndoSkip, "git ignores it now, and turnback leaves ignored files alone"
			p.Files = append(p.Files, f)
			continue
		}
		target, err := decide(sh, t, &f, c.Old, c.New, cur)
		if err != nil {
			return nil, err
		}
		if f.writes() {
			targets[c.Path] = target
		}
		p.Files = append(p.Files, f)
	}

	// A rename is only undone whole. If its old name cannot come back, the
	// new name stays too: deleting it would leave neither.
	for _, tf := range t.Files {
		if tf.OldPath == "" || !unrestored[tf.OldPath] {
			continue
		}
		for i := range p.Files {
			if f := &p.Files[i]; f.Path == tf.Path && f.Action == UndoDelete {
				f.Action, f.Reason = UndoSkip, fmt.Sprintf("kept: turn %d renamed %s to it, and git ignores %s now", t.ID, tf.OldPath, tf.OldPath)
				delete(targets, f.Path)
			}
		}
	}

	// Pass 2: a file brought back must have room, unless this same undo
	// clears the way.
	if err := a.checkRoom(sh, p, targets, current); err != nil {
		return nil, err
	}
	listings := make(map[string][]string) // folder listings, read once each
	for i := range p.Files {
		f := &p.Files[i]
		if f.Action != UndoConflict {
			continue
		}
		if name := a.spelledOnDisk(f.Path, listings); name != "" {
			f.Reason += fmt.Sprintf(" (on disk it is spelled %s, and this file system does not tell names apart by case)", name)
		}
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })

	turns, err := a.Store.Turns()
	if err != nil {
		return nil, err
	}
	for i := len(turns) - 1; i >= 0; i-- {
		if u := turns[i]; u.ID > t.ID && (u.Undoes == nil || u.Undoes.Turn != t.ID) {
			p.Later = append(p.Later, u.ID)
		}
	}
	for i := range p.Files {
		f := &p.Files[i]
		if f.Action == UndoMerge || f.Action == UndoConflict {
			if f.Later, f.Outside, err = history(sh, t, turns, f.Path, current); err != nil {
				return nil, err
			}
		}
		if f.writes() {
			p.writes = append(p.writes, f.Path)
		}
	}
	if err := a.markDirty(sh, p, turns, current); err != nil {
		return nil, err
	}
	if len(p.Conflicts()) > 0 || len(p.writes) == 0 {
		return p, nil
	}
	final := make(map[string]*shadow.Entry, len(p.writes))
	for _, path := range p.writes {
		final[path] = targets[path]
	}
	if p.target, err = sh.BuildTree(current, final); err != nil {
		return nil, err
	}
	if p.Preview, err = sh.Diff(current, p.target); err != nil {
		return nil, err
	}
	return p, nil
}

// decide works out one file's action from its version before the turn,
// after the turn and now, and returns the entry it should end up with.
func decide(sh *shadow.Repo, t *store.Turn, f *UndoFile, before, after, cur *shadow.Entry) (*shadow.Entry, error) {
	conflict := func(format string, args ...any) (*shadow.Entry, error) {
		f.Action, f.Reason = UndoConflict, fmt.Sprintf(format, args...)
		return nil, nil
	}
	switch {
	case before.Submodule() || after.Submodule() || cur.Submodule():
		f.Action, f.Reason = UndoSkip, "submodule, which turnback leaves alone"
		return nil, nil
	case shadow.Same(cur, before):
		f.Action, f.Reason = UndoSkip, fmt.Sprintf("already as it was before turn %d", t.ID)
		return nil, nil
	case shadow.Same(cur, after):
		switch {
		case before == nil:
			f.Action = UndoDelete
		case after == nil:
			f.Action = UndoRestore
		default:
			f.Action = UndoRevert
		}
		return before, nil
	case before == nil:
		return conflict("created by turn %d and edited since", t.ID)
	case after == nil:
		return conflict("deleted by turn %d and created again since, with different content", t.ID)
	case cur == nil:
		return conflict("changed by turn %d and deleted since", t.ID)
	case before.Symlink() || after.Symlink() || cur.Symlink():
		return conflict("symlink changed again since turn %d", t.ID)
	}

	// All three versions exist and the file changed since the turn: take
	// the turn's change out and keep everything else.
	mode, ok := merge3(after.Mode, cur.Mode, before.Mode)
	if !ok {
		return conflict("file mode changed again since turn %d", t.ID)
	}
	id := cur.ID
	switch {
	case cur.ID == after.ID: // only the mode changed since
		id = before.ID
	case before.ID == after.ID: // the turn only changed the mode
		id = cur.ID
	default:
		var blobs [3][]byte
		for i, e := range []*shadow.Entry{cur, after, before} {
			b, err := sh.ReadBlob(e.ID)
			if err != nil {
				return nil, err
			}
			if isBinary(b) {
				return conflict("binary file changed again since turn %d", t.ID)
			}
			blobs[i] = b
		}
		labels := [3]string{"now", fmt.Sprintf("turn %d", t.ID), fmt.Sprintf("before turn %d", t.ID)}
		merged, conflicts, err := sh.Merge3(blobs[0], blobs[1], blobs[2], labels)
		if err != nil {
			return nil, err
		}
		if conflicts > 0 {
			f.Markers = conflictRegions(merged)
			return conflict("later edits overlap the lines turn %d changed", t.ID)
		}
		if id, err = sh.WriteBlob(merged); err != nil {
			return nil, err
		}
	}
	target := &shadow.Entry{Mode: mode, ID: id}
	if shadow.Same(target, cur) {
		f.Action, f.Reason = UndoSkip, fmt.Sprintf("already as it was before turn %d", t.ID)
		return nil, nil
	}
	f.Action = UndoMerge
	return target, nil
}

// spelledOnDisk returns the name a file really has on disk when it differs
// from path only by case, as it can on case-insensitive file systems.
// listings caches folder contents across calls.
func (a *App) spelledOnDisk(path string, listings map[string][]string) string {
	dir, base := filepath.Split(filepath.Join(a.Root, filepath.FromSlash(path)))
	entries, ok := listings[dir]
	if !ok {
		if list, err := os.ReadDir(dir); err == nil {
			for _, e := range list {
				entries = append(entries, e.Name())
			}
		}
		listings[dir] = entries
	}
	for _, name := range entries {
		if name != base && strings.EqualFold(name, base) {
			return name
		}
	}
	return ""
}

// merge3 merges a file mode the way git does: keep a change made on one
// side only.
func merge3(base, ours, theirs string) (string, bool) {
	switch {
	case ours == base:
		return theirs, true
	case theirs == base, ours == theirs:
		return ours, true
	}
	return "", false
}

// isBinary uses git's rule: a NUL byte in the first 8000 bytes.
func isBinary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0
}

// checkRoom turns a restore into a conflict when something the undo does
// not remove stands where the file must go.
func (a *App) checkRoom(sh *shadow.Repo, p *UndoPlan, targets map[string]*shadow.Entry, current string) error {
	var restores []int
	for i, f := range p.Files {
		if f.Action == UndoRestore {
			restores = append(restores, i)
		}
	}
	if len(restores) == 0 {
		return nil
	}
	list, err := sh.Paths(current)
	if err != nil {
		return err
	}
	room := &roomCheck{
		root:    a.Root,
		tracked: make(map[string]bool, len(list)),
		folded:  make(map[string][]string, len(list)),
		removed: make(map[string]bool),
		holds:   make(map[string]bool),
	}
	for _, path := range list {
		room.tracked[path] = true
		room.folded[strings.ToLower(path)] = append(room.folded[strings.ToLower(path)], path)
	}
	for _, f := range p.Files {
		if f.writes() && targets[f.Path] == nil {
			room.removed[f.Path] = true
			parts := strings.Split(f.Path, "/")
			for i := 1; i < len(parts); i++ {
				room.holds[strings.Join(parts[:i], "/")] = true
			}
		}
	}
	for _, i := range restores {
		f := &p.Files[i]
		if reason := room.obstacle(f.Path); reason != "" {
			f.Action, f.Reason = UndoConflict, reason
		}
	}
	return nil
}

// roomCheck answers, for one plan, what stands where a file must come back.
type roomCheck struct {
	root    string
	tracked map[string]bool     // paths in the current snapshot
	folded  map[string][]string // tracked paths by lower-case spelling
	removed map[string]bool     // paths this undo deletes
	holds   map[string]bool     // folders that contain a path this undo deletes
}

// obstacle describes what stands where path must be created, or returns "".
func (rc *roomCheck) obstacle(path string) string {
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		parent := strings.Join(parts[:i], "/")
		if rc.tracked[parent] && !rc.removed[parent] {
			return fmt.Sprintf("cannot bring it back: %s is a file now", parent)
		}
		fi, err := os.Lstat(filepath.Join(rc.root, filepath.FromSlash(parent)))
		if err == nil && !rc.tracked[parent] && (!fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
			return fmt.Sprintf("cannot bring it back: %s, which git does not track, is in the way", parent)
		}
	}
	fi, err := os.Lstat(filepath.Join(rc.root, filepath.FromSlash(path)))
	if err != nil {
		return ""
	}
	if fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 {
		if !rc.clearable(path) {
			return "cannot bring it back: a folder with that name is in the way"
		}
		return ""
	}
	// On a case-insensitive disk, what stands there may be the same file
	// under another spelling. If this undo deletes it first, there is room.
	var twins []string
	for _, other := range rc.folded[strings.ToLower(path)] {
		if other != path {
			twins = append(twins, other)
		}
	}
	for _, other := range twins {
		if !rc.removed[other] {
			return fmt.Sprintf("cannot bring it back: %s is in the way, and this file system does not tell names apart by case", other)
		}
	}
	if len(twins) > 0 {
		return ""
	}
	return "cannot bring it back: a file git does not track, probably an ignored one, is in the way"
}

// clearable reports whether the folder at path disappears once the undo
// deletes the files it removes: every file in it must be a tracked file
// the undo deletes, and every folder in it must hold at least one, because
// git only removes folders it empties.
func (rc *roomCheck) clearable(path string) bool {
	if !rc.holds[path] {
		return false
	}
	root := filepath.Join(rc.root, filepath.FromSlash(path))
	ok := true
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			ok = false
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		rel = path + "/" + filepath.ToSlash(rel)
		if d.IsDir() && !rc.holds[rel] || !d.IsDir() && !rc.removed[rel] {
			ok = false
			return filepath.SkipAll
		}
		return nil
	})
	return ok
}

// history lists the later turns that changed path, and whether it also
// changed outside any turn: between two turns or since the last one.
func history(sh *shadow.Repo, t *store.Turn, turns []*store.Turn, path, current string) ([]int, bool, error) {
	var later []int
	outside := false
	prev := t.After
	for i := len(turns) - 1; i >= 0; i-- { // oldest first
		u := turns[i]
		if u.ID <= t.ID {
			continue
		}
		gap, err := sh.TreeDiff(prev, u.Before, path)
		if err != nil {
			return nil, false, err
		}
		outside = outside || len(gap) > 0
		for _, f := range u.Files {
			if f.Path == path || f.OldPath == path {
				later = append(later, u.ID)
				break
			}
		}
		prev = u.After
	}
	gap, err := sh.TreeDiff(prev, current, path)
	if err != nil {
		return nil, false, err
	}
	return later, outside || len(gap) > 0, nil
}

// markDirty flags files the undo would write whose current content is
// neither recorded in the latest snapshot nor committed, comparing content
// rather than trusting git status.
func (a *App) markDirty(sh *shadow.Repo, p *UndoPlan, turns []*store.Turn, current string) error {
	if len(p.writes) == 0 || len(turns) == 0 {
		return nil
	}
	unrecorded, err := sh.TreeDiff(turns[0].After, current, p.writes...)
	if err != nil || len(unrecorded) == 0 {
		return err
	}
	var candidates []string
	for _, c := range unrecorded {
		candidates = append(candidates, c.Path)
	}
	committed, err := git.Committed(a.Root, candidates)
	if err != nil {
		return err
	}
	unrecordedPaths := make(map[string]bool, len(candidates))
	for _, path := range candidates {
		unrecordedPaths[path] = true
	}
	for i := range p.Files {
		f := &p.Files[i]
		f.Dirty = unrecordedPaths[f.Path] && !committed[f.Path] && f.writes()
	}
	return nil
}

// conflictRegions extracts up to three conflicting regions, with two lines
// of context around each, from merge output.
func conflictRegions(merged []byte) string {
	lines := strings.SplitAfter(string(merged), "\n")
	var out []string
	regions := 0
	for i := 0; i < len(lines) && regions < 3; i++ {
		if !strings.HasPrefix(lines[i], "<<<<<<< ") {
			continue
		}
		j := i
		for j < len(lines) && !strings.HasPrefix(lines[j], ">>>>>>> ") {
			j++
		}
		if regions > 0 {
			out = append(out, "...\n")
		}
		out = append(out, lines[max(0, i-2):min(len(lines), j+3)]...)
		regions++
		i = j
	}
	return strings.Join(out, "")
}

// pendingRef keeps the state from right before an undo alive while the
// undo is being applied.
const pendingRef = "refs/turnback/pending-undo"

// UndoAbortedError means an undo stopped before it could finish and put
// every file back as it was: nothing was changed.
type UndoAbortedError struct {
	Cause error
}

func (e *UndoAbortedError) Error() string {
	return fmt.Sprintf("the undo stopped and nothing was changed: %v", e.Cause)
}

func (e *UndoAbortedError) Unwrap() error { return e.Cause }

// PartialUndoError means an undo stopped partway and could not put every
// file back. What it did change is recorded as Turn, which can be undone.
type PartialUndoError struct {
	Turn  *store.Turn
	Cause error
}

func (e *PartialUndoError) Error() string {
	return fmt.Sprintf("the undo stopped partway: %v", e.Cause)
}

func (e *PartialUndoError) Unwrap() error { return e.Cause }

// ApplyUndo carries out a plan and records the undo as a new turn. It
// refuses when the plan has conflicts, when it would rewrite unsaved work
// (unless force is set), or when a file it would write changed after the
// plan was made. If the undo cannot finish, it either changes nothing
// (*UndoAbortedError) or records what it changed as a turn of its own
// (*PartialUndoError), so no change is ever left unrecorded.
func (a *App) ApplyUndo(p *UndoPlan, force bool) (*store.Turn, error) {
	if len(p.Conflicts()) > 0 {
		return nil, ErrConflicts
	}
	if len(p.writes) == 0 {
		return nil, ErrNothingToUndo
	}
	if dirty := p.DirtyPaths(); len(dirty) > 0 && !force {
		return nil, &DirtyError{Paths: dirty}
	}
	sh, unlock, err := a.begin()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if sess, err := a.Store.Session(); err != nil {
		return nil, err
	} else if sess != nil {
		return nil, &SessionActiveError{Session: sess}
	}

	// A fresh snapshot saves the state right before the undo and proves
	// nothing the undo writes changed while the plan was on screen.
	now, err := sh.Snapshot()
	if err != nil {
		return nil, err
	}
	moved, err := sh.TreeDiff(p.current, now, p.writes...)
	if err != nil {
		return nil, err
	}
	if len(moved) > 0 {
		var changed []string
		for _, c := range moved {
			changed = append(changed, c.Path)
		}
		return nil, &ChangedError{Paths: changed}
	}
	desc := a.undoDescription(p.Turn, p.Paths)
	before, err := sh.Commit(now, "", "before: "+desc)
	if err != nil {
		return nil, err
	}
	if err := sh.SetRef(pendingRef, before); err != nil {
		return nil, err
	}

	// From here on, Ctrl-C must not leave the working tree half changed:
	// turnback ignores it, and git runs outside the terminal's process group.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	plain := sh
	sh = sh.Uninterruptible()

	if err := sh.Checkout(p.current, p.target); err != nil {
		var co *shadow.CheckoutError
		if errors.As(err, &co) && co.Restored {
			sh.DeleteRef(pendingRef)
			return nil, &UndoAbortedError{Cause: co.Cause}
		}
		partial, recErr := a.recordPartial(sh, p.Turn, p.Paths, before, err)
		switch {
		case recErr != nil:
			return nil, fmt.Errorf("%w; recording what it changed failed too (%v). %s", err, recErr, keepForRecovery(sh, before))
		case partial == nil:
			sh.DeleteRef(pendingRef)
			return nil, &UndoAbortedError{Cause: err}
		}
		sh.DeleteRef(pendingRef)
		return nil, &PartialUndoError{Turn: partial, Cause: err}
	}

	u, err := a.recordUndo(sh, p, before, desc)
	if err != nil {
		return nil, fmt.Errorf("the undo was applied, but recording it failed: %w. %s", err, keepForRecovery(sh, before))
	}
	sh.DeleteRef(pendingRef)
	// Housekeeping may take a while and is safe to interrupt.
	signal.Reset(os.Interrupt)
	plain.Tidy()
	return u, nil
}

// keepForRecovery keeps the state from before a failed undo under a ref of
// its own, so the next undo cannot replace it, and says where it is.
func keepForRecovery(sh *shadow.Repo, before string) string {
	ref := "refs/turnback/unrecorded/" + before
	if err := sh.SetRef(ref, before); err == nil {
		sh.DeleteRef(pendingRef)
	} else {
		ref = pendingRef
	}
	return fmt.Sprintf("The state from before the undo is saved in .turnback/git as commit %s (%s)", before, ref)
}

// recordUndo records a finished undo as a turn, from the verified state of
// the private index.
func (a *App) recordUndo(sh *shadow.Repo, p *UndoPlan, before, desc string) (*store.Turn, error) {
	tree, err := sh.IndexTree()
	if err != nil {
		return nil, err
	}
	return a.saveUndoTurn(sh, p.Turn, p.Paths, before, tree, desc)
}

// recordPartial records whatever an interrupted undo changed as a turn, so
// it can be undone. It returns nil when nothing changed after all.
func (a *App) recordPartial(sh *shadow.Repo, t *store.Turn, paths []string, before string, cause error) (*store.Turn, error) {
	tree, err := sh.Snapshot()
	if err != nil {
		return nil, err
	}
	changes, err := sh.TreeDiff(before, tree)
	if err != nil || len(changes) == 0 {
		return nil, err
	}
	return a.saveUndoTurn(sh, t, paths, before, tree, fmt.Sprintf("Partial undo of turn %d: %s", t.ID, t.Description))
}

func (a *App) saveUndoTurn(sh *shadow.Repo, t *store.Turn, paths []string, before, tree, desc string) (*store.Turn, error) {
	id, err := a.Store.NextTurnID()
	if err != nil {
		return nil, err
	}
	after, err := sh.Commit(tree, before, fmt.Sprintf("turn %d: %s", id, desc))
	if err != nil {
		return nil, err
	}
	if err := sh.SetRef(turnRef(id), after); err != nil {
		return nil, err
	}
	changes, err := sh.Changes(before, after)
	if err != nil {
		return nil, err
	}
	patch, err := sh.Patch(before, after)
	if err != nil {
		return nil, err
	}
	at := a.now()
	u := &store.Turn{
		ID:          id,
		Kind:        store.KindUndo,
		Description: desc,
		StartedAt:   at,
		EndedAt:     at,
		Before:      before,
		After:       after,
		Files:       toFiles(changes),
		Undoes:      &store.UndoInfo{Turn: t.ID, Paths: paths},
	}
	if err := a.Store.SaveTurn(u, patch); err != nil {
		return nil, err
	}
	moveCheckpoint(sh, after)
	return u, nil
}

// undoDescription names an undo after what it takes back. Undoing an undo
// is a redo of the original turn.
func (a *App) undoDescription(t *store.Turn, paths []string) string {
	if t.Kind == store.KindUndo && t.Undoes != nil && len(paths) == 0 {
		if orig, err := a.Store.Turn(t.Undoes.Turn); err == nil {
			if len(t.Undoes.Paths) > 0 {
				return fmt.Sprintf("Redo %s from turn %d: %s", strings.Join(t.Undoes.Paths, ", "), orig.ID, orig.Description)
			}
			return fmt.Sprintf("Redo turn %d: %s", orig.ID, orig.Description)
		}
	}
	if len(paths) > 0 {
		return fmt.Sprintf("Undo %s from turn %d: %s", strings.Join(paths, ", "), t.ID, t.Description)
	}
	return fmt.Sprintf("Undo turn %d: %s", t.ID, t.Description)
}
