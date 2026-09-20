package pluginhost

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

const abiHeaderSize = 4

// ABI status codes returned by PluginInvoke (C ABI level).
//
// The status is the int32 return value of the native PluginInvoke symbol.
// status 0 means success; the host reads *respLen bytes from the response
// buffer. A positive non-zero code carries a recoverable signal.
const (
	// StatusOK is the success code.
	StatusOK int32 = 0
	// StatusBufferTooSmall signals that the response buffer was too small.
	// The plugin sets *respLen to the minimum needed frame size. The host
	// should retry the invocation with a buffer of at least that size.
	//
	// Any other non-zero status is a non-recoverable error.
	StatusBufferTooSmall int32 = 2
)

// DefaultResponseCapacity is the initial response buffer the host allocates
// for a plugin invocation. This is NOT a hard ceiling: if the plugin's
// response exceeds this size, the plugin returns StatusBufferTooSmall and
// the host transparently retries with the needed buffer (up to
// MaxResponseBytes).
const DefaultResponseCapacity = 1 << 20 // 1 MiB

// MaxResponseBytes is the absolute upper bound on a single plugin response.
// It protects against unbounded allocation when a plugin reports an absurd
// needed size via StatusBufferTooSmall.
const MaxResponseBytes = 64 << 20 // 64 MiB

// EncodeInvokeFrame returns a length-prefixed request frame.
func EncodeInvokeFrame(payload []byte) []byte {
	frame := make([]byte, abiHeaderSize+len(payload))
	binary.BigEndian.PutUint32(frame[:abiHeaderSize], uint32(len(payload)))
	copy(frame[abiHeaderSize:], payload)
	return frame
}

// DecodeInvokeFrame validates and extracts one complete length-prefixed frame.
func DecodeInvokeFrame(frame []byte) ([]byte, error) {
	if len(frame) < abiHeaderSize {
		return nil, fmt.Errorf("plugin frame is shorter than header")
	}
	length := binary.BigEndian.Uint32(frame[:abiHeaderSize])
	if uint64(length) != uint64(len(frame)-abiHeaderSize) {
		return nil, fmt.Errorf("plugin frame length mismatch")
	}
	return frame[abiHeaderSize:], nil
}

// PluginInvoke is the host-side shape of the official native ABI.
type PluginInvoke func(request []byte, responseCapacity int) ([]byte, error)

func InvokeFramed(invoke PluginInvoke, request []byte, responseCapacity int) ([]byte, error) {
	if invoke == nil {
		return nil, fmt.Errorf("plugin invoke function is nil")
	}
	payload, err := DecodeInvokeFrame(request)
	if err != nil {
		return nil, err
	}
	response, err := invoke(payload, responseCapacity)
	if err != nil {
		return nil, err
	}
	if responseCapacity > 0 && len(response) > responseCapacity {
		return nil, fmt.Errorf("plugin response exceeds capacity")
	}
	return EncodeInvokeFrame(response), nil
}

// EncodeInvokeEnvelope wraps the raw request payload, the callable name, and
// traceable invocation metadata into a length-prefixed frame whose body is the
// JSON PluginAbiInvokeEnvelope. This is what gets handed to the native
// PluginInvoke ABI.
func EncodeInvokeEnvelope(callable string, request []byte, meta InvokeMeta) []byte {
	env := gen.PluginAbiInvokeEnvelope{
		Callable:  callable,
		Payload:   request,
		RequestID: meta.RequestID,
		SessionID: meta.SessionID,
		CallSeq:   meta.CallSeq,
	}
	body, _ := json.Marshal(env)
	return EncodeInvokeFrame(body)
}

// DecodeInvokeEnvelope extracts the JSON envelope from a length-prefixed
// frame and returns the envelope plus payload bytes. If the frame body is not
// an envelope, the raw body is returned as-is for backward compatibility.
func DecodeInvokeEnvelope(frame []byte) (env gen.PluginAbiInvokeEnvelope, raw []byte, err error) {
	raw, err = DecodeInvokeFrame(frame)
	if err != nil {
		return gen.PluginAbiInvokeEnvelope{}, nil, err
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return gen.PluginAbiInvokeEnvelope{}, raw, nil
	}
	return env, env.Payload, nil
}
