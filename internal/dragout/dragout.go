package dragout

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"
	"unsafe"
)

var (
	// ErrUnsupported is returned on platforms without a native implementation.
	ErrUnsupported = errors.New("dragout: not supported on this platform")
	// ErrNoFiles is returned when Start is called without any path.
	ErrNoFiles = errors.New("dragout: no files to drag")
	// ErrNoWindow is returned when the window handle is nil or unusable.
	ErrNoWindow = errors.New("dragout: no window to drag from")
	// ErrButtonReleased is returned when the mouse button was released before
	// the drag could start. Starting anyway would leave a drag stuck to the
	// pointer.
	ErrButtonReleased = errors.New("dragout: mouse button released before the drag started")
	// ErrBusy is returned when another drag is still in progress.
	ErrBusy = errors.New("dragout: a drag is already in progress")
	// ErrStartFailed is returned when the OS refused to start the drag.
	ErrStartFailed = errors.New("dragout: the system did not start the drag")
)

// selfDropGrace keeps Active true briefly after a drag ends: the toolkit
// reports the end of the drag and delivers the drop to our own window
// independently, so the drop handler may run just after Start returns.
const selfDropGrace = 750 * time.Millisecond

var (
	running     atomic.Bool
	activeUntil atomic.Int64 // unix nanoseconds
)

// Active reports whether a drag started by this package is in progress or
// ended a moment ago. A file drop that arrives on our own window while it is
// true is our own drag coming back.
func Active() bool {
	return running.Load() || time.Now().UnixNano() < activeUntil.Load()
}

// Start begins a native file drag of paths from window and blocks until the
// drag ends. window is the handle returned by Wails' Window.NativeWindow():
// a GtkWindow* on Linux, an NSWindow* on macOS and an HWND on Windows. label
// names the dragged items in the drag image where the platform shows one; an
// empty label is derived from paths.
//
// dropped is true when a target accepted the drop. Start must not be called
// from the UI thread; it dispatches to it and waits. It fails with
// ErrButtonReleased when the primary mouse button is no longer held.
func Start(window unsafe.Pointer, paths []string, label string) (dropped bool, err error) {
	if len(paths) == 0 {
		return false, ErrNoFiles
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return false, fmt.Errorf("dragout: path is not absolute: %q", p)
		}
	}
	if window == nil {
		return false, ErrNoWindow
	}
	if !running.CompareAndSwap(false, true) {
		return false, ErrBusy
	}
	defer running.Store(false)
	if label == "" {
		label = defaultLabel(paths)
	}
	dropped, err = start(window, paths, label)
	if err == nil {
		activeUntil.Store(time.Now().Add(selfDropGrace).UnixNano())
	}
	return dropped, err
}

func defaultLabel(paths []string) string {
	if len(paths) == 1 {
		return filepath.Base(paths[0])
	}
	return fmt.Sprintf("%d files", len(paths))
}
