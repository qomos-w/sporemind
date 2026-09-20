package cloudaccount

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ---------------------------------------------------------------------------
// Test stubs
// ---------------------------------------------------------------------------

// stubPureContext implements actor.PureContext for unit tests. Only the
// methods exercised by the content handlers are overridden; the nil embedded
// interface satisfies the remaining methods (never called in these tests).
type stubPureContext struct {
	actor.PureContext // nil-embedded for interface satisfaction
}

func (stubPureContext) Lifecycle() context.Context            { return context.Background() }
func (stubPureContext) Logger() actor.Logger                  { return stubLogger{} }
func (stubPureContext) LookupService(string) (ref.Ref, bool) { return nil, false }
func (stubPureContext) Planner() actor.Planner                { return nil }

type stubLogger struct{}

func (stubLogger) Debug(string, ...any) {}
func (stubLogger) Info(string, ...any)  {}
func (stubLogger) Warn(string, ...any)  {}
func (stubLogger) Error(string, ...any) {}

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeContentFetcher implements contentFetcher for tests.
type fakeContentFetcher struct {
	searchResult  gen.ContentSearchResp
	searchErr     error
	detailResult  gen.ContentDetailResp
	detailErr     error
	downloadData  []byte
	downloadErr   error
	downloadCalls int
}

func (f *fakeContentFetcher) SearchContent(_ context.Context, _ gen.ContentSearchReq) (gen.ContentSearchResp, error) {
	return f.searchResult, f.searchErr
}

func (f *fakeContentFetcher) GetContentDetail(_ context.Context, _ string) (gen.ContentDetailResp, error) {
	return f.detailResult, f.detailErr
}

func (f *fakeContentFetcher) DownloadContent(_ context.Context, _, _ string) ([]byte, string, error) {
	f.downloadCalls++
	return f.downloadData, f.detailResult.Item.Sha256, f.downloadErr
}

// fakeDispatcher implements contentDispatcher for tests.
type fakeDispatcher struct {
	sporeAppErr     error
	nativePluginErr error
	workflowErr     error
	lastContentType string
}

func (d *fakeDispatcher) Dispatch(_ serviceInvoker, contentType string, _ extractedPackage) error {
	d.lastContentType = contentType
	switch contentType {
	case contentTypeSporeApp:
		return d.sporeAppErr
	case contentTypeNativePlugin:
		return d.nativePluginErr
	case contentTypeWorkflowTemplate:
		return d.workflowErr
	default:
		return fmt.Errorf("unknown content_type %q", contentType)
	}
}

// ---------------------------------------------------------------------------
// VerifySHA256 tests
// ---------------------------------------------------------------------------

func TestVerifySHA256_Match(t *testing.T) {
	data := []byte("hello world")
	sum := sha256.Sum256(data)
	expected := hex.EncodeToString(sum[:])
	if !VerifySHA256(data, expected) {
		t.Fatal("expected sha256 to match")
	}
}

func TestVerifySHA256_Mismatch(t *testing.T) {
	data := []byte("hello world")
	if VerifySHA256(data, "deadbeef") {
		t.Fatal("expected sha256 to NOT match")
	}
}

func TestVerifySHA256_EmptyExpected(t *testing.T) {
	if VerifySHA256([]byte("data"), "") {
		t.Fatal("empty expected hash must not verify")
	}
}

func TestVerifySHA256_CaseInsensitive(t *testing.T) {
	data := []byte("test payload")
	sum := sha256.Sum256(data)
	upper := strings.ToUpper(hex.EncodeToString(sum[:]))
	lower := hex.EncodeToString(sum[:])
	if !VerifySHA256(data, upper) {
		t.Fatal("uppercase hex must match")
	}
	if !VerifySHA256(data, lower) {
		t.Fatal("lowercase hex must match")
	}
}

// ---------------------------------------------------------------------------
// extractZip tests
// ---------------------------------------------------------------------------

// buildZip creates a zip archive from a map of filename → content.
func buildZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func sporeAppManifestJSON() []byte {
	m := contentPackageManifest{
		ContentType: contentTypeSporeApp,
		Name:        "Test App",
		Slug:        "test-app",
		Version:     "1.0.0",
		EntryPoint:  "modules/main.ss",
	}
	b, _ := json.Marshal(m)
	return b
}

func TestExtractZip_ValidSporeApp(t *testing.T) {
	zipData := buildZip(t, map[string][]byte{
		"manifest.json":    sporeAppManifestJSON(),
		"modules/main.ss":  []byte("// entry module"),
		"modules/util.ss":  []byte("// util module"),
		"app.json":         []byte(`{"manifest":{"id":"test-app","name":"Test App","version":"1.0.0","runtime":"spore","protocol_version":1,"namespace":"test"},"entry_module":"modules/main.ss"}`),
	})
	pkg, err := extractZip(zipData)
	if err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	if pkg.Manifest.ContentType != contentTypeSporeApp {
		t.Fatalf("content_type = %q, want spore_app", pkg.Manifest.ContentType)
	}
	if pkg.Manifest.EntryPoint != "modules/main.ss" {
		t.Fatalf("entry_point = %q", pkg.Manifest.EntryPoint)
	}
	if len(pkg.Files) != 4 {
		t.Fatalf("expected 4 files, got %d", len(pkg.Files))
	}
}

func TestExtractZip_MissingManifest(t *testing.T) {
	zipData := buildZip(t, map[string][]byte{
		"modules/main.ss": []byte("// entry"),
	})
	_, err := extractZip(zipData)
	if err == nil || !strings.Contains(err.Error(), "missing manifest.json") {
		t.Fatalf("expected missing manifest error, got: %v", err)
	}
}

func TestExtractZip_InvalidZip(t *testing.T) {
	_, err := extractZip([]byte("not a zip"))
	if err == nil {
		t.Fatal("expected error for invalid zip")
	}
}

// ---------------------------------------------------------------------------
// buildRiskAssessment tests
// ---------------------------------------------------------------------------

func TestBuildRiskAssessment_NoSignature(t *testing.T) {
	m := contentPackageManifest{ContentType: contentTypeSporeApp, EntryPoint: "app.json"}
	risk := buildRiskAssessment(m, false, true)
	if risk.SignaturePresent {
		t.Fatal("expected SignaturePresent=false")
	}
	if !risk.Sha256Verified {
		t.Fatal("expected Sha256Verified=true")
	}
	found := false
	for _, w := range risk.Warnings {
		if strings.Contains(w, "no cryptographic signature") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected warning about missing signature")
	}
}

func TestBuildRiskAssessment_ShaMismatch(t *testing.T) {
	m := contentPackageManifest{ContentType: contentTypeNativePlugin}
	risk := buildRiskAssessment(m, true, false)
	if !risk.SignaturePresent {
		t.Fatal("expected SignaturePresent=true")
	}
	if risk.Sha256Verified {
		t.Fatal("expected Sha256Verified=false")
	}
	found := false
	for _, w := range risk.Warnings {
		if strings.Contains(w, "SHA256 hash mismatch") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected warning about sha256 mismatch")
	}
}

func TestBuildRiskAssessment_NativePluginWarning(t *testing.T) {
	risk := buildRiskAssessment(contentPackageManifest{ContentType: contentTypeNativePlugin}, true, true)
	found := false
	for _, w := range risk.Warnings {
		if strings.Contains(w, "compiled code in-process") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected native plugin risk warning")
	}
}

// ---------------------------------------------------------------------------
// handleContentInstall tests
// ---------------------------------------------------------------------------

func newInstallActor(detail gen.ContentDetailResp, downloadData []byte, disp contentDispatcher) *Actor {
	return &Actor{
		state: CloudAccountState{
			AccountID:   "acct-1",
			AccessToken: "token",
		},
		contentFetcher: &fakeContentFetcher{
			detailResult: detail,
			downloadData: downloadData,
		},
		dispatcher: disp,
	}
}

func TestHandleContentInstall_SHA256Mismatch_RejectsInstall(t *testing.T) {
	data := []byte("corrupt data")
	detail := gen.ContentDetailResp{
		Item: gen.ContentListItem{
			Slug:        "bad-pkg",
			ContentType: contentTypeSporeApp,
			Sha256:      "0000000000000000000000000000000000000000000000000000000000000000",
		},
	}

	disp := &fakeDispatcher{}
	a := newInstallActor(detail, data, disp)

	resp, err := a.handleContentInstall(stubPureContext{}, gen.ContentInstallReq{
		Slug: "bad-pkg", Confirm: true,
	})

	if err == nil {
		t.Fatal("expected error on sha256 mismatch")
	}
	if resp.Status != "failed" {
		t.Fatalf("status = %q, want failed", resp.Status)
	}
	if disp.lastContentType != "" {
		t.Fatal("dispatcher must not be called on sha256 mismatch")
	}
}

func TestHandleContentInstall_ConfirmFalse_ReturnsRiskPending(t *testing.T) {
	zipData := buildZip(t, map[string][]byte{
		"manifest.json":   sporeAppManifestJSON(),
		"modules/main.ss": []byte("// entry"),
		"app.json":        []byte(`{"manifest":{"id":"test-app","name":"Test","version":"1.0.0","runtime":"spore","protocol_version":1,"namespace":"test"},"entry_module":"modules/main.ss"}`),
	})
	sum := sha256.Sum256(zipData)
	sha := hex.EncodeToString(sum[:])

	detail := gen.ContentDetailResp{
		Item: gen.ContentListItem{
			Slug:        "test-app",
			ContentType: contentTypeSporeApp,
			Sha256:      sha,
			Signature:   "",
		},
	}

	disp := &fakeDispatcher{}
	a := newInstallActor(detail, zipData, disp)

	resp, err := a.handleContentInstall(stubPureContext{}, gen.ContentInstallReq{
		Slug: "test-app", Confirm: false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "risk_pending" {
		t.Fatalf("status = %q, want risk_pending", resp.Status)
	}
	if disp.lastContentType != "" {
		t.Fatal("dispatcher must not be called when Confirm=false")
	}
	if resp.Risk.Sha256Verified != true {
		t.Fatal("expected Sha256Verified=true in risk")
	}
}

func TestHandleContentInstall_SporeApp_Dispatch(t *testing.T) {
	zipData := buildZip(t, map[string][]byte{
		"manifest.json":   sporeAppManifestJSON(),
		"modules/main.ss": []byte("// entry"),
		"app.json":        []byte(`{"manifest":{"id":"test-app","name":"Test","version":"1.0.0","runtime":"spore","protocol_version":1,"namespace":"test"},"entry_module":"modules/main.ss"}`),
	})
	sum := sha256.Sum256(zipData)
	sha := hex.EncodeToString(sum[:])

	detail := gen.ContentDetailResp{
		Item: gen.ContentListItem{
			Slug:        "test-app",
			ContentType: contentTypeSporeApp,
			Sha256:      sha,
		},
	}

	disp := &fakeDispatcher{}
	a := newInstallActor(detail, zipData, disp)

	resp, err := a.handleContentInstall(stubPureContext{}, gen.ContentInstallReq{
		Slug: "test-app", Confirm: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "installed" {
		t.Fatalf("status = %q, want installed", resp.Status)
	}
	if disp.lastContentType != contentTypeSporeApp {
		t.Fatalf("dispatcher called with %q, want spore_app", disp.lastContentType)
	}
}

func TestHandleContentInstall_NativePlugin_Dispatch(t *testing.T) {
	cm := contentPackageManifest{ContentType: contentTypeNativePlugin, EntryPoint: "plugin.so", Slug: "test-plugin", Name: "Test Plugin", Version: "1.0.0"}
	cmJSON, _ := json.Marshal(cm)
	zipData := buildZip(t, map[string][]byte{
		"manifest.json": cmJSON,
		"plugin.so":     []byte("fake binary"),
		"plugin.json":   []byte(`{"manifest":{"id":"test-plugin","name":"Test","version":"1.0.0","runtime":"native","protocol_version":1,"namespace":"test"},"abi":{"name":"wasm","version":1,"encoding":"binary","invoke_symbol":"PluginInvoke"}}`),
	})
	sum := sha256.Sum256(zipData)
	sha := hex.EncodeToString(sum[:])

	detail := gen.ContentDetailResp{
		Item: gen.ContentListItem{Slug: "test-plugin", ContentType: contentTypeNativePlugin, Sha256: sha},
	}

	disp := &fakeDispatcher{}
	a := newInstallActor(detail, zipData, disp)

	resp, err := a.handleContentInstall(stubPureContext{}, gen.ContentInstallReq{
		Slug: "test-plugin", Confirm: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "installed" {
		t.Fatalf("status = %q, want installed", resp.Status)
	}
	if disp.lastContentType != contentTypeNativePlugin {
		t.Fatalf("dispatcher called with %q, want native_plugin", disp.lastContentType)
	}
}

func TestHandleContentInstall_WorkflowTemplate_Dispatch(t *testing.T) {
	cm := contentPackageManifest{ContentType: contentTypeWorkflowTemplate, EntryPoint: "kind.json", Slug: "test-wf", Name: "Test WF", Version: "1.0.0"}
	cmJSON, _ := json.Marshal(cm)
	kindJSON := []byte(`{"Kind":"test-workflow","DisplayName":"Test Workflow","UserCreatable":true}`)
	zipData := buildZip(t, map[string][]byte{
		"manifest.json": cmJSON,
		"kind.json":     kindJSON,
	})
	sum := sha256.Sum256(zipData)
	sha := hex.EncodeToString(sum[:])

	detail := gen.ContentDetailResp{
		Item: gen.ContentListItem{Slug: "test-wf", ContentType: contentTypeWorkflowTemplate, Sha256: sha},
	}

	disp := &fakeDispatcher{}
	a := newInstallActor(detail, zipData, disp)

	resp, err := a.handleContentInstall(stubPureContext{}, gen.ContentInstallReq{
		Slug: "test-wf", Confirm: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "installed" {
		t.Fatalf("status = %q, want installed", resp.Status)
	}
	if disp.lastContentType != contentTypeWorkflowTemplate {
		t.Fatalf("dispatcher called with %q, want workflow_template", disp.lastContentType)
	}
}

func TestHandleContentInstall_DispatchFailure_ReturnsFailed(t *testing.T) {
	zipData := buildZip(t, map[string][]byte{
		"manifest.json":   sporeAppManifestJSON(),
		"modules/main.ss": []byte("// entry"),
		"app.json":        []byte(`{"manifest":{"id":"test-app","name":"Test","version":"1.0.0","runtime":"spore","protocol_version":1,"namespace":"test"},"entry_module":"modules/main.ss"}`),
	})
	sum := sha256.Sum256(zipData)
	sha := hex.EncodeToString(sum[:])

	detail := gen.ContentDetailResp{
		Item: gen.ContentListItem{Slug: "test-app", ContentType: contentTypeSporeApp, Sha256: sha},
	}

	disp := &fakeDispatcher{sporeAppErr: errors.New("appmanager unavailable")}
	a := newInstallActor(detail, zipData, disp)

	resp, err := a.handleContentInstall(stubPureContext{}, gen.ContentInstallReq{
		Slug: "test-app", Confirm: true,
	})
	if err == nil {
		t.Fatal("expected error on dispatch failure")
	}
	if resp.Status != "failed" {
		t.Fatalf("status = %q, want failed", resp.Status)
	}
}

func TestHandleContentInstall_NotLinked_Rejects(t *testing.T) {
	zipData := buildZip(t, map[string][]byte{
		"manifest.json": sporeAppManifestJSON(),
	})
	sum := sha256.Sum256(zipData)
	sha := hex.EncodeToString(sum[:])

	detail := gen.ContentDetailResp{
		Item: gen.ContentListItem{Slug: "test-app", ContentType: contentTypeSporeApp, Sha256: sha},
	}

	a := &Actor{
		state:          CloudAccountState{}, // not linked
		contentFetcher: &fakeContentFetcher{detailResult: detail, downloadData: zipData},
		dispatcher:     &fakeDispatcher{},
	}

	_, err := a.handleContentInstall(stubPureContext{}, gen.ContentInstallReq{
		Slug: "test-app", Confirm: true,
	})
	if err == nil || !strings.Contains(err.Error(), "no cloud account linked") {
		t.Fatalf("expected not-linked error, got: %v", err)
	}
}

func TestHandleContentInstall_EmptySlug(t *testing.T) {
	a := &Actor{
		contentFetcher: &fakeContentFetcher{},
		dispatcher:     &fakeDispatcher{},
	}
	_, err := a.handleContentInstall(stubPureContext{}, gen.ContentInstallReq{Slug: ""})
	if err == nil || !strings.Contains(err.Error(), "slug is required") {
		t.Fatalf("expected slug-required error, got: %v", err)
	}
}

func TestRuntimeDispatch_UnknownContentType(t *testing.T) {
	d := runtimeDispatcher{}
	err := d.Dispatch(stubPureContext{}, "unknown_type", extractedPackage{})
	if err == nil || !strings.Contains(err.Error(), "unknown content_type") {
		t.Fatalf("expected unknown content_type error, got: %v", err)
	}
}

func TestRuntimeDispatch_SporeApp_MissingAppJSON(t *testing.T) {
	d := runtimeDispatcher{}
	pkg := extractedPackage{
		Manifest: contentPackageManifest{ContentType: contentTypeSporeApp},
		Files:    map[string][]byte{"manifest.json": {}},
	}
	err := d.dispatchSporeApp(stubPureContext{}, pkg)
	if err == nil || !strings.Contains(err.Error(), "missing app.json") {
		t.Fatalf("expected missing app.json error, got: %v", err)
	}
}

func TestRuntimeDispatch_NativePlugin_MissingPluginJSON(t *testing.T) {
	d := runtimeDispatcher{}
	pkg := extractedPackage{
		Manifest: contentPackageManifest{ContentType: contentTypeNativePlugin, EntryPoint: "plugin.so"},
		Files:    map[string][]byte{"manifest.json": {}, "plugin.so": {}},
	}
	err := d.dispatchNativePlugin(stubPureContext{}, pkg)
	if err == nil || !strings.Contains(err.Error(), "missing plugin.json") {
		t.Fatalf("expected missing plugin.json error, got: %v", err)
	}
}

func TestRuntimeDispatch_WorkflowTemplate_MissingKindJSON(t *testing.T) {
	d := runtimeDispatcher{}
	pkg := extractedPackage{
		Manifest: contentPackageManifest{ContentType: contentTypeWorkflowTemplate},
		Files:    map[string][]byte{"manifest.json": {}},
	}
	err := d.dispatchWorkflowTemplate(stubPureContext{}, pkg)
	if err == nil || !strings.Contains(err.Error(), "missing kind.json") {
		t.Fatalf("expected missing kind.json error, got: %v", err)
	}
}
