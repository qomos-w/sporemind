package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFakeSDK(t *testing.T, root string) string {
	t.Helper()
	sdk := filepath.Join(root, "sporemind-plugin-sdk")
	if err := os.MkdirAll(sdk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "go.mod"), []byte("module example.com/sdk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return sdk
}

func TestWalkUpForSDKFindsNestedCheckout(t *testing.T) {
	base := t.TempDir()
	want := writeFakeSDK(t, base)
	// A project nested several levels below the checkout root.
	project := filepath.Join(base, "apps", "deep", "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := walkUpForSDK(project); got != want {
		t.Fatalf("walkUpForSDK(%q) = %q, want %q", project, got, want)
	}
}

func TestWalkUpForSDKMissReturnsEmpty(t *testing.T) {
	project := t.TempDir()
	if got := walkUpForSDK(project); got != "" {
		t.Fatalf("walkUpForSDK(%q) = %q, want empty", project, got)
	}
}
