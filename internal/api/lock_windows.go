package api

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on path, creating the file. It returns
// errLocked when another holder (another process, or another open in this
// one) has it. The lock dies with the process, so a crash never leaves a
// library locked.
func lockFile(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, new(windows.Overlapped)); err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, errLocked
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}
