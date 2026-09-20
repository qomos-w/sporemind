package diff

import (
	"image"
	"testing"
)

// makeFrame builds a width*height*4 RGBA buffer filled with one color.
func makeFrame(w, h int, r, g, b, a byte) []byte {
	out := make([]byte, w*h*4)
	for i := 0; i+3 < len(out); i += 4 {
		out[i+0] = r
		out[i+1] = g
		out[i+2] = b
		out[i+3] = a
	}
	return out
}

func TestCompareIdentical(t *testing.T) {
	w, h := 64, 64
	a := makeFrame(w, h, 100, 100, 100, 255)
	b := makeFrame(w, h, 100, 100, 100, 255)
	d := NewDetector()
	res, err := d.Compare(a, b, w, h)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !res.IsIdentical {
		t.Fatalf("expected IsIdentical=true, got %+v", res)
	}
	if res.ChangedBlocks != 0 {
		t.Fatalf("expected 0 changed blocks, got %d", res.ChangedBlocks)
	}
}

func TestCompareLocalChange(t *testing.T) {
	w, h := 64, 64
	a := makeFrame(w, h, 0, 0, 0, 255)
	b := makeFrame(w, h, 0, 0, 0, 255)
	// Flip one pixel at (40, 40) bright white.
	idx := (40*w + 40) * 4
	b[idx+0] = 255
	b[idx+1] = 255
	b[idx+2] = 255

	d := NewDetector()
	res, err := d.Compare(a, b, w, h)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if res.IsIdentical {
		t.Fatalf("expected change, got IsIdentical")
	}
	if res.ChangedBlocks == 0 {
		t.Fatalf("expected at least one changed block")
	}
	// ChangedBounds should at minimum include (40,40); padding extends it.
	if !(image.Pt(40, 40).In(res.ChangedBounds)) {
		t.Fatalf("changed bounds %v missing pixel (40,40)", res.ChangedBounds)
	}
}

func TestCompareSizeMismatch(t *testing.T) {
	d := NewDetector()
	_, err := d.Compare(make([]byte, 16), make([]byte, 32), 2, 2)
	if err == nil {
		t.Fatal("expected size-mismatch error")
	}
}

func TestExtractRegion(t *testing.T) {
	w, h := 10, 10
	src := makeFrame(w, h, 0, 0, 0, 255)
	// Write a unique color at (5,5).
	idx := (5*w + 5) * 4
	src[idx+0] = 200
	src[idx+1] = 100
	src[idx+2] = 50

	out, err := ExtractRegion(src, w, h, image.Rect(5, 5, 8, 8))
	if err != nil {
		t.Fatalf("ExtractRegion: %v", err)
	}
	if len(out) != 3*3*4 {
		t.Fatalf("len=%d want %d", len(out), 3*3*4)
	}
	// First pixel of dst should be the unique color.
	if out[0] != 200 || out[1] != 100 || out[2] != 50 {
		t.Fatalf("top-left mismatch: %v", out[:4])
	}
}

func TestExtractRegionOutOfBounds(t *testing.T) {
	w, h := 10, 10
	src := makeFrame(w, h, 0, 0, 0, 255)
	if _, err := ExtractRegion(src, w, h, image.Rect(8, 8, 12, 12)); err == nil {
		t.Fatal("expected oob error")
	}
	if _, err := ExtractRegion(src, w, h, image.Rect(0, 0, 0, 0)); err == nil {
		t.Fatal("expected empty-region error")
	}
}

func TestTileCompareKeyframe(t *testing.T) {
	w, h := 128, 128
	curr := makeFrame(w, h, 100, 100, 100, 255)
	// prev is nil/mismatched -> full keyframe.
	tiles := TileCompare(nil, curr, w, h, 64, 30)
	if len(tiles) != 4 {
		t.Fatalf("keyframe expected 4 tiles, got %d", len(tiles))
	}
}

func TestTileCompareLocalChange(t *testing.T) {
	w, h := 128, 128
	a := makeFrame(w, h, 0, 0, 0, 255)
	b := makeFrame(w, h, 0, 0, 0, 255)
	// Flip pixel inside tile (1,1) — i.e. (64..128, 64..128).
	idx := (70*w + 70) * 4
	b[idx+0] = 255
	b[idx+1] = 255
	b[idx+2] = 255
	tiles := TileCompare(a, b, w, h, 64, 30)
	if len(tiles) != 1 {
		t.Fatalf("expected exactly 1 tile, got %d", len(tiles))
	}
	want := image.Rect(64, 64, 128, 128)
	if tiles[0].Rect != want {
		t.Fatalf("tile rect %v want %v", tiles[0].Rect, want)
	}
}

func TestTileCompareEdgeClip(t *testing.T) {
	w, h := 100, 100 // not a multiple of 64
	curr := makeFrame(w, h, 0, 0, 0, 255)
	tiles := TileCompare(nil, curr, w, h, 64, 30)
	if len(tiles) != 4 {
		t.Fatalf("expected 4 tiles, got %d", len(tiles))
	}
	// Last tile should be clipped to 100x100.
	last := tiles[len(tiles)-1].Rect
	if last.Max.X != 100 || last.Max.Y != 100 {
		t.Fatalf("last tile %v should max at (100,100)", last)
	}
}

func TestPixelDiffPerceptual(t *testing.T) {
	// Pure green change should register more than pure blue (weights 587 vs 114).
	a := []byte{0, 0, 0, 255}
	gShift := []byte{0, 30, 0, 255}
	bShift := []byte{0, 0, 30, 255}
	if !pixelDiff(a, gShift, 20) {
		t.Fatal("green shift should exceed threshold 20")
	}
	if pixelDiff(a, bShift, 20) {
		// Blue weight 114 → dampens — should fall below 20² perceptual.
		t.Fatal("blue shift should fall below threshold 20")
	}
}
