package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// parseAppToolCallable splits the synthetic app callable ID
// ("app.<appID>.<callable>") into its app and callable parts. The first
// segment must be "app". App IDs may themselves contain dots (e.g.
// "builtin.ssh"), which makes a pure leftmost split ambiguous: "app.builtin.ssh.list"
// could read appID "builtin" + callable "ssh.list" or appID "builtin.ssh" +
// callable "list". knownAppIDs carries the currently registered app IDs so
// the longest matching prefix wins, resolving dotted app IDs. When no known
// ID matches, the historical single-segment rule is preserved: appID is the
// second segment and the remainder (which may itself contain dots) is the
// callable.
func parseAppToolCallable(callableID string, knownAppIDs []string) (appID, callableName string, ok bool) {
	parts := strings.Split(callableID, ".")
	if len(parts) < 3 || parts[0] != "app" {
		return "", "", false
	}
	appID, callableName = parts[1], strings.Join(parts[2:], ".")
	// A registered app ID that is a strict prefix of callableID overrides the
	// naive first-segment split; longer prefixes win.
	for _, known := range knownAppIDs {
		if known == "" || len(known) <= len(appID) {
			continue
		}
		prefix := "app." + known + "."
		if len(prefix) < len(callableID) && strings.HasPrefix(callableID, prefix) {
			appID = known
			callableName = callableID[len(prefix):]
		}
	}
	return appID, callableName, true
}

// appIDsFromListResp extracts the IDs of running apps from an appmanager.list
// response — the same population resolveAppTools exposes as tool specs.
func appIDsFromListResp(resp gen.AppManagerListResp) []string {
	var ids []string
	for _, app := range resp.Items {
		if app.State == "running" {
			ids = append(ids, app.ID)
		}
	}
	return ids
}

// knownAppIDs returns the registered app IDs for the current turn, fetched
// once from appmanager.list and cached on the engine. It returns nil when the
// appmanager is unreachable or the query fails; callers then fall back to the
// naive parse. App calls are rare relative to other tools, so the one-time
// fetch at first app routing is acceptable.
func (e *turnEngine) knownAppIDs(ctx actor.Context, planner actor.Planner, svcRefs map[string]ref.Ref) []string {
	e.mu.RLock()
	cached := e.knownAppIDsCache
	e.mu.RUnlock()
	if cached != nil {
		return cached
	}
	svcRef, ok := svcRefs["appmanager"]
	if !ok {
		return nil
	}
	payload, _ := json.Marshal(gen.AppManagerListReq{})
	invokeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(invokeCtx, svcRef, "appmanager.list", payload).Await()
	if err != nil || result == nil {
		return nil
	}
	ids := []string{}
	switch v := result.(type) {
	case []byte:
		var resp gen.AppManagerListResp
		if err := json.Unmarshal(v, &resp); err != nil {
			return nil
		}
		ids = appIDsFromListResp(resp)
	case gen.AppManagerListResp:
		ids = appIDsFromListResp(v)
	case *gen.AppManagerListResp:
		if v == nil {
			return nil
		}
		ids = appIDsFromListResp(*v)
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		var resp gen.AppManagerListResp
		if json.Unmarshal(b, &resp) != nil {
			return nil
		}
		ids = appIDsFromListResp(resp)
	default:
		return nil
	}
	e.mu.Lock()
	if e.knownAppIDsCache == nil {
		e.knownAppIDsCache = ids
	}
	e.mu.Unlock()
	return e.knownAppIDsCache
}

// runAppToolCall routes a plugin tool invocation to appmanager.invoke and
// converts the opaque payload response into the tool frame text. The LLM's
// arguments (matching the app callable's request schema) are passed as the
// payload bytes; the response payload is decoded and returned as text.
func (e *turnEngine) runAppToolCall(ctx actor.Context, planner actor.Planner, svcRefs map[string]ref.Ref, call pendingToolCall, appID, callableName string) toolExecutionResult {
	svcRef, ok := svcRefs["appmanager"]
	if !ok {
		return toolExecutionResult{call: call, out: fmt.Sprintf("appmanager service not available for %q", call.LLMName), isErr: true}
	}

	// Encode LLM arguments as the payload bytes for appmanager.invoke.
	// AgentID is filled from the engine so resolveCaller's request-field
	// fallback identifies agent-originated invokes when the actor context
	// carries no gateway identity (external callers still hit the session
	// wall in handleInvoke). WorkspaceID carries the engine's workspace
	// actor id so bridge-routed llm.* calls inside the plugin can be
	// attributed in aistats.
	payload, err := json.Marshal(gen.AppManagerInvokeReq{
		ID:          appID,
		Callable:    callableName,
		AgentID:     e.agentID,
		WorkspaceID: e.workspaceID,
		Payload:     []byte(call.Input),
	})
	if err != nil {
		return toolExecutionResult{call: call, out: fmt.Sprintf("app tool %q: marshal request: %v", call.LLMName, err), isErr: true}
	}

	out, isErr, raw := callTool(ctx.Lifecycle(), planner, svcRef, "appmanager.invoke", string(payload))
	if !isErr {
		// Try to decode the AppManagerInvokeResp and extract payload text.
		var resp gen.AppManagerInvokeResp
		decoded := false
		switch v := raw.(type) {
		case gen.AppManagerInvokeResp:
			resp = v
			decoded = true
		case *gen.AppManagerInvokeResp:
			if v != nil {
				resp = *v
				decoded = true
			}
		case []byte:
			if len(v) > 0 {
				if err := json.Unmarshal(v, &resp); err == nil {
					decoded = true
				}
			}
		case map[string]interface{}:
			if b, err := json.Marshal(v); err == nil {
				if err := json.Unmarshal(b, &resp); err == nil {
					decoded = true
				}
			}
		}
		if decoded && len(resp.Payload) > 0 {
			out = string(resp.Payload)
		}
	}
	if !isErr && e.onToolExecuted != nil {
		e.onToolExecuted(ctx, call.CallableID, call.Input)
	}
	return toolExecutionResult{call: call, out: out, isErr: isErr}
}

// encodeAppManagerInvokeInput rewrites the LLM-facing appmanager-invoke tool
// input so the Payload field — a JSON object/array from the caller — becomes
// bytes the AppManagerInvokeReq handler can decode. The gospore handler
// decodes the request JSON into gen.AppManagerInvokeReq whose Payload is
// []byte; forwarding a raw object/array there fails json.Unmarshal ("cannot
// unmarshal object into ... of type []uint8"). Object/array payloads are
// serialized to their JSON bytes, an omitted/null payload becomes "{}",
// and a string payload (base64) keeps the original handler decoding semantics.
func encodeAppManagerInvokeInput(inputJSON string) (string, error) {
	var src struct {
		ID          string          `json:"Id"`
		Callable    string          `json:"Callable"`
		Payload     json.RawMessage `json:"Payload"`
		AgentID     string          `json:"AgentId"`
		Role        string          `json:"Role"`
		ProjectID   string          `json:"ProjectId"`
		WorkspaceID string          `json:"WorkspaceId"`
	}
	if err := json.Unmarshal([]byte(inputJSON), &src); err != nil {
		return "", err
	}

	var payload []byte
	trimmed := strings.TrimSpace(string(src.Payload))
	switch {
	case len(trimmed) == 0 || trimmed == "null":
		payload = []byte("{}")
	case trimmed[0] == '"':
		// A string Payload is base64 per the req schema; decode it the same way
		// the handler's json.Unmarshal into []byte would. But callers (agents,
		// tool descriptions) also pass JSON content as a string — "{}" or
		// "{\"Name\":\"x\"}" — which is not valid base64 and would fail here
		// before the request ever reaches the plugin. On base64 decode failure,
		// fall back to the string's literal bytes: JSON-text payloads then
		// arrive at the handler intact, and genuinely malformed input is left
		// for the plugin to reject with its own message.
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			var s string
			if jerr := json.Unmarshal([]byte(trimmed), &s); jerr != nil {
				return "", jerr
			}
			payload = []byte(s)
		}
	default:
		// JSON object, array, or scalar: serialize its literal bytes.
		payload = []byte(trimmed)
	}

	// Carry the identity fields through the rewrite. injectCallerAgentID
	// stamps AgentId into the input BEFORE this encoder runs; dropping it
	// here strips the authoritative agent identity and the handler denies
	// the invoke with identity_incomplete (observed live: even an explicit
	// AgentId tool argument never reached resolveCaller). Role/ProjectId
	// ride along the same way.
	req := gen.AppManagerInvokeReq{
		ID:          src.ID,
		Callable:    src.Callable,
		Payload:     payload,
		AgentID:     src.AgentID,
		Role:        src.Role,
		ProjectID:   src.ProjectID,
		WorkspaceID: src.WorkspaceID,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
