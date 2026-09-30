// Package testutil provides helpers for tests that need real git
// repositories. It runs git directly, independently of the code under test.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Isolate makes git ignore the developer's global and system configuration
// for the rest of the test, so personal settings cannot change results.
func Isolate(t testing.TB) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// Also hides the default global ignore and attributes files.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GIT_AUTHOR_NAME", "Test Author")
	t.Setenv("GIT_AUTHOR_EMAIL", "author@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test Author")
	t.Setenv("GIT_COMMITTER_EMAIL", "author@example.com")
}

// SetGlobalConfig sets a value in the isolated global git configuration.
func SetGlobalConfig(t testing.TB, key, value string) {
	t.Helper()
	cmd := exec.Command("git", "config", "--global", key, value)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config --global %s: %v\n%s", key, err, out)
	}
}

// TempDir returns a new temporary directory with symlinks resolved, so it
// compares equal to the paths git reports (on macOS /var is a symlink).
func TempDir(t testing.TB) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Repo is a git repository in a temporary directory.
type Repo struct {
	t   testing.TB
	Dir string
}

// NewRepo creates an empty git repository with isolated configuration.
func NewRepo(t testing.TB) *Repo {
	t.Helper()
	Isolate(t)
	r := &Repo{t: t, Dir: TempDir(t)}
	r.Git("init", "-q", "-b", "main")
	return r
}

// Git runs git in the repository and returns its trimmed output.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Path returns the absolute path of a slash-separated repository path.
func (r *Repo) Path(rel string) string {
	return filepath.Join(r.Dir, filepath.FromSlash(rel))
}

// Write creates or replaces a file, creating parent directories.
func (r *Repo) Write(rel, content string) {
	r.t.Helper()
	p := r.Path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// Read returns a file's content.
func (r *Repo) Read(rel string) string {
	r.t.Helper()
	b, err := os.ReadFile(r.Path(rel))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

// Exists reports whether a path exists (without following a final symlink).
func (r *Repo) Exists(rel string) bool {
	_, err := os.Lstat(r.Path(rel))
	return err == nil
}

// Remove deletes a file.
func (r *Repo) Remove(rel string) {
	r.t.Helper()
	if err := os.Remove(r.Path(rel)); err != nil {
		r.t.Fatal(err)
	}
}

// Commit stages everything and commits it.
func (r *Repo) Commit(msg string) {
	r.t.Helper()
	r.Git("add", "-A")
	r.Git("commit", "-q", "--allow-empty", "-m", msg)
}
