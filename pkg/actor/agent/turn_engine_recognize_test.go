package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// newRecognizeTestEngine builds a minimal engine whose history holds one user
// message with one image block, plus a counting planner that answers
// aiaggregator.image_recognize calls.
func newRecognizeTestEngine(t *testing.T, model string, plannerErr error) (*turnEngine, *testutil.FakeCtx, *int32, *[]string) {
	t.Helper()
	var calls int32
	aggRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	e := &turnEngine{
		logger: newNopActorLogger(),
		turnID: "turn-1",
		history: []domain.ChatMessage{
			{
				ID:   "user-turn-1",
				Role: domain.ChatRoleUser,
				Content: []domain.ContentBlock{
					{Type: domain.ContentBlockText, Text: "what is in this picture?"},
					{Type: domain.ContentBlockImage, ImageURL: "data:image/png;base64,aGVsbG8=", MimeType: "image/png"},
				},
			},
		},
		resolveTargets: func(actor.PureContext, domain.ModelSlot) []dispatchTarget {
			return []dispatchTarget{{aggRef: aggRef, unit: domain.ModelUnit{Model: model, Provider: "test"}}}
		},
	}
	var captured []string
	e.onImageRecognized = func(stepID string, blockIdx int, recognized bool, text string) {
		captured = append(captured, stepID)
	}
	ctx := &testutil.FakeCtx{PlannerFn: func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
			atomic.AddInt32(&calls, 1)
			if callID != "aiaggregator.image_recognize" {
				t.Errorf("unexpected call %q", callID)
			}
			if plannerErr != nil {
				return nil, plannerErr
			}
			return gen.AIAggregatorImageRecognizeResp{Text: "a red square on white background"}, nil
		}}
	}}
	return e, ctx, &calls, &captured
}

// TestRecognizeHistoryImages_TextOnlyUnitRecognizesOnce verifies the full
// one-shot contract for a text-only primary: exactly one aggregator call, the
// block is marked Recognized with the recognition text, the actor write-back
// callback fires, and a second pass makes zero calls.
func TestRecognizeHistoryImages_TextOnlyUnitRecognizesOnce(t *testing.T) {
	e, ctx, calls, captured := newRecognizeTestEngine(t, "deepseek-chat", nil)

	e.recognizeHistoryImages(ctx)

	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("planner calls = %d, want 1", n)
	}
	block := e.history[0].Content[1]
	if !block.Recognized {
		t.Error("block.Recognized = false, want true")
	}
	if block.RecognitionText != "a red square on white background" {
		t.Errorf("RecognitionText = %q", block.RecognitionText)
	}
	if !e.dispatchImagesAsText {
		t.Error("dispatchImagesAsText = false, want true for text-only unit")
	}
	if len(*captured) != 1 || (*captured)[0] != "user-turn-1" {
		t.Errorf("onImageRecognized captured = %v, want [user-turn-1]", *captured)
	}

	e.recognizeHistoryImages(ctx)
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Errorf("second pass planner calls = %d, want 1 (recognition must be one-shot)", n)
	}
}

// TestRecognizeHistoryImages_VisionUnitSkipsPass verifies a vision primary
// never triggers recognition and never reads recognition state.
func TestRecognizeHistoryImages_VisionUnitSkipsPass(t *testing.T) {
	e, ctx, calls, _ := newRecognizeTestEngine(t, "gpt-4o", nil)

	e.recognizeHistoryImages(ctx)

	if n := atomic.LoadInt32(calls); n != 0 {
		t.Errorf("planner calls = %d, want 0 for vision unit", n)
	}
	if e.dispatchImagesAsText {
		t.Error("dispatchImagesAsText = true for vision unit, want false")
	}
	block := e.history[0].Content[1]
	if block.Recognized || block.RecognitionText != "" {
		t.Errorf("vision pass mutated block: %+v", block)
	}
}

// TestRecognizeHistoryImages_AggregatorSlotWithoutUnitSkipsPass: an
// aggregator-backed slot with no pinned unit may route to a vision model, so
// the pass must not engage.
func TestRecognizeHistoryImages_AggregatorSlotWithoutUnitSkipsPass(t *testing.T) {
	e, ctx, calls, _ := newRecognizeTestEngine(t, "", nil)

	e.recognizeHistoryImages(ctx)

	if n := atomic.LoadInt32(calls); n != 0 {
		t.Errorf("planner calls = %d, want 0", n)
	}
	if e.dispatchImagesAsText {
		t.Error("dispatchImagesAsText = true, want false")
	}
}

// TestRecognizeHistoryImages_FailureMarksOnce: a failing recognition marks the
// block Recognized with the failure text — one attempt, never retried.
func TestRecognizeHistoryImages_FailureMarksOnce(t *testing.T) {
	e, ctx, calls, _ := newRecognizeTestEngine(t, "deepseek-chat", context.DeadlineExceeded)

	e.recognizeHistoryImages(ctx)
	e.recognizeHistoryImages(ctx)

	if n := atomic.LoadInt32(calls); n != 1 {
		t.Errorf("planner calls = %d, want 1", n)
	}
	block := e.history[0].Content[1]
	if !block.Recognized {
		t.Error("block.Recognized = false after failure, want true")
	}
	if !strings.Contains(block.RecognitionText, "[image recognition failed:") {
		t.Errorf("RecognitionText = %q, want failure marker", block.RecognitionText)
	}
}

// TestApplyImageRecognition_StepWriteBack verifies the actor-side write-back:
// the persisted step backing the history message is mutated in place and
// marked dirty for the next snapshot; unknown IDs and out-of-range block
// indexes are safe no-ops.
func TestApplyImageRecognition_StepWriteBack(t *testing.T) {
	a := &Actor{}
	a.appendStep(domain.Step{
		ID: "user-turn-1",
		Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: "hi"},
			{Type: domain.ContentBlockImage, ImageURL: "data:image/png;base64,aGk="},
		},
	})
	a.appendStep(domain.Step{ID: "step-2", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "answer"}}})

	a.applyImageRecognition("user-turn-1", 1, true, "a blue circle")

	block := a.steps[0].Content[1]
	if !block.Recognized || block.RecognitionText != "a blue circle" {
		t.Errorf("step block not written back: %+v", block)
	}
	if !a.msgDirty[0] {
		t.Error("msgDirty[0] = false, want true (snapshot must re-convert)")
	}
	if a.msgDirty[1] {
		t.Error("msgDirty[1] = true, want false (untouched step)")
	}

	a.applyImageRecognition("no-such-step", 0, true, "x")          // unknown ID: no-op, no panic
	a.applyImageRecognition("user-turn-1", 99, true, "out-of-range") // bad index: no mutation
	if a.steps[0].Content[1].RecognitionText != "a blue circle" {
		t.Errorf("out-of-range call mutated the block: %+v", a.steps[0].Content[1])
	}
}

// TestRecognizeHistoryImages_AlreadyRecognizedStillReplacesWire covers the
// rebuilt-engine path: blocks restored from persisted steps are already
// Recognized, so no aggregator call is made — but the dispatch wire must
// still replace them with text for a text-only unit.
func TestRecognizeHistoryImages_AlreadyRecognizedStillReplacesWire(t *testing.T) {
	e, ctx, calls, _ := newRecognizeTestEngine(t, "deepseek-chat", nil)
	e.history[0].Content[1].Recognized = true
	e.history[0].Content[1].RecognitionText = "cached description"

	e.recognizeHistoryImages(ctx)

	if n := atomic.LoadInt32(calls); n != 0 {
		t.Errorf("planner calls = %d, want 0 for already-recognized blocks", n)
	}
	if !e.dispatchImagesAsText {
		t.Error("dispatchImagesAsText = false, want true (wire replacement must stay engaged)")
	}
}

// TestBuildDispatchHistory_TextOnlyReplacesRecognizedImages verifies the wire
// transform: recognized image blocks become text blocks on the dispatch copy,
// unrecognized image blocks and tool messages pass through untouched, and the
// engine history itself is never mutated.
func TestBuildDispatchHistory_TextOnlyReplacesRecognizedImages(t *testing.T) {
	e := &turnEngine{
		logger:              newNopActorLogger(),
		dispatchImagesAsText: true,
		history: []domain.ChatMessage{
			{
				ID:   "m-user",
				Role: domain.ChatRoleUser,
				Content: []domain.ContentBlock{
					{Type: domain.ContentBlockImage, ImageURL: "data:image/png;base64,rec=", Recognized: true, RecognitionText: "a chart trending up"},
					{Type: domain.ContentBlockImage, ImageURL: "data:image/png;base64,unrec="},
					{Type: domain.ContentBlockText, Text: "hello"},
				},
			},
			{
				ID:   "m-asst",
				Role: domain.ChatRoleAssistant,
				Content: []domain.ContentBlock{
					{Type: domain.ContentBlockToolUse, ToolName: "screenshot", ToolUseID: "tc1"},
				},
			},
			{
				ID:   "m-tool",
				Role: "tool",
				Content: []domain.ContentBlock{
					{Type: domain.ContentBlockToolResult, ToolUseID: "tc1", Text: "result"},
				},
			},
		},
	}

	got := e.buildDispatchHistory(nil, 0)

	if len(got) != 3 {
		t.Fatalf("dispatch history length = %d, want 3 (sanitize must keep the pair): %+v", len(got), got)
	}
	user := got[0]
	if user.Content[0].Type != domain.ContentBlockText || user.Content[0].Text != "[image recognized]\na chart trending up\n[image ref: msg:m-user:0]" {
		// The msg:<id> handle lets the text-only primary re-recognize this
		// image with a task-specific prompt via recognize_image.
		t.Errorf("recognized image block not replaced: %+v", user.Content[0])
	}
	if user.Content[1].Type != domain.ContentBlockImage || user.Content[1].ImageURL != "data:image/png;base64,unrec=" {
		t.Errorf("unrecognized image block must pass through untouched: %+v", user.Content[1])
	}
	if user.Content[2].Text != "hello" {
		t.Errorf("text block changed: %+v", user.Content[2])
	}
	if got[2].Content[0].Type != domain.ContentBlockToolResult || got[2].Content[0].Text != "result" {
		t.Errorf("tool message changed: %+v", got[2].Content[0])
	}
	// Engine history must stay unmutated: both image blocks still image blocks.
	if e.history[0].Content[0].Type != domain.ContentBlockImage || e.history[0].Content[1].Type != domain.ContentBlockImage {
		t.Error("buildDispatchHistory mutated engine history")
	}
}

// TestBuildDispatchHistory_ImageBudget: a computeruse loop taking many shots
// in one turn must not ship every data URL on every dispatch — only the last
// dispatchImageKeepLast images ride as blocks; older ones degrade to the
// placeholder with their msg ref (vision primaries included).
func TestBuildDispatchHistory_ImageBudget(t *testing.T) {
	img := func(i int) domain.ContentBlock {
		return domain.ContentBlock{Type: domain.ContentBlockImage, ImageURL: fmt.Sprintf("data:image/png;base64,shot%d=", i)}
	}
	history := []domain.ChatMessage{
		{ID: "m0", Role: domain.ChatRoleUser, Content: []domain.ContentBlock{img(0)}},
		{ID: "m1", Role: domain.ChatRoleUser, Content: []domain.ContentBlock{img(1)}},
		{ID: "m2", Role: domain.ChatRoleUser, Content: []domain.ContentBlock{img(2)}},
		{ID: "m3", Role: domain.ChatRoleUser, Content: []domain.ContentBlock{img(3)}},
	}
	e := &turnEngine{logger: newNopActorLogger(), history: history}

	got := e.buildDispatchHistory(nil, 0)

	blockCount := 0
	for i, m := range got {
		for _, b := range m.Content {
			if b.Type == domain.ContentBlockImage {
				blockCount++
			}
			if b.Type == domain.ContentBlockText && strings.HasPrefix(b.Text, "[image omitted to bound request size]") {
				if !strings.Contains(b.Text, "[image ref: msg:m"+fmt.Sprint(i)+":0]") {
					t.Errorf("demoted image missing its msg ref: %q", b.Text)
				}
			}
		}
	}
	if blockCount != dispatchImageKeepLast {
		t.Fatalf("dispatch carried %d image blocks, want %d (keep-last budget)", blockCount, dispatchImageKeepLast)
	}
	// The newest images survive.
	last := got[len(got)-1].Content[0]
	secondLast := got[len(got)-2].Content[0]
	if last.Type != domain.ContentBlockImage || last.ImageURL != "data:image/png;base64,shot3=" ||
		secondLast.Type != domain.ContentBlockImage || secondLast.ImageURL != "data:image/png;base64,shot2=" {
		t.Fatalf("keep-last images wrong: %+v / %+v", secondLast, last)
	}
}
