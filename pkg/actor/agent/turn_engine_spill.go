package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// Spill thresholds. A tool result longer than spillThresholdChars (in runes)
// has its full text persisted to a session-scoped file and the history entry
// replaced with a head/tail preview plus a retrieval hint. Values align with
// the dispatch-time toolResultLimit so buildDispatchHistory's hard truncation
// almost never has to fire.
const (
	spillThresholdChars = 16000
	spillHeadChars      = 2000
	spillTailChars      = 1000
)

// spillMessageIfNeeded replaces oversized tool_result text in msg with a
// head/tail preview plus a locator to the spilled full text. Best-effort: on
// any storage failure the original text is kept inline (the harness spill
// policy treats a failed save as "keep the inline result", never as an error).
func (e *turnEngine) spillMessageIfNeeded(msg *domain.ChatMessage) {
	if msg.Role != "tool" {
		return
	}
	for i := range msg.Content {
		b := &msg.Content[i]
		if b.Type != domain.ContentBlockToolResult || utf8.RuneCountInString(b.Text) <= spillThresholdChars {
			continue
		}
		path, err := writeSpillFile(e.agentID, e.turnID, b.ToolUseID, b.Text)
		if err != nil {
			if e.logger != nil {
				e.logger.Warn("turnEngine: spill failed, keeping inline tool result", "err", err.Error())
			}
			continue
		}
		b.Text = spillPreview(b.Text, path)
	}
}

// writeSpillFile persists the full tool result text under the actor data dir:
// <ActorDataDir>/spill/<agentID>/<turnID>-<callID>-<rand>.txt. The directory is
// agent-scoped (0700) and the file is written with 0600.
func writeSpillFile(agentID, turnID, callID, text string) (string, error) {
	dir := filepath.Join(config.ActorDataDir(), "spill", sanitizeSpillSegment(agentID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("spill: mkdir: %w", err)
	}
	name := fmt.Sprintf("%s-%s-%s.txt",
		sanitizeSpillSegment(turnID), sanitizeSpillSegment(callID), randHex(4))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", fmt.Errorf("spill: write: %w", err)
	}
	return path, nil
}

// spillPreview builds the model-facing replacement for a spilled tool result:
// head, marker with the total byte length and the artifact path, tail, and a
// concrete retrieval recipe. Pure function (unit-tested in isolation).
func spillPreview(text, path string) string {
	runes := []rune(text)
	head := string(runes[:spillHeadChars])
	tail := string(runes[len(runes)-spillTailChars:])
	var b strings.Builder
	b.WriteString(head)
	b.WriteString("\n\n...[output too large for context; ")
	fmt.Fprintf(&b, "%d bytes spilled to %s]\n", len(text), path)
	b.WriteString(tail)
	b.WriteString("\n\n[full output is at the path above; read it in ranges with a shell command, e.g. sed -n '1,200p' <path>, or grep it]")
	return b.String()
}

// sanitizeSpillSegment reduces an arbitrary id to a single safe path segment.
func sanitizeSpillSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" || out == "." || out == ".." {
		return "x"
	}
	return out
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b)
}
