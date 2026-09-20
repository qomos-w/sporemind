package glassinteract_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/actor/glassinteract"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// invokeCall performs an in-process ref.Invoke against the glassinteract
// service with optional caller identity headers (role/subject), mirroring
// what the /ws gateway produces for a glass token (role=glass,
// subject=session_id).
func invokeCall(t *testing.T, h *runtime.Handle, callID string, payload any, role, subject string) ([]byte, error) {
	t.Helper()
	ref, ok := h.App().LookupService("glass_interact")
	if !ok {
		t.Fatal("glassinteract service not found")
	}
	var hdrs map[string]string
	if role != "" {
		hdrs = map[string]string{"gospore.caller_role": role}
		if subject != "" {
			hdrs["gospore.caller_subject"] = subject
		}
	}
	call := ref.Invoke(context.Background(), callID, payload, hdrs)
	if call == nil {
		t.Fatalf("invoke %s returned nil call", callID)
	}
	defer call.Close()
	return call.RecvRaw()
}

// TestGlassInteractEndToEnd verifies the callable-level session flow through
// the real runtime + actorset: bootstrap issues a glass token, a glass
// identity claims the session, a different session replaces it, and the
// replaced session can no longer claim. WebSocket ownership is intentionally
// not simulated; claim semantics are the integration surface.
func TestGlassInteractEndToEnd(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	t.Setenv("SPOREMIND_GLASS_KEY", "e2e-key")

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{Name: "glassinteract", Factory: func() actor.Actor { return &glassinteract.Actor{} }, RequirePersistent: true, Role: "system", Planner: true},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()
	time.Sleep(500 * time.Millisecond)

	// 1. Bootstrap: valid key issues a glass token bound to a server session.
	raw, err := invokeCall(t, handle, "glass_interact.bootstrap", map[string]any{
		"Key":      "e2e-key",
		"DeviceId": "dev-e2e",
	}, "", "")
	if err != nil {
		t.Fatalf("bootstrap invoke: %v", err)
	}
	var boot struct {
		SessionID string `json:"SessionId"`
		Token     string `json:"Token"`
		Kind      string `json:"Kind"`
		Scope     string `json:"Scope"`
	}
	if err := json.Unmarshal(raw, &boot); err != nil {
		t.Fatalf("bootstrap response parse: %v raw=%s", err, raw)
	}
	if boot.SessionID == "" || boot.Token == "" || boot.Kind != "glass" || boot.Scope != "glass" {
		t.Fatalf("bootstrap resp = %+v", boot)
	}

	// 2. Bootstrap with a wrong key must fail.
	if _, err := invokeCall(t, handle, "glass_interact.bootstrap", map[string]any{"Key": "wrong"}, "", ""); err == nil {
		t.Fatal("bootstrap with wrong key succeeded")
	}

	// 3. Claim the session with the glass identity the gateway would produce.
	resp, err := invokeCall(t, handle, "glass_interact.session_claim", map[string]any{
		"SessionId": boot.SessionID,
		"DeviceId":  "dev-e2e",
	}, "glass", boot.SessionID)
	if err != nil {
		t.Fatalf("claim invoke: %v", err)
	}
	var claimed struct {
		Generation int64 `json:"Generation"`
		Online     bool  `json:"Online"`
	}
	if err := json.Unmarshal(resp, &claimed); err != nil {
		t.Fatalf("claim response parse: %v raw=%s", err, resp)
	}
	if !claimed.Online || claimed.Generation != 1 {
		t.Fatalf("claim resp = %+v, want online gen 1", claimed)
	}

	// 4. A second bootstrap + claim (new session) replaces the first.
	raw2, err := invokeCall(t, handle, "glass_interact.bootstrap", map[string]any{
		"Key":      "e2e-key",
		"DeviceId": "dev-e2e",
	}, "", "")
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	var boot2 struct {
		SessionID string `json:"SessionId"`
	}
	if err := json.Unmarshal(raw2, &boot2); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeCall(t, handle, "glass_interact.session_claim", map[string]any{
		"SessionId": boot2.SessionID,
		"DeviceId":  "dev-e2e",
	}, "glass", boot2.SessionID); err != nil {
		t.Fatalf("second claim: %v", err)
	}

	// 5. The replaced session cannot reclaim.
	if _, err := invokeCall(t, handle, "glass_interact.session_claim", map[string]any{
		"SessionId": boot.SessionID,
		"DeviceId":  "dev-e2e",
	}, "glass", boot.SessionID); err == nil {
		t.Fatal("replaced session claim succeeded, want rejection")
	}

	// 6. get_state reports the new session as the only active one.
	stRaw, err := invokeCall(t, handle, "glass_interact.session_get_state", map[string]any{}, "", "")
	if err != nil {
		t.Fatalf("get_state invoke: %v", err)
	}
	var state struct {
		Active  bool `json:"Active"`
		Session *struct {
			SessionID string `json:"SessionId"`
		} `json:"Session"`
	}
	if err := json.Unmarshal(stRaw, &state); err != nil {
		t.Fatalf("get_state parse: %v raw=%s", err, stRaw)
	}
	if !state.Active || state.Session == nil || state.Session.SessionID != boot2.SessionID {
		t.Fatalf("get_state = %+v, want active %s", state, boot2.SessionID)
	}
}
