package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/protocol"
	"github.com/qomos-w/sporemind/pkg/scriptcard"
)

// scriptConfig is the executor-local view of a script card's data.exec block.
// The shape is delegated to pkg/scriptcard.Config so the project-side static
// validator and the workspace-side executor agree byte-for-byte on the
// canonical contract (inputs / outputs / gate / capabilities / budget). The
// embed keeps field access at call sites identical to scriptcard.Config.
type scriptConfig struct {
	scriptcard.Config
}

// scriptExecutor implements the script execKind. The struct mirrors
// the pattern of executor_toolcall.go: it holds a back-reference to
// the hosting workspace actor.
//
// Runtime instances are produced fresh for each Execute (the spore VM
// is not thread-safe; execution state stays per-claim). A previous
// (cardID + sha256(source)) compile cache was removed — the runtime
// is already per-claim so the cache only saved the body re-extraction
// step and provided no measurable benefit; the sha256 hash is kept
// for the script_hash audit field in task_outputs (see sha256Hex).
type scriptExecutor struct {
	a *Actor
}

// newScriptExecutor wires the executor back to its hosting workspace.
// Idempotent — called from ensureExecRegistry.
func newScriptExecutor(a *Actor) *scriptExecutor {
	return &scriptExecutor{a: a}
}

// Kind returns "script". Stable; matches ExecKindScript.
func (e *scriptExecutor) Kind() ExecKind {
	return ExecKindScript
}

// CompileErrorPrefix is the stable prefix every script-executor error
// message carries. Used by callers (and tests) to string-match
// script failures from other executor errors without depending on the
// executor's internal error wrapping.
const scriptErrorPrefix = "workspace.executor.script:"

// scriptDefaultCallableTimeout bounds a single host.invoke round trip.
// Mirrors the toolcall executor's per-call 5s budget — script cards
// stack N invokes inside a single script call, so the per-invoke
// cap protects both the per-call wall clock and the cumulative
// MaxDurationSec envelope.
const scriptDefaultCallableTimeout = 5 * time.Second

// ─────────────────────────────────────────────────────────────────────
// Preflight
// ─────────────────────────────────────────────────────────────────────

// Preflight validates the script declaration before the dispatcher
// claims the parent task card. Failures here leave the card untouched
// (same fail-before-claim ordering as the other executors).
//
// Required configuration from the bound card's data.exec block:
//
//   - capabilities: non-empty list of callable IDs; every entry must
//     resolve in the live topology AND pass the same gate checks as
//     the toolcall executor (EffectKind / streaming / agent-local).
//   - the card body must contain at least one ```spore fenced block
//     whose contents compile cleanly (optional compile probe; a
//     compile failure here becomes a preflight error).
//
// For mutating capabilities, also requires:
//
//   - data.exec.gate_card: ID of an upstream gate card; the gate card
//     must be present in the workflow map, status "done", and its
//     task_outputs.decision must read {approved, auto_approved}.
//   - the workflow map card must list the task card in
//     data.stamped_effects (same TOCTOU as toolcall v2).
//
// data.exec.inputs / outputs / budget are optional; missing values
// are filled with sensible defaults (no inputs, no output validation,
// default 60s duration ceiling).
func (e *scriptExecutor) Preflight(ctx actor.PureContext, req ClaimReq) (PreflightResult, error) {
	cfg, err := requireScriptConfig(req)
	if err != nil {
		return PreflightResult{}, err
	}

	// Extract the script source first. The compile probe runs
	// before the gate check so a missing fence surfaces as a
	// preflight error regardless of whether mutating capabilities
	// are also present (gate-card approval is meaningless for a
	// card with no script to run).
	src, err := extractScriptSource(req)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("%s %w", scriptErrorPrefix, err)
	}
	if strings.TrimSpace(src) == "" {
		return PreflightResult{}, fmt.Errorf("%s script body is empty (the ```spore fenced block must contain the entry function)", scriptErrorPrefix)
	}

	if len(cfg.Capabilities) == 0 {
		return PreflightResult{}, fmt.Errorf("%s data.exec.capabilities must list at least one callable ID (the script has no host.invoke surface to use)", scriptErrorPrefix)
	}

	// Resolve and gate every capability up front (cheap; the
	// topology lookup is in-process and the gate card / map fetch is
	// project-round-tripped). Per-capability failures are aggregated
	// so the card author sees every offender, not just the first.
	if err := e.gateCapabilities(ctx, cfg, req); err != nil {
		return PreflightResult{}, fmt.Errorf("%s %w", scriptErrorPrefix, err)
	}

	// Optional compile probe: build a throwaway Runtime, bind the
	// same host.invoke surface, and LoadSource. A compile failure
	// here is a preflight error (card author sees it on save);
	// runtime errors are deferred to Execute.
	if err := e.probeScriptCompile(src); err != nil {
		return PreflightResult{}, fmt.Errorf("%s compile probe failed: %w", scriptErrorPrefix, err)
	}

	if err := requireActiveWorkflow(ctx, scriptErrorPrefix, req.CallerAgentID); err != nil {
		return PreflightResult{}, err
	}

	return PreflightResult{
		KindConfig:  domain.AgentKindConfig{Kind: domain.AgentKindWorker, DisplayName: "ScriptExecutor"},
		DisplayName: "ScriptExecutor",
		SpawnName:   "script-" + req.BoundTaskCardID,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────
// Execute
// ─────────────────────────────────────────────────────────────────────

// Execute builds a per-claim spore Runtime, binds the host.invoke
// surface (whitelist-enforced and effect-gated at call time), feeds
// the script's input arguments from ClaimReq.Inputs, runs the entry
// function under the budget envelope, validates the result against
// data.exec.outputs (when set), and CASes the bound card to done with
// task_outputs containing both the script return value and the
// script_hash audit field. Failures map to outputs.error + card failed.
//
// The per-claim Runtime is single-shot: constructed for this Execute,
// used once, closed. The compile cache stores the canonical source
// string keyed by (cardID, sha256(source)) so a re-claim with the
// same body can short-circuit the extract + hash path; the live
// Runtime is never shared.
func (e *scriptExecutor) Execute(ctx actor.PureContext, req ClaimReq) (ExecResp, error) {
	return panicprobe.Guard(ctx, scriptErrorPrefix, req, func() (ExecResp, error) {
		if req.Preflight == nil {
			return ExecResp{}, fmt.Errorf("%s Preflight result is required (dispatcher contract)", scriptErrorPrefix)
		}
		cfg, err := requireScriptConfig(req)
		if err != nil {
			return ExecResp{}, err
		}
		if len(cfg.Capabilities) == 0 {
			return ExecResp{}, fmt.Errorf("%s data.exec.capabilities must list at least one callable ID", scriptErrorPrefix)
		}

		// Re-gate capabilities on the execute path. The preflight
		// already passed, but the gate card status and the
		// workflow map stamp may have drifted between preflight
		// and execute (TOCTOU). The check is cheap relative to
		// the VM lifecycle and the v2 contract demands the
		// double check.
		if err := e.gateCapabilities(ctx, cfg, req); err != nil {
			return ExecResp{}, fmt.Errorf("%s %w", scriptErrorPrefix, err)
		}

		projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
		if err != nil {
			return ExecResp{}, fmt.Errorf("%s %w", scriptErrorPrefix, err)
		}

		src, err := extractScriptSource(req)
		if err != nil {
			return ExecResp{}, fmt.Errorf("%s %w", scriptErrorPrefix, err)
		}
		hashHex := sha256Hex(src)

		// Build the host.invoke closure. The closure captures
		// the claim-time whitelist + the resolved callable
		// metadata so per-call re-verification matches the
		// preflight TOCTOU level.
		hostInvoke, err := e.buildHostInvoke(ctx, cfg, req)
		if err != nil {
			return ExecResp{}, fmt.Errorf("%s build host.invoke: %w", scriptErrorPrefix, err)
		}

		rt, err := script.NewRuntime()
		if err != nil {
			return ExecResp{}, fmt.Errorf("%s build runtime: %w", scriptErrorPrefix, err)
		}
		defer func() { _ = rt.Close() }()

		// Bind host.invoke under the "host" namespace. No other
		// host functions, no std imports — the script's only
		// host face is this one entry point.
		if err := rt.BindFunc("host", "invoke", hostInvoke); err != nil {
			return ExecResp{}, fmt.Errorf("%s bind host.invoke: %w", scriptErrorPrefix, err)
		}

		// moduleName is unique per card so re-loads of the same
		// card don't trip ErrRuntimeAlreadyLoaded on the same
		// Runtime (we build a fresh Runtime per claim anyway,
		// but keeping the name card-scoped makes the cache key
		// self-documenting).
		moduleName := "script_" + req.BoundTaskCardID
		if err := rt.LoadSource(moduleName, src); err != nil {
			note := fmt.Sprintf("script compile failed: %v", err)
			_ = e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, map[string]any{"error": note, "script_hash": hashHex})
			if serr := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "blocked"); serr != nil {
				return ExecResp{}, fmt.Errorf("%s set task card failed: %w", scriptErrorPrefix, serr)
			}
			return ExecResp{}, nil
		}

		// Resolve the entry function argument list. Argument
		// resolution happens against ClaimReq.Inputs (the same
		// source the worker_task / toolcall / scatter executors
		// already populate); the script just gets a typed
		// position-aligned slice.
		args, missing := resolveScriptArgs(cfg.Inputs, req.Inputs)
		if len(missing) > 0 {
			sort.Strings(missing)
			note := fmt.Sprintf("script inputs missing from upstream bindings: %s", strings.Join(missing, ", "))
			_ = e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, map[string]any{"error": note, "script_hash": hashHex})
			if serr := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "blocked"); serr != nil {
				return ExecResp{}, fmt.Errorf("%s set task card failed: %w", scriptErrorPrefix, serr)
			}
			return ExecResp{}, nil
		}

		// Run with budget. MaxDuration comes from the per-call
		// budget envelope; MaxHostCalls is the cumulative cap
		// across the script's lifetime; MaxOutputBytes bounds
		// the encoded return value. The actor lifecycle context
		// is propagated so a workflow cancel / actor stop aborts
		// the running script (the spore VM derives its run-time
		// context from this with MaxDuration applied as a timeout).
		callCtx := buildCallContext(ctx, cfg.MaxInstructions, cfg.MaxDurationSec, cfg.MaxHostCalls, cfg.MaxOutputBytes)
		result, err := rt.CallContext(callCtx, "run", args...)
		if err != nil {
			note := fmt.Sprintf("script execution failed: %v", err)
			_ = e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, map[string]any{"error": note, "script_hash": hashHex})
			if serr := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "blocked"); serr != nil {
				return ExecResp{}, fmt.Errorf("%s set task card failed: %w", scriptErrorPrefix, serr)
			}
			return ExecResp{}, nil
		}
		if result.Error != nil {
			note := fmt.Sprintf("script runtime error: %v", result.Error)
			_ = e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, map[string]any{"error": note, "script_hash": hashHex})
			if serr := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "blocked"); serr != nil {
				return ExecResp{}, fmt.Errorf("%s set task card failed: %w", scriptErrorPrefix, serr)
			}
			return ExecResp{}, nil
		}

		// Normalize the return value: scripts may return any
		// scalar / array / map shape. We coerce to JSON-able
		// form so task_outputs can carry it without Go-type
		// leakage to downstream bindings.
		normalized, nerr := normalizeScriptResult(result.Value)
		if nerr != nil {
			note := fmt.Sprintf("script result normalization failed: %v", nerr)
			_ = e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, map[string]any{"error": note, "script_hash": hashHex})
			if serr := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "blocked"); serr != nil {
				return ExecResp{}, fmt.Errorf("%s set task card failed: %w", scriptErrorPrefix, serr)
			}
			return ExecResp{}, nil
		}

		// Optional output validation. When data.exec.outputs
		// names a registered schema, Validate the normalized
		// value against its RequestLayout. The layout uses the
		// same protocol package the toolcall executor uses for
		// input validation, keeping schema drift structurally
		// impossible.
		if cfg.Outputs != "" {
			normalizedMap, ok := normalized.(map[string]any)
			if !ok {
				note := fmt.Sprintf("script returned non-map result but data.exec.outputs=%q requires a map-shaped value (got %T)", cfg.Outputs, normalized)
				_ = e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, map[string]any{"error": note, "script_hash": hashHex})
				if serr := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "blocked"); serr != nil {
					return ExecResp{}, fmt.Errorf("%s set task card failed: %w", scriptErrorPrefix, serr)
				}
				return ExecResp{}, nil
			}
			if err := validateScriptOutput(cfg.Outputs, normalizedMap); err != nil {
				note := fmt.Sprintf("script output validation failed for %q: %v", cfg.Outputs, err)
				_ = e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, map[string]any{"error": note, "script_hash": hashHex})
				if serr := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "blocked"); serr != nil {
					return ExecResp{}, fmt.Errorf("%s set task card failed: %w", scriptErrorPrefix, serr)
				}
				return ExecResp{}, nil
			}
		}

		outputs := map[string]any{
			"result":      normalized,
			"script_hash": hashHex,
		}
		if err := e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, outputs); err != nil {
			return ExecResp{}, fmt.Errorf("%s write task outputs: %w", scriptErrorPrefix, err)
		}
		if err := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "done"); err != nil {
			return ExecResp{}, fmt.Errorf("%s set task card done: %w", scriptErrorPrefix, err)
		}

		return ExecResp{
			DisplayName: "ScriptExecutor",
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

// ─────────────────────────────────────────────────────────────────────
// Capability gate (shared by Preflight and Execute)
// ─────────────────────────────────────────────────────────────────────

// gateCapabilities walks every callable declared in
// data.exec.capabilities and applies the v2 effect policy:
//   - streaming → rejected (no host surface for streamed events)
//   - agent-local (no service routing) → rejected (the script has no
//     way to address the local actor)
//   - irreversible → rejected (v1 disables irreversible effects)
//   - mutating / reversible → requires data.exec.gate_card done AND
//     workflow stamped_effects (same dual check as toolcall v2)
//
// All failures are aggregated into a single error so the card author
// sees every offender at once (vs. fixing one and re-running to find
// the next).
func (e *scriptExecutor) gateCapabilities(ctx actor.PureContext, cfg scriptConfig, req ClaimReq) error {
	mutatingPresent := false
	for _, callID := range cfg.Capabilities {
		ci, err := e.a.resolveTopologyCallable(callID)
		if err != nil {
			return fmt.Errorf("capability %q: %w", callID, err)
		}
		if ci.Stream {
			return fmt.Errorf("capability %q is streaming; script cards admit unary callables only", ci.Name)
		}
		if ci.ServiceName == "" {
			return fmt.Errorf("capability %q is agent-local (no service routing); script cards must use callable IDs that resolve to a service route", ci.Name)
		}
		effect := ci.EffectKind
		if effect == "" {
			effect = domain.EffectNone
		}
		switch effect {
		case domain.EffectNone:
			// read-only: no gate required
		case domain.EffectIrreversible:
			return fmt.Errorf("capability %q has effect %q; script cards admit reversible side effects only in v1 (irreversible external effects are gated off)", ci.Name, effect)
		default:
			// Treat every other EffectKind ("mutating",
			// "reversible", or any future in-between value)
			// as the v2 reversible path.
			mutatingPresent = true
		}
	}
	if !mutatingPresent {
		return nil
	}
	if cfg.GateCard == "" {
		return fmt.Errorf("mutating capability present but data.exec.gate_card is not declared (insert a gate card in the workflow before this script and reference it)")
	}
	if err := e.verifyGateCardApproved(ctx, cfg.GateCard, req); err != nil {
		return fmt.Errorf("gate card %q not approved: %w (resolve the upstream gate card before the script is allowed to run)", cfg.GateCard, err)
	}
	if err := e.verifyMapStampedScript(ctx, cfg, req); err != nil {
		return fmt.Errorf("workflow map stamp missing: %w (the script must be pre-approved via workflow_plan_submit; resubmit the plan with these capabilities listed and await approval)", err)
	}
	return nil
}

// verifyGateCardApproved resolves the upstream gate task card and
// asserts it carries an approval record. Returns nil only when the
// gate card exists in the project, status is "done", and its
// task_outputs.decision is one of {approved, auto_approved}. Mirrors
// toolcallExecutor.verifyGateCardApproved — same TOCTOU water level.
func (e *scriptExecutor) verifyGateCardApproved(ctx actor.PureContext, gateCardID string, req ClaimReq) error {
	projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
	if err != nil {
		return fmt.Errorf("resolve project for gate card %q: %w", gateCardID, err)
	}
	raw, status, err := fetchCardFromProject(ctx, projectID, gateCardID)
	if err != nil {
		return fmt.Errorf("fetch gate card %q: %w", gateCardID, err)
	}
	if status != "done" {
		return fmt.Errorf("gate card %q status is %q (expected \"done\"); the upstream gate must finish before the mutating script can run", gateCardID, status)
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

// verifyMapStampedScript checks that the workflow map card has stamped
// every mutating capability declared by this script card. The stamp is
// written by workflow_plan_submit at approval time (see
// pkg/actor/workspace/executor_workflow_stamp.go). Match is keyed on
// (callable_id, gate_card_id) so a stamp for one capability cannot
// accidentally satisfy another capability's gate.
//
// Security gate v2 contract: each mutating capability MUST carry its
// own matching stamp. A script that declares two mutating callables
// but only one is recorded in the plan approval cannot bypass the
// second gate by piggy-backing on the first's stamp; the resolution
// matches callable-by-callable. Read-only (EffectKind = "none")
// capabilities need no stamp. Irreversible capabilities are rejected
// earlier in gateCapabilities, so they never reach this check.
//
// All failures are aggregated into a single error so the card author
// sees every offender (vs. fixing one and re-running to find the
// next).
func (e *scriptExecutor) verifyMapStampedScript(ctx actor.PureContext, cfg scriptConfig, req ClaimReq) error {
	boundCard := project.ParseCardRaw(req.BoundTaskCardID, req.CardRaw)
	mapID := strings.TrimSpace(boundCard.Parent)
	if mapID == "" {
		return fmt.Errorf("script card %q has no parent map; cannot verify workflow stamp (mutating scripts must live under a workflow map)", req.BoundTaskCardID)
	}

	// First pass: resolve topology for each capability and identify
	// the mutating ones (EffectKind != "none"). Effect resolution
	// matches the gateCapabilities classifier so both functions
	// agree on the same mutating set.
	var mutating []string
	for _, callID := range cfg.Capabilities {
		ci, err := e.a.resolveTopologyCallable(callID)
		if err != nil {
			return fmt.Errorf("resolve capability %q: %w", callID, err)
		}
		effect := ci.EffectKind
		if effect == "" {
			effect = domain.EffectNone
		}
		if effect != domain.EffectNone {
			mutating = append(mutating, callID)
		}
	}
	if len(mutating) == 0 {
		return nil
	}

	projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
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
		return fmt.Errorf("workflow map %q has no data.stamped_effects; mutating scripts must be listed by workflow_plan_submit before they can run", mapID)
	}

	// Second pass: every mutating capability must find a matching
	// stamp entry on its own. Collect failures and report them all
	// at once instead of fail-on-first.
	var missing []string
	for _, callID := range mutating {
		found := false
		for _, entry := range stamped {
			entryCallable, _ := entry["callable_id"].(string)
			if entryCallable != callID {
				continue
			}
			if cfg.GateCard != "" {
				entryGate, _ := entry["gate_card_id"].(string)
				if entryGate != cfg.GateCard {
					continue
				}
			}
			found = true
			break
		}
		if !found {
			missing = append(missing, callID)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("workflow map %q does not stamp mutating capabilities %v for script %q (resubmit the plan so each mutating capability is recorded in data.stamped_effects)", mapID, missing, req.BoundTaskCardID)
}

// ─────────────────────────────────────────────────────────────────────
// host.invoke wiring
// ─────────────────────────────────────────────────────────────────────

// hostInvokeContext is the per-claim state threaded into the host.invoke
// closure. The closure captures:
//   - the claim-time whitelist (cfg.Capabilities)
//   - the resolved callable metadata for each capability (so the runtime
//     doesn't re-walk the topology per call — TOCTOU reads are still
//     cheap because we already have ci.ServiceName / ci.ReqSchemaID)
//   - the workspace actor's PureContext (for project refs + topology)
//
// Lives separately so buildHostInvoke can return a clean
// func(callID, args) (map, error) for BindFunc.
type hostInvokeContext struct {
	executor    *scriptExecutor
	cfg         scriptConfig
	ctx         actor.PureContext
	req         ClaimReq
	projectID   string
}

// buildHostInvoke constructs the host.invoke closure. The closure
// applies the whitelist + effect check at every invocation so a script
// cannot smuggle in a callable it wasn't approved for (the cap map
// captures only claim-time resolved metadata; a script edit between
// claim and invocation cannot widen the gate).
func (e *scriptExecutor) buildHostInvoke(ctx actor.PureContext, cfg scriptConfig, req ClaimReq) (func(string, map[string]any) (map[string]any, error), error) {
	if len(cfg.Capabilities) == 0 {
		return nil, fmt.Errorf("empty capability whitelist")
	}
	projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
	if err != nil {
		return nil, err
	}
	hctx := &hostInvokeContext{
		executor:  e,
		cfg:       cfg,
		ctx:       ctx,
		req:       req,
		projectID: projectID,
	}
	return func(callID string, payload map[string]any) (map[string]any, error) {
		return invokeHostCallable(hctx, callID, payload)
	}, nil
}

// invokeHostCallable performs one host.invoke round trip:
//   - whitelist check (callID ∈ cfg.capabilities; otherwise error)
//   - topology resolution (so we have a route / schema id / effect)
//   - target ref resolution (project.* vs service-name lookup)
//   - 5s per-call timeout (matches toolcall)
//   - payload validation via the callable's request layout (when it
//     declares a ReqSchemaID)
//   - the actual invoke + JSON shape round-trip
//
// The function returns a map[string]any so the script can use the
// result without further decoding (spore VM's map-typed return flow).
func invokeHostCallable(hctx *hostInvokeContext, callID string, payload map[string]any) (map[string]any, error) {
	if !isCapabilityAllowed(hctx.cfg.Capabilities, callID) {
		return nil, fmt.Errorf("host.invoke %q: not in capability whitelist %v", callID, hctx.cfg.Capabilities)
	}
	ci, err := hctx.executor.a.resolveTopologyCallable(callID)
	if err != nil {
		return nil, fmt.Errorf("host.invoke %q: %w", callID, err)
	}
	if ci.Stream {
		return nil, fmt.Errorf("host.invoke %q: streaming callable not supported from script", callID)
	}
	// Defense in depth: even though the whitelist was checked at
	// preflight, an irreversible / no-service callable in the
	// whitelist still has to bounce here. Cheap, never wrong.
	effect := ci.EffectKind
	if effect == "" {
		effect = domain.EffectNone
	}
	if effect == domain.EffectIrreversible {
		return nil, fmt.Errorf("host.invoke %q: irreversible callable refused at runtime", callID)
	}
	if ci.ServiceName == "" {
		return nil, fmt.Errorf("host.invoke %q: agent-local callable has no service route", callID)
	}
	if ci.ReqSchemaID != 0 {
		layout, resolveErr := protocol.ResolveRequestLayout(ci)
		if resolveErr != nil {
			return nil, fmt.Errorf("host.invoke %q: resolve request layout: %w", callID, resolveErr)
		}
		normalized, verrs := layout.Normalize(payload)
		if len(verrs) > 0 {
			return nil, fmt.Errorf("host.invoke %q: argument validation failed: %s", callID, joinValidationErrors(verrs))
		}
		payload = normalized
	}

	targetRef, err := resolveScriptTargetRef(hctx.ctx, ci, hctx.projectID)
	if err != nil {
		return nil, fmt.Errorf("host.invoke %q: %w", callID, err)
	}
	invokeCtx, cancel := context.WithTimeout(hctx.ctx.Lifecycle(), scriptDefaultCallableTimeout)
	defer cancel()
	call := targetRef.Invoke(invokeCtx, callID, payload)
	if call == nil {
		return nil, fmt.Errorf("host.invoke %q: invoke returned nil", callID)
	}
	raw, err := call.Final(invokeCtx)
	if err != nil {
		return nil, fmt.Errorf("host.invoke %q: %w", callID, err)
	}
	return scriptResultToMap(raw)
}

// resolveScriptTargetRef mirrors toolcallExecutor.resolveTargetRef
// but takes the projectID directly (the executor has already resolved
// it once; passing it through avoids a redundant
// resolveWorkflowProjectID round-trip per host.invoke).
func resolveScriptTargetRef(ctx actor.PureContext, ci domain.CallableInterface, projectID string) (ref.Ref, error) {
	if ci.ServiceName == "project" {
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

// isCapabilityAllowed returns true when callID appears in the
// per-card whitelist. Order-independent; allows duplicates (a script
// may declare the same capability twice and the executor does not
// penalise that).
func isCapabilityAllowed(whitelist []string, callID string) bool {
	for _, allowed := range whitelist {
		if allowed == callID {
			return true
		}
	}
	return false
}

// scriptResultToMap coerces an actor invoke result to map[string]any.
// The actor invoke returns the Go-typed payload as-is (a struct, a
// map, or a primitive). JSON round-trip drops Go type identity and
// yields the canonical wire shape the spore script runtime expects.
// Nil results (void / tell-style invokes) decode to an empty map.
func scriptResultToMap(raw any) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode result: %w", err)
	}
	if len(encoded) == 0 {
		return map[string]any{}, nil
	}
	decoded, err := unmarshalScriptResult(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode result: %w", err)
	}
	return decoded, nil
}

// unmarshalScriptResult accepts JSON objects into map[string]any and
// wraps JSON primitives (string/number/bool) into a {"value": ...}
// envelope so the script always sees a map-shaped return.
func unmarshalScriptResult(raw []byte) (map[string]any, error) {
	var probe any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	switch v := probe.(type) {
	case map[string]any:
		return v, nil
	case nil:
		return map[string]any{}, nil
	default:
		return map[string]any{"value": v}, nil
	}
}

// ─────────────────────────────────────────────────────────────────────
// Script source extraction
// ─────────────────────────────────────────────────────────────────────

// extractScriptSource finds the first ```spore (or ```SPORE) fenced
// code block in the card body and returns its trimmed content. An
// empty result is a preflight error (the card author forgot to put
// the script in a fenced block); a block whose language tag is
// anything other than `spore` is ignored so prose ```yaml blocks
// elsewhere in the card body do not accidentally become the script.
//
// Extraction delegates to the shared pkg/scriptcard.ExtractSporeBlock
// so the project-side validator and this executor agree byte-for-byte
// on what counts as the script source (same opener/closer rules, same
// case insensitivity, same indented-fence handling).
//
// Multiple ```spore blocks: the first one wins. Authors who need
// multi-file scripts can use the spore package model instead of the
// single-script surface (out of scope for v1).
func extractScriptSource(req ClaimReq) (string, error) {
	body := extractScriptBody(req)
	if body == "" {
		return "", fmt.Errorf("card body is empty (the script source must be in a ```spore fenced block)")
	}
	src, ok := scriptcard.ExtractSporeBlock(body)
	if !ok {
		return "", fmt.Errorf("no ```spore fenced code block found in card body (wrap the script entry function in a fenced block tagged spore)")
	}
	return src, nil
}

// extractScriptBody returns the frontmatter-stripped card body from
// either the parsed CardRecord or req.Body (the dispatcher injects
// the body alongside the raw frontmatter).
func extractScriptBody(req ClaimReq) string {
	if strings.TrimSpace(req.Body) != "" {
		return req.Body
	}
	card := project.ParseCardRaw(req.BoundTaskCardID, req.CardRaw)
	if card == nil {
		return ""
	}
	return card.Body
}

// probeScriptCompile builds a throwaway Runtime with the same
// host.invoke closure shape and LoadSource the candidate. Compile
// failures surface here so a syntax error is caught at preflight
// instead of failing the card at execute time. The probe does NOT
// run `run` — only the parser/linker is exercised.
func (e *scriptExecutor) probeScriptCompile(src string) error {
	rt, err := script.NewRuntime()
	if err != nil {
		return err
	}
	defer func() { _ = rt.Close() }()
	// Bind a no-op host.invoke — the compile probe never actually
	// runs the script, so any well-typed host call resolves.
	if err := rt.BindFunc("host", "invoke", func(string, map[string]any) (map[string]any, error) {
		return map[string]any{}, nil
	}); err != nil {
		return fmt.Errorf("bind probe host.invoke: %w", err)
	}
	return rt.LoadSource("probe", src)
}

// ─────────────────────────────────────────────────────────────────────
// Config parsing
// ─────────────────────────────────────────────────────────────────────

// requireScriptConfig parses the data.exec fields for a script card
// via the shared pkg/scriptcard package. Used by both Preflight and
// Execute (defence in depth). The scriptcard parser is the canonical
// contract — the project-side validator and the workspace executor
// both delegate to it so a card that passes one side passes the other.
//
// Errors from scriptcard bubble up unchanged (they already carry the
// "data.exec.<field>:" prefix); the caller may add the executor
// error prefix as appropriate.
func requireScriptConfig(req ClaimReq) (scriptConfig, error) {
	card := project.ParseCardRaw(req.BoundTaskCardID, req.CardRaw)
	execBlock := project.CardDataMap(card, "exec")
	if execBlock == nil {
		return scriptConfig{}, fmt.Errorf("data.exec is required (kind: script)")
	}
	cfg, err := scriptcard.ParseConfig(map[string]any{"exec": execBlock})
	if err != nil {
		return scriptConfig{}, err
	}
	return scriptConfig{Config: cfg}, nil
}

// buildCallContext assembles the spore VM CallContext for one script
// invocation. The actor lifecycle context is propagated so a workflow
// cancel / actor stop aborts the running script (the spore VM derives
// its run-time context from this with MaxDuration applied as a timeout).
// This mirrors how toolcall / crawl / sporeapp executors inject
// ctx.Lifecycle() into their invoke paths, so an external cancel signal
// reaches the script.
//
// ctx may be nil (test / pure-function scenarios); the spore VM treats
// a nil Context as background and falls back to MaxDuration alone.
//
// Zero values for the optional budget fields (MaxInstructions /
// MaxHostCalls / MaxOutputBytes) are passed through verbatim — the
// spore VM treats zero as "limit disabled". Only MaxDuration has a
// non-zero default (scriptcard.DefaultMaxDurationSec) so a forgetful
// author still gets a script that terminates.
func buildCallContext(ctx actor.PureContext, maxInstructions, maxDurationSec, maxHostCalls int, maxOutputBytes int) script.CallContext {
	dur := maxDurationSec
	if dur <= 0 {
		dur = scriptcard.DefaultMaxDurationSec
	}
	var lifecycle context.Context
	if ctx != nil {
		lifecycle = ctx.Lifecycle()
	}
	return script.CallContext{
		Context: lifecycle,
		Budget: script.ExecutionBudget{
			MaxInstructions: uint64(maxInstructions),
			MaxDuration:     time.Duration(dur) * time.Second,
			MaxHostCalls:    uint32(maxHostCalls),
			MaxOutputBytes:  uint64(maxOutputBytes),
		},
	}
}

// ─────────────────────────────────────────────────────────────────────
// Script argument resolution
// ─────────────────────────────────────────────────────────────────────

// resolveScriptArgs maps ClaimReq.Inputs onto the script's
// `export fun run(...)` parameters in declaration order. Missing
// keys (the script wants `foo` but ClaimReq.Inputs has no `foo`)
// are reported back so Execute can mark the card failed.
//
// A nil cfg.Inputs (script declares no inputs) is matched to a
// run() with no parameters: any ClaimReq.Inputs keys are ignored.
// This matches the spore VM's positional argument contract — there
// is no kwargs.
func resolveScriptArgs(declared []string, provided map[string]any) ([]any, []string) {
	if len(declared) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(declared))
	var missing []string
	for _, key := range declared {
		if v, ok := provided[key]; ok {
			args = append(args, v)
			continue
		}
		args = append(args, nil)
		missing = append(missing, key)
	}
	return args, missing
}

// ─────────────────────────────────────────────────────────────────────
// Output validation
// ─────────────────────────────────────────────────────────────────────

// validateScriptOutput checks the script's normalized return value
// against the RequestLayout derived from data.exec.outputs (a schema
// name resolved via gen.SchemaIDs). Returns a single joined error
// when any field is missing or type-mismatched.
//
// The validation uses protocol.ResolveRequestLayout + Normalize so
// the same shape drift semantics apply to script outputs as to
// toolcall request payloads. An unknown schema name is a hard
// preflight/execute failure (no silent fallback to "no validation").
func validateScriptOutput(schemaName string, value map[string]any) error {
	schemaID, ok := lookupSchemaIDByName(schemaName)
	if !ok {
		return fmt.Errorf("schema %q is not registered", schemaName)
	}
	layout, err := protocol.ResolveRequestLayout(domain.CallableInterface{
		Name:        schemaName,
		ReqSchemaID: int32(schemaID),
	})
	if err != nil {
		return fmt.Errorf("resolve layout: %w", err)
	}
	errs := layout.Validate(value)
	if len(errs) > 0 {
		return fmt.Errorf("%s", joinValidationErrors(errs))
	}
	return nil
}

// lookupSchemaIDByName is the inverse of gen.SchemaIDs. The registry
// maps schema ID → name; we keep a process-wide reverse map built
// lazily so per-call lookups stay O(1).
var (
	scriptSchemaNameIndexOnce sync.Once
	scriptSchemaNameIndex     map[string]uint64
)

func lookupSchemaIDByName(name string) (uint64, bool) {
	scriptSchemaNameOnceBuild()
	id, ok := scriptSchemaNameIndex[name]
	return id, ok
}

// scriptSchemaNameOnceBuild builds the name → id index. The schema
// registry is process-stable after init, so building it once on
// first use is sufficient. Safe under concurrent callers via Once.
func scriptSchemaNameOnceBuild() {
	scriptSchemaNameIndexOnce.Do(func() {
		scriptSchemaNameIndex = make(map[string]uint64, len(gen.SchemaIDs))
		for id, n := range gen.SchemaIDs {
			scriptSchemaNameIndex[n] = id
		}
	})
}

// normalizeScriptResult round-trips the script return value through
// JSON so task_outputs carries the canonical map[string]any /
// []any / scalar shape regardless of the underlying Go type. A nil
// value (script returned void) becomes an empty map.
func normalizeScriptResult(value any) (any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) == 0 {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	if decoded == nil {
		return map[string]any{}, nil
	}
	return decoded, nil
}

// ─────────────────────────────────────────────────────────────────────
// Script hash (audit field)
// ─────────────────────────────────────────────────────────────────────

// sha256Hex returns the hex-encoded sha256 of the script source. The
// hash is published into task_outputs as the script_hash audit field
// so downstream consumers can detect a card edit between plan time and
// execute time without re-extracting the source. (A concurrent
// compile cache was previously keyed off this hash; it was removed —
// the runtime is per-claim, so the cache had no real benefit.)
func sha256Hex(src string) string {
	sum := sha256.Sum256([]byte(src))
	return hex.EncodeToString(sum[:])
}

// ─────────────────────────────────────────────────────────────────────
// Project round-trip (output / status write-back)
// ─────────────────────────────────────────────────────────────────────

// setTaskOutputs writes outputs to the bound task card via
// project.wiki_set_task_outputs. Same helper as the toolcall
// executor — copied here to keep the executor self-contained (no
// cross-executor coupling).
func (e *scriptExecutor) setTaskOutputs(ctx actor.PureContext, projectID, cardID string, outputs map[string]any) error {
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
func (e *scriptExecutor) setTaskCardStatus(ctx actor.PureContext, projectID, cardID, status string) error {
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