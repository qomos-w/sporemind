package project

import (
	"path/filepath"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// contractCard builds a CardRecord whose data.outputs contract is the given
// spec map (mirroring how the frontmatter parser materializes data.outputs).
func contractCard(outputs map[string]any) *CardRecord {
	return &CardRecord{
		Type: "task",
		Data: map[string]any{"outputs": outputs},
	}
}

func errCodes2(errs []gen.CardValidationError) map[string]bool {
	out := map[string]bool{}
	for _, e := range errs {
		out[e.Code] = true
	}
	return out
}

func TestValidateOutputsNoContractIsValid(t *testing.T) {
	card := &CardRecord{Type: "task", Data: map[string]any{}}
	if errs := validateOutputsAgainstContract(card, map[string]any{"x": 1}); len(errs) != 0 {
		t.Fatalf("no contract should be valid, got %+v", errs)
	}
}

func TestValidateOutputsEmptyContractIsValid(t *testing.T) {
	card := &CardRecord{Type: "task", Data: map[string]any{"outputs": map[string]any{}}}
	if errs := validateOutputsAgainstContract(card, map[string]any{}); len(errs) != 0 {
		t.Fatalf("empty contract should be valid, got %+v", errs)
	}
}

func TestValidateOutputsNilCardIsValid(t *testing.T) {
	if errs := validateOutputsAgainstContract(nil, map[string]any{"x": 1}); len(errs) != 0 {
		t.Fatalf("nil card should be valid, got %+v", errs)
	}
}

func TestValidateOutputsScalarsPresentAndCorrect(t *testing.T) {
	card := contractCard(map[string]any{
		"name":  "string",
		"count": "int",
		"ok":    "bool",
	})
	out := map[string]any{
		"name":  "widget",
		"count": float64(3),
		"ok":    true,
	}
	if errs := validateOutputsAgainstContract(card, out); len(errs) != 0 {
		t.Fatalf("valid scalar outputs should pass, got %+v", errs)
	}
}

func TestValidateOutputsMissingField(t *testing.T) {
	card := contractCard(map[string]any{"report": "string", "score": "int"})
	out := map[string]any{"report": "x"}
	errs := validateOutputsAgainstContract(card, out)
	if !errCodes2(errs)["outputs_missing_field"] {
		t.Fatalf("expected outputs_missing_field for score, got %+v", errs)
	}
	// Field path should point at the missing field.
	found := false
	for _, e := range errs {
		if e.Field == "outputs.score" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected field outputs.score, got %+v", errs)
	}
}

func TestValidateOutputsScalarTypeMismatch(t *testing.T) {
	card := contractCard(map[string]any{"count": "int"})
	out := map[string]any{"count": "not-a-number"}
	errs := validateOutputsAgainstContract(card, out)
	if !errCodes2(errs)["outputs_type_mismatch"] {
		t.Fatalf("expected outputs_type_mismatch, got %+v", errs)
	}
}

func TestValidateOutputsBoolMismatch(t *testing.T) {
	card := contractCard(map[string]any{"ok": "bool"})
	out := map[string]any{"ok": float64(1)}
	errs := validateOutputsAgainstContract(card, out)
	if !errCodes2(errs)["outputs_type_mismatch"] {
		t.Fatalf("expected outputs_type_mismatch for bool, got %+v", errs)
	}
}

func TestValidateOutputsStructAsObject(t *testing.T) {
	known := knownStructForTest(t)
	card := contractCard(map[string]any{"report": known})
	// A struct-typed output is satisfied by any JSON object (map).
	if errs := validateOutputsAgainstContract(card, map[string]any{"report": map[string]any{"a": float64(1)}}); len(errs) != 0 {
		t.Fatalf("struct output as object should pass, got %+v", errs)
	}
	// A non-object value fails.
	errs := validateOutputsAgainstContract(card, map[string]any{"report": "string"})
	if !errCodes2(errs)["outputs_type_mismatch"] {
		t.Fatalf("expected outputs_type_mismatch for struct fed a string, got %+v", errs)
	}
}

func TestValidateOutputsStructBySchemaMap(t *testing.T) {
	known := knownStructForTest(t)
	card := contractCard(map[string]any{"report": map[string]any{"type": "object", "schema": known}})
	if errs := validateOutputsAgainstContract(card, map[string]any{"report": map[string]any{}}); len(errs) != 0 {
		t.Fatalf("object+schema output as object should pass, got %+v", errs)
	}
}

func TestValidateOutputsArray(t *testing.T) {
	card := contractCard(map[string]any{"tags": map[string]any{"type": "array", "of": "string"}})
	// Valid array of strings.
	if errs := validateOutputsAgainstContract(card, map[string]any{"tags": []any{"a", "b"}}); len(errs) != 0 {
		t.Fatalf("valid array should pass, got %+v", errs)
	}
	// Non-array fails.
	errs := validateOutputsAgainstContract(card, map[string]any{"tags": "a"})
	if !errCodes2(errs)["outputs_type_mismatch"] {
		t.Fatalf("expected mismatch for non-array, got %+v", errs)
	}
	// Bad element type fails.
	errs = validateOutputsAgainstContract(card, map[string]any{"tags": []any{"a", float64(2)}})
	if !errCodes2(errs)["outputs_type_mismatch"] {
		t.Fatalf("expected mismatch for bad array element, got %+v", errs)
	}
}

func TestValidateOutputsMap(t *testing.T) {
	card := contractCard(map[string]any{"table": map[string]any{"type": "map", "of": "int"}})
	// Valid map of ints.
	if errs := validateOutputsAgainstContract(card, map[string]any{"table": map[string]any{"x": float64(1), "y": float64(2)}}); len(errs) != 0 {
		t.Fatalf("valid map should pass, got %+v", errs)
	}
	// Bad value type fails.
	errs := validateOutputsAgainstContract(card, map[string]any{"table": map[string]any{"x": "no"}})
	if !errCodes2(errs)["outputs_type_mismatch"] {
		t.Fatalf("expected mismatch for bad map value, got %+v", errs)
	}
}

func TestValidateOutputsUntypedObjectAndAny(t *testing.T) {
	card := contractCard(map[string]any{
		"blob": map[string]any{"type": "object"},
		"raw":  "any",
	})
	out := map[string]any{
		"blob": map[string]any{"anything": float64(1)},
		"raw":  []any{float64(1), "two"},
	}
	if errs := validateOutputsAgainstContract(card, out); len(errs) != 0 {
		t.Fatalf("untyped object + any should pass, got %+v", errs)
	}
}

func TestValidateOutputsIntAcceptsIntegerFloat(t *testing.T) {
	card := contractCard(map[string]any{"n": "int"})
	// JSON decodes numbers to float64; an integer-valued float64 is a valid int.
	if errs := validateOutputsAgainstContract(card, map[string]any{"n": float64(42)}); len(errs) != 0 {
		t.Fatalf("integer-valued float64 should satisfy int, got %+v", errs)
	}
	// A fractional float64 is not a valid int.
	errs := validateOutputsAgainstContract(card, map[string]any{"n": float64(3.14)})
	if !errCodes2(errs)["outputs_type_mismatch"] {
		t.Fatalf("expected mismatch for fractional int, got %+v", errs)
	}
}

// TestHandleTaskValidateOutputsCallable exercises the project callable end to
// end: it reads the card store and runs the runtime validator.
func TestHandleTaskValidateOutputsCallable(t *testing.T) {
	a := &Actor{
		store:        newFSCardStore(filepath.Join(t.TempDir(), wikiDir)),
		persistStore: persist.NewFSPersist(t.TempDir()),
	}
	cardRaw := "---\nid: t\ntype: task\ntags: []\ndata:\n  outputs:\n    score: int\n---\n\nBody.\n"
	if err := a.store.Save(decodeCard("t", cardRaw)); err != nil {
		t.Fatalf("save card: %v", err)
	}

	// Valid: score present and integer-typed.
	resp, err := a.handleTaskValidateOutputs(nil, gen.ProjectTaskValidateOutputsReq{
		CardID:  "t",
		Outputs: map[string]any{"score": float64(9)},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected valid, got %+v", resp.Errors)
	}

	// Invalid: missing score.
	resp, err = a.handleTaskValidateOutputs(nil, gen.ProjectTaskValidateOutputsReq{
		CardID:  "t",
		Outputs: map[string]any{},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected invalid for missing field")
	}
	if !errCodes2(resp.Errors)["outputs_missing_field"] {
		t.Fatalf("expected outputs_missing_field, got %+v", resp.Errors)
	}

	// Card with no contract is always valid.
	if err := a.store.Save(decodeCard("plain", "---\nid: plain\ntype: task\ntags: []\n---\n\nBody.\n")); err != nil {
		t.Fatalf("save plain card: %v", err)
	}
	resp, err = a.handleTaskValidateOutputs(nil, gen.ProjectTaskValidateOutputsReq{
		CardID:  "plain",
		Outputs: map[string]any{},
	})
	if err != nil || !resp.Valid {
		t.Fatalf("no-contract card should be valid, resp=%+v err=%v", resp, err)
	}

	// Missing card surfaces an error.
	if _, err := a.handleTaskValidateOutputs(nil, gen.ProjectTaskValidateOutputsReq{CardID: "nope"}); err == nil {
		t.Fatal("expected error for missing card")
	}
}
