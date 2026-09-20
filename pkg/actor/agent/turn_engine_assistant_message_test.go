package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestFinalizeDispatch_AssistantMessagesUniqueIDAndUsage verifies that when
// multiple assistant ChatMessages are written to history during a single turn
// (via repeated finalizeDispatch calls), each gets a distinct message ID
// (not the shared turnID) and carries a non-nil Usage snapshot copied from the
// dispatch result. Previously all assistant messages shared ID: turnID and
// omitted Usage, which prevented per-message attribution in trajectory
// storage.
func TestFinalizeDispatch_AssistantMessagesUniqueIDAndUsage(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	e := &turnEngine{
		logger:   newNopActorLogger(),
		turnID:   "turn-1",
		stepByID: map[string]domain.TurnAction{},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				ContextWindowSize: 100000, // large window so compaction never fires
			},
		},
	}

	// First dispatch: tool_use response with usage.
	e.finalizeDispatch(ctx, "turn-1", dispatchResult{
		iterText:   "Let me read the file.",
		iterUsage:  &domain.UsageData{InputTokens: 100, OutputTokens: 50},
		stopReason: "tool_use",
		pending: []pendingToolCall{
			{ID: "tu-1", LLMName: "project.read", CallableID: "project.read", Input: `{"path":"a.txt"}`},
		},
	})

	// Second dispatch: plain text completion with usage.
	e.finalizeDispatch(ctx, "turn-1", dispatchResult{
		iterText:   "Here is the result.",
		iterUsage:  &domain.UsageData{InputTokens: 200, OutputTokens: 80},
		stopReason: "stop",
	})

	// Collect assistant messages.
	var asstMsgs []domain.ChatMessage
	for _, msg := range e.history {
		if msg.Role == domain.ChatRoleAssistant {
			asstMsgs = append(asstMsgs, msg)
		}
	}

	if len(asstMsgs) < 2 {
		t.Fatalf("expected at least 2 assistant messages, got %d: %+v", len(asstMsgs), asstMsgs)
	}

	// Verify each assistant message has a distinct ID and non-nil Usage with
	// a non-zero token count.
	seen := make(map[string]struct{})
	for i, msg := range asstMsgs {
		if msg.ID == "" || msg.ID == "turn-1" {
			t.Errorf("assistant message %d has invalid/shared ID: %s", i, msg.ID)
		}
		if msg.Usage == nil {
			t.Errorf("assistant message %d missing Usage", i)
		} else if msg.Usage.InputTokens == 0 {
			t.Errorf("assistant message %d has empty Usage (InputTokens=0)", i)
		}
		if _, dup := seen[msg.ID]; dup {
			t.Errorf("duplicate assistant message ID: %s", msg.ID)
		}
		seen[msg.ID] = struct{}{}
	}

	// Ensure the two Usage snapshots are independent copies and reflect their
	// respective dispatch results.
	if asstMsgs[0].Usage != nil && asstMsgs[1].Usage != nil {
		if asstMsgs[0].Usage.InputTokens == asstMsgs[1].Usage.InputTokens {
			t.Errorf("expected distinct Usage snapshots, both have InputTokens=%d",
				asstMsgs[0].Usage.InputTokens)
		}
	}
}
