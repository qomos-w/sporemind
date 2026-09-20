package appmanager

import (
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// gateResult extracts one gate's result entry.
func gateResult(report gen.AppManagerDevGateResp, name string) (gen.AppManagerGateResult, bool) {
	for _, r := range report.Results {
		if r.Gate == name {
			return r, true
		}
	}
	return gen.AppManagerGateResult{}, false
}

// TestDevGateBundleDriftRoundTrip pins the drift gate end-to-end through
// handleDevGate: after a dependency re-registers with a changed bundle, the
// gate fails with a precise diff, and re-running dev_generate restores the
// match.
func TestDevGateBundleDriftRoundTrip(t *testing.T) {
	env := newBP7Project(t, "bundle-drift-")
	env.write("app/app.appdef", bundleCallerAppDef)
	a := newBP7Actor(t)
	registerTranslatorDepForCodegen(a)
	ctx := env.ctx()

	runDevGate := func() gen.AppManagerDevGateResp {
		t.Helper()
		resp, err := a.handleDevGate(ctx, gen.AppManagerDevGateReq{ProjectID: bp7ProjectID(t), AppDir: "app"})
		if err != nil {
			t.Fatalf("handleDevGate: %v", err)
		}
		if resp.Error != "" {
			t.Fatalf("handleDevGate error: %s", resp.Error)
		}
		return resp
	}

	// Baseline generation: snapshots persisted for the current dep shape.
	if resp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{ProjectID: bp7ProjectID(t), AppDir: "app"}); err != nil || resp.Error != "" {
		t.Fatalf("dev_generate: err=%v error=%s", err, resp.Error)
	}
	report := runDevGate()
	if gate, ok := gateResult(report, "bundle_drift"); !ok || !gate.Passed {
		t.Fatalf("baseline bundle_drift gate = %+v, want pass", report)
	}

	// The dependency re-registers dropping translate_new from the bundle.
	a.mu.Lock()
	manifest := a.Apps["app.translator"]
	manifest.Bundles = []gen.AppBundle{{
		Title: "Translate Tools",
		Tools: []gen.AppBundleTool{{CallableID: "translate_run"}},
	}}
	manifest.Callables = []gen.AppCallableDescriptor{
		{ID: "translate_run", RequestSchema: "TranslateRunReq", ResponseSchema: "TranslateRunResp"},
	}
	a.Apps["app.translator"] = manifest
	rec := a.Records["app.translator"]
	rec.Manifest = manifest
	rec.PackageHash = "pkg-hash-v4"
	a.Records["app.translator"] = rec
	a.mu.Unlock()

	report = runDevGate()
	gate, ok := gateResult(report, "bundle_drift")
	if !ok || gate.Passed {
		t.Fatalf("drifted bundle_drift gate = %+v, want failure", report)
	}
	detail := ""
	if gate.Error != nil {
		detail = gate.Error.Detail
	}
	if !strings.Contains(detail, "removed plugin.app.translator.translate_new") {
		t.Errorf("detail %q missing removed callable", detail)
	}
	if !strings.Contains(detail, "package hash changed") {
		t.Errorf("detail %q missing package hash drift", detail)
	}

	// Re-generating against the current dep restores the match.
	if resp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{ProjectID: bp7ProjectID(t), AppDir: "app"}); err != nil || resp.Error != "" {
		t.Fatalf("re-generate: err=%v error=%s", err, resp.Error)
	}
	report = runDevGate()
	if gate, ok := gateResult(report, "bundle_drift"); !ok || !gate.Passed {
		t.Fatalf("post-regenerate bundle_drift gate = %+v, want pass", report)
	}
}
