package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestScaffoldNativeThreeLayerBuild verifies the task-card acceptance path:
// scaffold a native project → GOWORK=off CGO_ENABLED=0 go build succeeds →
// artifact directory contains the subprocess-mode executable. The three-layer
// scaffold produces a buildable project immediately after codegen.Generate;
// handlers.go stubs (sdk.ErrNotImplemented) compile fine. The dev build is a
// standalone executable (dev tree is zero-cgo; the release c-shared library
// is produced by the pluginhost release build via its staged cgo shim, not by
// a plain `go build -buildmode=c-shared` on the dev tree).
func TestScaffoldNativeThreeLayerBuild(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("native plugins not supported on this platform")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain not available")
	}

	dir := t.TempDir()
	if _, err := scaffoldApp(dir, "buildcheck", devAppDirName); err != nil {
		t.Fatalf("scaffoldApp: %v", err)
	}

	outDir := filepath.Join(dir, ".build")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(outDir, "app"+nativeExeSuffixForTest())
	cmd := exec.Command("go", "build", "-o", outPath, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build (subprocess) failed: %v\n%s", err, out)
	}

	// Artifact must be the standalone executable.
	if _, err := os.Stat(outPath); err != nil {
		t.Errorf("missing executable %s: %v", filepath.Base(outPath), err)
	}
}

func nativeExeSuffixForTest() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
