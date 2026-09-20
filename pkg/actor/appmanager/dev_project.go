package appmanager

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Dev toolchain entrypoints (dev_generate / dev_gate / register_project /
// reload_project) take a canonical project id. Agents used to transcribe that
// 32-hex id from prompt text into every call, and model transcription drift
// produced intermittent "invalid canonical id format" failures that looked
// like host nondeterminism. ProjectID is therefore now optional for agent
// callers: when omitted, the project is resolved from the turn-engine
// injected CallerAgentID (unforgeable), which is bound to exactly one project
// at spawn time. Every resolution failure carries the candidate project list
// so the caller can self-correct in one step.

const workspaceServiceName = "workspace"

// availableProjectsHint asks the workspace for the project registry and
// renders a compact candidate list ("name=<n> id=<id>", capped). Returns an
// empty string when the registry is unreachable — the hint must never mask
// the original error.
func availableProjectsHint(ctx actor.Context) string {
	planner := ctx.Planner()
	if planner == nil {
		return ""
	}
	wsRef, found := ctx.LookupService(workspaceServiceName)
	if !found {
		return ""
	}
	v, err := planner.Call(ctx.Lifecycle(), wsRef, "workspace.list_project", struct{}{}).Await()
	if err != nil {
		return ""
	}
	resp, ok := v.(gen.ProjectRefListResp)
	if !ok {
		return ""
	}
	names := make([]string, 0, len(resp.Items))
	for _, item := range resp.Items {
		if item.ActorID == "" {
			continue
		}
		names = append(names, fmt.Sprintf("%s id=%s", item.Name, item.ActorID))
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	const cap = 8
	if len(names) > cap {
		names = append(names[:cap:cap], fmt.Sprintf("... +%d more", len(names)-cap))
	}
	return "\navailable projects: " + strings.Join(names, "; ")
}

// callerBoundProjectID resolves the calling agent's bound project. Empty
// callerAgentID means a human/internal caller, which has no binding and must
// pass ProjectID explicitly.
func callerBoundProjectID(ctx actor.Context, callerAgentID string) (string, error) {
	callerAgentID = strings.TrimSpace(callerAgentID)
	if callerAgentID == "" {
		return "", fmt.Errorf("project id required: pass ProjectID explicitly (non-agent callers have no bound project)%s", availableProjectsHint(ctx))
	}
	planner := ctx.Planner()
	if planner == nil {
		return "", fmt.Errorf("project id required: planner not available to resolve the calling agent's project")
	}
	wsRef, found := ctx.LookupService(workspaceServiceName)
	if !found {
		return "", fmt.Errorf("project id required: workspace service not available to resolve the calling agent's project")
	}
	v, err := planner.Call(ctx.Lifecycle(), wsRef, "workspace.list_agents", gen.WorkspaceListAgentsReq{}).Await()
	if err != nil {
		return "", fmt.Errorf("project id required: resolving agent %s failed: %v%s", callerAgentID, err, availableProjectsHint(ctx))
	}
	resp, ok := v.(gen.AgentRefListResp)
	if !ok {
		return "", fmt.Errorf("project id required: unexpected workspace.list_agents response")
	}
	for _, agent := range resp.Items {
		if agent.ActorID == callerAgentID {
			if strings.TrimSpace(agent.ProjectID) == "" {
				return "", fmt.Errorf("project id required: agent %s (%s) is not bound to a project%s", agent.DisplayName, callerAgentID, availableProjectsHint(ctx))
			}
			return agent.ProjectID, nil
		}
	}
	return "", fmt.Errorf("project id required: agent %s not found in the workspace registry%s", callerAgentID, availableProjectsHint(ctx))
}

// resolveDevProjectID normalizes the dev entrypoints' project selection:
// explicit ProjectID wins; omitted means "the calling agent's bound project".
func resolveDevProjectID(ctx actor.Context, projectID, callerAgentID string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		return projectID, nil
	}
	return callerBoundProjectID(ctx, callerAgentID)
}
