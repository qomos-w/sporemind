package sdk

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// HTTPHandler handles one POST /invoke/{callableId} call. The generated
// server.gen.go (dev_generate, T3) registers one HTTPHandler per callable the
// appdef marks as frontend-exposed (expose: frontend|both) by wrapping the
// typed Handler / HandlerStream from handlers.go. The SDK owns the dispatch
// and the handler map only; it never references an app's callable types.
//
// The payload is the raw JSON request body. The returned value is
// JSON-encoded into the response; a returned json.RawMessage is written
// verbatim. Returning a non-nil error yields a 500 with {"error":...}.
type HTTPHandler func(payload json.RawMessage) (any, error)

// ServeHTTPOption configures a ServeHTTP call.
type ServeHTTPOption func(*serveConfig)

type serveConfig struct {
	staticDir string
	// staticDirExplicit marks a WithStaticDir call from app code: an explicit
	// choice always wins over the host-pushed LoadConfig.StaticDir, which may
	// only replace the accidental "." default.
	staticDirExplicit bool
}

// WithStaticDir sets the directory served by GET / (the catch-all route).
// The default "." resolves against the plugin process working directory —
// which the host does NOT set (the plugin inherits the host's cwd). To avoid
// serving an arbitrary host directory, hosts push the app directory via the
// OnLoad config (LoadConfig.StaticDir), which replaces the default (but never
// an explicit WithStaticDir from the app itself). Directory listings are
// disabled: a directory request without index.html answers 404.
func WithStaticDir(dir string) ServeHTTPOption {
	return func(c *serveConfig) { c.staticDir = dir; c.staticDirExplicit = true }
}

// HTTPServer is the running plugin HTTP listener. Addr reports the bound
// address (useful when ServeHTTP bound ":0" for an ephemeral port). Shutdown
// stops the listener and the SSE hub; the package-level instance is cleared so
// a subsequent ServeHTTP starts fresh (used by reload).
type HTTPServer struct {
	addr     string
	listener net.Listener
	srv      *http.Server
	hub      *sseHub
	stopOnce sync.Once
	// static holds the current serveConfig for the "/" catch-all so the
	// host-pushed LoadConfig.StaticDir can replace the "." default on the
	// already-running listener (SetStaticRoot). An explicit WithStaticDir
	// (staticDirExplicit) pins the choice and is never overridden.
	static atomic.Pointer[serveConfig]
}

// SetStaticRoot redirects the static "/" route to dir when the running server
// was left on the "." default. It never overrides an explicit WithStaticDir
// from app code, and stashes dir for maybeAutoStartHTTP when no server is
// running yet. Returns true when the running server adopted dir.
func SetStaticRoot(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	httpState.Lock()
	defer httpState.Unlock()
	if httpState.server == nil {
		pendingStaticDir.Store(dir)
		return false
	}
	cfg := httpState.server.static.Load()
	if cfg != nil && cfg.staticDirExplicit {
		return false
	}
	next := &serveConfig{staticDir: dir}
	httpState.server.static.Store(next)
	return true
}

// pendingStaticDir carries a host-pushed static root to the listener that
// maybeAutoStartHTTP starts later (OnLoad ordering: config applied before the
// server binds).
var pendingStaticDir atomic.Value

// Addr returns the bound listener address (host:port).
func (s *HTTPServer) Addr() string { return s.addr }

// Hub returns the SSE hub. Exported so tests and (rarely) generated code can
// broadcast directly; the normal event path is sdk.EmitEvent.
func (s *HTTPServer) Hub() *sseHub { return s.hub }

// Shutdown stops the listener and the SSE hub. Idempotent. It is called
// automatically during plugin unload (HandleOnUnload) so a reloaded plugin does
// not leave a dangling listener; callers may also invoke it directly.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	httpState.Lock()
	if httpState.server == s {
		httpState.server = nil
	}
	httpState.Unlock()
	var err error
	s.stopOnce.Do(func() {
		if s.hub != nil {
			s.hub.close()
		}
		err = s.srv.Shutdown(ctx)
	})
	return err
}

// httpState holds the single running HTTP listener for this plugin process.
// The SDK is single-plugin-per-process (see activeState / registeredPlugin),
// so one listener is the correct cardinality. ServeHTTP sets it; Shutdown and
// HandleOnUnload clear it.
var httpState = struct {
	sync.RWMutex
	server *HTTPServer
}{}

func currentHTTPServer() *HTTPServer {
	httpState.RLock()
	defer httpState.RUnlock()
	return httpState.server
}

// httpHandlerRegistry is the HTTP-callable dispatch table. It is distinct from
// the callableRegistry (the stdin/stdout + ABI dispatch path): a callable is
// served over HTTP only when the generated server.gen.go registers an
// HTTPHandler for it, which honours the appdef `expose` field. The two
// registries intentionally do not auto-mirror — the agent path (ABI) and the
// frontend path (HTTP) are independent exposure surfaces.
var httpHandlerRegistry = struct {
	sync.RWMutex
	items map[string]HTTPHandler
}{items: make(map[string]HTTPHandler)}

// RegisterHTTPHandler registers (or replaces) the HTTP handler for one
// callable. Generated server.gen.go calls this in package init for every
// frontend-exposed callable. Re-registration overwrites.
func RegisterHTTPHandler(callableID string, h HTTPHandler) {
	httpHandlerRegistry.Lock()
	defer httpHandlerRegistry.Unlock()
	httpHandlerRegistry.items[callableID] = h
}

// UnregisterHTTPHandler removes the HTTP handler for one callable.
func UnregisterHTTPHandler(callableID string) {
	httpHandlerRegistry.Lock()
	defer httpHandlerRegistry.Unlock()
	delete(httpHandlerRegistry.items, callableID)
}

func lookupHTTPHandler(callableID string) (HTTPHandler, bool) {
	httpHandlerRegistry.RLock()
	defer httpHandlerRegistry.RUnlock()
	h, ok := httpHandlerRegistry.items[callableID]
	return h, ok
}

func clearHTTPHandlers() {
	httpHandlerRegistry.Lock()
	defer httpHandlerRegistry.Unlock()
	httpHandlerRegistry.items = make(map[string]HTTPHandler)
}

// ServeHTTP starts the plugin's HTTP listener in a background goroutine,
// parallel to the stdin/stdout frame protocol (RunProcess). It binds the
// listener synchronously so address errors surface immediately, then serves
// in a goroutine. Routes:
//
//	POST /session/bootstrap    -> install the frontend session cookie (Set-Cookie)
//	POST /invoke/{callableId}  -> HTTPHandler dispatch (cookie-authed)
//	GET  /events              -> text/event-stream SSE (cookie-authed)
//	GET  /                    -> static files from the app dir (no CORS), HTML
//	                             documents get the bridge bootstrap snippet
//
// Idempotent while a server is running: a second call returns the existing
// server (the new addr is ignored) so OnLoad auto-start and an explicit call
// from generated code never double-bind. To rebind on a new address, Shutdown
// first.
func ServeHTTP(addr string, opts ...ServeHTTPOption) (*HTTPServer, error) {
	cfg := serveConfig{staticDir: "."}
	for _, o := range opts {
		o(&cfg)
	}
	httpState.Lock()
	defer httpState.Unlock()
	if httpState.server != nil {
		return httpState.server, nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("sdk: serve http %s: %w", addr, err)
	}
	hub := newSSEHub()
	s := &HTTPServer{
		addr:     ln.Addr().String(),
		listener: ln,
		hub:      hub,
	}
	s.static.Store(&cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /invoke/{callableId}", s.handleInvoke)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("POST /session/bootstrap", s.handleSessionBootstrap)
	mux.HandleFunc("POST /__sdk/file-drop/{dropID}/begin", handleFileDropBegin)
	mux.HandleFunc("POST /__sdk/file-drop/{dropID}/end", handleFileDropEnd)
	mux.HandleFunc("POST /__sdk/file-drop/{dropID}/{index}", handleFileDropUpload)
	mux.HandleFunc("POST /__sdk/file-drop/host", handleFileDropHost)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.static.Load()
		dir := "."
		if cfg != nil {
			dir = cfg.staticDir
		}
		staticDirHandler(dir).ServeHTTP(w, r)
	}))
	s.srv = &http.Server{Handler: mux}
	go func() { _ = s.srv.Serve(ln) }()
	httpState.server = s
	return s, nil
}

func (s *HTTPServer) handleInvoke(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r) {
		return
	}
	callableID := r.PathValue("callableId")
	if callableID == "" {
		writeHTTPError(w, http.StatusBadRequest, "missing callable id")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	defer r.Body.Close()
	h, ok := lookupHTTPHandler(callableID)
	if !ok {
		writeHTTPError(w, http.StatusNotFound, "callable not found: "+callableID)
		return
	}
	resp, err := h(json.RawMessage(body))
	if err != nil {
		writeHTTPError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleEvents serves the event channel. Panel clients upgrade to WebSocket
// (one connection per panel, outside the browser's HTTP/1.1 pool — see
// ws.go); requests without an Upgrade header get the legacy SSE stream
// (standalone browser opens, older generated clients). Auth is transport-
// independent and runs before any bytes are streamed: the gateway proxy
// path carries X-Spore-Gateway-Token, direct same-origin opens the
// spore_session cookie — both checked here, on the handshake itself, so an
// unauthorized client never reaches the hub.
func (s *HTTPServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r) {
		return
	}
	if websocket.IsWebSocketUpgrade(r) {
		s.serveEventsWS(w, r)
		return
	}
	s.serveEventsSSE(w, r)
}

func (s *HTTPServer) serveEventsSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeHTTPError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush() // send headers immediately so the client can attach

	c := &sseConn{w: w, ch: make(chan sseMsg, sseSendBuffer)}
	s.hub.register(c)
	defer s.hub.unregister(c)
	for {
		select {
		case msg := <-c.ch:
			if err := c.writeFrame(msg); err != nil {
				return // client gone
			}
		case <-r.Context().Done():
			return
		}
	}
}

func writeHTTPError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ── Session cookie bootstrap ──

// handleSessionBootstrap installs the frontend session cookie. The host shell
// (appmanager) mints the token — sessionId + "." + hex(HMAC(secret, sessionId)),
// the same value it hands the iframe as CookieToken — and the iframe posts it
// here (same-origin) during the bridge bootstrap. Setting it as an HttpOnly
// cookie means subsequent /invoke, /events, and <video>/<audio>/<img>
// requests from the iframe carry it automatically with no script access.
//
// Auth-disabled processes (empty secret, dev mode) accept any token so the
// cookie machinery stays exercisable; with a secret configured a malformed or
// wrongly-signed token is rejected with 401 and no cookie is set.
func (s *HTTPServer) handleSessionBootstrap(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	// A malformed body is not fatal: an empty token simply means no cookie.
	_ = json.NewDecoder(r.Body).Decode(&body)
	token := body.Token
	if secret := sessionSecret(); len(secret) > 0 {
		if !originAllowed(r) {
			writeHTTPError(w, http.StatusForbidden, "cross-origin request")
			return
		}
		if !validSessionToken(secret, token) {
			writeHTTPError(w, http.StatusUnauthorized, "invalid session token")
			return
		}
	}
	if token != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     SessionCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Static files with bridge bootstrap injection ──

// BridgeBootstrapSnippet is injected into every HTML document the plugin
// serves so the host shell can attach its management-plane bridge. It is the
// SINGLE SOURCE for the snippet: the host gateway fallback
// (pkg/pluginhost/assets.go) imports this constant instead of keeping a copy,
// so the two serving paths cannot drift.
//
// On the host's "sporemind:bootstrap" postMessage it: stashes the bridge
// context, applies the theme and locale, resolves
// window.__sporemindAppBaseReady with the mount base, sets
// window.__sporemindVoiceNative=true when the host advertises its
// native-voice relay (Capacitor mobile shell), and loads the management
// bridge client from the absolute
// URL the host provides (cross-origin <script> is allowed, so a host asset
// works from this origin). __sporemindAppBaseReady is created at snippet
// load and also resolves "" on a 3s timeout, so generated data-plane code
// can await the handshake instead of racing it (panel first-fetch 404s at
// the gateway root) while standalone dev (no handshake ever) still proceeds
// with the root-relative prefix. The data path is gateway-relative (appBase
// prefix) and authenticated by the reverse proxy's gateway token — there is
// no per-origin cookie bootstrap.
//
// The bridge-client append is idempotent: the snippet can be injected twice
// into the same document (gateway fallback + plugin listener copies) and the
// host may answer ready-for-bootstrap more than once, but the bridge client
// is a classic script whose top-level declarations throw SyntaxError on a
// second evaluation. The data-sporemind-bridge marker caps it at one append
// per document.
//
// The ready-for-bootstrap announcement RETRIES (immediately, then every
// 400ms for up to 15s, stopping once a bootstrap arrives): a one-shot post
// is lost when the host shell's message listener is not yet mounted, and
// after a host restart the shell's session bind can exceed the 3s fallback
// — the panel would then boot its data plane on an empty appBase (405/404
// at the gateway) with no retry path. Retries are safe: the appBase resolve
// is done-guarded and the bridge-script append dedups, so a late duplicate
// bootstrap is inert.
//
// Pointer-release normalization (iframe boundary): a press that starts in
// the panel and releases outside the iframe never delivers mouseup to the
// panel document (the release lands in the host document, or over a native
// window), which hangs naive drag/slider/selection logic and swallows
// click. The snippet installs a capture-phase pointerdown listener that
// calls setPointerCapture on the press target, so the browser retargets all
// subsequent pointer events — and their compatibility mouse events —
// (including the release, wherever it lands) to the element that received
// the press. This is transparent to plugins: a plugin that calls
// setPointerCapture itself simply wins (pending capture is last-write-wins),
// and editable/native-control targets are skipped because the browser
// already manages their press semantics. No host-side forwarding protocol,
// no plugin opt-in.
//
// OS file drop (host → plugin trigger): drops land inside the iframe
// document — the host page never sees them — and the default drop navigates
// the iframe to the dropped file, breaking the panel. The snippet therefore
// claims every unclaimed file drop at document-capture: dragover/drop are
// preventDefault()ed when the drag carries Files, the host file browser's
// cross-panel drag MIMEs, or text/plain, and the target is not inside a
// plugin-managed drop zone ([data-file-drop-target], the same marker
// convention as the host shell's os-file-dnd), so plugin-owned dropzones are
// untouched. Dropped OS file trees are expanded (webkitGetAsEntry, dirs-
// before-children, '/'-separated relPath, bare-File fallback) and announced
// as a cancelable CustomEvent ("sporemind:file-drop") whose entries carry the
// live File objects; a frontend that preventDefaults the event has handled
// the drop itself and the backend upload is skipped. Otherwise the metadata
// is POSTed to the plugin's own listener ({appBase}/__sdk/file-drop/...):
// begin → one raw-body POST per file (JSON metadata, native binary bytes —
// no base64) → end, which invokes sdk.RegisterFileDropListener on the
// backend and mirrors the delivery as the "file_drop" event on /events.
// Drags from the host's file panels (FileBrowser / SSH FTP) carry the host
// convention text/plain marker ("x-sporemind-dnd:" + payload JSON with
// origin/name/path/isDir/projectId/sessionId) instead of Files; the snippet
// parses it and delivers the same CustomEvent (detail.origin
// "host-browser") plus a metadata-only POST to /__sdk/file-drop/host — no
// bytes, entries carry path + projectId so the plugin opens them by
// reference. Non-marker text drops pass through untouched. With no backend
// listener registered, both deliveries are refused and nothing is uploaded.
const BridgeBootstrapSnippet = `<script>(function(){` +
	`window.__sporemindAppBaseReady=new Promise(function(res){var done=false;window.__sporemindResolveAppBase=function(v){if(done)return;done=true;res(v||"")};setTimeout(function(){window.__sporemindResolveAppBase("")},3000)});` +
	`var __sporemindBooted=false;` +
	`window.addEventListener("message",function(e){` +
	`if(e.data&&e.data.type==="sporemind:bootstrap"){` +
	`__sporemindBooted=true;` +
	`window.__sporemindBridgeContext={pluginID:e.data.pluginID,viewID:e.data.viewID,parentOrigin:e.origin,appBase:e.data.appBase};` +
	`if(e.data.appBase)window.__sporemindAppBase=e.data.appBase;` +
	`if(e.data.themeMode)document.documentElement.setAttribute("data-theme",e.data.themeMode);` +
	`if(e.data.locale)document.documentElement.setAttribute("lang",e.data.locale);` +
	`if(e.data.voiceNative)window.__sporemindVoiceNative=true;` +
	`window.__sporemindResolveAppBase(e.data.appBase);` +
	`if(!document.querySelector("script[data-sporemind-bridge]")){var s=document.createElement("script");s.setAttribute("data-sporemind-bridge","1");s.src=e.data.bridgeUrl;s.async=true;document.head.appendChild(s)}` +
	`}else if(e.data&&e.data.type==="sporemind:theme-update"){` +
	`document.documentElement.setAttribute("data-theme",e.data.themeMode)` +
	`}else if(e.data&&e.data.type==="sporemind:locale-update"){` +
	`document.documentElement.setAttribute("lang",e.data.locale)` +
	`}else if(e.data&&e.data.type==="sporemind:event-suspend"||e.data&&e.data.type==="sporemind:event-resume"){` +
	`var chs=window.__sporemindEventChannels;` +
	`if(chs&&chs.forEach){chs.forEach(function(ch){var m=e.data.type==="sporemind:event-suspend"?"suspend":"resume";if(ch&&typeof ch[m]==="function")try{ch[m]()}catch(err){}})}` +
	`}});` +
	`var __sporemindReadyStart=Date.now();` +
	`(function __sporemindAnnounceReady(){` +
	`if(__sporemindBooted)return;` +
	`window.parent.postMessage({type:"sporemind:ready-for-bootstrap"},"*");` +
	`if(Date.now()-__sporemindReadyStart>15000)return;` +
	`setTimeout(__sporemindAnnounceReady,400)` +
	`})();` +
	`document.addEventListener("pointerdown",function(e){` +
	`if(e.pointerType!=="mouse"&&e.pointerType!=="pen")return;` +
	`var t=e.target;` +
	`if(!t||t.nodeType!==1||!t.setPointerCapture)return;` +
	`if(t.closest&&t.closest("input,textarea,select,[contenteditable],iframe"))return;` +
	`try{t.setPointerCapture(e.pointerId)}catch(err){}` +
	`},true);` +
	`function __sporemindFileDrag(e){` +
	`if(!e.dataTransfer)return false;` +
	`var t=e.dataTransfer.types;` +
	`if(!t)return false;` +
	`var has=function(x){return Array.prototype.indexOf.call(t,x)>=0};` +
	`if(!has("Files")&&!has("text/plain")&&!has("application/x-sporemind-file-local")&&!has("application/x-sporemind-file-remote"))return false;` +
	`var tg=e.target;` +
	`if(tg&&tg.closest&&tg.closest("[data-file-drop-target]"))return false;` +
	`return true` +
	`}` +
	`document.addEventListener("dragover",function(e){if(__sporemindFileDrag(e)){e.preventDefault();try{e.dataTransfer.dropEffect="copy"}catch(err){}}},true);` +
	`document.addEventListener("drop",function(e){if(!__sporemindFileDrag(e))return;e.preventDefault();` +
	`if(e.dataTransfer.types&&Array.prototype.indexOf.call(e.dataTransfer.types,"Files")>=0){__sporemindHandleDrop(e);return}` +
	`__sporemindHandleHostDrop(e)},true);` +
	`function __sporemindHandleDrop(e){` +
	`var dt=e.dataTransfer,captured=[],i;` +
	`for(i=0;i<(dt.items?dt.items.length:0);i++){var it=dt.items[i];if(it.kind!=="file")continue;captured.push({file:it.getAsFile(),entry:it.webkitGetAsEntry?it.webkitGetAsEntry():null})}` +
	`if(captured.length===0&&dt.files)for(i=0;i<dt.files.length;i++)captured.push({file:dt.files[i],entry:null});` +
	`__sporemindExpandDrop(captured,function(entries){` +
	`var ev=new CustomEvent("sporemind:file-drop",{cancelable:true,detail:{entries:entries}});` +
	`window.dispatchEvent(ev);` +
	`if(ev.defaultPrevented)return;` +
	`__sporemindUploadDrop(entries)` +
	`},function(err){console.warn("[sporemind] file drop expansion failed:",err)});` +
	`}` +
	`function __sporemindExpandDrop(captured,done,fail){` +
	`var entries=[],pending=1,failed=false,bail=function(err){if(failed)return;failed=true;fail(err)};` +
	`var push=function(name,rel,isDir,size,file){entries.push({name:name,relPath:rel,isDir:isDir,size:size,file:file})};` +
	`var out=function(){if(--pending===0&&!failed)done(entries)};` +
	`var walk=function(entry,rel){` +
	`pending++;` +
	`if(entry.isDirectory){` +
	`push(entry.name,rel,true,0,null);` +
	`var reader=entry.createReader(),batch=function(){reader.readEntries(function(list){` +
	`if(!list.length){out();return}` +
	`for(var j=0;j<list.length;j++)walk(list[j],rel+"/"+list[j].name);` +
	`batch()` +
	`},function(err){bail(err)})};` +
	`batch()` +
	`}else{` +
	`entry.file(function(f){push(f.name||entry.name,rel,false,f.size,f);out()},function(err){bail(err)})` +
	`}` +
	`};` +
	`for(var i=0;i<captured.length;i++){` +
	`var c=captured[i];` +
	`if(c.entry){var full=String(c.entry.fullPath||c.entry.name||"");var rel=full.replace(/^\/+/,"");walk(c.entry,rel||c.entry.name||"file")}` +
	`else if(c.file)push(c.file.name,c.file.name,false,c.file.size,c.file)` +
	`}` +
	`out()` +
	`}` +
	`function __sporemindUploadDrop(entries){` +
	`var id="";for(var i=0;i<16;i++)id+=Math.floor(Math.random()*16).toString(16);` +
	`window.__sporemindAppBaseReady.then(function(base){` +
	`var meta=entries.map(function(en){return{name:en.name,relPath:en.relPath,isDir:en.isDir,size:en.size}});` +
	`fetch(base+"/__sdk/file-drop/"+id+"/begin",{method:"POST",credentials:"same-origin",headers:{"Content-Type":"application/json"},body:JSON.stringify({entries:meta})})` +
	`.then(function(r){if(!r.ok)throw new Error("file drop begin HTTP "+r.status);return r.json()})` +
	`.then(function(res){` +
	`if(!res||!res.accepted)return;` +
	`var chain=Promise.resolve();` +
	`entries.forEach(function(en,idx){` +
	`if(en.isDir)return;` +
	`chain=chain.then(function(){return en.file.arrayBuffer().then(function(buf){` +
	`return fetch(base+"/__sdk/file-drop/"+id+"/"+idx,{method:"POST",credentials:"same-origin",headers:{"Content-Type":"application/octet-stream"},body:buf})` +
	`.then(function(r){if(!r.ok)throw new Error("file drop upload "+en.relPath+" HTTP "+r.status)})` +
	`})})` +
	`});` +
	`return chain` +
	`.then(function(){return fetch(base+"/__sdk/file-drop/"+id+"/end",{method:"POST",credentials:"same-origin"})})` +
	`.then(function(r){if(!r.ok)throw new Error("file drop end HTTP "+r.status)})` +
	`})` +
	`.catch(function(err){console.warn("[sporemind] file drop delivery failed:",err)})` +
	`})` +
	`}` +
	`function __sporemindHandleHostDrop(e){` +
	`var dt=e.dataTransfer,raw="";` +
	`try{raw=dt.getData("text/plain")||""}catch(err){return}` +
	`var mark="x-sporemind-dnd:";` +
	`if(raw.slice(0,mark.length)!==mark)return;` +
	`var items;` +
	`try{var p=JSON.parse(raw.slice(mark.length));items=Array.isArray(p)?p:[p]}catch(err){return}` +
	`var entries=[];` +
	`for(var i=0;i<items.length;i++){var it=items[i];if(!it||!it.path)continue;entries.push({name:it.name||it.path,isDir:!!it.isDir,path:it.path,projectId:it.projectId||"",sessionId:it.sessionId||"",origin:it.origin||"local"})}` +
	`if(entries.length===0)return;` +
	`var ev=new CustomEvent("sporemind:file-drop",{cancelable:true,detail:{origin:"host-browser",entries:entries}});` +
	`window.dispatchEvent(ev);` +
	`if(ev.defaultPrevented)return;` +
	`window.__sporemindAppBaseReady.then(function(base){` +
	`return fetch(base+"/__sdk/file-drop/host",{method:"POST",credentials:"same-origin",headers:{"Content-Type":"application/json"},body:JSON.stringify({entries:entries})})` +
	`.then(function(r){if(!r.ok)throw new Error("file drop host HTTP "+r.status)})` +
	`}).catch(function(err){console.warn("[sporemind] host file drop delivery failed:",err)})` +
	`}` +
	`})();</script>`

// injectBridgeBootstrap inserts the bridge bootstrap snippet into an HTML
// document, before </head> when present, else </body>, else </html>, else
// appended at the end. Candidate closers are matched only when they are real
// markup closes: a </head> literal inside an HTML comment or a script block
// (e.g. a JS line comment) must not hijack the injection point — the snippet
// would land inside the comment and silently disable appBase injection,
// cookie minting, and the bridge client load.
func injectBridgeBootstrap(data []byte) []byte {
	lower := bytes.ToLower(data)
	for _, tag := range []string{"</head>", "</body>", "</html>"} {
		if idx := realCloseIndex(lower, tag); idx >= 0 {
			result := make([]byte, 0, len(data)+len(BridgeBootstrapSnippet))
			result = append(result, data[:idx]...)
			result = append(result, BridgeBootstrapSnippet...)
			result = append(result, data[idx:]...)
			return result
		}
	}
	return append(data, BridgeBootstrapSnippet...)
}

// realCloseIndex returns the offset of the first occurrence of tag that is not
// inside an HTML comment (<!-- -->) or a script block (<script ...>
// </script>). lower must be an already-lowercased copy of the document.
func realCloseIndex(lower []byte, tag string) int {
	t := []byte(tag)
	n := len(lower)
	for i := 0; i < n; i++ {
		if lower[i] != '<' {
			continue
		}
		rest := lower[i:]
		if bytes.HasPrefix(rest, []byte("<!--")) {
			end := bytes.Index(rest[4:], []byte("-->"))
			if end < 0 {
				return -1
			}
			i += 4 + end + 3 - 1
			continue
		}
		if bytes.HasPrefix(rest, []byte("<script")) {
			after := lower[i+7:]
			if len(after) == 0 || after[0] == '>' || after[0] == '/' || after[0] == ' ' || after[0] == '\t' {
				end := bytes.Index(rest, []byte("</script>"))
				if end < 0 {
					return -1
				}
				i += end + len("</script>") - 1
				continue
			}
		}
		if bytes.HasPrefix(rest, t) {
			return i
		}
	}
	return -1
}

// staticHandler serves the app directory like http.FileServer, except that
// HTML documents (the entry page) get the bridge bootstrap snippet injected
// and are served no-store so a reload-swapped asset bundle is always picked
// up. Non-HTML paths fall through to the file server untouched (range
// requests, media streaming, etc.).
//
// When a session secret is configured, static assets are no longer public:
// a valid gateway proxy token header or a valid session cookie is required,
// anything else gets 401. Browsers reach the assets exclusively through the
// gateway reverse proxy (which injects the token header); direct probes at
// the ephemeral listener are rejected. Dev mode (no secret) keeps the
// previous public behavior so standalone dev is unaffected.
// staticDirHandler serves the app's static files from dir. Directory
// listings are disabled (fail-closed): a directory request serves its
// index.html when present and answers 404 otherwise — the plugin's HTTP
// listener must never enumerate the host filesystem through the gateway.
func staticDirHandler(dir string) http.Handler {
	root := http.Dir(dir)
	fileServer := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if secret := sessionSecret(); len(secret) > 0 && !staticAuthorized(secret, r) {
			writeHTTPError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && wantsHTMLDoc(r.URL.Path) {
			if data, ok := readHTMLDoc(root, r.URL.Path); ok {
				data = injectBridgeBootstrap(data)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Content-Length", strconv.Itoa(len(data)))
				w.Header().Set("Cache-Control", "no-store")
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusOK)
					return
				}
				_, _ = w.Write(data)
				return
			}
		}
		// Non-HTML paths (and HTML paths with no file): plain files only.
		// A resolved directory would fall to the FileServer's directory
		// listing — serve its index.html if any, else 404.
		cleaned := path.Clean("/" + r.URL.Path)
		if f, err := root.Open(cleaned); err == nil {
			info, statErr := f.Stat()
			f.Close()
			if statErr == nil && info.IsDir() {
				if data, ok := readHTMLDoc(root, path.Join(cleaned, "index.html")); ok {
					data = injectBridgeBootstrap(data)
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Header().Set("Content-Length", strconv.Itoa(len(data)))
					w.Header().Set("Cache-Control", "no-store")
					_, _ = w.Write(data)
					return
				}
				writeHTTPError(w, http.StatusNotFound, "not found")
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

func wantsHTMLDoc(p string) bool {
	p = strings.ToLower(p)
	return p == "/" || strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".htm")
}

// readHTMLDoc reads the HTML document for a request path: the file itself
// when it is an HTML file, or the directory's index.html for directory paths.
// http.Dir anchors and cleans paths, so traversal segments cannot escape the
// served directory. Existence failures fall through to the file server (which
// produces the proper 404/redirect semantics).
func readHTMLDoc(root http.Dir, urlPath string) ([]byte, bool) {
	cleaned := path.Clean("/" + urlPath)
	for _, candidate := range []string{cleaned, path.Join(cleaned, "index.html")} {
		f, err := root.Open(candidate)
		if err != nil {
			continue
		}
		info, err := f.Stat()
		if err != nil || info.IsDir() {
			f.Close()
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			continue
		}
		return data, true
	}
	return nil, false
}

// ── Cookie authentication ──

// SessionCookieName is the cookie carrying the plugin session token. It is
// HttpOnly and SameSite=Lax so <video>/<audio>/<img> tags and fetch in the
// same-origin iframe carry it automatically, while script cannot read it.
const SessionCookieName = "spore_session"

// GatewayTokenHeader is the header carrying the gateway proxy token. The
// gateway (single ingress reverse-proxying /plugin/{id}/ to plugin listeners)
// injects it on every proxied request so the plugin listener can treat the
// gateway as a trusted internal caller. The token value is
// "gateway-proxy." + hex(HMAC-SHA256(instanceSecret, "gateway-proxy")) —
// i.e. MintSessionToken(secret, "gateway-proxy") — verified with the same
// HMAC scheme as session cookies.
const GatewayTokenHeader = "X-Spore-Gateway-Token"

// authState holds the per-instance HMAC secret used to mint and verify session
// cookies. An empty secret means cookie auth is disabled (dev mode / no host
// config) — every request is allowed. The host (pluginhost/appmanager, T4)
// pushes the secret down via the OnLoad config (LoadConfig.SessionSecret).
var authState = struct {
	sync.RWMutex
	secret []byte
}{}

// SetSessionSecret configures the HMAC secret used to mint/verify session
// cookies. An empty string disables cookie auth (dev mode). The value may be
// hex-encoded (preferred) or a raw string. This is called automatically from
// applyLoadConfig during OnLoad; it is exported for tests and explicit setup.
func SetSessionSecret(s string) {
	var b []byte
	if s != "" {
		if dec, err := hex.DecodeString(s); err == nil && len(dec) > 0 {
			b = dec
		} else {
			b = []byte(s)
		}
	}
	authState.Lock()
	authState.secret = b
	authState.Unlock()
}

func sessionSecret() []byte {
	authState.RLock()
	defer authState.RUnlock()
	return authState.secret
}

// MintSessionToken returns "sessionId.hex(HMAC-SHA256(secret, sessionId))" —
// the value to place in the spore_session cookie. Exported so tests (and the
// host's bootstrap Set-Cookie, T4) can mint a valid token.
func MintSessionToken(secret []byte, sessionID string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sessionID))
	return sessionID + "." + hex.EncodeToString(mac.Sum(nil))
}

// validSessionToken verifies a "sessionId.hexMAC" token against the secret with
// constant-time comparison. Malformed tokens (no dot, bad hex) are rejected.
func validSessionToken(secret []byte, token string) bool {
	idx := strings.IndexByte(token, '.')
	if idx < 1 || idx == len(token)-1 {
		return false
	}
	sessionID := token[:idx]
	gotMAC, err := hex.DecodeString(token[idx+1:])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sessionID))
	return hmac.Equal(mac.Sum(nil), gotMAC)
}

// authorizeRequest checks the session cookie. Returns true (allow) when auth is
// disabled (no secret) or the cookie is valid; writes 401 and returns false
// otherwise. A cross-origin Origin header (defense-in-depth against
// SameSite=Lax weakening) yields 403. A request carrying the gateway proxy
// token header is a trusted internal caller: a valid token allows it and
// skips the Origin check (the gateway's forwarded Origin/Host is unrelated
// to the plugin's own origin), an invalid one is rejected with 401. The
// caller stops processing on false.
func authorizeRequest(w http.ResponseWriter, r *http.Request) bool {
	secret := sessionSecret()
	if len(secret) == 0 {
		return true // dev mode: no secret configured
	}
	if tok := r.Header.Get(GatewayTokenHeader); tok != "" {
		if !validSessionToken(secret, tok) {
			writeHTTPError(w, http.StatusUnauthorized, "unauthorized")
			return false
		}
		return true
	}
	if !originAllowed(r) {
		writeHTTPError(w, http.StatusForbidden, "cross-origin request")
		return false
	}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie == nil || !validSessionToken(secret, cookie.Value) {
		writeHTTPError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}

// staticAuthorized reports whether a static-asset request may be served when
// a secret is configured: a valid gateway proxy token header (the trusted
// reverse-proxy path) or a valid session cookie (a direct same-origin open
// of the listener). Unlike authorizeRequest it applies no Origin check — asset requests
// (media tags, top-level navigations) legitimately carry no or foreign
// Origin headers, and the token/cookie verification is the authorization.
func staticAuthorized(secret []byte, r *http.Request) bool {
	if tok := r.Header.Get(GatewayTokenHeader); tok != "" {
		return validSessionToken(secret, tok)
	}
	cookie, err := r.Cookie(SessionCookieName)
	return err == nil && cookie != nil && validSessionToken(secret, cookie.Value)
}

// originAllowed reports whether a request carrying an Origin header comes
// from this listener's own origin. The app frontend is loaded from, and talks
// to, this same listener (sandboxed iframe with allow-same-origin), so its
// fetch/EventSource requests carry Origin = http://<listener-host>. Any other
// Origin — a cross-origin fetch from a website on the developer's machine,
// which SameSite=Lax already blocks at the cookie layer — is rejected here as
// defense-in-depth. Requests without an Origin header (top-level navigations,
// plain GETs) pass; the cookie check still applies. Only called when a secret
// is set, so standalone dev opens are unaffected.
func originAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	return strings.EqualFold(origin, "http://"+r.Host)
}

// ── OnLoad config (host → SDK) ──

// LoadConfig is the JSON payload of the plugin's OnLoad lifecycle hook,
// delivered by the host (pluginhost/appmanager, T4) at spawn. The SDK applies
// it itself, independent of the app author's OnLoad hook. The contract is
// owned here (the SDK is the consumer); the host produces matching JSON.
//
// Wire shape:
//
//	{"httpAddr": "127.0.0.1:0", "sessionSecret": "<hex or raw>",
//	 "staticDir": "<app dir>", "dataDir": "<appdata dir>"}
type LoadConfig struct {
	// HTTPAddr, when non-empty, asks the SDK to start its HTTP listener on
	// this address after the app's OnLoad hook returns (so registered
	// handlers are in place before traffic arrives). Use ":0" (or
	// "127.0.0.1:0") for an ephemeral port; the bound address is reported
	// back to the host in the OnLoad response as {"httpAddr": "..."}.
	HTTPAddr string `json:"httpAddr,omitempty"`
	// SessionSecret mints and verifies the frontend session cookie
	// (HMAC-SHA256). Empty disables cookie auth (dev mode). Accepts a
	// hex-encoded or raw string.
	SessionSecret string `json:"sessionSecret,omitempty"`
	// StaticDir anchors the "/" static route to the app directory (the
	// plugin process inherits the host's cwd, so the SDK's "." default
	// would serve whatever directory the host happens to run from — the
	// 2026-09 run-directory leak). It replaces the "." default only; an
	// explicit WithStaticDir in app code always wins.
	StaticDir string `json:"staticDir,omitempty"`
	// DataDir is the app-private writable data directory granted when the
	// app manifest declares the app.data capability:
	// <appDir>/.sporecode/appdata. Unlike StaticDir it is never HTTP-served
	// — the app owns it exclusively for its own files and embedded
	// databases (e.g. goleveldb: open it under sdk.DataDir()). Empty when
	// undeclared or the host could not derive a stable app directory.
	DataDir string `json:"dataDir,omitempty"`
}

// dataDir carries the host-pushed app-private data directory (see
// LoadConfig.DataDir); read it via DataDir().
var dataDir atomic.Value

// DataDir returns the app-private writable data directory granted by the host
// (non-empty only when the app manifest declares the app.data capability).
// The directory is created by the host before the plugin loads; use it for
// app-owned files and embedded databases (e.g. goleveldb). It survives
// unload/reload/re-register.
func DataDir() string {
	v, _ := dataDir.Load().(string)
	return v
}

// applyLoadConfig parses the host-pushed OnLoad config and applies the
// HTTP-listener settings. It returns the requested HTTP address (for the caller
// to start the listener after the app's OnLoad). Empty, blank, or unparseable
// config is a no-op (backward compatible with hosts that send no config).
func applyLoadConfig(raw json.RawMessage) (pendingHTTPAddr string) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	var cfg LoadConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		Log(LogLevelWarn, "onload config: parse: %v", err)
		return ""
	}
	if cfg.SessionSecret != "" {
		SetSessionSecret(cfg.SessionSecret)
	}
	if cfg.StaticDir != "" {
		SetStaticRoot(cfg.StaticDir)
	}
	if cfg.DataDir != "" {
		dataDir.Store(cfg.DataDir)
	}
	return cfg.HTTPAddr
}

// maybeAutoStartHTTP starts the HTTP listener when the host asked for one and
// no server is running yet. Called from HandleOnLoad after the app's OnLoad
// hook so handlers are registered first.
func maybeAutoStartHTTP(addr string) {
	if strings.TrimSpace(addr) == "" {
		return
	}
	if currentHTTPServer() != nil {
		return // an explicit ServeHTTP already started one
	}
	var opts []ServeHTTPOption
	if pending, ok := pendingStaticDir.Load().(string); ok && pending != "" {
		// Host-pushed root, not an app choice: do not mark it explicit, so a
		// later SetStaticRoot (config change) can still replace it.
		opts = append(opts, func(c *serveConfig) { c.staticDir = pending })
		pendingStaticDir.Store("")
	}
	srv, err := ServeHTTP(addr, opts...)
	if err != nil {
		Log(LogLevelError, "onload: serve http %s: %v", addr, err)
		return
	}
	Log(LogLevelInfo, "onload: http listener on %s", srv.Addr())
}

// broadcastSSE pushes an event to every connected frontend over the running
// listener's SSE hub. Best-effort: if no listener is running it is a no-op, so
// EmitEvent remains callable in tests / transports without an HTTP server.
func broadcastSSE(kind string, payload []byte) {
	srv := currentHTTPServer()
	if srv == nil {
		return
	}
	srv.hub.Broadcast(kind, payload)
}
