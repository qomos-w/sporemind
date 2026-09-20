package aimanager

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHandleProviderConfigure_Rename pins the rename semantic of
// provider_configure: with PreviousName set, the existing provider is updated
// in place (new Name, preserved AuthToken) and aggregator units referencing
// the old name are rewritten — instead of a duplicate entry being appended.
func TestHandleProviderConfigure_Rename(t *testing.T) {
	newActor := func() *Actor {
		return &Actor{
			actorID: "rename-test",
			store:   persist.NewFSPersist(t.TempDir()),
			Providers: []domain.Provider{
				{
					Name:      "openai",
					Kind:      "openai",
					Endpoint:  "https://api.openai.com",
					AuthToken: "tok-secret",
					Models:    []domain.ProviderModel{{Name: "gpt-test", MaxContextLength: 8192}},
				},
			},
			aggregators: map[string]domain.AIManagerAggregatorGetResp{
				"agg1": {
					ID:   "agg1",
					Name: "agg1",
					Units: []domain.ManualCallableUnit{
						{Model: "gpt-test", ProviderName: "openai", Endpoint: "https://api.openai.com", Protocol: "openai"},
					},
				},
			},
			aggRefs:         make(map[string]ref.Ref),
			aggActorIDs:     make(map[string]string),
			persistedHealth: make(map[string]persistedHealthEntry),
			assignments:     make(map[string]string),
		}
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())

	t.Run("rename updates in place and rewrites aggregator units", func(t *testing.T) {
		a := newActor()
		_, err := a.handleProviderConfigure(ctx, domain.AIManagerProviderConfigureReq{
			Name:         "openai-renamed",
			PreviousName: "openai",
			Kind:         "openai",
			Endpoint:     "https://api.openai.com",
			Models:       []domain.ProviderModel{{Name: "gpt-test", MaxContextLength: 8192}},
		})
		if err != nil {
			t.Fatalf("configure rename failed: %v", err)
		}
		if len(a.Providers) != 1 {
			t.Fatalf("expected 1 provider after rename, got %d: %+v", len(a.Providers), a.Providers)
		}
		p := a.Providers[0]
		if p.Name != "openai-renamed" {
			t.Errorf("provider name = %q, want openai-renamed", p.Name)
		}
		if p.AuthToken != "tok-secret" {
			t.Errorf("auth token not preserved across rename: %q", p.AuthToken)
		}
		units := a.aggregators["agg1"].Units
		if len(units) != 1 || units[0].ProviderName != "openai-renamed" {
			t.Errorf("aggregator unit not rewritten: %+v", units)
		}
	})

	t.Run("rename to existing name is rejected", func(t *testing.T) {
		a := newActor()
		a.Providers = append(a.Providers, domain.Provider{Name: "taken", Kind: "openai"})
		_, err := a.handleProviderConfigure(ctx, domain.AIManagerProviderConfigureReq{
			Name:         "taken",
			PreviousName: "openai",
			Kind:         "openai",
			Endpoint:     "https://api.openai.com",
			Models:       []domain.ProviderModel{{Name: "gpt-test"}},
		})
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("expected collision error, got %v", err)
		}
		if len(a.Providers) != 2 {
			t.Fatalf("collision rename mutated providers: %+v", a.Providers)
		}
	})

	t.Run("rename of unknown provider is rejected without append", func(t *testing.T) {
		a := newActor()
		_, err := a.handleProviderConfigure(ctx, domain.AIManagerProviderConfigureReq{
			Name:         "ghost-renamed",
			PreviousName: "ghost",
			Kind:         "openai",
			Endpoint:     "https://api.openai.com",
			Models:       []domain.ProviderModel{{Name: "gpt-test"}},
		})
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected not-found error, got %v", err)
		}
		if len(a.Providers) != 1 {
			t.Fatalf("unknown rename appended a provider: %+v", a.Providers)
		}
	})
}
