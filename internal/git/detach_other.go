//go:build !unix && !windows

package git

import "os/exec"

func detach(cmd *exec.Cmd) {}
