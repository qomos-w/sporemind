package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/actor/agent/memory"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newCompileTestActor(turns []domain.Turn, summarySegments []domain.SummarySegment, steps []domain.Step) *Actor {
	a := &Actor{
		Session: domain.Session{
			ActiveHead: int32(len(turns) - 1),
			Turns:      turns,
		},
		RawSession: domain.RawSession{
			SummarySegments: summarySegments,
		},
		steps: steps,
	}
	if a.Session.ActiveHead < 0 && len(turns) > 0 {
		a.Session.ActiveHead = 0
	}
	return a
}

func TestMemoryModeToolsAppearInPromptArtifact(t *testing.T) {
	memoryDesc := domain.ComponentDescriptor{
		Ref:   domain.ComponentRef{CardID: "builtin:mode:memory", Kind: "mode", Source: "builtin"},
		Title: "Memory Mode",
		Icon:  "🧠",
		Prompts: []domain.ComponentPromptContribution{
			{ID: "mem-1", CardID: "builtin:mode:memory", Text: "You have access to a persistent memory graph.", Placement: "system"},
		},
		Tools: []domain.ComponentToolContribution{
			{ID: "mem-save", CardID: "builtin:mode:memory", CallableID: "memory_save"},
			{ID: "mem-recall", CardID: "builtin:mode:memory", CallableID: "memory_recall"},
		},
	}
	descriptors := map[string]domain.ComponentDescriptor{
		"builtin:mode:memory": memoryDesc,
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{
		agentKind: "coder",
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "builtin:mode:memory", Enabled: true, Scope: "user", Kind: "mode", Order: 10},
		},
		Graph: memory.New(memory.Config{}),
	}
	// Pre-set kind config so fetchAgentKindConfig doesn't need workspace RPC.
	a.kindConfig.Store(&domain.AgentKindConfig{Kind: "coder"})

	snapshot := a.resolveComponentSnapshot(ctx)
	hasSave := false
	hasRecall := false
	for _, tool := range snapshot.Tools {
		if tool.CallableID == "memory_save" {
			hasSave = true
		}
		if tool.CallableID == "memory_recall" {
			hasRecall = true
		}
	}
	if !hasSave {
		t.Fatalf("component snapshot missing memory_save tool, got %+v", snapshot.Tools)
	}
	if !hasRecall {
		t.Fatalf("component snapshot missing memory_recall tool, got %+v", snapshot.Tools)
	}

	// Now verify the tools appear in the prompt artifact.
	artifact := a.buildPromptArtifactStateful(ctx)
	toolFrag := false
	for _, f := range artifact.Fragments {
		if f.Kind == "tool" && (f.Name == "save" || f.Name == "recall" || strings.Contains(f.Content, "memory_save") || strings.Contains(f.Content, "memory_recall")) {
			toolFrag = true
			break
		}
	}
	if !toolFrag {
		var toolNames []string
		for _, f := range artifact.Fragments {
			if f.Kind == "tool" {
				toolNames = append(toolNames, f.Name)
			}
		}
		t.Fatalf("memory tools not found in prompt artifact tool fragments. Tool fragments: %v", toolNames)
	}
}

func TestCompileMessages_NormalizesAgentMessageRole(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{{
		ID:      "agent-msg",
		Role:    "agent",
		Type:    "text",
		TurnID:  "turn-1",
		Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "peer message"}},
	}}

	a := newCompileTestActor(turns, nil, steps)
	msgs := a.compileMessages(true)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" {
		t.Fatalf("expected agent message to compile as user, got %q", msgs[0].Role)
	}
}

// TestCompileMessages_MetaNotIncludedInLLMContext verifies that the T1
// type|id|name Meta carried on steps never leaks verbatim into the messages
// compiled for the LLM. Since the peer-sender wiring, user-role steps with a
// sender meta (user|id|name / agent|id|name) get a structured annotation
// sentence; assistant-role steps (the agent's own output, carrying its self
// meta) stay untouched.
func TestCompileMessages_MetaNotIncludedInLLMContext(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	userMeta := "user|alice|Alice"
	agentMeta := "agent|actor-1|Coder#0001"
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Meta: userMeta, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
		{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Meta: agentMeta, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi there"}}},
	}

	a := newCompileTestActor(turns, nil, steps)
	msgs := a.compileMessages(true)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(msgs), msgs)
	}
	// The user message carries the structured sender annotation ...
	if msgs[0].Role != "user" || len(msgs[0].Content) != 1 ||
		msgs[0].Content[0].Text != `[This message is from user "Alice" (alice).]`+"\n\nhello" {
		t.Fatalf("user message changed: %+v", msgs[0])
	}
	// ... while the assistant message (agent self meta) is untouched.
	if msgs[1].Role != "assistant" || len(msgs[1].Content) != 1 || msgs[1].Content[0].Text != "hi there" {
		t.Fatalf("assistant message changed: %+v", msgs[1])
	}
	// No raw Meta value may appear anywhere in the compiled content.
	joined := msgs[0].Content[0].Text + msgs[1].Content[0].Text
	if strings.Contains(joined, userMeta) || strings.Contains(joined, agentMeta) {
		t.Fatalf("step Meta leaked into compiled LLM content: %q", joined)
	}
}

func TestCompileMessages_DropsCompactionOnlyAssistantStep(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
		{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi there"}}},
		{ID: "c1", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: "compaction", Text: `{"type":"compaction","status":"completed"}`}}},
	}

	a := newCompileTestActor(turns, nil, steps)

	msgs := a.compileMessages(true)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("expected user then assistant, got %v", msgs)
	}
	if len(msgs[1].Content) != 1 || msgs[1].Content[0].Type != domain.ContentBlockText {
		t.Fatalf("expected plain text assistant message, got %+v", msgs[1].Content)
	}
}

func TestCompileMessages_KeepsAssistantWithTextAndCompaction(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{
		{
			ID:     "a1",
			Role:   "assistant",
			Type:   "text",
			TurnID: "turn-1",
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "summary"},
				{Type: "compaction", Text: `{"type":"compaction"}`},
			},
		},
	}

	a := newCompileTestActor(turns, nil, steps)
	msgs := a.compileMessages(true)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(msgs), msgs)
	}
	if len(msgs[0].Content) != 1 || msgs[0].Content[0].Text != "summary" {
		t.Fatalf("expected only text block preserved, got %+v", msgs[0].Content)
	}
}

func TestCompileMessages_PreservesToolUseAndToolResult(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "read file"}}},
		{
			ID:     "a1",
			Role:   "assistant",
			Type:   "tool_call",
			TurnID: "turn-1",
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "I'll read it."},
				{Type: domain.ContentBlockToolUse, ToolUseID: "tc1", ToolName: "fs.read", Input: `{}`},
				{Type: domain.ContentBlockToolResult, ToolUseID: "tc1", Text: "file contents"},
			},
		},
	}

	a := newCompileTestActor(turns, nil, steps)
	msgs := a.compileMessages(true)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (user + assistant + tool), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected msg[0] role user, got %s", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("expected msg[1] role assistant, got %s", msgs[1].Role)
	}
	if msgs[2].Role != "tool" {
		t.Errorf("expected msg[2] role tool, got %s", msgs[2].Role)
	}
	if len(msgs[1].Content) != 2 {
		t.Fatalf("expected assistant to keep text + tool_use, got %+v", msgs[1].Content)
	}
}

func TestCompileMessages_SkipsCompactedSteps(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
		{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "old response"}}},
	}
	summary := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 1, Text: "summary of turn 1"},
	}

	a := newCompileTestActor(turns, summary, steps)
	msgs := a.compileMessages(true)
	if len(msgs) != 0 {
		t.Fatalf("expected no compacted messages, got %d: %+v", len(msgs), msgs)
	}
}

func TestHandleCompiledPrompt_ShowsMessagesAsSegments(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
		{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi there"}}},
	}

	a := newCompileTestActor(turns, nil, steps)
	artifact := a.buildCompiledPromptStateful(nil)

	if len(artifact.Fragments) != 2 {
		t.Fatalf("expected 2 fragments (user + assistant), got %d: %+v", len(artifact.Fragments), artifact.Fragments)
	}
	if artifact.Fragments[0].Kind != "message" || artifact.Fragments[0].Name != "user #1" || artifact.Fragments[0].SourceRange != "0" {
		t.Fatalf("expected first fragment user #1 message with range 0, got %+v", artifact.Fragments[0])
	}
	if artifact.Fragments[1].Kind != "message" || artifact.Fragments[1].Name != "assistant #2" || artifact.Fragments[1].SourceRange != "1" {
		t.Fatalf("expected second fragment assistant #2 message with range 1, got %+v", artifact.Fragments[1])
	}

	if len(artifact.ContextSegments) != 2 {
		t.Fatalf("expected 2 context segments, got %d", len(artifact.ContextSegments))
	}
}

func TestHandleCompiledPrompt_IncludesSummarySegment(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
		{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "old response"}}},
	}
	summary := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 1, Level: 1, Text: "previous context"},
	}

	a := newCompileTestActor(turns, summary, steps)
	artifact := a.buildCompiledPromptStateful(nil)

	if len(artifact.Fragments) != 1 {
		t.Fatalf("expected 1 fragment (summary), got %d: %+v", len(artifact.Fragments), artifact.Fragments)
	}
	if artifact.Fragments[0].Kind != "summary" || artifact.Fragments[0].Name != "Summary L1 [0-1]" || artifact.Fragments[0].SourceRange != "0-1" {
		t.Fatalf("expected first fragment summary with range 0-1, got %+v", artifact.Fragments[0])
	}
}

func TestHandleCompiledPrompt_Empty(t *testing.T) {
	a := newCompileTestActor(nil, nil, nil)
	artifact := a.buildCompiledPromptStateful(nil)
	if len(artifact.Fragments) != 0 {
		t.Fatalf("expected empty artifact, got %d fragments", len(artifact.Fragments))
	}
}

func TestHandlePromptArtifact_PreservesContextOrder(t *testing.T) {
	a := newCompileTestActor(
		[]domain.Turn{{ID: "turn-1"}},
		nil,
		[]domain.Step{{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "latest conversation"}}}},
	)
	artifact := a.buildPromptArtifactStateful(newFakeCompactionContext())
	kinds := make([]string, 0, len(artifact.Fragments))
	for _, fragment := range artifact.Fragments {
		kinds = append(kinds, fragment.Kind)
	}
	firstTool, firstMessage := -1, -1
	for i, kind := range kinds {
		if kind == "tool" && firstTool == -1 {
			firstTool = i
		}
		if kind == "message" && firstMessage == -1 {
			firstMessage = i
		}
	}
	if firstTool == -1 || firstMessage == -1 {
		t.Fatalf("expected tool and message segments, got kinds=%v", kinds)
	}
	if firstTool >= firstMessage {
		t.Fatalf("context order must place tool guidance/capabilities before conversation, got kinds=%v", kinds)
	}
	if artifact.Fragments[firstMessage].Content == "" || !strings.Contains(artifact.Fragments[firstMessage].Content, "latest conversation") {
		t.Fatalf("latest conversation is not the final message input: %+v", artifact.Fragments[firstMessage])
	}
}

func TestHandlePromptArtifact_ShowsOriginalMessagesAndNoSummary(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
		{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "old response"}}},
	}
	summary := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 1, Level: 1, Text: "previous context"},
	}

	a := newCompileTestActor(turns, summary, steps)
	artifact := a.buildPromptArtifactStateful(newFakeCompactionContext())

	if len(artifact.Fragments) < 2 {
		t.Fatalf("expected at least 2 message fragments, got %d: %+v", len(artifact.Fragments), artifact.Fragments)
	}

	var userMsg, assistantMsg *domain.PromptFragment
	for i := range artifact.Fragments {
		f := &artifact.Fragments[i]
		if f.Kind == "message" {
			if f.Name == "user #1" {
				userMsg = f
			} else if strings.Contains(f.Content, "old response") {
				assistantMsg = f
			}
		}
	}
	if userMsg == nil || userMsg.SourceRange != "0" {
		t.Fatalf("expected user #1 message with range 0, got %+v", userMsg)
	}
	if assistantMsg == nil || !strings.Contains(assistantMsg.Content, "old response") || assistantMsg.SourceRange != "1" {
		t.Fatalf("expected assistant message containing 'old response' with range 1, got %+v", assistantMsg)
	}
	for _, f := range artifact.Fragments {
		if f.Kind == "summary" {
			t.Fatalf("Prompt Context Snapshot must not include summary fragments, got %+v", f)
		}
	}
}

func TestHandlePromptArtifactKeepsConversationAsMessageSegments(t *testing.T) {
	a := newCompileTestActor([]domain.Turn{{ID: "turn-1"}}, nil, []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "latest conversation"}}},
	})
	artifact := a.buildPromptArtifactStateful(newFakeCompactionContext())
	found := false
	for _, fragment := range artifact.Fragments {
		if fragment.Kind == "message" && strings.Contains(fragment.Content, "latest conversation") {
			found = true
		}
		if fragment.Kind == "system" && strings.Contains(fragment.Content, "latest conversation") {
			t.Fatal("conversation was incorrectly compiled into system context")
		}
	}
	if !found {
		t.Fatal("latest conversation was not exposed as a message segment")
	}
}
func TestHandlePromptArtifact_SkipsDiscardedSteps(t *testing.T) {
	// handlePromptArtifact drives the inspector's "Prompt Context Snapshot"
	// via compileMessagesWithSource(false). Discarded steps must not appear
	// even though skipCompacted=false (which intentionally keeps compacted
	// originals for the snapshot). The Discarded guard in appendFromStep is
	// unconditional and runs before the compacted check.
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
		{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Discarded: true, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "secret discarded response"}}},
		{ID: "a2", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "current response"}}},
	}

	a := newCompileTestActor(turns, nil, steps)
	artifact := a.buildPromptArtifactStateful(newFakeCompactionContext())

	var sawDiscarded, sawLive bool
	for _, f := range artifact.Fragments {
		if f.Kind != "message" {
			continue
		}
		if strings.Contains(f.Content, "secret discarded response") {
			sawDiscarded = true
		}
		if strings.Contains(f.Content, "current response") {
			sawLive = true
		}
	}
	if sawDiscarded {
		t.Fatal("Prompt Context Snapshot must not include discarded step content")
	}
	if !sawLive {
		t.Fatal("Prompt Context Snapshot must include non-discarded assistant response")
	}
}

func TestHandlePromptArtifact_DoesNotIncludeUnmountedPlanSubmit(t *testing.T) {
	a := newCompileTestActor(nil, nil, nil)
	a.agentKind = domain.AgentKindCoder
	a.status.Unit = domain.ModelUnit{Model: "claude-sonnet-4"}
	cfg := domain.AgentKindConfig{Kind: domain.AgentKindCoder}
	a.kindConfig.Store(&cfg)

	artifact := a.buildPromptArtifactStateful(newFakeCompactionContext())

	var found bool
	for _, f := range artifact.Fragments {
		if f.Kind == "tool" && f.Name == "plan_submit" {
			found = true
			break
		}
	}
	if found {
		t.Fatal("plan_submit must not be available before plan-module is mounted")
	}
}

func TestHandlePromptArtifact_IncludesForkUsagePrompt(t *testing.T) {
	a := newCompileTestActor(nil, nil, nil)
	a.agentKind = domain.AgentKindCoder
	a.status.Unit = domain.ModelUnit{Model: "claude-sonnet-4"}
	cfg := domain.AgentKindConfig{
		Kind: domain.AgentKindCoder,
	}
	a.kindConfig.Store(&cfg)

	// The fork-explore bundle card is auto-mounted when the agent has the
	// fork_explore tool. Simulate the mounted card contribution.
	snapshot := domain.AgentComponentSnapshot{
		Revision: 1,
		Prompts: []domain.ComponentPromptContribution{
			{ID: "fork-explore-1", CardID: "builtin:bundle:fork-explore", Text: "### Explore\n\n**fork_explore** — Launch a read-only child agent.", Placement: "tool_guidance", Priority: 0},
		},
	}
	a.componentSnapshot.Store(&snapshot)

	artifact := a.buildPromptArtifactStateful(newFakeCompactionContext())

	var found bool
	for _, f := range artifact.Fragments {
		if strings.Contains(f.Content, "### Explore") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(artifact.Fragments))
		for _, f := range artifact.Fragments {
			names = append(names, fmt.Sprintf("%s:%s", f.Kind, f.Name))
		}
		t.Fatalf("expected fork-explore usage prompt in prompt artifact, got fragments: %v", names)
	}
}

func TestCompileMessages_CancelledTurnWithOrphanPlanSubmit(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-cancelled"}}
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-cancelled", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "refactor shell detection"}}},
		{ID: "llm1", Role: "assistant", Type: "text", TurnID: "turn-cancelled", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "I'll inspect first."}}},
		{
			ID:     "plan1",
			Role:   "assistant",
			Type:   "tool_call",
			TurnID: "turn-cancelled",
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockToolUse, ToolUseID: "plan_submit:26", ToolName: "plan_submit", Input: `{}`},
			},
		},
		{ID: "cancel1", Role: "assistant", Type: "text", TurnID: "turn-cancelled", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "用户停止了生成。"}}},
	}

	a := newCompileTestActor(turns, nil, steps)
	msgs := a.compileMessages(true)

	var sawOrphan, sawCancel bool
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == domain.ContentBlockToolUse {
				sawOrphan = true
			}
			if b.Type == domain.ContentBlockText && b.Text == "用户停止了生成。" {
				sawCancel = true
			}
		}
	}
	if !sawOrphan {
		t.Fatalf("orphan tool_use missing from compiled messages: %+v", msgs)
	}
	if !sawCancel {
		t.Fatalf("cancel marker missing from compiled messages: %+v", msgs)
	}

	cleaned := sanitizeMessages(msgs)
	for i, m := range cleaned {
		for _, b := range m.Content {
			if b.Type == domain.ContentBlockToolUse {
				t.Fatalf("orphan tool_use survived sanitize at index %d: %+v", i, b)
			}
		}
	}
	var sanitizedCancel bool
	for _, m := range cleaned {
		for _, b := range m.Content {
			if b.Type == domain.ContentBlockText && b.Text == "用户停止了生成。" {
				sanitizedCancel = true
			}
		}
	}
	if !sanitizedCancel {
		t.Fatalf("cancel marker dropped by sanitize: %+v", cleaned)
	}
}

func findToolByName(tools []domain.ToolSpec, name string) *domain.ToolSpec {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

// recognize_image is mounted automatically for every primary (text-only,
// vision, and undetermined aggregator/auto slots): text-only models need the
// task-specific re-recognition, vision models the on-demand inspection of
// images they cannot load into their own context. An explicit user mount is
// never duplicated.
func TestAppendAutonomousTools_ImageRecognitionMountedForAll(t *testing.T) {
	textOnly := &Actor{primary: domain.ModelSlot{Candidates: []domain.ModelRef{{
		Kind: modelRefKindUnit, Unit: &gen.ModelUnit{Model: "deepseek-chat", Provider: "deepseek"},
	}}}}
	tools := textOnly.appendAutonomousTools(nil, domain.AgentKindConfig{Kind: domain.AgentKindCoder})
	if findToolByName(tools, "recognize_image") == nil {
		t.Fatal("recognize_image must be mounted for a text-only primary unit")
	}

	vision := &Actor{primary: domain.ModelSlot{Candidates: []domain.ModelRef{{
		Kind: modelRefKindUnit, Unit: &gen.ModelUnit{Model: "gpt-4o", Provider: "openai"},
	}}}}
	tools = vision.appendAutonomousTools(nil, domain.AgentKindConfig{Kind: domain.AgentKindCoder})
	if findToolByName(tools, "recognize_image") == nil {
		t.Fatal("recognize_image must be mounted for a vision primary")
	}

	undetermined := &Actor{primary: domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: "aggregator", AggregatorID: "system"}}}}
	tools = undetermined.appendAutonomousTools(nil, domain.AgentKindConfig{Kind: domain.AgentKindCoder})
	if findToolByName(tools, "recognize_image") == nil {
		t.Fatal("recognize_image must be mounted for an undetermined slot")
	}

	// Explicit mount on a text-only primary must not duplicate the spec.
	count := 0
	tools = textOnly.appendAutonomousTools(nil, domain.AgentKindConfig{
		Kind:             domain.AgentKindCoder,
		DefaultBundleIDs: []string{agentkit.ImageRecognitionBundleID},
	})
	for _, tool := range tools {
		if tool.Name == "recognize_image" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("recognize_image spec count = %d, want 1 (no duplicate)", count)
	}
}

func TestAppendAutonomousTools_DebugBundle(t *testing.T) {
	a := &Actor{}
	// Without the debug bundle mounted: the introspection tools must not be exposed.
	disabled := a.appendAutonomousTools(nil, domain.AgentKindConfig{Kind: domain.AgentKindCoder})
	for _, name := range []string{"get_problems", "get_system_logs", "search_services", "list_callables", "invoke_callable", "frontend_debug"} {
		if findToolByName(disabled, name) != nil {
			t.Fatalf("%s must not be present when the debug bundle is not mounted", name)
		}
	}

	// With the debug bundle in DefaultBundleIDs: all introspection tools appear
	// and route to the expected services.
	enabled := a.appendAutonomousTools(nil, domain.AgentKindConfig{
		Kind:             domain.AgentKindCoder,
		DefaultBundleIDs: []string{"builtin:bundle:debug"},
	})
	problems := findToolByName(enabled, "get_problems")
	if problems == nil {
		t.Fatalf("get_problems tool missing when debug bundle is mounted")
	}
	if problems.CallableID != "oracle.list_diagnostics" {
		t.Errorf("expected CallableID oracle.list_diagnostics, got %q", problems.CallableID)
	}
	if problems.ServiceName != "oracle" {
		t.Errorf("expected ServiceName oracle, got %q", problems.ServiceName)
	}

	logs := findToolByName(enabled, "get_system_logs")
	if logs == nil {
		t.Fatalf("get_system_logs tool missing when debug bundle is mounted")
	}
	if logs.CallableID != "workspace.logs_query" {
		t.Errorf("expected CallableID workspace.logs_query, got %q", logs.CallableID)
	}
	if logs.ServiceName != "workspace" {
		t.Errorf("expected ServiceName workspace, got %q", logs.ServiceName)
	}

	services := findToolByName(enabled, "search_services")
	if services == nil {
		t.Fatalf("search_services tool missing when debug bundle is mounted")
	}
	if services.CallableID != "oracle.search_services" {
		t.Errorf("expected CallableID oracle.search_services, got %q", services.CallableID)
	}
	if services.ServiceName != "oracle" {
		t.Errorf("expected ServiceName oracle, got %q", services.ServiceName)
	}

	list := findToolByName(enabled, "list_callables")
	if list == nil {
		t.Fatalf("list_callables tool missing when debug bundle is mounted")
	}
	if list.CallableID != "list_callables" {
		t.Errorf("expected CallableID list_callables, got %q", list.CallableID)
	}
	if list.ServiceName != "agent" {
		t.Errorf("expected ServiceName agent, got %q", list.ServiceName)
	}

	invoke := findToolByName(enabled, "invoke_callable")
	if invoke == nil {
		t.Fatalf("invoke_callable tool missing when debug bundle is mounted")
	}
	if invoke.CallableID != "invoke_callable" {
		t.Errorf("expected CallableID invoke_callable, got %q", invoke.CallableID)
	}
	if invoke.ServiceName != "agent" {
		t.Errorf("expected ServiceName agent, got %q", invoke.ServiceName)
	}

	fe := findToolByName(enabled, "frontend_debug")
	if fe == nil {
		t.Fatalf("frontend_debug tool missing when debug bundle is mounted")
	}
	if fe.CallableID != "frontend_debug" {
		t.Errorf("expected CallableID frontend_debug, got %q", fe.CallableID)
	}

	// Runtime-mounted path: the bundle is in cardRefs (e.g. mounted via
	// component_mount or carried by the prompt:debug → bundle:debug rename
	// migration) but absent from DefaultBundleIDs. The introspection specs must
	// still appear — the mount is the single source of truth.
	runtimeMounted := a.appendAutonomousTools(nil, domain.AgentKindConfig{Kind: domain.AgentKindCoder})
	if findToolByName(runtimeMounted, "get_problems") != nil {
		t.Fatalf("get_problems must not be present before the bundle is mounted")
	}
	a.cardRefs = []gen.CardRef{{ID: "builtin:bundle:debug", Scope: "builtin"}}
	runtimeMounted = a.appendAutonomousTools(nil, domain.AgentKindConfig{Kind: domain.AgentKindCoder})
	if findToolByName(runtimeMounted, "get_problems") == nil {
		t.Fatalf("get_problems must be present when the debug bundle is mounted via cardRefs (runtime/migrated mount)")
	}
}

func TestAppendAutonomousTools_MediaBundles(t *testing.T) {
	a := &Actor{}
	for _, name := range []string{"generate_image", "generate_video"} {
		if findToolByName(a.appendAutonomousTools(nil, domain.AgentKindConfig{Kind: domain.AgentKindCoder}), name) != nil {
			t.Fatalf("%s exposed without media bundles", name)
		}
	}
	defaultTools := a.appendAutonomousTools(nil, domain.AgentKindConfig{Kind: domain.AgentKindCoder, DefaultBundleIDs: []string{"builtin:bundle:image-gen", "builtin:bundle:video-gen"}})
	for _, name := range []string{"generate_image", "generate_video"} {
		if findToolByName(defaultTools, name) == nil {
			t.Fatalf("%s missing with default media bundle", name)
		}
	}
	a.cardRefs = []gen.CardRef{{ID: "builtin:bundle:image-gen", Scope: "builtin"}}
	runtimeTools := a.appendAutonomousTools(nil, domain.AgentKindConfig{Kind: domain.AgentKindCoder})
	if findToolByName(runtimeTools, "generate_image") == nil || findToolByName(runtimeTools, "generate_video") != nil {
		t.Fatal("runtime cardRefs media gating is incorrect")
	}
}

func TestAppendAutonomousTools_OpenGlobalBrowserOnlyForPrivilegedKinds(t *testing.T) {
	a := &Actor{}

	for _, kind := range []string{domain.AgentKindCoder} {
		tools := a.appendAutonomousTools(nil, domain.AgentKindConfig{Kind: kind})
		if findToolByName(tools, "open_global_browser") == nil {
			t.Errorf("open_global_browser missing for kind %q", kind)
		}
	}

	for _, kind := range []string{domain.AgentKindReviewer} {
		tools := a.appendAutonomousTools(nil, domain.AgentKindConfig{Kind: kind})
		if findToolByName(tools, "open_global_browser") != nil {
			t.Errorf("open_global_browser should not be present for kind %q", kind)
		}
	}
}

func TestResolveInstructions_DebugPrompt(t *testing.T) {
	a := &Actor{}
	cfg := domain.AgentKindConfig{
		Kind: domain.AgentKindCoder,
	}
	a.kindConfig.Store(&cfg)

	// Pre-populate snapshot with the debug guidance coming from the bundle body.
	snapshot := domain.AgentComponentSnapshot{
		Revision: 1,
		Prompts: []domain.ComponentPromptContribution{
			{ID: "debug-1", CardID: "builtin:bundle:debug", Text: "You can be a recursive self-improvement agent operating inside this very system.", Placement: "system", Priority: -500},
		},
	}
	a.componentSnapshot.Store(&snapshot)

	ctx := testutil.AnonCtx(testutil.GenActorID())
	inst := a.resolveInstructions(ctx)
	if inst == nil {
		t.Fatal("expected instructions")
	}

	found := false
	for _, b := range inst.Base {
		if strings.Contains(b, "recursive self-improvement agent") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("debug system prompt not found in Base; got %v", inst.Base)
	}
}

// TestResolveInstructions_Ordering verifies that the system prompt is ordered
// as: role first, debug second, other component prompts follow, and skills
// appear in the Resolved section. Environment context is routed to the end of
// Resolved via the environment section (matching the dispatch path).
func TestResolveInstructions_Ordering(t *testing.T) {
	a := &Actor{}
	cfg := domain.AgentKindConfig{
		Kind:          domain.AgentKindCoder,
		RolePromptRef: domain.PromptRef{Kind: "profile", Key: "project.coder"},
		EnvironmentContext: map[string]string{
			"shell_executable": "/bin/bash",
			"os":               "linux",
		},
	}
	a.kindConfig.Store(&cfg)

	// Pre-populate a snapshot with prompts in deliberately wrong order to
	// verify resolveInstructions sorts them correctly.
	snapshot := domain.AgentComponentSnapshot{
		Revision: 1,
		Prompts: []domain.ComponentPromptContribution{
			{ID: "wiki-1", CardID: "builtin:bundle:project-wiki", Text: "WikiWord Card Linking", Placement: "system", Priority: 0},
			{ID: "debug-1", CardID: "builtin:bundle:debug", Text: "recursive self-improvement agent", Placement: "system", Priority: -500},
			{ID: "role-1", CardID: "prompt:profile:project.coder", Title: "project.coder", Text: "You are a coder.", Placement: "system", Priority: -1000},
		},
		Tools: []domain.ComponentToolContribution{},
	}
	a.componentSnapshot.Store(&snapshot)

	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" || name == "project" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				switch callID {
				case "project.wiki_list_cards":
					return domain.WikiListCardsResp{Cards: []domain.MonoCardListItem{{ID: "skill:read-logs"}}}, nil
				case "project.wiki_get_card":
					return domain.WikiGetCardResp{ID: "skill:read-logs", Raw: "---\ntitle: read-logs\n---\n\nRead logs"}, nil
				case "workspace.wiki_get_card":
					return domain.WikiGetCardResp{ID: "prompt:profile:project.coder", Raw: "---\ntitle: Coder\ntags: [component, prompt, profile]\n---\n\nYou are a coder."}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}

	inst := a.resolveInstructions(ctx)
	if inst == nil {
		t.Fatal("expected instructions")
	}

	// Base should be ordered: role, debug, wiki.
	if len(inst.Base) < 3 {
		t.Fatalf("expected at least 3 base entries, got %d: %v", len(inst.Base), inst.Base)
	}
	if !strings.Contains(inst.Base[0], "You are a coder.") {
		t.Fatalf("expected role first in Base, got %q", inst.Base[0])
	}
	if !strings.Contains(inst.Base[1], "recursive self-improvement agent") {
		t.Fatalf("expected debug second in Base, got %q", inst.Base[1])
	}
	if !strings.Contains(inst.Base[2], "WikiWord Card Linking") {
		t.Fatalf("expected wiki third in Base, got %q", inst.Base[2])
	}

	// Resolved should contain environment followed by skills (skills is
	// routed last via the skills section, so it lands immediately before the
	// hot context blocks appended after the compiled instructions).
	if len(inst.Resolved) != 2 {
		t.Fatalf("expected 2 resolved entries (environment, skills), got %d: %v", len(inst.Resolved), inst.Resolved)
	}
	if !strings.Contains(inst.Resolved[0], "Environment:") {
		t.Fatalf("expected environment first in Resolved, got %q", inst.Resolved[0])
	}

	// Skills should be the last Resolved entry (skills section, ordered
	// after every other instruction block, right before hot context).
	if !strings.Contains(inst.Resolved[len(inst.Resolved)-1], "Skills:") {
		t.Fatalf("expected skills last in Resolved (skills section), got %v", inst.Resolved)
	}
	for _, b := range inst.Base {
		if strings.Contains(b, "Environment:") {
			t.Fatalf("environment must not be in Base (moved to Resolved), got %v", inst.Base)
		}
	}
}

// TestResolveInstructionsCacheNotAliased verifies that resolveInstructions
// returns an independent copy on the cache-miss path. Before the fix, the
// cache-miss path returned the same pointer stored in the cache, so callers
// that mutate the result (appendMemoryBase/appendMemoryExperience) corrupted
// the cache — causing the second resolve (Compiled Prompt built after the
// Snapshot) to see ontology/experience blocks twice.
func TestResolveInstructionsCacheNotAliased(t *testing.T) {
	a := &Actor{}
	cfg := domain.AgentKindConfig{Kind: domain.AgentKindCoder}
	a.kindConfig.Store(&cfg)
	a.componentSnapshot.Store(&domain.AgentComponentSnapshot{
		Revision: 1,
		Prompts: []domain.ComponentPromptContribution{
			{ID: "base-1", Text: "base instructions", Placement: "system", Priority: 0},
		},
	})
	ctx := testutil.AnonCtx(testutil.GenActorID())

	// First resolve: cache miss.
	inst1 := a.resolveInstructions(ctx)
	if inst1 == nil {
		t.Fatal("expected instructions on cache miss")
	}
	// Simulate what the Snapshot builder does: prepend ontology, append experience.
	inst1.Base = append([]string{"## Active Memory (Ontology)\n\nfake"}, inst1.Base...)
	inst1.Resolved = append(inst1.Resolved, "## Active Memory (Experience Heads)\n\nfake")

	// Second resolve: cache hit. Must return a pristine copy.
	inst2 := a.resolveInstructions(ctx)
	for _, b := range inst2.Base {
		if strings.Contains(b, "Active Memory") {
			t.Fatalf("cache polluted: inst2.Base leaked mutation from inst1: %v", inst2.Base)
		}
	}
	for _, r := range inst2.Resolved {
		if strings.Contains(r, "Active Memory") {
			t.Fatalf("cache polluted: inst2.Resolved leaked mutation from inst1: %v", inst2.Resolved)
		}
	}
}

// TestCompiledPromptMemoryNotDuplicated exercises the full refreshPromptCaches
// flow (Snapshot built first, Compiled Prompt second) with a memory graph
// containing ontology + experience nodes. Before the fix, the Snapshot's
// appendMemoryBase/appendMemoryExperience polluted the resolvedInstructions
// cache, so the Compiled Prompt saw each memory block twice.
func TestCompiledPromptMemoryNotDuplicated(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"builtin:mode:memory": {
			Ref:   domain.ComponentRef{CardID: "builtin:mode:memory", Kind: "mode", Source: "builtin"},
			Title: "Memory Mode",
			Prompts: []domain.ComponentPromptContribution{
				{ID: "mem-1", CardID: "builtin:mode:memory", Text: "You have a memory graph.", Placement: "system"},
			},
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{
		agentKind: "coder",
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "builtin:mode:memory", Enabled: true, Scope: "user", Kind: "mode", Order: 10},
		},
		Graph: memory.New(memory.Config{}),
	}
	a.kindConfig.Store(&domain.AgentKindConfig{Kind: "coder"})
	_ = a.resolveComponentSnapshot(ctx) // warm cache

	a.Graph.AddNode("o1", memory.NodeTypeOntology, "core concept", "", 5)
	a.Graph.AddNode("e1", memory.NodeTypeExperience, "learned pattern", "", 3)

	// refreshPromptCaches builds Snapshot then Compiled Prompt sequentially.
	a.refreshPromptCaches(ctx)

	snapshot := a.cachedPromptArtifact.Load()
	if snapshot == nil {
		t.Fatal("cached prompt artifact is nil")
	}
	compiled := a.cachedCompiledPrompt.Load()
	if compiled == nil {
		t.Fatal("cached compiled prompt is nil")
	}

	countKind := func(art *domain.PromptArtifact, kind string) int {
		n := 0
		for _, f := range art.Fragments {
			if f.Kind == kind {
				n++
			}
		}
		return n
	}

	for _, art := range []*domain.PromptArtifact{snapshot, compiled} {
		if got := countKind(art, "memory-ontology"); got != 1 {
			t.Fatalf("expected exactly 1 ontology fragment, got %d", got)
		}
		if got := countKind(art, "memory-experience"); got != 1 {
			t.Fatalf("expected exactly 1 experience fragment, got %d", got)
		}
	}
}

func TestResolveToolsPicksUpKindConfigChangeAfterTurnReset(t *testing.T) {
	a := &Actor{}
	callables := map[string]domain.CallableInterface{
		"project.read":           {Name: "project.read", Description: "Read file"},
		"workspace.list_project": {Name: "workspace.list_project", Description: "List projects"},
	}
	// Pre-populate an empty component snapshot so resolveComponentSnapshot
	// does not call out to workspace (avoiding planner dependency chains).
	a.componentSnapshot.Store(&domain.AgentComponentSnapshot{})

	// Planner stub: returns the latest kind config from a mutable variable.
	cfgBytes := &[]byte{}
	initCfg, _ := json.Marshal(domain.AgentKindConfig{Kind: "coder", DefaultBundleIDs: []string{"builtin:bundle:file-tools"}})
	*cfgBytes = initCfg

	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), name == "workspace"
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
			if callID == "workspace.get_agent_kind_config" {
				return *cfgBytes, nil
			}
			return nil, fmt.Errorf("unexpected call %s", callID)
		}}
	}

	// Turn 1: file-tools config → project-read only.
	cfg1 := a.fetchAgentKindConfig(ctx)
	tools1 := a.resolveTools(ctx, cfg1, callables)
	if len(tools1) != 1 || tools1[0].Name != "project-read" {
		t.Fatalf("turn 1: expected [project-read], got %+v", toolNames(tools1))
	}

	// Config changes to workspace-tools (simulates a user editing the kind
	// config between turns). The next turn reset invalidates the cache.
	newCfg, _ := json.Marshal(domain.AgentKindConfig{Kind: "coder", DefaultBundleIDs: []string{"builtin:bundle:workspace-tools"}})
	*cfgBytes = newCfg
	a.resetTurnSnapshot(domain.ModelUnit{})

	cfg2 := a.fetchAgentKindConfig(ctx)
	tools2 := a.resolveTools(ctx, cfg2, callables)
	names := toolNames(tools2)
	if !slices.Contains(names, "workspace-list_project") {
		t.Fatalf("turn 2: expected workspace-list_project in tools, got %+v", names)
	}
	if slices.Contains(names, "project-read") {
		t.Fatalf("turn 2: project-read should not appear after switching to workspace-tools, got %+v", names)
	}
}

func toolNames(tools []domain.ToolSpec) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

func TestResetTurnSnapshotInvalidatesKindConfigCache(t *testing.T) {
	a := &Actor{}

	cached := domain.AgentKindConfig{Kind: domain.AgentKindCoder}
	a.kindConfig.Store(&cached)

	// Simulate the start of a new turn.
	a.resetTurnSnapshot(domain.ModelUnit{})

	if a.kindConfig.Load() != nil {
		t.Fatal("kindConfig cache should be nil after resetTurnSnapshot")
	}
}

func TestResolveTools_DebugBundleExposesListAgents(t *testing.T) {
	a := &Actor{}
	callables := map[string]domain.CallableInterface{
		"workspace.list_agents": {
			Name:        "workspace.list_agents",
			Description: "List agents",
			Params:      []domain.CallableParam{{Name: "Query", Type: "string", Required: false}},
		},
		"project.read": {
			Name:        "project.read",
			Description: "Read file",
			Params:      []domain.CallableParam{{Name: "Path", Type: "string", Required: true}},
		},
	}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	cfg := domain.AgentKindConfig{Kind: "coder"}

	// Pre-populate component snapshot with file_read tool from a mounted bundle.
	a.componentSnapshot.Store(&domain.AgentComponentSnapshot{
		Tools: []domain.ComponentToolContribution{
			{ID: "file_read", CardID: "builtin:bundle:file-tools", CallableID: "project.read"},
		},
	})

	// Without the debug bundle: only component-provided tools are available.
	tools := a.resolveTools(ctx, cfg, callables)
	if len(tools) != 1 || tools[0].Name != "project-read" {
		t.Fatalf("expected project-read only, got %+v", tools)
	}

	// Mount the debug bundle: its data.tools declares workspace.list_agents, so
	// it becomes available alongside file-tools.
	a.componentSnapshot.Store(&domain.AgentComponentSnapshot{
		Tools: []domain.ComponentToolContribution{
			{ID: "file_read", CardID: "builtin:bundle:file-tools", CallableID: "project.read"},
			{ID: "list_agents", CardID: "builtin:bundle:debug", CallableID: "workspace.list_agents"},
		},
	})
	tools = a.resolveTools(ctx, cfg, callables)
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	if len(tools) != 2 || !slices.Contains(names, "project-read") || !slices.Contains(names, "workspace-list_agents") {
		t.Fatalf("expected project-read and workspace-list_agents, got %+v", tools)
	}
}

func TestDedupToolsByName(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	spec := func(name, callableID string) domain.ToolSpec {
		return domain.ToolSpec{Name: name, CallableID: callableID}
	}

	tools := []domain.ToolSpec{
		spec("file_read", "project.read"),
		spec("file_read", "project.read"), // exact duplicate
		spec("list_agents", "workspace.list_agents"),
		spec("file_read", "workspace.file_read"), // distinct CallableID, same Name → collision
		spec("fork_explore", "workspace.agent_spawn_by_type"),
	}

	out := a.dedupToolsByName(ctx, tools)
	if len(out) != 3 {
		t.Fatalf("expected 3 tools after dedup, got %d: %+v", len(out), out)
	}
	// First occurrence wins for the colliding name.
	want := []string{"file_read", "list_agents", "fork_explore"}
	for i, w := range want {
		if out[i].Name != w {
			t.Errorf("out[%d]: got name %q, want %q", i, out[i].Name, w)
		}
	}
	if out[0].CallableID != "project.read" {
		t.Errorf("collision kept wrong callable: got %q, want project.read", out[0].CallableID)
	}
}

func TestFetchProjectGuideFile(t *testing.T) {
	newCtx := func(files map[string]string) *testutil.FakeCtx {
		ctx := testutil.AnonCtx(testutil.GenActorID())
		ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
		ctx.PlannerFn = func() actor.Planner {
			return fakePlannerForInvoke{
				callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
					if callID != "project.read" {
						return nil, fmt.Errorf("unexpected call %s", callID)
					}
					var req domain.FileSystemReadReq
					switch v := payload.(type) {
					case []byte:
						if err := json.Unmarshal(v, &req); err != nil {
							return nil, err
						}
					default:
						return nil, fmt.Errorf("unexpected payload %T", payload)
					}
					if content, ok := files[req.Path]; ok {
						return domain.FileSystemReadResp{Content: content}, nil
					}
					return nil, fmt.Errorf("file not found: %s", req.Path)
				},
			}
		}
		return ctx
	}

	t.Run("agents md overrides claude md", func(t *testing.T) {
		ctx := newCtx(map[string]string{"AGENTS.md": "guide from agents", "CLAUDE.md": "guide from claude"})
		name, content := fetchProjectGuideFile(ctx)
		if name != "AGENTS.md" || content != "guide from agents" {
			t.Fatalf("fetchProjectGuideFile = (%q, %q), want (AGENTS.md, guide from agents)", name, content)
		}
	})

	t.Run("falls back to claude md", func(t *testing.T) {
		ctx := newCtx(map[string]string{"CLAUDE.md": "guide from claude"})
		name, content := fetchProjectGuideFile(ctx)
		if name != "CLAUDE.md" || content != "guide from claude" {
			t.Fatalf("fetchProjectGuideFile = (%q, %q), want (CLAUDE.md, guide from claude)", name, content)
		}
	})

	t.Run("missing files return empty", func(t *testing.T) {
		ctx := newCtx(map[string]string{})
		name, content := fetchProjectGuideFile(ctx)
		if name != "" || content != "" {
			t.Fatalf("fetchProjectGuideFile = (%q, %q), want empty", name, content)
		}
	})

	t.Run("truncates oversized content on rune boundary", func(t *testing.T) {
		long := strings.Repeat("界", maxGuideFileChars/3+10)
		ctx := newCtx(map[string]string{"AGENTS.md": long})
		name, content := fetchProjectGuideFile(ctx)
		if name != "AGENTS.md" {
			t.Fatalf("name = %q, want AGENTS.md", name)
		}
		if !strings.HasSuffix(content, "...(truncated)") {
			t.Fatalf("truncated content missing marker; tail = %q", content[len(content)-40:])
		}
		if !utf8.ValidString(content) {
			t.Fatalf("truncated content is not valid UTF-8")
		}
	})
}

func TestResolveTools_WorktreeBundleCallablesHaveFallbackMetadata(t *testing.T) {
	a := &Actor{}
	seedTestBuiltinMounts(a, false)
	a.ComponentMounts = append(a.ComponentMounts, domain.AgentComponentMount{CardID: "builtin:bundle:worktree", Enabled: true, Scope: "builtin", Kind: "bundle"})
	a.componentSnapshot.Store(&domain.AgentComponentSnapshot{Tools: []domain.ComponentToolContribution{
		{CardID: "builtin:bundle:worktree", CallableID: "project.worktree_enter"},
		{CardID: "builtin:bundle:worktree", CallableID: "project.worktree_exit"},
	}})
	tools := a.resolveTools(testutil.AnonCtx(testutil.GenActorID()), domain.AgentKindConfig{Kind: "coder"}, nil)
	seen := map[string]bool{}
	for _, tool := range tools {
		seen[tool.CallableID] = true
	}
	if !seen["project.worktree_enter"] || !seen["project.worktree_exit"] {
		t.Fatalf("worktree tools were not injected: %+v", tools)
	}
}

func TestResolveTools_CoderGetsProjectWikiTools(t *testing.T) {
	a := &Actor{}
	seedTestBuiltinMounts(a, false)
	callables := map[string]domain.CallableInterface{
		"project.wiki_list_all_cards":     {Name: "project.wiki_list_all_cards", Description: "List all cards"},
		"project.wiki_list_cards":         {Name: "project.wiki_list_cards", Description: "List cards tree"},
		"project.wiki_get_card_hierarchy": {Name: "project.wiki_get_card_hierarchy", Description: "Get card hierarchy"},
		"project.wiki_get_card":           {Name: "project.wiki_get_card", Description: "Get card"},
		"project.wiki_create_card":        {Name: "project.wiki_create_card", Description: "Create card"},
		"project.wiki_edit_card":          {Name: "project.wiki_edit_card", Description: "Edit card"},
		"project.wiki_delete_card":         {Name: "project.wiki_delete_card", Description: "Delete card"},
	}
	toolContributions := make([]domain.ComponentToolContribution, 0, len(callables))
	for id := range callables {
		toolContributions = append(toolContributions, domain.ComponentToolContribution{CardID: "builtin:bundle:project-wiki", CallableID: id})
	}
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:bundle:project-wiki": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:project-wiki", Kind: "bundle", Source: "builtin"},
			Tools: toolContributions,
		},
	})
	cfg := domain.AgentKindConfig{Kind: "coder"}

	tools := a.resolveTools(ctx, cfg, callables)
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	expected := []string{"project-wiki_list_cards", "project-wiki_get_card", "project-wiki_create_card", "project-wiki_edit_card", "project-wiki_delete_card"}
	for _, name := range expected {
		if !slices.Contains(names, name) {
			t.Fatalf("expected coder tools to include %s, got %+v", name, names)
		}
	}
}

func TestEnsureLocalInteractionCallables_FillsMissing(t *testing.T) {
	callables := map[string]domain.CallableInterface{
		"goal_submit": {Name: "goal_submit"},
	}
	got := ensureLocalInteractionCallables(callables)
	if _, ok := got["plan_submit"]; !ok {
		t.Fatal("expected plan.submit to be added when missing")
	}
	if got["goal_submit"].Description != "" {
		t.Fatal("expected existing goal.submit entry to be preserved")
	}
	assess, ok := got["turn_assess"]
	if !ok {
		t.Fatal("expected turn.assess to be added when missing")
	}
	if len(assess.Params) != 4 || assess.Params[0].Name != "Decision" {
		t.Fatalf("unexpected turn.assess params: %#v", assess.Params)
	}
	if !hasCallableParam(assess.Params, "Outputs") {
		t.Fatal("expected turn.assess to carry an Outputs param for typed JSON review outputs")
	}
}

func TestEnsureLocalInteractionCallables_RepairsStalePlanSubmit(t *testing.T) {
	got := ensureLocalInteractionCallables(map[string]domain.CallableInterface{
		"plan_submit": {Name: "plan_submit", Description: "stale"},
	})
	planSubmit := got["plan_submit"]
	if !hasCallableParam(planSubmit.Params, "Title") {
		t.Fatal("expected stale plan.submit metadata to gain Title")
	}
	if !planSubmit.Params[0].Required || planSubmit.Params[0].Name != "Title" {
		t.Fatalf("expected Title to be the required first param, got %+v", planSubmit.Params)
	}
	if !hasCallableParam(planSubmit.Params, "Tasks") || !hasCallableParam(planSubmit.Params, "Policy") {
		t.Fatalf("expected complete plan.submit params, got %+v", planSubmit.Params)
	}
	if !hasCallableParam(planSubmit.Params, "Body") {
		t.Fatal("expected stale plan.submit metadata to gain Body")
	}
	bodyParam := planSubmit.Params[1]
	if bodyParam.Name != "Body" || !bodyParam.Required {
		t.Fatalf("expected Body to be the required second param, got %+v", planSubmit.Params)
	}
}
func TestEnsureLocalInteractionCallables_CreatesMap(t *testing.T) {
	got := ensureLocalInteractionCallables(nil)
	if _, ok := got["goal_submit"]; !ok {
		t.Fatal("expected goal.submit to be created for nil map")
	}
	if _, ok := got["plan_submit"]; !ok {
		t.Fatal("expected plan.submit to be created for nil map")
	}
}

func TestEnsureLocalInteractionCallables_RemovesSkillUse(t *testing.T) {
	got := ensureLocalInteractionCallables(map[string]domain.CallableInterface{
		"skill_use": {Name: "skill_use", Description: "from topology"},
	})
	if _, ok := got["skill_use"]; ok {
		t.Fatal("skill_use must be removed from callables — appendAutonomousTools provides it via SkillUseTool()")
	}
}

func TestEnsureLocalInteractionCallables_AddsMemoryCallables(t *testing.T) {
	got := ensureLocalInteractionCallables(nil)
	save, ok := got["memory_save"]
	if !ok {
		t.Fatal("expected memory_save fallback to be added")
	}
	if save.Description == "" {
		t.Fatal("memory_save must have a description")
	}
	if !hasCallableParam(save.Params, "Content") {
		t.Fatalf("memory_save must have Content param, got %+v", save.Params)
	}
	recall, ok := got["memory_recall"]
	if !ok {
		t.Fatal("expected memory_recall fallback to be added")
	}
	if recall.Description == "" {
		t.Fatal("memory_recall must have a description")
	}
	if !hasCallableParam(recall.Params, "Query") {
		t.Fatalf("memory_recall must have Query param, got %+v", recall.Params)
	}
}

func TestEnsureLocalInteractionCallables_PreservesExistingMemoryCallables(t *testing.T) {
	existing := domain.CallableInterface{Name: "memory_save", Description: "from manifest"}
	got := ensureLocalInteractionCallables(map[string]domain.CallableInterface{
		"memory_save": existing,
	})
	if got["memory_save"].Description != "from manifest" {
		t.Fatal("existing memory_save entry must be preserved, not overwritten")
	}
	// memory_recall should still be added as fallback.
	if _, ok := got["memory_recall"]; !ok {
		t.Fatal("memory_recall fallback should be added even when memory_save exists")
	}
}

func TestResolveHotContext_ExcludesActivePlan(t *testing.T) {
	a := &Actor{}
	a.plan.Task = "implement feature"
	a.plan.Status = "approved"
	a.plan.Plan = "# Plan\n\nDo things"
	a.plan.RequestID = "req-1"
	ctx := testutil.AnonCtx(testutil.GenActorID())

	// The active plan must not be injected into hot context.
	for _, b := range a.resolveHotContext(ctx) {
		if strings.Contains(b.Text, "## Active Plan") || strings.Contains(b.Text, "implement feature") {
			t.Fatalf("hot context must not include plan block, got %v", b.Text)
		}
	}
	for _, b := range a.resolveFullHotContext(ctx) {
		if strings.Contains(b.Text, "## Active Plan") || strings.Contains(b.Text, "implement feature") {
			t.Fatalf("full hot context must not include plan block, got %v", b.Text)
		}
	}
}

func TestResolveHotContext_IncludesTaskBoard(t *testing.T) {
	a := &Actor{
		RawSession: domain.RawSession{
			Tasks: []gen.TurnTask{
				{ID: "t1", Subject: "Add retry logic", Status: "in_progress", ActiveForm: "retrying"},
				{ID: "t2", Subject: "Write tests", Status: "pending"},
				{ID: "t3", Subject: "Already done", Status: "completed"},
			},
		},
	}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	blocks := a.resolveHotContext(ctx)

	var board string
	for _, b := range blocks {
		if strings.Contains(b.Text, "## Current Task Board") {
			board = b.Text
		}
	}
	if board == "" {
		t.Fatalf("expected a task board hot-context block, got %d blocks", len(blocks))
	}
	if !strings.Contains(board, "Add retry logic") || !strings.Contains(board, "Write tests") {
		t.Fatalf("task board must list active tasks, got %q", board)
	}
	if strings.Contains(board, "Already done") {
		t.Fatalf("task board must exclude completed tasks, got %q", board)
	}
	if !strings.Contains(board, "(retrying)") {
		t.Fatalf("task board must include active form, got %q", board)
	}

	// No active tasks → no task board block (and no goal → empty hot context).
	a2 := &Actor{
		RawSession: domain.RawSession{
			Tasks: []gen.TurnTask{{ID: "x", Subject: "finished", Status: "completed"}},
		},
	}
	if blocks := a2.resolveHotContext(ctx); len(blocks) != 0 {
		t.Fatalf("expected no hot context blocks when all tasks completed, got %v", blocks)
	}
}

// conversableTestCtx builds an agent-test context whose fake planner resolves
// workspace.list_agents to the given live agent metadata (or listErr), used by
// the buildConversableBlock hot-context tests.
func conversableTestCtx(t *testing.T, agents []domain.AgentRef, listErr error) actor.Context {
	return conversableTestCtxRecord(t, agents, listErr, nil)
}

// conversableTestCtxRecord additionally records each workspace.list_agents
// request payload so tests can assert the lookup scope.
func conversableTestCtxRecord(t *testing.T, agents []domain.AgentRef, listErr error, recorded *[]domain.WorkspaceListAgentsReq) actor.Context {
	t.Helper()
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				switch callID {
				case "workspace.list_agents":
					if recorded != nil {
						if r, ok := payload.(domain.WorkspaceListAgentsReq); ok {
							*recorded = append(*recorded, r)
						}
					}
					if listErr != nil {
						return nil, listErr
					}
					return domain.AgentRefListResp{Items: agents}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}
	return ctx
}

// findConversableBlock returns the hot-context block carrying the conversable
// agents section, or nil when absent.
func findConversableBlock(blocks []domain.ContentBlock) *domain.ContentBlock {
	for i := range blocks {
		if strings.Contains(blocks[i].Text, "## Conversable Agents") {
			return &blocks[i]
		}
	}
	return nil
}

func TestResolveHotContext_ConversableBlock_TwoMounts(t *testing.T) {
	agents := []domain.AgentRef{
		{ID: "Coder#0001", ActorID: "actor-1", ProjectID: "p1", ProjectName: "sporemind", DisplayName: "Alice", AgentKind: "coder", LoadState: "loaded", Status: "idle"},
		{ID: "Worker#0002", ActorID: "actor-2", ProjectID: "p1", DisplayName: "Bob", AgentKind: "worker", LoadState: "loaded", Status: "running"},
	}
	ctx := conversableTestCtx(t, agents, nil)
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "agent-chat:Coder#0001", Enabled: true, Scope: "user"},
			{CardID: "agent-chat:Worker#0002", Enabled: true, Scope: "user"},
			// Duplicate mount of an already-listed target: must be deduped.
			{CardID: "agent-chat:Coder#0001", Enabled: true, Scope: "user"},
		},
	}
	block := findConversableBlock(a.resolveHotContext(ctx))
	if block == nil {
		t.Fatal("expected a conversable hot-context block for two agent-chat mounts")
	}
	// ONE base section regardless of mount count.
	if got := strings.Count(block.Text, "## Conversable Agents"); got != 1 {
		t.Fatalf("expected exactly one heading, got %d:\n%s", got, block.Text)
	}
	// Two list rows (third mount is a dedupe), live metadata from list_agents.
	if got := strings.Count(block.Text, "(id: "); got != 2 {
		t.Fatalf("expected exactly two list rows, got %d:\n%s", got, block.Text)
	}
	for _, want := range []string{
		// Alice carries the resolved mount name; Bob falls back to ProjectID.
		"- Alice (id: Coder#0001, kind: coder, project: sporemind, status: idle)",
		"- Bob (id: Worker#0002, kind: worker, project: p1, status: running)",
		"workspace.agent_send_message",
		"workspace.agent_read_message",
		"workspace.agent_pause / workspace.agent_resume",
	} {
		if !strings.Contains(block.Text, want) {
			t.Fatalf("missing %q in block:\n%s", want, block.Text)
		}
	}
}

func TestResolveHotContext_ConversableBlock_CrossProjectTarget(t *testing.T) {
	var reqs []domain.WorkspaceListAgentsReq
	agents := []domain.AgentRef{
		{ID: "Coder#0001", ActorID: "actor-1", ProjectID: "project-other", DisplayName: "Alice", AgentKind: "coder", LoadState: "loaded", Status: "idle"},
	}
	ctx := conversableTestCtxRecord(t, agents, nil, &reqs)
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "agent-chat:Coder#0001", Enabled: true, Scope: "user"},
		},
	}
	block := findConversableBlock(a.resolveHotContext(ctx))
	if block == nil {
		t.Fatal("expected a conversable block for a cross-project agent-chat mount")
	}
	want := "- Alice (id: Coder#0001, kind: coder, project: project-other, status: idle)"
	if !strings.Contains(block.Text, want) {
		t.Fatalf("missing live metadata row %q in block:\n%s", want, block.Text)
	}
	if len(reqs) == 0 || reqs[0].ProjectID != "" {
		t.Fatalf("list_agents lookup must be workspace-wide (empty ProjectID), got %+v", reqs)
	}
}

func TestResolveHotContext_ConversableBlock_NoMounts(t *testing.T) {
	ctx := conversableTestCtx(t, nil, nil)
	a := &Actor{}
	if block := findConversableBlock(a.resolveHotContext(ctx)); block != nil {
		t.Fatalf("expected no conversable block without agent-chat mounts, got:\n%s", block.Text)
	}
	if b := a.buildConversableBlock(ctx); b != nil {
		t.Fatalf("buildConversableBlock must return nil without mounts, got:\n%s", b.Text)
	}
}

func TestResolveHotContext_ConversableBlock_ListFailure(t *testing.T) {
	ctx := conversableTestCtx(t, nil, errors.New("workspace unavailable"))
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "agent-chat:Coder#0001", Enabled: true, Scope: "user"},
		},
	}
	block := findConversableBlock(a.resolveHotContext(ctx))
	if block == nil {
		t.Fatal("expected conversable block even when list_agents fails")
	}
	if !strings.Contains(block.Text, "## Conversable Agents") {
		t.Fatalf("base prompt must be present on failure:\n%s", block.Text)
	}
	if !strings.Contains(block.Text, "workspace.agent_send_message") {
		t.Fatalf("guidance must be present on failure:\n%s", block.Text)
	}
	if !strings.Contains(block.Text, "- Coder#0001 (id: Coder#0001, status unavailable)") {
		t.Fatalf("row must carry the (status unavailable) degrade marker:\n%s", block.Text)
	}
}

func TestResolveHotContext_ConversableBlock_UnresolvableTarget(t *testing.T) {
	agents := []domain.AgentRef{
		{ID: "Coder#0001", ActorID: "actor-1", ProjectID: "p1", DisplayName: "Alice", AgentKind: "coder", LoadState: "loaded", Status: "idle"},
		{ID: "Worker#0002", ActorID: "actor-2", DisplayName: "Bob", AgentKind: "worker", LoadState: "unloaded"},
	}
	ctx := conversableTestCtx(t, agents, nil)
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "agent-chat:Coder#0001", Enabled: true, Scope: "user"},
			{CardID: "agent-chat:Missing#9", Enabled: true, Scope: "user"},
			{CardID: "agent-chat:Worker#0002", Enabled: true, Scope: "user"},
		},
	}
	block := findConversableBlock(a.resolveHotContext(ctx))
	if block == nil {
		t.Fatal("expected a conversable hot-context block")
	}
	if !strings.Contains(block.Text, "- Alice (id: Coder#0001, kind: coder, project: p1, status: idle)") {
		t.Fatalf("resolvable target row missing:\n%s", block.Text)
	}
	if strings.Contains(block.Text, "Missing#9") {
		t.Fatalf("missing (offline) target must be omitted from the block:\n%s", block.Text)
	}
	if strings.Contains(block.Text, "Worker#0002") {
		t.Fatalf("unloaded (offline) target must be omitted from the block:\n%s", block.Text)
	}
	if got := strings.Count(block.Text, "(id: "); got != 1 {
		t.Fatalf("expected exactly one online row, got %d:\n%s", got, block.Text)
	}
}

func TestResolveHotContext_ConversableBlock_AllTargetsOffline(t *testing.T) {
	agents := []domain.AgentRef{
		{ID: "Worker#0002", ActorID: "actor-2", DisplayName: "Bob", AgentKind: "worker", LoadState: "unloaded"},
	}
	ctx := conversableTestCtx(t, agents, nil)
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "agent-chat:Worker#0002", Enabled: true, Scope: "user"},
		},
	}
	if block := findConversableBlock(a.resolveHotContext(ctx)); block != nil {
		t.Fatalf("expected no conversable block when the only mount is offline, got:\n%s", block.Text)
	}
	if b := a.buildConversableBlock(ctx); b != nil {
		t.Fatalf("buildConversableBlock must return nil when no target is online, got:\n%s", b.Text)
	}
}

// findBrowserBlock returns the hot-context block carrying the mounted
// browser windows section, or nil when absent.
func findBrowserBlock(blocks []domain.ContentBlock) *domain.ContentBlock {
	for i := range blocks {
		if strings.Contains(blocks[i].Text, "## Mounted Browser Windows") {
			return &blocks[i]
		}
	}
	return nil
}

func TestResolveHotContext_BrowserBlock_Mounted(t *testing.T) {
	ctx := conversableTestCtx(t, nil, nil)
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "browser-chat:win-abc123", Enabled: true, Scope: "user"},
		},
	}
	block := findBrowserBlock(a.resolveHotContext(ctx))
	if block == nil {
		t.Fatal("expected a mounted-browser-windows hot-context block for a browser-chat mount")
	}
	if got := strings.Count(block.Text, "## Mounted Browser Windows"); got != 1 {
		t.Fatalf("expected exactly one browser section heading, got %d:\n%s", got, block.Text)
	}
	for _, want := range []string{
		`- win-abc123 (Config.InstanceID = "win-abc123")`,
		"crawl.start begins a task on the window",
		"crawl.handoff hands a login-walled task back to the user",
	} {
		if !strings.Contains(block.Text, want) {
			t.Fatalf("missing %q in block:\n%s", want, block.Text)
		}
	}
	// The browser section must not leak into a conversable-agents section
	// when no agent-chat mount exists.
	if strings.Contains(block.Text, "## Conversable Agents") {
		t.Fatalf("browser-only block must not carry the conversable section:\n%s", block.Text)
	}
}

func TestResolveHotContext_ConversableBlock_MixedAgentAndBrowser(t *testing.T) {
	agents := []domain.AgentRef{
		{ID: "Coder#0001", ActorID: "actor-1", ProjectID: "p1", DisplayName: "Alice", AgentKind: "coder", LoadState: "loaded", Status: "idle"},
	}
	ctx := conversableTestCtx(t, agents, nil)
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "agent-chat:Coder#0001", Enabled: true, Scope: "user"},
			{CardID: "browser-chat:win-abc123", Enabled: true, Scope: "user"},
			{CardID: "browser-chat:win-def456", Enabled: true, Scope: "user"},
		},
	}
	blocks := a.resolveHotContext(ctx)
	conversable := findConversableBlock(blocks)
	if conversable == nil {
		t.Fatal("expected the conversable section for the agent-chat mount")
	}
	if !strings.Contains(conversable.Text, "- Alice (id: Coder#0001, kind: coder, project: p1, status: idle)") {
		t.Fatalf("agent row missing:\n%s", conversable.Text)
	}
	browser := findBrowserBlock(blocks)
	if browser == nil {
		t.Fatal("expected the mounted-browser-windows section alongside the conversable section")
	}
	if got := strings.Count(browser.Text, "(Config.InstanceID = "); got != 2 {
		t.Fatalf("expected one browser row per mount, got %d:\n%s", got, browser.Text)
	}
	for _, want := range []string{`win-abc123 (Config.InstanceID = "win-abc123")`, `win-def456 (Config.InstanceID = "win-def456")`} {
		if !strings.Contains(browser.Text, want) {
			t.Fatalf("missing %q in block:\n%s", want, browser.Text)
		}
	}
}

// mcpStatusTestCtx builds an agent-test context whose fake planner resolves
// mcp.list_servers to the given server views (or listErr), used by the
// buildMCPStatusBlock hot-context tests.
func mcpStatusTestCtx(t *testing.T, items []domain.McpServerView, listErr error) actor.Context {
	t.Helper()
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "mcp" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "mcp.list_servers" {
					if listErr != nil {
						return nil, listErr
					}
					return domain.McpListServersResp{Items: items}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}
	return ctx
}

// findMCPStatusBlock returns the hot-context block carrying the mounted MCP
// servers section, or nil when absent.
func findMCPStatusBlock(blocks []domain.ContentBlock) *domain.ContentBlock {
	for i := range blocks {
		if strings.Contains(blocks[i].Text, "## Mounted MCP Servers") {
			return &blocks[i]
		}
	}
	return nil
}

func TestResolveHotContext_MCPBlock_Mounted(t *testing.T) {
	items := []domain.McpServerView{
		{ID: "srv-0", Name: "deepwiki", Enabled: true, Status: domain.McpServerStatus{ID: "srv-0", Connected: true, ToolCount: 5}},
		{ID: "srv-1", Name: "playwright", Enabled: true, Status: domain.McpServerStatus{ID: "srv-1", Connected: false, Error: "reconnect failed after 10 attempts: dial refused"}},
		{ID: "srv-2", Name: "pinned-off", Enabled: false, Status: domain.McpServerStatus{ID: "srv-2"}},
	}
	ctx := mcpStatusTestCtx(t, items, nil)
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "mcp:srv-1", Enabled: true, Scope: "user"},
			{CardID: "mcp:srv-0", Enabled: true, Scope: "user"},
			{CardID: "mcp:srv-ghost", Enabled: true, Scope: "user"},
			// Disabled mount contributes nothing.
			{CardID: "mcp:srv-2", Enabled: false, Scope: "user"},
		},
	}
	block := findMCPStatusBlock(a.resolveHotContext(ctx))
	if block == nil {
		t.Fatal("expected a mounted-MCP-servers hot-context block for mcp: mounts")
	}
	if got := strings.Count(block.Text, "## Mounted MCP Servers"); got != 1 {
		t.Fatalf("expected exactly one heading, got %d:\n%s", got, block.Text)
	}
	// Deterministic order: sorted by server id.
	for _, want := range []string{
		"- srv-0 (name: deepwiki, connected, 5 tools)",
		"- srv-1 (name: playwright, DISCONNECTED, 0 tools), error: reconnect failed after 10 attempts: dial refused — call mcp.reconnect with Id=srv-1",
		"- srv-ghost (not registered — no such server in the system)",
		"Call mcp.reconnect with Id=<server-id>",
	} {
		if !strings.Contains(block.Text, want) {
			t.Fatalf("missing %q in block:\n%s", want, block.Text)
		}
	}
	if strings.Contains(block.Text, "srv-2") {
		t.Fatalf("disabled mcp mount must not yield a row:\n%s", block.Text)
	}
	if pos0, pos1, posG := strings.Index(block.Text, "- srv-0"), strings.Index(block.Text, "- srv-1"), strings.Index(block.Text, "- srv-ghost"); !(pos0 < pos1 && pos1 < posG) {
		t.Fatalf("rows must be sorted by server id:\n%s", block.Text)
	}
}

func TestResolveHotContext_MCPBlock_Unmounted(t *testing.T) {
	ctx := mcpStatusTestCtx(t, nil, nil)
	a := &Actor{}
	if block := findMCPStatusBlock(a.resolveHotContext(ctx)); block != nil {
		t.Fatalf("expected no MCP block without mcp: mounts, got:\n%s", block.Text)
	}
	b := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "mcp:srv-0", Enabled: false, Scope: "user"},
		},
	}
	if block := findMCPStatusBlock(b.resolveHotContext(ctx)); block != nil {
		t.Fatalf("expected no MCP block for a disabled mcp: mount, got:\n%s", block.Text)
	}
}

// TestResolveHotContext_MCPBlock_StatusUnavailableDegrades: when
// mcp.list_servers fails the block survives with status-unavailable rows —
// the agent still knows which servers it has mounted.
func TestResolveHotContext_MCPBlock_StatusUnavailableDegrades(t *testing.T) {
	ctx := mcpStatusTestCtx(t, nil, errors.New("manager unreachable"))
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "mcp:srv-0", Enabled: true, Scope: "user"},
		},
	}
	block := findMCPStatusBlock(a.resolveHotContext(ctx))
	if block == nil {
		t.Fatal("expected the MCP block to degrade, not disappear")
	}
	if !strings.Contains(block.Text, "- srv-0 (status unavailable)") {
		t.Fatalf("expected a status-unavailable row:\n%s", block.Text)
	}
}

func TestTruncateMid(t *testing.T) {
	if got := truncateMid("short", 160); got != "short" {
		t.Fatalf("short string must pass through, got %q", got)
	}
	long := strings.Repeat("a", 300)
	got := truncateMid(long, 160)
	if len(got) != 160 {
		t.Fatalf("truncated length = %d, want 160", len(got))
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("truncated string must carry an ellipsis, got %q", got)
	}
}

func TestResolveHotContext_BrowserBlock_Unmounted(t *testing.T) {
	agents := []domain.AgentRef{
		{ID: "Coder#0001", ActorID: "actor-1", DisplayName: "Alice", AgentKind: "coder", LoadState: "loaded", Status: "idle"},
	}
	ctx := conversableTestCtx(t, agents, nil)
	// Agent-chat mount only: no browser section.
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "agent-chat:Coder#0001", Enabled: true, Scope: "user"},
		},
	}
	if block := findBrowserBlock(a.resolveHotContext(ctx)); block != nil {
		t.Fatalf("expected no browser section without browser-chat mounts, got:\n%s", block.Text)
	}
	// Disabled browser-chat mount: treated as unmounted.
	b := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "browser-chat:win-abc123", Enabled: false, Scope: "user"},
		},
	}
	if block := findBrowserBlock(b.resolveHotContext(ctx)); block != nil {
		t.Fatalf("expected no browser section for a disabled browser-chat mount, got:\n%s", block.Text)
	}
	if blk := b.buildConversableBlock(ctx); blk != nil {
		t.Fatalf("buildConversableBlock must return nil for only a disabled browser-chat mount, got:\n%s", blk.Text)
	}
	// Browser mount rescues the block when the agent-chat target is offline.
	c := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "agent-chat:Missing#9", Enabled: true, Scope: "user"},
			{CardID: "browser-chat:win-abc123", Enabled: true, Scope: "user"},
		},
	}
	browser := findBrowserBlock(c.resolveHotContext(ctx))
	if browser == nil {
		t.Fatal("expected the browser section even when every agent-chat target is offline")
	}
	if strings.Contains(browser.Text, "## Conversable Agents") {
		t.Fatalf("offline-only agent section must be dropped, got:\n%s", browser.Text)
	}
}

// hasAgentChatToolContribution reports whether the conversable send-message
// tool is among the snapshot contributions.
func hasAgentChatToolContribution(tools []domain.ComponentToolContribution) bool {
	for _, tool := range tools {
		if tool.CallableID == "workspace.agent_send_message" {
			return true
		}
	}
	return false
}

// TestResolveComponentSnapshot_AgentChatToolsTrackTargetLoadState reproduces
// the restart regression: after a process restart every agent restores
// LoadState="unloaded", so the component snapshot is first built while the
// @-mounted target is offline. The cached snapshot must NOT freeze that
// state — the conversable tools must appear once the target loads and drop
// again if it unloads.
func TestResolveComponentSnapshot_AgentChatToolsTrackTargetLoadState(t *testing.T) {
	loaded := domain.AgentRef{ID: "Coder#0001", ActorID: "actor-1", DisplayName: "Alice", AgentKind: "coder", LoadState: "loaded", Status: "idle"}
	unloaded := loaded
	unloaded.LoadState = "unloaded"
	ctxTargetDown := conversableTestCtx(t, []domain.AgentRef{unloaded}, nil)
	ctxTargetUp := conversableTestCtx(t, []domain.AgentRef{loaded}, nil)
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "agent-chat:Coder#0001", Enabled: true, Scope: "user"},
		},
	}

	// Restart moment: cold snapshot build while the target is still unloaded.
	snap := a.resolveComponentSnapshot(ctxTargetDown)
	if hasAgentChatToolContribution(snap.Tools) {
		t.Fatalf("unloaded target must not contribute conversable tools: %+v", snap.Tools)
	}
	for _, diag := range snap.Diagnostics {
		if strings.Contains(diag.CardID, "agent-chat:") {
			t.Fatalf("agent-chat offline liveness must not surface as a resolution error: %+v", diag)
		}
	}

	// Target loads: the CACHED snapshot must now carry the conversable tools.
	snap = a.resolveComponentSnapshot(ctxTargetUp)
	if !hasAgentChatToolContribution(snap.Tools) {
		t.Fatalf("loaded target must contribute conversable tools on cached reads: %+v", snap.Tools)
	}
	found := false
	for _, tool := range snap.Tools {
		if tool.CallableID == "workspace.agent_send_message" {
			found = true
			if tool.Description != "Send a message to Alice." {
				t.Fatalf("send_message description = %q, want %q", tool.Description, "Send a message to Alice.")
			}
		}
	}
	if !found {
		t.Fatal("workspace.agent_send_message contribution missing")
	}

	// Target unloads again: the next read drops the tools.
	snap = a.resolveComponentSnapshot(ctxTargetDown)
	if hasAgentChatToolContribution(snap.Tools) {
		t.Fatalf("unloaded target must drop conversable tools on cached reads: %+v", snap.Tools)
	}
}

// TestResolveTools_AgentChatToolsInjectedForLoadedTarget verifies the turn
// tool surface end-to-end: with the snapshot cache warmed while the target
// was offline (restart), resolveTools must still inject the conversable
// callables once the target is loaded, carrying the target-naming
// description from the live patch.
func TestResolveTools_AgentChatToolsInjectedForLoadedTarget(t *testing.T) {
	loaded := domain.AgentRef{ID: "Coder#0001", ActorID: "actor-1", DisplayName: "Alice", AgentKind: "coder", LoadState: "loaded", Status: "idle"}
	unloaded := loaded
	unloaded.LoadState = "unloaded"
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "agent-chat:Coder#0001", Enabled: true, Scope: "user"},
		},
	}
	// Warm the cache in the target-down world (restart moment).
	a.resolveComponentSnapshot(conversableTestCtx(t, []domain.AgentRef{unloaded}, nil))

	ctx := conversableTestCtx(t, []domain.AgentRef{loaded}, nil)
	callables := map[string]domain.CallableInterface{
		"workspace.agent_send_message": {Name: "workspace.agent_send_message", Description: "Send"},
		"workspace.agent_read_message": {Name: "workspace.agent_read_message", Description: "Read"},
	}
	tools := a.resolveTools(ctx, domain.AgentKindConfig{Kind: "coder"}, callables)
	found := false
	for _, tool := range tools {
		if tool.CallableID == "workspace.agent_send_message" {
			found = true
			if tool.Description != "Send a message to Alice." {
				t.Fatalf("send_message description = %q, want component-patched %q", tool.Description, "Send a message to Alice.")
			}
		}
	}
	if !found {
		t.Fatalf("workspace.agent_send_message missing from tool surface: %+v", tools)
	}
}

// TestAppendAutonomousTools_DropsInternalForkCallables verifies that:
//  1. The generic "fork_agent" spec (produced by ToolSpecsFromCallables from the
//     bundle's legacy data.tools=fork_agent entry) is dropped when fork aliases
//     (routed to workspace.agent_spawn_by_type) are present, so only intent-revealing
//     fork names reach the LLM.
//  2. The internal "resolve_child_slot" callable is also dropped — it is used
//     by workspace.handleAgentSpawnByType but must not appear in child tool lists.
func TestAppendAutonomousTools_DropsInternalForkCallables(t *testing.T) {
	a := newCompileTestActor(nil, nil, nil)
	var cfg domain.AgentKindConfig
	for _, c := range agentkit.BaseKindConfigs() {
		if c.Kind == domain.AgentKindCoder {
			cfg = c
			break
		}
	}
	if cfg.Kind == "" {
		t.Fatal("coder kind config not found")
	}
	input := []domain.ToolSpec{
		{Name: "file_read", CallableID: "filesystem.read", InputSchema: "{}"},
		{Name: "fork_agent", CallableID: "fork_agent", InputSchema: "{}"},
		{Name: "resolve_child_slot", CallableID: "resolve_child_slot", InputSchema: "{}"},
	}
	out := a.appendAutonomousTools(input, cfg)
	haveForkAgent := false
	haveResolveSlot := false
	aliases := map[string]bool{}
	haveFileRead := false
	for _, s := range out {
		switch s.Name {
		case "fork_agent":
			haveForkAgent = true
		case "resolve_child_slot":
			haveResolveSlot = true
		case "file_read":
			haveFileRead = true
		}
		if s.CallableID == "workspace.agent_spawn_by_type" {
			aliases[s.Name] = true
		}
	}
	if haveForkAgent {
		t.Errorf("generic fork_agent spec should be dropped when fork aliases exist")
	}
	if haveResolveSlot {
		t.Errorf("resolve_child_slot spec should be dropped from child tool list")
	}
	if !haveFileRead {
		t.Errorf("file_read was dropped by the fork filter")
	}
	for _, want := range []string{"fork_explore", "fork_review", "fork_general"} {
		if !aliases[want] {
			t.Errorf("missing fork alias %q (got %v)", want, aliases)
		}
	}
}

// TestEnsureLocalInteractionCallables_PreservesEffectKind verifies that the
// merge logic in ensureLocalInteractionCallables enriches metadata-only shells
// (empty Description + empty Params) without clobbering runtime declarations
// (EffectKind, ServiceName, ToolName, Stream) that the actor registration set.
// Regression: before the fix, a metadata-only shell with EffectKind=reversible
// from the manifest was wholesale-replaced by a localToolCallables entry with
// zero EffectKind, causing component_unmount to slip through the audit gate as
// EffectNone ("allow") instead of EffectReversible ("confirm").
func TestEnsureLocalInteractionCallables_PreservesEffectKind(t *testing.T) {
	// Simulate a manifest entry that carries EffectKind from actor registration
	// but has an empty Description and no Params (metadata-only shell).
	callables := map[string]domain.CallableInterface{
		"component_unmount": {
			Name:       "component_unmount",
			Kind:       "unary",
			Permission: "public",
			// EffectKind set by actor registration; Description/Params empty.
			EffectKind:  string(domain.EffectReversible),
			ServiceName: "agent",
		},
	}

	result := ensureLocalInteractionCallables(callables)

	// 1. EffectKind must survive the merge.
	if got := result["component_unmount"].EffectKind; got != string(domain.EffectReversible) {
		t.Fatalf("component_unmount EffectKind = %q, want %q (runtime declaration clobbered)", got, domain.EffectReversible)
	}

	// 2. ServiceName must survive the merge.
	if got := result["component_unmount"].ServiceName; got != "agent" {
		t.Fatalf("component_unmount ServiceName = %q, want %q", got, "agent")
	}

	// 3. Description must be enriched from the localToolCallables shell.
	if got := result["component_unmount"].Description; got == "" {
		t.Fatalf("component_unmount Description must not be empty after merge")
	}

	// 4. ToolSpecsFromCallables must emit the surviving EffectKind.
	specs := ToolSpecsFromCallables(result, []string{"component_unmount"})
	if len(specs) != 1 {
		t.Fatalf("ToolSpecsFromCallables returned %d specs, want 1", len(specs))
	}
	if got := specs[0].EffectKind; got != string(domain.EffectReversible) {
		t.Fatalf("ToolSpecsFromCallables EffectKind = %q, want %q", got, domain.EffectReversible)
	}
}

// TestEnsureLocalInteractionCallables_FullShellUnchanged verifies that a
// manifest entry with both Description and Params set is left completely
// untouched by the merge logic (the "default" branch).
func TestEnsureLocalInteractionCallables_FullShellUnchanged(t *testing.T) {
	callables := map[string]domain.CallableInterface{
		"component_mount": {
			Name:        "component_mount",
			Description: "Custom description from manifest",
			Params: []domain.CallableParam{
				{Name: "CardId", Type: "string", Required: true},
			},
			EffectKind: "irreversible",
		},
	}
	orig := callables["component_mount"].Description

	result := ensureLocalInteractionCallables(callables)

	if result["component_mount"].Description != orig {
		t.Errorf("Description changed from %q to %q; full shells must not be mutated", orig, result["component_mount"].Description)
	}
	if got := result["component_mount"].EffectKind; got != "irreversible" {
		t.Errorf("EffectKind = %q, want irreversible", got)
	}
}

// TestEnsureLocalInteractionCallables_InsertsAbsent verifies that callables
// not present in the manifest are inserted with the full localToolCallables
// shell (the "!ok" branch).
func TestEnsureLocalInteractionCallables_InsertsAbsent(t *testing.T) {
	result := ensureLocalInteractionCallables(nil)

	component, ok := result["component_unmount"]
	if !ok {
		t.Fatalf("component_unmount missing from result")
	}
	if component.Description == "" {
		t.Fatalf("absent callable must be inserted with a Description")
	}
	if component.Params == nil {
		t.Fatalf("absent callable must be inserted with Params")
	}
}

// TestCompileDispatchMessages_StripsPastTurnImages verifies the past-turn image
// rule: images are compiled only for the current turn's messages (latest user
// turn + active assistant turn). Image blocks from earlier turns are replaced
// by a text placeholder, so a text-only unit taking over the session
// (aggregator rotation, model switch) no longer receives doomed image content
// that would 400 with "messages.content.type 参数非法，取值范围 ['text']".
func TestCompileDispatchMessages_StripsPastTurnImages(t *testing.T) {
	imageBlock := func(url string) domain.ContentBlock {
		return domain.ContentBlock{Type: domain.ContentBlockImage, ImageURL: url, MimeType: "image/png"}
	}
	recognizedBlock := func(url string) domain.ContentBlock {
		b := imageBlock(url)
		b.Recognized = true
		b.RecognitionText = "a chart trending up"
		return b
	}
	failedBlock := func(url string) domain.ContentBlock {
		b := imageBlock(url)
		b.Recognized = true
		b.RecognitionText = imageRecognitionFailedPrefix + ": no vision unit]"
		return b
	}
	turns := []domain.Turn{
		{ID: "u1", Role: "user"},
		{ID: "t1", Role: "assistant"},
		{ID: "u2", Role: "user"},
	}
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "u1", Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: "look at this"},
			imageBlock("https://example/past.png"),
		}},
		{ID: "u0r", Role: "user", Type: "text", TurnID: "u1", Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: "earlier chart"},
			recognizedBlock("https://example/recognized.png"),
		}},
		{ID: "u0f", Role: "user", Type: "text", TurnID: "u1", Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: "earlier fail"},
			failedBlock("https://example/failed.png"),
		}},
		{ID: "t1", Role: "assistant", Type: "text", TurnID: "t1", Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: "got it"},
		}},
		{ID: "inj1", Role: "user", Type: "user_inject", TurnID: "turn-2", Content: []domain.ContentBlock{
			imageBlock("https://example/inject.png"),
		}},
		{ID: "u2", Role: "user", Type: "text", TurnID: "u2", Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: "and this one"},
			imageBlock("https://example/current.png"),
		}},
	}
	a := newCompileTestActor(turns, nil, steps)
	a.setActiveTurnRef("turn-2")
	a.status.TurnID = "turn-2"

	msgs := a.compileDispatchMessages()
	msgByID := make(map[string]domain.ChatMessage, len(msgs))
	for _, m := range msgs {
		msgByID[m.ID] = m
	}

	// Past-turn user message: image replaced by placeholder, text preserved.
	past, ok := msgByID["u1"]
	if !ok {
		t.Fatalf("past-turn user message missing from compiled dispatch context: %+v", msgs)
	}
	hasPlaceholder, hasImage, hasRef := false, false, false
	for _, b := range past.Content {
		switch {
		case b.Type == domain.ContentBlockText && b.Text == pastTurnImagePlaceholder+"\n[image ref: msg:u1:1]":
			hasPlaceholder, hasRef = true, true
		case b.Type == domain.ContentBlockImage:
			hasImage = true
		}
	}
	if !hasPlaceholder || hasImage {
		t.Errorf("past-turn image not stripped: hasPlaceholder=%v hasImage=%v, content=%+v", hasPlaceholder, hasImage, past.Content)
	}
	if !hasRef {
		t.Errorf("past-turn placeholder must carry the msg ref handle, content=%+v", past.Content)
	}

	// Past-turn RECOGNIZED image: the description text survives (already paid
	// for) with the recognized marker and the msg ref — not the bare placeholder.
	rec, ok := msgByID["u0r"]
	if !ok {
		t.Fatalf("recognized past-turn message missing: %+v", msgs)
	}
	foundRec := false
	for _, b := range rec.Content {
		if b.Type == domain.ContentBlockText {
			want := "[image recognized]\na chart trending up\n[image ref: msg:u0r:1]"
			if b.Text == want {
				foundRec = true
			}
		}
		if b.Type == domain.ContentBlockImage {
			t.Errorf("recognized past-turn image block leaked: %+v", rec.Content)
		}
	}
	if !foundRec {
		t.Errorf("recognized past-turn image must keep its text + ref, got %+v", rec.Content)
	}

	// Past-turn FAILED recognition: keeps the explicit failure marker (no
	// "[image recognized]" pretense) and the ref handle.
	failed, ok := msgByID["u0f"]
	if !ok {
		t.Fatalf("failed past-turn message missing: %+v", msgs)
	}
	foundFailed := false
	for _, b := range failed.Content {
		if b.Type == domain.ContentBlockText && strings.HasPrefix(b.Text, imageRecognitionFailedPrefix) && strings.Contains(b.Text, "[image ref: msg:u0f:1]") {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Errorf("failed past-turn recognition must keep failure marker + ref, got %+v", failed.Content)
	}

	// Current-turn user message: image compiled as-is.
	current, ok := msgByID["u2"]
	if !ok {
		t.Fatalf("current-turn user message missing from compiled dispatch context: %+v", msgs)
	}
	currentImage := false
	for _, b := range current.Content {
		if b.Type == domain.ContentBlockImage && b.ImageURL == "https://example/current.png" {
			currentImage = true
		}
	}
	if !currentImage {
		t.Errorf("current-turn image must stay in the dispatch context, content=%+v", current.Content)
	}

	// Mid-turn user_inject anchored to the active assistant turn: image kept.
	injected, ok := msgByID["inj1"]
	if !ok {
		t.Fatalf("user_inject message missing from compiled dispatch context: %+v", msgs)
	}
	injectImage := false
	for _, b := range injected.Content {
		if b.Type == domain.ContentBlockImage && b.ImageURL == "https://example/inject.png" {
			injectImage = true
		}
	}
	if !injectImage {
		t.Errorf("active-turn user_inject image must stay in the dispatch context, content=%+v", injected.Content)
	}

	// The plain compile path (prompt artifact / probe views) is unaffected.
	raw := a.compileMessages(true)
	for _, m := range raw {
		if m.ID != "u1" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == domain.ContentBlockImage && b.ImageURL == "https://example/past.png" {
				return // image preserved on the raw path
			}
		}
		t.Fatalf("compileMessages must not strip images (raw path), u1 content=%+v", m.Content)
	}
}
