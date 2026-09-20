package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestSpillPreview(t *testing.T) {
	text := strings.Repeat("a", 11000) + "MIDDLE" + strings.Repeat("b", 3000)
	got := spillPreview(text, "/data/spill/ag1/turn-1-tu_9-abc.txt")

	if !strings.HasPrefix(got, strings.Repeat("a", 2000)) {
		t.Errorf("preview must start with the 2000-char head")
	}
	if !strings.Contains(got, strings.Repeat("b", 1000)) {
		t.Errorf("preview must end with the 1000-char tail")
	}
	if strings.Contains(got, "MIDDLE") {
		t.Errorf("preview must not carry mid-output content")
	}
	if !strings.Contains(got, "spilled to /data/spill/ag1/turn-1-tu_9-abc.txt") {
		t.Errorf("preview must carry the artifact path, got: %q", clip(got))
	}
	if !strings.Contains(got, "sed -n") {
		t.Errorf("preview must carry a retrieval recipe")
	}
}

func TestSpillMessageIfNeeded(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())

	e := &turnEngine{agentID: "ag/../evil", turnID: "turn-1", logger: nopLogger{}}
	long := strings.Repeat("x", 20000)
	msg := domain.ChatMessage{
		Role: "tool",
		Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: long},
			{Type: domain.ContentBlockToolResult, ToolUseID: "tu_1", Text: long},
		},
	}
	e.spillMessageIfNeeded(&msg)

	if msg.Content[0].Text != long {
		t.Errorf("non-tool_result blocks must be untouched")
	}
	replaced := msg.Content[1].Text
	if replaced == long {
		t.Fatal("oversized tool_result text must be replaced")
	}
	if !strings.Contains(replaced, "spilled to") {
		t.Errorf("replacement must reference the spill path, got: %q", clip(replaced))
	}

	// The spilled file must exist under the sanitized agent dir with the
	// verbatim full text.
	dir := filepath.Join(config.ActorDataDir(), "spill", "ag-..-evil")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one spill file under %s: %v", dir, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != long {
		t.Errorf("spill file must hold the full verbatim text (%d bytes)", len(long))
	}
	if !strings.HasPrefix(entries[0].Name(), "turn-1-tu_1-") {
		t.Errorf("spill filename must derive from turnID and callID, got %s", entries[0].Name())
	}
}

func TestSpillMessageIfNeededUnderThreshold(t *testing.T) {
	e := &turnEngine{agentID: "ag1", turnID: "t1", logger: nopLogger{}}
	short := strings.Repeat("y", 100)
	msg := domain.ChatMessage{Role: "tool", Content: []domain.ContentBlock{
		{Type: domain.ContentBlockToolResult, ToolUseID: "tu_2", Text: short},
	}}
	e.spillMessageIfNeeded(&msg)
	if msg.Content[0].Text != short {
		t.Errorf("under-threshold text must stay inline")
	}
}

func TestSanitizeSpillSegment(t *testing.T) {
	cases := map[string]string{
		"turn-1":    "turn-1",
		"a/b\\c":    "a-b-c",
		"..":        "x",
		"":          "x",
		"tu_id.123": "tu_id.123",
	}
	for in, want := range cases {
		if got := sanitizeSpillSegment(in); got != want {
			t.Errorf("sanitizeSpillSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

func clip(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
