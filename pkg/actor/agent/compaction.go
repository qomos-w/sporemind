package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/compaction"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/tokenest"
	"sort"
	"time"
)

// resolveCompactionPolicy returns the effective compaction policy for this agent.
// 3-tier priority: instance override > Kind config > global hardcoded default.
func (a *Actor) resolveCompactionPolicy(kindCfg domain.AgentKindConfig) *domain.CompactionPolicy {
	if a.cfg.CompactionOverride != nil {
		return a.cfg.CompactionOverride
	}
	if kindCfg.CompactionPolicy != nil {
		cp := kindCfg.CompactionPolicy
		var summaryUnit *domain.ModelUnit
		if cp.SummaryUnit != nil {
			u := domain.ModelUnit(*cp.SummaryUnit)
			summaryUnit = &u
		}
		return &domain.CompactionPolicy{
			Enabled:          cp.Enabled,
			BudgetMode:       cp.BudgetMode,
			Strategy:         cp.Strategy,
			TriggerKind:      cp.TriggerKind,
			TokenBudget:      cp.TokenBudget,
			RecentWindow:     cp.RecentWindow,
			SummaryUnit:      summaryUnit,
			MaxSummaryTokens: cp.MaxSummaryTokens,
		}
	}
	d := compaction.DefaultCompactionPolicy
	return &d
}

// lastCompactedIdx returns the highest step array index covered by a
// contiguous prefix of non-empty SummarySegments, or -1 if none exist.
func (a *Actor) lastCompactedIdx() int32 {
	return tokenest.LastContiguousCoveredIndex(a.RawSession.SummarySegments)
}

// defaultStoragePolicy returns the fallback storage policy when the kind
// config does not provide one.
func defaultStoragePolicy() domain.StoragePolicy {
	return domain.StoragePolicy{
		Enabled:          true,
		MaxSessionChars:  500000,
		DiscardBatchSize: 1,
	}
}

// resolveStoragePolicy returns the effective storage policy for this agent.
// Priority: kind config > hardcoded default.
func (a *Actor) resolveStoragePolicy(kindCfg domain.AgentKindConfig) domain.StoragePolicy {
	if kindCfg.StoragePolicy != nil && kindCfg.StoragePolicy.Enabled {
		return *kindCfg.StoragePolicy
	}
	return defaultStoragePolicy()
}

// handleStorageConfigure handles agent.storage.configure callable.
// Read-only: returns the effective storage policy resolved from kind config.
func (a *Actor) handleStorageConfigure(ctx actor.Context, req domain.AgentStorageConfigureReq) (domain.AgentStorageConfigureResp, error) {
	kindCfg := a.fetchAgentKindConfig(ctx)
	policy := a.resolveStoragePolicy(kindCfg)

	resp := domain.AgentStorageConfigureResp{
		StoragePolicy: policy,
		Source:        "kind",
	}
	if kindCfg.StoragePolicy == nil {
		resp.Source = "default"
	}
	return resp, nil
}

// isCompactionOnlyStep reports whether a step exists solely to carry a
// compaction frame. These steps must not participate in protected-tail
// computation because they are not part of the logical assistant/tool turn.
func isCompactionOnlyStep(step domain.Step) bool {
	if step.Type != "text" {
		return false
	}
	for _, b := range step.Content {
		if b.Type == "compaction" {
			return true
		}
	}
	return false
}

// computeProtectedTail returns the number of trailing steps that must never be
// compacted because they form the most recent logical turn: the latest assistant
// step plus any immediately following tool_result steps. Concurrent tool call
// batches are naturally covered because their tool_result steps appear right
// after the assistant(tool_use) step and are kept in original order.
func computeProtectedTail(steps []domain.Step) int {
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Role != "assistant" || isCompactionOnlyStep(steps[i]) {
			continue
		}
		tail := 1 // the assistant step itself
		for j := i + 1; j < len(steps); j++ {
			if steps[j].Role == "tool" {
				tail++
				continue
			}
			break
		}
		return tail
	}
	return 0
}

// compiledSummary returns the active summary text, or empty string if none.
func (a *Actor) compiledSummary() string {
	if len(a.RawSession.SummarySegments) == 0 {
		return ""
	}
	var parts []string
	for _, seg := range a.RawSession.SummarySegments {
		parts = append(parts, seg.Text)
	}
	return joinNonEmpty(parts, "\n\n")
}

func joinNonEmpty(parts []string, sep string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return ""
	}
	result := out[0]
	for i := 1; i < len(out); i++ {
		result += sep + out[i]
	}
	return result
}

// handleCompactionConfigure handles agent.compaction.configure callable.
// Allows runtime adjustment of compaction policy and summary reset.
// nil CompactionPolicy = read-only query; non-nil = write override.
func (a *Actor) handleCompactionConfigure(ctx actor.Context, req domain.AgentCompactionConfigureReq) (domain.AgentCompactionConfigureResp, error) {
	if req.ResetSummary {
		a.RawSession.SummarySegments = nil
	}

	isWrite := req.CompactionPolicy != nil
	if isWrite {
		a.cfg.CompactionOverride = req.CompactionPolicy
		a.saveMailbox(ctx)
	}

	cfg := a.fetchAgentKindConfig(ctx)
	policy := a.resolveCompactionPolicy(cfg)

	resp := domain.AgentCompactionConfigureResp{
		CompactionPolicy: domain.CompactionPolicy{
			Enabled:          policy.Enabled,
			BudgetMode:       policy.BudgetMode,
			Strategy:         policy.Strategy,
			TriggerKind:      policy.TriggerKind,
			TokenBudget:      policy.TokenBudget,
			RecentWindow:     policy.RecentWindow,
			SummaryUnit:      policy.SummaryUnit,
			MaxSummaryTokens: policy.MaxSummaryTokens,
		},
		SummarySegments: make([]domain.SummarySegment, len(a.RawSession.SummarySegments)),
	}
	copy(resp.SummarySegments, a.RawSession.SummarySegments)

	layout := a.estimateLayout(ctx)
	resp.EstimatedTokens = int32(totalLayoutTokens(layout))

	if a.cfg.CompactionOverride != nil && a.cfg.CompactionOverride.Enabled {
		resp.Source = "instance"
	} else {
		resp.Source = "kind"
	}
	return resp, nil
}

// summarizeViaPlanImpl calls aiaggregator.summarize via a Plan node with context
// cancellation support, avoiding the Call().Await() deadlock risk.

func summarizeViaPlanImpl(ctx actor.Context, aggRef ref.Ref, session string, unit domain.ModelUnit, inputText string, systemPrompt string, timeout time.Duration) (string, error) {
	summarizeReq := domain.SendSessionMessageReq{
		SessionID: session,
		Unit:      &unit,
		Title:     "compaction",
		System:    systemPrompt,
		Messages: []domain.ChatMessage{
			{Role: "user", Content: []domain.ContentBlock{
				{Type: "text", Text: inputText},
			}},
		},
	}

	node, err := ctx.Planner().Plan(aggRef, "aiaggregator.summarize", summarizeReq)
	if err != nil {
		return "", fmt.Errorf("summarizer plan: %w", err)
	}
	if err := node.Start(ctx.Lifecycle()); err != nil {
		return "", fmt.Errorf("summarizer start: %w", err)
	}
	defer func() {
		_ = node.Stop()
		_ = ctx.Destroy(node.Ref())
	}()

	timeoutCtx, cancel := context.WithTimeout(ctx.Lifecycle(), timeout)
	defer cancel()

	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)
	panicprobe.SafeGo(ctx, "summarize_recv", func() {
		v, err := node.Recv()
		if err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{text: extractSummaryResult(v)}
	})

	select {
	case r := <-ch:
		if r.err != nil {
			return "", fmt.Errorf("summarizer: %w", r.err)
		}
		return r.text, nil
	case <-timeoutCtx.Done():
		// The summarizer may have produced a result concurrently with the
		// deadline firing. ch is buffered (cap 1), so the result is never lost
		// to the sender — but a symmetric select here picks the timeout branch
		// ~50% of the time at the boundary, reporting a spurious timeout for a
		// summary that actually completed. Prefer an already-arrived result
		// before declaring a timeout.
		select {
		case r := <-ch:
			if r.err != nil {
				return "", fmt.Errorf("summarizer: %w", r.err)
			}
			return r.text, nil
		default:
			return "", fmt.Errorf("summarizer timeout: %w", timeoutCtx.Err())
		}
	}
}

func extractSummaryResult(v any) string {
	switch v := v.(type) {
	case domain.SummarizeResp:
		return v.Text
	case string:
		return v
	case []byte:
		return string(v)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// ── Compaction frame constants (matched by aigen.ContextSegment.Kind) ──

const (
	kindCold    = "cold"
	kindHot     = "hot"
	kindSummary = "summary"
	kindMessage = "message"
)

// estimateLayout builds the ordered segment list from current session state.
// Internally delegates to tokenest.EstimateSegments for the actual counting.
func (a *Actor) estimateLayout(ctx actor.Context) []gen.ContextSegment {
	cfg := a.fetchAgentKindConfig(ctx)
	inst := a.resolveInstructions(ctx)

	// Cold texts: instructions
	var coldTexts []string
	if inst != nil {
		coldTexts = append(coldTexts, inst.Base...)
		coldTexts = append(coldTexts, inst.Resolved...)
	}

	// Tool names and schemas
	tools := a.resolveTools(ctx, cfg, a.callablesMap())
	toolNames := make([]string, len(tools))
	toolSchemas := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name
		toolSchemas[i] = t.InputSchema
	}

	// Summary texts
	var summaryTexts []string
	for _, seg := range a.RawSession.SummarySegments {
		summaryTexts = append(summaryTexts, seg.Text)
	}

	// Hot context texts
	var hotTexts []string
	for _, b := range a.resolveFullHotContext(ctx) {
		hotTexts = append(hotTexts, b.Text)
	}

	coveredEnd := tokenest.LastContiguousCoveredIndex(a.RawSession.SummarySegments)

	segments := tokenest.EstimateSegments(coldTexts, toolNames, toolSchemas, summaryTexts, hotTexts, a.steps, coveredEnd, a.cfg.TokenCalibration)

	// Convert tokenest.Segment -> aigen.ContextSegment for agent-local use.
	layout := make([]gen.ContextSegment, 0, len(segments))
	for _, seg := range segments {
		layout = append(layout, gen.ContextSegment{
			Kind:      string(seg.Kind),
			Tokens:    int32(seg.Tokens),
			StepIndex: int32(seg.StepIndex),
		})
	}
	return layout
}

// probeActualTokens sends a MaxTokens=1 request to the current model via
// aiaggregator.probe_tokens and returns the accurate InputTokens reported by
// the provider. It builds a SendSessionMessageReq from the agent's current
// state (instructions + hot context + tools + compiled messages), matching the
// exact serialization that dispatch uses. Returns 0 and a non-nil error on
// failure; callers should keep the previous value on error.
func (a *Actor) probeActualTokens(ctx actor.Context, turnID string) (int64, error) {
	return probeActualTokensFn(a, ctx, turnID)
}

const (
	maxCompactionRounds         = 5
	recentWindowProtectedRounds = 3
	maxCompactionChunkHardCap   = 100
)

// compactionTimeoutForTokens delegates to the shared compaction package so
// both agent and aggregator use the same scaling logic.
func compactionTimeoutForTokens(inputTokens int) time.Duration {
	return compaction.TimeoutForTokens(inputTokens)
}

// computeSplitIdx returns a split index that keeps at least 'window' recent
// steps while never going below hardFloor. hardFloor is interpreted as the
// number of trailing steps that must be protected (e.g. the latest logical turn).
// It only considers uncovered steps (above the current coveredEnd). Returns -1
// if no uncovered steps exist in the allowed range.
func (a *Actor) computeSplitIdx(window int32, hardFloor int) int {
	if len(a.steps) == 0 {
		return -1
	}
	coveredEnd := tokenest.LastContiguousCoveredIndex(a.RawSession.SummarySegments)
	keep := int(window)
	if keep < hardFloor {
		keep = hardFloor
	}
	idx := len(a.steps) - keep
	if idx <= int(coveredEnd)+1 {
		return -1
	}
	if idx < 0 || idx >= len(a.steps) {
		return -1
	}
	return idx
}

// computeChunkSplitIdx returns a split index for one compaction round. The
// chunk size is max(uncovered*3/4, min(100, remaining uncovered)) so long
// histories are compressed by 75% when possible while still capping tiny
// histories at the hard ceiling. The returned split never exceeds the
// recent-window floor.
func (a *Actor) computeChunkSplitIdx(window int32, hardFloor int) int {
	split := a.computeSplitIdx(window, hardFloor)
	if split < 0 {
		return -1
	}
	coveredEnd := tokenest.LastContiguousCoveredIndex(a.RawSession.SummarySegments)
	firstUncovered := int(coveredEnd) + 1
	if firstUncovered < 0 {
		firstUncovered = 0
	}
	if firstUncovered >= split {
		return -1
	}
	remaining := split - firstUncovered
	chunk := remaining * 3 / 4
	cap := remaining
	if cap > maxCompactionChunkHardCap {
		cap = maxCompactionChunkHardCap
	}
	if chunk < cap {
		chunk = cap
	}
	split = firstUncovered + chunk
	if split <= firstUncovered {
		return -1
	}
	return split
}

// findLowestMergeableSummaryLevel returns the lowest summary level that has at
// least two segments and can therefore be merged upward. Returns 0 if none.
func (a *Actor) findLowestMergeableSummaryLevel() int32 {
	var maxLevel int32
	for _, seg := range a.RawSession.SummarySegments {
		if seg.Level > maxLevel {
			maxLevel = seg.Level
		}
	}
	for l := int32(1); l <= maxLevel; l++ {
		if compaction.SegmentsAtLevel(a.RawSession.SummarySegments, l) < 2 {
			continue
		}
		if _, _, _, ok := compaction.BuildSegmentsInput(a.RawSession.SummarySegments, l); ok {
			return l
		}
	}
	return 0
}

// emitCompactionFrame serializes a CompactionFrameData block and emits a
// block.appended step event.
func (a *Actor) emitCompactionFrame(ctx actor.Context, frame gen.CompactionFrameData, stepID, turnID string, stepContent *[]domain.ContentBlock) {
	frameJSON, _ := json.Marshal(frame)
	block := domain.ContentBlock{Type: "compaction", Text: string(frameJSON)}
	*stepContent = append(*stepContent, block)
	_ = a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "block.appended",
		StepID: stepID,
		TurnID: turnID,
		Block:  &block,
	})
}

// persistCompactionStep appends the compaction step to a.steps and emits step.closed.
func (a *Actor) persistCompactionStep(ctx actor.Context, stepID, turnID string, seq int64, content []domain.ContentBlock) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	compactionStep := domain.Step{
		ID:        stepID,
		Role:      "assistant",
		Type:      "text",
		TurnID:    turnID,
		Content:   content,
		Closed:    true,
		Timestamp: now,
		Seq:       seq,
	}
	a.appendStep(compactionStep)
	_ = a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.closed",
		StepID: compactionStep.ID,
		TurnID: turnID,
	})
}

// countCompactedSteps counts the non-user steps inside [start, end) that
// contributed to a Level-1 summary. This matches the filtering done by
// compaction.BuildCompactInputSteps.
func countCompactedSteps(steps []domain.Step, start, end int) int32 {
	if start < 0 {
		start = 0
	}
	if end > len(steps) {
		end = len(steps)
	}
	var count int32
	for i := start; i < end; i++ {
		if steps[i].Role == "user" {
			continue
		}
		hasContent := false
		for _, cb := range steps[i].Content {
			if cb.Type == domain.ContentBlockText && cb.Text != "" {
				hasContent = true
				break
			}
			if cb.Type == domain.ContentBlockToolUse || cb.Type == domain.ContentBlockToolResult {
				hasContent = true
				break
			}
		}
		if hasContent {
			count++
		}
	}
	return count
}

// countMergedSegments counts how many existing summary segments at the given
// level were merged into a higher-level summary.
func countMergedSegments(segments []domain.SummarySegment, level int32) int32 {
	var count int32
	for _, seg := range segments {
		if seg.Level == level && seg.Text != "" {
			count++
		}
	}
	return count
}

// compactStepsToSummary compacts uncovered steps [0, splitIdx) into a new
// Level-1 summary segment.
func (a *Actor) compactStepsToSummary(ctx actor.Context, aggRef ref.Ref, splitIdx int, sessionID string, summaryUnit domain.ModelUnit, maxSummaryTokens int, turnID string, roundSeq *int32, records *[]gen.CompactionRoundRecord) error {
	beforeLayout := a.estimateLayout(ctx)
	inputText, srcStart, srcEnd := compaction.BuildCompactInputSteps(a.steps, a.RawSession.SummarySegments, splitIdx)
	if inputText == "" {
		return nil
	}
	summaryText, err := summarizeViaPlan(ctx, aggRef, sessionID, summaryUnit, inputText, agentkit.CompactionL1SummaryPrompt, compactionTimeoutForTokens(tokenest.EstimateTokens(inputText)))
	if err != nil {
		return err
	}
	summaryText = compaction.TruncateSummaryTokens(summaryText, maxSummaryTokens)
	if strings.TrimSpace(summaryText) == "" {
		return fmt.Errorf("empty compaction summary")
	}
	compaction.SummarizeCompact(&a.RawSession.SummarySegments, srcStart, srcEnd, summaryText)
	(*roundSeq)++
	afterLayout := a.estimateLayout(ctx)
	*records = append(*records, gen.CompactionRoundRecord{
		Round:                 *roundSeq,
		BeforeLayout:          beforeLayout,
		AfterLayout:           afterLayout,
		CompactedRanges:       buildCompactedRanges(beforeLayout, afterLayout),
		SourceStartIndex:      srcStart,
		SourceEndIndex:        srcEnd,
		Level:                 1,
		CompactedMessageCount: countCompactedSteps(a.steps, int(srcStart), int(srcEnd)+1),
	})
	return nil
}

// mergeSummaryLevel merges summary segments at the given level into a higher
// level summary.
func (a *Actor) mergeSummaryLevel(ctx actor.Context, aggRef ref.Ref, targetLevel int32, sessionID string, summaryUnit domain.ModelUnit, maxSummaryTokens int, turnID string, roundSeq *int32, records *[]gen.CompactionRoundRecord) error {
	beforeLayout := a.estimateLayout(ctx)
	mergedCount := countMergedSegments(a.RawSession.SummarySegments, targetLevel)
	inputText, minStart, maxEnd, ok := compaction.BuildSegmentsInput(a.RawSession.SummarySegments, targetLevel)
	if !ok {
		return nil
	}
	summaryText, err := summarizeViaPlan(ctx, aggRef, sessionID, summaryUnit, inputText, agentkit.CompactionL2PlusSummaryPrompt, compactionTimeoutForTokens(tokenest.EstimateTokens(inputText)))
	if err != nil {
		return err
	}
	summaryText = compaction.TruncateSummaryTokens(summaryText, maxSummaryTokens)
	if strings.TrimSpace(summaryText) == "" {
		return fmt.Errorf("empty compaction summary")
	}
	compaction.SummarizeSegments(&a.RawSession.SummarySegments, targetLevel, summaryText)
	(*roundSeq)++
	afterLayout := a.estimateLayout(ctx)
	*records = append(*records, gen.CompactionRoundRecord{
		Round:                 *roundSeq,
		BeforeLayout:          beforeLayout,
		AfterLayout:           afterLayout,
		CompactedRanges:       buildCompactedRanges(beforeLayout, afterLayout),
		SourceStartIndex:      minStart,
		SourceEndIndex:        maxEnd,
		Level:                 targetLevel + 1,
		CompactedMessageCount: mergedCount,
	})
	return nil
}

// compactAsStep runs multi-round progressive compaction directly on the
// agent's RawSession and emits a compaction step. It does not create a
// separate turn; the compaction step is appended to the current turn's step
// stream.
// stepID may be empty (compactAsStep will allocate one) or a placeholder ID
// that the caller has already opened; when a placeholder is supplied the same
// step is reused so the UI sees a smooth transition.
// acquireCompactionLock takes the durable compaction bracket (harness-style
// start marker): the lock is persisted the moment it is taken, so a crash
// between start and end leaves a detectable orphan on disk instead of a
// half-finished state with no evidence. A live lock blocks every entry point
// (manual /compact, /recompact, auto) with a busy error.
func (a *Actor) acquireCompactionLock(ctx actor.Context, turnID, trigger string) error {
	if lock := a.RawSession.CompactionLock; lock != nil {
		return fmt.Errorf("compaction: already in progress (turn %s, trigger %s, started at %s)", lock.TurnID, lock.Trigger, lock.StartedAt)
	}
	a.RawSession.CompactionLock = &gen.CompactionLockState{
		TurnID:    turnID,
		Trigger:   trigger,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Seq:       a.allocSeq(),
	}
	// Durable start: persist before any work so mid-operation crashes leave
	// the bracket open on disk.
	a.saveMailbox(ctx)
	return nil
}

// releaseCompactionLock closes the bracket started by acquireCompactionLock.
// It runs on success AND failure — a failed attempt must also close its lock
// (the failure itself stays visible via the error frame, step and diagnostic),
// so only a crash can leave the lock open.
func (a *Actor) releaseCompactionLock(ctx actor.Context, turnID string) {
	lock := a.RawSession.CompactionLock
	if lock == nil || lock.TurnID != turnID {
		return
	}
	a.RawSession.CompactionLock = nil
	a.saveMailbox(ctx)
}

// emitCompactTurnStarted announces the compact/recompact turn's running state
// on the turn topic so the live frontend tracks it as in-progress while the
// compaction frames stream.
func (a *Actor) emitCompactTurnStarted(ctx actor.Context, turn domain.Turn) {
	payload := map[string]any{
		"state":     domain.TurnStateRunning,
		"revision":  turn.Revision,
		"turnOrder": turn.TurnOrder,
	}
	if turn.StartedAt != "" {
		payload["startedAt"] = turn.StartedAt
	}
	_ = a.emitTurnLifecycle(ctx, domain.TurnEvent{
		Kind:    domain.TurnLifecycleStarted,
		TurnID:  turn.ID,
		Payload: payload,
	})
}

// finalizeCompactTurn closes the lifecycle of a compact-triggered turn when its
// compaction ends: a nil err marks it completed, otherwise failed carrying the
// error. Turns without a Session.Turns record (auto-trigger compaction rides
// the engine's own turn) and already-terminal records are skipped. It uses the
// reducer-only lifecycle path — the compact turn is a user-role turn and must
// not mirror into a.status (the agent-level active-turn status).
func (a *Actor) finalizeCompactTurn(ctx actor.Context, turnID string, err error) {
	idx := -1
	for i := range a.Session.Turns {
		if a.Session.Turns[i].ID == turnID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	rec := domain.NormalizeTurnForLifecycle(a.Session.Turns[idx])
	if domain.IsTerminalTurnState(rec.State) {
		return
	}
	event := domain.TurnLifecycleEvent{
		TurnID:    turnID,
		TurnOrder: rec.TurnOrder,
		StartedAt: rec.StartedAt,
		Revision:  rec.Revision + 1,
	}
	if err != nil {
		event.Kind = domain.TurnLifecycleFailed
		event.State = domain.TurnStateFailed
		event.Error = err.Error()
	} else {
		event.Kind = domain.TurnLifecycleCompleted
		event.State = domain.TurnStateCompleted
	}
	updated, _, applyErr := a.applyTurnLifecycleEventToRecord(turnID, event)
	if applyErr != nil {
		ctx.Logger().Warn("agent: compact turn finalize rejected", "turn", turnID, "error", applyErr)
		return
	}
	payload := map[string]any{
		"state":     updated.State,
		"revision":  updated.Revision,
		"turnOrder": updated.TurnOrder,
	}
	if updated.StartedAt != "" {
		payload["startedAt"] = updated.StartedAt
	}
	if updated.CompletedAt != "" {
		payload["completedAt"] = updated.CompletedAt
	}
	if updated.Error != "" {
		payload["error"] = updated.Error
	}
	_ = a.emitTurnLifecycle(ctx, domain.TurnEvent{
		Kind:    event.Kind,
		TurnID:  turnID,
		Payload: payload,
	})
	a.takeSnapshot()
}

// closeInterruptedCompaction cleans up after a crash mid-compaction discovered
// at load: compaction-only steps the dead run left open are closed with an
// error frame (so the compaction UI does not stay "running" forever), and the
// owning user-role turn is finalized as failed. An auto-trigger lock names the
// engine's assistant turn, whose crash recovery is owned by recoverTurnStatus;
// only its compaction steps are closed here.
func (a *Actor) closeInterruptedCompaction(ctx actor.Context, turnID, trigger string) {
	const reason = "compaction interrupted by restart"
	layout := a.estimateLayout(ctx)
	for i := range a.steps {
		s := &a.steps[i]
		if s.TurnID != turnID || s.Closed || !isCompactionOnlyStep(*s) {
			continue
		}
		frame := gen.CompactionFrameData{
			Type:              "compaction",
			ID:                s.ID,
			Status:            "error",
			Trigger:           trigger,
			BeforeTokens:      int32(totalLayoutTokens(layout)),
			AfterTokens:       int32(totalLayoutTokens(layout)),
			ContextWindowSize: a.currentContextWindow(),
			BeforeLayout:      layout,
			AfterLayout:       layout,
			Rounds:            []gen.CompactionRoundData{},
			Error:             reason,
		}
		frameJSON, _ := json.Marshal(frame)
		block := domain.ContentBlock{Type: "compaction", Text: string(frameJSON)}
		s.Content = append(s.Content, block)
		_ = a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:   "block.appended",
			StepID: s.ID,
			TurnID: turnID,
			Block:  &block,
		})
		s.Error = reason
		s.Closed = true
		a.touchStep(i)
		_ = a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:   "step.closed",
			StepID: s.ID,
			TurnID: turnID,
			Error:  reason,
		})
	}
	for i := range a.Session.Turns {
		if t := a.Session.Turns[i]; t.ID == turnID && t.Role == "user" {
			a.finalizeCompactTurn(ctx, turnID, fmt.Errorf("%s", reason))
			return
		}
	}
}

func (a *Actor) compactAsStep(ctx actor.Context, turnID string, trigger string, stepID string) error {
	if err := a.acquireCompactionLock(ctx, turnID, trigger); err != nil {
		return err
	}
	defer a.releaseCompactionLock(ctx, turnID)

	callerOpened := stepID != ""
	cfg := a.fetchAgentKindConfig(ctx)
	policy := a.resolveCompactionPolicy(cfg)

	recentWindow := int32(20)
	if policy.RecentWindow > 0 {
		recentWindow = policy.RecentWindow
	}
	// Resolve the summary slot's dispatch target once. The resolved unit and
	// aggregator come from the same candidate, so they stay consistent: a
	// unit-kind candidate pins both, and an aggregator/auto candidate leaves
	// the unit empty for the aggregator's own strategy to select. Unresolvable
	// candidates are skipped by resolveTarget, matching dispatch fallback
	// semantics — a named aggregator not yet bound falls through to the next
	// candidate instead of silently dropping a configured model.
	summarySlot := a.effectiveSummarySlot(policy)
	summaryAggRef, summaryUnit, err := a.resolveTarget(ctx, summarySlot)
	if err != nil {
		// Fall back to the primary target when the summary slot resolves to
		// an auto/aggregator candidate that isn't bound yet.
		summaryAggRef, summaryUnit, err = a.resolveTarget(ctx, a.primary)
		if err != nil {
			return fmt.Errorf("compaction: no model target available: %w", err)
		}
	}
	// Re-resolve context window size from the aggregator so compaction uses
	// the current model's limits even if the unit changed mid-turn or the
	// provider config was updated since turn start.
	freshWindow := a.resolveUnitMaxContextLength(ctx, a.status.Unit)
	if freshWindow <= 0 {
		freshWindow = a.currentContextWindow()
	}
	windowSize := freshWindow
	budget := compaction.ResolveTokenBudget(policy, windowSize)

	// Sync the cached ContextBudget so all currentContextWindow() calls
	// within this compaction and the emitted frames use the fresh value.
	if a.status.ContextBudget != nil && a.status.ContextBudget.ContextWindowSize != windowSize {
		a.status.ContextBudget.ContextWindowSize = windowSize
		a.status.ContextBudget.TokenBudget = budget
		_ = ctx.EmitEvent("turn", domain.TurnEvent{
			Kind:   domain.TurnContextBudget,
			TurnID: turnID,
			ContextBudget: &domain.TurnContextBudgetPayload{
				EstimatedTokens:   a.status.ContextBudget.EstimatedTokens,
				ContextWindowSize: windowSize,
				TokenBudget:       budget,
			},
		})
	}

	sessionID := "compaction-" + ctx.Self().ID().String()

	// beforeTokens: use the actual probed token count for accuracy.
	// estimateLayout is retained for UI layout breakdown segments and, now that
	// it is tiktoken-backed, as a fallback when the probe fails (some providers
	// never report usage). The fallback keeps compaction able to decide and the
	// frame able to show a non-zero size; real probe values always win.
	beforeLayout := a.estimateLayout(ctx)
	beforeTokens, beforeProbeErr := a.probeActualTokens(ctx, turnID)
	if beforeProbeErr != nil {
		ctx.Logger().Warn("agent: compaction before-token probe failed", "turnID", turnID, "error", beforeProbeErr)
	}
	if beforeTokens <= 0 {
		if a.status.ContextBudget != nil && a.status.ContextBudget.EstimatedTokens > 0 {
			beforeTokens = int64(a.status.ContextBudget.EstimatedTokens)
		} else {
			beforeTokens = int64(totalLayoutTokens(beforeLayout))
		}
	}

	// resolveActualTokens returns the real probed token count, or — when the
	// probe fails — the tiktoken-backed layout estimate. The fallback keeps
	// compaction able to decide (belowBudgetGate) and the frame able to show a
	// non-zero size for providers that never report usage. Real probe values
	// always win when available.
	resolveActualTokens := func(layout []gen.ContextSegment) int64 {
		actual, _ := a.probeActualTokens(ctx, turnID)
		if actual > 0 {
			return actual
		}
		return int64(totalLayoutTokens(layout))
	}

	var compactionRoundRecords []gen.CompactionRoundRecord
	var roundSeq int32 = 0
	frameID := ctx.NewID().String()
	var stepContent []domain.ContentBlock

	if stepID == "" {
		// Number compaction steps consistently with the turn's other steps:
		// turn-1-compaction-001, turn-1-compaction-002, ...
		compactionSeq := 1
		for _, ev := range a.RawSession.CompactionEvents {
			if ev.TurnID == turnID {
				compactionSeq++
			}
		}
		stepID = fmt.Sprintf("%s-compaction-%03d", turnID, compactionSeq)
	}

	// Open the step before any LLM work so the frontend can render a preview.
	if !callerOpened {
		_ = a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:     "step.opened",
			StepID:   stepID,
			TurnID:   turnID,
			StepType: "text",
			Role:     "assistant",
		})
	}

	maxSummaryTokens := int(policy.MaxSummaryTokens)
	if maxSummaryTokens <= 0 {
		maxSummaryTokens = 4000
	}

	protectedTail := computeProtectedTail(a.steps)
	hardFloor := protectedTail

	// failWith emits an error frame, step.error, persists the step, reports a
	// diagnostic, and returns a non-nil error.
	failWith := func(reason string, err error) error {
		currentLayout := a.estimateLayout(ctx)
		afterProbed := resolveActualTokens(currentLayout)
		frame := gen.CompactionFrameData{
			Type:              "compaction",
			ID:                frameID,
			Status:            "error",
			Trigger:           trigger,
			BeforeTokens:      int32(beforeTokens),
			AfterTokens:       int32(afterProbed),
			ContextWindowSize: a.currentContextWindow(),
			BeforeLayout:      beforeLayout,
			AfterLayout:       currentLayout,
			Rounds:            buildRoundDataFromRecords(compactionRoundRecords),
			Unit:              &summaryUnit,
			Error:             reason,
		}
		a.emitCompactionFrame(ctx, frame, stepID, turnID, &stepContent)
		_ = a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:   "step.error",
			StepID: stepID,
			TurnID: turnID,
			Error:  reason,
		})
		a.persistCompactionStep(ctx, stepID, turnID, a.allocSeq(), stepContent)
		a.reportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
			Severity: "error",
			Source:   "compaction",
			Message:  reason,
			TurnID:   turnID,
			Unit:     &summaryUnit,
		})
		if err != nil {
			return err
		}
		return fmt.Errorf("compaction: %s", reason)
	}

	// pushRunningFrame emits the latest compaction progress. It is called after
	// each successful round so the frontend can show intermediate state and the
	// top-level context budget bar can shrink live.
	pushRunningFrame := func() {
		currentLayout := a.estimateLayout(ctx)
		actualTokens := resolveActualTokens(currentLayout)
		frame := gen.CompactionFrameData{
			Type:              "compaction",
			ID:                frameID,
			Status:            "running",
			Trigger:           trigger,
			BeforeTokens:      int32(beforeTokens),
			AfterTokens:       int32(actualTokens),
			ContextWindowSize: a.currentContextWindow(),
			BeforeLayout:      beforeLayout,
			AfterLayout:       currentLayout,
			Rounds:            buildRoundDataFromRecords(compactionRoundRecords),
			Unit:              &summaryUnit,
		}
		a.emitCompactionFrame(ctx, frame, stepID, turnID, &stepContent)
		if a.status.ContextBudget != nil && actualTokens > 0 {
			a.status.ContextBudget.EstimatedTokens = int32(actualTokens)
			_ = ctx.EmitEvent("turn", domain.TurnEvent{
				Kind:          domain.TurnContextBudget,
				TurnID:        turnID,
				ContextBudget: a.status.ContextBudget,
			})
		}
	}

	// Running preview frame.
	previewFrame := gen.CompactionFrameData{
		Type:              "compaction",
		ID:                frameID,
		Status:            "running",
		Trigger:           trigger,
		BeforeTokens:      int32(beforeTokens),
		AfterTokens:       int32(beforeTokens),
		ContextWindowSize: a.currentContextWindow(),
		BeforeLayout:      beforeLayout,
		AfterLayout:       beforeLayout,
		Rounds:            []gen.CompactionRoundData{},
		Unit:              &summaryUnit,
	}
	a.emitCompactionFrame(ctx, previewFrame, stepID, turnID, &stepContent)

	// Multi-round compaction.
	//
	// IMPORTANT: the budget-threshold gate runs AFTER each round has made
	// progress, never BEFORE. The original code probed at the loop top and
	// broke when `actualTokens < budget`, which combined with the
	// estimateLayout() fallback (4-chars/token heuristic, systematically low)
	// caused compaction to exit on round 1 WITHOUT compressing anything —
	// the user-visible "compaction runs but size is unchanged" bug.
	//
	// Now round 1 always executes; only after it (and each subsequent round
	// that actually compressed/merged something) do we re-probe and break
	// early if the context has fallen below budget.
	belowBudgetGate := func() bool {
		// Probe real tokens first. When the probe fails, fall back to the
		// tiktoken layout estimate so the gate can still decide — previously
		// a failed probe made belowBudgetGate return false forever, so
		// compaction either never stopped or never triggered on providers
		// that omit usage.
		actualTokens := resolveActualTokens(a.estimateLayout(ctx))
		return actualTokens < int64(budget)
	}
	for round := 1; round <= maxCompactionRounds; round++ {
		if round == 1 {
			// Round 1: compact one chunk of uncovered steps outside the recent window.
			splitIdx := a.computeChunkSplitIdx(recentWindow, hardFloor)
			if splitIdx > 0 {
				if err := a.compactStepsToSummary(ctx, summaryAggRef, splitIdx, sessionID, summaryUnit, maxSummaryTokens, turnID, &roundSeq, &compactionRoundRecords); err != nil {
					return failWith(fmt.Sprintf("round %d summarize failed: %s", round, err.Error()), err)
				}
				pushRunningFrame()
				if belowBudgetGate() {
					break
				}
			}
			continue
		}

		if round <= recentWindowProtectedRounds {
			// Rounds 2-3: only merge existing summaries, never touch recent steps.
			targetLevel := a.findLowestMergeableSummaryLevel()
			if targetLevel == 0 {
				// No summary merge possible right now; let rounds 4-5 try to
				// compress the recent window if we are still over budget.
				continue
			}
			if err := a.mergeSummaryLevel(ctx, summaryAggRef, targetLevel, sessionID, summaryUnit, maxSummaryTokens, turnID, &roundSeq, &compactionRoundRecords); err != nil {
				return failWith(fmt.Sprintf("round %d summary merge failed: %s", round, err.Error()), err)
			}
			pushRunningFrame()
			if belowBudgetGate() {
				break
			}
			continue
		}

		// Rounds 4-5: may break the recent window, but never cross hardFloor.
		targetLevel := a.findLowestMergeableSummaryLevel()
		if targetLevel > 0 {
			if err := a.mergeSummaryLevel(ctx, summaryAggRef, targetLevel, sessionID, summaryUnit, maxSummaryTokens, turnID, &roundSeq, &compactionRoundRecords); err != nil {
				return failWith(fmt.Sprintf("round %d summary merge failed: %s", round, err.Error()), err)
			}
			pushRunningFrame()
			if belowBudgetGate() {
				break
			}
			continue
		}

		splitIdx := a.computeChunkSplitIdx(0, hardFloor)
		if splitIdx >= 0 {
			if err := a.compactStepsToSummary(ctx, summaryAggRef, splitIdx, sessionID, summaryUnit, maxSummaryTokens, turnID, &roundSeq, &compactionRoundRecords); err != nil {
				return failWith(fmt.Sprintf("round %d summarize failed: %s", round, err.Error()), err)
			}
			pushRunningFrame()
			if belowBudgetGate() {
				break
			}
			continue
		}

		break
	}

	afterLayout := a.estimateLayout(ctx)
	afterTokens := resolveActualTokens(afterLayout)

	// No post-compaction budget gate: compaction has already executed as far as
	// the available material allowed (chunks outside recent window + summary
	// merges). If afterTokens still exceeds budget, the recent window itself
	// is too large relative to the budget — a configuration issue the user
	// must resolve (reduce RecentWindow or raise TokenBudget), not a failure
	// to report. The completed frame below carries the real before/after
	// numbers so the UI can show the actual reduction.

	// Completed frame. roundSeq may be zero when the context was already under
	// budget; that is a successful no-op, not an error.
	rounds := buildRoundDataFromRecords(compactionRoundRecords)
	frame := gen.CompactionFrameData{
		Type:              "compaction",
		ID:                frameID,
		Status:            "completed",
		Trigger:           trigger,
		BeforeTokens:      int32(beforeTokens),
		AfterTokens:       int32(afterTokens),
		ContextWindowSize: a.currentContextWindow(),
		BeforeLayout:      beforeLayout,
		AfterLayout:       afterLayout,
		Rounds:            rounds,
		Unit:              &summaryUnit,
	}
	a.emitCompactionFrame(ctx, frame, stepID, turnID, &stepContent)

	// Persist CompactionEvent.
	compactionSeq := a.allocSeq()
	ce := domain.CompactionEvent{
		TurnID:            turnID,
		Trigger:           trigger,
		BeforeTokens:      int32(beforeTokens),
		AfterTokens:       int32(afterTokens),
		Unit:              summaryUnit,
		Level:             roundSeq,
		Rounds:            compactionRoundRecords,
		CreatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		Seq:               compactionSeq,
		ContextWindowSize: windowSize,
	}
	a.RawSession.CompactionEvents = append(a.RawSession.CompactionEvents, ce)
	// Mirror the compaction event into the session-level request ledger
	// ("compaction" kind) sharing the same Seq numbering as LLM requests so
	// the sequence can be reconstructed without stitching timestamps.
	a.appendRequestRecord(domain.SessionRequestRecord{
		ID:          fmt.Sprintf("compaction-%s-%d", turnID, compactionSeq),
		Kind:        "compaction",
		Seq:         compactionSeq,
		StartedAt:   ce.CreatedAt,
		CompletedAt: ce.CreatedAt,
		TurnID:      turnID,
	})

	a.persistCompactionStep(ctx, stepID, turnID, compactionSeq, stepContent)

	// After a successful compaction the covered range is maximized, so this is
	// the safest point to discard summarized turns if storage limits are exceeded.
	a.maybeDiscardForStorage(ctx, cfg)

	// Compaction is the primary sleep window: the context was just compressed,
	// so memory consolidation (dreaming) is due if layer pressure is high.
	// IsSleepDue() still gates the spawn; turn completion remains a fallback.
	a.maybeStartMemorySleep(ctx, turnID, true)

	return nil
}

// buildCompactedRanges extracts compacted ranges from a before/after layout pair.
// With per-step message segments, a before segment is compacted if its step
// index no longer appears in the after layout. Summary segments are marked
// compacted when the summary region shrinks.
func buildCompactedRanges(beforeLayout, afterLayout []gen.ContextSegment) []gen.CompactedRange {
	beforeTokens := totalLayoutTokens(beforeLayout)
	afterTokens := totalLayoutTokens(afterLayout)
	if afterTokens >= beforeTokens {
		return nil
	}

	afterStepIndexes := make(map[int32]struct{})
	var afterSummaryTokens int32
	afterSummaryCount := 0
	for _, seg := range afterLayout {
		if seg.Kind == kindMessage && seg.StepIndex >= 0 {
			afterStepIndexes[seg.StepIndex] = struct{}{}
		}
		if seg.Kind == kindSummary {
			afterSummaryTokens += seg.Tokens
			afterSummaryCount++
		}
	}

	var beforeSummaryTokens int32
	beforeSummaryCount := 0
	var ranges []gen.CompactedRange
	for i, seg := range beforeLayout {
		if seg.Kind == kindMessage && seg.StepIndex >= 0 {
			if _, ok := afterStepIndexes[seg.StepIndex]; !ok {
				ranges = append(ranges, gen.CompactedRange{SegmentIndex: int32(i), Tokens: seg.Tokens})
			}
		}
		if seg.Kind == kindSummary {
			beforeSummaryTokens += seg.Tokens
			beforeSummaryCount++
		}
	}

	if beforeSummaryCount > afterSummaryCount || beforeSummaryTokens > afterSummaryTokens {
		for i, seg := range beforeLayout {
			if seg.Kind == kindSummary {
				ranges = append(ranges, gen.CompactedRange{SegmentIndex: int32(i), Tokens: seg.Tokens})
			}
		}
	}
	return ranges
}

// buildRoundDataFromRecords converts internal per-round records into the
// wire shape sent to the frontend. It preserves source ranges and level so
// the UI can label each summarize/merge step.
func buildRoundDataFromRecords(records []gen.CompactionRoundRecord) []gen.CompactionRoundData {
	out := make([]gen.CompactionRoundData, 0, len(records))
	for _, r := range records {
		out = append(out, gen.CompactionRoundData{
			CompactedRanges:       r.CompactedRanges,
			AfterLayout:           r.AfterLayout,
			SourceStartIndex:      r.SourceStartIndex,
			SourceEndIndex:        r.SourceEndIndex,
			Level:                 r.Level,
			Round:                 r.Round,
			CompactedMessageCount: r.CompactedMessageCount,
		})
	}
	return out
}

// buildPreviewRanges marks the message segments whose step index falls inside
// the compaction window [coveredEnd+1, splitIdx-1]. With per-step layout each
// segment maps to exactly one step, so the UI can highlight individual steps.
func buildPreviewRanges(layout []gen.ContextSegment, coveredEnd int32, splitIdx int) []gen.CompactedRange {
	var ranges []gen.CompactedRange
	for i, seg := range layout {
		if seg.Kind != kindMessage {
			continue
		}
		if seg.StepIndex < 0 {
			continue
		}
		if coveredEnd >= 0 && seg.StepIndex <= coveredEnd {
			continue
		}
		if int(seg.StepIndex) < splitIdx {
			ranges = append(ranges, gen.CompactedRange{SegmentIndex: int32(i), Tokens: seg.Tokens})
		}
	}
	return ranges
}

// totalLayoutTokens sums tokens in an agent-local layout.
func totalLayoutTokens(layout []gen.ContextSegment) int {
	total := 0
	for _, seg := range layout {
		total += int(seg.Tokens)
	}
	return total
}

// effectiveSummarySlot returns the summary ModelSlot with an explicit policy
// override prepended as the candidate-chain head. The override is represented
// as a unit-kind candidate so it resolves to the system aggregator serving
// that specific model. When no override is set, the configured summary slot is
// returned unchanged (an empty slot yields [auto] via effectiveCandidates).
func (a *Actor) effectiveSummarySlot(policy *domain.CompactionPolicy) domain.ModelSlot {
	if policy == nil || policy.SummaryUnit == nil || policy.SummaryUnit.Model == "" {
		return a.summary
	}
	override := domain.ModelRef{Kind: modelRefKindUnit, Unit: policy.SummaryUnit}
	return domain.ModelSlot{Candidates: append([]domain.ModelRef{override}, a.summary.Candidates...)}
}

// mergeSummarySegments merges incoming compaction results into the existing
// summary segments, replacing overlapping ranges and keeping non-overlapping
// entries sorted by SourceStartIndex.
func mergeSummarySegments(existing, incoming []domain.SummarySegment) []domain.SummarySegment {
	filtered := make([]domain.SummarySegment, 0, len(existing))
	for _, old := range existing {
		if old.Text == "" {
			continue
		}
		overlaps := false
		for _, inc := range incoming {
			if inc.Text == "" {
				continue
			}
			if old.SourceStartIndex >= inc.SourceStartIndex && old.SourceStartIndex <= inc.SourceEndIndex {
				overlaps = true
				break
			}
			if old.SourceEndIndex >= inc.SourceStartIndex && old.SourceEndIndex <= inc.SourceEndIndex {
				overlaps = true
				break
			}
		}
		if !overlaps {
			filtered = append(filtered, old)
		}
	}
	result := make([]domain.SummarySegment, 0, len(filtered)+len(incoming))
	result = append(result, filtered...)
	for _, inc := range incoming {
		if inc.Text != "" {
			result = append(result, inc)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].SourceStartIndex < result[j].SourceStartIndex
	})
	return result
}

// maybeDiscardForStorage discards summarized user+assistant turn pairs when the
// visible session char count exceeds the configured limit. Called after each
// successful compaction so the discardable range is maximized.
func (a *Actor) maybeDiscardForStorage(ctx actor.Context, kindCfg domain.AgentKindConfig) {
	policy := a.resolveStoragePolicy(kindCfg)
	if !policy.Enabled || policy.MaxSessionChars <= 0 {
		return
	}
	limit := int64(policy.MaxSessionChars)

	discarded := 0
	for a.estimateSessionChars() > limit {
		group := a.findEarliestDiscardableTurnGroup()
		if group == nil {
			break
		}
		if err := a.discardTurnGroup(ctx, group); err != nil {
			ctx.Logger().Error("agent: discard turn group failed", "error", err)
			break
		}
		discarded++
	}
	if discarded > 0 {
		a.saveMailbox(ctx)
	}
}

// estimateSessionChars returns the total visible char count of non-discarded
// steps. Compaction-only steps are excluded because they carry UI frames that
// are reconstructed from CompactionEvents and do not represent discardable
// content.
func (a *Actor) estimateSessionChars() int64 {
	var total int64
	for _, step := range a.steps {
		if step.Discarded || isCompactionOnlyStep(step) {
			continue
		}
		total += stepChars(step)
	}
	return total
}

func stepChars(step domain.Step) int64 {
	var n int64
	n += int64(len(step.ReasoningContent))
	for _, cb := range step.Content {
		n += int64(len(cb.Text))
		n += int64(len(cb.Input))
	}
	return n
}

// discardableTurnGroup identifies a contiguous prefix of turns (starting from
// turns[0]) that can be discarded together. The prefix always ends at an
// assistant turn so the remaining conversation starts with a user turn.
type discardableTurnGroup struct {
	turnIndices []int // indices into Session.Turns
	stepIndices []int // indices into a.steps
}

// findEarliestDiscardableTurnGroup finds the oldest contiguous prefix of turns
// whose steps are all within the summarized range. The prefix starts at turns[0]
// (must be user) and ends at the last assistant turn before the next user turn
// or before a turn whose steps are not fully summarized.
func (a *Actor) findEarliestDiscardableTurnGroup() *discardableTurnGroup {
	coveredEnd := a.lastCompactedIdx()
	if coveredEnd < 0 || len(a.Session.Turns) < 2 || len(a.steps) == 0 {
		return nil
	}

	turnStepIdx := make(map[string][]int)
	for i, step := range a.steps {
		if step.Discarded || isCompactionOnlyStep(step) {
			continue
		}
		turnStepIdx[step.TurnID] = append(turnStepIdx[step.TurnID], i)
	}

	turns := a.Session.Turns

	// The conversation must start with a user turn.
	if turns[0].Role != "user" {
		return nil
	}

	lastAssistantTurnIdx := -1
	for i := 0; i < len(turns); i++ {
		stepIndices := turnStepIdx[turns[i].ID]
		if len(stepIndices) == 0 {
			break
		}
		allSummarized := true
		for _, idx := range stepIndices {
			if idx > int(coveredEnd) {
				allSummarized = false
				break
			}
		}
		if !allSummarized {
			break
		}
		// A user turn after at least one assistant marks the start of the next
		// group — stop so the next group starts cleanly with user.
		if turns[i].Role == "user" && lastAssistantTurnIdx >= 0 {
			break
		}
		if turns[i].Role == "assistant" {
			lastAssistantTurnIdx = i
		}
	}

	if lastAssistantTurnIdx < 1 {
		return nil
	}

	var turnIndices []int
	var stepIndices []int
	for i := 0; i <= lastAssistantTurnIdx; i++ {
		turnIndices = append(turnIndices, i)
		stepIndices = append(stepIndices, turnStepIdx[turns[i].ID]...)
	}

	return &discardableTurnGroup{
		turnIndices: turnIndices,
		stepIndices: stepIndices,
	}
}

// discardTurnGroup marks all steps in the group as discarded, removes the turns
// from Session.Turns, clears step content to release memory, and rewrites the
// per-turn step files. Compaction-only steps belonging to the discarded turns
// are also cleared and their CompactionEvents removed so rebuildSteps doesn't
// reconstruct stale frames on restart.
func (a *Actor) discardTurnGroup(ctx actor.Context, group *discardableTurnGroup) error {
	discardedTurnIDs := make(map[string]struct{}, len(group.turnIndices))
	for _, ti := range group.turnIndices {
		discardedTurnIDs[a.Session.Turns[ti].ID] = struct{}{}
	}

	// Collect compaction-only steps belonging to discarded turns. These are
	// handled separately from real content steps because they must not be
	// written to JSONL (consistency with flushClosedSteps).
	var compactionStepIdx []int
	for i, step := range a.steps {
		if step.Discarded || !isCompactionOnlyStep(step) {
			continue
		}
		if _, ok := discardedTurnIDs[step.TurnID]; ok {
			compactionStepIdx = append(compactionStepIdx, i)
		}
	}

	// 1. Clear real content steps.
	for _, idx := range group.stepIndices {
		a.steps[idx].Discarded = true
		a.steps[idx].Content = nil
		a.steps[idx].ReasoningContent = ""
		a.touchStep(idx)
	}

	// 2. Remove turns and adjust ActiveHead.
	removedCount := group.turnIndices[len(group.turnIndices)-1] + 1
	a.Session.Turns = a.Session.Turns[removedCount:]

	newHead := a.Session.ActiveHead - int32(removedCount)
	if newHead < 0 {
		newHead = 0
	}
	a.Session.ActiveHead = newHead

	// 3. Remove CompactionEvents whose turn was discarded so rebuildSteps
	// doesn't reconstruct stale compaction frames on restart.
	if len(discardedTurnIDs) > 0 {
		var kept []domain.CompactionEvent
		for _, ev := range a.RawSession.CompactionEvents {
			if _, ok := discardedTurnIDs[ev.TurnID]; ok {
				continue
			}
			kept = append(kept, ev)
		}
		a.RawSession.CompactionEvents = kept
	}

	// 4. Emit step.closed events for real steps.
	for _, idx := range group.stepIndices {
		_ = a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:      "step.closed",
			StepID:    a.steps[idx].ID,
			TurnID:    a.steps[idx].TurnID,
			Discarded: true,
		})
	}

	// 5. Rewrite JSONL files. This must happen BEFORE clearing compaction
	// step content so rewriteStepFile can still identify and skip them
	// via isCompactionOnlyStep (compaction steps are never persisted to JSONL).
	if err := a.rewriteStepFilesForDiscardedTurns(group); err != nil {
		return err
	}

	// 6. Now clear compaction steps and emit their events.
	for _, idx := range compactionStepIdx {
		a.steps[idx].Discarded = true
		a.steps[idx].Content = nil
		a.steps[idx].ReasoningContent = ""
		a.touchStep(idx)
		_ = a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:      "step.closed",
			StepID:    a.steps[idx].ID,
			TurnID:    a.steps[idx].TurnID,
			Discarded: true,
		})
	}

	a.takeSnapshot()
	return nil
}
