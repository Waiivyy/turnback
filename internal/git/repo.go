package git

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrNotRepository means the directory is not inside a git working tree.
var ErrNotRepository = errors.New("not inside a git working tree")

// Location describes where a directory sits in a git working tree.
type Location struct {
	Root   string // absolute path of the working tree root
	Prefix string // directory relative to Root, slash-separated; "" at the root
}

// Locate finds the working tree that contains dir.
func Locate(dir string) (Location, error) {
	// English messages, so "not a repository" can be told apart from other
	// fatal errors such as git's "dubious ownership" check.
	r := Runner{Dir: dir, Env: []string{"LC_ALL=C"}}
	out, err := r.Run("rev-parse", "--show-toplevel", "--show-prefix")
	if err != nil {
		var gitErr *Error
		if errors.As(err, &gitErr) && (strings.Contains(gitErr.Stderr, "not a git repository") ||
			strings.Contains(gitErr.Stderr, "must be run in a work tree")) {
			return Location{}, ErrNotRepository
		}
		return Location{}, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return Location{}, fmt.Errorf("unexpected output from git rev-parse: %q", out)
	}
	loc := Location{Root: filepath.FromSlash(lines[0])}
	if len(lines) > 1 {
		loc.Prefix = strings.TrimSuffix(lines[1], "/")
	}
	return loc, nil
}

// Uncommitted returns the subset of paths (relative to root) whose content
// in the working tree or the staging area differs from HEAD, including
// untracked files and deletions. It does not write to the index.
func Uncommitted(root string, paths []string) (map[string]bool, error) {
	out := make(map[string]bool)
	if len(paths) == 0 {
		return out, nil
	}
	r := Runner{Dir: root, Opts: []string{"--literal-pathspecs"}}
	args := append([]string{"status", "--porcelain=v1", "-z", "--no-renames",
		"--untracked-files=all", "--ignore-submodules=all", "--"}, paths...)
	status, err := r.Run(args...)
	if err != nil {
		return nil, err
	}
	for _, entry := range strings.Split(status, "\x00") {
		// Each entry is "XY path".
		if len(entry) > 3 {
			out[entry[3:]] = true
		}
	}
	return out, nil
}
