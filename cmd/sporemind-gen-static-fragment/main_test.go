package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qomos-w/spore/schema"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

func TestBuildEntriesUsesFilenameOffset(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "alpha._200.spore", `
struct First {}
@schema(999)
struct Second {}
struct Third {}
`)

	files, err := collectSchemaFiles(dir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	entries, err := buildEntries(files, protocol.SystemNamespace)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	byName := make(map[string]uint64)
	for _, e := range entries {
		byName[e.Name] = e.SchemaID
	}

	if got, want := byName["First"], uint64(200); got != want {
		t.Errorf("First id = %d, want %d", got, want)
	}
	if got, want := byName["Second"], uint64(201); got != want {
		t.Errorf("Second id = %d, want %d (explicit @schema should be ignored)", got, want)
	}
	if got, want := byName["Third"], uint64(202); got != want {
		t.Errorf("Third id = %d, want %d", got, want)
	}
}

func TestBuildEntriesRequiresBaseID(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "alpha.spore", `struct First {}`)

	files, err := collectSchemaFiles(dir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	_, err = buildEntries(files, protocol.SystemNamespace)
	if err == nil {
		t.Fatal("expected error for file without ._N base id")
	}
}

func TestBuildEntriesDetectsOverlappingIDs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "alpha._200.spore", `struct First {}`)  // 200
	writeFile(t, dir, "beta._200.spore", `struct Second {}`) // also 200

	files, err := collectSchemaFiles(dir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	_, err = buildEntries(files, protocol.SystemNamespace)
	if err == nil {
		t.Fatal("expected error for overlapping schema ids")
	}
}

func TestBuildEntriesMergesIdenticalDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "alpha._200.spore", `
struct First {
    optional X: string
}
`)
	writeFile(t, dir, "beta._200.spore", `
struct First {
    optional X: string
}
`)

	files, err := collectSchemaFiles(dir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	entries, err := buildEntries(files, protocol.SystemNamespace)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 merged entry, got %d", len(entries))
	}
	if entries[0].SchemaID != 200 {
		t.Errorf("merged entry id = %d, want 200", entries[0].SchemaID)
	}
}

func TestObjectEqual(t *testing.T) {
	a := schema.ObjectDesc{Kind: schema.TypeKindStruct, Name: "A"}
	b := schema.ObjectDesc{Kind: schema.TypeKindStruct, Name: "A"}
	if !objectEqual(a, b) {
		t.Error("expected equal")
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
