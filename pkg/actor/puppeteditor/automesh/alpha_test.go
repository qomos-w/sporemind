package automesh

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"testing"
)

// makeSolidPNG creates an in-memory RGBA PNG of size w×h where every pixel
// has the given (r, g, b, a) value.
func makeSolidPNG(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

// makeCirclePNG builds an RGBA PNG of size w×h with a filled disc of radius
// `r` centered at (cx, cy). Pixels inside the disc have alpha=255, others 0.
func makeCirclePNG(t *testing.T, w, h, cx, cy, r int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rSq := r * r
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy <= rSq {
				img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 0})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeAlphaPNG_EmptyPayload(t *testing.T) {
	_, err := DecodeAlphaPNG(nil)
	if err == nil {
		t.Fatal("expected error on empty payload")
	}
	_, err = DecodeAlphaPNG([]byte{})
	if err == nil {
		t.Fatal("expected error on zero-length payload")
	}
}

func TestDecodeAlphaPNG_GarbagePayload(t *testing.T) {
	_, err := DecodeAlphaPNG([]byte("not a png"))
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestDecodeAlphaPNGStrict_RejectsNonPNG(t *testing.T) {
	// A valid JPEG header but no actual image data. Strict mode requires
	// the PNG decoder; image.Decode would also fail, but we want to confirm
	// the strict path returns an error and not nil.
	_, err := DecodeAlphaPNGStrict([]byte{0xFF, 0xD8, 0xFF, 0xE0})
	if err == nil {
		t.Fatal("expected strict decoder to reject non-PNG payload")
	}
}

func TestDecodeAlphaPNG_SolidRGBA(t *testing.T) {
	payload := makeSolidPNG(t, 4, 3, color.NRGBA{R: 10, G: 20, B: 30, A: 200})
	in, err := DecodeAlphaPNG(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if in.W != 4 || in.H != 3 {
		t.Fatalf("size = %dx%d, want 4x3", in.W, in.H)
	}
	if len(in.Alpha) != 12 {
		t.Fatalf("alpha length = %d, want 12", len(in.Alpha))
	}
	for i, a := range in.Alpha {
		if a != 200 {
			t.Fatalf("alpha[%d] = %d, want 200", i, a)
		}
	}
	if len(in.RGBA) != 48 {
		t.Fatalf("rgba length = %d, want 48", len(in.RGBA))
	}
}

func TestAlphaFromImage_NilSafe(t *testing.T) {
	got := AlphaFromImage(nil)
	if got.W != 0 || got.H != 0 || len(got.Alpha) != 0 || len(got.RGBA) != 0 {
		t.Fatalf("nil image: got %+v, want zero", got)
	}
}

func TestThresholdAlpha_EmptyInput(t *testing.T) {
	got := ThresholdAlpha(AlphaInput{}, 100)
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestThresholdAlpha_DoesNotMutateInput(t *testing.T) {
	in := AlphaInput{W: 2, H: 2, Alpha: []byte{10, 100, 200, 250}}
	snapshot := append([]byte(nil), in.Alpha...)
	_ = ThresholdAlpha(in, 128)
	if !reflect.DeepEqual(in.Alpha, snapshot) {
		t.Fatalf("input was mutated: got %v, want %v", in.Alpha, snapshot)
	}
}

func TestThresholdAlpha_TableDriven(t *testing.T) {
	cases := []struct {
		name      string
		alpha     []byte
		threshold int
		want      []byte
	}{
		{
			name:      "all-zero alpha stays zero",
			alpha:     []byte{0, 0, 0, 0},
			threshold: 15,
			want:      []byte{0, 0, 0, 0},
		},
		{
			name:      "all-opaque alpha becomes 255",
			alpha:     []byte{255, 255, 255, 255},
			threshold: 15,
			want:      []byte{255, 255, 255, 255},
		},
		{
			name:      "boundary: equal to threshold → 255",
			alpha:     []byte{14, 15, 16, 14},
			threshold: 15,
			want:      []byte{0, 255, 255, 0},
		},
		{
			name:      "threshold zero makes everything opaque",
			alpha:     []byte{0, 1, 127, 200},
			threshold: 0,
			want:      []byte{255, 255, 255, 255},
		},
		{
			name:      "threshold 256 makes everything zero",
			alpha:     []byte{0, 1, 127, 200},
			threshold: 256,
			want:      []byte{0, 0, 0, 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := AlphaInput{W: len(tc.alpha), H: 1, Alpha: tc.alpha}
			got := ThresholdAlpha(in, tc.threshold)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestThresholdAlphaInPlace_RejectsShortBuffer(t *testing.T) {
	in := AlphaInput{W: 2, H: 2, Alpha: []byte{10, 20, 30, 40}}
	if got := ThresholdAlphaInPlace(in, 50, make([]byte, 3)); got != nil {
		t.Fatalf("expected nil for short dst, got %v", got)
	}
}

func TestThresholdAlphaInPlace_WritesIntoDst(t *testing.T) {
	in := AlphaInput{W: 1, H: 3, Alpha: []byte{10, 200, 100}}
	dst := make([]byte, 3)
	got := ThresholdAlphaInPlace(in, 128, dst)
	want := []byte{0, 255, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if &got[0] != &dst[0] {
		t.Fatal("expected returned slice to alias dst")
	}
}

func TestAlphaFromAlphaBytes_PreservesBuffer(t *testing.T) {
	buf := []byte{0, 1, 2, 3}
	in := AlphaFromAlphaBytes(2, 2, buf)
	if len(in.Alpha) != 4 || &in.Alpha[0] != &buf[0] {
		t.Fatalf("expected shared buffer, got %p vs %p", &in.Alpha[0], &buf[0])
	}
}

func TestAlphaFromRGBA_ShortBufferYieldsZero(t *testing.T) {
	in := AlphaFromRGBA(2, 2, []byte{0, 0, 0, 0, 1, 1, 1}) // 7 < 16
	if in.W != 2 || in.H != 2 || len(in.Alpha) != 0 {
		t.Fatalf("unexpected zero-state result: %+v", in)
	}
}

func TestAlphaFromRGBA_ExtractsAlpha(t *testing.T) {
	rgba := []byte{
		0, 0, 0, 10,
		1, 2, 3, 100,
		4, 5, 6, 200,
		7, 8, 9, 250,
	}
	in := AlphaFromRGBA(2, 2, rgba)
	want := []byte{10, 100, 200, 250}
	if !reflect.DeepEqual(in.Alpha, want) {
		t.Fatalf("alpha = %v, want %v", in.Alpha, want)
	}
}

func TestAlphaInput_CenterAndValid(t *testing.T) {
	in := AlphaInput{W: 10, H: 6, Alpha: make([]byte, 60)}
	if !in.Valid() {
		t.Fatal("expected valid input")
	}
	if c := in.Center(); c.X != 5 || c.Y != 3 {
		t.Fatalf("center = %+v, want {5,3}", c)
	}
	empty := AlphaInput{W: 0, H: 0}
	if empty.Valid() {
		t.Fatal("zero size should be invalid")
	}
	short := AlphaInput{W: 2, H: 2, Alpha: []byte{1}}
	if short.Valid() {
		t.Fatal("short alpha should be invalid")
	}
}

// TestDecodeAlphaPNG_CircleShape verifies that a PNG with a filled disc decodes
// to the expected W×H size and that the alpha channel is non-uniform.
func TestDecodeAlphaPNG_CircleShape(t *testing.T) {
	payload := makeCirclePNG(t, 16, 16, 8, 8, 4)
	in, err := DecodeAlphaPNG(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if in.W != 16 || in.H != 16 {
		t.Fatalf("size = %dx%d, want 16x16", in.W, in.H)
	}
	inside, outside := 0, 0
	for _, a := range in.Alpha {
		switch {
		case a == 255:
			inside++
		case a == 0:
			outside++
		default:
			t.Fatalf("unexpected alpha value %d (binary mask expected)", a)
		}
	}
	if inside == 0 || outside == 0 {
		t.Fatalf("expected both inside and outside pixels, got in=%d out=%d", inside, outside)
	}
}
