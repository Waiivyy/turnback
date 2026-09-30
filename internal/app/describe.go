package app

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Waiivyy/turnback/internal/store"
)

// maxDescription is the longest description Describe writes; anything
// longer falls back to Summarize.
const maxDescription = 72

// Describe summarizes a turn from its diff: the functions, types and other
// declarations it added, changed or removed in the files it modified, plus
// the files it added, deleted or renamed. Declarations are recognized in
// Go, JavaScript and TypeScript, Python, Rust and Ruby, and git's hunk
// headers name the function around each change. When nothing better can be
// said in a short sentence, it falls back to Summarize.
func Describe(files []store.File, patch string) string {
	byPath := make(map[string]store.File, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}
	name := fileNamer(files)
	var add, update, remove, rename, untrack words
	for _, f := range files {
		switch {
		case f.Ignored:
			untrack.push(name(f.Path))
		case f.Status == "A":
			add.push(name(f.Path))
		case f.Status == "D":
			remove.push(name(f.Path))
		case f.Status == "R":
			rename.push(f.OldPath + " to " + f.Path)
		}
	}
	withSymbols := make(map[string]bool)
	for _, sec := range splitPatch(patch) {
		f, ok := byPath[sec.path]
		lang := languageFor(sec.path)
		if !ok || (f.Status != "M" && f.Status != "T") || lang == nil {
			continue
		}
		added, removed, changed := lang.changes(sec)
		for _, n := range added {
			add.push(n)
		}
		for _, n := range changed {
			update.push(n)
		}
		for _, n := range removed {
			remove.push(n)
		}
		if len(added)+len(removed)+len(changed) > 0 {
			withSymbols[f.Path] = true
		}
	}
	for _, f := range files {
		if (f.Status == "M" || f.Status == "T") && !f.Ignored && !withSymbols[f.Path] {
			update.push(name(f.Path))
		}
	}

	var clauses []string
	for _, c := range []struct {
		verb  string
		words words
	}{{"add", add}, {"update", update}, {"remove", remove}, {"rename", rename}, {"stop tracking", untrack}} {
		if len(c.words.list) > 0 {
			clauses = append(clauses, c.verb+" "+c.words.phrase())
		}
	}
	if len(clauses) == 0 {
		return Summarize(files)
	}
	sentence := strings.Join(clauses, "; ")
	r, size := utf8.DecodeRuneInString(sentence)
	sentence = string(unicode.ToUpper(r)) + sentence[size:]
	if utf8.RuneCountInString(sentence) > maxDescription {
		return Summarize(files)
	}
	return sentence
}

// words is an ordered list without repeats.
type words struct {
	list []string
	seen map[string]bool
}

func (w *words) push(s string) {
	if w.seen == nil {
		w.seen = make(map[string]bool)
	}
	if s != "" && !w.seen[s] {
		w.seen[s] = true
		w.list = append(w.list, s)
	}
}

// phrase joins up to three words, counting the rest.
func (w *words) phrase() string {
	if len(w.list) > 3 {
		return fmt.Sprintf("%s and %d more", strings.Join(w.list[:3], ", "), len(w.list)-3)
	}
	return joinAnd(w.list)
}

// fileNamer names files by their base name, or by their full path when
// two files of the turn share a base name.
func fileNamer(files []store.File) func(string) string {
	seen := make(map[string]int)
	for _, f := range files {
		seen[path.Base(f.Path)]++
	}
	return func(p string) string {
		if seen[path.Base(p)] > 1 {
			return p
		}
		return path.Base(p)
	}
}

// patchSection is one file's part of a unified diff.
type patchSection struct {
	path  string
	lines []patchLine // hunk headers and hunk lines, in reading order
}

type patchLine struct {
	kind byte   // '@' hunk header context, ' ' unchanged, '+' added, '-' removed
	text string // without the leading marker
}

// splitPatch splits a unified diff by file.
func splitPatch(patch string) []patchSection {
	var out []patchSection
	var cur *patchSection
	inHunk := false
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			out = append(out, patchSection{})
			cur, inHunk = &out[len(out)-1], false
		case cur == nil:
		case strings.HasPrefix(line, "@@"):
			inHunk = true
			if end := strings.Index(line[2:], "@@"); end >= 0 {
				if ctx := strings.TrimSpace(line[end+4:]); ctx != "" {
					cur.lines = append(cur.lines, patchLine{'@', ctx})
				}
			}
		case !inHunk && strings.HasPrefix(line, "+++ b/"):
			cur.path = cleanPatchPath(line[len("+++ b/"):])
		case !inHunk && strings.HasPrefix(line, "--- a/") && cur.path == "":
			cur.path = cleanPatchPath(line[len("--- a/"):])
		case inHunk && line != "" && strings.ContainsRune(" +-", rune(line[0])):
			cur.lines = append(cur.lines, patchLine{line[0], line[1:]})
		}
	}
	return out
}

// cleanPatchPath drops the tab git appends after names with spaces.
func cleanPatchPath(p string) string {
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	return p
}

// language recognizes declarations in one family of file types. Each
// pattern captures a declared name.
type language struct {
	blocks []*regexp.Regexp // declarations that open a body: functions, types, classes
	values []*regexp.Regexp // declarations of plain values: constants and variables
}

// declared returns the names line declares, and whether it opens a body.
func (l *language) declared(line string) (names []string, block bool) {
	for _, re := range l.blocks {
		if m := re.FindStringSubmatch(line); m != nil {
			names, block = append(names, m[1]), true
		}
	}
	if block {
		return names, true
	}
	for _, re := range l.values {
		if m := re.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names, false
}

// changes returns the names a section adds, removes and changes, in
// reading order. A name declared only on added lines is added, only on
// removed lines removed. It is changed when it is declared on both sides,
// named in a hunk header, or when it opens the body a changed line sits
// in. An unindented line that declares nothing ends that body.
func (l *language) changes(sec patchSection) (added, removed, changed []string) {
	plus, minus, touched := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	var order []string
	seen := make(map[string]bool)
	current := "" // the body the reader is in
	for _, line := range sec.lines {
		names, block := l.declared(line.text)
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				order = append(order, n)
			}
			switch line.kind {
			case '+':
				plus[n] = true
			case '-':
				minus[n] = true
			case '@':
				touched[n] = true
			}
		}
		switch {
		case block:
			current = names[len(names)-1]
		case len(names) > 0:
			current = "" // a value declaration sits outside any body
		default:
			if (line.kind == '+' || line.kind == '-') && current != "" {
				touched[current] = true
			}
			if r, _ := utf8.DecodeRuneInString(line.text); line.text != "" && !unicode.IsSpace(r) {
				current = ""
			}
		}
	}
	for _, n := range order {
		switch {
		case plus[n] && !minus[n]:
			added = append(added, n)
		case minus[n] && !plus[n]:
			removed = append(removed, n)
		case plus[n] || touched[n]:
			changed = append(changed, n)
		}
	}
	return added, removed, changed
}

var languages = map[string]*language{}

func init() {
	golang := &language{
		blocks: compile(
			`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*[\[(]`,
			`^type\s+([A-Za-z_]\w*)\s+(?:struct|interface)\b`,
		),
		values: compile(
			`^type\s+([A-Za-z_]\w*)\s`,
			`^(?:var|const)\s+([A-Za-z_]\w*)\s*(?:[A-Za-z_*\[\]]+\s*)?=`,
		),
	}
	js := &language{
		blocks: compile(
			`^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)\s*[(<]`,
			`^(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`,
			`^(?:export\s+)?(?:declare\s+)?(?:interface|enum)\s+([A-Za-z_$][\w$]*)`,
			`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*(?:async\s+)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*(?::[^=]+)?=>`,
		),
		values: compile(
			`^(?:export\s+)?(?:declare\s+)?type\s+([A-Za-z_$][\w$]*)`,
			`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*[:=]`,
		),
	}
	python := &language{blocks: compile(
		`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`,
		`^\s*class\s+([A-Za-z_]\w*)`,
	)}
	rust := &language{blocks: compile(
		`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:unsafe\s+)?fn\s+([A-Za-z_]\w*)`,
		`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait)\s+([A-Za-z_]\w*)`,
	)}
	ruby := &language{blocks: compile(
		`^\s*def\s+(?:self\.)?([A-Za-z_]\w*[?!=]?)`,
		`^\s*(?:class|module)\s+([A-Z]\w*)`,
	)}
	for ext, lang := range map[string]*language{
		".go": golang,
		".js": js, ".jsx": js, ".mjs": js, ".cjs": js, ".ts": js, ".tsx": js, ".mts": js, ".cts": js,
		".py": python, ".rs": rust, ".rb": ruby,
	} {
		languages[ext] = lang
	}
}

func compile(patterns ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		out[i] = regexp.MustCompile(p)
	}
	return out
}

func languageFor(p string) *language {
	return languages[strings.ToLower(path.Ext(p))]
}
