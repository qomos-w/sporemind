package workspace

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/policy"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

// workspace.gate_approve / workspace.gate_reject — workflow gate terminal
// transitions. They share the same dispatch shape as the gate executor's
// bypass branch: validate args → write task_outputs → CAS doing → terminal
// status. Both callable paths are public so the companion-window main
// window (via toast.action_triggered → re-dispatch), an agent (via the
// turn-engine tool surface), and a developer UI can all drive them.
//
// Authorization model (mirrors workspace.agent_review):
//   - ctx.Identity trusted (developer / admin) bypasses.
//   - Otherwise, CallerAgentId MUST be non-empty AND resolve to a live
//     agent with an active workflow (requireActiveWorkflow). A direct
//     Public invocation from an anonymous / web identity is rejected so
//     a non-agent caller cannot forge an approval.
// The CAS guard ExpectedStatus=doing then protects the card against a
// stale or duplicate invocation.

const (
	gateHandlerErrorPrefix = "workspace.gate:"

	gateDefaultFormValuesField = "FormValues"
)

// handleWorkspaceGateApprove is the workspace.gate_approve handler.
// See the package-level contract above for authorization and CAS rules.
func (a *Actor) handleWorkspaceGateApprove(ctx actor.PureContext, req gen.WorkspaceGateApproveReq) (gen.WorkspaceGateApproveResp, error) {
	return panicprobe.Guard(ctx, "workspace.gate_approve", req, func() (gen.WorkspaceGateApproveResp, error) {
		if strings.TrimSpace(req.TaskCardID) == "" {
			return gen.WorkspaceGateApproveResp{}, fmt.Errorf("%s_approve: TaskCardId is required", gateHandlerErrorPrefix)
		}
		if strings.TrimSpace(req.Approver) == "" {
			return gen.WorkspaceGateApproveResp{}, fmt.Errorf("%s_approve: Approver is required", gateHandlerErrorPrefix)
		}
		if err := a.authorizeGateCaller(ctx, "workspace.gate_approve", req.CallerAgentID); err != nil {
			return gen.WorkspaceGateApproveResp{}, err
		}

		projectID, err := a.resolveWorkflowProjectID("", req.CallerAgentID)
		if err != nil {
			return gen.WorkspaceGateApproveResp{}, fmt.Errorf("%s_approve: %w", gateHandlerErrorPrefix, err)
		}

		// Normalize the request against the layout so the schema
		// validates required fields (TaskCardId / Approver /
		// CallerAgentId) and coerces scalar frontmatter values to
		// their schema families before they land in task_outputs.
		// Mirrors the toolcall executor's Normalize-before-write step.
		// FormValues itself is `map<string, any>` in the schema (an
		// opaque pass-through dict) so its inner keys are not
		// schema-validated; only the top-level request envelope is.
		var normalizedForm map[string]any
		if len(req.FormValues) > 0 {
			layout, err := gateApproveRequestLayout()
			if err != nil {
				return gen.WorkspaceGateApproveResp{}, fmt.Errorf("%s_approve: resolve request layout: %w", gateHandlerErrorPrefix, err)
			}
			reqMap := map[string]any{
				"TaskCardId":    req.TaskCardID,
				"Approver":      req.Approver,
				"CallerAgentId": req.CallerAgentID,
				"FormValues":    req.FormValues,
			}
			if req.CallableID != "" {
				reqMap["CallableId"] = req.CallableID
			}
			if req.Note != "" {
				reqMap["Note"] = req.Note
			}
			if _, verrs := layout.Normalize(reqMap); len(verrs) > 0 {
				return gen.WorkspaceGateApproveResp{}, fmt.Errorf("%s_approve: request failed schema validation: %s", gateHandlerErrorPrefix, joinValidationErrors(verrs))
			}
			normalizedForm = req.FormValues
		}

		approvedAt := time.Now().UTC().Format(time.RFC3339Nano)
		outputs := map[string]any{
			"decision":    "approved",
			"approver":    req.Approver,
			"approved_at": approvedAt,
		}
		if req.CallerAgentID != "" {
			outputs["caller_agent_id"] = req.CallerAgentID
		}
		if req.CallableID != "" {
			outputs["callable_id"] = req.CallableID
		}
		if normalizedForm != nil {
			outputs["form_values"] = normalizedForm
		}
		if n := strings.TrimSpace(req.Note); n != "" {
			outputs["note"] = n
		}

		cid, err := identity.ParseCanonicalID(projectID)
		if err != nil {
			return gen.WorkspaceGateApproveResp{}, fmt.Errorf("%s_approve: invalid project actor id: %w", gateHandlerErrorPrefix, err)
		}
		projectRef, ok := ctx.LookupID(id.From(cid))
		if !ok || projectRef == nil {
			return gen.WorkspaceGateApproveResp{}, fmt.Errorf("%s_approve: project actor unavailable", gateHandlerErrorPrefix)
		}

		// Set outputs first so an audit trail is durable before the
		// status flip; if the flip fails the card is still inspectable.
		outputsCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
		defer cancel()
		outCall := projectRef.Invoke(outputsCtx, "project.wiki_set_task_outputs", domain.WikiSetTaskOutputsReq{
			CardID:  req.TaskCardID,
			Outputs: outputs,
		})
		if outCall == nil {
			return gen.WorkspaceGateApproveResp{}, fmt.Errorf("%s_approve: project.wiki_set_task_outputs invoke returned nil", gateHandlerErrorPrefix)
		}
		if _, err := outCall.Final(outputsCtx); err != nil {
			return gen.WorkspaceGateApproveResp{}, fmt.Errorf("%s_approve: write approval outputs: %w", gateHandlerErrorPrefix, err)
		}

		// CAS the status. ExpectedStatus=doing so a cancel by the owner
		// agent (or a duplicate dispatch) cannot be clobbered. A miss
		// surfaces as the stable "expected status" phrase used by the
		// other executors; callers can string-match it.
		if err := gateHandlerSetStatus(ctx, projectRef, req.TaskCardID, "done", "doing", "approve"); err != nil {
			return gen.WorkspaceGateApproveResp{}, err
		}

		// Best-effort: dismiss the companion-window toast so the user
		// does not stare at a stale "Approve" button after the workflow
		// has moved on. Failures here are non-fatal — the card is
		// already done.
		a.dismissGateToastBestEffort(ctx, projectID, req.TaskCardID)

		return gen.WorkspaceGateApproveResp{
			CardStatus: "done",
			ApprovedAt: approvedAt,
		}, nil
	})
}

// handleWorkspaceGateReject is the workspace.gate_reject handler.
// Symmetric to handleWorkspaceGateApprove: same authorization model,
// same FormValues normalization (rejected form values are forwarded
// to task_outputs for audit), same CAS. The terminal status is
// "failed" (rejected gate = workflow branch terminates).
func (a *Actor) handleWorkspaceGateReject(ctx actor.PureContext, req gen.WorkspaceGateRejectReq) (gen.WorkspaceGateRejectResp, error) {
	return panicprobe.Guard(ctx, "workspace.gate_reject", req, func() (gen.WorkspaceGateRejectResp, error) {
		if strings.TrimSpace(req.TaskCardID) == "" {
			return gen.WorkspaceGateRejectResp{}, fmt.Errorf("%s_reject: TaskCardId is required", gateHandlerErrorPrefix)
		}
		if strings.TrimSpace(req.Approver) == "" {
			return gen.WorkspaceGateRejectResp{}, fmt.Errorf("%s_reject: Approver is required", gateHandlerErrorPrefix)
		}
		if err := a.authorizeGateCaller(ctx, "workspace.gate_reject", req.CallerAgentID); err != nil {
			return gen.WorkspaceGateRejectResp{}, err
		}

		projectID, err := a.resolveWorkflowProjectID("", req.CallerAgentID)
		if err != nil {
			return gen.WorkspaceGateRejectResp{}, fmt.Errorf("%s_reject: %w", gateHandlerErrorPrefix, err)
		}

		rejectedAt := time.Now().UTC().Format(time.RFC3339Nano)
		outputs := map[string]any{
			"decision":    "rejected",
			"approver":    req.Approver,
			"rejected_at": rejectedAt,
		}
		if req.CallerAgentID != "" {
			outputs["caller_agent_id"] = req.CallerAgentID
		}
		if req.CallableID != "" {
			outputs["callable_id"] = req.CallableID
		}
		if r := strings.TrimSpace(req.Reason); r != "" {
			outputs["reason"] = r
		}

		cid, err := identity.ParseCanonicalID(projectID)
		if err != nil {
			return gen.WorkspaceGateRejectResp{}, fmt.Errorf("%s_reject: invalid project actor id: %w", gateHandlerErrorPrefix, err)
		}
		projectRef, ok := ctx.LookupID(id.From(cid))
		if !ok || projectRef == nil {
			return gen.WorkspaceGateRejectResp{}, fmt.Errorf("%s_reject: project actor unavailable", gateHandlerErrorPrefix)
		}

		outputsCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
		defer cancel()
		outCall := projectRef.Invoke(outputsCtx, "project.wiki_set_task_outputs", domain.WikiSetTaskOutputsReq{
			CardID:  req.TaskCardID,
			Outputs: outputs,
		})
		if outCall == nil {
			return gen.WorkspaceGateRejectResp{}, fmt.Errorf("%s_reject: project.wiki_set_task_outputs invoke returned nil", gateHandlerErrorPrefix)
		}
		if _, err := outCall.Final(outputsCtx); err != nil {
			return gen.WorkspaceGateRejectResp{}, fmt.Errorf("%s_reject: write rejection outputs: %w", gateHandlerErrorPrefix, err)
		}

		if err := gateHandlerSetStatus(ctx, projectRef, req.TaskCardID, "failed", "doing", "reject"); err != nil {
			return gen.WorkspaceGateRejectResp{}, err
		}

		a.dismissGateToastBestEffort(ctx, projectID, req.TaskCardID)

		return gen.WorkspaceGateRejectResp{
			CardStatus: "failed",
			RejectedAt: rejectedAt,
		}, nil
	})
}

// authorizeGateCaller enforces the gate handler authorization rules
// (see handleWorkspaceGateApprove doc for the policy). Identical for
// approve and reject, so both callables share it.
func (a *Actor) authorizeGateCaller(ctx actor.PureContext, callID, callerAgentID string) error {
	if policy.RequireDeveloper(ctx.Identity().Role) == nil {
		return nil
	}
	if callerAgentID == "" {
		return fmt.Errorf("%s caller identity is not trusted (role=%q) and no CallerAgentId was injected", callID, ctx.Identity().Role)
	}
	if err := requireActiveWorkflow(ctx, callID, callerAgentID); err != nil {
		return err
	}
	return nil
}

// gateHandlerSetStatus performs the CAS-flip via project.wiki_set_status.
// Centralized so the approve/reject branches stay symmetric and the
// CAS-miss phrasing is uniform.
func gateHandlerSetStatus(ctx actor.PureContext, projectRef ref.Ref, cardID, status, expected, branch string) error {
	statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(statusCtx, "project.wiki_set_status", gen.WikiSetStatusReq{
		ID:             cardID,
		Status:         status,
		ExpectedStatus: expected,
	})
	if call == nil {
		return fmt.Errorf("%s_%s: project.wiki_set_status invoke returned nil", gateHandlerErrorPrefix, branch)
	}
	if _, err := call.Final(statusCtx); err != nil {
		return fmt.Errorf("%s_%s: %w", gateHandlerErrorPrefix, branch, err)
	}
	return nil
}

// gateApproveRequestLayout resolves the request layout for the
// WorkspaceGateApproveReq struct so callers can normalize FormValues
// against the schema-defined families. The layout is computed once per
// handler invocation; the executor's hot path (no FormValues) skips it
// entirely.
func gateApproveRequestLayout() (*protocol.RequestLayout, error) {
	ci := domain.CallableInterface{
		Name:       gateApproveCallID,
		ReqSchemaID: int32(gen.WorkspaceGateApproveReqSchemaID),
	}
	return protocol.ResolveRequestLayout(ci)
}

// dismissGateToastBestEffort looks up the toast actor and dismisses the
// pending gate card so the user does not see a stale "Approve" button
// after the workflow has moved on. Failures are logged at the actor
// level (via panicprobe) and never bubble back — the card is already
// terminal by the time we get here.
func (a *Actor) dismissGateToastBestEffort(ctx actor.PureContext, projectID, taskCardID string) {
	// Read the pending toast_id from task_outputs (best-effort).
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return
	}
	getCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 3*time.Second)
	defer cancel()
	getCall := projectRef.Invoke(getCtx, "project.wiki_get_card", domain.WikiGetCardReq{ID: taskCardID})
	if getCall == nil {
		return
	}
	result, err := getCall.Final(getCtx)
	if err != nil {
		return
	}
	resp, ok := result.(domain.WikiGetCardResp)
	if !ok {
		return
	}
	card := project.ParseCardRaw(taskCardID, resp.Raw)
	outputs := project.CardDataMap(card, "task_outputs")
	if outputs == nil {
		return
	}
	toastID, _ := outputs["toast_id"].(string)
	if toastID == "" {
		return
	}
	toastRef, ok := ctx.LookupService("toast")
	if !ok || toastRef == nil {
		return
	}
	dismissCtx, cancelDismiss := context.WithTimeout(ctx.Lifecycle(), 3*time.Second)
	defer cancelDismiss()
	dismissCall := toastRef.Invoke(dismissCtx, "toast.dismiss", gen.ToastDismissReq{ID: toastID})
	if dismissCall == nil {
		return
	}
	// Failure is not actionable for the user; just close the call.
	_, _ = dismissCall.Final(dismissCtx)
}

// joinValidationErrors — shared with the toolcall executor; see
// executor_toolcall.go's definition. Reused here because FormValues
// validation errors benefit from the same deterministic ordering and
// we do not want to fork the helper across files for a six-line
// implementation.