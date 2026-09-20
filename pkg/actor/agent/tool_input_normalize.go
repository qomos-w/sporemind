package agent

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// normalizeToolInputKeys rewrites top-level JSON object keys in input so they
// match the property names declared in the tool's InputSchema, and coerces
// scalar string values to the schema-declared primitive type (integer/number/
// boolean) so LLM payloads like {"Limit":"5"} decode cleanly into typed Go
// structs. This makes agent toolcalls case-insensitive, underscore-insensitive
// (camelCase keys like "OldString" match snake_case schema params like
// "old_string"), and type-lenient for LLM-generated payloads without changing
// the underlying gospore JSON decoder.
//
// Only top-level keys are normalized; nested objects are left as-is because
// gospore-generated request types also declare their own JSON tags and the
// common case for LLM tool arguments is flat scalar fields.
func normalizeToolInputKeys(input string, schema string) (string, error) {
	if input == "" || schema == "" {
		return input, nil
	}
	type propSpec struct {
		Type string `json:"type"`
	}
	var schemaObj struct {
		Properties map[string]propSpec `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schema), &schemaObj); err != nil || len(schemaObj.Properties) == 0 {
		return input, nil
	}

	lowerToCanonical := make(map[string]string, len(schemaObj.Properties))
	underscorelessToCanonical := make(map[string]string, len(schemaObj.Properties))
	propByCanonical := make(map[string]propSpec, len(schemaObj.Properties))
	for name, spec := range schemaObj.Properties {
		lower := strings.ToLower(name)
		lowerToCanonical[lower] = name
		underscorelessToCanonical[strings.ReplaceAll(lower, "_", "")] = name
		propByCanonical[name] = spec
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return input, nil
	}

	normalized := make(map[string]json.RawMessage, len(payload))
	for k, v := range payload {
		var canonical string
		lowerKey := strings.ToLower(k)
		if c, ok := lowerToCanonical[lowerKey]; ok {
			canonical = c
		} else if c, ok := underscorelessToCanonical[strings.ReplaceAll(lowerKey, "_", "")]; ok {
			canonical = c
		} else {
			canonical = k
		}
		if spec, ok := propByCanonical[canonical]; ok {
			if coerced, changed := coerceScalarType(v, spec.Type); changed {
				v = coerced
			}
		}
		normalized[canonical] = v
	}

	out, err := json.Marshal(normalized)
	if err != nil {
		return input, err
	}
	return string(out), nil
}

// coerceScalarType attempts to convert a JSON string value into the JSON token
// shape expected by schemaType (integer/number/boolean/array/object). It returns
// the (possibly rewritten) raw value and a flag indicating whether coercion was
// applied. Non-string inputs and unsupported types are returned unchanged so
// that the strict decoder still surfaces genuine type errors.
func coerceScalarType(raw json.RawMessage, schemaType string) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return raw, false
	}
	if raw[0] != '"' {
		return raw, false
	}
	switch schemaType {
	case "integer", "number":
		var s string
		if err := json.Unmarshal(raw, &s); err != nil || s == "" {
			return raw, false
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return raw, false
		}
		if schemaType == "integer" {
			return []byte(strconv.FormatInt(int64(n), 10)), true
		}
		return []byte(strconv.FormatFloat(n, 'f', -1, 64)), true
	case "boolean":
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return raw, false
		}
		switch strings.ToLower(s) {
		case "true":
			return []byte("true"), true
		case "false":
			return []byte("false"), true
		}
		return raw, false
	case "array", "object":
		// LLMs occasionally double-encode JSON payload fields (e.g. Args passed
		// as a JSON-stringified string). Unwrap when the string contents parse
		// as the schema-declared composite type; otherwise leave the strict
		// decoder to surface the type error.
		var s string
		if err := json.Unmarshal(raw, &s); err != nil || strings.TrimSpace(s) == "" {
			return raw, false
		}
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return raw, false
		}
		if (schemaType == "array") != isJSONArray(s) {
			return raw, false
		}
		b, err := json.Marshal(v)
		if err != nil {
			return raw, false
		}
		return b, true
	}
	return raw, false
}

// isJSONArray reports whether s starts with '[' (after leading whitespace) —
// the JSON token shape expected of a top-level array.
func isJSONArray(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "[")
}

// toolSpecByCallableID looks up a tool spec by CallableID from the engine's
// current allTools set. It returns nil when no matching spec is found.
func (e *turnEngine) toolSpecByCallableID(callableID string) *domain.ToolSpec {
	for i := range e.allTools {
		if e.allTools[i].CallableID == callableID {
			return &e.allTools[i]
		}
	}
	return nil
}
