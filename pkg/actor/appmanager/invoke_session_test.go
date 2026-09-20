package appmanager

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// addInvokeFixture augments a session test actor with the app manifest
// callable + child entry needed for handleInvoke to progress past the
// "app not running" check. It returns nothing; the actor is mutated in place.
func addInvokeFixture(a *Actor, appID string) {
	manifest := a.Apps[appID]
	manifest.Callables = append(manifest.Callables, gen.AppCallableDescriptor{ID: "answer"})
	a.Apps[appID] = manifest
	if a.children == nil {
		a.children = map[string]string{}
	}
	a.children[appID] = "actor-1"
}

// TestInvokeInvalidSessionTokenDenies proves a session token whose signature
// cannot be verified is a hard denial with the CodeSessionInvalid code.
func TestInvokeInvalidSessionTokenDenies(t *testing.T) {
	a, appID := newSessionTestActor(t)
	addInvokeFixture(a, appID)
	// Forge a token signed with a foreign key.
	otherKey := make([]byte, 32)
	tok, err := issueSessionToken(appSession{ID: uuid.NewString(), AppID: appID, ViewID: "main", Nonce: "n", Generation: 1}, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: appID, Callable: "answer", AgentID: "agent-1",
		SessionToken: tok,
	})
	if err == nil {
		t.Fatal("expected session denial")
	}
	if appbinding.DenialCode(err) != appbinding.CodeSessionInvalid {
		t.Fatalf("denial code = %q, want %q", appbinding.DenialCode(err), appbinding.CodeSessionInvalid)
	}
	if len(a.AuditRecords) != 1 || a.AuditRecords[0].Allowed {
		t.Fatalf("expected one denied audit record, got %+v", a.AuditRecords)
	}
}

// TestInvokeExpiredSessionTokenDenies proves an expired token is rejected.
func TestInvokeExpiredSessionTokenDenies(t *testing.T) {
	a, appID := newSessionTestActor(t)
	addInvokeFixture(a, appID)
	expired := appSession{
		ID: uuid.NewString(), AppID: appID, ViewID: "main",
		ExpiresAt: time.Now().Add(-time.Hour).Unix(), Nonce: "n", Generation: 1,
	}
	tok, err := issueSessionToken(expired, a.sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	a.sessions[expired.ID] = expired
	_, err = a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: appID, Callable: "answer", AgentID: "agent-1",
		SessionToken: tok,
	})
	if err == nil {
		t.Fatal("expected expired session denial")
	}
	if appbinding.DenialCode(err) != appbinding.CodeSessionInvalid {
		t.Fatalf("denial code = %q, want %q", appbinding.DenialCode(err), appbinding.CodeSessionInvalid)
	}
}

// TestInvokeRevokedSessionTokenDenies proves a revoked token cannot authorize.
func TestInvokeRevokedSessionTokenDenies(t *testing.T) {
	a, appID := newSessionTestActor(t)
	addInvokeFixture(a, appID)
	created, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleSessionRevoke(nil, gen.AppSessionRevokeReq{Token: created.Token}); err != nil {
		t.Fatal(err)
	}
	_, err = a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: appID, Callable: "answer", AgentID: "agent-1",
		SessionToken: created.Token,
	})
	if err == nil {
		t.Fatal("expected revoked session denial")
	}
	if appbinding.DenialCode(err) != appbinding.CodeSessionInvalid {
		t.Fatalf("denial code = %q, want %q", appbinding.DenialCode(err), appbinding.CodeSessionInvalid)
	}
}

// TestInvokeStaleGenerationSessionTokenDenies proves a generation-stale token
// (app was reloaded after the session was issued) is rejected. The error
// message must carry the [generation_stale] code so the frontend can match on
// it for transparent rebind.
func TestInvokeStaleGenerationSessionTokenDenies(t *testing.T) {
	a, appID := newSessionTestActor(t)
	addInvokeFixture(a, appID)
	created, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// Bump the app generation to simulate a reload.
	rec := a.Records[appID]
	rec.Generation++
	a.Records[appID] = rec
	_, err = a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: appID, Callable: "answer", AgentID: "agent-1",
		SessionToken: created.Token,
	})
	if err == nil {
		t.Fatal("expected stale-generation session denial")
	}
	if appbinding.DenialCode(err) != appbinding.CodeSessionInvalid {
		t.Fatalf("denial code = %q, want %q", appbinding.DenialCode(err), appbinding.CodeSessionInvalid)
	}
	if !strings.Contains(err.Error(), "["+GenerationStaleCode+"]") {
		t.Fatalf("error %q does not contain [%s] code", err.Error(), GenerationStaleCode)
	}
}

// TestInvokeSessionScopeMismatchDenies proves a valid session scoped to a
// different app is rejected with CodeSessionScopeMismatch.
func TestInvokeSessionScopeMismatchDenies(t *testing.T) {
	a, appID := newSessionTestActor(t)
	created, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// Set up a second app and invoke IT with the first app's session token.
	otherApp := "app.other"
	a.Apps[otherApp] = gen.AppManifest{ID: otherApp, Callables: []gen.AppCallableDescriptor{{ID: "answer"}}}
	if a.children == nil {
		a.children = map[string]string{}
	}
	a.children[otherApp] = "actor-other"
	_, err = a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: otherApp, Callable: "answer", AgentID: "agent-1",
		SessionToken: created.Token,
	})
	if err == nil {
		t.Fatal("expected scope mismatch denial")
	}
	if appbinding.DenialCode(err) != appbinding.CodeSessionScopeMismatch {
		t.Fatalf("denial code = %q, want %q", appbinding.DenialCode(err), appbinding.CodeSessionScopeMismatch)
	}
}

// TestInvokeSessionIdentityOverridesClientFields proves that a valid session's
// bound identity overrides client-supplied fields. The session is valid and
// scope-matched, so the override happens; then the invoke fails at callable
// lookup, and the audit record carries the session's identity — not the
// client-supplied forged one. (Project scoping now comes from the session
// or agent identity, not from a stale mount_bundle capability binding.)
func TestInvokeSessionIdentityOverridesClientFields(t *testing.T) {
	a, appID := newSessionTestActor(t)
	addInvokeFixture(a, appID)
	created, err := a.handleSessionCreate(&testutil.FakeCtx{
		Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-trusted", Role: "agent"},
	}, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// Invoke with a forged AgentID but valid session token, targeting a
	// non-existent callable so the failure happens AFTER the session override
	// but BEFORE dispatch (which would need a real ctx).
	_, err = a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: appID, Callable: "nonexistent", AgentID: "forged-agent",
		ProjectID:    "forged-project",
		SessionToken: created.Token,
	})
	if err == nil {
		t.Fatal("expected callable-not-found error")
	}
	if len(a.AuditRecords) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(a.AuditRecords))
	}
	rec := a.AuditRecords[0]
	if rec.AgentID != "agent-trusted" {
		t.Fatalf("audit AgentID = %q, want agent-trusted (session identity)", rec.AgentID)
	}
}

// TestInvokeWithoutSessionIsNotSessionDenied proves that invokes without a
// session token are not rejected by the session gate — internal actor calls
// and identity-based auth continue to work.
func TestInvokeWithoutSessionIsNotSessionDenied(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Callables: []gen.AppCallableDescriptor{{ID: "answer"}}}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, State: "running"}},
		children: map[string]string{manifest.ID: "actor-1"},
		bindings: appbinding.NewRegistry(),
	}
	// Use a non-existent callable so the failure is at lookup, before dispatch.
	_, err := a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: manifest.ID, Callable: "nonexistent", AgentID: "agent-1",
	})
	if err == nil {
		t.Fatal("expected callable-not-found error")
	}
	code := appbinding.DenialCode(err)
	if code == appbinding.CodeSessionInvalid || code == appbinding.CodeSessionScopeMismatch {
		t.Fatalf("invoke without session must not be session-denied, got code %q", code)
	}
}

// TestInvokeEmptySessionTokenSkipsGate proves that a whitespace-only session
// token is treated as absent (the gate is skipped).
func TestInvokeEmptySessionTokenSkipsGate(t *testing.T) {
	manifest := gen.AppManifest{ID: "example.app", Callables: []gen.AppCallableDescriptor{{ID: "answer"}}}
	a := &Actor{
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, State: "running"}},
		children: map[string]string{manifest.ID: "actor-1"},
		bindings: appbinding.NewRegistry(),
	}
	_, err := a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: manifest.ID, Callable: "nonexistent", AgentID: "agent-1",
		SessionToken: "   ",
	})
	if err == nil {
		t.Fatal("expected callable-not-found error")
	}
	code := appbinding.DenialCode(err)
	if code == appbinding.CodeSessionInvalid || code == appbinding.CodeSessionScopeMismatch {
		t.Fatalf("whitespace session token must skip the gate, got code %q", code)
	}
}
