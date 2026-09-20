// Package browserinstance owns the state of one standalone browser window.
//
// Lifecycle:
//   - browsermanager spawns NewActor(cfg) with the persisted config.
//   - OnStart registers callables and, if the desktop WindowOperator is already
//     bound, creates the Wails WebviewWindow.
//   - The desktop service drives the actual window lifecycle; this actor holds
//     the durable state and exposes callables such as navigate and update_state.
//   - State changes are forwarded to browsermanager via the internal sync_instance
//     callable so the manager remains the single source of persisted truth.
package browserinstance

import (
	"context"
	"fmt"
	"sync"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// WindowOperator abstracts the desktop-side Wails window operations so the
// actor package stays testable and does not depend on Wails directly.
type WindowOperator interface {
	Create(id string, cfg domain.BrowserInstanceConfig) error
	Close(id string) error
	Navigate(id, url string) error
	UpdateConfig(id string, cfg domain.BrowserInstanceConfig) error
	DeleteProfile(id string) error
	// Observe injects a constrained JS script that extracts the current page
	// state (URL, title, scroll, elements with stable refs) and returns it as a
	// structured BrowserPageObservation. The returned observation contains a
	// fresh observation ID that is used to correlate subsequent Use calls.
	Observe(id string) (*domain.BrowserPageObservation, error)
	// Use executes a structured browser action (click/type/scroll/wait) using
	// a constrained JS script. elementID is a ref from a prior Observe call.
	// Returns the action result and an updated BrowserPageObservation.
	Use(id string, req domain.BrowserUseReq) (*domain.BrowserUseResp, error)
	// ExportCookies enumerates all cookies from the instance's WebView2 profile
	// and returns them grouped by domain.
	ExportCookies(id string) (map[string][]domain.BrowserCookieEntry, error)
	// ImportCookies writes domain-grouped cookies into the instance's WebView2
	// profile and returns the number of cookies successfully written.
	ImportCookies(id string, cookies map[string][]domain.BrowserCookieEntry) (int64, error)
	// RegisterExternal hands an externally-owned WebView2 window (e.g. the
	// desktop Global tab) to the operator so Observe/Use/Navigate act on it,
	// WITHOUT creating a new window. The window keeps its owner's lifecycle
	// (MessageHandler, close path, geometry events); the operator only tracks
	// it by id.
	RegisterExternal(id string, win ExternalWindow) error
}

// ExternalWindow is the minimal surface of an externally-owned WebView2 window
// that the operator needs for observation and automation. The desktop
// service's *application.WebviewWindow satisfies it; using this interface
// instead of the concrete Wails type keeps the actor package independent of
// Wails.
type ExternalWindow interface {
	// ExecJS executes JavaScript in the window's webview.
	ExecJS(script string)
	// Close closes the window.
	Close()
}

// SetWindowOperator registers the desktop window operator. Call this from the
// desktop service once the Wails application is bound. It is safe to call
// before any browserinstance actors start; they will simply skip window
// creation until the operator is available. The operator is automatically
// wrapped with timeout protection.
func SetWindowOperator(op WindowOperator) {
	globalOperatorMu.Lock()
	if op != nil {
		globalOperator = NewTimeoutOperator(op)
	} else {
		globalOperator = nil
	}
	globalOperatorMu.Unlock()
}

// GetWindowOperator returns the currently registered operator, if any.
func GetWindowOperator() WindowOperator {
	globalOperatorMu.RLock()
	defer globalOperatorMu.RUnlock()
	return globalOperator
}

// Actor holds the state for one browser window instance.
type Actor struct {
	actor.Host

	mu      sync.Mutex
	cfg     domain.BrowserInstanceConfig
	removed bool
}

// NewActor returns a factory closure that captures the seed config.
func NewActor(cfg domain.BrowserInstanceConfig) func() actor.Actor {
	return func() actor.Actor {
		return &Actor{cfg: cfg}
	}
}

func (a *Actor) Type() string { return "browserinstance" }

func (a *Actor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("browserinstance.status", a.handleStatus, actor.Public()); err != nil {
		return fmt.Errorf("browserinstance: register status: %w", err)
	}
	if err := ctx.Register("browserinstance.update_state", a.handleUpdateState, actor.AdminOnly()); err != nil {
		return fmt.Errorf("browserinstance: register update_state: %w", err)
	}
	if err := ctx.Register("browserinstance.navigate", a.handleNavigate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("browserinstance: register navigate: %w", err)
	}
	if err := ctx.Register("browserinstance.sync", a.handleSync, actor.Internal()); err != nil {
		return fmt.Errorf("browserinstance: register sync: %w", err)
	}
	if err := ctx.Register("browserinstance.open", a.handleOpen, actor.Internal()); err != nil {
		return fmt.Errorf("browserinstance: register open: %w", err)
	}
	if err := ctx.Register("browserinstance.close", a.handleClose, actor.Internal()); err != nil {
		return fmt.Errorf("browserinstance: register close: %w", err)
	}
	if err := ctx.Register("browserinstance.remove", a.handleRemove, actor.Internal()); err != nil {
		return fmt.Errorf("browserinstance: register remove: %w", err)
	}
	if err := ctx.Register("browserinstance.export_cookies", a.handleExportCookies, actor.AdminOnly()); err != nil {
		return fmt.Errorf("browserinstance: register export_cookies: %w", err)
	}
	if err := ctx.Register("browserinstance.import_cookies", a.handleImportCookies, actor.AdminOnly()); err != nil {
		return fmt.Errorf("browserinstance: register import_cookies: %w", err)
	}

	// If the operator is already bound (runtime-created instance), create the
	// window immediately. During app startup the operator is bound later by the
	// desktop service, which will then explicitly restore windows.
	// Tab-mode instances are rendered in the right panel and have no standalone
	// window; window-mode instances (and legacy configs with empty Mode) open
	// their own Wails window.
	if op := GetWindowOperator(); op != nil && a.cfg.Open && a.cfg.Mode != "tab" {
		if err := op.Create(a.cfg.ID, a.cfg); err != nil {
			ctx.Logger().Error("browserinstance: create window failed", "id", a.cfg.ID, "error", err)
		}
	}

	return nil
}

func (a *Actor) OnStop(ctx actor.Context) error {
	if op := GetWindowOperator(); op != nil {
		_ = op.Close(a.cfg.ID)
		if a.removed {
			_ = op.DeleteProfile(a.cfg.ID)
		}
	}
	return nil
}

// handleStatus is a stateless (PureContext) snapshot read: it takes the
// instance mutex and returns a composed view without mutating any state, so
// it runs on the forked pure loop and never parks the owner lane.
func (a *Actor) handleStatus(_ actor.PureContext) (domain.BrowserInstance, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.composeInstanceLocked(), nil
}

func (a *Actor) handleUpdateState(ctx actor.Context, cfg domain.BrowserInstanceConfig) (domain.BrowserInstance, error) {
	if cfg.ID == "" {
		cfg.ID = a.cfg.ID
	}
	if cfg.ID != a.cfg.ID {
		return domain.BrowserInstance{}, fmt.Errorf("browserinstance.update_state: id mismatch %q != %q", cfg.ID, a.cfg.ID)
	}

	a.mu.Lock()
	previous := a.cfg
	a.cfg = cfg
	a.mu.Unlock()

	if op := GetWindowOperator(); op != nil && cfg.Mode != "tab" {
		switch {
		case !previous.Open && cfg.Open:
			if err := op.Create(cfg.ID, cfg); err != nil {
				ctx.Logger().Error("browserinstance: open window failed", "id", cfg.ID, "error", err)
			}
		case previous.Open && !cfg.Open:
			if err := op.Close(cfg.ID); err != nil {
				ctx.Logger().Error("browserinstance: close window failed", "id", cfg.ID, "error", err)
			}
		case cfg.Open:
			// If the effective proxy changed, close+reopen so the new
			// --proxy-server/--no-proxy-server takes effect at the WebView2
			// creation level.  Otherwise just update the in-memory config.
			if ProxyChanged(previous, cfg) {
				if err := op.Close(cfg.ID); err != nil {
					ctx.Logger().Error("browserinstance: close window for proxy update failed", "id", cfg.ID, "error", err)
				}
				if err := op.Create(cfg.ID, cfg); err != nil {
					ctx.Logger().Error("browserinstance: recreate window for proxy change failed", "id", cfg.ID, "error", err)
				}
			} else {
				if err := op.UpdateConfig(cfg.ID, cfg); err != nil {
					ctx.Logger().Error("browserinstance: update window failed", "id", cfg.ID, "error", err)
				}
			}
		}
	}

	a.syncToManager(ctx, cfg)

	return a.composeInstanceLocked(), nil
}

func (a *Actor) handleNavigate(ctx actor.Context, req domain.BrowserManagerNavigateReq) (domain.BrowserInstance, error) {
	if req.ID == "" {
		req.ID = a.cfg.ID
	}
	if req.ID != a.cfg.ID {
		return domain.BrowserInstance{}, fmt.Errorf("browserinstance.navigate: id mismatch %q != %q", req.ID, a.cfg.ID)
	}

	a.mu.Lock()
	// Navigation moves the current page (State.URL) only; Config.URL is the
	// settings page (home) and must survive navigation.
	a.cfg.State.URL = req.URL
	cfg := a.cfg
	a.mu.Unlock()

	if op := GetWindowOperator(); op != nil {
		if err := op.Navigate(cfg.ID, req.URL); err != nil {
			ctx.Logger().Error("browserinstance: navigate window failed", "id", cfg.ID, "url", req.URL, "error", err)
		}
	}

	a.syncToManager(ctx, cfg)

	return a.composeInstanceLocked(), nil
}

func (a *Actor) handleExportCookies(ctx actor.Context, req gen.BrowserManagerExportCookiesReq) (gen.BrowserManagerExportCookiesResp, error) {
	if req.ID == "" {
		req.ID = a.cfg.ID
	}
	if req.ID != a.cfg.ID {
		return gen.BrowserManagerExportCookiesResp{}, fmt.Errorf("browserinstance.export_cookies: id mismatch %q != %q", req.ID, a.cfg.ID)
	}
	op := GetWindowOperator()
	if op == nil {
		return gen.BrowserManagerExportCookiesResp{}, fmt.Errorf("browserinstance.export_cookies: browser operator not available")
	}
	cookies, err := op.ExportCookies(req.ID)
	if err != nil {
		return gen.BrowserManagerExportCookiesResp{}, fmt.Errorf("browserinstance.export_cookies: %w", err)
	}
	return gen.BrowserManagerExportCookiesResp{Cookies: cookies}, nil
}

func (a *Actor) handleImportCookies(ctx actor.Context, req gen.BrowserManagerImportCookiesReq) (gen.BrowserManagerImportCookiesResp, error) {
	if req.ID == "" {
		req.ID = a.cfg.ID
	}
	if req.ID != a.cfg.ID {
		return gen.BrowserManagerImportCookiesResp{}, fmt.Errorf("browserinstance.import_cookies: id mismatch %q != %q", req.ID, a.cfg.ID)
	}
	op := GetWindowOperator()
	if op == nil {
		return gen.BrowserManagerImportCookiesResp{}, fmt.Errorf("browserinstance.import_cookies: browser operator not available")
	}
	imported, err := op.ImportCookies(req.ID, req.Cookies)
	if err != nil {
		return gen.BrowserManagerImportCookiesResp{}, fmt.Errorf("browserinstance.import_cookies: %w", err)
	}
	return gen.BrowserManagerImportCookiesResp{Imported: imported}, nil
}

func (a *Actor) handleSync(ctx actor.Context, cfg domain.BrowserInstanceConfig) (domain.BrowserInstance, error) {
	if cfg.ID == "" {
		cfg.ID = a.cfg.ID
	}
	if cfg.ID != a.cfg.ID {
		return domain.BrowserInstance{}, fmt.Errorf("browserinstance.sync: id mismatch %q != %q", cfg.ID, a.cfg.ID)
	}
	a.mu.Lock()
	previous := a.cfg
	a.cfg = cfg
	a.mu.Unlock()

	// --proxy-server/--no-proxy-server are WebView2 creation-time parameters.
	// If the effective proxy changed while the window is open, close and
	// recreate so the new value takes effect.  A closed instance just holds the
	// updated config.
	if cfg.Open && cfg.Mode != "tab" && ProxyChanged(previous, cfg) {
		if op := GetWindowOperator(); op != nil {
			_ = op.Close(cfg.ID)
			if err := op.Create(cfg.ID, cfg); err != nil {
				ctx.Logger().Error("browserinstance: recreate window for proxy change failed", "id", cfg.ID, "error", err)
			}
		}
	}

	return a.composeInstanceLocked(), nil
}

func (a *Actor) handleOpen(ctx actor.Context) (domain.BrowserInstance, error) {
	a.mu.Lock()
	a.cfg.Open = true
	cfg := a.cfg
	a.mu.Unlock()

	if op := GetWindowOperator(); op != nil && cfg.Mode != "tab" {
		if err := op.Create(cfg.ID, cfg); err != nil {
			ctx.Logger().Error("browserinstance: open window failed", "id", cfg.ID, "error", err)
		}
	}

	a.syncToManager(ctx, cfg)

	return a.composeInstanceLocked(), nil
}

func (a *Actor) handleClose(ctx actor.Context) (domain.BrowserInstance, error) {
	a.mu.Lock()
	a.cfg.Open = false
	cfg := a.cfg
	a.mu.Unlock()

	if op := GetWindowOperator(); op != nil && cfg.Mode != "tab" {
		if err := op.Close(cfg.ID); err != nil {
			ctx.Logger().Error("browserinstance: close window failed", "id", cfg.ID, "error", err)
		}
	}

	a.syncToManager(ctx, cfg)

	return a.composeInstanceLocked(), nil
}
func (a *Actor) handleRemove(ctx actor.Context) (domain.BrowserInstance, error) {
	a.mu.Lock()
	a.cfg.Open = false
	a.removed = true
	cfg := a.cfg
	a.mu.Unlock()

	if op := GetWindowOperator(); op != nil {
		_ = op.Close(cfg.ID)
		_ = op.DeleteProfile(cfg.ID)
	}

	a.syncToManager(ctx, cfg)

	return a.composeInstanceLocked(), nil
}

func (a *Actor) syncToManager(ctx actor.Context, cfg domain.BrowserInstanceConfig) {
	mgrRef, ok := ctx.LookupService("browsermanager")
	if !ok {
		return
	}
	go func() {
		stream := mgrRef.Invoke(context.Background(), "browsermanager.sync_instance", cfg)
		if stream == nil {
			return
		}
		defer stream.Close()
		_, _ = stream.RecvRaw()
	}()
}

func (a *Actor) composeInstanceLocked() domain.BrowserInstance {
	cfg := a.cfg
	// Status.URL is the current navigation page (State.URL); Config.URL is the
	// settings page. Legacy conflated records keep State.URL == Config.URL.
	url := cfg.State.URL
	if url == "" {
		url = cfg.URL
	}
	return domain.BrowserInstance{
		Config: cfg,
		Status: domain.BrowserInstanceStatus{
			ID:    cfg.ID,
			Open:  cfg.Open,
			URL:   url,
			Title: cfg.State.Title,
		},
	}
}
