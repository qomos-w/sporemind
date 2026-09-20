package codegen

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appdef"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func bundleTestAppDef(t *testing.T, perms string, deps string) *appdef.AppDef {
	t.Helper()
	src := `app BundleCaller {
    id: app.caller
    name: Bundle Caller
    namespace: caller
    version: 1.0.0
    permissions: [` + perms + `]
` + deps + `
    callable ping {
        request: PingReq
        response: PingResp
    }
}
`
	app, diags, err := appdef.ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("diags: %v", diags)
	}
	return app
}

func translatorDep() BundleDep {
	return BundleDep{
		PackageHash: "hash-v3",
		Manifest: gen.AppManifest{
			ID: "app.translator", Name: "Translator", Version: "2.1.0", Runtime: "native",
			Schemas: []gen.AppSchemaRef{
				{Name: "TranslateRunReq", Hash: "sha-run-req"},
				{Name: "TranslateRunResp", Hash: "sha-run-resp"},
				{Name: "TranslateNewReq", Hash: "sha-new-req"},
				{Name: "Meta", Hash: "sha-meta"},
			},
			Callables: []gen.AppCallableDescriptor{
				{ID: "translate_run", RequestSchema: "TranslateRunReq", ResponseSchema: "TranslateRunResp"},
				{ID: "translate_new", RequestSchema: "TranslateNewReq"},
				{ID: "ping"},
			},
			Bundles: []gen.AppBundle{
				{Title: "Translate Tools", Tools: []gen.AppBundleTool{{CallableID: "translate_run"}, {CallableID: "translate_new"}}},
				{Title: "Ops", Tools: []gen.AppBundleTool{{CallableID: "ping"}}},
			},
		},
		Descriptors: map[string]gen.AppObjectDescriptor{
			"TranslateRunReq": {Kind: "struct", Name: "TranslateRunReq", Fields: []gen.AppFieldDescriptor{
				{Name: "Text", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "string"}},
				{Name: "Meta", Type: gen.AppTypeDescriptor{Kind: "struct", Name: "struct", ClassName: "Meta"}, Optional: true},
			}},
			"TranslateRunResp": {Kind: "struct", Name: "TranslateRunResp", Fields: []gen.AppFieldDescriptor{
				{Name: "Text", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "string"}},
				{Name: "Confidence", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "double"}},
			}},
			"TranslateNewReq": {Kind: "struct", Name: "TranslateNewReq", Fields: []gen.AppFieldDescriptor{
				{Name: "Raw", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "any"}},
			}},
			"Meta": {Kind: "struct", Name: "Meta", Fields: []gen.AppFieldDescriptor{
				{Name: "Locale", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "string"}},
			}},
		},
	}
}

func TestEmitBundleCallsTypesBundlePermission(t *testing.T) {
	app := bundleTestAppDef(t, `"plugin.app.translator.translate-tools"`, "dependency app.translator { }")
	deps := map[string]BundleDep{"app.translator": translatorDep()}

	content, snapshots, err := emitBundleCalls("main", app, deps)
	if err != nil {
		t.Fatalf("emitBundleCalls: %v", err)
	}

	for _, want := range []string{
		"type AppTranslatorTranslateRunReq struct {",
		"Text string",
		"`json:\"Text\"`",
		"`json:\"Meta,omitempty\"`",
		"type AppTranslatorTranslateRunResp struct {",
		"func CallBundleAppTranslatorTranslateRun(host sdk.Host, req AppTranslatorTranslateRunReq) (AppTranslatorTranslateRunResp, error) {",
		`host.Invoke("plugin.app.translator.translate_run", req)`,
		// translate_new has no response schema: byte-returning caller
		"func CallBundleAppTranslatorTranslateNew(host sdk.Host, req AppTranslatorTranslateNewReq) ([]byte, error) {",
		// json.RawMessage for the any-typed field and json/fmt/sdk imports
		"Raw json.RawMessage",
		"\"encoding/json\"",
		"\"fmt\"",
		`sdk "github.com/qomos-w/sporemind-plugin-sdk"`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated file missing %q\n--- content ---\n%s", want, content)
		}
	}
	if strings.Contains(content, "AppTranslatorMeta struct") == false {
		t.Errorf("nested struct Meta was not rendered")
	}
	if strings.Contains(content, "TranslateTools") {
		t.Errorf("unresolved permission leaked into typed output")
	}

	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %v, want 1", snapshots)
	}
	snap := snapshots[0]
	if snap.DepID != "app.translator" || snap.DepVersion != "2.1.0" || snap.DepPackageHash != "hash-v3" {
		t.Errorf("snapshot identity = %+v", snap)
	}
	if len(snap.Permissions) != 1 || snap.Permissions[0] != "plugin.app.translator.translate-tools" {
		t.Errorf("snapshot permissions = %v", snap.Permissions)
	}
	wantCalls := []string{"plugin.app.translator.translate_new", "plugin.app.translator.translate_run"}
	if len(snap.Callables) != 2 || snap.Callables[0] != wantCalls[0] || snap.Callables[1] != wantCalls[1] {
		t.Errorf("snapshot callables = %v", snap.Callables)
	}
	if snap.SchemaHashes["TranslateRunReq"] != "sha-run-req" || snap.SchemaHashes["Meta"] != "sha-meta" {
		t.Errorf("snapshot schema hashes = %v", snap.SchemaHashes)
	}
}

func TestEmitBundleCallsCallableLevelStaysRaw(t *testing.T) {
	app := bundleTestAppDef(t, `"plugin.app.translator.translate_run"`, "dependency app.translator { }")
	content, snapshots, err := emitBundleCalls("main", app, map[string]BundleDep{"app.translator": translatorDep()})
	if err != nil {
		t.Fatalf("emitBundleCalls: %v", err)
	}
	if len(snapshots) != 0 {
		t.Errorf("callable-level permission produced snapshots: %v", snapshots)
	}
	if strings.Contains(content, "func CallBundle") {
		t.Errorf("callable-level permission produced typed callers:\n%s", content)
	}
	if !strings.Contains(content, "intentionally empty") {
		t.Errorf("missing empty stub header:\n%s", content)
	}
}

func TestEmitBundleCallsUndeclaredDependencySkipped(t *testing.T) {
	// Permission resolves against a registered dep whose bundle exists, but
	// the app never declared the dependency — registration rejects it, so
	// codegen must not emit typed callers (nor snapshot anything).
	app := bundleTestAppDef(t, `"plugin.app.translator.translate-tools"`, "")
	content, snapshots, err := emitBundleCalls("main", app, map[string]BundleDep{"app.translator": translatorDep()})
	if err != nil {
		t.Fatalf("emitBundleCalls: %v", err)
	}
	if len(snapshots) != 0 || strings.Contains(content, "func CallBundle") {
		t.Errorf("undeclared dependency produced typed output: %v\n%s", snapshots, content)
	}
}

func TestEmitBundleCallsLongestPrefixDepMatch(t *testing.T) {
	app := bundleTestAppDef(t, `"plugin.app.translator.translate-tools"`, "dependency app.translator { }")
	base := translatorDep()
	base.Manifest.Bundles = []gen.AppBundle{{Title: "Translate Tools", Tools: []gen.AppBundleTool{{CallableID: "translate_run"}}}}
	deps := map[string]BundleDep{
		"app":            {Manifest: gen.AppManifest{ID: "app", Bundles: []gen.AppBundle{{Title: "Translator"}}}},
		"app.translator": base,
	}
	_, snapshots, err := emitBundleCalls("main", app, deps)
	if err != nil {
		t.Fatalf("emitBundleCalls: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].DepID != "app.translator" {
		t.Fatalf("snapshots = %+v, want longest-prefix dep app.translator", snapshots)
	}
}

func TestEmitBundleCallsDuplicateToolAcrossBundles(t *testing.T) {
	app := bundleTestAppDef(t, `"plugin.app.translator.translate-tools", "plugin.app.translator.ops"`, "dependency app.translator { }")
	dep := translatorDep()
	dep.Manifest.Bundles = []gen.AppBundle{
		{Title: "Translate Tools", Tools: []gen.AppBundleTool{{CallableID: "translate_run"}, {CallableID: "ping"}}},
		{Title: "Ops", Tools: []gen.AppBundleTool{{CallableID: "ping"}}},
	}
	content, snapshots, err := emitBundleCalls("main", app, map[string]BundleDep{"app.translator": dep})
	if err != nil {
		t.Fatalf("emitBundleCalls: %v", err)
	}
	if strings.Count(content, "func CallBundleAppTranslatorPing(") != 1 {
		t.Errorf("duplicate tool emitted multiple callers:\n%s", content)
	}
	snap := snapshots[0]
	for i, call := range snap.Callables {
		if i > 0 && call == snap.Callables[i-1] {
			t.Errorf("snapshot callables contain duplicates: %v", snap.Callables)
		}
	}
}

func TestEmitBundleCallsUnknownToolCallableErrors(t *testing.T) {
	app := bundleTestAppDef(t, `"plugin.app.translator.translate-tools"`, "dependency app.translator { }")
	dep := translatorDep()
	dep.Manifest.Bundles = []gen.AppBundle{{Title: "Translate Tools", Tools: []gen.AppBundleTool{{CallableID: "nonexistent"}}}}
	if _, _, err := emitBundleCalls("main", app, map[string]BundleDep{"app.translator": dep}); err == nil {
		t.Fatal("expected error for bundle tool without a manifest callable")
	}
}

func TestEmitBundleCallsMissingDescriptorErrors(t *testing.T) {
	app := bundleTestAppDef(t, `"plugin.app.translator.translate-tools"`, "dependency app.translator { }")
	dep := translatorDep()
	delete(dep.Descriptors, "TranslateRunResp")
	if _, _, err := emitBundleCalls("main", app, map[string]BundleDep{"app.translator": dep}); err == nil {
		t.Fatal("expected error for missing response descriptor")
	}
}
