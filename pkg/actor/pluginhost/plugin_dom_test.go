package pluginhost

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// fakeDomPlanner mirrors agent tests' fakePlannerForInvoke: one-shot Call
// returning a canned result for interfacemanager.control.
type fakeDomPlanner struct {
	callFunc func(context.Context, ref.Ref, string, any) (any, error)
}

func (f fakeDomPlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, nil
}

func (f fakeDomPlanner) Call(ctx context.Context, target ref.Ref, callID string, payload any) *promise.Promise[any] {
	result, err := f.callFunc(ctx, target, callID, payload)
	if err != nil {
		return promise.Reject[any](err)
	}
	return promise.Resolve[any](result)
}

func (fakeDomPlanner) Stream(_ context.Context, _ ref.Ref, _ string, _ any, _ func(any) error) *promise.Promise[any] {
	return nil
}

func newDomTestCtx(controlResp any, controlErr error) *testutil.FakeCtx {
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), name == "interfacemanager"
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakeDomPlanner{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID != "interfacemanager.control" {
					return nil, context.Canceled
				}
				if controlErr != nil {
					return nil, controlErr
				}
				if controlResp != nil {
					return controlResp, nil
				}
				return domain.InterfaceManagerControlResp{Accepted: true}, nil
			},
		}
	}
	return ctx
}

func TestPluginDomPutRoundTrip(t *testing.T) {
	a := &Actor{}
	push := gen.PluginDomPutReq{
		PluginID: "app.test",
		Snapshot: "div#root\n  button.btn1 \"Click\"",
		Ts:       time.Now().UnixMilli(),
	}
	if _, err := a.handlePluginDomPut(nil, push); err != nil {
		t.Fatalf("plugin_dom_put: %v", err)
	}
	got, ok := a.domSnapshot("app.test", time.UnixMilli(push.Ts-1))
	if !ok {
		t.Fatal("snapshot not found after put")
	}
	if got.Snapshot != push.Snapshot || got.Ts != push.Ts {
		t.Errorf("stored push mismatch: %+v", got)
	}

	// A push older than the poll floor is invisible (stale snapshot guard).
	if _, ok := a.domSnapshot("app.test", time.UnixMilli(push.Ts+1)); ok {
		t.Error("push older than floor should not match")
	}
}

func TestPluginDomPutRejectsOversizedSnapshot(t *testing.T) {
	a := &Actor{}
	big := strings.Repeat("x", domSnapshotMaxBytes+1)
	if _, err := a.handlePluginDomPut(nil, gen.PluginDomPutReq{PluginID: "app.test", Snapshot: big, Ts: time.Now().UnixMilli()}); err == nil {
		t.Fatal("oversized snapshot accepted")
	}
}

func TestPluginDomUnknownPlugin(t *testing.T) {
	a := &Actor{}
	resp, err := a.handlePluginDom(newDomTestCtx(nil, nil), gen.PluginDomReq{PluginID: "app.missing"})
	if err != nil {
		t.Fatalf("plugin_dom: %v", err)
	}
	if resp.Found || resp.Reason != "plugin not found" {
		t.Errorf("want found=false reason=plugin not found, got %+v", resp)
	}
}

func TestPluginDomControlRejected(t *testing.T) {
	a := &Actor{}
	a.Plugins = []PluginDescriptor{{ID: "app.test"}}
	resp, err := a.handlePluginDom(newDomTestCtx(domain.InterfaceManagerControlResp{Accepted: false, Error: "no plugin panel mounted"}, nil), gen.PluginDomReq{PluginID: "app.test"})
	if err != nil {
		t.Fatalf("plugin_dom: %v", err)
	}
	if resp.Found || resp.Reason != "no plugin panel mounted" {
		t.Errorf("want rejected control path, got %+v", resp)
	}
}

func TestPluginDomTimeoutNoPush(t *testing.T) {
	a := &Actor{}
	a.Plugins = []PluginDescriptor{{ID: "app.test"}}
	// No push ever lands: the poll loop must exhaust its budget, not spin.
	resp, err := a.handlePluginDom(newDomTestCtx(nil, nil), gen.PluginDomReq{PluginID: "app.test"})
	if err != nil {
		t.Fatalf("plugin_dom: %v", err)
	}
	if resp.Found || resp.Reason != "no panel mounted or snapshot timed out" {
		t.Errorf("want timeout path, got %+v", resp)
	}
}
