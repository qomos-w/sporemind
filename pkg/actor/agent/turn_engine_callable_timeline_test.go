package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestCallableConceptTreeResultReturnsToStepTimelineAndConversation(t *testing.T) {
	assertCallableResultReachesAllPaths(t,
		"project.wiki_get_concept_tree",
		`{"root":{"id":"concept:runtime","parentId":"concept:root","title":"Runtime Concept"}}`,
		"concept-step",
		"concept-call",
	)
}

func assertCallableResultReachesAllPaths(t *testing.T, callableID, output, stepID, toolUseID string) {
	t.Helper()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.openToolCalls = 1
	step := domain.TurnAction{
		ID:         stepID,
		Kind:       string(domain.TurnActionToolCall),
		State:      "running",
		CallableID: callableID,
		ToolUseID:  toolUseID,
	}
	result := toolExecutionResult{
		call: pendingToolCall{
			ID:         toolUseID,
			CallableID: callableID,
		},
		out: output,
	}

	if err := e.processBatchResults(ctx, "turn-callable", []toolExecutionResult{result}, []domain.TurnAction{step}); err != nil {
		t.Fatalf("processBatchResults returned error: %v", err)
	}

	stored, ok := e.stepByID[stepID]
	if !ok {
		t.Fatalf("callable result did not update step %q", stepID)
	}
	if stored.State != "completed" || stored.Output != output || stored.Text != output {
		t.Fatalf("step result = %+v, want completed output %q", stored, output)
	}

	var block, closed bool
	for _, event := range e.stepEvents {
		if event.StepID != stepID || event.TurnID != "turn-callable" {
			continue
		}
		switch event.Kind {
		case "block.appended":
			if event.Block == nil || event.Block.Type != domain.ContentBlockToolResult || event.Block.ToolUseID != toolUseID || event.Block.Text != output {
				t.Fatalf("unexpected tool-result timeline event: %+v", event)
			}
			block = true
		case "step.closed":
			closed = true
		}
	}
	if !block || !closed {
		t.Fatalf("callable result timeline events: block=%v closed=%v, events=%+v", block, closed, e.stepEvents)
	}

	var conversation bool
	for _, message := range e.history {
		if message.Role != domain.ChatRoleTool {
			continue
		}
		for _, content := range message.Content {
			if content.Type == domain.ContentBlockToolResult && content.ToolUseID == toolUseID && content.Text == output {
				conversation = true
			}
		}
	}
	if !conversation {
		t.Fatalf("callable result missing from conversation history: %+v", e.history)
	}
}
