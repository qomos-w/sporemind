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

// ClangdEngine serves C/C++ (plus Objective-C/C++ headers, which clangd
// routes itself) through clangd, the LLVM language server. It spawns the
// `clangd` binary (a single executable speaking LSP over stdio) directly.
// Detection prefers an existing `clangd` on PATH — LLVM installations and VS
// Code extensions commonly provide one matched to the user's toolchain — then
// falls back to a managed install under {dataDir}/lsp/clangd/<version>/.
type ClangdEngine struct {
	*StdioEngine
}

// ClangdEngineConfig carries optional install overrides.
type ClangdEngineConfig struct {
	BinaryPath  string // absolute path to the clangd executable
	ManagedRoot string // managed LSP root (default: <DataDir>/lsp); the actor override flows here
}

// NewClangdEngine resolves the clangd binary and returns a StdioEngine
// configured for it. --background-index enables cross-file indexing and
// --clang-tidy turns on lint diagnostics; both are stable flags across every
// supported clangd release. The spawned process lives as long as ctx.
func NewClangdEngine(ctx context.Context, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte), cfg ClangdEngineConfig) (*ClangdEngine, error) {
	bin, err := findClangdInstall(cfg)
	if err != nil {
		return nil, err
	}
	cmd := util.Command(bin, "--background-index", "--clang-tidy")
	cmd.Stderr = &stderrBridge{log: log, prefix: "lspserver: clangd stderr"}
	return newClangdEngine(ctx, cmd, log, diagnostics)
}

// newClangdEngine binds an already-built subprocess command as a ClangdEngine
// via the generic StdioEngine transport. Tests use this entry point with a
// fake server binary.
func newClangdEngine(ctx context.Context, cmd *exec.Cmd, log actor.Logger, diagnostics func(uri string, version int32, diagnosticsJSON []byte)) (*ClangdEngine, error) {
	se, err := NewStdioEngine(ctx, cmd, log, diagnostics, StdioOptions{})
	if err != nil {
		return nil, err
	}
	return &ClangdEngine{StdioEngine: se}, nil
}

// --- Install detection -------------------------------------------------------

// findClangdInstall probes for the clangd executable: explicit override, then
// PATH, then the managed install directory. Errors carry an actionable
// message.
func findClangdInstall(cfg ClangdEngineConfig) (string, error) {
	if cfg.BinaryPath != "" {
		if st, err := os.Stat(cfg.BinaryPath); err == nil && !st.IsDir() {
			return cfg.BinaryPath, nil
		}
	}

	if bin, err := exec.LookPath("clangd"); err == nil {
		return bin, nil
	}

	if bin := findManagedClangd(resolveLSPRoot(cfg.ManagedRoot)); bin != "" {
		return bin, nil
	}

	return "", errors.New("lsp cpp: clangd not found on PATH; install it via lsp.install or add it to PATH")
}

// findManagedClangd returns the latest managed-install clangd binary under
// <lspRoot>/clangd, or empty if none exists. Release archives unpack to
// bin/clangd(.exe) below the version directory; a binary sitting directly in
// the version dir is accepted defensively so future layout changes cannot
// orphan an installed tool.
func findManagedClangd(lspRoot string) string {
	root := filepath.Join(lspRoot, "clangd")
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
		for _, sub := range []string{"bin", ""} {
			bin := filepath.Join(root, versions[i], sub, clangdBinaryName())
			if st, err := os.Stat(bin); err == nil && !st.IsDir() {
				return bin
			}
		}
	}
	return ""
}

// clangdBinaryName returns the platform-specific binary name inside a clangd
// install directory.
func clangdBinaryName() string {
	if runtime.GOOS == "windows" {
		return "clangd.exe"
	}
	return "clangd"
}

// Compile-time check that ClangdEngine satisfies LanguageEngine.
var _ LanguageEngine = (*ClangdEngine)(nil)
