package project

import (
	"fmt"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// This file implements the task-card I/O contract vocabulary and its
// recognition by validateCard. The contract is declared in a task card's
// frontmatter data block:
//
//	data:
//	  inputs:  { repo: string, branch: string }
//	  outputs: { report: { type: object, schema: AuditReport }, score: int }
//
// The type vocabulary reuses the spore schema type system (canonical scalar
// names + codegen-registered struct names) and the schema's array/map
// constructors. It deliberately does NOT introduce a parallel JSON Schema: a
// declared type is valid iff its scalar is a known spore scalar or its struct
// name resolves against the codegen registry (gen.SchemaIDs). Runtime value
// validation against these declarations is the responsibility of the review
// path; validateCard only validates the declaration as authored.

// sporeScalarTypes is the canonical set of spore foundational scalar type
// names (mirrors schema.TypeBool..TypeAny in spore/schema/typeid.go). These
// are the only scalar tokens accepted in a type spec.
func knownStructName(name string) bool {
	structNameIndexOnce.Do(func() {
		structNameIndex = make(map[string]struct{}, len(gen.SchemaIDs))
		for _, name := range gen.SchemaIDs {
			structNameIndex[name] = struct{}{}
		}
	})
	_, ok := structNameIndex[name]
	return ok
}

// cardContractSection enumerates the two halves of the I/O contract.
type cardContractSection string

const (
	contractInputs  cardContractSection = "inputs"
	contractOutputs cardContractSection = "outputs"
)

// validateCardContract validates the inputs/outputs declarations on any card
// that carries them. It is wired into the task validator; it is exported as a
// standalone function so other type validators (e.g. workflow maps) can opt in
// when they grow an I/O contract.
//
// A contract section is optional: a task card with no inputs/outputs is valid.
// When present, every declared field must resolve to a known spore scalar or
// codegen-registered struct.
func validateCardContract(card *CardRecord) []gen.CardValidationError {
	if card == nil {
		return nil
	}
	var errs []gen.CardValidationError
	errs = append(errs, validateContractSection(card, contractInputs)...)
	errs = append(errs, validateContractSection(card, contractOutputs)...)
	return errs
}

func validateContractSection(card *CardRecord, section cardContractSection) []gen.CardValidationError {
	raw, present := card.Data[string(section)]
	if !present {
		return nil
	}
	sectionMap, ok := raw.(map[string]any)
	if !ok {
		return []gen.CardValidationError{{
			Code:    "task_contract_" + string(section) + "_not_map",
			Field:   "data." + string(section),
			Message: fmt.Sprintf("%s contract must be a map of name → type spec", section),
		}}
	}
	var errs []gen.CardValidationError
	for name, spec := range sectionMap {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, gen.CardValidationError{
				Code:    "task_contract_" + string(section) + "_empty_name",
				Field:   "data." + string(section),
				Message: fmt.Sprintf("%s: field name must not be empty", section),
			})
			continue
		}
		if derr := resolveTypeSpec(spec); derr != "" {
			errs = append(errs, gen.CardValidationError{
				Code:    derr,
				Field:   "data." + string(section) + "." + name,
				Message: contractErrorMessage(section, name, spec, derr),
			})
		}
	}
	return errs
}

// contractErrorMessage produces a human-readable message for a type-spec error
// at a specific contract field.
func contractErrorMessage(section cardContractSection, name string, spec any, code string) string {
	specRepr := fmt.Sprintf("%v", spec)
	switch code {
	case "task_contract_type_malformed":
		return fmt.Sprintf("%s.%s: type spec is malformed (%v); use a scalar/struct name or {type, schema|of}", section, name, specRepr)
	case "task_contract_type_unknown":
		return fmt.Sprintf("%s.%s: type %q is not a known spore scalar or codegen struct", section, name, specRepr)
	case "task_contract_schema_unknown":
		return fmt.Sprintf("%s.%s: schema struct %q is not registered by codegen", section, name, specRepr)
	case "task_contract_of_missing":
		return fmt.Sprintf("%s.%s: array/map type requires an \"of\" element type", section, name)
	}
	return fmt.Sprintf("%s.%s: invalid type spec (%v)", section, name, specRepr)
}

// resolveTypeSpec validates a single type-spec value and returns "" when it is
// well-formed and resolvable, or a non-empty diagnostic code otherwise.
//
// Accepted shapes (all reuse spore types — scalars, codegen structs, array/map):
//
//	"string"                     → scalar
//	"AuditReport"                → codegen struct (by name)
//	{ type: "int" }              → scalar
//	{ type: "object", schema: "AuditReport" }  → struct
//	{ type: "array", of: "string" }            → array<scalar>
//	{ type: "map",   of: "AuditReport" }       → map<string, struct>
func resolveTypeSpec(spec any) string {
	switch s := spec.(type) {
	case string:
		return resolveTypeName(strings.TrimSpace(s))
	case map[string]any:
		return resolveTypeMap(s)
	default:
		return "task_contract_type_malformed"
	}
}

// resolveTypeName resolves a bare scalar or struct name.
func resolveTypeName(name string) string {
	if name == "" {
		return "task_contract_type_malformed"
	}
	if _, ok := sporeScalarTypes[strings.ToLower(name)]; ok {
		return ""
	}
	if knownStructName(name) {
		return ""
	}
	return "task_contract_type_unknown"
}

// resolveTypeMap resolves the richer { type, schema|of } form.
func resolveTypeMap(m map[string]any) string {
	typeName, _ := m["type"].(string)
	typeName = strings.TrimSpace(typeName)
	if typeName == "" {
		// Allow a struct reference written as { schema: "AuditReport" } with no
		// explicit type (implies object).
		if schema, ok := m["schema"].(string); ok && strings.TrimSpace(schema) != "" {
			return resolveSchemaRef(schema)
		}
		return "task_contract_type_malformed"
	}
	lower := strings.ToLower(typeName)
	switch {
	case lower == "object":
		schema, _ := m["schema"].(string)
		schema = strings.TrimSpace(schema)
		if schema == "" {
			return "" // untyped object: valid (any map)
		}
		return resolveSchemaRef(schema)
	case lower == "array", lower == "map":
		ofSpec, present := m["of"]
		if !present {
			return "task_contract_of_missing"
		}
		return resolveTypeSpec(ofSpec)
	case sporeScalarExists(lower):
		// A scalar with an optional schema/of is still a scalar; extra keys are
		// ignored for forward compatibility.
		return ""
	case knownStructName(typeName):
		// { type: "AuditReport" } — struct by name.
		return ""
	default:
		return "task_contract_type_unknown"
	}
}

func resolveSchemaRef(schema string) string {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return "task_contract_type_malformed"
	}
	if !knownStructName(schema) {
		return "task_contract_schema_unknown"
	}
	return ""
}

func sporeScalarExists(lower string) bool {
	_, ok := sporeScalarTypes[lower]
	return ok
}

// --- template / instance semantic markers ------------------------------------
//
// A task card may declare a lifecycle role via the data block:
//
//	data:
//	  template: true            # definitional card carrying an I/O contract; not
//	                            # directly executable, only instantiable
//	  instance_of: SomeTemplate # one-shot instance snapshot of the named template
//
// The two markers are mutually exclusive. They are orthogonal to the I/O
// contract: a plain task card may declare outputs (reviewed by the review
// path) without being a template. validateCard recognizes the markers and
// enforces their consistency.

func validateCardTemplateRole(card *CardRecord) []gen.CardValidationError {
	if card == nil {
		return nil
	}
	isTemplate := cardDataBool(card, "template")
	instanceOf := strings.TrimSpace(cardDataString(card, "instance_of"))
	if isTemplate && instanceOf != "" {
		return []gen.CardValidationError{{
			Code:    "task_template_instance_conflict",
			Field:   "data.template",
			Message: "a card cannot be both a template (data.template) and an instance (data.instance_of)",
		}}
	}
	if instanceOf != "" {
		// Provenance is recorded but not enforced against existence here: a
		// template may be renamed or not yet saved at validation time.
		return nil
	}
	return nil
}

// cardIsTemplate reports whether the card carries the data.template marker.
func cardIsTemplate(card *CardRecord) bool {
	return cardDataBool(card, "template")
}

// cardInstanceOf returns the template provenance id of an instance card, or "".
func cardInstanceOf(card *CardRecord) string {
	return strings.TrimSpace(cardDataString(card, "instance_of"))
}
