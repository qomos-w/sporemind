package lspserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/util"
)

// TestFindClangdInstall_Explicit verifies that an explicit BinaryPath wins
// over all other sources.
func TestFindClangdInstall_Explicit(t *testing.T) {
	binDir := t.TempDir()
	bin := writeFakeExecutable(t, binDir, clangdBinaryName())

	got, err := findClangdInstall(ClangdEngineConfig{BinaryPath: bin})
	if err != nil {
		t.Fatalf("findClangdInstall: %v", err)
	}
	if got != bin {
		t.Fatalf("got %q, want %q", got, bin)
	}
}

// TestFindClangdInstall_Path verifies that a clangd on PATH is preferred over
// a managed install.
func TestFindClangdInstall_Path(t *testing.T) {
	// Create a managed install that would be found via findManagedClangd.
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	managedDir := filepath.Join(tmp, "lsp", "clangd", "22.1.6", "bin")
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = writeFakeExecutable(t, managedDir, clangdBinaryName())

	// Create a PATH binary that should win over the managed one.
	pathDir := t.TempDir()
	pathBin := writeFakeExecutable(t, pathDir, clangdBinaryName())

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", pathDir+string(filepath.ListSeparator)+origPath)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	got, err := findClangdInstall(ClangdEngineConfig{})
	if err != nil {
		t.Fatalf("findClangdInstall: %v", err)
	}
	if got != pathBin {
		t.Fatalf("got %q (PATH), want %q", got, pathBin)
	}
}

// TestFindClangdInstall_ManagedFallback verifies that when no PATH clangd
// exists, the managed install under bin/ is found.
func TestFindClangdInstall_ManagedFallback(t *testing.T) {
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	managedDir := filepath.Join(tmp, "lsp", "clangd", "22.1.6", "bin")
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := writeFakeExecutable(t, managedDir, clangdBinaryName())

	// Set PATH to a directory that does NOT contain clangd.
	emptyDir := t.TempDir()
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", emptyDir)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	got, err := findClangdInstall(ClangdEngineConfig{})
	if err != nil {
		t.Fatalf("findClangdInstall: %v", err)
	}
	if got != want {
		t.Fatalf("got %q (managed), want %q", got, want)
	}
}

// TestFindClangdInstall_ManagedRootLevelFallback verifies that a binary placed
// directly in the version dir (no bin/ subdir) is also found, as a defensive
// measure for future layout changes.
func TestFindClangdInstall_ManagedRootLevelFallback(t *testing.T) {
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	rootDir := filepath.Join(tmp, "lsp", "clangd", "22.1.6")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := writeFakeExecutable(t, rootDir, clangdBinaryName())

	emptyDir := t.TempDir()
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", emptyDir)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	got, err := findClangdInstall(ClangdEngineConfig{})
	if err != nil {
		t.Fatalf("findClangdInstall: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestProbeCPPInstall verifies that probeCPPInstall reports PATH first, then
// managed, then not installed.
func TestProbeCPPInstall(t *testing.T) {
	// No clangd anywhere.
	emptyDir := t.TempDir()
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", emptyDir)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	st := probeCPPInstall()
	if st.Installed {
		t.Fatalf("expected not installed when no clangd exists, got %+v", st)
	}

	// Only managed install.
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	managedDir := filepath.Join(tmp, "lsp", "clangd", "22.1.6", "bin")
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = writeFakeExecutable(t, managedDir, clangdBinaryName())

	st = probeCPPInstall()
	if !st.Installed {
		t.Fatalf("expected managed install to be found, got %+v", st)
	}
	if st.DownloadSource != sourceManaged {
		t.Fatalf("expected sourceManaged, got %q", st.DownloadSource)
	}

	// PATH clangd wins over managed.
	pathDir := t.TempDir()
	pathBin := writeFakeExecutable(t, pathDir, clangdBinaryName())
	os.Setenv("PATH", pathDir)

	st = probeCPPInstall()
	if !st.Installed {
		t.Fatalf("expected PATH clangd to be found, got %+v", st)
	}
	if st.DownloadSource != sourcePath {
		t.Fatalf("expected sourcePath, got %q", st.DownloadSource)
	}
	if st.BinaryPath != pathBin {
		t.Fatalf("expected BinaryPath %q, got %q", pathBin, st.BinaryPath)
	}
}

// TestClangdFakeServerHelper is not a real test: when spawned with
// CLANGD_FAKE_SERVER=1 it emulates clangd speaking LSP over stdio.
func TestClangdFakeServerHelper(t *testing.T) {
	if os.Getenv("CLANGD_FAKE_SERVER") != "1" {
		return
	}
	br := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	writeOut := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(out, "Content-Length: %d\r\n\r\n", len(b))
		_, _ = out.Write(b)
		_ = out.Flush()
	}
	for {
		body, err := readRPCFrame(br)
		if err != nil {
			os.Exit(0)
		}
		var msg rpcMessage
		_ = json.Unmarshal(body, &msg)
		switch msg.Method {
		case "initialize":
			writeOut(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{"capabilities":{"textDocumentSync":1}}`)})
		case "initialized":
			// no-op
		case "shutdown":
			writeOut(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`null`)})
		case "exit":
			os.Exit(0)
		default:
			if len(msg.ID) > 0 {
				writeOut(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{"ok":true}`)})
			}
		}
	}
}

// TestClangdEngineLifecycle verifies the full spawn/initialize/shutdown cycle
// with a clangd-like subprocess helper.
func TestClangdEngineLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a subprocess")
	}

	log := &testLogger{}
	cmd := exec.Command(os.Args[0], "-test.run=TestClangdFakeServerHelper")
	cmd.Env = append(os.Environ(), "CLANGD_FAKE_SERVER=1")
	util.HideWindow(cmd)
	cmd.Stderr = &stderrBridge{log: log, prefix: "lspserver: clangd stderr"}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	engine, err := newClangdEngine(ctx, cmd, log, nil)
	if err != nil {
		t.Fatalf("newClangdEngine: %v", err)
	}
	defer engine.Shutdown(context.Background())

	if engine.cmd == nil || engine.cmd.Process == nil {
		t.Fatal("engine process not started")
	}
}