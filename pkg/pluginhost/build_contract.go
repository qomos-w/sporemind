package pluginhost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ValidateNativeBuildContract checks an already-produced native artifact and
// its manifest before registration. Compilation remains caller-owned.
func ValidateNativeBuildContract(contract gen.NativeBuildContract) (gen.NativeBuildResult, error) {
	result := gen.NativeBuildResult{ArtifactPath: contract.ArtifactPath}
	fail := func(err error) (gen.NativeBuildResult, error) {
		result.Diagnostic = err.Error()
		return result, err
	}
	if contract.Runtime != NativeRuntime {
		return fail(fmt.Errorf("native build runtime must be %q", NativeRuntime))
	}
	if strings.TrimSpace(contract.TargetOS) == "" || strings.TrimSpace(contract.TargetArch) == "" {
		return fail(fmt.Errorf("native build target OS and arch are required"))
	}
	if contract.TargetOS != runtime.GOOS || contract.TargetArch != runtime.GOARCH {
		return fail(fmt.Errorf("native artifact target %s/%s cannot load on host %s/%s", contract.TargetOS, contract.TargetArch, runtime.GOOS, runtime.GOARCH))
	}
	if !filepath.IsAbs(contract.ArtifactPath) || !filepath.IsAbs(contract.ManifestPath) {
		return fail(fmt.Errorf("artifact and manifest paths must be absolute"))
	}
	artifact, err := os.Stat(contract.ArtifactPath)
	if err != nil {
		return fail(fmt.Errorf("native artifact: %w", err))
	}
	if !artifact.Mode().IsRegular() || artifact.Size() == 0 {
		return fail(fmt.Errorf("native artifact is empty or not a regular file"))
	}
	data, err := os.ReadFile(contract.ManifestPath)
	if err != nil {
		return fail(fmt.Errorf("native manifest: %w", err))
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fail(fmt.Errorf("native manifest decode: %w", err))
	}
	if manifest.Runtime != NativeRuntime || manifest.ID == "" || manifest.Name == "" || manifest.Version == "" || manifest.Namespace == "" {
		return fail(fmt.Errorf("native manifest metadata/runtime is invalid"))
	}
	artifactData, err := os.ReadFile(contract.ArtifactPath)
	if err != nil {
		return fail(fmt.Errorf("native artifact read: %w", err))
	}
	hash := sha256.Sum256(artifactData)
	result.ArtifactHash = hex.EncodeToString(hash[:])
	if contract.ArtifactHash != "" && !strings.EqualFold(contract.ArtifactHash, result.ArtifactHash) {
		return fail(fmt.Errorf("native artifact hash mismatch"))
	}
	result.Success = true
	return result, nil
}
