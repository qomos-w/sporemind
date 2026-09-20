package appmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestSDKVendorRewritesExistingApp lays out a non-vendored app (host-path SDK
// replace, the pre-round-7 layout) and verifies appmanager.sdk_vendor converts
// it to the self-contained vendor-sdk layout.
func TestSDKVendorRewritesExistingApp(t *testing.T) {
	env := newBP7Project(t, "sdkvendor-")
	env.write("app/app.appdef", subdirDemoAppDef)
	env.write("app/go.mod", "module com.example.totp\n\ngo 1.24\n\nrequire github.com/qomos-w/sporemind-plugin-sdk v0.0.0\n\nreplace github.com/qomos-w/sporemind-plugin-sdk => F:/dev/sporemind/sporemind-plugin-sdk\n")
	a := newBP7Actor(t)
	ctx := env.ctx()

	resp, err := a.handleSDKVendor(ctx, gen.AppManagerSdkVendorReq{
		ProjectID: bp7ProjectID(t),
		AppDir:    "app",
	})
	if err != nil {
		t.Fatalf("handleSDKVendor: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("sdk_vendor error: %s", resp.Error)
	}
	if !resp.Vendored {
		t.Fatal("expected Vendored=true")
	}
	if resp.Path != "app/vendor-sdk" {
		t.Errorf("Path = %q, want app/vendor-sdk", resp.Path)
	}

	if _, err := os.Stat(filepath.Join(env.root, "app", "vendor-sdk", "go.mod")); err != nil {
		t.Errorf("vendored SDK go.mod missing: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(env.root, "app", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "F:/dev/sporemind/sporemind-plugin-sdk") {
		t.Errorf("host path leaked into go.mod:\n%s", data)
	}
	if !strings.Contains(string(data), "replace github.com/qomos-w/sporemind-plugin-sdk => ./vendor-sdk") {
		t.Errorf("go.mod missing vendored replace:\n%s", data)
	}
}

// TestSDKVendorInvalidAppDir checks the AppDir guard rejects traversal.
func TestSDKVendorInvalidAppDir(t *testing.T) {
	env := newBP7Project(t, "sdkvendor-bad-")
	a := newBP7Actor(t)
	ctx := env.ctx()

	resp, err := a.handleSDKVendor(ctx, gen.AppManagerSdkVendorReq{
		ProjectID: bp7ProjectID(t),
		AppDir:    "../escape",
	})
	if err != nil {
		t.Fatalf("handleSDKVendor: %v", err)
	}
	if resp.Error == "" {
		t.Error("expected error for AppDir=../escape")
	}
}
