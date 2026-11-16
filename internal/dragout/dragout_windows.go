//go:build windows

package dragout

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

var (
	ole32   = windows.NewLazySystemDLL("ole32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	user32  = windows.NewLazySystemDLL("user32.dll")
	gdi32   = windows.NewLazySystemDLL("gdi32.dll")

	procOleInitialize                     = ole32.NewProc("OleInitialize")
	procDoDragDrop                        = ole32.NewProc("DoDragDrop")
	procCoCreateInstance                  = ole32.NewProc("CoCreateInstance")
	procILCreateFromPathW                 = shell32.NewProc("ILCreateFromPathW")
	procILFree                            = shell32.NewProc("ILFree")
	procSHCreateShellItemArrayFromIDLists = shell32.NewProc("SHCreateShellItemArrayFromIDLists")
	procSHCreateItemFromParsingName       = shell32.NewProc("SHCreateItemFromParsingName")
	procGetAsyncKeyState                  = user32.NewProc("GetAsyncKeyState")
	procGetSystemMetrics                  = user32.NewProc("GetSystemMetrics")
	procIsWindow                          = user32.NewProc("IsWindow")
	procGetObjectW                        = gdi32.NewProc("GetObjectW")
	procDeleteObject                      = gdi32.NewProc("DeleteObject")
)

var (
	iidIUnknown               = windows.GUID{Data1: 0x00000000, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDropSource            = windows.GUID{Data1: 0x00000121, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDataObject            = windows.GUID{Data1: 0x0000010E, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	bhidDataObject            = windows.GUID{Data1: 0xB8C0BD9F, Data2: 0xED24, Data3: 0x455C, Data4: [8]byte{0x83, 0xE6, 0xD5, 0x39, 0x0C, 0x4F, 0xE8, 0xC4}}
	clsidDragDropHelper       = windows.GUID{Data1: 0x4657278A, Data2: 0x411B, Data3: 0x11D2, Data4: [8]byte{0x83, 0x9A, 0x00, 0xC0, 0x4F, 0xD9, 0x18, 0xD0}}
	iidIDragSourceHelper      = windows.GUID{Data1: 0xDE5BF786, Data2: 0x477A, Data3: 0x11D2, Data4: [8]byte{0x83, 0x9D, 0x00, 0xC0, 0x4F, 0xD9, 0x18, 0xD0}}
	iidIShellItemImageFactory = windows.GUID{Data1: 0xBCC18B79, Data2: 0xBA16, Data3: 0x442F, Data4: [8]byte{0x80, 0xC4, 0x8A, 0x59, 0xC3, 0x0C, 0x46, 0x3B}}
)

const (
	sOK                        = 0
	eNoInterface               = 0x80004002
	dragDropSDrop              = 0x00040100
	dragDropSCancel            = 0x00040101
	dragDropSUseDefaultCursors = 0x00040102
	dropEffectNone             = 0
	dropEffectCopy             = 1
	mkLButton                  = 0x0001
	mkRButton                  = 0x0002
	vkLButton                  = 0x01
	vkRButton                  = 0x02
	smSwapButton               = 23
	clsctxInprocServer         = 0x1
	siigbfIconOnly             = 0x4
	dragIconSize               = 48

	// Vtable slots, after the three IUnknown methods.
	slotRelease              = 2
	slotBindToHandler        = 3 // IShellItemArray
	slotGetImage             = 3 // IShellItemImageFactory
	slotInitializeFromBitmap = 3 // IDragSourceHelper

	pointerSize          = unsafe.Sizeof(uintptr(0))
	hresultFailureBit    = 0x80000000
	asyncKeyStateDownBit = 0x8000
)

// comObject is a COM interface pointer. It is kept as unsafe.Pointer rather
// than uintptr so that dereferencing the vtable stays within the unsafe rules.
type comObject unsafe.Pointer

// comCall invokes the method at vtable index slot on obj.
//
// Callers pass out-params as uintptr(unsafe.Pointer(&local)). The
// uintptrescapes directive makes the compiler treat those conversions as it
// does for syscall.SyscallN: the locals are moved to the heap and kept alive
// for the duration of the call. Without it they stay on the goroutine stack,
// and a stack move before the COM method runs leaves it writing to the old
// stack. The directive only covers direct calls whose arguments are converted
// in the argument list, so comCall must not be called through a function
// value or wrapped by a helper that forwards uintptrs.
//
//go:uintptrescapes
func comCall(obj comObject, slot uintptr, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, slot*pointerSize))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func comRelease(obj comObject) {
	if obj != nil {
		comCall(obj, slotRelease)
	}
}

func failed(hr uintptr) bool { return uint32(hr)&hresultFailureBit != 0 }

func hresultError(op string, hr uintptr) error {
	return fmt.Errorf("dragout: %s: %w", op, windows.Errno(uint32(hr)))
}

// dropSource is a COM IDropSource whose methods are Go callbacks. There is a
// single static instance, so reference counting is a no-op.
type dropSource struct {
	vtbl *dropSourceVtbl
}

type dropSourceVtbl struct {
	queryInterface, addRef, release, queryContinueDrag, giveFeedback uintptr
}

var (
	dropSourceOnce sync.Once
	theDropSource  *dropSource
)

func getDropSource() *dropSource {
	dropSourceOnce.Do(func() {
		theDropSource = &dropSource{vtbl: &dropSourceVtbl{
			queryInterface: windows.NewCallback(func(this uintptr, riid *windows.GUID, ppv *uintptr) uintptr {
				if *riid == iidIUnknown || *riid == iidIDropSource {
					*ppv = this
					return sOK
				}
				*ppv = 0
				return eNoInterface
			}),
			addRef:  windows.NewCallback(func(this uintptr) uintptr { return 1 }),
			release: windows.NewCallback(func(this uintptr) uintptr { return 1 }),
			// fEscapePressed is a BOOL and grfKeyState a DWORD. The upper half
			// of a register carrying a 32-bit argument is undefined, so only
			// the low 32 bits are examined.
			queryContinueDrag: windows.NewCallback(func(this, escapePressed, keyState uintptr) uintptr {
				keys := uint32(keyState)
				switch {
				case uint32(escapePressed) != 0 || keys&mkRButton != 0:
					return dragDropSCancel
				case keys&mkLButton == 0:
					return dragDropSDrop
				default:
					return sOK
				}
			}),
			giveFeedback: windows.NewCallback(func(this, effect uintptr) uintptr {
				return dragDropSUseDefaultCursors
			}),
		}}
	})
	return theDropSource
}

func start(window unsafe.Pointer, paths []string, _ string) (dropped bool, err error) {
	if ok, _, _ := procIsWindow.Call(uintptr(window)); uint32(ok) == 0 {
		return false, ErrNoWindow
	}
	// DoDragDrop runs a modal loop and must be called on the thread that owns
	// the window. err is preset for the case where Wails recovers a panic in
	// the callback and returns without a result.
	err = ErrStartFailed
	application.InvokeSync(func() {
		dropped, err = dragOnUIThread(paths)
	})
	return dropped, err
}

func primaryButtonDown() bool {
	vk := uintptr(vkLButton)
	if swapped, _, _ := procGetSystemMetrics.Call(smSwapButton); uint32(swapped) != 0 {
		vk = vkRButton
	}
	state, _, _ := procGetAsyncKeyState.Call(vk)
	return state&asyncKeyStateDownBit != 0
}

func dragOnUIThread(paths []string) (bool, error) {
	// S_FALSE (already initialised on this thread) is fine. The matching
	// OleUninitialize is deliberately never called: the UI thread lives as
	// long as the process and Wails' own drop target relies on OLE too.
	if hr, _, _ := procOleInitialize.Call(0); failed(hr) {
		return false, hresultError("OleInitialize", hr)
	}
	if !primaryButtonDown() {
		return false, ErrButtonReleased
	}

	dataObject, err := shellDataObject(paths)
	if err != nil {
		return false, err
	}
	defer comRelease(dataObject)

	// Best effort: without it the drag shows only the cursor.
	setDragImage(dataObject, paths[0])

	var effect uint32
	hr, _, _ := procDoDragDrop.Call(
		uintptr(dataObject),
		uintptr(unsafe.Pointer(getDropSource())),
		dropEffectCopy,
		uintptr(unsafe.Pointer(&effect)),
	)
	// Only the low 32 bits of the return register hold the HRESULT.
	switch uint32(hr) {
	case dragDropSDrop:
		return effect != dropEffectNone, nil
	case dragDropSCancel:
		return false, nil
	default:
		return false, hresultError("DoDragDrop", hr)
	}
}

// shellDataObject returns the shell's own IDataObject for the files, which
// carries CF_HDROP, Shell IDList Array and the other formats Explorer offers.
func shellDataObject(paths []string) (comObject, error) {
	pidls := make([]uintptr, 0, len(paths))
	defer func() {
		for _, pidl := range pidls {
			procILFree.Call(pidl)
		}
	}()
	for _, p := range paths {
		wide, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return nil, fmt.Errorf("dragout: %q: %w", p, err)
		}
		pidl, _, _ := procILCreateFromPathW.Call(uintptr(unsafe.Pointer(wide)))
		if pidl == 0 {
			return nil, fmt.Errorf("dragout: file not found: %q", p)
		}
		pidls = append(pidls, pidl)
	}

	var items comObject
	hr, _, _ := procSHCreateShellItemArrayFromIDLists.Call(
		uintptr(len(pidls)),
		uintptr(unsafe.Pointer(&pidls[0])),
		uintptr(unsafe.Pointer(&items)),
	)
	if failed(hr) {
		return nil, hresultError("SHCreateShellItemArrayFromIDLists", hr)
	}
	defer comRelease(items)

	var dataObject comObject
	hr = comCall(items, slotBindToHandler,
		0,
		uintptr(unsafe.Pointer(&bhidDataObject)),
		uintptr(unsafe.Pointer(&iidIDataObject)),
		uintptr(unsafe.Pointer(&dataObject)),
	)
	if failed(hr) {
		return nil, hresultError("IShellItemArray.BindToHandler", hr)
	}
	if dataObject == nil {
		return nil, fmt.Errorf("dragout: IShellItemArray.BindToHandler returned no data object: %w", ErrStartFailed)
	}
	return dataObject, nil
}

// shDragImage mirrors SHDRAGIMAGE.
type shDragImage struct {
	width, height    int32
	offsetX, offsetY int32
	bitmap           uintptr
	colorKey         uint32
}

// gdiBitmap mirrors BITMAP.
type gdiBitmap struct {
	bmType, width, height, widthBytes int32
	planes, bitsPixel                 uint16
	bits                              uintptr
}

// setDragImage attaches the shell icon of path to the data object as the
// drag image. Failures are ignored.
func setDragImage(dataObject comObject, path string) {
	// GetImage takes a SIZE by value, packed here into one register. That
	// matches the amd64 and arm64 calling conventions but not 386.
	if pointerSize != 8 {
		return
	}
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	var factory comObject
	hr, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(wide)),
		0,
		uintptr(unsafe.Pointer(&iidIShellItemImageFactory)),
		uintptr(unsafe.Pointer(&factory)),
	)
	if failed(hr) {
		return
	}
	defer comRelease(factory)

	var bitmap uintptr
	size := uint64(dragIconSize) | uint64(dragIconSize)<<32
	hr = comCall(factory, slotGetImage, uintptr(size), siigbfIconOnly, uintptr(unsafe.Pointer(&bitmap)))
	if failed(hr) || bitmap == 0 {
		return
	}

	var info gdiBitmap
	n, _, _ := procGetObjectW.Call(bitmap, unsafe.Sizeof(info), uintptr(unsafe.Pointer(&info)))
	if uint32(n) == 0 {
		procDeleteObject.Call(bitmap)
		return
	}

	var helper comObject
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidDragDropHelper)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIDragSourceHelper)),
		uintptr(unsafe.Pointer(&helper)),
	)
	if failed(hr) {
		procDeleteObject.Call(bitmap)
		return
	}
	defer comRelease(helper)

	image := shDragImage{
		width:   info.width,
		height:  info.height,
		offsetX: info.width / 2,
		offsetY: info.height / 2,
		bitmap:  bitmap,
	}
	hr = comCall(helper, slotInitializeFromBitmap, uintptr(unsafe.Pointer(&image)), uintptr(dataObject))
	if failed(hr) {
		// On success the helper owns the bitmap.
		procDeleteObject.Call(bitmap)
	}
}
