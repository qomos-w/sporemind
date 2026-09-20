package computeruse

import (
	"fmt"
	"image"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
)

// fakeCapturer records every interaction and returns canned data. It lets
// the actor integration tests run on any platform without a real display.
type fakeCapturer struct {
	// Canned state.
	cursor    image.Point
	displays  []capture.DisplayInfo
	windows   []capture.WindowInfo
	processes []capture.ProcessInfo
	elements  []capture.ElementNode
	clipText  string
	clipData  capture.ClipboardData

	// Pixel buffer returned by Capture. If nil, a default 4x4 RGBA buffer is used.
	captureFrame *capture.Frame

	// Whether to fail next Capture call.
	captureErr error

	// Call log — each operation pushes a string for assertion.
	calls []string
}

func newFakeCapturer() *fakeCapturer {
	return &fakeCapturer{
		cursor: image.Point{X: 100, Y: 200},
		displays: []capture.DisplayInfo{
			{ID: 0, Name: "Display0", Bounds: image.Rect(0, 0, 1920, 1080), IsPrimary: true},
			{ID: 1, Name: "Display1", Bounds: image.Rect(1920, 0, 3840, 1080)},
		},
		windows: []capture.WindowInfo{
			{ID: 1001, Title: "Notepad", Bounds: image.Rect(100, 100, 700, 500), IsVisible: true, ProcessName: "notepad.exe", PID: 4242},
			{ID: 1002, Title: "Browser - Example", Bounds: image.Rect(800, 100, 1600, 900), IsVisible: true, ProcessName: "chrome.exe", PID: 4343},
		},
		processes: []capture.ProcessInfo{
			{PID: 4242, Name: "notepad.exe", ExePath: "C:\\Windows\\notepad.exe"},
			{PID: 4343, Name: "chrome.exe", ExePath: "C:\\Program Files\\Google\\chrome.exe"},
		},
		elements: []capture.ElementNode{
			{ID: "el-1", AutomationID: "btn-ok", Name: "OK", ControlType: "button", Bounds: image.Rect(120, 120, 200, 150), IsEnabled: true},
		},
	}
}

func (f *fakeCapturer) log(op string) { f.calls = append(f.calls, op) }

func (f *fakeCapturer) Capture(opts capture.CaptureOptions) (*capture.Frame, error) {
	f.log(fmt.Sprintf("Capture mode=%d display=%d region=%v", opts.Mode, opts.DisplayID, opts.Region))
	if f.captureErr != nil {
		return nil, f.captureErr
	}
	if f.captureFrame != nil {
		return f.captureFrame, nil
	}
	w, h := 4, 4
	rect := image.Rect(0, 0, w, h)
	if !opts.Region.Empty() {
		rect = opts.Region
		w, h = rect.Dx(), rect.Dy()
	}
	pix := make([]byte, w*h*4)
	for i := 0; i < len(pix); i += 4 {
		pix[i+0] = 10
		pix[i+1] = 20
		pix[i+2] = 30
		pix[i+3] = 255
	}
	return &capture.Frame{
		RawPixels:      pix,
		Data:           pix,
		Bounds:         rect,
		EncodingFormat: opts.EncodeFormat,
		CursorPos:      f.cursor,
		CursorValid:    true, // the fake always reports a valid cursor position
	}, nil
}

func (f *fakeCapturer) CaptureWindow(windowID int, opts capture.CaptureOptions) (*capture.Frame, error) {
	f.log(fmt.Sprintf("CaptureWindow id=%d", windowID))
	return f.Capture(opts)
}

func (f *fakeCapturer) GetLastFrame() *capture.Frame { return nil }

func (f *fakeCapturer) Displays() ([]capture.DisplayInfo, error) {
	f.log("Displays")
	return f.displays, nil
}

func (f *fakeCapturer) Windows() ([]capture.WindowInfo, error) {
	f.log("Windows")
	return f.windows, nil
}

func (f *fakeCapturer) FilteredWindows(filter capture.WindowFilter) ([]capture.WindowInfo, error) {
	f.log(fmt.Sprintf("FilteredWindows pid=%d title=%q", filter.PID, filter.TitlePattern))
	return f.windows, nil
}

func (f *fakeCapturer) WindowInfo(windowID int) (capture.DetailedWindowInfo, error) {
	f.log(fmt.Sprintf("WindowInfo id=%d", windowID))
	for _, w := range f.windows {
		if w.ID == windowID {
			return capture.DetailedWindowInfo{
				WindowInfo:   w,
				State:        "normal",
				ClassName:    "TestClass",
				IsForeground: false,
				IsTopmost:    false,
			}, nil
		}
	}
	return capture.DetailedWindowInfo{}, fmt.Errorf("window not found: %d", windowID)
}

func (f *fakeCapturer) Processes(nameContains string, pid int) ([]capture.ProcessInfo, error) {
	f.log(fmt.Sprintf("Processes name=%q pid=%d", nameContains, pid))
	return f.processes, nil
}

func (f *fakeCapturer) FocusWindow(windowID int) error {
	f.log(fmt.Sprintf("FocusWindow id=%d", windowID))
	return nil
}

func (f *fakeCapturer) CursorPosition() (image.Point, error) {
	f.log("CursorPosition")
	return f.cursor, nil
}

func (f *fakeCapturer) MoveCursor(x, y int) error {
	f.log(fmt.Sprintf("MoveCursor x=%d y=%d", x, y))
	f.cursor = image.Point{X: x, Y: y}
	return nil
}

func (f *fakeCapturer) Click(x, y int, button string) error {
	f.log(fmt.Sprintf("Click x=%d y=%d btn=%s", x, y, button))
	return nil
}

func (f *fakeCapturer) Scroll(x, y, dx, dy int) error {
	f.log(fmt.Sprintf("Scroll x=%d y=%d dx=%d dy=%d", x, y, dx, dy))
	return nil
}

func (f *fakeCapturer) Type(text string) error {
	f.log(fmt.Sprintf("Type %q", text))
	return nil
}

func (f *fakeCapturer) KeyPress(key string, modifiers ...string) error {
	f.log(fmt.Sprintf("KeyPress key=%s mods=%v", key, modifiers))
	return nil
}

func (f *fakeCapturer) Drag(fromX, fromY, toX, toY int, button string) error {
	f.log(fmt.Sprintf("Drag (%d,%d)->(%d,%d) btn=%s", fromX, fromY, toX, toY, button))
	return nil
}

func (f *fakeCapturer) KeyDown(key string, modifiers ...string) error {
	f.log(fmt.Sprintf("KeyDown key=%s mods=%v", key, modifiers))
	return nil
}

func (f *fakeCapturer) KeyUp(key string, modifiers ...string) error {
	f.log(fmt.Sprintf("KeyUp key=%s mods=%v", key, modifiers))
	return nil
}

func (f *fakeCapturer) SetWindowState(windowID int, state string) error {
	f.log(fmt.Sprintf("SetWindowState id=%d state=%s", windowID, state))
	return nil
}

func (f *fakeCapturer) MoveWindow(windowID int, x, y, w, h int) error {
	f.log(fmt.Sprintf("MoveWindow id=%d %d,%d %dx%d", windowID, x, y, w, h))
	return nil
}

func (f *fakeCapturer) CloseWindow(windowID int) error {
	f.log(fmt.Sprintf("CloseWindow id=%d", windowID))
	return nil
}

func (f *fakeCapturer) SetClipboard(text string) error {
	f.log(fmt.Sprintf("SetClipboard %q", text))
	f.clipText = text
	return nil
}

func (f *fakeCapturer) GetClipboard() (string, error) {
	f.log("GetClipboard")
	return f.clipText, nil
}

func (f *fakeCapturer) ListElements(filter capture.ElementFilter) ([]capture.ElementNode, error) {
	f.log(fmt.Sprintf("ListElements win=%d name=%q", filter.WindowID, filter.NameContains))
	return f.elements, nil
}

func (f *fakeCapturer) ElementInfo(elementID string) (capture.ElementNode, error) {
	f.log(fmt.Sprintf("ElementInfo id=%s", elementID))
	for _, e := range f.elements {
		if e.ID == elementID {
			return e, nil
		}
	}
	return capture.ElementNode{}, fmt.Errorf("element not found: %s", elementID)
}

func (f *fakeCapturer) ClickElement(elementID, button string) error {
	f.log(fmt.Sprintf("ClickElement id=%s btn=%s", elementID, button))
	return nil
}

func (f *fakeCapturer) FocusElement(elementID string) error {
	f.log(fmt.Sprintf("FocusElement id=%s", elementID))
	return nil
}

func (f *fakeCapturer) SetElementValue(elementID, value string) error {
	f.log(fmt.Sprintf("SetElementValue id=%s val=%q", elementID, value))
	return nil
}

func (f *fakeCapturer) OCR(pixels []byte, width, height int, language string) (capture.OcrResult, error) {
	f.log(fmt.Sprintf("OCR %dx%d lang=%s", width, height, language))
	return capture.OcrResult{
		Text:   "hello world",
		Engine: "fake",
		Lines: []capture.OcrLine{
			{Text: "hello world", Bounds: image.Rect(0, 0, width, height), Confidence: 0.99},
		},
	}, nil
}

func (f *fakeCapturer) GetClipboardData() (capture.ClipboardData, error) {
	f.log("GetClipboardData")
	return f.clipData, nil
}

func (f *fakeCapturer) SetClipboardData(d capture.ClipboardData) error {
	f.log(fmt.Sprintf("SetClipboardData kind=%s", d.Kind))
	f.clipData = d
	return nil
}

func (f *fakeCapturer) Capabilities() capture.CapabilitySet {
	f.log("Capabilities")
	return capture.BuildCapabilities(capture.CapabilitySpec{
		Backend: "fake",
		Features: []capture.Capability{
			{Key: "feature.element_tree", Supported: true},
			{Key: "feature.rich_clipboard", Supported: true},
			{Key: "feature.multi_display", Supported: true},
		},
		UnsupportedActions: map[string]string{
			"middle_click": "fake backend has no middle button",
		},
	})
}

func (f *fakeCapturer) Close() error {
	f.log("Close")
	return nil
}

// Ensure interface compliance.
var _ capture.Capturer = (*fakeCapturer)(nil)
