package pluginhost

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
)

// withTestClipboard installs throwaway DesktopClipboardWriter/Reader seams
// and restores the previous values (nil unless a desktop shell is attached).
func withTestClipboard(t *testing.T, write func(appbinding.ClipboardWriteReq) error, read func() (appbinding.ClipboardReadResp, error)) {
	t.Helper()
	prevW, prevR := appbinding.DesktopClipboardWriter, appbinding.DesktopClipboardReader
	appbinding.DesktopClipboardWriter, appbinding.DesktopClipboardReader = write, read
	t.Cleanup(func() {
		appbinding.DesktopClipboardWriter, appbinding.DesktopClipboardReader = prevW, prevR
	})
}

func TestHandleHostBridgeClipboardWrite_Dispatch(t *testing.T) {
	a := &Actor{}

	withTestClipboard(t, func(req appbinding.ClipboardWriteReq) error {
		if req.PngB64 != "aGk=" || req.Text != "sprite" {
			t.Errorf("req = %+v, want pngB64+text", req)
		}
		return nil
	}, nil)
	out, err := a.handleHostBridgeClipboardWrite([]byte(`{"pngB64":"aGk=","text":"sprite"}`))
	if err != nil {
		t.Fatalf("clipboard.write: %v", err)
	}
	var resp appbinding.ClipboardWriteResp
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
}

func TestHandleHostBridgeClipboardWrite_EmptyReqRejected(t *testing.T) {
	a := &Actor{}
	withTestClipboard(t, func(appbinding.ClipboardWriteReq) error {
		t.Error("seam must not be called for an empty request")
		return nil
	}, nil)
	if _, err := a.handleHostBridgeClipboardWrite([]byte(`{}`)); err == nil || !strings.Contains(err.Error(), "至少") {
		t.Fatalf("err = %v, want at-least-one-payload error", err)
	}
}

func TestHandleHostBridgeClipboardWrite_NoClipboard(t *testing.T) {
	a := &Actor{}
	withTestClipboard(t, nil, nil)
	_, err := a.handleHostBridgeClipboardWrite([]byte(`{"text":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "原生剪贴板") {
		t.Fatalf("err = %v, want explicit no-native-clipboard error", err)
	}
}

func TestHandleHostBridgeClipboardWrite_BadJSON(t *testing.T) {
	a := &Actor{}
	if _, err := a.handleHostBridgeClipboardWrite([]byte(`{nope`)); err == nil {
		t.Fatal("err = nil, want decode error")
	}
}

func TestHandleHostBridgeClipboardRead_Dispatch(t *testing.T) {
	a := &Actor{}

	withTestClipboard(t, nil, func() (appbinding.ClipboardReadResp, error) {
		return appbinding.ClipboardReadResp{HasImage: true, PngB64: "aGk=", HasText: true, Text: "备注"}, nil
	})
	out, err := a.handleHostBridgeClipboardRead(nil)
	if err != nil {
		t.Fatalf("clipboard.read: %v", err)
	}
	var resp appbinding.ClipboardReadResp
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if !resp.HasImage || resp.PngB64 != "aGk=" || !resp.HasText || resp.Text != "备注" {
		t.Errorf("resp = %+v, want image+text payload", resp)
	}
}

func TestHandleHostBridgeClipboardRead_NoClipboard(t *testing.T) {
	a := &Actor{}
	withTestClipboard(t, nil, nil)
	_, err := a.handleHostBridgeClipboardRead(nil)
	if err == nil || !strings.Contains(err.Error(), "原生剪贴板") {
		t.Fatalf("err = %v, want explicit no-native-clipboard error", err)
	}
}

func TestHandleHostBridgeClipboardRead_SeamError(t *testing.T) {
	a := &Actor{}
	withTestClipboard(t, nil, func() (appbinding.ClipboardReadResp, error) {
		return appbinding.ClipboardReadResp{}, errClipboardBoom
	})
	if _, err := a.handleHostBridgeClipboardRead(nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want seam error passthrough", err)
	}
}

var errClipboardBoom = &clipboardError{"boom"}

type clipboardError struct{ msg string }

func (e *clipboardError) Error() string { return e.msg }
