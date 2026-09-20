package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

// toolcallConfig holds the parsed data.exec fields for a toolcall task card.
type toolcallConfig struct {
	Callable string
	// Args are the static arguments fixed at plan time. Upstream
	// task_inputs bindings (ClaimReq.Inputs) are merged on top, so a
	// binding with the same key overrides the static value.
	Args map[string]any
	// TimeoutSec bounds the invoke; clamped to [1, 300], default 30.
	TimeoutSec int
	// GateCardID names the upstream gate task card that must be approved
	// before a mutating callable may run. Empty for read-only callables;
	// required (when the callable's EffectKind is mutating) so the
	// executor can double-check the approval record.
	GateCardID string
}

const (
	toolcallDefaultTimeoutSec = 30
	toolcallMaxTimeoutSec     = 300
)

// toolcallExecutor implements the toolcall execKind. It is a deterministic
// leaf executor: no LLM in the loop. The card declares the callable ID and
// static arguments; the executor resolves the callable's live interface
// metadata (ServiceName routing, EffectKind, request schema) from the
// runtime topology snapshot, validates the merged arguments against the
// protocol request layout, invokes once, and writes the result to the bound
// task card's data.task_outputs.
//
// Effect policy (security gate v2):
//   - EffectKind "none" (unset counts as none) — admitted unconditionally.
//   - EffectKind "mutating" / "reversible" — admitted only when the card
//     declares data.exec.gate_card (an upstream gate task card) AND that
//     gate card has status "done" with a task_outputs.decision in
//     {approved, auto_approved} AND the workflow map card has stamped the
//     task via data.stamped_effects. The dual gate (stamp + approval)
//     covers both the workflow_plan_submit approval flow and the
//     per-card gate toast flow.
//   - EffectKind "irreversible" — always rejected. Irreversible external
//     side effects are a documented v1 exclusion; landing this is out of
//     scope for the security-gate-v2 rollout.
type toolcallExecutor struct {
	a *Actor
}

// newToolCallExecutor wires the executor back to its hosting workspace.
func newToolCallExecutor(a *Actor) *toolcallExecutor {
	return &toolcallExecutor{a: a}
}

// Kind returns "toolcall".
func (e *toolcallExecutor) Kind() ExecKind {
	return ExecKindToolCall
}

// Preflight validates the toolcall declaration before the dispatcher claims
// the parent task card. Failures here leave the card untouched.
//
// Required configuration from the bound card's data.exec block:
//   - callable: dotted callable ID resolvable in the live topology.
//
// For mutating callables, also requires:
//
//   - data.exec.gate_card: ID of an upstream gate card; the gate card must
//     be present in the workflow map, status "done", and its
//     task_outputs.decision must read {approved, auto_approved}.
//
//   - the workflow map card must list the task card in
//     data.stamped_effects (stamp comes from the workflow_plan_submit
//     approval).
//
// Argument payload validation deliberately does NOT run here: upstream
// bindings (ClaimReq.Inputs) are only resolved by the dispatcher after the
// claim, so required fields may legitimately be absent from the static args.
// The merged payload is validated in Execute.
func (e *toolcallExecutor) Preflight(ctx actor.PureContext, req ClaimReq) (PreflightResult, error) {
	cfg, err := requireToolCallConfig(req)
	if err != nil {
		return PreflightResult{}, err
	}

	ci, err := e.a.resolveTopologyCallable(cfg.Callable)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.toolcall: %w", err)
	}
	if err := e.gateToolCallEffect(ctx, ci, cfg, req); err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.toolcall: %w", err)
	}
	if ci.ServiceName == "" {
		return PreflightResult{}, fmt.Errorf("workspace.executor.toolcall: callable %q has no service routing (agent-local callables cannot run as workflow cards)", cfg.Callable)
	}
	if _, err := e.resolveTargetRef(ctx, ci, req); err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.toolcall: %w", err)
	}

	if err := requireActiveWorkflow(ctx, "workspace.executor.toolcall", req.CallerAgentID); err != nil {
		return PreflightResult{}, err
	}

	return PreflightResult{
		KindConfig:  domain.AgentKindConfig{Kind: domain.AgentKindWorker, DisplayName: "ToolCallExecutor"},
		DisplayName: "ToolCallExecutor",
		SpawnName:   "toolcall-" + cfg.Callable,
	}, nil
}

// Execute merges static args with upstream inputs, validates the payload
// against the callable's request layout, invokes the callable once, and maps
// the result back onto the bound task card.
func (e *toolcallExecutor) Execute(ctx actor.PureContext, req ClaimReq) (ExecResp, error) {
	return panicprobe.Guard(ctx, "workspace.executor.toolcall", req, func() (ExecResp, error) {
		if req.Preflight == nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: Preflight result is required (dispatcher contract)")
		}
		cfg, err := requireToolCallConfig(req)
		if err != nil {
			return ExecResp{}, err
		}

		ci, err := e.a.resolveTopologyCallable(cfg.Callable)
		if err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: %w", err)
		}
		if err := e.gateToolCallEffect(ctx, ci, cfg, req); err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: %w", err)
		}

		projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
		if err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: %w", err)
		}

		payload := mergeToolCallArgs(cfg.Args, req.Inputs)
		if ci.ReqSchemaID != 0 {
			layout, resolveErr := protocol.ResolveRequestLayout(ci)
			if resolveErr != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: resolve request layout for %q: %w", cfg.Callable, resolveErr)
			}
			// Normalize (not plain Validate): card frontmatter scalars decode
			// as strings regardless of the author's intent, so numeric and
			// boolean args must be coerced to the layout's families before
			// validation and before the payload crosses the wire.
			normalized, verrs := layout.Normalize(payload)
			if len(verrs) > 0 {
				note := fmt.Sprintf("toolcall %q arguments failed schema validation: %s", cfg.Callable, joinValidationErrors(verrs))
				_ = e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, map[string]any{"error": note})
				if serr := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "blocked"); serr != nil {
					return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: set task card failed: %w", serr)
				}
				return ExecResp{}, nil
			}
			payload = normalized
		}

		targetRef, err := e.resolveTargetRef(ctx, ci, req)
		if err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: %w", err)
		}

		timeout := time.Duration(cfg.TimeoutSec) * time.Second
		if timeout <= 0 {
			timeout = toolcallDefaultTimeoutSec * time.Second
		}
		invokeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), timeout)
		defer cancel()
		call := targetRef.Invoke(invokeCtx, cfg.Callable, payload)
		if call == nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: invoke returned nil")
		}
		result, err := call.Final(invokeCtx)
		if err != nil {
			note := fmt.Sprintf("toolcall %q failed: %v", cfg.Callable, err)
			_ = e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, map[string]any{"error": note})
			if serr := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "blocked"); serr != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: set task card failed: %w", serr)
			}
			return ExecResp{}, nil
		}

		normalized, err := normalizeToolCallResult(result)
		if err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: normalize result for %q: %w", cfg.Callable, err)
		}
		if err := e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, map[string]any{"result": normalized}); err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: write task outputs for %q: %w", cfg.Callable, err)
		}
		if err := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "done"); err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.toolcall: set task card done: %w", err)
		}

		return ExecResp{
			DisplayName: "ToolCallExecutor",
			Goal: gen.GoalSummary{
				Condition:       req.Body,
				Status:          "active",
				Confirmed:       true,
				BoundTaskCardID: req.BoundTaskCardID,
				MaxTurns:        req.MaxTurns,
			},
		}, nil
	})
}

// requireToolCallConfig parses the data.exec block. Static args may supply
// defaults that upstream bindings override at execute time.
func requireToolCallConfig(req ClaimReq) (toolcallConfig, error) {
	card := project.ParseCardRaw(req.BoundTaskCardID, req.CardRaw)
	execBlock := project.CardDataMap(card, "exec")
	callable, _ := execBlock["callable"].(string)
	if strings.TrimSpace(callable) == "" {
		return toolcallConfig{}, fmt.Errorf("workspace.executor.toolcall: data.exec.callable is required")
	}
	args, _ := execBlock["args"].(map[string]any)
	// Card frontmatter scalars decode as strings (parseScalarValue keeps
	// YAML string semantics), so accept both numeric and string forms.
	timeoutSec := 0
	switch raw := execBlock["timeout_s"].(type) {
	case float64:
		timeoutSec = int(raw)
	case int:
		timeoutSec = raw
	case string:
		if parsed, perr := strconv.Atoi(strings.TrimSpace(raw)); perr == nil {
			timeoutSec = parsed
		}
	}
	if timeoutSec != 0 {
		if timeoutSec < 1 {
			timeoutSec = 1
		}
		if timeoutSec > toolcallMaxTimeoutSec {
			timeoutSec = toolcallMaxTimeoutSec
		}
	}
	// data.exec.gate_card names the upstream gate task card that approves a
	// mutating callable. Scalar frontmatter decodes to string; tolerate
	// other scalar shapes too (numbers, bools) for symmetry with the rest
	// of the executor block.
	var gateCard string
	switch raw := execBlock["gate_card"].(type) {
	case string:
		gateCard = raw
	case float64:
		gateCard = strconv.FormatInt(int64(raw), 10)
	case int:
		gateCard = strconv.Itoa(raw)
	}
	return toolcallConfig{
		Callable:   strings.TrimSpace(callable),
		Args:       args,
		TimeoutSec: timeoutSec,
		GateCardID: strings.TrimSpace(gateCard),
	}, nil
}

// gateToolCallEffect enforces the security gate v2 policy for toolcall
// task cards. The function is shared by Preflight (fail-before-claim
// ordering) and Execute (defence-in-depth re-check), so a stale dispatch
// or a card that slipped past the dependency graph still hits the same
// gate. The policy branches on EffectKind:
//
//   - unset / "none" — admitted unconditionally.
//   - "mutating" / "reversible" — admitted iff the card declares
//     data.exec.gate_card AND the gate card is "done" with a
//     task_outputs.decision in {approved, auto_approved} AND the
//     workflow map card lists this task in data.stamped_effects. The
//     two checks are independent: stamp = plan-level approval,
//     gate card approval = per-card approval; both must pass.
//   - "irreversible" — always rejected (v1 disables irreversible
//     external side effects).
//
// Streaming callables are rejected for all effect kinds (no gate-able
// surface; the existing rule survives intact).
//
// Lives on the executor instance so it can reach the workspace actor's
// verification helpers (project resolution, gate / stamp card fetch) and
// stays out of package-level mutable state.
func (e *toolcallExecutor) gateToolCallEffect(ctx actor.PureContext, ci domain.CallableInterface, cfg toolcallConfig, req ClaimReq) error {
	effect := ci.EffectKind
	if effect == "" {
		effect = domain.EffectNone
	}
	if ci.Stream {
		return fmt.Errorf("callable %q is streaming; toolcall cards support unary callables only", ci.Name)
	}
	switch effect {
	case domain.EffectNone:
		return nil
	case domain.EffectIrreversible:
		return fmt.Errorf("callable %q has effect %q; toolcall cards admit reversible side effects only in v1 (irreversible external effects are gated off)", ci.Name, effect)
	default:
		// Treat every other EffectKind ("mutating", "reversible", or any
		// future in-between value) as the v2 reversible path that requires
		// the dual stamp + approval check.
		if cfg.GateCardID == "" {
			return fmt.Errorf("callable %q has effect %q; mutating toolcalls must declare data.exec.gate_card pointing at an upstream gate card (insert a gate card in the workflow before this toolcall and reference it)", ci.Name, effect)
		}
		if err := e.verifyGateCardApproved(ctx, ci, cfg.GateCardID, req); err != nil {
			return fmt.Errorf("callable %q has effect %q; gate card %q not approved: %w (resolve the upstream gate card before the toolcall is allowed to run)", ci.Name, effect, cfg.GateCardID, err)
		}
		if err := e.verifyMapStampedEffect(ctx, ci, cfg, req); err != nil {
			return fmt.Errorf("callable %q has effect %q; workflow map stamp missing: %w (the mutating toolcall must be pre-approved via workflow_plan_submit; resubmit the plan with this callable listed and await approval)", ci.Name, effect, err)
		}
		return nil
	}
}

// verifyGateCardApproved resolves the upstream gate task card via the project
// actor and asserts that it carries an approval record. Returns nil only when
// the gate card exists in the project, status is "done", and its
// task_outputs.decision is one of {approved, auto_approved}. Any failure
// here is a hard preflight / execute error so a stale gate card (e.g. still
// doing, or approved with the wrong decision string) cannot quietly slip
// through the policy.
//
// Best-effort resilience: an I/O error fetching the gate card is surfaced as
// the gate-approval failure (the executor refuses to invoke rather than
// proceed with a missing audit trail). The caller wraps this error with the
// effect / callable context for human-readable diagnostics.
//
// Lives on the executor instance so it can reach the workspace actor's
// resolveWorkflowProjectID directly (no package-level mutable closure).
func (e *toolcallExecutor) verifyGateCardApproved(ctx actor.PureContext, ci domain.CallableInterface, gateCardID string, req ClaimReq) error {
	projectID, err := e.resolveProjectForCard(ctx, req)
	if err != nil {
		return fmt.Errorf("resolve project for gate card %q: %w", gateCardID, err)
	}
	raw, status, err := fetchCardFromProject(ctx, projectID, gateCardID)
	if err != nil {
		return fmt.Errorf("fetch gate card %q: %w", gateCardID, err)
	}
	if status != "done" {
		return fmt.Errorf("gate card %q status is %q (expected \"done\"); the upstream gate must finish before the mutating toolcall can run", gateCardID, status)
	}
	card := project.ParseCardRaw(gateCardID, raw)
	outputs := project.CardDataMap(card, "task_outputs")
	if outputs == nil {
		return fmt.Errorf("gate card %q has no task_outputs; no approval record found", gateCardID)
	}
	decision, _ := outputs["decision"].(string)
	if !isApprovedDecision(decision) {
		return fmt.Errorf("gate card %q task_outputs.decision is %q (expected \"approved\" or \"auto_approved\")", gateCardID, decision)
	}
	return nil
}

// verifyMapStampedEffect checks that the workflow map card has stamped the
// mutating toolcall in data.stamped_effects. The stamp is written by
// workflow_plan_submit at approval time (see pkg/actor/workspace/
// executor_workflow_stamp.go + the agent activation hook). The map card
// itself is identified via the task card's parent: frontmatter, falling back
// to the caller's active workflow map when the parent is missing.
//
// A missing stamp is a hard failure: even if the per-card gate approval is
// in place, the plan-level approval must also list the callable.
//
// Lives on the executor instance (no package-level resolver).
func (e *toolcallExecutor) verifyMapStampedEffect(ctx actor.PureContext, ci domain.CallableInterface, cfg toolcallConfig, req ClaimReq) error {
	boundCard := project.ParseCardRaw(req.BoundTaskCardID, req.CardRaw)
	mapID := strings.TrimSpace(boundCard.Parent)
	if mapID == "" {
		return fmt.Errorf("toolcall card %q has no parent map; cannot verify workflow stamp (mutating toolcalls must live under a workflow map)", req.BoundTaskCardID)
	}
	projectID, err := e.resolveProjectForCard(ctx, req)
	if err != nil {
		return fmt.Errorf("resolve project for map %q: %w", mapID, err)
	}
	raw, _, err := fetchCardFromProject(ctx, projectID, mapID)
	if err != nil {
		return fmt.Errorf("fetch workflow map card %q: %w", mapID, err)
	}
	mapCard := project.ParseCardRaw(mapID, raw)
	stamped := stampedEffectsForCard(mapCard.Data)
	if stamped == nil {
		return fmt.Errorf("workflow map %q has no data.stamped_effects; mutating toolcalls must be listed by workflow_plan_submit before they can run", mapID)
	}
	// Match by callable ID and (when set) gate card ID so a single toolcall
	// card mapped to a different callable / gate cannot accidentally inherit
	// another card's stamp.
	for _, entry := range stamped {
		entryCallable, _ := entry["callable_id"].(string)
		if entryCallable != cfg.Callable {
			continue
		}
		if cfg.GateCardID != "" {
			entryGate, _ := entry["gate_card_id"].(string)
			if entryGate != cfg.GateCardID {
				continue
			}
		}
		return nil
	}
	return fmt.Errorf("workflow map %q does not stamp toolcall %q for callable %q; resubmit the plan to record this mutating call in the approval", mapID, req.BoundTaskCardID, cfg.Callable)
}

// resolveProjectForCard is the common project resolution path used by both
// gate-card and map-stamp verification. Lives on the executor instance so
// each workspace actor has its own resolver (no package-level mutable
// closure, no cross-instance clobbering when several workspace actors run
// in the same process).
func (e *toolcallExecutor) resolveProjectForCard(ctx actor.PureContext, req ClaimReq) (string, error) {
	return e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
}

// stampedEffectsForCard extracts the data.stamped_effects list from a
// workflow map card. Returns nil when the field is absent or shaped
// incorrectly (treated as "not stamped" by the caller).
//
// stamped_effects is encoded in frontmatter as a JSON-string value (same
// shape as task_outputs); the data parser hands it back as a string.
// Tolerant to already-decoded lists for callers that read it through a
// richer schema path.
func stampedEffectsForCard(data map[string]any) []map[string]any {
	if data == nil {
		return nil
	}
	raw, ok := data["stamped_effects"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil
		}
		var out []map[string]any
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil
		}
		return out
	}
	return nil
}

// isApprovedDecision matches the decision strings written by both
// workspace.gate_approve ("approved") and the gate executor's
// bypass-mode auto-approve path ("auto_approved"). Anything else (including
// "rejected", "pending", or empty) fails the check.
func isApprovedDecision(decision string) bool {
	return decision == "approved" || decision == "auto_approved"
}

// fetchCardFromProject invokes project.wiki_get_card with a 5s timeout. The
// timeout is generous enough for fs-backed projects on slow machines but
// bounded so a wedged project actor cannot stall the toolcall executor.
// Returns the raw markdown and the canonical card status separately so the
// caller does not need to reparse status from the frontmatter.
func fetchCardFromProject(ctx actor.PureContext, projectID, cardID string) (raw string, status string, err error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return "", "", fmt.Errorf("invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return "", "", fmt.Errorf("project actor unavailable")
	}
	getCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(getCtx, "project.wiki_get_card", domain.WikiGetCardReq{ID: cardID})
	if call == nil {
		return "", "", fmt.Errorf("project.wiki_get_card invoke returned nil")
	}
	result, err := call.Final(getCtx)
	if err != nil {
		return "", "", err
	}
	resp, ok := result.(domain.WikiGetCardResp)
	if !ok {
		return "", "", fmt.Errorf("project.wiki_get_card returned unexpected type %T", result)
	}
	// Status is parsed from the frontmatter so the gate check does not need
	// a separate project.wiki_get_status call (saves one round trip and
	// keeps the verification atomic with the outputs read below).
	card := project.ParseCardRaw(cardID, resp.Raw)
	return resp.Raw, card.Status, nil
}
func (a *Actor) resolveTopologyCallable(callableID string) (domain.CallableInterface, error) {
	if a.topo == nil {
		return domain.CallableInterface{}, fmt.Errorf("topology provider unavailable")
	}
	for _, node := range a.topo.Snapshot() {
		for _, ci := range node.Callables {
			if ci.Name == callableID {
				return ci, nil
			}
		}
	}
	return domain.CallableInterface{}, fmt.Errorf("callable %q not found in topology", callableID)
}

// resolveTargetRef maps the callable's service routing to an invoke target.
// project.* callables route to the workflow's project actor via canonical ID
// (a workspace hosts multiple projects, so service-name lookup is ambiguous);
// everything else resolves by service name.
func (e *toolcallExecutor) resolveTargetRef(ctx actor.PureContext, ci domain.CallableInterface, req ClaimReq) (ref.Ref, error) {
	if ci.ServiceName == "project" {
		projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
		if err != nil {
			return nil, fmt.Errorf("resolve project for %q: %w", ci.Name, err)
		}
		cid, err := identity.ParseCanonicalID(projectID)
		if err != nil {
			return nil, fmt.Errorf("invalid project actor id: %w", err)
		}
		projectRef, ok := ctx.LookupID(id.From(cid))
		if !ok || projectRef == nil {
			return nil, fmt.Errorf("project actor unavailable")
		}
		return projectRef, nil
	}
	targetRef, ok := ctx.LookupService(ci.ServiceName)
	if !ok || targetRef == nil {
		return nil, fmt.Errorf("service %q not available", ci.ServiceName)
	}
	return targetRef, nil
}

// mergeToolCallArgs overlays upstream bindings on static args. Binding values
// win: a static value is a plan-time default, the binding is the runtime
// data flow.
func mergeToolCallArgs(static map[string]any, inputs map[string]any) map[string]any {
	merged := make(map[string]any, len(static)+len(inputs))
	for k, v := range static {
		merged[k] = v
	}
	for k, v := range inputs {
		merged[k] = v
	}
	return merged
}

// joinValidationErrors renders layout validation errors deterministically.
func joinValidationErrors(errs []error) string {
	msgs := make([]string, 0, len(errs))
	for _, err := range errs {
		msgs = append(msgs, err.Error())
	}
	sort.Strings(msgs)
	return strings.Join(msgs, "; ")
}

// normalizeToolCallResult round-trips an arbitrary typed response through
// JSON so task_outputs carry plain maps/slices/scalars that downstream
// bindings and the card view can consume without knowing the Go type.
func normalizeToolCallResult(result any) (any, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// setTaskOutputs writes outputs to the bound task card via
// project.wiki_set_task_outputs.
func (e *toolcallExecutor) setTaskOutputs(ctx actor.PureContext, projectID, cardID string, outputs map[string]any) error {
	if projectID == "" || cardID == "" {
		return fmt.Errorf("projectID and cardID are required")
	}
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return err
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return fmt.Errorf("project actor unavailable")
	}
	outputsCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(outputsCtx, "project.wiki_set_task_outputs", domain.WikiSetTaskOutputsReq{
		CardID:  cardID,
		Outputs: outputs,
	})
	if call == nil {
		return fmt.Errorf("project.wiki_set_task_outputs invoke returned nil")
	}
	_, err = call.Final(outputsCtx)
	return err
}

// setTaskCardStatus updates the bound task card status via
// project.wiki_set_status.
func (e *toolcallExecutor) setTaskCardStatus(ctx actor.PureContext, projectID, cardID, status string) error {
	if projectID == "" || cardID == "" {
		return fmt.Errorf("projectID and cardID are required")
	}
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return err
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return fmt.Errorf("project actor unavailable")
	}
	statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(statusCtx, "project.wiki_set_status", gen.WikiSetStatusReq{
		ID:     cardID,
		Status: status,
	})
	if call == nil {
		return fmt.Errorf("project.wiki_set_status invoke returned nil")
	}
	_, err = call.Final(statusCtx)
	return err
}
