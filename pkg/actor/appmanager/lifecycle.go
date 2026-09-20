package appmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/sporemind/pkg/appbinding"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/codec"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/spore/transport"
	"github.com/qomos-w/sporemind/pkg/actor/sporeapp"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// invokeTimeout caps a single QuickApp callable invocation. A hung or
// infinitely-looping script produces a stable timeout error instead of
// blocking the caller indefinitely.
const invokeTimeout = 30 * time.Second
const maxRouteDepth = 8

// lookupCallable finds a callable descriptor by ID in the manifest and returns
// an error if the callable is not declared.
func lookupCallable(manifest gen.AppManifest, callableID string) (gen.AppCallableDescriptor, error) {
	for _, c := range manifest.Callables {
		if c.ID == callableID {
			return c, nil
		}
	}
	return gen.AppCallableDescriptor{}, fmt.Errorf("appmanager: callable %q not found in app %q", callableID, manifest.ID)
}

func (a *Actor) spawnChild(ctx actor.Context, appID string, record appRecord, live bool) error {
	if record.Manifest.Runtime == "native" {
		// Plugins are loaded into the pluginhost service, not spawned as
		// children. The artifact must already be loaded via
		// pluginhost.artifact_load before registration. Here we only record
		// that the app routes through the pluginhost service.
		//
		// On restore (live=false) the appmanager cross-validates against the
		// pluginhost (B1): the record is only marked running when the
		// pluginhost confirms it holds the artifact. When the pluginhost is
		// not reachable yet (it starts after the appmanager in the runtime
		// tree) the record keeps its persisted state — restart_pending stays
		// pending and is activated by the next verification; running stays
		// running and is confirmed by the pluginhost's own OnStart restore.
		verified := false
		if live {
			verified = true
		} else {
			var err error
			verified, err = a.verifyNativeRestored(ctx, appID, record)
			if err != nil {
				return err
			}
		}
		a.withMu(func() {
			a.children[appID] = pluginhostServiceName
			if verified {
				record.State = stateRunning
				record.Error = ""
			}
			record.ActorID = pluginhostServiceName
			a.Records[appID] = record
		})
		// Live registrations push the manifest-declared assets to the
		// pluginhost so /plugin/{appID}/ serves the frontend bundle.
		// Restore (live=false) skips the push: pluginhost starts after the
		// appmanager in the runtime tree and re-registers asset routes from
		// its own persisted AssetStores at its OnStart.
		if live {
			if err := a.pushNativeAssets(ctx, appID, record.Assets); err != nil {
				return err
			}
		}
		return nil
	}
	if record.Manifest.Runtime != "spore" {
		return fmt.Errorf("appmanager: unsupported runtime %q for app %q", record.Manifest.Runtime, appID)
	}
	if record.Manifest.ID == "" || record.EntryModule == "" {
		return fmt.Errorf("appmanager: app %q has missing manifest or entry module", appID)
	}
	var allowed map[string]struct{}
	a.withMu(func() {
		allowed = manifestCapabilitySet(record.Manifest.Permissions)
	})
	props := actor.PropsFromFunc(sporeapp.NewActor(record.Manifest, record.EntryModule, record.Modules, record.Assets, record.SchemaDescriptors, allowed))
	if record.ActorID != "" {
		canonical, err := identity.ParseCanonicalID(record.ActorID)
		if err != nil {
			return err
		}
		props = props.WithID(id.From(canonical))
	}
	ref, err := ctx.Spawn(props, appID)
	if err != nil {
		return fmt.Errorf("appmanager: spawn %q: %w", appID, err)
	}
	a.withMu(func() {
		a.children[appID] = ref.ID().String()
		record.ActorID = ref.ID().String()
		a.Records[appID] = record
	})
	return nil
}

// verifyNativeRestored cross-validates a restored plugin against the
// pluginhost (B1): the appmanager must not mark an app running unless the
// pluginhost actually holds its artifact. It calls pluginhost.artifact_load,
// which is idempotent for an already-loaded (same) artifact (artifact.go:
// "already loaded with the same artifact" branch returns the existing record).
//
// Returns:
//   - (true, nil): the artifact is confirmed loaded; the caller marks running.
//   - (false, nil): no verification possible — the pluginhost is not reachable
//     yet (normal startup ordering: it starts after the appmanager) or the
//     record carries no artifact; the caller keeps the persisted state.
//   - (false, err): the artifact cannot be loaded; the caller marks failed.
func (a *Actor) verifyNativeRestored(ctx actor.Context, appID string, record appRecord) (bool, error) {
	if record.ArtifactPath == "" || record.Abi == nil {
		return false, nil
	}
	pluginRef, ok := ctx.LookupService(pluginhostServiceName)
	if !ok || pluginRef == nil || ctx.Planner() == nil {
		return false, nil
	}
	// Reuse the persisted per-instance secret when there is one: the
	// idempotent path (pluginhost already holds the artifact, e.g. its
	// restart-time restore) does not respawn, so a fresh secret would never
	// reach the process and would desync cookie minting from verification.
	// When the load actually spawns, the secret is (re)delivered and the
	// response's HttpAddr reports the fresh ephemeral port.
	secret := record.SessionSecret
	if secret == "" {
		var err error
		if secret, err = newInstanceSecret(); err != nil {
			return false, err
		}
	}
	appDir := resolveBackendAppDir(record.Manifest.ID, record.ArtifactPath)
	dataDir, err := appDataDirFor(record.Manifest, appDir)
	if err != nil {
		return false, err
	}
	raw, err := json.Marshal(backendLoadConfig{HTTPAddr: backendHTTPAddr, SessionSecret: secret, StaticDir: appDir, DataDir: dataDir, ProjectID: record.ProjectID})
	if err != nil {
		return false, fmt.Errorf("appmanager: marshal backend load config: %w", err)
	}
	// Expand bundle permissions when the dependencies are still registered.
	// The restore verification is usually idempotent (the pluginhost already
	// reloaded the artifact — with its own persisted, expanded ArtifactLoads)
	// and never reads this manifest's permissions; a resolution failure here
	// (a dependency vanished while the app was down) must not brick the
	// restore, so the declared form is the fallback.
	verifyManifest := record.Manifest
	if expanded, expErr := a.loadAuthManifest(record.Manifest); expErr == nil {
		verifyManifest = expanded
	}
	value, err := ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_load", gen.PluginArtifactLoadReq{
		Manifest: verifyManifest, Abi: *record.Abi, ArtifactPath: record.ArtifactPath, ArtifactHash: record.ArtifactHash, OnLoadConfig: raw,
	}).Await()
	if err != nil {
		return false, fmt.Errorf("pluginhost artifact verification failed: %w", err)
	}
	loaded, ok := value.(gen.PluginArtifactLoadResp)
	if !ok || loaded.PluginID != appID {
		return false, fmt.Errorf("pluginhost artifact verification returned invalid status")
	}
	if loaded.Status.State == stateRestartPending {
		// The pluginhost persisted a different artifact (deferred): it does
		// not hold the record's artifact. Report not verified so the
		// persisted state stays honest instead of claiming running.
		return false, nil
	}
	// Confirmed loaded: adopt the reported listener address (fresh port after
	// a respawn, current port on the idempotent path). An empty report means
	// the instance runs no listener — drop stale bootstrap state.
	a.commitBackend(ctx, appID, secret, loaded.HttpAddr)
	return true, nil
}

// dependentsOf returns the sorted IDs of registered apps whose manifests
// declare this app as a dependency — the reverse edge of the dependency
// graph used by the get/list views (reload impact, plugin_load ordering).
// Callers must not hold a.mu.
func (a *Actor) dependentsOf(appID string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dependentsOfLocked(appID)
}

func (a *Actor) dependentsOfLocked(appID string) []string {
	var dependents []string
	for id, m := range a.Apps {
		if id == appID {
			continue
		}
		for _, d := range m.Dependencies {
			if d.ID == appID {
				dependents = append(dependents, id)
				break
			}
		}
	}
	sort.Strings(dependents)
	return dependents
}

func (a *Actor) handleGet(_ actor.PureContext, req gen.AppManagerGetReq) (gen.AppManagerGetResp, error) {
	var manifest gen.AppManifest
	var ok bool
	var record appRecord
	a.withMu(func() {
		manifest, ok = a.Apps[req.ID]
		record = a.Records[req.ID]
	})
	if !ok {
		return gen.AppManagerGetResp{}, fmt.Errorf("appmanager: app %q not found", req.ID)
	}
	status := a.statusFromRecord(manifest, record)
	status.Dependencies = manifest.Dependencies
	status.Dependents = a.dependentsOf(req.ID)
	return gen.AppManagerGetResp{Status: status}, nil
}

// callerIdentity holds the resolved caller identity derived from the actor
// context, not from client-supplied request fields.
type callerIdentity struct {
	AgentID   string
	Role      string
	ProjectID string
	// WorkspaceID is the workspace ACTOR id used for aistats attribution of
	// plugin-issued llm.* reverse calls. It is host-stamped by the turn
	// engine (like AgentId) — never trusted from an external client.
	WorkspaceID string
}

// resolveCaller extracts the real caller identity from the actor context.
// It prefers ctx.Identity() (set by gateway from auth tokens) over
// client-supplied request fields. Request fields are used only as fallback
// for project scope (which is not carried in Identity) and for internal
// actor-to-actor calls where Identity is zero.
func resolveCaller(ctx actor.PureContext, agentID, role, projectID string) callerIdentity {
	return resolveCallerWorkspace(ctx, agentID, role, projectID, "")
}

// resolveCallerWorkspace is resolveCaller with the workspace actor id the
// turn engine stamps onto invoke requests. Only the appmanager.invoke path
// carries it today; other callables have no workspace identity to forward.
func resolveCallerWorkspace(ctx actor.PureContext, agentID, role, projectID, workspaceID string) callerIdentity {
	ci := callerIdentity{AgentID: agentID, Role: role, ProjectID: projectID, WorkspaceID: workspaceID}
	if ctx == nil {
		return ci
	}
	ident := ctx.Identity()
	if !ident.IsZero() {
		if ident.Subject != "" {
			ci.AgentID = ident.Subject
		}
		if ident.Role != "" {
			ci.Role = string(ident.Role)
		}
		// WorkspaceID has no Identity counterpart: a gateway-authenticated
		// caller never carries one, so the request field is dropped outright
		// (prevents a forged WorkspaceId from surviving external auth). Only
		// internal actor-to-actor calls (zero Identity) keep the field the
		// turn engine stamped.
		ci.WorkspaceID = ""
	}
	return ci
}

// audit helpers read a.Apps under a.mu (write paths on the ops lane update
// it concurrently) but call recordAudit OUTSIDE the lock — recordAudit takes
// a.mu itself, so calling it while holding a.mu deadlocks. Same invariant as
// route_token.go:71.

func (a *Actor) auditInvoke(req gen.AppManagerInvokeReq, allowed bool, reason string) {
	var runtime string
	a.withMu(func() {
		runtime = a.Apps[req.ID].Runtime
	})
	a.recordAudit(appbinding.AuditRecord{RequestID: req.RequestID, AppID: req.ID, Runtime: runtime, AgentID: req.AgentID, Role: req.Role, ProjectID: req.ProjectID, Callable: req.Callable, Allowed: allowed, Reason: reason, SessionID: req.SessionID, CallSeq: req.CallSeq})
}

// auditInvokeWithIdentity records an audit entry using resolved caller identity.
func (a *Actor) auditInvokeWithIdentity(req gen.AppManagerInvokeReq, ci callerIdentity, allowed bool, reason string) {
	var runtime string
	a.withMu(func() {
		runtime = a.Apps[req.ID].Runtime
	})
	a.recordAudit(appbinding.AuditRecord{RequestID: req.RequestID, AppID: req.ID, Runtime: runtime, AgentID: ci.AgentID, Role: ci.Role, ProjectID: ci.ProjectID, Callable: req.Callable, Allowed: allowed, Reason: reason, SessionID: req.SessionID, CallSeq: req.CallSeq})
}

// auditRuntime returns an audit callback that stamps the app's runtime onto
// each record produced by the dispatcher.
func (a *Actor) auditRuntime(appID string) func(appbinding.AuditRecord) {
	return func(r appbinding.AuditRecord) {
		var runtime string
		a.withMu(func() {
			runtime = a.Apps[appID].Runtime
		})
		r.Runtime = runtime
		a.recordAudit(r)
	}
}

func (a *Actor) auditLifecycle(appID, action, reason string) {
	var runtime string
	a.withMu(func() {
		runtime = a.Apps[appID].Runtime
	})
	a.recordAudit(appbinding.AuditRecord{AppID: appID, Runtime: runtime, Callable: action, Allowed: true, Reason: reason})
}

// isExternalCaller reports whether the context indicates an unauthenticated
// external caller (e.g. frontend bridge via gateway in local/desktop mode).
// A nil context only occurs in unit tests and is treated as trusted internal.
// A non-nil context with zero identity is the real external-caller signal.
func isExternalCaller(ctx actor.PureContext) bool {
	if ctx == nil {
		return false
	}
	return ctx.Identity().IsZero()
}

// resolveSessionForInvoke validates a session token and, if valid, applies its
// bound AgentID/ProjectID to the caller identity. Callers must not hold a.mu.
func (a *Actor) resolveSessionForInvoke(token string, req gen.AppManagerInvokeReq, ci *callerIdentity) error {
	var session appSession
	var sessionErr error
	a.withMu(func() {
		session, sessionErr = a.resolveSession(token, time.Now())
	})
	if sessionErr != nil {
		return appbinding.Deny(appbinding.CodeSessionInvalid, sessionErr.Error())
	}
	if session.AppID != req.ID {
		return appbinding.Deny(appbinding.CodeSessionScopeMismatch, fmt.Sprintf("session for app %q, invoke targets %q", session.AppID, req.ID))
	}
	if session.AgentID != "" {
		ci.AgentID = session.AgentID
	}
	if session.ProjectID != "" {
		ci.ProjectID = session.ProjectID
	}
	return nil
}

// resolvedInvoke carries the state resolved by resolveInvokeAuth: the
// authorized callable descriptor, the caller identity, and the invocation
// target ref. It is shared by the unary handleInvoke and the streaming
// handleInvokeStream so both paths enforce the exact same authorization
// boundary.
type resolvedInvoke struct {
	manifest     gen.AppManifest
	record       appRecord
	actorID      string
	ci           callerIdentity
	callableDesc gen.AppCallableDescriptor
	ref          ref.Ref
}

// resolveInvokeAuth performs the authorization/resolve preamble for an
// appmanager invoke: running check, pending-cleanup check, caller identity
// resolution, session-token validation for external callers, route-depth /
// route-token HMAC checks, package-hash check, callable lookup, capability
// authorization, and target ref resolution (native → pluginhost service,
// spore → child actor). Every failure is audited here (Allowed=false) before
// the error is returned, so callers must not audit again. It returns the
// resolved state plus the possibly-normalized request (RouteToken is
// re-stamped with the trimmed token).
func (a *Actor) resolveInvokeAuth(ctx actor.PureContext, req gen.AppManagerInvokeReq) (*resolvedInvoke, error) {
	var actorID string
	var ok bool
	var manifest gen.AppManifest
	var record appRecord
	a.withMu(func() {
		actorID, ok = a.children[req.ID]
		manifest = a.Apps[req.ID]
		record = a.Records[req.ID]
	})
	if !ok {
		// Plugin self-heal (mirror of handleReload's wedge recovery): a
		// native app's routing target is the pluginhost service, and legacy
		// records persisted without ActorID never received a children
		// sentinel from the OnStart restore loop. The pluginhost may well be
		// serving the artifact already (it restores from its own
		// ArtifactLoads); wedge the record, not the caller — restore the
		// sentinel and proceed. Spore apps genuinely need a live child actor
		// and keep the hard error.
		if manifest.Runtime == "native" && record.State != "" && !isCleanupPendingState(record.State) {
			a.withMu(func() {
				a.children[req.ID] = pluginhostServiceName
			})
			actorID = pluginhostServiceName
		} else {
			err := fmt.Errorf("appmanager: app %q is not running", req.ID)
			a.auditInvoke(req, false, err.Error())
			return nil, err
		}
	}
	// Reject invoke for apps in a pending-cleanup state. The artifact/child
	// may already be torn down; routing an invoke would hit a dead handler.
	if isCleanupPendingState(record.State) {
		err := fmt.Errorf("appmanager: app %q is pending cleanup (state: %s)", req.ID, record.State)
		a.auditInvoke(req, false, err.Error())
		return nil, err
	}
	// Resolve caller identity from actor context (authoritative) rather
	// than trusting client-supplied AgentID/Role fields. WorkspaceID rides
	// the request field only for zero-Identity (internal) callers — the
	// turn engine stamps it, external clients never reach it.
	ci := resolveCallerWorkspace(ctx, req.AgentID, req.Role, req.ProjectID, req.WorkspaceID)
	// Session authorization: external callers — those with no verified
	// identity (RouteDepth==0 and zero context identity, i.e. frontend bridge
	// in local/desktop mode) — must present a valid session token as their
	// credential. Internal actor calls carry a non-zero identity set by the
	// runtime, which serves as their credential. RouteDepth>0 invokes carry
	// an HMAC route token. When a session token IS presented (by any caller)
	// it is validated and its bound identity overrides client-supplied fields.
	if req.RouteDepth == 0 && isExternalCaller(ctx) {
		token := strings.TrimSpace(req.SessionToken)
		if token == "" {
			err := appbinding.Deny(appbinding.CodeSessionRequired, "appmanager: session token is required for external invoke")
			a.auditInvokeWithIdentity(req, ci, false, err.Error())
			return nil, err
		}
		if err := a.resolveSessionForInvoke(token, req, &ci); err != nil {
			a.auditInvokeWithIdentity(req, ci, false, err.Error())
			return nil, err
		}
	} else if token := strings.TrimSpace(req.SessionToken); token != "" {
		if err := a.resolveSessionForInvoke(token, req, &ci); err != nil {
			a.auditInvokeWithIdentity(req, ci, false, err.Error())
			return nil, err
		}
	}
	if ci.AgentID == "" {
		err := appbinding.Deny(appbinding.CodeIdentityIncomplete, "appmanager: agent identity is required")
		a.auditInvokeWithIdentity(req, ci, false, err.Error())
		return nil, err
	}
	if req.RouteDepth < 0 || req.RouteDepth > maxRouteDepth {
		err := fmt.Errorf("appmanager: route depth %d exceeds maximum %d", req.RouteDepth, maxRouteDepth)
		a.auditInvokeWithIdentity(req, ci, false, err.Error())
		return nil, err
	}
	if req.RouteDepth > 0 {
		token := strings.TrimSpace(req.RouteToken)
		if token == "" {
			err := fmt.Errorf("appmanager: route token is required at depth %d", req.RouteDepth)
			a.auditInvokeWithIdentity(req, ci, false, err.Error())
			return nil, err
		}
		// Verify the route token's HMAC signature, then enforce audience
		// (TargetAppID == invoked app), callable match, and expiry. A
		// forged, tampered, expired, or mis-scoped token is a hard denial.
		rt, rtErr := parseRouteToken(token, a.sessionKey)
		if rtErr != nil {
			err := appbinding.Deny(appbinding.CodeRouteTokenInvalid, rtErr.Error())
			a.auditInvokeWithIdentity(req, ci, false, err.Error())
			return nil, err
		}
		if rt.TargetAppID != req.ID {
			err := appbinding.Deny(appbinding.CodeRouteTokenAudienceMismatch, fmt.Sprintf("route token targets app %q, invoke targets %q", rt.TargetAppID, req.ID))
			a.auditInvokeWithIdentity(req, ci, false, err.Error())
			return nil, err
		}
		if rt.Callable != req.Callable {
			err := appbinding.Deny(appbinding.CodeRouteTokenCallableMismatch, fmt.Sprintf("route token for callable %q, invoke targets %q", rt.Callable, req.Callable))
			a.auditInvokeWithIdentity(req, ci, false, err.Error())
			return nil, err
		}
		if sessionExpired(rt.ExpiresAt, time.Now()) {
			err := appbinding.Deny(appbinding.CodeRouteTokenExpired, "route token expired")
			a.auditInvokeWithIdentity(req, ci, false, err.Error())
			return nil, err
		}
		req.RouteToken = token
	}

	if req.ExpectedPackageHash != "" && !strings.EqualFold(req.ExpectedPackageHash, record.PackageHash) {
		err := fmt.Errorf("appmanager: package hash mismatch for %q", req.ID)
		a.auditInvokeWithIdentity(req, ci, false, err.Error())
		return nil, err
	}

	callableDesc, err := lookupCallable(manifest, req.Callable)
	if err != nil {
		a.auditInvokeWithIdentity(req, ci, false, err.Error())
		return nil, err
	}
	// Permission authorization: under the declaration-is-authorization model
	// the manifest Permissions are the granted set, and validateManifestSecurity
	// already guaranteed every callable permission ref is declared. Nothing to
	// re-check at invoke time.
	var ref ref.Ref
	if manifest.Runtime == "native" {
		pluginRef, found := ctx.LookupService(pluginhostServiceName)
		if !found || pluginRef == nil {
			err := fmt.Errorf("appmanager: pluginhost service not available for plugin %q", req.ID)
			a.auditInvokeWithIdentity(req, ci, false, err.Error())
			return nil, err
		}
		ref = pluginRef
	} else {
		canonical, err := identity.ParseCanonicalID(actorID)
		if err != nil {
			a.auditInvokeWithIdentity(req, ci, false, err.Error())
			return nil, err
		}
		childRef, ok := ctx.LookupID(id.From(canonical))
		if !ok || childRef == nil {
			err := fmt.Errorf("appmanager: child for %q not found", req.ID)
			a.auditInvokeWithIdentity(req, ci, false, err.Error())
			return nil, err
		}
		ref = childRef
	}
	return &resolvedInvoke{manifest: manifest, record: record, actorID: actorID, ci: ci, callableDesc: callableDesc, ref: ref}, nil
}

// handleInvoke dispatches a callable to a running app. It is declared with
// actor.PureContext (not actor.Context) and registered WITHOUT the
// appmanager_ops loop: an invoke can run for the callable's full declared
// timeout (up to 15min for LLM callables), and c-shared FFI calls cannot be
// interrupted once entered — parking that wait on the shared control-plane
// lane froze status/list/dev_* behind a single long invoke. All state access
// is a.mu-guarded snapshots, so a forked stateless goroutine is safe; this
// mirrors pluginhost's own handleInvoke shape.
func (a *Actor) handleInvoke(ctx actor.PureContext, req gen.AppManagerInvokeReq) (gen.AppManagerInvokeResp, error) {
	resolved, err := a.resolveInvokeAuth(ctx, req)
	if err != nil {
		return gen.AppManagerInvokeResp{}, err
	}
	manifest := resolved.manifest
	ci := resolved.ci
	callableDesc := resolved.callableDesc
	ref := resolved.ref
	// Dispatch through appbinding.Dispatcher so the actual call and audit
	// share one code path with stable codes. Authorization has already been
	// performed above (identity + callable.Permission against security policy).
	dispatcher := appbinding.Dispatcher{
		Audit: a.auditRuntime(req.ID),
		Call: func(callCtx context.Context, callable string, payload any) (any, error) {
			raw, _ := payload.([]byte)
			invokeCtx, cancel := context.WithTimeout(callCtx, invokeTimeout)
			defer cancel()
			if manifest.Runtime == "native" {
				invokeReq := req
				invokeReq.Callable = callable
				invokeReq.Payload = raw
				return a.invokeNative(ctx, ref, invokeReq, ci, callableDesc)
			}
			return sporeAppInvoke(invokeCtx, ref, req, callable, raw, ci)
		},
	}
	result, err := dispatcher.Invoke(ctx.Lifecycle(), appbinding.DispatchContext{AppID: req.ID, AgentID: ci.AgentID, Role: ci.Role, ProjectID: ci.ProjectID, RequestID: req.RequestID, SessionID: req.SessionID, CallSeq: req.CallSeq}, req.Callable, req.Payload)
	if err != nil {
		return gen.AppManagerInvokeResp{}, err
	}
	payload, _ := result.([]byte)
	return gen.AppManagerInvokeResp{Payload: payload}, nil
}

// sporeAppInvoke dispatches a callable to a spore child actor and unwraps the
// app payload from the SporeAppInvokeResp envelope. Shared by the unary and
// streaming (degrade-to-unary) paths so both use the identical wire logic.
func sporeAppInvoke(invokeCtx context.Context, ref ref.Ref, req gen.AppManagerInvokeReq, callable string, raw []byte, ci callerIdentity) (any, error) {
	stream := ref.Invoke(invokeCtx, "sporeapp.invoke", gen.SporeAppInvokeReq{ID: req.ID, Callable: callable, Payload: raw, AgentID: ci.AgentID, Role: ci.Role, ProjectID: ci.ProjectID, RequestID: req.RequestID, RouteDepth: req.RouteDepth, RouteToken: req.RouteToken})
	if stream == nil {
		return nil, fmt.Errorf("appmanager: invoke returned no stream")
	}
	defer stream.Close()
	result, err := stream.RecvRaw()
	if err != nil && errors.Is(invokeCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("appmanager: invoke timeout after %s: %w", invokeTimeout, err)
	}
	if err != nil {
		return nil, err
	}
	// The reply frame body is the encoded SporeAppInvokeResp — unwrap
	// it and return only the app payload, so callers receive the
	// callable's wire bytes (BinaryCodec or JSON), not the envelope.
	appPayload, err := decodeSporeAppInvokeResp(result)
	if err != nil {
		return nil, fmt.Errorf("appmanager: decode sporeapp response: %w", err)
	}
	return appPayload, nil
}

// decodeSporeAppInvokeResp unwraps a reply frame body into the app payload.
// Child actors may encode handler replies with the app codec (TBC struct
// framing) or plain JSON depending on their codec inheritance; both forms
// are accepted.
func decodeSporeAppInvokeResp(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return nil, nil
	}
	var resp gen.SporeAppInvokeResp
	if codec.IsTBCData(body) {
		view := transport.View{Kind: transport.ViewKindFull, Schema: sporeAppInvokeRespDesc, Data: body}
		if err := (&transport.BinaryCodec{}).DecodeInto(view, &resp); err != nil {
			return nil, err
		}
		return resp.Payload, nil
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return resp.Payload, nil
}

// handleCast broadcasts an app event to all current subscribers of the
// "app_event" event kind. It validates the app exists, the caller is a bound
// agent of the app, and the event is declared in the manifest, then emits via
// the App-scoped EventBus. The resolved caller identity (from context) is used
// as the event sender — never a client-supplied AgentID.
func (a *Actor) handleCast(ctx actor.Context, req gen.AppManagerCastReq) (gen.AppManagerCastResp, error) {
	var manifest gen.AppManifest
	var exists bool
	a.withMu(func() {
		manifest, exists = a.Apps[req.ID]
	})
	if !exists {
		return gen.AppManagerCastResp{}, fmt.Errorf("appmanager: app %q not found", req.ID)
	}
	// Resolve caller identity from actor context (authoritative), not from
	// client-supplied AgentID/Role fields.
	ci := resolveCaller(ctx, req.AgentID, req.Role, req.ProjectID)
	// Authorize cast: either the caller is bound to the app, or they present
	// a valid session for the app (the UI-host credential).
	authorized, authErr := a.authorizeCastEmit(req.ID, req.SessionToken, &ci)
	if authErr != nil {
		a.recordAudit(appbinding.AuditRecord{AppID: req.ID, Runtime: manifest.Runtime, AgentID: ci.AgentID, Role: ci.Role, ProjectID: ci.ProjectID, Callable: "cast:" + req.Event, Allowed: false, Reason: authErr.Error()})
		return gen.AppManagerCastResp{}, authErr
	}
	if !authorized {
		err := appbinding.Deny(appbinding.CodeBindingMissing, fmt.Sprintf("appmanager: caller is not bound to app %q and no valid session presented", req.ID))
		a.recordAudit(appbinding.AuditRecord{AppID: req.ID, Runtime: manifest.Runtime, AgentID: ci.AgentID, Role: ci.Role, ProjectID: ci.ProjectID, Callable: "cast:" + req.Event, Allowed: false, Reason: err.Error()})
		return gen.AppManagerCastResp{}, err
	}
	if !isDeclaredEvent(manifest, req.Event) {
		return gen.AppManagerCastResp{}, fmt.Errorf("appmanager: event %q not declared by app %q", req.Event, req.ID)
	}
	err := a.emitAppEvent(ctx, gen.AppEventMessage{ID: req.ID, Event: req.Event, Payload: req.Payload, Sender: ci.AgentID})
	if err != nil {
		return gen.AppManagerCastResp{}, err
	}
	return gen.AppManagerCastResp{Delivered: 1}, nil
}

// handleEmit accepts an app event for asynchronous delivery. Same validation
// and authorization as cast — the caller must be a bound agent of the target
// app; the distinction (fire-and-forget vs. broadcast) is reserved for future
// transport-level routing.
func (a *Actor) handleEmit(ctx actor.Context, req gen.AppManagerEmitReq) (gen.AppManagerEmitResp, error) {
	var manifest gen.AppManifest
	var exists bool
	a.withMu(func() {
		manifest, exists = a.Apps[req.ID]
	})
	if !exists {
		return gen.AppManagerEmitResp{}, fmt.Errorf("appmanager: app %q not found", req.ID)
	}
	// Resolve caller identity from actor context (authoritative), not from
	// client-supplied AgentID/Role fields.
	ci := resolveCaller(ctx, req.AgentID, req.Role, req.ProjectID)
	// Authorize emit: either the caller is bound to the app, or they present
	// a valid session for the app (the UI-host credential).
	authorized, authErr := a.authorizeCastEmit(req.ID, req.SessionToken, &ci)
	if authErr != nil {
		a.recordAudit(appbinding.AuditRecord{AppID: req.ID, Runtime: manifest.Runtime, AgentID: ci.AgentID, Role: ci.Role, ProjectID: ci.ProjectID, Callable: "emit:" + req.Event, Allowed: false, Reason: authErr.Error()})
		return gen.AppManagerEmitResp{}, authErr
	}
	if !authorized {
		err := appbinding.Deny(appbinding.CodeBindingMissing, fmt.Sprintf("appmanager: caller is not bound to app %q and no valid session presented", req.ID))
		a.recordAudit(appbinding.AuditRecord{AppID: req.ID, Runtime: manifest.Runtime, AgentID: ci.AgentID, Role: ci.Role, ProjectID: ci.ProjectID, Callable: "emit:" + req.Event, Allowed: false, Reason: err.Error()})
		return gen.AppManagerEmitResp{}, err
	}
	if !isDeclaredEvent(manifest, req.Event) {
		return gen.AppManagerEmitResp{}, fmt.Errorf("appmanager: event %q not declared by app %q", req.Event, req.ID)
	}
	err := a.emitAppEvent(ctx, gen.AppEventMessage{ID: req.ID, Event: req.Event, Payload: req.Payload, Sender: ci.AgentID})
	if err != nil {
		return gen.AppManagerEmitResp{Accepted: false}, err
	}
	return gen.AppManagerEmitResp{Accepted: true}, nil
}

// handlePluginEmit is the server-side entry for the SDK `app.emit` host call,
// invoked only by the pluginhost (AdminOnly). The pluginhost already injected
// the calling plugin's identity into PluginId, so unlike handleEmit there is no
// session/binding authorization — trust is the actor boundary; the remaining
// check is that the event is declared by the app's own manifest.
func (a *Actor) handlePluginEmit(ctx actor.Context, req gen.AppManagerPluginEmitReq) (gen.AppManagerPluginEmitResp, error) {
	pluginID := strings.TrimSpace(req.PluginID)
	if pluginID == "" {
		return gen.AppManagerPluginEmitResp{}, fmt.Errorf("appmanager: plugin emit: plugin id is required")
	}
	var manifest gen.AppManifest
	var exists bool
	a.withMu(func() {
		manifest, exists = a.Apps[pluginID]
	})
	if !exists {
		return gen.AppManagerPluginEmitResp{}, fmt.Errorf("appmanager: app %q not found", pluginID)
	}
	if !isDeclaredEvent(manifest, req.Event) {
		a.recordAudit(appbinding.AuditRecord{AppID: pluginID, Runtime: manifest.Runtime, Callable: "plugin_emit:" + req.Event, Allowed: false, Reason: "event not declared"})
		return gen.AppManagerPluginEmitResp{}, fmt.Errorf("appmanager: event %q not declared by app %q", req.Event, pluginID)
	}
	a.recordAudit(appbinding.AuditRecord{AppID: pluginID, Runtime: manifest.Runtime, Callable: "plugin_emit:" + req.Event, Allowed: true})
	err := a.emitAppEvent(ctx, gen.AppEventMessage{ID: pluginID, Event: req.Event, Payload: req.Payload, Sender: pluginID})
	if err != nil {
		return gen.AppManagerPluginEmitResp{}, err
	}
	return gen.AppManagerPluginEmitResp{Accepted: true}, nil
}

// authorizeCastEmit checks whether the caller may cast/emit events for appID.
// Authorization succeeds if either:
//  1. The resolved caller has a capability/surface binding to the app, or
//  2. The caller presents a valid session for the app (session is the UI-host credential).
//
// When session authorization is used, ci is updated with the session's bound
// identity so the event sender is stamped from the trusted credential.
func (a *Actor) authorizeCastEmit(appID, sessionToken string, ci *callerIdentity) (bool, error) {
	if ci.AgentID != "" && a.bindings != nil && a.bindings.HasBinding(appID, ci.AgentID) {
		return true, nil
	}
	token := strings.TrimSpace(sessionToken)
	if token == "" {
		return false, nil
	}
	var session appSession
	var sessionErr error
	a.withMu(func() {
		session, sessionErr = a.resolveSession(token, time.Now())
	})
	if sessionErr != nil {
		return false, appbinding.Deny(appbinding.CodeSessionInvalid, sessionErr.Error())
	}
	if session.AppID != appID {
		return false, appbinding.Deny(appbinding.CodeSessionScopeMismatch, fmt.Sprintf("session for app %q, cast/emit targets %q", session.AppID, appID))
	}
	if session.AgentID != "" {
		ci.AgentID = session.AgentID
	}
	if session.ProjectID != "" {
		ci.ProjectID = session.ProjectID
	}
	return true, nil
}

func isDeclaredEvent(manifest gen.AppManifest, eventID string) bool {
	for _, e := range manifest.Events {
		if e.ID == eventID {
			return true
		}
	}
	return false
}

// handleAudit returns audit records optionally filtered by app ID.
func (a *Actor) handleAudit(_ actor.PureContext, req gen.AppManagerAuditReq) (gen.AppManagerAuditResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	limit := 100
	if req.Limit > 0 && req.Limit < 1000 {
		limit = int(req.Limit)
	}
	var records []gen.AppAuditRecord
	for i := len(a.AuditRecords) - 1; i >= 0 && len(records) < limit; i-- {
		r := a.AuditRecords[i]
		if req.AppID != "" && r.AppID != req.AppID {
			continue
		}
		records = append(records, gen.AppAuditRecord{
			Time:      r.Time.Format("2006-01-02T15:04:05Z07:00"),
			RequestID: r.RequestID,
			AppID:     r.AppID,
			Runtime:   r.Runtime,
			AgentID:   r.AgentID,
			Role:      r.Role,
			ProjectID: r.ProjectID,
			Callable:  r.Callable,
			Allowed:   r.Allowed,
			Reason:    r.Reason,
			SessionID: r.SessionID,
			CallSeq:   r.CallSeq,
		})
	}
	if records == nil {
		records = []gen.AppAuditRecord{}
	}
	return gen.AppManagerAuditResp{Records: records}, nil
}
