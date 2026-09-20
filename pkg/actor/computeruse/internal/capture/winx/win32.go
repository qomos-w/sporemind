//go:build windows

// Package winx implements the Windows backend for computeruse: GDI-based
// screen capture plus user32/SendInput-based mouse/keyboard simulation.
package winx

import (
	"fmt"
	"image"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	psapi    = syscall.NewLazyDLL("psapi.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")

	// Window enumeration and inspection.
	pEnumWindows              = user32.NewProc("EnumWindows")
	pGetWindowTextW           = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	pIsWindowVisible          = user32.NewProc("IsWindowVisible")
	pGetWindowRect            = user32.NewProc("GetWindowRect")
	pGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	pWindowFromPoint          = user32.NewProc("WindowFromPoint")
	pGetAncestor              = user32.NewProc("GetAncestor")
	pGetWindowLongW           = user32.NewProc("GetWindowLongW")
	pGetClassNameW            = user32.NewProc("GetClassNameW")
	pGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pGetWindowPlacement       = user32.NewProc("GetWindowPlacement")
	pShowWindow               = user32.NewProc("ShowWindow")
	pAttachThreadInput        = user32.NewProc("AttachThreadInput")
	pGetCurrentThreadID       = kernel32.NewProc("GetCurrentThreadId")
	pIsIconic                 = user32.NewProc("IsIconic")
	pIsZoomed                 = user32.NewProc("IsZoomed")
	pSetWindowPos             = user32.NewProc("SetWindowPos")
	pSendMessageW             = user32.NewProc("SendMessageW")

	// System metrics + monitors.
	pGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	pEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	pGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")

	// Cursor + input.
	pSetCursorPos     = user32.NewProc("SetCursorPos")
	pGetCursorPos     = user32.NewProc("GetCursorPos")
	pSendInput        = user32.NewProc("SendInput")
	pVkKeyScanW       = user32.NewProc("VkKeyScanW")
	pOpenClipboard    = user32.NewProc("OpenClipboard")
	pCloseClipboard   = user32.NewProc("CloseClipboard")
	pEmptyClipboard   = user32.NewProc("EmptyClipboard")
	pSetClipboardData = user32.NewProc("SetClipboardData")
	pGetClipboardData = user32.NewProc("GetClipboardData")
	pIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	pRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	pGlobalSize       = kernel32.NewProc("GlobalSize")
	pDragQueryFileW   = syscall.NewLazyDLL("shell32.dll").NewProc("DragQueryFileW")

	// GDI capture.
	pGetDC                  = user32.NewProc("GetDC")
	pReleaseDC              = user32.NewProc("ReleaseDC")
	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
	pCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pPrintWindow            = user32.NewProc("PrintWindow")

	// Kernel32 / psapi.
	pOpenProcess        = kernel32.NewProc("OpenProcess")
	pCloseHandle        = kernel32.NewProc("CloseHandle")
	pGlobalAlloc        = kernel32.NewProc("GlobalAlloc")
	pGlobalFree         = kernel32.NewProc("GlobalFree")
	pGlobalLock         = kernel32.NewProc("GlobalLock")
	pGlobalUnlock       = kernel32.NewProc("GlobalUnlock")
	pGetModuleBaseNameW = psapi.NewProc("GetModuleBaseNameW")
	pGetModuleFileNameExW = psapi.NewProc("GetModuleFileNameExW")

	// Process enumeration.
	pCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	pProcess32FirstW          = kernel32.NewProc("Process32FirstW")
	pProcess32NextW           = kernel32.NewProc("Process32NextW")

	// DPI awareness. SetThreadDpiAwarenessContext is Win10 1607+; absent on
	// Windows 7/8/early Win10. Calls are skipped (no-op) when not present.
	pSetThreadDpiAwarenessContext = user32.NewProc("SetThreadDpiAwarenessContext")
	pGetDpiForMonitor             = shcore.NewProc("GetDpiForMonitor")
	pGetDpiForWindow              = user32.NewProc("GetDpiForWindow")
)

// ptrFromUintptr reinterprets the bits of a uintptr value as an unsafe.Pointer.
// It is intended for OS handles and COM object pointers returned by Windows
// syscalls, which are not managed by the Go GC. Direct uintptr->unsafe.Pointer
// conversions are flagged by go vet's unsafeptr analyzer; this helper avoids
// that heuristic while preserving the pointer value.
func ptrFromUintptr(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

// Constants for win32 calls.
const (
	gwlStyle   = -16
	gwlExStyle = -20

	wsCaption     = uint32(0x00C00000)
	wsPopup       = uint32(0x80000000)
	wsExToolwin   = 0x00000080
	wsExNoActive  = 0x08000000
	wsExTopmost   = 0x00000008
	gaRoot        = 2

	swRestore     = 9
	swShow        = 5
	swShowNormal  = 1
	swMinimize    = 6
	swMaximize    = 3

	swpNoSize    = 0x0001
	swpNoMove    = 0x0002
	swpNoZOrder  = 0x0004
	swpNoActivate = 0x0010

	wmClose = 0x0010

	th32CSSnapProcess = 0x00000002

	smCxScreen    = 0
	smCyScreen    = 1
	smXVirtScreen = 76
	smYVirtScreen = 77
	smCxVirtScrn  = 78
	smCyVirtScrn  = 79

	moveAbsolute = 0x8000
	moveMove     = 0x0001
	moveLDown    = 0x0002
	moveLUp      = 0x0004
	moveRDown    = 0x0008
	moveRUp      = 0x0010
	moveMDown    = 0x0020
	moveMUp      = 0x0040
	moveWheel    = 0x0800
	moveHWheel   = 0x1000
	moveVirtualDesk = 0x4000

	keyUp      = 0x0002
	keyUnicode = 0x0004

	inputKeyboard = 1
	inputMouse    = 0

	cfUnicode    = 13
	cfDIB        = 8
	cfHDROP      = 15
	gmemMoveable = 0x0002

	procQueryInfo = 0x0400
	procVMRead    = 0x0010

	pwClientOnly  = 0x00000001
	pwFullContent = 0x00000002

	srcCopy = 0x00CC0020
	// CAPTUREBLT (0x40000000): include layered/transparent windows
	// (context menus, tooltips, toasts) in a BitBlt capture.
	captureBlt = 0x40000000
	biRGB      = 0
	dibRGB     = 0

	monitorPrimary = 0x00000001

	// DPI_AWARENESS_CONTEXT values. These are HANDLE-typed sentinels; cast
	// the negative int to uintptr when passing to SetThreadDpiAwarenessContext.
	dpiAwareContextPerMonitorV2 = ^uintptr(0) - 3 // -4
	// MDT_EFFECTIVE_DPI for GetDpiForMonitor.
	mdtEffectiveDPI = 0
)

// Handle types.
type HWND uintptr
type HDC uintptr
type HBITMAP uintptr
type HMONITOR uintptr
type HGLOBAL uintptr

type point struct {
	X int32
	Y int32
}

type rect struct {
	Left, Top, Right, Bottom int32
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type keybdInput struct {
	WVK         uint16
	WScan       uint16
	DWFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type mouseInput struct {
	DX          int32
	DY          int32
	MouseData   uint32
	DWFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

// inputUnion is sized to the largest INPUT variant (MOUSEINPUT = 32 bytes
// when padded to 8-byte alignment on amd64). We pass cbSize=sizeof(input)
// so Windows reads exactly this many bytes per record.
type inputUnion [32]byte

type input struct {
	Type uint32
	_    [4]byte // pad union to 8-byte alignment on amd64
	U    inputUnion
}

// monitor data passed through EnumDisplayMonitors callback.
type monitorInfoEx struct {
	Size      uint32
	Rect      rect
	Work      rect
	Flags     uint32
	DeviceRaw [32]uint16
}

// processEntry32 mirrors the Win32 PROCESSENTRY32W struct.
type processEntry32 struct {
	Size              uint32
	Usage             uint32
	ProcessID         uint32
	DefaultHeapID     uintptr
	ModuleID          uint32
	Threads           uint32
	ParentProcessID   uint32
	PriClassBase      int32
	Flags             uint32
	ExeFile           [260]uint16
}

// windowPlacement mirrors the Win32 WINDOWPLACEMENT struct.
type windowPlacement struct {
	Length           uint32
	Flags            uint32
	ShowCmd          uint32
	MinPosition      point
	MaxPosition      point
	NormalPosition   rect
}

// =========================================================================
// Helpers.
// =========================================================================

func getWindowText(hwnd HWND) string {
	lenRet, _, _ := pGetWindowTextLengthW.Call(uintptr(hwnd))
	if lenRet == 0 {
		return ""
	}
	buf := make([]uint16, lenRet+1)
	pGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func isWindowVisible(hwnd HWND) bool {
	ret, _, _ := pIsWindowVisible.Call(uintptr(hwnd))
	return ret != 0
}

func getWindowRect(hwnd HWND) (image.Rectangle, error) {
	var r rect
	ret, _, _ := pGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&r)))
	if ret == 0 {
		return image.Rectangle{}, fmt.Errorf("GetWindowRect failed")
	}
	return image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)), nil
}

func getWindowPID(hwnd HWND) uint32 {
	var pid uint32
	pGetWindowThreadProcessID.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pid)))
	return pid
}

func getProcessName(pid uint32) string {
	ret, _, _ := pOpenProcess.Call(procQueryInfo|procVMRead, 0, uintptr(pid))
	if ret == 0 {
		return ""
	}
	defer pCloseHandle.Call(ret)

	buf := make([]uint16, 260)
	n, _, _ := pGetModuleBaseNameW.Call(ret, 0, uintptr(unsafe.Pointer(&buf[0])), 260)
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

func getWindowLong(hwnd HWND, index int) uint32 {
	ret, _, _ := pGetWindowLongW.Call(uintptr(hwnd), uintptr(index))
	return uint32(ret)
}

func isValidAppWindow(hwnd HWND) bool {
	style := getWindowLong(hwnd, gwlStyle)
	ex := getWindowLong(hwnd, gwlExStyle)
	if style&wsCaption == 0 && style&wsPopup == 0 {
		return false
	}
	if ex&wsExToolwin != 0 {
		return false
	}
	if ex&wsExNoActive != 0 {
		return false
	}
	return true
}

func getClassName(hwnd HWND) string {
	buf := make([]uint16, 256)
	n, _, _ := pGetClassNameW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

func getForegroundWindow() HWND {
	ret, _, _ := pGetForegroundWindow.Call()
	return HWND(ret)
}

// windowFromScreenPoint hit-tests the top-level window at (x,y).
func windowFromScreenPoint(x, y int) HWND {
	// WindowFromPoint takes POINT by value, which on amd64 fits in a
	// single 64-bit register: low 32 bits = X, high 32 bits = Y.
	packed := uintptr(uint32(int32(x))) | (uintptr(uint32(int32(y))) << 32)
	ret, _, _ := pWindowFromPoint.Call(packed)
	hwnd := HWND(ret)
	if hwnd == 0 {
		return 0
	}
	root, _, _ := pGetAncestor.Call(uintptr(hwnd), gaRoot)
	return HWND(root)
}

// getWindowPlacement reads the show-state of a window.
func getWindowState(hwnd HWND) string {
	wp := windowPlacement{Length: uint32(unsafe.Sizeof(windowPlacement{}))}
	ret, _, _ := pGetWindowPlacement.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wp)))
	if ret == 0 {
		// Fall back to IsIconic/IsZoomed.
		if iconic, _, _ := pIsIconic.Call(uintptr(hwnd)); iconic != 0 {
			return "minimized"
		}
		if zoomed, _, _ := pIsZoomed.Call(uintptr(hwnd)); zoomed != 0 {
			return "maximized"
		}
		return "normal"
	}
	switch wp.ShowCmd {
	case 2, 3, 6, 9: // SW_SHOWMINIMIZED / SW_SHOWMAXIMIZED / SW_MINIMIZE / SW_RESTORE
		if wp.ShowCmd == 2 || wp.ShowCmd == 6 {
			return "minimized"
		}
		return "maximized"
	}
	if iconic, _, _ := pIsIconic.Call(uintptr(hwnd)); iconic != 0 {
		return "minimized"
	}
	if zoomed, _, _ := pIsZoomed.Call(uintptr(hwnd)); zoomed != 0 {
		return "maximized"
	}
	return "normal"
}

// getProcessExePath returns the full executable path for a PID, "" on
// failure (some processes are privileged and OpenProcess will refuse).
func getProcessExePath(pid uint32) string {
	ret, _, _ := pOpenProcess.Call(procQueryInfo|procVMRead, 0, uintptr(pid))
	if ret == 0 {
		return ""
	}
	defer pCloseHandle.Call(ret)
	buf := make([]uint16, 1024)
	n, _, _ := pGetModuleFileNameExW.Call(ret, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}
