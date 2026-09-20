package lspserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/util"
)

// LanguageEngine is the engine-agnostic analysis surface the actor talks to.
// Each implementation (GoEngine today, a subprocess-based TSEngine later)
// binds the actor's LSP calls to a concrete language server. Every result is
// a raw LSP JSON payload so the actor never parses server-specific shapes.
type LanguageEngine interface {
	Initialize(ctx context.Context, rootURI string) (json.RawMessage, error)
	DidOpen(ctx context.Context, uri, languageID, text string, version int32) error
	DidChange(ctx context.Context, uri string, version int32, changes json.RawMessage) error
	DidClose(ctx context.Context, uri string) error
	Definition(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error)
	Hover(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error)
	Completion(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error)
	References(ctx context.Context, uri string, line, char uint32, includeDecl bool) (json.RawMessage, error)
	Implementation(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error)
	TypeDefinition(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error)
	DocumentSymbol(ctx context.Context, uri string) (json.RawMessage, error)
	SignatureHelp(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error)
	Formatting(ctx context.Context, uri string) (json.RawMessage, error)
	Rename(ctx context.Context, uri string, line, char uint32, newName string) (json.RawMessage, error)
	PrepareRename(ctx context.Context, uri string, line, char uint32) (json.RawMessage, error)
	CodeAction(ctx context.Context, uri string, startLine, startChar, endLine, endChar uint32) (json.RawMessage, error)
	Shutdown(ctx context.Context) error
	WarmUp(ctx context.Context, uri string) error
}

// GoEngine serves Go through gopls (golang.org/x/tools/gopls) run as a
// subprocess speaking LSP over stdio. The binary is resolved from PATH, then
// from the Go toolchain's install dir (GOBIN / $(go env GOPATH)/bin).
type GoEngine struct {
	*StdioEngine
}

// NewGoEngine resolves the gopls binary and returns a StdioEngine bound to it.
// The diagnostics callback (may be nil) receives gopls publishDiagnostics
// notifications as (uri, version, diagnosticsJSON). The spawned process lives
// as long as ctx.
func NewGoEngine(ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte)) (*GoEngine, error) {
	bin, err := findGoplsInstall()
	if err != nil {
		return nil, err
	}
	cmd := util.Command(bin)
	cmd.Stderr = &stderrBridge{log: log, prefix: "lspserver: gopls stderr"}
	return newGoEngine(ctx, cmd, log, diagnostics)
}

// newGoEngine binds an already-built subprocess command as a GoEngine via the
// generic StdioEngine transport. Tests use this entry point with a fake
// server binary.
func newGoEngine(ctx context.Context, cmd *exec.Cmd, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte)) (*GoEngine, error) {
	se, err := NewStdioEngine(ctx, cmd, log, diagnostics, StdioOptions{})
	if err != nil {
		return nil, err
	}
	return &GoEngine{StdioEngine: se}, nil
}

// --- Install detection -------------------------------------------------------

// findGoplsInstall probes for the gopls executable: PATH first, then the Go
// toolchain's install dir (GOBIN when set, otherwise $(go env GOPATH)/bin —
// both destinations of `go install golang.org/x/tools/gopls@version`).
// Errors carry an actionable message.
func findGoplsInstall() (string, error) {
	if bin, err := exec.LookPath("gopls"); err == nil {
		return bin, nil
	}
	if bin := goplsInGoInstallDir(); bin != "" {
		return bin, nil
	}
	return "", errors.New("lsp go: gopls not found on PATH or in GOBIN/$(go env GOPATH)/bin; install it via lsp.install or go install golang.org/x/tools/gopls@" + goplsVersion)
}

// goplsInGoInstallDir returns the gopls binary inside the Go toolchain's
// install dir, or empty when the toolchain or the binary is unavailable.
func goplsInGoInstallDir() string {
	gobin, gopath := goEnvInstallDirs()
	for _, dir := range []string{gobin, filepath.Join(gopath, "bin")} {
		if dir == "" {
			continue
		}
		bin := filepath.Join(dir, goplsBinaryName())
		if st, err := os.Stat(bin); err == nil && !st.IsDir() {
			return bin
		}
	}
	return ""
}

// goEnvInstallDirs resolves GOBIN and GOPATH from the go toolchain, each empty
// on any failure (missing go, empty output).
func goEnvInstallDirs() (gobin, gopath string) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", ""
	}
	out, err := util.Command(goBin, "env", "GOBIN", "GOPATH").Output()
	if err != nil {
		return "", ""
	}
	// GOBIN is usually empty, so the first line is blank — trim per line, not
	// the whole output, or the empty GOBIN line disappears.
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return "", ""
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
}

// goplsBinaryName returns the platform-specific gopls executable name.
func goplsBinaryName() string {
	if runtime.GOOS == "windows" {
		return "gopls.exe"
	}
	return "gopls"
}

// Compile-time check that GoEngine satisfies LanguageEngine.
var _ LanguageEngine = (*GoEngine)(nil)
