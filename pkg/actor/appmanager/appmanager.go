package appmanager

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/resource"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/codegen"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

type appRecord struct {
	Manifest    gen.AppManifest   `json:"manifest"`
	ActorID     string            `json:"actorId"`
	EntryModule string            `json:"entryModule"`
	Modules     map[string]string `json:"modules"`
	Assets      map[string][]byte `json:"assets,omitempty"`
	// AssetRefs is the persisted form of Assets: name → content hash into
	// the content-addressed side store under <persistBase>/<actorID>/assets/
	// (see asset_store.go). Assets itself is stripped from the state document
	// on save and rehydrated from these refs at restore, so the document
	// stays small no matter how large the app bundles are. Empty on
	// backends that cannot host the side store (inline fallback).
	AssetRefs          map[string]string                  `json:"assetRefs,omitempty"`
	SchemaDescriptors  map[string]gen.AppObjectDescriptor `json:"schemaDescriptors,omitempty"`
	PackageHash        string                             `json:"packageHash,omitempty"`
	PackageHashVersion string                             `json:"packageHashVersion,omitempty"`
	PackagePath        string                             `json:"packagePath,omitempty"`
	ArtifactPath       string                             `json:"artifactPath,omitempty"`
	ArtifactHash       string                             `json:"artifactHash,omitempty"`
	Abi                *gen.PluginAbi                     `json:"abi,omitempty"`
	State              string                             `json:"state"`
	Error              string                             `json:"error,omitempty"`
	Origin             string                             `json:"origin,omitempty"`
	// Generation is the app session-generation counter. It starts at 1 on
	// register, increments on every reload, and is captured by session tokens
	// at issue time so that a reload invalidates all previously issued tokens.
	Generation int64 `json:"generation,omitempty"`
	// BackendPort / BackendUrl locate the plugin process's own HTTP listener
	// (subprocess SDK HTTP server); SessionSecret is the per-instance HMAC
	// secret pushed to that process in the OnLoad config, minting the
	// frontend's spore_session cookie. All empty/zero when no listener runs.
	BackendPort   int    `json:"backendPort,omitempty"`
	BackendUrl    string `json:"backendUrl,omitempty"`
	SessionSecret string `json:"sessionSecret,omitempty"`
	// ProjectID binds a project-registered app to its source project (mount
	// name or actor id). It rides the OnLoad config (backendLoadConfig.
	// ProjectID) so the pluginhost can route the plugin's project.wiki_*
	// host calls to the bound project actor. Empty for zip installs (no
	// project) — wiki host calls fail closed there.
	ProjectID string `json:"proj,omitempty"`
}

// pendingReload records an in-flight native reload whose candidate manifest
// has been persisted as the active record but whose pluginhost commit has
// not yet completed. If the process crashes in this window, OnStart recovery
// uses this marker to either complete the reload (re-prepare + commit the
// candidate) or roll back to the old record/manifest — restoring consistency
// between the persisted appmanager state and the pluginhost's loaded artifact.
type pendingReload struct {
	CandidateManifest     gen.AppManifest `json:"candidateManifest"`
	OldManifest           gen.AppManifest `json:"oldManifest"`
	OldRecord             appRecord       `json:"oldRecord"`
	CandidateArtifactPath string          `json:"candidateArtifactPath,omitempty"`
	CandidateArtifactHash string          `json:"candidateArtifactHash,omitempty"`
	CandidateAbi          *gen.PluginAbi  `json:"candidateAbi,omitempty"`
	// CandidateAssets is the asset bundle staged with the candidate
	// artifact. Nil means the reload leaves the installed bundle unchanged.
	// CandidateAssetRefs is its persisted side-store form (asset_store.go).
	CandidateAssets    map[string][]byte `json:"candidateAssets,omitempty"`
	CandidateAssetRefs map[string]string `json:"candidateAssetRefs,omitempty"`
	PackageHash        string            `json:"packageHash,omitempty"`
}

// appSession is the in-memory record of an issued session. It is also the
// claim set embedded (JSON) inside a signed session token, hence the json
// tags. Sessions are intentionally ephemeral: the map is never persisted and
// the signing key is regenerated on every appmanager start, so a process
// restart invalidates all outstanding tokens.
type appSession struct {
	ID         string `json:"sid"`
	AppID      string `json:"app"`
	ViewID     string `json:"view"`
	AgentID    string `json:"agent,omitempty"`
	ProjectID  string `json:"proj,omitempty"`
	Origin     string `json:"origin"`
	ExpiresAt  int64  `json:"exp"` // unix seconds; 0 means no expiry
	Nonce      string `json:"nonce"`
	Generation int64  `json:"gen"`
}

// Actor is the appmanager stateful actor. It owns the app registry (Apps /
// Records), the child routing table (children), pending-reload markers, the
// free-agent policy map, generated-manifest records, audit trails and
// ephemeral sessions.
//
// Lane split and the single-writer contract:
//
//   - The owner lane (stateful control-plane mutations: register / unregister
//     / retry_cleanup / cast / emit / plugin_emit / session_create /
//     agent_action ...) and the dedicated "appmanager_ops" lane (dev_gate /
//     dev_generate / reload / reload_project / project_package /
//     register_project / plugin_load / plugin_unload) run on SEPARATE
//     goroutines, so stateful handlers on the two lanes execute concurrently.
//     PureContext handlers (list / get / audit / callable_info / dev_guide /
//     host_protocol / icon_names / panel_topology / route_token /
//     report_process_state / pluginhost_online / session_resolve /
//     session_revoke / component_* / invoke / invoke_stream) run on the forked
//     "pure" loop. There is
//     therefore no single-lane writer for the shared maps below: Apps,
//     Records, children, PendingReloads, FreeAgentPolicies, GeneratedManifests
//     and AuditRecords are all written from both lanes.
//
//   - The single writer is therefore logical, not physical: a.mu serializes
//     every access (read or write) to the shared maps, and each handler's
//     multi-field transition (e.g. reload's PendingReloads+Records+Apps
//     candidate commit, register's Apps+Records install) is performed inside
//     ONE contiguous a.mu section so observers never see a torn tuple.
//
//   - Two invariants keep the split deadlock-free and race-free:
//     1. a.mu is only ever held for µs-scale map work — never across an
//     .Await(), EmitEvent, recordAudit or Save. recordAudit and Save take
//     a.mu themselves, so calling them while holding a.mu deadlocks.
//     2. Every map read outside the above helpers must be under a.mu; the
//     audit helpers (auditInvoke / auditRuntime / auditLifecycle) and the
//     binding helpers (bind/unbindFreeAgentPolicy) take a.mu internally
//     for exactly this reason.
//
//   - Sessions (sessions map) are the exception: they are written only on the
//     owner lane (session_create / session_revoke / revokeAppSessions via
//     unregister). Invoke (ops lane) and cast/emit (owner lane) read them
//     under a.mu, so a session can never be mutated by the ops lane.
//
//   - Accepted semantic: a full handler is NOT atomic across lanes. A
//     concurrent register and reload of the SAME app may interleave per
//     critical section; in-memory state stays consistent (no torn maps) and
//     persistence last-write-wins, which is safe because both operations
//     serialize their own persisted transitions.
type Actor struct {
	actor.Host
	// stateLoaded is set once Load() has completed (first start included).
	// OnStop skips the save before that: gospore's ForceCleanup (45e5a37)
	// guarantees OnStop even when aborted mid-OnInit, and saving then would
	// wipe the persisted apps record with zero-value maps.
	stateLoaded       atomic.Bool
	Apps              map[string]gen.AppManifest `gospore:"component,admin"`
	Records           map[string]appRecord
	PendingReloads    map[string]pendingReload
	FreeAgentPolicies map[string]appbinding.FreeAgentPolicy
	// pluginAgentSurfaceIDs tracks, per (app, binding slot), the agent id whose
	// surface binding the plugin_agent reconcile registered (appID+"\x00"+slot
	// → agent actor id). Needed because appbinding.Registry is keyed (appID,
	// agentID) and offers no per-app unbind; cleanup uses the tracked ids to
	// Unbind.
	pluginAgentSurfaceIDs map[string]string
	actorID               string
	mu                    sync.Mutex
	children              map[string]string

	sessions       map[string]appSession
	reloadingApps  map[string]bool
	hostCallsCache map[string]codegen.HostCallSchema
	protocol       *protocol.Manager
	bindings       *appbinding.Registry
	store          persist.Persist
	// sessionKey is the ephemeral HMAC key used to sign/verify session
	// tokens. It is generated on every OnInit and never persisted, which
	// is what makes all sessions restart-invalidated by construction.
	sessionKey []byte
	// exportSigKey is the per-host Ed25519 key that signs app_export
	// packages. Generated once on first use and persisted in saveState:
	// restoring (not regenerating) the key across restarts is what keeps
	// previously exported zips verifiable against their embedded PACKAGE.pub.
	exportSigKey ed25519.PrivateKey
	AuditRecords []appbinding.AuditRecord `gospore:"component,admin"`
	// auditPending counts audit records accumulated since the last successful
	// Save. recordAudit uses it to batch disk writes: only when it reaches
	// auditFlushThreshold does recordAudit trigger a full Save, instead of
	// serializing the entire actor state on every audit entry.
	auditPending int
	// GeneratedManifests tracks codegen output per project (projectID → manifest).
	// Consumed by project.write write-protection and gate manifest-consistency checks.
	GeneratedManifests map[string]codegen.GeneratedManifest `json:"generatedManifests,omitempty"`

	// appLocks guards per-app transaction serialization (reload vs load vs
	// plugin lifecycle): different apps proceed concurrently, the same app
	// serializes. Lazily populated; appLocksMu only guards the map itself.
	appLocksMu sync.Mutex
	appLocks   map[string]*sync.Mutex
}

// lockApp acquires the per-app transaction lock for id and returns the
// release function. Different apps take independent locks; the same app
// serializes (see lane_parallelism_test.go).
func (a *Actor) lockApp(id string) func() {
	a.appLocksMu.Lock()
	m, ok := a.appLocks[id]
	if !ok {
		if a.appLocks == nil {
			a.appLocks = make(map[string]*sync.Mutex)
		}
		m = &sync.Mutex{}
		a.appLocks[id] = m
	}
	a.appLocksMu.Unlock()
	m.Lock()
	var once sync.Once
	return func() { once.Do(m.Unlock) }
}

var _ persist.Persistent = (*Actor)(nil)

// withMu runs fn under a.mu with a defer-protected unlock. A panic inside a
// critical section (recovered upstream by the handler runtime) must never
// orphan the mutex: the 2026-09-09 incident had every appmanager callable —
// including OnStop's saveState — deadlocked on a lock held by a recovered
// goroutine, wedging the whole control plane. All new a.mu sections must use
// withMu (or an explicit defer) instead of manual Lock/Unlock pairs.
func (a *Actor) withMu(fn func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn()
}

func NewActor() actor.Actor   { return &Actor{} }
func (a *Actor) Type() string { return "appmanager" }

func (a *Actor) OnInit(ctx actor.Context) error {
	a.actorID = ctx.Self().ID().String()
	a.children = map[string]string{}

	a.sessions = map[string]appSession{}
	// reloadingApps guards the reload prepare→commit window: while an app's
	// native reload is in flight, handleReportProcessState must not switch
	// the gateway proxy to the candidate backend early — the record still
	// carries the pre-reload secret, so an early switch mints a proxy token
	// against the old secret and every request 401s until commitBackend
	// installs the new one. commitBackend (or the abort paths) clears the
	// flag. Purely in-memory: a crash loses it, which is fine — crash
	// recovery runs recoverPendingReloads from the persisted marker and
	// never races a live reload.
	a.reloadingApps = map[string]bool{}
	// pluginAgentSurfaceIDs is rebuilt from the persisted plugin_agent bind
	// state by reconcilePluginAgentsOnStart, but it must be writable before
	// then: a plugin_agent-bearing app reload (reconcilePluginAgent) that
	// lands while the map is still nil would panic on the first assignment.
	// The post-mortem actor crash surfaced exactly this nil map.
	a.pluginAgentSurfaceIDs = map[string]string{}
	a.GeneratedManifests = map[string]codegen.GeneratedManifest{}
	// Generate the ephemeral session signing key. Regenerated on every
	// start, so any token issued before this start fails signature
	// verification — sessions do not survive an appmanager restart.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("appmanager: generate session key: %w", err)
	}
	a.sessionKey = key
	if a.bindings == nil {
		a.bindings = appbinding.NewRegistry()
	}
	if a.FreeAgentPolicies == nil {
		a.FreeAgentPolicies = map[string]appbinding.FreeAgentPolicy{}
	}
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("appmanager"))
		if err != nil {
			return err
		}
	}
	if a.Records == nil {
		a.Records = map[string]appRecord{}
	}
	if a.Apps == nil {
		a.Apps = map[string]gen.AppManifest{}
	}
	var saved struct {
		Apps           map[string]gen.AppManifest `json:"apps"`
		Records        map[string]appRecord       `json:"records"`
		AuditRecords   []appbinding.AuditRecord   `json:"auditRecords"`
		PendingReloads map[string]pendingReload   `json:"pendingReloads"`
		ExportSigKey   []byte                     `json:"exportSigKey"`
	}
	if err := persist.LoadOrZero(a.store, a.actorID, &saved); err != nil {
		ctx.Logger().Error("appmanager: load state failed", "error", err)
	} else {
		if saved.Apps != nil {
			a.Apps = saved.Apps
		}
		if saved.Records != nil {
			a.Records = saved.Records
		}
		if saved.AuditRecords != nil {
			a.AuditRecords = saved.AuditRecords
		}
		if saved.PendingReloads != nil {
			a.PendingReloads = saved.PendingReloads
		}
		a.exportSigKey = ed25519.PrivateKey(saved.ExportSigKey)
	}
	// Migration cleanup (2026-08-19 builtinapp removal): state persisted before
	// that refactor may still contain builtin-runtime records (builtin.browser /
	// builtin.ssh). register now rejects every runtime except spore/native, so
	// restoring such a record would only mark it failed in spawnChild
	// (unsupported runtime) and leave a permanent failed ghost in
	// appmanager.list. Drop them from Apps/Records instead and audit the
	// removal; every other record restores unchanged and nothing here may
	// abort OnStart.
	purged := false
	for appID, record := range a.Records {
		if runtime := record.Manifest.Runtime; runtime != "spore" && runtime != "native" {
			delete(a.Apps, appID)
			delete(a.Records, appID)
			delete(a.children, appID)
			a.recordAudit(appbinding.AuditRecord{
				Time:     time.Now(),
				AppID:    appID,
				Runtime:  runtime,
				Callable: "load_migration",
				Allowed:  true,
				Reason:   "appmanager: dropped legacy record with unsupported runtime during restore",
			})
			ctx.Logger().Info("appmanager: dropped legacy app record with unsupported runtime", "app", appID, "runtime", runtime)
			purged = true
			continue
		}
		if err := a.bindFreeAgentPolicy(record.Manifest); err != nil {
			ctx.Logger().Error("appmanager: restore binding failed", "app", appID, "error", err)
			record.State = "failed"
			record.Error = redactProcessCause(fmt.Sprintf("appmanager: restore binding: %v", err))
			record.ActorID = ""
			a.Records[appID] = record
			delete(a.children, appID)
			continue
		}
		if record.ActorID != "" {
			a.children[appID] = record.ActorID
		}
		// A host restart clears the unload-pending condition: the leaked
		// in-process library mapping died with the previous process, so the
		// app is a plain stopped record that may load again safely.
		if record.State == "unload_pending" {
			record.State = "stopped"
			record.Error = ""
			a.Records[appID] = record
			ctx.Logger().Info("appmanager: unload-pending cleared by host restart", "app", appID)
		}
	}
	// Persist the purged state so the ghosts cannot reappear after a crash
	// before the next state change; a failed save is logged, never fatal.
	if purged {
		if err := a.Save(); err != nil {
			ctx.Logger().Error("appmanager: persist legacy record purge failed", "error", err)
		}
	}
	a.rehydrateAssetRefs(ctx.Logger().Warn)
	return nil
}
func (a *Actor) OnStart(ctx actor.Context) error {
	if manager, ok := resource.Get[*protocol.Manager](ctx.Resources(), protocol.ManagerKey); ok {
		a.protocol = manager
	}
	// Dedicated orchestration lane. The multi-step orchestration handlers
	// (dev_gate / dev_generate / reload / reload_project / project_package /
	// register_project / app_export / plugin_load / plugin_unload / invoke / agent_action)
	// each run 1..11+ sequential cross-actor .Await hops and can take seconds
	// to minutes (native builds, gate file scans). Running them on a DEDICATED
	// stateful lane keeps them off the owner lane, so control-plane handlers
	// (list / get / audit / session_* / route_token / cast / emit / dev_guide)
	// stay responsive while an orchestration is in flight. Synchronous
	// orchestration on the dedicated lane is allowed by the Owner-Lane
	// constraint; see the single-writer note on Actor for the concurrency
	// contract between the two lanes.
	if err := ctx.RegisterLoop("appmanager_ops", actor.ModeStateful); err != nil {
		return fmt.Errorf("appmanager: register ops loop: %w", err)
	}

	// Session callables are Public so any authenticated frontend caller can
	// obtain a session token for an app entrypoint. The token is the trust
	// boundary — session_create binds the caller's identity into a signed
	// credential; invoke authorization uses that identity plus the callable's
	// declared Permission checked against the host security policy.
	if err := ctx.Register("appmanager.session_create", a.handleSessionCreate, actor.Public()); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.session_resolve", a.handleSessionResolve, actor.Public()); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.session_revoke", a.handleSessionRevoke, actor.Public()); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.route_token", a.handleRouteToken, actor.Public()); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.list", a.handleList, actor.Public(),
		actor.WithDescription("List registered apps as AppStatus entries (manifest id/name/version/runtime plus live record state), sorted by id. Read-only."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.register", a.handleRegister, actor.AdminOnly(),
		actor.WithDescription("Register an app from an in-memory package (Manifest + EntryModule + Modules source map): validates the manifest and modules, runs the registration pipeline (policy, protocol, persistence), then starts the spore child or loads the native artifact."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.unregister", a.handleUnregister, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Unregister an app by ID: stop its runtime and remove the record. A failure mid-way leaves the record in cleanup_pending — finish it with appmanager.retry_cleanup."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.retry_cleanup", a.handleRetryCleanup, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Idempotently finish or retry an app unregister that failed midway: completes the pending back-half cleanup when the record is in cleanup_pending, or retries the full unregister from unload_failed/unloading. Returns completed | failed | not_pending | not_found."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.get", a.handleGet, actor.Public(),
		actor.WithDescription("Get one registered app's AppStatus by ID (manifest id/name/version/runtime plus live record state)."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.component_list", a.handleComponentList, actor.Public(),
		actor.WithDescription("List every virtual app-bundle component currently projected by the running app registry. Each descriptor includes the cardId, title, icon, declared tools, and dependencies. Use this to discover which bundles each loaded app exposes for mounting. Read-only."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.component_get", a.handleComponentGet, actor.Public(),
		actor.WithDescription("Get a single virtual app-bundle component descriptor by cardId (app-bundle:<...>). Use it to inspect an app-exposed bundle's tools and dependencies before mounting it."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.invoke", a.handleInvoke, actor.Public(),
		actor.WithDescription("Invoke a callable exposed by a registered app: routes Id + Callable + Payload (raw JSON) through the app's own runtime (spore child or native plugin process) and returns the app's response."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.invoke_stream", a.handleInvokeStream, actor.Public(),
		actor.Streaming[gen.PluginInvokeChunk](),
	); err != nil {
		return err
	}

	if err := ctx.Register("appmanager.open_view", a.handleOpenView, actor.Public(),
		actor.WithLoop("appmanager_ops"),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Mount an app's view panel in the right panel, identical to a user clicking the app tile (ViewId optional, defaults to the first view entrypoint). Precondition for any frontend signal: while no panel is mounted there is no iframe, no bridge session, and pluginhost.plugin_dom / plugin_logs see nothing. Prefer this over open_global_browser for showing a plugin panel — the browser bypasses the plugin bridge."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.audit", a.handleAudit, actor.AdminOnly()); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.cast", a.handleCast, actor.Public()); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.emit", a.handleEmit, actor.Public()); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.plugin_emit", a.handlePluginEmit, actor.AdminOnly()); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.agent_action", a.handleAgentAction, actor.Public(),
		actor.WithLoop("appmanager_ops"),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.reload", a.handleReload, actor.AdminOnly(),
		actor.WithLoop("appmanager_ops"),
		actor.WithDescription("Swap a running app to a new artifact version in place: validates, restarts the spore child (or reloads the native plugin artifact), audits the caller, and records success or failure. Plugins self-heal a wedged record."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.project_package", a.handleProjectPackage, actor.AdminOnly(),
		actor.WithLoop("appmanager_ops"),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.register_project", a.handleRegisterProject, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithLoop("appmanager_ops"),
		actor.WithDescription("Register a dev-built app from the current project directory into the host app registry (dev loop step: appdef -> dev_generate -> implement handlers -> register_project -> open_view). Irreversible per run; use reload_project to pick up source changes."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.registration_preview", a.handleRegistrationPreview, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithLoop("appmanager_ops"),
		actor.WithDescription("Resolve the manifest a pending registration would install (register / register_project / install_local / content_install source), read-only: no build, load, or persistence. Used by the permission panel to show the requested host capabilities before approval."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.reload_project", a.handleReloadProject, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithLoop("appmanager_ops"),
		actor.WithDescription("Reload a project-registered dev app: rebuild the artifact from the project sources and restart its runtime in place."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.callable_info", a.handleCallableInfo, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Return the plugin dev callable reference: entries of callable id + description for the appmanager dev-loop surface, filterable by Query substring. Read-only."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.dev_guide", a.handleDevGuide, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Return the structured plugin development guide by Topic: prerequisites, workflow (appdef → generate → implement → gate → register), host_api (plugin-side host calls), security, common errors. Omit Topic for the full guide. Read-only."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.host_protocol", a.handleHostProtocol, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Query the plugin-reachable host capability surface: grantable capabilities, the SDK host callIDs they gate, and request/response type names with schema IDs. Read-only discovery for the declare-then-extract flow (appdef permissions -> dev_generate hostproto.gen.go)."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.icon_names", a.handleIconNames, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("List valid icon names for card/bundle visual fields (data.icon, data.visual.icon) with labels, keywords, and categories. Optional Query substring or Category exact filter. Use before setting icons on bundle cards or app visuals."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.panel_topology", a.handlePanelTopology, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Read-only runtime topology diagnostic for a plugin panel: gateway base URL, the exact panel route /plugin/{id}/{route}?v={gen}, the plugin process's loopback listener address, and what each 401/404 on those hops means (auth working vs misconfigured). Replaces netstat port-scanning when a panel will not load."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.dev_generate", a.handleDevGenerate, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithLoop("appmanager_ops"),
		actor.WithDescription("Generate app scaffolding from a .appdef declaration: handlers, schema types, server/client glue, and (for native plugins) the SDK vendor step. Regenerating overwrites generated files; keep hand-written code out of generated paths."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.sdk_vendor", a.handleSDKVendor, actor.Public(),
		actor.WithDescription("Vendor the plugin SDK into the app directory (vendor-sdk/) and re-point go.mod at it, making the app buildable with zero host-machine dependencies. Template scaffolds vendor automatically; use this for existing apps or to refresh a stale vendored copy. Idempotent; re-running replaces vendor-sdk with the current host SDK."),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithLoop("appmanager_ops"),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.dev_gate", a.handleDevGate, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithLoop("appmanager_ops"),
		actor.WithDescription("Run the five registration gates against a dev app without registering it: resolve the .appdef, temporarily load the artifact to verify coverage, and return a pass/fail report per gate. Diagnostic only — passing gates does not register the app; call register_project for that."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.install_local", a.handleInstallLocal, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithLoop("appmanager_ops"),
		actor.WithDescription("Install an app from a local package: Path (a directory or zip path) or PackageData (raw zip bytes). A zip must carry app.manifest.json at its root and, when the app declares schemas, app.descriptors.json (the typed-callable descriptors the host protocol registry needs — an export always carries it); when PACKAGE.sig/PACKAGE.pub are present the Ed25519 signature is verified against the recomputed canonical package hash and a tampered package is rejected before any load. After verification the package is loaded and registered, invocable like any register_project app."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.app_export", a.handleAppExport, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithLoop("appmanager_ops"),
		actor.WithDescription("Export a project app as a signed, installable zip package: app.manifest.json, modules, assets, schema descriptors (app.descriptors.json), and for native runtimes abi.json plus the single native binary — with an Ed25519 signature (PACKAGE.sig / PACKAGE.pub, base64) over the canonical package hash. ProjectId is optional for agent callers: leave it empty and the host resolves your bound project from the injected CallerAgentId (same contract as register_project); never transcribe the id from prompt text. Returns the zip bytes, the package hash, and the base64 public key; the caller persists PackageData itself (e.g. via project.write). The exported zip installs through appmanager.install_local."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.plugin_load", a.handlePluginLoad, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithLoop("appmanager_ops"),
		actor.WithDescription("Load a registered native plugin's artifact (native runtime only): starts the process / maps the artifact, routes its callables, and returns AppStatus. Already-live records self-heal routing; an unloaded in-process plugin stages restart_pending (activates on next host restart)."),
	); err != nil {
		return err
	}
	if err := ctx.Register("appmanager.plugin_unload", a.handlePluginUnload, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithLoop("appmanager_ops"),
		actor.WithDescription("Unload a registered native plugin (native runtime only): unloads the pluginhost artifact and marks the record unloaded. A failed unload lands in unload_failed and can be retried by calling again; the registration stays for plugin_load."),
	); err != nil {
		return err
	}
	// Internal actor-to-actor surface: pluginhost reports native process
	// verdicts (running/stopped/crashed) here. Default (internal) visibility —
	// no frontend or agent exposure; AdminOnly does not apply because the
	// caller is the system pluginhost actor.
	if err := ctx.Register("appmanager.report_process_state", a.handleReportProcessState,
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return err
	}
	// pluginhost fires this at the end of its own OnStart (after exposing).
	// The reconciliation below is the deterministic close of the cold-start
	// proxy gap: the restore-time "running" reports are delivered while
	// pluginhost is still unexposed, so the attach tells they trigger cannot
	// resolve pluginhost and are dropped (never retried — no further state
	// transitions follow). Re-attaching here is idempotent (router replace
	// semantics) and correct regardless of report ordering.
	if err := ctx.Register("appmanager.pluginhost_online", a.handlePluginhostOnline,
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return err
	}
	if err := ctx.RegisterEventKind("app_lifecycle", gen.AppLifecycleEvent{}, actor.Public()); err != nil {
		return err
	}
	if err := ctx.RegisterEventKind("app_event", gen.AppEventMessage{}, actor.Public()); err != nil {
		return err
	}
	// Expose the control plane BEFORE state recovery. The handler surface is
	// fully registered at this point, and the cell's owner loop only drains
	// after OnStart returns, so queued calls still never race half-restored
	// state — but the service name is visible for the whole recovery window
	// (peer actors' reports queue instead of bouncing) and a later recovery
	// failure can no longer unregister the entire appmanager.* domain.
	// Before this reorder, a single panic anywhere in the recovery tail
	// (2026-09-09: restore of a poisoned 139MB state) killed the cell with
	// Expose unreached, leaving every appmanager.* call "not registered".
	if err := ctx.RegisterDomain("appmanager").Expose(); err != nil {
		return fmt.Errorf("appmanager: expose service: %w", err)
	}
	// R4: recovery order is reload-first, cleanup-second, and the two are
	// mutually exclusive for the same app. recoverPendingReloads resolves
	// every pending reload marker (committing the candidate or rolling back
	// to the old record), leaving each such app in an active/running state
	// with no marker — never a cleanup-pending state. Only then does
	// recoverPendingCleanup sweep the cleanup-pending states. This ordering
	// guarantees no app is seen by both recoverers and that the child spawn
	// loop below never races a stale candidate artifact against the
	// pluginhost's still-loaded old artifact.
	//
	// Every recovery step and every per-app restore is panic-guarded
	// (recoverStep): recovery consumes persisted state, which is untrusted
	// input — one malformed record must mark that app failed, never take
	// the actor (and with it the whole control plane) down.
	a.recoverStep(ctx, "recoverPendingReloads", func() { a.recoverPendingReloads(ctx) })
	a.recoverStep(ctx, "recoverPendingCleanup", func() { a.recoverPendingCleanup(ctx) })
	// Self-heal (重启自愈): a native artifact file that vanished while the
	// host was down (external wipe of the inventory artifacts store) is
	// re-extracted from its recorded package zip BEFORE the spawn loop and
	// BEFORE the pluginhost's own OnStart restore (production order starts
	// the pluginhost after this actor). The heal only rewrites the missing
	// file; records whose zip is gone too are left for the failure path with
	// an explicit no-self-heal log inside healMissingArtifacts.
	a.recoverStep(ctx, "healMissingArtifacts", func() { a.healMissingArtifacts(ctx) })
	// Legacy native records (persisted by writers before the commitBackend
	// single-critical-section fix) may carry ActorID == "" while their state
	// says running. A native app's routing target is the pluginhost service,
	// not a child actor — spawnChild's native branch re-stamps ActorID and
	// the children sentinel itself — so they must NOT be skipped: skipping
	// them left the app permanently uninvocable after a restart
	// ("not running" from resolveInvokeAuth) even though the pluginhost had
	// restored the artifact. Spore apps genuinely need their preallocated
	// child actor ID: without it a restore would auto-generate a drifted ID,
	// so spore records without ActorID stay skipped.
	nativeRouteMissing := map[string]bool{}
	for appID, record := range a.Records {
		if record.ActorID == "" && record.Manifest.Runtime != "native" {
			continue
		}
		if isCleanupPendingState(record.State) {
			continue
		}
		if record.Manifest.Runtime == "native" && record.ActorID == "" {
			nativeRouteMissing[appID] = true
		}
		deferredSwap := record.State == stateRestartPending
		a.recoverStep(ctx, "restore "+appID, func() {
			if err := runGuarded(func() error { return a.spawnChild(ctx, appID, record, false) }); err != nil {
				a.failRestoredApp(ctx, appID, record, err)
				return
			}
			a.activateDeferredGeneration(ctx, appID, record, deferredSwap)
		})
	}
	if len(nativeRouteMissing) > 0 {
		// Persist the healed routes so one restart permanently repairs the
		// persisted state instead of re-healing in memory every start.
		a.recoverStep(ctx, "persistRestoredNativeRoutes", func() {
			healed := false
			a.withMu(func() {
				for appID := range nativeRouteMissing {
					if rec, ok := a.Records[appID]; ok && rec.ActorID != "" {
						healed = true
					}
				}
			})
			if healed {
				if err := a.Save(); err != nil {
					ctx.Logger().Error("appmanager: persist restored native routes failed", "error", err)
				}
			}
		})
	}
	a.recoverStep(ctx, "reregisterProtocolDescriptors", func() { a.reregisterProtocolDescriptors(ctx) })
	// Restore the dedicated plugin agents (agent_binding.plugin_agents, one
	// per binding slot): the workspace registry rows survive restarts, but
	// their actors and bind state are re-established here (same lifecycle
	// guarantee the spawn loop above gives the apps themselves).
	a.recoverStep(ctx, "reconcilePluginAgentsOnStart", func() { a.reconcilePluginAgentsOnStart(ctx) })
	// Builtin spore bundles (sporecall) register here — after restore, so a
	// persisted record is never double-registered, and mount-gating stays the
	// only path to their capability.
	a.recoverStep(ctx, "ensureBuiltinSporeApps", func() { a.ensureBuiltinSporeApps(ctx) })
	return nil
}

// recoverStep runs one OnStart recovery step with a panic guard: a panic is
// logged with its stack and the step is abandoned, but the surrounding
// OnStart continues. This is the fail-soft boundary between untrusted
// persisted state and the actor lifecycle — the surface is already exposed
// and later steps (including other apps' restores) still run.
func (a *Actor) recoverStep(ctx actor.Context, name string, step func()) {
	defer func() {
		if r := recover(); r != nil {
			ctx.Logger().Error("appmanager: recovery step panicked (step abandoned, surface stays exposed)",
				"step", name, "panic", fmt.Sprintf("%v", r), "stack", string(debug.Stack()))
		}
	}()
	step()
}

// runGuarded executes step and converts a panic into an error return, so a
// panicking per-app restore degrades to failRestoredApp for that app
// instead of aborting the whole recovery pass.
func runGuarded(step func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("appmanager: restore panicked: %v", r)
		}
	}()
	return step()
}

// failRestoredApp marks a restored app as failed with a stable diagnostic,
// emits the app_lifecycle:failed event, and persists the state. Used during
// OnStart when either child spawning or protocol descriptor re-registration
// fails after a process restart.
// activateDeferredGeneration handles the session-generation bump for a
// deferred artifact swap (A1): a restart_pending record carried a NEW panel
// artifact that only activates on the next host start. Its activation changes
// the served panel content, so the generation must bump exactly like a live
// reload — otherwise connected SPAs keep pointing iframes at the old ?v=
// cache-bust value (2026-09-14 generation-drift finding) and tokens bound to
// the pre-swap version outlive their artifact. The lifecycle event lets live
// SPAs upsert the new generation without waiting for a reconnect resync.
func (a *Actor) activateDeferredGeneration(ctx actor.Context, appID string, record appRecord, deferredSwap bool) {
	if !deferredSwap {
		return
	}
	var generation int64
	a.withMu(func() {
		rec, ok := a.Records[appID]
		if !ok || rec.State != stateRunning {
			return
		}
		rec.Generation++
		generation = rec.Generation
		a.Records[appID] = rec
	})
	if generation == 0 {
		return
	}
	if err := a.Save(); err != nil {
		ctx.Logger().Error("appmanager: persist deferred generation bump failed", "app", appID, "error", err)
	}
	a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{
		Kind:       "reloaded",
		ID:         appID,
		Runtime:    record.Manifest.Runtime,
		State:      stateRunning,
		Version:    record.Manifest.Version,
		Generation: generation,
	})
}

// emitLifecycleEvent publishes an app_lifecycle bus event and forwards it to
// pluginhost for fan-out to plugins listening to the kind (appdef `listen`
// blocks). The forward is a fire-and-forget tell: plugin delivery (bounded by
// the pluginhost invoke timeout) must never block the calling handler.
func (a *Actor) emitLifecycleEvent(ctx actor.PureContext, ev gen.AppLifecycleEvent) {
	_ = ctx.EmitEvent("app_lifecycle", ev)
	a.forwardHostEvent(ctx, "app_lifecycle", ev)
}

// emitAppEvent is emitLifecycleEvent for the app_event envelope kind.
func (a *Actor) emitAppEvent(ctx actor.Context, ev gen.AppEventMessage) error {
	err := ctx.EmitEvent("app_event", ev)
	a.forwardHostEvent(ctx, "app_event", ev)
	return err
}

// forwardHostEvent pushes one host bus event to pluginhost.event_deliver as a
// Tell (invoke + immediate close): no child actor, no response wait. Missing
// pluginhost (early boot, tests) is silently skipped — event delivery is
// best-effort by contract.
func (a *Actor) forwardHostEvent(ctx actor.PureContext, kind string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ref, found := ctx.LookupService(pluginhostServiceName)
	if !found || ref == nil {
		return
	}
	if call := ref.Invoke(ctx.Lifecycle(), "pluginhost.event_deliver", gen.PluginEventDeliverReq{Kind: kind, Payload: string(raw)}); call != nil {
		_ = call.Close()
	}
}

func (a *Actor) failRestoredApp(ctx actor.Context, appID string, record appRecord, cause error) {
	a.withMu(func() {
		record.State = "failed"
		record.Error = redactProcessCause(cause.Error())
		record.ActorID = ""
		a.Records[appID] = record
		delete(a.children, appID)
	})
	_ = a.Save()
	a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "failed", ID: appID, Runtime: record.Manifest.Runtime, State: "failed", Version: record.Manifest.Version, Error: cause.Error()})
	ctx.Logger().Error("appmanager: restore child failed", "app", appID, "error", cause)
}

// reregisterProtocolDescriptors re-registers protocol descriptors for all apps
// whose children were successfully restored (ActorID still set). Apps marked
// failed during the spawn loop (ActorID cleared) are skipped. This runs as a
// separate pass after the spawn loop in OnStart.
func (a *Actor) registerRecordProtocol(record appRecord) error {
	if len(record.SchemaDescriptors) == 0 {
		return a.protocol.RegisterAppManifest(record.Manifest)
	}
	registration, err := protocol.ValidateAppSchemaDescriptors(record.Manifest, record.SchemaDescriptors)
	if err != nil {
		return err
	}
	return a.protocol.RegisterAppProtocol(registration)
}

func (a *Actor) reregisterProtocolDescriptors(ctx actor.Context) {
	if a.protocol == nil {
		return
	}
	for appID, record := range a.Records {
		if record.ActorID == "" {
			continue
		}
		if isCleanupPendingState(record.State) {
			continue
		}
		if err := a.registerRecordProtocol(record); err != nil {
			a.failRestoredApp(ctx, appID, record, fmt.Errorf("appmanager: re-register protocol descriptor: %w", err))
		}
	}
}

// maxAuditRecords is the maximum number of audit records kept in memory and
// persisted to disk. When exceeded, oldest records are trimmed.
const maxAuditRecords = 10000

// auditFlushThreshold is the number of audit records that accumulate in memory
// before recordAudit triggers a full Save. This batches disk I/O so that
// high-frequency invocations (each producing at least one audit entry) don't
// each serialize the entire actor state. Records below the threshold remain in
// memory and are persisted on the next Save triggered by any code path
// (register, reload, unregister, OnStop) or when the threshold is reached.
const auditFlushThreshold = 64

func (a *Actor) recordAudit(record appbinding.AuditRecord) {
	flush := false
	a.withMu(func() {
		a.AuditRecords = append(a.AuditRecords, record)
		// Trim oldest records if exceeding the cap
		if len(a.AuditRecords) > maxAuditRecords {
			excess := len(a.AuditRecords) - maxAuditRecords
			a.AuditRecords = a.AuditRecords[excess:]
		}
		a.auditPending++
		flush = a.auditPending >= auditFlushThreshold
		if flush {
			a.auditPending = 0
		}
	})
	if flush {
		_ = a.Save()
	}
}

func (a *Actor) OnStop(_ actor.Context) error {
	if !a.stateLoaded.Load() {
		// Stopped mid-OnInit (gospore ForceCleanup): saving the zero-value
		// Apps/Records would wipe the persisted record. Nothing was mutated.
		return nil
	}
	return a.Save()
}

// saveState persists the actor state with the given Apps/Records maps. It is
// the shared serialization path used by both Save (which passes the live maps)
// and finishCleanup (which passes filtered copies so the deletion is persisted
// before the in-memory maps are mutated).
//
// saveState copies the in-memory series under a.mu so that a concurrent
// owner-lane or ops-lane handler cannot mutate the maps while the JSON
// marshal reads them. The persistent store is called outside the lock.
func (a *Actor) saveState(apps map[string]gen.AppManifest, records map[string]appRecord) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("appmanager"))
		if err != nil {
			return err
		}
	}
	// Snapshot maps + audit state under the lock so a concurrent handler
	// on the other lane cannot race the marshal. The store.Save call is
	// outside the lock (it blocks on file I/O).
	var recordsCopy map[string]appRecord
	var pendingCopy map[string]pendingReload
	appsCopy := make(map[string]gen.AppManifest, len(apps))
	var auditCopy []appbinding.AuditRecord
	var genCopy map[string]codegen.GeneratedManifest
	var exportKeyCopy []byte
	a.withMu(func() {
		for k, v := range apps {
			appsCopy[k] = v
		}
		recordsCopy = make(map[string]appRecord, len(records))
		for k, v := range records {
			recordsCopy[k] = v
		}
		auditCopy = append([]appbinding.AuditRecord(nil), a.AuditRecords...)
		pendingCopy = make(map[string]pendingReload, len(a.PendingReloads))
		for k, v := range a.PendingReloads {
			pendingCopy[k] = v
		}
		genCopy = make(map[string]codegen.GeneratedManifest, len(a.GeneratedManifests))
		for k, v := range a.GeneratedManifests {
			genCopy[k] = v
		}
		exportKeyCopy = append([]byte(nil), a.exportSigKey...)
		a.auditPending = 0
	})
	if err := a.offloadAssetsSnapshot(recordsCopy, pendingCopy); err != nil {
		return err
	}
	return a.store.Save(a.actorID, struct {
		Apps               map[string]gen.AppManifest           `json:"apps"`
		Records            map[string]appRecord                 `json:"records"`
		AuditRecords       []appbinding.AuditRecord             `json:"auditRecords"`
		PendingReloads     map[string]pendingReload             `json:"pendingReloads,omitempty"`
		GeneratedManifests map[string]codegen.GeneratedManifest `json:"generatedManifests,omitempty"`
		ExportSigKey       []byte                               `json:"exportSigKey,omitempty"`
	}{appsCopy, recordsCopy, auditCopy, pendingCopy, genCopy, exportKeyCopy})
}

func (a *Actor) Save() error {
	return a.saveState(a.Apps, a.Records)
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
		a.store, err = persist.New(config.PersistConfig("appmanager"))
		if err != nil {
			return err
		}
	}
	var saved struct {
		Apps               map[string]gen.AppManifest           `json:"apps"`
		Records            map[string]appRecord                 `json:"records"`
		AuditRecords       []appbinding.AuditRecord             `json:"auditRecords"`
		PendingReloads     map[string]pendingReload             `json:"pendingReloads"`
		GeneratedManifests map[string]codegen.GeneratedManifest `json:"generatedManifests"`
		ExportSigKey       []byte                               `json:"exportSigKey"`
	}
	if err := persist.LoadOrZero(a.store, a.actorID, &saved); err != nil {
		return err
	}
	a.Apps, a.Records, a.AuditRecords, a.PendingReloads, a.GeneratedManifests = saved.Apps, saved.Records, saved.AuditRecords, saved.PendingReloads, saved.GeneratedManifests
	a.exportSigKey = ed25519.PrivateKey(saved.ExportSigKey)
	a.rehydrateAssetRefs(nil)
	return nil
}

// statusFromRecord builds an AppStatus from the manifest and record. Under
// the declaration-is-authorization model the manifest's declared Permissions
// are the granted set, so GrantedCapabilities mirrors Permissions.
func (a *Actor) statusFromRecord(manifest gen.AppManifest, record appRecord) gen.AppStatus {
	state := record.State
	if state == "" {
		state = "registered"
	}

	granted := make([]string, 0, len(manifest.Permissions))
	for _, c := range manifest.Permissions {
		if c == "" {
			continue
		}
		granted = append(granted, c)
	}
	sort.Strings(granted)

	return gen.AppStatus{
		ID:                  manifest.ID,
		Name:                manifest.Name,
		Runtime:             manifest.Runtime,
		State:               state,
		Version:             manifest.Version,
		Namespace:           manifest.Namespace,
		PackageHash:         record.PackageHash,
		ArtifactHash:        record.ArtifactHash,
		Generation:          record.Generation,
		Error:               record.Error,
		SchemaDescriptors:   record.SchemaDescriptors,
		Callables:           manifest.Callables,
		Events:              manifest.Events,
		Bundles:             manifest.Bundles,
		Entrypoints:         manifest.Entrypoints,
		Permissions:         manifest.Permissions,
		GrantedCapabilities: granted,
		BackendPort:         int32(record.BackendPort),
		BackendURL:          record.BackendUrl,
	}
}

func (a *Actor) handleList(_ actor.PureContext, _ gen.AppManagerListReq) (gen.AppManagerListResp, error) {
	type appRow struct {
		manifest gen.AppManifest
		record   appRecord
	}
	var rows []appRow
	// Reverse dependency edges for the whole registry, computed in the same
	// locked pass as the rows so list output is a consistent graph snapshot.
	dependents := map[string][]string{}
	a.withMu(func() {
		rows = make([]appRow, 0, len(a.Apps))
		for appID, m := range a.Apps {
			rows = append(rows, appRow{manifest: m, record: a.Records[appID]})
			for _, d := range m.Dependencies {
				dependents[d.ID] = append(dependents[d.ID], appID)
			}
		}
	})
	// Deterministic order: the agent tool surface (resolveAppTools) emits app
	// tool blocks in list order, so map iteration order must not leak through.
	sort.Slice(rows, func(i, j int) bool { return rows[i].manifest.ID < rows[j].manifest.ID })
	out := make([]gen.AppStatus, 0, len(rows))
	for i := range rows {
		status := a.statusFromRecord(rows[i].manifest, rows[i].record)
		status.Dependencies = rows[i].manifest.Dependencies
		reverse := append([]string(nil), dependents[rows[i].manifest.ID]...)
		sort.Strings(reverse)
		status.Dependents = reverse
		out = append(out, status)
	}
	return gen.AppManagerListResp{Items: out}, nil
}

// handleRegister registers an app and starts it (or loads its native
// artifact). It is the deferred=false form of doRegister; the deferred form
// is used by the installer flows when an in-process native artifact could
// not be swapped in and must wait for a host restart.
func (a *Actor) handleRegister(ctx actor.Context, req gen.AppManagerRegisterReq) (gen.AppStatus, error) {
	return a.doRegister(ctx, req, false)
}

// doRegister shares the full registration pipeline (validation, policy,
// protocol, persistence) between the immediate path (deferred=false: load and
// mark running) and the deferred path (deferred=true: persist the new record
// in restart_pending and skip the child/artifact activation — the artifact
// was already persisted into pluginhost ArtifactLoads by the caller and will
// activate on the next host restart).
func (a *Actor) doRegister(ctx actor.Context, req gen.AppManagerRegisterReq, deferred bool) (gen.AppStatus, error) {
	m := req.Manifest
	if req.EntryModule == "" || len(req.Modules) == 0 {
		return gen.AppStatus{}, fmt.Errorf("appmanager: package entry module and modules are required")
	}
	if _, ok := req.Modules[req.EntryModule]; !ok {
		return gen.AppStatus{}, fmt.Errorf("appmanager: entry module %q is missing", req.EntryModule)
	}
	for path, source := range req.Modules {
		if path == "" || source == "" {
			return gen.AppStatus{}, fmt.Errorf("appmanager: package modules must have non-empty paths and sources")
		}
	}
	if m.ID == "" || m.Namespace == "" || m.Name == "" || m.Version == "" {
		return gen.AppStatus{}, fmt.Errorf("appmanager: manifest id, name, namespace, and version are required")
	}
	if m.Runtime != "spore" && m.Runtime != "native" {
		return gen.AppStatus{}, fmt.Errorf("appmanager: unsupported runtime %q", m.Runtime)
	}
	if m.Runtime == "spore" && m.ProtocolVersion <= 0 {
		return gen.AppStatus{}, fmt.Errorf("appmanager: protocol version is required for spore runtime")
	}
	if m.Runtime == "spore" && len(m.Callables) == 0 {
		return gen.AppStatus{}, fmt.Errorf("appmanager: spore app must declare callables")
	}
	seenCallables := make(map[string]struct{}, len(m.Callables))
	for _, callable := range m.Callables {
		if callable.ID == "" || (callable.RequestSchema == "") != (callable.ResponseSchema == "") {
			return gen.AppStatus{}, fmt.Errorf("appmanager: callable descriptors require id and paired schemas")
		}
		if _, exists := seenCallables[callable.ID]; exists {
			return gen.AppStatus{}, fmt.Errorf("appmanager: duplicate callable %q", callable.ID)
		}
		seenCallables[callable.ID] = struct{}{}
	}
	canonicalHash, err := canonicalPackageHash(m, req.EntryModule, req.Modules, req.Assets, req.SchemaDescriptors, req.Abi, req.ArtifactHash)
	if err != nil {
		return gen.AppStatus{}, err
	}
	if req.PackageHash != "" && !strings.EqualFold(req.PackageHash, canonicalHash) {
		return gen.AppStatus{}, fmt.Errorf("appmanager: package hash mismatch")
	}
	req.PackageHash = canonicalHash
	record := appRecord{
		Manifest: m, EntryModule: req.EntryModule, Modules: req.Modules, Assets: req.Assets, SchemaDescriptors: req.SchemaDescriptors,
		PackageHash: req.PackageHash, PackageHashVersion: canonicalPackageHashVersion, PackagePath: req.PackagePath,
		ArtifactPath: req.ArtifactPath, ArtifactHash: req.ArtifactHash, Abi: req.Abi, Origin: normalizeOrigin(req.Origin),
	}
	if m.Runtime == "native" && (record.ArtifactPath == "" || record.ArtifactHash == "" || record.Abi == nil) {
		return gen.AppStatus{}, fmt.Errorf("appmanager: native registration requires artifact path, hash, and ABI")
	}
	// Origin override rule: an existing app may only be replaced by a
	// registration whose origin ranks >= the existing origin
	// (project > user > builtin).
	// Read a.Records under the lock (register runs on owner lane, but
	// reload/plugin/register_project run on the ops lane concurrently), and
	// audit outside the lock — recordAudit takes a.mu itself.
	var originErr error
	a.withMu(func() {
		originErr = checkOriginOverride(a.Records, record)
	})
	if originErr != nil {
		a.recordAudit(appbinding.AuditRecord{AppID: m.ID, Runtime: m.Runtime, Callable: "register", Allowed: false, Reason: originErr.Error()})
		return gen.AppStatus{}, originErr
	}
	// Registration-time security validation: capability vocabulary, callable
	// and event permission refs, and signer/ABI invariants are checked here.
	var securityErr error
	a.withMu(func() {
		securityErr = validateManifestSecurity(m, a.Records)
	})
	if securityErr != nil {
		a.recordAudit(appbinding.AuditRecord{AppID: m.ID, Runtime: m.Runtime, Callable: "register", Allowed: false, Reason: securityErr.Error()})
		return gen.AppStatus{}, securityErr
	}
	if err := validateEntrypointExports(m); err != nil {
		a.recordAudit(appbinding.AuditRecord{AppID: m.ID, Runtime: m.Runtime, Callable: "register", Allowed: false, Reason: err.Error()})
		return gen.AppStatus{}, err
	}
	var oldManifest *gen.AppManifest
	a.withMu(func() {
		if old, exists := a.Apps[m.ID]; exists {
			oldCopy := old
			oldManifest = &oldCopy
		}
	})
	if oldManifest != nil {
		a.unbindFreeAgentPolicy(*oldManifest)
		// Re-registering over an existing app must drop the old schema
		// registration first, or ValidateAppSchemaDescriptors hits
		// "duplicate dynamic struct" on the same namespace (admin-tools
		// migration: register_project over app.admin-tools required a
		// manual unregister). Mirrors the reload path's rollback order.
		if a.protocol != nil && oldManifest.Namespace != "" {
			_ = a.protocol.UnregisterAppProtocol(oldManifest.Namespace)
		}
	}
	if err := a.bindFreeAgentPolicy(m); err != nil {
		if oldManifest != nil {
			_ = a.bindFreeAgentPolicy(*oldManifest)
		}
		a.recordAudit(appbinding.AuditRecord{AppID: m.ID, Runtime: m.Runtime, Callable: "register", Allowed: false, Reason: err.Error()})
		return gen.AppStatus{}, err
	}
	if a.protocol != nil {
		if len(m.Schemas) > 0 && len(record.SchemaDescriptors) == 0 {
			return gen.AppStatus{}, fmt.Errorf("appmanager: schema descriptors are required")
		}
		if err := a.registerRecordProtocol(record); err != nil {
			a.unbindFreeAgentPolicy(m)
			// A failed protocol registration must not leave dynamic schema
			// entries behind: retrying the same register would hit
			// "duplicate dynamic struct" on this namespace forever.
			if a.protocol != nil && m.Namespace != "" {
				_ = a.protocol.UnregisterAppProtocol(m.Namespace)
			}
			if oldManifest != nil {
				_ = a.bindFreeAgentPolicy(*oldManifest)
			}
			return gen.AppStatus{}, err
		}
	}
	var oldRecord appRecord
	var hadOldRecord bool
	var oldChild string
	var hadOldChild bool
	a.withMu(func() {
		if old, exists := a.Records[m.ID]; exists {
			oldRecord, hadOldRecord = old, true
		}
		if child, exists := a.children[m.ID]; exists {
			oldChild, hadOldChild = child, true
		}
	})
	record.State = stateStarting
	// Deferred registration (A1): the in-process host could not swap the
	// artifact; the caller already persisted it into pluginhost
	// ArtifactLoads. The record waits in restart_pending until the next
	// host restart activates it (appmanager OnStart cross-validation or an
	// explicit plugin_load → running).
	if deferred {
		record.State = stateRestartPending
		// Carry the pluginhost route on the deferred record (native only —
		// deferral is exclusively produced by native install flows). Without
		// it the OnStart spawn loop skips the record (ActorID == "") and a
		// deferred re-register would sit in restart_pending forever even
		// though the pluginhost restored the new artifact. The in-memory
		// children map stays untouched pre-restart so the still-loaded old
		// artifact keeps routing; on the next start OnInit rebuilds children
		// from this ActorID and the spawn loop cross-validates the restored
		// artifact (idempotent pluginhost.artifact_load) → running.
		if m.Runtime == "native" {
			record.ActorID = pluginhostServiceName
		}
	}
	// Session generation: a fresh app starts at 1; re-registering an
	// existing app bumps the generation so any tokens bound to the old
	// version are invalidated.
	if hadOldRecord && oldRecord.Generation > 0 {
		record.Generation = oldRecord.Generation + 1
	} else {
		record.Generation = 1
	}
	a.withMu(func() {
		a.Apps[m.ID] = m
		a.Records[m.ID] = record
	})
	if err := a.Save(); err != nil {
		a.unbindFreeAgentPolicy(m)
		if oldManifest != nil {
			_ = a.bindFreeAgentPolicy(*oldManifest)
		}
		a.withMu(func() {
			if oldManifest != nil {
				a.Apps[m.ID] = *oldManifest
			} else {
				delete(a.Apps, m.ID)
			}
			if hadOldRecord {
				a.Records[m.ID] = oldRecord
			} else {
				delete(a.Records, m.ID)
			}
			if hadOldChild {
				a.children[m.ID] = oldChild
			} else {
				delete(a.children, m.ID)
			}
		})
		return gen.AppStatus{}, err
	}

	// Replaced-registration authority transfer (zip-install takeover): a
	// same-ID registration whose new record no longer references the old
	// install's inventory files supersedes the old authority. install_local
	// replaces user installs with user installs (its own GC in
	// handleInstallLocal removes the old files after commit); a
	// register_project / register with project origin over a zip-installed
	// app (user origin) switches authority back to the project directory, so
	// the stored zip and its content-addressed artifact become garbage —
	// remove them and audit the takeover. checkOriginOverride above already
	// admitted the replacement (project > user).
	if hadOldRecord && record.Origin == "project" && normalizeOrigin(oldRecord.Origin) == "user" {
		// Remove only inventory-owned files that the new record does not
		// itself reference (a content-addressed reinstall of identical bytes
		// keeps the file alive through the new PackagePath).
		if oldRecord.PackagePath != "" && oldRecord.PackagePath != record.PackagePath {
			removeOwnedInventoryFile(oldRecord.PackagePath)
		}
		if oldRecord.ArtifactPath != "" && oldRecord.ArtifactPath != record.ArtifactPath {
			removeOwnedInventoryFile(oldRecord.ArtifactPath)
		}
		a.auditLifecycle(m.ID, "register", fmt.Sprintf(
			"zip-install takeover by project origin: removed inventory zip %q and artifact %q (authority back to project directory)",
			oldRecord.PackagePath, oldRecord.ArtifactPath))
	}

	// Immediate path: spawn the child (spore) or mark the plugin running
	// against the pluginhost; failures roll back to State=failed. Deferred
	// path: the record is already persisted as restart_pending — do not
	// spawn or activate; the artifact activates on the next host restart.
	if !deferred {
		if err := a.spawnChild(ctx, m.ID, record, true); err != nil {
			if a.protocol != nil {
				_ = a.protocol.UnregisterAppProtocol(m.Namespace)
			}
			a.unbindFreeAgentPolicy(m)
			if oldManifest != nil {
				_ = a.bindFreeAgentPolicy(*oldManifest)
			}
			a.withMu(func() {
				record.State = stateFailed
				record.Error = redactProcessCause(err.Error())
				record.ActorID = ""
				a.Records[m.ID] = record
				delete(a.children, m.ID)
			})
			_ = a.Save()
			if ctx != nil {
				a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "failed", ID: m.ID, Runtime: m.Runtime, State: "failed", Version: m.Version, Error: err.Error()})
			}
			return gen.AppStatus{}, err
		}
		// spawnChild wrote the running record (with the child/pluginhost
		// route in ActorID) into the map on its own copy; merge that back
		// onto ours instead of clobbering it with the pre-spawn record. A
		// lost ActorID silently disables the OnStart spawn-loop
		// cross-validation for every registered app (records are skipped)
		// and orphans the children rebuild after a host restart.
		a.withMu(func() {
			stored := a.Records[m.ID]
			stored.State = stateRunning
			stored.Error = ""
			a.Apps[m.ID] = m
			a.Records[m.ID] = stored
			record = stored
		})
		if err := a.Save(); err != nil {
			return gen.AppStatus{}, err
		}
		a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "running", ID: m.ID, Runtime: m.Runtime, State: record.State, Version: m.Version})
		// Provision (or reconcile) the dedicated plugin agents declared by
		// agent_binding.plugin_agents — one create per binding slot.
		// Declaration IS authorization; a failure here fails the registration
		// so the operator sees it.
		if err := a.reconcilePluginAgent(ctx, m.ID); err != nil {
			a.recordAudit(appbinding.AuditRecord{AppID: m.ID, Runtime: m.Runtime, Callable: "register", Allowed: false, Reason: err.Error()})
			return gen.AppStatus{}, err
		}
		a.auditLifecycle(m.ID, "register", "registered")
		return a.statusFromRecord(m, record), nil
	}
	// Deferred registration: children is left untouched (the previous
	// version's entry survives; a fresh app has none) so the pluginhost
	// keeps routing the still-loaded old artifact until restart.
	a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "restart_pending", ID: m.ID, Runtime: m.Runtime, State: stateRestartPending, Version: m.Version})
	a.auditLifecycle(m.ID, "register", "restart_pending: in-process artifact blocked, activates on restart")
	return a.statusFromRecord(m, record), nil
}
func (a *Actor) markUnregisterFailed(appID, reason string) {
	a.withMu(func() {
		record := a.Records[appID]
		record.State = stateUnloadFailed
		record.Error = reason
		a.Records[appID] = record
	})
	_ = a.Save()
	a.auditLifecycle(appID, "unregister", "unload_failed: "+reason)
}

func (a *Actor) handleUnregister(ctx actor.Context, req gen.AppManagerUnregisterReq) error {
	var exists bool
	a.withMu(func() {
		_, exists = a.Records[req.ID]
	})
	if !exists {
		return fmt.Errorf("appmanager: app %q not found", req.ID)
	}

	// R4: reload/unregister mutual exclusion. An unresolved pending reload
	// means the candidate manifest was persisted as the active record but
	// never committed to the pluginhost (which still holds the old artifact).
	// Abort the reload first — restoring the pre-reload record and clearing
	// the marker — so the cleanup state machine operates on the pre-reload
	// state and no stale marker survives a crash.
	a.abortPendingReload(req.ID)

	var record appRecord
	a.withMu(func() {
		record = a.Records[req.ID]
	})

	// If the app is already past the front half (artifact/child torn down),
	// delegate directly to the back-half cleanup. This makes unregister
	// idempotent across retries that left the app in a pending state.
	if record.State == stateProtocolCleanupPending || record.State == stateCleanupPending {
		return a.finishCleanup(ctx, req.ID)
	}

	// --- Front half ---
	// Mark unloading and persist the intent so a crash here is recoverable.
	a.withMu(func() {
		record = a.Records[req.ID]
		record.State = stateUnloading
		a.Records[req.ID] = record
	})
	_ = a.Save()

	if err := a.unloadFrontHalf(ctx, req.ID); err != nil {
		a.markUnregisterFailed(req.ID, err.Error())
		return err
	}

	// Transition to protocol_cleanup_pending and persist.
	a.withMu(func() {
		record = a.Records[req.ID]
		record.State = stateProtocolCleanupPending
		record.Error = ""
		a.Records[req.ID] = record
	})
	_ = a.Save()

	// --- Back half ---
	return a.finishCleanup(ctx, req.ID)
}
