package project

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/agentkit"
)

func TestBuiltinComponentCardProvider(t *testing.T) {
	provider := newBuiltinComponentCardProvider()
	items, err := provider.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(agentkit.BuiltinCards) {
		t.Fatalf("items=%d want=%d", len(items), len(agentkit.BuiltinCards))
	}
	var interfaceControlsFound bool
	var wearableFound bool
	for _, item := range items {
		if item.ID == "builtin:bundle:interface-controls" {
			interfaceControlsFound = true
			if item.Data["settingsVisible"] != true {
				t.Fatalf("interface-controls settingsVisible=%#v", item.Data["settingsVisible"])
			}
			if _, ok := item.Data["settingsProtected"]; ok {
				t.Fatalf("interface-controls should be removable: %#v", item.Data)
			}
		}
		if item.ID == "builtin:bundle:coordinator-wearable" {
			wearableFound = true
			if item.Data["devOnly"] != true {
				t.Fatalf("coordinator-wearable devOnly=%#v", item.Data["devOnly"])
			}
		}
	}
	if !interfaceControlsFound {
		t.Fatal("interface-controls bundle not listed")
	}
	if !wearableFound {
		t.Fatal("coordinator-wearable bundle not listed")
	}
	raw, err := provider.Get(nil, "builtin:bundle:project-wiki")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "protected: true") || !strings.Contains(raw, "project.wiki_get_card") {
		t.Fatalf("unexpected card: %s", raw)
	}
	if err := provider.Delete(nil, "builtin:bundle:project-wiki"); err == nil {
		t.Fatal("expected protected delete error")
	}
	card, ok, err := componentDescriptorFromCard(decodeCard("builtin:bundle:project-wiki", func() string { r, _ := provider.Get(nil, "builtin:bundle:project-wiki"); return r }()))
	if err != nil || !ok {
		t.Fatalf("decode component: %v %v", ok, err)
	}
	if card.Ref.Kind != "bundle" || card.Protected != true || len(card.Tools) != 9 || card.Tools[0].CallableID != "project.wiki_list_cards" {
		t.Fatalf("unexpected descriptor: %+v", card)
	}

	goalRaw, err := provider.Get(nil, "builtin:mode:goal")
	if err != nil {
		t.Fatalf("get goal card: %v", err)
	}
	if !strings.Contains(goalRaw, "icon: target") {
		t.Fatalf("expected goal card icon in raw YAML, got:\n%s", goalRaw)
	}
	if !strings.Contains(goalRaw, "You are in Goal Mode.") {
		t.Fatalf("expected goal card body in rendered output, got:\n%s", goalRaw)
	}
	goalCard, ok, err := componentDescriptorFromCard(decodeCard("builtin:mode:goal", goalRaw))
	if err != nil || !ok {
		t.Fatalf("decode goal component: %v %v", ok, err)
	}
	if goalCard.Icon != "target" || goalCard.Title != "builtin:mode:goal" || goalCard.Protected != false {
		t.Fatalf("unexpected goal descriptor: %+v", goalCard)
	}
	if goalCard.Visual == nil || goalCard.Visual.Icon != "target" || goalCard.Visual.Color != "#d97706" {
		t.Fatalf("unexpected goal visual: %+v", goalCard.Visual)
	}
	if len(goalCard.Prompts) != 1 || !strings.Contains(goalCard.Prompts[0].Text, "Goal Mode") {
		t.Fatalf("unexpected goal prompt: %+v", goalCard.Prompts)
	}
}
