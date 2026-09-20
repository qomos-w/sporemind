// Package converter converts structured handler results into plain text
// formatted for LLM consumption. Each converter is registered by its
// Go return type using reflection.
package converter

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// Converter turns a structured result into a plain text string.
type Converter func(result any) (string, error)

// Register binds a converter to a specific result type.
// Panics if the same type is registered twice (programmer error).
func Register(resultType reflect.Type, c Converter) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[resultType]; ok {
		panic(fmt.Sprintf("converter: type %v already registered", resultType))
	}
	registry[resultType] = c
}

// Lookup returns the converter for a result type, or nil if not registered.
func Lookup(resultType reflect.Type) Converter {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[resultType]
}

// LookupValue is a convenience that looks up by the dynamic type of a value.
func LookupValue(v any) Converter {
	if v == nil {
		return nil
	}
	return Lookup(reflect.TypeOf(v))
}

// Convert looks up the converter for the value's type and runs it.
// If no converter is registered, it falls back to JSON marshaling.
func Convert(v any) string {
	if v == nil {
		return "null"
	}
	c := LookupValue(v)
	if c == nil {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("{error: %v}", err)
		}
		return string(b)
	}
	s, err := c(v)
	if err != nil {
		// Fallback to JSON on converter error.
		b, _ := json.Marshal(v)
		return string(b)
	}
	return s
}
