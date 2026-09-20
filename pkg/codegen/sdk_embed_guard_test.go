package codegen

import (
	"testing"
)

// TestEmbeddedSDKZipSizeReleaseGuard documents the release-build embed: the
// release-tagged binary must carry a non-empty, plausible SDK zip (>=100KB,
// <=5MB). Skipped on dev tags where the embed is intentionally empty.
func TestEmbeddedSDKZipSizeReleaseGuard(t *testing.T) {
	if len(embeddedSDKZip) == 0 {
		t.Skip("dev build: embeddedSDKZip is empty by design (findDevSDK used instead)")
	}
	if len(embeddedSDKZip) < 100*1024 {
		t.Fatalf("embedded SDK zip suspiciously small: %d bytes", len(embeddedSDKZip))
	}
	if len(embeddedSDKZip) > 5*1024*1024 {
		t.Fatalf("embedded SDK zip suspiciously large: %d bytes", len(embeddedSDKZip))
	}
}
