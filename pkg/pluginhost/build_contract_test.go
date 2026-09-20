package pluginhost

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestValidateNativeBuildContract(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "plugin.so")
	manifest := filepath.Join(dir, "app.manifest.json")
	if err := os.WriteFile(artifact, []byte("native artifact"), 0o600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(manifest, []byte(`{"Id":"test.native","Name":"Test","Version":"1.0.0","Runtime":"native","ProtocolVersion":1,"Namespace":"native.test","Permissions":[],"Schemas":[],"Callables":[],"Events":[],"Projections":[],"Entrypoints":[],"Dependencies":[]}`), 0o600); err != nil { t.Fatal(err) }
	result, err := ValidateNativeBuildContract(gen.NativeBuildContract{Runtime: NativeRuntime, TargetOS: runtime.GOOS, TargetArch: runtime.GOARCH, ArtifactPath: artifact, ManifestPath: manifest})
	if err != nil { t.Fatal(err) }
	if !result.Success || result.ArtifactHash == "" { t.Fatalf("unexpected result: %+v", result) }
}

func TestValidateNativeBuildContractRejectsMismatch(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "plugin.so")
	manifest := filepath.Join(dir, "app.manifest.json")
	_ = os.WriteFile(artifact, []byte("artifact"), 0o600)
	_ = os.WriteFile(manifest, []byte(`{"Id":"test.native","Name":"Test","Version":"1.0.0","Runtime":"native","ProtocolVersion":1,"Namespace":"native.test","Permissions":[],"Schemas":[],"Callables":[],"Events":[],"Projections":[],"Entrypoints":[],"Dependencies":[]}`), 0o600)
	if _, err := ValidateNativeBuildContract(gen.NativeBuildContract{Runtime: NativeRuntime, TargetOS: runtime.GOOS, TargetArch: runtime.GOARCH, ArtifactPath: artifact, ManifestPath: manifest, ArtifactHash: "bad-hash"}); err == nil { t.Fatal("expected hash mismatch") }
}
