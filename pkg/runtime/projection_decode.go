package runtime

import (
	"github.com/qomos-w/gospore/projection"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// decodeAgentRefs converts the projection-store snapshot of the workspace
// "agents" field (a []any of map[string]any) back into []domain.AgentRef.
// The reflection-based snapshotter in gospore destroys concrete slice and
// struct types, so a direct .([]domain.AgentRef) type assertion always fails.
func decodeAgentRefs(raw any) []domain.AgentRef {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	agents := make([]domain.AgentRef, 0, len(items))
	for _, item := range items {
		snap, ok := item.(projection.FieldSnapshot)
		if !ok {
			continue
		}
		var ag domain.AgentRef
		if err := projection.ApplyTo(&ag, snap); err == nil {
			agents = append(agents, ag)
		}
	}
	return agents
}

// decodeProjectRefs converts the projection-store snapshot of the workspace
// "mounts" field back into []domain.ProjectRef.
func decodeProjectRefs(raw any) []domain.ProjectRef {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	mounts := make([]domain.ProjectRef, 0, len(items))
	for _, item := range items {
		snap, ok := item.(projection.FieldSnapshot)
		if !ok {
			continue
		}
		var m domain.ProjectRef
		if err := projection.ApplyTo(&m, snap); err == nil {
			mounts = append(mounts, m)
		}
	}
	return mounts
}

// decodeMcpServerViews converts the projection-store snapshot of an
// mcpinstance "serverViews" field (a []any of field snapshots) back into
// []domain.McpServerView. The projection carries only the read-safe view —
// env/header values never leave the instance actor.
func decodeMcpServerViews(raw any) []domain.McpServerView {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	views := make([]domain.McpServerView, 0, len(items))
	for _, item := range items {
		snap, ok := item.(projection.FieldSnapshot)
		if !ok {
			continue
		}
		var v domain.McpServerView
		if err := projection.ApplyTo(&v, snap); err == nil {
			views = append(views, v)
		}
	}
	return views
}