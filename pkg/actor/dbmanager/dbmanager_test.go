package dbmanager

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newTestActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{
		store:       persist.NewFSPersist(t.TempDir()),
		Credentials: make(map[string]dbCredential),
	}
}

func adminCtx() *testutil.FakeCtx  { return testutil.AdminCtx(testutil.GenActorID()) }
func anonCtx() *testutil.FakeCtx   { return testutil.AnonCtx(testutil.GenActorID()) }
func systemCtx() *testutil.FakeCtx { return testutil.SystemCtx(testutil.GenActorID()) }

const (
	testPassword = "s3cr3t-pass"
	testSecret   = "s3cr3t-key"
	testToken    = "bearer-tok"
)

func saveTestProfile(t *testing.T, a *Actor) domain.DbProfileView {
	t.Helper()
	resp, err := a.handleProfileSave(adminCtx(), domain.DbProfileSaveReq{
		Name:     "prod redis",
		Backend:  "redis",
		Endpoint: "10.0.0.5:6379",
		Database: "0",
		Username: "alice",
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("profile_save: %v", err)
	}
	if resp.Profile.ID == "" {
		t.Fatal("profile_save must assign an Id")
	}
	return resp.Profile
}

func TestProfileSaveListGetRoundTrip(t *testing.T) {
	a := newTestActor(t)
	view := saveTestProfile(t, a)

	if !view.HasPassword || view.HasSecret || view.HasToken {
		t.Fatalf("Has* flags = %v/%v/%v, want true/false/false", view.HasPassword, view.HasSecret, view.HasToken)
	}
	if view.Username != "alice" || view.Endpoint != "10.0.0.5:6379" {
		t.Fatalf("non-secret fields not round-tripped: %+v", view)
	}

	list, err := a.handleProfileList(adminCtx(), domain.DbProfileListReq{})
	if err != nil {
		t.Fatalf("profile_list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != view.ID {
		t.Fatalf("profile_list items = %+v", list.Items)
	}

	got, err := a.handleProfileGet(adminCtx(), domain.DbProfileGetReq{ID: view.ID})
	if err != nil {
		t.Fatalf("profile_get: %v", err)
	}
	if got.Profile.ID != view.ID || !got.Profile.HasPassword {
		t.Fatalf("profile_get = %+v", got.Profile)
	}

	// The credential red line: no callable response may carry the secret.
	data, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testPassword, testSecret, testToken} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("masked response leaks secret %q: %s", secret, data)
		}
	}
}

func TestProfileSaveUpdateKeepsCredentialWhenSecretsEmpty(t *testing.T) {
	a := newTestActor(t)
	view := saveTestProfile(t, a)

	updated, err := a.handleProfileSave(adminCtx(), domain.DbProfileSaveReq{
		ID:       view.ID,
		Name:     "renamed",
		Backend:  "redis",
		Endpoint: "10.0.0.6:6379",
		Username: "alice",
	})
	if err != nil {
		t.Fatalf("profile_save update: %v", err)
	}
	if updated.Profile.Name != "renamed" || !updated.Profile.HasPassword {
		t.Fatalf("update lost credential or name: %+v", updated.Profile)
	}

	resolved, err := a.handleProfileResolve(systemCtx(), domain.DbProfileResolveReq{ID: view.ID})
	if err != nil {
		t.Fatalf("profile_resolve: %v", err)
	}
	if resolved.Password != testPassword || resolved.Username != "alice" {
		t.Fatalf("credential not kept across secret-less update: %+v", resolved)
	}
}

func TestProfileSaveUpdateReplacesCredential(t *testing.T) {
	a := newTestActor(t)
	view := saveTestProfile(t, a)

	if _, err := a.handleProfileSave(adminCtx(), domain.DbProfileSaveReq{
		ID:    view.ID,
		Name:  "renamed",
		Backend: "redis",
		Token: testToken,
	}); err != nil {
		t.Fatalf("profile_save update: %v", err)
	}

	resolved, err := a.handleProfileResolve(systemCtx(), domain.DbProfileResolveReq{ID: view.ID})
	if err != nil {
		t.Fatalf("profile_resolve: %v", err)
	}
	if resolved.Token != testToken {
		t.Fatalf("token not applied: %+v", resolved)
	}
	if resolved.Password != "" {
		t.Fatalf("password must be replaced, not merged: %+v", resolved)
	}
}

func TestProfileRemoveDeletesCredential(t *testing.T) {
	a := newTestActor(t)
	view := saveTestProfile(t, a)

	if _, err := a.handleProfileRemove(adminCtx(), domain.DbProfileRemoveReq{ID: view.ID}); err != nil {
		t.Fatalf("profile_remove: %v", err)
	}
	if _, err := a.handleProfileGet(adminCtx(), domain.DbProfileGetReq{ID: view.ID}); err == nil {
		t.Fatal("profile_get after remove must fail")
	}
	if _, err := a.handleProfileResolve(systemCtx(), domain.DbProfileResolveReq{ID: view.ID}); err == nil {
		t.Fatal("profile_resolve after remove must fail")
	}
	if _, err := a.handleProfileRemove(adminCtx(), domain.DbProfileRemoveReq{ID: view.ID}); err == nil {
		t.Fatal("double remove must fail")
	}
}

func TestAdminCRUDRejectsUnauthorizedRoles(t *testing.T) {
	a := newTestActor(t)
	ctx := anonCtx()
	if _, err := a.handleProfileSave(ctx, domain.DbProfileSaveReq{Name: "x", Backend: "redis"}); err == nil {
		t.Fatal("anon profile_save must be rejected")
	}
	if _, err := a.handleProfileList(ctx, domain.DbProfileListReq{}); err == nil {
		t.Fatal("anon profile_list must be rejected")
	}
	if _, err := a.handleProfileGet(ctx, domain.DbProfileGetReq{ID: "p1"}); err == nil {
		t.Fatal("anon profile_get must be rejected")
	}
	if _, err := a.handleProfileRemove(ctx, domain.DbProfileRemoveReq{ID: "p1"}); err == nil {
		t.Fatal("anon profile_remove must be rejected")
	}
	// The system role (peer actors) is not admin either.
	if _, err := a.handleProfileSave(systemCtx(), domain.DbProfileSaveReq{Name: "x", Backend: "redis"}); err == nil {
		t.Fatal("system-role profile_save must be rejected")
	}
}

func TestProfileResolveReturnsFullCredential(t *testing.T) {
	a := newTestActor(t)
	view := saveTestProfile(t, a)

	resolved, err := a.handleProfileResolve(systemCtx(), domain.DbProfileResolveReq{ID: view.ID})
	if err != nil {
		t.Fatalf("profile_resolve: %v", err)
	}
	if resolved.Username != "alice" || resolved.Password != testPassword {
		t.Fatalf("resolve = %+v", resolved)
	}

	if _, err := a.handleProfileResolve(systemCtx(), domain.DbProfileResolveReq{ID: "nope"}); err == nil {
		t.Fatal("resolve of unknown profile must fail")
	}
}

// TestProfileLookupReturnsNonSecretHalf is the peer-actor analog of the
// resolve contract: profile_lookup must return the non-secret dial fields
// without ever leaking Password/Secret/Token; the credential still resolves
// at dial time via the process-wide resolver.
func TestProfileLookupReturnsNonSecretHalf(t *testing.T) {
	a := newTestActor(t)
	view := saveTestProfile(t, a)

	resp, err := a.handleProfileLookup(systemCtx(), domain.DbProfileLookupReq{ID: view.ID})
	if err != nil {
		t.Fatalf("profile_lookup: %v", err)
	}
	if resp.Profile.ID != view.ID || resp.Profile.Backend != "redis" || resp.Profile.Endpoint != "10.0.0.5:6379" {
		t.Fatalf("non-secret fields not round-tripped: %+v", resp.Profile)
	}
	if resp.Profile.Username != "alice" {
		t.Fatalf("Username (non-secret) must be in lookup response: %+v", resp.Profile)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testPassword, testSecret, testToken} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("lookup response leaks secret %q: %s", secret, data)
		}
	}

	if _, err := a.handleProfileLookup(systemCtx(), domain.DbProfileLookupReq{ID: "nope"}); err == nil {
		t.Fatal("lookup of unknown profile must fail")
	}
}

func TestStatePersistsAcrossRestart(t *testing.T) {
	a := newTestActor(t)
	view := saveTestProfile(t, a)

	b := &Actor{store: a.store, Credentials: make(map[string]dbCredential)}
	if err := b.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	resolved, err := b.handleProfileResolve(systemCtx(), domain.DbProfileResolveReq{ID: view.ID})
	if err != nil {
		t.Fatalf("resolve after reload: %v", err)
	}
	if resolved.Password != testPassword {
		t.Fatalf("credential not persisted: %+v", resolved)
	}
	list, err := b.handleProfileList(adminCtx(), domain.DbProfileListReq{})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("list after reload: %v %+v", err, list.Items)
	}
}

// TestOnStartInstallsPersistResolver is the dial-time contract end to end:
// after OnStart, persist.ResolveCredential resolves the actor's profiles to
// backend-agnostic credentials, unknown refs are explicit errors, and a
// credential rotation is visible without any restart of the consumer.
func TestOnStartInstallsPersistResolver(t *testing.T) {
	t.Cleanup(func() { persist.SetCredentialResolver(nil) })
	a := newTestActor(t)
	view := saveTestProfile(t, a)

	if err := a.OnStart(testutil.SystemCtx(testutil.GenActorID())); err != nil {
		t.Fatalf("OnStart: %v", err)
	}

	c, err := persist.ResolveCredential(view.ID)
	if err != nil {
		t.Fatalf("ResolveCredential: %v", err)
	}
	if c.Username != "alice" || c.Password != testPassword {
		t.Fatalf("resolved credential = %+v", c)
	}

	if _, err := persist.ResolveCredential("unknown-profile"); err == nil {
		t.Fatal("unknown ref must be an explicit error, not a silent empty credential")
	}

	// Rotation: rewrite the credential; the next dial-time resolution sees
	// it immediately (the resolver never caches).
	if _, err := a.handleProfileSave(adminCtx(), domain.DbProfileSaveReq{
		ID: view.ID, Name: "renamed", Backend: "redis", Password: "rotated",
	}); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	c, err = persist.ResolveCredential(view.ID)
	if err != nil || c.Password != "rotated" {
		t.Fatalf("rotation not visible at dial time: %+v %v", c, err)
	}
}
