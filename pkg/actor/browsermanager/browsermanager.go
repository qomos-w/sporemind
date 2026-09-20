// Package browsermanager is the CRUD manager for standalone Wails browser windows.
//
// Topology:
//
//	/browsermanager                # this actor; holds []BrowserInstanceConfig
//	├── /browsermanager/inst-1     # one WebviewWindow per persisted config
//	├── /browsermanager/inst-2
//	└── ...
//
// Instances are spawned/respawned by this actor; the durable record lives
// here, the runtime window state lives on each browserinstance child.
package browsermanager

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"

	"github.com/qomos-w/sporemind/pkg/actor/browserinstance"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/policy"
)

const childInvokeTimeout = domain.DefaultInvokeTimeout

// Actor manages a list of BrowserInstanceConfig and spawns one browserinstance
// child per entry.
type Actor struct {
	actor.Host
	store persist.Persist
	// stateLoaded is set once Load() has completed (first start included);
	// OnStop skips the save before that (gospore ForceCleanup may stop us
	// mid-OnInit, and saving zero-value Instances would wipe the record).
	stateLoaded atomic.Bool
	Instances   []domain.BrowserInstanceConfig `gospore:"component,admin"`

	actorID       string
	nextID        int64
	mu            sync.Mutex
	childActorIDs map[string]string // instance ID -> actor ID
}

var _ persist.Persistent = (*Actor)(nil)

type managerSnapshot struct {
	Instances []domain.BrowserInstanceConfig `json:"instances"`
	NextID    int64                          `json:"nextId"`
}

func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("browsermanager"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	a.childActorIDs = make(map[string]string)
	if err := a.Load(); err != nil {
		ctx.Logger().Error("browsermanager: load state failed", "error", err)
	}
	// Preserve the Open flag from persisted state so browser windows that were
	// open when the app shut down are restored on the next startup. Tab-closing
	// deletes the instance via browsermanager.remove.
	return nil
}

func (a *Actor) Type() string { return "browsermanager" }

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("browsermanager: starting", "id", a.actorID, "instances", len(a.Instances))

	if err := ctx.Register("browsermanager.list", a.handleList, actor.Public()); err != nil {
		return fmt.Errorf("browsermanager: register list: %w", err)
	}
	if err := ctx.Register("browsermanager.get", a.handleGet, actor.Public()); err != nil {
		return fmt.Errorf("browsermanager: register get: %w", err)
	}
	if err := ctx.Register("browsermanager.create", a.handleCreate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("browsermanager: register create: %w", err)
	}
	if err := ctx.Register("browsermanager.update", a.handleUpdate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("browsermanager: register update: %w", err)
	}
	if err := ctx.Register("browsermanager.remove", a.handleRemove, actor.AdminOnly()); err != nil {
		return fmt.Errorf("browsermanager: register remove: %w", err)
	}
	if err := ctx.Register("browsermanager.navigate", a.handleNavigate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("browsermanager: register navigate: %w", err)
	}
	if err := ctx.Register("browsermanager.open", a.handleOpen, actor.AdminOnly()); err != nil {
		return fmt.Errorf("browsermanager: register open: %w", err)
	}
	if err := ctx.Register("browsermanager.open_global", a.handleOpenGlobal, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("browsermanager: register open_global: %w", err)
	}
	if err := ctx.Register("browsermanager.sync_instance", a.handleSyncInstance, actor.Internal()); err != nil {
		return fmt.Errorf("browsermanager: register sync_instance: %w", err)
	}
	if err := ctx.Register("browsermanager.internal_update_state", a.handleInternalUpdateState, actor.Internal()); err != nil {
		return fmt.Errorf("browsermanager: register internal.update_state: %w", err)
	}
	if err := ctx.Register("browsermanager.internal_remove", a.handleInternalRemove, actor.Internal()); err != nil {
		return fmt.Errorf("browsermanager: register internal.remove: %w", err)
	}
	if err := ctx.Register("browsermanager.internal_create", a.handleInternalCreate, actor.Internal()); err != nil {
		return fmt.Errorf("browsermanager: register internal.create: %w", err)
	}
	if err := ctx.Register("browsermanager.internal_close", a.handleInternalClose, actor.Internal()); err != nil {
		return fmt.Errorf("browsermanager: register internal.close: %w", err)
	}
	if err := ctx.Register("browsermanager.use", a.handleUse,
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Drive a browser instance by action name (observe, navigate, click, type, ...). InstanceID defaults to \"global\" — the shared desktop browser session. Read-only observe actions return the page state; interactive actions re-observe after acting so each response carries the updated page state (observe → act closed loop)."),
	); err != nil {
		return fmt.Errorf("browsermanager: register use: %w", err)
	}
	if err := ctx.Register("browsermanager.export_cookies", a.handleExportCookies, actor.AdminOnly()); err != nil {
		return fmt.Errorf("browsermanager: register export_cookies: %w", err)
	}
	if err := ctx.Register("browsermanager.import_cookies", a.handleImportCookies, actor.AdminOnly()); err != nil {
		return fmt.Errorf("browsermanager: register import_cookies: %w", err)
	}

	_ = ctx.RegisterEventKind("browser_manager_event", domain.BrowserManagerEvent{}, actor.Public())

	if err := ctx.RegisterDomain("browsermanager").Expose(); err != nil {
		return fmt.Errorf("browsermanager: expose: %w", err)
	}

	// Re-spawn persisted children so the tree survives process restart.
	for _, cfg := range a.Instances {
		props := actor.PropsFromFunc(browserinstance.NewActor(cfg)).WithAsyncStart()
		if spawned, err := ctx.Spawn(props, cfg.ID); err != nil {
			ctx.Logger().Error("browsermanager: re-spawn browserinstance failed", "id", cfg.ID, "name", cfg.Name, "error", err)
		} else {
			a.childActorIDs[cfg.ID] = spawned.ID().String()
			ctx.Logger().Info("browsermanager: re-spawned browserinstance", "id", cfg.ID, "name", cfg.Name)
		}
	}

	return nil
}

func (a *Actor) OnStop(ctx actor.Context) error {
	if !a.stateLoaded.Load() {
		// Stopped mid-OnInit (gospore ForceCleanup): skip the wipe-prone save.
		return nil
	}
	return a.Save()
}

// Save snapshots Instances + nextID.
func (a *Actor) Save() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("browsermanager"))
		if err != nil {
			return err
		}
	}
	a.mu.Lock()
	snapshot := managerSnapshot{
		Instances: append([]domain.BrowserInstanceConfig(nil), a.Instances...),
		NextID:    a.nextID,
	}
	a.mu.Unlock()
	return a.store.Save(a.actorID, snapshot)
}

func (a *Actor) Load() (err error) {
	// Mark state as load-complete only after a successful pass (first start
	// included) — the OnStop wipe guard depends on it.
	defer func() {
		if err == nil {
			a.stateLoaded.Store(true)
		}
	}()
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("browsermanager"))
		if err != nil {
			return err
		}
	}
	var snapshot managerSnapshot
	if err := persist.LoadOrZero(a.store, a.actorID, &snapshot); err != nil {
		return err
	}
	a.Instances = snapshot.Instances
	a.nextID = snapshot.NextID

	// Drop the legacy "global" pseudo-instance persisted by the old
	// browsermanager.open_global implementation. It was never a real
	// user-managed browser; on every restart it respawned a browserinstance
	// child actor and restored as an "independent" browser named Global.
	// User-created instances always get inst-N ids, so the "global" id is
	// unambiguously this artifact.
	kept := a.Instances[:0]
	for _, c := range a.Instances {
		if c.ID != "global" {
			kept = append(kept, c)
		}
	}
	a.Instances = kept

	// Migrate persisted window-mode instances to tabs: the standalone-window
	// rendering mode was removed, every user instance now renders as a
	// right-panel tab. "window" survives only as a transient crawl-engine
	// value, so leftover crawl instances become dormant tab configs too. Like
	// the global drop above, the correction persists on the next save.
	for i := range a.Instances {
		if a.Instances[i].Mode != "tab" {
			a.Instances[i].Mode = "tab"
		}
	}

	var maxN int64 = -1
	for _, c := range a.Instances {
		var n int64
		if _, err := fmt.Sscanf(c.ID, "inst-%d", &n); err == nil && n > maxN {
			maxN = n
		}
	}
	if a.nextID <= maxN {
		a.nextID = maxN + 1
	}
	return nil
}

func (a *Actor) saveOrLog(ctx actor.Context) {
	if err := a.Save(); err != nil {
		ctx.Logger().Error("browsermanager: save state failed", "error", err)
	}
}

func (a *Actor) handleList(_ actor.PureContext) (domain.BrowserManagerListResp, error) {
	a.mu.Lock()
	snap := append([]domain.BrowserInstanceConfig(nil), a.Instances...)
	a.mu.Unlock()

	out := make([]domain.BrowserInstance, 0, len(snap))
	for _, cfg := range snap {
		out = append(out, a.composeInstance(cfg))
	}
	return domain.BrowserManagerListResp{Items: out}, nil
}

// asyncResult is returned immediately by mutating callables. The actual
// window operation continues in the background and completes via a
// browser_manager_event.
type asyncResult = domain.BrowserManagerAsyncResult

// handleGet is a stateless (PureContext) snapshot read: it scans the
// instances slice under the manager mutex and composes a view without
// mutating state, so it runs on the forked pure loop.
func (a *Actor) handleGet(_ actor.PureContext, req domain.BrowserManagerGetReq) (domain.BrowserInstance, error) {
	a.mu.Lock()
	var cfg domain.BrowserInstanceConfig
	var found bool
	for _, c := range a.Instances {
		if c.ID == req.ID {
			cfg = c
			found = true
			break
		}
	}
	a.mu.Unlock()
	if !found {
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.get: instance %q not found", req.ID)
	}
	return a.composeInstance(cfg), nil
}

func (a *Actor) handleCreate(ctx actor.Context, req domain.BrowserManagerCreateReq) (domain.BrowserManagerAsyncResult, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return asyncResult{}, err
	}
	if req.Name == "" {
		return asyncResult{}, fmt.Errorf("browsermanager.create: name is required")
	}
	if req.URL == "" {
		return asyncResult{}, fmt.Errorf("browsermanager.create: url is required")
	}

	a.mu.Lock()
	for _, existing := range a.Instances {
		if existing.Name == req.Name {
			a.mu.Unlock()
			return asyncResult{}, fmt.Errorf("browsermanager.create: name %q already exists", req.Name)
		}
	}
	cfg := domain.BrowserInstanceConfig{
		ID:        fmt.Sprintf("inst-%d", a.nextID),
		Name:      req.Name,
		URL:       req.URL,
		Open:      true,
		Proxy:     req.Proxy,
		ProxyMode: req.ProxyMode,
		// User-managed instances always render as right-panel tabs; only the
		// crawl engine (internal_create) gets real windows.
		Mode:   "tab",
		Hidden: req.Hidden,
		State:  domain.BrowserWindowState{URL: req.URL, Title: req.Name, Width: 1024, Height: 768},
	}
	a.Instances = append(a.Instances, cfg)
	a.nextID++
	a.mu.Unlock()
	a.saveOrLog(ctx)

	inst := a.composeInstance(cfg)

	// The heavy work of spawning the child and creating the WebView2 window runs
	// in the background so the actor mailbox and the client request return fast.
	go a.finishCreate(ctx, cfg)

	return asyncResult{Accepted: true, Instance: inst}, nil
}

func (a *Actor) finishCreate(ctx actor.Context, cfg domain.BrowserInstanceConfig) {
	errFunc := func(err error) {
		ctx.Logger().Error("browsermanager.create: background window creation failed", "id", cfg.ID, "error", err)
		// Roll back the persisted record so we don't leave an orphan entry.
		a.mu.Lock()
		for i, c := range a.Instances {
			if c.ID == cfg.ID {
				a.Instances = append(a.Instances[:i], a.Instances[i+1:]...)
				break
			}
		}
		delete(a.childActorIDs, cfg.ID)
		a.mu.Unlock()
		a.saveOrLog(ctx)
		a.emitEvent(ctx, "create_failed", cfg, err)
	}

	props := actor.PropsFromFunc(browserinstance.NewActor(cfg)).WithAsyncStart()
	spawned, err := ctx.Spawn(props, cfg.ID)
	if err != nil {
		errFunc(fmt.Errorf("browsermanager.create: spawn browserinstance: %w", err))
		return
	}
	a.mu.Lock()
	a.childActorIDs[cfg.ID] = spawned.ID().String()
	a.mu.Unlock()
	ctx.Logger().Info("browsermanager.create", "id", cfg.ID, "name", cfg.Name, "url", cfg.URL)

	a.mu.Lock()
	idx := -1
	for i, c := range a.Instances {
		if c.ID == cfg.ID {
			idx = i
			break
		}
	}
	if idx >= 0 {
		cfg = a.Instances[idx]
	}
	a.mu.Unlock()
	a.emitEvent(ctx, "created", cfg, nil)
}

func (a *Actor) handleRemove(ctx actor.Context, req domain.BrowserManagerRemoveReq) (domain.BrowserManagerAsyncResult, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return asyncResult{}, err
	}
	a.mu.Lock()
	var removed domain.BrowserInstanceConfig
	var found bool
	for i, c := range a.Instances {
		if c.ID == req.ID {
			removed = c
			a.Instances = append(a.Instances[:i], a.Instances[i+1:]...)
			found = true
			break
		}
	}
	a.mu.Unlock()
	if !found {
		return asyncResult{}, fmt.Errorf("browsermanager.remove: instance %q not found", req.ID)
	}
	a.saveOrLog(ctx)

	inst := domain.BrowserInstance{
		Config: removed,
		Status: domain.BrowserInstanceStatus{
			ID:    removed.ID,
			Open:  false,
			URL:   removed.URL,
			Title: removed.State.Title,
		},
	}
	go a.finishRemove(ctx, removed)
	return asyncResult{Accepted: true, Instance: inst}, nil
}

func (a *Actor) finishRemove(ctx actor.Context, removed domain.BrowserInstanceConfig) {
	a.mu.Lock()
	actorIDStr, ok := a.childActorIDs[removed.ID]
	if ok {
		delete(a.childActorIDs, removed.ID)
	}
	a.mu.Unlock()
	if ok {
		canonical, err := identity.ParseCanonicalID(actorIDStr)
		if err == nil {
			if childRef, ok := ctx.LookupID(id.From(canonical)); ok && childRef != nil {
				_ = ctx.Destroy(childRef)
			}
		}
	}
	if op := browserinstance.GetWindowOperator(); op != nil {
		_ = op.DeleteProfile(removed.ID)
	}
	ctx.Logger().Info("browsermanager.remove", "id", removed.ID, "name", removed.Name)
	a.emitEvent(ctx, "removed", removed, nil)
}

func (a *Actor) handleInternalRemove(ctx actor.Context, req domain.BrowserManagerRemoveReq) (domain.BrowserInstance, error) {
	return a.doRemove(ctx, req.ID)
}

func (a *Actor) doRemove(ctx actor.Context, instanceID string) (domain.BrowserInstance, error) {
	a.mu.Lock()
	var removed domain.BrowserInstanceConfig
	var found bool
	for i, c := range a.Instances {
		if c.ID == instanceID {
			removed = c
			a.Instances = append(a.Instances[:i], a.Instances[i+1:]...)
			found = true
			break
		}
	}
	a.mu.Unlock()
	if !found {
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.remove: instance %q not found", instanceID)
	}
	a.saveOrLog(ctx)

	if actorIDStr, ok := a.childActorIDs[removed.ID]; ok {
		canonical, err := identity.ParseCanonicalID(actorIDStr)
		if err == nil {
			if childRef, ok := ctx.LookupID(id.From(canonical)); ok && childRef != nil {
				stream := childRef.Invoke(ctx.Lifecycle(), "browserinstance.remove", nil)
				if stream != nil {
					defer stream.Close()
					_, _ = stream.RecvRaw()
				}
				_ = ctx.Destroy(childRef)
			}
		}
		delete(a.childActorIDs, removed.ID)
	}
	if op := browserinstance.GetWindowOperator(); op != nil {
		_ = op.DeleteProfile(removed.ID)
	}
	ctx.Logger().Info("browsermanager.remove", "id", removed.ID, "name", removed.Name)

	return domain.BrowserInstance{
		Config: removed,
		Status: domain.BrowserInstanceStatus{
			ID:    removed.ID,
			Open:  false,
			URL:   removed.URL,
			Title: removed.State.Title,
		},
	}, nil
}

// handleInternalCreate creates or reconfigures a browser instance for internal
// consumers such as the crawl engine. If an instance with the provided ID
// already exists, its persisted config is replaced and the window is recreated
// so callers can switch modes (e.g. hidden -> visible handoff) without
// duplicating records. The profile directory is never deleted here.
func (a *Actor) handleInternalCreate(ctx actor.Context, cfg domain.BrowserInstanceConfig) (domain.BrowserInstance, error) {
	if cfg.Name == "" {
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.internal_create: name is required")
	}
	if cfg.URL == "" {
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.internal_create: url is required")
	}

	a.mu.Lock()
	existingIdx := -1
	for i, existing := range a.Instances {
		if existing.ID == cfg.ID && cfg.ID != "" {
			existingIdx = i
			break
		}
	}
	if existingIdx < 0 {
		for _, existing := range a.Instances {
			if existing.Name == cfg.Name {
				a.mu.Unlock()
				return domain.BrowserInstance{}, fmt.Errorf("browsermanager.internal_create: name %q already exists", cfg.Name)
			}
		}
	}
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("inst-%d", a.nextID)
		a.nextID++
	}
	cfg.Open = true
	if existingIdx >= 0 {
		old := a.Instances[existingIdx]
		cfg.Name = old.Name // name is managed by the manager
		a.Instances[existingIdx] = cfg
	} else {
		a.Instances = append(a.Instances, cfg)
	}
	a.mu.Unlock()
	a.saveOrLog(ctx)

	if existingIdx >= 0 {
		// Recreate the window with the updated config so mode changes such as
		// hidden -> visible take effect immediately. The operator is the source
		// of truth for the live window; the child actor syncs from its events.
		if op := browserinstance.GetWindowOperator(); op != nil && cfg.Mode != "tab" {
			_ = op.Close(cfg.ID)
			if err := op.Create(cfg.ID, cfg); err != nil {
				ctx.Logger().Error("browsermanager.internal_create: recreate window failed", "id", cfg.ID, "error", err)
			}
		}
		// Sync the updated config to the existing child actor if it is present.
		a.pushConfigure(ctx, cfg)
		ctx.Logger().Info("browsermanager.internal_create: reconfigured", "id", cfg.ID, "name", cfg.Name, "url", cfg.URL, "hidden", cfg.Hidden)
		return a.composeInstance(cfg), nil
	}

	props := actor.PropsFromFunc(browserinstance.NewActor(cfg)).WithAsyncStart()
	spawned, err := ctx.Spawn(props, cfg.ID)
	if err != nil {
		// Roll back the persisted record so we don't leave an orphan entry.
		a.mu.Lock()
		for i, c := range a.Instances {
			if c.ID == cfg.ID {
				a.Instances = append(a.Instances[:i], a.Instances[i+1:]...)
				break
			}
		}
		a.mu.Unlock()
		a.saveOrLog(ctx)
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.internal_create: spawn browserinstance: %w", err)
	}
	a.mu.Lock()
	a.childActorIDs[cfg.ID] = spawned.ID().String()
	a.mu.Unlock()

	ctx.Logger().Info("browsermanager.internal_create", "id", cfg.ID, "name", cfg.Name, "url", cfg.URL, "hidden", cfg.Hidden)
	return a.composeInstance(cfg), nil
}

// handleInternalClose closes the live window for an instance and marks it as not
// open, but keeps the persisted record and the profile directory. This is used by
// the crawl engine to release the profile lock while waiting for login handoff.
func (a *Actor) handleInternalClose(ctx actor.Context, req domain.BrowserManagerRemoveReq) (domain.BrowserInstance, error) {
	a.mu.Lock()
	idx := -1
	for i, c := range a.Instances {
		if c.ID == req.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.internal_close: instance %q not found", req.ID)
	}
	cfg := a.Instances[idx]
	cfg.Open = false
	a.Instances[idx] = cfg
	a.mu.Unlock()
	a.saveOrLog(ctx)

	if op := browserinstance.GetWindowOperator(); op != nil {
		_ = op.Close(cfg.ID)
	}

	if actorIDStr, ok := a.childActorIDs[cfg.ID]; ok {
		canonical, err := identity.ParseCanonicalID(actorIDStr)
		if err == nil {
			if childRef, ok := ctx.LookupID(id.From(canonical)); ok && childRef != nil {
				stream := childRef.Invoke(ctx.Lifecycle(), "browserinstance.close", nil)
				if stream != nil {
					defer stream.Close()
					_, _ = stream.RecvRaw()
				}
			}
		}
	}

	ctx.Logger().Info("browsermanager.internal_close", "id", cfg.ID)
	return a.composeInstance(cfg), nil
}

func (a *Actor) handleUpdate(ctx actor.Context, req domain.BrowserManagerUpdateReq) (domain.BrowserInstance, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.BrowserInstance{}, err
	}
	a.mu.Lock()
	idx := -1
	for i, c := range a.Instances {
		if c.ID == req.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.update: instance %q not found", req.ID)
	}

	cfg := req.Config
	cfg.ID = req.ID
	if cfg.Name == "" {
		a.mu.Unlock()
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.update: name is required")
	}
	for i, other := range a.Instances {
		if i != idx && other.Name == cfg.Name {
			a.mu.Unlock()
			return domain.BrowserInstance{}, fmt.Errorf("browsermanager.update: name %q already exists", cfg.Name)
		}
	}
	a.Instances[idx] = cfg
	a.mu.Unlock()
	a.saveOrLog(ctx)

	a.pushConfigure(ctx, cfg)
	ctx.Logger().Info("browsermanager.update", "id", cfg.ID, "name", cfg.Name)
	a.emitEvent(ctx, "updated", cfg, nil)
	return a.composeInstance(cfg), nil
}

func (a *Actor) handleNavigate(ctx actor.Context, req domain.BrowserManagerNavigateReq) (domain.BrowserManagerAsyncResult, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return asyncResult{}, err
	}
	a.mu.Lock()
	idx := -1
	for i, c := range a.Instances {
		if c.ID == req.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return asyncResult{}, fmt.Errorf("browsermanager.navigate: instance %q not found", req.ID)
	}
	cfg := a.Instances[idx]
	// Navigation moves the current page (State.URL) only. Config.URL is the
	// settings page (home) and must survive navigation.
	cfg.State.URL = req.URL
	a.Instances[idx] = cfg
	a.mu.Unlock()
	a.saveOrLog(ctx)

	inst := a.composeInstance(cfg)
	go a.finishNavigate(ctx, cfg)
	return asyncResult{Accepted: true, Instance: inst}, nil
}

func (a *Actor) finishNavigate(ctx actor.Context, cfg domain.BrowserInstanceConfig) {
	req := domain.BrowserManagerNavigateReq{ID: cfg.ID, URL: cfg.State.URL}
	a.mu.Lock()
	actorIDStr, ok := a.childActorIDs[cfg.ID]
	a.mu.Unlock()
	if ok {
		canonical, err := identity.ParseCanonicalID(actorIDStr)
		if err == nil {
			if childRef, ok := ctx.LookupID(id.From(canonical)); ok && childRef != nil {
				stream := childRef.Invoke(ctx.Lifecycle(), "browserinstance.navigate", req)
				if stream != nil {
					defer stream.Close()
					_, _ = stream.RecvRaw()
				}
			}
		}
	}
	ctx.Logger().Info("browsermanager.navigate", "id", cfg.ID, "url", cfg.State.URL)
	a.emitEvent(ctx, "navigated", cfg, nil)
}

func (a *Actor) handleOpenGlobal(ctx actor.Context, req domain.BrowserManagerOpenGlobalReq) (domain.BrowserManagerOpenGlobalResp, error) {
	url, err := normalizeGlobalURL(req.URL)
	if err != nil {
		return domain.BrowserManagerOpenGlobalResp{Opened: false, Error: err.Error()}, nil
	}
	if url == "" {
		return domain.BrowserManagerOpenGlobalResp{Opened: false, Error: "browsermanager.open_global: url is required"}, nil
	}

	// The global browser is shared workspace browsing state, NOT a persisted
	// BrowserManager instance (see "内置浏览器现状与重构目标": global 标签不作为
	// BrowserManager 的可持久化浏览器实例). Build a transient config carrying
	// only the URL, emit the open_global event so the desktop frontend opens /
	// navigates the shared global tab, and do NOT store it in Instances or
	// spawn a child actor. Persisting it made open_global materialize an
	// "independent" browser instance named global on every restart.
	cfg := domain.BrowserInstanceConfig{
		ID:    "global",
		Name:  "Global",
		URL:   url,
		Open:  true,
		Mode:  "tab",
		State: domain.BrowserWindowState{URL: url, Title: "Global", Width: 1024, Height: 768},
	}
	ctx.Logger().Info("browsermanager.open_global", "id", cfg.ID, "url", url)
	a.emitEvent(ctx, "open_global", cfg, nil)
	return domain.BrowserManagerOpenGlobalResp{Opened: true, URL: url}, nil
}

// normalizeGlobalURL standardizes raw agent input into a URL that the desktop
// global browser can load. It accepts full URLs and file:// paths; everything
// else is treated as a plain URL/keyword and left to the frontend to handle.
func normalizeGlobalURL(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}
	if strings.HasPrefix(input, "file://") || strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		return input, nil
	}
	// Windows drive-letter paths like C:\path or D:/path become file:// URLs.
	if len(input) >= 3 && input[1] == ':' && (input[2] == '\\' || input[2] == '/') {
		abs := filepath.ToSlash(input)
		return "file:///" + abs, nil
	}
	// Unix absolute path -> file://
	if strings.HasPrefix(input, "/") {
		return "file://" + filepath.ToSlash(input), nil
	}
	if strings.Contains(input, "://") {
		return input, nil
	}
	// Leave web-like shorthand (e.g. "example.com") as-is so the frontend can
	// prepend https:// if it chooses. Local relative paths should already have
	// been resolved by the agent before calling this callable.
	return input, nil
}

func (a *Actor) handleOpen(ctx actor.Context, req domain.BrowserManagerOpenReq) (domain.BrowserManagerAsyncResult, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return asyncResult{}, err
	}
	a.mu.Lock()
	idx := -1
	for i, c := range a.Instances {
		if c.ID == req.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return asyncResult{}, fmt.Errorf("browsermanager.open: instance %q not found", req.ID)
	}
	cfg := a.Instances[idx]
	cfg.Open = true
	a.Instances[idx] = cfg
	a.mu.Unlock()
	a.saveOrLog(ctx)

	inst := a.composeInstance(cfg)
	go a.finishOpen(ctx, cfg)
	return asyncResult{Accepted: true, Instance: inst}, nil
}

func (a *Actor) finishOpen(ctx actor.Context, cfg domain.BrowserInstanceConfig) {
	a.mu.Lock()
	actorIDStr, ok := a.childActorIDs[cfg.ID]
	a.mu.Unlock()
	if ok {
		canonical, err := identity.ParseCanonicalID(actorIDStr)
		if err == nil {
			if childRef, ok := ctx.LookupID(id.From(canonical)); ok && childRef != nil {
				stream := childRef.Invoke(ctx.Lifecycle(), "browserinstance.open", nil)
				if stream != nil {
					defer stream.Close()
					_, _ = stream.RecvRaw()
				}
			}
		}
	} else {
		props := actor.PropsFromFunc(browserinstance.NewActor(cfg)).WithAsyncStart()
		if spawned, err := ctx.Spawn(props, cfg.ID); err != nil {
			ctx.Logger().Error("browsermanager.open: spawn browserinstance failed", "id", cfg.ID, "error", err)
			a.emitEvent(ctx, "open_failed", cfg, err)
			return
		} else {
			a.mu.Lock()
			a.childActorIDs[cfg.ID] = spawned.ID().String()
			a.mu.Unlock()
		}
	}
	ctx.Logger().Info("browsermanager.open", "id", cfg.ID)
	a.emitEvent(ctx, "opened", cfg, nil)
}

func (a *Actor) handleInternalUpdateState(ctx actor.Context, cfg domain.BrowserInstanceConfig) (domain.BrowserInstance, error) {
	a.mu.Lock()
	idx := -1
	for i, c := range a.Instances {
		if c.ID == cfg.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.internal.update_state: instance %q not found", cfg.ID)
	}
	old := a.Instances[idx]
	cfg.Name = old.Name // name is managed by the manager
	a.Instances[idx] = cfg
	a.mu.Unlock()
	a.saveOrLog(ctx)

	// Notify the UI when the page URL or title changed (e.g. user clicked a link
	// inside the browser window). History growth is included so the address-bar
	// omnibox refreshes even when a revisit keeps URL and title unchanged.
	if old.URL != cfg.URL || old.State.URL != cfg.State.URL || old.State.Title != cfg.State.Title || len(old.State.History) != len(cfg.State.History) {
		a.emitEvent(ctx, "navigated", cfg, nil)
	}

	return a.composeInstance(cfg), nil
}

func (a *Actor) handleSyncInstance(ctx actor.Context, cfg domain.BrowserInstanceConfig) (domain.BrowserInstance, error) {
	a.mu.Lock()
	idx := -1
	for i, c := range a.Instances {
		if c.ID == cfg.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.sync_instance: instance %q not found", cfg.ID)
	}
	cfg.Name = a.Instances[idx].Name // name is managed by the manager
	a.Instances[idx] = cfg
	a.mu.Unlock()
	a.saveOrLog(ctx)
	return a.composeInstance(cfg), nil
}

func (a *Actor) pushConfigure(ctx actor.Context, cfg domain.BrowserInstanceConfig) {
	if actorIDStr, ok := a.childActorIDs[cfg.ID]; ok {
		canonical, err := identity.ParseCanonicalID(actorIDStr)
		if err == nil {
			if childRef, ok := ctx.LookupID(id.From(canonical)); ok && childRef != nil {
				stream := childRef.Invoke(ctx.Lifecycle(), "browserinstance.sync", cfg)
				if stream != nil {
					defer stream.Close()
					_, _ = stream.RecvRaw()
				}
			}
		}
	}
}

func (a *Actor) emitEvent(ctx actor.Context, kind string, cfg domain.BrowserInstanceConfig, err error) {
	inst := a.composeInstance(cfg)
	event := domain.BrowserManagerEvent{
		Kind:     kind,
		ID:       cfg.ID,
		Instance: inst,
	}
	if err != nil {
		event.Error = err.Error()
	}
	_ = ctx.EmitEvent("browser_manager_event", event)
}

// isObserveAction returns true for read-only browser actions that do not modify
// browser state. These are permitted without human-role verification.
func isObserveAction(action string) bool {
	switch strings.ToLower(action) {
	case "observe", "get_observation", "snapshot", "read", "inspect", "screenshot":
		return true
	}
	return false
}

func (a *Actor) handleUse(ctx actor.Context, req gen.BrowserUseReq) (gen.BrowserUseResp, error) {
	instanceID := req.InstanceID
	if instanceID == "" {
		// Default to "global" instance if no instance specified
		instanceID = "global"
	}

	// Verify instance exists. The "global" tab is the shared desktop browser
	// session (kind="global"): open_global deliberately neither spawns a child
	// actor nor stores an instance, so the child-actor check is skipped for it.
	// Its WebView2 window is registered in the WindowOperator under the stable
	// key "global" by the desktop service, so resolution is purely operator-side.
	exists := instanceID == "global"
	if !exists {
		a.mu.Lock()
		_, exists = a.childActorIDs[instanceID]
		// Also check in the Instances slice for names or partial IDs
		if !exists {
			for _, cfg := range a.Instances {
				if cfg.ID == instanceID || cfg.Name == instanceID {
					_, exists = a.childActorIDs[cfg.ID]
					if exists {
						instanceID = cfg.ID
					}
					break
				}
			}
		}
		a.mu.Unlock()
	}

	if !exists {
		return gen.BrowserUseResp{
			Success:   false,
			ErrorCode: "instance_not_found",
			Message:   fmt.Sprintf("browsermanager.use: instance %q not found", instanceID),
		}, nil
	}

	// Execute the action
	op := browserinstance.GetWindowOperator()
	if op == nil {
		return gen.BrowserUseResp{
			Success:   false,
			ErrorCode: "operator_not_bound",
			Message:   "browsermanager.use: browser operator not available",
		}, nil
	}

	// Execute the action
	if isObserveAction(req.Action) {
		// Observe is read-only - just get the current page state
		obs, err := op.Observe(instanceID)
		if err != nil {
			return gen.BrowserUseResp{
				Success:   false,
				ErrorCode: "observe_failed",
				Message:   fmt.Sprintf("browsermanager.use: observe failed: %v", err),
			}, nil
		}
		return gen.BrowserUseResp{
			Success:     true,
			Message:     "observe completed",
			Observation: obs,
		}, nil
	}

	// Interactive action - execute the use request
	resp, err := op.Use(instanceID, req)
	if err != nil {
		return gen.BrowserUseResp{
			Success:   false,
			ErrorCode: "use_failed",
			Message:   fmt.Sprintf("browsermanager.use: action %q failed: %v", req.Action, err),
		}, nil
	}

	// Re-observe after a successful interactive action so the caller gets the
	// new page state (observe → act closed loop). A re-observe failure does not
	// overturn the action result; it is only noted in the Message.
	if resp.Success {
		obs, obsErr := op.Observe(instanceID)
		if obsErr != nil {
			if resp.Message != "" {
				resp.Message += "; "
			}
			resp.Message += fmt.Sprintf("action ok, re-observe failed: %v", obsErr)
		} else {
			resp.Observation = obs
		}
	}

	return *resp, nil
}

func (a *Actor) handleExportCookies(ctx actor.Context, req gen.BrowserManagerExportCookiesReq) (gen.BrowserManagerExportCookiesResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return gen.BrowserManagerExportCookiesResp{}, err
	}
	instanceID, err := a.resolveInstanceID(req.ID)
	if err != nil {
		return gen.BrowserManagerExportCookiesResp{}, fmt.Errorf("browsermanager.export_cookies: %w", err)
	}
	req.ID = instanceID
	resp, err := a.invokeInstance(ctx, instanceID, "browserinstance.export_cookies", req)
	if err != nil {
		return gen.BrowserManagerExportCookiesResp{}, fmt.Errorf("browsermanager.export_cookies: %w", err)
	}
	out, ok := resp.(gen.BrowserManagerExportCookiesResp)
	if !ok {
		return gen.BrowserManagerExportCookiesResp{}, fmt.Errorf("browsermanager.export_cookies: unexpected response type %T", resp)
	}
	return out, nil
}

func (a *Actor) handleImportCookies(ctx actor.Context, req gen.BrowserManagerImportCookiesReq) (gen.BrowserManagerImportCookiesResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return gen.BrowserManagerImportCookiesResp{}, err
	}
	instanceID, err := a.resolveInstanceID(req.ID)
	if err != nil {
		return gen.BrowserManagerImportCookiesResp{}, fmt.Errorf("browsermanager.import_cookies: %w", err)
	}
	req.ID = instanceID
	resp, err := a.invokeInstance(ctx, instanceID, "browserinstance.import_cookies", req)
	if err != nil {
		return gen.BrowserManagerImportCookiesResp{}, fmt.Errorf("browsermanager.import_cookies: %w", err)
	}
	out, ok := resp.(gen.BrowserManagerImportCookiesResp)
	if !ok {
		return gen.BrowserManagerImportCookiesResp{}, fmt.Errorf("browsermanager.import_cookies: unexpected response type %T", resp)
	}
	return out, nil
}

// resolveInstanceID maps an instance ID or name to a live child actor ID.
func (a *Actor) resolveInstanceID(idOrName string) (string, error) {
	if idOrName == "" {
		return "", fmt.Errorf("instance id is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.childActorIDs[idOrName]; ok {
		return idOrName, nil
	}
	for _, cfg := range a.Instances {
		if cfg.ID == idOrName || cfg.Name == idOrName {
			if _, ok := a.childActorIDs[cfg.ID]; ok {
				return cfg.ID, nil
			}
			return "", fmt.Errorf("instance %q has no live child actor", idOrName)
		}
	}
	return "", fmt.Errorf("instance %q not found", idOrName)
}

// invokeInstance synchronously invokes a callable on the browserinstance child
// actor for instanceID and returns the decoded response.
func (a *Actor) invokeInstance(ctx actor.Context, instanceID, callable string, req any) (any, error) {
	a.mu.Lock()
	actorIDStr, ok := a.childActorIDs[instanceID]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("instance %q has no live child actor", instanceID)
	}
	canonical, err := identity.ParseCanonicalID(actorIDStr)
	if err != nil {
		return nil, err
	}
	childRef, ok := ctx.LookupID(id.From(canonical))
	if !ok || childRef == nil {
		return nil, fmt.Errorf("child actor for instance %q not found", instanceID)
	}
	stream := childRef.Invoke(ctx.Lifecycle(), callable, req)
	if stream == nil {
		return nil, fmt.Errorf("invoke %s on %q returned nil stream", callable, instanceID)
	}
	defer stream.Close()
	return stream.Recv()
}

func (a *Actor) composeInstance(cfg domain.BrowserInstanceConfig) domain.BrowserInstance {
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
