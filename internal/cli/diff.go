package cli

import (
	"fmt"
	"strings"
)

// writeDiff prints a unified diff, colored when color is on. Inside a hunk a
// line is classified only by its first character, so a removed line that
// happens to start with "--" is still shown as a removal.
func writeDiff(env *Env, patch string) {
	if !env.Color {
		fmt.Fprint(env.Stdout, patch)
		return
	}
	inHunk := false
	for _, line := range strings.SplitAfter(patch, "\n") {
		if line == "" {
			continue
		}
		text := strings.TrimSuffix(line, "\n")
		var out string
		switch {
		case strings.HasPrefix(text, "diff --git "):
			inHunk = false
			out = env.paint(bold, text)
		case strings.HasPrefix(text, "@@"):
			inHunk = true
			out = env.paint(cyan, text)
			if end := strings.Index(text[2:], "@@"); end >= 0 {
				end += 4
				out = env.paint(cyan, text[:end]) + text[end:]
			}
		case !inHunk:
			out = env.paint(bold, text)
		case strings.HasPrefix(text, "+"):
			out = env.paint(green, text)
		case strings.HasPrefix(text, "-"):
			out = env.paint(red, text)
		default:
			out = text
		}
		fmt.Fprintln(env.Stdout, out)
	}
}
