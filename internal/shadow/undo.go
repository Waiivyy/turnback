package shadow

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Waiivyy/turnback/internal/git"
)

// Entry is one file in a snapshot.
type Entry struct {
	Mode string // "100644", "100755", "120000" (symlink) or "160000" (submodule)
	ID   string // blob id (commit id for a submodule)
}

// Symlink and Submodule report special file types.
func (e *Entry) Symlink() bool   { return e != nil && e.Mode == "120000" }
func (e *Entry) Submodule() bool { return e != nil && e.Mode == "160000" }

// Same reports whether two entries are identical; nil means "no file".
func Same(a, b *Entry) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// EntryChange is a path whose entry differs between two snapshots.
type EntryChange struct {
	Path     string
	Old, New *Entry // nil when the path does not exist on that side
}

const nullID = "0000000000000000000000000000000000000000"

// TreeDiff lists every path that differs between two snapshots, with the
// entry on each side, without rename detection. With paths, it is limited
// to those paths.
func (r *Repo) TreeDiff(from, to string, paths ...string) ([]EntryChange, error) {
	args := []string{"diff-tree", "-r", "-z", "--no-renames", "--raw", from, to}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	out, err := r.run.Run(args...)
	if err != nil {
		return nil, err
	}
	entries, err := parseRaw(out)
	if err != nil {
		return nil, err
	}
	changes := make([]EntryChange, len(entries))
	for i, e := range entries {
		changes[i] = EntryChange{Path: e.path, Old: entry(e.oldMode, e.oldID), New: entry(e.newMode, e.newID)}
	}
	return changes, nil
}

func entry(mode, id string) *Entry {
	if strings.Trim(mode, "0") == "" {
		return nil
	}
	return &Entry{Mode: mode, ID: id}
}

// ReadBlob returns the content of a blob.
func (r *Repo) ReadBlob(id string) ([]byte, error) {
	return r.run.RunInput(nil, "cat-file", "blob", id)
}

// WriteBlob stores content as a blob and returns its id.
func (r *Repo) WriteBlob(content []byte) (string, error) {
	out, err := r.run.RunInput(content, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Merge3 merges into current the change that turns base into other, the
// way git revert does for a single file. It returns the merged content and
// the number of conflicting regions. When there are conflicts, the content
// carries conflict markers labeled current, base and other by labels.
func (r *Repo) Merge3(current, base, other []byte, labels [3]string) ([]byte, int, error) {
	dir, err := os.MkdirTemp(r.gitDir, "merge-")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(dir)
	names := [3]string{"current", "base", "other"}
	for i, content := range [3][]byte{current, base, other} {
		names[i] = filepath.Join(dir, names[i])
		if err := os.WriteFile(names[i], content, 0o600); err != nil {
			return nil, 0, err
		}
	}
	out, err := r.run.RunInput(nil, "-c", "merge.conflictStyle=merge", "merge-file", "-p",
		"-L", labels[0], "-L", labels[1], "-L", labels[2], names[0], names[1], names[2])
	if err == nil {
		return out, 0, nil
	}
	// merge-file exits with the number of conflicts, or a negative number
	// (seen as 128 and above) on error.
	var gitErr *git.Error
	if errors.As(err, &gitErr) && gitErr.ExitCode > 0 && gitErr.ExitCode < 128 {
		return out, gitErr.ExitCode, nil
	}
	return nil, 0, err
}

// BuildTree returns a tree like base with changes applied; a nil entry
// removes the path. The private index is not touched.
func (r *Repo) BuildTree(base string, changes map[string]*Entry) (string, error) {
	f, err := os.CreateTemp(r.gitDir, "index-")
	if err != nil {
		return "", err
	}
	index := f.Name()
	f.Close()
	os.Remove(index) // git creates it; an empty file is not a valid index
	defer os.Remove(index)
	run := r.run.With("GIT_INDEX_FILE=" + index)

	if _, err := run.Run("read-tree", base); err != nil {
		return "", err
	}
	paths := make([]string, 0, len(changes))
	for p := range changes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var info bytes.Buffer
	for _, p := range paths {
		if e := changes[p]; e == nil {
			fmt.Fprintf(&info, "0 %s\t%s\x00", nullID, p)
		} else {
			fmt.Fprintf(&info, "%s %s\t%s\x00", e.Mode, e.ID, p)
		}
	}
	if _, err := run.RunInput(info.Bytes(), "update-index", "-z", "--index-info"); err != nil {
		return "", err
	}
	out, err := run.Run("write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Checkout moves the working tree from snapshot from to snapshot to,
// writing only the paths that differ. The private index must match from,
// as it does right after Snapshot.
//
// It never overwrites a file that changed on disk after from was taken, and
// never overwrites or writes through anything git does not track: a file
// the new snapshot adds is only created where nothing exists yet, inside
// real folders. If it cannot finish, it puts back what it changed and
// returns an error.
func (r *Repo) Checkout(from, to string) error {
	changes, err := r.TreeDiff(from, to)
	if err != nil {
		return err
	}
	var adds []EntryChange
	tracked := make(map[string]*Entry) // changed or deleted paths that exist in from
	deleted := make(map[string]bool)
	for _, c := range changes {
		switch {
		case c.Old == nil:
			adds = append(adds, c)
		default:
			tracked[c.Path] = c.New
			if c.New == nil {
				deleted[c.Path] = true
			}
		}
	}
	// Fail early when an untracked file sits where a new file must go.
	for _, c := range adds {
		if err := r.checkFree(c.Path, deleted, false); err != nil {
			return err
		}
	}

	// Phase 1: files git already tracks. read-tree refuses, before writing
	// anything, if one of them changed on disk.
	mid := from
	if len(tracked) > 0 {
		if mid, err = r.BuildTree(from, tracked); err != nil {
			return err
		}
		if _, err := r.run.Run("read-tree", "-m", "-u", from, mid); err != nil {
			return fmt.Errorf("working tree changed; nothing was written: %w", err)
		}
	}
	if len(adds) == 0 {
		return nil
	}

	// Phase 2: new files, created only where nothing exists. read-tree -u
	// would overwrite ignored files here, so checkout-index without -f does
	// the writing; it creates each file exclusively.
	for _, c := range adds {
		if err := r.checkFree(c.Path, nil, true); err != nil {
			return r.rollback(err, mid, from, nil)
		}
	}
	if _, err := r.run.Run("read-tree", "-m", mid, to); err != nil {
		return r.rollback(err, mid, from, nil)
	}
	var list bytes.Buffer
	for _, c := range adds {
		list.WriteString(c.Path)
		list.WriteByte(0)
	}
	if _, err := r.run.RunInput(list.Bytes(), "checkout-index", "-u", "-z", "--stdin"); err != nil {
		return r.rollback(err, mid, from, adds)
	}
	return nil
}

// checkFree reports an error unless a new file can be created at path:
// nothing may exist there, and every existing parent must be a real
// folder, not a file or a symlink. Before phase 1 (strict false), paths
// that phase 1 deletes, and folders it may empty, are given the benefit of
// the doubt; the strict check after phase 1 has the final say.
func (r *Repo) checkFree(path string, deleted map[string]bool, strict bool) error {
	parts := strings.Split(path, "/")
	for i := range parts {
		rel := strings.Join(parts[:i+1], "/")
		fi, err := os.Lstat(filepath.Join(r.root, filepath.FromSlash(rel)))
		if errors.Is(err, os.ErrNotExist) {
			return nil // this and everything below it will be created
		}
		if err != nil {
			return err
		}
		last := i == len(parts)-1
		switch {
		case !strict && deleted[rel]:
			return nil // phase 1 removes it
		case last && fi.IsDir() && !strict:
			continue // phase 1 may empty and remove it
		case last:
			return fmt.Errorf("cannot create %s: a file git does not track is in the way", path)
		case fi.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("cannot create %s: %s is a symlink", path, rel)
		case !fi.IsDir():
			return fmt.Errorf("cannot create %s: %s is not a folder", path, rel)
		}
	}
	return nil
}

// rollback undoes phase 1 after phase 2 failed, removing any new files that
// were already written. It returns cause, or cause and the rollback error.
func (r *Repo) rollback(cause error, mid, from string, created []EntryChange) error {
	for _, c := range created {
		abs := filepath.Join(r.root, filepath.FromSlash(c.Path))
		if r.isEntry(abs, c.New) {
			os.Remove(abs)
		}
	}
	if _, err := r.Snapshot(); err != nil {
		return fmt.Errorf("%w; putting files back also failed: %v", cause, err)
	}
	if mid != from {
		if _, err := r.run.Run("read-tree", "-m", "-u", mid, from); err != nil {
			return fmt.Errorf("%w; putting files back also failed: %v", cause, err)
		}
	}
	return fmt.Errorf("nothing was changed: %w", cause)
}

// isEntry reports whether the file at abs has exactly the content of e.
func (r *Repo) isEntry(abs string, e *Entry) bool {
	want, err := r.ReadBlob(e.ID)
	if err != nil {
		return false
	}
	var got []byte
	if e.Symlink() {
		target, err := os.Readlink(abs)
		if err != nil {
			return false
		}
		got = []byte(target)
	} else if got, err = os.ReadFile(abs); err != nil {
		return false
	}
	return bytes.Equal(got, want)
}

// IndexTree returns the tree recorded in the private index.
func (r *Repo) IndexTree() (string, error) {
	out, err := r.run.Run("write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Paths lists every file in a snapshot.
func (r *Repo) Paths(tree string) ([]string, error) {
	out, err := r.run.Run("ls-tree", "-r", "-z", "--name-only", tree)
	if err != nil {
		return nil, err
	}
	return strings.FieldsFunc(out, func(c rune) bool { return c == 0 }), nil
}
