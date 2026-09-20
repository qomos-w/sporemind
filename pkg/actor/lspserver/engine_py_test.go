package lspserver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
)

func TestFindPYInstall_Paths(t *testing.T) {
	tmp := t.TempDir()

	// Create a fake node binary on PATH.
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
	// The node-relative prefix probe looks for <nodeDir>/node_modules/pyright.
	pyrightDir := filepath.Join(nodeDir, "node_modules", "pyright")
	if err := os.MkdirAll(pyrightDir, 0o755); err != nil {
		t.Fatalf("mkdir node-relative pyright: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pyrightDir, "langserver.index.js"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("write node-relative pyright entry: %v", err)
	}
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", nodeDir+string(filepath.ListSeparator)+origPath)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	node, pkgDir, err := findPYInstall(PYEngineConfig{})
	if err != nil {
		t.Fatalf("findPYInstall: %v", err)
	}
	if node != nodeBin {
		t.Errorf("node binary mismatch: got %q, want %q", node, nodeBin)
	}
	if pkgDir != pyrightDir {
		t.Errorf("package dir mismatch: got %q, want %q", pkgDir, pyrightDir)
	}
}

func TestFindPYInstall_Managed(t *testing.T) {
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	root := filepath.Join(tmp, "lsp", "pyright", "1.2.3")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir managed pyright: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "langserver.index.js"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("write managed entry: %v", err)
	}

	// Create fake node on PATH so findPYInstall does not fail early.
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

	_, pkgDir, err := findPYInstall(PYEngineConfig{})
	if err != nil {
		t.Fatalf("findPYInstall: %v", err)
	}
	if pkgDir != root {
		t.Errorf("managed package dir mismatch: got %q, want %q", pkgDir, root)
	}
}

func TestFindPYInstall_Explicit(t *testing.T) {
	tmp := t.TempDir()
	pkgDir := filepath.Join(tmp, "explicit", "pyright")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir explicit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "langserver.index.js"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("write explicit entry: %v", err)
	}

	nodeBin := filepath.Join(tmp, "node")
	if runtime.GOOS == "windows" {
		nodeBin += ".exe"
	}
	if err := os.WriteFile(nodeBin, []byte("fake node"), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}

	_, got, err := findPYInstall(PYEngineConfig{NodePath: nodeBin, PackagePath: pkgDir})
	if err != nil {
		t.Fatalf("findPYInstall: %v", err)
	}
	if got != pkgDir {
		t.Errorf("explicit package dir mismatch: got %q, want %q", got, pkgDir)
	}
}

func TestProbePYInstall(t *testing.T) {
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	// No managed install and no node -> not installed, no error.
	st := probePYInstall()
	if st.Installed {
		t.Fatalf("expected not installed when nothing present")
	}

	// Managed install wins.
	root := filepath.Join(tmp, "lsp", "pyright", "1.2.3")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir managed pyright: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "langserver.index.js"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("write managed entry: %v", err)
	}
	st = probePYInstall()
	if !st.Installed {
		t.Fatalf("expected installed from managed dir")
	}
	if st.BinaryPath != root {
		t.Errorf("managed binary path mismatch: got %q, want %q", st.BinaryPath, root)
	}
	if st.DownloadSource != sourceManaged {
		t.Errorf("download source mismatch: got %q, want %q", st.DownloadSource, sourceManaged)
	}
}
