package appbinding

import (
	"reflect"
)

// Clipboard capabilities. clipboard.write puts PNG image bytes and/or text
// on the system clipboard; clipboard.read returns whatever image/text the
// system clipboard currently holds. Images travel as PNG in standard base64
// on the host-call wire (the frame/process transport is JSON — the same
// precedent as computeruse's ImageBase64 and FileDragOutRequest's
// ArchiveTarGzBase64; the no-base64 rule governs the panel data plane, not
// host calls). Both are served locally by the pluginhost through the
// DesktopClipboardWriter / DesktopClipboardReader seams below, so the
// pluginhost never imports the desktop package and headless/server builds
// fail explicitly instead of degrading.

// ClipboardWriteReq is the clipboard.write wire request. At least one of
// PngB64 (standard-base64 PNG bytes) or Text must be non-empty; when both
// are present the host writes them as one clipboard snapshot (image formats
// plus CF_UNICODETEXT), so a paste target picks whichever it prefers.
type ClipboardWriteReq struct {
	PngB64 string `json:"pngB64,omitempty"`
	Text   string `json:"text,omitempty"`
}

// ClipboardWriteResp is the clipboard.write response: an empty marker so the
// generated caller is typed.
type ClipboardWriteResp struct{}

// ClipboardReadResp is the clipboard.read wire response. HasImage/HasText
// report which kinds the clipboard holds; image content is normalized to a
// base64 PNG regardless of the native format (the registered "PNG" format is
// passed through, CF_DIB is decoded and re-encoded).
type ClipboardReadResp struct {
	HasImage bool   `json:"hasImage"`
	PngB64   string `json:"pngB64,omitempty"`
	HasText  bool   `json:"hasText"`
	Text     string `json:"text,omitempty"`
}

// DesktopClipboardWriter is the desktop-shell seam for clipboard.write, in
// the debug.EvalJS pattern: the wails app assigns it at startup
// (pkg/desktop), keeping this package free of any desktop import. Server,
// headless, and mobile builds leave it nil and clipboard.write fails with an
// explicit "no native clipboard" error instead of silently degrading.
var DesktopClipboardWriter func(req ClipboardWriteReq) error

// DesktopClipboardReader is the desktop-shell seam for clipboard.read, the
// read sibling of DesktopClipboardWriter with the same nil-means-headless
// contract.
var DesktopClipboardReader func() (ClipboardReadResp, error)

func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapClipboardWrite,
		Title:              "写入系统剪贴板",
		Description:        "允许应用把图像（PNG）和/或文本写入系统剪贴板，覆盖当前剪贴板内容。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.clipboard.write.title",
		I18nDescriptionKey: "capability.clipboard.write.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "写入系统剪贴板", Description: "允许应用把图像（PNG）和/或文本写入系统剪贴板，覆盖当前剪贴板内容。"},
			"en-US": {Title: "Write system clipboard", Description: "Allows the app to put PNG image bytes and/or text on the system clipboard, replacing its current content."},
		},
	})
	RegisterCapability(CapabilityDef{
		ID:                 CapClipboardRead,
		Title:              "读取系统剪贴板",
		Description:        "允许应用读取系统剪贴板当前持有的图像（PNG）与文本——剪贴板可能包含密码等敏感内容，请仅在明确需要时授予。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.clipboard.read.title",
		I18nDescriptionKey: "capability.clipboard.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "读取系统剪贴板", Description: "允许应用读取系统剪贴板当前持有的图像（PNG）与文本——剪贴板可能包含密码等敏感内容，请仅在明确需要时授予。"},
			"en-US": {Title: "Read system clipboard", Description: "Allows the app to read the image (PNG) and text currently on the system clipboard — clipboards can hold secrets like passwords; grant only when clearly needed."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "clipboard.write",
		Capability: CapClipboardWrite,
		Local:      true,
		ReqType:    reflect.TypeOf(ClipboardWriteReq{}),
		RespType:   reflect.TypeOf(ClipboardWriteResp{}),
		Note:       "served locally by the pluginhost through the DesktopClipboardWriter desktop seam; writes base64 PNG and/or text to the system clipboard in one snapshot; headless builds fail with an explicit error",
	})
	RegisterHostCall(HostCallDef{
		CallID:     "clipboard.read",
		Capability: CapClipboardRead,
		Local:      true,
		RespType:   reflect.TypeOf(ClipboardReadResp{}),
		Note:       "served locally by the pluginhost through the DesktopClipboardReader desktop seam; returns the clipboard's image (base64 PNG) and text with HasImage/HasText markers; headless builds fail with an explicit error",
	})
}
