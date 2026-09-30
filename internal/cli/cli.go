// Package cli implements turnback's command-line interface.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

// version is set for release builds with
// -ldflags "-X github.com/Waiivyy/turnback/internal/cli.version=v1.2.3".
var version = ""

// Version returns the version of this build.
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// Env holds everything a command needs from the outside world, so commands
// can run in tests without a real terminal.
type Env struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Dir         string           // directory the command runs in
	Now         func() time.Time // clock used for timestamps
	Color       bool             // colorize output with ANSI escapes
	Interactive bool             // stdin is a terminal, so prompts are allowed
	Pager       string           // shell command to page long output through, or ""
}

// Main runs turnback with the process's arguments and terminal and returns
// the exit code.
func Main() int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "turnback: %v\n", err)
		return 1
	}
	env := &Env{
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Dir:         dir,
		Now:         time.Now,
		Color:       isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
		Interactive: isTerminal(os.Stdin),
	}
	if isTerminal(os.Stdout) {
		env.Pager = pagerCommand()
	}
	return Run(env, os.Args[1:])
}

// command is one turnback subcommand.
type command struct {
	name    string
	summary string
	usage   string
	run     func(env *Env, args []string) error
}

// commands lists the subcommands in the order they appear in help.
var commands []*command

func init() {
	commands = []*command{startCommand, endCommand, statusCommand, logCommand, showCommand, undoCommand}
}

func lookup(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

// Run executes one turnback command line and returns the exit code.
func Run(env *Env, args []string) int {
	if len(args) == 0 {
		printUsage(env.Stdout)
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		if len(args) > 1 {
			if c := lookup(args[1]); c != nil {
				fmt.Fprint(env.Stdout, c.usage)
				return 0
			}
			return report(env, usageErrorf("unknown command %q", args[1]).withHint("Run 'turnback help' to see the available commands."))
		}
		printUsage(env.Stdout)
		return 0
	case "-v", "--version", "version":
		fmt.Fprintf(env.Stdout, "turnback %s\n", Version())
		return 0
	}
	c := lookup(args[0])
	if c == nil {
		return report(env, usageErrorf("unknown command %q", args[0]).withHint("Run 'turnback help' to see the available commands."))
	}
	err := c.run(env, args[1:])
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(env.Stdout, c.usage)
		return 0
	}
	return report(env, err)
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `turnback records the changes AI coding agents make to your working tree,
one turn at a time, and lets you inspect or undo any turn on its own.

Usage:
  turnback <command> [options]

Commands:
`)
	for _, c := range commands {
		fmt.Fprintf(w, "  %-8s  %s\n", c.name, c.summary)
	}
	fmt.Fprint(w, `
Run 'turnback help <command>' for details about a command.
`)
}

// cliError is a failure with an exit code and optional hints for the user.
type cliError struct {
	msg   string
	hints []string
	code  int
}

func (e *cliError) Error() string { return e.msg }

func (e *cliError) withHint(hints ...string) *cliError {
	e.hints = append(e.hints, hints...)
	return e
}

// usageErrorf reports a mistake in how turnback was invoked (exit code 2).
func usageErrorf(format string, a ...any) *cliError {
	return &cliError{msg: fmt.Sprintf(format, a...), code: 2}
}

// errorf reports a failure to do what was asked (exit code 1).
func errorf(format string, a ...any) *cliError {
	return &cliError{msg: fmt.Sprintf(format, a...), code: 1}
}

// errSilentFailure makes a command exit with code 1 after it has already
// explained the problem on its own output.
var errSilentFailure = errors.New("silent failure")

// report prints err (if any) and returns the matching exit code.
func report(env *Env, err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, errSilentFailure) {
		return 1
	}
	var ce *cliError
	if errors.As(err, &ce) {
		fmt.Fprintf(env.Stderr, "turnback: %s\n", ce.msg)
		for _, h := range ce.hints {
			fmt.Fprintf(env.Stderr, "hint: %s\n", h)
		}
		return ce.code
	}
	fmt.Fprintf(env.Stderr, "turnback: %v\n", err)
	return 1
}

// parseFlags parses args with fs and returns the positional arguments.
// Flags and positional arguments may be mixed, so "undo 3 --yes" and
// "undo --yes 3" both work. Everything after "--" is positional.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var afterDashes []string
	for i, a := range args {
		if a == "--" {
			args, afterDashes = args[:i], args[i+1:]
			break
		}
	}
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, usageErrorf("%s", flagMessage(err)).
				withHint(fmt.Sprintf("Run 'turnback help %s' for usage.", fs.Name()))
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
	return append(positional, afterDashes...), nil
}

// flagMessage rewords the flag package's errors for people.
func flagMessage(err error) string {
	msg := err.Error()
	if name, ok := strings.CutPrefix(msg, "flag provided but not defined: -"); ok {
		return "unknown option " + dashes(name) + name
	}
	if name, ok := strings.CutPrefix(msg, "flag needs an argument: -"); ok {
		return "option " + dashes(name) + name + " needs a value"
	}
	return strings.TrimPrefix(msg, "flag ")
}

func dashes(name string) string {
	if len(name) == 1 {
		return "-"
	}
	return "--"
}

// noArguments rejects positional arguments for commands that take none.
func noArguments(name string, positional []string) error {
	if len(positional) > 0 {
		return usageErrorf("unexpected argument %q", positional[0]).
			withHint(fmt.Sprintf("Run 'turnback help %s' for usage.", name))
	}
	return nil
}
