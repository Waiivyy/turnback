package cli

import (
	"strings"
	"testing"

	"github.com/Waiivyy/turnback/internal/testutil"
)

func TestHookInstallAndUninstall(t *testing.T) {
	repo := testutil.NewRepo(t)
	code, stdout, stderr := run(t, repo.Dir, "hook", "install")
	if code != 0 || !strings.Contains(stdout, "Installed a post-commit hook in .git/hooks/post-commit") {
		t.Fatalf("install: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	_, status, _ := run(t, repo.Dir, "status")
	if !strings.Contains(status, "Every commit records a turn") {
		t.Errorf("status does not mention the hook:\n%s", status)
	}
	code, stdout, _ = run(t, repo.Dir, "hook", "uninstall")
	if code != 0 || !strings.Contains(stdout, "Removed the post-commit hook") {
		t.Errorf("uninstall: exit %d, stdout %q", code, stdout)
	}
	code, _, stderr = run(t, repo.Dir, "hook", "uninstall")
	if code != 1 || !strings.Contains(stderr, "no turnback hook is installed") {
		t.Errorf("second uninstall: exit %d, stderr %q", code, stderr)
	}
}

func TestHookInstallExplainsWhatToDoWithAnExistingHook(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".git/hooks/post-commit", "#!/bin/sh\necho mine\n")
	code, _, stderr := run(t, repo.Dir, "hook", "install")
	if code != 1 || !strings.Contains(stderr, "was not written by turnback") || !strings.Contains(stderr, "turnback hook post-commit") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	repo2 := testutil.NewRepo(t)
	repo2.Git("config", "core.hooksPath", ".husky")
	code, _, stderr = run(t, repo2.Dir, "hook", "install")
	if code != 1 || !strings.Contains(stderr, "managed in .husky") || !strings.Contains(stderr, "turnback hook post-commit") {
		t.Errorf("managed hooks: exit %d, stderr %q", code, stderr)
	}
}

func TestPostCommitRecordsTheCommitAsATurn(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	repo.Commit("initial")
	run(t, repo.Dir, "hook", "install")
	repo.Write("a.txt", "2\n")
	repo.Commit("Bump a")

	code, _, stderr := run(t, repo.Dir, "hook", "post-commit")
	if code != 0 || !strings.Contains(stderr, "turnback: recorded turn 1 (1 file changed, +1 -1)") {
		t.Errorf("post-commit: exit %d, stderr %q", code, stderr)
	}
	_, show, _ := run(t, repo.Dir, "show", "1", "--stat")
	if !strings.Contains(show, "turn 1  Bump a") || !strings.Contains(show, "Commit    "+repo.Git("rev-parse", "--short", "HEAD")) {
		t.Errorf("show:\n%s", show)
	}
	// Nothing new: silent.
	code, stdout, stderr := run(t, repo.Dir, "hook", "post-commit")
	if code != 0 || stdout != "" || stderr != "" {
		t.Errorf("second post-commit: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
