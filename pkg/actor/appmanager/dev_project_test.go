package appmanager

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// devProjectEnv wires a fake workspace service with one bound agent and a
// small project registry so resolveDevProjectID can be exercised end to end.
type devProjectEnv struct {
	t *testing.T
	// agents registered in the fake workspace; ActorID -> ProjectID.
	agents     []gen.AgentRef
	projects   []gen.ProjectRef
	wsActor    id.ActorID
	projectRef ref.Ref
}

func newDevProjectEnv(t *testing.T) *devProjectEnv {
	return &devProjectEnv{
		t:          t,
		wsActor:    testutil.GenActorID(),
		projectRef: testutil.NewFakeRef(testutil.GenActorID(), nil),
	}
}

func (e *devProjectEnv) ctx() *testutil.FakeCtx {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	wsRef := testutil.NewFakeRef(e.wsActor, nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return wsRef, true
	}
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			switch callID {
			case "workspace.list_agents":
				return gen.AgentRefListResp{Items: e.agents}, nil
			case "workspace.list_project":
				return gen.ProjectRefListResp{Items: e.projects}, nil
			}
			e.t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}}
	}
	return ctx
}

func TestResolveDevProjectIDDefaultsToCallerBoundProject(t *testing.T) {
	env := newDevProjectEnv(t)
	ctx := env.ctx()
	agentID := testutil.GenActorID().String()
	boundProject := "01a0517c0322000000000000000005bb"
	env.agents = append(env.agents, gen.AgentRef{ActorID: agentID, ProjectID: boundProject, DisplayName: "Code Snail"})

	got, err := resolveDevProjectID(ctx, "", agentID)
	if err != nil {
		t.Fatalf("resolveDevProjectID: %v", err)
	}
	if got != boundProject {
		t.Fatalf("resolved %q, want the agent's bound project %q", got, boundProject)
	}
}

func TestResolveDevProjectIDExplicitWins(t *testing.T) {
	env := newDevProjectEnv(t)
	ctx := env.ctx()
	got, err := resolveDevProjectID(ctx, " 01a0517c0322000000000000000005bb ", "")
	if err != nil || got != "01a0517c0322000000000000000005bb" {
		t.Fatalf("explicit = (%q, %v), want trimmed explicit id", got, err)
	}
}

func TestResolveDevProjectIDHumanCallerNeedsCandidates(t *testing.T) {
	env := newDevProjectEnv(t)
	ctx := env.ctx()
	env.projects = append(env.projects,
		gen.ProjectRef{Name: "sporecloud", ActorID: "01a0517c0322000000000000000005bb"},
		gen.ProjectRef{Name: "sporemind", ActorID: "019efa0960520000000000000000004e"},
	)
	_, err := resolveDevProjectID(ctx, "", "")
	if err == nil {
		t.Fatal("empty project id with no agent caller must fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "available projects:") || !strings.Contains(msg, "sporecloud id=01a0517c0322000000000000000005bb") {
		t.Fatalf("error must carry candidate projects, got: %s", msg)
	}
}

func TestResolveDevProjectIDUnknownAgentFailsWithHint(t *testing.T) {
	env := newDevProjectEnv(t)
	ctx := env.ctx()
	env.projects = append(env.projects, gen.ProjectRef{Name: "sporecloud", ActorID: "01a0517c0322000000000000000005bb"})
	_, err := resolveDevProjectID(ctx, "", "01deadbeef0000000000000000000bad")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown agent must fail with a not-found error, got %v", err)
	}
}
