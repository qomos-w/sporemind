package appmanager

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// fakeRefForInstall is a minimal ref.Ref usable for tests that need a spawned
// child without booting a real actor system.
type fakeRefForInstall struct {
	actorID id.ActorID
}

func (f fakeRefForInstall) ID() id.ActorID          { return f.actorID }
func (f fakeRefForInstall) Service() (string, bool) { return "", false }
func (f fakeRefForInstall) Invoke(ctx context.Context, callID string, payload any, headers ...map[string]string) *invoke.Call {
	return nil
}

func installLocalTestActor(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.SpawnFn = func(props actor.Props, name string) (ref.Ref, error) {
		return fakeRefForInstall{actorID: testutil.GenActorID()}, nil
	}
	a := &Actor{
		actorID:           "appmanager-install-test",
		Apps:              map[string]gen.AppManifest{},
		Records:           map[string]appRecord{},
		children:          map[string]string{},
		bindings:          appbinding.NewRegistry(),
		FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
		store:             persist.NewFSPersist(t.TempDir()),
	}
	return a, ctx
}

func minimalSporeManifest(appID string) gen.AppManifest {
	return gen.AppManifest{
		ID:              appID,
		Name:            "TestApp",
		Version:         "1.0.0",
		Runtime:         "spore",
		ProtocolVersion: 1,
		Namespace:       appID,
		Callables: []gen.AppCallableDescriptor{
			{ID: "main", RequestSchema: "Req", ResponseSchema: "Resp"},
		},
	}
}

func buildLocalZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func manifestJSON(t *testing.T, m gen.AppManifest) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return b
}

// signedLocalZip builds a valid spore_app zip and returns the zip bytes along
// with the public key (base64) used to sign it. When corruptSig is true the
// signature is flipped so verification fails.
func signedLocalZip(t *testing.T, m gen.AppManifest, corruptSig bool) ([]byte, ed25519.PublicKey) {
	files, pub := signedLocalFiles(t, m, corruptSig)
	return buildLocalZip(t, files), pub
}

// signedLocalFiles returns the same signed package as plain files, for
// writing directory-based install sources in tests.
func signedLocalFiles(t *testing.T, m gen.AppManifest, corruptSig bool) (map[string][]byte, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}

	files := map[string][]byte{
		"app.manifest.json": manifestJSON(t, m),
		"main.spore":        []byte("export default {}"),
	}

	// Compute the package hash the same way install_local does for a spore app.
	pkgHash, err := canonicalPackageHash(m, "main.spore", map[string]string{"main.spore": "export default {}"}, nil, nil, nil, "")
	if err != nil {
		t.Fatalf("canonical package hash: %v", err)
	}
	sig := ed25519.Sign(priv, []byte(pkgHash))
	if corruptSig && len(sig) > 0 {
		sig[0] ^= 0xff
	}

	files["PACKAGE.sig"] = []byte(base64.StdEncoding.EncodeToString(sig))
	files["PACKAGE.pub"] = []byte(base64.StdEncoding.EncodeToString(pub))
	return files, pub
}

// writeLocalPackageDir materializes package files as a directory source.
func writeLocalPackageDir(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %q: %v", name, err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
	return dir
}

func TestInstallLocal_SporeAppSuccess(t *testing.T) {
	config.SetExeDirForTest(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a, ctx := installLocalTestActor(t)
	manifest := minimalSporeManifest("app.local.success")
	zipData := buildLocalZip(t, map[string][]byte{
		"app.manifest.json": manifestJSON(t, manifest),
		"main.spore":        []byte("export default {}"),
	})

	resp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: zipData})
	if err != nil {
		t.Fatalf("install_local: %v", err)
	}
	if resp.Status.ID != manifest.ID {
		t.Fatalf("status id = %q, want %q", resp.Status.ID, manifest.ID)
	}
	if resp.Status.Runtime != "spore" {
		t.Fatalf("status runtime = %q, want spore", resp.Status.Runtime)
	}
}

func TestInstallLocal_BadSignature(t *testing.T) {
	config.SetExeDirForTest(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a, ctx := installLocalTestActor(t)
	manifest := minimalSporeManifest("app.local.badsig")
	zipData, _ := signedLocalZip(t, manifest, true)

	_, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: zipData})
	if err == nil || !contains(err.Error(), "PACKAGE.sig verification failed") {
		t.Fatalf("expected signature verification failure, got: %v", err)
	}
}

func TestInstallLocal_MissingManifest(t *testing.T) {
	config.SetExeDirForTest(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a, ctx := installLocalTestActor(t)
	zipData := buildLocalZip(t, map[string][]byte{
		"main.spore": []byte("export default {}"),
	})

	_, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: zipData})
	if err == nil || !contains(err.Error(), "missing app.manifest.json") {
		t.Fatalf("expected missing manifest error, got: %v", err)
	}
}

func TestInstallLocal_DirectorySporeAppSuccess(t *testing.T) {
	config.SetExeDirForTest(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a, ctx := installLocalTestActor(t)
	manifest := minimalSporeManifest("app.local.dir.success")
	dir := writeLocalPackageDir(t, map[string][]byte{
		"app.manifest.json": manifestJSON(t, manifest),
		"main.spore":        []byte("export default {}"),
		"assets/icon.png":   []byte("png-bytes"),
	})

	resp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{Path: dir})
	if err != nil {
		t.Fatalf("install_local: %v", err)
	}
	if resp.Status.ID != manifest.ID {
		t.Fatalf("status id = %q, want %q", resp.Status.ID, manifest.ID)
	}

	record, ok := a.Records[manifest.ID]
	if !ok {
		t.Fatalf("app record missing after directory install")
	}
	if got := record.Assets["assets/icon.png"]; string(got) != "png-bytes" {
		t.Fatalf("nested asset not collected from directory: %q", got)
	}
}

func TestInstallLocal_DirectorySkipsDotAndNodeModules(t *testing.T) {
	config.SetExeDirForTest(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a, ctx := installLocalTestActor(t)
	manifest := minimalSporeManifest("app.local.dir.skip")
	dir := writeLocalPackageDir(t, map[string][]byte{
		"app.manifest.json":         manifestJSON(t, manifest),
		"main.spore":                []byte("export default {}"),
		".DS_Store":                 []byte("junk"),
		".hidden/secret.txt":        []byte("junk"),
		"node_modules/pkg/index.js": []byte("junk"),
	})

	// A symlink must not leak host files into the installed assets.
	outside := filepath.Join(t.TempDir(), "outside-secret.txt")
	if err := os.WriteFile(outside, []byte("host-secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "leaked.txt")); err != nil {
		t.Skipf("cannot create symlink on this platform/user: %v", err)
	}

	resp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{Path: dir})
	if err != nil {
		t.Fatalf("install_local: %v", err)
	}
	if resp.Status.ID != manifest.ID {
		t.Fatalf("status id = %q, want %q", resp.Status.ID, manifest.ID)
	}
	record := a.Records[manifest.ID]
	for name, content := range record.Assets {
		if strings.Contains(name, "node_modules") || strings.HasPrefix(name, ".") {
			t.Fatalf("excluded entry leaked into assets: %q", name)
		}
		if string(content) == "host-secret" {
			t.Fatalf("symlinked host file leaked into assets: %q", name)
		}
	}
}

func TestInstallLocal_DirectoryBadSignature(t *testing.T) {
	config.SetExeDirForTest(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a, ctx := installLocalTestActor(t)
	manifest := minimalSporeManifest("app.local.dir.badsig")
	files, _ := signedLocalFiles(t, manifest, true)
	dir := writeLocalPackageDir(t, files)

	_, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{Path: dir})
	if err == nil || !contains(err.Error(), "PACKAGE.sig verification failed") {
		t.Fatalf("expected signature verification failure, got: %v", err)
	}
}

func TestInstallLocal_DirectoryMissingManifest(t *testing.T) {
	config.SetExeDirForTest(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a, ctx := installLocalTestActor(t)
	dir := writeLocalPackageDir(t, map[string][]byte{
		"main.spore": []byte("export default {}"),
	})

	_, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{Path: dir})
	if err == nil || !contains(err.Error(), "missing app.manifest.json") {
		t.Fatalf("expected missing manifest error, got: %v", err)
	}
}

func TestInstallLocal_DeclaredCapabilityPasses(t *testing.T) {
	config.SetExeDirForTest(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a, ctx := installLocalTestActor(t)
	manifest := minimalSporeManifest("app.local.declared")
	manifest.Permissions = []string{appbinding.CapAppState}
	manifest.Callables = []gen.AppCallableDescriptor{
		{ID: "get", RequestSchema: "GetReq", ResponseSchema: "GetResp", Permission: appbinding.CapAppState},
	}
	zipData := buildLocalZip(t, map[string][]byte{
		"app.manifest.json": manifestJSON(t, manifest),
		"main.spore":        []byte("export default {}"),
	})

	resp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: zipData})
	if err != nil {
		t.Fatalf("install_local: %v", err)
	}
	if resp.Status.ID != manifest.ID {
		t.Fatalf("status id = %q, want %q", resp.Status.ID, manifest.ID)
	}
	if len(resp.Status.GrantedCapabilities) != 1 || resp.Status.GrantedCapabilities[0] != appbinding.CapAppState {
		t.Fatalf("GrantedCapabilities = %v, want [app.state]", resp.Status.GrantedCapabilities)
	}
}

func TestInstallLocal_ValidSignatureSuccess(t *testing.T) {
	config.SetExeDirForTest(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a, ctx := installLocalTestActor(t)
	manifest := minimalSporeManifest("app.local.signed")
	zipData, _ := signedLocalZip(t, manifest, false)

	resp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: zipData})
	if err != nil {
		t.Fatalf("install_local: %v", err)
	}
	if resp.Status.ID != manifest.ID {
		t.Fatalf("status id = %q, want %q", resp.Status.ID, manifest.ID)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
