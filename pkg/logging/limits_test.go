package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/gateway"
)

func TestLevelEnabled(t *testing.T) {
	cases := []struct{ min, level string; want bool }{
		{"info", "debug", false},
		{"info", "info", true},
		{"info", "warn", true},
		{"info", "error", true},
		{"warn", "info", false},
		{"warn", "warn", true},
		{"debug", "debug", true},
		{"INFO", "Debug", false}, // case-insensitive
	}
	for _, c := range cases {
		if got := levelEnabled(c.min, c.level); got != c.want {
			t.Errorf("levelEnabled(%q,%q)=%v want %v", c.min, c.level, got, c.want)
		}
	}
}

func TestHookDropsBelowMinLevel(t *testing.T) {
	ring := NewRing(100)
	hook := NewHook(ring, "info")
	hook("debug", "dropped", "caller/x.go:1", nil)
	hook("info", "kept", "caller/x.go:2", nil)

	all := ring.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 entry (debug dropped), got %d", len(all))
	}
	if all[0].Message != "kept" {
		t.Fatalf("unexpected entry: %+v", all[0])
	}
}

// ensure NewHook still satisfies actor.LogHook
var _ actor.LogHook = NewHook(nil, "info")

func TestFileStoreFlushBeforeRead(t *testing.T) {
	dir := t.TempDir()
	s, err := newFileStore(dir, defaultMaxSize, time.Hour, 1<<30) // never auto-flush
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	for i := 0; i < 3; i++ {
		if err := s.Write(gateway.LogEntry{Timestamp: "2025-06-10T10:00:00Z", Level: "info", Message: "m"}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// No Close, no auto-flush yet. Tail must flush internally then read.
	tail, err := s.Tail(10)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(tail) != 3 {
		t.Fatalf("expected 3 tail entries after flush-before-read, got %d", len(tail))
	}
}

func TestFileStoreSizeRotation(t *testing.T) {
	dir := t.TempDir()
	// maxSize small enough that a few entries force multiple rotations.
	s, err := newFileStore(dir, 120, time.Hour, 1<<30)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	const n = 10
	for i := 0; i < n; i++ {
		if err := s.Write(gateway.LogEntry{
			Timestamp: "2025-06-10T10:00:0" + string(rune('0'+i)) + "Z",
			Level:     "info",
			Message:   "m" + string(rune('0'+i)),
		}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	_ = s.Flush()

	// At least one rotated sibling must exist alongside the bare file.
	entries, err := os.ReadDir(filepath.Join(dir, "backend"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected rotation to produce >1 file, got %d", len(entries))
	}

	tail, err := s.Tail(n)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(tail) != n {
		t.Fatalf("expected %d entries across rotated files, got %d", n, len(tail))
	}
	// Verify chronological order preserved across rotations.
	for i, e := range tail {
		want := "m" + string(rune('0'+i))
		if e.Message != want {
			t.Fatalf("tail[%d]=%q want %q (order broken across rotation)", i, e.Message, want)
		}
	}
}

func TestTailLinesBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.jsonl")
	// Write 5000 lines (~300 bytes each => ~1.5MB), well above a small scan cap.
	var sb strings.Builder
	const total = 5000
	for i := 0; i < total; i++ {
		sb.WriteString(`{"ts":"2025-01-01T00:00:00Z","level":"info","msg":"line-`)
		sb.WriteString(pad(i))
		sb.WriteString(`","fields":{"padding":"`)
		sb.WriteString(strings.Repeat("x", 200))
		sb.WriteString(`"}}`)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Ask for the last 5 lines with a scan cap far smaller than the file.
	got, err := tailLines(path, 5, 1<<14)
	if err != nil {
		t.Fatalf("tailLines: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(got))
	}
	for i, line := range got {
		want := "line-" + pad(total-5+i)
		if !strings.Contains(line, want) {
			t.Fatalf("got[%d] %q does not contain %q", i, line, want)
		}
	}
}

func pad(i int) string {
	s := []byte{'0', '0', '0', '0'}
	b := []byte{}
	if i == 0 {
		b = append(b, '0')
	} else {
		for i > 0 {
			b = append([]byte{byte('0' + i%10)}, b...)
			i /= 10
		}
	}
	copy(s[len(s)-len(b):], b)
	return string(s)
}

func TestConsoleStoreSizeRotation(t *testing.T) {
	dir := t.TempDir()
	s, err := NewConsoleStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	s.maxSize = 100 // force rotation quickly
	defer s.Close()

	for i := 0; i < 8; i++ {
		if err := s.Write(ConsoleEntry{Level: "info", Message: "msg", Time: 1750000000000}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	_ = s.Flush()

	tail, err := s.Tail(8)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(tail) != 8 {
		t.Fatalf("expected 8 entries across rotated files, got %d", len(tail))
	}
}
