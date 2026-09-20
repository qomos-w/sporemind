package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Sentinel errors for in-process (c-shared) reload blocking. A Go c-shared
// library stays mapped for the host process lifetime, so a plugin that is
// unload-pending or holds a different artifact cannot be swapped until the
// next host restart. These sentinels let callers classify the failure
// programmatically (errors.Is) instead of matching error strings.
var (
	// The "(in-process)" transport qualifier was dropped from the messages:
	// both classes also surface for subprocess plugins whenever the record
	// cannot be swapped (round-9 breakpoint #3 — the text claimed a transport
	// the caller did not have).
	ErrInProcessUnloadPending     = errors.New("pluginhost: artifact unload-pending")
	ErrInProcessDifferentArtifact = errors.New("pluginhost: artifact already loaded with a different artifact")
)

// IsInProcessDeferredError reports whether err is one of the in-process
// reload-blocking classes that a host restart resolves: the plugin is either
// unload-pending or holds a different artifact that cannot be swapped while
// the host lives. The caller must additionally confirm the TARGET transport
// is in-process; the same error class is a hard failure for subprocess
// plugins (which can always be swapped).
func IsInProcessDeferredError(err error) bool {
	return errors.Is(err, ErrInProcessUnloadPending) ||
		errors.Is(err, ErrInProcessDifferentArtifact)
}

// IsArtifactConflictError reports whether err is the different-artifact
// conflict, matching across the actor wire too: the planner wraps handler
// errors as text ("gospore.handler.error: pluginhost: artifact already
// loaded with a different artifact: ..."), so errors.Is alone is not enough
// on the caller side. Callers use this to decide whether an unload+retry
// (same-ID re-registration) can resolve the conflict.
func IsArtifactConflictError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrInProcessDifferentArtifact) {
		return true
	}
	return strings.Contains(err.Error(), ErrInProcessDifferentArtifact.Error())
}

// ArtifactRecord captures the runtime state of a loaded native artifact:
// the manifest/ABI used at load time, the validated artifact hash, the library
// handle, the bound invoke symbol, and the callable handlers installed in the
// host's handler map.
type ArtifactRecord struct {
	PluginID     string
	Manifest     gen.AppManifest
	Abi          gen.PluginAbi
	ArtifactPath string
	ArtifactHash string
	EntrySymbol  string
	// HTTPAddr is the bound address (host:port) of the plugin's own HTTP
	// listener when the subprocess SDK started one from the OnLoad config;
	// empty for the in-process transport or when no listener runs.
	HTTPAddr string
	Closer   func() error
	Invoke   func(ctx context.Context, callable string, request []byte) ([]byte, error)
	// InvokeStream is the streaming forward-invoke closure (nil for FFI/c-shared
	// transports with no forward-chunk wire). Guarded by the same invokeMu as
	// Invoke so reload drains and nils both atomically.
	InvokeStream InvokeStreamFunc
	CallIDs      []string
	// EventCallIDs are the reserved host→plugin event delivery routes
	// (`plugin.<id>.__event__:<kind>`) for manifest.Listens kinds. Kept
	// separate from CallIDs so the public descriptor/allowlist never
	// exposes event forging through pluginhost.invoke: only
	// pluginhost.event_deliver dispatches these routes.
	EventCallIDs []string

	// invokeMu protects the Invoke field and serializes library close
	// against in-flight C calls. invoke handlers take an RLock for the
	// duration of the call (allowing concurrent invokes of the same or
	// different plugins); Unload / CommitReload take a write Lock to drain
	// all in-flight invokes before closing the native library handle.
	// This replaces the previous design where the loader's global mutex
	// was held for the entire synchronous C call, serializing all
	// plugins system-wide.
	invokeMu sync.RWMutex

	// UnloadPending marks an in-process (c-shared) plugin whose unload was
	// requested: a Go c-shared library cannot be safely unmapped while the
	// host lives (FreeLibrary/dlclose with live plugin goroutines crashes the
	// process), so the record stays put, invoke is disabled, and Load refuses
	// re-entry until a host restart provides a fresh address space.
	UnloadPending bool

	// OnLoadConfig is the per-instance bootstrap payload the process was
	// actually spawned with (verbatim req.OnLoadConfig). Retained so an
	// idempotent re-load can detect a session-secret drift between the
	// appmanager record and the live process (two actors, two persisted
	// files, non-atomic writes) and respawn the backend under the incoming
	// config instead of silently keeping a listener that rejects every
	// gateway token.
	OnLoadConfig []byte
}

// HandlerFunc processes a plugin callable request and returns a response.
// The request and response payloads are opaque bytes (typically spore binary
// codec encoded).
type HandlerFunc func(ctx context.Context, req []byte) ([]byte, error)

// HandlerStreamFunc is the streaming handler-map form: like HandlerFunc but
// onChunk receives each forward-chunk payload (0x08) before the terminal
// response payload is returned. nil emit (FFI/non-streaming degrade) never
// fires.
type HandlerStreamFunc func(ctx context.Context, req []byte, onChunk func([]byte) error) ([]byte, error)

// InvokeStreamFunc is the generic streaming forward-invoke closure an opener
// returns: callable is the bare local callable id. nil when the transport
// carries no forward-chunk wire (FFI/c-shared) — streaming callables degrade
// to zero chunks plus the terminal there. It MUST share the opener/session of
// the sibling unary Invoke, so Open returns both from one call.
type InvokeStreamFunc func(ctx context.Context, callable string, request []byte, onChunk func([]byte) error) ([]byte, error)

// PluginDescriptorData is the loader-side view of a plugin descriptor: just
// the fields the loader needs to publish when an artifact is loaded. The
// actor package converts this into its full PluginDescriptor component.
type PluginDescriptorData struct {
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
	Trusted      bool
	Status       string
}

// ArtifactHost is the minimal surface ArtifactLoader needs from the PluginHost
// actor to install/uninstall handlers, descriptors and closers. It avoids an
// import cycle back into the actor package.
type ArtifactHost interface {
	RegisterHandler(callID string, h HandlerFunc)
	RegisterHandlerStream(callID string, h HandlerStreamFunc)
	UnregisterHandler(callID string)
	RegisterDescriptor(desc PluginDescriptorData)
	RemoveDescriptor(pluginID string)
	RegisterCloser(pluginID string, closer func() error)
	RemoveCloser(pluginID string)
	ReplaceArtifact(oldCallIDs []string, handlers map[string]HandlerFunc, streamHandlers map[string]HandlerStreamFunc, desc PluginDescriptorData, oldCloser, newCloser func() error) error
}

// ArtifactOpener opens a native plugin artifact and returns an invoke
// function and a closer. The pluginhost package delegates the concrete loader
// to the caller (the actor package) to avoid an import cycle between
// pluginhost and pluginloader.
//
// The abi parameter carries the generated manifest ABI (gen.PluginAbi); it is
// the transport-selection signal set (Isolation/TrustClass) that the loader's
// dual-transport opener uses at open time. The allowed parameter is the set of
// EffectiveCapabilities granted to this plugin instance (intersection of
// manifest Permissions and host SecurityPolicy). The opener uses it to build
// the host bridge capability gate so that reverse calls from the plugin are
// authorized per-plugin.
//
// onLoadConfig is the per-instance host→plugin bootstrap payload (subprocess
// transport): the JSON OnLoad config ({"httpAddr","sessionSecret"}) the SDK
// applies at load time. The in-process transport cannot carry an OnLoad
// payload and ignores it.
//
// The returned httpAddr is the bound address (host:port) of the plugin's own
// HTTP listener when the SDK started one from onLoadConfig (reported back in
// the OnLoad response); empty when no listener runs. The loader records it on
// the ArtifactRecord and returns it in the load/commit responses so
// AppManager can build the frontend bootstrap URL.
type ArtifactOpener interface {
	Open(abi gen.PluginAbi, artifactPath, entrySymbol, pluginID string, allowed map[string]struct{}, onLoadConfig []byte) (invoke func(ctx context.Context, callable string, request []byte) ([]byte, error), invokeStream InvokeStreamFunc, closer func() error, httpAddr string, err error)
}

// OnLoadConfigSecret extracts the sessionSecret field from a raw OnLoad
// config payload ("" when absent, unparsable, or empty). The config shape is
// the appmanager↔SDK contract ({"httpAddr","sessionSecret",...}); only the
// secret matters for drift detection, so the rest is ignored.
func OnLoadConfigSecret(config []byte) string {
	if len(config) == 0 {
		return ""
	}
	var parsed struct {
		SessionSecret string `json:"sessionSecret"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil {
		return ""
	}
	return parsed.SessionSecret
}

// ArtifactLoader owns native library lifecycle: open, validate, bind, install
// handlers, and close. It is safe for concurrent use.
type ArtifactLoader struct {
	mu         sync.Mutex
	records    map[string]*ArtifactRecord
	candidates map[string]*ArtifactRecord
	host       ArtifactHost
	opener     ArtifactOpener
}

// NewArtifactLoader constructs a loader bound to a host implementation.
// manifestCapabilitySet converts a plugin manifest's declared Permissions
// into the granted capability set passed to the opener. Under the
// declaration-is-authorization model the manifest is the sole authority.
func manifestCapabilitySet(perms []string) map[string]struct{} {
	out := make(map[string]struct{}, len(perms))
	for _, p := range perms {
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

func NewArtifactLoader(host ArtifactHost) *ArtifactLoader {
	return &ArtifactLoader{
		records:    map[string]*ArtifactRecord{},
		candidates: map[string]*ArtifactRecord{},
		host:       host,
	}
}

// SetOpener attaches the concrete library opener. Called by the actor's
// OnStart after the loader is constructed.
func (l *ArtifactLoader) SetOpener(opener ArtifactOpener) {
	l.mu.Lock()
	l.opener = opener
	l.mu.Unlock()
}

// Load opens the artifact, validates manifest+ABI, binds the invoke symbol,
// installs a handler per declared callable, registers the descriptor and a
// closer that closes the library. Returns the resulting AppStatus.
func (l *ArtifactLoader) Load(ctx context.Context, req gen.PluginArtifactLoadReq) (gen.PluginArtifactLoadResp, error) {
	if req.Manifest.ID == "" {
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: manifest id is required")
	}
	if req.ArtifactPath == "" {
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: artifact path is required")
	}
	schemaHash := ComputeSchemaHash(req.Manifest.Schemas)
	if err := ValidateNativeManifest(req.Manifest, req.Abi, schemaHash); err != nil {
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: manifest validation: %w", err)
	}
	data, err := os.ReadFile(req.ArtifactPath)
	if err != nil {
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: read artifact: %w", err)
	}
	sum := sha256.Sum256(data)
	artifactHash := hex.EncodeToString(sum[:])
	if req.ArtifactHash != "" && !strings.EqualFold(req.ArtifactHash, artifactHash) {
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: artifact hash mismatch")
	}
	if err := NativeBuildPlatformSupported(req.Abi.Isolation, ""); err != nil {
		// fall through; isolation is metadata, real platform check is via the
		// shared-library open failing below.
	}
	l.mu.Lock()
	opener := l.opener
	l.mu.Unlock()
	if opener == nil {
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: artifact opener not configured")
	}
	symbol := strings.TrimSpace(req.EntrySymbol)
	if symbol == "" {
		symbol = defaultNativeInvokeSymbol
	}
	allowed := manifestCapabilitySet(req.Manifest.Permissions)

	// Check whether the plugin is already loaded BEFORE opening the
	// shared library. Opening a duplicate DLL handle and then closing
	// it via FreeLibrary crashes the host process on Windows
	// (0xc0000005). By checking first, we avoid the open-then-close
	// cycle entirely for the idempotent and error paths.
	l.mu.Lock()
	if old, exists := l.records[req.Manifest.ID]; exists {
		if old.UnloadPending {
			l.mu.Unlock()
			return gen.PluginArtifactLoadResp{}, fmt.Errorf("%w: plugin %q: the shared library stays mapped for the host process lifetime, restart the host to fully unload and re-load it", ErrInProcessUnloadPending, old.PluginID)
		}
		// Idempotent reload: if the same artifact (same path AND hash)
		// is already loaded, return the existing record without opening
		// a new library handle — UNLESS the incoming OnLoadConfig carries
		// a different session secret than the live process was spawned
		// with. The record/ArtifactLoads pair is persisted by two actors
		// non-atomically; after a crash between their Saves the process
		// and the appmanager record hold different secrets and every
		// gateway-proxied request 401s until a manual unload/load. A
		// subprocess backend (it runs a listener) is respawned under the
		// incoming config instead; in-process (FFI) mappings cannot be
		// replaced and never run a listener, so they stay idempotent.
		if req.ArtifactHash != "" && old.ArtifactPath == req.ArtifactPath && strings.EqualFold(old.ArtifactHash, req.ArtifactHash) {
			respawn := old.HTTPAddr != "" &&
				OnLoadConfigSecret(req.OnLoadConfig) != "" &&
				OnLoadConfigSecret(req.OnLoadConfig) != OnLoadConfigSecret(old.OnLoadConfig)
			if !respawn {
				l.mu.Unlock()
				return gen.PluginArtifactLoadResp{
					PluginID:     old.PluginID,
					ArtifactHash: old.ArtifactHash,
					HttpAddr:     old.HTTPAddr,
					Status: gen.AppStatus{
						ID: old.Manifest.ID, Runtime: "native", State: "active",
						Version: old.Manifest.Version, Namespace: old.Manifest.Namespace,
					},
				}, nil
			}
			l.mu.Unlock()
			if _, err := l.Unload(ctx, gen.PluginArtifactUnloadReq{PluginID: req.Manifest.ID}); err != nil {
				return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: respawn drifted backend %q: %w", req.Manifest.ID, err)
			}
		} else {
			l.mu.Unlock()
			return gen.PluginArtifactLoadResp{}, fmt.Errorf("%w: plugin %q; unload first", ErrInProcessDifferentArtifact, old.PluginID)
		}
	} else {
		l.mu.Unlock()
	}

	invoke, invokeStream, closer, httpAddr, err := opener.Open(req.Abi, req.ArtifactPath, symbol, req.Manifest.ID, allowed, req.OnLoadConfig)
	if err != nil {
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: open artifact: %w", err)
	}

	l.mu.Lock()
	// Defensive re-check: another concurrent Load may have registered the
	// same plugin while we were opening the library. If so, keep the
	// existing record; leaking the newly opened handle is safer than
	// FreeLibrary-ing it on Windows (0xc0000005 crash).
	if old, exists := l.records[req.Manifest.ID]; exists {
		if req.ArtifactHash != "" && old.ArtifactPath == req.ArtifactPath && strings.EqualFold(old.ArtifactHash, req.ArtifactHash) {
			l.mu.Unlock()
			return gen.PluginArtifactLoadResp{
				PluginID:     old.PluginID,
				ArtifactHash: old.ArtifactHash,
				HttpAddr:     old.HTTPAddr,
				Status: gen.AppStatus{
					ID: old.Manifest.ID, Runtime: "native", State: "active",
					Version: old.Manifest.Version, Namespace: old.Manifest.Namespace,
				},
			}, nil
		}
		l.mu.Unlock()
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("%w: plugin %q; unload first", ErrInProcessDifferentArtifact, old.PluginID)
	}
	record := &ArtifactRecord{
		PluginID:     req.Manifest.ID,
		Manifest:     req.Manifest,
		Abi:          req.Abi,
		ArtifactPath: req.ArtifactPath,
		ArtifactHash: artifactHash,
		EntrySymbol:  symbol,
		HTTPAddr:     httpAddr,
		Closer:       closer,
		Invoke:       invoke,
		InvokeStream: invokeStream,
		OnLoadConfig: append([]byte(nil), req.OnLoadConfig...),
	}
	for _, callable := range req.Manifest.Callables {
		callID := PluginCallID(req.Manifest.ID, callable.ID)
		record.CallIDs = append(record.CallIDs, callID)
	}
	for _, kind := range req.Manifest.Listens {
		record.EventCallIDs = append(record.EventCallIDs, EventRoute(req.Manifest.ID, kind))
	}
	l.records[req.Manifest.ID] = record
	l.mu.Unlock()

	// Install handlers and descriptor outside the loader lock to avoid
	// nesting under the host's locks.
	for _, callable := range req.Manifest.Callables {
		callID := PluginCallID(req.Manifest.ID, callable.ID)
		localCallable := callable.ID
		l.host.RegisterHandler(callID, buildInvokeHandler(record, localCallable))
	}
	for _, kind := range req.Manifest.Listens {
		l.host.RegisterHandler(EventRoute(req.Manifest.ID, kind), buildInvokeHandler(record, ReservedEventPrefix+kind))
	}
	desc := PluginDescriptorData{
		ID:           record.PluginID,
		Name:         record.Manifest.Name,
		Version:      record.Manifest.Version,
		Namespace:    record.Manifest.Namespace,
		Runtime:      NativeRuntime,
		AbiName:      record.Abi.Name,
		AbiVersion:   int32(record.Abi.Version),
		Encoding:     record.Abi.Encoding,
		SchemaHash:   schemaHash,
		Capabilities: append([]string(nil), record.Abi.Capabilities...),
		Callables:    append([]string(nil), record.CallIDs...),
		Isolation:    record.Abi.Isolation,
		TrustClass:   record.Abi.TrustClass,
		Signer:       record.Abi.Signer,
		Trusted:      record.Abi.TrustClass == TrustFirstParty && strings.TrimSpace(record.Abi.Signer) != "",
		Status:       "active",
	}
	l.host.RegisterDescriptor(desc)
	if closer != nil {
		l.host.RegisterCloser(record.PluginID, closer)
	}

	return gen.PluginArtifactLoadResp{
		Status: gen.AppStatus{
			ID:           record.Manifest.ID,
			Runtime:      record.Manifest.Runtime,
			State:        "active",
			Version:      record.Manifest.Version,
			ArtifactHash: artifactHash,
			Entrypoints:  record.Manifest.Entrypoints,
		},
		PluginID:     record.PluginID,
		ArtifactHash: artifactHash,
		HttpAddr:     record.HTTPAddr,
	}, nil
}

func (l *ArtifactLoader) PrepareReload(ctx context.Context, req gen.PluginArtifactReloadPrepareReq) (gen.PluginArtifactReloadPrepareResp, error) {
	loadReq := gen.PluginArtifactLoadReq{Manifest: req.Manifest, Abi: req.Abi, ArtifactPath: req.ArtifactPath, ArtifactHash: req.ArtifactHash, EntrySymbol: req.EntrySymbol, OnLoadConfig: req.OnLoadConfig, AgentID: req.AgentID, RequestID: req.RequestID}
	record, _, err := l.openRecord(loadReq)
	if err != nil {
		return gen.PluginArtifactReloadPrepareResp{}, err
	}
	l.mu.Lock()
	if _, ok := l.records[record.PluginID]; !ok {
		l.mu.Unlock()
		if record.Closer != nil {
			_ = record.Closer()
		}
		return gen.PluginArtifactReloadPrepareResp{}, fmt.Errorf("pluginhost: plugin %q is not loaded", record.PluginID)
	}
	for _, candidate := range l.candidates {
		if candidate.PluginID == record.PluginID {
			l.mu.Unlock()
			if record.Closer != nil {
				_ = record.Closer()
			}
			return gen.PluginArtifactReloadPrepareResp{}, fmt.Errorf("pluginhost: reload already prepared for plugin %q", record.PluginID)
		}
	}
	token := uuid.NewString()
	l.candidates[token] = record
	l.mu.Unlock()
	return gen.PluginArtifactReloadPrepareResp{Token: token, PluginID: record.PluginID, ArtifactHash: record.ArtifactHash, Status: artifactStatus(record)}, nil
}

func (l *ArtifactLoader) CommitReload(_ context.Context, req gen.PluginArtifactReloadCommitReq) (gen.PluginArtifactReloadCommitResp, error) {
	if strings.TrimSpace(req.Token) == "" {
		return gen.PluginArtifactReloadCommitResp{}, fmt.Errorf("pluginhost: reload token is required")
	}
	l.mu.Lock()
	candidate, ok := l.candidates[req.Token]
	if !ok {
		l.mu.Unlock()
		return gen.PluginArtifactReloadCommitResp{}, fmt.Errorf("pluginhost: reload token not found")
	}
	handlers := make(map[string]HandlerFunc, len(candidate.CallIDs))
	streamHandlers := make(map[string]HandlerStreamFunc, len(candidate.CallIDs))
	for _, callable := range candidate.Manifest.Callables {
		callID := PluginCallID(candidate.PluginID, callable.ID)
		handlers[callID] = buildInvokeHandler(candidate, callable.ID)
		if callable.Streaming && candidate.InvokeStream != nil {
			streamHandlers[callID] = buildInvokeStreamHandler(candidate, callable.ID)
		}
	}
	for _, kind := range candidate.Manifest.Listens {
		handlers[EventRoute(candidate.PluginID, kind)] = buildInvokeHandler(candidate, ReservedEventPrefix+kind)
	}
	old, ok := l.records[candidate.PluginID]
	if !ok {
		l.mu.Unlock()
		return gen.PluginArtifactReloadCommitResp{}, fmt.Errorf("pluginhost: plugin %q is not loaded", candidate.PluginID)
	}
	// Drain in-flight invokes on the old record before ReplaceArtifact
	// closes the old library. Lock ordering: l.mu → old.invokeMu (never
	// reversed; the invoke handler acquires record.invokeMu without l.mu).
	old.invokeMu.Lock()
	if err := l.host.ReplaceArtifact(append(append([]string(nil), old.CallIDs...), old.EventCallIDs...), handlers, streamHandlers, descriptorForRecord(candidate), old.Closer, candidate.Closer); err != nil {
		old.invokeMu.Unlock()
		l.mu.Unlock()
		return gen.PluginArtifactReloadCommitResp{}, fmt.Errorf("pluginhost: replace active artifact: %w", err)
	}
	l.records[candidate.PluginID] = candidate
	delete(l.candidates, req.Token)
	old.Invoke = nil
	old.InvokeStream = nil
	old.invokeMu.Unlock()
	l.mu.Unlock()
	return gen.PluginArtifactReloadCommitResp{Status: artifactStatus(candidate), PluginID: candidate.PluginID, ArtifactHash: candidate.ArtifactHash, HttpAddr: candidate.HTTPAddr}, nil
}

func (l *ArtifactLoader) AbortReload(_ context.Context, req gen.PluginArtifactReloadAbortReq) (gen.PluginArtifactReloadAbortResp, error) {
	if strings.TrimSpace(req.Token) == "" {
		return gen.PluginArtifactReloadAbortResp{}, fmt.Errorf("pluginhost: reload token is required")
	}
	l.mu.Lock()
	candidate, ok := l.candidates[req.Token]
	if !ok {
		l.mu.Unlock()
		return gen.PluginArtifactReloadAbortResp{}, fmt.Errorf("pluginhost: reload token not found")
	}
	if candidate.Closer != nil {
		if err := candidate.Closer(); err != nil {
			l.mu.Unlock()
			return gen.PluginArtifactReloadAbortResp{}, fmt.Errorf("pluginhost: close candidate artifact: %w", err)
		}
	}
	delete(l.candidates, req.Token)
	candidate.Invoke = nil
	candidate.InvokeStream = nil
	l.mu.Unlock()
	return gen.PluginArtifactReloadAbortResp{}, nil
}

func (l *ArtifactLoader) openRecord(req gen.PluginArtifactLoadReq) (*ArtifactRecord, string, error) {
	if req.Manifest.ID == "" {
		return nil, "", fmt.Errorf("pluginhost: manifest id is required")
	}
	if req.ArtifactPath == "" {
		return nil, "", fmt.Errorf("pluginhost: artifact path is required")
	}
	schemaHash := ComputeSchemaHash(req.Manifest.Schemas)
	if err := ValidateNativeManifest(req.Manifest, req.Abi, schemaHash); err != nil {
		return nil, "", fmt.Errorf("pluginhost: manifest validation: %w", err)
	}
	data, err := os.ReadFile(req.ArtifactPath)
	if err != nil {
		return nil, "", fmt.Errorf("pluginhost: read artifact: %w", err)
	}
	sum := sha256.Sum256(data)
	artifactHash := hex.EncodeToString(sum[:])
	if req.ArtifactHash != "" && !strings.EqualFold(req.ArtifactHash, artifactHash) {
		return nil, "", fmt.Errorf("pluginhost: artifact hash mismatch")
	}
	l.mu.Lock()
	opener := l.opener
	l.mu.Unlock()
	if opener == nil {
		return nil, "", fmt.Errorf("pluginhost: artifact opener not configured")
	}
	symbol := strings.TrimSpace(req.EntrySymbol)
	if symbol == "" {
		symbol = defaultNativeInvokeSymbol
	}
	allowed := manifestCapabilitySet(req.Manifest.Permissions)
	invoke, invokeStream, closer, httpAddr, err := opener.Open(req.Abi, req.ArtifactPath, symbol, req.Manifest.ID, allowed, req.OnLoadConfig)
	if err != nil {
		return nil, "", fmt.Errorf("pluginhost: open artifact: %w", err)
	}
	record := &ArtifactRecord{PluginID: req.Manifest.ID, Manifest: req.Manifest, Abi: req.Abi, ArtifactPath: req.ArtifactPath, ArtifactHash: artifactHash, EntrySymbol: symbol, HTTPAddr: httpAddr, Closer: closer, Invoke: invoke, InvokeStream: invokeStream, OnLoadConfig: append([]byte(nil), req.OnLoadConfig...)}
	for _, callable := range req.Manifest.Callables {
		record.CallIDs = append(record.CallIDs, PluginCallID(req.Manifest.ID, callable.ID))
	}
	for _, kind := range req.Manifest.Listens {
		record.EventCallIDs = append(record.EventCallIDs, EventRoute(req.Manifest.ID, kind))
	}
	return record, schemaHash, nil
}

func artifactStatus(record *ArtifactRecord) gen.AppStatus {
	return gen.AppStatus{ID: record.Manifest.ID, Runtime: record.Manifest.Runtime, State: "active", Version: record.Manifest.Version, ArtifactHash: record.ArtifactHash, Entrypoints: record.Manifest.Entrypoints}
}

// ArtifactPath returns the loaded build artifact path recorded for pluginID,
// or "" when no record exists. Host-side consumers (generated-media placement
// derives the app dir from it) read it through this accessor instead of
// touching the loader's records directly.
func (l *ArtifactLoader) ArtifactPath(pluginID string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if record, ok := l.records[pluginID]; ok {
		return record.ArtifactPath
	}
	return ""
}

func (l *ArtifactLoader) installRecord(record *ArtifactRecord) {
	for _, callable := range record.Manifest.Callables {
		callID := PluginCallID(record.PluginID, callable.ID)
		l.host.RegisterHandler(callID, buildInvokeHandler(record, callable.ID))
		if callable.Streaming && record.InvokeStream != nil {
			l.host.RegisterHandlerStream(callID, buildInvokeStreamHandler(record, callable.ID))
		}
	}
	for _, kind := range record.Manifest.Listens {
		l.host.RegisterHandler(EventRoute(record.PluginID, kind), buildInvokeHandler(record, ReservedEventPrefix+kind))
	}
	l.host.RegisterDescriptor(descriptorForRecord(record))
	if record.Closer != nil {
		l.host.RegisterCloser(record.PluginID, record.Closer)
	} else {
		l.host.RemoveCloser(record.PluginID)
	}
}

func descriptorForRecord(record *ArtifactRecord) PluginDescriptorData {
	return PluginDescriptorData{ID: record.PluginID, Name: record.Manifest.Name, Version: record.Manifest.Version, Namespace: record.Manifest.Namespace, Runtime: NativeRuntime, AbiName: record.Abi.Name, AbiVersion: int32(record.Abi.Version), Encoding: record.Abi.Encoding, SchemaHash: ComputeSchemaHash(record.Manifest.Schemas), Capabilities: append([]string(nil), record.Abi.Capabilities...), Callables: append([]string(nil), record.CallIDs...), Isolation: record.Abi.Isolation, TrustClass: record.Abi.TrustClass, Signer: record.Abi.Signer, Trusted: record.Abi.TrustClass == TrustFirstParty && strings.TrimSpace(record.Abi.Signer) != "", Status: "active"}
}

func (l *ArtifactLoader) MaxOutputBytes(pluginID string) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	record := l.records[pluginID]
	if record == nil || record.Manifest.Security == nil {
		return 0
	}
	return uint64(record.Manifest.Security.MaxOutputBytes)
}

// Unload removes all handlers for the plugin, runs any plugin-provided
// OnUnload (via the declared ABI lifecycle — future), removes the descriptor
// and closer, and closes the library handle. Returns the number of handlers
// removed.
func (l *ArtifactLoader) Unload(ctx context.Context, req gen.PluginArtifactUnloadReq) (gen.PluginArtifactUnloadResp, error) {
	if req.PluginID == "" {
		return gen.PluginArtifactUnloadResp{}, fmt.Errorf("pluginhost: plugin id is required")
	}
	l.mu.Lock()
	record, ok := l.records[req.PluginID]
	if !ok {
		l.mu.Unlock()
		// Idempotent success: nothing loaded under this ID means nothing to
		// unload. This is the recovery path for persisted-but-unloadable
		// artifacts (e.g. the artifact file vanished before a host restart,
		// so restore could not install the record): appmanager's unregister
		// front half calls artifact_unload for every registered plugin
		// and a hard error here wedges the record in unload_failed forever.
		return gen.PluginArtifactUnloadResp{Removed: 0}, nil
	}
	// Acquire the per-record write lock to drain all in-flight invokes
	// before closing the native library. This blocks until every concurrent
	// invoke releases its RLock, preventing use-after-free of the shared
	// library handle. Lock ordering: l.mu → record.invokeMu (never reversed).
	record.invokeMu.Lock()

	// In-process (c-shared) plugins cannot be safely unmapped while the host
	// lives — FreeLibrary/dlclose with live plugin goroutines crashes the
	// process. Unload degrades to "unload-pending": disable invoke, keep the
	// record so Load refuses re-entry on the stale mapping, and require a
	// host restart to fully reclaim. Subprocess plugins are killed for real.
	if record.Abi.Isolation == IsolationInProcess {
		record.UnloadPending = true
		record.Invoke = nil
		record.invokeMu.Unlock()
		l.mu.Unlock()
		removed := int32(0)
		for _, callID := range append(record.CallIDs, record.EventCallIDs...) {
			l.host.UnregisterHandler(callID)
			removed++
		}
		l.host.RemoveDescriptor(record.PluginID)
		l.host.RemoveCloser(record.PluginID)
		return gen.PluginArtifactUnloadResp{Removed: removed}, nil
	}

	if record.Closer != nil {
		if closeErr := record.Closer(); closeErr != nil {
			record.invokeMu.Unlock()
			l.mu.Unlock()
			return gen.PluginArtifactUnloadResp{}, fmt.Errorf("pluginhost: close artifact: %w", closeErr)
		}
	}
	delete(l.records, req.PluginID)
	record.Invoke = nil
	record.invokeMu.Unlock()
	l.mu.Unlock()

	removed := int32(0)
	for _, callID := range append(record.CallIDs, record.EventCallIDs...) {
		l.host.UnregisterHandler(callID)
		removed++
	}
	l.host.RemoveDescriptor(record.PluginID)
	l.host.RemoveCloser(record.PluginID)
	return gen.PluginArtifactUnloadResp{Removed: removed}, nil
}

// Listeners returns the sorted plugin IDs whose loaded manifest declares the
// given host event kind — the event_deliver fan-out set.
func (l *ArtifactLoader) Listeners(kind string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, record := range l.records {
		for _, k := range record.Manifest.Listens {
			if k == kind {
				out = append(out, record.PluginID)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// HasPermission reports whether the loaded plugin's manifest declares perm in
// its Permissions (the granted-capability set under the
// declaration-is-authorization model). event_deliver consults it for the
// per-kind capability gate on capability-restricted host events.
func (l *ArtifactLoader) HasPermission(pluginID, perm string) bool {
	if perm == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	record, ok := l.records[pluginID]
	if !ok {
		return false
	}
	for _, p := range record.Manifest.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// Get returns the in-memory record for a loaded plugin, or nil if not loaded.
func (l *ArtifactLoader) Get(pluginID string) (*ArtifactRecord, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[pluginID]
	return rec, ok
}

// ComputeSchemaHash derives a stable aggregate hash for a manifest's declared
// schemas by concatenating each schema's name and hash and hashing the result
// with sha256. Returns the lowercase hex digest, or "" when no schemas are
// declared so the descriptor and validation distinguish schema-less plugins.
func ComputeSchemaHash(schemas []gen.AppSchemaRef) string {
	if len(schemas) == 0 {
		return ""
	}
	h := sha256.New()
	for _, s := range schemas {
		h.Write([]byte(s.Name))
		h.Write([]byte(s.Hash))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)
}

// defaultNativeInvokeSymbol is the conventional native ABI entry point name.
// The concrete symbol lives in pkg/pluginloader to avoid an import cycle.
const defaultNativeInvokeSymbol = "PluginInvoke"

// buildInvokeHandler creates a HandlerFunc for a single callable bound to the
// loaded library's invoke function. The handler dispatches via the record's
// Invoke closure captured at Open time. localCallable is the plugin-local
// callable name (e.g. "greet"), as opposed to the namespaced route id.
//
// The per-record invokeMu RWMutex is held for the duration of the call. This
// allows concurrent invokes across all plugins (different records have
// independent locks) and even concurrent invokes of callables within the same
// plugin, while ensuring that Unload/CommitReload cannot close the native
// library handle while an invoke is in flight.
func buildInvokeHandler(record *ArtifactRecord, localCallable string) HandlerFunc {
	return func(ctx context.Context, req []byte) ([]byte, error) {
		record.invokeMu.RLock()
		defer record.invokeMu.RUnlock()
		invoke := record.Invoke
		if invoke == nil {
			return nil, fmt.Errorf("pluginhost: plugin %q invoke is closed", record.PluginID)
		}
		return invoke(ctx, localCallable, req)
	}
}

// buildInvokeStreamHandler binds a callable id to the record's generic
// InvokeStream closure, mirroring buildInvokeHandler. nil InvokeStream (FFI)
// is never installed: installRecord only registers a stream handler when the
// record carries a forward-chunk wire, so this dereference is safe.
func buildInvokeStreamHandler(record *ArtifactRecord, localCallable string) HandlerStreamFunc {
	return func(ctx context.Context, req []byte, onChunk func([]byte) error) ([]byte, error) {
		record.invokeMu.RLock()
		defer record.invokeMu.RUnlock()
		invokeStream := record.InvokeStream
		if invokeStream == nil {
			return nil, fmt.Errorf("pluginhost: plugin %q stream invoke is closed", record.PluginID)
		}
		return invokeStream(ctx, localCallable, req, onChunk)
	}
}
