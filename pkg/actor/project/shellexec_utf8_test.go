package project

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// invalidUTF8Reader yields arbitrary non-UTF-8 bytes once, then EOF. It
// imitates a Windows child process writing GBK-encoded text (e.g. Python
// printing CJK to a cp936 console) or a multibyte character split by a
// byte-boundary truncation.
type invalidUTF8Reader struct {
	data []byte
	sent bool
}

func (r *invalidUTF8Reader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	return copy(p, r.data), nil
}

// TestShellExec_InvalidUTF8OutputIsSanitized pins the fix for the
// "shell: stream ended without exit chunk" symptom: the JSON reply codec
// rejects invalid UTF-8, so a chunk carrying invalid bytes failed to encode
// and (with the emit error ignored) was dropped — including the terminal exit
// chunk, which discarded the whole command result. The exit chunk must now
// carry valid UTF-8 so it always encodes.
func TestShellExec_InvalidUTF8OutputIsSanitized(t *testing.T) {
	name, args := exitZeroCommand()
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}

	emit := testutil.NewFakeEmitter()
	// 0xC4 0xE3 0xBA 0xC3 = GBK "你好" — an invalid UTF-8 byte sequence.
	r := &invalidUTF8Reader{data: append([]byte{0xC4, 0xE3, 0xBA, 0xC3}, '\n')}

	if err := runDualStream(cmd, context.Background(), emit, r, r, time.Second, ""); err != nil {
		t.Fatalf("runDualStream: %v", err)
	}

	var exit domain.ShellChunk
	found := false
	for _, c := range emit.Chunks {
		ch, ok := c.(domain.ShellChunk)
		if !ok || ch.Kind != "exit" {
			continue
		}
		exit, found = ch, true
		if !utf8.ValidString(ch.Stdout) {
			t.Fatalf("streamed chunk not valid UTF-8: %q", ch.Stdout)
		}
	}
	if !found {
		t.Fatal("exit chunk not emitted")
	}
	if !utf8.ValidString(exit.Stdout) {
		t.Fatalf("exit stdout not valid UTF-8: %q", exit.Stdout)
	}
	if !strings.Contains(exit.Stdout, "\uFFFD") {
		t.Fatalf("invalid bytes were not replaced with U+FFFD: %q", exit.Stdout)
	}
	if !strings.Contains(exit.Stdout, "\n") {
		t.Fatalf("surrounding ASCII output lost: %q", exit.Stdout)
	}
}
