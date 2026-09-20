package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestCompileMessagesReasoningStep verifies that a persisted reasoning step
// (Type="reasoning", ReasoningContent set, Content empty) followed by a text
// step from the same turn are merged into a single ChatMessage. The merged
// message carries both ReasoningContent (from the reasoning step) and Content
// (from the text step) — so the LLM receives reasoning alongside the assistant
// output it belongs to, without creating consecutive assistant messages.
func TestCompileMessagesReasoningStep(t *testing.T) {
	turns := []domain.Turn{
		{ID: "turn-1", Role: "user"},
		{ID: "turn-2", Role: "assistant"},
	}
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1",
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
		{ID: "rs-1", Role: "assistant", Type: "reasoning", TurnID: "turn-2",
			ReasoningContent: "thinking..."},
		{ID: "s-1", Role: "assistant", Type: "text", TurnID: "turn-2",
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
	}

	a := newCompileTestActor(turns, nil, steps)
	msgs := a.compileMessages(true)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (reasoning merged into text), got %d: %+v", len(msgs), msgs)
	}

	if msgs[0].Role != "user" {
		t.Errorf("msgs[0].Role = %q, want user", msgs[0].Role)
	}

	if msgs[1].Role != "assistant" {
		t.Errorf("msgs[1].Role = %q, want assistant", msgs[1].Role)
	}
	if msgs[1].ReasoningContent != "thinking..." {
		t.Errorf("msgs[1].ReasoningContent = %q, want 'thinking...'", msgs[1].ReasoningContent)
	}
	if len(msgs[1].Content) != 1 || msgs[1].Content[0].Text != "hello" {
		t.Errorf("msgs[1].Content = %+v, want single text block 'hello'", msgs[1].Content)
	}
}

// TestStepToChatMessageReasoningRoundTrip ensures step <-> ChatMessage conversion
// is symmetric for reasoning content: reasoning step becomes a message with
// ReasoningContent only, and converting back preserves Type/ReasoningContent
// and keeps Content empty.
func TestStepToChatMessageReasoningRoundTrip(t *testing.T) {
	step := domain.Step{
		ID:               "rs-1",
		Role:             "assistant",
		Type:             "reasoning",
		ReasoningContent: "thinking...",
		TurnID:           "turn-1",
	}

	msg := stepToChatMessage(step)
	if msg.ReasoningContent != "thinking..." {
		t.Errorf("msg.ReasoningContent = %q, want 'thinking...'", msg.ReasoningContent)
	}
	if len(msg.Content) != 0 {
		t.Errorf("msg.Content = %+v, want empty", msg.Content)
	}

	back := chatMessageToStep(msg, "turn-1")
	if back.Type != "reasoning" {
		t.Errorf("back.Type = %q, want reasoning", back.Type)
	}
	if back.ReasoningContent != "thinking..." {
		t.Errorf("back.ReasoningContent = %q, want 'thinking...'", back.ReasoningContent)
	}
	if len(back.Content) != 0 {
		t.Errorf("back.Content = %+v, want empty", back.Content)
	}
}

// TestStepToChatMessageTextPreservesContent verifies non-reasoning steps still
// carry their Content through the conversion and leave ReasoningContent empty.
func TestStepToChatMessageTextPreservesContent(t *testing.T) {
	step := domain.Step{
		ID:      "s-1",
		Role:    "assistant",
		Type:    "text",
		TurnID:  "turn-1",
		Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}},
	}

	msg := stepToChatMessage(step)
	if msg.ReasoningContent != "" {
		t.Errorf("msg.ReasoningContent = %q, want empty", msg.ReasoningContent)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text != "hi" {
		t.Errorf("msg.Content = %+v, want single text block 'hi'", msg.Content)
	}
}

// TestMessageHasContentReasoningOnly checks that a message with only
// ReasoningContent (no Content blocks) is still treated as having content,
// so compileMessages does not skip it.
func TestMessageHasContentReasoningOnly(t *testing.T) {
	msg := domain.ChatMessage{
		Role:             domain.ChatRoleAssistant,
		ReasoningContent: "thinking...",
	}
	if !messageHasContent(msg) {
		t.Error("messageHasContent returned false for reasoning-only message")
	}
}

// TestApplyStepEventReasoningDelta verifies block.delta on a reasoning step
// accumulates into Step.ReasoningContent and leaves Content empty.
func TestApplyStepEventReasoningDelta(t *testing.T) {
	a := &Actor{
		steps: []domain.Step{
			{ID: "rs-1", Role: "assistant", Type: "reasoning", TurnID: "turn-1"},
		},
	}

	a.applyStepEvent(domain.StepEvent{
		Kind:   "block.delta",
		StepID: "rs-1",
		Delta:  "Thinking...",
	})

	if a.steps[0].ReasoningContent != "Thinking..." {
		t.Errorf("ReasoningContent = %q, want 'Thinking...'", a.steps[0].ReasoningContent)
	}
	if len(a.steps[0].Content) != 0 {
		t.Errorf("Content = %+v, want empty", a.steps[0].Content)
	}

	a.applyStepEvent(domain.StepEvent{
		Kind:   "block.delta",
		StepID: "rs-1",
		Delta:  " More.",
	})
	if a.steps[0].ReasoningContent != "Thinking... More." {
		t.Errorf("ReasoningContent = %q, want 'Thinking... More.'", a.steps[0].ReasoningContent)
	}
}

// TestApplyStepEventReasoningOpenedNoBlock ensures step.opened for a reasoning
// step does not seed a Content block.
func TestApplyStepEventReasoningOpenedNoBlock(t *testing.T) {
	a := &Actor{}
	a.applyStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   "rs-1",
		TurnID:   "turn-1",
		StepType: "reasoning",
		Role:     "assistant",
		// Block intentionally omitted; even if it were set, it should be ignored
		// for reasoning steps.
	})

	if len(a.steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(a.steps))
	}
	if a.steps[0].Type != "reasoning" {
		t.Errorf("Type = %q, want reasoning", a.steps[0].Type)
	}
	if len(a.steps[0].Content) != 0 {
		t.Errorf("Content = %+v, want empty", a.steps[0].Content)
	}
}

// TestApplyStepEventTextOpenedKeepsBlock ensures non-reasoning step.opened
// continues to seed a Content block when Block is provided.
func TestApplyStepEventTextOpenedKeepsBlock(t *testing.T) {
	a := &Actor{}
	a.applyStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   "s-1",
		TurnID:   "turn-1",
		StepType: "text",
		Role:     "assistant",
		Block:    &domain.ContentBlock{Type: domain.ContentBlockText, Text: ""},
	})

	if len(a.steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(a.steps))
	}
	if a.steps[0].Type != "text" {
		t.Errorf("Type = %q, want text", a.steps[0].Type)
	}
	if len(a.steps[0].Content) != 1 {
		t.Errorf("Content = %+v, want 1 block", a.steps[0].Content)
	}
}

// TestStepToChatMessageRoundTripPreservesUsageAndTimes verifies that Usage,
// StartedAt and CompletedAt are carried through both directions of the
// Step <-> ChatMessage conversion and survive a full round trip.
func TestStepToChatMessageRoundTripPreservesUsageAndTimes(t *testing.T) {
	usage := &domain.UsageData{
		InputTokens:  12,
		OutputTokens: 34,
		TotalTokens:  46,
	}
	step := domain.Step{
		ID:          "s-usage",
		Role:        "assistant",
		Type:        "text",
		TurnID:      "turn-1",
		Content:     []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}},
		Usage:       usage,
		StartedAt:   "2026-08-17T10:00:00.000000000Z",
		CompletedAt: "2026-08-17T10:00:01.000000000Z",
	}

	msg := stepToChatMessage(step)
	if msg.Usage == nil || msg.Usage.InputTokens != 12 || msg.Usage.OutputTokens != 34 || msg.Usage.TotalTokens != 46 {
		t.Errorf("stepToChatMessage lost Usage: got %+v, want %+v", msg.Usage, usage)
	}
	if msg.StartedAt != step.StartedAt {
		t.Errorf("stepToChatMessage StartedAt = %q, want %q", msg.StartedAt, step.StartedAt)
	}
	if msg.CompletedAt != step.CompletedAt {
		t.Errorf("stepToChatMessage CompletedAt = %q, want %q", msg.CompletedAt, step.CompletedAt)
	}

	back := chatMessageToStep(msg, "turn-2")
	if back.Usage == nil || back.Usage.InputTokens != 12 || back.Usage.OutputTokens != 34 || back.Usage.TotalTokens != 46 {
		t.Errorf("chatMessageToStep lost Usage: got %+v, want %+v", back.Usage, usage)
	}
	if back.StartedAt != step.StartedAt {
		t.Errorf("chatMessageToStep StartedAt = %q, want %q", back.StartedAt, step.StartedAt)
	}
	if back.CompletedAt != step.CompletedAt {
		t.Errorf("chatMessageToStep CompletedAt = %q, want %q", back.CompletedAt, step.CompletedAt)
	}
}

// TestStepToChatMessageReasoningRoundTripPreservesUsageAndTimes verifies that
// reasoning-only steps also carry Usage and time fields through conversion.
func TestStepToChatMessageReasoningRoundTripPreservesUsageAndTimes(t *testing.T) {
	usage := &domain.UsageData{OutputTokens: 7}
	step := domain.Step{
		ID:               "rs-usage",
		Role:             "assistant",
		Type:             "reasoning",
		TurnID:           "turn-1",
		ReasoningContent: "thinking...",
		Usage:            usage,
		StartedAt:        "2026-08-17T10:00:00.000000000Z",
		CompletedAt:      "2026-08-17T10:00:01.000000000Z",
	}

	msg := stepToChatMessage(step)
	if msg.Usage == nil || msg.Usage.OutputTokens != 7 {
		t.Errorf("reasoning stepToChatMessage lost Usage: got %+v", msg.Usage)
	}
	if msg.StartedAt != step.StartedAt || msg.CompletedAt != step.CompletedAt {
		t.Errorf("reasoning stepToChatMessage lost time fields: got StartedAt=%q CompletedAt=%q", msg.StartedAt, msg.CompletedAt)
	}
	if msg.Content != nil {
		t.Errorf("reasoning ChatMessage should have no Content, got %+v", msg.Content)
	}

	back := chatMessageToStep(msg, "turn-1")
	if back.Type != "reasoning" {
		t.Errorf("back.Type = %q, want reasoning", back.Type)
	}
	if back.Usage == nil || back.Usage.OutputTokens != 7 {
		t.Errorf("reasoning chatMessageToStep lost Usage: got %+v", back.Usage)
	}
	if back.StartedAt != step.StartedAt || back.CompletedAt != step.CompletedAt {
		t.Errorf("reasoning chatMessageToStep lost time fields: got StartedAt=%q CompletedAt=%q", back.StartedAt, back.CompletedAt)
	}
}
