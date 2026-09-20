//go:build windows

package winx

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/annotate"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/diff"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/encoder"
)

func init() {
	capture.NewWindowsCapturer = func() (capture.Capturer, error) {
		return New()
	}
}

// monitor is one display entry.
type monitor struct {
	id        int
	hMon      HMONITOR
	name      string
	bounds    image.Rectangle
	isPrimary bool
}

// Capturer is the Windows GDI-backed capturer.
type Capturer struct {
	capture.BaseCapturer
	mu       sync.RWMutex
	monitors []*monitor
	uia      *uiaClient
}

// New creates and initializes the Windows capturer.
func New() (*Capturer, error) {
	c := &Capturer{}
	if err := c.refreshDisplays(); err != nil {
		return nil, err
	}
	c.uia = newUIAClient()
	return c, nil
}

func (c *Capturer) refreshDisplays() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.monitors = nil

	cb := syscall.NewCallback(func(hMon HMONITOR, _ HDC, lprc *rect, _ uintptr) uintptr {
		mi := monitorInfoEx{Size: uint32(unsafe.Sizeof(monitorInfoEx{}))}
		ret, _, _ := pGetMonitorInfoW.Call(uintptr(hMon), uintptr(unsafe.Pointer(&mi)))
		if ret == 0 {
			return 1
		}
		name := syscall.UTF16ToString(mi.DeviceRaw[:])
		if name == "" {
			name = fmt.Sprintf("DISPLAY%d", len(c.monitors)+1)
		}
		c.monitors = append(c.monitors, &monitor{
			id:        len(c.monitors),
			hMon:      hMon,
			name:      name,
			bounds:    image.Rect(int(mi.Rect.Left), int(mi.Rect.Top), int(mi.Rect.Right), int(mi.Rect.Bottom)),
			isPrimary: mi.Flags&monitorPrimary != 0,
		})
		return 1
	})
	ret, _, _ := pEnumDisplayMonitors.Call(0, 0, cb, 0)
	if ret == 0 && len(c.monitors) == 0 {
		// fallback to single virtual screen
		w, _, _ := pGetSystemMetrics.Call(smCxScreen)
		h, _, _ := pGetSystemMetrics.Call(smCyScreen)
		c.monitors = []*monitor{{
			id: 0, name: "DISPLAY1",
			bounds: image.Rect(0, 0, int(w), int(h)), isPrimary: true,
		}}
	}
	return nil
}

// Capture takes one frame on the chosen display per opts.Mode.
func (c *Capturer) Capture(opts capture.CaptureOptions) (*capture.Frame, error) {
	opts.Validate()
	start := time.Now()

	c.mu.RLock()
	if len(c.monitors) == 0 {
		c.mu.RUnlock()
		return nil, errors.New("no displays")
	}
	displayID := opts.DisplayID
	if displayID < 0 || displayID >= len(c.monitors) {
		displayID = 0
	}
	dispBounds := c.monitors[displayID].bounds
	c.mu.RUnlock()

	// Decide capture rect in screen coordinates.
	captureRect := dispBounds
	if opts.Mode == capture.ModeRegion && !opts.Region.Empty() {
		captureRect = opts.Region
	}

	pixels, err := captureScreenRect(captureRect)
	if err != nil {
		return nil, err
	}
	bounds := image.Rect(0, 0, captureRect.Dx(), captureRect.Dy())

	img := &image.RGBA{Pix: pixels, Stride: bounds.Dx() * 4, Rect: bounds}

	// Cursor position (always queried).
	cursor, cursorErr := c.CursorPosition()
	cursorInFrame := image.Point{X: cursor.X - captureRect.Min.X, Y: cursor.Y - captureRect.Min.Y}

	if opts.IncludeCursor {
		annotate.NewCursorDrawer().DrawCursor(img, cursorInFrame)
	}
	if opts.Annotate.DrawGrid {
		g := annotate.NewGridDrawer()
		if opts.Annotate.GridSize > 0 {
			g.CellSize = opts.Annotate.GridSize
		}
		g.Draw(img)
	}

	// Delta mode: compare to PreviousFrame.
	if opts.Mode == capture.ModeDelta && opts.PreviousFrame != nil && len(opts.PreviousFrame.RawPixels) == len(pixels) {
		det := diff.NewDetector()
		res, err := det.Compare(opts.PreviousFrame.RawPixels, pixels, bounds.Dx(), bounds.Dy())
		if err == nil && res.IsIdentical {
			return &capture.Frame{
				Timestamp:   time.Now(),
				DisplayID:   displayID,
				IsIdentical: true,
				Duration:    time.Since(start),
				FrameIndex:  c.NextFrameIndex(),
			}, nil
		}
	}

	// Encode.
	enc, err := encoder.New(opts.EncodeFormat)
	if err != nil {
		return nil, err
	}
	var data []byte
	if opts.DisableCompression {
		data = make([]byte, len(pixels))
		copy(data, pixels)
	} else {
		data, err = enc.Encode(pixels, bounds, opts.Quality)
		if err != nil {
			return nil, fmt.Errorf("encode: %w", err)
		}
	}

	frame := &capture.Frame{
		Data:            data,
		RawPixels:       pixels,
		Bounds:          captureRect,
		Timestamp:       time.Now(),
		DisplayID:       displayID,
		CursorPos:       cursor,
		CursorValid:     cursorErr == nil,
		HasCursor:       opts.IncludeCursor,
		EncodingFormat:  enc.Format(),
		EncodingQuality: opts.Quality,
		FrameIndex:      c.NextFrameIndex(),
		Duration:        time.Since(start),
	}
	c.SetLastFrame(frame)
	return frame, nil
}

// CaptureWindow captures a single window. Visible windows are captured
// from screen + crop; covered windows fall back to PrintWindow.
func (c *Capturer) CaptureWindow(windowID int, opts capture.CaptureOptions) (*capture.Frame, error) {
	hwnd := HWND(uintptr(windowID))
	rect, err := getWindowRect(hwnd)
	if err != nil {
		return nil, fmt.Errorf("window rect: %w", err)
	}
	if isWindowVisible(hwnd) {
		opts.Mode = capture.ModeRegion
		opts.Region = rect
		return c.Capture(opts)
	}
	return c.printWindow(hwnd, opts)
}

func (c *Capturer) printWindow(hwnd HWND, opts capture.CaptureOptions) (*capture.Frame, error) {
	opts.Validate()
	start := time.Now()
	r, err := getWindowRect(hwnd)
	if err != nil {
		return nil, err
	}
	w := r.Dx()
	h := r.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid window size %dx%d", w, h)
	}

	hdcWnd, _, _ := pGetDC.Call(uintptr(hwnd))
	if hdcWnd == 0 {
		return nil, errors.New("GetDC failed")
	}
	defer pReleaseDC.Call(uintptr(hwnd), hdcWnd)

	hdcMem, _, _ := pCreateCompatibleDC.Call(hdcWnd)
	if hdcMem == 0 {
		return nil, errors.New("CreateCompatibleDC failed")
	}
	defer pDeleteDC.Call(hdcMem)

	hBmp, bits, err := createDIBSection(hdcMem, w, h)
	if err != nil {
		return nil, err
	}
	defer pDeleteObject.Call(hBmp)

	if prev, _, _ := pSelectObject.Call(hdcMem, hBmp); prev != 0 {
		defer pSelectObject.Call(hdcMem, prev)
	}
	pwRet, _, _ := pPrintWindow.Call(uintptr(hwnd), hdcMem, pwFullContent)
	if pwRet == 0 {
		pwRet, _, _ = pPrintWindow.Call(uintptr(hwnd), hdcMem, 0)
		if pwRet == 0 {
			pBitBlt.Call(hdcMem, 0, 0, uintptr(w), uintptr(h), hdcWnd, 0, 0, srcCopy)
		}
	}

	pixels := copyPixels(bits, w, h)

	bounds := image.Rect(0, 0, w, h)
	enc, err := encoder.New(opts.EncodeFormat)
	if err != nil {
		return nil, err
	}
	data, err := enc.Encode(pixels, bounds, opts.Quality)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	return &capture.Frame{
		Data:           data,
		RawPixels:      pixels,
		Bounds:         r,
		Timestamp:      time.Now(),
		DisplayID:      -1,
		EncodingFormat: enc.Format(),
		FrameIndex:     c.NextFrameIndex(),
		Duration:       time.Since(start),
	}, nil
}

// captureScreenRect grabs the pixels for r from the desktop DC.
//
// The blit target is a 32bpp top-down DIB section, not a compatible (DDB)
// bitmap. Reading a DDB back with GetDIBits is unreliable in real desktop
// sessions: it fails intermittently for bitmaps beyond roughly 500x500 and
// consistently at fullscreen sizes. Blitting straight into a DIB section has
// no readback step, so it works at every size.
func captureScreenRect(r image.Rectangle) ([]byte, error) {
	if r.Empty() {
		return nil, fmt.Errorf("empty capture rect")
	}
	w := r.Dx()
	h := r.Dy()

	hdcScreen, _, _ := pGetDC.Call(0)
	if hdcScreen == 0 {
		return nil, errors.New("GetDC(desktop) failed")
	}
	defer pReleaseDC.Call(0, hdcScreen)

	hdcMem, _, _ := pCreateCompatibleDC.Call(hdcScreen)
	if hdcMem == 0 {
		return nil, errors.New("CreateCompatibleDC failed")
	}
	defer pDeleteDC.Call(hdcMem)

	hBmp, bits, err := createDIBSection(hdcMem, w, h)
	if err != nil {
		return nil, err
	}
	defer pDeleteObject.Call(hBmp)

	if prev, _, _ := pSelectObject.Call(hdcMem, hBmp); prev != 0 {
		defer pSelectObject.Call(hdcMem, prev)
	}
	ok, _, _ := pBitBlt.Call(hdcMem, 0, 0, uintptr(w), uintptr(h), hdcScreen,
		uintptr(int32(r.Min.X)), uintptr(int32(r.Min.Y)), srcCopy|captureBlt)
	if ok == 0 {
		return nil, errors.New("BitBlt failed")
	}
	return copyPixels(bits, w, h), nil
}

// createDIBSection creates a 32bpp top-down BGRA DIB section of size w*h.
// The caller owns hBmp and must DeleteObject it after finishing with bits.
func createDIBSection(hdc uintptr, w, h int) (uintptr, unsafe.Pointer, error) {
	bi := bitmapInfo{
		Header: bitmapInfoHeader{
			Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			Width:       int32(w),
			Height:      -int32(h), // top-down
			Planes:      1,
			BitCount:    32,
			Compression: biRGB,
		},
	}
	var bits unsafe.Pointer
	hBmp, _, _ := pCreateDIBSection.Call(hdc, uintptr(unsafe.Pointer(&bi)), dibRGB,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hBmp == 0 || bits == nil {
		return 0, nil, errors.New("CreateDIBSection failed")
	}
	return hBmp, bits, nil
}

// copyPixels copies the DIB section's BGRA pixels into a new RGBA buffer.
func copyPixels(bits unsafe.Pointer, w, h int) []byte {
	src := unsafe.Slice((*byte)(bits), w*h*4)
	pixels := make([]byte, len(src))
	copy(pixels, src)
	for i := 0; i < len(pixels); i += 4 {
		pixels[i], pixels[i+2] = pixels[i+2], pixels[i]
	}
	return pixels
}

// Displays returns all enumerated displays.
func (c *Capturer) Displays() ([]capture.DisplayInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]capture.DisplayInfo, 0, len(c.monitors))
	for _, m := range c.monitors {
		dpi := dpiForMonitor(m.hMon)
		out = append(out, capture.DisplayInfo{
			ID: m.id, Name: m.name, Bounds: m.bounds, IsPrimary: m.isPrimary,
			DPI: float64(dpi), RefreshRate: 60,
		})
	}
	return out, nil
}

// Windows lists visible top-level windows.
func (c *Capturer) Windows() ([]capture.WindowInfo, error) {
	var list []capture.WindowInfo
	cb := syscall.NewCallback(func(hwnd HWND, _ uintptr) uintptr {
		if !isWindowVisible(hwnd) || !isValidAppWindow(hwnd) {
			return 1
		}
		r, err := getWindowRect(hwnd)
		if err != nil || r.Dx() < 10 || r.Dy() < 10 {
			return 1
		}
		pid := getWindowPID(hwnd)
		list = append(list, capture.WindowInfo{
			ID:          int(hwnd),
			Title:       getWindowText(hwnd),
			Bounds:      r,
			IsVisible:   true,
			ProcessName: getProcessName(pid),
			PID:         int(pid),
		})
		return 1
	})
	pEnumWindows.Call(cb, 0)
	return list, nil
}

// Close releases any held resources. The GDI backend caches none.
func (c *Capturer) Close() error { return nil }
