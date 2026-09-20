package capture

import (
	"fmt"
	"image"
	"runtime"
	"sync"
)

// Capturer is the cross-platform screen + input interface backing the
// computeruse actor.
type Capturer interface {
	// Capture takes a single screenshot.
	Capture(opts CaptureOptions) (*Frame, error)
	// CaptureWindow captures a specific window by ID (HWND on Windows).
	CaptureWindow(windowID int, opts CaptureOptions) (*Frame, error)
	// GetLastFrame returns the most recently captured frame (for delta mode).
	GetLastFrame() *Frame

	// Displays returns information about connected displays.
	Displays() ([]DisplayInfo, error)
	// Windows returns visible top-level windows.
	Windows() ([]WindowInfo, error)
	// FilteredWindows returns windows matching the filter.
	FilteredWindows(f WindowFilter) ([]WindowInfo, error)
	// WindowInfo returns detailed info for one window.
	WindowInfo(windowID int) (DetailedWindowInfo, error)
	// Processes returns OS process list (optionally filtered).
	Processes(nameContains string, pid int) ([]ProcessInfo, error)
	// FocusWindow brings a window to the foreground.
	FocusWindow(windowID int) error

	// Cursor / input.
	CursorPosition() (image.Point, error)
	MoveCursor(x, y int) error
	Click(x, y int, button string) error
	Scroll(x, y, dx, dy int) error
	Type(text string) error
	KeyPress(key string, modifiers ...string) error
	// Drag presses button at (fromX, fromY), moves to (toX, toY) via
	// SendInput batch, and releases. button: "left"|"right"|"middle".
	Drag(fromX, fromY, toX, toY int, button string) error
	// KeyDown holds modifiers then presses (down only) key. The caller is
	// responsible for issuing a matching KeyUp.
	KeyDown(key string, modifiers ...string) error
	// KeyUp releases key then releases modifiers in reverse order.
	KeyUp(key string, modifiers ...string) error

	// Window control.
	// SetWindowState changes show-state. state: "minimize"|"maximize"|"restore".
	SetWindowState(windowID int, state string) error
	// MoveWindow moves/resizes a window. x==y==0 keeps position; w==h==0 keeps size.
	MoveWindow(windowID int, x, y, width, height int) error
	// CloseWindow asks the window to close (WM_CLOSE — graceful, not kill).
	CloseWindow(windowID int) error

	// Clipboard.
	SetClipboard(text string) error
	GetClipboard() (string, error)

	// UI Automation. Backends without UIA support return ("", err).
	// ListElements enumerates the accessibility tree under filter.WindowID
	// (or the desktop when 0). Results are flattened depth-first.
	ListElements(f ElementFilter) ([]ElementNode, error)
	// ElementInfo returns the latest snapshot of one element by handle.
	ElementInfo(elementID string) (ElementNode, error)
	// ClickElement performs an Invoke / Toggle / SelectionItem.Select on
	// the element when the corresponding pattern is supported; otherwise
	// falls back to a coordinate click on the element's bounding rect.
	ClickElement(elementID, button string) error
	// FocusElement requests keyboard focus on the element.
	FocusElement(elementID string) error
	// SetElementValue replaces the text of value-pattern elements (edit
	// boxes, combo boxes). Returns an error when the element is read-only.
	SetElementValue(elementID, value string) error

	// OCR runs text recognition over the given image region. It tries the
	// embedded PaddleOCR engine first, then falls back to tesseract.
	// Returns an "ocr not available" error when no engine is reachable.
	OCR(pixels []byte, width, height int, language string) (OcrResult, error)

	// GetClipboardData returns every format present on the system clipboard
	// in one shot. Implementations may return only text on platforms without
	// full clipboard support.
	GetClipboardData() (ClipboardData, error)
	// SetClipboardData replaces the clipboard with one of the fields in d.
	// Precedence: Image > Files > HTML > Text.
	SetClipboardData(d ClipboardData) error

	// Capabilities returns the backend's self-declared support matrix
	// (features + interact actions). The actor surfaces it via
	// computeruse.capabilities so agents can avoid invoking actions that
	// would fail on the current platform.
	Capabilities() CapabilitySet

	// Close releases all resources.
	Close() error
}

// New returns a platform-specific Capturer.
func New() (Capturer, error) {
	switch runtime.GOOS {
	case "windows":
		if NewWindowsCapturer == nil {
			return nil, fmt.Errorf("windows capturer not registered")
		}
		return NewWindowsCapturer()
	case "linux":
		if NewLinuxCapturer == nil {
			return nil, fmt.Errorf("linux capturer not registered")
		}
		return NewLinuxCapturer()
	case "darwin":
		if NewDarwinCapturer == nil {
			return nil, fmt.Errorf("darwin capturer not registered")
		}
		return NewDarwinCapturer()
	default:
		return nil, fmt.Errorf("computeruse: unsupported platform: %s", runtime.GOOS)
	}
}

// BaseCapturer provides shared frame-tracking state that platform
// implementations embed.
type BaseCapturer struct {
	mu          sync.RWMutex
	frameIndex  uint64
	lastFrame   *Frame
	cursorTrail []image.Point
}

func (b *BaseCapturer) NextFrameIndex() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.frameIndex++
	return b.frameIndex
}

func (b *BaseCapturer) GetLastFrame() *Frame {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastFrame
}

func (b *BaseCapturer) SetLastFrame(f *Frame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastFrame = f
}

func (b *BaseCapturer) TrackCursor(pt image.Point) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cursorTrail = append(b.cursorTrail, pt)
	if len(b.cursorTrail) > 100 {
		b.cursorTrail = b.cursorTrail[len(b.cursorTrail)-100:]
	}
}

func (b *BaseCapturer) GetCursorTrail(length int) []image.Point {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if length <= 0 || len(b.cursorTrail) == 0 {
		return nil
	}
	if len(b.cursorTrail) <= length {
		out := make([]image.Point, len(b.cursorTrail))
		copy(out, b.cursorTrail)
		return out
	}
	out := make([]image.Point, length)
	copy(out, b.cursorTrail[len(b.cursorTrail)-length:])
	return out
}
