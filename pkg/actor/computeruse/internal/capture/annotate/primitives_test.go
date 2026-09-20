package annotate

import (
	"image"
	"image/color"
	"testing"
)

// TestDrawLineBlendsSemiTransparent verifies that a semi-transparent line is
// alpha-blended over the existing background instead of replacing the pixel
// (which is what image.RGBA.Set would do).
func TestDrawLineBlendsSemiTransparent(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	FillRect(img, img.Bounds(), color.RGBA{R: 255, G: 255, B: 255, A: 255})

	// Semi-transparent red line, A=128. Over white background the blended
	// result is (255, 127, 127): R stays 255, G/B are pulled halfway toward
	// the line color. A plain Set would have left (128, 0, 0) instead.
	DrawLine(img, image.Pt(0, 10), image.Pt(19, 10), color.RGBA{R: 255, G: 0, B: 0, A: 128})

	got := img.RGBAAt(10, 10)
	want := color.RGBA{R: 255, G: 127, B: 127, A: 255}
	if got != want {
		t.Fatalf("line pixel = %v, want blended %v (pure overwrite would be {128 0 0 128})", got, want)
	}

	// Pixel off the line must be untouched.
	off := img.RGBAAt(10, 0)
	if off != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("off-line pixel = %v, want untouched white background", off)
	}
}

// TestDrawLineOpaqueMatchesSet verifies that for A=255 colors blendPixel is
// equivalent to a plain Set, so behavior for opaque annotations is unchanged.
func TestDrawLineOpaqueMatchesSet(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	FillRect(img, img.Bounds(), color.RGBA{R: 255, G: 255, B: 255, A: 255})

	col := color.RGBA{R: 0, G: 0, B: 255, A: 255}
	DrawLine(img, image.Pt(0, 5), image.Pt(19, 5), col)

	got := img.RGBAAt(5, 5)
	if got != col {
		t.Fatalf("opaque line pixel = %v, want exact source color %v", got, col)
	}
}

// TestFillRectBlendsSemiTransparent verifies the fill path also blends rather
// than overwriting the background.
func TestFillRectBlendsSemiTransparent(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	FillRect(img, img.Bounds(), color.RGBA{R: 0, G: 0, B: 0, A: 255})

	// 50%-alpha white over black background: (128, 128, 128).
	FillRect(img, image.Rect(2, 2, 8, 8), color.RGBA{R: 255, G: 255, B: 255, A: 128})

	got := img.RGBAAt(5, 5)
	want := color.RGBA{R: 128, G: 128, B: 128, A: 255}
	if got != want {
		t.Fatalf("fill pixel = %v, want blended %v", got, want)
	}

	// Outside the fill region the background must be untouched.
	if outside := img.RGBAAt(0, 0); outside != (color.RGBA{R: 0, G: 0, B: 0, A: 255}) {
		t.Fatalf("outside fill pixel = %v, want untouched black background", outside)
	}
}