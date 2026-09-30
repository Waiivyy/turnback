//go:build !(darwin || dragonfly || freebsd || netbsd || openbsd || linux || windows)

package cli

import "os"

// isTerminal is conservative where terminals cannot be detected: no colors,
// no pager and no prompts.
func isTerminal(f *os.File) bool { return false }
