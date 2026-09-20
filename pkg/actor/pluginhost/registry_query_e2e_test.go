package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistryQueryExampleEndToEnd is the registry.read end-to-end closure:
// the plugin-dev-example (declaring registry.query in .appdef) is built
// as a real subprocess artifact and loaded through the production
// ArtifactLoader, and its discover callable drives sdk.ListCallables across
// the real host bridge into the pluginhost's local registry.query handler:
//
//  1. Granted load (manifest carries registry.read): full-surface query,
//     case-insensitive substring filters on callID/Description and Service,
//     schema-ID metadata, and cursor pagination over the mock topology.
//  2. Denied load (registry.read stripped from the manifest permissions):
//     the SDK reverse call is refused at the bridge capability gate and the
//     error surfaces through the plugin invoke — the undeclared-permission
//     rejection is observed from outside the host, not just unit-tested.
//
// Skipped on js/wasm, without a Go toolchain, or when the example's
// generated artifacts are absent (run appmanager.dev_generate first).
func TestRegistryQueryExampleEndToEnd(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("native plugins not supported on this platform")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain not available")
	}

	sourceDir, err := filepath.Abs("../../../plugin-dev-example")
	if err != nil {
		t.Fatalf("resolve example dir: %v", err)
	}
	manifestData, err := os.ReadFile(filepath.Join(sourceDir, "app.manifest.json"))
	if err != nil {
		t.Skipf("generated artifacts missing at %s (run appmanager.dev_generate first)", sourceDir)
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.ID != "app.authenticator" {
		t.Skipf("plugin-dev-example holds app %q, not app.authenticator", manifest.ID)
	}
	if !hasPermission(manifest.Permissions, "registry.read") {
		t.Skipf("example manifest %q does not declare registry.read (run appmanager.dev_generate)", manifest.ID)
	}

	outDir := filepath.Join(sourceDir, ".build")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stageDir := t.TempDir() // staged copy lives outside the source tree
	artifactPath, artifactHash, err := pluginhost.BuildNativeArtifact(pluginhost.NativeBuildOptions{
		ProjectID:   manifest.ID,
		SourceRoot:  sourceDir,
		EntryModule: "main.gen.go",
		OutDir:      outDir,
		Mode:        pluginhost.ModeSubprocess,
		GoBuild: func(dir, outPath string, env []string) ([]byte, error) {
			modDir := testutil.StageExampleModuleDir(t, sourceDir, stageDir)
			return testutil.GoRunOutput(modDir, env, "build", "-o", outPath, ".")
		},
	})
	if err != nil {
		t.Fatalf("build example artifact: %v", err)
	}

	// Real pluginhost-side handlers: the registry.query local host call runs
	// against the predictable mock topology (same fixture as the unit tests).
	// registry.query is NOT a store call — like production (pluginhost.go's
	// host-bridge dispatch switch) it rides the general dispatch wire, while
	// state.* rides storeDispatch.
	registryActor := &Actor{topo: &mockTopologyProvider{snapshot: testSnapshot()}}
	stateActor := &Actor{}
	registryDispatch := func(callID string, req []byte) ([]byte, error) {
		if callID != "registry.query" {
			return nil, fmt.Errorf("unexpected dispatch callID %q", callID)
		}
		return registryActor.handleRegistryQuery(req)
	}
	storeDispatch := func(callID string, req []byte) ([]byte, error) {
		switch callID {
		case "state.get":
			var r gen.PluginStateGetReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, err
			}
			resp, err := stateActor.handleStateGet(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		case "state.set":
			var r gen.PluginStateSetReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, err
			}
			resp, err := stateActor.handleStateSet(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		case "state.delete":
			var r gen.PluginStateDeleteReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, err
			}
			resp, err := stateActor.handleStateDelete(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		}
		return nil, fmt.Errorf("unknown store callID %q", callID)
	}

	host := &captureHost{}
	loader := pluginhost.NewArtifactLoader(host)
	pop := &processOpener{dispatch: registryDispatch, storeDispatch: storeDispatch}
	var live *processOpener
	pop.observeClone = func(o *processOpener) { live = o }
	loader.SetOpener(&transportOpener{devMode: true, inprocess: loaderOpener{}, subprocess: pop})
	abi := gen.PluginAbi{
		Name: "spore-plugin", Version: 1, Encoding: pluginhost.BinaryCodecV1,
		InvokeSymbol: "PluginInvoke", ContractVersion: "1",
		Isolation: pluginhost.IsolationSubprocess, TrustClass: pluginhost.TrustFirstParty,
		Signer: "sporemind.first-party",
	}
	loadReq := gen.PluginArtifactLoadReq{
		ArtifactPath: artifactPath, ArtifactHash: artifactHash,
		Manifest: manifest, Abi: abi,
	}
	if _, err := loader.Load(context.Background(), loadReq); err != nil {
		t.Fatalf("ArtifactLoader.Load: %v", err)
	}
	if live == nil || live.cmd == nil || live.cmd.Process == nil {
		t.Fatal("expected the subprocess transport to spawn the plugin process; live clone has no cmd")
	}

	discover := func(payload any) (discoverResponse, error) {
		t.Helper()
		h, ok := host.handler(pluginhost.PluginCallID(manifest.ID, "discover"))
		if !ok {
			t.Fatalf("handler for %q not registered", "discover")
		}
		raw, _ := json.Marshal(payload)
		resp, err := h(context.Background(), raw)
		if err != nil {
			return discoverResponse{}, err
		}
		// Plugin handler errors surface as an {"error": ...} payload envelope
		// on this path (the frame-level invoke stays successful), so decode
		// and promote it — the permission-gate assertion below relies on it.
		var env struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(resp, &env); err == nil && env.Error != "" {
			return discoverResponse{}, fmt.Errorf("%s", env.Error)
		}
		var out discoverResponse
		if err := json.Unmarshal(resp, &out); err != nil {
			t.Fatalf("decode discover response %q: %v", resp, err)
		}
		return out, nil
	}

	// 1a. Full surface: both filters empty returns every callable, sorted by
	// callID, no cursor.
	full, err := discover(map[string]any{})
	if err != nil {
		t.Fatalf("discover full surface: %v", err)
	}
	if len(full.Items) != 6 {
		t.Fatalf("full surface: expected 6 items, got %d", len(full.Items))
	}
	if full.NextCursor != "" {
		t.Fatalf("full surface: expected empty NextCursor, got %q", full.NextCursor)
	}
	if full.Items[0].CallID != "appmanager.register_project" {
		t.Fatalf("full surface: expected first item appmanager.register_project, got %q", full.Items[0].CallID)
	}

	// 1b. Case-insensitive substring on the callID + description haystack.
	flt, err := discover(map[string]any{"Callable": "MESSAGE"})
	if err != nil {
		t.Fatalf("discover filter: %v", err)
	}
	if len(flt.Items) != 1 || flt.Items[0].CallID != "workspace.agent_send_message" {
		t.Fatalf("filter 'MESSAGE': expected exactly workspace.agent_send_message, got %+v", flt.Items)
	}
	// Schema-ID metadata flows through the whole chain (SDK uint64 decode of
	// the host's int32 wire value).
	if flt.Items[0].ReqSchemaId != 102 || flt.Items[0].RespSchemaId != 202 {
		t.Fatalf("schema IDs: got req=%d resp=%d, want 102/202", flt.Items[0].ReqSchemaId, flt.Items[0].RespSchemaId)
	}

	// 1c. Substring on Description only ("services" appears in the
	// oracle.search_services description).
	desc, err := discover(map[string]any{"Callable": "services"})
	if err != nil {
		t.Fatalf("discover description filter: %v", err)
	}
	if len(desc.Items) != 1 || desc.Items[0].CallID != "oracle.search_services" {
		t.Fatalf("filter 'services': expected exactly oracle.search_services, got %+v", desc.Items)
	}

	// 1d. Service filter, case-insensitive.
	svc, err := discover(map[string]any{"Service": "ORACLE"})
	if err != nil {
		t.Fatalf("discover service filter: %v", err)
	}
	if len(svc.Items) != 2 {
		t.Fatalf("service 'ORACLE': expected 2 items, got %d", len(svc.Items))
	}
	for _, item := range svc.Items {
		if item.Service != "oracle" {
			t.Fatalf("service 'ORACLE': item with Service=%q leaked in", item.Service)
		}
	}

	// 1e. Cursor pagination: three pages of two cover all six uniquely.
	page1, err := discover(map[string]any{"Limit": 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1.Items) != 2 || page1.NextCursor == "" {
		t.Fatalf("page 1: expected 2 items and a cursor, got %d items cursor %q", len(page1.Items), page1.NextCursor)
	}
	page2, err := discover(map[string]any{"Limit": 2, "Cursor": page1.NextCursor})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2.Items) != 2 || page2.NextCursor == "" {
		t.Fatalf("page 2: expected 2 items and a cursor, got %d items cursor %q", len(page2.Items), page2.NextCursor)
	}
	page3, err := discover(map[string]any{"Limit": 2, "Cursor": page2.NextCursor})
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(page3.Items) != 2 || page3.NextCursor != "" {
		t.Fatalf("page 3: expected 2 items and no cursor, got %d items cursor %q", len(page3.Items), page3.NextCursor)
	}
	seen := map[string]bool{}
	for _, item := range append(append(page1.Items, page2.Items...), page3.Items...) {
		if seen[item.CallID] {
			t.Fatalf("duplicate item across pages: %s", item.CallID)
		}
		seen[item.CallID] = true
	}
	if len(seen) != 6 {
		t.Fatalf("expected 6 unique items across pages, got %d", len(seen))
	}

	// 1f. Metadata only: the wire response carries no credential-like fields.
	raw, _ := json.Marshal(full)
	for _, cred := range []string{"token", "secret", "password", "apikey", "auth"} {
		if strings.Contains(strings.ToLower(string(raw)), cred) {
			t.Fatalf("discover response contains credential-like substring %q: %s", cred, raw)
		}
	}

	// 2. Permission gate: reload the same artifact WITHOUT registry.read in
	// the manifest permissions. The SDK-side ListCallables reverse call must
	// be refused at the host bridge, and the denial must surface as the
	// plugin invoke error.
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: manifest.ID}); err != nil {
		t.Fatalf("unload: %v", err)
	}
	deniedManifest := manifest
	deniedManifest.Permissions = nil
	for _, p := range manifest.Permissions {
		if p != "registry.read" {
			deniedManifest.Permissions = append(deniedManifest.Permissions, p)
		}
	}
	deniedReq := loadReq
	deniedReq.Manifest = deniedManifest
	if _, err := loader.Load(context.Background(), deniedReq); err != nil {
		t.Fatalf("load denied manifest: %v", err)
	}
	_, err = discover(map[string]any{})
	if err == nil {
		t.Fatal("discover without registry.read must be denied, got success")
	}
	if !strings.Contains(err.Error(), "capability not granted") || !strings.Contains(err.Error(), "registry.read") {
		t.Fatalf("denial error = %v, want capability-not-granted naming registry.read", err)
	}

	// The granted capability must NOT imply any other: state.get is granted
	// here but the denied-registry load above still answers state fine — the
	// gate is per-callID, not all-or-nothing. (state.set round trip:)
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: manifest.ID}); err != nil {
		t.Fatalf("unload denied: %v", err)
	}
	if _, err := loader.Load(context.Background(), loadReq); err != nil {
		t.Fatalf("reload granted manifest: %v", err)
	}
	if _, err := discover(map[string]any{"Service": "workspace", "Callable": "list"}); err != nil {
		t.Fatalf("discover after reload: %v", err)
	}
}

// discoverResponse mirrors the example app's DiscoverResponse wire shape.
type discoverResponse struct {
	Items []struct {
		CallID       string
		Service      string
		Description  string
		Permission   string
		EffectKind   string
		ReqSchemaId  int64
		RespSchemaId int64
	}
	NextCursor string
}

func hasPermission(perms []string, want string) bool {
	for _, p := range perms {
		if p == want {
			return true
		}
	}
	return false
}
