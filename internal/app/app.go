// Package app implements turnback's operations on a working tree. It returns
// plain results; the command-line interface decides how to present them.
package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/Waiivyy/turnback/internal/git"
	"github.com/Waiivyy/turnback/internal/shadow"
	"github.com/Waiivyy/turnback/internal/store"
)

// lockWait is how long a command waits for another turnback command in the
// same working tree to finish.
const lockWait = 10 * time.Second

// sessionRef keeps the start snapshot of the turn in progress alive.
const sessionRef = "refs/turnback/session"

func turnRef(id int) string { return fmt.Sprintf("refs/turns/%d", id) }

// ErrNoSession means no turn is being recorded.
var ErrNoSession = errors.New("no turn is being recorded")

// SessionActiveError means a turn is already being recorded.
type SessionActiveError struct {
	Session *store.Session
}

func (e *SessionActiveError) Error() string { return "a turn is already being recorded" }

// App is turnback opened on one working tree.
type App struct {
	Root   string // working tree root
	Prefix string // directory turnback runs in, relative to Root ("" at the root)
	Store  *store.Store
	now    func() time.Time
}

// Open finds the working tree containing dir. It does not create anything.
func Open(dir string, now func() time.Time) (*App, error) {
	loc, err := git.Locate(dir)
	if err != nil {
		return nil, err
	}
	return &App{Root: loc.Root, Prefix: loc.Prefix, Store: store.Open(loc.Root), now: now}, nil
}

// begin creates turnback's folder if needed, takes the lock and opens the
// snapshot repository. The caller must call unlock.
func (a *App) begin() (sh *shadow.Repo, unlock func(), err error) {
	if err := a.Store.Init(); err != nil {
		return nil, nil, err
	}
	unlock, err = a.Store.Lock(lockWait)
	if err != nil {
		return nil, nil, err
	}
	sh, err = shadow.Open(a.Root, a.Store.GitDir())
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return sh, unlock, nil
}

// StartOptions configures Start.
type StartOptions struct {
	Description string // what the agent was asked to do
	Agent       string // which agent is making the changes
}

// Started describes a turn that has begun recording.
type Started struct {
	Session     *store.Session
	Files       int  // files in the starting snapshot
	Initialized bool // this call created turnback's folder
}

// Start snapshots the working tree and begins recording a turn.
func (a *App) Start(opts StartOptions) (*Started, error) {
	initialized := !a.Store.Exists()
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
	tree, err := sh.Snapshot()
	if err != nil {
		return nil, err
	}
	commit, err := sh.Commit(tree, "", "turn started")
	if err != nil {
		return nil, err
	}
	if err := sh.SetRef(sessionRef, commit); err != nil {
		return nil, err
	}
	sess := &store.Session{
		StartedAt:   a.now(),
		Snapshot:    commit,
		Description: opts.Description,
		Agent:       opts.Agent,
	}
	if err := a.Store.SaveSession(sess); err != nil {
		return nil, err
	}
	files, err := sh.CountFiles(tree)
	if err != nil {
		return nil, err
	}
	return &Started{Session: sess, Files: files, Initialized: initialized}, nil
}

// EndOptions configures End.
type EndOptions struct {
	Description string // overrides the description given at start
	Discard     bool   // drop the turn instead of recording it
}

// Ended describes how a turn finished.
type Ended struct {
	Session   *store.Session
	Turn      *store.Turn // nil when nothing changed or the turn was discarded
	Discarded bool
}

// End snapshots the working tree again and records everything that changed
// since Start as a new turn.
func (a *App) End(opts EndOptions) (*Ended, error) {
	if !a.Store.Exists() {
		return nil, ErrNoSession
	}
	sh, unlock, err := a.begin()
	if err != nil {
		return nil, err
	}
	defer unlock()

	sess, err := a.Store.Session()
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, ErrNoSession
	}
	res := &Ended{Session: sess}
	if opts.Discard {
		res.Discarded = true
		return res, a.closeSession(sh)
	}

	tree, err := sh.Snapshot()
	if err != nil {
		return nil, err
	}
	changes, err := sh.Changes(sess.Snapshot, tree)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		return res, a.closeSession(sh)
	}

	files := toFiles(changes)
	id, err := a.Store.NextTurnID()
	if err != nil {
		return nil, err
	}
	desc := firstNonEmpty(opts.Description, sess.Description, Summarize(files))
	commit, err := sh.Commit(tree, sess.Snapshot, fmt.Sprintf("turn %d: %s", id, desc))
	if err != nil {
		return nil, err
	}
	if err := sh.SetRef(turnRef(id), commit); err != nil {
		return nil, err
	}
	patch, err := sh.Patch(sess.Snapshot, commit)
	if err != nil {
		return nil, err
	}
	turn := &store.Turn{
		ID:          id,
		Kind:        store.KindSession,
		Description: desc,
		Agent:       sess.Agent,
		StartedAt:   sess.StartedAt,
		EndedAt:     a.now(),
		Before:      sess.Snapshot,
		After:       commit,
		Files:       files,
	}
	if err := a.Store.SaveTurn(turn, patch); err != nil {
		return nil, err
	}
	res.Turn = turn
	if err := a.closeSession(sh); err != nil {
		return res, err
	}
	sh.Tidy() // best effort housekeeping
	return res, nil
}

// closeSession forgets the turn in progress.
func (a *App) closeSession(sh *shadow.Repo) error {
	if err := a.Store.ClearSession(); err != nil {
		return err
	}
	sh.DeleteRef(sessionRef) // a leftover ref only keeps a snapshot alive
	return nil
}

// Status describes what turnback knows about the working tree.
type Status struct {
	Initialized bool            // turnback has been used here
	Session     *store.Session  // turn being recorded, if any
	Pending     []shadow.Change // changes so far in the turn being recorded
	Turns       int             // number of recorded turns
	Latest      *store.Turn     // most recent turn, if any
}

// Status reports the recording state, including the changes made so far in
// a turn that is being recorded.
func (a *App) Status() (*Status, error) {
	st := &Status{Initialized: a.Store.Exists()}
	if !st.Initialized {
		return st, nil
	}
	turns, err := a.Store.Turns()
	if err != nil {
		return nil, err
	}
	st.Turns = len(turns)
	if len(turns) > 0 {
		st.Latest = turns[0]
	}
	if st.Session, err = a.Store.Session(); err != nil || st.Session == nil {
		return st, err
	}
	sh, unlock, err := a.begin()
	if err != nil {
		return nil, err
	}
	defer unlock()
	tree, err := sh.Snapshot()
	if err != nil {
		return nil, err
	}
	st.Pending, err = sh.Changes(st.Session.Snapshot, tree)
	return st, err
}

func toFiles(changes []shadow.Change) []store.File {
	files := make([]store.File, len(changes))
	for i, c := range changes {
		files[i] = store.File{
			Path:    c.Path,
			OldPath: c.OldPath,
			Status:  c.Status,
			Added:   c.Added,
			Deleted: c.Deleted,
			Binary:  c.Binary,
		}
	}
	return files
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
