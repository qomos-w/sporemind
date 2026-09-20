package project

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// knownStructForTest returns a struct name that the codegen registry knows
// about, so contract tests can assert valid struct references.
func knownStructForTest(t *testing.T) string {
	t.Helper()
	for _, name := range gen.SchemaIDs {
		if name != "" {
			return name
		}
	}
	t.Fatal("gen.SchemaIDs is empty; codegen registry not loaded")
	return ""
}

func taskCardRaw(dataBlock string) string {
	return "---\nid: c\ntype: task\ntags: []\n" + dataBlock + "\n---\n\nBody."
}

func errorCodes(errs []gen.CardValidationError) map[string]bool {
	out := map[string]bool{}
	for _, e := range errs {
		out[e.Code] = true
	}
	return out
}

func TestValidateCardContractNoContractIsValid(t *testing.T) {
	errs := validateCardStructured("c", taskCardRaw(""))
	if len(errs) != 0 {
		t.Fatalf("task without contract should be valid, got %+v", errs)
	}
}

func TestValidateCardContractScalarInputs(t *testing.T) {
	raw := taskCardRaw("data:\n  inputs:\n    repo: string\n    branch: string\n")
	if err := validateCard("c", raw); err != nil {
		t.Fatalf("scalar inputs should be valid: %v", err)
	}
}

func TestValidateCardContractStructRefBySchema(t *testing.T) {
	known := knownStructForTest(t)
	raw := taskCardRaw("data:\n  outputs:\n    report:\n      type: object\n      schema: " + known + "\n")
	if err := validateCard("c", raw); err != nil {
		t.Fatalf("struct-ref output should be valid: %v", err)
	}
}

func TestValidateCardContractBareStructName(t *testing.T) {
	known := knownStructForTest(t)
	raw := taskCardRaw("data:\n  outputs:\n    report: " + known + "\n")
	if err := validateCard("c", raw); err != nil {
		t.Fatalf("bare struct-name output should be valid: %v", err)
	}
}

func TestValidateCardContractArrayAndMap(t *testing.T) {
	known := knownStructForTest(t)
	raw := taskCardRaw("data:\n  inputs:\n    tags:\n      type: array\n      of: string\n    table:\n      type: map\n      of: " + known + "\n")
	if err := validateCard("c", raw); err != nil {
		t.Fatalf("array/map inputs should be valid: %v", err)
	}
}

func TestValidateCardContractUntypedObjectValid(t *testing.T) {
	raw := taskCardRaw("data:\n  outputs:\n    blob:\n      type: object\n")
	if err := validateCard("c", raw); err != nil {
		t.Fatalf("untyped object should be valid: %v", err)
	}
}

func TestValidateCardContractRejectsUnknownScalar(t *testing.T) {
	raw := taskCardRaw("data:\n  inputs:\n    x: notatype\n")
	errs := validateCardStructured("c", raw)
	if !errorCodes(errs)["task_contract_type_unknown"] {
		t.Fatalf("expected task_contract_type_unknown, got %+v", errs)
	}
}

func TestValidateCardContractRejectsUnknownStruct(t *testing.T) {
	raw := taskCardRaw("data:\n  outputs:\n    report: NoSuchStruct\n")
	errs := validateCardStructured("c", raw)
	if !errorCodes(errs)["task_contract_type_unknown"] {
		t.Fatalf("expected task_contract_type_unknown, got %+v", errs)
	}
}

func TestValidateCardContractRejectsUnknownSchema(t *testing.T) {
	raw := taskCardRaw("data:\n  outputs:\n    report:\n      type: object\n      schema: NoSuchStruct\n")
	errs := validateCardStructured("c", raw)
	if !errorCodes(errs)["task_contract_schema_unknown"] {
		t.Fatalf("expected task_contract_schema_unknown, got %+v", errs)
	}
}

func TestValidateCardContractRejectsArrayMissingOf(t *testing.T) {
	raw := taskCardRaw("data:\n  inputs:\n    tags:\n      type: array\n")
	errs := validateCardStructured("c", raw)
	if !errorCodes(errs)["task_contract_of_missing"] {
		t.Fatalf("expected task_contract_of_missing, got %+v", errs)
	}
}

func TestValidateCardContractRejectsMalformedSpec(t *testing.T) {
	// The frontmatter parser coerces scalar values to strings, so a bare number
	// is reported as an unknown type name ("123"), not malformed. The genuinely
	// malformed (non-string, non-map) case is exercised directly below.
	raw := taskCardRaw("data:\n  inputs:\n    x: 123\n")
	errs := validateCardStructured("c", raw)
	if !errorCodes(errs)["task_contract_type_unknown"] {
		t.Fatalf("expected task_contract_type_unknown for numeric-looking name, got %+v", errs)
	}
	if got := resolveTypeSpec(42); got != "task_contract_type_malformed" {
		t.Fatalf("resolveTypeSpec(42) = %q, want task_contract_type_malformed", got)
	}
}

func TestValidateCardContractRejectsInputsNotMap(t *testing.T) {
	raw := taskCardRaw("data:\n  inputs: not-a-map\n")
	errs := validateCardStructured("c", raw)
	if !errorCodes(errs)["task_contract_inputs_not_map"] {
		t.Fatalf("expected task_contract_inputs_not_map, got %+v", errs)
	}
}

func TestValidateCardContractRejectsEmptyFieldName(t *testing.T) {
	// The frontmatter parser does not unquote keys, so an empty field name is
	// constructed programmatically (as an external/codegen provider would).
	card := &CardRecord{
		Type: "task",
		Data: map[string]any{"inputs": map[string]any{"": "string"}},
	}
	errs := validateCardContract(card)
	if !errorCodes(errs)["task_contract_inputs_empty_name"] {
		t.Fatalf("expected task_contract_inputs_empty_name, got %+v", errs)
	}
}

func TestValidateCardContractReportsFieldPath(t *testing.T) {
	raw := taskCardRaw("data:\n  outputs:\n    report: NoSuchStruct\n")
	errs := validateCardStructured("c", raw)
	if len(errs) == 0 || errs[0].Field != "data.outputs.report" {
		t.Fatalf("expected field path data.outputs.report, got %+v", errs)
	}
}

func TestValidateCardTemplateMarker(t *testing.T) {
	raw := taskCardRaw("data:\n  template: true\n")
	card := decodeCard("c", raw)
	if !cardIsTemplate(card) {
		t.Fatal("cardIsTemplate should report true")
	}
	if err := validateCard("c", raw); err != nil {
		t.Fatalf("template marker alone should be valid: %v", err)
	}
}

func TestValidateCardInstanceOfMarker(t *testing.T) {
	raw := taskCardRaw("data:\n  instance_of: SomeTemplate\n")
	card := decodeCard("c", raw)
	if cardInstanceOf(card) != "SomeTemplate" {
		t.Fatalf("cardInstanceOf = %q, want SomeTemplate", cardInstanceOf(card))
	}
	if err := validateCard("c", raw); err != nil {
		t.Fatalf("instance_of marker alone should be valid: %v", err)
	}
}

func TestValidateCardRejectsTemplateInstanceConflict(t *testing.T) {
	raw := taskCardRaw("data:\n  template: true\n  instance_of: SomeTemplate\n")
	errs := validateCardStructured("c", raw)
	if !errorCodes(errs)["task_template_instance_conflict"] {
		t.Fatalf("expected task_template_instance_conflict, got %+v", errs)
	}
}

func TestResolveTypeSpecTable(t *testing.T) {
	known := knownStructForTest(t)
	cases := []struct {
		name string
		spec any
		want string // "" means valid
	}{
		{"scalar string", "string", ""},
		{"scalar int", "int", ""},
		{"scalar any", "any", ""},
		{"bare struct", known, ""},
		{"unknown bare", "NoSuch", "task_contract_type_unknown"},
		{"empty string", "", "task_contract_type_malformed"},
		{"map scalar", map[string]any{"type": "bool"}, ""},
		{"map object+schema", map[string]any{"type": "object", "schema": known}, ""},
		{"map object unknown schema", map[string]any{"type": "object", "schema": "Nope"}, "task_contract_schema_unknown"},
		{"map object no schema", map[string]any{"type": "object"}, ""},
		{"map array of scalar", map[string]any{"type": "array", "of": "string"}, ""},
		{"map array missing of", map[string]any{"type": "array"}, "task_contract_of_missing"},
		{"map array of struct", map[string]any{"type": "array", "of": known}, ""},
		{"map struct by name", map[string]any{"type": known}, ""},
		{"map unknown type", map[string]any{"type": "frobnicate"}, "task_contract_type_unknown"},
		{"map no type no schema", map[string]any{"desc": "x"}, "task_contract_type_malformed"},
		{"map implicit schema", map[string]any{"schema": known}, ""},
		{"number spec", 42, "task_contract_type_malformed"},
		{"bool spec", true, "task_contract_type_malformed"},
	}
	for _, c := range cases {
		if got := resolveTypeSpec(c.spec); got != c.want {
			t.Errorf("resolveTypeSpec(%v) = %q, want %q", c.spec, got, c.want)
		}
	}
}

func TestKnownStructNameResolvesCodegenRegistry(t *testing.T) {
	known := knownStructForTest(t)
	if !knownStructName(known) {
		t.Errorf("knownStructName(%q) = false, want true", known)
	}
	if knownStructName("DefinitelyNotARegisteredStruct123") {
		t.Error("knownStructName should return false for an unregistered name")
	}
}
