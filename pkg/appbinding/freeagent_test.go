package appbinding

import "testing"

// FreeAgentPolicy has no explicit role field; the policy's allow-flags and
// AgentKinds set encode the privilege level of the bound agent. These tests
// model three privilege tiers — admin, user, and anonymous — as distinct
// policy configurations and verify create/switch/message are allowed or denied
// accordingly. The "anonymous" tier (a zero-value policy) must always deny.

func TestFreeAgentPolicyAdminAllowsAllActions(t *testing.T) {
	// admin: every action permitted, any agent kind.
	p := FreeAgentPolicy{AllowCreate: true, AllowSwitch: true, AllowMessage: true}
	for _, action := range []string{"create", "switch", "message"} {
		if err := p.Authorize(action, "coder"); err != nil {
			t.Fatalf("admin should allow %q: %v", action, err)
		}
		if !p.Allows(action, "reviewer") {
			t.Fatalf("admin should allow %q for any kind", action)
		}
	}
	// No AgentKinds restriction => any kind passes once the action is allowed.
	if err := p.Authorize("create", "oracle"); err != nil {
		t.Fatalf("admin should allow create for any kind: %v", err)
	}
}

func TestFreeAgentPolicyUserDeniesCreateAndEnforcesKinds(t *testing.T) {
	// user: switch + message only, restricted to the "coder" agent kind.
	p := FreeAgentPolicy{AllowSwitch: true, AllowMessage: true, AgentKinds: map[string]struct{}{"coder": {}}}

	// create is not permitted for users.
	if err := p.Authorize("create", "coder"); err == nil {
		t.Fatal("user must not create")
	}
	if code := DenialCode(p.Authorize("create", "coder")); code != CodeFreeAgentDenied {
		t.Fatalf("create denial code = %q, want %q", code, CodeFreeAgentDenied)
	}

	// switch + message are permitted for the allowed kind.
	if err := p.Authorize("switch", "coder"); err != nil {
		t.Fatalf("user should switch: %v", err)
	}
	if err := p.Authorize("message", "coder"); err != nil {
		t.Fatalf("user should message: %v", err)
	}

	// A disallowed agent kind is rejected even for an allowed action.
	if err := p.Authorize("switch", "oracle"); err == nil {
		t.Fatal("user switch with disallowed kind must be denied")
	}
	if code := DenialCode(p.Authorize("switch", "oracle")); code != CodeFreeAgentDenied {
		t.Fatalf("kind denial code = %q, want %q", code, CodeFreeAgentDenied)
	}
}

func TestFreeAgentPolicyAnonymousAlwaysDenied(t *testing.T) {
	// anonymous: a zero-value policy denies every action regardless of kind.
	var p FreeAgentPolicy
	for _, action := range []string{"create", "switch", "message"} {
		if err := p.Authorize(action, "coder"); err == nil {
			t.Fatalf("anonymous must deny %q", action)
		}
		// Even a privileged-sounding kind cannot escape a deny-all policy.
		if p.Allows(action, "admin") {
			t.Fatalf("anonymous must deny %q even for kind \"admin\"", action)
		}
		if code := DenialCode(p.Authorize(action, "")); code != CodeFreeAgentDenied {
			t.Fatalf("anonymous %q denial code = %q, want %q", action, code, CodeFreeAgentDenied)
		}
	}
	// Unknown actions are denied too.
	if err := p.Authorize("teleport", ""); err == nil {
		t.Fatal("anonymous must deny unknown action")
	}
}
