package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// scatterExecutor implements the scatter execKind: a dynamic fan-out node.
// When claimed, it reads an array from the upstream binding resolved into
// ClaimReq.Inputs (data.exec.source names the key), instantiates one child
// task card per element via project.wiki_create_task_card, and then waits.
// The scatter card itself stays "doing" while the children run; a 5s sweep
// (workspace.internal_scatter_sweep, mirroring the project updater tick that
// drives the children through the frontier) joins the fan-out: all children
// done → scatter done with data.task_outputs.items aggregating each child's
// outputs in element order; any child failed → scatter failed; any child
// cancelled → scatter cancelled.
//
// Fan-out state is tracked in the workspace actor's persisted scatterFanouts
// table (same pattern as subMapInstances) so the join survives restarts.
// The table is a projection — the cards are the ground truth — so the sweep
// reconciles card state first and converges the table onto it.
//
// Child cards carry NO depends_on edge to the scatter card: children become
// frontier-claimable immediately, and the scatter card's own done transition
// is produced by the join (depending on the scatter card would deadlock).
// Each child receives its element both inline ({{item}} / {{index}}
// placeholders in the template) and as a deterministic "Task Inputs" JSON
// block appended to the body.
//
// Re-entry is idempotent: child ids follow the stable naming convention
// <scatter-card-id>-item-<idx>, and a create that reports "already exists"
// is treated as success, so claim retries never duplicate instantiation.
type scatterExecutor struct {
	a *Actor
}

// newScatterExecutor wires the executor back to its hosting workspace.
func newScatterExecutor(a *Actor) *scatterExecutor {
	return &scatterExecutor{a: a}
}

// Kind returns "scatter".
func (e *scatterExecutor) Kind() ExecKind {
	return ExecKindScatter
}

const (
	// scatterDefaultMax bounds the fan-out when the card does not declare
	// data.exec.max. Large enough for ordinary batch plans, small enough
	// that a malformed upstream array cannot explode the card tree.
	scatterDefaultMax = 20

	// scatterAbsoluteMax is the hard ceiling on data.exec.max. Cards
	// declaring more are rejected in Preflight — a plan that legitimately
	// needs a wider fan-out should be restructured (nested maps / sub_map).
	scatterAbsoluteMax = 100

	// scatterChildIDFormat builds the stable per-element child card id.
	// The scatter card id is an exact prefix, so sibling scatter cards can
	// never collide (their ids differ before the "-item-" suffix).
	scatterChildIDFormat = "%s-item-%d"

	// scatterSweepInterval is the join sweep cadence; it matches the
	// project workflow updater tick that advances the children through
	// frontier notifications, so the join settles at the same granularity
	// as the rest of the map.
	scatterSweepInterval = 5 * time.Second

	// scatterInvokeTimeout bounds each single project round trip made by
	// the executor and the sweep (card reads/writes, child creation).
	scatterInvokeTimeout = 15 * time.Second
)

// scatterConfig holds the parsed data.exec fields for a scatter task card.
type scatterConfig struct {
	// Source names the key in ClaimReq.Inputs whose value must be an
	// array — the upstream binding (task_inputs) is resolved by the
	// dispatcher's claim before Execute runs.
	Source string
	// Template is the child card body template. {{item}} and {{index}}
	// placeholders are substituted per element; the element is also
	// appended as a machine-readable "Task Inputs" JSON block.
	Template string
	// Max caps the accepted element count; over-max arrays fail the card
	// loudly rather than silently truncating work.
	Max int
}

// requireScatterConfig parses the data.exec fields from the bound card raw.
// Used by both Preflight and Execute.
func requireScatterConfig(req ClaimReq) (scatterConfig, error) {
	card := project.ParseCardRaw(req.BoundTaskCardID, req.CardRaw)
	execBlock := project.CardDataMap(card, "exec")

	source, _ := execBlock["source"].(string)
	source = strings.TrimSpace(source)
	if source == "" {
		return scatterConfig{}, fmt.Errorf("workspace.executor.scatter: data.exec.source is required (upstream input key holding the array to fan out)")
	}
	template, _ := execBlock["template"].(string)
	if strings.TrimSpace(template) == "" {
		return scatterConfig{}, fmt.Errorf("workspace.executor.scatter: data.exec.template is required (child card body template)")
	}
	max, err := scatterMaxFromValue(execBlock["max"])
	if err != nil {
		return scatterConfig{}, fmt.Errorf("workspace.executor.scatter: %w", err)
	}
	return scatterConfig{Source: source, Template: template, Max: max}, nil
}

// scatterMaxFromValue decodes data.exec.max across the shapes the
// frontmatter decoder can produce (string per YAML scalar semantics,
// float64/int from typed values, json.Number from decoded JSON). Absent or
// empty yields the default; the result is range-checked against
// [1, scatterAbsoluteMax].
func scatterMaxFromValue(v any) (int, error) {
	var parsed int
	switch n := v.(type) {
	case nil:
		return scatterDefaultMax, nil
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return scatterDefaultMax, nil
		}
		p, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("data.exec.max %q is not an integer", s)
		}
		parsed = p
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("data.exec.max %v is not an integer", n)
		}
		parsed = int(n)
	case int:
		parsed = n
	case int64:
		parsed = int(n)
	case json.Number:
		p, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("data.exec.max %q is not an integer", n.String())
		}
		parsed = int(p)
	default:
		return 0, fmt.Errorf("data.exec.max has unsupported type %T", v)
	}
	if parsed < 1 {
		return 0, fmt.Errorf("data.exec.max must be >= 1, got %d", parsed)
	}
	if parsed > scatterAbsoluteMax {
		return 0, fmt.Errorf("data.exec.max must be <= %d, got %d", scatterAbsoluteMax, parsed)
	}
	return parsed, nil
}

// Preflight validates the scatter declaration before the dispatcher claims
// the card. Failures here leave the card untouched.
//
// Payload validation deliberately does NOT run here: the upstream array is
// only resolved into ClaimReq.Inputs by the claim (after Preflight), so its
// presence, type, and size are checked in Execute (mirroring the toolcall
// executor's argument-payload stance).
func (e *scatterExecutor) Preflight(ctx actor.PureContext, req ClaimReq) (PreflightResult, error) {
	if _, err := requireScatterConfig(req); err != nil {
		return PreflightResult{}, err
	}
	if err := requireActiveWorkflow(ctx, "workspace.executor.scatter", req.CallerAgentID); err != nil {
		return PreflightResult{}, err
	}
	return PreflightResult{
		KindConfig:  domain.AgentKindConfig{Kind: domain.AgentKindWorker, DisplayName: "ScatterExecutor"},
		DisplayName: "ScatterExecutor",
		SpawnName:   "scatter-" + req.BoundTaskCardID,
	}, nil
}

// Execute resolves the upstream array, instantiates the child cards
// (idempotently), records the fan-out, and runs one immediate join pass.
// The scatter card remains "doing" after a successful Execute; completion
// is driven asynchronously by the scatter sweep.
func (e *scatterExecutor) Execute(ctx actor.PureContext, req ClaimReq) (ExecResp, error) {
	return panicprobe.Guard(ctx, "workspace.executor.scatter", req, func() (ExecResp, error) {
		if req.Preflight == nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.scatter: Preflight result is required (dispatcher contract)")
		}
		cfg, err := requireScatterConfig(req)
		if err != nil {
			return ExecResp{}, err
		}

		projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
		if err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.scatter: %w", err)
		}

		// Resolve the upstream array. Bad payloads are card-level
		// failures (write error outputs + card failed, no claim
		// rollback) so the plan author sees a precise reason.
		rawItems, ok := req.Inputs[cfg.Source]
		if !ok {
			if ferr := e.failScatterCard(ctx, projectID, req.BoundTaskCardID,
				fmt.Sprintf("scatter source %q not resolved from upstream bindings (task_inputs)", cfg.Source)); ferr != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.scatter: %w", ferr)
			}
			return ExecResp{}, nil
		}
		items, ok := rawItems.([]any)
		if !ok {
			if ferr := e.failScatterCard(ctx, projectID, req.BoundTaskCardID,
				fmt.Sprintf("scatter source %q is not an array (got %T)", cfg.Source, rawItems)); ferr != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.scatter: %w", ferr)
			}
			return ExecResp{}, nil
		}
		if len(items) > cfg.Max {
			if ferr := e.failScatterCard(ctx, projectID, req.BoundTaskCardID,
				fmt.Sprintf("scatter source %q has %d elements, exceeds data.exec.max %d", cfg.Source, len(items), cfg.Max)); ferr != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.scatter: %w", ferr)
			}
			return ExecResp{}, nil
		}

		// The scatter card's parent map is where children are created;
		// create_task_card requires it, and it keeps children inside the
		// map's include/topo scope so the frontier picks them up.
		scatterCard := project.ParseCardRaw(req.BoundTaskCardID, req.CardRaw)
		mapID := scatterCard.Parent
		if mapID == "" {
			if ferr := e.failScatterCard(ctx, projectID, req.BoundTaskCardID,
				"scatter card has no parent map (parent field is required to scope child cards)"); ferr != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.scatter: %w", ferr)
			}
			return ExecResp{}, nil
		}

		childIDs := make([]string, len(items))
		for idx, item := range items {
			childID := fmt.Sprintf(scatterChildIDFormat, req.BoundTaskCardID, idx)
			childIDs[idx] = childID
			body := renderScatterChildBody(cfg.Template, idx, item)
			// Idempotent instantiation: a child that already exists
			// (claim retry, sweep self-heal, crash replay) is skipped.
			// Any other creation failure propagates as a dispatch
			// error so the dispatcher rolls the claim back and a
			// retry re-creates only the missing children.
			if cerr := e.a.createScatterChildCard(ctx, projectID, mapID, childID, body); cerr != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.scatter: create child %q: %w", childID, cerr)
			}
		}

		e.a.upsertScatterFanout(scatterFanout{
			ScatterCardID: req.BoundTaskCardID,
			ProjectID:     projectID,
			MapID:         mapID,
			Source:        cfg.Source,
			Template:      cfg.Template,
			Items:         items,
			ChildIDs:      childIDs,
			Status:        scatterStatusActive,
			CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		})

		// Immediate join pass: covers the zero-element fan-out (trivially
		// done) and a re-entry after the children already finished.
		e.a.reconcileScatterFanout(ctx, scatterFanout{
			ScatterCardID: req.BoundTaskCardID,
			ProjectID:     projectID,
			MapID:         mapID,
			Source:        cfg.Source,
			Template:      cfg.Template,
			Items:         items,
			ChildIDs:      childIDs,
			Status:        scatterStatusActive,
		})

		condition := fmt.Sprintf("Scatter fan-out of %d child task(s) from %q under map %s; the scatter card completes when every child is done.",
			len(items), cfg.Source, mapID)
		if len(items) == 0 {
			condition = fmt.Sprintf("Scatter fan-out from %q produced no elements; the scatter card completes immediately.", cfg.Source)
		}
		return ExecResp{
			DisplayName: "ScatterExecutor",
			Goal: gen.GoalSummary{
				Condition:       condition,
				Status:          "active",
				Confirmed:       true,
				BoundTaskCardID: req.BoundTaskCardID,
				MaxTurns:        req.MaxTurns,
			},
		}, nil
	})
}

// failScatterCard marks the scatter card failed with an error note, mirroring
// the toolcall executor's card-level failure semantics (no claim rollback:
// the card stays failed and the note lands in data.task_outputs).
func (e *scatterExecutor) failScatterCard(ctx actor.PureContext, projectID, cardID, note string) error {
	_ = e.a.scatterWriteOutputs(ctx, projectID, cardID, map[string]any{"error": note})
	if err := e.a.scatterSetCardStatus(ctx, projectID, cardID, "failed"); err != nil {
		return fmt.Errorf("set task card failed: %w", err)
	}
	return nil
}

// scatterItemInput is the deterministic shape of the per-child task-inputs
// block. A struct (not a map) so json.Marshal emits fields in declared
// order — item first, index second — keeping rendered bodies stable.
type scatterItemInput struct {
	Item  any `json:"item"`
	Index int `json:"index"`
}

// renderScatterChildBody builds the child card body: the template with
// {{item}} / {{index}} substituted, plus a deterministic "Task Inputs" JSON
// block carrying the element and its index (the same contract shape the
// dispatcher's injectTaskInputs produces, so workers parse one format).
// String elements substitute inline; structured elements substitute as JSON.
func renderScatterChildBody(template string, idx int, item any) string {
	itemText := ""
	if s, ok := item.(string); ok {
		itemText = s
	} else if b, err := json.Marshal(item); err == nil {
		itemText = string(b)
	}
	body := strings.ReplaceAll(template, "{{item}}", itemText)
	body = strings.ReplaceAll(body, "{{index}}", strconv.Itoa(idx))
	block, err := json.Marshal(scatterItemInput{Item: item, Index: idx})
	if err != nil {
		return body
	}
	return body + "\n\n## Task Inputs (scatter element)\n\n```json\n" + string(block) + "\n```\n"
}

// createScatterChildCard creates one child task card via
// project.wiki_create_task_card. Children are plain worker task cards
// (default worker_task execKind, status todo, no depends_on). A create that
// reports the card already exists is success — re-entry idempotency.
func (a *Actor) createScatterChildCard(ctx actor.PureContext, projectID, mapID, childID, question string) error {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return fmt.Errorf("invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return fmt.Errorf("project actor unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), scatterInvokeTimeout)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.wiki_create_task_card", domain.WikiCreateTaskCardReq{
		MapID:    mapID,
		Title:    childID,
		Question: question,
	})
	if call == nil {
		return fmt.Errorf("wiki_create_task_card invoke returned nil")
	}
	if _, err := call.Final(callCtx); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return err
	}
	return nil
}

// scatterWriteOutputs writes outputs to a card via
// project.wiki_set_task_outputs.
func (a *Actor) scatterWriteOutputs(ctx actor.PureContext, projectID, cardID string, outputs map[string]any) error {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return fmt.Errorf("invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return fmt.Errorf("project actor unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), scatterInvokeTimeout)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.wiki_set_task_outputs", domain.WikiSetTaskOutputsReq{
		CardID:  cardID,
		Outputs: outputs,
	})
	if call == nil {
		return fmt.Errorf("wiki_set_task_outputs invoke returned nil")
	}
	_, err = call.Final(callCtx)
	return err
}

// scatterSetCardStatus updates a card status via project.wiki_set_status.
func (a *Actor) scatterSetCardStatus(ctx actor.PureContext, projectID, cardID, status string) error {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return fmt.Errorf("invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return fmt.Errorf("project actor unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), scatterInvokeTimeout)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.wiki_set_status", gen.WikiSetStatusReq{
		ID:     cardID,
		Status: status,
	})
	if call == nil {
		return fmt.Errorf("wiki_set_status invoke returned nil")
	}
	_, err = call.Final(callCtx)
	return err
}

// fetchScatterCardsBatch reads many card raws in one project round trip via
// project.wiki_get_cards_batch. Per-id misses are reported Found=false
// rather than failing the batch.
func (a *Actor) fetchScatterCardsBatch(ctx actor.PureContext, projectID string, ids []string) ([]domain.WikiCardRaw, error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return nil, fmt.Errorf("invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return nil, fmt.Errorf("project actor unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), scatterInvokeTimeout)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.wiki_get_cards_batch", domain.WikiGetCardsBatchReq{Ids: ids})
	if call == nil {
		return nil, fmt.Errorf("wiki_get_cards_batch invoke returned nil")
	}
	result, err := call.Final(callCtx)
	if err != nil {
		return nil, err
	}
	resp, ok := result.(domain.WikiGetCardsBatchResp)
	if !ok {
		return nil, fmt.Errorf("unexpected wiki_get_cards_batch response %T", result)
	}
	return resp.Cards, nil
}

// isScatterCardMissing reports whether a card read failed because the card
// does not exist (rather than transport/lookup trouble). Matches the stable
// phrasing from project.wiki_get_card / the persist not-exist sentinel.
func isScatterCardMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not found") || strings.Contains(msg, "not exist")
}

// ---------------------------------------------------------------------------
// Fan-out tracking table
// ---------------------------------------------------------------------------

// scatterFanout statuses.
const (
	scatterStatusActive    = "active"    // waiting on children
	scatterStatusJoining   = "joining"   // join pass in flight / resumable
	scatterStatusDone      = "done"      // scatter card flipped done
	scatterStatusFailed    = "failed"    // a child failed (or card flipped)
	scatterStatusCancelled = "cancelled" // a child was cancelled (or card)
)

// scatterFanout is one tracked scatter activation. Persisted with the
// workspace actor state (a.subMapsCardName-style record) so the join sweep
// survives restarts; the cards remain the ground truth and the sweep
// reconciles the table onto them. Items/Template are kept so a missing
// child card can be re-created verbatim (self-heal).
type scatterFanout struct {
	ScatterCardID string   `json:"scatter_card_id"`
	ProjectID     string   `json:"project_id"`
	MapID         string   `json:"map_id"`
	Source        string   `json:"source"`
	Template      string   `json:"template"`
	Items         []any    `json:"items"`
	ChildIDs      []string `json:"child_ids"`
	Status        string   `json:"status"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
}

// upsertScatterFanout inserts or replaces the fan-out record for a scatter
// card (re-activation on idempotent re-entry replaces the prior record,
// preserving the original CreatedAt). Snapshots and persists under the
// writer lock so the persist never runs under it.
func (a *Actor) upsertScatterFanout(f scatterFanout) {
	now := time.Now().UTC().Format(time.RFC3339)
	a.scatterMu.Lock()
	replaced := false
	for i := range a.scatterFanouts {
		if a.scatterFanouts[i].ScatterCardID != f.ScatterCardID {
			continue
		}
		created := a.scatterFanouts[i].CreatedAt
		f.CreatedAt = created
		f.UpdatedAt = now
		a.scatterFanouts[i] = f
		replaced = true
		break
	}
	if !replaced {
		f.UpdatedAt = now
		a.scatterFanouts = append(a.scatterFanouts, f)
	}
	snapshot := make([]scatterFanout, len(a.scatterFanouts))
	copy(snapshot, a.scatterFanouts)
	a.scatterMu.Unlock()
	_ = a.saveScatterFanoutsSnapshot(snapshot)
}

// setScatterFanoutStatus transitions a fan-out record's status and persists
// — but only when the status actually changed, so the sweep's still-waiting
// branch (active→joining, then joining→joining) does not write state every
// tick. Returns false when no record exists.
func (a *Actor) setScatterFanoutStatus(scatterCardID, status string) bool {
	now := time.Now().UTC().Format(time.RFC3339)
	a.scatterMu.Lock()
	found := false
	changed := false
	for i := range a.scatterFanouts {
		if a.scatterFanouts[i].ScatterCardID != scatterCardID {
			continue
		}
		found = true
		if a.scatterFanouts[i].Status != status {
			a.scatterFanouts[i].Status = status
			a.scatterFanouts[i].UpdatedAt = now
			changed = true
		}
		break
	}
	var snapshot []scatterFanout
	if changed {
		snapshot = make([]scatterFanout, len(a.scatterFanouts))
		copy(snapshot, a.scatterFanouts)
	}
	a.scatterMu.Unlock()
	if !found {
		return false
	}
	if changed {
		_ = a.saveScatterFanoutsSnapshot(snapshot)
	}
	return true
}

// dropScatterFanout removes a fan-out record (scatter card deleted).
func (a *Actor) dropScatterFanout(scatterCardID string) {
	a.scatterMu.Lock()
	out := a.scatterFanouts[:0]
	for _, f := range a.scatterFanouts {
		if f.ScatterCardID != scatterCardID {
			out = append(out, f)
		}
	}
	a.scatterFanouts = out
	snapshot := make([]scatterFanout, len(a.scatterFanouts))
	copy(snapshot, a.scatterFanouts)
	a.scatterMu.Unlock()
	_ = a.saveScatterFanoutsSnapshot(snapshot)
}

// ---------------------------------------------------------------------------
// Join sweep
// ---------------------------------------------------------------------------

// scatterSweepReq/scatterSweepResp are the internal callable's wire shapes.
// The payload is always nil (ctx.After passes nil); the response is unused.
type scatterSweepReq struct{}
type scatterSweepResp struct{}

// handleScatterSweep is the periodic join driver. It self-reschedules and
// reconciles every non-terminal fan-out. Runs as a stateless internal
// callable (PureContext), mirroring the deletion sweep's arming pattern.
func (a *Actor) handleScatterSweep(ctx actor.PureContext, _ scatterSweepReq) (scatterSweepResp, error) {
	defer a.scheduleScatterSweepNext(ctx)
	a.reconcileScatterFanouts(ctx)
	return scatterSweepResp{}, nil
}

// startScatterSweep arms the first sweep tick from OnStart. Idempotent via
// the scatterSweepScheduled flag (guarded by scatterMu).
func (a *Actor) startScatterSweep(ctx actor.PureContext) {
	a.scatterMu.Lock()
	if a.scatterSweepScheduled {
		a.scatterMu.Unlock()
		return
	}
	a.scatterSweepScheduled = true
	a.scatterMu.Unlock()
	if err := ctx.After(scatterSweepInterval, "workspace.internal_scatter_sweep", nil); err != nil {
		ctx.Logger().Error("workspace: failed to schedule scatter sweep", "error", err)
		a.scatterMu.Lock()
		a.scatterSweepScheduled = false
		a.scatterMu.Unlock()
	}
}

// scheduleScatterSweepNext re-arms the next sweep tick. A failure here only
// loses one cadence period; the flag stays set so the sweep still believes
// it is scheduled — the next successful re-arm keeps the loop alive, and a
// process restart re-arms from OnStart.
func (a *Actor) scheduleScatterSweepNext(ctx actor.PureContext) {
	if err := ctx.After(scatterSweepInterval, "workspace.internal_scatter_sweep", nil); err != nil {
		ctx.Logger().Warn("workspace: scatter sweep re-schedule failed", "error", err)
	}
}

// reconcileScatterFanouts runs one join pass over every non-terminal
// fan-out record. Snapshotted under the read lock; invokes happen outside.
func (a *Actor) reconcileScatterFanouts(ctx actor.PureContext) {
	a.scatterMu.RLock()
	pending := make([]scatterFanout, 0, len(a.scatterFanouts))
	for _, f := range a.scatterFanouts {
		if f.Status == scatterStatusActive || f.Status == scatterStatusJoining {
			pending = append(pending, f)
		}
	}
	a.scatterMu.RUnlock()
	for _, f := range pending {
		a.reconcileScatterFanout(ctx, f)
	}
}

// reconcileScatterFanout evaluates one fan-out and drives the scatter card
// to its joined terminal state. Idempotent: concurrent passes (the sweep and
// an Execute re-entry) may overlap, but every write is a same-value status
// or outputs write and the terminal transition is guarded by the table.
func (a *Actor) reconcileScatterFanout(ctx actor.PureContext, f scatterFanout) {
	// The scatter card is the ground truth. A missing card or one already
	// in a terminal state converges the table without touching children.
	raw, err := a.fetchCardRaw(ctx, f.ProjectID, f.ScatterCardID)
	if err != nil {
		if isScatterCardMissing(err) {
			a.dropScatterFanout(f.ScatterCardID)
			return
		}
		a.logScatterError(ctx, "fetch scatter card", err)
		return
	}
	card := project.ParseCardRaw(f.ScatterCardID, raw)
	switch card.Status {
	case "done", "failed", "cancelled":
		// Settled externally (manual flip or a prior pass landed).
		a.setScatterFanoutStatus(f.ScatterCardID, card.Status)
		return
	}

	// Read all children in one round trip.
	cards, err := a.fetchScatterCardsBatch(ctx, f.ProjectID, f.ChildIDs)
	if err != nil {
		a.logScatterError(ctx, "fetch scatter children", err)
		return
	}
	byID := make(map[string]domain.WikiCardRaw, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}

	var (
		doneOutputs     = make([]any, 0, len(f.ChildIDs))
		failedChildren  []string
		cancelledCh     []string
		pendingChildren int
	)
	for idx, childID := range f.ChildIDs {
		childRaw, ok := byID[childID]
		if !ok || !childRaw.Found {
			// Self-heal: re-create the missing child verbatim from the
			// recorded element/template. Counted as pending; the join
			// settles on a later pass.
			if idx < len(f.Items) {
				body := renderScatterChildBody(f.Template, idx, f.Items[idx])
				if cerr := a.createScatterChildCard(ctx, f.ProjectID, f.MapID, childID, body); cerr != nil {
					a.logScatterError(ctx, "re-create scatter child "+childID, cerr)
				}
			}
			pendingChildren++
			doneOutputs = append(doneOutputs, map[string]any{})
			continue
		}
		child := project.ParseCardRaw(childID, childRaw.Raw)
		switch child.Status {
		case "done":
			doneOutputs = append(doneOutputs, taskOutputsFromCardData(child.Data["task_outputs"]))
		case "failed":
			failedChildren = append(failedChildren, childID)
			doneOutputs = append(doneOutputs, taskOutputsFromCardData(child.Data["task_outputs"]))
		case "cancelled":
			cancelledCh = append(cancelledCh, childID)
			doneOutputs = append(doneOutputs, taskOutputsFromCardData(child.Data["task_outputs"]))
		default:
			// backlog / todo / doing / pending_review: still running.
			pendingChildren++
			doneOutputs = append(doneOutputs, map[string]any{})
		}
	}

	switch {
	case len(failedChildren) > 0:
		// Partial failure propagates: the first failing child flips the
		// scatter card failed. Remaining children are not cancelled here
		// (v1); the map owner sees the failure via the card tree.
		note := fmt.Sprintf("scatter child task(s) failed: %s", strings.Join(failedChildren, ", "))
		if werr := a.scatterWriteOutputs(ctx, f.ProjectID, f.ScatterCardID, map[string]any{
			"error":           note,
			"failed_children": failedChildren,
		}); werr != nil {
			a.logScatterError(ctx, "write scatter failure outputs", werr)
			return
		}
		if serr := a.scatterSetCardStatus(ctx, f.ProjectID, f.ScatterCardID, "failed"); serr != nil {
			a.logScatterError(ctx, "set scatter card failed", serr)
			return
		}
		a.setScatterFanoutStatus(f.ScatterCardID, scatterStatusFailed)
	case len(cancelledCh) > 0:
		if werr := a.scatterWriteOutputs(ctx, f.ProjectID, f.ScatterCardID, map[string]any{
			"cancelled_children": cancelledCh,
		}); werr != nil {
			a.logScatterError(ctx, "write scatter cancel outputs", werr)
			return
		}
		if serr := a.scatterSetCardStatus(ctx, f.ProjectID, f.ScatterCardID, "cancelled"); serr != nil {
			a.logScatterError(ctx, "set scatter card cancelled", serr)
			return
		}
		a.setScatterFanoutStatus(f.ScatterCardID, scatterStatusCancelled)
	case pendingChildren == 0:
		// Join complete: aggregate every child's outputs in element order.
		if werr := a.scatterWriteOutputs(ctx, f.ProjectID, f.ScatterCardID, map[string]any{
			"items": doneOutputs,
		}); werr != nil {
			a.logScatterError(ctx, "write scatter outputs", werr)
			return
		}
		if serr := a.scatterSetCardStatus(ctx, f.ProjectID, f.ScatterCardID, "done"); serr != nil {
			a.logScatterError(ctx, "set scatter card done", serr)
			return
		}
		a.setScatterFanoutStatus(f.ScatterCardID, scatterStatusDone)
	default:
		// Still waiting on children. Mark joining so an interrupted pass
		// is visibly resumable; the next tick re-evaluates.
		a.setScatterFanoutStatus(f.ScatterCardID, scatterStatusJoining)
	}
}

// logScatterError logs a join/propagation error. The logger is obtained
// from context; if unavailable the error is dropped.
func (a *Actor) logScatterError(ctx actor.PureContext, op string, err error) {
	if ctx == nil || err == nil {
		return
	}
	ctx.Logger().Error("workspace: scatter propagation", "op", op, "error", err)
}
