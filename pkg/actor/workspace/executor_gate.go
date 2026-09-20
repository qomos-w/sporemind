package workspace

import (
	"context"
	"fmt"
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

// Gate executor — workflow approval node. See Executor.Kind / ExecKindGate
// for the executor contract; the semantic contract lives on the type below.
//
// Activation model (gate is the inverse of toolcall: it stops the DAG and
// waits for human/owner consent instead of pushing an effect outward):
//
//   - Preflight validates the gate card's static config (optional prompt
//     text, optional mutating-callable declaration). Failures here leave
//     the card untouched — same "fail before claim" ordering as the other
//     executors.
//   - Execute resolves the project + caller workflow. The execute path
//     branches on the global permission mode:
//       * yolo / autopilot (bypass) → auto-approve: write task_outputs
//         { decision:"auto_approved", reason, mode } + CAS doing→done in
//         one synchronous round trip; the dispatcher's spawn_assign returns
//         normally and the map's frontier advances.
//       * anything else → enqueue a companion-window toast via the global
//         toast actor. The card stays "doing"; workspace.gate_approve /
//         workspace.gate_reject (registered on the workspace actor) flip
//         the terminal state via project.wiki_set_status CAS when the user
//         clicks through. The dispatcher's response mirrors event_wait's
//         (ExecResp.DisplayName set, AgentActorID empty → owner stamping
//         skipped by workspace_workflow.go).
//
// The toast payload mirrors WorkspaceGateApproveReq (TaskCardId +
// optional CallableId) and ships ActionSchemaID =
// WorkspaceGateApproveReqSchemaID so the main window can re-dispatch
// with the right typed args when it sees toast.action_triggered.

const (
	// gateExecutorErrorPrefix is the stable prefix every gate-executor
	// error message carries. Used by callers (and tests) to string-match
	// gate failures from other executor errors.
	gateExecutorErrorPrefix = "workspace.executor.gate:"

	// gateApproveCallID is the toast action target; companion window
	// clicks bubble up via toast.action → main window invokes this
	// callable. Registered in workspace.go's OnStart.
	gateApproveCallID = "workspace.gate_approve"
	gateRejectCallID  = "workspace.gate_reject"

	// gatePermissionModeYolo / Autopilot auto-approve the gate; matches
	// the bypass set the agent's normalizePermissionMode recognizes.
	gatePermissionModeYolo      = "yolo"
	gatePermissionModeAutopilot = "autopilot"

	// gateDefaultPrompt is shown in the toast body when the card author
	// left data.exec.prompt empty.
	gateDefaultPrompt = "Approve this workflow step?"
)

// gateConfig holds the parsed data.exec fields for a gate task card.
type gateConfig struct {
	// Prompt is the toast body the user sees; empty → gateDefaultPrompt.
	Prompt string
	// CallableID, when non-empty, names the mutating callable the gate
	// protects (recorded in task_outputs for audit, never re-invoked by
	// the gate executor itself). Preflight enforces mutating EffectKind.
	CallableID string
	// TimeoutSec is reserved for future "auto-fail after N seconds";
	// intentionally unused in v1 — the gate waits indefinitely and is
	// only resolved by an explicit approve/reject (or a card-level
	// cancel from the owner agent).
	TimeoutSec int
}

// gateExecutor implements the gate execKind. The struct mirrors the
// pattern of executor_toolcall.go: it holds a back-reference to the
// hosting workspace actor (no shared mutable service) so it can resolve
// the project, invoke the toast service, and write back to the bound
// task card.
type gateExecutor struct {
	a *Actor
}

// newGateExecutor wires the executor back to its hosting workspace.
// Idempotent — called from ensureExecRegistry, which itself is guarded
// so multiple OnStart invocations are safe.
func newGateExecutor(a *Actor) *gateExecutor {
	return &gateExecutor{a: a}
}

// Kind returns "gate". Stable; matches ExecKindGate.
func (e *gateExecutor) Kind() ExecKind {
	return ExecKindGate
}

// Preflight validates the gate declaration before the dispatcher claims
// the parent task card. Failures here leave the card untouched.
//
// Required configuration:
//   - none (a gate card with no body is valid: prompt defaults to
//     gateDefaultPrompt, no mutating-callable link to enforce).
//
// Optional:
//   - prompt:  free-form toast body (trimmed).
//   - callable: dotted callable ID; when declared it MUST resolve in
//     the live topology AND its EffectKind MUST be "mutating" — read-only
//     callables do not need a gate. Streaming callables are rejected
//     (no gate-able surface). Agent-local callables (no service routing)
//     are rejected because the gate's audit story requires a service
//     hop.
//
// requireActiveWorkflow is the universal gate the dispatcher itself
// would re-check; we run it here too so a "gate card in a workflow-less
// project" is rejected at Preflight rather than as a confusing runtime
// error mid-Execute.
func (e *gateExecutor) Preflight(ctx actor.PureContext, req ClaimReq) (PreflightResult, error) {
	cfg, err := requireGateConfig(req)
	if err != nil {
		return PreflightResult{}, err
	}
	if cfg.CallableID != "" {
		ci, err := e.a.resolveTopologyCallable(cfg.CallableID)
		if err != nil {
			return PreflightResult{}, fmt.Errorf("%s resolve callable %q: %w", gateExecutorErrorPrefix, cfg.CallableID, err)
		}
		if err := gateValidateCallable(ci); err != nil {
			return PreflightResult{}, fmt.Errorf("%s %w", gateExecutorErrorPrefix, err)
		}
	}
	if err := requireActiveWorkflow(ctx, gateExecutorErrorPrefix, req.CallerAgentID); err != nil {
		return PreflightResult{}, err
	}
	return PreflightResult{
		KindConfig:  domain.AgentKindConfig{Kind: domain.AgentKindWorker, DisplayName: "GateExecutor"},
		DisplayName: "GateExecutor",
		SpawnName:   "gate-" + req.BoundTaskCardID,
	}, nil
}

// Execute activates the gate. See the package-level comment for the
// bypass / toast branching. The card stays "doing" in the toast branch;
// the workspace.gate_approve / workspace.gate_reject handlers do the
// terminal flip.
func (e *gateExecutor) Execute(ctx actor.PureContext, req ClaimReq) (ExecResp, error) {
	return panicprobe.Guard(ctx, "workspace.executor.gate", req, func() (ExecResp, error) {
		if req.Preflight == nil {
			return ExecResp{}, fmt.Errorf("%s Preflight result is required (dispatcher contract)", gateExecutorErrorPrefix)
		}
		cfg, err := requireGateConfig(req)
		if err != nil {
			return ExecResp{}, err
		}
		projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
		if err != nil {
			return ExecResp{}, fmt.Errorf("%s %w", gateExecutorErrorPrefix, err)
		}

		mode := e.a.globalPermissionMode()
		if isGateBypassMode(mode) {
			return e.executeAutoApprove(ctx, projectID, req.BoundTaskCardID, cfg, mode, req.MaxTurns)
		}
		return e.executeArmToast(ctx, projectID, req.BoundTaskCardID, cfg, req.MaxTurns)
	})
}

// executeAutoApprove writes the bypass-mode auto-approval record and
// flips the card to done in the same synchronous trip. A CAS miss (card
// already terminal) is surfaced as a dispatch error so the dispatcher
// can roll back the claim like any other executor failure.
func (e *gateExecutor) executeAutoApprove(ctx actor.PureContext, projectID, cardID string, cfg gateConfig, mode string, maxTurns int32) (ExecResp, error) {
	approvedAt := time.Now().UTC().Format(time.RFC3339Nano)
	outputs := map[string]any{
		"decision":    "auto_approved",
		"approver":    "system",
		"reason":      fmt.Sprintf("permission mode %q bypasses gate", mode),
		"mode":        mode,
		"approved_at": approvedAt,
	}
	if cfg.CallableID != "" {
		outputs["callable_id"] = cfg.CallableID
	}
	if cfg.Prompt != "" {
		outputs["prompt"] = cfg.Prompt
	}
	if err := gateSetTaskOutputs(ctx, projectID, cardID, outputs); err != nil {
		return ExecResp{}, fmt.Errorf("%s write auto-approval outputs: %w", gateExecutorErrorPrefix, err)
	}
	if err := gateSetTaskCardStatus(ctx, projectID, cardID, "done", "doing"); err != nil {
		return ExecResp{}, fmt.Errorf("%s flip auto-approved card to done: %w", gateExecutorErrorPrefix, err)
	}
	return ExecResp{
		DisplayName: "GateExecutor",
		Goal: gen.GoalSummary{
			Condition:       "gate auto-approved by permission mode bypass",
			Status:          "active",
			Confirmed:       true,
			BoundTaskCardID: cardID,
			MaxTurns:        maxTurns,
		},
	}, nil
}

// executeArmToast enqueues the companion-window toast and writes a
// "pending_approval" record to task_outputs. The card stays "doing";
// the actual terminal flip happens in workspace.gate_approve /
// workspace.gate_reject (with an ExpectedStatus=doing CAS so a stale
// pending toast cannot overwrite a cancel).
//
// Toast errors are non-fatal at the executor level: if the toast actor
// is unreachable (rare — toast is a global actor), we still record the
// pending state and let the owner agent retry by canceling or by
// triggering another gate card. Failing the card here would silently
// strand the workflow, which is the worse outcome.
func (e *gateExecutor) executeArmToast(ctx actor.PureContext, projectID, cardID string, cfg gateConfig, maxTurns int32) (ExecResp, error) {
	prompt := cfg.Prompt
	if prompt == "" {
		prompt = gateDefaultPrompt
	}
	actionArgs := map[string]any{"TaskCardId": cardID}
	if cfg.CallableID != "" {
		actionArgs["CallableId"] = cfg.CallableID
	}
	// Default to the Approve button; the toast card also exposes a
	// card-local "Reject" link (UI-handled) which routes to
	// workspace.gate_reject with the same TaskCardId.
	toastID, toastErr := e.showGateToast(ctx, prompt, actionArgs)

	pending := map[string]any{
		"decision":  "pending_approval",
		"armed_at":  time.Now().UTC().Format(time.RFC3339Nano),
		"callable_id": cfg.CallableID,
		"prompt":    prompt,
	}
	if toastID != "" {
		pending["toast_id"] = toastID
	}
	if toastErr != nil {
		// Surface the toast failure in task_outputs so the owner agent
		// (or an inspector) can see why no UI signal was raised; we
		// still leave the card "doing" — the workspace.gate_approve
		// path stays valid for any caller (UI, agent, automation).
		pending["toast_error"] = toastErr.Error()
	}
	if err := gateSetTaskOutputs(ctx, projectID, cardID, pending); err != nil {
		return ExecResp{}, fmt.Errorf("%s write pending-approval outputs: %w", gateExecutorErrorPrefix, err)
	}

	return ExecResp{
		DisplayName: "GateExecutor",
		Goal: gen.GoalSummary{
			Condition:       "gate armed — waiting for workspace.gate_approve / workspace.gate_reject",
			Status:          "active",
			Confirmed:       true,
			BoundTaskCardID: cardID,
			MaxTurns:        maxTurns,
		},
	}, nil
}

// showGateToast invokes toast.show via the toast service. Returns the
// card ID (empty on error) so the caller can record it in task_outputs.
// Failures are returned, not panicked — the gate path is robust against
// a temporarily-unavailable toast actor.
func (e *gateExecutor) showGateToast(ctx actor.PureContext, prompt string, actionArgs map[string]any) (string, error) {
	toastRef, ok := ctx.LookupService("toast")
	if !ok || toastRef == nil {
		return "", fmt.Errorf("toast actor service not available")
	}
	showCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	showCall := toastRef.Invoke(showCtx, "toast.show", gen.ToastShowReq{
		Title:          "Workflow gate",
		Body:           prompt,
		Kind:           "info",
		Source:         "workflow.gate",
		ActionLabel:    "Approve",
		ActionCallable: gateApproveCallID,
		ActionArgs:     actionArgs,
		ActionSchemaID: gen.WorkspaceGateApproveReqSchemaID,
		DurationMs:     0, // persist until dismissed or the user acts
	})
	if showCall == nil {
		return "", fmt.Errorf("toast.show invoke returned nil")
	}
	result, err := showCall.Final(showCtx)
	if err != nil {
		return "", fmt.Errorf("toast.show: %w", err)
	}
	resp, ok := result.(gen.ToastShowResp)
	if !ok {
		return "", fmt.Errorf("toast.show: unexpected response type %T", result)
	}
	return resp.ID, nil
}

// requireGateConfig parses the data.exec fields. Empty prompt and
// missing callable are valid (the gate is usable without either).
func requireGateConfig(req ClaimReq) (gateConfig, error) {
	card := project.ParseCardRaw(req.BoundTaskCardID, req.CardRaw)
	execBlock := project.CardDataMap(card, "exec")
	prompt, _ := execBlock["prompt"].(string)
	callable, _ := execBlock["callable"].(string)
	return gateConfig{
		Prompt:     strings.TrimSpace(prompt),
		CallableID: strings.TrimSpace(callable),
	}, nil
}

// gateValidateCallable enforces the gate-policy on the optional
// mutating callable declaration: it must be mutating, unary, and routed
// (not agent-local). Mirrors gateToolCallEffect in executor_toolcall.go
// but inverted: toolcall rejects anything that is not read-only;
// gate rejects anything that is not mutating.
func gateValidateCallable(ci domain.CallableInterface) error {
	if ci.EffectKind != "mutating" {
		return fmt.Errorf("callable %q has effect %q; gate cards protect mutating callables only", ci.Name, ci.EffectKind)
	}
	if ci.Stream {
		return fmt.Errorf("callable %q is streaming; gate cards support unary callables only", ci.Name)
	}
	if ci.ServiceName == "" {
		return fmt.Errorf("callable %q has no service routing (agent-local callables cannot be gated)", ci.Name)
	}
	return nil
}

// isGateBypassMode reports whether the global permission mode auto-
// approves the gate. The agent's normalizePermissionMode recognizes
// "yolo" and "autopilot" as the "act fully autonomously" set;
// "allow-all" and "auto" still expect user confirmation for plan /
// goal / workflow checkpoints, so they do NOT bypass the gate.
func isGateBypassMode(mode string) bool {
	return mode == gatePermissionModeYolo || mode == gatePermissionModeAutopilot
}

// gateSetTaskOutputs writes outputs to the bound task card via
// project.wiki_set_task_outputs. Mirrors the same helper on
// executor_toolcall.go / executor_crawl.go (intentionally not shared —
// each executor owns its helper so refactors to one do not silently
// affect another).
func gateSetTaskOutputs(ctx actor.PureContext, projectID, cardID string, outputs map[string]any) error {
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

// gateSetTaskCardStatus updates the bound task card status via
// project.wiki_set_status with an ExpectedStatus CAS guard. The guard
// is non-empty so a stale gate handler (the user clicked twice, a
// duplicate dispatch arrived, the owner agent already canceled) cannot
// overwrite the card's live state. A CAS miss surfaces as a
// stable-phrased error that callers can string-match on.
func gateSetTaskCardStatus(ctx actor.PureContext, projectID, cardID, status, expected string) error {
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
		ID:             cardID,
		Status:         status,
		ExpectedStatus: expected,
	})
	if call == nil {
		return fmt.Errorf("project.wiki_set_status invoke returned nil")
	}
	_, err = call.Final(statusCtx)
	return err
}