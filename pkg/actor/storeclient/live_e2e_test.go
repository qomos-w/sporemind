package storeclient

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// channel resolves the test channel (dev|production) from the environment,
// defaulting to dev so the test doubles as the regression env's default path.
func channel() string {
	if c := os.Getenv("SPORECLOUD_LIVE_CHANNEL"); c != "" {
		return c
	}
	return "dev"
}

// zipEntryNames lists the entry names of an in-memory zip.
func zipEntryNames(b []byte) (map[string]bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	return names, nil
}

// TestLiveCloudEndToEnd exercises the full store install pipeline against a
// running sporecloud dev stack (index → download → verify → decrypt → hash).
// Skipped unless SPORECLOUD_LIVE_URL is set; the stack needs a published
// store entry and SPORECLOUD_LIVE_TOKEN carrying a valid JWT.
func TestLiveCloudEndToEnd(t *testing.T) {
	base := os.Getenv("SPORECLOUD_LIVE_URL")
	token := os.Getenv("SPORECLOUD_LIVE_TOKEN")
	slug := os.Getenv("SPORECLOUD_LIVE_SLUG")
	if base == "" || token == "" {
		t.Skip("SPORECLOUD_LIVE_URL / SPORECLOUD_LIVE_TOKEN not set")
	}
	if slug == "" {
		slug = "daily-tools"
	}

	a := &Actor{http: &http.Client{Timeout: httpTimeout}}
	a.state.BaseURL = base
	a.state.Channel = "dev"
	a.state.AuthToken = token

	wire, _, _, err := a.fetchIndex(context.Background(), true)
	if err != nil {
		t.Fatalf("fetchIndex: %v", err)
	}
	_, version, err := pickStoreVersion(wire, slug, "")
	if err != nil {
		t.Fatalf("pickStoreVersion: %v", err)
	}
	t.Logf("picked %s@%s payload=%s", slug, version.Version, version.PayloadSha256[:16])

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		joinURL(base, "/store/plugins/"+slug+"/download?version="+version.Version), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	artifact, headers, err := a.downloadWithHeaders(req)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	for _, h := range []string{"X-Signature", "X-Content-Key", "X-Payload-Sha256"} {
		if headers.Get(h) == "" {
			t.Fatalf("missing header %s", h)
		}
	}

	// Pinned dev key from keys.go — the same constant production code uses.
	pub, err := officialPubKey("dev", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyStoreSignature(pub, headers.Get("X-Signature"), artifact); err != nil {
		t.Fatalf("signature verify with pinned dev key: %v", err)
	}
	if !matchesSha256(headers.Get("X-Ciphertext-Sha256"), artifact) {
		t.Fatal("ciphertext hash mismatch")
	}
	zipBytes, err := decryptStoreArtifact(headers.Get("X-Content-Key"), artifact)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !matchesSha256(headers.Get("X-Payload-Sha256"), zipBytes) {
		t.Fatal("payload hash mismatch")
	}
	if !matchesSha256(version.PayloadSha256, zipBytes) {
		t.Fatal("payload hash does not match the immutable index record")
	}
	t.Logf("verified+decrypted %d-byte install_local zip from %s", len(zipBytes), base)

	// The decrypted payload must be a zip with a root app.manifest.json —
	// the install_local entry contract.
	names, err := zipEntryNames(zipBytes)
	if err != nil {
		t.Fatalf("zip read: %v", err)
	}
	if !names["app.manifest.json"] {
		t.Fatalf("decrypted payload missing root app.manifest.json: %v", names)
	}
}

// TestLiveCloudFullNativePackage pins the complete native package contract
// against the live cloud: a store version whose zip carries the compiled
// artifact + abi.json (the seed pipeline's fully-built path, e.g. 0.2.1).
func TestLiveCloudFullNativePackage(t *testing.T) {
	base := os.Getenv("SPORECLOUD_LIVE_URL")
	token := os.Getenv("SPORECLOUD_LIVE_TOKEN")
	version := os.Getenv("SPORECLOUD_LIVE_FULL_VERSION")
	if base == "" || token == "" || version == "" {
		t.Skip("SPORECLOUD_LIVE_URL / SPORECLOUD_LIVE_TOKEN / SPORECLOUD_LIVE_FULL_VERSION not set")
	}

	a := &Actor{http: &http.Client{Timeout: httpTimeout}}
	a.state.BaseURL = base
	a.state.Channel = channel()
	a.state.ProductionPubKey = os.Getenv("SPORECLOUD_LIVE_PROD_PUBKEY")
	a.state.AuthToken = token

	wire, _, _, err := a.fetchIndex(context.Background(), true)
	if err != nil {
		t.Fatalf("fetchIndex: %v", err)
	}
	slug, entry := "", (*storeVersionWire)(nil)
	for i := range wire.Store {
		for j := range wire.Store[i].Versions {
			if wire.Store[i].Versions[j].Version == version {
				slug, entry = wire.Store[i].Slug, &wire.Store[i].Versions[j]
			}
		}
	}
	if entry == nil {
		t.Skipf("version %s not in live index", version)
	}
	if ok, reason := versionCompat(entry); !ok {
		t.Fatalf("live version %s incompatible: %s", version, reason)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		joinURL(base, "/store/plugins/"+slug+"/download?version="+version), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	artifact, headers, err := a.downloadWithHeaders(req)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	pub, err := officialPubKey(a.state.Channel, a.state.ProductionPubKey)
	if err != nil {
		t.Fatalf("officialPubKey(%s): %v", a.state.Channel, err)
	}
	if err := verifyStoreSignature(pub, headers.Get("X-Signature"), artifact); err != nil {
		t.Fatalf("verify: %v", err)
	}
	zipBytes, err := decryptStoreArtifact(headers.Get("X-Content-Key"), artifact)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !matchesSha256(entry.PayloadSha256, zipBytes) {
		t.Fatal("payload hash does not match index record")
	}
	names, err := zipEntryNames(zipBytes)
	if err != nil {
		t.Fatalf("zip read: %v", err)
	}
	hasArtifact := false
	for name := range names {
		switch strings.ToLower(filepath.Ext(name)) {
		case ".exe", ".dll", ".so", ".dylib":
			hasArtifact = true
		}
	}
	for _, want := range []string{"app.manifest.json", "abi.json"} {
		if !names[want] {
			t.Fatalf("full native package missing %s: %v", want, names)
		}
	}
	if !hasArtifact {
		t.Fatalf("full native package missing artifact binary (.exe/.dll/.so/.dylib): %v", names)
	}
	t.Logf("version %s: %d-byte package with abi.json + artifact verified end-to-end", version, len(zipBytes))
}

// TestLiveCloudCommunityChain runs the community install pipeline against the
// live cloud: /store/index community entry → public mirror tarball → sha256
// pin → extraction → manifest/compat gates → module collection. The native
// build hop (identical code) is covered by TestCommunityPipelineRealBuild;
// this test proves the cloud-facing half end to end.
func TestLiveCloudCommunityChain(t *testing.T) {
	base := os.Getenv("SPORECLOUD_LIVE_URL")
	slug := os.Getenv("SPORECLOUD_LIVE_COMMUNITY_SLUG")
	if base == "" || slug == "" {
		t.Skip("SPORECLOUD_LIVE_URL / SPORECLOUD_LIVE_COMMUNITY_SLUG not set")
	}

	a := &Actor{http: &http.Client{Timeout: httpTimeout}}
	a.state.BaseURL = base
	a.state.Channel = "dev"

	wire, _, _, err := a.fetchIndex(context.Background(), true)
	if err != nil {
		t.Fatalf("fetchIndex: %v", err)
	}
	entry := findCommunityEntry(wire, slug)
	if entry == nil {
		t.Skipf("community slug %q not in live index", slug)
	}
	if len(entry.CommitSha) != 40 {
		t.Fatalf("live entry commit_sha invalid: %q", entry.CommitSha)
	}

	codeload, err := codeloadURL(entry.RepoUrl, entry.CommitSha)
	if err != nil {
		t.Fatalf("codeloadURL: %v", err)
	}
	tarball, err := a.fetchVerifiedTarball(context.Background(),
		joinURL(base, entry.MirrorUrl), codeload, entry.TarballSha256)
	if err != nil {
		t.Fatalf("fetchVerifiedTarball: %v", err)
	}
	t.Logf("mirror delivered %d bytes, sha256 pinned to %s", len(tarball), entry.TarballSha256[:16])

	appDir, cleanup, err := extractCommunityApp(tarball)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	defer cleanup()
	manifest, err := readManifest(appDir)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if entry.Manifest.ID != "" && manifest.ID != entry.Manifest.ID {
		t.Fatalf("manifest Id %q != index entry %q", manifest.ID, entry.Manifest.ID)
	}
	if manifest.ProtocolVersion != hostProtocolVersion {
		t.Fatalf("manifest protocol %d != host %d", manifest.ProtocolVersion, hostProtocolVersion)
	}
	if ok, reason := sdkCompatible(manifest.SdkVersion); !ok {
		t.Fatalf("sdk incompatible: %s", reason)
	}
	files, err := collectAppFiles(appDir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if _, ok := files.modules["main.gen.go"]; !ok {
		t.Fatal("main.gen.go missing from collected modules")
	}
	if len(files.descriptors) == 0 {
		t.Fatal("app.descriptors.json missing from collected package")
	}
	t.Logf("community chain verified: %s (%s v%s), %d modules + descriptors",
		slug, manifest.ID, manifest.Version, len(files.modules))
}

// TestLiveProductionIndex validates the anonymous production /store/index
// through the production wire types and compatibility gates — no token
// needed. Signature-chain verification of production artifacts additionally
// requires an authenticated JWT (TestLiveCloudEndToEnd against the prod URL).
func TestLiveProductionIndex(t *testing.T) {
	base := os.Getenv("SPORECLOUD_PROD_URL")
	if base == "" {
		t.Skip("SPORECLOUD_PROD_URL not set")
	}

	a := &Actor{http: &http.Client{Timeout: httpTimeout}}
	a.state.BaseURL = base
	a.state.Channel = "production"

	wire, cacheTTL, stale, err := a.fetchIndex(context.Background(), true)
	if err != nil {
		t.Fatalf("fetchIndex: %v", err)
	}
	if len(wire.Store) == 0 {
		t.Fatal("production index has no store entries")
	}
	for i := range wire.Store {
		app := &wire.Store[i]
		if app.CurrentVersion == "" {
			t.Fatalf("entry %q missing current_version", app.Slug)
		}
		picked := false
		for j := range app.Versions {
			if ok, _ := versionCompat(&app.Versions[j]); ok {
				picked = true
			}
		}
		t.Logf("store %q current=%s versions=%d compatible=%v", app.Slug, app.CurrentVersion, len(app.Versions), picked)
	}
	if cacheTTL.Before(time.Now()) || stale {
		t.Fatalf("unexpected cache state ttl=%v stale=%v", cacheTTL, stale)
	}
}
