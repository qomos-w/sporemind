package protocol

// RequestLayout is the field-level request schema of a callable, resolved from
// the authoritative protocol source (host: generated registry structs via
// reflection; app: manifest schema descriptors). One layout feeds both
// projections:
//
//	JSONSchema  — provider-facing tool input schema (deep)
//	Validate    — host-side JSON payload check against the same shape
//
// Keeping generation and validation on a single resolved shape makes contract
// drift structurally impossible: what the provider is told the model must emit
// is exactly what the host enforces before the callable runs.
//
// Enums: the JSON boundary represents an enum-typed field as a string whose
// allowed values are the enum's member names. The member set is resolved from
// the descriptor table (app face; see enumAllowedValues). The host reflection
// face cannot supply it — DescribeGoStruct has no enum category (named Go
// types map to scalars) and the .spore DSL has no enum keyword, so host
// registry structs never carry TypeKindEnum fields. When no enum descriptor
// resolves, both projections degrade to a plain string (lenient: absence of
// descriptor information never blocks).

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	spore "github.com/qomos-w/spore/schema"
	appgen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ErrNoRequestLayout marks callables without a resolvable request schema
// (no ReqSchemaID, unknown schema id, or a struct the reflection walk cannot
// describe). Callers degrade to a shallow/empty schema for these.
var ErrNoRequestLayout = errors.New("protocol: no resolvable request layout")

// RequestLayout is a resolved field-level request shape plus the expanded
// nested structs it references.
type RequestLayout struct {
	root   spore.ObjectDesc
	nested map[string]spore.ObjectDesc
}

// ResolveRequestLayout resolves a host callable's request layout from its
// enriched interface metadata. Deep struct expansion uses the generated
// registry (gen.SchemaIDs/gen.SchemaTypes) — the same structs the receiving
// handler decodes into.
func ResolveRequestLayout(ci appgen.CallableInterface) (*RequestLayout, error) {
	if ci.ReqSchemaID <= 0 {
		return nil, fmt.Errorf("%w: %s has no ReqSchemaID", ErrNoRequestLayout, ci.Name)
	}
	typ, ok := appgen.SchemaTypes[uint64(ci.ReqSchemaID)]
	if !ok {
		return nil, fmt.Errorf("%w: schema id %d of %s not in registry", ErrNoRequestLayout, ci.ReqSchemaID, ci.Name)
	}
	root, err := spore.DescribeGoStruct(reflect.New(typ).Interface())
	if err != nil {
		return nil, fmt.Errorf("%w: describe %s: %v", ErrNoRequestLayout, ci.Name, err)
	}
	mergeParamDescriptions(&root, ci.Params)
	l := &RequestLayout{root: root, nested: map[string]spore.ObjectDesc{}}
	l.expandFrom(func(name string) (spore.ObjectDesc, bool) {
		typ, ok := registryTypeByName(name)
		if !ok {
			return spore.ObjectDesc{}, false
		}
		desc, err := spore.DescribeGoStruct(reflect.New(typ).Interface())
		if err != nil {
			return spore.ObjectDesc{}, false
		}
		return desc, true
	})
	return l, nil
}

// LayoutFromAppObjects resolves a layout from an app manifest's object
// descriptor table (manifest.SchemaDescriptors keyed by object name). The table
// carries structs and enums: an enum entry is an object of Kind "enum" whose
// fields name the members (member field types are meaningless on the JSON
// boundary and are ignored).
func LayoutFromAppObjects(rootName string, objects map[string]appgen.AppObjectDescriptor) (*RequestLayout, error) {
	if rootName == "" {
		return nil, fmt.Errorf("%w: empty object name", ErrNoRequestLayout)
	}
	converted := make(map[string]spore.ObjectDesc, len(objects))
	for name, descriptor := range objects {
		desc, err := convertAppObject(name, descriptor)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNoRequestLayout, err)
		}
		converted[name] = desc
	}
	root, ok := converted[rootName]
	if !ok || root.Kind != spore.TypeKindStruct {
		return nil, fmt.Errorf("%w: object %q is not a struct in the descriptor table", ErrNoRequestLayout, rootName)
	}
	l := &RequestLayout{root: root, nested: map[string]spore.ObjectDesc{}}
	l.expandFrom(func(name string) (spore.ObjectDesc, bool) {
		desc, ok := converted[name]
		return desc, ok
	})
	return l, nil
}

// convertAppObject converts an app descriptor to a spore ObjectDesc without
// the strict registry validations of AppObjectDescriptors: the layout projects
// shapes only and does not need schema ids or hashes. Entries of Kind "enum"
// convert to an ObjectDesc of Kind enum whose fields name the members — the
// canonical spore EnumDesc shape projected onto the descriptor table's
// field-list container (the table has no dedicated member channel).
func convertAppObject(name string, d appgen.AppObjectDescriptor) (spore.ObjectDesc, error) {
	if name == "" || d.Name != name {
		return spore.ObjectDesc{}, fmt.Errorf("app schema descriptor %q has mismatched name %q", name, d.Name)
	}
	if spore.TypeKind(d.Kind) == spore.TypeKindEnum {
		members := make([]spore.FieldDesc, len(d.Fields))
		for i, field := range d.Fields {
			if field.Name == "" {
				return spore.ObjectDesc{}, fmt.Errorf("app schema descriptor %q has unnamed enum member", name)
			}
			members[i] = spore.FieldDesc{Name: field.Name}
		}
		return spore.ObjectDesc{Kind: spore.TypeKindEnum, Name: d.Name, Fields: members}, nil
	}
	fields := make([]spore.FieldDesc, len(d.Fields))
	for i, field := range d.Fields {
		if field.Name == "" {
			return spore.ObjectDesc{}, fmt.Errorf("app schema descriptor %q has unnamed field", name)
		}
		typ, err := appTypeDescriptor(field.Type)
		if err != nil {
			return spore.ObjectDesc{}, fmt.Errorf("app schema descriptor %q field %q: %w", name, field.Name, err)
		}
		fields[i] = spore.FieldDesc{Name: field.Name, Type: typ, Description: field.Description, Private: field.Private, Optional: field.Optional}
	}
	return spore.ObjectDesc{Kind: spore.TypeKind(d.Kind), Name: d.Name, Fields: fields}, nil
}

// Root returns the resolved request object shape.
func (l *RequestLayout) Root() spore.ObjectDesc { return l.root }

// mergeParamDescriptions copies descriptions from the manifest-derived params
// onto matching top-level fields (reflection carries no descriptions).
func mergeParamDescriptions(root *spore.ObjectDesc, params []appgen.CallableParam) {
	if len(params) == 0 {
		return
	}
	descByName := make(map[string]string, len(params))
	for _, p := range params {
		if p.Description != "" {
			descByName[p.Name] = p.Description
		}
	}
	for i := range root.Fields {
		if d, ok := descByName[root.Fields[i].Name]; ok {
			root.Fields[i].Description = d
		}
	}
}

// expandFrom materializes nested struct shapes referenced by the root (and by
// each other), keyed by struct name. Struct refs nested inside array element
// and map value types are followed as well; enum-typed fields resolve their
// member declarations the same way (the enum descriptor lands in the same
// nested table, distinguished by Kind). Cycles degrade to an unexpanded
// bare object instead of looping.
func (l *RequestLayout) expandFrom(lookup func(string) (spore.ObjectDesc, bool)) {
	pending := []spore.ObjectDesc{l.root}
	seen := map[string]bool{l.root.Name: true}
	for len(pending) > 0 {
		obj := pending[0]
		pending = pending[1:]
		for _, f := range obj.Fields {
			for _, name := range typeRefsIn(f.Type) {
				if name == "" || seen[name] {
					continue
				}
				seen[name] = true
				desc, ok := lookup(name)
				if !ok {
					continue
				}
				l.nested[name] = desc
				pending = append(pending, desc)
			}
		}
	}
}

// typeRefsIn collects the names of named type declarations (struct, class,
// enum) a type descriptor references, descending through array elements and
// map values.
func typeRefsIn(td spore.TypeDesc) []string {
	var refs []string
	switch td.Kind {
	case spore.TypeKindStruct, spore.TypeKindClass:
		if name := structRefName(td); name != "" {
			refs = append(refs, name)
		}
	case spore.TypeKindEnum:
		if name := enumRefName(td); name != "" {
			refs = append(refs, name)
		}
	case spore.TypeKindArray:
		if td.Element != nil {
			refs = append(refs, typeRefsIn(*td.Element)...)
		}
	case spore.TypeKindMap:
		if td.Value != nil {
			refs = append(refs, typeRefsIn(*td.Value)...)
		}
	}
	return refs
}

// structRefName returns the referenced struct/class name for expandable field
// types. Reflection-derived descriptors carry ClassName; app-descriptor
// conversions carry the struct name in Name.
func structRefName(td spore.TypeDesc) string {
	switch td.Kind {
	case spore.TypeKindStruct, spore.TypeKindClass:
		if td.ClassName != "" {
			return td.ClassName
		}
		if td.Name != "struct" && td.Name != "class" {
			return td.Name
		}
	}
	return ""
}

// enumRefName returns the enum declaration name an enum-typed field
// references. The spore script frontend and app descriptors carry it in Name;
// the generic label "enum" (e.g. from DescribeType of an enum-category TypeID)
// is not a resolvable name.
func enumRefName(td spore.TypeDesc) string {
	if td.Kind != spore.TypeKindEnum {
		return ""
	}
	if td.Name == "" || td.Name == "enum" {
		return ""
	}
	return td.Name
}

// enumAllowedValues resolves an enum-typed field's allowed member names from
// the expanded descriptor table (member declaration order preserved). It
// returns nil when the enum is unresolvable — no descriptor entry, an entry of
// the wrong kind, or an empty member set — signalling callers to degrade to
// plain-string semantics instead of blocking.
func (l *RequestLayout) enumAllowedValues(td spore.TypeDesc) []string {
	name := enumRefName(td)
	if name == "" {
		return nil
	}
	desc, ok := l.nested[name]
	if !ok || desc.Kind != spore.TypeKindEnum {
		return nil
	}
	members := make([]string, 0, len(desc.Fields))
	for _, f := range desc.Fields {
		if f.Name != "" {
			members = append(members, f.Name)
		}
	}
	if len(members) == 0 {
		return nil
	}
	return members
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

var (
	registryNameIndexOnce sync.Once
	registryNameIndex     map[string]reflect.Type
)

// registryTypeByName resolves a generated struct name to its reflect.Type via
// the codegen registry. The index is derived once from the immutable registry
// tables.
func registryTypeByName(name string) (reflect.Type, bool) {
	registryNameIndexOnce.Do(func() {
		registryNameIndex = make(map[string]reflect.Type, len(appgen.SchemaIDs))
		for id, structName := range appgen.SchemaIDs {
			if typ, ok := appgen.SchemaTypes[id]; ok {
				registryNameIndex[structName] = typ
			}
		}
	})
	typ, ok := registryNameIndex[name]
	return typ, ok
}

// --- JSON Schema projection ---

// JSONSchema returns the provider-facing input schema for the resolved shape:
// {"type":"object","properties":{...},"required":[...]} with nested structs
// expanded where the layout resolved them. Cyclic references degrade to a
// bare object.
func (l *RequestLayout) JSONSchema() string {
	encoded, err := json.Marshal(l.projectObject(l.root, l.root.Name, map[string]bool{}))
	if err != nil {
		return ""
	}
	return string(encoded)
}

func (l *RequestLayout) projectObject(obj spore.ObjectDesc, name string, visiting map[string]bool) map[string]any {
	if name != "" {
		if visiting[name] {
			return map[string]any{"type": "object"}
		}
		visiting[name] = true
		defer delete(visiting, name)
	}
	props := map[string]any{}
	var required []string
	for _, f := range obj.Fields {
		if f.Private {
			continue
		}
		prop := l.projectType(f.Type, visiting)
		if f.Description != "" {
			prop["description"] = f.Description
		}
		props[f.Name] = prop
		if !f.Optional {
			required = append(required, f.Name)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func (l *RequestLayout) projectType(td spore.TypeDesc, visiting map[string]bool) map[string]any {
	switch td.Kind {
	case spore.TypeKindScalar:
		return projectScalar(td.Name)
	case spore.TypeKindArray:
		if td.Element == nil {
			return map[string]any{"type": "array"}
		}
		return map[string]any{"type": "array", "items": l.projectType(*td.Element, visiting)}
	case spore.TypeKindMap:
		if td.Value == nil {
			return map[string]any{"type": "object"}
		}
		return map[string]any{"type": "object", "additionalProperties": l.projectType(*td.Value, visiting)}
	case spore.TypeKindStruct, spore.TypeKindClass:
		if desc, ok := l.nested[structRefName(td)]; ok && expandableObjectKind(desc.Kind) {
			return l.projectObject(desc, structRefName(td), visiting)
		}
		return map[string]any{"type": "object"}
	case spore.TypeKindEnum:
		prop := map[string]any{"type": "string"}
		// Allowed member names let form renderers offer a dropdown and let
		// the model pick from the closed set. Unresolvable enums degrade to
		// the plain-string shape (see enumAllowedValues).
		if allowed := l.enumAllowedValues(td); len(allowed) > 0 {
			prop["enum"] = allowed
		}
		return prop
	default:
		return map[string]any{}
	}
}

// expandableObjectKind reports whether a nested descriptor may be projected as
// an expanded object shape. Enum entries share the nested table and must not
// be mistaken for expandable structs.
func expandableObjectKind(kind spore.TypeKind) bool {
	return kind == spore.TypeKindStruct || kind == spore.TypeKindClass
}

// scalarFamily normalizes a scalar name to its JSON Schema family. Both the
// spore canonical vocabulary ("long", "ushort") and the Go-flavored names app
// descriptors carry ("int64", "uint8", "float32") map to the same families.
func scalarFamily(name string) string {
	switch name {
	case "bool", "boolean":
		return "boolean"
	case "string", "bytes":
		return "string"
	case "byte", "short", "ushort", "int", "uint", "long", "ulong",
		"int8", "int16", "int32", "int64", "uint8", "uint16", "uint32", "uint64":
		return "integer"
	case "float", "double", "float32", "float64":
		return "number"
	case "object":
		return "object"
	default:
		return "any"
	}
}

// projectScalar maps the scalar vocabulary onto JSON Schema types.
func projectScalar(name string) map[string]any {
	switch family := scalarFamily(name); family {
	case "any":
		return map[string]any{}
	case "object":
		return map[string]any{"type": "object"}
	default:
		return map[string]any{"type": family}
	}
}

// --- JSON payload validation ---

// Validate checks a decoded JSON payload against the resolved shape. A field
// declared non-optional must be present; present values must match their type.
// Unexpanded struct references accept any JSON object (structural degrade).
func (l *RequestLayout) Validate(payload map[string]any) []error {
	if payload == nil {
		return []error{errors.New("payload must be a JSON object")}
	}
	var errs []error
	errs = l.validateObject(l.root, payload, "", l.root.Name, map[string]bool{}, errs)
	return errs
}

// Normalize coerces card-authored scalar strings to the field's declared
// family and validates the payload in one pass. Card frontmatter scalars
// decode as strings regardless of the author's intent (the card parser
// keeps YAML string semantics), so `timeout_s: 60` arrives as "60".
// Normalize converts such strings to numbers/bools where the shape says
// so and returns the coerced payload plus any validation errors; the
// returned payload is the best-effort partial result when errors exist.
func (l *RequestLayout) Normalize(payload map[string]any) (map[string]any, []error) {
	if payload == nil {
		return nil, []error{errors.New("payload must be a JSON object")}
	}
	normalized, errs := l.normalizeObject(l.root, payload, "", l.root.Name, map[string]bool{})
	return normalized, errs
}

func (l *RequestLayout) normalizeObject(obj spore.ObjectDesc, payload map[string]any, path, name string, visiting map[string]bool) (map[string]any, []error) {
	if name != "" {
		if visiting[name] {
			return payload, nil // cyclic reference: accept any object here
		}
		visiting[name] = true
		defer delete(visiting, name)
	}
	out := make(map[string]any, len(payload))
	var errs []error
	for _, f := range obj.Fields {
		if f.Private {
			continue
		}
		p := f.Name
		if path != "" {
			p = path + "." + f.Name
		}
		value, present := payload[f.Name]
		if !present {
			if !f.Optional {
				errs = append(errs, fmt.Errorf("%s is required", p))
			}
			continue
		}
		normalized, nerrs := l.normalizeValue(f.Type, value, p, visiting)
		out[f.Name] = normalized
		errs = append(errs, nerrs...)
	}
	// Preserve keys the layout does not describe so executor payloads can
	// carry extra context without being silently dropped.
	for k, v := range payload {
		if _, described := fieldByName(obj, k); !described {
			out[k] = v
		}
	}
	return out, errs
}

func fieldByName(obj spore.ObjectDesc, name string) (spore.FieldDesc, bool) {
	for _, f := range obj.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return spore.FieldDesc{}, false
}

func (l *RequestLayout) normalizeValue(td spore.TypeDesc, value any, path string, visiting map[string]bool) (any, []error) {
	switch td.Kind {
	case spore.TypeKindScalar:
		return coerceScalar(td.Name, value, path)
	case spore.TypeKindArray:
		items, ok := toJSONSlice(value)
		if !ok {
			return value, []error{fmt.Errorf("%s: expected array, got %s", path, jsonTypeName(value))}
		}
		if td.Element == nil {
			return items, nil
		}
		out := make([]any, len(items))
		var errs []error
		for i, item := range items {
			normalized, nerrs := l.normalizeValue(*td.Element, item, fmt.Sprintf("%s[%d]", path, i), visiting)
			out[i] = normalized
			errs = append(errs, nerrs...)
		}
		return out, errs
	case spore.TypeKindMap:
		m, ok := value.(map[string]any)
		if !ok {
			return value, []error{fmt.Errorf("%s: expected object, got %s", path, jsonTypeName(value))}
		}
		if td.Value == nil {
			return m, nil
		}
		out := make(map[string]any, len(m))
		var errs []error
		for k, item := range m {
			normalized, nerrs := l.normalizeValue(*td.Value, item, path+"."+k, visiting)
			out[k] = normalized
			errs = append(errs, nerrs...)
		}
		return out, errs
	case spore.TypeKindStruct, spore.TypeKindClass:
		m, ok := value.(map[string]any)
		if !ok {
			return value, []error{fmt.Errorf("%s: expected object, got %s", path, jsonTypeName(value))}
		}
		if desc, ok := l.nested[structRefName(td)]; ok && expandableObjectKind(desc.Kind) {
			return l.normalizeObject(desc, m, path, structRefName(td), visiting)
		}
		return m, nil
	case spore.TypeKindEnum:
		s, ok := value.(string)
		if !ok {
			return value, []error{fmt.Errorf("%s: expected string (enum), got %s", path, jsonTypeName(value))}
		}
		if allowed := l.enumAllowedValues(td); len(allowed) > 0 && !containsString(allowed, s) {
			return value, []error{enumValueError(path, td.Name, s, allowed)}
		}
		return value, nil
	default:
		return value, nil
	}
}

// toJSONSlice widens card-parser list forms ([]string from block lists,
// []any from flow arrays) into []any. Non-slice values return false.
func toJSONSlice(value any) ([]any, bool) {
	switch v := value.(type) {
	case []any:
		return v, true
	case []string:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out, true
	default:
		return nil, false
	}
}

// coerceScalar converts card-authored strings to the declared scalar family
// and validates the value. JSON-decoded values pass through unchanged when
// already well-typed.
func coerceScalar(name string, value any, path string) (any, []error) {
	switch family := scalarFamily(name); family {
	case "boolean":
		switch v := value.(type) {
		case bool:
			return v, nil
		case string:
			if v == "true" {
				return true, nil
			}
			if v == "false" {
				return false, nil
			}
		}
		return value, []error{fmt.Errorf("%s: expected bool, got %s", path, jsonTypeName(value))}
	case "string":
		if _, ok := value.(string); ok {
			return value, nil
		}
		return value, []error{fmt.Errorf("%s: expected string, got %s", path, jsonTypeName(value))}
	case "integer":
		switch v := value.(type) {
		case float64:
			if v != float64(int64(v)) {
				return value, []error{fmt.Errorf("%s: expected integer, got non-integral number", path)}
			}
			return v, nil
		case int:
			return float64(v), nil
		case int64:
			return float64(v), nil
		case string:
			if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
				return float64(parsed), nil
			}
		}
		return value, []error{fmt.Errorf("%s: expected integer, got %s", path, jsonTypeName(value))}
	case "number":
		switch v := value.(type) {
		case float64:
			return v, nil
		case int:
			return float64(v), nil
		case int64:
			return float64(v), nil
		case string:
			if parsed, err := strconv.ParseFloat(v, 64); err == nil {
				return parsed, nil
			}
		}
		return value, []error{fmt.Errorf("%s: expected number, got %s", path, jsonTypeName(value))}
	default:
		return value, nil
	}
}

// ValidateJSON unmarshals raw JSON bytes and validates the result.
func (l *RequestLayout) ValidateJSON(raw []byte) []error {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return []error{fmt.Errorf("payload is not a JSON object: %w", err)}
	}
	return l.Validate(payload)
}

func (l *RequestLayout) validateObject(obj spore.ObjectDesc, payload map[string]any, path, name string, visiting map[string]bool, errs []error) []error {
	if name != "" {
		if visiting[name] {
			return errs // cyclic reference: accept any object here
		}
		visiting[name] = true
		defer delete(visiting, name)
	}
	for _, f := range obj.Fields {
		if f.Private {
			continue
		}
		p := f.Name
		if path != "" {
			p = path + "." + f.Name
		}
		value, present := payload[f.Name]
		if !present {
			if !f.Optional {
				errs = append(errs, fmt.Errorf("%s is required", p))
			}
			continue
		}
		errs = l.validateType(f.Type, value, p, visiting, errs)
	}
	return errs
}

func (l *RequestLayout) validateType(td spore.TypeDesc, value any, path string, visiting map[string]bool, errs []error) []error {
	switch td.Kind {
	case spore.TypeKindScalar:
		return validateScalarValue(td.Name, value, path, errs)
	case spore.TypeKindArray:
		items, ok := value.([]any)
		if !ok {
			return append(errs, fmt.Errorf("%s: expected array, got %s", path, jsonTypeName(value)))
		}
		if td.Element != nil {
			for i, item := range items {
				errs = l.validateType(*td.Element, item, fmt.Sprintf("%s[%d]", path, i), visiting, errs)
			}
		}
		return errs
	case spore.TypeKindMap:
		m, ok := value.(map[string]any)
		if !ok {
			return append(errs, fmt.Errorf("%s: expected object, got %s", path, jsonTypeName(value)))
		}
		if td.Value != nil {
			for k, item := range m {
				errs = l.validateType(*td.Value, item, path+"."+k, visiting, errs)
			}
		}
		return errs
	case spore.TypeKindStruct, spore.TypeKindClass:
		m, ok := value.(map[string]any)
		if !ok {
			return append(errs, fmt.Errorf("%s: expected object, got %s", path, jsonTypeName(value)))
		}
		if desc, ok := l.nested[structRefName(td)]; ok && expandableObjectKind(desc.Kind) {
			return l.validateObject(desc, m, path, structRefName(td), visiting, errs)
		}
		return errs
	case spore.TypeKindEnum:
		s, ok := value.(string)
		if !ok {
			return append(errs, fmt.Errorf("%s: expected string (enum), got %s", path, jsonTypeName(value)))
		}
		if allowed := l.enumAllowedValues(td); len(allowed) > 0 && !containsString(allowed, s) {
			return append(errs, enumValueError(path, td.Name, s, allowed))
		}
		return errs
	default:
		return errs
	}
}

// validateScalarValue checks one JSON value against a scalar name. JSON
// numbers decode to float64; integer-typed fields require an integral value.
func validateScalarValue(name string, value any, path string, errs []error) []error {
	switch family := scalarFamily(name); family {
	case "boolean":
		if _, ok := value.(bool); !ok {
			return append(errs, fmt.Errorf("%s: expected bool, got %s", path, jsonTypeName(value)))
		}
	case "string":
		if _, ok := value.(string); !ok {
			return append(errs, fmt.Errorf("%s: expected string, got %s", path, jsonTypeName(value)))
		}
	case "integer":
		if !isIntegralNumber(value) {
			return append(errs, fmt.Errorf("%s: expected integer, got %s", path, jsonTypeName(value)))
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return append(errs, fmt.Errorf("%s: expected number, got %s", path, jsonTypeName(value)))
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return append(errs, fmt.Errorf("%s: expected object, got %s", path, jsonTypeName(value)))
		}
	}
	return errs
}

func isIntegralNumber(value any) bool {
	f, ok := value.(float64)
	if !ok {
		return false
	}
	return f == float64(int64(f))
}

// enumValueError reports a payload string outside the resolved enum member
// set. The message carries the field path, the offending value, and the full
// allowed list so downstream surfaces (task card errors, agent retries) can
// self-correct without re-resolving the schema.
func enumValueError(path, enumName, value string, allowed []string) error {
	if enumName == "" {
		enumName = "enum"
	}
	return fmt.Errorf("%s: %q is not a valid %s value (allowed: %s)", path, value, enumName, strings.Join(allowed, ", "))
}

func jsonTypeName(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", value)
	}
}
