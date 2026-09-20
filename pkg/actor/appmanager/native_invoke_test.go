package appmanager

import (
	"context"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

type invokeTestPlanner struct {
	onCall func(callID string, payload any) (any, error)
}

func (p invokeTestPlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, nil
}
func (p invokeTestPlanner) Call(_ context.Context, _ ref.Ref, callID string, payload any) *promise.Promise[any] {
	value, err := p.onCall(callID, payload)
	if err != nil {
		return promise.Reject[any](err)
	}
	return promise.Resolve[any](value)
}
func (p invokeTestPlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return nil
}

func TestInvokeNativeForwardsTimeoutMsFromManifest(t *testing.T) {
	a := &Actor{}
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	var captured gen.PluginInvokeReq
	ctx.PlannerFn = func() actor.Planner {
		return invokeTestPlanner{onCall: func(callID string, payload any) (any, error) {
			if callID != "pluginhost.invoke" {
				t.Fatalf("unexpected callID %q", callID)
			}
			captured = payload.(gen.PluginInvokeReq)
			return gen.PluginInvokeResp{Payload: []byte("ok")}, nil
		}}
	}

	callable := gen.AppCallableDescriptor{ID: "slow.call", TimeoutMs: 90_000}
	_, err := a.invokeNative(ctx, pluginRef, gen.AppManagerInvokeReq{ID: "app", Callable: "slow.call", Payload: []byte(`{}`)}, callerIdentity{}, callable)
	if err != nil {
		t.Fatalf("invokeNative: %v", err)
	}

	if captured.TimeoutMs != 90_000 {
		t.Fatalf("TimeoutMs = %d, want 90000", captured.TimeoutMs)
	}
}

func TestInvokeNativeKeepsDefaultTimeoutWhenNotDeclared(t *testing.T) {
	a := &Actor{}
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	var captured gen.PluginInvokeReq
	ctx.PlannerFn = func() actor.Planner {
		return invokeTestPlanner{onCall: func(callID string, payload any) (any, error) {
			if callID != "pluginhost.invoke" {
				t.Fatalf("unexpected callID %q", callID)
			}
			captured = payload.(gen.PluginInvokeReq)
			return gen.PluginInvokeResp{Payload: []byte("ok")}, nil
		}}
	}

	_, err := a.invokeNative(ctx, pluginRef, gen.AppManagerInvokeReq{ID: "app", Callable: "fast.call", Payload: []byte(`{}`)}, callerIdentity{}, gen.AppCallableDescriptor{ID: "fast.call"})
	if err != nil {
		t.Fatalf("invokeNative: %v", err)
	}

	want := int64(nativeInvokeTimeout / time.Millisecond)
	if captured.TimeoutMs != want {
		t.Fatalf("TimeoutMs = %d, want %d", captured.TimeoutMs, want)
	}
}

func TestInvokeNativeClampsShortDeclaredTimeoutUpToDefault(t *testing.T) {
	a := &Actor{}
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	var captured gen.PluginInvokeReq
	ctx.PlannerFn = func() actor.Planner {
		return invokeTestPlanner{onCall: func(callID string, payload any) (any, error) {
			if callID != "pluginhost.invoke" {
				t.Fatalf("unexpected callID %q", callID)
			}
			captured = payload.(gen.PluginInvokeReq)
			return gen.PluginInvokeResp{Payload: []byte("ok")}, nil
		}}
	}

	callable := gen.AppCallableDescriptor{ID: "medium.call", TimeoutMs: 5_000}
	_, err := a.invokeNative(ctx, pluginRef, gen.AppManagerInvokeReq{ID: "app", Callable: "medium.call", Payload: []byte(`{}`)}, callerIdentity{}, callable)
	if err != nil {
		t.Fatalf("invokeNative: %v", err)
	}

	want := int64(nativeInvokeTimeout / time.Millisecond)
	if captured.TimeoutMs != want {
		t.Fatalf("TimeoutMs = %d, want %d", captured.TimeoutMs, want)
	}
}
