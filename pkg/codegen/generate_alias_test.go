package codegen

import (
	"strings"
	"testing"

	schema "github.com/qomos-w/spore/schema"
	"github.com/qomos-w/sporemind/pkg/appdef"
)

func TestGenerateSchemasGoAliasFieldResolution(t *testing.T) {
	src := `app AliasTest {
    type Greeting = string
    type Count = int
    type Names = array<Greeting>

    struct Message {
        text: Greeting
        count: Count
        plain: string
        names: Names
    }

    callable echo {
        request: Message
        response: Message
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

	out, err0 := generateSchemasGo(app)
	if err0 != nil {
		t.Fatal(err0)
	}

	// Scalar aliases resolve to target Go types.
	if !strings.Contains(out, "Text string") {
		t.Errorf("text field should resolve to string, got:\n%s", out)
	}
	if !strings.Contains(out, "Count int32") {
		t.Errorf("count field should resolve to int (int32), got:\n%s", out)
	}
	// Composite alias (array of alias) resolves too.
	if !strings.Contains(out, "Names []string") {
		t.Errorf("names field should resolve to []string, got:\n%s", out)
	}
	// Alias definitions still emitted.
	if !strings.Contains(out, "type Greeting = string") {
		t.Errorf("missing Greeting alias definition, got:\n%s", out)
	}
}

func TestGenerateSchemasGoAliasStructRef(t *testing.T) {
	src := `app AliasStruct {
    type ItemRef = Item

    struct Item {
        id: string
    }

    struct Holder {
        ref: Item
        aliasRef: ItemRef
    }

    callable get {
        request: Holder
        response: Holder
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

	out, err0 := generateSchemasGo(app)
	if err0 != nil {
		t.Fatal(err0)
	}

	// Direct struct reference unaffected.
	if !strings.Contains(out, "Ref Item") {
		t.Errorf("ref field should be Item struct, got:\n%s", out)
	}
	// Alias-to-struct resolves to the struct name.
	if !strings.Contains(out, "AliasRef Item") {
		t.Errorf("aliasRef field should resolve to Item, got:\n%s", out)
	}
}

// TestGenerateSchemasGoUnknownScalarFailsHard is the codegen belt: even if a
// programmatic caller bypasses appdef.Validate, generateSchemasGo must
// reject an unknown scalar instead of silently degrading it to interface{}.
func TestGenerateSchemasGoUnknownScalarFailsHard(t *testing.T) {
	app := &appdef.AppDef{
		Structs: []schema.ObjectDesc{{
			Name: "Req",
			Fields: []schema.FieldDesc{{
				Name: "score",
				Type: schema.TypeDesc{Kind: schema.TypeKindScalar, Name: "float64"},
			}},
		}},
	}
	_, err := generateSchemasGo(app)
	if err == nil || !strings.Contains(err.Error(), "unknown scalar") {
		t.Fatalf("expected unknown-scalar error, got: %v", err)
	}
}
