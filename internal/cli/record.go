package cli

import (
	"flag"
	"fmt"

	"github.com/Waiivyy/turnback/internal/app"
)

var startCommand = &command{
	name:    "start",
	summary: "Start recording a turn",
	usage: `Usage: turnback start [-m <description>] [--agent <name>]

Snapshot the working tree and start recording a turn. Run it right before
you hand a task to an AI agent, and run 'turnback end' when the agent is
done. Edits you make before 'start' or after 'end' are never part of the
turn.

Options:
  -m, --message <text>  what the agent was asked to do
      --agent <name>    which agent is making the changes, e.g. cursor
`,
	run: runStart,
}

var endCommand = &command{
	name:    "end",
	summary: "Finish the turn and record what changed",
	usage: `Usage: turnback end [-m <description>] [--discard]

Snapshot the working tree again and record everything that changed since
'turnback start' as a new turn. Without a description, turnback writes a
short summary of the files that changed.

Options:
  -m, --message <text>  describe the turn (replaces the one given to start)
      --discard         stop recording without saving a turn
`,
	run: runEnd,
}

var statusCommand = &command{
	name:    "status",
	summary: "Show whether a turn is being recorded",
	usage: `Usage: turnback status

Show whether a turn is being recorded, what it has changed so far, and the
most recent recorded turn.
`,
	run: runStatus,
}

func runStart(env *Env, args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	var desc, agent string
	fs.StringVar(&desc, "m", "", "")
	fs.StringVar(&desc, "message", "", "")
	fs.StringVar(&agent, "agent", "", "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if err := noArguments("start", pos); err != nil {
		return err
	}
	a, err := app.Open(env.Dir, env.Now)
	if err != nil {
		return explain(env, err)
	}
	res, err := a.Start(app.StartOptions{Description: desc, Agent: agent})
	if err != nil {
		return explain(env, err)
	}
	if res.Initialized {
		fmt.Fprintln(env.Stdout, "Initialized turnback in .turnback/ (git ignores this folder).")
	}
	title := "Recording a new turn"
	if desc != "" {
		title += ": " + shownText(desc)
	}
	fmt.Fprintf(env.Stdout, "%s (snapshot of %s).\n", title, plural(res.Files, "file"))
	fmt.Fprintln(env.Stdout, "Run 'turnback end' when the agent is done.")
	return nil
}

func runEnd(env *Env, args []string) error {
	fs := flag.NewFlagSet("end", flag.ContinueOnError)
	var desc string
	var discard bool
	fs.StringVar(&desc, "m", "", "")
	fs.StringVar(&desc, "message", "", "")
	fs.BoolVar(&discard, "discard", false, "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if err := noArguments("end", pos); err != nil {
		return err
	}
	a, err := app.Open(env.Dir, env.Now)
	if err != nil {
		return explain(env, err)
	}
	res, err := a.End(app.EndOptions{Description: desc, Discard: discard})
	if err != nil {
		return explain(env, err)
	}
	switch {
	case res.Discarded:
		fmt.Fprintf(env.Stdout, "Discarded the turn started %s. Nothing was recorded.\n", ago(env.Now(), res.Session.StartedAt))
	case res.Turn == nil:
		fmt.Fprintln(env.Stdout, "Nothing changed since 'turnback start'. No turn recorded.")
	default:
		t := res.Turn
		fmt.Fprintf(env.Stdout, "%s: %s\n", env.paint(bold, fmt.Sprintf("Recorded turn %d", t.ID)), shownText(t.Description))
		printFiles(env, t.Files)
		added, deleted := t.Lines()
		fmt.Fprintf(env.Stdout, "%s changed, +%d -%d\n", plural(len(t.Files), "file"), added, deleted)
		fmt.Fprintf(env.Stdout, "See it with 'turnback show %d', undo it with 'turnback undo %d'.\n", t.ID, t.ID)
	}
	return nil
}

func runStatus(env *Env, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if err := noArguments("status", pos); err != nil {
		return err
	}
	a, err := app.Open(env.Dir, env.Now)
	if err != nil {
		return explain(env, err)
	}
	st, err := a.Status()
	if err != nil {
		return explain(env, err)
	}
	now := env.Now()
	if !st.Initialized {
		fmt.Fprintln(env.Stdout, "Nothing recorded in this repository yet. Start a turn with 'turnback start',")
		fmt.Fprintln(env.Stdout, "or record one at every commit with 'turnback hook install'.")
		return nil
	}
	if s := st.Session; s != nil {
		line := fmt.Sprintf("Recording a turn since %s (%s)", clockTime(now, s.StartedAt), ago(now, s.StartedAt))
		if s.Description != "" {
			line += ": " + shownText(s.Description)
		}
		fmt.Fprintln(env.Stdout, line)
		if len(st.Pending) == 0 {
			fmt.Fprintln(env.Stdout, "No changes so far.")
		} else {
			fmt.Fprintf(env.Stdout, "%s changed so far:\n", plural(len(st.Pending), "file"))
			printFiles(env, st.Pending)
		}
		fmt.Fprintln(env.Stdout, "Run 'turnback end' to record the turn.")
	} else {
		fmt.Fprintln(env.Stdout, "Not recording. Start a turn with 'turnback start'.")
	}
	if a.HookInstalled() {
		fmt.Fprintln(env.Stdout, "Every commit records a turn (post-commit hook installed).")
	}
	if st.Latest != nil {
		fmt.Fprintf(env.Stdout, "%s recorded. Latest: turn %d, %s (%s).\n",
			plural(st.Turns, "turn"), st.Latest.ID, shownText(st.Latest.Description), ago(now, st.Latest.EndedAt))
	} else {
		fmt.Fprintln(env.Stdout, "No turns recorded yet.")
	}
	return nil
}
