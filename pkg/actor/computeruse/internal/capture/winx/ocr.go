//go:build windows

package winx

import (
	"image"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/ocr"
)

// OCR runs the embedded PaddleOCR engine over the provided RGBA buffer,
// falling back to tesseract when PaddleOCR model files are not available.
func (c *Capturer) OCR(pixels []byte, width, height int, language string) (capture.OcrResult, error) {
	res, err := ocr.Run(pixels, width, height, language, image.Rect(0, 0, width, height))
	if err != nil {
		return capture.OcrResult{}, err
	}
	out := capture.OcrResult{
		Text:   res.Text,
		Engine: res.Engine,
		Bounds: res.Bounds,
	}
	for _, l := range res.Lines {
		out.Lines = append(out.Lines, capture.OcrLine{
			Text:       l.Text,
			Bounds:     l.Bounds,
			Confidence: l.Confidence,
		})
	}
	return out, nil
}
