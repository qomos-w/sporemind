package lspserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/util"
)

// TSEngine serves TypeScript/JavaScript through typescript-language-server
// (the LSP wrapper around Microsoft's tsserver). All subprocess and JSON-RPC
// transport machinery lives in StdioEngine; TSEngine is the registry
// configuration: it resolves the node + cli.mjs install, builds the spawn
// command `node <cli.mjs> --stdio`, and pins tsserver-specific
// initializationOptions.
//
// Protocol notes (see workflow card "Research tsserver integration"):
//   - tls 6.x requires --stdio and no longer accepts the v4 flag set.
//   - tsserver projects load lazily; there is no warm-up request.
//   - tls emits textDocument/publishDiagnostics only when the client
//     advertises textDocument.publishDiagnostics (Initialize does).
//   - The process is spawned as `node <cli.mjs> --stdio` (not the .cmd shim)
//     so the handle is directly killable and no cmd.exe/PATHEXT fragility.
type TSEngine struct {
	*StdioEngine
}

// TSEngineConfig carries optional install overrides. Empty values fall back to
// the detection probes in findTSInstall: node & typescript-language-server on
// PATH, plus the global npm root and NODE_PATH. The managed-prefix install
// documented for the desktop app can be pinned here.
type TSEngineConfig struct {
	NodePath       string // node executable (default: exec.LookPath("node"))
	CLIPath        string // absolute path to typescript-language-server/lib/cli.mjs
	TypeScriptPath string // absolute path to the typescript package dir (tsserver.path pin)
	LogLevel       int    // tls --log-level 1..4; default 2 (warn); 4 = full log
	ManagedRoot    string // managed LSP root (default: <DataDir>/lsp); the actor override flows here
}

// NewTSEngine resolves the typescript-language-server install and returns a
// StdioEngine configured for it. The spawned process lives as long as ctx:
// when ctx ends, Shutdown is forced (the actor's lifecycle context).
// The diagnostics callback (may be nil) receives publishDiagnostics payloads
// as (uri, version, diagnosticsJSON) — the same contract as NewGoEngine.
func NewTSEngine(ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte), cfg TSEngineConfig) (*TSEngine, error) {
	nodeBin, cliPath, tsDir, err := findTSInstall(cfg)
	if err != nil {
		return nil, err
	}
	logLevel := cfg.LogLevel
	if logLevel < 1 || logLevel > 4 {
		logLevel = 2
	}
	cmd := util.Command(nodeBin, cliPath, "--stdio", "--log-level", strconv.Itoa(logLevel))
	cmd.Stderr = &stderrBridge{log: log, prefix: "lspserver: typescript-language-server stderr"}
	return newTSEngine(ctx, cmd, log, diagnostics, tsDir)
}

// newTSEngine binds an already-built subprocess command as a TSEngine via the
// generic StdioEngine transport. Tests use this entry point with a fake server
// binary that speaks LSP over stdio.
func newTSEngine(ctx context.Context, cmd *exec.Cmd, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte), tsDir string) (*TSEngine, error) {
	initOptions := map[string]any{}
	if tsDir != "" {
		initOptions["tsserver"] = map[string]any{"path": tsDir}
	}
	se, err := NewStdioEngine(ctx, cmd, log, diagnostics, StdioOptions{
		InitOptions: initOptions,
		OnNotification: func(log actor.Logger, msg *rpcMessage) {
			if msg.Method == "$/typescriptVersion" {
				var params struct {
					Version string `json:"version"`
					Source  string `json:"source"`
				}
				_ = json.Unmarshal(msg.Params, &params)
				log.Info("lspserver: tsserver version", "version", params.Version, "source", params.Source)
			} else {
				log.Debug("lspserver: ts engine notification", "method", msg.Method)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	return &TSEngine{StdioEngine: se}, nil
}

// --- Install detection -------------------------------------------------------

// findTSInstall probes for node and the typescript-language-server CLI entry
// point. Errors carry an actionable message (npm install hint) per the plan.
func findTSInstall(cfg TSEngineConfig) (nodeBin, cliPath, tsDir string, err error) {
	nodeBin = cfg.NodePath
	if nodeBin == "" {
		nodeBin, err = exec.LookPath("node")
		if err != nil {
			return "", "", "", errors.New("lsp ts: node not found on PATH; install Node.js >= 22.22.2 and run 'npm install -g typescript-language-server typescript@6'")
		}
	}

	cliPath, err = findTSCLI(nodeBin, cfg.CLIPath, resolveLSPRoot(cfg.ManagedRoot))
	if err != nil {
		return "", "", "", err
	}
	tsDir = findTypeScriptDir(cfg.TypeScriptPath, findTSGlobalRoot(), resolveLSPRoot(cfg.ManagedRoot))
	return nodeBin, cliPath, tsDir, nil
}

// findTSCLI locates lib/cli.mjs (the bundled v6 entry point). Windows shims
// are deliberately avoided: we always spawn `node <cli.mjs> --stdio`.
func findTSCLI(nodeBin, explicit, lspRoot string) (string, error) {
	var candidates []string
	add := func(p string) { candidates = append(candidates, p) }

	if explicit != "" {
		add(explicit)
	}
	// Managed install: <lspRoot>/typescript-language-server/<version>/
	if d := findManagedTSServerDir(lspRoot); d != "" {
		add(filepath.Join(d, "lib", "cli.mjs"))
	}
	// Next to the node binary (Windows global prefix install):
	// <prefix>/node_modules/typescript-language-server/lib/cli.mjs
	if dir := filepath.Dir(nodeBin); dir != "" && dir != "." {
		add(filepath.Join(dir, "node_modules", "typescript-language-server", "lib", "cli.mjs"))
	}
	// Global npm root.
	if root := findTSGlobalRoot(); root != "" {
		add(filepath.Join(root, "typescript-language-server", "lib", "cli.mjs"))
	}
	// PATH shim -> its own or parent node_modules.
	if shim, err := exec.LookPath("typescript-language-server"); err == nil {
		shimDir := filepath.Dir(shim)
		add(filepath.Join(shimDir, "node_modules", "typescript-language-server", "lib", "cli.mjs"))
		add(filepath.Join(filepath.Dir(shimDir), "node_modules", "typescript-language-server", "lib", "cli.mjs"))
	}
	// NODE_PATH entries.
	for _, p := range filepath.SplitList(os.Getenv("NODE_PATH")) {
		if p != "" {
			add(filepath.Join(p, "typescript-language-server", "lib", "cli.mjs"))
		}
	}

	for _, c := range candidates {
		if st, serr := os.Stat(c); serr == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", errors.New("lsp ts: typescript-language-server not found; install it with 'npm install -g typescript-language-server typescript@6' (or set the managed install path in settings)")
}

// findTSGlobalRoot runs `npm root -g` once per engine construction; empty on
// failure (npm absent) — detection still works via the node-relative probe.
func findTSGlobalRoot() string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := util.CommandContext(ctx, "npm", "root", "-g")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// findTypeScriptDir resolves an optional typescript package dir used to pin
// initializationOptions.tsserver.path. Empty means "let tls resolve" (its own
// node_modules next to the CLI, or the global install).
func findTypeScriptDir(explicit, globalRoot, lspRoot string) string {
	if explicit != "" {
		if _, err := os.Stat(filepath.Join(explicit, "package.json")); err == nil {
			return explicit
		}
	}
	// Managed companion install: typescript-language-server/<version>/typescript/
	if d := findManagedTSServerDir(lspRoot); d != "" {
		for _, candidate := range []string{
			filepath.Join(d, "typescript"),
			filepath.Join(d, "typescript", "lib"),
		} {
			if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
				return candidate
			}
			if _, err := os.Stat(filepath.Join(candidate, "tsserver.js")); err == nil {
				return candidate
			}
		}
	}
	if globalRoot == "" {
		return ""
	}
	for _, candidate := range []string{
		filepath.Join(globalRoot, "typescript"),
		filepath.Join(globalRoot, "typescript", "lib"),
	} {
		// The pin accepts a package dir; lib/ contains tsserver.js directly.
		if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
			return candidate
		}
		if _, err := os.Stat(filepath.Join(candidate, "tsserver.js")); err == nil {
			return candidate
		}
	}
	return ""
}

// Compile-time check that TSEngine satisfies LanguageEngine (promoted from
// the embedded StdioEngine).
var _ LanguageEngine = (*TSEngine)(nil)
