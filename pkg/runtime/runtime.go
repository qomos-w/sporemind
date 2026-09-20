package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/gospore/gateway"
	gosporeID "github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/resource"
	"github.com/qomos-w/gospore/schema"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/gen"
	"github.com/qomos-w/sporemind/pkg/actor/glassinteract"
	"github.com/qomos-w/sporemind/pkg/auth"
	"github.com/qomos-w/sporemind/pkg/buildinfo"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/debug"
	"github.com/qomos-w/sporemind/pkg/domain"
	sporeGateway "github.com/qomos-w/sporemind/pkg/gateway"
	"github.com/qomos-w/sporemind/pkg/gatewayauth"
	"github.com/qomos-w/sporemind/pkg/logging"
	"github.com/qomos-w/sporemind/pkg/mobileassets"
	"github.com/qomos-w/sporemind/pkg/peerserver"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/protocol"
	"github.com/qomos-w/sporemind/pkg/runtime/topo"
	"github.com/qomos-w/sporemind/pkg/version"
)

// Config holds runtime assembly parameters.
type Config struct {
	Namespace string
	// GatewayAddr controls the HTTP gateway listen address.
	// Empty string falls back to the default (loopback-only 127.0.0.1:18080).
	GatewayAddr string
	// GatewayBindAddrs adds extra listen addresses beyond GatewayAddr. Each
	// gets its own listener sharing the same handler, enabling dual binding
	// (loopback + LAN IP) without exposing 0.0.0.0.
	GatewayBindAddrs []string
	// NoGateway disables the HTTP gateway entirely (e.g. for offline
	// manifest export). When true, GatewayAddr is ignored.
	NoGateway bool
	// WebStatic enables SPA/static hosting on the same HTTP gateway.
	WebStatic fs.FS
	// DevProxy, when non-empty, is the URL of a frontend dev server (e.g.
	// Vite on http://localhost:5558). The gateway proxies all non-API
	// requests there instead of serving WebStatic. Takes precedence over
	// WebStatic.
	DevProxy string
	// LogRing is the structured log ring buffer. When non-nil, it is
	// wired as the gateway /logs LogProvider and a LogHook is registered
	// so gospore forwards every log entry (with correct caller) into it.
	// Use *logging.Ring for an in-memory buffer or *logging.FileRing for
	// disk-backed persistence.
	LogRing logging.LogRing
	// ConsoleLogSource is the frontend console log store. When non-nil, it is
	// exposed as a gospore resource so actors (e.g. workspace.logs.query with
	// Source="console") can read persisted frontend console logs.
	ConsoleLogSource logging.ConsoleLogSource
	// LogStreamer is the batched live-log streamer. When non-nil, it is
	// exposed as a resource so the workspace actor can subscribe its handler
	// and emit the workspace.log event kind. The ring and console store must
	// already be wired to push entries into the streamer.
	LogStreamer *logging.LogStreamer
	// LogStore is the persisted backend log store (daily JSONL files). When
	// non-nil, it is exposed as a gospore resource so workspace.logs_query
	// can page beyond the in-memory ring window — freeze forensics must
	// survive restarts even when the ring's 2000-entry tail was flooded by
	// log storms.
	LogStore logging.BackendLogSource
	// Children declares the top-level actors to spawn under root.
	// Empty/nil means no business actors are spawned; the framework
	// itself still starts. Callers wire their domain actors here.
	Children []ChildSpec
	// NodeID is the cluster node identifier used to derive the root
	// actor's stable ID. Default is 1.
	NodeID uint16
	// PeerAuth authenticates incoming peer HTTP requests. When nil,
	// all peer requests are accepted (development only).
	PeerAuth peerserver.PeerAuthenticator
}

// ChildSpec describes one top-level actor to spawn under the runtime root.
type ChildSpec struct {
	Name              string
	Factory           func() actor.Actor
	RequirePersistent bool   // if true, the actor must implement persist.Persistent
	Role              string // default caller role propagated on outbound calls
	// Planner grants the actor the Planner capability (ctx.Planner()).
	// Needed when the actor must invoke other services' callables (e.g.
	// glassinteract calling voice.recognize).
	Planner bool
	// DependsOn lists the names of other children that must complete both
	// OnInit and OnStart before this child starts. The runtime resolves
	// these into a topological order; cycles and missing names are
	// reported as errors during OnInit.
	DependsOn []string
}

// New assembles a gospore App from the given Config. The actor set is
// taken from cfg.Children; pkg/runtime itself ships no default actors.
func New(cfg Config) (app.App, error) {
	if cfg.Namespace == "" {
		cfg.Namespace = "sporemind"
	}
	if cfg.NodeID == 0 {
		cfg.NodeID = 1
	}

	// One-time migration of the legacy filesystem persist layout
	// (<name>/state.json → <name>.json). Idempotent: a no-op on fresh
	// installs and on trees that were already migrated. Must run before any
	// actor loads its state.
	if err := persist.MigrateFSLegacyLayout(config.ActorDataDir()); err != nil {
		return nil, fmt.Errorf("runtime: persist layout migration: %w", err)
	}

	// Derive a stable root actor ID from the node ID.
	rootCID, err := identity.NewCanonicalID(0, cfg.NodeID, 0, 1)
	if err != nil {
		return nil, fmt.Errorf("runtime: root ID: %w", err)
	}
	rootAID := gosporeID.From(rootCID)

	// Load the service→actorID registry from disk.
	regPath := filepath.Join(config.DataDir(), "registry.json")
	reg, err := LoadRegistry(regPath)
	if err != nil {
		return nil, fmt.Errorf("runtime: registry: %w", err)
	}
	reg.NodeID = fmt.Sprintf("%d", cfg.NodeID)

	var opts []app.Option
	opts = append(opts, app.WithNamespace(cfg.Namespace))
	opts = append(opts, app.WithStrictHandlers(true))
	opts = append(opts, app.WithBinaryOnly(true))

	// Load the static schema fragment and build the protocol manager. The
	// fragment is the single source of truth for internal schema IDs; it is
	// converted into a gospore manifest import so handler registration reuses
	// the declared IDs instead of auto-allocating.
	var fragment protocol.StaticFragment
	if err := json.Unmarshal(gen.StaticSchemaFragmentJSON(), &fragment); err != nil {
		return nil, fmt.Errorf("runtime: parse static schema fragment: %w", err)
	}
	protoMgr, err := protocol.NewManager(fragment, protocol.WithLocalVersion(version.Version))
	if err != nil {
		return nil, fmt.Errorf("runtime: protocol manager: %w", err)
	}
	manifestImportJSON, err := protoMgr.ToManifestImportJSON()
	if err != nil {
		return nil, fmt.Errorf("runtime: manifest import json: %w", err)
	}
	opts = append(opts, app.WithManifestImport(manifestImportJSON))
	opts = append(opts, app.WithResource[*protocol.Manager](protocol.ManagerKey, protoMgr))

	opts = append(opts, app.WithRootID(rootAID))

	// TopologyProvider shared via resource registry; App set after construction.
	topo := newTopologyProvider(nil)
	opts = append(opts, app.WithResource[TopologyProvider](TopologyKey, topo))

	var pluginRouter *pluginhost.Router
	if !cfg.NoGateway {
		pluginRouter = pluginhost.NewRouter()
		opts = append(opts, app.WithResource[*pluginhost.Router](pluginhost.RouterKey, pluginRouter))
	}

	var appForDebug app.App

	opts = append(opts, app.WithRootActor(func() actor.Actor {
		return &rootActor{
			children: cfg.Children,
			topo:     topo,
			registry: reg,
			onInitComplete: func(startOrder []string, nameToID map[string]string) {
				appForDebug.SetChildStartOrder(startOrder, nameToID)
			},
		}
	}))
	var peerServer *peerserver.Server
	var policyInterceptor *sporeGateway.PolicyReloadInterceptor
	if !cfg.NoGateway {
		addr := cfg.GatewayAddr
		if addr == "" {
			addr = config.DefaultGatewayAddr
		}
		// Unary invokes may legitimately run long (e.g. complete_message's
		// 2-minute handler budget for LLM failover retries). The default
		// 60s cap silently truncated such calls; the frontend session
		// already waits 120s, so align the server cap with it.
		gateway.GatewayInvokeTimeout = 120 * time.Second

		// The policy interceptor reloads the PolicyStore after a permission
		// matrix update via the gateway. It is created before the App so it
		// can be registered as an option; SetHost wires the App reference once
		// construction completes.
		policyInterceptor = sporeGateway.NewPolicyReloadInterceptor(nil)
		opts = append(opts, app.WithGatewayHTTP(addr, policyInterceptor))
		if len(cfg.GatewayBindAddrs) > 0 {
			opts = append(opts, app.WithGatewayExtraAddrs(cfg.GatewayBindAddrs))
		}
		opts = append(opts, app.WithURLAuth(gatewayauth.New(auth.NewManager(auth.DefaultJWTConfig()))))

		// Build all extra routes into one closure so they do not overwrite each
		// other (WithExtraRoutes stores a single callback on the gateway).
		var routeFuncs []func(*http.ServeMux)
		if cfg.DevProxy != "" {
			proxyTarget, err := url.Parse(cfg.DevProxy)
			if err != nil {
				return nil, fmt.Errorf("runtime: invalid DevProxy URL %q: %w", cfg.DevProxy, err)
			}
			proxy := httputil.NewSingleHostReverseProxy(proxyTarget)
			origDirector := proxy.Director
			proxy.Director = func(req *http.Request) {
				origDirector(req)
				req.Host = proxyTarget.Host
			}
			proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
				slog.Warn("dev-proxy: request failed", "method", r.Method, "path", r.URL.Path, "error", err)
				http.Error(w, "dev proxy: "+err.Error(), http.StatusBadGateway)
			}
			routeFuncs = append(routeFuncs, func(mux *http.ServeMux) {
				mux.Handle("/", proxy)
			})
		} else if cfg.WebStatic != nil {
			opts = append(opts, app.WithStaticFS(cfg.WebStatic))
		}
		routeFuncs = append(routeFuncs, func(mux *http.ServeMux) {
			debug.RegisterRoutes(mux, debug.Deps{
				App: appForDebug,
				Topology: func() any {
					tp, ok := resource.Get[TopologyProvider](appForDebug.Resources(), TopologyKey)
					if !ok {
						return nil
					}
					return tp.Snapshot()
				},
			})
		})
		routeFuncs = append(routeFuncs, func(mux *http.ServeMux) {
			if peerServer == nil {
				return
			}
			if err := collectCrossAppServices(appForDebug, protoMgr); err != nil {
				slog.Warn("runtime: failed to collect cross-app services", "error", err)
			}
			peerServer.RegisterRoutes(mux)
		})
		routeFuncs = append(routeFuncs, func(mux *http.ServeMux) {
			// Glass bootstrap: dedicated route that shadows the generic
			// /api/ proxy so only glass_interact.bootstrap is invocable here.
			mux.Handle(glassinteract.BootstrapRoute, glassinteract.BootstrapHandler(appForDebug))
		})
		routeFuncs = append(routeFuncs, func(mux *http.ServeMux) {
			if pluginRouter != nil {
				mux.Handle("/plugin/", pluginRouter)
			}
		})
		routeFuncs = append(routeFuncs, func(mux *http.ServeMux) {
			mux.HandleFunc("/download/mobile.apk", mobileassets.ServeDownload)
		})
		opts = append(opts, app.WithExtraRoutes(func(mux *http.ServeMux) {
			for _, fn := range routeFuncs {
				fn(mux)
			}
		}))
	}
	if cfg.LogRing != nil {
		opts = append(opts, app.WithResource[logging.LogRing](logging.LogRingKey, cfg.LogRing))
		opts = append(opts, app.WithLogProvider(cfg.LogRing))
		opts = append(opts, app.WithLogHook(logging.NewHook(cfg.LogRing, logging.DefaultMinLevel)))
	}
	if cfg.ConsoleLogSource != nil {
		opts = append(opts, app.WithResource[logging.ConsoleLogSource](logging.ConsoleLogSourceKey, cfg.ConsoleLogSource))
	}
	if cfg.LogStreamer != nil {
		opts = append(opts, app.WithResource[*logging.LogStreamer](logging.LogStreamerKey, cfg.LogStreamer))
	}
	if cfg.LogStore != nil {
		opts = append(opts, app.WithResource[logging.BackendLogSource](logging.BackendLogSourceKey, cfg.LogStore))
	}

	a, err := app.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	if policyInterceptor != nil {
		policyInterceptor.SetHost(a)
	}
	topo.app = a
	appForDebug = a
	if !cfg.NoGateway {
		peerServer = peerserver.New(a, protoMgr, cfg.PeerAuth, fmt.Sprintf("sporemind-%d", cfg.NodeID), version.Version)
	}
	a.Tree().OnChange(topo.markDirty)
	return a, nil
}

// Handle represents an App started by Bootstrap. The app runs in its
// own goroutine so the caller can keep the main goroutine for another
// event loop (e.g. Wails).
type Handle struct {
	app  app.App
	done chan error
}

// App returns the underlying gospore App. Bridges (e.g. Wails) use this
// to dispatch callables without re-spawning the actor tree.
func (h *Handle) App() app.App { return h.app }

// GatewayReady returns a channel that closes when the HTTP gateway is
// accepting connections. Returns nil if no gateway is configured.
func (h *Handle) GatewayReady() <-chan struct{} {
	return h.app.GatewayReady()
}

// Wait blocks until app.Run returns and reports the meaningful error.
// Call exactly once per Handle.
//
// A clean context cancellation is collapsed to nil.
func (h *Handle) Wait() error {
	runErr := <-h.done
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	return nil
}

// Bootstrap assembles the App via New and starts app.Run in a goroutine.
// Use this when the main goroutine must be handed to another event loop
// (e.g. cmd/sporemind-desktop hands main to Wails).
func Bootstrap(ctx context.Context, cfg Config) (*Handle, error) {
	a, err := New(cfg)
	if err != nil {
		return nil, err
	}
	h := &Handle{
		app:  a,
		done: make(chan error, 1),
	}
	go func() {
		h.done <- a.Run(ctx)
	}()

	// Load the user permission matrix into the PolicyStore once all cells are
	// started. Failures are logged and do not block startup.
	StartPolicyBridge(ctx, h, 30*time.Second)

	return h, nil
}

// Run assembles the App and runs it on the calling goroutine, then
// persists the actor registry. Suitable for headless binaries that do
// not need to share the main goroutine. Equivalent to Bootstrap + Wait.
func Run(ctx context.Context, cfg Config) error {
	h, err := Bootstrap(ctx, cfg)
	if err != nil {
		return err
	}
	return h.Wait()
}

// collectCrossAppServices walks the registered gospore services, finds
// callables marked with actor.CrossApp(), and exposes each service to the
// protocol manager. Only services whose callIDs start with the service name
// are exposed; all referenced schemas are expected to live in the system
// protocol namespace.
func collectCrossAppServices(a app.App, protoMgr *protocol.Manager) error {
	schemas := a.Schemas()
	for _, svcName := range a.Service().Names() {
		svcRef, ok := a.Service().Lookup(svcName)
		if !ok {
			continue
		}
		ht, ok := a.HandlerTableFor(svcRef)
		if !ok {
			continue
		}
		es := protocol.ExportedService{
			ServiceName:       svcName,
			ProtocolNamespace: protocol.SystemNamespace,
			Schemas:           make(map[string]uint64),
		}
		hasAny := false
		prefix := svcName + "."
		for _, callable := range ht.CrossAppCallables() {
			if !strings.HasPrefix(callable.CallID, prefix) {
				continue
			}
			hasAny = true
			for _, id := range callable.ParamSchemaIDs {
				addSchemaForID(es.Schemas, schemas, id)
			}
			for _, id := range callable.ReturnSchemaIDs {
				addSchemaForID(es.Schemas, schemas, id)
			}
			addSchemaForID(es.Schemas, schemas, callable.ChunkSchemaID)
		}
		if !hasAny {
			continue
		}
		if err := protoMgr.ExposeService(es); err != nil {
			return fmt.Errorf("expose service %q: %w", svcName, err)
		}
	}
	return nil
}

func addSchemaForID(m map[string]uint64, schemas schema.Reader, id uint64) {
	if id == 0 {
		return
	}
	e, ok := schemas.Lookup(id)
	if !ok {
		return
	}
	if e.Name == "" {
		return
	}
	m[e.Name] = e.ID
}

// rootActor is the App root. It spawns the top-level actors declared
// in Config.Children and hosts the system-level observation services
// (topology epochs, unified graph, facts, inspector).
type rootActor struct {
	actor.Host
	children       []ChildSpec
	topo           *topologyProvider
	registry       *Registry
	actorID        string
	startOrder     []string            // topologically sorted child names, set by OnInit
	nameToID       map[string]string   // child name → actor ID string, populated by OnInit
	onInitComplete func(startOrder []string, nameToID map[string]string)
}

func (r *rootActor) Type() string { return "runtime" }

func (r *rootActor) OnStart(ctx actor.Context) error {
	r.actorID = ctx.Self().ID().String()

	if err := r.registerCallables(ctx); err != nil {
		return err
	}

	// Register topology epoch change callback.
	r.topo.OnEpochChange(func(epoch int32, patch *domain.GraphPatch) {
		ctx.Logger().Debug("runtime: firing topology-epoch event", "epoch", epoch, "addedNodes", len(patch.AddedNodes), "removedNodes", len(patch.RemovedNodes))
		_ = ctx.EmitEvent("topology-epoch", domain.TopologyEpochEvent{
			Epoch: epoch,
			Patch: patch,
		})
	})

	// Enable push-model epoch advancement. Triggers the initial build (single
	// epoch with ALL actors) and ensures the OnEpochChange callback fires.
	r.topo.EnablePush()

	return nil
}

// registerCallables declares the system-level observation services
// (topology epochs, unified graph, facts, inspector) and their lanes. It is
// split out of OnStart so the registration surface — handler modes and lane
// routing — is unit-testable with a fake context, without triggering the
// topology provider's push wiring.
func (r *rootActor) registerCallables(ctx actor.Context) error {
	// Register event kinds.
	if err := ctx.RegisterEventKind("topology-epoch", domain.TopologyEpochEvent{}, actor.Public()); err != nil {
		ctx.Logger().Warn("runtime: register topology-epoch event failed", "error", err)
	}

	// unified_graph.sync fires once per topology epoch (driven by the frontend
	// observationStore) and takes the heavy path: it rebuilds the full graph
	// and ships the patch/snapshot deltas, so it must not run on the stateful
	// owner lane (spec #1: the owner lane only does state changes and message
	// sends). Sync mutates the topology provider's epoch history, so it stays
	// stateful, but on a dedicated "graph_sync" lane so a rebuild never parks
	// the owner lane behind it.
	if err := ctx.RegisterLoop("graph_sync", actor.ModeStateful); err != nil {
		return fmt.Errorf("runtime: register graph_sync loop: %w", err)
	}
	if err := ctx.Register("unified_graph.sync", r.handleUnifiedGraphSync, actor.Public(), actor.WithLoop("graph_sync")); err != nil {
		return fmt.Errorf("runtime: register unified_graph.sync: %w", err)
	}

	// The history / inspector / service-listing readers only read the topology
	// snapshot and app projections, so they are registered stateless
	// (PureContext) and never touch the owner lane.
	if err := ctx.Register("unified_graph.history", r.handleUnifiedGraphHistory, actor.Public()); err != nil {
		return fmt.Errorf("runtime: register unified_graph.history: %w", err)
	}
	_ = ctx.RegisterDomain("unified_graph").Expose()

	if err := ctx.Register("inspect.document", r.handleInspectDocument, actor.Public()); err != nil {
		return fmt.Errorf("runtime: register inspect.document: %w", err)
	}
	_ = ctx.RegisterDomain("inspect").Expose()

	if err := ctx.Register("runtime.list_services", r.handleListServices, actor.Public()); err != nil {
		return fmt.Errorf("runtime: register runtime.list_services: %w", err)
	}

	// Build identity read (pure): lets agents and the frontend debug surface
	// answer "which build is actually running" in one call — the stale-process
	// vs stale-frontend drift that otherwise costs a long debug session.
	if err := ctx.Register("runtime.build_info", r.handleBuildInfo, actor.Public()); err != nil {
		return fmt.Errorf("runtime: register runtime.build_info: %w", err)
	}

	_ = ctx.RegisterDomain("observation").Expose()
	return nil
}

func (r *rootActor) OnStop(ctx actor.Context) error {
	_ = r.topo.Close()
	return nil
}

// ---------------------------------------------------------------------------
// Callable handlers
// ---------------------------------------------------------------------------

func (r *rootActor) handleUnifiedGraphSync(ctx actor.Context, req domain.TopologySyncReq) (domain.TopologySyncResp, error) {
	resp := r.topo.Sync(req.ClientEpoch)
	if resp.Snapshot != nil || len(resp.Patches) > 0 {
		ctx.Logger().Info("runtime: handleUnifiedGraphSync",
			"clientEpoch", req.ClientEpoch,
			"currentEpoch", resp.CurrentEpoch,
			"snapshotNil", resp.Snapshot == nil,
			"patchCount", len(resp.Patches),
		)
	}
	return resp, nil
}

func (r *rootActor) handleUnifiedGraphHistory(ctx actor.PureContext) (domain.TopologyHistoryResp, error) {
	return domain.TopologyHistoryResp{Entries: r.topo.HistoryEntries()}, nil
}

func (r *rootActor) handleInspectDocument(ctx actor.PureContext, req domain.InspectDocumentReq) (domain.InspectDocument, error) {
	ref := domain.InspectRef{
		Kind:  req.Kind,
		ID:    req.ID,
		Scope: req.Scope,
	}

	switch req.Kind {
	case "actor_node":
		return inspectActorNode(ctx, r.topo, ref), nil
	default:
		return domain.InspectDocument{
			Ref:      ref,
			Title:    fmt.Sprintf("%s (%s)", req.ID, req.Kind),
			Summary:  fmt.Sprintf("Inspector not yet implemented for kind %q", req.Kind),
			Sections: []domain.InspectSection{},
		}, nil
	}
}

func (r *rootActor) handleListServices(ctx actor.PureContext, _ domain.RuntimeListServicesReq) (domain.RuntimeListServicesResp, error) {
	if r.topo.app == nil {
		return domain.RuntimeListServicesResp{}, fmt.Errorf("runtime.list_services: app not available")
	}
	svc := r.topo.app.Service()
	names := svc.Names()
	items := make([]domain.ServiceInfo, 0, len(names))
	for _, name := range names {
		ref, ok := svc.Lookup(name)
		if !ok {
			continue
		}
		actorType := r.topo.app.ActorType(ref.ID())
		items = append(items, domain.ServiceInfo{
			Name:      name,
			ActorID:   ref.ID().String(),
			ActorType: actorType,
		})
	}
	return domain.RuntimeListServicesResp{Items: items}, nil
}

func (r *rootActor) handleBuildInfo(_ actor.PureContext, _ domain.RuntimeBuildInfoReq) (domain.RuntimeBuildInfoResp, error) {
	info := buildinfo.Get()
	return domain.RuntimeBuildInfoResp{
		Version:       info.Version,
		BuildType:     info.BuildType,
		BuildFlavor:   info.BuildFlavor,
		Commit:        info.Commit,
		BuildTime:     info.BuildTime,
		Dirty:         info.Dirty,
		PublicVersion: info.PublicVersion,
		Channel:       info.Channel,
	}, nil
}

func (r *rootActor) OnInit(ctx actor.Context) error {
	// Topologically sort children by DependsOn so that every child's
	// dependencies are spawned (and later started) first. When no
	// DependsOn is set the sort is a stable no-op and preserves input order.
	nodes := make([]string, 0, len(r.children))
	for _, spec := range r.children {
		nodes = append(nodes, spec.Name)
	}
	deps := make(map[string][]string, len(r.children))
	for _, spec := range r.children {
		if len(spec.DependsOn) > 0 {
			deps[spec.Name] = spec.DependsOn
		}
	}
	sorted, err := topo.Sort(nodes, deps)
	if err != nil {
		return fmt.Errorf("runtime: dependency sort: %w", err)
	}
	r.startOrder = sorted
	r.nameToID = make(map[string]string, len(sorted))

	// Build a name→spec index for sorted traversal.
	specByName := make(map[string]*ChildSpec, len(r.children))
	for i := range r.children {
		specByName[r.children[i].Name] = &r.children[i]
	}

	for _, name := range sorted {
		spec := specByName[name]
		if spec.RequirePersistent {
			instance := spec.Factory()
			if _, ok := instance.(persist.Persistent); !ok {
				return fmt.Errorf("runtime: actor %q requires persist.Persistent but %T does not implement it", spec.Name, instance)
			}
		}
		props := actor.PropsFromFunc(spec.Factory)
		if spec.Role != "" {
			props = props.WithRole(spec.Role)
		}
		if spec.Planner {
			props = props.WithPlanner()
		}

		// Restore stable actor ID from registry. Service-actor state is
		// persisted keyed on a.actorID (e.g. workspace.Save), so a drifted
		// ID orphans ALL of the service's state. We refuse to spawn rather
		// than auto-genning — silent fallback would mask the corruption
		// until the user notices their data is gone. Recovery: clear the
		// offending entry in the registry file (<datadir>/registry.json) or
		// delete the file entirely (accepts state loss for affected services).
		if savedID := r.registry.Lookup(spec.Name); savedID != "" {
			parsed, err := gosporeID.Parse(savedID)
			if err != nil {
				return fmt.Errorf("runtime: spawn %s: stored actor ID %q is malformed; refusing to auto-generate (would orphan persisted state). Recovery: remove the entry from registry.json and restart",
					spec.Name, savedID)
			}
			props = props.WithID(parsed)
		}

		ref, err := ctx.Spawn(props, spec.Name)
		if err != nil {
			return fmt.Errorf("runtime: spawn %s: %w", spec.Name, err)
		}

		// Sanity check: ctx.Spawn should honor WithID. If it didn't, the
		// service's state will drift — surface as an error, not silent overwrite.
		if savedID := r.registry.Lookup(spec.Name); savedID != "" && ref.ID().String() != savedID {
			return fmt.Errorf("runtime: spawn %s: ctx.Spawn returned ID %q but registry expected %q; persistence would drift",
				spec.Name, ref.ID().String(), savedID)
		}

		// Record the actor ID in the registry and persist immediately
		// (only for fresh spawns — recovery path keeps the same ID).
		if err := r.registry.Record(spec.Name, ref.ID().String()); err != nil {
			ctx.Logger().Warn("runtime: failed to save registry entry", "service", spec.Name, "error", err)
		}

		// Record name → actorID mapping for Phase 2 ordered start.
		r.nameToID[spec.Name] = ref.ID().String()
	}

	// Notify the runtime that OnInit is complete with the computed start
	// order and name-to-ID mapping for Phase 2 ordered Start.
	if r.onInitComplete != nil {
		r.onInitComplete(sorted, r.nameToID)
	}

	return nil
}
