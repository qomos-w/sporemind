package aimanager

import (
	"testing"
)

func TestProviderAssign_UpdatesAssignments(t *testing.T) {
	a := &Actor{assignments: make(map[string]string)}
	req := providerAssignReq{
		AgentID:      "agent-1",
		SlotKind:     "primary",
		ProviderName: "openai",
	}
	_, err := a.handleProviderAssign(nil, req)
	if err != nil {
		t.Fatalf("handleProviderAssign: %v", err)
	}
	key := "agent-1|primary"
	if a.assignments[key] != "openai" {
		t.Errorf("assignment not recorded: got %q", a.assignments[key])
	}
}

func TestProviderAssign_EmptyAgentIDIgnored(t *testing.T) {
	a := &Actor{assignments: make(map[string]string)}
	_, _ = a.handleProviderAssign(nil, providerAssignReq{ProviderName: "openai"})
	if len(a.assignments) != 0 {
		t.Errorf("expected no assignment with empty AgentID, got %d", len(a.assignments))
	}
}

func TestProviderAssign_OverwritesPreviousAssignment(t *testing.T) {
	a := &Actor{assignments: make(map[string]string)}
	a.handleProviderAssign(nil, providerAssignReq{
		AgentID: "agent-1", SlotKind: "primary", ProviderName: "openai",
	})
	a.handleProviderAssign(nil, providerAssignReq{
		AgentID: "agent-1", SlotKind: "primary", ProviderName: "anthropic",
	})
	if a.assignments["agent-1|primary"] != "anthropic" {
		t.Errorf("expected overwrite to anthropic, got %q", a.assignments["agent-1|primary"])
	}
}

func TestProviderAssignments_DerivesCounts(t *testing.T) {
	a := &Actor{assignments: map[string]string{
		"agent-1|primary": "openai",
		"agent-2|primary": "openai",
		"agent-3|primary": "anthropic",
	}}
	resp, err := a.handleProviderAssignments(nil)
	if err != nil {
		t.Fatalf("handleProviderAssignments: %v", err)
	}
	if resp.Counts["openai"] != 2 {
		t.Errorf("openai count = %d, want 2", resp.Counts["openai"])
	}
	if resp.Counts["anthropic"] != 1 {
		t.Errorf("anthropic count = %d, want 1", resp.Counts["anthropic"])
	}
}

func TestProviderAssignments_EmptyMap(t *testing.T) {
	a := &Actor{assignments: make(map[string]string)}
	resp, _ := a.handleProviderAssignments(nil)
	if resp.Counts == nil {
		t.Fatal("expected non-nil counts map")
	}
	if len(resp.Counts) != 0 {
		t.Errorf("expected empty counts, got %d", len(resp.Counts))
	}
}
