package shadow_test

import "os/exec"

// execGit runs git and returns raw stdout, keeping trailing whitespace and
// carriage returns intact.
func execGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}
