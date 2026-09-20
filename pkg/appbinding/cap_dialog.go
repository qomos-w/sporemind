package appbinding

import (
	"reflect"
	"time"
)

// Dialog capabilities. dialog.openFile, dialog.openFolder and dialog.saveFile
// pop the host's native picker and return the chosen absolute path(s) to the
// plugin — the plugin never reads or writes the picked content through these
// calls, it only receives addresses (paths), which it may then use with
// separately-granted fs capabilities or its own processing. All are served
// locally by the pluginhost (no backing gospore callable) through the
// DesktopFilePicker / DesktopFolderPicker / DesktopFileSavePicker seams below.
// They are deliberately explicit callables rather than one mode-parameterized
// call: the picker kind is fixed by the callID. Folder picks are always single
// (see DialogOpenFolderReq).

// DialogFileFilter mirrors the OS picker filter: a display name plus a
// semicolon-separated extension list, e.g. "Image Files" / "*.jpg;*.png".
type DialogFileFilter struct {
	DisplayName string `json:"displayName,omitempty"`
	Pattern     string `json:"pattern,omitempty"`
}

// DialogOpenFileReq is the dialog.openFile wire request. Multiple toggles
// multi-selection; Filters constrain the visible file kinds.
type DialogOpenFileReq struct {
	Multiple bool               `json:"multiple,omitempty"`
	Title    string             `json:"title,omitempty"`
	Filters  []DialogFileFilter `json:"filters,omitempty"`
}

// DialogOpenFolderReq is the dialog.openFolder wire request. Folder picks are
// always single — Windows IFileDialog cannot multi-select folders
// (FOS_PICKFOLDERS greys out files, and the folder dialog is
// single-selection) — so the request carries no Multiple field.
type DialogOpenFolderReq struct {
	Title string `json:"title,omitempty"`
}

// DialogSaveFileReq is the dialog.saveFile wire request. DefaultPath seeds the
// dialog's starting directory and suggested filename (either may be empty);
// Filters constrain the selectable file kinds.
type DialogSaveFileReq struct {
	Title       string             `json:"title,omitempty"`
	DefaultPath string             `json:"defaultPath,omitempty"`
	Filters     []DialogFileFilter `json:"filters,omitempty"`
}

// DialogOpenResp carries the chosen absolute paths. Empty Paths means the
// user cancelled the picker.
type DialogOpenResp struct {
	Paths []string `json:"paths"`
}

// DialogSaveResp carries the chosen save target as an absolute path. An empty
// Path means the user cancelled the picker. The host never writes content —
// the plugin receives the address only and writes through its own granted
// fs capability or subprocess I/O.
type DialogSaveResp struct {
	Path string `json:"path"`
}

// DesktopFilePicker is the desktop-shell seam for dialog.openFile, in the
// debug.EvalJS pattern: the wails app assigns it at startup (pkg/desktop),
// keeping this package free of any desktop import. Server, headless, and
// mobile builds leave it nil and dialog.openFile fails with an explicit
// "no native picker" error instead of silently degrading.
var DesktopFilePicker func(req DialogOpenFileReq) (DialogOpenResp, error)

// DesktopFolderPicker is the desktop-shell seam for dialog.openFolder, the
// folder sibling of DesktopFilePicker with the same nil-means-headless
// contract.
var DesktopFolderPicker func(req DialogOpenFolderReq) (DialogOpenResp, error)

// DesktopFileSavePicker is the desktop-shell seam for dialog.saveFile:
// pops the native save dialog and returns the chosen absolute target path
// (empty = cancelled). Same nil-means-headless contract as its siblings.
var DesktopFileSavePicker func(req DialogSaveFileReq) (DialogSaveResp, error)

func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapDialogOpenFile,
		Title:              "打开文件选择器",
		Description:        "允许应用弹出宿主原生文件选择对话框，并接收所选文件的绝对路径（仅返回地址，不读取内容；取消时返回空数组）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.dialog.openFile.title",
		I18nDescriptionKey: "capability.dialog.openFile.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "打开文件选择器", Description: "允许应用弹出宿主原生文件选择对话框，并接收所选文件的绝对路径（仅返回地址，不读取内容；取消时返回空数组）。"},
			"en-US": {Title: "Open file picker", Description: "Allows the app to open the host's native file picker and receive the absolute paths of the selected files (addresses only, no content is read; cancel returns an empty array)."},
		},
	})
	RegisterCapability(CapabilityDef{
		ID:                 CapDialogOpenFolder,
		Title:              "打开文件夹选择器",
		Description:        "允许应用弹出宿主原生文件夹选择对话框，并接收所选文件夹的绝对路径（仅单选；仅返回地址，不读取内容；取消时返回空数组）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.dialog.openFolder.title",
		I18nDescriptionKey: "capability.dialog.openFolder.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "打开文件夹选择器", Description: "允许应用弹出宿主原生文件夹选择对话框，并接收所选文件夹的绝对路径（仅单选；仅返回地址，不读取内容；取消时返回空数组）。"},
			"en-US": {Title: "Open folder picker", Description: "Allows the app to open the host's native folder picker and receive the absolute path of the selected folder (single selection only; addresses only, no content is read; cancel returns an empty array)."},
		},
	})
	RegisterCapability(CapabilityDef{
		ID:                 CapDialogSaveFile,
		Title:              "打开保存文件对话框",
		Description:        "允许应用弹出宿主原生保存文件对话框，并接收所选保存位置的绝对路径（仅返回地址，不写入内容；取消时返回空路径）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.dialog.saveFile.title",
		I18nDescriptionKey: "capability.dialog.saveFile.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "打开保存文件对话框", Description: "允许应用弹出宿主原生保存文件对话框，并接收所选保存位置的绝对路径（仅返回地址，不写入内容；取消时返回空路径）。"},
			"en-US": {Title: "Open save-file dialog", Description: "Allows the app to open the host's native save-file dialog and receive the absolute path of the chosen save location (address only, no content is written; cancel returns an empty path)."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "dialog.saveFile",
		Capability: CapDialogSaveFile,
		Local:      true,
		ReqType:    reflect.TypeOf(DialogSaveFileReq{}),
		RespType:   reflect.TypeOf(DialogSaveResp{}),
		Note:       "served locally by the pluginhost through the DesktopFileSavePicker desktop seam; returns the chosen absolute save path (empty = cancelled); headless builds fail with an explicit error",
		// A save dialog can legitimately sit open for minutes; lift the
		// generic 30s reverse-call cap on the HTTP-data path.
		Budget: 10 * time.Minute,
	})
	RegisterHostCall(HostCallDef{
		CallID:     "dialog.openFile",
		Capability: CapDialogOpenFile,
		Local:      true,
		ReqType:    reflect.TypeOf(DialogOpenFileReq{}),
		RespType:   reflect.TypeOf(DialogOpenResp{}),
		Note:       "served locally by the pluginhost through the DesktopFilePicker desktop seam; returns selected absolute file paths (empty array = cancelled); headless builds fail with an explicit error",
		// A picker can legitimately sit open for minutes; lift the generic
		// 30s reverse-call cap on the HTTP-data path.
		Budget: 10 * time.Minute,
	})
	RegisterHostCall(HostCallDef{
		CallID:     "dialog.openFolder",
		Capability: CapDialogOpenFolder,
		Local:      true,
		ReqType:    reflect.TypeOf(DialogOpenFolderReq{}),
		RespType:   reflect.TypeOf(DialogOpenResp{}),
		Note:       "served locally by the pluginhost through the DesktopFolderPicker desktop seam; returns the selected absolute folder path (single selection; empty array = cancelled); headless builds fail with an explicit error",
		// A picker can legitimately sit open for minutes; lift the generic
		// 30s reverse-call cap on the HTTP-data path.
		Budget: 10 * time.Minute,
	})
}
