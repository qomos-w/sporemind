package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// setStatusVerifyHook, when non-nil, runs between a set_status Save and its
// verification read. It exists solely so concurrency tests can widen the
// Save→verify window deterministically; production code never sets it.
var setStatusVerifyHook func(cardID string)

// maxSetStatusAttempts bounds the verify-and-replay loop below: one write plus
// two replays when concurrent writers keep winning before our verification
// read. Exhausting it fails loudly instead of silently dropping the transition.
const maxSetStatusAttempts = 3

// handleWikiSetStatus atomically updates the frontmatter status field of a card
// without requiring a full Raw rewrite. It is the lightweight path for
// self-done task cards (research/grilling) and programmatic status transitions
// (spawn_assign claim, review approve/reject).
//
// The read-modify-write is a validate loop, not a blind Get→Save: after each
// Save the card is re-read and compared against what we wrote. A concurrent
// writer that landed in between is detected by content mismatch and the
// transition is replayed on top of the winner's version, so a racing
// edit_card body change can never be silently reverted by a stale status
// write (the lost-update window behind card status rollbacks).
func (a *Actor) handleWikiSetStatus(ctx actor.PureContext, req domain.WikiSetStatusReq) (domain.WikiSetStatusResp, error) {
	canonical := normalizeTaskStatus(req.Status)
	if canonical == "" {
		return domain.WikiSetStatusResp{}, fmt.Errorf("project.wiki.set_status: status %q is not recognized", req.Status)
	}

	// Serialize same-title writes for the entire read-modify-write-verify
	// cycle so a concurrent edit_card cannot interleave between our Get and
	// Save (the remaining lost-update window after the Save→verify check).
	// Non-fsCardStore implementations fall back to Save per call; the loop
	// is still correct because saveLocked is only used when we hold the lock.
	var release func()
	var fsStore *fsCardStore
	if fs, ok := a.store.(*fsCardStore); ok {
		fsStore = fs
		release = fs.lockTitle(req.ID)
	}
	if release != nil {
		defer release()
	}

	var (
		previousStatus string
		existingType   string
		existingParent string
		saved          *CardRecord
		wrote          string // exact raw we persisted, for verification
	)

	for attempt := 0; ; attempt++ {
		existing, err := a.store.Get(req.ID)
		if err != nil {
			return domain.WikiSetStatusResp{}, fmt.Errorf("project.wiki.set_status: %w", err)
		}
		previousStatus = existing.Status
		existingType = existing.Type
		existingParent = existing.Parent

		if req.ExpectedStatus != "" && previousStatus != req.ExpectedStatus {
			return domain.WikiSetStatusResp{PreviousStatus: previousStatus},
				fmt.Errorf("project.wiki.set_status: expected status %q, got %q", req.ExpectedStatus, previousStatus)
		}

		// Claim guard: a task card may only enter "doing" once every depends_on
		// dependency is "done". This blocks premature assignment (spawn_assign,
		// goal_card_submit) regardless of the caller. Re-checked on every
		// attempt so a replay never bypasses a guard that became false.
		if canonical == "doing" {
			if unmet := a.unmetDependencies(existing); len(unmet) > 0 {
				return domain.WikiSetStatusResp{PreviousStatus: previousStatus},
					fmt.Errorf("project.wiki.set_status: cannot claim task card %q: dependencies not done: %s", req.ID, strings.Join(unmet, ", "))
			}
		}

		raw := setCardStatusInRaw(existing.Raw, canonical)
		if err := validateCard(req.ID, raw); err != nil {
			return domain.WikiSetStatusResp{}, fmt.Errorf("project.wiki.set_status: %w", err)
		}

		now := time.Now().UTC().Format(time.RFC3339)
		raw = ensureCardMeta(req.ID, raw, now)

		// Persist optional review evidence into the card's data block so the
		// reviewer's context survives the status transition and round-trips through
		// storage.
		if len(req.Evidence) > 0 {
			evidenceBytes, err := json.Marshal(req.Evidence)
			if err != nil {
				return domain.WikiSetStatusResp{}, fmt.Errorf("project.wiki.set_status: marshal evidence: %w", err)
			}
			raw = setCardDataFieldInRaw(raw, "review_evidence", string(evidenceBytes))
		}

		// When a task card transitions to done, strip any frozen
		// reviewChangeset snapshot. The normal path is reviewApprove
		// (which clears it explicitly), but a direct wiki_set_status to
		// "done" (e.g. human UI bypassing review) would leave a stale
		// frozen snapshot on a done card. Do this inside the write loop
		// (not via clearReviewChangesetCardData, which does its own
		// Get+Save and would deadlock against the title lock).
		if canonical == "done" && existingType == "task" {
			if existing.Data != nil {
				if _, ok := existing.Data["reviewChangeset"]; ok {
					raw = removeCardDataFieldInRaw(raw, "reviewChangeset")
				}
			}
		}

		card := &CardRecord{Title: req.ID, Raw: raw}
		if release != nil {
			if err := a.store.(*fsCardStore).saveLocked(card); err != nil {
				return domain.WikiSetStatusResp{}, fmt.Errorf("project.wiki.set_status: %w", err)
			}
		} else {
			if err := a.store.Save(card); err != nil {
				return domain.WikiSetStatusResp{}, fmt.Errorf("project.wiki.set_status: %w", err)
			}
		}
		wrote = raw

		if setStatusVerifyHook != nil {
			setStatusVerifyHook(req.ID)
		}

		// Verify the write survived: read the card back from disk, bypassing
		// the in-memory cache. Save's write-through refresh repopulates the
		// cache with our own bytes synchronously, and the fsnotify watcher
		// may not have processed a concurrent external write yet — a cached
		// read here would mask the racer and silently drop their edit. The
		// disk read also refreshes the cache, so a retry below replays on
		// top of the winner's version.
		if fsStore != nil {
			saved, err = fsStore.refreshFromDisk(req.ID)
		} else {
			saved, err = a.store.Get(req.ID)
		}
		if err == nil && saved.Raw == wrote {
			break
		}
		if attempt >= maxSetStatusAttempts-1 {
			if err != nil {
				return domain.WikiSetStatusResp{}, fmt.Errorf("project.wiki.set_status: verify after save: %w", err)
			}
			return domain.WikiSetStatusResp{PreviousStatus: previousStatus},
				fmt.Errorf("project.wiki.set_status: concurrent writers kept overwriting card %q (%d attempts)", req.ID, maxSetStatusAttempts)
		}
	}

	_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
		ID:       saved.Title,
		Modified: saved.Modified,
	})
	a.syncSchedulerCard(ctx, req.ID, wrote)
	a.syncCardMirrorToTopo(ctx, req.ID)

	// A4: when a task card reaches done, auto-complete the parent workflow map
	// if every included task is now done.
	// (reviewChangeset stripping is done inside the write loop above.)
	if canonical == "done" && existingType == "task" && existingParent != "" {
		a.maybeAutoCompleteRoot(ctx, existingParent)
	}

	return domain.WikiSetStatusResp{
		Card:           cardToListItem(saved),
		PreviousStatus: previousStatus,
	}, nil
}

// maybeAutoCompleteRoot transitions an ownerless workflow root to "done" when
// every task card in its task set is already "done". A root with a bound owner
// is never auto-completed: while the owner exists, the workflow stays
// in-progress and completion is the owner's explicit workflow_stop (merge +
// mark done), which the updater keeps urging via its tree-exhausted
// notification. It is a no-op if the root is already terminal (done/failed),
// not a workflow, or has no task scope.
func (a *Actor) maybeAutoCompleteRoot(ctx actor.PureContext, rootID string) {
	root, err := a.store.Get(rootID)
	if err != nil || root == nil {
		return
	}
	if root.Type != "workflow" || root.Status == "done" || root.Status == "failed" {
		return
	}
	if ownerID, _ := root.Data["ownerAgentId"].(string); ownerID != "" {
		return
	}
	includeIDs := a.mapTaskIDs(rootID, root)
	if len(includeIDs) == 0 {
		return
	}
	if !a.allTaskCardsDone(includeIDs) {
		return
	}
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: rootID, Status: "done"}); err != nil {
		ctx.Logger().Warn("workflow: auto-complete root failed", "root", rootID, "error", err)
	}
}

// handleWikiClaimTaskCard fuses "CAS status + read body" into one atomic
// step for the workspace spawn_assign/agent_assign claim path: the workspace
// ownerLoop waits on a single project invoke instead of up to three serial
// ones. Semantics mirror handleWikiSetStatus, except the CAS guard accepts a
// set of expected statuses and the response carries the saved raw.
func (a *Actor) handleWikiClaimTaskCard(ctx actor.PureContext, req domain.WikiClaimTaskCardReq) (domain.WikiClaimTaskCardResp, error) {
	canonical := normalizeTaskStatus(req.Status)
	if canonical == "" {
		return domain.WikiClaimTaskCardResp{}, fmt.Errorf("project.wiki.claim_task_card: status %q is not recognized", req.Status)
	}
	if len(req.ExpectedStatuses) == 0 {
		return domain.WikiClaimTaskCardResp{}, fmt.Errorf("project.wiki.claim_task_card: ExpectedStatuses is required (claim must be guarded)")
	}

	existing, err := a.store.Get(req.ID)
	if err != nil {
		return domain.WikiClaimTaskCardResp{}, fmt.Errorf("project.wiki.claim_task_card: %w", err)
	}
	previousStatus := existing.Status

	match := false
	for _, exp := range req.ExpectedStatuses {
		if previousStatus == exp {
			match = true
			break
		}
	}
	if !match {
		return domain.WikiClaimTaskCardResp{PreviousStatus: previousStatus},
			fmt.Errorf("project.wiki.claim_task_card: expected status one of %v, got %q", req.ExpectedStatuses, previousStatus)
	}

	// Claim guard mirrors handleWikiSetStatus, including the
	// "dependencies not done" phrasing that workspace string-matches.
	if canonical == "doing" {
		if unmet := a.unmetDependencies(existing); len(unmet) > 0 {
			return domain.WikiClaimTaskCardResp{PreviousStatus: previousStatus},
				fmt.Errorf("project.wiki.claim_task_card: cannot claim task card %q: dependencies not done: %s", req.ID, strings.Join(unmet, ", "))
		}
	}

	raw := setCardStatusInRaw(existing.Raw, canonical)
	if err := validateCard(req.ID, raw); err != nil {
		return domain.WikiClaimTaskCardResp{}, fmt.Errorf("project.wiki.claim_task_card: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	raw = ensureCardMeta(req.ID, raw, now)
	card := &CardRecord{Title: req.ID, Raw: raw}
	if err := a.store.Save(card); err != nil {
		return domain.WikiClaimTaskCardResp{}, fmt.Errorf("project.wiki.claim_task_card: %w", err)
	}

	saved, _ := a.store.Get(req.ID)
	_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
		ID:       saved.Title,
		Modified: saved.Modified,
	})
	a.syncSchedulerCard(ctx, req.ID, raw)
	a.syncCardMirrorToTopo(ctx, req.ID)

	// Resolve data-flow bindings into an inputs map so the workspace
	// spawn_assign/agent_assign path can inject upstream outputs into the
	// worker's goal condition. Only populated when the task has a parent map
	// (orphan cards have no graph bindings).
	var inputs map[string]any
	if existing.Parent != "" {
		inputs = a.resolveTaskBindings(existing.Parent, req.ID)
	}

	return domain.WikiClaimTaskCardResp{
		Card:           cardToListItem(saved),
		PreviousStatus: previousStatus,
		Raw:            saved.Raw,
		Inputs:         inputs,
	}, nil
}

// setCardStatusInRaw rewrites or inserts the status field in the card's
// frontmatter. If the card has no frontmatter, the status is prepended in a
// new frontmatter block.
func setCardStatusInRaw(raw, status string) string {
	if !strings.HasPrefix(raw, "---") {
		return "---\nstatus: " + status + "\n---\n" + raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return "---\nstatus: " + status + "\n---\n" + raw
	}
	front := raw[3 : 3+end]
	lines := strings.Split(front, "\n")
	found := false
	for i, line := range lines {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		trimmed := strings.TrimSpace(line)
		key, _, ok := strings.Cut(trimmed, ":")
		if !ok || strings.TrimSpace(key) != "status" {
			continue
		}
		lines[i] = "status: " + status
		found = true
		break
	}
	if !found {
		lines = append(lines, "status: "+status)
	}
	return raw[:3] + joinFrontLines(lines) + raw[3+end:]
}

// setOwnerInDataBlock inserts or replaces the ownerAgentId field inside
// the frontmatter's data: block. If no data: block exists, one is created.
func setDependsOnInDataBlock(raw string, deps []string) string {
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
	value := yamlStringList(deps, "    ")
	if dataIdx < 0 {
		lines = append(lines, "data:", "  depends_on: "+value)
		return rebuildFront(lines, raw, end)
	}
	endIdx := dataIdx + 1
	for endIdx < len(lines) && (lines[endIdx] == "" || lines[endIdx][0] == ' ' || lines[endIdx][0] == '\t') {
		endIdx++
	}
	for i := dataIdx + 1; i < endIdx; i++ {
		if strings.TrimSpace(lines[i]) == "depends_on:" || strings.HasPrefix(strings.TrimSpace(lines[i]), "depends_on: ") {
			start := i
			i++
			// Consume only the value lines of depends_on: deeper-indented
			// list items (and blanks). A sibling key at the same or lower
			// indent (e.g. "  exec:") ends the value — swallowing it here
			// has historically deleted the whole rest of the data block
			// (exec, capabilities, budget) on every depends_on rewrite.
			for i < endIdx {
				if lines[i] == "" {
					i++
					continue
				}
				if lines[i][0] != ' ' && lines[i][0] != '\t' {
					break
				}
				if leadingSpaceCount(lines[i]) <= 2 {
					break
				}
				i++
			}
			lines = append(lines[:start], append([]string{"  depends_on: " + value}, lines[i:]...)...)
			return rebuildFront(lines, raw, end)
		}
	}
	lines = append(lines[:endIdx], append([]string{"  depends_on: " + value}, lines[endIdx:]...)...)
	return rebuildFront(lines, raw, end)
}

func yamlStringList(values []string, indent string) string {
	if len(values) == 0 {
		return "[]\n"
	}
	return "\n" + indent + "- " + strings.Join(values, "\n"+indent+"- ") + "\n"
}

// dataExecBlock renders an optional structured Exec pass-through map into a
// frontmatter fragment nested under the caller's data: block (the exec: key at
// the standard two-space child indent, deeper levels +2). An empty/nil map
// returns "" so callers that omit Exec keep byte-identical legacy raw output.
//
// This is a pass-through, not a validator: the map is serialized verbatim and
// the save-time validateCard gate (per-kind contract via pkg/scriptcard,
// including the script compile probe) judges it. The schema deliberately
// declares no per-kind fields (no typed capabilities/budget/gate_card) so that
// one gate stays the single source of truth for every exec kind.
//
// Keys are emitted in sorted order so the produced frontmatter is
// deterministic (stable tests and diffs, no map-iteration jitter); nested maps
// recurse (budget.max_duration_sec) and string lists become block bullets
// (capabilities).
func dataExecBlock(exec map[string]any) string {
	if len(exec) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("  exec:\n")
	writeYAMLMap(&b, exec, 4)
	return b.String()
}

// writeYAMLMap emits map entries with each key indented by indent spaces. The
// two-level nesting the frontmatter parser supports is exactly what data.exec
// needs (exec → budget → scalar); deeper maps recurse but the inline parser
// stops nesting, so callers should keep exec shallow.
func writeYAMLMap(b *strings.Builder, m map[string]any, indent int) {
	pad := strings.Repeat(" ", indent)
	for _, key := range sortedMapKeys(m) {
		switch v := m[key].(type) {
		case map[string]any:
			if len(v) == 0 {
				b.WriteString(pad + key + ": {}\n")
				continue
			}
			b.WriteString(pad + key + ":\n")
			writeYAMLMap(b, v, indent+2)
		case []any:
			writeYAMLList(b, key, v, indent, pad)
		case []string:
			items := make([]any, len(v))
			for i, s := range v {
				items[i] = s
			}
			writeYAMLList(b, key, items, indent, pad)
		default:
			b.WriteString(pad + key + ": " + yamlScalarLiteral(v) + "\n")
		}
	}
}

func writeYAMLList(b *strings.Builder, key string, items []any, indent int, pad string) {
	if len(items) == 0 {
		b.WriteString(pad + key + ": []\n")
		return
	}
	b.WriteString(pad + key + ":\n")
	itemPad := strings.Repeat(" ", indent+2)
	for _, item := range items {
		b.WriteString(itemPad + "- " + yamlScalarLiteral(item) + "\n")
	}
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// yamlScalarLiteral renders a scalar so both frontmatter parsers round-trip it
// faithfully. Strings are always double-quoted: the inline parsers unquote by
// stripping the outer pair only, so a value with a colon, a leading dash, or an
// embedded quote survives, while a bare token could be re-read as a bool/number
// (type drift between the Go and TS parsers). Bools and numbers stay bare so
// integer budgets decode as numbers on the frontend and as digit strings on the
// Go side (scriptcard accepts both).
func yamlScalarLiteral(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return quoteValue(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case json.Number:
		return v.String()
	default:
		return quoteValue(fmt.Sprint(v))
	}
}

// leadingSpaceCount returns the number of leading space characters. Tabs count
// as one column each; the helper only needs to distinguish sibling keys (≤2)
// from deeper-indented value lines.
func leadingSpaceCount(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' {
			n++
			continue
		}
		if r == '\t' {
			n++
			continue
		}
		break
	}
	return n
}

func setOwnerInDataBlock(raw, ownerID string) string {
	return setKeyInDataBlock(raw, "ownerAgentId", ownerID)
}

// setKeyInDataBlock inserts or replaces a scalar key inside the frontmatter's
// data: block. If no data: block exists, one is created. The key is stamped at
// the standard two-space child indentation, mirroring ownerAgentId.
func setKeyInDataBlock(raw, key, value string) string {
	field := "  " + key + ": " + value
	if !strings.HasPrefix(raw, "---") {
		return "---\ndata:\n" + field + "\n---\n" + raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return "---\ndata:\n" + field + "\n---\n" + raw
	}
	front := raw[3 : 3+end]
	lines := strings.Split(front, "\n")

	// Find the data: block and its indentation scope.
	dataLineIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		k, _, ok := strings.Cut(trimmed, ":")
		if ok && strings.TrimSpace(k) == "data" && (len(line) == 0 || line[0] != ' ') {
			dataLineIdx = i
			break
		}
	}

	if dataLineIdx < 0 {
		// No data: block — add one before the closing ---.
		lines = append(lines, "data:", field)
	} else {
		// Scan children of data: for an existing key.
		found := false
		insertAt := dataLineIdx + 1
		for i := dataLineIdx + 1; i < len(lines); i++ {
			line := lines[i]
			if line == "" {
				continue
			}
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
				break // left the data block
			}
			if strings.Contains(strings.TrimSpace(line), key+":") {
				pad := line[:len(line)-len(strings.TrimLeft(line, " "))]
				lines[i] = pad + key + ": " + value
				found = true
				break
			}
			insertAt = i + 1
		}
		if !found {
			lines = append(lines[:insertAt], append([]string{field}, lines[insertAt:]...)...)
		}
	}

	return raw[:3] + joinFrontLines(lines) + raw[3+end:]
}

// removeKeyFromDataBlock deletes key from the raw frontmatter's data: block
// (no-op when absent). Mirrors setKeyInDataBlock's block detection so a
// cleared stamp leaves no empty residue in the card frontmatter.
func removeKeyFromDataBlock(raw, key string) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return raw
	}
	front := raw[3 : 3+end]
	lines := strings.Split(front, "\n")

	dataLineIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		k, _, ok := strings.Cut(trimmed, ":")
		if ok && strings.TrimSpace(k) == "data" && (len(line) == 0 || line[0] != ' ') {
			dataLineIdx = i
			break
		}
	}
	if dataLineIdx < 0 {
		return raw
	}
	var removeIdx []int
	for i := dataLineIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			break // left the data block
		}
		k, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.TrimSpace(k) == key {
			removeIdx = append(removeIdx, i)
		}
	}
	if len(removeIdx) == 0 {
		return raw
	}
	for j := len(removeIdx) - 1; j >= 0; j-- {
		i := removeIdx[j]
		lines = append(lines[:i], lines[i+1:]...)
	}
	return raw[:3] + joinFrontLines(lines) + raw[3+end:]
}

// handleWikiSetMapOwner stamps an agent's actor id into a card's
// Data.ownerAgentId, binding the agent to the card. Accepts both workflow
// map cards (orchestrator binding) and task cards (worker binding).
// Map cards enforce single-binding: rejects if already bound to a different
// agent. Task cards allow overwrite: the CAS claim already guards ownership,
// so a stale binding from a crashed worker is replaced.
func (a *Actor) handleWikiSetMapOwner(ctx actor.PureContext, req domain.WikiSetMapOwnerReq) (domain.WikiSetMapOwnerResp, error) {
	if req.MapID == "" {
		return domain.WikiSetMapOwnerResp{}, fmt.Errorf("project.wiki.set_map_owner: MapId is required")
	}
	if req.OwnerActorID == "" {
		return domain.WikiSetMapOwnerResp{}, fmt.Errorf("project.wiki.set_map_owner: OwnerActorId is required")
	}

	existing, err := a.store.Get(req.MapID)
	if err != nil {
		return domain.WikiSetMapOwnerResp{}, fmt.Errorf("project.wiki.set_map_owner: %w", err)
	}
	if existing.Type != "workflow" && existing.Type != "task" {
		return domain.WikiSetMapOwnerResp{}, fmt.Errorf("project.wiki.set_map_owner: card %q is type %q, not workflow or task", req.MapID, existing.Type)
	}

	prevOwner, _ := existing.Data["ownerAgentId"].(string)
	if existing.Type == "workflow" && prevOwner != "" && prevOwner != req.OwnerActorID {
		return domain.WikiSetMapOwnerResp{PreviousOwnerActorID: prevOwner},
			fmt.Errorf("project.wiki.set_map_owner: card %q already bound to owner %q", req.MapID, prevOwner)
	}

	raw := setOwnerInDataBlock(existing.Raw, req.OwnerActorID)
	if err := validateCard(req.MapID, raw); err != nil {
		return domain.WikiSetMapOwnerResp{}, fmt.Errorf("project.wiki.set_map_owner: %w", err)
	}
	raw = ensureCardMeta(req.MapID, raw, time.Now().UTC().Format(time.RFC3339))
	card := &CardRecord{Title: req.MapID, Raw: raw}
	if err := a.store.Save(card); err != nil {
		return domain.WikiSetMapOwnerResp{}, fmt.Errorf("project.wiki.set_map_owner: %w", err)
	}

	saved, _ := a.store.Get(req.MapID)
	_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
		ID:       saved.Title,
		Modified: saved.Modified,
	})
	return domain.WikiSetMapOwnerResp{
		Card:                 cardToListItem(saved),
		PreviousOwnerActorID: prevOwner,
	}, nil
}

// handleWikiUnbindAgent is the agent-deletion hook. Called by the workspace
// deletion path (cascade teardown) so the project can clear the deleted
// agent's actor id from every card it was bound to (data.ownerAgentId on
// workflow map cards), returning those cards to the unbound state.
// Internal-only; idempotent — unknown ids unbind nothing.
func (a *Actor) handleWikiUnbindAgent(ctx actor.PureContext, req gen.ProjectWikiUnbindAgentReq) error {
	if req.AgentActorID == "" {
		return fmt.Errorf("project.wiki.unbind_agent: AgentActorID required")
	}
	cards, err := a.store.List()
	if err != nil {
		return fmt.Errorf("project.wiki.unbind_agent: %w", err)
	}
	for _, card := range cards {
		ownerID, _ := card.Data["ownerAgentId"].(string)
		if ownerID != req.AgentActorID {
			continue
		}
		if !a.unbindMapOwner(ctx, card.Title) {
			continue
		}
		saved, _ := a.store.Get(card.Title)
		if saved != nil {
			_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
				ID:       saved.Title,
				Modified: saved.Modified,
			})
		}
		// A4: the bound agent is being deleted, so the workflow root is
		// abandoned. Mark it failed unless every included task is already done
		// (in which case auto-complete to done is the more accurate terminal
		// state). Owner-agent-id has already been cleared above.
		a.maybeAutoFailRoot(ctx, card.Title)
		ctx.Logger().Info("workflow: unbound deleted agent from card", "card", card.Title, "agent", req.AgentActorID)
	}
	return nil
}

// maybeAutoFailRoot transitions a workflow root to "failed" when its owner
// agent is gone and at least one included task is not yet done. If all included
// tasks are done it transitions to "done" instead. No-op if the root is already
// terminal (done/failed), not a workflow, or has no include scope.
func (a *Actor) maybeAutoFailRoot(ctx actor.PureContext, rootID string) {
	root, err := a.store.Get(rootID)
	if err != nil || root == nil {
		return
	}
	if root.Type != "workflow" || root.Status == "done" || root.Status == "failed" {
		return
	}
	includeIDs := a.mapTaskIDs(rootID, root)
	if len(includeIDs) == 0 {
		return
	}
	target := "failed"
	if a.allTaskCardsDone(includeIDs) {
		target = "done"
	}
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: rootID, Status: target}); err != nil {
		ctx.Logger().Warn("workflow: auto-fail root failed", "root", rootID, "target", target, "error", err)
	}
}

// currently claimable: status ∈ {backlog, todo} AND all depends_on dependencies
// are done. The function is deterministic Go — no LLM reasoning.
//
// Task cards are scoped by the root map card's Data.scope.Include array.
// depends_on edges are read from each task card's Data.depends_on list.
func (a *Actor) handleWikiFrontier(_ actor.PureContext, req domain.WikiFrontierReq) (domain.WikiFrontierResp, error) {
	if req.MapID == "" {
		return domain.WikiFrontierResp{}, fmt.Errorf("project.wiki.frontier: MapId is required")
	}

	root, err := a.store.Get(req.MapID)
	if err != nil {
		return domain.WikiFrontierResp{}, fmt.Errorf("project.wiki.frontier: %w", err)
	}

	taskIDs := a.mapTaskIDs(req.MapID, root)
	if len(taskIDs) == 0 {
		return domain.WikiFrontierResp{TaskCards: []gen.FrontierTaskCard{}}, nil
	}

	return domain.WikiFrontierResp{TaskCards: a.computeFrontier(req.MapID, taskIDs)}, nil
}

// computeFrontier is the single deterministic frontier computation shared by
// the wiki_frontier callable and the workflow updater: task cards that are
// unclaimed (status backlog/todo) and whose all depends_on dependencies are
// done. Dependencies are read from the authoritative workflow_topo graph
// snapshot (reconciled from frontmatter on first access for maps that predate
// the graph feature). A cycle in the graph would make frontier undefined;
// topoHasCycle is the write-time guard, and the natural computation already
// keeps cyclic nodes out of the frontier (each blocks the other).
func (a *Actor) computeFrontier(mapID string, includeIDs []string) []gen.FrontierTaskCard {
	g := a.reconcileWorkflowTopo(mapID)

	// Status cache: cardId → status. Covers both include IDs and their deps
	// (deps may reference cards outside this map's scope).
	statusCache := make(map[string]string, len(includeIDs))
	for _, id := range includeIDs {
		statusCache[id] = cardStatusByID(a.store, id)
	}
	for _, id := range includeIDs {
		for _, dep := range topoDependencies(&g, id) {
			if _, ok := statusCache[dep]; !ok {
				statusCache[dep] = cardStatusByID(a.store, dep)
			}
		}
	}

	var frontier []gen.FrontierTaskCard
	for _, id := range includeIDs {
		status := statusCache[id]
		if status != "backlog" && status != "todo" {
			continue // not unclaimed
		}
		allDepsDone := true
		for _, depID := range topoDependencies(&g, id) {
			if statusCache[depID] != "done" {
				allDepsDone = false
				break
			}
		}
		if !allDepsDone {
			continue
		}
		card, err := a.store.Get(id)
		if err != nil {
			continue
		}
		frontier = append(frontier, gen.FrontierTaskCard{
			ID:     card.Title,
			Title:  card.Title,
			Status: status,
			Tags:   card.Tags,
		})
	}
	if frontier == nil {
		frontier = []gen.FrontierTaskCard{}
	}
	return frontier
}

// allTaskCardsDone returns true when every card in includeIDs exists and has
// status "done". It is used by the root auto-done/failed paths.
func (a *Actor) allTaskCardsDone(includeIDs []string) bool {
	for _, id := range includeIDs {
		card, err := a.store.Get(id)
		if err != nil || card == nil || card.Status != "done" {
			return false
		}
	}
	return true
}

// scopeIncludeIDs extracts the include task-card-id list from a root map card's
// Data["include"]. Returns nil if absent or malformed.
func scopeIncludeIDs(root *CardRecord) []string {
	if root == nil || root.Data == nil {
		return nil
	}
	if scope, ok := root.Data["scope"].(map[string]any); ok {
		if ids := toStringSlice(scope["include"]); len(ids) > 0 {
			return ids
		}
	}
	return toStringSlice(root.Data["include"])
}

// mapTaskIDs returns the authoritative task-card id set for a workflow map:
// the union of the workflow_topo graph nodes (create_task_card upserts each
// node there; the graph is never pruned, so it survives frontmatter
// data.scope.include truncation from concurrent-append races) and the
// frontmatter include projection. Graph nodes whose backing card no longer
// exists are dropped so a stale graph entry can't wedge the set. Order:
// include order first (stable for human readers), then graph-only ids.
//
// The updater gate additionally falls back to the task card's own parent
// field (see gatherUpdaterInputs), so membership holds even when both the
// graph sync and the include append were lost.
func (a *Actor) mapTaskIDs(mapID string, card *CardRecord) []string {
	include := scopeIncludeIDs(card)
	g := a.reconcileWorkflowTopo(mapID)
	if len(g.Nodes) == 0 {
		return include
	}
	seen := make(map[string]struct{}, len(include)+len(g.Nodes))
	ids := make([]string, 0, len(include)+len(g.Nodes))
	for _, id := range include {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for i := range g.Nodes {
		id := g.Nodes[i].ID
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if _, err := a.store.Get(id); err != nil {
			continue // graph node for a deleted card — drop the stale entry
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// collectDependsOn reads data.depends_on from the task cards scoped by a map.
// It reads frontmatter (the projection), NOT the authoritative graph — use this
// when you need to inspect what frontmatter says (e.g. consistency checks). For
// frontier/claim decisions use the graph via reconcileWorkflowTopo instead.
func (a *Actor) collectDependsOn(mapID string) map[string][]string {
	mapCard, err := a.store.Get(mapID)
	if err != nil {
		return map[string][]string{}
	}
	includeIDs := scopeIncludeIDs(mapCard)
	deps := make(map[string][]string, len(includeIDs))
	for _, id := range includeIDs {
		card, err := a.store.Get(id)
		if err != nil || card.Data == nil {
			continue
		}
		deps[id] = toStringSlice(card.Data["depends_on"])
	}
	return deps
}

// unmetDependencies returns the depends_on ids of a task card whose status is
// not yet "done". Returns nil when the card has no depends_on or all are done.
// It is the single invariant guard for claiming a task card: a worker may only
// start (status → doing) once every dependency is resolved. Dependencies are
// read from the authoritative workflow_topo graph snapshot (reconciled from
// frontmatter on first access). If the card has no parent map, frontmatter is
// read directly as a fallback.
func (a *Actor) unmetDependencies(card *CardRecord) []string {
	if card == nil {
		return nil
	}
	var deps []string
	if card.Parent != "" {
		g := a.reconcileWorkflowTopo(card.Parent)
		deps = topoDependencies(&g, card.Title)
	} else if card.Data != nil {
		deps = toStringSlice(card.Data["depends_on"])
	}
	var unmet []string
	for _, depID := range deps {
		if cardStatusByID(a.store, depID) != "done" {
			unmet = append(unmet, depID)
		}
	}
	return unmet
}

// cardStatusByID reads a card's status by id, returning "" on error.
func cardStatusByID(store CardStore, id string) string {
	card, err := store.Get(id)
	if err != nil {
		return ""
	}
	return card.Status
}

// toStringSlice converts an any (typically []any from JSON/Data) to []string.
// It also accepts the inline "[a, b]" string form: the frontmatter data parser
// stores flow-style lists as plain strings (see parseDataBlock), while writers
// such as appendIncludeID may emit either form after re-serialization.
func toStringSlice(v any) []string {
	switch val := v.(type) {
	case []string:
		return val
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		text := strings.Trim(strings.TrimSpace(val), "[]")
		if text == "" {
			return nil
		}
		parts := strings.Split(text, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// ── workflow card creation callables ──

// handleWikiCreateMap creates a root map card with the correct frontmatter
// (type=map, status=doing, data.scope.include=[]) and a body template. This
// replaces hand-written YAML by the LLM, which frequently gets the data.scope
// nesting wrong.
func (a *Actor) handleWikiCreateMap(ctx actor.PureContext, req domain.WikiCreateMapReq) (domain.WikiCreateMapResp, error) {
	if req.ID == "" {
		return domain.WikiCreateMapResp{}, fmt.Errorf("project.wiki.create_map: Id is required")
	}
	if len(req.ID) > maxCardIDLen {
		return domain.WikiCreateMapResp{}, fmt.Errorf("project.wiki.create_map: Id is too long (max %d bytes): %q", maxCardIDLen, truncateForError(req.ID))
	}
	if strings.HasPrefix(req.ID, builtinCardPrefix) {
		return domain.WikiCreateMapResp{}, fmt.Errorf("project.wiki.create_map: cannot create builtin card")
	}
	if a.externalProviderFor(req.ID) != nil {
		return domain.WikiCreateMapResp{}, fmt.Errorf("project.wiki.create_map: cannot create external card")
	}
	if _, err := a.store.Get(req.ID); err == nil {
		return domain.WikiCreateMapResp{}, fmt.Errorf("project.wiki.create_map: card %q already exists", req.ID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	body := "# " + req.ID + "\n"
	if req.Destination != "" {
		body += "\n**Destination:** " + req.Destination + "\n"
	}
	if req.Notes != "" {
		body += "\n" + req.Notes + "\n"
	}
	body += "\n## Not yet specified\n\n_(open questions, graduated into task cards as the frontier reaches them)_\n"
	body += "\n## Decisions-so-far\n\n_(empty — appended as task cards resolve)_\n"

	raw := "---\nid: " + req.ID + "\ntype: workflow\ntags: []\nstatus: doing\n" +
		"created: " + quoteValue(now) + "\nmodified: " + quoteValue(now) + "\n" +
		"data:\n  scope:\n    include: []\n---\n\n" + body

	if err := validateCard(req.ID, raw); err != nil {
		return domain.WikiCreateMapResp{}, fmt.Errorf("project.wiki.create_map: %w", err)
	}
	card := &CardRecord{Title: req.ID, Raw: raw}
	if err := a.store.Save(card); err != nil {
		return domain.WikiCreateMapResp{}, fmt.Errorf("project.wiki.create_map: %w", err)
	}

	// Initialize the authoritative workflow_topo graph snapshot (double-write).
	// The graph is the source of truth for topology; frontmatter scope.include
	// is a projection. Idempotent if a snapshot already exists.
	a.initWorkflowTopo(ctx, req.ID)

	saved, _ := a.store.Get(req.ID)
	a.emitCardChanged(ctx, saved)
	return domain.WikiCreateMapResp{Card: cardToListItem(saved)}, nil
}

// handleWikiCreateTaskCard creates a task card under a map, stores optional
// dependencies in data.depends_on, and appends its id to data.scope.include.
func (a *Actor) handleWikiCreateTaskCard(ctx actor.PureContext, req domain.WikiCreateTaskCardReq) (domain.WikiCreateTaskCardResp, error) {
	if req.MapID == "" {
		return domain.WikiCreateTaskCardResp{}, fmt.Errorf("project.wiki.create_task_card: MapId is required")
	}
	if req.Title == "" {
		return domain.WikiCreateTaskCardResp{}, fmt.Errorf("project.wiki.create_task_card: Title is required")
	}
	if len(req.Title) > maxCardIDLen {
		return domain.WikiCreateTaskCardResp{}, fmt.Errorf("project.wiki.create_task_card: Title is too long (max %d bytes): %q", maxCardIDLen, truncateForError(req.Title))
	}
	if strings.HasPrefix(req.Title, builtinCardPrefix) {
		return domain.WikiCreateTaskCardResp{}, fmt.Errorf("project.wiki.create_task_card: cannot create builtin card")
	}
	if a.externalProviderFor(req.Title) != nil {
		return domain.WikiCreateTaskCardResp{}, fmt.Errorf("project.wiki.create_task_card: cannot create external card")
	}
	if _, err := a.store.Get(req.Title); err == nil {
		return domain.WikiCreateTaskCardResp{}, fmt.Errorf("project.wiki.create_task_card: card %q already exists", req.Title)
	}

	mapCard, err := a.store.Get(req.MapID)
	if err != nil {
		return domain.WikiCreateTaskCardResp{}, fmt.Errorf("project.wiki.create_task_card: map %q not found: %w", req.MapID, err)
	}
	if mapCard.Type != "workflow" {
		return domain.WikiCreateTaskCardResp{}, fmt.Errorf("project.wiki.create_task_card: card %q is type %q, not workflow", req.MapID, mapCard.Type)
	}

	// Validate data-flow bindings before any persist so a bad binding
	// (FromNode not in DependsOn) is rejected synchronously without
	// half-creating the task card. Bindings also enforce the data-flow
	// invariant that the upstream must be one of the declared deps so it
	// completes before the downstream can claim.
	if err := validateBindings(req.DependsOn, toWorkflowBindings(req.Bindings)); err != nil {
		return domain.WikiCreateTaskCardResp{}, fmt.Errorf("project.wiki.create_task_card: %w", err)
	}

	status := req.Status
	if status == "" {
		status = "todo"
	}

	now := time.Now().UTC().Format(time.RFC3339)
	dependsOn := yamlStringList(req.DependsOn, "    ")
	// category is optional and written to data.category only when declared;
	// absent category keeps cards valid for backward compatibility. The line
	// must end with a newline so the closing --- delimiter below always starts
	// its own line (a glued "value---" closer splits frontmatter parsing in
	// two between the tolerant backend and the strict frontend parser).
	var categoryLine string
	if category := strings.TrimSpace(req.Category); category != "" {
		categoryLine = "\n  category: " + quoteValue(category) + "\n"
	}
	raw := "---\nid: " + req.Title + "\ntype: task\ntags: [" + req.MapID + "]\n" +
		"status: " + status + "\nparent: " + req.MapID + "\n" +
		"created: " + quoteValue(now) + "\nmodified: " + quoteValue(now) + "\n" +
		"data:\n  depends_on: " + dependsOn + categoryLine + dataExecBlock(req.Exec) + "---\n\n" + req.Question + "\n"

	if err := validateCard(req.Title, raw); err != nil {
		return domain.WikiCreateTaskCardResp{}, fmt.Errorf("project.wiki.create_task_card: %w", err)
	}
	card := &CardRecord{Title: req.Title, Raw: raw}
	if err := a.store.Save(card); err != nil {
		return domain.WikiCreateTaskCardResp{}, fmt.Errorf("project.wiki.create_task_card: %w", err)
	}

	// Append the new task id to the map's include list. The whole-card
	// Get→append→Save is serialized by includeAppendMu: concurrent
	// create_task_card calls on one map would otherwise last-writer-win and
	// silently drop every loser's append (observed as truncated
	// data.scope.include after parallel card creation).
	a.includeAppendMu.Lock()
	updatedMap, err := a.store.Get(req.MapID)
	if err == nil {
		updatedMap.Raw = appendIncludeID(updatedMap.Raw, req.Title)
		// Re-stamp from the freshly-read status, not the pre-create snapshot:
		// a concurrent wiki_set_status between the two reads must win.
		updatedMap.Raw = setCardStatusInRaw(updatedMap.Raw, updatedMap.Status)
		if err := validateCard(req.MapID, updatedMap.Raw); err == nil {
			if err := a.store.Save(updatedMap); err != nil {
				ctx.Logger().Warn("workflow: include append save failed", "map", req.MapID, "task", req.Title, "error", err)
			}
		}
	}
	a.includeAppendMu.Unlock()

	// Double-write: graph snapshot (authoritative) + frontmatter (projection).
	// The graph stores the node + depends_on edges + data-flow bindings;
	// frontmatter is derived. The bindings have already been validated above
	// (so the only remaining error modes are storage failures), which the
	// graph can self-heal from via lazy migration — log and continue.
	// Concurrent creates race the optimistic lock (both load the same
	// revision); retry on lock mismatch with a fresh load so the node always
	// lands in the authoritative graph.
	for attempt := 0; ; attempt++ {
		err := a.syncTaskCardToTopo(ctx, req.MapID, req.Title, status, []string{req.MapID}, req.DependsOn, toWorkflowBindings(req.Bindings))
		if err == nil {
			break
		}
		if attempt >= 2 || !errors.Is(err, ErrWorkflowTopoLockMismatch) {
			ctx.Logger().Warn("workflow_topo: failed to sync task card to graph snapshot", "map", req.MapID, "task", req.Title, "error", err)
			break
		}
	}

	saved, _ := a.store.Get(req.Title)
	a.emitCardChanged(ctx, saved)
	updatedMap, _ = a.store.Get(req.MapID)
	a.emitCardChanged(ctx, updatedMap)
	return domain.WikiCreateTaskCardResp{Card: cardToListItem(saved)}, nil
}

// healTaskCardMapMembership repairs workflow scope membership when a task card
// was created through the generic wiki_create_card with its parent pointing at
// a workflow map — the path the workflow-tools bundle forbids in favor of
// wiki_create_task_card. The card's Parent is the ground truth: when it names
// an existing workflow map and the card is missing from the map's
// data.scope.include, the card is appended (under includeAppendMu, same
// serialized read-modify-write as create_task_card) and double-written into
// the topo graph from its own status/tags/data.depends_on. No-op for non-task
// cards, absent parents, non-workflow parents, and cards already in scope.
// edit_card can move a card under a map the same way; that path heals lazily
// via reconcileWorkflowTopo → adoptOrphanTaskCards on the next read.
func (a *Actor) healTaskCardMapMembership(ctx actor.PureContext, card *CardRecord) {
	if card == nil || card.Type != "task" || card.Parent == "" {
		return
	}
	parent, err := a.store.Get(card.Parent)
	if err != nil || parent.Type != "workflow" {
		return
	}
	for _, id := range scopeIncludeIDs(parent) {
		if id == card.Title {
			return
		}
	}
	ctx.Logger().Warn("workflow: task card created via generic wiki_create_card under workflow map; healing scope membership",
		"map", card.Parent, "task", card.Title)

	a.includeAppendMu.Lock()
	updated, err := a.store.Get(card.Parent)
	if err == nil {
		updated.Raw = appendIncludeID(updated.Raw, card.Title)
		// Re-stamp from the freshly-read status: a concurrent wiki_set_status
		// between the two reads must win (same rule as create_task_card).
		updated.Raw = setCardStatusInRaw(updated.Raw, updated.Status)
		if err := validateCard(card.Parent, updated.Raw); err == nil {
			if err := a.store.Save(updated); err != nil {
				ctx.Logger().Warn("workflow: include append save failed", "map", card.Parent, "task", card.Title, "error", err)
			}
		}
	}
	a.includeAppendMu.Unlock()

	// Double-write the node into the topo graph (authoritative), retrying the
	// optimistic-lock race like create_task_card does.
	for attempt := 0; ; attempt++ {
		err := a.syncTaskCardToTopo(ctx, card.Parent, card.Title, card.Status, card.Tags, toStringSlice(card.Data["depends_on"]), nil)
		if err == nil {
			break
		}
		if attempt >= 2 || !errors.Is(err, ErrWorkflowTopoLockMismatch) {
			ctx.Logger().Warn("workflow_topo: failed to sync healed task card to graph", "map", card.Parent, "task", card.Title, "error", err)
			break
		}
	}

	if updatedMap, err := a.store.Get(card.Parent); err == nil {
		a.emitCardChanged(ctx, updatedMap)
	}
}

// handleWikiSetTaskDependencies replaces the dependency list stored on a task
// card. The workflow map is used only to confirm that the task belongs to it.
func (a *Actor) handleWikiSetTaskDependencies(ctx actor.PureContext, req domain.WikiSetTaskDependenciesReq) (domain.WikiSetTaskDependenciesResp, error) {
	if req.MapID == "" || req.TaskID == "" {
		return domain.WikiSetTaskDependenciesResp{}, fmt.Errorf("project.wiki.set_task_dependencies: MapId and TaskId are required")
	}
	mapCard, err := a.store.Get(req.MapID)
	if err != nil || mapCard.Type != "workflow" || !containsString(a.mapTaskIDs(req.MapID, mapCard), req.TaskID) {
		return domain.WikiSetTaskDependenciesResp{}, fmt.Errorf("project.wiki.set_task_dependencies: task %q is not in workflow %q", req.TaskID, req.MapID)
	}
	// Graph-authoritative: validate and persist topology first. A cycle would
	// make frontier computation non-deterministic; reject before touching
	// frontmatter so the projection never diverges from the graph.
	g, err := a.syncTaskDepsToTopo(ctx, req.MapID, req.TaskID, req.DependsOn, toWorkflowBindings(req.Bindings))
	if err != nil {
		return domain.WikiSetTaskDependenciesResp{}, fmt.Errorf("project.wiki.set_task_dependencies: %w", err)
	}

	// Project: write depends_on back to frontmatter from the authoritative
	// graph. Non-fatal — the graph is the source of truth; frontmatter is a
	// derived projection for human readability and backward compatibility.
	if err := a.projectTopoToFrontmatter(req.TaskID, g); err != nil {
		ctx.Logger().Warn("workflow_topo: frontmatter projection failed", "map", req.MapID, "task", req.TaskID, "error", err)
	}

	saved, _ := a.store.Get(req.TaskID)
	a.emitCardChanged(ctx, saved)
	return domain.WikiSetTaskDependenciesResp{Revision: saved.Modified}, nil
}

// handleWikiListDependencies derives edges from task-card Markdown rather than
// graph snapshots.
func (a *Actor) handleWikiListDependencies(_ actor.PureContext, req domain.WikiListDependenciesReq) (domain.WikiListDependenciesResp, error) {
	cards, err := a.store.List()
	if err != nil {
		return domain.WikiListDependenciesResp{}, err
	}
	var edges []gen.TaskDepEdge
	for _, task := range cards {
		if task.Type != "task" || task.Parent == "" || (req.MapID != "" && task.Parent != req.MapID) {
			continue
		}
		for _, dep := range toStringSlice(task.Data["depends_on"]) {
			edges = append(edges, gen.TaskDepEdge{MapID: task.Parent, From: task.Title, To: dep})
		}
	}
	if edges == nil {
		edges = []gen.TaskDepEdge{}
	}
	return domain.WikiListDependenciesResp{Edges: edges}, nil
}

// appendIncludeID adds id to the include list in a map card's frontmatter.
// Handles data.scope.include (primary) and data.include (fallback); creates
// data.scope.include if neither exists.
func appendIncludeID(raw, id string) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return raw
	}
	front := raw[3 : 3+end]
	lines := strings.Split(front, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "include:") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "include:"))

		if rest == "[]" || rest == "" {
			lines[i] = indent + "include:"
			lines = insertAt(lines, i+1, indent+"  - "+id)
		} else if strings.HasPrefix(rest, "[") {
			// Inline flow-style list (produced by re-serialization): expand to
			// block style so subsequent appends and the data parser agree on
			// one form.
			lines[i] = indent + "include:"
			items := append(toStringSlice(rest), id)
			for j := len(items) - 1; j >= 0; j-- {
				lines = insertAt(lines, i+1, indent+"  - "+items[j])
			}
		} else {
			insertIdx := i + 1
			for j := i + 1; j < len(lines); j++ {
				curr := lines[j]
				if curr == "" {
					break
				}
				currIndent := curr[:len(curr)-len(strings.TrimLeft(curr, " "))]
				if len(currIndent) <= len(indent) {
					break
				}
				insertIdx = j + 1
			}
			lines = insertAt(lines, insertIdx, indent+"  - "+id)
		}
		return rebuildFront(lines, raw, end)
	}

	// No include: found — create data.scope.include.
	dataIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		key, _, ok := strings.Cut(trimmed, ":")
		if ok && strings.TrimSpace(key) == "data" && (len(line) == 0 || line[0] != ' ') {
			dataIdx = i
			break
		}
	}
	if dataIdx >= 0 {
		lines = insertAt(lines, dataIdx+1, "  scope:")
		lines = insertAt(lines, dataIdx+2, "    include:")
		lines = insertAt(lines, dataIdx+3, "      - "+id)
	} else {
		lines = append(lines, "data:", "  scope:", "    include:", "      - "+id)
	}
	return rebuildFront(lines, raw, end)
}

func insertAt(lines []string, idx int, item string) []string {
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:idx]...)
	out = append(out, item)
	out = append(out, lines[idx:]...)
	return out
}

// removeFromScopeInclude removes id from the include list in a map card's
// frontmatter (data.scope.include or data.include). Inverse of
// appendIncludeID. No-op if id is not present.
func removeFromScopeInclude(raw, id string) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return raw
	}
	front := raw[3 : 3+end]
	lines := strings.Split(front, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "include:") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "include:"))

		if strings.HasPrefix(rest, "[") {
			// Inline flow-style: parse, filter, re-serialize.
			items := toStringSlice(rest)
			filtered := make([]string, 0, len(items))
			for _, item := range items {
				if item != id {
					filtered = append(filtered, item)
				}
			}
			if len(filtered) == 0 {
				lines[i] = indent + "include: []"
			} else {
				lines[i] = indent + "include: [" + strings.Join(filtered, ", ") + "]"
			}
			return rebuildFront(lines, raw, end)
		}

		// Block-style: remove the matching "- id" line(s) under include:.
		for j := i + 1; j < len(lines); j++ {
			curr := lines[j]
			if curr == "" {
				break
			}
			currIndent := curr[:len(curr)-len(strings.TrimLeft(curr, " "))]
			if len(currIndent) <= len(indent) {
				break
			}
			if strings.TrimSpace(curr) == "- "+id {
				lines = append(lines[:j], lines[j+1:]...)
				j--
			}
		}
		return rebuildFront(lines, raw, end)
	}
	return raw
}

func rebuildFront(lines []string, raw string, end int) string {
	return raw[:3] + joinFrontLines(lines) + raw[3+end:]
}

func (a *Actor) emitCardChanged(ctx actor.PureContext, card *CardRecord) {
	_ = ctx.EmitEvent("card_changed", gen.WikiCardChangedEvent{
		ID:       card.Title,
		Modified: card.Modified,
	})
	a.syncSchedulerCard(ctx, card.Title, card.Raw)
}

// ── data binding: declaration conversion, resolution, output persistence ──

// toWorkflowBindings converts wire TaskDataBinding values into the internal
// WorkflowTopoBinding shape stored in the graph snapshot. The Node field is
// filled by topoSetNodeBindings from the owning task id, so it is left empty
// here.
func toWorkflowBindings(wire []gen.TaskDataBinding) []WorkflowTopoBinding {
	if len(wire) == 0 {
		return nil
	}
	out := make([]WorkflowTopoBinding, 0, len(wire))
	for _, b := range wire {
		out = append(out, WorkflowTopoBinding{
			Input:      b.Input,
			FromNode:   b.FromNode,
			FromOutput: b.FromOutput,
		})
	}
	return out
}

// resolveTaskBindings resolves the data-flow bindings declared for taskID in the
// workflow_topo graph of mapID into a concrete inputs map. For each binding
// (taskID.input ← fromNode.fromOutput):
//   - if fromNode == MapInputSourceNode ("$map"), the value is read from the
//     graph's map-level Inputs table (key = fromOutput). This is the path for
//     the three typed-inputs channels (startup text, instantiation params,
//     intake grilling promotion) — downstream tasks consume them identically.
//   - otherwise, the value is read from the upstream task card's persisted
//     data.task_outputs[fromOutput].
//
// Missing or unparseable values are silently skipped — the claim still succeeds,
// the worker just receives a partial (or empty) inputs set. This is intentional:
// a binding referencing an output the upstream did not produce is a soft
// mismatch, not a hard gate (the review path validates outputs against the
// contract; here we merely forward what exists).
func (a *Actor) resolveTaskBindings(mapID, taskID string) map[string]any {
	g := a.reconcileWorkflowTopo(mapID)
	bindings := topoNodeBindings(&g, taskID)
	if len(bindings) == 0 {
		return nil
	}
	// Cache upstream outputs: node id → parsed task_outputs map.
	cache := make(map[string]map[string]any, len(bindings))
	inputs := make(map[string]any, len(bindings))
	for _, b := range bindings {
		if b.FromNode == MapInputSourceNode {
			// Map-level input: read from the graph's Inputs table.
			if g.Inputs == nil {
				continue
			}
			val, present := g.Inputs[b.FromOutput]
			if !present {
				continue
			}
			inputs[b.Input] = val
			continue
		}
		upstream, ok := cache[b.FromNode]
		if !ok {
			upstream = a.cardTaskOutputs(b.FromNode)
			cache[b.FromNode] = upstream
		}
		if upstream == nil {
			continue
		}
		val, present := upstream[b.FromOutput]
		if !present {
			continue
		}
		inputs[b.Input] = val
	}
	if len(inputs) == 0 {
		return nil
	}
	return inputs
}

// cardTaskOutputs reads and parses the data.task_outputs block from a task
// card's frontmatter. The block is stored as a JSON-encoded string because the
// inline frontmatter parser only supports scalar/list/nested-map shapes, not
// arbitrary nested maps; we therefore JSON-decode on read. Returns nil when
// the card is absent, has no task_outputs block, or the stored value is not a
// JSON object.
func (a *Actor) cardTaskOutputs(cardID string) map[string]any {
	card, err := a.store.Get(cardID)
	if err != nil || card.Data == nil {
		return nil
	}
	raw, present := card.Data["task_outputs"]
	if !present {
		return nil
	}
	switch v := raw.(type) {
	case map[string]any:
		return v
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
			return nil
		}
		return m
	}
	return nil
}

// handleWikiSetTaskOutputs persists a worker's produced outputs onto the task
// card's data.task_outputs block. Called by the workspace review-approve path
// so that downstream tasks (via data bindings) can resolve upstream outputs
// after the worker agent has been torn down. The outputs are stored as a JSON
// map inside the frontmatter data block.
func (a *Actor) handleWikiSetTaskOutputs(ctx actor.PureContext, req domain.WikiSetTaskOutputsReq) (domain.WikiSetTaskOutputsResp, error) {
	if req.CardID == "" {
		return domain.WikiSetTaskOutputsResp{}, fmt.Errorf("project.wiki.set_task_outputs: CardId is required")
	}
	existing, err := a.store.Get(req.CardID)
	if err != nil {
		return domain.WikiSetTaskOutputsResp{}, fmt.Errorf("project.wiki.set_task_outputs: %w", err)
	}
	raw := setTaskOutputsInDataBlock(existing.Raw, req.Outputs)
	if err := validateCard(req.CardID, raw); err != nil {
		return domain.WikiSetTaskOutputsResp{}, fmt.Errorf("project.wiki.set_task_outputs: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	raw = ensureCardMeta(req.CardID, raw, now)
	card := &CardRecord{Title: req.CardID, Raw: raw}
	if err := a.store.Save(card); err != nil {
		return domain.WikiSetTaskOutputsResp{}, fmt.Errorf("project.wiki.set_task_outputs: %w", err)
	}
	saved, _ := a.store.Get(req.CardID)
	a.emitCardChanged(ctx, saved)
	return domain.WikiSetTaskOutputsResp{Card: cardToListItem(saved)}, nil
}

// setTaskOutputsInDataBlock inserts or replaces the task_outputs field inside
// the frontmatter's data: block, serializing the outputs map as a JSON string
// (frontmatter is not rich enough for arbitrary nested maps). If no data: block
// exists, one is created.
func setTaskOutputsInDataBlock(raw string, outputs map[string]any) string {
	jsonBytes, _ := json.Marshal(outputs)
	value := string(jsonBytes)
	return setDataBlockField(raw, "task_outputs", value)
}

// setDataBlockField inserts or replaces a named string field inside the
// frontmatter's data: block. If no data: block exists, one is created.
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
		return rebuildFront(lines, raw, end)
	}
	endIdx := dataIdx + 1
	for endIdx < len(lines) && (lines[endIdx] == "" || lines[endIdx][0] == ' ' || lines[endIdx][0] == '\t') {
		endIdx++
	}
	for i := dataIdx + 1; i < endIdx; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, field+":") {
			lines[i] = fieldLine
			return rebuildFront(lines, raw, end)
		}
	}
	lines = append(lines[:endIdx], append([]string{fieldLine}, lines[endIdx:]...)...)
	return rebuildFront(lines, raw, end)
}

// ── map-level typed inputs: set_map_inputs (channel a) ───────────────────────

// handleWikiSetMapInputs replaces the map-level inputs table for a workflow map
// (channel a of the typed-inputs three-channel system). The caller parses
// startup/intake text into a key-value map and writes it here; the values are
// stored verbatim in the graph's Inputs field. Downstream tasks consume them via
// bindings whose FromNode == MapInputSourceNode.
func (a *Actor) handleWikiSetMapInputs(ctx actor.PureContext, req domain.WikiSetMapInputsReq) (domain.WikiSetMapInputsResp, error) {
	if req.MapID == "" {
		return domain.WikiSetMapInputsResp{}, fmt.Errorf("project.wiki.set_map_inputs: MapId is required")
	}
	_, rev := a.loadWorkflowTopo(req.MapID)
	g := a.reconcileWorkflowTopo(req.MapID)
	topoSetMapInputs(&g, req.Inputs)
	if err := a.saveWorkflowTopo(ctx, req.MapID, g, rev); err != nil {
		return domain.WikiSetMapInputsResp{}, fmt.Errorf("project.wiki.set_map_inputs: %w", err)
	}
	_, newRev := a.loadWorkflowTopo(req.MapID)
	return domain.WikiSetMapInputsResp{Revision: newRev}, nil
}

// ── map-level typed inputs: promote_node_outputs (channel c) ─────────────────

// handleWikiPromoteNodeOutputs promotes a task node's persisted outputs into the
// map-level inputs table (channel c of the typed-inputs three-channel system).
// After an intake grilling node is reviewed and its outputs persisted (via
// set_task_outputs), this callable reads data.task_outputs from the node card
// and merges them into the map's Inputs table. The node's output field names
// become map-level input keys. Existing keys not in the promotion are preserved;
// promoted keys overwrite existing ones.
func (a *Actor) handleWikiPromoteNodeOutputs(ctx actor.PureContext, req domain.WikiPromoteNodeOutputsReq) (domain.WikiPromoteNodeOutputsResp, error) {
	if req.MapID == "" {
		return domain.WikiPromoteNodeOutputsResp{}, fmt.Errorf("project.wiki.promote_node_outputs: MapId is required")
	}
	if req.NodeID == "" {
		return domain.WikiPromoteNodeOutputsResp{}, fmt.Errorf("project.wiki.promote_node_outputs: NodeId is required")
	}
	outputs := a.cardTaskOutputs(req.NodeID)
	if outputs == nil {
		return domain.WikiPromoteNodeOutputsResp{}, fmt.Errorf("project.wiki.promote_node_outputs: node %q has no task_outputs to promote", req.NodeID)
	}
	_, rev := a.loadWorkflowTopo(req.MapID)
	g := a.reconcileWorkflowTopo(req.MapID)
	topoMergeMapInputs(&g, outputs)
	if err := a.saveWorkflowTopo(ctx, req.MapID, g, rev); err != nil {
		return domain.WikiPromoteNodeOutputsResp{}, fmt.Errorf("project.wiki.promote_node_outputs: %w", err)
	}
	savedGraph, newRev := a.loadWorkflowTopo(req.MapID)
	return domain.WikiPromoteNodeOutputsResp{Revision: newRev, Inputs: savedGraph.Inputs}, nil
}
