package aimanager

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newProxyTestActor() *Actor {
	return &Actor{
		actorID: "test-aimanager",
		store:   &memStore{data: make(map[string][]byte)},
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"agg-1": {
				ID:   "agg-1",
				Name: "pool",
				Units: []domain.ManualCallableUnit{
					{Model: "gpt-4o", Endpoint: "https://openai", ProviderName: "openai", Protocol: "openai"},
				},
			},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
}

func TestProviderConfigure_ProxyRoundtrip(t *testing.T) {
	a := newProxyTestActor()
	ctx := testutil.AdminCtx(testutil.GenActorID())

	if _, err := a.handleProviderConfigure(ctx, domain.AIManagerProviderConfigureReq{
		Name:     "openai",
		Kind:     "openai",
		Endpoint: "https://openai",
		Models:   []domain.ProviderModel{{Name: "gpt-4o"}},
		Proxy:    "http://127.0.0.1:7890",
	}); err != nil {
		t.Fatalf("configure with proxy failed: %v", err)
	}
	if a.Providers[0].Proxy != "http://127.0.0.1:7890" {
		t.Fatalf("provider proxy = %q, want http://127.0.0.1:7890", a.Providers[0].Proxy)
	}
	unit := a.aggregators["agg-1"].Units[0]
	if unit.Proxy != "http://127.0.0.1:7890" {
		t.Fatalf("aggregator unit proxy = %q, want http://127.0.0.1:7890", unit.Proxy)
	}

	// A provider-only update that omits Proxy must clear it (explicit empty
	// is the editor's way of removing the proxy).
	if _, err := a.handleProviderConfigure(ctx, domain.AIManagerProviderConfigureReq{
		Name:     "openai",
		Kind:     "openai",
		Endpoint: "https://openai",
		Models:   []domain.ProviderModel{{Name: "gpt-4o"}},
	}); err != nil {
		t.Fatalf("configure without proxy failed: %v", err)
	}
	if a.Providers[0].Proxy != "" {
		t.Fatalf("provider proxy = %q, want cleared", a.Providers[0].Proxy)
	}
	if u := a.aggregators["agg-1"].Units[0]; u.Proxy != "" {
		t.Fatalf("aggregator unit proxy = %q, want cleared", u.Proxy)
	}
}

func TestProviderConfigure_ProxyOnlyIsNotDeleteSignal(t *testing.T) {
	a := newProxyTestActor()
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// A request carrying ONLY the proxy must not delete the provider; it
	// updates the proxy on an otherwise-empty request.
	if _, err := a.handleProviderConfigure(ctx, domain.AIManagerProviderConfigureReq{
		Name:  "openai",
		Proxy: "socks5://127.0.0.1:1080",
	}); err != nil {
		t.Fatalf("configure proxy-only failed: %v", err)
	}
	if len(a.Providers) != 1 || a.Providers[0].Name != "openai" {
		t.Fatalf("provider must survive proxy-only update, got %+v", a.Providers)
	}
	if a.Providers[0].Proxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("provider proxy = %q, want socks5://127.0.0.1:1080", a.Providers[0].Proxy)
	}
}

func TestHandleProviderList_ExposesProxy(t *testing.T) {
	a := newProxyTestActor()
	a.Providers[0].Proxy = "http://proxy:3128"

	resp, err := a.handleProviderList(nil)
	if err != nil {
		t.Fatalf("provider list failed: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Proxy != "http://proxy:3128" {
		t.Fatalf("list proxy = %+v, want http://proxy:3128", resp.Items)
	}
}

func TestHandleProviderResolveModel_CarriesProxy(t *testing.T) {
	a := newProxyTestActor()
	a.Providers[0].Proxy = "http://proxy:3128"

	resp, err := a.handleProviderResolveModel(nil, domain.AIManagerProviderResolveModelReq{Name: "openai", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("resolve model failed: %v", err)
	}
	if resp.Proxy != "http://proxy:3128" {
		t.Fatalf("resolve model proxy = %q, want http://proxy:3128", resp.Proxy)
	}
}
