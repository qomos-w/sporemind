package lspserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/util"
)

// WEBEngine serves CSS/SCSS/Less, HTML, and JSON through the
// vscode-langservers-extracted npm package (language servers extracted from
// VS Code). The package ships one entry script per language under bin/; this
// wrapper resolves the shared package directory once and spawns the relevant
// entry with `node <pkgDir>/bin/<server> --stdio`. The bundled ESLint server
// is not registered.
type WEBEngine struct {
	*StdioEngine
}

// WEBEngineConfig carries optional install overrides. Empty values fall back
// to the same npm detection cascade used by the Python engine: node on PATH,
// then the package via node-relative prefix, global npm root, PATH shim,
// NODE_PATH, plus the managed-install directory.
type WEBEngineConfig struct {
	NodePath    string // node executable (default: exec.LookPath("node"))
	PackagePath string // absolute path to the vscode-langservers-extracted package dir
	Server      string // entry script under the package dir; required for spawning
	ManagedRoot string // managed LSP root (default: <DataDir>/lsp); the actor override flows here
}

// Entry scripts inside the vscode-langservers-extracted package.
const (
	webCSSEntry  = "bin/vscode-css-language-server"
	webHTMLEntry = "bin/vscode-html-language-server"
	webJSONEntry = "bin/vscode-json-language-server"
)

// NewCSSEngine resolves the CSS language server and returns a WEBEngine for
// it. The spawned process lives as long as ctx.
func NewCSSEngine(ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte), cfg WEBEngineConfig) (*WEBEngine, error) {
	cfg.Server = webCSSEntry
	return newWebEngineFor(ctx, log, diagnostics, cfg)
}

// NewHTMLEngine resolves the HTML language server and returns a WEBEngine for
// it. The spawned process lives as long as ctx.
func NewHTMLEngine(ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte), cfg WEBEngineConfig) (*WEBEngine, error) {
	cfg.Server = webHTMLEntry
	return newWebEngineFor(ctx, log, diagnostics, cfg)
}

// NewJSONEngine resolves the JSON language server and returns a WEBEngine for
// it. The spawned process lives as long as ctx.
func NewJSONEngine(ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte), cfg WEBEngineConfig) (*WEBEngine, error) {
	cfg.Server = webJSONEntry
	return newWebEngineFor(ctx, log, diagnostics, cfg)
}

// newWebEngineFor is the shared constructor: resolves the package install,
// builds the `node <pkgDir>/<server> --stdio` command and binds it through the
// generic StdioEngine transport.
func newWebEngineFor(ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte), cfg WEBEngineConfig) (*WEBEngine, error) {
	nodeBin, pkgDir, err := findWEBInstall(cfg)
	if err != nil {
		return nil, err
	}
	cmd := util.Command(nodeBin, filepath.Join(pkgDir, filepath.FromSlash(cfg.Server)), "--stdio")
	cmd.Stderr = &stderrBridge{log: log, prefix: fmt.Sprintf("lspserver: %s language server stderr", webServerName(cfg.Server))}
	return newWEBEngine(ctx, cmd, log, diagnostics)
}

// webServerName returns a short display name for stderr prefixes.
func webServerName(server string) string {
	switch server {
	case webCSSEntry:
		return "css"
	case webHTMLEntry:
		return "html"
	case webJSONEntry:
		return "json"
	default:
		return "web"
	}
}

// newWEBEngine binds an already-built subprocess command as a WEBEngine via
// the generic StdioEngine transport. Tests use this entry point with a fake
// server binary.
func newWEBEngine(ctx context.Context, cmd *exec.Cmd, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte)) (*WEBEngine, error) {
	se, err := NewStdioEngine(ctx, cmd, log, diagnostics, StdioOptions{})
	if err != nil {
		return nil, err
	}
	return &WEBEngine{StdioEngine: se}, nil
}

// --- Install detection -------------------------------------------------------

// findWEBInstall probes for node and the vscode-langservers-extracted package
// directory containing cfg.Server. Errors carry an actionable message.
func findWEBInstall(cfg WEBEngineConfig) (nodeBin, pkgDir string, err error) {
	if cfg.Server == "" {
		return "", "", errors.New("lsp web: server entry is required")
	}
	nodeBin = cfg.NodePath
	if nodeBin == "" {
		nodeBin, err = exec.LookPath("node")
		if err != nil {
			return "", "", errors.New("lsp web: node not found on PATH; install Node.js >= 22.22.2 and run 'npm install -g vscode-langservers-extracted'")
		}
	}

	pkgDir, err = findNPMPackage(nodeBin, "vscode-langservers-extracted", cfg.Server, cfg.PackagePath, filepath.Join(resolveLSPRoot(cfg.ManagedRoot), "vscode-langservers-extracted"))
	if err != nil {
		return "", "", err
	}
	return nodeBin, pkgDir, nil
}

// --- Shared npm detection (reuses the pyright cascade) ----------------------

// findNPMPackage locates an npm package directory containing the entry script
// entryRel (a slash-separated path relative to the package dir). The search
// order mirrors findPyrightPackage:
//
//  1. explicit cfg path
//  2. managed-install directory under {dataDir}/lsp/<pkg>/<version>/
//  3. node-relative prefix: <nodeDir>/node_modules/<pkg>
//  4. global npm root: `<npm root -g>`/<pkg>
//  5. PATH shim: find `<pkg>` on PATH, then look next to it for node_modules/<pkg>
//  6. NODE_PATH entries
func findNPMPackage(nodeBin, pkgName, entryRel, explicit, managedRoot string) (string, error) {
	var candidates []string
	add := func(p string) { candidates = append(candidates, p) }

	if explicit != "" {
		add(explicit)
	}

	// Managed install: {dataDir}/lsp/<pkg>/<version>/
	if managedRoot != "" {
		if d := findManagedNPMDir(managedRoot, entryRel); d != "" {
			add(d)
		}
	}

	// Next to the node binary (Windows global prefix install):
	// <prefix>/node_modules/<pkg>
	if dir := filepath.Dir(nodeBin); dir != "" && dir != "." {
		add(filepath.Join(dir, "node_modules", pkgName))
	}
	// Global npm root.
	if root := findTSGlobalRoot(); root != "" {
		add(filepath.Join(root, pkgName))
	}
	// PATH shim -> its own or parent node_modules.
	if shim, err := exec.LookPath(pkgName); err == nil {
		shimDir := filepath.Dir(shim)
		add(filepath.Join(shimDir, "node_modules", pkgName))
		add(filepath.Join(filepath.Dir(shimDir), "node_modules", pkgName))
	}
	// NODE_PATH entries.
	for _, p := range filepath.SplitList(os.Getenv("NODE_PATH")) {
		if p != "" {
			add(filepath.Join(p, pkgName))
		}
	}

	for _, c := range candidates {
		entry := filepath.Join(c, filepath.FromSlash(entryRel))
		if st, serr := os.Stat(entry); serr == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("lsp %s: %s package not found; install it with 'npm install -g %s' (or use lsp.install)", pkgName, pkgName, pkgName)
}

// findManagedNPMDir returns the newest managed-install npm package directory
// under root whose entryRel file exists, or empty if none exists.
func findManagedNPMDir(root, entryRel string) string {
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
		entry := filepath.Join(pkgDir, filepath.FromSlash(entryRel))
		if st, err := os.Stat(entry); err == nil && !st.IsDir() {
			return pkgDir
		}
	}
	return ""
}

// Compile-time check that WEBEngine satisfies LanguageEngine.
var _ LanguageEngine = (*WEBEngine)(nil)
