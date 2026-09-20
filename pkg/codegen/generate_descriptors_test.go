package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appdef"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

const descriptorTestAppDef = `app DescTest {
    id: app.desctest
    name: Descriptor Test
    namespace: desctest
    version: 1.0.0

    struct ProvisionRequest {
        name: string
        optional secret: string
        optional digits: int
        optional period: int
    }

    struct ProvisionResponse {
        account_id: string
        created_unix: int
    }

    struct CodesRequest {
        optional account_id: string
    }

    callable provision {
        request: ProvisionRequest
        response: ProvisionResponse
        effect: mutate
    }

    callable codes {
        request: CodesRequest
        effect: read
    }

    bundle tools {
        title: Tools
        tools: [provision, codes]
    }
}
`

func parseDescriptorTestApp(t *testing.T) *appdef.AppDef {
	t.Helper()
	app, diags, err := appdef.ParseFile(descriptorTestAppDef)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("diags: %v", diags)
	}
	return app
}

// The generated manifest schema refs and the descriptor artifact must
// round-trip through protocol.ValidateAppSchemaDescriptors — this is the
// gate handleRegister enforces at registration time.
func TestGeneratedSchemasPassProtocolValidation(t *testing.T) {
	app := parseDescriptorTestApp(t)

	manifest, err := GenerateManifestFromAppDef(app)
	if err != nil {
		t.Fatalf("GenerateManifestFromAppDef: %v", err)
	}
	if len(manifest.Schemas) != len(app.Structs) {
		t.Fatalf("manifest.Schemas = %d refs, want %d", len(manifest.Schemas), len(app.Structs))
	}
	for _, ref := range manifest.Schemas {
		if ref.SchemaID <= 0 {
			t.Errorf("schema %q has non-positive SchemaID %d", ref.Name, ref.SchemaID)
		}
		if len(ref.Hash) != 64 {
			t.Errorf("schema %q hash %q is not sha256 hex", ref.Name, ref.Hash)
		}
	}

	descriptors := GenerateDescriptorsFromAppDef(app)
	if descriptors == nil {
		t.Fatal("descriptors = nil, want descriptor set for struct-typed appdef")
	}
	// The artifact round-trips through JSON (that is how the package path
	// reads it back).
	encoded, err := json.Marshal(descriptors)
	if err != nil {
		t.Fatalf("marshal descriptors: %v", err)
	}
	var decoded map[string]gen.AppObjectDescriptor
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal descriptors: %v", err)
	}

	if _, err := protocol.ValidateAppSchemaDescriptors(manifest, decoded); err != nil {
		t.Fatalf("ValidateAppSchemaDescriptors: %v", err)
	}
}

// Schema IDs must be deterministic and fit the per-namespace local id space
// (21-bit mask enforced by the spore registry); collisions inside one app are
// resolved by declaration-order probing.
func TestAppSchemaIDDeterministic(t *testing.T) {
	app := parseDescriptorTestApp(t)
	first := appSchemaIDs(app)
	second := appSchemaIDs(app)
	if len(first) != len(app.Structs) {
		t.Fatalf("ids = %v, want one per struct (%d)", first, len(app.Structs))
	}
	for name, id := range first {
		if id != second[name] {
			t.Fatalf("appSchemaIDs not deterministic for %q: %d vs %d", name, id, second[name])
		}
		if id <= 0 || id > 0x1fffff {
			t.Errorf("schema %q id %d outside local id space (0, 0x1fffff]", name, id)
		}
	}
}

// Alias-typed callable schemas fall back to the legacy no-refs manifest and
// no descriptor artifact, so validation never sees unresolvable references.
func TestAliasTypedAppDefEmitsNoSchemas(t *testing.T) {
	src := `app AliasDesc {
    id: app.aliasdesc
    type Label = string

    struct Item {
        label: Label
    }

    callable emit {
        request: Label
        response: Item
    }
}
`
	app, diags, err := appdef.ParseFile(src)
	if err != nil || len(diags) > 0 {
		t.Fatalf("ParseFile: err=%v diags=%v", err, diags)
	}
	manifest, err := GenerateManifestFromAppDef(app)
	if err != nil {
		t.Fatalf("GenerateManifestFromAppDef: %v", err)
	}
	if len(manifest.Schemas) != 0 {
		t.Fatalf("manifest.Schemas = %v, want none for alias-typed request", manifest.Schemas)
	}
	if descriptors := GenerateDescriptorsFromAppDef(app); descriptors != nil {
		t.Fatalf("descriptors = %v, want nil for alias-typed request", descriptors)
	}
}

// The generator writes app.descriptors.json next to the other artifacts.
func TestGenerateWritesDescriptorsArtifact(t *testing.T) {
	if testing.Short() {
		t.Skip("generate test writes files")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileAppDef), []byte(descriptorTestAppDef), 0o644); err != nil {
		t.Fatalf("write appdef: %v", err)
	}
	result, err := Generate(dir, testGenOpts(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	found := false
	for _, f := range result.Files {
		if f == FileDescriptorsJSON {
			found = true
		}
	}
	if !found {
		t.Fatalf("generated files %v missing %s", result.Files, FileDescriptorsJSON)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileDescriptorsJSON))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var decoded map[string]gen.AppObjectDescriptor
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	if _, ok := decoded["ProvisionRequest"]; !ok {
		t.Fatalf("artifact missing ProvisionRequest: keys=%v", mapKeys(decoded))
	}
}

func mapKeys(m map[string]gen.AppObjectDescriptor) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
