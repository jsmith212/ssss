//go:build cgo && (linux || darwin)

package dragout

/*
#cgo linux pkg-config: gtk4
#cgo darwin CFLAGS: -fobjc-arc
#cgo darwin LDFLAGS: -framework Cocoa

#include <stdlib.h>
#include "dragout_cgo.h"
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"unsafe"
)

//export dragoutDone
func dragoutDone(handle C.uintptr_t, result C.int) {
	// The channel is buffered, so this never blocks the UI thread.
	cgo.Handle(handle).Value().(chan int) <- int(result)
}

func start(window unsafe.Pointer, paths []string, label string) (bool, error) {
	cpaths := make([]*C.char, len(paths))
	for i, p := range paths {
		cpaths[i] = C.CString(p)
	}
	clabel := C.CString(label)
	defer func() {
		for _, p := range cpaths {
			C.free(unsafe.Pointer(p))
		}
		C.free(unsafe.Pointer(clabel))
	}()

	done := make(chan int, 1)
	handle := cgo.NewHandle(done)
	defer handle.Delete()

	C.dragout_start(window, &cpaths[0], C.int(len(cpaths)), clabel, C.uintptr_t(handle))

	switch result := <-done; result {
	case C.DRAGOUT_DROPPED:
		return true, nil
	case C.DRAGOUT_CANCELLED:
		return false, nil
	case C.DRAGOUT_ERR_NO_WINDOW:
		return false, ErrNoWindow
	case C.DRAGOUT_ERR_BUTTON_RELEASED:
		return false, ErrButtonReleased
	case C.DRAGOUT_ERR_START_FAILED:
		return false, ErrStartFailed
	default:
		return false, fmt.Errorf("dragout: unexpected result %d", result)
	}
}
