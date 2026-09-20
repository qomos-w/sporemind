package storeclient

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestExtractCommunityAppStripsRepoRoot(t *testing.T) {
	tarball := buildGithubTarball(t, "repo-main", map[string]string{
		"app.manifest.json": `{"Id":"app.repo","Name":"Repo","Version":"1.0.0","Runtime":"native","ProtocolVersion":2,"Namespace":"app.repo"}`,
		"main.gen.go":       "package main\n",
		"README.md":         "readme",
	})
	appDir, cleanup, err := extractCommunityApp(tarball)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(appDir, "app.manifest.json")); err != nil {
		t.Fatalf("manifest must be at app root: %v", err)
	}
	m, err := readManifest(appDir)
	if err != nil || m.ID != "app.repo" {
		t.Fatalf("readManifest: %v %+v", err, m)
	}
	files, err := collectAppFiles(appDir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if _, ok := files.modules["main.gen.go"]; !ok {
		t.Fatalf("modules = %v", files.modules)
	}
	if entryModuleFor(files) != "main.gen.go" {
		t.Fatalf("entryModuleFor = %q", entryModuleFor(files))
	}
}

func TestExtractCommunityAppMonorepoSubdir(t *testing.T) {
	tarball := buildGithubTarball(t, "repo-x", map[string]string{
		"plugin/app.manifest.json": `{"Id":"app.sub","Name":"Sub","Version":"0.1.0","Runtime":"native","ProtocolVersion":2,"Namespace":"app.sub"}`,
		"plugin/main.gen.go":       "package main\n",
	})
	appDir, cleanup, err := extractCommunityApp(tarball)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	defer cleanup()
	if !strings.HasSuffix(filepath.ToSlash(appDir), "/plugin") {
		t.Fatalf("appDir = %q, want .../plugin", appDir)
	}
}

func TestExtractRejectsMissingManifest(t *testing.T) {
	tarball := buildGithubTarball(t, "repo-y", map[string]string{"README.md": "x"})
	if _, _, err := extractCommunityApp(tarball); err == nil {
		t.Fatal("tarball without app.manifest.json must be rejected")
	}
}

func TestAssembleInstallZipShape(t *testing.T) {
	manifest := &gen.AppManifest{
		ID: "app.zipshape", Name: "ZipShape", Version: "1.0.0",
		Runtime: "native", ProtocolVersion: 2, Namespace: "app.zipshape",
	}
	files := appFiles{modules: map[string]string{"main.gen.go": "package main\n"}}
	zipBytes, err := assembleInstallZip(manifest, files, []byte("artifact-bytes"))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip read: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	artifactName := "plugin"
	if runtime.GOOS == "windows" {
		artifactName += ".exe"
	}
	for _, want := range []string{"app.manifest.json", "abi.json", artifactName, "main.gen.go"} {
		if !names[want] {
			t.Errorf("zip missing %q (has %v)", want, names)
		}
	}
}

func TestFetchVerifiedTarballMirrorAndFallback(t *testing.T) {
	payload := []byte("tarball-bytes")
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])

	codeloadHits := 0
	codeload := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		codeloadHits++
		_, _ = w.Write(payload)
	}))
	defer codeload.Close()

	a := &Actor{http: codeload.Client()}

	// Mirror down → codeload fallback with matching sha.
	got, err := a.fetchVerifiedTarball(context.Background(), codeload.URL+"/mirror", codeload.URL+"/codeload", sha)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if string(got) != string(payload) || codeloadHits != 1 {
		t.Fatalf("fallback result wrong (hits=%d)", codeloadHits)
	}

	// Mirror healthy with matching sha wins without touching codeload.
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer mirror.Close()
	if _, err := a.fetchVerifiedTarball(context.Background(), mirror.URL, codeload.URL, sha); err != nil {
		t.Fatalf("mirror: %v", err)
	}
	if codeloadHits != 1 {
		t.Fatalf("codeload must not be hit when mirror works (hits=%d)", codeloadHits)
	}

	// Both paths serve bytes with wrong sha → error.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("other"))
	}))
	defer bad.Close()
	if _, err := a.fetchVerifiedTarball(context.Background(), bad.URL, bad.URL, sha); err == nil {
		t.Fatal("sha mismatch on both paths must error")
	}

	// Missing sha pin always errors.
	if _, err := a.fetchVerifiedTarball(context.Background(), mirror.URL, codeload.URL, ""); err == nil {
		t.Fatal("empty sha must error")
	}
}

func TestDownloadVerifiesSignatureChain(t *testing.T) {
	// End-to-end over httptest: download headers → verify → decrypt → hash,
	// the exact store install pipeline minus the appmanager hop.
	plaintext := []byte("zip-payload")
	artifact, keyB64, sigHex, priv := sealForTest(t, plaintext)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-Signature", sigHex)
		w.Header().Set("X-Content-Key", keyB64)
		w.Header().Set("X-Payload-Sha256", sha256Hex(plaintext))
		w.Header().Set("X-Ciphertext-Sha256", sha256Hex(artifact))
		_, _ = w.Write(artifact)
	}))
	defer srv.Close()

	a := &Actor{http: srv.Client()}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	body, headers, err := a.downloadWithHeaders(req)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if err := verifyStoreSignature(priv.Public().(ed25519.PublicKey), headers.Get("X-Signature"), body); err != nil {
		t.Fatalf("verify: %v", err)
	}
	zipBytes, err := decryptStoreArtifact(headers.Get("X-Content-Key"), body)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !matchesSha256(headers.Get("X-Payload-Sha256"), zipBytes) {
		t.Fatal("payload hash mismatch")
	}
	if string(zipBytes) != string(plaintext) {
		t.Fatal("payload drift")
	}
}

// buildGithubTarball builds a tar.gz shaped like a codeload archive: every
// entry under a {root}/ prefix.
func buildGithubTarball(t *testing.T, root string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: root + "/" + name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
