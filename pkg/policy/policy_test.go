package policy

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestRoleRank(t *testing.T) {
	cases := []struct {
		role id.Role
		want int
	}{
		{"admin", RoleRankAdmin},
		{"developer", RoleRankDeveloper},
		{"operator", RoleRankOperator},
		{"viewer", RoleRankViewer},
		{"", RoleRankUnknown},
		{"anonymous", RoleRankUnknown},
		{"system", RoleRankUnknown},
		{"agent", RoleRankUnknown},
		{"glass", RoleRankUnknown},
		{"peer", RoleRankUnknown},
		{"unknown", RoleRankUnknown},
	}
	for _, c := range cases {
		if got := RoleRank(c.role); got != c.want {
			t.Errorf("RoleRank(%q) = %d; want %d", c.role, got, c.want)
		}
	}
}

func TestRequireAdmin(t *testing.T) {
	allowed := []id.Role{"admin"}
	denied := []id.Role{"", "anonymous", "glass", "peer", "agent", "system", "developer", "operator", "viewer", "unknown"}
	assertAllowed(t, "RequireAdmin", RequireAdmin, allowed)
	assertDenied(t, "RequireAdmin", RequireAdmin, denied)
}

func TestRequireDeveloper(t *testing.T) {
	allowed := []id.Role{"admin", "developer"}
	denied := []id.Role{"", "anonymous", "glass", "peer", "agent", "system", "operator", "viewer", "unknown"}
	assertAllowed(t, "RequireDeveloper", RequireDeveloper, allowed)
	assertDenied(t, "RequireDeveloper", RequireDeveloper, denied)
}

func TestRequireOperator(t *testing.T) {
	allowed := []id.Role{"admin", "developer", "operator"}
	denied := []id.Role{"", "anonymous", "glass", "peer", "agent", "system", "viewer", "unknown"}
	assertAllowed(t, "RequireOperator", RequireOperator, allowed)
	assertDenied(t, "RequireOperator", RequireOperator, denied)
}

func TestRequireAgentOrHuman(t *testing.T) {
	// developer (JWT-cast user role) and system/agent (internal actors) are allowed.
	allowed := []id.Role{"admin", "developer", "system", "agent"}
	denied := []id.Role{"", "anonymous", "glass", "peer", "operator", "viewer", "unknown"}
	assertAllowed(t, "RequireAgentOrHuman", RequireAgentOrHuman, allowed)
	assertDenied(t, "RequireAgentOrHuman", RequireAgentOrHuman, denied)
}

func TestRequireDeveloperOrManager(t *testing.T) {
	allowed := []id.Role{"admin", "developer", "system"}
	denied := []id.Role{"", "anonymous", "glass", "peer", "agent", "operator", "viewer", "unknown"}
	assertAllowed(t, "RequireDeveloperOrManager", RequireDeveloperOrManager, allowed)
	assertDenied(t, "RequireDeveloperOrManager", RequireDeveloperOrManager, denied)
}

func TestRequireOperatorOrInternal(t *testing.T) {
	allowed := []id.Role{"", "admin", "developer", "operator", "system", "agent"}
	denied := []id.Role{"anonymous", "glass", "peer", "viewer", "unknown"}
	assertAllowed(t, "RequireOperatorOrInternal", RequireOperatorOrInternal, allowed)
	assertDenied(t, "RequireOperatorOrInternal", RequireOperatorOrInternal, denied)
}

func assertAllowed(t *testing.T, name string, fn func(id.Role) error, roles []id.Role) {
	t.Helper()
	for _, role := range roles {
		if err := fn(role); err != nil {
			t.Errorf("%s(%q) returned %v; want nil", name, role, err)
		}
	}
}

func assertDenied(t *testing.T, name string, fn func(id.Role) error, roles []id.Role) {
	t.Helper()
	for _, role := range roles {
		if err := fn(role); err == nil {
			t.Errorf("%s(%q) returned nil; want forbidden", name, role)
		}
	}
}

func seedMatrix() gen.PermissionMatrix {
	allActions := []string{
		"workspace.mount",
		"workspace.unmount",
		"workspace.create",
		"workspace.create_agent",
		"workspace.save_agent_kind_config",
		"agent.invoke",
		"project.write",
		"project.shell_exec",
	}
	roles := []string{"admin", "developer", "viewer", "operator"}
	var entries []gen.PermissionEntry
	for _, role := range roles {
		actions := make(map[string]bool)
		for _, act := range allActions {
			granted := false
			switch role {
			case "admin":
				granted = true
			case "developer":
				granted = act != "workspace.mount" && act != "workspace.unmount"
			case "operator":
				granted = act == "agent.invoke" || act == "project.shell_exec"
			}
			actions[act] = granted
		}
		entries = append(entries, gen.PermissionEntry{Role: role, Actions: actions})
	}
	return gen.PermissionMatrix{Entries: entries}
}

func TestMatrixDenyRules(t *testing.T) {
	got := MatrixDenyRules(seedMatrix())
	want := []actor.Policy{
		// developer: denied workspace.mount, workspace.unmount
		{Scope: "workspace.add_mount", Role: actor.Role("developer"), Allow: false},
		{Scope: "workspace.remove_mount", Role: actor.Role("developer"), Allow: false},
		{Scope: "workspace.remove_mount", Role: actor.Role("developer"), Allow: false},

		// viewer: denied everything
		{Scope: "agent", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.shell_exec", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.write", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.edit", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.rm", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.wiki_create_card", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.wiki_create_map", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.wiki_create_task_card", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.wiki_edit_card", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.worktree_create", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.workflow_create_worktree", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.card_mount", Role: actor.Role("viewer"), Allow: false},
		{Scope: "project.card_unmount", Role: actor.Role("viewer"), Allow: false},
		{Scope: "workspace.create", Role: actor.Role("viewer"), Allow: false},
		{Scope: "workspace.create_agent", Role: actor.Role("viewer"), Allow: false},
		{Scope: "workspace.add_mount", Role: actor.Role("viewer"), Allow: false},
		{Scope: "workspace.remove_mount", Role: actor.Role("viewer"), Allow: false},
		{Scope: "workspace.save_agent_kind_config", Role: actor.Role("viewer"), Allow: false},
		{Scope: "workspace.remove_mount", Role: actor.Role("viewer"), Allow: false},

		// operator: denied everything except agent.invoke and project.shell_exec
		{Scope: "project.write", Role: actor.Role("operator"), Allow: false},
		{Scope: "project.edit", Role: actor.Role("operator"), Allow: false},
		{Scope: "project.rm", Role: actor.Role("operator"), Allow: false},
		{Scope: "project.wiki_create_card", Role: actor.Role("operator"), Allow: false},
		{Scope: "project.wiki_create_map", Role: actor.Role("operator"), Allow: false},
		{Scope: "project.wiki_create_task_card", Role: actor.Role("operator"), Allow: false},
		{Scope: "project.wiki_edit_card", Role: actor.Role("operator"), Allow: false},
		{Scope: "project.worktree_create", Role: actor.Role("operator"), Allow: false},
		{Scope: "project.workflow_create_worktree", Role: actor.Role("operator"), Allow: false},
		{Scope: "project.card_mount", Role: actor.Role("operator"), Allow: false},
		{Scope: "project.card_unmount", Role: actor.Role("operator"), Allow: false},
		{Scope: "workspace.create", Role: actor.Role("operator"), Allow: false},
		{Scope: "workspace.create_agent", Role: actor.Role("operator"), Allow: false},
		{Scope: "workspace.add_mount", Role: actor.Role("operator"), Allow: false},
		{Scope: "workspace.remove_mount", Role: actor.Role("operator"), Allow: false},
		{Scope: "workspace.save_agent_kind_config", Role: actor.Role("operator"), Allow: false},
		{Scope: "workspace.remove_mount", Role: actor.Role("operator"), Allow: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MatrixDenyRules mismatch:\ngot (%d): %+v\nwant (%d): %+v", len(got), got, len(want), want)
	}
}
