package cli

import (
	"bufio"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Waiivyy/turnback/internal/app"
)

var undoCommand = &command{
	name:    "undo",
	summary: "Take one turn back out, keeping everything after it",
	usage: `Usage: turnback undo <turn> [--file <path>]... [--yes] [--dry-run] [--force]

Take the changes of one turn back out of the working tree while keeping
every change made after it, by later turns or by you. <turn> is an id from
'turnback log', or 'last'.

turnback first shows what it would change. It only writes files when you
confirm at the prompt, or when you pass --yes. If a later change overlaps
the lines the turn changed, nothing is written and turnback shows where.
Every undo is recorded as a turn of its own, so an undo can be undone.

Options:
      --file <path>  only undo this file, or the files in this folder;
                     repeat to undo several paths
  -y, --yes          apply without asking
  -n, --dry-run      only show what would change, even with --yes
  -f, --force        also rewrite files whose current changes are neither
                     committed nor recorded (they are saved first)
`,
	run: runUndo,
}

func runUndo(env *Env, args []string) error {
	fs := flag.NewFlagSet("undo", flag.ContinueOnError)
	var files pathList
	var yes, dryRun, force bool
	fs.Var(&files, "file", "")
	fs.BoolVar(&yes, "y", false, "")
	fs.BoolVar(&yes, "yes", false, "")
	fs.BoolVar(&dryRun, "n", false, "")
	fs.BoolVar(&dryRun, "dry-run", false, "")
	fs.BoolVar(&force, "f", false, "")
	fs.BoolVar(&force, "force", false, "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("undo needs one turn, as in 'turnback undo 3' or 'turnback undo last'").
			withHint("Run 'turnback log' to see the recorded turns.")
	}
	a, err := app.Open(env.Dir, env.Now)
	if err != nil {
		return explain(env, err)
	}
	turn, err := a.ResolveTurn(pos[0])
	if err != nil {
		return explain(env, err)
	}
	paths, err := repoPaths(a, files)
	if err != nil {
		return err
	}
	plan, err := a.PlanUndo(turn, app.UndoOptions{Paths: paths})
	if err != nil {
		return explain(env, err)
	}

	printUndoPlan(env, plan)
	if conflicts := plan.Conflicts(); len(conflicts) > 0 {
		printConflictDetails(env, plan)
		return errorf("cannot undo turn %d cleanly, so nothing was changed", turn.ID).
			withHint("Undo the later turns first, or leave the conflicting files out and undo the others with --file.")
	}
	if plan.Writes() == 0 {
		if alreadyUndone(plan) {
			fmt.Fprintf(env.Stdout, "Nothing to undo: every file is already as it was before turn %d.\n", turn.ID)
		} else {
			fmt.Fprintf(env.Stdout, "Nothing to undo: turnback cannot change the files turn %d changed, for the reasons listed above.\n", turn.ID)
		}
		return nil
	}
	fmt.Fprintln(env.Stdout)
	writeDiff(env, plan.Preview)

	if dirty := plan.DirtyPaths(); len(dirty) > 0 && !force {
		verb := "has"
		if len(dirty) > 1 {
			verb = "have"
		}
		return errorf("%s %s changes that are neither committed nor recorded", joinAnd(dirty), verb).
			withHint("Commit them first, or run again with --force. turnback saves the current state before writing, so a forced undo can be undone too.")
	}

	again := "turnback undo " + shellQuote(pos[0])
	for _, f := range files {
		again += " --file " + shellQuote(f)
	}
	if force {
		again += " --force"
	}
	switch {
	case dryRun || (!yes && !env.Interactive):
		fmt.Fprintf(env.Stdout, "\nDry run: nothing was changed. To apply it, run '%s --yes'.\n", again)
		return nil
	case !yes:
		// The question goes to stderr so it stays visible if stdout is redirected.
		fmt.Fprint(env.Stderr, "\nApply this undo? [y/N] ")
		answer, _ := bufio.NewReader(env.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(env.Stdout, "Nothing was changed.")
			return nil
		}
	}

	u, err := a.ApplyUndo(plan, force)
	if err != nil {
		return explain(env, err)
	}
	added, deleted := u.Lines()
	fmt.Fprintf(env.Stdout, "\nUndid turn %d: %s changed, +%d -%d.\n", turn.ID, plural(plan.Writes(), "file"), added, deleted)
	fmt.Fprintf(env.Stdout, "Recorded as turn %d. To take this undo back, run 'turnback undo %d'.\n", u.ID, u.ID)
	return nil
}

// alreadyUndone reports whether every file was skipped because it already
// matches its state from before the turn.
func alreadyUndone(p *app.UndoPlan) bool {
	for _, f := range p.Files {
		if f.Action != app.UndoSkip || !strings.HasPrefix(f.Reason, "already as it was before turn") {
			return false
		}
	}
	return true
}

var undoLetters = map[string]string{
	app.UndoRevert: "M", app.UndoMerge: "M", app.UndoRestore: "A",
	app.UndoDelete: "D", app.UndoSkip: " ", app.UndoConflict: "!",
}

var undoStyles = map[string]string{
	app.UndoRevert: yellow, app.UndoMerge: yellow, app.UndoRestore: green,
	app.UndoDelete: red, app.UndoConflict: red,
}

// printUndoPlan lists what the undo would do with each file.
func printUndoPlan(env *Env, p *app.UndoPlan) {
	fmt.Fprintln(env.Stdout, env.paint(bold, fmt.Sprintf("Undo turn %d: %s", p.Turn.ID, shownText(p.Turn.Description))))
	fmt.Fprintln(env.Stdout)
	width := 0
	for _, f := range p.Files {
		width = max(width, utf8.RuneCountInString(shownPath(f.Path)))
	}
	width = min(width, 60)
	for _, f := range p.Files {
		name := shownPath(f.Path)
		pad := strings.Repeat(" ", max(width-utf8.RuneCountInString(name), 0))
		line := fmt.Sprintf("  %s  %s%s  %s", env.paint(undoStyles[f.Action], undoLetters[f.Action]), name, pad,
			describeUndo(p, f))
		if f.Dirty {
			line += " " + env.paint(red, "(unsaved changes)")
		}
		fmt.Fprintln(env.Stdout, strings.TrimRight(line, " "))
	}
	if len(p.Later) > 0 && len(p.Conflicts()) == 0 && p.Writes() > 0 {
		subject, pronoun, verb := "turn "+strconv.Itoa(p.Later[0]), "Its", "it relies"
		if len(p.Later) > 1 {
			subject, pronoun, verb = "turns "+joinAnd(itoas(p.Later)), "Their", "they rely"
		}
		fmt.Fprintln(env.Stdout)
		fmt.Fprint(env.Stdout, wrap(fmt.Sprintf(
			"Note: %s came after turn %d. %s changes are kept, but if %s on what turn %d did, run your tests after the undo.",
			subject, p.Turn.ID, pronoun, verb, p.Turn.ID), 76))
	}
}

// wrap breaks text into lines of at most width characters at spaces.
func wrap(text string, width int) string {
	var b strings.Builder
	line := 0
	for _, word := range strings.Fields(text) {
		n := utf8.RuneCountInString(word)
		if line > 0 && line+1+n > width {
			b.WriteString("\n")
			line = 0
		}
		if line > 0 {
			b.WriteString(" ")
			line++
		}
		b.WriteString(word)
		line += n
	}
	b.WriteString("\n")
	return b.String()
}

func describeUndo(p *app.UndoPlan, f app.UndoFile) string {
	id := p.Turn.ID
	switch f.Action {
	case app.UndoRevert:
		return fmt.Sprintf("put back the version from before turn %d", id)
	case app.UndoRestore:
		return fmt.Sprintf("bring it back: turn %d deleted it", id)
	case app.UndoDelete:
		return fmt.Sprintf("delete it: turn %d created it", id)
	case app.UndoMerge:
		return fmt.Sprintf("take out turn %d's changes, keep %s", id, keptChanges(f))
	case app.UndoSkip:
		return "skip: " + f.Reason
	}
	return "conflict: " + f.Reason
}

// keptChanges says whose later changes a merge keeps.
func keptChanges(f app.UndoFile) string {
	var parts []string
	switch len(f.Later) {
	case 0:
	case 1:
		parts = append(parts, "turn "+strconv.Itoa(f.Later[0]))
	default:
		parts = append(parts, "turns "+joinAnd(itoas(f.Later)))
	}
	if f.Outside {
		parts = append(parts, "your own edits")
	}
	if len(parts) == 0 {
		return "the later changes"
	}
	return "the later changes from " + strings.Join(parts, " and ")
}

// printConflictDetails explains each conflict: who changed the file later,
// and where the edits collide.
func printConflictDetails(env *Env, p *app.UndoPlan) {
	fmt.Fprintln(env.Stdout)
	for _, f := range p.Conflicts() {
		fmt.Fprintln(env.Stdout, env.paint(bold, shownPath(f.Path)))
		var who []string
		switch len(f.Later) {
		case 0:
		case 1:
			who = append(who, "turn "+strconv.Itoa(f.Later[0]))
		default:
			who = append(who, "turns "+joinAnd(itoas(f.Later)))
		}
		if f.Outside {
			who = append(who, "edits made outside any turn")
		}
		if len(who) > 0 {
			fmt.Fprintf(env.Stdout, "     Also changed later by %s.\n", strings.Join(who, " and by "))
		}
		if f.Markers != "" {
			fmt.Fprintln(env.Stdout)
			for _, line := range strings.Split(strings.TrimRight(f.Markers, "\n"), "\n") {
				if line == "" {
					fmt.Fprintln(env.Stdout)
					continue
				}
				fmt.Fprintln(env.Stdout, "       "+markerLine(env, line))
			}
		}
		fmt.Fprintln(env.Stdout)
	}
}

func markerLine(env *Env, line string) string {
	for _, marker := range []string{"<<<<<<< ", "=======", ">>>>>>> "} {
		if strings.HasPrefix(line, marker) {
			return env.paint(cyan, line)
		}
	}
	return line
}

func itoas(ids []int) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.Itoa(id)
	}
	return out
}

// shellQuote quotes s for a POSIX shell when it needs quoting.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"$`\\*?[]{}()<>|&;!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
