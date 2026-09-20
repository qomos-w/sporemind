package llmclient

import "encoding/json"

// stripNullSchemaValues removes keyword entries whose value is JSON null from
// a tool input schema. Some schema sources (MCP servers passed through
// verbatim, hand-written component frontmatter) emit "required": null, which
// violates the JSON Schema metaschema — required must be an array of strings —
// and makes strict providers reject the whole request with HTTP 400. Keywords
// that legitimately accept null as a value (default, const) are preserved.
func stripNullSchemaValues(schema string) string {
	if schema == "" {
		return schema
	}
	var obj any
	if err := json.Unmarshal([]byte(schema), &obj); err != nil {
		return schema
	}
	out, err := json.Marshal(stripNullSchemaNode(obj))
	if err != nil {
		return schema
	}
	return string(out)
}

func stripNullSchemaNode(node any) any {
	switch v := node.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for k, val := range v {
			if val == nil && k != "default" && k != "const" {
				continue
			}
			result[k] = stripNullSchemaNode(val)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, val := range v {
			result[i] = stripNullSchemaNode(val)
		}
		return result
	default:
		return node
	}
}
