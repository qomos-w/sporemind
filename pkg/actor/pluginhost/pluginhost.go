package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/gospore/resource"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/buildinfo"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/runtime/topo"
)

// PluginDescriptor is persisted as a public component so the frontend can
// observe the list of loaded plugins.
type PluginDescriptor struct {
	ID           string
	Name         string
	Version      string
	Namespace    string
	Runtime      string
	AbiName      string
	AbiVersion   int32
	Encoding     string
	SchemaHash   string
	Capabilities []string
	Callables    []string
	Isolation    string
	TrustClass   string
	Signer       string
	Trusted      bool // trusted plugins run in-process; untrusted require OOP transport
	Dev          bool // loaded via the appmanager dev loop; gates panel ops (dev-only)
	LoadedAt     time.Time
	Status       string // loading | active | error | unloaded
	Error        string
}

// HandlerFunc processes a plugin callable request and returns a response.
// The request and response payloads are opaque bytes (typically spore binary
// codec encoded).
type HandlerFunc = pluginhost.HandlerFunc

// HandlerStreamFunc aliases the loader-defined streaming handler-map form.
type HandlerStreamFunc = pluginhost.HandlerStreamFunc

// Actor is the plugin host. It manages plugin descriptors and dispatches
// calls to registered plugin handlers. Because gospore services are static,
// plugin callables are routed through this actor's invoke dispatcher.
type Actor struct {
	actor.Host

	Plugins []PluginDescriptor `gospore:"component,public"`

	// panelOps holds in-flight panel-op results (the push side of
	// pluginhost.panel_op / panel_op_put), matched by RequestId. Mutex-guarded
	// because puts arrive on stateless invoke goroutines.
	panelOpsMu sync.Mutex
	panelOps   map[string]*pendingPanelOp

	mu             sync.RWMutex
	handlers       map[string]HandlerFunc
	handlerStreams map[string]HandlerStreamFunc
	router         *pluginhost.Router
	closers        map[string]func() error // pluginID → library close function
	loader         *pluginhost.ArtifactLoader
	store          persist.Persist
	appStateStore  persist.Persist
	// appState quota overrides; zero values fall back to config
	// (config.AppStateMaxDocsPerApp/ValueBytes/AppendBytes). Tests set these
	// to exercise quota rejections without touching global config.
	appStateMaxDocsPerApp  int
	appStateMaxValueBytes  int64
	appStateMaxAppendBytes int64
	actorID                string
	actorCtx               actor.Context // stored for lazy runtime service lookups
	ArtifactLoads          map[string]gen.PluginArtifactLoadReq
	pendingArtifactReloads map[string]gen.PluginArtifactLoadReq
	// pendingArtifactAssets holds the candidate asset bundle staged by
	// artifact_reload_prepare, keyed by reload token. It is applied (or
	// discarded) together with the artifact swap at commit/abort time.
	pendingArtifactAssets map[string]map[string][]byte
	// AssetStores persists each plugin's HTTP-served asset bundle
	// (pluginID → path → bytes). It is the pluginhost-side counterpart of
	// the appmanager appRecord.Assets: appmanager pushes it via
	// pluginhost.assets_put at registration, and OnStart re-registers the
	// router handlers from here so assets survive a restart without
	// cross-actor start ordering.
	AssetStores map[string]map[string][]byte

	// AppDataGrants is the app.data grant ledger (pluginID → granted
	// directory), recorded at artifact load from the OnLoad config. It
	// survives unload and host restarts — the data must outlive the
	// artifact — and only state_purge reclaims it.
	AppDataGrants map[string]string

	// proxies is the gateway reverse-proxy attach table (pluginID →
	// backend listener + proxy handler), guarded by mu like AssetStores.
	// In-memory only: after a host restart appmanager re-attaches live
	// backends via pluginhost.proxy_attach. While attached, the proxy
	// occupies the plugin's router slot above the assets handler; detach
	// (callable, or the process-state seam on crash/stop) falls back to
	// the persisted asset bundle or drops the route.
	proxies map[string]proxyAttach

	// svcRefs is a runtime-resolved cache of lazy service refs, keyed by
	// service domain (aiaggregator, sshmanager, ...). buildHostBridgeDispatch
	// seeds it from the OnStart snapshot; services that expose their domain
	// only after pluginhost starts (aiaggregator learns its config via
	// aimanager push or poll; sshmanager mirrors this) re-resolve via a live
	// LookupService on each host call until the exposure lands.
	svcRefMu sync.Mutex
	svcRefs  map[string]ref.Ref

	// pluginErrStates tracks transient invoke errors per plugin (pluginID →
	// error message). It is NOT part of the public Plugins component — which
	// must only be mutated from the cell goroutine — so that the stateless
	// handleInvoke can record errors without racing the cell's projection
	// snapshot. handleListPlugins merges this state at read time.
	pluginErrStates sync.Map

	// eventQueues carries per-plugin host-event drain queues (pluginID →
	// *eventDrainQueue). Host events — the agent step fan-out above all —
	// arrive as unbounded concurrent handleEventDeliver calls; invoking the
	// plugin synchronously per event through the single-flight channel starved
	// data-plane callers and got the process killed on expired budgets. Each
	// queue drains strictly FIFO on one goroutine, interleaving fairly with
	// data invokes. sync.Map for the same reason as pluginErrStates.
	eventQueues sync.Map

	// logRings is the per-plugin log capture (backend funnel + forwarded
	// frontend console entries), queried via pluginhost.plugin_logs. Same
	// sync-guard rationale as pluginErrStates: the funnel runs on stateless
	// invoke goroutines. pluginProcessStates carries the runtime verdict
	// (running/stopped/crashed) reported explicitly by the transport
	// lifecycle seam (pluginLogSink.ReportProcessState).
	logRingsMu          sync.Mutex
	logRings            map[string]*pluginLogRing
	pluginProcessStates sync.Map // pluginID → *pluginProcessInfo
	// processGenerations holds the latest allocated process generation per
	// plugin (pluginID → *int64). Reports carrying an older generation are
	// stale — the process was replaced by a newer spawn — and are dropped
	// before they can overwrite the current verdict.
	processGenerations sync.Map // pluginID → *int64

	// processStateOnTransition, when non-nil, is invoked by
	// setProcessState on every actual running/stopped/crashed transition.
	// It is the T2 seam (wired to appmanager there); nil (default) is a
	// no-op.
	processStateOnTransition func(pluginID, state, crash, httpAddr string)

	// domSnapshots holds the latest frontend DOM snapshot per plugin (the
	// push side of pluginhost.plugin_dom). One bounded string per plugin,
	// mutex-guarded because puts arrive on stateless invoke goroutines.
	domSnapshotsMu sync.Mutex
	domSnapshots   map[string]gen.PluginDomPutReq

	// pluginLogFn is the host-side structured logger for plugin log
	// entries. It is set at OnStart from the actor context's logger and
	// wired into the loaderOpener so every invoke drains plugin logs
	// into the host's observation stream.
	pluginLogFn func(pluginID string, generation int, level int, msg string)

	// goBuildOverride, when non-nil, replaces the default `go build` command
	// in handleNativeBuild (NativeBuildOptions.GoBuild seam). It exists so
	// tests can control compile duration without invoking a real toolchain;
	// nil keeps the production default.
	goBuildOverride func(dir, outPath string, env []string) ([]byte, error)

	// openerOverride is a test-only seam mirroring goBuildOverride: when
	// set, OnStart installs it instead of the production dual-transport
	// opener so matrix e2e tests can boot the pluginhost inside a real
	// runtime with stub artifacts (no real shared libraries or child
	// processes). nil keeps the production transportOpener.
	openerOverride pluginhost.ArtifactOpener

	// topo is the runtime topology provider, resolved at OnStart from the
	// shared resource pool. It supplies the actor-tree snapshot consumed by
	// the registry.query local host call to enumerate callable metadata.
	topo runtime.TopologyProvider
}

// Type returns the actor type identifier.
func (a *Actor) Type() string { return "pluginhost" }

func (a *Actor) Save() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("pluginhost"))
		if err != nil {
			return err
		}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.store.Save(a.actorID, struct {
		ArtifactLoads map[string]gen.PluginArtifactLoadReq `json:"artifactLoads"`
		AssetStores   map[string]map[string][]byte         `json:"assetStores,omitempty"`
		// AppDataGrants is the app.data grant ledger: pluginID → granted
		// directory. It survives unload (data must outlive the artifact) and
		// is only reclaimed by state_purge.
		AppDataGrants map[string]string `json:"appDataGrants,omitempty"`
	}{a.ArtifactLoads, a.AssetStores, a.AppDataGrants})
}

func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("pluginhost"))
		if err != nil {
			return err
		}
	}
	var saved struct {
		ArtifactLoads map[string]gen.PluginArtifactLoadReq `json:"artifactLoads"`
		AssetStores   map[string]map[string][]byte         `json:"assetStores"`
		AppDataGrants map[string]string                    `json:"appDataGrants"`
	}
	if err := persist.LoadOrZero(a.store, a.actorID, &saved); err != nil {
		return err
	}
	a.ArtifactLoads = saved.ArtifactLoads
	if a.ArtifactLoads == nil {
		a.ArtifactLoads = map[string]gen.PluginArtifactLoadReq{}
	}
	a.AssetStores = saved.AssetStores
	if a.AssetStores == nil {
		a.AssetStores = map[string]map[string][]byte{}
	}
	a.AppDataGrants = saved.AppDataGrants
	if a.AppDataGrants == nil {
		a.AppDataGrants = map[string]string{}
	}
	return nil
}

// RegisterCloser associates a library close function with a plugin ID.
// When the plugin is unregistered, the closer is called to release the
// native library handle (dlclose / FreeLibrary).
func (a *Actor) RegisterCloser(pluginID string, closer func() error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closers == nil {
		a.closers = make(map[string]func() error)
	}
	a.closers[pluginID] = closer
}

// RemoveCloser drops a registered closer without invoking it. Used by
// ArtifactLoader.Unload which closes the library directly.
func (a *Actor) RemoveCloser(pluginID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.closers, pluginID)
}

func (a *Actor) ReplaceArtifact(oldCallIDs []string, handlers map[string]HandlerFunc, streamHandlers map[string]HandlerStreamFunc, desc pluginhost.PluginDescriptorData, oldCloser, newCloser func() error) error {
	// oldCloser kills the old plugin process; its close path re-enters
	// ReportProcessState → detachProxy → a.mu, so it must run before the
	// lock — calling it under a.mu self-deadlocks the actor.
	if oldCloser != nil {
		if err := oldCloser(); err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, callID := range oldCallIDs {
		delete(a.handlers, callID)
		delete(a.handlerStreams, callID)
	}
	for callID, handler := range handlers {
		a.handlers[callID] = handler
	}
	for callID, handler := range streamHandlers {
		a.handlerStreams[callID] = handler
	}
	full := PluginDescriptor{ID: desc.ID, Name: desc.Name, Version: desc.Version, Namespace: desc.Namespace, Runtime: desc.Runtime, AbiName: desc.AbiName, AbiVersion: desc.AbiVersion, Encoding: desc.Encoding, SchemaHash: desc.SchemaHash, Capabilities: desc.Capabilities, Callables: desc.Callables, Isolation: desc.Isolation, TrustClass: desc.TrustClass, Signer: desc.Signer, Trusted: desc.Trusted, Status: desc.Status}
	found := false
	for i := range a.Plugins {
		if a.Plugins[i].ID == desc.ID {
			full.Dev = a.Plugins[i].Dev // Dev (panel-op gate) survives artifact swaps
			a.Plugins[i] = full
			found = true
			break
		}
	}
	if !found {
		a.Plugins = append(a.Plugins, full)
	}
	if newCloser != nil {
		a.closers[desc.ID] = newCloser
	} else {
		delete(a.closers, desc.ID)
	}
	return nil
}

// RegisterDescriptor upserts a plugin descriptor into the public Plugins
// component. Used by ArtifactLoader.Load to expose loaded native plugins.
func (a *Actor) RegisterDescriptor(desc pluginhost.PluginDescriptorData) {
	a.mu.Lock()
	defer a.mu.Unlock()
	full := PluginDescriptor{
		ID:           desc.ID,
		Name:         desc.Name,
		Version:      desc.Version,
		Namespace:    desc.Namespace,
		Runtime:      desc.Runtime,
		AbiName:      desc.AbiName,
		AbiVersion:   desc.AbiVersion,
		Encoding:     desc.Encoding,
		SchemaHash:   desc.SchemaHash,
		Capabilities: desc.Capabilities,
		Callables:    desc.Callables,
		Isolation:    desc.Isolation,
		TrustClass:   desc.TrustClass,
		Signer:       desc.Signer,
		Trusted:      desc.Trusted,
		Status:       desc.Status,
	}
	for i, p := range a.Plugins {
		if p.ID == full.ID {
			full.Dev = p.Dev // Dev (panel-op gate) is sticky across descriptor re-registration
			a.Plugins[i] = full
			return
		}
	}
	a.Plugins = append(a.Plugins, full)
}

// RemoveDescriptor removes a plugin descriptor by ID. Used by
// ArtifactLoader.Unload.
func (a *Actor) RemoveDescriptor(pluginID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, p := range a.Plugins {
		if p.ID == pluginID {
			a.Plugins = append(a.Plugins[:i], a.Plugins[i+1:]...)
			return
		}
	}
}

func (a *Actor) OnInit(ctx actor.Context) error {
	a.actorID = ctx.Self().ID().String()
	if a.ArtifactLoads == nil {
		a.ArtifactLoads = map[string]gen.PluginArtifactLoadReq{}
	}
	a.pendingArtifactReloads = map[string]gen.PluginArtifactLoadReq{}
	a.pendingArtifactAssets = map[string]map[string][]byte{}
	if a.AssetStores == nil {
		a.AssetStores = map[string]map[string][]byte{}
	}
	return a.Load()
}

func (a *Actor) OnStart(ctx actor.Context) error {
	a.actorCtx = ctx
	a.mu.Lock()
	if a.handlers == nil {
		a.handlers = make(map[string]HandlerFunc)
	}
	if a.handlerStreams == nil {
		a.handlerStreams = make(map[string]HandlerStreamFunc)
	}
	if a.closers == nil {
		a.closers = make(map[string]func() error)
	}
	a.mu.Unlock()

	if router, ok := resource.Get[*pluginhost.Router](ctx.Resources(), pluginhost.RouterKey); ok {
		a.router = router
	}
	if prov, ok := resource.Get[runtime.TopologyProvider](ctx.Resources(), runtime.TopologyKey); ok {
		a.topo = prov
	}
	if a.loader == nil {
		a.loader = pluginhost.NewArtifactLoader(a)
	}
	a.pluginLogFn = newPluginLogFn(ctx.Logger())
	// Dual-transport wiring (T7): one loader, two openers — the selector
	// picks FFI/purego (loaderOpener) vs subprocess (processOpener) per
	// manifest signals (TrustClass, dev-mode + artifact type). Dev builds
	// prefer the subprocess transport so the loader also restores persisted
	// artifacts through the same selection path. The test seam
	// (openerOverride, set via SetOpenerOverrideForTest) lets matrix e2e
	// tests boot this actor in a real runtime with stub artifacts.
	if a.openerOverride != nil {
		a.loader.SetOpener(a.openerOverride)
	} else {
		unary := a.buildHostBridgeDispatch(ctx)
		a.loader.SetOpener(newTransportOpener(unary, a.buildHostBridgeDispatchStream(unary), a.buildStoreDispatch(ctx), a, buildinfo.IsDev()))
	}

	// Wire the process-state seam BEFORE the restore loop: the restored
	// subprocesses report their post-OnLoad "running" (with the fresh HTTP
	// listener addr) from inside loader.Load below, and that report must
	// reach appmanager so the stale persisted backend is refreshed and the
	// gateway proxy attaches without an explicit reload.
	// T2 wire: process-state transitions (subprocess spawn / abnormal exit /
	// explicit unload close) are reported to appmanager as fire-and-forget
	// tells so registry records carry the real verdict. nil-safe in tests
	// that construct the actor without OnStart.
	a.processStateOnTransition = a.reportProcessStateToAppManager

	// Sort persisted artifacts by their Dependencies before loading so
	// that each plugin's dependencies are loaded first. Cycles or
	// missing dependencies are logged and skipped rather than failing
	// the entire restore (individual plugin loads are still validated).
	sortedIDs := artifactLoadOrder(a.ArtifactLoads)
	for _, pluginID := range sortedIDs {
		req := a.ArtifactLoads[pluginID]
		if _, err := a.loader.Load(ctx.Lifecycle(), req); err != nil {
			a.RegisterDescriptor(pluginhost.PluginDescriptorData{ID: pluginID, Name: req.Manifest.Name, Version: req.Manifest.Version, Runtime: req.Manifest.Runtime, Status: "error"})
			ctx.Logger().Error("pluginhost: restore artifact failed", "plugin", pluginID, "error", err)
			// Without this report the appmanager keeps the persisted record
			// state (typically "running" from before the restart) while no
			// handler exists here — every invoke then fails with "callable
			// not declared" and the frontend shows nothing. Report the load
			// verdict so the registry reflects reality (same tell discipline
			// as process-state transitions; appmanager starts before us).
			a.reportProcessStateToAppManager(pluginID, "failed", err.Error(), "")
		} else {
			a.markPanelOpDev(pluginID, req.Dev)
		}
	}

	// Restore the HTTP-served asset bundles for every persisted plugin. This
	// is the pluginhost side of the plugin assets contract: appmanager
	// pushes the bundle via pluginhost.assets_put when the app registers
	// (and swaps it atomically with artifact reloads), while restart
	// recovery happens here — pluginhost starts after appmanager in the
	// runtime tree, so it cannot rely on an appmanager push at its own
	// OnStart. Asset serving is intentionally independent of artifact
	// restore success: a failed .so restore leaves the plugin descriptor
	// in error state but the frontend bundle is still the declared one.
	a.restoreAssetRoutes()

	_ = ctx.Register("pluginhost.list_plugins", a.handleListPlugins, actor.Public(),
		actor.WithDescription("List loaded native plugins: id, name, version, runtime/ABI, capabilities, callable ids, trust class, and status (loading | active | error | unloaded, with Error text on transient invoke failures). Read-only."))
	// Per-plugin log capture query (agent/dev tool): newest backend + frontend
	// ring entries plus the inferred process verdict (running/stopped/crashed
	// with crash cause). Read-only, ring access is mutex-guarded.
	_ = ctx.Register("pluginhost.plugin_logs", a.handlePluginLogs, actor.Public(),
		actor.WithDescription("Fetch a plugin's captured logs: newest backend and frontend console ring entries, plus the inferred process verdict (running/stopped/crashed with crash cause). Read-only; use it when a plugin misbehaves."))
	// Frontend console entry ingestion: the host shell forwards plugin iframe
	// console/error output here. Log-only surface — no state beyond the ring.
	_ = ctx.Register("pluginhost.plugin_log_put", a.handlePluginLogPut, actor.Public())
	// Frontend DOM observability: plugin_dom asks the mounted iframe (through
	// the bridge port) to serialize its DOM and polls until the webview push
	// lands in domSnapshots; plugin_dom_put is that push. PureContext handlers
	// (stateless invoke goroutines), same exposure rationale as plugin_logs.
	_ = ctx.Register("pluginhost.plugin_dom", a.handlePluginDom, actor.Public(),
		actor.WithDescription("Request a plugin panel's serialized DOM: asks the mounted iframe through the bridge port to serialize, then waits for the webview push. Use it for layout and rendering diagnosis (structure, not pixels)."))
	_ = ctx.Register("pluginhost.plugin_dom_put", a.handlePluginDomPut, actor.Public())
	// Panel operations (dev-only plugins): browser-use-style precise control
	// of a mounted plugin panel iframe — dom (bounded serialization),
	// eval (bounded async JS), click, type, wait. Routed through
	// interfacemanager to the host webview and the plugin's bridge port;
	// results return via panel_op_put matched by RequestId. Same PureContext
	// exposure rationale as plugin_dom; the handler gates on the descriptor
	// Dev flag set by the appmanager dev loop.
	_ = ctx.Register("pluginhost.panel_op", a.handlePanelOp, actor.Public(),
		actor.WithDescription("Run a precise operation on a mounted dev-registered plugin panel: Op=dom|eval|click|type|wait (dom: serialize DOM with [k] action markers; eval: run bounded async JS and return JSON; click/type: target a CSS selector; wait: await a selector/text). Requires a plugin registered via the dev loop."))
	_ = ctx.Register("pluginhost.panel_op_put", a.handlePanelOpPut, actor.Public())
	_ = ctx.Register("pluginhost.register_actor", a.handleRegisterActor, actor.AdminOnly())
	_ = ctx.Register("pluginhost.unregister_actor", a.handleUnregisterActor, actor.AdminOnly())
	_ = ctx.Register("pluginhost.invoke", a.handleInvoke, actor.Public())
	// Streaming sibling of pluginhost.invoke: a streaming callable emits zero
	// or more PluginInvokeChunk frames (one per forward-chunk payload, then a
	// final Terminal=true chunk carrying the plugin's terminal response) over
	// the gospore stream. The actor framework detects streaming mode from the
	// actor.Emitter parameter; actor.Streaming stamps the chunk schema so
	// callers and codegen resolve the typed chunk. FFI/non-streaming
	// callables degrade to a single terminal chunk via the unary fallback.
	_ = ctx.Register("pluginhost.invoke_stream", a.handleInvokeStream, actor.Public(), actor.Streaming[gen.PluginInvokeChunk]())
	// Host→plugin event delivery: the event source (appmanager) fires this
	// as a fire-and-forget tell after EmitEvent; the handler fans the event
	// out to every loaded plugin whose manifest declares the kind. Pure
	// (stateless) so per-plugin delivery (bounded by the invoke timeout)
	// never blocks the owner lane; AdminOnly so only the host event source
	// can drive it — plugins must not be able to forge events to peers.
	_ = ctx.Register("pluginhost.event_deliver", a.handleEventDeliver, actor.AdminOnly())
	_ = ctx.Register("pluginhost.artifact_load", a.handleArtifactLoad, actor.AdminOnly())
	_ = ctx.Register("pluginhost.artifact_reload_prepare", a.handleArtifactReloadPrepare, actor.AdminOnly())
	_ = ctx.Register("pluginhost.artifact_reload_commit", a.handleArtifactReloadCommit, actor.AdminOnly())
	_ = ctx.Register("pluginhost.artifact_reload_abort", a.handleArtifactReloadAbort, actor.AdminOnly())
	_ = ctx.Register("pluginhost.artifact_unload", a.handleArtifactUnload, actor.AdminOnly())
	_ = ctx.Register("pluginhost.assets_put", a.handleAssetsPut, actor.AdminOnly())
	_ = ctx.Register("pluginhost.assets_remove", a.handleAssetsRemove, actor.AdminOnly())
	// Gateway reverse-proxy bridge (plugin gateway workflow): appmanager
	// attaches a committed backend's loopback listener to /plugin/{id}/ and
	// detaches it when the backend goes away; the proxy supersedes the
	// assets handler while attached (Router replace semantics).
	_ = ctx.Register("pluginhost.proxy_attach", a.handleProxyAttach, actor.AdminOnly())
	_ = ctx.Register("pluginhost.proxy_detach", a.handleProxyDetach, actor.AdminOnly())
	// Per-app document state (state.*) runs on its own lane: the store is
	// backend-pluggable (fs today, network backends via the persist
	// registry), and network IO must never block the owner lane (CLAUDE.md
	// Owner Lane 禁阻塞). Handlers mutate no actor tables, but a stateful
	// lane keeps per-app quota checks exact (single writer per store).
	if err := ctx.RegisterLoop("app_state", actor.ModeStateful); err != nil {
		return fmt.Errorf("pluginhost: register app_state loop: %w", err)
	}
	_ = ctx.Register("pluginhost.state_get", a.handleStateGet, actor.AdminOnly(), actor.WithLoop("app_state"))
	_ = ctx.Register("pluginhost.state_set", a.handleStateSet, actor.AdminOnly(), actor.WithLoop("app_state"))
	_ = ctx.Register("pluginhost.state_delete", a.handleStateDelete, actor.AdminOnly(), actor.WithLoop("app_state"))
	_ = ctx.Register("pluginhost.state_list", a.handleStateList, actor.AdminOnly(), actor.WithLoop("app_state"))
	_ = ctx.Register("pluginhost.state_append", a.handleStateAppend, actor.AdminOnly(), actor.WithLoop("app_state"))
	_ = ctx.Register("pluginhost.state_get_many", a.handleStateGetMany, actor.AdminOnly(), actor.WithLoop("app_state"))
	_ = ctx.Register("pluginhost.state_set_many", a.handleStateSetMany, actor.AdminOnly(), actor.WithLoop("app_state"))
	// state_purge is the explicit data reclamation callable: it drops the
	// app's whole "<pluginID>/" document subtree AND its app.data grant
	// directory. Unregister deliberately does not fire it (app data survives
	// re-register). Never routed through the plugin host bridge.
	_ = ctx.Register("pluginhost.state_purge", a.handleStatePurge, actor.AdminOnly(), actor.WithLoop("app_state"))
	// appdata_usage walks grant directories to sum sizes — potentially
	// seconds of file I/O, so it gets its own lane off the owner and
	// app_state lanes.
	if err := ctx.RegisterLoop("appdata_scan", actor.ModeStateful); err != nil {
		return fmt.Errorf("pluginhost: register appdata_scan loop: %w", err)
	}
	_ = ctx.Register("pluginhost.appdata_usage", a.handleAppDataUsage, actor.AdminOnly(), actor.WithLoop("appdata_scan"))
	// Build lane: subprocess compilation (go build, minutes) moves off the
	// owner lane so query callables (list_plugins, invoke dispatch) stay
	// responsive while a compile is in flight. The handler mutates no actor
	// state, so the plugin state tables keep a single writer (owner lane).
	if err := ctx.RegisterLoop("plugin_build", actor.ModeStateful); err != nil {
		return fmt.Errorf("pluginhost: register build loop: %w", err)
	}
	_ = ctx.Register("pluginhost.native_build", a.handleNativeBuild, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithLoop("plugin_build"),
	)

	if err := ctx.RegisterDomain("pluginhost").Expose(); err != nil {
		return fmt.Errorf("expose pluginhost: %w", err)
	}
	// Deterministic cold-start proxy reconciliation: the restore loop above
	// fired "running" reports before this Expose, so the proxy_attach tells
	// they triggered on the appmanager side could not resolve this service
	// and were dropped (never retried). Now that pluginhost is exposed, tell
	// appmanager to re-attach gateway routes for every running native app.
	// Fire-and-forget: when appmanager is not exposed yet (pluginhost-first
	// boot order), the tell is dropped and the appmanager's own spawn-loop
	// artifact_load verification covers the attach instead.
	a.notifyAppManagerOnline()
	return nil
}

// RegisterHandler registers a handler for callID. This is used by the C ABI
// bridge when a shared library loads and exposes its callables.
func (a *Actor) RegisterHandler(callID string, h HandlerFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handlers == nil {
		a.handlers = make(map[string]HandlerFunc)
	}
	a.handlers[callID] = h
}

// RegisterHandlerStream registers a streaming handler for callID alongside
// its unary sibling. Installed by the artifact loader when a streaming
// callable loads over a forward-chunk-capable transport (subprocess);
// FFI-loaded streaming callables register no stream handler and
// pluginhost.invoke_stream degrades to the unary path.
func (a *Actor) RegisterHandlerStream(callID string, h HandlerStreamFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.handlerStreams == nil {
		a.handlerStreams = make(map[string]HandlerStreamFunc)
	}
	a.handlerStreams[callID] = h
}

// UnregisterHandler removes a handler.
func (a *Actor) UnregisterHandler(callID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.handlers, callID)
	delete(a.handlerStreams, callID)
}

// artifactLoadOrder returns plugin IDs sorted by their manifest Dependencies
// using the shared topo.Sort algorithm. When dependencies reference IDs not
// present in the loads map, they are silently dropped (the individual load
// will validate them at runtime). Cycles cause a fallback to insertion order
// with a logged warning.
func artifactLoadOrder(loads map[string]gen.PluginArtifactLoadReq) []string {
	if len(loads) == 0 {
		return nil
	}
	// Stable: use sorted key order for deterministic fallback.
	ids := make([]string, 0, len(loads))
	for id := range loads {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	deps := make(map[string][]string, len(loads))
	for id, req := range loads {
		for _, d := range req.Manifest.Dependencies {
			// Only include deps that are also in the load set; missing
			// deps are validated by the individual load handler.
			if _, ok := loads[d.ID]; ok {
				deps[id] = append(deps[id], d.ID)
			}
		}
	}
	sorted, err := topo.Sort(ids, deps)
	if err != nil {
		// Cycle or unexpected error — fall back to insertion order.
		// The individual load will still catch hard errors.
		return ids
	}
	return sorted
}

// findPluginID resolves the plugin ID from a callable ID by matching against
// registered plugin prefixes. Plugin IDs can contain dots, so naive splitting
// is unreliable.
func (a *Actor) findPluginID(callID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, plugin := range a.Plugins {
		prefix := "plugin." + plugin.ID + "."
		if strings.HasPrefix(callID, prefix) {
			return plugin.ID
		}
	}
	return ""
}

func (a *Actor) allowsCallable(callID string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, plugin := range a.Plugins {
		prefix := "plugin." + plugin.ID + "."
		if !strings.HasPrefix(callID, prefix) {
			continue
		}
		for _, declared := range plugin.Callables {
			if declared == callID {
				return true
			}
		}
		for _, capability := range plugin.Capabilities {
			if capability == "app.invoke" && strings.HasPrefix(callID, prefix) {
				return true
			}
		}
		return false
	}
	return false
}

func (a *Actor) handler(callID string) (HandlerFunc, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	h, ok := a.handlers[callID]
	return h, ok
}

func (a *Actor) handlerStream(callID string) (HandlerStreamFunc, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	h, ok := a.handlerStreams[callID]
	return h, ok
}

type listPluginsReq struct{}

// ListPluginsResp wraps the plugin descriptor list. Handler returns must
// not be top-level struct arrays (callable-surface constraint), so the
// slice rides inside a struct.
type ListPluginsResp struct {
	Plugins []PluginDescriptor
}

// handleListPlugins is a stateless (PureContext) snapshot read: it copies the
// plugin table under a.mu and overlays transient invoke-error states (an
// atomic map), never mutating durable state, so it runs on the forked pure
// loop and never parks the owner lane.
func (a *Actor) handleListPlugins(_ actor.PureContext, _ listPluginsReq) (ListPluginsResp, error) {
	a.mu.RLock()
	out := make([]PluginDescriptor, len(a.Plugins))
	copy(out, a.Plugins)
	a.mu.RUnlock()
	// Merge transient invoke-error states recorded by the stateless
	// handleInvoke. These are kept outside the Plugins component to avoid
	// racing the cell's projection snapshot.
	for i := range out {
		if msg, ok := a.pluginErrStates.Load(out[i].ID); ok {
			out[i].Status = "error"
			out[i].Error = msg.(string)
		}
	}
	return ListPluginsResp{Plugins: out}, nil
}

type registerActorReq struct {
	PluginID  string
	Name      string
	Version   string
	Namespace string
	CallIDs   []string
	Manifest  *gen.AppManifest
	Abi       *gen.PluginAbi
}

type registerActorRes struct {
	Registered int
}

func (a *Actor) handleRegisterActor(ctx actor.Context, req registerActorReq) (registerActorRes, error) {
	if req.PluginID == "" {
		return registerActorRes{}, fmt.Errorf("plugin_id is required")
	}
	var schemaHash string
	if req.Manifest != nil && req.Abi != nil {
		schemaHash = pluginhost.ComputeSchemaHash(req.Manifest.Schemas)
		if err := pluginhost.ValidateNativeManifest(*req.Manifest, *req.Abi, schemaHash); err != nil {
			return registerActorRes{}, err
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Upsert plugin descriptor.
	found := -1
	for i, p := range a.Plugins {
		if p.ID == req.PluginID {
			found = i
			break
		}
	}
	desc := PluginDescriptor{ID: req.PluginID, Name: req.Name, Version: req.Version, Namespace: req.Namespace, Runtime: pluginhost.NativeRuntime, Encoding: pluginhost.BinaryCodecV1, LoadedAt: time.Now(), Status: "active", Callables: append([]string(nil), req.CallIDs...)}
	if req.Manifest != nil && req.Abi != nil {
		desc.Name = req.Manifest.Name
		desc.Version = req.Manifest.Version
		desc.Namespace = req.Manifest.Namespace
		desc.AbiName = req.Abi.Name
		desc.AbiVersion = req.Abi.Version
		desc.Encoding = req.Abi.Encoding
		desc.Capabilities = append([]string(nil), req.Abi.Capabilities...)
		desc.Callables = append([]string(nil), req.CallIDs...)
		for _, callable := range req.Manifest.Callables {
			desc.Callables = append(desc.Callables, pluginhost.PluginCallID(req.PluginID, callable.ID))
		}
		desc.Isolation = req.Abi.Isolation
		desc.TrustClass = req.Abi.TrustClass
		desc.Signer = req.Abi.Signer
		desc.Trusted = req.Abi.TrustClass == pluginhost.TrustFirstParty && strings.TrimSpace(req.Abi.Signer) != ""
		desc.SchemaHash = schemaHash
	}

	if found >= 0 {
		a.Plugins[found] = desc
	} else {
		a.Plugins = append(a.Plugins, desc)
	}

	return registerActorRes{Registered: len(req.CallIDs)}, nil
}

type unregisterActorReq struct {
	PluginID string
}

type unregisterActorRes struct {
	Removed int
}

func (a *Actor) handleUnregisterActor(ctx actor.Context, req unregisterActorReq) (unregisterActorRes, error) {
	a.mu.Lock()

	removed := 0
	for callID := range a.handlers {
		// Plugin callables are namespaced: plugin.<pluginID>.<actor>.<method>
		prefix := "plugin." + req.PluginID + "."
		if len(callID) > len(prefix) && callID[:len(prefix)] == prefix {
			delete(a.handlers, callID)
			removed++
		}
	}

	// Grab the closer but invoke it after unlocking: the process close path
	// re-enters ReportProcessState → detachProxy → a.mu and would
	// self-deadlock under the held lock.
	var closer func() error
	if c, ok := a.closers[req.PluginID]; ok {
		closer = c
		delete(a.closers, req.PluginID)
	}

	// Update plugin descriptor status.
	for i, p := range a.Plugins {
		if p.ID == req.PluginID {
			a.Plugins[i].Status = "unloaded"
			break
		}
	}
	a.mu.Unlock()

	var closeErr error
	if closer != nil {
		closeErr = closer()
	}

	// Drop the HTTP asset route (if any) together with the plugin so
	// /plugin/{pluginId}/ stops serving after unregistration.
	a.dropAssetsRouter(req.PluginID)

	return unregisterActorRes{Removed: removed}, closeErr
}

// invokeTimeout is the maximum time a plugin callable may run before being
// considered hung. Native plugins that exceed this are marked as errored.
const invokeTimeout = 30 * time.Second

// invokeMuPoll remains as a queue-granularity unit for the single-flight
// admission regression test (process_session_test.go), which scales its
// queued-invoke budget by it. The admission path itself no longer polls — the
// single-flight token is a channel handed off the instant the running invoke
// releases.
const invokeMuPoll = 10 * time.Millisecond

// handleInvoke dispatches a callable to a registered plugin handler. The wire
// contract is gen.PluginInvokeReq{ID, Callable, ...}: the caller supplies the
// plugin ID and the LOCAL callable name; the host namespaces them into the
// canonical route via pluginhost.PluginCallID to resolve the handler.
//
// This handler is declared with actor.PureContext (not actor.Context) so the
// gospore cell dispatches it on a forked goroutine (ModeStateless). This
// decouples blocking synchronous C calls from the cell's message loop,
// allowing concurrent invokes across all plugins. All actor state accessed
// here is either immutable or guarded by a.mu (RLock); the only mutation —
// recording an error — goes through the thread-safe pluginErrStates
// sync.Map rather than the public Plugins component, to avoid racing the
// cell's projection snapshot.
func (a *Actor) handleInvoke(ctx actor.PureContext, req gen.PluginInvokeReq) (gen.PluginInvokeResp, error) {
	callID := pluginhost.PluginCallID(req.ID, req.Callable)
	pluginID := a.findPluginID(callID)
	if pluginID != "" && !a.allowsCallable(callID) {
		return gen.PluginInvokeResp{}, fmt.Errorf("plugin callable %q is not declared", callID)
	}
	h, ok := a.handler(callID)
	if !ok {
		return gen.PluginInvokeResp{}, fmt.Errorf("plugin callable %q not found", callID)
	}

	var callCtx context.Context = context.Background()
	if ctx != nil {
		callCtx = ctx.Lifecycle()
	}

	// Enforce a timeout to prevent hung native plugins from blocking the host.
	// Use the per-callable budget forwarded by the appmanager when it is set;
	// otherwise fall back to the default 30s bound so legacy callers keep the
	// previous behavior.
	timeout := invokeTimeout
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}
	timeoutCtx, cancel := context.WithTimeout(callCtx, timeout)
	defer cancel()

	// Attach traceable metadata so loader/transport layers can propagate it
	// into the native ABI envelope. AgentID/WorkspaceID are forwarded as
	// caller identity so bridge-routed llm.* reverse calls attribute usage
	// stats and provider assignment to the originating agent/workspace —
	// WorkspaceID is the workspace ACTOR id the turn engine stamped onto
	// appmanager.invoke (zero for UI/external callers, in which case the
	// aiaggregator stats gate skips the record exactly as before).
	meta := pluginhost.InvokeMeta{
		RequestID:   req.RequestID,
		SessionID:   req.SessionID,
		CallSeq:     req.CallSeq,
		AgentID:     req.AgentID,
		WorkspaceID: req.WorkspaceID,
	}
	invokeCtx := pluginhost.WithInvokeMeta(timeoutCtx, meta)

	// Recover from panics in native plugin handlers to prevent host crash.
	resp, err := safeInvoke(invokeCtx, h, req.Payload)
	if err != nil {
		// Record the error in the thread-safe map (not the component field,
		// which must only be mutated from the cell goroutine).
		if pluginID != "" {
			a.pluginErrStates.Store(pluginID, err.Error())
		}
		return gen.PluginInvokeResp{}, err
	}
	// Clear any previous error state on success.
	if pluginID != "" {
		a.pluginErrStates.Delete(pluginID)
	}
	if a.loader != nil {
		if limit := a.loader.MaxOutputBytes(req.ID); limit > 0 && uint64(len(resp)) > limit {
			return gen.PluginInvokeResp{}, fmt.Errorf("pluginhost: output budget exceeded: %d > %d bytes", len(resp), limit)
		}
	}
	return gen.PluginInvokeResp{Payload: resp}, nil
}

// handleInvokeStream is the streaming sibling of handleInvoke: it resolves
// the callID the same way, then drives the registered HandlerStream —
// emitting one PluginInvokeChunk per forward-chunk payload and a final
// Terminal=true chunk carrying the terminal response — over the gospore
// Emitter. It takes actor.PureContext (unlike the unary handleInvoke) so the
// call runs on the forked stateless goroutine: a streaming invoke stays open
// for the whole stream (up to TimeoutMs) and must never block the owner lane.
//
// Degrade path: a callable with no stream handler (FFI transport, or a
// non-streaming callable addressed here) falls back to the unary handler and
// emits a single terminal chunk — streaming callers get the terminal even
// when the transport carries no forward-chunk wire.
func (a *Actor) handleInvokeStream(ctx actor.PureContext, req gen.PluginInvokeReq, emit actor.Emitter) error {
	if emit == nil {
		return fmt.Errorf("pluginhost: invoke_stream requires a streaming emitter")
	}
	callID := pluginhost.PluginCallID(req.ID, req.Callable)
	pluginID := a.findPluginID(callID)
	if pluginID != "" && !a.allowsCallable(callID) {
		return fmt.Errorf("plugin callable %q is not declared", callID)
	}
	hs, hasStream := a.handlerStream(callID)

	var callCtx context.Context = context.Background()
	if ctx != nil {
		callCtx = ctx.Lifecycle()
	}
	timeout := invokeTimeout
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}
	timeoutCtx, cancel := context.WithTimeout(callCtx, timeout)
	defer cancel()
	meta := pluginhost.InvokeMeta{
		RequestID:   req.RequestID,
		SessionID:   req.SessionID,
		CallSeq:     req.CallSeq,
		AgentID:     req.AgentID,
		WorkspaceID: req.WorkspaceID,
	}
	invokeCtx := pluginhost.WithInvokeMeta(timeoutCtx, meta)

	emitChunk := func(payload []byte, terminal bool) error {
		return emit.Send(gen.PluginInvokeChunk{Payload: payload, Terminal: terminal})
	}

	if hasStream {
		onChunk := func(p []byte) error {
			return emitChunk(p, false)
		}
		terminal, err := safeInvokeStream(invokeCtx, hs, req.Payload, onChunk)
		if err != nil {
			if pluginID != "" {
				a.pluginErrStates.Store(pluginID, err.Error())
			}
			return err
		}
		if pluginID != "" {
			a.pluginErrStates.Delete(pluginID)
		}
		return emitChunk(terminal, true)
	}

	// Degrade: no stream handler. Run the unary callable and emit a single
	// terminal chunk (zero intermediate chunks), preserving the streaming
	// contract for callers over transports without a forward-chunk wire.
	h, ok := a.handler(callID)
	if !ok {
		return fmt.Errorf("plugin callable %q not found", callID)
	}
	resp, err := safeInvoke(invokeCtx, h, req.Payload)
	if err != nil {
		if pluginID != "" {
			a.pluginErrStates.Store(pluginID, err.Error())
		}
		return err
	}
	if pluginID != "" {
		a.pluginErrStates.Delete(pluginID)
	}
	return emitChunk(resp, true)
}

// safeInvokeStream wraps a streaming handler call with panic recovery,
// mirroring safeInvoke for the unary path.
func safeInvokeStream(ctx context.Context, h pluginhost.HandlerStreamFunc, payload []byte, onChunk func([]byte) error) (terminal []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("plugin crashed: %v", r)
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("plugin invoke cancelled: %w", err)
	}
	return h(ctx, payload, onChunk)
}

// handleEventDeliver fans one host bus event out to every loaded plugin whose
// manifest declares the kind in Listens. The plugin receives it under the
// reserved `__event__:<kind>` dispatch name, which the SDK maps to
// RegisterEventListener handlers.
//
// Delivery is asynchronous: each event is enqueued on the plugin's bounded
// drain queue (see enqueueHostEvent) and invoked strictly in FIFO order by a
// single drainer goroutine, interleaving fairly with data-plane invokes at
// the session's single-flight semaphore. The agent step fan-out runs at
// hundreds of events per second while any agent streams; a synchronous
// per-event invoke starved data-plane callers behind the flood and, when a
// queued invoke's budget expired mid-admission, got the process killed (the
// novelking crash-loop). Failures surface in the plugin error state, matching
// handleInvoke's semantics; the response counts queued deliveries (the caller
// is a fire-and-forget tell).
func (a *Actor) handleEventDeliver(ctx actor.PureContext, req gen.PluginEventDeliverReq) (gen.PluginEventDeliverResp, error) {
	if strings.TrimSpace(req.Kind) == "" {
		return gen.PluginEventDeliverResp{}, fmt.Errorf("pluginhost: event deliver kind is required")
	}
	if _, ok := appbinding.LookupHostEvent(req.Kind); !ok {
		return gen.PluginEventDeliverResp{}, fmt.Errorf(
			"pluginhost: event kind %q is not a subscribable host event (allowed: %s)",
			req.Kind, strings.Join(appbinding.SubscribableEventKinds(), ", "))
	}
	hostEvent, _ := appbinding.LookupHostEvent(req.Kind)
	if a.loader == nil {
		return gen.PluginEventDeliverResp{Delivered: 0}, nil
	}

	var callCtx context.Context = context.Background()
	if ctx != nil {
		callCtx = ctx.Lifecycle()
	}

	var failures []string
	queued := 0
	for _, pluginID := range a.loader.Listeners(req.Kind) {
		if hostEvent.RequiresCapability != "" && !a.loader.HasPermission(pluginID, hostEvent.RequiresCapability) {
			failures = append(failures, fmt.Sprintf(
				"%s: event kind %q requires capability %q not declared by the plugin manifest",
				pluginID, req.Kind, hostEvent.RequiresCapability))
			continue
		}
		a.enqueueHostEvent(callCtx, pluginID, req.Kind, req.Payload)
		queued++
	}
	return gen.PluginEventDeliverResp{Delivered: int32(queued), Failures: failures}, nil
}

// eventQueueDepth bounds pending host-event deliveries per plugin. Step floods
// run at hundreds of events per second per streaming agent; beyond the bound
// the oldest pending event is dropped (best-effort delivery by design —
// streaming surfaces tolerate gaps; an unbounded backlog would instead starve
// the plugin's data plane behind the flood).
const eventQueueDepth = 256

// hostEventDispatch is one queued host bus event.
type hostEventDispatch struct {
	kind    string
	payload string
}

// eventDrainQueue is the per-plugin FIFO of pending host events with its
// single drainer goroutine. life is the lifecycle context captured at queue
// creation, used as the parent of every per-event invoke budget.
type eventDrainQueue struct {
	ch   chan hostEventDispatch
	life context.Context
}

// pushBounded enqueues ev on a bounded queue, evicting the oldest pending
// event when full (recency beats completeness for streaming surfaces).
func pushBounded(q chan hostEventDispatch, ev hostEventDispatch) {
	select {
	case q <- ev:
	default:
		select {
		case <-q:
		default:
		}
		select {
		case q <- ev:
		default:
		}
	}
}

// enqueueHostEvent appends one event to the plugin's drain queue, creating
// the queue (and starting its drainer) on first use.
func (a *Actor) enqueueHostEvent(life context.Context, pluginID, kind, payload string) {
	if v, ok := a.eventQueues.Load(pluginID); ok {
		pushBounded(v.(*eventDrainQueue).ch, hostEventDispatch{kind: kind, payload: payload})
		return
	}
	if life == nil {
		life = context.Background()
	}
	q := &eventDrainQueue{ch: make(chan hostEventDispatch, eventQueueDepth), life: life}
	actual, loaded := a.eventQueues.LoadOrStore(pluginID, q)
	if !loaded {
		go a.drainHostEvents(pluginID, actual.(*eventDrainQueue))
	}
	pushBounded(actual.(*eventDrainQueue).ch, hostEventDispatch{kind: kind, payload: payload})
}

// drainHostEvents delivers queued events one at a time, strictly in arrival
// order, each bounded by the standard invoke timeout. Handler-missing events
// (plugin unloaded between enqueue and drain) are dropped quietly — the
// loader's Listeners/permission gates already ran at enqueue time.
func (a *Actor) drainHostEvents(pluginID string, q *eventDrainQueue) {
	for ev := range q.ch {
		h, ok := a.handler(pluginhost.EventRoute(pluginID, ev.kind))
		if !ok {
			continue
		}
		timeoutCtx, cancel := context.WithTimeout(q.life, invokeTimeout)
		_, err := safeInvoke(timeoutCtx, h, []byte(ev.payload))
		cancel()
		if err != nil {
			a.pluginErrStates.Store(pluginID, err.Error())
			continue
		}
		a.pluginErrStates.Delete(pluginID)
	}
}

// safeInvoke wraps a plugin handler call with panic recovery.
func safeInvoke(ctx context.Context, h HandlerFunc, payload []byte) (resp []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("plugin crashed: %v", r)
		}
	}()
	// Check context before calling
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("plugin invoke cancelled: %w", err)
	}
	return h(ctx, payload)
}

// hostBridgeDispatchTranslations maps SDK-facing host callIDs to the actual
// callable names registered by the backing services. The prefix of the
// target callID selects the service from the captured service refs. The
// table lives in pkg/appbinding (HostCallAliases) as the single source of
// truth shared with the appmanager protocol query/extraction.
var hostBridgeDispatchTranslations = appbinding.HostCallAliases

// handleHostBridgeConfigGet serves the SDK config.get callID locally. It
// reads from pkg/config for the "host" scope and returns the value as JSON
// bytes. Unknown scopes or keys return null bytes rather than failing so
// the host call contract stays simple for plugins.
func (a *Actor) handleHostBridgeConfigGet(req []byte) ([]byte, error) {
	var r struct {
		Scope string `json:"scope"`
		Key   string `json:"key"`
	}
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, fmt.Errorf("pluginhost: config.get decode: %w", err)
	}
	if r.Scope != "host" {
		return json.Marshal(nil)
	}
	var v any
	switch r.Key {
	case "data_dir":
		v = config.DataDir()
	case "exe_dir":
		v = config.ExeDir()
	case "gateway_addr":
		v = config.GatewayAddr()
	case "gateway_bind_addrs":
		v = config.GatewayBindAddrs()
	default:
		v = nil
	}
	return json.Marshal(v)
}

// handleHostBridgeConfigSet serves the SDK config.set callID locally. Global
// host configuration is mutable singleton state owned by pkg.config; the host
// bridge exposes it read-only to plugins to avoid cross-actor writes.
func (a *Actor) handleHostBridgeConfigSet(_ []byte) ([]byte, error) {
	return nil, fmt.Errorf("pluginhost: config.set is read-only via host bridge")
}

// handleHostBridgeProviderGet serves the SDK provider.get callID locally by
// reading the public provider list from aimanager and returning the provider
// whose Name matches the requested id. This keeps read-only provider
// inspection on a public callable without adding provider-secret handling
// to pluginhost.
func (a *Actor) handleHostBridgeProviderGet(aimanagerRef ref.Ref, req []byte) ([]byte, error) {
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, fmt.Errorf("pluginhost: provider.get decode: %w", err)
	}
	if aimanagerRef == nil {
		return nil, fmt.Errorf("pluginhost: aimanager service not available for provider.get")
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostBridgeInvokeTimeout)
	defer cancel()
	call := aimanagerRef.Invoke(ctx, "aimanager.provider_list", nil)
	if call == nil {
		return nil, fmt.Errorf("pluginhost: aimanager.provider_list invoke returned nil")
	}
	defer call.Close()
	raw, err := call.RecvRaw()
	if err != nil {
		return nil, fmt.Errorf("pluginhost: provider.get: aimanager.provider_list: %w", err)
	}
	var resp domain.ProviderListResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("pluginhost: provider.get decode list: %w", err)
	}
	for _, p := range resp.Items {
		if p.Name == r.ID {
			return json.Marshal(p)
		}
	}
	return json.Marshal(nil)
}

// handleHostBridgeDbProfileList serves the SDK db.profile_list callID
// locally: it forwards to dbmanager's masked profile list and returns it
// unchanged. dbmanager.profile_list is AdminOnly and predates the capability
// system, so the hop carries pluginhostAdminCallerHeaders — the plugin-side
// gate is the db.profile.read capability grant.
func (a *Actor) handleHostBridgeDbProfileList(dbmRef ref.Ref) ([]byte, error) {
	if dbmRef == nil {
		return nil, fmt.Errorf("pluginhost: dbmanager service not available for db.profile_list")
	}
	raw, err := invokeHostUnary(dbmRef, "dbmanager.profile_list", nil, pluginhostAdminCallerHeaders)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: db.profile_list: %w", err)
	}
	return raw, nil
}

// handleHostBridgeDbProfileDial serves the SDK db.profile_dial callID
// locally: it resolves a dbmanager profile into dial-ready parameters —
// non-secret dial fields via profile_lookup, the full credential via
// profile_resolve (both ungated Internal surfaces), the SSH tunnel via the
// process-wide persist resolver (DialAddr becomes the tunnel's loopback
// listener), and the decision-point-4 TLS default. The plugin dials DialAddr
// directly. Raw secrets cross this surface; the gate is the db.dial
// capability grant (RiskHigh).
func (a *Actor) handleHostBridgeDbProfileDial(dbmRef ref.Ref, req []byte) ([]byte, error) {
	var r appbinding.DbProfileDialReq
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, fmt.Errorf("pluginhost: db.profile_dial decode: %w", err)
	}
	if r.ID == "" {
		return nil, fmt.Errorf("pluginhost: db.profile_dial: id is required")
	}
	if dbmRef == nil {
		return nil, fmt.Errorf("pluginhost: dbmanager service not available for db.profile_dial")
	}
	lookupRaw, err := invokeHostUnary(dbmRef, "dbmanager.profile_lookup", gen.DbProfileLookupReq{ID: r.ID}, nil)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: db.profile_dial: %w", err)
	}
	var lookup gen.DbProfileLookupResp
	if err := json.Unmarshal(lookupRaw, &lookup); err != nil {
		return nil, fmt.Errorf("pluginhost: db.profile_dial decode lookup: %w", err)
	}
	resolveRaw, err := invokeHostUnary(dbmRef, "dbmanager.profile_resolve", gen.DbProfileResolveReq{ID: r.ID}, nil)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: db.profile_dial: %w", err)
	}
	var cred gen.DbProfileResolveResp
	if err := json.Unmarshal(resolveRaw, &cred); err != nil {
		return nil, fmt.Errorf("pluginhost: db.profile_dial decode resolve: %w", err)
	}
	p := lookup.Profile
	dialAddr := p.Endpoint
	if p.TunnelRef != "" {
		local, err := persist.ResolveTunnel(p.TunnelRef, p.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("pluginhost: db.profile_dial: tunnel: %w", err)
		}
		dialAddr = local
	}
	resp := appbinding.DbProfileDialResp{
		Backend:   p.Backend,
		Endpoint:  p.Endpoint,
		Database:  p.Database,
		DialAddr:  dialAddr,
		TLS:       persist.BackendTLSFor(p.Endpoint, p.TunnelRef),
		Username:  cred.Username,
		Password:  cred.Password,
		AccessKey: cred.AccessKey,
		Secret:    cred.Secret,
		Token:     cred.Token,
	}
	if resp.TLS {
		resp.TLSServerName = endpointHost(p.Endpoint)
	}
	// Mirror pkg/persist/mongo.go: a configured username authenticates
	// against the admin database.
	if p.Backend == "mongo" && cred.Username != "" {
		resp.AuthSource = "admin"
	}
	return json.Marshal(resp)
}

// invokeHostUnary performs one reverse-call hop to a system service with the
// generic host-bridge timeout.
func invokeHostUnary(svc ref.Ref, callID string, payload any, headers map[string]string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), hostBridgeInvokeTimeout)
	defer cancel()
	call := svc.Invoke(ctx, callID, payload, headers)
	if call == nil {
		return nil, fmt.Errorf("%s invoke returned nil", callID)
	}
	defer call.Close()
	return call.RecvRaw()
}

// endpointHost extracts the bare host from a host:port or scheme:// endpoint
// for use as the TLS ServerName when dialing through a tunnel.
func endpointHost(endpoint string) string {
	host := endpoint
	if i := strings.LastIndex(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return strings.TrimSpace(host)
}

// pluginhostAdminCallerHeaders carries the internal identity pluginhost uses
// for hops to human-facing AdminOnly callables behind capability-gated local
// host calls (browser.cookies_export → browsermanager.export_cookies,
// db.profile_list → dbmanager.profile_list). The plugin's authorization
// already happened at the host bridge (capability grant); this header only
// satisfies the callee's RequireAdmin gate, which predates the capability
// system and would otherwise reject every in-tree caller (internal
// actor→actor invokes carry zero identity). Callees keep their AdminOnly
// semantics untouched for every other caller.
var pluginhostAdminCallerHeaders = map[string]string{
	"gospore.caller_role":    "admin",
	"gospore.caller_subject": "sporemind-pluginhost",
}

// reservedCookieProfileError rejects identifiers that name non-independent
// browser profiles. "global" is the host's own shared login state; "app-*"
// are per-app panel profiles. Both are outside the browser.cookies.read
// grant even though they cannot resolve through the manager's instance list.
func reservedCookieProfileError(idOrName string) error {
	switch {
	case idOrName == "global":
		return fmt.Errorf("pluginhost: browser.cookies_export: %q is the shared host browser profile; only independent instances are readable via browser.cookies.read", idOrName)
	case strings.HasPrefix(idOrName, "app-"):
		return fmt.Errorf("pluginhost: browser.cookies_export: %q is an app panel profile; only independent instances are readable via browser.cookies.read", idOrName)
	}
	return nil
}

// normalizeCookieDomain makes cookie domains comparable: trims the leading
// host-only dot and lowercases, so ".ollama.com" and "ollama.com" match.
func normalizeCookieDomain(d string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "."))
}

// handleHostBridgeBrowserCookiesExport serves the SDK browser.cookies_export
// callID locally: resolve the requested instance against browsermanager's
// managed instance list, reject every non-independent profile, then export
// that instance's cookies as the host and optionally filter them by domain.
//
// The instance list is the whitelist: browsermanager.Instances holds only
// user-created independent windows (inst-N; the legacy "global" row is
// dropped at load), while the shared global tab and app panel sessions are
// desktop-side sessions that never appear in it. A hit in the list is
// therefore by construction an independent profile; the reserved-identifier
// guards exist so obvious misuse (passing "global" or an app profile id)
// fails with a clear policy error instead of a bare not-found.
func (a *Actor) handleHostBridgeBrowserCookiesExport(req []byte) ([]byte, error) {
	var r appbinding.BrowserCookiesExportReq
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, fmt.Errorf("pluginhost: browser.cookies_export decode: %w", err)
	}
	if r.ID == "" {
		return nil, fmt.Errorf("pluginhost: browser.cookies_export: instance id is required")
	}
	if err := reservedCookieProfileError(r.ID); err != nil {
		return nil, err
	}
	bm := a.streamServiceRef("browsermanager")
	if bm == nil {
		return nil, fmt.Errorf("pluginhost: browsermanager service not available for browser.cookies_export")
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostBridgeInvokeTimeout)
	defer cancel()

	call := bm.Invoke(ctx, "browsermanager.list", nil)
	if call == nil {
		return nil, fmt.Errorf("pluginhost: browsermanager.list invoke returned nil")
	}
	raw, err := call.RecvRaw()
	call.Close()
	if err != nil {
		return nil, fmt.Errorf("pluginhost: browser.cookies_export: browsermanager.list: %w", err)
	}
	var list domain.BrowserManagerListResp
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("pluginhost: browser.cookies_export decode list: %w", err)
	}
	var instanceID string
	for _, inst := range list.Items {
		if inst.Config.ID == r.ID || inst.Config.Name == r.ID {
			instanceID = inst.Config.ID
			break
		}
	}
	if instanceID == "" {
		return nil, fmt.Errorf("pluginhost: browser.cookies_export: instance %q not found; only managed independent instances are readable via browser.cookies.read", r.ID)
	}
	if err := reservedCookieProfileError(instanceID); err != nil {
		return nil, err
	}

	exportReq, err := json.Marshal(gen.BrowserManagerExportCookiesReq{ID: instanceID})
	if err != nil {
		return nil, fmt.Errorf("pluginhost: browser.cookies_export encode: %w", err)
	}
	call = bm.Invoke(ctx, "browsermanager.export_cookies", exportReq, pluginhostAdminCallerHeaders)
	if call == nil {
		return nil, fmt.Errorf("pluginhost: browsermanager.export_cookies invoke returned nil")
	}
	raw, err = call.RecvRaw()
	call.Close()
	if err != nil {
		return nil, fmt.Errorf("pluginhost: browser.cookies_export: browsermanager.export_cookies: %w", err)
	}
	var resp gen.BrowserManagerExportCookiesResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("pluginhost: browser.cookies_export decode resp: %w", err)
	}
	if want := normalizeCookieDomain(r.Domain); want != "" {
		filtered := make(map[string][]gen.BrowserCookieEntry, len(resp.Cookies))
		for site, cookies := range resp.Cookies {
			var kept []gen.BrowserCookieEntry
			for _, ck := range cookies {
				if normalizeCookieDomain(ck.Domain) == want {
					kept = append(kept, ck)
				}
			}
			if len(kept) > 0 {
				filtered[site] = kept
			}
		}
		resp.Cookies = filtered
	}
	return json.Marshal(resp)
}

// handleHostBridgeBrowserInstancesList serves the SDK
// browser_instances_list: a thin projection of browsermanager.list onto the
// minimal {Id, Name, Url, Title} refs. browsermanager.list already drops the
// global row, so every item is an independent instance by construction; no
// admin headers needed (Public callable), and config fields beyond the ref
// four (Proxy etc.) are never marshaled out.
func (a *Actor) handleHostBridgeBrowserInstancesList(_ []byte) ([]byte, error) {
	bm := a.streamServiceRef("browsermanager")
	if bm == nil {
		return nil, fmt.Errorf("pluginhost: browsermanager service not available for browser.instances.list")
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostBridgeInvokeTimeout)
	defer cancel()

	call := bm.Invoke(ctx, "browsermanager.list", nil)
	if call == nil {
		return nil, fmt.Errorf("pluginhost: browsermanager.list invoke returned nil")
	}
	raw, err := call.RecvRaw()
	call.Close()
	if err != nil {
		return nil, fmt.Errorf("pluginhost: browser.instances.list: browsermanager.list: %w", err)
	}
	var list domain.BrowserManagerListResp
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("pluginhost: browser.instances.list decode list: %w", err)
	}
	resp := appbinding.BrowserInstancesListResp{Items: make([]appbinding.BrowserInstanceRef, 0, len(list.Items))}
	for _, inst := range list.Items {
		resp.Items = append(resp.Items, appbinding.BrowserInstanceRef{
			ID:    inst.Config.ID,
			Name:  inst.Config.Name,
			URL:   inst.Config.URL,
			Title: inst.Status.Title,
		})
	}
	return json.Marshal(resp)
}

// handleHostBridgeStream is the generic streaming forwarder for every
// callID registered in the appbinding stream catalog. It knows nothing about
// the callID's domain: the route carries the target (service, callable), the
// payload adapter, the chunk encoder, and the terminal aggregator, and this
// loop only moves bytes — Invoke → Next → encode → onChunk → aggregate →
// terminal. An onChunk error aborts the stream: the backing call is closed
// and the error surfaces to the plugin as a __host_error__ terminal, so the
// SDK's InvokeStream unwinds cleanly.
//
// dctx.Parent (process transport) becomes the cancellation root: the
// reverse-cancel frame fires it and the Next loop unwinds with the parent's
// error instead of running the upstream LLM stream to completion.
func (a *Actor) handleHostBridgeStream(dctx DispatchContext, target ref.Ref, route appbinding.StreamRoute, callID string, req []byte, onChunk func([]byte) error) ([]byte, error) {
	if target == nil {
		return nil, fmt.Errorf("pluginhost: %s service %q not available", callID, route.Service)
	}
	// Inherit the outer invoke budget when the reverse transport forwarded
	// it via __DeadlineAt (process transport). The FFI/c-shared transport has
	// no rctx channel, so DeadlineAt is absent there and we fall back to the
	// route's Budget (LLM generation legitimately runs minutes) or the fixed
	// host-bridge cap.
	fallback := hostBridgeInvokeTimeout
	if route.Budget > 0 {
		fallback = route.Budget
	}
	ctx, cancel := streamBudgetContext(dctx.Parent, req, fallback)
	defer cancel()

	body := req
	if route.AdaptReq != nil {
		adapted, err := route.AdaptReq(req)
		if err != nil {
			return nil, fmt.Errorf("pluginhost: %s adapt request: %w", callID, err)
		}
		body = adapted
	}

	var agg appbinding.StreamAggregator
	if route.Aggregate != nil {
		agg = route.Aggregate()
	}

	call := target.Invoke(ctx, route.Callable, body)
	if call == nil {
		return nil, fmt.Errorf("pluginhost: %s invoke returned nil", callID)
	}
	defer call.Close()

	for {
		v, err := call.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("pluginhost: %s stream: %w", callID, err)
		}
		if onChunk != nil {
			envelope, eerr := route.Encode(v)
			if eerr != nil {
				_, _ = call.Final(ctx)
				return nil, fmt.Errorf("pluginhost: %s encode chunk: %w", callID, eerr)
			}
			wire, merr := json.Marshal(envelope)
			if merr != nil {
				_, _ = call.Final(ctx)
				return nil, fmt.Errorf("pluginhost: %s marshal chunk: %w", callID, merr)
			}
			if cerr := onChunk(wire); cerr != nil {
				_, _ = call.Final(ctx)
				return nil, fmt.Errorf("pluginhost: %s stream aborted by consumer: %w", callID, cerr)
			}
		}
		if agg != nil {
			if perr := agg.Push(v); perr != nil {
				_, _ = call.Final(ctx)
				return nil, fmt.Errorf("pluginhost: %s aggregate chunk: %w", callID, perr)
			}
		}
	}
	// Consume the stream close frame so server-side resources are released.
	final, _ := call.Final(ctx)

	if agg != nil {
		return json.Marshal(agg.Terminal())
	}
	// No aggregator: pass the backing call's Final value through verbatim
	// (callables that emit a complete terminal themselves). Emitter-based
	// streams have no Final value, so this yields an empty JSON object.
	if final == nil {
		return []byte("{}"), nil
	}
	if raw, ok := final.([]byte); ok {
		return raw, nil
	}
	out, err := json.Marshal(final)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s marshal terminal: %w", callID, err)
	}
	return out, nil
}

// streamBudgetContext derives the context for a streaming host-bridge
// reverse call. When the process transport forwards the outer invoke
// deadline via __DeadlineAt, the backing call runs until deadline minus the
// same unwind headroom used by reverseBudgetContext. Otherwise it falls
// back to the caller-provided cap — the route's Budget when the route
// registered one, else the fixed hostBridgeInvokeTimeout (the behavior for
// the FFI/c-shared transport, which has no DispatchContext channel).
// streamBudgetContext derives the upstream invoke context from the reverse
// payload's __DeadlineAt (outer invoke budget, forwarded by the process
// transport), falling back to the route/host-bridge cap. parent, when set
// (process transport's dispatch ctx), is the cancellation root: cancelling it
// cancels ctx and unwinds the Next loop — the 0x09 reverse-cancel path.
func streamBudgetContext(parent context.Context, req []byte, fallback time.Duration) (context.Context, context.CancelFunc) {
	base := parent
	if base == nil {
		base = context.Background()
	}
	var payload map[string]any
	if len(req) > 0 {
		if err := json.Unmarshal(req, &payload); err != nil {
			payload = nil
		}
	}
	deadline := parseDeadlineAt(payload)
	if !deadline.IsZero() && deadline.After(time.Now()) {
		derived, cancel := context.WithDeadline(base, deadline.Add(-reverseHeadroom))
		return derived, cancel
	}
	if base.Err() != nil {
		// Parent already cancelled (late __DeadlineAt absent): the deadline
		// fallback would resurrect a cancelled dispatch; let the parent win.
		derived, cancel := context.WithCancel(base)
		return derived, cancel
	}
	return context.WithTimeout(base, fallback)
}

func parseDeadlineAt(payload map[string]any) time.Time {
	var ms int64
	switch v := payload["__DeadlineAt"].(type) {
	case float64:
		ms = int64(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			ms = n
		}
	case int64:
		ms = v
	}
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// (LLM payload adaptation moved to pkg/appbinding — the single home of the
// llm.* host-call binding; see llm_stream.go's adaptLLMReq. The host bridge
// now looks the route up in the appbinding stream catalog and never imports
// the aggregator's domain types.)

// streamServiceRef returns the cached service ref for a host-bridge service
// domain, falling back to a runtime LookupService when the OnStart snapshot
// missed it (aiaggregator and sshmanager expose their domains only after
// they learn their config). One generic cache for every service the host
// bridge dispatches to — streaming routes resolve their target through it,
// so adding a route never adds a per-service accessor.
func (a *Actor) streamServiceRef(name string) ref.Ref {
	a.svcRefMu.Lock()
	defer a.svcRefMu.Unlock()
	if r, ok := a.svcRefs[name]; ok && r != nil {
		return r
	}
	if a.actorCtx != nil {
		if r, ok := a.actorCtx.LookupService(name); ok && r != nil {
			if a.svcRefs == nil {
				a.svcRefs = map[string]ref.Ref{}
			}
			a.svcRefs[name] = r
			return r
		}
	}
	return nil
}

// (sshmanagerRef folded into the generic streamServiceRef cache above; the
// sshmanager.* host-call branch resolves through streamServiceRef("sshmanager").)

// buildHostBridgeDispatch constructs the HostDispatchFunc used by the host
// bridge to route authorized reverse calls from plugins to backing actor
// services. It captures stable ref.Ref values for the system services at
// OnStart time — these refs are safe to Invoke from any goroutine for the
// lifetime of the actor tree.
//
// SDK callID prefixes that do not directly match a service name are aliased
// to the backing service (e.g. "llm" → aimanager). SDK callIDs whose actual
// callable name differs from the SDK name are rewritten via
// hostBridgeDispatchTranslations before the service is resolved. CallIDs
// whose service is not available fail with a structured error at dispatch time.
func (a *Actor) buildHostBridgeDispatch(ctx actor.Context) HostDispatchFunc {
	services := make(map[string]ref.Ref)
	for _, name := range hostBridgeDispatchServices {
		if r, ok := ctx.LookupService(name); ok && r != nil {
			services[name] = r
		}
	}
	// Alias SDK callID prefixes to actual service names.
	if r, ok := services["aimanager"]; ok {
		services["llm"] = r
		services["provider"] = r
		services["aggregator"] = r
	}
	if r, ok := services["filesystem"]; ok {
		services["project"] = r
	}

	// Seed the lazy service-ref cache from the OnStart snapshot. Services
	// that have not yet exposed their domain (aiaggregator learns its config
	// via aimanager after pluginhost starts; sshmanager mirrors this) are
	// re-resolved by a live LookupService on each host call through
	// streamServiceRef.
	a.svcRefMu.Lock()
	for name, r := range services {
		if r == nil {
			continue
		}
		if a.svcRefs == nil {
			a.svcRefs = map[string]ref.Ref{}
		}
		a.svcRefs[name] = r
	}
	a.svcRefMu.Unlock()

	serviceDispatch := NewTranslatedServiceDispatch(NewServiceDispatch(services), hostBridgeDispatchTranslations)

	return func(callID string, req []byte) ([]byte, error) {
		// Streaming-catalog callIDs (llm.complete / llm.chat today) consume
		// their backing stream even on the unary path: the route's
		// aggregator produces the terminal. The catalog is the single
		// source of streaming callIDs — no domain prefix checks here.
		// Unary reverses carry no dispatch context: the FFI transport and
		// the unary process path have no cancellation root to pass.
		if route, ok := appbinding.LookupStreamRoute(callID); ok {
			return a.handleHostBridgeStream(DispatchContext{}, a.streamServiceRef(route.Service), route, callID, req, nil)
		}
		switch callID {
		case "config.get":
			return a.handleHostBridgeConfigGet(req)
		case "config.set":
			return a.handleHostBridgeConfigSet(req)
		case "provider.get":
			return a.handleHostBridgeProviderGet(services["aimanager"], req)
		case "db.profile_list":
			return a.handleHostBridgeDbProfileList(services["dbmanager"])
		case "db.profile_dial":
			return a.handleHostBridgeDbProfileDial(services["dbmanager"], req)
		case "browser.cookies_export":
			return a.handleHostBridgeBrowserCookiesExport(req)
		case "browser_instances_list":
			return a.handleHostBridgeBrowserInstancesList(req)
		case "registry.query":
			return a.handleRegistryQuery(req)
		case "dialog.openFile":
			return a.handleHostBridgeDialogOpenFile(req)
		case "dialog.openFolder":
			return a.handleHostBridgeDialogOpenFolder(req)
		case "dialog.saveFile":
			return a.handleHostBridgeDialogSaveFile(req)
		case "clipboard.write":
			return a.handleHostBridgeClipboardWrite(req)
		case "clipboard.read":
			return a.handleHostBridgeClipboardRead(req)
		}
		// Services may expose their domain after pluginhost OnStart
		// (aiaggregator learns its config via aimanager; sshmanager and
		// media mirror this). Resolve the alias target first — the lazy
		// branch below and the snapshot dispatch must route the ACTUAL
		// callID (e.g. media.list_units -> aimanager.list_units), not the
		// SDK-facing one. Then resolve any service missing from the
		// OnStart snapshot live through the cached streamServiceRef before
		// falling back to the snapshot dispatch (which fails with a
		// structured error). ResolveHostCall is idempotent, so the
		// translated wrapper is unaffected by the pre-resolution here.
		routed := appbinding.ResolveHostCall(callID)
		if _, ok := services[serviceFromHostCallID(routed)]; !ok {
			if r := a.streamServiceRef(serviceFromHostCallID(routed)); r != nil {
				ctx, cancel := context.WithTimeout(context.Background(), hostBridgeInvokeTimeout)
				defer cancel()
				call := r.Invoke(ctx, routed, req)
				if call == nil {
					return nil, fmt.Errorf("pluginhost: invoke %q returned nil", routed)
				}
				defer call.Close()
				raw, err := call.RecvRaw()
				if err != nil {
					return nil, fmt.Errorf("pluginhost: host invoke %q: %w", routed, err)
				}
				return raw, nil
			}
		}
		return serviceDispatch(routed, req)
	}
}

// buildHostBridgeDispatchStream is the streaming sibling of the unary
// HostDispatchFunc above. A callID with a registered appbinding stream route
// walks the generic forwarder (adapt → invoke → encode chunks → aggregate →
// terminal); callIDs without a route have no stream source and degrade to
// the unary dispatch (no chunks).
func (a *Actor) buildHostBridgeDispatchStream(unary HostDispatchFunc) HostDispatchStreamFunc {
	return func(dctx DispatchContext, callID string, req []byte, onChunk func([]byte) error) ([]byte, error) {
		if route, ok := appbinding.LookupStreamRoute(callID); ok {
			return a.handleHostBridgeStream(dctx, a.streamServiceRef(route.Service), route, callID, req, onChunk)
		}
		// No streaming source for this callID — degrade to the unary route
		// (no chunks). dctx is unused here: the bridge already performed
		// caller-context injection for routes that want it.
		return unary(callID, req)
	}
}

// (from the SDK StateClient) to the pluginhost actor's own state handlers.
// It binds the handler methods directly rather than resolving the actor's
// own service via LookupService: self-lookup at OnStart time is not
// guaranteed (service exposure happens after the actor's start), and a nil
// dispatch here silently breaks app.state persistence for every plugin.
// The handlers ignore their context, so a nil context is safe.
func (a *Actor) buildStoreDispatch(_ actor.Context) HostDispatchFunc {
	return func(callID string, req []byte) ([]byte, error) {
		switch callID {
		case "image.generate", "video.generate":
			var r struct {
				Plugin string `json:"Plugin"`
			}
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, fmt.Errorf("pluginhost: %s decode: %w", callID, err)
			}
			return a.handleMediaGenerate(callID, r.Plugin, req)
		case "app.emit":
			var r struct {
				Plugin  string          `json:"Plugin"`
				Event   string          `json:"event"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, fmt.Errorf("pluginhost: app.emit decode: %w", err)
			}
			return a.handleHostBridgeAppEmit(r.Plugin, r.Event, r.Payload)
		default:
			if appbinding.HostCallCapability(callID) == appbinding.CapWikiRead {
				// project.wiki_* host calls have no backing system service:
				// the project domain is ExposeChildren (invisible to the
				// pluginhost), so they forward through the workspace actor to
				// the plugin's bound project. The "project" prefix alias in
				// buildHostBridgeDispatch points at the filesystem service
				// (fs.read's project.read_file) — routing wiki calls there
				// answers "call ID not registered".
				var r struct {
					Plugin string `json:"Plugin"`
				}
				if err := json.Unmarshal(req, &r); err != nil {
					return nil, fmt.Errorf("pluginhost: %s decode: %w", callID, err)
				}
				return a.handleHostBridgeWikiForward(callID, r.Plugin, req)
			}
		case "state.get":
			var r gen.PluginStateGetReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, fmt.Errorf("pluginhost: state.get decode: %w", err)
			}
			resp, err := a.handleStateGet(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		case "state.set":
			var r gen.PluginStateSetReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, fmt.Errorf("pluginhost: state.set decode: %w", err)
			}
			resp, err := a.handleStateSet(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		case "state.delete":
			var r gen.PluginStateDeleteReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, fmt.Errorf("pluginhost: state.delete decode: %w", err)
			}
			resp, err := a.handleStateDelete(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		case "state.list":
			var r gen.PluginStateListReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, fmt.Errorf("pluginhost: state.list decode: %w", err)
			}
			resp, err := a.handleStateList(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		case "state.append":
			var r gen.PluginStateAppendReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, fmt.Errorf("pluginhost: state.append decode: %w", err)
			}
			resp, err := a.handleStateAppend(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		case "state.get_many":
			var r gen.PluginStateGetManyReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, fmt.Errorf("pluginhost: state.get_many decode: %w", err)
			}
			resp, err := a.handleStateGetMany(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		case "state.set_many":
			var r gen.PluginStateSetManyReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, fmt.Errorf("pluginhost: state.set_many decode: %w", err)
			}
			resp, err := a.handleStateSetMany(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		}
		return nil, fmt.Errorf("pluginhost: unknown state callID %q", callID)
	}
}

// handleHostBridgeAppEmit forwards the SDK `app.emit` host call to
// appmanager.plugin_emit. The bridge already injected the calling plugin's
// identity into pluginID; the appmanager validates the event against the app
// manifest and broadcasts on the app_event bus kind. Runs on the reverse-call
// goroutine with the standard host-bridge timeout, like provider.get.
func (a *Actor) handleHostBridgeAppEmit(pluginID, event string, payload json.RawMessage) ([]byte, error) {
	if strings.TrimSpace(pluginID) == "" {
		return nil, fmt.Errorf("pluginhost: app.emit: plugin identity missing")
	}
	if strings.TrimSpace(event) == "" {
		return nil, fmt.Errorf("pluginhost: app.emit: event is required")
	}
	r := a.streamServiceRef("appmanager")
	if r == nil {
		return nil, fmt.Errorf("pluginhost: appmanager service not available for app.emit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostBridgeInvokeTimeout)
	defer cancel()
	call := r.Invoke(ctx, "appmanager.plugin_emit", gen.AppManagerPluginEmitReq{
		PluginID: pluginID,
		Event:    event,
		Payload:  []byte(payload),
	})
	if call == nil {
		return nil, fmt.Errorf("pluginhost: app.emit invoke returned nil")
	}
	defer call.Close()
	raw, err := call.RecvRaw()
	if err != nil {
		return nil, fmt.Errorf("pluginhost: app.emit: %w", err)
	}
	return raw, nil
}

// hostBridgeDispatchServices lists system services the host bridge may route
// authorized reverse calls to. The callID prefix selects the service. Services
// absent at OnStart (headless/test environments) are skipped by LookupService
// and fail with a structured "service not available" error at dispatch time.
var hostBridgeDispatchServices = []string{
	"aimanager",
	"aiaggregator",
	"filesystem",
	"shell",
	"sshmanager",
	"dbmanager",
	"workspace",
	"websearch",
	"crawl",
	"aistats",
	"oracle",
	"unified_graph",
	"media",
	"voice",
}

// LogPluginEntry implements pluginLogSink, forwarding a single plugin log
// entry to the host's structured logger AND into the per-plugin capture ring
// (the surface behind pluginhost.plugin_logs). Log-only surface: process
// state transitions are reported separately via ReportProcessState.
func (a *Actor) LogPluginEntry(pluginID string, generation int, level int, msg string) {
	if a.pluginLogFn != nil {
		a.pluginLogFn(pluginID, generation, level, msg)
	}
	a.appendPluginLog(pluginID, pluginLogLevelName(level), logSourceBackend, msg)
}

// ReportProcessState implements the pluginLogSink lifecycle seam: the
// transport transition points (post-OnLoad spawn, abnormal exit, unload
// close, including invoke-timeout kills) report the process verdict
// explicitly, replacing the old log-message string sniffing. httpAddr is the
// plugin's own HTTP listener when the plugin is "running" and bound one;
// gen is the reporting process's generation (stale generations are dropped
// in setProcessState).
func (a *Actor) ReportProcessState(pluginID, state, crash, httpAddr string, gen int64) {
	a.setProcessState(pluginID, state, crash, httpAddr, gen)
	// Gateway bridge lifecycle: a backend that is gone (crash, unload close,
	// invoke-timeout kill) must stop serving through its loopback proxy —
	// the route falls back to the plugin's static assets bundle or 404.
	// Reload re-attaches when appmanager commits the new backend
	// (pluginhost.proxy_attach). "running" deliberately does not attach:
	// only appmanager holds the gateway token.
	switch state {
	case "stopped", "crashed":
		a.detachProxy(pluginID)
	}
}

// newPluginLogFn creates a structured log adapter that routes plugin log
// entries through the host actor's logger. Each entry is emitted with
// pluginID, generation, and msg as structured key-value pairs so it is
// visible in the agent turn output stream.
func newPluginLogFn(logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}) func(pluginID string, generation int, level int, msg string) {
	return func(pluginID string, generation int, level int, msg string) {
		args := []any{"plugin", pluginID, "generation", generation, "msg", msg}
		switch level {
		case 0: // LogLevelDebug
			logger.Debug("plugin.log", args...)
		case 1: // LogLevelInfo
			logger.Info("plugin.log", args...)
		case 2: // LogLevelWarn
			logger.Warn("plugin.log", args...)
		default: // LogLevelError or unknown
			logger.Error("plugin.log", args...)
		}
	}
}
