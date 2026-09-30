// Package shadow keeps snapshots of a working tree in a private git
// repository whose work tree is the user's working tree. The private
// repository has its own index, objects and refs, so the user's repository,
// staging area, refs and stash are never written to.
package shadow

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Waiivyy/turnback/internal/git"
)

// storeAttributes makes the private repository store exact working tree
// bytes: no line-ending conversion, no clean/smudge filters (Git LFS
// included), no keyword expansion and no re-encoding. info/attributes takes
// precedence over every .gitattributes file in the working tree.
const storeAttributes = "# Written by turnback: store exact working tree bytes.\n" +
	"* -text -filter -ident -working-tree-encoding\n"

// identity is used for the private repository's internal commits.
var identity = []string{
	"GIT_AUTHOR_NAME=turnback",
	"GIT_AUTHOR_EMAIL=turnback@localhost",
	"GIT_COMMITTER_NAME=turnback",
	"GIT_COMMITTER_EMAIL=turnback@localhost",
}

// Repo is a private snapshot repository for one working tree.
type Repo struct {
	root   string
	gitDir string
	ownDir string // top-level folder of root holding gitDir, never snapshotted
	run    git.Runner
}

// Change is one file that differs between two snapshots.
type Change struct {
	Status  string // "A" added, "M" modified, "D" deleted, "R" renamed, "T" type changed
	Path    string // path after the change (the deleted path, for deletions)
	OldPath string // path before a rename
	Added   int    // lines added
	Deleted int    // lines deleted
	Binary  bool   // binary content; line counts are zero
}

// Open opens the private repository at gitDir for the working tree at root,
// creating it on first use.
func Open(root, gitDir string) (*Repo, error) {
	r := &Repo{root: root, gitDir: gitDir}
	if rel, err := filepath.Rel(root, gitDir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		r.ownDir = strings.Split(filepath.ToSlash(rel), "/")[0]
	}
	if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(gitDir), 0o755); err != nil {
			return nil, err
		}
		// An empty template keeps sample hooks and other files out.
		if _, err := (git.Runner{Dir: root}).Run("init", "--quiet", "--bare", "--template=", gitDir); err != nil {
			return nil, fmt.Errorf("create snapshot store: %w", err)
		}
	} else if err != nil {
		return nil, err
	}
	opts, err := r.syncSettings()
	if err != nil {
		return nil, err
	}
	r.run = git.Runner{Dir: root, Opts: opts, Env: identity}
	return r, nil
}

// syncSettings copies the user's ignore rules into the private repository
// and returns the global options every private git command runs with.
func (r *Repo) syncSettings() ([]string, error) {
	user := git.Runner{Dir: r.root}
	var excludes bytes.Buffer
	excludes.WriteString("# Rebuilt by turnback on every run from the repository's info/exclude.\n")
	if out, err := user.Run("rev-parse", "--git-path", "info/exclude"); err == nil {
		path := strings.TrimSpace(out)
		if !filepath.IsAbs(path) {
			path = filepath.Join(r.root, path)
		}
		if b, err := os.ReadFile(path); err == nil {
			excludes.Write(b)
			excludes.WriteString("\n")
		}
	}
	if r.ownDir != "" {
		// Last, so no earlier negated pattern can re-include it.
		excludes.WriteString("/" + r.ownDir + "/\n")
	}
	if err := writeIfChanged(filepath.Join(r.gitDir, "info", "exclude"), excludes.Bytes()); err != nil {
		return nil, err
	}
	if err := writeIfChanged(filepath.Join(r.gitDir, "info", "attributes"), []byte(storeAttributes)); err != nil {
		return nil, err
	}

	opts := []string{
		"--git-dir=" + r.gitDir,
		"--work-tree=" + r.root,
		// Paths are always literal: "src/[id].tsx" is a file, not a glob.
		"--literal-pathspecs",
		"-c", "core.hooksPath=" + filepath.Join(r.gitDir, "hooks-disabled"),
		"-c", "core.fsmonitor=false",
		"-c", "core.autocrlf=false",
		"-c", "core.quotePath=false",
		"-c", "advice.addEmbeddedRepo=false",
		"-c", "gc.autoDetach=false",
	}
	// core.excludesFile may be set in the user's repository config, which the
	// private repository does not read.
	if out, err := user.Run("config", "--type=path", "--get", "core.excludesFile"); err == nil {
		if path := strings.TrimSpace(out); path != "" {
			opts = append(opts, "-c", "core.excludesFile="+path)
		}
	}
	return opts, nil
}

// writeIfChanged replaces path atomically when its content differs, so a
// concurrent git command never reads a half-written ignore list.
func writeIfChanged(path string, content []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(content)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(f.Name(), path)
	}
	if werr != nil {
		os.Remove(f.Name())
	}
	return werr
}

// Snapshot records the current working tree (tracked files plus untracked
// files that are not ignored) and returns the id of its tree.
func (r *Repo) Snapshot() (string, error) {
	if _, err := r.run.Run("add", "--all"); err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	out, err := r.run.Run("write-tree")
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	tree := strings.TrimSpace(out)
	if r.ownDir != "" {
		listed, err := r.run.Run("ls-tree", "--name-only", tree, "--", r.ownDir)
		if err != nil {
			return "", fmt.Errorf("snapshot: %w", err)
		}
		if strings.TrimSpace(listed) != "" {
			return "", fmt.Errorf("snapshot picked up turnback's own %s/ folder; check the ignore rules for it", r.ownDir)
		}
	}
	return tree, nil
}

// Commit stores tree as a commit, with parent unless it is empty, and
// returns the commit id.
func (r *Repo) Commit(tree, parent, message string) (string, error) {
	args := []string{"commit-tree", "--no-gpg-sign", "-m", message}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	out, err := r.run.Run(append(args, tree)...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// SetRef points ref at id. Anything reachable from a ref survives git gc.
func (r *Repo) SetRef(ref, id string) error {
	_, err := r.run.Run("update-ref", ref, id)
	return err
}

// DeleteRef removes ref.
func (r *Repo) DeleteRef(ref string) error {
	_, err := r.run.Run("update-ref", "-d", ref)
	return err
}

// Changes lists the files that differ between two snapshots (trees or
// commits), sorted by path, with renames detected.
func (r *Repo) Changes(from, to string) ([]Change, error) {
	raw, err := r.run.Run("diff-tree", "-r", "-z", "-M", "--raw", from, to)
	if err != nil {
		return nil, err
	}
	numstat, err := r.run.Run("diff-tree", "-r", "-z", "-M", "--numstat", from, to)
	if err != nil {
		return nil, err
	}
	entries, err := parseRaw(raw)
	if err != nil {
		return nil, err
	}
	stats, err := parseNumstat(numstat)
	if err != nil {
		return nil, err
	}
	changes := make([]Change, 0, len(entries))
	for _, e := range entries {
		c := Change{Status: e.status, Path: e.path, OldPath: e.oldPath}
		if s, ok := stats[e.path]; ok {
			c.Added, c.Deleted, c.Binary = s.added, s.deleted, s.binary
		}
		changes = append(changes, c)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// Patch returns the diff between two snapshots as a standard patch that
// includes binary content.
func (r *Repo) Patch(from, to string) (string, error) {
	return r.run.Run("diff-tree", "-p", "--binary", "--full-index", "-M", "--no-color", from, to)
}

// Tidy packs loose objects once enough have piled up (git gc --auto). It
// runs in the foreground and does nothing most of the time.
func (r *Repo) Tidy() error {
	_, err := r.run.Run("gc", "--auto", "--quiet")
	return err
}

// Diff returns a readable diff between two snapshots, limited to paths if
// any are given. Binary files are summarized in one line.
func (r *Repo) Diff(from, to string, paths ...string) (string, error) {
	args := []string{"diff-tree", "-p", "-M", "--no-color", from, to}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	return r.run.Run(args...)
}

// CountFiles returns the number of files in a snapshot.
func (r *Repo) CountFiles(tree string) (int, error) {
	out, err := r.run.Run("ls-tree", "-r", "-z", "--name-only", tree)
	if err != nil {
		return 0, err
	}
	return strings.Count(out, "\x00"), nil
}

type rawEntry struct {
	status, path, oldPath string
}

// parseRaw parses "git diff-tree -r -z --raw" output.
func parseRaw(out string) ([]rawEntry, error) {
	tokens := strings.Split(out, "\x00")
	var entries []rawEntry
	for i := 0; i < len(tokens); i++ {
		meta := tokens[i]
		if meta == "" {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(meta, ":"))
		if !strings.HasPrefix(meta, ":") || len(fields) != 5 || i+1 >= len(tokens) {
			return nil, fmt.Errorf("unexpected diff output %q", meta)
		}
		e := rawEntry{status: fields[4][:1]}
		i++
		e.path = tokens[i]
		if e.status == "R" || e.status == "C" {
			if i+1 >= len(tokens) {
				return nil, fmt.Errorf("unexpected diff output after %q", meta)
			}
			i++
			e.oldPath, e.path = e.path, tokens[i]
		}
		entries = append(entries, e)
	}
	return entries, nil
}

type lineStat struct {
	added, deleted int
	binary         bool
}

// parseNumstat parses "git diff-tree -r -z --numstat" output, keyed by the
// path after the change.
func parseNumstat(out string) (map[string]lineStat, error) {
	tokens := strings.Split(out, "\x00")
	stats := make(map[string]lineStat)
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "" {
			continue
		}
		parts := strings.SplitN(tok, "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected numstat output %q", tok)
		}
		path := parts[2]
		if path == "" { // rename: old and new paths follow as separate tokens
			if i+2 >= len(tokens) {
				return nil, fmt.Errorf("unexpected numstat output after %q", tok)
			}
			path = tokens[i+2]
			i += 2
		}
		var s lineStat
		if parts[0] == "-" {
			s.binary = true
		} else {
			var err1, err2 error
			s.added, err1 = strconv.Atoi(parts[0])
			s.deleted, err2 = strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("unexpected numstat output %q", tok)
			}
		}
		stats[path] = s
	}
	return stats, nil
}
