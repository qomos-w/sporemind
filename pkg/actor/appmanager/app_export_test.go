package appmanager

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// zipEntryNames lists the file names inside zip bytes.
func zipEntryNames(t *testing.T, data []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open exported zip: %v", err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

func hasEntry(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestAppExport_SporeZipRoundTripsVerification exports a spore app whose
// project carries schema descriptors and build noise, then replays the
// exported bytes through install_local's reader (parse + signature
// verification). Schema descriptors are package content and must travel in
// the zip (the receiving host needs them to register the app's typed
// schemas) and be covered by the signed hash; excluded build artifacts must
// stay out of the zip.
func TestAppExport_SporeZipRoundTripsVerification(t *testing.T) {
	env := newBP7Project(t, "app-export-spore-")
	env.write("app.manifest.json", `{
		"id": "app.export.spore",
		"name": "ExportSpore",
		"version": "0.1.0",
		"runtime": "spore",
		"protocolVersion": 1,
		"namespace": "app.exportspore",
		"Schemas": [{"Name": "Obj", "Hash": "objhash"}]
	}`)
	env.write("main.spore", "export fun main(): int = 42")
	env.write("index.html", "<html>real asset</html>")
	env.write("go.sum", "github.com/some/dep v1.2.3 h1:abcdef==\n")
	env.write("app.descriptors.json", `{"Obj":{"Kind":"object","Name":"Obj"}}`)

	a := newBP7Actor(t)
	ctx := env.ctx()

	resp, err := a.handleAppExport(ctx, gen.AppManagerAppExportReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleAppExport: %v", err)
	}
	if len(resp.PackageData) == 0 {
		t.Fatalf("empty PackageData")
	}
	if resp.PackageHash == "" {
		t.Fatalf("empty PackageHash")
	}
	pub, err := base64.StdEncoding.DecodeString(resp.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("PublicKey is not base64 Ed25519 key: len=%d err=%v", len(pub), err)
	}

	names := zipEntryNames(t, resp.PackageData)
	for _, want := range []string{"app.manifest.json", "main.spore", "index.html", descriptorFile, localPackageSigName, localPackagePubName} {
		if !hasEntry(names, want) {
			t.Errorf("zip missing entry %q (entries: %v)", want, names)
		}
	}
	for _, forbid := range []string{"go.sum"} {
		if hasEntry(names, forbid) {
			t.Errorf("zip must not contain %q (entries: %v)", forbid, names)
		}
	}

	// Reader-side replay: parse + verify must accept the export as-is.
	pkg, err := a.parseLocalZipPackage(resp.PackageData)
	if err != nil {
		t.Fatalf("exported zip rejected by install_local reader: %v", err)
	}
	if !pkg.HasSig || !pkg.HasPub {
		t.Fatalf("exported zip missing signature entries")
	}
	if pkg.Manifest.ID != "app.export.spore" {
		t.Fatalf("manifest id = %q", pkg.Manifest.ID)
	}
	if _, ok := pkg.Modules["main.spore"]; !ok {
		t.Fatalf("entry module missing after reader replay: %v", pkg.Modules)
	}
	if string(pkg.Assets["index.html"]) != "<html>real asset</html>" {
		t.Fatalf("asset round-trip mismatch: %q", pkg.Assets["index.html"])
	}
	// Descriptors are content, not assets: the reader must surface them on
	// SchemaDescriptors (register_project needs them) and keep them out of the
	// asset bundle (they must not be served).
	if _, ok := pkg.SchemaDescriptors["Obj"]; !ok {
		t.Fatalf("schema descriptors missing after reader replay: %v", pkg.SchemaDescriptors)
	}
	if _, leaked := pkg.Assets[descriptorFile]; leaked {
		t.Fatalf("descriptors leaked into Assets: %v", pkg.Assets)
	}

	// The standalone signature must verify against the declared package hash
	// and the resp's public key.
	sig, err := decodeBase64Bytes(pkg.Sig)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(resp.PackageHash), sig) {
		t.Fatalf("PACKAGE.sig does not verify over resp.PackageHash with resp.PublicKey")
	}

	// Tamper with a module and ensure the reader rejects the zip.
	tampered := map[string][]byte{}
	zr, err := zip.NewReader(bytes.NewReader(resp.PackageData), int64(len(resp.PackageData)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry: %v", err)
		}
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read entry: %v", err)
		}
		_ = rc.Close()
		content := buf.Bytes()
		if f.Name == "main.spore" {
			content = append([]byte(nil), content...)
			content[0] ^= 0xff
		}
		tampered[f.Name] = content
	}
	if _, err := a.parseLocalZipPackage(buildLocalZip(t, tampered)); err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("tampered zip must fail signature verification, got: %v", err)
	}
}

// exportNativeCtx builds a FakeCtx whose pluginhost.native_build answers with
// the artifact at artifactRel; project.* calls delegate to the bp7 fixture
// planner.
func exportNativeCtx(t *testing.T, e *bp7Env, artifactRel string, abi gen.PluginAbi) *testutil.FakeCtx {
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
			if callID == "pluginhost.native_build" {
				return gen.NativeBuildResp{
					Result:       gen.NativeBuildResult{Success: true, ArtifactPath: e.root + "/" + artifactRel, ArtifactHash: "build-report-hash"},
					ManifestPath: e.root + "/app.manifest.json",
					Abi:          abi,
				}, nil
			}
			return base.call(callID, payload)
		}}
	}
	return ctx
}

// TestAppExport_NativeZipIncludesArtifactAndAbi exports a native app and
// replays it through install_local's reader: the zip must carry abi.json and
// the single artifact binary, and the signature must verify — which only
// holds if the export hash counts abi.json and the artifact as assets, the
// exact classification the reader applies.
func TestAppExport_NativeZipIncludesArtifactAndAbi(t *testing.T) {
	env := newBP7Project(t, "app-export-native-")
	env.write("app.manifest.json", `{
		"id": "app.export.native",
		"name": "ExportNative",
		"version": "0.1.0",
		"runtime": "native",
		"protocolVersion": 1,
		"namespace": "app.exportnative"
	}`)
	env.write("main.gen.go", "package main\n")
	env.write("index.html", "<html>native asset</html>")
	env.write(".sporecode/build/app-test.exe", "MZ-fake-native-binary")

	a := newBP7Actor(t)
	ctx := exportNativeCtx(t, env, ".sporecode/build/app-test.exe", scaffoldAbi())

	resp, err := a.handleAppExport(ctx, gen.AppManagerAppExportReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleAppExport: %v", err)
	}

	names := zipEntryNames(t, resp.PackageData)
	for _, want := range []string{"app.manifest.json", "main.gen.go", "index.html", "abi.json", "app-test.exe", localPackageSigName, localPackagePubName} {
		if !hasEntry(names, want) {
			t.Errorf("native zip missing entry %q (entries: %v)", want, names)
		}
	}

	pkg, err := a.parseLocalZipPackage(resp.PackageData)
	if err != nil {
		t.Fatalf("exported native zip rejected by install_local reader: %v", err)
	}
	if pkg.ArtifactName != "app-test.exe" {
		t.Fatalf("artifact name = %q", pkg.ArtifactName)
	}
	if string(pkg.Artifact) != "MZ-fake-native-binary" {
		t.Fatalf("artifact bytes mismatch: %q", pkg.Artifact)
	}
	if pkg.Abi == nil {
		t.Fatalf("abi missing after reader replay")
	}
}

// TestAppExport_NativeZipExcludesVendoredSDKAndStrayBinaries pins the
// packaging sweep: a native app directory typically carries a vendored SDK
// tree (vendor-sdk/, re-materialized by dev_generate) and legacy build
// outputs (*.exe next to the sources). Neither may enter the package — the
// vendored SDK is generated material, and a stray binary would make the
// installed zip contain multiple artifact candidates, which install_local
// rejects with "multiple artifact binaries".
func TestAppExport_NativeZipExcludesVendoredSDKAndStrayBinaries(t *testing.T) {
	env := newBP7Project(t, "app-export-native-noise-")
	env.write("app.manifest.json", `{
		"id": "app.export.native.noise",
		"name": "ExportNativeNoise",
		"version": "0.1.0",
		"runtime": "native",
		"protocolVersion": 1,
		"namespace": "app.exportnoise"
	}`)
	env.write("main.gen.go", "package main\n")
	env.write("index.html", "<html>native asset</html>")
	env.write(".sporecode/build/app-test.exe", "MZ-fake-native-binary")
	env.write("vendor-sdk/manifest.go", "package sdk\n\nfunc DefaultAbi() {}\n")
	env.write("vendor-sdk/host.go", "package sdk\n\nfunc Invoke() {}\n")
	env.write("app-other.exe", "MZ-stray-legacy-binary")
	env.write("stale.dll", "MZ-stray-legacy-library")

	a := newBP7Actor(t)
	ctx := exportNativeCtx(t, env, ".sporecode/build/app-test.exe", scaffoldAbi())

	resp, err := a.handleAppExport(ctx, gen.AppManagerAppExportReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleAppExport: %v", err)
	}

	names := zipEntryNames(t, resp.PackageData)
	for _, want := range []string{"app.manifest.json", "main.gen.go", "index.html", "abi.json", "app-test.exe", localPackageSigName, localPackagePubName} {
		if !hasEntry(names, want) {
			t.Errorf("native zip missing entry %q (entries: %v)", want, names)
		}
	}
	for _, forbid := range []string{
		"vendor-sdk/manifest.go", "vendor-sdk/host.go",
		"app-other.exe", "stale.dll",
	} {
		if hasEntry(names, forbid) {
			t.Errorf("zip must not contain %q (entries: %v)", forbid, names)
		}
	}

	// Reader replay: with the stray binaries excluded the package parses as
	// a valid native install source with exactly one artifact candidate.
	pkg, err := a.parseLocalZipPackage(resp.PackageData)
	if err != nil {
		t.Fatalf("exported native zip rejected by install_local reader: %v", err)
	}
	if pkg.ArtifactName != "app-test.exe" || string(pkg.Artifact) != "MZ-fake-native-binary" {
		t.Fatalf("artifact = %q (%d bytes), want app-test.exe", pkg.ArtifactName, len(pkg.Artifact))
	}
}

// TestAppExport_OmitsProjectIDForBoundAgentCaller pins the agent-facing
// contract: an agent bound to one project calls app_export with an empty
// ProjectId — the host resolves the bound project from the turn-engine
// injected CallerAgentId (unforgeable), the same contract as
// register_project / dev_generate. Hand-transcribed 32-hex ids are the
// exact failure mode this removes.
func TestAppExport_OmitsProjectIDForBoundAgentCaller(t *testing.T) {
	env := newBP7Project(t, "app-export-bound-")
	env.write("app.manifest.json", `{
		"id": "app.export.bound",
		"name": "ExportBound",
		"version": "0.1.0",
		"runtime": "spore",
		"protocolVersion": 1,
		"namespace": "app.exportbound"
	}`)
	env.write("main.spore", "export fun main(): int = 42")

	a := newBP7Actor(t)
	agentID := testutil.GenActorID().String()
	boundProject := bp7ProjectID(t)

	// One planner answering both the workspace binding lookup and the
	// project.* calls over the fixture filesystem.
	ctx := testutil.HumanCtx(testutil.GenActorID())
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == workspaceServiceName {
			return wsRef, true
		}
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
	base := env.planner()
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			switch callID {
			case "workspace.list_agents":
				return gen.AgentRefListResp{Items: []gen.AgentRef{{ActorID: agentID, ProjectID: boundProject, DisplayName: "Bound Agent"}}}, nil
			}
			return base.call(callID, payload)
		}}
	}

	resp, err := a.handleAppExport(ctx, gen.AppManagerAppExportReq{CallerAgentID: agentID})
	if err != nil {
		t.Fatalf("handleAppExport with empty ProjectId: %v", err)
	}
	if len(resp.PackageData) == 0 {
		t.Fatalf("empty PackageData")
	}
	names := zipEntryNames(t, resp.PackageData)
	if !hasEntry(names, "app.manifest.json") || !hasEntry(names, "main.spore") {
		t.Fatalf("bound-project export missing entries: %v", names)
	}
}

// TestAppExport_SigningKeyPersistsAcrossRestart: the export signing key is
// generated once and restored from saveState — a regenerated key would
// invalidate every previously exported zip.
func TestAppExport_SigningKeyPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	newTestActor := func() *Actor {
		return &Actor{
			actorID:  "appmanager-export-key-test",
			store:    persist.NewFSPersist(dir),
			Apps:     map[string]gen.AppManifest{},
			Records:  map[string]appRecord{},
			children: map[string]string{},
		}
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a1 := newTestActor()
	k1, err := a1.exportSigningKey(ctx)
	if err != nil {
		t.Fatalf("exportSigningKey (first): %v", err)
	}
	k1b, err := a1.exportSigningKey(ctx)
	if err != nil {
		t.Fatalf("exportSigningKey (repeat): %v", err)
	}
	if !bytes.Equal(k1, k1b) {
		t.Fatalf("exportSigningKey returned different keys across calls")
	}
	if err := a1.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	a2 := newTestActor()
	if err := a2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	k2, err := a2.exportSigningKey(ctx)
	if err != nil {
		t.Fatalf("exportSigningKey (after restart): %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatalf("signing key not restored across restart")
	}
}
