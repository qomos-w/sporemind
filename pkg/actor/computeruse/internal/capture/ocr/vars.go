package ocr

import (
	"errors"
	"sync"

	goocr "github.com/getcharzp/go-ocr"
)

// --- configuration ---

// configuredDir is an optional model directory chosen at runtime (e.g. via
// computeruse.setup_ocr with a custom Dir, restored from persisted state on
// actor start). It takes precedence over every other search root.
//
// TODO(actor-ownership): migrate to actor-owned state.
var (
	configuredDirMu sync.RWMutex
	configuredDir   string
)

// --- Paddle engine ---

// paddleEngine is the lazily initialized PaddleOCR engine.
//
// TODO(actor-ownership): migrate to actor-owned state.
var (
	paddleMu     sync.Mutex
	paddleEngine *goocr.PaddleOcrEngine
	// downloadOnce limits the implicit auto-download to one attempt per
	// process; it is re-armed by ResetPaddle (e.g. after setup_ocr ran).
	downloadOnce sync.Once
	downloadErr  error
)

// ensurePaddleModelsFn is the model-download entry point. It is a package
// variable so tests can stub out the network fetch.
//
// TODO(actor-ownership): migrate to actor-owned state.
var ensurePaddleModelsFn = EnsurePaddleModels

// --- Tesseract engine ---

// ErrNotAvailable signals that no OCR engine is reachable.
var ErrNotAvailable = errors.New("ocr engine not available (install tesseract)")

// tesseract probe cache.
//
// TODO(actor-ownership): migrate to actor-owned state.
var (
	probeMu  sync.Mutex
	probeBin string
)
