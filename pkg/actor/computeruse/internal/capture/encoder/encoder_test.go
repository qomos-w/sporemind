package encoder

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func gradient(w, h int) []byte {
	out := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			out[i+0] = byte(x % 256)
			out[i+1] = byte(y % 256)
			out[i+2] = byte((x + y) % 256)
			out[i+3] = 255
		}
	}
	return out
}

func TestPNGRoundtrip(t *testing.T) {
	w, h := 32, 16
	pix := gradient(w, h)
	enc, err := New("png")
	if err != nil {
		t.Fatal(err)
	}
	data, err := enc.Encode(pix, image.Rect(0, 0, w, h), 0)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// PNG signature.
	want := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	if !bytes.HasPrefix(data, want) {
		t.Fatalf("missing PNG signature: %x", data[:8])
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("decoded dims %v want %dx%d", img.Bounds(), w, h)
	}
	// Spot-check pixel (10, 5).
	r, g, b, _ := img.At(10, 5).RGBA()
	if byte(r>>8) != 10 || byte(g>>8) != 5 || byte(b>>8) != 15 {
		t.Fatalf("pixel (10,5) = %d,%d,%d", r>>8, g>>8, b>>8)
	}
}

func TestJPEGSignature(t *testing.T) {
	w, h := 32, 16
	pix := gradient(w, h)
	enc, err := New("jpeg")
	if err != nil {
		t.Fatal(err)
	}
	data, err := enc.Encode(pix, image.Rect(0, 0, w, h), 85)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// JPEG SOI marker.
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		t.Fatalf("missing JPEG SOI: %x", data[:4])
	}
}

func TestRawPassthrough(t *testing.T) {
	pix := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	enc, err := New("raw")
	if err != nil {
		t.Fatal(err)
	}
	out, err := enc.Encode(pix, image.Rect(0, 0, 1, 2), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, pix) {
		t.Fatalf("raw should be identical, got %v", out)
	}
	// Must be a copy — mutating out shouldn't touch pix.
	out[0] = 99
	if pix[0] == 99 {
		t.Fatal("raw encoder must return copy, not alias")
	}
}

func TestUnknownFormat(t *testing.T) {
	if _, err := New("webp"); err == nil {
		t.Fatal("expected unknown-format error")
	}
}

func TestJPEGQualityClamp(t *testing.T) {
	w, h := 8, 8
	pix := gradient(w, h)
	enc, _ := New("jpeg")
	// quality = 0 → falls back to 85; should not error.
	if _, err := enc.Encode(pix, image.Rect(0, 0, w, h), 0); err != nil {
		t.Fatalf("quality=0: %v", err)
	}
	// quality = 200 → clamped to 100.
	if _, err := enc.Encode(pix, image.Rect(0, 0, w, h), 200); err != nil {
		t.Fatalf("quality=200: %v", err)
	}
}
