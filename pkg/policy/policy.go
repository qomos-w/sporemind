// Package policy carries shared authorization predicates used across actors.
package policy

import (
	"fmt"
	"sort"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Role rank constants define the authorization ladder for recognized roles.
const (
	RoleRankUnknown   = 0
	RoleRankViewer    = 1
	RoleRankOperator  = 2
	RoleRankDeveloper = 3
	RoleRankAdmin     = 4
)

// RoleRank returns the authorization level for recognized roles.
// admin=4 > developer=3 > operator=2 > viewer=1 > unknown=0.
func RoleRank(role id.Role) int {
	switch role {
	case "admin":
		return RoleRankAdmin
	case "developer":
		return RoleRankDeveloper
	case "operator":
		return RoleRankOperator
	case "viewer":
		return RoleRankViewer
	default:
		return RoleRankUnknown
	}
}

// RequireAdmin allows only admin.
func RequireAdmin(role id.Role) error {
	switch role {
	case "admin":
		return nil
	}
	return fmt.Errorf("forbidden: requires admin role, got %q", role)
}

// RequireAdminOrInternal allows admin callers plus trusted internal
// actor-to-actor callers:
//   - "admin" — external session callers (gateway token auth)
//   - "" — zero-identity internal calls (plain refs carry no caller_role
//     header)
//   - "system" — the role gospore stamps on service-ref calls: a service ref
//     forwards its owning cell's Props role, and every sporemind top-level
//     actor is spawned with Role "system" (cmd/internal/actorset). Project
//     cells inherit it too (buildSpawn role inheritance), so internal
//     project→workspace calls such as the scheduler timer-fire path arrive
//     stamped "system" — see the sibling note on RequireAgentOrHuman.
//
// External non-admin roles (anonymous/guest/glass/peer) are denied. Used by
// callables that are admin-gated for the UI but must also be reachable from
// internal actors (e.g. workspace.agent_spawn_scheduler from the project
// timer execution loop).
func RequireAdminOrInternal(role id.Role) error {
	switch role {
	case "admin", "system", "":
		return nil
	}
	return fmt.Errorf("forbidden: requires admin role, got %q", role)
}

// RequireDeveloper allows admin and developer.
func RequireDeveloper(role id.Role) error {
	switch role {
	case "admin", "developer":
		return nil
	}
	return fmt.Errorf("forbidden: requires developer role or higher, got %q", role)
}

// RequireOperator allows admin, developer and operator.
func RequireOperator(role id.Role) error {
	switch role {
	case "admin", "developer", "operator":
		return nil
	}
	return fmt.Errorf("forbidden: requires operator role or higher, got %q", role)
}

// RequireAgentOrHuman allows the caller sets that may reach user-facing
// lifecycle callables such as workspace.create_agent:
//   - admin/developer — direct web UI calls (X-Role / JWT session)
//   - "system" — manager actors, AND project-scoped agents: gospore buildSpawn
//     role inheritance makes any actor spawned without an explicit role
//     inherit its parent cell's role, and project cells are spawned by the
//     workspace manager (Role "system"), so every project agent's turn-engine
//     tool call arrives stamped "system"
//   - "agent" — global agents (workspace.spawnGlobalAgent stamps RoleAgent)
//
// create_agent is deliberately part of the agent tool surface (bundle-declared
// tools, e.g. interface-controls); the per-agent reachability is enforced by
// the turn engine's card-scope audit, not by this role gate.
//
// Zero identity ("") is denied: gateway HTTP calls without X-Role resolve to
// a zero-identity context, and that path must not mint agents. Explicit
// external roles (anonymous/glass/peer) are denied as well.
func RequireAgentOrHuman(role id.Role) error {
	switch role {
	case "admin", "developer", "system", "agent":
		return nil
	}
	return fmt.Errorf("forbidden: requires agent or manager role, got %q", role)
}

// RequireDeveloperOrManager allows admin, developer and system. It is used by
// crawl-style callables that should accept manager actors and project agents
// (which arrive as system) but not global agents.
func RequireDeveloperOrManager(role id.Role) error {
	switch role {
	case "admin", "developer", "system":
		return nil
	}
	return fmt.Errorf("forbidden: requires developer or manager role, got %q", role)
}

// RequireOperatorOrInternal allows admin, developer, operator, system, agent
// and the empty role. It mirrors the public-facing human-level check used by
// project.shell_exec, where zero identity is permitted on the public surface.
func RequireOperatorOrInternal(role id.Role) error {
	switch role {
	case "", "admin", "developer", "operator", "system", "agent":
		return nil
	}
	return fmt.Errorf("forbidden: requires operator, internal or manager role, got %q", role)
}

// actionScopes maps matrix action names to the actor policy scopes that should
// be denied when the action is false for a role.
var actionScopes = map[string][]string{
	"workspace.mount":                  {"workspace.add_mount", "workspace.remove_mount"},
	"workspace.unmount":                {"workspace.remove_mount"},
	"workspace.create":                 {"workspace.create"},
	"workspace.create_agent":           {"workspace.create_agent"},
	"workspace.save_agent_kind_config": {"workspace.save_agent_kind_config"},
	"agent.invoke":                     {"agent"},
	"project.write": {
		"project.write",
		"project.edit",
		"project.rm",
		"project.wiki_create_card",
		"project.wiki_create_map",
		"project.wiki_create_task_card",
		"project.wiki_edit_card",
		"project.worktree_create",
		"project.workflow_create_worktree",
		"project.card_mount",
		"project.card_unmount",
	},
	"project.shell_exec": {"project.shell_exec"},
}

// MatrixDenyRules turns a permission matrix into explicit actor.Policy deny
// rules. For every action that is false for a role, it emits a deny Policy for
// each scope in actionScopes; actions not present in the map use their literal
// action string as the scope.
func MatrixDenyRules(matrix gen.PermissionMatrix) []actor.Policy {
	var rules []actor.Policy
	for _, entry := range matrix.Entries {
		// Iterate in a stable order so output is deterministic.
		actions := make([]string, 0, len(entry.Actions))
		for action := range entry.Actions {
			actions = append(actions, action)
		}
		sort.Strings(actions)
		for _, action := range actions {
			if entry.Actions[action] {
				continue
			}
			scopes, ok := actionScopes[action]
			if !ok {
				scopes = []string{action}
			}
			for _, scope := range scopes {
				rules = append(rules, actor.Policy{
					Scope: scope,
					Role:  actor.Role(entry.Role),
					Allow: false,
				})
			}
		}
	}
	return rules
}
