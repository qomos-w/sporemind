package appmanager

import (
	"context"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestDevGenerateRoutesWorktreeBoundCallerToWorktree: a worktree-bound agent
// calling dev_generate is no longer rejected — the pipeline routes at the
// caller's worktree path (boundCheckPlanner answers WorktreePath="/wt/dev"),
// so the codegen failure names /wt/dev instead of the main tree.
func TestDevGenerateRoutesWorktreeBoundCallerToWorktree(t *testing.T) {
	a := &Actor{actorID: "appmanager-test"}
	projectCID, cidErr := identity.NewCanonicalID(1700000000000, 1, 1, 60)
	if cidErr != nil {
		t.Fatalf("create project CID: %v", cidErr)
	}
	projectID := id.From(projectCID)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	projectRef := testutil.NewFakeRef(projectID, nil)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return projectRef, aid == projectID
	}
	ctx.PlannerFn = func() actor.Planner {
		return boundCheckPlanner{bound: true}
	}

	resp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID:     projectCID.String(),
		CallerAgentID: "01a000000000000000000000000000ff",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if resp.Error == "" || strings.Contains(resp.Error, "worktree-bound") {
		t.Fatalf("Error = %q, want a routed-to-worktree codegen failure, not a worktree rejection", resp.Error)
	}
	if !strings.Contains(resp.Error, "/wt/dev") {
		t.Fatalf("Error = %q, want it to name the worktree path /wt/dev (root resolution must route the bound caller)", resp.Error)
	}
}

// TestDevGateRoutesWorktreeBoundCallerToWorktree mirrors the routing for
// dev_gate: the .appdef listing must target the worktree path, and the gate
// must fail with a plain input error rather than a worktree rejection.
func TestDevGateRoutesWorktreeBoundCallerToWorktree(t *testing.T) {
	a := &Actor{actorID: "appmanager-test"}
	projectCID, cidErr := identity.NewCanonicalID(1700000000000, 1, 1, 60)
	if cidErr != nil {
		t.Fatalf("create project CID: %v", cidErr)
	}
	projectID := id.From(projectCID)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	projectRef := testutil.NewFakeRef(projectID, nil)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return projectRef, aid == projectID
	}
	route := &wtRouteRecorder{}
	ctx.PlannerFn = func() actor.Planner {
		return boundCheckPlanner{bound: true, route: route}
	}

	resp, err := a.handleDevGate(ctx, gen.AppManagerDevGateReq{
		ProjectID:     projectCID.String(),
		CallerAgentID: "01a000000000000000000000000000ff",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if resp.Error == "" || strings.Contains(resp.Error, "worktree-bound") {
		t.Fatalf("Error = %q, want a routed-to-worktree gate failure, not a worktree rejection", resp.Error)
	}
	if len(route.listPaths) == 0 || route.listPaths[0] != "/wt/dev" {
		t.Fatalf("project.list paths = %v, want first listing at the worktree root /wt/dev", route.listPaths)
	}
}

// TestDevGenerateAllowsUnboundCaller: empty CallerAgentID (human/internal
// caller) skips the bound check entirely — only agents can be worktree-bound.
func TestDevGenerateAllowsUnboundCaller(t *testing.T) {
	a := &Actor{actorID: "appmanager-test"}
	projectCID, cidErr := identity.NewCanonicalID(1700000000000, 1, 1, 60)
	if cidErr != nil {
		t.Fatalf("create project CID: %v", cidErr)
	}
	projectID := id.From(projectCID)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	projectRef := testutil.NewFakeRef(projectID, nil)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return projectRef, aid == projectID
	}
	var checkCalled bool
	ctx.PlannerFn = func() actor.Planner {
		return boundCheckPlanner{bound: false, called: &checkCalled}
	}

	resp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID: projectCID.String(),
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if resp.Error == "" || strings.Contains(resp.Error, "worktree-bound") {
		t.Fatalf("Error = %q, want a non-guard error (missing project files), not a worktree rejection", resp.Error)
	}
	if checkCalled {
		t.Fatal("bound check invoked despite empty CallerAgentID; human/internal callers must skip the check")
	}
}

// TestGeneratedManifestKeyWorktreeScope pins the worktree dimension of the
// drift-record key: unbound keys stay bit-identical with the legacy format,
// bound keys are prefixed with a stable hash of the worktree path so a
// worktree build can neither clobber nor satisfy the main tree's record.
func TestGeneratedManifestKeyWorktreeScope(t *testing.T) {
	if got := generatedManifestKey("proj", "", ""); got != "proj" {
		t.Fatalf("root unbound key = %q, want %q (legacy bit-identical)", got, "proj")
	}
	if got := generatedManifestKey("proj", "subdir", ""); got != "proj::subdir" {
		t.Fatalf("subdir unbound key = %q, want %q (legacy bit-identical)", got, "proj::subdir")
	}
	wt := generatedManifestKey("proj", "", "/wt/dev")
	if !strings.HasPrefix(wt, "wt") || !strings.Contains(wt, "::proj") {
		t.Fatalf("bound key = %q, want wt<hash>::proj form", wt)
	}
	if wt2 := generatedManifestKey("proj", "", "/wt/other"); wt2 == wt {
		t.Fatalf("different worktrees must produce different keys, both %q", wt)
	}
	if generatedManifestKey("proj", "subdir", "/wt/dev") != wt+"::subdir" {
		t.Fatalf("bound subdir key must append ::subdir to the bound root key")
	}
}

// wtRouteRecorder captures the paths appmanager passes to project.list and
// project.read_base64 so the routing tests can pin where reads landed.
type wtRouteRecorder struct {
	listPaths []string
	readPaths []string
}

// boundCheckPlanner answers only project.worktree_bound_check (recording the
// call), plus project.list / project.read_base64 path recording when a route
// recorder is present.
type boundCheckPlanner struct {
	bound  bool
	called *bool
	route  *wtRouteRecorder
}

func (p boundCheckPlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, nil
}
func (p boundCheckPlanner) Call(_ context.Context, _ ref.Ref, callID string, payload any) *promise.Promise[any] {
	switch callID {
	case "project.worktree_bound_check":
		if p.called != nil {
			*p.called = true
		}
		return promise.Resolve[any](gen.ProjectWorktreeBoundCheckResp{Bound: p.bound, WorktreePath: "/wt/dev"})
	case "project.list":
		if p.route != nil {
			if req, ok := payload.(gen.FileSystemListReq); ok {
				p.route.listPaths = append(p.route.listPaths, req.Path)
			}
			return promise.Resolve[any]("")
		}
		return promise.Reject[any](errUnexpectedPlannerCall{callID})
	case "project.read_base64":
		if p.route != nil {
			if req, ok := payload.(gen.FileSystemReadBase64Req); ok {
				p.route.readPaths = append(p.route.readPaths, req.Path)
			}
		}
		return promise.Reject[any](errUnexpectedPlannerCall{callID})
	default:
		return promise.Reject[any](errUnexpectedPlannerCall{callID})
	}
}
func (p boundCheckPlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return nil
}

type errUnexpectedPlannerCall struct{ callID string }

func (e errUnexpectedPlannerCall) Error() string { return "unexpected planner call: " + e.callID }
