package sporecall

import (
	"strings"
	"testing"
)

// TestManifestModuleConsistency asserts the manifest, entry module, and
// script exports stay aligned: every declared callable has an export fun,
// the invoke permission is declared, and the register req carries the same
// manifest (deep-copy check happens in the sporeapp capability tests that
// actually load the module).
func TestManifestModuleConsistency(t *testing.T) {
	src := Modules[EntryModule]
	for _, c := range Manifest.Callables {
		if !strings.Contains(src, "export fun "+c.ID+"(") {
			t.Errorf("callable %q has no export fun in %s", c.ID, EntryModule)
		}
	}
	found := false
	for _, p := range Manifest.Permissions {
		if p == "spore.invoke" {
			found = true
		}
	}
	if !found {
		t.Error("manifest must declare the spore.invoke permission")
	}
	if Manifest.Runtime != "spore" {
		t.Fatalf("runtime = %q, want spore", Manifest.Runtime)
	}

	// Every bundle tool must reference a declared callable, or the projected
	// app-bundle would advertise a tool id that can never resolve.
	callableIDs := map[string]bool{}
	for _, c := range Manifest.Callables {
		callableIDs[c.ID] = true
	}
	if len(Manifest.Bundles) == 0 {
		t.Fatal("manifest declares no bundle: the app cannot be mounted as an app-bundle")
	}
	for _, b := range Manifest.Bundles {
		for _, tool := range b.Tools {
			if !callableIDs[tool.CallableID] {
				t.Errorf("bundle %q tool %q is not a declared callable", b.Title, tool.CallableID)
			}
		}
	}

	req := RegisterReq()
	if req.Manifest.ID != Manifest.ID || req.EntryModule != EntryModule || len(req.Modules) != len(Modules) {
		t.Fatalf("RegisterReq drift: %+v", req.Manifest)
	}
	if req.Origin != "builtin" {
		t.Fatalf("origin = %q, want builtin", req.Origin)
	}
}
