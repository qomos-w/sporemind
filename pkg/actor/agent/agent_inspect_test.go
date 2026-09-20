package agent

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestHandleInspectPages(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleInspectPages(nil)
	if err != nil {
		t.Fatalf("handleInspectPages returned error: %v", err)
	}
	want := map[string]bool{
		"prompt-context":      true,
		"compiled-prompt":     true,
		"turn-history":        true,
		"compaction-snapshot": true,
		"context-budget":      true,
		"component-snapshot":  true,
	}
	seen := make(map[string]bool)
	for _, p := range resp.Items {
		seen[p.ID] = true
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("inspect page %q missing from response", id)
		}
	}
}

func TestHandleInspectPagesLabels(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleInspectPages(nil)
	if err != nil {
		t.Fatalf("handleInspectPages returned error: %v", err)
	}
	for _, p := range resp.Items {
		if p.Label == "" {
			t.Errorf("inspect page %q has empty label", p.ID)
		}
		if p.Icon == "" {
			t.Errorf("inspect page %q has empty icon", p.ID)
		}
	}
	_ = gen.InspectPage{}
}
