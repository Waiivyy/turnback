//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package store

import (
	"errors"
	"os"
	"syscall"
)

// openLock opens the lock file, creating it, without following a symlink.
func openLock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o644)
}

// tryLock takes an exclusive flock on f without waiting.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EINTR):
		return false, nil
	case errors.Is(err, syscall.ENOTSUP), errors.Is(err, syscall.EOPNOTSUPP), errors.Is(err, syscall.ENOLCK):
		// The file system cannot lock, as NFS without a lock service cannot.
		// Refusing would make turnback unusable there, so it runs unlocked,
		// as on platforms without file locking.
		return true, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
