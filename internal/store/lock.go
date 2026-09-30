package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Lock takes the lock that serializes turnback commands in this working
// tree, waiting up to wait for another command to finish.
//
// It is an operating system lock on .turnback/lock, so it is released the
// moment its holder exits, even if it crashes: there is never a stale lock
// to clean up, and so no race between two processes cleaning one up. The
// file itself is never deleted; that would let two processes lock two
// different files of the same name.
func (s *Store) Lock(wait time.Duration) (unlock func(), err error) {
	path := filepath.Join(s.Dir, "lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			f.Close()
			if pid := lockHolder(path); pid > 0 {
				return nil, fmt.Errorf("%w (pid %d)", ErrLocked, pid)
			}
			return nil, ErrLocked
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Note who holds the lock, for the message others see.
	if err := f.Truncate(0); err == nil {
		f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return func() {
		unlockFile(f)
		f.Close()
	}, nil
}

// lockHolder returns the pid written into the lock file, or 0.
func lockHolder(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}
