package pluginhost

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"

	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// scaffoldTemplateVars matches the {{.Name}} / {{.AppID}} substitutions used
// by scaffoldPlugin in the workspace package.
type scaffoldTemplateVars struct {
	Name  string
	AppID string
}

// generateScaffoldFromTemplates reads the A1 scaffold templates from the
// workspace package, renders {{.Name}}/{{.AppID}}, and writes the result to
// dir. Then runs codegen.Generate to produce the five generated artifacts.
// This mirrors scaffoldPlugin without importing the workspace package
// (which would exceed the test scope). If the templates are not found, the
// test is skipped.
func generateScaffoldFromTemplates(t *testing.T, dir, name string) {
	t.Helper()
	templateDir, err := filepath.Abs("../workspace/scaffoldtemplates/native")
	if err != nil {
		t.Fatalf("resolve template dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(templateDir, "app.appdef.tmpl")); err != nil {
		t.Skipf("scaffold templates not found at %s", templateDir)
	}
	vars := scaffoldTemplateVars{Name: name, AppID: "app." + name}
	err = filepath.Walk(templateDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, err := filepath.Rel(templateDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		outRel := relPath
		var content []byte
		if strings.HasSuffix(relPath, ".tmpl") {
			tmpl, err := template.New(relPath).Parse(string(data))
			if err != nil {
				return err
			}
			var buf strings.Builder
			if err := tmpl.Execute(&buf, vars); err != nil {
				return err
			}
			content = []byte(buf.String())
			outRel = strings.TrimSuffix(relPath, ".tmpl")
		} else {
			content = data
		}
		outPath := filepath.Join(dir, outRel)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(outPath, content, 0o644)
	})
	if err != nil {
		t.Fatalf("generate scaffold: %v", err)
	}

	// Run codegen.Generate to produce main.gen.go, handlers.go,
	// schemas_gen.go, app.manifest.json, client.gen.ts from app.appdef.
	// SDK resolution is the standard one (dev checkout via the cwd anchor
	// under go test): Generate vendors the real sporemind-plugin-sdk into
	// vendor-sdk/ and ensureGoMod preserves the replace => ./vendor-sdk
	// directive because the resolved SDK IS the vendored copy.
	_, err = codegen.Generate(dir, codegen.Options{})
	if err != nil {
		t.Fatalf("codegen.Generate: %v", err)
	}
}

// buildScaffoldArtifact compiles the scaffold source tree into a subprocess-
// mode executable and returns the artifact path + hash. Uses the same build
// options as handleNativeBuild.
func buildScaffoldArtifact(t *testing.T, sourceDir, projectID string) (string, string) {
	t.Helper()
	artifactPath, artifactHash, err := pluginhost.BuildNativeArtifact(pluginhost.NativeBuildOptions{
		ProjectID:   projectID,
		SourceRoot:  sourceDir,
		EntryModule: "main.gen.go",
		OutDir:      filepath.Join(sourceDir, ".build"),
		Mode:        pluginhost.ModeSubprocess, // dev transport: standalone executable, zero cgo
	})
	if err != nil {
		t.Fatalf("build scaffold artifact: %v", err)
	}
	return artifactPath, artifactHash
}

// TestScaffoldArtifactLoadEndToEnd is the pluginhost-level acceptance test for
// the A1 native scaffold. It verifies the full compile→load→invoke→unload
// cycle through the production ArtifactLoader path:
//
//  1. The scaffold templates render and codegen produces a valid subprocess
//     executable (main.gen.go + main_run.gen.go + handlers.go + vendored SDK).
//  2. The agent-owned handlers.go is implemented (fill in handlePing).
//  3. ArtifactLoader.Load spawns the executable over the subprocess transport,
//     invokes OnLoad (which registers the "ping" callable), and installs the
//     namespaced handler.
//  4. Invoking "ping" returns the implemented {"Pong":"ok"} response.
//  5. Unload kills the process cleanly.
//
// This complements TestNativeScaffoldE2ERegistration (which tests appmanager
// orchestration) by verifying the actual subprocess artifact loads and the
// vendored SDK dispatches correctly. Skipped when the Go toolchain is
// unavailable, or when the scaffold templates are not checked out.
func TestScaffoldArtifactLoadEndToEnd(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("native plugins not supported on this platform")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain not available")
	}

	// Generate scaffold into a temp directory.
	sourceDir := t.TempDir()
	generateScaffoldFromTemplates(t, sourceDir, "e2eplugin")

	// The three-layer model leaves handlers.go as the agent-owned file:
	// codegen writes stubs (sdk.ErrNotImplemented); the agent implements
	// them. Simulate the agent step so the loaded plugin responds.
	handlersPath := filepath.Join(sourceDir, "handlers.go")
	handlersContent := `// handlers.go — agent-owned implementation.
package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

// handlePing returns a pong payload per the PingResponse schema.
func handlePing(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{Payload: map[string]string{"Pong": "ok"}}, nil
}
`
	if err := os.WriteFile(handlersPath, []byte(handlersContent), 0o644); err != nil {
		t.Fatalf("write handlers.go: %v", err)
	}

	// Read the generated manifest to construct the artifact_load request.
	manifestData, err := os.ReadFile(filepath.Join(sourceDir, "app.manifest.json"))
	if err != nil {
		t.Fatalf("read generated manifest: %v", err)
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	// Build the artifact.
	artifactPath, artifactHash := buildScaffoldArtifact(t, sourceDir, manifest.ID)

	// Set up the ArtifactLoader with the production transportOpener; its
	// dev-mode selection routes the executable artifact to the subprocess
	// transport (processOpener), which spawns the plugin and speaks the
	// stdin/stdout framing protocol.
	host := &captureHost{}
	loader := pluginhost.NewArtifactLoader(host)
	loader.SetOpener(&transportOpener{devMode: true, inprocess: loaderOpener{}, subprocess: &processOpener{}})

	// Construct the ABI the same way handleNativeBuild does.
	abi := gen.PluginAbi{
		Name:            "spore-plugin",
		Version:         1,
		Encoding:        pluginhost.BinaryCodecV1,
		InvokeSymbol:    "PluginInvoke",
		ContractVersion: "1",
		Isolation:       pluginhost.IsolationSubprocess,
		TrustClass:      pluginhost.TrustFirstParty,
		Signer:          pluginhost.NativeBuildSigner,
	}

	// Load the artifact.
	loadResp, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
		ArtifactPath: artifactPath,
		ArtifactHash: artifactHash,
		Manifest:     manifest,
		Abi:          abi,
	})
	if err != nil {
		t.Fatalf("ArtifactLoader.Load: %v", err)
	}
	if loadResp.PluginID != manifest.ID {
		t.Fatalf("load PluginID = %q, want %q", loadResp.PluginID, manifest.ID)
	}

	// Invoke the default callable ("ping").
	pingRoute := pluginhost.PluginCallID(manifest.ID, "ping")
	h, ok := host.handler(pingRoute)
	if !ok {
		t.Fatalf("handler for %q not registered", pingRoute)
	}
	resp, err := h(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("invoke ping: %v", err)
	}
	var pong struct {
		Pong string `json:"Pong"`
	}
	if err := json.Unmarshal(resp, &pong); err != nil {
		t.Fatalf("decode ping response %q: %v", resp, err)
	}
	if pong.Pong != "ok" {
		t.Fatalf("ping response = %q, want %q", pong.Pong, "ok")
	}

	// Unload the artifact (clean teardown).
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: manifest.ID}); err != nil {
		t.Fatalf("unload: %v", err)
	}

	// Handler must be removed after unload.
	if _, ok := host.handler(pingRoute); ok {
		t.Fatal("handler should be removed after unload")
	}
}
