package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unsafe"

	"github.com/qomos-w/sporemind-plugin-sdk/gen"
)

// panicError converts a recovered panic value into a readable error.
func panicError(r any) error {
	if r == nil {
		return nil
	}
	switch v := r.(type) {
	case error:
		return fmt.Errorf("plugin panicked: %w", v)
	case string:
		return fmt.Errorf("plugin panicked: %s", v)
	default:
		return fmt.Errorf("plugin panicked: %+v", v)
	}
}

// WriteManifest writes the registered plugin manifest JSON into buf.
func WriteManifest(buf unsafe.Pointer, n int32) int32 {
	plugin := registered()
	if plugin == nil {
		return -1
	}
	data, err := json.Marshal(plugin.Manifest.Normalize())
	if err != nil || int(n) < len(data)+1 || buf == nil {
		return -1
	}
	src := (*[0x7fffffff]byte)(buf)
	copy(src[:len(data)], data)
	src[len(data)] = 0
	return int32(len(data))
}

// HandleOnLoad is invoked by the host when the plugin is loaded.
func HandleOnLoad(pluginID unsafe.Pointer, config unsafe.Pointer) (ret int32) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[sporemind-plugin-sdk] OnLoad panic: %v\n", panicError(r))
			ret = -1
		}
	}()

	id := goString(pluginID)
	state, err := ensureState(id)
	if err != nil {
		fmt.Printf("[sporemind-plugin-sdk] OnLoad error: %v\n", err)
		return -1
	}
	state.mu.Lock()
	if state.loaded && !state.unloading {
		state.mu.Unlock()
		return 0
	}
	state.unloading = false
	state.mu.Unlock()

	// Apply host-pushed per-instance config (HTTP listener address, session
	// cookie secret) before the app's OnLoad runs, so the secret is in place
	// before any HTTP request can arrive. The HTTP listener itself is started
	// after the app's OnLoad so registered handlers are in place first.
	pendingHTTPAddr := applyLoadConfig(json.RawMessage(goString(config)))

	// Run the user's OnLoad with panic recovery. A panic in the user
	// function must not crash the plugin's Go runtime (and by extension
	// the host process). We capture the panic here and return an error
	// code instead. The state is only marked loaded when OnLoad succeeds.
	loadFailed := false
	if state.plugin.OnLoad != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[sporemind-plugin-sdk] OnLoad panic: %v\n", panicError(r))
					loadFailed = true
				}
			}()
			if err := state.plugin.OnLoad(state.ctx); err != nil {
				fmt.Printf("[sporemind-plugin-sdk] OnLoad error: %v\n", err)
				loadFailed = true
			}
		}()
	}
	if loadFailed {
		return -1
	}
	// Start the HTTP listener if the host asked for one and none is running
	// yet. Done after the app's OnLoad so HTTP handlers the app registered are
	// in place before the listener accepts traffic.
	maybeAutoStartHTTP(pendingHTTPAddr)
	state.mu.Lock()
	state.loaded = true
	state.mu.Unlock()
	return 0
}

// HandleOnUnload is invoked by the host when the plugin is unloaded.
func HandleOnUnload(pluginID unsafe.Pointer) (ret int32) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[sporemind-plugin-sdk] OnUnload panic: %v\n", panicError(r))
			ret = -1
		}
	}()

	id := goString(pluginID)
	state, err := currentState()
	if err != nil || state.pluginID != id {
		return -1
	}
	state.mu.Lock()
	if state.unloading {
		state.mu.Unlock()
		return 0
	}
	state.unloading = true
	state.mu.Unlock()

	// Run the user's OnUnload with panic recovery. Cleanup must proceed
	// regardless of whether the user function panics, so the callback is
	// wrapped in an inner scope.
	if state.plugin.OnUnload != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[sporemind-plugin-sdk] OnUnload panic: %v\n", panicError(r))
					ret = -1
				}
			}()
			if err := state.plugin.OnUnload(state.ctx); err != nil {
				fmt.Printf("[sporemind-plugin-sdk] OnUnload error: %v\n", err)
			}
		}()
	}

	// Always run cleanup even if the user's OnUnload panicked.
	unregisterPluginCallables(state.pluginID)
	clearFileDropState()
	// Stop the HTTP listener so a reloaded plugin does not leave a dangling
	// port. In the subprocess transport the process exits on unload anyway,
	// but the c-shared (in-process) transport keeps the host process alive,
	// so the listener must be closed here.
	if srv := currentHTTPServer(); srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
	state.mu.Lock()
	state.loaded = false
	state.mu.Unlock()
	activeState.Lock()
	if activeState.state == state {
		activeState.state = nil
	}
	activeState.Unlock()
	return ret
}

// HandleOnConfigChange is invoked when the host updates plugin configuration.
func HandleOnConfigChange(pluginID unsafe.Pointer, config unsafe.Pointer) (ret int32) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[sporemind-plugin-sdk] OnConfigChange panic: %v\n", panicError(r))
			ret = -1
		}
	}()

	id := goString(pluginID)
	state, err := currentState()
	if err != nil || state.pluginID != id {
		return -1
	}
	pluginConfig := json.RawMessage(goString(config))
	if state.plugin.OnConfigChange != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[sporemind-plugin-sdk] OnConfigChange panic: %v\n", panicError(r))
					ret = -1
				}
			}()
			if err := state.plugin.OnConfigChange(state.ctx, pluginConfig); err != nil {
				fmt.Printf("[sporemind-plugin-sdk] OnConfigChange error: %v\n", err)
				ret = -1
			}
		}()
	}
	return ret
}

// HandleInvoke is the legacy NUL-terminated ABI entry point, retained for
// backward compatibility. New plugin hosts should call PluginInvokeFramed
// instead, which uses length-prefixed framing matching abi.go.
//
// Deprecated: Use PluginInvokeFramed for length-prefixed binary ABI.
func HandleInvoke(pluginID unsafe.Pointer, callID unsafe.Pointer, req unsafe.Pointer, res unsafe.Pointer, resLen int32) int32 {
	id := goString(pluginID)
	method := goString(callID)
	state, err := currentState()
	if err != nil || state.pluginID != id {
		return -1
	}
	data, err := Dispatch(id, method, json.RawMessage(goString(req)))
	if err != nil || res == nil || resLen <= int32(len(data)) {
		return -1
	}
	src := (*[0x7fffffff]byte)(res)
	copy(src[:len(data)], data)
	src[len(data)] = 0
	return int32(len(data))
}

// HandleInvokeFramed is the official length-prefixed native ABI entry point.
// It mirrors the host-side abi.go framing.
//
// Signature: int PluginInvoke(const uint8_t* req, size_t reqLen,
//
//	uint8_t* resp, size_t respCap, size_t* respLen)
//
// The request frame payload is JSON: {"callable":"greet","payload":{...}}
// The response frame payload is JSON: {"payload":{...}} or {"error":"..."}
func HandleInvokeFramed(reqPtr unsafe.Pointer, reqLen uintptr, respPtr unsafe.Pointer, respCap uintptr, respLen *uintptr) int32 {
	if reqPtr == nil || reqLen < abiHeaderSize {
		return -1
	}

	// Read the length-prefixed request frame.
	reqBuf := (*[0x7fffffff]byte)(reqPtr)[:reqLen:reqLen]
	payload, err := decodeFrame(reqBuf)
	if err != nil {
		return -1
	}

	// Parse JSON ABI envelope {Callable, Payload, RequestId, SessionId, CallSeq}.
	var env gen.PluginAbiInvokeEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return -1
	}
	if env.Callable == "" {
		return -1
	}

	state, err := currentState()
	if err != nil {
		return -1
	}

	data, err := dispatchRequest(gen.PluginSdkRequest{
		PluginID:  state.pluginID,
		CallID:    env.Callable,
		Payload:   env.Payload,
		RequestID: env.RequestID,
		SessionID: env.SessionID,
		CallSeq:   env.CallSeq,
	})
	if err != nil {
		// Encode error as a JSON response inside a frame so the host can decode it.
		errResp, _ := json.Marshal(map[string]string{"error": err.Error()})
		frame := encodeFrame(errResp)
		if uintptr(len(frame)) > respCap {
			if respLen != nil {
				*respLen = uintptr(len(frame))
			}
			return StatusBufferTooSmall
		}
		if respPtr == nil || respLen == nil {
			return -1
		}
		respBuf := (*[0x7fffffff]byte)(respPtr)
		copy(respBuf[:len(frame)], frame)
		*respLen = uintptr(len(frame))
		return 0
	}

	// Encode the response payload as a length-prefixed frame.
	frame := encodeFrame(data)
	if uintptr(len(frame)) > respCap {
		if respLen != nil {
			*respLen = uintptr(len(frame))
		}
		return StatusBufferTooSmall
	}
	if respPtr == nil || respLen == nil {
		return -1
	}
	respBuf := (*[0x7fffffff]byte)(respPtr)
	copy(respBuf[:len(frame)], frame)
	*respLen = uintptr(len(frame))
	return 0
}

// HandleSetHostBridge receives the host bridge callback pointer.
func HandleSetHostBridge(bridge unsafe.Pointer) int32 {
	// setHostBridge is provided by both build modes: cgo installs the FFI
	// callback, !cgo (subprocess) rejects it since reverse calls go through
	// the injected IPC host instead.
	return setHostBridge(bridge)
}

// HandlePluginLog drains the plugin's log ring buffer into buf as a
// JSON array of LogEntry, NUL-terminated. Returns the number of bytes
// written (excluding NUL), or -1 on error.
//
// ABI signature: int PluginLog(char* buf, int n)
// The host calls this after each invoke to pull structured logs emitted
// by the plugin via Context.Log. If the ring buffer overflowed, a
// synthetic warning entry is prepended to indicate how many entries
// were dropped.
func HandlePluginLog(buf unsafe.Pointer, n int32) int32 {
	state, err := currentState()
	if err != nil {
		return -1
	}
	entries, dropped := state.logRing.drain()
	if dropped > 0 {
		entries = append([]LogEntry{{
			Level:   LogLevelWarn,
			Message: fmt.Sprintf("[pluginlog] %d log entries were dropped (ring buffer full)", dropped),
		}}, entries...)
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return -1
	}
	if buf == nil || int(n) < len(data)+1 {
		return -1
	}
	src := (*[0x7fffffff]byte)(buf)
	copy(src[:len(data)], data)
	src[len(data)] = 0
	return int32(len(data))
}

func goString(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	b := (*[0x7fffffff]byte)(p)
	n := 0
	for b[n] != 0 {
		n++
	}
	return string(b[:n])
}

// ── Length-prefixed ABI (matches pkg/pluginhost/abi.go) ──

const abiHeaderSize = 4

// StatusBufferTooSmall is returned by HandleInvokeFramed when the response
// frame does not fit in the caller-provided buffer. *respLen is set to the
// minimum needed frame size so the host can retry with a larger buffer.
const StatusBufferTooSmall int32 = 2

// encodeFrame returns a 4-byte big-endian length prefix followed by the payload.
func encodeFrame(payload []byte) []byte {
	frame := make([]byte, abiHeaderSize+len(payload))
	frame[0] = byte(len(payload) >> 24)
	frame[1] = byte(len(payload) >> 16)
	frame[2] = byte(len(payload) >> 8)
	frame[3] = byte(len(payload))
	copy(frame[abiHeaderSize:], payload)
	return frame
}

// decodeFrame validates the length prefix and returns the payload. Returns an
// error if the frame is truncated or the declared length does not match.
func decodeFrame(frame []byte) ([]byte, error) {
	if len(frame) < abiHeaderSize {
		return nil, fmt.Errorf("plugin frame shorter than header")
	}
	declared := int(frame[0])<<24 | int(frame[1])<<16 | int(frame[2])<<8 | int(frame[3])
	if declared != len(frame)-abiHeaderSize {
		return nil, fmt.Errorf("plugin frame length mismatch: declared %d, actual %d", declared, len(frame)-abiHeaderSize)
	}
	return frame[abiHeaderSize:], nil
}
