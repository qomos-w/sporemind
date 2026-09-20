package compaction

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/tokenest"
)

// ── EstimateSessionTokens ────────────────────────────────────

func TestEstimateSessionTokens_CountsText(t *testing.T) {
	// Realistic English text: tiktoken BPE encodes ~4 chars/token, matching
	// the fallback heuristic's assumption. (Degenerate repeated-character
	// input like "aaaa..." merges into long BPE tokens and breaks the ~4
	// chars/token expectation, so don't use it here.)
	content := strings.Repeat("hello world this is a token estimation test ", 22)
	messages := []domain.ChatMessage{
		{Role: "user", Idx: 0, Content: []domain.ContentBlock{
			{Type: "text", Text: content},
		}},
	}
	tokens := EstimateSessionTokens(messages, nil, -1)
	if tokens <= 0 {
		t.Fatal("expected positive token count")
	}
	heuristic := len(content) / 4
	// Accept anything within 60% of the heuristic estimate so both the real
	// tiktoken encoder and the fallback heuristic pass.
	lo, hi := heuristic*2/5, heuristic*8/5
	if int(tokens) < lo || int(tokens) > hi {
		t.Fatalf("expected ~%d tokens, got %d", heuristic, tokens)
	}
}

func TestEstimateSessionTokens_IncludesInput(t *testing.T) {
	messages := []domain.ChatMessage{
		{Role: "assistant", Idx: 0, Content: []domain.ContentBlock{
			{Type: "tool_use", Input: `{"path":"/very/long/path"}`},
		}},
	}
	tokens := EstimateSessionTokens(messages, nil, -1)
	if tokens <= 0 {
		t.Fatal("expected tokens from Input field")
	}
}

func TestEstimateSessionTokens_IncludesSummarySegments(t *testing.T) {
	segments := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Level: 1, Text: strings.Repeat("s", 400)},
	}
	tokens := EstimateSessionTokens(nil, segments, -1)
	if tokens <= 0 {
		t.Fatal("expected tokens from summary segments")
	}
}

func TestEstimateSessionTokens_SkipsCoveredMessages(t *testing.T) {
	messages := []domain.ChatMessage{
		{Role: "user", Idx: 0, Content: []domain.ContentBlock{{Type: "text", Text: strings.Repeat("a", 1000)}}},
		{Role: "user", Idx: 1, Content: []domain.ContentBlock{{Type: "text", Text: strings.Repeat("b", 1000)}}},
		{Role: "user", Idx: 2, Content: []domain.ContentBlock{{Type: "text", Text: strings.Repeat("c", 1000)}}},
	}
	segments := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 1, Level: 1, Text: "summary"},
	}
	// CoveredEnd=1: messages 0 and 1 are user messages — never skipped.
	// All 3 user messages + summary counted (~750 total).
	tokens := EstimateSessionTokens(messages, segments, 1)
	if tokens <= 0 {
		t.Fatal("expected positive token count")
	}
	if tokens < 600 || tokens > 900 {
		t.Fatalf("expected ~752 tokens (3 user msgs + summary), got %d", tokens)
	}
}

func TestEstimateSessionTokens_Empty(t *testing.T) {
	tokens := EstimateSessionTokens(nil, nil, -1)
	if tokens != 0 {
		t.Fatalf("expected 0, got %d", tokens)
	}
}

// ── SplitIndex ───────────────────────────────────────────────

func TestSplitIndex_Normal(t *testing.T) {
	msgs := make([]domain.ChatMessage, 30)
	idx := SplitIndex(msgs, 10)
	if idx != 20 {
		t.Fatalf("expected 20, got %d", idx)
	}
}

func TestSplitIndex_WindowLargerThanMessages(t *testing.T) {
	msgs := make([]domain.ChatMessage, 5)
	idx := SplitIndex(msgs, 10)
	if idx != 0 {
		t.Fatalf("expected 0 (no split), got %d", idx)
	}
}

func TestSplitIndex_ZeroWindow(t *testing.T) {
	msgs := make([]domain.ChatMessage, 10)
	idx := SplitIndex(msgs, 0)
	if idx != 0 {
		t.Fatalf("expected 0, got %d", idx)
	}
}

func TestSplitIndex_EmptyMessages(t *testing.T) {
	idx := SplitIndex(nil, 10)
	if idx != 0 {
		t.Fatalf("expected 0, got %d", idx)
	}
}

// ── SummarizeCompact ─────────────────────────────────────────

func TestTruncateSummaryTokens_RespectsLimit(t *testing.T) {
	text := strings.Repeat("summary ", 100)
	got := TruncateSummaryTokens(text, 10)
	if tokenest.EstimateTokens(got) > 10 {
		t.Fatalf("summary tokens = %d, want <= 10", tokenest.EstimateTokens(got))
	}
	if got == "" {
		t.Fatal("truncated summary should remain non-empty")
	}
}

func TestTruncateSummaryTokens_PreservesShortText(t *testing.T) {
	text := "short summary"
	if got := TruncateSummaryTokens(text, 100); got != text {
		t.Fatalf("short summary changed: %q", got)
	}
}

func TestSummarizeCompact_Success(t *testing.T) {
	msgs := make([]domain.ChatMessage, 10)
	for i := range msgs {
		msgs[i].Idx = int32(i)
		msgs[i].Role = "assistant"
		msgs[i].Content = []domain.ContentBlock{{Type: "text", Text: "message"}}
	}
	var segments []domain.SummarySegment

	inputText, srcStart, srcEnd := BuildCompactInput(msgs, segments, 5)
	if inputText == "" {
		t.Fatal("expected non-empty input text")
	}
	if srcStart != 0 || srcEnd != 4 {
		t.Fatalf("expected index range [0,4], got [%d,%d]", srcStart, srcEnd)
	}
	SummarizeCompact(&segments, srcStart, srcEnd, "LLM summary of conversation")

	if len(segments) != 1 {
		t.Fatalf("expected 1 summary segment, got %d", len(segments))
	}
	seg := segments[0]
	if seg.Text != "LLM summary of conversation" {
		t.Fatalf("unexpected summary: %q", seg.Text)
	}
	if seg.SourceStartIndex != 0 || seg.SourceEndIndex != 4 {
		t.Fatalf("expected segment [0,4], got [%d,%d]", seg.SourceStartIndex, seg.SourceEndIndex)
	}

	// Empty/whitespace summaries must not be appended; they would create false
	// coverage and break the contiguous-coverage invariant.
	SummarizeCompact(&segments, 0, 4, "   ")
	if len(segments) != 1 {
		t.Fatalf("expected empty summary to be ignored, got %d segments", len(segments))
	}
}

func TestBuildCompactInput_Incremental(t *testing.T) {
	msgs := make([]domain.ChatMessage, 20)
	for i := range msgs {
		msgs[i].Idx = int32(i)
		msgs[i].Role = "assistant"
		msgs[i].Content = []domain.ContentBlock{{Type: "text", Text: "message"}}
	}
	segments := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Level: 1, Text: "previous summary"},
	}

	inputText, srcStart, srcEnd := BuildCompactInput(msgs, segments, 15)
	if inputText == "" {
		t.Fatal("expected non-empty input text")
	}
	if strings.Contains(inputText, "previous summary") {
		t.Fatal("should NOT include previous summary — segments are independent")
	}
	// Second segment covers [5, 14]
	if srcStart != 5 || srcEnd != 14 {
		t.Fatalf("expected index range [5,14], got [%d,%d]", srcStart, srcEnd)
	}
	SummarizeCompact(&segments, srcStart, srcEnd, "new summary")
	if len(segments) != 2 {
		t.Fatalf("expected 2 summary segments, got %d", len(segments))
	}
	// First segment unchanged
	if segments[0].SourceEndIndex != 4 {
		t.Fatalf("expected first seg end=4, got %d", segments[0].SourceEndIndex)
	}
	seg := segments[1]
	if seg.SourceStartIndex != 5 || seg.SourceEndIndex != 14 {
		t.Fatalf("expected segment [5,14], got [%d,%d]", seg.SourceStartIndex, seg.SourceEndIndex)
	}
}

func TestBuildCompactInput_Empty(t *testing.T) {
	msgs := make([]domain.ChatMessage, 5)
	var segments []domain.SummarySegment
	// splitIdx=0 means nothing to compact
	inputText, _, _ := BuildCompactInput(msgs, segments, 0)
	if inputText != "" {
		t.Fatal("expected empty input when nothing to compact")
	}
}

// ── CollapseCompact ──────────────────────────────────────────

func TestCollapseCompact_DropsOldMessages(t *testing.T) {
	msgs := make([]domain.ChatMessage, 20)
	for i := range msgs {
		msgs[i].Idx = int32(i)
		msgs[i].Role = "user"
		msgs[i].Content = []domain.ContentBlock{{Type: "text", Text: "msg"}}
	}
	var segments []domain.SummarySegment

	err := CollapseCompact(msgs, &segments, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected 1 summary segment, got %d", len(segments))
	}
	seg := segments[0]
	if seg.SourceStartIndex != 0 || seg.SourceEndIndex != 15 {
		t.Fatalf("expected segment [0,15], got [%d,%d]", seg.SourceStartIndex, seg.SourceEndIndex)
	}
}

func TestCollapseCompact_SessionSmallerThanWindow(t *testing.T) {
	msgs := make([]domain.ChatMessage, 3)
	var segments []domain.SummarySegment

	err := CollapseCompact(msgs, &segments, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 0 {
		t.Fatal("expected no collapse when session smaller than window")
	}
}

// ── IsPromptTooLongError ─────────────────────────────────────

func TestIsPromptTooLongError_Anthropic(t *testing.T) {
	if !IsPromptTooLongError(fmt.Errorf("prompt is too long: 100000 tokens > 20000 maximum")) {
		t.Fatal("should detect Anthropic error")
	}
}

func TestIsPromptTooLongError_OpenAI(t *testing.T) {
	if !IsPromptTooLongError(fmt.Errorf("context_length_exceeded")) {
		t.Fatal("should detect OpenAI error")
	}
}

func TestIsPromptTooLongError_Other(t *testing.T) {
	if IsPromptTooLongError(fmt.Errorf("connection refused")) {
		t.Fatal("should not match unrelated errors")
	}
}

func TestIsPromptTooLongError_Kimi(t *testing.T) {
	err := fmt.Errorf(`http 400: {"error":{"message":"Invalid request: Your request exceeded model token limit: 262144 (requested: 262590)","type":"invalid_request_error"}}`)
	if !IsPromptTooLongError(err) {
		t.Fatal("should detect Kimi token-limit error")
	}
}

func TestIsPromptTooLongError_Nil(t *testing.T) {
	if IsPromptTooLongError(nil) {
		t.Fatal("nil should return false")
	}
}

// ── SummarizeSegments ────────────────────────────────────────

func TestSummarizeSegments_TwoLevels(t *testing.T) {
	segments := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Level: 1, Text: "summary of msgs 0-4"},
		{SourceStartIndex: 5, SourceEndIndex: 9, Level: 1, Text: "summary of msgs 5-9"},
	}

	inputText, minStart, maxEnd, ok := BuildSegmentsInput(segments, 1)
	if !ok {
		t.Fatal("expected ok=true for 2 L1 segments")
	}
	if !strings.Contains(inputText, "msgs 0-4") || !strings.Contains(inputText, "msgs 5-9") {
		t.Fatalf("expected both L1 summaries in input, got %q", inputText)
	}
	if minStart != 0 || maxEnd != 9 {
		t.Fatalf("expected [0,9], got [%d,%d]", minStart, maxEnd)
	}

	SummarizeSegments(&segments, 1, "L2 summary")

	if len(segments) != 1 {
		t.Fatalf("expected 1 segment after merge, got %d", len(segments))
	}
	seg := segments[0]
	if seg.Level != 2 {
		t.Fatalf("expected level 2, got %d", seg.Level)
	}
	if seg.SourceStartIndex != 0 || seg.SourceEndIndex != 9 {
		t.Fatalf("expected [0,9], got [%d,%d]", seg.SourceStartIndex, seg.SourceEndIndex)
	}
	if seg.Text != "L2 summary" {
		t.Fatalf("expected L2 summary, got %q", seg.Text)
	}
}

func TestSummarizeSegments_OnlyOneSegment_NoOp(t *testing.T) {
	segments := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Level: 1, Text: "only one"},
	}

	_, _, _, ok := BuildSegmentsInput(segments, 1)
	if ok {
		t.Fatal("expected ok=false with only 1 segment")
	}

	// SummarizeSegments is a no-op when fewer than 2 segments at the level.
	SummarizeSegments(&segments, 1, "should not be used")
	if len(segments) != 1 {
		t.Fatal("should remain unchanged")
	}
}

func TestSummarizeSegments_PreservesOtherLevels(t *testing.T) {
	segments := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Level: 1, Text: "L1-a"},
		{SourceStartIndex: 5, SourceEndIndex: 9, Level: 1, Text: "L1-b"},
		{SourceStartIndex: 0, SourceEndIndex: 9, Level: 2, Text: "existing L2"},
	}

	inputText, minStart, maxEnd, ok := BuildSegmentsInput(segments, 1)
	if !ok {
		t.Fatal("expected ok=true for 2 L1 segments")
	}
	if minStart != 0 || maxEnd != 9 {
		t.Fatalf("expected [0,9], got [%d,%d]", minStart, maxEnd)
	}
	if !strings.Contains(inputText, "L1-a") || !strings.Contains(inputText, "L1-b") {
		t.Fatalf("expected L1 texts in input, got %q", inputText)
	}

	SummarizeSegments(&segments, 1, "new L2")

	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segments))
	}
	var newL2 *domain.SummarySegment
	for i := range segments {
		if segments[i].Text == "new L2" {
			newL2 = &segments[i]
		}
	}
	if newL2 == nil {
		t.Fatal("expected new L2 segment")
	}
	if newL2.Level != 2 {
		t.Fatalf("expected level 2, got %d", newL2.Level)
	}
}

// ── SegmentsAtLevel ──────────────────────────────────────────

func TestSegmentsAtLevel(t *testing.T) {
	segments := []domain.SummarySegment{
		{Level: 1, Text: "a"},
		{Level: 1, Text: "b"},
		{Level: 2, Text: "c"},
	}
	if n := SegmentsAtLevel(segments, 1); n != 2 {
		t.Fatalf("expected 2 L1 segments, got %d", n)
	}
	if n := SegmentsAtLevel(segments, 2); n != 1 {
		t.Fatalf("expected 1 L2 segment, got %d", n)
	}
	if n := SegmentsAtLevel(segments, 3); n != 0 {
		t.Fatalf("expected 0 L3 segments, got %d", n)
	}
}

// ── BuildCompactInputSteps regressions ───────────────────────

func TestBuildCompactInputSteps_SkipsCompactionOnlySteps(t *testing.T) {
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
		{ID: "a1", Role: "assistant", Type: "text", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
		{ID: "c1", Role: "assistant", Type: "text", Content: []domain.ContentBlock{{Type: "compaction", Text: `{}`}}},
	}
	input, srcStart, srcEnd := BuildCompactInputSteps(steps, nil, 3)
	if input == "" {
		t.Fatal("expected non-empty input")
	}
	if srcStart != 0 || srcEnd != 2 {
		t.Fatalf("expected source range [0,2], got [%d,%d]", srcStart, srcEnd)
	}
	if !strings.Contains(input, "hi") {
		t.Fatal("input should contain the real assistant step")
	}
	if strings.Contains(input, "compaction") {
		t.Fatal("input should not contain compaction frame text")
	}
	if !strings.Contains(input, "hello") {
		t.Fatal("user step text should now be included")
	}
}

func TestBuildCompactInputSteps_IncludesUserText(t *testing.T) {
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "Please refactor auth.go"}}},
		{ID: "a1", Role: "assistant", Type: "text", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "I will refactor auth.go"}}},
	}
	input, srcStart, srcEnd := BuildCompactInputSteps(steps, nil, 2)
	if input == "" {
		t.Fatal("expected non-empty input")
	}
	if srcStart != 0 || srcEnd != 1 {
		t.Fatalf("expected source range [0,1], got [%d,%d]", srcStart, srcEnd)
	}
	if !strings.Contains(input, "user: Please refactor auth.go") {
		t.Fatalf("expected user text, got %q", input)
	}
	if !strings.Contains(input, "assistant: I will refactor auth.go") {
		t.Fatalf("expected assistant text, got %q", input)
	}
}

func TestBuildCompactInputSteps_SkipsUserImages(t *testing.T) {
	steps := []domain.Step{
		{ID: "u1", Role: "user", Type: "text", Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: "Look at this screenshot"},
			{Type: "image", Text: strings.Repeat("iVBORw0KGgo=", 1000)},
		}},
		{ID: "a1", Role: "assistant", Type: "text", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "I see the screenshot"}}},
	}
	input, srcStart, srcEnd := BuildCompactInputSteps(steps, nil, 2)
	if input == "" {
		t.Fatal("expected non-empty input")
	}
	if srcStart != 0 || srcEnd != 1 {
		t.Fatalf("expected source range [0,1], got [%d,%d]", srcStart, srcEnd)
	}
	if !strings.Contains(input, "Look at this screenshot") {
		t.Fatalf("expected user text, got %q", input)
	}
	if strings.Contains(input, "iVBORw0KGgo=") {
		t.Fatal("image base64 should not appear in input")
	}
	if !strings.Contains(input, "[image attachment omitted]") {
		t.Fatalf("expected image placeholder, got %q", input)
	}
}

func TestBuildCompactInputSteps_ToolResultTruncation(t *testing.T) {
	longText := strings.Repeat("a", DefaultToolResultMaxBytes+500)
	steps := []domain.Step{
		{ID: "a1", Role: "assistant", Type: "text", Content: []domain.ContentBlock{{Type: domain.ContentBlockToolUse, ToolName: "read_file", Input: `{"path":"/tmp/big.log"}`}}},
		{ID: "t1", Role: "tool", Type: "text", Content: []domain.ContentBlock{{Type: domain.ContentBlockToolResult, ToolName: "read_file", Text: longText}}},
	}
	input, _, _ := BuildCompactInputSteps(steps, nil, 2)
	if input == "" {
		t.Fatal("expected non-empty input")
	}
	if !strings.Contains(input, "[tool_result:read_file]") {
		t.Fatalf("expected tool_result header, got %q", input)
	}
	if strings.Contains(input, strings.Repeat("a", DefaultToolResultMaxBytes+1)) {
		t.Fatal("tool result should be truncated")
	}
	if !strings.Contains(input, "... [truncated]") {
		t.Fatalf("expected truncation marker, got %q", input)
	}
}

func TestBuildSegmentsInput_DoesNotCrossGap(t *testing.T) {
	segments := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Level: 1, Text: "first"},
		{SourceStartIndex: 10, SourceEndIndex: 14, Level: 1, Text: "second"},
	}
	if _, _, _, ok := BuildSegmentsInput(segments, 1); ok {
		t.Fatal("must not merge summary segments across an uncovered gap")
	}
}

func TestSummarizeSegments_DuplicateValuesOnlyRemovesSelectedGroup(t *testing.T) {
	segments := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Level: 1, Text: "same"},
		{SourceStartIndex: 5, SourceEndIndex: 9, Level: 1, Text: "next"},
		{SourceStartIndex: 20, SourceEndIndex: 24, Level: 1, Text: "same"},
	}
	SummarizeSegments(&segments, 1, "merged")
	if len(segments) != 2 {
		t.Fatalf("expected merged group and untouched segment, got %+v", segments)
	}
	foundUntouched := false
	for _, seg := range segments {
		if seg.SourceStartIndex == 20 && seg.Text == "same" {
			foundUntouched = true
		}
	}
	if !foundUntouched {
		t.Fatal("summary outside selected group was removed")
	}
}

func TestSummarizeSegments_EmptySummaryPreservesInput(t *testing.T) {
	segments := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Level: 1, Text: "first"},
		{SourceStartIndex: 5, SourceEndIndex: 9, Level: 1, Text: "second"},
	}
	SummarizeSegments(&segments, 1, " \n\t ")
	if len(segments) != 2 || segments[0].Text != "first" || segments[1].Text != "second" {
		t.Fatalf("empty summary must preserve input segments, got %+v", segments)
	}
}

func TestBuildSegmentsInput_WithBoundaryMarkers(t *testing.T) {
	segments := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Level: 1, Text: "summary of msgs 0-4"},
		{SourceStartIndex: 5, SourceEndIndex: 9, Level: 1, Text: "summary of msgs 5-9"},
	}
	inputText, minStart, maxEnd, ok := BuildSegmentsInput(segments, 1)
	if !ok {
		t.Fatal("expected ok=true for 2 L1 segments")
	}
	if minStart != 0 || maxEnd != 9 {
		t.Fatalf("expected [0,9], got [%d,%d]", minStart, maxEnd)
	}
	if !strings.Contains(inputText, "msgs 0-4") || !strings.Contains(inputText, "msgs 5-9") {
		t.Fatalf("expected both summaries in input, got %q", inputText)
	}
	if !strings.Contains(inputText, "\n\n---\n\n") {
		t.Fatalf("expected segment separator, got %q", inputText)
	}
	if !strings.Contains(inputText, "<!-- segment level=1 source=[0,4] -->") {
		t.Fatalf("expected boundary marker for first segment, got %q", inputText)
	}
	if !strings.Contains(inputText, "<!-- segment level=1 source=[5,9] -->") {
		t.Fatalf("expected boundary marker for second segment, got %q", inputText)
	}
}

func TestBuildCompactInputSteps_SkipsCompactionContentBlocks(t *testing.T) {
	steps := []domain.Step{
		{ID: "a1", Role: "assistant", Type: "text", Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: "first"},
			{Type: "compaction", Text: `{"ignored":true}`},
			{Type: domain.ContentBlockText, Text: "second"},
		}},
	}
	input, _, _ := BuildCompactInputSteps(steps, nil, 1)
	if strings.Contains(input, "ignored") {
		t.Fatal("compaction block content should be excluded from summarizer input")
	}
	if !strings.Contains(input, "first") || !strings.Contains(input, "second") {
		t.Fatalf("regular text blocks should remain, got %q", input)
	}
}

func TestBuildCompactInputSteps_ResumesAfterContiguousCoverage(t *testing.T) {
	steps := make([]domain.Step, 0, 220)
	for i := 0; i < 220; i++ {
		role := "assistant"
		if i%10 == 0 {
			role = "user"
		}
		steps = append(steps, domain.Step{
			ID:   fmt.Sprintf("s%d", i),
			Role: role,
			Type: "text",
			Content: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: fmt.Sprintf("step %d", i),
			}},
		})
	}
	// A gap in coverage (0-99 then 200-219) must not cause 100-199 to be skipped.
	segments := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 99, Level: 1, Text: "first summary"},
		{SourceStartIndex: 200, SourceEndIndex: 219, Level: 1, Text: "last summary"},
	}
	input, srcStart, srcEnd := BuildCompactInputSteps(steps, segments, 200)
	if input == "" {
		t.Fatal("expected input covering the gap")
	}
	if srcStart != 100 {
		t.Fatalf("expected resume at 100, got %d", srcStart)
	}
	if srcEnd != 199 {
		t.Fatalf("expected end 199, got %d", srcEnd)
	}
	if strings.Contains(input, "step 0") {
		t.Fatal("already-covered steps should not be re-summarized")
	}
	if !strings.Contains(input, "step 101") {
		t.Fatal("gap steps must be included in input")
	}
}

func TestSummarizeCompact_GuardsEmptySummary(t *testing.T) {
	var segments []domain.SummarySegment
	SummarizeCompact(&segments, 0, 4, "real summary")
	if len(segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segments))
	}
	SummarizeCompact(&segments, 5, 9, "")
	SummarizeCompact(&segments, 5, 9, "   \n\t  ")
	if len(segments) != 1 {
		t.Fatalf("expected empty/whitespace summaries to be ignored, got %d", len(segments))
	}
}

// ── TimeoutForTokens ─────────────────────────────────────────

func TestTimeoutForTokens(t *testing.T) {
	cases := []struct {
		name  string
		input int
		want  string
	}{
		{"zero returns base", 0, "1m0s"},
		{"negative returns base", -1, "1m0s"},
		{"small returns base", 100, "1m0s"},
		{"10k adds one increment", 10000, "1m30s"},
		{"20k adds two increments", 20000, "2m0s"},
		{"40k adds four increments", 40000, "3m0s"},
		{"80k hits max", 80000, "5m0s"},
		{"100k clamped to max", 100000, "5m0s"},
		{"200k clamped to max", 200000, "5m0s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TimeoutForTokens(tc.input).String()
			if got != tc.want {
				t.Errorf("TimeoutForTokens(%d) = %s, want %s", tc.input, got, tc.want)
			}
		})
	}
}
