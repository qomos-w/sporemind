package codegen

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnsureDevSDKWorkspace resolves the SDK directory path. In development mode
// (sporemind repo checkout), it finds the SDK directory by walking up from the
// project. In release mode, it extracts the embedded SDK zip to a user-level
// cache directory on first use. Returns (sdkPath, error).
//
// This function is idempotent: if the SDK is already resolved/extracted, it
// returns immediately. It is called by generate, gate, and register build
// paths as an inline guarantee (not an agent step).
func EnsureDevSDKWorkspace(projectDir string) (string, error) {
	// Development mode: find the SDK relative to the project directory.
	if sdkPath := findDevSDK(projectDir); sdkPath != "" {
		return sdkPath, nil
	}

	// Release mode: extract embedded SDK to user-level cache. The cache is
	// fingerprinted by the embedded zip's sha256: a newer host binary whose
	// embedded SDK changed re-extracts over the stale copy instead of
	// silently serving an old SDK to every scaffolded app.
	cacheDir, err := userSDKCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve SDK cache dir: %w", err)
	}

	zipFP := sdkCacheFingerprint()
	markerPath := filepath.Join(cacheDir, "sdk-zip.sha256")
	marker, markerErr := os.ReadFile(markerPath)
	cacheExtracted := false
	if _, err := os.Stat(filepath.Join(cacheDir, "go.mod")); err == nil {
		cacheExtracted = true
	}

	switch {
	case cacheExtracted && markerErr == nil && string(marker) == zipFP:
		// Cache is fresh for this binary's embedded SDK.
		return cacheDir, nil
	case cacheExtracted && len(embeddedSDKZip) == 0:
		// Dev/test build (no embedded zip) that fell through to the cache:
		// dev resolution should have found a checkout; reusing an existing
		// extraction is the last resort rather than failing hard.
		return cacheDir, nil
	}

	// Extract (or re-extract over a stale fingerprint mismatch) the
	// embedded SDK zip.
	if len(embeddedSDKZip) == 0 {
		return "", fmt.Errorf("sporemind-plugin-sdk not found; not in dev mode and no embedded SDK available")
	}

	if err := extractEmbeddedSDK(cacheDir); err != nil {
		return "", fmt.Errorf("extract embedded SDK: %w", err)
	}
	if werr := os.WriteFile(markerPath, []byte(zipFP), 0o644); werr != nil {
		return "", fmt.Errorf("write SDK cache fingerprint: %w", werr)
	}

	return cacheDir, nil
}

// sdkCacheFingerprint returns the sha256 hex of the embedded SDK zip — the
// cache freshness marker. Empty-zip builds fingerprint as "" and never match
// a real extraction marker.
func sdkCacheFingerprint() string {
	if len(embeddedSDKZip) == 0 {
		return ""
	}
	h := sha256.Sum256(embeddedSDKZip)
	return hex.EncodeToString(h[:])
}

// findDevSDK locates the sporemind-plugin-sdk directory for dev builds. Three
// anchors are probed in order: the project directory (repo-internal projects
// share the checkout root), the process working directory (go test runs with
// cwd = package dir, and the test binary itself lives in the go-build cache,
// so only the cwd anchor finds the checkout there), and the running
// executable (dev builds run from inside the checkout, so external projects
// — e.g. a sibling workspace like sporecloud — resolve to the repo SDK too).
// Release builds ship an embedded SDK zip and never reach the cache fallback.
func findDevSDK(projectDir string) string {
	if sdkPath := walkUpForSDK(projectDir); sdkPath != "" {
		return sdkPath
	}
	if cwd, err := os.Getwd(); err == nil {
		if sdkPath := walkUpForSDK(cwd); sdkPath != "" {
			return sdkPath
		}
	}
	if exe, err := os.Executable(); err == nil {
		if sdkPath := walkUpForSDK(filepath.Dir(exe)); sdkPath != "" {
			return sdkPath
		}
	}
	return ""
}

// walkUpForSDK walks up from startDir (max 10 levels) looking for a
// sporemind-plugin-sdk directory with a go.mod.
func walkUpForSDK(startDir string) string {
	absDir, _ := filepath.Abs(startDir)
	dir := absDir
	for i := 0; i < 10; i++ {
		candidate := filepath.Join(dir, "sporemind-plugin-sdk")
		goMod := filepath.Join(candidate, "go.mod")
		if _, err := os.Stat(goMod); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// userSDKCacheDir returns the user-level cache directory for the SDK.
func userSDKCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	var cacheDir string
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("LOCALAPPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Local")
		}
		cacheDir = filepath.Join(appData, "sporemind", "sdk")
	case "darwin":
		cacheDir = filepath.Join(home, "Library", "Application Support", "sporemind", "sdk")
	default:
		cacheDir = filepath.Join(home, ".local", "share", "sporemind", "sdk")
	}
	return cacheDir, nil
}

// extractEmbeddedSDK extracts the embedded SDK zip to the target directory.
func extractEmbeddedSDK(targetDir string) error {
	return extractEmbeddedSDKBytes(embeddedSDKZip, targetDir)
}

// extractEmbeddedSDKBytes verifies (when a checksum is compiled in) and
// extracts a packed SDK zip into targetDir. Zip-slip is rejected.
func extractEmbeddedSDKBytes(zipBytes []byte, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	// Verify checksum if set.
	if embeddedSDKChecksum != "" {
		h := sha256.Sum256(zipBytes)
		actual := hex.EncodeToString(h[:])
		if actual != embeddedSDKChecksum {
			// Clean up partial extraction.
			os.RemoveAll(targetDir)
			return fmt.Errorf("SDK checksum mismatch: expected %s, got %s", embeddedSDKChecksum, actual)
		}
	}

	// Open zip archive.
	r, err := zip.NewReader(strings.NewReader(string(zipBytes)), int64(len(zipBytes)))
	if err != nil {
		return fmt.Errorf("open SDK zip: %w", err)
	}

	// Extract all files.
	for _, f := range r.File {
		fpath := filepath.Join(targetDir, f.Name)

		// Prevent zip slip.
		if !strings.HasPrefix(filepath.Clean(fpath), filepath.Clean(targetDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in SDK zip: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, 0o755)
			continue
		}

		// Ensure parent directory exists.
		if err := os.MkdirAll(filepath.Dir(fpath), 0o755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return err
		}
	}

	return nil
}
