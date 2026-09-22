package workbench

// 统一策略判断（"小脑"）接入：policy.decide 作为 workbench 的第四类分数来
// 源（judge），与 lifecycle / step / turn / user 并列。
//
// 语义合同：
//   - judge 贡献是有界 overlay，不是累积加分：每轮先撤销上一轮的全部
//     overlay 再套用本轮，单轮最大 +judgeMaxBump（远小于 Hysteresis）——
//     judge 单独永远不可能触发换位，只能强化既有趋势（hysteresis 保留）。
//   - 用户 promote / pinned 永远压过 judge：pinned 卡与终端卡不参与评
//     估；promote 的 score squeeze 不受 overlay 影响（overlay 每轮重置）。
//   - fail-open：policy 服务不可解析、decide 出错、答案缺卡——一律静默
//     跳过本轮，board 行为与无 judge 时完全一致。
//   - 溯源：bump 后的 why 带 backend 标记；未校准（LLM）答案权重减半。
//
// 并发纪律（CLAUDE.md Owner Lane 禁阻塞）：网络调用在 judge loop 自己的
// goroutine（绝不进 score lane）；结果经 lane-routed 的内部 callable
// workbench.judge_ingest 回流，与 drift tick 同一模式。

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	callJudgeIngest = "workbench.judge_ingest"

	// judgeInterval is the policy round cadence. The LLM fallback can take
	// seconds, so this stays well above the drift tick.
	judgeInterval = 30 * time.Second
	// judgeFirstDelay gives the board time to populate before the first round.
	judgeFirstDelay = 10 * time.Second
	// judgeMaxBump bounds one round's overlay: 3 levels × judgeBumpPerLevel.
	// Deliberately < Hysteresis so a judge verdict alone can never swap slots.
	judgeMaxBump     = 6.0
	judgeBumpPerLvl  = 2.0
	judgeLLMWeight   = 0.5
	judgeMaxCards    = 12
	judgeMaxRecent   = 16
	judgeStateMaxLen = 6000

	policyServiceName = "policy"
	policyDecideCall  = "policy.decide"
	policyCallTimeout = 100 * time.Second
)

// judgeLevels are the Score-rubric level descriptions (0..3).
var judgeLevels = []string{
	"Quiet — no live activity worth surfacing",
	"Routine background progress",
	"Notable — the user likely wants a glance",
	"Urgent — needs the user's attention now",
}

// judgeBump converts one verdict into the overlay delta. Quiet verdicts carry
// no bump (judge never punishes below the rules' floor); uncalibrated (LLM)
// verdicts are weighted down.
func judgeBump(j gen.WorkbenchJudgeScore) float64 {
	if j.Level <= 0 || int(j.Level) >= len(judgeLevels) {
		return 0
	}
	bump := float64(j.Level) * judgeBumpPerLvl
	if !j.Calibrated {
		bump *= judgeLLMWeight
	}
	if bump > judgeMaxBump {
		bump = judgeMaxBump
	}
	return bump
}

func judgeLevelName(level int) string {
	if level < 0 || int(level) >= len(judgeLevels) {
		return "?"
	}
	return []string{"安静", "常规", "值得关注", "紧急"}[level]
}

// ---------------------------------------------------------------------------
// Judge loop — off-lane network caller
// ---------------------------------------------------------------------------

// startJudgeLoop arms the periodic policy round. Each tick runs on its own
// goroutine: pure board snapshot → policy.decide → lane-routed ingest.
func (a *Actor) startJudgeLoop(ctx actor.Context) {
	lifecycle := ctx.Lifecycle()
	done := make(chan struct{})
	a.judgeDone = done
	go func() {
		timer := time.NewTimer(judgeFirstDelay)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-lifecycle.Done():
				return
			case <-timer.C:
				a.runJudgeRound(ctx)
				timer.Reset(judgeInterval)
			}
		}
	}()
}

func (a *Actor) stopJudgeLoop() {
	if a.judgeDone != nil {
		close(a.judgeDone)
		a.judgeDone = nil
	}
}

// runJudgeRound executes one policy.decide round. Every failure path is a
// quiet skip (fail-open): the board keeps behaving exactly as without judge.
func (a *Actor) runJudgeRound(ctx actor.Context) {
	if !a.judgeInFlight.CompareAndSwap(false, true) {
		return
	}
	defer a.judgeInFlight.Store(false)

	snap := a.judgeBoardSnapshot()
	if snap.frozen || snap.maximized || len(snap.judgeable) == 0 {
		return
	}

	policyRef, ok := ctx.LookupService(policyServiceName)
	if !ok {
		return
	}

	req := buildJudgeDecideReq(snap)
	callCtx, cancel := context.WithTimeout(a.lifecycle, policyCallTimeout)
	defer cancel()

	call := policyRef.Invoke(callCtx, policyDecideCall, req)
	if call == nil {
		return
	}
	defer call.Close()
	raw, err := call.Final(callCtx)
	if err != nil {
		return
	}

	var resp gen.PolicyDecideResp
	switch r := raw.(type) {
	case gen.PolicyDecideResp:
		resp = r
	case *gen.PolicyDecideResp:
		if r == nil {
			return
		}
		resp = *r
	default:
		b, merr := json.Marshal(raw)
		if merr != nil {
			return
		}
		if json.Unmarshal(b, &resp) != nil {
			return
		}
	}
	if len(resp.Answers) == 0 {
		return // all backends down — fail open
	}

	ingest := gen.WorkbenchJudgeIngestReq{Scores: map[string]gen.WorkbenchJudgeScore{}}
	for cardID, key := range snap.keys {
		ans, ok := resp.Answers[key]
		if !ok || ans.Type != "score" {
			continue
		}
		level := int32(ans.Score + 0.5) // fractional level → nearest
		if level < 0 {
			level = 0
		}
		if int(level) >= len(judgeLevels) {
			level = int32(len(judgeLevels) - 1)
		}
		ingest.Scores[cardID] = gen.WorkbenchJudgeScore{
			Level:      level,
			Backend:    ans.Backend,
			Calibrated: ans.Calibrated,
		}
	}
	if len(ingest.Scores) == 0 {
		return
	}
	a.dispatch(callJudgeIngest, ingest)
}

// ---------------------------------------------------------------------------
// Board snapshot + request building (pure reads)
// ---------------------------------------------------------------------------

// judgeBoard is the pure snapshot one round works off.
type judgeBoard struct {
	frozen    bool
	maximized bool
	// judgeable holds visible, non-pinned, non-terminal cards, highest score
	// first, capped at judgeMaxCards.
	judgeable []*cardState
	// board renders every visible card for the state text.
	board  []string
	recent []string
	// keys maps card id → question key for answer correlation.
	keys map[string]string
}

func (a *Actor) judgeBoardSnapshot() judgeBoard {
	a.mu.RLock()
	defer a.mu.RUnlock()

	jb := judgeBoard{frozen: a.frozen, maximized: a.maximizedID != "", keys: map[string]string{}}
	if jb.frozen || jb.maximized {
		return jb
	}

	elig := make([]*cardState, 0, len(a.cards))
	for _, c := range a.cards {
		if c.hidden || c.pinned || c.kind == kindTerminal {
			continue
		}
		elig = append(elig, c)
		jb.board = append(jb.board, fmt.Sprintf("- [%s] %q (status: %s, note: %s)", c.kind, c.title, c.status, c.why))
	}
	sort.Slice(elig, func(i, j int) bool { return elig[i].score > elig[j].score })
	if len(elig) > judgeMaxCards {
		elig = elig[:judgeMaxCards]
	}
	jb.judgeable = elig

	n := len(a.recent)
	if n > judgeMaxRecent {
		n = judgeMaxRecent
	}
	for i := len(a.recent) - 1; i >= len(a.recent)-n; i-- {
		ev := a.recent[i]
		who := ev.ActorID
		if who == "" {
			who = "user"
		}
		jb.recent = append(jb.recent, fmt.Sprintf("- %s %s: %s", ev.Kind, who, ev.Summary))
	}
	return jb
}

// buildJudgeDecideReq renders the state text and one Score question per
// judgeable card (question keys are positional and mapped back by card id).
func buildJudgeDecideReq(jb judgeBoard) gen.PolicyDecideReq {
	var state strings.Builder
	state.WriteString("Workbench board (visible cards) and recent system activity:\n")
	state.WriteString(strings.Join(jb.board, "\n"))
	state.WriteString("\n\nRecent activity (newest first):\n")
	state.WriteString(strings.Join(jb.recent, "\n"))

	// Bound the state; producers truncate, transport never carries long bodies.
	s := state.String()
	if runes := []rune(s); len(runes) > judgeStateMaxLen {
		s = string(runes[:judgeStateMaxLen]) + "…"
	}

	questions := map[string]gen.PolicyQuestion{}
	for i, c := range jb.judgeable {
		key := fmt.Sprintf("card%d", i)
		jb.keys[c.id] = key
		questions[key] = gen.PolicyQuestion{
			Type: "score",
			Instructions: fmt.Sprintf("Card %q (%s, status: %s, note: %s): how attention-worthy is this card's current activity for the user right now?",
				c.title, c.kind, c.status, c.why),
			Levels: judgeLevels,
		}
	}
	return gen.PolicyDecideReq{State: s, Questions: questions}
}

// ---------------------------------------------------------------------------
// Lane-routed ingest — the only state mutation
// ---------------------------------------------------------------------------

// handleJudgeIngest applies one judge round on the score lane: reset every
// previous judge overlay, then apply the new bounded bumps. Pinned, hidden and
// terminal cards are never touched; absent cards simply lose their overlay.
func (a *Actor) handleJudgeIngest(_ actor.Context, req gen.WorkbenchJudgeIngestReq) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for id, prev := range a.judgeBumps {
		if c := a.cards[id]; c != nil {
			c.score = capScore(c.score - prev)
		}
		delete(a.judgeBumps, id)
	}

	changed := false
	for id, j := range req.Scores {
		c := a.cards[id]
		if c == nil || c.pinned || c.hidden || c.kind == kindTerminal {
			continue
		}
		bump := judgeBump(j)
		if bump <= 0 {
			c.why = "" // quiet verdict clears a stale judge note
			changed = true
			continue
		}
		c.score = capScore(c.score + bump)
		c.why = fmt.Sprintf("小脑: %s（%s）", judgeLevelName(int(j.Level)), j.Backend)
		a.judgeBumps[id] = bump
		changed = true
	}

	if !changed {
		return nil
	}
	a.refreshLocked()
	if err := a.saveLocked(); err != nil {
		return fmt.Errorf("workbench.judge_ingest: save: %w", err)
	}
	return nil
}
