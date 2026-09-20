package pluginhost

import (
	"encoding/json"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// mockTopologyProvider implements runtime.TopologyProvider for tests.
type mockTopologyProvider struct {
	snapshot []runtime.ActorNode
}

func (m *mockTopologyProvider) Snapshot() []runtime.ActorNode { return m.snapshot }
func (m *mockTopologyProvider) OnChange(func())               {}
func (m *mockTopologyProvider) UnifiedGraph() domain.UnifiedGraph {
	return domain.UnifiedGraph{}
}
func (m *mockTopologyProvider) CurrentEpoch() int32 { return 0 }
func (m *mockTopologyProvider) Sync(_ int32) domain.TopologySyncResp {
	return domain.TopologySyncResp{}
}
func (m *mockTopologyProvider) HistoryEntries() []domain.TopologyHistoryEntry { return nil }
func (m *mockTopologyProvider) OnEpochChange(func(int32, *domain.GraphPatch)) {}
func (m *mockTopologyProvider) EnablePush()                                   {}

// testSnapshot builds a topology snapshot with predictable callables for
// filter and pagination tests.
func testSnapshot() []runtime.ActorNode {
	return []runtime.ActorNode{
		{Kind: "workspace", Callables: []domain.CallableInterface{
			{Name: "workspace.list_agents", ServiceName: "workspace", Kind: "unary", Description: "List all agents", Permission: "public", EffectKind: "read", ReqSchemaID: 100, FinalSchemaID: 200},
			{Name: "workspace.create_agent", ServiceName: "workspace", Kind: "unary", Description: "Create a new agent", Permission: "admin", EffectKind: "mutate", ReqSchemaID: 101, FinalSchemaID: 201},
			{Name: "workspace.agent_send_message", ServiceName: "workspace", Kind: "unary", Description: "Send a message to an agent", Permission: "public", EffectKind: "mutate", ReqSchemaID: 102, FinalSchemaID: 202},
		}},
		{Kind: "oracle", Callables: []domain.CallableInterface{
			{Name: "oracle.search_services", ServiceName: "oracle", Kind: "unary", Description: "Search services by name", Permission: "public", EffectKind: "read", ReqSchemaID: 103, FinalSchemaID: 203},
			{Name: "oracle.inspect_actor", ServiceName: "oracle", Kind: "unary", Description: "Inspect actor state", Permission: "admin", EffectKind: "read", ReqSchemaID: 104, FinalSchemaID: 204},
		}},
		{Kind: "appmanager", Callables: []domain.CallableInterface{
			{Name: "appmanager.register_project", ServiceName: "appmanager", Kind: "unary", Description: "Register a plugin project", Permission: "admin", EffectKind: "mutate", ReqSchemaID: 105, FinalSchemaID: 205},
		}},
	}
}

// mustQuery exercises the real handleRegistryQuery against the given request,
// decoding through the appbinding wire types — the same JSON contract the
// SDK-facing host bridge and the generated CallRegistryQuery caller consume.
func mustQuery(t *testing.T, a *Actor, req appbinding.RegistryQueryReq) appbinding.RegistryQueryResp {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	resp, err := a.handleRegistryQuery(raw)
	if err != nil {
		t.Fatalf("handleRegistryQuery: %v", err)
	}
	var out appbinding.RegistryQueryResp
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	return out
}

func TestRegistryQuery_FullReturnBothEmpty(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{})
	if len(resp.Items) != 6 {
		t.Fatalf("expected 6 items (both filters empty), got %d", len(resp.Items))
	}
	if resp.NextCursor != "" {
		t.Fatalf("expected no next cursor when all fit, got %q", resp.NextCursor)
	}
	// Verify sort order (alphabetical by CallID).
	if resp.Items[0].CallID != "appmanager.register_project" {
		t.Fatalf("expected first item appmanager.register_project, got %s", resp.Items[0].CallID)
	}
}

func TestRegistryQuery_ServiceFilter_Prefix(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	// Prefix match on service name.
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{Service: "work"})
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 workspace items, got %d", len(resp.Items))
	}
	for _, item := range resp.Items {
		if item.Service != "workspace" {
			t.Fatalf("expected all items Service=workspace, got %q", item.Service)
		}
	}
}

func TestRegistryQuery_ServiceFilter_CaseInsensitive(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	// Case-insensitive match on service name.
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{Service: "ORACLE"})
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 oracle items (case-insensitive), got %d", len(resp.Items))
	}
	for _, item := range resp.Items {
		if item.Service != "oracle" {
			t.Fatalf("expected all items Service=oracle, got %q", item.Service)
		}
	}
}

func TestRegistryQuery_CallableFilter_Infix(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	// Infix match on the callID+Description haystack.
	// "message" appears in "workspace.agent_send_message" (CallID) and
	// "Send a message to an agent" (Description).
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{Callable: "message"})
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item matching 'message', got %d", len(resp.Items))
	}
	if resp.Items[0].CallID != "workspace.agent_send_message" {
		t.Fatalf("expected workspace.agent_send_message, got %s", resp.Items[0].CallID)
	}
}

func TestRegistryQuery_CallableFilter_DescriptionMatch(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	// "services" matches Description of oracle.search_services.
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{Callable: "services"})
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item matching 'services', got %d", len(resp.Items))
	}
	if resp.Items[0].CallID != "oracle.search_services" {
		t.Fatalf("expected oracle.search_services, got %s", resp.Items[0].CallID)
	}
}

func TestRegistryQuery_CallableFilter_CaseInsensitive(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	// Case-insensitive: "AGENT" should match CallIDs containing "agent".
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{Callable: "AGENT"})
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 items matching 'AGENT' (case-insensitive), got %d", len(resp.Items))
	}
}

func TestRegistryQuery_ServiceAndCallableCombined(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	// Both filters apply (AND).
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{Service: "workspace", Callable: "create"})
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item (workspace + create), got %d", len(resp.Items))
	}
	if resp.Items[0].CallID != "workspace.create_agent" {
		t.Fatalf("expected workspace.create_agent, got %s", resp.Items[0].CallID)
	}
}

func TestRegistryQuery_CursorContinuation(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	// Page 1: limit=2, no cursor.
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{Limit: 2})
	if len(resp.Items) != 2 {
		t.Fatalf("page 1: expected 2 items, got %d", len(resp.Items))
	}
	if resp.NextCursor == "" {
		t.Fatal("page 1: expected non-empty NextCursor")
	}

	// Page 2: use cursor from page 1.
	resp2 := mustQuery(t, a, appbinding.RegistryQueryReq{Limit: 2, Cursor: resp.NextCursor})
	if len(resp2.Items) != 2 {
		t.Fatalf("page 2: expected 2 items, got %d", len(resp2.Items))
	}
	// Verify no overlap: page 2 first item must differ from page 1 last item.
	if resp2.Items[0].CallID == resp.Items[1].CallID {
		t.Fatalf("page 2: first item %s must not equal page 1 last item %s",
			resp2.Items[0].CallID, resp.Items[1].CallID)
	}

	// Page 3: remaining 2 items (6 total, 4 already returned).
	resp3 := mustQuery(t, a, appbinding.RegistryQueryReq{Limit: 2, Cursor: resp2.NextCursor})
	if len(resp3.Items) != 2 {
		t.Fatalf("page 3: expected 2 items, got %d", len(resp3.Items))
	}
	// Page 3 should be the last page (offset=4, 6 items total, limit=2 →
	// items[4:6] = 2 items, end=6 == len, no next cursor).
	if resp3.NextCursor != "" {
		t.Fatalf("page 3: expected no NextCursor (last page), got %q", resp3.NextCursor)
	}

	// Verify all 6 unique items across the three pages.
	seen := map[string]bool{}
	for _, item := range append(append(resp.Items, resp2.Items...), resp3.Items...) {
		if seen[item.CallID] {
			t.Fatalf("duplicate item across pages: %s", item.CallID)
		}
		seen[item.CallID] = true
	}
	if len(seen) != 6 {
		t.Fatalf("expected 6 unique items across pages, got %d", len(seen))
	}

	// Verify alphabetical order across pages (the sort is stable).
	var allCallIDs []string
	for _, item := range append(append(resp.Items, resp2.Items...), resp3.Items...) {
		allCallIDs = append(allCallIDs, item.CallID)
	}
	for i := 1; i < len(allCallIDs); i++ {
		if allCallIDs[i] < allCallIDs[i-1] {
			t.Fatalf("items not in alphabetical order: %s before %s",
				allCallIDs[i-1], allCallIDs[i])
		}
	}
}

func TestRegistryQuery_LimitBoundary(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}

	// Limit larger than total: return all, no next cursor.
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{Limit: 100})
	if len(resp.Items) != 6 {
		t.Fatalf("limit=100: expected 6 items, got %d", len(resp.Items))
	}
	if resp.NextCursor != "" {
		t.Fatalf("limit=100: expected no NextCursor, got %q", resp.NextCursor)
	}

	// Limit exactly equal to total: return all, no next cursor.
	resp = mustQuery(t, a, appbinding.RegistryQueryReq{Limit: 6})
	if len(resp.Items) != 6 {
		t.Fatalf("limit=6: expected 6 items, got %d", len(resp.Items))
	}
	if resp.NextCursor != "" {
		t.Fatalf("limit=6: expected no NextCursor, got %q", resp.NextCursor)
	}

	// Limit = 1: first page has 1 item, next cursor set.
	resp = mustQuery(t, a, appbinding.RegistryQueryReq{Limit: 1})
	if len(resp.Items) != 1 {
		t.Fatalf("limit=1: expected 1 item, got %d", len(resp.Items))
	}
	if resp.NextCursor == "" {
		t.Fatal("limit=1: expected non-empty NextCursor")
	}
}

func TestRegistryQuery_DefaultLimit(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	// Limit=0 (unset) should default to 200, returning all 6 items.
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{})
	if len(resp.Items) != 6 {
		t.Fatalf("default limit: expected 6 items, got %d", len(resp.Items))
	}
	// Negative limit behaves like unset: same default, not an error.
	resp = mustQuery(t, a, appbinding.RegistryQueryReq{Limit: -3})
	if len(resp.Items) != 6 {
		t.Fatalf("negative limit: expected 6 items (default), got %d", len(resp.Items))
	}
}

func TestRegistryQuery_MetaFields(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{Callable: "create"})
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	m := resp.Items[0]
	if m.CallID != "workspace.create_agent" {
		t.Fatalf("CallID: got %q", m.CallID)
	}
	if m.Service != "workspace" {
		t.Fatalf("Service: got %q", m.Service)
	}
	if m.Kind != "unary" {
		t.Fatalf("Kind: got %q", m.Kind)
	}
	if m.Permission != "admin" {
		t.Fatalf("Permission: got %q", m.Permission)
	}
	if m.EffectKind != "mutate" {
		t.Fatalf("EffectKind: got %q", m.EffectKind)
	}
	if m.Description != "Create a new agent" {
		t.Fatalf("Description: got %q", m.Description)
	}
	if m.ReqSchemaId != 101 {
		t.Fatalf("ReqSchemaId: got %d", m.ReqSchemaId)
	}
	if m.RespSchemaId != 201 {
		t.Fatalf("RespSchemaId: got %d", m.RespSchemaId)
	}
	if m.FinalType != "" {
		t.Fatalf("FinalType: expected empty, got %q", m.FinalType)
	}
}

func TestRegistryQuery_NoTopology(t *testing.T) {
	// When topo is nil (e.g. before OnStart), return empty list, no error.
	a := &Actor{}
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{})
	if len(resp.Items) != 0 {
		t.Fatalf("expected 0 items with nil topo, got %d", len(resp.Items))
	}
}

func TestRegistryQuery_InvalidCursor(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	// Invalid cursor string should fall back to offset 0.
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{Cursor: "not-a-number", Limit: 2})
	if len(resp.Items) != 2 {
		t.Fatalf("invalid cursor: expected 2 items (fallback offset 0), got %d", len(resp.Items))
	}
}

func TestRegistryQuery_CursorBeyondEnd(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	// Cursor beyond the list should return empty, no next cursor.
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{Cursor: "999"})
	if len(resp.Items) != 0 {
		t.Fatalf("cursor beyond end: expected 0 items, got %d", len(resp.Items))
	}
	if resp.NextCursor != "" {
		t.Fatalf("cursor beyond end: expected no NextCursor, got %q", resp.NextCursor)
	}
}

func TestRegistryQuery_NoCredentialValues(t *testing.T) {
	a := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	resp := mustQuery(t, a, appbinding.RegistryQueryReq{})
	// Verify the raw JSON response does not contain credential-like fields.
	raw, _ := json.Marshal(resp)
	s := string(raw)
	for _, cred := range []string{"token", "secret", "password", "apiKey", "auth"} {
		// Case-insensitive check.
		lower := toLower(s)
		if contains(lower, cred) {
			t.Fatalf("response contains credential-like field %q: %s", cred, s)
		}
	}
}

// toLower is a helper to avoid importing strings in this simple check.
func toLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}

// contains is a simple substring check.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
