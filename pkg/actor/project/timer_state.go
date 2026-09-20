package project

import (
	"encoding/json"
	"strings"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	timerStatusIdle          = "idle"
	timerStatusRunning       = "running"
	timerStatusPendingReview = "pending_review"
	timerStatusFailed        = "failed"
)

func frontmatterValue(raw, wanted string) string {
	front, ok := cardFrontmatter(raw)
	if !ok {
		return ""
	}
	for _, line := range strings.Split(front, "\n") {
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == wanted {
			return unquote(strings.TrimSpace(value))
		}
	}
	return ""
}

func cardFrontmatter(raw string) (string, bool) {
	if !strings.HasPrefix(raw, "---") {
		return "", false
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return "", false
	}
	return raw[3 : 3+end], true
}

// saveTimerState persists timer runtime fields (run_status, last_run,
// executor_ref, reviewer_ref) into the card's data: block. The data block is
// the durable location the frontend round-trips verbatim: top-level
// frontmatter keys are dropped by the frontend's parse→format save path
// (web/src/domain/mono-types.ts formatMonoCard), which reset the run status to
// "none" after every modal edit. The dual-path readers (timerRuntimeField)
// still resolve legacy top-level values written before this fix.
func (a *Actor) saveTimerState(card *CardRecord, fields map[string]string) error {
	if card.Data == nil {
		card.Data = map[string]any{}
	}
	for key, value := range fields {
		card.Raw = setCardDataStringInRaw(card.Raw, key, value)
		card.Data[key] = value
	}
	if status, ok := fields["run_status"]; ok {
		card.Status = status
	}
	if lastRun, ok := fields["last_run"]; ok {
		card.Modified = lastRun
	}
	return a.store.Save(card)
}

// timerRuntimeField resolves a timer runtime scalar from the data: block with
// a legacy top-level frontmatter fallback (cards written before the field
// moved into the data block). Mirrors the dual-path pattern of
// currentInstance/workflowTemplate/boundAgent.
func timerRuntimeField(card *CardRecord, key string) string {
	if v := cardDataString(card, key); v != "" {
		return v
	}
	return frontmatterValue(card.Raw, key)
}

func lastRunOf(card *CardRecord) string     { return timerRuntimeField(card, "last_run") }
func executorRefOf(card *CardRecord) string { return timerRuntimeField(card, "executor_ref") }
func reviewerRefOf(card *CardRecord) string { return timerRuntimeField(card, "reviewer_ref") }

func timerState(card *CardRecord) string {
	status := timerRuntimeField(card, "run_status")
	if status == "" {
		return timerStatusIdle
	}
	return status
}

// workflowTemplate returns the bound workflow template id on a scheduler card,
// or "" when the card has no workflow binding. It reads CardRecord.Data first
// (where automation_bind writes data.workflow_template, round-tripped through
// parseDataBlock), then falls back to frontmatter top-level so cards authored
// per the design doc's top-level form are still recognized. This mirrors the
// resolveAgentRefs dual-path pattern.
func workflowTemplate(card *CardRecord) string {
	if v := cardDataString(card, "workflow_template"); v != "" {
		return v
	}
	return frontmatterValue(card.Raw, "workflow_template")
}

// currentInstance returns the instance map id created by the most recent timer
// fire of a workflow-bound scheduler card, or "" when none is active. Same
// dual-path resolution as workflowTemplate: data block first, top-level
// frontmatter fallback.
func currentInstance(card *CardRecord) string {
	if v := cardDataString(card, "current_instance"); v != "" {
		return v
	}
	return frontmatterValue(card.Raw, "current_instance")
}

// scheduleTypeWorkflow / scheduleTypePrompt are the two canonical values of a
// scheduler card's data.schedule_type (see the workflow|prompt contract shared
// with the frontend).
const (
	scheduleTypeWorkflow    = "workflow"
	scheduleTypePrompt      = "prompt"
	scheduleTypeAgentAction = "agent_action"
	scheduleTypeAgentTask   = "agent_task"
	// scheduleTypeTask is the unified scheduler type: it absorbs the legacy
	// prompt/workflow dichotomy. A bound workflow_template means template mode
	// (fire instantiates the template); everything else is prompt mode (fire
	// creates a fresh agent and submits the card body).
	scheduleTypeTask = "task"
)

// taskModeTemplate / taskModePrompt are the two derived modes of the unified
// schedule_type=task, exposed for badges and for the fire/validation dispatch.
const (
	taskModeTemplate = "template"
	taskModePrompt   = "prompt"
)

// schedulerBindModeBound / schedulerBindModeEphemeral are the two canonical
// values of a scheduler card's data.bind_mode. Bound mode keeps a stable agent
// (data.bound_agent) across fires and mounts builtin:mode:scheduler on it;
// ephemeral mode creates a fresh agent per run and unloads it (grey AgentRef)
// after completion. The constants are shared with the frontend contract.
const (
	schedulerBindModeBound     = "bound"
	schedulerBindModeEphemeral = "ephemeral"
)

// scheduleTypeOf returns the effective schedule type of a scheduler card. An
// explicit data.schedule_type wins (validation guarantees it is task,
// workflow, prompt, agent_action, or agent_task); otherwise the legacy
// derivation applies: a non-empty workflow_template implies workflow,
// everything else is prompt. Old cards without the field are never rewritten —
// this is a read-side derivation. An invalid explicit value is returned as-is
// so the caller decides how to reject it (execution errors, validation reports
// before persistence).
func scheduleTypeOf(card *CardRecord) string {
	if v := cardDataString(card, "schedule_type"); v != "" {
		return v
	}
	if workflowTemplate(card) != "" {
		return scheduleTypeWorkflow
	}
	return scheduleTypePrompt
}

// taskModeOf returns the derived task mode of a scheduler card for the unified
// schedule_type=task (and, read-only, for the legacy workflow/prompt values):
// a bound workflow_template means template mode, everything else means prompt
// mode. It is the single source of truth shared by badge data, fire dispatch,
// and validation.
func taskModeOf(card *CardRecord) string {
	if workflowTemplate(card) != "" {
		return taskModeTemplate
	}
	return taskModePrompt
}

// isTaskCard reports whether a scheduler card uses the unified schedule_type
// "task" (the value the frontend writes for every prompt/template task).
func isTaskCard(card *CardRecord) bool {
	return scheduleTypeOf(card) == scheduleTypeTask
}

// isPromptTaskCard reports whether a task card fires in prompt mode: unified
// schedule_type=task with no bound workflow_template. Prompt-mode task cards
// always create a fresh agent per fire (ephemeral semantics), so this flag is
// shared by the fire dispatch and the monitor completion path to keep the
// ephemeral unload consistent.
func isPromptTaskCard(card *CardRecord) bool {
	return isTaskCard(card) && workflowTemplate(card) == ""
}

// agentKindOf returns the agent kind a unified task card spawns its run agent
// with (data.agent_kind), or "" when the card carries no explicit kind — the
// spawn path defaults that to coder.
func agentKindOf(card *CardRecord) string {
	return cardDataString(card, "agent_kind")
}

// schedulerModelSlots parses the model-slot overrides a unified task card
// carries in data.model_slots as a JSON object string (keys
// primary/fast/execution/review/summary, each a ModelSlot JSON). JSON string is
// used because the cardstore's hand-rolled frontmatter parser does not
// round-trip maps nested inside list items (a ModelSlot's Candidates list).
// A slot absent from the card returns nil so the workspace spawn falls back to
// the agent-kind config default, matching workspace.create_agent's
// resolveAgentModelSlots behavior.
func schedulerModelSlots(card *CardRecord) (primary, fast, execution, review, summary *gen.ModelSlot) {
	raw := cardDataString(card, "model_slots")
	if raw == "" {
		return nil, nil, nil, nil, nil
	}
	var slots struct {
		Primary   *gen.ModelSlot `json:"primary"`
		Fast      *gen.ModelSlot `json:"fast"`
		Execution *gen.ModelSlot `json:"execution"`
		Review    *gen.ModelSlot `json:"review"`
		Summary   *gen.ModelSlot `json:"summary"`
	}
	if err := json.Unmarshal([]byte(raw), &slots); err != nil {
		return nil, nil, nil, nil, nil
	}
	return slots.Primary, slots.Fast, slots.Execution, slots.Review, slots.Summary
}

// bindModeOf returns the effective bind mode of a scheduler card. An explicit
// data.bind_mode wins; legacy cards without the field default to the bound
// mode (the original scheduler shape reused one agent across fires — the
// executor/target_agent semantics). An invalid explicit value is returned
// as-is so the caller decides how to reject it.
func bindModeOf(card *CardRecord) string {
	if v := cardDataString(card, "bind_mode"); v != "" {
		return v
	}
	return schedulerBindModeBound
}

// explicitBindMode returns the bind mode when the scheduler card explicitly
// declares data.bind_mode (bound or ephemeral), plus ok=false otherwise. It is
// the agent_task fire-branch trigger: a card must opt into the dual-mode
// scheduler contract; legacy prompt cards without the field keep the executor
// prompt flow regardless of bindModeOf's legacy default.
func explicitBindMode(card *CardRecord) (mode string, ok bool) {
	if v := cardDataString(card, "bind_mode"); v != "" {
		return v, true
	}
	if v := frontmatterValue(card.Raw, "bind_mode"); v != "" {
		return v, true
	}
	return "", false
}

// isAgentTaskCard reports whether a scheduler card fires through the agent_task
// branch: either schedule_type=agent_task is explicit, or the effective type is
// prompt and the card opts into the dual-mode contract with a valid explicit
// data.bind_mode (bound|ephemeral). It is the single source of truth shared by
// timerFireBranch (fire dispatch), advanceExecutor (completion dispatch), and
// validateSchedulerCard (field requirements) so the three never drift.
func isAgentTaskCard(card *CardRecord) bool {
	switch scheduleTypeOf(card) {
	case scheduleTypeAgentTask:
		return true
	case scheduleTypePrompt:
		mode, ok := explicitBindMode(card)
		return ok && (mode == schedulerBindModeBound || mode == schedulerBindModeEphemeral)
	default:
		return false
	}
}

// boundAgentOf returns the bound-mode target agent id on a scheduler card, or
// "" when the card is ephemeral or has no agent boundary yet. It reads
// data.bound_agent first (round-tripped through parseDataBlock), then falls
// back to frontmatter top-level — the same dual-path pattern as
// workflowTemplate / currentInstance.
func boundAgentOf(card *CardRecord) string {
	if v := cardDataString(card, "bound_agent"); v != "" {
		return v
	}
	return frontmatterValue(card.Raw, "bound_agent")
}

// lastRunAgentOf returns the agent id used by the most recent run of a
// scheduler card (ephemeral mode records the agent it created; bound mode
// records the bound agent that executed), or "" when no run is recorded.
// Same dual-path read as boundAgentOf.
func lastRunAgentOf(card *CardRecord) string {
	if v := cardDataString(card, "last_run_agent"); v != "" {
		return v
	}
	return frontmatterValue(card.Raw, "last_run_agent")
}

func timerNow() string { return time.Now().UTC().Format(time.RFC3339) }

// agentAction is one entry in the unified agent_actions list: a pause/resume
// command applied to a target agent reference.
type agentAction struct {
	Action string
	Target string
}

// agentActionsOf returns the unified agent_actions list from a scheduler card.
// The inline frontmatter parser does not support list-of-map items, so the
// YAML block form is parsed directly from the raw frontmatter. For backward
// compatibility with legacy agent_action cards, the old single-value
// agent_action + target_agent pair is returned as a one-item list when the
// card's effective schedule_type is agent_action and no unified list is present.
func agentActionsOf(card *CardRecord) []agentAction {
	if card == nil {
		return nil
	}
	if actions := agentActionsFromData(card.Data); len(actions) > 0 {
		return actions
	}
	if actions := parseAgentActionsList(card.Raw); len(actions) > 0 {
		return actions
	}
	if scheduleTypeOf(card) == scheduleTypeAgentAction {
		action := cardDataString(card, "agent_action")
		target := cardDataString(card, "target_agent")
		if a, ok := parseAgentAction(action, target); ok {
			return []agentAction{a}
		}
	}
	return nil
}

// agentActionsFromData reads a structured agent_actions value from the parsed
// data block. It accepts a []any of maps if the parser ever returns one, and as
// a fallback a JSON string like '[{"action":"pause","target":"agent:coder"}]'.
func agentActionsFromData(data map[string]any) []agentAction {
	if data == nil {
		return nil
	}
	if list, ok := data["agent_actions"].([]any); ok && len(list) > 0 {
		var out []agentAction
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			action, _ := m["action"].(string)
			target, _ := m["target"].(string)
			if a, ok := parseAgentAction(action, target); ok {
				out = append(out, a)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if text, ok := data["agent_actions"].(string); ok && text != "" {
		var list []agentAction
		if err := json.Unmarshal([]byte(text), &list); err == nil && len(list) > 0 {
			var out []agentAction
			for _, a := range list {
				if a2, ok := parseAgentAction(a.Action, a.Target); ok {
					out = append(out, a2)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

// parseAgentActionsList extracts a YAML block list of maps shaped like:
//
//	agent_actions:
//	  - action: pause
//	    target: agent:coder
//
// The key may live anywhere in the frontmatter (data block or top-level). Lines
// are matched by indentation only, so the function is intentionally tolerant of
// the exact nesting depth.
func parseAgentActionsList(raw string) []agentAction {
	front, ok := cardFrontmatter(raw)
	if !ok {
		return nil
	}
	lines := strings.Split(front, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.TrimSuffix(trimmed, ":") != "agent_actions" {
			continue
		}
		parentIndent := leadingSpaces(line)
		j := i + 1
		for ; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) != "" {
				break
			}
		}
		if j >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[j]), "- ") {
			return nil
		}
		itemBaseIndent := leadingSpaces(lines[j])
		var actions []agentAction
		current := map[string]string{}
		haveItem := false
		flush := func() {
			if !haveItem {
				return
			}
			if a, ok := parseAgentAction(current["action"], current["target"]); ok {
				actions = append(actions, a)
			}
			current = map[string]string{}
			haveItem = false
		}
		for ; j < len(lines); j++ {
			l := lines[j]
			t := strings.TrimSpace(l)
			if t == "" {
				continue
			}
			indent := leadingSpaces(l)
			if indent <= parentIndent && !strings.HasPrefix(t, "- ") {
				break
			}
			if strings.HasPrefix(t, "- ") {
				if indent != itemBaseIndent {
					break
				}
				flush()
				haveItem = true
				rest := strings.TrimSpace(strings.TrimPrefix(t, "- "))
				key, value, ok := strings.Cut(rest, ":")
				if ok {
					current[strings.TrimSpace(key)] = unquote(strings.TrimSpace(value))
				}
				continue
			}
			if !haveItem {
				continue
			}
			if indent <= itemBaseIndent {
				break
			}
			key, value, ok := strings.Cut(t, ":")
			if !ok {
				continue
			}
			current[strings.TrimSpace(key)] = unquote(strings.TrimSpace(value))
		}
		flush()
		return actions
	}
	return nil
}

// parseAgentAction normalizes one action/target pair. Only pause and resume are
// accepted, matching the legacy agent_action contract.
func parseAgentAction(action, target string) (agentAction, bool) {
	action = strings.ToLower(strings.TrimSpace(action))
	target = strings.TrimSpace(target)
	if action == "" || target == "" {
		return agentAction{}, false
	}
	if action != "pause" && action != "resume" {
		return agentAction{}, false
	}
	return agentAction{Action: action, Target: target}, true
}

// liquidateInstance marks an instance map card as failed when it is still
// active (neither done nor failed). Missing cards are a no-op. It is the
// clean-up primitive for orphaned/aborted instances: a stale current_instance
// being superseded by a new timer fire (F2) and an instance whose start
// failed (F3). Marking failed instead of deleting preserves the audit trail.
func (a *Actor) liquidateInstance(instID string) {
	inst, err := a.store.Get(instID)
	if err != nil {
		return // already deleted — nothing to liquidate
	}
	a.markCardFailed(inst)
}

// markCardFailed transitions a card to the canonical "failed" status in the
// frontmatter (and thus CardRecord.Status on re-read). Cards already in a
// terminal state (done/failed) are left untouched. Best-effort: persistence
// failure is swallowed because scheduler convergence must never depend on
// liquidation succeeding.
func (a *Actor) markCardFailed(card *CardRecord) {
	if card == nil || card.Status == "done" || card.Status == "failed" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	raw := setCardStatusInRaw(card.Raw, "failed")
	raw = ensureCardMeta(card.Title, raw, now)
	_ = a.store.Save(&CardRecord{Title: card.Title, Raw: raw})
}
