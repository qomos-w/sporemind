package pluginhost

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
)

// withTestFilePicker installs a throwaway DesktopFilePicker seam and restores
// the previous value (nil unless a desktop shell is attached).
func withTestFilePicker(t *testing.T, pick func(appbinding.DialogOpenFileReq) (appbinding.DialogOpenResp, error)) {
	t.Helper()
	prev := appbinding.DesktopFilePicker
	appbinding.DesktopFilePicker = pick
	t.Cleanup(func() { appbinding.DesktopFilePicker = prev })
}

// withTestFolderPicker installs a throwaway DesktopFolderPicker seam and
// restores the previous value.
func withTestFolderPicker(t *testing.T, pick func(appbinding.DialogOpenFolderReq) (appbinding.DialogOpenResp, error)) {
	t.Helper()
	prev := appbinding.DesktopFolderPicker
	appbinding.DesktopFolderPicker = pick
	t.Cleanup(func() { appbinding.DesktopFolderPicker = prev })
}

// withTestFileSavePicker installs a throwaway DesktopFileSavePicker seam and
// restores the previous value.
func withTestFileSavePicker(t *testing.T, pick func(appbinding.DialogSaveFileReq) (appbinding.DialogSaveResp, error)) {
	t.Helper()
	prev := appbinding.DesktopFileSavePicker
	appbinding.DesktopFileSavePicker = pick
	t.Cleanup(func() { appbinding.DesktopFileSavePicker = prev })
}

func TestHandleHostBridgeDialogOpenFile_Dispatch(t *testing.T) {
	a := &Actor{}

	withTestFilePicker(t, func(req appbinding.DialogOpenFileReq) (appbinding.DialogOpenResp, error) {
		if !req.Multiple || req.Title != "选文件" {
			t.Errorf("req = %+v, want multiple/title", req)
		}
		return appbinding.DialogOpenResp{Paths: []string{"F:/a.txt", "F:/b.txt"}}, nil
	})
	out, err := a.handleHostBridgeDialogOpenFile([]byte(`{"multiple":true,"title":"选文件"}`))
	if err != nil {
		t.Fatalf("dialog.openFile: %v", err)
	}
	var resp appbinding.DialogOpenResp
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if len(resp.Paths) != 2 || resp.Paths[0] != "F:/a.txt" || resp.Paths[1] != "F:/b.txt" {
		t.Errorf("paths = %v, want [F:/a.txt F:/b.txt]", resp.Paths)
	}
}

func TestHandleHostBridgeDialogOpenFile_CancelIsEmptyArray(t *testing.T) {
	a := &Actor{}
	withTestFilePicker(t, func(req appbinding.DialogOpenFileReq) (appbinding.DialogOpenResp, error) {
		return appbinding.DialogOpenResp{}, nil // cancelled
	})
	out, err := a.handleHostBridgeDialogOpenFile([]byte(`{}`))
	if err != nil {
		t.Fatalf("dialog.openFile: %v", err)
	}
	if got := string(out); got != `{"paths":[]}` {
		t.Errorf("resp = %s, want {\"paths\":[]}", got)
	}
}

func TestHandleHostBridgeDialogOpenFile_NoPicker(t *testing.T) {
	a := &Actor{}
	withTestFilePicker(t, nil)
	_, err := a.handleHostBridgeDialogOpenFile([]byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "原生文件选择器") {
		t.Fatalf("err = %v, want explicit no-native-picker error", err)
	}
}

func TestHandleHostBridgeDialogOpenFile_BadJSON(t *testing.T) {
	a := &Actor{}
	if _, err := a.handleHostBridgeDialogOpenFile([]byte(`{nope`)); err == nil {
		t.Fatal("err = nil, want decode error")
	}
}

func TestHandleHostBridgeDialogOpenFolder_Dispatch(t *testing.T) {
	a := &Actor{}

	withTestFolderPicker(t, func(req appbinding.DialogOpenFolderReq) (appbinding.DialogOpenResp, error) {
		if req.Title != "选文件夹" {
			t.Errorf("title = %q, want 选文件夹", req.Title)
		}
		return appbinding.DialogOpenResp{Paths: []string{"F:/books"}}, nil
	})
	out, err := a.handleHostBridgeDialogOpenFolder([]byte(`{"title":"选文件夹"}`))
	if err != nil {
		t.Fatalf("dialog.openFolder: %v", err)
	}
	var resp appbinding.DialogOpenResp
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if len(resp.Paths) != 1 || resp.Paths[0] != "F:/books" {
		t.Errorf("paths = %v, want [F:/books]", resp.Paths)
	}
}

func TestHandleHostBridgeDialogOpenFolder_CancelIsEmptyArray(t *testing.T) {
	a := &Actor{}
	withTestFolderPicker(t, func(req appbinding.DialogOpenFolderReq) (appbinding.DialogOpenResp, error) {
		return appbinding.DialogOpenResp{}, nil // cancelled
	})
	out, err := a.handleHostBridgeDialogOpenFolder([]byte(`{}`))
	if err != nil {
		t.Fatalf("dialog.openFolder: %v", err)
	}
	if got := string(out); got != `{"paths":[]}` {
		t.Errorf("resp = %s, want {\"paths\":[]}", got)
	}
}

func TestHandleHostBridgeDialogOpenFolder_NoPicker(t *testing.T) {
	a := &Actor{}
	withTestFolderPicker(t, nil)
	_, err := a.handleHostBridgeDialogOpenFolder([]byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "原生文件夹选择器") {
		t.Fatalf("err = %v, want explicit no-native-picker error", err)
	}
}

func TestHandleHostBridgeDialogOpenFolder_BadJSON(t *testing.T) {
	a := &Actor{}
	if _, err := a.handleHostBridgeDialogOpenFolder([]byte(`{nope`)); err == nil {
		t.Fatal("err = nil, want decode error")
	}
}

func TestHandleHostBridgeDialogSaveFile_Dispatch(t *testing.T) {
	a := &Actor{}

	withTestFileSavePicker(t, func(req appbinding.DialogSaveFileReq) (appbinding.DialogSaveResp, error) {
		if req.Title != "另存为" || req.DefaultPath != "F:/art/sprite.aseprite" {
			t.Errorf("req = %+v, want title/defaultPath", req)
		}
		if len(req.Filters) != 1 || req.Filters[0].Pattern != "*.aseprite" {
			t.Errorf("filters = %+v, want one *.aseprite filter", req.Filters)
		}
		return appbinding.DialogSaveResp{Path: "F:/art/sprite.aseprite"}, nil
	})
	out, err := a.handleHostBridgeDialogSaveFile([]byte(`{"title":"另存为","defaultPath":"F:/art/sprite.aseprite","filters":[{"displayName":"Aseprite","pattern":"*.aseprite"}]}`))
	if err != nil {
		t.Fatalf("dialog.saveFile: %v", err)
	}
	var resp appbinding.DialogSaveResp
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if resp.Path != "F:/art/sprite.aseprite" {
		t.Errorf("path = %q, want F:/art/sprite.aseprite", resp.Path)
	}
}

func TestHandleHostBridgeDialogSaveFile_CancelIsEmptyPath(t *testing.T) {
	a := &Actor{}
	withTestFileSavePicker(t, func(req appbinding.DialogSaveFileReq) (appbinding.DialogSaveResp, error) {
		return appbinding.DialogSaveResp{}, nil // cancelled
	})
	out, err := a.handleHostBridgeDialogSaveFile([]byte(`{}`))
	if err != nil {
		t.Fatalf("dialog.saveFile: %v", err)
	}
	if got := string(out); got != `{"path":""}` {
		t.Errorf("resp = %s, want {\"path\":\"\"}", got)
	}
}

func TestHandleHostBridgeDialogSaveFile_NoPicker(t *testing.T) {
	a := &Actor{}
	withTestFileSavePicker(t, nil)
	_, err := a.handleHostBridgeDialogSaveFile([]byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "原生保存文件对话框") {
		t.Fatalf("err = %v, want explicit no-native-save-dialog error", err)
	}
}

func TestHandleHostBridgeDialogSaveFile_BadJSON(t *testing.T) {
	a := &Actor{}
	if _, err := a.handleHostBridgeDialogSaveFile([]byte(`{nope`)); err == nil {
		t.Fatal("err = nil, want decode error")
	}
}
