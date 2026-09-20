package runtime

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/projection"
	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestMcpServerNodeStatus(t *testing.T) {
	cases := []struct {
		name string
		st   domain.McpServerStatus
		want string
	}{
		{"connected", domain.McpServerStatus{Connected: true}, "connected"},
		{"disconnected", domain.McpServerStatus{Connected: false}, "disconnected"},
		{"error wins over connected", domain.McpServerStatus{Connected: true, Error: "session lost"}, "error"},
		{"error when disconnected", domain.McpServerStatus{Connected: false, Error: "handshake failed"}, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpServerNodeStatus(tc.st); got != tc.want {
				t.Errorf("mcpServerNodeStatus(%+v) = %q, want %q", tc.st, got, tc.want)
			}
		})
	}
}

func TestMcpCallEdges_DeterministicSorted(t *testing.T) {
	edges := mcpCallEdges([]string{"agent-b", "agent-a"}, []string{"srv-2", "srv-1"})
	want := []domain.UnifiedGraphEdge{
		{ID: "e:agent-a→srv-1", From: "agent-a", To: "srv-1", Kind: "call"},
		{ID: "e:agent-a→srv-2", From: "agent-a", To: "srv-2", Kind: "call"},
		{ID: "e:agent-b→srv-1", From: "agent-b", To: "srv-1", Kind: "call"},
		{ID: "e:agent-b→srv-2", From: "agent-b", To: "srv-2", Kind: "call"},
	}
	if !reflect.DeepEqual(edges, want) {
		t.Errorf("mcpCallEdges mismatch:\n got %+v\nwant %+v", edges, want)
	}
}

func TestMcpCallEdges_EmptyInputs(t *testing.T) {
	if got := mcpCallEdges(nil, []string{"srv-1"}); got != nil {
		t.Errorf("no agents → expected nil, got %+v", got)
	}
	if got := mcpCallEdges([]string{"agent-a"}, nil); got != nil {
		t.Errorf("no servers → expected nil, got %+v", got)
	}
	if got := mcpCallEdges(nil, nil); got != nil {
		t.Errorf("no inputs → expected nil, got %+v", got)
	}
}

func TestAddMcpCallEdges_OnlyConnectedServers(t *testing.T) {
	tp := &topologyProvider{}
	nodes := []domain.UnifiedGraphNode{
		{ID: "agent-1", Kind: "agent"},
		{ID: "agent-2", Kind: "agent"},
		{ID: "srv-1", Kind: "mcp-server", Status: "connected"},
		{ID: "srv-2", Kind: "mcp-server", Status: "disconnected"},
		{ID: "srv-3", Kind: "mcp-server", Status: "error"},
		{ID: "other", Kind: "actor"},
	}
	_, edges := tp.addMcpCallEdges(nodes, nil)
	want := []domain.UnifiedGraphEdge{
		{ID: "e:agent-1→srv-1", From: "agent-1", To: "srv-1", Kind: "call"},
		{ID: "e:agent-2→srv-1", From: "agent-2", To: "srv-1", Kind: "call"},
	}
	if !reflect.DeepEqual(edges, want) {
		t.Errorf("addMcpCallEdges mismatch:\n got %+v\nwant %+v", edges, want)
	}
}

func TestDecodeMcpServerViews(t *testing.T) {
	view := domain.McpServerView{
		ID:        "srv-1",
		Name:      "My Server",
		Transport: "http",
		Enabled:   true,
		Status:    domain.McpServerStatus{ID: "srv-1", Connected: true, ToolCount: 3},
	}
	snap, err := projection.SnapshotOf(&view)
	if err != nil {
		t.Fatalf("SnapshotOf: %v", err)
	}

	decoded := decodeMcpServerViews([]any{snap})
	if len(decoded) != 1 {
		t.Fatalf("expected 1 view, got %d", len(decoded))
	}
	got := decoded[0]
	if got.ID != view.ID || got.Name != view.Name || got.Transport != view.Transport || got.Enabled != view.Enabled {
		t.Errorf("view identity mismatch: got %+v want %+v", got, view)
	}
	if !got.Status.Connected || got.Status.ToolCount != 3 || got.Status.ID != "srv-1" {
		t.Errorf("view status mismatch: got %+v", got.Status)
	}

	if got := decodeMcpServerViews("not-a-slice"); got != nil {
		t.Errorf("non-slice input → expected nil, got %+v", got)
	}
	if got := decodeMcpServerViews([]any{}); len(got) != 0 {
		t.Errorf("empty slice → expected 0 views, got %d", len(got))
	}
}
