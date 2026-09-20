package codegen

import (
	"os"
	"os/exec"
	"testing"
)

// TestVendoredTemplateBuildsStandalone is the round-7 end-to-end proof: a
// template scaffold whose only SDK is the vendored copy compiles with the
// host SDK path deliberately unavailable (no replace pointing outside).
func TestVendoredTemplateBuildsStandalone(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the full SDK")
	}
	dir := t.TempDir()
	if _, err := Generate(dir, Options{SDKPath: testSDKPath(t), Template: true}); err != nil {
		t.Fatalf("Generate template: %v", err)
	}
	if !GoModUsesVendoredSDK(dir) {
		t.Fatal("go.mod must use the vendored replace")
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("vendored build failed: %v\n%s", err, out)
	}
}
