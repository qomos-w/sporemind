package codegen

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appdef"
)

const lowercaseFieldsAppdef = `app Lower {
    struct ProvisionRequest {
        account: string
        secret_base32: string
        digits: int
    }

    callable provision {
        request: ProvisionRequest
        response: ProvisionRequest
    }
}
`

// TestGenerateSchemasGoExportsLowercaseFields pins the fix for the live
// breakpoint: a lowercase-declared field used to emit an unexported Go field
// that encoding/json silently ignores in both directions, so every handler
// saw zero values regardless of the payload. The Go name must be exported;
// the json tag keeps the declared wire name.
func TestGenerateSchemasGoExportsLowercaseFields(t *testing.T) {
	app, diags, err := appdef.ParseFile(lowercaseFieldsAppdef)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("diags: %v", diags)
	}
	out, err := generateSchemasGo(app)
	if err != nil {
		t.Fatalf("generateSchemasGo: %v", err)
	}
	for _, want := range []string{
		"Account string `json:\"account\"`",
		"Secret_base32 string `json:\"secret_base32\"`",
		"Digits int32 `json:\"digits\"`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\taccount ") {
		t.Errorf("unexported Go field still emitted:\n%s", out)
	}
}

// TestGenerateSchemasGoExportNameCollision verifies that two fields whose
// names collide after exportization fail generation loudly instead of
// emitting a duplicate Go field (compile error) or silently merging.
func TestGenerateSchemasGoExportNameCollision(t *testing.T) {
	src := `app Collide {
    struct Req {
        id: string
        Id: string
    }

    callable f {
        request: Req
        response: Req
    }
}
`
	app, _, err := appdef.ParseFile(src)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	_, err = generateSchemasGo(app)
	if err == nil {
		t.Fatal("expected collision error for id/Id, got nil")
	}
	if !strings.Contains(err.Error(), "Go field") {
		t.Fatalf("unexpected error: %v", err)
	}
}
