package project

import "strings"

// parseScalarValue mirrors the frontend's YAML scalar semantics for data-block
// values: bare true/false become Go bools, quoted strings stay strings. This
// keeps round-trips stable — data["template"]=true written as `template: true`
// decodes back as bool true (not "true"), so strict consumers
// (Data["editable"].(bool), frontend card.data?.template === true) see the
// same type before and after a restart.
//
// Unquoted flow-style arrays (`key: [a, b]`) decode as []any. Hand-authored
// frontmatter (agent-written plan cards) sometimes uses the flow form for
// data.scope.include; without this the whole bracket text stays a string and
// workflowTaskIDs' []any assert silently drops every child.
func parseScalarValue(value string) any {
	if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
		return value[1 : len(value)-1]
	}
	if value == "true" {
		return true
	}
	if value == "false" {
		return false
	}
	if len(value) >= 2 && value[0] == '[' && value[len(value)-1] == ']' {
		return parseFlowArray(value[1 : len(value)-1])
	}
	return value
}

// parseFlowArray splits a flow-array body ("a, b, c") into []any, stripping
// per-item quotes. Empty bodies yield an empty (non-nil) slice so `key: []`
// decodes as an empty array rather than a string.
func parseFlowArray(body string) []any {
	out := []any{}
	for _, part := range strings.Split(body, ",") {
		item := strings.TrimSpace(part)
		if len(item) >= 2 && ((item[0] == '\'' && item[len(item)-1] == '\'') || (item[0] == '"' && item[len(item)-1] == '"')) {
			item = item[1 : len(item)-1]
		}
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func parseDataBlock(lines []string, start int) (map[string]any, int) {
	data := map[string]any{}
	i := start
	baseIndent := -1
	for ; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := leadingSpaces(line)
		if baseIndent < 0 {
			baseIndent = indent
		}
		if indent < baseIndent {
			break
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" {
			if items, next := collectListItems(lines, i+1); len(items) > 0 {
				data[key] = items
				i = next - 1
				continue
			}
			nested, next := parseNestedDataBlock(lines, i+1, indent)
			if len(nested) > 0 {
				data[key] = nested
				i = next
				continue
			}
		}
		data[key] = parseScalarValue(value)
	}
	return data, i - 1
}

func parseNestedDataBlock(lines []string, start, parentIndent int) (map[string]any, int) {
	data := map[string]any{}
	i := start
	childIndent := -1
	for ; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := leadingSpaces(line)
		if indent <= parentIndent {
			break
		}
		if childIndent < 0 {
			childIndent = indent
		}
		if indent < childIndent {
			break
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" {
			if items, next := collectListItems(lines, i+1); len(items) > 0 {
				data[key] = items
				i = next - 1
				continue
			}
			nested, next := parseNestedDataBlock(lines, i+1, indent)
			if len(nested) > 0 {
				data[key] = nested
				i = next
				continue
			}
		}
		data[key] = parseScalarValue(value)
	}
	return data, i - 1
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}
