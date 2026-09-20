package runtime

import (
	"sort"
	"testing"

	spore "github.com/qomos-w/spore/schema"
	"github.com/qomos-w/gospore/schema"
)

func TestGroupCallablesByKind_PassesEffectServiceToolName(t *testing.T) {
	gm := schema.GosporeManifest{
		Manifest: spore.Manifest{
			Schemas: []spore.ManifestSchema{
				{SchemaID: 1, Name: "EchoReq"},
				{SchemaID: 2, Name: "EchoFinal"},
			},
			Callables: []spore.ManifestCallable{
				{
					Namespace:     "project",
					Name:          "echo",
					Mode:          "unary",
					Effect:        "idempotent",
					Service:       "echo-service",
					ToolName:      "echo-tool",
					Visibility:    "public",
					Description:   "echoes input",
					ReqSchemaID:   1,
					FinalSchemaID: 2,
				},
			},
		},
	}

	result := groupCallablesByKind(gm)

	callables, ok := result["project"]
	if !ok {
		t.Fatal("expected callables grouped under 'project'")
	}
	if len(callables) != 1 {
		t.Fatalf("expected 1 callable, got %d", len(callables))
	}
	ci := callables[0]

	if ci.EffectKind != "idempotent" {
		t.Errorf("EffectKind = %q, want %q", ci.EffectKind, "idempotent")
	}
	if ci.ServiceName != "echo-service" {
		t.Errorf("ServiceName = %q, want %q", ci.ServiceName, "echo-service")
	}
	if ci.ToolName != "echo-tool" {
		t.Errorf("ToolName = %q, want %q", ci.ToolName, "echo-tool")
	}
}

func TestGroupCallablesByKind_ZeroValuesWhenEmpty(t *testing.T) {
	gm := schema.GosporeManifest{
		Manifest: spore.Manifest{
			Schemas: []spore.ManifestSchema{
				{SchemaID: 10, Name: "FooReq"},
				{SchemaID: 11, Name: "FooFinal"},
			},
			Callables: []spore.ManifestCallable{
				{
					Namespace:     "auth",
					Name:          "login",
					Mode:          "unary",
					Visibility:    "internal",
					ReqSchemaID:   10,
					FinalSchemaID: 11,
				},
			},
		},
	}

	result := groupCallablesByKind(gm)

	callables := result["auth"]
	if len(callables) != 1 {
		t.Fatalf("expected 1 callable, got %d", len(callables))
	}
	ci := callables[0]

	if ci.EffectKind != "" {
		t.Errorf("EffectKind = %q, want empty string", ci.EffectKind)
	}
	if ci.ServiceName != "" {
		t.Errorf("ServiceName = %q, want empty string", ci.ServiceName)
	}
	if ci.ToolName != "" {
		t.Errorf("ToolName = %q, want empty string", ci.ToolName)
	}
}

func TestGroupCallablesByKind_MixedFields(t *testing.T) {
	gm := schema.GosporeManifest{
		Manifest: spore.Manifest{
			Schemas: []spore.ManifestSchema{
				{SchemaID: 20, Name: "ReqA"},
				{SchemaID: 21, Name: "FinalA"},
				{SchemaID: 22, Name: "ReqB"},
				{SchemaID: 23, Name: "FinalB"},
			},
			Callables: []spore.ManifestCallable{
				{
					Namespace:     "project",
					Name:          "with_effect",
					Mode:          "unary",
					Effect:        "mutation",
					Visibility:    "public",
					ReqSchemaID:   20,
					FinalSchemaID: 21,
				},
				{
					Namespace:     "project",
					Name:          "with_tool",
					Mode:          "unary",
					ToolName:      "my-tool",
					Visibility:    "public",
					ReqSchemaID:   22,
					FinalSchemaID: 23,
				},
			},
		},
	}

	result := groupCallablesByKind(gm)
	callables := result["project"]
	if len(callables) != 2 {
		t.Fatalf("expected 2 callables, got %d", len(callables))
	}
	// Sort by name for deterministic order.
	sort.Slice(callables, func(i, j int) bool { return callables[i].Name < callables[j].Name })

	ci0 := callables[0] // "with_effect"
	if ci0.Name != "project.with_effect" {
		t.Errorf("Name = %q, want %q", ci0.Name, "project.with_effect")
	}
	if ci0.EffectKind != "mutation" {
		t.Errorf("EffectKind = %q, want %q", ci0.EffectKind, "mutation")
	}
	if ci0.ToolName != "" {
		t.Errorf("ToolName = %q, want empty string", ci0.ToolName)
	}

	ci1 := callables[1] // "with_tool"
	if ci1.EffectKind != "" {
		t.Errorf("EffectKind = %q, want empty string", ci1.EffectKind)
	}
	if ci1.ToolName != "my-tool" {
		t.Errorf("ToolName = %q, want %q", ci1.ToolName, "my-tool")
	}
}

func TestManifestFingerprintOf_ServiceChangesInvalidate(t *testing.T) {
	base := schema.GosporeManifest{
		Manifest: spore.Manifest{
			Callables: []spore.ManifestCallable{
				{Namespace: "a", Name: "fn1"},
				{Namespace: "b", Name: "fn2"},
			},
		},
	}

	// Same callables with Service populated — the fingerprint MUST change
	// so ensureManifestCache rebuilds after RegisterDomain backfills
	// invoker.ServiceName from "" to the domain name.
	withService := schema.GosporeManifest{
		Manifest: spore.Manifest{
			Callables: []spore.ManifestCallable{
				{Namespace: "a", Name: "fn1", Service: "svc"},
				{Namespace: "b", Name: "fn2", Service: "svc"},
			},
		},
	}

	fp1 := manifestFingerprintOf(base)
	fp2 := manifestFingerprintOf(withService)
	if fp1 == fp2 {
		t.Errorf("fingerprint unchanged after Service backfill: %q", fp1)
	}

	// Effect and ToolName still do NOT affect the fingerprint (count-based
	// for those fields is fine — they don't drive routing decisions).
	withEffect := schema.GosporeManifest{
		Manifest: spore.Manifest{
			Callables: []spore.ManifestCallable{
				{Namespace: "a", Name: "fn1", Effect: "idempotent", Service: "svc", ToolName: "tool"},
				{Namespace: "b", Name: "fn2", Effect: "mutation", Service: "svc"},
			},
		},
	}
	fp3 := manifestFingerprintOf(withEffect)
	if fp2 != fp3 {
		t.Errorf("fingerprint changed with Effect/ToolName (should be stable): %q != %q", fp2, fp3)
	}
}
