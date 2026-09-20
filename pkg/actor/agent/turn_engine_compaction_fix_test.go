package agent

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestFinalizeDispatch_CompactionPreservesToolUse verifies that when mid-turn
// compaction triggers during finalizeDispatch Case 1, the assistant(tool_use)
// message is preserved in e.history. Previously, appendMessage ran before
// onCompact, and onCompact replaced e.history via compileMessages(true) —
// discarding the tool_use because the current iteration's LLM step hadn't
// been flushed to a.steps yet. The fix moves appendMessage after onCompact.
func TestFinalizeDispatch_CompactionPreservesToolUse(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	compactionRan := false
	e := &turnEngine{
		logger:   newNopActorLogger(),
		turnID:   "turn-1",
		stepByID: map[string]domain.TurnAction{},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				ContextWindowSize: 10000,
				CompactionPolicy: &domain.CompactionPolicy{
					TokenBudget: 100, // small budget so 200 input tokens triggers compaction
				},
			},
		},
	}
	e.onCompact = func(_ actor.Context, _, _ string) error {
		compactionRan = true
		// Simulate compileMessages(true) replacing e.history with old
		// messages only (the current LLM step is not yet in a.steps).
		e.history = []domain.ChatMessage{
			{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "old msg"}}},
		}
		return nil
	}

	e.finalizeDispatch(ctx, "turn-1", dispatchResult{
		iterText:   "Let me check that.",
		iterUsage:  &domain.UsageData{InputTokens: 200},
		stopReason: "tool_use",
		pending: []pendingToolCall{
			{ID: "tu-1", LLMName: "fs.read", CallableID: "project.read", Input: `{"path":"a.txt"}`},
		},
	})

	if !compactionRan {
		t.Fatal("expected onCompact to have been called")
	}

	// The last message must be the assistant(tool_use) — not lost to the
	// compaction history rebuild.
	if len(e.history) < 2 {
		t.Fatalf("expected at least 2 messages after compaction, got %d: %+v", len(e.history), e.history)
	}
	last := e.history[len(e.history)-1]
	if last.Role != domain.ChatRoleAssistant {
		t.Fatalf("expected last message role=assistant, got %s", last.Role)
	}
	var foundToolUse bool
	for _, b := range last.Content {
		if b.Type == domain.ContentBlockToolUse && b.ToolUseID == "tu-1" {
			foundToolUse = true
		}
	}
	if !foundToolUse {
		t.Fatalf("assistant message missing tool_use block tu-1, content=%+v", last.Content)
	}

	// pendingCalls should be set for phaseAudit.
	if len(e.pendingCalls) != 1 || e.pendingCalls[0].ID != "tu-1" {
		t.Fatalf("expected pendingCalls=[tu-1], got %+v", e.pendingCalls)
	}
}

// TestShouldCompactAfterDispatch_CircuitBreaker verifies that once compaction
// has failed maxCompactionFailures times, shouldCompactAfterDispatch returns
// false for the rest of the turn — breaking the "dispatch → compaction-timeout
// → dispatch" loop that freezes the conversation when the summarizer is dead.
func TestShouldCompactAfterDispatch_CircuitBreaker(t *testing.T) {
	e := &turnEngine{
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				ContextWindowSize: 10000,
				CompactionPolicy: &domain.CompactionPolicy{
					TokenBudget: 100,
				},
			},
		},
	}
	if e.shouldCompactAfterDispatch(50) {
		t.Fatal("expected no compaction under budget")
	}
	if !e.shouldCompactAfterDispatch(200) {
		t.Fatal("expected compaction over budget with no prior failure")
	}
	// One failure trips the breaker: no more auto-compaction this turn even
	// though tokens still exceed the budget.
	e.compactionFailures = maxCompactionFailures
	if e.shouldCompactAfterDispatch(200) {
		t.Fatal("expected circuit breaker to block compaction after a failure")
	}
}
