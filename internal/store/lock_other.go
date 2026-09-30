//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package store

import "os"

// openLock opens the lock file, creating it, refusing a link that is
// already there.
func openLock(path string) (*os.File, error) {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
}

// Platforms without file locking run unlocked.
func tryLock(f *os.File) (bool, error) { return true, nil }

func unlockFile(f *os.File) {}
