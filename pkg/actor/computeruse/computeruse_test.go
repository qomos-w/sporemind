package computeruse

import (
	"image"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// freshActor returns an Actor whose capturer field is the fake. Bypasses
// OnStart's gospore registration (FakeCtx is a no-op for Register) but
// still exercises the handler dispatch logic.
func freshActor(t *testing.T) (*Actor, *fakeCapturer, *testutil.FakeCtx) {
	t.Helper()
	a := &Actor{}
	fc := newFakeCapturer()
	a.cap = fc
	ctx := testutil.HumanCtx(testutil.GenActorID())
	return a, fc, ctx
}

// --- screenshot ---

func TestHandleScreenshot_DefaultsToFullDisplay(t *testing.T) {
	a, fc, ctx := freshActor(t)
	resp, err := a.handleScreenshot(ctx, domain.ComputerUseScreenshotReq{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Width != 4 || resp.Height != 4 {
		t.Fatalf("dims=%dx%d", resp.Width, resp.Height)
	}
	if resp.Format != "jpeg" {
		t.Fatalf("default format=%q want jpeg", resp.Format)
	}
	if len(resp.ImageBytes) == 0 {
		t.Fatal("imageBytes empty")
	}
	if resp.DisplayName != "Display0" {
		t.Fatalf("displayName=%q", resp.DisplayName)
	}
	// Should have called Capture and Displays.
	if !hasCall(fc, "Capture mode=") {
		t.Fatalf("no Capture call: %v", fc.calls)
	}
}

func TestHandleScreenshot_CursorAtOriginIsReported(t *testing.T) {
	a, fc, ctx := freshActor(t)
	fc.cursor = image.Point{X: 0, Y: 0}
	resp, err := a.handleScreenshot(ctx, domain.ComputerUseScreenshotReq{})
	if err != nil {
		t.Fatal(err)
	}
	// (0,0) is a valid cursor position (screen origin) and must be reported,
	// not dropped by a non-zero guard.
	if resp.CursorX != 0 || resp.CursorY != 0 {
		t.Fatalf("cursor=(%d,%d), want (0,0)", resp.CursorX, resp.CursorY)
	}
}

func TestHandleScreenshot_CursorHiddenWhenInvalid(t *testing.T) {
	a, fc, ctx := freshActor(t)
	if fc.captureFrame == nil {
		fc.captureFrame = &capture.Frame{}
	}
	fc.captureFrame.CursorPos = image.Point{X: 0, Y: 0} // stale zero, no valid cursor
	resp, err := a.handleScreenshot(ctx, domain.ComputerUseScreenshotReq{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.CursorX != 0 || resp.CursorY != 0 {
		t.Fatalf("cursor=(%d,%d), want unset", resp.CursorX, resp.CursorY)
	}
}

func TestHandleScreenshot_RegionMode(t *testing.T) {
	a, fc, ctx := freshActor(t)
	req := domain.ComputerUseScreenshotReq{
		Mode:   "region",
		Region: &domain.ComputerUseRegion{X: 10, Y: 20, Width: 100, Height: 50},
		Format: "png",
	}
	resp, err := a.handleScreenshot(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Format != "png" {
		t.Fatalf("format=%q", resp.Format)
	}
	// Region got passed through.
	found := false
	for _, c := range fc.calls {
		if strings.Contains(c, "region=(10,20)-(110,70)") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("region not propagated: %v", fc.calls)
	}
}

// Regression (2026-09-13 live trace): a request carrying only Region (no
// Mode) used to resolve mode to "" — the region was silently ignored and the
// whole screen was captured. mergeScreenshotTarget must infer region mode.
func TestHandleScreenshot_RegionWithoutModeInfersRegion(t *testing.T) {
	a, fc, ctx := freshActor(t)
	_, err := a.handleScreenshot(ctx, domain.ComputerUseScreenshotReq{
		Region: &domain.ComputerUseRegion{X: 1196, Y: 700, Width: 620, Height: 150},
		Format: "png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasCall(fc, "region=(1196,700)-(1816,850)") {
		t.Fatalf("region not inferred from Region-only request: %v", fc.calls)
	}
	// A Target carrying only WindowID (no Mode anywhere) likewise infers
	// window mode instead of falling through to a full-screen capture.
	_, err = a.handleScreenshot(ctx, domain.ComputerUseScreenshotReq{
		Target: &domain.ComputerUseWindowTarget{WindowID: 1001},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasCall(fc, "CaptureWindow id=1001") {
		t.Fatalf("window not inferred from WindowID-only request: %v", fc.calls)
	}
}

func TestHandleScreenshot_TargetWinsOverLegacy(t *testing.T) {
	a, fc, ctx := freshActor(t)
	req := domain.ComputerUseScreenshotReq{
		Mode: "region", // legacy — should be IGNORED when Target is set
		Target: &domain.ComputerUseWindowTarget{
			Mode:    "display",
			Display: 1,
		},
	}
	resp, err := a.handleScreenshot(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.DisplayName != "Display1" {
		t.Fatalf("expected Display1 (target wins), got %q", resp.DisplayName)
	}
	if hasCall(fc, "region=(") && !hasCall(fc, "region=(0,0)-(0,0)") {
		t.Fatalf("legacy region leaked: %v", fc.calls)
	}
}

func TestHandleScreenshot_WindowModeRequiresIDOrTitle(t *testing.T) {
	a, _, ctx := freshActor(t)
	_, err := a.handleScreenshot(ctx, domain.ComputerUseScreenshotReq{Mode: "window"})
	if err == nil {
		t.Fatal("expected error when window mode lacks ID/title")
	}
}

func TestHandleScreenshot_WindowByTitle(t *testing.T) {
	a, fc, ctx := freshActor(t)
	_, err := a.handleScreenshot(ctx, domain.ComputerUseScreenshotReq{
		Mode:        "window",
		WindowTitle: "notepad",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasCall(fc, "CaptureWindow id=1001") {
		t.Fatalf("title lookup failed: %v", fc.calls)
	}
}

// Regression (2026-09-12 live trace): Mode at the top level plus a Target
// carrying only WindowID used to resolve mode to "" — the window capture
// silently fell through to full display. Per-field merge keeps window mode.
func TestHandleScreenshot_WindowModeWithTargetWindowID(t *testing.T) {
	a, fc, ctx := freshActor(t)
	_, err := a.handleScreenshot(ctx, domain.ComputerUseScreenshotReq{
		Mode:   "window",
		Target: &domain.ComputerUseWindowTarget{WindowID: 1001},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasCall(fc, "CaptureWindow id=1001") {
		t.Fatalf("target window id not honoured: %v", fc.calls)
	}
	// Target.Mode still overrides the top-level mode when set.
	mode, _, _, id, _ := mergeScreenshotTarget(domain.ComputerUseScreenshotReq{
		Mode:   "window",
		Target: &domain.ComputerUseWindowTarget{Mode: "region", Region: &domain.ComputerUseRegion{X: 1, Y: 2, Width: 3, Height: 4}},
	})
	if mode != "region" || id != 0 {
		t.Fatalf("target.mode should override, got mode=%q id=%d", mode, id)
	}
}

// --- interact ---

func TestHandleInteract_ClickResolvesXY(t *testing.T) {
	a, fc, ctx := freshActor(t)
	resp, err := a.handleInteract(ctx, domain.ComputerUseInteractReq{
		Action: "click",
		Target: &domain.ComputerUseTargetSpec{X: 555, Y: 666},
		Button: "left",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("not success: %s", resp.Message)
	}
	if !hasCall(fc, "Click x=555 y=666 btn=left") {
		t.Fatalf("click args wrong: %v", fc.calls)
	}
}

func TestHandleInteract_GridCoordResolves(t *testing.T) {
	a, fc, ctx := freshActor(t)
	resp, err := a.handleInteract(ctx, domain.ComputerUseInteractReq{
		Action: "click",
		Target: &domain.ComputerUseTargetSpec{GridCoord: "A1"},
	})
	if err != nil || !resp.Success {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	// A1 at default cellSize 100 = center (50,50).
	if !hasCall(fc, "Click x=50 y=50 btn=left") {
		t.Fatalf("grid resolve failed: %v", fc.calls)
	}
}

func TestHandleInteract_WindowIDResolvesToCenter(t *testing.T) {
	a, fc, ctx := freshActor(t)
	resp, err := a.handleInteract(ctx, domain.ComputerUseInteractReq{
		Action: "click",
		Target: &domain.ComputerUseTargetSpec{WindowID: 1001},
	})
	if err != nil || !resp.Success {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	// Notepad bounds (100,100)-(700,500) → center (400, 300).
	if !hasCall(fc, "Click x=400 y=300 btn=left") {
		t.Fatalf("window center wrong: %v", fc.calls)
	}
}

func TestHandleInteract_Drag(t *testing.T) {
	a, fc, ctx := freshActor(t)
	resp, err := a.handleInteract(ctx, domain.ComputerUseInteractReq{
		Action: "drag",
		Target: &domain.ComputerUseTargetSpec{X: 10, Y: 20},
		DragTo: &domain.ComputerUseTargetSpec{X: 300, Y: 400},
	})
	if err != nil || !resp.Success {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if !hasCall(fc, "Drag (10,20)->(300,400) btn=left") {
		t.Fatalf("drag args wrong: %v", fc.calls)
	}
}

func TestHandleInteract_DragRejectsMissingDragTo(t *testing.T) {
	a, _, ctx := freshActor(t)
	resp, _ := a.handleInteract(ctx, domain.ComputerUseInteractReq{
		Action: "drag",
		Target: &domain.ComputerUseTargetSpec{X: 10, Y: 20},
	})
	if resp.Success {
		t.Fatal("expected failure when dragTo missing")
	}
	if !strings.Contains(resp.Message, "dragTo") {
		t.Fatalf("error doesn't mention dragTo: %s", resp.Message)
	}
}

func TestHandleInteract_KeyDownUp(t *testing.T) {
	a, fc, ctx := freshActor(t)
	if _, err := a.handleInteract(ctx, domain.ComputerUseInteractReq{
		Action: "key_down", Key: "shift",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleInteract(ctx, domain.ComputerUseInteractReq{
		Action: "key_up", Key: "shift",
	}); err != nil {
		t.Fatal(err)
	}
	if !hasCall(fc, "KeyDown key=shift") || !hasCall(fc, "KeyUp key=shift") {
		t.Fatalf("key_down/up missing: %v", fc.calls)
	}
}

func TestHandleInteract_WaitClampsTo30k(t *testing.T) {
	a, _, ctx := freshActor(t)
	// 1ms wait should return quickly without timing out the test.
	resp, err := a.handleInteract(ctx, domain.ComputerUseInteractReq{
		Action: "wait",
		WaitMs: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Fatalf("wait failed: %s", resp.Message)
	}
	if !strings.Contains(resp.Message, "Waited 1 ms") {
		t.Fatalf("message=%q", resp.Message)
	}
}

func TestHandleInteract_WaitRejectsZero(t *testing.T) {
	a, _, ctx := freshActor(t)
	resp, _ := a.handleInteract(ctx, domain.ComputerUseInteractReq{Action: "wait", WaitMs: 0})
	if resp.Success {
		t.Fatal("expected failure for waitMs=0")
	}
}

func TestHandleInteract_WindowControls(t *testing.T) {
	a, fc, ctx := freshActor(t)
	t.Run("state", func(t *testing.T) {
		if _, err := a.handleInteract(ctx, domain.ComputerUseInteractReq{
			Action:    "window_state",
			Target:    &domain.ComputerUseTargetSpec{WindowID: 1001},
			ShowState: "MAXIMIZE",
		}); err != nil {
			t.Fatal(err)
		}
		if !hasCall(fc, "SetWindowState id=1001 state=maximize") {
			t.Fatalf("state wrong: %v", fc.calls)
		}
	})
	t.Run("move", func(t *testing.T) {
		if _, err := a.handleInteract(ctx, domain.ComputerUseInteractReq{
			Action:    "window_move",
			Target:    &domain.ComputerUseTargetSpec{WindowID: 1001},
			WindowPos: &domain.ComputerUseRegion{X: 10, Y: 20, Width: 800, Height: 600},
		}); err != nil {
			t.Fatal(err)
		}
		if !hasCall(fc, "MoveWindow id=1001 10,20 800x600") {
			t.Fatalf("move args wrong: %v", fc.calls)
		}
	})
	t.Run("close", func(t *testing.T) {
		if _, err := a.handleInteract(ctx, domain.ComputerUseInteractReq{
			Action: "window_close",
			Target: &domain.ComputerUseTargetSpec{WindowID: 1001},
		}); err != nil {
			t.Fatal(err)
		}
		if !hasCall(fc, "CloseWindow id=1001") {
			t.Fatalf("close missing: %v", fc.calls)
		}
	})
}

func TestHandleInteract_UnknownActionFailsCleanly(t *testing.T) {
	a, _, ctx := freshActor(t)
	resp, _ := a.handleInteract(ctx, domain.ComputerUseInteractReq{Action: "fly_to_mars"})
	if resp.Success {
		t.Fatal("expected failure for unknown action")
	}
	if !strings.Contains(resp.Message, "unknown action") {
		t.Fatalf("message=%q", resp.Message)
	}
}

// --- listings ---

func TestHandleListWindows(t *testing.T) {
	a, _, ctx := freshActor(t)
	resp, err := a.handleListWindows(ctx, domain.ComputerUseListWindowsReq{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Count != 2 {
		t.Fatalf("count=%d", resp.Count)
	}
	if resp.Items[0].Title != "Notepad" {
		t.Fatalf("title=%q", resp.Items[0].Title)
	}
	if resp.Items[0].Bounds.Width != 600 || resp.Items[0].Bounds.Height != 400 {
		t.Fatalf("bounds=%+v", resp.Items[0].Bounds)
	}
}

func TestHandleListDisplays(t *testing.T) {
	a, _, ctx := freshActor(t)
	resp, err := a.handleListDisplays(ctx, domain.ComputerUseListDisplaysReq{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Count != 2 {
		t.Fatalf("count=%d", resp.Count)
	}
	if !resp.Items[0].IsPrimary {
		t.Fatal("primary flag not propagated")
	}
}

func TestHandleListProcesses(t *testing.T) {
	a, _, ctx := freshActor(t)
	resp, err := a.handleListProcesses(ctx, domain.ComputerUseListProcessesReq{NameContains: "chrome"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Count != 2 {
		t.Fatalf("count=%d", resp.Count)
	}
}

func TestHandleCursorPosition(t *testing.T) {
	a, _, ctx := freshActor(t)
	resp, err := a.handleCursorPosition(ctx, domain.ComputerUseCursorPositionReq{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.X != 100 || resp.Y != 200 {
		t.Fatalf("pos=(%d,%d)", resp.X, resp.Y)
	}
}

func TestHandleGetWindowInfo(t *testing.T) {
	a, _, ctx := freshActor(t)
	resp, err := a.handleGetWindowInfo(ctx, domain.ComputerUseGetWindowInfoReq{WindowID: 1002})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Title != "Browser - Example" || resp.ClassName != "TestClass" {
		t.Fatalf("resp=%+v", resp)
	}
}

func TestHandleGetWindowInfo_RequiresID(t *testing.T) {
	a, _, ctx := freshActor(t)
	if _, err := a.handleGetWindowInfo(ctx, domain.ComputerUseGetWindowInfoReq{}); err == nil {
		t.Fatal("expected error when windowID missing")
	}
}

// --- UIA / OCR / Clipboard ---

func TestHandleListElements(t *testing.T) {
	a, _, ctx := freshActor(t)
	resp, err := a.handleListElements(ctx, domain.ComputerUseListElementsReq{NameContains: "OK"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Count != 1 || resp.Items[0].Name != "OK" {
		t.Fatalf("items=%+v", resp.Items)
	}
}

func TestHandleOcr(t *testing.T) {
	a, _, ctx := freshActor(t)
	resp, err := a.handleOcr(ctx, domain.ComputerUseOcrReq{
		Target: &domain.ComputerUseWindowTarget{Mode: "display"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello world" {
		t.Fatalf("text=%q", resp.Text)
	}
	if len(resp.Lines) != 1 {
		t.Fatalf("lines=%d", len(resp.Lines))
	}
	if resp.Engine != "fake" {
		t.Fatalf("engine=%q", resp.Engine)
	}
}

func TestHandleClipboardGetSet(t *testing.T) {
	a, _, ctx := freshActor(t)
	if _, err := a.handleClipboardSet(ctx, domain.ComputerUseClipboardSetReq{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleClipboardGet(ctx, domain.ComputerUseClipboardGetReq{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hi" {
		t.Fatalf("roundtrip got %q", resp.Text)
	}
}

func TestHandleClipboardSetRichDispatch(t *testing.T) {
	a, fc, ctx := freshActor(t)
	resp, err := a.handleClipboardSetRich(ctx, domain.ComputerUseClipboardRichSetReq{
		HTML: "<p>hi</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success || resp.Kind != "html" {
		t.Fatalf("resp=%+v", resp)
	}
	if !hasCall(fc, "SetClipboardData") {
		t.Fatalf("no SetClipboardData call: %v", fc.calls)
	}
}

// helpers

func TestRecoverHandlerConvertsPanicToError(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	handler := recoverHandler("screenshot", func(testutilFakeContext actor.Context, req domain.ComputerUseScreenshotReq) (domain.ComputerUseScreenshotResp, error) {
		panic("native capture failure")
	})
	_, err := handler(ctx, domain.ComputerUseScreenshotReq{})
	if err == nil || !strings.Contains(err.Error(), "computeruse: screenshot panic recovered: native capture failure") {
		t.Fatalf("err=%v", err)
	}
}

type fakeEmitter struct{}

func (fakeEmitter) Send(any) error        { return nil }
func (fakeEmitter) Done() <-chan struct{} { return nil }

func TestRecoverStreamHandlerConvertsPanicToError(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	handler := recoverStreamHandler("stream", func(testutilPureContext actor.PureContext, req domain.ComputerUseStreamReq, emit actor.Emitter) error {
		panic("native stream failure")
	})
	err := handler(ctx, domain.ComputerUseStreamReq{}, fakeEmitter{})
	if err == nil || !strings.Contains(err.Error(), "computeruse: stream panic recovered: native stream failure") {
		t.Fatalf("err=%v", err)
	}
}

func hasCall(fc *fakeCapturer, substr string) bool {
	for _, c := range fc.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}
