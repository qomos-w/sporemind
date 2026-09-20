package project

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// This file implements runtime value validation for a task card's data.outputs
// contract. card_contract.go validates the contract *as authored* (every
// declared type spec resolves to a known spore scalar or codegen struct);
// validateOutputsAgainstContract validates the *values* a worker produced
// against that contract. It is invoked by the review path
// (project.task_validate_outputs) before workspace.agent.review approves.
//
// Semantics:
//   - A card with no data.outputs (or an empty one) declares no contract and is
//     always valid — every declared field is optional to *declare*, but a
//     declared field is required to *produce*.
//   - Every declared field must be present in the worker's outputs.
//   - Each present value must match its type spec:
//     scalar  → compatible Go/JSON scalar kind
//     struct  → a JSON object (deep field validation is deferred)
//     array<T> → a JSON array whose elements each match T
//     map<T>   → a JSON object whose values each match T
//     object  → any JSON object
//     any     → anything

// outputTypeKind classifies a declared output type for value validation.
type outputTypeKind int

const (
	outputKindUnknown outputTypeKind = iota
	outputKindScalar                 // a concrete spore scalar (bool/string/int/float/...)
	outputKindStruct                 // a codegen struct name — validated structurally as a JSON object
	outputKindArray                  // array<T>
	outputKindMap                    // map<string, T>
	outputKindObject                 // untyped object (any JSON map)
	outputKindAny                    // any
)

// outputType is the parsed form of a type spec.
type outputType struct {
	kind   outputTypeKind
	scalar string      // canonical scalar name when kind == outputKindScalar
	elem   *outputType // element type when kind == outputKindArray/outputKindMap
}

// validateOutputsAgainstContract validates the worker's produced outputs against
// the task card's declared data.outputs contract. Returns nil when the card has
// no contract or all declared fields are satisfied; otherwise returns one
// CardValidationError per violation.
func validateOutputsAgainstContract(card *CardRecord, outputs map[string]any) []gen.CardValidationError {
	if card == nil {
		return nil
	}
	decl := contractDeclMap(card.Data["outputs"])
	if len(decl) == 0 {
		// Malformed/empty contract is a card-authoring concern (caught by
		// validateCardContract at save time). Treat as no contract here.
		return nil
	}
	return validateValuesAgainstContract(decl, outputs, "outputs")
}

// validateInputsAgainstContract validates the provided input values against a
// card's declared data.inputs contract. Used by template_instantiate (channel b
// of the typed-inputs system) to enforce that instantiation parameters satisfy
// the template's input contract before the instance is created. Returns nil
// when the card has no inputs contract (no constraint); otherwise returns one
// CardValidationError per violation (missing field or type mismatch).
func validateInputsAgainstContract(card *CardRecord, inputs map[string]any) []gen.CardValidationError {
	if card == nil {
		return nil
	}
	decl := contractDeclMap(card.Data["inputs"])
	if len(decl) == 0 {
		return nil // no contract declared
	}
	return validateValuesAgainstContract(decl, inputs, "inputs")
}

// contractDeclMap extracts a contract declaration (data.inputs or data.outputs)
// from the parsed card.Data value. It accepts either a nested map (from YAML
// inline frontmatter) or a JSON-encoded string (the same encoding used for
// task_outputs), so contract declarations written as either shape can be
// validated.
func contractDeclMap(raw any) map[string]any {
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	s, ok := raw.(string)
	if !ok {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return nil
	}
	return parsed
}

// validateValuesAgainstContract is the shared core of inputs and outputs value
// validation. Every declared field must be present in values, and each present
// value must match its type spec. label is "inputs" or "outputs" for diagnostic
// messages and error codes.
func validateValuesAgainstContract(decl map[string]any, values map[string]any, label string) []gen.CardValidationError {
	var errs []gen.CardValidationError
	for name, spec := range decl {
		field := label + "." + name
		val, present := values[name]
		if !present {
			errs = append(errs, gen.CardValidationError{
				Code:    label + "_missing_field",
				Field:   field,
				Message: fmt.Sprintf("%s: required field %q is missing", label, name),
			})
			continue
		}
		t, ok := parseOutputType(spec)
		if !ok {
			// Unparseable declaration — surfaced so it is not silently
			// accepted against a broken contract.
			errs = append(errs, gen.CardValidationError{
				Code:    label + "_bad_contract",
				Field:   field,
				Message: fmt.Sprintf("%s: field %q has an unparseable type declaration", label, name),
			})
			continue
		}
		if code := validateContractValue(t, val, label); code != "" {
			errs = append(errs, gen.CardValidationError{
				Code:    code,
				Field:   field,
				Message: outputMismatchMessage(name, t, val),
			})
		}
	}
	return errs
}

// parseOutputType parses a type spec (same shapes as resolveTypeSpec) into an
// outputType descriptor for value validation. Returns ok=false when the spec
// does not resolve to a known type.
func parseOutputType(spec any) (outputType, bool) {
	switch s := spec.(type) {
	case string:
		return parseOutputTypeName(s)
	case map[string]any:
		return parseOutputTypeMap(s)
	default:
		return outputType{kind: outputKindUnknown}, false
	}
}

func parseOutputTypeName(name string) (outputType, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return outputType{kind: outputKindUnknown}, false
	}
	lower := strings.ToLower(name)
	switch lower {
	case "any":
		return outputType{kind: outputKindAny}, true
	case "object":
		return outputType{kind: outputKindObject}, true
	}
	if sporeScalarExists(lower) {
		return outputType{kind: outputKindScalar, scalar: lower}, true
	}
	if knownStructName(name) {
		return outputType{kind: outputKindStruct}, true
	}
	return outputType{kind: outputKindUnknown}, false
}

func parseOutputTypeMap(m map[string]any) (outputType, bool) {
	typeName := strings.ToLower(strings.TrimSpace(stringValue(m["type"])))
	if typeName == "" {
		// { schema: "X" } with no explicit type → struct.
		if schema := strings.TrimSpace(stringValue(m["schema"])); schema != "" {
			if knownStructName(schema) {
				return outputType{kind: outputKindStruct}, true
			}
			return outputType{kind: outputKindUnknown}, false
		}
		return outputType{kind: outputKindUnknown}, false
	}
	switch {
	case typeName == "any":
		return outputType{kind: outputKindAny}, true
	case typeName == "object":
		schema := strings.TrimSpace(stringValue(m["schema"]))
		if schema == "" {
			return outputType{kind: outputKindObject}, true
		}
		if knownStructName(schema) {
			return outputType{kind: outputKindStruct}, true
		}
		return outputType{kind: outputKindUnknown}, false
	case typeName == "array", typeName == "map":
		ofSpec, present := m["of"]
		if !present {
			return outputType{kind: outputKindUnknown}, false
		}
		elem, ok := parseOutputType(ofSpec)
		if !ok {
			return outputType{kind: outputKindUnknown}, false
		}
		kind := outputKindArray
		if typeName == "map" {
			kind = outputKindMap
		}
		return outputType{kind: kind, elem: &elem}, true
	case sporeScalarExists(typeName):
		return outputType{kind: outputKindScalar, scalar: typeName}, true
	case knownStructName(stringValue(m["type"])):
		return outputType{kind: outputKindStruct}, true
	}
	return outputType{kind: outputKindUnknown}, false
}

// validateContractValue checks a single value against a parsed type, recursing
// into array/map elements. Returns "" when the value matches, or a non-empty
// diagnostic code otherwise. label is "inputs" or "outputs" for the type-
// mismatch code.
func validateContractValue(t outputType, val any, label string) string {
	switch t.kind {
	case outputKindAny:
		return ""
	case outputKindScalar:
		if !scalarValueMatches(t.scalar, val) {
			return label + "_type_mismatch"
		}
		return ""
	case outputKindObject, outputKindStruct:
		if !isJSONMap(val) {
			return label + "_type_mismatch"
		}
		return ""
	case outputKindArray:
		arr, ok := val.([]any)
		if !ok {
			return label + "_type_mismatch"
		}
		for _, e := range arr {
			if code := validateContractValue(*t.elem, e, label); code != "" {
				return code
			}
		}
		return ""
	case outputKindMap:
		m, ok := val.(map[string]any)
		if !ok {
			return label + "_type_mismatch"
		}
		for _, v := range m {
			if code := validateContractValue(*t.elem, v, label); code != "" {
				return code
			}
		}
		return ""
	}
	return label + "_type_mismatch"
}

// scalarValueMatches reports whether a JSON/Go value is compatible with a spore
// scalar kind. JSON numbers decode to float64; YAML/Go integers are also
// accepted defensively.
func scalarValueMatches(scalar string, val any) bool {
	switch scalar {
	case "bool":
		_, ok := val.(bool)
		return ok
	case "string", "bytes":
		_, ok := val.(string)
		return ok
	case "int", "short", "ushort", "uint", "long", "ulong", "byte":
		return isJSONInt(val)
	case "float", "double":
		return isJSONNumber(val)
	}
	return false
}

func isJSONInt(val any) bool {
	switch n := val.(type) {
	case float64:
		return n == float64(int64(n))
	case float32:
		return n == float32(int32(n))
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case json.Number:
		_, err := n.Int64()
		return err == nil
	}
	return false
}

func isJSONNumber(val any) bool {
	switch val.(type) {
	case float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case json.Number:
		return true
	}
	return false
}

func isJSONMap(val any) bool {
	_, ok := val.(map[string]any)
	return ok
}

func outputMismatchMessage(name string, t outputType, val any) string {
	return fmt.Sprintf("outputs.%s: expected %s, got %s", name, describeOutputType(t), jsonValueKind(val))
}

func describeOutputType(t outputType) string {
	switch t.kind {
	case outputKindScalar:
		return t.scalar
	case outputKindStruct:
		return "object"
	case outputKindArray:
		return "array"
	case outputKindMap:
		return "map"
	case outputKindObject:
		return "object"
	case outputKindAny:
		return "any"
	}
	return "unknown"
}

func jsonValueKind(val any) string {
	switch val.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case string:
		return "string"
	case float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return "number"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	}
	return fmt.Sprintf("%T", val)
}
