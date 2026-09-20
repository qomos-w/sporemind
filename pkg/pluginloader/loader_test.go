package pluginloader

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/qomos-w/sporemind/pkg/testutil"
)

// buildSDKExamplePlugin compiles the SDK-based example plugin into a temp directory.
func buildSDKExamplePlugin(t *testing.T) string {
	t.Helper()

	pluginDir := absPluginDir(t)

	// Use a process-scoped temp directory rather than t.TempDir(): a Go
	// c-shared library stays mapped into the process until exit, so Windows
	// refuses to delete the .dll while the process is alive. t.TempDir()
	// cleanup would fail with "Access is denied".
	tmpDir, err := os.MkdirTemp("", "sporemind-plugin-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	outName := "plugin-hello" + PlatformSuffix()
	outPath := filepath.Join(tmpDir, outName)

	// The example's own go.mod replaces are relative to the main checkout
	// tree and do not resolve inside agent worktrees; stage the sources into
	// a temp module with absolute replaces (pkg/testutil), then build there.
	// c-shared requires cgo; force it on so the build does not silently fail
	// on environments where CGO_ENABLED defaults to 0.
	modDir := testutil.StageExampleModuleDir(t, pluginDir, tmpDir)
	testutil.GoRun(t, modDir, []string{"CGO_ENABLED=1"}, "build", "-buildmode=c-shared", "-o", outPath, ".")

	return outPath
}

// skipIfNoSharedLibSupport centralises the platform/checkout skip guards for
// tests that need to load a real c-shared library.
func skipIfNoSharedLibSupport(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("shared libraries not supported on this platform")
	}
	if _, err := os.Stat(filepath.Join(absPluginDir(t), "main.go")); err != nil {
		t.Skip("external SDK example is not checked out")
	}
}

func TestOpen_NonExistent(t *testing.T) {
	_, err := Open("/nonexistent/plugin.so")
	if err == nil {
		t.Fatal("expected error for missing library")
	}
}

// TestLibrary_SDKExampleManifest reads the plugin manifest via the exported
// PluginManifest symbol. It uses the process-wide shared library handle so
// that the Go c-shared runtime is only initialised once.
func TestLibrary_SDKExampleManifest(t *testing.T) {
	skipIfNoSharedLibSupport(t)

	lib, _ := sharedExampleLibrary(t)

	manifestJSON, err := lib.ReadString("PluginManifest")
	if err != nil {
		t.Fatalf("PluginManifest: %v", err)
	}
	if manifestJSON == "" {
		t.Fatal("manifest is empty")
	}

	id, _, version := func() (string, string, string) {
		var m struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(manifestJSON), &m); err != nil {
			t.Fatalf("unmarshal manifest: %v", err)
		}
		return m.ID, m.Name, m.Version
	}()
	if id != "com.example.hello" {
		t.Fatalf("id = %q, want com.example.hello", id)
	}
	if version != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0", version)
	}

	var manifest struct {
		Entrypoints []struct {
			Kind string `json:"kind"`
		} `json:"entrypoints"`
	}
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(manifest.Entrypoints) == 0 {
		t.Fatal("expected entrypoints in manifest")
	}
}
