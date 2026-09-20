// Package computeruse is the sporemind actor that exposes screen capture and
// mouse/keyboard automation as a small set of callables for LLM agents.
//
// Surface:
//   - computeruse.screenshot      (Public, read-only)
//   - computeruse.capabilities    (Public, read-only — platform support matrix)
//   - computeruse.list_windows    (Public, read-only, filtered)
//   - computeruse.list_displays   (Public, read-only)
//   - computeruse.list_processes  (Public, read-only)
//   - computeruse.get_window_info (Public, read-only)
//   - computeruse.cursor_position (Public, read-only)
//   - computeruse.ocr             (Public, read-only)
//   - computeruse.setup_ocr       (Public — dedicated computeruse_setup lane;
//     downloads PaddleOCR model files and writes durable OCR state)
//   - computeruse.stream          (Internal + gated, scope computeruse.continuous_capture)
//   - computeruse.interact        (Internal + gated, scope computeruse.input_control)
//   - computeruse.clipboard_get   (Internal + gated, scope computeruse.clipboard_read)
//   - computeruse.clipboard_set   (Internal + gated, scope computeruse.clipboard_write)
//
// The read-only OS queries above (list_windows / list_displays /
// list_processes / list_elements / cursor_position / get_window_info /
// capabilities / ocr / clipboard_get / clipboard_get_rich) are registered as
// PureContext handlers: the gospore cell infers ModeStateless from the first
// parameter, so they run on the pure loop without serializing behind the
// owner lane. They only read the capturer (itself lock-guarded and already
// driven from PureContext by computeruse.stream). setup_ocr is the sole
// exception — it downloads models and persists the chosen dir/status via
// pkg/persist — so it keeps the stateful signature and is routed to its own
// dedicated "computeruse_setup" lane instead of the owner lane.
//
// Gated callables are declared explicitly in policy.go (gatedCallables) and
// registered actor.Internal(): they are never exported to frontend codegen
// and reach agents only via the admin-enabled tool bundle, where the turn
// engine's PermissionPolicy requires user confirmation for the
// EffectIrreversible ones.
//
// All wire types are defined in schemas/computeruse.*.spore and aliased
// through pkg/domain. Platform backend lives in internal/capture (winx /
// cgmac / x11). Durable state (OCR model dir, setup status, engine probe
// results) goes through pkg/persist.
package computeruse

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // register JPEG decoder for clipboard set_rich
	"image/png"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/annotate"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/diff"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/encoder"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/ocr"
	_ "github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/winx" // register windows backend
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// Actor is the computeruse top-level actor.
type Actor struct {
	actor.Host

	store   persist.Persist
	actorID string

	// ocrState is the durable OCR setup/probe record, persisted via store.
	ocrState ocrSnapshot

	mu  sync.Mutex
	cap capture.Capturer
}

var _ persist.Persistent = (*Actor)(nil)

// ocrSnapshot is the persisted OCR state: the model directory chosen via
// setup_ocr, the last setup result, and the last engine probe results.
type ocrSnapshot struct {
	ModelDir           string   `json:"modelDir,omitempty"`
	LastSetupStatus    string   `json:"lastSetupStatus,omitempty"`
	LastSetupAt        string   `json:"lastSetupAt,omitempty"`
	LastSetupFiles     []string `json:"lastSetupFiles,omitempty"`
	PaddleAvailable    bool     `json:"paddleAvailable"`
	TesseractAvailable bool     `json:"tesseractAvailable"`
}

// OnInit initialises the persist store and restores durable state before
// any callable registration.
func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("computeruse"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	if err := a.Load(); err != nil {
		ctx.Logger().Error("computeruse: load state failed", "error", err)
	}
	// Restore the persisted OCR model dir so the paddle probe finds models
	// installed into a custom directory by a previous setup_ocr call.
	if a.ocrState.ModelDir != "" {
		ocr.SetModelDir(a.ocrState.ModelDir)
	}
	return nil
}

// Save persists the OCR snapshot via pkg/persist.
func (a *Actor) Save() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("computeruse"))
		if err != nil {
			return err
		}
	}
	return a.store.Save(a.actorID, a.ocrState)
}

// Load restores the OCR snapshot via pkg/persist. Missing state is a valid
// first-start condition.
func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("computeruse"))
		if err != nil {
			return err
		}
	}
	var snap ocrSnapshot
	if err := persist.LoadOrZero(a.store, a.actorID, &snap); err != nil {
		return err
	}
	a.ocrState = snap
	return nil
}

// Type identifies this actor in the gospore runtime.
func (a *Actor) Type() string { return "computeruse" }

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("computeruse: starting", "id", ctx.Self().ID().String())

	const setupLoop = "computeruse_setup"
	if err := ctx.RegisterLoop(setupLoop, actor.ModeStateful); err != nil {
		return fmt.Errorf("computeruse: register loop %s: %w", setupLoop, err)
	}

	if err := ctx.Register("computeruse.screenshot", recoverHandler("screenshot", a.handleScreenshot),
		actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Capture a screenshot of the screen for LLM analysis"),
	); err != nil {
		return fmt.Errorf("computeruse: register screenshot: %w", err)
	}
	if err := ctx.Register("computeruse.list_windows", recoverPureHandler("list_windows", a.handleListWindows),
		actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("List top-level windows (with optional filters)"),
	); err != nil {
		return fmt.Errorf("computeruse: register list_windows: %w", err)
	}
	if err := ctx.Register("computeruse.list_displays", recoverPureHandler("list_displays", a.handleListDisplays),
		actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("List connected displays"),
	); err != nil {
		return fmt.Errorf("computeruse: register list_displays: %w", err)
	}
	if err := ctx.Register("computeruse.list_processes", recoverPureHandler("list_processes", a.handleListProcesses),
		actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("List OS processes (optionally filtered by name/pid)"),
	); err != nil {
		return fmt.Errorf("computeruse: register list_processes: %w", err)
	}
	if err := ctx.Register("computeruse.get_window_info", recoverPureHandler("get_window_info", a.handleGetWindowInfo),
		actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Return detailed info for one window"),
	); err != nil {
		return fmt.Errorf("computeruse: register get_window_info: %w", err)
	}
	if err := ctx.Register("computeruse.cursor_position", recoverPureHandler("cursor_position", a.handleCursorPosition),
		actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Return the current cursor position"),
	); err != nil {
		return fmt.Errorf("computeruse: register cursor_position: %w", err)
	}
	if err := ctx.Register("computeruse.list_elements", recoverPureHandler("list_elements", a.handleListElements),
		actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Enumerate UI Automation elements under a window (or desktop)"),
	); err != nil {
		return fmt.Errorf("computeruse: register list_elements: %w", err)
	}
	if err := ctx.Register("computeruse.ocr", recoverPureHandler("ocr", a.handleOcr),
		actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Run OCR over a screen region and return recognised text"),
	); err != nil {
		return fmt.Errorf("computeruse: register ocr: %w", err)
	}
	if err := ctx.Register("computeruse.setup_ocr", recoverHandler("setup_ocr", a.handleSetupOcr),
		actor.Public(),
		actor.WithLoop("computeruse_setup"),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Download or verify PaddleOCR model files and ONNX runtime for embedded OCR [dedicated computeruse_setup lane: writes durable OCR state]"),
	); err != nil {
		return fmt.Errorf("computeruse: register setup_ocr: %w", err)
	}
	if err := ctx.Register("computeruse.capabilities", recoverPureHandler("capabilities", a.handleCapabilities),
		actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Report the platform capability matrix: which features, interact actions, OCR engines and gated callables are supported on this host"),
	); err != nil {
		return fmt.Errorf("computeruse: register capabilities: %w", err)
	}
	// The callables below are gated: each carries an explicit policy scope
	// declaration (policy.go, gatedCallables) and is registered
	// actor.Internal() — never exported to frontend codegen. Agents reach
	// them only through the admin-enabled computeruse tool bundle; the turn
	// engine's PermissionPolicy requires user confirmation for the
	// EffectIrreversible ones (interact, clipboard_set, clipboard_set_rich).
	if err := ctx.Register("computeruse.interact", recoverHandler("interact", a.handleInteract),
		actor.Internal(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Perform mouse / keyboard / window actions on the computer [gated scope: "+ScopeInputControl+"]"),
	); err != nil {
		return fmt.Errorf("computeruse: register interact: %w", err)
	}
	if err := ctx.Register("computeruse.stream", recoverStreamHandler("stream", a.handleStream),
		actor.Internal(),
		actor.Streaming[domain.ComputerUseStreamChunk](),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Continuous low-bandwidth tile-diff capture of a display/region/window [gated scope: "+ScopeContinuousCapture+"]"),
	); err != nil {
		return fmt.Errorf("computeruse: register stream: %w", err)
	}
	if err := ctx.Register("computeruse.clipboard_get", recoverPureHandler("clipboard_get", a.handleClipboardGet),
		actor.Internal(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Read the system clipboard text [gated scope: "+ScopeClipboardRead+"]"),
	); err != nil {
		return fmt.Errorf("computeruse: register clipboard_get: %w", err)
	}
	if err := ctx.Register("computeruse.clipboard_set", recoverHandler("clipboard_set", a.handleClipboardSet),
		actor.Internal(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Write text to the system clipboard [gated scope: "+ScopeClipboardWrite+"]"),
	); err != nil {
		return fmt.Errorf("computeruse: register clipboard_set: %w", err)
	}
	if err := ctx.Register("computeruse.clipboard_get_rich", recoverPureHandler("clipboard_get_rich", a.handleClipboardGetRich),
		actor.Internal(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Read the system clipboard in all available formats (text/image/files/html) [gated scope: "+ScopeClipboardRead+"]"),
	); err != nil {
		return fmt.Errorf("computeruse: register clipboard_get_rich: %w", err)
	}
	if err := ctx.Register("computeruse.clipboard_set_rich", recoverHandler("clipboard_set_rich", a.handleClipboardSetRich),
		actor.Internal(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Write a typed payload (image/files/html/text) to the system clipboard [gated scope: "+ScopeClipboardWrite+"]"),
	); err != nil {
		return fmt.Errorf("computeruse: register clipboard_set_rich: %w", err)
	}
	if err := ctx.RegisterDomain("computeruse").Expose(); err != nil {
		return fmt.Errorf("computeruse: expose service: %w", err)
	}
	return nil
}

func (a *Actor) OnStop(ctx actor.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			ctx.Logger().Error("computeruse: native close panic recovered", "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("computeruse: native close panic recovered: %v", r)
		}
	}()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cap != nil {
		err = a.cap.Close()
		a.cap = nil
	}
	if serr := a.Save(); serr != nil {
		ctx.Logger().Error("computeruse: save state failed", "error", serr)
		if err == nil {
			err = serr
		}
	}
	return err
}

func recoverHandler[Req any, Resp any](name string, handler func(actor.Context, Req) (Resp, error)) func(actor.Context, Req) (Resp, error) {
	return func(ctx actor.Context, req Req) (resp Resp, err error) {
		defer func() {
			if r := recover(); r != nil {
				ctx.Logger().Error("computeruse: native panic recovered", "callable", name, "panic", r, "stack", string(debug.Stack()))
				err = fmt.Errorf("computeruse: %s panic recovered: %v", name, r)
			}
		}()
		return handler(ctx, req)
	}
}

// recoverPureHandler wraps a read-only (PureContext) handler with the same
// panic containment as recoverHandler, preserving the stateless signature so
// the gospore cell infers ModeStateless from the first parameter type.
func recoverPureHandler[Req any, Resp any](name string, handler func(actor.PureContext, Req) (Resp, error)) func(actor.PureContext, Req) (Resp, error) {
	return func(ctx actor.PureContext, req Req) (resp Resp, err error) {
		defer func() {
			if r := recover(); r != nil {
				ctx.Logger().Error("computeruse: native panic recovered", "callable", name, "panic", r, "stack", string(debug.Stack()))
				err = fmt.Errorf("computeruse: %s panic recovered: %v", name, r)
			}
		}()
		return handler(ctx, req)
	}
}

func recoverStreamHandler[Req any](name string, handler func(actor.PureContext, Req, actor.Emitter) error) func(actor.PureContext, Req, actor.Emitter) error {
	return func(ctx actor.PureContext, req Req, emit actor.Emitter) (err error) {
		defer func() {
			if r := recover(); r != nil {
				ctx.Logger().Error("computeruse: native stream panic recovered", "callable", name, "panic", r, "stack", string(debug.Stack()))
				err = fmt.Errorf("computeruse: %s panic recovered: %v", name, r)
			}
		}()
		return handler(ctx, req, emit)
	}
}

// capturer returns a lazily-initialised platform Capturer.
func (a *Actor) capturer() (capture.Capturer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cap != nil {
		return a.cap, nil
	}
	c, err := capture.New()
	if err != nil {
		return nil, fmt.Errorf("computeruse: init capturer: %w", err)
	}
	a.cap = c
	return c, nil
}

// =========================================================================
// Screenshot.
// =========================================================================

func (a *Actor) handleScreenshot(ctx actor.Context, req domain.ComputerUseScreenshotReq) (domain.ComputerUseScreenshotResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseScreenshotResp{}, err
	}

	mode, display, region, windowID, windowTitle := mergeScreenshotTarget(req)

	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" {
		format = "jpeg"
	}
	quality := int(req.Quality)
	if quality <= 0 || quality > 100 {
		quality = 85
	}
	scale := float64(req.Scale)
	if scale <= 0 || scale > 1.0 {
		scale = 1.0
	}

	opts := capture.CaptureOptions{
		DisplayID:     display,
		IncludeCursor: req.IncludeCursor,
		EncodeFormat:  format,
		Quality:       quality,
		Annotate: capture.AnnotateOptions{
			DrawGrid:     req.Annotate,
			MarkElements: req.Annotate,
		},
	}

	var frame *capture.Frame
	switch mode {
	case "", "full", "display":
		opts.Mode = capture.ModeFull
		frame, err = c.Capture(opts)
	case "delta":
		prev := c.GetLastFrame()
		if prev != nil {
			opts.Mode = capture.ModeDelta
			opts.PreviousFrame = prev
		} else {
			opts.Mode = capture.ModeFull
		}
		frame, err = c.Capture(opts)
	case "region":
		if region == nil {
			return domain.ComputerUseScreenshotResp{}, fmt.Errorf("region is required when mode='region'")
		}
		opts.Mode = capture.ModeRegion
		opts.Region = image.Rect(
			int(region.X), int(region.Y),
			int(region.X+region.Width), int(region.Y+region.Height))
		frame, err = c.Capture(opts)
	case "window":
		hwnd, ferr := findScreenshotWindow(c, windowID, windowTitle)
		if ferr != nil {
			return domain.ComputerUseScreenshotResp{}, ferr
		}
		frame, err = c.CaptureWindow(hwnd, opts)
	case "window_region":
		if region == nil {
			return domain.ComputerUseScreenshotResp{}, fmt.Errorf("region is required when mode='window_region'")
		}
		hwnd, ferr := findScreenshotWindow(c, windowID, windowTitle)
		if ferr != nil {
			return domain.ComputerUseScreenshotResp{}, ferr
		}
		info, werr := c.WindowInfo(hwnd)
		if werr != nil {
			return domain.ComputerUseScreenshotResp{}, fmt.Errorf("window info: %w", werr)
		}
		opts.Mode = capture.ModeRegion
		opts.Region = image.Rect(
			info.Bounds.Min.X+int(region.X),
			info.Bounds.Min.Y+int(region.Y),
			info.Bounds.Min.X+int(region.X+region.Width),
			info.Bounds.Min.Y+int(region.Y+region.Height))
		frame, err = c.Capture(opts)
	default:
		return domain.ComputerUseScreenshotResp{}, fmt.Errorf("unknown mode: %s", mode)
	}
	if err != nil {
		return domain.ComputerUseScreenshotResp{}, fmt.Errorf("capture: %w", err)
	}

	if frame.IsIdentical {
		return domain.ComputerUseScreenshotResp{
			Format:    format,
			Unchanged: true,
			Message:   "No significant changes detected since last screenshot.",
		}, nil
	}

	pixels := frame.RawPixels
	bounds := frame.Bounds
	w := bounds.Dx()
	h := bounds.Dy()

	if req.Grayscale && pixels != nil {
		pixels = capture.ToGrayscale(pixels)
	}
	if scale < 1.0 && pixels != nil {
		scaled, sw, sh, sErr := capture.Scale(pixels, w, h, scale)
		if sErr == nil {
			pixels = scaled
			w, h = sw, sh
		}
	}

	data := frame.Data
	if req.Grayscale || scale < 1.0 {
		enc, encErr := encoder.New(format)
		if encErr != nil {
			return domain.ComputerUseScreenshotResp{}, fmt.Errorf("encoder: %w", encErr)
		}
		data, err = enc.Encode(pixels, image.Rect(0, 0, w, h), quality)
		if err != nil {
			return domain.ComputerUseScreenshotResp{}, fmt.Errorf("re-encode: %w", err)
		}
	}

	resp := domain.ComputerUseScreenshotResp{
		ImageBytes: data,
		Format:     format,
		Width:      int32(w),
		Height:     int32(h),
	}
	// Gate on CursorValid (not on non-zero coords): a cursor at the screen
	// origin (0,0) is a valid position and must still be reported.
	if frame.CursorValid {
		resp.CursorX = int32(frame.CursorPos.X)
		resp.CursorY = int32(frame.CursorPos.Y)
	}
	if displays, _ := c.Displays(); display < len(displays) && display >= 0 {
		resp.DisplayName = displays[display].Name
	}
	if req.Annotate {
		resp.Message = "Annotation enabled. Use grid coordinates (e.g. 'A1', 'B3') to refer to screen locations."
	}
	return resp, nil
}

// mergeScreenshotTarget resolves the unified target field over the legacy
// mode/display/region/windowTitle fields. Each target field wins only when
// actually set — a target carrying just WindowID combined with a top-level
// Mode="window" must stay window mode (it used to resolve to "" → full
// display, silently returning the whole screen for window captures).
func mergeScreenshotTarget(req domain.ComputerUseScreenshotReq) (mode string, display int, region *domain.ComputerUseRegion, windowID int, windowTitle string) {
	mode = strings.ToLower(strings.TrimSpace(req.Mode))
	display = int(req.Display)
	region = req.Region
	windowTitle = req.WindowTitle
	if t := req.Target; t != nil {
		if m := strings.ToLower(strings.TrimSpace(t.Mode)); m != "" {
			mode = m
		}
		display = int(t.Display)
		if t.Region != nil {
			region = t.Region
		}
		if t.WindowID != 0 {
			windowID = int(t.WindowID)
		}
		if t.WindowTitle != "" {
			windowTitle = t.WindowTitle
		}
	}
	// An omitted Mode combined with an explicit Region/WindowID must not
	// silently fall through to a full-screen capture; infer the mode from
	// whichever targeting field the caller actually set (region > window).
	if mode == "" {
		if region != nil {
			mode = "region"
		} else if windowID != 0 || windowTitle != "" {
			mode = "window"
		}
	}
	return mode, display, region, windowID, windowTitle
}

// findScreenshotWindow resolves a window by ID first, then by title.
func findScreenshotWindow(c capture.Capturer, windowID int, windowTitle string) (int, error) {
	if windowID != 0 {
		return windowID, nil
	}
	if windowTitle == "" {
		return 0, fmt.Errorf("windowID or windowTitle is required for window mode")
	}
	wins, err := c.Windows()
	if err != nil {
		return 0, fmt.Errorf("list windows: %w", err)
	}
	needle := strings.ToLower(windowTitle)
	for i := range wins {
		if strings.Contains(strings.ToLower(wins[i].Title), needle) {
			return wins[i].ID, nil
		}
	}
	return 0, fmt.Errorf("window not found: %s", windowTitle)
}

// =========================================================================
// Interact.
// =========================================================================

func (a *Actor) handleInteract(ctx actor.Context, req domain.ComputerUseInteractReq) (domain.ComputerUseInteractResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseInteractResp{}, err
	}

	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		return domain.ComputerUseInteractResp{Success: false, Message: "action is required"}, nil
	}

	// Actions that don't address a screen point skip target resolution.
	var x, y int
	switch action {
	case "wait", "element_click", "element_focus", "element_set_value":
		// no screen-point target needed
	default:
		x, y, err = a.resolveTarget(c, req.Target)
		if err != nil {
			return domain.ComputerUseInteractResp{Success: false, Message: err.Error()}, nil
		}
	}

	resp := domain.ComputerUseInteractResp{Success: true}

	switch action {
	case "click":
		btn := pickButton(req.Button)
		if err := c.Click(x, y, btn); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("click failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Clicked %s at (%d, %d)", btn, x, y)
		}
	case "double_click":
		if err := c.Click(x, y, "left"); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("double click failed: %v", err)
			break
		}
		_ = c.Click(x, y, "left")
		resp.Message = fmt.Sprintf("Double-clicked at (%d, %d)", x, y)
	case "right_click":
		if err := c.Click(x, y, "right"); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("right click failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Right-clicked at (%d, %d)", x, y)
		}
	case "middle_click":
		if err := c.Click(x, y, "middle"); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("middle click failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Middle-clicked at (%d, %d)", x, y)
		}
	case "move":
		if err := c.MoveCursor(x, y); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("move failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Moved cursor to (%d, %d)", x, y)
		}
	case "scroll":
		dx, dy := 0, 0
		if req.ScrollDelta != nil {
			dx = int(req.ScrollDelta.X)
			dy = int(req.ScrollDelta.Y)
		}
		if err := c.Scroll(x, y, dx, dy); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("scroll failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Scrolled (%d, %d) at (%d, %d)", dx, dy, x, y)
		}
	case "type":
		if req.Text == "" {
			return domain.ComputerUseInteractResp{Success: false, Message: "text is required for action='type'"}, nil
		}
		if err := c.Type(req.Text); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("type failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Typed %d chars", len(req.Text))
		}
	case "key":
		if req.Key == "" {
			return domain.ComputerUseInteractResp{Success: false, Message: "key is required for action='key'"}, nil
		}
		if err := c.KeyPress(req.Key, req.Modifiers...); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("keypress failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Pressed key: %s", req.Key)
		}
	case "copy":
		if err := c.KeyPress("c", "ctrl"); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("copy failed: %v", err)
		} else {
			resp.Message = "Copied selection to clipboard"
		}
	case "paste":
		if req.Text != "" {
			if err := c.SetClipboard(req.Text); err != nil {
				resp.Success = false
				resp.Message = fmt.Sprintf("set clipboard failed: %v", err)
				break
			}
		}
		if err := c.KeyPress("v", "ctrl"); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("paste failed: %v", err)
		} else {
			resp.Message = "Pasted from clipboard"
		}
	case "screenshot_then_click":
		btn := pickButton(req.Button)
		if err := c.Click(x, y, btn); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("click failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Clicked %s at (%d, %d)", btn, x, y)
		}
	case "focus_window":
		if req.Target == nil || req.Target.WindowID == 0 {
			return domain.ComputerUseInteractResp{Success: false, Message: "target.windowID is required for action='focus_window'"}, nil
		}
		if err := c.FocusWindow(int(req.Target.WindowID)); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("focus failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Focused window %d", req.Target.WindowID)
		}
	case "drag":
		if req.DragTo == nil {
			return domain.ComputerUseInteractResp{Success: false, Message: "dragTo is required for action='drag'"}, nil
		}
		toX, toY, derr := a.resolveTarget(c, req.DragTo)
		if derr != nil {
			return domain.ComputerUseInteractResp{Success: false, Message: derr.Error()}, nil
		}
		btn := pickButton(req.Button)
		if err := c.Drag(x, y, toX, toY, btn); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("drag failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Dragged %s from (%d,%d) to (%d,%d)", btn, x, y, toX, toY)
		}
	case "key_down":
		if req.Key == "" {
			return domain.ComputerUseInteractResp{Success: false, Message: "key is required for action='key_down'"}, nil
		}
		if err := c.KeyDown(req.Key, req.Modifiers...); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("key_down failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Pressed (down) key: %s", req.Key)
		}
	case "key_up":
		if req.Key == "" {
			return domain.ComputerUseInteractResp{Success: false, Message: "key is required for action='key_up'"}, nil
		}
		if err := c.KeyUp(req.Key, req.Modifiers...); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("key_up failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Released key: %s", req.Key)
		}
	case "wait":
		ms := int(req.WaitMs)
		if ms <= 0 {
			return domain.ComputerUseInteractResp{Success: false, Message: "waitMs > 0 required for action='wait'"}, nil
		}
		if ms > 30000 {
			ms = 30000
		}
		timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
		select {
		case <-timer.C:
			resp.Message = fmt.Sprintf("Waited %d ms", ms)
		case <-ctx.Done():
			timer.Stop()
			resp.Success = false
			resp.Message = "wait cancelled"
		}
	case "window_state":
		if req.Target == nil || req.Target.WindowID == 0 {
			return domain.ComputerUseInteractResp{Success: false, Message: "target.windowID is required for action='window_state'"}, nil
		}
		state := strings.ToLower(strings.TrimSpace(req.ShowState))
		if err := c.SetWindowState(int(req.Target.WindowID), state); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("window_state failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Set window %d state=%s", req.Target.WindowID, state)
		}
	case "window_move":
		if req.Target == nil || req.Target.WindowID == 0 {
			return domain.ComputerUseInteractResp{Success: false, Message: "target.windowID is required for action='window_move'"}, nil
		}
		if req.WindowPos == nil {
			return domain.ComputerUseInteractResp{Success: false, Message: "windowPos is required for action='window_move'"}, nil
		}
		if err := c.MoveWindow(int(req.Target.WindowID),
			int(req.WindowPos.X), int(req.WindowPos.Y),
			int(req.WindowPos.Width), int(req.WindowPos.Height)); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("window_move failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Moved window %d to (%d,%d) %dx%d",
				req.Target.WindowID,
				req.WindowPos.X, req.WindowPos.Y,
				req.WindowPos.Width, req.WindowPos.Height)
		}
	case "window_close":
		if req.Target == nil || req.Target.WindowID == 0 {
			return domain.ComputerUseInteractResp{Success: false, Message: "target.windowID is required for action='window_close'"}, nil
		}
		if err := c.CloseWindow(int(req.Target.WindowID)); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("window_close failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Sent WM_CLOSE to window %d", req.Target.WindowID)
		}
	case "element_click":
		if req.ElementID == "" {
			return domain.ComputerUseInteractResp{Success: false, Message: "elementID is required for action='element_click'"}, nil
		}
		btn := pickButton(req.Button)
		if err := c.ClickElement(req.ElementID, btn); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("element_click failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Clicked element %s", req.ElementID)
		}
	case "element_focus":
		if req.ElementID == "" {
			return domain.ComputerUseInteractResp{Success: false, Message: "elementID is required for action='element_focus'"}, nil
		}
		if err := c.FocusElement(req.ElementID); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("element_focus failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Focused element %s", req.ElementID)
		}
	case "element_set_value":
		if req.ElementID == "" {
			return domain.ComputerUseInteractResp{Success: false, Message: "elementID is required for action='element_set_value'"}, nil
		}
		if err := c.SetElementValue(req.ElementID, req.ElementValue); err != nil {
			resp.Success = false
			resp.Message = fmt.Sprintf("element_set_value failed: %v", err)
		} else {
			resp.Message = fmt.Sprintf("Set element %s value", req.ElementID)
		}
	default:
		return domain.ComputerUseInteractResp{Success: false, Message: fmt.Sprintf("unknown action: %s", action)}, nil
	}

	if pos, perr := c.CursorPosition(); perr == nil {
		resp.CursorX = int32(pos.X)
		resp.CursorY = int32(pos.Y)
	}
	return resp, nil
}

func pickButton(b string) string {
	switch strings.ToLower(b) {
	case "left", "right", "middle":
		return strings.ToLower(b)
	default:
		return "left"
	}
}

// defaultGridCellSize is the pixel width of one annotation grid cell. It must
// match the cell size the screenshot grid was drawn with. The capture layer's
// AnnotateOptions.GridSize defaults to 100 and is not currently exposed by the
// screenshot/interact request schemas (handleScreenshot never overrides it), so
// both drawing and GridCoord resolution agree on this default.
// TODO: plumb GridSize through the spore schema (screenshot + interact reqs)
// and pass it into resolveTarget so a custom grid size stays consistent with
// the drawn overlay.
const defaultGridCellSize = 100

func (a *Actor) resolveTarget(c capture.Capturer, target *domain.ComputerUseTargetSpec) (int, int, error) {
	if target == nil {
		pos, err := c.CursorPosition()
		return pos.X, pos.Y, err
	}
	if target.GridCoord != "" {
		pt, err := annotate.GridCoordToPoint(target.GridCoord, defaultGridCellSize)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid grid coordinate %q: %w", target.GridCoord, err)
		}
		return pt.X, pt.Y, nil
	}
	if target.ElementDescription != "" {
		wins, err := c.Windows()
		if err != nil {
			return 0, 0, fmt.Errorf("list windows: %w", err)
		}
		needle := strings.ToLower(target.ElementDescription)
		for _, w := range wins {
			if strings.Contains(strings.ToLower(w.Title), needle) {
				return (w.Bounds.Min.X + w.Bounds.Max.X) / 2,
					(w.Bounds.Min.Y + w.Bounds.Max.Y) / 2, nil
			}
		}
		return 0, 0, fmt.Errorf("element not found: %s", target.ElementDescription)
	}
	if target.WindowID != 0 {
		info, err := c.WindowInfo(int(target.WindowID))
		if err != nil {
			return 0, 0, fmt.Errorf("window info: %w", err)
		}
		return (info.Bounds.Min.X + info.Bounds.Max.X) / 2,
			(info.Bounds.Min.Y + info.Bounds.Max.Y) / 2, nil
	}
	return int(target.X), int(target.Y), nil
}

// =========================================================================
// Listings.
// =========================================================================

func (a *Actor) handleListWindows(ctx actor.PureContext, req domain.ComputerUseListWindowsReq) (domain.ComputerUseWindowsResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseWindowsResp{}, err
	}
	filter := capture.WindowFilter{
		PID:              int(req.Pid),
		ProcessName:      req.ProcessName,
		TitlePattern:     req.TitlePattern,
		ForegroundOnly:   req.ForegroundOnly,
		IncludeInvisible: req.IncludeInvisible,
	}
	if req.AtPoint != nil && (req.AtPoint.X != 0 || req.AtPoint.Y != 0) {
		pt := image.Point{X: int(req.AtPoint.X), Y: int(req.AtPoint.Y)}
		filter.AtPoint = &pt
	}
	wins, err := c.FilteredWindows(filter)
	if err != nil {
		return domain.ComputerUseWindowsResp{}, err
	}
	out := make([]domain.ComputerUseWindowInfo, 0, len(wins))
	for _, w := range wins {
		out = append(out, domain.ComputerUseWindowInfo{
			ID:    int32(w.ID),
			Title: w.Title,
			Bounds: domain.ComputerUseRegion{
				X:      int32(w.Bounds.Min.X),
				Y:      int32(w.Bounds.Min.Y),
				Width:  int32(w.Bounds.Dx()),
				Height: int32(w.Bounds.Dy()),
			},
			ProcessName: w.ProcessName,
			Pid:         int32(w.PID),
			IsVisible:   w.IsVisible,
		})
	}
	return domain.ComputerUseWindowsResp{Items: out, Count: int32(len(out))}, nil
}

func (a *Actor) handleListDisplays(ctx actor.PureContext, _ domain.ComputerUseListDisplaysReq) (domain.ComputerUseDisplaysResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseDisplaysResp{}, err
	}
	ds, err := c.Displays()
	if err != nil {
		return domain.ComputerUseDisplaysResp{}, err
	}
	out := make([]domain.ComputerUseDisplayInfo, 0, len(ds))
	for _, d := range ds {
		out = append(out, domain.ComputerUseDisplayInfo{
			ID:   int32(d.ID),
			Name: d.Name,
			Bounds: domain.ComputerUseRegion{
				X:      int32(d.Bounds.Min.X),
				Y:      int32(d.Bounds.Min.Y),
				Width:  int32(d.Bounds.Dx()),
				Height: int32(d.Bounds.Dy()),
			},
			IsPrimary: d.IsPrimary,
		})
	}
	return domain.ComputerUseDisplaysResp{Items: out, Count: int32(len(out))}, nil
}

func (a *Actor) handleCursorPosition(ctx actor.PureContext, _ domain.ComputerUseCursorPositionReq) (domain.ComputerUseCursorPosResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseCursorPosResp{}, err
	}
	p, err := c.CursorPosition()
	if err != nil {
		return domain.ComputerUseCursorPosResp{}, err
	}
	return domain.ComputerUseCursorPosResp{X: int32(p.X), Y: int32(p.Y)}, nil
}

func (a *Actor) handleListProcesses(ctx actor.PureContext, req domain.ComputerUseListProcessesReq) (domain.ComputerUseProcessesResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseProcessesResp{}, err
	}
	procs, err := c.Processes(req.NameContains, int(req.Pid))
	if err != nil {
		return domain.ComputerUseProcessesResp{}, err
	}
	out := make([]domain.ComputerUseProcessInfo, 0, len(procs))
	for _, p := range procs {
		out = append(out, domain.ComputerUseProcessInfo{
			Pid:       int32(p.PID),
			ParentPid: int32(p.ParentPID),
			Name:      p.Name,
			ExePath:   p.ExePath,
		})
	}
	return domain.ComputerUseProcessesResp{Items: out, Count: int32(len(out))}, nil
}

func (a *Actor) handleGetWindowInfo(ctx actor.PureContext, req domain.ComputerUseGetWindowInfoReq) (domain.ComputerUseWindowInfoDetailed, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseWindowInfoDetailed{}, err
	}
	if req.WindowID == 0 {
		return domain.ComputerUseWindowInfoDetailed{}, fmt.Errorf("windowID is required")
	}
	d, err := c.WindowInfo(int(req.WindowID))
	if err != nil {
		return domain.ComputerUseWindowInfoDetailed{}, err
	}
	return domain.ComputerUseWindowInfoDetailed{
		ID:    int32(d.ID),
		Title: d.Title,
		Bounds: domain.ComputerUseRegion{
			X:      int32(d.Bounds.Min.X),
			Y:      int32(d.Bounds.Min.Y),
			Width:  int32(d.Bounds.Dx()),
			Height: int32(d.Bounds.Dy()),
		},
		ProcessName:  d.ProcessName,
		Pid:          int32(d.PID),
		IsVisible:    d.IsVisible,
		State:        d.State,
		ClassName:    d.ClassName,
		IsForeground: d.IsForeground,
		IsTopmost:    d.IsTopmost,
	}, nil
}

// =========================================================================
// UI Automation — element tree.
// =========================================================================

func (a *Actor) handleListElements(ctx actor.PureContext, req domain.ComputerUseListElementsReq) (domain.ComputerUseListElementsResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseListElementsResp{}, err
	}
	filter := capture.ElementFilter{
		WindowID:     int(req.WindowID),
		NameContains: req.NameContains,
		ControlType:  req.ControlType,
		MaxDepth:     int(req.MaxDepth),
		MaxResults:   int(req.MaxResults),
	}
	nodes, err := c.ListElements(filter)
	if err != nil {
		return domain.ComputerUseListElementsResp{}, err
	}
	out := make([]domain.ComputerUseElementNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, domain.ComputerUseElementNode{
			ID:           n.ID,
			AutomationID: n.AutomationID,
			Name:         n.Name,
			ClassName:    n.ClassName,
			ControlType:  n.ControlType,
			Bounds: domain.ComputerUseRegion{
				X:      int32(n.Bounds.Min.X),
				Y:      int32(n.Bounds.Min.Y),
				Width:  int32(n.Bounds.Dx()),
				Height: int32(n.Bounds.Dy()),
			},
			IsEnabled:           n.IsEnabled,
			IsKeyboardFocusable: n.IsKeyboardFocusable,
			HasKeyboardFocus:    n.HasKeyboardFocus,
			Value:               n.Value,
		})
	}
	return domain.ComputerUseListElementsResp{Items: out, Count: int32(len(out))}, nil
}

// =========================================================================
// Capabilities — platform support matrix.
// =========================================================================

func (a *Actor) handleCapabilities(ctx actor.PureContext, _ domain.ComputerUseCapabilitiesReq) (domain.ComputerUseCapabilitiesResp, error) {
	resp := domain.ComputerUseCapabilitiesResp{
		Platform: runtime.GOOS,
		Backend:  "unavailable",
	}

	if c, err := a.capturer(); err != nil {
		resp.Items = append(resp.Items, domain.ComputerUseCapability{
			Key:    "backend.init",
			Detail: fmt.Sprintf("capture backend unavailable: %v", err),
		})
	} else {
		set := c.Capabilities()
		resp.Backend = set.Backend
		for _, item := range set.Items {
			resp.Items = append(resp.Items, domain.ComputerUseCapability{
				Key:       item.Key,
				Supported: item.Supported,
				Detail:    item.Detail,
			})
		}
	}

	// OCR engine availability is backend-independent (shared ocr package).
	// PureContext is read-only, so the probe results are reported but not
	// cached into durable state — the probe is recomputed per call and the
	// computeruse_setup lane owns the persisted OCR state.
	paddleOK := ocr.PaddleAvailable()
	tesseractOK := ocr.TesseractAvailable()
	resp.Items = append(resp.Items,
		domain.ComputerUseCapability{Key: "ocr.paddle", Supported: paddleOK, Detail: "embedded PaddleOCR (go-ocr/ONNX); install models via computeruse.setup_ocr"},
		domain.ComputerUseCapability{Key: "ocr.tesseract", Supported: tesseractOK, Detail: "external tesseract CLI on PATH"},
	)

	// Gated callables: surface the explicit policy declarations so agents
	// can introspect which scope each non-public callable requires.
	names := make([]string, 0, len(gatedCallables))
	for name := range gatedCallables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g := gatedCallables[name]
		resp.Items = append(resp.Items, domain.ComputerUseCapability{
			Key:       "callable." + name,
			Supported: true,
			Scope:     g.Scope,
			Detail:    g.Reason,
		})
	}
	return resp, nil
}

// =========================================================================
// OCR.
// =========================================================================

func (a *Actor) handleOcr(ctx actor.PureContext, req domain.ComputerUseOcrReq) (domain.ComputerUseOcrResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseOcrResp{}, err
	}

	// Capture using the same target semantics as screenshot, then hand the
	// raw RGBA buffer to OCR.
	var ssReq domain.ComputerUseScreenshotReq
	if req.Target != nil {
		ssReq.Target = req.Target
	}
	ssReq.Format = "raw" // we want raw pixels for OCR
	mode, display, region, windowID, windowTitle := mergeScreenshotTarget(ssReq)
	opts := capture.CaptureOptions{
		DisplayID:    display,
		EncodeFormat: "raw",
	}
	var frame *capture.Frame
	switch mode {
	case "", "full", "display":
		opts.Mode = capture.ModeFull
		frame, err = c.Capture(opts)
	case "region":
		if region == nil {
			return domain.ComputerUseOcrResp{}, fmt.Errorf("region is required when mode='region'")
		}
		opts.Mode = capture.ModeRegion
		opts.Region = image.Rect(
			int(region.X), int(region.Y),
			int(region.X+region.Width), int(region.Y+region.Height))
		frame, err = c.Capture(opts)
	case "window":
		hwnd, ferr := findScreenshotWindow(c, windowID, windowTitle)
		if ferr != nil {
			return domain.ComputerUseOcrResp{}, ferr
		}
		frame, err = c.CaptureWindow(hwnd, opts)
	case "window_region":
		if region == nil {
			return domain.ComputerUseOcrResp{}, fmt.Errorf("region is required when mode='window_region'")
		}
		hwnd, ferr := findScreenshotWindow(c, windowID, windowTitle)
		if ferr != nil {
			return domain.ComputerUseOcrResp{}, ferr
		}
		info, werr := c.WindowInfo(hwnd)
		if werr != nil {
			return domain.ComputerUseOcrResp{}, fmt.Errorf("window info: %w", werr)
		}
		opts.Mode = capture.ModeRegion
		opts.Region = image.Rect(
			info.Bounds.Min.X+int(region.X),
			info.Bounds.Min.Y+int(region.Y),
			info.Bounds.Min.X+int(region.X+region.Width),
			info.Bounds.Min.Y+int(region.Y+region.Height))
		frame, err = c.Capture(opts)
	default:
		return domain.ComputerUseOcrResp{}, fmt.Errorf("unknown mode: %s", mode)
	}
	if err != nil {
		return domain.ComputerUseOcrResp{}, fmt.Errorf("capture: %w", err)
	}

	w := frame.Bounds.Dx()
	h := frame.Bounds.Dy()
	result, err := c.OCR(frame.RawPixels, w, h, req.Language)
	if err != nil {
		return domain.ComputerUseOcrResp{}, err
	}

	out := domain.ComputerUseOcrResp{
		Text:   result.Text,
		Engine: result.Engine,
		CaptureBounds: domain.ComputerUseRegion{
			X:      int32(frame.Bounds.Min.X),
			Y:      int32(frame.Bounds.Min.Y),
			Width:  int32(w),
			Height: int32(h),
		},
	}
	originX, originY := frame.Bounds.Min.X, frame.Bounds.Min.Y
	for _, l := range result.Lines {
		out.Lines = append(out.Lines, domain.ComputerUseOcrLine{
			Text: l.Text,
			Bounds: domain.ComputerUseRegion{
				X:      int32(l.Bounds.Min.X + originX),
				Y:      int32(l.Bounds.Min.Y + originY),
				Width:  int32(l.Bounds.Dx()),
				Height: int32(l.Bounds.Dy()),
			},
			Confidence: float32(l.Confidence),
		})
	}
	return out, nil
}

// =========================================================================
// OCR setup — auto-download PaddleOCR model files.
// =========================================================================

func (a *Actor) handleSetupOcr(ctx actor.Context, req domain.ComputerUseSetupOcrReq) (domain.ComputerUseSetupOcrResp, error) {
	dir := req.Dir
	if dir == "" {
		dir = ocr.DefaultPaddleDir()
	}
	status, files, err := ensurePaddleModels(dir, req.Force)

	a.ocrState.LastSetupStatus = status
	a.ocrState.LastSetupAt = time.Now().UTC().Format(time.RFC3339)
	a.ocrState.LastSetupFiles = files
	if err == nil {
		// Register the install dir as the preferred search root and let the
		// next OCR call re-probe from scratch (the engine init failure is not
		// latched anymore, but a previously initialised engine must not pin
		// stale model paths either).
		ocr.SetModelDir(dir)
		ocr.ResetPaddle()
		a.ocrState.ModelDir = dir
		a.ocrState.PaddleAvailable = ocr.PaddleAvailable()
	}
	if serr := a.Save(); serr != nil {
		ctx.Logger().Error("computeruse: save state failed", "error", serr)
	}

	if err != nil {
		return domain.ComputerUseSetupOcrResp{
			Dir:    dir,
			Status: "failed",
			Files:  files,
			Error:  err.Error(),
		}, nil
	}
	return domain.ComputerUseSetupOcrResp{
		Dir:    dir,
		Status: status,
		Files:  files,
	}, nil
}

// =========================================================================

func (a *Actor) handleClipboardGet(ctx actor.PureContext, _ domain.ComputerUseClipboardGetReq) (domain.ComputerUseClipboardGetResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseClipboardGetResp{}, err
	}
	text, err := c.GetClipboard()
	if err != nil {
		return domain.ComputerUseClipboardGetResp{}, err
	}
	return domain.ComputerUseClipboardGetResp{Text: text}, nil
}

func (a *Actor) handleClipboardSet(ctx actor.Context, req domain.ComputerUseClipboardSetReq) (domain.ComputerUseClipboardSetResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseClipboardSetResp{}, err
	}
	if err := c.SetClipboard(req.Text); err != nil {
		return domain.ComputerUseClipboardSetResp{Success: false}, err
	}
	return domain.ComputerUseClipboardSetResp{Success: true}, nil
}

func (a *Actor) handleClipboardGetRich(ctx actor.PureContext, _ domain.ComputerUseClipboardRichGetReq) (domain.ComputerUseClipboardRichGetResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseClipboardRichGetResp{}, err
	}
	d, err := c.GetClipboardData()
	if err != nil {
		return domain.ComputerUseClipboardRichGetResp{}, err
	}
	resp := domain.ComputerUseClipboardRichGetResp{
		Kind:  d.Kind,
		Text:  d.Text,
		Files: d.Files,
		HTML:  d.HTML,
	}
	if d.Image != nil {
		var buf bytes.Buffer
		if err := png.Encode(&buf, d.Image); err != nil {
			return domain.ComputerUseClipboardRichGetResp{}, fmt.Errorf("encode clipboard image: %w", err)
		}
		resp.ImageBase64 = base64.StdEncoding.EncodeToString(buf.Bytes())
		resp.ImageWidth = int32(d.Image.Rect.Dx())
		resp.ImageHeight = int32(d.Image.Rect.Dy())
	}
	return resp, nil
}

func (a *Actor) handleClipboardSetRich(ctx actor.Context, req domain.ComputerUseClipboardRichSetReq) (domain.ComputerUseClipboardRichSetResp, error) {
	c, err := a.capturer()
	if err != nil {
		return domain.ComputerUseClipboardRichSetResp{}, err
	}
	d := capture.ClipboardData{
		Text:  req.Text,
		Files: req.Files,
		HTML:  req.HTML,
	}
	if req.ImageBase64 != "" {
		raw, err := base64.StdEncoding.DecodeString(req.ImageBase64)
		if err != nil {
			return domain.ComputerUseClipboardRichSetResp{Success: false}, fmt.Errorf("imageBase64 decode: %w", err)
		}
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return domain.ComputerUseClipboardRichSetResp{Success: false}, fmt.Errorf("imageBase64 image decode: %w", err)
		}
		rgba, ok := img.(*image.RGBA)
		if !ok {
			b := img.Bounds()
			rgba = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
			draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
		}
		d.Image = rgba
	}
	if err := c.SetClipboardData(d); err != nil {
		return domain.ComputerUseClipboardRichSetResp{Success: false}, err
	}
	kind := "empty"
	switch {
	case d.Image != nil:
		kind = "image"
	case len(d.Files) > 0:
		kind = "files"
	case d.HTML != "":
		kind = "html"
	case d.Text != "":
		kind = "text"
	}
	return domain.ComputerUseClipboardRichSetResp{Success: true, Kind: kind}, nil
}

// =========================================================================
// Stream — continuous tile-diff capture.
// =========================================================================

// streamTarget resolves a WindowTarget into the current capture rectangle
// plus origin metadata for the next frame. When the target follows a
// window, the rect tracks window movement; when the window is minimized
// or hidden, ok=false is returned so the caller can emit a window_hidden
// chunk and wait.
type streamTarget struct {
	mode        string // "display" | "region" | "window" | "window_region"
	displayID   int
	hwnd        int
	region      image.Rectangle // for "region" + "window_region"
	abs         image.Rectangle // absolute screen rect to capture this tick
	originX     int             // window origin if window mode
	originY     int
	windowBased bool
}

func (a *Actor) handleStream(ctx actor.PureContext, req domain.ComputerUseStreamReq, emit actor.Emitter) error {
	c, err := a.capturer()
	if err != nil {
		return err
	}

	target, err := initStreamTarget(c, &req.Target)
	if err != nil {
		return fmt.Errorf("computeruse.stream: %w", err)
	}

	maxFPS := int(req.MaxFPS)
	if maxFPS <= 0 || maxFPS > 60 {
		maxFPS = 10
	}
	tileSize := int(req.TileSize)
	if tileSize <= 0 {
		tileSize = 64
	}
	keyframeMs := int(req.KeyframeIntervalMs)
	if keyframeMs <= 0 {
		keyframeMs = 5000
	}
	quality := int(req.Quality)
	if quality <= 0 || quality > 100 {
		quality = 75
	}

	enc, err := encoder.New("jpeg")
	if err != nil {
		return err
	}

	frameInterval := time.Second / time.Duration(maxFPS)
	keyframeInterval := time.Duration(keyframeMs) * time.Millisecond
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()

	var prevPixels []byte
	var prevW, prevH int
	var frameIndex int64
	lastKeyframe := time.Now().Add(-keyframeInterval) // force keyframe first tick

	for {
		select {
		case <-emit.Done():
			return nil
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			frameIndex++

			ok, err := resolveStreamRect(c, &target)
			if err != nil {
				return fmt.Errorf("computeruse.stream: resolve target: %w", err)
			}
			if !ok {
				if err := emit.Send(domain.ComputerUseStreamChunk{
					Kind:        "window_hidden",
					FrameIndex:  frameIndex,
					TimestampMs: now.UnixMilli(),
					Message:     "target window is not visible",
				}); err != nil {
					return nil
				}
				prevPixels = nil
				continue
			}

			opts := capture.CaptureOptions{
				Mode:          capture.ModeRegion,
				DisplayID:     target.displayID,
				Region:        target.abs,
				IncludeCursor: req.IncludeCursor,
				EncodeFormat:  "raw",
				Quality:       quality,
			}
			frame, err := c.Capture(opts)
			if err != nil {
				return fmt.Errorf("computeruse.stream: capture: %w", err)
			}
			w, h := frame.Bounds.Dx(), frame.Bounds.Dy()
			pixels := frame.RawPixels

			// Cursor position relative to captured frame.
			cursorX := frame.CursorPos.X - target.abs.Min.X
			cursorY := frame.CursorPos.Y - target.abs.Min.Y

			// Force keyframe if interval elapsed or dimensions changed.
			forceKey := now.Sub(lastKeyframe) >= keyframeInterval ||
				len(prevPixels) != len(pixels) || prevW != w || prevH != h

			if forceKey {
				data, err := enc.Encode(pixels, image.Rect(0, 0, w, h), quality)
				if err != nil {
					return fmt.Errorf("computeruse.stream: encode keyframe: %w", err)
				}
				chunk := domain.ComputerUseStreamChunk{
					Kind:        "keyframe",
					FrameIndex:  frameIndex,
					TimestampMs: now.UnixMilli(),
					FrameWidth:  int32(w),
					FrameHeight: int32(h),
					Tiles: []domain.ComputerUseStreamTile{{
						X: 0, Y: 0, Width: int32(w), Height: int32(h),
						ImageBytes: data,
					}},
					CursorX:       int32(cursorX),
					CursorY:       int32(cursorY),
					WindowOriginX: int32(target.originX),
					WindowOriginY: int32(target.originY),
				}
				if err := emit.Send(chunk); err != nil {
					return nil
				}
				prevPixels = append(prevPixels[:0], pixels...)
				prevW, prevH = w, h
				lastKeyframe = now
				continue
			}

			// Delta path.
			tiles := diff.TileCompare(prevPixels, pixels, w, h, tileSize, 30)
			out := make([]domain.ComputerUseStreamTile, 0, len(tiles))
			for _, t := range tiles {
				region, err := diff.ExtractRegion(pixels, w, h, t.Rect)
				if err != nil {
					continue
				}
				data, err := enc.Encode(region, image.Rect(0, 0, t.Rect.Dx(), t.Rect.Dy()), quality)
				if err != nil {
					continue
				}
				out = append(out, domain.ComputerUseStreamTile{
					X:          int32(t.Rect.Min.X),
					Y:          int32(t.Rect.Min.Y),
					Width:      int32(t.Rect.Dx()),
					Height:     int32(t.Rect.Dy()),
					ImageBytes: data,
				})
			}
			kind := "delta"
			if len(out) == 0 {
				kind = "heartbeat"
			}
			chunk := domain.ComputerUseStreamChunk{
				Kind:          kind,
				FrameIndex:    frameIndex,
				TimestampMs:   now.UnixMilli(),
				FrameWidth:    int32(w),
				FrameHeight:   int32(h),
				Tiles:         out,
				CursorX:       int32(cursorX),
				CursorY:       int32(cursorY),
				WindowOriginX: int32(target.originX),
				WindowOriginY: int32(target.originY),
			}
			if err := emit.Send(chunk); err != nil {
				return nil
			}
			prevPixels = append(prevPixels[:0], pixels...)
			prevW, prevH = w, h
		}
	}
}

// initStreamTarget validates and freezes the static fields of the target
// spec (mode, display, hwnd, region). resolveStreamRect computes the
// per-tick capture rectangle.
func initStreamTarget(c capture.Capturer, t *domain.ComputerUseWindowTarget) (streamTarget, error) {
	st := streamTarget{mode: "display"}
	if t != nil {
		st.mode = strings.ToLower(strings.TrimSpace(t.Mode))
		st.displayID = int(t.Display)
		st.hwnd = int(t.WindowID)
		if t.Region != nil {
			st.region = image.Rect(
				int(t.Region.X), int(t.Region.Y),
				int(t.Region.X+t.Region.Width), int(t.Region.Y+t.Region.Height))
		}
		if st.hwnd == 0 && t.WindowTitle != "" {
			wins, err := c.Windows()
			if err != nil {
				return st, err
			}
			needle := strings.ToLower(t.WindowTitle)
			for _, w := range wins {
				if strings.Contains(strings.ToLower(w.Title), needle) {
					st.hwnd = w.ID
					break
				}
			}
			if st.hwnd == 0 {
				return st, fmt.Errorf("window not found: %s", t.WindowTitle)
			}
		}
	}
	if st.mode == "" {
		st.mode = "display"
	}
	switch st.mode {
	case "display", "region", "window", "window_region":
	default:
		return st, fmt.Errorf("unknown stream target mode: %s", st.mode)
	}
	st.windowBased = st.mode == "window" || st.mode == "window_region"
	if st.windowBased && st.hwnd == 0 {
		return st, fmt.Errorf("windowID or windowTitle is required for mode=%s", st.mode)
	}
	if st.mode == "window_region" && st.region.Empty() {
		return st, fmt.Errorf("region is required for mode=window_region")
	}
	return st, nil
}

// resolveStreamRect computes the absolute screen rect to capture this
// tick. Returns (false, nil) when the target window is hidden/minimized.
func resolveStreamRect(c capture.Capturer, st *streamTarget) (bool, error) {
	switch st.mode {
	case "display":
		ds, err := c.Displays()
		if err != nil {
			return false, err
		}
		idx := st.displayID
		if idx < 0 || idx >= len(ds) {
			idx = 0
		}
		st.abs = ds[idx].Bounds
		st.originX, st.originY = 0, 0
		return true, nil
	case "region":
		if st.region.Empty() {
			return false, fmt.Errorf("region is required for mode=region")
		}
		st.abs = st.region
		st.originX, st.originY = 0, 0
		return true, nil
	case "window", "window_region":
		info, err := c.WindowInfo(st.hwnd)
		if err != nil {
			return false, nil // window vanished
		}
		if !info.IsVisible || info.State == "minimized" {
			return false, nil
		}
		st.originX, st.originY = info.Bounds.Min.X, info.Bounds.Min.Y
		if st.mode == "window" {
			st.abs = info.Bounds
			return true, nil
		}
		// window_region: region is in window-local coords.
		st.abs = image.Rect(
			info.Bounds.Min.X+st.region.Min.X,
			info.Bounds.Min.Y+st.region.Min.Y,
			info.Bounds.Min.X+st.region.Max.X,
			info.Bounds.Min.Y+st.region.Max.Y,
		)
		return true, nil
	}
	return false, fmt.Errorf("unknown mode: %s", st.mode)
}
