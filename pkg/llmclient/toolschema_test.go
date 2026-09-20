package llmclient

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStripNullSchemaValues(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		drops []string
		keeps []string
	}{
		{
			name:  "null required removed",
			in:    `{"type":"object","properties":{"path":{"type":"string"}},"required":null}`,
			drops: []string{`"required"`},
			keeps: []string{`"properties"`, `"path"`},
		},
		{
			name:  "nested null keywords removed",
			in:    `{"type":"object","properties":{"a":{"type":"object","required":null,"properties":{"b":{"type":"string","enum":null}}}},"description":null}`,
			drops: []string{`"required":null`, `"enum":null`, `"description":null`},
			keeps: []string{`"type":"object"`, `"b"`},
		},
		{
			name:  "default and const null preserved",
			in:    `{"type":["string","null"],"default":null,"properties":{"a":{"const":null}}}`,
			keeps: []string{`"default":null`, `"const":null`},
		},
		{
			name:  "valid required array untouched",
			in:    `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`,
			keeps: []string{`"required":["a"]`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripNullSchemaValues(tc.in)
			for _, s := range tc.drops {
				if strings.Contains(out, s) {
					t.Errorf("output %q still contains %q", out, s)
				}
			}
			for _, s := range tc.keeps {
				if !strings.Contains(out, s) {
					t.Errorf("output %q lost %q", out, s)
				}
			}
			if !json.Valid([]byte(out)) {
				t.Errorf("output %q is not valid JSON", out)
			}
		})
	}
}

func TestStripNullSchemaValuesEdgeCases(t *testing.T) {
	if got := stripNullSchemaValues(""); got != "" {
		t.Errorf("empty schema: got %q", got)
	}
	invalid := `{"type":"object",`
	if got := stripNullSchemaValues(invalid); got != invalid {
		t.Errorf("invalid JSON must pass through unchanged, got %q", got)
	}
}
