package aiaggregator

import (
	"context"
	"testing"

	gosporeactor "github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// exposeCtx is a minimal actor.Context stub that records RegisterDomain calls.
type exposeCtx struct {
	gosporeactor.Context
	logger  noopLogger
	domains []string
}

func (c *exposeCtx) Logger() gosporeactor.Logger { return c.logger }
func (c *exposeCtx) Lifecycle() context.Context { return context.Background() }

func (c *exposeCtx) RegisterDomain(name string) *gosporeactor.DomainHandle {
	c.domains = append(c.domains, name)
	return gosporeactor.NewDomainHandle(name, func(string) error { return nil }, func(string) error { return nil })
}

func TestApplyResolvedConfig_SystemInstanceExposesService(t *testing.T) {
	ctx := &exposeCtx{}
	a := &Actor{id: "actor-ulid", actorCtx: ctx}
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		ID:   systemAggregatorID,
		Name: "Auto",
		Units: []domain.ManualCallableUnit{
			{Model: "gpt-4o", ProviderName: "openai", Endpoint: "https://api.openai.com", Protocol: "openai"},
		},
	})
	if len(ctx.domains) != 1 || ctx.domains[0] != "aiaggregator" {
		t.Fatalf("expected aiaggregator domain exposed once, got %v", ctx.domains)
	}
}

func TestApplyResolvedConfig_CustomInstanceDoesNotExposeService(t *testing.T) {
	ctx := &exposeCtx{}
	a := &Actor{id: "actor-ulid", actorCtx: ctx}
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		ID:   "custom-agg",
		Name: "Custom",
		Units: []domain.ManualCallableUnit{
			{Model: "gpt-4o", ProviderName: "openai", Endpoint: "https://api.openai.com", Protocol: "openai"},
		},
	})
	if len(ctx.domains) != 0 {
		t.Fatalf("expected no domain expose for custom aggregator, got %v", ctx.domains)
	}
}

func TestApplyResolvedConfig_SystemExposeIsIdempotent(t *testing.T) {
	ctx := &exposeCtx{}
	a := &Actor{id: "actor-ulid", actorCtx: ctx}
	resp := domain.AIManagerAggregatorResolveResp{
		ID:   systemAggregatorID,
		Name: "Auto",
		Units: []domain.ManualCallableUnit{
			{Model: "gpt-4o", ProviderName: "openai", Endpoint: "https://api.openai.com", Protocol: "openai"},
		},
	}
	a.applyResolvedConfig(resp)
	a.applyResolvedConfig(resp)
	if len(ctx.domains) != 1 {
		t.Fatalf("expected exactly one aiaggregator expose, got %d calls", len(ctx.domains))
	}
}
