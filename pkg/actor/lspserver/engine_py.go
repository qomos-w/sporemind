package lspserver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/util"
)

// PYEngine serves Python through pyright, the Microsoft Python type-checker
// and language server.  Pyright is distributed as an npm package whose LSP
// entry point is langserver.index.js, spawned via `node <pkgDir>/langserver.index.js --stdio`.
// The detection logic mirrors the TypeScript engine's findTSInstall but
// locates the pyright package directory instead of typescript-language-server.
type PYEngine struct {
	*StdioEngine
}

// PYEngineConfig carries optional install overrides.  Empty values fall back
// to the detection probes: node on PATH, then pyright via the same cascade
// used by the TypeScript engine (node-relative prefix, global npm root, PATH
// shim, NODE_PATH), plus the managed-install directory.
type PYEngineConfig struct {
	NodePath    string // node executable (default: exec.LookPath("node"))
	PackagePath string // absolute path to the pyright package directory containing langserver.index.js
	ManagedRoot string // managed LSP root (default: <DataDir>/lsp); the actor override flows here
}

// NewPYEngine resolves the pyright install and returns a StdioEngine
// configured for it.  The spawned process lives as long as ctx.
func NewPYEngine(ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte), cfg PYEngineConfig) (*PYEngine, error) {
	nodeBin, pkgDir, err := findPYInstall(cfg)
	if err != nil {
		return nil, err
	}
	cmd := util.Command(nodeBin, filepath.Join(pkgDir, "langserver.index.js"), "--stdio")
	cmd.Stderr = &stderrBridge{log: log, prefix: "lspserver: pyright stderr"}
	return newPYEngine(ctx, cmd, log, diagnostics)
}

// newPYEngine binds an already-built subprocess command as a PYEngine via the
// generic StdioEngine transport.  Tests use this entry point with a fake
// server binary.
func newPYEngine(ctx context.Context, cmd *exec.Cmd, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte)) (*PYEngine, error) {
	se, err := NewStdioEngine(ctx, cmd, log, diagnostics, StdioOptions{})
	if err != nil {
		return nil, err
	}
	return &PYEngine{StdioEngine: se}, nil
}

// --- Install detection -------------------------------------------------------

// findPYInstall probes for node and the pyright package directory.  Errors
// carry an actionable message.
func findPYInstall(cfg PYEngineConfig) (nodeBin, pkgDir string, err error) {
	nodeBin = cfg.NodePath
	if nodeBin == "" {
		nodeBin, err = exec.LookPath("node")
		if err != nil {
			return "", "", errors.New("lsp py: node not found on PATH; install Node.js >= 22.22.2 and run 'npm install -g pyright'")
		}
	}

	pkgDir, err = findPyrightPackage(nodeBin, cfg.PackagePath, resolveLSPRoot(cfg.ManagedRoot))
	if err != nil {
		return "", "", err
	}
	return nodeBin, pkgDir, nil
}

// findPyrightPackage locates the pyright package directory (the one containing
// langserver.index.js).  The search order is:
//
//  1. explicit cfg path
//  2. managed-install directory under <lspRoot>/pyright/<version>/
//  3. node-relative prefix: <nodeDir>/node_modules/pyright
//  4. global npm root: `<npm root -g>`/pyright
//  5. PATH shim: find `pyright` on PATH, then look next to it for node_modules/pyright
//  6. NODE_PATH entries
func findPyrightPackage(nodeBin, explicit, lspRoot string) (string, error) {
	var candidates []string
	add := func(p string) { candidates = append(candidates, p) }

	if explicit != "" {
		add(explicit)
	}

	// Managed install: <lspRoot>/pyright/<version>/
	if d := findManagedPyrightDir(lspRoot); d != "" {
		add(d)
	}

	// Next to the node binary (Windows global prefix install):
	// <prefix>/node_modules/pyright
	if dir := filepath.Dir(nodeBin); dir != "" && dir != "." {
		add(filepath.Join(dir, "node_modules", "pyright"))
	}
	// Global npm root.
	if root := findPyrightGlobalRoot(); root != "" {
		add(filepath.Join(root, "pyright"))
	}
	// PATH shim -> its own or parent node_modules.
	if shim, err := exec.LookPath("pyright"); err == nil {
		shimDir := filepath.Dir(shim)
		add(filepath.Join(shimDir, "node_modules", "pyright"))
		add(filepath.Join(filepath.Dir(shimDir), "node_modules", "pyright"))
	}
	// NODE_PATH entries.
	for _, p := range filepath.SplitList(os.Getenv("NODE_PATH")) {
		if p != "" {
			add(filepath.Join(p, "pyright"))
		}
	}

	for _, c := range candidates {
		js := filepath.Join(c, "langserver.index.js")
		if st, serr := os.Stat(js); serr == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", errors.New("lsp py: pyright package not found; install it with 'npm install -g pyright' (or set the managed install path in settings)")
}

// findManagedPyrightDir returns the latest managed-install pyright package
// directory, or empty if none exists.
// findManagedPyrightDir returns the latest managed-install pyright package
// directory under <lspRoot>/pyright, or empty if none exists.
func findManagedPyrightDir(lspRoot string) string {
	root := filepath.Join(lspRoot, "pyright")
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	if len(versions) == 0 {
		return ""
	}
	sort.Strings(versions)
	// Walk from newest to oldest.
	for i := len(versions) - 1; i >= 0; i-- {
		pkgDir := filepath.Join(root, versions[i])
		js := filepath.Join(pkgDir, "langserver.index.js")
		if st, serr := os.Stat(js); serr == nil && !st.IsDir() {
			return pkgDir
		}
	}
	return ""
}

// findPyrightGlobalRoot runs `npm root -g` once per engine construction.
// Pyright and TypeScript share the same npm global prefix, so we reuse the
// helper from the TypeScript engine.
func findPyrightGlobalRoot() string { return findTSGlobalRoot() }

// Compile-time check that PYEngine satisfies LanguageEngine.
var _ LanguageEngine = (*PYEngine)(nil)