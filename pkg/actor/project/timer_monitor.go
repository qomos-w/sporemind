package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Scheduler convergence sentinels. checkAgentIdle surfaces these so the monitor
// can tell a transient poll failure (retry next tick) apart from a terminal
// condition that must converge the run instead of wedging it.
var (
	// errAgentNotFound marks an executor/reviewer agent whose actor can no
	// longer be resolved (deleted, or its registry row lost across a restart).
	errAgentNotFound = errors.New("scheduler: agent not found")
	// errAgentFailed marks an executor/reviewer agent that reports a failed
	// turn state; the run can never complete and must converge to failed.
	errAgentFailed = errors.New("scheduler: agent failed")
)

// isAgentConvergenceError reports whether a checkAgentIdle error is a terminal
// condition (agent gone / agent failed) rather than a transient poll error.
func isAgentConvergenceError(err error) bool {
	return errors.Is(err, errAgentNotFound) || errors.Is(err, errAgentFailed)
}

func (a *Actor) handleTimerCheck(ctx actor.PureContext) error {
	cards, err := a.store.List()
	if err != nil {
		return fmt.Errorf("project.timer.check: list cards: %w", err)
	}
	for _, card := range cards {
		if !strings.HasPrefix(card.Title, schedulerCardPrefix) || parseScheduleFromFrontmatter(card.Raw).Cron == "" && parseScheduleFromFrontmatter(card.Raw).Expression == "" {
			continue
		}
		switch timerState(card) {
		case timerStatusRunning:
			if err := a.advanceExecutor(ctx, card); err != nil {
				ctx.Logger().Warn("project: timer executor check failed", "card", card.Title, "error", err)
			}
		case timerStatusPendingReview:
			if err := a.advanceReviewer(ctx, card); err != nil {
				ctx.Logger().Warn("project: timer reviewer check failed", "card", card.Title, "error", err)
			}
		}
	}

	a.workflowUpdaterTick(ctx)

	// Retry removal of worktree directories left behind by locked handles
	// after a successful workflow_stop merge; no-op when nothing is residual.
	a.sweepResidualWorktrees()

	return ctx.After(monitorInterval, "project.timer_check", nil)
}

func (a *Actor) advanceExecutor(ctx actor.PureContext, card *CardRecord) error {
	// agent_action cards never enter running/pending_review: the fire path
	// records run_status=idle immediately after the pause/resume invoke
	// returns. If the monitor still sees one here it is stale state (e.g. a
	// type changed between fire and tick) — converge without polling agents,
	// which would fail on the empty executor_ref.
	if scheduleTypeOf(card) == scheduleTypeAgentAction {
		return a.monitorSaveTimerState(card, map[string]string{"run_status": timerStatusIdle}, false)
	}

	// Unified task cards that carry only agent_actions never enter a real running
	// state; if stale state (schedule_type changed, hand-edited frontmatter, etc.)
	// leaves them at running, converge to idle immediately instead of polling an
	// empty executor_ref or trying to create an ephemeral agent.
	if isTaskCard(card) && taskModeOf(card) == taskModePrompt && strings.TrimSpace(card.Body) == "" && len(agentActionsOf(card)) > 0 {
		return a.monitorSaveTimerState(card, map[string]string{"run_status": timerStatusIdle}, false)
	}

	// agent_task cards are tracked through the same idle-based completion check
	// as prompt cards (the run agent goes idle when the body turn finishes),
	// but the ephemeral sub-branch also unloads the run agent and clears
	// last_run_agent once the run completes. isAgentTaskCard mirrors
	// timerFireBranch so prompt cards that opt in via bind_mode also complete
	// through this path. Unified task cards in prompt mode fire through the
	// same ephemeral path (create-new-agent semantics, no executor/bind_mode),
	// so they complete here too — otherwise the fresh run agent would never be
	// unloaded.
	if isAgentTaskCard(card) || isPromptTaskCard(card) {
		return a.advanceAgentTaskExecutor(ctx, card)
	}

	executor := executorRefOf(card)
	reviewer := reviewerRefOf(card)

	// Workflow completion: a legacy workflow card and a template-mode unified
	// task card (schedule_type=task + workflow_template) both fire through
	// executeWorkflowTimerCard and record current_instance, so their run
	// completes on the instance map's status — NOT on the executor agent going
	// idle. Routing template-mode task cards here is essential: otherwise a
	// failed/cancelled instance was judged by agent idle and produced a false
	// "completed".
	if scheduleTypeOf(card) == scheduleTypeWorkflow || (isTaskCard(card) && taskModeOf(card) == taskModeTemplate) {
		return a.advanceWorkflowExecutor(ctx, card, executor, reviewer)
	}

	if executor == "" {
		// A running non-workflow card with no executor can never be completed by
		// polling an agent. Converge to the failed terminal state instead of
		// returning the same error every tick: the in-flight guard would
		// otherwise block every later fire behind a permanently running card.
		return a.convergeRunFailed(ctx, card, "", "failed", "Scheduler run has no executor agent to poll.")
	}

	done, err := a.checkAgentIdle(ctx, executor)
	if err != nil {
		if isAgentConvergenceError(err) {
			return a.convergeRunFailed(ctx, card, executor, "failed", err.Error())
		}
		return err
	}
	if !done {
		// Still running (or paused/unknown): keep polling.
		return nil
	}
	if reviewer == "" {
		a.createResultCard(ctx, pendingExecution{CardID: card.Title, ExecutorActorID: executor, Tags: card.Tags}, "completed", "")
		return a.monitorSaveTimerState(card, map[string]string{"run_status": timerStatusIdle}, false)
	}
	if err := a.triggerReviewer(ctx, pendingExecution{CardID: card.Title, ExecutorActorID: executor, ReviewerActorID: reviewer}); err != nil {
		return err
	}
	return a.monitorSaveTimerState(card, map[string]string{"run_status": timerStatusPendingReview}, false)
}

// advanceWorkflowExecutor is the workflow completion path for advanceExecutor.
// Instead of checking the executor agent's idle state, it checks whether the
// instance map created by the last timer fire has reached a terminal status
// ("done", "failed", "cancelled", "blocked" — set by workflow_stop and the
// failure paths). When done, it mirrors the agent-idle path: creates a result
// card and transitions to idle (or pending_review if a reviewer is configured).
// A terminal non-completion — failed/cancelled/blocked — or a missing instance
// map creates a failed result card and transitions run_status to the failed
// terminal state so the scheduler stops polling and the in-flight guard no
// longer blocks later fires.
func (a *Actor) advanceWorkflowExecutor(ctx actor.PureContext, card *CardRecord, executor, reviewer string) error {
	instID := currentInstance(card)
	if instID == "" {
		// No instance to track: converge to the failed terminal state rather
		// than returning an error that would keep the monitor polling forever.
		return a.convergeRunFailed(ctx, card, executor, "failed",
			"Workflow-bound scheduler run has no current_instance; run marked failed.")
	}
	inst, err := a.store.Get(instID)
	if err != nil {
		// The instance map is gone (deleted or unreadable). Converge to the
		// failed terminal state instead of returning an error that would keep
		// the monitor polling forever.
		ctx.Logger().Warn("project: instance map unreadable, converging scheduler to failed", "card", card.Title, "instance", instID, "error", err)
		return a.convergeRunFailed(ctx, card, executor, "failed",
			fmt.Sprintf("Instance map **%s** could not be read; run marked failed.", instID))
	}
	// Terminal convergence: "done" completes the run; failed/cancelled/blocked
	// are terminal non-completions that must stop the poll. Previously only
	// "failed" converged, so a cancelled/blocked instance wedged the card at
	// running forever and the in-flight guard blocked every later fire.
	switch inst.Status {
	case "done":
		// completed — fall through to the completion path
	case "failed", "cancelled", "blocked":
		a.createResultCard(ctx, pendingExecution{CardID: card.Title, ExecutorActorID: executor, Tags: card.Tags},
			inst.Status, fmt.Sprintf("Instance map **%s** reached terminal status %q.", instID, inst.Status))
		return a.monitorSaveTimerState(card, map[string]string{"run_status": timerStatusFailed}, false)
	default:
		return nil // instance still running
	}
	if reviewer == "" {
		a.createResultCard(ctx, pendingExecution{CardID: card.Title, ExecutorActorID: executor, Tags: card.Tags}, "completed", "")
		return a.monitorSaveTimerState(card, map[string]string{"run_status": timerStatusIdle}, false)
	}
	if err := a.triggerReviewer(ctx, pendingExecution{CardID: card.Title, ExecutorActorID: executor, ReviewerActorID: reviewer}); err != nil {
		return err
	}
	return a.monitorSaveTimerState(card, map[string]string{"run_status": timerStatusPendingReview}, false)
}

// advanceAgentTaskExecutor is the agent_task completion path for
// advanceExecutor. Completion is detected via the run agent's idle state —
// exactly like the prompt branch, because the run body is a chat_submit turn.
// The ephemeral sub-branch (and every prompt-mode task card, which always
// creates a fresh agent) then unloads the run agent (workspace.agent_unload,
// keeping the gray lazy-loadable AgentRef) and clears data.last_run_agent; the
// bound sub-branch keeps the agent resident (the scheduler mode stays
// mounted).
func (a *Actor) advanceAgentTaskExecutor(ctx actor.PureContext, card *CardRecord) error {
	executor := executorRefOf(card)
	if executor == "" {
		// No run agent recorded: the run can never be observed to complete.
		// Converge to failed instead of erroring every tick.
		return a.convergeRunFailed(ctx, card, "", "failed", "agent_task run has no executor agent to poll.")
	}
	done, err := a.checkAgentIdle(ctx, executor)
	if err != nil {
		if isAgentConvergenceError(err) {
			return a.convergeRunFailed(ctx, card, executor, "failed", err.Error())
		}
		return err
	}
	if !done {
		return nil
	}

	a.createResultCard(ctx, pendingExecution{CardID: card.Title, ExecutorActorID: executor, Tags: card.Tags}, "completed", "")

	// ephemeral cleanup: between the result card and the idle transition, take
	// down the run agent and drop the last_run_agent binding (R1 hook point).
	// Unified task cards default to ephemeral (fresh agent per fire); legacy
	// agent_task cards unload only when they explicitly opted into the
	// ephemeral mode. An explicit bind_mode overrides the default in both
	// directions: a task card bound to a stable agent keeps it resident.
	ephemeral := isPromptTaskCard(card)
	if mode, explicit := explicitBindMode(card); explicit {
		ephemeral = mode == schedulerBindModeEphemeral
	}
	fields := map[string]string{"run_status": timerStatusIdle}
	if ephemeral {
		if lastAgent := lastRunAgentOf(card); lastAgent != "" {
			// Unbind BEFORE the unload destroys the run agent: scheduler_unbind
			// clears the persisted ActiveScheduler entry and unmounts the
			// scheduler mode. Skipping it leaves the binding in the mailbox, so
			// a lazy reload revives a stale scheduler badge on the gray agent.
			a.unbindSchedulerAgent(ctx, executor, card.Title)
			a.unloadSchedulerAgent(ctx, lastAgent)
		}
		fields["last_run_agent"] = ""
	}
	return a.monitorSaveTimerState(card, fields, false)
}

func (a *Actor) advanceReviewer(ctx actor.PureContext, card *CardRecord) error {
	reviewer := reviewerRefOf(card)
	if reviewer == "" {
		return a.convergeRunFailed(ctx, card, "", "failed", "pending_review run has no reviewer agent to poll.")
	}
	done, err := a.checkAgentIdle(ctx, reviewer)
	if err != nil {
		if isAgentConvergenceError(err) {
			return a.convergeRunFailed(ctx, card, reviewer, "failed", err.Error())
		}
		return err
	}
	if !done {
		return nil
	}
	a.createResultCard(ctx, pendingExecution{CardID: card.Title, ReviewerActorID: reviewer, Tags: card.Tags}, "completed", "")
	return a.monitorSaveTimerState(card, map[string]string{"run_status": timerStatusIdle}, false)
}

func (a *Actor) checkAgentIdle(ctx actor.PureContext, agentActorID string) (bool, error) {
	return a.checkAgentIdleTimeout(ctx, agentActorID, workflowPollTimeout)
}

func (a *Actor) checkAgentIdleTimeout(ctx actor.PureContext, agentActorID string, timeout time.Duration) (bool, error) {
	agentRef, ok := a.lookupAgentRef(ctx, agentActorID)
	if !ok {
		return false, fmt.Errorf("%w: %s", errAgentNotFound, agentActorID)
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), timeout)
	defer cancel()
	call := agentRef.Invoke(callCtx, "agent_status", nil)
	if call == nil {
		return false, fmt.Errorf("agent.status returned nil")
	}
	defer call.Close()
	v, err := call.Final(callCtx)
	if err != nil {
		return false, fmt.Errorf("agent.status: %w", err)
	}
	// A nil/untyped status response is not evidence of completion: keep polling
	// instead of declaring a false "completed".
	if v == nil {
		return false, nil
	}
	var status gen.AgentStatusResp
	switch s := v.(type) {
	case gen.AgentStatusResp:
		status = s
	default:
		body, _ := json.Marshal(v)
		_ = json.Unmarshal(body, &status)
	}
	// Only a genuinely idle agent completes the run. An agent's idle state is
	// reported as "" (the topology surfaces it as "idle"). "failed" converges
	// the run to failed. Everything else — running, paused, waiting, abandoned,
	// unknown — keeps polling rather than producing a false "completed".
	switch status.State {
	case "", "idle":
		return true, nil
	case "failed":
		return false, fmt.Errorf("%w: %s", errAgentFailed, agentActorID)
	default:
		return false, nil
	}
}

func (a *Actor) triggerReviewer(ctx actor.PureContext, pe pendingExecution) error {
	reviewerRef, ok := a.lookupAgentRef(ctx, pe.ReviewerActorID)
	if !ok {
		return fmt.Errorf("reviewer agent not found: %s", pe.ReviewerActorID)
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 30*time.Second)
	defer cancel()
	call := reviewerRef.Invoke(callCtx, "chat_submit", gen.AgentChatSubmitReq{
		Text: fmt.Sprintf("Review the output of timer card %s executed by %s. Provide a brief assessment.", pe.CardID, pe.ExecutorActorID),
	})
	if call == nil {
		return fmt.Errorf("agent.chat.submit returned nil for reviewer")
	}
	_ = call.Close()
	return nil
}

func (a *Actor) createResultCard(ctx actor.PureContext, pe pendingExecution, status, body string) {
	ts := time.Now().UTC().Format("2006-01-02-150405")
	resultID := fmt.Sprintf("%s%s#%s", schedulerCardPrefix, strings.TrimPrefix(pe.CardID, schedulerCardPrefix), ts)
	tags := append([]string{}, pe.Tags...)
	tags = append(tags, "automation-log")
	now := timerNow()
	cardBody := fmt.Sprintf("Timer card **%s** executed at %s.\n\n- Status: %s\n- Executor: %s\n", pe.CardID, now, status, pe.ExecutorActorID)
	if pe.ReviewerActorID != "" {
		cardBody += fmt.Sprintf("- Reviewer: %s\n", pe.ReviewerActorID)
	}
	if strings.TrimSpace(body) != "" {
		cardBody += "\n" + body + "\n"
	}
	// The timestamp has second granularity, so two completions in the same
	// second would otherwise collide on the same title and overwrite each
	// other. Disambiguate with a stable numeric suffix.
	base := fmt.Sprintf("%s #%s", pe.CardID, ts)
	title := base
	for i := 2; ; i++ {
		if _, err := a.store.Get(title); err != nil {
			break
		}
		title = fmt.Sprintf("%s-%d", base, i)
	}
	raw := fmt.Sprintf("---\nid: %s\ntags: [%s]\ncreated: %s\nmodified: %s\n---\n\n%s", title, strings.Join(tags, ", "), now, now, cardBody)
	if err := a.store.Save(&CardRecord{Title: title, Tags: tags, Created: now, Modified: now, Body: cardBody, Raw: raw}); err != nil {
		ctx.Logger().Error("project: create timer result card failed", "id", resultID, "error", err)
	}
}

// monitorSaveTimerState persists a monitor-driven run transition. Before saving
// it re-reads the card so the tick's stale List snapshot cannot silently
// overwrite a concurrent user edit made between the List and this Save
// (verify-replay). The monitor's own field writes are then replayed on top of
// the fresh raw. When markFailed is set the scheduler card's own frontmatter
// status is stamped failed as well (markCardFailed semantics), which is only
// used for runs that can never complete (missing agent/executor/instance).
func (a *Actor) monitorSaveTimerState(card *CardRecord, fields map[string]string, markFailed bool) error {
	if fresh, err := a.store.Get(card.Title); err == nil && fresh.Raw != "" {
		card.Raw = fresh.Raw
		card.Data = fresh.Data
	}
	if markFailed {
		card.Raw = setCardStatusInRaw(card.Raw, "failed")
	}
	return a.saveTimerState(card, fields)
}

// convergeRunFailed terminates a scheduler run in the failed state: a failed
// result card for the audit trail, a frontmatter failed status, and
// run_status=failed so the monitor stops polling and the in-flight guard no
// longer blocks later fires. It is the shared convergence primitive for runs
// that can never complete (agent gone/failed, no executor, no reviewer, no
// instance).
func (a *Actor) convergeRunFailed(ctx actor.PureContext, card *CardRecord, executor, resultStatus, body string) error {
	a.createResultCard(ctx, pendingExecution{CardID: card.Title, ExecutorActorID: executor, Tags: card.Tags}, resultStatus, body)
	return a.monitorSaveTimerState(card, map[string]string{"run_status": timerStatusFailed}, true)
}

func (a *Actor) handleListTimers(ctx actor.PureContext) (gen.WikiListTimersResp, error) {
	schedRef, ok := ctx.LookupService("scheduler")
	if !ok || schedRef == nil {
		return gen.WikiListTimersResp{}, fmt.Errorf("project.wiki.list_timers: scheduler service not found")
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := schedRef.Invoke(callCtx, "scheduler.list", gen.SchedulerListReq{})
	if call == nil {
		return gen.WikiListTimersResp{}, fmt.Errorf("project.wiki.list_timers: scheduler.list returned nil")
	}
	defer call.Close()
	v, err := call.Final(callCtx)
	if err != nil {
		return gen.WikiListTimersResp{}, fmt.Errorf("project.wiki.list_timers: scheduler.list: %w", err)
	}
	listResp, ok := v.(gen.SchedulerListResp)
	if !ok {
		return gen.WikiListTimersResp{}, fmt.Errorf("project.wiki.list_timers: unexpected scheduler.list response type %T", v)
	}

	var timers []gen.WikiTimerListItem
	for _, entry := range listResp.Entries {
		if entry.ProjectID != "" && entry.ProjectID != a.actorID {
			continue
		}
		item := gen.WikiTimerListItem{
			ID:         entry.CardID,
			NextFireAt: entry.NextFireAt,
			Enabled:    entry.Schedule.Enabled,
		}
		card, err := a.store.Get(entry.CardID)
		if err == nil {
			item.LastRunAt = lastRunOf(card)
			item.LastStatus = timerState(card)
			item.CurrentInstance = currentInstance(card)
			item.ScheduleType = scheduleTypeOf(card)
			// Unified task cards (and the legacy workflow/prompt/agent_task
			// values feeding the same model) expose the derived task mode plus
			// the spawn/display fields for the task mode badge: the prompt vs
			// template mode, the agent kind a prompt-mode fire creates, and the
			// bound template. agent_action cards are a different contract and
			// leave these empty.
			if item.ScheduleType != scheduleTypeAgentAction {
				item.TaskMode = taskModeOf(card)
				item.AgentKind = agentKindOf(card)
				item.BoundTemplate = workflowTemplate(card)
			}
		}
		timers = append(timers, item)
	}
	return gen.WikiListTimersResp{Timers: timers}, nil
}

func (a *Actor) handleToggleTimer(ctx actor.PureContext, req gen.WikiToggleTimerReq) (gen.WikiToggleTimerResp, error) {
	if !strings.HasPrefix(req.ID, schedulerCardPrefix) {
		return gen.WikiToggleTimerResp{}, fmt.Errorf("project.wiki.toggle_timer: %q is not a scheduler card", req.ID)
	}

	card, err := a.store.Get(req.ID)
	if err != nil {
		return gen.WikiToggleTimerResp{}, fmt.Errorf("project.wiki.toggle_timer: get card: %w", err)
	}

	updated := setScheduleEnabled(card.Raw, req.Enabled)
	if updated == card.Raw {
		// Already in the desired state; still tell the scheduler.
	}
	card.Raw = updated
	card.Modified = time.Now().UTC().Format(time.RFC3339)
	if err := a.store.Save(card); err != nil {
		return gen.WikiToggleTimerResp{}, fmt.Errorf("project.wiki.toggle_timer: save card: %w", err)
	}

	schedRef, ok := ctx.LookupService("scheduler")
	if !ok || schedRef == nil {
		return gen.WikiToggleTimerResp{}, fmt.Errorf("project.wiki.toggle_timer: scheduler service not found")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := schedRef.Invoke(callCtx, "scheduler.set_enabled", gen.SchedulerSetEnabledReq{
		ProjectID: a.actorID,
		CardID:    req.ID,
		Enabled:   req.Enabled,
	})
	if call == nil {
		return gen.WikiToggleTimerResp{}, fmt.Errorf("project.wiki.toggle_timer: scheduler.set_enabled returned nil")
	}
	defer call.Close()
	if _, err := call.Final(callCtx); err != nil {
		return gen.WikiToggleTimerResp{}, fmt.Errorf("project.wiki.toggle_timer: scheduler.set_enabled: %w", err)
	}

	return gen.WikiToggleTimerResp{ID: req.ID, Enabled: req.Enabled}, nil
}

// setScheduleEnabled updates the `enabled` field inside the card's schedule
// block. It writes into whichever layout the parser reads: `data.schedule`
// when present (current layout), otherwise a legacy top-level `schedule:`
// block. The previous behavior — writing a top-level `enabled:` key when the
// card had no data block — was invisible to parseTopLevelSchedule, so the next
// sync re-registered the timer as enabled and a "disabled" timer silently
// restarted on any later card edit.
func setScheduleEnabled(raw string, value bool) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return raw
	}
	end += 3
	front := raw[:end]
	body := raw[end:]

	lines := strings.Split(front, "\n")
	type lineInfo struct {
		raw     string
		indent  int
		trimmed string
		key     string
		value   string
	}
	infos := make([]lineInfo, len(lines))
	for i, line := range lines {
		li := lineInfo{raw: line}
		li.indent = len(line) - len(strings.TrimLeft(line, " \t"))
		li.trimmed = strings.TrimSpace(line)
		if key, val, ok := strings.Cut(li.trimmed, ":"); ok {
			li.key = strings.TrimSpace(key)
			li.value = strings.TrimSpace(val)
		}
		infos[i] = li
	}

	dataIdx := -1
	for i, li := range infos {
		if li.indent == 0 && li.key == "data" {
			dataIdx = i
			break
		}
	}

	// Preferred: the schedule block nested under data: (parser precedence).
	scheduleIdx := -1
	if dataIdx >= 0 {
		for i := dataIdx + 1; i < len(infos); i++ {
			li := infos[i]
			if li.indent <= infos[dataIdx].indent {
				break
			}
			if li.key == "schedule" {
				scheduleIdx = i
				break
			}
		}
	}
	if scheduleIdx < 0 {
		// Legacy layout: a top-level schedule: block the parser also accepts.
		for i, li := range infos {
			if li.indent == 0 && li.key == "schedule" {
				scheduleIdx = i
				break
			}
		}
	}

	if scheduleIdx < 0 {
		// No schedule block anywhere: create one where the parser will read it
		// — under data when a data block exists, else at top level.
		if dataIdx >= 0 {
			insertAt := dataIdx + 1
			for insertAt < len(infos) && infos[insertAt].indent > infos[dataIdx].indent {
				insertAt++
			}
			block := []string{"  schedule:", "    enabled: " + strconv.FormatBool(value)}
			lines = append(lines[:insertAt], append(block, lines[insertAt:]...)...)
		} else {
			insertAt := len(lines) - 1
			for insertAt > 0 && strings.TrimSpace(lines[insertAt]) == "" {
				insertAt--
			}
			if strings.TrimSpace(lines[insertAt]) == "---" {
				insertAt--
			}
			block := []string{"schedule:", "  enabled: " + strconv.FormatBool(value)}
			lines = append(lines[:insertAt+1], append(block, lines[insertAt+1:]...)...)
		}
		return strings.Join(lines, "\n") + body
	}

	enabledIdx := -1
	insertAfter := scheduleIdx
	for i := scheduleIdx + 1; i < len(infos); i++ {
		li := infos[i]
		if li.indent <= infos[scheduleIdx].indent {
			break
		}
		insertAfter = i
		if li.key == "enabled" {
			enabledIdx = i
			break
		}
	}

	enabledLine := strings.Repeat(" ", infos[scheduleIdx].indent+2) + "enabled: " + strconv.FormatBool(value)
	if enabledIdx >= 0 {
		lines[enabledIdx] = enabledLine
	} else {
		lines = append(lines[:insertAfter+1], append([]string{enabledLine}, lines[insertAfter+1:]...)...)
	}
	return strings.Join(lines, "\n") + body
}
