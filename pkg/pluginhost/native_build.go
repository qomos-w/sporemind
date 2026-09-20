package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/util"
)

// Native build modes (NativeBuildOptions.Mode). Empty Mode defaults to
// subprocess — the dev transport: zero-cgo standalone executables. Release
// builds opt into inprocess explicitly to get the generated cgo shim plus the
// c-shared library the in-process purego loader opens.
const (
	// ModeSubprocess compiles a standalone executable: `go build` without
	// -buildmode under CGO_ENABLED=0, so dev trees never need a C toolchain
	// and the SDK's !cgo subprocess entry (RunProcess) is selected. The
	// artifact suffix is ".exe" on Windows and empty elsewhere.
	ModeSubprocess = "subprocess"
	// ModeInprocess compiles a c-shared library (CGO_ENABLED=1,
	// -buildmode=c-shared) from a staged copy of the source plus a generated
	// generic cgo export shim; the purego loader opens it in-process.
	ModeInprocess = "inprocess"
)

// NativeBuildOptions configures the source-to-artifact compiler invocation.
type NativeBuildOptions struct {
	ProjectID   string
	SourceRoot  string // absolute path to the package directory containing entry .go
	EntryModule string // default "main.gen.go"; must be a .go file
	OutDir      string // absolute path; artifact is written here
	TargetOS    string
	TargetArch  string
	// AppName / AppVersion carry the manifest identity for the artifact
	// filename (plugin-<name>-<version>-<hash><ext>); when empty the
	// filename falls back to the ProjectID stem. CompileFromProject fills
	// them from app.manifest.json, so production builds always name by
	// plugin name + version instead of a bare hash.
	AppName    string
	AppVersion string
	// Mode selects the artifact kind: "subprocess" (default when empty, the
	// dev transport) or "inprocess" (release). See ModeSubprocess /
	// ModeInprocess. Unknown values are rejected with a stable error.
	Mode string
	// GoBuild, when non-nil, replaces the default `go build` invocation
	// (dir, outPath, env). For inprocess builds dir is the staged module
	// directory (see StageSource); for subprocess builds it is SourceRoot.
	GoBuild func(dir, outPath string, env []string) ([]byte, error) // overridable for tests
	// StageSource, when non-nil, replaces the default release-source staging
	// used only by inprocess builds: copy every *.go + go.mod of SourceRoot
	// into a fresh temp directory (relative replace targets rewritten to
	// absolute) where the cgo shim is written and the c-shared build runs.
	// It receives SourceRoot and returns the staged module directory, which
	// BuildNativeArtifact removes after the build. Ignored for subprocess.
	StageSource func(srcDir string) (string, error)
}

// resolveBuildMode normalizes NativeBuildOptions.Mode: empty defaults to
// ModeSubprocess (the dev transport), anything else must be one of the two
// declared constants.
func resolveBuildMode(mode string) (string, error) {
	switch mode {
	case "":
		return ModeSubprocess, nil
	case ModeSubprocess, ModeInprocess:
		return mode, nil
	default:
		return "", fmt.Errorf("native build mode %q must be %q or %q", mode, ModeSubprocess, ModeInprocess)
	}
}

// NativeBuildPlatformSupported returns nil if the host can build and load a
// native artifact for the given target. Cross-compilation is rejected because
// the resulting artifact could not be loaded in-process by this host.
func NativeBuildPlatformSupported(targetOS, targetArch string) error {
	if targetOS == "" {
		targetOS = runtime.GOOS
	}
	if targetArch == "" {
		targetArch = runtime.GOARCH
	}
	if targetOS != runtime.GOOS || targetArch != runtime.GOARCH {
		return fmt.Errorf("native build target %s/%s cannot run on host %s/%s; cross-compilation is not supported", targetOS, targetArch, runtime.GOOS, runtime.GOARCH)
	}
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		return nil
	default:
		return fmt.Errorf("native build is not supported on host %s", runtime.GOOS)
	}
}

// BuildNativeArtifact compiles a Go package into a native artifact (a
// standalone executable in subprocess mode, a c-shared library in inprocess
// mode) and returns the absolute artifact path and its sha256 hex digest. The
// function refuses cross-compilation targets and unknown host OSes with a
// stable error, and rejects unknown build modes. Empty Mode defaults to
// subprocess.
func BuildNativeArtifact(opts NativeBuildOptions) (artifactPath, artifactHash string, err error) {
	if err := NativeBuildPlatformSupported(opts.TargetOS, opts.TargetArch); err != nil {
		return "", "", err
	}
	mode, err := resolveBuildMode(opts.Mode)
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(opts.SourceRoot) {
		return "", "", fmt.Errorf("native build source root must be absolute")
	}
	if info, statErr := os.Stat(opts.SourceRoot); statErr != nil || !info.IsDir() {
		return "", "", fmt.Errorf("native build source root not found: %s", opts.SourceRoot)
	}
	if !filepath.IsAbs(opts.OutDir) {
		return "", "", fmt.Errorf("native build out dir must be absolute")
	}
	if mkErr := os.MkdirAll(opts.OutDir, 0o755); mkErr != nil {
		return "", "", fmt.Errorf("native build out dir: %w", mkErr)
	}
	entry := strings.TrimSpace(opts.EntryModule)
	if entry == "" {
		entry = "main.gen.go"
	}
	entryPath := filepath.Join(opts.SourceRoot, entry)
	if _, statErr := os.Stat(entryPath); statErr != nil {
		return "", "", fmt.Errorf("native build entry module %q: %w", entry, statErr)
	}

	// Release (inprocess) builds stage the source into a disposable temp
	// directory and drop in the generic cgo export shim: the dev tree never
	// carries cgo, and the shim is a fixed constant, so identical source
	// produces identical staged input — the content-addressed artifact name
	// below is then naturally idempotent. The staged directory is used only
	// for the compile and discarded afterwards.
	buildDir := opts.SourceRoot
	if mode == ModeInprocess {
		stage := opts.StageSource
		if stage == nil {
			stage = stageReleaseSource
		}
		buildDir, err = stage(opts.SourceRoot)
		if err != nil {
			return "", "", fmt.Errorf("native build stage source: %w", err)
		}
		defer os.RemoveAll(buildDir)
		if err := os.WriteFile(filepath.Join(buildDir, cgoShimFileName), []byte(cgoShimSource), 0o644); err != nil {
			return "", "", fmt.Errorf("native build write cgo shim: %w", err)
		}
	}

	// Artifacts are content-addressed: build into a unique temp name, hash it,
	// then rename to plugin-<name>-<version>-<hash16><ext> (name/version from
	// the manifest, falling back to the ProjectID stem). A rebuild with
	// different content therefore lands on a new file and never clobbers the
	// artifact a loaded plugin is mapped to (on Windows a loaded DLL cannot be
	// removed, and the persisted (path, hash) pair must stay stable across
	// unrelated dev_gate/register rebuilds — the fixed name broke restart
	// restores).
	suffix := artifactSuffixForMode(mode)
	stem := artifactStem(opts)
	tmpPath := filepath.Join(opts.OutDir, fmt.Sprintf("%s-build-%d%s", stem, time.Now().UnixNano(), suffix))
	cgoFlag := "CGO_ENABLED=0"
	if mode == ModeInprocess {
		cgoFlag = "CGO_ENABLED=1"
	}
	env := append(os.Environ(), cgoFlag, "GOWORK=off", "GOFLAGS=-mod=mod")
	goBuild := opts.GoBuild
	if goBuild == nil {
		if _, lookErr := exec.LookPath("go"); lookErr != nil {
			return "", "", fmt.Errorf("native build blocked: Go toolchain is not available on PATH")
		}
		goBuild = func(dir, outPath string, env []string) ([]byte, error) { return defaultGoBuild(mode, dir, outPath, env) }
	}
	out, buildErr := goBuild(buildDir, tmpPath, env)
	if buildErr != nil {
		_ = os.Remove(tmpPath)
		diagnostic := strings.TrimSpace(string(out))
		if diagnostic == "" {
			diagnostic = buildErr.Error()
		}
		// Compiler output is unbounded (hundreds of diagnostics across many
		// files) and travels cross-actor inside the response + error text —
		// cap it before it leaves this package (openai respSnippet lesson).
		diagnostic = truncateBuildOutput(diagnostic)
		return "", "", fmt.Errorf("native build failed: %s", diagnostic)
	}
	data, readErr := os.ReadFile(tmpPath)
	if readErr != nil {
		return "", "", fmt.Errorf("native build artifact read: %w", readErr)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	finalPath := filepath.Join(opts.OutDir, fmt.Sprintf("%s-%s%s", stem, hash[:16], suffix))
	if _, statErr := os.Stat(finalPath); statErr == nil {
		// Same content already materialized (or currently loaded): keep it.
		_ = os.Remove(tmpPath)
		return finalPath, hash, nil
	}
	if renErr := os.Rename(tmpPath, finalPath); renErr != nil {
		return "", "", fmt.Errorf("native build artifact publish: %w", renErr)
	}
	return finalPath, hash, nil
}

// defaultGoBuild is the production `go build` invocation, mode-aware:
// subprocess builds a standalone executable (no -buildmode); inprocess builds
// a c-shared library with -trimpath so release bytes do not depend on the
// (differently-named) staged build directory — on linkers that are
// deterministic (Linux ELF) repeated builds of identical source are
// byte-identical; Windows/macOS c-shared links additionally embed per-link
// external-linker data (PE timestamps/random cookie, LC_UUID), so the
// content-addressed name remains the idempotency guarantee there.
func defaultGoBuild(mode, dir, outPath string, env []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	args := []string{"build"}
	if mode == ModeInprocess {
		args = append(args, "-buildmode=c-shared", "-trimpath")
	}
	args = append(args, "-o", outPath, ".")
	cmd := util.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("go build: timed out after %s", 5*time.Minute)
	}
	return out, err
}

func nativeLibrarySuffix() string {
	switch runtime.GOOS {
	case "windows":
		return ".dll"
	case "darwin":
		return ".dylib"
	default:
		return ".so"
	}
}

// artifactSuffixForMode returns the on-disk extension for an artifact built in
// the given mode: shared-library suffixes (.dll/.so/.dylib) apply only to
// inprocess (release) builds; subprocess (dev) artifacts are executables —
// ".exe" on Windows, no extension elsewhere (which is what selectTransport's
// isSharedLibraryArtifact check distinguishes).
func artifactSuffixForMode(mode string) string {
	if mode == ModeInprocess {
		return nativeLibrarySuffix()
	}
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// cgoShimFileName is the generic cgo export shim written into staged release
// sources by the inprocess build. It is purely generic — no app-specific
// manifest or callable data — so its bytes are a fixed constant: identical
// source + identical shim = identical artifact hash.
const cgoShimFileName = "main_cgo.gen.go"

// cgoShimSource is the release (inprocess) side of the native ABI: the seven
// //export entry points the host's purego loader resolves, delegating to the
// SDK's Handle* functions, plus an empty main so package main links as a
// library. The content mirrors the export tail of the codegen main.gen.go
// template (pkg/codegen/generate.go) — and the SDK's own examples/hello
// main_cgo.go — kept here so dev codegen output stays cgo-free and the shim
// is generated, deterministically, only at release build time. Compiled only
// under cgo (the build tag); dev (subprocess) builds skip it entirely.
const cgoShimSource = `// main_cgo.gen.go — generic cgo export shim, generated at release build time.
// Code generated by the pluginhost release build; DO NOT EDIT.
//go:build cgo

package main

import (
	"C"
	"unsafe"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

//export PluginManifest
func PluginManifest(buf *C.char, n C.int) C.int {
	return C.int(sdk.WriteManifest(unsafe.Pointer(buf), int32(n)))
}

//export PluginOnLoad
func PluginOnLoad(pluginID *C.char, config *C.char) C.int {
	return C.int(sdk.HandleOnLoad(unsafe.Pointer(pluginID), unsafe.Pointer(config)))
}

//export PluginOnUnload
func PluginOnUnload(pluginID *C.char) C.int {
	return C.int(sdk.HandleOnUnload(unsafe.Pointer(pluginID)))
}

//export PluginOnConfigChange
func PluginOnConfigChange(pluginID *C.char, config *C.char) C.int {
	return C.int(sdk.HandleOnConfigChange(unsafe.Pointer(pluginID), unsafe.Pointer(config)))
}

// PluginInvoke is the official length-prefixed native ABI entry point.
// Signature: int PluginInvoke(const uint8_t* req, size_t reqLen,
//
//	uint8_t* resp, size_t respCap, size_t* respLen)
//
//export PluginInvoke
func PluginInvoke(req *C.char, reqLen C.size_t, resp *C.char, respCap C.size_t, respLen *C.size_t) C.int {
	return C.int(sdk.HandleInvokeFramed(unsafe.Pointer(req), uintptr(reqLen), unsafe.Pointer(resp), uintptr(respCap), (*uintptr)(unsafe.Pointer(respLen))))
}

//export PluginSetHostBridge
func PluginSetHostBridge(bridge unsafe.Pointer) C.int {
	return C.int(sdk.HandleSetHostBridge(bridge))
}

//export PluginLog
func PluginLog(buf *C.char, n C.int) C.int {
	return C.int(sdk.HandlePluginLog(unsafe.Pointer(buf), int32(n)))
}

func main() {}
`

// stageReleaseSource copies every *.go and go.mod file of srcDir — preserving
// the relative layout, including nested packages — into a fresh temporary
// directory. Relative replace targets in the copied go.mod are rewritten to
// absolute paths resolved against the ORIGINAL srcDir, so a release build
// from the temp dir resolves the same modules as an in-place build. The
// staged directory holds the c-shared compile and is discarded afterwards.
func stageReleaseSource(srcDir string) (string, error) {
	staged, err := os.MkdirTemp("", "plugin-release-shim-*")
	if err != nil {
		return "", err
	}
	err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if name != "go.mod" && !strings.HasSuffix(name, ".go") {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(staged, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if name == "go.mod" {
			data = rewriteRelativeReplaces(data, srcDir)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		os.RemoveAll(staged)
		return "", err
	}
	return staged, nil
}

// replaceTargetRe matches the target token of either an inline
// "replace <mod> => <target>" line or a "<mod> => <target>" entry inside a
// "replace ( ... )" block. Group 1 is the prefix (replace + module + "=>"),
// group 2 the target token.
var replaceTargetRe = regexp.MustCompile(`^(\s*(?:replace\s+)?[^\s=>]+\s*=>\s*)(\S+)$`)

// rewriteRelativeReplaces makes local replace targets absolute so the staged
// (temp-directory) module resolves them from outside the checkout tree, like
// an in-place build would: a relative target is joined with the ORIGINAL
// srcDir. Absolute-path targets and versioned module-path targets (e.g.
// "replace old => example.com/new v1.2.3") are left untouched — versioned
// targets resolve through the module proxy like any require. require-block
// entries never match (no "=>").
func rewriteRelativeReplaces(goMod []byte, srcDir string) []byte {
	lines := strings.Split(string(goMod), "\n")
	for i, ln := range lines {
		m := replaceTargetRe.FindStringSubmatch(strings.TrimRight(ln, "\r"))
		if m == nil || m[2] == "" {
			continue
		}
		target := m[2]
		if !strings.HasPrefix(target, "./") && !strings.HasPrefix(target, "../") {
			continue
		}
		lines[i] = m[1] + filepath.ToSlash(filepath.Clean(filepath.Join(srcDir, target)))
	}
	return []byte(strings.Join(lines, "\n"))
}

// truncateBuildOutput caps compiler diagnostics at 512 bytes before they are
// embedded in error text / NativeBuildResult.Diagnostic and travel cross-actor.
func truncateBuildOutput(s string) string {
	if len(s) > 512 {
		return s[:512] + "...(truncated)"
	}
	return s
}

func sanitizeFilename(s string) string {
	if s == "" {
		return "app"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

// sanitizeVersion keeps semver readable (dots, prerelease/build segments)
// while confining the result to filename-safe characters.
func sanitizeVersion(s string) string {
	s = strings.TrimSpace(s)
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '+':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

// artifactStem returns the filename stem for a native artifact:
// plugin-<name>[-<version>]. The version segment is omitted when unknown so
// direct BuildNativeArtifact callers (tests) keep the legacy
// plugin-<stem>-<hash> shape.
func artifactStem(opts NativeBuildOptions) string {
	name := opts.AppName
	if name == "" {
		name = opts.ProjectID
	}
	stem := "plugin-" + sanitizeFilename(name)
	if v := sanitizeVersion(opts.AppVersion); v != "" {
		stem += "-" + v
	}
	return stem
}

// PluginArtifactSweepPrefix returns the filename prefix shared by every
// content-addressed artifact of the given app name (all versions, all
// rebuilds). Superseded-artifact GC uses it so a version bump still sweeps
// the previous version's stale binaries. Empty name returns "" (caller
// falls back to deriving the prefix from the keep path).
func PluginArtifactSweepPrefix(appName string) string {
	if appName == "" {
		return ""
	}
	return "plugin-" + sanitizeFilename(appName) + "-"
}

// BuildNativeResult is a convenience struct combining the artifact path/hash
// and the manifest path expected by ValidateNativeBuildContract.
type BuildNativeResult struct {
	ArtifactPath string
	ArtifactHash string
}

// CompileFromProject builds a native plugin from a project source root and
// validates the resulting artifact + manifest. It is the bridge between the
// build pipeline and the validation contract used by registration. When the
// options lack an app name/version it fills them from the manifest so the
// content-addressed artifact filename carries plugin identity
// (plugin-<name>-<version>-<hash>) instead of a bare project hash.
func CompileFromProject(opts NativeBuildOptions, manifestPath string, expectedHash string) (gen.NativeBuildResult, error) {
	if opts.AppName == "" || opts.AppVersion == "" {
		if data, err := os.ReadFile(manifestPath); err == nil {
			var m gen.AppManifest
			if json.Unmarshal(data, &m) == nil {
				if opts.AppName == "" {
					opts.AppName = m.Name
				}
				if opts.AppVersion == "" {
					opts.AppVersion = m.Version
				}
			}
		}
	}
	artifactPath, artifactHash, err := BuildNativeArtifact(opts)
	result := gen.NativeBuildResult{ArtifactPath: artifactPath, ArtifactHash: artifactHash}
	if err != nil {
		result.Diagnostic = err.Error()
		return result, err
	}
	contract := gen.NativeBuildContract{
		Runtime:      NativeRuntime,
		TargetOS:     runtime.GOOS,
		TargetArch:   runtime.GOARCH,
		ArtifactPath: artifactPath,
		ManifestPath: manifestPath,
		ArtifactHash: expectedHash,
	}
	validated, err := ValidateNativeBuildContract(contract)
	if err != nil {
		return validated, err
	}
	return validated, nil
}
