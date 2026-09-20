package ocr

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// resetTesseractProbe clears the cached tesseract path for tests.
func resetTesseractProbe() {
	probeMu.Lock()
	probeBin = ""
	probeMu.Unlock()
}

func TestTesseractBin_FailureNotLatched(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	resetTesseractProbe()
	t.Cleanup(resetTesseractProbe)

	if _, err := tesseractBin(); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("first probe: err=%v, want ErrNotAvailable", err)
	}

	// "Install" tesseract after the failed probe: the next call must
	// re-probe and find it instead of returning the latched failure.
	name := "tesseract"
	if runtime.GOOS == "windows" {
		name = "tesseract.exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o755); err != nil {
		t.Fatal(err)
	}
	bin, err := tesseractBin()
	if err != nil {
		t.Fatalf("second probe after install: %v", err)
	}
	if bin == "" {
		t.Fatal("second probe returned empty path")
	}
}

func TestPaddleEngine_FailureNotLatched(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GO_OCR_HOME", dir)
	SetModelDir("")
	ResetPaddle()
	t.Cleanup(ResetPaddle)

	orig := ensurePaddleModelsFn
	t.Cleanup(func() { ensurePaddleModelsFn = orig })

	// Phase 1: no model files, download fails → error.
	ensurePaddleModelsFn = func(string, bool) (string, []string, error) {
		return "", nil, errors.New("network down")
	}
	if _, err := paddleEngineInstance(); err == nil || !strings.Contains(err.Error(), "auto-download failed") {
		t.Fatalf("first call: err=%v, want auto-download failure", err)
	}

	// Phase 2: simulate setup_ocr installing the model files, then reset.
	// The next call must re-probe (finding the files) and fail later at
	// engine init on the fake files — NOT return the latched phase-1 error.
	ResetPaddle()
	ensurePaddleModelsFn = EnsurePaddleModels
	for _, name := range []string{paddleLibName(), "det.onnx", "rec.onnx", "dict.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := paddleEngineInstance()
	if err == nil {
		t.Fatal("second call: expected engine init failure on fake model files")
	}
	if !strings.Contains(err.Error(), "paddle ocr init") {
		t.Fatalf("second call: err=%v, want engine init error (proves re-probe happened)", err)
	}
}

func TestPaddleAvailable_ReflectsModelFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GO_OCR_HOME", dir)
	SetModelDir("")
	t.Cleanup(func() { SetModelDir("") })

	if PaddleAvailable() {
		t.Fatal("PaddleAvailable=true with no model files")
	}
	for _, name := range []string{paddleLibName(), "det.onnx", "rec.onnx", "dict.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !PaddleAvailable() {
		t.Fatal("PaddleAvailable=false with all model files present")
	}
}

func TestSetModelDir_TakesPrecedence(t *testing.T) {
	preferred := t.TempDir()
	t.Setenv("GO_OCR_HOME", t.TempDir())
	SetModelDir(preferred)
	t.Cleanup(func() { SetModelDir("") })

	roots := paddleSearchRoots()
	if len(roots) == 0 || roots[0] != preferred {
		t.Fatalf("first search root = %v, want %q", roots, preferred)
	}
}
