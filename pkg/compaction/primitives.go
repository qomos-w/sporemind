package compaction

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/tokenest"
)

const TruncationSuffix = "... (truncated)"

// DefaultToolResultMaxBytes is the default maximum byte length for tool_result
// text included in compaction input. It is a policy-level default; callers may
// override by truncating before passing the content block.
const DefaultToolResultMaxBytes = 2000

// Compaction timeout scaling constants. The summary deadline scales with the
// input token count so small chunks get a tight deadline while large inputs
// get proportionally more time.
const (
	CompactionTimeoutBase      = 1 * time.Minute
	CompactionTimeoutPer10KTok = 30 * time.Second
	CompactionTimeoutMax       = 5 * time.Minute
)

// TimeoutForTokens returns a summary deadline that scales with the input token
// count: base + (tokens / 10_000) minutes, clamped to [base, max]. This gives
// small chunks a tight deadline while large compaction inputs (100K+ tokens)
// get up to the max.
func TimeoutForTokens(inputTokens int) time.Duration {
	if inputTokens <= 0 {
		return CompactionTimeoutBase
	}
	d := CompactionTimeoutBase + time.Duration(inputTokens/10000)*CompactionTimeoutPer10KTok
	if d > CompactionTimeoutMax {
		d = CompactionTimeoutMax
	}
	if d < CompactionTimeoutBase {
		d = CompactionTimeoutBase
	}
	return d
}

// TruncateSafe truncates s to at most maxLen bytes without breaking a
// multi-byte UTF-8 character. If truncation occurs, suffix is appended.
func TruncateSafe(s string, maxLen int, suffix string) string {
	if len(s) <= maxLen {
		return s
	}
	cut := maxLen - len(suffix)
	if cut <= 0 {
		return suffix
	}
	for cut > 0 && cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// ResolveTokenBudget returns the effective token budget from a policy,
// resolving BudgetMode="percentage" against the given context window size.
// Falls back to 75% of windowSize if policy is nil or TokenBudget is 0.
func ResolveTokenBudget(policy *domain.CompactionPolicy, windowSize int32) int32 {
	if policy == nil || policy.TokenBudget <= 0 {
		return windowSize * 65 / 100
	}
	if policy.BudgetMode == "percentage" {
		return windowSize * policy.TokenBudget / 100
	}
	return policy.TokenBudget
}

// EstimateSessionTokens estimates total tokens for the effective session:
// summary segments + messages whose Idx > coveredEnd (i.e. not yet
// replaced by a summary). When coveredEnd < 0 all messages are counted.
func EstimateSessionTokens(messages []domain.ChatMessage, segments []domain.SummarySegment, coveredEnd int32) int32 {
	total := 0
	for _, msg := range messages {
		if coveredEnd >= 0 && msg.Idx <= coveredEnd && msg.Role != "user" {
			continue
		}
		for _, cb := range msg.Content {
			if cb.Text != "" {
				total += tokenest.EstimateTokens(cb.Text)
			}
			if cb.Input != "" {
				total += tokenest.EstimateTokens(cb.Input)
			}
		}
	}
	for _, seg := range segments {
		if seg.Text != "" {
			total += tokenest.EstimateTokens(seg.Text)
		}
	}
	return int32(total)
}

// SplitIndex calculates the index at which to split messages for compaction.
// Returns 0 if all messages fit within recentWindow.
func SplitIndex(msgs []domain.ChatMessage, recentWindow int32) int {
	if len(msgs) == 0 || recentWindow <= 0 {
		return 0
	}
	idx := len(msgs) - int(recentWindow)
	if idx <= 0 {
		return 0
	}
	return idx
}

// formatContentBlockForCompaction appends a compact, human-readable form of
// one content block to the input builder. It replaces image/attachment blocks
// with a placeholder and truncates tool results to DefaultToolResultMaxBytes.
func formatContentBlockForCompaction(b *strings.Builder, role string, cb domain.ContentBlock) {
	switch cb.Type {
	case "compaction":
		return
	case "tool_use":
		b.WriteString(role + ": [tool_use:" + cb.ToolName + "] " + cb.Input + "\n")
	case "tool_result":
		text := TruncateSafe(cb.Text, DefaultToolResultMaxBytes, "... [truncated]")
		b.WriteString(role + ": [tool_result:" + cb.ToolName + "] " + text + "\n")
	case "image_url", "image":
		b.WriteString(role + ": [image attachment omitted]\n")
	default:
		if cb.Text != "" {
			b.WriteString(role + ": " + cb.Text + "\n")
		}
	}
}

// BuildCompactInput builds the summarization input text from messages in
// [startArrayIdx, splitIdx) that are not yet covered by summary segments.
// Returns the input text and the step array index range, or ("", 0, 0) if
// there is nothing to compact.
func BuildCompactInput(messages []domain.ChatMessage, segments []domain.SummarySegment, splitIdx int) (inputText string, sourceStart, sourceEnd int32) {
	lastIdx := tokenest.LastContiguousCoveredIndex(segments)
	startArrayIdx := 0
	if lastIdx >= 0 {
		startArrayIdx = int(lastIdx) + 1
	}
	if startArrayIdx >= splitIdx {
		return "", 0, 0
	}

	var b strings.Builder
	hasContent := false
	for _, msg := range messages[startArrayIdx:splitIdx] {
		if !messageHasCompactableContent(msg) {
			continue
		}
		hasContent = true
		for _, cb := range msg.Content {
			formatContentBlockForCompaction(&b, msg.Role, cb)
		}
	}
	if !hasContent {
		return "", 0, 0
	}
	return b.String(), int32(startArrayIdx), int32(splitIdx - 1)
}

func messageHasCompactableContent(msg domain.ChatMessage) bool {
	for _, cb := range msg.Content {
		if cb.Type == "compaction" {
			continue
		}
		if cb.Text != "" || cb.Input != "" || cb.Type == domain.ContentBlockToolUse || cb.Type == domain.ContentBlockToolResult {
			return true
		}
	}
	return false
}

// BuildCompactInputSteps is the step-based variant of BuildCompactInput.
func BuildCompactInputSteps(steps []domain.Step, segments []domain.SummarySegment, splitIdx int) (inputText string, sourceStart, sourceEnd int32) {
	lastIdx := tokenest.LastContiguousCoveredIndex(segments)
	startArrayIdx := 0
	if lastIdx >= 0 {
		startArrayIdx = int(lastIdx) + 1
	}
	if startArrayIdx >= splitIdx {
		return "", 0, 0
	}

	var b strings.Builder
	hasContent := false
	for _, step := range steps[startArrayIdx:splitIdx] {
		if !stepHasCompactableContent(step) {
			continue
		}
		hasContent = true
		for _, cb := range step.Content {
			formatContentBlockForCompaction(&b, step.Role, cb)
		}
	}
	if !hasContent {
		return "", 0, 0
	}
	return b.String(), int32(startArrayIdx), int32(splitIdx - 1)
}

func stepHasCompactableContent(step domain.Step) bool {
	for _, cb := range step.Content {
		if cb.Type == "compaction" {
			continue
		}
		if cb.Text != "" || cb.Input != "" || cb.Type == domain.ContentBlockToolUse || cb.Type == domain.ContentBlockToolResult {
			return true
		}
	}
	return false
}

// SummarizeCompact appends a Level-1 SummarySegment covering the given step
// array index range. Callers must first obtain summaryText via LLM summarization.
// Empty summaries are ignored so they do not create false coverage.
func TruncateSummaryTokens(text string, maxTokens int) string {
	if maxTokens <= 0 || tokenest.EstimateTokens(text) <= maxTokens {
		return text
	}
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if tokenest.EstimateTokens(string(runes[:mid])) <= maxTokens {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[:low])
}

func SummarizeCompact(segments *[]domain.SummarySegment, sourceStart, sourceEnd int32, summaryText string) {
	if strings.TrimSpace(summaryText) == "" {
		return
	}
	*segments = append(*segments, domain.SummarySegment{
		SourceStartIndex: sourceStart,
		SourceEndIndex:   sourceEnd,
		Level:            1,
		Text:             summaryText,
		CreatedAt:        time.Now().Format(time.RFC3339),
	})
}

// SegmentsAtLevel returns the number of summary segments at the given level.
func SegmentsAtLevel(segments []domain.SummarySegment, level int32) int {
	n := 0
	for _, seg := range segments {
		if seg.Level == level && seg.Text != "" {
			n++
		}
	}
	return n
}

// BuildSegmentsInput builds input text for the first contiguous group of
// Level-N segments containing at least two segments.
func BuildSegmentsInput(segments []domain.SummarySegment, level int32) (inputText string, minStart, maxEnd int32, ok bool) {
	group := contiguousSegmentGroup(segments, level)
	if len(group) < 2 {
		return "", 0, 0, false
	}
	var b strings.Builder
	for i, seg := range group {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		_, _ = fmt.Fprintf(&b, "<!-- segment level=%d source=[%d,%d] -->\n", seg.seg.Level, seg.seg.SourceStartIndex, seg.seg.SourceEndIndex)
		b.WriteString(seg.seg.Text)
	}
	return b.String(), group[0].seg.SourceStartIndex, group[len(group)-1].seg.SourceEndIndex, true
}

type summaryGroupEntry struct {
	index int
	seg   domain.SummarySegment
}

func contiguousSegmentGroup(segments []domain.SummarySegment, level int32) []summaryGroupEntry {
	group := make([]summaryGroupEntry, 0)
	for i, seg := range segments {
		if seg.Level == level && strings.TrimSpace(seg.Text) != "" {
			group = append(group, summaryGroupEntry{index: i, seg: seg})
		}
	}
	sort.SliceStable(group, func(i, j int) bool {
		return group[i].seg.SourceStartIndex < group[j].seg.SourceStartIndex
	})
	if len(group) < 2 {
		return nil
	}
	bestStart := 0
	bestEnd := 1
	start := 0
	for i := 1; i < len(group); i++ {
		if group[i].seg.SourceStartIndex != group[i-1].seg.SourceEndIndex+1 {
			if i-start > bestEnd-bestStart {
				bestStart, bestEnd = start, i
			}
			start = i
		}
	}
	if len(group)-start > bestEnd-bestStart {
		bestStart, bestEnd = start, len(group)
	}
	if bestEnd-bestStart < 2 {
		return nil
	}
	return group[bestStart:bestEnd]
}

// SummarizeSegments replaces one contiguous group of Level-N summary
// segments with a single Level-(N+1) segment.
func SummarizeSegments(segments *[]domain.SummarySegment, level int32, summaryText string) {
	group := contiguousSegmentGroup(*segments, level)
	if len(group) < 2 || strings.TrimSpace(summaryText) == "" {
		return
	}
	minStart := group[0].seg.SourceStartIndex
	maxEnd := group[len(group)-1].seg.SourceEndIndex

	selected := make(map[int]struct{}, len(group))
	for _, entry := range group {
		selected[entry.index] = struct{}{}
	}
	out := make([]domain.SummarySegment, 0, len(*segments)-len(group)+1)
	for i, seg := range *segments {
		if _, ok := selected[i]; ok {
			continue
		}
		out = append(out, seg)
	}
	out = append(out, domain.SummarySegment{
		SourceStartIndex: minStart,
		SourceEndIndex:   maxEnd,
		Level:            level + 1,
		Text:             summaryText,
		CreatedAt:        time.Now().Format(time.RFC3339),
	})
	*segments = out
}

// CollapseCompact drops all messages except the last minWindow and appends
// a SummarySegment covering the dropped range.
func CollapseCompact(messages []domain.ChatMessage, segments *[]domain.SummarySegment, minWindow int32) error {
	if minWindow <= 0 || len(messages) <= int(minWindow) {
		return nil
	}

	lastIdx := tokenest.LastCoveredIndex(*segments)
	startArrayIdx := 0
	if lastIdx >= 0 {
		if pos := tokenest.MsgArrayIndex(messages, lastIdx); pos >= 0 {
			startArrayIdx = pos + 1
		}
	}
	splitIdx := len(messages) - int(minWindow)
	if startArrayIdx >= splitIdx {
		return nil
	}

	var parts []string
	for _, msg := range messages[startArrayIdx:splitIdx] {
		for _, cb := range msg.Content {
			if cb.Text != "" {
				line := msg.Role + ": " + cb.Text
				line = TruncateSafe(line, 100, "...")
				parts = append(parts, line)
			}
		}
	}

	summaryText := "[Collapsed context]\n" + strings.Join(parts, "\n")
	summaryText = TruncateSafe(summaryText, 2000, "\n"+TruncationSuffix)

	*segments = append(*segments, domain.SummarySegment{
		SourceStartIndex: messages[startArrayIdx].Idx,
		SourceEndIndex:   messages[splitIdx-1].Idx,
		Level:            1,
		Text:             summaryText,
		CreatedAt:        time.Now().Format(time.RFC3339),
	})
	return nil
}

// EstimateMessagesTokens returns the estimated token count for a slice of
// messages, summing all Text and Input fields in their content blocks.
func EstimateMessagesTokens(msgs []domain.ChatMessage) int {
	total := 0
	for _, msg := range msgs {
		for _, cb := range msg.Content {
			if cb.Text != "" {
				total += tokenest.EstimateTokens(cb.Text)
			}
			if cb.Input != "" {
				total += tokenest.EstimateTokens(cb.Input)
			}
		}
	}
	return total
}

// TruncateMessageByTokens proportionally truncates a message's content blocks
// so the total estimated token count does not exceed maxTokens. Each text
// field is reduced proportionally and appended with a truncation suffix.
func TruncateMessageByTokens(msg *domain.ChatMessage, maxTokens int) {
	if maxTokens <= 0 || len(msg.Content) == 0 {
		return
	}
	totalTokens := EstimateMessagesTokens([]domain.ChatMessage{*msg})
	if totalTokens <= maxTokens {
		return
	}

	headroom := tokenest.EstimateTokens(TruncationSuffix) + 10
	target := maxTokens - headroom
	if target <= 0 {
		target = maxTokens / 2
	}
	ratio := float64(target) / float64(totalTokens)
	if ratio > 1 {
		ratio = 1
	}
	if ratio < 0.05 {
		ratio = 0.05
	}

	for i := range msg.Content {
		cb := &msg.Content[i]
		if cb.Text != "" {
			maxLen := int(float64(len(cb.Text)) * ratio)
			if maxLen < 30 {
				maxLen = 30
			}
			cb.Text = TruncateSafe(cb.Text, maxLen, TruncationSuffix)
		}
		if cb.Input != "" {
			maxLen := int(float64(len(cb.Input)) * ratio)
			if maxLen < 30 {
				maxLen = 30
			}
			cb.Input = TruncateSafe(cb.Input, maxLen, TruncationSuffix)
		}
	}
}

// TruncateStepByTokens is the step-based variant of TruncateMessageByTokens.
func TruncateStepByTokens(step *domain.Step, maxTokens int) {
	if maxTokens <= 0 || len(step.Content) == 0 {
		return
	}
	totalTokens := 0
	for _, cb := range step.Content {
		if cb.Text != "" {
			totalTokens += tokenest.EstimateTokens(cb.Text)
		}
		if cb.Input != "" {
			totalTokens += tokenest.EstimateTokens(cb.Input)
		}
	}
	if totalTokens <= maxTokens {
		return
	}

	headroom := tokenest.EstimateTokens(TruncationSuffix) + 10
	target := maxTokens - headroom
	if target <= 0 {
		target = maxTokens / 2
	}
	ratio := float64(target) / float64(totalTokens)
	if ratio > 1 {
		ratio = 1
	}
	if ratio < 0.05 {
		ratio = 0.05
	}

	for i := range step.Content {
		cb := &step.Content[i]
		if cb.Text != "" {
			maxLen := int(float64(len(cb.Text)) * ratio)
			if maxLen < 30 {
				maxLen = 30
			}
			cb.Text = TruncateSafe(cb.Text, maxLen, TruncationSuffix)
		}
		if cb.Input != "" {
			maxLen := int(float64(len(cb.Input)) * ratio)
			if maxLen < 30 {
				maxLen = 30
			}
			cb.Input = TruncateSafe(cb.Input, maxLen, TruncationSuffix)
		}
	}
}

// IsPromptTooLongError checks whether an error indicates the LLM prompt
// exceeded the model's context window.
func IsPromptTooLongError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "prompt is too long") ||
		strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "maximum context length") ||
		strings.Contains(msg, "exceeds limit") ||
		strings.Contains(msg, "total message size") ||
		strings.Contains(msg, "request too large") ||
		strings.Contains(msg, "too many tokens") ||
		strings.Contains(msg, "exceeded model token limit")
}

// EstimateJSONBodySize returns a rough estimate of the JSON-encoded request
// body size in bytes for a slice of ChatMessages. It accounts for JSON
// escaping and structural overhead (object keys, braces, quotes).
func EstimateJSONBodySize(msgs []domain.ChatMessage) int {
	size := 0
	for _, msg := range msgs {
		// Per-message object overhead: {"role":"...","content":[...],...}
		size += 120
		size += len(msg.Role) * 2
		for _, cb := range msg.Content {
			// Per-block object overhead: {"type":"...","text":"...",...}
			size += 100
			if cb.Text != "" {
				// JSON escaping roughly adds 25% for typical text.
				size += len(cb.Text) * 5 / 4
			}
			if cb.Input != "" {
				size += len(cb.Input) * 5 / 4
			}
			if cb.ToolUseID != "" {
				size += len(cb.ToolUseID) * 2
			}
			if cb.ToolName != "" {
				size += len(cb.ToolName) * 2
			}
		}
	}
	return size
}
