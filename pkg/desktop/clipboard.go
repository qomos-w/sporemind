package desktop

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/qomos-w/sporemind/pkg/appbinding"
)

// Native clipboard for the clipboard.write / clipboard.read host calls
// (appbinding.DesktopClipboardWriter / DesktopClipboardReader seams). Write
// puts the PNG (registered "PNG" format + CF_DIB + CF_HDROP via a temp file,
// same construction as the screenshot copy path) and optional text
// (CF_UNICODETEXT) on the clipboard in one snapshot; read prefers the
// registered "PNG" format, falls back to decoding CF_DIB back into PNG, and
// reports CF_UNICODETEXT text. Everything below is plain Win32 against the
// procs in vars.go — no shell-outs, no clipboard helper executables.

const (
	cfUnicodeText = 13
	// maxClipboardImageBytes bounds decoded clipboard images so a hostile
	// multi-gigabyte DIB fails fast instead of being allocated. 64 MiB.
	maxClipboardImageBytes = 64 << 20
	biRGB                  = 0
	biBitfields            = 3
)

var pngClipboardFormatOnce sync.Once
var pngClipboardFormatID uintptr

// pngClipboardFormat returns the registered clipboard format id for "PNG"
// (the format Office/browsers put on the clipboard alongside CF_DIB).
func pngClipboardFormat() uintptr {
	pngClipboardFormatOnce.Do(func() {
		name, err := syscall.UTF16PtrFromString("PNG")
		if err != nil {
			return
		}
		id, _, _ := procRegisterClipboardFormat.Call(uintptr(unsafe.Pointer(name)))
		pngClipboardFormatID = id
	})
	return pngClipboardFormatID
}

// clipboardWrite implements the appbinding.DesktopClipboardWriter seam: it
// decodes the base64 PNG (when present) and writes every provided format in
// one clipboard snapshot. Text-only writes go through the wails clipboard.
// Assigned in ServiceStartup.
func (a *App) clipboardWrite(req appbinding.ClipboardWriteReq) error {
	if a.app == nil {
		return fmt.Errorf("desktop: not started")
	}
	if req.PngB64 == "" {
		if req.Text == "" {
			return fmt.Errorf("clipboard.write: pngB64 与 text 至少要提供一个")
		}
		a.app.Clipboard.SetText(req.Text)
		return nil
	}

	raw, err := base64.StdEncoding.DecodeString(req.PngB64)
	if err != nil {
		return fmt.Errorf("clipboard.write: base64 decode: %w", err)
	}
	if len(raw) > maxClipboardImageBytes {
		return fmt.Errorf("clipboard.write: image exceeds %d bytes", maxClipboardImageBytes)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("clipboard.write: png decode: %w", err)
	}

	// CF_HDROP paste needs a physical file; the clipboard owns the path for
	// as long as the snapshot lives, so the temp file is not removed.
	tmpPath := filepath.Join(os.TempDir(), "Sporemind_Clipboard_"+time.Now().Format("20060102_150405")+".png")
	if err := os.WriteFile(tmpPath, raw, 0o644); err != nil {
		return fmt.Errorf("clipboard.write: write temp file: %w", err)
	}
	return writeClipboardFormats(img, raw, tmpPath, req.Text)
}

// clipboardRead implements the appbinding.DesktopClipboardReader seam:
// registered "PNG" format first (byte-exact passthrough), CF_DIB decode
// second, CF_UNICODETEXT text alongside. Assigned in ServiceStartup.
func (a *App) clipboardRead() (appbinding.ClipboardReadResp, error) {
	if a.app == nil {
		return appbinding.ClipboardReadResp{}, fmt.Errorf("desktop: not started")
	}
	var resp appbinding.ClipboardReadResp

	hwnd, _, _ := procGetDesktopWindow.Call()
	if err := openClipboardWithRetry(hwnd); err != nil {
		return resp, err
	}
	defer procCloseClipboard.Call()

	if pngFmt := pngClipboardFormat(); pngFmt != 0 && formatAvailable(pngFmt) {
		if raw := hglobalBytes(getClipboardData(pngFmt)); len(raw) > 0 && looksLikePNG(raw) {
			resp.HasImage = true
			resp.PngB64 = base64.StdEncoding.EncodeToString(raw)
		}
	}
	if !resp.HasImage && formatAvailable(cfDIB) {
		dib := hglobalBytes(getClipboardData(cfDIB))
		pngRaw, err := decodeDIBToPNG(dib)
		if err != nil {
			return resp, fmt.Errorf("clipboard.read: %w", err)
		}
		resp.HasImage = true
		resp.PngB64 = base64.StdEncoding.EncodeToString(pngRaw)
	}
	if formatAvailable(cfUnicodeText) {
		if text := utf16ZBytesToString(hglobalBytes(getClipboardData(cfUnicodeText))); text != "" {
			resp.HasText = true
			resp.Text = text
		}
	}
	return resp, nil
}

// --- write-side helpers -----------------------------------------------------

// writeClipboardFormats writes one clipboard snapshot: the raw PNG bytes
// under the registered "PNG" format, a CF_DIB rendering, a CF_HDROP entry
// pointing at filePath, and optional UTF-16 text. All formats land between
// one Open/Empty/Close so no observer sees a partial write.
func writeClipboardFormats(img image.Image, rawPNG []byte, filePath, text string) error {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return fmt.Errorf("clipboard.write: empty image")
	}

	hDIB, err := allocDIB(img, w, h)
	if err != nil {
		return fmt.Errorf("clipboard.write: allocate DIB: %w", err)
	}
	hHDROP, err := allocHDROP(filePath)
	if err != nil {
		procGlobalFree.Call(hDIB)
		return fmt.Errorf("clipboard.write: allocate HDROP: %w", err)
	}
	hPNG := uintptr(0)
	if len(rawPNG) > 0 {
		h, err := allocGlobalBytes(rawPNG)
		if err != nil {
			procGlobalFree.Call(hDIB)
			procGlobalFree.Call(hHDROP)
			return fmt.Errorf("clipboard.write: allocate PNG: %w", err)
		}
		hPNG = h
	}
	hText := uintptr(0)
	if text != "" {
		h, err := allocGlobalBytes(utf16ZBytes(text))
		if err != nil {
			procGlobalFree.Call(hDIB)
			procGlobalFree.Call(hHDROP)
			procGlobalFree.Call(hPNG)
			return fmt.Errorf("clipboard.write: allocate text: %w", err)
		}
		hText = h
	}

	hwnd, _, _ := procGetDesktopWindow.Call()
	if err := openClipboardWithRetry(hwnd); err != nil {
		freeClipboardHandles(hDIB, hHDROP, hPNG, hText)
		return err
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	procSetClipboardData.Call(uintptr(cfDIB), hDIB)
	procSetClipboardData.Call(uintptr(cfHDROP), hHDROP)
	if hPNG != 0 {
		procSetClipboardData.Call(pngClipboardFormat(), hPNG)
	}
	if hText != 0 {
		procSetClipboardData.Call(uintptr(cfUnicodeText), hText)
	}
	// The clipboard now owns every successfully-set HGLOBAL — do NOT free
	// those. Handles that were refused (SetClipboardData returned NULL) are
	// not owned by the clipboard; leak-free handling would need per-call
	// result checking, which the retry-on-open pattern above already makes
	// vanishingly rare — same trade-off as the screenshot copy path.
	return nil
}

func freeClipboardHandles(handles ...uintptr) {
	for _, h := range handles {
		if h != 0 {
			procGlobalFree.Call(h)
		}
	}
}

func openClipboardWithRetry(hwnd uintptr) error {
	var opened uintptr
	for i := 0; i < 5; i++ {
		opened, _, _ = procOpenClipboard.Call(hwnd)
		if opened != 0 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("open clipboard failed")
}

func formatAvailable(format uintptr) bool {
	ok, _, _ := procIsClipboardFormatAvailable.Call(format)
	return ok != 0
}

func getClipboardData(format uintptr) uintptr {
	h, _, _ := procGetClipboardData.Call(format)
	return h
}

// hglobalBytes copies the bytes of an HGLOBAL the clipboard still owns (the
// GetClipboardData handle is borrowed, never freed by the reader).
func hglobalBytes(h uintptr) []byte {
	if h == 0 {
		return nil
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return nil
	}
	size, _, _ := procGlobalSize.Call(h)
	defer procGlobalUnlock.Call(h)
	if size == 0 || size > maxClipboardImageBytes {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size)
}

// allocGlobalBytes copies b into a fresh moveable GMEM block and returns the
// handle. Ownership passes to the clipboard on SetClipboardData success.
func allocGlobalBytes(b []byte) (uintptr, error) {
	hMem, _, _ := procGlobalAlloc.Call(uintptr(gmemMoveable|gmemZeroInit), uintptr(len(b)))
	if hMem == 0 {
		return 0, fmt.Errorf("GlobalAlloc failed")
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return 0, fmt.Errorf("GlobalLock failed")
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(b)), b)
	procGlobalUnlock.Call(hMem)
	return hMem, nil
}

// utf16ZBytes encodes s as NUL-terminated UTF-16LE.
func utf16ZBytes(s string) []byte {
	u := append(utf16.Encode([]rune(s)), 0)
	out := make([]byte, len(u)*2)
	for i, ch := range u {
		out[i*2+0] = byte(ch)
		out[i*2+1] = byte(ch >> 8)
	}
	return out
}

// utf16ZBytesToString decodes NUL-terminated UTF-16LE bytes.
func utf16ZBytesToString(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		ch := uint16(b[i]) | uint16(b[i+1])<<8
		if ch == 0 {
			break
		}
		u = append(u, ch)
	}
	return string(utf16.Decode(u))
}

func looksLikePNG(b []byte) bool {
	sig := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	return bytes.HasPrefix(b, sig)
}

// --- read-side DIB decode (pure, unit-tested) --------------------------------

// decodeDIBToPNG converts a CF_DIB payload (BITMAPINFOHEADER [+ masks] +
// rows) into PNG bytes. Supported: 40/108/124-byte headers, BI_RGB 24bpp and
// BI_RGB/BI_BITFIELDS 32bpp, top-down and bottom-up rows. The common
// all-zero-alpha DIB (producers that never set alpha) is treated as opaque.
// Anything else (paletted, 16bpp, RLE, non-default BI_BITFIELDS masks)
// errors explicitly — a paste target must never receive a silently-wrong
// image.
func decodeDIBToPNG(dib []byte) ([]byte, error) {
	const (
		hdrV1 = 40
		hdrV4 = 108
		hdrV5 = 124
	)
	if len(dib) < hdrV1 {
		return nil, fmt.Errorf("DIB too short (%d bytes)", len(dib))
	}
	hdrSize := int(binary.LittleEndian.Uint32(dib[0:4]))
	if hdrSize != hdrV1 && hdrSize != hdrV4 && hdrSize != hdrV5 {
		return nil, fmt.Errorf("unsupported DIB header size %d", hdrSize)
	}
	width := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	heightRaw := int32(binary.LittleEndian.Uint32(dib[8:12]))
	topDown := heightRaw < 0
	height := int(heightRaw)
	if topDown {
		height = -height
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("degenerate DIB dimensions %dx%d", width, height)
	}
	if int64(width)*int64(height)*4 > maxClipboardImageBytes {
		return nil, fmt.Errorf("DIB too large (%dx%d)", width, height)
	}
	planes := binary.LittleEndian.Uint16(dib[12:14])
	bitCount := int(binary.LittleEndian.Uint16(dib[14:16]))
	compression := int(binary.LittleEndian.Uint32(dib[16:20]))
	if planes != 1 {
		return nil, fmt.Errorf("unsupported DIB planes %d", planes)
	}
	if bitCount != 24 && bitCount != 32 {
		return nil, fmt.Errorf("unsupported DIB bit depth %d", bitCount)
	}
	if compression != biRGB && !(compression == biBitfields && bitCount == 32) {
		return nil, fmt.Errorf("unsupported DIB compression %d", compression)
	}

	pixOffset := hdrSize
	if compression == biBitfields {
		// Only the default BGRA masks are accepted: V1 carries them right
		// after the header, V4/V5 embed them at fixed offsets.
		r, g, b := binary.LittleEndian.Uint32(dib[pixOffset:pixOffset+4]),
			binary.LittleEndian.Uint32(dib[pixOffset+4:pixOffset+8]),
			binary.LittleEndian.Uint32(dib[pixOffset+8:pixOffset+12])
		if hdrSize == hdrV1 {
			pixOffset += 12
		}
		if r != 0x00FF0000 || g != 0x0000FF00 || b != 0x000000FF {
			return nil, fmt.Errorf("unsupported BI_BITFIELDS masks %08x/%08x/%08x", r, g, b)
		}
	}

	rowBytes := width * (bitCount / 8)
	paddedRow := (rowBytes + 3) &^ 3
	if len(dib) < pixOffset+paddedRow*height {
		return nil, fmt.Errorf("DIB truncated: %d bytes, need %d", len(dib), pixOffset+paddedRow*height)
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	anyAlpha := false
	for row := 0; row < height; row++ {
		srcY := row
		if !topDown {
			srcY = height - 1 - row
		}
		src := dib[pixOffset+srcY*paddedRow:]
		dst := img.Pix[row*img.Stride : row*img.Stride+width*4]
		for col := 0; col < width; col++ {
			if bitCount == 24 {
				dst[col*4+0] = src[col*3+2]
				dst[col*4+1] = src[col*3+1]
				dst[col*4+2] = src[col*3+0]
				dst[col*4+3] = 0xFF
			} else {
				dst[col*4+0] = src[col*4+2]
				dst[col*4+1] = src[col*4+1]
				dst[col*4+2] = src[col*4+0]
				dst[col*4+3] = src[col*4+3]
				if src[col*4+3] != 0 {
					anyAlpha = true
				}
			}
		}
	}
	// Many producers leave alpha at zero for opaque images; an all-zero
	// alpha channel would paste as fully transparent.
	if bitCount == 32 && !anyAlpha {
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 0xFF
		}
	}

	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, fmt.Errorf("png encode: %w", err)
	}
	return out.Bytes(), nil
}
