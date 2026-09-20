package sdk

import (
	"encoding/json"
	"fmt"
	"sync"
)

// defaultHost implements Host by calling the host bridge (FFI c-shared
// transport) or by reporting that no bridge is available (subprocess
// transport, where an IPC host is injected via SetHost instead). The
// transport-specific Invoke lives in bridge_cgo.go / bridge_nocgo.go; the
// capability clients here are transport-neutral.
type defaultHost struct{}

func newHostBridge() Host { return &defaultHost{} }

// InvokeStream is the FFI-transport fallback for the streaming reverse-call
// API: the c-shared bridge is synchronous and unary, so there are no
// intermediate chunks — the full response is delivered to onChunk as a
// single chunk right before returning. This keeps the API shape identical
// across transports; callers that only need the aggregated result work
// unchanged, and callers that render deltas degrade to one final delta.
// The subprocess transport overrides this with true 0x07-frame streaming.
func (h *defaultHost) InvokeStream(callID string, payload any, onChunk func([]byte) error) ([]byte, error) {
	resp, err := h.Invoke(callID, payload)
	if err != nil {
		return nil, err
	}
	if onChunk != nil {
		if err := onChunk(resp); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

// injectedHost holds the Host installed by SetHost. When set it takes
// priority over the default host bridge: plugin states are created with the
// injected host and ActiveHost resolves to it.
var injectedHost = struct {
	sync.RWMutex
	host Host
}{}

// SetHost installs the Host implementation used by the SDK, overriding the
// default host bridge. Process-mode plugins inject an IPC-backed host before
// loading; c-shared plugins normally leave it unset and keep the default
// newHostBridge FFI path. Passing nil clears the injection and restores the
// default host bridge behavior.
func SetHost(h Host) {
	injectedHost.Lock()
	defer injectedHost.Unlock()
	injectedHost.host = h
}

// activeHost returns the injected host if set, otherwise the default host
// bridge. ensureState uses it when a plugin lifecycle state is created.
func activeHost() Host {
	injectedHost.RLock()
	h := injectedHost.host
	injectedHost.RUnlock()
	if h != nil {
		return h
	}
	return newHostBridge()
}

// bridgeResult post-processes the raw bytes returned by the host bridge fn.
// The host encodes dispatch failures as {"__host_error__": "..."} with a
// positive return count; without this check state.set-style failures are
// silently swallowed as successful responses. It is shared by the FFI
// bridgeCall and the subprocess processHost reverse path so both transports
// decode the error envelope identically.
func bridgeResult(callID string, out []byte) ([]byte, error) {
	var env struct {
		HostError string `json:"__host_error__"`
	}
	if json.Unmarshal(out, &env) == nil && env.HostError != "" {
		return nil, fmt.Errorf("host invoke %s: %s", callID, env.HostError)
	}
	return out, nil
}

// ForwardLLMChunks adapts a typed onChunk callback to the raw-bytes onChunk
// contract of Host.InvokeStream. The wire bytes are the generic host envelope
// {kind, data}; this decoder maps the LLM vocabulary into the LLMChunk fields
// and passes unknown kinds through with Data set. A decode failure aborts the
// stream — chunks are never silently dropped.
//
// This is the shared primitive behind the generated StreamLLM* callers in a
// hostproto.gen.go; it is not attached to any hand-written capability client.
func ForwardLLMChunks(onChunk func(LLMChunk) error) func([]byte) error {
	return func(wire []byte) error {
		var env struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(wire, &env); err != nil {
			return fmt.Errorf("decode stream chunk: %w", err)
		}
		chunk := LLMChunk{Kind: env.Kind}
		switch env.Kind {
		case "text_delta", "reasoning_delta":
			var data struct {
				Text string `json:"text"`
			}
			if len(env.Data) > 0 {
				if err := json.Unmarshal(env.Data, &data); err != nil {
					return fmt.Errorf("decode %s chunk: %w", env.Kind, err)
				}
			}
			chunk.Text = data.Text
		case "usage":
			chunk.Usage = env.Data
		default:
			chunk.Data = env.Data
		}
		return onChunk(chunk)
	}
}

// ActiveHost returns the current host. An injected host (SetHost) takes
// priority, so process-mode plugins resolve to their IPC-backed host even
// before a lifecycle state exists; otherwise the active plugin state's host
// is used, with the default host bridge as the final fallback.
func ActiveHost() Host {
	injectedHost.RLock()
	injected := injectedHost.host
	injectedHost.RUnlock()
	if injected != nil {
		return injected
	}
	state, err := currentState()
	if err != nil {
		return newHostBridge()
	}
	return state.host
}
