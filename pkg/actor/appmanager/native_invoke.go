package appmanager

import (
	"context"
	"fmt"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// pluginhostServiceName is the registered gospore service name for the
// PluginHost actor. Plugins route invoke/reload/unregister through it.
const pluginhostServiceName = "pluginhost"

// nativeInvokeTimeout caps a single native plugin callable. Matches the
// PluginHost actor's own invoke timeout so both sides agree on the bound.
const nativeInvokeTimeout = 30 * time.Second

// invokeNative dispatches a callable to the pluginhost service for a loaded
// native plugin. The ref must point at the pluginhost actor. The response is
// the plugin's raw payload bytes (already unwrapped from the ABI frame by
// PluginHost). If the callable descriptor declares a TimeoutMs larger than the
// default nativeInvokeTimeout, that budget is forwarded to PluginHost so both
// sides agree on the bound.
func (a *Actor) invokeNative(ctx actor.PureContext, pluginRef ref.Ref, req gen.AppManagerInvokeReq, ci callerIdentity, callable gen.AppCallableDescriptor) ([]byte, error) {
	if pluginRef == nil {
		return nil, fmt.Errorf("appmanager: pluginhost ref is nil")
	}
	planner := ctx.Planner()
	if planner == nil {
		return nil, fmt.Errorf("appmanager: planner not available")
	}
	timeout := nativeInvokeTimeout
	if callable.TimeoutMs > 0 {
		declared := time.Duration(callable.TimeoutMs) * time.Millisecond
		if declared > timeout {
			timeout = declared
		}
	}
	// WithoutCancel detaches the declared budget from the caller's context
	// chain. Go contexts only narrow: if the calling lane's lifecycle carries
	// a deadline (agent turn budget, gateway invoke cap), WithTimeout would
	// min-cap the declared timeout to the caller's remaining time and a
	// low-budget lane would starve the plugin despite its appdef declaration.
	// The appdef TimeoutMs is authoritative here; the agent-side tool budget
	// (nativeToolTimeout, 15m) already bounds the caller's wait.
	invokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.Lifecycle()), timeout)
	defer cancel()
	value, err := planner.Call(invokeCtx, pluginRef, "pluginhost.invoke", gen.PluginInvokeReq{
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
	}).Await()
	if err != nil {
		return nil, fmt.Errorf("appmanager: native invoke %q: %w", req.Callable, err)
	}
	resp, ok := value.(gen.PluginInvokeResp)
	if !ok {
		return nil, fmt.Errorf("appmanager: native invoke returned unexpected response type")
	}
	return resp.Payload, nil
}
