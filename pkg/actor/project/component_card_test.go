package project

import "testing"

func TestComponentDescriptorFromCard(t *testing.T) {
	card := &CardRecord{
		Title: "tool:file-read",
		Type:  "tool",
		Tags:  []string{"component", "tool"},
		Data: map[string]any{
			"componentVersion": float64(2),
			"icon":             "📄",
			"callable":         "project.read",
			"requires":         []any{"builtin:prompt:coder"},
			"usage":            "Read a file before editing it.",
			"constraints":      "Do not use shell as a replacement.",
		},
		Body: "# Read File\n\nRead an existing project file.",
	}

	descriptor, ok, err := componentDescriptorFromCard(card)
	if err != nil {
		t.Fatalf("componentDescriptorFromCard failed: %v", err)
	}
	if !ok {
		t.Fatal("component card was not recognized")
	}
	if descriptor.Ref.CardID != card.Title || descriptor.Ref.Kind != "tool" || descriptor.Ref.Version != 2 {
		t.Fatalf("unexpected ref: %+v", descriptor.Ref)
	}
	if descriptor.Icon != "📄" {
		t.Fatalf("expected icon 📄, got %q", descriptor.Icon)
	}
	if len(descriptor.Dependencies) != 1 || descriptor.Dependencies[0].CardID != "builtin:prompt:coder" {
		t.Fatalf("unexpected dependencies: %+v", descriptor.Dependencies)
	}
	if len(descriptor.Tools) != 1 || descriptor.Tools[0].CallableID != "project.read" {
		t.Fatalf("unexpected tools: %+v", descriptor.Tools)
	}
	if descriptor.Tools[0].Usage == "" || descriptor.Tools[0].Constraints == "" {
		t.Fatal("tool usage and constraints must be preserved")
	}
	if len(descriptor.Prompts) != 1 || descriptor.Prompts[0].Text != card.Body {
		t.Fatalf("unexpected prompts: %+v", descriptor.Prompts)
	}
}

func TestComponentDescriptorProjectsToolListFromCardMetadata(t *testing.T) {
	card := &CardRecord{
		Title: "bundle:wiki", Type: "bundle", Tags: []string{"component", "bundle"},
		Data: map[string]any{
			"tools": []any{"project.wiki_get_card", "project.wiki_list_cards"},
		},
	}
	descriptor, ok, err := componentDescriptorFromCard(card)
	if err != nil || !ok {
		t.Fatalf("componentDescriptorFromCard failed: ok=%v err=%v", ok, err)
	}
	if len(descriptor.Tools) != 2 || descriptor.Tools[0].CallableID != "project.wiki_get_card" || descriptor.Tools[1].CallableID != "project.wiki_list_cards" {
		t.Fatalf("card tool list was not projected: %+v", descriptor.Tools)
	}
}

func TestComponentDescriptorPreservesPromptSection(t *testing.T) {
	card := &CardRecord{
		Title: "prompt:profile:coder", Type: "prompt", Tags: []string{"component", "prompt", "profile"},
		Data: map[string]any{"source": "builtin"}, Body: "You are a coder.",
	}
	descriptor, ok, err := componentDescriptorFromCard(card)
	if err != nil || !ok {
		t.Fatalf("componentDescriptorFromCard failed: ok=%v err=%v", ok, err)
	}
	if len(descriptor.Prompts) != 1 || descriptor.Prompts[0].Placement != "role" {
		t.Fatalf("profile section was not preserved: %+v", descriptor.Prompts)
	}
}

func TestComponentDescriptorPreservesPlacementField(t *testing.T) {
	card := &CardRecord{
		Title: "builtin:bundle:file-tools", Type: "bundle", Tags: []string{"component", "bundle"},
		Data: map[string]any{
			"placement": "tool_guidance",
			"tools":     []any{"project.read"},
		},
		Body: "## File Tools Usage",
	}
	descriptor, ok, err := componentDescriptorFromCard(card)
	if err != nil || !ok {
		t.Fatalf("componentDescriptorFromCard failed: ok=%v err=%v", ok, err)
	}
	if len(descriptor.Prompts) != 1 || descriptor.Prompts[0].Placement != "tool_guidance" {
		t.Fatalf("bundle card placement must be tool_guidance, got: %+v", descriptor.Prompts)
	}
}

func TestComponentDescriptorPrefersDeclaredTitle(t *testing.T) {
	card := &CardRecord{
		Title: "mcp:srv-0", Type: "bundle", Tags: []string{"component", "bundle"},
		Data: map[string]any{"title": "filesystem", "icon": "plug"},
	}
	descriptor, ok, err := componentDescriptorFromCard(card)
	if err != nil || !ok {
		t.Fatalf("componentDescriptorFromCard failed: ok=%v err=%v", ok, err)
	}
	if descriptor.Title != "filesystem" {
		t.Fatalf("expected declared title, got %q", descriptor.Title)
	}
	if descriptor.Icon != "plug" {
		t.Fatalf("expected icon plug, got %q", descriptor.Icon)
	}

	// Cards without data.title keep falling back to the card ID.
	plain := &CardRecord{
		Title: "bundle:wiki", Type: "bundle", Tags: []string{"component", "bundle"},
	}
	descriptor, ok, err = componentDescriptorFromCard(plain)
	if err != nil || !ok {
		t.Fatalf("componentDescriptorFromCard failed: ok=%v err=%v", ok, err)
	}
	if descriptor.Title != "bundle:wiki" {
		t.Fatalf("expected card ID fallback title, got %q", descriptor.Title)
	}
}

func TestComponentDescriptorFromCardIgnoresOrdinaryCard(t *testing.T) {
	descriptor, ok, err := componentDescriptorFromCard(&CardRecord{Title: "note", Tags: []string{"idea"}})
	if err != nil {
		t.Fatalf("ordinary card returned error: %v", err)
	}
	if ok || descriptor.Ref.CardID != "" {
		t.Fatalf("ordinary card was treated as component: %+v", descriptor)
	}
}

func TestComponentDescriptorRequiresKind(t *testing.T) {
	_, ok, err := componentDescriptorFromCard(&CardRecord{Title: "broken", Tags: []string{"component"}})
	if !ok || err == nil {
		t.Fatal("component without kind should fail validation")
	}
}

// Virtual mount nodes like __builtin_prompt__ carry the component tag purely as
// a hierarchy-parenting device; they must be excluded from the component
// catalog instead of failing type projection.
func TestComponentDescriptorIgnoresVirtualMountNodes(t *testing.T) {
	for _, builtinRole := range []string{"mount", "aggregator"} {
		descriptor, ok, err := componentDescriptorFromCard(&CardRecord{
			Title: "__builtin_prompt__",
			Tags:  []string{"component"},
			Data:  map[string]any{"builtinRole": builtinRole, "mountType": "prompt", "autoMount": true},
		})
		if err != nil {
			t.Fatalf("builtinRole=%s returned error: %v", builtinRole, err)
		}
		if ok || descriptor.Ref.CardID != "" {
			t.Fatalf("builtinRole=%s node was treated as component: %+v", builtinRole, descriptor)
		}
	}
}
