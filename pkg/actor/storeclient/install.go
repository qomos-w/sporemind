package storeclient

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

func (a *Actor) handleInstall(ctx actor.Context, req gen.StoreInstallReq) (gen.StoreInstallResp, error) {
	switch req.Kind {
	case "store":
		return a.installStore(ctx, req)
	case "community":
		return a.installCommunity(ctx, req)
	default:
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient.install: Kind must be \"store\" or \"community\", got %q", req.Kind)
	}
}

// installStore downloads an encrypted store package, verifies and decrypts
// it, then feeds the plaintext zip to appmanager.install_local (which
// re-verifies any in-package PACKAGE.sig against the publisher key).
func (a *Actor) installStore(ctx actor.Context, req gen.StoreInstallReq) (gen.StoreInstallResp, error) {
	if req.Slug == "" {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient.install: Slug is required")
	}
	wire, _, _, err := a.fetchIndex(ctx.Lifecycle(), false)
	if err != nil {
		return gen.StoreInstallResp{}, err
	}
	entry, version, err := pickStoreVersion(wire, req.Slug, req.Version)
	if err != nil {
		return gen.StoreInstallResp{}, err
	}

	baseURL, err := a.baseURL()
	if err != nil {
		return gen.StoreInstallResp{}, err
	}
	token, err := a.authToken(ctx)
	if err != nil {
		return gen.StoreInstallResp{}, err
	}
	url := joinURL(baseURL, fmt.Sprintf("/store/plugins/%s/download?version=%s", req.Slug, version.Version))
	dlReq, err := http.NewRequestWithContext(ctx.Lifecycle(), http.MethodGet, url, nil)
	if err != nil {
		return gen.StoreInstallResp{}, err
	}
	if token != "" {
		dlReq.Header.Set("Authorization", "Bearer "+token)
	}
	artifact, headers, err := a.downloadWithHeaders(dlReq)
	if err != nil {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: download %s: %w", req.Slug, err)
	}

	a.mu.RLock()
	channel := a.state.Channel
	prodKey := a.state.ProductionPubKey
	a.mu.RUnlock()
	pub, err := officialPubKey(channel, prodKey)
	if err != nil {
		return gen.StoreInstallResp{}, err
	}
	// Order is security-critical: verify the official signature over the
	// ciphertext BEFORE decrypting, then confirm the decrypted payload hash.
	if err := verifyStoreSignature(pub, headers.Get("X-Signature"), artifact); err != nil {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: %w", err)
	}
	if cs := headers.Get("X-Ciphertext-Sha256"); cs != "" && !matchesSha256(cs, artifact) {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: ciphertext sha256 mismatch (download corrupted)")
	}
	zipBytes, err := decryptStoreArtifact(headers.Get("X-Content-Key"), artifact)
	if err != nil {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: %w", err)
	}
	if !matchesSha256(headers.Get("X-Payload-Sha256"), zipBytes) {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: payload sha256 mismatch after decrypt")
	}
	// Cross-check against the (immutable) index version record.
	if version.PayloadSha256 != "" && !matchesSha256(version.PayloadSha256, zipBytes) {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: payload sha256 does not match index version %s", version.Version)
	}

	appRef, ok := ctx.LookupService("appmanager")
	if !ok || appRef == nil {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: appmanager service not available")
	}
	respValue, err := ctx.Planner().Call(ctx.Lifecycle(), appRef, "appmanager.install_local",
		gen.AppManagerInstallLocalReq{PackageData: zipBytes}).Await()
	if err != nil {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: install_local: %w", err)
	}
	resp, ok := respValue.(gen.AppManagerInstallLocalResp)
	if !ok {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: unexpected install_local response %T", respValue)
	}
	ctx.Logger().Info("storeclient: store install complete",
		"slug", req.Slug, "version", version.Version, "app", resp.Status.ID)
	return gen.StoreInstallResp{
		AppID:   resp.Status.ID,
		Version: version.Version,
		State:   resp.Status.State,
		Origin:  "store:" + entry.Channel,
	}, nil
}

// installCommunity fetches the commit-pinned GitHub tarball (cloud mirror
// first, codeload fallback), verifies sha256, extracts the app source,
// runs the local compatibility gates, builds (native runtime), and registers
// through appmanager.
func (a *Actor) installCommunity(ctx actor.Context, req gen.StoreInstallReq) (gen.StoreInstallResp, error) {
	if req.Slug == "" {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient.install: Slug is required")
	}
	if req.Version != "" {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient.install: community installs are commit-pinned; Version is not selectable")
	}
	wire, _, _, err := a.fetchIndex(ctx.Lifecycle(), false)
	if err != nil {
		return gen.StoreInstallResp{}, err
	}
	entry := findCommunityEntry(wire, req.Slug)
	if entry == nil {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: community plugin %q not found in index", req.Slug)
	}
	view := gen.StoreCommunityView{ProtocolVersion: entry.ProtocolVersion, SdkVersion: entry.SdkVersion}
	checkCommunityCompat(&view)
	if !view.Compatible {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: community plugin %q incompatible: %s", req.Slug, view.IncompatibleReason)
	}
	if len(entry.CommitSha) != 40 {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: community entry %q has invalid commit_sha %q", req.Slug, entry.CommitSha)
	}

	baseURL, _ := a.baseURL()
	mirror := joinURL(baseURL, entry.MirrorUrl)
	codeload, err := codeloadURL(entry.RepoUrl, entry.CommitSha)
	if err != nil {
		return gen.StoreInstallResp{}, err
	}
	tarball, err := a.fetchVerifiedTarball(ctx.Lifecycle(), mirror, codeload, entry.TarballSha256)
	if err != nil {
		return gen.StoreInstallResp{}, err
	}

	appDir, cleanup, err := extractCommunityApp(tarball)
	if err != nil {
		return gen.StoreInstallResp{}, err
	}
	defer cleanup()

	manifest, err := readManifest(appDir)
	if err != nil {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: community plugin %q: %w", req.Slug, err)
	}
	if entry.Manifest.ID != "" && manifest.ID != entry.Manifest.ID {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: manifest Id %q does not match index entry %q", manifest.ID, entry.Manifest.ID)
	}
	// Same protocol/sdk gates as the store path, this time against the
	// actual manifest rather than the index summary.
	if manifest.ProtocolVersion != hostProtocolVersion {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: manifest protocol_version %d != host %d", manifest.ProtocolVersion, hostProtocolVersion)
	}
	if ok, reason := sdkCompatible(manifest.SdkVersion); !ok {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: %s", reason)
	}

	pkgFiles, err := collectAppFiles(appDir)
	if err != nil {
		return gen.StoreInstallResp{}, err
	}

	registerReq := gen.AppManagerRegisterReq{
		Manifest:    *manifest,
		EntryModule: entryModuleFor(pkgFiles),
		Modules:     pkgFiles.modules,
		Origin:      "user",
	}
	if len(pkgFiles.descriptors) > 0 {
		descs := map[string]gen.AppObjectDescriptor{}
		if err := json.Unmarshal(pkgFiles.descriptors, &descs); err != nil {
			return gen.StoreInstallResp{}, fmt.Errorf("storeclient: decode app.descriptors.json: %w", err)
		}
		registerReq.SchemaDescriptors = descs
	}

	if manifest.Runtime == "native" {
		buildResult, err := pluginhost.CompileFromProject(pluginhost.NativeBuildOptions{
			AppName:    manifest.Name,
			AppVersion: manifest.Version,
			SourceRoot: appDir,
			OutDir:     filepath.Join(appDir, ".sporecode", "build"),
		}, filepath.Join(appDir, "app.manifest.json"), "")
		if err != nil {
			return gen.StoreInstallResp{}, fmt.Errorf("storeclient: community build %q: %s", manifest.ID, buildResult.Diagnostic)
		}
		artifactBytes, err := os.ReadFile(buildResult.ArtifactPath)
		if err != nil {
			return gen.StoreInstallResp{}, fmt.Errorf("storeclient: read built artifact: %w", err)
		}
		zipBytes, err := assembleInstallZip(manifest, pkgFiles, artifactBytes)
		if err != nil {
			return gen.StoreInstallResp{}, err
		}
		appRef, ok := ctx.LookupService("appmanager")
		if !ok || appRef == nil {
			return gen.StoreInstallResp{}, fmt.Errorf("storeclient: appmanager service not available")
		}
		respValue, err := ctx.Planner().Call(ctx.Lifecycle(), appRef, "appmanager.install_local",
			gen.AppManagerInstallLocalReq{PackageData: zipBytes}).Await()
		if err != nil {
			return gen.StoreInstallResp{}, fmt.Errorf("storeclient: install_local: %w", err)
		}
		resp, ok := respValue.(gen.AppManagerInstallLocalResp)
		if !ok {
			return gen.StoreInstallResp{}, fmt.Errorf("storeclient: unexpected install_local response %T", respValue)
		}
		ctx.Logger().Info("storeclient: community install complete",
			"slug", req.Slug, "commit", entry.CommitSha[:12], "app", resp.Status.ID)
		return gen.StoreInstallResp{
			AppID:   resp.Status.ID,
			Version: manifest.Version,
			State:   resp.Status.State,
			Origin:  "community",
		}, nil
	}

	// Spore-runtime community apps register directly from module sources.
	appRef, ok := ctx.LookupService("appmanager")
	if !ok || appRef == nil {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: appmanager service not available")
	}
	respValue, err := ctx.Planner().Call(ctx.Lifecycle(), appRef, "appmanager.register", registerReq).Await()
	if err != nil {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: register: %w", err)
	}
	resp, ok := respValue.(gen.AppStatus)
	if !ok {
		return gen.StoreInstallResp{}, fmt.Errorf("storeclient: unexpected register response %T", respValue)
	}
	ctx.Logger().Info("storeclient: community install complete",
		"slug", req.Slug, "commit", entry.CommitSha[:12], "app", resp.ID)
	return gen.StoreInstallResp{
		AppID:   resp.ID,
		Version: manifest.Version,
		State:   resp.State,
		Origin:  "community",
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// authToken resolves the bearer token for authenticated downloads: explicit
// config token first, then the linked cloudaccount session token.
func (a *Actor) authToken(ctx actor.Context) (string, error) {
	a.mu.RLock()
	token := a.state.AuthToken
	a.mu.RUnlock()
	if token != "" {
		return token, nil
	}
	ref, ok := ctx.LookupService("cloudaccount")
	if !ok || ref == nil {
		return "", nil
	}
	value, err := ctx.Planner().Call(ctx.Lifecycle(), ref, "cloudaccount.session_token",
		gen.CloudAccountSessionTokenReq{}).Await()
	if err != nil {
		return "", fmt.Errorf("storeclient: cloudaccount.session_token: %w", err)
	}
	resp, ok := value.(gen.CloudAccountSessionTokenResp)
	if !ok {
		return "", fmt.Errorf("storeclient: unexpected session_token response %T", value)
	}
	return resp.AccessToken, nil
}

func (a *Actor) downloadWithHeaders(req *http.Request) ([]byte, http.Header, error) {
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("status %d: %s", resp.StatusCode, snippet(body))
	}
	return body, resp.Header, nil
}

// fetchVerifiedTarball downloads the mirror URL first and falls back to
// codeload; both paths verify the index-pinned sha256 before use.
func (a *Actor) fetchVerifiedTarball(ctx context.Context, mirror, codeload, sha string) ([]byte, error) {
	if sha == "" {
		return nil, fmt.Errorf("storeclient: community entry has no tarball_sha256")
	}
	var errs []string
	for _, url := range []string{mirror, codeload} {
		if url == "" {
			continue
		}
		body, err := httpGetAll(ctx, a.http, url)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", redactPath(url), err))
			continue
		}
		if !matchesSha256(sha, body) {
			errs = append(errs, fmt.Sprintf("%s: tarball sha256 mismatch", redactPath(url)))
			continue
		}
		return body, nil
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("no tarball source available")
	}
	return nil, fmt.Errorf("storeclient: tarball fetch failed — %s", strings.Join(errs, "; "))
}

func redactPath(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		if j := strings.Index(raw[i+3:], "/"); j >= 0 {
			return raw[i+3+j:]
		}
	}
	return raw
}

var githubRepoRe = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/?$`)

func codeloadURL(repoURL, commitSha string) (string, error) {
	m := githubRepoRe.FindStringSubmatch(strings.TrimSpace(repoURL))
	if m == nil {
		return "", fmt.Errorf("storeclient: repo_url %q is not a github.com owner/repo URL", repoURL)
	}
	return fmt.Sprintf("https://codeload.github.com/%s/%s/tar.gz/%s", m[1], m[2], commitSha), nil
}

func pickStoreVersion(wire storeIndexWire, slug, version string) (*storePluginWire, *storeVersionWire, error) {
	for i := range wire.Store {
		p := &wire.Store[i]
		if p.Slug != slug {
			continue
		}
		if version != "" {
			for j := range p.Versions {
				v := &p.Versions[j]
				if v.Version == version {
					if ok, reason := versionCompat(v); !ok {
						return nil, nil, fmt.Errorf("storeclient: %s %s incompatible: %s", slug, version, reason)
					}
					return p, v, nil
				}
			}
			return nil, nil, fmt.Errorf("storeclient: version %q not found for %q", version, slug)
		}
		var latest *storeVersionWire
		for j := range p.Versions {
			v := &p.Versions[j]
			if ok, _ := versionCompat(v); !ok {
				continue
			}
			if latest == nil || v.CreatedAt > latest.CreatedAt {
				latest = v
			}
		}
		if latest == nil {
			return nil, nil, fmt.Errorf("storeclient: no compatible version for %q", slug)
		}
		return p, latest, nil
	}
	return nil, nil, fmt.Errorf("storeclient: store plugin %q not found in index", slug)
}

func versionCompat(v *storeVersionWire) (bool, string) {
	view := gen.StoreVersionView{ProtocolVersion: v.ProtocolVersion, SdkVersion: v.SdkVersion, MinHostVersion: v.MinHostVersion}
	checkVersionCompat(&view)
	return view.Compatible, view.IncompatibleReason
}

func findCommunityEntry(wire storeIndexWire, slug string) *storeCommunityWire {
	for i := range wire.Community {
		if wire.Community[i].Slug == slug {
			return &wire.Community[i]
		}
	}
	return nil
}

// extractCommunityApp unpacks the GitHub tar.gz into a temp directory and
// returns the directory containing app.manifest.json (the tarball's
// {repo}-{sha}/ root wrapper is stripped).
func extractCommunityApp(tarball []byte) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "storecommunity-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("tar: %w", err)
		}
		name := path.Clean(hdr.Name)
		if name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
			continue
		}
		// Symlinks/hardlinks/dev nodes never appear in GitHub archives; skip
		// anything that is not a regular file or directory defensively.
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeDir {
			continue
		}
		// Strip the {repo}-{sha}/ root wrapper (first path segment).
		trimmed := strings.SplitN(strings.TrimPrefix(name, "./"), "/", 2)
		if len(trimmed) < 2 {
			continue
		}
		rel := trimmed[1]
		if rel == "" || hasHiddenComponent(rel) {
			continue
		}
		dest := filepath.Join(tmp, filepath.FromSlash(rel))
		if !strings.HasPrefix(dest, tmp+string(filepath.Separator)) {
			continue
		}
		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				cleanup()
				return "", nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			cleanup()
			return "", nil, err
		}
		if err := writeTarFile(dest, tr); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	appDir := findAppRoot(tmp)
	if appDir == "" {
		cleanup()
		return "", nil, fmt.Errorf("no app.manifest.json found in tarball")
	}
	return appDir, cleanup, nil
}

func writeTarFile(dest string, r io.Reader) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, io.LimitReader(r, maxDownloadBytes))
	return err
}

// hasHiddenComponent reports whether any path component starts with "." —
// VCS dirs and build caches (.build, .sporecode) are never app content and
// can be gigabytes; skipping them keeps extraction bounded.
func hasHiddenComponent(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// findAppRoot locates the nearest directory containing app.manifest.json —
// the repo root, or the single app subdirectory for monorepos.
func findAppRoot(root string) string {
	if _, err := os.Stat(filepath.Join(root, "app.manifest.json")); err == nil {
		return root
	}
	matches, _ := filepath.Glob(filepath.Join(root, "*", "app.manifest.json"))
	if len(matches) == 1 {
		return filepath.Dir(matches[0])
	}
	return ""
}

func readManifest(appDir string) (*gen.AppManifest, error) {
	data, err := os.ReadFile(filepath.Join(appDir, "app.manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read app.manifest.json: %w", err)
	}
	var m gen.AppManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode app.manifest.json: %w", err)
	}
	if m.ID == "" || m.Name == "" || m.Version == "" {
		return nil, fmt.Errorf("manifest Id/Name/Version are required")
	}
	return &m, nil
}

// appFiles is the collected package content from an app directory.
type appFiles struct {
	modules     map[string]string // package-relative slash path → content
	descriptors []byte            // app.descriptors.json (nil when absent)
}

// collectSkipDirs mirrors the appmanager package walker: vendored SDK and
// build output never enter the package.
var collectSkipDirs = map[string]bool{
	"vendor":      true,
	"vendor-sdk":  true,
	"node_modules": true,
	".sporecode":  true,
	".git":        true,
}

func collectAppFiles(appDir string) (appFiles, error) {
	modules := map[string]string{}
	var descriptors []byte
	err := filepath.WalkDir(appDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(appDir, p)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel != "." && (collectSkipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "app.descriptors.json" {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			descriptors = data
			return nil
		}
		if d.Name() == "app.manifest.json" || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".go" && ext != ".spore" && ext != ".ss" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		modules[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		return appFiles{}, err
	}
	return appFiles{modules: modules, descriptors: descriptors}, nil
}

func entryModuleFor(f appFiles) string {
	if _, ok := f.modules["main.gen.go"]; ok {
		return "main.gen.go"
	}
	if _, ok := f.modules["main.go"]; ok {
		return "main.go"
	}
	return ""
}

// assembleInstallZip builds an install_local-compatible zip from a built
// community app: manifest, schema descriptors, module sources, abi.json, and
// the artifact binary. install_local performs the authoritative contract
// validation (schema hash, native manifest) when loading the package.
func assembleInstallZip(manifest *gen.AppManifest, f appFiles, artifact []byte) ([]byte, error) {
	// Same ABI the pluginhost build pipeline stamps (subprocess dev
	// transport, first-party trust).
	abi := gen.PluginAbi{
		Name: "spore-plugin", Version: 1, Encoding: pluginhost.BinaryCodecV1,
		InvokeSymbol: "PluginInvoke", ContractVersion: "1",
		TrustClass: pluginhost.TrustFirstParty,
		Signer:     pluginhost.NativeBuildSigner,
		Isolation:  pluginhost.IsolationSubprocess,
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, content []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(content)
		return err
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := write("app.manifest.json", manifestJSON); err != nil {
		return nil, err
	}
	if len(f.descriptors) > 0 {
		if err := write("app.descriptors.json", f.descriptors); err != nil {
			return nil, err
		}
	}
	abiJSON, err := json.MarshalIndent(abi, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := write("abi.json", abiJSON); err != nil {
		return nil, err
	}
	artifactName := "plugin"
	if runtime.GOOS == "windows" {
		artifactName += ".exe"
	}
	if err := write(artifactName, artifact); err != nil {
		return nil, err
	}
	for name, content := range f.modules {
		if err := write(name, []byte(content)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
