package glassinteract

import (
	"sort"
	"sync"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	// agentHistoryTTL is how long finished agents remain visible in the idle
	// history log after their last terminal transition.
	agentHistoryTTL = 30 * time.Minute
	// maxActiveDisplay limits how many active agents are shown in the left
	// column. The total line budget is small (G2 ~7 lines).
	maxActiveDisplay = 4
	// maxHistoryDisplay limits how many finished agents are shown in the right
	// column idle history log.
	maxHistoryDisplay = 8
)

// finishedAgent records a single terminal transition for an agent.
type finishedAgent struct {
	ActorID     string
	DisplayName string
	Title       string
	Outcome     string // "completed" | "failed" | "cancelled"
	FinishedAt  time.Time
}

// agentMonitor maintains the latest workspace agent snapshot and a bounded
// history of agents that finished within agentHistoryTTL.
type agentMonitor struct {
	mu        sync.RWMutex
	snapshot  gen.WorkspaceAgentListState
	updatedAt time.Time
	history   []finishedAgent
}

func newAgentMonitor() *agentMonitor {
	return &agentMonitor{}
}

// update replaces the snapshot, detects running->terminal transitions, and
// prunes stale history.
func (m *agentMonitor) update(state gen.WorkspaceAgentListState, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prevByID := make(map[string]gen.AgentListItem, len(m.snapshot.Items))
	for _, it := range m.snapshot.Items {
		prevByID[it.ActorID] = it
	}

	for i := range state.Items {
		cur := state.Items[i]
		prev, hadPrev := prevByID[cur.ActorID]
		if !hadPrev {
			continue
		}
		wasRunning := prev.Runtime != nil && prev.Runtime.State == "running"
		isRunning := cur.Runtime != nil && cur.Runtime.State == "running"
		if wasRunning && !isRunning {
			outcome := "completed"
			if cur.Runtime != nil && cur.Runtime.State == "failed" {
				outcome = "failed"
			}
			m.recordFinished(cur, outcome, now)
		}
		delete(prevByID, cur.ActorID)
	}

	// Any remaining prev agent that was running but is no longer present is
	// treated as failed/removed.
	for _, prev := range prevByID {
		if prev.Runtime != nil && prev.Runtime.State == "running" {
			m.recordFinished(prev, "failed", now)
		}
	}

	m.snapshot = state
	m.updatedAt = now
	m.prune(now)
}

func (m *agentMonitor) recordFinished(item gen.AgentListItem, outcome string, now time.Time) {
	// Replace the most recent entry for the same actor/outcome pair instead of
	// creating duplicates.
	for i := range m.history {
		if m.history[i].ActorID == item.ActorID && m.history[i].Outcome == outcome {
			m.history[i].FinishedAt = now
			m.history[i].DisplayName = item.DisplayName
			m.history[i].Title = item.Title
			return
		}
	}
	m.history = append(m.history, finishedAgent{
		ActorID:     item.ActorID,
		DisplayName: item.DisplayName,
		Title:       item.Title,
		Outcome:     outcome,
		FinishedAt:  now,
	})
}

func (m *agentMonitor) prune(now time.Time) {
	cutoff := now.Add(-agentHistoryTTL)
	kept := m.history[:0]
	for _, h := range m.history {
		if h.FinishedAt.After(cutoff) {
			kept = append(kept, h)
		}
	}
	m.history = kept
}

// active returns currently running agents sorted by display name. The
// coordinator is excluded because it has its own dedicated status line.
func (m *agentMonitor) active() []gen.AgentListItem {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []gen.AgentListItem
	for _, it := range m.snapshot.Items {
		if it.AgentKind == "coordinator" {
			continue
		}
		if it.Runtime != nil && it.Runtime.State == "running" {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].DisplayName < out[j].DisplayName
	})
	return out
}

// activeCount returns the number of running agents.
func (m *agentMonitor) activeCount() int {
	return len(m.active())
}

// recentHistory returns finished agents newest-first. update() already prunes
// stale entries on every refresh, so this is a read-only snapshot.
func (m *agentMonitor) recentHistory() []finishedAgent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]finishedAgent, len(m.history))
	for i := range m.history {
		out[len(m.history)-1-i] = m.history[i]
	}
	return out
}

// coordinatorIdle reports whether the coordinator agent is not running.
func (m *agentMonitor) coordinatorIdle() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, it := range m.snapshot.Items {
		if it.AgentKind == "coordinator" {
			return it.Runtime == nil || it.Runtime.State != "running"
		}
	}
	return true
}

// coordinatorDisplayName returns the coordinator's display name if known.
func (m *agentMonitor) coordinatorDisplayName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, it := range m.snapshot.Items {
		if it.AgentKind == "coordinator" {
			if it.DisplayName != "" {
				return it.DisplayName
			}
			return "coordinator"
		}
	}
	return "coordinator"
}
