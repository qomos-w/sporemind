package sdk

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/qomos-w/sporemind-plugin-sdk/gen"
)

// Request is the plugin-side callable input. It is a generated alias so the
// SDK and host share the same schema-defined shape.
type Request = gen.PluginSdkRequest

// Response is the plugin-side callable output.
type Response = gen.PluginSdkResponse

type Handler func(req Request) (Response, error)

// HandlerStream is the streaming variant: the handler pushes intermediate
// chunks by calling emit (each chunk's Payload is a partial of the terminal),
// then returns the terminal Response. Transports without a forward-chunk wire
// (FFI/c-shared, in-process test) pass a no-op emit so streaming callables
// degrade to zero chunks plus the terminal — identical to the reverse-side
// FFI fallback. emit is only valid for the duration of the handler call; it
// must not be retained or called after the handler returns.
type HandlerStream func(req Request, emit func(Response) error) (Response, error)

// handlerEntry stores either a unary or a streaming handler for one callable.
// Re-registration overwrites the whole entry, so switching modes leaves no
// stale residue. At dispatch the populated field wins (stream takes
// precedence).
type handlerEntry struct {
	unary  Handler
	stream HandlerStream
}

var callableRegistry = struct {
	sync.RWMutex
	items map[string]handlerEntry
}{items: make(map[string]handlerEntry)}

func callableKey(pluginID, method string) string { return pluginID + "\x00" + method }

// RegisterCallable registers a unary handler for the active plugin. It is
// retained as a convenience for SDK examples; lifecycle contexts use the
// plugin-scoped form.
func RegisterCallable(method string, h Handler) {
	pluginID := ""
	if state, err := currentState(); err == nil {
		pluginID = state.pluginID
	}
	registerCallable(pluginID, method, h)
}

// RegisterCallableStream registers a streaming handler for the active plugin.
// The handler receives an emit closure it calls per intermediate chunk before
// returning the terminal Response.
func RegisterCallableStream(method string, h HandlerStream) {
	pluginID := ""
	if state, err := currentState(); err == nil {
		pluginID = state.pluginID
	}
	registerCallableStream(pluginID, method, h)
}

func registerCallable(pluginID, method string, h Handler) {
	callableRegistry.Lock()
	defer callableRegistry.Unlock()
	callableRegistry.items[callableKey(pluginID, method)] = handlerEntry{unary: h}
}

func registerCallableStream(pluginID, method string, h HandlerStream) {
	callableRegistry.Lock()
	defer callableRegistry.Unlock()
	callableRegistry.items[callableKey(pluginID, method)] = handlerEntry{stream: h}
}

func UnregisterCallable(method string) {
	pluginID := ""
	if state, err := currentState(); err == nil {
		pluginID = state.pluginID
	}
	unregisterCallable(pluginID, method)
}

func unregisterCallable(pluginID, method string) {
	callableRegistry.Lock()
	defer callableRegistry.Unlock()
	delete(callableRegistry.items, callableKey(pluginID, method))
}

func unregisterPluginCallables(pluginID string) {
	prefix := pluginID + "\x00"
	callableRegistry.Lock()
	defer callableRegistry.Unlock()
	for key := range callableRegistry.items {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(callableRegistry.items, key)
		}
	}
}

// Dispatch invokes a registered callable using the legacy positional signature.
// It builds a generated Request internally and is kept for tests/convenience.
// Streaming callables degrade to zero chunks plus the terminal here (the
// FFI/in-process path has no forward-chunk wire).
func Dispatch(pluginID string, method string, payload json.RawMessage) ([]byte, error) {
	return dispatchRequest(gen.PluginSdkRequest{PluginID: pluginID, CallID: method, Payload: payload})
}

// dispatchRequest invokes the handler registered for req.CallID and returns
// the JSON-encoded Response.Payload, passing a no-op emit so streaming
// callables degrade to their terminal only. A panic in any registered handler
// is captured and returned as an error so the host process is never terminated.
func dispatchRequest(req gen.PluginSdkRequest) (resp []byte, err error) {
	return dispatchRequestStream(req, func(Response) error { return nil })
}

// dispatchRequestStream invokes the handler registered for req.CallID. emit is
// called for each intermediate chunk of a streaming callable before the
// terminal Response is returned; the response bytes are the JSON-encoded
// Response.Payload. Unary handlers ignore emit.
func dispatchRequestStream(req gen.PluginSdkRequest, emit func(Response) error) (resp []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError(r)
		}
	}()

	callableRegistry.RLock()
	e, ok := callableRegistry.items[callableKey(req.PluginID, req.CallID)]
	if !ok {
		e, ok = callableRegistry.items[callableKey("", req.CallID)]
	}
	callableRegistry.RUnlock()
	if !ok {
		return nil, fmt.Errorf("callable %s not found", req.CallID)
	}
	var r Response
	if e.stream != nil {
		r, err = e.stream(req, emit)
	} else {
		r, err = e.unary(req)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(r.Payload)
}
