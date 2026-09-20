package appbinding

import "testing"

func TestCapabilityBindingAuthorize(t *testing.T) {
	b := CapabilityBinding{AppID: "app", AgentID: "agent", Role: "coder", ProjectID: "project", Callables: map[string]struct{}{"agent_status": {}}}
	if err := b.Authorize("agent", "coder", "project", "agent_status"); err != nil {
		t.Fatal(err)
	}
	if err := b.Authorize("agent", "reviewer", "project", "agent_status"); err == nil {
		t.Fatal("expected role denial")
	}
	if err := b.Authorize("agent", "coder", "other", "agent_status"); err == nil {
		t.Fatal("expected project denial")
	}
	if err := b.Authorize("agent", "coder", "project", "agent_run"); err == nil {
		t.Fatal("expected callable denial")
	}
}

func TestFreeAgentPolicy(t *testing.T) {
	p := FreeAgentPolicy{AllowCreate: true, AgentKinds: map[string]struct{}{"coder": {}}}
	if !p.Allows("create", "coder") || p.Allows("create", "oracle") || p.Allows("switch", "coder") {
		t.Fatal("unexpected free-agent policy result")
	}
}
