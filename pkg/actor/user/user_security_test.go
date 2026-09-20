package user

import (
	"reflect"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestListAccountsRejectsAnonymousAndReturnsRedactedView(t *testing.T) {
	a := &Actor{Accounts: []gen.Account{{ID: "user-1", Username: "alice", PasswordHash: "secret-hash", Roles: []string{"viewer"}, Status: "active"}}}
	if _, err := a.handleListAccounts(testutil.AnonCtx(testutil.GenActorID())); err == nil {
		t.Fatal("anonymous account listing must be rejected")
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.Identity_.Subject = "user-1"
	resp, err := a.handleListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Username != "alice" {
		t.Fatalf("account view = %+v", resp.Items)
	}
	if _, ok := reflect.TypeOf(resp.Items[0]).FieldByName("PasswordHash"); ok {
		t.Fatal("list response must not expose PasswordHash")
	}
}

func TestAccountsComponentIsNotPublic(t *testing.T) {
	field, ok := reflect.TypeOf(Actor{}).FieldByName("Accounts")
	if !ok {
		t.Fatal("Accounts field missing")
	}
	if got := field.Tag.Get("gospore"); got != "component,admin" {
		t.Fatalf("Accounts visibility = %q, want component,admin", got)
	}
}

func TestAccountViewDoesNotExposePasswordHash(t *testing.T) {
	account := gen.Account{ID: "user-1", Username: "alice", PasswordHash: "secret-hash", Roles: []string{"viewer"}}
	view := toAccountView(account)
	if view.ID != account.ID || view.Username != account.Username {
		t.Fatalf("unexpected account view: %+v", view)
	}
	if _, ok := reflect.TypeOf(view).FieldByName("PasswordHash"); ok {
		t.Fatal("AccountView must not expose PasswordHash")
	}
}

func TestUpdateBuiltinAdminLocksNameRolesAndGroups(t *testing.T) {
	a := &Actor{
		store:   persist.NewFSPersist(t.TempDir()),
		actorID: "user-test",
		Accounts: []gen.Account{
			{ID: "admin", Username: "admin", DisplayName: "Administrator", Roles: []string{"admin"}, Groups: []string{"admins"}, Status: "active"},
			{ID: "u1", Username: "bob", DisplayName: "Bob", Roles: []string{"viewer"}, Groups: []string{"viewers"}, Status: "active"},
		},
	}
	adminCtx := testutil.HumanCtx(testutil.GenActorID())
	adminCtx.Identity_.Subject = "admin"
	adminCtx.Identity_.Role = "admin"

	rejected := []gen.AccountUpdateReq{
		{ID: "admin", DisplayName: "Root"},
		{ID: "admin", Roles: []string{"viewer"}},
		{ID: "admin", Groups: []string{"developers"}},
	}
	for _, req := range rejected {
		if _, err := a.handleUpdateAccount(adminCtx, req); err == nil {
			t.Fatalf("update %v must be rejected for built-in admin", req)
		}
	}

	view, err := a.handleUpdateAccount(adminCtx, gen.AccountUpdateReq{
		ID:          "admin",
		DisplayName: "Administrator",
		Roles:       []string{"admin"},
		Groups:      []string{"admins"},
		Status:      "disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.DisplayName != "Administrator" || view.Roles[0] != "admin" || view.Groups[0] != "admins" || view.Status != "disabled" {
		t.Fatalf("unexpected view: %+v", view)
	}

	// Regular accounts keep full admin editing.
	if _, err := a.handleUpdateAccount(adminCtx, gen.AccountUpdateReq{ID: "u1", DisplayName: "Bobby", Groups: []string{"developers"}}); err != nil {
		t.Fatalf("regular account update rejected: %v", err)
	}
}
