package lspserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/lspserver/installer"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/util"
)

// installProgressEvent is the event kind streaming managed-install progress.
// Declared in schemas/lsp._3300.spore as LspInstallProgressEvent (schema 3321).
const installProgressEvent = "lsp.install_progress"

// Install progress states per the LspInstallProgressEvent contract:
// "running" while Percent advances, then "done" or "failed".
const (
	installStateRunning = "running"
	installStateDone    = "done"
	installStateFailed  = "failed"
)

// DownloadSource values reported by lsp.status.
const (
	sourcePath    = "path"    // discovered on PATH / global npm root
	sourceManaged = "managed" // installed into the managed data dir
)

// goplsVersion is the gopls release pinned for the managed `go install`.
const goplsVersion = "v0.23.0"

// languageSpec describes how to probe one language's server availability and,
// optionally, how to managed-install it. Languages without a manifest or
// goInstall report lsp.install errors instead of silently doing nothing; the
// probe is always required so lsp.status can answer for every known language.
type languageSpec struct {
	probe func() gen.LspLanguageInstallState
	// manifest returns the installer manifest for a managed download. nil
	// means the language installs through another mechanism (or not at all);
	// lsp.install rejects it with an actionable error.
	manifest func() (installer.Manifest, error)
	// deps lists companion package manifests installed under the primary
	// install dir (e.g. typescript next to typescript-language-server).
	deps []func() (installer.Manifest, error)
	// goInstall, when set, managed-installs the server with the Go toolchain
	// (`go install module@version`) instead of the HTTP manifest downloader.
	goInstall *goInstallSpec
}

// goInstallSpec pins a toolchain-managed install: `go install module@version`
// places the binary in GOBIN (default $(go env GOPATH)/bin), which the engine
// and probe resolve without any local directory layout.
type goInstallSpec struct {
	Tool    string // tool name for install records and status ("gopls")
	Module  string // module path passed to go install
	Version string // pinned version
}

// defaultLanguageSpecs seeds the builtin languages. Downstream engines add
// their entries here or via Actor.setLanguageSpec before OnStart.
func defaultLanguageSpecs() map[string]languageSpec {
	return map[string]languageSpec{
		"go": {probe: probeGoInstall, goInstall: &goInstallSpec{Tool: "gopls", Module: "golang.org/x/tools/gopls", Version: goplsVersion}},
		"typescript": {probe: probeTSInstall, manifest: tsServerManifest, deps: []func() (installer.Manifest, error){typescriptPackageManifest}},
		"javascript": {probe: probeTSInstall, manifest: tsServerManifest, deps: []func() (installer.Manifest, error){typescriptPackageManifest}},
		"python":     {probe: probePYInstall, manifest: pyrightManifest},
		"rust":       {probe: probeRSInstall, manifest: rustAnalyzerManifest},
		"cpp":        {probe: probeCPPInstall, manifest: clangdManifest},
		"css":        {probe: probeCSSInstall, manifest: webManifest},
		"html":       {probe: probeHTMLInstall, manifest: webManifest},
		"json":       {probe: probeJSONInstall, manifest: webManifest},
		"bash":       {probe: probeBashInstall, manifest: bashManifest},
	}
}

// probeGoInstall checks for the gopls binary on PATH, then in the Go
// toolchain's install dir (GOBIN / $(go env GOPATH)/bin). Missing gopls is
// Installed=false; lsp.install covers it with `go install golang.org/x/tools/gopls@version`.
// Returns Installed=false (never an error).
func probeGoInstall() gen.LspLanguageInstallState {
	bin, err := findGoplsInstall()
	if err != nil {
		return gen.LspLanguageInstallState{}
	}
	return gen.LspLanguageInstallState{Installed: true, BinaryPath: bin, DownloadSource: sourcePath}
}

// probeTSInstall checks for typescript-language-server: managed install
// first, then the native node-based detection cascade. Any error — missing
// node OR missing typescript-language-server — means "not installed", which
// lsp.status must surface as Installed=false rather than an error.
func probeTSInstall() gen.LspLanguageInstallState {
	if d := findManagedTSServerDir(defaultLSPRoot()); d != "" {
		return gen.LspLanguageInstallState{Installed: true, BinaryPath: filepath.Join(d, "lib", "cli.mjs"), DownloadSource: sourceManaged}
	}
	_, cliPath, _, err := findTSInstall(TSEngineConfig{})
	if err != nil {
		return gen.LspLanguageInstallState{}
	}
	// Version detection would require spawning `tls --version`; the field is
	// optional and left empty until a cheap source exists.
	return gen.LspLanguageInstallState{Installed: true, BinaryPath: cliPath, DownloadSource: sourcePath}
}

// findManagedTSServerDir returns the latest managed typescript-language-server
// package directory under lspRoot, or empty if none exists.
func findManagedTSServerDir(lspRoot string) string {
	root := filepath.Join(lspRoot, "typescript-language-server")
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
		pkgDir := filepath.Join(root, versions[i])
		js := filepath.Join(pkgDir, "lib", "cli.mjs")
		if st, serr := os.Stat(js); serr == nil && !st.IsDir() {
			return pkgDir
		}
	}
	return ""
}

// probePYInstall checks for pyright: managed install first, then the native
// node-based detection cascade.  Returns Installed=false (never an error).
func probePYInstall() gen.LspLanguageInstallState {
	if d := findManagedPyrightDir(defaultLSPRoot()); d != "" {
		return gen.LspLanguageInstallState{Installed: true, BinaryPath: d, DownloadSource: sourceManaged}
	}
	_, pkgDir, err := findPYInstall(PYEngineConfig{})
	if err != nil {
		return gen.LspLanguageInstallState{}
	}
	return gen.LspLanguageInstallState{Installed: true, BinaryPath: pkgDir, DownloadSource: sourcePath}
}

// probeRSInstall checks for rust-analyzer: managed install first, then PATH.
// Returns Installed=false (never an error).
func probeRSInstall() gen.LspLanguageInstallState {
	if bin := findManagedRustAnalyzer(defaultLSPRoot()); bin != "" {
		return gen.LspLanguageInstallState{Installed: true, BinaryPath: bin, DownloadSource: sourceManaged}
	}
	if bin, err := exec.LookPath("rust-analyzer"); err == nil {
		return gen.LspLanguageInstallState{Installed: true, BinaryPath: bin, DownloadSource: sourcePath}
	}
	return gen.LspLanguageInstallState{}
}

// probeCPPInstall checks for clangd: PATH first (LLVM installations and VS
// Code extensions commonly ship a clangd matched to the user's toolchain),
// then the managed install directory. Returns Installed=false (never an
// error).
func probeCPPInstall() gen.LspLanguageInstallState {
	if bin, err := exec.LookPath("clangd"); err == nil {
		return gen.LspLanguageInstallState{Installed: true, BinaryPath: bin, DownloadSource: sourcePath}
	}
	if bin := findManagedClangd(defaultLSPRoot()); bin != "" {
		return gen.LspLanguageInstallState{Installed: true, BinaryPath: bin, DownloadSource: sourceManaged}
	}
	return gen.LspLanguageInstallState{}
}

// probeCSSInstall checks for the CSS/SCSS/Less language server (shipped in
// vscode-langservers-extracted): managed install first, then the npm cascade.
// Returns Installed=false (never an error).
func probeCSSInstall() gen.LspLanguageInstallState {
	return probeWEBInstall(webCSSEntry)
}

// probeHTMLInstall checks for the HTML language server (shipped in
// vscode-langservers-extracted): managed install first, then the npm cascade.
// Returns Installed=false (never an error).
func probeHTMLInstall() gen.LspLanguageInstallState {
	return probeWEBInstall(webHTMLEntry)
}

// probeJSONInstall checks for the JSON language server (shipped in
// vscode-langservers-extracted): managed install first, then the npm cascade.
// Returns Installed=false (never an error).
func probeJSONInstall() gen.LspLanguageInstallState {
	return probeWEBInstall(webJSONEntry)
}

// probeWEBInstall resolves one vscode-langservers-extracted language server.
func probeWEBInstall(entry string) gen.LspLanguageInstallState {
	managedRoot := filepath.Join(config.DataDir(), "lsp", "vscode-langservers-extracted")
	if d := findManagedNPMDir(managedRoot, entry); d != "" {
		return gen.LspLanguageInstallState{Installed: true, BinaryPath: filepath.Join(d, filepath.FromSlash(entry)), DownloadSource: sourceManaged}
	}
	_, pkgDir, err := findWEBInstall(WEBEngineConfig{Server: entry})
	if err != nil {
		return gen.LspLanguageInstallState{}
	}
	return gen.LspLanguageInstallState{Installed: true, BinaryPath: filepath.Join(pkgDir, filepath.FromSlash(entry)), DownloadSource: sourcePath}
}

// probeBashInstall checks for bash-language-server: managed install first, then
// the npm cascade. Returns Installed=false (never an error).
func probeBashInstall() gen.LspLanguageInstallState {
	managedRoot := filepath.Join(config.DataDir(), "lsp", "bash-language-server")
	if d := findManagedNPMDir(managedRoot, bashEntry); d != "" {
		return gen.LspLanguageInstallState{Installed: true, BinaryPath: filepath.Join(d, filepath.FromSlash(bashEntry)), DownloadSource: sourceManaged}
	}
	_, pkgDir, err := findBashInstall(BashEngineConfig{})
	if err != nil {
		return gen.LspLanguageInstallState{}
	}
	return gen.LspLanguageInstallState{Installed: true, BinaryPath: filepath.Join(pkgDir, filepath.FromSlash(bashEntry)), DownloadSource: sourcePath}
}

// managedInstall is one completed managed install, persisted via pkg/persist
// so lsp.status can resolve managed installs across restarts without probing
// the filesystem layout from scratch. Dir/BinaryPath are absolute. Asset keeps
// the platform-selected descriptor used at install time so the record can be
// re-validated against the on-disk marker (installer.InstalledVersion requires
// a fully valid manifest).
type managedInstall struct {
	Tool        string          `json:"tool"`
	Version     string          `json:"version"`
	Dir         string          `json:"dir"`
	BinaryPath  string          `json:"binary_path,omitempty"`
	InstalledAt string          `json:"installed_at"` // RFC 3339 UTC
	Asset       installer.Asset `json:"asset"`
}

// manifest rebuilds a validation-ready manifest from the record.
func (r managedInstall) manifest() installer.Manifest {
	return toolManifest(r.Tool, r.Version, []installer.Asset{r.Asset})
}

// toolManifest builds a Manifest following the managed-directory convention
// {managedRoot}/{tool}/{version}/: every version installs into its own
// directory, so downgrades/upgrades never clobber a running install in place.
func toolManifest(tool, version string, assets []installer.Asset) installer.Manifest {
	return installer.Manifest{
		Tool:       tool,
		Version:    version,
		Assets:     assets,
		InstallDir: tool + "/" + version,
	}
}

// specFor returns the language spec, falling back to the builtin set when the
// actor was constructed without OnStart (unit tests). Read-only: it never
// mutates the map, so concurrent status handlers stay race-free.
func (a *Actor) specFor(lang string) (languageSpec, bool) {
	specs := a.specs
	if specs == nil {
		specs = defaultLanguageSpecs()
	}
	s, ok := specs[lang]
	return s, ok
}

// setLanguageSpec registers or replaces the spec for one language. Intended
// for engine wiring before OnStart; not safe for concurrent use with running
// handlers.
func (a *Actor) setLanguageSpec(lang string, s languageSpec) {
	if a.specs == nil {
		a.specs = defaultLanguageSpecs()
	}
	a.specs[lang] = s
}

// managedRoot returns the root directory for managed tool installs:
// {dataDir}/lsp. managedRootOverride exists purely for tests.
func (a *Actor) managedRoot() string {
	if a.managedRootOverride != "" {
		return a.managedRootOverride
	}
	return defaultLSPRoot()
}

// defaultLSPRoot is the conventional managed install root used by package
// level probes (no actor receiver): {dataDir}/lsp.
func defaultLSPRoot() string {
	return filepath.Join(config.DataDir(), "lsp")
}

// resolveLSPRoot resolves an engine config's ManagedRoot: empty falls back to
// the conventional root so package-level probes and engine lookups agree.
func resolveLSPRoot(managedRoot string) string {
	if strings.TrimSpace(managedRoot) == "" {
		return defaultLSPRoot()
	}
	return managedRoot
}

// --- lsp.status ---

// handleStatus reports the per-language install projection. It is stateless
// (PureContext): managed installs come from the persist-backed cache (verified
// against the on-disk install marker), everything else falls back to the
// native probe. A language that is simply not installed yields
// Installed=false — never an error.
func (a *Actor) handleStatus(_ actor.PureContext, req gen.LspStatusReq) (gen.LspStatusResp, error) {
	langs := knownLanguages
	if req.Language != "" {
		found := false
		for _, l := range knownLanguages {
			if l == req.Language {
				found = true
				break
			}
		}
		if !found {
			return gen.LspStatusResp{}, fmt.Errorf("lsp.status: unsupported language %q", req.Language)
		}
		langs = []string{req.Language}
	}

	out := make([]gen.LspLanguageInstallState, 0, len(langs))
	for _, lang := range langs {
		out = append(out, a.installStateFor(lang))
	}
	return gen.LspStatusResp{Languages: out}, nil
}

// installStateFor resolves the install state for one language: a verified
// managed install wins over the native discovery probe.
func (a *Actor) installStateFor(lang string) gen.LspLanguageInstallState {
	if st, ok := a.managedStateFor(lang); ok {
		return st
	}
	st := gen.LspLanguageInstallState{}
	if spec, ok := a.specFor(lang); ok && spec.probe != nil {
		st = spec.probe()
	}
	st.Language = lang
	return st
}

// managedStateFor verifies the cached managed-install record against the
// on-disk marker. A record whose directory or marker vanished (manual delete,
// data-dir move) is ignored, letting the native probe answer instead.
func (a *Actor) managedStateFor(lang string) (gen.LspLanguageInstallState, bool) {
	a.installMu.RLock()
	rec, ok := a.installs[lang]
	a.installMu.RUnlock()
	if !ok {
		return gen.LspLanguageInstallState{}, false
	}
	if rec.Asset.URL == "" && rec.BinaryPath != "" {
		// Toolchain-managed installs (`go install`) carry no HTTP asset; the
		// recorded binary on disk is the verification.
		if st, err := os.Stat(rec.BinaryPath); err == nil && !st.IsDir() {
			return gen.LspLanguageInstallState{
				Language:       lang,
				Installed:      true,
				Version:        rec.Version,
				BinaryPath:     rec.BinaryPath,
				DownloadSource: sourceManaged,
			}, true
		}
		return gen.LspLanguageInstallState{}, false
	}
	m := rec.manifest()
	version, found, err := installer.InstalledVersion(a.managedRoot(), m)
	if err != nil || !found || version != rec.Version {
		return gen.LspLanguageInstallState{}, false
	}
	return gen.LspLanguageInstallState{
		Language:       lang,
		Installed:      true,
		Version:        rec.Version,
		BinaryPath:     rec.BinaryPath,
		DownloadSource: sourceManaged,
	}, true
}

// --- lsp.install ---

// handleInstall acknowledges an async managed download. The callable returns
// immediately (Started=true); progress streams via lsp.install_progress and
// the final state lands in the persist cache for lsp.status polling.
// Duplicate triggers while an install is already running are acked
// idempotently without spawning a second download. Stateful (actor.Context):
// inFlight bookkeeping is serialized through the mailbox.
func (a *Actor) handleInstall(ctx actor.Context, req gen.LspInstallReq) (gen.LspInstallResp, error) {
	if req.Language == "" {
		return gen.LspInstallResp{}, errors.New("lsp.install: language is required")
	}
	lang := req.Language
	spec, ok := a.specFor(lang)
	if !ok || (spec.manifest == nil && spec.goInstall == nil) {
		return gen.LspInstallResp{}, fmt.Errorf("lsp.install: no managed install configured for language %q", lang)
	}
	var m installer.Manifest
	if spec.manifest != nil {
		var err error
		if m, err = spec.manifest(); err != nil {
			return gen.LspInstallResp{}, fmt.Errorf("lsp.install: %w", err)
		}
	}

	a.installMu.Lock()
	if a.inFlight[lang] {
		a.installMu.Unlock()
		return gen.LspInstallResp{Started: true}, nil
	}
	if a.inFlight == nil {
		a.inFlight = make(map[string]bool)
	}
	a.inFlight[lang] = true
	a.installMu.Unlock()

	lifecycle := ctx.Lifecycle()
	log := ctx.Logger()
	go func() {
		defer func() {
			a.installMu.Lock()
			delete(a.inFlight, lang)
			a.installMu.Unlock()
		}()
		if spec.goInstall != nil {
			a.runGoInstall(lifecycle, log, lang, spec.goInstall)
			return
		}
		deps := make([]installer.Manifest, 0, len(spec.deps))
		for _, dep := range spec.deps {
			if dm, derr := dep(); derr == nil {
				deps = append(deps, dm)
			}
		}
		a.runInstall(lifecycle, log, lang, m, deps...)
	}()
	return gen.LspInstallResp{Started: true}, nil
}

// runInstall performs the download-extract-publish cycle for one language,
// emitting progress events and persisting the resulting record. Runs on its
// own goroutine; ctx is the actor lifecycle, so stopping the actor cancels an
// in-flight download.
func (a *Actor) runInstall(ctx context.Context, log actor.Logger, lang string, m installer.Manifest, deps ...installer.Manifest) {
	a.emitInstallProgress(lang, installStateRunning, 0, "")

	res, err := installer.Ensure(ctx, a.managedRoot(), m, installer.Options{
		Progress: a.progressSink(lang),
	})
	if err != nil {
		log.Error("lspserver: managed install failed", "language", lang, "tool", m.Tool, "error", err)
		a.emitInstallProgress(lang, installStateFailed, 0, err.Error())
		return
	}
	// Companion packages installed under the primary install dir (e.g. the
	// typescript package next to typescript-language-server so tls can pin
	// tsserver without a global npm install). A missing companion leaves the
	// server unable to start (no bundled fallback exists), so failures fail
	// the whole install instead of degrading silently.
	for _, dep := range deps {
		depDir := filepath.Join(res.Dir, dep.Tool)
		depManifest := dep
		depManifest.InstallDir = m.InstallDir + "/" + dep.Tool
		if _, derr := installer.Ensure(ctx, a.managedRoot(), depManifest, installer.Options{}); derr != nil {
			log.Error("lspserver: managed install failed", "language", lang, "tool", dep.Tool, "error", derr)
			a.emitInstallProgress(lang, installStateFailed, 0, fmt.Sprintf("%s: %v", dep.Tool, derr))
			return
		} else {
			log.Info("lspserver: companion install complete", "language", lang, "tool", dep.Tool, "dir", depDir)
		}
	}

	// Capture the platform-selected asset for the persisted record so
	// managedStateFor can reconstruct a validation-ready manifest.
	selectedAsset := installer.Asset{}
	if sel, selErr := m.Select(runtime.GOOS, runtime.GOARCH); selErr == nil {
		selectedAsset = sel
	}

	rec := managedInstall{
		Tool:        m.Tool,
		Version:     m.Version,
		Dir:         res.Dir,
		BinaryPath:  managedBinaryPath(m, res.Dir),
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Asset:       selectedAsset,
	}
	if rec.BinaryPath == "" {
		// Archive-style installs (npm tarball, GitHub release tarball/zip)
		// have a tool-specific entry layout; resolve it so lsp.status can
		// report a concrete binary path.
		rec.BinaryPath = managedArchiveEntryPath(lang, res.Dir)
	}
	a.finishManagedInstall(log, lang, rec)
}

// runGoInstall performs the toolchain-managed install for one language
// (`go install module@version`), emitting progress events and persisting the
// resulting record. Runs on its own goroutine; ctx is the actor lifecycle.
func (a *Actor) runGoInstall(ctx context.Context, log actor.Logger, lang string, spec *goInstallSpec) {
	a.emitInstallProgress(lang, installStateRunning, 0, "")

	bin, err := goInstallTool(ctx, spec)
	if err != nil {
		log.Error("lspserver: managed go install failed", "language", lang, "tool", spec.Tool, "error", err)
		a.emitInstallProgress(lang, installStateFailed, 0, err.Error())
		return
	}

	a.finishManagedInstall(log, lang, managedInstall{
		Tool:        spec.Tool,
		Version:     spec.Version,
		Dir:         filepath.Dir(bin),
		BinaryPath:  bin,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// goInstallTool runs `go install module@version` with the user's Go toolchain
// and returns the installed binary path (GOBIN / $(go env GOPATH)/bin).
func goInstallTool(ctx context.Context, spec *goInstallSpec) (string, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("go toolchain not found on PATH; cannot install %s@%s", spec.Module, spec.Version)
	}
	out, err := util.CommandContext(ctx, goBin, "install", spec.Module+"@"+spec.Version).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if len(detail) > 512 {
			detail = detail[:512] + "...(truncated)"
		}
		return "", fmt.Errorf("go install %s@%s: %v: %s", spec.Module, spec.Version, err, detail)
	}
	bin, err := findGoplsInstall()
	if err != nil {
		return "", fmt.Errorf("go install %s@%s succeeded but binary not found: %w", spec.Module, spec.Version, err)
	}
	return bin, nil
}

// finishManagedInstall records a completed managed install, persists it, shuts
// down any cached engine for the language so the next lsp.initialize picks up
// the freshly installed binary, and emits the done event.
func (a *Actor) finishManagedInstall(log actor.Logger, lang string, rec managedInstall) {
	a.installMu.Lock()
	if a.installs == nil {
		a.installs = make(map[string]managedInstall)
	}
	a.installs[lang] = rec
	a.installMu.Unlock()
	if err := a.Save(); err != nil {
		log.Error("lspserver: managed install state persist failed", "language", lang, "error", err)
	}

	log.Info("lspserver: managed install complete", "language", lang, "tool", rec.Tool, "version", rec.Version, "dir", rec.Dir)

	// Shut down any existing engine for this language so the next
	// lsp.initialize picks up the freshly installed binary. Without this
	// a pre-install failure (or a stale on-PATH engine) stays cached and
	// the new managed install is never used.
	a.mu.Lock()
	for rootUri, engines := range a.servers {
		if rs, ok := engines[lang]; ok {
			delete(engines, lang)
			if len(engines) == 0 {
				delete(a.servers, rootUri)
			}
			go rs.server.Shutdown(context.Background())
		}
	}
	a.mu.Unlock()

	a.emitInstallProgress(lang, installStateDone, 100, "")
}

// managedBinaryPath best-effort resolves the installed binary location: raw
// artifacts keep their URL base name (query stripped) inside the published
// dir; archive layouts vary, so no path is claimed there.
func managedBinaryPath(m installer.Manifest, dir string) string {
	asset, err := m.Select(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return ""
	}
	if asset.Kind != installer.KindRaw {
		return ""
	}
	base := path.Base(asset.URL)
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base = base[:i]
	}
	if base == "" || base == "." || base == "/" {
		return ""
	}
	return filepath.Join(dir, base)
}

// managedArchiveEntryPath resolves the conventional binary/entry point inside
// an archive-style managed install directory.  Returns empty when the expected
// file does not exist (e.g. the directory has not been populated yet or the
// tool is not standalone).
func managedArchiveEntryPath(lang, dir string) string {
	switch lang {
	case "python":
		js := filepath.Join(dir, "langserver.index.js")
		if st, err := os.Stat(js); err == nil && !st.IsDir() {
			return dir
		}
	case "rust":
		for _, name := range []string{"rust-analyzer", "rust-analyzer.exe"} {
			bin := filepath.Join(dir, name)
			if st, err := os.Stat(bin); err == nil && !st.IsDir() {
				return bin
			}
		}
	case "cpp":
		// clangd release zips unpack with the binary under bin/ (after
		// StripTopLevel removes the clangd_<version>/ wrapper); accept a
		// root-level binary defensively.
		for _, rel := range []string{filepath.Join("bin", clangdBinaryName()), clangdBinaryName()} {
			bin := filepath.Join(dir, rel)
			if st, err := os.Stat(bin); err == nil && !st.IsDir() {
				return bin
			}
		}
	case "typescript", "javascript":
		cli := filepath.Join(dir, "lib", "cli.mjs")
		if st, err := os.Stat(cli); err == nil && !st.IsDir() {
			return cli
		}
	case "css", "html", "json":
		entries := map[string]string{
			"css":  webCSSEntry,
			"html": webHTMLEntry,
			"json": webJSONEntry,
		}
		entry := filepath.Join(dir, filepath.FromSlash(entries[lang]))
		if st, err := os.Stat(entry); err == nil && !st.IsDir() {
			return entry
		}
	case "bash":
		entry := filepath.Join(dir, filepath.FromSlash(bashEntry))
		if st, err := os.Stat(entry); err == nil && !st.IsDir() {
			return entry
		}
	}
	return ""
}

// --- Manifest definitions ----------------------------------------------------
//
// Version and SHA256 constants define the pinned pyright and rust-analyzer
// releases for managed install.  The SHA256 values below are the real digests
// of the artifacts at the pinned versions; update both the version and the
// checksums when bumping to a newer release.

// pyrightManifest returns the installer manifest for the pyright npm package.
// The npm tarball (pyright-{version}.tgz) is extracted with StripTopLevel so
// langserver.index.js lands directly in the version directory.
func pyrightManifest() (installer.Manifest, error) {
	return toolManifest("pyright", pyrightVersion, []installer.Asset{
		{
			GOOS:          "*",
			GOARCH:        "*",
			URL:           "https://registry.npmjs.org/pyright/-/pyright-" + pyrightVersion + ".tgz",
			SHA256:        pyrightSHA256,
			Kind:          installer.KindTarGz,
			StripTopLevel: true,
		},
	}), nil
}

// webManifest returns the installer manifest for the vscode-langservers-extracted
// npm package. One tarball installs the CSS, HTML, and JSON language servers.
func webManifest() (installer.Manifest, error) {
	return toolManifest("vscode-langservers-extracted", webLangserversVersion, []installer.Asset{
		{
			GOOS:          "*",
			GOARCH:        "*",
			URL:           "https://registry.npmjs.org/vscode-langservers-extracted/-/vscode-langservers-extracted-" + webLangserversVersion + ".tgz",
			SHA256:        webLangserversSHA256,
			Kind:          installer.KindTarGz,
			StripTopLevel: true,
		},
	}), nil
}

// bashManifest returns the installer manifest for the bash-language-server npm
// package.
func bashManifest() (installer.Manifest, error) {
	return toolManifest("bash-language-server", bashLanguageServerVersion, []installer.Asset{
		{
			GOOS:          "*",
			GOARCH:        "*",
			URL:           "https://registry.npmjs.org/bash-language-server/-/bash-language-server-" + bashLanguageServerVersion + ".tgz",
			SHA256:        bashLanguageServerSHA256,
			Kind:          installer.KindTarGz,
			StripTopLevel: true,
		},
	}), nil
}

// tsServerManifest returns the installer manifest for the
// typescript-language-server npm package (serves both "typescript" and
// "javascript").
func tsServerManifest() (installer.Manifest, error) {
	return toolManifest("typescript-language-server", tsServerVersion, []installer.Asset{
		{
			GOOS:          "*",
			GOARCH:        "*",
			URL:           "https://registry.npmjs.org/typescript-language-server/-/typescript-language-server-" + tsServerVersion + ".tgz",
			SHA256:        tsServerSHA256,
			Kind:          installer.KindTarGz,
			StripTopLevel: true,
		},
	}), nil
}

// typescriptPackageManifest returns the manifest for the typescript npm
// package, installed as a companion under the typescript-language-server
// install dir so tls can pin tsserver without a global npm install.
func typescriptPackageManifest() (installer.Manifest, error) {
	return toolManifest("typescript", typescriptPackageVersion, []installer.Asset{
		{
			GOOS:          "*",
			GOARCH:        "*",
			URL:           "https://registry.npmjs.org/typescript/-/typescript-" + typescriptPackageVersion + ".tgz",
			SHA256:        typescriptPackageSHA256,
			Kind:          installer.KindTarGz,
			StripTopLevel: true,
		},
	}), nil
}

// rustAnalyzerManifest returns the installer manifest for rust-analyzer from
// GitHub releases.  Unix assets are gzip-compressed single binaries installed
// as "rust-analyzer"; the Windows asset is a zip containing "rust-analyzer.exe"
// at the archive root.
func rustAnalyzerManifest() (installer.Manifest, error) {
	return toolManifest("rust-analyzer", rustAnalyzerVersion, []installer.Asset{
		{
			GOOS:        "linux",
			GOARCH:      "amd64",
			URL:         "https://github.com/rust-lang/rust-analyzer/releases/download/" + rustAnalyzerVersion + "/rust-analyzer-x86_64-unknown-linux-gnu.gz",
			SHA256:      rustAnalyzerLinuxAMD64SHA256,
			Kind:        installer.KindGzip,
			InstallName: "rust-analyzer",
		},
		{
			GOOS:        "linux",
			GOARCH:      "arm64",
			URL:         "https://github.com/rust-lang/rust-analyzer/releases/download/" + rustAnalyzerVersion + "/rust-analyzer-aarch64-unknown-linux-gnu.gz",
			SHA256:      rustAnalyzerLinuxARM64SHA256,
			Kind:        installer.KindGzip,
			InstallName: "rust-analyzer",
		},
		{
			GOOS:        "darwin",
			GOARCH:      "amd64",
			URL:         "https://github.com/rust-lang/rust-analyzer/releases/download/" + rustAnalyzerVersion + "/rust-analyzer-x86_64-apple-darwin.gz",
			SHA256:      rustAnalyzerDarwinAMD64SHA256,
			Kind:        installer.KindGzip,
			InstallName: "rust-analyzer",
		},
		{
			GOOS:        "darwin",
			GOARCH:      "arm64",
			URL:         "https://github.com/rust-lang/rust-analyzer/releases/download/" + rustAnalyzerVersion + "/rust-analyzer-aarch64-apple-darwin.gz",
			SHA256:      rustAnalyzerDarwinARM64SHA256,
			Kind:        installer.KindGzip,
			InstallName: "rust-analyzer",
		},
		{
			GOOS:   "windows",
			GOARCH: "amd64",
			URL:    "https://github.com/rust-lang/rust-analyzer/releases/download/" + rustAnalyzerVersion + "/rust-analyzer-x86_64-pc-windows-msvc.zip",
			SHA256: rustAnalyzerWindowsAMD64SHA256,
			Kind:   installer.KindZip,
		},
	}), nil
}

// clangdManifest returns the installer manifest for clangd from GitHub
// releases. Every asset is a zip wrapping a single top-level
// clangd_<version>/ directory — binary at bin/clangd(.exe) plus the
// lib/clang builtin headers — extracted with StripTopLevel. The macOS zip
// carries a universal binary covering both amd64 and arm64.
func clangdManifest() (installer.Manifest, error) {
	clangdAsset := func(platform, goos, goarch, sha string) installer.Asset {
		return installer.Asset{
			GOOS:          goos,
			GOARCH:        goarch,
			URL:           "https://github.com/clangd/clangd/releases/download/" + clangdVersion + "/clangd-" + platform + "-" + clangdVersion + ".zip",
			SHA256:        sha,
			Kind:          installer.KindZip,
			StripTopLevel: true,
		}
	}
	return toolManifest("clangd", clangdVersion, []installer.Asset{
		clangdAsset("linux", "linux", "amd64", clangdLinuxAMD64SHA256),
		clangdAsset("mac", "darwin", "*", clangdDarwinSHA256), // universal binary: amd64 + arm64
		clangdAsset("windows", "windows", "amd64", clangdWindowsAMD64SHA256),
	}), nil
}

// Pinned release versions and SHA256 checksums for the managed installs.  These
// match the actual current stable release artifacts at the versions below.
const (
	pyrightVersion = "1.1.413"
	pyrightSHA256  = "7322a75188e788f9fe7cbb71891af435a713bf8985141dc0d28e8ca243977bee"

	webLangserversVersion = "4.10.0"
	webLangserversSHA256  = "d6e2d090d09c4b91daa74e9e7462a3d3f244efb96aa5111004cfffa49d6dc9ef"

	tsServerVersion = "6.0.0"
	tsServerSHA256  = "6e23b48efc76af4e70928cdfe62ea6e6cfef67ab4c1e7579c4e82dd284fbdfd2"

	typescriptPackageVersion = "6.0.3"
	typescriptPackageSHA256  = "33cd0ee1beaa8c9e9d15a9da836c62ddea4c34a42d7c2d349dbc80d94165d22a"

	bashLanguageServerVersion = "5.6.0"
	bashLanguageServerSHA256  = "56d22481ffd0eed3edd23d1130554da31dc186d935202f08cf7ce3894fe801be"

	rustAnalyzerVersion            = "2026-08-17.4"
	rustAnalyzerLinuxAMD64SHA256   = "a559eaa29920e4c12718fba101f2055f1da0ad8bc458ef9dc1a670778cc66901"
	rustAnalyzerLinuxARM64SHA256   = "941ad31c4256eec3c8457257b0fcfb696d2b4f80c0e5a996f7375a92130c2447"
	rustAnalyzerDarwinAMD64SHA256  = "134a7d305991de776864e43d1e6c291f60fa2888d4b9b7749864c562c5dc28b7"
	rustAnalyzerDarwinARM64SHA256  = "ece932daf2f077be87bf745d2eb0a62cbc550f4b1e2e31ca76dfafdd0cc599b3"
	rustAnalyzerWindowsAMD64SHA256 = "3212cc9e7ab3f6b07f97be681c2a7200f73fb0463e6f8055c214ebe0b00901f2"

	// clangd 22.1.6 (github.com/clangd/clangd releases); digests are the
	// release assets' published sha256 values.
	clangdVersion            = "22.1.6"
	clangdLinuxAMD64SHA256   = "a9c77443af2e447ed467e84771848d3a6ac1c56f84bcfcde717e66318de77cfa"
	clangdDarwinSHA256       = "631aef462556cbd74e0ebaae1778a38d1997d0ba3371652ca54f82652a179e7d"
	clangdWindowsAMD64SHA256 = "ce54f16e0b4fd76d450eeda9664420b195360b73febcfe40e661108fa57f2ce1"
)

// progressSink maps installer byte-level progress into throttled
// lsp.install_progress events (at most one per ≥2% step) so a multi-hundred-MB
// artifact does not flood the event stream. Unknown total size emits no
// incremental updates; the start/done/failed events still fire.
func (a *Actor) progressSink(lang string) installer.ProgressFunc {
	var mu sync.Mutex
	last := 0
	return func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		if pct > 99 {
			pct = 99
		}
		mu.Lock()
		if pct < last+2 && pct < 99 {
			mu.Unlock()
			return
		}
		last = pct
		mu.Unlock()
		a.emitInstallProgress(lang, installStateRunning, int32(pct), "")
	}
}

// emitInstallProgress publishes one progress event; emission failures are
// dropped (no subscriber should break an install).
func (a *Actor) emitInstallProgress(lang, state string, percent int32, errMsg string) {
	if a.emit == nil {
		return
	}
	ev := gen.LspInstallProgressEvent{Language: lang, State: state, Percent: percent}
	if errMsg != "" {
		ev.Error = errMsg
	}
	_ = a.emit(installProgressEvent, ev)
}

// --- persist integration ---

// installSnapshot returns a copy of the managed-install records.
func (a *Actor) installSnapshot() map[string]managedInstall {
	a.installMu.RLock()
	defer a.installMu.RUnlock()
	out := make(map[string]managedInstall, len(a.installs))
	for k, v := range a.installs {
		out[k] = v
	}
	return out
}

// decodeInstalls parses the persisted installs document. Accepts nil (absent
// key) as an empty map; malformed entries degrade to empty rather than failing
// the whole load — the filesystem marker check in managedStateFor is the real
// source of truth.
func decodeInstalls(raw json.RawMessage) map[string]managedInstall {
	installs := map[string]managedInstall{}
	if len(raw) == 0 {
		return installs
	}
	var parsed map[string]managedInstall
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return installs
	}
	for k, v := range parsed {
		installs[k] = v
	}
	return installs
}

// autoInstallTSCompanion downloads the typescript companion package into the
// existing typescript-language-server managed install directory. Called
// when initialize fails with "Could not find a valid TypeScript installation"
// — the common case after upgrading from a pre-companion version that left
// the server installed without a bundled tsserver.
func (a *Actor) autoInstallTSCompanion(log actor.Logger) {
	spec, ok := a.specFor("typescript")
	if !ok || spec.manifest == nil {
		return
	}
	serverM, err := spec.manifest()
	if err != nil {
		return
	}
	if len(spec.deps) == 0 {
		return
	}
	depM, err := spec.deps[0]()
	if err != nil {
		return
	}
	depM.InstallDir = serverM.InstallDir + "/" + depM.Tool

	a.installMu.Lock()
	if a.inFlight["typescript"] {
		a.installMu.Unlock()
		return
	}
	a.inFlight["typescript"] = true
	a.installMu.Unlock()

	go func() {
		defer func() {
			a.installMu.Lock()
			delete(a.inFlight, "typescript")
			a.installMu.Unlock()
		}()

		log.Info("lspserver: auto-installing typescript companion (missing tsserver)")
		a.emitInstallProgress("typescript", installStateRunning, 0, "")

		if _, derr := installer.Ensure(context.Background(), a.managedRoot(), depM, installer.Options{
			Progress: a.progressSink("typescript"),
		}); derr != nil {
			log.Error("lspserver: auto companion install failed", "error", derr)
			a.emitInstallProgress("typescript", installStateFailed, 0, derr.Error())
			return
		}

		log.Info("lspserver: auto companion install complete")
		a.emitInstallProgress("typescript", installStateDone, 100, "")
	}()
}
