package tokenest

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestLastContiguousCoveredIndex_Empty(t *testing.T) {
	if got := LastContiguousCoveredIndex(nil); got != -1 {
		t.Fatalf("expected -1 for empty segments, got %d", got)
	}
}

func TestLastContiguousCoveredIndex_Single(t *testing.T) {
	segs := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 9, Text: "summary"},
	}
	if got := LastContiguousCoveredIndex(segs); got != 9 {
		t.Fatalf("expected 9, got %d", got)
	}
}

func TestLastContiguousCoveredIndex_Contiguous(t *testing.T) {
	segs := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Text: "a"},
		{SourceStartIndex: 5, SourceEndIndex: 9, Text: "b"},
	}
	if got := LastContiguousCoveredIndex(segs); got != 9 {
		t.Fatalf("expected 9, got %d", got)
	}
}

func TestLastContiguousCoveredIndex_SkipsGap(t *testing.T) {
	// Regression: a gap between summaries must stop the contiguous walk so
	// compaction resumes right after the last contiguous segment instead of
	// skipping the gap.
	segs := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 99, Text: "first"},
		{SourceStartIndex: 200, SourceEndIndex: 299, Text: "second"},
	}
	if got := LastContiguousCoveredIndex(segs); got != 99 {
		t.Fatalf("expected gap to stop at 99, got %d", got)
	}
}

func TestLastContiguousCoveredIndex_SkipsEmptySegments(t *testing.T) {
	segs := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 49, Text: "first"},
		{SourceStartIndex: 50, SourceEndIndex: 99, Text: ""},
		{SourceStartIndex: 100, SourceEndIndex: 149, Text: "third"},
	}
	if got := LastContiguousCoveredIndex(segs); got != 49 {
		t.Fatalf("expected empty segment to break coverage at 49, got %d", got)
	}
}

func TestLastContiguousCoveredIndex_Unordered(t *testing.T) {
	segs := []domain.SummarySegment{
		{SourceStartIndex: 50, SourceEndIndex: 99, Text: "b"},
		{SourceStartIndex: 0, SourceEndIndex: 49, Text: "a"},
	}
	if got := LastContiguousCoveredIndex(segs); got != 99 {
		t.Fatalf("expected 99 after sorting, got %d", got)
	}
}

func TestLastCoveredIndex_ReturnsMaxEnd(t *testing.T) {
	segs := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 99, Text: "first"},
		{SourceStartIndex: 200, SourceEndIndex: 299, Text: "second"},
	}
	if got := LastCoveredIndex(segs); got != 299 {
		t.Fatalf("expected max end 299, got %d", got)
	}
}

func TestEstimateSegments_SkipsCompactionBlocks(t *testing.T) {
	cal := &Calibration{}
	steps := []domain.Step{
		{Role: "user", Content: []domain.ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []domain.ContentBlock{
			{Type: "text", Text: "world"},
			{Type: "compaction", Text: strings.Repeat("x", 10000)},
		}},
	}
	layout := EstimateSegments(nil, nil, nil, nil, nil, steps, -1, cal)
	total := TotalTokens(layout)
	// Only hello+world contribute; the 10000-char compaction frame is excluded
	// from the context budget, so the total must equal their tiktoken counts.
	want := EstimateTokens("hello") + EstimateTokens("world")
	if total != want {
		t.Fatalf("expected %d tokens (hello+world, compaction skipped), got %d", want, total)
	}
}

func TestModelContextWindow_AlwaysReturnsGenericDefault(t *testing.T) {
	// ModelContextWindow must NOT do model-specific lookups — it returns the
	// same conservative default for every model. Model-specific context windows
	// come exclusively from the provider config.
	for _, model := range []string{"", "claude-sonnet-4-6", "gpt-5.4", "kimi-for-coding", "deepseek-v4-pro", "totally-unknown"} {
		if got := ModelContextWindow(model); got != defaultModelWindow {
			t.Errorf("ModelContextWindow(%q) = %d, want generic default %d", model, got, defaultModelWindow)
		}
	}
	if defaultModelWindow >= 200000 {
		t.Errorf("defaultModelWindow %d must be < 200000 to avoid masking non-200k models", defaultModelWindow)
	}
}

func TestEstimateSegments_SkipsCoveredCompactionBlocks(t *testing.T) {
	cal := &Calibration{}
	steps := []domain.Step{
		{Role: "assistant", Content: []domain.ContentBlock{
			{Type: "compaction", Text: strings.Repeat("x", 10000)},
		}},
		{Role: "user", Content: []domain.ContentBlock{{Type: "text", Text: "hello"}}},
	}
	// The compaction frame at index 0 is covered; the user step at index 1 is
	// always emitted. No tokens should leak from the frame regardless of coverage.
	layout := EstimateSegments(nil, nil, nil, nil, nil, steps, 0, cal)
	total := TotalTokens(layout)
	want := EstimateTokens("hello")
	if total != want {
		t.Fatalf("expected %d tokens (hello only), got %d", want, total)
	}
}
