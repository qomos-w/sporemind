package im

import (
	"encoding/json"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

func newTestActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "im-test"}
}

func TestAccountViewRedactsToken(t *testing.T) {
	view := accountView(gen.ImAccount{ID: "ia_1", Name: "mybot", Provider: "telegram", Token: "secret", Enabled: true})
	if !view.HasToken || view.Name != "mybot" || view.Provider != "telegram" || !view.Enabled {
		t.Fatalf("unexpected account view: %+v", view)
	}
}

func TestAccountCRUDRoundTrip(t *testing.T) {
	a := newTestActor(t)

	created, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{
		Name: "mybot", Provider: "telegram", Token: "secret-token", AllowUsers: []string{"10001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Account.ID == "" || !created.Account.HasToken || !created.Account.Enabled {
		t.Fatalf("unexpected create response: %+v", created.Account)
	}

	list, err := a.handleAccountList(nil, gen.ImAccountListReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || !list.Items[0].HasToken {
		t.Fatalf("unexpected list response: %+v", list.Items)
	}
	// Serialized projections must not leak the token.
	blob, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "secret-token") {
		t.Fatalf("list response leaked token: %s", blob)
	}
	// The token itself is persisted server-side.
	st, err := a.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Accounts) != 1 || st.Accounts[0].Token != "secret-token" {
		t.Fatalf("unexpected stored account: %+v", st.Accounts)
	}

	// Empty Token preserves the stored secret (redacted edit flow).
	updated, err := a.handleAccountUpdate(nil, gen.ImAccountUpdateReq{ID: created.Account.ID, Name: "renamed"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Account.Name != "renamed" || !updated.Account.HasToken {
		t.Fatalf("unexpected update response: %+v", updated.Account)
	}
	st, err = a.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	if st.Accounts[0].Token != "secret-token" || st.Accounts[0].Name != "renamed" {
		t.Fatalf("update lost stored fields: %+v", st.Accounts[0])
	}

	// Non-empty Token overwrites the stored secret.
	if _, err := a.handleAccountUpdate(nil, gen.ImAccountUpdateReq{ID: created.Account.ID, Token: "new-token"}); err != nil {
		t.Fatal(err)
	}
	st, err = a.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	if st.Accounts[0].Token != "new-token" {
		t.Fatalf("expected token 'new-token', got %q", st.Accounts[0].Token)
	}

	// Delete removes the account.
	if _, err := a.handleAccountDelete(nil, gen.ImAccountDeleteReq{ID: created.Account.ID}); err != nil {
		t.Fatal(err)
	}
	list, err = a.handleAccountList(nil, gen.ImAccountListReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("expected empty list after delete, got %+v", list.Items)
	}
}

func TestAccountCreateRequiresFields(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Provider: "telegram", Token: "t"}); err == nil {
		t.Fatal("expected error for missing name")
	}
	if _, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "n", Token: "t"}); err == nil {
		t.Fatal("expected error for missing provider")
	}
	if _, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "n", Provider: "telegram"}); err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestAccountUpdateUnknownFails(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountUpdate(nil, gen.ImAccountUpdateReq{ID: "nope", Name: "x"}); err == nil {
		t.Fatal("expected error updating unknown account")
	}
}

func TestAccountDeleteUnknownFails(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountDelete(nil, gen.ImAccountDeleteReq{ID: "nope"}); err == nil {
		t.Fatal("expected error deleting unknown account")
	}
}

func TestStatusReportsDisconnectedUntilAdaptersLand(t *testing.T) {
	a := newTestActor(t)
	created, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "mybot", Provider: "telegram", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	// Seed a disabled account directly to cover the enabled-only branch.
	st, err := a.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	st.Accounts = append(st.Accounts, gen.ImAccount{ID: "ia_off", Name: "off", Provider: "telegram", Token: "t2", Enabled: false})
	if err := a.saveStore(st); err != nil {
		t.Fatal(err)
	}

	resp, err := a.handleStatus(nil, gen.ImStatusReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 status items, got %d", len(resp.Items))
	}
	byID := map[string]gen.ImAccountView{}
	for _, item := range resp.Items {
		byID[item.ID] = item
	}
	if byID[created.Account.ID].Status != statusDisconnected {
		t.Fatalf("enabled account should report %q, got %q", statusDisconnected, byID[created.Account.ID].Status)
	}
	if byID["ia_off"].Status != "" {
		t.Fatalf("disabled account should omit status, got %q", byID["ia_off"].Status)
	}
}

func TestSendStubReturnsNotImplemented(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleSend(nil, gen.ImSendReq{AccountID: "ia_1", ChatID: "42", Text: "hi"}); err == nil {
		t.Fatal("expected send stub to report not-implemented")
	}
	if _, err := a.handleSend(nil, gen.ImSendReq{AccountID: "", ChatID: "42", Text: "hi"}); err == nil {
		t.Fatal("expected send stub to validate required fields")
	}
}
