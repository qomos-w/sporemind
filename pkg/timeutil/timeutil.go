package timeutil

import (
	"strings"
	"time"
)

// ToLocalISO converts a UTC RFC3339/RFC3339Nano timestamp string to the
// machine's local timezone with offset, preserving nanosecond precision when
// present. Empty or unparseable strings pass through unchanged. This is the
// single output-boundary converter for timestamps sent to callers.
func ToLocalISO(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return iso
	}
	return t.Local().Format(time.RFC3339Nano)
}

// ToLocalNano is ToLocalISO for RFC3339Nano precision (used by logs and
// changeset timestamps).
func ToLocalNano(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return iso
	}
	return t.Local().Format(time.RFC3339Nano)
}

// ParseFlexible parses a timestamp that may carry a timezone offset (RFC3339)
// or may omit it (naive local time). A naive timestamp like "2026-08-13T00:00:00"
// is interpreted as local machine time, not UTC.
func ParseFlexible(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	// RFC3339 requires either a Z suffix or a +hh:mm / -hh:mm offset.
	// If the string ends with 'Z' or contains a '+'/'-' offset after the
	// time portion, time.Parse handles it directly.
	if strings.HasSuffix(s, "Z") || hasOffset(s) {
		return time.Parse(time.RFC3339, s)
	}
	// Naive timestamp: try RFC3339, then fall back to interpreting as local.
	if t, err := time.ParseInLocation(time.RFC3339, s, time.Local); err == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02T15:04:05", s, time.Local)
}

// hasOffset reports whether s contains a timezone offset (±HH:MM) after the
// time portion (position 19+ in a canonical RFC3339 string).
func hasOffset(s string) bool {
	if len(s) < 20 {
		return false
	}
	tail := s[19:]
	return strings.HasPrefix(tail, "+") || strings.HasPrefix(tail, "-")
}
