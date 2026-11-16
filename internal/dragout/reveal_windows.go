//go:build windows

package dragout

import (
	"fmt"
	"os/exec"
	"syscall"
)

func reveal(path string) error {
	cmd := exec.Command("explorer.exe")
	// Explorer parses its command line itself and only understands the path
	// when the quotes follow "/select," directly, which Go's argument quoting
	// would not produce. File names cannot contain a double quote.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("dragout: explorer: %w", err)
	}
	// Explorer exits with status 1 even on success, so the result is not
	// checked; waiting only reaps the process.
	go func() { _ = cmd.Wait() }()
	return nil
}
