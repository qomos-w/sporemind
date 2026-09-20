package workspace

import (
	"context"
	"encoding/json"
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

// subMapExecutor implements the sub_map execKind: a nested workflow node that
// instantiates a template map, binds it to an independent owner agent, and
// maps the sub-map's lifecycle back onto the parent task card.
//
// Responsibilities:
//   - map-level I/O contract: sub-map task outputs are merged and promoted to
//     the parent task card's data.task_outputs block when the sub-map succeeds.
//   - instantiation isolation: every activation creates a fresh instance map
//     via project.wiki_template_instantiate with a unique id prefix.
//   - independent owner: the sub-map has its own owner agent; the parent owner
//     never runs the nested tasks directly.
//   - state mapping: sub-map success → parent task done; sub-map failure →
//     parent task failed (and owner is torn down); sub-map cancellation →
//     parent task cancelled.
//   - cancellation propagation: terminating a parent sub_map task terminates
//     the sub-map owner and its children.
//   - template reference cycle detection: Preflight rejects templates whose
//     transitive sub_map references would form a cycle.
//
// Fan-out (one sub_map task spawning multiple instances from a list input) is
// intentionally out of scope; this executor only creates a single instance.
type subMapExecutor struct {
	a *Actor
}

// newSubMapExecutor wires the executor back to its hosting workspace.
func newSubMapExecutor(a *Actor) *subMapExecutor {
	return &subMapExecutor{a: a}
}

// Kind returns "sub_map".
func (e *subMapExecutor) Kind() ExecKind {
	return ExecKindSubMap
}

// Preflight validates the sub_map declaration before the dispatcher claims the
// parent task card. Failures here leave the card untouched.
//
// Required configuration from the bound card's data.exec block:
//   - template_map_id: id of a workflow map card with data.template:true.
//   - inputs (optional): map of instantiation parameters.
func (e *subMapExecutor) Preflight(ctx actor.PureContext, req ClaimReq) (PreflightResult, error) {
	cfg, err := e.requireSubMapConfig(req)
	if err != nil {
		return PreflightResult{}, err
	}

	projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.sub_map: %w", err)
	}

	templateCard, err := e.a.fetchCardRaw(ctx, projectID, cfg.TemplateMapID)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.sub_map: fetch template %q: %w", cfg.TemplateMapID, err)
	}
	card := project.ParseCardRaw(cfg.TemplateMapID, templateCard)
	if !project.CardDataBool(card, "template") {
		return PreflightResult{}, fmt.Errorf("workspace.executor.sub_map: card %q is not a template (data.template must be true)", cfg.TemplateMapID)
	}

	if err := requireActiveWorkflow(ctx, "workspace.executor.sub_map", req.CallerAgentID); err != nil {
		return PreflightResult{}, err
	}

	resolvedSlots, err := invokeResolveChildSlot(ctx, "workspace.executor.sub_map", req.CallerAgentID, req.AgentKind, req.Unit)
	if err != nil {
		return PreflightResult{}, err
	}

	if err := e.detectTemplateCycle(ctx, projectID, cfg.TemplateMapID, map[string]struct{}{}); err != nil {
		return PreflightResult{}, err
	}

	ownerName, err := e.a.uniqueSubMapOwnerName(projectID, cfg.TemplateMapID)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.sub_map: allocate owner name: %w", err)
	}
	return PreflightResult{
		KindConfig:    domain.AgentKindConfig{Kind: domain.AgentKindWorker, DisplayName: "SubMapOwner"},
		DisplayName:   ownerName,
		SpawnName:     ownerName,
		ResolvedSlot:  resolvedSlots.Slot,
		FastSlot:      resolvedSlots.Fast,
		ExecutionSlot: resolvedSlots.Execution,
		ReviewSlot:    resolvedSlots.Review,
		SummarySlot:   resolvedSlots.Summary,
	}, nil
}

// Execute instantiates the template map, spawns an independent owner agent for
// the instance, and records the sub-map instance in workspace state. The
// parent task card remains "doing" while the sub-map runs; completion is
// handled asynchronously by the workspace status-update path.
func (e *subMapExecutor) Execute(ctx actor.PureContext, req ClaimReq) (ExecResp, error) {
	return panicprobe.Guard(ctx, "workspace.executor.sub_map", req, func() (ExecResp, error) {
		if req.Preflight == nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.sub_map: Preflight result is required (dispatcher contract)")
		}
		cfg, err := e.requireSubMapConfig(req)
		if err != nil {
			return ExecResp{}, err
		}

		projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
		if err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.sub_map: %w", err)
		}

		instanceMapID := e.a.uniqueSubMapInstanceID(req.BoundTaskCardID)
		if _, err := e.a.instantiateSubMapTemplate(ctx, projectID, cfg.TemplateMapID, instanceMapID, cfg.Inputs); err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.sub_map: instantiate template %q: %w", cfg.TemplateMapID, err)
		}

		pf := req.Preflight
		ownerAgent, err := e.a.spawnSubMapOwner(ctx, projectID, req.CallerAgentID, pf.DisplayName, *pf, instanceMapID)
		if err != nil {
			e.a.deleteSubMapInstanceCards(ctx, projectID, instanceMapID)
			return ExecResp{}, fmt.Errorf("workspace.executor.sub_map: spawn owner for %q: %w", instanceMapID, err)
		}

		e.a.recordSubMapInstance(subMapInstance{
			ParentTaskCardID:  req.BoundTaskCardID,
			ParentAgentID:     req.CallerAgentID,
			InstanceMapID:     instanceMapID,
			OwnerAgentActorID: ownerAgent.ActorID,
			ProjectID:         projectID,
			TemplateMapID:     cfg.TemplateMapID,
			Status:            "active",
			CreatedAt:         time.Now().UTC().Format(time.RFC3339),
		})

		return ExecResp{
			AgentActorID: ownerAgent.ActorID,
			DisplayName:  ownerAgent.DisplayName,
			Goal: gen.GoalSummary{
				Condition:       fmt.Sprintf("Own and run nested workflow map %s (instance of template %s)", instanceMapID, cfg.TemplateMapID),
				Status:          "active",
				Confirmed:       true,
				BoundTaskCardID: req.BoundTaskCardID,
				MaxTurns:        req.MaxTurns,
			},
		}, nil
	})
}

// subMapConfig holds the parsed sub_map execution declaration.
type subMapConfig struct {
	TemplateMapID string
	Inputs        map[string]any
}

// requireSubMapConfig parses the required data.exec fields from the claimed
// card raw. It is used by both Preflight and Execute.
func (e *subMapExecutor) requireSubMapConfig(req ClaimReq) (subMapConfig, error) {
	card := project.ParseCardRaw(req.BoundTaskCardID, req.CardRaw)
	execBlock := project.CardDataMap(card, "exec")
	templateMapID, _ := execBlock["template_map_id"].(string)
	if templateMapID == "" {
		return subMapConfig{}, fmt.Errorf("workspace.executor.sub_map: data.exec.template_map_id is required")
	}
	inputs, _ := execBlock["inputs"].(map[string]any)
	return subMapConfig{TemplateMapID: templateMapID, Inputs: inputs}, nil
}

// detectTemplateCycle detects direct/indirect template self-reference. It
// walks the template graph and follows any sub_map task's template_map_id
// recursively. If the chain reaches a template that is already being visited,
// a cycle is reported.
func (e *subMapExecutor) detectTemplateCycle(ctx actor.PureContext, projectID, templateMapID string, visited map[string]struct{}) error {
	if _, seen := visited[templateMapID]; seen {
		return fmt.Errorf("workspace.executor.sub_map: template reference cycle detected at %q", templateMapID)
	}
	visited[templateMapID] = struct{}{}
	defer delete(visited, templateMapID)

	raw, err := e.a.fetchCardRaw(ctx, projectID, templateMapID)
	if err != nil {
		return nil // missing card fails later; not a cycle here
	}
	card := project.ParseCardRaw(templateMapID, raw)
	if !project.CardDataBool(card, "template") {
		return nil // only follow templates
	}

	graph := e.a.fetchWorkflowTopo(ctx, projectID, templateMapID)
	for _, node := range graph.Nodes {
		nodeRaw, err := e.a.fetchCardRaw(ctx, projectID, node.ID)
		if err != nil {
			continue
		}
		nodeCard := project.ParseCardRaw(node.ID, nodeRaw)
		execBlock := project.CardDataMap(nodeCard, "exec")
		if execBlock["kind"] != ExecKindSubMap {
			continue
		}
		nextTemplate, _ := execBlock["template_map_id"].(string)
		if nextTemplate == "" {
			continue
		}
		if err := e.detectTemplateCycle(ctx, projectID, nextTemplate, visited); err != nil {
			return err
		}
	}
	return nil
}

// instantiateSubMapTemplate invokes project.wiki_template_instantiate and
// returns the response.
func (a *Actor) instantiateSubMapTemplate(ctx actor.PureContext, projectID, templateMapID, instanceMapID string, inputs map[string]any) (domain.WikiTemplateInstantiateResp, error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return domain.WikiTemplateInstantiateResp{}, fmt.Errorf("invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return domain.WikiTemplateInstantiateResp{}, fmt.Errorf("project actor unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 15*time.Second)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.wiki_template_instantiate", domain.WikiTemplateInstantiateReq{
		TemplateMapID: templateMapID,
		InstanceMapID: instanceMapID,
		Inputs:        inputs,
		Source:        "workspace",
	})
	if call == nil {
		return domain.WikiTemplateInstantiateResp{}, fmt.Errorf("template_instantiate invoke returned nil")
	}
	result, err := call.Final(callCtx)
	if err != nil {
		return domain.WikiTemplateInstantiateResp{}, err
	}
	resp, ok := result.(domain.WikiTemplateInstantiateResp)
	if !ok {
		return domain.WikiTemplateInstantiateResp{}, fmt.Errorf("unexpected template_instantiate response %T", result)
	}
	return resp, nil
}

// spawnSubMapOwner spawns a fresh agent, makes it the owner of the instance map
// by calling agent.workflow_start, and returns the AgentRef. The preflight's
// resolved slots are copied to the owner so it inherits the parent agent's
// model configuration; empty ([auto]) slots stay nil.
func (a *Actor) spawnSubMapOwner(ctx actor.PureContext, projectID, parentAgentID, displayName string, pf PreflightResult, instanceMapID string) (domain.AgentRef, error) {
	spawnName := displayName
	if spawnName == "" {
		var err error
		spawnName, err = a.uniqueAgentSpawnName(projectID, "SubMapOwner")
		if err != nil {
			return domain.AgentRef{}, fmt.Errorf("allocate owner name: %w", err)
		}
	}
	var primarySlot *domain.ModelSlot
	if len(pf.ResolvedSlot.Candidates) > 0 {
		rs := pf.ResolvedSlot
		primarySlot = &rs
	}
	owner, err := a.spawnAgentViaProject(ctx, projectID, spawnName, domain.AgentKindWorker, displayName, primarySlot, spawnSlotPtr(pf.FastSlot), spawnSlotPtr(pf.ExecutionSlot), spawnSlotPtr(pf.ReviewSlot), spawnSlotPtr(pf.SummarySlot), "", "", "", nil, nil, parentAgentID, a.globalPermissionMode())
	if err != nil {
		return domain.AgentRef{}, fmt.Errorf("spawn owner: %w", err)
	}
	owner.Title = instanceMapID

	cid, err := identity.ParseCanonicalID(owner.ActorID)
	if err != nil {
		return domain.AgentRef{}, fmt.Errorf("invalid owner actor id: %w", err)
	}
	ownerRef, ok := ctx.LookupID(id.From(cid))
	if !ok || ownerRef == nil {
		return domain.AgentRef{}, fmt.Errorf("owner actor not available")
	}
	startCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	startCall := ownerRef.Invoke(startCtx, "workflow_start", domain.AgentWorkflowStartReq{MapCardID: instanceMapID})
	if startCall == nil {
		return domain.AgentRef{}, fmt.Errorf("workflow_start invoke returned nil")
	}
	if _, err := startCall.Final(startCtx); err != nil {
		return domain.AgentRef{}, fmt.Errorf("workflow_start failed: %w", err)
	}

	// Record the owner in the workspace agent list so it appears in lists and
	// can be terminated/cancelled.
	ag := owner
	ag.Mode = &gen.AgentModeState{BoundTaskCardID: instanceMapID}
	a.agentsMu.Lock()
	a.Agents = append(a.Agents, ag)
	a.agentsMu.Unlock()
	_ = a.saveAgentRegistry()
	a.emitAgentsChanged(ctx)

	return ag, nil
}

// uniqueSubMapOwnerName generates a unique owner display/spawn name for the
// given template in the given project.
func (a *Actor) uniqueSubMapOwnerName(projectID, templateMapID string) (string, error) {
	base := "SubMapOwner-" + strings.ReplaceAll(templateMapID, ":", "-")
	return a.uniqueAgentSpawnName(projectID, base)
}

// uniqueSubMapInstanceID generates a unique instance map id derived from the
// parent task card id.
func (a *Actor) uniqueSubMapInstanceID(parentTaskCardID string) string {
	return parentTaskCardID + "::instance-" + time.Now().UTC().Format("20060102-150405.000000000")
}

// deleteSubMapInstanceCards best-effort deletes the cards created by a failed
// instantiation. The project actor's template_instantiate rollback already
// handles most cases; this is a last-resort cleanup.
func (a *Actor) deleteSubMapInstanceCards(ctx actor.PureContext, projectID, instanceMapID string) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.wiki_delete_card", domain.WikiDeleteCardReq{ID: instanceMapID})
	if call != nil {
		_ = call.Close()
	}
}

// fetchCardRaw reads a card raw via project.wiki_get_card.
func (a *Actor) fetchCardRaw(ctx actor.PureContext, projectID, cardID string) (string, error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return "", fmt.Errorf("invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return "", fmt.Errorf("project actor unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.wiki_get_card", domain.WikiGetCardReq{ID: cardID})
	if call == nil {
		return "", fmt.Errorf("wiki_get_card invoke returned nil")
	}
	result, err := call.Final(callCtx)
	if err != nil {
		return "", err
	}
	resp, ok := result.(domain.WikiGetCardResp)
	if !ok {
		return "", fmt.Errorf("unexpected wiki_get_card response %T", result)
	}
	return resp.Raw, nil
}

// fetchWorkflowTopo reads the workflow_topo graph for a map via the project
// actor's graph snapshot. If graph snapshots are not yet exposed, it falls back
// to an empty graph.
func (a *Actor) fetchWorkflowTopo(ctx actor.PureContext, projectID, mapID string) project.WorkflowTopoGraph {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return project.WorkflowTopoGraph{}
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return project.WorkflowTopoGraph{}
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.graph_get", gen.ProjectGraphGetReq{GraphKind: "workflow_topo", ID: mapID})
	if call == nil {
		return project.WorkflowTopoGraph{}
	}
	result, err := call.Final(callCtx)
	if err != nil {
		return project.WorkflowTopoGraph{}
	}
	resp, ok := result.(gen.ProjectGraphEnvelopeResp)
	if !ok {
		return project.WorkflowTopoGraph{}
	}
	var g project.WorkflowTopoGraph
	_ = json.Unmarshal([]byte(resp.EnvelopeText), &g)
	return g
}

// recordSubMapInstance adds a sub-map instance to the workspace's tracking
// table under subMapMu.
func (a *Actor) recordSubMapInstance(inst subMapInstance) {
	a.subMapMu.Lock()
	a.subMapInstances = append(a.subMapInstances, inst)
	// Snapshot under the writer lock so the persistence includes our write.
	snapshot := make([]subMapInstance, len(a.subMapInstances))
	copy(snapshot, a.subMapInstances)
	a.subMapMu.Unlock()
	_ = a.saveSubMapInstancesSnapshot(snapshot)
}

// subMapInstanceByOwner returns the active sub-map instance owned by the given
// agent actor id, or nil if none.
func (a *Actor) subMapInstanceByOwner(ownerAgentActorID string) *subMapInstance {
	a.subMapMu.RLock()
	defer a.subMapMu.RUnlock()
	for i := range a.subMapInstances {
		if a.subMapInstances[i].OwnerAgentActorID == ownerAgentActorID && a.subMapInstances[i].Status == "active" {
			inst := a.subMapInstances[i]
			return &inst
		}
	}
	return nil
}

// subMapInstanceByTask returns the most recent sub-map instance for the given
// parent task card id, regardless of status. Used when writing promoted outputs.
func (a *Actor) subMapInstanceByTask(parentTaskCardID string) *subMapInstance {
	a.subMapMu.RLock()
	defer a.subMapMu.RUnlock()
	var found *subMapInstance
	for i := range a.subMapInstances {
		if a.subMapInstances[i].ParentTaskCardID == parentTaskCardID {
			inst := a.subMapInstances[i]
			if found == nil || inst.CreatedAt > found.CreatedAt {
				found = &inst
			}
		}
	}
	return found
}

// updateSubMapInstance updates the status/outputs of a sub-map instance by
// owner agent id.
func (a *Actor) updateSubMapInstance(ownerAgentActorID, status string, outputs map[string]any) {
	a.subMapMu.Lock()
	for i := range a.subMapInstances {
		if a.subMapInstances[i].OwnerAgentActorID != ownerAgentActorID {
			continue
		}
		a.subMapInstances[i].Status = status
		if len(outputs) > 0 {
			if a.subMapInstances[i].Outputs == nil {
				a.subMapInstances[i].Outputs = make(map[string]any)
			}
			for k, v := range outputs {
				a.subMapInstances[i].Outputs[k] = v
			}
		}
		// Snapshot under the writer lock so the persistence includes our write.
		snapshot := make([]subMapInstance, len(a.subMapInstances))
		copy(snapshot, a.subMapInstances)
		a.subMapMu.Unlock()
		_ = a.saveSubMapInstancesSnapshot(snapshot)
		return
	}
	a.subMapMu.Unlock()
}

// handleSubMapOwnerStatus is called on every workspace.agent_status_update. It
// watches for sub-map owner transitions and propagates completion/failure to
// the parent task card. Only owner agents that were spawned by the sub_map
// executor are tracked in subMapInstances.
func (a *Actor) handleSubMapOwnerStatus(ctx actor.PureContext, agentActorID, state, activeWorkflowMapCardID, agentError string) {
	inst := a.subMapInstanceByOwner(agentActorID)
	if inst == nil {
		return
	}
	if inst.Status != "active" {
		return
	}
	switch state {
	case "idle":
		if activeWorkflowMapCardID != "" {
			return
		}
		a.updateSubMapInstance(agentActorID, "completing", nil)
		outputs, err := a.promoteSubMapOutputs(ctx, inst.ProjectID, inst.ParentTaskCardID, inst.InstanceMapID)
		if err != nil {
			a.updateSubMapInstance(agentActorID, "failed", map[string]any{"error": err.Error()})
			a.markParentTaskCardStatus(ctx, inst.ProjectID, inst.ParentTaskCardID, "failed", err.Error())
			return
		}
		a.updateSubMapInstance(agentActorID, "done", outputs)
		a.markParentTaskCardStatus(ctx, inst.ProjectID, inst.ParentTaskCardID, "done", "")
	case "failed", "error":
		note := agentError
		if note == "" {
			note = fmt.Sprintf("sub-map owner %s reported %s", agentActorID, state)
		}
		a.updateSubMapInstance(agentActorID, state, map[string]any{"error": note})
		a.markParentTaskCardStatus(ctx, inst.ProjectID, inst.ParentTaskCardID, "failed", note)
	}
}

// promoteSubMapOutputs merges the data.task_outputs of every completed task
// card inside the sub-map instance, validates the merged map against the
// parent task card's data.outputs contract, and returns the merged outputs.
// If validation fails, an error is returned and the parent task remains doing.
func (a *Actor) promoteSubMapOutputs(ctx actor.PureContext, projectID, parentTaskCardID, instanceMapID string) (map[string]any, error) {
	instanceRaw, err := a.fetchCardRaw(ctx, projectID, instanceMapID)
	if err != nil {
		return nil, fmt.Errorf("fetch instance map %q: %w", instanceMapID, err)
	}
	instanceCard := project.ParseCardRaw(instanceMapID, instanceRaw)
	include := scopeIncludeIDs(instanceCard.Data)
	merged := make(map[string]any)
	for _, taskID := range include {
		raw, err := a.fetchCardRaw(ctx, projectID, taskID)
		if err != nil {
			continue
		}
		card := project.ParseCardRaw(taskID, raw)
		if card.Status != "done" {
			continue
		}
		outs := taskOutputsFromCardData(card.Data["task_outputs"])
		for k, v := range outs {
			merged[k] = v
		}
	}
	if len(merged) == 0 {
		return nil, fmt.Errorf("sub-map %q produced no outputs", instanceMapID)
	}
	if err := a.validateParentOutputs(ctx, projectID, parentTaskCardID, merged); err != nil {
		return nil, fmt.Errorf("outputs validation failed: %w", err)
	}
	return merged, nil
}

// validateParentOutputs invokes project.task_validate_outputs against the
// parent task card's declared data.outputs contract.
func (a *Actor) validateParentOutputs(ctx actor.PureContext, projectID, parentTaskCardID string, outputs map[string]any) error {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return fmt.Errorf("invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return fmt.Errorf("project actor unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.task_validate_outputs", gen.ProjectTaskValidateOutputsReq{
		CardID: parentTaskCardID,
		Outputs: outputs,
	})
	if call == nil {
		return fmt.Errorf("validate_outputs invoke returned nil")
	}
	result, err := call.Final(callCtx)
	if err != nil {
		return err
	}
	resp, ok := result.(gen.ProjectTaskValidateOutputsResp)
	if !ok {
		return fmt.Errorf("unexpected validate_outputs response %T", result)
	}
	if !resp.Valid {
		msgs := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			msgs = append(msgs, fmt.Sprintf("%s: %s", e.Field, e.Message))
		}
		return fmt.Errorf("%s", strings.Join(msgs, "; "))
	}
	return nil
}

// markParentTaskCardStatus updates the parent task card status and writes the
// promoted outputs on success. It uses project.wiki_set_status and
// project.wiki_set_task_outputs.
func (a *Actor) markParentTaskCardStatus(ctx actor.PureContext, projectID, parentTaskCardID, status, note string) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		a.logSubMapError(ctx, "parse project id", err)
		return
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		a.logSubMapError(ctx, "lookup project", fmt.Errorf("project actor unavailable"))
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()

	// Update status.
	_ = projectRef.Invoke(callCtx, "project.wiki_set_status", domain.WikiSetStatusReq{
		ID:     parentTaskCardID,
		Status: status,
	})

	// On success, write the promoted outputs.
	if status == "done" {
		inst := a.subMapInstanceByTask(parentTaskCardID)
		if inst != nil && len(inst.Outputs) > 0 {
			_ = projectRef.Invoke(callCtx, "project.wiki_set_task_outputs", domain.WikiSetTaskOutputsReq{
				CardID:  parentTaskCardID,
				Outputs: inst.Outputs,
			})
		}
	}
	if status == "failed" && note != "" {
		_ = projectRef.Invoke(callCtx, "project.wiki_set_task_outputs", domain.WikiSetTaskOutputsReq{
			CardID: parentTaskCardID,
			Outputs: map[string]any{"error": note},
		})
	}
}

// scopeIncludeIDs extracts the list of task card ids from a map card's
// data.scope.include block. Exported mirror of project.scopeIncludeIDs for use
// by the sub_map executor without exporting the project helper.
func scopeIncludeIDs(data map[string]any) []string {
	if data == nil {
		return nil
	}
	scope, _ := data["scope"].(map[string]any)
	if scope == nil {
		return nil
	}
	if ids, ok := scope["include"].([]string); ok {
		return ids
	}
	if items, ok := scope["include"].([]any); ok {
		out := make([]string, 0, len(items))
		for _, item := range items {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	if text, ok := scope["include"].(string); ok {
		text = strings.Trim(strings.TrimSpace(text), "[]")
		if text == "" {
			return nil
		}
		items := strings.Split(text, ",")
		out := make([]string, 0, len(items))
		for _, item := range items {
			item = strings.TrimSpace(strings.Trim(item, "\"'"))
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	}
	return nil
}

// taskOutputsFromCardData extracts a JSON object from a card.Data["task_outputs"]
// value, which may be stored as a map or as a JSON-encoded string.
func taskOutputsFromCardData(raw any) map[string]any {
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	s, ok := raw.(string)
	if !ok {
		return nil
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}

// logSubMapError logs a sub-map propagation error. The logger is obtained from
// context; if unavailable the error is dropped.
func (a *Actor) logSubMapError(ctx actor.PureContext, op string, err error) {
	if ctx == nil || err == nil {
		return
	}
	ctx.Logger().Error("workspace: sub_map propagation", "op", op, "error", err)
}

// sweepSubMapInstances polls parent task cards and propagates cancellation or
// failure from the parent task down to the sub-map owner. This closes the
// bidirectional cancellation contract: parent task cancelled → child owner
// terminated; child owner failed → parent task failed (handled above).
func (a *Actor) sweepSubMapInstances(ctx actor.PureContext) {
	a.subMapMu.RLock()
	active := make([]subMapInstance, 0, len(a.subMapInstances))
	for _, inst := range a.subMapInstances {
		if inst.Status == "active" {
			active = append(active, inst)
		}
	}
	a.subMapMu.RUnlock()
	if len(active) == 0 {
		return
	}
	for _, inst := range active {
		raw, err := a.fetchCardRaw(ctx, inst.ProjectID, inst.ParentTaskCardID)
		if err != nil {
			a.logSubMapError(ctx, "fetch parent task", err)
			continue
		}
		card := project.ParseCardRaw(inst.ParentTaskCardID, raw)
		switch card.Status {
		case "cancelled":
			if _, err := a.handleAgentTerminate(ctx, domain.WorkspaceAgentTerminateReq{
				AgentActorID:  inst.OwnerAgentActorID,
				CallerAgentID: inst.ParentAgentID,
				Reason:        "parent task cancelled",
			}); err != nil {
				a.logSubMapError(ctx, "terminate sub-map owner", err)
			}
			a.updateSubMapInstance(inst.OwnerAgentActorID, "cancelled", nil)
		case "failed":
			if _, err := a.handleAgentTerminate(ctx, domain.WorkspaceAgentTerminateReq{
				AgentActorID:  inst.OwnerAgentActorID,
				CallerAgentID: inst.ParentAgentID,
				Reason:        "parent task failed",
			}); err != nil {
				a.logSubMapError(ctx, "terminate sub-map owner", err)
			}
			a.updateSubMapInstance(inst.OwnerAgentActorID, "cancelled", nil)
		}
	}
}
