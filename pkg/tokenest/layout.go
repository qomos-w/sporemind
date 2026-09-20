package tokenest

import (
	"sort"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// SegmentKind identifies a category of context tokens.
type SegmentKind string

const (
	SegmentCold    SegmentKind = "cold"
	SegmentHot     SegmentKind = "hot"
	SegmentSummary SegmentKind = "summary"
	SegmentMessage SegmentKind = "message"
)

// Segment is a single token-counted region in a context layout.
// StepIndex is the source step array index for message segments; -1 for
// aggregated segments (cold, hot, summary).
type Segment struct {
	Kind      SegmentKind `json:"kind"`
	Tokens    int         `json:"tokens"`
	StepIndex int         `json:"stepIndex"`
}

// EstimateSegments returns a layout of token segments from the given context
// components. It is a pure function — callers (agent or turn) prepare the
// inputs from their respective state sources.
func EstimateSegments(coldTexts []string, toolNames []string, toolSchemas []string, summaryTexts []string, hotTexts []string, steps []domain.Step, coveredEnd int32, cal *Calibration) []Segment {
	var layout []Segment

	// Cold: instructions + tool schemas
	coldTokens := 0
	for _, s := range coldTexts {
		coldTokens += cal.EstimateTokens(s)
	}
	for i := range toolNames {
		coldTokens += cal.EstimateTokens(toolNames[i])
		if i < len(toolSchemas) {
			coldTokens += cal.EstimateTokens(toolSchemas[i])
		}
	}
	if coldTokens > 0 {
		layout = append(layout, Segment{Kind: SegmentCold, Tokens: coldTokens, StepIndex: -1})
	}

	// Summary segments
	summaryTokens := 0
	for _, s := range summaryTexts {
		summaryTokens += cal.EstimateTokens(s)
	}
	if summaryTokens > 0 {
		layout = append(layout, Segment{Kind: SegmentSummary, Tokens: summaryTokens, StepIndex: -1})
	}

	// Hot context
	hotTokens := 0
	for _, s := range hotTexts {
		hotTokens += cal.EstimateTokens(s)
	}
	if hotTokens > 0 {
		layout = append(layout, Segment{Kind: SegmentHot, Tokens: hotTokens, StepIndex: -1})
	}

	// Steps: emit one segment per non-covered step so the compaction UI can
	// highlight exactly which steps will be compressed. Covered assistant steps
	// are omitted; user steps are always emitted because they are never
	// replaced by summaries. Discarded steps are hidden from layout entirely.
	for i, step := range steps {
		if step.Discarded {
			continue
		}
		if step.Role != "user" {
			if coveredEnd >= 0 && int32(i) <= coveredEnd {
				continue
			}
		}
		stepTokens := 0
		for _, cb := range step.Content {
			// UI-only blocks (e.g. compaction frames) are not sent to the LLM,
			// so they must not contribute to the context budget.
			if cb.Type == "compaction" {
				continue
			}
			if cb.Text != "" {
				stepTokens += cal.EstimateTokens(cb.Text)
			}
			if cb.Input != "" {
				stepTokens += cal.EstimateTokens(cb.Input)
			}
		}
		if stepTokens > 0 {
			layout = append(layout, Segment{Kind: SegmentMessage, Tokens: stepTokens, StepIndex: i})
		}
	}

	return layout
}

// TotalTokens sums tokens across a layout.
func TotalTokens(layout []Segment) int {
	total := 0
	for _, seg := range layout {
		total += seg.Tokens
	}
	return total
}

// LastCoveredIndex returns the highest SourceEndIndex among all summary
// segments, or -1 if no segments exist. It does NOT verify that coverage is
// contiguous; callers that need the end of the contiguous covered prefix
// should use LastContiguousCoveredIndex.
func LastCoveredIndex(segments []domain.SummarySegment) int32 {
	if len(segments) == 0 {
		return -1
	}
	var max int32 = -1
	for _, seg := range segments {
		if seg.SourceEndIndex > max {
			max = seg.SourceEndIndex
		}
	}
	return max
}

// LastContiguousCoveredIndex returns the highest step array index that is
// covered by a contiguous prefix of non-empty summary segments. Gaps or empty
// segments stop the walk, so the returned value is the safe boundary for
// deciding where the next compaction should start.
func LastContiguousCoveredIndex(segments []domain.SummarySegment) int32 {
	var nonEmpty []domain.SummarySegment
	for _, seg := range segments {
		if seg.Text != "" {
			nonEmpty = append(nonEmpty, seg)
		}
	}
	if len(nonEmpty) == 0 {
		return -1
	}
	sort.Slice(nonEmpty, func(i, j int) bool {
		return nonEmpty[i].SourceStartIndex < nonEmpty[j].SourceStartIndex
	})
	end := nonEmpty[0].SourceEndIndex
	for i := 1; i < len(nonEmpty); i++ {
		seg := nonEmpty[i]
		if seg.SourceStartIndex > end+1 {
			break
		}
		if seg.SourceEndIndex > end {
			end = seg.SourceEndIndex
		}
	}
	return end
}

// MsgArrayIndex returns the array position of the message whose Idx equals
// targetIdx, or -1 if not found.
func MsgArrayIndex(msgs []domain.ChatMessage, targetIdx int32) int {
	for i, msg := range msgs {
		if msg.Idx == targetIdx {
			return i
		}
	}
	return -1
}

// defaultModelWindow is the conservative fallback used when no provider-config
// value is available. It is deliberately generic — model-specific context
// windows must come from the provider config (provider.Models[].MaxContextLength),
// not from a hardcoded lookup table.
const defaultModelWindow = 128000

// ModelContextWindow returns a conservative context window size for a model
// name. This is an emergency fallback used only when the aggregator cannot
// provide MaxContextLength (i.e., the provider config has a 0/missing value).
// The authoritative source is always the provider config; callers that can
// resolve the aggregator value should never need to call this.
func ModelContextWindow(model string) int {
	return defaultModelWindow
}
