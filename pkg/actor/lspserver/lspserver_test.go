package lspserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	testutil "github.com/qomos-w/sporemind/pkg/testutil"
)

// Compile-time assertions.
var (
	_ actor.Actor = (*Actor)(nil)
	// GoEngine must satisfy the LanguageEngine interface.
	_ LanguageEngine = (*GoEngine)(nil)
)

// wantCallables lists every callable registered in OnStart, in the same
// order as the registrations slice in registerCallables.
var wantCallables = []string{
	"lsp.initialize",
	"lsp.did_open",
	"lsp.did_change",
	"lsp.did_close",
	"lsp.definition",
	"lsp.hover",
	"lsp.completion",
	"lsp.references",
	"lsp.implementation",
	"lsp.type_definition",
	"lsp.document_symbol",
	"lsp.signature_help",
	"lsp.formatting",
	"lsp.rename",
	"lsp.prepare_rename",
	"lsp.code_action",
	"lsp.warm_up",
	"lsp.shutdown",
	"lsp.shutdown_by_root",
	"lsp.clear_cache",
	"lsp.state_get",
	"lsp.state_save",
	"lsp.status",
	"lsp.install",
}

// TestOnStart follows the existing test pattern: construct the actor, call
// OnStart, then verify the registration surface and shut down cleanly.
func TestOnStart(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	ctx.RegisteredDomains = []string{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatalf("OnStart: %v", err)
	}

	// All 18 callables must be registered.
	if got, want := len(ctx.Regs), len(wantCallables); got != want {
		t.Errorf("registered %d callables, want %d", got, want)
	}
	for _, id := range wantCallables {
		handler, ok := ctx.Regs[id]
		if !ok {
			t.Errorf("callable %q not registered", id)
			continue
		}
		if handler == nil {
			t.Errorf("callable %q has nil handler", id)
		}
	}

	// The "lsp" domain must be registered and exposed.
	found := false
	for _, d := range ctx.RegisteredDomains {
		if d == "lsp" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("domain %q not registered", "lsp")
	}

	// The "lsp.diagnostics" event kind must be registered.
	if a.emit == nil {
		t.Errorf("emit function not captured in OnStart")
	}

	_ = a.OnStop(ctx)
}

// --- fake server for handler tests ---

// fakeServer implements LanguageEngine by embedding a nil LanguageEngine so
// the full interface is satisfied without stubbing every method. Only
// Definition, Hover, References, WarmUp, and Shutdown are overridden; the
// promoted methods are never called by the tests below, so the nil embedding
// is safe.
type fakeServer struct {
	LanguageEngine

	initResult json.RawMessage
	initErr    error
	initCalls  int
	defResult  json.RawMessage
	defErr     error
	defCalls   []fakePosCall
	hoverResult json.RawMessage
	hoverErr    error
	hoverCalls  []fakePosCall
	refResult  json.RawMessage
	refErr     error
	refCalls   []fakeRefCall
	warmUpCalls []string
	warmUpErr   error
	language    string
}

type fakePosCall struct {
	uri       string
	line      uint32
	character uint32
}

type fakeRefCall struct {
	uri         string
	line        uint32
	character   uint32
	includeDecl bool
}

func (f *fakeServer) Initialize(_ context.Context, rootURI string) (json.RawMessage, error) {
	f.initCalls++
	return f.initResult, f.initErr
}

func (f *fakeServer) Definition(_ context.Context, uri string, line, char uint32) (json.RawMessage, error) {
	f.defCalls = append(f.defCalls, fakePosCall{uri: uri, line: line, character: char})
	return f.defResult, f.defErr
}

func (f *fakeServer) Hover(_ context.Context, uri string, line, char uint32) (json.RawMessage, error) {
	f.hoverCalls = append(f.hoverCalls, fakePosCall{uri: uri, line: line, character: char})
	return f.hoverResult, f.hoverErr
}

func (f *fakeServer) References(_ context.Context, uri string, line, char uint32, includeDecl bool) (json.RawMessage, error) {
	f.refCalls = append(f.refCalls, fakeRefCall{uri: uri, line: line, character: char, includeDecl: includeDecl})
	return f.refResult, f.refErr
}

func (f *fakeServer) WarmUp(_ context.Context, uri string) error {
	f.warmUpCalls = append(f.warmUpCalls, uri)
	return f.warmUpErr
}

func (f *fakeServer) Shutdown(_ context.Context) error {
	return nil
}

// withFakeServer returns an Actor whose sole registered fake server is keyed
// under the empty root and language "go" (single-workspace fallback).
func withFakeServer(f *fakeServer) *Actor {
	return withFakeServerFor(f, "", "")
}

// withFakeServerFor returns an Actor with an initialized fake server registered
// for the given rootURI and language (empty language defaults to "go").
func withFakeServerFor(f *fakeServer, rootURI, language string) *Actor {
	a := withPendingServerFor(f, rootURI, language)
	for _, engines := range a.servers {
		for _, rs := range engines {
			rs.initialized = true
		}
	}
	return a
}

// withPendingServerFor returns an Actor with a fake server registered for the
// given rootURI and language whose initialize handshake has not run yet;
// handleInitialize is expected to drive Initialize and cache the result.
func withPendingServerFor(f *fakeServer, rootURI, language string) *Actor {
	lang := language
	if lang == "" {
		lang = "go"
	}
	return &Actor{
		servers: map[string]map[string]*rootServer{
			rootURI: {lang: {server: f}},
		},
		enabled: defaultEnabled(),
	}
}

// TestHandleDefinition verifies request parsing (uri/line/character are
// forwarded to the server) and response wrapping (raw JSON is placed into
// LspJsonResp.JSON) for the definition handler.
func TestHandleDefinition(t *testing.T) {
	canned := json.RawMessage(`[{"uri":"file:///x.go","range":{"start":{"line":3,"character":6},"end":{"line":3,"character":10}}}]`)
	srv := &fakeServer{defResult: canned}
	a := withFakeServer(srv)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	req := gen.LspPositionParams{Uri: "file:///x.go", Line: 5, Character: 10}
	resp, err := a.handleDefinition(ctx, req)
	if err != nil {
		t.Fatalf("handleDefinition: unexpected error: %v", err)
	}

	// Request parsing: the server must receive the exact uri/line/character.
	if len(srv.defCalls) != 1 {
		t.Fatalf("expected 1 Definition call, got %d", len(srv.defCalls))
	}
	got := srv.defCalls[0]
	if got.uri != req.Uri || got.line != req.Line || got.character != req.Character {
		t.Errorf("server received uri=%q line=%d char=%d, want uri=%q line=%d char=%d",
			got.uri, got.line, got.character, req.Uri, req.Line, req.Character)
	}

	// Response wrapping: the raw JSON must be wrapped into LspJsonResp.JSON.
	if resp.JSON != string(canned) {
		t.Errorf("response JSON = %q, want %q", resp.JSON, string(canned))
	}
}

// TestHandleDefinition_Error verifies that server errors are wrapped with
// the "lsp.definition:" prefix and the response JSON is empty.
func TestHandleDefinition_Error(t *testing.T) {
	srv := &fakeServer{defErr: errors.New("no views")}
	a := withFakeServer(srv)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleDefinition(ctx, gen.LspPositionParams{Uri: "file:///x.go"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "lsp.definition:") {
		t.Errorf("error should be prefixed with 'lsp.definition:', got: %v", err)
	}
	if resp.JSON != "" {
		t.Errorf("error response should have empty JSON, got %q", resp.JSON)
	}
}

// TestHandleHover verifies request parsing and response wrapping for the
// hover handler.
func TestHandleHover(t *testing.T) {
	canned := json.RawMessage(`{"contents":{"kind":"markdown","value":"func main()"}}`)
	srv := &fakeServer{hoverResult: canned}
	a := withFakeServer(srv)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	req := gen.LspPositionParams{Uri: "file:///x.go", Line: 12, Character: 3}
	resp, err := a.handleHover(ctx, req)
	if err != nil {
		t.Fatalf("handleHover: unexpected error: %v", err)
	}

	// Request parsing.
	if len(srv.hoverCalls) != 1 {
		t.Fatalf("expected 1 Hover call, got %d", len(srv.hoverCalls))
	}
	got := srv.hoverCalls[0]
	if got.uri != req.Uri || got.line != req.Line || got.character != req.Character {
		t.Errorf("server received uri=%q line=%d char=%d, want uri=%q line=%d char=%d",
			got.uri, got.line, got.character, req.Uri, req.Line, req.Character)
	}

	// Response wrapping.
	if resp.JSON != string(canned) {
		t.Errorf("response JSON = %q, want %q", resp.JSON, string(canned))
	}
}

// TestHandleHover_Error verifies that server errors are wrapped with the
// "lsp.hover:" prefix.
func TestHandleHover_Error(t *testing.T) {
	srv := &fakeServer{hoverErr: errors.New("no views")}
	a := withFakeServer(srv)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleHover(ctx, gen.LspPositionParams{Uri: "file:///x.go"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "lsp.hover:") {
		t.Errorf("error should be prefixed with 'lsp.hover:', got: %v", err)
	}
	if resp.JSON != "" {
		t.Errorf("error response should have empty JSON, got %q", resp.JSON)
	}
}

// TestHandleReferences verifies that the references handler forwards the
// request (uri/line/character/includeDeclaration) to the server and wraps
// the returned raw JSON into LspJsonResp.JSON, preserving the standard LSP
// Location[] payload untouched.
func TestHandleReferences(t *testing.T) {
	canned := json.RawMessage(`[{"uri":"file:///x.go","range":{"start":{"line":3,"character":6},"end":{"line":3,"character":10}}},{"uri":"file:///x.go","range":{"start":{"line":9,"character":2},"end":{"line":9,"character":6}}}]`)
	srv := &fakeServer{refResult: canned}
	a := withFakeServer(srv)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	req := gen.LspReferencesReq{Uri: "file:///x.go", Line: 3, Character: 6, IncludeDeclaration: true}
	resp, err := a.handleReferences(ctx, req)
	if err != nil {
		t.Fatalf("handleReferences: unexpected error: %v", err)
	}

	// Request parsing: uri/line/character/includeDeclaration must be forwarded.
	if len(srv.refCalls) != 1 {
		t.Fatalf("expected 1 References call, got %d", len(srv.refCalls))
	}
	got := srv.refCalls[0]
	if got.uri != req.Uri || got.line != req.Line || got.character != req.Character || got.includeDecl != req.IncludeDeclaration {
		t.Errorf("server received uri=%q line=%d char=%d includeDecl=%v, want uri=%q line=%d char=%d includeDecl=%v",
			got.uri, got.line, got.character, got.includeDecl, req.Uri, req.Line, req.Character, req.IncludeDeclaration)
	}

	// Response wrapping: raw JSON must pass through untouched.
	if resp.JSON != string(canned) {
		t.Errorf("response JSON = %q, want %q", resp.JSON, string(canned))
	}
}

// TestHandleReferences_NoDeclaration verifies that includeDeclaration=false
// is forwarded verbatim (declaration excluded from the result set).
func TestHandleReferences_NoDeclaration(t *testing.T) {
	canned := json.RawMessage(`[{"uri":"file:///x.go","range":{"start":{"line":9,"character":2},"end":{"line":9,"character":6}}}]`)
	srv := &fakeServer{refResult: canned}
	a := withFakeServer(srv)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	req := gen.LspReferencesReq{Uri: "file:///x.go", Line: 3, Character: 6, IncludeDeclaration: false}
	resp, err := a.handleReferences(ctx, req)
	if err != nil {
		t.Fatalf("handleReferences: unexpected error: %v", err)
	}

	if len(srv.refCalls) != 1 {
		t.Fatalf("expected 1 References call, got %d", len(srv.refCalls))
	}
	if srv.refCalls[0].includeDecl {
		t.Errorf("includeDeclaration = true, want false")
	}
	if resp.JSON != string(canned) {
		t.Errorf("response JSON = %q, want %q", resp.JSON, string(canned))
	}
}

// TestHandleReferences_Error verifies that server errors are wrapped with
// the "lsp.references:" prefix and the response JSON is empty.
func TestHandleReferences_Error(t *testing.T) {
	srv := &fakeServer{refErr: errors.New("no views")}
	a := withFakeServer(srv)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleReferences(ctx, gen.LspReferencesReq{Uri: "file:///x.go"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "lsp.references:") {
		t.Errorf("error should be prefixed with 'lsp.references:', got: %v", err)
	}
	if resp.JSON != "" {
		t.Errorf("error response should have empty JSON, got %q", resp.JSON)
	}
}

// TestHandleWarmUp verifies that the warm_up handler forwards the URI to
// the server.
func TestHandleWarmUp(t *testing.T) {
	srv := &fakeServer{}
	a := withFakeServer(srv)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	req := gen.LspUriReq{Uri: "file:///x.go"}
	_, err := a.handleWarmUp(ctx, req)
	if err != nil {
		t.Fatalf("handleWarmUp: unexpected error: %v", err)
	}
	if len(srv.warmUpCalls) != 1 || srv.warmUpCalls[0] != req.Uri {
		t.Errorf("warmUp received %v, want [%q]", srv.warmUpCalls, req.Uri)
	}
}

// TestServerFor_MultiRoot verifies that serverFor routes to the correct
// per-root, per-language engine when multiple roots are registered, and errors
// when an empty rootUri is ambiguous.
func TestServerFor_MultiRoot(t *testing.T) {
	srvA := &fakeServer{}
	srvB := &fakeServer{}
	a := &Actor{servers: map[string]map[string]*rootServer{
		"file:///a": {"go": {server: srvA, initialized: true}},
		"file:///b": {"typescript": {server: srvB, initialized: true}},
	}}

	s, done, err := a.serverFor("file:///a", "go")
	if err != nil || s != srvA {
		t.Errorf("serverFor(a, go): got %v err=%v, want srvA", s, err)
	} else {
		done()
	}
	s, done, err = a.serverFor("file:///b", "typescript")
	if err != nil || s != srvB {
		t.Errorf("serverFor(b, typescript): got %v err=%v, want srvB", s, err)
	} else {
		done()
	}
	if _, _, err := a.serverFor("file:///c", "go"); err == nil {
		t.Errorf("serverFor(c, go): expected error for unknown root, got nil")
	}
	if _, _, err := a.serverFor("file:///a", "typescript"); err == nil {
		t.Errorf("serverFor(a, typescript): expected error for missing language, got nil")
	}
	if _, _, err := a.serverFor("", "go"); err == nil {
		t.Errorf("serverFor(\"\", go): expected error when multiple roots, got nil")
	}
}

// TestDiagnosticsSink verifies that diagnostic notifications are forwarded
// as "lsp.diagnostics" events with the correct payload shape.
func TestDiagnosticsSink(t *testing.T) {
	var emitted []testutil.EmittedEvent
	a := &Actor{
		emit: func(kind string, payload any) error {
			emitted = append(emitted, testutil.EmittedEvent{Kind: kind, Payload: payload})
			return nil
		},
	}
	sink := a.diagnosticsSink()
	sink("file:///x.go", 42, []byte(`[{"message":"undefined: foo"}]`))

	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(emitted))
	}
	if emitted[0].Kind != "lsp.diagnostics" {
		t.Errorf("event kind = %q, want %q", emitted[0].Kind, "lsp.diagnostics")
	}
	ev, ok := emitted[0].Payload.(gen.LspDiagnosticsEvent)
	if !ok {
		t.Fatalf("payload type = %T, want gen.LspDiagnosticsEvent", emitted[0].Payload)
	}
	if ev.Uri != "file:///x.go" || ev.Version != 42 || ev.Diagnostics != `[{"message":"undefined: foo"}]` {
		t.Errorf("event payload = %+v, want {Uri:file:///x.go Version:42 Diagnostics:[...]}", ev)
	}
}

// TestLruVictim verifies that lruVictimLocked selects the entry with the
// smallest lastUsed across all roots and languages, and skips the excluded
// (root, language) pair.
func TestLruVictim(t *testing.T) {
	srvA := &fakeServer{}
	srvB := &fakeServer{}
	srvC := &fakeServer{}
	a := &Actor{servers: map[string]map[string]*rootServer{
		"file:///a": {
			"go":         {server: srvA},
			"typescript": {server: srvB},
		},
		"file:///b": {"go": {server: srvC}},
	}}
	// a/typescript is the oldest, b/go is the most recent.
	a.servers["file:///a"]["go"].lastUsed.Store(2000)
	a.servers["file:///a"]["typescript"].lastUsed.Store(1000)
	a.servers["file:///b"]["go"].lastUsed.Store(3000)

	victim := a.lruVictimLocked("file:///b", "go")
	if victim == nil || victim.rootUri != "file:///a" || victim.language != "typescript" {
		t.Errorf("victim = %+v, want file:///a / typescript", victim)
	}

	// When the excluded entry IS the oldest, the next-oldest wins.
	victim = a.lruVictimLocked("file:///a", "typescript")
	if victim == nil || victim.rootUri != "file:///a" || victim.language != "go" {
		t.Errorf("victim excluding typescript = %+v, want file:///a / go", victim)
	}
}

// TestHandleClearCache verifies that clear_cache shuts down every engine
// across all roots and languages, and empties the map.
func TestHandleClearCache(t *testing.T) {
	srvA := &fakeServer{}
	srvB := &fakeServer{}
	srvC := &fakeServer{}
	a := &Actor{servers: map[string]map[string]*rootServer{
		"file:///a": {
			"go":         {server: srvA, initialized: true},
			"typescript": {server: srvB, initialized: true},
		},
		"file:///b": {"go": {server: srvC, initialized: true}},
	}}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleClearCache(ctx)
	if err != nil {
		t.Fatalf("handleClearCache: %v", err)
	}
	if resp.Evicted != 3 {
		t.Errorf("evicted = %d, want 3", resp.Evicted)
	}
	if len(a.servers) != 0 {
		t.Errorf("servers map not empty: %v", a.servers)
	}
	// After clear, a query with the old root/language must fail.
	if _, _, err := a.serverFor("file:///a", "go"); err == nil {
		t.Errorf("serverFor after clear: expected error, got nil")
	}
}

// TestHandleStateGet_Default verifies that every known language defaults to
// enabled on a fresh actor (before OnStart loads persisted state).
func TestHandleStateGet_Default(t *testing.T) {
	a := &Actor{enabled: defaultEnabled()} // mirrors OnStart
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleStateGet(ctx)
	if err != nil {
		t.Fatalf("handleStateGet: %v", err)
	}
	if len(resp.Languages) != len(knownLanguages) {
		t.Fatalf("Languages = %v, want %d entries", resp.Languages, len(knownLanguages))
	}
	for i, want := range knownLanguages {
		got := resp.Languages[i]
		if got.Language != want {
			t.Errorf("Languages[%d].Language = %q, want %q", i, got.Language, want)
		}
		if !got.Enabled {
			t.Errorf("Languages[%d].Enabled = false, want true (default)", i)
		}
	}
}

// TestHandleStateSave_Disable verifies that disabling one language persists its
// flag, shuts down that language's engines across all roots, and leaves other
// languages and their engines untouched.
func TestHandleStateSave_Disable(t *testing.T) {
	srvGoA := &fakeServer{}
	srvGoB := &fakeServer{}
	srvTS := &fakeServer{}
	a := &Actor{
		servers: map[string]map[string]*rootServer{
			"file:///a": {
				"go":         {server: srvGoA, initialized: true},
				"typescript": {server: srvTS, initialized: true},
			},
			"file:///b": {"go": {server: srvGoB, initialized: true}},
		},
		enabled: defaultEnabled(),
		store:   persist.NewFSPersist(t.TempDir()),
		actorID: "test-state-disable",
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleStateSave(ctx, gen.LspStateSaveReq{Language: "go", Enabled: false})
	if err != nil {
		t.Fatalf("handleStateSave: %v", err)
	}
	if len(resp.Languages) != len(knownLanguages) {
		t.Fatalf("Languages = %v, want %d entries", resp.Languages, len(knownLanguages))
	}
	for _, ls := range resp.Languages {
		if ls.Language == "go" && ls.Enabled {
			t.Errorf("go language should be disabled, got %+v", ls)
		}
		if ls.Language != "go" && !ls.Enabled {
			t.Errorf("%s language should remain enabled, got %+v", ls.Language, ls)
		}
	}
	if a.enabled["go"] {
		t.Errorf("a.enabled[go] = true, want false")
	}
	if !a.enabled["typescript"] || !a.enabled["javascript"] {
		t.Errorf("other languages disabled unexpectedly: %v", a.enabled)
	}
	// All go engines shut down and are removed; the typescript engine survives.
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.servers["file:///a"]) != 1 || a.servers["file:///a"]["typescript"] == nil {
		t.Errorf("root a engines = %+v, want only typescript", a.servers["file:///a"])
	}
	if _, ok := a.servers["file:///b"]; ok {
		t.Errorf("root b should be removed after its only (go) engine was shut down")
	}

	// Round-trip: a fresh actor loading from the same store must read go=false.
	loaded := &Actor{
		store:   a.store,
		actorID: a.actorID,
	}
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.enabled["go"] {
		t.Errorf("loaded enabled[go] = true, want false (persisted)")
	}
}

// TestHandleInitialize_Disabled verifies that handleInitialize refuses to
// spawn an engine for a language whose service is disabled, and that other
// languages remain usable.
func TestHandleInitialize_Disabled(t *testing.T) {
	a := &Actor{
		servers: make(map[string]map[string]*rootServer),
		enabled: map[string]bool{"go": true, "typescript": false, "javascript": true},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	_, err := a.handleInitialize(ctx, gen.LspInitializeReq{RootUri: "file:///repo", Language: "typescript"})
	if err == nil {
		t.Fatal("expected error when LSP disabled for language, got nil")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention 'disabled', got: %v", err)
	}
	if len(a.servers) != 0 {
		t.Errorf("no server should be spawned when disabled, got %d", len(a.servers))
	}

	// An enabled language initializes normally.
	canned := json.RawMessage(`{"capabilities":{}}`)
	a.servers["file:///repo"] = map[string]*rootServer{"go": {server: &fakeServer{initResult: canned}}}
	resp, err := a.handleInitialize(ctx, gen.LspInitializeReq{RootUri: "file:///repo", Language: "go"})
	if err != nil {
		t.Fatalf("handleInitialize (go): unexpected error: %v", err)
	}
	if resp.JSON != string(canned) {
		t.Errorf("response JSON = %q, want %q", resp.JSON, string(canned))
	}
}

// TestHandleInitialize_CachesResult verifies that handleInitialize drives the
// engine Initialize, wraps the raw result into LspJsonResp.JSON, and serves
// subsequent calls from the cached result without re-initializing the engine.
func TestHandleInitialize_CachesResult(t *testing.T) {
	canned := json.RawMessage(`{"capabilities":{"textDocument":{"definitionProvider":true}}}`)
	srv := &fakeServer{initResult: canned}
	a := withPendingServerFor(srv, "file:///repo", "go")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleInitialize(ctx, gen.LspInitializeReq{RootUri: "file:///repo", Language: "go"})
	if err != nil {
		t.Fatalf("handleInitialize: unexpected error: %v", err)
	}
	if resp.JSON != string(canned) {
		t.Errorf("response JSON = %q, want %q", resp.JSON, string(canned))
	}
	if srv.initCalls != 1 {
		t.Fatalf("engine Initialize called %d times, want 1", srv.initCalls)
	}

	// A second initialize must be served from the cached result.
	resp2, err := a.handleInitialize(ctx, gen.LspInitializeReq{RootUri: "file:///repo", Language: "go"})
	if err != nil {
		t.Fatalf("handleInitialize (second): unexpected error: %v", err)
	}
	if resp2.JSON != string(canned) {
		t.Errorf("cached response JSON = %q, want %q", resp2.JSON, string(canned))
	}
	if srv.initCalls != 1 {
		t.Errorf("engine Initialize called %d times after cache, want 1", srv.initCalls)
	}
}

// TestHandleInitialize_Error verifies that engine initialization errors are
// wrapped with the "lsp.initialize:" prefix, the response JSON is empty, and
// the failed engine is removed so retries are possible.
func TestHandleInitialize_Error(t *testing.T) {
	srv := &fakeServer{initErr: errors.New("no workspace")}
	a := withPendingServerFor(srv, "file:///repo", "go")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleInitialize(ctx, gen.LspInitializeReq{RootUri: "file:///repo", Language: "go"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "lsp.initialize:") {
		t.Errorf("error should be prefixed with 'lsp.initialize:', got: %v", err)
	}
	if resp.JSON != "" {
		t.Errorf("error response should have empty JSON, got %q", resp.JSON)
	}
	if len(a.servers["file:///repo"]) != 0 {
		t.Errorf("failed engine should be removed, got %+v", a.servers["file:///repo"])
	}
}

// TestLoad_LegacySingleBool verifies that the legacy persisted shape
// {"enabled": <bool>} migrates to the per-language map keyed under "go".
func TestLoad_LegacySingleBool(t *testing.T) {
	store := persist.NewFSPersist(t.TempDir())
	if err := store.Save("legacy-actor", map[string]any{"enabled": false}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}
	a := &Actor{store: store, actorID: "legacy-actor", enabled: defaultEnabled()}
	if err := a.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if a.enabled["go"] {
		t.Errorf("legacy disabled state lost: enabled[go]=true, want false")
	}
	if !a.enabled["typescript"] || !a.enabled["javascript"] {
		t.Errorf("other languages should stay at default enabled, got %v", a.enabled)
	}
}

// TestHandleDefinition_RoutesByLanguage verifies that two requests to the same
// root but with different Language values are routed to their respective fake
// engines.
func TestHandleDefinition_RoutesByLanguage(t *testing.T) {
	srvGo := &fakeServer{defResult: json.RawMessage(`[]`)}
	srvTS := &fakeServer{defResult: json.RawMessage(`[]`)}
	a := &Actor{
		servers: map[string]map[string]*rootServer{
			"file:///repo": {
				"go":         {server: srvGo, initialized: true},
				"typescript": {server: srvTS, initialized: true},
			},
		},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	_, err := a.handleDefinition(ctx, gen.LspPositionParams{Uri: "file:///repo/main.go", Line: 0, Character: 0, RootUri: "file:///repo", Language: "go"})
	if err != nil {
		t.Fatalf("handleDefinition(go): %v", err)
	}
	_, err = a.handleDefinition(ctx, gen.LspPositionParams{Uri: "file:///repo/main.ts", Line: 0, Character: 0, RootUri: "file:///repo", Language: "typescript"})
	if err != nil {
		t.Fatalf("handleDefinition(typescript): %v", err)
	}

	if len(srvGo.defCalls) != 1 {
		t.Errorf("go engine calls = %d, want 1", len(srvGo.defCalls))
	}
	if len(srvTS.defCalls) != 1 {
		t.Errorf("typescript engine calls = %d, want 1", len(srvTS.defCalls))
	}
}

// TestServerFor_EmptyLanguageDefaultsToGo verifies that requests without a
// Language value route to the go engine in the single-workspace fallback.
func TestServerFor_EmptyLanguageDefaultsToGo(t *testing.T) {
	srvGo := &fakeServer{}
	srvTS := &fakeServer{}
	a := &Actor{servers: map[string]map[string]*rootServer{
		"": {
			"go":         {server: srvGo, initialized: true},
			"typescript": {server: srvTS, initialized: true},
		},
	}}

	s, done, err := a.serverFor("", "")
	if err != nil || s != srvGo {
		t.Errorf("serverFor(empty): got %v err=%v, want go engine", s, err)
	} else {
		done()
	}
}

// TestHandleInitialize_LruEvictionAcrossLanguages verifies that the LRU cap is
// enforced across every (root, language) engine, and that initialize evicts the
// least-recently-used engine.
func TestHandleInitialize_LruEvictionAcrossLanguages(t *testing.T) {
	a := &Actor{
		servers: make(map[string]map[string]*rootServer),
		enabled: defaultEnabled(),
		newEngine: func(language string, _ string, _ context.Context, _ actor.Logger, _ func(uri string, version int32, diagnosticsJSON []byte)) (LanguageEngine, error) {
			return &fakeServer{language: language}, nil
		},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	// Pre-fill maxServers engines across two roots and languages with known ages.
	engineRoots := []string{"file:///r1", "file:///r1", "file:///r2", "file:///r2"}
	engineLangs := []string{"go", "typescript", "go", "typescript"}
	for i := range engineRoots {
		rs := &rootServer{server: &fakeServer{language: engineLangs[i]}, initialized: true}
		rs.lastUsed.Store(int64(i * 1000))
		if a.servers[engineRoots[i]] == nil {
			a.servers[engineRoots[i]] = make(map[string]*rootServer)
		}
		a.servers[engineRoots[i]][engineLangs[i]] = rs
	}

	// Initialize a fifth engine for a new (root, language); the oldest engine
	// (r1/go) must be evicted.
	_, err := a.handleInitialize(ctx, gen.LspInitializeReq{RootUri: "file:///r3", Language: "go"})
	if err != nil {
		t.Fatalf("handleInitialize: %v", err)
	}

	if a.servers["file:///r1"]["go"] != nil {
		t.Errorf("oldest engine file:///r1/go was not evicted")
	}
	if a.servers["file:///r1"]["typescript"] == nil {
		t.Errorf("file:///r1/typescript should still be present")
	}
	if a.servers["file:///r3"]["go"] == nil {
		t.Errorf("file:///r3/go should be present")
	}
}

// TestServerFor_SkipsUninitialized verifies that an engine registered in the
// map but whose initialize handshake has not completed yet is not handed out
// to concurrent queries.
func TestServerFor_SkipsUninitialized(t *testing.T) {
	srv := &fakeServer{}
	a := &Actor{servers: map[string]map[string]*rootServer{
		"file:///a": {"go": {server: srv}}, // initialized == false
	}}

	if _, _, err := a.serverFor("file:///a", "go"); err == nil {
		t.Fatalf("serverFor on uninitialized engine: expected error, got nil")
	}

	// Complete the handshake; now it must be served.
	a.servers["file:///a"]["go"].initialized = true
	s, done, err := a.serverFor("file:///a", "go")
	if err != nil || s != srv {
		t.Fatalf("serverFor after init: got %v err=%v, want srv", s, err)
	}
	done()
}

// TestLruVictim_SkipsInFlight verifies that lruVictimLocked never selects an
// engine with an in-flight request, even when it is the oldest entry.
func TestLruVictim_SkipsInFlight(t *testing.T) {
	a := &Actor{servers: map[string]map[string]*rootServer{
		"file:///a": {
			"go":         {server: &fakeServer{}},
			"typescript": {server: &fakeServer{}},
		},
		"file:///b": {"go": {server: &fakeServer{}}},
	}}
	a.servers["file:///a"]["go"].lastUsed.Store(2000)
	a.servers["file:///a"]["typescript"].lastUsed.Store(1000) // oldest
	a.servers["file:///b"]["go"].lastUsed.Store(3000)
	a.servers["file:///a"]["typescript"].inFlight.Store(1) // in-flight ⇒ must be skipped

	victim := a.lruVictimLocked("file:///b", "go")
	if victim == nil {
		t.Fatalf("expected a non-in-flight victim, got nil")
	}
	if victim.rootUri != "file:///a" || victim.language != "go" {
		t.Errorf("victim = %+v, want file:///a / go (next oldest, skipping in-flight typescript)", victim)
	}
}

// TestHandleInitialize_CapacityAllInFlight verifies that when the engine cache
// is full and every engine has an in-flight request, initialize refuses to
// exceed the cap instead of evicting a busy engine.
func TestHandleInitialize_CapacityAllInFlight(t *testing.T) {
	a := &Actor{
		servers: make(map[string]map[string]*rootServer),
		enabled: defaultEnabled(),
		newEngine: func(language string, _ string, _ context.Context, _ actor.Logger, _ func(uri string, version int32, diagnosticsJSON []byte)) (LanguageEngine, error) {
			return &fakeServer{language: language}, nil
		},
	}
	roots := []string{"file:///r1", "file:///r1", "file:///r2", "file:///r2"}
	langs := []string{"go", "typescript", "go", "typescript"}
	for i := range roots {
		rs := &rootServer{server: &fakeServer{}, initialized: true}
		rs.lastUsed.Store(int64(i * 1000))
		rs.inFlight.Store(1)
		if a.servers[roots[i]] == nil {
			a.servers[roots[i]] = make(map[string]*rootServer)
		}
		a.servers[roots[i]][langs[i]] = rs
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	_, err := a.handleInitialize(ctx, gen.LspInitializeReq{RootUri: "file:///r3", Language: "go"})
	if err == nil {
		t.Fatalf("handleInitialize with all engines in-flight: expected error, got nil")
	}
	if got := a.engineCountLocked(); got != maxServers {
		t.Errorf("engine count = %d, want %d (must not exceed cap when all busy)", got, maxServers)
	}
}

// TestHandleShutdown_Language verifies that shutdown can target a single
// language engine, a whole root (when language is empty), or all servers.
func TestHandleShutdown_Language(t *testing.T) {
	srvGoA := &fakeServer{}
	srvTSA := &fakeServer{}
	srvGoB := &fakeServer{}
	a := &Actor{
		servers: map[string]map[string]*rootServer{
			"file:///a": {"go": {server: srvGoA}, "typescript": {server: srvTSA}},
			"file:///b": {"go": {server: srvGoB}},
		},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	// Shut down only the typescript engine for root a.
	_, err := a.handleShutdown(ctx, gen.LspShutdownReq{RootUri: "file:///a", Language: "typescript"})
	if err != nil {
		t.Fatalf("handleShutdown(a, typescript): %v", err)
	}
	if _, ok := a.servers["file:///a"]["typescript"]; ok {
		t.Errorf("typescript engine for root a should be removed")
	}
	if _, ok := a.servers["file:///a"]["go"]; !ok {
		t.Errorf("go engine for root a should remain")
	}

	// Shut down all engines for root a (language empty).
	_, err = a.handleShutdown(ctx, gen.LspShutdownReq{RootUri: "file:///a", Language: ""})
	if err != nil {
		t.Fatalf("handleShutdown(a, empty): %v", err)
	}
	if _, ok := a.servers["file:///a"]; ok {
		t.Errorf("root a should be removed after its last language is shut down")
	}

	// Shut down all engines.
	_, err = a.handleShutdown(ctx, gen.LspShutdownReq{RootUri: "", Language: ""})
	if err != nil {
		t.Fatalf("handleShutdown(empty): %v", err)
	}
	if len(a.servers) != 0 {
		t.Errorf("servers map should be empty, got %+v", a.servers)
	}
}
