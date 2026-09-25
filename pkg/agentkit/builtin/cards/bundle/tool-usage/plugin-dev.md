---
id: builtin:bundle:plugin-dev
type: bundle
title: Plugin Dev
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: package
  visual:
    icon: package
    accent: purple
    color: "#9333ea"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  requires:
    - builtin:bundle:app-tools
  tools:
    - appmanager.register_project
    - appmanager.app_export
    - appmanager.install_local
    - appmanager.reload_project
    - appmanager.unregister
    - appmanager.retry_cleanup
    - appmanager.plugin_load
    - appmanager.plugin_unload
    - appmanager.dev_generate
    - appmanager.dev_gate
    - appmanager.sdk_vendor
    - pluginhost.list_plugins
    - pluginhost.plugin_logs
    - pluginhost.plugin_dom
    - pluginhost.panel_op
    - appmanager.callable_info
    - appmanager.dev_guide
    - appmanager.host_protocol
    - appmanager.icon_names
    - appmanager.open_view
    - appmanager.panel_topology
---

## Plugin Dev

First-party Plugin development bundle — generate, implement, gate-check, register, and manage native plugins. Not mounted by default; mount it explicitly when the task is to build or manage Plugins.

**This card is the map, not the manual.** Call `appmanager.dev_guide` (`prerequisites` / `workflow` / `host_api` / `security` / `errors`) before coding — it is the authoritative development reference — and `appmanager.callable_info` for the callable surface. Mount `skill:plugin-builder` for exact SDK API signatures.

> **Layering**: requires `builtin:bundle:app-tools` (auto-mounted, scope "dependency"); the runtime app-manager surface (`appmanager.list` / `get` / `invoke` / `component_list` / `component_get`) is inherited through that `requires` entry, not re-declared here.

> **Scope**: first-party Plugins only (Runtime=native, Isolation=inprocess, TrustClass=first_party, non-empty signer). Third-party Plugins are NO-GO.

### Before you build — discover existing bundles

An app's bundle, once registered, projects as a virtual component any agent can mount. Before writing a new `.appdef`: `appmanager.list` (registered apps + state) → `appmanager.component_list` (projectable bundles) → `appmanager.component_get {CardID}` (a bundle's tools/deps) → `appmanager.invoke` (test-drive a callable without mounting). If one fits, mount it (`app-bundle:{appID}:{bundleName}`) or add it to the agent kind's `DefaultBundleIDs` — no new app needed. Only build when nothing covers the requirement.

### Icons

Bundle/entrypoint icons must come from the host icon library: call `appmanager.icon_names` (optional `Query` / `Category`) and use the returned `Name` verbatim in `data.icon` and `data.visual.icon` (repeat it in both). Invalid names render a neutral fallback.

### Go toolchain preflight

Verify once per session with `go version` — server-side builds use the host's `go` from PATH. Missing `go` → stop and tell the user to install it. An installed Go older than the generated `go` directive is fine under the default `GOTOOLCHAIN=auto`; if `GOTOOLCHAIN=local` is pinned, ask the user to upgrade or run `go env -w GOTOOLCHAIN=auto`. Release (c-shared) builds additionally need `CGO_ENABLED=1` + a C compiler. Never edit the generated `go` directive downward.

### Development workflow

Five steps with `.appdef` as the single declaration source; file I/O via `project.read` / `project.write` / `project.edit`.

1. **Write `<name>.appdef`** — same struct syntax as `schemas/*.spore` plus wrapper blocks (`app` / `callable` / `entrypoint` / `event` / `bundle` / `free_agent` / `plugin_agent` / `listen`). Sole file you edit at the declaration layer. A callable may set an explicit `toolName` (the agent-facing tool name); `expose: frontend|agent|both` (own line, empty = both) picks the consumption face — `expose: agent` callables are not served over the plugin's HTTP listener (frontend `/invoke` 404), `expose: frontend` callables are filtered out of the agent tool registry; `watch: [event_id, ...]` (own line) names the events that invalidate the callable's cached results (every entry must be declared in the same `.appdef`). Optional `free_agent { allow_create / allow_switch / allow_message / agent_kinds }` gates user-facing agent actions; optional `plugin_agent [name] { display_name / system_prompt / bundles }` provisions a dedicated workspace-level agent at register.
2. **`appmanager.dev_generate`** (plus `AppDir` for subdirectory apps) — reads `.appdef`, writes the artifacts + `go.mod`. A directory without `.appdef` is not scaffolded implicitly; pass `Template=true` to scaffold the minimal template into a directory with no `go.mod` / `*.go`.
3. **Implement `handlers.go`** — the only file you edit by hand: replace the generated stub bodies (and provide real payloads for the watched events a `mutate` callable announces).
4. **`appmanager.dev_gate`** — run the four gates (build / stub_filling / manifest_consistency / coverage) without registering.
5. **`appmanager.register_project`** — build + validate + load + register; the gates re-run inline. Use `appmanager.reload_project` for the edit loop.

Generator-owned (write-protected — hand edits rejected): `main.gen.go`, `main_run.gen.go`, `server.gen.go`, `app.manifest.json`, `schemas_gen.go`, `app.descriptors.json`, `client.gen.ts`. Agent-owned: `handlers.go`. Regenerating appends new callable stubs but never overwrites existing functions. Any non-source file in the app root (`index.html`, `icon.png`, …) is silently packaged as an asset and served at `/plugin/{id}/<name>`.

**Deploy ≠ edit**: handler code and assets are snapshotted at `register_project` / `reload_project`. Editing a file on disk does not change what the running plugin serves until you reload — confirm `ArtifactHash` changed via `appmanager.get`. `dev_generate` / `dev_gate` / `register_project` route to the caller's bound worktree when there is one.

#### appdef blocks you will use

- **`listen <kind> {}`** — receive host bus events. Kinds: `app_lifecycle` (payload `AppLifecycleEvent`: register/reload/unload/fail) and `app_event` (payload `AppEventMessage`: the generic envelope). `dev_generate` emits the typed payloads + an `OnXxx` handler stub. Handlers run inline on the invoke path — keep them fast, never block.
- **`event <id> { payload: T }`** — publish events. `dev_generate` derives the `app.emit` capability automatically and generates an `Emit<ID>` wrapper (Go) and `on<ID>` subscription (TS). `sdk.EmitEvent` delivers along two independent paths: local SSE (`GET /events`, fan-out to connected panels) and the host bridge (`app.emit` → `appmanager.plugin_emit`, which validates the event against the manifest and broadcasts on the `app_event` bus for cross-app visibility). Payload-less events use `event <id> { }`.

#### Frontend data path (no bridge relay)

The panel iframe loads the host gateway route `/plugin/{id}/{route}?v={generation}` — the gateway reverse-proxies every request to the plugin process's own HTTP listener (`sdk.ServeHTTP`), attaching a per-app gateway token, so the panel never learns the subprocess origin or session secret. The wire format is plain JSON (no base64 / binary codec); large media travels as HTTP-native binary (`uploadFile` / `streamVideo` helpers; play via same-origin `<video>` / `<audio>` / `<img>`). `client.gen.ts` exposes typed `on<Event>` subscriptions over the SSE `GET /events`. A single-source bootstrap snippet (`sdk.BridgeBootstrapSnippet`) is injected into every HTML response and resolves `window.__sporemindAppBaseReady`; generated clients `await appBase()` before building any URL, so always reach the host through the generated client.

#### Frontend runtime contract (sandboxed iframe)

The panel — your `index.html` / `client.gen.ts` — runs inside a sandboxed iframe on the gateway origin. Every frontend must survive that sandbox, so:

- **No native browser dialogs.** `window.alert` / `confirm` / `prompt` are suppressed in the sandbox (`allow-modals` is not set): `confirm()` returns `false` without ever rendering — this is the usual "the confirmation modal won't open" failure. Build confirm/choice UI as in-panel HTML (`<dialog>` or a positioned element inside the iframe); never gate an action on a native dialog.
- **No popups or window navigation.** `window.open` / `target="_blank"` are blocked; `window.top` / `parent` / `opener` cross-origin access throws. Never assign `location.href` to navigate — route in-page (hash / history API) and render within the iframe.
- **No web storage for durable state.** `localStorage` / `sessionStorage` are off-limits; persist durable UI/preference state through a backend callable (`app.state`).
- **Reach the host only through the generated client.** `await appBase()` then `fetch {base}/invoke/...`; never hardcode host origins, ports, or absolute URLs. Static files load same-origin from the app directory.
- **Theme and locale come from the host.** The injected bridge snippet applies the host theme to `<html data-theme>` (live via `sporemind:theme-update`) and the host locale (BCP47, e.g. `zh-CN`) to `<html lang>` (live via `sporemind:locale-update`). Read the locale via `window.sporemind.locale` and track switches with a `MutationObserver` on `lang`; standalone browser opens fall back to `navigator.language`.
- **One event connection, WebSocket-first.** `client.gen.ts` subscribes via one shared WebSocket on `{base}/events` (SSE fallback when the handshake is refused). Do NOT open your own `EventSource` per panel: browser HTTP/1.1 pools cap at 6 connections per host and every panel iframe shares the gateway origin — permanent streams starve all transient invokes (this froze every panel on 2026-09-14). If you hand-write panel JS without the generated client, poll instead of holding a permanent stream, or wait for WS.
- **Data-channel heartbeat, not the bridge.** A cross-origin iframe's console and DOM are invisible to the host until the management bridge attaches — and the bridge itself can be dead while the data plane still works. Panel health must be observable through the data plane: add a lightweight beacon (e.g. a `sdk.Log` line on the first + every Nth poll of a frequent callable, or a gap detector that logs when a normally-1.5s poll goes silent >5s). This is the pattern that cracked the 2026-09-14 incident; do not build panels whose only observability is the bridge.
- **Pointer release is host-guaranteed.** The injected bridge snippet auto-captures the pointer on `pointerdown` (capture phase), so a press released outside the iframe — over host chrome or a native window — still delivers `pointerup`/`mouseup`/`click` to the pressed element. Write plain `pointerdown`/`pointerup` handlers; do NOT hand-roll window-level mouseup/blur fallbacks or stuck-drag watchdogs. Excluded targets: `input`/`textarea`/`select`/`[contenteditable]` (the browser already manages their press semantics). If the plugin calls `setPointerCapture` itself, the plugin's capture wins (last-write-wins).
- Keep overlays inside the iframe viewport; the iframe is the whole world — host chrome and native browser windows are not addressable from the panel.

Verify frontend behaviour with `appmanager.open_view` → `pluginhost.plugin_dom` (rendered structure) → `pluginhost.panel_op` (precise interaction, below) → `pluginhost.plugin_logs` (Source=frontend console + errors).

#### Panel operations (drive your own panel)

`pluginhost.panel_op` runs a precise operation inside a mounted panel iframe — dev-registered plugins only (`register_project` / `reload_project`); installed third-party plugins are refused. Requires the panel to be open (`appmanager.open_view`) and handshaked. Ops:

- `dom` — bounded DOM serialization with `[k]` markers on interactive elements; `Selector` scopes the root, `MaxChars` (default 16384, cap 131072) bounds the output.
- `eval` — bounded async JS in the panel (`Expr` is the body of an async function; `return` a value; a bare expression is auto-wrapped); result JSON-serialized, `TimeoutMs` default 5000 / cap 15000.
- `click` — synthetic pointer/mouse sequence on `Selector`.
- `type` — native-value-setter input on `Selector` (controlled components observe it) + `input`/`change` events; also contenteditable.
- `wait` — poll until `Selector` (+ optional `Text` substring) is visible, within `TimeoutMs`.

Workflow: `dom` to read state → `click`/`type` to act → `wait` to confirm. Typical failure reasons: `selector not found`, `no bridge port: panel not mounted`, timeouts. `Ok=false` + `Reason` is a business result, not an error — inspect and adapt the selector.

#### Host calls (query → declare → generate)

Host services are declared, not assumed: call `appmanager.host_protocol` for the grantable capabilities and the SDK callIDs they gate, add those **callIDs** to `permissions:` in `.appdef` (capability strings like `"fs.read"` are rejected; unknown callIDs fail generation with zero writes), then re-run `dev_generate` to emit `hostproto.gen.go` — typed callers over `host.Invoke`. Undeclared calls are refused by the SDK. Current capabilities: `fs.read` / `fs.write`, `config.read`, `app.state`, `shell.exec`, `llm.invoke`, `aggregator.read`, `provider.read`, `registry.read`, `voice.stt` / `voice.tts` / `voice.read`, `media.read`, `image.gen` / `video.gen`, `ssh.invoke`. Per-call payload and type contract: `dev_guide` topic `host_api`.

### Post-registration operations

- `appmanager.invoke` {Id, Callable, Payload} — call a callable.
- `appmanager.plugin_unload` / `appmanager.plugin_load` — stop / resume a running plugin without deleting the registration (idempotent; the stopped state does not survive a host restart).
- `appmanager.unregister` — unload and delete the registration; `appmanager.retry_cleanup` finishes a registration stuck in `unload_failed`.
- `appmanager.reload_project` — atomic swap; the previous handler is kept on failure. Confirm with `appmanager.get`.

### Packaged distribution (export → install)

`appmanager.app_export` reassembles the package (manifest, modules, assets, descriptors, plus for native runtimes the binary) and signs it (Ed25519). The caller persists the returned `PackageData` bytes; distribute the zip; on the target host `appmanager.install_local` recomputes the canonical hash and rejects a tampered package before load, then registers it like any dev app. Same-ID install is an in-place update. Details: `dev_guide` topic `workflow`.

### Manifest required fields

`Id`, `Name`, `Version`, `Runtime=native`, `ProtocolVersion=2` (v2-only frame codec), `Namespace`, `Permissions`, `Schemas`, `Callables`, `Events`, `Projections`, `Entrypoints`, `Dependencies`. ABI fields (Isolation / TrustClass / Signer) are validated by PluginHost (`pkg/pluginhost/contract.go`). `AgentBinding` is optional: `free_agent` → `AppAgentBinding.FreeAgent`; each `plugin_agent` block → one `PluginAgents[]` entry (slot `default` when unnamed); with neither block the manifest carries a nil binding and agent actions are denied.

### ABI frame

Length-prefixed `PluginInvoke`, wrapped by `sdk.HandleInvokeFramed`. Dev transport (subprocess) speaks `[4-byte BE length][1-byte msg type][payload]` — 0x01 invoke-req / 0x02 invoke-resp / 0x03 reverse-req / 0x04 reverse-resp / 0x05 log / 0x06 error / 0x07 reverse-chunk, correlated by callID and terminated by 0x04 — via `sdk.RunProcess()`. In-process (release) loads require seven exported symbols (`PluginManifest`, `PluginOnLoad`, `PluginOnUnload`, `PluginOnConfigChange`, `PluginInvoke`, `PluginSetHostBridge`, `PluginLog`), auto-generated in a staged cgo shim.

### Safety constraints

- **Declaration is authorization**: the manifest's declared capabilities are the runtime permission set — there is no host allowlist and no consent round-trip. Register-time validation rejects unknown capability strings and callable/event/entrypoint permission refs not declared in the manifest's own `permissions:`.
- **30s timeout**: a synchronous call into the shared library cannot be interrupted once entered.
- **Crash isolation**: an in-process crash affects the host process — hence first-party only. Test fragile/long handlers in the subprocess (dev) transport first.
- **Generated-file ownership**: generator-owned files are write-protected; `handlers.go` is yours.
- **Sandboxed-iframe frontend**: native dialogs, popups, and web storage are unavailable — see "Frontend runtime contract" above.

Full security rationale: `dev_guide` topic `security`.

### Diagnostics

`pluginhost.plugin_logs` (backend `sdk.Log` + frontend console, per app, with the process verdict) and `pluginhost.plugin_dom` (rendered DOM snapshot; needs a panel mounted via `appmanager.open_view`) are the first tools for a misbehaving app; `appmanager.dev_gate` diagnoses the gates; `appmanager.panel_topology` explains a panel that will not load at all.

### Related skills

- `skill:plugin-builder` — Full SDK API reference (~400 lines). Mount when you need detailed API signatures.
- `skill:experimental-third-party-plugin-builder` — Experimental third-party path (explicit risk acceptance required).
