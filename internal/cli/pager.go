package cli

import (
	"os"
	"os/exec"
	"runtime"
)

// pagerCommand picks the pager for long output: $TURNBACK_PAGER, then
// $PAGER, then less if it is installed. Setting either variable to "" or
// "cat" turns paging off.
func pagerCommand() string {
	for _, name := range []string{"TURNBACK_PAGER", "PAGER"} {
		if p, ok := os.LookupEnv(name); ok {
			if p == "" || p == "cat" {
				return ""
			}
			return p
		}
	}
	if _, err := exec.LookPath("less"); err == nil {
		return "less"
	}
	return ""
}

// startPager sends the rest of the command's standard output through the
// pager, if one is configured, and returns a function that waits for it.
func (e *Env) startPager() (wait func()) {
	if e.Pager == "" || runtime.GOOS == "windows" {
		return func() {}
	}
	cmd := exec.Command("sh", "-c", e.Pager)
	cmd.Stdout, cmd.Stderr = e.Stdout, e.Stderr
	cmd.Env = os.Environ()
	if os.Getenv("LESS") == "" {
		// Quit if one screen is enough, keep colors, leave the screen as is.
		cmd.Env = append(cmd.Env, "LESS=FRX")
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return func() {}
	}
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	out := e.Stdout
	e.Stdout = in
	return func() {
		in.Close()
		cmd.Wait()
		e.Stdout = out
	}
}
