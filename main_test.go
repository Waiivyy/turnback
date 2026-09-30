package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestTheWebPageServesTurnsUntilCtrlC(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	repo.Commit("initial")
	turnback(t, repo.Dir, "start", "-m", "Bump a")
	repo.Write("a.txt", "2\n")
	turnback(t, repo.Dir, "end")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "ui", "--no-open")
	cmd.Dir = repo.Dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(stdout)
	address := regexp.MustCompile(`http://127\.0\.0\.1:\d+/\?token=[0-9a-f]+`)
	var url string
	for url == "" && lines.Scan() {
		url = address.FindString(lines.Text())
	}
	if url == "" {
		cmd.Process.Kill()
		t.Fatalf("turnback ui printed no address (%v)", cmd.Wait())
	}

	res, err := http.Get(strings.Replace(url, "/?", "/api/turns?", 1))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "Bump a") {
		t.Errorf("turns: status %d, body %s", res.StatusCode, body)
	}

	if runtime.GOOS == "windows" {
		// Windows cannot send Ctrl-C to one process from Go.
		cmd.Process.Kill()
		cmd.Wait()
		return
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	var rest strings.Builder
	for lines.Scan() {
		rest.WriteString(lines.Text() + "\n")
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("turnback ui exited with %v after Ctrl-C", err)
	}
	if !strings.Contains(rest.String(), "Stopped.") {
		t.Errorf("output after Ctrl-C:\n%s", rest.String())
	}
}
