// Package store persists turnback's state in the .turnback folder at the
// root of a working tree: recorded turns, the session in progress, the turn
// counter and the lock that serializes turnback commands.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DirName is the folder turnback keeps its data in.
const DirName = ".turnback"

// Turn kinds.
const (
	KindSession = "session" // recorded between "turnback start" and "turnback end"
	KindUndo    = "undo"    // written by "turnback undo"
	KindCommit  = "commit"  // recorded by the post-commit hook
)

const formatVersion = 1

var (
	// ErrTurnNotFound means no turn has the requested id. Errors returned
	// for a specific id are NotFoundError values that match it.
	ErrTurnNotFound = errors.New("turn not found")
	// ErrLocked means another turnback command holds the lock.
	ErrLocked = errors.New("another turnback command is running in this repository")
)

// NotFoundError reports a turn id that does not exist.
type NotFoundError struct {
	ID int
}

func (e *NotFoundError) Error() string { return fmt.Sprintf("there is no turn %d", e.ID) }

// Is makes errors.Is(err, ErrTurnNotFound) true.
func (e *NotFoundError) Is(target error) bool { return target == ErrTurnNotFound }

// Store is the .turnback folder of one working tree.
type Store struct {
	Dir string
}

// Open returns the store for the working tree at root. Nothing is created
// until Init is called.
func Open(root string) *Store {
	return &Store{Dir: filepath.Join(root, DirName)}
}

// GitDir is where the private snapshot repository lives.
func (s *Store) GitDir() string { return filepath.Join(s.Dir, "git") }

// Exists reports whether turnback has been used in this working tree.
func (s *Store) Exists() bool {
	fi, err := os.Stat(s.Dir)
	return err == nil && fi.IsDir()
}

const gitignore = "# turnback keeps private data in this folder. This file makes git ignore all of it.\n*\n"

const readme = `This folder is managed by turnback (https://github.com/Waiivyy/turnback).

It holds the agent turns recorded in this working tree and private snapshots
of the files they changed. git ignores the folder, and turnback never writes
to your repository's history, index, refs or stash.

Deleting this folder removes all turnback history for this repository.
`

// UnsafeError reports something in turnback's folder that turnback did not
// make and will not use, such as a symbolic link.
type UnsafeError struct {
	Path   string // relative to the working tree root, as in .turnback/lock
	Reason string // what is wrong with it, as in "is a symbolic link"
}

func (e *UnsafeError) Error() string { return filepath.ToSlash(e.Path) + " " + e.Reason }

func (s *Store) unsafe(rel string, fi fs.FileInfo) *UnsafeError {
	reason := "is not a regular file"
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		reason = "is a symbolic link"
	case rel != "lock":
		reason = "is not a folder"
	}
	return &UnsafeError{Path: filepath.Join(DirName, rel), Reason: reason}
}

// Check makes sure turnback's folder holds only what turnback makes itself:
// real folders, and a lock that is a regular file. A link there, planted by
// a repository or an archive, could otherwise redirect turnback's writes to
// any file the user owns.
func (s *Store) Check() error {
	for _, rel := range []string{"", "git", "turns", "lock"} {
		fi, err := os.Lstat(filepath.Join(s.Dir, rel))
		switch {
		case errors.Is(err, fs.ErrNotExist) && rel == "":
			return nil
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return err
		}
		if rel == "lock" && !fi.Mode().IsRegular() || rel != "lock" && fi.Mode().Type() != fs.ModeDir {
			return s.unsafe(rel, fi)
		}
	}
	return nil
}

// Init creates the folder. Its own .gitignore makes git ignore everything
// inside, so the user's .gitignore never needs editing.
func (s *Store) Init() error {
	if err := s.Check(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, "turns"), 0o755); err != nil {
		return err
	}
	for name, content := range map[string]string{".gitignore": gitignore, "README.txt": readme} {
		path := filepath.Join(s.Dir, name)
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			if err := writeFileAtomic(path, []byte(content)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Session is a turn in progress.
type Session struct {
	StartedAt   time.Time `json:"started_at"`
	Snapshot    string    `json:"snapshot"` // snapshot commit taken by "turnback start"
	Description string    `json:"description,omitempty"`
	Agent       string    `json:"agent,omitempty"`
}

// File is one file changed by a turn.
type File struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"` // set for renames
	Status  string `json:"status"`             // A, M, D, R or T
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary,omitempty"`
	Ignored bool   `json:"ignored,omitempty"` // left the turn's view because git began ignoring it; still on disk
}

// UndoInfo records what an undo turn reverted.
type UndoInfo struct {
	Turn  int      `json:"turn"`
	Paths []string `json:"paths,omitempty"` // set when only some files were undone
}

// Turn is one recorded batch of changes.
type Turn struct {
	ID          int       `json:"id"`
	Kind        string    `json:"kind"`
	Description string    `json:"description"`
	Agent       string    `json:"agent,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
	Before      string    `json:"before"` // snapshot commit before the turn
	After       string    `json:"after"`  // snapshot commit after the turn
	Files       []File    `json:"files"`
	Undoes      *UndoInfo `json:"undoes,omitempty"`
	Commit      string    `json:"commit,omitempty"` // the user's commit that closed a commit turn
}

// Lines returns the total lines added and deleted by the turn.
func (t *Turn) Lines() (added, deleted int) {
	for _, f := range t.Files {
		added += f.Added
		deleted += f.Deleted
	}
	return added, deleted
}

func (s *Store) sessionPath() string { return filepath.Join(s.Dir, "session.json") }

// Session returns the session in progress, or nil if there is none.
func (s *Store) Session() (*Session, error) {
	var sess Session
	if err := readJSON(s.sessionPath(), &sess); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &sess, nil
}

// SaveSession records sess as the session in progress.
func (s *Store) SaveSession(sess *Session) error { return writeJSON(s.sessionPath(), sess) }

// ClearSession forgets the session in progress.
func (s *Store) ClearSession() error {
	if err := os.Remove(s.sessionPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

type state struct {
	Format   int `json:"format"`
	NextTurn int `json:"next_turn"`
}

// NextTurnID allocates the id for a new turn. Ids are never reused, even if
// state.json is lost, because saved turns are checked as well.
func (s *Store) NextTurnID() (int, error) {
	path := filepath.Join(s.Dir, "state.json")
	var st state
	if err := readJSON(path, &st); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	ids, err := s.turnIDs()
	if err != nil {
		return 0, err
	}
	next := max(st.NextTurn, 1)
	for _, id := range ids {
		next = max(next, id+1)
	}
	st.Format, st.NextTurn = formatVersion, next+1
	if err := writeJSON(path, &st); err != nil {
		return 0, err
	}
	return next, nil
}

func (s *Store) turnPath(id int, ext string) string {
	return filepath.Join(s.Dir, "turns", fmt.Sprintf("%04d.%s", id, ext))
}

// SaveTurn writes a turn and its patch.
func (s *Store) SaveTurn(t *Turn, patch string) error {
	if err := os.MkdirAll(filepath.Join(s.Dir, "turns"), 0o755); err != nil {
		return err
	}
	// The patch goes first: a turn file never points at a missing patch.
	if err := writeFileAtomic(s.turnPath(t.ID, "patch"), []byte(patch)); err != nil {
		return err
	}
	return writeJSON(s.turnPath(t.ID, "json"), t)
}

// Turn loads the turn with the given id.
func (s *Store) Turn(id int) (*Turn, error) {
	var t Turn
	if err := readJSON(s.turnPath(id, "json"), &t); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &NotFoundError{ID: id}
		}
		return nil, err
	}
	return &t, nil
}

// Patch returns the patch saved with a turn.
func (s *Store) Patch(id int) (string, error) {
	b, err := os.ReadFile(s.turnPath(id, "patch"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", &NotFoundError{ID: id}
	}
	return string(b), err
}

// Turns loads every recorded turn, newest first.
func (s *Store) Turns() ([]*Turn, error) {
	ids, err := s.turnIDs()
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	turns := make([]*Turn, 0, len(ids))
	for _, id := range ids {
		t, err := s.Turn(id)
		if err != nil {
			return nil, err
		}
		turns = append(turns, t)
	}
	return turns, nil
}

// TurnIDs returns the ids of the recorded turns, lowest first, without
// reading the turns themselves.
func (s *Store) TurnIDs() ([]int, error) {
	ids, err := s.turnIDs()
	sort.Ints(ids)
	return ids, err
}

func (s *Store) turnIDs() ([]int, error) {
	entries, err := os.ReadDir(filepath.Join(s.Dir, "turns"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if id, err := strconv.Atoi(name); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(b, '\n'))
}

// writeFileAtomic replaces path in one step, so readers and crashes never
// see a half-written file.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
