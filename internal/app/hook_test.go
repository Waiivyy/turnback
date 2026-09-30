package app_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/store"
	"github.com/Waiivyy/turnback/internal/testutil"
)

func installHook(t *testing.T, a *app.App) string {
	t.Helper()
	path, err := a.InstallHook("/usr/local/bin/turnback")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func recordCommit(t *testing.T, a *app.App) *store.Turn {
	t.Helper()
	turn, err := a.RecordCommit()
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func TestInstallHookWritesAnExecutablePostCommitHook(t *testing.T) {
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)
	path := installHook(t, a)
	if path != repo.Path(".git/hooks/post-commit") {
		t.Errorf("hook path = %q", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#!/bin/sh", "hook post-commit", "/usr/local/bin/turnback", "exit 0"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("hook lacks %q:\n%s", want, b)
		}
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode()&0o111 == 0 {
		t.Errorf("hook mode = %v, want executable", fi.Mode())
	}
	if !a.HookInstalled() {
		t.Error("HookInstalled() = false after InstallHook")
	}
	// Installing again is fine.
	if _, err := a.InstallHook("/usr/local/bin/turnback"); err != nil {
		t.Errorf("second InstallHook: %v", err)
	}
}

func TestInstallHookNeverReplacesSomeoneElsesHook(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write(".git/hooks/post-commit", "#!/bin/sh\necho mine\n")
	a := openApp(t, repo)
	var exists *app.HookExistsError
	if _, err := a.InstallHook("/usr/local/bin/turnback"); !errors.As(err, &exists) {
		t.Fatalf("err = %v, want HookExistsError", err)
	}
	if got := repo.Read(".git/hooks/post-commit"); got != "#!/bin/sh\necho mine\n" {
		t.Errorf("the existing hook changed to %q", got)
	}
	if err := a.UninstallHook(); err == nil {
		t.Error("UninstallHook removed a hook turnback did not write")
	}
	if !repo.Exists(".git/hooks/post-commit") {
		t.Error("the existing hook was deleted")
	}
}

func TestInstallHookLeavesManagedHooksAlone(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Git("config", "core.hooksPath", ".husky")
	a := openApp(t, repo)
	var managed *app.HooksManagedError
	if _, err := a.InstallHook("/usr/local/bin/turnback"); !errors.As(err, &managed) || managed.Dir != ".husky" {
		t.Fatalf("err = %v, want HooksManagedError for .husky", err)
	}
	if repo.Exists(".husky") {
		t.Error("turnback wrote into the managed hooks folder")
	}
}

func TestUninstallHookRemovesOnlyItsOwnHook(t *testing.T) {
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)
	path := installHook(t, a)
	if err := a.UninstallHook(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("hook still exists: %v", err)
	}
	if a.HookInstalled() {
		t.Error("HookInstalled() = true after UninstallHook")
	}
}

func TestEachCommitRecordsWhatChangedSinceTheLastTurn(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	installHook(t, a)

	repo.Write("a.txt", "2\n")
	repo.Write("b.txt", "new\n")
	repo.Commit("Add b and bump a")
	first := recordCommit(t, a)
	if first == nil || first.Kind != store.KindCommit || first.Description != "Add b and bump a" {
		t.Fatalf("first commit turn = %+v", first)
	}
	if first.Commit != repo.Git("rev-parse", "HEAD") {
		t.Errorf("turn commit = %q, want HEAD", first.Commit)
	}
	if got := paths(first.Files); !reflect.DeepEqual(got, []string{"M a.txt", "A b.txt"}) {
		t.Errorf("first commit turn files = %q", got)
	}

	// A commit that changes nothing new records nothing.
	repo.Git("commit", "-q", "--allow-empty", "-m", "empty")
	if turn := recordCommit(t, a); turn != nil {
		t.Errorf("empty commit recorded %+v", turn)
	}

	repo.Write("c.txt", "c\n")
	repo.Commit("Add c")
	second := recordCommit(t, a)
	if second == nil || !reflect.DeepEqual(paths(second.Files), []string{"A c.txt"}) {
		t.Errorf("second commit turn = %+v", second)
	}
}

func TestCommitsDoNotRecordDuringAnExplicitTurn(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	installHook(t, a)

	start(t, a, app.StartOptions{Description: "Agent work"})
	repo.Write("a.txt", "2\n")
	repo.Commit("mid-turn commit")
	if turn := recordCommit(t, a); turn != nil {
		t.Fatalf("a commit during start/end recorded %+v", turn)
	}
	end(t, a, app.EndOptions{})

	// The next commit turn starts where the explicit turn ended.
	repo.Write("b.txt", "b\n")
	repo.Commit("Add b")
	turn := recordCommit(t, a)
	if turn == nil || !reflect.DeepEqual(paths(turn.Files), []string{"A b.txt"}) {
		t.Errorf("commit turn after an explicit turn = %+v", turn)
	}
}

func TestCommitsAreNotRecordedDuringARebase(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit("initial")
	a := openApp(t, repo)
	installHook(t, a)
	if err := os.MkdirAll(repo.Path(".git/rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo.Write("a.txt", "1\n")
	repo.Commit("while rebasing")
	if turn := recordCommit(t, a); turn != nil {
		t.Errorf("a commit during a rebase recorded %+v", turn)
	}
}

func TestACommitTurnCanBeUndone(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	installHook(t, a)
	repo.Write("a.txt", "2\n")
	repo.Commit("Bump a")
	turn := recordCommit(t, a)
	applyUndo(t, a, planUndo(t, a, turn.ID), false)
	if got := repo.Read("a.txt"); got != "1\n" {
		t.Errorf("a.txt = %q", got)
	}
}

func TestTheHookDoesNothingWhenItIsAlreadyRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as a stand-in for turnback")
	}
	repo := testutil.NewRepo(t)
	a := openApp(t, repo)
	hook, err := a.InstallHook("")
	if err != nil {
		t.Fatal(err)
	}
	// A stand-in turnback that leaves a mark when the hook runs it.
	bin := testutil.TempDir(t)
	mark := filepath.Join(bin, "ran")
	fake := "#!/bin/sh\ntouch '" + mark + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "turnback"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	runHook := func(extra ...string) {
		t.Helper()
		cmd := exec.Command("sh", hook)
		cmd.Dir = repo.Dir
		cmd.Env = append(os.Environ(), append([]string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}, extra...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook: %v\n%s", err, out)
		}
	}

	runHook("TURNBACK_IN_HOOK=1")
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("the hook ran turnback although it was already running inside itself")
	}
	runHook()
	if _, err := os.Stat(mark); err != nil {
		t.Error("the hook did not run turnback on a normal commit")
	}
}
