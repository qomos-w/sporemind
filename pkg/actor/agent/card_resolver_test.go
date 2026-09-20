package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/agentkit/cardcompiler"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestProjectCardFromRawUsesMetadataBeforeLegacyID(t *testing.T) {
	card, ok := projectCardFromRaw("custom-card", "---\ntype: prompt\nsource: project\ndata:\n  section: role\n---\nRole instructions")
	if !ok || card.Type != "prompt" || card.Source != "project" {
		t.Fatalf("metadata was not used: %#v", card)
	}
	if len(card.PromptContributions) != 1 || card.PromptContributions[0].Section != "role" {
		t.Fatalf("metadata section was not used: %#v", card.PromptContributions)
	}
}

func TestProjectCardFromRawParsesToolFrontmatter(t *testing.T) {
	card, ok := projectCardFromRaw("builtin:bundle:project-wiki", "---\ntags: [component, builtin, bundle]\ndata:\n  componentKind: bundle\n  source: builtin\n  tools:\n    - project.wiki_get_card\n    - project.wiki_list_cards\n---\nWiki tools")
	if !ok || len(card.ToolContributions) != 2 {
		t.Fatalf("raw card tools were not parsed: %#v", card)
	}
	result := compileTurnCardContext(domain.AgentComponentSnapshot{Mounts: []domain.AgentComponentMount{{CardID: card.ID, Enabled: true}}}, []domain.ToolSpec{{Name: "get_card", CallableID: "project.wiki_get_card"}}, nil, nil, []gen.MonoCard{card})
	if len(result.ToolSpec) != 1 || result.ToolSpec[0].CallableID != "project.wiki_get_card" {
		t.Fatalf("raw card tool did not reach compiler: %#v", result.ToolSpec)
	}
	if len(result.Tools) != 2 || result.Tools[0].ID != card.ID || result.Tools[0].Reason != "included" {
		t.Fatalf("tool source audit was not preserved: %#v", result.Tools)
	}
}

func TestProjectCardStoreAdapterExcludesSkillFromCompiler(t *testing.T) {
	store := cardcompiler.NewProjectCardStoreAdapter(cardcompiler.ProjectCardReaderFunc(func(id string) (gen.MonoCard, bool) {
		if id != "skill:review" {
			return gen.MonoCard{}, false
		}
		return projectCardFromRaw(id, "---\ntags: [component, skill]\n---\nReview the change.")
	}))
	result := compileTurnCardContextWithStore(store, domain.AgentComponentSnapshot{
		Mounts: []domain.AgentComponentMount{{CardID: "skill:review", Enabled: true, Order: 1}},
	}, nil, nil, nil)
	for _, block := range result.SystemBlocks {
		if strings.Contains(block.Text, "Review the change") {
			t.Fatalf("skill body should NOT reach compiler: %#v", result.SystemBlocks)
		}
	}
}

type batchCountingStore struct {
	cards     map[string]gen.MonoCard
	batchIDs  [][]string
	singleIDs []string
	batchable bool
}

func (s *batchCountingStore) GetCard(id string) (gen.MonoCard, bool) {
	s.singleIDs = append(s.singleIDs, id)
	card, ok := s.cards[id]
	return card, ok
}

func (s *batchCountingStore) GetCards(ids []string) map[string]gen.MonoCard {
	s.batchIDs = append(s.batchIDs, ids)
	if !s.batchable {
		return nil
	}
	out := map[string]gen.MonoCard{}
	for _, id := range ids {
		if card, ok := s.cards[id]; ok {
			out[id] = card
		}
	}
	return out
}

func promptMountCard(id, text string) gen.MonoCard {
	card, _ := projectCardFromRaw(id, "---\ntype: prompt\ndata:\n  section: role\n---\n"+text)
	return card
}

func TestCompileTurnCardContextPrefersBatchFetch(t *testing.T) {
	store := &batchCountingStore{
		batchable: true,
		cards: map[string]gen.MonoCard{
			"m-a":     promptMountCard("m-a", "role text a"),
			"m-b":     promptMountCard("m-b", "role text b"),
			"skill:x": promptMountCard("skill:x", "skill body"),
			"m-dup":   promptMountCard("m-dup", "role text dup"),
		},
	}
	result := compileTurnCardContextWithStore(store, domain.AgentComponentSnapshot{
		Mounts: []domain.AgentComponentMount{
			{CardID: "m-a", Enabled: true},
			{CardID: "m-b", Enabled: true},
			{CardID: "skill:x", Enabled: true},
			{CardID: "m-dup", Enabled: true},
			{CardID: "m-dup", Enabled: true},
			{CardID: "m-disabled", Enabled: false},
			{CardID: "m-missing", Enabled: true},
		},
	}, nil, nil, nil)
	if len(store.batchIDs) != 1 {
		t.Fatalf("expected exactly one batch fetch, got %v", store.batchIDs)
	}
	got := strings.Join(store.batchIDs[0], ",")
	if got != "m-a,m-b,m-dup,m-missing" {
		t.Fatalf("batch ids = %q, want unique non-skill enabled mounts (missing allowed)", got)
	}
	if len(store.singleIDs) != 0 {
		t.Fatalf("batch path must not fall back to per-id GetCard, got %v", store.singleIDs)
	}
	for _, want := range []string{"role text a", "role text b", "role text dup"} {
		found := false
		for _, block := range result.SystemBlocks {
			if block.Text == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q in compiled blocks: %#v", want, result.SystemBlocks)
		}
	}
	for _, block := range result.SystemBlocks {
		if strings.Contains(block.Text, "skill body") {
			t.Fatalf("skill body must not reach compiler: %#v", result.SystemBlocks)
		}
	}
}

func TestCompileTurnCardContextFallsBackWhenBatchUnavailable(t *testing.T) {
	store := &batchCountingStore{
		batchable: false,
		cards:     map[string]gen.MonoCard{"m-a": promptMountCard("m-a", "role text a")},
	}
	result := compileTurnCardContextWithStore(store, domain.AgentComponentSnapshot{
		Mounts: []domain.AgentComponentMount{{CardID: "m-a", Enabled: true}},
	}, nil, nil, nil)
	if len(store.batchIDs) != 1 {
		t.Fatalf("GetCards should have been attempted once, got %v", store.batchIDs)
	}
	found := false
	for _, block := range result.SystemBlocks {
		if block.Text == "role text a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("per-id fallback did not compile mounted card: %#v", result.SystemBlocks)
	}
}

func TestCompileTurnCardContextRejectsUnregisteredTools(t *testing.T) {
	result := compileTurnCardContext(domain.AgentComponentSnapshot{
		Tools: []domain.ComponentToolContribution{{ID: "missing", CardID: "capability", CallableID: "app.secret"}},
	}, nil, nil, nil)
	if len(result.ToolSpec) != 0 {
		t.Fatalf("unregistered tool entered compiled context: %#v", result.ToolSpec)
	}
	if len(result.Tools) != 1 || result.Tools[0].Reason != "registry_missing" {
		t.Fatalf("missing registry decision was not audited: %#v", result.Tools)
	}
}

func TestActiveContextCardsReturnsNil(t *testing.T) {
	// activeContextCards returns nothing — the active plan is deliberately not
	// injected into the prompt.
	a := Actor{}
	a.plan = planState{Task: "implement", Plan: "step one", Status: "approved"}
	cards := a.activeContextCards()
	if len(cards) != 0 {
		t.Fatalf("activeContextCards should return nil/empty, got %d cards: %#v", len(cards), cards)
	}
}

func TestActiveContextCardsExcludesWorktreeStatus(t *testing.T) {
	// Worktree is now represented by the mounted builtin:mode:worktree card,
	// so activeContextCards must not synthesize a worktree card here.
	a := Actor{worktreeID: "wt-1", worktreeName: "feature", worktreeStatus: "active"}
	cards := a.activeContextCards()
	if len(cards) != 0 {
		t.Fatalf("expected no synthetic worktree card, got %#v", cards)
	}
}

func TestComponentSnapshotCardsUseCompilerContributions(t *testing.T) {
	snapshot := domain.AgentComponentSnapshot{
		Prompts: []domain.ComponentPromptContribution{
			{ID: "resolved", CardID: "card-resolved", Text: "task", Placement: "resolved", Priority: 20},
			{ID: "system", CardID: "card-system", Text: "policy", Placement: "system", Priority: 10},
		},
		Tools: []domain.ComponentToolContribution{
			{ID: "tool", CardID: "card-tool", CallableID: "project.read", Priority: 3},
		},
	}
	result := compileComponentSnapshot(snapshot, map[string][]gen.ToolSpec{
		"project.read": {{Name: "file_read", CallableID: "project.read"}},
	})
	if len(result.SystemBlocks) != 2 || result.SystemBlocks[0].Text != "policy" || result.SystemBlocks[1].Text != "task" {
		t.Fatalf("unexpected compiled blocks: %#v", result.SystemBlocks)
	}
	if len(result.ToolSpec) != 1 || result.ToolSpec[0].CallableID != "project.read" {
		t.Fatalf("unexpected compiled tools: %#v", result.ToolSpec)
	}
}

func TestCompileTurnInstructionsIncludesWorktreeNotPlan(t *testing.T) {
	a := &Actor{}
	a.worktreeStatus = "active"
	a.worktreeName = "feature-branch"
	a.worktreeID = "wt-123"
	a.plan = planState{Task: "implement feature", Plan: "1. do A then B", Status: "approved"}

	// Worktree context now travels through the builtin:mode:worktree component
	// card, mirrored here in the snapshot.
	snapshot := domain.AgentComponentSnapshot{
		Revision: 1,
		Prompts: []domain.ComponentPromptContribution{
			{ID: "wt-ctx", CardID: "builtin:mode:worktree", Text: "You are operating in an isolated git worktree.", Placement: "worktree_status"},
		},
	}

	dynamic := a.activeContextCards()
	result := compileTurnCardContextWithStoreAndFilters(nil, snapshot, nil, nil, nil, nil, dynamic)
	inst := compiledInstructions(result)

	if inst == nil {
		t.Fatal("expected non-nil instructions")
	}

	var foundWorktree bool
	for _, r := range inst.Resolved {
		if strings.Contains(r, "worktree_status") || strings.Contains(r, "isolated git worktree") {
			foundWorktree = true
		}
		// Plan must NOT appear in Resolved — it is in HotContext.
		if strings.Contains(r, "active_plan") || strings.Contains(r, "implement feature") {
			t.Fatalf("plan must NOT be in Resolved, got %q", r)
		}
	}
	if !foundWorktree {
		t.Fatalf("expected worktree_status in Resolved, got %v", inst.Resolved)
	}
}

func TestActiveContextCardsExcludesGoalAndPlan(t *testing.T) {
	a := &Actor{
		RawSession: domain.RawSession{
			Goal: &gen.SessionGoal{
				Condition:       "refactor auth module",
				InterpretedGoal: "Refactor the authentication module",
				MaxTurns:        20,
				TurnCount:       3,
				Confirmed:       true,
				Status:          "active",
			},
		},
	}
	a.plan = planState{Task: "implement auth", Plan: "steps here", Status: "approved"}

	cards := a.activeContextCards()
	// activeContextCards returns nil; goal and plan are in HotContext.
	if len(cards) != 0 {
		t.Fatalf("expected 0 cards, got %d: %#v", len(cards), cards)
	}
}

func TestResolveHotContextReturnsGoalBlock(t *testing.T) {
	a := &Actor{
		RawSession: domain.RawSession{
			Goal: &gen.SessionGoal{
				Condition: "implement the feature",
				MaxTurns:  10,
				TurnCount: 2,
				Confirmed: true,
				Status:    "active",
			},
		},
	}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	blocks := a.resolveHotContext(ctx)
	if blocks == nil {
		t.Fatal("expected non-nil blocks from resolveHotContext with goal set")
	}
	if len(blocks) == 0 {
		t.Fatal("expected at least one content block")
	}
	if !strings.Contains(blocks[0].Text, "## Active Goal") {
		t.Fatalf("expected block text to contain '## Active Goal', got %q", blocks[0].Text)
	}
}

func TestComponentSnapshotCardsPlacementOrdering(t *testing.T) {
	// Configurable placement must drive section ordering: leading (before role),
	// system→policy (after role, the default), and trailing (end of system
	// prompt, ahead of conversation history).
	snapshot := domain.AgentComponentSnapshot{
		Prompts: []domain.ComponentPromptContribution{
			{ID: "p-after", CardID: "prompt:fragment:after", Placement: "system", Text: "after role"},
			{ID: "p-lead", CardID: "prompt:fragment:lead", Placement: "leading", Text: "leading"},
			{ID: "p-trail", CardID: "prompt:fragment:trail", Placement: "trailing", Text: "trailing"},
		},
	}
	cards := componentSnapshotCards(snapshot)
	result := cardcompiler.Compile(cards, cardcompiler.CompileOptions{})

	wantSections := []string{"leading", "policy", "trailing"}
	var gotSections []string
	for _, s := range result.Sections {
		gotSections = append(gotSections, s.Section)
	}
	if strings.Join(gotSections, ",") != strings.Join(wantSections, ",") {
		t.Fatalf("section order = %v, want %v", gotSections, wantSections)
	}

	wantTexts := []string{"leading", "after role", "trailing"}
	if len(result.SystemBlocks) != len(wantTexts) {
		t.Fatalf("SystemBlocks = %v, want %v", systemBlockTexts(result.SystemBlocks), wantTexts)
	}
	for i, want := range wantTexts {
		if result.SystemBlocks[i].Text != want {
			t.Fatalf("SystemBlocks = %v, want %v", systemBlockTexts(result.SystemBlocks), wantTexts)
		}
	}
}

func TestCompiledInstructionsGroupsLeadingAndTrailing(t *testing.T) {
	result := cardcompiler.CompileResult{
		SystemBlocks: []gen.SystemContentBlock{
			{Text: "lead text"},
			{Text: "role text"},
			{Text: "policy text"},
			{Text: "trailing text"},
			{Text: "task text"},
		},
		ContextSnapshot: cardcompiler.ContextSnapshot{
			Sections: []cardcompiler.ContextSectionRecord{
				{Section: "leading"},
				{Section: "role"},
				{Section: "policy"},
				{Section: "trailing"},
				{Section: "task"},
			},
		},
	}
	instr := compiledInstructions(result)
	wantBase := []string{"lead text", "role text", "policy text"}
	wantResolved := []string{"trailing text", "task text"}
	if strings.Join(instr.Base, "|") != strings.Join(wantBase, "|") {
		t.Fatalf("Base = %v, want %v", instr.Base, wantBase)
	}
	if strings.Join(instr.Resolved, "|") != strings.Join(wantResolved, "|") {
		t.Fatalf("Resolved = %v, want %v", instr.Resolved, wantResolved)
	}
}

func systemBlockTexts(blocks []gen.SystemContentBlock) []string {
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = b.Text
	}
	return out
}
