package lspserver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
)

func TestFindWEBInstall_Paths(t *testing.T) {
	tmp := t.TempDir()

	nodeDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatalf("mkdir node dir: %v", err)
	}
	nodeBin := filepath.Join(nodeDir, "node")
	if runtime.GOOS == "windows" {
		nodeBin += ".exe"
	}
	if err := os.WriteFile(nodeBin, []byte("fake node"), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}

	webDir := filepath.Join(nodeDir, "node_modules", "vscode-langservers-extracted")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(webDir, webCSSEntry)), 0o755); err != nil {
		t.Fatalf("mkdir web bin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(webDir, webCSSEntry), []byte("fake css ls"), 0o644); err != nil {
		t.Fatalf("write node-relative css entry: %v", err)
	}

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", nodeDir+string(filepath.ListSeparator)+origPath)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	node, pkgDir, err := findWEBInstall(WEBEngineConfig{Server: webCSSEntry})
	if err != nil {
		t.Fatalf("findWEBInstall: %v", err)
	}
	if node != nodeBin {
		t.Errorf("node binary mismatch: got %q, want %q", node, nodeBin)
	}
	if pkgDir != webDir {
		t.Errorf("package dir mismatch: got %q, want %q", pkgDir, webDir)
	}
}

func TestFindWEBInstall_Managed(t *testing.T) {
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	root := filepath.Join(tmp, "lsp", "vscode-langservers-extracted", "4.10.0")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, webJSONEntry)), 0o755); err != nil {
		t.Fatalf("mkdir managed web bin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, webJSONEntry), []byte("fake json ls"), 0o644); err != nil {
		t.Fatalf("write managed entry: %v", err)
	}

	nodeDir := t.TempDir()
	nodeBin := filepath.Join(nodeDir, "node")
	if runtime.GOOS == "windows" {
		nodeBin += ".exe"
	}
	if err := os.WriteFile(nodeBin, []byte("fake node"), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", nodeDir+string(filepath.ListSeparator)+origPath)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	_, pkgDir, err := findWEBInstall(WEBEngineConfig{Server: webJSONEntry})
	if err != nil {
		t.Fatalf("findWEBInstall: %v", err)
	}
	if pkgDir != root {
		t.Errorf("managed package dir mismatch: got %q, want %q", pkgDir, root)
	}
}

func TestFindWEBInstall_Explicit(t *testing.T) {
	tmp := t.TempDir()
	pkgDir := filepath.Join(tmp, "explicit", "vscode-langservers-extracted")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(pkgDir, webHTMLEntry)), 0o755); err != nil {
		t.Fatalf("mkdir explicit bin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, webHTMLEntry), []byte("fake html ls"), 0o644); err != nil {
		t.Fatalf("write explicit entry: %v", err)
	}

	nodeBin := filepath.Join(tmp, "node")
	if runtime.GOOS == "windows" {
		nodeBin += ".exe"
	}
	if err := os.WriteFile(nodeBin, []byte("fake node"), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}

	_, got, err := findWEBInstall(WEBEngineConfig{NodePath: nodeBin, PackagePath: pkgDir, Server: webHTMLEntry})
	if err != nil {
		t.Fatalf("findWEBInstall: %v", err)
	}
	if got != pkgDir {
		t.Errorf("explicit package dir mismatch: got %q, want %q", got, pkgDir)
	}
}

func TestProbeWEBInstall(t *testing.T) {
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	st := probeJSONInstall()
	if st.Installed {
		t.Fatalf("expected not installed when nothing present")
	}

	root := filepath.Join(tmp, "lsp", "vscode-langservers-extracted", "4.10.0")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, webCSSEntry)), 0o755); err != nil {
		t.Fatalf("mkdir managed web bin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, webCSSEntry), []byte("fake css ls"), 0o644); err != nil {
		t.Fatalf("write managed entry: %v", err)
	}
	st = probeCSSInstall()
	if !st.Installed {
		t.Fatalf("expected installed from managed dir")
	}
	wantPath := filepath.Join(root, filepath.FromSlash(webCSSEntry))
	if st.BinaryPath != wantPath {
		t.Errorf("managed binary path mismatch: got %q, want %q", st.BinaryPath, wantPath)
	}
	if st.DownloadSource != sourceManaged {
		t.Errorf("download source mismatch: got %q, want %q", st.DownloadSource, sourceManaged)
	}
}
