package glassinteract

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/auth"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func freshTestActor(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	a := &Actor{
		store: persist.NewFSPersist(t.TempDir()),
		key:   "test-key",
		jwt:   auth.NewManager(auth.JWTConfig{Secret: []byte("test-secret"), Issuer: "test", ExpiryTime: time.Hour}),
		now:   func() time.Time { return testTime() },
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

// glassCtx returns a FakeCtx authenticated as a glass /ws session for
// sessionID (the identity produced by the gateway for a glass token).
func glassCtx(actorID id.ActorID, sessionID string) *testutil.FakeCtx {
	ctx := testutil.AnonCtx(actorID)
	ctx.Identity_ = id.Identity{Role: "glass", Subject: sessionID}
	return ctx
}

func TestBootstrapIssuesGlassToken(t *testing.T) {
	a, _ := freshTestActor(t)

	resp, err := a.handleBootstrap(nil, gen.GlassBootstrapReq{Key: "test-key", DeviceID: "dev-1"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if resp.SessionID == "" {
		t.Fatal("bootstrap returned empty session id")
	}
	if resp.DeviceID != "dev-1" {
		t.Fatalf("device id = %q, want dev-1", resp.DeviceID)
	}
	if resp.Kind != auth.GlassScope || resp.Scope != auth.GlassScope {
		t.Fatalf("kind/scope = %q/%q, want glass/glass", resp.Kind, resp.Scope)
	}
	exp, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	if err != nil {
		t.Fatalf("expiresAt not RFC3339: %v", err)
	}
	if !exp.After(a.now()) {
		t.Fatalf("expiresAt %v not in the future", exp)
	}

	claims, err := a.jwt.VerifyGlass(resp.Token)
	if err != nil {
		t.Fatalf("issued token does not verify: %v", err)
	}
	if claims.SessionID != resp.SessionID {
		t.Fatalf("token session id = %q, want %q", claims.SessionID, resp.SessionID)
	}
	if claims.DeviceID != "dev-1" {
		t.Fatalf("token device id = %q, want dev-1", claims.DeviceID)
	}
	if claims.Subject != resp.SessionID {
		t.Fatalf("token subject = %q, want %q", claims.Subject, resp.SessionID)
	}
}

func TestBootstrapRejectsInvalidKey(t *testing.T) {
	a, _ := freshTestActor(t)
	_, err := a.handleBootstrap(nil, gen.GlassBootstrapReq{Key: "wrong-key"})
	if err == nil || !strings.Contains(err.Error(), "invalid key") {
		t.Fatalf("bootstrap with wrong key err = %v, want invalid key", err)
	}
}

func TestBootstrapRequiresConfiguredKey(t *testing.T) {
	a, _ := freshTestActor(t)
	a.key = ""
	_, err := a.handleBootstrap(nil, gen.GlassBootstrapReq{Key: "test-key"})
	if err == nil || !strings.Contains(err.Error(), "no credentials provided") {
		t.Fatalf("bootstrap without configured key err = %v", err)
	}
}

func TestBootstrapWithAdminCredentials(t *testing.T) {
	a, _ := freshTestActor(t)
	a.key = "" // no pre-shared key; rely solely on admin password

	userRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	planner := fakePlanner{callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
		if callID != "user.auth_login" {
			return nil, fmt.Errorf("unexpected call %s", callID)
		}
		req := payload.(gen.AuthLoginReq)
		if req.Username == "admin" && req.Password == "admin" {
			return gen.AuthLoginResp{
				Token: "jwt",
				Account: gen.AccountView{
					ID:       "admin",
					Username: "admin",
					Roles:    []string{"admin"},
					Status:   "active",
				},
			}, nil
		}
		return nil, fmt.Errorf("invalid username or password")
	}}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "user" {
			return userRef, true
		}
		return nil, false
	}

	resp, err := a.handleBootstrap(ctx, gen.GlassBootstrapReq{
		Username: "admin",
		Password: "admin",
		DeviceID: "dev-admin",
	})
	if err != nil {
		t.Fatalf("bootstrap with admin credentials: %v", err)
	}
	if resp.SessionID == "" {
		t.Fatal("bootstrap returned empty session id")
	}
	if resp.DeviceID != "dev-admin" {
		t.Fatalf("device id = %q, want dev-admin", resp.DeviceID)
	}
}

func TestBootstrapWithAdminCredentials_RejectsNonAdmin(t *testing.T) {
	a, _ := freshTestActor(t)
	a.key = ""

	planner := fakePlanner{callFunc: func(_ context.Context, _ ref.Ref, _ string, _ any) (any, error) {
		return gen.AuthLoginResp{
			Account: gen.AccountView{Roles: []string{"viewer"}},
		}, nil
	}}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), name == "user"
	}

	_, err := a.handleBootstrap(ctx, gen.GlassBootstrapReq{
		Username: "viewer",
		Password: "pass",
	})
	if err == nil || !strings.Contains(err.Error(), "admin role required") {
		t.Fatalf("non-admin bootstrap err = %v", err)
	}
}

func TestBootstrapWithAdminCredentials_RejectsBadPassword(t *testing.T) {
	a, _ := freshTestActor(t)
	a.key = ""

	planner := fakePlanner{callFunc: func(_ context.Context, _ ref.Ref, _ string, _ any) (any, error) {
		return nil, fmt.Errorf("invalid username or password")
	}}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), name == "user"
	}

	_, err := a.handleBootstrap(ctx, gen.GlassBootstrapReq{
		Username: "admin",
		Password: "wrong",
	})
	if err == nil || !strings.Contains(err.Error(), "auth failed") {
		t.Fatalf("bad password bootstrap err = %v", err)
	}
}

func TestClaimRequiresGlassIdentity(t *testing.T) {
	a, _ := freshTestActor(t)
	ctx := testutil.HumanCtx(testutil.GenActorID()) // role=human

	_, err := a.handleClaim(ctx, gen.GlassSessionClaimReq{SessionID: "gs_aaa"})
	if err == nil || !strings.Contains(err.Error(), "glass-authenticated") {
		t.Fatalf("claim with non-glass role err = %v", err)
	}

	// Glass role but mismatched subject (token bound to another session).
	ctx.Identity_ = id.Identity{Role: "glass", Subject: "gs_other"}
	_, err = a.handleClaim(ctx, gen.GlassSessionClaimReq{SessionID: "gs_aaa"})
	if err == nil || !strings.Contains(err.Error(), "does not match session id") {
		t.Fatalf("claim with mismatched subject err = %v", err)
	}
}

func TestClaimFirstSessionAndGetState(t *testing.T) {
	a, _ := freshTestActor(t)
	sessionID := "gs_aaa"
	ctx := glassCtx(testutil.GenActorID(), sessionID)

	resp, err := a.handleClaim(ctx, gen.GlassSessionClaimReq{SessionID: sessionID, DeviceID: "dev-1"})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if resp.SessionID != sessionID || resp.Generation != 1 || !resp.Online {
		t.Fatalf("claim resp = %+v, want gs_aaa gen 1 online", resp)
	}
	if resp.Reconnected || resp.Replaced {
		t.Fatalf("first claim flags = reconnected=%v replaced=%v, want false", resp.Reconnected, resp.Replaced)
	}

	state, err := a.handleGetState(nil, gen.GlassGetStateReq{})
	if err != nil {
		t.Fatalf("get_state: %v", err)
	}
	if !state.Active || state.Session == nil || state.Session.SessionID != sessionID {
		t.Fatalf("get_state = %+v, want active session %s", state, sessionID)
	}
}

func TestClaimReconnectRestoresLastFrame(t *testing.T) {
	a, _ := freshTestActor(t)
	sessionID := "gs_aaa"
	ctx := glassCtx(testutil.GenActorID(), sessionID)

	if _, err := a.handleClaim(ctx, gen.GlassSessionClaimReq{SessionID: sessionID, DeviceID: "dev-1"}); err != nil {
		t.Fatal(err)
	}

	// Stage 4 render will set this; here it simulates a confirmed frame.
	frame := gen.GlassRenderFrame{Text: "restore me", Layout: "bottom"}
	a.sess.active.LastFrame = frame
	a.sess.active.HasLastFrame = true

	resp, err := a.handleClaim(ctx, gen.GlassSessionClaimReq{SessionID: sessionID, DeviceID: "dev-1"})
	if err != nil {
		t.Fatalf("reconnect claim: %v", err)
	}
	if !resp.Reconnected {
		t.Fatal("same-session claim was not flagged reconnected")
	}
	if resp.LastFrame == nil || resp.LastFrame.Text != "restore me" {
		t.Fatalf("lastFrame not restored on reconnect: %+v", resp.LastFrame)
	}
	if resp.Generation != 1 {
		t.Fatalf("generation changed on reconnect: %d", resp.Generation)
	}
}

func TestClaimReplaceEmitsLifecycleEvents(t *testing.T) {
	a, _ := freshTestActor(t)
	oldID := "gs_old"
	newID := "gs_new"

	oldCtx := glassCtx(testutil.GenActorID(), oldID)
	newCtx := glassCtx(testutil.GenActorID(), newID)

	if _, err := a.handleClaim(oldCtx, gen.GlassSessionClaimReq{SessionID: oldID, DeviceID: "dev-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleClaim(newCtx, gen.GlassSessionClaimReq{SessionID: newID, DeviceID: "dev-2"}); err != nil {
		t.Fatal(err)
	}

	// The second claim's handler emits both the replaced (old) and the
	// online (new) lifecycle events on the claiming context.
	kinds := map[string]bool{}
	for _, ev := range newCtx.EmittedEvents {
		kinds[ev.Kind] = true
	}
	for _, want := range []string{eventOnline, eventReplaced} {
		if !kinds[want] {
			t.Fatalf("missing lifecycle event %q; got %v", want, kinds)
		}
	}

	// The replaced old session cannot reclaim.
	if _, err := a.handleClaim(oldCtx, gen.GlassSessionClaimReq{SessionID: oldID, DeviceID: "dev-1"}); err == nil {
		t.Fatal("replaced session claim succeeded, want rejection")
	}
}

func TestFormalOfflineEventAndPostGraceFresh(t *testing.T) {
	a, ctx := freshTestActor(t)
	sessionID := "gs_aaa"
	gctx := glassCtx(testutil.GenActorID(), sessionID)

	if _, err := a.handleClaim(gctx, gen.GlassSessionClaimReq{SessionID: sessionID, DeviceID: "dev-1"}); err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return testTime().Add(ReconnectGrace) }
	if err := a.handleCheck(ctx); err != nil {
		t.Fatal(err)
	}

	kinds := map[string]bool{}
	for _, ev := range ctx.EmittedEvents {
		kinds[ev.Kind] = true
	}
	if !kinds[eventOffline] {
		t.Fatalf("missing offline event; got %v", kinds)
	}

	// Re-claim after formal offline starts fresh (generation advanced).
	resp, err := a.handleClaim(gctx, gen.GlassSessionClaimReq{SessionID: sessionID, DeviceID: "dev-1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Generation != 2 {
		t.Fatalf("post-offline generation = %d, want 2", resp.Generation)
	}
}

func TestOnInitLoadsPersistedSession(t *testing.T) {
	dir := t.TempDir()
	a1 := &Actor{
		store: persist.NewFSPersist(dir),
		key:   "test-key",
		jwt:   auth.NewManager(auth.JWTConfig{Secret: []byte("test-secret"), Issuer: "test", ExpiryTime: time.Hour}),
		now:   func() time.Time { return testTime() },
	}
	ctx1 := testutil.HumanCtx(testutil.GenActorID())
	if err := a1.OnInit(ctx1); err != nil {
		t.Fatal(err)
	}
	sessionID := "gs_persisted"
	gctx := glassCtx(testutil.GenActorID(), sessionID)
	if _, err := a1.handleClaim(gctx, gen.GlassSessionClaimReq{SessionID: sessionID, DeviceID: "dev-9"}); err != nil {
		t.Fatal(err)
	}
	if err := a1.Save(); err != nil {
		t.Fatal(err)
	}

	// New actor on the same store restores the logical session.
	a2 := &Actor{
		store: persist.NewFSPersist(dir),
		key:   "test-key",
		jwt:   auth.NewManager(auth.JWTConfig{Secret: []byte("test-secret"), Issuer: "test", ExpiryTime: time.Hour}),
		now:   func() time.Time { return testTime() },
	}
	ctx2 := testutil.HumanCtx(testutil.GenActorID())
	if err := a2.OnInit(ctx2); err != nil {
		t.Fatal(err)
	}
	if err := a2.OnStart(ctx2); err != nil {
		t.Fatal(err)
	}

	state, err := a2.handleGetState(nil, gen.GlassGetStateReq{})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.Session == nil || state.Session.SessionID != sessionID {
		t.Fatalf("restored get_state = %+v, want active %s", state, sessionID)
	}
}
