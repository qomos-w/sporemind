// Package buildinfo provides build metadata: version, build type, flavor,
// commit, build time, and dirty flag. The canonical values are injected at
// link time via -ldflags -X, with runtime fallbacks for Version.
package buildinfo

import (
	"os"
	"strings"
)

// readVersion replicates the fallback logic from pkg/version/vars.go:
// SPOREMIND_VERSION env → "version" file at repo root → "dev".
func readVersion() string {
	if v := os.Getenv("SPOREMIND_VERSION"); v != "" {
		return strings.TrimSpace(v)
	}
	data, err := os.ReadFile("version")
	if err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data))
	}
	return "dev"
}