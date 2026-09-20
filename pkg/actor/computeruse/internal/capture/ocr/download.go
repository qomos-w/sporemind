package ocr

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// HuggingFace repository hosting the go-ocr model files and ONNX runtime
// shared libraries.  Individual files are fetched via the resolve endpoint.
const hfBase = "https://huggingface.co/getcharzp/go-ocr/resolve/main"

// paddleRequiredFiles returns the relative paths (within the HF repo) and
// local filenames for the four files PaddleOCR needs at runtime.
func paddleRequiredFiles() []struct{ remote, local string } {
	var libFile string
	switch runtime.GOOS {
	case "windows":
		libFile = "onnxruntime.dll"
	case "darwin":
		libFile = fmt.Sprintf("onnxruntime_%s.dylib", runtime.GOARCH)
	default:
		libFile = fmt.Sprintf("onnxruntime_%s.so", runtime.GOARCH)
	}

	return []struct{ remote, local string }{
		{"lib/" + libFile, libFile},
		{"paddle_weights/det.onnx", "det.onnx"},
		{"paddle_weights/rec.onnx", "rec.onnx"},
		{"paddle_weights/dict.txt", "dict.txt"},
	}
}

// EnsurePaddleModels verifies that all PaddleOCR files exist in targetDir.
// When force=false and all files are present, it returns "skipped". When
// files are missing (or force=true), missing files are downloaded from the
// HuggingFace repo.  Returns status ("ready", "downloaded", "skipped") and
// the list of verified file paths.
func EnsurePaddleModels(targetDir string, force bool) (status string, files []string, err error) {
	if targetDir == "" {
		targetDir = defaultPaddleDir()
	}

	reqs := paddleRequiredFiles()

	// Build expected local paths.
	localPaths := make([]string, len(reqs))
	for i, r := range reqs {
		localPaths[i] = filepath.Join(targetDir, r.local)
	}

	// Check which files exist.
	allPresent := true
	for _, p := range localPaths {
		if !fileExists(p) {
			allPresent = false
			break
		}
	}

	if allPresent && !force {
		return "skipped", localPaths, nil
	}

	// Ensure target directory exists.
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "failed", nil, fmt.Errorf("paddle setup: mkdir: %w", err)
	}

	// Download missing (or all when force) files.
	downloaded := false
	client := &http.Client{Timeout: 5 * time.Minute}

	for i, r := range reqs {
		localPath := localPaths[i]

		if fileExists(localPath) && !force {
			continue
		}
		// Download to a .tmp file first, then atomically rename.
		url := hfBase + "/" + r.remote
		if err := downloadFile(client, url, localPath); err != nil {
			return "failed", localPaths, fmt.Errorf("paddle setup: download %s: %w", r.remote, err)
		}
		downloaded = true
	}

	if downloaded {
		return "downloaded", localPaths, nil
	}
	return "ready", localPaths, nil
}

// downloadFile fetches url and writes it to destPath via a temp file +
// atomic rename.
func downloadFile(client *http.Client, url, destPath string) error {
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}

	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmpPath, err)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s → %s: %w", tmpPath, destPath, err)
	}

	return nil
}

// DefaultPaddleDir returns the default download target directory: lib/ocr
// relative to the CWD. This matches the second search root in
// paddleSearchRoots.
func DefaultPaddleDir() string {
	return filepath.Join("lib", "ocr")
}

// defaultPaddleDir is an unexported alias for internal use.
func defaultPaddleDir() string {
	return DefaultPaddleDir()
}