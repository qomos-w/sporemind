//go:build windows

package winx

import (
	"encoding/binary"
	"image"
	"strings"
	"testing"
	"unsafe"
)

func TestBuildDIBHeader(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	out := buildDIB(img)
	headerSz := int(unsafe.Sizeof(bitmapInfoHeader{}))
	if len(out) != headerSz+4*2*4 {
		t.Fatalf("len=%d want %d", len(out), headerSz+4*2*4)
	}
	// Read fields via parseDIB.
	got, err := parseDIB(out)
	if err != nil {
		t.Fatalf("parseDIB: %v", err)
	}
	if got.Rect.Dx() != 4 || got.Rect.Dy() != 2 {
		t.Fatalf("dims %v want 4x2", got.Rect)
	}
}

func TestDIBRoundtripPreservesRGB(t *testing.T) {
	w, h := 5, 3
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*img.Stride + x*4
			img.Pix[idx+0] = byte(x * 30)   // R
			img.Pix[idx+1] = byte(y * 50)   // G
			img.Pix[idx+2] = byte(127)      // B
			img.Pix[idx+3] = byte(200)      // A
		}
	}
	dib := buildDIB(img)
	back, err := parseDIB(dib)
	if err != nil {
		t.Fatalf("parseDIB: %v", err)
	}
	if back.Rect.Dx() != w || back.Rect.Dy() != h {
		t.Fatalf("dims %v want %dx%d", back.Rect, w, h)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			origIdx := y*img.Stride + x*4
			backIdx := y*back.Stride + x*4
			for c := 0; c < 4; c++ {
				if img.Pix[origIdx+c] != back.Pix[backIdx+c] {
					t.Fatalf("pixel(%d,%d) ch%d: orig=%d back=%d", x, y, c,
						img.Pix[origIdx+c], back.Pix[backIdx+c])
				}
			}
		}
	}
}

func TestDIBBottomUpRowOrder(t *testing.T) {
	// Mark top row red, bottom row blue. After buildDIB (bottom-up),
	// row 0 in the byte buffer should be the BLUE row in BGRA order.
	w, h := 2, 2
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// row 0 = top = red
	img.Pix[0+0] = 255 // R
	img.Pix[4+0] = 255
	img.Pix[0+3] = 255
	img.Pix[4+3] = 255
	// row 1 = bottom = blue
	img.Pix[img.Stride+0+2] = 255 // B
	img.Pix[img.Stride+4+2] = 255
	img.Pix[img.Stride+0+3] = 255
	img.Pix[img.Stride+4+3] = 255

	out := buildDIB(img)
	headerSz := int(unsafe.Sizeof(bitmapInfoHeader{}))
	// First row of pixel data = bottom row of image = blue → BGRA = (255, 0, 0, 255).
	first := out[headerSz : headerSz+4]
	if first[0] != 255 || first[1] != 0 || first[2] != 0 {
		t.Fatalf("first dib row not blue BGRA: %v", first)
	}
}

func TestParseDIBTopDown(t *testing.T) {
	// Hand-craft a top-down 1x1 DIB: height = -1 in the header.
	headerSz := int(unsafe.Sizeof(bitmapInfoHeader{}))
	out := make([]byte, headerSz+4)
	bih := (*bitmapInfoHeader)(unsafe.Pointer(&out[0]))
	*bih = bitmapInfoHeader{
		Size: uint32(headerSz), Width: 1, Height: -1,
		Planes: 1, BitCount: 32, Compression: biRGB, SizeImage: 4,
	}
	// One BGRA pixel = blue.
	out[headerSz+0] = 255 // B
	out[headerSz+1] = 0
	out[headerSz+2] = 0
	out[headerSz+3] = 255 // A
	img, err := parseDIB(out)
	if err != nil {
		t.Fatalf("parseDIB: %v", err)
	}
	// Should decode to RGBA where B=255.
	if img.Pix[0] != 0 || img.Pix[1] != 0 || img.Pix[2] != 255 || img.Pix[3] != 255 {
		t.Fatalf("top-down decode wrong: %v", img.Pix[:4])
	}
}

func TestParseDIB24Bit(t *testing.T) {
	headerSz := int(unsafe.Sizeof(bitmapInfoHeader{}))
	// width=2, height=1, 24-bit. Row stride padded to 4 bytes:
	// (2*24 + 31)/32*4 = 8.
	rowStride := 8
	out := make([]byte, headerSz+rowStride)
	bih := (*bitmapInfoHeader)(unsafe.Pointer(&out[0]))
	*bih = bitmapInfoHeader{
		Size: uint32(headerSz), Width: 2, Height: 1,
		Planes: 1, BitCount: 24, Compression: biRGB, SizeImage: uint32(rowStride),
	}
	// pixel 0 = green BGR, pixel 1 = red BGR.
	out[headerSz+0] = 0
	out[headerSz+1] = 255
	out[headerSz+2] = 0
	out[headerSz+3] = 0
	out[headerSz+4] = 0
	out[headerSz+5] = 255
	// out[headerSz+6..7] = padding (already 0).
	img, err := parseDIB(out)
	if err != nil {
		t.Fatalf("parseDIB 24-bit: %v", err)
	}
	if img.Pix[0+0] != 0 || img.Pix[0+1] != 255 || img.Pix[0+2] != 0 || img.Pix[0+3] != 0xff {
		t.Fatalf("pixel 0 = %v want green opaque", img.Pix[:4])
	}
	if img.Pix[4+0] != 255 || img.Pix[4+1] != 0 || img.Pix[4+2] != 0 {
		t.Fatalf("pixel 1 = %v want red", img.Pix[4:8])
	}
}

func TestParseDIBRejectsTruncated(t *testing.T) {
	if _, err := parseDIB(make([]byte, 4)); err == nil {
		t.Fatal("expected header-truncated error")
	}
}

func TestBuildDROPFILESHeader(t *testing.T) {
	out := buildDROPFILES([]string{"C:\\foo.txt"})
	if len(out) < 20 {
		t.Fatalf("len=%d too short", len(out))
	}
	// DROPFILES.pFiles (uint32 LE) = 20
	if binary.LittleEndian.Uint32(out[0:4]) != 20 {
		t.Fatalf("pFiles offset = %d want 20", binary.LittleEndian.Uint32(out[0:4]))
	}
	// pt.x = 0
	if binary.LittleEndian.Uint32(out[4:8]) != 0 {
		t.Fatalf("pt.x non-zero: %x", out[4:8])
	}
	// pt.y = 0
	if binary.LittleEndian.Uint32(out[8:12]) != 0 {
		t.Fatalf("pt.y non-zero: %x", out[8:12])
	}
	// fNC = FALSE (0)
	if binary.LittleEndian.Uint32(out[12:16]) != 0 {
		t.Fatalf("fNC non-zero: %x", out[12:16])
	}
	// fWide = TRUE (1)
	if binary.LittleEndian.Uint32(out[16:20]) != 1 {
		t.Fatalf("fWide = %d want 1", binary.LittleEndian.Uint32(out[16:20]))
	}
}

func TestBuildDROPFILESPaths(t *testing.T) {
	out := buildDROPFILES([]string{"C:\\a.txt", "C:\\b.txt"})
	// After header, paths are UTF-16LE, NUL-separated, double-NUL terminated.
	tail := out[20:]
	// Decode UTF-16LE manually.
	var sb strings.Builder
	var paths []string
	for i := 0; i+1 < len(tail); i += 2 {
		u := binary.LittleEndian.Uint16(tail[i : i+2])
		if u == 0 {
			if sb.Len() == 0 {
				// double-NUL: stop.
				break
			}
			paths = append(paths, sb.String())
			sb.Reset()
			continue
		}
		sb.WriteRune(rune(u))
	}
	if len(paths) != 2 {
		t.Fatalf("decoded %d paths: %v", len(paths), paths)
	}
	if !strings.HasSuffix(paths[0], "a.txt") || !strings.HasSuffix(paths[1], "b.txt") {
		t.Fatalf("paths = %v", paths)
	}
	// And the trailing double-NUL must be present: the last two bytes are zero.
	last := out[len(out)-2:]
	if last[0] != 0 || last[1] != 0 {
		t.Fatalf("missing double-NUL terminator: %v", last)
	}
}

func TestBuildCFHTMLOffsets(t *testing.T) {
	frag := "<p>hello</p>"
	out := buildCFHTML(frag)
	// Pull StartHTML / EndHTML / StartFragment / EndFragment.
	idx := strings.Index(out, "<!--StartFragment-->")
	if idx < 0 {
		t.Fatal("missing StartFragment marker")
	}
	// The fragment content begins right after the marker.
	startFrag := idx + len("<!--StartFragment-->")
	if out[startFrag:startFrag+len(frag)] != frag {
		t.Fatalf("fragment content mismatch")
	}
	// Header should declare StartFragment = startFrag.
	want := "StartFragment:" + pad10(startFrag)
	if !strings.Contains(out, want) {
		t.Fatalf("header missing %q", want)
	}
	wantEnd := "EndFragment:" + pad10(startFrag+len(frag))
	if !strings.Contains(out, wantEnd) {
		t.Fatalf("header missing %q", wantEnd)
	}
}

func pad10(n int) string {
	s := ""
	for v, w := n, 1_000_000_000; w > 0; w /= 10 {
		s += string(rune('0' + (v/w)%10))
	}
	return s
}
