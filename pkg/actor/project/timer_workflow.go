package project

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// wfOwnerState summarises one bound owner agent's state relevant to the updater.
type wfOwnerState struct {
	ActorID           string // workspace AgentRef.ActorID — the id workspace.agent_review matches on
	AgentID           string
	TaskCardID        string // bound task card id (status.Goal.BoundTaskCardID)
	State             string // agent.status.State: idle/running/failed/paused
	PauseKind         string // agent.status.PauseKind: user/task/""
	GoalStatus        string // ready_for_review / active / ""
	// BoundCardStatus is the canonical status of the worker's bound task
	// card (done/failed/cancelled/...), fetched only when GoalStatus ==
	// ready_for_review so the updater can reconcile: a ready_for_review
	// worker whose card is already resolved is an orphan — agent_review
	// approve would fail the card CAS, so the owner must agent_terminate.
	BoundCardStatus   string
	WorkflowMapCardID string
	LoadState         string
	IsDead            bool
	WorktreeBranch    string // child worktree's git branch name (empty when no worktree)
}

// wfWorkerEvent represents one worker needing owner attention.
type wfWorkerEvent struct {
	ActorID        string // workspace AgentRef.ActorID — pass this to workspace.agent_review
	AgentID        string
	TaskCardID     string
	EventType      string // ready_for_review | failed | dead
	WorktreeBranch string // child branch name for merge instructions
}

// wfUpdaterInput is the set of inputs to the pure notification-decision function.
// Worker events (ready_for_review / failed / dead) are handled separately by
// collectWorkerEvents before updaterDecide is called; this struct covers only
// structural notifications.
type wfUpdaterInput struct {
	RootDone  bool
	OwnerBusy bool
	// MapID and Frontier carry the structural detail the owner needs to act
	// without re-querying: which map and exactly which tickets are claimable.
	MapID          string
	Frontier       []string
	HasActiveOwner bool
}

// wfUpdaterDecision is the notification the updater should send (empty Message = none).
type wfUpdaterDecision struct {
	Notify  bool
	Message string
}

// updaterDecide is the pure function: given orchestration state, decide whether
// to notify the map owner and what message to send. Deterministic, side-effect-free.
// Worker events (ready_for_review / failed / dead) are collected and aggregated
// by collectWorkerEvents before this function is called; updaterDecide covers
// only structural notifications (frontier, tree exhausted).
func updaterDecide(in wfUpdaterInput) wfUpdaterDecision {
	if in.RootDone {
		return wfUpdaterDecision{}
	}
	if in.OwnerBusy {
		return wfUpdaterDecision{}
	}
	if len(in.Frontier) > 0 {
		return wfUpdaterDecision{Notify: true, Message: formatFrontierMessage(in.MapID, in.Frontier)}
	}
	if in.HasActiveOwner {
		return wfUpdaterDecision{}
	}
	// Tree exhausted: no frontier, no workers needing attention. The owner
	// must assess whether the overall goal is met before completing or
	// extending the tree.
	return wfUpdaterDecision{Notify: true, Message: "No frontier tasks or active workers remain. Assess whether the overall goal is met. If it is: commit all work in your worktree, rebase it onto the latest main branch (git rebase <base>), resolve conflicts and verify tests, then call workflow_stop (it verifies the worktree contains the latest main HEAD before merging back). Otherwise extend the task tree and continue."}
}

// ── worker event aggregation (pure functions) ──

// wfNudgePrefix is the sentinel prefix shared by all nudge messages. It is
// used for dedup/escalation counting: any history entry starting with this
// prefix counts as a nudge.
const wfNudgePrefix = "We'll continue"

// wfNudgeFormat is the template for nudge messages. The %s placeholder is
// replaced with a short structural hint (e.g. "2 to review, 1 unresponsive")
// so the owner knows what to continue on without re-reading the full message.
const wfNudgeFormat = "We'll continue: %s."

// wfNudgeSentinel is the bare nudge used when no hint is available.
const wfNudgeSentinel = "We'll continue."

// isNudge reports whether s is a nudge entry (starts with the nudge prefix).
func isNudge(s string) bool {
	return strings.HasPrefix(s, wfNudgePrefix)
}

// makeNudge builds a nudge message from a structural hint. When the hint is
// empty the bare sentinel is used.
func makeNudge(hint string) string {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return wfNudgeSentinel
	}
	return fmt.Sprintf(wfNudgeFormat, hint)
}

// wfNudgeClosers are randomly appended to the nudge base text so repeated
// nudges are never byte-identical. An owner LLM receiving the same prompt
// again replays the same (non-)action and death-loops; the closers also push
// it to break the loop. The nudge prefix stays intact so isNudge (and with it
// dedup + escalation counting) keeps classifying the varied text.
var wfNudgeClosers = []string{
	"",
	" (this is unchanged since the last reminder; we'll take a different action this time)",
	" (this condition has persisted; we should reconsider our approach)",
	" (if we already tried this and it did not help, we'll try something else)",
	" (progress is stalled — we'll re-check what is actually blocking it)",
	" (we won't just wait; we'll act on the outstanding item now)",
}

// nudgeWithCloser appends closers[idx] to the nudge base text.
func nudgeWithCloser(nudge string, idx int) string {
	return nudge + wfNudgeClosers[idx%len(wfNudgeClosers)]
}

// varyNudge returns the nudge with a randomly picked closer.
func varyNudge(nudge string) string {
	return nudgeWithCloser(nudge, rand.IntN(len(wfNudgeClosers)))
}

// nudgeHintForEvents builds a short structural summary from worker events,
// prefixed with an action verb so it reads naturally after "We'll continue:".
// e.g. "review 2 ready for review, handle 1 unresponsive".
func nudgeHintForEvents(events []wfWorkerEvent) string {
	groups := map[string][]wfWorkerEvent{}
	var order []string
	for _, e := range events {
		label := eventLabel(e.EventType)
		if _, ok := groups[label]; !ok {
			order = append(order, label)
		}
		groups[label] = append(groups[label], e)
	}
	var parts []string
	for _, label := range order {
		n := len(groups[label])
		verb := nudgeVerb(label)
		noun := label
		if n > 1 {
			noun = pluralizeNudgeNoun(label)
		}
		parts = append(parts, fmt.Sprintf("%s %d %s", verb, n, noun))
	}
	return strings.Join(parts, ", ")
}

// nudgeVerb returns the action verb for an event label.
func nudgeVerb(label string) string {
	switch label {
	case "ready for review":
		return "review"
	case "orphaned review":
		return "dispose"
	case "orphaned review card":
		return "dispose"
	case "failed":
		return "retry"
	case "unresponsive":
		return "handle"
	default:
		return "address"
	}
}

// pluralizeNudgeNoun returns the plural form of an event label for counts > 1.
func pluralizeNudgeNoun(label string) string {
	switch label {
	case "ready for review":
		return "ready for review"
	case "orphaned review":
		return "orphaned reviews"
	case "orphaned review card":
		return "orphaned review cards"
	case "failed":
		return "failures"
	case "unresponsive":
		return "unresponsive"
	default:
		return label + "s"
	}
}

// nudgeHintForFrontier builds a short hint from the frontier ticket count.
func nudgeHintForFrontier(count int) string {
	if count <= 0 {
		return ""
	}
	noun := "frontier tickets"
	if count == 1 {
		noun = "frontier ticket"
	}
	return fmt.Sprintf("advance %d %s", count, noun)
}

// wfNotifyAction is the outcome of the dedup decision.
type wfNotifyAction int

const (
	wfNotifySend     wfNotifyAction = iota // send the original message
	wfNotifyContinue                       // send the nudge text instead
	wfNotifyUnbind                         // unbind the owner and stop
)

// wfNotifyMeta marks updater chat_submit notifications so the frontend can
// distinguish system continuation messages from human user submits. It rides
// on AgentChatSubmitReq.Meta only; the message Text is never altered.
const wfNotifyMeta = "workflow"

// ownerConsumedSince reports whether the owner completed a turn after
// dispatchedAt — evidence a notification dispatched at that time was actually
// consumed. chat_submit is fire-and-forget: a backlogged agent accepts the
// frame without ever processing it (it never flips to running), so delivery
// alone must not be read as "seen". A zero dispatchedAt (no real message sent
// yet) or empty LastTurnCompletedAt means unconsumed.
func ownerConsumedSince(status *gen.AgentStatusResp, dispatchedAt time.Time) bool {
	if status == nil || dispatchedAt.IsZero() || status.LastTurnCompletedAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, status.LastTurnCompletedAt)
	if err != nil {
		return false
	}
	return !t.Before(dispatchedAt)
}

// decideNotify examines the message history and returns what to do.
// history is the slice of messages actually sent (including nudge entries).
// maxContinue is the threshold: after this many consecutive nudge entries, unbind.
// nudgeHint is the short structural summary embedded in the nudge text.
// Returns the action to take and the message to send (only meaningful for Send/Continue).
func decideNotify(message string, history []string, maxContinue int, nudgeHint string) (wfNotifyAction, string) {
	// 1. Count trailing consecutive nudge entries.
	trailing := 0
	for i := len(history) - 1; i >= 0 && isNudge(history[i]); i-- {
		trailing++
	}
	// 2. The owner has been nudged too many times in a row: unbind and stop.
	if trailing >= maxContinue {
		return wfNotifyUnbind, ""
	}
	nudge := makeNudge(nudgeHint)
	// 3. The last message sent is identical to this one: nudge instead.
	if len(history) > 0 && history[len(history)-1] == message {
		return wfNotifyContinue, nudge
	}
	// 4. The same message was sent within the last maxContinue sends and every
	//    entry after it was a nudge: the owner has already seen it, so nudge
	//    instead of resending.
	lo := len(history) - maxContinue
	if lo < 0 {
		lo = 0
	}
	for i := len(history) - 1; i >= lo; i-- {
		if history[i] != message {
			continue
		}
		allContinue := true
		for j := i + 1; j < len(history); j++ {
			if !isNudge(history[j]) {
				allContinue = false
				break
			}
		}
		if allContinue {
			return wfNotifyContinue, nudge
		}
	}
	// 5. Otherwise resend the original message.
	return wfNotifySend, message
}

// classifyWorkerEvent returns the notification event type for a single worker,
// or "" if the worker needs no attention.
func classifyWorkerEvent(o wfOwnerState) string {
	// A worker paused by the user is intentionally on hold: the updater must
	// not report it (even if it looks dead after the idle threshold).
	if o.State == "paused" && o.PauseKind == "user" {
		return ""
	}
	if o.GoalStatus == "ready_for_review" {
		// Reconciliation: the worker signals ready_for_review, but its bound
		// task card is already resolved (done/failed/cancelled) — e.g. the
		// owner flipped the card manually without going through agent_review,
		// or a prior approve set the card done but the teardown desynced and
		// left the worker goal stuck. approve is now a dead end (the card CAS
		// expects pending_review), so reclassify: the owner must
		// agent_terminate the orphan to release the worker + worktree.
		if isResolvedCardStatus(o.BoundCardStatus) {
			return "orphan_review"
		}
		return "ready_for_review"
	}
	if o.State == "failed" {
		return "failed"
	}
	if o.IsDead {
		return "dead"
	}
	return ""
}

// isResolvedCardStatus reports whether a task card status is terminal-resolved
// (the work is settled; no further disposition can advance it). A
// ready_for_review worker bound to such a card is an orphan.
func isResolvedCardStatus(s string) bool {
	switch s {
	case "done", "failed", "cancelled":
		return true
	}
	return false
}

// collectWorkerEvents scans all workers and returns every event needing owner
// attention. Unlike the old first-match approach, this collects ALL events.
func collectWorkerEvents(owners []wfOwnerState) []wfWorkerEvent {
	var events []wfWorkerEvent
	for _, o := range owners {
		et := classifyWorkerEvent(o)
		if et == "" {
			continue
		}
		events = append(events, wfWorkerEvent{
			ActorID:        o.ActorID,
			AgentID:        o.AgentID,
			TaskCardID:     o.TaskCardID,
			EventType:      et,
			WorktreeBranch: o.WorktreeBranch,
		})
	}
	return events
}

// orphanPendingReviewCards returns synthetic events for task cards stuck at
// pending_review with no live worker agent bound to them. collectWorkerEvents
// is registry-driven: a worker torn down (or never respawned) after setting
// its card to pending_review is invisible to it, so the owner is never woken
// to dispose the card and every downstream depends_on task stalls forever
// (2026-08-28 incident: a task card sat in pending_review for hours with the
// updater silent because its worker agent was already gone). Scanning the
// cards themselves closes that gap.
func orphanPendingReviewCards(taskIDs []string, liveBound map[string]bool, getCard func(string) (*CardRecord, error)) []wfWorkerEvent {
	var events []wfWorkerEvent
	for _, tid := range taskIDs {
		if liveBound[tid] {
			continue
		}
		card, err := getCard(tid)
		if err != nil || card == nil || card.Status != "pending_review" {
			continue
		}
		events = append(events, wfWorkerEvent{TaskCardID: tid, EventType: "orphaned_card"})
	}
	return events
}

// orphanedPendingReviewEvents is the actor wrapper over
// orphanPendingReviewCards, sourcing cards from the store and the live
// bindings from the registry-driven owner states.
func (a *Actor) orphanedPendingReviewEvents(taskIDs []string, owners []wfOwnerState) []wfWorkerEvent {
	if a.store == nil {
		return nil
	}
	liveBound := make(map[string]bool, len(owners))
	for _, o := range owners {
		if o.TaskCardID != "" {
			liveBound[o.TaskCardID] = true
		}
	}
	return orphanPendingReviewCards(taskIDs, liveBound, a.store.Get)
}

// eventLabel converts an internal event type to a human-readable label.
func eventLabel(et string) string {
	switch et {
	case "ready_for_review":
		return "ready for review"
	case "orphan_review":
		return "orphaned review"
	case "failed":
		return "failed"
	case "dead":
		return "unresponsive"
	case "orphaned_card":
		return "orphaned review card"
	default:
		return et
	}
}

// formatWorkerEventSummary builds a single structured message listing all worker
// events that need owner attention. Each line shows the agent, task card, and
// event type. The closing instructions are mandatory on purpose: a weak
// "please review and take action" let the owner (or whoever consumed the
// notification) end the turn with the worker dangling in pending_review.
func formatWorkerEventSummary(events []wfWorkerEvent) string {
	var b strings.Builder
	noun := "items"
	if len(events) == 1 {
		noun = "item"
	}
	fmt.Fprintf(&b, "Workflow status update (%d %s):\n\n", len(events), noun)
	for _, e := range events {
		var refs []string
		if e.ActorID != "" {
			refs = append(refs, fmt.Sprintf("actor: %s", e.ActorID))
		}
		if e.TaskCardID != "" {
			refs = append(refs, fmt.Sprintf("task: %s", e.TaskCardID))
		}
		refInfo := ""
		if len(refs) > 0 {
			refInfo = " (" + strings.Join(refs, ", ") + ")"
		}
		if e.AgentID == "" {
			// Synthetic card-driven event (orphaned_card): no agent to name.
			fmt.Fprintf(&b, "- Task card %s: %s", e.TaskCardID, eventLabel(e.EventType))
		} else {
			fmt.Fprintf(&b, "- Agent %s%s: %s", e.AgentID, refInfo, eventLabel(e.EventType))
		}
		if e.EventType == "ready_for_review" && e.WorktreeBranch != "" {
			fmt.Fprintf(&b, " branch: %s", e.WorktreeBranch)
		}
		b.WriteString("\n")
	}
	hasReadyForReview := false
	hasOrphanReview := false
	hasOrphanedCard := false
	noBranchReadyForReview := false
	for _, e := range events {
		switch e.EventType {
		case "ready_for_review":
			hasReadyForReview = true
			if e.WorktreeBranch == "" && e.AgentID != "" {
				noBranchReadyForReview = true
			}
		case "orphan_review":
			hasOrphanReview = true
		case "orphaned_card":
			hasOrphanedCard = true
		}
	}
	if hasReadyForReview {
		var mergeCmds []string
		seen := make(map[string]bool)
		for _, e := range events {
			if e.EventType != "ready_for_review" || e.WorktreeBranch == "" || seen[e.WorktreeBranch] {
				continue
			}
			seen[e.WorktreeBranch] = true
			mergeCmds = append(mergeCmds, "git merge "+e.WorktreeBranch)
		}
		mergeText := "git merge <branch>"
		if len(mergeCmds) > 0 {
			mergeText = strings.Join(mergeCmds, "; ")
		}
		noBranchNote := ""
		if noBranchReadyForReview {
			noBranchNote = " Workers without a branch (no-worktree / main-repo edits) write directly into the trunk — no merge is needed for them; just review and approve."
		}
		fmt.Fprintf(&b, "\nFor each ready_for_review worker above: first merge its branch into your own worktree (%s — a true merge, not cherry-pick/squash), resolve conflicts, verify, then call workspace.agent_review to approve. The approve call verifies the merge landed and cleans up the child worktree. To reject with feedback, no git operation is needed — the system rebases the child for you.%s", mergeText, noBranchNote)
	}
	if hasOrphanReview {
		fmt.Fprintf(&b, "\nFor each orphaned-review worker above: its task card is already resolved but the worker is still registered and its worktree is not released. Call workspace.agent_terminate on the worker's ActorID to dispose it and free the worktree — no git merge is needed (approve would fail because the card is no longer pending_review).")
	}
	if hasOrphanedCard {
		fmt.Fprintf(&b, "\nFor each orphaned review card above: its worker agent no longer exists (torn down or unloaded), so workspace.agent_review cannot dispose it. Re-dispose the card yourself: inspect its body and review evidence, merge any worker branch it references if that work is not already in your worktree, then project.wiki_set_status it to done (or failed, or backlog to re-spawn) and append an audit note. Never leave it in pending_review.")
	}
	if !hasReadyForReview && !hasOrphanReview && !hasOrphanedCard {
		b.WriteString("\nMandatory: every item above must reach an explicit disposition this turn — see your Workflow Tools for the review procedure and edge-case handling.")
	}
	return b.String()
}

// formatFrontierMessage builds the structural frontier notification, listing
// every claimable ticket as a card link so the owner can act without first
// re-querying project.wiki_frontier.
func formatFrontierMessage(mapID string, tickets []string) string {
	var b strings.Builder
	noun := "tickets"
	if len(tickets) == 1 {
		noun = "ticket"
	}
	fmt.Fprintf(&b, "Map [[%s]] has %d frontier %s ready:\n\n", mapID, len(tickets), noun)
	for _, id := range tickets {
		fmt.Fprintf(&b, "- [[%s]]\n", id)
	}
	b.WriteString("\nCheck: are these truly the right next tickets? Reorder if dependencies or priorities have shifted (depends_on via project.wiki_set_task_dependencies), defer any you do not start, then advance per your Workflow Tools.")
	return b.String()
}

// ── worker event lifecycle dedup ──

// wfEventKey identifies one worker event occurrence for dedup. The actor ID
// distinguishes a respawned worker from its predecessor on the same task card,
// so a fresh spawn of the same task is treated as a new event, not a duplicate.
func wfEventKey(e wfWorkerEvent) string {
	return e.ActorID + "|" + e.TaskCardID + "|" + e.EventType
}

// boundLiveOwner returns the ActorID of a root map's ownerAgentId when that
// owner is live (loaded) and still has this map active in workflow mode, else
// "". A done map is only terminal when no such owner exists; a lingering live
// binding keeps the updater responsible for urging the owner to release it.
func (a *Actor) boundLiveOwner(ctx actor.PureContext, card *CardRecord, ownerID string, tickAgents wfTickAgents) string {
	if ownerID == "" {
		return ""
	}
	for _, ag := range a.list(ctx, tickAgents) {
		if ag.ActorID != ownerID && ag.ID != ownerID {
			continue
		}
		if ag.LoadState == "loaded" && activeWorkflowMapCardID(ag) == card.Title {
			return ag.ActorID
		}
		return ""
	}
	return ""
}

// rebindStalledOwner scans the agent list for a loaded agent whose
// ActiveWorkflowMapCardID matches the given map card. The nudge-escalation
// unbind (notifyOwnerWithDedup → unbindMapOwner) clears ownerAgentId from the
// map card's frontmatter but does NOT clear the agent's own ActiveWorkflow
// binding — the agent is still alive and believes it owns this workflow.
//
// When such an agent is found, re-stamp ownerAgentId back onto the map card so
// the normal update path resumes nudging it. Throttled by WakeAt to avoid
// re-stamping every tick (the rebind is idempotent, but the write and event
// emission should not repeat at 5s intervals). Returns the rebound owner's
// ActorID, or "" if no live owner was found.
func (a *Actor) rebindStalledOwner(ctx actor.PureContext, card *CardRecord, st *wfDedupState, tickAgents wfTickAgents) string {
	if !parseWfTime(st.WakeAt).IsZero() && time.Since(parseWfTime(st.WakeAt)) < ownerWakeRetryInterval {
		return ""
	}
	for _, ag := range a.list(ctx, tickAgents) {
		if ag.LoadState != "loaded" || ag.ActorID == "" {
			continue
		}
		if activeWorkflowMapCardID(ag) != card.Title {
			continue
		}
		// Found a live agent still bound to this map — re-stamp.
		st.WakeAt = time.Now().UTC().Format(time.RFC3339Nano)
		// Apply ownerAgentId + dedup state in a single store.Save to
		// avoid the write-after-write race where saveWfDedupState reads
		// the card fresh and saves, only for store.Save to overwrite it
		// with the stale card.Raw (erasing the dedup state just written).
		raw := setOwnerInDataBlock(card.Raw, ag.ActorID)
		if b, err := json.Marshal(st); err == nil && !st.isEmpty() {
			raw = setCardDataFieldInRaw(raw, wfDedupDataKey, string(b))
		}
		if err := validateCard(card.Title, raw); err != nil {
			ctx.Logger().Warn("workflow: rebind stalled owner: validation failed", "card", card.Title, "owner", ag.ActorID, "error", err)
			return ""
		}
		raw = ensureCardMeta(card.Title, raw, time.Now().UTC().Format(time.RFC3339))
		if err := a.store.Save(&CardRecord{Title: card.Title, Raw: raw}); err != nil {
			ctx.Logger().Warn("workflow: rebind stalled owner: save failed", "card", card.Title, "error", err)
			return ""
		}
		ctx.Logger().Info("workflow: rebound stalled owner", "card", card.Title, "owner", ag.ActorID)
		_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{ID: card.Title, Modified: time.Now().UTC().Format(time.RFC3339)})
		return ag.ActorID
	}
	return ""
}

// wfTickAgents is the per-tick shared snapshot of the project's agent list.
// workflowUpdaterTick fetches it once and hands it to every map so N maps cost
// one workspace.list_agents RPC per tick instead of N. fetched distinguishes
// "prefetched (possibly empty)" from "not prefetched"; consumers fall back to
// fetching themselves only in the latter case (tests call
// workflowUpdateOneMap directly).
type wfTickAgents struct {
	agents  []gen.AgentRef
	fetched bool
}

// list returns the snapshot when present, else fetches it fresh.
func (a *Actor) list(ctx actor.PureContext, snap wfTickAgents) []gen.AgentRef {
	if snap.fetched {
		return snap.agents
	}
	return a.listProjectAgents(ctx)
}

// workflowUpdaterTick runs on every 5s timer tick.
func (a *Actor) workflowUpdaterTick(ctx actor.PureContext) {
	cards, err := a.store.List()
	if err != nil {
		return
	}
	var mapCards []*CardRecord
	for _, card := range cards {
		if card.Type == "workflow" {
			mapCards = append(mapCards, card)
		}
	}
	if len(mapCards) == 0 {
		return
	}

	// Single shared agent-list snapshot for every map this tick.
	tickAgents := wfTickAgents{agents: a.listProjectAgents(ctx), fetched: true}

	// Single-root fallback: if an owner is bound to multiple map cards,
	// unbind all but the most recently modified one. The primary binding
	// defense is set_map_owner (rejects double-bind); this is a safety net.
	a.enforceSingleRootBinding(ctx, mapCards)

	for _, card := range mapCards {
		a.workflowUpdateOneMap(ctx, card, tickAgents)
	}
}

// enforceSingleRootBinding collects (ownerID, cardID, modified) triples and
// for each owner bound to more than one root, clears ownerAgentId on all
// but the most recently modified card.
func (a *Actor) enforceSingleRootBinding(ctx actor.PureContext, mapCards []*CardRecord) {
	type binding struct {
		cardID   string
		modified string
	}
	bound := make(map[string][]binding) // ownerID → bindings
	for _, card := range mapCards {
		ownerID, _ := card.Data["ownerAgentId"].(string)
		if ownerID == "" {
			continue
		}
		bound[ownerID] = append(bound[ownerID], binding{
			cardID:   card.Title,
			modified: card.Modified,
		})
	}
	for ownerID, bindings := range bound {
		if len(bindings) <= 1 {
			continue
		}
		// Find the most recently modified card; keep it, unbind the rest.
		newest := 0
		for i := 1; i < len(bindings); i++ {
			if bindings[i].modified > bindings[newest].modified {
				newest = i
			}
		}
		for i, b := range bindings {
			if i == newest {
				continue
			}
			a.unbindMapOwner(ctx, b.cardID)
		}
		ctx.Logger().Info("workflow: single-root fallback unbound extra map cards", "owner", ownerID, "kept", bindings[newest].cardID, "cleared", len(bindings)-1)
	}
}

// ownerAgentIDLine matches a frontmatter line carrying only an (empty)
// ownerAgentId field, at any indentation depth.
// unbindMapOwner clears the ownerAgentId field from a map card's data block.
// Returns true when the card was actually updated.
func (a *Actor) unbindMapOwner(ctx actor.PureContext, cardID string) bool {
	card, err := a.store.Get(cardID)
	if err != nil {
		return false
	}
	// Clear ownerAgentId by setting it to empty, then strip the line.
	raw := setOwnerInDataBlock(card.Raw, "")
	// Remove the now-empty ownerAgentId line entirely.
	raw = ownerAgentIDLine.ReplaceAllString(raw, "")
	raw = ensureCardMeta(cardID, raw, time.Now().UTC().Format(time.RFC3339))
	if err := validateCard(cardID, raw); err != nil {
		ctx.Logger().Warn("workflow: unbindMapOwner validation failed", "card", cardID, "error", err)
		return false
	}
	updated := &CardRecord{Title: cardID, Raw: raw}
	if err := a.store.Save(updated); err != nil {
		ctx.Logger().Warn("workflow: unbindMapOwner save failed", "card", cardID, "error", err)
		return false
	}
	return true
}

// resetWfDedupState drops the notify history and pending event marks for a map
// by clearing those fields from its card data block, preserving WakeAt for
// backoff continuity (the original in-memory code never cleared wfWakeAt in
// any of these paths). When no fields remain, the data entry is removed.
func (a *Actor) resetWfDedupState(cardID string) {
	if cardID == "" || a.store == nil {
		return
	}
	card, err := a.store.Get(cardID)
	if err != nil || card == nil {
		return
	}
	st := readWfDedupState(card)
	if st.isEmpty() {
		return
	}
	wakeAt := st.WakeAt
	if wakeAt == "" {
		a.clearWfDedupState(cardID)
		return
	}
	*st = wfDedupState{WakeAt: wakeAt}
	a.saveWfDedupState(cardID, st)
}

func (a *Actor) workflowUpdateOneMap(ctx actor.PureContext, card *CardRecord, tickAgents wfTickAgents) {
	// Re-read the card to get the latest dedup state — the caller may pass a
	// stale card fetched before a prior tick saved dedup state.
	if fresh, err := a.store.Get(card.Title); err == nil && fresh != nil {
		card = fresh
	}
	st := readWfDedupState(card)

	ownerID, _ := card.Data["ownerAgentId"].(string)
	if ownerID == "" {
		// Ownerless map: the owner was unbound (nudge escalation, manual
		// unbind, or process restart loss). Three recovery paths, checked
		// in order:
		//
		// 1. Rebind: scan the agent list for a loaded agent whose
		//    ActiveWorkflowMapCardID matches this map. The nudge-escalation
		//    unbind clears ownerAgentId from the map card but does NOT
		//    clear the agent's own ActiveWorkflow binding — the agent is
		//    still alive and believes it owns this workflow. Re-stamp
		//    ownerAgentId so the normal update path resumes. This applies
		//    regardless of the map's status: a done map still needs the
		//    owner to be nudged to call workflow_stop; a doing map needs
		//    the owner to finish pending_review tasks. Throttled by WakeAt.
		//
		// 2. Auto-complete: if no live owner is found and all tasks are
		//    done, clear the stale ownerWorktreeId stamp and auto-complete
		//    the root. Only for doing maps (done/failed already settled).
		//
		// 3. Pending-review stall: if no live owner is found and some tasks
		//    are pending_review, the map is stuck — the worker branches
		//    have not been merged, and there is no owner to merge them.
		//    Leave the map in its current status; manual intervention needed.
		if reboundID := a.rebindStalledOwner(ctx, card, st, tickAgents); reboundID != "" {
			ownerID = reboundID
			if fresh, err := a.store.Get(card.Title); err == nil && fresh != nil {
				card = fresh
			}
			goto normalUpdate
		}
		// No live owner found: auto-complete only if doing + all tasks done.
		if card.Status != "done" && card.Status != "failed" {
			taskIDs := a.mapTaskIDs(card.Title, card)
			if len(taskIDs) > 0 && a.allTaskCardsDone(taskIDs) {
				if wtID, _ := card.Data["ownerWorktreeId"].(string); wtID != "" {
					a.clearMapOwnerWorktree(card.Title)
				}
				a.maybeAutoCompleteRoot(ctx, card.Title)
			}
		}
		a.resetWfDedupState(card.Title)
		return
	}
normalUpdate:
	rootDone := card.Status == "done"
	if rootDone {
		if liveActorID := a.boundLiveOwner(ctx, card, ownerID, tickAgents); liveActorID != "" {
			// A done root with a still-bound, live owner is not settled: the
			// owner must exit workflow mode / be unbound. Keep nudging it so
			// the binding does not linger forever; the dedup/escalation path
			// reuses the normal notification machinery.
			if status := a.pollAgentStatus(ctx, liveActorID); status != nil && !isAgentBusyForWorkflowUpdate(status.State) {
				a.notifyOwnerWithDedup(ctx, card.Title, ownerID,
					"Workflow "+card.Title+" is done but still bound to you. Call workflow_stop (or have it unbound) to release the workflow.",
					status, false, "", st)
			}
			a.saveWfDedupState(card.Title, st)
			return
		}
		a.resetWfDedupState(card.Title)
		return
	}

	taskIDs := a.mapTaskIDs(card.Title, card)
	taskSet := make(map[string]bool, len(taskIDs))
	for _, id := range taskIDs {
		taskSet[id] = true
	}

	ownerBusy, ownerWorkflowActive, ownerNeedsWake, owners, frontier, ownerStatus := a.gatherUpdaterInputs(ctx, ownerID, taskSet, taskIDs, card.Title, tickAgents)

	// A4: owner agent reports failed/stuck -> root auto-failed. Explicit agent
	// deletion is handled by handleWikiUnbindAgent; this catches agents that are
	// still listed yet dead according to their status. A recently woken owner
	// (ownerWakeGraceActive) is exempt: its stale LastTurnCompletedAt predates
	// the wake, and it must first get the chance to consume the notification
	// it was woken for before being declared dead.
	if ownerStatus != nil && isAgentDead(ownerStatus) && !ownerWakeGraceActive(st) {
		a.maybeAutoFailRoot(ctx, card.Title)
		// Clear notification dedup state but preserve WakeAt for backoff
		// continuity (the original in-memory code never cleared wfWakeAt
		// in this path).
		wakeAt := st.WakeAt
		*st = wfDedupState{WakeAt: wakeAt}
		a.saveWfDedupState(card.Title, st)
		return
	}

	if !ownerWorkflowActive {
		// A card binding alone is not enough to wake an agent. The owner must
		// have this map active in workflow mode. An owner that merely is not
		// loaded (e.g. after a process restart) still owns the workflow: wake
		// it via workspace.load_agent so a later tick can deliver the pending
		// notification instead of stalling the map in "doing" forever.
		if ownerNeedsWake {
			a.wakeMapOwner(ctx, card.Title, ownerID, st, tickAgents)
		}
		// Clear notification dedup state but preserve WakeAt for backoff
		// continuity (the original in-memory code never cleared wfWakeAt
		// in this path).
		wakeAt := st.WakeAt
		*st = wfDedupState{WakeAt: wakeAt}
		a.saveWfDedupState(card.Title, st)
		return
	}

	// Owner busy: skip notification entirely (don't spam a working owner).
	// An accepted chat_submit flips the owner to running, so every branch
	// below can re-notify on each tick: an idle owner that has not resolved a
	// pending condition is reminded until it does. Notification dedup is now
	// handled by notifyOwnerWithDedup (via decideNotify): repeated identical
	// messages degrade to nudges and eventually unbind the owner.
	if ownerBusy {
		return
	}

	events := collectWorkerEvents(owners)
	events = append(events, a.orphanedPendingReviewEvents(taskIDs, owners)...)
	if len(events) > 0 {
		// Send ONE aggregated summary message listing every event. Worker
		// events take priority over structural notifications this tick.
		// A genuinely new event (first sighting, or re-fired after resolution)
		// forces a full send; only commit the notified marks on success so a
		// failed dispatch retries as fresh next tick.
		fresh := pruneNotifiedKeys(st, events)
		if a.notifyOwnerWithDedup(ctx, card.Title, ownerID, formatWorkerEventSummary(events), ownerStatus, fresh, nudgeHintForEvents(events), st) && fresh {
			commitNotifiedKeys(st, events)
		}
		a.saveWfDedupState(card.Title, st)
		return
	}
	// No worker events this tick: any previously notified event has resolved,
	// so drop the pending marks (a later re-fire must notify fully again).
	st.NotifiedKeys = nil

	// No worker events → fall through to structural notifications.
	hasActive := false
	for _, o := range owners {
		if o.State == "running" {
			hasActive = true
		}
	}

	decision := updaterDecide(wfUpdaterInput{
		OwnerBusy:      ownerBusy,
		MapID:          card.Title,
		Frontier:       frontier,
		HasActiveOwner: hasActive,
	})
	if decision.Notify {
		// Re-notify an idle owner until it advances or reorders the frontier,
		// or assesses the exhausted tree.
		a.notifyOwnerWithDedup(ctx, card.Title, ownerID, decision.Message, ownerStatus, false, nudgeHintForFrontier(len(frontier)), st)
	}
	a.saveWfDedupState(card.Title, st)
}

func (a *Actor) gatherUpdaterInputs(ctx actor.PureContext, ownerID string, taskSet map[string]bool, taskIDs []string, mapID string, tickAgents wfTickAgents) (ownerBusy, ownerWorkflowActive, ownerNeedsWake bool, owners []wfOwnerState, frontier []string, ownerStatus *gen.AgentStatusResp) {
	agents := a.list(ctx, tickAgents)
	var owner *gen.AgentRef
	for i := range agents {
		if agents[i].ActorID == ownerID || agents[i].ID == ownerID {
			owner = &agents[i]
			break
		}
	}
	if owner == nil {
		return false, false, false, nil, nil, nil
	}
	ownerState := wfOwnerState{
		AgentID:           owner.ID,
		WorkflowMapCardID: activeWorkflowMapCardID(*owner),
		LoadState:         owner.LoadState,
	}
	if ownerState.LoadState != "loaded" {
		return false, false, true, nil, nil, nil
	}
	ownerWorkflowActive = ownerState.WorkflowMapCardID == mapID
	status := a.pollAgentStatus(ctx, owner.ActorID)
	if status == nil {
		// Listed as loaded but the actor is not addressable (crashed or never
		// respawned): same wake path as an unloaded owner.
		return false, false, true, nil, nil, nil
	}
	ownerState.State = status.State
	ownerBusy = isAgentBusyForWorkflowUpdate(ownerState.State)
	if !ownerWorkflowActive {
		return ownerBusy, false, false, nil, nil, nil
	}

	for _, ag := range agents {
		if ag.ActorID == ownerID || ag.ID == ownerID || ag.LoadState != "loaded" {
			continue
		}
		status := a.pollAgentStatus(ctx, ag.ActorID)
		if status == nil {
			continue
		}
		boundID := ""
		if status.Goal != nil {
			boundID = status.Goal.BoundTaskCardID
		}
		if boundID == "" || !taskSet[boundID] {
			// Not in the include projection or the topo graph: fall back to
			// the task card's own parent field — the ground truth
			// create_task_card writes. This rescues workers whose card id was
			// dropped from both stores by a concurrent-create race; without it
			// their ready_for_review would never reach the map owner. Runs
			// even when taskSet is empty (fully truncated scope), so the
			// membership decision rests on the card itself, not projections.
			boundCard, getErr := a.store.Get(boundID)
			if getErr != nil || boundCard.Parent != mapID {
				continue
			}
			ctx.Logger().Warn("workflow updater: worker bound to task card missing from map scope projection, recovered via parent field",
				"map", mapID, "task", boundID, "agent", ag.ID)
		}
		os := wfOwnerState{
			ActorID:           ag.ActorID,
			AgentID:           ag.ID,
			TaskCardID:        boundID,
			State:             status.State,
			PauseKind:         status.PauseKind,
			WorkflowMapCardID: activeWorkflowMapCardID(ag),
			LoadState:         ag.LoadState,
		}
		if status.Goal != nil {
			os.GoalStatus = status.Goal.Status
		}
		// Reconcile: a ready_for_review worker whose bound card is already
		// resolved is an orphan — approve would fail the card CAS. Fetch the
		// card status only in that case (ready_for_review workers are few)
		// so classifyWorkerEvent can reclassify it as orphan_review.
		if os.GoalStatus == "ready_for_review" && boundID != "" {
			if bc, gErr := a.store.Get(boundID); gErr == nil {
				os.BoundCardStatus = bc.Status
			}
		}
		os.IsDead = isAgentDead(status)
		if ag.Mode != nil {
			os.WorktreeBranch = a.lookupWorktreeBranch(ag.Mode.ActiveWorkflowWorktreeID)
		}
		owners = append(owners, os)
	}

	for _, fc := range a.computeFrontier(mapID, taskIDs) {
		frontier = append(frontier, fc.ID)
	}
	return ownerBusy, ownerWorkflowActive, false, owners, frontier, status
}

func (a *Actor) lookupWorktreeBranch(worktreeID string) string {
	if worktreeID == "" {
		return ""
	}
	a.worktreeParentMu.RLock()
	defer a.worktreeParentMu.RUnlock()
	if wt, ok := a.worktrees[worktreeID]; ok {
		return wt.Branch
	}
	return ""
}

func activeWorkflowMapCardID(agent gen.AgentRef) string {
	if agent.Mode == nil {
		return ""
	}
	return agent.Mode.ActiveWorkflowMapCardID
}

// ownerWakeRetryInterval bounds how often the updater re-attempts to load a
// not-running workflow owner. The timer ticks every 5s; without this backoff a
// spawn that keeps failing would be retried every tick.
const ownerWakeRetryInterval = time.Minute

// wakeMapOwner loads a not-running workflow owner (unloaded, e.g. after a
// process restart, or loaded but no longer addressable) by spawning the agent
// directly on the project owner loop and then fire-and-forget notifying
// workspace to update its in-memory registry. This eliminates the deadlock
// cycle that occurred when the old code synchronously called
// workspace.load_agent → loadAgentByID → spawnAgentViaProject →
// project.spawn_agent (while the project loop was already occupied by
// wakeMapOwner).
//
// Dedup state is read from st; the backoff check uses st.WakeAt so the
// updater does not retry more often than ownerWakeRetryInterval.
func (a *Actor) wakeMapOwner(ctx actor.PureContext, mapID, ownerID string, st *wfDedupState, tickAgents wfTickAgents) {
	if !parseWfTime(st.WakeAt).IsZero() && time.Since(parseWfTime(st.WakeAt)) < ownerWakeRetryInterval {
		return
	}
	st.WakeAt = time.Now().UTC().Format(time.RFC3339Nano)

	// Find the agent info from the workspace registry. listProjectAgents
	// calls workspace.list_agents (a read-only query that does not call back
	// to project), so it is safe from the deadlock perspective.
	var agent gen.AgentRef
	for _, ag := range a.list(ctx, tickAgents) {
		if ag.ActorID == ownerID || ag.ID == ownerID {
			agent = ag
			break
		}
	}
	if agent.ID == "" {
		return
	}

	// If the agent is already loaded (has a live actor), skip spawning.
	if agent.LoadState == "loaded" && agent.ActorID != "" {
		if cid, err := identity.ParseCanonicalID(agent.ActorID); err == nil {
			if _, ok := ctx.LookupID(id.From(cid)); ok {
				return
			}
		}
	}

	// Look up the agent's bound worktree (if any). The project maintains
	// this mapping independently of the workspace registry.
	worktreeID := ""
	a.bindingMu.RLock()
	worktreeID = a.agentWorktree[agent.ActorID]
	a.bindingMu.RUnlock()

	// Spawn the agent locally — this is the key change that breaks the
	// deadlock cycle. Instead of calling workspace.load_agent which would
	// call back to project.spawn_agent, we spawn directly on the project
	// owner loop. The agent's OnStart runs asynchronously (WithAsyncStart),
	// so ctx.Spawn returns immediately.
	resp, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName:     agent.ID,
		ProjectID:     a.actorID,
		AgentKind:     agent.AgentKind,
		WorkspaceID:   "", // populated by workspace when notifying
		ActorID:       agent.ActorID,
		DisplayName:   agent.DisplayName,
		Primary:       agent.Primary,
		Fast:          agent.Fast,
		Execution:     agent.Execution,
		Review:        agent.Review,
		Summary:       agent.Summary,
		WorktreeID:    worktreeID,
		ParentAgentID: agent.ParentAgentID,
		PermissionMode: a.globalPermissionMode(ctx),
	})
	if err != nil {
		ctx.Logger().Warn("workflow: local spawn owner failed", "map", mapID, "owner", ownerID, "error", err)
		return
	}

	// Persist the worktree binding so the agent's file/git/shell calls
	// route to the correct worktree even after a process restart.
	if worktreeID != "" {
		if err := a.persistWorktreeManifest(worktreeID); err != nil {
			ctx.Logger().Error("project: persist worktree manifest after local spawn failed", "error", err)
		}
	}

	// Fire-and-forget notify workspace to update its in-memory agent
	// registry. The notification is best-effort: if the workspace is busy,
	// the message will be delivered when it processes its next mailbox
	// frame. No response is awaited, so this cannot deadlock.
	wsRef, ok := ctx.LookupService("workspace")
	if !ok || wsRef == nil {
		return
	}
	notifyCtx, cancel := context.WithTimeout(ctx.Lifecycle(), notifyTimeout)
	defer cancel()
	call := wsRef.Invoke(notifyCtx, "workspace.agent_loaded", gen.WorkspaceAgentLoadedReq{
		AgentID:   agent.ID,
		ActorID:   resp.ActorID,
		ProjectID: a.actorID,
	})
	if call != nil {
		_ = call.Close()
	}
	ctx.Logger().Info("workflow: owner not running; spawned locally and notified workspace", "map", mapID, "owner", ownerID, "actorID", resp.ActorID)
}

// ownerWakeGraceActive reports whether an owner woken within the last
// deadOwnerThreshold is still inside its grace window. A freshly loaded owner
// reports a LastTurnCompletedAt from before the restart; without this grace
// isAgentDead would auto-complete or auto-fail the root before the owner ever
// received the notification it was woken for.
func ownerWakeGraceActive(st *wfDedupState) bool {
	t := parseWfTime(st.WakeAt)
	if t.IsZero() {
		return false
	}
	return time.Since(t) < deadOwnerThreshold
}

// workflowPollTimeout bounds timer-loop dependency calls.
const workflowPollTimeout = 5 * time.Second

// notifyTimeout bounds dispatchOwnerChatSubmit's Invoke against wedged targets.
// Fire-and-forget normally returns in microseconds (frame delivered at Invoke
// time, Close drops the caller-side handle); the short timeout caps the rare
// case where Invoke itself stalls on a dead/full mailbox.
const notifyTimeout = 500 * time.Millisecond

// maxContinueNotifications is N+1: after this many consecutive nudge
// nudges for the same pending condition, the owner is unbound.
const maxContinueNotifications = 5

func (a *Actor) pollAgentStatus(ctx actor.PureContext, agentActorID string) *gen.AgentStatusResp {
	return a.pollAgentStatusTimeout(ctx, agentActorID, workflowPollTimeout)
}

// pollAgentStatusTimeout polls agent_status with an explicit timeout. Final
// (not Value) is used: Value() triggers lazy consumption with no context
// cancellation hook, so a hung agent would block the timer loop forever.
// Final wires ctx cancellation into Cancel, unblocking Recv at the deadline.
func (a *Actor) pollAgentStatusTimeout(ctx actor.PureContext, agentActorID string, timeout time.Duration) *gen.AgentStatusResp {
	ref, ok := a.lookupAgentRef(ctx, agentActorID)
	if !ok || ref == nil {
		return nil
	}
	statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), timeout)
	defer cancel()
	call := ref.Invoke(statusCtx, "agent_status", nil)
	if call == nil {
		return nil
	}
	defer call.Close()
	v, err := call.Final(statusCtx)
	if err != nil {
		return nil
	}
	switch s := v.(type) {
	case gen.AgentStatusResp:
		return &s
	case *gen.AgentStatusResp:
		return s
	}
	return nil
}

func (a *Actor) pollAgentBusy(ctx actor.PureContext, agentActorID string) (busy bool, status *gen.AgentStatusResp) {
	status = a.pollAgentStatus(ctx, agentActorID)
	if status == nil {
		return false, nil
	}
	return isAgentBusyForWorkflowUpdate(status.State), status
}

// Any paused owner (user pause, task pause, crash-recovery pause) is busy:
// it cannot act on updater messages until resumed, and the updater re-fires
// pending notifications once the owner is idle again.
func isAgentBusyForWorkflowUpdate(state string) bool {
	return state == "running" || state == "paused"
}

func (a *Actor) notifyOwner(ctx actor.PureContext, ownerID, message string) bool {
	ref, ok := a.lookupAgentRef(ctx, ownerID)
	if !ok || ref == nil {
		return false
	}
	return dispatchOwnerChatSubmit(ctx.Lifecycle(), ref, message)
}

// dispatchOwnerChatSubmit delivers a chat_submit to the owner agent without
// waiting for its response. The owner can synchronously query workspace while
// starting the turn; awaiting that response from the updater's owner loop
// creates a circular wait. A non-nil call means the message was accepted for
// delivery, so callers can deduplicate the notification.
func dispatchOwnerChatSubmit(ctx context.Context, target ref.Ref, message string) bool {
	callCtx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	call := target.Invoke(callCtx, "chat_submit", gen.AgentChatSubmitReq{Text: message, Meta: wfNotifyMeta})
	if call == nil {
		return false
	}
	defer call.Close()
	return true
}

// notifyOwnerWithDedup applies decideNotify against the map's dedup state and
// acts on the result: send the message, send the nudge text, or unbind the
// owner. The dedup state lives in the map card's data block (wfDedup key) and
// is persisted via persist.Persist by the caller after this function returns.
// Escalation (continue nudges → unbind) is gated on ownerConsumedSince: while
// the owner has not completed a turn after the pending message was dispatched
// (backlogged or wedged mailbox), nudges are held — neither resent into the
// jammed queue nor counted — so a stuck-but-alive owner never loses its
// workflow binding. A genuinely new message (wfNotifySend) is never gated.
//
// forceSend bypasses decideNotify and dispatches the full message as a fresh
// send (recording it in the state so a persisting condition degrades to nudges
// on subsequent ticks). Used for worker events that are genuinely new (first
// sighting, or re-fired after resolution) — see pruneNotifiedKeys.
// Returns true only when a dispatch was actually delivered.
func (a *Actor) notifyOwnerWithDedup(ctx actor.PureContext, mapID, ownerID, message string, ownerStatus *gen.AgentStatusResp, forceSend bool, nudgeHint string, st *wfDedupState) bool {
	action, send := decideNotifyFromCard(message, st, maxContinueNotifications, nudgeHint)
	if forceSend {
		action, send = wfNotifySend, message
	}
	if action == wfNotifyContinue {
		// Randomize repeated nudges: a byte-identical prompt makes the owner
		// LLM replay the same non-action every cycle.
		send = varyNudge(send)
	}
	if action != wfNotifySend && !ownerConsumedSince(ownerStatus, parseWfTime(st.LastRealSendAt)) {
		if !st.Held {
			st.Held = true
			ctx.Logger().Warn("workflow: owner has not consumed pending notification; holding nudges", "map", mapID, "owner", ownerID)
		}
		return false
	}
	st.Held = false
	switch action {
	case wfNotifyUnbind:
		ctx.Logger().Warn("workflow: owner unbound after repeated identical notifications", "map", mapID, "owner", ownerID)
		a.unbindMapOwner(ctx, mapID)
		*st = wfDedupState{}
		return false
	default: // wfNotifyContinue, wfNotifySend (including forced sends)
		if a.notifyOwner(ctx, ownerID, send) {
			if isNudge(send) {
				st.NudgeCount++
			} else {
				markSendApplied(st, message, time.Now())
			}
			return true
		}
		return false
	}
}

const deadOwnerThreshold = 10 * time.Minute

func isAgentDead(status *gen.AgentStatusResp) bool {
	if status == nil || status.State == "failed" {
		return true
	}
	// An idle agent with an active goal that hasn't completed a turn
	// in a long time is considered dead (stuck/hung). An agent waiting
	// for review (ready_for_review) is NOT dead — it is intentionally paused.
	if status.State != "running" && status.LastTurnCompletedAt != "" {
		goalActive := status.Goal != nil && status.Goal.Status == "active"
		if !goalActive {
			return false
		}
		t, err := time.Parse(time.RFC3339Nano, status.LastTurnCompletedAt)
		if err != nil {
			return false
		}
		return time.Since(t) > deadOwnerThreshold
	}
	return false
}
