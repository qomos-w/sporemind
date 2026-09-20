package ocr

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	goocr "github.com/getcharzp/go-ocr"
)

// SetModelDir registers dir as the preferred PaddleOCR model directory.
// Pass an empty string to clear it.
func SetModelDir(dir string) {
	configuredDirMu.Lock()
	configuredDir = dir
	configuredDirMu.Unlock()
}

// ModelDir returns the preferred model directory registered via SetModelDir,
// or "" when none was configured.
func ModelDir() string {
	configuredDirMu.RLock()
	defer configuredDirMu.RUnlock()
	return configuredDir
}

// paddleSearchRoots returns the directories searched for the onnxruntime
// library and PaddleOCR model files. The first root containing all
// required files wins. Order:
//  1. directory configured via SetModelDir (persisted setup_ocr choice)
//  2. GO_OCR_HOME env var
//  3. ./lib/ocr (relative to CWD, matching go-ocr's ./lib convention)
//  4. ./assets/ocr
//  5. <exe-dir>/lib/ocr (desktop app bundle)
func paddleSearchRoots() []string {
	var roots []string
	if dir := ModelDir(); dir != "" {
		roots = append(roots, dir)
	}
	if env := os.Getenv("GO_OCR_HOME"); env != "" {
		roots = append(roots, env)
	}
	roots = append(roots, "lib/ocr", "assets/ocr")
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Join(filepath.Dir(exe), "lib", "ocr"))
	}
	return roots
}

// paddleLibName returns the platform-appropriate onnxruntime shared
// library filename.
func paddleLibName() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		return "onnxruntime_" + runtime.GOARCH + ".dylib"
	default:
		return "onnxruntime_" + runtime.GOARCH + ".so"
	}
}

// paddleConfig holds resolved paths after a successful probe.
type paddleConfig struct {
	libPath  string
	detModel string
	recModel string
	dictPath string
}

// probePaddle searches the roots for onnxruntime + det.onnx + rec.onnx +
// dict.txt and returns a ready-to-use config.  Returns an error when the
// files are not found (not a fatal condition — callers fall back to
// tesseract).
func probePaddle() (*paddleConfig, error) {
	libName := paddleLibName()
	for _, root := range paddleSearchRoots() {
		libPath := filepath.Join(root, libName)
		detModel := filepath.Join(root, "det.onnx")
		recModel := filepath.Join(root, "rec.onnx")
		dictPath := filepath.Join(root, "dict.txt")
		if !fileExists(libPath) || !fileExists(detModel) || !fileExists(recModel) || !fileExists(dictPath) {
			continue
		}
		return &paddleConfig{
			libPath:  libPath,
			detModel: detModel,
			recModel: recModel,
			dictPath: dictPath,
		}, nil
	}
	return nil, errors.New("paddle ocr model files not found (set GO_OCR_HOME or place files in lib/ocr)")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// paddleEngineInstance returns a lazily-initialised singleton
// PaddleOcrEngine.  When the model files are not found in any search root,
// it auto-downloads them from the Hugging Face go-ocr repository into
// lib/ocr (the default search root) before initialising the engine.  When
// the download also fails, callers fall back to tesseract.
//
// Only a successfully initialised engine is cached: failures are NOT
// latched, so the next call re-probes (files may have been installed via
// setup_ocr in the meantime). The implicit auto-download is attempted at
// most once per process; ResetPaddle re-arms it.
func paddleEngineInstance() (*goocr.PaddleOcrEngine, error) {
	paddleMu.Lock()
	defer paddleMu.Unlock()
	if paddleEngine != nil {
		return paddleEngine, nil
	}
	cfg, err := probePaddle()
	if err != nil {
		// Auto-download model files into the default directory (once).
		downloadOnce.Do(func() {
			dir := defaultPaddleDir()
			_, _, downloadErr = ensurePaddleModelsFn(dir, false)
		})
		if downloadErr != nil {
			return nil, fmt.Errorf("paddle ocr: model files not found and auto-download failed: %w", downloadErr)
		}
		// Re-probe after download.
		cfg, err = probePaddle()
		if err != nil {
			return nil, err
		}
	}
	eng, err := goocr.NewPaddleOcrEngine(goocr.Config{
		OnnxRuntimeLibPath: cfg.libPath,
		DetModelPath:       cfg.detModel,
		RecModelPath:       cfg.recModel,
		DictPath:           cfg.dictPath,
	})
	if err != nil {
		return nil, fmt.Errorf("paddle ocr init: %w", err)
	}
	paddleEngine = eng
	return paddleEngine, nil
}

// ResetPaddle drops the cached engine and re-arms the one-shot implicit
// auto-download, so the next OCR call re-probes from scratch. Called after
// setup_ocr installs model files, and by tests.
func ResetPaddle() {
	paddleMu.Lock()
	defer paddleMu.Unlock()
	paddleEngine = nil
	downloadOnce = sync.Once{}
	downloadErr = nil
}

// PaddleAvailable reports whether the PaddleOCR model files are present in
// one of the search roots. It does not initialise the engine and never
// triggers a download, so it is safe to call from status/capability paths.
func PaddleAvailable() bool {
	_, err := probePaddle()
	return err == nil
}

// TesseractAvailable reports whether the tesseract CLI is on PATH. A
// negative result is not cached: the next probe re-checks.
func TesseractAvailable() bool {
	_, err := tesseractBin()
	return err == nil
}

// runPaddle executes PaddleOCR over the RGBA pixel buffer and translates
// results into the shared Result type.  Returns ErrNotAvailable when the
// model files are not found so callers can fall back to tesseract.
func runPaddle(pixels []byte, width, height int, originBounds image.Rectangle) (Result, error) {
	eng, err := paddleEngineInstance()
	if err != nil {
		return Result{}, err
	}
	if width <= 0 || height <= 0 || len(pixels) < width*height*4 {
		return Result{}, fmt.Errorf("ocr: invalid pixel buffer (%dx%d, %d bytes)", width, height, len(pixels))
	}

	img := &image.RGBA{Pix: pixels, Stride: width * 4, Rect: image.Rect(0, 0, width, height)}
	results, err := eng.RunOCR(img)
	if err != nil {
		return Result{}, fmt.Errorf("paddle ocr: %w", err)
	}

	var textBuilder strings.Builder
	var lines []Line
	ox, oy := originBounds.Min.X, originBounds.Min.Y
	for i, r := range results {
		if r.Text == "" {
			continue
		}
		if i > 0 && len(lines) > 0 {
			textBuilder.WriteByte('\n')
		}
		textBuilder.WriteString(r.Text)
		x1, y1, x2, y2 := r.Box[0], r.Box[1], r.Box[2], r.Box[3]
		lines = append(lines, Line{
			Text:       r.Text,
			Bounds:     image.Rect(x1+ox, y1+oy, x2+ox, y2+oy),
			Confidence: float64(r.Score),
		})
	}

	return Result{
		Text:   textBuilder.String(),
		Lines:  lines,
		Engine: "paddle",
		Bounds: originBounds,
	}, nil
}
