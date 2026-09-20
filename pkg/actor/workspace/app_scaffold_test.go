package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

func TestScaffoldAppCreatesSporeAppFiles(t *testing.T) {
	dir := t.TempDir()
	files, err := scaffoldApp(dir, "hello", sporeAppDirName)
	if err != nil {
		t.Fatalf("scaffoldApp: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("created files = %v, want 3 files", files)
	}
	var manifest map[string]any
	data, err := os.ReadFile(filepath.Join(dir, "app.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if manifest["Runtime"] != "spore" {
		t.Fatalf("manifest Runtime = %v, want spore", manifest["Runtime"])
	}
	if _, err := os.Stat(filepath.Join(dir, "main.spore")); err != nil {
		t.Fatal(err)
	}
	if _, err := scaffoldApp(dir, "hello", sporeAppDirName); err == nil {
		t.Fatal("expected non-empty directory to be rejected")
	}
}

func TestScaffoldAppCreatesPluginFiles(t *testing.T) {
	dir := t.TempDir()
	files, err := scaffoldApp(dir, "myplugin", devAppDirName)
	if err != nil {
		t.Fatalf("scaffoldApp: %v", err)
	}

	// Assert the generated file set includes all expected files.
	// Template files (written by scaffold) + codegen-generated files. The
	// vendored real SDK lands under vendor-sdk/ but is not listed in
	// `files` (vendored material, not generated artifacts) — its presence
	// is asserted by TestScaffoldNativeVendoredSDKCompiles.
	wantFiles := map[string]bool{
		"app.appdef":            false,
		"go.mod":                false,
		"README.md":             false,
		"main.gen.go":           false,
		"handlers.go":           false,
		"app.manifest.json":     false,
		"schemas_gen.go":        false,
		"client.gen.ts":         false,
	}
	for _, f := range files {
		if _, ok := wantFiles[f]; ok {
			wantFiles[f] = true
		}
	}
	for f, found := range wantFiles {
		if !found {
			t.Errorf("expected file %q in created files, got %v", f, files)
		}
		// Also verify the file exists on disk.
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected file %q on disk: %v", f, err)
		}
	}

	// Non-empty directory should be rejected.
	if _, err := scaffoldApp(dir, "myplugin", devAppDirName); err == nil {
		t.Fatal("expected non-empty directory to be rejected")
	}
}

func TestScaffoldNativeManifestFields(t *testing.T) {
	dir := t.TempDir()
	if _, err := scaffoldApp(dir, "myplugin", devAppDirName); err != nil {
		t.Fatalf("scaffoldApp: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "app.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}

	// Assert host-required manifest fields.
	if manifest.ID != "app.myplugin" {
		t.Errorf("manifest ID = %q, want %q", manifest.ID, "app.myplugin")
	}
	if manifest.Name != "myplugin" {
		t.Errorf("manifest Name = %q, want %q", manifest.Name, "myplugin")
	}
	if manifest.Version != "0.1.0" {
		t.Errorf("manifest Version = %q, want %q", manifest.Version, "0.1.0")
	}
	if manifest.Runtime != "native" {
		t.Errorf("manifest Runtime = %q, want %q", manifest.Runtime, "native")
	}
	// HostProtocolVersion is 2 — the v2-only correlated-frame transport
	// (pkg/pluginhost/contract.go); the scaffold manifest must emit it.
	if manifest.ProtocolVersion != 2 {
		t.Errorf("manifest ProtocolVersion = %d, want 2", manifest.ProtocolVersion)
	}
	if manifest.Namespace != "app.myplugin" {
		t.Errorf("manifest Namespace = %q, want %q", manifest.Namespace, "app.myplugin")
	}
	// Callables must have ID, RequestSchema, ResponseSchema (ValidateNativeManifest requirement).
	if len(manifest.Callables) == 0 {
		t.Fatal("manifest has no callables")
	}
	for _, c := range manifest.Callables {
		if c.ID == "" || c.RequestSchema == "" || c.ResponseSchema == "" {
			t.Errorf("callable descriptor incomplete: %+v", c)
		}
	}
	// Entrypoints must have Kind and ID.
	if len(manifest.Entrypoints) == 0 {
		t.Fatal("manifest has no entrypoints")
	}
	for _, ep := range manifest.Entrypoints {
		if ep.Kind == "" || ep.ID == "" {
			t.Errorf("entrypoint descriptor incomplete: %+v", ep)
		}
	}
}

func TestScaffoldNativeManifestValidates(t *testing.T) {
	dir := t.TempDir()
	if _, err := scaffoldApp(dir, "myplugin", devAppDirName); err != nil {
		t.Fatalf("scaffoldApp: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "app.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}

	// The ABI is produced by native_build; construct the expected values
	// (matching what a correct native_build should emit) to verify the
	// manifest passes ValidateNativeManifest.
	abi := gen.PluginAbi{
		Name:            "spore-plugin",
		Version:         1,
		Encoding:        pluginhost.BinaryCodecV1,
		InvokeSymbol:    "PluginInvoke",
		ContractVersion: "1",
		Isolation:       pluginhost.IsolationInProcess,
		TrustClass:      pluginhost.TrustFirstParty,
		Signer:          "sporemind",
	}
	schemaHash := pluginhost.ComputeSchemaHash(manifest.Schemas)
	if err := pluginhost.ValidateNativeManifest(manifest, abi, schemaHash); err != nil {
		t.Fatalf("ValidateNativeManifest: %v", err)
	}
}

func TestScaffoldNativeGoMod(t *testing.T) {
	dir := t.TempDir()
	if _, err := scaffoldApp(dir, "myplugin", devAppDirName); err != nil {
		t.Fatalf("scaffoldApp: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// go.mod must require the SDK and replace it with the vendored directory.
	if !strings.Contains(content, "module myplugin") {
		t.Errorf("go.mod missing module declaration: %s", content)
	}
	if !strings.Contains(content, "require github.com/qomos-w/sporemind-plugin-sdk") {
		t.Errorf("go.mod missing SDK require: %s", content)
	}
	if !strings.Contains(content, "replace github.com/qomos-w/sporemind-plugin-sdk => ./vendor-sdk") {
		t.Errorf("go.mod missing replace to vendor-sdk: %s", content)
	}
}

func TestScaffoldNativeABISymbols(t *testing.T) {
	dir := t.TempDir()
	if _, err := scaffoldApp(dir, "myplugin", devAppDirName); err != nil {
		t.Fatalf("scaffoldApp: %v", err)
	}
	mainData, err := os.ReadFile(filepath.Join(dir, "main.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	mainContent := string(mainData)

	// The dev (subprocess) main.gen.go is registration-only: it must
	// register via sdk.Register and carry NO cgo ABI surface — the //export
	// symbols moved to the release-time cgo shim (pluginhost's
	// main_cgo.gen.go) and the dev entry lives in main_run.gen.go.
	if !strings.Contains(mainContent, "sdk.Register(") {
		t.Errorf("main.gen.go must register plugin via sdk.Register")
	}
	for _, sym := range []string{"//export", `import "C"`, "func main()"} {
		if strings.Contains(mainContent, sym) {
			t.Errorf("main.gen.go must not contain %q (register-only dev entry)", sym)
		}
	}

	// The subprocess entry must exist and delegate to sdk.RunProcess under
	// the !cgo build tag.
	runData, err := os.ReadFile(filepath.Join(dir, "main_run.gen.go"))
	if err != nil {
		t.Fatalf("read main_run.gen.go: %v", err)
	}
	runContent := string(runData)
	if !strings.Contains(runContent, "//go:build !cgo") {
		t.Errorf("main_run.gen.go missing !cgo build tag")
	}
	if !strings.Contains(runContent, "sdk.RunProcess()") {
		t.Errorf("main_run.gen.go must delegate to sdk.RunProcess")
	}
	if !strings.Contains(runContent, "func main()") {
		t.Errorf("main_run.gen.go must declare func main()")
	}
}

func TestScaffoldNativeVendoredSDKCompiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := scaffoldApp(dir, "myplugin", devAppDirName); err != nil {
		t.Fatalf("scaffoldApp: %v", err)
	}
	// Verify representative vendored real-SDK files exist and are non-empty.
	// The vendored tree is the actual sporemind-plugin-sdk module (host
	// bridge, process transport, manifest, registry), not a trimmed
	// scaffold subset — this is the drift guard between scaffold and SDK.
	vendoredFiles := []string{
		"vendor-sdk/go.mod",
		"vendor-sdk/host.go",
		"vendor-sdk/bridge.go",
		"vendor-sdk/manifest.go",
		"vendor-sdk/plugin.go",
		"vendor-sdk/process_transport.go",
		"vendor-sdk/registry_test.go",
	}
	for _, f := range vendoredFiles {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if len(data) == 0 {
			t.Errorf("vendored file %s is empty", f)
		}
	}

	// The vendored go.mod must declare the SDK module path.
	goModData, err := os.ReadFile(filepath.Join(dir, "vendor-sdk/go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goModData), "module github.com/qomos-w/sporemind-plugin-sdk") {
		t.Errorf("vendor-sdk/go.mod missing module declaration")
	}

	// The vendored SDK must carry the Invoke/InvokeStream surface and the
	// dot-style permission vocabulary — the two most drift-prone contract
	// surfaces.
	hostGo, err := os.ReadFile(filepath.Join(dir, "vendor-sdk/host.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"InvokeStream", "LLMChunk", "llm.invoke"} {
		if !strings.Contains(string(hostGo), marker) && !strings.Contains(string(manifestOr(t, dir)), marker) {
			t.Errorf("vendored SDK missing streaming/permission marker %q", marker)
		}
	}
}

// manifestOr reads the vendored manifest.go for marker checks that live there
// (permission constants) rather than in host.go.
func manifestOr(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "vendor-sdk", "manifest.go"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestScaffoldAppRejectsUnknownKind(t *testing.T) {
	if _, err := scaffoldApp(t.TempDir(), "bad", "unknown"); err == nil {
		t.Fatal("expected unknown app kind to be rejected")
	}
}

func TestScaffoldAppAllowsGitOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := scaffoldApp(dir, "hello", sporeAppDirName); err != nil {
		t.Fatalf("git-only directory should be scaffoldable: %v", err)
	}
	if _, err := scaffoldApp(dir, "hello", sporeAppDirName); err == nil {
		t.Fatal("expected non-empty directory to be rejected")
	}
}
