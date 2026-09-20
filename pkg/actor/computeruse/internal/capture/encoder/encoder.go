// Package encoder converts raw RGBA pixels to JPEG/PNG bytes.
package encoder

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
)

// Encoder encodes a raw RGBA buffer to bytes in some image format.
type Encoder interface {
	// Encode produces image bytes from RGBA pixels and bounds. Quality is
	// 1-100 for lossy formats; ignored for lossless.
	Encode(pixels []byte, bounds image.Rectangle, quality int) ([]byte, error)
	// Format returns the canonical format name ("jpeg", "png", "raw").
	Format() string
}

// New returns an encoder for the named format.
func New(format string) (Encoder, error) {
	switch format {
	case "", "jpeg", "jpg":
		return jpegEncoder{}, nil
	case "png":
		return pngEncoder{}, nil
	case "raw":
		return rawEncoder{}, nil
	default:
		return nil, fmt.Errorf("unsupported encode format: %s", format)
	}
}

type jpegEncoder struct{}

func (jpegEncoder) Format() string { return "jpeg" }
func (jpegEncoder) Encode(pixels []byte, bounds image.Rectangle, quality int) ([]byte, error) {
	if quality < 1 {
		quality = 85
	} else if quality > 100 {
		quality = 100
	}
	img := pixelsToRGBA(pixels, bounds)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type pngEncoder struct{}

func (pngEncoder) Format() string { return "png" }
func (pngEncoder) Encode(pixels []byte, bounds image.Rectangle, _ int) ([]byte, error) {
	img := pixelsToRGBA(pixels, bounds)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type rawEncoder struct{}

func (rawEncoder) Format() string { return "raw" }
func (rawEncoder) Encode(pixels []byte, _ image.Rectangle, _ int) ([]byte, error) {
	out := make([]byte, len(pixels))
	copy(out, pixels)
	return out, nil
}

// pixelsToRGBA wraps a raw RGBA buffer as a stdlib *image.RGBA.
// The output's Rect is zero-anchored to keep stdlib encoders happy.
func pixelsToRGBA(pixels []byte, bounds image.Rectangle) *image.RGBA {
	w := bounds.Dx()
	h := bounds.Dy()
	return &image.RGBA{
		Pix:    pixels,
		Stride: w * 4,
		Rect:   image.Rect(0, 0, w, h),
	}
}
