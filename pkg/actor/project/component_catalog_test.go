package project

import (
	"fmt"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestComponentCatalogUsesStableOrder(t *testing.T) {
	a := &Actor{store: &testCardStore{cards: []*CardRecord{
		{Title: "z", Type: "prompt", Tags: []string{"component"}},
		{Title: "a", Type: "tool", Tags: []string{"component"}},
	}}}
	descriptors, err := a.listComponentDescriptors(actor.Context(nil))
	if err != nil {
		t.Fatalf("listComponentDescriptors failed: %v", err)
	}
	if len(descriptors) != 2 || descriptors[0].Ref.CardID != "a" || descriptors[1].Ref.CardID != "z" {
		t.Fatalf("unexpected order: %+v", descriptors)
	}
}

type testCardStore struct{ cards []*CardRecord }

func (s *testCardStore) Get(id string) (*CardRecord, error) {
	for _, c := range s.cards {
		if c.Title == id {
			return c, nil
		}
	}
	return nil, ErrCardNotFound
}
func (s *testCardStore) List() ([]*CardRecord, error) { return s.cards, nil }
func (s *testCardStore) Save(*CardRecord) error       { return nil }
func (s *testCardStore) Delete(string) error          { return nil }

// testExternalCardProvider is a minimal ExternalCardProvider for unit tests.
type testExternalCardProvider struct {
	prefix string
	items  []domain.MonoCardListItem
	cards  map[string]string // ID → raw markdown
}

func (p *testExternalCardProvider) Prefix() string { return p.prefix }
func (p *testExternalCardProvider) List(_ actor.PureContext) ([]domain.MonoCardListItem, error) {
	return p.items, nil
}
func (p *testExternalCardProvider) Get(_ actor.PureContext, id string) (string, error) {
	if raw, ok := p.cards[id]; ok {
		return raw, nil
	}
	return "", fmt.Errorf("not found: %s", id)
}
func (p *testExternalCardProvider) Save(_ actor.PureContext, _ string, _ string) error { return nil }
func (p *testExternalCardProvider) Delete(_ actor.PureContext, _ string) error         { return nil }

func TestListComponentDescriptorsShadowsExternalCard(t *testing.T) {
	// A persisted card with the same ID as an external card should shadow it,
	// and the descriptor should carry ShadowedById.
	persistedCard := &CardRecord{
		Title: "builtin:bundle:test-bundle",
		Type:  "bundle",
		Tags:  []string{"component", "bundle"},
		Data:  map[string]any{"componentKind": "bundle"},
	}
	externalRaw := "---\nid: builtin:bundle:test-bundle\ntype: bundle\ntags: [component, bundle]\ndata:\n  componentKind: bundle\n---\n\n# Test Bundle (external)\n"

	provider := &testExternalCardProvider{
		prefix: "builtin:",
		items: []domain.MonoCardListItem{
			{ID: "builtin:bundle:test-bundle", Type: "bundle", Tags: []string{"component", "bundle"}},
		},
		cards: map[string]string{"builtin:bundle:test-bundle": externalRaw},
	}

	a := &Actor{
		store:             &testCardStore{cards: []*CardRecord{persistedCard}},
		externalProviders: []ExternalCardProvider{provider},
	}
	descriptors, err := a.listComponentDescriptors(actor.Context(nil))
	if err != nil {
		t.Fatalf("listComponentDescriptors failed: %v", err)
	}
	if len(descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d: %+v", len(descriptors), descriptors)
	}
	d := descriptors[0]
	if d.Ref.CardID != "builtin:bundle:test-bundle" {
		t.Fatalf("unexpected card ID: %s", d.Ref.CardID)
	}
	if d.ShadowedByID != "builtin:bundle:test-bundle" {
		t.Fatalf("expected ShadowedById %q, got %q", "builtin:bundle:test-bundle", d.ShadowedByID)
	}
}

func TestListComponentDescriptorsNoShadowWhenNoOverlap(t *testing.T) {
	persistedCard := &CardRecord{
		Title: "local:my-tool",
		Type:  "tool",
		Tags:  []string{"component", "tool"},
		Data:  map[string]any{"componentKind": "tool"},
	}
	provider := &testExternalCardProvider{
		prefix: "builtin:",
		items: []domain.MonoCardListItem{
			{ID: "builtin:bundle:other", Type: "bundle", Tags: []string{"component", "bundle"}},
		},
		cards: map[string]string{"builtin:bundle:other": "---\nid: builtin:bundle:other\ntype: bundle\ntags: [component, bundle]\ndata:\n  componentKind: bundle\n---\n\n# Other\n"},
	}

	a := &Actor{
		store:             &testCardStore{cards: []*CardRecord{persistedCard}},
		externalProviders: []ExternalCardProvider{provider},
	}
	descriptors, err := a.listComponentDescriptors(actor.Context(nil))
	if err != nil {
		t.Fatalf("listComponentDescriptors failed: %v", err)
	}
	if len(descriptors) != 2 {
		t.Fatalf("expected 2 descriptors, got %d", len(descriptors))
	}
	for _, d := range descriptors {
		if d.ShadowedByID != "" {
			t.Fatalf("unexpected ShadowedById %q on card %s", d.ShadowedByID, d.Ref.CardID)
		}
	}
}

func TestListComponentDescriptorsExternalCardWithoutShadow(t *testing.T) {
	// An external card that is NOT shadowed by a persisted card should appear
	// normally without ShadowedById.
	provider := &testExternalCardProvider{
		prefix: "builtin:",
		items: []domain.MonoCardListItem{
			{ID: "builtin:bundle:standalone", Type: "bundle", Tags: []string{"component", "bundle"}},
		},
		cards: map[string]string{"builtin:bundle:standalone": "---\nid: builtin:bundle:standalone\ntype: bundle\ntags: [component, bundle]\ndata:\n  componentKind: bundle\n---\n\n# Standalone\n"},
	}

	a := &Actor{
		store:             &testCardStore{cards: nil},
		externalProviders: []ExternalCardProvider{provider},
	}
	descriptors, err := a.listComponentDescriptors(actor.Context(nil))
	if err != nil {
		t.Fatalf("listComponentDescriptors failed: %v", err)
	}
	if len(descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(descriptors))
	}
	if descriptors[0].ShadowedByID != "" {
		t.Fatalf("expected empty ShadowedById, got %q", descriptors[0].ShadowedByID)
	}
}
