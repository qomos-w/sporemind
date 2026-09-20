package voice

// This file consolidates package-level variable declarations for the voice
// package.

import "fmt"

// --- Audio format errors ---

// errUnsupportedFormat is returned when an incoming audio payload is not a
// supported format.
var errUnsupportedFormat = fmt.Errorf("voice: unsupported audio format")

// truncate caps an upstream response body before it is embedded in an error so
// a large error page never floods logs or callers.
func truncate(s string) string {
	const max = 512
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
