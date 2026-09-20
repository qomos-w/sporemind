package media

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestProviderModelBindingLifecycle locks the media-page provider-model
// selection: set, project in list/active views, mutual exclusion with the
// active account, and clear.
func TestProviderModelBindingLifecycle(t *testing.T) {
	a := newTestActor(t)

	// No account, no binding → active_account errors (agent falls back to
	// first-match aggregator resolve).
	if _, err := a.handleActiveAccount(nil, gen.MediaActiveAccountReq{Kind: "video"}); err == nil {
		t.Fatal("expected error with nothing configured")
	}

	// Bind visioncoder/seedance2.5.
	set, err := a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "video", Provider: "visioncoder", Model: "seedance2.5"})
	if err != nil {
		t.Fatal(err)
	}
	if set.Provider != "visioncoder" || set.Model != "seedance2.5" {
		t.Fatalf("unexpected set response: %+v", set)
	}

	// active_account surfaces the binding instead of erroring.
	active, err := a.handleActiveAccount(nil, gen.MediaActiveAccountReq{Kind: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if active.BoundProvider != "visioncoder" || active.BoundModel != "seedance2.5" {
		t.Fatalf("active response lost binding: %+v", active)
	}
	if active.Account.Kind != "" {
		t.Fatalf("active response should carry no account: %+v", active.Account)
	}

	// list projects the binding too.
	list, err := a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if list.BoundProvider != "visioncoder" || list.BoundModel != "seedance2.5" {
		t.Fatalf("list response lost binding: %+v", list)
	}

	// Creating an account auto-activates it and takes over (mutual exclusion).
	created := createAccount(t, a, "video", "direct", "key1")
	list, err = a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if list.BoundProvider != "" || list.ActiveID != created.Account.ID {
		t.Fatalf("account create should clear the binding: %+v", list)
	}

	// Re-binding deactivates the account (mutual exclusion, other direction).
	if _, err := a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "video", Provider: "nbw", Model: "gpt-image-2"}); err != nil {
		t.Fatal(err)
	}
	list, err = a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if list.ActiveID != "" || list.BoundProvider != "nbw" || list.BoundModel != "gpt-image-2" {
		t.Fatalf("binding should clear the active account: %+v", list)
	}

	// Re-activating the account clears the binding.
	if _, err := a.handleAccountActivate(nil, gen.MediaAccountActivateReq{Kind: "video", ID: created.Account.ID}); err != nil {
		t.Fatal(err)
	}
	list, err = a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if list.BoundProvider != "" || list.ActiveID != created.Account.ID {
		t.Fatalf("activation should clear the binding: %+v", list)
	}

	// Clearing the binding (empty Provider/Model) leaves accounts untouched.
	if _, err := a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "image", Provider: "p", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	cleared, err := a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Provider != "" || cleared.Model != "" {
		t.Fatalf("clear should empty the binding: %+v", cleared)
	}

	// Kinds are independent: image binding must not affect video state.
	list, err = a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if list.ActiveID != created.Account.ID {
		t.Fatalf("image binding ops leaked into video kind: %+v", list)
	}
}

// TestProviderModelSetValidation locks the request validation: Provider and
// Model must arrive together, and the Kind must be known.
func TestProviderModelSetValidation(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "image", Provider: "p"}); err == nil {
		t.Fatal("expected error for provider without model")
	}
	if _, err := a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "audio", Provider: "p", Model: "m"}); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}
