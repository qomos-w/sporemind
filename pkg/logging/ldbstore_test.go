package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/gateway"
)

func TestLevelDBLogStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLevelDBLogStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

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
}

func TestLevelDBLogStoreReload(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewLevelDBLogStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	_ = s1.Write(gateway.LogEntry{Timestamp: "2025-06-10T10:00:00.000000000Z", Level: "info", Message: "persisted"})
	_ = s1.Close()

	s2, err := NewLevelDBLogStore(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s2.Close()

	tail, _ := s2.Tail(10)
	if len(tail) != 1 || tail[0].Message != "persisted" {
		t.Fatalf("expected persisted entry after reopen, got %+v", tail)
	}
}

func TestLevelDBLogStoreConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLevelDBLogStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	const goroutines = 8
	const perG = 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				_ = s.Write(gateway.LogEntry{
					Timestamp: fmt.Sprintf("2025-06-10T10:00:%02d.%09dZ", i%60, gid*1000+i),
					Level:     "info",
					Message:   fmt.Sprintf("g%d-%d", gid, i),
				})
			}
		}(g)
	}
	wg.Wait()

	tail, err := s.Tail(goroutines * perG)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(tail) != goroutines*perG {
		t.Fatalf("expected %d entries, got %d", goroutines*perG, len(tail))
	}

	// Verify all messages are unique (no lost or duplicated writes).
	seen := make(map[string]bool, len(tail))
	for _, e := range tail {
		if seen[e.Message] {
			t.Fatalf("duplicate entry: %s", e.Message)
		}
		seen[e.Message] = true
	}
}

func TestLevelDBLogStoreConcurrentReadWrite(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLevelDBLogStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	// Seed with some initial entries.
	for i := 0; i < 20; i++ {
		_ = s.Write(gateway.LogEntry{
			Timestamp: fmt.Sprintf("2025-06-10T10:00:%02d.000000000Z", i),
			Level:     "info",
			Message:   fmt.Sprintf("seed-%d", i),
		})
	}

	const goroutines = 4
	const perG = 30
	var wg sync.WaitGroup

	// Concurrent writers.
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				_ = s.Write(gateway.LogEntry{
					Timestamp: fmt.Sprintf("2025-06-10T10:01:%02d.%09dZ", i%60, gid*1000+i),
					Level:     "info",
					Message:   fmt.Sprintf("g%d-%d", gid, i),
				})
			}
		}(g)
	}

	// Concurrent readers.
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_, _ = s.Tail(50)
				_, _ = s.QueryBefore("2025-06-10T10:01:00.000000000Z", 20)
			}
		}()
	}

	wg.Wait()

	// Final consistency check.
	tail, err := s.Tail(10000)
	if err != nil {
		t.Fatalf("final tail: %v", err)
	}
	expected := 20 + goroutines*perG
	if len(tail) != expected {
		t.Fatalf("expected %d total entries, got %d", expected, len(tail))
	}
}

func TestLevelDBLogStoreCleanup(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLevelDBLogStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	// Write entries with old and current dates.
	_ = s.Write(gateway.LogEntry{
		Timestamp: "2020-01-01T00:00:00.000000000Z",
		Level:     "info",
		Message:   "old",
	})
	_ = s.Write(gateway.LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     "info",
		Message:   "current",
	})

	// Cleanup entries older than 1 day.
	if err := s.Cleanup(24 * time.Hour); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	tail, _ := s.Tail(10)
	for _, e := range tail {
		if e.Message == "old" {
			t.Fatal("old entry should have been removed by cleanup")
		}
	}
	if len(tail) == 0 {
		t.Fatal("current entry should still exist after cleanup")
	}
}

func TestLevelDBLogStoreMultiDay(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLevelDBLogStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	// Write entries across 3 days.
	for _, ts := range []string{
		"2025-06-09T10:00:00.000000000Z",
		"2025-06-09T10:00:01.000000000Z",
		"2025-06-10T10:00:00.000000000Z",
		"2025-06-10T10:00:01.000000000Z",
		"2025-06-11T10:00:00.000000000Z",
	} {
		_ = s.Write(gateway.LogEntry{Timestamp: ts, Level: "info", Message: ts})
	}

	tail, err := s.Tail(3)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(tail) != 3 {
		t.Fatalf("expected 3 tail entries, got %d", len(tail))
	}
	// Should be the 2 from June 10 (latest 2 before June 11) + 1 from June 11.
	if tail[0].Message != "2025-06-10T10:00:00.000000000Z" {
		t.Fatalf("expected first tail entry from 06-10, got %s", tail[0].Message)
	}
	if tail[2].Message != "2025-06-11T10:00:00.000000000Z" {
		t.Fatalf("expected last tail entry from 06-11, got %s", tail[2].Message)
	}

	// QueryBefore should span multiple days.
	older, err := s.QueryBefore("2025-06-10T10:00:01.000000000Z", 10)
	if err != nil {
		t.Fatalf("query before: %v", err)
	}
	if len(older) != 3 {
		t.Fatalf("expected 3 entries before 06-10T10:00:01, got %d", len(older))
	}
	// Should include 2 from June 9 + 1 from June 10 (10:00:00).
	if older[0].Message != "2025-06-09T10:00:00.000000000Z" {
		t.Fatalf("expected first older entry from 06-09, got %s", older[0].Message)
	}
	if older[2].Message != "2025-06-10T10:00:00.000000000Z" {
		t.Fatalf("expected last older entry from 06-10T10:00:00, got %s", older[2].Message)
	}
}

func TestLevelDBLogStoreMigration(t *testing.T) {
	dir := t.TempDir()

	// Create JSONL files as if FileStore had written them.
	backendDir := filepath.Join(dir, "logs", "backend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	entries := []gateway.LogEntry{
		{Timestamp: "2025-06-10T10:00:00.000000000Z", Level: "info", Message: "first"},
		{Timestamp: "2025-06-10T10:00:01.000000000Z", Level: "warn", Message: "second"},
	}
	var buf bytes.Buffer
	for _, e := range entries {
		b, _ := json.Marshal(e)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(backendDir, "2025-06-10.jsonl"), buf.Bytes(), 0644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	// Open LevelDBLogStore — should migrate.
	s, err := NewLevelDBLogStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	tail, err := s.Tail(10)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(tail) != 2 {
		t.Fatalf("expected 2 migrated entries, got %d", len(tail))
	}
	if tail[0].Message != "first" || tail[1].Message != "second" {
		t.Fatalf("unexpected order: %+v", tail)
	}

	// Source files should still exist (not deleted for rollback safety).
	if _, err := os.Stat(filepath.Join(backendDir, "2025-06-10.jsonl")); err != nil {
		t.Fatal("source JSONL file should not be deleted after migration")
	}

	// Reopen — should not re-migrate (migrated flag set).
	s2, err := NewLevelDBLogStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	tail2, _ := s2.Tail(10)
	if len(tail2) != 2 {
		t.Fatalf("expected 2 entries after reopen (no re-migrate), got %d", len(tail2))
	}
}

func TestLevelDBLogStoreMigrationMultiFile(t *testing.T) {
	dir := t.TempDir()
	backendDir := filepath.Join(dir, "logs", "backend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create multiple JSONL files across different days.
	day1 := []gateway.LogEntry{
		{Timestamp: "2025-06-09T10:00:00.000000000Z", Level: "info", Message: "d1-1"},
		{Timestamp: "2025-06-09T10:00:01.000000000Z", Level: "info", Message: "d1-2"},
	}
	day2 := []gateway.LogEntry{
		{Timestamp: "2025-06-10T10:00:00.000000000Z", Level: "info", Message: "d2-1"},
		{Timestamp: "2025-06-10T10:00:01.000000000Z", Level: "info", Message: "d2-2"},
	}
	for name, entries := range map[string][]gateway.LogEntry{
		"2025-06-09.jsonl": day1,
		"2025-06-10.jsonl": day2,
	} {
		var buf bytes.Buffer
		for _, e := range entries {
			b, _ := json.Marshal(e)
			buf.Write(b)
			buf.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(backendDir, name), buf.Bytes(), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	s, err := NewLevelDBLogStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	tail, err := s.Tail(10)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(tail) != 4 {
		t.Fatalf("expected 4 migrated entries, got %d", len(tail))
	}
	// Verify chronological order.
	expected := []string{"d1-1", "d1-2", "d2-1", "d2-2"}
	for i, msg := range expected {
		if tail[i].Message != msg {
			t.Fatalf("tail[%d] = %s, want %s", i, tail[i].Message, msg)
		}
	}
}

func TestLevelDBLogStoreImplementsLogStore(t *testing.T) {
	var _ LogStore = (*LevelDBLogStore)(nil)
	var _ BackendLogSource = (*LevelDBLogStore)(nil)
}

func TestLevelDBLogStoreWriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLevelDBLogStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	_ = s.Write(gateway.LogEntry{Timestamp: "2025-06-10T10:00:00Z", Level: "info", Message: "before close"})
	_ = s.Close()

	err = s.Write(gateway.LogEntry{Timestamp: "2025-06-10T10:00:01Z", Level: "info", Message: "after close"})
	if err == nil {
		t.Fatal("expected error writing to closed store")
	}
}
