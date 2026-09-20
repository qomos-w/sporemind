package pluginhost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// TestCaptchaProbeLoadExistingArtifact loads the already-built captcha DLL via
// the production ArtifactLoader path to surface the error dev_gate swallows.
func TestCaptchaProbeLoadExistingArtifact(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("shared libraries not supported on this platform")
	}
	sourceDir, err := filepath.Abs("../../../plugin-dev-example")
	if err != nil {
		t.Fatal(err)
	}
	buildDir := filepath.Join(sourceDir, ".sporecode", "build")
	entries, _ := os.ReadDir(buildDir)
	var artifactPath string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".dll" || filepath.Ext(e.Name()) == ".so" {
			artifactPath = filepath.Join(buildDir, e.Name())
		}
	}
	if artifactPath == "" {
		t.Skipf("no built artifact in %s", buildDir)
	}
	manifestData, err := os.ReadFile(filepath.Join(sourceDir, "app.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	host := &captureHost{}
	loader := pluginhost.NewArtifactLoader(host)
	loader.SetOpener(loaderOpener{})
	abi := gen.PluginAbi{
		Name:            "spore-plugin",
		Version:         1,
		Encoding:        pluginhost.BinaryCodecV1,
		InvokeSymbol:    "PluginInvoke",
		ContractVersion: "1",
		Isolation:       pluginhost.IsolationInProcess,
		TrustClass:      pluginhost.TrustFirstParty,
		Signer:          "sporemind.first-party",
	}
	resp, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
		Manifest:     manifest,
		Abi:          abi,
		ArtifactPath: artifactPath,
	})
	if err != nil {
		t.Fatalf("LOAD FAILED: %v", err)
	}
	t.Logf("loaded plugin %q status=%+v", resp.PluginID, resp.Status)
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: resp.PluginID}); err != nil {
		t.Logf("unload: %v", err)
	}
}
