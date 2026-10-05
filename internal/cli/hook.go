package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Waiivyy/turnback/internal/app"
)

var hookCommand = &command{
	name:    "hook",
	summary: "Record turns from git's or an agent's hooks",
	usage: `Usage: turnback hook install | uninstall
       turnback hook start [--agent <name>]
       turnback hook end

Record a turn at every commit instead of wrapping each turn in start and
end. 'install' adds a post-commit hook to this repository; from then on,
each commit records everything that changed since the previous turn as a
turn named after the commit. 'uninstall' removes the hook again.

turnback never replaces a hook it did not write. If this repository already
has a post-commit hook, or its hooks are managed by a tool through
core.hooksPath, add this line to that post-commit hook instead:

    turnback hook post-commit

'turnback start' and 'turnback end' keep working alongside the hook: while
a turn is being recorded, commits do not close turns.

'start' and 'end' are for an AI agent's own hooks. Run 'turnback hook start'
from the hook that fires when you submit a prompt, and 'turnback hook end'
from the one that fires when the agent has finished. They record a turn
like 'turnback start' and 'turnback end', but never get in the agent's way:
they always exit with status 0 and print nothing unless something went
wrong, they find the project in the JSON the agent passes on standard
input, and 'hook start' first records a turn that was never ended.

Options for 'hook start':
      --agent <name>  which agent is making the changes, e.g. cursor
`,
	run: runHook,
}

const hookLine = "turnback hook post-commit"

// selfPath returns the path of this turnback binary for the hook to fall
// back on, or "" when this process is not a turnback binary (a test, say).
func selfPath() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	if name := filepath.Base(self); name != "turnback" && name != "turnback.exe" {
		return ""
	}
	return self
}

func runHook(env *Env, args []string) error {
	if len(args) > 0 && (args[0] == "start" || args[0] == "end") {
		return agentHook(env, args[0], args[1:])
	}
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("hook needs 'install', 'uninstall', 'start' or 'end'").withHint("Run 'turnback help hook' for usage.")
	}
	a, err := app.Open(env.Dir, env.Now)
	if err != nil {
		return explain(env, err)
	}
	switch pos[0] {
	case "install":
		path, err := a.InstallHook(selfPath())
		var exists *app.HookExistsError
		var managed *app.HooksManagedError
		switch {
		case errors.As(err, &exists):
			return errorf("%s", err).withHint("Add this line to that hook instead: " + hookLine)
		case errors.As(err, &managed):
			return errorf("%s", err).withHint("Add this line to the post-commit hook there instead: " + hookLine)
		case err != nil:
			return explain(env, err)
		}
		if rel, err := filepath.Rel(a.Root, path); err == nil {
			path = filepath.ToSlash(rel)
		}
		fmt.Fprintf(env.Stdout, "Installed a post-commit hook in %s. From now on, every commit records a turn.\n", path)
		fmt.Fprintln(env.Stdout, "Remove it with 'turnback hook uninstall'.")
	case "uninstall":
		if err := a.UninstallHook(); err != nil {
			var exists *app.HookExistsError
			if errors.As(err, &exists) {
				return errorf("%s, so turnback leaves it alone", err)
			}
			return errorf("%s", err)
		}
		fmt.Fprintln(env.Stdout, "Removed the post-commit hook. Commits no longer record turns.")
	case "post-commit":
		turn, err := a.RecordCommit()
		if err != nil {
			return errorf("could not record this commit: %v", err)
		}
		if turn != nil {
			added, deleted := turn.Lines()
			fmt.Fprintf(env.Stderr, "turnback: recorded turn %d (%s changed, +%d -%d)\n",
				turn.ID, plural(len(turn.Files), "file"), added, deleted)
		}
	default:
		return usageErrorf("unknown hook action %q", pos[0]).withHint("Run 'turnback help hook' for usage.")
	}
	return nil
}
