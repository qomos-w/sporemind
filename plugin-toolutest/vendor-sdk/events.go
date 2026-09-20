package sdk

import (
	"encoding/json"
	"fmt"
)

// ReservedEventPrefix namespaces host→plugin event delivery. The pluginhost
// subscribes the gospore event bus on the plugin's behalf (for the `listen`
// kinds declared in the app manifest) and forwards each matching event by
// invoking the plugin under this reserved callID — event delivery therefore
// reuses the callable dispatch path, both transports (process and c-shared),
// with zero ABI change. Appdef rejects callable IDs carrying this prefix so
// the namespace cannot be squatted by an app callable.
const ReservedEventPrefix = "__event__:"

// EventCallID returns the reserved dispatch name for a host event kind.
func EventCallID(kind string) string { return ReservedEventPrefix + kind }

// EventHandler receives the raw JSON payload of one host event delivery.
// Generated main.gen.go registers typed OnXxx handlers (handlers.go) by
// wrapping them with the decode step:
//
//	ctx.RegisterEventListener("app_lifecycle", func(payload json.RawMessage) error {
//	    var ev AppLifecycleEvent
//	    if err := json.Unmarshal(payload, &ev); err != nil { return err }
//	    return OnAppLifecycle(ev)
//	})
type EventHandler func(payload json.RawMessage) error

// RegisterEventListener registers an event listener for the active plugin.
// Mirrors RegisterCallable's convenience form; lifecycle contexts use the
// plugin-scoped method on Context.
func RegisterEventListener(kind string, h EventHandler) {
	pluginID := ""
	if state, err := currentState(); err == nil {
		pluginID = state.pluginID
	}
	registerEventListener(pluginID, kind, h)
}

// registerEventListener stores the listener in the callable registry under
// the reserved dispatch name; the host's reverse invoke lands in the normal
// dispatchRequest path. Unload teardown is inherited from
// unregisterPluginCallables.
//
// The wrapper also mirrors every host-delivered event onto the plugin's local
// SSE stream (GET /events) so panel frontends can subscribe to listen kinds
// the same way they subscribe to app-declared events (subscribeEvent /
// window.sporemind.subscribe) — no host round-trip, best-effort no-op when no
// HTTP listener is running. Broadcast happens before the Go handler runs, so
// a buggy handler cannot break the frontend stream.
func registerEventListener(pluginID, kind string, h EventHandler) {
	registerCallable(pluginID, EventCallID(kind), func(req Request) (Response, error) {
		broadcastSSE(kind, req.Payload)
		if h == nil {
			return Response{}, fmt.Errorf("event listener for %q is nil", kind)
		}
		return Response{}, h(req.Payload)
	})
}

// appEmitReq is the wire contract of the `app.emit` host call. Payload embeds
// the marshaled event payload verbatim.
type appEmitReq struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// appEmitResp is the `app.emit` host-call response.
type appEmitResp struct {
	Accepted bool `json:"Accepted"`
}

// EmitEvent publishes one app-declared event (`event <id> { payload: T }`
// blocks in .appdef) along two independent paths:
//
//   - Local SSE: the event is fanned out to every frontend connected to the
//     plugin's HTTP listener (GET /events), giving the iframe real-time
//     updates with no round-trip through the host. Best-effort; if no HTTP
//     listener is running this path is a no-op.
//   - Host bridge: the event is forwarded to the host via the `app.emit` host
//     call, unchanged. The host validates the event against the app manifest
//     and broadcasts it on the app_event bus kind, so apps listening to
//     app_event (including other apps) still receive it (cross-app visibility).
//
// Requires the app.emit capability — codegen derives it automatically when the
// appdef declares event blocks. Payload may be nil for payload-less events.
// A host-call failure is returned as an error; the local SSE push is
// fire-and-forget and never affects the return value.
func EmitEvent(kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("emit event %s: marshal payload: %w", kind, err)
	}
	// Local SSE path: push to connected frontends (best-effort, never fails).
	broadcastSSE(kind, raw)
	// Host bridge path: forward to the host bus (cross-app visibility).
	out, err := ActiveHost().Invoke("app.emit", appEmitReq{Event: kind, Payload: raw})
	if err != nil {
		return fmt.Errorf("emit event %s: %w", kind, err)
	}
	var resp appEmitResp
	if err := json.Unmarshal(out, &resp); err != nil {
		return fmt.Errorf("emit event %s: decode response: %w", kind, err)
	}
	if !resp.Accepted {
		return fmt.Errorf("emit event %s: host did not accept the event", kind)
	}
	return nil
}
