package aimanager

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestProviderListProjectsInferredModality locks the public-view contract:
// models persisted without an explicit Modality come back with the
// runtime-inferred classification, so consumers (provider settings badges,
// media page detection) never need to replicate the heuristic.
func TestProviderListProjectsInferredModality(t *testing.T) {
	a := &Actor{
		Providers: []domain.Provider{{
			Name: "relay",
			Models: []domain.ProviderModel{
				{Name: "grok-imagine-video"},               // no explicit modality
				{Name: "gpt-image-2"},                      // no explicit modality
				{Name: "grok-imagine-image"},               // suffix pattern
				{Name: "seedance2.5"},                      // no separator variant
				{Name: "custom-thing", Modality: "chat"},   // explicit wins
				{Name: "renamed-image", Modality: "chat"},  // explicit chat stays chat
			},
		}},
	}

	resp, err := a.handleProviderList(nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range resp.Items[0].Models {
		got[m.Name] = m.Modality
	}
	want := map[string]string{
		"grok-imagine-video": "video",
		"gpt-image-2":        "image",
		"grok-imagine-image": "image",
		"seedance2.5":        "video",
		"custom-thing":       "chat",
		"renamed-image":      "chat",
	}
	for name, modality := range want {
		if got[name] != modality {
			t.Errorf("provider_list modality for %q = %q, want %q", name, got[name], modality)
		}
	}
	// The projection must not leak back into persisted state.
	if a.Providers[0].Models[0].Modality != "" {
		t.Fatalf("projection mutated persisted provider state: %q", a.Providers[0].Models[0].Modality)
	}
	_ = testutil.GenActorID()
}
