package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// workflow_stamp.go — security gate v2 stamping chain.
//
// When a workflow_plan_submit is approved the user implicitly approves the
// plan body. The toolcall executor cannot read that body back out (it lives
// on the plan card, not on each task card), so the approval must be
// materialised as a structured list of mutating toolcalls on the workflow
// map card. The executor Preflight then double-checks this list against the
// per-card gate approval.
//
// StampWorkflowMutatingEffects is called by the agent's workflow
// activation path right after activateWorkflow sets the active workflow
// map. The stamp logic lives next to the toolcall executor that consumes
// it (single-package ownership of the security gate v2 contract); the
// agent side just hands the stamp helper the project + map IDs it already
// has on hand.

// StampWorkflowMutatingEffects walks every task card under the given
// workflow map and, for each mutating toolcall card that declares a
// gate_card, writes a single entry to the map card's
// data.stamped_effects list. The result is the authoritative stamp used by
// toolcall executor Preflight (see executor_toolcall.go).
//
// The stamp is idempotent: re-running with the same map ID keeps the
// existing entries and only adds new ones. Existing entries for the same
// task_card_id are overwritten in place so the latest callable / gate_card
// reference always wins (lets the author edit a toolcall card's callable
// after plan approval and have the stamp follow without a manual reset).
//
// Stamps are written via project.wiki_edit_card so the authoritative graph
// snapshot stays in sync with the frontmatter projection. Errors fetching
// individual task cards are tolerated (a single broken card must not block
// stamping the rest); errors writing back to the map card are returned so
// the caller can surface them to the user.
func StampWorkflowMutatingEffects(ctx actor.PureContext, projectRef ref.Ref, mapID string) error {
	if projectRef == nil {
		return fmt.Errorf("workspace.stamp_workflow_mutating_effects: projectRef is required")
	}
	if mapID == "" {
		return fmt.Errorf("workspace.stamp_workflow_mutating_effects: mapId is required")
	}

	// Fetch the map card first so we can read its current stamped_effects
	// (idempotency) and discover the task-card scope. The scope include list
	// is authoritative for the cards the workflow owns.
	mapCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 10*time.Second)
	defer cancel()
	mapCall := projectRef.Invoke(mapCtx, "project.wiki_get_card", domain.WikiGetCardReq{ID: mapID})
	if mapCall == nil {
		return fmt.Errorf("workspace.stamp_workflow_mutating_effects: project.wiki_get_card invoke returned nil")
	}
	mapResult, err := mapCall.Final(mapCtx)
	if err != nil {
		return fmt.Errorf("workspace.stamp_workflow_mutating_effects: fetch map card %q: %w", mapID, err)
	}
	mapResp, ok := mapResult.(domain.WikiGetCardResp)
	if !ok {
		return fmt.Errorf("workspace.stamp_workflow_mutating_effects: fetch map card returned unexpected type %T", mapResult)
	}
	mapCard := project.ParseCardRaw(mapID, mapResp.Raw)
	scope := scopeIncludeFromCard(mapCard.Data)
	if len(scope) == 0 {
		// No scoped task cards → nothing to stamp. Still allowed; the
		// workflow was approved but no mutating toolcalls exist yet.
		return nil
	}

	// Build the candidate stamp set. We must inspect each task card's
	// frontmatter to discover whether it is a mutating toolcall with a
	// declared gate_card. Skip non-task / non-toolcall cards silently.
	candidates := make([]stampedEffectEntry, 0, len(scope))
	cardCtx, cancelCards := context.WithTimeout(ctx.Lifecycle(), 30*time.Second)
	defer cancelCards()
	for _, taskID := range scope {
		entry := scanStampedCandidate(cardCtx, projectRef, mapID, taskID)
		if entry != nil {
			candidates = append(candidates, *entry)
		}
	}

	// Merge with any pre-existing stamped_effects (idempotency) and write
	// the merged set back to the map card.
	merged := mergeStampedEffects(mapCard.Data, candidates)
	raw, err := setStampedEffectsInDataBlock(mapResp.Raw, merged)
	if err != nil {
		return fmt.Errorf("workspace.stamp_workflow_mutating_effects: build stamped raw: %w", err)
	}
	if raw == mapResp.Raw {
		// Nothing to write (no candidates and no existing entries) — skip
		// the edit call so we do not bump the card's modified time.
		return nil
	}
	editCtx, cancelEdit := context.WithTimeout(ctx.Lifecycle(), 10*time.Second)
	defer cancelEdit()
	editCall := projectRef.Invoke(editCtx, "project.wiki_edit_card", domain.WikiEditCardReq{
		ID:  mapID,
		Raw: raw,
	})
	if editCall == nil {
		return fmt.Errorf("workspace.stamp_workflow_mutating_effects: project.wiki_edit_card invoke returned nil")
	}
	if _, err := editCall.Final(editCtx); err != nil {
		return fmt.Errorf("workspace.stamp_workflow_mutating_effects: write stamped effects to map %q: %w", mapID, err)
	}
	return nil
}

// stampedEffectEntry is the in-memory shape of one map.stamped_effects row.
// JSON-serialised when written back to the map card's frontmatter (via
// setStampedEffectsInDataBlock → setDataBlockField, same path as
// task_outputs).
type stampedEffectEntry struct {
	TaskCardID string `json:"task_card_id"`
	CallableID string `json:"callable_id"`
	GateCardID string `json:"gate_card_id,omitempty"`
	// StampedAt is the RFC3339 timestamp of the stamp pass. Stable per
	// pass — multiple runs overwrite the timestamp, which is the desired
	// behaviour (the entry's "freshness" follows the latest approval).
	StampedAt string `json:"stamped_at"`
}

// scanStampedCandidate fetches a single task card from the project actor
// and, if it is a mutating toolcall with a declared gate_card, returns a
// stampedEffectEntry describing it. Returns nil for non-mutating /
// non-toolcall cards so the caller can keep the loop tidy without
// branching on every iteration. Errors fetching the card are tolerated
// (logged via the context logger would be nicer, but the call site
// already wraps the loop).
func scanStampedCandidate(ctx context.Context, projectRef ref.Ref, mapID, taskID string) *stampedEffectEntry {
	call := projectRef.Invoke(ctx, "project.wiki_get_card", domain.WikiGetCardReq{ID: taskID})
	if call == nil {
		return nil
	}
	result, err := call.Final(ctx)
	if err != nil {
		return nil
	}
	resp, ok := result.(domain.WikiGetCardResp)
	if !ok {
		return nil
	}
	card := project.ParseCardRaw(taskID, resp.Raw)
	if card.Type != "task" {
		return nil
	}
	execBlock := project.CardDataMap(card, "exec")
	if execBlock == nil {
		return nil
	}
	kind, _ := execBlock["kind"].(string)
	if kind != "toolcall" {
		return nil
	}
	callable, _ := execBlock["callable"].(string)
	if strings.TrimSpace(callable) == "" {
		return nil
	}
	gateCard, _ := execBlock["gate_card"].(string)
	if strings.TrimSpace(gateCard) == "" {
		// No gate_card → not a v2-stamped mutating toolcall. The executor
		// will reject the invocation anyway, but stamping it would imply
		// approval and that would be misleading.
		return nil
	}
	return &stampedEffectEntry{
		TaskCardID: taskID,
		CallableID: strings.TrimSpace(callable),
		GateCardID: strings.TrimSpace(gateCard),
		StampedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// scopeIncludeFromCard reads the data.scope.include list (or data.include
// fallback) from a workflow map card. Returns nil when neither exists so
// the caller can distinguish "no tasks yet" from "tasks present but
// empty". Mirrors the canonical projection used by the project's
// wiki_workflow.go; duplicated here to keep the workspace stamp helper
// self-contained (the workspace package may not have access to the
// project's scopeIncludeIDs internal helper, and the stamp chain must
// remain an in-workspace concern).
func scopeIncludeFromCard(data map[string]any) []string {
	if data == nil {
		return nil
	}
	if scope, ok := data["scope"].(map[string]any); ok {
		if inc, ok := scope["include"].([]any); ok {
			out := make([]string, 0, len(inc))
			for _, v := range inc {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					out = append(out, strings.TrimSpace(s))
				}
			}
			return out
		}
		if inc, ok := scope["include"].([]string); ok {
			return inc
		}
	}
	if inc, ok := data["include"].([]any); ok {
		out := make([]string, 0, len(inc))
		for _, v := range inc {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	return nil
}

// mergeStampedEffects builds the union of pre-existing stamped_effects and
// the freshly-scanned candidates. Existing rows keyed by TaskCardID are
// overwritten in place; other rows survive untouched. The returned slice
// is stable-sorted by TaskCardID so the persisted JSON is reproducible
// across runs (helps test assertions and diff-friendly frontmatter).
func mergeStampedEffects(data map[string]any, candidates []stampedEffectEntry) []stampedEffectEntry {
	byTask := make(map[string]stampedEffectEntry, len(candidates))
	for _, c := range candidates {
		byTask[c.TaskCardID] = c
	}
	for _, existing := range stampedEffectsForCard(data) {
		taskID, _ := existing["task_card_id"].(string)
		if taskID == "" {
			continue
		}
		if _, hasNew := byTask[taskID]; hasNew {
			// Fresh candidate wins. This lets edits to the toolcall's
			// callable / gate_card propagate without an explicit reset.
			continue
		}
		byTask[taskID] = stampedEffectEntry{
			TaskCardID: taskID,
			CallableID: stringField(existing["callable_id"]),
			GateCardID: stringField(existing["gate_card_id"]),
			StampedAt:  stringField(existing["stamped_at"]),
		}
	}
	out := make([]stampedEffectEntry, 0, len(byTask))
	for _, e := range byTask {
		out = append(out, e)
	}
	// Stable sort by TaskCardID.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].TaskCardID > out[j].TaskCardID; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// setStampedEffectsInDataBlock writes the merged stamped_effects slice to
// the map card's data: block. Returns the new raw markdown (or the input
// raw when the merged set is empty so the caller can no-op). Marshalling
// failures surface as a hard error: a malformed stamp must not silently
// land as an empty list.
//
// setDataBlockField is duplicated from pkg/actor/project/wiki_workflow.go
// (private there) because the stamp helper is a workspace concern that
// cannot import an unexported helper from the project package. The two
// copies must stay in lockstep — if the canonical helper grows new
// behaviours (e.g. deeper-nested data.foo.bar handling) the duplicate
// must too. Kept private to this file.
func setStampedEffectsInDataBlock(raw string, entries []stampedEffectEntry) (string, error) {
	if len(entries) == 0 {
		return raw, nil
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return setDataBlockField(raw, "stamped_effects", string(encoded)), nil
}

// setDataBlockField inserts or replaces a named string field inside the
// frontmatter's data: block. If no data: block exists, one is created.
// Duplicate of the same-named helper in pkg/actor/project/wiki_workflow.go
// (kept private there). See setStampedEffectsInDataBlock for the rationale.
func setDataBlockField(raw, field, value string) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return raw
	}
	front := raw[3 : 3+end]
	lines := strings.Split(front, "\n")
	dataIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "data:" && (len(line) == 0 || line[0] != ' ') {
			dataIdx = i
			break
		}
	}
	fieldLine := "  " + field + ": " + value
	if dataIdx < 0 {
		lines = append(lines, "data:", fieldLine)
		return rebuildFrontmatter(lines, raw, end)
	}
	endIdx := dataIdx + 1
	for endIdx < len(lines) && (lines[endIdx] == "" || lines[endIdx][0] == ' ' || lines[endIdx][0] == '\t') {
		endIdx++
	}
	for i := dataIdx + 1; i < endIdx; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, field+":") {
			lines[i] = fieldLine
			return rebuildFrontmatter(lines, raw, end)
		}
	}
	lines = append(lines[:endIdx], append([]string{fieldLine}, lines[endIdx:]...)...)
	return rebuildFrontmatter(lines, raw, end)
}

// rebuildFrontmatter stitches the rewritten frontmatter lines back together
// with the rest of the document (everything after the second ---).
func rebuildFrontmatter(lines []string, raw string, endOffset int) string {
	rebuilt := strings.Join(lines, "\n")
	return "---\n" + rebuilt + raw[3+endOffset:]
}

func stringField(v any) string {
	s, _ := v.(string)
	return s
}

// StampWorkflowMutatingEffectsReq is the workspace callable payload used by
// the agent's activateWorkflow hook. ProjectID is the canonical project
// actor ID; MapCardID names the workflow map card to stamp. Fields are
// snake_case in the JSON wire form (matching the project's existing
// convention for inter-actor payloads); the agent's call site builds a
// map[string]any with the same keys.
type StampWorkflowMutatingEffectsReq struct {
	ProjectID string `json:"project_id"`
	MapCardID string `json:"map_card_id"`
}

// handleStampWorkflowMutatingEffects is the workspace-side entry point for
// workspace.stamp_workflow_mutating_effects. Pure context (no actor state
// touched); bounded 30s timeout so a wedged project actor cannot stall the
// agent's activation turn.
//
// The handler resolves the project actor from the canonical project ID,
// then delegates to StampWorkflowMutatingEffects (the same helper that
// any future in-process caller would use). Internal callable —
// authorization is implicit (actor.Internal() role) and the agent's call
// site is the only consumer.
func (a *Actor) handleStampWorkflowMutatingEffects(ctx actor.PureContext, req StampWorkflowMutatingEffectsReq) error {
	if req.ProjectID == "" || req.MapCardID == "" {
		return fmt.Errorf("workspace.stamp_workflow_mutating_effects: project_id and map_card_id are required")
	}
	cid, err := identity.ParseCanonicalID(req.ProjectID)
	if err != nil {
		return fmt.Errorf("workspace.stamp_workflow_mutating_effects: invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return fmt.Errorf("workspace.stamp_workflow_mutating_effects: project actor unavailable")
	}
	return StampWorkflowMutatingEffects(ctx, projectRef, req.MapCardID)
}

// MarshalJSON is exposed so the agent can build the request payload with
// the canonical snake_case wire form even when constructing it via
// map[string]any (which already matches). Kept private to the package —
// the handler signature is the public surface, not the marshaller.
func (r StampWorkflowMutatingEffectsReq) MarshalJSON() ([]byte, error) {
	type alias StampWorkflowMutatingEffectsReq
	return json.Marshal(alias(r))
}
