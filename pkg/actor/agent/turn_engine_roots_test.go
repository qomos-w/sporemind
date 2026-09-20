package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/util"
)

// captureLogger records Warn calls so tests can assert on degradation paths.
type captureLogger struct {
	warns []string
}

func (l *captureLogger) Debug(string, ...any) {}
func (l *captureLogger) Info(string, ...any)  {}
func (l *captureLogger) Warn(msg string, args ...any) {
	l.warns = append(l.warns, fmt.Sprintf(msg, args...))
}
func (l *captureLogger) Error(string, ...any) {}

// fakeProjectPlanner is a configurable actor.Planner stub for initRoots tests.
// result is returned from Call when err == nil; both fields control behavior.
type fakeProjectPlanner struct {
	result any
	err    error
	calls  int
}

func (p *fakeProjectPlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, errors.New("not implemented")
}

func (p *fakeProjectPlanner) Call(context.Context, ref.Ref, string, any) *promise.Promise[any] {
	p.calls++
	if p.err != nil {
		return promise.Reject[any](p.err)
	}
	return promise.Async(func(resolve func(any), reject func(any)) {
		resolve(p.result)
	})
}

func (p *fakeProjectPlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return promise.Reject[any](errors.New("not implemented"))
}

// mutatingTools returns a tool slice with at least one non-EffectNone entry,
// so initRoots proceeds past the read-only short-circuit.
func mutatingTools() []domain.ToolSpec {
	return []domain.ToolSpec{
		{Name: "read", CallableID: "project.read", EffectKind: string(domain.EffectNone)},
		{Name: "write", CallableID: "project.write", EffectKind: string(domain.EffectReversible)},
	}
}

// readOnlyTools returns a tool slice that is entirely EffectNone — the
// explorer child surface. initRoots must skip the planner call for this set.
func readOnlyTools() []domain.ToolSpec {
	return []domain.ToolSpec{
		{Name: "read", CallableID: "project.read", EffectKind: string(domain.EffectNone)},
		{Name: "glob", CallableID: "project.glob", EffectKind: string(domain.EffectNone)},
	}
}

func newInitRootsCtx(planner actor.Planner, lookupOk bool) *testutil.FakeCtx {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "project" || !lookupOk {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), nil), true
	}
	if planner != nil {
		p := planner
		ctx.PlannerFn = func() actor.Planner { return p }
	}
	return ctx
}

func TestInitRoots_LookupServiceMissing(t *testing.T) {
	planner := &fakeProjectPlanner{result: nil}
	log := &captureLogger{}
	e := &turnEngine{logger: log, allTools: mutatingTools()}

	ctx := newInitRootsCtx(planner, false)
	e.initRoots(ctx)

	if len(e.roots) != 0 {
		t.Errorf("expected no roots, got %v", e.roots)
	}
	if planner.calls != 0 {
		t.Errorf("planner should not be called when LookupService fails, got %d calls", planner.calls)
	}
}

func TestInitRoots_PlannerCallFails(t *testing.T) {
	planner := &fakeProjectPlanner{err: errors.New("project unreachable")}
	log := &captureLogger{}
	e := &turnEngine{logger: log, allTools: mutatingTools()}

	ctx := newInitRootsCtx(planner, true)
	e.initRoots(ctx)

	if len(e.roots) != 0 {
		t.Errorf("expected no roots on planner failure, got %v", e.roots)
	}
	if len(log.warns) == 0 {
		t.Errorf("expected warn log on planner failure")
	}
}

func TestInitRoots_CancelPropagatesToProjectInfo(t *testing.T) {
	planner := newBlockingPlanner()
	e := &turnEngine{
		done:     make(chan struct{}),
		logger:   newNopActorLogger(),
		allTools: mutatingTools(),
	}
	ctx := newInitRootsCtx(planner, true)
	done := make(chan struct{})
	go func() {
		e.initRoots(ctx)
		close(done)
	}()

	started := waitPlannerCall(t, planner, "project.info")
	e.cancel()
	select {
	case <-started.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("turn cancellation did not reach project.info context")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("initRoots remained blocked after engine cancellation")
	}
}

func TestInitRoots_ByteSlice(t *testing.T) {
	payload, _ := json.Marshal(domain.ProjectInfoResp{
		Roots: []gen.ProjectInfoRoot{
			{Path: "/workspace/main", Name: "main"},
			{Path: "/workspace/extra", Name: "extra"},
		},
	})
	planner := &fakeProjectPlanner{result: payload}
	log := &captureLogger{}
	e := &turnEngine{logger: log, allTools: mutatingTools()}

	ctx := newInitRootsCtx(planner, true)
	e.initRoots(ctx)

	want := []string{
		util.NormalizePath("/workspace/main"),
		util.NormalizePath("/workspace/extra"),
	}
	if len(e.roots) != len(want) {
		t.Fatalf("expected %d roots, got %v", len(want), e.roots)
	}
	for i := range want {
		if e.roots[i] != want[i] {
			t.Errorf("root[%d]: got %q, want %q", i, e.roots[i], want[i])
		}
	}
}

func TestInitRoots_TypedResult(t *testing.T) {
	planner := &fakeProjectPlanner{result: domain.ProjectInfoResp{
		Roots: []gen.ProjectInfoRoot{{Path: "/repo", Name: "primary"}},
	}}
	log := &captureLogger{}
	e := &turnEngine{logger: log, allTools: mutatingTools()}

	ctx := newInitRootsCtx(planner, true)
	e.initRoots(ctx)

	want := util.NormalizePath("/repo")
	if len(e.roots) != 1 || e.roots[0] != want {
		t.Errorf("expected [%q], got %v", want, e.roots)
	}
}

func TestInitRoots_MapResult(t *testing.T) {
	planner := &fakeProjectPlanner{result: map[string]interface{}{
		"Roots": []map[string]interface{}{
			{"Path": "/map/repo", "Name": "primary"},
		},
	}}
	log := &captureLogger{}
	e := &turnEngine{logger: log, allTools: mutatingTools()}

	ctx := newInitRootsCtx(planner, true)
	e.initRoots(ctx)

	want := util.NormalizePath("/map/repo")
	if len(e.roots) != 1 || e.roots[0] != want {
		t.Errorf("expected [%q], got %v", want, e.roots)
	}
}

func TestInitRoots_SkipsForReadOnlyTools(t *testing.T) {
	planner := &fakeProjectPlanner{result: domain.ProjectInfoResp{
		Roots: []gen.ProjectInfoRoot{{Path: "/never"}},
	}}
	log := &captureLogger{}
	e := &turnEngine{logger: log, allTools: readOnlyTools()}

	ctx := newInitRootsCtx(planner, true)
	e.initRoots(ctx)

	if len(e.roots) != 0 {
		t.Errorf("read-only agent should skip initRoots, got %v", e.roots)
	}
	if planner.calls != 0 {
		t.Errorf("planner should not be called for read-only agent, got %d calls", planner.calls)
	}
}
