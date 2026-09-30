package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Lock takes the lock that serializes turnback commands in this working
// tree, waiting up to wait for another command to finish. A lock left behind
// by a process that no longer exists is cleared automatically.
func (s *Store) Lock(wait time.Duration) (unlock func(), err error) {
	path := filepath.Join(s.Dir, "lock")
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		pid, stale := inspectLock(path)
		if stale {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			if pid > 0 {
				return nil, fmt.Errorf("%w (pid %d); if it is not, delete %s", ErrLocked, pid, path)
			}
			return nil, fmt.Errorf("%w; if it is not, delete %s", ErrLocked, path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// inspectLock returns the pid recorded in a lock file and whether the lock
// is stale: its owner is gone, or it never got a pid and is old.
func inspectLock(path string) (pid int, stale bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		fi, statErr := os.Stat(path)
		return 0, statErr == nil && time.Since(fi.ModTime()) > 10*time.Second
	}
	return pid, !processAlive(pid)
}
