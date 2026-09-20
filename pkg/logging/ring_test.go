package logging

import (
	"fmt"
	"testing"

	"github.com/qomos-w/gospore/gateway"
)

func TestRingQueryLevel(t *testing.T) {
	r := NewRing(100)
	r.Append(gateway.LogEntry{Level: "info", Message: "a"})
	r.Append(gateway.LogEntry{Level: "error", Message: "b"})
	r.Append(gateway.LogEntry{Level: "info", Message: "c"})

	got := r.QueryLogs(gateway.LogQuery{Level: "error"})
	if len(got) != 1 || got[0].Message != "b" {
		t.Fatalf("expected 1 error entry, got %v", got)
	}
}

func TestRingQueryCaller(t *testing.T) {
	r := NewRing(100)
	r.Append(gateway.LogEntry{Caller: "workspace/workspace.go:93", Message: "a"})
	r.Append(gateway.LogEntry{Caller: "agent/agent.go:226", Message: "b"})

	got := r.QueryLogs(gateway.LogQuery{Caller: "workspace"})
	if len(got) != 1 || got[0].Message != "a" {
		t.Fatalf("expected 1 workspace entry, got %v", got)
	}
}

func TestRingQueryLimit(t *testing.T) {
	r := NewRing(100)
	for i := 0; i < 10; i++ {
		r.Append(gateway.LogEntry{Level: "info", Message: "x"})
	}

	got := r.QueryLogs(gateway.LogQuery{Limit: 3})
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
}

func TestRingQueryDefaultLimit(t *testing.T) {
	r := NewRing(300)
	for i := 0; i < 250; i++ {
		r.Append(gateway.LogEntry{Level: "info", Message: "x"})
	}

	got := r.QueryLogs(gateway.LogQuery{})
	if len(got) != 200 {
		t.Fatalf("expected 200 entries (default), got %d", len(got))
	}
}

func TestRingEviction(t *testing.T) {
	r := NewRing(5)
	for i := 0; i < 10; i++ {
		r.Append(gateway.LogEntry{Level: "info", Message: "x"})
	}
	all := r.All()
	if len(all) != 5 {
		t.Fatalf("expected 5 entries after eviction, got %d", len(all))
	}
}

func TestRingTail(t *testing.T) {
	r := NewRing(100)
	for i := 0; i < 5; i++ {
		r.Append(gateway.LogEntry{Level: "info", Message: fmt.Sprintf("m%d", i), Timestamp: fmt.Sprintf("2026-07-11T00:00:0%dZ", i)})
	}

	got, err := r.Tail(3)
	if err != nil {
		t.Fatalf("Tail failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if got[2].Message != "m4" {
		t.Fatalf("expected last message m4, got %s", got[2].Message)
	}
}

func TestRingQueryBefore(t *testing.T) {
	r := NewRing(100)
	for i := 0; i < 5; i++ {
		r.Append(gateway.LogEntry{Level: "info", Message: fmt.Sprintf("m%d", i), Timestamp: fmt.Sprintf("2026-07-11T00:00:0%dZ", i)})
	}

	got, err := r.QueryBefore("2026-07-11T00:00:03Z", 10)
	if err != nil {
		t.Fatalf("QueryBefore failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries before ts 03, got %d", len(got))
	}
	if got[2].Message != "m2" {
		t.Fatalf("expected last message m2, got %s", got[2].Message)
	}
}
