package logging

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qomos-w/gospore/gateway"
)

func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	entries := []gateway.LogEntry{
		{Timestamp: "2025-06-10T10:00:00.000000000Z", Level: "info", Message: "first"},
		{Timestamp: "2025-06-10T10:00:01.000000000Z", Level: "warn", Message: "second"},
		{Timestamp: "2025-06-10T10:00:02.000000000Z", Level: "error", Message: "third"},
	}
	for _, e := range entries {
		if err := s.Write(e); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	tail, err := s.Tail(2)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(tail) != 2 {
		t.Fatalf("expected 2 tail entries, got %d", len(tail))
	}
	if tail[0].Message != "second" || tail[1].Message != "third" {
		t.Fatalf("unexpected tail order/content: %+v", tail)
	}

	older, err := s.QueryBefore("2025-06-10T10:00:02.000000000Z", 10)
	if err != nil {
		t.Fatalf("query before: %v", err)
	}
	if len(older) != 2 {
		t.Fatalf("expected 2 older entries, got %d", len(older))
	}
	if older[0].Message != "first" || older[1].Message != "second" {
		t.Fatalf("unexpected older order/content: %+v", older)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestFileStoreReload(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	_ = s1.Write(gateway.LogEntry{Timestamp: "2025-06-10T10:00:00.000000000Z", Level: "info", Message: "persisted"})
	_ = s1.Close()

	s2, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s2.Close()

	tail, _ := s2.Tail(10)
	if len(tail) != 1 || tail[0].Message != "persisted" {
		t.Fatalf("expected persisted entry after reopen, got %+v", tail)
	}
}

func TestFileStoreCleanup(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	path := filepath.Join(dir, "backend", "2020-01-01.jsonl")
	if err := os.WriteFile(path, []byte(`{"ts":"2020-01-01T00:00:00Z","level":"info","msg":"old"}
`), 0644); err != nil {
		t.Fatalf("write old file: %v", err)
	}
	oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatalf("set old mtime: %v", err)
	}

	if err := s.Cleanup(0); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("old log file should have been removed")
	}
}
