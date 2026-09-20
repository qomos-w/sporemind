package capture

import (
	"bytes"
	"testing"
)

func makePix(w, h int, r, g, b, a byte) []byte {
	out := make([]byte, w*h*4)
	for i := 0; i+3 < len(out); i += 4 {
		out[i+0] = r
		out[i+1] = g
		out[i+2] = b
		out[i+3] = a
	}
	return out
}

func TestResizeDoubles(t *testing.T) {
	src := makePix(2, 2, 50, 100, 150, 255)
	out, err := Resize(src, 2, 2, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4*4*4 {
		t.Fatalf("len=%d want 64", len(out))
	}
	// All pixels were uniform, so all 16 should match.
	for i := 0; i+3 < len(out); i += 4 {
		if out[i+0] != 50 || out[i+1] != 100 || out[i+2] != 150 || out[i+3] != 255 {
			t.Fatalf("pixel @%d = %v", i/4, out[i:i+4])
		}
	}
}

func TestResizeInvalidArgs(t *testing.T) {
	src := makePix(2, 2, 0, 0, 0, 0)
	if _, err := Resize(src, 0, 2, 1, 1); err == nil {
		t.Fatal("expected error for srcW=0")
	}
	if _, err := Resize(src[:4], 2, 2, 1, 1); err == nil {
		t.Fatal("expected size-mismatch error")
	}
}

func TestScaleHalf(t *testing.T) {
	src := makePix(4, 4, 1, 2, 3, 4)
	out, w, h, err := Scale(src, 4, 4, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if w != 2 || h != 2 || len(out) != 2*2*4 {
		t.Fatalf("scale half: w=%d h=%d len=%d", w, h, len(out))
	}
}

func TestScaleFloorClamp(t *testing.T) {
	src := makePix(4, 4, 1, 2, 3, 4)
	// factor=0.1 → 0 floor; should clamp to 1×1.
	_, w, h, err := Scale(src, 4, 4, 0.1)
	if err != nil {
		t.Fatal(err)
	}
	if w != 1 || h != 1 {
		t.Fatalf("expected 1x1 clamp, got %dx%d", w, h)
	}
}

func TestScaleInvalidFactor(t *testing.T) {
	src := makePix(4, 4, 0, 0, 0, 0)
	if _, _, _, err := Scale(src, 4, 4, 0); err == nil {
		t.Fatal("expected zero-factor error")
	}
	if _, _, _, err := Scale(src, 4, 4, -1); err == nil {
		t.Fatal("expected negative-factor error")
	}
}

func TestToGrayscaleLuminance(t *testing.T) {
	// Pure red (255, 0, 0) → 0.299*255 ≈ 76.
	src := []byte{255, 0, 0, 200}
	out := ToGrayscale(src)
	if out[0] != 76 || out[1] != 76 || out[2] != 76 {
		t.Fatalf("red luminance = %v want 76", out[:3])
	}
	if out[3] != 200 {
		t.Fatalf("alpha lost: %d", out[3])
	}
	// Pure green (0, 255, 0) → 0.587*255 ≈ 149.
	src = []byte{0, 255, 0, 255}
	out = ToGrayscale(src)
	if out[0] != 149 {
		t.Fatalf("green luminance = %d want 149", out[0])
	}
	// Pure blue (0, 0, 255) → 0.114*255 ≈ 29.
	src = []byte{0, 0, 255, 255}
	out = ToGrayscale(src)
	if out[0] != 29 {
		t.Fatalf("blue luminance = %d want 29", out[0])
	}
}

func TestToGrayscaleNotInPlace(t *testing.T) {
	src := []byte{255, 0, 0, 255}
	orig := append([]byte{}, src...)
	_ = ToGrayscale(src)
	if !bytes.Equal(src, orig) {
		t.Fatal("ToGrayscale modified its input")
	}
}
