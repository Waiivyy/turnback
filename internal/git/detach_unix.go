//go:build unix

package git

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in its own process group, so the terminal's Ctrl-C does
// not reach it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
