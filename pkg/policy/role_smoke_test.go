package policy

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
)

// hasPolicy reports whether rules contains a policy matching the given role,
// scope, and allow value.
func hasPolicy(t *testing.T, rules []actor.Policy, role, scope string, allow bool) bool {
	t.Helper()
	for _, r := range rules {
		if string(r.Role) == role && r.Scope == scope && r.Allow == allow {
			return true
		}
	}
	return false
}

// TestRoleSmoke_MatrixDeniesViewerAgentInvoke checks that the permission matrix
// translates into a PolicyStore deny rule covering the agent domain for viewers.
func TestRoleSmoke_MatrixDeniesViewerAgentInvoke(t *testing.T) {
	rules := MatrixDenyRules(seedMatrix())
	if !hasPolicy(t, rules, "viewer", "agent", false) {
		t.Error("expected viewer deny rule for scope 'agent' (covers agent.invoke)")
	}
}

// TestRoleSmoke_MatrixDeniesOperatorProjectWrite checks that operators are denied
// project.write and workspace.create_agent while retaining project.shell_exec.
func TestRoleSmoke_MatrixDeniesOperatorProjectWrite(t *testing.T) {
	rules := MatrixDenyRules(seedMatrix())
	for _, scope := range []string{"project.write", "workspace.create_agent"} {
		if !hasPolicy(t, rules, "operator", scope, false) {
			t.Errorf("expected operator deny rule for scope %q", scope)
		}
	}
	if hasPolicy(t, rules, "operator", "project.shell_exec", false) {
		t.Error("operator should not be denied project.shell_exec")
	}
}

// TestRoleSmoke_PredicatesDenyAnonymousAndViewer ensures that lower-tier roles
// cannot bypass the predicate checks used directly by handlers.
func TestRoleSmoke_PredicatesDenyAnonymousAndViewer(t *testing.T) {
	predicates := []struct {
		name string
		fn   func(id.Role) error
		// roles that must be denied; anonymous is the empty role.
		deny []id.Role
	}{
		{"RequireAdmin", RequireAdmin, []id.Role{"", "viewer", "operator", "developer"}},
		{"RequireDeveloper", RequireDeveloper, []id.Role{"", "viewer", "operator"}},
		{"RequireOperator", RequireOperator, []id.Role{"", "viewer"}},
		{"RequireAgentOrHuman", RequireAgentOrHuman, []id.Role{""}},
	}
	for _, tc := range predicates {
		for _, role := range tc.deny {
			if err := tc.fn(role); err == nil {
				t.Errorf("%s(%q) returned nil, want forbidden", tc.name, role)
			}
		}
	}
}

// TestRoleSmoke_SystemAndAgentAreInternal surfaces that process/agent roles
// remain allowed through the broad gates used by the agent tool surface.
func TestRoleSmoke_SystemAndAgentAreInternal(t *testing.T) {
	for _, role := range []id.Role{"system", "agent"} {
		if err := RequireAgentOrHuman(role); err != nil {
			t.Errorf("RequireAgentOrHuman(%q) returned %v, want allowed", role, err)
		}
		if err := RequireOperatorOrInternal(role); err != nil {
			t.Errorf("RequireOperatorOrInternal(%q) returned %v, want allowed", role, err)
		}
	}
}
