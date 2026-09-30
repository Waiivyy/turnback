package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Waiivyy/turnback/internal/shadow"
	"github.com/Waiivyy/turnback/internal/store"
)

// ErrNoTurns means nothing has been recorded yet.
var ErrNoTurns = errors.New("no turns recorded yet")

// NotInTurnError means a turn did not change any of the requested paths.
type NotInTurnError struct {
	Turn  *store.Turn
	Paths []string
}

func (e *NotInTurnError) Error() string {
	return fmt.Sprintf("turn %d did not change %s", e.Turn.ID, strings.Join(e.Paths, ", "))
}

// ResolveTurn finds a turn by id ("3" or "#3") or by the name "last".
func (a *App) ResolveTurn(ref string) (*store.Turn, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	if ref == "last" || ref == "latest" {
		turns, err := a.Store.Turns()
		if err != nil {
			return nil, err
		}
		if len(turns) == 0 {
			return nil, ErrNoTurns
		}
		return turns[0], nil
	}
	id, err := strconv.Atoi(ref)
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("%q is not a turn id", ref)
	}
	return a.Store.Turn(id)
}

// RepoPath converts a path given on the command line, relative to the
// directory turnback runs in or absolute, into a slash-separated path
// relative to the working tree root. The root itself is "".
func (a *App) RepoPath(arg string) (string, error) {
	abs := arg
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(a.Root, filepath.FromSlash(a.Prefix), arg)
	}
	rel, ok := relInside(a.Root, filepath.Clean(abs))
	if !ok {
		// The path may reach the repository through a symlink, such as
		// /tmp and /private/tmp on macOS.
		if resolved, err := resolveExisting(abs); err == nil {
			rel, ok = relInside(a.Root, resolved)
		}
	}
	if !ok {
		return "", fmt.Errorf("%s is outside the repository", arg)
	}
	return rel, nil
}

func relInside(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

// resolveExisting resolves symlinks in the longest existing prefix of path.
func resolveExisting(path string) (string, error) {
	dir, rest := filepath.Clean(path), ""
	for {
		if _, err := os.Lstat(dir); err == nil {
			resolved, err := filepath.EvalSymlinks(dir)
			if err != nil {
				return "", err
			}
			return filepath.Join(resolved, rest), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// touches reports whether f is the path p or lies under the directory p.
// A rename touches both its old and its new path. "" matches everything.
func touches(f store.File, p string) bool {
	return p == "" || under(f.Path, p) || (f.OldPath != "" && under(f.OldPath, p))
}

func under(path, p string) bool {
	return path == p || strings.HasPrefix(path, p+"/")
}

// matchingFiles returns the files of t that touch any of paths.
func matchingFiles(t *store.Turn, paths []string) []store.File {
	var out []store.File
	for _, f := range t.Files {
		for _, p := range paths {
			if touches(f, p) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// LogOptions filters Log.
type LogOptions struct {
	Since time.Time // only turns recorded at or after this time
	Paths []string  // only turns touching one of these repository paths
	Limit int       // at most this many turns; 0 means no limit
}

// Log returns recorded turns, newest first.
func (a *App) Log(opts LogOptions) ([]*store.Turn, error) {
	turns, err := a.Store.Turns()
	if err != nil {
		return nil, err
	}
	var out []*store.Turn
	for _, t := range turns {
		if !opts.Since.IsZero() && t.EndedAt.Before(opts.Since) {
			continue
		}
		if len(opts.Paths) > 0 && len(matchingFiles(t, opts.Paths)) == 0 {
			continue
		}
		out = append(out, t)
		if opts.Limit > 0 && len(out) == opts.Limit {
			break
		}
	}
	return out, nil
}

// Diff returns the diff of a turn. With paths, it is limited to the files
// under those paths; a renamed file always keeps both of its names so the
// rename shows as a rename.
func (a *App) Diff(t *store.Turn, paths []string) (string, error) {
	var pathspec []string
	if len(paths) > 0 {
		files := matchingFiles(t, paths)
		if len(files) == 0 {
			return "", &NotInTurnError{Turn: t, Paths: paths}
		}
		for _, f := range files {
			pathspec = append(pathspec, f.Path)
			if f.OldPath != "" {
				pathspec = append(pathspec, f.OldPath)
			}
		}
	}
	sh, err := shadow.Open(a.Root, a.Store.GitDir())
	if err != nil {
		return "", err
	}
	return sh.Diff(t.Before, t.After, pathspec...)
}
