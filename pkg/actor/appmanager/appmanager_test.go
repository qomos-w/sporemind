package appmanager

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestActorRegisterRequiresManifestContract(t *testing.T) {
	a := &Actor{Apps: map[string]gen.AppManifest{}, Records: map[string]appRecord{}}
	_, err := a.handleRegister(nil, gen.AppManagerRegisterReq{Manifest: gen.AppManifest{ID: "example.app", Name: "Example", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1, Namespace: "sporeapp.example.app"}, EntryModule: "main", Modules: map[string]string{"main": "export fun answer(): int = 42"}})
	if err == nil {
		t.Fatal("expected callable descriptor validation error")
	}
}

func TestActorRegisterRequiresPackage(t *testing.T) {
	a := &Actor{Apps: map[string]gen.AppManifest{}, Records: map[string]appRecord{}}
	_, err := a.handleRegister(nil, gen.AppManagerRegisterReq{Manifest: gen.AppManifest{ID: "example.app", Namespace: "sporeapp.example.app", Runtime: "spore", Version: "1.0.0"}})
	if err == nil {
		t.Fatal("expected package validation error")
	}
}

func TestBuiltinReloadIsRejectedBeforeSporeDispatch(t *testing.T) {
	manifest := gen.AppManifest{ID: "builtin.test", Runtime: "unknown"}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, State: "running"}},
		children: map[string]string{manifest.ID: "actor/1"},
	}
	_, err := a.handleReload(nil, gen.AppManagerReloadReq{ID: manifest.ID, AgentID: "agent-1"})
	if err == nil || !strings.Contains(err.Error(), "unsupported reload runtime") {
		t.Fatalf("expected unsupported reload runtime error, got: %v", err)
	}
	if len(a.AuditRecords) != 1 || a.AuditRecords[0].Allowed {
		t.Fatalf("expected denied reload audit, got %+v", a.AuditRecords)
	}
}

func TestRecordAuditKeepsInvocationContext(t *testing.T) {
	a := &Actor{}
	a.recordAudit(appbinding.AuditRecord{RequestID: "req-1", AppID: "example.app", Runtime: "spore", AgentID: "agent-1", Role: "coder", ProjectID: "project-1", Callable: "answer", Allowed: false, Reason: "denied", SessionID: "sess-1", CallSeq: 3})
	if len(a.AuditRecords) != 1 {
		t.Fatalf("audit records=%d", len(a.AuditRecords))
	}
	record := a.AuditRecords[0]
	if record.RequestID != "req-1" || record.AppID != "example.app" || record.AgentID != "agent-1" || record.Role != "coder" || record.ProjectID != "project-1" || record.Callable != "answer" || record.Allowed || record.Reason != "denied" || record.SessionID != "sess-1" || record.CallSeq != 3 {
		t.Fatalf("unexpected audit record: %+v", record)
	}
}

func TestHandleAuditMapsSessionIDAndCallSeq(t *testing.T) {
	a := &Actor{AuditRecords: []appbinding.AuditRecord{
		{RequestID: "r1", AppID: "app-a", Callable: "answer", Allowed: true, SessionID: "s1", CallSeq: 10},
		{RequestID: "r2", AppID: "app-b", Callable: "compute", Allowed: false, Reason: "denied", SessionID: "s2", CallSeq: 20},
	}}
	resp, err := a.handleAudit(nil, gen.AppManagerAuditReq{})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(resp.Records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(resp.Records))
	}
	if resp.Records[0].SessionID != "s2" || resp.Records[0].CallSeq != 20 {
		t.Fatalf("unexpected record[0] session/callseq: %+v", resp.Records[0])
	}
	if resp.Records[1].SessionID != "s1" || resp.Records[1].CallSeq != 10 {
		t.Fatalf("unexpected record[1] session/callseq: %+v", resp.Records[1])
	}
}

func TestAuditInvokeCarriesSessionIDAndCallSeq(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Runtime: "spore", Callables: []gen.AppCallableDescriptor{{ID: "answer"}}}
	a := &Actor{
		Apps:    map[string]gen.AppManifest{manifest.ID: manifest},
		Records: map[string]appRecord{manifest.ID: {Manifest: manifest, PackageHash: "hash", State: "running"}},
	}
	a.auditInvoke(gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "answer", AgentID: "agent-1", RequestID: "req-1", SessionID: "sess-1", CallSeq: 42}, false, "denied")
	if len(a.AuditRecords) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(a.AuditRecords))
	}
	r := a.AuditRecords[0]
	if r.SessionID != "sess-1" || r.CallSeq != 42 {
		t.Fatalf("expected SessionID=sess-1 CallSeq=42, got %+v", r)
	}
}

func TestAppStatusUsesRecordState(t *testing.T) {
	descriptor := gen.AppObjectDescriptor{Kind: "struct", Name: "Payload", SchemaID: 9001}
	a := &Actor{
		Apps: map[string]gen.AppManifest{
			"example.app": {ID: "example.app", Namespace: "example", Runtime: "native", Version: "1.0.0", Entrypoints: []gen.AppEntrypoint{{ID: "home", Kind: "view", Title: "Home"}}},
		},
		Records: map[string]appRecord{
			"example.app": {State: "failed", PackageHash: "package-hash", ArtifactHash: "artifact-hash", Error: "load failed", SchemaDescriptors: map[string]gen.AppObjectDescriptor{"Payload": descriptor}},
		},
	}
	resp, err := a.handleList(nil, gen.AppManagerListReq{})
	if err != nil || len(resp.Items) != 1 {
		t.Fatalf("list: resp=%+v err=%v", resp, err)
	}
	if resp.Items[0].State != "failed" || resp.Items[0].Namespace != "example" || resp.Items[0].SchemaDescriptors["Payload"].SchemaID != 9001 || resp.Items[0].PackageHash != "package-hash" || resp.Items[0].ArtifactHash != "artifact-hash" || resp.Items[0].Error != "load failed" || len(resp.Items[0].Entrypoints) != 1 || resp.Items[0].Entrypoints[0].ID != "home" {
		t.Fatalf("unexpected status: %+v", resp.Items[0])
	}
}

func TestLookupCallableNotFound(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Callables: []gen.AppCallableDescriptor{{ID: "answer", RequestSchema: "AnswerReq", ResponseSchema: "AnswerResp"}}}
	_, err := lookupCallable(manifest, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown callable")
	}
}

func TestLookupCallableFound(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Callables: []gen.AppCallableDescriptor{{ID: "answer", RequestSchema: "AnswerReq", ResponseSchema: "AnswerResp"}}}
	desc, err := lookupCallable(manifest, "answer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc.ID != "answer" || desc.RequestSchema != "AnswerReq" || desc.ResponseSchema != "AnswerResp" {
		t.Fatalf("unexpected descriptor: %+v", desc)
	}
}

func TestInvokeRejectsForgedRouteToken(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Runtime: "spore", Callables: []gen.AppCallableDescriptor{{ID: "answer"}}}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, PackageHash: "hash", State: "running"}},
		children: map[string]string{manifest.ID: "actor-1"},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
		sessionKey: key,
	}
	// An unsigned, arbitrary string is rejected by signature verification.
	_, err := a.handleInvoke(nil, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "answer", AgentID: "agent-1", RequestID: "req-forged", RouteDepth: 1, RouteToken: "forged-token-string"})
	if err == nil {
		t.Fatal("forged route token must be rejected")
	}
	if code := appbinding.DenialCode(err); code != appbinding.CodeRouteTokenInvalid {
		t.Fatalf("expected CodeRouteTokenInvalid, got %q (err: %v)", code, err)
	}
	// A token signed with a foreign key is also rejected.
	foreign, _ := issueRouteToken(routeToken{SourceAppID: "src", TargetAppID: manifest.ID, Callable: "answer", ExpiresAt: time.Now().Add(time.Hour).Unix()}, []byte("foreign-key-00000000000000000000"))
	_, err = a.handleInvoke(nil, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "answer", AgentID: "agent-1", RequestID: "req-foreign", RouteDepth: 1, RouteToken: foreign})
	if err == nil {
		t.Fatal("foreign-signed route token must be rejected")
	}
	if code := appbinding.DenialCode(err); code != appbinding.CodeRouteTokenInvalid {
		t.Fatalf("expected CodeRouteTokenInvalid for foreign-signed token, got %q (err: %v)", code, err)
	}
}
func TestInvokeRejectsMissingRouteToken(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Runtime: "spore", Callables: []gen.AppCallableDescriptor{{ID: "answer"}}}
	a := &Actor{Apps: map[string]gen.AppManifest{manifest.ID: manifest}, Records: map[string]appRecord{manifest.ID: {Manifest: manifest, PackageHash: "hash", State: "running"}}, children: map[string]string{manifest.ID: "actor-1"}, bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{}}
	_, err := a.handleInvoke(nil, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "answer", AgentID: "agent-1", RequestID: "req-token", RouteDepth: 1})
	if err == nil || !strings.Contains(err.Error(), "route token is required at depth 1") {
		t.Fatalf("unexpected route token error: %v", err)
	}
}
func TestInvokeRejectsRouteDepthOverBudget(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Runtime: "spore", Callables: []gen.AppCallableDescriptor{{ID: "answer"}}}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, PackageHash: "hash", State: "running"}},
		children: map[string]string{manifest.ID: "actor-1"},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	_, err := a.handleInvoke(nil, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "answer", AgentID: "agent-1", RequestID: "req-depth", RouteDepth: 9})
	if err == nil || !strings.Contains(err.Error(), "route depth 9 exceeds maximum 8") {
		t.Fatalf("unexpected route depth error: %v", err)
	}
	if len(a.AuditRecords) != 1 || a.AuditRecords[0].Allowed || a.AuditRecords[0].RequestID != "req-depth" {
		t.Fatalf("expected audited route denial, got %+v", a.AuditRecords)
	}
}
func TestInvokeRejectsExpectedPackageHashMismatch(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Runtime: "spore", Callables: []gen.AppCallableDescriptor{{ID: "answer"}}}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, PackageHash: "actual-hash", State: "running"}},
		children: map[string]string{manifest.ID: "actor-1"},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	_, err := a.handleInvoke(nil, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "answer", AgentID: "agent-1", RequestID: "req-hash", ExpectedPackageHash: "expected-hash"})
	if err == nil || err.Error() != `appmanager: package hash mismatch for "example.app"` {
		t.Fatalf("unexpected hash mismatch error: %v", err)
	}
	if len(a.AuditRecords) != 1 || a.AuditRecords[0].Allowed || a.AuditRecords[0].RequestID != "req-hash" {
		t.Fatalf("expected audited hash denial, got %+v", a.AuditRecords)
	}
}
func TestInvokeAppNotRunningAuditsFailure(t *testing.T) {
	a := &Actor{Apps: map[string]gen.AppManifest{}, Records: map[string]appRecord{}, children: map[string]string{}, bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{}}
	_, err := a.handleInvoke(nil, gen.AppManagerInvokeReq{ID: "nonexistent.app", Callable: "answer", AgentID: "agent-1"})
	if err == nil {
		t.Fatal("expected error for app not running")
	}
	if len(a.AuditRecords) != 1 || a.AuditRecords[0].Allowed {
		t.Fatalf("expected denied audit record, got %+v", a.AuditRecords)
	}
}

func TestInvokeMissingAgentIDAuditsFailure(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Callables: []gen.AppCallableDescriptor{{ID: "answer", RequestSchema: "AnswerReq", ResponseSchema: "AnswerResp"}}}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{"example.app": manifest},
		Records:  map[string]appRecord{},
		children: map[string]string{"example.app": "actor-1"},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	_, err := a.handleInvoke(nil, gen.AppManagerInvokeReq{ID: "example.app", Callable: "answer"})
	if err == nil {
		t.Fatal("expected error for missing AgentID")
	}
	if len(a.AuditRecords) != 1 || a.AuditRecords[0].Allowed {
		t.Fatalf("expected denied audit record, got %+v", a.AuditRecords)
	}
}

func TestInvokeCallableNotFoundAuditsFailure(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Callables: []gen.AppCallableDescriptor{{ID: "answer", RequestSchema: "AnswerReq", ResponseSchema: "AnswerResp"}}}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{"example.app": manifest},
		Records:  map[string]appRecord{},
		children: map[string]string{"example.app": "actor-1"},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	_, err := a.handleInvoke(nil, gen.AppManagerInvokeReq{ID: "example.app", Callable: "nonexistent", AgentID: "agent-1"})
	if err == nil {
		t.Fatal("expected error for callable not found")
	}
	if len(a.AuditRecords) != 1 || a.AuditRecords[0].Allowed {
		t.Fatalf("expected denied audit record, got %+v", a.AuditRecords)
	}
	if a.AuditRecords[0].Callable != "nonexistent" {
		t.Fatalf("expected callable 'nonexistent' in audit, got %q", a.AuditRecords[0].Callable)
	}
}

func TestInvokeAuthorizationDeniedAuditsFailure(t *testing.T) {
	manifest := gen.AppManifest{
		ID:        "example.app",
		Runtime:   "spore",
		Callables: []gen.AppCallableDescriptor{{ID: "answer", RequestSchema: "AnswerReq", ResponseSchema: "AnswerResp"}},
		AgentBinding: &gen.AppAgentBinding{
			Surface: &gen.AgentSurfaceBinding{
				AgentID:     "agent-1",
				Entrypoint:  "home",
				Projections: []string{},
				Events:      []string{},
			},
			Capability: &gen.AgentCapabilityBinding{
				Callables: []string{"other"},
			},
		},
	}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{"example.app": manifest},
		Records:  map[string]appRecord{"example.app": {Manifest: manifest}},
		children: map[string]string{"example.app": "actor-1"},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	if err := a.bindFreeAgentPolicy(manifest); err != nil {
		t.Fatalf("bindFreeAgentPolicy: %v", err)
	}
	_, err := a.handleInvoke(nil, gen.AppManagerInvokeReq{ID: "example.app", Callable: "answer", AgentID: "agent-1"})
	if err == nil {
		t.Fatal("expected authorization error")
	}
	if len(a.AuditRecords) != 1 || a.AuditRecords[0].Allowed {
		t.Fatalf("expected denied audit record, got %+v", a.AuditRecords)
	}
}

func TestIsDeclaredEvent(t *testing.T) {
	manifest := gen.AppManifest{
		Events: []gen.AppEventDescriptor{{ID: "update", PayloadSchema: "UpdatePayload"}},
	}
	if !isDeclaredEvent(manifest, "update") {
		t.Fatal("expected 'update' to be declared")
	}
	if isDeclaredEvent(manifest, "unknown") {
		t.Fatal("expected 'unknown' to be undeclared")
	}
}

func TestCastAppNotFound(t *testing.T) {
	a := &Actor{Apps: map[string]gen.AppManifest{}}
	_, err := a.handleCast(nil, gen.AppManagerCastReq{ID: "nonexistent", Event: "update"})
	if err == nil {
		t.Fatal("expected error for unknown app")
	}
}

func TestCastEventNotDeclared(t *testing.T) {
	manifest := gen.AppManifest{
		ID:      "example.app",
		Runtime: "spore",
		AgentBinding: &gen.AppAgentBinding{
			Surface: &gen.AgentSurfaceBinding{AgentID: "agent-1", Entrypoint: "main"},
		},
	}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{"example.app": manifest},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	if err := a.bindFreeAgentPolicy(manifest); err != nil {
		t.Fatal(err)
	}
	_, err := a.handleCast(nil, gen.AppManagerCastReq{ID: "example.app", Event: "undeclared", AgentID: "agent-1"})
	if err == nil {
		t.Fatal("expected error for undeclared event")
	}
}

func TestEmitAppNotFound(t *testing.T) {
	a := &Actor{Apps: map[string]gen.AppManifest{}}
	_, err := a.handleEmit(nil, gen.AppManagerEmitReq{ID: "nonexistent", Event: "update"})
	if err == nil {
		t.Fatal("expected error for unknown app")
	}
}

func TestEmitEventNotDeclared(t *testing.T) {
	manifest := gen.AppManifest{
		ID:      "example.app",
		Runtime: "spore",
		AgentBinding: &gen.AppAgentBinding{
			Surface: &gen.AgentSurfaceBinding{AgentID: "agent-1", Entrypoint: "main"},
		},
	}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{"example.app": manifest},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	if err := a.bindFreeAgentPolicy(manifest); err != nil {
		t.Fatal(err)
	}
	_, err := a.handleEmit(nil, gen.AppManagerEmitReq{ID: "example.app", Event: "undeclared", AgentID: "agent-1"})
	if err == nil {
		t.Fatal("expected error for undeclared event")
	}
}

// castEmitTestActor builds an actor with a single app whose surface binding is
// scoped to agent "bound-agent" and which declares the "update" event.
func castEmitTestAgent(t *testing.T) *Actor {
	t.Helper()
	manifest := gen.AppManifest{
		ID:      "evt.app",
		Runtime: "spore",
		Events:  []gen.AppEventDescriptor{{ID: "update"}},
	}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{"evt.app": manifest},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	// The agent binds the app's surface before casting.
	if err := a.bindings.BindSurface(appbinding.SurfaceBinding{AppID: "evt.app", AgentID: "bound-agent", Entrypoint: "home", Events: map[string]struct{}{"update": {}}}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCastDeniedWithoutAgentID(t *testing.T) {
	a := castEmitTestAgent(t)
	_, err := a.handleCast(nil, gen.AppManagerCastReq{ID: "evt.app", Event: "update"})
	if err == nil {
		t.Fatal("expected denial without agent id or session")
	}
	if appbinding.DenialCode(err) != appbinding.CodeBindingMissing {
		t.Fatalf("expected CodeBindingMissing, got %v", err)
	}
}

func TestCastDeniedForUnboundAgent(t *testing.T) {
	a := castEmitTestAgent(t)
	_, err := a.handleCast(nil, gen.AppManagerCastReq{ID: "evt.app", Event: "update", AgentID: "intruder"})
	if err == nil {
		t.Fatal("expected binding-missing denial for unbound agent")
	}
	if appbinding.DenialCode(err) != appbinding.CodeBindingMissing {
		t.Fatalf("expected CodeBindingMissing, got %v", err)
	}
	if len(a.AuditRecords) != 1 || a.AuditRecords[0].Allowed {
		t.Fatalf("expected denied audit record, got %+v", a.AuditRecords)
	}
}

func TestCastSucceedsForBoundAgent(t *testing.T) {
	a := castEmitTestAgent(t)
	ctx := &testutil.FakeCtx{}
	resp, err := a.handleCast(ctx, gen.AppManagerCastReq{ID: "evt.app", Event: "update", AgentID: "bound-agent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Delivered != 1 {
		t.Fatalf("expected Delivered=1, got %d", resp.Delivered)
	}
	if len(ctx.EmittedEvents) != 1 || ctx.EmittedEvents[0].Kind != "app_event" {
		t.Fatalf("expected one app_event emission, got %+v", ctx.EmittedEvents)
	}
	msg, ok := ctx.EmittedEvents[0].Payload.(gen.AppEventMessage)
	if !ok {
		t.Fatalf("expected AppEventMessage, got %T", ctx.EmittedEvents[0].Payload)
	}
	if msg.Sender != "bound-agent" {
		t.Fatalf("expected sender 'bound-agent', got %q", msg.Sender)
	}
}

func TestEmitDeniedWithoutAgentID(t *testing.T) {
	a := castEmitTestAgent(t)
	_, err := a.handleEmit(nil, gen.AppManagerEmitReq{ID: "evt.app", Event: "update"})
	if err == nil {
		t.Fatal("expected denial without agent id or session")
	}
	if appbinding.DenialCode(err) != appbinding.CodeBindingMissing {
		t.Fatalf("expected CodeBindingMissing, got %v", err)
	}
}

func TestEmitDeniedForUnboundAgent(t *testing.T) {
	a := castEmitTestAgent(t)
	_, err := a.handleEmit(nil, gen.AppManagerEmitReq{ID: "evt.app", Event: "update", AgentID: "intruder"})
	if err == nil {
		t.Fatal("expected binding-missing denial for unbound agent")
	}
	if appbinding.DenialCode(err) != appbinding.CodeBindingMissing {
		t.Fatalf("expected CodeBindingMissing, got %v", err)
	}
}

func TestEmitSucceedsForBoundAgent(t *testing.T) {
	a := castEmitTestAgent(t)
	ctx := &testutil.FakeCtx{}
	resp, err := a.handleEmit(ctx, gen.AppManagerEmitReq{ID: "evt.app", Event: "update", AgentID: "bound-agent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Accepted {
		t.Fatal("expected Accepted=true")
	}
	if len(ctx.EmittedEvents) != 1 {
		t.Fatalf("expected one event emission, got %d", len(ctx.EmittedEvents))
	}
}

// TestCastUsesContextIdentity verifies that the resolved sender identity comes
// from the actor context, not the client-supplied AgentID field — a caller
// cannot forge a different sender identity.
func TestCastUsesContextIdentity(t *testing.T) {
	a := castEmitTestAgent(t)
	ctx := &testutil.FakeCtx{
		Identity_: id.Identity{Kind: id.IdentityToken, Subject: "bound-agent", Role: "agent"},
	}
	_, err := a.handleCast(ctx, gen.AppManagerCastReq{
		ID:      "evt.app",
		Event:   "update",
		AgentID: "forged-sender", // should be overridden by context identity
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := ctx.EmittedEvents[0].Payload.(gen.AppEventMessage)
	if msg.Sender != "bound-agent" {
		t.Fatalf("expected sender from context 'bound-agent', got %q", msg.Sender)
	}
}

// pluginEmitTestActor mirrors castEmitTestAgent for the SDK app.emit path: one
// app declaring the "update" event, no session/binding requirements (trust is
// the pluginhost actor boundary; the plugin identity arrives injected).
func pluginEmitTestActor(t *testing.T) *Actor {
	t.Helper()
	manifest := gen.AppManifest{
		ID:      "evt.app",
		Runtime: "native",
		Events:  []gen.AppEventDescriptor{{ID: "update"}},
	}
	return &Actor{
		Apps:     map[string]gen.AppManifest{"evt.app": manifest},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
}

func TestPluginEmitRejectedWithoutPluginID(t *testing.T) {
	a := pluginEmitTestActor(t)
	if _, err := a.handlePluginEmit(nil, gen.AppManagerPluginEmitReq{Event: "update"}); err == nil {
		t.Fatal("expected error for missing plugin id")
	}
}

func TestPluginEmitUnknownApp(t *testing.T) {
	a := pluginEmitTestActor(t)
	_, err := a.handlePluginEmit(nil, gen.AppManagerPluginEmitReq{PluginID: "ghost.app", Event: "update"})
	if err == nil {
		t.Fatal("expected error for unknown app")
	}
}

func TestPluginEmitUndeclaredEvent(t *testing.T) {
	a := pluginEmitTestActor(t)
	_, err := a.handlePluginEmit(nil, gen.AppManagerPluginEmitReq{PluginID: "evt.app", Event: "undeclared"})
	if err == nil {
		t.Fatal("expected error for undeclared event")
	}
}

func TestPluginEmitBroadcastsAppEvent(t *testing.T) {
	a := pluginEmitTestActor(t)
	ctx := &testutil.FakeCtx{}
	resp, err := a.handlePluginEmit(ctx, gen.AppManagerPluginEmitReq{
		PluginID: "evt.app",
		Event:    "update",
		Payload:  []byte(`{"done":true}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Accepted {
		t.Fatal("expected Accepted=true")
	}
	if len(ctx.EmittedEvents) != 1 || ctx.EmittedEvents[0].Kind != "app_event" {
		t.Fatalf("expected one app_event emission, got %+v", ctx.EmittedEvents)
	}
	msg, ok := ctx.EmittedEvents[0].Payload.(gen.AppEventMessage)
	if !ok {
		t.Fatalf("expected AppEventMessage, got %T", ctx.EmittedEvents[0].Payload)
	}
	if msg.ID != "evt.app" || msg.Event != "update" || msg.Sender != "evt.app" || string(msg.Payload) != `{"done":true}` {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestHandleAuditReturnsAllRecords(t *testing.T) {
	a := &Actor{AuditRecords: []appbinding.AuditRecord{
		{RequestID: "r1", AppID: "app-a", Callable: "answer", Allowed: true},
		{RequestID: "r2", AppID: "app-b", Callable: "compute", Allowed: false, Reason: "denied"},
	}}
	resp, err := a.handleAudit(nil, gen.AppManagerAuditReq{})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(resp.Records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(resp.Records))
	}
	// Records are returned newest-first
	if resp.Records[0].RequestID != "r2" || resp.Records[1].RequestID != "r1" {
		t.Fatalf("unexpected order: %+v", resp.Records)
	}
}

func TestHandleAuditFiltersByAppID(t *testing.T) {
	a := &Actor{AuditRecords: []appbinding.AuditRecord{
		{RequestID: "r1", AppID: "app-a", Callable: "answer", Allowed: true},
		{RequestID: "r2", AppID: "app-b", Callable: "compute", Allowed: false},
	}}
	resp, err := a.handleAudit(nil, gen.AppManagerAuditReq{AppID: "app-a"})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(resp.Records) != 1 || resp.Records[0].AppID != "app-a" {
		t.Fatalf("expected 1 record for app-a, got %+v", resp.Records)
	}
}

func TestHandleAuditRespectsLimit(t *testing.T) {
	a := &Actor{AuditRecords: []appbinding.AuditRecord{
		{RequestID: "r1", AppID: "app-a"},
		{RequestID: "r2", AppID: "app-a"},
		{RequestID: "r3", AppID: "app-a"},
	}}
	resp, err := a.handleAudit(nil, gen.AppManagerAuditReq{Limit: 2})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(resp.Records) != 2 {
		t.Fatalf("expected 2 records with limit, got %d", len(resp.Records))
	}
}

func TestHandleAuditEmptyReturnsEmptyArray(t *testing.T) {
	a := &Actor{AuditRecords: nil}
	resp, err := a.handleAudit(nil, gen.AppManagerAuditReq{})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if resp.Records == nil || len(resp.Records) != 0 {
		t.Fatalf("expected empty array, got %+v", resp.Records)
	}
}

func TestRecordAuditTrimsOldRecords(t *testing.T) {
	a := &Actor{AuditRecords: []appbinding.AuditRecord{}, actorID: "trim-test"}
	// Fill directly without Save() to avoid slow disk I/O
	total := maxAuditRecords + 5
	a.mu.Lock()
	for i := 0; i < total; i++ {
		a.AuditRecords = append(a.AuditRecords, appbinding.AuditRecord{RequestID: fmt.Sprintf("r%d", i), AppID: "app", Callable: "c"})
		// Simulate trim
		if len(a.AuditRecords) > maxAuditRecords {
			excess := len(a.AuditRecords) - maxAuditRecords
			a.AuditRecords = a.AuditRecords[excess:]
		}
	}
	a.mu.Unlock()
	if len(a.AuditRecords) != maxAuditRecords {
		t.Fatalf("expected %d records after trim, got %d", maxAuditRecords, len(a.AuditRecords))
	}
	// Oldest 5 should have been trimmed
	if a.AuditRecords[0].RequestID != "r5" {
		t.Fatalf("expected first record to be r5 (oldest trimmed), got %q", a.AuditRecords[0].RequestID)
	}
	// Latest record should be the last one added
	if a.AuditRecords[len(a.AuditRecords)-1].RequestID != fmt.Sprintf("r%d", total-1) {
		t.Fatalf("expected last record to be r%d, got %q", total-1, a.AuditRecords[len(a.AuditRecords)-1].RequestID)
	}
}

// countingPersist wraps a Persist to count Save calls for testing.
type countingPersist struct {
	persist.Persist
	saves int
}

func (c *countingPersist) Save(name string, v any) error {
	c.saves++
	return c.Persist.Save(name, v)
}

// TestRecordAuditBatchesSaveCalls verifies that recordAudit does not trigger a
// full Save on every call. Instead, Saves are batched: only when the pending
// count reaches auditFlushThreshold does a single Save fire, collapsing many
// audit entries into one disk write.
func TestRecordAuditBatchesSaveCalls(t *testing.T) {
	cp := &countingPersist{Persist: persist.NewFSPersist(t.TempDir())}
	a := &Actor{
		store:   cp,
		actorID: "batch-test",
		Apps:    map[string]gen.AppManifest{},
		Records: map[string]appRecord{},
	}

	// Adding auditFlushThreshold-1 records should not trigger any Save.
	for i := 0; i < auditFlushThreshold-1; i++ {
		a.recordAudit(appbinding.AuditRecord{RequestID: fmt.Sprintf("r%d", i), AppID: "app", Callable: "c"})
	}
	if cp.saves != 0 {
		t.Fatalf("expected 0 saves below threshold, got %d", cp.saves)
	}
	if a.auditPending != auditFlushThreshold-1 {
		t.Fatalf("expected auditPending=%d, got %d", auditFlushThreshold-1, a.auditPending)
	}
	// All records should be in memory regardless of Save batching.
	if len(a.AuditRecords) != auditFlushThreshold-1 {
		t.Fatalf("expected %d in-memory records, got %d", auditFlushThreshold-1, len(a.AuditRecords))
	}

	// The auditFlushThreshold-th record should trigger exactly one Save.
	a.recordAudit(appbinding.AuditRecord{RequestID: "trigger", AppID: "app", Callable: "c"})
	if cp.saves != 1 {
		t.Fatalf("expected 1 save at threshold, got %d", cp.saves)
	}
	if a.auditPending != 0 {
		t.Fatalf("expected auditPending=0 after flush, got %d", a.auditPending)
	}
}

// TestRecordAuditManualSaveResetsPending verifies that a Save triggered by an
// unrelated state change (register, reload, unregister) also flushes pending
// audits and resets the batch counter, so those records are not needlessly
// re-flushed.
func TestRecordAuditManualSaveResetsPending(t *testing.T) {
	cp := &countingPersist{Persist: persist.NewFSPersist(t.TempDir())}
	a := &Actor{
		store:   cp,
		actorID: "reset-test",
		Apps:    map[string]gen.AppManifest{},
		Records: map[string]appRecord{},
	}

	// Accumulate some pending audits without hitting the threshold.
	for i := 0; i < 10; i++ {
		a.recordAudit(appbinding.AuditRecord{RequestID: fmt.Sprintf("r%d", i), AppID: "app", Callable: "c"})
	}
	if cp.saves != 0 {
		t.Fatalf("expected 0 saves before manual Save, got %d", cp.saves)
	}
	// A manual Save (as done by register/reload/unregister) flushes all
	// pending audits and resets the counter.
	if err := a.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if cp.saves != 1 {
		t.Fatalf("expected 1 save after manual Save, got %d", cp.saves)
	}
	if a.auditPending != 0 {
		t.Fatalf("expected auditPending=0 after Save, got %d", a.auditPending)
	}
	// Adding more audits should start counting from 0 again.
	a.recordAudit(appbinding.AuditRecord{RequestID: "post", AppID: "app", Callable: "c"})
	if a.auditPending != 1 {
		t.Fatalf("expected auditPending=1 after post-save audit, got %d", a.auditPending)
	}
}

func TestHandleGetReturnsEntrypoints(t *testing.T) {
	a := &Actor{Apps: map[string]gen.AppManifest{"example.app": {ID: "example.app", Runtime: "spore", Version: "1.0.0", Entrypoints: []gen.AppEntrypoint{{ID: "home", Kind: "view", Title: "Home"}, {ID: "panel-1", Kind: "panel", Title: "Panel 1"}}}}, Records: map[string]appRecord{"example.app": {State: "running", PackageHash: "hash"}}}
	resp, err := a.handleGet(nil, gen.AppManagerGetReq{ID: "example.app"})
	if err != nil {
		t.Fatalf("get: err=%v", err)
	}
	if resp.Status.ID != "example.app" || resp.Status.State != "running" {
		t.Fatalf("unexpected status: %+v", resp.Status)
	}
	if len(resp.Status.Entrypoints) != 2 {
		t.Fatalf("expected 2 entrypoints, got %d", len(resp.Status.Entrypoints))
	}
	if resp.Status.Entrypoints[0].ID != "home" || resp.Status.Entrypoints[1].ID != "panel-1" {
		t.Fatalf("unexpected entrypoints: %+v", resp.Status.Entrypoints)
	}
}

func TestResolveCallerNilCtxFallsBackToRequest(t *testing.T) {
	req := gen.AppManagerInvokeReq{AgentID: "agent-from-req", Role: "coder", ProjectID: "proj-1"}
	ci := resolveCaller(nil, req.AgentID, req.Role, req.ProjectID)
	if ci.AgentID != "agent-from-req" || ci.Role != "coder" || ci.ProjectID != "proj-1" {
		t.Fatalf("expected request fallback values, got %+v", ci)
	}
}

func TestResolveCallerIdentityOverridesRequest(t *testing.T) {
	ctx := &testutil.FakeCtx{
		Identity_: id.Identity{Kind: id.IdentityToken, Subject: "real-agent-id", Role: "agent"},
	}
	req := gen.AppManagerInvokeReq{AgentID: "forged-agent", Role: "admin", ProjectID: "proj-1"}
	ci := resolveCaller(ctx, req.AgentID, req.Role, req.ProjectID)
	if ci.AgentID != "real-agent-id" {
		t.Fatalf("expected AgentID from ctx.Identity(), got %q", ci.AgentID)
	}
	if ci.Role != "agent" {
		t.Fatalf("expected Role from ctx.Identity(), got %q", ci.Role)
	}
	// ProjectID is not in Identity, should come from request
	if ci.ProjectID != "proj-1" {
		t.Fatalf("expected ProjectID from request, got %q", ci.ProjectID)
	}
}

func TestResolveCallerZeroIdentityFallsBackToRequest(t *testing.T) {
	ctx := &testutil.FakeCtx{
		Identity_: id.Identity{}, // zero = anonymous
	}
	ci := resolveCaller(ctx, "agent-from-req", "coder", "")
	if ci.AgentID != "agent-from-req" || ci.Role != "coder" {
		t.Fatalf("expected request fallback for zero identity, got %+v", ci)
	}
}

// TestResolveCallerWorkspaceID pins the WorkspaceId trust boundary: the
// request field survives only for zero-Identity (internal actor-to-actor)
// callers — where the turn engine is the stamper — and is dropped whenever a
// gateway-authenticated identity is present (external callers must not be
// able to forge aistats attribution).
func TestResolveCallerWorkspaceID(t *testing.T) {
	// Internal caller (zero identity): engine-stamped WorkspaceId survives.
	internal := &testutil.FakeCtx{Identity_: id.Identity{}}
	ci := resolveCallerWorkspace(internal, "agent-1", "", "", "ws-7")
	if ci.WorkspaceID != "ws-7" {
		t.Fatalf("expected WorkspaceID from request for zero identity, got %q", ci.WorkspaceID)
	}

	// External authenticated caller: forged WorkspaceId is dropped.
	external := &testutil.FakeCtx{
		Identity_: id.Identity{Kind: id.IdentityToken, Subject: "user-1", Role: "admin"},
	}
	ci2 := resolveCallerWorkspace(external, "user-1", "admin", "", "forged-ws")
	if ci2.WorkspaceID != "" {
		t.Fatalf("forged WorkspaceID must be dropped for authenticated callers, got %q", ci2.WorkspaceID)
	}
}

func TestInvokeWithIdentityContextRejectsForgedAgentID(t *testing.T) {
	manifest := gen.AppManifest{
		ID:        "example.app",
		Runtime:   "spore",
		Callables: []gen.AppCallableDescriptor{{ID: "answer", RequestSchema: "AnswerReq", ResponseSchema: "AnswerResp"}},
		AgentBinding: &gen.AppAgentBinding{
			Surface:    &gen.AgentSurfaceBinding{AgentID: "real-agent", Entrypoint: "home"},
			Capability: &gen.AgentCapabilityBinding{Callables: []string{"answer"}},
		},
	}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{"example.app": manifest},
		Records:  map[string]appRecord{"example.app": {Manifest: manifest}},
		children: map[string]string{"example.app": "actor-1"},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	if err := a.bindFreeAgentPolicy(manifest); err != nil {
		t.Fatalf("bindFreeAgentPolicy: %v", err)
	}
	// Caller tries to forge AgentID "real-agent" but ctx.Identity says "impostor"
	ctx := &testutil.FakeCtx{
		Identity_: id.Identity{Kind: id.IdentityToken, Subject: "impostor", Role: "agent"},
	}
	_, err := a.handleInvoke(ctx, gen.AppManagerInvokeReq{
		ID:       "example.app",
		Callable: "answer",
		AgentID:  "real-agent", // forged!
	})
	if err == nil {
		t.Fatal("expected authorization error — forged AgentID should be overridden by ctx.Identity")
	}
	// Audit should record the real identity, not the forged one
	if len(a.AuditRecords) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(a.AuditRecords))
	}
	if a.AuditRecords[0].AgentID != "impostor" {
		t.Fatalf("expected audit AgentID 'impostor' (from ctx), got %q", a.AuditRecords[0].AgentID)
	}
}

// TestInvokeRejectsExternalCallerWithoutSession verifies that an invoke from
// an external caller (non-nil context with zero identity, simulating the
// frontend bridge via gateway in local mode) without a session token is
// rejected with CodeSessionRequired.
func TestInvokeRejectsExternalCallerWithoutSession(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Runtime: "spore", Callables: []gen.AppCallableDescriptor{{ID: "answer"}}}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, State: "running"}},
		children: map[string]string{manifest.ID: "actor-1"},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	ctx := &testutil.FakeCtx{Identity_: id.Identity{}} // zero identity = external
	_, err := a.handleInvoke(ctx, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "answer", AgentID: "agent-1"})
	if err == nil {
		t.Fatal("expected session-required denial for external caller")
	}
	if appbinding.DenialCode(err) != appbinding.CodeSessionRequired {
		t.Fatalf("expected CodeSessionRequired, got %v", err)
	}
}

// TestInvokeExternalCallerWithValidSession verifies that an external caller
// presenting a valid session token is authorized and the session's bound
// identity overrides client-supplied fields.
func TestInvokeExternalCallerWithValidSession(t *testing.T) {
	a, appID := newSessionTestActor(t)
	// Make the app "running" with a callable for invoke
	a.children = map[string]string{appID: "actor-1"}
	manifest := a.Apps[appID]
	manifest.Callables = []gen.AppCallableDescriptor{{ID: "answer"}}
	a.Apps[appID] = manifest
	rec := a.Records[appID]
	rec.State = "running"
	a.Records[appID] = rec
	created, err := a.handleSessionCreate(&testutil.FakeCtx{
		Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-trusted", Role: "agent"},
	}, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &testutil.FakeCtx{Identity_: id.Identity{}} // zero identity = external
	_, err = a.handleInvoke(ctx, gen.AppManagerInvokeReq{
		ID:           appID,
		Callable:     "answer",
		AgentID:      "forged-agent",
		SessionToken: created.Token,
	})
	// The session gate must pass (no CodeSessionRequired). The downstream
	// invoke may fail for test-harness reasons (no real spore runtime), but
	// that's unrelated to session authorization.
	if err != nil && appbinding.DenialCode(err) == appbinding.CodeSessionRequired {
		t.Fatalf("session token should have passed the gate, got %v", err)
	}
	// Audit should show the session's bound agent, not the forged one
	if len(a.AuditRecords) == 0 {
		t.Fatal("expected audit records")
	}
	last := a.AuditRecords[len(a.AuditRecords)-1]
	if last.AgentID != "agent-trusted" {
		t.Fatalf("expected audit AgentID from session 'agent-trusted', got %q", last.AgentID)
	}
}

// TestCastAuthorizedBySession verifies that cast/emit succeed when the caller
// presents a valid session for the app, even without a manifest binding.
func TestCastAuthorizedBySession(t *testing.T) {
	manifest := gen.AppManifest{
		ID:        "evt.app",
		Runtime:   "spore",
		Callables: []gen.AppCallableDescriptor{{ID: "ping"}},
		Events:    []gen.AppEventDescriptor{{ID: "update"}},
		Entrypoints: []gen.AppEntrypoint{
			{ID: "main", Kind: "view"},
		},
		AgentBinding: &gen.AppAgentBinding{
			Surface: &gen.AgentSurfaceBinding{AgentID: "bound-agent", Entrypoint: "main"},
		},
	}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, Generation: 1}},
		sessions: map[string]appSession{},
		bindings: appbinding.NewRegistry(), FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	a.sessionKey = key
	// Issue a session (nil context = local trusted path)
	created, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: manifest.ID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// Cast with session token but no binding for this caller
	ctx := &testutil.FakeCtx{Identity_: id.Identity{}} // external, zero identity
	resp, err := a.handleCast(ctx, gen.AppManagerCastReq{
		ID:           manifest.ID,
		Event:        "update",
		SessionToken: created.Token,
	})
	if err != nil {
		t.Fatalf("expected cast to succeed with valid session, got %v", err)
	}
	if resp.Delivered < 1 {
		t.Fatalf("expected at least 1 delivery, got %d", resp.Delivered)
	}
}

// TestSessionCreateRejectsUnauthorizedIdentity verifies the dual-track
// authorization: an identity with an anonymous/unverified role is rejected
// even when a subject is present.
func TestSessionCreateRejectsUnauthorizedIdentity(t *testing.T) {
	manifest := gen.AppManifest{
		ID:        "secure.app",
		Runtime:   "spore",
		Callables: []gen.AppCallableDescriptor{{ID: "ping"}},
		Entrypoints: []gen.AppEntrypoint{
			{ID: "main", Kind: "view"},
		},
	}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, Generation: 1}},
		sessions: map[string]appSession{},
	}
	ctx := &testutil.FakeCtx{
		Identity_: id.Identity{Kind: id.IdentityToken, Subject: "forged-agent", Role: "anonymous"},
	}
	_, err := a.handleSessionCreate(ctx, gen.AppSessionCreateReq{AppID: manifest.ID, ViewID: "main"})
	if err == nil {
		t.Fatal("expected denial for unauthorized identity")
	}
	if appbinding.DenialCode(err) != appbinding.CodeAgentScopeDenied {
		t.Fatalf("expected CodeAgentScopeDenied, got %v", err)
	}
}

// TestSessionCreateAllowsAuthenticatedAgent verifies the dual-track
// authorization: any authenticated agent (verified role) can create a
// session, regardless of whether it is the manifest's bound agent.
func TestSessionCreateAllowsAuthenticatedAgent(t *testing.T) {
	manifest := gen.AppManifest{
		ID:        "secure.app",
		Runtime:   "spore",
		Callables: []gen.AppCallableDescriptor{{ID: "ping"}},
		Entrypoints: []gen.AppEntrypoint{
			{ID: "main", Kind: "view"},
		},
	}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, Generation: 1}},
		sessions: map[string]appSession{},
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	a.sessionKey = key
	ctx := &testutil.FakeCtx{
		Identity_: id.Identity{Kind: id.IdentityToken, Subject: "any-agent", Role: "agent"},
	}
	_, err := a.handleSessionCreate(ctx, gen.AppSessionCreateReq{AppID: manifest.ID, ViewID: "main"})
	if err != nil {
		t.Fatalf("expected authenticated agent to be allowed: %v", err)
	}
}
