package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/store"
)

var logCommand = &command{
	name:    "log",
	summary: "List recorded turns, newest first",
	usage: `Usage: turnback log [--since <when>] [--file <path>]... [-n <count>] [--json]

List recorded turns, newest first, with the number of files and lines each
one changed.

Options:
      --since <when>   only turns recorded since then: a date (2026-09-30),
                       a date and time ("2026-09-30 14:00"), a duration
                       (30m, 2h, 3d, 1w), today or yesterday
      --file <path>    only turns that changed this file, or anything in
                       this folder; repeat to match several paths
  -n, --limit <count>  show at most this many turns
      --json           print the turns as JSON
      --no-pager       do not page long output
`,
	run: runLog,
}

var showCommand = &command{
	name:    "show",
	summary: "Show what a turn changed",
	usage: `Usage: turnback show [<turn>] [--file <path>]... [--stat] [--json]

Show a turn's details, the files it changed and its diff. <turn> is an id
from 'turnback log' or 'last', which is also the default.

Options:
      --file <path>  only show this file, or the files in this folder;
                     repeat to show several paths
      --stat         show the list of files without the diff
      --json         print the turn and its diff as JSON
      --no-pager     do not page long output
`,
	run: runShow,
}

// pathList collects repeated --file flags.
type pathList []string

func (p *pathList) String() string     { return strings.Join(*p, ", ") }
func (p *pathList) Set(v string) error { *p = append(*p, v); return nil }

// repoPaths converts --file arguments into repository paths.
func repoPaths(a *app.App, args []string) ([]string, error) {
	var out []string
	for _, arg := range args {
		p, err := a.RepoPath(arg)
		if err != nil {
			return nil, usageErrorf("%v", err)
		}
		out = append(out, p)
	}
	return out, nil
}

func runLog(env *Env, args []string) error {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	var since string
	var files pathList
	var limit int
	var asJSON, noPager bool
	fs.StringVar(&since, "since", "", "")
	fs.Var(&files, "file", "")
	fs.IntVar(&limit, "n", 0, "")
	fs.IntVar(&limit, "limit", 0, "")
	fs.BoolVar(&asJSON, "json", false, "")
	fs.BoolVar(&noPager, "no-pager", false, "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if err := noArguments("log", pos); err != nil {
		return err
	}
	now := env.Now()
	opts := app.LogOptions{Limit: limit}
	if since != "" {
		if opts.Since, err = parseSince(since, now); err != nil {
			return usageErrorf("%v", err).withHint(`Examples: --since 2h, --since 3d, --since 2026-09-30, --since "2026-09-30 14:00".`)
		}
	}
	a, err := app.Open(env.Dir, env.Now)
	if err != nil {
		return explain(env, err)
	}
	if opts.Paths, err = repoPaths(a, files); err != nil {
		return err
	}
	turns, err := a.Log(opts)
	if err != nil {
		return explain(env, err)
	}
	if asJSON {
		if turns == nil {
			turns = []*store.Turn{}
		}
		return writeJSON(env, turns)
	}
	sess, err := a.Store.Session()
	if err != nil {
		return err
	}
	if !noPager {
		defer env.startPager()()
	}
	if sess != nil {
		fmt.Fprintln(env.Stdout, env.paint(dim, fmt.Sprintf(
			"A turn is being recorded (started %s). It will be listed after 'turnback end'.", ago(now, sess.StartedAt))))
	}
	if len(turns) == 0 {
		if since == "" && len(files) == 0 {
			fmt.Fprintln(env.Stdout, "No turns recorded yet. Record one with 'turnback start' and 'turnback end'.")
		} else {
			fmt.Fprintln(env.Stdout, "No turns match.")
		}
		return nil
	}
	printLog(env, turns, now)
	return nil
}

// printLog prints turns as an aligned table.
func printLog(env *Env, turns []*store.Turn, now time.Time) {
	withAgent := false
	for _, t := range turns {
		withAgent = withAgent || t.Agent != ""
	}
	header := []string{"ID", "WHEN", "FILES", "CHANGES"}
	if withAgent {
		header = append(header, "AGENT")
	}
	header = append(header, "DESCRIPTION")
	rightAligned := map[int]bool{0: true, 2: true}

	rows := [][]string{header}
	for _, t := range turns {
		added, deleted := t.Lines()
		row := []string{
			strconv.Itoa(t.ID),
			shortWhen(now, t.EndedAt),
			strconv.Itoa(len(t.Files)),
			fmt.Sprintf("+%d -%d", added, deleted),
		}
		if withAgent {
			row = append(row, t.Agent)
		}
		rows = append(rows, append(row, t.Description))
	}
	widths := make([]int, len(header))
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}
	for r, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			pad := strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell))
			styled := cell
			switch {
			case r == 0:
				styled = env.paint(bold, cell)
			case i == 0:
				styled = env.paint(yellow, cell)
			case i == 3:
				plus, minus, _ := strings.Cut(cell, " ")
				styled = env.paint(green, plus) + " " + env.paint(red, minus)
			}
			switch {
			case i == len(row)-1:
				b.WriteString(styled)
			case rightAligned[i]:
				b.WriteString(pad + styled + "  ")
			default:
				b.WriteString(styled + pad + "  ")
			}
		}
		fmt.Fprintln(env.Stdout, strings.TrimRight(b.String(), " "))
	}
}

// shortWhen formats a time for the log: "today 15:02", "yesterday 09:10",
// "Sep 28 18:40" within the year, and a date before that.
func shortWhen(now, t time.Time) string {
	t, now = t.Local(), now.Local()
	switch {
	case sameDay(t, now):
		return "today " + t.Format("15:04")
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "yesterday " + t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("Jan 2 15:04")
	}
	return t.Format("2006-01-02")
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

var durationPattern = regexp.MustCompile(
	`^(\d+)\s*(s|secs?|seconds?|m|mins?|minutes?|h|hrs?|hours?|d|days?|w|weeks?)(\s+ago)?$`)

var sinceLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// parseSince reads the --since value: a duration back from now, a date or
// date and time in local time, or "today" or "yesterday".
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch lower {
	case "today":
		return midnight, nil
	case "yesterday":
		return midnight.AddDate(0, 0, -1), nil
	}
	if m := durationPattern.FindStringSubmatch(lower); m != nil {
		n, err := strconv.Atoi(m[1])
		if err == nil {
			unit := map[byte]time.Duration{
				's': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour,
			}[m[2][0]]
			return now.Add(-time.Duration(n) * unit), nil
		}
	}
	for _, layout := range sinceLayouts {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot read %q as a date or a duration", s)
}

func runShow(env *Env, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	var files pathList
	var stat, asJSON, noPager bool
	fs.Var(&files, "file", "")
	fs.BoolVar(&stat, "stat", false, "")
	fs.BoolVar(&asJSON, "json", false, "")
	fs.BoolVar(&noPager, "no-pager", false, "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	ref := "last"
	switch len(pos) {
	case 0:
	case 1:
		ref = pos[0]
	default:
		return usageErrorf("show takes one turn, got %d", len(pos)).withHint("Run 'turnback help show' for usage.")
	}
	a, err := app.Open(env.Dir, env.Now)
	if err != nil {
		return explain(env, err)
	}
	turn, err := a.ResolveTurn(ref)
	if err != nil {
		return explain(env, err)
	}
	paths, err := repoPaths(a, files)
	if err != nil {
		return err
	}
	var diff string
	if !stat || asJSON {
		if diff, err = a.Diff(turn, paths); err != nil {
			return explain(env, err)
		}
	}
	if asJSON {
		return writeJSON(env, struct {
			*store.Turn
			Diff string `json:"diff"`
		}{turn, diff})
	}
	if !noPager {
		defer env.startPager()()
	}
	shown := turn.Files
	if len(paths) > 0 {
		shown = app.FilesTouching(turn, paths)
	}
	printTurn(env, turn, shown, env.Now())
	if !stat {
		fmt.Fprintln(env.Stdout)
		writeDiff(env, diff)
	}
	return nil
}

// printTurn prints a turn's header and the files it changed, or the subset
// of them given in shown.
func printTurn(env *Env, t *store.Turn, shown []store.File, now time.Time) {
	fmt.Fprintf(env.Stdout, "%s  %s\n", env.paint(boldYellow, fmt.Sprintf("turn %d", t.ID)), t.Description)
	fmt.Fprintf(env.Stdout, "Recorded  %s (%s), %s\n",
		t.EndedAt.Local().Format("2006-01-02 15:04"), ago(now, t.EndedAt), took(t.EndedAt.Sub(t.StartedAt)))
	if t.Agent != "" {
		fmt.Fprintf(env.Stdout, "Agent     %s\n", t.Agent)
	}
	if u := t.Undoes; u != nil {
		line := fmt.Sprintf("Undoes    turn %d", u.Turn)
		if len(u.Paths) > 0 {
			line += " (" + strings.Join(u.Paths, ", ") + ")"
		}
		fmt.Fprintln(env.Stdout, line)
	}
	fmt.Fprintln(env.Stdout)
	printFiles(env, shown)
	if len(shown) < len(t.Files) {
		fmt.Fprintf(env.Stdout, "Showing %d of %s\n", len(shown), plural(len(t.Files), "file"))
		return
	}
	added, deleted := t.Lines()
	fmt.Fprintf(env.Stdout, "%s changed, +%d -%d\n", plural(len(t.Files), "file"), added, deleted)
}

// took describes how long a turn was recorded for.
func took(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "took under a minute"
	case d < time.Hour:
		return "took " + plural(int(d/time.Minute), "minute")
	}
	return "took " + plural(int(d/time.Hour), "hour")
}

func writeJSON(env *Env, v any) error {
	enc := json.NewEncoder(env.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
