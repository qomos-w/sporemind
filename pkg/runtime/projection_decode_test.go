package runtime

import (
	"testing"

	"github.com/qomos-w/gospore/projection"
)

func TestDecodeAgentRefs(t *testing.T) {
	// The gospore projection snapshotter converts []domain.AgentRef into
	// []any of projection.FieldSnapshot (map[string]any). Verify decodeAgentRefs
	// reconstructs the structs correctly. Wire keys are the schema-declared
	// public names (json tags, e.g. "Id"/"ActorId"), never lowerFirst(GoName).
	raw := []any{
		projection.FieldSnapshot{
			"Id":          "agent-1",
			"ActorId":     "actor-abc",
			"AgentKind":   "coder",
			"DisplayName": "My Coder",
		},
		projection.FieldSnapshot{
			"Id":        "agent-2",
			"ActorId":   "actor-def",
			"AgentKind": "architect",
		},
	}
	agents := decodeAgentRefs(raw)
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	if agents[0].ID != "agent-1" || agents[0].ActorID != "actor-abc" || agents[0].AgentKind != "coder" {
		t.Errorf("agent[0] mismatch: %+v", agents[0])
	}
	if agents[0].DisplayName != "My Coder" {
		t.Errorf("agent[0] DisplayName mismatch: %q", agents[0].DisplayName)
	}
	if agents[1].ID != "agent-2" || agents[1].ActorID != "actor-def" || agents[1].AgentKind != "architect" {
		t.Errorf("agent[1] mismatch: %+v", agents[1])
	}
}

func TestDecodeAgentRefs_Empty(t *testing.T) {
	if agents := decodeAgentRefs(nil); agents != nil {
		t.Errorf("expected nil for nil input, got %v", agents)
	}
	if agents := decodeAgentRefs("not a slice"); agents != nil {
		t.Errorf("expected nil for non-slice input, got %v", agents)
	}
}

func TestDecodeProjectRefs(t *testing.T) {
	raw := []any{
		projection.FieldSnapshot{
			"Name":    "my-project",
			"Path":    "/tmp/project",
			"ActorId": "actor-proj-1",
		},
	}
	mounts := decodeProjectRefs(raw)
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	if mounts[0].Name != "my-project" || mounts[0].Path != "/tmp/project" || mounts[0].ActorID != "actor-proj-1" {
		t.Errorf("mount mismatch: %+v", mounts[0])
	}
}

func TestDecodeProjectRefs_Empty(t *testing.T) {
	if mounts := decodeProjectRefs(nil); mounts != nil {
		t.Errorf("expected nil for nil input, got %v", mounts)
	}
}