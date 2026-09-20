package desktop

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"testing"
)

// buildDIB constructs a CF_DIB payload for decodeDIBToPNG tests. Header is
// always the 40-byte V1 form; pixel rows are given top-to-bottom in visual
// order and written top-down or bottom-up per the topDown flag, with 4-byte
// row padding like real producers emit.
func buildDIB(t *testing.T, w, h, bpp int, compression uint32, topDown bool, rows [][]byte, extraMasks []byte) []byte {
	t.Helper()
	rowBytes := w * bpp / 8
	padded := (rowBytes + 3) &^ 3
	total := 40 + len(extraMasks) + padded*h
	dib := make([]byte, total)
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], uint32(w))
	hdr := uint32(h)
	if topDown {
		hdr = uint32(-int32(h))
	}
	binary.LittleEndian.PutUint32(dib[8:12], hdr)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], uint16(bpp))
	binary.LittleEndian.PutUint32(dib[16:20], compression)
	copy(dib[40:], extraMasks)
	pix := dib[40+len(extraMasks):]
	for row := 0; row < h; row++ {
		src := row
		if !topDown {
			src = h - 1 - row
		}
		copy(pix[src*padded:], rows[row])
	}
	return dib
}

func decodeToNRGBA(t *testing.T, dib []byte) *image.NRGBA {
	t.Helper()
	pngBytes, err := decodeDIBToPNG(dib)
	if err != nil {
		t.Fatalf("decodeDIBToPNG: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if n, ok := img.(*image.NRGBA); ok {
		return n
	}
	// png.Decode hands opaque images back as *image.RGBA; normalize.
	out := image.NewNRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out
}

func TestDecodeDIBToPNG_32bppBottomUp(t *testing.T) {
	// 2x1: visual row is written last in a bottom-up DIB.
	dib := buildDIB(t, 2, 1, 32, biRGB, false, [][]byte{
		{ // visual row: red (opaque), green (half alpha)
			0x00, 0x00, 0xFF, 0xFF, 0x00, 0xFF, 0x00, 0x80,
		},
	}, nil)
	img := decodeToNRGBA(t, dib)
	want := [][4]byte{{0xFF, 0, 0, 0xFF}, {0, 0xFF, 0x00, 0x80}}
	for i, w := range want {
		got := [4]byte{img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3]}
		if got != w {
			t.Errorf("pixel %d = %v, want %v", i, got, w)
		}
	}
}

func TestDecodeDIBToPNG_32bppTopDownZeroAlphaOpaque(t *testing.T) {
	// Top-down (negative height) with an all-zero alpha channel — the common
	// opaque-image-from-DIB-producer shape — must decode as opaque, and the
	// first visual row must be the first stored row.
	dib := buildDIB(t, 1, 2, 32, biRGB, true, [][]byte{
		{0x11, 0x22, 0x33, 0x00},
		{0xAA, 0xBB, 0xCC, 0x00},
	}, nil)
	img := decodeToNRGBA(t, dib)
	if got := [4]byte{img.Pix[0], img.Pix[1], img.Pix[2], img.Pix[3]}; got != [4]byte{0x33, 0x22, 0x11, 0xFF} {
		t.Errorf("row0 = %v, want BGRA→RGBA opaque", got)
	}
	if got := [4]byte{img.Pix[4], img.Pix[5], img.Pix[6], img.Pix[7]}; got != [4]byte{0xCC, 0xBB, 0xAA, 0xFF} {
		t.Errorf("row1 = %v, want BGRA→RGBA opaque", got)
	}
}

func TestDecodeDIBToPNG_24bppRowPadding(t *testing.T) {
	// 3px wide 24bpp rows are padded 9→12 bytes; the decoder must skip the
	// pad, not skew subsequent rows.
	rows := [][]byte{
		{0x00, 0x00, 0xFF, 0x00, 0xFF, 0x00, 0xFF, 0x00, 0x00},
		{0xFF, 0x00, 0x00, 0xFF, 0x00, 0x00, 0xFF, 0x00, 0x00},
	}
	dib := buildDIB(t, 3, 2, 24, biRGB, true, rows, nil)
	img := decodeToNRGBA(t, dib)
	if img.Pix[0] != 0xFF || img.Pix[1] != 0x00 || img.Pix[2] != 0x00 || img.Pix[3] != 0xFF {
		t.Errorf("pixel00 = %v, want red opaque", img.Pix[0:4])
	}
	if img.Pix[12] != 0x00 || img.Pix[13] != 0x00 || img.Pix[14] != 0xFF {
		t.Errorf("pixel10 = %v, want blue", img.Pix[12:16])
	}
}

func TestDecodeDIBToPNG_BitfieldsDefaultMasks(t *testing.T) {
	masks := make([]byte, 12)
	binary.LittleEndian.PutUint32(masks[0:4], 0x00FF0000)
	binary.LittleEndian.PutUint32(masks[4:8], 0x0000FF00)
	binary.LittleEndian.PutUint32(masks[8:12], 0x000000FF)
	dib := buildDIB(t, 1, 1, 32, biBitfields, false, [][]byte{{0xE1, 0xD2, 0xC3, 0x55}}, masks)
	img := decodeToNRGBA(t, dib)
	if got := [4]byte{img.Pix[0], img.Pix[1], img.Pix[2], img.Pix[3]}; got != [4]byte{0xC3, 0xD2, 0xE1, 0x55} {
		t.Errorf("pixel = %v, want default-mask BGRA", got)
	}
}

func TestDecodeDIBToPNG_Rejections(t *testing.T) {
	pixel := [][]byte{{0x00, 0x00, 0xFF, 0xFF}}
	cases := []struct {
		name string
		dib  []byte
		want string
	}{
		{
			name: "16bpp",
			dib:  buildDIB(t, 1, 1, 16, biRGB, false, [][]byte{{0x00, 0x00}}, nil),
			want: "bit depth 16",
		},
		{
			name: "RLE compression",
			dib:  buildDIB(t, 1, 1, 32, 1, false, pixel, nil),
			want: "compression 1",
		},
		{
			name: "non-default bitfield masks",
			dib: buildDIB(t, 1, 1, 32, biBitfields, false, pixel, func() []byte {
				m := make([]byte, 12)
				binary.LittleEndian.PutUint32(m[0:4], 0x000000FF) // swapped mask
				return m
			}()),
			want: "BI_BITFIELDS masks",
		},
		{
			name: "truncated pixels",
			dib:  buildDIB(t, 1, 1, 32, biRGB, false, pixel, nil)[:41],
			want: "truncated",
		},
		{
			name: "bad header size",
			dib: func() []byte {
				d := buildDIB(t, 1, 1, 32, biRGB, false, pixel, nil)
				binary.LittleEndian.PutUint32(d[0:4], 56)
				return d
			}(),
			want: "header size 56",
		},
	}
	for _, c := range cases {
		_, err := decodeDIBToPNG(c.dib)
		if err == nil {
			t.Errorf("%s: err = nil, want %q", c.name, c.want)
			continue
		}
		if !bytes.Contains([]byte(err.Error()), []byte(c.want)) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.want)
		}
	}
}

func TestUTF16ZBytesRoundTrip(t *testing.T) {
	for _, s := range []string{"", "sprite", "精灵 émoji 🎨"} {
		if got := utf16ZBytesToString(utf16ZBytes(s)); got != s {
			t.Errorf("round trip %q → %q", s, got)
		}
	}
	// Trailing garbage after the NUL is ignored.
	if got := utf16ZBytesToString(utf16ZBytes("ab")[:4]); got != "ab" {
		t.Errorf("NUL-terminated prefix = %q, want ab", got)
	}
}

func TestLooksLikePNG(t *testing.T) {
	if !looksLikePNG([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0}) {
		t.Error("PNG signature not detected")
	}
	if looksLikePNG([]byte("PNG...")) {
		t.Error("non-PNG misdetected")
	}
}
