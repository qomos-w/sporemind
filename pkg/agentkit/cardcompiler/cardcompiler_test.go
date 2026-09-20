package cardcompiler

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestResolveOrdersDeduplicatesAndCompiles(t *testing.T) {
	store := CardMap{
		"base":  {ID: "base", Type: "prompt", PromptContributions: []gen.CardPromptContribution{{ID: "p-base", Section: "policy", Text: "policy"}}},
		"child": {ID: "child", Type: "prompt", Dependencies: []gen.CardRef{{ID: "base"}}, PromptContributions: []gen.CardPromptContribution{{ID: "p-task", Section: "task", Text: "task"}}, ToolContributions: []gen.CardToolContribution{{ID: "t", CallableID: "run"}}},
	}
	resolver := Resolver{Store: store}
	result, err := resolver.Compile([]gen.CardRef{{ID: "child"}, {ID: "base"}}, CompileOptions{ToolSpecs: map[string][]gen.ToolSpec{"run": {{Name: "run", InputSchema: "{}"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SystemBlocks) != 2 || result.SystemBlocks[0].Text != "policy" || result.SystemBlocks[1].Text != "task" {
		t.Fatalf("unexpected blocks: %#v", result.SystemBlocks)
	}
	if len(result.ToolSpec) != 1 || result.ToolSpec[0].Name != "run" {
		t.Fatalf("unexpected tools: %#v", result.ToolSpec)
	}
	if len(result.Messages) != 0 {
		t.Fatalf("compiler result must preserve messages: %#v", result.Messages)
	}
	messages := []gen.ChatMessage{{ID: "m1", Role: "user"}}
	withMessages := Compile([]gen.MonoCard{{ID: "message", Type: "prompt"}}, CompileOptions{Messages: messages})
	if len(withMessages.Messages) != 1 || withMessages.Messages[0].ID != "m1" {
		t.Fatalf("conversation messages were not returned: %#v", withMessages.Messages)
	}
}

func TestCompileSnapshotAuditsSourcesSectionsAndTools(t *testing.T) {
	result := Compile([]gen.MonoCard{
		{ID: "z", Source: "project", Version: 2, PromptContributions: []gen.CardPromptContribution{{ID: "z-task", Section: "task", Text: "task"}}, ToolContributions: []gen.CardToolContribution{{CallableID: "missing", Priority: 2}}},
		{ID: "a", Source: "builtin", Version: 1, PromptContributions: []gen.CardPromptContribution{{ID: "a-policy", Section: "policy", Text: "policy"}}, ToolContributions: []gen.CardToolContribution{{CallableID: "known", Priority: 1}}},
	}, CompileOptions{ToolSpecs: map[string][]gen.ToolSpec{"known": {{Name: "known"}}}})
	if len(result.Cards) != 2 || result.Cards[0].Source != "project" || result.Cards[0].Version != 2 {
		t.Fatalf("missing card audit: %#v", result.Cards)
	}
	if result.SystemBlocks[0].Text != "policy" || result.SystemBlocks[1].Text != "task" {
		t.Fatalf("unstable section ordering: %#v", result.SystemBlocks)
	}
	if len(result.Tools) != 2 || result.Tools[0].Reason != "registry_missing" || !result.Tools[1].Included {
		t.Fatalf("tool audit mismatch: %#v", result.Tools)
	}
}

func TestCompileSnapshotAuditsFilters(t *testing.T) {
	result, err := (Resolver{Store: CardMap{
		"policy":    {ID: "policy", Type: "policy", Source: "builtin", Version: 3},
		"knowledge": {ID: "knowledge", Type: "knowledge", Source: "kb", Version: 4},
		"concept":   {ID: "concept", Type: "concept", Source: "concepts", Version: 5},
	}}).Compile([]gen.CardRef{{ID: "policy"}, {ID: "knowledge"}, {ID: "concept"}, {ID: "policy", Disabled: true}}, CompileOptions{ResolveOptions: ResolveOptions{ExcludeKnowledge: true, ExcludeConcept: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Filtered) != 3 || result.Filtered[0].Reason != "concept_excluded" || result.Filtered[1].Reason != "knowledge_excluded" || result.Filtered[2].Reason != "disabled" {
		t.Fatalf("unexpected filters: %#v", result.Filtered)
	}
}

func TestCompileSnapshotAuditsPolicyFilteredTools(t *testing.T) {
	result := Compile([]gen.MonoCard{{ID: "bundle", ToolContributions: []gen.CardToolContribution{{CallableID: "blocked"}}}}, CompileOptions{
		ToolSpecs:         map[string][]gen.ToolSpec{},
		ToolFilterReasons: map[string]string{"blocked": "policy_filtered"},
	})
	if len(result.Tools) != 1 || result.Tools[0].Included || result.Tools[0].Reason != "policy_filtered" {
		t.Fatalf("expected policy filter audit: %#v", result.Tools)
	}
}

func TestResolveDetectsCycle(t *testing.T) {
	store := CardMap{
		"a": {ID: "a", Dependencies: []gen.CardRef{{ID: "b"}}},
		"b": {ID: "b", Dependencies: []gen.CardRef{{ID: "a"}}},
	}
	_, err := (Resolver{Store: store}).Resolve([]gen.CardRef{{ID: "a"}})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestResolveExcludesOnDemandKnowledgeAndConcept(t *testing.T) {
	store := CardMap{
		"prompt":    {ID: "prompt", Type: "prompt"},
		"knowledge": {ID: "knowledge", Type: "knowledge"},
		"concept":   {ID: "concept", Type: "concept"},
		"ondemand":  {ID: "ondemand", Type: "prompt", OnDemand: true},
	}
	cards, err := (Resolver{Store: store, Options: ResolveOptions{ExcludeKnowledge: true, ExcludeConcept: true}}).Resolve([]gen.CardRef{{ID: "prompt"}, {ID: "knowledge"}, {ID: "concept"}, {ID: "ondemand"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].ID != "prompt" {
		t.Fatalf("unexpected cards: %#v", cards)
	}
}

// TestCompile_ForkAliasesNotCollapsed verifies that multiple tool specs
// sharing one CallableID (fork_explore/fork_review/fork_general all routed to
// agent.fork_agent) are all emitted, not collapsed into one. The card
// contribution carries a single CallableID; the specs map carries all aliases.
func TestCompile_ForkAliasesNotCollapsed(t *testing.T) {
	cards := []gen.MonoCard{{
		ID: "fork-bundle",
		Type: "bundle",
		ToolContributions: []gen.CardToolContribution{{ID: "t", CallableID: "fork_agent"}},
	}}
	specs := map[string][]gen.ToolSpec{
		"fork_agent": {
			{Name: "fork_explore", CallableID: "fork_agent", InputSchema: "{}"},
			{Name: "fork_review", CallableID: "fork_agent", InputSchema: "{}"},
			{Name: "fork_general", CallableID: "fork_agent", InputSchema: "{}"},
		},
	}
	result := Compile(cards, CompileOptions{ToolSpecs: specs})
	if len(result.ToolSpec) != 3 {
		t.Fatalf("expected 3 fork alias specs, got %d: %#v", len(result.ToolSpec), result.ToolSpec)
	}
	names := map[string]bool{}
	for _, s := range result.ToolSpec {
		names[s.Name] = true
	}
	for _, want := range []string{"fork_explore", "fork_review", "fork_general"} {
		if !names[want] {
			t.Errorf("missing fork alias %q in %#v", want, result.ToolSpec)
		}
	}
}
