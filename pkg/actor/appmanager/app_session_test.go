package appmanager

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qomos-w/gospore/id"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// newSessionTestActor builds an Actor wired with an ephemeral signing key and
// a single registered app at Generation 1, ready for session round-trips.
func newSessionTestActor(t *testing.T) (*Actor, string) {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i) // deterministic key is fine for tests
	}
	const appID = "app.one"
	manifest := gen.AppManifest{
		ID: appID, Entrypoints: []gen.AppEntrypoint{{ID: "main", Kind: "view"}},
	}
	a := &Actor{
		Apps:       map[string]gen.AppManifest{appID: manifest},
		Records:    map[string]appRecord{appID: {Manifest: manifest, Generation: 1}},
		sessions:   map[string]appSession{},
		sessionKey: key,
	}
	return a, appID
}

func TestAppSessionIssuesSignedTokenAndResolves(t *testing.T) {
	a, appID := newSessionTestActor(t)
	ctx := &testutil.FakeCtx{
		Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-trusted", Role: "agent"},
	}
	created, err := a.handleSessionCreate(ctx, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// The create response must carry the trusted-boundary claims + token.
	if created.Token == "" {
		t.Fatal("create response must issue a token")
	}
	if created.Origin != "user" {
		t.Fatalf("origin default = %q, want user", created.Origin)
	}
	if created.Nonce == "" {
		t.Fatal("nonce must be populated")
	}
	if created.Generation != 1 {
		t.Fatalf("generation = %d, want 1", created.Generation)
	}
	if created.ExpiresAt <= time.Now().Unix() {
		t.Fatal("expires_at must be in the future")
	}
	// Session identity comes from the caller, not from a manifest binding.
	if created.AgentID != "agent-trusted" {
		t.Fatalf("session did not bind caller identity: %+v", created)
	}
	resolved, err := a.handleSessionResolve(nil, gen.AppSessionResolveReq{Token: created.Token})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.AppID != appID || resolved.AgentID != "agent-trusted" || resolved.Nonce != created.Nonce {
		t.Fatalf("resolve identity mismatch: %+v", resolved)
	}
}

func TestAppSessionRejectsUnknownView(t *testing.T) {
	a, appID := newSessionTestActor(t)
	if _, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: appID, ViewID: "forged"}); err == nil {
		t.Fatal("unknown view must be rejected")
	}
}

func TestAppSessionRejectsForgedSignature(t *testing.T) {
	a, appID := newSessionTestActor(t)
	created, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with the payload portion of the token. The payload is the first
	// base64 segment; flipping its first character always changes the decoded
	// bytes, guaranteeing the HMAC signature verification fails.
	parts := strings.Split(created.Token, ".")
	if len(parts) != 2 {
		t.Fatalf("expected token with payload.signature format, got %q", created.Token)
	}
	payload := parts[0]
	switch payload[0] {
	case 'A':
		payload = "B" + payload[1:]
	default:
		payload = "A" + payload[1:]
	}
	forged := payload + "." + parts[1]
	if _, err := a.handleSessionResolve(nil, gen.AppSessionResolveReq{Token: forged}); err == nil {
		t.Fatal("tampered token must not resolve")
	}
	// A token signed with a different key (simulating another process) is rejected.
	otherKey := make([]byte, 32)
	tok, err := issueSessionToken(appSession{ID: uuid.NewString(), AppID: appID, ViewID: "main", Nonce: "n", Generation: 1}, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	a.sessions["other"] = appSession{ID: "other", AppID: appID, Nonce: "n", Generation: 1}
	if _, err := a.handleSessionResolve(nil, gen.AppSessionResolveReq{Token: tok}); err == nil {
		t.Fatal("token signed with a foreign key must not resolve")
	}
}

func TestAppSessionRejectsExpired(t *testing.T) {
	a, appID := newSessionTestActor(t)
	expired := appSession{
		ID: uuid.NewString(), AppID: appID, ViewID: "main",
		ExpiresAt: time.Now().Add(-time.Hour).Unix(), Nonce: "n", Generation: 1,
	}
	tok, err := issueSessionToken(expired, a.sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	a.sessions[expired.ID] = expired
	if _, err := a.handleSessionResolve(nil, gen.AppSessionResolveReq{Token: tok}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired token must be rejected with expiry error, got %v", err)
	}
	// ExpiresAt == 0 means no expiry: such a token resolves even far in the future.
	never := appSession{ID: uuid.NewString(), AppID: appID, ViewID: "main", ExpiresAt: 0, Nonce: "n2", Generation: 1}
	neverTok, err := issueSessionToken(never, a.sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	a.sessions[never.ID] = never
	if _, err := a.resolveSession(neverTok, time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatalf("never-expiring token must resolve, got %v", err)
	}
}

func TestAppSessionRejectsNonceMismatch(t *testing.T) {
	a, appID := newSessionTestActor(t)
	created, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// Mutate the stored nonce so it no longer matches the token's claim.
	stored := a.sessions[created.SessionID]
	stored.Nonce = "tampered-nonce"
	a.sessions[created.SessionID] = stored
	if _, err := a.handleSessionResolve(nil, gen.AppSessionResolveReq{Token: created.Token}); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("nonce mismatch must be rejected, got %v", err)
	}
}

func TestAppSessionRejectsStaleGeneration(t *testing.T) {
	a, appID := newSessionTestActor(t)
	created, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// Resolve succeeds before reload.
	if _, err := a.handleSessionResolve(nil, gen.AppSessionResolveReq{Token: created.Token}); err != nil {
		t.Fatalf("pre-reload resolve: %v", err)
	}
	// A reload bumps the app generation; the old token is now stale.
	rec := a.Records[appID]
	rec.Generation++
	a.Records[appID] = rec
	if _, err := a.handleSessionResolve(nil, gen.AppSessionResolveReq{Token: created.Token}); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("stale-generation token must be rejected, got %v", err)
	}
	// A freshly issued token at the new generation resolves again.
	fresh, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Generation != 2 {
		t.Fatalf("fresh token generation = %d, want 2", fresh.Generation)
	}
	if _, err := a.handleSessionResolve(nil, gen.AppSessionResolveReq{Token: fresh.Token}); err != nil {
		t.Fatalf("fresh token must resolve: %v", err)
	}
}

func TestAppSessionRevokedByToken(t *testing.T) {
	a, appID := newSessionTestActor(t)
	created, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleSessionRevoke(nil, gen.AppSessionRevokeReq{Token: created.Token}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleSessionResolve(nil, gen.AppSessionResolveReq{Token: created.Token}); err == nil {
		t.Fatal("revoked session must not resolve")
	}
	// Revoking twice fails on the missing map entry.
	if _, err := a.handleSessionRevoke(nil, gen.AppSessionRevokeReq{Token: created.Token}); err == nil {
		t.Fatal("revoking an already-revoked session must fail")
	}
}

func TestRevokeAppSessionsCascadeIsScoped(t *testing.T) {
	a, appID := newSessionTestActor(t)
	created, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// Seed a session for a different app to prove the cascade is scoped.
	otherApp := "app.two"
	a.Apps[otherApp] = gen.AppManifest{ID: otherApp, Entrypoints: []gen.AppEntrypoint{{ID: "main", Kind: "view"}}}
	a.Records[otherApp] = appRecord{Generation: 1}
	other, err := a.handleSessionCreate(nil, gen.AppSessionCreateReq{AppID: otherApp, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// revokeAppSessions is the unregister cascade hook.
	a.revokeAppSessions(appID)
	if _, err := a.handleSessionResolve(nil, gen.AppSessionResolveReq{Token: created.Token}); err == nil {
		t.Fatal("unregistered app session must not resolve")
	}
	if _, err := a.handleSessionResolve(nil, gen.AppSessionResolveReq{Token: other.Token}); err != nil {
		t.Fatalf("other app session must survive scoped cascade: %v", err)
	}
}
