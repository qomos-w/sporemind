package demoapp

import (
	"testing"

	"github.com/qomos-w/spore/script"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestManifestValidates checks the demo manifest has all required fields.
func TestManifestValidates(t *testing.T) {
	m := Manifest
	if m.ID == "" || m.Name == "" || m.Namespace == "" || m.Version == "" {
		t.Fatal("manifest missing required fields")
	}
	if m.Runtime != "spore" {
		t.Fatalf("expected runtime 'spore', got %q", m.Runtime)
	}
	if m.ProtocolVersion <= 0 {
		t.Fatal("protocol version must be positive")
	}
	if len(m.Callables) == 0 {
		t.Fatal("manifest must declare callables")
	}
	for _, c := range m.Callables {
		if c.ID == "" || (c.RequestSchema == "") != (c.ResponseSchema == "") {
			t.Fatalf("callable descriptor incomplete: %+v", c)
		}
	}
}

// TestCallableIDsUnique ensures no duplicate callable IDs.
func TestCallableIDsUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Manifest.Callables {
		if seen[c.ID] {
			t.Fatalf("duplicate callable ID: %q", c.ID)
		}
		seen[c.ID] = true
	}
}

// TestEntrypointsValid ensures all entrypoints have required fields.
func TestEntrypointsValid(t *testing.T) {
	if len(Manifest.Entrypoints) == 0 {
		t.Fatal("manifest should have entrypoints")
	}
	for _, e := range Manifest.Entrypoints {
		if e.ID == "" || e.Kind == "" || e.Title == "" {
			t.Fatalf("entrypoint incomplete: %+v", e)
		}
	}
}

// TestAgentBindingConsistent verifies the binding references valid callables.
func TestAgentBindingConsistent(t *testing.T) {
	if Manifest.AgentBinding == nil {
		t.Fatal("expected AgentBinding in demo manifest")
	}
	if Manifest.AgentBinding.Capability == nil {
		t.Fatal("expected Capability binding")
	}
	// Every capability callable must exist in manifest
	callableSet := map[string]bool{}
	for _, c := range Manifest.Callables {
		callableSet[c.ID] = true
	}
	for _, c := range Manifest.AgentBinding.Capability.Callables {
		if !callableSet[c] {
			t.Fatalf("capability references unknown callable %q", c)
		}
	}
}

// TestModulesCompile verifies the spore script compiles and callables execute.
func TestModulesCompile(t *testing.T) {
	rt, err := script.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer rt.Close()

	pkg := script.Package{
		AppID:       Manifest.ID,
		Version:     Manifest.Version,
		EntryModule: EntryModule,
		Modules:     Modules,
	}
	// Mirror the sporeapp runtime: manifest-declared schema structs are bound
	// under the "app" namespace before the package is loaded, so script imports
	// like `import DemoEchoReq from "app"` resolve to the host type.
	for _, ref := range Manifest.Schemas {
		if ref.Name == "" {
			continue
		}
		switch ref.Name {
		case "DemoEchoReq":
			if err := rt.BindStruct("app", ref.Name, gen.DemoEchoReq{}); err != nil {
				t.Fatalf("BindStruct %q: %v", ref.Name, err)
			}
		}
	}
	if err := rt.LoadPackage(pkg); err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}

	// Test ping
	result, err := rt.Call("ping")
	if err != nil {
		t.Fatalf("Call ping: %v", err)
	}
	if !result.Ok() {
		t.Fatalf("ping result not ok: %+v", result)
	}

	// Test twice
	result, err = rt.Call("twice", 21)
	if err != nil {
		t.Fatalf("Call double: %v", err)
	}
	if !result.Ok() {
		t.Fatalf("double result not ok: %+v", result)
	}
}

// TestRegisterReqComplete verifies RegisterReq produces a valid package.
func TestRegisterReqComplete(t *testing.T) {
	req := RegisterReq()
	if req.Manifest.ID != Manifest.ID {
		t.Fatal("RegisterReq manifest mismatch")
	}
	if req.EntryModule != EntryModule {
		t.Fatal("RegisterReq entry module mismatch")
	}
	if len(req.Modules) == 0 {
		t.Fatal("RegisterReq has no modules")
	}
	if _, ok := req.Modules[req.EntryModule]; !ok {
		t.Fatal("entry module not in modules map")
	}
}
