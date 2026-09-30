package shadow

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

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
// to exactly those paths. The filtering happens here rather than on git's
// command line, so any number of paths works.
func (r *Repo) TreeDiff(from, to string, paths ...string) ([]EntryChange, error) {
	out, err := r.run.Run("diff-tree", "-r", "-z", "--no-renames", "--raw", from, to)
	if err != nil {
		return nil, err
	}
	entries, err := parseRaw(out)
	if err != nil {
		return nil, err
	}
	var keep map[string]bool
	if len(paths) > 0 {
		keep = make(map[string]bool, len(paths))
		for _, p := range paths {
			keep[p] = true
		}
	}
	changes := make([]EntryChange, 0, len(entries))
	for _, e := range entries {
		if keep == nil || keep[e.path] {
			changes = append(changes, EntryChange{Path: e.path, Old: entry(e.oldMode, e.oldID), New: entry(e.newMode, e.newID)})
		}
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

// CheckoutError reports a checkout that could not finish.
type CheckoutError struct {
	Cause    error    // why the checkout stopped
	Restored bool     // every path is back as it was before the checkout
	Leftover []string // paths that could not be put back, when Restored is false
}

func (e *CheckoutError) Error() string {
	if e.Restored {
		return "nothing was changed: " + e.Cause.Error()
	}
	return fmt.Sprintf("%v; these files could not be put back: %s", e.Cause, strings.Join(e.Leftover, ", "))
}

func (e *CheckoutError) Unwrap() error { return e.Cause }

// Checkout moves the working tree from snapshot from to snapshot to,
// writing only the paths that differ.
//
// It never overwrites a file that changed on disk after from was taken, and
// never overwrites or writes through anything git does not track: a file
// the new snapshot adds is only created where nothing exists yet, inside
// real folders. It does not trust git's exit status alone: after each
// phase it checks every written path on disk. If anything is off, it puts
// back what it changed, checks that too, and returns a *CheckoutError.
//
// The private index must match from for every path that differs between
// from and to, as it does right after Snapshot. Other paths may differ;
// they are left as they are.
func (r *Repo) Checkout(from, to string) error {
	changes, err := r.TreeDiff(from, to)
	if err != nil {
		return err
	}
	var adds []EntryChange
	tracked := make(map[string]*Entry) // changed or deleted paths that exist in from
	added := make(map[string]*Entry)
	deleted := make(map[string]bool) // exact and lower-case spellings of deleted paths
	for _, c := range changes {
		if c.Old == nil {
			adds = append(adds, c)
			added[c.Path] = c.New
			continue
		}
		tracked[c.Path] = c.New
		if c.New == nil {
			deleted[c.Path] = true
			deleted[strings.ToLower(c.Path)] = true
		}
	}
	// Fail early when an untracked file sits where a new file must go.
	for _, c := range adds {
		if err := r.checkFree(c.Path, deleted, false); err != nil {
			return &CheckoutError{Cause: err, Restored: true}
		}
	}

	co := &checkout{repo: r, from: from, mid: from, changes: changes}
	// Phase 1: files git already tracks. read-tree checks that none of them
	// changed on disk, but it can still stop halfway (a folder that is not
	// writable) or skip a deletion with only a warning.
	if len(tracked) > 0 {
		if co.mid, err = r.BuildTree(from, tracked); err != nil {
			return &CheckoutError{Cause: err, Restored: true}
		}
		if _, err := r.run.Run("read-tree", "-m", "-u", from, co.mid); err != nil {
			return co.fail(err)
		}
		if err := r.verify(tracked); err != nil {
			return co.fail(err)
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
			return co.fail(err)
		}
	}
	co.newDirs = r.missingDirs(adds)
	co.created = adds
	if _, err := r.run.Run("read-tree", "-m", co.mid, to); err != nil {
		return co.fail(err)
	}
	var list bytes.Buffer
	for _, c := range adds {
		list.WriteString(c.Path)
		list.WriteByte(0)
	}
	if _, err := r.run.RunInput(list.Bytes(), "checkout-index", "-u", "-z", "--stdin"); err != nil {
		return co.fail(err)
	}
	if err := r.verify(added); err != nil {
		return co.fail(err)
	}
	return nil
}

// checkout remembers what a Checkout did, so a failure can be undone.
type checkout struct {
	repo      *Repo
	from, mid string        // mid is from with phase 1 applied
	changes   []EntryChange // every path the checkout touches
	created   []EntryChange // paths phase 2 may have created
	newDirs   []string      // folders phase 2 may have created, parents first
}

// fail puts every path of the checkout back as it was in from, checks the
// result, and returns a *CheckoutError describing the outcome.
func (co *checkout) fail(cause error) error {
	r := co.repo
	for _, c := range co.created {
		if r.matches(c.Path, c.New) { // only remove what this checkout wrote
			os.Remove(filepath.Join(r.root, filepath.FromSlash(c.Path)))
		}
	}
	for i := len(co.newDirs) - 1; i >= 0; i-- {
		os.Remove(co.newDirs[i]) // only succeeds while empty
	}
	var putBack error
	if _, err := r.Snapshot(); err != nil {
		putBack = err
	} else if co.mid != co.from {
		_, putBack = r.run.Run("read-tree", "-m", "-u", co.mid, co.from)
	}
	var leftover []string
	for _, c := range co.changes {
		if c.Old == nil {
			// A new path: only a file this checkout wrote and could not
			// remove is left over. Something else standing there is not.
			if r.matches(c.Path, c.New) {
				leftover = append(leftover, c.Path)
			}
			continue
		}
		if !r.matches(c.Path, c.Old) {
			leftover = append(leftover, c.Path)
		}
	}
	if len(leftover) == 0 {
		return &CheckoutError{Cause: cause, Restored: true}
	}
	if putBack != nil {
		cause = fmt.Errorf("%w (putting files back failed too: %v)", cause, putBack)
	}
	return &CheckoutError{Cause: cause, Leftover: leftover}
}

// verify checks that every path is on disk exactly as expected; a nil
// entry means the path must not exist.
func (r *Repo) verify(expected map[string]*Entry) error {
	var wrong []string
	for path, e := range expected {
		if !r.matches(path, e) {
			wrong = append(wrong, path)
		}
	}
	if len(wrong) == 0 {
		return nil
	}
	sort.Strings(wrong)
	if len(wrong) > 5 {
		wrong = append(wrong[:5], fmt.Sprintf("and %d more", len(wrong)-5))
	}
	return fmt.Errorf("could not write or delete %s", strings.Join(wrong, ", "))
}

// matches reports whether path is on disk exactly as e describes: same
// content, type and executable bit. A nil entry means it must not exist.
func (r *Repo) matches(path string, e *Entry) bool {
	abs := filepath.Join(r.root, filepath.FromSlash(path))
	fi, err := os.Lstat(abs)
	if e == nil {
		return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
	}
	if err != nil {
		return false
	}
	switch {
	case e.Submodule():
		return fi.IsDir()
	case e.Symlink():
		if fi.Mode()&os.ModeSymlink == 0 {
			return false
		}
		target, err := os.Readlink(abs)
		return err == nil && git.BlobID([]byte(target), len(e.ID)) == e.ID
	}
	if !fi.Mode().IsRegular() {
		return false
	}
	if r.fileMode && (e.Mode == "100755") != (fi.Mode()&0o111 != 0) {
		return false
	}
	content, err := os.ReadFile(abs)
	return err == nil && git.BlobID(content, len(e.ID)) == e.ID
}

// missingDirs lists the folders that creating the new files will create,
// parents before children.
func (r *Repo) missingDirs(adds []EntryChange) []string {
	seen := make(map[string]bool)
	var dirs []string
	for _, c := range adds {
		parts := strings.Split(c.Path, "/")
		for i := 1; i < len(parts); i++ {
			dir := filepath.Join(r.root, filepath.FromSlash(strings.Join(parts[:i], "/")))
			if seen[dir] {
				continue
			}
			seen[dir] = true
			if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
				dirs = append(dirs, dir)
			}
		}
	}
	return dirs
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
		if errors.Is(err, fs.ErrNotExist) {
			return nil // this and everything below it will be created
		}
		if err != nil {
			return err
		}
		last := i == len(parts)-1
		switch {
		case !strict && (deleted[rel] || deleted[strings.ToLower(rel)]):
			return nil // phase 1 removes it (on a case-insensitive disk, maybe under another spelling)
		case last && fi.IsDir() && !strict:
			continue // phase 1 may empty and remove it
		case last && fi.IsDir():
			return fmt.Errorf("cannot create %s: a folder with that name is in the way", path)
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

// Uninterruptible returns a copy of r whose git commands keep running when
// the user presses Ctrl-C.
func (r *Repo) Uninterruptible() *Repo {
	c := *r
	c.run.Detach = true
	return &c
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
