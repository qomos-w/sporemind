// Package capture provides cross-platform screen capture and input simulation
// for the computeruse actor. Windows uses GDI (BitBlt + PrintWindow); other
// platforms return a "not implemented" error.
package capture

import (
	"image"
	"time"
)

// CaptureMode selects what area to capture.
type CaptureMode int

const (
	ModeFull CaptureMode = iota
	ModeRegion
	ModeWindow
	ModeDelta
)

// Frame is one captured screen image plus metadata.
type Frame struct {
	// Data is the encoded image bytes (JPEG/PNG).
	Data []byte
	// RawPixels is the original RGBA pixel buffer (row-major, 4 bytes per pixel).
	RawPixels []byte
	// Bounds is the image rectangle in screen coordinates (Min may be non-zero
	// when this frame is a region crop).
	Bounds image.Rectangle
	// Timestamp is when this frame was captured.
	Timestamp time.Time
	// DisplayID is the source display index, or -1 for window captures.
	DisplayID int
	// CursorPos is the cursor position at capture time, in screen coords.
	CursorPos image.Point
	// CursorValid is true when CursorPos was successfully queried at capture
	// time. It is false when the platform could not report a position. A zero
	// CursorPos with CursorValid=true legitimately means the cursor is at the
	// screen origin (0,0), so callers must gate on CursorValid, not on
	// non-zero coordinates.
	CursorValid bool
	// HasCursor is true when the cursor was painted onto the frame.
	HasCursor bool
	// EncodingFormat is "jpeg", "png", or "raw".
	EncodingFormat string
	// EncodingQuality is the JPEG quality used (0 for lossless).
	EncodingQuality int
	// FrameIndex is a monotonically increasing per-capturer counter.
	FrameIndex uint64
	// Duration is how long the capture took.
	Duration time.Duration
	// IsIdentical is true when delta mode detected no change.
	IsIdentical bool
}

// WindowInfo describes a single visible window.
type WindowInfo struct {
	ID          int
	Title       string
	Bounds      image.Rectangle
	IsVisible   bool
	ProcessName string
	PID         int
}

// DetailedWindowInfo extends WindowInfo with state + flags.
type DetailedWindowInfo struct {
	WindowInfo
	State        string // "normal" | "minimized" | "maximized"
	ClassName    string
	IsForeground bool
	IsTopmost    bool
}

// ProcessInfo describes one OS process.
type ProcessInfo struct {
	PID       int
	ParentPID int
	Name      string
	ExePath   string
}

// WindowFilter filters the result of ListWindows().
type WindowFilter struct {
	PID              int
	ProcessName      string
	TitlePattern     string
	ForegroundOnly   bool
	AtPoint          *image.Point
	IncludeInvisible bool
}

// DisplayInfo describes one connected display.
type DisplayInfo struct {
	ID          int
	Name        string
	Bounds      image.Rectangle
	IsPrimary   bool
	DPI         float64
	RefreshRate int
}

// ElementType categorizes a detected UI element for annotation.
type ElementType string

const (
	ElementButton ElementType = "button"
	ElementInput  ElementType = "input"
	ElementLink   ElementType = "link"
	ElementText   ElementType = "text"
	ElementIcon   ElementType = "icon"
)

// ElementMark is a detected UI element annotated on a screenshot.
type ElementMark struct {
	ID     int
	Bounds image.Rectangle
	Type   ElementType
	Text   string
}

// ElementNode is a UI Automation element. Backends populate the fields
// they can resolve cheaply; missing fields return zero values.
type ElementNode struct {
	// ID is a backend-stable handle for the element. Pass it back to
	// element-targeted operations (click, focus, info, value). Lifetime
	// is bounded — backends may evict via LRU.
	ID string
	// AutomationID is the developer-assigned identifier (UIA AutomationId
	// property, usually stable across runs).
	AutomationID string
	// Name is the accessible name (UIA Name property).
	Name string
	// ClassName is the underlying widget class.
	ClassName string
	// ControlType is the high-level role: "button"/"text"/"edit"/"list"/...
	ControlType string
	// Bounds is the on-screen rectangle in physical pixels.
	Bounds image.Rectangle
	// IsEnabled / IsKeyboardFocusable / HasKeyboardFocus mirror UIA flags.
	IsEnabled           bool
	IsKeyboardFocusable bool
	HasKeyboardFocus    bool
	// Value is the current text content for value-pattern elements
	// (edit boxes, combo boxes); empty otherwise.
	Value string
	// HWND is the underlying window handle (0 for non-window elements).
	HWND uintptr
	// Children is populated for tree-walk results when depth > 0.
	Children []ElementNode
}

// ElementFilter selects which UI elements to enumerate.
type ElementFilter struct {
	// WindowID restricts the search to the given window's subtree. 0 = desktop.
	WindowID int
	// NameContains case-insensitively matches Name or AutomationID.
	NameContains string
	// ControlType filters by control type ("button", "edit", ...).
	ControlType string
	// MaxDepth limits tree-walk depth from the root. 0 = unbounded (capped at 8).
	MaxDepth int
	// MaxResults caps the result count (0 = backend default of 256).
	MaxResults int
}

// OcrLine is one line of recognised text with its bounding rectangle.
type OcrLine struct {
	Text       string
	Bounds     image.Rectangle
	Confidence float64
}

// OcrResult bundles OCR output across a captured region.
type OcrResult struct {
	Text   string
	Lines  []OcrLine
	Engine string
	Bounds image.Rectangle
}

// ClipboardData is a multi-format snapshot of the system clipboard. Each
// field is independent and set only when the corresponding format was
// present.
type ClipboardData struct {
	Kind  string // "text"|"image"|"files"|"empty"|"mixed"
	Text  string
	Image *image.RGBA
	Files []string
	HTML  string
}
