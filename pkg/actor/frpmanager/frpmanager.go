// Package frpmanager is the CRUD manager for embedded frpc instances.
//
// Topology:
//
//	/frpmanager                # this actor; holds []FrpInstanceConfig
//	├── /frpmanager/inst-1     # one frpc client per persisted config
//	├── /frpmanager/inst-2
//	└── ...
//
// Instances are spawned/respawned by this actor; the durable record lives
// here, the runtime frpc state lives on each frpinstance child.
//
// Lane model (OwnerLane阻塞审计2026-08-27 P2): the child-facing orchestration
// must not occupy the owner lane, which would let a hung child (or a slow
// fan-out) stall every control callable. State-mutating handlers
// (create/update/remove/start/stop) stay on the owner lane and only fire
// non-blocking child messages; everything that waits on a child
// (list/get/refresh/detect) is a PureContext (stateless) handler that runs
// off the owner loop on pooled goroutines. Each child status invoke is
// bounded by childInvokeTimeout.
package frpmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"

	"github.com/qomos-w/sporemind/pkg/actor/frpinstance"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/policy"
)

// childInvokeTimeout bounds how long the manager waits on a child callable
// before falling back to a "child unreachable" response. Keep small: handleList
// fans out one invoke per persisted instance, so a single hung child must not
// stall the whole list.
const childInvokeTimeout = domain.DefaultInvokeTimeout

// Actor manages a list of FrpInstanceConfig and spawns one frpinstance child
// per entry. Instances is admin-scoped because each FrpInstanceConfig may
// carry a server token; Public callables strip tokens before returning.
type Actor struct {
	actor.Host
	store persist.Persist
	// stateLoaded is set once Load() has completed (first start included);
	// OnStop skips the save before that (gospore ForceCleanup may stop us
	// mid-OnInit, and saving zero-value Instances would wipe the record).
	stateLoaded atomic.Bool
	Instances   []domain.FrpInstanceConfig `gospore:"component,admin"`

	actorID       string
	nextID        int64
	mu            sync.Mutex
	childActorIDs map[string]string // instance ID -> actor ID

	// listSnapshot caches the latest handleList fan-out result. handleList
	// rebuilds it on every call because child runtime state (Running/lastErr)
	// changes asynchronously — e.g. a frpc login failure lands on the child
	// seconds after start, and frpinstance emits no status event. Mutation
	// handlers and OnStart also refresh it so their responses stay consistent.
	listSnapshot atomic.Pointer[frpListSnapshot]
}

// frpListSnapshot is the immutable handleList result cached for PureContext reads.
type frpListSnapshot struct {
	Items       []domain.FrpInstance
	GatewayPort int32
}

var _ persist.Persistent = (*Actor)(nil)

type managerSnapshot struct {
	Instances []domain.FrpInstanceConfig `json:"instances"`
	NextID    int64                      `json:"nextId"`
}

func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("frpmanager"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	a.childActorIDs = make(map[string]string)
	if err := a.Load(); err != nil {
		ctx.Logger().Error("frpmanager: load state failed", "error", err)
	}
	return nil
}

func (a *Actor) Type() string { return "frpmanager" }

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("frpmanager: starting", "id", a.actorID, "instances", len(a.Instances))

	if err := ctx.Register("frpmanager.list", a.handleList, actor.Public()); err != nil {
		return fmt.Errorf("frpmanager: register list: %w", err)
	}
	if err := ctx.Register("frpmanager.refresh", a.handleRefresh, actor.Internal()); err != nil {
		return fmt.Errorf("frpmanager: register refresh: %w", err)
	}
	if err := ctx.Register("frpmanager.get", a.handleGet, actor.Public()); err != nil {
		return fmt.Errorf("frpmanager: register get: %w", err)
	}
	if err := ctx.Register("frpmanager.create", a.handleCreate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("frpmanager: register create: %w", err)
	}
	if err := ctx.Register("frpmanager.update", a.handleUpdate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("frpmanager: register update: %w", err)
	}
	if err := ctx.Register("frpmanager.remove", a.handleRemove, actor.AdminOnly()); err != nil {
		return fmt.Errorf("frpmanager: register remove: %w", err)
	}
	if err := ctx.Register("frpmanager.start", a.handleStart, actor.AdminOnly()); err != nil {
		return fmt.Errorf("frpmanager: register start: %w", err)
	}
	if err := ctx.Register("frpmanager.stop", a.handleStop, actor.AdminOnly()); err != nil {
		return fmt.Errorf("frpmanager: register stop: %w", err)
	}
	if err := ctx.Register("frpmanager.detect", a.handleDetect, actor.Public()); err != nil {
		return fmt.Errorf("frpmanager: register detect: %w", err)
	}

	if err := ctx.RegisterDomain("frpmanager").Expose(); err != nil {
		return fmt.Errorf("frpmanager: expose: %w", err)
	}

	// Re-spawn persisted children so the tree survives process restart.
	for _, cfg := range a.Instances {
		props := actor.PropsFromFunc(frpinstance.NewActor(cfg)).WithAsyncStart()
		if spawned, err := ctx.Spawn(props, cfg.ID); err != nil {
			ctx.Logger().Error("frpmanager: re-spawn frpinstance failed", "id", cfg.ID, "error", err)
		} else {
			a.mu.Lock()
			a.childActorIDs[cfg.ID] = spawned.ID().String()
			a.mu.Unlock()
			ctx.Logger().Info("frpmanager: re-spawned frpinstance", "id", cfg.ID, "name", cfg.Name)
		}
	}
	a.rebuildListSnapshot(ctx)
	return nil
}

func (a *Actor) OnStop(ctx actor.Context) error {
	if !a.stateLoaded.Load() {
		// Stopped mid-OnInit (gospore ForceCleanup): skip the wipe-prone save.
		return nil
	}
	return a.Save()
}

// Save snapshots Instances + nextID. Caller may hold a.mu (Save acquires it
// internally too, but only briefly to copy the slice).
func (a *Actor) Save() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("frpmanager"))
		if err != nil {
			return err
		}
	}
	a.mu.Lock()
	snapshot := managerSnapshot{
		Instances: append([]domain.FrpInstanceConfig(nil), a.Instances...),
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
		a.store, err = persist.New(config.PersistConfig("frpmanager"))
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

	// Defensive: rehydrate nextID from max existing ID in case the counter
	// got out of sync with the list (e.g. mid-write crash).
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
		ctx.Logger().Error("frpmanager: save state failed", "error", err)
	}
}

// handleList returns live status: it fans out one status invoke per child on
// every call (via rebuildListSnapshot) instead of serving the last
// mutation-time snapshot, so a tunnel whose frpc login failed after start is
// reported as failed, not running. It is a PureContext handler: the fan-out
// must not occupy the owner lane, and the invokes are individually bounded by
// childInvokeTimeout so one hung child cannot stall the list.
func (a *Actor) handleList(ctx actor.PureContext) (domain.FrpManagerListResp, error) {
	a.rebuildListSnapshot(ctx)
	if s := a.listSnapshot.Load(); s != nil {
		return domain.FrpManagerListResp{Items: s.Items, GatewayPort: s.GatewayPort}, nil
	}
	// Defensive fallback before the first refresh (OnStart always refreshes,
	// so this only covers an un-started actor).
	return domain.FrpManagerListResp{GatewayPort: int32(frpinstance.GatewayLocalPort())}, nil
}

// handleRefresh rebuilds the cached list snapshot in the background. It is
// the fire-and-forget self-call that mutation handlers schedule (see
// kickRefresh) so their responses stay µs-fast while the next list still sees
// live child status. Runs off the owner lane (PureContext), like handleList.
func (a *Actor) handleRefresh(ctx actor.PureContext) error {
	a.rebuildListSnapshot(ctx)
	return nil
}

// rebuildListSnapshot rebuilds the cached list result by fanning out one
// status invoke per child in parallel and storing the result atomically. The
// per-invoke wait is bounded by childInvokeTimeout; a single hung child costs
// at most one timeout, never N serialized timeouts. Callers must be off the
// owner lane (stateless handler or OnStart bootstrap).
func (a *Actor) rebuildListSnapshot(ctx actor.PureContext) {
	a.mu.Lock()
	snap := append([]domain.FrpInstanceConfig(nil), a.Instances...)
	a.mu.Unlock()

	out := make([]domain.FrpInstance, len(snap))
	var wg sync.WaitGroup
	for i, cfg := range snap {
		wg.Add(1)
		go func(i int, cfg domain.FrpInstanceConfig) {
			defer wg.Done()
			out[i] = a.composeInstance(ctx, cfg)
		}(i, cfg)
	}
	wg.Wait()
	a.listSnapshot.Store(&frpListSnapshot{
		Items:       out,
		GatewayPort: int32(frpinstance.GatewayLocalPort()),
	})
}

// kickRefresh schedules an asynchronous snapshot rebuild via a fire-and-forget
// self-call. Owner-lane mutation handlers use it instead of a synchronous
// fan-out, so a slow child can never stall the control surface.
func (a *Actor) kickRefresh(ctx actor.Context) {
	if err := ctx.After(0, "frpmanager.refresh", nil); err != nil {
		ctx.Logger().Warn("frpmanager: schedule refresh failed", "error", err)
	}
}

func (a *Actor) handleGet(ctx actor.PureContext, req domain.FrpManagerGetReq) (domain.FrpInstance, error) {
	a.mu.Lock()
	var cfg domain.FrpInstanceConfig
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
		return domain.FrpInstance{}, fmt.Errorf("frpmanager.get: instance %q not found", req.ID)
	}
	// PureContext: the single-child status wait runs off the owner lane.
	return a.composeInstance(ctx, cfg), nil
}

func (a *Actor) handleCreate(ctx actor.Context, req domain.FrpManagerCreateReq) (domain.FrpInstance, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.FrpInstance{}, err
	}
	if req.Name == "" {
		return domain.FrpInstance{}, fmt.Errorf("frpmanager.create: name is required")
	}

	a.mu.Lock()
	for _, existing := range a.Instances {
		if existing.Name == req.Name {
			a.mu.Unlock()
			return domain.FrpInstance{}, fmt.Errorf("frpmanager.create: name %q already exists", req.Name)
		}
	}
	cfg := domain.FrpInstanceConfig{
		ID:         fmt.Sprintf("inst-%d", a.nextID),
		Name:       req.Name,
		ServerAddr: req.ServerAddr,
		Token:      req.Token,
		Tls:        req.Tls,
		Proxies:    append([]domain.FrpProxy(nil), req.Proxies...),
		WebProxy:   copyWebProxy(req.WebProxy),
	}
	if err := frpinstance.ValidateConfig(cfg); err != nil {
		a.mu.Unlock()
		return domain.FrpInstance{}, fmt.Errorf("frpmanager.create: %w", err)
	}
	a.Instances = append(a.Instances, cfg)
	a.nextID++
	a.mu.Unlock()
	a.saveOrLog(ctx)

	props := actor.PropsFromFunc(frpinstance.NewActor(cfg)).WithAsyncStart()
	spawned, err := ctx.Spawn(props, cfg.ID)
	if err != nil {
		// Spawn failed: roll back the persisted record so we don't leave an
		// orphan entry that will fail again on every restart.
		a.mu.Lock()
		for i, c := range a.Instances {
			if c.ID == cfg.ID {
				a.Instances = append(a.Instances[:i], a.Instances[i+1:]...)
				break
			}
		}
		a.mu.Unlock()
		a.saveOrLog(ctx)
		return domain.FrpInstance{}, fmt.Errorf("frpmanager.create: spawn frpinstance: %w", err)
	}
	a.mu.Lock()
	a.childActorIDs[cfg.ID] = spawned.ID().String()
	a.mu.Unlock()
	ctx.Logger().Info("frpmanager.create", "id", cfg.ID, "name", cfg.Name, "server", cfg.ServerAddr)
	// No synchronous child waits on the owner lane: schedule an async
	// snapshot rebuild and answer from the cached/derived view.
	a.kickRefresh(ctx)
	return a.serveInstance(cfg), nil
}

func (a *Actor) handleRemove(ctx actor.Context, req domain.FrpManagerRemoveReq) (domain.FrpInstance, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.FrpInstance{}, err
	}
	a.mu.Lock()
	var removed domain.FrpInstanceConfig
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
		return domain.FrpInstance{}, fmt.Errorf("frpmanager.remove: instance %q not found", req.ID)
	}
	a.saveOrLog(ctx)

	// Stop the spawned child if it exists.
	a.mu.Lock()
	actorIDStr, ok := a.childActorIDs[removed.ID]
	if ok {
		canonical, err := identity.ParseCanonicalID(actorIDStr)
		if err == nil {
			if childRef, ok := ctx.LookupID(id.From(canonical)); ok && childRef != nil {
				_ = ctx.Destroy(childRef)
			}
		}
		delete(a.childActorIDs, removed.ID)
	}
	a.mu.Unlock()
	ctx.Logger().Info("frpmanager.remove", "id", removed.ID, "name", removed.Name)

	sanitized := removed
	sanitized.Token = ""
	sanitized.Proxies = append([]domain.FrpProxy(nil), removed.Proxies...)
	sanitized.WebProxy = sanitizeWebProxy(removed.WebProxy)
	a.kickRefresh(ctx)
	return domain.FrpInstance{
		Config: sanitized,
		Status: domain.FrpInstanceStatus{
			ID:              removed.ID,
			Running:         false,
			DetectedVersion: frpinstance.FrpVersion,
		},
	}, nil
}

// handleUpdate replaces a persisted FrpInstanceConfig and pushes the new
// config to the running child via the Internal configure callable.
// Token preservation rule: an empty Token in the request keeps the existing
// stored token (the edit UI can't see it because List strips it). The same
// rule applies to WebProxy.KeyPem (https mode private key) for the same
// reason — Public list strips it, so a blank in the request means "keep".
// CertPem (public material) is also preserved on blank for UX symmetry; see
// preserveWebProxySecrets for the full rationale.
// Disabled is preserved from the existing record — lifecycle is owned by
// start/stop, not by update.
func (a *Actor) handleUpdate(ctx actor.Context, req domain.FrpManagerUpdateReq) (domain.FrpInstance, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.FrpInstance{}, err
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
		return domain.FrpInstance{}, fmt.Errorf("frpmanager.update: instance %q not found", req.ID)
	}
	existing := a.Instances[idx]

	cfg := req.Config
	cfg.ID = existing.ID
	cfg.Disabled = existing.Disabled
	if cfg.Token == "" {
		cfg.Token = existing.Token
	}
	preserveWebProxySecrets(&cfg, existing.WebProxy)
	if cfg.Name == "" {
		a.mu.Unlock()
		return domain.FrpInstance{}, fmt.Errorf("frpmanager.update: name is required")
	}
	for i, other := range a.Instances {
		if i != idx && other.Name == cfg.Name {
			a.mu.Unlock()
			return domain.FrpInstance{}, fmt.Errorf("frpmanager.update: name %q already exists", cfg.Name)
		}
	}
	if err := frpinstance.ValidateConfig(cfg); err != nil {
		a.mu.Unlock()
		return domain.FrpInstance{}, fmt.Errorf("frpmanager.update: %w", err)
	}
	cfg.Proxies = append([]domain.FrpProxy(nil), cfg.Proxies...)
	cfg.WebProxy = copyWebProxy(cfg.WebProxy)
	a.Instances[idx] = cfg
	a.mu.Unlock()
	a.saveOrLog(ctx)

	a.pushConfigure(ctx, cfg)
	ctx.Logger().Info("frpmanager.update", "id", cfg.ID, "name", cfg.Name)
	a.kickRefresh(ctx)
	return a.serveInstance(cfg), nil
}

// handleStart flips Disabled=false on the persisted record and pushes the
// updated config to the child so frpc starts. Idempotent.
func (a *Actor) handleStart(ctx actor.Context, req domain.FrpManagerStartReq) (domain.FrpInstance, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.FrpInstance{}, err
	}
	cfg, ok := a.setDisabled(req.ID, false)
	if !ok {
		return domain.FrpInstance{}, fmt.Errorf("frpmanager.start: instance %q not found", req.ID)
	}
	a.saveOrLog(ctx)
	a.pushConfigure(ctx, cfg)
	ctx.Logger().Info("frpmanager.start", "id", cfg.ID, "name", cfg.Name)
	a.kickRefresh(ctx)
	return a.serveInstance(cfg), nil
}

// handleStop flips Disabled=true on the persisted record and pushes the
// updated config to the child so frpc stops. Idempotent.
func (a *Actor) handleStop(ctx actor.Context, req domain.FrpManagerStopReq) (domain.FrpInstance, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.FrpInstance{}, err
	}
	cfg, ok := a.setDisabled(req.ID, true)
	if !ok {
		return domain.FrpInstance{}, fmt.Errorf("frpmanager.stop: instance %q not found", req.ID)
	}
	a.saveOrLog(ctx)
	a.pushConfigure(ctx, cfg)
	ctx.Logger().Info("frpmanager.stop", "id", cfg.ID, "name", cfg.Name)
	a.kickRefresh(ctx)
	return a.serveInstance(cfg), nil
}

// handleDetect probes a remote frps server to determine whether legacy mode
// (TCPMux=false) is required. Public — any authenticated user can check
// compatibility before creating a tunnel. PureContext (stateless): the probe
// is pure network IO bounded by frpinstance's dialTimeout/probeTimeout and
// touches no actor state, so it must not occupy the owner lane.
func (a *Actor) handleDetect(ctx actor.PureContext, req domain.FrpManagerDetectReq) (domain.FrpManagerDetectResp, error) {
	legacyNeeded, err := frpinstance.DetectServerCompatibility(req.ServerAddr, req.Token, false)
	if err != nil {
		return domain.FrpManagerDetectResp{LegacyMode: legacyNeeded, Error: err.Error()}, nil
	}
	return domain.FrpManagerDetectResp{LegacyMode: legacyNeeded}, nil
}

// setDisabled flips the Disabled flag on the persisted entry and returns the
// updated cfg copy. Caller must Save and pushConfigure.
func (a *Actor) setDisabled(id string, disabled bool) (domain.FrpInstanceConfig, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, c := range a.Instances {
		if c.ID == id {
			a.Instances[i].Disabled = disabled
			cfg := a.Instances[i]
			cfg.Proxies = append([]domain.FrpProxy(nil), cfg.Proxies...)
			cfg.WebProxy = copyWebProxy(cfg.WebProxy)
			return cfg, true
		}
	}
	return domain.FrpInstanceConfig{}, false
}

// pushConfigure fires the Internal frpinstance.configure callable at the
// child for cfg.Id. If the child isn't reachable, the manager logs a warning
// and returns: the durable record on disk is the source of truth, but a
// missed configure means runtime state has drifted from the persisted policy
// until the child is re-spawned (e.g. on restart) or this is retried.
// The invoke is fire-and-forget (non-blocking): the child reconciles on its
// own frp_ops lane, and the manager's owner lane stays µs-fast.
func (a *Actor) pushConfigure(ctx actor.Context, cfg domain.FrpInstanceConfig) {
	childRef, ok := a.childRef(ctx, cfg.ID)
	if !ok {
		ctx.Logger().Warn("frpmanager: configure push skipped, child unreachable — runtime may diverge from persisted state",
			"id", cfg.ID)
		return
	}
	invokeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), childInvokeTimeout)
	defer cancel()
	call := childRef.Invoke(invokeCtx, "frpinstance.configure", domain.FrpInstanceConfigureReq{Config: cfg})
	if call != nil {
		_ = call.Close()
	}
}

// childRef resolves the spawned child actor for instance ID, guarding the
// childActorIDs map against concurrent stateless reads. Thread-safe.
func (a *Actor) childRef(ctx interface {
	LookupID(id.ActorID) (ref.Ref, bool)
}, instanceID string) (ref.Ref, bool) {
	a.mu.Lock()
	actorIDStr, ok := a.childActorIDs[instanceID]
	a.mu.Unlock()
	if !ok {
		return nil, false
	}
	canonical, err := identity.ParseCanonicalID(actorIDStr)
	if err != nil {
		return nil, false
	}
	childRef, ok := ctx.LookupID(id.From(canonical))
	if !ok || childRef == nil {
		return nil, false
	}
	return childRef, true
}

// composeInstance assembles the wire-level FrpInstance for a persisted cfg.
// It looks up the child actor and asks it for live status; if the child
// isn't reachable (e.g. unit test, or child still booting), it falls back
// to a "not running" snapshot derived from the persisted cfg.
// In all paths, Token and WebProxy.KeyPem are stripped before returning.
// Must be called off the owner lane (stateless handler or OnStart): the
// child status invoke is a bounded wait (childInvokeTimeout).
func (a *Actor) composeInstance(ctx actor.PureContext, cfg domain.FrpInstanceConfig) domain.FrpInstance {
	fallback := fallbackView(cfg)

	childRef, ok := a.childRef(ctx, cfg.ID)
	if !ok {
		return fallback
	}
	// Bounded wait: rebuildListSnapshot fans out one invoke per instance, so a
	// single hung child cannot be allowed to stall the whole list rebuild.
	invokeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), childInvokeTimeout)
	defer cancel()
	call := childRef.Invoke(invokeCtx, "frpinstance.status", nil)
	if call == nil {
		return fallback
	}
	v, _ := call.Final(invokeCtx)
	call.Close()
	if v == nil {
		return fallback
	}
	live, ok := decodeFrpInstance(v)
	if !ok {
		return fallback
	}
	// Defense-in-depth: ensure secrets never leak even if a future child
	// implementation forgets to sanitize its own status response.
	live.Config.Token = ""
	if live.Config.WebProxy != nil {
		live.Config.WebProxy.KeyPem = ""
	}
	return live
}

// fallbackView derives the sanitized "not running" wire view of cfg without
// any child interaction.
func fallbackView(cfg domain.FrpInstanceConfig) domain.FrpInstance {
	sanitized := cfg
	sanitized.Token = ""
	sanitized.Proxies = append([]domain.FrpProxy(nil), cfg.Proxies...)
	sanitized.WebProxy = sanitizeWebProxy(cfg.WebProxy)
	return domain.FrpInstance{
		Config: sanitized,
		Status: domain.FrpInstanceStatus{
			ID:              cfg.ID,
			Running:         false,
			DetectedVersion: frpinstance.FrpVersion,
		},
	}
}

// serveInstance returns the freshest view available to an owner-lane mutation
// handler without waiting on a child: the live-status item from the most
// recent completed snapshot rebuild when present (the child was seen by a
// recent list), else the not-running fallback derived from cfg. Never invokes
// a child synchronously.
func (a *Actor) serveInstance(cfg domain.FrpInstanceConfig) domain.FrpInstance {
	if s := a.listSnapshot.Load(); s != nil {
		for _, item := range s.Items {
			if item.Config.ID == cfg.ID {
				return item
			}
		}
	}
	return fallbackView(cfg)
}

func decodeFrpInstance(v any) (domain.FrpInstance, bool) {
	switch x := v.(type) {
	case domain.FrpInstance:
		return x, true
	case *domain.FrpInstance:
		if x != nil {
			return *x, true
		}
		return domain.FrpInstance{}, false
	case []byte:
		if len(x) == 0 {
			return domain.FrpInstance{}, false
		}
		var inst domain.FrpInstance
		if err := json.Unmarshal(x, &inst); err != nil {
			return domain.FrpInstance{}, false
		}
		return inst, true
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return domain.FrpInstance{}, false
		}
		var inst domain.FrpInstance
		if err := json.Unmarshal(body, &inst); err != nil {
			return domain.FrpInstance{}, false
		}
		return inst, true
	}
}

// copyWebProxy returns a deep copy of w so a caller cannot mutate our
// internal state by holding the pointer. Returns nil if w is nil. The copy
// retains all fields including secrets — it is for internal use; outward
// responses must go through sanitizeWebProxy.
func copyWebProxy(w *domain.FrpWebProxy) *domain.FrpWebProxy {
	if w == nil {
		return nil
	}
	cp := *w
	cp.CustomDomains = append([]string(nil), w.CustomDomains...)
	return &cp
}

// sanitizeWebProxy returns a deep copy with the secret KeyPem stripped, for
// use in any callable that may reach a non-admin caller. CertPem stays — the
// public certificate is not a secret and admins editing the entry need to see
// what was previously stored.
func sanitizeWebProxy(w *domain.FrpWebProxy) *domain.FrpWebProxy {
	if w == nil {
		return nil
	}
	cp := copyWebProxy(w)
	cp.KeyPem = ""
	return cp
}

// preserveWebProxySecrets fills still-secret fields from the prior persisted
// record into cfg.WebProxy when the request left them blank.
//
// KeyPem is a secret: Public list strips it, so the edit UI literally cannot
// resubmit it — an empty value in the request means "keep what was stored",
// not "clear it".
//
// CertPem is public material (sanitizeWebProxy keeps it), so an empty value in
// the request usually means the admin really did blank it. We still preserve
// it on blank for UX symmetry: the edit form pre-fills cert from the stripped
// list view, and if a user clears that textarea by accident we'd rather keep
// the previous cert than fail validation. CertPem rotation just means pasting
// a new value.
func preserveWebProxySecrets(cfg *domain.FrpInstanceConfig, prior *domain.FrpWebProxy) {
	if cfg == nil || cfg.WebProxy == nil || prior == nil {
		return
	}
	if cfg.WebProxy.CertPem == "" {
		cfg.WebProxy.CertPem = prior.CertPem
	}
	if cfg.WebProxy.KeyPem == "" {
		cfg.WebProxy.KeyPem = prior.KeyPem
	}
}
