//go:build !unix && !windows

package api

// lockFile is a no-op where there is no file locking.
func lockFile(string) (release func(), err error) { return func() {}, nil }
