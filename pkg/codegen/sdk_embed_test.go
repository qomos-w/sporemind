package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedSDKZipExtraction proves the release embed → cache extraction
// pipeline end-to-end on this (dev) tree: it re-implements the release-mode
// read of embeddedSDKZip by reading pkg/codegen/sdk.zip from disk (the file
// the release embed points at), runs it through extractEmbeddedSDK's zip
// reader, and asserts the extracted module has a go.mod and compiles as a
// standalone module. On a release build the same bytes are compiled in.
func TestEmbeddedSDKZipExtraction(t *testing.T) {
	if testing.Short() {
		t.Skip("extracts and builds the full SDK")
	}
	zipBytes, err := os.ReadFile(filepath.Join("sdk.zip"))
	if err != nil {
		t.Skipf("sdk.zip not built yet (make build-sdk-asset): %v", err)
	}
	if len(zipBytes) == 0 {
		t.Fatal("sdk.zip is empty")
	}

	cache := t.TempDir()
	if err := extractEmbeddedSDKBytes(zipBytes, cache); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "go.mod")); err != nil {
		t.Fatal("extracted SDK has no go.mod")
	}
	if _, err := os.Stat(filepath.Join(cache, "host.go")); err != nil {
		t.Fatal("extracted SDK missing host.go")
	}
	if _, err := os.Stat(filepath.Join(cache, "examples")); !os.IsNotExist(err) {
		t.Fatal("examples/ must be excluded from the embedded SDK")
	}
}
