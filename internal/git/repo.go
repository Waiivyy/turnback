package git

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// Committed reports, for each of paths (relative to root), whether its
// content in the working tree is exactly what HEAD holds, compared the way
// git compares it: after line-ending and other filters, and including the
// executable bit and symlink targets. A path that exists in neither counts
// as committed. Unlike git status, it is not fooled by files marked
// skip-worktree or assume-unchanged. It does not write to the repository.
func Committed(root string, paths []string) (map[string]bool, error) {
	out := make(map[string]bool, len(paths))
	if len(paths) == 0 {
		return out, nil
	}
	r := Runner{Dir: root}
	type entry struct{ mode, id string }
	head := make(map[string]entry)
	if _, err := r.Run("rev-parse", "--verify", "-q", "HEAD"); err == nil {
		listing, err := r.Run("ls-tree", "-r", "-z", "--full-tree", "HEAD")
		if err != nil {
			return nil, err
		}
		for _, item := range strings.Split(listing, "\x00") {
			meta, path, ok := strings.Cut(item, "\t") // "<mode> <type> <id>\t<path>"
			if f := strings.Fields(meta); ok && len(f) == 3 {
				head[path] = entry{f[0], f[2]}
			}
		}
	}
	fileMode := true
	if v, err := r.Run("config", "--type=bool", "--default=true", "core.fileMode"); err == nil {
		fileMode = strings.TrimSpace(v) != "false"
	}

	var toHash []string
	for _, p := range paths {
		want, inHead := head[p]
		abs := filepath.Join(root, filepath.FromSlash(p))
		fi, err := os.Lstat(abs)
		switch {
		case err != nil:
			out[p] = !inHead && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR))
		case !inHead:
			out[p] = false
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(abs)
			out[p] = err == nil && want.mode == "120000" && BlobID([]byte(target), len(want.id)) == want.id
		case !fi.Mode().IsRegular() || want.mode == "120000" || want.mode == "160000":
			out[p] = false
		case fileMode && (want.mode == "100755") != (fi.Mode()&0o111 != 0):
			out[p] = false
		case strings.ContainsAny(p, "\n\r"):
			out[p] = false // hash-object reads one name per line; assume unsaved
		default:
			toHash = append(toHash, p)
		}
	}
	// hash-object applies the same filters git add would. Names read from
	// stdin that start with a double quote are un-quoted by git, so those
	// few are passed as arguments instead.
	var batch []string
	for _, p := range toHash {
		if !strings.HasPrefix(p, `"`) {
			batch = append(batch, p)
			continue
		}
		id, err := r.Run("hash-object", "--", p)
		if err != nil {
			return nil, err
		}
		out[p] = strings.TrimSpace(id) == head[p].id
	}
	if len(batch) > 0 {
		ids, err := r.RunInput([]byte(strings.Join(batch, "\n")+"\n"), "hash-object", "--stdin-paths")
		if err != nil {
			return nil, err
		}
		lines := strings.Split(strings.TrimSpace(string(ids)), "\n")
		if len(lines) != len(batch) {
			return nil, fmt.Errorf("git hash-object returned %d ids for %d files", len(lines), len(batch))
		}
		for i, p := range batch {
			out[p] = lines[i] == head[p].id
		}
	}
	return out, nil
}

// BlobID computes the id git gives a blob with this content. hexLen, the
// length of an id from the same repository, tells SHA-1 (40) from SHA-256
// (64).
func BlobID(content []byte, hexLen int) string {
	var h hash.Hash = sha1.New()
	if hexLen == 64 {
		h = sha256.New()
	}
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// Ignored returns which of paths, relative to root with forward slashes,
// git ignores now, whether or not they exist. A tracked file is never
// ignored, in any spelling git would match it by, and a path beyond a
// symbolic link or inside a nested repository is not reported either: git
// does not look there at all.
func Ignored(root string, paths []string) (map[string]bool, error) {
	out := make(map[string]bool)
	var list bytes.Buffer
	hidden := make(map[string]bool) // folder -> it or a folder above it is a link or a repository
	for _, p := range paths {
		if outOfView(root, p, hidden) {
			continue
		}
		// "./" stops git from reading a name such as ":!notes" as pathspec magic.
		list.WriteString("./" + p)
		list.WriteByte(0)
	}
	if list.Len() == 0 {
		return out, nil
	}
	// With --no-index git only matches the ignore rules. Without it, git
	// also looks each path up in the index, one full scan per path, so the
	// tracked files are filtered out below instead.
	res, err := (Runner{Dir: root}).RunInput(list.Bytes(), "check-ignore", "--no-index", "-z", "--stdin")
	var gitErr *Error
	if errors.As(err, &gitErr) && gitErr.ExitCode == 1 {
		return out, nil // none of the paths is ignored
	}
	if err != nil {
		return nil, err
	}
	var matched []string
	for _, p := range strings.Split(string(res), "\x00") {
		if p = strings.TrimPrefix(p, "./"); p != "" {
			matched = append(matched, p)
		}
	}
	if len(matched) == 0 {
		return out, nil
	}
	tracked, fold, err := trackedPaths(root)
	if err != nil {
		return nil, err
	}
	for _, p := range matched {
		key := p
		if fold {
			key = strings.ToLower(p)
		}
		if !tracked[key] {
			out[p] = true
		}
	}
	return out, nil
}

// outOfView reports whether a folder above path is a symbolic link or a
// nested repository, where git does not look for path at all.
func outOfView(root, path string, cache map[string]bool) bool {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return false
	}
	dir := path[:i]
	hidden, seen := cache[dir]
	if !seen {
		full := filepath.Join(root, filepath.FromSlash(dir))
		fi, err := os.Lstat(full)
		hidden = err == nil && fi.Mode()&os.ModeSymlink != 0
		if !hidden {
			_, err := os.Lstat(filepath.Join(full, ".git"))
			hidden = err == nil
		}
		if !hidden {
			hidden = outOfView(root, dir, cache)
		}
		cache[dir] = hidden
	}
	return hidden
}

// trackedPaths returns the paths in the index, lower-cased if git compares
// names without regard to case in this repository, and whether it does.
func trackedPaths(root string) (map[string]bool, bool, error) {
	run := Runner{Dir: root}
	cfg, err := run.Run("config", "--type=bool", "--default=false", "core.ignorecase")
	if err != nil {
		return nil, false, err
	}
	fold := strings.TrimSpace(cfg) == "true"
	listed, err := run.Run("ls-files", "-z")
	if err != nil {
		return nil, false, err
	}
	tracked := make(map[string]bool)
	for _, p := range strings.Split(listed, "\x00") {
		if p == "" {
			continue
		}
		if fold {
			p = strings.ToLower(p)
		}
		tracked[p] = true
	}
	return tracked, fold, nil
}

// TrackedIn returns the first path git tracks inside dir, a folder at the
// top of the working tree, spelled in any case, or "" if there is none.
func TrackedIn(root, dir string) (string, error) {
	out, err := Runner{Dir: root}.Run("ls-files", "-z", "--", ":(icase)"+dir)
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(out, "\x00")
	return first, nil
}
