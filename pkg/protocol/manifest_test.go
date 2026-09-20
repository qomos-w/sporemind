package protocol

import (
	"testing"

	sporeschema "github.com/qomos-w/spore/schema"
)

func TestNormalizeManifestSchemasToSystem(t *testing.T) {
	got := NormalizeManifestSchemasToSystem([]sporeschema.ManifestSchema{
		{SchemaID: 171, Namespace: "agent.chat", Name: "Resp"},
		{SchemaID: 170, Namespace: "workspace", Name: "Req"},
		{SchemaID: 170, Namespace: "system", Name: "Req"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d schemas, want 2", len(got))
	}
	if got[0].SchemaID != 170 || got[1].SchemaID != 171 {
		t.Fatalf("schema IDs = %d, %d; want 170, 171", got[0].SchemaID, got[1].SchemaID)
	}
	for _, schema := range got {
		if schema.Namespace != SystemNamespace {
			t.Errorf("schema %d namespace = %q, want %q", schema.SchemaID, schema.Namespace, SystemNamespace)
		}
	}
}

// TestNormalizeManifestSchemasDedupByName covers the case where a struct
// declared in the shared domain package surfaces under several runtime-assigned
// schema IDs (one per actor cell). Only the lowest ID must survive so the TS
// schema registry has no duplicate keys.
func TestNormalizeManifestSchemasDedupByName(t *testing.T) {
	got := NormalizeManifestSchemasToSystem([]sporeschema.ManifestSchema{
		{SchemaID: 128, Namespace: "agent", Name: "ComponentVisual"},
		{SchemaID: 129, Namespace: "workspace", Name: "ComponentVisual"},
		{SchemaID: 132, Namespace: "project", Name: "ComponentVisual"},
		{SchemaID: 130, Namespace: "agent", Name: "AuditRecord"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d schemas, want 2 (ComponentVisual collapsed): %+v", len(got), got)
	}
	// ComponentVisual collapses to its lowest ID (128).
	if got[0].SchemaID != 128 || got[0].Name != "ComponentVisual" {
		t.Errorf("first = %+v, want ComponentVisual/128", got[0])
	}
	if got[1].SchemaID != 130 || got[1].Name != "AuditRecord" {
		t.Errorf("second = %+v, want AuditRecord/130", got[1])
	}
}

// TestNormalizeManifestSchemasVisibilityMerge covers a type that surfaces as
// an internal callable payload under one walk namespace and as a public event
// payload under another. The deduped entry must keep the widest visibility so
// generated event clients that reference the type still compile.
func TestNormalizeManifestSchemasVisibilityMerge(t *testing.T) {
	got := NormalizeManifestSchemasToSystem([]sporeschema.ManifestSchema{
		{SchemaID: 2623, Namespace: "mcp", Name: "McpServerStatusEvent", Visibility: "internal"},
		{SchemaID: 2623, Namespace: "mcpmanager", Name: "McpServerStatusEvent", Visibility: "public"},
	})
	if len(got) != 1 {
		t.Fatalf("got %d schemas, want 1: %+v", len(got), got)
	}
	if got[0].Visibility != "public" {
		t.Errorf("visibility = %q, want %q (public event payload must not be narrowed by an internal duplicate)", got[0].Visibility, "public")
	}
}

// Same-name dedup across different schema IDs must also merge visibility,
// whether the lower or the higher ID arrives first.
func TestNormalizeManifestSchemasVisibilityMergeAcrossIDs(t *testing.T) {
	lowerFirst := NormalizeManifestSchemasToSystem([]sporeschema.ManifestSchema{
		{SchemaID: 128, Namespace: "agent", Name: "ComponentVisual", Visibility: "internal"},
		{SchemaID: 130, Namespace: "project", Name: "ComponentVisual", Visibility: "admin"},
	})
	if len(lowerFirst) != 1 || lowerFirst[0].SchemaID != 128 || lowerFirst[0].Visibility != "admin" {
		t.Errorf("lowerFirst = %+v, want ComponentVisual/128 with admin visibility", lowerFirst)
	}

	higherFirst := NormalizeManifestSchemasToSystem([]sporeschema.ManifestSchema{
		{SchemaID: 130, Namespace: "project", Name: "ComponentVisual", Visibility: "public"},
		{SchemaID: 128, Namespace: "agent", Name: "ComponentVisual", Visibility: "internal"},
	})
	if len(higherFirst) != 1 || higherFirst[0].SchemaID != 128 || higherFirst[0].Visibility != "public" {
		t.Errorf("higherFirst = %+v, want ComponentVisual/128 with public visibility", higherFirst)
	}
}
