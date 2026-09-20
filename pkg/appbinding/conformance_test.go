package appbinding

import "testing"

func TestSurfaceBindingRequiresEntrypoint(t *testing.T) {
	if err := (SurfaceBinding{AppID: "app", AgentID: "agent"}).Validate(); err == nil {
		t.Fatal("expected entrypoint validation error")
	}
}
