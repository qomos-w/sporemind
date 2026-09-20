package frpmanager

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func freshFM(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	a := &Actor{store: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

func freshFMAnon(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	a := &Actor{store: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

func validCreateReq() domain.FrpManagerCreateReq {
	return domain.FrpManagerCreateReq{
		Name:       "primary",
		ServerAddr: "frps.example.com:7000",
		Token:      "super-secret",
		Tls:        true,
		Proxies: []domain.FrpProxy{
			{Name: "ssh", Kind: "tcp", LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: 6022},
		},
	}
}

func TestHandleList_Empty(t *testing.T) {
	a, ctx := freshFM(t)
	resp, err := a.handleList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("expected empty list, got %d items", len(resp.Items))
	}
}

func TestHandleCreate_Valid(t *testing.T) {
	a, ctx := freshFM(t)
	resp, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config.ID != "inst-0" {
		t.Errorf("expected first ID inst-0, got %q", resp.Config.ID)
	}
	if resp.Config.Name != "primary" {
		t.Errorf("expected name primary, got %q", resp.Config.Name)
	}
	if resp.Config.ServerAddr != "frps.example.com:7000" {
		t.Errorf("expected serverAddr propagated, got %q", resp.Config.ServerAddr)
	}
	if len(a.Instances) != 1 {
		t.Fatalf("expected 1 persisted instance, got %d", len(a.Instances))
	}
	if a.Instances[0].Token != "super-secret" {
		t.Errorf("persisted record must keep token, got %q", a.Instances[0].Token)
	}
	if a.nextID != 1 {
		t.Errorf("nextID should advance to 1, got %d", a.nextID)
	}
}

// TestHandleCreate_TokenStripped: the create response (which goes back to
// the AdminOnly caller) must not carry the token — defense in depth, since
// the same caller pushed it in.
func TestHandleCreate_TokenStripped(t *testing.T) {
	a, ctx := freshFM(t)
	resp, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config.Token != "" {
		t.Errorf("create response must strip token, got %q", resp.Config.Token)
	}
	if a.Instances[0].Token != "super-secret" {
		t.Errorf("token strip on response must not mutate stored cfg, got %q", a.Instances[0].Token)
	}
}

func TestHandleCreate_AssignsSequentialIDs(t *testing.T) {
	a, ctx := freshFM(t)
	r1, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	req2 := validCreateReq()
	req2.Name = "secondary"
	r2, err := a.handleCreate(ctx, req2)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Config.ID != "inst-0" || r2.Config.ID != "inst-1" {
		t.Errorf("expected inst-0, inst-1; got %q, %q", r1.Config.ID, r2.Config.ID)
	}
}

func TestHandleCreate_EmptyName(t *testing.T) {
	a, ctx := freshFM(t)
	req := validCreateReq()
	req.Name = ""
	if _, err := a.handleCreate(ctx, req); err == nil {
		t.Error("expected error for empty name")
	}
	if len(a.Instances) != 0 {
		t.Errorf("invalid create must not persist; got %d instances", len(a.Instances))
	}
}

func TestHandleCreate_DuplicateName(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleCreate(ctx, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleCreate(ctx, validCreateReq()); err == nil {
		t.Error("expected error for duplicate name")
	}
	if len(a.Instances) != 1 {
		t.Errorf("expected 1 instance after duplicate rejection, got %d", len(a.Instances))
	}
}

func TestHandleCreate_InvalidConfig(t *testing.T) {
	a, ctx := freshFM(t)
	req := validCreateReq()
	req.ServerAddr = "no-port"
	if _, err := a.handleCreate(ctx, req); err == nil {
		t.Error("expected error for invalid serverAddr")
	}
	if len(a.Instances) != 0 {
		t.Errorf("invalid create must not persist; got %d instances", len(a.Instances))
	}
	if a.nextID != 0 {
		t.Errorf("nextID must not advance on validation failure; got %d", a.nextID)
	}
}

func TestHandleCreate_RequiresHuman(t *testing.T) {
	a, ctx := freshFMAnon(t)
	if _, err := a.handleCreate(ctx, validCreateReq()); err == nil {
		t.Error("expected error for anonymous role")
	}
}

// TestHandleList_TokenStripped: the security boundary — Public list must
// never expose any instance's token to anonymous callers.
func TestHandleList_TokenStripped(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleCreate(ctx, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	if resp.Items[0].Config.Token != "" {
		t.Errorf("list must strip token, got %q", resp.Items[0].Config.Token)
	}
	if a.Instances[0].Token != "super-secret" {
		t.Errorf("list must not mutate stored token, got %q", a.Instances[0].Token)
	}
}

func TestHandleGet_Valid(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleGet(ctx, domain.FrpManagerGetReq{ID: created.Config.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config.ID != created.Config.ID {
		t.Errorf("expected ID %q, got %q", created.Config.ID, resp.Config.ID)
	}
	if resp.Config.Token != "" {
		t.Errorf("get must strip token, got %q", resp.Config.Token)
	}
}

func TestHandleGet_NotFound(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleGet(ctx, domain.FrpManagerGetReq{ID: "ghost"}); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestHandleRemove_Valid(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleRemove(ctx, domain.FrpManagerRemoveReq{ID: created.Config.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config.ID != created.Config.ID {
		t.Errorf("expected returned ID %q, got %q", created.Config.ID, resp.Config.ID)
	}
	if resp.Config.Token != "" {
		t.Errorf("remove response must strip token, got %q", resp.Config.Token)
	}
	if len(a.Instances) != 0 {
		t.Errorf("expected list empty after remove, got %d items", len(a.Instances))
	}
}

func TestHandleRemove_NotFound(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleRemove(ctx, domain.FrpManagerRemoveReq{ID: "ghost"}); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestHandleRemove_RequiresHuman(t *testing.T) {
	a, ctx := freshFMAnon(t)
	if _, err := a.handleRemove(ctx, domain.FrpManagerRemoveReq{ID: "inst-0"}); err == nil {
		t.Error("expected error for anonymous role")
	}
}

func TestPersistRoundTrip(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleCreate(ctx, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	req2 := validCreateReq()
	req2.Name = "secondary"
	if _, err := a.handleCreate(ctx, req2); err != nil {
		t.Fatal(err)
	}
	// Simulate restart: new actor with the same actorID + store path.
	b := &Actor{actorID: a.actorID, store: a.store}
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	if len(b.Instances) != 2 {
		t.Fatalf("expected 2 instances restored, got %d", len(b.Instances))
	}
	if b.Instances[0].Token != "super-secret" {
		t.Errorf("expected token restored, got %q", b.Instances[0].Token)
	}
	if b.Instances[0].Name != "primary" || b.Instances[1].Name != "secondary" {
		t.Errorf("expected names restored, got %q, %q", b.Instances[0].Name, b.Instances[1].Name)
	}
	if b.nextID != 2 {
		t.Errorf("expected nextID restored to 2, got %d", b.nextID)
	}
}

// TestLoad_RehydratesNextID: if the persisted counter is behind the actual
// max(id), Load bumps it to max+1 so new creates don't reuse an ID. Guards
// against a mid-write crash that bumps the counter but never flushes.
func TestLoad_RehydratesNextID(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "actor-1"}
	a.Instances = []domain.FrpInstanceConfig{
		{ID: "inst-0", Name: "a", ServerAddr: "x:1", Proxies: []domain.FrpProxy{{Name: "p", Kind: "tcp", LocalPort: 1, RemotePort: 2}}},
		{ID: "inst-3", Name: "b", ServerAddr: "x:1", Proxies: []domain.FrpProxy{{Name: "p", Kind: "tcp", LocalPort: 1, RemotePort: 2}}},
	}
	a.nextID = 0
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b := &Actor{actorID: "actor-1", store: a.store}
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	if b.nextID != 4 {
		t.Errorf("expected nextID rehydrated to 4 (max+1), got %d", b.nextID)
	}
}

func TestHandleUpdate_Valid(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	req := domain.FrpManagerUpdateReq{
		ID: created.Config.ID,
		Config: domain.FrpInstanceConfig{
			Name:       "renamed",
			ServerAddr: "other.example.com:8000",
			Proxies:    []domain.FrpProxy{{Name: "web", Kind: "tcp", LocalPort: 80, RemotePort: 6080}},
		},
	}
	resp, err := a.handleUpdate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config.ID != created.Config.ID {
		t.Errorf("expected ID preserved, got %q", resp.Config.ID)
	}
	if resp.Config.Name != "renamed" {
		t.Errorf("expected name updated, got %q", resp.Config.Name)
	}
	if a.Instances[0].Name != "renamed" {
		t.Errorf("expected stored name updated, got %q", a.Instances[0].Name)
	}
	if a.Instances[0].ServerAddr != "other.example.com:8000" {
		t.Errorf("expected stored serverAddr updated, got %q", a.Instances[0].ServerAddr)
	}
	if resp.Config.Token != "" {
		t.Errorf("update response must strip token, got %q", resp.Config.Token)
	}
}

func TestHandleUpdate_NotFound(t *testing.T) {
	a, ctx := freshFM(t)
	req := domain.FrpManagerUpdateReq{
		ID:     "ghost",
		Config: domain.FrpInstanceConfig{Name: "x", ServerAddr: "x:1", Proxies: []domain.FrpProxy{{Name: "p", Kind: "tcp", LocalPort: 1, RemotePort: 2}}},
	}
	if _, err := a.handleUpdate(ctx, req); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestHandleUpdate_DuplicateName(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleCreate(ctx, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	req2 := validCreateReq()
	req2.Name = "secondary"
	second, err := a.handleCreate(ctx, req2)
	if err != nil {
		t.Fatal(err)
	}
	// Try to rename "secondary" → "primary" (already taken).
	update := domain.FrpManagerUpdateReq{
		ID: second.Config.ID,
		Config: domain.FrpInstanceConfig{
			Name:       "primary",
			ServerAddr: "x:1",
			Proxies:    []domain.FrpProxy{{Name: "p", Kind: "tcp", LocalPort: 1, RemotePort: 2}},
		},
	}
	if _, err := a.handleUpdate(ctx, update); err == nil {
		t.Error("expected error for duplicate name")
	}
}

// TestHandleUpdate_SameNameAllowed: an update that keeps the existing name
// must not be rejected as a duplicate against itself.
func TestHandleUpdate_SameNameAllowed(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	req := domain.FrpManagerUpdateReq{
		ID: created.Config.ID,
		Config: domain.FrpInstanceConfig{
			Name:       "primary", // same as create
			ServerAddr: "newaddr.example.com:9000",
			Proxies:    []domain.FrpProxy{{Name: "ssh", Kind: "tcp", LocalPort: 22, RemotePort: 6022}},
		},
	}
	if _, err := a.handleUpdate(ctx, req); err != nil {
		t.Errorf("update with same name must succeed, got %v", err)
	}
	if a.Instances[0].ServerAddr != "newaddr.example.com:9000" {
		t.Errorf("expected serverAddr updated, got %q", a.Instances[0].ServerAddr)
	}
}

func TestHandleUpdate_InvalidConfig(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	req := domain.FrpManagerUpdateReq{
		ID: created.Config.ID,
		Config: domain.FrpInstanceConfig{
			Name:       "renamed",
			ServerAddr: "no-port",
			Proxies:    []domain.FrpProxy{{Name: "p", Kind: "tcp", LocalPort: 1, RemotePort: 2}},
		},
	}
	if _, err := a.handleUpdate(ctx, req); err == nil {
		t.Error("expected error for invalid serverAddr")
	}
	// State must not have changed.
	if a.Instances[0].Name != "primary" {
		t.Errorf("invalid update must not mutate stored cfg, got name %q", a.Instances[0].Name)
	}
}

func TestHandleUpdate_EmptyName(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	req := domain.FrpManagerUpdateReq{
		ID: created.Config.ID,
		Config: domain.FrpInstanceConfig{
			Name:       "",
			ServerAddr: "x:1",
			Proxies:    []domain.FrpProxy{{Name: "p", Kind: "tcp", LocalPort: 1, RemotePort: 2}},
		},
	}
	if _, err := a.handleUpdate(ctx, req); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestHandleUpdate_RequiresHuman(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleCreate(ctx, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	req := domain.FrpManagerUpdateReq{
		ID: "inst-0",
		Config: domain.FrpInstanceConfig{
			Name:       "renamed",
			ServerAddr: "x:1",
			Proxies:    []domain.FrpProxy{{Name: "p", Kind: "tcp", LocalPort: 1, RemotePort: 2}},
		},
	}
	if _, err := a.handleUpdate(anonCtx, req); err == nil {
		t.Error("expected error for anonymous role")
	}
}

// TestHandleUpdate_PreservesDisabled: lifecycle is owned by start/stop, not
// update. If the instance was stopped, update must keep it stopped — even
// when the caller's req.Config.Disabled is false.
func TestHandleUpdate_PreservesDisabled(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleStop(ctx, domain.FrpManagerStopReq{ID: created.Config.ID}); err != nil {
		t.Fatal(err)
	}
	if !a.Instances[0].Disabled {
		t.Fatal("setup: handleStop should have flipped Disabled=true")
	}
	req := domain.FrpManagerUpdateReq{
		ID: created.Config.ID,
		Config: domain.FrpInstanceConfig{
			Name:       "renamed",
			ServerAddr: "x:1",
			Proxies:    []domain.FrpProxy{{Name: "p", Kind: "tcp", LocalPort: 1, RemotePort: 2}},
			Disabled:   false, // caller tries to re-enable via update — must be ignored
		},
	}
	if _, err := a.handleUpdate(ctx, req); err != nil {
		t.Fatal(err)
	}
	if !a.Instances[0].Disabled {
		t.Error("update must preserve existing Disabled flag (start/stop owns lifecycle)")
	}
}

// TestHandleUpdate_PreservesTokenWhenEmpty: edit UI can't see the existing
// token (Public list strips it). An empty Token in the update request must
// keep the existing stored token.
func TestHandleUpdate_PreservesTokenWhenEmpty(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	req := domain.FrpManagerUpdateReq{
		ID: created.Config.ID,
		Config: domain.FrpInstanceConfig{
			Name:       "primary",
			ServerAddr: "x:1",
			Token:      "", // empty — preserve existing
			Proxies:    []domain.FrpProxy{{Name: "p", Kind: "tcp", LocalPort: 1, RemotePort: 2}},
		},
	}
	if _, err := a.handleUpdate(ctx, req); err != nil {
		t.Fatal(err)
	}
	if a.Instances[0].Token != "super-secret" {
		t.Errorf("expected token preserved, got %q", a.Instances[0].Token)
	}
}

// TestHandleUpdate_AcceptsNewToken: a non-empty Token in the update request
// overwrites the existing token (this is how token rotation happens).
func TestHandleUpdate_AcceptsNewToken(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	req := domain.FrpManagerUpdateReq{
		ID: created.Config.ID,
		Config: domain.FrpInstanceConfig{
			Name:       "primary",
			ServerAddr: "x:1",
			Token:      "rotated",
			Proxies:    []domain.FrpProxy{{Name: "p", Kind: "tcp", LocalPort: 1, RemotePort: 2}},
		},
	}
	if _, err := a.handleUpdate(ctx, req); err != nil {
		t.Fatal(err)
	}
	if a.Instances[0].Token != "rotated" {
		t.Errorf("expected token rotated, got %q", a.Instances[0].Token)
	}
}

func TestHandleStart_FlipsDisabled(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleStop(ctx, domain.FrpManagerStopReq{ID: created.Config.ID}); err != nil {
		t.Fatal(err)
	}
	if !a.Instances[0].Disabled {
		t.Fatal("setup: stop should have flipped Disabled=true")
	}
	if _, err := a.handleStart(ctx, domain.FrpManagerStartReq{ID: created.Config.ID}); err != nil {
		t.Fatal(err)
	}
	if a.Instances[0].Disabled {
		t.Error("expected Disabled=false after handleStart")
	}
}

func TestHandleStart_NotFound(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleStart(ctx, domain.FrpManagerStartReq{ID: "ghost"}); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestHandleStart_RequiresHuman(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleCreate(ctx, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.handleStart(anonCtx, domain.FrpManagerStartReq{ID: "inst-0"}); err == nil {
		t.Error("expected error for anonymous role")
	}
}

func TestHandleStop_FlipsDisabled(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	if a.Instances[0].Disabled {
		t.Fatal("setup: fresh create should have Disabled=false")
	}
	if _, err := a.handleStop(ctx, domain.FrpManagerStopReq{ID: created.Config.ID}); err != nil {
		t.Fatal(err)
	}
	if !a.Instances[0].Disabled {
		t.Error("expected Disabled=true after handleStop")
	}
}

func TestHandleStop_NotFound(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleStop(ctx, domain.FrpManagerStopReq{ID: "ghost"}); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestHandleStop_RequiresHuman(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleCreate(ctx, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.handleStop(anonCtx, domain.FrpManagerStopReq{ID: "inst-0"}); err == nil {
		t.Error("expected error for anonymous role")
	}
}

// TestPersistRoundTrip_Disabled: Disabled flag must survive Save/Load along
// with the other config fields.
func TestPersistRoundTrip_Disabled(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleCreate(ctx, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleStop(ctx, domain.FrpManagerStopReq{ID: "inst-0"}); err != nil {
		t.Fatal(err)
	}
	b := &Actor{actorID: a.actorID, store: a.store}
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	if len(b.Instances) != 1 {
		t.Fatalf("expected 1 instance restored, got %d", len(b.Instances))
	}
	if !b.Instances[0].Disabled {
		t.Error("expected Disabled flag to survive Save/Load")
	}
}

// TestHandleCreate_WithWebProxy: a Create with WebProxy stores it on the
// persisted record and returns it in the response (token always stripped).
func TestHandleCreate_WithWebProxy(t *testing.T) {
	a, ctx := freshFM(t)
	req := validCreateReq()
	req.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", RemotePort: 6080}
	resp, err := a.handleCreate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config.WebProxy == nil {
		t.Fatal("expected WebProxy in response")
	}
	if resp.Config.WebProxy.Name != "sporemind-web" || resp.Config.WebProxy.RemotePort != 6080 {
		t.Errorf("unexpected WebProxy: %+v", resp.Config.WebProxy)
	}
	if !resp.Config.WebProxy.Enabled {
		t.Error("expected WebProxy.Enabled=true in response")
	}
	if a.Instances[0].WebProxy == nil {
		t.Fatal("expected WebProxy persisted on stored cfg")
	}
	if a.Instances[0].WebProxy.RemotePort != 6080 {
		t.Errorf("expected stored WebProxy.RemotePort=6080, got %d", a.Instances[0].WebProxy.RemotePort)
	}
}

// TestHandleCreate_WebProxyDeepCopied: defensive deep-copy — mutating the req
// after handleCreate must not mutate the stored cfg.
func TestHandleCreate_WebProxyDeepCopied(t *testing.T) {
	a, ctx := freshFM(t)
	req := validCreateReq()
	req.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", RemotePort: 6080}
	if _, err := a.handleCreate(ctx, req); err != nil {
		t.Fatal(err)
	}
	if a.Instances[0].WebProxy == req.WebProxy {
		t.Error("stored WebProxy must be a deep copy, not the same pointer as req.WebProxy")
	}
	req.WebProxy.RemotePort = 9999
	if a.Instances[0].WebProxy.RemotePort != 6080 {
		t.Errorf("mutating req.WebProxy must not affect stored cfg, got %d", a.Instances[0].WebProxy.RemotePort)
	}
}

// TestHandleCreate_WebProxyInvalid: a malformed WebProxy (e.g. empty name
// when enabled) must reject create and not persist.
func TestHandleCreate_WebProxyInvalid(t *testing.T) {
	a, ctx := freshFM(t)
	req := validCreateReq()
	req.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "", RemotePort: 6080}
	if _, err := a.handleCreate(ctx, req); err == nil {
		t.Error("expected error for empty webProxy name when enabled")
	}
	if len(a.Instances) != 0 {
		t.Errorf("invalid WebProxy must not persist; got %d instances", len(a.Instances))
	}
}

// TestHandleUpdate_AddsWebProxy: update can attach a WebProxy to an instance
// that didn't have one.
func TestHandleUpdate_AddsWebProxy(t *testing.T) {
	a, ctx := freshFM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	req := domain.FrpManagerUpdateReq{
		ID: created.Config.ID,
		Config: domain.FrpInstanceConfig{
			Name:       "primary",
			ServerAddr: "x:1",
			Proxies:    []domain.FrpProxy{{Name: "ssh", Kind: "tcp", LocalPort: 22, RemotePort: 6022}},
			WebProxy:   &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", RemotePort: 6080},
		},
	}
	if _, err := a.handleUpdate(ctx, req); err != nil {
		t.Fatal(err)
	}
	if a.Instances[0].WebProxy == nil {
		t.Fatal("expected WebProxy attached after update")
	}
	if a.Instances[0].WebProxy.RemotePort != 6080 {
		t.Errorf("expected stored WebProxy.RemotePort=6080, got %d", a.Instances[0].WebProxy.RemotePort)
	}
}

// TestPersistRoundTrip_WebProxy: WebProxy survives Save/Load.
func TestPersistRoundTrip_WebProxy(t *testing.T) {
	a, ctx := freshFM(t)
	req := validCreateReq()
	req.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", RemotePort: 6080}
	if _, err := a.handleCreate(ctx, req); err != nil {
		t.Fatal(err)
	}
	b := &Actor{actorID: a.actorID, store: a.store}
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	if len(b.Instances) != 1 {
		t.Fatalf("expected 1 instance restored, got %d", len(b.Instances))
	}
	if b.Instances[0].WebProxy == nil {
		t.Fatal("expected WebProxy restored after Load")
	}
	if !b.Instances[0].WebProxy.Enabled || b.Instances[0].WebProxy.RemotePort != 6080 {
		t.Errorf("expected WebProxy fields restored, got %+v", b.Instances[0].WebProxy)
	}
}

// TestHandleList_WebProxyIncluded: the Public list path surfaces WebProxy
// (it's not a secret) while still stripping the token.
func TestHandleList_WebProxyIncluded(t *testing.T) {
	a, ctx := freshFM(t)
	req := validCreateReq()
	req.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", RemotePort: 6080}
	if _, err := a.handleCreate(ctx, req); err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	if resp.Items[0].Config.WebProxy == nil {
		t.Fatal("expected WebProxy surfaced in list")
	}
	if resp.Items[0].Config.Token != "" {
		t.Errorf("list must still strip token alongside WebProxy, got %q", resp.Items[0].Config.Token)
	}
}

// TestHandleList_KeyPemStripped: the https-mode private key is a secret on the
// same footing as Token. Public list must strip it; CertPem (public material)
// stays so admins editing the entry can see what was previously stored.
func TestHandleList_KeyPemStripped(t *testing.T) {
	a, ctx := freshFM(t)
	certPem, keyPem := testutil.GenTestCert(t)
	req := validCreateReq()
	req.WebProxy = &domain.FrpWebProxy{
		Enabled:       true,
		Name:          "sporemind-web",
		Mode:          "https",
		CustomDomains: []string{"web.example.com"},
		CertPem:       certPem,
		KeyPem:        keyPem,
	}
	if _, err := a.handleCreate(ctx, req); err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	wp := resp.Items[0].Config.WebProxy
	if wp == nil {
		t.Fatal("expected WebProxy in list")
	}
	if wp.KeyPem != "" {
		t.Errorf("list must strip keyPem, got %q", wp.KeyPem)
	}
	if wp.CertPem != certPem {
		t.Errorf("list must preserve certPem (public), got %q", wp.CertPem)
	}
	if a.Instances[0].WebProxy.KeyPem != keyPem {
		t.Errorf("list must not mutate stored keyPem, got %q", a.Instances[0].WebProxy.KeyPem)
	}
}

// TestHandleGet_KeyPemStripped: same boundary as list, via the get path.
func TestHandleGet_KeyPemStripped(t *testing.T) {
	a, ctx := freshFM(t)
	certPem, keyPem := testutil.GenTestCert(t)
	req := validCreateReq()
	req.WebProxy = &domain.FrpWebProxy{
		Enabled: true, Name: "sporemind-web", Mode: "https",
		CustomDomains: []string{"web.example.com"},
		CertPem:       certPem,
		KeyPem:        keyPem,
	}
	created, err := a.handleCreate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleGet(ctx, domain.FrpManagerGetReq{ID: created.Config.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config.WebProxy == nil {
		t.Fatal("expected WebProxy in response")
	}
	if resp.Config.WebProxy.KeyPem != "" {
		t.Errorf("get must strip keyPem, got %q", resp.Config.WebProxy.KeyPem)
	}
}

// TestHandleUpdate_PreservesKeyPemWhenEmpty: edit UI can't see the stored key
// (Public list strips it). An empty KeyPem in the update request must keep the
// existing stored value, mirroring the Token-preservation rule.
func TestHandleUpdate_PreservesKeyPemWhenEmpty(t *testing.T) {
	a, ctx := freshFM(t)
	certPem, keyPem := testutil.GenTestCert(t)
	req := validCreateReq()
	req.WebProxy = &domain.FrpWebProxy{
		Enabled: true, Name: "sporemind-web", Mode: "https",
		CustomDomains: []string{"web.example.com"},
		CertPem:       certPem,
		KeyPem:        keyPem,
	}
	created, err := a.handleCreate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	update := domain.FrpManagerUpdateReq{
		ID: created.Config.ID,
		Config: domain.FrpInstanceConfig{
			Name:       "primary",
			ServerAddr: "frps.example.com:7000",
			Proxies:    []domain.FrpProxy{{Name: "ssh", Kind: "tcp", LocalPort: 22, RemotePort: 6022}},
			WebProxy: &domain.FrpWebProxy{
				Enabled:       true,
				Name:          "sporemind-web",
				Mode:          "https",
				CustomDomains: []string{"web.example.com"},
				CertPem:       "", // also blanked by the stripped list view
				KeyPem:        "", // empty — must preserve existing
			},
		},
	}
	if _, err := a.handleUpdate(ctx, update); err != nil {
		t.Fatal(err)
	}
	stored := a.Instances[0].WebProxy
	if stored == nil {
		t.Fatal("expected WebProxy still stored")
	}
	if stored.KeyPem != keyPem {
		t.Errorf("expected stored keyPem preserved, got %q", stored.KeyPem)
	}
	if stored.CertPem != certPem {
		t.Errorf("expected stored certPem preserved (UX), got %q", stored.CertPem)
	}
}

// TestHandleUpdate_AcceptsNewKeyPem: a non-empty KeyPem in the update request
// overwrites the stored key (key rotation).
func TestHandleUpdate_AcceptsNewKeyPem(t *testing.T) {
	a, ctx := freshFM(t)
	oldCert, oldKey := testutil.GenTestCert(t)
	newCert, newKey := testutil.GenTestCert(t)
	req := validCreateReq()
	req.WebProxy = &domain.FrpWebProxy{
		Enabled: true, Name: "sporemind-web", Mode: "https",
		CustomDomains: []string{"web.example.com"},
		CertPem:       oldCert,
		KeyPem:        oldKey,
	}
	created, err := a.handleCreate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	update := domain.FrpManagerUpdateReq{
		ID: created.Config.ID,
		Config: domain.FrpInstanceConfig{
			Name:       "primary",
			ServerAddr: "frps.example.com:7000",
			Proxies:    []domain.FrpProxy{{Name: "ssh", Kind: "tcp", LocalPort: 22, RemotePort: 6022}},
			WebProxy: &domain.FrpWebProxy{
				Enabled:       true,
				Name:          "sporemind-web",
				Mode:          "https",
				CustomDomains: []string{"web.example.com"},
				CertPem:       newCert,
				KeyPem:        newKey,
			},
		},
	}
	if _, err := a.handleUpdate(ctx, update); err != nil {
		t.Fatal(err)
	}
	stored := a.Instances[0].WebProxy
	if stored.KeyPem != newKey {
		t.Errorf("expected keyPem rotated, got %q", stored.KeyPem)
	}
	if stored.CertPem != newCert {
		t.Errorf("expected certPem rotated, got %q", stored.CertPem)
	}
}

// TestHandleDetect_InvalidAddr returns an error for malformed server addresses
// without attempting any network I/O.
func TestHandleDetect_InvalidAddr(t *testing.T) {
	a, ctx := freshFM(t)
	resp, err := a.handleDetect(ctx, domain.FrpManagerDetectReq{ServerAddr: "not-valid"})
	if err != nil {
		t.Fatalf("handleDetect should not error, got %v", err)
	}
	if resp.Error == "" {
		t.Error("expected Error for invalid address")
	}
	if resp.LegacyMode {
		t.Error("expected LegacyMode=false for invalid address")
	}
}

// TestHandleDetect_UnreachableTimesOut: the Public detect callable must return
// an error for an unroutable server address within the audit's 3s bound
// (OwnerLane阻塞审计2026-08-27 P2), instead of hanging on the OS-level TCP
// connect timeout. frpinstance's DialTimeout pre-flight provides the bound.
func TestHandleDetect_UnreachableTimesOut(t *testing.T) {
	a, ctx := freshFM(t)
	start := time.Now()
	resp, err := a.handleDetect(ctx, domain.FrpManagerDetectReq{
		ServerAddr: "192.0.2.1:7000", // RFC 5737 TEST-NET-1: dropped by real networks
		Token:      "probe-token",
	})
	if err != nil {
		t.Fatalf("handleDetect should report the probe error in-band, got handler error %v", err)
	}
	if resp.Error == "" {
		t.Error("expected Error for unreachable server")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("detect must return within 3s for an unreachable server, took %v", elapsed)
	}
}

// TestHandlerModes_OwnerWritePureRead pins the lane model: child-waiting
// handlers (list/get/detect/refresh) must be PureContext (stateless — off the
// owner loop), while state-mutating handlers (create/update/remove/start/stop)
// stay stateful on the owner lane. Go handler modes are inferred from the
// first parameter type, so this reflects on the method values.
func TestHandlerModes_OwnerWritePureRead(t *testing.T) {
	pureTy := reflect.TypeOf((*actor.PureContext)(nil)).Elem()
	ctxTy := reflect.TypeOf((*actor.Context)(nil)).Elem()
	pureHandlers := map[string]any{
		"frpmanager.list":    (&Actor{}).handleList,
		"frpmanager.get":     (&Actor{}).handleGet,
		"frpmanager.detect":  (&Actor{}).handleDetect,
		"frpmanager.refresh": (&Actor{}).handleRefresh,
	}
	for name, h := range pureHandlers {
		if got := reflect.TypeOf(h).In(0); got != pureTy {
			t.Errorf("%s must be PureContext (stateless, off-owner); first param is %v", name, got)
		}
	}
	statefulHandlers := map[string]any{
		"frpmanager.create": (&Actor{}).handleCreate,
		"frpmanager.update": (&Actor{}).handleUpdate,
		"frpmanager.remove": (&Actor{}).handleRemove,
		"frpmanager.start":  (&Actor{}).handleStart,
		"frpmanager.stop":   (&Actor{}).handleStop,
	}
	for name, h := range statefulHandlers {
		if got := reflect.TypeOf(h).In(0); got != ctxTy {
			t.Errorf("%s must stay stateful on the owner lane; first param is %v", name, got)
		}
	}
}

// TestMutationHandler_SchedulesRefreshNoChildWait: owner-lane mutations must
// not synchronously wait on children. handleStart schedules the async snapshot
// rebuild (fire-and-forget frpmanager.refresh self-call) and answers from the
// cached/derived view.
func TestMutationHandler_SchedulesRefreshNoChildWait(t *testing.T) {
	a, ctx := freshFM(t)
	if _, err := a.handleCreate(ctx, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var scheduled []string
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		mu.Lock()
		scheduled = append(scheduled, callID)
		mu.Unlock()
		return nil
	}
	resp, err := a.handleStart(ctx, domain.FrpManagerStartReq{ID: "inst-0"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config.ID != "inst-0" {
		t.Errorf("expected inst-0 response, got %q", resp.Config.ID)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, c := range scheduled {
		if c == "frpmanager.refresh" {
			found = true
		}
	}
	if !found {
		t.Error("expected frpmanager.refresh self-call scheduled by handleStart")
	}
}
