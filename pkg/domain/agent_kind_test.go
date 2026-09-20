package domain

import "testing"

func TestDreamerAgentKindIsValidAndInternal(t *testing.T) {
	if err := ValidateAgentKind(AgentKindDreamer); err != nil {
		t.Fatal(err)
	}
	for _, kind := range ValidAgentKinds() {
		if kind.Kind == AgentKindDreamer {
			if kind.UserCreatable || !kind.SystemManaged {
				t.Fatalf("unexpected dreamer policy: %#v", kind)
			}
			return
		}
	}
	t.Fatal("dreamer kind missing from valid kinds")
}

func TestReviewerAgentKindIsValid(t *testing.T) {
	if err := ValidateAgentKind(AgentKindReviewer); err != nil {
		t.Fatal(err)
	}
	for _, kind := range ValidAgentKinds() {
		if kind.Kind == AgentKindReviewer {
			return
		}
	}
	t.Fatal("reviewer kind missing from valid kinds")
}

func TestScoutAgentKindIsValidAndSystemManaged(t *testing.T) {
	if err := ValidateAgentKind(AgentKindScout); err != nil {
		t.Fatal(err)
	}
	for _, kind := range ValidAgentKinds() {
		if kind.Kind == AgentKindScout {
			if kind.UserCreatable || !kind.SystemManaged {
				t.Fatalf("unexpected scout policy: %#v", kind)
			}
			if kind.DisplayName != "Scout" {
				t.Fatalf("scout DisplayName = %q, want Scout", kind.DisplayName)
			}
			return
		}
	}
	t.Fatal("scout kind missing from valid kinds")
}

func TestAgentKindDisplayName(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{AgentKindCoder, "Coder"},
		{AgentKindCoordinator, "Coordinator"},
		{"explorer", "Explorer"},
		{"", ""},
		{"unknown-kind", "unknown-kind"},
		// architect is retired; display name falls back to raw kind
		{AgentKindArchitect, "architect"},
	}
	for _, c := range cases {
		if got := AgentKindDisplayName(c.kind); got != c.want {
			t.Errorf("AgentKindDisplayName(%q) = %q, want %q", c.kind, got, c.want)
		}
	}
}
