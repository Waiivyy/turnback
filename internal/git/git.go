// Package git runs git commands with an environment that inherited variables
// cannot redirect to another repository. git exports GIT_DIR, GIT_INDEX_FILE
// and friends to hooks, and turnback must never act on the wrong repository
// or index because it was started from one.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner runs git in a fixed directory with fixed global options.
type Runner struct {
	Dir    string   // working directory for every command
	Opts   []string // global options placed before the subcommand, e.g. "--git-dir=..."
	Env    []string // extra KEY=VALUE environment entries
	Detach bool     // run git outside the terminal's process group, immune to Ctrl-C
}

// Error is returned when git exits with a non-zero status.
type Error struct {
	Args     []string // subcommand and its arguments
	ExitCode int
	Stderr   string
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", e.ExitCode)
	}
	name := "git"
	if len(e.Args) > 0 {
		name += " " + e.Args[0]
	}
	return name + ": " + msg
}

// With returns a copy of r that adds the given KEY=VALUE environment entries.
func (r Runner) With(env ...string) Runner {
	r.Env = append(append([]string{}, r.Env...), env...)
	return r
}

// Run runs git with args and returns its standard output.
func (r Runner) Run(args ...string) (string, error) {
	out, err := r.RunInput(nil, args...)
	return string(out), err
}

// RunInput runs git with args, feeding it stdin, and returns its standard
// output.
func (r Runner) RunInput(stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append(append([]string{}, r.Opts...), args...)...)
	cmd.Dir = r.Dir
	cmd.Env = append(cleanEnv(os.Environ()), r.Env...)
	if r.Detach {
		detach(cmd)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.Bytes(), &Error{Args: args, ExitCode: exitErr.ExitCode(), Stderr: stderr.String()}
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("git is not installed or not on PATH")
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// locationVars are environment variables that point git at a repository,
// index or object store other than the one found from the working directory,
// or change how diffs are produced or how pathspecs are read.
var locationVars = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_COMMON_DIR":                   true,
	"GIT_NAMESPACE":                    true,
	"GIT_PREFIX":                       true,
	"GIT_QUARANTINE_PATH":              true,
	"GIT_GRAFT_FILE":                   true,
	"GIT_SHALLOW_FILE":                 true,
	"GIT_REPLACE_REF_BASE":             true,
	"GIT_EXTERNAL_DIFF":                true,
	"GIT_DIFF_OPTS":                    true,
	"GIT_OPTIONAL_LOCKS":               true,
	"GIT_LITERAL_PATHSPECS":            true,
	"GIT_GLOB_PATHSPECS":               true,
	"GIT_NOGLOB_PATHSPECS":             true,
	"GIT_ICASE_PATHSPECS":              true,
}

func cleanEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if locationVars[strings.ToUpper(key)] {
			continue
		}
		out = append(out, kv)
	}
	// Read-only commands such as "git status" must not rewrite the user's
	// index as a side effect.
	return append(out, "GIT_OPTIONAL_LOCKS=0")
}
