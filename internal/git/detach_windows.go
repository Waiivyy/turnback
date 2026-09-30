//go:build windows

package git

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in its own process group, so the console's Ctrl-C does
// not reach it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
