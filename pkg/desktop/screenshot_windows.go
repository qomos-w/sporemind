//go:build windows

package desktop

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"syscall"
	"unsafe"
)

// WindowInfo represents a captured application window.
type WindowInfo struct {
	Handle uintptr
	Title  string
	X      int
	Y      int
	Width  int
	Height int
	ZOrder int
}

// ScreenshotData holds the screenshot image and window list.
type ScreenshotData struct {
	ImageBase64 string
	Windows     []WindowInfo
}

var (
	gdi32                      = syscall.NewLazyDLL("gdi32.dll")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	procGetDeviceCaps          = gdi32.NewProc("GetDeviceCaps")

	procGetDC                = user32.NewProc("GetDC")
	procReleaseDC            = user32.NewProc("ReleaseDC")
	procEnumWindows          = user32.NewProc("EnumWindows")
	procGetWindowRect        = user32.NewProc("GetWindowRect")
	procIsWindowVisible      = user32.NewProc("IsWindowVisible")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procSetParent            = user32.NewProc("SetParent")
	procGetWindowLongW       = user32.NewProc("GetWindowLongW")
	procSetWindowLongW       = user32.NewProc("SetWindowLongW")
	procSetWindowPos         = user32.NewProc("SetWindowPos")
	procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	procShowWindow           = user32.NewProc("ShowWindow")
)

const (
	SRCCOPY      = 0x00CC0020
	horzRES      = 8
	vertRES      = 10
	dibRGBColors = 0

	gwlStyle        uintptr = ^uintptr(15)
	wsChild         uintptr = 0x40000000
	wsPopup         uintptr = 0x80000000
	wsCaption       uintptr = 0x00C00000
	wsThickFrame    uintptr = 0x00040000
	wsSysMenu       uintptr = 0x00080000
	wsMinimizeBox   uintptr = 0x00020000
	wsMaximizeBox   uintptr = 0x00010000
	wsDlgFrame      uintptr = 0x00400000
	wsBorder        uintptr = 0x00800000
	swpNoSize       uintptr = 0x0001
	swpNoMove       uintptr = 0x0002
	swpNoZOrder     uintptr = 0x0004
	swpNoActivate   uintptr = 0x0010
	swpFrameChanged uintptr = 0x0020
	swRestore       uintptr = 9
)

// Windows types.
type (
	winHDC     uintptr
	winHBITMAP uintptr
	winHWND    uintptr
	winHANDLE  uintptr
)

type rect struct {
	Left, Top, Right, Bottom int32
}

type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type bitmapInfo struct {
	BmiHeader bitmapInfoHeader
}

func getDC(hwnd winHWND) winHDC {
	ret, _, _ := procGetDC.Call(uintptr(hwnd))
	return winHDC(ret)
}

func releaseDC(hwnd winHWND, dc winHDC) int32 {
	ret, _, _ := procReleaseDC.Call(uintptr(hwnd), uintptr(dc))
	return int32(ret)
}

func getDeviceCaps(dc winHDC, index int32) int32 {
	ret, _, _ := procGetDeviceCaps.Call(uintptr(dc), uintptr(index))
	return int32(ret)
}

func createCompatibleDC(dc winHDC) winHDC {
	ret, _, _ := procCreateCompatibleDC.Call(uintptr(dc))
	return winHDC(ret)
}

func deleteDC(dc winHDC) bool {
	ret, _, _ := procDeleteDC.Call(uintptr(dc))
	return ret != 0
}

func createCompatibleBitmap(dc winHDC, width, height int32) winHBITMAP {
	ret, _, _ := procCreateCompatibleBitmap.Call(uintptr(dc), uintptr(width), uintptr(height))
	return winHBITMAP(ret)
}

func deleteObject(hObject uintptr) bool {
	ret, _, _ := procDeleteObject.Call(hObject)
	return ret != 0
}

func selectObject(dc winHDC, hObject uintptr) uintptr {
	ret, _, _ := procSelectObject.Call(uintptr(dc), hObject)
	return ret
}

func bitBlt(dcDest winHDC, nXDest, nYDest, nWidth, nHeight int32, dcSrc winHDC, nXSrc, nYSrc int32, dwRop uint32) bool {
	ret, _, _ := procBitBlt.Call(
		uintptr(dcDest),
		uintptr(nXDest), uintptr(nYDest),
		uintptr(nWidth), uintptr(nHeight),
		uintptr(dcSrc),
		uintptr(nXSrc), uintptr(nYSrc),
		uintptr(dwRop),
	)
	return ret != 0
}

func createDIBSection(dc winHDC, pbmi *bitmapInfo, iUsage uint32, ppvBits *unsafe.Pointer, hSection winHANDLE, dwOffset uint32) winHBITMAP {
	ret, _, _ := procCreateDIBSection.Call(
		uintptr(dc),
		uintptr(unsafe.Pointer(pbmi)),
		uintptr(iUsage),
		uintptr(unsafe.Pointer(ppvBits)),
		uintptr(hSection),
		uintptr(dwOffset),
	)
	return winHBITMAP(ret)
}

func isWindowVisible(hwnd winHWND) bool {
	ret, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
	return ret != 0
}

func getWindowRect(hwnd winHWND, r *rect) bool {
	ret, _, _ := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(r)))
	return ret != 0
}

func getWindowTextLength(hwnd winHWND) int32 {
	ret, _, _ := procGetWindowTextLengthW.Call(uintptr(hwnd))
	return int32(ret)
}

func getWindowText(hwnd winHWND, buf []uint16, maxCount int32) int32 {
	if len(buf) == 0 {
		return 0
	}
	ret, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(maxCount))
	return int32(ret)
}

func captureWindows() ([]WindowInfo, error) {
	var windows []WindowInfo
	var zOrder int

	callback := syscall.NewCallback(func(hwnd winHWND, lparam uintptr) uintptr {
		if !isWindowVisible(hwnd) {
			return 1
		}

		var r rect
		if !getWindowRect(hwnd, &r) {
			return 1
		}

		width := int(r.Right - r.Left)
		height := int(r.Bottom - r.Top)
		if width <= 0 || height <= 0 {
			return 1
		}

		titleLen := getWindowTextLength(hwnd)
		if titleLen == 0 {
			return 1
		}

		buf := make([]uint16, titleLen+1)
		getWindowText(hwnd, buf, int32(len(buf)))
		title := syscall.UTF16ToString(buf)

		if title == "" {
			return 1
		}

		windows = append(windows, WindowInfo{
			Handle: uintptr(hwnd),
			Title:  title,
			X:      int(r.Left),
			Y:      int(r.Top),
			Width:  width,
			Height: height,
			ZOrder: zOrder,
		})
		zOrder++
		return 1
	})

	procEnumWindows.Call(callback, 0)
	return windows, nil
}

func activateWindow(handle uintptr) error {
	if handle == 0 {
		return fmt.Errorf("invalid window handle")
	}
	procShowWindow.Call(handle, swRestore)
	activated, _, err := procSetForegroundWindow.Call(handle)
	if activated == 0 {
		return fmt.Errorf("SetForegroundWindow failed: %w", err)
	}
	return nil
}

func captureScreen() (image.Image, error) {
	dc := getDC(0)
	if dc == 0 {
		return nil, fmt.Errorf("GetDC failed")
	}
	defer releaseDC(0, dc)

	width := int(getDeviceCaps(dc, horzRES))
	height := int(getDeviceCaps(dc, vertRES))
	return captureScreenRegion(0, 0, width, height)
}

// captureScreenRegion copies a rectangular area of the screen into an RGBA image.
func captureScreenRegion(x, y, width, height int) (image.Image, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid region %dx%d", width, height)
	}
	dc := getDC(0)
	if dc == 0 {
		return nil, fmt.Errorf("GetDC failed")
	}
	defer releaseDC(0, dc)

	var bmi bitmapInfo
	bmi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bmi.BmiHeader))
	bmi.BmiHeader.BiWidth = int32(width)
	bmi.BmiHeader.BiHeight = -int32(height) // top-down DIB
	bmi.BmiHeader.BiPlanes = 1
	bmi.BmiHeader.BiBitCount = 32
	bmi.BmiHeader.BiCompression = 0 // BI_RGB

	var bits unsafe.Pointer
	memDC := createCompatibleDC(dc)
	if memDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer deleteDC(memDC)

	dib := createDIBSection(memDC, &bmi, dibRGBColors, &bits, 0, 0)
	if dib == 0 {
		return nil, fmt.Errorf("CreateDIBSection failed")
	}
	defer deleteObject(uintptr(dib))

	selectObject(memDC, uintptr(dib))

	if !bitBlt(memDC, 0, 0, int32(width), int32(height), dc, int32(x), int32(y), SRCCOPY) {
		return nil, fmt.Errorf("BitBlt failed")
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	src := unsafe.Slice((*byte)(bits), width*height*4)
	copy(img.Pix, src)

	// BGRA to RGBA
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+2] = img.Pix[i+2], img.Pix[i]
	}

	return img, nil
}

func encodeImageToPNGBase64(img image.Image) (string, error) {
	var buf bytes.Buffer
	// Default (BestCompression) filtering spends 100-400ms on a full-window
	// capture, which shows up as a visible delay before the pre-hide snapshot
	// swap. BestSpeed keeps UI screenshots (mostly flat colour) fast while
	// staying a valid PNG.
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// captureWindowRegion captures the on-screen content of a Win32 window by its
// HWND and returns a data:image/png;base64 string.
func captureWindowRegion(hwndPtr unsafe.Pointer) (string, error) {
	hwnd := winHWND(uintptr(hwndPtr))
	var r rect
	if !getWindowRect(hwnd, &r) {
		return "", fmt.Errorf("GetWindowRect failed")
	}
	w := int(r.Right - r.Left)
	h := int(r.Bottom - r.Top)
	if w <= 0 || h <= 0 {
		return "", fmt.Errorf("invalid window rect %dx%d", w, h)
	}
	img, err := captureScreenRegion(int(r.Left), int(r.Top), w, h)
	if err != nil {
		return "", err
	}
	b64, err := encodeImageToPNGBase64(img)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + b64, nil
}

// setChildWindow makes child a child of parentWin via Win32 SetParent. Once
// parented, the child moves/clips automatically with the parent — zero-latency
// follow, no IPC, no event handling needed for moves. Position becomes relative
// to the parent's client area.
func setChildWindow(childPtr, parentPtr unsafe.Pointer) {
	child := winHWND(uintptr(childPtr))
	parent := winHWND(uintptr(parentPtr))
	if child == 0 || parent == 0 {
		return
	}
	procSetParent.Call(uintptr(child), uintptr(parent))

	// Convert from WS_POPUP to WS_CHILD. Frameless Wails windows use WS_POPUP,
	// which keeps the window independently activatable — clicking it when the
	// parent is behind other apps activates the child but not the parent.
	// WS_CHILD makes focus propagate to the parent naturally.
	style, _, _ := procGetWindowLongW.Call(uintptr(child), uintptr(gwlStyle))
	cur := style & 0xFFFFFFFF
	cur &^= wsPopup | wsCaption | wsThickFrame | wsSysMenu | wsMinimizeBox | wsMaximizeBox | wsDlgFrame | wsBorder
	cur |= wsChild
	procSetWindowLongW.Call(uintptr(child), uintptr(gwlStyle), cur)

	procSetWindowPos.Call(
		uintptr(child), 0, 0, 0, 0, 0,
		uintptr(swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged),
	)
}
