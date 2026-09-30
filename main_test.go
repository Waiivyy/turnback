package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Waiivyy/turnback/internal/testutil"
)

// binary is the turnback executable built for the end-to-end tests.
var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "turnback-e2e-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "turnback")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		panic("building turnback: " + err.Error() + "\n" + string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// turnback runs the built binary in dir.
func turnback(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turnback %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestTheInstalledHookRecordsRealCommits(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	repo.Commit("initial")
	// The hook finds turnback on the PATH of whoever commits.
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	turnback(t, repo.Dir, "hook", "install")

	repo.Write("a.txt", "2\n")
	repo.Write("b.txt", "new\n")
	repo.Git("add", "-A")
	commit := exec.Command("git", "commit", "-m", "Add b and bump a")
	commit.Dir = repo.Dir
	out, err := commit.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "turnback: recorded turn 1 (2 files changed, +2 -1)") {
		t.Errorf("git commit output lacks the hook's line:\n%s", out)
	}
	if log := turnback(t, repo.Dir, "log"); !strings.Contains(log, "Add b and bump a") {
		t.Errorf("log:\n%s", log)
	}
}
