package lspserver

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/util"
)

// BashEngine serves Bash through bash-language-server, an npm-packaged LSP
// server. Detection mirrors the Python engine's npm cascade.
type BashEngine struct {
	*StdioEngine
}

// BashEngineConfig carries optional install overrides. Empty values fall back
// to the detection probes: node on PATH, then bash-language-server via the
// same npm cascade used by the WEB engines (node-relative prefix, global npm
// root, PATH shim, NODE_PATH), plus the managed-install directory.
type BashEngineConfig struct {
	NodePath    string // node executable (default: exec.LookPath("node"))
	PackagePath string // absolute path to the bash-language-server package dir
	ManagedRoot string // managed LSP root (default: <DataDir>/lsp); the actor override flows here
}

// bashEntry is the server entry point inside the bash-language-server package.
// Pinned to 5.6.0 layout: the actual bin script lives at out/cli.js (not
// bin/main.js as earlier versions used).
const bashEntry = "out/cli.js"

// NewBashEngine resolves the bash-language-server install and returns a
// StdioEngine configured for it. The spawned process lives as long as ctx.
func NewBashEngine(ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte), cfg BashEngineConfig) (*BashEngine, error) {
	nodeBin, pkgDir, err := findBashInstall(cfg)
	if err != nil {
		return nil, err
	}
	cmd := util.Command(nodeBin, filepath.Join(pkgDir, filepath.FromSlash(bashEntry)), "--stdio")
	cmd.Stderr = &stderrBridge{log: log, prefix: "lspserver: bash-language-server stderr"}
	return newBashEngine(ctx, cmd, log, diagnostics)
}

// newBashEngine binds an already-built subprocess command as a BashEngine via
// the generic StdioEngine transport. Tests use this entry point with a fake
// server binary.
func newBashEngine(ctx context.Context, cmd *exec.Cmd, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte)) (*BashEngine, error) {
	se, err := NewStdioEngine(ctx, cmd, log, diagnostics, StdioOptions{})
	if err != nil {
		return nil, err
	}
	return &BashEngine{StdioEngine: se}, nil
}

// --- Install detection -------------------------------------------------------

// findBashInstall probes for node and the bash-language-server package
// directory. Errors carry an actionable message.
func findBashInstall(cfg BashEngineConfig) (nodeBin, pkgDir string, err error) {
	nodeBin = cfg.NodePath
	if nodeBin == "" {
		nodeBin, err = exec.LookPath("node")
		if err != nil {
			return "", "", errors.New("lsp bash: node not found on PATH; install Node.js >= 22.22.2 and run 'npm install -g bash-language-server'")
		}
	}

	pkgDir, err = findNPMPackage(nodeBin, "bash-language-server", bashEntry, cfg.PackagePath, filepath.Join(resolveLSPRoot(cfg.ManagedRoot), "bash-language-server"))
	if err != nil {
		return "", "", err
	}
	return nodeBin, pkgDir, nil
}

// Compile-time check that BashEngine satisfies LanguageEngine.
var _ LanguageEngine = (*BashEngine)(nil)
