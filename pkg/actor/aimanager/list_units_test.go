package aimanager

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestHandleListUnits locks the unit-list contract: the callable returns
// (model, provider) unit tuples — not bare models — each carrying its runtime
// modality, filterable by Modality. All modalities are included; an empty
// filter returns the full set.
func TestHandleListUnits(t *testing.T) {
	a := &Actor{
		Providers: []domain.Provider{{
			Name: "relay",
			Kind: "openai", // chat models need a supported wire protocol
			Models: []domain.ProviderModel{
				{Name: "gpt-5.2"},               // chat
				{Name: "gpt-image-2"},          // image
				{Name: "veo-3.0-generate-001"}, // video
				{Name: "claude-sonnet-5"},      // chat
			},
		}},
	}

	resp, err := a.handleListUnits(nil, domain.AIManagerListUnitsReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 4 {
		t.Fatalf("list_units (no filter) returned %d units, want 4", len(resp.Items))
	}
	for _, u := range resp.Items {
		if u.ProviderName != "relay" {
			t.Errorf("unit %q has provider %q, want relay", u.Model, u.ProviderName)
		}
		if u.Modality == "" {
			t.Errorf("unit %q has empty modality", u.Model)
		}
	}

	modalities := func(items []domain.ManualCallableUnit) []string {
		out := make([]string, len(items))
		for i, u := range items {
			out[i] = u.Modality
		}
		return out
	}

	img, _ := a.handleListUnits(nil, domain.AIManagerListUnitsReq{Modality: "image"})
	if len(img.Items) != 1 || img.Items[0].Model != "gpt-image-2" {
		t.Fatalf("list_units(image) = %+v, want [gpt-image-2]", img.Items)
	}
	for _, m := range modalities(img.Items) {
		if m != "image" {
			t.Errorf("image filter leaked modality %q", m)
		}
	}

	vid, _ := a.handleListUnits(nil, domain.AIManagerListUnitsReq{Modality: "video"})
	if len(vid.Items) != 1 || vid.Items[0].Model != "veo-3.0-generate-001" {
		t.Fatalf("list_units(video) = %+v, want [veo-3.0-generate-001]", vid.Items)
	}

	chat, _ := a.handleListUnits(nil, domain.AIManagerListUnitsReq{Modality: "chat"})
	if len(chat.Items) != 2 {
		t.Fatalf("list_units(chat) returned %d units, want 2", len(chat.Items))
	}
	for _, m := range modalities(chat.Items) {
		if m != "chat" {
			t.Errorf("chat filter leaked modality %q", m)
		}
	}
}
