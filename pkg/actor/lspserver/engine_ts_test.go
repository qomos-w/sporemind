package lspserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/util"
)

// testLogger records leveled log lines for assertions.
type testLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLogger) Debug(msg string, args ...any) { l.record("debug", msg, args...) }
func (l *testLogger) Info(msg string, args ...any)  { l.record("info", msg, args...) }
func (l *testLogger) Warn(msg string, args ...any)  { l.record("warn", msg, args...) }
func (l *testLogger) Error(msg string, args ...any) { l.record("error", msg, args...) }

func (l *testLogger) record(level, msg string, args ...any) {
	var sb strings.Builder
	sb.WriteString(level)
	sb.WriteString(": ")
	sb.WriteString(msg)
	for i := 0; i+1 < len(args); i += 2 {
		fmt.Fprintf(&sb, " %v=%v", args[i], args[i+1])
	}
	l.mu.Lock()
	l.lines = append(l.lines, sb.String())
	l.mu.Unlock()
}

func (l *testLogger) contains(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// waitLog polls until the logger contains sub or the deadline passes.
func waitLog(l *testLogger, sub string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if l.contains(sub) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// --- LSP stdio framing -----------------------------------------------------

func TestReadRPCFrame(t *testing.T) {
	// Well-formed message decodes to the exact body.
	msg := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	raw := "Content-Length: " + strconv.Itoa(len(msg)) + "\r\n" +
		"Content-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n" + msg
	body, err := readRPCFrame(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("readRPCFrame: %v", err)
	}
	if string(body) != msg {
		t.Fatalf("body mismatch:\n got %s\nwant %s", body, msg)
	}

	// Missing Content-Length is a framing error.
	if _, err := readRPCFrame(bufio.NewReader(strings.NewReader("GET / HTTP/1.1\r\n\r\n"))); err == nil {
		t.Fatal("expected error for missing Content-Length")
	}

	// Truncated body is a framing error.
	raw2 := "Content-Length: 100\r\n\r\nshort"
	if _, err := readRPCFrame(bufio.NewReader(strings.NewReader(raw2))); err == nil {
		t.Fatal("expected error for truncated body")
	}
}

func TestTSWriteFrame(t *testing.T) {
	pr, pw := io.Pipe()
	e := &StdioEngine{stdin: pw, log: &testLogger{}, pending: make(map[int64]chan *rpcMessage)}
	go func() {
		_ = e.write(rpcMessage{
			JSONRPC: "2.0",
			ID:      json.RawMessage("7"),
			Method:  "textDocument/hover",
			Params:  mustMarshalParams(map[string]any{"x": 1}),
		})
		_ = pw.Close()
	}()

	body, err := readRPCFrame(bufio.NewReader(pr))
	if err != nil {
		t.Fatalf("readRPCFrame: %v", err)
	}
	var got rpcMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Method != "textDocument/hover" || string(got.ID) != "7" {
		t.Fatalf("envelope mismatch: %s", body)
	}
	var p struct {
		X int `json:"x"`
	}
	if err := json.Unmarshal(got.Params, &p); err != nil || p.X != 1 {
		t.Fatalf("params mismatch: %s", got.Params)
	}
}

// --- TSEngine lifecycle against a fake LSP server ---------------------------

// TestTSFakeServerHelper is not a real test: when spawned with TS_FAKE_SERVER=1
// (from TestTSEngineLifecycle below) it emulates typescript-language-server
// speaking LSP 3.17 over stdio, so the whole TSEngine subprocess machinery is
// exercised without requiring the npm install. Markers are printed to stderr
// and observed through the engine's stderr bridge.
func TestTSFakeServerHelper(t *testing.T) {
	if os.Getenv("TS_FAKE_SERVER") != "1" {
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
			fmt.Fprintln(os.Stderr, "helper: read error:", err)
			os.Exit(3)
		}
		var msg rpcMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			fmt.Fprintln(os.Stderr, "helper: bad frame:", err)
			continue
		}

		// Responses (to the config request the helper issued below).
		if msg.Method == "" {
			if string(msg.ID) == "9001" {
				var resp struct {
					ID     json.RawMessage  `json:"id"`
					Result []map[string]any `json:"result"`
				}
				_ = json.Unmarshal(body, &resp)
				if len(resp.Result) == 1 {
					if ts, ok := resp.Result[0]["tabSize"].(float64); ok && ts == 4 {
						fmt.Fprintln(os.Stderr, "helper: WS_CONFIG_OK")
					} else {
						fmt.Fprintln(os.Stderr, "helper: WS_CONFIG_BAD")
					}
				} else {
					fmt.Fprintln(os.Stderr, "helper: WS_CONFIG_BAD")
				}
			}
			continue
		}

		switch msg.Method {
		case "initialize":
			var p struct {
				RootURI string `json:"rootUri"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			fmt.Fprintln(os.Stderr, "helper: ROOT="+p.RootURI)
			writeOut(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{"capabilities":{"textDocumentSync":1}}`)})
			// tls announces its TypeScript version right after initialize.
			writeOut(rpcMessage{JSONRPC: "2.0", Method: "$/typescriptVersion", Params: json.RawMessage(`{"version":"6.0.3","source":"fake"}`)})
		case "initialized":
			// tls asks for workspace configuration lazily; emulate that now.
			writeOut(rpcMessage{JSONRPC: "2.0", ID: json.RawMessage("9001"), Method: "workspace/configuration", Params: json.RawMessage(`{"items":[{"section":"formattingOptions","scopeUri":"file:///x"}]}`)})
			fmt.Fprintln(os.Stderr, "helper: SENT_CONFIG")
		case "textDocument/didOpen":
			var p struct {
				TextDocument struct {
					URI        string `json:"uri"`
					LanguageID string `json:"languageId"`
					Version    int    `json:"version"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			fmt.Fprintln(os.Stderr, "helper: OPEN="+p.TextDocument.URI+" lang="+p.TextDocument.LanguageID+" ver="+strconv.Itoa(p.TextDocument.Version))
			writeOut(rpcMessage{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: json.RawMessage(`{"uri":"file:///a.ts","version":1,"diagnostics":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"message":"fake diag","severity":1}]}`)})
		case "textDocument/didChange":
			var p struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
				} `json:"textDocument"`
				Changes json.RawMessage `json:"contentChanges"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			fmt.Fprintln(os.Stderr, "helper: CHANGE="+p.TextDocument.URI+" ver="+strconv.Itoa(p.TextDocument.Version)+" changes="+string(p.Changes))
			writeOut(rpcMessage{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: json.RawMessage(`{"uri":"file:///a.ts","version":2,"diagnostics":[]}`)})
		case "textDocument/didClose":
			fmt.Fprintln(os.Stderr, "helper: CLOSE")
		case "textDocument/definition":
			writeOut(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`[{"uri":"file:///a.ts","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":2}}}]`)})
		case "textDocument/completion":
			writeOut(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{"isIncomplete":false,"items":[]}`)})
		case "textDocument/hover":
			// Slow responder so the cancellation path can be exercised.
			time.Sleep(500 * time.Millisecond)
			writeOut(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{"contents":{"kind":"markdown","value":"hover"}}`)})
		case "$/cancelRequest":
			fmt.Fprintln(os.Stderr, "helper: CANCELLED")
		case "shutdown":
			writeOut(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`null`)})
		case "exit":
			fmt.Fprintln(os.Stderr, "helper: EXITED")
			os.Exit(0)
		default:
			// Any other request is answered generically so the engine never
			// blocks; any notification is acknowledged via stderr marker.
			if len(msg.ID) > 0 {
				writeOut(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{"ok":true}`)})
				fmt.Fprintln(os.Stderr, "helper: REQ="+msg.Method)
			} else {
				fmt.Fprintln(os.Stderr, "helper: NOTIF="+msg.Method)
			}
		}
	}
}

// fakeServerCmd builds the helper subprocess command for the lifecycle test.
func fakeServerCmd(t *testing.T) (*exec.Cmd, *testLogger) {
	t.Helper()
	log := &testLogger{}
	cmd := exec.Command(os.Args[0], "-test.run=TestTSFakeServerHelper")
	cmd.Env = append(os.Environ(), "TS_FAKE_SERVER=1")
	util.HideWindow(cmd)
	cmd.Stderr = &stderrBridge{log: log, prefix: "lspserver: typescript-language-server stderr"}
	return cmd, log
}

func TestTSEngineLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a subprocess")
	}
	cmd, serverLog := fakeServerCmd(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	diagCh := make(chan struct{})
	diagCalled := make(chan struct{})
	var diagCount atomic.Int32
	e, err := newTSEngine(ctx, cmd, serverLog, func(uri string, version int32, diags []byte) {
		if uri != "file:///a.ts" {
			t.Errorf("diagnostics uri mismatch: got %s", uri)
		}
		if diagCount.Add(1) == 1 {
			if version != 1 || !strings.Contains(string(diags), "fake diag") {
				t.Errorf("bad first diagnostics: version=%d diags=%s", version, diags)
			}
			close(diagCh)
		} else {
			close(diagCalled)
		}
	}, "")
	if err != nil {
		t.Fatalf("newTSEngine: %v", err)
	}
	t.Cleanup(func() { e.Close() })

	// initialize handshake.
	res, err := e.Initialize(ctx, "file:///root")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !strings.Contains(string(res), "textDocumentSync") {
		t.Fatalf("unexpected initialize result: %s", res)
	}
	if !waitLog(serverLog, "tsserver version", 3*time.Second) {
		t.Error("$/typescriptVersion notification was not logged")
	}

	// didOpen triggers the server's publishDiagnostics -> actor callback.
	if err := e.DidOpen(ctx, "file:///a.ts", "typescript", "const x = 1;", 1); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	select {
	case <-diagCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no publishDiagnostics received after didOpen")
	}

	// didChange: pass-through of contentChanges raw JSON + second diagnostics.
	changes := json.RawMessage(`[{"range":{"start":{"line":0,"character":0}},"text":"const y = 2;"}]`)
	if err := e.DidChange(ctx, "file:///a.ts", 2, changes); err != nil {
		t.Fatalf("DidChange: %v", err)
	}
	select {
	case <-diagCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("no second publishDiagnostics received after didChange")
	}
	if !waitLog(serverLog, "CHANGE=file:///a.ts ver=2", 3*time.Second) {
		t.Error("didChange notification did not carry version + raw changes to the server")
	}

	if err := e.DidClose(ctx, "file:///a.ts"); err != nil {
		t.Fatalf("DidClose: %v", err)
	}

	// workspace/configuration answer must reach the server with tabSize 4.
	if !waitLog(serverLog, "WS_CONFIG_OK", 5*time.Second) {
		t.Error("server did not receive the workspace/configuration response with formattingOptions")
	}

	// Concurrent requests over the single stdio channel.
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := e.Definition(ctx, "file:///a.ts", uint32(i), 0)
			if err != nil {
				errs <- fmt.Errorf("Definition: %w", err)
				return
			}
			if !strings.Contains(string(res), "file:///a.ts") {
				errs <- fmt.Errorf("unexpected definition result: %s", res)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Exercise every remaining LanguageEngine request against the fake.
	methods := []struct {
		name string
		fn   func(ctx context.Context) error
	}{
		{"Completion", func(ctx context.Context) error { _, err := e.Completion(ctx, "file:///a.ts", 1, 0); return err }},
		{"References", func(ctx context.Context) error { _, err := e.References(ctx, "file:///a.ts", 1, 0, true); return err }},
		{"Implementation", func(ctx context.Context) error { _, err := e.Implementation(ctx, "file:///a.ts", 1, 0); return err }},
		{"TypeDefinition", func(ctx context.Context) error { _, err := e.TypeDefinition(ctx, "file:///a.ts", 1, 0); return err }},
		{"DocumentSymbol", func(ctx context.Context) error { _, err := e.DocumentSymbol(ctx, "file:///a.ts"); return err }},
		{"SignatureHelp", func(ctx context.Context) error { _, err := e.SignatureHelp(ctx, "file:///a.ts", 1, 0); return err }},
		{"Formatting", func(ctx context.Context) error { _, err := e.Formatting(ctx, "file:///a.ts"); return err }},
		{"Rename", func(ctx context.Context) error { _, err := e.Rename(ctx, "file:///a.ts", 1, 0, "renamed"); return err }},
		{"PrepareRename", func(ctx context.Context) error { _, err := e.PrepareRename(ctx, "file:///a.ts", 1, 0); return err }},
		{"CodeAction", func(ctx context.Context) error { _, err := e.CodeAction(ctx, "file:///a.ts", 0, 0, 1, 1); return err }},
	}
	for _, m := range methods {
		if err := m.fn(ctx); err != nil {
			t.Errorf("%s: %v", m.name, err)
		}
	}
	if err := e.WarmUp(ctx, "file:///a.ts"); err != nil {
		t.Errorf("WarmUp: %v", err)
	}
	if !waitLog(serverLog, "REQ=textDocument/codeAction", 3*time.Second) {
		t.Errorf("codeAction request did not reach the server (method sweep)")
	}

	// Cancellation: hover is slow; a short-lived ctx must abort quickly.
	shortCtx, cancelShort := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelShort()
	start := time.Now()
	if _, err := e.Hover(shortCtx, "file:///a.ts", 1, 0); err == nil {
		t.Error("expected hover to fail on ctx cancellation")
	}
	if elapsed := time.Since(start); elapsed > 800*time.Millisecond {
		t.Errorf("hover cancellation took too long: %v", elapsed)
	}
	if !waitLog(serverLog, "CANCELLED", 3*time.Second) {
		t.Error("$/cancelRequest was not delivered to the server")
	}

	// Graceful shutdown: shutdown request -> exit notification -> process exit.
	if err := e.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !waitLog(serverLog, "EXITED", 3*time.Second) {
		t.Error("exit notification was not delivered to the server")
	}

	// After shutdown the engine refuses further work.
	if err := e.DidOpen(ctx, "file:///a.ts", "typescript", "x", 3); err == nil {
		t.Error("expected DidOpen after Shutdown to fail")
	}
}
