package pluginhost

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/config"
)

func TestCallIDToCapability(t *testing.T) {
	cases := []struct {
		callID string
		want   string
	}{
		{"llm.complete", "llm.invoke"},
		{"llm.chat", "llm.invoke"},
		{"project.read_file", "fs.read"},
		{"project.write_file", "fs.write"},
		{"shell.exec", "shell.exec"},
		{"config.get", "config.read"},
		{"config.set", "config.read"},
		{"provider.list", "provider.read"},
		{"provider.get", "provider.read"},
		{"aggregator.list", "aggregator.read"},
		{"aggregator.get", "aggregator.read"},
		{"state.get", "app.state"},
		{"state.set", "app.state"},
		{"state.delete", "app.state"},
		{"app.emit", "app.emit"},
		{"browser.cookies_export", "browser.cookies.read"},
		{"dialog.openFile", "dialog.openFile"},
		{"dialog.openFolder", "dialog.openFolder"},
		{"dialog.saveFile", "dialog.saveFile"},
		{"clipboard.write", "clipboard.write"},
		{"clipboard.read", "clipboard.read"},
		{"db.profile_list", "db.profile.read"},
		{"db.profile_dial", "db.dial"},
		// Unknown / unmapped callIDs map to "".
		{"notification.send", ""},
		{"", ""},
		{"unknown", ""},
	}
	for _, tc := range cases {
		t.Run(tc.callID, func(t *testing.T) {
			got := callIDToCapability(tc.callID)
			if got != tc.want {
				t.Errorf("callIDToCapability(%q) = %q, want %q", tc.callID, got, tc.want)
			}
		})
	}
}

func TestHostBridgeAllows(t *testing.T) {
	allCaps := map[string]struct{}{
		"llm.invoke":      {},
		"fs.read":         {},
		"fs.write":        {},
		"shell.exec":      {},
		"config.read":     {},
		"provider.read":   {},
		"aggregator.read": {},
		"app.state":       {},
		"app.emit":        {},
	}
	b := NewHostBridge(allCaps, nil, "", nil)

	allowed := []string{
		"llm.complete", "llm.chat",
		"project.read_file", "project.write_file",
		"shell.exec",
		"config.get", "config.set",
		"provider.list", "provider.get",
		"aggregator.list", "aggregator.get",
	}
	for _, callID := range allowed {
		if !b.Allows(callID) {
			t.Errorf("Allows(%q) = false, want true", callID)
		}
	}

	denied := []string{
		"notification.send",
		"",
		"unknown",
	}
	for _, callID := range denied {
		if b.Allows(callID) {
			t.Errorf("Allows(%q) = true, want false", callID)
		}
	}
}

func TestHostBridgeEmptyAllowedDeniesAll(t *testing.T) {
	b := NewHostBridge(nil, nil, "", nil)
	// No capabilities granted — every callID must be denied, including
	// known ones.
	for _, callID := range []string{"llm.complete", "config.get", "project.read_file"} {
		if b.Allows(callID) {
			t.Errorf("Allows(%q) = true with empty allowed set, want false", callID)
		}
	}
}

func TestHostBridgeAllowedSetIsCopied(t *testing.T) {
	src := map[string]struct{}{"llm.invoke": {}}
	b := NewHostBridge(src, nil, "", nil)
	// Mutate the source; the bridge must be unaffected.
	delete(src, "llm.invoke")
	if !b.Allows("llm.complete") {
		t.Error("bridge allowed set was mutated by external map change")
	}
}

func TestHostBridgeDispatchDeniesUngranted(t *testing.T) {
	called := false
	b := NewHostBridge(nil, func(string, []byte) ([]byte, error) {
		called = true
		return []byte("{}"), nil
	}, "", nil)
	_, err := b.Dispatch("llm.complete", []byte("{}"))
	if err == nil {
		t.Fatal("Dispatch with empty allowed set must error")
	}
	if called {
		t.Error("dispatch function must not be called for denied capability")
	}
}

func TestHostBridgeDispatchAllowsGranted(t *testing.T) {
	var gotCallID string
	var gotReq []byte
	b := NewHostBridge(
		map[string]struct{}{"llm.invoke": {}},
		func(callID string, req []byte) ([]byte, error) {
			gotCallID = callID
			gotReq = req
			return []byte(`{"ok":true}`), nil
		},
		"", nil,
	)
	resp, err := b.Dispatch("llm.complete", []byte(`{"prompt":"hi"}`))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if gotCallID != "llm.complete" {
		t.Errorf("dispatch got callID %q, want llm.complete", gotCallID)
	}
	if string(gotReq) != `{"prompt":"hi"}` {
		t.Errorf("dispatch got req %q", gotReq)
	}
	if string(resp) != `{"ok":true}` {
		t.Errorf("dispatch resp = %q", resp)
	}
}

// TestHostBridgeDispatchAppEmitInjectsPluginID pins the app.emit routing: the
// call goes through the plugin-identity-injecting local dispatch (like
// state.*), never the service dispatch, and the injected Plugin field rides
// in the JSON the local handler decodes.
func TestHostBridgeDispatchAppEmitInjectsPluginID(t *testing.T) {
	var gotCallID string
	var gotReq []byte
	b := NewHostBridge(
		map[string]struct{}{"app.emit": {}},
		nil,
		"app.demo",
		func(callID string, req []byte) ([]byte, error) {
			gotCallID = callID
			gotReq = req
			return []byte(`{"Accepted":true}`), nil
		},
	)
	resp, err := b.Dispatch("app.emit", []byte(`{"event":"todo_changed","payload":{"done":true}}`))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if gotCallID != "app.emit" {
		t.Errorf("dispatch got callID %q, want app.emit", gotCallID)
	}
	var got struct {
		Plugin  string          `json:"Plugin"`
		Event   string          `json:"event"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(gotReq, &got); err != nil {
		t.Fatalf("injected req decode: %v", err)
	}
	if got.Plugin != "app.demo" {
		t.Errorf("injected Plugin = %q, want app.demo", got.Plugin)
	}
	if got.Event != "todo_changed" {
		t.Errorf("event = %q, want todo_changed", got.Event)
	}
	if string(got.Payload) != `{"done":true}` {
		t.Errorf("payload = %q", got.Payload)
	}
	if string(resp) != `{"Accepted":true}` {
		t.Errorf("dispatch resp = %q", resp)
	}
}

// TestHostBridgeAppEmitDeniedWithoutCapability pins the deny-all default for
// app.emit: without the derived capability the local dispatch is never
// reached, even though the handler exists.
func TestHostBridgeAppEmitDeniedWithoutCapability(t *testing.T) {
	called := false
	b := NewHostBridge(nil, nil, "app.demo", func(string, []byte) ([]byte, error) {
		called = true
		return []byte("{}"), nil
	})
	if _, err := b.Dispatch("app.emit", []byte(`{"event":"x"}`)); err == nil {
		t.Fatal("app.emit with empty allowed set must error")
	}
	if called {
		t.Error("dispatch function must not be called for denied app.emit")
	}
}

// TestHostBridgeNewCapabilityCallID pins the expandability contract behind
// hostBridgeDispatchServices: a newly registered capability callID is
// allowed once its capability is granted, and — with the backing service
// present — dispatch reaches it. The test registers a throwaway probe
// callID/capability ("hostbridgetest.*") instead of a real domain callID:
// the appbinding registry is process-global, so claiming e.g.
// websearch.search here would overwrite the permanent catalog mapping that
// another expansion task owns, and pollute later assertions in the same
// test binary. The generic mechanism under test is identical.
func TestHostBridgeNewCapabilityCallID(t *testing.T) {
	appbinding.RegisterCapability(appbinding.CapabilityDef{
		ID:          "hostbridgetest.probe",
		Title:       "HostBridge Probe",
		Description: "Throwaway capability for the host-bridge expansion test.",
		RiskLevel:   "low",
	})
	appbinding.RegisterHostCall(appbinding.HostCallDef{
		CallID:     "hostbridgetest.probe",
		Capability: "hostbridgetest.probe",
	})

	// Without the granted capability the new callID is denied even though
	// it maps to a known capability.
	denied := NewHostBridge(nil, nil, "", nil)
	if denied.Allows("hostbridgetest.probe") {
		t.Error("Allows(hostbridgetest.probe) = true without grant, want false")
	}

	// Granted capability → Allows true, and dispatch reaches the backing
	// service when it is present.
	var gotCallID string
	b := NewHostBridge(
		map[string]struct{}{"hostbridgetest.probe": {}},
		func(callID string, req []byte) ([]byte, error) {
			gotCallID = callID
			return []byte(`{"results":[]}`), nil
		},
		"", nil,
	)
	if !b.Allows("hostbridgetest.probe") {
		t.Fatal("Allows(hostbridgetest.probe) = false after grant, want true")
	}
	resp, err := b.Dispatch("hostbridgetest.probe", []byte(`{"query":"x"}`))
	if err != nil {
		t.Fatalf("Dispatch(hostbridgetest.probe): %v", err)
	}
	if gotCallID != "hostbridgetest.probe" {
		t.Errorf("dispatch got callID %q, want hostbridgetest.probe", gotCallID)
	}
	if string(resp) != `{"results":[]}` {
		t.Errorf("dispatch resp = %q", resp)
	}
}

// TestHostBridgeDispatchServicesListsNewDomains pins the routing table:
// hostBridgeDispatchServices must name the websearch/crawl/aistats/oracle/
// unified_graph services so buildHostBridgeDispatch captures their refs at
// OnStart (LookupService skips absent ones — headless-safe), and the callID
// prefix selects the service.
func TestHostBridgeDispatchServicesListsNewDomains(t *testing.T) {
	missing := map[string]bool{
		"websearch":     true,
		"crawl":         true,
		"aistats":       true,
		"oracle":        true,
		"unified_graph": true,
	}
	for _, name := range hostBridgeDispatchServices {
		delete(missing, name)
	}
	for name := range missing {
		t.Errorf("hostBridgeDispatchServices missing %q", name)
	}
	// The callID prefix selects the service, which is how NewServiceDispatch
	// resolves the captured ref.
	for callID, svc := range map[string]string{
		"websearch.search":  "websearch",
		"crawl.page":        "crawl",
		"aistats.usage":     "aistats",
		"oracle.ask":        "oracle",
		"unified_graph.get": "unified_graph",
	} {
		if got := serviceFromHostCallID(callID); got != svc {
			t.Errorf("serviceFromHostCallID(%q) = %q, want %q", callID, got, svc)
		}
	}
}

func TestHostBridgeDispatchNilFunc(t *testing.T) {
	b := NewHostBridge(map[string]struct{}{"llm.invoke": {}}, nil, "", nil)
	_, err := b.Dispatch("llm.complete", nil)
	if err == nil {
		t.Fatal("Dispatch with nil dispatch func must error")
	}
}

func TestHostBridgeServeWritesResponse(t *testing.T) {
	b := NewHostBridge(
		map[string]struct{}{"llm.invoke": {}},
		func(string, []byte) ([]byte, error) {
			return []byte(`{"result":"ok"}`), nil
		},
		"", nil,
	)
	// Simulate C buffers: NUL-terminated callID and req, a res buffer.
	callID := append([]byte("llm.complete"), 0)
	req := append([]byte(`{"prompt":"hi"}`), 0)
	res := make([]byte, 256)

	n := b.serve(&callID[0], &req[0], &res[0], int32(len(res)))
	if n <= 0 {
		t.Fatalf("serve returned %d, want > 0", n)
	}
	var result map[string]string
	if err := json.Unmarshal(res[:n], &result); err != nil {
		t.Fatalf("unmarshal response: %v (raw=%q)", err, res[:n])
	}
	if result["result"] != "ok" {
		t.Errorf("response result = %q, want ok", result["result"])
	}
	// Response must be NUL-terminated.
	if res[n] != 0 {
		t.Errorf("response not NUL-terminated: res[%d]=%d", n, res[n])
	}
}

func TestHostBridgeServeDenyWritesError(t *testing.T) {
	b := NewHostBridge(nil, nil, "", nil)
	callID := append([]byte("llm.complete"), 0)
	req := append([]byte(`{}`), 0)
	res := make([]byte, 256)

	n := b.serve(&callID[0], &req[0], &res[0], int32(len(res)))
	if n <= 0 {
		t.Fatalf("serve returned %d, want > 0 (error must still be written)", n)
	}
	var result map[string]string
	if err := json.Unmarshal(res[:n], &result); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if _, ok := result["__host_error__"]; !ok {
		t.Errorf("error response missing '__host_error__' key: %v", result)
	}
}

func TestHostBridgeServeBufferTooSmall(t *testing.T) {
	b := NewHostBridge(
		map[string]struct{}{"llm.invoke": {}},
		func(string, []byte) ([]byte, error) {
			return []byte(`{"result":"this response is too long for the buffer"}`), nil
		},
		"", nil,
	)
	callID := append([]byte("llm.complete"), 0)
	req := append([]byte(`{}`), 0)
	res := make([]byte, 4)

	n := b.serve(&callID[0], &req[0], &res[0], int32(len(res)))
	if n >= 0 {
		t.Errorf("serve returned %d with tiny buffer, want negative", n)
	}
}

func TestWriteBridgeResult(t *testing.T) {
	t.Run("nil buffer", func(t *testing.T) {
		if n := writeBridgeResult(nil, 10, []byte("x")); n >= 0 {
			t.Errorf("writeBridgeResult(nil,...) = %d, want negative", n)
		}
	})
	t.Run("exact fit", func(t *testing.T) {
		data := []byte("hello")
		buf := make([]byte, len(data)+1) // +1 for NUL
		n := writeBridgeResult(&buf[0], int32(len(buf)), data)
		if int(n) != len(data) {
			t.Errorf("writeBridgeResult = %d, want %d", n, len(data))
		}
		if buf[n] != 0 {
			t.Error("NUL terminator missing")
		}
	})
	t.Run("too small", func(t *testing.T) {
		data := []byte("hello")
		buf := make([]byte, 3)
		if n := writeBridgeResult(&buf[0], int32(len(buf)), data); n >= 0 {
			t.Errorf("writeBridgeResult = %d with small buffer, want negative", n)
		}
	})
}

func TestServiceFromHostCallID(t *testing.T) {
	cases := []struct {
		callID string
		want   string
	}{
		{"llm.complete", "llm"},
		{"config.get", "config"},
		{"project.read_file", "project"},
		{"shell.exec", "shell"},
		{"bare", "bare"},
		{"", ""},
	}
	for _, tc := range cases {
		got := serviceFromHostCallID(tc.callID)
		if got != tc.want {
			t.Errorf("serviceFromHostCallID(%q) = %q, want %q", tc.callID, got, tc.want)
		}
	}
}

func TestNewServiceDispatchMissingService(t *testing.T) {
	dispatch := NewServiceDispatch(nil)
	_, err := dispatch("llm.complete", nil)
	if err == nil {
		t.Fatal("dispatch with no services must error")
	}
}

func TestHostBridgeDispatchProvider(t *testing.T) {
	var gotCallID string
	b := NewHostBridge(
		map[string]struct{}{"provider.read": {}},
		func(callID string, req []byte) ([]byte, error) {
			gotCallID = callID
			return []byte(`{"providers":[]}`), nil
		},
		"", nil,
	)
	resp, err := b.Dispatch("provider.list", nil)
	if err != nil {
		t.Fatalf("Dispatch provider.list: %v", err)
	}
	if gotCallID != "provider.list" {
		t.Errorf("dispatch got callID %q, want provider.list", gotCallID)
	}
	if string(resp) != `{"providers":[]}` {
		t.Errorf("dispatch resp = %q", resp)
	}
}

func TestHostBridgeDispatchAggregator(t *testing.T) {
	var gotCallID string
	b := NewHostBridge(
		map[string]struct{}{"aggregator.read": {}},
		func(callID string, req []byte) ([]byte, error) {
			gotCallID = callID
			return []byte(`{"aggregators":[]}`), nil
		},
		"", nil,
	)
	resp, err := b.Dispatch("aggregator.get", []byte(`{"id":"auto"}`))
	if err != nil {
		t.Fatalf("Dispatch aggregator.get: %v", err)
	}
	if gotCallID != "aggregator.get" {
		t.Errorf("dispatch got callID %q, want aggregator.get", gotCallID)
	}
	if string(resp) != `{"aggregators":[]}` {
		t.Errorf("dispatch resp = %q", resp)
	}
}

func TestHostBridgeDispatchDeniesUngrantedProvider(t *testing.T) {
	called := false
	b := NewHostBridge(nil, func(string, []byte) ([]byte, error) {
		called = true
		return []byte("{}"), nil
	}, "", nil)
	_, err := b.Dispatch("provider.list", nil)
	if err == nil {
		t.Fatal("Dispatch provider.list with empty allowed set must error")
	}
	if called {
		t.Error("dispatch function must not be called for denied capability")
	}
}

func TestHostBridgeDispatchDeniesUngrantedAggregator(t *testing.T) {
	called := false
	b := NewHostBridge(nil, func(string, []byte) ([]byte, error) {
		called = true
		return []byte("{}"), nil
	}, "", nil)
	_, err := b.Dispatch("aggregator.list", nil)
	if err == nil {
		t.Fatal("Dispatch aggregator.list with empty allowed set must error")
	}
	if called {
		t.Error("dispatch function must not be called for denied capability")
	}
}

// --- IPC branch (process transport) ---

func TestDecodeReverseReq(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		frameBody := []byte(`{"callID":"state.set","payload":{"key":"k","value":"aGVsbG8="}}`)
		callID, payload, err := DecodeReverseReq(frameBody)
		if err != nil {
			t.Fatalf("DecodeReverseReq: %v", err)
		}
		if callID != "state.set" {
			t.Errorf("callID = %q, want state.set", callID)
		}
		// RawMessage preserves the payload bytes verbatim (base64 Value and
		// key order untouched — the host forwards what the SDK marshaled).
		if string(payload) != `{"key":"k","value":"aGVsbG8="}` {
			t.Errorf("payload = %q", payload)
		}
	})
	t.Run("null payload", func(t *testing.T) {
		callID, payload, err := DecodeReverseReq([]byte(`{"callID":"provider.list","payload":null}`))
		if err != nil {
			t.Fatalf("DecodeReverseReq: %v", err)
		}
		if callID != "provider.list" {
			t.Errorf("callID = %q, want provider.list", callID)
		}
		if string(payload) != "null" {
			t.Errorf("payload = %q, want null", payload)
		}
	})
	t.Run("absent payload", func(t *testing.T) {
		callID, payload, err := DecodeReverseReq([]byte(`{"callID":"provider.list"}`))
		if err != nil {
			t.Fatalf("DecodeReverseReq: %v", err)
		}
		if callID != "provider.list" {
			t.Errorf("callID = %q, want provider.list", callID)
		}
		if payload != nil {
			t.Errorf("payload = %q, want nil", payload)
		}
	})
	t.Run("empty callID is not a decode error", func(t *testing.T) {
		// An empty callID is denied later by Dispatch with a __host_error__
		// envelope, exactly like the FFI serve path — not a protocol error.
		callID, _, err := DecodeReverseReq([]byte(`{"callID":"","payload":{}}`))
		if err != nil {
			t.Fatalf("DecodeReverseReq: %v", err)
		}
		if callID != "" {
			t.Errorf("callID = %q, want empty", callID)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		if _, _, err := DecodeReverseReq([]byte(`not json`)); err == nil {
			t.Fatal("DecodeReverseReq with malformed body must error")
		}
	})
}

func TestHostBridgeServeReverse(t *testing.T) {
	var gotCallID string
	var gotReq []byte
	b := NewHostBridge(
		map[string]struct{}{"llm.invoke": {}},
		func(callID string, req []byte) ([]byte, error) {
			gotCallID = callID
			gotReq = req
			return []byte(`{"result":"ok"}`), nil
		},
		"", nil,
	)
	resp, err := b.ServeReverse([]byte(`{"callID":"llm.complete","payload":{"prompt":"hi"}}`))
	if err != nil {
		t.Fatalf("ServeReverse: %v", err)
	}
	if gotCallID != "llm.complete" {
		t.Errorf("dispatch got callID %q, want llm.complete", gotCallID)
	}
	if string(gotReq) != `{"prompt":"hi"}` {
		t.Errorf("dispatch got req %q", gotReq)
	}
	if string(resp) != `{"result":"ok"}` {
		t.Errorf("ServeReverse resp = %q", resp)
	}
}

func TestHostBridgeServeReversePayloadVerbatim(t *testing.T) {
	// The SDK-side wire quirks — state.set Value as base64 text,
	// project.write_file content as a JSON string — are encoded by the SDK
	// client before the frame is built. The bridge must route the payload
	// through the same dispatch paths as the FFI transport without altering
	// the encoded values: write_file bytes reach the dispatch function
	// verbatim, and state.set reaches storeDispatch with the base64 Value
	// string intact (plus the injected Plugin identity, shared with FFI).
	var gotWrite []byte
	var gotState map[string]any
	b := NewHostBridge(
		map[string]struct{}{"app.state": {}, "fs.write": {}},
		func(_ string, req []byte) ([]byte, error) {
			gotWrite = append([]byte(nil), req...)
			return []byte("{}"), nil
		},
		"plugin-a",
		func(_ string, req []byte) ([]byte, error) {
			if err := json.Unmarshal(req, &gotState); err != nil {
				t.Fatalf("storeDispatch: unmarshal req: %v", err)
			}
			return []byte("{}"), nil
		},
	)
	writeFile := []byte(`{"path":"a.txt","content":"hello"}`)
	if _, err := b.ServeReverse(append([]byte(`{"callID":"project.write_file","payload":`), append(writeFile, '}')...)); err != nil {
		t.Fatalf("ServeReverse project.write_file: %v", err)
	}
	if string(gotWrite) != string(writeFile) {
		t.Errorf("project.write_file req = %q, want verbatim %q", gotWrite, writeFile)
	}
	stateSet := []byte(`{"key":"accounts","value":"aGVsbG8="}`)
	if _, err := b.ServeReverse(append([]byte(`{"callID":"state.set","payload":`), append(stateSet, '}')...)); err != nil {
		t.Fatalf("ServeReverse state.set: %v", err)
	}
	if gotState["key"] != "accounts" {
		t.Errorf("state.set key = %v, want accounts", gotState["key"])
	}
	if gotState["value"] != "aGVsbG8=" {
		t.Errorf("state.set value = %v, want base64 aGVsbG8=", gotState["value"])
	}
	if gotState["Plugin"] != "plugin-a" {
		t.Errorf("state.set Plugin = %v, want plugin-a", gotState["Plugin"])
	}
}

func TestHostBridgeServeReverseDenied(t *testing.T) {
	called := false
	b := NewHostBridge(nil, func(string, []byte) ([]byte, error) {
		called = true
		return []byte("{}"), nil
	}, "", nil)
	resp, err := b.ServeReverse([]byte(`{"callID":"llm.complete","payload":{}}`))
	if err != nil {
		t.Fatalf("ServeReverse: %v (denied dispatch must be an envelope, not an error)", err)
	}
	if called {
		t.Error("dispatch must not run for denied capability")
	}
	var env map[string]string
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v (raw=%q)", err, resp)
	}
	if env["__host_error__"] == "" {
		t.Errorf("expected __host_error__ envelope, got %q", resp)
	}
}

func TestHostBridgeServeReverseDispatchError(t *testing.T) {
	b := NewHostBridge(map[string]struct{}{"llm.invoke": {}}, func(string, []byte) ([]byte, error) {
		return nil, fmt.Errorf("upstream boom")
	}, "", nil)
	resp, err := b.ServeReverse([]byte(`{"callID":"llm.complete","payload":{}}`))
	if err != nil {
		t.Fatalf("ServeReverse: %v (dispatch errors must be envelopes, not transport errors)", err)
	}
	var env map[string]string
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v (raw=%q)", err, resp)
	}
	if env["__host_error__"] != "upstream boom" {
		t.Errorf("__host_error__ = %q, want upstream boom", env["__host_error__"])
	}
}

// A panicking host capability handler (the 2026-09-09 crash loop: an agent
// invoked app.plan-usage refresh_plan, whose browser.cookies_export reverse
// call panicked host-side and killed the whole process with no trace) must be
// recovered at the dispatch chokepoint and returned as a __host_error__
// envelope for the plugin.
func TestHostBridgeServeReverseDispatchPanic(t *testing.T) {
	b := NewHostBridge(map[string]struct{}{"llm.invoke": {}}, func(string, []byte) ([]byte, error) {
		panic("cookie manager exploded")
	}, "", nil)
	resp, err := b.ServeReverse([]byte(`{"callID":"llm.complete","payload":{}}`))
	if err != nil {
		t.Fatalf("ServeReverse: %v (panics must be envelopes, not transport errors)", err)
	}
	var env map[string]string
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v (raw=%q)", err, resp)
	}
	if !strings.Contains(env["__host_error__"], "panicked") || !strings.Contains(env["__host_error__"], "cookie manager exploded") {
		t.Errorf("__host_error__ = %q, want a panicked envelope carrying the panic value", env["__host_error__"])
	}
}

func TestHostBridgeServeReverseEmptyResponse(t *testing.T) {
	b := NewHostBridge(map[string]struct{}{"llm.invoke": {}}, func(string, []byte) ([]byte, error) {
		return nil, nil
	}, "", nil)
	resp, err := b.ServeReverse([]byte(`{"callID":"llm.complete","payload":{}}`))
	if err != nil {
		t.Fatalf("ServeReverse: %v", err)
	}
	if string(resp) != "{}" {
		t.Errorf("ServeReverse resp = %q, want {}", resp)
	}
}

func TestHostBridgeServeReverseMalformed(t *testing.T) {
	b := NewHostBridge(nil, nil, "", nil)
	if _, err := b.ServeReverse([]byte(`not json`)); err == nil {
		t.Fatal("ServeReverse with malformed frame body must error")
	}
}

func TestServeReverseParityWithServe(t *testing.T) {
	// The IPC reverse-resp body must be byte-identical to what the FFI
	// serve path writes into the C response buffer — for success, denied,
	// dispatch-failure and empty responses alike — so dev/prod wire formats
	// stay aligned.
	cases := []struct {
		name     string
		allowed  map[string]struct{}
		dispatch HostDispatchFunc
		callID   string
		req      string
	}{
		{
			name:    "success",
			allowed: map[string]struct{}{"llm.invoke": {}},
			dispatch: func(string, []byte) ([]byte, error) {
				return []byte(`{"result":"ok"}`), nil
			},
			callID: "llm.complete",
			req:    `{"prompt":"hi"}`,
		},
		{
			name:    "dispatch error",
			allowed: map[string]struct{}{"llm.invoke": {}},
			dispatch: func(string, []byte) ([]byte, error) {
				return nil, fmt.Errorf("upstream boom")
			},
			callID: "llm.complete",
			req:    `{}`,
		},
		{
			name:     "denied",
			allowed:  nil,
			dispatch: func(string, []byte) ([]byte, error) { return []byte(`{"x":1}`), nil },
			callID:   "llm.complete",
			req:      `{}`,
		},
		{
			name:    "empty response",
			allowed: map[string]struct{}{"llm.invoke": {}},
			dispatch: func(string, []byte) ([]byte, error) {
				return nil, nil
			},
			callID: "llm.complete",
			req:    `{}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewHostBridge(tc.allowed, tc.dispatch, "", nil)

			// FFI side: serve writes the payload into a C buffer.
			cid := append([]byte(tc.callID), 0)
			req := append([]byte(tc.req), 0)
			res := make([]byte, 1024)
			n := b.serve(&cid[0], &req[0], &res[0], int32(len(res)))
			if n < 0 {
				t.Fatalf("serve returned %d", n)
			}
			ffi := res[:n]

			// IPC side: ServeReverse returns the 0x04 frame body.
			frameBody, err := json.Marshal(ReverseReqBody{CallID: tc.callID, Payload: json.RawMessage(tc.req)})
			if err != nil {
				t.Fatalf("marshal frame body: %v", err)
			}
			ipc, err := b.ServeReverse(frameBody)
			if err != nil {
				t.Fatalf("ServeReverse: %v", err)
			}
			if string(ipc) != string(ffi) {
				t.Errorf("IPC payload %q != FFI payload %q", ipc, ffi)
			}
		})
	}
}

func TestServeReverseFrameRoundTrip(t *testing.T) {
	// Integration with the shared transport codec: ServeReverse's returned
	// body written as a 0x04 reverse-resp frame must come back byte-identical.
	b := NewHostBridge(map[string]struct{}{"llm.invoke": {}}, func(string, []byte) ([]byte, error) {
		return []byte(`{"result":"ok"}`), nil
	}, "", nil)
	resp, err := b.ServeReverse([]byte(`{"callID":"llm.complete","payload":{}}`))
	if err != nil {
		t.Fatalf("ServeReverse: %v", err)
	}
	r, w := io.Pipe()
	defer r.Close()
	go func() {
		_ = WriteTransportFrame(w, MsgReverseResp, "rt1", resp)
		_ = w.Close()
	}()
	frame, err := ReadTransportFrame(r)
	if err != nil {
		t.Fatalf("ReadTransportFrame: %v", err)
	}
	if frame.Type != MsgReverseResp {
		t.Errorf("frame type = 0x%02x, want 0x%02x (MsgReverseResp)", frame.Type, MsgReverseResp)
	}
	if frame.CallID != "rt1" {
		t.Errorf("frame callID = %q, want rt1", frame.CallID)
	}
	if string(frame.Payload) != string(resp) {
		t.Errorf("frame payload %q != ServeReverse result %q", frame.Payload, resp)
	}
}

func TestNewTranslatedServiceDispatch(t *testing.T) {
	var gotCallID string
	base := func(callID string, req []byte) ([]byte, error) {
		gotCallID = callID
		return []byte(`{"ok":true}`), nil
	}
	dispatch := NewTranslatedServiceDispatch(base, hostBridgeDispatchTranslations)

	cases := []struct {
		callID string
		want   string
	}{
		{"project.read_file", "filesystem.read"},
		{"project.write_file", "filesystem.write"},
		{"provider.list", "aimanager.provider_list"},
		{"aggregator.list", "aimanager.aggregator_list"},
		{"aggregator.get", "aimanager.aggregator_get"},
		{"llm.complete", "aiaggregator.dispatch"},
		{"llm.chat", "aiaggregator.dispatch"},
		{"voice.accounts.list", "voice.list_accounts"},
		{"media.list_units", "aimanager.list_units"},
		{"media.accounts.list", "media.list_accounts"},
		{"shell.exec", "shell.exec"},
	}
	for _, tc := range cases {
		t.Run(tc.callID, func(t *testing.T) {
			gotCallID = ""
			_, err := dispatch(tc.callID, nil)
			if err != nil {
				t.Fatalf("dispatch %q: %v", tc.callID, err)
			}
			if gotCallID != tc.want {
				t.Errorf("dispatch got callID %q, want %q", gotCallID, tc.want)
			}
		})
	}
}

func TestHostBridgeDispatchUnknownCallIDError(t *testing.T) {
	b := NewHostBridge(map[string]struct{}{"llm.invoke": {}}, nil, "", nil)
	_, err := b.Dispatch("unknown.call", nil)
	if err == nil {
		t.Fatal("expected error for unknown callID")
	}
	if !strings.Contains(err.Error(), "no host-service mapping") {
		t.Errorf("error = %q, want no host-service mapping", err.Error())
	}
}

func TestHostBridgeDispatchCapabilityDeniedError(t *testing.T) {
	b := NewHostBridge(map[string]struct{}{}, nil, "", nil)
	_, err := b.Dispatch("llm.complete", nil)
	if err == nil {
		t.Fatal("expected error for denied capability")
	}
	if !strings.Contains(err.Error(), "capability not granted") {
		t.Errorf("error = %q, want capability not granted", err.Error())
	}
}

func TestHandleHostBridgeConfigGet(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	defer config.SetDataDirForTest("")

	a := &Actor{}
	resp, err := a.handleHostBridgeConfigGet([]byte(`{"scope":"host","key":"data_dir"}`))
	if err != nil {
		t.Fatalf("config.get: %v", err)
	}
	var got string
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("unmarshal resp %q: %v", resp, err)
	}
	if got != config.DataDir() {
		t.Errorf("data_dir = %q, want %q", got, config.DataDir())
	}
}

func TestHandleHostBridgeConfigSetReadOnly(t *testing.T) {
	a := &Actor{}
	_, err := a.handleHostBridgeConfigSet([]byte(`{"scope":"host","key":"data_dir","value":"/x"}`))
	if err == nil {
		t.Fatal("expected config.set to be read-only")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("error = %q, want read-only", err.Error())
	}
}

// TestHostBridgeBundleCallAuthorization pins the second authorization path
// for plugin.* callIDs: the exact-callID gate must allow only the declared
// plugin.<target>.<callable> entries, never other plugins even when the
// capability (bundle.invoke) would match. Dispatch routes authorized calls
// through the bundle dispatch, never the regular host dispatch.
func TestHostBridgeBundleCallAuthorization(t *testing.T) {
	// The allowed set carries both capability strings and verbatim plugin.*
	// callIDs (DerivedCapabilities keeps the latter); NewHostBridge splits
	// them automatically — no separate wiring needed.
	b := NewHostBridge(map[string]struct{}{
		"plugin.translator.translate": {},
	}, nil, "caller.app", nil)

	// Capability mapping: plugin.* callIDs gate to bundle.invoke.
	if got := appbinding.HostCallCapability("plugin.translator.translate"); got != appbinding.CapBundleInvoke {
		t.Fatalf("HostCallCapability(plugin.translator.translate) = %q, want bundle.invoke", got)
	}

	// Exact-callID authorization: declared callID allowed, sibling denied.
	if !b.Allows("plugin.translator.translate") {
		t.Error("declared plugin.translator.translate must be allowed")
	}
	if b.Allows("plugin.translator.summarize") {
		t.Error("undeclared plugin.translator.summarize must be denied (exact-callID gate)")
	}
	if b.Allows("plugin.other.callable") {
		t.Error("undeclared plugin.other.callable must be denied")
	}

	// Dispatch: authorized callID routes to bundle dispatch.
	b.SetBundleDispatch(func(callID string, req []byte) ([]byte, error) {
		if callID != "plugin.translator.translate" {
			t.Errorf("bundle dispatch received %q", callID)
		}
		return []byte(`{"ok":true}`), nil
	})
	resp, err := b.Dispatch("plugin.translator.translate", []byte(`{}`))
	if err != nil {
		t.Fatalf("bundle dispatch: %v", err)
	}
	if string(resp) != `{"ok":true}` {
		t.Errorf("bundle response = %q", resp)
	}

	// Dispatch: undeclared callID denied before reaching bundle dispatch.
	if _, err := b.Dispatch("plugin.other.callable", nil); err == nil {
		t.Error("undeclared bundle callID must fail")
	}

	// Dispatch without bundle dispatch wired: declared callID still denied.
	b2 := NewHostBridge(map[string]struct{}{"plugin.translator.translate": {}}, nil, "caller.app", nil)
	if _, err := b2.Dispatch("plugin.translator.translate", nil); err == nil {
		t.Error("bundle call without bundleDispatch must fail")
	} else if !strings.Contains(err.Error(), "bundle dispatch not configured") {
		t.Errorf("error = %q, want bundle dispatch not configured", err.Error())
	}
}

// TestDerivedCapabilitiesBundlePassthrough pins that DerivedCapabilities
// keeps plugin.* entries verbatim instead of collapsing them to the
// bundle.invoke capability — the exact callIDs are the authorization gates.
func TestDerivedCapabilitiesBundlePassthrough(t *testing.T) {
	got := appbinding.DerivedCapabilities([]string{
		"plugin.translator.translate", "plugin.translator.summarize", "llm.complete",
	})
	want := []string{"llm.invoke", "plugin.translator.summarize", "plugin.translator.translate"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("DerivedCapabilities = %v, want %v", got, want)
	}
	if !appbinding.IsKnownHostCapability("plugin.any.callable") {
		t.Error("IsKnownHostCapability must accept plugin.* bundle callIDs")
	}
}

// TestHostBridgeMediaGenerateInjectsPluginID pins the image/video.generate
// routing: they are plugin-aware local services, so the bridge injects the
// calling plugin identity before storeDispatch — the handler must never
// trust a Plugin field supplied by the payload itself.
func TestHostBridgeMediaGenerateInjectsPluginID(t *testing.T) {
	for _, callID := range []string{"image.generate", "video.generate"} {
		var gotCallID string
		var gotReq []byte
		b := NewHostBridge(
			map[string]struct{}{"image.gen": {}, "video.gen": {}},
			nil,
			"app.demo",
			func(cid string, req []byte) ([]byte, error) {
				gotCallID = cid
				gotReq = req
				return []byte(`{"Path":"media/x.png"}`), nil
			},
		)
		resp, err := b.Dispatch(callID, []byte(`{"Prompt":"a cat","Plugin":"forged"}`))
		if err != nil {
			t.Fatalf("Dispatch %s: %v", callID, err)
		}
		if gotCallID != callID {
			t.Errorf("dispatch got callID %q, want %q", gotCallID, callID)
		}
		var got struct {
			Plugin string `json:"Plugin"`
			Prompt string `json:"Prompt"`
		}
		if err := json.Unmarshal(gotReq, &got); err != nil {
			t.Fatalf("injected req decode: %v", err)
		}
		if got.Plugin != "app.demo" {
			t.Errorf("injected Plugin = %q, want app.demo (payload-forged Plugin must be overwritten)", got.Plugin)
		}
		if string(resp) != `{"Path":"media/x.png"}` {
			t.Errorf("dispatch resp = %q", resp)
		}
	}
}

// TestHostBridgeMediaGenerateDeniedWithoutCapability pins the deny-all
// default for the generation callIDs: without the capability grant the local
// dispatch is never reached.
func TestHostBridgeMediaGenerateDeniedWithoutCapability(t *testing.T) {
	for _, callID := range []string{"image.generate", "video.generate"} {
		called := false
		b := NewHostBridge(nil, nil, "app.demo", func(string, []byte) ([]byte, error) {
			called = true
			return []byte("{}"), nil
		})
		if _, err := b.Dispatch(callID, []byte(`{"Prompt":"x"}`)); err == nil {
			t.Fatalf("%s with empty allowed set must error", callID)
		}
		if called {
			t.Errorf("dispatch function must not be called for denied %s", callID)
		}
	}
}
