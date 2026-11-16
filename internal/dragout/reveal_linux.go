//go:build linux

package dragout

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/godbus/dbus/v5"
)

const fileManagerTimeout = 3 * time.Second

func reveal(path string) error {
	if err := showItems(path); err == nil {
		return nil
	}
	// No file manager implements FileManager1 (bare window managers, some
	// minimal desktops): open the folder without a selection.
	dir := filepath.Dir(path)
	cmd := exec.Command("xdg-open", dir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("dragout: xdg-open: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// showItems asks the desktop's file manager to open the containing folder
// with the file selected, via the org.freedesktop.FileManager1 interface that
// Nautilus, Dolphin, Nemo, Thunar and others implement.
func showItems(path string) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), fileManagerTimeout)
	defer cancel()
	obj := conn.Object("org.freedesktop.FileManager1", "/org/freedesktop/FileManager1")
	return obj.CallWithContext(ctx, "org.freedesktop.FileManager1.ShowItems", 0,
		[]string{fileURI(path)}, "").Err
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}
