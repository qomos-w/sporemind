package automesh

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

// AlphaInput is the AutoMesh alpha-mask input. It is the Go-side mirror of
// nijigenerate's AlphaInput struct in common.d:10. The Alpha buffer is the
// only channel consulted by findContours and the downstream pipeline.
//
// W and H describe the buffer dimensions in pixels. Alpha is row-major,
// top-down, length W*H. RGBA, when non-nil, is an optional RGBA copy of the
// full image (length W*H*4) used for downstream consumers that need RGB as
// well as alpha.
type AlphaInput struct {
	W, H                int
	Alpha               []byte
	RGBA                []byte // optional, may be nil
	FromProvider        bool
	ProviderWorldCenter [2]float64
}

// Center returns the image center in pixel coordinates, matching
// common.d alphaImageCenter.
func (a AlphaInput) Center() PointF {
	return PointF{float64(a.W) / 2, float64(a.H) / 2}
}

// Valid reports whether the input is usable by the pipeline.
func (a AlphaInput) Valid() bool { return a.W > 0 && a.H > 0 && len(a.Alpha) >= a.W*a.H }

// DecodeAlphaPNG decodes a PNG byte slice and returns an AlphaInput whose
// Alpha buffer holds the source alpha (or, when the image has no alpha
// channel, the luminance for grayscale, or 0xFF for RGB). The full RGBA is
// also retained in RGBA for callers that need it.
func DecodeAlphaPNG(pngBytes []byte) (AlphaInput, error) {
	if len(pngBytes) == 0 {
		return AlphaInput{}, errors.New("automesh: empty PNG payload")
	}
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return AlphaInput{}, fmt.Errorf("automesh: decode PNG: %w", err)
	}
	return AlphaFromImage(img), nil
}

// DecodeAlphaPNGStrict is DecodeAlphaPNG with strict PNG format checking; it
// rejects non-PNG image sources even when image.Decode falls back to other
// decoders registered in the binary.
func DecodeAlphaPNGStrict(pngBytes []byte) (AlphaInput, error) {
	if len(pngBytes) == 0 {
		return AlphaInput{}, errors.New("automesh: empty PNG payload")
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return AlphaInput{}, fmt.Errorf("automesh: decode PNG: %w", err)
	}
	return AlphaFromImage(img), nil
}

// AlphaFromImage extracts the alpha channel from any image.Image, mirroring
// the spirit of common.d getAlphaInput where a non-alpha image still yields a
// usable buffer (luminance for grayscale, opaque for RGB).
func AlphaFromImage(img image.Image) AlphaInput {
	if img == nil {
		return AlphaInput{}
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return AlphaInput{}
	}
	alpha := make([]byte, w*h)
	rgba := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			alpha[y*w+x] = c.A
			off := (y*w + x) * 4
			rgba[off+0] = c.R
			rgba[off+1] = c.G
			rgba[off+2] = c.B
			rgba[off+3] = c.A
		}
	}
	return AlphaInput{
		W:     w,
		H:     h,
		Alpha: alpha,
		RGBA:  rgba,
	}
}

// AlphaFromAlphaBytes builds an AlphaInput from a precomputed alpha buffer.
// The buffer is consumed by reference; callers must not mutate it during the
// pipeline run.
func AlphaFromAlphaBytes(w, h int, alpha []byte) AlphaInput {
	return AlphaInput{W: w, H: h, Alpha: alpha}
}

// AlphaFromRGBA builds an AlphaInput from an RGBA byte buffer (length W*H*4).
// Only the alpha channel is retained.
func AlphaFromRGBA(w, h int, rgba []byte) AlphaInput {
	if len(rgba) < w*h*4 {
		return AlphaInput{W: w, H: h}
	}
	alpha := make([]byte, w*h)
	for i := range alpha {
		alpha[i] = rgba[i*4+3]
	}
	return AlphaInput{W: w, H: h, Alpha: alpha}
}

// ThresholdAlpha returns a fresh binary mask buffer (length W*H, each byte
// either 0 or 255) where every pixel A satisfies:
//
//	A' = 0   if A < threshold
//	A' = 255 otherwise
//
// This is the binarize step performed by ContourAutoMeshProcessor before
// findContours (contours.d:108-111). The input buffer is not mutated.
func ThresholdAlpha(in AlphaInput, threshold int) []byte {
	if !in.Valid() {
		return nil
	}
	if threshold < 0 {
		threshold = 0
	}
	if threshold > 255 {
		threshold = 255
	}
	out := make([]byte, in.W*in.H)
	t := uint8(threshold)
	for i, a := range in.Alpha[:in.W*in.H] {
		if a < t {
			out[i] = 0
		} else {
			out[i] = 255
		}
	}
	return out
}

// ThresholdAlphaInPlace is like ThresholdAlpha but writes into the provided
// dst buffer (which must be at least len(in.Alpha) long). It returns dst so
// callers can chain. Provided for hot paths that want to avoid the
// allocation; tests use ThresholdAlpha instead.
func ThresholdAlphaInPlace(in AlphaInput, threshold int, dst []byte) []byte {
	if !in.Valid() || len(dst) < in.W*in.H {
		return nil
	}
	if threshold < 0 {
		threshold = 0
	}
	if threshold > 255 {
		threshold = 255
	}
	t := uint8(threshold)
	for i, a := range in.Alpha[:in.W*in.H] {
		if a < t {
			dst[i] = 0
		} else {
			dst[i] = 255
		}
	}
	return dst[:in.W*in.H]
}
