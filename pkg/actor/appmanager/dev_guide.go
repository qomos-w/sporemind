package appmanager

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleCallableInfo returns interface descriptions for the callables exposed
// by the plugin-dev bundle. It is a read-only knowledge callable so the
// agent can discover callable IDs, parameters, effects, and usage guidance
// before invoking them.
func (a *Actor) handleCallableInfo(_ actor.PureContext, req gen.AppManagerCallableInfoReq) (gen.AppManagerCallableInfoResp, error) {
	entries := pluginDevCallableInfo()
	query := strings.ToLower(req.Query)
	if query != "" {
		filtered := make([]gen.AppManagerCallableInfoEntry, 0, len(entries))
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.ID), query) || strings.Contains(strings.ToLower(e.Description), query) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}
	return gen.AppManagerCallableInfoResp{Items: entries}, nil
}

// handleDevGuide returns a structured Plugin development guide covering
// prerequisites, the appdef→generate→implement→gate→register workflow,
// the plugin-side host-call reference (host_api), security constraints, and
// common errors.
func (a *Actor) handleDevGuide(_ actor.PureContext, req gen.AppManagerDevGuideReq) (gen.AppManagerDevGuideResp, error) {
	guide := pluginDevGuide()
	topic := strings.ToLower(req.Topic)
	switch topic {
	case "prerequisites":
		return gen.AppManagerDevGuideResp{Prerequisites: guide.Prerequisites}, nil
	case "workflow":
		return gen.AppManagerDevGuideResp{Workflow: guide.Workflow}, nil
	case "host_api":
		return gen.AppManagerDevGuideResp{HostAPI: guide.HostAPI}, nil
	case "security":
		return gen.AppManagerDevGuideResp{Security: guide.Security}, nil
	case "errors":
		return gen.AppManagerDevGuideResp{CommonErrors: guide.CommonErrors}, nil
	default:
		return guide, nil
	}
}

func pluginDevCallableInfo() []gen.AppManagerCallableInfoEntry {
	return []gen.AppManagerCallableInfoEntry{
		{
			ID:          "appmanager.list",
			Service:     "appmanager",
			Description: "List all registered apps with their status, runtime, and binding info. Read-only; use to discover what is currently loaded.",
			Effect:      "none",
			Params:      nil,
		},
		{
			ID:          "appmanager.get",
			Service:     "appmanager",
			Description: "Get detailed status of a single app by ID, including artifact path/hash, package hash, callable list, and binding state.",
			Effect:      "none",
			Params: []gen.AppManagerCallableParam{
				{Name: "Id", Type: "string", Required: true, Description: "App ID to query"},
			},
		},
		{
			ID:          "appmanager.invoke",
			Service:     "appmanager",
			Description: "Invoke a callable on a registered app. The payload is opaque bytes routed through the app's runtime (native ABI or spore script). Identity is resolved from the caller context, not from the request body.",
			Effect:      "none",
			Params: []gen.AppManagerCallableParam{
				{Name: "Id", Type: "string", Required: true, Description: "App ID to invoke"},
				{Name: "Callable", Type: "string", Required: true, Description: "Callable ID declared in the app manifest"},
				{Name: "Payload", Type: "bytes", Required: true, Description: "Opaque request payload (JSON-encoded for spore apps, ABI-framed for native)"},
			},
		},
		{
			ID:          "appmanager.register_project",
			Service:     "appmanager",
			Description: "Register a plugin from a project source. Compiles the project (a standalone executable under the subprocess dev default; a c-shared library for release inprocess builds), validates the manifest/ABI/hash/trust, loads the artifact, and commits to the registry. Admin-only. REPLACES any existing app with the same ID: an unchanged artifact is an idempotent refresh, a rebuilt one auto-unloads the old artifact first (subprocess: clean swap; in-process: defers to restart_pending until the host restarts); the generation bumps and old session tokens are invalidated. If registration fails after artifact load, the handler is unloaded and the registry is rolled back.",
			Effect:      "irreversible",
			Params: []gen.AppManagerCallableParam{
				{Name: "ProjectId", Type: "string", Required: false, Description: "Project ID; if empty uses the caller's bound project"},
				{Name: "AppId", Type: "string", Required: false, Description: "Optional explicit app ID; defaults to manifest ID"},
				{Name: "EntryModule", Type: "string", Required: false, Description: "Optional entry module path; defaults to main.gen.go"},
				{Name: "AppDir", Type: "string", Required: false, Description: "Optional subdirectory holding the app, relative to the project root (e.g. \"plugin-dev-example\"); \".\" or empty = project root; rejects \"..\" and absolute paths"},
			},
		},
		{
			ID:          "appmanager.reload_project",
			Service:     "appmanager",
			Description: "Hot-reload a plugin from project source. Rebuilds the artifact, validates, prepares the new handler, and atomically swaps it in. If the new artifact fails prepare/commit, the old handler is preserved. Admin-only.",
			Effect:      "irreversible",
			Params: []gen.AppManagerCallableParam{
				{Name: "ProjectId", Type: "string", Required: false, Description: "Project ID; if empty uses the caller's bound project"},
				{Name: "AppId", Type: "string", Required: true, Description: "App ID to reload"},
				{Name: "EntryModule", Type: "string", Required: false, Description: "Optional entry module path override"},
				{Name: "ExpectedStateVersion", Type: "long", Required: false, Description: "Optimistic concurrency on app state version"},
				{Name: "AppDir", Type: "string", Required: false, Description: "Optional subdirectory holding the app, relative to the project root (e.g. \"plugin-dev-example\"); \".\" or empty = project root; rejects \"..\" and absolute paths"},
			},
		},
		{
			ID:          "appmanager.unregister",
			Service:     "appmanager",
			Description: "Unregister and unload an app. Unloads the runtime handler, removes protocol/session/binding/record entries. If unload fails, the app enters unload_failed state and its record is preserved for retry_cleanup. Admin-only.",
			Effect:      "irreversible",
			Params: []gen.AppManagerCallableParam{
				{Name: "Id", Type: "string", Required: true, Description: "App ID to unregister"},
			},
		},
		{
			ID:          "appmanager.retry_cleanup",
			Service:     "appmanager",
			Description: "Retry cleanup for an app stuck in unload_failed or cleanup_pending state. Idempotent: resumes from whichever half (unload or protocol cleanup) was pending. Irreversible effect; requires confirmation under the default permission mode.",
			Effect:      "irreversible",
			Params: []gen.AppManagerCallableParam{
				{Name: "Id", Type: "string", Required: true, Description: "App ID to retry cleanup for"},
			},
		},
		{
			ID:          "appmanager.plugin_load",
			Service:     "appmanager",
			Description: "Load a registered plugin's artifact back into the plugin host and mark it running. Native-only; idempotent (loading an already-running app is a no-op). Does not recompile: reinstalls the artifact recorded at registration. The stopped state does not survive a host restart — pluginhost restore loads every persisted artifact, so after a restart the app is running again.",
			Effect:      "none",
			Params: []gen.AppManagerCallableParam{
				{Name: "Id", Type: "string", Required: true, Description: "App ID to load"},
			},
		},
		{
			ID:          "appmanager.plugin_unload",
			Service:     "appmanager",
			Description: "Stop a registered plugin: unloads the runtime handler from the plugin host and marks the record stopped. Native-only; idempotent (unloading a non-running app is a no-op). Keeps the app record (unlike unregister), so the app can be restarted later with appmanager.plugin_load. Stopped state does not survive a host restart.",
			Effect:      "none",
			Params: []gen.AppManagerCallableParam{
				{Name: "Id", Type: "string", Required: true, Description: "App ID to unload"},
			},
		},
		{
			ID:          "pluginhost.list_plugins",
			Service:     "pluginhost",
			Description: "List all loaded native plugins with their artifact path, hash, ABI version, and handler status. Read-only diagnostic callable for inspecting the PluginHost runtime state.",
			Effect:      "none",
			Params:      nil,
		},
		{
			ID:          "pluginhost.plugin_logs",
			Service:     "pluginhost",
			Description: "Query one app's merged log ring (512 entries): handler sdk.Log output and stderr (Source=backend) plus the app frontend's console.log/warn/error/debug, window.onerror and unhandledrejection forwarded by the plugin iframe bridge (Source=frontend). Also reports the live process verdict (running/stopped/crashed with crash cause). First diagnostic tool when an app misbehaves.",
			Effect:      "none",
			Params: []gen.AppManagerCallableParam{
				{Name: "PluginId", Type: "string", Required: true, Description: "Plugin ID to query"},
				{Name: "Limit", Type: "int", Required: false, Description: "Max entries to return (default 200)"},
			},
		},
		{
			ID:          "pluginhost.plugin_dom",
			Service:     "pluginhost",
			Description: "Bounded DOM snapshot of a mounted plugin view: the host webview asks the plugin iframe to serialize its DOM through the bridge port and polls for the push (short budget). Returns Found/Snapshot/CapturedAt, or Found=false with Reason (plugin unknown, no panel mounted, or snapshot timed out). Requires a mounted panel — call appmanager.open_view first. The snapshot is a structural text outline (truncated at source ~32KB), not pixels or computed styles.",
			Effect:      "none",
			Params: []gen.AppManagerCallableParam{
				{Name: "PluginId", Type: "string", Required: true, Description: "Plugin ID to snapshot"},
			},
		},
		{
			ID:          "appmanager.callable_info",
			Service:     "appmanager",
			Description: "Query interface descriptions (IDs, parameters, effects, usage) for callables in the plugin-dev surface. Read-only self-discovery callable.",
			Effect:      "none",
			Params: []gen.AppManagerCallableParam{
				{Name: "Query", Type: "string", Required: false, Description: "Substring filter against callable ID or description"},
			},
		},
		{
			ID:          "appmanager.dev_guide",
			Service:     "appmanager",
			Description: "Return a structured Plugin development guide: prerequisites, appdef→generate→implement→gate→register workflow, host-call reference (plugin-side ctx.Host() API), security constraints, and common errors. Read-only knowledge callable.",
			Effect:      "none",
			Params: []gen.AppManagerCallableParam{
				{Name: "Topic", Type: "string", Required: false, Description: "Filter: 'prerequisites', 'workflow', 'host_api', 'security', 'errors'; empty returns all"},
			},
		},
		{
			ID:          "appmanager.host_protocol",
			Service:     "appmanager",
			Description: "Query the plugin-reachable host capability surface: every grantable capability (fs.read, llm.invoke, shell.exec, ssh.invoke, registry.read, ...) with title/description/risk level, the SDK host callIDs it gates (fs.read -> project.read_file, llm.invoke -> llm.complete/llm.chat, ssh.invoke -> sshmanager.*, registry.read -> registry.query), and the request/response type names behind each call. Read-only discovery for the declare-then-extract host-call flow: query this, declare the host callIDs you will invoke in .appdef permissions: (e.g. [\"llm.complete\", \"state.get\", \"registry.query\"]), and dev_generate derives the capabilities and emits hostproto.gen.go — wire payload types plus typed CallXxx/StreamXxx callers for exactly the declared callIDs.",
			Effect:      "none",
			Params: []gen.AppManagerCallableParam{
				{Name: "Query", Type: "string", Required: false, Description: "Substring filter across capability id/title/description/callIDs"},
				{Name: "Capability", Type: "string", Required: false, Description: "Exact capability ID filter (e.g. \"fs.read\")"},
			},
		},
		{
			ID:          "appmanager.dev_generate",
			Service:     "appmanager",
			Description: "Read the .appdef declaration in the app directory and generate the seven plugin artifacts (main.gen.go, main_run.gen.go, server.gen.go, handlers.go, app.manifest.json, schemas_gen.go, client.gen.ts) plus app.descriptors.json and go.mod (hostproto.gen.go additionally when permissions: are declared). Irreversible; a worktree-bound caller is routed to its bound worktree (otherwise operates on the main tree).",
			Effect:      "irreversible",
			Params: []gen.AppManagerCallableParam{
				{Name: "ProjectId", Type: "string", Required: false, Description: "Project ID; if empty uses the caller's bound project (agent callers should omit it — do not transcribe 32-hex ids from prompt text)"},
				{Name: "AppDir", Type: "string", Required: false, Description: "Project-root-relative subdirectory holding .appdef (e.g. \"plugin-dev-example\"). \".\" or empty = project root. \"..\" and absolute paths are rejected. Pass the same AppDir to every dev callable for a subdirectory app."},
				{Name: "Template", Type: "bool", Required: false, Description: "When true, scaffold the default template into a directory that has no .appdef. Only valid for a directory containing no go.mod and no *.go; otherwise refused. Generates the initial .appdef and the seven artifacts (including server.gen.go) in one step."},
			},
		},
		{
			ID:          "appmanager.dev_gate",
			Service:     "appmanager",
			Description: "Run the five registration gates (build, stub-filling, manifest-consistency, coverage, vendor-freshness) server-side for a plugin project. Diagnostic-only: does not register the app. Temporarily builds and loads the artifact to verify coverage, then unloads it.",
			Effect:      "none",
			Params: []gen.AppManagerCallableParam{
				{Name: "ProjectId", Type: "string", Required: false, Description: "Project ID; if empty uses the caller's bound project"},
				{Name: "AppDir", Type: "string", Required: false, Description: "Optional subdirectory holding the app, relative to the project root; \".\" or empty = project root; rejects \"..\" and absolute paths"},
			},
		},
		{
			ID:          "appmanager.sdk_vendor",
			Service:     "appmanager",
			Description: "Extract a self-contained copy of the plugin SDK into the app directory (vendor-sdk/) and re-point go.mod at it. Every dev_generate vendors automatically now; this callable remains for refreshing a stale vendored copy without regenerating (e.g. after a host SDK upgrade changed SDK signatures) — re-running replaces vendor-sdk with the current host SDK. Idempotent. The vendored SDK is dependency-free (purego only), so the app builds with zero host-machine dependencies.",
			Effect:      "irreversible",
			Params: []gen.AppManagerCallableParam{
				{Name: "ProjectId", Type: "string", Required: false, Description: "Project ID; if empty uses the caller's bound project"},
				{Name: "AppDir", Type: "string", Required: false, Description: "Optional subdirectory holding the app, relative to the project root; \".\" or empty = project root; rejects \"..\" and absolute paths"},
			},
		},
		{
			ID:          "appmanager.install_local",
			Service:     "appmanager",
			Description: "Install an app from a local package: Path (a .zip package or a package directory with app.manifest.json at its root) or PackageData (the raw zip bytes). When PACKAGE.sig/PACKAGE.pub are present the Ed25519 signature is verified over the recomputed canonical package hash — a tampered package is rejected before any load. The verified install is then KEPT in the package store instead of discarded: the zip bytes are persisted content-addressed under <DataDir>/.actors/appmanager/packages/<appID>-<PackageHash>.zip (raw bytes, PACKAGE.sig preserved) and the record's PackagePath points at that stable path; for runtime=native the artifact is extracted to <DataDir>/.actors/appmanager/artifacts/<appID>-<ArtifactHash><ext> and ArtifactPath records it — no OS temp dependency. Installing the same app ID again from a different zip is an UPDATE: the new zip/artifact is staged first, the record pointer swaps only after the new registration commits, then the superseded zip/artifact files are garbage-collected (identical bytes are a content-addressed no-op) and the old→new package hash swap is audited. unregister cascades the stored package and artifact away with the record; a project-origin register/reload over a zip-installed app clears the inventory and PackagePath (authority returns to the project directory). If a native artifact file is missing after a host restart, appmanager re-extracts it from the stored package zip before the app starts (self-heal). Irreversible; AdminOnly.",
			Effect:      "irreversible",
			Params: []gen.AppManagerCallableParam{
				{Name: "Path", Type: "string", Required: false, Description: "Install source on the host: a .zip package file or a package directory containing app.manifest.json; exactly one of Path / PackageData"},
				{Name: "PackageData", Type: "bytes", Required: false, Description: "Raw zip bytes (e.g. read back from an app_export PackageData write); exactly one of Path / PackageData"},
			},
		},
	}
}

// capabilityQuickReference builds the "重要能力速览" (Important Capability Quick
// Reference) entry for the HostAPI section by iterating the authoritative
// CapabilityCatalog — one line per capability (ID, risk level, en-US title
// and description). Derived, never hand-written: the catalog is the single
// source of truth, so this text cannot drift from it.
func capabilityQuickReference() string {
	ids := make([]string, 0, len(appbinding.CapabilityCatalog))
	for id := range appbinding.CapabilityCatalog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	b.WriteString("重要能力速览 (Important Capability Quick Reference) — every grantable host capability, sorted by ID, with risk level and one-line purpose (derived from the CapabilityCatalog; always authoritative): ")
	for i, id := range ids {
		if i > 0 {
			b.WriteString("; ")
		}
		c := appbinding.CapabilityCatalog[id]
		fmt.Fprintf(&b, "%s (%s): %s — %s", id, c.RiskLevel, c.Locales["en-US"].Title, c.Locales["en-US"].Description)
	}
	b.WriteString(".")
	return b.String()
}

func pluginDevGuide() gen.AppManagerDevGuideResp {
	return gen.AppManagerDevGuideResp{
		Prerequisites: []string{
			"Go toolchain on PATH — before starting the dev workflow run `go version` once: generated apps pin the SDK's Go directive (currently 1.25.0, see the generated go.mod); with the default GOTOOLCHAIN=auto any Go >= 1.21 self-upgrades, otherwise install/upgrade the latest Go from https://go.dev/dl/ first. A missing toolchain fails late at build time (\"Go toolchain is not available on PATH\"). The dev subprocess build compiles with CGO disabled; release inprocess c-shared builds additionally require CGO_ENABLED=1 and a C compiler (gcc/clang)",
			"sporemind-plugin-sdk: dev hosts resolve it from the in-repo checkout (./sporemind-plugin-sdk, kept local so plugin development never needs to pull the whole sporemind repository); release binaries (and `make dev-release` desktop builds) embed the packed SDK source (pkg/codegen/sdk.zip, built by `make build-sdk-asset`) and extract it to a user-level cache on first use — no checkout needed on user machines",
			"The workspace actor must be running and the project must be open",
			"dev_generate / dev_gate / register_project / retry_cleanup are agent-callable (Public) gated by effect confirmation: irreversible ops require user approval under the default permission mode. reload_project and unregister carry admin-visibility in codegen exports but no extra runtime ACL — agents can invoke them too (observed live in the external TOTP run); the effect-confirmation gate is the enforcement layer",
			"dev_generate / dev_gate / register_project read and write the project tree: a worktree-bound caller's operations are routed to the caller's bound worktree (otherwise the main tree), so plugin dev works from inside a worktree too",
			"Runtime=native with a non-empty Signer and Isolation=inprocess or subprocess. The dev flow defaults to the subprocess transport: the plugin is built as a standalone executable and talks to the host over a stdin/stdout frame protocol (plugin crashes do not kill the host, hot reload is kill+respawn, and the 30s invoke timeout is interruptible). Both transports are available on every host (dev or prod, first or third party): the manifest's declared Isolation is the primary transport signal — a plugin declaring Isolation=subprocess loads via the subprocess transport on a production host too, not only in dev; an undeclared isolation on a non-dev host defaults to inprocess. TrustClass=third_party is allowed and forces the subprocess transport (its artifact must be an executable, never a shared library). Isolation=process/sandbox remain NO-GO (unimplemented, distinct from subprocess).",
		},
		Workflow: []gen.AppManagerDevGuideStep{
			{
				Order:  0,
				Title:  "Discover existing bundles before building",
				Detail: "Before writing a new .appdef, search whether an already-registered app's bundle covers the requirement — an app's bundle, once registered, projects as a virtual component any agent can mount, no rebuild needed. Chain: (1) appmanager.list — what apps are registered and in what state; (2) appmanager.component_list — what app bundles are projected and available to mount right now; (3) appmanager.component_get {CardID} — inspect a specific bundle's declared tools, title, icon, and dependencies before mounting; (4) appmanager.invoke {Id, Callable, Payload} — test-drive a callable directly to see if its output fits, without mounting. If a bundle fits: mount it via the agent's component_mount callable (card id app-bundle:{appID}:{bundleName}), or add it to the agent kind config's DefaultBundleIDs. Only proceed to Step 1 (write .appdef) when no existing bundle covers the requirement.",
				Tools:  []string{"appmanager.list", "appmanager.component_list", "appmanager.component_get", "appmanager.invoke"},
			},
			{
				Order:  1,
				Title:  "Write/edit .appdef",
				Detail: "NEW APP — template-first bootstrap (recommended): create a fresh empty subdirectory for the app (it must contain no go.mod and no *.go), then call appmanager.dev_generate with AppDir=<subdir> and Template=true. This scaffolds the canonical template — app.appdef demonstrating named-struct request/response references, the `optional` prefix modifier, struct blocks and the entrypoint syntax, plus handlers.go showing the typed handler loop against schemas_gen.go — buildable and registerable as-is. READ the scaffolded files first: they are the authoritative syntax reference (prose descriptions of the format have drifted before), then rewrite app.appdef for your app and re-run dev_generate. Hand-writing .appdef from scratch is also valid for apps that already fit a known pattern. In both cases the .appdef is the sole declaration file the agent edits — it defines bundles, callables (request/response schemas, effect, toolName, service, timeout, expose, watch), entrypoints, events, permissions, and agent reachability via the optional free_agent { allow_create/allow_switch/allow_message/agent_kinds } block (user-facing agent actions: create/switch/message) and the optional plugin_agent [name] { display_name / system_prompt / bundles } block (a dedicated workspace-level plugin-kind agent auto-provisioned at register, one agent per (app, slot), removed at unregister; the unnamed block binds the \"default\" slot, a named block like plugin_agent reviewer { ... } binds that slot — [a-z][a-z0-9-]*, unique per app, several blocks allowed; bundles lists extra component card ids — e.g. \"builtin:bundle:web-search\" — the agent mounts beyond its own app-bundle:{appID}:{bundleName} cards; system_prompt is capped at 8192 bytes per block). Without a free_agent block the app still registers and invokes normally, but its agent actions (create/switch/message) are denied. Callable exposure fields: `expose: frontend|agent|both` (own line inside the callable block; empty defaults to both) controls which consumer surfaces may call the callable — expose: agent callables are LLM-tool-facing only and are intentionally NOT served over the plugin's HTTP listener (generated server.gen.go and client.gen.ts skip them, and /invoke answers 404 for them); expose: frontend callables are filtered out of the agent tool registry at projection time. `watch: [event_id, ...]` (own line) declares the events whose emission invalidates this callable's cached results — every entry must reference an event declared in the same .appdef (dangling reference = validation error); it drives the generated useXxxList() hooks in client.gen.ts (immediate fetch + refetch on watched SSE events), and the manifest_consistency gate flags Watch drift. Field type syntax: scalars (string, bool, int, int32, int64, long, float — Go float32, double — Go float64, bytes, any), struct references by name, and the generic forms array<T> and map<K,V> — Go-style []T slices and Rust-style f32/f64/int casts are NOT valid: an unknown scalar now fails validation at dev_generate with the field path and the supported list instead of silently generating interface{}. `optional` is a PREFIX modifier on the field line (`optional issuer string`), not a suffix after the type; the suffix form shifts the modifier onto the next field or fails at struct end. Request/response schemas attach to a callable via named-struct reference (`request: ProvisionRequest` / `response: ProvisionResponse` on their own lines inside the callable block) with the struct declared as a top-level `struct Name { ... }` block — inline request/response blocks inside the callable are silently dropped. All entrypoint, event, and struct declarations must live INSIDE the app { } block: top-level entrypoint/event blocks outside it are silently ignored (the manifest then carries null Entrypoints/Events) — this is a data-loss trap, check the generated app.manifest.json after dev_generate. Entrypoint syntax: `entrypoint view <id> { title: <label>; route: index.html }` (kind is the block label, the id follows it). Events are first-class on the gateway data path: a handler (or a generated stub) calls sdk.EmitEvent, which pushes to every frontend connected to the plugin's own HTTP listener over SSE (GET /events) AND forwards to the host bus via the app.emit host call for validation and cross-app visibility — see step 8 and the HostAPI section. To RECEIVE host events, declare `listen <kind> { }` blocks (phase 1: empty body — unknown fields are parse errors): kind must be a subscribable host event, currently `app_lifecycle` (any app registers/reloads/unloads/fails; payload AppLifecycleEvent) and `app_event` (generic envelope AppEventMessage). dev_generate validates kinds against the host event catalog and writes them into the manifest Listens; the runtime pluginhost fans matching gospore bus events into the plugin's OnXxx handlers (best-effort delivery, bounded by the invoke timeout — keep event handler bodies fast and non-blocking). Required meta fields: the app block MUST set all four of id, name, version, and namespace — dev_generate validates them up front and rejects a missing one (namespace is a short lowercase slug, e.g. namespace: \"totp\") — and a callable id must match [A-Za-z_][A-Za-z0-9_]* (dots are rejected). Callable timeout: declare `timeout: 300s` (or `timeout_ms: 300000`) on its own line inside the callable block to raise the per-invoke budget above the 30s default — valid range 30s..15min. Use this for LLM-type callables whose handler calls sdk llm.complete/llm.chat through the host bridge: reasoning models can take 60-90s+ before the first token, which exceeds the 30s default and fails with a timeout error; declare timeout: 300s (or as high as needed) for them. The declared value lands in the manifest's per-callable TimeoutMs and propagates through the whole invoke chain (appmanager → pluginhost → reverse bridge calls), so a slow LLM stream inside the handler runs under the same budget instead of being cut at the old hardcoded 30s. Callables without the declaration keep the 30s default. Use project.read / project.write / project.edit from the file-tools bundle.",
				Tools:  []string{"project.read", "project.write", "project.edit"},
			},
			{
				Order:  2,
				Title:  "Generate",
				Detail: "Run appmanager.dev_generate to read .appdef and produce the artifacts: main.gen.go (callable registration, fully generated, cgo-free), main_run.gen.go (subprocess entry with //go:build !cgo that calls sdk.RunProcess() and ALSO starts the plugin HTTP listener via sdk.ServeHTTP(\"127.0.0.1:0\") — the gateway data-path backend the host proxies /plugin/{id} traffic to: static files, POST /invoke/{id}, GET /events SSE, parallel to the frame protocol), server.gen.go (HTTP dispatch table behind the gateway data path: one sdk.RegisterHTTPHandler per frontend-exposed callable — expose: frontend|both — registered in package init; expose: agent callables are intentionally not served), handlers.go (compiling stubs, created once / append-only: zero-value responses with same-named request fields echoed, and an effect: \"mutate\" callable that declares watch events auto-emits each watched event via sdk.EmitEvent with a nil payload TODO — no ErrNotImplemented placeholders), app.manifest.json (generated, write-protected), schemas_gen.go (Go struct stubs), client.gen.ts (HTTP client for the gateway data path: an async appBase() gate that awaits window.__sporemindAppBaseReady before building any URL, typed invoke wrappers over fetch POST {base}/invoke/{id}, one shared auto-reconnecting WebSocket event channel on {base}/events with typed on<Event>(cb) subscriptions returning an unsubscribe function (WS-first: browser HTTP/1.1 pools cap at 6 connections per host and every panel shares the gateway origin, so the old per-panel SSE stream starved transient invokes; capped exponential-backoff reconnect, SSE fallback on refused handshakes), framework-agnostic useXxxList() hooks for watch-declared callables — immediate fetch + refetch on watched events — and uploadFile/streamVideo helpers for HTTP-native binary media), plus app.descriptors.json (schema descriptors for protocol registration and LLM tool schemas, written when every callable schema resolves to a declared struct) and go.mod. Host calls: any permissions: entries must name the host callIDs you will invoke (query appmanager.host_protocol first) — capability strings like \"llm.invoke\" are rejected with a hint, and an unknown callID fails generation with zero writes. Capabilities are derived per callID (llm.complete/llm.chat → llm.invoke, state.* → app.state, ...) into the manifest. When permissions are declared, dev_generate also emits hostproto.gen.go — wire payload structs plus typed CallXxx (and StreamXxx for llm.*) host-call callers for exactly the declared callIDs, write-protected like the other generated artifacts. Every generate vendors the SDK into vendor-sdk/ (go.mod replace points at ./vendor-sdk), making every app self-contained — it builds on machines with no sporemind checkout; regeneration refreshes the vendored copy so host SDK upgrades propagate. appmanager.sdk_vendor refreshes vendor-sdk without regenerating; when a go.mod already points at ./vendor-sdk but the directory is missing, dev_generate re-materializes it from the host SDK. For a subdirectory app pass AppDir=\"<subdir>\"; artifacts land inside that subdirectory and are reported root-relative. ProjectID is optional for agent callers: when omitted, the project resolves from the calling agent's binding (the turn engine injects the caller identity; do not transcribe 32-hex ids from prompt text — omit the field or copy it from a tool result verbatim). A directory without .appdef is NOT scaffolded implicitly: write the .appdef first, or pass Template=true (only for a directory with no go.mod and no *.go) to scaffold the minimal default template. The seven C symbols used by release inprocess loads (PluginManifest, PluginOnLoad, PluginOnUnload, PluginOnConfigChange, PluginInvoke, PluginSetHostBridge, PluginLog) are not hand-written: the release build auto-generates them in a staged cgo shim (main_cgo.gen.go) and compiles with -buildmode=c-shared.",
				Tools:  []string{"appmanager.dev_generate"},
			},
			{
				Order:  3,
				Title:  "Implement handlers",
				Detail: "Edit only handlers.go (inside the app directory) to replace the generated stub bodies with real logic. The stubs already compile and run end to end — zero-value responses with same-named request fields echoed, plus sdk.EmitEvent announcements (nil payload) for each watched event on an effect: \"mutate\" callable — so the work is real logic and real event payloads, not filling ErrNotImplemented holes (the dev_gate stub_filling gate still rejects any leftover ErrNotImplemented). This is the agent's sole implementation work. All other generated files remain untouched. Use project.read / project.edit from the file-tools bundle. Handler signature is `func handleXxx(req sdk.Request) (sdk.Response, error)` — there is NO Context parameter. Inside a handler, reach host capabilities via `sdk.ActiveHost()` (returns the injected IPC host on the subprocess transport, or the FFI bridge host on c-shared); `ctx.Host()`/`ctx.Log` are only available inside the OnLoad closure. For logging inside handlers use the package-level `sdk.Log(sdk.LogLevelInfo, \"format %s\", arg)` — the FIRST argument is an INT CONSTANT (sdk.LogLevelDebug=0, LogLevelInfo=1, LogLevelWarn=2, LogLevelError=3), NOT a string (older docs said string; a string literal fails compilation there). Event handlers: `listen <kind>` blocks generate `func OnXxx(ev <PayloadType>) error` stubs (append-only like callable stubs) — the payload types live in hostproto.gen.go; fill them to react to host bus events. Keep event handlers fast: the pluginhost delivers them best-effort under the invoke timeout and a blocking body delays (or drops) subsequent deliveries.",
				Tools:  []string{"project.read", "project.edit"},
			},
			{
				Order:  4,
				Title:  "Gate self-check (optional)",
				Detail: "Run appmanager.dev_gate to execute the five registration gates server-side without registering the app: build (native compilation: a standalone executable under the subprocess dev default, a c-shared library for release inprocess builds), stub_filling (zero ErrNotImplemented), manifest_consistency (PluginManifest matches .appdef), coverage (every declared callable in dispatch table), vendor_freshness (vendored SDK stamp matches the host SDK digest; fix with appmanager.sdk_vendor). Each gate returns pass/fail with details. Pass the same AppDir as dev_generate for subdirectory apps.",
				Tools:  []string{"appmanager.dev_gate"},
			},
			{
				Order:  5,
				Title:  "Register (inline gates)",
				Detail: "Run appmanager.register_project to compile, validate manifest/ABI/hash/trust, load the artifact into PluginHost, and commit to the registry. The five gates are re-executed inline — any failure rejects registration. Re-registering an existing app ID REPLACES it: unchanged artifact = idempotent refresh, rebuilt artifact = old one is auto-unloaded first (no manual plugin_unload needed; in-process builds defer to restart_pending). On failure after artifact load, the handler is unloaded and the registry rolls back. Pass the same AppDir as dev_generate for subdirectory apps.",
				Tools:  []string{"appmanager.register_project"},
			},
			{
				Order:  6,
				Title:  "Hot reload (dev loop)",
				Detail: "After editing .appdef and/or handlers.go, call appmanager.reload_project to rebuild and atomically swap the artifact. Pass the SAME parameters as register_project — including AppDir for subdirectory apps (reload_project without AppDir looks for app.manifest.json at the project root and fails with a file-not-found hint). The old handler keeps serving until the new one commits; a failed reload rolls back. reload_project also accepts the optional ProjectId with the same caller-binding resolution as dev_generate — omit it.",
				Tools:  []string{"appmanager.reload_project"},
			},
			{
				Order:  7,
				Title:  "Expose tools via bundles (optional)",
				Detail: "Declare a bundle block in .appdef to group callables into an agent-facing tool surface: bundle <name> { title: <label>; description: <text>; icon: \"shield-check\"; tools: [callable1, callable2, ...] } (tools must reference callables declared in the same .appdef). description is NOT a UI blurb: when the bundle is mounted, the text is injected verbatim into the mounting agent's system prompt as the bundle's tool-usage guidance (tool_guidance section, compiled after all system bundles). Match the system bundle-card body format (reference: builtin:bundle:debug at pkg/agentkit/builtin/cards/bundle/debug.md): a one-paragraph purpose statement, then per-tool bullets '- **<toolName>** — when to call it, parameter semantics, side effects/constraints', then closing notes for preconditions if any. Markdown must be paragraphed: the .appdef value is single-line, so write \\n escapes (the parser unwraps them to real newlines; use \\n\\n for blank-line paragraph breaks) — never one dense line, never marketing copy. Bundles are VIRTUAL components: appmanager projects them on demand from the loaded registry (appmanager.component_list / component_get) — no wiki card is created, nothing is written to the project on register/reload/unregister. icon is optional: either a host icon-library name (query appmanager.icon_names first and use the returned Name verbatim) OR an image asset file name placed in the app directory root (e.g. \"icon.png\", \"logo.svg\") — supported formats: .svg, .png, .jpg, .jpeg, .webp, .gif, .ico, .bmp; the file is bundled as an asset and served at /plugin/{id}/<name>. Image icons are used as-is via <img> in the UI (lucide takes precedence only when the name does not end with a known image extension). Unknown or omitted icons fall back to the \"package\" default. dev_generate emits the bundle into app.manifest.json — never hand-edit the manifest. The tools reach an agent only through the projected bundle: mount it via the agent's component_mount callable (component id app-bundle:{appID}:{bundleName}), or list it in the agent kind config's DefaultBundleIDs. While mounted, the agent's app tools are exactly the union of mounted bundle callables; an unmounted bundle-declaring app contributes zero tools to that agent. Unregistering (or unloading) the app makes the projection disappear — a stale mount simply resolves to no tools. Apps without any bundle block expose zero agent tools: agent visibility is bundle-mount-only (fail-closed).",
				Tools:  []string{"appmanager.component_list", "component_mount"},
			},
			{
				Order:  8,
				Title:  "Frontend panel (optional)",
				Detail: "A declared view entrypoint (`entrypoint view <id> { title: \"...\"; route: \"index.html\" }` inside the app block) renders in a plugin panel iframe loaded through the HOST GATEWAY route /plugin/{id}/{route}?v={generation} (cache-busted per reload generation, HTML served Cache-Control: no-store); the gateway reverse-proxies every /plugin/{id} request to the plugin process's own HTTP listener with the per-app gateway token header (X-Gateway-Token, sdk.GatewayTokenHeader), and injects the bootstrap snippet (sdk.BridgeBootstrapSnippet) that resolves window.__sporemindAppBaseReady with the app base path — the panel never learns the subprocess origin or session secret, and the old multi-hop host bridge relay stays gone from the data path (management plane only). Non-source root files in the app directory (index.html, icon.png, styles.css) are still packaged as assets at register/reload time and served at /plugin/{id}/<name>, and the plugin process also serves the app directory via GET / on its own listener (what the gateway proxies). Routes on the plugin listener (sdk.ServeHTTP): POST /invoke/{callableId} — JSON body in, JSON response out (gateway-token-authed via the proxy; spore_session cookie for direct same-origin opens); GET /events — text/event-stream SSE; GET / — static files from the app dir, no CORS. The management plane (frontend console capture, DOM snapshot, openView, theme, locale) still rides the host plugin bridge — data never does. Listener startup: main_run.gen.go starts sdk.ServeHTTP(\"127.0.0.1:0\") immediately (ephemeral port, logged), and when the host pushes an OnLoad config JSON ({\"httpAddr\": \"127.0.0.1:0\", \"sessionSecret\": \"<hex>\"}) the SDK auto-starts after the app's OnLoad and reports the bound address back as {\"httpAddr\": \"<bound>\"} so the gateway can point its proxy at it; ServeHTTP is idempotent while running, so the two startup paths never double-bind. Frontend SDK: dev_generate's client.gen.ts — an async appBase() gate (awaits window.__sporemindAppBaseReady before building any URL, closing the first-fetch 404 race), typed invoke wrappers (fetch POST {base}/invoke/{bare-callable-name} — the bare id as declared in .appdef; expose: agent callables answer 404 here), one shared auto-reconnecting WebSocket event channel on GET /events with typed on<Event>(cb) subscriptions (each returns an unsubscribe function; WS-first because browser HTTP/1.1 pools cap at 6 connections per host and every panel iframe shares the gateway origin — the old per-panel SSE streams exhausted the pool and stalled all invokes; capped exponential-backoff reconnect with jitter, SSE fallback when the handshake is refused), framework-agnostic useXxxList() hooks for watch-declared callables (immediate fetch + refetch on watched events), and uploadFile/streamVideo helpers. Streaming callables degrade to unary over HTTP: intermediate chunks are dropped and only the terminal response returns — model progress with app events (on<Event>) instead. Events: handler-side sdk.EmitEvent (or the generated Emit<ID> wrapper) fans out to every connected frontend over the event channel (WS-first, SSE fallback) locally (real-time, no host round-trip) AND forwards to the host bus via the app.emit host call for manifest validation and cross-app visibility. Cookie auth (direct same-origin opens of the listener): /invoke and /events require the spore_session cookie (HttpOnly, SameSite=Lax) whose value is sessionId.hex(HMAC-SHA256(sessionSecret, sessionId)); the per-instance sessionSecret reaches the plugin only via the host-pushed OnLoad config, and an empty secret disables auth (dev mode). Same-origin cookies mean <video>/<audio>/<img> and fetch carry it automatically. Media: no base64 and no binary codec on this path — plain JSON for invoke/event payloads, and HTTP-native binary for large media: upload as the raw request body (client uploadFile helper: fetch POST, or XHR when upload progress is needed), play via plain same-origin <video src>/<audio src>/<img src> URLs, or streamVideo(url, videoEl, mimeType) for chunked playback through Media Source Extensions. Theme: the management bridge still delivers theme updates; key plugin CSS off :root[data-theme=\"dark\"] / :root[data-theme=\"light\"] with a @media (prefers-color-scheme: light) :root:not([data-theme]) fallback for standalone browser opens. Locale: the same bridge delivers the host's active locale (BCP47 tag such as zh-CN) on <html lang> — initially via the bootstrap payload, live via sporemind:locale-update when the user switches language. Read it with window.sporemind.locale and track changes with a MutationObserver on the lang attribute (standalone browser opens fall back to navigator.language). File drop: OS files dragged onto the panel are claimed by the SDK bootstrap snippet (mark plugin-owned dropzones with [data-file-drop-target] to keep native HTML5 DnD) — the panel frontend gets a cancelable sporemind:file-drop DOM event whose detail.entries carry live File objects (preventDefault = handled in the frontend, backend upload skipped), and the Go backend receives the completed drop — flat entries with bytes in memory (dirs-before-children, '/'-separated relPath, raw-body uploads, no base64) — through sdk.RegisterFileDropListener(func([]sdk.FileDropEntry) error); registration is the opt-in (while absent no bytes are uploaded), and after delivery the metadata is mirrored as the file_drop event on the panel event channel. Drags from the HOST FILE PANELS (local FileBrowser, remote SSH FTP) are DOM drags without Files: the snippet recognizes the host x-sporemind-dnd: text/plain marker and delivers the same event (detail.origin \"host-browser\") plus a metadata-only /__sdk/file-drop/host POST — entries carry Path (project-relative + ProjectID for local, absolute + SessionID for remote) with nil Data, so the plugin opens them by reference instead of receiving bytes.",
				Tools:  []string{"project.read", "project.write", "project.edit"},
			},
			{
				Order:  9,
				Title:  "Lifecycle semantics (transport matrix)",
				Detail: "Once registered, a plugin is driven by appmanager.register_project (install from source), appmanager.reload_project (swap artifact), appmanager.plugin_unload / appmanager.plugin_load (stop/start without unregistering), and appmanager.unregister (remove). Their immediacy depends on the transport declared in the manifest ABI. SUBPROCESS transport (default in dev; every third-party plugin): all five operations are immediate and never require a host restart — unload really stops the process, load respawns it, reload does a prepare+commit hot swap (kill+respawn), register/unregister are immediate. INPROCESS transport (c-shared release builds): a fresh register is immediate (running), but a loaded c-shared library cannot be swapped or unmapped while the host process lives (FreeLibrary/dlclose of a Go c-shared plugin crashes the host on Windows), so three operations become STAGED instead of failing: re-registering a different artifact, reloading to a different artifact, and loading an app that is unload-pending all succeed with the record in state restart_pending — the new artifact is persisted in the pluginhost ArtifactLoads and activates on the NEXT HOST RESTART (pluginhost OnStart restores and loads it; the appmanager spawn loop then cross-validates via an idempotent artifact_load and marks the app running only when the pluginhost physically holds the artifact). unload of an in-process plugin lands in unload_pending (the DLL stays mapped until the process exits; a restart clears it to stopped). State meanings: running (pluginhost confirmed), stopped (unloaded; subprocess can load again immediately, in-process needs a restart), unload_pending (in-process unload, restart to reclaim), restart_pending (new artifact persisted, activates on restart), failed / unload_failed (load or unload failure with a retryable diagnostic; plugin_unload / retry_cleanup retry). While a record is restart_pending the previous artifact keeps serving; after the restart, plugin_load is also a valid manual activation. appmanager.list / appmanager.get expose the current state; the pluginhost list at pluginhost.list_plugins shows the physically loaded side.",
				Tools:  []string{"appmanager.register_project", "appmanager.reload_project", "appmanager.plugin_load", "appmanager.plugin_unload", "appmanager.unregister", "appmanager.get", "pluginhost.list_plugins"},
			},
			{
				Order:  10,
				Title:  "Frontend debugging & observability",
				Detail: "The app frontend renders in a sandboxed plugin iframe that host-side eval cannot read directly, so observability goes through the plugin management bridge (DOM snapshot, console capture) and requires a mounted panel. ANTI-PATTERN: do NOT open the app's index.html via open_global_browser or the built-in browser — it loads from disk outside the gateway (no auth cookie, so API calls 401), runs no iframe bridge session (so plugin_logs shows zero frontend entries), and plugin_dom cannot see it. The ONLY path that produces observable frontend signal is through the gateway panel: reload_project first (or the live bundle still serves the pre-edit copy), then open_view. PROACTIVE INSTRUMENTATION: plugin_logs only shows frontend entries if you added console.log checkpoints in the suspect code path AND a panel is mounted — without checkpoints, there is nothing to read. Add console.log around data-fetch, render-decision, and error-branch sites before reloading, then read them back. Chain: (1) appmanager.open_view {Id} mounts the view panel — without it there is no iframe, no bridge session, and nothing to observe; (2) pluginhost.plugin_dom {PluginId} asks the iframe to serialize its DOM through the bridge port and returns a bounded structural outline (Found=true + Snapshot + CapturedAt; Found=false + Reason means plugin unknown, no panel mounted, or the ~3s snapshot budget expired — mount the panel and retry); the snapshot is tag tree with text/attributes truncated at source ~32KB, not pixels or computed styles; (3) pluginhost.plugin_logs {PluginId, Limit} reads the merged log ring — backend sdk.Log/stderr plus frontend console.log/warn/error/debug, window.onerror and unhandledrejection (Source=frontend), plus the process verdict — add console.log checkpoints to the suspect code path to trace values; (4) appmanager.invoke exercises the app's callables directly to isolate backend vs view faults; (5) appmanager.get confirms ArtifactHash/PackageHash changed after reload_project (an unchanged hash means the edit never shipped); (6) appmanager.panel_topology {Id} is the read-only runtime-topology diagnostic BEFORE any of the above: gateway base URL, the exact panel URL (/plugin/{id}/{route}?v={gen}), the plugin process's loopback listener, and which 401/404 on which hop is auth working vs misconfigured — use it instead of netstat port-scanning when a panel will not load at all. After a frontend fix: reload_project → confirm hash changed → open_view → exercise the flow → plugin_dom for rendered markup → plugin_logs for frontend entries. Rendering quality (layout, animation) remains a user check.",
				Tools:  []string{"appmanager.open_view", "pluginhost.plugin_dom", "pluginhost.plugin_logs", "appmanager.invoke", "appmanager.get", "appmanager.reload_project"},
			},
			{
				Order:  11,
				Title:  "Packaged distribution (export → install)",
				Detail: "To ship an app to another host instead of registering it in place, run appmanager.app_export to package the project app (manifest, modules, assets, plus abi.json + the native binary for runtime=native) into a signed zip — Ed25519 PACKAGE.sig/PACKAGE.pub over the canonical package hash (sporemind.app-package.v2). The response is {PackageData, PackageHash, PublicKey}: AppManager never writes export output to disk, so the caller persists PackageData itself (e.g. project.write). On the target host, appmanager.install_local with the zip (Path) or its bytes (PackageData) re-verifies the signature over the recomputed canonical hash — a tampered package is rejected before any load — then KEEPS the install in the package store: the zip is persisted content-addressed under <DataDir>/.actors/appmanager/packages/<appID>-<PackageHash>.zip (record field PackagePath), and a native artifact is extracted to <DataDir>/.actors/appmanager/artifacts/<appID>-<ArtifactHash><ext> (ArtifactPath) instead of an OS temp file. Reinstalling the same app ID from a newer zip is an in-place UPDATE (new files staged first, pointer swap only after the new registration commits, then old-file GC + an audited old→new package hash swap; identical bytes are a no-op). unregister cascades those files away; register_project/reload_project over a zip-installed app clears them (authority back to the project directory); a native artifact wiped out-of-band is re-extracted from the stored zip on the next restart (self-heal). Loop check: app_export on host A → transfer zip → install_local on host B → appmanager.invoke to confirm it runs. Unlike dev_generate/dev_gate/register_project, app_export and install_local do not touch the project tree at all, so they work identically for worktree-bound and unbound callers.",
				Tools:  []string{"appmanager.app_export", "appmanager.install_local", "appmanager.get", "appmanager.invoke"},
			},
		},
		HostAPI: []string{
			capabilityQuickReference(),
			"Inside a native handler the plugin reaches host capabilities through sdk.ActiveHost() — the SDK Host client. Inside the OnLoad closure, the equivalent is ctx.Host(). Handler signatures are `func handleXxx(req sdk.Request) (sdk.Response, error)` with NO Context parameter; do not write `ctx.Host()` inside a handler body. Logging inside handlers uses the package-level sdk.Log(level, format, ...) (not ctx.Log, which is OnLoad-only). The Host interface exposes only two primitives: Host().Invoke(callID, payload) ([]byte, error) and Host().InvokeStream(callID, payload, onChunk func([]byte) error) ([]byte, error) — there are no hand-written capability clients. For every callID declared in permissions:, hostproto.gen.go provides a typed caller generated from the same wire catalog — CallStateGet(host, StateKeyReq{...}) (StateGetResp, error), StreamLLMComplete(host, req, onChunk func(sdk.LLMChunk) error) (LLMResp, error), ... — InvokeStream + sdk.ForwardLLMChunks wire the LLM chunk decode; no hand-written decode. This topic is the complete plugin-side host-call reference; the same table lives in the native-plugin-builder skill.",
			"Host callIDs (payload shape → result): config.get {scope, key} → config bytes. llm.complete {prompt?, messages?, system?, model?, provider?, temperature?, top_p?, frequency_penalty?, presence_penalty?, reasoning_effort?, thinking_budget?, tools?: [{name, description?, input_schema?}], tool_choice?: \"auto\"|\"none\"|\"required\"|<tool name>|{type:function,function:{name}}} and llm.chat {same shape} are translated to aiaggregator.dispatch (a SendSessionMessageReq) and return a unified JSON {Text, Usage?, Reasoning?, ToolCalls?: [{Id?, Name, Arguments}]}. FORCED TOOL CALL: declare tools + tool_choice (a bare tool name forces that exact tool; \"required\" forces any tool) and the model must end with a tool call — the terminal ToolCalls carries the name and raw-JSON Arguments, which the PLUGIN executes itself (the host never dispatches plugin tools). NOTE: on DeepSeek hybrid-thinking models (deepseek-v*) a forced tool_choice with no reasoning_effort automatically disables thinking — the provider rejects forced tool_choice while thinking (HTTP 400 \"Thinking mode does not support this tool_choice\"); passing reasoning_effort alongside a forced tool_choice keeps both and surfaces the provider conflict. project.read_file {path} → file bytes; project.write_file {path, content} → writes a workspace file. provider.list {} / provider.get {id}; aggregator.list {} / aggregator.get {id} → read-only configuration JSON. state.get {key} → {Value: value bytes, Found: bool}; state.set {key, value} (value base64) / state.delete {key} → per-app key-value storage. app.emit {event, payload} → {Accepted: bool} — publishes one app-declared event (an `event` block in .appdef). sdk.EmitEvent delivers along TWO paths: (1) LOCAL SSE — the event is fanned out to every frontend connected to the plugin's own HTTP listener (GET /events), giving the panel real-time updates with no host round-trip; best-effort, a no-op when no listener is running. (2) HOST BRIDGE — the event is forwarded via the app.emit host call: the pluginhost injects the calling plugin's identity and hands it to appmanager.plugin_emit, which validates the event against the app manifest (undeclared events are rejected) and broadcasts on the app_event bus kind, so apps listening to app_event (including other apps) still receive it. A host-call failure returns an error; the local SSE push never affects the return value. registry.query {service?, callable?, limit?, cursor?} → {Items: [CallableMeta], NextCursor?} — case-insensitive substring discovery over the actor tree's callable metadata (Service filters the service name; Callable filters the full dotted callID AND the Description; both empty = the whole surface; Limit caps the page, Cursor continues it as a string offset). CallableMeta = CallID / Service / Kind / Params / FinalType / FinalFields / Permission / EffectKind / Description / ReqSchemaId / RespSchemaId — metadata only, no credential values; discovery ≠ invocation (calling a discovered callable still requires its declared permission). Served locally by the pluginhost; the SDK wrapper is sdk.ListCallables(sdk.ListCallablesQuery) (declare callID registry.query → capability registry.read). The plugin-dev-example's discover callable exercises this path end-to-end.",
			"STREAMING LLM: the generated StreamLLMComplete(host, req, onChunk func(sdk.LLMChunk) error) / StreamLLMChat stream llm.complete/llm.chat deltas via Host().InvokeStream(callID, req, sdk.ForwardLLMChunks(onChunk)). Same payload and same terminal {Text, Usage?, Reasoning?, ToolCalls?} as the unary CallLLMComplete. The 0x07 chunk wire shape is the GENERIC envelope {\"kind\": <kind>, \"data\": <JSON>} — the host routes it without interpreting either field; the streaming callID set is a host-side catalog (currently llm.complete/llm.chat), so a second streaming callable will ride the same path. The SDK decodes the envelope into sdk.LLMChunk{Kind, Text, Usage(raw JSON), Data(raw JSON for unknown kinds)}: \"text_delta\"/\"reasoning_delta\" carry {\"text\": \"...\"} in data (mapped to Text), \"usage\" carries the usage object in data (mapped to Usage), \"tool_use_start\"/\"tool_use_input_delta\"/\"tool_use_complete\" carry {tool_use_id, name, input|input_delta} in data (read via Data; completed calls are also aggregated into the terminal ToolCalls), \"stop\" carries {stop_reason} (\"tool_use\" = the model ended on a tool call); unknown kinds arrive with Data set and Text/Usage empty — ignore kinds you do not understand. Ordering is structural: chunks arrive in generation order, all before the terminal return. Returning an error from onChunk aborts consumption (the call still completes host-side; the error surfaces to the handler). Degrades transparently: on the inprocess/FFI transport or against a pre-streaming host there are ZERO intermediate chunks and the terminal arrives as the single delivery — the same code renders progressively when streaming and works unchanged when not. The reverse call inherits the invoking callable's budget (including a declared timeout: in .appdef) minus a fixed headroom; design slow-first-chunk models accordingly (declare timeout: 120s+ rather than relying on the 30s default).",
			"DIRECT-HTTP FRONTEND SURFACE (plugin-side SDK API; serves the panel directly, no host round-trip, no capability gate — it is the app's own listener): sdk.ServeHTTP(addr, ...ServeHTTPOption) starts the HTTP listener in a background goroutine, parallel to the stdin/stdout frame protocol; it binds synchronously and is idempotent while running (a second call returns the existing server), and Shutdown(ctx) stops it (also auto-called on unload so a reload never leaves a dangling listener). WithStaticDir(dir) overrides the served directory (default \".\" = the app dir). Routes: POST /invoke/{callableId} → the HTTPHandler (func(payload json.RawMessage) (any, error)) registered by generated server.gen.go via sdk.RegisterHTTPHandler(callableID, fn) / removed via sdk.UnregisterHTTPHandler; GET /events → the SSE hub fed by sdk.EmitEvent; GET / → static files. Generated main_run.gen.go starts ServeHTTP(\"127.0.0.1:0\") on boot, and the host-pushed OnLoad config (LoadConfig {httpAddr, sessionSecret}) auto-starts it after the app's OnLoad with the bound address reported back in the OnLoad response as {httpAddr: \"<bound>\"}. COOKIE AUTH: sdk.SetSessionSecret (auto-applied from LoadConfig.SessionSecret; hex or raw) sets the per-instance HMAC-SHA256 secret; sdk.MintSessionToken(secret, sessionID) = sessionId + \".\" + hex(HMAC) is the spore_session cookie value (HttpOnly, SameSite=Lax, so <video>/<audio>/<img> and same-origin fetch carry it automatically); an EMPTY secret disables auth (dev mode); otherwise /invoke and /events answer 401 without a valid cookie. WIRE FORMAT: plain JSON on this path — no base64, no binary codec; large media travels as HTTP-native binary (raw POST bodies via the generated uploadFile helper, <video src>/<audio src>/<img src> same-origin URLs, chunked + MSE via streamVideo). Streaming callables degrade to unary over HTTP (intermediate chunks dropped; model progress with SSE events). expose: agent callables are not registered on this listener (404).",
			"Unit selection via llm.complete/llm.chat: model + provider together form the ModelUnit — the ONLY selection primitive. BOTH set → pin that exact unit (must exist in the system aggregator pool; enumerate candidates first via aggregator.get {id} → Units[] and request aggregator.read alongside llm.invoke). NEITHER set → auto-pick with failover. model WITHOUT provider → rejected (naked-model selection is forbidden by matchUnits). The pin is SOFT: an unavailable unit (cooldown/disabled/token-exhausted) silently falls back to pool auto-selection — do not assume the requested model served the response. There is no hard-pin flag, no named-aggregator entry, and no SlotKind from the SDK bridge.",
			"Each host call is gated by a capability derived from its callID (dot-style catalog): llm.complete/llm.chat ⇒ llm.invoke; project.read_file ⇒ fs.read; project.write_file ⇒ fs.write; config.get ⇒ config.read; provider.list/get ⇒ provider.read; aggregator.list/get ⇒ aggregator.read; state.get/set/delete ⇒ app.state; app.emit ⇒ app.emit; registry.query ⇒ registry.read. Declaration IS authorization: the manifest's declared capability set is the sole runtime authority. Declare the CALLIDs you will invoke in the .appdef as a FIELD+LIST inside the app block (`permissions: [\"llm.complete\", \"state.get\", \"registry.query\"]`; there is NO permissions { … } block form, and capability strings like \"llm.invoke\" are rejected with a hint) — capabilities are derived, never declared. Exception: declaring `event` blocks derives the app.emit capability automatically (event publishing is part of the declared event surface), so events never need a permissions entry.",
			"app.state (state.get/set/delete, called via CallStateGet/CallStateSet/CallStateDelete or Host().Invoke) is the sanctioned key-value persistence path for plugins: the host injects the calling plugin's identity (the SDK client only passes key and value), storage is per-app, and it survives reload, plugin_unload→plugin_load, and host restarts. Do not persist app state by writing config/state JSON through project.write_file.",
			"app.data is the SECOND sanctioned persistence path, for bulk/binary/local-index data that does not fit the key-value quotas: declaring the app.data capability makes the host grant a private writable directory via the OnLoad config — sdk.DataDir() returns <appDir>/.sporecode/appdata (created by the host before load, per-app, never HTTP-served). The app owns that directory exclusively: write files, open embedded databases there (e.g. goleveldb under filepath.Join(sdk.DataDir(), \"db\") for ordered key-value with prefix scans). It survives unload/reload/re-register like app.state. Empty string = the capability was not declared (or the app has no derivable project app dir, e.g. zip installs): fail closed, do not guess a location.",
			"shell.exec, fs.read and fs.write are the only write/exec capabilities; provider.read and aggregator.read are read-only mirrors of host provider configuration — use them instead of asking the user to re-enter provider settings, and never copy provider credentials into app state.",
			"Host calls are synchronous with the same 30s invoke budget on both transports (subprocess kills+reaps an expired call; inprocess cannot preempt). Batch design accordingly: return quickly, chunk long work. EXCEPTION: dialog.openFile / dialog.openFolder (native file/folder picker) each register a 10min budget — the picker legitimately blocks until the user closes it.",
			"dialog.openFile {multiple?: bool, title?: string, filters?: [{displayName, pattern}]} → {paths: string[]} and dialog.openFolder {title?: string} → {paths: string[]} pop the desktop's NATIVE picker and return absolute paths (empty array = cancelled; addresses only, no content is read — combine with fs.read/app.data for content). dialog.openFile honors multiple and filters; dialog.openFolder is always single (Windows IFileDialog cannot multi-select folders). Headless builds (server/mobile) return an explicit no-native-picker error for both. Declare the \"dialog.openFile\" / \"dialog.openFolder\" capabilities in the appdef permissions to use them.",
		"clipboard.write {pngB64?: string, text?: string} → {} and clipboard.read {} → {hasImage, pngB64?, hasText, text?} talk to the DESKTOP's native clipboard (declare \"clipboard.write\" (low risk) / \"clipboard.read\" (medium risk — it reads whatever the user's clipboard holds) in the appdef permissions). clipboard.write needs at least one payload; when both are given the host writes image formats (registered PNG format + CF_DIB + CF_HDROP file entry) and CF_UNICODETEXT in ONE snapshot so paste targets pick their preferred format. clipboard.read normalizes the image to base64 PNG (PNG format passthrough, CF_DIB decoded; all-zero-alpha DIBs are treated as opaque) and prefers the image when both kinds are present — check hasImage first. Images are capped at 64MiB decoded; the host-call wire carries them base64 (frame-protocol JSON), while the panel data plane stays binary-only. Headless builds return an explicit no-native-clipboard error. Call via CallClipboardWrite / CallClipboardRead.",
			"workspace.list_projects {} → {Items: [{Name, Path, Root, ActorId?, Mounts?: [{Name, Path}], LastOpenedAt?, PermissionMode?, System?}]} lists the host workspace's MOUNTED projects (translated to workspace.list_project, the mount-table snapshot the AI shell sidebar uses). Use it to discover a project root to scan instead of guessing from the plugin cwd. System entries are the host's meta project — skip them for user-code mapping. Read-only: mounting stays AdminOnly host-side. Declare \"workspace.read\" (low risk). Call via CallWorkspaceListProjects.",
			"Error envelope (pinned live by the round-8 probe): a denied/failed host call returns {\"__host_error__\": \"…\"} and the SDK bridgeResult decodes it uniformly into a Go error prefixed `host invoke <callID>: …` on both transports. SDK StateClient.Get returns (value, found, error); on a missing key it returns (nil, false, nil). state.set carries value base64-encoded.",
			"Identity semantics on the plugin side: the Request.SessionId / RequestId fields are audit and correlation metadata only — authorization is established by the host before the plugin is invoked; a plugin must not re-derive trust from them. Same for AgentID/Role: resolved from the caller's actor context host-side, never from the request body.",
		},
		Security: []string{
			"Declaration IS authorization: the capabilities a manifest declares are the capabilities it can invoke at runtime — there is no host allowlist and no consent round-trip. Register-time validation rejects only unknown capability strings and callable/event/entrypoint permission refs that are not declared in the manifest's own Permissions block. Grantable capabilities include: llm.invoke, fs.read, fs.write, shell.exec, config.read, provider.read, aggregator.read, app.state (per-app key-value storage), app.data (per-app private writable data directory granted via the OnLoad config; no host callID backs it — declare it as the capability string \"app.data\" in the .appdef permissions list and read the granted path with sdk.DataDir()), app.emit (auto-derived from `event` blocks), registry.read (read-only query of the host app registry via registry.query), voice.stt / voice.tts (speech recognition / synthesis via voice.recognize / voice.synthesize), voice.read (redacted account enumeration via voice.accounts.list; recognize/synthesize accept an optional AccountId to override the kind's active account, subject to kind matching), media.read (modality-filtered generation-unit enumeration via media.list_units, plus redacted media-account enumeration via media.accounts.list — Kind filter image/video, HasAPIKey flag, never the key; the active-account default path of image/video.generate serves from these accounts), and image.gen / video.gen (media generation via image.generate / video.generate: the host resolves the unit — Provider+Model both set pins it strictly, both empty uses the kind's active media account then aggregator auto-pick, model-only is rejected — calls the provider itself so credentials never cross the bridge, persists the artifact under the app's media/ directory, and returns {Path, MimeType, SizeBytes, Provider, Model}; the panel loads the artifact at its relative Path as a same-origin URL; reference inputs are URLs/base64 only — bare local file paths are rejected; budgets: 2min image / 12min video), and agent.observe (high-risk, load-time like app.data: no host callID backs it — declaring \\\"agent.observe\\\" in the permissions list authorizes the pluginhost to deliver the agent event stream to `listen step { }` / `listen agent_message_received { }` blocks; step payloads carry full conversation content, hence the explicit opt-in; delivered events also mirror onto the plugin's own /events SSE so panels can subscribe with subscribeEvent / window.sporemind.subscribe).",
			"In the dev flow plugins run as standalone executables over a stdin/stdout frame protocol: a plugin crash, abort, or hang is contained in the child process and does not kill the host; hot reload is kill+respawn; an invoke exceeding the 30s timeout is interruptible (the plugin process is killed and reaped). Release inprocess builds run inside the host process, where a crash, abort, or non-cooperative hang still terminates or stalls the host and cannot be force-killed or preempted.",
			"The 30-second invoke timeout applies to both transports: over subprocess it is interruptible (an expired invoke kills and reaps the plugin process). Only the inprocess transport cannot interrupt a synchronous C call already executing inside the loaded library.",
			"Identity (AgentID, Role, ProjectID) is resolved from the caller's actor context, never from the request body. SessionToken (HMAC-signed) is the sole authorization credential for session-scoped invoke.",
			"TrustClass=third_party is allowed and forces the subprocess transport (crash isolation): a third-party plugin may never ship a shared-library artifact. Isolation=process/sandbox remain NO-GO: they are not implemented, and are distinct from subprocess, which is a supported transport.",
			"Frontend HTTP auth: the plugin's own /invoke and /events endpoints are gated by a per-instance HMAC session cookie (spore_session, HttpOnly, SameSite=Lax) minted from the host-pushed sessionSecret; an empty secret disables auth (dev mode). The secret reaches the plugin only through the host-pushed OnLoad config — never through manifest fields, callable payloads, or URLs.",
			"Do not put secrets in manifest fields, callable descriptions, or public components. API keys must not reach anonymous-role callables.",
		},
		CommonErrors: []gen.AppManagerDevGuideError{
			{
				Symptom: "handler body fails to compile with 'undefined: ctx' or 'cannot use ctx (no Context in scope)'",
				Cause:   "codegen handler stubs are `func handleXxx(req sdk.Request) (sdk.Response, error)` with NO Context parameter — the SDK Context only exists inside OnLoad/OnUnload/OnConfigChange closures",
				Remedy:  "Inside handlers reach the host via sdk.ActiveHost() (Host client, same sub-clients) and log via sdk.Log(level, format, ...) — both are package-level, no ctx needed",
			},
			{
				Symptom: "dev_generate fails with 'app block: missing namespace field (short lowercase slug, e.g. namespace: \"totp\")' (or missing id/name/version)",
				Cause:   "All four meta fields (id, name, version, namespace) are validated at dev_generate time — an appdef without them is rejected before any artifact is written",
				Remedy:  "Add the missing field(s) to the app { } block in .appdef — e.g. id: \"com.example.app\"; name: \"Example\"; version: \"0.1.0\"; namespace: \"example\"",
			},
			{
				Symptom: "dev_gate build gate (or register) fails with undefined types like PingRequest/PingResponse after rewriting .appdef, and dev_generate reported Warnings about orphan handlers",
				Cause:   "handlers.go is append-only: a handler whose callable was removed from .appdef survives regeneration and still references structs that are no longer generated in schemas_gen.go",
				Remedy:  "Delete the orphan handler functions named in the dev_generate Warnings from handlers.go (agent-owned file), then re-run dev_generate and the gates",
			},
			{
				Symptom: "dev_generate fails with 'no .appdef found in <dir>; pass Template to scaffold'",
				Cause:   "The target directory has no .appdef file, and template scaffolding now requires an explicit request (it once silently wrote six template files into the wrong directory)",
				Remedy:  "Write the .appdef first (step 1), or re-run dev_generate with Template=true — allowed only when the directory has no go.mod and no *.go files",
			},
			{
				Symptom: "dev_generate fails with 'template scaffold refused: <dir> contains ...'",
				Cause:   "Template=true was passed but the directory already contains go.mod or *.go files",
				Remedy:  "Scaffold into a clean directory (or delete the Go files if they are unwanted), or write an .appdef instead of using the template",
			},
			{
				Symptom: "register_project fails with 'trust/isolation rejected'",
				Cause:   "Manifest declares Isolation=process or sandbox (both unimplemented), the signer is empty, or a TrustClass=third_party plugin ships a shared-library artifact (third-party forces the subprocess transport)",
				Remedy:  "Use Isolation=inprocess or subprocess, provide a non-empty Signer, and for TrustClass=third_party ship a standalone executable (subprocess), never a shared library",
			},
			{
				Symptom: "register_project fails with 'ABI symbol not found'",
				Cause:   "A c-shared artifact (.dll/.so/.dylib) used for an inprocess (release) load does not export one of the seven required C symbols (PluginManifest, PluginOnLoad, PluginOnUnload, PluginOnConfigChange, PluginInvoke, PluginSetHostBridge, PluginLog)",
				Remedy:  "The native_build release build auto-generates the export shim (main_cgo.gen.go): do not hand-write //export into main.gen.go. If you hand-rolled the c-shared build, export the seven symbols and rebuild with -buildmode=c-shared",
			},
			{
				Symptom: "handwritten index.html frontend gets 404/undefined on every call (window.sporemind.invoke, POST /api/pluginhost.invoke are dead)",
				Cause:   "Those are the OLD bridge surfaces; panels no longer load through them. The gateway data path is the only transport: JSON over the gateway reverse proxy",
				Remedy:  "await window.__sporemindAppBaseReady, then fetch((window.__sporemindAppBase||'') + '/invoke/' + callableId) with JSON body — the gateway attaches the per-app token on the proxy hop, no credentials needed in the panel; events come from EventSource((appBase) + '/events'). dev_gate flags legacy bridge tokens in index.html",
			},
			{
				Symptom: "app panel renders a directory listing of the host's run directory instead of the app UI",
				Cause:   "The plugin subprocess inherits the host's cwd; older SDKs served GET / from \".\" with directory listings enabled, exposing whatever directory the host runs from through the gateway",
				Remedy:  "Refresh the vendored SDK (appmanager.sdk_vendor) and rebuild: the host now pushes staticDir (the app directory) in the OnLoad config, the SDK anchors / to it, and directory requests without index.html answer 404 (fail-closed, no listings)",
			},
			{
				Symptom: "a zip-installed native plugin's panel 404s on /plugin/{id}/index.html while its callables and manifest fetch work",
				Cause:   "Zip installs keep the artifact in the content-addressed inventory (<DataDir>/.actors/appmanager/artifacts/), not <appDir>/.sporecode/build/, so the app directory (LoadConfig.StaticDir) was not derivable and the SDK's static root fell back to its \".\" default (the host run directory)",
				Remedy:  "Use a host that materializes the asset bundle at <DataDir>/.actors/appmanager/apps/<appID>/ and pushes it as staticDir; a plugin process already loaded before the fix keeps its config-less OnLoad config and must be uninstalled and reinstalled (respawn) to pick it up",
			},
			{
				Symptom: "callable responses or task output echo absolute filesystem paths (D:\\... or /home/...)",
				Cause:   "OS error strings and shell output embed host run-directory paths; returning them to the panel discloses the host's on-disk layout to app users",
				Remedy:  "Diagnostics go to sdk.Log (host-side ring, full detail kept there); responses carry path-free messages only — trim or redact candidates/paths before returning (the host also redacts [path] in failure causes, but plugin responses are the plugin's responsibility)",
			},
			{
				Symptom: "dev_generate fails with 'field <struct>.<field>: unknown scalar \"float64\"' (or float/int64-style Go/Rust type names)",
				Cause:   "Struct field types were previously unvalidated: an unsupported scalar silently generated interface{} and the drift surfaced only when the handler was written against the wrong generated type",
				Remedy:  "Use the supported scalars: string, bool, int, int32, int64, long, float (Go float32), double (Go float64), bytes, any — 64-bit floats are `double`, not float64; or reference a declared struct/alias. Validation names the exact field path",
			},
			{
				Symptom: "app panel will not load and you are port-scanning with netstat to find where the gateway listens",
				Cause:   "The runtime topology (gateway route /plugin/{id} -> per-app proxy with X-Gateway-Token -> plugin loopback listener with spore_session cookie) lives in three places and was not observable through one read-only entry",
				Remedy:  "Call appmanager.panel_topology {Id}: it returns the gateway base (config gateway_addr, default 127.0.0.1:18080), the exact panel URL with generation, the plugin listener address, and the expected status code per hop — including that a 401 on a DIRECT listener hit is auth working (cookie is same-origin only), not a bug",
			},
			{
				Symptom: "dev_generate fails with 'content after app block (unbalanced braces? ...)'",
				Cause:   "A stray '}' balanced the app { } block early; the parser used to silently drop everything after it (declarations vanished, orphan-handler warnings misled)",
				Remedy:  "Balance the braces in .appdef — the error names the line where the app block ended; the removed declarations are everything after it",
			},
			{
				Symptom: "register_project over an already-registered app ID fails with 'schema conflict: duplicate dynamic struct ... in namespace ...'",
				Cause:   "The replace path used to skip unregistering the old namespace, so re-registering the same app collided with its own previous schema registration",
				Remedy:  "Fixed: doRegister now unregisters the old app's protocol before binding the new one — re-running register_project over the same ID is the supported replace path (no manual unregister needed)",
			},
			{
				Symptom: "sdk.Log entries written at package-init stage never appear in pluginhost.plugin_logs",
				Cause:   "Init-stage Log calls ran before the subprocess writer and the OnLoad state existed and were dropped; OnLoad-stage and handler-stage logs did flow",
				Remedy:  "Refresh the vendored SDK (appmanager.sdk_vendor): init-stage entries now buffer and flush as the first 0x05 frames when the process transport starts, so they reach the host log ring",
			},
			{
				Symptom: "invoke returns 'callable not found'",
				Cause:   "The callable ID in the invoke request does not match any callable declared in the manifest's Callables list",
				Remedy:  "Check appmanager.get to see declared callables; update the manifest and reload_project if needed",
			},
			{
				Symptom: "reload_project fails but old version still works",
				Cause:   "The new artifact failed prepare or commit validation; the atomic swap preserved the old handler",
				Remedy:  "Fix the source error, rebuild, and retry reload_project. The old app continues serving until a successful reload",
			},
			{
				Symptom: "app shows restart_pending after register/reload/plugin_load on an in-process plugin",
				Cause:   "A c-shared library cannot be swapped or unmapped while the host process lives, so the operation was staged instead of failing: the new artifact is persisted in the pluginhost ArtifactLoads and the record honestly reports restart_pending",
				Remedy:  "This is the intended deferred-activation state, not an error. Restart the host process: pluginhost restores the ArtifactLoads and the appmanager spawn loop cross-validates the artifact and marks the app running. If the pluginhost is already up (partial restart), call appmanager.plugin_load to activate manually. The previous artifact keeps serving until then",
			},
			{
				Symptom: "unregister leaves app in unload_failed state",
				Cause:   "The native handler's OnUnload returned an error or timed out",
				Remedy:  "Call appmanager.retry_cleanup to retry the cleanup; if it persists, inspect pluginhost.list_plugins for the stuck handler",
			},
			{
				Symptom: "invoke times out after 30 seconds",
				Cause:   "A callable exceeded the invoke timeout: typically a hanging or deadlocked handler",
				Remedy:  "Over the subprocess transport the timed-out plugin was already killed and reaped: fix the code and reload or plugin_load it. Over inprocess the host cannot interrupt native calls: restart the host process if needed, then inspect the plugin code for the deadlock",
			},
			{
				Symptom: "frontend fetch to /invoke/{id} (or the EventSource on /events) returns 401 unauthorized",
				Cause:   "Cookie auth is enforced (the host pushed a sessionSecret via the OnLoad config) and the spore_session cookie is missing or invalid — e.g. a stale cookie minted against a previous secret after a reload, or a standalone open that never received the cookie",
				Remedy:  "Remount the panel so the host bootstrap re-mints the cookie from the current secret; for standalone dev opens with no host, ensure no sessionSecret is set (an empty secret disables auth, dev mode)",
			},
			{
				Symptom: "the panel's FIRST data fetch right after mount returns 404 against the gateway root (URL /invoke/{id} or /events missing the /plugin/{id} prefix)",
				Cause:   "The mount base reaches the iframe via the postMessage bootstrap handshake (window.__sporemindAppBase), which can land after the page's first fetch: a client that reads the base synchronously races the handshake",
				Remedy:  "Nothing to do in app code — generated client.gen.ts awaits window.__sporemindAppBaseReady (created by the injected bootstrap snippet, resolved on handshake with a 3s fallback) before building any URL. Hand-written fetches must await the same promise or read __sporemindAppBase after it resolves; the standalone dev path (no handshake) proceeds with the root-relative prefix after the fallback",
			},
			{
				Symptom: "frontend fetch to /invoke/{id} returns 404 callable not found although the callable is declared in .appdef",
				Cause:   "The callable declares expose: agent — agent-only callables are intentionally not registered on the plugin's HTTP listener (generated server.gen.go skips them), so the frontend path cannot see them",
				Remedy:  "Change expose to frontend or both, re-run dev_generate, and reload_project; or invoke the callable through the agent path (appmanager.invoke) instead of the frontend HTTP path",
			},
			{
				Symptom: "frontend on<Event> subscription never fires",
				Cause:   "The event is not declared as an `event` block in .appdef, the handler never calls sdk.EmitEvent (or the generated Emit<ID> wrapper) after the mutation, or the EventSource never connected (401 — see the cookie entry above)",
				Remedy:  "Declare the event block, emit it from the mutating handler after the state change (watch-declared mutate stubs already auto-emit with nil payloads), and verify GET /events returns text/event-stream with a valid spore_session cookie",
			},
			{
				Symptom: "invoke reaches the handler but fails to locate external repo/workspace roots, or resolves paths under the host exe dir (e.g. .../sporemind/build/bin)",
				Cause:   "The handler inferred roots from the process cwd (os.Getwd). The desktop host runs plugins with cwd = the host exe directory, not a checkout root, so cwd-based inference works in dev runs and breaks in release builds",
				Remedy:  "Never resolve repo roots from cwd inside a handler. Persist configured or once-discovered roots in the per-app host state (state.set / CallStateSet), or accept an explicit root field on the request; marker-based upward discovery may serve only as a first-run fallback whose result is cached and re-validated",
			},
		},
	}
}
