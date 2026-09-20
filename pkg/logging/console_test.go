package logging

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestConsoleStoreTailAndQueryBefore verifies the read side of the console
// store: Tail returns the newest entries and QueryBefore pages strictly older
// entries using an RFC3339Nano cursor (the NextBefore round-trip shape).
func TestConsoleStoreTailAndQueryBefore(t *testing.T) {
	dir := t.TempDir()
	s, err := NewConsoleStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC).UnixMilli()
	for i := 0; i < 5; i++ {
		entry := ConsoleEntry{Level: "info", Message: fmt.Sprintf("msg %d", i), Time: base + int64(i)*1000}
		if err := s.Write(entry); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	tail, err := s.Tail(3)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(tail) != 3 || tail[0].Message != "msg 2" || tail[2].Message != "msg 4" {
		t.Fatalf("unexpected tail: %+v", tail)
	}

	// QueryBefore with a cursor equal to msg 2's timestamp returns msg 0, msg 1.
	before := time.UnixMilli(base + 2*1000).UTC().Format(time.RFC3339Nano)
	older, err := s.QueryBefore(before, 10)
	if err != nil {
		t.Fatalf("query before: %v", err)
	}
	if len(older) != 2 || older[0].Message != "msg 0" || older[1].Message != "msg 1" {
		t.Fatalf("unexpected query-before: %+v", older)
	}

	// The limit caps the page (newest first among the older segment).
	limited, err := s.QueryBefore(before, 1)
	if err != nil {
		t.Fatalf("query before limited: %v", err)
	}
	if len(limited) != 1 || limited[0].Message != "msg 1" {
		t.Fatalf("unexpected limited query-before: %+v", limited)
	}

	// An invalid cursor is surfaced as an error, not silently ignored.
	if _, err := s.QueryBefore("not-a-time", 10); err == nil {
		t.Fatal("expected error for invalid before cursor")
	}
}

// TestConsoleStoreQueryBeforeNaiveCursor verifies that a Before cursor
// stripped of its zone suffix ("2006-01-02T15:04:05") is accepted and treated
// as UTC instead of failing the query.
func TestConsoleStoreQueryBeforeNaiveCursor(t *testing.T) {
	dir := t.TempDir()
	s, err := NewConsoleStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 8, 27, 11, 8, 50, 0, time.UTC).UnixMilli()
	for i := 0; i < 4; i++ {
		entry := ConsoleEntry{Level: "info", Message: fmt.Sprintf("msg %d", i), Time: base + int64(i)*1000}
		if err := s.Write(entry); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// Entries live at 11:08:50/51/52/53 UTC; a naive cursor at 11:08:52
	// must return the two strictly older entries.
	older, err := s.QueryBefore("2026-08-27T11:08:52", 10)
	if err != nil {
		t.Fatalf("query before naive cursor: %v", err)
	}
	if len(older) != 2 || older[0].Message != "msg 0" || older[1].Message != "msg 1" {
		t.Fatalf("unexpected query-before: %+v", older)
	}

	// Fractional seconds without a zone are accepted too.
	if _, err := s.QueryBefore("2026-08-27T11:08:52.5", 10); err != nil {
		t.Fatalf("query before fractional naive cursor: %v", err)
	}
}

// TestConsoleStoreQueryBeforeDeepCursor makes sure a page boundary deep inside
// a busy file cannot hide older entries in the same file: the tail window is a
// dense run of newer entries, so the backward scan must widen until it finds
// the older segment.
func TestConsoleStoreQueryBeforeDeepCursor(t *testing.T) {
	dir := t.TempDir()
	s, err := NewConsoleStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC).UnixMilli()
	for i := 0; i < 50; i++ {
		entry := ConsoleEntry{Level: "info", Message: fmt.Sprintf("msg-%02d-%s", i, strings.Repeat("x", 260)), Time: base + int64(i)*1000}
		if err := s.Write(entry); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// Cursor at entry 40. The default window (limit*2048 bytes) only covers the
	// newest ~7 entries, all newer than the cursor — the scan must widen to
	// surface entry 39.
	cursor := time.UnixMilli(base + 40*1000).UTC().Format(time.RFC3339Nano)
	older, err := s.QueryBefore(cursor, 1)
	if err != nil {
		t.Fatalf("query before: %v", err)
	}
	if len(older) != 1 || !strings.HasPrefix(older[0].Message, "msg-39-") {
		t.Fatalf("expected newest entry older than cursor (msg-39), got %+v", older)
	}

	// A cursor older than every entry yields an empty page, not an error.
	tooOld := time.UnixMilli(base).UTC().Format(time.RFC3339Nano)
	none, err := s.QueryBefore(tooOld, 10)
	if err != nil {
		t.Fatalf("query before too-old: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no entries older than the first, got %+v", none)
	}
}
