package pluginhost

import (
	"encoding/json"
	"fmt"

	"github.com/qomos-w/sporemind/pkg/appbinding"
)

// handleHostBridgeDialogOpenFile serves the SDK dialog.openFile callID locally:
// it decodes the file picker request and drives the DesktopFilePicker desktop
// seam. The picker blocks for as long as the user keeps the dialog open — the
// callID registers a Budget that lifts the generic 30s reverse-call cap (see
// cap_dialog.go). Headless builds (no desktop shell assigned the seam) fail
// with an explicit error rather than silently degrading.
func (a *Actor) handleHostBridgeDialogOpenFile(req []byte) ([]byte, error) {
	var r appbinding.DialogOpenFileReq
	if len(req) > 0 {
		if err := json.Unmarshal(req, &r); err != nil {
			return nil, fmt.Errorf("pluginhost: dialog.openFile decode: %w", err)
		}
	}
	pick := appbinding.DesktopFilePicker
	if pick == nil {
		return nil, fmt.Errorf("pluginhost: dialog.openFile: 当前运行环境没有原生文件选择器（仅桌面端可用）")
	}
	resp, err := pick(r)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: dialog.openFile: %w", err)
	}
	if resp.Paths == nil {
		resp.Paths = []string{}
	}
	return json.Marshal(resp)
}

// handleHostBridgeDialogOpenFolder serves the SDK dialog.openFolder callID
// locally through the DesktopFolderPicker desktop seam. Folder picks are
// always single — the request type has no multiple field, so the SDK cannot
// request multi-folder selection — and the headless contract matches
// dialog.openFile (explicit no-native-picker error).
func (a *Actor) handleHostBridgeDialogOpenFolder(req []byte) ([]byte, error) {
	var r appbinding.DialogOpenFolderReq
	if len(req) > 0 {
		if err := json.Unmarshal(req, &r); err != nil {
			return nil, fmt.Errorf("pluginhost: dialog.openFolder decode: %w", err)
		}
	}
	pick := appbinding.DesktopFolderPicker
	if pick == nil {
		return nil, fmt.Errorf("pluginhost: dialog.openFolder: 当前运行环境没有原生文件夹选择器（仅桌面端可用）")
	}
	resp, err := pick(r)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: dialog.openFolder: %w", err)
	}
	if resp.Paths == nil {
		resp.Paths = []string{}
	}
	return json.Marshal(resp)
}

// handleHostBridgeDialogSaveFile serves the SDK dialog.saveFile callID locally
// through the DesktopFileSavePicker desktop seam: it pops the native save
// dialog and returns the chosen absolute path (empty = cancelled). Like the
// open variants, the call only returns an address — the plugin writes the
// content itself. Headless builds (no desktop shell assigned the seam) fail
// with an explicit error rather than silently degrading.
func (a *Actor) handleHostBridgeDialogSaveFile(req []byte) ([]byte, error) {
	var r appbinding.DialogSaveFileReq
	if len(req) > 0 {
		if err := json.Unmarshal(req, &r); err != nil {
			return nil, fmt.Errorf("pluginhost: dialog.saveFile decode: %w", err)
		}
	}
	pick := appbinding.DesktopFileSavePicker
	if pick == nil {
		return nil, fmt.Errorf("pluginhost: dialog.saveFile: 当前运行环境没有原生保存文件对话框（仅桌面端可用）")
	}
	resp, err := pick(r)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: dialog.saveFile: %w", err)
	}
	return json.Marshal(resp)
}
