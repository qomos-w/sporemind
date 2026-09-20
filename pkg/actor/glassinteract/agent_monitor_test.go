package glassinteract

import (
	"strings"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func runtimeState(state string) *gen.AgentRuntimeState {
	return &gen.AgentRuntimeState{State: state}
}

func TestAgentMonitorActive(t *testing.T) {
	m := newAgentMonitor()
	now := time.Unix(1000000, 0)
	m.update(gen.WorkspaceAgentListState{
		Items: []gen.AgentListItem{
			{ActorID: "a1", DisplayName: "coder", Runtime: runtimeState("running")},
			{ActorID: "a2", DisplayName: "reviewer", Runtime: runtimeState("idle")},
			{ActorID: "a3", DisplayName: "orchestrator", Runtime: runtimeState("running")},
		},
	}, now)

	active := m.active()
	if len(active) != 2 {
		t.Fatalf("active count = %d, want 2", len(active))
	}
	if m.activeCount() != 2 {
		t.Fatalf("activeCount = %d, want 2", m.activeCount())
	}
	if active[0].DisplayName != "coder" || active[1].DisplayName != "orchestrator" {
		t.Fatalf("active not sorted: %+v", active)
	}
}

func TestAgentMonitorTransitionCompleted(t *testing.T) {
	m := newAgentMonitor()
	now := time.Unix(1000000, 0)
	m.update(gen.WorkspaceAgentListState{
		Items: []gen.AgentListItem{
			{ActorID: "a1", DisplayName: "coder", Runtime: runtimeState("running")},
		},
	}, now)
	m.update(gen.WorkspaceAgentListState{
		Items: []gen.AgentListItem{
			{ActorID: "a1", DisplayName: "coder", Runtime: runtimeState("")},
		},
	}, now.Add(time.Second))

	hist := m.recentHistory()
	if len(hist) != 1 || hist[0].Outcome != "completed" {
		t.Fatalf("history = %+v", hist)
	}
}

func TestAgentMonitorTransitionFailed(t *testing.T) {
	m := newAgentMonitor()
	now := time.Unix(1000000, 0)
	m.update(gen.WorkspaceAgentListState{
		Items: []gen.AgentListItem{
			{ActorID: "a1", DisplayName: "coder", Runtime: runtimeState("running")},
		},
	}, now)
	m.update(gen.WorkspaceAgentListState{
		Items: []gen.AgentListItem{
			{ActorID: "a1", DisplayName: "coder", Runtime: runtimeState("failed")},
		},
	}, now.Add(time.Second))

	hist := m.recentHistory()
	if len(hist) != 1 || hist[0].Outcome != "failed" {
		t.Fatalf("history = %+v", hist)
	}
}

func TestAgentMonitorHistoryTTL(t *testing.T) {
	m := newAgentMonitor()
	now := time.Unix(1000000, 0)
	m.update(gen.WorkspaceAgentListState{
		Items: []gen.AgentListItem{
			{ActorID: "a1", DisplayName: "old", Runtime: runtimeState("running")},
		},
	}, now)
	m.update(gen.WorkspaceAgentListState{
		Items: []gen.AgentListItem{
			{ActorID: "a1", DisplayName: "old", Runtime: runtimeState("")},
		},
	}, now.Add(time.Second))

	later := now.Add(agentHistoryTTL + time.Minute)
	m.update(gen.WorkspaceAgentListState{Items: []gen.AgentListItem{}}, later)
	hist := m.recentHistory()
	if len(hist) != 0 {
		t.Fatalf("pruned history = %+v", hist)
	}
}

func TestAgentMonitorCoordinatorIdle(t *testing.T) {
	m := newAgentMonitor()
	now := time.Unix(1000000, 0)
	if !m.coordinatorIdle() {
		t.Fatal("expected idle when no coordinator in snapshot")
	}
	m.update(gen.WorkspaceAgentListState{
		Items: []gen.AgentListItem{
			{ActorID: "c1", DisplayName: "管家", AgentKind: "coordinator", Runtime: runtimeState("running")},
		},
	}, now)
	if m.coordinatorIdle() {
		t.Fatal("expected coordinator busy")
	}
	m.update(gen.WorkspaceAgentListState{
		Items: []gen.AgentListItem{
			{ActorID: "c1", DisplayName: "管家", AgentKind: "coordinator", Runtime: runtimeState("")},
		},
	}, now.Add(time.Second))
	if !m.coordinatorIdle() {
		t.Fatal("expected coordinator idle")
	}
}

func TestIdleFrameRealData(t *testing.T) {
	m := newAgentMonitor()
	now := time.Unix(1000000, 0)
	m.update(gen.WorkspaceAgentListState{
		Items: []gen.AgentListItem{
			{ActorID: "c1", DisplayName: "管家", AgentKind: "coordinator", Runtime: runtimeState("")},
			{ActorID: "a1", DisplayName: "coder", Runtime: runtimeState("running"), Title: "编码助手"},
			{ActorID: "a2", DisplayName: "reviewer", Runtime: runtimeState("running")},
			{ActorID: "a3", DisplayName: "old", Runtime: runtimeState("running")},
		},
	}, now)
	m.update(gen.WorkspaceAgentListState{
		Items: []gen.AgentListItem{
			{ActorID: "c1", DisplayName: "管家", AgentKind: "coordinator", Runtime: runtimeState("")},
			{ActorID: "a1", DisplayName: "coder", Runtime: runtimeState("running"), Title: "编码助手"},
			{ActorID: "a2", DisplayName: "reviewer", Runtime: runtimeState("running")},
			{ActorID: "a3", DisplayName: "old", Runtime: runtimeState("")},
		},
	}, now.Add(time.Second))

	frame := IdleFrame(now.Add(time.Second), m, "████░", "\u25cf")
	if frame.Scene == nil || len(frame.Scene.Elements) != 2 {
		t.Fatalf("frame elements = %d, want 2", len(frame.Scene.Elements))
	}
	left := frame.Scene.Elements[0].Text
	right := frame.Scene.Elements[1].Text

	if !strings.Contains(left, "\u25cf 管家 空闲") {
		t.Fatalf("left missing bottom connection+coordinator: %s", left)
	}
	if !strings.Contains(left, "\u00b7 编码助手") {
		t.Fatalf("left missing active agent Title: %s", left)
	}
	if strings.Contains(right, "最近活动") {
		t.Fatalf("right should not have title: %s", right)
	}
	if !strings.Contains(right, "[完成]") {
		t.Fatalf("right missing completed entry: %s", right)
	}
}
