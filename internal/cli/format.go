package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/git"
	"github.com/Waiivyy/turnback/internal/store"
)

// ANSI styles, used only when Env.Color is set.
const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	cyan   = "\x1b[36m"
)

func (e *Env) paint(style, s string) string {
	if !e.Color || s == "" {
		return s
	}
	return style + s + reset
}

var statusStyle = map[string]string{"A": green, "M": yellow, "D": red, "R": cyan, "T": yellow}

// printFiles lists changed files with their status letter and line counts.
func printFiles(env *Env, files []store.File) {
	names := make([]string, len(files))
	width := 0
	for i, f := range files {
		names[i] = f.Path
		if f.OldPath != "" {
			names[i] = f.OldPath + " -> " + f.Path
		}
		width = max(width, utf8.RuneCountInString(names[i]))
	}
	width = min(width, 60)
	for i, f := range files {
		line := "  " + env.paint(statusStyle[f.Status], f.Status) + "  " + names[i]
		if counts := lineCounts(env, f); counts != "" {
			pad := max(width-utf8.RuneCountInString(names[i]), 0) + 2
			line += strings.Repeat(" ", pad) + counts
		}
		fmt.Fprintln(env.Stdout, line)
	}
}

func lineCounts(env *Env, f store.File) string {
	if f.Binary {
		return "binary"
	}
	var parts []string
	if f.Added > 0 {
		parts = append(parts, env.paint(green, fmt.Sprintf("+%d", f.Added)))
	}
	if f.Deleted > 0 {
		parts = append(parts, env.paint(red, fmt.Sprintf("-%d", f.Deleted)))
	}
	return strings.Join(parts, " ")
}

// plural formats a count with its unit, such as "1 file" or "3 files".
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// ago describes how long before now t was, in words.
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	case d < 30*24*time.Hour:
		return plural(int(d/(24*time.Hour)), "day") + " ago"
	}
	return "on " + t.Local().Format("2006-01-02")
}

// clockTime formats t as a time of day, adding the date unless it is today.
func clockTime(now, t time.Time) string {
	t, now = t.Local(), now.Local()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("2006-01-02 15:04")
}

// explain turns known failures into messages with a hint on what to do.
func explain(env *Env, err error) error {
	var active *app.SessionActiveError
	switch {
	case errors.Is(err, git.ErrNotRepository):
		return errorf("not inside a git repository").
			withHint("turnback works on top of git. Run it inside a repository, or create one with 'git init'.")
	case errors.Is(err, app.ErrNoSession):
		return errorf("no turn is being recorded").
			withHint("Start one with 'turnback start'.")
	case errors.As(err, &active):
		return errorf("a turn is already being recorded (started %s)", ago(env.Now(), active.Session.StartedAt)).
			withHint("Run 'turnback end' to record it, or 'turnback end --discard' to drop it.")
	case errors.Is(err, store.ErrLocked):
		return errorf("%v", err)
	}
	return err
}
