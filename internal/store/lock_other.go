//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package store

import "os"

// Platforms without file locking run unlocked.
func tryLock(f *os.File) (bool, error) { return true, nil }

func unlockFile(f *os.File) {}
