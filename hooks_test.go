package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/Waiivyy/turnback/internal/testutil"
)

// The hook settings in examples/hooks are what people copy into their
// agents, so they are tested the way the agents run them: each command is
// fed the JSON input the agent documents (testdata/hooks), on Unix through
// sh as the agents do, and must record turns without ever failing the agent.

// hookTurn is one way an agent's settings start and end a turn.
type hookTurn struct {
	start, end           []any  // JSON paths of the two commands in the settings file
	winStart, winEnd     []any  // the commands for Windows, where they differ
	startInput, endInput string // the agent's documented input, in testdata/hooks/<agent>
}

// commands returns the turn's start and end commands for this system.
func (s hookSetting) commands(t *testing.T, turn hookTurn) (start, end string) {
	t.Helper()
	startPath, endPath := turn.start, turn.end
	if runtime.GOOS == "windows" && turn.winStart != nil {
		startPath, endPath = turn.winStart, turn.winEnd
	}
	return s.command(t, startPath), s.command(t, endPath)
}

type hookSetting struct {
	agent     string // folder in examples/hooks and testdata/hooks
	file      string // the settings file
	label     string // what the settings pass to --agent
	elsewhere bool   // the agent runs user-level hooks outside the project
	turns     []hookTurn
}

var hookSettings = []hookSetting{
	{
		agent: "cursor", file: "hooks.json", label: "cursor", elsewhere: true,
		turns: []hookTurn{{
			start: []any{"hooks", "beforeSubmitPrompt", 0, "command"}, startInput: "beforeSubmitPrompt.json",
			end: []any{"hooks", "stop", 0, "command"}, endInput: "stop.json",
		}},
	},
	{
		agent: "copilot", file: "turnback.json", label: "copilot",
		turns: []hookTurn{{
			start: []any{"hooks", "userPromptSubmitted", 0, "bash"}, startInput: "userPromptSubmitted.json",
			end: []any{"hooks", "agentStop", 0, "bash"}, endInput: "agentStop.json",
			winStart: []any{"hooks", "userPromptSubmitted", 0, "powershell"},
			winEnd:   []any{"hooks", "agentStop", 0, "powershell"},
		}, {
			// VS Code runs the same file and passes its own input.
			start: []any{"hooks", "userPromptSubmitted", 0, "bash"}, startInput: "vscode-UserPromptSubmit.json",
			end: []any{"hooks", "agentStop", 0, "bash"}, endInput: "vscode-Stop.json",
			winStart: []any{"hooks", "userPromptSubmitted", 0, "powershell"},
			winEnd:   []any{"hooks", "agentStop", 0, "powershell"},
		}},
	},
	{
		agent: "codex", file: "hooks.json", label: "codex",
		turns: []hookTurn{{
			start: []any{"hooks", "UserPromptSubmit", 0, "hooks", 0, "command"}, startInput: "UserPromptSubmit.json",
			end: []any{"hooks", "Stop", 0, "hooks", 0, "command"}, endInput: "Stop.json",
			winStart: []any{"hooks", "UserPromptSubmit", 0, "hooks", 0, "commandWindows"},
			winEnd:   []any{"hooks", "Stop", 0, "hooks", 0, "commandWindows"},
		}, {
			// An interrupted turn ends with Interrupt.
			start: []any{"hooks", "UserPromptSubmit", 0, "hooks", 0, "command"}, startInput: "UserPromptSubmit.json",
			end: []any{"hooks", "Interrupt", 0, "hooks", 0, "command"}, endInput: "Interrupt.json",
			winStart: []any{"hooks", "UserPromptSubmit", 0, "hooks", 0, "commandWindows"},
			winEnd:   []any{"hooks", "Interrupt", 0, "hooks", 0, "commandWindows"},
		}},
	},
	{
		agent: "gemini-cli", file: "settings.json", label: "gemini-cli",
		turns: []hookTurn{{
			start: []any{"hooks", "BeforeAgent", 0, "hooks", 0, "command"}, startInput: "BeforeAgent.json",
			end: []any{"hooks", "AfterAgent", 0, "hooks", 0, "command"}, endInput: "AfterAgent.json",
		}},
	},
}

// loadJSON parses a JSON file.
func loadJSON(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	return v
}

// lookup follows a path of object keys and array indexes through v.
func lookup(v any, path []any) (any, bool) {
	for _, step := range path {
		switch s := step.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			if v, ok = m[s]; !ok {
				return nil, false
			}
		case int:
			a, ok := v.([]any)
			if !ok || s >= len(a) {
				return nil, false
			}
			v = a[s]
		}
	}
	return v, true
}

func (s hookSetting) command(t *testing.T, path []any) string {
	t.Helper()
	v, ok := lookup(loadJSON(t, filepath.Join("examples", "hooks", s.agent, s.file)), path)
	cmd, isString := v.(string)
	if !ok || !isString {
		t.Fatalf("examples/hooks/%s/%s has no command at %v", s.agent, s.file, path)
	}
	return cmd
}

// input returns one of the agent's documented hook inputs, with the
// placeholder project folder replaced by dir.
func (s hookSetting) input(t *testing.T, name, dir string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "hooks", s.agent, name))
	if err != nil {
		t.Fatal(err)
	}
	quoted, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(b, []byte(`"/path/to/project"`), quoted)
}

// strings returns every string in a parsed JSON value.
func jsonStrings(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			out = append(out, jsonStrings(e)...)
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			out = append(out, jsonStrings(v[k])...)
		}
		return out
	}
	return nil
}

// turnbackCalls returns the arguments of every turnback command in a shell
// command line. It knows the shell used in this repository's settings and
// docs: quotes, backslashes, comments, operators and redirections. Command
// substitution is refused rather than misread.
func turnbackCalls(line string) ([][]string, error) {
	var (
		calls    [][]string
		words    []string
		word     strings.Builder
		inWord   bool
		skipNext bool // the next word is the target of a redirection
	)
	endWord := func() {
		if !inWord {
			return
		}
		if skipNext {
			skipNext = false
		} else {
			words = append(words, word.String())
		}
		word.Reset()
		inWord = false
	}
	endCommand := func() {
		endWord()
		for len(words) > 0 && strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-") {
			words = words[1:] // variable assignments before the command
		}
		if len(words) > 0 && words[0] == "turnback" {
			calls = append(calls, words[1:])
		}
		words = nil
	}
	r := []rune(line)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\'':
			inWord = true
			j := i + 1
			for j < len(r) && r[j] != '\'' {
				word.WriteRune(r[j])
				j++
			}
			if j == len(r) {
				return nil, fmt.Errorf("unterminated quote in %q", line)
			}
			i = j
		case c == '"':
			inWord = true
			j := i + 1
			for ; j < len(r) && r[j] != '"'; j++ {
				if r[j] == '$' && j+1 < len(r) && r[j+1] == '(' || r[j] == '`' {
					return nil, fmt.Errorf("command substitution in %q", line)
				}
				if r[j] == '\\' && j+1 < len(r) && strings.ContainsRune(`"\$`+"`", r[j+1]) {
					j++
				}
				word.WriteRune(r[j])
			}
			if j == len(r) {
				return nil, fmt.Errorf("unterminated quote in %q", line)
			}
			i = j
		case c == '\\' && i+1 < len(r):
			inWord = true
			i++
			word.WriteRune(r[i])
		case c == '`' || c == '$' && i+1 < len(r) && r[i+1] == '(':
			return nil, fmt.Errorf("command substitution in %q", line)
		case c == '#' && !inWord:
			endCommand()
			return calls, nil
		case unicode.IsSpace(c):
			endWord()
		case c == '<' || c == '>':
			if inWord && strings.Trim(word.String(), "0123456789") == "" {
				word.Reset() // a file descriptor, as in 2>
				inWord = false
			}
			endWord()
			for i+1 < len(r) && (r[i+1] == '>' || r[i+1] == '&') {
				i++
			}
			skipNext = true
		case strings.ContainsRune(";&|()", c):
			endCommand()
		default:
			inWord = true
			word.WriteRune(c)
		}
	}
	endCommand()
	return calls, nil
}

func TestTurnbackCallsReadsShellLikeTheShell(t *testing.T) {
	for _, tc := range []struct {
		line string
		want string
	}{
		{"turnback hook start --agent cursor || exit 0", "[[hook start --agent cursor]]"},
		{"turnback hook end; exit 0", "[[hook end]]"},
		{`turnback start -m "Add rate limiting"   # right before you prompt`, "[[start -m Add rate limiting]]"},
		{"git add -A && git commit -q -m 'turnback undo 1'", "[]"},
		{"cd turnback && TURNBACK_X=1 turnback log --since 2h 2>/dev/null | less", "[[log --since 2h]]"},
		{"curl -fsSL https://example.com/install.sh | sh", "[]"},
	} {
		calls, err := turnbackCalls(tc.line)
		if err != nil {
			t.Errorf("%q: %v", tc.line, err)
			continue
		}
		if got := fmt.Sprint(calls); got != tc.want {
			t.Errorf("%q: calls = %s, want %s", tc.line, got, tc.want)
		}
	}
	if _, err := turnbackCalls("turnback show $(cat id)"); err == nil {
		t.Error("command substitution was not refused")
	}
}

// checkCall fails the test unless the real command line accepts args: the
// command exists and knows every option. Asking for help after the options
// checks them all without running the command.
func checkCall(t *testing.T, where string, args []string) {
	t.Helper()
	for _, a := range args {
		if strings.HasPrefix(a, "<") && strings.HasSuffix(a, ">") {
			t.Errorf("%s: turnback %s: use a real value instead of %s", where, strings.Join(args, " "), a)
			return
		}
	}
	cmd := exec.Command(binary, append(append([]string{}, args...), "--help")...)
	cmd.Dir = t.TempDir()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil || stderr.Len() > 0 {
		t.Errorf("%s: turnback %s is not a valid command line: %v %s", where, strings.Join(args, " "), err, stderr.String())
	}
}

func TestEveryHookSettingsFileIsCoveredAndParses(t *testing.T) {
	covered := map[string]bool{}
	for _, s := range hookSettings {
		covered[filepath.Join("examples", "hooks", s.agent, s.file)] = true
	}
	found := 0
	err := filepath.WalkDir(filepath.Join("examples", "hooks"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		found++
		if filepath.Ext(path) != ".json" {
			t.Errorf("%s: only JSON settings are checked; teach this test the new format", path)
			return nil
		}
		loadJSON(t, path)
		if !covered[path] {
			t.Errorf("%s is not in hookSettings, so no test runs it", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found != len(hookSettings) {
		t.Errorf("found %d settings files, hookSettings lists %d", found, len(hookSettings))
	}
}

func TestHookSettingsOnlyUseRealCommands(t *testing.T) {
	for _, s := range hookSettings {
		path := filepath.Join("examples", "hooks", s.agent, s.file)
		n := 0
		for _, str := range jsonStrings(loadJSON(t, path)) {
			calls, err := turnbackCalls(str)
			if err != nil {
				t.Errorf("%s: %v", path, err)
			}
			for _, args := range calls {
				n++
				checkCall(t, path, args)
			}
		}
		if n == 0 {
			t.Errorf("%s runs no turnback command", path)
		}
	}
}

// hookShell runs a hook command the way agents on Unix do, through sh, in
// dir, with input on stdin and path as PATH.
func hookShell(t *testing.T, command, dir, path string, input []byte) (code int, stdout, stderr string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not installed")
	}
	cmd := exec.Command(sh, "-c", command)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+path)
	cmd.Stdin = bytes.NewReader(input)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return code, out.String(), errOut.String()
}

// runHookCommand runs a hook command with the built turnback: through sh on
// Unix, and on Windows, where agents use other shells, by calling turnback
// with the command's arguments directly.
func runHookCommand(t *testing.T, command, dir string, input []byte) (int, string, string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return hookShell(t, command, dir, filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"), input)
	}
	calls, err := turnbackCalls(command)
	if err != nil || len(calls) != 1 {
		t.Fatalf("%q: want one turnback command, got %v (%v)", command, calls, err)
	}
	cmd := exec.Command(binary, calls[0]...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(input)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return code, out.String(), errOut.String()
}

func mustBeQuiet(t *testing.T, what string, code int, stdout, stderr string) {
	t.Helper()
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("%s: exit %d, stdout %q, stderr %q; want exit 0 and no output", what, code, stdout, stderr)
	}
}

type turnInLog struct {
	Agent string `json:"agent"`
	Files []struct {
		Path string `json:"path"`
	} `json:"files"`
}

func turnsIn(t *testing.T, dir string) []turnInLog {
	t.Helper()
	var turns []turnInLog
	if err := json.Unmarshal([]byte(turnback(t, dir, "log", "--json")), &turns); err != nil {
		t.Fatal(err)
	}
	return turns
}

// hookDirs returns a new project with one commit, and the folder the agent
// runs its hooks in.
func hookDirs(t *testing.T, s hookSetting) (*testutil.Repo, string) {
	t.Helper()
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "1\n")
	repo.Commit("initial")
	if !s.elsewhere {
		return repo, repo.Dir
	}
	settings := testutil.TempDir(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(settings))
	return repo, settings
}

func TestHookSettingsRecordTurnsFromTheAgentsInput(t *testing.T) {
	for _, s := range hookSettings {
		for i, turn := range s.turns {
			t.Run(fmt.Sprintf("%s/%d", s.agent, i), func(t *testing.T) {
				repo, dir := hookDirs(t, s)
				start, end := s.commands(t, turn)
				startInput, endInput := s.input(t, turn.startInput, repo.Dir), s.input(t, turn.endInput, repo.Dir)

				code, stdout, stderr := runHookCommand(t, start, dir, startInput)
				mustBeQuiet(t, "start", code, stdout, stderr)
				repo.Write("a.txt", "2\n")
				code, stdout, stderr = runHookCommand(t, end, dir, endInput)
				mustBeQuiet(t, "end", code, stdout, stderr)

				turns := turnsIn(t, repo.Dir)
				if len(turns) != 1 || turns[0].Agent != s.label || len(turns[0].Files) != 1 || turns[0].Files[0].Path != "a.txt" {
					t.Fatalf("turns = %+v, want one turn by %s changing a.txt", turns, s.label)
				}
				if status := turnback(t, repo.Dir, "status"); !strings.Contains(status, "Not recording") {
					t.Errorf("a turn is still being recorded after the end hook:\n%s", status)
				}
			})
		}
	}
}

func TestHookSettingsKeepInterruptedTurnsApart(t *testing.T) {
	for _, s := range hookSettings {
		t.Run(s.agent, func(t *testing.T) {
			repo, dir := hookDirs(t, s)
			turn := s.turns[0]
			start, end := s.commands(t, turn)
			startInput, endInput := s.input(t, turn.startInput, repo.Dir), s.input(t, turn.endInput, repo.Dir)

			// An end without a start, as after installing the settings
			// in the middle of a session, does nothing.
			code, stdout, stderr := runHookCommand(t, end, dir, endInput)
			mustBeQuiet(t, "end without start", code, stdout, stderr)

			runHookCommand(t, start, dir, startInput)
			repo.Write("a.txt", "2\n")
			// The agent is stopped before its end hook runs; the next
			// prompt starts a new turn.
			code, stdout, stderr = runHookCommand(t, start, dir, startInput)
			mustBeQuiet(t, "second start", code, stdout, stderr)
			repo.Write("b.txt", "new\n")
			runHookCommand(t, end, dir, endInput)

			turns := turnsIn(t, repo.Dir)
			if len(turns) != 2 || len(turns[0].Files) != 1 || turns[0].Files[0].Path != "b.txt" ||
				len(turns[1].Files) != 1 || turns[1].Files[0].Path != "a.txt" {
				t.Fatalf("turns = %+v, want a.txt in turn 1 and b.txt in turn 2", turns)
			}
		})
	}
}

func TestHookSettingsNeverFailTheAgentWhenTurnbackIsMissingOrOld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the settings guard their commands with sh syntax on Unix")
	}
	// An older turnback has no "hook start" and fails with status 2, which
	// most agents read as "block this prompt" or "keep going".
	old := t.TempDir()
	fake := "#!/bin/sh\necho 'turnback: unknown command \"hook\"' >&2\nexit 2\n"
	if err := os.WriteFile(filepath.Join(old, "turnback"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := t.TempDir()
	for _, s := range hookSettings {
		repo, dir := hookDirs(t, s)
		for _, turn := range s.turns {
			for _, step := range []struct {
				path  []any
				input string
			}{{turn.start, turn.startInput}, {turn.end, turn.endInput}} {
				command, input := s.command(t, step.path), s.input(t, step.input, repo.Dir)
				for name, path := range map[string]string{"an old turnback": old, "no turnback": missing} {
					code, stdout, _ := hookShell(t, command, dir, path, input)
					if code != 0 || stdout != "" {
						t.Errorf("%s with %s: %q exits %d, stdout %q; want exit 0 and no stdout", s.agent, name, command, code, stdout)
					}
				}
			}
		}
	}
}

// codeBlock is a fenced code block in a markdown file.
type codeBlock struct {
	lang string
	text string
	line int // line of the opening fence
}

func codeBlocks(t *testing.T, path string) []codeBlock {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var (
		blocks []codeBlock
		open   *codeBlock
		body   []string
	)
	for i, line := range strings.Split(string(b), "\n") {
		fence := strings.TrimSpace(line)
		if open == nil {
			if strings.HasPrefix(fence, "```") {
				lang, _, _ := strings.Cut(strings.TrimSpace(strings.TrimLeft(fence, "`")), " ")
				open, body = &codeBlock{lang: lang, line: i + 1}, nil
			}
			continue
		}
		if fence == "```" {
			open.text = strings.Join(body, "\n")
			blocks = append(blocks, *open)
			open = nil
			continue
		}
		body = append(body, line)
	}
	if open != nil {
		t.Fatalf("%s:%d: the code block is never closed", path, open.line)
	}
	return blocks
}

// blockCommands returns the shell command lines in a code block: every line
// of a shell block, the lines after "$ " in a console block, and every
// string in a JSON block, such as the commands in hook settings.
func blockCommands(t *testing.T, where string, b codeBlock) []string {
	t.Helper()
	switch b.lang {
	case "bash", "sh", "shell", "powershell":
		return strings.Split(strings.ReplaceAll(b.text, "\\\n", " "), "\n")
	case "console":
		var out []string
		for _, line := range strings.Split(b.text, "\n") {
			if command, ok := strings.CutPrefix(line, "$ "); ok {
				out = append(out, command)
			}
		}
		return out
	case "json":
		var v any
		if err := json.Unmarshal([]byte(b.text), &v); err != nil {
			t.Errorf("%s: the JSON block is not valid: %v", where, err)
			return nil
		}
		return jsonStrings(v)
	}
	return nil
}

func docs(t *testing.T) []string {
	t.Helper()
	files := []string{"README.md"}
	for _, pattern := range []string{"docs/*.md", "docs/integrations/*.md"} {
		matches, err := filepath.Glob(filepath.FromSlash(pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	return files
}

func TestDocsOnlyShowRealCommands(t *testing.T) {
	checked := 0
	for _, doc := range docs(t) {
		for _, b := range codeBlocks(t, doc) {
			where := fmt.Sprintf("%s:%d", doc, b.line)
			for _, command := range blockCommands(t, where, b) {
				calls, err := turnbackCalls(command)
				if err != nil {
					t.Errorf("%s: %v", where, err)
				}
				for _, args := range calls {
					checked++
					checkCall(t, where, args)
				}
			}
		}
	}
	if checked == 0 {
		t.Error("found no turnback commands in the docs")
	}
}

func TestIntegrationDocsShowTheTestedSettings(t *testing.T) {
	for _, s := range hookSettings {
		doc := filepath.Join("docs", "integrations", s.agent+".md")
		want := loadJSON(t, filepath.Join("examples", "hooks", s.agent, s.file))
		shown := false
		for _, b := range codeBlocks(t, doc) {
			var v any
			if b.lang == "json" && json.Unmarshal([]byte(b.text), &v) == nil && reflect.DeepEqual(v, want) {
				shown = true
			}
		}
		if !shown {
			t.Errorf("%s does not show the settings in examples/hooks/%s/%s", doc, s.agent, s.file)
		}
	}
}
