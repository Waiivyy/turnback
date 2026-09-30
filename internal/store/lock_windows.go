//go:build windows

package store

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileFailImmediately = 0x1
	lockfileExclusiveLock   = 0x2
	errorLockViolation      = syscall.Errno(33)
	// The locked byte lies far past the end of the file, so the pid written
	// at the start stays readable for other processes.
	lockOffset = 0x7fffffff
)

// tryLock takes an exclusive LockFileEx lock on f without waiting.
func tryLock(f *os.File) (bool, error) {
	ol := syscall.Overlapped{Offset: lockOffset}
	r, _, err := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r != 0 {
		return true, nil
	}
	if err == errorLockViolation || err == syscall.ERROR_IO_PENDING {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	ol := syscall.Overlapped{Offset: lockOffset}
	procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
}
