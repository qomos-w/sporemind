package media

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestVisionBindingLifecycle locks the vision kind: provider-model binding
// only, surfaced through list/active views, fully independent of the image and
// video kinds, with account operations rejected.
// TestVisionAggregatorBinding: the vision kind can bind a named aggregator
// config instead of a (Provider, Model) pair; the binding is surfaced through
// list/active responses and is mutually exclusive with the provider-model
// binding.
func TestVisionAggregatorBinding(t *testing.T) {
	a := newTestActor(t)

	// Aggregator binding: set, surfaced, mutual exclusion with the
	// provider-model binding, clear.
	setResp, err := a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "vision", Aggregator: "my-vision-pool"})
	if err != nil {
		t.Fatal(err)
	}
	if setResp.Aggregator != "my-vision-pool" {
		t.Fatalf("aggregator binding not echoed: %+v", setResp)
	}
	listResp, err := a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "vision"})
	if err != nil {
		t.Fatal(err)
	}
	if listResp.BoundAggregator != "my-vision-pool" || listResp.BoundProvider != "" {
		t.Fatalf("aggregator binding not surfaced: %+v", listResp)
	}
	actResp, err := a.handleActiveAccount(nil, gen.MediaActiveAccountReq{Kind: "vision"})
	if err != nil {
		t.Fatal(err)
	}
	if actResp.BoundAggregator != "my-vision-pool" {
		t.Fatalf("aggregator binding not served to aggregator pin lookup: %+v", actResp)
	}

	// Provider-model binding takes over and clears the aggregator binding.
	if _, err = a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "vision", Provider: "v", Model: "gpt-4o"}); err != nil {
		t.Fatal(err)
	}
	if listResp, err = a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "vision"}); err != nil {
		t.Fatal(err)
	}
	if listResp.BoundAggregator != "" || listResp.BoundModel != "gpt-4o" {
		t.Fatalf("provider-model binding should replace aggregator binding: %+v", listResp)
	}

	// Aggregator binding is vision-only.
	if _, err = a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "image", Aggregator: "x"}); err == nil {
		t.Fatal("aggregator binding must be rejected for image kind")
	}
	// Aggregator and provider-model are mutually exclusive in one request.
	if _, err = a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "vision", Aggregator: "x", Provider: "v", Model: "gpt-4o"}); err == nil {
		t.Fatal("aggregator + provider-model must be rejected together")
	}

	// Empty set clears everything.
	if _, err = a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "vision"}); err != nil {
		t.Fatal(err)
	}
	if listResp, err = a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "vision"}); err != nil {
		t.Fatal(err)
	}
	if listResp.BoundAggregator != "" || listResp.BoundModel != "" {
		t.Fatalf("empty set should clear the binding: %+v", listResp)
	}
}

func TestVisionBindingLifecycle(t *testing.T) {
	a := newTestActor(t)

	// Nothing configured → active_account errors.
	if _, err := a.handleActiveAccount(nil, gen.MediaActiveAccountReq{Kind: "vision"}); err == nil {
		t.Fatal("expected error with nothing configured")
	}

	// Bind a vision model.
	set, err := a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "vision", Provider: "openai", Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	if set.Kind != "vision" || set.Provider != "openai" || set.Model != "gpt-4o" {
		t.Fatalf("unexpected set response: %+v", set)
	}

	// active_account surfaces the binding; the active IMAGE account must not
	// leak into the vision kind.
	imgAcc := createAccount(t, a, "image", "img", "key1")
	active, err := a.handleActiveAccount(nil, gen.MediaActiveAccountReq{Kind: "vision"})
	if err != nil {
		t.Fatal(err)
	}
	if active.BoundProvider != "openai" || active.BoundModel != "gpt-4o" {
		t.Fatalf("active response lost vision binding: %+v", active)
	}
	if active.Account.Kind != "" {
		t.Fatalf("vision must never resolve an account, got %+v", active.Account)
	}

	// list projects the binding; no vision accounts exist.
	list, err := a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "vision"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 || list.BoundProvider != "openai" || list.BoundModel != "gpt-4o" {
		t.Fatalf("unexpected vision list: %+v", list)
	}

	// Vision binding never disturbs the image kind's active account or binding.
	if list, err = a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "image"}); err != nil {
		t.Fatal(err)
	}
	if list.ActiveID != imgAcc.Account.ID || list.BoundProvider != "" {
		t.Fatalf("vision binding leaked into image kind: %+v", list)
	}

	// Account operations on the vision kind are rejected.
	if _, err := a.handleAccountCreate(nil, gen.MediaAccountCreateReq{Kind: "vision", Name: "n", Provider: "p"}); err == nil {
		t.Fatal("expected error creating a vision account")
	}
	if _, err := a.handleAccountActivate(nil, gen.MediaAccountActivateReq{Kind: "vision", ID: "ma_x"}); err == nil {
		t.Fatal("expected error activating a vision account")
	}

	// Clearing the vision binding empties it without touching other kinds.
	cleared, err := a.handleProviderModelSet(nil, gen.MediaProviderModelSetReq{Kind: "vision"})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Provider != "" || cleared.Model != "" {
		t.Fatalf("clear should empty the vision binding: %+v", cleared)
	}
	if _, err := a.handleActiveAccount(nil, gen.MediaActiveAccountReq{Kind: "vision"}); err == nil {
		t.Fatal("expected error after clearing the vision binding")
	}
	if list, err = a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "image"}); err != nil {
		t.Fatal(err)
	}
	if list.ActiveID != imgAcc.Account.ID {
		t.Fatalf("clearing vision binding disturbed image kind: %+v", list)
	}
}
