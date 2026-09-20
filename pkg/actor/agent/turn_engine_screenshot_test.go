package agent

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestScreenshotMime(t *testing.T) {
	cases := map[string]string{
		"jpeg": "image/jpeg",
		"jpg":  "image/jpeg",
		"PNG":  "image/png",
		" webp ": "image/webp",
		"":     "image/jpeg",
		"bmp":  "image/jpeg",
	}
	for in, want := range cases {
		if got := screenshotMime(in); got != want {
			t.Errorf("screenshotMime(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestScreenshotObservation locks the screenshot → LLM embedding contract: the
// tool_result text is compact metadata without the base64 payload, and the
// image rides a user-role observation message with a data URL — the same
// content shape as user-submitted chat images.
func TestScreenshotObservation(t *testing.T) {
	resp := domain.ComputerUseScreenshotResp{
		ImageBytes:   []byte("fake-jpeg-bytes"),
		Format:       "jpeg",
		Width:        800,
		Height:       600,
		CursorX:      10,
		CursorY:      20,
		DisplayName:  "Display 1",
		Message:      "Annotation enabled.",
	}
	metaText, obs := screenshotObservation(resp)
	if obs == nil {
		t.Fatal("observation = nil, want message")
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(metaText), &meta); err != nil {
		t.Fatalf("metadata is not JSON: %v", err)
	}
	if _, ok := meta["ImageBytes"]; ok {
		t.Error("metadata contains ImageBytes; base64 payload must never inline into tool_result")
	}
	if meta["Width"].(float64) != 800 || meta["Height"].(float64) != 600 {
		t.Errorf("metadata dimensions = %v/%v, want 800/600", meta["Width"], meta["Height"])
	}
	if meta["Note"] == "" {
		t.Error("metadata Note is empty")
	}

	if obs.Role != domain.ChatRoleUser {
		t.Errorf("observation role = %q, want user", obs.Role)
	}
	if len(obs.Content) != 2 {
		t.Fatalf("observation has %d blocks, want 2 (text + image)", len(obs.Content))
	}
	if obs.Content[0].Type != domain.ContentBlockText || !strings.Contains(obs.Content[0].Text, "computeruse.screenshot") {
		t.Errorf("observation first block = %+v, want text tag", obs.Content[0])
	}
	img := obs.Content[1]
	wantURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(resp.ImageBytes)
	if img.Type != domain.ContentBlockImage || img.ImageURL != wantURL || img.MimeType != "image/jpeg" {
		t.Errorf("observation image block = %+v", img)
	}
}

func TestScreenshotObservation_NoImageReturnsNil(t *testing.T) {
	if _, obs := screenshotObservation(domain.ComputerUseScreenshotResp{Unchanged: true}); obs != nil {
		t.Error("Unchanged response produced an observation, want nil")
	}
	if _, obs := screenshotObservation(domain.ComputerUseScreenshotResp{}); obs != nil {
		t.Error("empty ImageBytes produced an observation, want nil")
	}
}

// TestApplySingleToolResult_AppendsScreenshotObservation verifies the engine
// wiring: after the tool_result message, the screenshot observation message is
// appended to history so the next dispatch sees tool result followed by the
// user-role image message.
func TestApplySingleToolResult_AppendsScreenshotObservation(t *testing.T) {
	resp := domain.ComputerUseScreenshotResp{
		ImageBytes: []byte("fake-jpeg-bytes"),
		Format:     "jpeg",
		Width:      100,
		Height:     50,
	}
	metaText, obs := screenshotObservation(resp)
	if obs == nil {
		t.Fatal("screenshotObservation returned nil observation")
	}

	eng := &turnEngine{logger: newNopActorLogger()}
	eng.openToolCalls = 1
	result := toolExecutionResult{
		call:        pendingToolCall{ID: "tc-1", CallableID: "computeruse.screenshot", ServiceName: "computeruse"},
		out:         metaText,
		observation: obs,
	}
	step := domain.TurnAction{ID: "s-1", Kind: "tool_call"}

	ctx := testutil.AnonCtx(testutil.GenActorID())
	if err := eng.applySingleToolResult(ctx, "turn-1", result, step); err != nil {
		t.Fatalf("applySingleToolResult: %v", err)
	}

	if len(eng.history) != 2 {
		t.Fatalf("history length = %d, want 2 (tool_result + observation)", len(eng.history))
	}
	toolMsg := eng.history[0]
	if toolMsg.Role != "tool" || len(toolMsg.Content) != 1 || toolMsg.Content[0].Type != domain.ContentBlockToolResult {
		t.Fatalf("first message = %+v, want tool_result", toolMsg)
	}
	if toolMsg.Content[0].Text != metaText || strings.Contains(toolMsg.Content[0].Text, "fake-jpeg-bytes") {
		t.Errorf("tool_result text = %q", toolMsg.Content[0].Text)
	}
	obsMsg := eng.history[1]
	if obsMsg.Role != domain.ChatRoleUser {
		t.Errorf("second message role = %q, want user", obsMsg.Role)
	}
	found := false
	for _, b := range obsMsg.Content {
		if b.Type == domain.ContentBlockImage && strings.HasPrefix(b.ImageURL, "data:image/jpeg;base64,") {
			found = true
		}
	}
	if !found {
		t.Errorf("observation message has no image block: %+v", obsMsg.Content)
	}
	if obsMsg.ID == "" {
		t.Error("observation message needs an ID: the persisted step and the recognition write-back both key on it")
	}
	if eng.openToolCalls != 0 {
		t.Errorf("openToolCalls = %d, want 0", eng.openToolCalls)
	}

	// The observation must also surface as a persisted user_inject step so the
	// UI renders the screenshot inside the assistant turn envelope and the
	// rebuild restores the image into history.
	var opened, appended, closed bool
	var openedBlock domain.ContentBlock
	for _, ev := range eng.stepEvents {
		switch ev.Kind {
		case "step.opened":
			if ev.StepID == obsMsg.ID && ev.Role == "assistant" && ev.StepType == "user_inject" && ev.Meta == "screenshot" {
				opened = true
				if ev.Block != nil {
					openedBlock = *ev.Block
				}
			}
		case "block.appended":
			if ev.StepID == obsMsg.ID {
				appended = true
			}
		case "step.closed":
			if ev.StepID == obsMsg.ID {
				closed = true
			}
		}
	}
	if !opened || !appended || !closed {
		t.Fatalf("observation step events missing: opened=%v appended=%v closed=%v (events=%d)", opened, appended, closed, len(eng.stepEvents))
	}
	if openedBlock.Type != domain.ContentBlockText {
		t.Errorf("first emitted block = %+v, want the text caption (image arrives via block.appended)", openedBlock)
	}
}
