// Package ocr provides text-recognition helpers for the computeruse actor.
// It tries the embedded PaddleOCR engine (go-ocr / ONNX Runtime) first and
// falls back to the tesseract CLI when the PaddleOCR model files are not
// available. When neither engine is reachable, calls return a sentinel
// error that the actor surfaces as "ocr not available".
package ocr

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"strconv"
	"strings"
	"time"

	"os/exec"

	"github.com/qomos-w/sporemind/pkg/util"
)

// execLookPath keeps os/exec imported only for process lookup (not a spawn,
// no window hiding needed); tesseract spawn itself goes through util.Command.
func execLookPath(bin string) (string, error) { return exec.LookPath(bin) }

// Result mirrors capture.OcrResult without taking on that import.
type Result struct {
	Text   string
	Lines  []Line
	Engine string
	Bounds image.Rectangle
}

// Line is one recognised text line.
type Line struct {
	Text       string
	Bounds     image.Rectangle
	Confidence float64
}

// tesseractBin resolves the tesseract CLI. Only a successful lookup is
// cached: a failed probe is re-attempted on the next call so installing
// tesseract after process start does not require a restart.
func tesseractBin() (string, error) {
	probeMu.Lock()
	defer probeMu.Unlock()
	if probeBin != "" {
		return probeBin, nil
	}
	if p, err := execLookPath("tesseract"); err == nil {
		probeBin = p
		return probeBin, nil
	}
	return "", ErrNotAvailable
}

// Run runs OCR over the RGBA pixel buffer. It tries the embedded PaddleOCR
// engine first (go-ocr / ONNX runtime) and falls back to the tesseract CLI
// when the PaddleOCR model files are not available. At least one engine
// must be reachable.
func Run(pixels []byte, width, height int, language string, originBounds image.Rectangle) (Result, error) {
	if width <= 0 || height <= 0 || len(pixels) < width*height*4 {
		return Result{}, fmt.Errorf("ocr: invalid pixel buffer (%dx%d, %d bytes)", width, height, len(pixels))
	}

	// Try PaddleOCR (embedded, no external CLI dependency).
	if res, err := runPaddle(pixels, width, height, originBounds); err == nil {
		return res, nil
	}

	// Fall back to tesseract CLI.
	return runTesseract(pixels, width, height, language, originBounds)
}

// runTesseract encodes the RGBA pixel buffer as PNG, pipes it into
// tesseract, and parses its TSV output into text + per-line bounding boxes.
func runTesseract(pixels []byte, width, height int, language string, originBounds image.Rectangle) (Result, error) {
	bin, err := tesseractBin()
	if err != nil {
		return Result{}, err
	}
	lang := strings.TrimSpace(language)
	if lang == "" {
		lang = "eng"
	}

	img := &image.RGBA{Pix: pixels, Stride: width * 4, Rect: image.Rect(0, 0, width, height)}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return Result{}, fmt.Errorf("ocr: encode png: %w", err)
	}

	// `tesseract - - -l <lang> tsv` reads PNG on stdin and writes TSV on stdout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := util.CommandContext(ctx, bin, "stdin", "stdout", "-l", lang, "tsv")
	cmd.Stdin = &pngBuf
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return Result{}, fmt.Errorf("ocr: tesseract timed out after 30s")
		}
		return Result{}, fmt.Errorf("ocr: tesseract failed: %w (stderr: %s)", err, strings.TrimSpace(errBuf.String()))
	}

	lines := parseTSV(out.String(), originBounds.Min.X, originBounds.Min.Y)
	var text strings.Builder
	for i, l := range lines {
		if i > 0 {
			text.WriteByte('\n')
		}
		text.WriteString(l.Text)
	}
	return Result{
		Text:   text.String(),
		Lines:  lines,
		Engine: "tesseract",
		Bounds: originBounds,
	}, nil
}

// parseTSV consumes tesseract's TSV output. Columns:
//
//	level page block para line word left top width height conf text
//
// We aggregate words into lines (same block + par + line indices) and shift
// each bounding box by (offsetX, offsetY) so callers get screen-relative
// rects when the source was a region capture.
func parseTSV(tsv string, offsetX, offsetY int) []Line {
	var lines []Line
	type accum struct {
		key     string
		words   []string
		left    int
		top     int
		right   int
		bottom  int
		confSum float64
		confN   int
		hasBox  bool
	}
	var cur accum
	flush := func() {
		if !cur.hasBox || len(cur.words) == 0 {
			cur = accum{}
			return
		}
		text := strings.Join(cur.words, " ")
		conf := 0.0
		if cur.confN > 0 {
			conf = cur.confSum / float64(cur.confN) / 100.0
		}
		lines = append(lines, Line{
			Text: text,
			Bounds: image.Rect(
				cur.left+offsetX, cur.top+offsetY,
				cur.right+offsetX, cur.bottom+offsetY,
			),
			Confidence: conf,
		})
		cur = accum{}
	}

	rows := strings.Split(tsv, "\n")
	for i, row := range rows {
		if i == 0 || row == "" {
			continue // header / blank
		}
		fields := strings.Split(row, "\t")
		if len(fields) < 12 {
			continue
		}
		level := fields[0]
		if level != "5" { // 5 = WORD
			// On block/line boundaries, flush.
			if level == "4" { // LINE
				flush()
			}
			continue
		}
		left, _ := strconv.Atoi(fields[6])
		top, _ := strconv.Atoi(fields[7])
		w, _ := strconv.Atoi(fields[8])
		h, _ := strconv.Atoi(fields[9])
		conf, _ := strconv.ParseFloat(fields[10], 64)
		word := strings.TrimSpace(fields[11])
		if word == "" {
			continue
		}
		key := fields[2] + "/" + fields[3] + "/" + fields[4]
		if cur.key != key {
			flush()
			cur.key = key
		}
		cur.words = append(cur.words, word)
		if conf >= 0 {
			cur.confSum += conf
			cur.confN++
		}
		right := left + w
		bottom := top + h
		if !cur.hasBox {
			cur.left, cur.top, cur.right, cur.bottom = left, top, right, bottom
			cur.hasBox = true
		} else {
			if left < cur.left {
				cur.left = left
			}
			if top < cur.top {
				cur.top = top
			}
			if right > cur.right {
				cur.right = right
			}
			if bottom > cur.bottom {
				cur.bottom = bottom
			}
		}
	}
	flush()
	return lines
}
