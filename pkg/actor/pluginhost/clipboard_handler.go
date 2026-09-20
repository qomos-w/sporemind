package pluginhost

import (
	"encoding/json"
	"fmt"

	"github.com/qomos-w/sporemind/pkg/appbinding"
)

// handleHostBridgeClipboardWrite serves the SDK clipboard.write callID
// locally: it decodes the write request (base64 PNG and/or text, at least
// one required) and drives the DesktopClipboardWriter desktop seam, which
// puts all provided formats on the system clipboard in one snapshot.
// Headless builds (no desktop shell assigned the seam) fail with an explicit
// error rather than silently degrading.
func (a *Actor) handleHostBridgeClipboardWrite(req []byte) ([]byte, error) {
	var r appbinding.ClipboardWriteReq
	if len(req) > 0 {
		if err := json.Unmarshal(req, &r); err != nil {
			return nil, fmt.Errorf("pluginhost: clipboard.write decode: %w", err)
		}
	}
	if r.PngB64 == "" && r.Text == "" {
		return nil, fmt.Errorf("pluginhost: clipboard.write: pngB64 与 text 至少要提供一个")
	}
	write := appbinding.DesktopClipboardWriter
	if write == nil {
		return nil, fmt.Errorf("pluginhost: clipboard.write: 当前运行环境没有原生剪贴板（仅桌面端可用）")
	}
	if err := write(r); err != nil {
		return nil, fmt.Errorf("pluginhost: clipboard.write: %w", err)
	}
	return json.Marshal(appbinding.ClipboardWriteResp{})
}

// handleHostBridgeClipboardRead serves the SDK clipboard.read callID locally
// through the DesktopClipboardReader desktop seam: it returns whatever
// image (base64 PNG) and text the system clipboard holds, with HasImage /
// HasText markers. Headless contract matches clipboard.write.
func (a *Actor) handleHostBridgeClipboardRead(req []byte) ([]byte, error) {
	read := appbinding.DesktopClipboardReader
	if read == nil {
		return nil, fmt.Errorf("pluginhost: clipboard.read: 当前运行环境没有原生剪贴板（仅桌面端可用）")
	}
	resp, err := read()
	if err != nil {
		return nil, fmt.Errorf("pluginhost: clipboard.read: %w", err)
	}
	return json.Marshal(resp)
}
