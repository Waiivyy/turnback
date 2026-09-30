package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Waiivyy/turnback/internal/git"
	"github.com/Waiivyy/turnback/internal/shadow"
	"github.com/Waiivyy/turnback/internal/store"
)

// hookMarker identifies the hook turnback writes, so it never touches
// anyone else's.
const hookMarker = "# Installed by turnback"

// checkpointRef marks where the next commit turn starts. It exists only
// while the post-commit hook is installed.
const checkpointRef = "refs/turnback/checkpoint"

// ErrNoHook means no turnback hook is installed.
var ErrNoHook = errors.New("no turnback hook is installed in this repository")

// HookExistsError means a post-commit hook that turnback did not write is
// in place; turnback never replaces it.
type HookExistsError struct {
	Path string
}

func (e *HookExistsError) Error() string {
	return fmt.Sprintf("%s exists and was not written by turnback", e.Path)
}

// HooksManagedError means git hooks live in a folder set by core.hooksPath,
// usually tracked in the repository and managed by a tool.
type HooksManagedError struct {
	Dir string
}

func (e *HooksManagedError) Error() string {
	return fmt.Sprintf("git hooks in this repository are managed in %s (core.hooksPath)", e.Dir)
}

// hookScript is the post-commit hook. It finds turnback on PATH, or where
// it was installed from, and never makes a commit fail.
func hookScript(self string) string {
	return fmt.Sprintf(`#!/bin/sh
%s: records a turn at every commit.
# Remove it with: turnback hook uninstall
tb=$(command -v turnback 2>/dev/null) || tb=%s
if [ -x "$tb" ]; then
	"$tb" hook post-commit
fi
exit 0
`, hookMarker, quoteForShell(filepath.ToSlash(self)))
}

func quoteForShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hookPath returns where the post-commit hook lives.
func (a *App) hookPath() (string, error) {
	out, err := (git.Runner{Dir: a.Root}).Run("rev-parse", "--git-path", "hooks/post-commit")
	if err != nil {
		return "", err
	}
	path := filepath.FromSlash(strings.TrimSpace(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.Root, path)
	}
	return path, nil
}

// InstallHook installs the post-commit hook that records a turn at every
// commit; self is the path of the turnback binary, used when turnback is
// not on the PATH of whoever makes the commit. It returns the hook's path.
func (a *App) InstallHook(self string) (string, error) {
	if out, err := (git.Runner{Dir: a.Root}).Run("config", "--get", "core.hooksPath"); err == nil && strings.TrimSpace(out) != "" {
		return "", &HooksManagedError{Dir: strings.TrimSpace(out)}
	}
	path, err := a.hookPath()
	if err != nil {
		return "", err
	}
	if b, err := os.ReadFile(path); err == nil {
		if !strings.Contains(string(b), hookMarker) {
			return "", &HookExistsError{Path: path}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp := path + ".turnback-new"
	if err := os.WriteFile(tmp, []byte(hookScript(self)), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}

	// The first commit turn starts now, unless the hook was already on.
	sh, unlock, err := a.begin()
	if err != nil {
		return "", err
	}
	defer unlock()
	if _, ok := sh.RefTarget(checkpointRef); !ok {
		tree, err := sh.Snapshot()
		if err != nil {
			return "", err
		}
		commit, err := sh.Commit(tree, "", "checkpoint")
		if err != nil {
			return "", err
		}
		if err := sh.SetRef(checkpointRef, commit); err != nil {
			return "", err
		}
	}
	return path, nil
}

// UninstallHook removes the post-commit hook, but only if turnback wrote it.
func (a *App) UninstallHook() error {
	path, err := a.hookPath()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNoHook
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), hookMarker) {
		return &HookExistsError{Path: path}
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if a.Store.Exists() {
		sh, unlock, err := a.begin()
		if err != nil {
			return err
		}
		defer unlock()
		sh.DeleteRef(checkpointRef)
	}
	return nil
}

// HookInstalled reports whether turnback's post-commit hook is in place.
func (a *App) HookInstalled() bool {
	path, err := a.hookPath()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), hookMarker)
}

// RecordCommit is run by the post-commit hook. It records everything that
// changed since the last turn boundary as a turn named after the commit.
// It records nothing while a turn is being recorded with start and end,
// during a rebase, or when nothing changed.
func (a *App) RecordCommit() (*store.Turn, error) {
	if a.rebasing() {
		return nil, nil
	}
	sh, unlock, err := a.begin()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if sess, err := a.Store.Session(); err != nil || sess != nil {
		return nil, err
	}
	tree, err := sh.Snapshot()
	if err != nil {
		return nil, err
	}
	base, ok := sh.RefTarget(checkpointRef)
	if !ok {
		// The hook is running without a checkpoint: start one here.
		commit, err := sh.Commit(tree, "", "checkpoint")
		if err != nil {
			return nil, err
		}
		return nil, sh.SetRef(checkpointRef, commit)
	}
	changes, err := sh.Changes(base, tree)
	if err != nil || len(changes) == 0 {
		return nil, err
	}
	files := toFiles(changes)
	if err := a.markIgnored(files); err != nil {
		return nil, err
	}
	subject, commitID := a.headCommit()
	desc := firstNonEmpty(subject, Summarize(files))
	id, err := a.Store.NextTurnID()
	if err != nil {
		return nil, err
	}
	after, err := sh.Commit(tree, base, fmt.Sprintf("turn %d: %s", id, desc))
	if err != nil {
		return nil, err
	}
	if err := sh.SetRef(turnRef(id), after); err != nil {
		return nil, err
	}
	patch, err := sh.Patch(base, after)
	if err != nil {
		return nil, err
	}
	startedAt, err := sh.CommitTime(base)
	if err != nil {
		startedAt = a.now()
	}
	turn := &store.Turn{
		ID:          id,
		Kind:        store.KindCommit,
		Description: desc,
		StartedAt:   startedAt,
		EndedAt:     a.now(),
		Before:      base,
		After:       after,
		Files:       files,
		Commit:      commitID,
	}
	if err := a.Store.SaveTurn(turn, patch); err != nil {
		return nil, err
	}
	if err := sh.SetRef(checkpointRef, after); err != nil {
		return nil, err
	}
	sh.Tidy()
	return turn, nil
}

// moveCheckpoint makes the next commit turn start at commit, if the hook
// is in use.
func moveCheckpoint(sh *shadow.Repo, commit string) {
	if _, ok := sh.RefTarget(checkpointRef); ok {
		sh.SetRef(checkpointRef, commit)
	}
}

// headCommit returns the subject and id of the user's latest commit.
func (a *App) headCommit() (subject, id string) {
	out, err := (git.Runner{Dir: a.Root}).Run("log", "-1", "--format=%H%x00%s")
	if err != nil {
		return "", ""
	}
	id, subject, _ = strings.Cut(strings.TrimSpace(out), "\x00")
	return subject, id
}

// rebasing reports whether a rebase is in progress; the working tree jumps
// around while one runs, so its commits are not turns.
func (a *App) rebasing() bool {
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		out, err := (git.Runner{Dir: a.Root}).Run("rev-parse", "--git-path", dir)
		if err != nil {
			continue
		}
		path := filepath.FromSlash(strings.TrimSpace(out))
		if !filepath.IsAbs(path) {
			path = filepath.Join(a.Root, path)
		}
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}
