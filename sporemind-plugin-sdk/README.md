# sporemind-plugin-sdk

Go SDK for building sporemind plugins in two transports:

- **c-shared (prod)**: in-process FFI library, loaded via purego/Dlopen.
- **subprocess (dev)**: standalone executable speaking a length-prefixed
  duplex framing protocol over stdin/stdout — gives hot reload, crash
  isolation, and debugger attachability. `main()` reads frames from stdin,
  dispatches to registered callables, writes responses (and direct `0x05`
  log frames) to stdout; reverse calls travel through an IPC-backed `Host`
  injected via `SetHost`. The transport is chosen by the host at load time.

## Features

- Declarative plugin manifest
- Lifecycle hooks: `OnLoad`, `OnUnload`, `OnConfigChange`
- Callable registration with isolated namespace
- Host-call primitives: `Invoke(callID)` / `InvokeStream(callID)`; typed `CallXxx`/`StreamXxx` generated per-app from declared permissions
- Callable discovery: `ListCallables(q)` queries the host's callable registry — metadata only, requires `registry.read` permission
- Reverse bridge for plugin-to-host calls (FFI callback in c-shared mode,
  `0x03/0x04` frames in subprocess mode)
- HTTP listener (`ServeHTTP`) serving the frontend directly: `POST /invoke/{id}`
  dispatch, `GET /events` SSE, and same-origin static files — parallel to the
  stdin/stdout frame protocol

## HTTP listener, SSE, and cookie auth

`ServeHTTP(addr, ...)` starts an HTTP listener in a background goroutine,
parallel to the frame protocol. Generated `server.gen.go` fills the HTTP
handler map with `RegisterHTTPHandler(callableID, fn)`; the SDK owns the
dispatch and routes only. Routes:

- `POST /invoke/{callableId}` — JSON body → `HTTPHandler` → JSON response.
- `GET /events` — `text/event-stream` SSE, upgraded to WebSocket when the
  client sends an Upgrade header. Panel clients use WebSocket: browsers cap
  same-pool HTTP/1.1 connections at 6 per host and all panels share the
  gateway origin, so a permanent SSE stream per panel starves every transient
  invoke; WebSocket lives outside that pool. WS frame envelope is one JSON
  object per event (`{"event":"<id>","data":<raw JSON>}`), server pings every
  25s; plain SSE remains for standalone browser opens and older generated
  clients. `sdk.EmitEvent` fans out to every connected frontend on either
  transport (local path) *and* forwards to the host bus via the `app.emit`
  host call (cross-app path, unchanged).
- `POST /session/bootstrap` — the host-minted session token is posted here
  (same-origin) during the iframe bootstrap; a valid token comes back as an
  HttpOnly `Set-Cookie`, so the data path above is authorized without any
  script-visible credential. Invalid/missing token → 401 when a secret is
  configured.
- `POST /__sdk/file-drop/{dropID}/begin | /{index} | /end` — OS file-drop
  delivery (see below). `begin` carries the flat entry metadata as JSON;
  each `/{index}` POST carries one file's bytes as the raw body (no base64);
  `POST /__sdk/file-drop/host` delivers host file-browser drags as
  metadata-only path references.
  `end` invokes the backend listener and mirrors the delivery as the
  `file_drop` event on `/events`. Cookie/gateway-token authenticated like
  `/invoke`.
- `GET /` — static files from the app directory (same-origin, no CORS).
  Served HTML documents get the host bridge bootstrap snippet injected
  (behavior-identical to the gateway fallback copy in
  `pkg/pluginhost/assets.go`) so the management plane (console logs, DOM
  snapshots, theme, locale, ui-actions) attaches in the iframe; non-HTML
  assets pass through untouched, and HTML is served `no-store`.

`/invoke` and `/events` are cookie-authenticated with a per-instance HMAC
secret. The host pushes it (and an optional listener address) as the OnLoad
config JSON, which the SDK applies itself:

```json
{ "httpAddr": "127.0.0.1:0", "sessionSecret": "<hex or raw>" }
```

- `httpAddr` non-empty → the SDK auto-starts the listener after the app's
  `OnLoad` runs, and reports the bound address back in the OnLoad response as
  `{"httpAddr": "<bound>"}` (subprocess transport) so the host can bootstrap
  the iframe `backendUrl`.
- `sessionSecret` non-empty → cookie auth is enforced: the client must send a
  `spore_session` cookie equal to `MintSessionToken(secret, sessionId)`
  (`sessionId + "." + hex(HMAC-SHA256(secret, sessionId))`); invalid → 401.
  Empty secret disables auth (dev mode). The cookie is HttpOnly / SameSite so
  `<video>`/`<audio>`/`<img>` and same-origin `fetch` carry it automatically.

`Shutdown` (also called automatically on unload) stops the listener so a
reloaded plugin does not leave a dangling port.

## OS file drop (drag files into the plugin panel)

Files dragged from the OS onto the plugin panel iframe are claimed by the
injected bootstrap snippet: drops land inside the iframe document (the host
page never sees them) and the default drop would navigate the iframe to the
file, breaking the panel. The snippet claims every *unclaimed* file drop —
drags carrying `Files` whose target is not inside an element marked
`[data-file-drop-target]` (the same yield marker the host shell uses) —
expands directory trees (`webkitGetAsEntry`, dirs-before-children,
`'/'`-separated `relPath`), and triggers the plugin on both sides:

```go
// Backend: receive the drop with bytes already in memory.
sdk.RegisterFileDropListener(func(entries []sdk.FileDropEntry) error {
    for _, e := range entries {
        if e.IsDir { continue }
        process(e.RelPath, e.Data) // Data is nil for directories
    }
    return nil
})
```

```js
// Frontend: raw entries with live File objects; preventDefault marks the
// drop as handled here and skips the backend upload entirely.
window.addEventListener('sporemind:file-drop', (e) => {
  for (const en of e.detail.entries) console.log(en.relPath, en.size, en.file)
  e.preventDefault()
})
```

Contract details:

- **Opt-in by registration**: while no backend listener is registered, the
  panel's `begin` call is refused and no file bytes are uploaded. The DOM
  event still fires, so frontend-only handling works with zero backend code.
- **Delivery**: metadata as JSON, file bytes as raw POST bodies (no base64),
  over the same listener/gateway path as `/invoke`. After the listener runs,
  the entry metadata is broadcast as the `file_drop` event on `/events`
  (`window.sporemind.subscribe('file_drop', ...)`).
- **Caps**: a drop batch is refused (`too-large`) when the declared total
  exceeds `MaxFileDropBytes` (256 MiB); pending batches are swept after
  10 minutes. Bytes stay in memory — drop data is transient, not state.
- **Yield**: plugin-owned dropzones marked `[data-file-drop-target]` keep
  native HTML5 drag-and-drop; the snippet does not interfere with them.
- **Listener errors** surface as HTTP 500 to the panel (logged in the panel
  console); a panicking listener is recovered and never kills the process.

### Host file-browser drags

Dragging an entry from the host's own file panels (local FileBrowser, remote
SSH FTP) into the panel is a DOM drag, not an OS drag — the payload is the
host convention `text/plain` marker `x-sporemind-dnd:` + JSON
(`{origin, name, path, isDir, projectId?, sessionId?}`), and it carries no
`Files`. The snippet recognizes the marker and delivers it through the same
two paths, with `origin: "host-browser"` and **no bytes**:

```go
sdk.RegisterFileDropListener(func(entries []sdk.FileDropEntry) error {
    for _, e := range entries {
        if e.Origin == "host-browser" {
            openByReference(e.Path, e.ProjectID, e.SessionID) // no Data
            continue
        }
        process(e.RelPath, e.Data)
    }
    return nil
})
```

The frontend event carries `e.detail.origin === "host-browser"` and entries
with `path`/`projectId` instead of `file`. `path` is project-relative for
local-origin drags (`projectId` identifies the project) and absolute for
remote-origin drags (`sessionId` identifies the SSH session). The same
opt-in, `[data-file-drop-target]` yield, and `/events` `file_drop` mirror
apply; non-marker text drops pass through untouched.

## Quick Start

```go
package main

import (
    "C"
    "unsafe"

    sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

func init() {
    sdk.Register(&sdk.Plugin{
        Manifest: sdk.Manifest{
            ID:          "com.example.hello",
            Name:        "Hello Plugin",
            Version:     "1.0.0",
            Permissions: []string{sdk.PermLLMInvoke, sdk.PermFSRead},
            Entrypoints: []sdk.Entrypoint{
                {Kind: "view", ID: "dashboard", Title: "Dashboard", Route: "index.html"},
            },
        },
        OnLoad: func(ctx sdk.Context) error {
            ctx.RegisterCallable("greet", func(req sdk.Request) (sdk.Response, error) {
                return sdk.Response{Payload: "hello"}, nil
            })
            return nil
        },
    })
}

//export PluginManifest
func PluginManifest(buf *C.char, n C.int) C.int {
    return C.int(sdk.WriteManifest(unsafe.Pointer(buf), int32(n)))
}

//export PluginOnLoad
func PluginOnLoad(pluginID *C.char, config *C.char) C.int {
    return C.int(sdk.HandleOnLoad(unsafe.Pointer(pluginID), unsafe.Pointer(config)))
}

//export PluginOnUnload
func PluginOnUnload(pluginID *C.char) C.int {
    return C.int(sdk.HandleOnUnload(unsafe.Pointer(pluginID)))
}

//export PluginOnConfigChange
func PluginOnConfigChange(pluginID *C.char, config *C.char) C.int {
    return C.int(sdk.HandleOnConfigChange(unsafe.Pointer(pluginID), unsafe.Pointer(config)))
}

//export PluginInvoke
func PluginInvoke(req *C.char, reqLen C.size_t, resp *C.char, respCap C.size_t, respLen *C.size_t) C.int {
    return C.int(sdk.HandleInvokeFramed(unsafe.Pointer(req), uintptr(reqLen), unsafe.Pointer(resp), uintptr(respCap), (*uintptr)(unsafe.Pointer(respLen))))
}

//export PluginSetHostBridge
func PluginSetHostBridge(bridge unsafe.Pointer) C.int {
    return C.int(sdk.HandleSetHostBridge(bridge))
}

//export PluginLog
func PluginLog(buf *C.char, n C.int) C.int {
    return C.int(sdk.HandlePluginLog(unsafe.Pointer(buf), int32(n)))
}

func main() {}
```

## Build

The plugin's `package main` needs two build-tagged entry files plus the shared
plugin definition:

- `main.go` (no tag): `init()` calling `sdk.Register(...)` — shared.
- `main_cgo.go` (`//go:build cgo`): the `//export Plugin*` symbols + `func main() {}`.
- `main_nocgo.go` (`//go:build !cgo`): `func main() { sdk.RunProcess() }`.

```bash
# prod: in-process c-shared library (requires cgo). Directory mode "." so
# main_cgo.go's //export symbols are included.
go build -buildmode=c-shared -o plugin-hello.dll .

# dev: standalone subprocess executable (CGO_ENABLED=0 pulls in the SDK's
# process_main.go transport via RunProcess).
CGO_ENABLED=0 go build -o plugin-hello.exe .
```

The dev executable's protocol: `[4-byte BE length][1-byte msg type][payload]`,
message types `0x01` invoke-req / `0x02` invoke-resp / `0x03` reverse-req /
`0x04` reverse-resp / `0x05` log / `0x06` error. `stderr` is reserved for Go
runtime panics and diagnostics — data frames only travel on stdout. The host
sends `onLoad` as the first invoke-req after spawning the process.

## Example

See `examples/hello/` for a complete example.

## License

MIT

## License

MIT — this SDK module is licensed separately from the host application (AGPL-3.0). See [LICENSE](LICENSE).
