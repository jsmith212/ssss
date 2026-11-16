//go:build !windows && !(cgo && (linux || darwin))

package dragout

import "unsafe"

func start(unsafe.Pointer, []string, string) (bool, error) {
	return false, ErrUnsupported
}
