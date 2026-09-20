package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestFailTurn_ContextCanceled_TransitionsToCancelled verifies that a
// cancellation surfacing as context.Canceled (the exact error returned by
// runDispatch / phaseDispatch when e.done closes mid-stream) transitions the
// engine to LoopCancelled, not LoopFailed — so the turn is committed as
// cancelled instead of a "context canceled" failure.
func TestFailTurn_ContextCanceled_TransitionsToCancelled(t *testing.T) {
	e := &turnEngine{turnID: "turn-1"}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	e.failTurn(ctx, "turn-1", context.Canceled)

	if e.loopState != LoopCancelled {
		t.Fatalf("loopState = %v, want LoopCancelled", e.loopState)
	}
	if e.turnError != "" {
		t.Fatalf("turnError = %q, want empty (cancel is not an error)", e.turnError)
	}
	delta := e.getDelta()
	if delta.Turn.State != "cancelled" {
		t.Fatalf("getDelta().Turn.State = %q, want cancelled", delta.Turn.State)
	}
	if delta.Turn.Error != "" {
		t.Fatalf("getDelta().Turn.Error = %q, want empty", delta.Turn.Error)
	}
}

// TestFailTurn_WrappedCanceled_TransitionsToCancelled verifies the
// errors.Is check handles wrapped context.Canceled.
func TestFailTurn_WrappedCanceled_TransitionsToCancelled(t *testing.T) {
	e := &turnEngine{turnID: "turn-1"}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	e.failTurn(ctx, "turn-1", fmt.Errorf("dispatch: %w", context.Canceled))

	if e.loopState != LoopCancelled {
		t.Fatalf("loopState = %v, want LoopCancelled", e.loopState)
	}
}

// TestFailTurn_RealError_TransitionsToFailed verifies a genuine (non-cancel)
// error still transitions to LoopFailed and records the error message.
func TestFailTurn_RealError_TransitionsToFailed(t *testing.T) {
	e := &turnEngine{turnID: "turn-1"}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	boom := errors.New("upstream 500")
	e.failTurn(ctx, "turn-1", boom)

	if e.loopState != LoopFailed {
		t.Fatalf("loopState = %v, want LoopFailed", e.loopState)
	}
	if e.turnError != "upstream 500" {
		t.Fatalf("turnError = %q, want %q", e.turnError, boom.Error())
	}
	delta := e.getDelta()
	if delta.Turn.State != "failed" {
		t.Fatalf("getDelta().Turn.State = %q, want failed", delta.Turn.State)
	}
}

type blockingPlannerCall struct {
	callID string
	ctx    context.Context
}

type blockingPlanner struct {
	started chan blockingPlannerCall
}

func newBlockingPlanner() *blockingPlanner {
	return &blockingPlanner{started: make(chan blockingPlannerCall, 4)}
}

func (*blockingPlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, errors.New("not implemented")
}

func (p *blockingPlanner) Call(ctx context.Context, _ ref.Ref, callID string, _ any) *promise.Promise[any] {
	return promise.Async(func(_ func(any), reject func(any)) {
		p.started <- blockingPlannerCall{callID: callID, ctx: ctx}
		<-ctx.Done()
		reject(ctx.Err())
	})
}

func (p *blockingPlanner) Stream(ctx context.Context, _ ref.Ref, callID string, _ any, _ func(any) error) *promise.Promise[any] {
	return promise.Async(func(_ func(any), reject func(any)) {
		p.started <- blockingPlannerCall{callID: callID, ctx: ctx}
		<-ctx.Done()
		reject(ctx.Err())
	})
}

func waitPlannerCall(t *testing.T, p *blockingPlanner, wantCallID string) blockingPlannerCall {
	t.Helper()
	select {
	case call := <-p.started:
		if call.callID != wantCallID {
			t.Fatalf("planner call = %q, want %q", call.callID, wantCallID)
		}
		return call
	case <-time.After(time.Second):
		t.Fatalf("planner call %q did not start", wantCallID)
		return blockingPlannerCall{}
	}
}

func TestTurnContext_InheritsActorLifecycleAndEngineCancel(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	e := &turnEngine{done: make(chan struct{})}
	turnCtx := e.turnContext(parent)
	cancelParent()
	select {
	case <-turnCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("agent lifecycle cancellation did not reach turn context")
	}

	e = &turnEngine{done: make(chan struct{})}
	turnCtx = e.turnContext(context.Background())
	e.cancel()
	select {
	case <-turnCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("engine cancellation did not reach turn context")
	}
}

func TestRunOneCall_CancelPropagatesToTurnIO(t *testing.T) {
	tests := []struct {
		name       string
		call       pendingToolCall
		wantCallID string
	}{
		{
			name: "file pre-read",
			call: pendingToolCall{
				ID: "tool-1", LLMName: "file_write", CallableID: "project.write", ServiceName: "project",
				Input: `{"Path":"a.txt","Content":"new"}`,
			},
			wantCallID: "project.read",
		},
		{
			name: "filesystem shell intercept",
			call: pendingToolCall{
				ID: "tool-1", LLMName: "shell_exec", CallableID: "shell.exec", ServiceName: "shell",
				Input: `{"Command":"grep","Args":["needle","a.txt"]}`,
			},
			wantCallID: "project.grep",
		},
		{
			name: "streaming shell",
			call: pendingToolCall{
				ID: "tool-1", LLMName: "shell_exec", CallableID: "shell.exec", ServiceName: "shell",
				Input: `{"Command":"go","Args":["test","./..."]}`,
			},
			wantCallID: "shell.exec",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planner := newBlockingPlanner()
			e := &turnEngine{
				done:             make(chan struct{}),
				logger:           newNopActorLogger(),
				openStepEvents:   make(map[string][]domain.StepEvent),
				acceptingInjects: true,
			}
			ctx := testutil.HumanCtx(testutil.GenActorID())
			svcRefs := map[string]ref.Ref{
				"project": testutil.NewFakeRef(testutil.GenActorID(), nil),
				"shell":   testutil.NewFakeRef(testutil.GenActorID(), nil),
			}

			resultCh := make(chan toolExecutionResult, 1)
			go func() {
				resultCh <- e.runOneCall(ctx, planner, tt.call, svcRefs, "step-1")
			}()

			started := waitPlannerCall(t, planner, tt.wantCallID)
			e.cancel()
			select {
			case <-started.ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("turn cancellation did not reach blocking I/O context")
			}
			select {
			case result := <-resultCh:
				if !result.isErr {
					t.Fatal("cancelled I/O returned success")
				}
			case <-time.After(time.Second):
				t.Fatal("tool call remained blocked after engine cancellation")
			}
		})
	}
}

type blockingIntentRef struct {
	actorID id.ActorID
	started chan context.Context
}

func (r *blockingIntentRef) ID() id.ActorID        { return r.actorID }
func (*blockingIntentRef) Service() (string, bool) { return "aiaggregator", true }
func (r *blockingIntentRef) Invoke(ctx context.Context, _ string, _ any, _ ...map[string]string) *invoke.Call {
	r.started <- ctx
	return invoke.NewCall(invoke.CallModeUnary, &blockingIntentStream{ctx: ctx})
}

type blockingIntentStream struct {
	ctx context.Context
}

func (s *blockingIntentStream) Recv() (any, error) {
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}
func (s *blockingIntentStream) RecvRaw() ([]byte, error) {
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}
func (*blockingIntentStream) Close() error { return nil }

func TestExecuteBypassApproval_CancelPropagatesToIntentCall(t *testing.T) {
	intentRef := &blockingIntentRef{actorID: testutil.GenActorID(), started: make(chan context.Context, 1)}
	e := &turnEngine{
		done:   make(chan struct{}),
		logger: newNopActorLogger(),
		resolveTargets: func(actor.PureContext, domain.ModelSlot) []dispatchTarget {
			return []dispatchTarget{{aggRef: intentRef}}
		},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	resultCh := make(chan bool, 1)
	go func() {
		allowed, _ := e.executeBypassApproval(ctx, toolExecutionBatch{calls: []pendingToolCall{{CallableID: "project.write"}}})
		resultCh <- allowed
	}()

	var callCtx context.Context
	select {
	case callCtx = <-intentRef.started:
	case <-time.After(time.Second):
		t.Fatal("permission intent call did not start")
	}
	e.cancel()
	select {
	case <-callCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("turn cancellation did not reach permission intent context")
	}
	select {
	case allowed := <-resultCh:
		if allowed {
			t.Fatal("cancelled permission intent call was allowed")
		}
	case <-time.After(time.Second):
		t.Fatal("permission intent call remained blocked after engine cancellation")
	}
}

var _ invoke.Stream = (*blockingIntentStream)(nil)
var _ ref.Ref = (*blockingIntentRef)(nil)
var _ actor.Planner = (*blockingPlanner)(nil)
