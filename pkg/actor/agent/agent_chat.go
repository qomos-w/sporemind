package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/slashcmd"
	"strings"
	"time"
)

func builtinModeSlashInput(text string) (cardID, args string, matched bool, err error) {
	parsed := slashcmd.Parse(text)
	if parsed == nil {
		return "", "", false, nil
	}
	modes, err := agentkit.BuiltinModeCards()
	if err != nil {
		return "", "", false, fmt.Errorf("agent: load builtin modes: %w", err)
	}
	for _, mode := range modes {
		if mode.Name == parsed.Name {
			return mode.CardID, parsed.Args, true, nil
		}
	}
	return "", "", false, nil
}

// handleChatSubmit 是用户提交聊天消息的入口 callable。
// 职责： slash 命令拦截 → 把用户消息写入 Session.Turns / steps → 启动新 turn
// 或追加到正在运行的 turn。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│     slash 命令拦截与分发     │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│    生成 user step + turn     │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│      当前有活跃 turn？       │
//	└──────────────────────────────┘
//	            ↓
//	       是 /     \ 否
//	         /       \
//	turnEngine 运行中?   startTurn 启动新 turn
//	  是 → appendMessagesToTurn
//	  否 → 清理 stale ref
func (a *Actor) handleChatSubmit(ctx actor.Context, req domain.AgentChatSubmitReq) (domain.AgentChatSubmitResp, error) {
	defer a.takeSnapshot()
	if req.Text == "" && len(req.Images) == 0 && len(req.Attachments) == 0 {
		return domain.AgentChatSubmitResp{}, fmt.Errorf("text, images, or attachments are required")
	}

	turnInput := domain.TurnInput{
		Text:            req.Text,
		Unit:            agentChatUnitToAigen(derefModelUnit(req.Unit)),
		Title:           req.Title,
		ThinkingBudget:  req.ThinkingBudget,
		ReasoningEffort: req.ReasoningEffort,
		Attachments:     toAigenAttachments(req.Attachments),
		Images:          toAigenImages(req.Images),
		Meta:            req.Meta,
	}

	// Meta contract: remote-human injects (glass context agent, IM bridge)
	// pass an explicit "user|<id>|<name>" Meta so the recipient can attribute
	// the message (sender avatar, LLM annotation). Direct submits from the
	// local operator stay meta-less (bare "user" after defaultUserMeta):
	// they are self by definition and must not render a foreign-sender
	// identity anywhere.

	modeCardID, modeArgs, modeMatched, err := builtinModeSlashInput(turnInput.Text)
	if err != nil {
		return domain.AgentChatSubmitResp{}, err
	}
	if modeMatched {
		if modeCardID == "builtin:mode:worktree" {
			// Mounting worktree mode IS entering isolation: bind the
			// worktree before the card so card presence always implies an
			// active binding. project.worktree_exit stays the only
			// unbind/delete path.
			if err := a.enterWorktreeMode(ctx); err != nil {
				return domain.AgentChatSubmitResp{}, err
			}
		}
		if !a.cardRefEnabled(modeCardID) {
			if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: modeCardID, Enabled: true, Scope: "user"}); err != nil {
				return domain.AgentChatSubmitResp{}, err
			}
		}
		turnInput.Text = modeArgs
		if turnInput.Text == "" {
			return domain.AgentChatSubmitResp{}, nil
		}
	}

	// If no explicit title is provided and we have not cached one, infer
	// title via the aggregator's fast model asynchronously so it never blocks
	// the user message. Skip when there's no text — title inference runs on
	// text only.
	intentAggRef, intentUnit, resolveErr := a.resolveTarget(ctx, a.fast)
	if intentAggRef == nil && shouldInferTitle(a.agentKind, turnInput.Title, a.title, turnInput.Text, true) {
		// Everything else permits inference; only the missing aggregator
		// target blocks it. Surface the skip — this path was previously
		// completely silent.
		ctx.Logger().Warn("agent: title inference skipped: no aggregator target for fast slot", "error", resolveErr)
	}
	if shouldInferTitle(a.agentKind, turnInput.Title, a.title, turnInput.Text, intentAggRef != nil) {
		intentReq := domain.SendSessionMessageReq{
			Messages: []domain.ChatMessage{
				{Role: "user", Content: []domain.ContentBlock{{Type: "text", Text: turnInput.Text}}},
			},
		}
		// Pin the fast-slot unit so the aggregator uses it instead of its own
		// IsFast-based selection (now removed).
		if intentUnit.Model != "" {
			intentReq.Unit = &intentUnit
		}
		// A named (non-system) fast aggregator may only contain nested
		// aggregator references, which the intent handler cannot dial.
		// Resolve the system aggregator up-front (on the actor loop) so the
		// off-loop inference can fall back to its auto pool.
		var sysFallback ref.Ref
		if slotPrimaryAggID(a.fast) != systemAggID {
			sysFallback = a.cachedAggRef(ctx, systemAggID)
		}
		// Run inference off the actor loop so the user message is not blocked.
		// State mutation is routed back via agent.title.set to keep all actor
		// state changes on the actor's single goroutine.
		lifeCtx := ctx.Lifecycle()
		selfRef := ctx.Self()
		panicprobe.SafeGo(ctx, "title_inference", func() {
			callIntent := func(target ref.Ref) (string, error) {
				callCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				call := target.Invoke(callCtx, "aiaggregator.intent", intentReq)
				result, err := call.Final(callCtx)
				if err != nil {
					return "", err
				}
				return decodeIntentResult(result), nil
			}
			title, err := callIntent(intentAggRef)
			if err != nil && sysFallback != nil {
				ctx.Logger().Warn("agent: title inference failed on fast aggregator, falling back to system",
					"error", err)
				title, err = callIntent(sysFallback)
			}
			if err != nil {
				ctx.Logger().Error("agent: title inference failed", "error", err)
				return
			}
			if title == "" {
				ctx.Logger().Warn("agent: title inference returned empty", "result_type", "unknown")
				return
			}
			notifyCtx, notifyCancel := context.WithTimeout(lifeCtx, 5*time.Second)
			defer notifyCancel()
			// Final drains the pending slot and surfaces delivery/handler
			// errors; the discarded Invoke made failures invisible.
			if _, err := selfRef.Invoke(notifyCtx, "title_set", setTitleReq{Title: title}).Final(notifyCtx); err != nil {
				ctx.Logger().Error("agent: title_set delivery failed", "error", err)
			}
		})
	}
	if a.title != "" {
		turnInput.Title = a.title
	}

	// 当调用方没指定 model 时，回退到 primary 槽首个可用 unit（含 aggregator 池）。
	if turnInput.Unit == nil {
		if u := a.slotFirstUnitResolved(ctx, a.primary); u.Model != "" {
			turnInput.Unit = &u
		}
	}

	ctx.Logger().Info("agent: chat.submit received", "model", derefModelUnit(turnInput.Unit).Model, "text_len", len(turnInput.Text), "title", turnInput.Title)

	// /clear must stop any active turn first instead of being queued as a
	// pending submit behind a running turn.
	if parsed := slashcmd.Parse(turnInput.Text); parsed != nil && parsed.Name == "clear" {
		if a.getActiveTurnRef() != "" {
			if err := a.handleTurnCancel(ctx); err != nil {
				return domain.AgentChatSubmitResp{}, err
			}
		}
		if err := a.clearSession(ctx); err != nil {
			return domain.AgentChatSubmitResp{}, err
		}
		return domain.AgentChatSubmitResp{}, nil
	}

	var pendingSkillMount *slashcmd.ParsedCommand
	// ── Slash command interception ──
	if result, dispatched := slashcmd.Dispatch(ctx.Lifecycle(), a.slashcmdState(turnInput), turnInput.Text); dispatched {
		if result.Error != nil {
			return domain.AgentChatSubmitResp{}, result.Error
		}
		if result.NotFound {
			// /<name> did not match a built-in. Probe the project card store for a
			// matching skill ID; if found, defer synthesis until after the
			// user turn exists so the synthesized tool_call step can attach
			// to it. Otherwise try a builtin bundle by slug (bundle titles
			// contain spaces, e.g. "/websearch" or "/web search" for
			// builtin:bundle:web-search): mount it and forward unconsumed
			// trailing words as the turn input, mirroring the mode path. If
			// nothing matches, fall through — the raw /<name> text is
			// delivered to the LLM as an ordinary user message.
			if parsed := slashcmd.Parse(turnInput.Text); parsed != nil {
				if a.skillExists(ctx, parsed.Name) {
					pendingSkillMount = parsed
				} else if bundleCardID, rest, matched := agentkit.ResolveBuiltinBundle(parsed.Name, parsed.Args); matched {
					if !a.cardRefEnabled(bundleCardID) {
						if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: bundleCardID, Enabled: true, Scope: "user"}); err != nil {
							return domain.AgentChatSubmitResp{}, err
						}
					}
					turnInput.Text = rest
					if turnInput.Text == "" {
						return domain.AgentChatSubmitResp{}, nil
					}
				}
			}
		} else if result.Text != "" && !result.Continue {
			msgId := ctx.NewID().String()
			msgIdx := a.RawSession.NextIdx
			a.RawSession.NextIdx++
			sysMsg := domain.ChatMessage{
				Idx:       msgIdx,
				ID:        msgId,
				Role:      "system",
				Content:   []domain.ContentBlock{{Type: domain.ContentBlockText, Text: turnInput.Text}, {Type: domain.ContentBlockText, Text: result.Text}},
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			}
			_ = sysMsg
			return domain.AgentChatSubmitResp{MessageID: msgId, Idx: msgIdx, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}, nil
		} else if !result.Continue {
			parsed := slashcmd.Parse(turnInput.Text)
			if parsed == nil {
				return domain.AgentChatSubmitResp{}, nil
			}
			switch parsed.Name {
			case "compact":
				msgId := a.nextUserTurnID()
				msgIdx := a.RawSession.NextIdx
				a.RawSession.NextIdx++
				seq := a.allocSeq()
				userStep := domain.Step{
					ID:        msgId,
					Role:      "user",
					Type:      "text",
					Content:   []domain.ContentBlock{{Type: domain.ContentBlockText, Text: turnInput.Text}},
					Closed:    true,
					Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
					TurnID:    msgId,
					Seq:       seq,
					Meta:      defaultUserMeta(turnInput.Meta),
				}
				// The compact turn stays running until compaction ends;
				// handleCompact finalizes it to completed/failed.
				userTurn := domain.Turn{
					ID:        msgId,
					Role:      "user",
					UserInput: turnInput.Text,
					State:     domain.TurnStateRunning,
					StartedAt: userStep.Timestamp,
					Timestamp: userStep.Timestamp,
					Seq:       seq,
					TurnOrder: a.allocTurnOrder(),
					Revision:  1,
				}
				a.Session.Turns = append(a.Session.Turns, userTurn)
				a.appendStep(userStep)
				_ = a.emitActorStepEvent(ctx, domain.StepEvent{
					Kind:            "step.opened",
					StepID:          userStep.ID,
					TurnID:          userStep.ID,
					StepType:        userStep.Type,
					Role:            userStep.Role,
					Block:           &domain.ContentBlock{Type: domain.ContentBlockText, Text: turnInput.Text},
					OriginMessageID: msgId,
					Meta:            defaultUserMeta(turnInput.Meta),
				})
				_ = a.emitActorStepEvent(ctx, domain.StepEvent{
					Kind:   "step.closed",
					StepID: userStep.ID,
					TurnID: userStep.ID,
				})
				a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
				a.saveMailbox(ctx)
				a.emitCompactTurnStarted(ctx, userTurn)
				_ = ctx.After(0, "compact", compactReq{TurnID: msgId, Trigger: "user"})
				return domain.AgentChatSubmitResp{MessageID: msgId, Idx: msgIdx, Timestamp: userTurn.Timestamp}, nil
			case "recompact":
				msgId := a.nextUserTurnID()
				msgIdx := a.RawSession.NextIdx
				a.RawSession.NextIdx++
				seq := a.allocSeq()
				userStep := domain.Step{
					ID:        msgId,
					Role:      "user",
					Type:      "text",
					Content:   []domain.ContentBlock{{Type: domain.ContentBlockText, Text: turnInput.Text}},
					Closed:    true,
					Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
					TurnID:    msgId,
					Seq:       seq,
					Meta:      defaultUserMeta(turnInput.Meta),
				}
				// The recompact turn stays running until compaction ends;
				// handleCompact finalizes it to completed/failed.
				userTurn := domain.Turn{
					ID:        msgId,
					Role:      "user",
					UserInput: turnInput.Text,
					State:     domain.TurnStateRunning,
					StartedAt: userStep.Timestamp,
					Timestamp: userStep.Timestamp,
					Seq:       seq,
					TurnOrder: a.allocTurnOrder(),
					Revision:  1,
				}
				a.Session.Turns = append(a.Session.Turns, userTurn)
				a.appendStep(userStep)
				_ = a.emitActorStepEvent(ctx, domain.StepEvent{
					Kind:            "step.opened",
					StepID:          userStep.ID,
					TurnID:          userStep.ID,
					StepType:        userStep.Type,
					Role:            userStep.Role,
					Block:           &domain.ContentBlock{Type: domain.ContentBlockText, Text: turnInput.Text},
					OriginMessageID: msgId,
					Meta:            defaultUserMeta(turnInput.Meta),
				})
				_ = a.emitActorStepEvent(ctx, domain.StepEvent{
					Kind:   "step.closed",
					StepID: userStep.ID,
					TurnID: userStep.ID,
				})
				a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
				// Discard all previous compaction results and re-run from scratch.
				a.RawSession.SummarySegments = nil
				a.RawSession.CompactionEvents = nil
				a.rebuildSteps()
				a.saveMailbox(ctx)
				a.emitCompactTurnStarted(ctx, userTurn)
				_ = ctx.After(0, "compact", compactReq{TurnID: msgId, Trigger: "recompact"})
				return domain.AgentChatSubmitResp{MessageID: msgId, Idx: msgIdx, Timestamp: userTurn.Timestamp}, nil
			case "dream":
				msgId := a.nextUserTurnID()
				msgIdx := a.RawSession.NextIdx
				a.RawSession.NextIdx++
				seq := a.allocSeq()
				userStep := domain.Step{
					ID:        msgId,
					Role:      "user",
					Type:      "text",
					Content:   []domain.ContentBlock{{Type: domain.ContentBlockText, Text: turnInput.Text}},
					Closed:    true,
					Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
					TurnID:    msgId,
					Seq:       seq,
					Meta:      defaultUserMeta(turnInput.Meta),
				}
				userTurn := domain.Turn{
					ID:        msgId,
					Role:      "user",
					UserInput: turnInput.Text,
					State:     "completed",
					Timestamp: userStep.Timestamp,
					Seq:       seq,
					TurnOrder: a.allocTurnOrder(),
				}
				a.Session.Turns = append(a.Session.Turns, userTurn)
				a.appendStep(userStep)
				_ = a.emitActorStepEvent(ctx, domain.StepEvent{
					Kind:            "step.opened",
					StepID:          userStep.ID,
					TurnID:          userStep.ID,
					StepType:        userStep.Type,
					Role:            userStep.Role,
					Block:           &domain.ContentBlock{Type: domain.ContentBlockText, Text: turnInput.Text},
					OriginMessageID: msgId,
					Meta:            defaultUserMeta(turnInput.Meta),
				})
				_ = a.emitActorStepEvent(ctx, domain.StepEvent{
					Kind:   "step.closed",
					StepID: userStep.ID,
					TurnID: userStep.ID,
				})
				a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
				a.saveMailbox(ctx)
				// Open an assistant step so the user sees the dream activity.
				dreamStepID := msgId + "-dream"
				a.dreamStepID = dreamStepID
				_ = a.emitActorStepEvent(ctx, domain.StepEvent{
					Kind:     "step.opened",
					StepID:   dreamStepID,
					TurnID:   msgId,
					StepType: "text",
					Role:     "assistant",
					Block:    &domain.ContentBlock{Type: domain.ContentBlockText, Text: "Dreaming…"},
				})
				a.maybeStartMemorySleep(ctx, msgId, true)
				return domain.AgentChatSubmitResp{MessageID: msgId, Idx: msgIdx, Timestamp: userTurn.Timestamp}, nil
			case "clear":
				if err := a.clearSession(ctx); err != nil {
					return domain.AgentChatSubmitResp{}, err
				}
				return domain.AgentChatSubmitResp{}, nil
			}
			return domain.AgentChatSubmitResp{}, nil
		}
	}

	// ── Goal component interception ──
	// When the goal component is mounted (via the composer "/goal" slash-mode
	// interception) AND there is no active goal, the user's first message is
	// treated as the goal condition. When a goal IS active, user input is a
	// normal message (additional context during goal execution).
	//
	// This must run BEFORE the active-turn inject/queue paths below: the goal
	// condition is a new directive that supersedes the running turn, not
	// additional context to inject into it. Without this ordering the message
	// is queued into the active turn via queuePendingSubmit and goal mode
	// never activates.
	{
		goalCardID := "builtin:mode:goal"
		goalMounted := a.cardRefEnabled(goalCardID)
		if goalMounted && a.RawSession.Goal == nil && turnInput.Text != "" {
			ctx.Logger().Info("agent: goal component mounted, intercepting message as goal condition", "text", turnInput.Text)
			if err := a.mountComponentDependencies(ctx, goalCardID, map[string]struct{}{}); err != nil {
				return domain.AgentChatSubmitResp{}, fmt.Errorf("agent: goal mode dependencies unavailable: %w", err)
			}
			// Supersede any active turn (running engine or paused ref) the
			// same way /clear does, so the goal turn starts from a clean
			// state instead of racing the dying engine's completion path.
			if a.getActiveTurnRef() != "" {
				ctx.Logger().Info("agent: cancelling active turn for goal condition", "turn", a.getActiveTurnRef())
				if err := a.handleTurnCancel(ctx); err != nil {
					ctx.Logger().Warn("agent: failed to cancel superseded turn for goal", "error", err)
				}
			}
			return a.setGoalFromInput(ctx, turnInput.Text)
		}
	}

	// 如果 turn 还在运行且愿意接受实时注入，把消息挂到当前 turn 的
	// PendingSubmits；否则创建新的 user turn（立即启动或排队到当前 turn
	// 结束后由 maybeAutoStartTurn 接棒）。
	eng := a.activeTurnEngine()
	if eng != nil && eng.AcceptingInjects() {
		return a.queuePendingSubmit(ctx, turnInput)
	}
	// Crash-recovery pause: the engine is gone but ActiveTurnRef still points
	// to a paused, resumable turn. Queue the message into that turn exactly
	// like the live-pause path so turn_resume injects it in place, instead of
	// cancelling the paused turn and superseding it with a new one.
	if eng == nil && a.resumablePausedTurnID() != "" {
		ctx.Logger().Info("agent: queueing message onto recovered paused turn", "turn", a.getActiveTurnRef())
		return a.queuePendingSubmit(ctx, turnInput)
	}

	// Queue the message into session as a user turn.
	msgId, msgIdx, userTurn := a.createUserTurn(ctx, turnInput)

	// ── Workflow component tagging ──
	// When the workflow mode is mounted and no workflow is active, tag the
	// user message so compileMessages attaches the workflow-establish prefix,
	// mirroring the goal_submit prefix for an unconfirmed goal.
	if a.cardRefEnabled("builtin:mode:workflow") && !a.workflowActive() && turnInput.Text != "" {
		if n := len(a.steps); n > 0 {
			a.steps[n-1].Meta = "workflow_submit"
			a.touchStep(n - 1)
		}
	}

	// The assistant turn name is pre-computed here (before scheduling startTurn)
	// so that a synthesized slash-skill step can be created with the correct
	// TurnID from the start, avoiding a migration from user turn id to
	// assistant turn id in startTurnWithName.
	turnName := fmt.Sprintf("turn-%d", a.nextTurnOrder())

	// If /<name> resolved to a known skill, synthesize a tool_call step that
	// is shape-identical to an LLM-initiated agent_skill_mount call. The LLM
	// sees the skill body in its own prior context (tool_use + tool_result
	// pair) and treats it as already-loaded, avoiding re-invocation.
	//
	// On success the step is created with TurnID = turnName and its events are
	// deferred to startTurnWithName (after turn.started) so the skill mount
	// renders inside the assistant envelope. The step ID is stored in
	// pendingSkillStepID so startTurnWithName can emit it.
	//
	// On failure no assistant turn will start, so emit events directly with
	// the user turn ID to surface the error round + toast, then return.
	if pendingSkillMount != nil {
		stepID, synthErr := a.synthesizeSkillMount(ctx, turnName, msgId, pendingSkillMount.Name, pendingSkillMount.Args)
		if synthErr != nil {
			a.emitStepEventsForTurn(ctx, stepID, msgId)
			return domain.AgentChatSubmitResp{MessageID: msgId, Idx: msgIdx, Timestamp: userTurn.Timestamp}, nil
		}
		a.pendingSkillStepID = stepID
	}

	if eng != nil {
		if eng.isCancelled() {
			// The active engine was cancelled (user clicked stop) but its
			// pointer has not been cleared yet — handleRun clears it immediately
			// after run() exits. Since cancelled turns skip handleTurnComplete, the
			// usual maybeAutoStartTurn pickup never fires. Schedule start_turn
			// explicitly for this user turn so it is not orphaned; it queues
			// on agent.exec behind the dying turn and runs once the engine
			// pointer is cleared.
			ctx.Logger().Info("agent: active turn cancelled, starting new turn for queued user message", "turn", turnName)
			if err := ctx.After(0, "internal_start_turn", startTurnInternalReq{
				TurnInput: turnInput,
				TurnName:  turnName,
			}); err != nil {
				ctx.Logger().Error("agent: failed to schedule start_turn after cancel", "turn", turnName, "error", err)
				return domain.AgentChatSubmitResp{}, fmt.Errorf("agent: schedule start_turn: %w", err)
			}
			return domain.AgentChatSubmitResp{
				MessageID:   msgId,
				Idx:         msgIdx,
				Timestamp:   userTurn.Timestamp,
				TurnActorID: turnName,
			}, nil
		}
		// turnEngine 存在但已不再接受注入（正在正常收尾）。只把 user turn 追加到
		// Session.Turns，不更新 ActiveHead；当前 turn 完成后 maybeAutoStartTurn
		// 会自动启动新 turn 处理它。
		// 例外：engine 指针要到 handleTurnComplete 返回后才在 agent.exec 上清除，
		// 而 workflow owner 的 turn 在 handleTurnComplete 内就已 park 成 waiting
		// （ActiveTurnRef 保留、maybeAutoStartTurn 被压制）。此窗口内到达的 submit
		// 看到的是"僵尸 engine + 已 waiting 的记录"：若仍按收尾排队，该 user turn
		// 永远无人接棒（2026-08-28 Render Chonk 卡死事故）。落穿到下方
		// waiting-finalize 路径，由本次 submit 直接唤醒 owner。
		if activeRef := a.getActiveTurnRef(); activeRef != "" && a.turnRecordState(activeRef) == domain.TurnStateWaiting {
			ctx.Logger().Info("agent: turn already parked waiting behind live engine pointer; finalizing", "turn", activeRef)
		} else {
			ctx.Logger().Info("agent: turn finishing, queueing user turn for next cycle", "turn", a.getActiveTurnRef())
			return domain.AgentChatSubmitResp{MessageID: msgId, Idx: msgIdx, Timestamp: userTurn.Timestamp}, nil
		}
	}

	if activeRef := a.getActiveTurnRef(); activeRef != "" {
		if a.turnRecordState(activeRef) == domain.TurnStateWaiting {
			// A workflow-waiting turn is finalized (waiting→completed) rather
			// than cancelled so its output stays a completed record. The
			// updater's chat message must never be blocked by the parked
			// owner: clear the ref/status and start the new turn normally.
			ctx.Logger().Info("agent: finalizing waiting turn for new user message", "turn", activeRef)
			if _, err := a.applyTurnLifecycle(ctx, domain.TurnLifecycleCompleted, activeRef, nil, turnLifecycleOptions{
				role: "assistant",
			}); err != nil {
				ctx.Logger().Warn("agent: finalize waiting turn failed", "turn", activeRef, "error", err)
			}
			a.setActiveTurnRef("")
			a.status.TurnID = ""
		} else {
			// A paused turn (e.g. after crash recovery) is being superseded by
			// a new user message. Cancel it so its task snapshot is persisted to
			// history and completed tasks don't leak into the new turn.
			ctx.Logger().Info("agent: cancelling paused turn for new user message", "pausedRef", activeRef)
			if err := a.handleTurnCancel(ctx); err != nil {
				ctx.Logger().Warn("agent: failed to cancel superseded paused turn", "pausedRef", activeRef, "error", err)
			}
		}
	}
	ctx.Logger().Info("agent: starting new turn")

	a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
	a.saveMailbox(ctx)

	// Notify the workspace that this agent is running now, before deferring the
	// turn. start_turn is scheduled via ctx.After(0) (a mailbox round-trip), so
	// without this the status stays on the previous state (idle/completed) for
	// an extra actor cycle — the sidebar/avatar bar lag behind the turn actually
	// starting. resetTurnSnapshot will re-affirm state="running" once the turn
	// engine spins up, so setting it here is idempotent.
	a.status.State = "running"
	a.notifyWorkspaceStatus(ctx)

	// Defer startTurn to an internal message so chat.submit returns immediately.
	// turnName was already computed above so a slash-skill step could be bound
	// to the correct assistant turn from creation.
	if err := ctx.After(0, "internal_start_turn", startTurnInternalReq{
		TurnInput: turnInput,
		TurnName:  turnName,
	}); err != nil {
		ctx.Logger().Error("agent: failed to schedule start_turn", "turn", turnName, "error", err)
		return domain.AgentChatSubmitResp{}, fmt.Errorf("agent: schedule start_turn: %w", err)
	}

	return domain.AgentChatSubmitResp{
		MessageID:   msgId,
		Idx:         msgIdx,
		Timestamp:   userTurn.Timestamp,
		TurnActorID: turnName,
	}, nil
}

// startTurnInternalReq is the payload for agent.internal.start_turn; it carries
// the user input and the pre-computed turn name so chat.submit can return
// immediately while startTurn runs asynchronously on the actor's goroutine.
type startTurnInternalReq struct {
	TurnInput domain.TurnInput `json:"turnInput"`
	TurnName  string           `json:"turnName"`
	Retries   int              `json:"retries,omitempty"`
}

const (
	startTurnAggregatorMaxRetries    = 10
	startTurnAggregatorRetryDelay    = 200 * time.Millisecond
	startTurnAggregatorRetryMaxDelay = 3 * time.Second
)

func isAggregatorStartupError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "no aggregator available") ||
		strings.Contains(message, "no model target available")
}

func (a *Actor) handleStartTurnInternal(ctx actor.Context, req startTurnInternalReq) error {
	// Abort if the turn was cancelled before it could start. This happens when
	// handleTurnResume sets ActiveTurnRef=turnName and schedules start_turn, but
	// handleTurnCancel fires in between — it creates a cancelled turn entry and
	// clears ActiveTurnRef. Without this guard the new turn would start despite
	// the user's stop request.
	for _, t := range a.Session.Turns {
		if t.ID == req.TurnName && t.State == "cancelled" {
			ctx.Logger().Info("agent: start_turn aborted, cancelled before start", "turn", req.TurnName)
			return nil
		}
	}
	_, err := a.startTurnWithName(ctx, req.TurnInput, req.TurnName)
	if err != nil {
		// Startup race: aimanager hasn't finished spawning aggregators yet.
		// The frontend already has TurnActorID and is showing a "running"
		// turn (seedActiveTurn). Failing immediately leaves a phantom turn
		// that the frontend can't reconcile ("turn actor id not found" on
		// subsequent RPCs). Instead, re-schedule with a short delay and a
		// retry cap so the turn starts once aggregators are ready.
		if isAggregatorStartupError(err) && req.Retries < startTurnAggregatorMaxRetries {
			delay := startTurnAggregatorRetryDelay << uint(req.Retries)
			if delay > startTurnAggregatorRetryMaxDelay {
				delay = startTurnAggregatorRetryMaxDelay
			}
			ctx.Logger().Info("agent: aggregator not ready, deferring turn start", "turn", req.TurnName, "retry", req.Retries+1, "delay", delay)
			if rerr := ctx.After(delay, "internal_start_turn", startTurnInternalReq{
				TurnInput: req.TurnInput,
				TurnName:  req.TurnName,
				Retries:   req.Retries + 1,
			}); rerr != nil {
				ctx.Logger().Error("agent: failed to re-schedule start_turn for aggregator retry", "turn", req.TurnName, "error", rerr)
				// Fall through to failure-cleanup below — without a pending
				// retry the optimistic "running" status would be a zombie.
			} else {
				return nil
			}
		}

		ctx.Logger().Error("agent: start turn failed", "turn", req.TurnName, "error", err)
		// Defensive cleanup: reset any turn state that handleChatSubmit or
		// startTurnWithName may have set before the failure. handleChatSubmit
		// sets status.State="running" optimistically; startTurnWithName may
		// set ActiveTurnRef before hitting a later error. Without this reset
		// the agent is left in a zombie "running" state (ActiveTurnRef set)
		// that blocks all subsequent chat.submit, or a cosmetic "running"
		// status that hides the failure from the UI.
		if a.getActiveTurnRef() == req.TurnName {
			a.setActiveTurnRef("")
		}
		a.status.TurnID = ""
		a.status.State = "failed"
		a.notifyWorkspaceStatus(ctx)
		_ = ctx.EmitEvent("turn", domain.TurnEvent{
			Kind:   domain.TurnFailed,
			TurnID: req.TurnName,
			Payload: map[string]any{
				"state":       "failed",
				"error":       err.Error(),
				"startedAt":   "",
				"completedAt": "",
			},
		})
	}
	return nil
}

// setGoalFromInput treats the supplied text as a goal condition: it records the
// session goal, refreshes the component snapshot (the goal-mode dependency pulls
// in the goal-tools bundle), and starts the autonomous goal turn. The goal mode
// is mounted up front via the composer slash-mode interception ("/goal"); this
// runs once, on the first message after the mode is active and no goal is set.
func (a *Actor) setGoalFromInput(ctx actor.Context, text string) (domain.AgentChatSubmitResp, error) {
	maxTurns := DefaultGoalMaxTurns
	if cfg := a.fetchAgentKindConfig(ctx); cfg.MaxTurns > 0 {
		maxTurns = cfg.MaxTurns
	}
	a.RawSession.Goal = &gen.SessionGoal{
		Condition: text,
		MaxTurns:  maxTurns,
		TurnCount: 0,
	}
	a.invalidateComponentSnapshot(ctx)

	msgID, msgIdx, userTurn := a.createUserTurn(ctx, domain.TurnInput{Text: text})
	// Mark this user step as the goal-condition message so context
	// compilation can attach the goal-submit prompt prefix.
	if n := len(a.steps); n > 0 {
		a.steps[n-1].Meta = "goal_submit"
		a.touchStep(n - 1)
	}
	a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
	a.saveMailbox(ctx)
	turnName := fmt.Sprintf("turn-%d", a.nextTurnOrder())
	if err := ctx.After(0, "internal_start_turn", startTurnInternalReq{
		TurnInput: domain.TurnInput{Text: text},
		TurnName:  turnName,
	}); err != nil {
		return domain.AgentChatSubmitResp{}, fmt.Errorf("agent: schedule goal start_turn: %w", err)
	}
	return domain.AgentChatSubmitResp{MessageID: msgID, Idx: msgIdx, Timestamp: userTurn.Timestamp, TurnActorID: turnName}, nil
}

// resumablePausedTurnID returns ActiveTurnRef when it points to a paused turn
// in Session.Turns (a turn that turn_resume can continue in place), or "".
func (a *Actor) resumablePausedTurnID() string {
	ref := a.getActiveTurnRef()
	if ref == "" {
		return ""
	}
	for i := range a.Session.Turns {
		if a.Session.Turns[i].ID == ref {
			if a.Session.Turns[i].State == "paused" {
				return ref
			}
			return ""
		}
	}
	return ""
}

// queuePendingSubmit appends the user message to the active turn's
// PendingSubmits so the inline turnEngine can consume it at the next step
// boundary. The caller must have already verified AcceptingInjects() (live
// engine) or resumablePausedTurnID() (crash-recovery pause).
// Returns ps.ID as MessageID so the frontend can bind its tempId and confirm
// the pending entry when consumePendingSubmits emits the user_inject step.
func (a *Actor) queuePendingSubmit(ctx actor.Context, req domain.TurnInput) (domain.AgentChatSubmitResp, error) {
	// Resolve the target turn by ActiveTurnRef first: after a crash-recovery
	// pause the persisted ActiveHead may not point at the paused turn.
	var turn *domain.Turn
	if ref := a.getActiveTurnRef(); ref != "" {
		for i := range a.Session.Turns {
			if a.Session.Turns[i].ID == ref {
				turn = &a.Session.Turns[i]
				break
			}
		}
	}
	if turn == nil {
		activeIdx := a.Session.ActiveHead
		if activeIdx < 0 || int(activeIdx) >= len(a.Session.Turns) {
			ctx.Logger().Warn("agent: queuePendingSubmit no active turn", "activeHead", activeIdx)
			return domain.AgentChatSubmitResp{}, nil
		}
		turn = &a.Session.Turns[activeIdx]
	}
	ps := domain.PendingSubmit{
		ID:          ctx.NewID().String(),
		Text:        req.Text,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Attachments: req.Attachments,
		Images:      req.Images,
		Meta:        defaultUserMeta(req.Meta),
	}
	if a.pendingSubmits == nil {
		a.pendingSubmits = make(map[string][]domain.PendingSubmit)
	}
	a.pendingSubmits[turn.ID] = append(a.pendingSubmits[turn.ID], ps)
	// Persist immediately: while paused (live or crash-recovered) the entry can
	// sit in the queue for a long time and must survive an app restart.
	a.saveMailbox(ctx)
	ctx.Logger().Info("agent: chat.submit queued as pending submit", "turn", a.getActiveTurnRef(), "psID", ps.ID)
	return domain.AgentChatSubmitResp{MessageID: ps.ID, Timestamp: ps.Timestamp}, nil
}

// handleCancelPendingSubmit removes a not-yet-consumed PendingSubmit by its ID
// so the turn engine never injects it. Called by the frontend when the user
// clicks the X button on a queued message. If the submit was already consumed
// (user_inject step already emitted), this is a safe no-op.
func (a *Actor) handleCancelPendingSubmit(ctx actor.Context, req gen.AgentChatCancelPendingReq) (gen.AgentChatCancelPendingResp, error) {
	defer a.takeSnapshot()
	if req.MessageID == "" {
		return gen.AgentChatCancelPendingResp{}, fmt.Errorf("MessageId is required")
	}
	for turnID, submits := range a.pendingSubmits {
		for i, ps := range submits {
			if ps.ID == req.MessageID {
				a.pendingSubmits[turnID] = append(submits[:i], submits[i+1:]...)
				if len(a.pendingSubmits[turnID]) == 0 {
					delete(a.pendingSubmits, turnID)
				}
				ctx.Logger().Info("agent: cancelled pending submit", "psID", ps.ID, "turn", turnID)
				a.saveMailbox(ctx)
				return gen.AgentChatCancelPendingResp{Cancelled: true}, nil
			}
		}
	}
	// Already consumed or not found — safe no-op.
	ctx.Logger().Debug("agent: cancel pending submit not found (already consumed?)", "psID", req.MessageID)
	return gen.AgentChatCancelPendingResp{Cancelled: false}, nil
}

// createUserTurn creates a completed user turn from the submitted text, emits
// the corresponding step events, and appends it to Session.Turns / a.steps.
// It returns the generated IDs and turn value. It does NOT update ActiveHead
// or start a new assistant turn.
func (a *Actor) nextUserTurnID() string {
	return fmt.Sprintf("user-turn-%d", a.nextTurnOrder())
}

func (a *Actor) createUserTurn(ctx actor.Context, req domain.TurnInput) (string, int32, domain.Turn) {
	msgId := a.nextUserTurnID()
	msgIdx := a.RawSession.NextIdx
	a.RawSession.NextIdx++
	meta := req.Meta
	if meta == "" {
		meta = "user"
	}
	var content []domain.ContentBlock
	if req.Text != "" {
		content = append(content, domain.ContentBlock{Type: domain.ContentBlockText, Text: req.Text})
	}
	for _, img := range req.Images {
		content = append(content, domain.ContentBlock{Type: domain.ContentBlockImage, ImageURL: img.URL, MimeType: img.MimeType})
	}
	for _, att := range req.Attachments {
		content = append(content, domain.ContentBlock{Type: domain.ContentBlockText, Text: fmt.Sprintf("[Attachment: %s (%s)]", att.Name, att.MimeType)})
	}
	userStep := domain.Step{
		ID:        msgId,
		Role:      "user",
		Type:      "text",
		Content:   content,
		Closed:    true,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		TurnID:    msgId,
		Seq:       a.allocSeq(),
		Meta:      meta,
	}
	userTurn := domain.Turn{
		ID:              msgId,
		Role:            "user",
		UserInput:       req.Text,
		State:           "completed",
		Timestamp:       userStep.Timestamp,
		UserAttachments: req.Attachments,
		UserImages:      req.Images,
		Seq:             userStep.Seq,
		TurnOrder:       a.allocTurnOrder(),
	}
	if a.turnOrderByID == nil {
		a.turnOrderByID = make(map[string]int64)
	}
	a.turnOrderByID[userTurn.ID] = userTurn.TurnOrder
	a.Session.Turns = append(a.Session.Turns, userTurn)
	a.appendStep(userStep)
	// step.opened carries the first content block (text when present, otherwise
	// the first image). Remaining blocks arrive via block.appended below so the
	// frontend renders the full envelope without waiting for a reconnect.
	if len(content) > 0 {
		firstBlock := content[0]
		_ = a.emitActorStepEvent(ctx, domain.StepEvent{
			Kind:            "step.opened",
			StepID:          userStep.ID,
			TurnID:          userStep.ID,
			StepType:        userStep.Type,
			Role:            userStep.Role,
			Block:           &firstBlock,
			OriginMessageID: msgId,
			Meta:            meta,
		})
		for i := 1; i < len(content); i++ {
			block := content[i]
			_ = a.emitActorStepEvent(ctx, domain.StepEvent{
				Kind:   "block.appended",
				StepID: userStep.ID,
				TurnID: userStep.ID,
				Block:  &block,
			})
		}
	}
	_ = a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.closed",
		StepID: userStep.ID,
		TurnID: userStep.ID,
	})
	return msgId, msgIdx, userTurn
}

// handleCompleteMessage makes a clean, non-streaming LLM call without
// entering the agent's turn loop or polluting conversation history.
// The result text is returned directly to the caller.

func (a *Actor) handleCompleteMessage(ctx actor.Context, req domain.CompleteMessageReq) (domain.SummarizeResp, error) {
	if req.UserText == "" {
		return domain.SummarizeResp{}, fmt.Errorf("agent.complete_message: UserText is required")
	}

	// Runs on the agent_exec loop (serialized with the turn engine), so the
	// summary slot must be read under slotMu — the owner lane rewrites slots on
	// agent_configure.
	a.slotMu.RLock()
	summarySlot := a.summary
	a.slotMu.RUnlock()
	aggRef, _, err := a.resolveTarget(ctx, summarySlot)
	if err != nil {
		return domain.SummarizeResp{}, fmt.Errorf("agent.complete_message: no model target available: %w", err)
	}

	ctx.Logger().Info("agent: complete_message", "model", derefModelUnit(agentUnitToAigen(derefModelUnit(req.Unit))).Model, "text_len", len(req.UserText))

	// Build a SendSessionMessageReq for the aggregator's summarize callable.
	summarizeReq := domain.SendSessionMessageReq{
		SessionID: "complete-" + ctx.NewID().String(),
		AgentID:   a.actorID,
		SlotKind:  "summary",
		System:    req.System,
		Unit:      agentUnitToAigen(derefModelUnit(req.Unit)),
		Messages: []domain.ChatMessage{
			{Role: "user", Content: []domain.ContentBlock{
				{Type: "text", Text: req.UserText},
			}},
		},
	}

	node, err := ctx.Planner().Plan(aggRef, "aiaggregator.summarize", summarizeReq)
	if err != nil {
		return domain.SummarizeResp{}, fmt.Errorf("agent.complete_message: plan: %w", err)
	}
	if err := node.Start(ctx.Lifecycle()); err != nil {
		return domain.SummarizeResp{}, fmt.Errorf("agent.complete_message: start: %w", err)
	}
	defer func() { _ = ctx.Stop(node.Ref()) }()

	timeoutCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 2*time.Minute)
	defer cancel()

	type result struct {
		resp domain.SummarizeResp
		err  error
	}
	ch := make(chan result, 1)
	panicprobe.SafeGo(ctx, "summarize_recv", func() {
		v, err := node.Recv()
		if err != nil {
			ch <- result{err: err}
			return
		}
		s, ok := v.(domain.SummarizeResp)
		if !ok {
			ch <- result{err: fmt.Errorf("unexpected response type %T", v)}
			return
		}
		ch <- result{resp: s}
	})

	select {
	case r := <-ch:
		if r.err != nil {
			return domain.SummarizeResp{}, fmt.Errorf("agent.complete_message: recv: %w", r.err)
		}
		return r.resp, nil
	case <-timeoutCtx.Done():
		return domain.SummarizeResp{}, fmt.Errorf("agent.complete_message: timeout after 2 minutes")
	}
}

// consumePendingSubmits drains pending submits from the active turn, merges
// those with the same meta into a single user message (text concatenated with
// newline separators), and emits one user_inject step per merged message so the
// turnEngine can inject them into history. Messages with different meta remain
// separate user messages.
func (a *Actor) consumePendingSubmits(ctx actor.Context) []domain.ChatMessage {
	activeRef := a.getActiveTurnRef()
	turnID := activeRef
	if turnID == "" {
		activeIdx := a.Session.ActiveHead
		if activeIdx < 0 || int(activeIdx) >= len(a.Session.Turns) {
			return nil
		}
		turnID = a.Session.Turns[activeIdx].ID
	}
	submits := a.pendingSubmits[turnID]
	if len(submits) == 0 {
		return nil
	}

	// Group pending submits by their normalized meta. First-seen order is kept
	// so messages are injected in the same order they were queued.
	groups := groupPendingSubmitsByMeta(submits)

	var messages []domain.ChatMessage
	for _, g := range groups {
		content := mergePendingGroupContent(g.submits)
		stepID := g.submits[0].ID

		firstBlock := content[0]
		openedEv := domain.StepEvent{
			Kind:            "step.opened",
			StepID:          stepID,
			TurnID:          activeRef,
			StepType:        "user_inject",
			Role:            "assistant",
			Block:           &firstBlock,
			OriginMessageID: g.submits[0].ID,
			Meta:            g.meta,
		}
		a.applyStepEvent(openedEv)
		_ = a.emitActorStepEvent(ctx, openedEv)

		// The remaining submits in this group were merged into the same step.
		// Emit a duplicate step.opened for each so the frontend can confirm
		// the corresponding pending entry via OriginMessageID. Both the backend
		// state and the frontend reducer ignore duplicate step.opened events.
		for _, ps := range g.submits[1:] {
			confirmEv := openedEv
			confirmEv.OriginMessageID = ps.ID
			a.applyStepEvent(confirmEv)
			_ = a.emitActorStepEvent(ctx, confirmEv)
		}

		for i := 1; i < len(content); i++ {
			block := content[i]
			appendEv := domain.StepEvent{
				Kind:            "block.appended",
				StepID:          stepID,
				TurnID:          activeRef,
				Block:           &block,
				OriginMessageID: g.submits[0].ID,
			}
			a.applyStepEvent(appendEv)
			_ = a.emitActorStepEvent(ctx, appendEv)
		}
		closedEv := domain.StepEvent{
			Kind:            "step.closed",
			StepID:          stepID,
			TurnID:          activeRef,
			OriginMessageID: g.submits[0].ID,
		}
		a.applyStepEvent(closedEv)
		_ = a.emitActorStepEvent(ctx, closedEv)

		injected := domain.ChatMessage{
			Role:    domain.ChatRoleUser,
			Content: content,
		}
		// Peer agent / human-inject submits: annotate the sender so the LLM
		// can tell them apart from the operator's own input.
		if kind, sid, sname, ok := parseSenderMeta(g.meta); ok {
			applyPeerSenderPrefix(&injected, kind, sid, sname)
		}
		messages = append(messages, injected)
	}

	delete(a.pendingSubmits, turnID)
	return messages
}

// mergePendingGroupContent combines all content blocks from a group of pending
// submits into a single []ContentBlock suitable for one ChatMessage. Text from
// each submit is joined into one text block with newline separators; images and
// attachments are appended after the text in queue order.
func mergePendingGroupContent(submits []domain.PendingSubmit) []domain.ContentBlock {
	var texts []string
	for _, ps := range submits {
		if ps.Text != "" {
			texts = append(texts, ps.Text)
		}
	}
	var content []domain.ContentBlock
	if len(texts) > 0 {
		content = append(content, domain.ContentBlock{Type: domain.ContentBlockText, Text: strings.Join(texts, "\n")})
	}
	for _, ps := range submits {
		for _, img := range ps.Images {
			content = append(content, domain.ContentBlock{Type: domain.ContentBlockImage, ImageURL: img.URL, MimeType: img.MimeType})
		}
		for _, att := range ps.Attachments {
			content = append(content, domain.ContentBlock{Type: domain.ContentBlockText, Text: fmt.Sprintf("[Attachment: %s (%s)]", att.Name, att.MimeType)})
		}
	}
	if len(content) == 0 {
		content = append(content, domain.ContentBlock{Type: domain.ContentBlockText, Text: ""})
	}
	return content
}

// pendingGroup is a meta-normalized group of PendingSubmit entries. It is used
// by both the live user_inject path (consumePendingSubmits) and the successor
// promotion path (pendingSubmitsToTurnInputs) so they share the same merge rule.
type pendingGroup struct {
	meta    string
	submits []domain.PendingSubmit
}

// groupPendingSubmitsByMeta groups submits by defaultUserMeta(ps.Meta). The
// returned slice preserves the first-seen order of each distinct meta so
// messages are processed in the same order they were queued.
func groupPendingSubmitsByMeta(submits []domain.PendingSubmit) []pendingGroup {
	groupOrder := make([]string, 0, len(submits))
	groups := make(map[string]*pendingGroup, len(submits))
	for _, ps := range submits {
		meta := defaultUserMeta(ps.Meta)
		if g, ok := groups[meta]; ok {
			g.submits = append(g.submits, ps)
			continue
		}
		g := &pendingGroup{meta: meta, submits: []domain.PendingSubmit{ps}}
		groups[meta] = g
		groupOrder = append(groupOrder, meta)
	}
	out := make([]pendingGroup, 0, len(groupOrder))
	for _, meta := range groupOrder {
		out = append(out, *groups[meta])
	}
	return out
}

// pendingSubmitsToTurnInputs groups pending submits by their normalized meta
// and merges each group into a TurnInput suitable for createUserTurn. Same-meta
// submits have their text joined with newline separators; images and
// attachments are concatenated in queue order. Distinct meta groups remain
// separate inputs so each is promoted to its own user turn.
func pendingSubmitsToTurnInputs(submits []domain.PendingSubmit) []domain.TurnInput {
	groups := groupPendingSubmitsByMeta(submits)
	inputs := make([]domain.TurnInput, 0, len(groups))
	for _, g := range groups {
		var texts []string
		var attachments []gen.AttachmentEntry
		var images []gen.ImageEntry
		for _, ps := range g.submits {
			if ps.Text != "" {
				texts = append(texts, ps.Text)
			}
			attachments = append(attachments, ps.Attachments...)
			images = append(images, ps.Images...)
		}
		inputs = append(inputs, domain.TurnInput{
			Text:        strings.Join(texts, "\n"),
			Attachments: attachments,
			Images:      images,
			Meta:        g.meta,
		})
	}
	return inputs
}

// defaultUserMeta returns the provided meta if non-empty, otherwise "user".
func defaultUserMeta(meta string) string {
	if meta != "" {
		return meta
	}
	return "user"
}

// toAigenAttachments converts agentchat.AttachmentEntry slice to aigen.AttachmentEntry slice.
func toAigenAttachments(in []gen.AttachmentEntry) []gen.AttachmentEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]gen.AttachmentEntry, len(in))
	for i, v := range in {
		out[i] = gen.AttachmentEntry{
			Name:      v.Name,
			MimeType:  v.MimeType,
			URL:       v.URL,
			SizeBytes: v.SizeBytes,
		}
	}
	return out
}

// toAigenImages converts agentchat.ImageEntry slice to aigen.ImageEntry slice.
func toAigenImages(in []gen.ImageEntry) []gen.ImageEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]gen.ImageEntry, len(in))
	for i, v := range in {
		out[i] = gen.ImageEntry{
			URL:      v.URL,
			Alt:      v.Alt,
			MimeType: v.MimeType,
		}
	}
	return out
}

func agentChatUnitToAigen(u gen.ModelUnit) *gen.ModelUnit {
	v := gen.ModelUnit(u)
	return &v
}

func agentUnitToAigen(u gen.ModelUnit) *gen.ModelUnit {
	v := gen.ModelUnit(u)
	return &v
}

func derefModelUnit(u *gen.ModelUnit) gen.ModelUnit {
	if u == nil {
		return gen.ModelUnit{}
	}
	return *u
}

// compactReq is the internal payload for agent.compact. It carries the
// turn ID that owns the compaction step and the trigger kind.
type compactReq struct {
	TurnID  string
	Trigger string
}

// setTitleReq is the internal payload for agent.title.set.
type setTitleReq struct {
	Title string
}

// handleCompact runs compaction asynchronously on the actor's own loop so
// long-running summary rounds do not block the chat.submit response.
func (a *Actor) handleCompact(ctx actor.Context, req compactReq) error {
	defer a.takeSnapshot()
	if err := a.compactAsStep(ctx, req.TurnID, req.Trigger, ""); err != nil {
		ctx.Logger().Error("agent: compact failed", "turnID", req.TurnID, "trigger", req.Trigger, "error", err)
		a.finalizeCompactTurn(ctx, req.TurnID, err)
		return err
	}
	a.finalizeCompactTurn(ctx, req.TurnID, nil)
	a.saveMailbox(ctx)
	return nil
}

func (a *Actor) slashcmdState(req domain.TurnInput) slashcmd.AgentState {
	turns := make([]slashcmd.TurnSnapshot, len(a.Session.Turns))
	for i, t := range a.Session.Turns {
		turns[i] = slashcmd.TurnSnapshot{
			ID:        t.ID,
			Role:      t.Role,
			UserInput: t.UserInput,
			State:     t.State,
		}
	}
	return slashcmd.AgentState{
		Session: &slashcmd.SessionSnapshot{
			Turns:      turns,
			ActiveHead: a.Session.ActiveHead,
		},
		RawSession: &slashcmd.RawSessionSnapshot{
			MessageCount:    len(a.steps),
			SummarySegments: len(a.RawSession.SummarySegments),
		},
		Model: derefModelUnit(req.Unit).Model,
	}
}

// shouldInferTitle reports whether this chat.submit should kick off async
// conversation-title inference. Coordinators are long-lived global assistants
// whose Title is their identity (the greeting name on the home page), so a
// per-conversation intent title must never be stamped onto them.
func shouldInferTitle(kind, turnTitle, cachedTitle, text string, hasAggregator bool) bool {
	if kind == domain.AgentKindCoordinator {
		return false
	}
	return turnTitle == "" && cachedTitle == "" && hasAggregator && text != ""
}

// decodeIntentResult extracts the inferred title text from an aggregator intent
// response. It handles direct Go types as well as serialized forms ([]byte,
// json.RawMessage, map) that may come back from cross-actor calls.
func decodeIntentResult(result any) string {
	switch v := result.(type) {
	case domain.SummarizeResp:
		return v.Text
	case *domain.SummarizeResp:
		if v != nil {
			return v.Text
		}
	case string:
		return v
	case []byte:
		var resp domain.SummarizeResp
		if err := json.Unmarshal(v, &resp); err == nil {
			return resp.Text
		}
		return strings.TrimSpace(string(v))
	case json.RawMessage:
		var resp domain.SummarizeResp
		if err := json.Unmarshal(v, &resp); err == nil {
			return resp.Text
		}
	}
	// Last resort: JSON round-trip anything else (e.g. map[string]any).
	body, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	var resp domain.SummarizeResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return ""
	}
	return resp.Text
}
