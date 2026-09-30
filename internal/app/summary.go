package app

import (
	"fmt"
	"path"
	"strings"

	"github.com/Waiivyy/turnback/internal/store"
)

var verbs = map[string]string{"A": "Add", "M": "Update", "D": "Delete", "R": "Rename", "T": "Update"}

func verbFor(f store.File) string {
	if f.Ignored {
		return "Stop tracking"
	}
	return verb(f.Status)
}

func verb(status string) string {
	if v, ok := verbs[status]; ok {
		return v
	}
	return "Change"
}

// Summarize describes a set of changed files in a short sentence, used when
// a turn has no description of its own.
func Summarize(files []store.File) string {
	switch len(files) {
	case 0:
		return "No changes"
	case 1:
		f := files[0]
		if f.Status == "R" {
			return fmt.Sprintf("Rename %s to %s", f.OldPath, f.Path)
		}
		return verbFor(f) + " " + f.Path
	}
	v := verbFor(files[0])
	for _, f := range files[1:] {
		if verbFor(f) != v {
			v = "Change"
			break
		}
	}
	names := displayNames(files)
	if len(names) <= 3 {
		return v + " " + joinAnd(names)
	}
	return fmt.Sprintf("%s %d files: %s and %d more", v, len(names), strings.Join(names[:3], ", "), len(names)-3)
}

// displayNames returns base names, or full paths if two base names collide.
func displayNames(files []store.File) []string {
	names := make([]string, len(files))
	seen := make(map[string]bool)
	collide := false
	for i, f := range files {
		names[i] = path.Base(f.Path)
		if seen[names[i]] {
			collide = true
		}
		seen[names[i]] = true
	}
	if collide {
		for i, f := range files {
			names[i] = f.Path
		}
	}
	return names
}

func joinAnd(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
