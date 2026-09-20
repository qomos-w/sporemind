package appmanager

import (
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// routeTestKey returns a deterministic 32-byte key for tests.
func routeTestKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func newRouteTestActor() *Actor {
	key := routeTestKey()
	source := gen.AppManifest{ID: "app.source", Runtime: "spore", Callables: []gen.AppCallableDescriptor{{ID: "ping"}}}
	target := gen.AppManifest{ID: "app.target", Runtime: "spore", Callables: []gen.AppCallableDescriptor{{ID: "echo"}, {ID: "answer"}}}
	return &Actor{
		Apps: map[string]gen.AppManifest{
			source.ID: source,
			target.ID: target,
		},
		Records: map[string]appRecord{
			source.ID: {Manifest: source, State: "running", PackageHash: "h"},
			target.ID: {Manifest: target, State: "running", PackageHash: "h"},
		},
		children:   map[string]string{source.ID: "src-actor", target.ID: "tgt-actor"},
		sessions:   map[string]appSession{},
		sessionKey: key,
		bindings:   nil,
	}
}

// ---------------------------------------------------------------------------
// HMAC primitives (issueRouteToken / parseRouteToken)
// ---------------------------------------------------------------------------

func TestRouteTokenRoundTrip(t *testing.T) {
	key := routeTestKey()
	rt := routeToken{
		SourceAppID: "app.source",
		TargetAppID: "app.target",
		Callable:    "echo",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}
	tok, err := issueRouteToken(rt, key)
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" {
		t.Fatal("issued token must be non-empty")
	}
	parsed, err := parseRouteToken(tok, key)
	if err != nil {
		t.Fatalf("parseRouteToken: %v", err)
	}
	if parsed != rt {
		t.Fatalf("round-trip mismatch:\n got  %+v\n want %+v", parsed, rt)
	}
}

func TestRouteTokenRejectsForgedSignature(t *testing.T) {
	key := routeTestKey()
	rt := routeToken{SourceAppID: "a", TargetAppID: "b", Callable: "fn", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	tok, err := issueRouteToken(rt, key)
	if err != nil {
		t.Fatal(err)
	}
	// Flip the first character of the payload half. The last char of the
	// MAC half is unreliable (a 32-byte MAC's final base64 char carries
	// only 4 significant bits, so swapping A/B can leave the decoded MAC
	// unchanged); the first payload char always alters the decoded bytes.
	forged := tok
	if forged[0] == 'A' {
		forged = "B" + forged[1:]
	} else {
		forged = "A" + forged[1:]
	}
	if _, err := parseRouteToken(forged, key); err == nil {
		t.Fatal("tampered route token must fail verification")
	}
}

func TestRouteTokenRejectsForeignKey(t *testing.T) {
	key := routeTestKey()
	foreign := make([]byte, 32)
	for i := range foreign {
		foreign[i] = byte(255 - i)
	}
	rt := routeToken{SourceAppID: "a", TargetAppID: "b", Callable: "fn", ExpiresAt: 0}
	tok, err := issueRouteToken(rt, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseRouteToken(tok, foreign); err == nil {
		t.Fatal("route token signed with one key must not verify under another")
	}
}

func TestRouteTokenRejectsEmptyKey(t *testing.T) {
	rt := routeToken{SourceAppID: "a", TargetAppID: "b", Callable: "fn"}
	if _, err := issueRouteToken(rt, nil); err == nil {
		t.Fatal("issueRouteToken with empty key must fail")
	}
	if _, err := parseRouteToken("abc.def", nil); err == nil {
		t.Fatal("parseRouteToken with empty key must fail")
	}
}

// ---------------------------------------------------------------------------
// Route token issuance handler (handleRouteToken)
// ---------------------------------------------------------------------------

func TestRouteTokenIssuedForValidRequest(t *testing.T) {
	a := newRouteTestActor()
	resp, err := a.handleRouteToken(nil, gen.AppRouteTokenReq{
		SourceAppID: "app.source",
		TargetAppID: "app.target",
		Callable:    "echo",
	})
	if err != nil {
		t.Fatalf("handleRouteToken: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("response must carry a token")
	}
	if resp.ExpiresAt <= time.Now().Unix() {
		t.Fatal("expires_at must be in the future")
	}
	// The issued token must round-trip with correct claims.
	rt, err := parseRouteToken(resp.Token, a.sessionKey)
	if err != nil {
		t.Fatalf("parseRouteToken: %v", err)
	}
	if rt.SourceAppID != "app.source" || rt.TargetAppID != "app.target" || rt.Callable != "echo" {
		t.Fatalf("route token claims mismatch: %+v", rt)
	}
	if rt.ExpiresAt != resp.ExpiresAt {
		t.Fatalf("expires_at mismatch: token %d vs resp %d", rt.ExpiresAt, resp.ExpiresAt)
	}
	// Issuance should produce an audit record.
	if len(a.AuditRecords) == 0 {
		t.Fatal("route token issuance must be audited")
	}
	last := a.AuditRecords[len(a.AuditRecords)-1]
	if !last.Allowed || last.AppID != "app.source" {
		t.Fatalf("unexpected audit record: %+v", last)
	}
}

func TestRouteTokenIssuanceRejectsMissingFields(t *testing.T) {
	a := newRouteTestActor()
	cases := []gen.AppRouteTokenReq{
		{TargetAppID: "app.target", Callable: "echo"},                    // missing source
		{SourceAppID: "app.source", Callable: "echo"},                    // missing target
		{SourceAppID: "app.source", TargetAppID: "app.target"},           // missing callable
		{SourceAppID: "  ", TargetAppID: "app.target", Callable: "echo"}, // blank source
	}
	for i, req := range cases {
		if _, err := a.handleRouteToken(nil, req); err == nil {
			t.Fatalf("case %d: missing fields must be rejected", i)
		}
	}
}

func TestRouteTokenIssuanceRejectsUnknownApps(t *testing.T) {
	a := newRouteTestActor()
	if _, err := a.handleRouteToken(nil, gen.AppRouteTokenReq{SourceAppID: "ghost", TargetAppID: "app.target", Callable: "echo"}); err == nil {
		t.Fatal("unknown source app must be rejected")
	}
	if _, err := a.handleRouteToken(nil, gen.AppRouteTokenReq{SourceAppID: "app.source", TargetAppID: "ghost", Callable: "echo"}); err == nil {
		t.Fatal("unknown target app must be rejected")
	}
}

func TestRouteTokenIssuanceRejectsUnknownCallable(t *testing.T) {
	a := newRouteTestActor()
	if _, err := a.handleRouteToken(nil, gen.AppRouteTokenReq{SourceAppID: "app.source", TargetAppID: "app.target", Callable: "nope"}); err == nil {
		t.Fatal("unknown callable must be rejected")
	}
}

// ---------------------------------------------------------------------------
// Route token validation in handleInvoke
// ---------------------------------------------------------------------------

func TestInvokeRejectsRouteTokenAudienceMismatch(t *testing.T) {
	a := newRouteTestActor()
	// Token authorizes routing to app.target, but the invoke targets app.source.
	tok, err := issueRouteToken(routeToken{
		SourceAppID: "app.source",
		TargetAppID: "app.target",
		Callable:    "answer",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}, a.sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: "app.source", Callable: "answer", AgentID: "agent-1",
		RouteDepth: 1, RouteToken: tok,
	})
	if err == nil {
		t.Fatal("audience-mismatched route token must be rejected")
	}
}

func TestInvokeRejectsRouteTokenCallableMismatch(t *testing.T) {
	a := newRouteTestActor()
	// Token authorizes "echo" but invoke requests "answer".
	tok, err := issueRouteToken(routeToken{
		SourceAppID: "app.source",
		TargetAppID: "app.target",
		Callable:    "echo",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}, a.sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: "app.target", Callable: "answer", AgentID: "agent-1",
		RouteDepth: 1, RouteToken: tok,
	})
	if err == nil {
		t.Fatal("callable-mismatched route token must be rejected")
	}
}

func TestInvokeRejectsExpiredRouteToken(t *testing.T) {
	a := newRouteTestActor()
	tok, err := issueRouteToken(routeToken{
		SourceAppID: "app.source",
		TargetAppID: "app.target",
		Callable:    "echo",
		ExpiresAt:   time.Now().Add(-time.Hour).Unix(), // already expired
	}, a.sessionKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: "app.target", Callable: "echo", AgentID: "agent-1",
		RouteDepth: 1, RouteToken: tok,
	})
	if err == nil {
		t.Fatal("expired route token must be rejected")
	}
}

func TestInvokeRejectsRouteTokenSignedWithDifferentKey(t *testing.T) {
	a := newRouteTestActor()
	otherKey := make([]byte, 32)
	for i := range otherKey {
		otherKey[i] = byte(200 - i)
	}
	tok, err := issueRouteToken(routeToken{
		SourceAppID: "app.source",
		TargetAppID: "app.target",
		Callable:    "echo",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.handleInvoke(nil, gen.AppManagerInvokeReq{
		ID: "app.target", Callable: "echo", AgentID: "agent-1",
		RouteDepth: 1, RouteToken: tok,
	})
	if err == nil {
		t.Fatal("route token signed with foreign key must be rejected")
	}
}

func TestInvokeRouteTokenIssuedClaimsSatisfyValidation(t *testing.T) {
	a := newRouteTestActor()
	// Issue a valid route token for app.source → app.target.echo.
	resp, err := a.handleRouteToken(nil, gen.AppRouteTokenReq{
		SourceAppID: "app.source",
		TargetAppID: "app.target",
		Callable:    "echo",
	})
	if err != nil {
		t.Fatalf("issue route token: %v", err)
	}
	// Parse and verify the claims satisfy every check handleInvoke enforces.
	rt, err := parseRouteToken(resp.Token, a.sessionKey)
	if err != nil {
		t.Fatalf("parseRouteToken: %v", err)
	}
	// Audience: target must equal the app that would be invoked.
	if rt.TargetAppID != "app.target" {
		t.Fatalf("audience = %q, want app.target", rt.TargetAppID)
	}
	// Callable must match the requested callable.
	if rt.Callable != "echo" {
		t.Fatalf("callable = %q, want echo", rt.Callable)
	}
	// Expiry must not be in the past.
	if sessionExpired(rt.ExpiresAt, time.Now()) {
		t.Fatal("issued route token must not be expired")
	}
}
