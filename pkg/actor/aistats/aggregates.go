package aistats

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// counters is the in-memory aggregate table. It keeps six views:
// session, project, workspace, provider, model. All views are updated together
// when a record is appended. The global actor aggregates across all
// workspaces: `workspace` holds the cross-workspace total, while `workspaceMap`
// keeps per-workspace totals so a query can return a single workspace's
// counters without rescanning records.
type counters struct {
	workspace    gen.AIStatsCounters
	workspaceMap map[string]gen.AIStatsCounters
	session      map[string]gen.AIStatsCounters
	project      map[string]gen.AIStatsCounters
	agent        map[string]gen.AIStatsCounters
	provider     map[string]gen.AIStatsCounters
	model        map[string]gen.AIStatsCounters
}

func newCounters() *counters {
	return &counters{
		workspaceMap: make(map[string]gen.AIStatsCounters),
		session:      make(map[string]gen.AIStatsCounters),
		project:      make(map[string]gen.AIStatsCounters),
		agent:        make(map[string]gen.AIStatsCounters),
		provider:     make(map[string]gen.AIStatsCounters),
		model:        make(map[string]gen.AIStatsCounters),
	}
}

func addCounters(a gen.AIStatsCounters, b gen.AIStatsCounters) gen.AIStatsCounters {
	return gen.AIStatsCounters{
		RequestCount:             a.RequestCount + b.RequestCount,
		ErrorCount:               a.ErrorCount + b.ErrorCount,
		LatencySumMs:             a.LatencySumMs + b.LatencySumMs,
		InputTokens:              a.InputTokens + b.InputTokens,
		OutputTokens:             a.OutputTokens + b.OutputTokens,
		TotalTokens:              a.TotalTokens + b.TotalTokens,
		CacheCreationInputTokens: a.CacheCreationInputTokens + b.CacheCreationInputTokens,
		CacheReadInputTokens:     a.CacheReadInputTokens + b.CacheReadInputTokens,
		ReasoningTokens:          a.ReasoningTokens + b.ReasoningTokens,
		CostInput:                a.CostInput + b.CostInput,
		CostOutput:               a.CostOutput + b.CostOutput,
		CostCacheRead:            a.CostCacheRead + b.CostCacheRead,
		CostCacheWrite:           a.CostCacheWrite + b.CostCacheWrite,
		CostTotal:                a.CostTotal + b.CostTotal,
	}
}

// isStopReasonError reports whether a record represents a failed request.
func isStopReasonError(r gen.AIStatsRecord) bool {
	return r.StopReason == "error" || r.StopReason == "aborted" || r.ErrorMessage != ""
}

func recordToCounters(r gen.AIStatsRecord) gen.AIStatsCounters {
	c := gen.AIStatsCounters{RequestCount: 1}
	if isStopReasonError(r) {
		c.ErrorCount = 1
	}
	if r.LatencyMs > 0 {
		c.LatencySumMs = r.LatencyMs
	}
	if r.Usage != nil {
		u := r.Usage
		c.InputTokens = u.InputTokens
		c.OutputTokens = u.OutputTokens
		c.TotalTokens = u.TotalTokens
		c.CacheCreationInputTokens = u.CacheCreationInputTokens
		c.CacheReadInputTokens = u.CacheReadInputTokens
		c.ReasoningTokens = u.ReasoningTokens
		c.CostInput = u.CostInput
		c.CostOutput = u.CostOutput
		c.CostCacheRead = u.CostCacheRead
		c.CostCacheWrite = u.CostCacheWrite
		c.CostTotal = u.CostTotal
	}
	return c
}

func (c *counters) apply(r gen.AIStatsRecord) {
	delta := recordToCounters(r)
	c.workspace = addCounters(c.workspace, delta)
	if r.WorkspaceID != "" {
		c.workspaceMap[r.WorkspaceID] = addCounters(c.workspaceMap[r.WorkspaceID], delta)
	}
	if r.SessionID != "" {
		c.session[r.SessionID] = addCounters(c.session[r.SessionID], delta)
	}
	if r.ProjectID != "" {
		c.project[r.ProjectID] = addCounters(c.project[r.ProjectID], delta)
	}
	if r.AgentID != "" {
		c.agent[r.AgentID] = addCounters(c.agent[r.AgentID], delta)
	}
	if r.Provider != "" {
		c.provider[r.Provider] = addCounters(c.provider[r.Provider], delta)
	}
	if r.Provider != "" && r.Model != "" {
		key := r.Provider + "/" + r.Model
		c.model[key] = addCounters(c.model[key], delta)
	}
}

func (c *counters) query(scope, scopeID string) gen.AIStatsCounters {
	switch scope {
	case "session":
		return c.session[scopeID]
	case "project":
		return c.project[scopeID]
	case "agent":
		return c.agent[scopeID]
	case "workspace":
		if scopeID != "" {
			return c.workspaceMap[scopeID]
		}
		return c.workspace
	case "provider":
		return c.provider[scopeID]
	case "model":
		return c.model[scopeID]
	default:
		return c.workspace
	}
}

// workspaceTotals returns the per-workspace aggregate when workspaceID is set,
// otherwise the cross-workspace global total.
func (c *counters) workspaceTotals(workspaceID string) gen.AIStatsCounters {
	if workspaceID != "" {
		return c.workspaceMap[workspaceID]
	}
	return c.workspace
}

// rebuildCounters replays the given records to reconstruct in-memory views.
// Used on startup when a checkpoint is missing or stale.
func rebuildCounters(records []gen.AIStatsRecord) *counters {
	c := newCounters()
	for _, r := range records {
		c.apply(r)
	}
	return c
}

// snapshot returns a stable aggregate view suitable for persistence. The
// workspace-level counters are included under the empty workspace key.
func (c *counters) snapshot() *checkpoint {
	cp := &checkpoint{
		Workspace:    c.workspace,
		WorkspaceMap: make(map[string]gen.AIStatsCounters, len(c.workspaceMap)),
		Session:      make(map[string]gen.AIStatsCounters, len(c.session)),
		Project:      make(map[string]gen.AIStatsCounters, len(c.project)),
		Agent:        make(map[string]gen.AIStatsCounters, len(c.agent)),
		Provider:     make(map[string]gen.AIStatsCounters, len(c.provider)),
		Model:        make(map[string]gen.AIStatsCounters, len(c.model)),
	}
	for k, v := range c.workspaceMap {
		cp.WorkspaceMap[k] = v
	}
	for k, v := range c.session {
		cp.Session[k] = v
	}
	for k, v := range c.project {
		cp.Project[k] = v
	}
	for k, v := range c.agent {
		cp.Agent[k] = v
	}
	for k, v := range c.provider {
		cp.Provider[k] = v
	}
	for k, v := range c.model {
		cp.Model[k] = v
	}
	return cp
}

// checkpoint is the persisted aggregate cache.
type checkpoint struct {
	Workspace    gen.AIStatsCounters            `json:"workspace"`
	WorkspaceMap map[string]gen.AIStatsCounters `json:"workspaceMap,omitempty"`
	Session      map[string]gen.AIStatsCounters `json:"session"`
	Project      map[string]gen.AIStatsCounters `json:"project"`
	Agent        map[string]gen.AIStatsCounters `json:"agent,omitempty"`
	Provider     map[string]gen.AIStatsCounters `json:"provider"`
	Model        map[string]gen.AIStatsCounters `json:"model"`
}

// checkpointToCounters reconstructs a counters instance from a checkpoint
// snapshot. Shared by loadCheckpoint (persist) and loadCheckpointFS (legacy).
func checkpointToCounters(cp *checkpoint) *counters {
	c := newCounters()
	c.workspace = cp.Workspace
	for k, v := range cp.WorkspaceMap {
		c.workspaceMap[k] = v
	}
	for k, v := range cp.Session {
		c.session[k] = v
	}
	for k, v := range cp.Project {
		c.project[k] = v
	}
	for k, v := range cp.Agent {
		c.agent[k] = v
	}
	for k, v := range cp.Provider {
		c.provider[k] = v
	}
	for k, v := range cp.Model {
		c.model[k] = v
	}
	return c
}

// loadCheckpoint loads the aggregate checkpoint from the persist store. A
// missing checkpoint (first start) returns a fresh empty counters, not an
// error. The checkpoint is stored as document name "checkpoint".
func loadCheckpoint(store persist.Persist) (*counters, error) {
	var cp checkpoint
	if err := store.Load("checkpoint", &cp); err != nil {
		if errors.Is(err, persist.ErrNotExist) {
			return newCounters(), nil
		}
		return nil, fmt.Errorf("aistats: load checkpoint: %w", err)
	}
	return checkpointToCounters(&cp), nil
}

// saveCheckpoint saves the aggregate checkpoint to the persist store.
func saveCheckpoint(store persist.Persist, c *counters) error {
	if err := store.Save("checkpoint", c.snapshot()); err != nil {
		return fmt.Errorf("aistats: save checkpoint: %w", err)
	}
	return nil
}

// --- Legacy filesystem checkpoint helpers (migration only) ---

// loadCheckpointFS reads a checkpoint from the legacy filesystem layout
// (<root>/checkpoint.json). Used by the legacy per-workspace migration which
// operates on raw filesystem paths before the persist store is initialized.
func loadCheckpointFS(root string) (*counters, error) {
	path := filepath.Join(root, "checkpoint.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newCounters(), nil
		}
		return nil, err
	}
	var cp checkpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		return nil, fmt.Errorf("aistats: decode checkpoint: %w", err)
	}
	return checkpointToCounters(&cp), nil
}

// writeCheckpointFS writes a checkpoint to the legacy filesystem layout
// (<root>/checkpoint.json). Used by the legacy per-workspace migration.
func writeCheckpointFS(root string, cp *checkpoint) error {
	path := filepath.Join(root, "checkpoint.json")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("aistats: mkdir checkpoint: %w", err)
	}
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("aistats: marshal checkpoint: %w", err)
	}
	if err := persist.WriteFileAtomic(path, b, 0o644); err != nil {
		return fmt.Errorf("aistats: write checkpoint: %w", err)
	}
	return nil
}
