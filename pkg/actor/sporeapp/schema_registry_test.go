package sporeapp

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/schema"
	sporesch "github.com/qomos-w/spore/schema"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestRegisterAppSchemasEmpty(t *testing.T) {
	a := &Actor{
		Manifest: gen.AppManifest{ID: "test.app", Runtime: "spore"},
		schemas:  nil, // no ctx schemas
	}
	result, err := a.registerAppSchemas()
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("expected nil reader for empty schemas and no ctx schemas")
	}
}

func TestRegisterAppSchemasPreservesFallback(t *testing.T) {
	fallback, err := schema.New("test")
	if err != nil {
		t.Fatal(err)
	}
	// Register a known schema in fallback.
	_ = fallback.Register(100, "KnownType",
		sporesch.TypeDesc{Kind: sporesch.TypeKindScalar, Name: "KnownType"},
		sporesch.ObjectDesc{Kind: sporesch.TypeKindStruct, Name: "KnownType"})

	a := &Actor{
		Manifest: gen.AppManifest{ID: "test.app", Runtime: "spore", Namespace: "test"},
		schemas:  fallback,
	}
	result, err := a.registerAppSchemas()
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil reader")
	}
	// The fallback schema should still be accessible.
	entry, ok := result.LookupByName("KnownType")
	if !ok {
		t.Fatal("expected KnownType from fallback")
	}
	if entry.Name != "KnownType" {
		t.Fatalf("expected KnownType, got %s", entry.Name)
	}
}

func testAppDescriptor(name string, id uint64) gen.AppObjectDescriptor {
	return gen.AppObjectDescriptor{
		Kind: string(sporesch.TypeKindStruct), Name: name, SchemaID: int64(id),
		Fields: []gen.AppFieldDescriptor{{Name: "Action", Type: gen.AppTypeDescriptor{Kind: string(sporesch.TypeKindScalar), Name: "string"}}},
	}
}

func TestRegisterAppSchemasRejectsMissingDescriptor(t *testing.T) {
	fallback, _ := schema.New("test")
	a := &Actor{
		Manifest: gen.AppManifest{
			ID: "test.app", Runtime: "spore", Namespace: "test",
			Schemas: []gen.AppSchemaRef{
				{Name: "UnknownSchema", Hash: "abc123", SchemaID: 99999}, // not in global registry
			},
		},
		schemas: fallback,
	}
	_, err := a.registerAppSchemas()
	if err == nil {
		t.Fatal("expected missing descriptor rejection")
	}
}

func TestRegisterAppSchemasWithKnownType(t *testing.T) {
	// Register a test type in the global registry.
	const testSchemaID = uint64(1<<20 + 1) // user-space ID unlikely to collide
	type TestPayload struct {
		Action string `json:"action"`
	}
	sporesch.RegisterStructType(testSchemaID, reflect.TypeOf(TestPayload{}))

	fallback, _ := schema.New("test")
	a := &Actor{
		Manifest: gen.AppManifest{
			ID: "test.app", Runtime: "spore", Namespace: "testapp",
			Schemas: []gen.AppSchemaRef{
				{Name: "TestPayload", Hash: "abc123", SchemaID: int64(testSchemaID)},
			},
		},
		schemas:           fallback,
		SchemaDescriptors: map[string]gen.AppObjectDescriptor{"TestPayload": testAppDescriptor("TestPayload", testSchemaID)},
	}
	result, err := a.registerAppSchemas()
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil reader")
	}
	entry, ok := result.LookupByName("TestPayload")
	if !ok {
		t.Fatal("expected TestPayload in overlay")
	}
	if entry.ID != testSchemaID {
		t.Fatalf("expected ID %d, got %d", testSchemaID, entry.ID)
	}
}

func TestRegisterAppSchemasCombines(t *testing.T) {
	fallback, _ := schema.New("test")
	_ = fallback.Register(100, "FallbackType",
		sporesch.TypeDesc{Kind: sporesch.TypeKindScalar, Name: "FallbackType"},
		sporesch.ObjectDesc{Kind: sporesch.TypeKindStruct, Name: "FallbackType"})

	// Register a test type in the global registry.
	const testSchemaID = uint64(1<<20 + 2)
	type TestPayload struct {
		Action string `json:"action"`
	}
	sporesch.RegisterStructType(testSchemaID, reflect.TypeOf(TestPayload{}))

	a := &Actor{
		Manifest: gen.AppManifest{
			ID: "test.app", Runtime: "spore", Namespace: "testapp",
			Schemas: []gen.AppSchemaRef{
				{Name: "OverlayType", Hash: "abc123", SchemaID: int64(testSchemaID)},
			},
		},
		schemas:           fallback,
		SchemaDescriptors: map[string]gen.AppObjectDescriptor{"OverlayType": testAppDescriptor("OverlayType", testSchemaID)},
	}
	result, err := a.registerAppSchemas()
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil reader")
	}
	// Both overlay and fallback types should be accessible.
	if _, ok := result.LookupByName("OverlayType"); !ok {
		t.Fatal("expected OverlayType from overlay")
	}
	if _, ok := result.LookupByName("FallbackType"); !ok {
		t.Fatal("expected FallbackType from fallback")
	}
	if result.Len() < 2 {
		t.Fatalf("expected Len >= 2, got %d", result.Len())
	}
}

func TestSchemaRegistryDebug(t *testing.T) {
	a := &Actor{
		Manifest: gen.AppManifest{ID: "debug.app", Runtime: "spore"},
		schemas:  nil,
	}
	msg := a.SchemaRegistryDebug()
	if msg == "" {
		t.Fatal("expected non-empty debug message")
	}
}

func TestBuildDescriptorsFromStruct(t *testing.T) {
	type TestStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	typ := reflect.TypeOf(TestStruct{})
	td, od := buildDescriptors(typ, "TestStruct", 500)

	if td.Kind != sporesch.TypeKindStruct {
		t.Fatalf("expected struct kind, got %s", td.Kind)
	}
	if td.Name != "TestStruct" {
		t.Fatalf("expected TestStruct, got %s", td.Name)
	}
	if td.TypeID != 500 {
		t.Fatalf("expected TypeID 500, got %d", td.TypeID)
	}
	if len(od.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(od.Fields))
	}
	if od.Fields[0].Name != "name" {
		t.Fatalf("expected first field 'name', got %s", od.Fields[0].Name)
	}
}
