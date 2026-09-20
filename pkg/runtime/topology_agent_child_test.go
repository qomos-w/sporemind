package runtime

import (
	"reflect"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestRewireAgentChildEdges_LogicalParentOverridesPhysical(t *testing.T) {
	// Physical tree: workspace → project → parentAgent, project → childAgent
	nodes := []domain.UnifiedGraphNode{
		{ID: "ws", Kind: "workspace", ParentID: ""},
		{ID: "proj", Kind: "project", ParentID: "ws"},
		{ID: "parent-agent", Kind: "agent", ParentID: "proj", Status: "idle"},
		{ID: "child-agent", Kind: "agent", ParentID: "proj", Status: "running"},
	}
	edges := []domain.UnifiedGraphEdge{
		{ID: "e:proj", From: "ws", To: "proj", Kind: "child"},
		{ID: "e:parent-agent", From: "proj", To: "parent-agent", Kind: "child"},
		{ID: "e:child-agent", From: "proj", To: "child-agent", Kind: "child"},
	}
	agents := []domain.AgentRef{
		{ActorID: "parent-agent", ParentAgentID: ""},
		{ActorID: "child-agent", ParentAgentID: "parent-agent", LifecycleScope: "workflow"},
	}

	outNodes, outEdges := rewireAgentChildEdges(nodes, edges, agents)

	// child-agent's ParentId should be rewritten to parent-agent.
	childNode := findNode(outNodes, "child-agent")
	if childNode.ParentID != "parent-agent" {
		t.Errorf("child-agent ParentId = %q, want %q", childNode.ParentID, "parent-agent")
	}
	if childNode.Detail["lifecycle_scope"] != "workflow" {
		t.Errorf("child-agent lifecycle_scope = %v, want %q", childNode.Detail["lifecycle_scope"], "workflow")
	}
	if childNode.Detail["logical_parent_agent_id"] != "parent-agent" {
		t.Errorf("child-agent logical_parent_agent_id = %v, want %q", childNode.Detail["logical_parent_agent_id"], "parent-agent")
	}
	if childNode.Detail["physical_parent_id"] != "proj" {
		t.Errorf("child-agent physical_parent_id = %v, want %q", childNode.Detail["physical_parent_id"], "proj")
	}

	// parent-agent should be unchanged (no logical parent).
	parentNode := findNode(outNodes, "parent-agent")
	if parentNode.ParentID != "proj" {
		t.Errorf("parent-agent ParentId = %q, want %q (should be unchanged)", parentNode.ParentID, "proj")
	}

	// Physical child edge to child-agent should be removed.
	if findEdge(outEdges, "e:child-agent") != nil {
		t.Error("physical child edge e:child-agent should be removed")
	}

	// agent_child edge should be added.
	acEdge := findEdge(outEdges, "ac:child-agent")
	if acEdge == nil {
		t.Fatal("agent_child edge ac:child-agent should exist")
	}
	if acEdge.From != "parent-agent" || acEdge.To != "child-agent" {
		t.Errorf("agent_child edge From/To = %q→%q, want parent-agent→child-agent", acEdge.From, acEdge.To)
	}
	if acEdge.Kind != "agent_child" {
		t.Errorf("agent_child edge Kind = %q, want %q", acEdge.Kind, "agent_child")
	}
	if acEdge.LifecycleScope != "workflow" {
		t.Errorf("agent_child edge LifecycleScope = %q, want %q", acEdge.LifecycleScope, "workflow")
	}
	if acEdge.Status != "running" {
		t.Errorf("agent_child edge Status = %q, want %q", acEdge.Status, "running")
	}
}

func TestRewireAgentChildEdges_NoAgentsNoOp(t *testing.T) {
	nodes := []domain.UnifiedGraphNode{{ID: "a", Kind: "agent", ParentID: "proj"}}
	edges := []domain.UnifiedGraphEdge{{ID: "e:a", From: "proj", To: "a", Kind: "child"}}

	outNodes, outEdges := rewireAgentChildEdges(nodes, edges, nil)
	if !reflect.DeepEqual(outNodes, nodes) {
		t.Error("nodes should be unchanged when agents list is empty")
	}
	if !reflect.DeepEqual(outEdges, edges) {
		t.Error("edges should be unchanged when agents list is empty")
	}
}

func TestRewireAgentChildEdges_MissingParentKeepsPhysicalEdge(t *testing.T) {
	// child-agent has ParentAgentId "ghost" which is not in the graph.
	nodes := []domain.UnifiedGraphNode{
		{ID: "proj", Kind: "project"},
		{ID: "child-agent", Kind: "agent", ParentID: "proj"},
	}
	edges := []domain.UnifiedGraphEdge{
		{ID: "e:child-agent", From: "proj", To: "child-agent", Kind: "child"},
	}
	agents := []domain.AgentRef{
		{ActorID: "child-agent", ParentAgentID: "ghost", LifecycleScope: "workflow"},
	}

	outNodes, outEdges := rewireAgentChildEdges(nodes, edges, agents)

	// Physical edge should be retained.
	if findEdge(outEdges, "e:child-agent") == nil {
		t.Error("physical child edge should be retained when logical parent is missing")
	}
	if findEdge(outEdges, "ac:child-agent") != nil {
		t.Error("agent_child edge should not be created when logical parent is missing")
	}
	// ParentId should be unchanged.
	if n := findNode(outNodes, "child-agent"); n.ParentID != "proj" {
		t.Errorf("ParentId = %q, want proj (unchanged)", n.ParentID)
	}
}

func TestRewireAgentChildEdges_MultiLevelChain(t *testing.T) {
	// parent → child → grandchild chain.
	nodes := []domain.UnifiedGraphNode{
		{ID: "proj", Kind: "project"},
		{ID: "parent", Kind: "agent", ParentID: "proj"},
		{ID: "child", Kind: "agent", ParentID: "proj"},
		{ID: "grandchild", Kind: "agent", ParentID: "proj"},
	}
	edges := []domain.UnifiedGraphEdge{
		{ID: "e:parent", From: "proj", To: "parent", Kind: "child"},
		{ID: "e:child", From: "proj", To: "child", Kind: "child"},
		{ID: "e:grandchild", From: "proj", To: "grandchild", Kind: "child"},
	}
	agents := []domain.AgentRef{
		{ActorID: "child", ParentAgentID: "parent", LifecycleScope: "workflow"},
		{ActorID: "grandchild", ParentAgentID: "child", LifecycleScope: "workflow"},
	}

	outNodes, outEdges := rewireAgentChildEdges(nodes, edges, agents)

	// Both child and grandchild should be rewired.
	if n := findNode(outNodes, "child"); n.ParentID != "parent" {
		t.Errorf("child ParentId = %q, want parent", n.ParentID)
	}
	if n := findNode(outNodes, "grandchild"); n.ParentID != "child" {
		t.Errorf("grandchild ParentId = %q, want child", n.ParentID)
	}

	// parent keeps its physical edge.
	if findEdge(outEdges, "e:parent") == nil {
		t.Error("parent physical child edge should be retained")
	}
	// child and grandchild get agent_child edges.
	if findEdge(outEdges, "ac:child") == nil {
		t.Error("agent_child edge ac:child should exist")
	}
	if findEdge(outEdges, "ac:grandchild") == nil {
		t.Error("agent_child edge ac:grandchild should exist")
	}
}

func TestRewireAgentChildEdges_DeterministicEdgeOrder(t *testing.T) {
	agents := []domain.AgentRef{
		{ActorID: "z-agent", ParentAgentID: "parent"},
		{ActorID: "a-agent", ParentAgentID: "parent"},
		{ActorID: "m-agent", ParentAgentID: "parent"},
	}
	nodes := []domain.UnifiedGraphNode{
		{ID: "parent", Kind: "agent"},
		{ID: "z-agent", Kind: "agent"},
		{ID: "a-agent", Kind: "agent"},
		{ID: "m-agent", Kind: "agent"},
	}
	edges := []domain.UnifiedGraphEdge{
		{ID: "e:z-agent", From: "root", To: "z-agent", Kind: "child"},
		{ID: "e:a-agent", From: "root", To: "a-agent", Kind: "child"},
		{ID: "e:m-agent", From: "root", To: "m-agent", Kind: "child"},
	}

	_, outEdges := rewireAgentChildEdges(nodes, edges, agents)

	// The three agent_child edges should be sorted by agent ID.
	var acKinds []string
	for _, e := range outEdges {
		if e.Kind == "agent_child" {
			acKinds = append(acKinds, e.ID)
		}
	}
	want := []string{"ac:a-agent", "ac:m-agent", "ac:z-agent"}
	if !reflect.DeepEqual(acKinds, want) {
		t.Errorf("agent_child edge order = %v, want %v", acKinds, want)
	}
}

func TestRewireAgentChildEdges_SelfLoopGuard(t *testing.T) {
	// Agent whose ParentAgentId points to itself — should be ignored.
	nodes := []domain.UnifiedGraphNode{
		{ID: "proj", Kind: "project"},
		{ID: "self", Kind: "agent", ParentID: "proj"},
	}
	edges := []domain.UnifiedGraphEdge{
		{ID: "e:self", From: "proj", To: "self", Kind: "child"},
	}
	agents := []domain.AgentRef{
		{ActorID: "self", ParentAgentID: "self", LifecycleScope: "workflow"},
	}

	outNodes, outEdges := rewireAgentChildEdges(nodes, edges, agents)

	if n := findNode(outNodes, "self"); n.ParentID != "proj" {
		t.Errorf("self-loop agent ParentId = %q, want proj (unchanged)", n.ParentID)
	}
	if findEdge(outEdges, "e:self") == nil {
		t.Error("physical child edge should be retained for self-loop agent")
	}
}

// --- helpers ---

func findNode(nodes []domain.UnifiedGraphNode, id string) *domain.UnifiedGraphNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func findEdge(edges []domain.UnifiedGraphEdge, id string) *domain.UnifiedGraphEdge {
	for i := range edges {
		if edges[i].ID == id {
			return &edges[i]
		}
	}
	return nil
}
