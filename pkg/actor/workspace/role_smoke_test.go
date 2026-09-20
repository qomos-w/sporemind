package workspace

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// roleCtx returns a fresh FakeCtx that carries the given role and shares the
// actor's test harness (spawn/lookup) so handlers can be invoked as if they
// came from a caller with that role.
func roleCtx(t *testing.T, a *Actor, role string) actor.PureContext {
	t.Helper()
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.Identity_.Role = id.Role(role)
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK
	return ctx
}

// addTestProject mounts a non-system project so coder agents can be created
// without invoking the real project spawn path.
func addTestProject(t *testing.T, a *Actor) string {
	t.Helper()
	projectID := "01a05555000000000000000000000001"
	a.Mounts = append(a.Mounts, domain.ProjectRef{
		ActorID: projectID,
		Name:    "test-project",
		Path:    t.TempDir(),
		Root:    false,
		System:  false,
	})
	return projectID
}

// TestRoleSmoke_DeveloperCanCreateAgent verifies that workspace.create_agent
// is open to the developer role.
func TestRoleSmoke_DeveloperCanCreateAgent(t *testing.T) {
	a, _ := freshActor(t)
	projectID := addTestProject(t, a)
	ctx := roleCtx(t, a, "developer")

	_, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{AgentKind: domain.AgentKindCoder, ProjectID: projectID})
	if err != nil {
		t.Fatalf("developer create_agent: expected success, got %v", err)
	}
}

// TestRoleSmoke_AnonymousCannotCreateAgent verifies that the anonymous/zero
// identity cannot create agents.
func TestRoleSmoke_AnonymousCannotCreateAgent(t *testing.T) {
	a, _ := freshActor(t)
	projectID := addTestProject(t, a)
	ctx := roleCtx(t, a, "")

	_, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{AgentKind: domain.AgentKindCoder, ProjectID: projectID})
	if err == nil || !strings.Contains(err.Error(), "agent or manager role") {
		t.Fatalf("anonymous create_agent: expected agent-or-manager denial, got %v", err)
	}
}

// TestRoleSmoke_DeveloperCannotAddMount verifies that workspace.add_mount is
// admin-only and rejects developer callers.
func TestRoleSmoke_DeveloperCannotAddMount(t *testing.T) {
	a, _ := freshActor(t)
	ctx := roleCtx(t, a, "developer")

	_, err := a.handleAddMount(ctx, domain.WorkspaceAddMountReq{ProjectID: "p", MountName: "m", MountPath: "/tmp/m"})
	if err == nil || !strings.Contains(err.Error(), "requires admin role") {
		t.Fatalf("developer add_mount: expected admin-only denial, got %v", err)
	}
}

// TestRoleSmoke_DeveloperCannotUpdateAgent verifies that workspace.update_agent
// is admin-only and rejects developer callers.
func TestRoleSmoke_DeveloperCannotUpdateAgent(t *testing.T) {
	a, adminCtx := freshActor(t)
	projectID := addTestProject(t, a)
	// Seed an agent as admin so the test isolates the role gate from the
	// not-found path.
	ag, err := a.handleCreateAgent(adminCtx, domain.WorkspaceCreateAgentReq{AgentKind: domain.AgentKindCoder, ProjectID: projectID})
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	ctx := roleCtx(t, a, "developer")
	_, err = a.handleUpdateAgent(ctx, domain.WorkspaceUpdateAgentReq{AgentID: ag.ID, DisplayName: "hacked"})
	if err == nil || !strings.Contains(err.Error(), "requires admin role") {
		t.Fatalf("developer update_agent: expected admin-only denial, got %v", err)
	}
}

// TestRoleSmoke_AgentCreateAgentRegression verifies that internal actor roles
// ("system" and "agent") retain access to the agent tool surface.
func TestRoleSmoke_AgentCreateAgentRegression(t *testing.T) {
	a, _ := freshActor(t)
	for i, role := range []string{"system", "agent"} {
		ctx := roleCtx(t, a, role)
		projectID := addTestProject(t, a)
		_, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{AgentKind: domain.AgentKindCoder, ProjectID: projectID})
		if err != nil {
			t.Fatalf("%s create_agent #%d: expected success, got %v", role, i, err)
		}
	}
}
