package lspserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/actor"
)

// StdioEngine implements LanguageEngine by running an arbitrary LSP server as
// a subprocess and speaking LSP 3.17 / JSON-RPC 2.0 over stdio. It carries no
// language-specific knowledge: the command line, initializationOptions,
// capabilities and warm-up behavior are supplied by the caller (see
// StdioOptions). TSEngine is a thin registry configuration over it.
type StdioEngine struct {
	cmd         *exec.Cmd
	log         actor.Logger
	diagnostics func(uri string, version int32, diagnosticsJSON []byte)

	opts StdioOptions

	nextID  atomic.Int64
	closing atomic.Bool  // set by Shutdown/Close; reader loop then stays quiet on EOF
	rootURI atomic.Value // stores string; answered back to workspace/workspaceFolders

	mu      sync.Mutex
	pending map[int64]chan *rpcMessage
	dead    bool
	done    chan struct{} // closed when the reader loop exits (process gone or Close)

	writeMu sync.Mutex // serializes writes to stdin (LSP forbids interleaving)
	stdin   io.WriteCloser
}

// StdioOptions carries the per-language knobs of a StdioEngine. Zero values
// fall back to generic LSP defaults.
type StdioOptions struct {
	// RequestTimeout bounds each request that has no deadline of its own.
	RequestTimeout time.Duration
	// ShutdownTimeout bounds how long Shutdown waits for process exit before
	// force-killing.
	ShutdownTimeout time.Duration
	// Stderr (optional) receives the subprocess stderr stream. When nil the
	// child's stderr is discarded into the process's default sink.
	Stderr io.Writer
	// InitOptions is sent as initializationOptions during Initialize.
	InitOptions map[string]any
	// Capabilities overrides the client capability advertisement sent during
	// Initialize. Nil keeps the default set below.
	Capabilities map[string]any
	// WarmUp (optional) customizes WarmUp; nil means a documented no-op.
	WarmUp func(ctx context.Context, uri string) error
	// OnNotification (optional) observes every server notification after the
	// built-in routing (diagnostics / window messages); use it for
	// language-specific notifications like $/typescriptVersion.
	OnNotification func(log actor.Logger, msg *rpcMessage)
}

// Default stdio transport timeouts, mirroring the MCP stdio constants.
const (
	stdioRequestTimeout  = 15 * time.Second
	stdioShutdownTimeout = 10 * time.Second
)

var errStdioEngineDead = errors.New("lsp server process is not running")

// NewStdioEngine spawns cmd and binds it as a LanguageEngine over its stdio
// pipes: starts the reader loop and force-closes the engine when ctx ends
// (the actor's lifecycle context). The diagnostics callback (may be nil)
// receives publishDiagnostics payloads as (uri, version, diagnosticsJSON) —
// the same contract as NewGoEngine.
func NewStdioEngine(ctx context.Context, cmd *exec.Cmd, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte), opts StdioOptions) (*StdioEngine, error) {
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = stdioRequestTimeout
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = stdioShutdownTimeout
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp stdio: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp stdio: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("lsp stdio: spawn: %w", err)
	}

	e := &StdioEngine{
		cmd:         cmd,
		log:         log,
		diagnostics: diagnostics,
		opts:        opts,
		pending:     make(map[int64]chan *rpcMessage),
		done:        make(chan struct{}),
		stdin:       stdin,
	}
	e.rootURI.Store("")
	go e.readLoop(bufio.NewReader(stdout))
	go func() { _ = cmd.Wait() }()
	go func() {
		select {
		case <-ctx.Done():
			e.Close()
		case <-e.done:
		}
	}()
	return e, nil
}

// --- JSON-RPC 2.0 transport -------------------------------------------------

// rpcMessage models every JSON-RPC 2.0 envelope the engine exchanges: requests
// (method+id), responses (id+result|error), notifications (method only).
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// write serializes v with LSP Content-Length framing. The write mutex keeps
// requests/notifications byte-atomic so the server never sees interleaved
// bodies.
func (e *StdioEngine) write(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	e.writeMu.Lock()
	_, werr1 := io.WriteString(e.stdin, header)
	_, werr2 := e.stdin.Write(payload)
	e.writeMu.Unlock()
	if werr1 != nil {
		e.markDead(werr1)
		return werr1
	}
	if werr2 != nil {
		e.markDead(werr2)
		return werr2
	}
	return nil
}

// readRPCFrame reads exactly one LSP-framed message from br.
func readRPCFrame(br *bufio.Reader) (json.RawMessage, error) {
	contentLength := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if contentLength < 0 {
				return nil, errors.New("lsp stdio: message missing Content-Length header")
			}
			body := make([]byte, contentLength)
			if _, err := io.ReadFull(br, body); err != nil {
				return nil, err
			}
			return body, nil
		}
		if rest, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("lsp stdio: invalid Content-Length %q", rest)
			}
			contentLength = n
		}
	}
}

// sendRequest issues a request and blocks for its response; ctx cancellation
// sends $/cancelRequest and returns ctx.Err(). Results are the raw LSP JSON.
func (e *StdioEngine) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := int64(e.nextID.Add(1))
	ch := make(chan *rpcMessage, 1)

	e.mu.Lock()
	if e.dead {
		e.mu.Unlock()
		return nil, errStdioEngineDead
	}
	e.pending[id] = ch
	e.mu.Unlock()

	req := rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: mustMarshalParams(params)}
	if err := e.write(req); err != nil {
		e.cancelPending(id)
		return nil, err
	}

	reqCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, e.opts.RequestTimeout)
		defer cancel()
	}

	stopCancelWatcher := make(chan struct{})
	defer close(stopCancelWatcher)
	go func() {
		select {
		case <-reqCtx.Done():
			e.cancelPending(id)
		case <-stopCancelWatcher:
		}
	}()

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-reqCtx.Done():
		return nil, reqCtx.Err()
	}
}

// notify sends a JSON-RPC notification (no response expected).
func (e *StdioEngine) notify(method string, params any) error {
	msg := rpcMessage{JSONRPC: "2.0", Method: method, Params: mustMarshalParams(params)}
	return e.write(msg)
}

// cancelPending removes a pending request (if any) and cancels it server-side.
func (e *StdioEngine) cancelPending(id int64) {
	e.mu.Lock()
	if _, ok := e.pending[id]; ok {
		delete(e.pending, id)
		e.mu.Unlock()
		_ = e.notify("$/cancelRequest", map[string]any{"id": id})
		return
	}
	e.mu.Unlock()
}

// markDead records that the engine can no longer serve requests and fails all
// in-flight requests. Safe to call from the reader loop AND the writer path.
func (e *StdioEngine) markDead(cause error) {
	e.mu.Lock()
	if e.dead {
		e.mu.Unlock()
		return
	}
	e.dead = true
	pending := e.pending
	e.pending = make(map[int64]chan *rpcMessage)
	e.mu.Unlock()

	for _, ch := range pending {
		ch <- &rpcMessage{Error: &rpcError{Code: -32603, Message: "server process died: " + cause.Error()}}
	}
	if cause != nil {
		e.log.Warn("lspserver: stdio engine died", "error", cause.Error())
	}
}

// readLoop drains stdout: responses resolve pending requests, notifications
// route to diagnostics/logging, and server requests (workspace/configuration)
// get answered so the server never times out.
func (e *StdioEngine) readLoop(br *bufio.Reader) {
	defer close(e.done)
	for {
		body, err := readRPCFrame(br)
		if err != nil {
			if e.closing.Load() {
				return // expected EOF after Shutdown/Close
			}
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				e.markDead(err)
			} else {
				e.markDead(errors.New("stdout closed"))
			}
			return
		}
		var msg rpcMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			e.log.Warn("lspserver: stdio engine sent unparsable message", "error", err.Error())
			continue
		}
		if msg.Method == "" {
			e.resolveResponse(&msg)
			continue
		}
		if len(msg.ID) == 0 {
			e.handleNotification(&msg)
			continue
		}
		e.handleServerRequest(&msg)
	}
}

func (e *StdioEngine) resolveResponse(msg *rpcMessage) {
	id, err := strconv.ParseInt(string(msg.ID), 10, 64)
	if err != nil {
		e.log.Debug("lspserver: stdio engine response with non-numeric id", "id", string(msg.ID))
		return
	}
	e.mu.Lock()
	ch, ok := e.pending[id]
	if ok {
		delete(e.pending, id)
	}
	e.mu.Unlock()
	if !ok {
		e.log.Debug("lspserver: stdio engine response for unknown request", "id", string(msg.ID))
		return
	}
	ch <- msg
}

func (e *StdioEngine) handleNotification(msg *rpcMessage) {
	switch msg.Method {
	case "textDocument/publishDiagnostics":
		var params struct {
			URI         string          `json:"uri"`
			Version     int             `json:"version"`
			Diagnostics json.RawMessage `json:"diagnostics"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			e.log.Warn("lspserver: publishDiagnostics params unparsable", "error", err.Error())
			return
		}
		if e.diagnostics != nil {
			e.diagnostics(params.URI, int32(params.Version), params.Diagnostics)
		}
	case "window/logMessage", "window/showMessage", "window/showWarningMessage", "window/showErrorMessage":
		var params struct {
			Type    int    `json:"type"`
			Message string `json:"message"`
			Text    string `json:"text"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		text := params.Message
		if text == "" {
			text = params.Text
		}
		switch t := msg.Method; t {
		case "window/showWarningMessage":
			e.log.Warn("lspserver: lsp server", "message", text)
		case "window/showErrorMessage":
			e.log.Error("lspserver: lsp server", "message", text)
		default:
			switch params.Type {
			case 1:
				e.log.Error("lspserver: lsp server", "message", text)
			case 2:
				e.log.Warn("lspserver: lsp server", "message", text)
			default:
				e.log.Info("lspserver: lsp server", "message", text)
			}
		}
	default:
		if e.opts.OnNotification != nil {
			e.opts.OnNotification(e.log, msg)
			return
		}
		e.log.Debug("lspserver: stdio engine notification", "method", msg.Method)
	}
}

// handleServerRequest answers server->client requests. Servers issue
// workspace/configuration lazily (formatting options); anything unknown gets a
// method-not-found error so the server never hangs waiting on our reply.
func (e *StdioEngine) handleServerRequest(msg *rpcMessage) {
	switch msg.Method {
	case "workspace/configuration":
		e.respondConfiguration(msg.ID, msg.Params)
	case "workspace/workspaceFolders":
		folders := []any{}
		if uri := e.rootURI.Load().(string); uri != "" {
			folders = append(folders, map[string]any{"uri": uri, "name": ""})
		}
		_ = e.write(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: mustMarshalParams(folders)})
	default:
		_ = e.write(rpcMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error:   &rpcError{Code: -32601, Message: "method not found: " + msg.Method},
		})
	}
}

func (e *StdioEngine) respondConfiguration(id json.RawMessage, params json.RawMessage) {
	var req struct {
		Items []struct {
			Section string `json:"section"`
		} `json:"items"`
	}
	_ = json.Unmarshal(params, &req)

	results := make([]any, len(req.Items))
	for i, item := range req.Items {
		switch item.Section {
		case "formattingOptions":
			results[i] = map[string]any{"tabSize": 4, "insertSpaces": true}
		default:
			results[i] = nil
		}
	}
	_ = e.write(rpcMessage{JSONRPC: "2.0", ID: id, Result: mustMarshalParams(results)})
}

// Close force-terminates the subprocess. Idempotent; safe from any goroutine.
func (e *StdioEngine) Close() {
	e.closing.Store(true)
	e.mu.Lock()
	if e.dead {
		e.mu.Unlock()
		return
	}
	e.dead = true
	e.mu.Unlock()
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}
	select {
	case <-e.done:
	case <-time.After(e.opts.ShutdownTimeout):
	}
}

// --- LanguageEngine implementation -----------------------------------------

func (e *StdioEngine) Initialize(ctx context.Context, rootURI string) (json.RawMessage, error) {
	params := map[string]any{
		"processId": nil,
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{},
				"definition":         map[string]any{"linkSupport": true},
				"completion":         map[string]any{"completionItem": map[string]any{"snippetSupport": false}},
			},
			"workspace": map[string]any{"configuration": true},
		},
		"initializationOptions": e.opts.InitOptions,
		"workspaceFolders":      []map[string]any{{"uri": rootURI, "name": ""}},
	}
	if e.opts.Capabilities != nil {
		params["capabilities"] = e.opts.Capabilities
	}
	e.rootURI.Store(rootURI)
	result, err := e.sendRequest(ctx, "initialize", params)
	if err != nil {
		return nil, err
	}
	// The initialized notification completes the handshake (LSP §initialize).
	if err := e.notify("initialized", map[string]any{}); err != nil {
		return nil, err
	}
	return result, nil
}

func (e *StdioEngine) DidOpen(ctx context.Context, uri, languageID, text string, version int32) error {
	_ = ctx // notifications are fire-and-forget; cancellation is handled by sendRequest paths
	return e.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": languageID,
			"version":    version,
			"text":       text,
		},
	})
}

func (e *StdioEngine) DidChange(ctx context.Context, uri string, version int32, changes json.RawMessage) error {
	_ = ctx
	return e.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": json.RawMessage(changes),
	})
}

func (e *StdioEngine) DidClose(ctx context.Context, uri string) error {
	_ = ctx
	return e.notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
}

func (e *StdioEngine) positionParams(uri string, line, char uint32) map[string]any {
	return map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": char},
	}
}

func (e *StdioEngine) Definition(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error) {
	return e.sendRequest(ctx, "textDocument/definition", e.positionParams(uri, line, char))
}

func (e *StdioEngine) Hover(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error) {
	return e.sendRequest(ctx, "textDocument/hover", e.positionParams(uri, line, char))
}

func (e *StdioEngine) Completion(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error) {
	return e.sendRequest(ctx, "textDocument/completion", e.positionParams(uri, line, char))
}

func (e *StdioEngine) References(ctx context.Context, uri string, line, char uint32, includeDecl bool) (json.RawMessage, error) {
	params := e.positionParams(uri, line, char)
	params["context"] = map[string]any{"includeDeclaration": includeDecl}
	return e.sendRequest(ctx, "textDocument/references", params)
}

func (e *StdioEngine) Implementation(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error) {
	return e.sendRequest(ctx, "textDocument/implementation", e.positionParams(uri, line, char))
}

func (e *StdioEngine) TypeDefinition(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error) {
	return e.sendRequest(ctx, "textDocument/typeDefinition", e.positionParams(uri, line, char))
}

func (e *StdioEngine) DocumentSymbol(ctx context.Context, uri string) (json.RawMessage, error) {
	return e.sendRequest(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
}

func (e *StdioEngine) SignatureHelp(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error) {
	return e.sendRequest(ctx, "textDocument/signatureHelp", e.positionParams(uri, line, char))
}

func (e *StdioEngine) Formatting(ctx context.Context, uri string) (json.RawMessage, error) {
	return e.sendRequest(ctx, "textDocument/formatting", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"options":      map[string]any{"tabSize": 4, "insertSpaces": true},
	})
}

func (e *StdioEngine) Rename(ctx context.Context, uri string, line, char uint32, newName string) (json.RawMessage, error) {
	params := e.positionParams(uri, line, char)
	params["newName"] = newName
	return e.sendRequest(ctx, "textDocument/rename", params)
}

func (e *StdioEngine) PrepareRename(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error) {
	return e.sendRequest(ctx, "textDocument/prepareRename", e.positionParams(uri, line, char))
}

func (e *StdioEngine) CodeAction(ctx context.Context, uri string, startLine, startChar, endLine, endChar uint32) (json.RawMessage, error) {
	return e.sendRequest(ctx, "textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"range": map[string]any{
			"start": map[string]any{"line": startLine, "character": startChar},
			"end":   map[string]any{"line": endLine, "character": endChar},
		},
		"context": map[string]any{"diagnostics": []any{}},
	})
}

// Shutdown performs the LSP shutdown request, then the exit notification, and
// waits up to ShutdownTimeout for the process to exit before force-killing.
func (e *StdioEngine) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	if e.dead {
		e.mu.Unlock()
		return nil // already gone; nothing to shut down
	}
	e.mu.Unlock()
	e.closing.Store(true)

	shCtx, cancel := context.WithTimeout(ctx, e.opts.RequestTimeout)
	defer cancel()
	if _, err := e.sendRequest(shCtx, "shutdown", nil); err != nil && !errors.Is(err, errStdioEngineDead) {
		e.log.Debug("lspserver: stdio shutdown request failed", "error", err.Error())
	}
	_ = e.notify("exit", nil)

	select {
	case <-e.done:
		e.mu.Lock()
		e.dead = true
		e.mu.Unlock()
		return nil
	case <-time.After(e.opts.ShutdownTimeout):
		e.Close()
		return fmt.Errorf("lsp stdio: server did not exit within %s", e.opts.ShutdownTimeout)
	}
}

// WarmUp runs the configured warm-up hook; without one it is a documented
// no-op (many servers load projects lazily on first didOpen/query).
func (e *StdioEngine) WarmUp(ctx context.Context, uri string) error {
	if e.opts.WarmUp != nil {
		return e.opts.WarmUp(ctx, uri)
	}
	return nil
}

// Compile-time check that StdioEngine satisfies LanguageEngine.
var _ LanguageEngine = (*StdioEngine)(nil)

// stderrBridge forwards subprocess stderr lines to the actor logger.
type stderrBridge struct {
	log    actor.Logger
	prefix string
}

func (b *stderrBridge) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	if line != "" {
		b.log.Debug(b.prefix, "line", line)
	}
	return len(p), nil
}

// mustMarshalParams marshals params into raw JSON, returning null on failure
// (sendRequest/notify then emit a null body; the caller reports the real error).
func mustMarshalParams(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}
