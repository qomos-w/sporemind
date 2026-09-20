package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestNativeBuildPlatformSupportedRejectsCrossCompile(t *testing.T) {
	err := NativeBuildPlatformSupported("linux", "arm64")
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		if err != nil {
			t.Fatalf("expected nil on native platform, got %v", err)
		}
		return
	}
	if err == nil {
		t.Fatal("expected cross-compile rejection")
	}
	if !strings.Contains(err.Error(), "cannot run on host") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildNativeArtifactInvokesGoBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build integration test in short mode")
	}
	dir := t.TempDir()
	outDir := t.TempDir()
	// Create a dummy entry module so build validation passes.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Simulate a successful build by overriding GoBuild.
	var called bool
	var gotDir string
	var gotEnv []string
	artifactPath, artifactHash, err := BuildNativeArtifact(NativeBuildOptions{
		ProjectID:   "test-app",
		SourceRoot:  dir,
		EntryModule: "main.go",
		OutDir:      outDir,
		GoBuild: func(dir, outPath string, env []string) ([]byte, error) {
			called = true
			gotDir = dir
			gotEnv = env
			// Write a dummy artifact at outPath so hashing succeeds.
			if werr := os.WriteFile(outPath, []byte("artifact"), 0o600); werr != nil {
				return nil, werr
			}
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if !called {
		t.Fatal("GoBuild was not invoked")
	}
	if artifactPath == "" || artifactHash == "" {
		t.Fatalf("expected non-empty artifact path/hash: %q %q", artifactPath, artifactHash)
	}
	sum := sha256.Sum256([]byte("artifact"))
	if artifactHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash mismatch: got %q want %q", artifactHash, hex.EncodeToString(sum[:]))
	}
	// Empty Mode defaults to subprocess: build in place (no staging) with
	// cgo disabled and an executable suffix.
	if gotDir != dir {
		t.Fatalf("default mode build dir = %q, want %q (no staging for subprocess)", gotDir, dir)
	}
	if !hasEnv(gotEnv, "CGO_ENABLED=0") {
		t.Fatalf("default mode env missing CGO_ENABLED=0: %v", gotEnv)
	}
	wantName := "plugin-test-app-" + artifactHash[:16] + artifactSuffixForMode(ModeSubprocess)
	if filepath.Base(artifactPath) != wantName {
		t.Fatalf("artifact name %q unexpected, want %q", filepath.Base(artifactPath), wantName)
	}
	if _, statErr := os.Stat(artifactPath); statErr != nil {
		t.Fatalf("published artifact missing: %v", statErr)
	}
}

// hasEnv reports whether env contains the exact key=value entry.
func hasEnv(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

// Rebuilding identical content must reuse the already-published content
// address instead of clobbering the file a loaded plugin maps to.
func TestBuildNativeArtifactContentAddressed(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	build := func() (string, string) {
		path, hash, err := BuildNativeArtifact(NativeBuildOptions{
			ProjectID: "test-app", SourceRoot: dir, EntryModule: "main.go", OutDir: outDir,
			GoBuild: func(_, outPath string, _ []string) ([]byte, error) {
				return nil, os.WriteFile(outPath, []byte("artifact"), 0o600)
			},
		})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}
		return path, hash
	}
	p1, h1 := build()
	p2, h2 := build()
	if h1 != h2 || p1 != p2 {
		t.Fatalf("identical content produced different artifacts: %q/%q vs %q/%q", p1, h1, p2, h2)
	}
	entries, _ := os.ReadDir(outDir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one published artifact, got %d", len(entries))
	}
}

// Artifact filenames carry plugin identity (name + version from the
// manifest), not a bare hash: plugin-<name>-<version>-<hash16><ext>. Version
// dots stay readable; the content hash tail keeps the name content-addressed.
func TestBuildNativeArtifactNamedByVersion(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, hash, err := BuildNativeArtifact(NativeBuildOptions{
		ProjectID: "01a07badbb2c00000000000000000053", AppName: "Daily Tools", AppVersion: "1.2.3",
		SourceRoot: dir, EntryModule: "main.go", OutDir: outDir,
		GoBuild: func(_, outPath string, _ []string) ([]byte, error) {
			return nil, os.WriteFile(outPath, []byte("artifact"), 0o600)
		},
	})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	base := filepath.Base(path)
	want := "plugin-daily-tools-1.2.3-" + hash[:16] + ".exe"
	if runtime.GOOS != "windows" {
		want = strings.TrimSuffix(want, ".exe")
	}
	if base != want {
		t.Fatalf("artifact name = %q, want %q", base, want)
	}
	if strings.Contains(base, "01a07bad") {
		t.Fatalf("artifact name must not leak the raw project id hash: %q", base)
	}
	if !strings.HasPrefix(base, PluginArtifactSweepPrefix("Daily Tools")) {
		t.Fatalf("artifact name %q must start with the sweep prefix %q", base, PluginArtifactSweepPrefix("Daily Tools"))
	}
}

// Without AppName/AppVersion (direct callers) the filename keeps the legacy
// plugin-<project>-<hash> shape.
func TestBuildNativeArtifactFallbackNaming(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, _, err := BuildNativeArtifact(NativeBuildOptions{
		ProjectID: "test-app", SourceRoot: dir, EntryModule: "main.go", OutDir: outDir,
		GoBuild: func(_, outPath string, _ []string) ([]byte, error) {
			return nil, os.WriteFile(outPath, []byte("artifact"), 0o600)
		},
	})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if base := filepath.Base(path); !strings.HasPrefix(base, "plugin-test-app-") {
		t.Fatalf("fallback artifact name = %q, want plugin-test-app-<hash> prefix", base)
	}
}

func TestBuildNativeArtifactReportsBuildFailure(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.gen.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := BuildNativeArtifact(NativeBuildOptions{
		ProjectID:  "fail-app",
		SourceRoot: dir,
		OutDir:     outDir,
		GoBuild: func(dir, outPath string, env []string) ([]byte, error) {
			return []byte("syntax error"), errors.New("exit code 1")
		},
	})
	if err == nil {
		t.Fatal("expected build failure error")
	}
	if !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("expected diagnostic in error, got %q", err.Error())
	}
}

// TestBuildNativeArtifactTruncatesDiagnostic pins the log/error truncation
// constraint: unbounded compiler output must be capped before it leaves the
// package (it travels cross-actor inside the error text and the response
// Diagnostic field).
func TestBuildNativeArtifactTruncatesDiagnostic(t *testing.T) {
	dir := t.TempDir()
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.gen.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("compiler diagnostic line\n", 200)
	_, _, err := BuildNativeArtifact(NativeBuildOptions{
		ProjectID:  "trunc-app",
		SourceRoot: dir,
		OutDir:     outDir,
		GoBuild: func(dir, outPath string, env []string) ([]byte, error) {
			return []byte(big), errors.New("exit code 1")
		},
	})
	if err == nil {
		t.Fatal("expected build failure error")
	}
	msg := err.Error()
	if !strings.HasSuffix(msg, "...(truncated)") {
		t.Fatalf("diagnostic not truncated: got %d bytes, suffix %q", len(msg), msg[len(msg)-20:])
	}
	const limit = 512
	if len(msg) > limit+len("...(truncated)")+len("native build failed: ") {
		t.Fatalf("error exceeds truncation budget: %d bytes", len(msg))
	}
	if !strings.Contains(msg, big[:100]) {
		t.Fatal("truncated diagnostic lost the leading compiler output")
	}
}

// TestBuildNativeArtifactModeBranches pins the Mode branch behavior:
// subprocess (and the empty default) builds in place with CGO_ENABLED=0 and
// an executable suffix; inprocess stages the source, writes the generic cgo
// shim into the staged directory, and builds with CGO_ENABLED=1 and the
// shared-library suffix. Unknown modes are rejected up front.
func TestBuildNativeArtifactModeBranches(t *testing.T) {
	srcDir := t.TempDir()
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.gen.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	build := func(mode string) (dir string, env []string, shim bool, name string, err error) {
		var shimErr error
		var builtIn string
		var builtEnv []string
		path, _, buildErr := BuildNativeArtifact(NativeBuildOptions{
			ProjectID: "mode-app", SourceRoot: srcDir, OutDir: outDir, Mode: mode,
			GoBuild: func(d, outPath string, e []string) ([]byte, error) {
				builtIn, builtEnv = d, e
				if werr := os.WriteFile(outPath, []byte("mode-artifact"), 0o600); werr != nil {
					return nil, werr
				}
				_, shimErr = os.Stat(filepath.Join(d, cgoShimFileName))
				return nil, nil
			},
		})
		if buildErr != nil {
			return "", nil, false, "", buildErr
		}
		return builtIn, builtEnv, shimErr == nil, filepath.Base(path), nil
	}

	// Empty Mode defaults to subprocess (the dev transport).
	dir, env, shim, name, err := build("")
	if err != nil {
		t.Fatalf("default mode build: %v", err)
	}
	if dir != srcDir || shim {
		t.Fatalf("default mode: dir=%q (want source root), shim=%v (want false)", dir, shim)
	}
	if !hasEnv(env, "CGO_ENABLED=0") {
		t.Fatalf("default mode env = %v, want CGO_ENABLED=0", env)
	}
	if !strings.HasSuffix(name, artifactSuffixForMode(ModeSubprocess)) {
		t.Fatalf("default mode artifact %q: want suffix %q", name, artifactSuffixForMode(ModeSubprocess))
	}

	// Explicit subprocess behaves identically.
	dir, env, shim, name, err = build(ModeSubprocess)
	if err != nil {
		t.Fatalf("subprocess build: %v", err)
	}
	if dir != srcDir || shim {
		t.Fatalf("subprocess mode: dir=%q (want source root), shim=%v (want false)", dir, shim)
	}
	if !hasEnv(env, "CGO_ENABLED=0") {
		t.Fatalf("subprocess env = %v, want CGO_ENABLED=0", env)
	}
	if !strings.HasSuffix(name, artifactSuffixForMode(ModeSubprocess)) {
		t.Fatalf("subprocess artifact %q: want suffix %q", name, artifactSuffixForMode(ModeSubprocess))
	}

	// Inprocess stages the source, writes the shim, and enables cgo; the
	// artifact name carries the shared-library suffix. The staged directory
	// is discarded after the build.
	dir, env, shim, name, err = build(ModeInprocess)
	if err != nil {
		t.Fatalf("inprocess build: %v", err)
	}
	if dir == srcDir || !shim {
		t.Fatalf("inprocess mode: dir=%q (want staged), shim=%v (want true)", dir, shim)
	}
	if !hasEnv(env, "CGO_ENABLED=1") {
		t.Fatalf("inprocess env = %v, want CGO_ENABLED=1", env)
	}
	if !strings.HasSuffix(name, nativeLibrarySuffix()) {
		t.Fatalf("inprocess artifact %q: want suffix %q", name, nativeLibrarySuffix())
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("staged dir %q still exists after the build (stat err %v)", dir, statErr)
	}

	// Unknown modes are rejected with a stable error before any build.
	if _, _, _, _, err := build("sandbox"); err == nil || !strings.Contains(err.Error(), "must be") {
		t.Fatalf("invalid mode error = %v, want rejection", err)
	}
}

// TestBuildNativeArtifactShimIdempotent pins the release-shim determinism
// guarantee: two inprocess builds of the same source write byte-identical
// shim files, so the staged input — and therefore the content-addressed
// artifact — is identical across builds. The shim itself is the generic cgo
// export tail: cgo-tagged, seven //export entries delegating to sdk.Handle*,
// and an empty main.
func TestBuildNativeArtifactShimIdempotent(t *testing.T) {
	srcDir := t.TempDir()
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.gen.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var snaps []string
	build := func() string {
		var captured []byte
		path, _, err := BuildNativeArtifact(NativeBuildOptions{
			ProjectID: "shim-app", SourceRoot: srcDir, OutDir: outDir, Mode: ModeInprocess,
			GoBuild: func(dir, outPath string, env []string) ([]byte, error) {
				captured, _ = os.ReadFile(filepath.Join(dir, cgoShimFileName))
				if werr := os.WriteFile(outPath, []byte("release-artifact"), 0o600); werr != nil {
					return nil, werr
				}
				return nil, nil
			},
		})
		if err != nil {
			t.Fatalf("inprocess build: %v", err)
		}
		snaps = append(snaps, string(captured))
		return path
	}
	p1 := build()
	p2 := build()
	if len(snaps) != 2 {
		t.Fatalf("captured %d shim snapshots, want 2", len(snaps))
	}
	if p1 != p2 {
		t.Fatalf("identical source+s shim produced different artifacts: %q vs %q", p1, p2)
	}
	if snaps[0] != snaps[1] {
		t.Fatalf("shim bytes differ across two builds (same hash expected):\n%s\n---\n%s", snaps[0], snaps[1])
	}
	if snaps[0] != cgoShimSource {
		t.Fatalf("staged shim differs from the cgoShimSource constant:\n%s", snaps[0])
	}
	for _, want := range []string{
		"//go:build cgo",
		"package main",
		`"C"`,
		"unsafe",
		"github.com/qomos-w/sporemind-plugin-sdk",
		"PluginManifest", "PluginOnLoad", "PluginOnUnload", "PluginOnConfigChange", "PluginInvoke", "PluginSetHostBridge", "PluginLog",
		"sdk.WriteManifest", "sdk.HandleOnLoad", "sdk.HandleOnUnload", "sdk.HandleOnConfigChange", "sdk.HandleInvokeFramed", "sdk.HandleSetHostBridge", "sdk.HandlePluginLog",
		"func main() {}",
	} {
		if !strings.Contains(snaps[0], want) {
			t.Errorf("shim missing %q", want)
		}
	}
}

// TestBuildNativeArtifactStageSourceOverride verifies the staging seam:
// StageSource replaces the default staging and is only consulted for
// inprocess builds; the cgo shim is written into the returned directory.
func TestBuildNativeArtifactStageSourceOverride(t *testing.T) {
	srcDir := t.TempDir()
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.gen.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stageCalls int
	staged := t.TempDir()
	artifactPath, _, err := BuildNativeArtifact(NativeBuildOptions{
		ProjectID: "stage-app", SourceRoot: srcDir, OutDir: outDir, Mode: ModeInprocess,
		StageSource: func(src string) (string, error) {
			stageCalls++
			if src != srcDir {
				t.Fatalf("StageSource src = %q, want %q", src, srcDir)
			}
			return staged, nil
		},
		GoBuild: func(dir, outPath string, env []string) ([]byte, error) {
			if dir != staged {
				t.Fatalf("inprocess build dir = %q, want injected %q", dir, staged)
			}
			if _, statErr := os.Stat(filepath.Join(dir, cgoShimFileName)); statErr != nil {
				t.Fatalf("shim not written into injected stage dir: %v", statErr)
			}
			return nil, os.WriteFile(outPath, []byte("staged"), 0o600)
		},
	})
	if err != nil {
		t.Fatalf("inprocess build with injected stage: %v", err)
	}
	if stageCalls != 1 {
		t.Fatalf("StageSource calls = %d, want 1", stageCalls)
	}
	if !strings.HasSuffix(filepath.Base(artifactPath), nativeLibrarySuffix()) {
		t.Fatalf("artifact %q: want shared-library suffix", artifactPath)
	}

	// Subprocess builds never stage, even when a StageSource is configured.
	stageCalls = 0
	if _, _, err := BuildNativeArtifact(NativeBuildOptions{
		ProjectID: "stage-app", SourceRoot: srcDir, OutDir: outDir,
		StageSource: func(src string) (string, error) {
			stageCalls++
			return staged, nil
		},
		GoBuild: func(_, outPath string, _ []string) ([]byte, error) {
			return nil, os.WriteFile(outPath, []byte("staged"), 0o600)
		},
	}); err != nil {
		t.Fatalf("subprocess build with StageSource set: %v", err)
	}
	if stageCalls != 0 {
		t.Fatalf("StageSource called for a subprocess build: %d calls", stageCalls)
	}
}

// TestStageReleaseSourceCopiesAndRewritesReplaces covers the default release
// staging: only *.go and go.mod are copied (nested layout preserved), and
// relative replace targets — inline and block form — become absolute against
// the original source dir, while absolute-path targets and versioned
// module-path targets are left untouched.
func TestStageReleaseSourceCopiesAndRewritesReplaces(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "pkg", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"main.gen.go": "package main\n",
		"go.mod": "module example.com/plug\n" +
			"\n" +
			"require github.com/qomos-w/sporemind-plugin-sdk v0.0.0\n" +
			"\n" +
			"replace (\n" +
			"\tgithub.com/qomos-w/spore => ../../spore\n" +
			"\tgithub.com/qomos-w/sporemind-plugin-sdk => ./vendor-sdk\n" +
			")\n" +
			"\n" +
			"replace example.com/abs => /opt/absolute\n" +
			"replace example.com/ver => example.com/new v1.2.3\n",
		"pkg/sub/helper.go": "package sub\n",
		"skip.txt":          "not copied\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(srcDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	staged, err := stageReleaseSource(srcDir)
	if err != nil {
		t.Fatalf("stageReleaseSource: %v", err)
	}
	defer os.RemoveAll(staged)

	for _, rel := range []string{"main.gen.go", "go.mod", filepath.Join("pkg", "sub", "helper.go")} {
		if _, statErr := os.Stat(filepath.Join(staged, rel)); statErr != nil {
			t.Errorf("staged file %s missing: %v", rel, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(staged, "skip.txt")); !os.IsNotExist(statErr) {
		t.Errorf("non-.go/non-go.mod file was copied (stat err %v)", statErr)
	}

	goMod, err := os.ReadFile(filepath.Join(staged, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(goMod)
	for _, want := range []string{
		"github.com/qomos-w/spore => " + filepath.ToSlash(filepath.Clean(filepath.Join(srcDir, "..", "..", "spore"))),
		"github.com/qomos-w/sporemind-plugin-sdk => " + filepath.ToSlash(filepath.Join(srcDir, "vendor-sdk")),
		"replace example.com/abs => /opt/absolute",
		"replace example.com/ver => example.com/new v1.2.3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("staged go.mod missing %q:\n%s", want, got)
		}
	}
}

// TestBuildNativeArtifactInprocessRealBuild is an end-to-end smoke test of
// the release path against a real toolchain: stage the SDK hello example
// (register-only layout — its hand-written cgo export file removed, exactly
// the post-codegen-split shape) and compile a real c-shared library through
// the production default GoBuild (staging + replace rewrite + generated shim
// + CGO_ENABLED=1 -buildmode=c-shared -trimpath). Skipped in -short mode,
// without a Go toolchain or C compiler, or when the SDK example is not
// checked out.
func TestBuildNativeArtifactInprocessRealBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real release build in short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain not available")
	}
	if !lookPathAny("gcc", "clang", "cc") {
		t.Skip("no C compiler on PATH (cgo is required for c-shared builds)")
	}
	sdkDir, ok := testutil.FindSDKDir(t)
	if !ok {
		t.Skip("sporemind-plugin-sdk checkout not found")
	}
	pluginDir := filepath.Join(sdkDir, "examples", "hello")
	if _, err := os.Stat(filepath.Join(pluginDir, "main.go")); err != nil {
		t.Skipf("SDK example not checked out at %s", pluginDir)
	}

	// Copy the example into a throwaway source dir and drop its hand-written
	// main_cgo.go — the generated shim is the only export carrier in the
	// staged build. The copy's go.mod replaces are rewritten to absolute
	// paths because the temp dir is outside the checkout tree.
	srcDir := t.TempDir()
	for _, name := range []string{"main.go", "main_nocgo.go", "go.mod"} {
		data, err := os.ReadFile(filepath.Join(pluginDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "go.mod" {
			goMod := string(data)
			goMod = strings.ReplaceAll(goMod, "../..", filepath.ToSlash(sdkDir))
			data = []byte(goMod)
		}
		if err := os.WriteFile(filepath.Join(srcDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	outDir := t.TempDir()
	build := func() (string, string) {
		artifactPath, artifactHash, err := BuildNativeArtifact(NativeBuildOptions{
			ProjectID: "hello-smoke", SourceRoot: srcDir, EntryModule: "main.go", OutDir: outDir, Mode: ModeInprocess,
		})
		if err != nil {
			t.Fatalf("real inprocess build: %v", err)
		}
		return artifactPath, artifactHash
	}
	artifactPath, artifactHash := build()
	if !strings.HasSuffix(artifactPath, nativeLibrarySuffix()) {
		t.Fatalf("artifact %q: want shared-library suffix %q", artifactPath, nativeLibrarySuffix())
	}
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if artifactHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("artifact hash != sha256 of artifact bytes")
	}
	if !strings.HasPrefix(filepath.Base(artifactPath), "plugin-hello-smoke-") {
		t.Fatalf("artifact name %q unexpected", filepath.Base(artifactPath))
	}

	// Real-toolchain idempotency. Byte-level: a second build with a fresh
	// staged dir must produce the identical content-addressed artifact — this
	// holds on Linux, where the external ELF linker is deterministic under
	// -trimpath. Windows (mingw PE timestamps + per-link random cookie) and
	// macOS (LC_UUID) c-shared links are not byte-deterministic by design; the
	// content-addressed name still guarantees a rebuild never clobbers the
	// currently-loaded artifact (each new hash lands on a new file), and
	// shim-generation determinism is pinned byte-exactly by
	// TestBuildNativeArtifactShimIdempotent.
	if _, hash2 := build(); hash2 == artifactHash {
		t.Logf("second real build hash identical: %s", artifactHash)
	} else if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Logf("second real build hash %q differs on %s (external-linker nondeterminism, expected)", hash2, runtime.GOOS)
	} else {
		t.Fatalf("second real build hash = %q, want %q (release build not idempotent)", hash2, artifactHash)
	}
}

// lookPathAny reports whether at least one of the named executables is
// available on PATH.
func lookPathAny(names ...string) bool {
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			return true
		}
	}
	return false
}

// stubHost implements ArtifactHost for unit tests. It records all calls so
// tests can assert handler/descriptor/closer installation.
type stubHost struct {
	handlers       map[string]HandlerFunc
	streamHandlers map[string]HandlerStreamFunc
	descs          []PluginDescriptorData
	closers        map[string]func() error
}

func newStubHost() *stubHost {
	return &stubHost{
		handlers:       map[string]HandlerFunc{},
		streamHandlers: map[string]HandlerStreamFunc{},
		closers:        map[string]func() error{},
	}
}

func (s *stubHost) RegisterHandler(callID string, h HandlerFunc) { s.handlers[callID] = h }
func (s *stubHost) RegisterHandlerStream(callID string, h HandlerStreamFunc) {
	if s.streamHandlers == nil {
		s.streamHandlers = map[string]HandlerStreamFunc{}
	}
	s.streamHandlers[callID] = h
}
func (s *stubHost) UnregisterHandler(callID string) {
	delete(s.handlers, callID)
	delete(s.streamHandlers, callID)
}
func (s *stubHost) RegisterDescriptor(desc PluginDescriptorData) { s.descs = append(s.descs, desc) }
func (s *stubHost) RemoveDescriptor(pluginID string)             {}
func (s *stubHost) RegisterCloser(pluginID string, closer func() error) {
	s.closers[pluginID] = closer
}
func (s *stubHost) RemoveCloser(pluginID string) { delete(s.closers, pluginID) }
func (s *stubHost) ReplaceArtifact(oldCallIDs []string, handlers map[string]HandlerFunc, streamHandlers map[string]HandlerStreamFunc, desc PluginDescriptorData, oldCloser, newCloser func() error) error {
	if oldCloser != nil {
		if err := oldCloser(); err != nil {
			return err
		}
	}
	for _, callID := range oldCallIDs {
		delete(s.handlers, callID)
		delete(s.streamHandlers, callID)
	}
	for callID, handler := range handlers {
		s.handlers[callID] = handler
	}
	for callID, handler := range streamHandlers {
		s.streamHandlers[callID] = handler
	}
	s.descs = append(s.descs, desc)
	if newCloser != nil {
		s.closers[desc.ID] = newCloser
	} else {
		delete(s.closers, desc.ID)
	}
	return nil
}

// stubOpener returns canned invoke/closer functions without touching the OS.
type stubOpener struct {
	openedPaths []string
	invokeErr   error
	closeErr    error
	allowedSets []map[string]struct{}
	pluginIDs   []string
}

func (s *stubOpener) Open(abi gen.PluginAbi, artifactPath, entrySymbol, pluginID string, allowed map[string]struct{}, onLoadConfig []byte) (func(ctx context.Context, callable string, request []byte) ([]byte, error), InvokeStreamFunc, func() error, string, error) {
	s.openedPaths = append(s.openedPaths, artifactPath)
	opened := make(map[string]struct{}, len(allowed))
	for k := range allowed {
		opened[k] = struct{}{}
	}
	s.allowedSets = append(s.allowedSets, opened)
	s.pluginIDs = append(s.pluginIDs, pluginID)
	invoke := func(ctx context.Context, callable string, request []byte) ([]byte, error) {
		if s.invokeErr != nil {
			return nil, s.invokeErr
		}
		return append([]byte("resp:"), request...), nil
	}
	closer := func() error { return s.closeErr }
	return invoke, nil, closer, "", nil
}

func makeArtifactFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "plugin.so")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func validLoadReq(artifactPath string) gen.PluginArtifactLoadReq {
	req := validSubprocessLoadReq(artifactPath)
	req.Abi.Isolation = IsolationInProcess
	return req
}

// validSubprocessLoadReq mirrors validLoadReq with the subprocess transport:
// unload semantics (real close, retry, in-flight drain) only apply there.
func validSubprocessLoadReq(artifactPath string) gen.PluginArtifactLoadReq {
	return gen.PluginArtifactLoadReq{
		ArtifactPath: artifactPath,
		Manifest: gen.AppManifest{
			ID: "test.native", Name: "Test", Version: "1.0.0", Runtime: "native",
			Namespace: "native.test", ProtocolVersion: 2,
			Callables: []gen.AppCallableDescriptor{
				{ID: "ping", RequestSchema: "Empty", ResponseSchema: "Empty"},
			},
		},
		Abi: gen.PluginAbi{
			Name: "c-abi", Version: 1, Encoding: BinaryCodecV1,
			InvokeSymbol: "PluginInvoke", ContractVersion: "1",
			Isolation: IsolationSubprocess, TrustClass: TrustFirstParty, Signer: "sporemind.first-party",
		},
	}
}

func TestArtifactLoaderLoadAndInvoke(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	opener := &stubOpener{}
	loader.SetOpener(opener)

	artifactPath := makeArtifactFile(t, "binary")
	resp, err := loader.Load(context.Background(), validLoadReq(artifactPath))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resp.PluginID != "test.native" {
		t.Fatalf("plugin id = %q", resp.PluginID)
	}
	if len(opener.openedPaths) != 1 || opener.openedPaths[0] != artifactPath {
		t.Fatalf("opener not called with artifact path, got %v", opener.openedPaths)
	}
	if len(host.handlers) != 1 {
		t.Fatalf("expected 1 handler installed, got %d", len(host.handlers))
	}
	h, ok := host.handlers[PluginCallID("test.native", "ping")]
	if !ok {
		t.Fatal("expected handler for plugin.test.native.ping")
	}
	out, err := h(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if string(out) != "resp:hello" {
		t.Fatalf("unexpected response: %q", string(out))
	}
}

func TestArtifactLoaderRejectsHashMismatch(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	loader.SetOpener(&stubOpener{})

	artifactPath := makeArtifactFile(t, "binary")
	req := validLoadReq(artifactPath)
	req.ArtifactHash = "deadbeef"
	_, err := loader.Load(context.Background(), req)
	if err == nil {
		t.Fatal("expected hash mismatch error")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestArtifactLoaderUnloadRemovesHandlers(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	loader.SetOpener(&stubOpener{})

	artifactPath := makeArtifactFile(t, "binary")
	if _, err := loader.Load(context.Background(), validLoadReq(artifactPath)); err != nil {
		t.Fatal(err)
	}
	if len(host.handlers) != 1 {
		t.Fatalf("precondition: expected 1 handler, got %d", len(host.handlers))
	}
	resp, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "test.native"})
	if err != nil {
		t.Fatalf("unload: %v", err)
	}
	if resp.Removed != 1 {
		t.Fatalf("removed = %d, want 1", resp.Removed)
	}
	if len(host.handlers) != 0 {
		t.Fatalf("expected 0 handlers after unload, got %d", len(host.handlers))
	}
}

// TestArtifactLoaderUnloadNotLoadedIsIdempotent pins the recovery path for a
// deleted artifact: when no loader record exists (e.g. the artifact file
// vanished before a host restart, so restore could not install it), Unload
// must succeed with Removed=0 instead of erroring. A hard error here wedges
// appmanager's unregister front half and the record can never be cleaned up.
func TestArtifactLoaderUnloadNotLoadedIsIdempotent(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	loader.SetOpener(&stubOpener{})

	resp, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "never.loaded"})
	if err != nil {
		t.Fatalf("unload of not-loaded plugin must be idempotent success, got %v", err)
	}
	if resp.Removed != 0 {
		t.Fatalf("removed = %d, want 0", resp.Removed)
	}
}

// TestArtifactLoaderUnloadPendingInProcess verifies the in-process transport's
// degraded unload: the library is NOT closed (FreeLibrary/dlclose of a live Go
// c-shared library would crash the host), the record stays as UnloadPending,
// handlers are uninstalled, and Load refuses re-entry until a host restart.
func TestArtifactLoaderUnloadPendingInProcess(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	closed := make(chan struct{}, 1)
	loader.SetOpener(ArtifactOpenerFunc(func(abi gen.PluginAbi, _, _, _ string, _ map[string]struct{}, _ []byte) (func(context.Context, string, []byte) ([]byte, error), InvokeStreamFunc, func() error, string, error) {
		invoke := func(context.Context, string, []byte) ([]byte, error) { return []byte("ok"), nil }
		closer := func() error { closed <- struct{}{}; return nil }
		return invoke, nil, closer, "", nil
	}))

	if _, err := loader.Load(context.Background(), validLoadReq(makeArtifactFile(t, "dll"))); err != nil {
		t.Fatal(err)
	}
	resp, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "test.native"})
	if err != nil {
		t.Fatalf("unload: %v", err)
	}
	if resp.Removed != 1 {
		t.Fatalf("removed = %d, want 1", resp.Removed)
	}
	select {
	case <-closed:
		t.Fatal("in-process unload must not close the library")
	default:
	}
	record, ok := loader.Get("test.native")
	if !ok || !record.UnloadPending {
		t.Fatalf("expected retained UnloadPending record, got ok=%v", ok)
	}
	if len(host.handlers) != 0 {
		t.Fatalf("expected 0 handlers after unload-pending, got %d", len(host.handlers))
	}
	if _, err := loader.Load(context.Background(), validLoadReq(makeArtifactFile(t, "dll"))); err == nil {
		t.Fatal("expected Load refusal while unload-pending")
	}
}

func TestArtifactLoaderUnloadFailureKeepsRegistryForRetry(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	opener := &stubOpener{closeErr: fmt.Errorf("injected close failure")}
	loader.SetOpener(opener)

	artifactPath := makeArtifactFile(t, "binary")
	if _, err := loader.Load(context.Background(), validSubprocessLoadReq(artifactPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "test.native"}); err == nil {
		t.Fatal("expected unload failure")
	}
	if _, ok := loader.Get("test.native"); !ok || len(host.handlers) != 1 || len(host.closers) != 1 {
		t.Fatalf("failed unload must retain registry: record=%v handlers=%d closers=%d", ok, len(host.handlers), len(host.closers))
	}
	opener.closeErr = nil
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "test.native"}); err != nil {
		t.Fatalf("retry unload: %v", err)
	}
	if _, ok := loader.Get("test.native"); ok || len(host.handlers) != 0 || len(host.closers) != 0 {
		t.Fatalf("successful retry must clean registry: record=%v handlers=%d closers=%d", ok, len(host.handlers), len(host.closers))
	}
}

type ArtifactOpenerFunc func(abi gen.PluginAbi, path, symbol, pluginID string, allowed map[string]struct{}, onLoadConfig []byte) (func(context.Context, string, []byte) ([]byte, error), InvokeStreamFunc, func() error, string, error)

func (f ArtifactOpenerFunc) Open(abi gen.PluginAbi, path, symbol, pluginID string, allowed map[string]struct{}, onLoadConfig []byte) (func(context.Context, string, []byte) ([]byte, error), InvokeStreamFunc, func() error, string, error) {
	return f(abi, path, symbol, pluginID, allowed, onLoadConfig)
}

type reloadTestOpener struct {
	oldCloseErr     error
	candidateClosed int
}

func (o *reloadTestOpener) Open(abi gen.PluginAbi, path, _, _ string, _ map[string]struct{}, _ []byte) (func(context.Context, string, []byte) ([]byte, error), InvokeStreamFunc, func() error, string, error) {
	version := "old:"
	closer := func() error { return o.oldCloseErr }
	if strings.Contains(path, "candidate") {
		version = "new:"
		closer = func() error { o.candidateClosed++; return nil }
	}
	return func(_ context.Context, _ string, req []byte) ([]byte, error) {
		return append([]byte(version), req...), nil
	}, nil, closer, "", nil
}

func prepareReq(req gen.PluginArtifactLoadReq) gen.PluginArtifactReloadPrepareReq {
	return gen.PluginArtifactReloadPrepareReq{Manifest: req.Manifest, Abi: req.Abi, ArtifactPath: req.ArtifactPath, ArtifactHash: req.ArtifactHash, EntrySymbol: req.EntrySymbol, OnLoadConfig: req.OnLoadConfig}
}

func TestArtifactReloadPrepareAbortKeepsActiveHandler(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	opener := &reloadTestOpener{}
	loader.SetOpener(opener)
	oldReq := validLoadReq(makeArtifactFile(t, "old"))
	if _, err := loader.Load(context.Background(), oldReq); err != nil {
		t.Fatal(err)
	}
	candidatePath := filepath.Join(t.TempDir(), "candidate.so")
	if err := os.WriteFile(candidatePath, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidateReq := oldReq
	candidateReq.ArtifactPath = candidatePath
	candidateReq.Manifest.Version = "2.0.0"
	prepared, err := loader.PrepareReload(context.Background(), prepareReq(candidateReq))
	if err != nil {
		t.Fatal(err)
	}
	h := host.handlers[PluginCallID("test.native", "ping")]
	out, _ := h(context.Background(), []byte("x"))
	if string(out) != "old:x" {
		t.Fatalf("prepare replaced active handler: %q", out)
	}
	if _, err := loader.AbortReload(context.Background(), gen.PluginArtifactReloadAbortReq{Token: prepared.Token}); err != nil {
		t.Fatal(err)
	}
	out, _ = h(context.Background(), []byte("x"))
	if string(out) != "old:x" || opener.candidateClosed != 1 {
		t.Fatalf("abort changed active runtime or leaked candidate: out=%q closed=%d", out, opener.candidateClosed)
	}
}

func TestArtifactReloadCommitSwapsAtomicallyAndIsSingleUse(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	opener := &reloadTestOpener{}
	loader.SetOpener(opener)
	oldReq := validLoadReq(makeArtifactFile(t, "old"))
	if _, err := loader.Load(context.Background(), oldReq); err != nil {
		t.Fatal(err)
	}
	candidatePath := filepath.Join(t.TempDir(), "candidate.so")
	if err := os.WriteFile(candidatePath, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidateReq := oldReq
	candidateReq.ArtifactPath = candidatePath
	candidateReq.Manifest.Version = "2.0.0"
	prepared, err := loader.PrepareReload(context.Background(), prepareReq(candidateReq))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.CommitReload(context.Background(), gen.PluginArtifactReloadCommitReq{Token: prepared.Token}); err != nil {
		t.Fatal(err)
	}
	h := host.handlers[PluginCallID("test.native", "ping")]
	out, _ := h(context.Background(), []byte("x"))
	if string(out) != "new:x" {
		t.Fatalf("commit did not publish candidate: %q", out)
	}
	if rec, _ := loader.Get("test.native"); rec.Manifest.Version != "2.0.0" {
		t.Fatalf("active record = %+v", rec)
	}
	if _, err := loader.CommitReload(context.Background(), gen.PluginArtifactReloadCommitReq{Token: prepared.Token}); err == nil {
		t.Fatal("commit token must be single-use")
	}
}

func TestArtifactReloadCommitCloseFailureKeepsActiveHandler(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	opener := &reloadTestOpener{oldCloseErr: fmt.Errorf("injected close failure")}
	loader.SetOpener(opener)
	oldReq := validLoadReq(makeArtifactFile(t, "old"))
	if _, err := loader.Load(context.Background(), oldReq); err != nil {
		t.Fatal(err)
	}
	candidatePath := filepath.Join(t.TempDir(), "candidate.so")
	if err := os.WriteFile(candidatePath, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidateReq := oldReq
	candidateReq.ArtifactPath = candidatePath
	candidateReq.Manifest.Version = "2.0.0"
	prepared, err := loader.PrepareReload(context.Background(), prepareReq(candidateReq))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.CommitReload(context.Background(), gen.PluginArtifactReloadCommitReq{Token: prepared.Token}); err == nil {
		t.Fatal("expected close failure")
	}
	h := host.handlers[PluginCallID("test.native", "ping")]
	out, _ := h(context.Background(), []byte("x"))
	if string(out) != "old:x" {
		t.Fatalf("failed commit replaced active handler: %q", out)
	}
	if _, err := loader.AbortReload(context.Background(), gen.PluginArtifactReloadAbortReq{Token: prepared.Token}); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactReloadCommitWaitsForActiveInvocation(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	started := make(chan struct{})
	release := make(chan struct{})
	closed := make(chan struct{}, 1)
	loader.SetOpener(ArtifactOpenerFunc(func(abi gen.PluginAbi, path, _, _ string, _ map[string]struct{}, _ []byte) (func(context.Context, string, []byte) ([]byte, error), InvokeStreamFunc, func() error, string, error) {
		if strings.Contains(path, "candidate") {
			return func(context.Context, string, []byte) ([]byte, error) { return []byte("new"), nil }, nil, func() error { return nil }, "", nil
		}
		return func(context.Context, string, []byte) ([]byte, error) {
			close(started)
			<-release
			return []byte("old"), nil
		}, nil, func() error { closed <- struct{}{}; return nil }, "", nil
	}))
	oldReq := validLoadReq(makeArtifactFile(t, "old"))
	if _, err := loader.Load(context.Background(), oldReq); err != nil {
		t.Fatal(err)
	}
	candidatePath := filepath.Join(t.TempDir(), "candidate.so")
	if err := os.WriteFile(candidatePath, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidateReq := oldReq
	candidateReq.ArtifactPath = candidatePath
	prepared, err := loader.PrepareReload(context.Background(), prepareReq(candidateReq))
	if err != nil {
		t.Fatal(err)
	}
	invokeDone := make(chan struct{})
	go func() {
		_, _ = host.handlers[PluginCallID("test.native", "ping")](context.Background(), nil)
		close(invokeDone)
	}()
	<-started
	commitDone := make(chan error, 1)
	go func() {
		_, err := loader.CommitReload(context.Background(), gen.PluginArtifactReloadCommitReq{Token: prepared.Token})
		commitDone <- err
	}()
	select {
	case <-closed:
		t.Fatal("active library closed during invocation")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	<-invokeDone
	if err := <-commitDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	default:
		t.Fatal("old library was not closed after invocation")
	}
}

func TestArtifactLoaderReloadRequiresExplicitUnload(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	loader.SetOpener(&stubOpener{})

	artifactPath := makeArtifactFile(t, "binary")
	if _, err := loader.Load(context.Background(), validLoadReq(artifactPath)); err != nil {
		t.Fatal(err)
	}
	// Second load of same plugin id should fail without an explicit unload.
	if _, err := loader.Load(context.Background(), validLoadReq(artifactPath)); err == nil {
		t.Fatal("expected double-load to fail; caller must unload first")
	}
}

// TestArtifactLoaderIdempotentReloadSameHash verifies that loading the same
// artifact (same path + same hash) returns the existing record instead of
// erroring. This lets dev_gate and register_project safely call artifact_load
// when the plugin is already loaded without needing an explicit unload.
func TestArtifactLoaderIdempotentReloadSameHash(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	loader.SetOpener(&stubOpener{})

	artifactPath := makeArtifactFile(t, "binary")
	data, _ := os.ReadFile(artifactPath)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	req := validLoadReq(artifactPath)
	req.ArtifactHash = hash
	if _, err := loader.Load(context.Background(), req); err != nil {
		t.Fatalf("first load: %v", err)
	}
	// Second load with same path + hash should succeed (idempotent).
	resp, err := loader.Load(context.Background(), req)
	if err != nil {
		t.Fatalf("idempotent reload: %v", err)
	}
	if resp.PluginID != req.Manifest.ID {
		t.Fatalf("plugin id = %q, want %q", resp.PluginID, req.Manifest.ID)
	}
	if resp.ArtifactHash != hash {
		t.Fatalf("artifact hash = %q, want %q", resp.ArtifactHash, hash)
	}
	if resp.Status.PackageHash != "" {
		t.Fatalf("pluginhost status must not set PackageHash; got %q", resp.Status.PackageHash)
	}
}

// TestBuildInvokeHandlerConcurrentInvokes verifies that invokes of different
// plugins run concurrently (not serialized through a global lock). Two
// plugins are loaded, each with an invoke that sleeps 100ms. If the handlers
// were serialized the total time would be ≥200ms; concurrent execution
// should be well under that.
func TestBuildInvokeHandlerConcurrentInvokes(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	loader.SetOpener(ArtifactOpenerFunc(func(abi gen.PluginAbi, _, _, _ string, _ map[string]struct{}, _ []byte) (func(context.Context, string, []byte) ([]byte, error), InvokeStreamFunc, func() error, string, error) {
		invoke := func(ctx context.Context, callable string, request []byte) ([]byte, error) {
			time.Sleep(100 * time.Millisecond)
			return append([]byte("resp:"), request...), nil
		}
		return invoke, nil, func() error { return nil }, "", nil
	}))

	for _, pid := range []string{"plugin.alpha", "plugin.beta"} {
		req := validLoadReq(makeArtifactFile(t, pid))
		req.Manifest.ID = pid
		if _, err := loader.Load(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}

	hAlpha := host.handlers[PluginCallID("plugin.alpha", "ping")]
	hBeta := host.handlers[PluginCallID("plugin.beta", "ping")]
	if hAlpha == nil || hBeta == nil {
		t.Fatal("expected handlers for both plugins")
	}

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = hAlpha(context.Background(), []byte("a")) }()
	go func() { defer wg.Done(); _, _ = hBeta(context.Background(), []byte("b")) }()
	wg.Wait()
	elapsed := time.Since(start)

	// With the old global mutex, elapsed would be ≥200ms. With per-record
	// RWMutex, the two invokes overlap and should complete near 100ms.
	if elapsed >= 190*time.Millisecond {
		t.Fatalf("invokes appear serialized: elapsed=%v (expected <190ms for concurrent execution)", elapsed)
	}
}

// TestBuildInvokeHandlerSamePluginConcurrent verifies that multiple invokes
// targeting the SAME plugin (different callables) also run concurrently
// thanks to the per-record RWMutex (RLock is shared).
func TestBuildInvokeHandlerSamePluginConcurrent(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	loader.SetOpener(ArtifactOpenerFunc(func(abi gen.PluginAbi, _, _, _ string, _ map[string]struct{}, _ []byte) (func(context.Context, string, []byte) ([]byte, error), InvokeStreamFunc, func() error, string, error) {
		invoke := func(ctx context.Context, callable string, request []byte) ([]byte, error) {
			time.Sleep(100 * time.Millisecond)
			return append([]byte("resp:"), callable...), nil
		}
		return invoke, nil, func() error { return nil }, "", nil
	}))

	req := validLoadReq(makeArtifactFile(t, "dual"))
	req.Manifest.ID = "dual"
	req.Manifest.Callables = []gen.AppCallableDescriptor{
		{ID: "a", RequestSchema: "Empty", ResponseSchema: "Empty"},
		{ID: "b", RequestSchema: "Empty", ResponseSchema: "Empty"},
	}
	if _, err := loader.Load(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	hA := host.handlers[PluginCallID("dual", "a")]
	hB := host.handlers[PluginCallID("dual", "b")]
	if hA == nil || hB == nil {
		t.Fatal("expected handlers for both callables")
	}

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = hA(context.Background(), nil) }()
	go func() { defer wg.Done(); _, _ = hB(context.Background(), nil) }()
	wg.Wait()
	elapsed := time.Since(start)

	if elapsed >= 190*time.Millisecond {
		t.Fatalf("same-plugin invokes appear serialized: elapsed=%v (expected <190ms for concurrent execution)", elapsed)
	}
}

// TestUnloadWaitsForInFlightInvoke verifies that Unload drains in-flight
// invokes (waits for the per-record RLock to release) before closing the
// library. This is the use-after-free safety guarantee.
func TestUnloadWaitsForInFlightInvoke(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	started := make(chan struct{})
	release := make(chan struct{})
	closed := make(chan struct{}, 1)
	loader.SetOpener(ArtifactOpenerFunc(func(abi gen.PluginAbi, _, _, _ string, _ map[string]struct{}, _ []byte) (func(context.Context, string, []byte) ([]byte, error), InvokeStreamFunc, func() error, string, error) {
		invoke := func(context.Context, string, []byte) ([]byte, error) {
			close(started)
			<-release
			return []byte("ok"), nil
		}
		closer := func() error { closed <- struct{}{}; return nil }
		return invoke, nil, closer, "", nil
	}))

	if _, err := loader.Load(context.Background(), validSubprocessLoadReq(makeArtifactFile(t, "x"))); err != nil {
		t.Fatal(err)
	}

	h := host.handlers[PluginCallID("test.native", "ping")]
	invokeDone := make(chan struct{})
	go func() {
		_, _ = h(context.Background(), nil)
		close(invokeDone)
	}()
	<-started

	unloadDone := make(chan error, 1)
	go func() {
		_, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "test.native"})
		unloadDone <- err
	}()

	// Unload must block while the invoke is in flight.
	select {
	case <-closed:
		t.Fatal("library closed during in-flight invoke")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	<-invokeDone

	if err := <-unloadDone; err != nil {
		t.Fatalf("unload: %v", err)
	}
	select {
	case <-closed:
	default:
		t.Fatal("library was not closed after invoke drained")
	}
}

// httpAddrOpener is a stubOpener variant that records the OnLoad config it
// was handed and reports a canned HTTP listener address, standing in for the
// subprocess SDK's OnLoad response ({"httpAddr": "<bound>"}).
type httpAddrOpener struct {
	stubOpener
	httpAddr      string
	onLoadConfigs [][]byte
}

func (o *httpAddrOpener) Open(abi gen.PluginAbi, artifactPath, entrySymbol, pluginID string, allowed map[string]struct{}, onLoadConfig []byte) (func(ctx context.Context, callable string, request []byte) ([]byte, error), InvokeStreamFunc, func() error, string, error) {
	o.onLoadConfigs = append(o.onLoadConfigs, append([]byte(nil), onLoadConfig...))
	invoke, stream, closer, _, err := o.stubOpener.Open(abi, artifactPath, entrySymbol, pluginID, allowed, onLoadConfig)
	return invoke, stream, closer, o.httpAddr, err
}

// TestArtifactLoaderLoadReportsHTTPAddr covers the port-report half of the
// T4 contract: the opener's reported listener address lands on the
// ArtifactRecord, in the load response, and (on the idempotent same-artifact
// reload) in the idempotent response too.
func TestArtifactLoaderLoadReportsHTTPAddr(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	opener := &httpAddrOpener{httpAddr: "127.0.0.1:52345"}
	loader.SetOpener(opener)

	artifactPath := makeArtifactFile(t, "binary")
	sum := sha256.Sum256([]byte("binary"))
	req := validSubprocessLoadReq(artifactPath)
	req.ArtifactHash = hex.EncodeToString(sum[:])
	req.OnLoadConfig = []byte(`{"httpAddr":"127.0.0.1:0","sessionSecret":"abc"}`)
	resp, err := loader.Load(context.Background(), req)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resp.HttpAddr != "127.0.0.1:52345" {
		t.Fatalf("load resp HttpAddr = %q, want the opener-reported address", resp.HttpAddr)
	}
	if len(opener.onLoadConfigs) != 1 || string(opener.onLoadConfigs[0]) != string(req.OnLoadConfig) {
		t.Fatalf("opener must receive the load's OnLoadConfig verbatim, got %q", opener.onLoadConfigs)
	}
	record, ok := loader.Get("test.native")
	if !ok || record.HTTPAddr != "127.0.0.1:52345" {
		t.Fatalf("record HTTPAddr = %q (found=%v), want the opener-reported address", record.HTTPAddr, ok)
	}

	// Idempotent same-artifact reload: no respawn, the current address is
	// reported back from the existing record.
	resp2, err := loader.Load(context.Background(), req)
	if err != nil {
		t.Fatalf("idempotent reload: %v", err)
	}
	if resp2.HttpAddr != "127.0.0.1:52345" {
		t.Fatalf("idempotent reload resp HttpAddr = %q, want the record's address", resp2.HttpAddr)
	}
	if len(opener.onLoadConfigs) != 1 {
		t.Fatalf("idempotent reload must not respawn (opener calls = %d)", len(opener.onLoadConfigs))
	}
}

// TestArtifactLoaderIdempotentRespawnsOnSecretDrift pins the cold-start heal:
// loading the SAME artifact (path+hash) while the incoming OnLoadConfig
// carries a different sessionSecret than the live backend was spawned with
// must respawn the process under the incoming config instead of returning the
// existing record. The record/ArtifactLoads pair is persisted by two actors
// non-atomically; a crash between their Saves leaves the appmanager record
// and the live process holding different secrets, and an idempotent no-op
// would keep a listener that 401s every gateway-proxied request.
func TestArtifactLoaderIdempotentRespawnsOnSecretDrift(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	opener := &httpAddrOpener{httpAddr: "127.0.0.1:52345"}
	loader.SetOpener(opener)

	artifactPath := makeArtifactFile(t, "binary")
	req := validSubprocessLoadReq(artifactPath)
	req.ArtifactHash = hex.EncodeToString(sumArtifact(t, artifactPath))
	req.OnLoadConfig = []byte(`{"httpAddr":"127.0.0.1:0","sessionSecret":"s0"}`)
	if _, err := loader.Load(context.Background(), req); err != nil {
		t.Fatalf("first load: %v", err)
	}

	drifted := req
	drifted.OnLoadConfig = []byte(`{"httpAddr":"127.0.0.1:0","sessionSecret":"s1"}`)
	resp, err := loader.Load(context.Background(), drifted)
	if err != nil {
		t.Fatalf("drifted reload: %v", err)
	}
	if resp.PluginID != req.Manifest.ID {
		t.Fatalf("plugin id = %q", resp.PluginID)
	}
	if len(opener.onLoadConfigs) != 2 {
		t.Fatalf("drifted reload must respawn the backend (opener calls = %d)", len(opener.onLoadConfigs))
	}
	if string(opener.onLoadConfigs[1]) != string(drifted.OnLoadConfig) {
		t.Fatalf("respawn must deliver the incoming OnLoadConfig, got %q", opener.onLoadConfigs[1])
	}
	record, ok := loader.Get("test.native")
	if !ok || string(record.OnLoadConfig) != string(drifted.OnLoadConfig) {
		t.Fatalf("record must retain the respawned config, got %q (found=%v)", record.OnLoadConfig, ok)
	}
	if len(host.handlers) != 1 || host.handlers[PluginCallID("test.native", "ping")] == nil {
		t.Fatal("respawn must leave the callable handler installed")
	}

	// Same-secret reload afterwards stays idempotent (no third spawn).
	if _, err := loader.Load(context.Background(), drifted); err != nil {
		t.Fatalf("post-respawn idempotent reload: %v", err)
	}
	if len(opener.onLoadConfigs) != 2 {
		t.Fatalf("same-secret reload must not respawn (opener calls = %d)", len(opener.onLoadConfigs))
	}
}

// TestArtifactLoaderIdempotentWithoutListenerIgnoresConfigDrift: a backend
// without an HTTP listener (FFI/in-process transport) never gates on the
// session secret, so a config drift must NOT trigger a respawn (the mapping
// cannot be replaced while the host lives).
func TestArtifactLoaderIdempotentWithoutListenerIgnoresConfigDrift(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	opener := &stubOpener{}
	loader.SetOpener(opener)

	artifactPath := makeArtifactFile(t, "binary")
	req := validLoadReq(artifactPath) // in-process isolation, no httpAddr
	req.ArtifactHash = hex.EncodeToString(sumArtifact(t, artifactPath))
	req.OnLoadConfig = []byte(`{"httpAddr":"127.0.0.1:0","sessionSecret":"s0"}`)
	if _, err := loader.Load(context.Background(), req); err != nil {
		t.Fatalf("first load: %v", err)
	}
	drifted := req
	drifted.OnLoadConfig = []byte(`{"httpAddr":"127.0.0.1:0","sessionSecret":"s1"}`)
	if _, err := loader.Load(context.Background(), drifted); err != nil {
		t.Fatalf("listenerless drifted reload: %v", err)
	}
	if len(opener.openedPaths) != 1 {
		t.Fatalf("listenerless drift must stay idempotent (opener calls = %d)", len(opener.openedPaths))
	}
}

func sumArtifact(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return sum[:]
}

// TestArtifactReloadCommitReportsCandidateHTTPAddr covers the reload half:
// the candidate's listener address (from its prepare-time spawn) is reported
// in the commit response.
func TestArtifactReloadCommitReportsCandidateHTTPAddr(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	opener := &httpAddrOpener{httpAddr: "127.0.0.1:6001"}
	loader.SetOpener(opener)

	oldReq := validLoadReq(makeArtifactFile(t, "old"))
	if _, err := loader.Load(context.Background(), oldReq); err != nil {
		t.Fatal(err)
	}
	candidatePath := filepath.Join(t.TempDir(), "candidate.so")
	if err := os.WriteFile(candidatePath, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidateReq := validLoadReq(candidatePath)
	candidateReq.Manifest.ID = oldReq.Manifest.ID
	candidateReq.ArtifactHash = ""
	candidateReq.OnLoadConfig = []byte(`{"httpAddr":"127.0.0.1:0"}`)
	prepared, err := loader.PrepareReload(context.Background(), prepareReq(candidateReq))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	committed, err := loader.CommitReload(context.Background(), gen.PluginArtifactReloadCommitReq{Token: prepared.Token})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if committed.HttpAddr != "127.0.0.1:6001" {
		t.Fatalf("commit resp HttpAddr = %q, want the candidate's address", committed.HttpAddr)
	}
	// The candidate spawn must have received the prepare's OnLoadConfig.
	if len(opener.onLoadConfigs) != 2 || string(opener.onLoadConfigs[1]) != string(candidateReq.OnLoadConfig) {
		t.Fatalf("candidate spawn must receive the prepare OnLoadConfig, got %q", opener.onLoadConfigs)
	}
	record, ok := loader.Get(oldReq.Manifest.ID)
	if !ok || record.HTTPAddr != "127.0.0.1:6001" {
		t.Fatalf("record HTTPAddr after commit = %q (found=%v)", record.HTTPAddr, ok)
	}
}
