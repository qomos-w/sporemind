package agent

import (
	"encoding/json"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// decodeLikeHandler reproduces the gospore handler's json.Unmarshal into
// gen.AppManagerInvokeReq — the exact step that failed with a raw object
// Payload ("cannot unmarshal object into ... of type []uint8").
func decodeLikeHandler(t *testing.T, out string) gen.AppManagerInvokeReq {
	t.Helper()
	var req gen.AppManagerInvokeReq
	if err := json.Unmarshal([]byte(out), &req); err != nil {
		t.Fatalf("handler-style decode of encoded input failed: %v", err)
	}
	return req
}

func TestEncodeAppManagerInvokeInput_ObjectPayload(t *testing.T) {
	out, err := encodeAppManagerInvokeInput(`{"Id":"app.captcha","Callable":"stats","Payload":{"Page":1,"Limit":10}}`)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := decodeLikeHandler(t, out)
	if req.ID != "app.captcha" || req.Callable != "stats" {
		t.Fatalf("unexpected request: %+v", req)
	}
	if string(req.Payload) != `{"Page":1,"Limit":10}` {
		t.Fatalf("want payload bytes %q, got %q", `{"Page":1,"Limit":10}`, string(req.Payload))
	}
}

func TestEncodeAppManagerInvokeInput_ArrayPayload(t *testing.T) {
	out, err := encodeAppManagerInvokeInput(`{"Id":"app.captcha","Callable":"stats","Payload":[1,2,3]}`)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := decodeLikeHandler(t, out)
	if string(req.Payload) != `[1,2,3]` {
		t.Fatalf("want payload bytes [1,2,3], got %q", string(req.Payload))
	}
}

func TestEncodeAppManagerInvokeInput_EmptyPayload(t *testing.T) {
	out, err := encodeAppManagerInvokeInput(`{"Id":"app.captcha","Callable":"stats"}`)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := decodeLikeHandler(t, out)
	if req.Callable != "stats" {
		t.Fatalf("unexpected request: %+v", req)
	}
	if string(req.Payload) != "{}" {
		t.Fatalf("want empty payload bytes {}, got %q", string(req.Payload))
	}
}

// TestEncodeAppManagerInvokeInput_PreservesIdentity pins the regression where
// the re-encoder dropped AgentId (stamped by injectCallerAgentID) and any
// explicit Role/ProjectId, so agent-originated invokes were denied with
// identity_incomplete at resolveCaller.
func TestEncodeAppManagerInvokeInput_PreservesIdentity(t *testing.T) {
	out, err := encodeAppManagerInvokeInput(`{"Id":"app.totp","Callable":"provision","AgentId":"01AGENT","Role":"agent","ProjectId":"01PROJ","Payload":{"account":"a"}}`)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := decodeLikeHandler(t, out)
	if req.AgentID != "01AGENT" {
		t.Fatalf("AgentID = %q, want 01AGENT (dropped by re-encoder)", req.AgentID)
	}
	if req.Role != "agent" || req.ProjectID != "01PROJ" {
		t.Fatalf("Role/ProjectID dropped: %+v", req)
	}
}

func TestEncodeAppManagerInvokeInput_EmptyObjectPayload(t *testing.T) {
	out, err := encodeAppManagerInvokeInput(`{"Id":"app.captcha","Callable":"stats","Payload":{}}`)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := decodeLikeHandler(t, out)
	if string(req.Payload) != "{}" {
		t.Fatalf("want payload bytes {}, got %q", string(req.Payload))
	}
}

func TestEncodeAppManagerInvokeInput_Base64Payload(t *testing.T) {
	// base64 of `{"ok":true}`.
	out, err := encodeAppManagerInvokeInput(`{"Id":"app.captcha","Callable":"stats","Payload":"eyJvayI6dHJ1ZX0="}`)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := decodeLikeHandler(t, out)
	if string(req.Payload) != `{"ok":true}` {
		t.Fatalf("want base64-decoded payload %q, got %q", `{"ok":true}`, string(req.Payload))
	}
}

// TestEncodeAppManagerInvokeInput_JSONContentAsStringPayload pins the bridge
// regression where a JSON-content string payload ("{}" or "{\"Name\":\"x\"}")
// was treated as base64 and rejected with "illegal base64 data at input byte 0"
// before reaching the plugin. Such strings must fall through to their literal
// bytes so the handler decodes the JSON normally.
func TestEncodeAppManagerInvokeInput_JSONContentAsStringPayload(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty object string", `{"Id":"app.plan-usage","Callable":"list_plans","Payload":"{}"}`, `{}`},
		{"object string", `{"Id":"app.plan-usage","Callable":"list_plans","Payload":"{\"Name\":\"x\"}"}`, `{"Name":"x"}`},
		{"array string", `{"Id":"app.plan-usage","Callable":"list_plans","Payload":"[1,2,3]"}`, `[1,2,3]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := encodeAppManagerInvokeInput(c.input)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			req := decodeLikeHandler(t, out)
			if string(req.Payload) != c.want {
				t.Fatalf("want payload bytes %q, got %q", c.want, string(req.Payload))
			}
		})
	}
}

func TestEncodeAppManagerInvokeInput_WrongCallable(t *testing.T) {
	// The bridge only forwards; the appmanager handler rejects the unknown
	// callable after decoding. Decode must succeed so that rejection happens
	// with the intended error, not a payload decode error.
	out, err := encodeAppManagerInvokeInput(`{"Id":"app.captcha","Callable":"nonexistent","Payload":{}}`)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := decodeLikeHandler(t, out)
	if req.Callable != "nonexistent" {
		t.Fatalf("want callable nonexistent, got %q", req.Callable)
	}
	if string(req.Payload) != "{}" {
		t.Fatalf("want payload bytes {}, got %q", string(req.Payload))
	}
}

func TestEncodeAppManagerInvokeInput_MalformedInput(t *testing.T) {
	if _, err := encodeAppManagerInvokeInput(`{"Id":"app.captcha","Callable":`); err == nil {
		t.Fatal("expected error for malformed JSON input")
	}
}
