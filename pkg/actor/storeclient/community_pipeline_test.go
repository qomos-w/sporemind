package storeclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// TestCommunityPipelineRealBuild runs the community install pipeline against
// a real plugin source tree (plugin-dev-example from this repo) exactly as
// installCommunity does — tarball fetch with sha256 pin, GitHub root-wrapper
// stripping, manifest/compat gates, module collection, a REAL native build
// through pluginhost.CompileFromProject, and install_local zip assembly.
// Skipped with -short (go build takes ~30s) and when the fixture repo is
// absent.
func TestCommunityPipelineRealBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("real go build too slow for -short")
	}
	srcRepo := filepath.Join("..", "..", "..", "plugin-dev-example")
	manifestPath := filepath.Join(srcRepo, "app.manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Skipf("plugin-dev-example fixture not present: %v", err)
	}

	// Serve the repo as a codeload-shaped tarball ({repo}-{sha}/ wrapper).
	tarball := buildDirTarball(t, "plugin-dev-example", "0123456789abcdef0123456789abcdef01234567", srcRepo)
	t.Logf("tarball size = %d bytes", len(tarball))
	sum := sha256.Sum256(tarball)
	sha := hex.EncodeToString(sum[:])
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	}))
	defer mirror.Close()

	a := &Actor{http: mirror.Client()}
	if !matchesSha256(sha, tarball) {
		t.Fatal("pre-flight: matchesSha256 fails on the exact bytes being served — hasher bug")
	}
	// Direct GET to prove byte-exact transport before the pipeline call.
	direct, err := httpGetAll(context.Background(), mirror.Client(), mirror.URL)
	if err != nil {
		t.Fatalf("direct GET: %v", err)
	}
	if len(direct) != len(tarball) || sha256Hex(direct) != sha256Hex(tarball) {
		t.Fatalf("transport mutated payload: got %d bytes %s, want %d bytes %s", len(direct), sha256Hex(direct)[:16], len(tarball), sha256Hex(tarball)[:16])
	}
	got, err := a.fetchVerifiedTarball(context.Background(), mirror.URL, "https://codeload.github.com/x/y/tar.gz/deadbeef", sha)
	if err != nil {
		t.Fatalf("fetchVerifiedTarball: %v", err)
	}
	if len(got) != len(tarball) {
		t.Fatal("mirror must win over codeload")
	}

	appDir, cleanup, err := extractCommunityApp(got)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	defer cleanup()

	manifest, err := readManifest(appDir)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if !strings.HasPrefix(manifest.ID, "app.") {
		t.Fatalf("unexpected manifest id %q", manifest.ID)
	}
	if ok, reason := sdkCompatible(manifest.SdkVersion); !ok {
		t.Fatalf("fixture SDK %q incompatible with host: %s", manifest.SdkVersion, reason)
	}

	files, err := collectAppFiles(appDir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if _, ok := files.modules["main.gen.go"]; !ok {
		t.Fatal("main.gen.go must be collected as module source")
	}
	if len(files.modules) < 3 {
		t.Fatalf("expected several modules, got %v", files.modules)
	}
	// vendor-sdk must never enter the package.
	for name := range files.modules {
		if strings.HasPrefix(name, "vendor-sdk/") {
			t.Fatalf("vendor-sdk leaked into package: %s", name)
		}
	}

	buildResult, err := pluginhost.CompileFromProject(pluginhost.NativeBuildOptions{
		AppName:    manifest.Name,
		AppVersion: manifest.Version,
		SourceRoot: appDir,
		OutDir:     filepath.Join(appDir, ".sporecode", "build"),
	}, filepath.Join(appDir, "app.manifest.json"), "")
	if err != nil {
		t.Fatalf("CompileFromProject (real go build): %s", buildResult.Diagnostic)
	}
	base := filepath.Base(buildResult.ArtifactPath)
	wantPrefix := "plugin-" + strings.ToLower(strings.ReplaceAll(manifest.Name, " ", "-")) + "-" + manifest.Version + "-"
	if !strings.HasPrefix(base, wantPrefix) {
		t.Fatalf("artifact name %q must start with %q (name+version naming)", base, wantPrefix)
	}

	artifactBytes, err := os.ReadFile(buildResult.ArtifactPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	zipBytes, err := assembleInstallZip(manifest, files, artifactBytes)
	if err != nil {
		t.Fatalf("assembleInstallZip: %v", err)
	}
	names, err := zipEntryNames(zipBytes)
	if err != nil {
		t.Fatalf("zip read: %v", err)
	}
	for _, want := range []string{"app.manifest.json", "abi.json", "main.gen.go"} {
		if !names[want] {
			t.Errorf("zip missing %q: %v", want, names)
		}
	}
	artifactName := "plugin"
	if goos := os.Getenv("GOOS"); goos == "" || goos == "windows" {
		if goos != "linux" && goos != "darwin" {
			artifactName += ".exe"
		}
	}
	if !names[artifactName] {
		t.Errorf("zip missing artifact %q: %v", artifactName, names)
	}
	t.Logf("built %s (%d bytes) and assembled a %d-byte install_local zip", base, len(artifactBytes), len(zipBytes))
}

// buildDirTarball packs srcDir into a codeload-shaped tar.gz: every file
// under {root}-{sha}/, skipping vendored/build/dependency noise the same way
// collectAppFiles does.
func buildDirTarball(t *testing.T, repo, sha, srcDir string) []byte {
	t.Helper()
	prefix := repo + "-" + sha + "/"
	var files []struct{ name, content string }
	err := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch {
			case collectSkipDirs[d.Name()]:
				// vendor-sdk stays IN the tarball (the real GitHub repo
				// carries it and the local go build needs it); it is only
				// excluded from the final install package (collectAppFiles).
				if p != srcDir && d.Name() != "vendor-sdk" {
					return filepath.SkipDir
				}
			case strings.HasPrefix(d.Name(), "."):
				// Hidden build/cache dirs (.build, .sporecode, .git …).
				if p != srcDir {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcDir, p)
		files = append(files, struct{ name, content string }{prefix + filepath.ToSlash(rel), string(data)})
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixture: %v", err)
	}
	return buildGithubTarball(t, strings.TrimSuffix(prefix, "/"), func() map[string]string {
		m := map[string]string{}
		for _, f := range files {
			m[strings.TrimPrefix(f.name, prefix)] = f.content
		}
		return m
	}())
}
