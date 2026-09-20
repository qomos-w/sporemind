package appmanager

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/protocol"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// This file is the E2E acceptance suite for the native-app-zip-export
// workflow: a real export (handleAppExport output) must install through
// the full install_local handler — not just the zip reader — on the same
// appmanager actor, end with the app registered and routed, and every
// tamper of the exported archive (content, signature, or public key) must
// be rejected by signature verification with no registration side effects.
// It also pins the dev-surface worktree-caller rejection (register_project)
// so the guard cannot regress while export/install evolve.

// configSetupForInstallTest isolates the exe/data dirs the register/save
// path derives, the same way install_local_test.go does per test.
func configSetupForInstallTest(t *testing.T) {
	t.Helper()
	config.SetExeDirForTest(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
}

// exportLoopSporeManifestJSON is a spore manifest that survives
// doRegister: it declares a callable (spore apps must), keeps
// permissions/schemas empty, and has no agent binding.
func exportLoopSporeManifestJSON(t *testing.T, appID string) string {
	t.Helper()
	m := gen.AppManifest{
		ID:              appID,
		Name:            "ExportLoopSpore",
		Version:         "0.1.0",
		Runtime:         "spore",
		ProtocolVersion: 1,
		Namespace:       appID,
		Permissions:     []string{},
		Schemas:         []gen.AppSchemaRef{},
		Callables: []gen.AppCallableDescriptor{
			{ID: "main", RequestSchema: "MainReq", ResponseSchema: "MainResp"},
		},
		Events: []gen.AppEventDescriptor{},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return string(data)
}

// exportLoopNativeManifestJSON is a native manifest with a ping callable
// and a capability binding, matching the shape the pluginhost mocks answer.
func exportLoopNativeManifestJSON(t *testing.T, appID string) string {
	t.Helper()
	m := gen.AppManifest{
		ID:              appID,
		Name:            "ExportLoopNative",
		Version:         "0.1.0",
		Runtime:         "native",
		ProtocolVersion: 1,
		Namespace:       appID,
		Permissions:     []string{},
		Schemas:         []gen.AppSchemaRef{},
		Callables: []gen.AppCallableDescriptor{
			{ID: "ping", RequestSchema: "PingRequest", ResponseSchema: "PingResponse", Service: appID},
		},
		Events: []gen.AppEventDescriptor{},
		AgentBinding: &gen.AppAgentBinding{
			Capability: &gen.AgentCapabilityBinding{Callables: []string{"ping"}},
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return string(data)
}

// wireExportEnv copies the project/pluginhost lookup + planner wiring of a
// bp7 fixture ctx onto an install-capable ctx (one that has SpawnFn), so a
// single ctx drives both handleAppExport and handleInstallLocal.
func wireExportEnv(ctx *testutil.FakeCtx, env *bp7Env) {
	ectx := env.ctx()
	ctx.LookupServiceFn = ectx.LookupServiceFn
	ctx.LookupIDFn = ectx.LookupIDFn
	ctx.PlannerFn = ectx.PlannerFn
}

// exportSporeFixture builds a bp7 spore project, then exports it on an
// install-capable actor+ctx pair, returning all three for the install leg.
func exportSporeFixture(t *testing.T, name, appID string) (*Actor, *testutil.FakeCtx, gen.AppManagerAppExportResp) {
	t.Helper()
	env := newBP7Project(t, name)
	env.write("app.manifest.json", exportLoopSporeManifestJSON(t, appID))
	env.write("main.spore", "export fun main(): int = 42")
	env.write("index.html", "<html>export loop</html>")

	a, ctx := installLocalTestActor(t)
	wireExportEnv(ctx, env)

	resp, err := a.handleAppExport(ctx, gen.AppManagerAppExportReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleAppExport: %v", err)
	}
	if len(resp.PackageData) == 0 || resp.PackageHash == "" {
		t.Fatalf("incomplete export response: %+v", resp)
	}
	return a, ctx, resp
}

// assertAppRegisteredAndRouted verifies the post-install state that makes
// the app callable: registry entry, running record (with the round-tripped
// package hash when wantHash is set), and a live child/pluginhost route.
func assertAppRegisteredAndRouted(t *testing.T, a *Actor, appID, wantHash string) {
	t.Helper()
	manifest, ok := a.Apps[appID]
	if !ok {
		t.Fatalf("app %q not in Apps after install", appID)
	}
	if len(manifest.Callables) != 1 {
		t.Fatalf("manifest callables lost in round trip: %+v", manifest.Callables)
	}
	record, ok := a.Records[appID]
	if !ok {
		t.Fatalf("record for %q missing after install", appID)
	}
	if record.State != stateRunning {
		t.Fatalf("record state = %q, want running", record.State)
	}
	if wantHash != "" && !strings.EqualFold(record.PackageHash, wantHash) {
		t.Fatalf("record package hash = %q, want export hash %q", record.PackageHash, wantHash)
	}
	if a.children[appID] == "" {
		t.Fatalf("child route for %q missing after install", appID)
	}
}

// TestAppExportInstallLoop_SporeZipBytes: export → install_local with the
// PackageData zip bytes → the app is registered, running, and routed.
func TestAppExportInstallLoop_SporeZipBytes(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.export.loop.sporebytes"
	a, ctx, resp := exportSporeFixture(t, "app-export-loop-bytes-", appID)

	iresp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: resp.PackageData})
	if err != nil {
		t.Fatalf("install_local (PackageData): %v", err)
	}
	if iresp.Status.ID != appID || iresp.Status.State != stateRunning {
		t.Fatalf("install status = %+v, want %s running", iresp.Status, appID)
	}
	assertAppRegisteredAndRouted(t, a, appID, resp.PackageHash)
}

// TestAppExportInstallLoop_SporeZipPath: same loop, but the exported zip is
// persisted to disk first and installed via Path (the .zip file branch of
// loadLocalPackage).
func TestAppExportInstallLoop_SporeZipPath(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.export.loop.sporepath"
	a, ctx, resp := exportSporeFixture(t, "app-export-loop-path-", appID)

	zipPath := filepath.Join(t.TempDir(), "export.zip")
	if err := os.WriteFile(zipPath, resp.PackageData, 0o644); err != nil {
		t.Fatalf("persist export zip: %v", err)
	}

	iresp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{Path: zipPath})
	if err != nil {
		t.Fatalf("install_local (Path): %v", err)
	}
	if iresp.Status.ID != appID || iresp.Status.State != stateRunning {
		t.Fatalf("install status = %+v, want %s running", iresp.Status, appID)
	}
	assertAppRegisteredAndRouted(t, a, appID, resp.PackageHash)
}

// rewriteZipEntries unpacks zip bytes, lets mutate edit the entry map, and
// repacks. Used to simulate tampering with an exported archive.
func rewriteZipEntries(t *testing.T, data []byte, mutate func(map[string][]byte)) []byte {
	t.Helper()
	files := map[string][]byte{}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %q: %v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read entry %q: %v", f.Name, err)
		}
		files[f.Name] = content
	}
	mutate(files)
	return buildLocalZip(t, files)
}

// TestAppExportInstallLoop_TamperedZipRejected: every tamper of the exported
// archive — a flipped module byte, a flipped asset byte, a changed manifest,
// a replaced/corrupted signature, a replaced public key, or a dangling
// signature without its key — must be rejected by install_local's signature
// gate, and none of them may leave a registered app behind.
func TestAppExportInstallLoop_TamperedZipRejected(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.export.loop.tamper"
	_, _, resp := exportSporeFixture(t, "app-export-loop-tamper-", appID)

	// A structurally valid but foreign signature: signed over the original
	// package hash with an attacker key. Decoding succeeds; verification
	// must still fail.
	foreignPub, foreignPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("foreign keygen: %v", err)
	}
	foreignSig := ed25519.Sign(foreignPriv, []byte(resp.PackageHash))

	cases := []struct {
		name    string
		mutate  func(t *testing.T, files map[string][]byte)
		wantErr string
	}{
		{
			name: "module byte flipped",
			mutate: func(t *testing.T, files map[string][]byte) {
				files["main.spore"][0] ^= 0xff
			},
			wantErr: "PACKAGE.sig verification failed",
		},
		{
			name: "asset byte flipped",
			mutate: func(t *testing.T, files map[string][]byte) {
				files["index.html"][0] ^= 0xff
			},
			wantErr: "PACKAGE.sig verification failed",
		},
		{
			name: "manifest version changed",
			mutate: func(t *testing.T, files map[string][]byte) {
				var m gen.AppManifest
				if err := json.Unmarshal(files["app.manifest.json"], &m); err != nil {
					t.Fatalf("decode manifest: %v", err)
				}
				m.Version = "9.9.9"
				data, err := json.Marshal(m)
				if err != nil {
					t.Fatalf("re-encode manifest: %v", err)
				}
				files["app.manifest.json"] = data
			},
			wantErr: "PACKAGE.sig verification failed",
		},
		{
			name: "signature replaced with foreign signature",
			mutate: func(t *testing.T, files map[string][]byte) {
				// The original embedded pub stays in place; only the
				// signature is swapped, so verification must fail.
				files[localPackageSigName] = []byte(base64.StdEncoding.EncodeToString(foreignSig))
			},
			wantErr: "PACKAGE.sig verification failed",
		},
		{
			name: "signature payload corrupted",
			mutate: func(t *testing.T, files map[string][]byte) {
				sig, err := base64.StdEncoding.DecodeString(string(files[localPackageSigName]))
				if err != nil {
					t.Fatalf("decode sig: %v", err)
				}
				sig[0] ^= 0xff
				files[localPackageSigName] = []byte(base64.StdEncoding.EncodeToString(sig))
			},
			wantErr: "PACKAGE.sig verification failed",
		},
		{
			name: "public key replaced",
			mutate: func(t *testing.T, files map[string][]byte) {
				files[localPackagePubName] = []byte(base64.StdEncoding.EncodeToString(foreignPub))
			},
			wantErr: "PACKAGE.sig verification failed",
		},
		{
			name: "signature without public key",
			mutate: func(t *testing.T, files map[string][]byte) {
				delete(files, localPackagePubName)
			},
			wantErr: "PACKAGE.sig present but PACKAGE.pub missing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, ctx := installLocalTestActor(t)
			tampered := rewriteZipEntries(t, resp.PackageData, func(files map[string][]byte) {
				tc.mutate(t, files)
			})

			_, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: tampered})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("install_local error = %v, want substring %q", err, tc.wantErr)
			}
			if _, ok := a.Apps[appID]; ok {
				t.Fatalf("tampered zip left app %q registered", appID)
			}
			if _, ok := a.Records[appID]; ok {
				t.Fatalf("tampered zip left a record for %q", appID)
			}
			if _, ok := a.children[appID]; ok {
				t.Fatalf("tampered zip left a child route for %q", appID)
			}
		})
	}
}

// TestAppExportInstallLoop_UnsignedZipInstalls: a zip with the signature
// entries stripped installs fine — verification stays optional on the
// install side (export always signs; the reader-side contract accepts
// unsigned local packages). This pins the documented trust-model decision.
func TestAppExportInstallLoop_UnsignedZipInstalls(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.export.loop.unsigned"
	a, ctx, resp := exportSporeFixture(t, "app-export-loop-unsigned-", appID)

	unsigned := rewriteZipEntries(t, resp.PackageData, func(files map[string][]byte) {
		delete(files, localPackageSigName)
		delete(files, localPackagePubName)
	})

	iresp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: unsigned})
	if err != nil {
		t.Fatalf("install_local (unsigned zip): %v", err)
	}
	if iresp.Status.ID != appID || iresp.Status.State != stateRunning {
		t.Fatalf("install status = %+v, want %s running", iresp.Status, appID)
	}
	assertAppRegisteredAndRouted(t, a, appID, "")
}

// TestAppExportInstallLoop_ResignedZipInstalls: a package re-signed by a
// different key pair (foreign sig + matching foreign pub, contents
// untouched) installs fine. Verification is integrity-only against the
// embedded pub — without a trust store there is no way to tell a re-sign
// apart from an export by a different host, so rejecting it is impossible
// by construction. This pins the trust-model decision alongside the
// unsigned-zip case: only unsigned-vs-signed and intact-vs-tampered are
// distinguishable at install time.
func TestAppExportInstallLoop_ResignedZipInstalls(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.export.loop.resigned"
	a, ctx, resp := exportSporeFixture(t, "app-export-loop-resigned-", appID)

	resignerPub, resignerPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("resigner keygen: %v", err)
	}
	resignedSig := ed25519.Sign(resignerPriv, []byte(resp.PackageHash))

	resigned := rewriteZipEntries(t, resp.PackageData, func(files map[string][]byte) {
		files[localPackageSigName] = []byte(base64.StdEncoding.EncodeToString(resignedSig))
		files[localPackagePubName] = []byte(base64.StdEncoding.EncodeToString(resignerPub))
	})

	iresp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: resigned})
	if err != nil {
		t.Fatalf("install_local (re-signed zip): %v", err)
	}
	if iresp.Status.ID != appID || iresp.Status.State != stateRunning {
		t.Fatalf("install status = %+v, want %s running", iresp.Status, appID)
	}
	assertAppRegisteredAndRouted(t, a, appID, resp.PackageHash)
}

// exportLoopNativeCtx wires a bp7 fixture for the native export→install
// loop: native_build answers with the on-disk fixture artifact (a real .exe
// path so the zip carries an extractable artifact), artifact_load/list/
// assets_put delegate to the fixture planner, and pluginhost.invoke answers
// the post-install callable dispatch.
func exportLoopNativeCtx(t *testing.T, e *bp7Env, artifactRel string) *testutil.FakeCtx {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	}
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 61)
	if err != nil {
		t.Fatal(err)
	}
	projectActorID := id.From(projectCID)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return projectRef, aid == projectActorID
	}
	base := e.planner()
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			switch callID {
			case "pluginhost.native_build":
				return gen.NativeBuildResp{
					Result:       gen.NativeBuildResult{Success: true, ArtifactPath: e.root + "/" + artifactRel, ArtifactHash: "build-report-hash"},
					ManifestPath: e.root + "/app.manifest.json",
					Abi:          scaffoldAbi(),
				}, nil
			case "pluginhost.artifact_load":
				// Mirror production: the load echoes the bytes-derived
				// artifact hash the caller computed from the zip payload,
				// so the registered record hash reproduces the export hash.
				req := payload.(gen.PluginArtifactLoadReq)
				return gen.PluginArtifactLoadResp{
					PluginID:     req.Manifest.ID,
					ArtifactHash: req.ArtifactHash,
					Status:       gen.AppStatus{ID: req.Manifest.ID, Runtime: "native", State: "active"},
				}, nil
			case "pluginhost.invoke":
				return gen.PluginInvokeResp{Payload: []byte(`{"pong":"ok"}`)}, nil
			default:
				return base.call(callID, payload)
			}
		}}
	}
	return ctx
}

// TestAppExportInstallLoop_NativeZip: export a native app (zip carries
// abi.json + the single artifact binary) → install_local loads the artifact
// through pluginhost.artifact_load → the app registers running against the
// pluginhost route, and its callable is actually invocable.
func TestAppExportInstallLoop_NativeZip(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.export.loop.native"
	env := newBP7Project(t, "app-export-loop-native-")
	env.write("app.manifest.json", exportLoopNativeManifestJSON(t, appID))
	env.write("main.gen.go", "package main\n")
	env.write("index.html", "<html>native loop</html>")
	env.write(".sporecode/build/app-loop.exe", "MZ-fake-native-binary")

	a, ictx := installLocalTestActor(t)
	ctx := exportLoopNativeCtx(t, env, ".sporecode/build/app-loop.exe")
	ctx.SpawnFn = ictx.SpawnFn

	resp, err := a.handleAppExport(ctx, gen.AppManagerAppExportReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleAppExport: %v", err)
	}

	iresp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: resp.PackageData})
	if err != nil {
		t.Fatalf("install_local (native): %v", err)
	}
	if iresp.Status.ID != appID || iresp.Status.State != stateRunning || iresp.Status.Runtime != "native" {
		t.Fatalf("install status = %+v, want %s native running", iresp.Status, appID)
	}
	if target := a.children[appID]; target != pluginhostServiceName {
		t.Fatalf("native child routing = %q, want %q", target, pluginhostServiceName)
	}
	record := a.Records[appID]
	if record.ArtifactPath == "" || record.ArtifactHash == "" || record.Abi == nil {
		t.Fatalf("native record missing artifact info: %+v", record)
	}
	if !strings.EqualFold(record.PackageHash, resp.PackageHash) {
		t.Fatalf("record package hash = %q, want export hash %q", record.PackageHash, resp.PackageHash)
	}

	// The installed frontend must be materialized at the app's install dir —
	// the SDK static root the plugin listener serves (LoadConfig.StaticDir).
	// Without it the panel loads /plugin/{id}/ against the host cwd and 404s
	// ("error not found").
	if b, err := os.ReadFile(filepath.Join(installedAppDir(appID), "index.html")); err != nil || string(b) != "<html>native loop</html>" {
		t.Fatalf("installed index.html = %q, err %v", b, err)
	}
	if got := resolveBackendAppDir(appID, record.ArtifactPath); got != installedAppDir(appID) {
		t.Fatalf("backend app dir = %q, want %q", got, installedAppDir(appID))
	}

	// The installed app must actually serve its callable.
	inv, err := a.handleInvoke(ctx, gen.AppManagerInvokeReq{ID: appID, Callable: "ping", AgentID: "loop-agent"})
	if err != nil {
		t.Fatalf("invoke ping after install: %v", err)
	}
	if string(inv.Payload) != `{"pong":"ok"}` {
		t.Fatalf("invoke payload = %q, want {\"pong\":\"ok\"}", string(inv.Payload))
	}
}

// schemaRefFor computes the manifest schema ref (name + hash) the protocol
// validator expects for a descriptor: the hash covers the marshaled
// spore.ObjectDesc the descriptor converts to.
func schemaRefFor(t *testing.T, name string, d gen.AppObjectDescriptor) gen.AppSchemaRef {
	t.Helper()
	objs, err := protocol.AppObjectDescriptors(map[string]gen.AppObjectDescriptor{name: d})
	if err != nil {
		t.Fatalf("AppObjectDescriptors(%s): %v", name, err)
	}
	encoded, err := json.Marshal(objs[name])
	if err != nil {
		t.Fatalf("marshal object desc %s: %v", name, err)
	}
	sum := sha256.Sum256(encoded)
	return gen.AppSchemaRef{Name: name, Hash: hex.EncodeToString(sum[:])}
}

// TestAppExportInstallLoop_SchemasRoundTrip pins the reported failure: an app
// whose manifest declares schemas needs those descriptors when the receiving
// host registers it. The exported package must carry app.descriptors.json,
// the reader must feed it into the register request, and the protocol registry
// must accept it — a zip without descriptors installs into
// "appmanager: schema descriptors are required".
func TestAppExportInstallLoop_SchemasRoundTrip(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.export.loop.schemas"
	reqDesc := gen.AppObjectDescriptor{
		Kind: "struct", Name: "PingRequest", SchemaID: 9001,
		Fields: []gen.AppFieldDescriptor{{Name: "N", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "int"}}},
	}
	respDesc := gen.AppObjectDescriptor{
		Kind: "struct", Name: "PingResponse", SchemaID: 9002,
		Fields: []gen.AppFieldDescriptor{{Name: "Pong", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "string"}}},
	}
	descriptors := map[string]gen.AppObjectDescriptor{"PingRequest": reqDesc, "PingResponse": respDesc}

	manifest := gen.AppManifest{
		ID: appID, Name: "ExportLoopSchemas", Version: "0.1.0", Runtime: "spore",
		ProtocolVersion: 1, Namespace: appID, Permissions: []string{},
		Schemas: []gen.AppSchemaRef{
			schemaRefFor(t, "PingRequest", reqDesc),
			schemaRefFor(t, "PingResponse", respDesc),
		},
		Callables: []gen.AppCallableDescriptor{{ID: "ping", RequestSchema: "PingRequest", ResponseSchema: "PingResponse"}},
		Events:    []gen.AppEventDescriptor{},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	descriptorsJSON, err := json.Marshal(descriptors)
	if err != nil {
		t.Fatalf("marshal descriptors: %v", err)
	}

	env := newBP7Project(t, "app-export-loop-schemas-")
	env.write("app.manifest.json", string(manifestJSON))
	env.write(descriptorFile, string(descriptorsJSON))
	env.write("main.spore", "export fun main(): int = 42")
	env.write("index.html", "<html>schemas loop</html>")

	// A real protocol registry, so registration actually validates the
	// descriptors instead of skipping the check on a nil manager.
	a, ctx := installLocalTestActor(t)
	a.protocol = newProtocolManager(t)
	wireExportEnv(ctx, env)

	resp, err := a.handleAppExport(ctx, gen.AppManagerAppExportReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleAppExport: %v", err)
	}
	if !hasEntry(zipEntryNames(t, resp.PackageData), descriptorFile) {
		t.Fatalf("exported zip missing %s", descriptorFile)
	}

	iresp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: resp.PackageData})
	if err != nil {
		t.Fatalf("install_local (schemas): %v", err)
	}
	if iresp.Status.ID != appID || iresp.Status.State != stateRunning {
		t.Fatalf("install status = %+v, want %s running", iresp.Status, appID)
	}
	assertAppRegisteredAndRouted(t, a, appID, resp.PackageHash)

	record := a.Records[appID]
	if len(record.SchemaDescriptors) != len(descriptors) {
		t.Fatalf("record schema descriptors = %d entries, want %d", len(record.SchemaDescriptors), len(descriptors))
	}
	if _, ok := record.SchemaDescriptors["PingRequest"]; !ok {
		t.Fatalf("PingRequest descriptor not registered: %v", record.SchemaDescriptors)
	}
	// The namespace must be live in the protocol registry, making the app's
	// typed callable usable.
	if _, err := a.protocol.WireID(manifest.Namespace, 9001); err != nil {
		t.Fatalf("schema not registered in protocol manager: %v", err)
	}
}

// TestRegisterProjectRoutesWorktreeBoundCaller pins the dev-surface routing
// for register_project (the sibling of app_export on the project read
// path): a worktree-bound agent is no longer rejected — packaging reads
// resolve against the caller's worktree path (the stub answers
// WorktreePath="/wt/dev"), so the manifest read targets /wt/dev and the
// failure is a plain input error, not a worktree rejection.
func TestRegisterProjectRoutesWorktreeBoundCaller(t *testing.T) {
	a := &Actor{actorID: "appmanager-test"}
	projectCID, cidErr := identity.NewCanonicalID(1700000000000, 1, 1, 62)
	if cidErr != nil {
		t.Fatalf("create project CID: %v", cidErr)
	}
	projectID := id.From(projectCID)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	projectRef := testutil.NewFakeRef(projectID, nil)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return projectRef, aid == projectID
	}
	route := &wtRouteRecorder{}
	ctx.PlannerFn = func() actor.Planner {
		return boundCheckPlanner{bound: true, route: route}
	}

	_, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{
		ProjectID:     projectCID.String(),
		CallerAgentID: "01a000000000000000000000000000ff",
	})
	if err == nil || strings.Contains(err.Error(), "worktree-bound") {
		t.Fatalf("register_project err = %v, want a routed-to-worktree packaging failure, not a worktree rejection", err)
	}
	if len(route.readPaths) == 0 || route.readPaths[0] != "/wt/dev/app.manifest.json" {
		t.Fatalf("project.read_base64 paths = %v, want manifest read at /wt/dev/app.manifest.json", route.readPaths)
	}
}
