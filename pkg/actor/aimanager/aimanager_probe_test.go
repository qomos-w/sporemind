package aimanager

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestHandleProviderRecordProbe_PersistsOutcome(t *testing.T) {
	a := &Actor{
		actorID: "test-aimanager",
		Providers: []domain.Provider{
			{
				Name:     "openai",
				Kind:     "openai",
				Endpoint: "https://api.openai.com/v1",
				Models: []domain.ProviderModel{
					{Name: "gpt-5", MaxContextLength: 128000, MaxTokens: 16384},
				},
			},
		},
	}
	a.store = persist.MustNew(persist.PersistConfig{DataDir: t.TempDir(), Prefix: "aimanager"})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// Success path records ok + latency and clears any previous error.
	resp, err := a.handleProviderRecordProbe(ctx, domain.AIManagerProviderRecordProbeReq{
		ProviderName: "openai", Model: "gpt-5", Ok: true, LatencyMs: 420,
	})
	if err != nil || !resp.Ok {
		t.Fatalf("unexpected resp: %+v err=%v", resp, err)
	}
	m := a.Providers[0].Models[0]
	if m.ProbeState != "ok" || m.ProbeLatencyMs != 420 || m.ProbeError != "" || m.ProbeAt == 0 {
		t.Errorf("ok probe not persisted: %+v", m)
	}

	// Failure path flips state, stores the first error line only.
	resp, err = a.handleProviderRecordProbe(ctx, domain.AIManagerProviderRecordProbeReq{
		ProviderName: "openai", Model: "gpt-5", Ok: false,
		Error: "stream open: 401 unauthorized\nsecond line",
	})
	if err != nil || !resp.Ok {
		t.Fatalf("unexpected resp: %+v err=%v", resp, err)
	}
	m = a.Providers[0].Models[0]
	if m.ProbeState != "failed" || m.ProbeLatencyMs != 0 || m.ProbeError != "stream open: 401 unauthorized" {
		t.Errorf("failed probe not persisted: %+v", m)
	}

	// The recorded result is surfaced by provider_list.
	list, err := a.handleProviderList(nil)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	got := list.Items[0].Models[0]
	if got.ProbeState != "failed" || got.ProbeAt == 0 {
		t.Errorf("provider_list does not surface probe state: %+v", got)
	}
}

func TestHandleProviderRecordProbe_UnknownTargets(t *testing.T) {
	a := &Actor{
		actorID: "test-aimanager",
		Providers: []domain.Provider{
			{Name: "openai", Models: []domain.ProviderModel{{Name: "gpt-5"}}},
		},
	}
	a.store = persist.MustNew(persist.PersistConfig{DataDir: t.TempDir(), Prefix: "aimanager"})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleProviderRecordProbe(ctx, domain.AIManagerProviderRecordProbeReq{
		ProviderName: "ghost", Model: "gpt-5", Ok: true,
	})
	if err != nil || resp.Ok || resp.Error != "provider not found" {
		t.Fatalf("expected provider-not-found, got %+v err=%v", resp, err)
	}

	resp, err = a.handleProviderRecordProbe(ctx, domain.AIManagerProviderRecordProbeReq{
		ProviderName: "openai", Model: "ghost", Ok: true,
	})
	if err != nil || resp.Ok || resp.Error != "model not found" {
		t.Fatalf("expected model-not-found, got %+v err=%v", resp, err)
	}

	resp, err = a.handleProviderRecordProbe(ctx, domain.AIManagerProviderRecordProbeReq{})
	if err != nil || resp.Ok || resp.Error != "providerName and model are required" {
		t.Fatalf("expected validation error, got %+v err=%v", resp, err)
	}
}
