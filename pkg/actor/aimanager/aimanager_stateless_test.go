package aimanager

import (
	"reflect"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// pureContextType mirrors gospore's ClassifyHandlerMode rule: a Go handler's
// mode is inferred from an exact first-parameter match on actor.PureContext
// (gospore/internal/handler/classify.go). The tests below pin that contract so
// a future regression back to actor.Context (which would silently re-serialize
// the handler onto the owner lane and reintroduce mailbox blocking) fails
// loudly instead of shipping.
var pureContextType = reflect.TypeOf((*actor.PureContext)(nil)).Elem()

// TestConvertedHandlers_PureContextSignature pins the 2026-08-27 stateless
// conversion: all nine formerly stateful handlers take actor.PureContext.
func TestConvertedHandlers_PureContextSignature(t *testing.T) {
	a := &Actor{}
	cases := map[string]any{
		"handleProviderResetHealth":   a.handleProviderResetHealth,
		"handleProviderRecordProbe":   a.handleProviderRecordProbe,
		"handleProviderConfigure":     a.handleProviderConfigure,
		"handleProviderSetTokenPlan":  a.handleProviderSetTokenPlan,
		"handleProviderSetDisabled":   a.handleProviderSetDisabled,
		"handleAggregatorConfigure":   a.handleAggregatorConfigure,
		"handleAggregatorSetDisabled": a.handleAggregatorSetDisabled,
		"handleConfigImport":          a.handleConfigImport,
		"handleModelDefaultsSet":      a.handleModelDefaultsSet,
	}
	if len(cases) != 9 {
		t.Fatalf("expected 9 converted handlers, got %d", len(cases))
	}
	for name, fn := range cases {
		ft := reflect.TypeOf(fn)
		if ft.NumIn() < 1 {
			t.Errorf("%s: no context parameter", name)
			continue
		}
		if got := ft.In(0); got != pureContextType {
			t.Errorf("%s: first parameter = %v, want actor.PureContext", name, got)
		}
	}
}

// TestStatelessCallables_NoLoopPinning verifies the registration surface of
// the converted callables: none may declare a WithLoop option. A named loop
// would enqueue a stateless handler onto that lane and re-serialize it,
// defeating the forked-goroutine dispatch; without one they default to the
// pure loop (see gospore internal/cell/run.go resolveCallLoop).
func TestStatelessCallables_NoLoopPinning(t *testing.T) {
	a := &Actor{
		aggregators:     make(map[string]domain.AIManagerAggregatorGetResp),
		aggRefs:         make(map[string]ref.Ref),
		aggActorIDs:     make(map[string]string),
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	ctx.Loops = map[string]actor.HandlerMode{}
	if err := a.OnStart(ctx); err != nil {
		t.Fatalf("OnStart failed: %v", err)
	}
	if len(ctx.Loops) != 0 {
		t.Errorf("aimanager must not register loop lanes; got %v", ctx.Loops)
	}
	for _, id := range []string{
		"aimanager.provider_configure",
		"aimanager.provider_set_token_plan",
		"aimanager.provider_set_disabled",
		"aimanager.provider_reset_health",
		"aimanager.provider_record_probe",
		"aimanager.aggregator_configure",
		"aimanager.aggregator_set_disabled",
		"aimanager.config_import",
		"aimanager.model_defaults_set",
	} {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Fatalf("%s not registered", id)
		}
		if got := actor.ResolveLoop(opts...); got != "" {
			t.Errorf("%s pins loop %q; stateless handlers must not pin a lane", id, got)
		}
	}
}

// TestConvertedHandlers_ConcurrentInvocations exercises the converted
// stateless handlers concurrently against a shared Actor with a real FS
// persist store. Run under -race this pins the lock discipline the conversion
// relies on: every mutation + Save happens inside one a.mu write critical
// section (notably handleModelDefaultsSet, which previously called Save
// outside the lock in violation of Save's contract).
func TestConvertedHandlers_ConcurrentInvocations(t *testing.T) {
	a := &Actor{
		actorID: "concurrent-test",
		store:   persist.NewFSPersist(t.TempDir()),
		Providers: []domain.Provider{
			{
				Name: "openai",
				Kind: "openai",
				Models: []domain.ProviderModel{
					{Name: "gpt-test", MaxContextLength: 8192},
				},
			},
		},
		aggregators:     make(map[string]domain.AIManagerAggregatorGetResp),
		aggRefs:         make(map[string]ref.Ref),
		aggActorIDs:     make(map[string]string),
		persistedHealth: make(map[string]persistedHealthEntry),
		assignments:     make(map[string]string),
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())

	const workers = 8
	const iterations = 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				switch (w + i) % 4 {
				case 0:
					if resp, err := a.handleModelDefaultsSet(ctx, domain.AIManagerModelDefaultsSetReq{
						Items: []domain.ModelDefault{{Prefix: "gpt-test", MaxContextLength: int32(1000 + i)}},
					}); err != nil || !resp.Ok {
						t.Errorf("model_defaults_set failed: err=%v resp=%+v", err, resp)
						return
					}
				case 1:
					if _, err := a.handleProviderSetDisabled(ctx, domain.AIManagerProviderSetDisabledReq{
						ProviderName: "openai", Disabled: i%2 == 0,
					}); err != nil {
						t.Errorf("provider_set_disabled failed: %v", err)
						return
					}
				case 2:
					if resp, err := a.handleProviderRecordProbe(ctx, domain.AIManagerProviderRecordProbeReq{
						ProviderName: "openai", Model: "gpt-test", Ok: true, LatencyMs: 12,
					}); err != nil || !resp.Ok {
						t.Errorf("provider_record_probe failed: err=%v resp=%+v", err, resp)
						return
					}
				case 3:
					// Configure round-trips add/update/delete of a scratch
					// provider so provider_configure's three lock paths run.
					if _, err := a.handleProviderConfigure(ctx, domain.AIManagerProviderConfigureReq{
						Name: "scratch", Kind: "openai", Endpoint: "https://example.invalid",
						AuthToken: "tok",
					}); err != nil {
						t.Errorf("provider_configure (add) failed: %v", err)
						return
					}
					if _, err := a.handleProviderConfigure(ctx, domain.AIManagerProviderConfigureReq{Name: "scratch"}); err != nil {
						t.Errorf("provider_configure (delete) failed: %v", err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()

	// Final state sanity: no torn state — the persistent provider survives,
	// no blank entries appear, and the persisted state reloads cleanly into
	// a fresh actor (Save ran under the write lock on every mutation path).
	a.mu.RLock()
	if len(a.Providers) == 0 {
		a.mu.RUnlock()
		t.Fatal("providers list empty after concurrent round-trips")
	}
	for _, p := range a.Providers {
		if p.Name == "" {
			a.mu.RUnlock()
			t.Fatalf("blank provider entry after concurrent round-trips: %+v", a.Providers)
		}
	}
	if len(a.modelDefaults) == 0 {
		a.mu.RUnlock()
		t.Fatal("model defaults empty after concurrent sets")
	}
	a.mu.RUnlock()

	fresh := &Actor{
		actorID:         "concurrent-test",
		store:           a.store,
		aggregators:     make(map[string]domain.AIManagerAggregatorGetResp),
		aggRefs:         make(map[string]ref.Ref),
		aggActorIDs:     make(map[string]string),
		persistedHealth: make(map[string]persistedHealthEntry),
		assignments:     make(map[string]string),
	}
	if err := fresh.Load(); err != nil {
		t.Fatalf("reload persisted state after concurrent writes: %v", err)
	}
	found := false
	for _, p := range fresh.Providers {
		if p.Name == "openai" {
			found = true
		}
	}
	if !found {
		t.Fatal("persistent provider openai missing after reload")
	}
}
