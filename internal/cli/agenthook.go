package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/git"
)

// payloadWait is how long a hook waits for the JSON input an agent passes
// on standard input before it goes on without it.
var payloadWait = 2 * time.Second

// payloadLimit caps how much of an agent's input a hook reads.
const payloadLimit = 8 << 20

// agentHook runs 'turnback hook start' or 'turnback hook end' for an agent's
// hook. Agents act on a hook's exit status and output: status 2 blocks the
// prompt in most of them, or keeps the agent working, and several add a
// prompt hook's output to the model's context. So whatever happens, the exit
// status is 0, nothing is printed when all goes well, and a problem is
// reported as one line on standard error. Only a request for help is passed
// on, so that the usage is printed.
func agentHook(env *Env, action string, args []string) error {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(env.Stderr, "turnback: hook %s failed: %s\n", action, shownText(fmt.Sprint(r)))
		}
	}()
	err := recordFromHook(env, action, args)
	switch {
	case err == nil:
	case errors.Is(err, flag.ErrHelp):
		return err
	default:
		fmt.Fprintf(env.Stderr, "turnback: %s\n", hookMessage(action, err))
	}
	return nil
}

func recordFromHook(env *Env, action string, args []string) error {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	var agent string
	if action == "start" {
		fs.StringVar(&agent, "agent", "", "")
	}
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageErrorf("unexpected argument %q", pos[0])
	}
	a, err := hookApp(env, readPayload(env))
	if action == "end" {
		if errors.Is(err, git.ErrNotRepository) {
			return nil // no turn can be open outside a repository
		}
		if err == nil {
			_, err = a.End(app.EndOptions{})
		}
		if err == nil || errors.Is(err, app.ErrNoSession) {
			return nil
		}
		return explain(env, err)
	}
	if err == nil {
		_, err = a.Start(app.StartOptions{Agent: agent, EndOpen: true})
	}
	if err != nil {
		return explain(env, err)
	}
	return nil
}

// hookMessage is the one line that reports err from a hook.
func hookMessage(action string, err error) string {
	msg := err.Error()
	var ce *cliError
	if errors.As(err, &ce) {
		msg = ce.msg
	}
	msg, _, _ = strings.Cut(msg, "\n")
	what := "not recording this turn"
	if action == "end" {
		what = "turn not recorded yet"
	}
	return what + ": " + shownText(msg)
}

// hookApp opens turnback on the project a hook is about: the first folder
// the agent's input names that is inside a git repository, or else the
// folder the hook runs in. Cursor, for one, runs user-level hooks from its
// own settings folder.
func hookApp(env *Env, payload map[string]any) (*app.App, error) {
	for _, dir := range payloadDirs(payload) {
		if a, err := app.Open(dir, env.Now); !errors.Is(err, git.ErrNotRepository) {
			return a, err
		}
	}
	return app.Open(env.Dir, env.Now)
}

// payloadDirs returns the existing folders, given as absolute paths, that an
// agent's input names: workspace_roots (Cursor), then cwd (most others).
func payloadDirs(payload map[string]any) []string {
	var dirs []string
	add := func(v any) {
		if dir, ok := v.(string); ok && filepath.IsAbs(dir) {
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				dirs = append(dirs, dir)
			}
		}
	}
	if roots, ok := payload["workspace_roots"].([]any); ok {
		for _, root := range roots {
			add(root)
		}
	}
	add(payload["cwd"])
	return dirs
}

// readPayload returns the JSON object an agent passes its hook on standard
// input, or nil. It never reads a terminal, waits only briefly for input
// that does not come, and stops reading once the object is complete, so an
// agent that keeps standard input open does not hold the hook up.
func readPayload(env *Env) map[string]any {
	if env.Stdin == nil || env.Interactive {
		return nil
	}
	got := make(chan map[string]any, 1)
	go func() {
		var payload map[string]any
		if json.NewDecoder(io.LimitReader(env.Stdin, payloadLimit)).Decode(&payload) != nil {
			payload = nil
		}
		got <- payload
	}()
	select {
	case payload := <-got:
		return payload
	case <-time.After(payloadWait):
		return nil
	}
}
