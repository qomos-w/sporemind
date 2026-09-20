package sdk

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestRegisterEventListenerDispatch covers the reserved-name delivery path:
// a listener registered via RegisterEventListener receives the JSON payload
// when the host invokes the plugin under `__event__:<kind>`, through the
// exact same dispatchRequest callables use.
func TestRegisterEventListenerDispatch(t *testing.T) {
	type lifecyclePayload struct {
		Kind string `json:"Kind"`
		ID   string `json:"Id"`
	}

	var got lifecyclePayload
	RegisterEventListener("app_lifecycle", func(payload json.RawMessage) error {
		return json.Unmarshal(payload, &got)
	})
	t.Cleanup(func() {
		unregisterCallable("", EventCallID("app_lifecycle"))
	})

	raw, err := json.Marshal(lifecyclePayload{Kind: "registered", ID: "app.demo"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Dispatch("", EventCallID("app_lifecycle"), raw)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if string(resp) != "null" {
		t.Errorf("response payload = %q, want null (event delivery has no payload)", resp)
	}
	if got.Kind != "registered" || got.ID != "app.demo" {
		t.Errorf("listener payload = %+v, want {registered app.demo}", got)
	}

	// No listener registered: unknown-kind dispatch errors like any
	// unregistered callable.
	if _, err := Dispatch("", EventCallID("app_event"), raw); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("dispatch of unlistened kind must error 'not found', got %v", err)
	}
}

// TestEmitEventCoversWireAndErrors pins the app.emit host-call contract: the
// event kind and marshaled payload ride the wire untouched, an accepted
// response decodes cleanly, and both a host error and a non-accepted response
// surface as errors.
func TestEmitEventCoversWireAndErrors(t *testing.T) {
	type emitCapture struct {
		callID  string
		payload appEmitReq
	}
	var captured []emitCapture
	host := &captureHost{
		invoke: func(callID string, payload any) ([]byte, error) {
			req, ok := payload.(appEmitReq)
			if !ok {
				t.Fatalf("app.emit payload type %T, want appEmitReq", payload)
			}
			captured = append(captured, emitCapture{callID: callID, payload: req})
			return []byte(`{"Accepted":true}`), nil
		},
	}
	SetHost(host)
	defer SetHost(nil)

	if err := EmitEvent("todo_changed", map[string]any{"done": true}); err != nil {
		t.Fatalf("EmitEvent: %v", err)
	}
	if len(captured) != 1 || captured[0].callID != "app.emit" {
		t.Fatalf("captured = %+v, want one app.emit call", captured)
	}
	if captured[0].payload.Event != "todo_changed" {
		t.Errorf("wire event = %q, want todo_changed", captured[0].payload.Event)
	}
	if string(captured[0].payload.Payload) != `{"done":true}` {
		t.Errorf("wire payload = %q, want {\"done\":true}", captured[0].payload.Payload)
	}

	// Host error surfaces.
	SetHost(&captureHost{invoke: func(string, any) ([]byte, error) {
		return nil, errTestHostFailure
	}})
	if err := EmitEvent("todo_changed", nil); err == nil || !strings.Contains(err.Error(), "host failure") {
		t.Errorf("host error not surfaced: %v", err)
	}

	// Non-accepted response surfaces.
	SetHost(&captureHost{invoke: func(string, any) ([]byte, error) {
		return []byte(`{"Accepted":false}`), nil
	}})
	if err := EmitEvent("todo_changed", nil); err == nil || !strings.Contains(err.Error(), "did not accept") {
		t.Errorf("rejected event not surfaced: %v", err)
	}
}

var errTestHostFailure = errors.New("host failure")

type captureHost struct {
	invoke func(callID string, payload any) ([]byte, error)
}

func (h *captureHost) Invoke(callID string, payload any) ([]byte, error) {
	if h.invoke == nil {
		return []byte(`{}`), nil
	}
	return h.invoke(callID, payload)
}

func (h *captureHost) InvokeStream(callID string, payload any, onChunk func([]byte) error) ([]byte, error) {
	resp, err := h.Invoke(callID, payload)
	if err != nil {
		return nil, err
	}
	if onChunk != nil {
		_ = onChunk(resp)
	}
	return resp, nil
}
