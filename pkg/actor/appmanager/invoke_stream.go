package appmanager

import (
	"context"
	"fmt"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleInvokeStream is the streaming sibling of handleInvoke: it enforces the
// identical authorization boundary (resolveInvokeAuth) and then proxies the
// invocation over the caller's stream. Plugins relay pluginhost.invoke_stream
// chunk-by-chunk (intermediate forward chunks plus the terminal chunk). Spore
// apps have no forward-chunk wire, so they degrade to the unary dispatch and
// emit exactly one Terminal=true chunk carrying the callable's response.
//
// It takes actor.PureContext (unlike the unary handleInvoke) so the call runs
// on the forked stateless goroutine: a long-lived stream must never block the
// appmanager_ops owner lane.
func (a *Actor) handleInvokeStream(ctx actor.PureContext, req gen.AppManagerInvokeReq, emit actor.Emitter) error {
	if emit == nil {
		return fmt.Errorf("appmanager: invoke_stream requires a streaming emitter")
	}
	resolved, err := a.resolveInvokeAuth(ctx, req)
	if err != nil {
		// The preamble has already recorded the denied audit entry.
		return err
	}
	manifest := resolved.manifest
	ci := resolved.ci
	callableDesc := resolved.callableDesc
	ref := resolved.ref
	// Dispatch through appbinding.Dispatcher so the actual call and audit
	// share one code path with stable codes, mirroring handleInvoke.
	dispatcher := appbinding.Dispatcher{
		Audit: a.auditRuntime(req.ID),
		Call: func(callCtx context.Context, callable string, payload any) (any, error) {
			raw, _ := payload.([]byte)
			invokeCtx, cancel := context.WithTimeout(callCtx, invokeTimeout)
			defer cancel()
			if manifest.Runtime == "native" {
				invokeReq := req
				invokeReq.Callable = callable
				invokeReq.Payload = raw
				return nil, a.invokeNativeStream(ctx, ref, invokeReq, ci, callableDesc, emit)
			}
			// Spore degrade: run the same unary dispatch as handleInvoke and
			// deliver a single terminal chunk. On error no chunk is emitted.
			appPayload, err := sporeAppInvoke(invokeCtx, ref, req, callable, raw, ci)
			if err != nil {
				return nil, err
			}
			respPayload, _ := appPayload.([]byte)
			if err := emit.Send(gen.PluginInvokeChunk{Payload: respPayload, Terminal: true}); err != nil {
				return nil, fmt.Errorf("appmanager: emit terminal chunk: %w", err)
			}
			return respPayload, nil
		},
	}
	_, err = dispatcher.Invoke(ctx.Lifecycle(), appbinding.DispatchContext{AppID: req.ID, AgentID: ci.AgentID, Role: ci.Role, ProjectID: ci.ProjectID, RequestID: req.RequestID, SessionID: req.SessionID, CallSeq: req.CallSeq}, req.Callable, req.Payload)
	return err
}

// invokeNativeStream is the streaming sibling of invokeNative: it forwards the
// callable to pluginhost.invoke_stream and re-emits each PluginInvokeChunk —
// intermediate forward chunks plus the final Terminal=true chunk — onto the
// caller's stream. Same timeout logic and PluginInvokeReq field copying
// (including TimeoutMs) as invokeNative, so both sides agree on the bound.
func (a *Actor) invokeNativeStream(ctx actor.PureContext, pluginRef ref.Ref, req gen.AppManagerInvokeReq, ci callerIdentity, callable gen.AppCallableDescriptor, emit actor.Emitter) error {
	if pluginRef == nil {
		return fmt.Errorf("appmanager: pluginhost ref is nil")
	}
	planner := ctx.Planner()
	if planner == nil {
		return fmt.Errorf("appmanager: planner not available")
	}
	timeout := nativeInvokeTimeout
	if callable.TimeoutMs > 0 {
		declared := time.Duration(callable.TimeoutMs) * time.Millisecond
		if declared > timeout {
			timeout = declared
		}
	}
	// Same WithoutCancel detach as invokeNative: the declared timeout is
	// authoritative, never min-capped by the caller lane's lifecycle budget.
	invokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.Lifecycle()), timeout)
	defer cancel()
	onChunk := func(value any) error {
		chunk, ok := value.(gen.PluginInvokeChunk)
		if !ok {
			return fmt.Errorf("appmanager: invoke_stream returned unexpected chunk type %T", value)
		}
		if err := emit.Send(gen.PluginInvokeChunk{Payload: chunk.Payload, Terminal: chunk.Terminal}); err != nil {
			return fmt.Errorf("appmanager: emit chunk: %w", err)
		}
		return nil
	}
	_, err := planner.Stream(invokeCtx, pluginRef, "pluginhost.invoke_stream", gen.PluginInvokeReq{
		ID:          req.ID,
		Callable:    req.Callable,
		Payload:     req.Payload,
		AgentID:     ci.AgentID,
		Role:        ci.Role,
		ProjectID:   ci.ProjectID,
		WorkspaceID: ci.WorkspaceID,
		RequestID:   req.RequestID,
		SessionID:   req.SessionID,
		CallSeq:     req.CallSeq,
		TimeoutMs:   timeout.Milliseconds(),
	}, onChunk).Await()
	if err != nil {
		return fmt.Errorf("appmanager: native invoke_stream %q: %w", req.Callable, err)
	}
	return nil
}
