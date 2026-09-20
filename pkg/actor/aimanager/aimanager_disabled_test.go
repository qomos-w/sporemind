package aimanager

import (
	"encoding/json"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHandleProviderSetDisabled_ProviderLevel verifies that disabling a provider
// sets p.Disabled, is reflected in handleProviderList, and re-enabling works.
func TestHandleProviderSetDisabled_ProviderLevel(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}
	a := &Actor{
		actorID: "test-aimanager",
		store:   ms,
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		aggregators:     map[string]domain.AIManagerAggregatorGetResp{},
		persistedHealth: make(map[string]persistedHealthEntry),
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())

	// Disable.
	resp, err := a.handleProviderSetDisabled(ctx, domain.AIManagerProviderSetDisabledReq{
		ProviderName: "openai",
		Disabled:     true,
	})
	if err != nil {
		t.Fatalf("handleProviderSetDisabled failed: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("expected Ok=true, got error %q", resp.Error)
	}

	// Verify in-memory state.
	if !a.Providers[0].Disabled {
		t.Fatal("provider should be disabled in memory")
	}

	// Verify handleProviderList projects Disabled.
	listResp, err := a.handleProviderList(nil)
	if err != nil {
		t.Fatalf("handleProviderList failed: %v", err)
	}
	if len(listResp.Items) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(listResp.Items))
	}
	if !listResp.Items[0].Disabled {
		t.Fatal("provider should be disabled in list projection")
	}

	// Verify persisted state.
	raw, ok := ms.data["test-aimanager"]
	if !ok {
		t.Fatal("state not persisted")
	}
	var state persistState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(state.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(state.Providers))
	}
	if !state.Providers[0].Disabled {
		t.Fatal("provider should be disabled in persisted state")
	}

	// Re-enable.
	resp, err = a.handleProviderSetDisabled(ctx, domain.AIManagerProviderSetDisabledReq{
		ProviderName: "openai",
		Disabled:     false,
	})
	if err != nil {
		t.Fatalf("handleProviderSetDisabled failed: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("expected Ok=true, got error %q", resp.Error)
	}

	// Verify in-memory state.
	if a.Providers[0].Disabled {
		t.Fatal("provider should no longer be disabled")
	}
}

// TestHandleProviderSetDisabled_ModelLevel verifies that disabling a model
// sets ProviderModel.Disabled, is reflected in handleProviderResolveModel,
// and re-enabling works.
func TestHandleProviderSetDisabled_ModelLevel(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}
	a := &Actor{
		actorID: "test-aimanager",
		store:   ms,
		Providers: []domain.Provider{
			{
				Name: "openai", Kind: "openai", Endpoint: "https://openai",
				Models: []domain.ProviderModel{
					{Name: "gpt-4o"},
					{Name: "gpt-4o-mini"},
				},
			},
		},
		aggregators:     map[string]domain.AIManagerAggregatorGetResp{},
		persistedHealth: make(map[string]persistedHealthEntry),
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())

	// Disable a specific model.
	resp, err := a.handleProviderSetDisabled(ctx, domain.AIManagerProviderSetDisabledReq{
		ProviderName: "openai",
		Model:        "gpt-4o",
		Disabled:     true,
	})
	if err != nil {
		t.Fatalf("handleProviderSetDisabled failed: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("expected Ok=true, got error %q", resp.Error)
	}

	// Verify in-memory state.
	if !a.Providers[0].Models[0].Disabled {
		t.Fatal("model should be disabled in memory")
	}
	if a.Providers[0].Models[1].Disabled {
		t.Fatal("other model should not be disabled")
	}

	// Verify handleProviderResolveModel projects Disabled.
	resolveResp, err := a.handleProviderResolveModel(nil, domain.AIManagerProviderResolveModelReq{
		Name:  "openai",
		Model: "gpt-4o",
	})
	if err != nil {
		t.Fatalf("handleProviderResolveModel failed: %v", err)
	}
	if !resolveResp.Disabled {
		t.Fatal("model should be disabled in resolve model projection")
	}

	// Verify the other model is not disabled.
	resolveResp2, err := a.handleProviderResolveModel(nil, domain.AIManagerProviderResolveModelReq{
		Name:  "openai",
		Model: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("handleProviderResolveModel failed: %v", err)
	}
	if resolveResp2.Disabled {
		t.Fatal("other model should not be disabled in resolve model projection")
	}

	// Re-enable.
	resp, err = a.handleProviderSetDisabled(ctx, domain.AIManagerProviderSetDisabledReq{
		ProviderName: "openai",
		Model:        "gpt-4o",
		Disabled:     false,
	})
	if err != nil {
		t.Fatalf("handleProviderSetDisabled failed: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("expected Ok=true, got error %q", resp.Error)
	}
	if a.Providers[0].Models[0].Disabled {
		t.Fatal("model should no longer be disabled")
	}
}

// TestHandleProviderSetDisabled_NotFound verifies that unknown provider/model
// returns Ok:false with an error message.
func TestHandleProviderSetDisabled_NotFound(t *testing.T) {
	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// Unknown provider.
	resp, err := a.handleProviderSetDisabled(ctx, domain.AIManagerProviderSetDisabledReq{
		ProviderName: "nonexistent",
		Disabled:     true,
	})
	if err != nil {
		t.Fatalf("handleProviderSetDisabled failed: %v", err)
	}
	if resp.Ok {
		t.Fatal("expected Ok=false for unknown provider")
	}
	if resp.Error == "" {
		t.Fatal("expected error message for unknown provider")
	}

	// Known provider but unknown model.
	resp, err = a.handleProviderSetDisabled(ctx, domain.AIManagerProviderSetDisabledReq{
		ProviderName: "openai",
		Model:        "nonexistent-model",
		Disabled:     true,
	})
	if err != nil {
		t.Fatalf("handleProviderSetDisabled failed: %v", err)
	}
	if resp.Ok {
		t.Fatal("expected Ok=false for unknown model")
	}
	if resp.Error == "" {
		t.Fatal("expected error message for unknown model")
	}

	// Empty provider name.
	resp, err = a.handleProviderSetDisabled(ctx, domain.AIManagerProviderSetDisabledReq{
		ProviderName: "",
		Disabled:     true,
	})
	if err != nil {
		t.Fatalf("handleProviderSetDisabled failed: %v", err)
	}
	if resp.Ok {
		t.Fatal("expected Ok=false for empty provider name")
	}
}

// TestHandleAggregatorSetDisabled verifies that disabling an aggregator sets
// Disabled and is reflected in handleAggregatorList.
func TestHandleAggregatorSetDisabled(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}
	a := &Actor{
		actorID: "test-aimanager",
		store:   ms,
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"my-agg": {ID: "my-agg", Name: "My Aggregator", Units: []domain.ManualCallableUnit{{Model: "gpt-4o", ProviderName: "openai"}}},
		},
		aggRefs:         map[string]ref.Ref{},
		persistedHealth: make(map[string]persistedHealthEntry),
		configVersion:   1,
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())

	// Disable.
	resp, err := a.handleAggregatorSetDisabled(ctx, domain.AIManagerAggregatorSetDisabledReq{
		ID:       "my-agg",
		Disabled: true,
	})
	if err != nil {
		t.Fatalf("handleAggregatorSetDisabled failed: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("expected Ok=true, got error %q", resp.Error)
	}

	// Verify in-memory state.
	if !a.aggregators["my-agg"].Disabled {
		t.Fatal("aggregator should be disabled in memory")
	}

	// Verify handleAggregatorList projects Disabled. Since aggRefs is empty,
	// the list will be empty; we verify the map directly.
	// (aggRefs maps configID to actor ref; aggRefs empty means no aggregators
	// are listed even though config exists. We manually add a ref for the list test.)
	a.aggRefs["my-agg"] = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any { return nil })
	listResp, err := a.handleAggregatorList(ctx)
	if err != nil {
		t.Fatalf("handleAggregatorList failed: %v", err)
	}
	if len(listResp.Items) != 1 {
		t.Fatalf("expected 1 aggregator, got %d", len(listResp.Items))
	}
	if !listResp.Items[0].Disabled {
		t.Fatal("aggregator should be disabled in list projection")
	}

	// Verify persisted state.
	raw, ok := ms.data["test-aimanager"]
	if !ok {
		t.Fatal("state not persisted")
	}
	var state persistState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	agg, ok := state.Aggregators["my-agg"]
	if !ok {
		t.Fatal("aggregator not in persisted state")
	}
	if !agg.Disabled {
		t.Fatal("aggregator should be disabled in persisted state")
	}

	// Re-enable.
	resp, err = a.handleAggregatorSetDisabled(ctx, domain.AIManagerAggregatorSetDisabledReq{
		ID:       "my-agg",
		Disabled: false,
	})
	if err != nil {
		t.Fatalf("handleAggregatorSetDisabled failed: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("expected Ok=true, got error %q", resp.Error)
	}
	if a.aggregators["my-agg"].Disabled {
		t.Fatal("aggregator should no longer be disabled")
	}
}

// TestHandleAggregatorSetDisabled_NotFound verifies that unknown aggregator
// returns Ok:false with an error message.
func TestHandleAggregatorSetDisabled_NotFound(t *testing.T) {
	a := &Actor{
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"my-agg": {ID: "my-agg", Name: "My Aggregator", Units: []domain.ManualCallableUnit{{Model: "gpt-4o", ProviderName: "openai"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleAggregatorSetDisabled(ctx, domain.AIManagerAggregatorSetDisabledReq{
		ID:       "nonexistent",
		Disabled: true,
	})
	if err != nil {
		t.Fatalf("handleAggregatorSetDisabled failed: %v", err)
	}
	if resp.Ok {
		t.Fatal("expected Ok=false for unknown aggregator")
	}
	if resp.Error == "" {
		t.Fatal("expected error message for unknown aggregator")
	}
}

// TestConfigExportImport_RoundTripDisabled verifies that Disabled on provider
// and provider model are preserved through export/import, and that the export
// payload also carries aggregator-level Disabled.
func TestConfigExportImport_RoundTripDisabled(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}
	a := &Actor{
		actorID: "test-aimanager",
		store:   ms,
		Providers: []domain.Provider{
			{
				Name: "openai", Kind: "openai", Endpoint: "https://openai",
				Disabled: true,
				Models: []domain.ProviderModel{
					{Name: "gpt-4o", Disabled: true},
					{Name: "gpt-4o-mini"},
				},
			},
		},
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"my-agg": {ID: "my-agg", Name: "My Aggregator", Disabled: true, Units: []domain.ManualCallableUnit{{Model: "gpt-4o", ProviderName: "openai"}}},
		},
		aggRefs: map[string]ref.Ref{
			"my-agg": testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any { return nil }),
		},
		aggActorIDs: map[string]string{
			"my-agg": testutil.GenActorID().String(),
		},
		AggregatorNames: make(map[string]string),
		configVersion:   1,
		persistedHealth: make(map[string]persistedHealthEntry),
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = func(_ actor.Props, name string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any { return nil }), nil
	}

	// Export.
	exportResp, err := a.handleConfigExport(nil)
	if err != nil {
		t.Fatalf("handleConfigExport failed: %v", err)
	}

	// Verify the exported payload includes all Disabled flags.
	var payload struct {
		Providers   []domain.Provider                            `json:"providers"`
		Aggregators map[string]domain.AIManagerAggregatorGetResp `json:"aggregators"`
	}
	if err := json.Unmarshal([]byte(exportResp.Data), &payload); err != nil {
		t.Fatalf("unmarshal exported data: %v", err)
	}
	if len(payload.Providers) != 1 || !payload.Providers[0].Disabled {
		t.Fatal("provider Disabled not present in export payload")
	}
	if !payload.Providers[0].Models[0].Disabled {
		t.Fatal("model Disabled not present in export payload")
	}
	if payload.Providers[0].Models[1].Disabled {
		t.Fatal("un-disabled model should not be Disabled in export payload")
	}
	agg, ok := payload.Aggregators["my-agg"]
	if !ok {
		t.Fatal("aggregator not present in export payload")
	}
	if !agg.Disabled {
		t.Fatal("aggregator Disabled not present in export payload")
	}

	// Create a fresh actor and import.
	a2 := &Actor{
		actorID: "test-aimanager",
		store:   &memStore{data: make(map[string][]byte)},
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			autoAggregatorID: {ID: autoAggregatorID, Name: "Auto (All Providers)", Units: []domain.ManualCallableUnit{}},
		},
		aggRefs: map[string]ref.Ref{
			"my-agg":        testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any { return nil }),
			autoAggregatorID: testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any { return nil }),
		},
		aggActorIDs: map[string]string{
			"my-agg":        testutil.GenActorID().String(),
			autoAggregatorID: testutil.GenActorID().String(),
		},
		AggregatorNames: make(map[string]string),
		configVersion:   1,
		persistedHealth: make(map[string]persistedHealthEntry),
	}

	importReq := domain.AIManagerConfigImportReq{Data: exportResp.Data}
	_, err = a2.handleConfigImport(ctx, importReq)
	if err != nil {
		t.Fatalf("handleConfigImport failed: %v", err)
	}

	// Verify provider-level Disabled survived round-trip.
	if len(a2.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(a2.Providers))
	}
	if !a2.Providers[0].Disabled {
		t.Fatal("provider-level Disabled not preserved through export/import")
	}

	// Verify model-level Disabled survived.
	if !a2.Providers[0].Models[0].Disabled {
		t.Fatal("model-level Disabled not preserved through export/import")
	}
	if a2.Providers[0].Models[1].Disabled {
		t.Fatal("un-disabled model should not have Disabled after export/import")
	}
}