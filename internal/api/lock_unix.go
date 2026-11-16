//go:build unix

package api

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on path, creating the file. It
// returns errLocked when another holder (another process, or another open
// in this one) has it. The lock dies with the process, so a crash never
// leaves a library locked.
func lockFile(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLocked
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}
