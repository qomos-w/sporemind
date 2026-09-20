package lspserver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/util"
)

// RSEngine serves Rust through rust-analyzer, the Rust Language Server.
// It spawns the `rust-analyzer` binary (a single executable speaking LSP over
// stdio) directly.  Detection prefers a managed install under
// {dataDir}/lsp/rust-analyzer/<version>/, then a `rust-analyzer` binary on
// PATH.
type RSEngine struct {
	*StdioEngine
}

// RSEngineConfig carries optional install overrides.
type RSEngineConfig struct {
	BinaryPath  string // absolute path to the rust-analyzer executable
	ManagedRoot string // managed LSP root (default: <DataDir>/lsp); the actor override flows here
}

// NewRSEngine resolves the rust-analyzer binary and returns a StdioEngine
// configured for it.  The spawned process lives as long as ctx.
func NewRSEngine(ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte), cfg RSEngineConfig) (*RSEngine, error) {
	bin, err := findRSInstall(cfg)
	if err != nil {
		return nil, err
	}
	cmd := util.Command(bin, "--stdio")
	cmd.Stderr = &stderrBridge{log: log, prefix: "lspserver: rust-analyzer stderr"}
	return newRSEngine(ctx, cmd, log, diagnostics)
}

// newRSEngine binds an already-built subprocess command as an RSEngine via the
// generic StdioEngine transport.  Tests use this entry point with a fake
// server binary.
func newRSEngine(ctx context.Context, cmd *exec.Cmd, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte)) (*RSEngine, error) {
	se, err := NewStdioEngine(ctx, cmd, log, diagnostics, StdioOptions{})
	if err != nil {
		return nil, err
	}
	return &RSEngine{StdioEngine: se}, nil
}

// --- Install detection -------------------------------------------------------

// findRSInstall probes for the rust-analyzer executable.  Errors carry an
// actionable message.
func findRSInstall(cfg RSEngineConfig) (string, error) {
	if cfg.BinaryPath != "" {
		if st, err := os.Stat(cfg.BinaryPath); err == nil && !st.IsDir() {
			return cfg.BinaryPath, nil
		}
	}

	if bin := findManagedRustAnalyzer(resolveLSPRoot(cfg.ManagedRoot)); bin != "" {
		return bin, nil
	}

	bin, err := exec.LookPath("rust-analyzer")
	if err == nil {
		return bin, nil
	}

	return "", errors.New("lsp rust: rust-analyzer not found on PATH; install it via lsp.install or add it to PATH")
}

// findManagedRustAnalyzer returns the latest managed-install rust-analyzer
// binary under <lspRoot>/rust-analyzer, or empty if none exists.
func findManagedRustAnalyzer(lspRoot string) string {
	root := filepath.Join(lspRoot, "rust-analyzer")
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
	for i := len(versions) - 1; i >= 0; i-- {
		for _, name := range []string{"rust-analyzer", "rust-analyzer.exe"} {
			bin := filepath.Join(root, versions[i], name)
			if st, err := os.Stat(bin); err == nil && !st.IsDir() {
				return bin
			}
		}
	}
	return ""
}

// rustAnalyzerBinaryName returns the platform-specific binary name inside a
// rust-analyzer install directory.
func rustAnalyzerBinaryName() string {
	if runtime.GOOS == "windows" {
		return "rust-analyzer.exe"
	}
	return "rust-analyzer"
}

// Compile-time check that RSEngine satisfies LanguageEngine.
var _ LanguageEngine = (*RSEngine)(nil)