package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Waiivyy/turnback/internal/testutil"
)

// runWithInput runs a command line in dir with input on standard input, the
// way an agent runs a hook, and returns exit code, stdout and stderr.
func runWithInput(t *testing.T, dir string, input io.Reader, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := &Env{Stdin: input, Stdout: &stdout, Stderr: &stderr, Dir: dir, Now: time.Now}
	code := Run(env, args)
	return code, stdout.String(), stderr.String()
}

// jsonInput returns v encoded as JSON, as an agent passes it to a hook.
func jsonInput(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

// notARepository returns a folder that is not inside any git repository.
func notARepository(t *testing.T) string {
	t.Helper()
	testutil.Isolate(t)
	dir := testutil.TempDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	return dir
}

type loggedTurn struct {
	ID    int    `json:"id"`
	Agent string `json:"agent"`
	Files []struct {
		Path   string `json:"path"`
		Status string `json:"status"`
	} `json:"files"`
}

// changes lists a turn's files as "M a.txt, A b.txt".
func (lt loggedTurn) changes() string {
	var out []string
	for _, f := range lt.Files {
		out = append(out, f.Status+" "+f.Path)
	}
	return strings.Join(out, ", ")
}

// loggedTurns returns the turns recorded in dir, newest first.
func loggedTurns(t *testing.T, dir string) []loggedTurn {
	t.Helper()
	code, stdout, stderr := run(t, dir, "log", "--json")
	if code != 0 {
		t.Fatalf("log: exit %d, stderr %q", code, stderr)
	}
	var turns []loggedTurn
	if err := json.Unmarshal([]byte(stdout), &turns); err != nil {
		t.Fatalf("log --json is not JSON: %v\n%s", err, stdout)
	}
	return turns
}

// quiet fails the test unless a hook exited 0 without printing anything.
func quiet(t *testing.T, what string, code int, stdout, stderr string) {
	t.Helper()
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("%s: exit %d, stdout %q, stderr %q; want exit 0 and no output", what, code, stdout, stderr)
	}
}

// oneLine fails the test unless a hook exited 0 with nothing on stdout and
// exactly one line on stderr that contains want.
func oneLine(t *testing.T, what string, code int, stdout, stderr, want string) {
	t.Helper()
	if code != 0 || stdout != "" || strings.Count(stderr, "\n") != 1 || !strings.HasSuffix(stderr, "\n") || !strings.Contains(stderr, want) {
		t.Fatalf("%s: exit %d, stdout %q, stderr %q; want exit 0 and one line mentioning %q", what, code, stdout, stderr, want)
	}
}

func committedRepo(t *testing.T) *testutil.Repo {
	t.Helper()
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	repo.Commit("initial")
	return repo
}

func TestHookStartAndEndRecordATurnWithoutOutput(t *testing.T) {
	repo := committedRepo(t)
	code, stdout, stderr := runWithInput(t, repo.Dir, strings.NewReader(`{}`), "hook", "start", "--agent", "cursor")
	quiet(t, "hook start", code, stdout, stderr)
	repo.Write("a.txt", "2\n")
	code, stdout, stderr = runWithInput(t, repo.Dir, strings.NewReader(`{}`), "hook", "end")
	quiet(t, "hook end", code, stdout, stderr)

	turns := loggedTurns(t, repo.Dir)
	if len(turns) != 1 || turns[0].Agent != "cursor" || turns[0].changes() != "M a.txt" {
		t.Errorf("turns = %+v, want one turn by cursor changing a.txt", turns)
	}
}

func TestHookStartRecordsATurnThatWasNeverEnded(t *testing.T) {
	repo := committedRepo(t)
	runWithInput(t, repo.Dir, strings.NewReader(""), "hook", "start", "--agent", "codex")
	repo.Write("a.txt", "2\n")
	// The agent was stopped, so its stop hook never ran.
	code, stdout, stderr := runWithInput(t, repo.Dir, strings.NewReader(""), "hook", "start", "--agent", "codex")
	quiet(t, "second hook start", code, stdout, stderr)
	repo.Write("b.txt", "new\n")
	runWithInput(t, repo.Dir, strings.NewReader(""), "hook", "end")

	turns := loggedTurns(t, repo.Dir)
	if len(turns) != 2 || turns[0].changes() != "A b.txt" || turns[1].changes() != "M a.txt" {
		t.Errorf("turns = %+v, want turn 1 changing a.txt and turn 2 adding b.txt", turns)
	}
}

func TestHookEndWithoutAStartDoesNothing(t *testing.T) {
	repo := testutil.NewRepo(t)
	code, stdout, stderr := runWithInput(t, repo.Dir, strings.NewReader(`{}`), "hook", "end")
	quiet(t, "hook end", code, stdout, stderr)
	if repo.Exists(".turnback") {
		t.Error("hook end created .turnback")
	}
}

func TestHookFindsTheProjectInTheHookInput(t *testing.T) {
	repo := committedRepo(t)
	// Cursor runs user-level hooks from its settings folder and names the
	// project in workspace_roots; other agents name it in cwd.
	settings := notARepository(t)
	code, stdout, stderr := runWithInput(t, settings, jsonInput(t, map[string]any{"workspace_roots": []string{repo.Dir}}), "hook", "start")
	quiet(t, "hook start", code, stdout, stderr)
	repo.Write("a.txt", "2\n")
	code, stdout, stderr = runWithInput(t, settings, jsonInput(t, map[string]any{"cwd": repo.Dir}), "hook", "end")
	quiet(t, "hook end", code, stdout, stderr)

	if turns := loggedTurns(t, repo.Dir); len(turns) != 1 || turns[0].changes() != "M a.txt" {
		t.Errorf("turns = %+v, want one turn changing a.txt", turns)
	}
}

func TestHookPrefersTheProjectNamedInTheInput(t *testing.T) {
	project := committedRepo(t)
	// The hook may run in another repository, such as a home folder kept in
	// git; the turn belongs to the project the agent works on.
	other := testutil.NewRepo(t)
	input := func() io.Reader { return jsonInput(t, map[string]any{"cwd": project.Dir}) }
	code, stdout, stderr := runWithInput(t, other.Dir, input(), "hook", "start")
	quiet(t, "hook start", code, stdout, stderr)
	project.Write("a.txt", "2\n")
	code, stdout, stderr = runWithInput(t, other.Dir, input(), "hook", "end")
	quiet(t, "hook end", code, stdout, stderr)

	if turns := loggedTurns(t, project.Dir); len(turns) != 1 {
		t.Errorf("project turns = %+v, want 1", turns)
	}
	if other.Exists(".turnback") {
		t.Error("the hook recorded in the folder it ran in, not in the project")
	}
}

func TestHookReadsInputThatStartsWithAByteOrderMark(t *testing.T) {
	repo := committedRepo(t)
	settings := notARepository(t)
	input := func() io.Reader {
		b, err := json.Marshal(map[string]any{"workspace_roots": []string{repo.Dir}})
		if err != nil {
			t.Fatal(err)
		}
		return bytes.NewReader(append([]byte{0xEF, 0xBB, 0xBF}, b...))
	}
	code, stdout, stderr := runWithInput(t, settings, input(), "hook", "start")
	quiet(t, "hook start", code, stdout, stderr)
	repo.Write("a.txt", "2\n")
	code, stdout, stderr = runWithInput(t, settings, input(), "hook", "end")
	quiet(t, "hook end", code, stdout, stderr)

	if turns := loggedTurns(t, repo.Dir); len(turns) != 1 {
		t.Errorf("turns = %+v, want 1", turns)
	}
}

func TestDrivePathsWrittenLikeURLsLoseTheirLeadingSlash(t *testing.T) {
	for in, want := range map[string]string{
		"/C:/Users/me/project": "C:/Users/me/project",
		"/d:/work":             "d:/work",
		"/C:":                  "C:",
		"C:/Users/me":          "C:/Users/me",
		"/home/me/project":     "/home/me/project",
		"//server/share":       "//server/share",
		"/1:/x":                "/1:/x",
	} {
		if got := withoutURLSlash(in); got != want {
			t.Errorf("withoutURLSlash(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHookReadsWindowsPathsWrittenLikeURLs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive letters exist on Windows only")
	}
	repo := committedRepo(t)
	settings := notARepository(t)
	input := func() io.Reader {
		return jsonInput(t, map[string]any{"workspace_roots": []string{"/" + filepath.ToSlash(repo.Dir)}})
	}
	code, stdout, stderr := runWithInput(t, settings, input(), "hook", "start")
	quiet(t, "hook start", code, stdout, stderr)
	repo.Write("a.txt", "2\n")
	code, stdout, stderr = runWithInput(t, settings, input(), "hook", "end")
	quiet(t, "hook end", code, stdout, stderr)

	if turns := loggedTurns(t, repo.Dir); len(turns) != 1 {
		t.Errorf("turns = %+v, want 1", turns)
	}
}

func TestHookDoesNotRecordElsewhereWhenTheNamedProjectIsNoRepository(t *testing.T) {
	// The agent works in a folder outside git; the hook runs in a
	// repository that happens to enclose the agent's settings folder.
	notes := notARepository(t)
	home := testutil.NewRepo(t)
	code, stdout, stderr := runWithInput(t, home.Dir, jsonInput(t, map[string]any{"cwd": notes}), "hook", "start")
	oneLine(t, "hook start", code, stdout, stderr, "not inside a git repository")
	if home.Exists(".turnback") {
		t.Error("the hook recorded in the repository it ran in, not in the project the agent named")
	}
}

func TestHookIgnoresInputThatIsNotJSON(t *testing.T) {
	repo := committedRepo(t)
	code, stdout, stderr := runWithInput(t, repo.Dir, strings.NewReader("{not json"), "hook", "start")
	quiet(t, "hook start", code, stdout, stderr)
	repo.Write("a.txt", "2\n")
	code, stdout, stderr = runWithInput(t, repo.Dir, strings.NewReader(`["cwd", "/"`), "hook", "end")
	quiet(t, "hook end", code, stdout, stderr)

	if turns := loggedTurns(t, repo.Dir); len(turns) != 1 || turns[0].changes() != "M a.txt" {
		t.Errorf("turns = %+v, want one turn changing a.txt", turns)
	}
}

func TestHookOutsideARepositorySaysSoInOneLine(t *testing.T) {
	dir := notARepository(t)
	code, stdout, stderr := runWithInput(t, dir, strings.NewReader(`{}`), "hook", "start")
	oneLine(t, "hook start", code, stdout, stderr, "not inside a git repository")
	// No turn can be open outside a repository, so end has nothing to say.
	code, stdout, stderr = runWithInput(t, dir, strings.NewReader(`{}`), "hook", "end")
	quiet(t, "hook end", code, stdout, stderr)
}

func TestHookKeepsTheTurnWhenItsFolderIsNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs Unix permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	repo := committedRepo(t)
	runWithInput(t, repo.Dir, strings.NewReader(""), "hook", "start", "--agent", "gemini-cli")
	repo.Write("a.txt", "2\n")

	folder := repo.Path(".turnback")
	if err := os.Chmod(folder, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(folder, 0o755) })
	code, stdout, stderr := runWithInput(t, repo.Dir, strings.NewReader(""), "hook", "end")
	oneLine(t, "hook end", code, stdout, stderr, "permission denied")
	code, stdout, stderr = runWithInput(t, repo.Dir, strings.NewReader(""), "hook", "start")
	oneLine(t, "hook start", code, stdout, stderr, "permission denied")

	// Once the folder is writable again, the turn is still there to record.
	if err := os.Chmod(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithInput(t, repo.Dir, strings.NewReader(""), "hook", "end")
	quiet(t, "hook end", code, stdout, stderr)
	if turns := loggedTurns(t, repo.Dir); len(turns) != 1 || turns[0].Agent != "gemini-cli" || turns[0].changes() != "M a.txt" {
		t.Errorf("turns = %+v, want the turn by gemini-cli changing a.txt", turns)
	}
}

func TestHookReportsABadOptionWithoutFailing(t *testing.T) {
	repo := testutil.NewRepo(t)
	code, stdout, stderr := runWithInput(t, repo.Dir, strings.NewReader(""), "hook", "start", "--bogus")
	oneLine(t, "hook start --bogus", code, stdout, stderr, "unknown option --bogus")
	code, stdout, stderr = runWithInput(t, repo.Dir, strings.NewReader(""), "hook", "end", "extra")
	oneLine(t, "hook end extra", code, stdout, stderr, `unexpected argument "extra"`)
	if repo.Exists(".turnback") {
		t.Error("a hook with a bad command line started recording")
	}
}

func TestHookHelpPrintsTheUsage(t *testing.T) {
	code, stdout, stderr := runWithInput(t, t.TempDir(), strings.NewReader(""), "hook", "start", "--agent", "cursor", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "turnback hook start") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// within runs f and fails the test if it takes longer than limit.
func within(t *testing.T, limit time.Duration, what string, f func() (int, string, string)) (int, string, string) {
	t.Helper()
	type result struct {
		code           int
		stdout, stderr string
	}
	done := make(chan result, 1)
	go func() {
		code, stdout, stderr := f()
		done <- result{code, stdout, stderr}
	}()
	select {
	case r := <-done:
		return r.code, r.stdout, r.stderr
	case <-time.After(limit):
		t.Fatalf("%s was still running after %v", what, limit)
		return 0, "", ""
	}
}

func TestHookDoesNotWaitLongForInputThatNeverComes(t *testing.T) {
	saved := payloadWait
	payloadWait = 100 * time.Millisecond
	defer func() { payloadWait = saved }()
	repo := committedRepo(t)
	// An agent that keeps standard input open and sends nothing.
	silent, w := io.Pipe()
	defer w.Close()

	code, stdout, stderr := within(t, 10*time.Second, "hook start", func() (int, string, string) {
		return runWithInput(t, repo.Dir, silent, "hook", "start")
	})
	quiet(t, "hook start", code, stdout, stderr)
	if _, status, _ := run(t, repo.Dir, "status"); !strings.Contains(status, "Recording a turn") {
		t.Errorf("status = %q, want a turn being recorded", status)
	}
}

func TestHookUsesItsInputWithoutWaitingForItToClose(t *testing.T) {
	saved := payloadWait
	payloadWait = time.Minute
	defer func() { payloadWait = saved }()
	repo := committedRepo(t)
	settings := notARepository(t)
	// An agent that sends its JSON and keeps standard input open.
	open, w := io.Pipe()
	defer w.Close()
	b, err := json.Marshal(map[string]any{"cwd": repo.Dir})
	if err != nil {
		t.Fatal(err)
	}
	input := io.MultiReader(bytes.NewReader(b), open)

	code, stdout, stderr := within(t, 10*time.Second, "hook start", func() (int, string, string) {
		return runWithInput(t, settings, input, "hook", "start")
	})
	quiet(t, "hook start", code, stdout, stderr)
	if _, status, _ := run(t, repo.Dir, "status"); !strings.Contains(status, "Recording a turn") {
		t.Errorf("status = %q, want a turn being recorded", status)
	}
}

func TestHookDoesNotReadATerminal(t *testing.T) {
	saved := payloadWait
	payloadWait = time.Minute
	defer func() { payloadWait = saved }()
	repo := committedRepo(t)
	terminal, w := io.Pipe()
	defer w.Close()

	code, stdout, stderr := within(t, 10*time.Second, "hook start", func() (int, string, string) {
		var stdout, stderr bytes.Buffer
		env := &Env{Stdin: terminal, Stdout: &stdout, Stderr: &stderr, Dir: repo.Dir, Now: time.Now, Interactive: true}
		return Run(env, []string{"hook", "start"}), stdout.String(), stderr.String()
	})
	quiet(t, "hook start", code, stdout, stderr)
}
