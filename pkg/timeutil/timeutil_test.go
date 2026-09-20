package timeutil

import (
	"testing"
	"time"
)

func TestToLocalISO(t *testing.T) {
	got := ToLocalISO("2026-08-12T00:00:00Z")
	want := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC).Local().Format(time.RFC3339)
	if got != want {
		t.Errorf("ToLocalISO = %q, want %q", got, want)
	}
	// Offset input preserved as local.
	if g := ToLocalISO("2026-08-12T08:00:00+08:00"); g == "" {
		t.Error("expected non-empty for offset input")
	}
}

func TestToLocalISOEdgeCases(t *testing.T) {
	if ToLocalISO("") != "" {
		t.Error("empty should return empty")
	}
	if ToLocalISO("garbage") != "garbage" {
		t.Error("unparseable should pass through")
	}
}

// TestToLocalISORoundTripNano exercises the production timestamp format used
// by Step/StepEvent/ChatMessage write points: UTC RFC3339Nano. The value must
// survive a write -> ToLocalISO -> parse round trip preserving the exact
// instant, so the display boundary never silently drops sub-second precision
// or leaks a raw UTC string to callers.
func TestToLocalISORoundTripNano(t *testing.T) {
	// Use a value with real fractional seconds so precision loss is detected.
	src := time.Now().UTC().Add(123*time.Millisecond + 456*time.Microsecond)
	written := src.Format(time.RFC3339Nano)

	out := ToLocalISO(written)
	if out == "" {
		t.Fatalf("ToLocalISO(%q) returned empty", written)
	}

	want, err := time.Parse(time.RFC3339Nano, written)
	if err != nil {
		t.Fatalf("setup parse failed: %v", err)
	}
	if out != want.Local().Format(time.RFC3339Nano) {
		t.Fatalf("ToLocalISO(%q) = %q, want local %q", written, out, want.Local().Format(time.RFC3339Nano))
	}

	parsed, err := time.Parse(time.RFC3339Nano, out)
	if err != nil {
		t.Fatalf("round-trip parse(%q) failed: %v", out, err)
	}
	if !parsed.Equal(src) {
		t.Fatalf("round-trip instant drift: written=%v parsed=%v (out=%q)", src, parsed, out)
	}

	// Whole-second RFC3339 input (no fraction) must also still convert, since
	// RFC3339Nano is a strict superset of RFC3339.
	plain := "2026-08-12T00:00:00Z"
	plainOut := ToLocalISO(plain)
	if plainOut == "" {
		t.Fatalf("ToLocalISO(%q) returned empty", plain)
	}
	if _, err := time.Parse(time.RFC3339Nano, plainOut); err != nil {
		t.Fatalf("round-trip parse(%q) failed: %v", plainOut, err)
	}
}

func TestParseFlexibleUTC(t *testing.T) {
	// Z suffix: parsed as UTC instant.
	got, err := ParseFlexible("2026-08-13T00:00:00Z")
	if err != nil {
		t.Fatalf("parse Z: %v", err)
	}
	want := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Z parse = %v, want %v", got, want)
	}
}

func TestParseFlexibleOffset(t *testing.T) {
	got, err := ParseFlexible("2026-08-13T08:00:00+08:00")
	if err != nil {
		t.Fatalf("parse offset: %v", err)
	}
	want := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("offset parse = %v, want %v (UTC)", got, want)
	}
}

func TestParseFlexibleNaiveLocal(t *testing.T) {
	// Naive timestamp (no offset) interpreted as local time.
	got, err := ParseFlexible("2026-08-13T08:00:00")
	if err != nil {
		t.Fatalf("parse naive: %v", err)
	}
	want := time.Date(2026, 8, 13, 8, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("naive parse = %v, want %v (local)", got, want)
	}
}

func TestParseFlexibleNaiveEqualsOffsetEquivalent(t *testing.T) {
	// A naive local time should equal the same wall-clock time expressed
	// with the local offset.
	naive, err := ParseFlexible("2026-08-13T08:00:00")
	if err != nil {
		t.Fatalf("naive: %v", err)
	}
	offsetStr := naive.Format(time.RFC3339)
	offset, err := ParseFlexible(offsetStr)
	if err != nil {
		t.Fatalf("offset: %v", err)
	}
	if !naive.Equal(offset) {
		t.Errorf("naive %v != offset %v", naive, offset)
	}
}
