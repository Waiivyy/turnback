package cli

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Waiivyy/turnback/internal/testutil"
)

// startUI runs 'turnback ui' in dir until the returned stop function is
// called, and returns the address it opened.
func startUI(t *testing.T, dir string, args ...string) (url string, stop func() (int, string)) {
	t.Helper()
	opened := make(chan string, 1)
	interrupt := make(chan os.Signal, 1)
	savedOpen, savedSignals := openURL, uiSignals
	openURL = func(u string) error { opened <- u; return nil }
	uiSignals = func() (<-chan os.Signal, func()) { return interrupt, func() {} }
	t.Cleanup(func() { openURL, uiSignals = savedOpen, savedSignals })

	var stdout bytes.Buffer
	env := &Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: io.Discard, Dir: dir, Now: time.Now}
	done := make(chan int, 1)
	go func() { done <- Run(env, append([]string{"ui"}, args...)) }()
	select {
	case url = <-opened:
	case code := <-done:
		t.Fatalf("turnback ui exited with %d before opening a browser:\n%s", code, stdout.String())
	case <-time.After(10 * time.Second):
		t.Fatal("turnback ui did not open a browser")
	}
	return url, func() (int, string) {
		interrupt <- os.Interrupt
		select {
		case code := <-done:
			return code, stdout.String()
		case <-time.After(10 * time.Second):
			t.Fatal("turnback ui did not stop")
		}
		return 0, ""
	}
}

func TestUIServesTheTurnsUntilInterrupted(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Commit("initial")
	run(t, repo.Dir, "start", "-m", "Add a second line")
	repo.Write("a.txt", "one\ntwo\n")
	run(t, repo.Dir, "end")

	url, stop := startUI(t, repo.Dir)
	if !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/\?token=[0-9a-f]{48}$`).MatchString(url) {
		t.Fatalf("opened %q, want a loopback address with a token", url)
	}
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(page), "<title>turnback</title>") {
		t.Errorf("page: status %d\n%.300s", res.StatusCode, page)
	}
	res, err = http.Get(strings.Replace(url, "/?", "/api/turns?", 1))
	if err != nil {
		t.Fatal(err)
	}
	turns, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(turns), "Add a second line") {
		t.Errorf("turns: %s", turns)
	}

	code, stdout := stop()
	if code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
	if !strings.Contains(stdout, url) || !strings.Contains(stdout, "Press Ctrl-C to stop.") || !strings.Contains(stdout, "Stopped.") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if _, err := http.Get(url); err == nil {
		t.Error("the server still answers after it stopped")
	}
}

func TestUINoOpenOnlyPrintsTheAddress(t *testing.T) {
	repo := testutil.NewRepo(t)
	opened := false
	savedOpen := openURL
	openURL = func(string) error { opened = true; return nil }
	defer func() { openURL = savedOpen }()
	interrupt := make(chan os.Signal, 1)
	savedSignals := uiSignals
	uiSignals = func() (<-chan os.Signal, func()) { return interrupt, func() {} }
	defer func() { uiSignals = savedSignals }()

	interrupt <- os.Interrupt // stop as soon as it is serving
	code, stdout, stderr := run(t, repo.Dir, "ui", "--no-open")
	if code != 0 || opened {
		t.Errorf("exit %d, opened %v, stderr %q", code, opened, stderr)
	}
	if !regexp.MustCompile(`http://127\.0\.0\.1:\d+/\?token=`).MatchString(stdout) {
		t.Errorf("stdout does not show the address:\n%s", stdout)
	}
}

func TestUIRejectsBadArguments(t *testing.T) {
	repo := testutil.NewRepo(t)
	for _, args := range [][]string{{"ui", "extra"}, {"ui", "--port", "70000"}, {"ui", "--port", "-1"}} {
		if code, _, stderr := run(t, repo.Dir, args...); code != 2 {
			t.Errorf("%v: exit %d, stderr %q; want a usage error", args, code, stderr)
		}
	}
	if code, _, stderr := run(t, t.TempDir(), "ui"); code != 1 || !strings.Contains(stderr, "not inside a git repository") {
		t.Errorf("outside a repository: exit %d, stderr %q", code, stderr)
	}
}

func TestUIReportsABusyPort(t *testing.T) {
	repo := testutil.NewRepo(t)
	url, stop := startUI(t, repo.Dir)
	defer stop()
	port := regexp.MustCompile(`:(\d+)/`).FindStringSubmatch(url)[1]
	code, _, stderr := run(t, repo.Dir, "ui", "--port", port, "--no-open")
	if code != 1 || !strings.Contains(stderr, "port "+port) {
		t.Errorf("busy port: exit %d, stderr %q", code, stderr)
	}
}
