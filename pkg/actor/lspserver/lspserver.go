package lspserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/persist"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/util"
)

// rootServer tracks one language engine bound to a workspace root.
type rootServer struct {
	server      LanguageEngine
	initialized bool
	initResult  json.RawMessage
	lastUsed    atomic.Int64
	// inFlight counts queries currently executing against this engine.
	// LRU eviction skips engines with in-flight work so a slow RPC is never
	// racing its own Shutdown.
	inFlight atomic.Int64
}

func (rs *rootServer) touch() {
	rs.lastUsed.Store(time.Now().UnixNano())
}

const maxServers = 4

// knownLanguages lists the languages the actor can serve. TypeScript and
// JavaScript are backed by the same engine implementation but tracked as
// separate per-language slots so the settings panel can toggle them independently.
var knownLanguages = []string{"go", "python", "rust", "cpp", "typescript", "javascript", "css", "html", "json", "bash"}

// defaultEnabled returns a fresh map with every known language enabled.
func defaultEnabled() map[string]bool {
	return map[string]bool{"go": true, "python": true, "rust": true, "cpp": true, "typescript": true, "javascript": true, "css": true, "html": true, "json": true, "bash": true}
}

// Actor manages per-workspace, per-language language engines and exposes their
// capabilities as gateway callables under the "lsp" domain. Diagnostics computed
// by the engines are forwarded to subscribers as "lsp.diagnostics" events.
//
// Lifecycle handlers (initialize/shutdown/clear_cache) are stateful
// (actor.Context) and therefore serialized through the actor mailbox; query
// handlers are stateless (PureContext) and run concurrently against a
// read-locked view of the server map.
//
// At most maxServers engines are kept alive across all roots and languages;
// the least-recently-used engine is evicted (shut down) when a new language
// engine is initialized. Each access via serverFor bumps lastUsed so actively
// queried engines survive.
type Actor struct {
	actor.Host
	mu      sync.RWMutex
	servers map[string]map[string]*rootServer // rootURI -> language -> engine
	emit    func(kind string, payload any) error

	stateMu sync.RWMutex
	enabled map[string]bool
	store   persist.Persist
	actorID string

	// installMu guards the managed-install cache and in-flight set.
	installMu sync.RWMutex
	installs  map[string]managedInstall // language -> last completed managed install
	inFlight  map[string]bool           // language -> install goroutine running

	// specs maps language -> probe/install spec, seeded from
	// defaultLanguageSpecs in OnStart. Tests may pre-populate it.
	specs map[string]languageSpec

	// managedRootOverride replaces {dataDir}/lsp as the managed-install root;
	// test-only, empty in production.
	managedRootOverride string

	// newEngine is the engine factory used by handleInitialize, defaulting to
	// newEngineFor. Tests override it to inject fake engines without spawning
	// gopls/tsserver. lspRoot is the actor's managed-install root so engines
	// and lsp.install read the same directory tree.
	newEngine func(language string, lspRoot string, ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte)) (LanguageEngine, error)
}

func (a *Actor) Type() string { return "lspserver" }

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("lspserver: starting")

	a.servers = make(map[string]map[string]*rootServer)
	a.emit = ctx.EmitEvent
	a.actorID = ctx.Self().ID().String()
	a.enabled = defaultEnabled()
	a.installs = make(map[string]managedInstall)
	a.inFlight = make(map[string]bool)
	a.specs = defaultLanguageSpecs()

	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("lspserver"))
		if err != nil {
			return err
		}
	}
	if err := a.Load(); err != nil {
		ctx.Logger().Error("lspserver: load state failed, starting with default per-language enabled flags", "error", err)
	}

	if err := ctx.RegisterEventKind("lsp.diagnostics", gen.LspDiagnosticsEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("lspserver: register event kind: %w", err)
	}
	if err := ctx.RegisterEventKind(installProgressEvent, gen.LspInstallProgressEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("lspserver: register event kind: %w", err)
	}

	if err := a.registerCallables(ctx); err != nil {
		return err
	}

	if err := ctx.RegisterDomain("lsp").Expose(); err != nil {
		return fmt.Errorf("lspserver: expose service: %w", err)
	}

	ctx.Logger().Info("lspserver: started")
	return nil
}

func (a *Actor) OnStop(ctx actor.Context) error {
	ctx.Logger().Info("lspserver: stopping")
	a.mu.Lock()
	var targets []*rootServer
	for _, engines := range a.servers {
		for _, rs := range engines {
			targets = append(targets, rs)
		}
	}
	a.servers = make(map[string]map[string]*rootServer)
	a.mu.Unlock()
	for _, rs := range targets {
		_ = rs.server.Shutdown(context.Background())
	}
	if err := a.Save(); err != nil {
		ctx.Logger().Error("lspserver: save state failed on stop", "error", err)
	}
	return nil
}

// --- persisted enabled state ---

func (a *Actor) Save() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("lspserver"))
		if err != nil {
			return err
		}
	}
	a.stateMu.RLock()
	enabled := make(map[string]bool, len(a.enabled))
	for lang, v := range a.enabled {
		enabled[lang] = v
	}
	a.stateMu.RUnlock()
	return a.store.Save(a.actorID, map[string]any{"enabled": enabled, "installs": a.installSnapshot()})
}

func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("lspserver"))
		if err != nil {
			return err
		}
	}
	// Use json.RawMessage so we can detect the legacy single-bool shape
	// ({"enabled": true/false}) without a decode error that would rename the
	// state file to .corrupt. installs carries the managed-install cache
	// (see install.go); absent in older documents.
	var raw struct {
		Enabled  json.RawMessage `json:"enabled"`
		Installs json.RawMessage `json:"installs"`
	}
	if err := persist.LoadOrZero(a.store, a.actorID, &raw); err != nil {
		return err
	}

	// Managed-install cache decodes independently of the enabled flags so a
	// stateless document still restores it.
	installs := decodeInstalls(raw.Installs)
	a.installMu.Lock()
	a.installs = installs
	a.installMu.Unlock()

	if len(raw.Enabled) == 0 {
		return nil
	}

	enabled := defaultEnabled()
	var perLang map[string]bool
	if err := json.Unmarshal(raw.Enabled, &perLang); err == nil {
		for lang, v := range perLang {
			enabled[lang] = v
		}
	} else {
		var legacy *bool
		if err := json.Unmarshal(raw.Enabled, &legacy); err == nil && legacy != nil {
			enabled["go"] = *legacy
		}
	}

	a.stateMu.Lock()
	a.enabled = enabled
	a.stateMu.Unlock()
	return nil
}

// normalizeLanguage returns the routing key for a request Language field. Empty
// values fall back to "go" so legacy clients that do not pass the field keep
// working against the Go engine.
func normalizeLanguage(lang string) string {
	if lang == "" {
		return "go"
	}
	return lang
}

// isEnabled reports whether the actor is allowed to initialize new engines for
// the given language.
func (a *Actor) isEnabled(lang string) bool {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.enabled[lang]
}

// stateSnapshotLocked returns the enabled state for all known languages. Caller
// must hold a.stateMu (read or write).
func (a *Actor) stateSnapshotLocked() []gen.LspLanguageState {
	out := make([]gen.LspLanguageState, 0, len(knownLanguages))
	for _, lang := range knownLanguages {
		out = append(out, gen.LspLanguageState{Language: lang, Enabled: a.enabled[lang]})
	}
	return out
}

// engineCountLocked returns the total number of running engines across all
// roots and languages. Caller must hold a.mu (read or write).
func (a *Actor) engineCountLocked() int {
	n := 0
	for _, engines := range a.servers {
		n += len(engines)
	}
	return n
}

// serverFor resolves the language engine for a request. An empty rootUri falls
// back to the sole registered root when it contains an engine for the requested
// language. With multiple roots an explicit rootUri is required.
//
// The returned done func must be called when the request finishes (typically
// via defer): it clears the in-flight marker that shields the engine from LRU
// eviction and refreshes its recency. Engines whose initialize handshake has
// not completed yet are not returned — the caller gets an "initializing" error.
func (a *Actor) serverFor(rootUri, language string) (LanguageEngine, func(), error) {
	lang := normalizeLanguage(language)
	a.mu.RLock()
	var (
		srv      LanguageEngine
		rs       *rootServer
		found    bool
		initing  bool
		numRoots = len(a.servers)
	)
	if rootUri != "" {
		if engines, ok := a.servers[rootUri]; ok {
			if e, ok := engines[lang]; ok {
				if e.initialized {
					srv, rs, found = e.server, e, true
				} else {
					initing = true
				}
			}
		}
	} else if numRoots == 1 {
		for _, engines := range a.servers {
			if e, ok := engines[lang]; ok {
				if e.initialized {
					srv, rs, found = e.server, e, true
				} else {
					initing = true
				}
			}
		}
	}
	a.mu.RUnlock()

	switch {
	case found:
		rs.touch()
		rs.inFlight.Add(1)
		return srv, func() { rs.inFlight.Add(-1); rs.touch() }, nil
	case initing:
		return nil, nil, fmt.Errorf("lsp: %s engine for root %q is still initializing", lang, rootUri)
	case rootUri != "":
		return nil, nil, fmt.Errorf("lsp: no %s server initialized for root %q", lang, rootUri)
	default:
		return nil, nil, fmt.Errorf("lsp: rootUri required (%d roots registered)", numRoots)
	}
}

func (a *Actor) registerCallables(ctx actor.Context) error {
	posParams := []actor.ParamDesc{
		{Name: "uri", Description: "Document URI."},
		{Name: "line", Description: "Zero-based line number."},
		{Name: "character", Description: "Zero-based character offset."},
	}

	languageParam := actor.ParamDesc{Name: "language", Description: "Language key (e.g. \"go\", \"typescript\")."}
	posParamsWithLanguage := append(append([]actor.ParamDesc(nil), posParams...), languageParam)

	registrations := []func() error{
		func() error {
			return ctx.Register("lsp.initialize", a.handleInitialize, actor.Public(), actor.WithDescription("Initialize the LSP server for a workspace root and language."), actor.WithParams(actor.ParamDesc{Name: "rootUri", Description: "Workspace root URI."}, languageParam))
		},
		func() error {
			return ctx.Register("lsp.did_open", a.handleDidOpen, actor.Public(), actor.WithDescription("Notify the server that a document was opened."), actor.WithParams(actor.ParamDesc{Name: "uri", Description: "Document URI."}, actor.ParamDesc{Name: "languageId", Description: "Language identifier (e.g. \"go\")."}, actor.ParamDesc{Name: "text", Description: "Full document text."}, actor.ParamDesc{Name: "version", Description: "Document version number."}, actor.ParamDesc{Name: "rootUri", Description: "Workspace root URI."}, languageParam))
		},
		func() error {
			return ctx.Register("lsp.did_change", a.handleDidChange, actor.Public(), actor.WithDescription("Notify the server that a document changed."), actor.WithParams(actor.ParamDesc{Name: "uri", Description: "Document URI."}, actor.ParamDesc{Name: "version", Description: "Document version number."}, actor.ParamDesc{Name: "changes", Description: "JSON-encoded content changes."}, actor.ParamDesc{Name: "rootUri", Description: "Workspace root URI."}, languageParam))
		},
		func() error {
			return ctx.Register("lsp.did_close", a.handleDidClose, actor.Public(), actor.WithDescription("Notify the server that a document was closed."), actor.WithParams(actor.ParamDesc{Name: "uri", Description: "Document URI."}, actor.ParamDesc{Name: "rootUri", Description: "Workspace root URI."}, languageParam))
		},
		func() error {
			return ctx.Register("lsp.definition", a.handleDefinition, actor.Public(), actor.WithDescription("Go to definition at a position."), actor.WithParams(posParamsWithLanguage...))
		},
		func() error {
			return ctx.Register("lsp.hover", a.handleHover, actor.Public(), actor.WithDescription("Get hover information at a position."), actor.WithParams(posParamsWithLanguage...))
		},
		func() error {
			return ctx.Register("lsp.completion", a.handleCompletion, actor.Public(), actor.WithDescription("Get completion items at a position."), actor.WithParams(posParamsWithLanguage...))
		},
		func() error {
			return ctx.Register("lsp.references", a.handleReferences, actor.Public(), actor.WithDescription("Find references at a position."), actor.WithParams(actor.ParamDesc{Name: "uri", Description: "Document URI."}, actor.ParamDesc{Name: "line", Description: "Zero-based line number."}, actor.ParamDesc{Name: "character", Description: "Zero-based character offset."}, actor.ParamDesc{Name: "includeDeclaration", Description: "Include the declaration in results."}, languageParam))
		},
		func() error {
			return ctx.Register("lsp.implementation", a.handleImplementation, actor.Public(), actor.WithDescription("Find implementations at a position."), actor.WithParams(posParamsWithLanguage...))
		},
		func() error {
			return ctx.Register("lsp.type_definition", a.handleTypeDefinition, actor.Public(), actor.WithDescription("Go to type definition at a position."), actor.WithParams(posParamsWithLanguage...))
		},
		func() error {
			return ctx.Register("lsp.document_symbol", a.handleDocumentSymbol, actor.Public(), actor.WithDescription("Get document symbols for a file."), actor.WithParams(actor.ParamDesc{Name: "uri", Description: "Document URI."}, languageParam))
		},
		func() error {
			return ctx.Register("lsp.signature_help", a.handleSignatureHelp, actor.Public(), actor.WithDescription("Get signature help at a position."), actor.WithParams(posParamsWithLanguage...))
		},
		func() error {
			return ctx.Register("lsp.formatting", a.handleFormatting, actor.Public(), actor.WithDescription("Format a document."), actor.WithParams(actor.ParamDesc{Name: "uri", Description: "Document URI."}, languageParam))
		},
		func() error {
			return ctx.Register("lsp.rename", a.handleRename, actor.Public(), actor.WithDescription("Rename the symbol at a position."), actor.WithParams(actor.ParamDesc{Name: "uri", Description: "Document URI."}, actor.ParamDesc{Name: "line", Description: "Zero-based line number."}, actor.ParamDesc{Name: "character", Description: "Zero-based character offset."}, actor.ParamDesc{Name: "newName", Description: "New name for the symbol."}, languageParam))
		},
		func() error {
			return ctx.Register("lsp.prepare_rename", a.handlePrepareRename, actor.Public(), actor.WithDescription("Prepare a rename at a position."), actor.WithParams(posParamsWithLanguage...))
		},
		func() error {
			return ctx.Register("lsp.code_action", a.handleCodeAction, actor.Public(), actor.WithDescription("Get code actions for a range."), actor.WithParams(actor.ParamDesc{Name: "uri", Description: "Document URI."}, actor.ParamDesc{Name: "startLine", Description: "Zero-based start line."}, actor.ParamDesc{Name: "startChar", Description: "Zero-based start character."}, actor.ParamDesc{Name: "endLine", Description: "Zero-based end line."}, actor.ParamDesc{Name: "endChar", Description: "Zero-based end character."}, languageParam))
		},
		func() error {
			return ctx.Register("lsp.warm_up", a.handleWarmUp, actor.Public(), actor.WithDescription("Preload the package containing a document so subsequent queries are memory-fast."), actor.WithParams(actor.ParamDesc{Name: "uri", Description: "Document URI."}, actor.ParamDesc{Name: "rootUri", Description: "Workspace root URI."}, languageParam))
		},
		func() error {
			return ctx.Register("lsp.shutdown", a.handleShutdown, actor.Public(), actor.WithDescription("Shut down the LSP server for a workspace root and language (all languages if language is empty, all servers if rootUri is empty)."), actor.WithParams(actor.ParamDesc{Name: "rootUri", Description: "Workspace root URI; empty shuts down all servers."}, languageParam))
		},
		func() error {
			return ctx.Register("lsp.shutdown_by_root", a.handleShutdownByRoot, actor.Internal(), actor.WithDescription("Shut down every language server whose workspace root URI decodes to a path at or under RootPath. Internal: used to release LSP process handles before worktree directory deletion."))
		},
		func() error {
			return ctx.Register("lsp.clear_cache", a.handleClearCache, actor.Public(), actor.WithDescription("Shut down every LSP server and free all cached analysis data. Servers are re-created lazily on the next lsp.initialize."))
		},
		func() error {
			return ctx.Register("lsp.state_get", a.handleStateGet, actor.Public(), actor.WithDescription("Get the LSP service enabled state."))
		},
		func() error {
			return ctx.Register("lsp.state_save", a.handleStateSave, actor.Public(), actor.WithDescription("Enable or disable the LSP service for a language. When disabled, all running language engines are shut down and new lsp.initialize calls are refused."), actor.WithParams(actor.ParamDesc{Name: "Language", Description: "Language key (e.g. \"go\", \"typescript\")."}, actor.ParamDesc{Name: "Enabled", Description: "Whether the LSP service is enabled for this language."}))
		},
		func() error {
			return ctx.Register("lsp.status", a.handleStatus, actor.Public(), actor.WithDescription("Report per-language language-server install state (managed downloads and native PATH discovery). Empty language reports every supported language."), actor.WithParams(actor.ParamDesc{Name: "language", Description: "Language key (e.g. \"go\", \"typescript\"); empty means all languages."}))
		},
		func() error {
			return ctx.Register("lsp.install", a.handleInstall, actor.Public(), actor.WithDescription("Trigger an async managed download of the language server for a language. Returns immediately; progress streams via lsp.install_progress events and the final state is visible via lsp.status."), actor.WithParams(languageParam))
		},
	}

	for i, reg := range registrations {
		if err := reg(); err != nil {
			return fmt.Errorf("lspserver: registration %d: %w", i, err)
		}
	}
	return nil
}

// diagnosticsSink adapts language-server diagnostic notifications into actor
// events. The callback shape matches the LanguageEngine diagnostics contract.
func (a *Actor) diagnosticsSink() func(uri string, version int32, diagnosticsJSON []byte) {
	return func(uri string, version int32, diagnosticsJSON []byte) {
		if a.emit == nil {
			return
		}
		_ = a.emit("lsp.diagnostics", gen.LspDiagnosticsEvent{
			Uri:         uri,
			Version:     version,
			Diagnostics: string(diagnosticsJSON),
		})
	}
}

// newEngineFor builds the LanguageEngine implementation chosen for the language
// key. "cpp" is served by ClangdEngine, "typescript" and "javascript" by
// TSEngine, "python" by PYEngine, "rust" by RSEngine, "css"/"html"/"json" by
// WEBEngine, "bash" by BashEngine, and "go" by GoEngine.
func newEngineFor(language string, lspRoot string, ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte)) (LanguageEngine, error) {
	switch language {
	case "go":
		return NewGoEngine(ctx, log, diagnostics)
	case "typescript", "javascript":
		return NewTSEngine(ctx, log, diagnostics, TSEngineConfig{ManagedRoot: lspRoot})
	case "python":
		return NewPYEngine(ctx, log, diagnostics, PYEngineConfig{ManagedRoot: lspRoot})
	case "rust":
		return NewRSEngine(ctx, log, diagnostics, RSEngineConfig{ManagedRoot: lspRoot})
	case "cpp":
		return NewClangdEngine(ctx, log, diagnostics, ClangdEngineConfig{ManagedRoot: lspRoot})
	case "css":
		return NewCSSEngine(ctx, log, diagnostics, WEBEngineConfig{ManagedRoot: lspRoot})
	case "html":
		return NewHTMLEngine(ctx, log, diagnostics, WEBEngineConfig{ManagedRoot: lspRoot})
	case "json":
		return NewJSONEngine(ctx, log, diagnostics, WEBEngineConfig{ManagedRoot: lspRoot})
	case "bash":
		return NewBashEngine(ctx, log, diagnostics, BashEngineConfig{ManagedRoot: lspRoot})
	default:
		return nil, fmt.Errorf("lsp: unsupported language %q", language)
	}
}

// --- Lifecycle handlers (stateful: serialized through the actor mailbox) ---

func (a *Actor) handleInitialize(ctx actor.Context, req gen.LspInitializeReq) (gen.LspJsonResp, error) {
	lang := normalizeLanguage(req.Language)
	if !a.isEnabled(lang) {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.initialize: service disabled for language %q", lang)
	}

	a.mu.Lock()
	engines := a.servers[req.RootUri]
	if engines == nil {
		engines = make(map[string]*rootServer)
		a.servers[req.RootUri] = engines
	}
	rs, ok := engines[lang]
	if !ok && a.engineCountLocked() >= maxServers {
		if victim := a.lruVictimLocked(req.RootUri, lang); victim != nil {
			delete(a.servers[victim.rootUri], victim.language)
			if len(a.servers[victim.rootUri]) == 0 {
				delete(a.servers, victim.rootUri)
			}
			go victim.rs.server.Shutdown(context.Background())
		} else {
			// Every engine is serving an in-flight request; evicting any of
			// them would race its own Shutdown. Refuse instead of exceeding
			// the cache cap.
			a.mu.Unlock()
			return gen.LspJsonResp{}, fmt.Errorf("lsp.initialize: engine cache full (%d) and all engines have in-flight requests; retry later", maxServers)
		}
	}
	a.mu.Unlock()

	if !ok {
		engineFactory := a.newEngine
		if engineFactory == nil {
			engineFactory = newEngineFor
		}
		engine, err := engineFactory(lang, a.managedRoot(), ctx.Lifecycle(), ctx.Logger(), a.diagnosticsSink())
		if err != nil {
			return gen.LspJsonResp{}, fmt.Errorf("lsp.initialize: create %s engine: %w", lang, err)
		}
		rs = &rootServer{server: engine}
		a.mu.Lock()
		engines = a.servers[req.RootUri]
		if engines == nil {
			engines = make(map[string]*rootServer)
			a.servers[req.RootUri] = engines
		}
		if existing, exists := engines[lang]; exists {
			// Another lifecycle handler created it (defensive; mailbox serialization makes this unlikely).
			a.mu.Unlock()
			_ = engine.Shutdown(context.Background())
			rs = existing
		} else {
			engines[lang] = rs
			a.mu.Unlock()
		}
	}

	rs.touch()
	if rs.initialized {
		return gen.LspJsonResp{JSON: string(rs.initResult)}, nil
	}

	result, err := rs.server.Initialize(ctx.Lifecycle(), req.RootUri)
	if err != nil {
		// Remove the failed engine so subsequent calls can retry.
		a.mu.Lock()
		if engines := a.servers[req.RootUri]; engines != nil {
			delete(engines, lang)
			if len(engines) == 0 {
				delete(a.servers, req.RootUri)
			}
		}
		a.mu.Unlock()
		_ = rs.server.Shutdown(context.Background())

		// TS self-heal: if the typescript companion package is missing
		// (common after upgrading from a pre-companion install), the
		// server reports "Could not find a valid TypeScript installation".
		// Trigger a background companion install so the next initialize
		// succeeds without the user manually re-clicking install.
		if lang == "typescript" || lang == "javascript" {
			if strings.Contains(err.Error(), "Could not find a valid TypeScript installation") {
				a.autoInstallTSCompanion(ctx.Logger())
			}
		}

		return gen.LspJsonResp{}, fmt.Errorf("lsp.initialize: %w", err)
	}
	// Publish the handshake result under the write lock so concurrent queries
	// in serverFor observe initialized/initResult consistently.
	a.mu.Lock()
	rs.initResult = result
	rs.initialized = true
	a.mu.Unlock()
	return gen.LspJsonResp{JSON: string(result)}, nil
}

// lruVictimLocked returns the least-recently-used engine entry across all roots
// and languages, excluding the (root, language) about to be initialized and
// entries with in-flight requests. Caller must hold a.mu write lock.
func (a *Actor) lruVictimLocked(excludeRootUri, excludeLanguage string) *lruEntry {
	var victim *lruEntry
	for rootUri, engines := range a.servers {
		for language, rs := range engines {
			if rootUri == excludeRootUri && language == excludeLanguage {
				continue
			}
			if rs.inFlight.Load() > 0 {
				continue
			}
			last := rs.lastUsed.Load()
			if victim == nil || last < victim.lastUsed {
				victim = &lruEntry{rootUri: rootUri, language: language, rs: rs, lastUsed: last}
			}
		}
	}
	return victim
}

type lruEntry struct {
	rootUri  string
	language string
	rs       *rootServer
	lastUsed int64
}

// handleShutdownByRoot shuts down every engine whose workspace root URI
// decodes to a filesystem path at or under req.RootPath. Internal callable
// used by the project actor to release LSP process handles before deleting a
// worktree directory. Unlike handleShutdown this prefix-matches a path, so
// the caller does not need to know which RootUri strings were used. An empty
// match is not an error — teardown is best-effort.
func (a *Actor) handleShutdownByRoot(ctx actor.Context, req gen.LspShutdownByRootReq) (gen.LspShutdownByRootResp, error) {
	if strings.TrimSpace(req.RootPath) == "" {
		return gen.LspShutdownByRootResp{}, fmt.Errorf("lsp.shutdown_by_root: rootPath required")
	}
	a.mu.Lock()
	var targets []*rootServer
	var hitRoots []string
	for rootUri, engines := range a.servers {
		p, ok := fileUriToPath(rootUri)
		if !ok || !util.PathWithin(p, req.RootPath) {
			continue
		}
		for _, rs := range engines {
			targets = append(targets, rs)
		}
		delete(a.servers, rootUri)
		hitRoots = append(hitRoots, rootUri)
	}
	a.mu.Unlock()
	for _, rs := range targets {
		if err := rs.server.Shutdown(ctx.Lifecycle()); err != nil {
			return gen.LspShutdownByRootResp{Shutdown: hitRoots}, fmt.Errorf("lsp.shutdown_by_root: %w", err)
		}
	}
	return gen.LspShutdownByRootResp{Shutdown: hitRoots}, nil
}

// fileUriToPath decodes a file:// workspace root URI to a host filesystem
// path. Percent-encoded forms (tsserver's file:///c%3A/...) decode the same as
// plain ones (gopls' file:///C:/...). Returns ok=false for non-file or
// undecodable URIs.
func fileUriToPath(uri string) (string, bool) {
	if !strings.HasPrefix(uri, "file://") {
		return "", false
	}
	u, err := url.ParseRequestURI(uri)
	if err != nil || u.Path == "" {
		return "", false
	}
	p, err := url.PathUnescape(u.Path)
	if err != nil {
		return "", false
	}
	p = filepath.FromSlash(p)
	// Windows drive paths arrive as "/C:/x"; strip the leading slash so the
	// result matches native paths. Gated on GOOS so a literal "/c:/x" on a
	// Unix host is not corrupted.
	if runtime.GOOS == "windows" && len(p) > 2 && p[0] == filepath.Separator && p[2] == ':' &&
		((p[1] >= 'A' && p[1] <= 'Z') || (p[1] >= 'a' && p[1] <= 'z')) {
		p = p[1:]
	}
	return p, true
}

func (a *Actor) handleShutdown(ctx actor.Context, req gen.LspShutdownReq) (gen.LspJsonResp, error) {
	// Empty language means "all languages for the specified root" (matches the
	// old single-engine-per-root behavior); an explicit language targets one slot.
	lang := req.Language
	a.mu.Lock()
	var targets []*rootServer
	if req.RootUri != "" {
		if engines, ok := a.servers[req.RootUri]; ok {
			if lang != "" {
				if rs, ok := engines[lang]; ok {
					targets = append(targets, rs)
					delete(engines, lang)
					if len(engines) == 0 {
						delete(a.servers, req.RootUri)
					}
				}
			} else {
				for _, rs := range engines {
					targets = append(targets, rs)
				}
				delete(a.servers, req.RootUri)
			}
		}
	} else {
		for rootUri, engines := range a.servers {
			for _, rs := range engines {
				targets = append(targets, rs)
			}
			delete(a.servers, rootUri)
		}
	}
	a.mu.Unlock()

	if len(targets) == 0 {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.shutdown: no server registered for root %q language %q", req.RootUri, lang)
	}
	for _, rs := range targets {
		if err := rs.server.Shutdown(ctx.Lifecycle()); err != nil {
			return gen.LspJsonResp{}, fmt.Errorf("lsp.shutdown: %w", err)
		}
	}
	return gen.LspJsonResp{}, nil
}

func (a *Actor) handleClearCache(ctx actor.Context) (gen.LspClearCacheReq, error) {
	a.mu.Lock()
	var targets []*rootServer
	for _, engines := range a.servers {
		for _, rs := range engines {
			targets = append(targets, rs)
		}
	}
	a.servers = make(map[string]map[string]*rootServer)
	a.mu.Unlock()

	for _, rs := range targets {
		_ = rs.server.Shutdown(ctx.Lifecycle())
	}
	ctx.Logger().Info("lspserver: cache cleared", "engines", len(targets))
	return gen.LspClearCacheReq{Evicted: int32(len(targets))}, nil
}

// handleStateGet returns the current enabled state per language.
func (a *Actor) handleStateGet(_ actor.PureContext) (gen.LspStateResp, error) {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return gen.LspStateResp{Languages: a.stateSnapshotLocked()}, nil
}

// handleStateSave sets the enabled state for a language and persists it.
// When disabling, all running engines for that language are shut down
// immediately so no processes linger.
func (a *Actor) handleStateSave(ctx actor.Context, req gen.LspStateSaveReq) (gen.LspStateResp, error) {
	lang := normalizeLanguage(req.Language)
	a.stateMu.Lock()
	a.enabled[lang] = req.Enabled
	a.stateMu.Unlock()

	var targets []*rootServer
	if !req.Enabled {
		a.mu.Lock()
		for rootUri, engines := range a.servers {
			if rs, ok := engines[lang]; ok {
				targets = append(targets, rs)
				delete(engines, lang)
				if len(engines) == 0 {
					delete(a.servers, rootUri)
				}
			}
		}
		a.mu.Unlock()
		for _, rs := range targets {
			_ = rs.server.Shutdown(ctx.Lifecycle())
		}
	}

	if err := a.Save(); err != nil {
		ctx.Logger().Error("lspserver: state_save persist failed", "error", err)
	}
	a.stateMu.RLock()
	resp := gen.LspStateResp{Languages: a.stateSnapshotLocked()}
	a.stateMu.RUnlock()
	ctx.Logger().Info("lspserver: state saved", "language", lang, "enabled", req.Enabled, "shutdown", len(targets))
	return resp, nil
}

// --- Document sync handlers ---

func (a *Actor) handleDidOpen(ctx actor.PureContext, req gen.LspDidOpenReq) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.did_open: %w", err)
	}
	defer done()
	if err := s.DidOpen(ctx.Lifecycle(), req.Uri, req.LanguageID, req.Text, req.Version); err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.did_open: %w", err)
	}
	return gen.LspJsonResp{}, nil
}

func (a *Actor) handleDidChange(ctx actor.PureContext, req gen.LspDidChangeReq) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.did_change: %w", err)
	}
	defer done()
	if err := s.DidChange(ctx.Lifecycle(), req.Uri, req.Version, json.RawMessage(req.Changes)); err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.did_change: %w", err)
	}
	return gen.LspJsonResp{}, nil
}

func (a *Actor) handleDidClose(ctx actor.PureContext, req gen.LspDidCloseReq) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.did_close: %w", err)
	}
	defer done()
	if err := s.DidClose(ctx.Lifecycle(), req.Uri); err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.did_close: %w", err)
	}
	return gen.LspJsonResp{}, nil
}

func (a *Actor) handleWarmUp(ctx actor.PureContext, req gen.LspUriReq) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.warm_up: %w", err)
	}
	defer done()
	if err := s.WarmUp(ctx.Lifecycle(), req.Uri); err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.warm_up: %w", err)
	}
	return gen.LspJsonResp{}, nil
}

// --- Query handlers ---

func (a *Actor) handleDefinition(ctx actor.PureContext, req gen.LspPositionParams) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.definition: %w", err)
	}
	defer done()
	result, err := s.Definition(ctx.Lifecycle(), req.Uri, req.Line, req.Character)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.definition: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}

func (a *Actor) handleHover(ctx actor.PureContext, req gen.LspPositionParams) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.hover: %w", err)
	}
	defer done()
	result, err := s.Hover(ctx.Lifecycle(), req.Uri, req.Line, req.Character)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.hover: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}

func (a *Actor) handleCompletion(ctx actor.PureContext, req gen.LspPositionParams) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.completion: %w", err)
	}
	defer done()
	result, err := s.Completion(ctx.Lifecycle(), req.Uri, req.Line, req.Character)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.completion: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}

func (a *Actor) handleReferences(ctx actor.PureContext, req gen.LspReferencesReq) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.references: %w", err)
	}
	defer done()
	result, err := s.References(ctx.Lifecycle(), req.Uri, req.Line, req.Character, req.IncludeDeclaration)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.references: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}

func (a *Actor) handleImplementation(ctx actor.PureContext, req gen.LspPositionParams) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.implementation: %w", err)
	}
	defer done()
	result, err := s.Implementation(ctx.Lifecycle(), req.Uri, req.Line, req.Character)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.implementation: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}

func (a *Actor) handleTypeDefinition(ctx actor.PureContext, req gen.LspPositionParams) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.type_definition: %w", err)
	}
	defer done()
	result, err := s.TypeDefinition(ctx.Lifecycle(), req.Uri, req.Line, req.Character)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.type_definition: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}

func (a *Actor) handleDocumentSymbol(ctx actor.PureContext, req gen.LspUriReq) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.document_symbol: %w", err)
	}
	defer done()
	result, err := s.DocumentSymbol(ctx.Lifecycle(), req.Uri)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.document_symbol: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}

func (a *Actor) handleSignatureHelp(ctx actor.PureContext, req gen.LspPositionParams) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.signature_help: %w", err)
	}
	defer done()
	result, err := s.SignatureHelp(ctx.Lifecycle(), req.Uri, req.Line, req.Character)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.signature_help: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}

func (a *Actor) handleFormatting(ctx actor.PureContext, req gen.LspUriReq) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.formatting: %w", err)
	}
	defer done()
	result, err := s.Formatting(ctx.Lifecycle(), req.Uri)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.formatting: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}

func (a *Actor) handleRename(ctx actor.PureContext, req gen.LspRenameReq) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.rename: %w", err)
	}
	defer done()
	result, err := s.Rename(ctx.Lifecycle(), req.Uri, req.Line, req.Character, req.NewName)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.rename: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}

func (a *Actor) handlePrepareRename(ctx actor.PureContext, req gen.LspPositionParams) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.prepare_rename: %w", err)
	}
	defer done()
	result, err := s.PrepareRename(ctx.Lifecycle(), req.Uri, req.Line, req.Character)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.prepare_rename: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}

func (a *Actor) handleCodeAction(ctx actor.PureContext, req gen.LspRangeParams) (gen.LspJsonResp, error) {
	s, done, err := a.serverFor(req.RootUri, req.Language)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.code_action: %w", err)
	}
	defer done()
	result, err := s.CodeAction(ctx.Lifecycle(), req.Uri, req.StartLine, req.StartChar, req.EndLine, req.EndChar)
	if err != nil {
		return gen.LspJsonResp{}, fmt.Errorf("lsp.code_action: %w", err)
	}
	return gen.LspJsonResp{JSON: string(result)}, nil
}
