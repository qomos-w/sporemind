package pluginloader

import (
	"fmt"

	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

const NativeInvokeSymbol = "PluginInvoke"

// Invoke calls the length-prefixed native plugin ABI. The exported function
// must have signature:
// int PluginInvoke(const uint8_t*, size_t, uint8_t*, size_t, size_t*).
//
// If the plugin returns StatusBufferTooSmall (setting *respLen to the needed
// frame size), Invoke transparently retries once with a buffer large enough
// to hold the response (capped at pluginhost.MaxResponseBytes).
func (lib *Library) Invoke(symbol string, request []byte, responseCapacity int) ([]byte, error) {
	if responseCapacity < 0 {
		return nil, fmt.Errorf("plugin response capacity cannot be negative")
	}
	var fn func(*byte, uintptr, *byte, uintptr, *uintptr) int32
	if err := lib.Symbol(symbol, &fn); err != nil {
		return nil, err
	}
	var requestPtr *byte
	if len(request) > 0 {
		requestPtr = &request[0]
	}
	reqLen := uintptr(len(request))

	// First attempt with the caller-provided capacity.
	response := make([]byte, responseCapacity)
	var responsePtr *byte
	if len(response) > 0 {
		responsePtr = &response[0]
	}
	var responseLen uintptr
	status := fn(requestPtr, reqLen, responsePtr, uintptr(responseCapacity), &responseLen)

	// Dynamic retry: the plugin signalled the buffer was too small and set
	// *respLen to the minimum needed frame size. Retry once.
	if needed, retryErr := retryCapacity(status, responseLen, responseCapacity); retryErr != nil {
		return nil, fmt.Errorf("plugin %s: %w", symbol, retryErr)
	} else if needed > 0 {
		response = make([]byte, needed)
		if len(response) > 0 {
			responsePtr = &response[0]
		}
		responseLen = 0
		status = fn(requestPtr, reqLen, responsePtr, uintptr(needed), &responseLen)
	}

	if status != 0 {
		return nil, fmt.Errorf("plugin %s returned status %d", symbol, status)
	}
	if int(responseLen) > len(response) {
		return nil, fmt.Errorf("plugin %s returned response length %d over capacity %d", symbol, responseLen, len(response))
	}
	return response[:int(responseLen)], nil
}

// retryCapacity evaluates a plugin invocation status and decides whether to
// retry with a larger buffer. It returns the capacity to allocate for the
// retry, or 0 if no retry is warranted. A non-nil error means the
// StatusBufferTooSmall signal was present but invalid (the needed size is
// inconsistent or exceeds the safety cap) and the caller should fail
// immediately rather than treating the status as a generic error.
func retryCapacity(status int32, responseLen uintptr, currentCapacity int) (int, error) {
	if status != pluginhost.StatusBufferTooSmall {
		return 0, nil
	}
	needed := int(responseLen)
	if needed <= currentCapacity {
		return 0, fmt.Errorf("reported buffer too small but needed %d <= capacity %d", needed, currentCapacity)
	}
	if needed > pluginhost.MaxResponseBytes {
		return 0, fmt.Errorf("requested %d bytes, exceeds max %d", needed, pluginhost.MaxResponseBytes)
	}
	return needed, nil
}

func (lib *Library) InvokeDefault(request []byte, responseCapacity int) ([]byte, error) {
	return lib.Invoke(NativeInvokeSymbol, request, responseCapacity)
}

func InvokeFramed(lib *Library, request []byte, responseCapacity int) ([]byte, error) {
	response, err := lib.InvokeDefault(request, responseCapacity)
	if err != nil {
		return nil, err
	}
	return pluginhost.DecodeInvokeFrame(response)
}
