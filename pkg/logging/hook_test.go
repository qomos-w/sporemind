package logging

import (
	"errors"
	"fmt"
	"testing"
)

func TestSanitizeFields_ErrorToString(t *testing.T) {
	fields := map[string]any{
		"error": errors.New("boom"),
		"err":   fmt.Errorf("wrapped: %w", errors.New("inner")),
		"name":  "workspace",
		"count": 3,
	}
	out := sanitizeFields(fields)
	if got, ok := out["error"].(string); !ok || got != "boom" {
		t.Errorf("error field should become string %q, got %#v", "boom", out["error"])
	}
	if got, ok := out["err"].(string); !ok || got != "wrapped: inner" {
		t.Errorf("err field should become string %q, got %#v", "wrapped: inner", out["err"])
	}
	if out["name"] != "workspace" || out["count"] != 3 {
		t.Error("non-error fields must be left untouched")
	}
}

func TestSanitizeFields_Nil(t *testing.T) {
	if got := sanitizeFields(nil); got != nil {
		t.Errorf("nil fields should stay nil, got %#v", got)
	}
}
