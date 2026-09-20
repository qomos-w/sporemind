// Content-store integration (M4-B): the cloudaccount actor proxies the
// sporemind cloud content-store API (§3.5) for the desktop frontend and
// dispatches downloaded packages to the appropriate runtime installer.
//
// Install pipeline:
//  1. Fetch content detail from the cloud (sha256, signature, content_type).
//  2. Download the zip package bytes (auth required).
//  3. Verify sha256 of the downloaded bytes against the expected hash.
//  4. Parse the zip to extract manifest.json + content-specific files.
//  5. Build a risk assessment and — if the caller confirmed — dispatch to the
//     installer selected by content_type:
//     - spore_app         → appmanager.register
//     - native_plugin     → pluginhost.artifact_load (SHA256 verified internally)
//     - workflow_template → workspace.save_agent_kind_config (workflow exec kind)
//
// The contentFetcher and contentDispatcher interfaces keep the download and
// dispatch logic independently testable: tests inject fakes without starting
// HTTP servers or the actor runtime.

package cloudaccount

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Content type constants matching the cloud content-store values.
const (
	contentTypeSporeApp         = "spore_app"
	contentTypeNativePlugin     = "native_plugin"
	contentTypeWorkflowTemplate = "workflow_template"
)

// ---------------------------------------------------------------------------
// contentFetcher — abstracts the cloud content-store REST API
// ---------------------------------------------------------------------------

// contentFetcher abstracts the cloud /store/content endpoints so the actor
// can swap in a fake in tests. Search and Detail hit the public (no-auth)
// endpoints; Download hits the auth-required download endpoint.
type contentFetcher interface {
	SearchContent(ctx context.Context, req gen.ContentSearchReq) (gen.ContentSearchResp, error)
	GetContentDetail(ctx context.Context, slug string) (gen.ContentDetailResp, error)
	DownloadContent(ctx context.Context, accessToken, slug string) (data []byte, expectedSHA256 string, err error)
}

// --- cloud HTTP client implementations (methods on *cloudHTTPClient) ---

// cloudListItem mirrors the cloud List API response item shape.
type cloudListItem struct {
	ID            string `json:"id"`
	ContentType   string `json:"content_type"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Description   string `json:"description,omitempty"`
	Version       string `json:"version"`
	AuthorID      string `json:"author_id"`
	Status        string `json:"status"`
	SHA256        string `json:"sha256"`
	DownloadCount int64  `json:"download_count"`
}

// cloudListResponse mirrors the cloud List API response wrapper.
type cloudListResponse struct {
	Items    []cloudListItem `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

// cloudDetailResponse mirrors the cloud Detail API response.
type cloudDetailResponse struct {
	cloudListItem
	Signature string          `json:"signature,omitempty"`
	Manifest  json.RawMessage `json:"manifest"`
}

func (c *cloudHTTPClient) SearchContent(ctx context.Context, req gen.ContentSearchReq) (gen.ContentSearchResp, error) {
	u := c.baseURL + "/store/content"
	q := url.Values{}
	if req.ContentType != "" {
		q.Set("type", req.ContentType)
	}
	if req.Query != "" {
		q.Set("q", req.Query)
	}
	if req.Sort != "" {
		q.Set("sort", req.Sort)
	}
	if req.Page > 0 {
		q.Set("page", fmt.Sprintf("%d", req.Page))
	}
	if req.PageSize > 0 {
		q.Set("page_size", fmt.Sprintf("%d", req.PageSize))
	}
	if encoded := q.Encode(); encoded != "" {
		u += "?" + encoded
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return gen.ContentSearchResp{}, fmt.Errorf("cloudaccount: build content search request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return gen.ContentSearchResp{}, fmt.Errorf("cloudaccount: content search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return gen.ContentSearchResp{}, fmt.Errorf("cloudaccount: content search: status %d: %s", resp.StatusCode, string(body))
	}

	var lr cloudListResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return gen.ContentSearchResp{}, fmt.Errorf("cloudaccount: decode content search: %w", err)
	}

	items := make([]gen.ContentListItem, 0, len(lr.Items))
	for _, it := range lr.Items {
		items = append(items, cloudItemToView(it))
	}
	return gen.ContentSearchResp{
		Items:    items,
		Page:     int64(lr.Page),
		PageSize: int64(lr.PageSize),
		Total:    lr.Total,
	}, nil
}

func (c *cloudHTTPClient) GetContentDetail(ctx context.Context, slug string) (gen.ContentDetailResp, error) {
	u := c.baseURL + "/store/content/" + url.PathEscape(slug)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return gen.ContentDetailResp{}, fmt.Errorf("cloudaccount: build content detail request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return gen.ContentDetailResp{}, fmt.Errorf("cloudaccount: content detail: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return gen.ContentDetailResp{}, fmt.Errorf("cloudaccount: content detail: status %d: %s", resp.StatusCode, string(body))
	}

	var dr cloudDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return gen.ContentDetailResp{}, fmt.Errorf("cloudaccount: decode content detail: %w", err)
	}

	item := cloudItemToView(dr.cloudListItem)
	item.Signature = dr.Signature
	return gen.ContentDetailResp{
		Item:     item,
		Manifest: string(dr.Manifest),
	}, nil
}

func (c *cloudHTTPClient) DownloadContent(ctx context.Context, accessToken, slug string) ([]byte, string, error) {
	u := c.baseURL + "/store/content/" + url.PathEscape(slug) + "/download"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", fmt.Errorf("cloudaccount: build content download request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	httpReq.Header.Set("Accept", "application/zip")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("cloudaccount: content download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("cloudaccount: content download: status %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 100<<20)) // 100 MB cap
	if err != nil {
		return nil, "", fmt.Errorf("cloudaccount: read content download: %w", err)
	}
	return data, resp.Header.Get("X-Content-SHA256"), nil
}

func cloudItemToView(it cloudListItem) gen.ContentListItem {
	return gen.ContentListItem{
		ID:            it.ID,
		ContentType:   it.ContentType,
		Name:          it.Name,
		Slug:          it.Slug,
		Description:   it.Description,
		Version:       it.Version,
		Author:        it.AuthorID,
		DownloadCount: it.DownloadCount,
		Sha256:        it.SHA256,
	}
}

// ---------------------------------------------------------------------------
// Pure helpers (no side effects, testable in isolation)
// ---------------------------------------------------------------------------

// VerifySHA256 reports whether sha256(data) equals the expected hex digest.
// Comparison is case-insensitive. An empty expected value returns false — a
// package without a recorded hash cannot be verified.
func VerifySHA256(data []byte, expected string) bool {
	if expected == "" {
		return false
	}
	sum := sha256.Sum256(data)
	return strings.EqualFold(hex.EncodeToString(sum[:]), expected)
}

// contentPackageManifest mirrors the manifest.json embedded inside a
// downloaded content zip. It carries the content_type and entry_point needed
// to select and parameterise the installer.
type contentPackageManifest struct {
	ContentType string            `json:"content_type"`
	Name        string            `json:"name"`
	Slug        string            `json:"slug"`
	Version     string            `json:"version"`
	Description string            `json:"description,omitempty"`
	Author      string            `json:"author,omitempty"`
	Signature   string            `json:"signature,omitempty"`
	EntryPoint  string            `json:"entry_point,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// extractedPackage holds the parsed contents of a content zip.
type extractedPackage struct {
	Manifest contentPackageManifest
	Files    map[string][]byte
}

// extractZip parses a zip byte slice and returns the extracted package. The
// zip must contain a manifest.json at the root; all other files are keyed by
// their path within the archive. It is a pure function (no filesystem access).
func extractZip(data []byte) (extractedPackage, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return extractedPackage{}, fmt.Errorf("cloudaccount: open zip: %w", err)
	}

	pkg := extractedPackage{Files: make(map[string][]byte)}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return extractedPackage{}, fmt.Errorf("cloudaccount: open zip entry %q: %w", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return extractedPackage{}, fmt.Errorf("cloudaccount: read zip entry %q: %w", f.Name, err)
		}
		pkg.Files[filepath.ToSlash(f.Name)] = content
	}

	manifestData, ok := pkg.Files["manifest.json"]
	if !ok {
		return extractedPackage{}, fmt.Errorf("cloudaccount: zip missing manifest.json")
	}
	if err := json.Unmarshal(manifestData, &pkg.Manifest); err != nil {
		return extractedPackage{}, fmt.Errorf("cloudaccount: parse manifest.json: %w", err)
	}
	return pkg, nil
}

// buildRiskAssessment constructs the wire risk assessment from the parsed
// package and verification results. It is a pure function.
func buildRiskAssessment(m contentPackageManifest, signaturePresent, sha256Verified bool) gen.ContentRiskAssessment {
	risk := gen.ContentRiskAssessment{
		ContentType:      m.ContentType,
		SignaturePresent: signaturePresent,
		Sha256Verified:   sha256Verified,
		EntryPoint:       m.EntryPoint,
		Warnings:         []string{},
	}
	if !signaturePresent {
		risk.Warnings = append(risk.Warnings, "Package has no cryptographic signature (sha256-only verification).")
	}
	if !sha256Verified {
		risk.Warnings = append(risk.Warnings, "SHA256 hash mismatch — package may be corrupted or tampered.")
	}
	switch m.ContentType {
	case contentTypeNativePlugin:
		risk.Warnings = append(risk.Warnings, "Native plugin executes compiled code in-process.")
	case contentTypeSporeApp:
		risk.Warnings = append(risk.Warnings, "Spore App registers new callables accessible to agents.")
	case contentTypeWorkflowTemplate:
		risk.Warnings = append(risk.Warnings, "Workflow Template adds a new agent kind to the workspace.")
	}
	return risk
}

// ---------------------------------------------------------------------------
// contentDispatcher — abstracts the three install dispatch targets
// ---------------------------------------------------------------------------

// serviceInvoker is the narrow context slice the dispatcher needs to locate
// sibling actors and invoke their callables. actor.PureContext satisfies this.
type serviceInvoker interface {
	LookupService(name string) (ref.Ref, bool)
	Planner() actor.Planner
	Lifecycle() context.Context
}

// contentDispatcher abstracts the three install paths so install logic can be
// unit tested without real actor invocations. Dispatch routes to the
// type-specific installer based on contentType.
type contentDispatcher interface {
	Dispatch(svc serviceInvoker, contentType string, pkg extractedPackage) error
}

// runtimeDispatcher is the production dispatcher: it invokes sibling system
// actors via Planner.Call. It is stateless; the zero value is ready to use.
type runtimeDispatcher struct{}

// Dispatch routes to the installer selected by contentType.
func (runtimeDispatcher) Dispatch(svc serviceInvoker, contentType string, pkg extractedPackage) error {
	switch contentType {
	case contentTypeSporeApp:
		return runtimeDispatcher{}.dispatchSporeApp(svc, pkg)
	case contentTypeNativePlugin:
		return runtimeDispatcher{}.dispatchNativePlugin(svc, pkg)
	case contentTypeWorkflowTemplate:
		return runtimeDispatcher{}.dispatchWorkflowTemplate(svc, pkg)
	default:
		return fmt.Errorf("cloudaccount: unknown content_type %q", contentType)
	}
}

// --- Spore App → appmanager.register ---

// sporeAppPackage is the app.json inside a spore_app content zip. It bundles
// the AppManifest with the entry module path.
type sporeAppPackage struct {
	Manifest    gen.AppManifest `json:"manifest"`
	EntryModule string          `json:"entry_module"`
}

func (runtimeDispatcher) dispatchSporeApp(svc serviceInvoker, pkg extractedPackage) error {
	appData, ok := pkg.Files["app.json"]
	if !ok {
		return fmt.Errorf("cloudaccount: spore_app package missing app.json")
	}
	var appPkg sporeAppPackage
	if err := json.Unmarshal(appData, &appPkg); err != nil {
		return fmt.Errorf("cloudaccount: parse app.json: %w", err)
	}

	// Collect module sources from the zip. Any file under modules/ is treated
	// as a spore script module; the path key is the archive-relative path.
	modules := make(map[string]string)
	for path, content := range pkg.Files {
		if path == "manifest.json" || path == "app.json" {
			continue
		}
		if strings.HasPrefix(path, "modules/") && strings.HasSuffix(path, ".ss") {
			modules[path] = string(content)
		}
	}
	if appPkg.EntryModule == "" {
		return fmt.Errorf("cloudaccount: spore_app package missing entry_module in app.json")
	}
	if _, ok := modules[appPkg.EntryModule]; !ok {
		return fmt.Errorf("cloudaccount: spore_app entry module %q not found in package", appPkg.EntryModule)
	}

	appMgr, ok := svc.LookupService("appmanager")
	if !ok {
		return fmt.Errorf("cloudaccount: appmanager service not found")
	}

	req := gen.AppManagerRegisterReq{
		Manifest:    appPkg.Manifest,
		EntryModule: appPkg.EntryModule,
		Modules:     modules,
		Origin:      "user",
	}
	_, err := svc.Planner().Call(svc.Lifecycle(), appMgr, "appmanager.register", req).Await()
	if err != nil {
		return fmt.Errorf("cloudaccount: appmanager.register: %w", err)
	}
	return nil
}

// --- Native Plugin → pluginhost.artifact_load ---

// nativePluginPackage is the plugin.json inside a native_plugin content zip.
type nativePluginPackage struct {
	Manifest gen.AppManifest `json:"manifest"`
	Abi      gen.PluginAbi   `json:"abi"`
}

func (runtimeDispatcher) dispatchNativePlugin(svc serviceInvoker, pkg extractedPackage) error {
	pluginData, ok := pkg.Files["plugin.json"]
	if !ok {
		return fmt.Errorf("cloudaccount: native_plugin package missing plugin.json")
	}
	var pluginPkg nativePluginPackage
	if err := json.Unmarshal(pluginData, &pluginPkg); err != nil {
		return fmt.Errorf("cloudaccount: parse plugin.json: %w", err)
	}

	// The binary is identified by the content manifest's entry_point.
	binaryName := pkg.Manifest.EntryPoint
	if binaryName == "" {
		return fmt.Errorf("cloudaccount: native_plugin package missing entry_point (binary path)")
	}
	binaryData, ok := pkg.Files[binaryName]
	if !ok {
		return fmt.Errorf("cloudaccount: native_plugin binary %q not found in package", binaryName)
	}

	// Write the binary to a temp file — pluginhost loads from a path.
	tmpFile, err := os.CreateTemp("", "cloudcontent-*"+filepath.Ext(binaryName))
	if err != nil {
		return fmt.Errorf("cloudaccount: create temp file for native plugin: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		// Best-effort cleanup; the plugin may keep the file mapped.
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmpFile.Write(binaryData); err != nil {
		return fmt.Errorf("cloudaccount: write native plugin binary: %w", err)
	}
	_ = tmpFile.Close()

	pluginHost, ok := svc.LookupService("pluginhost")
	if !ok {
		return fmt.Errorf("cloudaccount: pluginhost service not found")
	}

	sum := sha256.Sum256(binaryData)
	req := gen.PluginArtifactLoadReq{
		Manifest:     pluginPkg.Manifest,
		Abi:          pluginPkg.Abi,
		ArtifactPath: tmpPath,
		ArtifactHash: hex.EncodeToString(sum[:]),
	}
	_, err = svc.Planner().Call(svc.Lifecycle(), pluginHost, "pluginhost.artifact_load", req).Await()
	if err != nil {
		return fmt.Errorf("cloudaccount: pluginhost.artifact_load: %w", err)
	}
	return nil
}

// --- Workflow Template → workspace.save_agent_kind_config ---

func (runtimeDispatcher) dispatchWorkflowTemplate(svc serviceInvoker, pkg extractedPackage) error {
	kindData, ok := pkg.Files["kind.json"]
	if !ok {
		return fmt.Errorf("cloudaccount: workflow_template package missing kind.json")
	}
	var kindReq gen.WorkspaceSaveAgentKindConfigReq
	if err := json.Unmarshal(kindData, &kindReq); err != nil {
		return fmt.Errorf("cloudaccount: parse kind.json: %w", err)
	}
	if kindReq.Kind == "" {
		return fmt.Errorf("cloudaccount: workflow_template kind.json missing Kind")
	}

	ws, ok := svc.LookupService("workspace")
	if !ok {
		return fmt.Errorf("cloudaccount: workspace service not found")
	}

	_, err := svc.Planner().Call(svc.Lifecycle(), ws, "workspace.save_agent_kind_config", kindReq).Await()
	if err != nil {
		return fmt.Errorf("cloudaccount: workspace.save_agent_kind_config: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleContentSearch proxies the cloud content-store list/search endpoint.
func (a *Actor) handleContentSearch(_ actor.PureContext, req gen.ContentSearchReq) (gen.ContentSearchResp, error) {
	return a.contentFetcher.SearchContent(a.actorCtx.Lifecycle(), req)
}

// handleContentDetail proxies the cloud content-store detail endpoint.
func (a *Actor) handleContentDetail(_ actor.PureContext, req gen.ContentDetailReq) (gen.ContentDetailResp, error) {
	if req.Slug == "" {
		return gen.ContentDetailResp{}, fmt.Errorf("cloudaccount.content_detail: slug is required")
	}
	return a.contentFetcher.GetContentDetail(a.actorCtx.Lifecycle(), req.Slug)
}

// handleContentInstall downloads, verifies, and dispatches a content package.
// When Confirm is false, it returns a risk assessment without installing; the
// UI must show the risk and re-call with Confirm=true to proceed.
func (a *Actor) handleContentInstall(ctx actor.PureContext, req gen.ContentInstallReq) (gen.ContentInstallResp, error) {
	if req.Slug == "" {
		return gen.ContentInstallResp{}, fmt.Errorf("cloudaccount.content_install: slug is required")
	}

	// 1. Fetch content detail for sha256 + content_type + signature.
	detail, err := a.contentFetcher.GetContentDetail(ctx.Lifecycle(), req.Slug)
	if err != nil {
		return gen.ContentInstallResp{}, fmt.Errorf("cloudaccount.content_install: %w", err)
	}

	// 2. Download the zip bytes (auth required).
	a.mu.RLock()
	accessToken := a.state.AccessToken
	linked := a.state.Linked()
	a.mu.RUnlock()
	if !linked {
		return gen.ContentInstallResp{}, fmt.Errorf("cloudaccount.content_install: no cloud account linked")
	}

	data, _, err := a.contentFetcher.DownloadContent(ctx.Lifecycle(), accessToken, req.Slug)
	if err != nil {
		return gen.ContentInstallResp{}, fmt.Errorf("cloudaccount.content_install: %w", err)
	}

	// 3. Verify SHA256 of the downloaded bytes.
	shaVerified := VerifySHA256(data, detail.Item.Sha256)
	if !shaVerified {
		return gen.ContentInstallResp{
			Slug:        req.Slug,
			ContentType: detail.Item.ContentType,
			Status:      "failed",
			Message:     "SHA256 hash mismatch — installation rejected",
			Risk:        buildRiskAssessment(contentPackageManifest{ContentType: detail.Item.ContentType}, detail.Item.Signature != "", false),
		}, fmt.Errorf("cloudaccount.content_install: sha256 mismatch for %q", req.Slug)
	}

	// 4. Parse the zip.
	pkg, err := extractZip(data)
	if err != nil {
		return gen.ContentInstallResp{
			Slug: req.Slug, ContentType: detail.Item.ContentType, Status: "failed",
			Message: err.Error(),
			Risk:    buildRiskAssessment(contentPackageManifest{ContentType: detail.Item.ContentType}, detail.Item.Signature != "", true),
		}, fmt.Errorf("cloudaccount.content_install: %w", err)
	}

	// 5. Build risk assessment.
	signaturePresent := detail.Item.Signature != "" || pkg.Manifest.Signature != ""
	risk := buildRiskAssessment(pkg.Manifest, signaturePresent, shaVerified)

	// 6. If not confirmed, return the risk for UI display.
	if !req.Confirm {
		return gen.ContentInstallResp{
			Slug:        req.Slug,
			ContentType: pkg.Manifest.ContentType,
			Status:      "risk_pending",
			Message:     "Risk assessment ready — confirm to proceed with installation",
			Risk:        risk,
		}, nil
	}

	// 7. Dispatch to the appropriate installer.
	if err := a.dispatcher.Dispatch(ctx, pkg.Manifest.ContentType, pkg); err != nil {
		resp := gen.ContentInstallResp{
			Slug: req.Slug, ContentType: pkg.Manifest.ContentType, Status: "failed",
			Message: err.Error(), Risk: risk,
		}
		return resp, err
	}

	ctx.Logger().Info("cloudaccount: content installed", "slug", req.Slug, "type", pkg.Manifest.ContentType)
	return gen.ContentInstallResp{
		Slug:        req.Slug,
		ContentType: pkg.Manifest.ContentType,
		Status:      "installed",
		Message:     "Installation successful",
		Risk:        risk,
	}, nil
}

