package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestSystemPromptPartsOrderWithMemory(t *testing.T) {
	// Verify the system prompt parts ordering when memory is present:
	// Base → Resolved → Ontology → Experience Heads → Hot Context → Session.
	// This uses systemPromptParts directly which doesn't have memory context.
	// The memory injection happens upstream in resolveFullHotContext.
	// Test the parts assembly independently.
	inst := &domain.CompiledInstructions{
		Base:     []string{"## Base\nrole instruction"},
		Resolved: []string{"## Resolved\nplan details"},
	}
	hotContext := []domain.ContentBlock{
		{Type: domain.ContentBlockText, Text: "## Active Goal\ncomplete the task"},
		{Type: domain.ContentBlockText, Text: "## Task Board\nitem 1"},
	}

	parts := systemPromptParts(inst, hotContext)
	if len(parts) != 4 {
		t.Fatalf("expected 4 parts, got %d", len(parts))
	}
	if !strings.Contains(parts[0], "Base") {
		t.Fatal("part[0] should be Base")
	}
	if !strings.Contains(parts[1], "Resolved") {
		t.Fatal("part[1] should be Resolved")
	}
	if !strings.Contains(parts[2], "Goal") {
		t.Fatal("part[2] should be Goal (first hot context)")
	}
	if !strings.Contains(parts[3], "Task Board") {
		t.Fatal("part[3] should be Task Board (second hot context)")
	}
}

func TestSystemPromptPartsNilInstructions(t *testing.T) {
	hotContext := []domain.ContentBlock{
		{Type: domain.ContentBlockText, Text: "## Active Goal\ntest"},
	}
	parts := systemPromptParts(nil, hotContext)
	if len(parts) != 1 || !strings.Contains(parts[0], "Goal") {
		t.Fatal("expected only hot context when instructions nil")
	}
}

func TestSystemPromptPartsEmpty(t *testing.T) {
	if parts := systemPromptParts(nil, nil); len(parts) != 0 {
		t.Fatal("expected empty parts")
	}
}

func TestSystemPromptPartsSkipsEmptyHotContext(t *testing.T) {
	inst := &domain.CompiledInstructions{Base: []string{"base"}}
	hotContext := []domain.ContentBlock{
		{Type: domain.ContentBlockText, Text: ""},
		{Type: domain.ContentBlockText, Text: "valid"},
	}
	parts := systemPromptParts(inst, hotContext)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
}

func TestAssembleSystemPromptCacheControl(t *testing.T) {
	inst := &domain.CompiledInstructions{
		Base:     []string{"base instruction"},
		Resolved: []string{"resolved instruction"},
	}
	hotContext := []domain.ContentBlock{
		{Type: domain.ContentBlockText, Text: "goal block"},
		{Type: domain.ContentBlockText, Text: "task board"},
	}

	_, blocks := assembleSystemPrompt(inst, hotContext)
	if len(blocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(blocks))
	}
	// Last block should be ephemeral.
	if blocks[3].CacheControl != "ephemeral" {
		t.Fatal("expected last block to have ephemeral cache control")
	}
	// Other blocks should not.
	for i := 0; i < 3; i++ {
		if blocks[i].CacheControl != "" {
			t.Fatalf("block %d should not have cache control", i)
		}
	}
}

func TestAssembleSystemPromptEmptyInput(t *testing.T) {
	prompt, blocks := assembleSystemPrompt(nil, nil)
	if prompt != "" || len(blocks) != 0 {
		t.Fatal("expected empty for nil input")
	}
}

func TestAssembleSystemPromptSingleBlock(t *testing.T) {
	inst := &domain.CompiledInstructions{Base: []string{"only"}}
	prompt, blocks := assembleSystemPrompt(inst, nil)
	if prompt != "only" {
		t.Fatalf("expected 'only', got %q", prompt)
	}
	if len(blocks) != 1 || blocks[0].CacheControl != "ephemeral" {
		t.Fatal("single block should be ephemeral")
	}
}
