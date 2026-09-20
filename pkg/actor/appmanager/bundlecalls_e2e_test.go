package appmanager

import (
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// This file covers task 5 of the bundle-shell plan: dev_generate resolves
// bundle-level `plugin.<dep>.<bundle>` permissions against the REGISTERED
// dependency manifests, emits typed bundle_calls.gen.go callers, and persists
// the expansion snapshots into GeneratedManifests (input for the dev_gate
// drift gate).

const bundleCallerAppDef = `app BundleCaller {
    id:          "app.bundlecaller"
    name:        "BundleCaller"
    version:     "0.1.0"
    namespace:   bundlecaller

    permissions: ["plugin.app.translator.translate-tools"]

    dependency app.translator {
        version: "2.1.0"
    }

    struct LocalReq {
        Q: string
    }
    struct LocalResp {
        A: string
    }

    callable local {
        request:  LocalReq
        response: LocalResp
    }
}
`

// registerTranslatorDepForCodegen seeds the actor registry with a registered
// dependency app carrying one bundle ("Translate Tools" → slug
// translate-tools) with two typed tools.
func registerTranslatorDepForCodegen(a *Actor) {
	manifest := gen.AppManifest{
		ID: "app.translator", Name: "Translator", Version: "2.1.0", Runtime: "native", ProtocolVersion: 2,
		Schemas: []gen.AppSchemaRef{
			{Name: "TranslateRunReq", Hash: "sha-run-req"},
			{Name: "TranslateRunResp", Hash: "sha-run-resp"},
		},
		Callables: []gen.AppCallableDescriptor{
			{ID: "translate_run", RequestSchema: "TranslateRunReq", ResponseSchema: "TranslateRunResp"},
			{ID: "translate_new"},
		},
		Bundles: []gen.AppBundle{
			{Title: "Translate Tools", Tools: []gen.AppBundleTool{{CallableID: "translate_run"}, {CallableID: "translate_new"}}},
		},
	}
	a.Apps["app.translator"] = manifest
	a.Records["app.translator"] = appRecord{
		Manifest:          manifest,
		SchemaDescriptors: translatorCodegenDescriptors(),
		PackageHash:       "pkg-hash-v3",
		State:             "running",
	}
}

func translatorCodegenDescriptors() map[string]gen.AppObjectDescriptor {
	return map[string]gen.AppObjectDescriptor{
		"TranslateRunReq": {Kind: "struct", Name: "TranslateRunReq", Fields: []gen.AppFieldDescriptor{
			{Name: "Text", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "string"}},
		}},
		"TranslateRunResp": {Kind: "struct", Name: "TranslateRunResp", Fields: []gen.AppFieldDescriptor{
			{Name: "Text", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "string"}},
		}},
	}
}

func TestDevGenerateEmitsTypedBundleCallsAndSnapshots(t *testing.T) {
	env := newBP7Project(t, "bundle-calls-")
	env.write("app/app.appdef", bundleCallerAppDef)
	a := newBP7Actor(t)
	registerTranslatorDepForCodegen(a)
	ctx := env.ctx()

	resp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID: bp7ProjectID(t),
		AppDir:    "app",
	})
	if err != nil {
		t.Fatalf("handleDevGenerate: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("dev_generate error: %s", resp.Error)
	}

	content, ok := env.readRel("app/bundle_calls.gen.go")
	if !ok {
		t.Fatal("bundle_calls.gen.go was not generated")
	}
	for _, want := range []string{
		"type AppTranslatorTranslateRunReq struct {",
		"type AppTranslatorTranslateRunResp struct {",
		"func CallBundleAppTranslatorTranslateRun(host sdk.Host, req AppTranslatorTranslateRunReq) (AppTranslatorTranslateRunResp, error) {",
		"func CallBundleAppTranslatorTranslateNew(host sdk.Host) ([]byte, error) {",
		`host.Invoke("plugin.app.translator.translate_run", req)`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("bundle_calls.gen.go missing %q", want)
		}
	}

	// The snapshot is persisted for the drift gate.
	manifest, ok := a.GeneratedManifests[bp7ProjectID(t)+"::app"]
	if !ok {
		t.Fatalf("GeneratedManifests missing app key; keys: %v", mapKeys(a.GeneratedManifests))
	}
	if len(manifest.BundleSnapshots) != 1 {
		t.Fatalf("BundleSnapshots = %+v, want 1", manifest.BundleSnapshots)
	}
	snap := manifest.BundleSnapshots[0]
	if snap.DepID != "app.translator" || snap.DepVersion != "2.1.0" || snap.DepPackageHash != "pkg-hash-v3" {
		t.Errorf("snapshot identity = %+v", snap)
	}
	if len(snap.Callables) != 2 ||
		snap.Callables[0] != "plugin.app.translator.translate_new" ||
		snap.Callables[1] != "plugin.app.translator.translate_run" {
		t.Errorf("snapshot callables = %v", snap.Callables)
	}
	if snap.SchemaHashes["TranslateRunReq"] != "sha-run-req" {
		t.Errorf("snapshot schema hashes = %v", snap.SchemaHashes)
	}

	// bundle_calls.gen.go is write-protected alongside the other artifacts.
	env.mu.Lock()
	protected := append([]string{}, env.protectedFiles...)
	env.mu.Unlock()
	found := false
	for _, p := range protected {
		if p == "app/bundle_calls.gen.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("protected files missing bundle_calls.gen.go: %v", protected)
	}
}

func TestDevGenerateWithoutBundleDepsOmitsBundleCallsFile(t *testing.T) {
	env := newBP7Project(t, "bundle-calls-nodep-")
	env.write("app/app.appdef", bundleCallerAppDef)
	a := newBP7Actor(t) // no registered dependencies
	ctx := env.ctx()

	resp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID: bp7ProjectID(t),
		AppDir:    "app",
	})
	if err != nil {
		t.Fatalf("handleDevGenerate: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("dev_generate error: %s", resp.Error)
	}
	if env.exists("app/bundle_calls.gen.go") {
		t.Error("bundle_calls.gen.go must not be written when nothing resolves")
	}
	if manifest, ok := a.GeneratedManifests[bp7ProjectID(t)+"::app"]; ok && len(manifest.BundleSnapshots) != 0 {
		t.Errorf("BundleSnapshots = %v, want none", manifest.BundleSnapshots)
	}
}
