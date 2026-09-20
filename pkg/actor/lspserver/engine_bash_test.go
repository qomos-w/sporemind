package lspserver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
)

func TestFindBashInstall_Paths(t *testing.T) {
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

	bashDir := filepath.Join(nodeDir, "node_modules", "bash-language-server")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(bashDir, bashEntry)), 0o755); err != nil {
		t.Fatalf("mkdir bash out dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bashDir, bashEntry), []byte("fake bash ls"), 0o644); err != nil {
		t.Fatalf("write node-relative bash entry: %v", err)
	}

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", nodeDir+string(filepath.ListSeparator)+origPath)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	node, pkgDir, err := findBashInstall(BashEngineConfig{})
	if err != nil {
		t.Fatalf("findBashInstall: %v", err)
	}
	if node != nodeBin {
		t.Errorf("node binary mismatch: got %q, want %q", node, nodeBin)
	}
	if pkgDir != bashDir {
		t.Errorf("package dir mismatch: got %q, want %q", pkgDir, bashDir)
	}
}

func TestFindBashInstall_Managed(t *testing.T) {
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	root := filepath.Join(tmp, "lsp", "bash-language-server", "5.6.0")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, bashEntry)), 0o755); err != nil {
		t.Fatalf("mkdir managed bash out dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, bashEntry), []byte("fake bash ls"), 0o644); err != nil {
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

	_, pkgDir, err := findBashInstall(BashEngineConfig{})
	if err != nil {
		t.Fatalf("findBashInstall: %v", err)
	}
	if pkgDir != root {
		t.Errorf("managed package dir mismatch: got %q, want %q", pkgDir, root)
	}
}

func TestFindBashInstall_Explicit(t *testing.T) {
	tmp := t.TempDir()
	pkgDir := filepath.Join(tmp, "explicit", "bash-language-server")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(pkgDir, bashEntry)), 0o755); err != nil {
		t.Fatalf("mkdir explicit out dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, bashEntry), []byte("fake bash ls"), 0o644); err != nil {
		t.Fatalf("write explicit entry: %v", err)
	}

	nodeBin := filepath.Join(tmp, "node")
	if runtime.GOOS == "windows" {
		nodeBin += ".exe"
	}
	if err := os.WriteFile(nodeBin, []byte("fake node"), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}

	_, got, err := findBashInstall(BashEngineConfig{NodePath: nodeBin, PackagePath: pkgDir})
	if err != nil {
		t.Fatalf("findBashInstall: %v", err)
	}
	if got != pkgDir {
		t.Errorf("explicit package dir mismatch: got %q, want %q", got, pkgDir)
	}
}

func TestProbeBashInstall(t *testing.T) {
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	st := probeBashInstall()
	if st.Installed {
		t.Fatalf("expected not installed when nothing present")
	}

	root := filepath.Join(tmp, "lsp", "bash-language-server", "5.6.0")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, bashEntry)), 0o755); err != nil {
		t.Fatalf("mkdir managed bash out dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, bashEntry), []byte("fake bash ls"), 0o644); err != nil {
		t.Fatalf("write managed entry: %v", err)
	}
	st = probeBashInstall()
	if !st.Installed {
		t.Fatalf("expected installed from managed dir")
	}
	wantPath := filepath.Join(root, filepath.FromSlash(bashEntry))
	if st.BinaryPath != wantPath {
		t.Errorf("managed binary path mismatch: got %q, want %q", st.BinaryPath, wantPath)
	}
	if st.DownloadSource != sourceManaged {
		t.Errorf("download source mismatch: got %q, want %q", st.DownloadSource, sourceManaged)
	}
}
