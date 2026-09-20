package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"

	"github.com/qomos-w/sporemind/pkg/tokenest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const statusStepTextLimit = 4096

// buildTurnStatusReadOnly 是 turn 状态的无锁只读构建。
// 它把运行时的 status 快照、step 索引、steps 数组组装成前端可用的
// domain.TurnStatus。必须在 cell goroutine 上调用。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│  从 StepOrder 重建 Actions   │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│  从 steps slice 重建 Entries  │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│       组装 domain.Turn       │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│    返回 domain.TurnStatus    │
//	└──────────────────────────────┘

// buildTurnStatusReadOnly 是 turn 状态的无锁只读构建。
// 它把运行时的 status 快照、step 索引、steps 数组组装成前端可用的
// domain.TurnStatus。必须在 cell goroutine 上调用。
// activeRef 与 activeOrder 让 takeSnapshot 传入在 turnRefMu 下一次性读
// 取的成对快照，避免 owner-loop 并发写导致 ref/order 不一致。
func (a *Actor) buildTurnStatusReadOnly(activeRef string, activeOrder int64) domain.TurnStatus {
	turnID := a.status.TurnID
	if activeRef == "" {
		activeRef = a.getActiveTurnRef()
	}
	if activeOrder == 0 {
		activeOrder = a.getActiveTurnOrder()
	}
	if turnID == "" {
		turnID = activeRef
	}
	// Derive Seq/StartedAt/Timestamp from the active turn's first assistant
	// step so the frontend can sort the running envelope correctly. Without
	// this, turnStatusToEnvelope falls back to seq=undefined and timestamp=NOW,
	// which breaks ordering against concurrent user/assistant envelopes.
	var firstStepTs string
	var firstStepSeq int64
	for _, s := range a.steps[a.status.StartStepCount:] {
		if s.Role == "assistant" {
			firstStepTs = s.Timestamp
			firstStepSeq = s.Seq
			break
		}
	}
	turn := domain.Turn{
		ID:            turnID,
		Role:          "assistant",
		State:         a.status.State,
		Usage:         copyUsage(a.status.Usage),
		ContextBudget: copyContextBudget(a.status.ContextBudget),
		StartedAt:     firstStepTs,
		Timestamp:     firstStepTs,
		Seq:           firstStepSeq,
		TurnOrder:     activeOrder,
	}
	if len(a.RawSession.Tasks) > 0 {
		turn.Tasks = make([]gen.TurnTask, len(a.RawSession.Tasks))
		copy(turn.Tasks, a.RawSession.Tasks)
	}
	// EventSeq carries the active turn engine's step-event watermark so the
	// frontend reconcile can detect a stale active turn (background→foreground /
	// reconnect) and authoritatively replace its open steps from the summary.
	var activeStepEventSeq int32
	if eng := a.activeTurnEngine(); eng != nil {
		eng.stepEventMu.Lock()
		activeStepEventSeq = eng.stepEventSeq
		eng.stepEventMu.Unlock()
	}
	return domain.TurnStatus{
		Turn:         turn,
		Unit:         &a.status.Unit,
		StartedAt:    a.status.StartedAt,
		EventSeq:     activeStepEventSeq,
		PlanMode:     a.plan.Status == "pending_approval",
		PlanTask:     a.plan.Task,
		PlanApproval: a.buildPlanApprovalPayload(),
	}
}

// rebuildSteps 从 Session.Turns 重建 a.steps UI 单元数组。
// 用于 actor 启动时恢复状态，以及 undo/fork 后重新对齐 steps。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│      遍历 Session.Turns      │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│  追加 turn.Steps 到 a.steps  │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│  从 CompactionEvents 重建    │
//	│  压缩帧 step                 │
//	└──────────────────────────────┘
func (a *Actor) rebuildSteps() bool {
	modified := false
	// Load closed steps from per-turn JSONL files.
	fileSteps := a.loadStepFiles()

	// Build the combined step list. File steps carry all closed steps;
	// RawSession.Steps holds open steps for crash recovery. Deduplicate by
	// step ID, preferring the file version.
	a.steps = fileSteps
	if len(a.RawSession.Steps) > 0 {
		seen := make(map[string]struct{}, len(a.steps))
		for _, s := range a.steps {
			seen[s.ID] = struct{}{}
		}
		for _, s := range a.RawSession.Steps {
			if _, ok := seen[s.ID]; ok {
				continue
			}
			a.steps = append(a.steps, s)
			seen[s.ID] = struct{}{}
		}
	}
	// Always sort by Seq — loadStepFiles returns steps in directory listing
	// order (alphabetical by filename), not in Seq order.
	sort.SliceStable(a.steps, func(i, j int) bool {
		return a.steps[i].Seq < a.steps[j].Seq
	})

	// Mark persisted step IDs so flushClosedSteps doesn't rewrite them. Only
	// steps that came from the per-turn JSONL files (fileSteps) are actually on
	// disk; steps merged in from RawSession.Steps (e.g. freshly imported closed
	// steps during session clone) are NOT yet on disk and must stay unmarked so
	// flushClosedSteps writes them. Marking all of a.steps here would make the
	// clone silently lose its imported closed steps on the next restart.
	a.stepsPersistedMu.Lock()
	if a.stepsPersisted == nil {
		a.stepsPersisted = make(map[string]struct{})
	}
	for _, s := range fileSteps {
		if s.Closed {
			a.stepsPersisted[s.ID] = struct{}{}
		}
	}
	a.stepsPersistedMu.Unlock()

	// Compaction steps are ephemeral at runtime; only CompactionEvents are
	// persisted. Reconstruct a step for each event so the compaction frame
	// survives disconnect/reconnect.
	//
	// Group compaction events by turn so they can be inserted at the end of
	// the owning turn rather than appended to the end of the whole step array.
	// Without this, reconnect would move every compaction frame to the end of
	// the timeline.
	compactionEventsByTurn := make(map[string][]int)
	for i, ev := range a.RawSession.CompactionEvents {
		if ev.TurnID == "" {
			continue
		}
		compactionEventsByTurn[ev.TurnID] = append(compactionEventsByTurn[ev.TurnID], i)
	}

	for i := range a.Session.Turns {
		t := &a.Session.Turns[i]
		// Compaction happened inside this turn. Anchor its reconstructed step
		// to the turn's end timestamp so frontend timestamp sorting does not
		// float it past subsequent turns.
		turnTimestamp := t.CompletedAt
		if turnTimestamp == "" {
			turnTimestamp = t.Timestamp
		}
		if turnTimestamp == "" {
			// fall back to last step timestamp for this turn
			for k := len(a.steps) - 1; k >= 0; k-- {
				if a.steps[k].TurnID == t.ID {
					turnTimestamp = a.steps[k].Timestamp
					break
				}
			}
		}
		insertAt := len(a.steps)
		for k := len(a.steps) - 1; k >= 0; k-- {
			if a.steps[k].TurnID == t.ID {
				insertAt = k + 1
				break
			}
		}
		// Legacy CompactionEvents may have Seq=0 (the field was omitted before
		// the monotonic Seq counter was added). Without a valid Seq, the frontend
		// sorts the reconstructed compaction step to the very top of the timeline,
		// making load-more's beforeTurnId anchor on a recent turn and return
		// already-loaded turns. Assign each event a stable Seq after the turn's
		// last real step so it stays within the turn.
		evIdxs := compactionEventsByTurn[t.ID]
		if len(evIdxs) > 0 {
			var maxSeq int64
			for k := 0; k < len(a.steps); k++ {
				if a.steps[k].TurnID == t.ID && a.steps[k].Seq > maxSeq {
					maxSeq = a.steps[k].Seq
				}
			}
			for j, evIdx := range evIdxs {
				ev := &a.RawSession.CompactionEvents[evIdx]
				if ev.Seq == 0 {
					if maxSeq > 0 {
						ev.Seq = maxSeq + int64(j) + 1
					} else {
						ev.Seq = a.allocSeq()
					}
					if ev.Seq >= a.RawSession.NextSeq {
						a.RawSession.NextSeq = ev.Seq + 1
					}
					modified = true
				}
			}
		}
		var compactSteps []domain.Step
		for _, evIdx := range evIdxs {
			compactSteps = append(compactSteps, a.buildCompactionStep(evIdx, turnTimestamp))
		}
		a.steps = append(a.steps[:insertAt], append(compactSteps, a.steps[insertAt:]...)...)
		delete(compactionEventsByTurn, t.ID)
	}

	// Any remaining compaction events belong to turns not present in the
	// persisted session (e.g. an active turn not yet committed). Append them
	// at the end as a fallback so they are still rendered somewhere.
	var remainingTurnIDs []string
	for turnID := range compactionEventsByTurn {
		remainingTurnIDs = append(remainingTurnIDs, turnID)
	}
	sort.Strings(remainingTurnIDs)
	for _, turnID := range remainingTurnIDs {
		for _, evIdx := range compactionEventsByTurn[turnID] {
			ev := &a.RawSession.CompactionEvents[evIdx]
			if ev.Seq == 0 {
				ev.Seq = a.allocSeq()
				if ev.Seq >= a.RawSession.NextSeq {
					a.RawSession.NextSeq = ev.Seq + 1
				}
				modified = true
			}
			a.steps = append(a.steps, a.buildCompactionStep(evIdx, ""))
		}
	}
	// The steps slice was replaced/spliced wholesale above — rebuild the
	// message cache to restore the len(msgCache) == len(a.steps) invariant.
	a.rebuildMsgCache()
	return modified
}

// buildCompactionStep reconstructs a single synthetic compaction Step from a
// RawSession.CompactionEvents entry. The caller decides where to place it.
func (a *Actor) buildCompactionStep(evIdx int, turnTimestamp string) domain.Step {
	ev := a.RawSession.CompactionEvents[evIdx]
	stepID := fmt.Sprintf("compaction-%s-%d", ev.TurnID, evIdx)
	// Prefer the window size persisted on the event; fall back to the
	// tokenest lookup for events saved before ContextWindowSize existed.
	ctxWindow := ev.ContextWindowSize
	if ctxWindow <= 0 {
		ctxWindow = int32(tokenest.ModelContextWindow(ev.Unit.Model))
	}
	frame := gen.CompactionFrameData{
		Type:              "compaction",
		ID:                stepID,
		Status:            "completed",
		Trigger:           ev.Trigger,
		BeforeTokens:      ev.BeforeTokens,
		AfterTokens:       ev.AfterTokens,
		ContextWindowSize: ctxWindow,
		Unit:              &ev.Unit,
	}
	if len(ev.Rounds) > 0 {
		frame.BeforeLayout = ev.Rounds[0].BeforeLayout
		frame.AfterLayout = ev.Rounds[len(ev.Rounds)-1].AfterLayout
	}
	for _, r := range ev.Rounds {
		frame.Rounds = append(frame.Rounds, gen.CompactionRoundData{
			CompactedRanges:       r.CompactedRanges,
			AfterLayout:           r.AfterLayout,
			SourceStartIndex:      r.SourceStartIndex,
			SourceEndIndex:        r.SourceEndIndex,
			Level:                 r.Level,
			Round:                 r.Round,
			CompactedMessageCount: r.CompactedMessageCount,
		})
	}
	frameJSON, _ := json.Marshal(frame)
	ts := turnTimestamp
	if ts == "" {
		ts = ev.CreatedAt
	}
	return domain.Step{
		ID:        stepID,
		Role:      "system",
		Type:      "text",
		TurnID:    ev.TurnID,
		Closed:    true,
		Timestamp: ts,
		Seq:       ev.Seq,
		Meta:      "system",
		Content:   []domain.ContentBlock{{Type: "compaction", Text: string(frameJSON)}},
	}
}

// reconcilePersistedOrphans 扫描 Session.Turns，给每个 turn 内部就地补齐
// 缺失的 tool_result step。
//
// 场景：老存档因历史 bug 留下孤儿 tool_use（assistant step 含 tool_use
// 块但 turn 内没有对应 tool_result）。sanitizeMessages 在 dispatch 时
// 会剥掉孤儿避免 LLM 400，但存档本身不修——UI 上会永久显示一个"无结果"
// 的 tool_call step，且每次 dispatch 都得重剥一遍。
//
// 在 OnStart 跑一次，把存档层也补干净。新产生的孤儿仍由 turnEngine 的
// reconcileOrphanToolUses 在 run() 退出时处理。
func (a *Actor) reconcilePersistedOrphans() {
	// After flattening, steps are in a.steps (linked via TurnID). Collect
	// steps per turn for orphan detection.
	stepsByTurn := make(map[string][]domain.Step)
	for _, s := range a.steps {
		stepsByTurn[s.TurnID] = append(stepsByTurn[s.TurnID], s)
	}
	for ti := range a.Session.Turns {
		turn := &a.Session.Turns[ti]
		turnSteps := stepsByTurn[turn.ID]

		covered := make(map[string]struct{})
		for _, step := range turnSteps {
			for _, b := range step.Content {
				if b.Type == domain.ContentBlockToolResult && b.ToolUseID != "" {
					covered[b.ToolUseID] = struct{}{}
				}
			}
		}

		var orphans []string
		for _, step := range turnSteps {
			for _, b := range step.Content {
				if b.Type != domain.ContentBlockToolUse || b.ToolUseID == "" {
					continue
				}
				if _, ok := covered[b.ToolUseID]; ok {
					continue
				}
				orphans = append(orphans, b.ToolUseID)
			}
		}

		if len(orphans) == 0 {
			continue
		}

		for _, id := range orphans {
			a.appendStep(domain.Step{
				ID:        fmt.Sprintf("reconciled-%s-%s", turn.ID, id),
				Role:      "tool",
				Type:      "tool_result",
				TurnID:    turn.ID,
				Closed:    true,
				Timestamp: turn.Timestamp,
				Seq:       a.allocSeq(),
				Meta:      a.agentMeta(),
				Content: []domain.ContentBlock{{
					Type:      domain.ContentBlockToolResult,
					ToolUseID: id,
					Text:      "tool result unavailable: turn ended before this tool call resolved",
					IsError:   true,
				}},
			})
		}
	}
}

// allocSeq returns the next unique UI step sequence number and increments
// RawSession.NextSeq. Seq starts at 1; 0 means "unset" and is used to detect
// legacy data created before this field existed.
func (a *Actor) allocSeq() int64 {
	if a.RawSession.NextSeq == 0 {
		a.RawSession.NextSeq = 1
	}
	seq := a.RawSession.NextSeq
	a.RawSession.NextSeq++
	return seq
}

// nextTurnOrder returns the persisted monotonic turn identity without
// consuming it. Turn IDs must never derive from Session.Turns length because
// storage compaction removes retained prefixes and would make IDs collide with
// previously emitted turns after a restart or reload.
//
// Protected by turnRefMu because the owner loop (chat.submit) and the
// agent.exec loop (startTurnWithName) both read this value concurrently.
func (a *Actor) nextTurnOrder() int64 {
	a.turnRefMu.Lock()
	defer a.turnRefMu.Unlock()
	if a.RawSession.NextTurnOrder <= 0 {
		a.RawSession.NextTurnOrder = 1
	}
	return a.RawSession.NextTurnOrder
}

func (a *Actor) allocTurnOrder() int64 {
	a.turnRefMu.Lock()
	defer a.turnRefMu.Unlock()
	if a.RawSession.NextTurnOrder <= 0 {
		a.RawSession.NextTurnOrder = 1
	}
	order := a.RawSession.NextTurnOrder
	a.RawSession.NextTurnOrder++
	return order
}

func (a *Actor) normalizeSessionTurns() bool {
	if len(a.Session.Turns) == 0 {
		return false
	}
	before := make(map[string]int, len(a.Session.Turns))
	for i, t := range a.Session.Turns {
		before[t.ID] = i
	}
	ordered := turnsBySeq(a.Session.Turns)
	latest := make(map[string]domain.Turn, len(ordered))
	for _, t := range ordered {
		current, ok := latest[t.ID]
		if !ok || t.TurnOrder > current.TurnOrder || (t.TurnOrder == current.TurnOrder && t.State == "running") {
			latest[t.ID] = t
		}
	}
	filtered := make([]domain.Turn, 0, len(latest))
	for _, t := range latest {
		filtered = append(filtered, t)
	}
	filtered = turnsBySeq(filtered)
	changed := len(filtered) != len(a.Session.Turns)
	if len(filtered) == len(a.Session.Turns) {
		for i, t := range filtered {
			if before[t.ID] != i {
				changed = true
				break
			}
		}
	}
	if !changed {
		return false
	}
	a.Session.Turns = filtered
	if ref := a.getActiveTurnRef(); ref != "" {
		for i, t := range a.Session.Turns {
			if t.ID == ref {
				a.Session.ActiveHead = int32(i)
				return true
			}
		}
	}
	a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
	return true
}

func (a *Actor) migrateTurnOrders() bool {
	if len(a.Session.Turns) == 0 {
		if a.RawSession.NextTurnOrder <= 0 {
			a.RawSession.NextTurnOrder = 1
		}
		return false
	}
	changed := false
	maxOrder := a.RawSession.NextTurnOrder - 1
	if maxOrder < 0 {
		maxOrder = 0
	}
	lastOrder := int64(0)
	for i := range a.Session.Turns {
		order := a.Session.Turns[i].TurnOrder
		if order <= 0 {
			order = a.Session.Turns[i].Seq
		}
		if order <= lastOrder {
			order = lastOrder + 1
		}
		if order <= 0 {
			order = lastOrder + 1
		}
		if a.Session.Turns[i].TurnOrder != order {
			changed = true
			a.Session.Turns[i].TurnOrder = order
		}
		lastOrder = order
		if order > maxOrder {
			maxOrder = order
		}
	}
	a.RawSession.NextTurnOrder = maxOrder + 1
	if a.turnOrderByID == nil {
		a.turnOrderByID = make(map[string]int64, len(a.Session.Turns))
	}
	for _, t := range a.Session.Turns {
		a.turnOrderByID[t.ID] = t.TurnOrder
	}
	return changed
}

// initNextSeq scans persisted turns/compaction events and sets RawSession.NextSeq
// to max(existing Seq) + 1. No-op when NextSeq is already positive (fresh or
// previously initialized session).
func (a *Actor) initNextSeq() {
	if a.RawSession.NextSeq > 0 {
		return
	}
	var maxSeq int64
	for i := range a.Session.Turns {
		if a.Session.Turns[i].Seq > maxSeq {
			maxSeq = a.Session.Turns[i].Seq
		}
	}
	for _, s := range a.RawSession.Steps {
		if s.Seq > maxSeq {
			maxSeq = s.Seq
		}
	}
	for i := range a.RawSession.CompactionEvents {
		if a.RawSession.CompactionEvents[i].Seq > maxSeq {
			maxSeq = a.RawSession.CompactionEvents[i].Seq
		}
	}
	for _, s := range a.steps {
		if s.Seq > maxSeq {
			maxSeq = s.Seq
		}
	}
	if maxSeq > 0 {
		a.RawSession.NextSeq = maxSeq + 1
	} else {
		a.RawSession.NextSeq = 1
	}
}

// applyStepEvent 根据 StepEvent 更新 a.steps 数组。
// 这是 turnEngine 产生的流式/事件化输出落地到 UI 状态的唯一入口。
// 支持的事件类型：step.opened / block.appended / block.delta / step.closed / step.error / step.execution_progress。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│        switch ev.Kind        │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│      追加 step 或 block      │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│     累加文本到对应 block     │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│  更新执行进度（Progress）    │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│     标记 Closed 或 Error     │
//	└──────────────────────────────┘

// defaultStepMeta returns the step Meta to persist for the given event.
// If the event already carries a Meta value (e.g. user override), it is
// returned unchanged. Otherwise it falls back to a source identifier:
// user for user/user_inject steps, agent:<name|id> for assistant steps,
// and system for system steps.
func (a *Actor) defaultStepMeta(ev domain.StepEvent) string {
	if ev.Meta != "" {
		return ev.Meta
	}
	if ev.StepType == "user_inject" {
		return "user"
	}
	switch ev.Role {
	case "user":
		return "user"
	case "assistant", "tool":
		return a.agentMeta()
	case "system":
		return "system"
	default:
		return ""
	}
}

func (a *Actor) applyStepEvent(ev domain.StepEvent) {
	switch ev.Kind {
	case "step.opened":
		seq := ev.Seq
		if seq == 0 {
			seq = a.allocSeq()
		}
		step := domain.Step{
			ID:        ev.StepID,
			Role:      ev.Role,
			Type:      ev.StepType,
			Closed:    false,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
			TurnID:    ev.TurnID,
			Seq:       seq,
			Meta:      a.defaultStepMeta(ev),
			Model:     ev.Model,
			Provider:  ev.Provider,
		}
		// Reasoning content lives in ReasoningContent, not Content.
		if ev.StepType != "reasoning" && ev.Block != nil {
			step.Content = append(step.Content, *ev.Block)
		}
		for _, existing := range a.steps {
			if existing.ID == step.ID {
				return
			}
		}
		a.appendStep(step)
	case "block.appended":
		for i := range a.steps {
			if a.steps[i].ID == ev.StepID {
				if ev.Block != nil {
					a.touchStep(i)
					// tool_result blocks for fork_child arrive twice:
					// once from applySingleToolResult (placeholder),
					// once from resolveForkStep (actual result).
					// Replace the existing block instead of appending a duplicate.
					if ev.Block.Type == domain.ContentBlockToolResult && ev.Block.ToolUseID != "" {
						for j := range a.steps[i].Content {
							if a.steps[i].Content[j].Type == domain.ContentBlockToolResult && a.steps[i].Content[j].ToolUseID == ev.Block.ToolUseID {
								a.steps[i].Content[j] = *ev.Block
								goto blockAppendedDone
							}
						}
					}
					a.steps[i].Content = append(a.steps[i].Content, *ev.Block)
					// Mirror the final tool_result text into Progress.summaryText so
					// reconnect/refresh sees the authoritative summary even if the
					// last execution_progress event was throttled before completion.
					if ev.Block.Type == domain.ContentBlockToolResult && ev.Block.Text != "" && a.steps[i].Progress != "" {
						var progress map[string]any
						if err := json.Unmarshal([]byte(a.steps[i].Progress), &progress); err == nil && progress != nil {
							progress["summaryText"] = ev.Block.Text
							if updated, err := json.Marshal(progress); err == nil {
								a.steps[i].Progress = string(updated)
							}
						}
					}
				}
			blockAppendedDone:
				break
			}
		}
	case "block.delta":
		for i := range a.steps {
			if a.steps[i].ID != ev.StepID {
				continue
			}
			if a.steps[i].Type == "reasoning" {
				a.steps[i].ReasoningContent += ev.Delta
				a.touchStep(i)
			} else {
				idx := int(ev.BlockIndex)
				if idx >= 0 && idx < len(a.steps[i].Content) {
					a.steps[i].Content[idx].Text += ev.Delta
					a.touchStep(i)
				}
			}
			break
		}
		if a.child.Mode && ev.Delta != "" && a.agentKind == "explorer" {
			// Only accumulate text from non-reasoning steps into the summary.
			isReasoning := false
			for _, s := range a.steps {
				if s.ID == ev.StepID && s.Type == "reasoning" {
					isReasoning = true
					break
				}
			}
			if !isReasoning {
				a.child.SummaryText += ev.Delta
				a.child.ProgressDirty = true
			}
		}
	case "step.reset":
		for i := range a.steps {
			if a.steps[i].ID != ev.StepID {
				continue
			}
			removed := ""
			if a.steps[i].Type == "reasoning" {
				a.steps[i].ReasoningContent = ""
				a.touchStep(i)
			} else {
				for j := range a.steps[i].Content {
					removed += a.steps[i].Content[j].Text
					a.steps[i].Content[j].Text = ""
				}
				a.touchStep(i)
			}
			// The explorer child summary accumulated this step's text as a
			// suffix (only non-reasoning text enters it, appended in arrival
			// order); trim it so a retried dispatch does not double-count.
			if a.child.Mode && a.agentKind == "explorer" && removed != "" &&
				strings.HasSuffix(a.child.SummaryText, removed) {
				a.child.SummaryText = a.child.SummaryText[:len(a.child.SummaryText)-len(removed)]
				a.child.ProgressDirty = true
			}
			break
		}
	case "step.closed":
		for i := range a.steps {
			if a.steps[i].ID == ev.StepID {
				a.steps[i].Closed = true
				a.steps[i].CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
				if ev.Usage != nil {
					a.steps[i].Usage = copyUsage(ev.Usage)
				}
				a.touchStep(i)
				break
			}
		}
		// Per-step decay: advance memory graph by one or more ticks after
		// every step closes, using the step's total token count (input +
		// output). Input tokens dominate (prompt + context), so throughput
		// drives faster decay; output tokens alone under-counted work done.
		if g := a.graphRead(); g != nil {
			var tokens int64
			if ev.Usage != nil {
				tokens = int64(ev.Usage.OutputTokens) + int64(ev.Usage.InputTokens)
			}
			g.TickTokens(tokens)
		}
	case "step.execution_progress":
		for i := range a.steps {
			if a.steps[i].ID == ev.StepID {
				a.steps[i].ExecutionStatus = "in_progress"
				if ev.Progress != "" {
					a.steps[i].Progress = ev.Progress
				}
				a.touchStep(i)
				break
			}
		}
	case "step.error":
		for i := range a.steps {
			if a.steps[i].ID == ev.StepID {
				a.steps[i].Error = ev.Error
				a.steps[i].Closed = true
				a.steps[i].CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
				a.touchStep(i)
				break
			}
		}
	case "step.interaction_requested":
		seq := ev.Seq
		if seq == 0 {
			seq = a.allocSeq()
		}
		a.appendStep(domain.Step{
			ID:                ev.StepID,
			Role:              "assistant",
			Type:              ev.InteractionType,
			Closed:            false,
			Timestamp:         time.Now().UTC().Format(time.RFC3339Nano),
			StartedAt:         time.Now().UTC().Format(time.RFC3339Nano),
			TurnID:            ev.TurnID,
			Seq:               seq,
			ContentStatus:     "stable",
			InteractionStatus: "pending",
			RequestID:         ev.RequestID,
		})
		if ev.Task != nil {
			data, _ := json.Marshal(ev.Task)
			for i := range a.steps {
				if a.steps[i].ID == ev.StepID {
					a.steps[i].Content = []domain.ContentBlock{{Type: domain.ContentBlockText, Text: string(data)}}
					a.touchStep(i)
					break
				}
			}
		}
	case "step.interaction_resolved":
		for i := range a.steps {
			if a.steps[i].ID == ev.StepID {
				a.steps[i].InteractionStatus = "resolved"
				a.steps[i].Closed = true
				a.steps[i].CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
				if ev.Task != nil {
					data, _ := json.Marshal(ev.Task)
					a.steps[i].Content = append(a.steps[i].Content, domain.ContentBlock{Type: domain.ContentBlockText, Text: string(data)})
				}
				a.touchStep(i)
				break
			}
		}
		a.stepsGen++
	}
}

// dispatchFirstToolCallHint is the coordinator's contextual micro-tip dispatch
// seam: it runs when the first tool_result block of the session lands (wired
// from the turn engine's onToolResult callback). It claims the persistent
// hint.first-tool-call guard (idle → triggered, one-shot per account) and,
// exactly on the first claim, asks the interface manager to show a single-step
// guide bubble through the existing show_guide seam. Triggered or dismissed
// states never re-fire, so the hint cannot loop. All failures are logged and
// swallowed — a guidance hint must never break the turn it rides on.
func (a *Actor) dispatchFirstToolCallHint(ctx actor.Context, ev domain.StepEvent) {
	if a.agentKind != domain.AgentKindCoordinator {
		return
	}
	if ev.Kind != "block.appended" || ev.Block == nil || ev.Block.Type != domain.ContentBlockToolResult {
		return
	}
	claimed, err := a.claimCoordinatorHint(ctx, coordinatorHintFirstToolCall)
	if err != nil {
		ctx.Logger().Warn("coordinator: first tool-call hint claim failed", "error", err)
		return
	}
	if !claimed {
		return
	}
	mgrRef, ok := ctx.LookupService("interfacemanager")
	if !ok || ctx.Planner() == nil {
		ctx.Logger().Warn("coordinator: first tool-call hint skipped, interfacemanager unavailable")
		return
	}
	req := domain.InterfaceManagerControlReq{
		Action:  "show_guide",
		AgentID: ctx.Self().ID().String(),
		Steps: []domain.GuideStep{
			{
				TargetGuideID: "composer.input",
				Title:         "onboarding.guide.hint.firstToolCall.title",
				Body:          "onboarding.guide.hint.firstToolCall.body",
				I18nTitleKey:  "onboarding.guide.hint.firstToolCall.title",
				I18nBodyKey:   "onboarding.guide.hint.firstToolCall.body",
				Placement:     "top",
			},
		},
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	if _, err := ctx.Planner().Call(callCtx, mgrRef, "interfacemanager.control", req).Await(); err != nil {
		ctx.Logger().Warn("coordinator: first tool-call hint show_guide failed", "error", err)
	}
}

func (a *Actor) handleTurnStatus(actor.PureContext) (domain.TurnStatus, error) {
	snap := a.snapshot.Load()
	if snap == nil {
		return domain.TurnStatus{}, nil
	}
	return snap.turnStatus, nil
}

// handleTurnMiddleware returns the middleware entries for the current turn.

func (a *Actor) handleTurnMiddleware(_ actor.PureContext) (gen.TurnMiddlewareResp, error) {
	return gen.TurnMiddlewareResp{}, nil
}

// handleTurnHistory returns the current turn's step history, trace entries, and
// usage. This is the agent-side replacement for turn.history.

func (a *Actor) handleTurnHistory(_ actor.PureContext) (gen.TurnHistoryResp, error) {
	snap := a.snapshot.Load()
	if snap == nil || snap.turnStatus.Turn.ID == "" {
		return gen.TurnHistoryResp{}, nil
	}
	// Only the current turn's steps are surfaced here; the full session
	// history is available via session.fork (inspect path).
	turnID := snap.turnStatus.Turn.ID
	return gen.TurnHistoryResp{
		Steps:        stepsToTurnActions(snap.steps, turnID),
		TraceEntries: nil,
		Usage:        snap.turnStatus.Turn.Usage,
	}, nil
}

// stepsToTurnActions converts the snapshot's Step list into TurnAction
// entries for the turn_history response. When turnID is non-empty, only
// steps belonging to that turn are included; otherwise all steps are
// returned.
//
// Each TurnAction carries StartedAt/CompletedAt so the frontend can compute
// per-step duration (CompletedAt - StartedAt). When CompletedAt is empty
// (e.g. the step is still running, or the historical data predates the
// stamping), the duration is "unknown" — no value is fabricated. Usage is
// copied from the Step so per-message token counts are visible without
// having to reconcile against the turn-level total.

func stepsToTurnActions(steps []domain.Step, turnID string) []gen.TurnAction {
	actions := make([]gen.TurnAction, 0, len(steps))
	for _, step := range steps {
		if turnID != "" && step.TurnID != turnID {
			continue
		}
		if step.Discarded {
			continue
		}
		actions = append(actions, stepToTurnAction(step))
	}
	return actions
}

func stepToTurnAction(step domain.Step) gen.TurnAction {
	state := "completed"
	if !step.Closed {
		state = "running"
	}
	if step.Error != "" {
		state = "failed"
	}

	act := gen.TurnAction{
		ID:          step.ID,
		Kind:        stepKind(step.Type),
		State:       state,
		Error:       step.Error,
		StartedAt:   step.StartedAt,
		CompletedAt: step.CompletedAt,
		Usage:       copyUsage(step.Usage),
		Meta:        step.Meta,
		Seq:         step.Seq,
	}

	// Extract tool-call metadata and I/O from content blocks so the row
	// is self-describing without requiring the frontend to parse blocks.
	for _, block := range step.Content {
		switch block.Type {
		case "text":
			if act.Text == "" {
				act.Text = block.Text
			}
		case "tool_use":
			act.Kind = "tool"
			act.CallableID = block.ToolName
			act.ToolUseID = block.ToolUseID
			if act.Input == "" {
				act.Input = block.Input
			}
			if act.Title == "" {
				act.Title = block.ToolName
			}
		case "tool_result", "memory_result":
			if act.Output == "" {
				act.Output = block.Text
			}
			if block.ToolUseID != "" {
				act.ToolUseID = block.ToolUseID
			}
			if block.IsError && act.Error == "" {
				act.Error = block.Text
			}
		}
	}

	if act.Title == "" && step.ReasoningContent != "" {
		act.Text = step.ReasoningContent
	}

	return act
}

// stepKind maps a Step.Type to a TurnAction.Kind label.

func stepKind(stepType string) string {
	switch stepType {
	case "text":
		return "assistant_text"
	case "reasoning":
		return "reasoning"
	case "tool_use", "tool_call":
		return "tool"
	case "tool_result":
		return "tool_result"
	default:
		return stepType
	}
}

// handleContextBudget returns the current context budget (estimated tokens,
// context window, and compaction threshold) from the atomic snapshot.

func (a *Actor) handleContextBudget(_ actor.PureContext) (*domain.TurnContextBudgetPayload, error) {
	snap := a.snapshot.Load()
	if snap == nil || snap.turnStatus.Turn.ContextBudget == nil {
		// Return zero value instead of nil to avoid gospore codec
		// "cannot encode nil pointer for schema struct" error.
		return &domain.TurnContextBudgetPayload{}, nil
	}
	cb := snap.turnStatus.Turn.ContextBudget
	return &domain.TurnContextBudgetPayload{
		EstimatedTokens:   cb.EstimatedTokens,
		ContextWindowSize: cb.ContextWindowSize,
		TokenBudget:       cb.TokenBudget,
	}, nil
}

// stepsToTurnEntries converts runtime Step UI units into TurnEntry slices
// for the legacy frontend status endpoint.

func stepsToTurnEntries(steps []domain.Step) []domain.TurnEntry {
	entries := make([]domain.TurnEntry, 0, len(steps))
	for _, step := range steps {
		if step.Role == "user" {
			continue
		}
		entry := domain.TurnEntry{
			ID:        step.ID,
			State:     "completed",
			StartedAt: step.Timestamp,
		}
		if !step.Closed {
			entry.State = "running"
		}
		if step.Error != "" {
			entry.State = "failed"
		}
		switch step.Type {
		case "text":
			entry.Kind = "assistant_text"
			for _, block := range step.Content {
				entry.Blocks = append(entry.Blocks, domain.TurnBlock{
					ID:    step.ID,
					Kind:  "text",
					Text:  block.Text,
					State: entry.State,
				})
			}
		case "reasoning":
			entry.Kind = "reasoning"
			for _, block := range step.Content {
				entry.Blocks = append(entry.Blocks, domain.TurnBlock{
					ID:    step.ID,
					Kind:  "reasoning",
					Text:  block.Text,
					State: entry.State,
				})
			}
		case "tool_call":
			entry.Kind = "tool"
			for _, block := range step.Content {
				tb := domain.TurnBlock{
					ID:       step.ID,
					Kind:     "tool",
					State:    entry.State,
					Text:     block.Text,
					Name:     block.ToolName,
					DataJSON: block.Input,
				}
				if block.Type == "tool_result" {
					tb.Text = block.Text
					tb.ToolUseID = block.ToolUseID
				}
				entry.Blocks = append(entry.Blocks, tb)
			}
		default:
			entry.Kind = step.Type
			for _, block := range step.Content {
				entry.Blocks = append(entry.Blocks, domain.TurnBlock{
					ID:    step.ID,
					Kind:  block.Type,
					Text:  block.Text,
					State: entry.State,
				})
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func truncateStepText(s domain.TurnAction) domain.TurnAction {
	s.Text = truncateStatusField(s.Text)
	s.Output = truncateStatusField(s.Output)
	s.Input = truncateStatusField(s.Input)
	return s
}

// truncateStatusField sanitizes a turn-status text field and caps it at
// statusStepTextLimit on a rune boundary.

func truncateStatusField(s string) string {
	s = sanitizeStepText(s)
	if len(s) <= statusStepTextLimit {
		return s
	}
	return cutRunes(s, statusStepTextLimit) + "..."
}

// cutRunes returns the longest prefix of s that is at most max bytes and ends
// on a UTF-8 rune boundary. Cutting mid-rune produces invalid UTF-8 that the
// wire codec refuses to marshal.

func cutRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func copyUsage(u *domain.UsageData) *domain.UsageData {
	if u == nil {
		return nil
	}
	copy := *u
	return &copy
}

// mergeUsageData accumulates a delta usage into a base. Used by handleTurnComplete
// to fold a resumed segment's usage into the turn's existing entry. It merges all
// token, reasoning and cost fields so resumed turns report full cumulative usage.
func mergeUsageData(base, delta *domain.UsageData) *domain.UsageData {
	if delta == nil {
		return base
	}
	if base == nil {
		cp := *delta
		return &cp
	}
	base.InputTokens += delta.InputTokens
	base.OutputTokens += delta.OutputTokens
	base.CacheCreationInputTokens += delta.CacheCreationInputTokens
	base.CacheReadInputTokens += delta.CacheReadInputTokens
	base.ReasoningTokens += delta.ReasoningTokens
	base.TotalTokens = base.InputTokens + base.OutputTokens + base.CacheCreationInputTokens + base.CacheReadInputTokens
	base.CostInput += delta.CostInput
	base.CostOutput += delta.CostOutput
	base.CostCacheRead += delta.CostCacheRead
	base.CostCacheWrite += delta.CostCacheWrite
	base.CostTotal += delta.CostTotal
	if delta.MaxContextLength > base.MaxContextLength {
		base.MaxContextLength = delta.MaxContextLength
	}
	if delta.EstimatedPromptTokens > base.EstimatedPromptTokens {
		base.EstimatedPromptTokens = delta.EstimatedPromptTokens
	}
	return base
}

// mergeTurnFileChanges appends a resumed segment's file changes to the turn's
// existing change list, deduplicating by path (the later segment wins) so a
// resumed turn shows the union of files it touched.
func mergeTurnFileChanges(base, delta []domain.TurnFileChange) []domain.TurnFileChange {
	if len(delta) == 0 {
		return base
	}
	byPath := make(map[string]int, len(base)+len(delta))
	out := make([]domain.TurnFileChange, 0, len(base)+len(delta))
	for _, c := range base {
		byPath[c.Path] = len(out)
		out = append(out, c)
	}
	for _, c := range delta {
		if idx, ok := byPath[c.Path]; ok {
			out[idx] = c
		} else {
			byPath[c.Path] = len(out)
			out = append(out, c)
		}
	}
	return out
}

func copyContextBudget(cb *domain.TurnContextBudgetPayload) *domain.TurnContextBudgetPayload {
	if cb == nil {
		return nil
	}
	copy := *cb
	return &copy
}

func (a *Actor) handleSessionUndo(ctx actor.Context) (domain.Session, error) {
	if a.getActiveTurnRef() != "" {
		return domain.Session{}, fmt.Errorf("agent: cannot undo while turn is active")
	}
	if len(a.Session.Turns) == 0 {
		return domain.Session{}, fmt.Errorf("agent: nothing to undo")
	}
	head := int(a.Session.ActiveHead)
	if head >= len(a.Session.Turns) {
		head = len(a.Session.Turns) - 1
	}
	for head >= 0 {
		if a.Session.Turns[head].Role == "assistant" {
			break
		}
		head--
	}
	if head < 0 {
		return domain.Session{}, fmt.Errorf("agent: no assistant turn to undo")
	}
	head--
	a.Session.ActiveHead = int32(head)
	return a.Session, nil
}

// clearSession resets the agent's conversation context. Any active turn is
// cancelled first so the session is left in a clean, empty state.
func (a *Actor) clearSession(ctx actor.Context) error {
	// Clear the inferred title first so that any workspace notification
	// triggered by the cancel path pushes the empty title instead of the
	// stale one.
	a.title = ""
	a.Title = ""
	if a.getActiveTurnRef() != "" {
		if err := a.handleTurnCancel(ctx); err != nil {
			return fmt.Errorf("agent: cancel active turn before clear: %w", err)
		}
	}

	a.Session = domain.Session{ActiveHead: -1}
	a.RawSession.SummarySegments = nil
	a.RawSession.CompactionEvents = nil
	a.RawSession.CompactionLock = nil
	a.RawSession.ExploreResults = nil
	a.RawSession.Goal = nil
	a.RawSession.Tasks = nil
	a.RawSession.TaskHistory = nil
	a.RawSession.Steps = nil
	a.steps = nil
	a.rebuildMsgCache()
	a.stepsPersistedMu.Lock()
	a.stepsPersisted = nil
	a.stepsPersistedMu.Unlock()
	_ = os.RemoveAll(a.stepsDir())
	a.pendingSubmits = nil
	a.plan = planState{}
	// Reset turn status so subsequent read-only snapshots don't slice the
	// now-empty a.steps with a stale StartStepCount.
	unit := a.status.Unit
	a.status = turnStatus{Unit: unit}
	// Clear the inferred title so the frontend falls back to the display name.
	a.title = ""
	a.Title = ""
	if a.actorID != "" {
		a.saveMailbox(ctx)
		a.notifyWorkspaceStatus(ctx)
	}
	return nil
}

func (a *Actor) handleSessionFork(_ actor.PureContext, req domain.AgentSessionForkReq) (domain.AgentSessionForkResp, error) {
	snap := a.snapshot.Load()
	if snap == nil {
		return domain.AgentSessionForkResp{Session: domain.Session{}}, nil
	}
	head := int(snap.activeHead)
	if head < 0 {
		head = len(snap.turns) - 1
	}
	if req.AtTurnID != "" {
		found := -1
		for i, t := range snap.turns {
			if t.ID == req.AtTurnID {
				found = i
				break
			}
		}
		if found < 0 {
			return domain.AgentSessionForkResp{}, fmt.Errorf("agent: turn %s not found", req.AtTurnID)
		}
		head = found
	}
	if head < 0 || head >= len(snap.turns) {
		return domain.AgentSessionForkResp{Session: domain.Session{}}, nil
	}
	forked := make([]domain.Turn, head+1)
	copy(forked, snap.turns[:head+1])

	// Build the set of TurnIDs retained in the fork prefix so we can filter
	// per-turn artifacts (ExploreResults) to those that still have an anchor.
	retainedTurnIDs := make(map[string]struct{}, head+1)
	for i := 0; i <= head; i++ {
		retainedTurnIDs[snap.turns[i].ID] = struct{}{}
	}

	// Compaction removes early turns from Session.Turns but keeps their steps in
	// a.steps as content-less (Discarded) placeholders so summary-segment
	// indices stay aligned to the absolute step count. A fork must carry those
	// placeholders along with the retained recent turns' steps; otherwise the
	// clone's step array loses alignment with its summary segments: the compacted
	// early history (which exists only inside the summary) is dropped, and
	// compileMessages' coveredEnd check then skips the clone's recent steps
	// because their array indices fall below the summary's absolute SourceEndIndex.
	var forkedSteps []domain.Step
	for _, step := range snap.steps {
		if _, ok := retainedTurnIDs[step.TurnID]; ok {
			forkedSteps = append(forkedSteps, step)
			continue
		}
		if step.Discarded {
			forkedSteps = append(forkedSteps, step)
		}
	}
	forkStepCount := int32(len(forkedSteps))

	var segs []domain.SummarySegment
	for _, seg := range snap.summarySegments {
		if seg.SourceEndIndex == 0 || seg.SourceEndIndex <= forkStepCount {
			segs = append(segs, seg)
		}
	}

	var explore []domain.ExploreResult
	for _, er := range snap.exploreResults {
		if _, ok := retainedTurnIDs[er.TurnID]; ok {
			explore = append(explore, er)
		}
	}

	return domain.AgentSessionForkResp{
		Session: domain.Session{
			Turns:      forked,
			ActiveHead: int32(head),
		},
		SummarySegments: segs,
		ExploreResults:  explore,
		Steps:           forkedSteps,
		Goal:            snap.goal,
		NextIdx:         snap.nextIdx,
		NextSeq:         snap.nextSeq,
		NextTurnOrder:   a.RawSession.NextTurnOrder,
	}, nil
}

func (a *Actor) handleSessionImport(ctx actor.Context, req domain.AgentSessionImportReq) (domain.AgentSessionImportResp, error) {
	head := req.Session.ActiveHead
	if head < 0 || int(head) >= len(req.Session.Turns) {
		head = int32(len(req.Session.Turns) - 1)
	}
	turns := make([]domain.Turn, len(req.Session.Turns))
	copy(turns, req.Session.Turns)
	a.Session.Turns = turns
	a.Session.ActiveHead = head

	if req.SummarySegments != nil {
		segs := make([]domain.SummarySegment, len(req.SummarySegments))
		copy(segs, req.SummarySegments)
		a.RawSession.SummarySegments = segs
	}
	if req.ExploreResults != nil {
		er := make([]domain.ExploreResult, len(req.ExploreResults))
		copy(er, req.ExploreResults)
		a.RawSession.ExploreResults = er
	}
	if req.Steps != nil {
		steps := make([]domain.Step, len(req.Steps))
		copy(steps, req.Steps)
		a.RawSession.Steps = steps
		_ = os.RemoveAll(a.stepsDir())
		a.stepsPersistedMu.Lock()
		a.stepsPersisted = nil
		a.stepsPersistedMu.Unlock()
	}
	if req.Goal != nil {
		goal := *req.Goal
		a.RawSession.Goal = &goal
	}
	if req.NextIdx > 0 {
		a.RawSession.NextIdx = req.NextIdx
	}
	if req.NextSeq > 0 {
		a.RawSession.NextSeq = req.NextSeq
	}
	if req.NextTurnOrder > 0 {
		a.RawSession.NextTurnOrder = req.NextTurnOrder
	} else {
		// Fall back: scan imported turns to derive NextTurnOrder so the
		// next submitted turn does not collide with imported turns.
		a.migrateTurnOrders()
	}

	_ = a.rebuildSteps()
	a.takeSnapshot()
	// See handleSessionImportTurns: signal the fork/clone path so subscribers
	// re-fetch instead of showing an empty session.
	if len(turns) > 0 && ctx != nil {
		_ = ctx.EmitEvent("turn", domain.TurnEvent{Kind: domain.TurnHistoryImported})
	}
	// Persist the imported session so the clone survives an actor restart /
	// passivation. Without this the imported turns/steps live only in memory and
	// are lost on the next OnStart Load, leaving the clone with empty assistant
	// turns. Mirrors the saveMailbox call every other mutating handler makes.
	if a.actorID != "" {
		a.saveMailbox(ctx)
	}
	return domain.AgentSessionImportResp{
		AcceptedTurns: int32(len(turns)),
		ActiveHead:    head,
	}, nil
}

// handleSessionExportRange exports a slice of older turns (and their dependent
// steps, summary segments and explore results) for chunked session cloning.
func (a *Actor) handleSessionExportRange(_ actor.PureContext, req domain.AgentSessionExportRangeReq) (domain.AgentSessionExportRangeResp, error) {
	snap := a.snapshot.Load()
	if snap == nil {
		return domain.AgentSessionExportRangeResp{HasMore: false}, nil
	}

	limit := 20
	if req.Limit > 0 {
		limit = int(req.Limit)
	}

	turns := turnsBySeq(snap.turns)
	total := len(turns)

	var slice []domain.Turn
	hasMore := false
	nextBeforeTurnID := ""

	if total > 0 {
		startIdx := total
		if req.BeforeTurnID != "" {
			for i, t := range turns {
				if t.ID == req.BeforeTurnID {
					startIdx = i
					break
				}
			}
		}
		if startIdx > 0 {
			endIdx := startIdx
			beginIdx := startIdx - limit
			if beginIdx < 0 {
				beginIdx = 0
			}
			slice = make([]domain.Turn, endIdx-beginIdx)
			copy(slice, turns[beginIdx:endIdx])
			hasMore = beginIdx > 0
			if len(slice) > 0 {
				nextBeforeTurnID = slice[0].ID
			}
		}
	}

	// Include the active in-progress assistant turn on the first page so that
	// cloning a running agent preserves its latest assistant message.
	if req.BeforeTurnID == "" && snap.turnStatus.Turn.ID != "" {
		activeTurn := snap.turnStatus.Turn
		// Normalise to completed so the clone shows the message as history rather
		// than an on-going turn that is no longer executing.
		if activeTurn.State != "completed" {
			activeTurn.State = "completed"
		}
		if activeTurn.CompletedAt == "" {
			activeTurn.CompletedAt = activeTurn.Timestamp
			if activeTurn.CompletedAt == "" {
				activeTurn.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
		}
		slice = append(slice, activeTurn)
	}

	if len(slice) == 0 {
		return domain.AgentSessionExportRangeResp{HasMore: false}, nil
	}

	retainedTurnIDs := make(map[string]struct{}, len(slice))
	for _, t := range slice {
		retainedTurnIDs[t.ID] = struct{}{}
	}

	var steps []domain.Step
	// Recent turns' steps for this chunk, plus (on the oldest page) the early
	// discarded placeholder steps whose turns compaction already removed from
	// Session.Turns. Those placeholders keep summary-segment indices aligned to
	// the absolute step count in the clone; without them compileMessages'
	// coveredEnd check skips the clone's recent steps because their array
	// indices fall below the summary's absolute SourceEndIndex. import_turns
	// dedups by step ID, so emitting them once on the oldest page is enough.
	for _, step := range snap.steps {
		if _, ok := retainedTurnIDs[step.TurnID]; ok {
			steps = append(steps, step)
			continue
		}
		if step.Discarded && !hasMore {
			steps = append(steps, step)
		}
	}

	// export_range paginates backward from the most recent turn and transfers
	// the full session across repeated calls. Summary segments encode the
	// compacted early history whose original assistant steps no longer exist as
	// raw steps, so every segment must be carried over; otherwise the clone
	// loses that history. The import side dedups by source range, so emitting
	// all segments on every chunk is idempotent. (Unlike fork, export_range is
	// not a turn-0 prefix, so a step-count filter would wrongly drop segments
	// that cover older, not-yet-imported history.)
	var segs []domain.SummarySegment
	if len(snap.summarySegments) > 0 {
		segs = append(segs, snap.summarySegments...)
	}

	var explore []domain.ExploreResult
	for _, er := range snap.exploreResults {
		if _, ok := retainedTurnIDs[er.TurnID]; ok {
			explore = append(explore, er)
		}
	}

	return domain.AgentSessionExportRangeResp{
		Turns:            slice,
		Steps:            steps,
		SummarySegments:  segs,
		ExploreResults:   explore,
		HasMore:          hasMore,
		TotalTurns:       int32(total),
		NextBeforeTurnID: nextBeforeTurnID,
		NextSeq:          a.RawSession.NextSeq,
		NextIdx:          a.RawSession.NextIdx,
	}, nil
}

// handleSessionImportTurns appends a chunk of older turns to the current
// session. The incoming turns must be older than any existing turns; they are
// prepended to the turn list so chronological order is preserved.
func (a *Actor) handleSessionImportTurns(ctx actor.Context, req domain.AgentSessionImportTurnsReq) (domain.AgentSessionImportTurnsResp, error) {
	if req.SourceAgentID != "" && !a.cloneSourceSet {
		a.cloneSourceActorID = req.SourceAgentID
		a.cloneSourceTotalTurns = int(req.SourceTotalTurns)
		a.clonePendingHistory = true
		a.cloneSourceSet = true
	}

	if len(req.Turns) == 0 && len(req.Steps) == 0 && len(req.SummarySegments) == 0 && len(req.ExploreResults) == 0 {
		return domain.AgentSessionImportTurnsResp{AcceptedTurns: 0}, nil
	}

	existingTurnIDs := make(map[string]struct{}, len(a.Session.Turns))
	for _, t := range a.Session.Turns {
		existingTurnIDs[t.ID] = struct{}{}
	}

	newTurns := make([]domain.Turn, 0, len(req.Turns))
	for _, t := range req.Turns {
		if _, exists := existingTurnIDs[t.ID]; exists {
			continue
		}
		newTurns = append(newTurns, t)
		existingTurnIDs[t.ID] = struct{}{}
	}

	if len(newTurns) > 0 {
		a.Session.Turns = append(newTurns, a.Session.Turns...)
	}

	if req.SummarySegments != nil && len(req.SummarySegments) > 0 {
		existingSegKeys := make(map[string]struct{}, len(a.RawSession.SummarySegments))
		for _, seg := range a.RawSession.SummarySegments {
			key := fmt.Sprintf("%d-%d", seg.SourceStartIndex, seg.SourceEndIndex)
			existingSegKeys[key] = struct{}{}
		}
		for _, seg := range req.SummarySegments {
			key := fmt.Sprintf("%d-%d", seg.SourceStartIndex, seg.SourceEndIndex)
			if _, exists := existingSegKeys[key]; exists {
				continue
			}
			a.RawSession.SummarySegments = append(a.RawSession.SummarySegments, seg)
			existingSegKeys[key] = struct{}{}
		}
	}

	if req.ExploreResults != nil && len(req.ExploreResults) > 0 {
		existingExploreIDs := make(map[string]struct{}, len(a.RawSession.ExploreResults))
		for _, er := range a.RawSession.ExploreResults {
			existingExploreIDs[er.TurnID] = struct{}{}
		}
		for _, er := range req.ExploreResults {
			if _, exists := existingExploreIDs[er.TurnID]; exists {
				continue
			}
			a.RawSession.ExploreResults = append(a.RawSession.ExploreResults, er)
			existingExploreIDs[er.TurnID] = struct{}{}
		}
	}

	if req.Steps != nil && len(req.Steps) > 0 {
		existingStepIDs := make(map[string]struct{}, len(a.steps))
		for _, s := range a.steps {
			existingStepIDs[s.ID] = struct{}{}
		}
		for _, s := range req.Steps {
			if _, exists := existingStepIDs[s.ID]; exists {
				continue
			}
			a.RawSession.Steps = append(a.RawSession.Steps, s)
			existingStepIDs[s.ID] = struct{}{}
		}
	}

	// Propagate the source session's sequence counters so that any new
	// steps/turns created in the clone are ordered after the imported history.
	// Without this, the clone's next turn gets Seq 1 and appears before the
	// imported turns in the frontend timeline, making the new conversation
	// invisible or interleaved with the cloned history.
	if req.NextSeq > 0 && req.NextSeq > a.RawSession.NextSeq {
		a.RawSession.NextSeq = req.NextSeq
	}
	if req.NextTurnOrder > 0 && req.NextTurnOrder > a.RawSession.NextTurnOrder {
		a.RawSession.NextTurnOrder = req.NextTurnOrder
	}
	for _, t := range newTurns {
		if t.Seq >= a.RawSession.NextSeq {
			a.RawSession.NextSeq = t.Seq + 1
		}
		if t.TurnOrder >= a.RawSession.NextTurnOrder {
			a.RawSession.NextTurnOrder = t.TurnOrder + 1
		}
	}
	for _, s := range req.Steps {
		if s.Seq >= a.RawSession.NextSeq {
			a.RawSession.NextSeq = s.Seq + 1
		}
	}
	if req.NextIdx > 0 && req.NextIdx > a.RawSession.NextIdx {
		a.RawSession.NextIdx = req.NextIdx
	}

	_ = a.rebuildSteps()
	a.takeSnapshot()

	// Signal that history was imported off the live turn loop. Cloned/forked
	// agents receive their session this way; without an event the frontend
	// timeline — which fetched an empty summary at clone time — never re-fetches
	// and shows an empty conversation. Only emit when turns were actually added
	// so dedup no-ops and empty chunks stay quiet.
	if len(newTurns) > 0 && ctx != nil {
		_ = ctx.EmitEvent("turn", domain.TurnEvent{Kind: domain.TurnHistoryImported})
	}

	if req.IsFinal {
		a.clonePendingHistory = false
	}

	// Persist each imported chunk so the clone survives restart/passivation.
	// See handleSessionImport for rationale.
	if a.actorID != "" {
		a.saveMailbox(ctx)
	}

	return domain.AgentSessionImportTurnsResp{AcceptedTurns: int32(len(newTurns))}, nil
}

func (a *Actor) handleGetSession(_ actor.PureContext) (domain.AgentGetSessionResp, error) {
	snap := a.snapshot.Load()
	if snap == nil {
		return domain.AgentGetSessionResp{Turns: nil}, nil
	}

	turns := make([]domain.Turn, len(snap.turns))
	copy(turns, snap.turns)

	var activeTurn *domain.TurnStatus
	if snap.activeTurnRef != "" || snap.turnID != "" {
		activeTurn = &snap.turnStatus
	}

	return domain.AgentGetSessionResp{
		Turns:            turns,
		ActiveTurn:       activeTurn,
		ActiveTurnEvents: nil,
	}, nil
}

// handleSessionSummaryPure is the pure (stateless) variant of session.summary.
// It reads the pre-built atomic snapshot set by takeSnapshot() on the cell
// goroutine — no locks and no blocking on the cell goroutine.
//
// Cold start: when the snapshot is not ready yet (OnStart still reconstructing
// the session), this handler waits on onStartDone rather than returning an
// error immediately. This is SAFE because pure handlers run off the owner loop
// — blocking here does not stall OnStart, which synchronously calls back into
// the workspace owner loop (fetchAgentKindConfig). (Blocking the workspace or
// project owner loop instead would deadlock that loop chain.) The wait is
// bounded by the request's Done channel; if OnStart exceeds it, the caller
// (frontend reconciler) retries.
func (a *Actor) handleSessionSummaryPure(ctx actor.PureContext, req domain.AgentSessionSummaryReq) (domain.AgentSessionSummaryResp, error) {
	snap := a.snapshot.Load()
	if snap == nil || !snap.ready {
		// Bridge cold start: wait for OnStart to finish seeding the snapshot.
		if a.onStartDone != nil {
			if ctx == nil {
				<-a.onStartDone
			} else {
				select {
				case <-a.onStartDone:
				case <-ctx.Done():
					return domain.AgentSessionSummaryResp{}, fmt.Errorf("agent: session snapshot not ready")
				}
			}
		}
		snap = a.snapshot.Load()
		if snap == nil || !snap.ready {
			return domain.AgentSessionSummaryResp{}, fmt.Errorf("agent: session snapshot not ready")
		}
	}

	turns := turnsBySeq(snap.turns)
	total := len(turns)
	var recent []domain.Turn
	hasMore := false
	hasMoreBefore := false

	// Load-more mode: offset + limit from the beginning (oldest turns first).
	// Default mode: MaxTurns from the end (newest turns last).
	if req.Offset > 0 || req.Limit > 0 {
		limit := 5
		if req.Limit > 0 {
			limit = int(req.Limit)
		}
		offset := int(req.Offset)
		end := offset + limit
		if end >= total {
			end = total
		} else {
			hasMore = true
		}
		if offset > 0 {
			hasMoreBefore = true
		}
		if offset < total {
			recent = make([]domain.Turn, end-offset)
			copy(recent, turns[offset:end])
		}
		_ = offset
	} else {
		maxTurns := 5
		if req.MaxTurns > 0 {
			maxTurns = int(req.MaxTurns)
		}
		// Reconnect: expand the window when the client's known seq watermark is
		// behind the server's nextSeq. This prevents a fixed MaxTurns=5 window
		// from truncating turns that contain steps/events produced while the
		// client was in the background.
		if len(req.KnownStepEventSeqs) > 0 {
			expanded := computeReconnectTurnWindow(turns, snap.steps, snap.nextSeq, req.KnownStepEventSeqs, maxTurns, 50)
			if expanded > maxTurns {
				maxTurns = expanded
			}
		}
		if total > maxTurns {
			recent = make([]domain.Turn, maxTurns)
			copy(recent, turns[total-maxTurns:])
			hasMore = true
			hasMoreBefore = true
		} else {
			recent = make([]domain.Turn, total)
			copy(recent, turns)
		}
		_ = total // reserved for future use
	}

	resp := domain.AgentSessionSummaryResp{
		Turns:            recent,
		ActiveTurnEvents: nil,
		TotalTurns:       int32(total),
		HasMoreHistory:   hasMore || hasMoreBefore || (a.clonePendingHistory && total < a.cloneSourceTotalTurns),
		AgentState:       snap.statusState,
		NextSeq:          snap.nextSeq,
		Goal:             snap.goal,
	}
	for _, step := range snap.steps {
		if step.Discarded {
			resp.HasDiscardedSteps = true
			break
		}
	}
	if snap.turnStatus.Turn.ID != "" {
		resp.ActiveTurn = snap.turnStatus
	}
	if len(snap.exploreResults) > 0 {
		er := make([]domain.ExploreResult, len(snap.exploreResults))
		copy(er, snap.exploreResults)
		resp.ExploreResults = er
	}

	// Include steps from the selected turns + active turn.
	recentTurnIDs := make(map[string]struct{}, len(recent)+1)
	for _, t := range recent {
		recentTurnIDs[t.ID] = struct{}{}
	}
	if snap.turnStatus.Turn.ID != "" {
		recentTurnIDs[snap.turnStatus.Turn.ID] = struct{}{}
	}
	var recentSteps []domain.Step
	for _, step := range snap.steps {
		if _, ok := recentTurnIDs[step.TurnID]; ok {
			recentSteps = append(recentSteps, step)
		}
	}
	resp.Steps = recentSteps

	// Reconnection: use knownStepEventSeqs (stepId → last consumed seq) when available.
	// Falls back to KnownStepIDs for older clients.
	if len(req.KnownStepEventSeqs) > 0 {
		for _, step := range recentSteps {
			lastSeq, known := req.KnownStepEventSeqs[step.ID]
			if !known || step.Seq > int64(lastSeq) {
				resp.MissingSteps = append(resp.MissingSteps, step)
			}
		}
		// Return events for still-open steps so the frontend can replay deltas
		// that were lost during the disconnect window.
		if len(snap.openStepEvents) > 0 {
			for _, events := range snap.openStepEvents {
				resp.OpenStepEvents = append(resp.OpenStepEvents, events...)
			}
			slices.SortFunc(resp.OpenStepEvents, func(a, b domain.StepEvent) int {
				return cmp.Compare(a.EventSeq, b.EventSeq)
			})
		}
	} else if len(req.KnownStepIDs) > 0 {
		known := make(map[string]struct{}, len(req.KnownStepIDs))
		for _, id := range req.KnownStepIDs {
			known[id] = struct{}{}
		}
		for _, step := range recentSteps {
			if _, ok := known[step.ID]; !ok {
				resp.MissingSteps = append(resp.MissingSteps, step)
			}
		}
	}
	return resp, nil
}

// turnsBySeq returns a copy of session turns in stable turn order, with Seq as legacy fallback.
// computeReconnectTurnWindow calculates how many of the newest turns must be
// returned so that every step whose seq is ahead of the client's known
// watermark is included. It keeps at least baseMaxTurns (so the UI still has
// recent context) and caps at hardMaxTurns to avoid huge reconnect payloads.
func computeReconnectTurnWindow(
	turns []domain.Turn,
	steps []domain.Step,
	nextSeq int64,
	knownStepEventSeqs map[string]int32,
	baseMaxTurns int,
	hardMaxTurns int,
) int {
	var maxKnownSeq int64
	for _, seq := range knownStepEventSeqs {
		if int64(seq) > maxKnownSeq {
			maxKnownSeq = int64(seq)
		}
	}
	if maxKnownSeq == 0 || maxKnownSeq >= nextSeq-1 {
		return baseMaxTurns
	}

	missingTurnIDs := make(map[string]struct{})
	for _, step := range steps {
		if step.Seq > maxKnownSeq && step.Seq < nextSeq {
			missingTurnIDs[step.TurnID] = struct{}{}
		}
	}
	if len(missingTurnIDs) == 0 {
		return baseMaxTurns
	}

	total := len(turns)
	covered := 0
	selected := baseMaxTurns
	for i := total - 1; i >= 0 && (total-i) <= hardMaxTurns; i-- {
		selected = total - i
		if _, ok := missingTurnIDs[turns[i].ID]; ok {
			covered++
			if covered >= len(missingTurnIDs) {
				break
			}
		}
	}
	return selected
}

func turnsBySeq(in []domain.Turn) []domain.Turn {
	out := make([]domain.Turn, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TurnOrder > 0 || out[j].TurnOrder > 0 {
			if out[i].TurnOrder == 0 {
				return false
			}
			if out[j].TurnOrder == 0 {
				return true
			}
			if out[i].TurnOrder != out[j].TurnOrder {
				return out[i].TurnOrder < out[j].TurnOrder
			}
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

func (a *Actor) handleTurnsList(_ actor.PureContext, req domain.AgentTurnsListReq) (domain.AgentTurnsListResp, error) {
	snap := a.snapshot.Load()
	if snap == nil {
		return domain.AgentTurnsListResp{Turns: nil, HasMore: false}, nil
	}

	limit := 20
	if req.Limit > 0 {
		limit = int(req.Limit)
	}

	turns := turnsBySeq(snap.turns)
	total := len(turns)

	if total == 0 {
		return domain.AgentTurnsListResp{Turns: nil, HasMore: false}, nil
	}

	startIdx := total
	if req.BeforeTurnID != "" {
		for i, t := range turns {
			if t.ID == req.BeforeTurnID {
				startIdx = i
				break
			}
		}
		// AUDIT 3.2: BeforeTurnID not found. Returning HasMore=false made the
		// "load more" button permanently disappear when the anchor turn was
		// still live-only. Return HasMore=true so the button survives — the
		// frontend skips stale anchors (AUDIT 3.1) and retries with a valid one.
		if startIdx == total {
			return domain.AgentTurnsListResp{Turns: nil, HasMore: total > limit}, nil
		}
	}

	// Walk backwards from startIdx to assemble a slice of at most `limit` turns.
	// beginIdx is the index of the oldest turn included; anything strictly older
	// is the "more" region.
	slice := make([]domain.Turn, 0, limit)
	beginIdx := startIdx
	for i := startIdx - 1; i >= 0 && len(slice) < limit; i-- {
		beginIdx = i
		slice = append([]domain.Turn{turns[i]}, slice...)
	}

	hasMore := beginIdx > 0
	// Also surface more when the clone-pending path advertises more.
	if a.clonePendingHistory && total < a.cloneSourceTotalTurns {
		hasMore = true
	}

	// Include steps for the returned turns so the UI can reconstruct frames
	// for older assistant turns that are no longer actively streaming.
	turnIDs := make(map[string]struct{}, len(slice))
	for _, t := range slice {
		turnIDs[t.ID] = struct{}{}
	}
	var steps []domain.Step
	for _, step := range snap.steps {
		if _, ok := turnIDs[step.TurnID]; ok {
			steps = append(steps, step)
		}
	}

	return domain.AgentTurnsListResp{
		Turns:   slice,
		HasMore: hasMore,
		Steps:   steps,
	}, nil
}

// chatMessageToStep converts a ChatMessage to a Step UI unit.

func chatMessageToStep(msg domain.ChatMessage, turnId string) domain.Step {
	if msg.ReasoningContent != "" && len(msg.Content) == 0 {
		return domain.Step{
			ID:               msg.ID,
			Role:             msg.Role,
			Type:             "reasoning",
			ReasoningContent: msg.ReasoningContent,
			Closed:           true,
			Timestamp:        msg.Timestamp,
			Usage:            copyUsage(msg.Usage),
			StartedAt:        msg.StartedAt,
			CompletedAt:      msg.CompletedAt,
			TurnID:           turnId,
		}
	}
	stepType := "text"
	if msg.Role == "user" {
		stepType = "text"
	} else if len(msg.Content) == 1 && msg.Content[0].Type == "tool_use" {
		stepType = "tool_call"
	}
	return domain.Step{
		ID:               msg.ID,
		Role:             msg.Role,
		Type:             stepType,
		Content:          append([]domain.ContentBlock(nil), msg.Content...),
		Closed:           true,
		Timestamp:        msg.Timestamp,
		Usage:            copyUsage(msg.Usage),
		StartedAt:        msg.StartedAt,
		CompletedAt:      msg.CompletedAt,
		TurnID:           turnId,
		ReasoningContent: msg.ReasoningContent,
	}
}

// stepToChatMessage converts a Step back to a ChatMessage for LLM dispatch.

func stepToChatMessage(step domain.Step) domain.ChatMessage {
	msg := domain.ChatMessage{
		ID:          step.ID,
		Role:        step.Role,
		Timestamp:   step.Timestamp,
		Usage:       copyUsage(step.Usage),
		StartedAt:   step.StartedAt,
		CompletedAt: step.CompletedAt,
	}
	if step.Type == "reasoning" {
		msg.ReasoningContent = step.ReasoningContent
		msg.Content = nil
		return msg
	}
	msg.Content = append([]domain.ContentBlock(nil), step.Content...)
	msg.ReasoningContent = step.ReasoningContent
	return msg
}

// appendStep is the single choke point for appending a step to a.steps: it
// appends the step and its initial ChatMessage conversion to msgCache in the
// same call, preserving the invariant len(a.msgCache) == len(a.steps). Every
// new-step write must go through here (or rebuildMsgCache for wholesale
// slice replacement).
func (a *Actor) appendStep(step domain.Step) {
	msg := stepToChatMessage(step)
	msg.Idx = int32(len(a.steps))
	a.steps = append(a.steps, step)
	a.msgCache = append(a.msgCache, msg)
	a.msgDirty = append(a.msgDirty, false)
}

// touchStep marks msgCache[i] dirty so takeSnapshot re-converts it from
// a.steps[i] at the next publish. Every in-place mutation of a.steps[i] must
// call this.
func (a *Actor) touchStep(i int) {
	if i >= 0 && i < len(a.msgDirty) {
		a.msgDirty[i] = true
	}
}

// applyImageRecognition writes a one-shot image recognition result from the
// turn engine back into the persisted step that backs the history message (by
// message ID), so the Recognized flag survives engine restarts: a rebuilt
// engine compiles steps into history again and never re-recognizes. Best
// effort — synthesized engine-internal messages without a backing step are
// skipped.
func (a *Actor) applyImageRecognition(stepID string, blockIdx int, recognized bool, text string) {
	for i := range a.steps {
		if a.steps[i].ID != stepID {
			continue
		}
		if blockIdx < 0 || blockIdx >= len(a.steps[i].Content) {
			return
		}
		a.steps[i].Content[blockIdx].Recognized = recognized
		a.steps[i].Content[blockIdx].RecognitionText = text
		a.touchStep(i)
		return
	}
}

// rebuildMsgCache rebuilds msgCache wholesale from a.steps. Used where the
// steps slice is replaced or spliced as a whole (session load/rebuild,
// recompact, clear) rather than appended or index-mutated.
func (a *Actor) rebuildMsgCache() {
	a.msgCache = make([]domain.ChatMessage, len(a.steps))
	for i, step := range a.steps {
		msg := stepToChatMessage(step)
		msg.Idx = int32(i)
		a.msgCache[i] = msg
	}
	a.msgDirty = make([]bool, len(a.steps))
}

// messageHasContent returns true if the ChatMessage carries non-empty
// text, non-empty reasoning, or any LLM-compatible non-text block.
// UI-only block types such as "compaction" are ignored.

func messageHasContent(msg domain.ChatMessage) bool {
	if msg.ReasoningContent != "" {
		return true
	}
	for _, cb := range msg.Content {
		if !isLLMContentBlock(cb) {
			continue
		}
		if cb.Type == domain.ContentBlockText && cb.Text != "" {
			return true
		}
		if cb.Type != domain.ContentBlockText {
			return true
		}
	}
	return false
}

// filterLLMContent keeps only blocks that an LLM provider understands.
// UI-only artifacts (e.g. compaction frames, user_inject markers) are removed.
func filterLLMContent(msg *domain.ChatMessage) {
	if msg == nil {
		return
	}
	filtered := msg.Content[:0]
	for _, cb := range msg.Content {
		if isLLMContentBlock(cb) {
			filtered = append(filtered, cb)
		}
	}
	msg.Content = filtered
}

// applyGoalSubmitPrefix prepends a goal-condition marker to the first text
// block of the message. It is applied to the user step whose Meta is
// "goal_submit" while the goal is not yet Confirmed.
func applyGoalSubmitPrefix(msg *domain.ChatMessage) {
	if msg == nil {
		return
	}
	const prefix = "[This message states the goal condition. Do NOT start working on it yet. Analyze the user's intent. If the intent is an actionable, trackable task, search existing task cards and call goal_card_submit with its CardId; otherwise call goal_submit with your interpretation. Wait for confirmation before working.]\n\n"
	for i := range msg.Content {
		if msg.Content[i].Type == domain.ContentBlockText {
			msg.Content[i].Text = prefix + msg.Content[i].Text
			return
		}
	}
	// No text block: prepend one carrying only the marker.
	msg.Content = append([]domain.ContentBlock{{Type: domain.ContentBlockText, Text: prefix}}, msg.Content...)
}

// applyWorkflowStartPrefix prepends a workflow-intent marker to the first text
// block of the message. It is applied to the user step whose Meta is
// "workflow_submit" while no workflow is active, mirroring the goal_submit
// prefix for an unconfirmed goal.
func applyWorkflowStartPrefix(msg *domain.ChatMessage) {
	if msg == nil {
		return
	}
	const prefix = "[This message states the workflow intent. Do NOT start orchestrating yet. Analyze the user's intent, create or find a map card, then call workflow_start with its MapCardId. Wait for confirmation before orchestrating.]\n\n"
	for i := range msg.Content {
		if msg.Content[i].Type == domain.ContentBlockText {
			msg.Content[i].Text = prefix + msg.Content[i].Text
			return
		}
	}
	msg.Content = append([]domain.ContentBlock{{Type: domain.ContentBlockText, Text: prefix}}, msg.Content...)
}

// parseSenderMeta splits a step meta of the form "<kind>|<id>|<name>" into its
// parts, where kind is "agent" (peer agent message) or "user" (human inject).
// ok is true only when the meta carries a usable identity (non-empty id or
// name) beyond the bare kind marker; bare "agent"/"user" return ok=false.
// The legacy self-stamp shape user|<subject>|<subject> (identical halves,
// written before direct submits stopped being stamped) is the local
// operator's own message and is rejected the same way.
func parseSenderMeta(meta string) (kind, senderID, name string, ok bool) {
	switch {
	case strings.HasPrefix(meta, "agent|"):
		kind = "agent"
	case strings.HasPrefix(meta, "user|"):
		kind = "user"
	default:
		return "", "", "", false
	}
	rest := meta[len(kind)+1:]
	parts := strings.SplitN(rest, "|", 2)
	if len(parts) > 0 {
		senderID = parts[0]
	}
	if len(parts) > 1 {
		name = parts[1]
	}
	if senderID == "" && name == "" {
		return "", "", "", false
	}
	if kind == "user" && senderID != "" && senderID == name {
		return "", "", "", false
	}
	return kind, senderID, name, true
}

// applyPeerSenderPrefix prepends a sender annotation to the first text block
// of the message so the LLM can tell peer-agent and human-inject input apart
// from the operator's own messages. Applied to user-role messages whose step
// meta is "agent|<id>|<name>" or "user|<id>|<name>".
func applyPeerSenderPrefix(msg *domain.ChatMessage, kind, senderID, name string) {
	if msg == nil {
		return
	}
	label := name
	if label == "" {
		label = senderID
	}
	if label == "" {
		return
	}
	idPart := ""
	if senderID != "" && senderID != label {
		idPart = " (" + senderID + ")"
	}
	var prefix string
	if kind == "agent" {
		prefix = fmt.Sprintf("[This message is from agent %q%s, not the human operator.]\n\n", label, idPart)
	} else {
		prefix = fmt.Sprintf("[This message is from user %q%s.]\n\n", label, idPart)
	}
	for i := range msg.Content {
		if msg.Content[i].Type == domain.ContentBlockText {
			msg.Content[i].Text = prefix + msg.Content[i].Text
			return
		}
	}
	msg.Content = append([]domain.ContentBlock{{Type: domain.ContentBlockText, Text: prefix}}, msg.Content...)
}

// isLLMContentBlock reports whether a content block type can be sent to an
// LLM provider. Unknown or UI-only types are rejected.
func isLLMContentBlock(cb domain.ContentBlock) bool {
	switch cb.Type {
	case domain.ContentBlockText, domain.ContentBlockToolUse, domain.ContentBlockToolResult, domain.ContentBlockImage:
		return true
	default:
		return false
	}
}

// compileMessages builds the []ChatMessage slice for the next LLM dispatch.
// It sources from Session.Turns Steps (and any in-flight steps in a.steps)
// and applies SummarySegment filtering using step array indices.

func (a *Actor) handlePlanSubmit(ctx actor.Context, req gen.PlanSubmitReq) (string, error) {
	requestID, err := a.applyPlanSubmit(ctx, req)
	if err != nil {
		return "", err
	}
	if a.plan.PlanCardID != "" {
		return fmt.Sprintf("Plan submitted for approval (requestId=%s, card=%s).", requestID, a.plan.PlanCardID), nil
	}
	return fmt.Sprintf("Plan submitted for approval (requestId=%s).", requestID), nil
}

func (a *Actor) applyPlanSubmit(ctx actor.Context, req gen.PlanSubmitReq) (string, error) {
	defer a.takeSnapshot()
	if !a.isSkillMounted("plan-module") {
		return "", fmt.Errorf("plan_submit: plan-module skill must be mounted and used before submitting a plan")
	}
	// Only pending_approval is a true blocker: an approval card is on screen
	// waiting for the user, and a second plan_submit would race / overlap.
	// approved / rejected / "" are history — a new plan_submit supersedes
	// them with fresh state.
	if a.plan.Status == "pending_approval" {
		return "", fmt.Errorf("plan already submitted (requestId=%s); await approval", a.plan.RequestID)
	}
	a.plan.Title = strings.TrimSpace(req.Title)
	if a.plan.Title == "" {
		return "", fmt.Errorf("plan_submit: Title is required")
	}
	if req.Body == "" {
		return "", fmt.Errorf("plan_submit: Body is required")
	}
	a.plan.Plan = req.Body
	if req.Policy != nil {
		a.plan.Policy = req.Policy
	}
	requestID := ctx.NewID().String()
	a.plan.RequestID = requestID
	a.plan.PendingTasks = req.Tasks
	a.plan.ApprovedTasks = nil

	// Persist plan as a wiki card so it appears in the project knowledge
	// base, carries task metadata in Data, and can be dispatched to any
	// agent. Skipped gracefully when the project actor is unavailable
	// (e.g., tests without a parent project actor).
	a.plan.PlanCardID = a.writePlanCard(ctx, requestID)

	// When a goal is active, link this plan card as supporting evidence.
	// Multiple plans can accumulate during a single goal (initial plan,
	// revised plans after discoveries, etc.) — append, never overwrite.
	// Only link successfully persisted cards and avoid duplicates from
	// repeated submissions of the same plan.
	if a.RawSession.Goal != nil && a.plan.PlanCardID != "" {
		exists := false
		for _, id := range a.RawSession.Goal.PlanCardIDs {
			if id == a.plan.PlanCardID {
				exists = true
				break
			}
		}
		if !exists {
			a.RawSession.Goal.PlanCardIDs = append(a.RawSession.Goal.PlanCardIDs, a.plan.PlanCardID)
		}
	}

	a.plan.Status = "pending_approval"
	a.invalidateComponentSnapshot(ctx)
	a.planApprovalPending = true
	// Persist immediately: the turn pauses for plan approval after this. If
	// the program crashes during the pause, the goal's PlanCardIDs and the
	// pending plan state must survive so the goal reviewer can re-read the
	// plan evidence after restart.
	a.saveMailbox(ctx)
	return requestID, nil
}

// handleWorkflowPlanSubmit is the registered callable for direct (non-LLM)
// invocation. It persists a plan card, creates a workflow map, and returns the
// card IDs. It does NOT create session-level tasks or enter the plan approval
// flow — the workflow map's task cards are authored separately via
// project.wiki_create_task_card.
func (a *Actor) handleWorkflowPlanSubmit(ctx actor.Context, req gen.WorkflowPlanSubmitReq) (gen.WorkflowPlanSubmitResp, error) {
	planCardID, mapCardID, err := a.createWorkflowPlanArtifacts(ctx, req)
	if err != nil {
		return gen.WorkflowPlanSubmitResp{}, err
	}
	a.takeSnapshot()
	return gen.WorkflowPlanSubmitResp{PlanCardID: planCardID, MapCardID: mapCardID}, nil
}

// createWorkflowPlanArtifacts persists a plan card and creates a workflow map
// from the plan body. Shared by the direct handler and the turn-engine
// interception path. Does NOT call createPlanTasks or enter the plan approval
// flow — this is the workflow-specific entry that avoids session-level task
// generation entirely.
func (a *Actor) createWorkflowPlanArtifacts(ctx actor.Context, req gen.WorkflowPlanSubmitReq) (planCardID, mapCardID string, err error) {
	if a.plan.Status == "pending_approval" {
		return "", "", fmt.Errorf("workflow_plan_submit: plan approval pending (requestId=%s); await resolution first", a.plan.RequestID)
	}
	if a.workflowActive() {
		return "", "", fmt.Errorf("workflow_plan_submit: workflow %q is already active", a.RawSession.ActiveWorkflow.MapCardID)
	}
	a.plan.Title = strings.TrimSpace(req.Title)
	if a.plan.Title == "" {
		return "", "", fmt.Errorf("workflow_plan_submit: Title is required")
	}
	if req.Body == "" {
		return "", "", fmt.Errorf("workflow_plan_submit: Body is required")
	}
	a.plan.Plan = req.Body
	// Workflow plan never carries inline tasks; clear any stale task refs so the
	// plan card and map are clean.
	a.plan.PendingTasks = nil
	a.plan.ApprovedTasks = nil

	requestID := ctx.NewID().String()
	a.plan.RequestID = requestID

	// Persist plan card in the same format as plan_submit. The card carries
	// no task metadata — workflow task cards are authored separately.
	planCardID = a.writePlanCard(ctx, requestID)
	a.plan.PlanCardID = planCardID

	// Create the workflow map. Destination is extracted from a "## Goal"
	// heading in the plan body; fall back to the title when absent.
	candidateMapID := "workflow-" + a.derivePlanCardID(requestID)
	destination := planGoalText(a.plan.Plan)
	if destination == "" {
		destination = a.planCardTitle()
	}
	notes := a.plan.Plan
	if planCardID != "" {
		notes = "**Plan:** [[" + planCardID + "]]\n\n" + notes
	}
	mapCardID, err = a.callWikiCreateMap(ctx, candidateMapID, destination, notes)
	if err != nil {
		return planCardID, "", fmt.Errorf("workflow_plan_submit: create workflow map failed: %w", err)
	}
	a.invalidateComponentSnapshot(ctx)
	a.saveMailbox(ctx)
	return planCardID, mapCardID, nil
}

// applyWorkflowPlanSubmit is the turn-engine interception path for
// workflow_plan_submit. It creates the plan card and workflow map, then enters
// the two-phase workflow_start confirmation by calling applyWorkflowStart.
// Returns the confirmation request ID so the turn engine can track the pending
// interaction. Never creates session-level tasks.
func (a *Actor) applyWorkflowPlanSubmit(ctx actor.Context, req gen.WorkflowPlanSubmitReq) (string, error) {
	_, mapCardID, err := a.createWorkflowPlanArtifacts(ctx, req)
	if err != nil {
		return "", err
	}
	// Enter the two-phase workflow_start confirmation with the newly created
	// map. This stores PendingWorkflowStart and returns a request ID for the
	// confirmation interaction.
	startReqID, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: mapCardID})
	if err != nil {
		return "", fmt.Errorf("workflow_plan_submit: enter workflow confirmation failed: %w", err)
	}
	// Propagate the submitter's coding intent onto the pending record so
	// resolveWorkflowStart → activateWorkflow can read it. NonCoding=false
	// and absence are equivalent (auto-infer), so we only set when the
	// caller asked explicitly — this also keeps the persisted payload
	// compact (no false → absent round-trip).
	if req.NonCoding && a.RawSession.PendingWorkflowStart != nil {
		a.RawSession.PendingWorkflowStart.NonCoding = true
		a.saveMailbox(ctx)
	}
	return startReqID, nil
}

// writePlanCard creates a wiki card to persist the current plan state.
// Returns the card ID on success, or "" when the project actor is unavailable
// (no parent). Errors are logged but non-fatal — the in-memory plan is still
// authoritative for hot-context injection.
func (a *Actor) writePlanCard(ctx actor.Context, requestID string) string {
	cardID := a.derivePlanCardID(requestID)
	raw := a.formatPlanCardRaw()
	if err := a.callWikiCreateCard(ctx, cardID, raw); err != nil {
		ctx.Logger().Warn("agent: plan card create failed, trying update", "error", err, "cardID", cardID)
		// Card may already exist from a previous submission with same ID — try edit.
		if err := a.callWikiEditCard(ctx, cardID, raw); err != nil {
			ctx.Logger().Warn("agent: plan card edit also failed", "error", err, "cardID", cardID)
			return ""
		}
	}
	return cardID
}

// updatePlanCard overwrites the plan wiki card with current plan state.
// Used when the user edits the plan during approval or when status changes.
// Silently no-ops when no card was originally created.
func (a *Actor) updatePlanCard(ctx actor.Context) {
	if a.plan.PlanCardID == "" {
		return
	}
	raw := a.formatPlanCardRaw()
	if err := a.callWikiEditCard(ctx, a.plan.PlanCardID, raw); err != nil {
		ctx.Logger().Warn("agent: plan card edit failed", "error", err, "cardID", a.plan.PlanCardID)
	}
}

// derivePlanCardID generates a meaningful, filesystem-safe card ID from the
// plan content. The ID is a slug of the plan title plus a short unique suffix
// from the requestID to avoid collisions.
func (a *Actor) derivePlanCardID(requestID string) string {
	title := a.planCardTitle()
	slug := slugifyPlanTitle(title)
	if slug == "" {
		slug = "untitled"
	}
	// Short unique suffix from requestID for collision avoidance.
	suffix := requestID
	if len(suffix) > 6 {
		suffix = suffix[len(suffix)-6:]
	}
	return "plan-" + slug + "-" + suffix
}

// planCardTitle returns the explicit title supplied with plan_submit. Older
// persisted plans may not have one, so they use a stable generic fallback.
func (a *Actor) planCardTitle() string {
	if title := strings.TrimSpace(a.plan.Title); title != "" {
		return title
	}
	return "Untitled Plan"
}

// slugifyPlanTitle converts a title string into a lowercase, filesystem-safe
// slug suitable for use as a card ID. Non-alphanumeric characters are replaced
// with hyphens; consecutive hyphens are collapsed; the result is truncated.
func slugifyPlanTitle(title string) string {
	slug := strings.ToLower(strings.TrimSpace(title))
	var sb strings.Builder
	sb.Grow(len(slug))
	prevDash := true // suppress leading dash
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			sb.WriteByte('-')
			prevDash = true
		}
	}
	result := strings.Trim(sb.String(), "-")
	if len(result) > 40 {
		result = result[:40]
	}
	return result
}

// formatPlanCardRaw renders the YAML frontmatter + markdown body for the plan
// wiki card from the current planState.
func (a *Actor) formatPlanCardRaw() string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "id: %s\n", a.planCardTitle())
	sb.WriteString("tags: [plan]\n")
	fmt.Fprintf(&sb, "status: %s\n", a.plan.Status)
	sb.WriteString("data:\n")
	sb.WriteString("  type: plan\n")
	sb.WriteString("  storage: runtime\n")
	sb.WriteString("  visibility: runtime\n")
	fmt.Fprintf(&sb, "  requestId: %s\n", a.plan.RequestID)
	fmt.Fprintf(&sb, "  agentId: %s\n", a.actorID)
	fmt.Fprintf(&sb, "  status: %s\n", a.plan.Status)
	if a.plan.Policy != nil {
		fmt.Fprintf(&sb, "  mode: %s\n", a.plan.Policy.Mode)
	}
	// tasks as JSON string for reliable round-tripping through the card
	// frontmatter parser.
	tasksJSON := a.planTasksJSON()
	if tasksJSON != "" {
		fmt.Fprintf(&sb, "  tasksJson: '%s'\n", tasksJSON)
	}
	sb.WriteString("---\n\n")
	sb.WriteString(a.plan.Plan)
	return sb.String()
}

// planTasksJSON serialises pending + approved tasks as a compact JSON string
// for storage in the card Data field.
func (a *Actor) planTasksJSON() string {
	type cardTask struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
		Status  string `json:"status"`
	}
	var tasks []cardTask
	for _, t := range a.plan.PendingTasks {
		tasks = append(tasks, cardTask{ID: t.ID, Subject: t.Subject, Status: t.Status})
	}
	for _, t := range a.plan.ApprovedTasks {
		tasks = append(tasks, cardTask{ID: t.ID, Subject: t.Subject, Status: t.Status})
	}
	if len(tasks) == 0 {
		return ""
	}
	b, err := json.Marshal(tasks)
	if err != nil {
		return ""
	}
	return string(b)
}

// callWikiCreateCard calls project.wiki.create_card on the parent project actor.
func (a *Actor) callWikiCreateCard(ctx actor.Context, cardID, raw string) error {
	parent := ctx.Parent()
	if parent == nil {
		return fmt.Errorf("no parent project actor")
	}
	planner := ctx.Planner()
	if planner == nil {
		return fmt.Errorf("no planner")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	_, err := planner.Call(callCtx, parent, "project.wiki_create_card",
		domain.WikiCreateCardReq{ID: cardID, Raw: raw}).Await()
	return err
}

// callWikiEditCard calls project.wiki.edit_card on the parent project actor.
func (a *Actor) callWikiEditCard(ctx actor.Context, cardID, raw string) error {
	parent := ctx.Parent()
	if parent == nil {
		return fmt.Errorf("no parent project actor")
	}
	planner := ctx.Planner()
	if planner == nil {
		return fmt.Errorf("no planner")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	_, err := planner.Call(callCtx, parent, "project.wiki_edit_card",
		domain.WikiEditCardReq{ID: cardID, Raw: raw}).Await()
	return err
}

// callWikiCreateMap calls project.wiki_create_map on the parent project actor.
func (a *Actor) callWikiCreateMap(ctx actor.Context, mapID, destination, notes string) (string, error) {
	parent := ctx.Parent()
	if parent == nil {
		return "", fmt.Errorf("no parent project actor")
	}
	planner := ctx.Planner()
	if planner == nil {
		return "", fmt.Errorf("no planner")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, parent, "project.wiki_create_map",
		domain.WikiCreateMapReq{ID: mapID, Destination: destination, Notes: notes}).Await()
	if err != nil {
		return "", err
	}
	resp, ok := result.(domain.WikiCreateMapResp)
	if !ok {
		return "", fmt.Errorf("wiki_create_map returned unexpected type %T", result)
	}
	return resp.Card.ID, nil
}

// callWikiSetStatus calls project.wiki.set_status on the parent project actor.
// expected, when non-empty, CAS-guards the transition: a stale write (e.g. a
// worker setting pending_review after a racing approve already set done) fails
// instead of silently rolling the card back.
func (a *Actor) callWikiSetStatus(ctx actor.Context, cardID, status, expected string, evidence []string) error {
	parent := ctx.Parent()
	if parent == nil {
		return fmt.Errorf("no parent project actor")
	}
	planner := ctx.Planner()
	if planner == nil {
		return fmt.Errorf("no planner")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	_, err := planner.Call(callCtx, parent, "project.wiki_set_status",
		domain.WikiSetStatusReq{ID: cardID, Status: status, ExpectedStatus: expected, Evidence: evidence}).Await()
	return err
}

// triggerReviewChangesetFreeze tells the parent project actor to kick off async
// changeset generation. Fire-and-forget: the freeze handler returns immediately
// (it only registers a preparing placeholder and launches a goroutine), so we
// use a short timeout. Failure is non-fatal — the parent can still read the
// changeset later via review_changeset (which will trigger freeze on demand).
//
// Skip when this agent is not bound to a workflow worktree: a no-worktree
// worker writes directly into the main repo (no isolated child branch), so
// there is no changeset to freeze and no preparing placeholder to write. The
// ready_for_review path still proceeds — the parent reviewer inspects the
// card body / assessment instead of a per-branch diff. The project-side
// handleReviewChangesetFreeze applies the same skip as depth-in-defense.
func (a *Actor) triggerReviewChangesetFreeze(ctx actor.Context) {
	if a.activeWorkflowWorktreeID() == "" {
		ctx.Logger().Debug("agent: skip review changeset freeze (no workflow worktree bound)", "agentID", a.actorID)
		return
	}
	parent := ctx.Parent()
	if parent == nil {
		return
	}
	planner := ctx.Planner()
	if planner == nil {
		return
	}
	// Pass the bound task card ID so the project actor can project the
	// review changeset summary into the card's custom data.
	taskCardID := ""
	if a.RawSession.Goal != nil {
		taskCardID = a.RawSession.Goal.BoundTaskCardID
	}
	freezeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := planner.Call(freezeCtx, parent, "project.review_changeset_freeze",
		gen.ProjectReviewChangesetReq{
			AgentActorID: a.actorID,
			TaskCardID:   taskCardID,
		}).Await()
	if err != nil {
		ctx.Logger().Warn("agent: trigger review changeset freeze failed", "error", err, "agentID", a.actorID)
	}
}

// checkWorktreeClean reports whether the worktree bound to this agent is free
// of uncommitted changes, as seen by the parent project actor. It guards a
// worker's ready_for_review declaration: the parent reviewer inspects the
// committed changeset, so a dirty worktree must be bounced back before the
// agent pauses for review.
//
// Fail-open: no parent project actor, no planner, an invoke failure, or an
// unexpected response type all log a Warning and return clean=true — a
// missing or unreachable worktree must never block a review. The project
// handler itself treats "no worktree bound to this agent" as clean, so that
// case flows through as clean here as well.
func (a *Actor) checkWorktreeClean(ctx actor.Context) (clean bool, dirtyFiles []string) {
	parent := ctx.Parent()
	if parent == nil {
		ctx.Logger().Warn("agent: worktree clean check skipped, no parent project actor", "agentID", a.actorID)
		return true, nil
	}
	planner := ctx.Planner()
	if planner == nil {
		ctx.Logger().Warn("agent: worktree clean check skipped, no planner", "agentID", a.actorID)
		return true, nil
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, parent, "project.workflow_worktree_clean_check",
		gen.ProjectWorktreeCleanCheckReq{AgentActorID: a.actorID}).Await()
	if err != nil {
		ctx.Logger().Warn("agent: worktree clean check failed, fail-open", "error", err, "agentID", a.actorID)
		return true, nil
	}
	resp, ok := result.(gen.ProjectWorktreeCleanCheckResp)
	if !ok {
		ctx.Logger().Warn("agent: worktree clean check returned unexpected type, fail-open", "type", fmt.Sprintf("%T", result), "agentID", a.actorID)
		return true, nil
	}
	if resp.Clean {
		return true, nil
	}
	return false, resp.DirtyFiles
}

func (a *Actor) emitPlanApprovalEvent(ctx actor.Context, turnID, requestID string) {
	payload := a.buildPlanApprovalPayload()
	if payload == nil {
		return
	}
	payload.TurnID = turnID
	payload.RequestID = requestID
	// Emit as step.interaction_requested — frontend renders a plan approval card.
	stepID := turnID + "-plan-" + requestID
	taskMaps := make([]map[string]any, len(payload.Tasks))
	for i, t := range payload.Tasks {
		taskMaps[i] = map[string]any{
			"id":         t.ID,
			"subject":    t.Subject,
			"status":     t.Status,
			"activeForm": t.ActiveForm,
		}
	}
	payloadMap := map[string]any{
		"plan":       payload.Plan,
		"tasks":      taskMaps,
		"policy":     payload.Policy,
		"editable":   true,
		"goalActive": a.RawSession.Goal != nil,
	}
	ev := domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "plan_approval",
		RequestID:       requestID,
		Task:            payloadMap,
		Seq:             a.allocSeq(),
	}
	a.applyStepEvent(ev)
	// Record the pending interaction before advertising the step so restart
	// recovery cannot downgrade this confirmation to a generic pause.
	a.setPendingInteraction(ctx, turnID, stepID, requestID, "plan_approval", payloadMap)
	_ = a.emitActorStepEvent(ctx, ev)
	// Refresh atomic snapshot so session.summary (browser refresh / reconnect)
	// sees the plan_approval interaction step. emitActorStepEvent bypasses
	// turnEngine.stepEvents, so flushEvents' onAfterFlush won't fire here.
	a.takeSnapshot()
}

// buildPlanApprovalPayload returns the current pending plan as a payload, or
// nil when no plan is awaiting approval. Used both for the live event and for
// the TurnStatus snapshot so a browser refresh can rebuild the approval card.
func (a *Actor) buildPlanApprovalPayload() *domain.TurnPlanApprovalRequestedPayload {
	if a.plan.Status != "pending_approval" {
		return nil
	}
	policy := &gen.TurnPlanPolicy{Mode: "default"}
	if a.plan.Policy != nil {
		policy.Mode = a.plan.Policy.Mode
		for _, p := range a.plan.Policy.AllowedPrompts {
			policy.AllowedPrompts = append(policy.AllowedPrompts, gen.PlanAllowedPrompt{Tool: p.Tool, Prompt: p.Prompt})
		}
	}
	tasks := make([]gen.TurnTask, len(a.plan.PendingTasks))
	for i, t := range a.plan.PendingTasks {
		tasks[i] = gen.TurnTask{
			ID:         t.ID,
			Subject:    t.Subject,
			Status:     t.Status,
			ActiveForm: t.ActiveForm,
		}
	}
	return &domain.TurnPlanApprovalRequestedPayload{
		RequestID: a.plan.RequestID,
		Plan:      a.plan.Plan,
		Tasks:     tasks,
		Policy:    policy,
		Editable:  true,
	}
}

// ── task list ──

func (a *Actor) createPlanTasks(ctx actor.Context) []gen.Task {
	if len(a.plan.PendingTasks) == 0 {
		a.plan.ApprovedTasks = nil
		return nil
	}
	created := make([]gen.Task, 0, len(a.plan.PendingTasks))
	for _, t := range a.plan.PendingTasks {
		taskID := t.ID
		if taskID == "" {
			taskID = ctx.NewID().String()
		}
		// Avoid duplicate task IDs: a plan may reference a task that already
		// exists in RawSession.Tasks (e.g. carried over from a previous turn).
		// Update the existing entry in place so the frontend sees a consistent
		// task list instead of a new "created" duplicate.
		existingIdx := -1
		for i := range a.RawSession.Tasks {
			if a.RawSession.Tasks[i].ID == taskID {
				existingIdx = i
				break
			}
		}
		if existingIdx >= 0 {
			a.RawSession.Tasks[existingIdx] = gen.TurnTask{
				ID:         taskID,
				Subject:    t.Subject,
				Status:     t.Status,
				ActiveForm: t.ActiveForm,
			}
		} else {
			a.RawSession.Tasks = append(a.RawSession.Tasks, gen.TurnTask{
				ID:         taskID,
				Subject:    t.Subject,
				Status:     t.Status,
				ActiveForm: t.ActiveForm,
			})
		}
		created = append(created, gen.Task{
			ID:         taskID,
			Subject:    t.Subject,
			Status:     t.Status,
			ActiveForm: t.ActiveForm,
		})
	}
	if err := a.emitTaskCreateEvent(ctx, created); err != nil {
		ctx.Logger().Warn("agent: failed to emit task_create event", "error", err)
	}
	a.plan.PendingTasks = nil
	a.plan.ApprovedTasks = created
	return created
}

// resolvePlanApproval processes the user's plan approval decision. It mutates
// plan state, creates tasks on approval, and emits the interaction_resolved
// step event. Runs on the agent.exec loop (called from applyPlanApprovalAnswer)
// so all plan state changes happen on the same goroutine as the turn engine —
// no cross-loop race with task reads in hot-context compilation.
func (a *Actor) resolvePlanApproval(ctx actor.Context, answer string) (decision string, tasks []gen.Task, requestID string, feedback string) {
	var ans struct {
		Decision       string          `json:"decision"`
		EditedPlan     string          `json:"editedPlan"`
		Feedback       string          `json:"feedback"`
		SelectedPolicy *gen.PlanPolicy `json:"selectedPolicy"`
	}
	if err := json.Unmarshal([]byte(answer), &ans); err != nil {
		ctx.Logger().Warn("agent: failed to unmarshal plan approval answer", "error", err, "answer", answer)
	}
	ctx.Logger().Info("agent: resolving plan approval", "decision", ans.Decision, "feedback", ans.Feedback, "planRequestID", a.plan.RequestID)

	decision = ans.Decision
	requestID = a.plan.RequestID
	feedback = ans.Feedback

	switch ans.Decision {
	case "approve", "edit", "confirm_goal", "start_workflow":
		a.plan.Status = "approved"
		a.invalidateComponentSnapshot(ctx)
		if ans.EditedPlan != "" {
			a.plan.Plan = ans.EditedPlan
		}
		if ans.SelectedPolicy != nil {
			a.plan.Policy = ans.SelectedPolicy
		}
		if ans.Decision == "start_workflow" {
			// Skip createPlanTasks: in workflow mode the agent is the
			// orchestrator — work items become map task cards (created by the
			// agent from the plan's PendingTasks on its next turn), not
			// session-level plan tasks. Keep PendingTasks so the agent can
			// read them when building the task-card tree.
			a.updatePlanCard(ctx)
			a.activateWorkflowFromPlan(ctx)
		} else {
			tasks = a.createPlanTasks(ctx)
			// Sync the card after state mutations (plan text, status, tasks).
			a.updatePlanCard(ctx)
			if ans.Decision == "confirm_goal" {
				a.activateGoalFromPlan(ctx)
			}
		}
	case "reject":
		a.plan.Status = "rejected"
		a.invalidateComponentSnapshot(ctx)
		a.plan.PendingTasks = nil
		a.plan.ApprovedTasks = nil
		a.updatePlanCard(ctx)
	}
	a.planApprovalPending = false
	a.takeSnapshot()
	a.notifyWorkspaceStatus(ctx)
	return decision, tasks, requestID, feedback
}

// activateGoalFromPlan converts the approved plan into an active goal. It is
// called when the user picks "confirm_goal" on the plan approval card. The
// plan body becomes both the goal Condition and InterpretedGoal, goal mode is
// mounted, and the goal is set Confirmed+active so the agent enters autonomous
// execution on the next turn. If a goal is already active the call is a no-op:
// the plan is still approved (tasks created) but the existing goal is
// preserved.
func (a *Actor) activateGoalFromPlan(ctx actor.Context) {
	if a.RawSession.Goal != nil {
		ctx.Logger().Info("agent: confirm_goal skipped — goal already active", "condition", a.RawSession.Goal.Condition)
		return
	}
	maxTurns := DefaultGoalMaxTurns
	if cfg := a.fetchAgentKindConfig(ctx); cfg.MaxTurns > 0 {
		maxTurns = cfg.MaxTurns
	}
	goalText := a.plan.Plan
	a.RawSession.Goal = &gen.SessionGoal{
		Condition:       goalText,
		MaxTurns:        maxTurns,
		TurnCount:       0,
		InterpretedGoal: goalText,
		Confirmed:       true,
		Status:          "active",
	}
	if a.plan.PlanCardID != "" {
		a.RawSession.Goal.PlanCardIDs = []string{a.plan.PlanCardID}
	}
	a.ensureGoalCardMounted(ctx)
	a.invalidateComponentSnapshot(ctx)
	a.applyGoalTitle(ctx, goalText, goalText)
	ctx.Logger().Info("agent: plan confirmed as goal", "planCardID", a.plan.PlanCardID)
}

// activateWorkflowFromPlan converts the approved plan into an active workflow.
// It creates a workflow map card from the plan title and body, then activates
// workflow mode so the agent enters the workflow orchestration loop on its next
// turn. Called when the user picks "start_workflow" on the plan approval card.
func planGoalText(markdown string) string {
	lines := strings.Split(markdown, "\n")
	start := -1
	level := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		headingLevel := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		if headingLevel == 0 || headingLevel >= len(trimmed) || trimmed[headingLevel] != ' ' || !strings.EqualFold(strings.TrimSpace(trimmed[headingLevel:]), "goal") {
			continue
		}
		start, level = i+1, headingLevel
		break
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		headingLevel := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		if headingLevel > 0 && headingLevel < len(trimmed) && trimmed[headingLevel] == ' ' && headingLevel <= level {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func (a *Actor) activateWorkflowFromPlan(ctx actor.Context) {
	if a.workflowActive() {
		ctx.Logger().Info("agent: start_workflow skipped — workflow already active", "mapCardID", a.RawSession.ActiveWorkflow.MapCardID)
		return
	}
	mapID := "workflow-" + a.derivePlanCardID(a.plan.RequestID)
	destination := planGoalText(a.plan.Plan)
	if destination == "" {
		destination = a.planCardTitle()
	}
	notes := a.plan.Plan
	if a.plan.PlanCardID != "" {
		notes = "**Plan:** [[" + a.plan.PlanCardID + "]]\n\n" + notes
	}
	resolvedID, err := a.callWikiCreateMap(ctx, mapID, destination, notes)
	if err != nil {
		ctx.Logger().Warn("agent: failed to create workflow map from plan", "error", err, "mapID", mapID)
		return
	}
	if err := a.activateWorkflow(ctx, resolvedID, false); err != nil {
		ctx.Logger().Warn("agent: workflow activation from plan failed", "error", err, "mapID", resolvedID)
		return
	}
	ctx.Logger().Info("agent: plan converted to workflow", "mapCardID", resolvedID, "planCardID", a.plan.PlanCardID)
}

// expirePlanApproval drops a pending plan to "rejected" state without
// emitting any step event. Called by the turn engine when the turn is
// cancelled while executeWaitPlanApproval is blocked on resumeCh — the
// cancel path's writeCancelledToolResults already closes the plan.submit
// step, so emitting interaction_resolved here would race the closure.
// Caller passes the requestID held by the engine (e.resumeRequestID);
// we guard against matching a stale/empty state to avoid clobbering a
// later plan that may have been submitted after this turn ended.
func (a *Actor) expirePlanApproval(ctx actor.Context, requestID string) {
	if a.plan.Status != "pending_approval" {
		return
	}
	if requestID != "" && a.plan.RequestID != requestID {
		return
	}
	a.plan.Status = "rejected"
	a.invalidateComponentSnapshot(ctx)
	a.plan.PendingTasks = nil
	a.planApprovalPending = false
	a.takeSnapshot()
	ctx.Logger().Info("agent: plan approval expired (turn cancelled)", "requestID", a.plan.RequestID)
}

// archiveTask records a finished task in RawSession.TaskHistory.
func (a *Actor) archiveTask(t gen.TurnTask) {
	if a.RawSession.TaskHistory == nil {
		a.RawSession.TaskHistory = make(map[string]gen.TurnTask)
	}
	a.RawSession.TaskHistory[t.ID] = t
}

// sweepFinishedTasks moves completed and cancelled tasks out of the active
// list (turn-complete path) into TaskHistory.
func (a *Actor) sweepFinishedTasks() {
	var active []gen.TurnTask
	for _, t := range a.RawSession.Tasks {
		if t.Status == "completed" || t.Status == "cancelled" {
			a.archiveTask(t)
			continue
		}
		active = append(active, t)
	}
	a.RawSession.Tasks = active
}

// sweepCompletedTasks moves completed tasks out of the active list
// (turn-cancel path keeps cancelled tasks active) into TaskHistory.
func (a *Actor) sweepCompletedTasks() {
	var active []gen.TurnTask
	for _, t := range a.RawSession.Tasks {
		if t.Status == "completed" {
			a.archiveTask(t)
			continue
		}
		active = append(active, t)
	}
	a.RawSession.Tasks = active
}

func (a *Actor) handleTaskCreate(ctx actor.Context, req domain.AgentTaskCreateReq) (domain.AgentTaskCreateResp, error) {
	defer a.takeSnapshot()
	if strings.TrimSpace(req.Subject) == "" {
		return domain.AgentTaskCreateResp{}, fmt.Errorf("create_task requires a non-empty Subject; example: {\"Subject\":\"Add retry logic\"}")
	}
	taskID := ctx.NewID().String()
	a.RawSession.Tasks = append(a.RawSession.Tasks, gen.TurnTask{
		ID:         taskID,
		Subject:    req.Subject,
		Status:     "pending",
		ActiveForm: req.ActiveForm,
	})
	ctx.Logger().Info("agent: task created", "id", taskID, "subject", req.Subject)

	created := a.RawSession.Tasks[len(a.RawSession.Tasks)-1]
	if err := a.emitTaskCreateEvent(ctx, []gen.Task{{
		ID:         created.ID,
		Subject:    created.Subject,
		Status:     created.Status,
		ActiveForm: created.ActiveForm,
	}}); err != nil {
		ctx.Logger().Warn("agent: failed to emit task_create event", "error", err)
	}
	a.notifyWorkspaceStatus(ctx)

	return domain.AgentTaskCreateResp{Task: gen.Task{
		ID:         created.ID,
		Subject:    created.Subject,
		Status:     created.Status,
		ActiveForm: created.ActiveForm,
	}}, nil
}

func (a *Actor) handleTaskUpdate(ctx actor.Context, req domain.AgentTaskUpdateReq) (domain.AgentTaskUpdateResp, error) {
	defer a.takeSnapshot()
	var found *gen.TurnTask
	for i := range a.RawSession.Tasks {
		if a.RawSession.Tasks[i].ID == req.ID {
			a.RawSession.Tasks[i].Status = req.Status
			found = &a.RawSession.Tasks[i]
			break
		}
	}
	if found == nil {
		// Fall back to TaskHistory: tasks swept at turn completion stay
		// addressable so a replayed update id resolves instead of failing
		// "task not found".
		if archived, ok := a.RawSession.TaskHistory[req.ID]; ok {
			archived.Status = req.Status
			if req.Status == "completed" || req.Status == "cancelled" {
				a.RawSession.TaskHistory[req.ID] = archived
			} else {
				// Reactivated: move it back into the active list.
				delete(a.RawSession.TaskHistory, req.ID)
				a.RawSession.Tasks = append(a.RawSession.Tasks, archived)
			}
			found = &archived
		}
	}
	if found == nil {
		return domain.AgentTaskUpdateResp{}, fmt.Errorf("task not found: %s", req.ID)
	}
	ctx.Logger().Info("agent: task updated", "id", req.ID, "status", req.Status)

	if err := a.emitTaskUpdateEvent(ctx, *found); err != nil {
		ctx.Logger().Warn("agent: failed to emit task_update event", "error", err)
	}
	// When every task in the active plan is completed, the plan is done —
	// drop it from hot context by transitioning to "completed". The plan
	// card is kept as an audit trail in the knowledge base.
	if a.plan.Status == "approved" && a.allPlanTasksComplete() {
		a.plan.Status = "completed"
		a.invalidateComponentSnapshot(ctx)
	}
	// Sync the plan card so task statuses are current.
	a.updatePlanCard(ctx)
	a.notifyWorkspaceStatus(ctx)

	return domain.AgentTaskUpdateResp{Task: gen.Task{
		ID:         found.ID,
		Subject:    found.Subject,
		Status:     found.Status,
		ActiveForm: found.ActiveForm,
	}}, nil
}

// allPlanTasksComplete reports whether every task created by the active plan
// has reached "completed" or "cancelled" status (or been deleted). Returns
// false when the plan has no approved tasks. Only tasks whose IDs match
// plan.ApprovedTasks are checked, so leftover tasks from previous plans or
// manual creation do not block plan completion.
func (a *Actor) allPlanTasksComplete() bool {
	if len(a.plan.ApprovedTasks) == 0 {
		return false
	}
	approvedIDs := make(map[string]struct{}, len(a.plan.ApprovedTasks))
	for _, t := range a.plan.ApprovedTasks {
		approvedIDs[t.ID] = struct{}{}
	}
	for _, t := range a.RawSession.Tasks {
		if _, ok := approvedIDs[t.ID]; ok && t.Status != "completed" && t.Status != "cancelled" {
			return false
		}
	}
	return true
}

func (a *Actor) handleTaskList(_ actor.PureContext) (domain.AgentTaskListResp, error) {
	snap := a.snapshot.Load()
	if snap == nil || len(snap.tasks) == 0 {
		return domain.AgentTaskListResp{Tasks: nil}, nil
	}
	tasks := make([]gen.Task, len(snap.tasks))
	for i, t := range snap.tasks {
		tasks[i] = gen.Task{ID: t.ID, Subject: t.Subject, Status: t.Status, ActiveForm: t.ActiveForm}
	}
	return domain.AgentTaskListResp{Tasks: tasks}, nil
}

func (a *Actor) handleTaskDelete(ctx actor.Context, req domain.AgentTaskDeleteReq) error {
	defer a.takeSnapshot()
	for i, t := range a.RawSession.Tasks {
		if t.ID == req.ID {
			a.RawSession.Tasks = append(a.RawSession.Tasks[:i], a.RawSession.Tasks[i+1:]...)
			ctx.Logger().Info("agent: task deleted", "id", req.ID)
			if err := a.emitTaskDeleteEvent(ctx, req.ID); err != nil {
				ctx.Logger().Warn("agent: failed to emit task_delete event", "error", err)
			}
			a.notifyWorkspaceStatus(ctx)
			return nil
		}
	}
	if _, ok := a.RawSession.TaskHistory[req.ID]; ok {
		delete(a.RawSession.TaskHistory, req.ID)
		ctx.Logger().Info("agent: archived task deleted", "id", req.ID)
		if err := a.emitTaskDeleteEvent(ctx, req.ID); err != nil {
			ctx.Logger().Warn("agent: failed to emit task_delete event", "error", err)
		}
		a.notifyWorkspaceStatus(ctx)
		return nil
	}
	return fmt.Errorf("task not found: %s", req.ID)
}

func (a *Actor) handleTaskCancel(ctx actor.Context, req domain.AgentTaskCancelReq) (domain.AgentTaskCancelResp, error) {
	defer a.takeSnapshot()
	var found *gen.TurnTask
	for i := range a.RawSession.Tasks {
		if a.RawSession.Tasks[i].ID == req.ID {
			a.RawSession.Tasks[i].Status = "cancelled"
			found = &a.RawSession.Tasks[i]
			break
		}
	}
	if found == nil {
		if archived, ok := a.RawSession.TaskHistory[req.ID]; ok {
			archived.Status = "cancelled"
			a.RawSession.TaskHistory[req.ID] = archived
			found = &archived
		}
	}
	if found == nil {
		return domain.AgentTaskCancelResp{}, fmt.Errorf("task not found: %s", req.ID)
	}
	ctx.Logger().Info("agent: task cancelled", "id", req.ID)
	if err := a.emitTaskUpdateEvent(ctx, *found); err != nil {
		ctx.Logger().Warn("agent: failed to emit task_cancel event", "error", err)
	}
	a.notifyWorkspaceStatus(ctx)
	return domain.AgentTaskCancelResp{Task: gen.Task{
		ID:         found.ID,
		Subject:    found.Subject,
		Status:     found.Status,
		ActiveForm: found.ActiveForm,
	}}, nil
}

// buildGoalBlock formats the active goal as a ContentBlock for HotContext.
// Returns nil when no goal is set. Placed before task board so the model
// sees title before decomposition.
//
// Goal status flow:
//   - "" / "active":  normal working state; LLM continues toward the goal.
//   - "completed":    LLM called turn.assess with complete_candidate; the goal
//     state is cleared (Goal = nil) and the goal card is unmounted.
//   - "exhausted":    turn budget reached without completion.

func (a *Actor) buildGoalBlock(ctx actor.Context) *domain.ContentBlock {
	if a.RawSession.Goal == nil {
		return nil
	}
	if a.RawSession.Goal.BoundTaskCardID != "" {
		return a.buildBoundTaskGoalBlock(ctx)
	}
	condition := a.goalCondition(ctx)
	if condition == "" {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("## Active Goal\n\n")
	sb.WriteString(condition)
	sb.WriteString("\n\n")
	sb.WriteString(fmt.Sprintf("- Turn limit: %d\n", a.RawSession.Goal.MaxTurns))
	sb.WriteString(fmt.Sprintf("- Turns used: %d\n", a.RawSession.Goal.TurnCount))
	if g := a.RawSession.Goal; g.Status != "" {
		sb.WriteString(fmt.Sprintf("- Status: %s\n", g.Status))
	}
	if !a.RawSession.Goal.Confirmed {
		sb.WriteString("\n**The goal is not yet submitted.** Analyze the conversation to fully understand the user's true intent. If the intent matches an existing, actionable task card, first use the project wiki tools to find it, then call `goal_card_submit` with its CardId; if no existing task card matches, call `goal_submit` with your interpretation of the end-state and how the user would verify success, NOT implementation steps. Call exactly one of the two — never both. Do NOT start working on the task until the user confirms.\n")
	} else {
		if assessment := a.latestGoalAssessment(); assessment != nil {
			sb.WriteString("\n")
			sb.WriteString(formatGoalPreviousAssessment(assessment))
		}
		sb.WriteString("\n")
		sb.WriteString(agentkit.GoalCompletionCheck)
		sb.WriteString("\n")
	}
	if a.RawSession.Goal.TurnCount >= a.RawSession.Goal.MaxTurns {
		sb.WriteString("\n⚠️ Turn budget exhausted. Wrap up and report status.\n")
	}
	return &domain.ContentBlock{Type: "text", Text: sb.String()}
}

func (a *Actor) buildBoundTaskGoalBlock(ctx actor.Context) *domain.ContentBlock {
	goal := a.RawSession.Goal
	projectRef, ok := ctx.LookupService("project")
	if !ok || ctx.Planner() == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	cardResult, err := ctx.Planner().Call(callCtx, projectRef, "project.wiki_get_card", gen.WikiGetCardReq{ID: goal.BoundTaskCardID}).Await()
	if err != nil || cardResult == nil {
		return nil
	}
	card, ok := cardResult.(gen.WikiGetCardResp)
	if !ok {
		return nil
	}
	treeResult, err := ctx.Planner().Call(callCtx, projectRef, "project.wiki_get_card_hierarchy", gen.WikiGetCardHierarchyReq{ID: goal.BoundTaskCardID, Level: 32}).Await()
	if err != nil || treeResult == nil {
		return nil
	}
	tree, ok := treeResult.(gen.WikiGetCardHierarchyResp)
	if !ok {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("## Bound Task Card\n\n")
	sb.WriteString(fmt.Sprintf("- Card ID: %s\n", goal.BoundTaskCardID))
	sb.WriteString("- This goal is an existing task card.\n")
	sb.WriteString("\n### Task card body\n\n")
	sb.WriteString(taskCardBody(card.Raw))
	sb.WriteString("\n\n### Card status tree\n\n")
	sb.WriteString(tree.Tree)
	sb.WriteString("\n\n")
	sb.WriteString(fmt.Sprintf("- Turn limit: %d\n", goal.MaxTurns))
	sb.WriteString(fmt.Sprintf("- Turns used: %d\n", goal.TurnCount))
	if goal.Status != "" {
		sb.WriteString(fmt.Sprintf("- Status: %s\n", goal.Status))
	}
	if goal.Confirmed {
		sb.WriteString("\nAfter completing this task card and every necessary child task, call `project.wiki_set_status` to set the current task card to `done` before calling completion assessment.\n")
		if a.hasParentAgent() {
			// A parent agent (map owner) reviews the changeset: pause on
			// ready_for_review instead of self-completing.
			sb.WriteString(agentkit.GoalCompletionCheckBound)
		} else {
			// No parent agent: the user reviews directly in this session, so
			// complete via complete_candidate — completion clears the goal,
			// unbinds the card, and unmounts it from context.
			sb.WriteString(agentkit.GoalCompletionCheck)
		}
		sb.WriteString("\n")
	}
	return &domain.ContentBlock{Type: "text", Text: sb.String()}
}

func (a *Actor) latestGoalAssessment() *gen.TurnAssessment {
	for i := len(a.Session.Turns) - 1; i >= 0; i-- {
		turn := a.Session.Turns[i]
		if turn.Role == "assistant" && turn.Assessment != nil {
			assessment := *turn.Assessment
			assessment.Evidence = append([]string(nil), turn.Assessment.Evidence...)
			return &assessment
		}
	}
	return nil
}

func formatGoalPreviousAssessment(assessment *gen.TurnAssessment) string {
	evidence := "- (none provided)"
	if len(assessment.Evidence) > 0 {
		var b strings.Builder
		for _, item := range assessment.Evidence {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteByte('\n')
		}
		evidence = strings.TrimSuffix(b.String(), "\n")
	}
	return fmt.Sprintf(agentkit.GoalPreviousAssessment,
		assessment.Decision, assessment.Reason, evidence)
}

// LLM can see its own tasks in every turn's hot context. Returns nil when no
// tasks exist. Reads from RawSession.Tasks (the persisted source of truth).

func (a *Actor) buildWorkflowBlock(ctx actor.Context) *domain.ContentBlock {
	if !a.workflowActive() {
		// Workflow mode mounted but no workflow started yet: surface a
		// guidance block (mirroring the unconfirmed goal block) so the LLM
		// establishes the workflow before it begins orchestrating.
		if !a.cardRefEnabled("builtin:mode:workflow") {
			return nil
		}
		var sb strings.Builder
		sb.WriteString("## Active Workflow\n\n")
		sb.WriteString("- No workflow started yet.\n")
		sb.WriteString("- The user's message describes the workflow intent. Analyze it, create or find a map card, then call `workflow_start` with its MapCardId. Do NOT orchestrate tasks until a workflow is active.\n")
		return &domain.ContentBlock{Type: "text", Text: sb.String()}
	}
	mapID := a.RawSession.ActiveWorkflow.MapCardID
	var sb strings.Builder
	sb.WriteString("## Active Workflow\n\n")
	sb.WriteString(fmt.Sprintf("- Map: %s\n", mapID))

	projectRef, ok := ctx.LookupService("project")
	if !ok || ctx.Planner() == nil {
		sb.WriteString("- Progress: project state is temporarily unavailable; inspect the map before acting.\n")
		return &domain.ContentBlock{Type: "text", Text: sb.String()}
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := ctx.Planner().Call(callCtx, projectRef, "project.wiki_get_card", domain.WikiGetCardReq{ID: mapID}).Await()
	if err != nil {
		sb.WriteString("- Progress: unable to read map; inspect it before acting.\n")
		return &domain.ContentBlock{Type: "text", Text: sb.String()}
	}
	if card, ok := result.(domain.WikiGetCardResp); ok {
		status := cardField(card.Raw, "status")
		if status != "" {
			sb.WriteString(fmt.Sprintf("- Map status: %s\n", status))
		}
	}
	frontierResult, err := ctx.Planner().Call(callCtx, projectRef, "project.wiki_frontier", domain.WikiFrontierReq{MapID: mapID}).Await()
	if err != nil {
		sb.WriteString("- Frontier: unavailable; inspect task cards before assigning work.\n")
		return &domain.ContentBlock{Type: "text", Text: sb.String()}
	}
	frontier, ok := frontierResult.(domain.WikiFrontierResp)
	if !ok {
		sb.WriteString("- Frontier: unavailable; inspect task cards before assigning work.\n")
		return &domain.ContentBlock{Type: "text", Text: sb.String()}
	}
	if len(frontier.TaskCards) == 0 {
		sb.WriteString("- Frontier: none. Review current worker results and assess whether the overall goal is met; if not, extend the task tree.\n")
	} else {
		sb.WriteString("- Frontier:\n")
		for _, task := range frontier.TaskCards {
			sb.WriteString(fmt.Sprintf("  - %s (%s)\n", task.ID, task.Status))
		}
		sb.WriteString("- Next: review pending workers, then assign only appropriate frontier tasks.\n")
	}
	return &domain.ContentBlock{Type: "text", Text: sb.String()}
}

// conversableBlockBase is the fixed guidance section of the conversable
// agents hot-context block. It is emitted exactly once per block regardless
// of how many agent-chat cards are mounted; buildConversableBlock appends one
// row per mounted target after it.
const conversableBlockBase = `## Conversable Agents

You have direct conversational channels to the agents listed below ONLY.
- Send messages with workspace.agent_send_message(ToAgentId=...).
- Read their recent steps with workspace.agent_read_message.
- Pause or resume them with workspace.agent_pause / workspace.agent_resume.
- ToAgentId must be one of the listed ids.
- Always read replies after sending.
- Messages wake idle agents and queue on busy ones.`

// browserWindowsBlockBase is the fixed guidance section of the mounted
// browser windows hot-context block. It is emitted exactly once per block
// regardless of how many browser-chat cards are mounted;
// buildBrowserWindowsSection appends one row per mounted window after it.
const browserWindowsBlockBase = `## Mounted Browser Windows

Visible, reusable browser windows are mounted for you. Drive them with the crawl.* callables; pin the window on every call with Config.InstanceID.
- crawl.start begins a task on the window, crawl.status polls it, crawl.results reads the collected pages, crawl.cancel stops it, crawl.handoff hands a login-walled task back to the user.`

// buildConversableBlock injects the conversable (@-tagged) channels and the
// mounted browser windows into hot context: ONE fixed base prompt per
// section regardless of mount count, with one row appended per mounted
// agent-chat:<AgentRef.ID> / browser-chat:<instanceId> card. Returns nil
// when neither kind of mount exists (zero token cost).
//
// Live agent metadata is fetched with a single workspace.list_agents call,
// mirroring buildWorkflowBlock's degrade pattern: on call failure the base
// prompt is still emitted with every row marked (status unavailable). When
// the list resolves, a target that is missing or not loaded is offline and
// is omitted entirely; if no mounted target is online and no browser window
// is mounted the block is dropped (nil) so offline agents never appear in
// hot context. Browser windows are deliberately NOT liveness-gated (the
// frontend owns the mount lifecycle — closing the window unmounts the card):
// a mounted browser-chat:<instanceId> always yields a row.
func (a *Actor) buildConversableBlock(ctx actor.Context) *domain.ContentBlock {
	refs := a.cardRefs
	if refs == nil {
		refs = canonicalCardRefsFromMounts(a.ComponentMounts)
	}
	var targets []string
	var browsers []string
	for _, ref := range refs {
		if ref.Disabled {
			continue
		}
		if instanceID, ok := strings.CutPrefix(ref.ID, browserChatCardPrefix); ok {
			if instanceID != "" {
				browsers = append(browsers, instanceID)
			}
			continue
		}
		if targetID, ok := strings.CutPrefix(ref.ID, agentChatCardPrefix); ok && targetID != "" {
			targets = append(targets, targetID)
		}
	}
	if len(targets) == 0 && len(browsers) == 0 {
		return nil
	}

	var sections []string
	if len(targets) > 0 {
		if section := a.buildAgentChatSection(ctx, targets); section != "" {
			sections = append(sections, section)
		}
	}
	if len(browsers) > 0 {
		sections = append(sections, buildBrowserWindowsSection(browsers))
	}
	if len(sections) == 0 {
		return nil
	}
	return &domain.ContentBlock{Type: "text", Text: strings.Join(sections, "\n\n")}
}

// buildAgentChatSection renders the "## Conversable Agents" section: the
// fixed base prompt plus one row per mounted agent-chat target. Returns ""
// when every mounted target is offline so the section (and possibly the
// whole block) can be dropped.
//
// One live-metadata fetch for all mounts, keyed by both ID and ActorID so
// agent-chat:<ID> and agent-chat:<ActorID> mounts both resolve.
// Workspace-wide (no ProjectID filter): mounts may target agents from any
// project — the @ menu offers them and send_message resolves workspace-wide.
func (a *Actor) buildAgentChatSection(ctx actor.Context, targets []string) string {
	var sb strings.Builder
	sb.WriteString(conversableBlockBase)
	sb.WriteString("\n")

	agents := make(map[string]domain.AgentRef, len(targets)*2)
	statusUnavailable := true
	if workspaceRef, ok := ctx.LookupService("workspace"); ok && ctx.Planner() != nil {
		callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
		defer cancel()
		result, err := ctx.Planner().Call(callCtx, workspaceRef, "workspace.list_agents",
			domain.WorkspaceListAgentsReq{}).Await()
		if err == nil {
			switch v := result.(type) {
			case domain.AgentRefListResp:
				for _, ag := range v.Items {
					agents[ag.ID] = ag
					agents[ag.ActorID] = ag
				}
				statusUnavailable = false
			case *domain.AgentRefListResp:
				for _, ag := range v.Items {
					agents[ag.ID] = ag
					agents[ag.ActorID] = ag
				}
				statusUnavailable = false
			}
		}
	}

	if statusUnavailable {
		for _, targetID := range targets {
			sb.WriteString(fmt.Sprintf("- %s (id: %s, status unavailable)\n", targetID, targetID))
		}
		return sb.String()
	}
	online := 0
	for _, targetID := range targets {
		target, found := agents[targetID]
		if !found || target.LoadState != "loaded" {
			continue
		}
		id := target.ID
		if id == "" {
			id = targetID
		}
		displayName := target.DisplayName
		if displayName == "" {
			displayName = id
		}
		kind := target.AgentKind
		if kind == "" {
			kind = "agent"
		}
		status := target.Status
		if status == "" {
			status = "unknown"
		}
		// Project disambiguates same-named agents across projects; fall back
		// to the raw project actor ID when the mount name is unresolvable.
		project := target.ProjectName
		if project == "" {
			project = target.ProjectID
		}
		sb.WriteString(fmt.Sprintf("- %s (id: %s, kind: %s, project: %s, status: %s)\n", displayName, id, kind, project, status))
		online++
	}
	if online == 0 {
		return ""
	}
	return sb.String()
}

// buildBrowserWindowsSection renders the "## Mounted Browser Windows"
// section: the fixed base prompt plus one row per mounted browser-chat
// window, each pinning its Config.InstanceID.
func buildBrowserWindowsSection(instances []string) string {
	var sb strings.Builder
	sb.WriteString(browserWindowsBlockBase)
	sb.WriteString("\n")
	for _, instanceID := range instances {
		sb.WriteString(fmt.Sprintf("- %s (Config.InstanceID = %q)\n", instanceID, instanceID))
	}
	return sb.String()
}

func cardField(raw, field string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, field+":") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, field+":")), "\"'")
		}
	}
	return ""
}

func (a *Actor) buildTaskBoardBlock() *domain.ContentBlock {
	// Only surface active tasks (pending + in_progress) so the LLM focuses on
	// unfinished work. Completed and cancelled tasks remain in RawSession.Tasks
	// for history but no longer consume hot-context tokens.
	var active []gen.TurnTask
	for _, t := range a.RawSession.Tasks {
		if t.Status != "completed" && t.Status != "cancelled" {
			active = append(active, t)
		}
	}
	if len(active) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("## Current Task Board\n\n")
	for _, t := range active {
		icon := "◯"
		if t.Status == "in_progress" {
			icon = "◐"
		}
		sb.WriteString(fmt.Sprintf("%s %s", icon, t.Subject))
		if t.ActiveForm != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", t.ActiveForm))
		}
		sb.WriteString("\n")
	}
	return &domain.ContentBlock{Type: "text", Text: sb.String()}
}

// emitActorStepEvent assigns a monotonic EventSeq and emits a step event
// through the event bus. Used for Actor-level events (plan_approval, task
// create/update) that are emitted outside of turnEngine context.
func (a *Actor) emitActorStepEvent(ctx actor.Context, ev domain.StepEvent) error {
	a.stepEventSeq++
	ev.EventSeq = a.stepEventSeq
	ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	if ev.Seq == 0 && (ev.Kind == "step.opened" || ev.Kind == "step.interaction_requested") {
		ev.Seq = a.allocSeq()
	}
	if ev.Meta == "" {
		ev.Meta = a.defaultStepMeta(ev)
	}
	if err := ctx.EmitEvent("step", ev); err != nil {
		return err
	}
	a.forwardToPlugins(ctx, "step", ev)
	return nil
}

func (a *Actor) emitTaskCreateEvent(ctx actor.Context, tasks []gen.Task) error {
	for _, t := range tasks {
		payload := map[string]any{
			"id":      t.ID,
			"subject": t.Subject,
			"status":  t.Status,
		}
		if t.ActiveForm != "" {
			payload["activeForm"] = t.ActiveForm
		}
		if err := a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:   "step.task_created",
			StepID: t.ID,
			TurnID: a.status.TurnID,
			TaskID: t.ID,
			Task:   payload,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a *Actor) emitTaskUpdateEvent(ctx actor.Context, t gen.TurnTask) error {
	payload := map[string]any{
		"id":      t.ID,
		"subject": t.Subject,
		"status":  t.Status,
	}
	if t.ActiveForm != "" {
		payload["activeForm"] = t.ActiveForm
	}
	return a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.task_updated",
		StepID: t.ID,
		TurnID: a.status.TurnID,
		TaskID: t.ID,
		Task:   payload,
	})
}

func (a *Actor) emitTaskDeleteEvent(ctx actor.Context, id string) error {
	return a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.task_deleted",
		StepID: id,
		TurnID: a.status.TurnID,
		TaskID: id,
		Task:   map[string]any{"id": id},
	})
}

// ── agent messaging ──

func (a *Actor) handleReceiveMessage(ctx actor.Context, req domain.AgentMessageReceiveReq) error {
	defer a.takeSnapshot()
	msgId := ctx.NewID().String()
	msg := domain.AgentMessage{
		ID:          msgId,
		FromAgentID: req.FromAgentID,
		FromName:    req.FromName,
		ToAgentID:   ctx.Self().ID().String(),
		Text:        req.Text,
		MessageType: req.MessageType,
		Timestamp:   req.Timestamp,
	}

	// Append to mailbox
	a.mailbox = append(a.mailbox, msg)

	// Unified peer-message flow (T1 meta format: agent|<FromAgentID>|<FromName>).
	// Agent messages are treated like user messages, distinguished only by meta.
	// An active turn absorbs the message as a pending submit (merged by meta in
	// consumePendingSubmits); an idle agent gets a fresh user turn + scheduled
	// startTurn. No agent turn/step is created and the text is never appended to
	// the running turn directly.
	meta := "agent"
	if req.FromAgentID != "" {
		meta = "agent|" + req.FromAgentID + "|" + req.FromName
	} else if req.FromName != "" {
		meta = "agent||" + req.FromName
	}
	turnInput := domain.TurnInput{Text: req.Text, Meta: meta}

	// Mirror handleChatSubmit's engine-aware gating so a peer message always
	// surfaces. The previous getActiveTurnRef()-only gate queued messages onto
	// turns with no live engine to consume them (workflow-waiting owner,
	// zombie running record, cancelled engine) — the message then never
	// appeared in the recipient's conversation and never drove a turn.
	eng := a.activeTurnEngine()
	if eng != nil && !eng.isCancelled() {
		// Live engine (accepting or finalizing): queue as a pending submit.
		// The turn engine consumes it at the next step boundary; a residual
		// landing in the finalize window is promoted by handleTurnComplete.
		if _, err := a.queuePendingSubmit(ctx, turnInput); err != nil {
			return fmt.Errorf("agent: queue pending submit for peer message: %w", err)
		}
	} else if eng == nil && a.resumablePausedTurnID() != "" {
		// Crash-recovery pause: the engine is gone but the paused turn resumes
		// in place. Queue onto it so turn_resume injects the message.
		if _, err := a.queuePendingSubmit(ctx, turnInput); err != nil {
			return fmt.Errorf("agent: queue pending submit for peer message: %w", err)
		}
	} else {
		// No live engine will consume the queue: surface the peer message as a
		// user turn (Role=user, meta=agent|id|name) and schedule a fresh
		// assistant turn for it.
		a.createUserTurn(ctx, turnInput)
		if activeRef := a.getActiveTurnRef(); activeRef != "" {
			if a.turnRecordState(activeRef) == domain.TurnStateWaiting {
				// Workflow owner parked waiting: finalize the waiting turn so
				// the peer message drives a fresh turn (same carve-out as
				// chat_submit — the parked owner must never swallow a message).
				if _, err := a.applyTurnLifecycle(ctx, domain.TurnLifecycleCompleted, activeRef, nil, turnLifecycleOptions{role: "assistant"}); err != nil {
					ctx.Logger().Warn("agent: finalize waiting turn for peer message failed", "turn", activeRef, "error", err)
				}
				a.setActiveTurnRef("")
				a.status.TurnID = ""
			} else if eng == nil {
				// Superseded paused/zombie turn record with no engine behind
				// it: cancel so the new peer-message turn can start (a live
				// cancelled engine clears itself via handleRun).
				if err := a.handleTurnCancel(ctx); err != nil {
					ctx.Logger().Warn("agent: cancel superseded turn for peer message failed", "error", err)
				}
			}
		}
		a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
		a.status.State = "running"
		a.notifyWorkspaceStatus(ctx)
		turnName := fmt.Sprintf("turn-%d", a.nextTurnOrder())
		if err := ctx.After(0, "internal_start_turn", startTurnInternalReq{
			TurnInput: turnInput,
			TurnName:  turnName,
		}); err != nil {
			return fmt.Errorf("agent: schedule start_turn for peer message: %w", err)
		}
	}

	// Notify frontend
	if err := ctx.EmitEvent("agent_message_received", msg); err != nil {
		return fmt.Errorf("agent: emit message received event: %w", err)
	}
	a.forwardToPlugins(ctx, "agent_message_received", msg)

	a.saveMailbox(ctx)
	return nil
}

func (a *Actor) handleMessageList(actor.PureContext) (domain.AgentMessageListResp, error) {
	snap := a.snapshot.Load()
	if snap == nil {
		return domain.AgentMessageListResp{}, nil
	}
	items := make([]domain.AgentMessage, len(snap.mailbox))
	copy(items, snap.mailbox)
	return domain.AgentMessageListResp{Items: items}, nil
}

func (a *Actor) handleMessageClear(ctx actor.Context) error {
	defer a.takeSnapshot()
	a.mailbox = nil
	a.saveMailbox(ctx)
	return nil
}

// fetchActiveTurnDelta returns the active turn's accumulated delta messages
// converted from the turnEngine's live Steps.

func (a *Actor) fetchActiveTurnDelta() []domain.ChatMessage {
	if a.getActiveTurnRef() == "" {
		return nil
	}
	if eng := a.activeTurnEngine(); eng != nil {
		steps := eng.getDelta().Steps
		msgs := make([]domain.ChatMessage, 0, len(steps))
		for _, step := range steps {
			msgs = append(msgs, stepToChatMessage(step))
		}
		return msgs
	}
	return nil
}

func (a *Actor) handleMessagesList(_ actor.PureContext, req domain.AgentMessagesListReq) (domain.AgentMessagesListResp, error) {
	snap := a.snapshot.Load()
	if snap == nil {
		return domain.AgentMessagesListResp{}, nil
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	allMessages := snap.messages

	var result []domain.ChatMessage

	if req.BeforeIdx > 0 {
		// Pagination: load messages before a specific Idx (reverse chronological).
		for i := len(allMessages) - 1; i >= 0; i-- {
			msg := allMessages[i]
			if msg.Idx < req.BeforeIdx {
				result = append(result, msg)
				if int32(len(result)) >= limit {
					break
				}
			}
		}
		// Reverse back to chronological order.
		for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
			result[i], result[j] = result[j], result[i]
		}
		hasMore := false
		if len(result) > 0 {
			firstIdx := result[0].Idx
			for _, msg := range allMessages {
				if msg.Idx < firstIdx {
					hasMore = true
					break
				}
			}
		}
		return domain.AgentMessagesListResp{Messages: result, HasMore: hasMore}, nil
	}

	// Default: forward query since a specific Idx (gap recovery), or
	// initial load (SinceIdx <= 0 returns most recent messages).
	if req.SinceIdx <= 0 {
		// Initial load: return the most recent 'limit' messages.
		start := len(allMessages) - int(limit)
		if start < 0 {
			start = 0
		}
		result = append(result, allMessages[start:]...)
		return domain.AgentMessagesListResp{Messages: result, HasMore: start > 0}, nil
	}

	for _, msg := range allMessages {
		if msg.Idx > req.SinceIdx {
			result = append(result, msg)
			if int32(len(result)) >= limit {
				break
			}
		}
	}

	return domain.AgentMessagesListResp{Messages: result, HasMore: false}, nil
}

const (
	messageReadDefaultLimit = 20
	messageReadMaxLimit     = 200
	messageReadItemMaxBytes = 4096
)

// handleMessageRead returns the agent's conversation text as flat entries —
// one per closed step, newest first — for the pure message_read callable
// forwarded by workspace.agent_read_message. Text blocks render as their
// text; tool_use blocks render as "Name(input)" so the caller sees what was
// sent, not what came back. Tool results, reasoning content and UI-only
// blocks are excluded; each item is truncated before leaving the actor.

func (a *Actor) handleMessageRead(_ actor.PureContext, req domain.AgentMessageReadReq) (domain.AgentMessageReadResp, error) {
	snap := a.snapshot.Load()
	if snap == nil {
		return domain.AgentMessageReadResp{}, nil
	}

	limit := req.Limit
	if limit <= 0 {
		limit = messageReadDefaultLimit
	}
	if limit > messageReadMaxLimit {
		limit = messageReadMaxLimit
	}

	resp := domain.AgentMessageReadResp{}
	for i := len(snap.steps) - 1; i >= 0; i-- {
		step := snap.steps[i]
		if !step.Closed || step.Discarded {
			continue
		}
		if req.BeforeSeq > 0 && step.Seq >= req.BeforeSeq {
			continue
		}
		content := sanitizeStepText(stepTextContent(step))
		if content == "" {
			continue
		}
		if len(resp.Items) >= int(limit) {
			resp.HasMore = true
			break
		}
		resp.Items = append(resp.Items, domain.AgentMessageReadItem{
			Seq:     step.Seq,
			Role:    step.Role,
			Content: truncateStepContent(content),
		})
	}
	return resp, nil
}

// stepTextContent renders a step's readable text: text blocks joined with
// newlines, tool_use blocks as "Name(input)". Returns "" when nothing
// readable remains (e.g. reasoning-only steps).

func stepTextContent(step domain.Step) string {
	var parts []string
	for _, block := range step.Content {
		switch block.Type {
		case domain.ContentBlockText:
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case domain.ContentBlockToolUse:
			parts = append(parts, block.ToolName+"("+block.Input+")")
		}
	}
	return strings.Join(parts, "\n")
}

// truncateStepContent caps a read item so a single oversized step cannot
// flood the caller's context. The cut must land on a UTF-8 rune boundary:
// slicing mid-rune produces invalid UTF-8 that the wire codec refuses to
// marshal, failing the whole callable.

func truncateStepContent(s string) string {
	if len(s) <= messageReadItemMaxBytes {
		return s
	}
	return cutRunes(s, messageReadItemMaxBytes) + "...(truncated)"
}

// sanitizeStepText coerces stored step text to valid UTF-8. Step content can
// carry invalid bytes from upstream tool output; without this the read
// response fails codec marshal at the actor boundary.

func sanitizeStepText(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

func (a *Actor) loadMailbox(ctx actor.Context) {
	var snapshot struct {
		Mailbox                  []domain.AgentMessage             `json:"mailbox"`
		CompactionPolicyOverride *domain.CompactionPolicy          `json:"compactionPolicyOverride,omitempty"`
		TokenCalibration         tokenest.CalibrationSnapshot      `json:"tokenCalibration,omitempty"`
		Goal                     *gen.SessionGoal                  `json:"goal,omitempty"`
		ThinkingRegistry         map[string]gen.ThinkingLevel      `json:"thinkingRegistry,omitempty"`
		RawSession               *domain.RawSession                `json:"rawSession,omitempty"`
		ComponentMounts          []domain.AgentComponentMount      `json:"componentMounts,omitempty"`
		CardRefs                 []gen.CardRef                     `json:"cardRefs,omitempty"`
		ProjectApprovals         []string                          `json:"projectApprovals,omitempty"`
		Title                    string                            `json:"title,omitempty"`
		Plan                     planState                         `json:"plan,omitempty"`
		Memory                   json.RawMessage                   `json:"memory,omitempty"`
		PendingInteraction       *pendingInteractionState          `json:"pendingInteraction,omitempty"`
		PendingSubmits           map[string][]domain.PendingSubmit `json:"pendingSubmits,omitempty"`
		PermissionMode           string                            `json:"permissionMode,omitempty"`
		BoundAppID               string                            `json:"boundAppId,omitempty"`
		BoundAppPrompt           string                            `json:"boundAppPrompt,omitempty"`
		BoundBundleCardIDs       []string                          `json:"boundBundleCardIds,omitempty"`
	}
	err := agentStore.Load(a.agentStoreKey(), &snapshot)
	switch {
	case err == nil:
	case errors.Is(err, persist.ErrNotExist):
		if !a.child.Mode {
			ctx.Logger().Info("agent: no persisted state (fresh or recovered)",
				"actorId", a.actorID)
		}
		a.loadTurns()
		return
	default:
		ctx.Logger().Error("agent: load mailbox failed", "error", err,
			"actorId", a.actorID)
		a.loadTurns()
		return
	}

	a.mailbox = snapshot.Mailbox
	a.cfg.CompactionOverride = snapshot.CompactionPolicyOverride
	if snapshot.TokenCalibration.Count > 0 {
		if a.cfg.TokenCalibration == nil {
			a.cfg.TokenCalibration = tokenest.NewCalibration()
		}
		a.cfg.TokenCalibration.Load(snapshot.TokenCalibration)
	}
	if len(snapshot.ThinkingRegistry) > 0 {
		reg := make(map[thinkingUnitKey]gen.ThinkingLevel, len(snapshot.ThinkingRegistry))
		for k, v := range snapshot.ThinkingRegistry {
			parts := strings.SplitN(k, "::", 2)
			if len(parts) == 2 {
				reg[thinkingUnitKey{Model: parts[1], Provider: parts[0]}] = v
			}
		}
		a.thinkingRegistry.Store(&reg)
	}
	if snapshot.RawSession != nil {
		a.RawSession = *snapshot.RawSession
	}
	// Orphaned compaction lock: a lock present at load means the process died
	// mid-compaction (the bracket is only cleared by releaseCompactionLock).
	// Report and clear it so a fresh compaction is not blocked by stale
	// evidence from a previous lifecycle.
	if orphan := a.RawSession.CompactionLock; orphan != nil {
		a.RawSession.CompactionLock = nil
		ctx.Logger().Warn("agent: orphaned compaction lock cleared (previous run crashed mid-compaction)",
			"turnID", orphan.TurnID, "trigger", orphan.Trigger, "startedAt", orphan.StartedAt)
		a.reportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
			Severity: "warn",
			Source:   "compaction",
			Message:  fmt.Sprintf("orphaned compaction lock cleared on load (crash mid-compaction): turn %s, trigger %s, started at %s", orphan.TurnID, orphan.Trigger, orphan.StartedAt),
			TurnID:   orphan.TurnID,
		})
		// The crash interrupted that compaction: close its orphaned frame
		// steps and fail the owning compact turn so neither stays "running".
		a.closeInterruptedCompaction(ctx, orphan.TurnID, orphan.Trigger)
		a.saveMailbox(ctx)
	}
	// Defensive: if RawSession.Goal was lost during deserialization but the
	// top-level goal field was persisted, restore it. This must run AFTER the
	// RawSession assignment above, which would otherwise overwrite it.
	if a.RawSession.Goal == nil && snapshot.Goal != nil {
		a.RawSession.Goal = snapshot.Goal
	}
	// Restore persisted component mounts so user-mounted components survive
	// restarts. seedBuiltinComponentMounts runs after loadMailbox and only
	// adds builtin defaults that are missing; ensureGoalCardMounted (called
	// in OnStart) separately repairs any session where the goal state
	// survived but the builtin:mode:goal mount did not.
	if len(snapshot.CardRefs) > 0 {
		// Canonicalize on load to clean historical corruption: stale empty-ID
		// refs and accidental duplicates (e.g. a bundle listed both in the old
		// hardcoded default set and the kind config).
		a.cardRefs = canonicalCardRefs(snapshot.CardRefs)
		a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
	} else if len(snapshot.ComponentMounts) > 0 {
		a.ComponentMounts = snapshot.ComponentMounts
		a.syncCanonicalCardRefs()
	}
	if len(snapshot.ProjectApprovals) > 0 {
		a.projectApprovals = make(map[string]struct{}, len(snapshot.ProjectApprovals))
		for _, k := range snapshot.ProjectApprovals {
			a.projectApprovals[k] = struct{}{}
		}
	}
	if snapshot.Plan.Status != "" {
		a.plan = snapshot.Plan
	}
	a.pendingInteraction = snapshot.PendingInteraction
	// Restore queued user messages that were waiting on a paused turn so a
	// restart doesn't silently drop them; turn_resume consumes them in place.
	if len(snapshot.PendingSubmits) > 0 {
		a.pendingSubmits = snapshot.PendingSubmits
	}
	// Restore an explicitly-set per-agent permission mode so a re-spawned
	// agent (e.g. a workflow owner woken after a process restart) keeps the
	// user's choice instead of resetting to the spawn-time default.
	if snapshot.PermissionMode != "" {
		a.permissionMode = normalizePermissionMode(snapshot.PermissionMode)
		a.permissionModeOverride = true
	}
	// Restore the bound app (plugin) fields written by agent_bind_app so a
	// restarted plugin agent keeps its app prompt overlay and desired bundle
	// list. Empty for all agents not provisioned by an app registration.
	if snapshot.BoundAppID != "" {
		a.boundAppMu.Lock()
		a.boundAppID = snapshot.BoundAppID
		a.boundAppPrompt = snapshot.BoundAppPrompt
		a.boundBundleCardIDs = snapshot.BoundBundleCardIDs
		a.boundAppMu.Unlock()
	}
	// When a plan_submit interaction is pending, keep the plan in
	// pending_approval so it can be re-presented after restart instead of being
	// silently rejected. Only downgrade when there is no surviving pending
	// interaction (e.g. older snapshots without pendingInteraction).
	if a.plan.Status == "pending_approval" {
		if pi := a.pendingInteraction; pi != nil && pi.Type == "plan_approval" {
			a.planApprovalPending = true
		} else {
			a.plan.Status = "rejected"
			a.planApprovalPending = false
		}
	} else {
		a.planApprovalPending = false
	}
	a.title = snapshot.Title
	a.Title = snapshot.Title
	a.memoryPersisted = snapshot.Memory
	a.loadTurns()
}

func (a *Actor) saveMailbox(ctx actor.Context) {
	if a.child.Mode {
		return
	}
	key := a.agentStoreKey()
	if key == "" {
		return
	}
	// Flush closed steps to per-turn files; only keep open steps in
	// RawSession.Steps for crash recovery.
	a.flushClosedSteps()
	// Persist any new session-level request ledger entries appended since the
	// last save (per-turn RequestStats and compaction events).
	a.flushRequestRecords()
	a.RawSession.Steps = make([]domain.Step, 0)
	for i := range a.steps {
		if !a.steps[i].Closed {
			a.RawSession.Steps = append(a.RawSession.Steps, a.steps[i])
		}
	}

	calSnap := tokenest.CalibrationSnapshot{}
	if a.cfg.TokenCalibration != nil {
		calSnap = a.cfg.TokenCalibration.Snapshot()
	}
	payload := map[string]any{
		"mailbox":                  a.mailbox,
		"compactionPolicyOverride": a.cfg.CompactionOverride,
		"tokenCalibration":         calSnap,
		"rawSession":               a.RawSession,
		"title":                    a.title,
	}
	if a.cardRefs == nil && len(a.ComponentMounts) > 0 {
		payload["componentMounts"] = a.ComponentMounts
	}
	if err := a.persistMemorySnapshot(); err != nil {
		if ctx != nil {
			ctx.Logger().Error("agent: persistMemorySnapshot failed", "error", err, "actorId", a.actorID)
		}
	}
	if a.memoryPersisted != nil {
		payload["memory"] = a.memoryPersisted
	}
	if a.plan.Status != "" {
		payload["plan"] = a.plan
	}
	if a.RawSession.Goal != nil {
		payload["goal"] = *a.RawSession.Goal
	}
	if len(a.projectApprovals) > 0 {
		keys := make([]string, 0, len(a.projectApprovals))
		for k := range a.projectApprovals {
			keys = append(keys, k)
		}
		payload["projectApprovals"] = keys
	}
	if reg := a.loadThinkingRegistry(); len(reg) > 0 {
		tr := make(map[string]gen.ThinkingLevel, len(reg))
		for k, v := range reg {
			tr[k.Provider+"::"+k.Model] = v
		}
		payload["thinkingRegistry"] = tr
	}
	if len(a.cardRefs) > 0 {
		payload["cardRefs"] = canonicalCardRefs(a.cardRefs)
	}
	if a.pendingInteraction != nil {
		payload["pendingInteraction"] = *a.pendingInteraction
	}
	if len(a.pendingSubmits) > 0 {
		payload["pendingSubmits"] = a.pendingSubmits
	}
	if a.permissionModeOverride {
		payload["permissionMode"] = a.permissionMode
	}
	a.boundAppMu.RLock()
	if a.boundAppID != "" {
		payload["boundAppId"] = a.boundAppID
		if a.boundAppPrompt != "" {
			payload["boundAppPrompt"] = a.boundAppPrompt
		}
		if len(a.boundBundleCardIDs) > 0 {
			payload["boundBundleCardIds"] = a.boundBundleCardIDs
		}
	}
	a.boundAppMu.RUnlock()
	if err := agentStore.Save(key, payload); err != nil {
		if ctx != nil {
			ctx.Logger().Error("agent: save mailbox failed", "error", err,
				"actorId", a.actorID)
		}
	}
	if err := a.saveTurns(); err != nil {
		if ctx != nil {
			ctx.Logger().Error("agent: save turns failed", "error", err,
				"actorId", a.actorID)
		}
	}
}

// storeBaseDir returns the persist backend root for the agent store, derived
// from the persist instance (not config.ActorDataDir() directly) so the
// session directory tree stays within the persist backend's root. For
// non-filesystem backends that don't expose a base path, it falls back to
// the config-derived path. A fresh persist instance is created from the
// current config each call so SetDataDirForTest is respected; the underlying
// FSPersist is lightweight (a single BaseDir string) and fileLock is keyed
// on the absolute path, so concurrent instances share locks safely.
func (a *Actor) storeBaseDir() string {
	s := persist.MustNew(config.PersistConfig("agent"))
	if bp, ok := s.(persist.BasePather); ok {
		return bp.BasePath()
	}
	return filepath.Join(config.ActorDataDir(), "agent")
}

func (a *Actor) turnsDir() string {
	return filepath.Join(a.storeBaseDir(), a.agentStoreKey(), "turns")
}

func (a *Actor) stepsDir() string {
	return filepath.Join(a.storeBaseDir(), a.agentStoreKey(), "steps")
}

func (a *Actor) snapshotsDir() string {
	return filepath.Join(a.storeBaseDir(), a.agentStoreKey(), "snapshots")
}

func (a *Actor) requestsDir() string {
	return filepath.Join(a.storeBaseDir(), a.agentStoreKey(), "requests")
}

// agentStoreKey returns the persistence path segment for this agent's state.
// Decoupled from a.actorID — uses stable logical identity (projectID, agentID)
// so history survives actor re-spawns. Child agents (short-lived forks) use a
// separate _transient/ namespace keyed on actorID since they have no logical
// identity worth preserving across crashes.
func (a *Actor) agentStoreKey() string {
	return a.actorID
}

// persistStepToFile appends a single closed step as one JSONL line to the
// per-turn step file. Called from flushClosedSteps during saveMailbox.
// The append goes through the persist backend's Appender capability, which
// provides per-path locking and O_APPEND atomicity — replacing the previous
// bare os.OpenFile(O_APPEND) that bypassed the persist contract.
func (a *Actor) persistStepToFile(s domain.Step) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	name := filepath.ToSlash(filepath.Join(a.agentStoreKey(), "steps", s.TurnID+".jsonl"))
	return a.appendViaStore(name, data)
}

// appendViaStore appends data to the named entry via the agent persist
// backend's Appender capability. It creates a fresh persist instance from
// the current config (so SetDataDirForTest is respected) and type-asserts
// to Appender. For backends that don't implement Appender, it returns an
// error.
func (a *Actor) appendViaStore(name string, data []byte) error {
	s := persist.MustNew(config.PersistConfig("agent"))
	ap, ok := s.(persist.Appender)
	if !ok {
		return fmt.Errorf("persist: backend does not implement Appender")
	}
	return ap.Append(name, data)
}

// flushClosedSteps writes every closed, not-yet-persisted step to its
// per-turn file. Compaction steps are skipped — they are reconstructed
// from CompactionEvents on load and must not be stored as files.
func (a *Actor) flushClosedSteps() {
	a.stepsPersistedMu.Lock()
	if a.stepsPersisted == nil {
		a.stepsPersisted = make(map[string]struct{})
	}
	persisted := a.stepsPersisted
	a.stepsPersistedMu.Unlock()
	for i := range a.steps {
		s := &a.steps[i]
		if !s.Closed || s.TurnID == "" || isCompactionOnlyStep(*s) {
			continue
		}
		a.stepsPersistedMu.Lock()
		if _, ok := persisted[s.ID]; ok {
			a.stepsPersistedMu.Unlock()
			continue
		}
		if err := a.persistStepToFile(*s); err != nil {
			a.stepsPersistedMu.Unlock()
			continue
		}
		a.stepsPersisted[s.ID] = struct{}{}
		a.stepsPersistedMu.Unlock()
	}
}

// loadFileConcurrency bounds the worker pool used to read per-turn files at
// cold start. Sessions store one JSONL per turn for steps and one JSON per
// turn for turn records; reading them sequentially makes a long-history agent
// (1000+ turns) spend 15s+ in OnStart on Windows, which stalls the first pure
// session.summary behind onStartDone until the frontend invoke times out and
// the conversation appears stuck loading.
const loadFileConcurrency = 8

// loadStepFiles reads all per-turn step JSONL files and returns the steps
// sorted by Seq. Returns nil if the steps directory does not exist.
func (a *Actor) loadStepFiles() []domain.Step {
	dir := a.stepsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	if len(files) == 0 {
		return nil
	}
	perFile := make([][]domain.Step, len(files))
	sem := make(chan struct{}, min(loadFileConcurrency, len(files)))
	var wg sync.WaitGroup
	for i, path := range files {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			perFile[i] = parseStepFile(path)
		}(i, path)
	}
	wg.Wait()
	var steps []domain.Step
	for _, batch := range perFile {
		steps = append(steps, batch...)
	}
	sort.SliceStable(steps, func(i, j int) bool {
		return steps[i].Seq < steps[j].Seq
	})
	return steps
}

func parseStepFile(path string) []domain.Step {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var steps []domain.Step
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var s domain.Step
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			continue
		}
		steps = append(steps, s)
	}
	return steps
}

// loadRequestRecords reads the session-level request ledger from disk.
// The ledger is a single JSONL file at requests/ledger.jsonl. If the file
// does not exist, an empty ledger is returned. Duplicate IDs (e.g. from an
// interrupted append) are deduplicated, keeping the first occurrence.
func (a *Actor) loadRequestRecords() {
	if a.child.Mode {
		a.requestRecords = nil
		a.requestRecordsPersisted = 0
		return
	}
	path := filepath.Join(a.requestsDir(), "ledger.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		a.requestRecords = nil
		a.requestRecordsPersisted = 0
		return
	}
	seen := make(map[string]struct{})
	var records []domain.SessionRequestRecord
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec domain.SessionRequestRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.ID == "" {
			continue
		}
		if _, ok := seen[rec.ID]; ok {
			continue
		}
		seen[rec.ID] = struct{}{}
		records = append(records, rec)
	}
	a.requestRecords = records
	a.requestRecordsPersisted = len(records)
}

// appendRequestRecord appends a request record to the in-memory ledger.
// The record is persisted to disk during the next saveMailbox call.
func (a *Actor) appendRequestRecord(rec domain.SessionRequestRecord) {
	a.requestRecords = append(a.requestRecords, rec)
}

// flushRequestRecords writes any new request records (those not yet persisted)
// to the requests/ledger.jsonl file via the persist backend's Appender
// capability. All new records are batched into a single Append call to
// reduce the risk of partial writes. The per-path lock inside Append
// replaces the previous bare os.OpenFile(O_APPEND) that bypassed the
// persist contract.
func (a *Actor) flushRequestRecords() {
	if a.child.Mode {
		return
	}
	if len(a.requestRecords) <= a.requestRecordsPersisted {
		return
	}
	var buf []byte
	for _, rec := range a.requestRecords[a.requestRecordsPersisted:] {
		data, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		buf = append(buf, data...)
		buf = append(buf, '\n')
	}
	if len(buf) == 0 {
		return
	}
	name := filepath.ToSlash(filepath.Join(a.agentStoreKey(), "requests", "ledger.jsonl"))
	if err := a.appendViaStore(name, buf); err != nil {
		return
	}
	a.requestRecordsPersisted = len(a.requestRecords)
}

// removeStepFilesForTurn deletes the per-turn step file and clears the
// stepsPersisted entries for all steps belonging to the given turn.
func (a *Actor) removeStepFilesForTurn(turnID string) {
	_ = os.Remove(filepath.Join(a.stepsDir(), turnID+".jsonl"))
	a.stepsPersistedMu.Lock()
	if a.stepsPersisted != nil {
		for i := range a.steps {
			if a.steps[i].TurnID == turnID {
				delete(a.stepsPersisted, a.steps[i].ID)
			}
		}
	}
	a.stepsPersistedMu.Unlock()
}

// rewriteStepFile overwrites the per-turn step JSONL file with the current
// in-memory steps for that turn. Used after discarding to persist the cleared
// Content while keeping the step skeleton and index intact.
func (a *Actor) rewriteStepFile(turnID string) error {
	dir := a.stepsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, turnID+".jsonl")

	var turnSteps []domain.Step
	for _, s := range a.steps {
		if s.TurnID == turnID && !isCompactionOnlyStep(s) {
			turnSteps = append(turnSteps, s)
		}
	}
	if len(turnSteps) == 0 {
		return os.Remove(path)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, s := range turnSteps {
		data, err := json.Marshal(s)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
		if _, err := f.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return nil
}

// rewriteStepFilesForDiscardedTurns rewrites the JSONL files for the turns
// whose steps were just discarded so the cleared content is persisted.
func (a *Actor) rewriteStepFilesForDiscardedTurns(group *discardableTurnGroup) error {
	turnIDs := make(map[string]struct{})
	for _, idx := range group.stepIndices {
		turnIDs[a.steps[idx].TurnID] = struct{}{}
	}
	for turnID := range turnIDs {
		if err := a.rewriteStepFile(turnID); err != nil {
			return err
		}
	}
	return nil
}

func (a *Actor) saveSnapshot(id string, content []byte) error {
	// Child agents are transient; their regular state is not persisted, so their
	// large file snapshots should not be either. Otherwise every forked explorer
	// leaves behind a directory containing only snapshots.
	if a.child.Mode {
		return nil
	}
	dir := a.snapshotsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("agent: mkdir snapshots: %w", err)
	}
	path := filepath.Join(dir, id+".snapshot")
	if err := persist.WriteFileAtomic(path, content, 0644); err != nil {
		return fmt.Errorf("agent: write snapshot: %w", err)
	}
	return nil
}

func (a *Actor) loadSnapshot(id string) ([]byte, error) {
	path := filepath.Join(a.snapshotsDir(), id+".snapshot")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("agent: snapshot not found: %s", id)
		}
		return nil, fmt.Errorf("agent: read snapshot: %w", err)
	}
	return data, nil
}

type readSnapshotReq struct {
	SnapshotID string `json:"snapshotId"`
}

type readSnapshotResp struct {
	Content string `json:"content"`
}

func (a *Actor) handleReadSnapshot(_ actor.PureContext, req readSnapshotReq) (readSnapshotResp, error) {
	data, err := a.loadSnapshot(req.SnapshotID)
	if err != nil {
		return readSnapshotResp{}, err
	}
	return readSnapshotResp{Content: string(data)}, nil
}

type turnIndex struct {
	TurnIDs    []string `json:"turnIds"`
	ActiveHead int32    `json:"activeHead"`
}

func (a *Actor) loadTurns() {
	dir := a.turnsDir()
	idxPath := filepath.Join(dir, "index.json")
	data, err := os.ReadFile(idxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		return
	}
	var idx turnIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return
	}
	if len(idx.TurnIDs) == 0 {
		a.Session.Turns = nil
		a.Session.ActiveHead = idx.ActiveHead
		return
	}
	type loadedTurn struct {
		turn     *domain.Turn
		rawBytes string
	}
	results := make([]loadedTurn, len(idx.TurnIDs))
	sem := make(chan struct{}, min(loadFileConcurrency, len(idx.TurnIDs)))
	var wg sync.WaitGroup
	for i, tid := range idx.TurnIDs {
		wg.Add(1)
		go func(i int, tid string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			turnData, err := os.ReadFile(filepath.Join(dir, tid+".json"))
			if err != nil {
				return
			}
			var t domain.Turn
			if err := json.Unmarshal(turnData, &t); err != nil {
				return
			}
			results[i] = loadedTurn{turn: &t, rawBytes: string(turnData)}
		}(i, tid)
	}
	wg.Wait()
	turns := make([]domain.Turn, 0, len(idx.TurnIDs))
	for _, r := range results {
		if r.turn == nil {
			continue
		}
		// Seed turnSync with the on-disk content so the first saveTurns after
		// load does not pointlessly rewrite every turn (AUDIT 4.1).
		if a.turnSync == nil {
			a.turnSync = make(map[string]string)
		}
		a.turnSync[r.turn.ID] = r.rawBytes
		turns = append(turns, *r.turn)
	}
	a.Session.Turns = turns
	a.Session.ActiveHead = idx.ActiveHead
}

func (a *Actor) saveTurns() error {
	if a.actorID == "" {
		return nil
	}
	// AUDIT 4.3: prior impl also early-returned on len(Turns)==0, which
	// skipped writing index.json when a brand-new actor had only just
	// created its first turn but not yet appended it. Let the loop fall
	// through — empty input just writes an empty index, which is correct.
	if len(a.Session.Turns) == 0 {
		dir := a.turnsDir()
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		idx := turnIndex{TurnIDs: nil, ActiveHead: a.Session.ActiveHead}
		idxData, _ := json.Marshal(idx)
		return persist.WriteFileAtomic(filepath.Join(dir, "index.json"), idxData, 0644)
	}
	dir := a.turnsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if a.turnSync == nil {
		a.turnSync = make(map[string]string)
	}
	var firstErr error
	for _, t := range a.Session.Turns {
		data, err := json.Marshal(t)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sig := string(data)
		// AUDIT 4.1: only rewrite when content changed. The previous
		// IsNotExist-only check meant running → completed transitions
		// (Usage, Error, CompletedAt, State) were never persisted.
		if prev, ok := a.turnSync[t.ID]; ok && prev == sig {
			continue
		}
		turnPath := filepath.Join(dir, t.ID+".json")
		if err := persist.WriteFileAtomic(turnPath, data, 0644); err != nil {
			// Atomic write failed: surface the error instead of silently
			// dropping the turn. Do NOT advance turnSync so the next save
			// retries this turn.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		a.turnSync[t.ID] = sig
	}
	idx := turnIndex{
		TurnIDs:    make([]string, len(a.Session.Turns)),
		ActiveHead: a.Session.ActiveHead,
	}
	for i, t := range a.Session.Turns {
		idx.TurnIDs[i] = t.ID
	}
	idxData, _ := json.Marshal(idx)
	if err := persist.WriteFileAtomic(filepath.Join(dir, "index.json"), idxData, 0644); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// closeOpenStepsForTerminalTurns closes any open steps whose owning turn has
// already reached a terminal state.
func (a *Actor) closeOpenStepsForTerminalTurns() bool {
	terminal := make(map[string]struct{})
	for _, t := range a.Session.Turns {
		switch t.State {
		case "completed", "failed", "cancelled", "abandoned", "resumed":
			terminal[t.ID] = struct{}{}
		}
	}
	changed := false
	for i := range a.steps {
		if a.steps[i].Closed {
			continue
		}
		if _, ok := terminal[a.steps[i].TurnID]; !ok {
			continue
		}
		a.steps[i].Closed = true
		a.touchStep(i)
		changed = true
	}
	if changed {
		a.stepsGen++
	}
	return changed
}

// recoverOrphanTurnSteps wraps steps whose TurnID is not in Session.Turns
// (left behind by a crashed in-flight turn) into canonical turn records so
// compileMessages includes them in the next turn's LLM history. Safe orphans
// become paused/recovery turns the user can resume; orphans whose step/tool
// protocol cannot be safely recovered become abandoned turns with a diagnostic
// Error. All Session.Turns writes go through domain.ApplyTurnLifecycleEvent.
func (a *Actor) recoverOrphanTurnSteps() bool {
	turnIDs := make(map[string]struct{}, len(a.Session.Turns))
	for _, t := range a.Session.Turns {
		turnIDs[t.ID] = struct{}{}
	}
	// Discarded turns are intentionally removed from the visible session. Clean
	// persisted recovery entries whose every step was discarded.
	turnHasSteps := make(map[string]bool)
	turnHasLiveSteps := make(map[string]bool)
	for _, s := range a.steps {
		if s.TurnID == "" {
			continue
		}
		turnHasSteps[s.TurnID] = true
		if !s.Discarded {
			turnHasLiveSteps[s.TurnID] = true
		}
	}
	cleaned := false
	if len(turnHasSteps) > 0 {
		filtered := a.Session.Turns[:0]
		for _, t := range a.Session.Turns {
			if turnHasSteps[t.ID] && !turnHasLiveSteps[t.ID] {
				cleaned = true
				continue
			}
			filtered = append(filtered, t)
		}
		a.Session.Turns = filtered
	}

	// Group steps by orphan TurnID for safety analysis and ordering.
	orphanSteps := make(map[string][]domain.Step)
	for _, s := range a.steps {
		if s.TurnID == "" {
			continue
		}
		if _, exists := turnIDs[s.TurnID]; exists {
			continue
		}
		// User turns are complete conversational units; they should never be
		// rewrapped as a paused assistant turn. Skipping them prevents discarded
		// user turns from being rendered as fake assistant pause tails.
		if s.Discarded {
			continue
		}
		if s.Role == domain.ChatRoleUser {
			continue
		}
		orphanSteps[s.TurnID] = append(orphanSteps[s.TurnID], s)
	}
	if len(orphanSteps) == 0 {
		return cleaned
	}

	// Order orphan turns by their max Seq so the most recent one drives status.
	order := make([]string, 0, len(orphanSteps))
	orphanSeq := make(map[string]int64, len(orphanSteps))
	for tid, steps := range orphanSteps {
		order = append(order, tid)
		var maxSeq int64
		for _, s := range steps {
			if s.Seq > maxSeq {
				maxSeq = s.Seq
			}
		}
		orphanSeq[tid] = maxSeq
	}
	sort.Slice(order, func(i, j int) bool {
		return orphanSeq[order[i]] < orphanSeq[order[j]]
	})

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var lastRecoverableOrphan string
	for _, tid := range order {
		steps := orphanSteps[tid]
		safe := a.orphanTurnStepsSafe(steps)
		turnOrder := a.allocTurnOrder()
		event := domain.TurnLifecycleEvent{
			Kind:        domain.TurnLifecyclePaused,
			TurnID:      tid,
			State:       domain.TurnStatePaused,
			Revision:    1,
			TurnOrder:   turnOrder,
			StartedAt:   now,
			PauseReason: domain.PauseReasonRecovery,
		}
		if !safe {
			// Tool protocol cannot be safely recovered: abandon the turn.
			event = domain.TurnLifecycleEvent{
				Kind:        domain.TurnLifecycleAbandoned,
				TurnID:      tid,
				State:       domain.TurnStateAbandoned,
				Revision:    1,
				TurnOrder:   turnOrder,
				StartedAt:   now,
				Error:       fmt.Sprintf("orphan turn steps cannot be safely recovered: missing tool_result for tool_use in turn %s", tid),
				CompletedAt: now,
			}
		}
		updated, existed, err := a.applyTurnLifecycleEventToRecord(tid, event)
		if err != nil {
			slog.Warn("orphan turn recovery rejected, skipping", "turn", tid, "error", err)
			continue
		}
		if existed {
			// Should never happen: orphan means the turn ID is not in Session.Turns.
			// Skip to avoid duplicate records.
			slog.Warn("orphan turn ID already exists in Session.Turns, skipping duplicate", "turn", tid)
			continue
		}
		// Stamp the Seq and Timestamp from the orphan steps onto the new record.
		for i := range a.Session.Turns {
			if a.Session.Turns[i].ID == tid {
				a.Session.Turns[i].Seq = orphanSeq[tid]
				if orphanSeq[tid] >= a.RawSession.NextSeq {
					a.RawSession.NextSeq = orphanSeq[tid] + 1
				}
				break
			}
		}
		if a.turnOrderByID == nil {
			a.turnOrderByID = make(map[string]int64)
		}
		if updated.State == domain.TurnStatePaused {
			lastRecoverableOrphan = tid
		}
		if a.turnOrderByID == nil {
			a.turnOrderByID = make(map[string]int64)
		}
		a.turnOrderByID[tid] = turnOrder
		_ = updated
	}
	if len(a.Session.Turns) == 0 {
		return cleaned
	}
	a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
	if lastRecoverableOrphan != "" {
		lastOrphan := order[len(order)-1]
		// Only point ActiveTurnRef to the most recent recoverable orphan.
		if lastRecoverableOrphan == lastOrphan {
			a.setActiveTurnRef(lastRecoverableOrphan)
			a.status.TurnID = lastRecoverableOrphan
			a.status.State = domain.TurnStatePaused
			a.status.PauseKind = domain.PauseReasonRecovery
		} else {
			// The most recent orphan is abandoned; the earlier paused one is still
			// recoverable but don't shadow the abandoned terminal state with active.
			a.setActiveTurnRef(lastRecoverableOrphan)
			a.status.TurnID = lastRecoverableOrphan
			a.status.State = domain.TurnStatePaused
			a.status.PauseKind = domain.PauseReasonRecovery
		}
		if order := a.turnOrderByID[lastRecoverableOrphan]; order > 0 {
			a.setActiveTurnOrder(order)
		}
	}
	return true
}

// orphanTurnStepsSafe reports whether the orphan steps can be safely included in
// a resumed turn's LLM history. It returns false when a tool_use content block
// lacks a matching tool_result (which would violate the LLM protocol).
func (a *Actor) orphanTurnStepsSafe(steps []domain.Step) bool {
	covered := make(map[string]struct{})
	for _, step := range steps {
		for _, b := range step.Content {
			if b.Type == domain.ContentBlockToolResult && b.ToolUseID != "" {
				covered[b.ToolUseID] = struct{}{}
			}
		}
	}
	for _, step := range steps {
		for _, b := range step.Content {
			if b.Type != domain.ContentBlockToolUse || b.ToolUseID == "" {
				continue
			}
			if _, ok := covered[b.ToolUseID]; !ok {
				return false
			}
		}
	}
	return true
}
