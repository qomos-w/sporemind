package codegen

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// VendorSDKDir is the canonical vendored-SDK directory inside a plugin
// directory. When present, the app go.mod points its SDK replace directive at
// ./vendor-sdk and the app builds with zero host-machine dependencies — no
// sporemind checkout, no SDK cache dir. This is what makes a generated app
// portable across machines (user machines have no sporemind repository).
const VendorSDKDir = "vendor-sdk"

// VendoredSDKReplace is the exact go.mod replace target that marks a vendored
// app. ensureReplaceDirective preserves it verbatim.
const VendoredSDKReplace = "./vendor-sdk"

// sdkModuleName is the module path of the plugin SDK.
const sdkModuleName = "github.com/qomos-w/sporemind-plugin-sdk"

// VendorStampFileName records inside vendor-sdk/ the SDKDigest of the SDK
// source the tree was copied from, so drift (host SDK upgraded, vendored copy
// stale) is detectable without re-walking the host SDK.
const VendorStampFileName = "VENDOR_STAMP"

// SDKGenDepsFileName is the manifest of SDK core contract types and their
// schema IDs, read by the vendoring step to record a gen-set summary in the
// VENDOR_STAMP and by CI to verify gen/ is in sync with the host schema
// registry.
const SDKGenDepsFileName = "sdk_gen_deps.json"

// SDKDigest returns a deterministic SHA-256 hex digest of the SDK source
// tree, walking the same exclusion set as VendorSDKIntoAppDir (examples/
// module, dot entries, the stamp file itself) so digest and copy agree.
// Paths are hashed slash-normalized and files are visited in sorted order.
func SDKDigest(sdkSource string) (string, error) {
	if sdkSource == "" {
		return "", fmt.Errorf("codegen: sdk digest: sdkSource is required")
	}
	if _, err := os.Stat(filepath.Join(sdkSource, FileGoMod)); err != nil {
		return "", fmt.Errorf("codegen: sdk digest: %s is not an SDK module (no go.mod): %v", sdkSource, err)
	}
	var rels []string
	contents := make(map[string][]byte)
	err := filepath.WalkDir(sdkSource, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sdkSource, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := filepath.Base(rel)
		if name == "examples" && d.IsDir() {
			return filepath.SkipDir
		}
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if name == VendorStampFileName {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		rels = append(rels, slash)
		contents[slash] = data
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("codegen: sdk digest: walk %s: %w", sdkSource, err)
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(contents[rel])
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// ReadVendorStamp returns the SDK source digest recorded in
// projectDir/vendor-sdk/VENDOR_STAMP, or "" when the tree has none (pre-stamp
// legacy vendor — drift is undecidable). Only the first line (the digest) is
// returned; subsequent lines (gen-set summary) are accessible via
// ReadVendorStampGenSet.
func ReadVendorStamp(projectDir string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, VendorSDKDir, VendorStampFileName))
	if err != nil {
		return ""
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	return strings.TrimSpace(lines[0])
}

// ReadVendorStampGenSet returns the gen-set summary line from the vendor
// stamp, or "" when the stamp has no gen-set line (legacy single-line stamps).
func ReadVendorStampGenSet(projectDir string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, VendorSDKDir, VendorStampFileName))
	if err != nil {
		return ""
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	if len(lines) < 2 {
		return ""
	}
	return strings.TrimSpace(lines[1])
}

// VendorSDKIsStale reports whether the vendored SDK tree drifted from
// sdkSource. A missing stamp returns (false, nil): legacy trees cannot be
// judged and must not fail gates.
func VendorSDKIsStale(projectDir, sdkSource string) (bool, error) {
	stamp := ReadVendorStamp(projectDir)
	if stamp == "" {
		return false, nil
	}
	digest, err := SDKDigest(sdkSource)
	if err != nil {
		return false, err
	}
	return stamp != digest, nil
}

// VendorSDKIntoAppDir copies the SDK module from sdkSource into
// projectDir/vendor-sdk and re-points the app go.mod SDK replace directive at
// ./vendor-sdk. An existing vendor-sdk tree is removed first: it is generated
// material (a copy of the host SDK), not agent-owned source.
//
// Excluded from the copy: the examples/ subdirectory (a nested module used
// only for documentation) and dot-prefixed entries. Everything else — the
// module go.mod, all packages, gen types — is copied so the vendored module
// compiles standalone.
func VendorSDKIntoAppDir(projectDir, sdkSource string) (string, error) {
	if projectDir == "" || sdkSource == "" {
		return "", fmt.Errorf("codegen: vendor sdk: projectDir and sdkSource are required")
	}
	if _, err := os.Stat(filepath.Join(sdkSource, FileGoMod)); err != nil {
		return "", fmt.Errorf("codegen: vendor sdk: %s is not an SDK module (no go.mod): %v", sdkSource, err)
	}

	target := filepath.Join(projectDir, VendorSDKDir)
	if err := os.RemoveAll(target); err != nil {
		return "", fmt.Errorf("codegen: vendor sdk: clean %s: %w", target, err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("codegen: vendor sdk: mkdir %s: %w", target, err)
	}

	err := filepath.WalkDir(sdkSource, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sdkSource, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := filepath.Base(rel)
		// Skip the examples module and dot entries (.git, .gitignore, ...).
		if name == "examples" && d.IsDir() {
			return filepath.SkipDir
		}
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		dst := filepath.Join(target, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		return "", fmt.Errorf("codegen: vendor sdk: copy: %w", err)
	}

	if err := pointGoModAtVendoredSDK(projectDir); err != nil {
		return "", err
	}
	digest, err := SDKDigest(sdkSource)
	if err != nil {
		return "", err
	}
	// Extend the stamp with a gen-set summary so CI can verify the slimmed
	// gen/ directory matches the host's schema registry.
	genSet, _ := genSetSummary(sdkSource)
	stampContent := digest
	if genSet != "" {
		stampContent += "\n" + genSet
	}
	if err := os.WriteFile(filepath.Join(target, VendorStampFileName), []byte(stampContent+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("codegen: vendor sdk: write stamp: %w", err)
	}
	return target, nil
}

// pointGoModAtVendoredSDK rewrites the app go.mod SDK replace directive to
// ./vendor-sdk (appending one when absent). A missing go.mod is not an error —
// ensureGoModContent writes the directive with the vendored path on the next
// generate.
func pointGoModAtVendoredSDK(projectDir string) error {
	goModPath := filepath.Join(projectDir, FileGoMod)
	data, err := os.ReadFile(goModPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("codegen: vendor sdk: read go.mod: %w", err)
	}

	prefix := "replace " + sdkModuleName + " =>"
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		if current := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)); current != VendoredSDKReplace {
			lines[i] = prefix + " " + VendoredSDKReplace
		}
		found = true
		break
	}
	if !found {
		lines = append(lines, "", prefix+" "+VendoredSDKReplace)
	}

	content := strings.Join(lines, "\n")
	if content != string(data) {
		if err := os.WriteFile(goModPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("codegen: vendor sdk: write go.mod: %w", err)
		}
	}
	return nil
}

// GoModUsesVendoredSDK reports whether the app go.mod already points its SDK
// replace at ./vendor-sdk.
func GoModUsesVendoredSDK(projectDir string) bool {
	data, err := os.ReadFile(filepath.Join(projectDir, FileGoMod))
	if err != nil {
		return false
	}
	prefix := "replace " + sdkModuleName + " =>"
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)) == VendoredSDKReplace
		}
	}
	return false
}

// isVendoredSDKPath reports whether sdkPath resolves to the app's own
// vendor-sdk directory (case- and separator-insensitive on Windows).
func isVendoredSDKPath(projectDir, sdkPath string) bool {
	if projectDir == "" || sdkPath == "" {
		return false
	}
	absSDK, err := filepath.Abs(sdkPath)
	if err != nil {
		return false
	}
	absVendor, err := filepath.Abs(filepath.Join(projectDir, VendorSDKDir))
	if err != nil {
		return false
	}
	return filepath.Clean(absSDK) == filepath.Clean(absVendor)
}

// materializeVendoredSDKIfNeeded keeps the vendor-sdk tree in sync with the
// host SDK when the app go.mod is already vendored (replace => ./vendor-sdk).
// This keeps vendored apps portable across machines: the repo carries only
// the relative replace directive, and any host running dev_generate
// re-materializes the SDK copy from its own resolved SDK (dev checkout or
// release-extracted zip). Since vendor-sdk carries a content stamp, a stale
// copy (host SDK upgraded after the last vendor) is refreshed too — this is
// the documented "regeneration refreshes the vendored copy" behavior; legacy
// pre-stamp trees are only restored when missing, never overwritten.
func materializeVendoredSDKIfNeeded(projectDir, sdkSource string) error {
	if !GoModUsesVendoredSDK(projectDir) {
		return nil
	}
	if isVendoredSDKPath(projectDir, sdkSource) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(projectDir, VendorSDKDir, FileGoMod)); err == nil {
		stale, serr := VendorSDKIsStale(projectDir, sdkSource)
		if serr != nil {
			return serr
		}
		if !stale {
			return nil
		}
	}
	_, err := VendorSDKIntoAppDir(projectDir, sdkSource)
	return err
}

// genSetSummary reads sdk_gen_deps.json from the SDK source and returns a
// one-line summary: the gen file names and a SHA-256 of the deps file
// content. Empty when the deps file is absent (legacy SDK without the
// slimmed gen/ directory).
func genSetSummary(sdkSource string) (string, error) {
	depsPath := filepath.Join(sdkSource, SDKGenDepsFileName)
	data, err := os.ReadFile(depsPath)
	if err != nil {
		return "", nil //nolint:nilerr // legacy SDK without deps file
	}
	h := sha256.New()
	h.Write(data)
	depsHash := fmt.Sprintf("%x", h.Sum(nil))
	// Parse gen_files from the deps JSON to list them in the summary.
	var deps struct {
		GenFiles []string `json:"gen_files"`
	}
	files := "unknown"
	if jsonErr := json.Unmarshal(data, &deps); jsonErr == nil && len(deps.GenFiles) > 0 {
		files = strings.Join(deps.GenFiles, ",")
	}
	return fmt.Sprintf("gen-set:%s:deps=%s", files, depsHash[:16]), nil
}
