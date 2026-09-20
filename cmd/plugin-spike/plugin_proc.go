package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// pluginProcessEnv makes the test binary re-execute itself as the plugin
// process (used in TestMain). This is the spike stand-in for the planned
// sporemind-plugin-sdk/process_main.go: a normal Go executable whose main()
// speaks the duplex framing protocol instead of exporting FFI symbols. The
// child spawned by the tests is a real, separate OS process.
const pluginProcessEnv = "PLUGIN_SPIKE_PROCESS"

// pluginMainLoop is the plugin-side read/dispatch/write loop. It reads one
// frame at a time from stdin, dispatches invoke-reqs, and writes responses
// (plus log frames) to stdout. A clean stdin EOF is the graceful unload
// signal (host closed its write end); any other read error is fatal.
func pluginMainLoop(in *bufio.Reader, out io.Writer) error {
	for {
		typ, payload, err := readFrame(in)
		if err != nil {
			return err // io.EOF -> graceful unload; other errors -> fatal
		}
		switch typ {
		case msgInvokeReq:
			resp, err := handleInvoke(payload, in, out)
			if err != nil {
				errBody, _ := json.Marshal(map[string]string{"error": err.Error()})
				if werr := writeFrame(out, msgInvokeResp, errBody); werr != nil {
					return werr
				}
				continue
			}
			if werr := writeFrame(out, msgInvokeResp, resp); werr != nil {
				return werr
			}
		case msgError:
			return fmt.Errorf("host reported fatal error: %s", payload)
		default:
			return fmt.Errorf("unexpected message type 0x%02x", typ)
		}
	}
}

// invokeEnvelope mirrors the generated PluginAbiInvokeEnvelope
// (pkg/domain/gen/plugin.gen.go, framed in pkg/pluginhost/abi.go) so the
// spike exercises the same request shape the planned process transport will
// carry.
type invokeEnvelope struct {
	Callable  string          `json:"Callable"`
	Payload   json.RawMessage `json:"Payload"`
	RequestID string          `json:"RequestId,omitempty"`
	SessionID string          `json:"SessionId,omitempty"`
	CallSeq   int64           `json:"CallSeq,omitempty"`
}

func handleInvoke(payload []byte, in *bufio.Reader, out io.Writer) ([]byte, error) {
	var env invokeEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("decode invoke envelope: %w", err)
	}
	if env.Callable == "" {
		return nil, fmt.Errorf("empty callable")
	}
	switch env.Callable {
	case "greet":
		return handleGreet(env.Payload, in, out)
	case "ping":
		return json.Marshal(map[string]string{"pong": "ok"})
	case "echo":
		var req struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(env.Payload, &req); err != nil {
			return nil, fmt.Errorf("echo: %w", err)
		}
		return json.Marshal(map[string]any{"data": req.Data})
	case "stall":
		// Only used by the timeout/crash tests: blocks the plugin's single
		// dispatch thread so the host-side read loop has nothing to read.
		time.Sleep(30 * time.Second)
		return json.Marshal(map[string]string{"stalled": "never"})
	default:
		return nil, fmt.Errorf("callable %s not found", env.Callable)
	}
}

// reverseReq is the wire body of a reverse bridge call, matching the SDK
// bridgeCall shape {callID, JSON payload} (sporemind-plugin-sdk/bridge.go).
type reverseReq struct {
	CallID  string          `json:"callID"`
	Payload json.RawMessage `json:"payload"`
}

type greetReq struct {
	Name    string      `json:"name"`
	Reverse *reverseReq `json:"reverse,omitempty"`
}

func handleGreet(payload json.RawMessage, in *bufio.Reader, out io.Writer) ([]byte, error) {
	var req greetReq
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("greet: %w", err)
	}
	// In the subprocess transport, ctx.Log output travels as a 0x05 log
	// frame instead of being drained via the PluginLog FFI symbol after each
	// invoke (the ring-drain path is replaced by direct frame writes).
	_ = writeFrame(out, msgLog, []byte(fmt.Sprintf("greet called for %s", req.Name)))

	resp := map[string]any{"message": fmt.Sprintf("Hello, %s!", req.Name)}
	if req.Reverse != nil {
		rev, err := reverseInvoke(in, out, req.Reverse.CallID, req.Reverse.Payload)
		if err != nil {
			return nil, fmt.Errorf("greet: reverse %s: %w", req.Reverse.CallID, err)
		}
		resp["reverse_result"] = json.RawMessage(rev)
	}
	return json.Marshal(resp)
}

// reverseInvoke is the plugin-side reverse bridge call: it writes a 0x03
// reverse-req frame and synchronously blocks reading until the matching 0x04
// reverse-resp arrives. It mirrors sdk bridgeCall/bridgeResult: the response
// envelope carrying "__host_error__" is converted into a Go error.
//
// The plugin is single-threaded while an invoke is in flight (the design's
// synchronous model), so no request-id is needed on this side: the only
// frames the host sends during an invoke are reverse-resps and fatals.
func reverseInvoke(in *bufio.Reader, out io.Writer, callID string, payload any) ([]byte, error) {
	req, err := json.Marshal(map[string]any{"callID": callID, "payload": payload})
	if err != nil {
		return nil, err
	}
	if err := writeFrame(out, msgReverseReq, req); err != nil {
		return nil, err
	}
	for {
		typ, p, err := readFrame(in)
		if err != nil {
			return nil, err
		}
		switch typ {
		case msgReverseResp:
			var env struct {
				HostError string `json:"__host_error__"`
			}
			if json.Unmarshal(p, &env) == nil && env.HostError != "" {
				return nil, fmt.Errorf("host invoke %s: %s", callID, env.HostError)
			}
			return p, nil
		case msgInvokeReq:
			// The host transport serializes invokes (single in-flight per
			// process). A nested invoke-req here means the host broke that
			// invariant; fail loudly instead of interleaving.
			return nil, fmt.Errorf("protocol violation: nested invoke-req while awaiting reverse-resp")
		case msgError:
			return nil, fmt.Errorf("host reported fatal error: %s", p)
		default:
			return nil, fmt.Errorf("unexpected message type 0x%02x while awaiting reverse-resp", typ)
		}
	}
}
