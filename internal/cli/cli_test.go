package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// run executes a command line in dir and returns exit code, stdout, stderr.
func run(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := &Env{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Dir:    dir,
		Now:    time.Now,
	}
	code := Run(env, args)
	return code, stdout.String(), stderr.String()
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	code, _, stderr := run(t, t.TempDir(), "frobnicate")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, `unknown command "frobnicate"`) {
		t.Errorf("stderr = %q, want it to name the unknown command", stderr)
	}
}

func TestVersionPrintsTheReleaseVersion(t *testing.T) {
	saved := version
	version = "v1.2.3"
	defer func() { version = saved }()

	for _, arg := range []string{"--version", "version"} {
		code, stdout, _ := run(t, t.TempDir(), arg)
		if code != 0 {
			t.Errorf("%s: exit code = %d, want 0", arg, code)
		}
		if stdout != "turnback v1.2.3\n" {
			t.Errorf("%s: stdout = %q, want %q", arg, stdout, "turnback v1.2.3\n")
		}
	}
}

func TestNoArgumentsPrintsUsage(t *testing.T) {
	code, stdout, _ := run(t, t.TempDir())
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("stdout = %q, want usage text", stdout)
	}
}
