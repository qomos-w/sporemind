package llmclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestZZProbePayload prints the lowered OpenAI payload for a representative
// agent conversation to inspect exact wire shapes.
func TestZZProbePayload(t *testing.T) {
	var raw string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	req := Request{
		Model: "glm-5.3",
		System: "sys",
		Messages: []Message{
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "look at this"}, {Type: BlockTypeImage, ImageURL: "https://x/y.png"}}},
			{Role: RoleAssistant, ReasoningContent: "thinking", Content: []Block{{Type: BlockTypeText, Text: "calling"}, {Type: BlockTypeToolUse, ToolUseID: "c1", ToolName: "f", Input: "{}"}}},
			{Role: RoleTool, Content: []Block{{Type: BlockTypeToolResult, ToolUseID: "c1", Text: "ok"}}},
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "again"}}},
		},
		Tools: []ToolSpec{{Name: "f", Description: "d", InputSchema: `{"type":"object"}`}},
	}
	s, err := c.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range s.Events() {
	}
	s.Close()
	var pretty any
	_ = json.Unmarshal([]byte(raw), &pretty)
	out, _ := json.MarshalIndent(pretty, "", "  ")
	t.Logf("PAYLOAD:\n%s", out)
}
