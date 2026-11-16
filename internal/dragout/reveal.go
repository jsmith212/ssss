package dragout

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Reveal opens the system file manager with the file at path selected. Where
// the platform cannot select a file, it opens the containing folder instead.
func Reveal(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("dragout: path is not absolute: %q", path)
	}
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("dragout: file not found: %q", path)
		}
		return fmt.Errorf("dragout: %w", err)
	}
	return reveal(path)
}
