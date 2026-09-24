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

// TestGlassInteractRenderSpeakDebugEndToEnd drives the Stage 4 surface over
// the real runtime: Coordinator-internal render/speak, the capabilities
// projection, the public debug.state callable, and lastFrame restore on a
// same-session reconnect.
func TestGlassInteractRenderSpeakDebugEndToEnd(t *testing.T) {
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

	// Bootstrap + claim a glass session that reports audio + display caps.
	raw, err := invokeCall(t, handle, "glass_interact.bootstrap", map[string]any{
		"Key":      "e2e-key",
		"DeviceId": "dev-e2e",
	}, "", "")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	var boot struct {
		SessionID string `json:"SessionId"`
	}
	if err := json.Unmarshal(raw, &boot); err != nil {
		t.Fatalf("bootstrap parse: %v", err)
	}
	if _, err := invokeCall(t, handle, "glass_interact.session_claim", map[string]any{
		"SessionId": boot.SessionID,
		"DeviceId":  "dev-e2e",
		"Capabilities": []map[string]any{
			{"Name": "native_text", "Version": "1.0"},
			{"Name": "audio", "Features": []string{"vad", "pcm"}},
		},
	}, "glass", boot.SessionID); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// 1. Coordinator-internal render applies a frame.
	renderRaw, err := invokeCall(t, handle, "glass_interact.render", map[string]any{
		"Frame": map[string]any{"Text": "天气晴朗", "Layout": "top"},
	}, "", "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var renderResp struct {
		SessionID  string `json:"SessionId"`
		Generation int64  `json:"Generation"`
		Applied    bool   `json:"Applied"`
	}
	if err := json.Unmarshal(renderRaw, &renderResp); err != nil {
		t.Fatalf("render parse: %v raw=%s", err, renderRaw)
	}
	if !renderResp.Applied || renderResp.SessionID != boot.SessionID || renderResp.Generation != 1 {
		t.Fatalf("render resp = %+v", renderResp)
	}

	// 2. A second render overwrites the first (latest-only).
	if _, err := invokeCall(t, handle, "glass_interact.render", map[string]any{
		"Frame": map[string]any{"Text": "最新一帧", "Layout": "bottom"},
	}, "", ""); err != nil {
		t.Fatalf("second render: %v", err)
	}

	// 3. speak is at-most-once per message id.
	speakResp1, err := invokeCall(t, handle, "glass_interact.speak", map[string]any{
		"Text":      "好的",
		"MessageId": "msg_e2e",
	}, "", "")
	if err != nil {
		t.Fatalf("speak: %v", err)
	}
	var speak1 struct {
		MessageID string `json:"MessageId"`
		Duplicate bool   `json:"Duplicate"`
	}
	if err := json.Unmarshal(speakResp1, &speak1); err != nil {
		t.Fatalf("speak parse: %v", err)
	}
	if speak1.Duplicate {
		t.Fatalf("first speak flagged duplicate: %+v", speak1)
	}
	speakResp2, err := invokeCall(t, handle, "glass_interact.speak", map[string]any{
		"Text":      "好的",
		"MessageId": "msg_e2e",
	}, "", "")
	if err != nil {
		t.Fatalf("duplicate speak: %v", err)
	}
	var speak2 struct {
		MessageID string `json:"MessageId"`
		Duplicate bool   `json:"Duplicate"`
	}
	if err := json.Unmarshal(speakResp2, &speak2); err != nil {
		t.Fatalf("duplicate speak parse: %v", err)
	}
	if !speak2.Duplicate || speak2.MessageID != "msg_e2e" {
		t.Fatalf("duplicate speak resp = %+v", speak2)
	}

	// 4. Capabilities projection reflects the claim.
	capsRaw, err := invokeCall(t, handle, "glass_interact.capabilities_get", map[string]any{}, "", "")
	if err != nil {
		t.Fatalf("capabilities.get: %v", err)
	}
	var capsResp struct {
		State struct {
			Active       bool   `json:"Active"`
			SessionID    string `json:"SessionId"`
			DeviceID     string `json:"DeviceId"`
			Generation   int64  `json:"Generation"`
			Capabilities []struct {
				Name string `json:"Name"`
			} `json:"Capabilities"`
		} `json:"State"`
	}
	if err := json.Unmarshal(capsRaw, &capsResp); err != nil {
		t.Fatalf("capabilities.parse: %v raw=%s", err, capsRaw)
	}
	if !capsResp.State.Active || capsResp.State.SessionID != boot.SessionID || capsResp.State.DeviceID != "dev-e2e" {
		t.Fatalf("capabilities state = %+v", capsResp.State)
	}
	if len(capsResp.State.Capabilities) != 2 || capsResp.State.Capabilities[1].Name != "audio" {
		t.Fatalf("capabilities = %+v", capsResp.State.Capabilities)
	}

	// 5. debug.state exposes session/frame/stats/timeline, secret-free, and is
	// developer-gated: call it with a developer role.
	dbgRaw, err := invokeCall(t, handle, "glass_interact.debug_state", map[string]any{}, "developer", "")
	if err != nil {
		t.Fatalf("debug.state: %v", err)
	}
	var dbg struct {
		State struct {
			Session *struct {
				SessionID string `json:"SessionId"`
				Online    bool   `json:"Online"`
			} `json:"Session"`
			CurrentFrame *struct {
				Text   string `json:"Text"`
				Layout string `json:"Layout"`
			} `json:"CurrentFrame"`
			Stats struct {
				Renders      int64 `json:"Renders"`
				Speaks       int64 `json:"Speaks"`
				SpeakDeduped int64 `json:"SpeakDeduped"`
			} `json:"Stats"`
			Timeline []struct {
				Kind string `json:"Kind"`
			} `json:"Timeline"`
		} `json:"State"`
	}
	if err := json.Unmarshal(dbgRaw, &dbg); err != nil {
		t.Fatalf("debug.parse: %v raw=%s", err, dbgRaw)
	}
	st := dbg.State
	if st.Session == nil || st.Session.SessionID != boot.SessionID || !st.Session.Online {
		t.Fatalf("debug session = %+v", st.Session)
	}
	if st.CurrentFrame == nil || st.CurrentFrame.Text != "最新一帧" || st.CurrentFrame.Layout != "bottom" {
		t.Fatalf("debug current frame = %+v (want latest-only)", st.CurrentFrame)
	}
	if st.Stats.Renders != 2 || st.Stats.Speaks != 1 || st.Stats.SpeakDeduped != 1 {
		t.Fatalf("debug stats = %+v", st.Stats)
	}
	if len(st.Timeline) == 0 {
		t.Fatal("debug timeline empty")
	}

	// 6. Same-session reconnect restores lastFrame (persisted), and speak
	// dedup is still enforced at runtime (not replayed).
	claim2Raw, err := invokeCall(t, handle, "glass_interact.session_claim", map[string]any{
		"SessionId": boot.SessionID,
		"DeviceId":  "dev-e2e",
	}, "glass", boot.SessionID)
	if err != nil {
		t.Fatalf("reconnect claim: %v", err)
	}
	var claim2 struct {
		Reconnected bool `json:"Reconnected"`
		LastFrame   *struct {
			Text string `json:"Text"`
		} `json:"LastFrame"`
	}
	if err := json.Unmarshal(claim2Raw, &claim2); err != nil {
		t.Fatalf("reconnect claim parse: %v", err)
	}
	if !claim2.Reconnected || claim2.LastFrame == nil || claim2.LastFrame.Text != "最新一帧" {
		t.Fatalf("reconnect lastFrame restore = %+v", claim2)
	}
	speak3, err := invokeCall(t, handle, "glass_interact.speak", map[string]any{
		"Text":      "好的",
		"MessageId": "msg_e2e",
	}, "", "")
	if err != nil {
		t.Fatalf("speak after reconnect: %v", err)
	}
	var speak3Resp struct {
		Duplicate bool `json:"Duplicate"`
	}
	if err := json.Unmarshal(speak3, &speak3Resp); err != nil {
		t.Fatalf("speak after reconnect parse: %v", err)
	}
	if !speak3Resp.Duplicate {
		t.Fatal("speak message id was not suppressed after reconnect (replay risk)")
	}
}
