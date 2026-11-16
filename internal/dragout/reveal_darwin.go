//go:build darwin

package dragout

import (
	"fmt"
	"os/exec"
)

func reveal(path string) error {
	if out, err := exec.Command("open", "-R", "--", path).CombinedOutput(); err != nil {
		return fmt.Errorf("dragout: open -R: %w: %s", err, out)
	}
	return nil
}
