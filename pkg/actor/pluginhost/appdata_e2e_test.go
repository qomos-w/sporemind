package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestAppDataSubprocessEndToEnd proves the app.data grant over the production
// subprocess path: OnLoad config dataDir → sdk.DataDir() → the plugin opens a
// goleveldb database inside its private <appDir>/.sporecode/appdata directory,
// writes keys, reads them back with an ordered prefix scan, and the data
// survives unload + reload (the directory is durable app storage, not process
// state). The grant semantics themselves (permission gating, layout
// fail-closed, eager mkdir) are pinned in appmanager's TestAppDataDirFor; the
// config JSON built here mirrors appmanager's backendLoadConfig.
func TestAppDataSubprocessEndToEnd(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("native plugins not supported on this platform")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain not available")
	}
	sdkDir, ok := testutil.FindSDKDir(t)
	if !ok {
		t.Skip("sporemind-plugin-sdk checkout not found")
	}
	if _, err := os.Stat(filepath.Join(sdkDir, "go.mod")); err != nil {
		t.Skipf("SDK checkout at %s has no go.mod", sdkDir)
	}

	// Process-scoped base dir: Windows keeps a running executable locked, so
	// an auto-cleaning t.TempDir could fail its removal while the plugin is
	// alive; the explicit Unload below releases it first.
	base, err := os.MkdirTemp("", "sporemind-appdata-e2e-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	appDir := filepath.Join(base, "myapp")
	buildDir := filepath.Join(appDir, ".sporecode", "build")
	appdataDir := filepath.Join(appDir, ".sporecode", "appdata")
	for _, d := range []string{buildDir, appdataDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeAppdataProbeModule(t, appDir, sdkDir)
	testutil.GoRun(t, appDir, nil, "mod", "tidy")

	artifactPath, artifactHash, err := pluginhost.BuildNativeArtifact(pluginhost.NativeBuildOptions{
		ProjectID:   "app.appdata-probe",
		SourceRoot:  appDir,
		EntryModule: "main.go",
		OutDir:      buildDir,
		Mode:        pluginhost.ModeSubprocess,
		GoBuild: func(dir, outPath string, env []string) ([]byte, error) {
			return testutil.GoRunOutput(dir, env, "build", "-o", outPath, ".")
		},
	})
	if err != nil {
		t.Fatalf("build appdata probe artifact: %v", err)
	}

	onLoadConfig, err := json.Marshal(map[string]string{
		"httpAddr": "127.0.0.1:0",
		"dataDir":  appdataDir,
	})
	if err != nil {
		t.Fatal(err)
	}

	host := &captureHost{}
	loader := pluginhost.NewArtifactLoader(host)
	loader.SetOpener(&transportOpener{devMode: true, inprocess: loaderOpener{}, subprocess: &processOpener{}})
	manifest := gen.AppManifest{
		ID: "app.appdata-probe", Name: "Appdata Probe", Version: "1.0.0",
		Runtime: pluginhost.NativeRuntime, ProtocolVersion: 2, Namespace: "plugin.app.appdata-probe", Permissions: []string{"app.data"},
		Callables: []gen.AppCallableDescriptor{
			{ID: "datadir", RequestSchema: "empty", ResponseSchema: "dataDirResp"},
			{ID: "kv_put", RequestSchema: "kvPutReq", ResponseSchema: "kvResp"},
			{ID: "kv_scan", RequestSchema: "kvScanReq", ResponseSchema: "kvScanResp"},
		},
	}
	abi := gen.PluginAbi{
		Name: "spore-plugin", Version: 1, Encoding: pluginhost.BinaryCodecV1,
		InvokeSymbol: "PluginInvoke", ContractVersion: "1",
		Isolation: pluginhost.IsolationSubprocess, TrustClass: pluginhost.TrustFirstParty,
		Signer: "sporemind.first-party",
	}
	load := func() {
		t.Helper()
		if _, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
			ArtifactPath: artifactPath, ArtifactHash: artifactHash,
			Manifest: manifest, Abi: abi, OnLoadConfig: onLoadConfig,
		}); err != nil {
			t.Fatalf("ArtifactLoader.Load: %v", err)
		}
	}
	unload := func() {
		t.Helper()
		if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: manifest.ID}); err != nil {
			t.Fatalf("unload: %v", err)
		}
	}

	call := func(name string, payload any, out any) {
		t.Helper()
		h, ok := host.handler(pluginhost.PluginCallID(manifest.ID, name))
		if !ok {
			t.Fatalf("handler for %q not registered", name)
		}
		raw, _ := json.Marshal(payload)
		resp, err := h(context.Background(), raw)
		if err != nil {
			t.Fatalf("invoke %s: %v", name, err)
		}
		if err := json.Unmarshal(resp, out); err != nil {
			t.Fatalf("decode %s response %q: %v", name, resp, err)
		}
	}

	load()

	// 1. The plugin sees exactly the granted directory, created by the host.
	var dd struct {
		Dir    string `json:"dir"`
		Exists bool   `json:"exists"`
	}
	call("datadir", map[string]any{}, &dd)
	if dd.Dir != appdataDir {
		t.Fatalf("sdk.DataDir() = %q, want %q", dd.Dir, appdataDir)
	}
	if !dd.Exists {
		t.Fatal("granted appdata dir must exist")
	}

	// 2. goleveldb put + ordered prefix scan inside the appdata dir.
	want := map[string]string{"k1": "alpha", "k2": "beta", "k3": "gamma"}
	for k, v := range want {
		var put struct {
			OK bool `json:"ok"`
		}
		call("kv_put", map[string]any{"key": k, "value": v}, &put)
		if !put.OK {
			t.Fatalf("kv_put(%s) failed", k)
		}
	}
	var scan struct {
		Keys   []string `json:"keys"`
		Values []string `json:"values"`
	}
	call("kv_scan", map[string]any{"prefix": "k"}, &scan)
	if len(scan.Keys) != 3 || scan.Keys[0] != "k1" || scan.Keys[1] != "k2" || scan.Keys[2] != "k3" {
		t.Fatalf("kv_scan keys = %v, want ordered [k1 k2 k3]", scan.Keys)
	}
	for i, k := range scan.Keys {
		if scan.Values[i] != want[k] {
			t.Fatalf("kv_scan[%d] = %s=%s, want %s", i, k, scan.Values[i], want[k])
		}
	}
	// The database files really live under the granted appdata directory.
	if _, err := os.Stat(filepath.Join(appdataDir, "db", "CURRENT")); err != nil {
		t.Fatalf("leveldb CURRENT missing under appdata: %v", err)
	}

	// 3. Data survives unload + reload (durable app storage).
	unload()
	load()
	call("kv_scan", map[string]any{"prefix": "k"}, &scan)
	if len(scan.Keys) != 3 || scan.Values[2] != "gamma" {
		t.Fatalf("kv data lost across unload/reload: %+v", scan)
	}
	unload()
}

// writeAppdataProbeModule writes a minimal subprocess-mode plugin that uses
// sdk.DataDir() for its storage: a goleveldb database under <DataDir>/db.
func writeAppdataProbeModule(t *testing.T, appDir, sdkDir string) {
	t.Helper()
	files := map[string]string{
		"go.mod": fmt.Sprintf(`module app.appdata-probe

go 1.27.0

require (
	github.com/qomos-w/sporemind-plugin-sdk v0.0.0
	github.com/syndtr/goleveldb v1.0.0
)

replace github.com/qomos-w/sporemind-plugin-sdk => %s
`, filepath.ToSlash(sdkDir)),
		"main_nocgo.go": `//go:build !cgo

package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

func main() { sdk.RunProcess() }
`,
		"main.go": `package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

var (
	dbMu sync.Mutex
	db   *leveldb.DB
)

func openDB() (*leveldb.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db != nil {
		return db, nil
	}
	d, err := leveldb.OpenFile(filepath.Join(sdk.DataDir(), "db"), nil)
	if err != nil {
		return nil, err
	}
	db = d
	return db, nil
}

func closeDB() {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db != nil {
		_ = db.Close()
		db = nil
	}
}

func init() {
	sdk.Register(&sdk.Plugin{
		Manifest: sdk.Manifest{
			ID: "app.appdata-probe", Name: "Appdata Probe", Version: "1.0.0",
			Permissions: []string{sdk.PermAppData},
			Callables: []sdk.Callable{
				{ID: "datadir", RequestSchema: "empty", ResponseSchema: "dataDirResp"},
				{ID: "kv_put", RequestSchema: "kvPutReq", ResponseSchema: "kvResp"},
				{ID: "kv_scan", RequestSchema: "kvScanReq", ResponseSchema: "kvScanResp"},
			},
		},
		OnLoad: func(ctx sdk.Context) error {
			ctx.RegisterCallable("datadir", func(req sdk.Request) (sdk.Response, error) {
				dir := sdk.DataDir()
				_, statErr := os.Stat(dir)
				return sdk.Response{Payload: map[string]any{"dir": dir, "exists": statErr == nil}}, nil
			})
			ctx.RegisterCallable("kv_put", func(req sdk.Request) (sdk.Response, error) {
				var p struct {
					Key   string ` + "`json:\"key\"`" + `
					Value string ` + "`json:\"value\"`" + `
				}
				if err := json.Unmarshal(req.Payload, &p); err != nil {
					return sdk.Response{}, err
				}
				h, err := openDB()
				if err != nil {
					return sdk.Response{}, err
				}
				if err := h.Put([]byte(p.Key), []byte(p.Value), nil); err != nil {
					return sdk.Response{}, err
				}
				return sdk.Response{Payload: map[string]any{"ok": true}}, nil
			})
			ctx.RegisterCallable("kv_scan", func(req sdk.Request) (sdk.Response, error) {
				var p struct {
					Prefix string ` + "`json:\"prefix\"`" + `
				}
				if err := json.Unmarshal(req.Payload, &p); err != nil {
					return sdk.Response{}, err
				}
				h, err := openDB()
				if err != nil {
					return sdk.Response{}, err
				}
				var keys, values []string
				it := h.NewIterator(nil, nil)
				defer it.Release()
				for it.First(); it.Valid(); it.Next() {
					if p.Prefix != "" && !bytes.HasPrefix(it.Key(), []byte(p.Prefix)) {
						continue
					}
					keys = append(keys, string(it.Key()))
					values = append(values, string(it.Value()))
				}
				return sdk.Response{Payload: map[string]any{"keys": keys, "values": values}}, nil
			})
			return nil
		},
		OnUnload: func(ctx sdk.Context) error {
			closeDB()
			return nil
		},
	})
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(appDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}
