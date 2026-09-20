package sporeapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/codec"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/gospore/schema"
	"github.com/qomos-w/spore/identity"
	sporesch "github.com/qomos-w/spore/schema"
	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/spore/transport"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/sporebridge"
)

type Actor struct {
	actor.Host
	Manifest          gen.AppManifest                    `gospore:"component,admin"`
	PackageHash       string                             `gospore:"component,admin"`
	EntryModule       string                             `gospore:"component,admin"`
	Modules           map[string]string                  `gospore:"component,admin"`
	Assets            map[string][]byte                  `gospore:"component,admin"`
	SchemaDescriptors map[string]gen.AppObjectDescriptor `gospore:"component,admin"`
	State             map[string]any                     `gospore:"component,admin"`
	StateVersion      int                                `gospore:"component,admin"` // incremented on each state mutation
	MigrationVersion  int                                `gospore:"component,admin"` // bumped on each reload that carries migrated state

	actorID             string
	allowedCapabilities map[string]struct{}
	hostSeams           sporebridge.ServiceHost // captured in OnInit; backs the "invoke" capability
	mu                  sync.Mutex
	runtime             *script.Runtime
	codec               codec.Codec
	schemas             schema.Reader
	store               persist.Persist
	assetTiers          []AssetTier // project/user/builtin resolution tiers
	resolvedAssets      map[string][]byte
}

var _ persist.Persistent = (*Actor)(nil)

func NewActor(manifest gen.AppManifest, entry string, modules map[string]string, assets map[string][]byte, descriptors map[string]gen.AppObjectDescriptor, allowedCaps map[string]struct{}) func() actor.Actor {
	return func() actor.Actor {
		return &Actor{Manifest: manifest, EntryModule: entry, Modules: modules, Assets: assets, SchemaDescriptors: descriptors, allowedCapabilities: allowedCaps}
	}
}

func (a *Actor) Type() string { return "sporeapp" }

func (a *Actor) OnInit(ctx actor.Context) error {
	a.actorID = ctx.Self().ID().String()
	a.codec = ctx.Codec()
	a.schemas = ctx.Schemas()
	a.hostSeams = actorSeams{lookupService: ctx.LookupService, self: ctx.Self()}
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("sporeapp"))
		if err != nil {
			return err
		}
	}
	if a.Modules == nil {
		a.Modules = map[string]string{}
	}
	if a.Assets == nil {
		a.Assets = map[string][]byte{}
	}
	if a.State == nil {
		a.State = map[string]any{}
	}
	if err := a.Load(); err != nil {
		ctx.Logger().Error("sporeapp: load state failed", "error", err)
	}
	registered, err := a.registerAppSchemas()
	if err != nil {
		return err
	}
	a.schemas = registered
	return a.loadRuntime()
}

func (a *Actor) OnStart(ctx actor.Context) error {
	// Script execution lane: invoke runs arbitrary-duration user scripts and
	// reload destroys/recreates the script runtime. Both are routed to the
	// dedicated "spore_exec" stateful lane so the owner lane keeps answering
	// control/query callables (sporeapp.state / sporeapp.state_set) while a
	// script is running. Same-app scripts queue on this lane — the intended
	// single-writer semantics for the script runtime table (a.runtime /
	// a.schemas); cross-app apps are separate actor instances and stay
	// concurrent.
	if err := ctx.RegisterLoop("spore_exec", actor.ModeStateful); err != nil {
		return err
	}
	if err := ctx.Register("sporeapp.invoke", a.handleInvoke, actor.Public(), actor.WithLoop("spore_exec")); err != nil {
		return err
	}
	if err := ctx.Register("sporeapp.state", a.handleState, actor.Public()); err != nil {
		return err
	}
	if err := ctx.Register("sporeapp.state_set", a.handleStateSet, actor.Public()); err != nil {
		return err
	}
	if err := ctx.Register("sporeapp.reload", a.handleReload, actor.AdminOnly(), actor.WithLoop("spore_exec")); err != nil {
		return err
	}
	return nil
}

func (a *Actor) OnStop(_ actor.Context) error {
	if a.runtime != nil {
		_ = a.runtime.Close()
	}
	return a.Save()
}

func (a *Actor) Save() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("sporeapp"))
		if err != nil {
			return err
		}
	}
	return a.store.Save(a.actorID, struct {
		Manifest gen.AppManifest                    `json:"manifest"`
		Hash     string                             `json:"packageHash,omitempty"`
		Entry    string                             `json:"entry"`
		Modules  map[string]string                  `json:"modules"`
		Assets   map[string][]byte                  `json:"assets,omitempty"`
		Schemas  map[string]gen.AppObjectDescriptor `json:"schemaDescriptors,omitempty"`
		State    map[string]any                     `json:"state"`
		StateVer int                                `json:"stateVersion,omitempty"`
	}{a.Manifest, a.PackageHash, a.EntryModule, a.Modules, a.Assets, a.SchemaDescriptors, a.State, a.StateVersion})
}

func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("sporeapp"))
		if err != nil {
			return err
		}
	}
	var saved struct {
		Manifest gen.AppManifest                    `json:"manifest"`
		Hash     string                             `json:"packageHash,omitempty"`
		Entry    string                             `json:"entry"`
		Modules  map[string]string                  `json:"modules"`
		Assets   map[string][]byte                  `json:"assets,omitempty"`
		Schemas  map[string]gen.AppObjectDescriptor `json:"schemaDescriptors,omitempty"`
		State    map[string]any                     `json:"state"`
		StateVer int                                `json:"stateVersion,omitempty"`
	}
	if err := persist.LoadOrZero(a.store, a.actorID, &saved); err != nil {
		return err
	}
	if saved.Manifest.ID != "" {
		a.Manifest, a.PackageHash, a.EntryModule, a.Modules, a.Assets, a.SchemaDescriptors, a.State, a.StateVersion = saved.Manifest, saved.Hash, saved.Entry, saved.Modules, saved.Assets, saved.Schemas, saved.State, saved.StateVer
	}
	return nil
}

func (a *Actor) loadRuntime() error {
	if a.Manifest.ID == "" || a.EntryModule == "" {
		return fmt.Errorf("sporeapp: manifest and entry module are required")
	}
	pkg := script.Package{
		AppID:        a.Manifest.ID,
		Version:      a.Manifest.Version,
		EntryModule:  a.EntryModule,
		Modules:      a.Modules,
		SchemaRefs:   schemaRefs(a.Manifest),
		AssetRefs:    assetRefs(a.Manifest),
		Dependencies: dependencyMap(a.Manifest),
		Hash:         a.PackageHash,
	}
	if err := pkg.Validate(); err != nil {
		return fmt.Errorf("sporeapp: package validation failed: %w", err)
	}
	rt, err := script.NewRuntime()
	if err != nil {
		return err
	}
	if err := a.bindCapabilities(rt); err != nil {
		_ = rt.Close()
		return err
	}
	if err := a.bindSchemaTypes(rt); err != nil {
		_ = rt.Close()
		return err
	}
	if err := rt.LoadPackage(pkg); err != nil {
		_ = rt.Close()
		return err
	}
	if err := a.validateSchemaRefs(); err != nil {
		_ = rt.Close()
		return err
	}
	if err := validateExports(rt, a.EntryModule, a.Manifest); err != nil {
		_ = rt.Close()
		return fmt.Errorf("sporeapp: export validation failed: %w", err)
	}
	if err := validateCapabilityConsistency(rt, a.allowedCapabilities); err != nil {
		_ = rt.Close()
		return err
	}
	// Resolve declared assets using the project/user/builtin override tiers.
	// Assets that can't be resolved are silently skipped — the runtime will
	// use JSON fallback for opaque types.
	a.resolveDeclaredAssets()
	a.runtime = rt
	return nil
}

// resolveDeclaredAssets resolves every asset declared in the manifest using
// the configured asset tiers. Successfully resolved assets are stored in
// resolvedAssets for runtime access. This implements the project > user >
// builtin override rule at the runtime layer.
func (a *Actor) resolveDeclaredAssets() {
	a.resolvedAssets = make(map[string][]byte, len(a.Assets))
	for path, data := range a.Assets {
		a.resolvedAssets[path] = data
	}
	for _, ref := range assetRefs(a.Manifest) {
		if _, ok := a.resolvedAssets[ref]; ok {
			continue
		}
		if data, ok := resolveAsset(ref, a.assetTiers); ok {
			a.resolvedAssets[ref] = data
		}
	}
}

// SetAssetDirs configures the project/user/builtin directory paths for
// asset resolution. This should be called before loadRuntime (typically
// during initialization by the AppManager).
func (a *Actor) SetAssetDirs(projectDir, userDir, builtinDir string) {
	a.assetTiers = assetTiersForApp(a.Manifest.Namespace, projectDir, userDir, builtinDir)
}

// capabilityHostBinding describes one host function that a manifest
// permission unlocks inside the script runtime.
type capabilityHostBinding struct {
	Namespace string
	Name      string
	bind      func(a *Actor) any
}

// actorSeams adapts an actor's OnInit context into the minimal
// sporebridge.ServiceHost surface, without retaining the whole context.
type actorSeams struct {
	lookupService func(string) (ref.Ref, bool)
	self          ref.Ref
}

func (s actorSeams) LookupService(name string) (ref.Ref, bool) { return s.lookupService(name) }
func (s actorSeams) Self() ref.Ref                             { return s.self }

// hostInvoke backs the "invoke" capability: script-side
// host.invoke(callID, payload) relays to any host-resolvable actor
// callable with an all-JSON wire (mcp.call_tool, plain services, ...).
// The per-call wall clock is capped by domain.DefaultInvokeTimeout inside
// the bridge; the caller role stays the bridge default ("developer") so
// target-side policy ladders still apply.
func (a *Actor) hostInvoke(callID string, payload map[string]any) (map[string]any, error) {
	if a.hostSeams == nil {
		return nil, fmt.Errorf("sporeapp: host.invoke unavailable: actor seams not captured")
	}
	return sporebridge.NewHost(a.hostSeams).InvokeCtx(context.Background(), callID, payload)
}

// hostInvokeApp backs host.invoke_app(appID, callable, payload, agentID):
// the appmanager.invoke wire carries the nested payload as raw JSON bytes
// and requires an agent identity (resolveInvokeAuth denies empty AgentID),
// neither of which a script-side map can express. payload follows the
// sporeapp positional convention (a JSON array of args, or nil for
// zero-arg callables); this helper performs the same typed wrapping the
// agent tool path does (turn_engine_app.go runAppToolCall) and decodes the
// response payload back into a map.
func (a *Actor) hostInvokeApp(appID, callable string, payload any, agentID string) (map[string]any, error) {
	if a.hostSeams == nil {
		return nil, fmt.Errorf("sporeapp: host.invoke_app unavailable: actor seams not captured")
	}
	target, ok := a.hostSeams.LookupService("appmanager")
	if !ok {
		return nil, fmt.Errorf("sporeapp: appmanager service not available")
	}
	var raw []byte
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("sporeapp: marshal app payload: %w", err)
		}
	}
	req, err := json.Marshal(gen.AppManagerInvokeReq{ID: appID, Callable: callable, Payload: raw, AgentID: agentID})
	if err != nil {
		return nil, fmt.Errorf("sporeapp: marshal app invoke request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), domain.DefaultInvokeTimeout)
	defer cancel()
	call := target.Invoke(ctx, "appmanager.invoke", req, map[string]string{"gospore.caller_role": "developer"})
	if call == nil {
		return nil, fmt.Errorf("sporeapp: invoke appmanager.invoke returned nil call")
	}
	defer call.Close()
	respRaw, err := call.RecvRaw()
	if err != nil {
		return nil, fmt.Errorf("sporeapp: appmanager.invoke %s.%s: %w", appID, callable, err)
	}
	var resp gen.AppManagerInvokeResp
	if len(respRaw) > 0 {
		if err := json.Unmarshal(respRaw, &resp); err != nil {
			return nil, fmt.Errorf("sporeapp: decode appmanager.invoke response: %w", err)
		}
	}
	if len(resp.Payload) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Payload, &out); err != nil {
		// Non-JSON app payloads (e.g. plain text) surface as {"text": ...}.
		return map[string]any{"text": string(resp.Payload)}, nil
	}
	return out, nil
}

// bindCapabilities binds host functions for each capability in the allowed
// set. A declared capability with no registered host binding is a
// manifest/runtime drift and fails the load with a diagnostic.
func (a *Actor) bindCapabilities(rt *script.Runtime) error {
	for capName := range a.allowedCapabilities {
		bindings, ok := capabilityHostBindings[capName]
		if !ok {
			return fmt.Errorf("sporeapp: declared capability %q has no host binding", capName)
		}
		for _, b := range bindings {
			if err := rt.BindFunc(b.Namespace, b.Name, b.bind(a)); err != nil {
				return fmt.Errorf("sporeapp: bind capability %q: %w", capName, err)
			}
		}
	}
	return nil
}

// bindSchemaTypes exposes manifest-declared generated structs to the script
// package under the app namespace, allowing typed callable parameters.
func (a *Actor) bindSchemaTypes(rt *script.Runtime) error {
	for _, ref := range a.Manifest.Schemas {
		if ref.SchemaID <= 0 || ref.Name == "" {
			continue
		}
		typ, ok := sporesch.StructTypeByID(uint64(ref.SchemaID))
		if !ok {
			continue
		}
		_, desc := buildDescriptors(typ, ref.Name, uint64(ref.SchemaID))
		if err := rt.BindStructDesc("app", ref.Name, desc); err != nil {
			return fmt.Errorf("sporeapp: bind schema type %q: %w", ref.Name, err)
		}
	}
	return nil
}

// validateCapabilityConsistency ensures the runtime's bound host functions
// are exactly those granted by the allowed capability set — nothing bound
// without a granted capability, and every granted capability bound.
func validateCapabilityConsistency(rt *script.Runtime, allowed map[string]struct{}) error {
	// Build the set of (namespace, name) pairs that the allowed capabilities
	// should produce.
	expected := make(map[string]bool)
	for capName := range allowed {
		for _, b := range capabilityHostBindings[capName] {
			expected[b.Namespace+"."+b.Name] = true
		}
	}
	for _, fn := range rt.BoundFunctions() {
		key := fn.Namespace + "." + fn.Name
		if !expected[key] {
			return fmt.Errorf("sporeapp: host function %s bound without granted capability", key)
		}
	}
	return nil
}

// hostStateGet returns a snapshot of the app's durable state. Exposed to the
// script runtime under the "state" capability as app.stateGet.
func (a *Actor) hostStateGet() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]any, len(a.State))
	for k, v := range a.State {
		out[k] = v
	}
	return out
}

// hostStateSet merges values into the app's durable state, bumps the state
// version, and persists. Exposed to the script runtime under the "state"
// capability as app.stateSet.
func (a *Actor) hostStateSet(key string, value any) {
	a.mu.Lock()
	if a.State == nil {
		a.State = map[string]any{}
	}
	a.State[key] = value
	a.StateVersion++
	a.mu.Unlock()
	_ = a.Save()
}

// validateExports ensures every callable declared in the manifest is actually
// exported by the loaded script runtime. This catches schema / implementation
// drift before the app becomes callable.
func validateExports(rt *script.Runtime, entryModule string, m gen.AppManifest) error {
	exports, err := rt.Exports(entryModule)
	if err != nil {
		return fmt.Errorf("read exports: %w", err)
	}
	exported := make(map[string]struct{}, len(exports))
	for _, e := range exports {
		exported[e.Name] = struct{}{}
	}
	for _, c := range m.Callables {
		if _, ok := exported[c.ID]; !ok {
			return fmt.Errorf("manifest callable %q not exported by package", c.ID)
		}
	}
	return nil
}

// schemaRefs extracts schema reference names from the manifest for package
// validation. The runtime uses these to ensure all declared schema types are
// available before execution.
func schemaRefs(m gen.AppManifest) []string {
	refs := make([]string, 0, len(m.Schemas))
	for _, s := range m.Schemas {
		if s.Name != "" {
			refs = append(refs, s.Name)
		}
	}
	return refs
}

// validateSchemaRefs validates every schema reference declared in the manifest
// at load time, so malformed refs are rejected before the app becomes
// runnable. A SchemaID of 0 means "unregistered" and skips the hash check;
// any registered ref (SchemaID > 0) must carry a non-empty hash, and every ref
// must have a non-empty name and a non-negative SchemaID.
func (a *Actor) validateSchemaRefs() error {
	for _, s := range a.Manifest.Schemas {
		if s.Name == "" {
			return fmt.Errorf("sporeapp: schema ref has empty name")
		}
		if s.SchemaID < 0 {
			return fmt.Errorf("sporeapp: schema ref %q has negative SchemaID %d", s.Name, s.SchemaID)
		}
		// SchemaID == 0 means "unregistered" — a hash is optional in that case.
		if s.SchemaID != 0 && s.Hash == "" {
			return fmt.Errorf("sporeapp: schema ref %q (id %d) has empty hash", s.Name, s.SchemaID)
		}
	}
	return nil
}

// assetRefs extracts asset reference names from the manifest. QuickApps may
// reference packaged assets (templates, static files, schema descriptors) that
// the runtime resolves before loading. Schema names are treated as asset
// references because they identify compiled schema descriptors that must be
// available at load time.
func assetRefs(m gen.AppManifest) []string {
	seen := map[string]bool{}
	refs := make([]string, 0, len(m.Schemas))
	for _, s := range m.Schemas {
		if s.Name != "" && !seen[s.Name] {
			refs = append(refs, s.Name)
			seen[s.Name] = true
		}
	}
	return refs
}

// resolveAsset implements the project > user > builtin override rule for
// asset resolution. It tries each tier in priority order and returns the
// first match. The tiers parameter provides directory paths keyed by origin.
func resolveAsset(name string, tiers []AssetTier) ([]byte, bool) {
	for _, tier := range tiers {
		if tier.Dir == "" {
			continue
		}
		path := filepath.Join(tier.Dir, name)
		data, err := os.ReadFile(path)
		if err == nil {
			return data, true
		}
	}
	return nil, false
}

// AssetTier represents a single resolution tier in the override chain.
type AssetTier struct {
	Origin string // "project", "user", or "builtin"
	Dir    string // absolute directory path for this tier
}

// assetTiersForApp returns the resolution tiers in priority order
// (project > user > builtin) for a given app namespace.
func assetTiersForApp(namespace, projectDir, userDir, builtinDir string) []AssetTier {
	base := filepath.Join("apps", namespace, "assets")
	return []AssetTier{
		{Origin: "project", Dir: filepath.Join(projectDir, base)},
		{Origin: "user", Dir: filepath.Join(userDir, base)},
		{Origin: "builtin", Dir: filepath.Join(builtinDir, base)},
	}
}

// dependencyMap converts manifest dependencies into the script package format.
func dependencyMap(m gen.AppManifest) map[string]string {
	if len(m.Dependencies) == 0 {
		return nil
	}
	deps := make(map[string]string, len(m.Dependencies))
	for _, d := range m.Dependencies {
		if d.ID != "" {
			deps[d.ID] = d.Version
		}
	}
	return deps
}

func (a *Actor) handleInvoke(actx actor.Context, req gen.SporeAppInvokeReq) (resp gen.SporeAppInvokeResp, err error) {
	// Defense-in-depth (spore v0.6.0, bytecode-boundary panic collection,
	// #29 phase 4): the spore VM no longer lets invariant violations —
	// including OOM over RuntimeOptions.VMHeapBytes (default 4 MiB) — escape
	// as a raw panic; they are captured into a structured RuntimeError with
	// Diagnostic.Code "vm_internal_panic" delivered on the Result.Error
	// channel (original panic text preserved in message). The Result.Error
	// path below surfaces that. This per-invoke recover stays as a hard
	// safety net: a future regression or a non-VM panic still yields a
	// bounded error instead of tearing the actor.
	defer func() {
		if r := recover(); r != nil {
			resp = gen.SporeAppInvokeResp{}
			err = fmt.Errorf("sporeapp: invoke %q panicked: %s", req.Callable, truncDetail(fmt.Sprint(r)))
		}
	}()
	if req.ID != "" && req.ID != a.Manifest.ID {
		return gen.SporeAppInvokeResp{}, fmt.Errorf("sporeapp: app %q not found", req.ID)
	}
	args, err := a.decodeRequestPayload(req.Callable, req.Payload)
	if err != nil {
		return gen.SporeAppInvokeResp{}, fmt.Errorf("sporeapp: decode request: %s", truncDetail(err.Error()))
	}
	callCtx, cancel := scriptContextFromActor(actx)
	defer cancel()
	budget := executionBudget(a.Manifest.Security)
	if budget.MaxDuration > 0 {
		var timeoutCancel context.CancelFunc
		callCtx, timeoutCancel = context.WithTimeout(callCtx, budget.MaxDuration)
		defer timeoutCancel()
	}
	result, err := a.runtime.CallContext(script.CallContext{Context: callCtx, Budget: budget}, req.Callable, args...)
	if err != nil {
		return gen.SporeAppInvokeResp{}, fmt.Errorf("%s", truncDetail(err.Error()))
	}
	if result.Error != nil {
		// Script runtime failures carry diagnostics with code, path, span and
		// stack — unbounded in aggregate and they travel cross-actor.
		return gen.SporeAppInvokeResp{}, fmt.Errorf("%s", truncDetail(result.Error.Error()))
	}
	payload, err := a.encodeResponsePayload(req.Callable, result.Value)
	if err != nil {
		return gen.SporeAppInvokeResp{}, fmt.Errorf("sporeapp: encode response: %s", truncDetail(err.Error()))
	}
	if budget.MaxOutputBytes > 0 && uint64(len(payload)) > budget.MaxOutputBytes {
		return gen.SporeAppInvokeResp{}, fmt.Errorf("sporeapp: output budget exceeded: %d > %d bytes", len(payload), budget.MaxOutputBytes)
	}
	return gen.SporeAppInvokeResp{Payload: payload}, nil
}

func executionBudget(policy *gen.AppSecurityPolicy) script.ExecutionBudget {
	if policy == nil {
		return script.ExecutionBudget{}
	}
	return script.ExecutionBudget{
		MaxInstructions: uint64(policy.MaxInstructions),
		MaxDuration:     time.Duration(policy.MaxDurationMs) * time.Millisecond,
		MaxHostCalls:    uint32(policy.MaxHostCalls),
		MaxOutputBytes:  uint64(policy.MaxOutputBytes),
	}
}

func scriptContextFromActor(actx actor.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	if actx == nil || actx.Done() == nil {
		return ctx, cancel
	}
	go func() {
		select {
		case <-actx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// decodeRequestPayload decodes wire bytes into script-callable arguments.
// If the payload carries the TBC binary magic header and a matching schema is
// found, BinaryCodec materialises the declared Go struct; otherwise JSON is
// assumed. Two JSON shapes are accepted:
//
//   - positional array (the canonical wire form, e.g. ["arg1", {...}]);
//   - named object mapped onto the script signature's parameter names
//     (e.g. {"callId": "shell.bash", "payload": {}}). The agent tool path
//     always forwards the LLM's arguments as an object, so schema-less
//     callables would be uncallable without this fallback. Unknown keys are
//     rejected (a typo must fail loudly, not silently drop an argument to
//     nil); when no key matches any parameter and the callable takes exactly
//     one, the object itself is passed as that single argument (the
//     struct-shaped-JSON case).
func (a *Actor) decodeRequestPayload(callableID string, payload []byte) ([]any, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	if codec.IsTBCData(payload) {
		desc, ok := a.lookupRequestSchema(callableID)
		if ok {
			decoded, err := decodeTypedPayload(desc, payload)
			if err != nil {
				return nil, err
			}
			return []any{decoded}, nil
		}
	}
	var args []any
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil {
		return a.decodeNamedObjectPayload(callableID, payload, err)
	}
	for i := range args {
		args[i] = normaliseNumbers(args[i])
	}
	return args, nil
}

// decodeNamedObjectPayload is the named-object fallback of
// decodeRequestPayload: it maps {"param": value} keys onto the callable's
// script parameters in declaration order. arrErr is the original
// positional-decode error, returned as-is when the payload is not a JSON
// object or the callable's parameters cannot be resolved.
func (a *Actor) decodeNamedObjectPayload(callableID string, payload []byte, arrErr error) ([]any, error) {
	var obj map[string]any
	named := json.NewDecoder(bytes.NewReader(payload))
	named.UseNumber()
	if err := named.Decode(&obj); err != nil {
		return nil, arrErr
	}
	params, err := a.callableParamNames(callableID)
	if err != nil {
		return nil, arrErr
	}
	matched := false
	args := make([]any, len(params))
	for i, name := range params {
		v, ok := obj[name]
		if !ok {
			continue
		}
		matched = true
		args[i] = normaliseNumbers(v)
		delete(obj, name)
	}
	if len(obj) > 0 {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("unknown argument %v for callable %q (parameters: %v)", keys, callableID, params)
	}
	if !matched && len(params) == 1 {
		return []any{normaliseNumbers(obj)}, nil
	}
	return args, nil
}

// callableParamNames returns the declared parameter names of one script
// callable, read from the loaded runtime's export surface.
func (a *Actor) callableParamNames(callableID string) ([]string, error) {
	if a.runtime == nil {
		return nil, fmt.Errorf("runtime not loaded")
	}
	infos, err := a.runtime.Exports(a.EntryModule)
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		if info.Name != callableID {
			continue
		}
		names := make([]string, 0, len(info.Parameters))
		for _, p := range info.Parameters {
			names = append(names, p.Name)
		}
		return names, nil
	}
	return nil, fmt.Errorf("callable %q not exported", callableID)
}

func normaliseNumbers(v any) any {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
		if f, err := n.Float64(); err == nil {
			return f
		}
		return n.String()
	case []any:
		for i := range n {
			n[i] = normaliseNumbers(n[i])
		}
		return n
	case map[string]any:
		for k := range n {
			n[k] = normaliseNumbers(n[k])
		}
		return n
	default:
		return v
	}
}

func decodeTypedPayload(desc sporesch.TypeDesc, payload []byte) (any, error) {
	view := transport.View{Kind: transport.ViewKindFull, Schema: desc, Data: payload}
	binary := &transport.BinaryCodec{}
	if typ, ok := sporesch.StructTypeByID(uint64(desc.TypeID)); ok && typ.Kind() == reflect.Struct {
		target := reflect.New(typ)
		if err := binary.DecodeInto(view, target.Interface()); err != nil {
			return nil, err
		}
		return target.Elem().Interface(), nil
	}
	return binary.Decode(view)
}

// encodeResponsePayload encodes app payloads with BinaryCodec when a schema
// resolves, independent of the inter-actor message codec used by the child.
func (a *Actor) encodeResponsePayload(callableID string, value any) ([]byte, error) {
	if value == nil {
		return []byte{}, nil
	}
	desc, ok := a.lookupResponseSchema(callableID)
	if ok {
		if view, err := (&transport.BinaryCodec{}).Encode(desc, identity.CanonicalID{}, value); err == nil {
			return view.Data, nil
		}
	}
	return json.Marshal(value)
}

func (a *Actor) lookupRequestSchema(callableID string) (sporesch.TypeDesc, bool) {
	return a.lookupSchemaByDirection(callableID, true)
}

func (a *Actor) lookupResponseSchema(callableID string) (sporesch.TypeDesc, bool) {
	return a.lookupSchemaByDirection(callableID, false)
}

func (a *Actor) lookupSchemaByDirection(callableID string, request bool) (sporesch.TypeDesc, bool) {
	if a.schemas == nil {
		return sporesch.TypeDesc{}, false
	}
	var schemaName string
	for _, c := range a.Manifest.Callables {
		if c.ID == callableID {
			if request {
				schemaName = c.RequestSchema
			} else {
				schemaName = c.ResponseSchema
			}
			break
		}
	}
	if schemaName == "" {
		return sporesch.TypeDesc{}, false
	}
	entry, ok := a.schemas.LookupByName(schemaName)
	if !ok {
		return sporesch.TypeDesc{}, false
	}
	return entry.Desc, true
}

func (a *Actor) handleReload(ctx actor.Context, req gen.SporeAppReloadReq) (gen.SporeAppReloadResp, error) {
	if req.ID != "" && req.ID != a.Manifest.ID {
		return gen.SporeAppReloadResp{}, fmt.Errorf("sporeapp: app %q not found", req.ID)
	}
	if req.EntryModule == "" || len(req.Modules) == 0 {
		return gen.SporeAppReloadResp{}, fmt.Errorf("sporeapp: reload package is required")
	}
	// Optimistic concurrency: when the caller pins an expected state version,
	// a mismatch means the app state drifted since the caller migrated it.
	a.mu.Lock()
	current := a.StateVersion
	a.mu.Unlock()
	if req.ExpectedStateVersion != 0 && int64(current) != req.ExpectedStateVersion {
		return gen.SporeAppReloadResp{}, fmt.Errorf("sporeapp: state version conflict: expected %d, current %d", req.ExpectedStateVersion, current)
	}
	if a.runtime == nil {
		return gen.SporeAppReloadResp{}, fmt.Errorf("sporeapp: runtime is not loaded")
	}

	oldSchemas, oldDescriptors := a.schemas, a.SchemaDescriptors
	a.SchemaDescriptors = req.SchemaDescriptors
	registered, err := a.registerAppSchemas()
	if err != nil {
		a.SchemaDescriptors = oldDescriptors
		return gen.SporeAppReloadResp{}, fmt.Errorf("sporeapp: reload schema validation failed: %w", err)
	}
	a.schemas = registered

	// §5: Build and validate a candidate runtime BEFORE the atomic swap, so a
	// broken reload (missing exported callables, drifted capability surface,
	// inconsistent host bindings) can never leave the actor half-reloaded. The
	// candidate mirrors loadRuntime: it binds the actor's allowed capabilities,
	// loads the replacement package, and re-validates declared exports and
	// capability consistency. On failure the candidate is closed and the live
	// runtime is left untouched.
	candidate, err := a.buildReloadCandidate(req.EntryModule, req.Modules, req.PackageHash)
	if err != nil {
		a.schemas, a.SchemaDescriptors = oldSchemas, oldDescriptors
		return gen.SporeAppReloadResp{}, fmt.Errorf("sporeapp: reload validation failed: %w", err)
	}

	// Atomic swap: replace the runtime pointer under the lock and close the
	// superseded runtime. Actor message processing is serialized per actor, so
	// no concurrent handler observes a torn pointer.
	a.mu.Lock()
	oldRuntime := a.runtime
	a.runtime = candidate
	a.mu.Unlock()
	if oldRuntime != candidate {
		_ = oldRuntime.Close()
	}

	// Snapshot old metadata so we can rollback if Save() fails. The runtime
	// swap above is intentionally not rolled back (same semantics as the
	// previous ReloadPackage path): a failed persist restores the durable
	// fields, and a restart reloads from whatever survived on disk.
	a.mu.Lock()
	oldEntry, oldModules, oldAssets, oldHash := a.EntryModule, a.Modules, a.Assets, a.PackageHash
	oldState, oldStateVer, oldMigration := a.State, a.StateVersion, a.MigrationVersion
	a.EntryModule, a.Modules, a.Assets, a.PackageHash = req.EntryModule, req.Modules, req.Assets, req.PackageHash
	a.resolveDeclaredAssets()
	// Migration metadata: a non-empty MigratedState replaces the app state
	// after a successful package swap, bumps the state version, and records a
	// new migration version. A nil MigratedState with a wide expected-version
	// gap warns that a migration was likely skipped.
	if len(req.MigratedState) > 0 {
		a.State = req.MigratedState
		a.StateVersion++
		a.MigrationVersion++
		if ctx != nil {
			ctx.Logger().Info("sporeapp: migration applied", "app", a.Manifest.ID, "version", a.MigrationVersion)
		}
	} else if ctx != nil && req.ExpectedStateVersion != 0 {
		if diff := req.ExpectedStateVersion - int64(current); diff > 1 || diff < -1 {
			ctx.Logger().Warn("sporeapp: reload without migrated state but expected version differs by >1",
				"app", a.Manifest.ID, "expected", req.ExpectedStateVersion, "current", current)
		}
	}
	newStateVer := a.StateVersion
	a.mu.Unlock()
	if err := a.Save(); err != nil {
		// Rollback durable fields — runtime already swapped, but persisted
		// state must remain consistent with what's on disk.
		a.mu.Lock()
		a.EntryModule, a.Modules, a.Assets, a.PackageHash = oldEntry, oldModules, oldAssets, oldHash
		a.schemas, a.SchemaDescriptors = oldSchemas, oldDescriptors
		a.State, a.StateVersion, a.MigrationVersion = oldState, oldStateVer, oldMigration
		a.mu.Unlock()
		return gen.SporeAppReloadResp{}, fmt.Errorf("sporeapp: reload succeeded but persist failed (rolled back metadata): %w", err)
	}
	return gen.SporeAppReloadResp{ID: a.Manifest.ID, Version: a.Manifest.Version, StateVersion: int64(newStateVer)}, nil
}

// buildReloadCandidate constructs a fresh script runtime for the replacement
// package, binds the actor's allowed capabilities, loads the package, and
// validates declared exports and capability consistency. On success it returns
// a runtime ready for an atomic swap in handleReload; on any failure the
// candidate is closed and the original runtime is left untouched.
//
// This is the reload-path analogue of loadRuntime: the same capability and
// export contracts that gate initial load also gate every reload, so a
// replacement package can never silently drop an exported callable or break a
// capability binding.
func (a *Actor) buildReloadCandidate(entry string, modules map[string]string, hash string) (*script.Runtime, error) {
	pkg := script.Package{
		AppID:        a.Manifest.ID,
		Version:      a.Manifest.Version,
		EntryModule:  entry,
		Modules:      modules,
		SchemaRefs:   schemaRefs(a.Manifest),
		AssetRefs:    assetRefs(a.Manifest),
		Dependencies: dependencyMap(a.Manifest),
		Hash:         hash,
	}
	if err := pkg.Validate(); err != nil {
		return nil, fmt.Errorf("package validation: %s", truncDetail(err.Error()))
	}
	rt, err := script.NewRuntime()
	if err != nil {
		return nil, err
	}
	if err := a.bindCapabilities(rt); err != nil {
		_ = rt.Close()
		return nil, err
	}
	if err := rt.LoadPackage(pkg); err != nil {
		_ = rt.Close()
		return nil, fmt.Errorf("load package: %s", truncDetail(err.Error()))
	}
	if err := validateExports(rt, entry, a.Manifest); err != nil {
		_ = rt.Close()
		return nil, fmt.Errorf("export validation: %w", err)
	}
	if err := validateCapabilityConsistency(rt, a.allowedCapabilities); err != nil {
		_ = rt.Close()
		return nil, err
	}
	return rt, nil
}

func (a *Actor) handleStateSet(_ actor.Context, req gen.SporeAppStateSetReq) (map[string]any, error) {
	if req.ID != "" && req.ID != a.Manifest.ID {
		return nil, fmt.Errorf("sporeapp: app %q not found", req.ID)
	}
	a.mu.Lock()
	if a.State == nil {
		a.State = map[string]any{}
	}
	for key, value := range req.State {
		a.State[key] = value
	}
	a.StateVersion++
	a.mu.Unlock()
	if err := a.Save(); err != nil {
		return nil, err
	}
	return a.handleState(nil, gen.SporeAppStateReq{ID: req.ID})
}

// handleState is a stateless (PureContext) snapshot read: it copies the app
// state map under a.mu without mutating anything, so it runs on the forked
// pure loop and stays off the spore_exec lane.
func (a *Actor) handleState(_ actor.PureContext, req gen.SporeAppStateReq) (map[string]any, error) {
	if req.ID != "" && req.ID != a.Manifest.ID {
		return nil, fmt.Errorf("sporeapp: app %q not found", req.ID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make(map[string]any, len(a.State))
	for k, v := range a.State {
		result[k] = v
	}
	return result, nil
}
