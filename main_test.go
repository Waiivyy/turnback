package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
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

func TestAnUndoThatRunsOutOfSpaceLeavesNothingBehind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs ulimit")
	}
	repo := testutil.NewRepo(t)
	big := strings.Repeat("0123456789abcdef", 3<<16) // 3 MB
	repo.Write("big.bin", big)
	repo.Commit("initial")
	turnback(t, repo.Dir, "start", "-m", "Delete the big file")
	repo.Remove("big.bin")
	turnback(t, repo.Dir, "end")

	// A file size limit far below 3 MB makes git's write of big.bin stop
	// partway, as a full disk would.
	undo := exec.Command("sh", "-c", `ulimit -f 1024 && exec "$0" undo 1 --yes`, binary)
	undo.Dir = repo.Dir
	out, err := undo.CombinedOutput()
	if err == nil {
		t.Fatalf("the undo succeeded under the limit:\n%s", out)
	}
	if !strings.Contains(string(out), "nothing was changed") {
		t.Errorf("output:\n%s", out)
	}
	if repo.Exists("big.bin") {
		t.Error("a partly written big.bin was left behind")
	}
	// Without the limit, the same undo works.
	turnback(t, repo.Dir, "undo", "1", "--yes")
	if repo.Read("big.bin") != big {
		t.Error("big.bin did not come back whole")
	}
}

// mirror writes a release archive holding the built binary, and its
// checksums, the way scripts/build-release.sh lays them out.
func mirror(t *testing.T) (dir, archive string) {
	t.Helper()
	dir = t.TempDir()
	name := "turnback_" + runtime.GOOS + "_" + runtime.GOARCH
	bin, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name + "/turnback", Mode: 0o755, Size: int64(len(bin))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(bin)
	tw.Close()
	gz.Close()
	archive = filepath.Join(dir, name+".tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	sums := fmt.Sprintf("%x  %s.tar.gz\n", sum, name)
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, archive
}

func install(t *testing.T, from, to string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "install.sh")
	cmd.Env = append(os.Environ(), "TURNBACK_BASE_URL=file://"+from, "TURNBACK_INSTALL_DIR="+to)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestTheInstallScriptInstallsOnlyVerifiedBinaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for Unix systems")
	}
	for _, tool := range []string{"sh", "curl", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "freebsd/amd64":
	default:
		t.Skip("no release archive is built for this platform")
	}
	from, archive := mirror(t)
	to := filepath.Join(t.TempDir(), "bin")
	out, err := install(t, from, to)
	if err != nil || !strings.Contains(out, "Installed turnback") {
		t.Fatalf("install: %v\n%s", err, out)
	}
	installed := filepath.Join(to, "turnback")
	if got, err := exec.Command(installed, "--version").Output(); err != nil || !strings.HasPrefix(string(got), "turnback ") {
		t.Fatalf("the installed binary: %v, %q", err, got)
	}
	before, _ := os.ReadFile(installed)

	// A tampered archive is refused, and the installed binary stays.
	tampered, _ := os.ReadFile(archive)
	tampered[len(tampered)/2] ^= 0xff
	if err := os.WriteFile(archive, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = install(t, from, to)
	if err == nil || !strings.Contains(out, "checksum mismatch") || !strings.Contains(out, "nothing was installed") {
		t.Errorf("tampered install: %v\n%s", err, out)
	}
	if after, _ := os.ReadFile(installed); !bytes.Equal(before, after) {
		t.Error("the installed binary changed")
	}
	if entries, _ := os.ReadDir(to); len(entries) != 1 {
		t.Errorf("install left files behind in %s: %d entries", to, len(entries))
	}
}
