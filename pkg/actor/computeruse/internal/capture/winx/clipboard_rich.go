//go:build windows

package winx

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
)

// Registered (non-standard) clipboard formats are cached so we look them
// up at most once per process. CF_HTML is registered as "HTML Format".
var (
	cfHTMLOnce sync.Once
	cfHTMLID   uint32
)

func cfHTML() uint32 {
	cfHTMLOnce.Do(func() {
		name, _ := syscall.UTF16PtrFromString("HTML Format")
		id, _, _ := pRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
		cfHTMLID = uint32(id)
	})
	return cfHTMLID
}

func clipboardFormatAvailable(fmtID uint32) bool {
	ret, _, _ := pIsClipboardFormatAvailable.Call(uintptr(fmtID))
	return ret != 0
}

// GetClipboardData scans every supported format and bundles what's present.
func (c *Capturer) GetClipboardData() (capture.ClipboardData, error) {
	ret, _, _ := pOpenClipboard.Call(0)
	if ret == 0 {
		return capture.ClipboardData{Kind: "empty"}, errors.New("OpenClipboard failed")
	}
	defer pCloseClipboard.Call()

	var d capture.ClipboardData
	formats := 0

	if clipboardFormatAvailable(cfUnicode) {
		if text, ok := readClipboardText(); ok && text != "" {
			d.Text = text
			formats++
		}
	}
	if clipboardFormatAvailable(cfDIB) {
		if img, ok := readClipboardImage(); ok && img != nil {
			d.Image = img
			formats++
		}
	}
	if clipboardFormatAvailable(cfHDROP) {
		if files, ok := readClipboardFiles(); ok && len(files) > 0 {
			d.Files = files
			formats++
		}
	}
	if htmlID := cfHTML(); htmlID != 0 && clipboardFormatAvailable(htmlID) {
		if html, ok := readClipboardHTML(htmlID); ok && html != "" {
			d.HTML = html
			formats++
		}
	}

	switch {
	case formats == 0:
		d.Kind = "empty"
	case formats == 1 && d.Image != nil:
		d.Kind = "image"
	case formats == 1 && len(d.Files) > 0:
		d.Kind = "files"
	case formats == 1 && d.Text != "":
		d.Kind = "text"
	case formats == 1 && d.HTML != "":
		d.Kind = "html"
	default:
		d.Kind = "mixed"
	}
	return d, nil
}

// SetClipboardData replaces the clipboard with one format from d.
// Precedence: Image > Files > HTML > Text.
func (c *Capturer) SetClipboardData(d capture.ClipboardData) error {
	if d.Image != nil {
		return writeClipboardImage(d.Image)
	}
	if len(d.Files) > 0 {
		return writeClipboardFiles(d.Files)
	}
	if d.HTML != "" {
		return writeClipboardHTML(d.HTML)
	}
	if d.Text != "" {
		return c.SetClipboard(d.Text)
	}
	return errors.New("clipboard: no data provided")
}

// readClipboardText pulls CF_UNICODETEXT. Must hold OpenClipboard.
func readClipboardText() (string, bool) {
	hData, _, _ := pGetClipboardData.Call(cfUnicode)
	if hData == 0 {
		return "", false
	}
	ptrRaw, _, _ := pGlobalLock.Call(hData)
	if ptrRaw == 0 {
		return "", false
	}
	defer pGlobalUnlock.Call(hData)

	const maxRunes = 1 << 20
	view := unsafe.Slice((*uint16)(ptrFromUintptr(ptrRaw)), maxRunes)
	n := 0
	for n < len(view) && view[n] != 0 {
		n++
	}
	return syscall.UTF16ToString(view[:n]), true
}

// readClipboardImage pulls CF_DIB and rebuilds an RGBA image. The DIB is
// a packed BITMAPINFOHEADER followed by the pixel array. We support only
// 32-bit (BI_RGB) and 24-bit (BI_RGB) bitmaps — the two common cases.
func readClipboardImage() (*image.RGBA, bool) {
	hData, _, _ := pGetClipboardData.Call(cfDIB)
	if hData == 0 {
		return nil, false
	}
	ptrRaw, _, _ := pGlobalLock.Call(hData)
	if ptrRaw == 0 {
		return nil, false
	}
	defer pGlobalUnlock.Call(hData)
	sz, _, _ := pGlobalSize.Call(hData)
	if sz < uintptr(unsafe.Sizeof(bitmapInfoHeader{})) {
		return nil, false
	}
	raw := unsafe.Slice((*byte)(ptrFromUintptr(ptrRaw)), int(sz))
	img, err := parseDIB(raw)
	if err != nil {
		return nil, false
	}
	return img, true
}

// parseDIB decodes a packed CF_DIB blob into an RGBA image. Supports
// 24- and 32-bit BI_RGB bitmaps. Caller owns the returned image.
func parseDIB(raw []byte) (*image.RGBA, error) {
	if len(raw) < int(unsafe.Sizeof(bitmapInfoHeader{})) {
		return nil, errors.New("dib: header truncated")
	}
	bih := (*bitmapInfoHeader)(unsafe.Pointer(&raw[0])) //nolint:govet
	if bih.Compression != biRGB {
		return nil, fmt.Errorf("dib: unsupported compression %d", bih.Compression)
	}
	w := int(bih.Width)
	h := int(bih.Height)
	if w <= 0 || h == 0 {
		return nil, fmt.Errorf("dib: invalid dims %dx%d", w, h)
	}
	topDown := h < 0
	if h < 0 {
		h = -h
	}
	bytesPerPixel := int(bih.BitCount) / 8
	if bytesPerPixel != 3 && bytesPerPixel != 4 {
		return nil, fmt.Errorf("dib: unsupported bit count %d", bih.BitCount)
	}
	rowStride := ((w*int(bih.BitCount) + 31) / 32) * 4
	pixelOffset := int(bih.Size)
	if pixelOffset == 0 {
		pixelOffset = int(unsafe.Sizeof(bitmapInfoHeader{}))
	}
	if pixelOffset+rowStride*h > len(raw) {
		return nil, errors.New("dib: pixel data truncated")
	}
	src := raw[pixelOffset:]
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		srcRow := y
		if !topDown {
			srcRow = h - 1 - y
		}
		base := srcRow * rowStride
		dst := out.Pix[y*out.Stride : y*out.Stride+w*4]
		for x := 0; x < w; x++ {
			si := base + x*bytesPerPixel
			dst[x*4+0] = src[si+2]
			dst[x*4+1] = src[si+1]
			dst[x*4+2] = src[si+0]
			if bytesPerPixel == 4 {
				dst[x*4+3] = src[si+3]
			} else {
				dst[x*4+3] = 0xff
			}
		}
	}
	return out, nil
}

// readClipboardFiles pulls CF_HDROP and returns absolute file paths.
func readClipboardFiles() ([]string, bool) {
	hData, _, _ := pGetClipboardData.Call(cfHDROP)
	if hData == 0 {
		return nil, false
	}
	// DragQueryFileW(hDrop, 0xFFFFFFFF, nil, 0) returns file count.
	count, _, _ := pDragQueryFileW.Call(hData, ^uintptr(0), 0, 0)
	if count == 0 {
		return nil, false
	}
	out := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		// Probe length, then read.
		ln, _, _ := pDragQueryFileW.Call(hData, i, 0, 0)
		if ln == 0 {
			continue
		}
		buf := make([]uint16, ln+1)
		got, _, _ := pDragQueryFileW.Call(hData, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if got == 0 {
			continue
		}
		p := syscall.UTF16ToString(buf[:got])
		if abs, err := filepath.Abs(p); err == nil {
			out = append(out, abs)
		} else {
			out = append(out, p)
		}
	}
	return out, true
}

// readClipboardHTML pulls the registered "HTML Format" clipboard data and
// extracts the fragment between the <!--StartFragment--> markers. The CF_HTML
// payload is UTF-8 (despite the rest of the clipboard being UTF-16).
func readClipboardHTML(fmtID uint32) (string, bool) {
	hData, _, _ := pGetClipboardData.Call(uintptr(fmtID))
	if hData == 0 {
		return "", false
	}
	ptrRaw, _, _ := pGlobalLock.Call(hData)
	if ptrRaw == 0 {
		return "", false
	}
	defer pGlobalUnlock.Call(hData)
	sz, _, _ := pGlobalSize.Call(hData)
	if sz == 0 {
		return "", false
	}
	raw := unsafe.Slice((*byte)(ptrFromUintptr(ptrRaw)), int(sz))
	// Drop trailing NULs.
	for len(raw) > 0 && raw[len(raw)-1] == 0 {
		raw = raw[:len(raw)-1]
	}
	return string(raw), true
}

// writeClipboardImage publishes a CF_DIB version of img. Pixel format is
// 32-bit RGBA -> BGRA, BI_RGB, bottom-up.
func writeClipboardImage(img *image.RGBA) error {
	w := img.Rect.Dx()
	h := img.Rect.Dy()
	if w == 0 || h == 0 {
		return errors.New("clipboard: empty image")
	}
	payload := buildDIB(img)
	totalSz := len(payload)

	ret, _, _ := pOpenClipboard.Call(0)
	if ret == 0 {
		return errors.New("OpenClipboard failed")
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()

	hMem, _, _ := pGlobalAlloc.Call(gmemMoveable, uintptr(totalSz))
	if hMem == 0 {
		return errors.New("GlobalAlloc failed")
	}
	dst, _, _ := pGlobalLock.Call(hMem)
	if dst == 0 {
		pGlobalFree.Call(hMem)
		return errors.New("GlobalLock failed")
	}
	mem := unsafe.Slice((*byte)(ptrFromUintptr(dst)), totalSz)
	copy(mem, payload)
	pGlobalUnlock.Call(hMem)
	if set, _, _ := pSetClipboardData.Call(cfDIB, hMem); set == 0 {
		pGlobalFree.Call(hMem)
		return errors.New("SetClipboardData(CF_DIB) failed")
	}
	return nil
}

// buildDIB serialises an RGBA image into a packed BITMAPINFOHEADER+BGRA
// blob suitable for CF_DIB. Bottom-up row order; alpha preserved.
// Caller must guarantee non-zero dimensions.
func buildDIB(img *image.RGBA) []byte {
	w := img.Rect.Dx()
	h := img.Rect.Dy()
	headerSz := int(unsafe.Sizeof(bitmapInfoHeader{}))
	rowStride := w * 4
	out := make([]byte, headerSz+rowStride*h)
	bih := (*bitmapInfoHeader)(unsafe.Pointer(&out[0])) //nolint:govet
	*bih = bitmapInfoHeader{
		Size:        uint32(headerSz),
		Width:       int32(w),
		Height:      int32(h), // bottom-up
		Planes:      1,
		BitCount:    32,
		Compression: biRGB,
		SizeImage:   uint32(rowStride * h),
	}
	pixDst := out[headerSz:]
	for y := 0; y < h; y++ {
		srcRow := h - 1 - y
		s := img.Pix[srcRow*img.Stride : srcRow*img.Stride+w*4]
		d := pixDst[y*rowStride : y*rowStride+w*4]
		for x := 0; x < w; x++ {
			// RGBA -> BGRA.
			d[x*4+0] = s[x*4+2]
			d[x*4+1] = s[x*4+1]
			d[x*4+2] = s[x*4+0]
			d[x*4+3] = s[x*4+3]
		}
	}
	return out
}

// writeClipboardFiles publishes CF_HDROP with the given absolute paths.
// The HDROP structure is:
//
//	DROPFILES{pFiles=20, ..., fWide=1}
//	wchar_t paths separated by NUL, terminated by double NUL.
func writeClipboardFiles(paths []string) error {
	payload := buildDROPFILES(paths)
	total := len(payload)

	ret, _, _ := pOpenClipboard.Call(0)
	if ret == 0 {
		return errors.New("OpenClipboard failed")
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()

	// GHND = GMEM_MOVEABLE | GMEM_ZEROINIT — defence-in-depth though
	// buildDROPFILES already returns a zeroed header.
	hMem, _, _ := pGlobalAlloc.Call(gmemMoveable|0x0040, uintptr(total))
	if hMem == 0 {
		return errors.New("GlobalAlloc failed")
	}
	dst, _, _ := pGlobalLock.Call(hMem)
	if dst == 0 {
		pGlobalFree.Call(hMem)
		return errors.New("GlobalLock failed")
	}
	mem := unsafe.Slice((*byte)(ptrFromUintptr(dst)), total)
	copy(mem, payload)
	pGlobalUnlock.Call(hMem)
	if set, _, _ := pSetClipboardData.Call(cfHDROP, hMem); set == 0 {
		pGlobalFree.Call(hMem)
		return errors.New("SetClipboardData(CF_HDROP) failed")
	}
	return nil
}

// buildDROPFILES serialises absolute paths into the on-wire CF_HDROP
// payload. Layout:
//
//	[0..3]   DROPFILES.pFiles = 20 (offset to first path)
//	[4..11]  POINT pt = {0, 0}
//	[12..15] BOOL fNC = FALSE
//	[16..19] BOOL fWide = 1 (Unicode)
//	[20..]   UTF-16LE paths, NUL-separated, double-NUL terminated.
func buildDROPFILES(paths []string) []byte {
	const hdrSize = 20
	var buf []uint16
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		buf = append(buf, utf16.Encode([]rune(abs))...)
		buf = append(buf, 0)
	}
	buf = append(buf, 0) // double-NUL terminator
	out := make([]byte, hdrSize+len(buf)*2)
	// out[1..19] left at 0 by make() — covers pt.x/pt.y/fNC and high bytes of pFiles/fWide.
	out[0] = 20
	out[16] = 1
	// UTF-16LE encode into out[20..].
	for i, u := range buf {
		out[hdrSize+i*2] = byte(u)
		out[hdrSize+i*2+1] = byte(u >> 8)
	}
	return out
}

// writeClipboardHTML publishes the registered "HTML Format" clipboard
// payload. The wire format requires a small header with byte offsets.
func writeClipboardHTML(html string) error {
	id := cfHTML()
	if id == 0 {
		return errors.New("RegisterClipboardFormat(HTML Format) failed")
	}
	payload := buildCFHTML(html)

	ret, _, _ := pOpenClipboard.Call(0)
	if ret == 0 {
		return errors.New("OpenClipboard failed")
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()

	sz := len(payload) + 1
	hMem, _, _ := pGlobalAlloc.Call(gmemMoveable, uintptr(sz))
	if hMem == 0 {
		return errors.New("GlobalAlloc failed")
	}
	dst, _, _ := pGlobalLock.Call(hMem)
	if dst == 0 {
		pGlobalFree.Call(hMem)
		return errors.New("GlobalLock failed")
	}
	mem := unsafe.Slice((*byte)(ptrFromUintptr(dst)), sz)
	copy(mem, payload)
	mem[len(payload)] = 0
	pGlobalUnlock.Call(hMem)
	if set, _, _ := pSetClipboardData.Call(uintptr(id), hMem); set == 0 {
		pGlobalFree.Call(hMem)
		return errors.New("SetClipboardData(CF_HTML) failed")
	}
	return nil
}

// buildCFHTML assembles the CF_HTML envelope used by Microsoft's HTML
// clipboard format. The header carries 4 byte offsets that must point
// to specific locations in the resulting blob.
func buildCFHTML(html string) string {
	prefixHeader := "Version:0.9\r\nStartHTML:%010d\r\nEndHTML:%010d\r\n" +
		"StartFragment:%010d\r\nEndFragment:%010d\r\n"
	prefixBefore := "<html><body><!--StartFragment-->"
	suffixAfter := "<!--EndFragment--></body></html>"
	headerLen := len(fmt.Sprintf(prefixHeader, 0, 0, 0, 0))
	startHTML := headerLen
	startFrag := startHTML + len(prefixBefore)
	endFrag := startFrag + len(html)
	endHTML := endFrag + len(suffixAfter)
	header := fmt.Sprintf(prefixHeader, startHTML, endHTML, startFrag, endFrag)
	return header + prefixBefore + html + suffixAfter
}
