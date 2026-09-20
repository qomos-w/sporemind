package lspserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/util"
)

// writeFakeExecutable writes a file with the platform executable extension.
func writeFakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		name += ".exe"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("fake bin"), 0o755); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestFindRSInstall_Path(t *testing.T) {
	binDir := t.TempDir()
	bin := writeFakeExecutable(t, binDir, "rust-analyzer")

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", binDir+string(filepath.ListSeparator)+origPath)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	got, err := findRSInstall(RSEngineConfig{})
	if err != nil {
		t.Fatalf("findRSInstall: %v", err)
	}
	if got != bin {
		t.Errorf("binary mismatch: got %q, want %q", got, bin)
	}
}

func TestFindRSInstall_Managed(t *testing.T) {
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	root := filepath.Join(tmp, "lsp", "rust-analyzer", "2024-11-18")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir managed rust-analyzer: %v", err)
	}
	want := writeFakeExecutable(t, root, "rust-analyzer")

	got, err := findRSInstall(RSEngineConfig{})
	if err != nil {
		t.Fatalf("findRSInstall: %v", err)
	}
	if got != want {
		t.Errorf("managed binary mismatch: got %q, want %q", got, want)
	}
}

func TestFindRSInstall_Explicit(t *testing.T) {
	bin := writeFakeExecutable(t, t.TempDir(), "rust-analyzer")
	got, err := findRSInstall(RSEngineConfig{BinaryPath: bin})
	if err != nil {
		t.Fatalf("findRSInstall: %v", err)
	}
	if got != bin {
		t.Errorf("binary mismatch: got %q, want %q", got, bin)
	}
}

func TestProbeRSInstall(t *testing.T) {
	tmp := t.TempDir()
	config.SetDataDirForTest(tmp)
	t.Cleanup(config.ResetForTest)

	st := probeRSInstall()
	if st.Installed {
		t.Fatalf("expected not installed when nothing present")
	}

	root := filepath.Join(tmp, "lsp", "rust-analyzer", "2024-11-18")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir managed rust-analyzer: %v", err)
	}
	bin := writeFakeExecutable(t, root, "rust-analyzer")

	st = probeRSInstall()
	if !st.Installed {
		t.Fatalf("expected installed from managed dir")
	}
	if st.BinaryPath != bin {
		t.Errorf("managed binary path mismatch: got %q, want %q", st.BinaryPath, bin)
	}
	if st.DownloadSource != sourceManaged {
		t.Errorf("download source mismatch: got %q, want %q", st.DownloadSource, sourceManaged)
	}
}

// TestRSFakeServerHelper is not a real test: when spawned with
// RS_FAKE_SERVER=1 it emulates rust-analyzer speaking LSP over stdio.
func TestRSFakeServerHelper(t *testing.T) {
	if os.Getenv("RS_FAKE_SERVER") != "1" {
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
		case "textDocument/didOpen":
			writeOut(rpcMessage{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: json.RawMessage(`{"uri":"file:///a.rs","version":1,"diagnostics":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"message":"fake rust diag","severity":1}]}`)})
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

// TestRSEngineLifecycle verifies rust-analyzer engine spawn, initialize, and
// diagnostic forwarding through the shared StdioEngine transport.
func TestRSEngineLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a subprocess")
	}
	log := &testLogger{}
	cmd := exec.Command(os.Args[0], "-test.run=TestRSFakeServerHelper")
	cmd.Env = append(os.Environ(), "RS_FAKE_SERVER=1")
	util.HideWindow(cmd)
	cmd.Stderr = &stderrBridge{log: log, prefix: "lspserver: rust-analyzer stderr"}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	diagCount := atomic.Int32{}
	diagCh := make(chan struct{})
	e, err := newRSEngine(ctx, cmd, log, func(uri string, version int32, diags []byte) {
		if uri == "file:///a.rs" && diagCount.Add(1) == 1 {
			if version != 1 || !bytes.Contains(diags, []byte("fake rust diag")) {
				t.Errorf("bad first diagnostics: version=%d diags=%s", version, diags)
			}
			close(diagCh)
		}
	})
	if err != nil {
		t.Fatalf("newRSEngine: %v", err)
	}
	defer e.Shutdown(ctx)

	if _, err := e.Initialize(ctx, "file:///project"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := e.DidOpen(ctx, "file:///a.rs", "rust", "fn main() {}", 1); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	select {
	case <-diagCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for rust diagnostics")
	}
}