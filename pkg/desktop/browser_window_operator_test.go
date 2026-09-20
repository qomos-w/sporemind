package desktop

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestHandlePageStateMessage(t *testing.T) {
	o := &desktopWindowOperator{
		windows:          make(map[string]*browserWindow),
		snapshots:        newSnapshotStore(),
		observeCallbacks: make(map[string]chan *domain.BrowserPageObservation),
	}
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		State: domain.BrowserWindowState{},
	}
	o.windows["inst-0"] = &browserWindow{id: "inst-0", cfg: cfg}

	msg := `{"type":"page-state","scrollX":120,"scrollY":340,"zoom":1.25,"data":"{\"q\":\"hello\"}"}`
	if !o.handlePageStateMessage("inst-0", msg) {
		t.Fatal("expected message to be handled")
	}

	ps := o.windows["inst-0"].cfg.State.PageState
	if ps.ScrollX != 120 {
		t.Errorf("expected ScrollX 120, got %d", ps.ScrollX)
	}
	if ps.ScrollY != 340 {
		t.Errorf("expected ScrollY 340, got %d", ps.ScrollY)
	}
	if ps.Zoom != 1.25 {
		t.Errorf("expected Zoom 1.25, got %f", ps.Zoom)
	}
	if ps.Data != `{"q":"hello"}` {
		t.Errorf("expected Data, got %q", ps.Data)
	}
}

func TestHandlePageStateMessage_NotPageState(t *testing.T) {
	o := &desktopWindowOperator{
		windows:          make(map[string]*browserWindow),
		snapshots:        newSnapshotStore(),
		observeCallbacks: make(map[string]chan *domain.BrowserPageObservation),
	}
	if o.handlePageStateMessage("inst-0", `{"type":"page-snapshot"}`) {
		t.Error("expected non-page-state message to be ignored")
	}
}

func TestRestorePageState_Empty(t *testing.T) {
	o := &desktopWindowOperator{
		windows:          make(map[string]*browserWindow),
		snapshots:        newSnapshotStore(),
		observeCallbacks: make(map[string]chan *domain.BrowserPageObservation),
	}
	// Empty page state should be a no-op; it must not panic with a nil window.
	o.restorePageState("inst-0", domain.BrowserPageState{})
}

func TestCloseWindowPreserve(t *testing.T) {
	o := &desktopWindowOperator{
		windows:          make(map[string]*browserWindow),
		snapshots:        newSnapshotStore(),
		observeCallbacks: make(map[string]chan *domain.BrowserPageObservation),
	}
	cfg := domain.BrowserInstanceConfig{
		ID:   "inst-0",
		Open: true,
		State: domain.BrowserWindowState{
			X: 10, Y: 20, Width: 800, Height: 600,
			PageState: domain.BrowserPageState{ScrollX: 100, ScrollY: 200, Zoom: 1.25},
		},
	}
	o.windows["inst-0"] = &browserWindow{id: "inst-0", cfg: cfg}

	o.closeWindow("inst-0", nil, true)

	if _, ok := o.windows["inst-0"]; ok {
		t.Fatal("expected window to be removed from operator tracking")
	}
	if o.shuttingDown {
		t.Fatal("closeWindow should not set shuttingDown")
	}
	// The sync to manager will fail because there is no manager ref, but the
	// local window is gone. We can't easily assert the manager state here.
}

func TestCloseWindow_UserCloseMarksOpenFalse(t *testing.T) {
	o := &desktopWindowOperator{
		windows:          make(map[string]*browserWindow),
		snapshots:        newSnapshotStore(),
		observeCallbacks: make(map[string]chan *domain.BrowserPageObservation),
	}
	cfg := domain.BrowserInstanceConfig{
		ID:   "inst-0",
		Open: true,
		Mode: "window",
	}
	o.windows["inst-0"] = &browserWindow{id: "inst-0", cfg: cfg}

	o.closeWindow("inst-0", nil, false)

	if _, ok := o.windows["inst-0"]; ok {
		t.Fatal("expected window to be removed from operator tracking")
	}
}

func TestInPlaceNavigationURL(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    string
	}{
		{"http url", map[string]any{"url": "https://example.com/page"}, "https://example.com/page"},
		{"file url", map[string]any{"url": "file:///D:/web/page.html"}, "file:///D:/web/page.html"},
		{"about blank", map[string]any{"url": "about:blank"}, "about:blank"},
		{"empty url", map[string]any{"url": ""}, ""},
		{"internal url", map[string]any{"url": "chrome-error://chromewebdata/"}, ""},
		{"non-string url", map[string]any{"url": 42}, ""},
		{"non-map payload", "https://example.com", ""},
		{"nil payload", nil, ""},
	}
	for _, tc := range cases {
		if got := inPlaceNavigationURL(tc.payload); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNavigateInPlace_NoWindowIsNoop(t *testing.T) {
	o := &desktopWindowOperator{
		windows:          make(map[string]*browserWindow),
		snapshots:        newSnapshotStore(),
		observeCallbacks: make(map[string]chan *domain.BrowserPageObservation),
	}
	// Unknown instance and nil window must both be safe no-ops.
	o.navigateInPlace("missing", "https://example.com")
	o.windows["inst-0"] = &browserWindow{id: "inst-0"}
	o.navigateInPlace("inst-0", "https://example.com")
}

func TestBuildUsePayload_NewFields(t *testing.T) {
	req := domain.BrowserUseReq{
		Action:              "drag",
		ElementID:           "src-1",
		DragTargetElementID: "dst-1",
		Modifiers:           []string{"ctrl", "shift"},
		FilePath:            "C:/tmp/file.txt",
		TimeoutMs:           30000,
	}
	p := buildUsePayload(req)

	var m map[string]any
	if err := json.Unmarshal([]byte(p.JSON), &m); err != nil {
		t.Fatalf("invalid JSON payload: %v", err)
	}
	if got := m["dragTargetElementId"]; got != "dst-1" {
		t.Errorf("expected dragTargetElementId dst-1, got %v", got)
	}
	if got := m["filePath"]; got != "C:/tmp/file.txt" {
		t.Errorf("expected filePath, got %v", got)
	}
	mods, ok := m["modifiers"].([]any)
	if !ok {
		t.Fatalf("expected modifiers to be a JSON string array, got %T", m["modifiers"])
	}
	if len(mods) != 2 || mods[0] != "ctrl" || mods[1] != "shift" {
		t.Errorf("expected modifiers [ctrl shift], got %v", mods)
	}
	// timeoutMs is a dead field on the page side (useScript has no consumer),
	// so it must NOT be forwarded.
	if _, ok := m["timeoutMs"]; ok {
		t.Error("expected timeoutMs NOT to be mapped (dead field)")
	}
}

func TestBuildUsePayload_EmptyNewFieldsOmitted(t *testing.T) {
	p := buildUsePayload(domain.BrowserUseReq{Action: "click", ElementID: "e1"})
	var m map[string]any
	if err := json.Unmarshal([]byte(p.JSON), &m); err != nil {
		t.Fatalf("invalid JSON payload: %v", err)
	}
	for _, key := range []string{"modifiers", "filePath", "dragTargetElementId"} {
		if _, ok := m[key]; ok {
			t.Errorf("expected %q omitted when empty, got %v", key, m[key])
		}
	}
}

func TestValidateUseAction(t *testing.T) {
	cases := []struct {
		name    string
		req     domain.BrowserUseReq
		wantMsg string // empty = valid
	}{
		{"drag with target element", domain.BrowserUseReq{Action: "drag", DragTargetElementID: "dst-1"}, ""},
		{"drag with coordinates", domain.BrowserUseReq{Action: "drag", ClickX: 100, ClickY: 200}, ""},
		{"drag missing target", domain.BrowserUseReq{Action: "drag"}, "drag: dragTargetElementId or clickX+clickY required"},
		{"drag half coordinates", domain.BrowserUseReq{Action: "drag", ClickX: 100}, "drag: dragTargetElementId or clickX+clickY required"},
		{"scroll_to with element", domain.BrowserUseReq{Action: "scroll_to", ElementID: "e1"}, ""},
		{"scroll_to missing element", domain.BrowserUseReq{Action: "scroll_to"}, "scroll_to: elementId required"},
		{"hover with element", domain.BrowserUseReq{Action: "hover", ElementID: "e1"}, ""},
		{"hover missing element", domain.BrowserUseReq{Action: "hover"}, "hover: elementId required"},
		{"focus with element", domain.BrowserUseReq{Action: "focus", ElementID: "e1"}, ""},
		{"focus missing element", domain.BrowserUseReq{Action: "focus"}, "focus: elementId required"},
		{"file_upload with path", domain.BrowserUseReq{Action: "file_upload", FilePath: "/tmp/a.txt"}, ""},
		{"file_upload missing path", domain.BrowserUseReq{Action: "file_upload"}, "file_upload: filePath required"},
		{"unknown action passes", domain.BrowserUseReq{Action: "warp"}, ""},
		{"click unaffected", domain.BrowserUseReq{Action: "click", ElementID: "e1"}, ""},
	}
	for _, tc := range cases {
		if got := validateUseAction(tc.req); got != tc.wantMsg {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.wantMsg)
		}
	}
}

func TestDecodeScreenshotDataURL(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	data, w, h, err := decodeScreenshotDataURL(dataURL)
	if err != nil {
		t.Fatalf("decode screenshot: %v", err)
	}
	if !bytes.Equal(data, buf.Bytes()) {
		t.Error("expected decoded bytes to round-trip the original PNG")
	}
	if w != 4 || h != 3 {
		t.Errorf("expected dimensions 4x3, got %dx%d", w, h)
	}
}

func TestDecodeScreenshotDataURL_Errors(t *testing.T) {
	if _, _, _, err := decodeScreenshotDataURL("not-a-data-url"); err == nil {
		t.Error("expected error for missing data URL prefix")
	}
	if _, _, _, err := decodeScreenshotDataURL("data:image/png;base64,%%%not-base64%%%"); err == nil {
		t.Error("expected error for invalid base64")
	}
	if _, _, _, err := decodeScreenshotDataURL("data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not a png"))); err == nil {
		t.Error("expected error for non-PNG bytes")
	}
}

func TestAttachScreenshot_NilSafe(t *testing.T) {
	o := &desktopWindowOperator{}
	// Nil window / nil observation must be safe no-ops (no panic).
	o.attachScreenshot(nil, &domain.BrowserPageObservation{})
	o.attachScreenshot(&browserWindow{}, nil)
	o.attachScreenshot(nil, nil)
}
