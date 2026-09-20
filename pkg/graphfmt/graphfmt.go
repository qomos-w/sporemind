// Package graphfmt parses the brace-delimited format used for Project Graph
// LLM I/O. See prompts/project-graph/graph-format.md.
package graphfmt

import (
	"fmt"
	"strings"
)

// Prop is a single key-value property on a Node.
type Prop struct {
	Key   string
	Value string
}

// Node is the intermediate representation produced by Parse.
// Conversion to typed structs (TargetGraph, RealityGraph, etc.)
// is the caller's responsibility, driven by GraphKind.
type Node struct {
	Type     string
	ID       string
	Props    []Prop
	Children []*Node
}

// Get returns the value of the first property with the given key,
// or "" if not found.
func (n *Node) Get(key string) string {
	for _, p := range n.Props {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// GetList splits a comma-separated property value into trimmed fields.
func (n *Node) GetList(key string) []string {
	v := n.Get(key)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// FindChildren returns all direct children with the given Type.
func (n *Node) FindChildren(typ string) []*Node {
	var out []*Node
	for _, c := range n.Children {
		if c.Type == typ {
			out = append(out, c)
		}
	}
	return out
}

// FindByType walks the forest and returns all nodes with the given Type.
func FindByType(roots []*Node, typ string) []*Node {
	var out []*Node
	for _, n := range roots {
		collectByType(n, typ, &out)
	}
	return out
}

func collectByType(n *Node, typ string, out *[]*Node) {
	if n.Type == typ {
		*out = append(*out, n)
	}
	for _, c := range n.Children {
		collectByType(c, typ, out)
	}
}

// Parse converts brace-delimited text into a forest of Nodes.
//
// Rules:
//   - A line ending with "{" begins a new node. The preceding text is the
//     header: "Type" or "Type Id".
//   - A line containing ":" is a property (key: value) of the most recent
//     open node.
//   - A line containing only "}" closes the current node.
//   - Indentation is ignored; nesting is determined entirely by braces.
//   - Empty lines are ignored.
func Parse(text string) ([]*Node, error) {
	lines := strings.Split(text, "\n")

	var stack []*Node
	var roots []*Node

	for i, raw := range lines {
		lineNo := i + 1

		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}

		// Node end.
		if trimmed == "}" {
			if len(stack) == 0 {
				return nil, fmt.Errorf("graphfmt: line %d: unexpected }", lineNo)
			}
			completed := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			if len(stack) == 0 {
				roots = append(roots, completed)
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, completed)
			}
			continue
		}

		// Node start.
		if strings.HasSuffix(trimmed, "{") {
			header := strings.TrimSpace(strings.TrimSuffix(trimmed, "{"))
			parts := strings.SplitN(header, " ", 2)
			node := &Node{Type: parts[0]}
			if len(parts) > 1 {
				node.ID = strings.TrimSpace(parts[1])
			}
			stack = append(stack, node)
			continue
		}

		// Property line.
		if len(stack) == 0 {
			return nil, fmt.Errorf("graphfmt: line %d: property %q before any node", lineNo, trimmed)
		}
		colonIdx := strings.Index(trimmed, ":")
		if colonIdx < 0 {
			return nil, fmt.Errorf("graphfmt: line %d: expected 'key: value', got %q", lineNo, trimmed)
		}
		key := strings.TrimSpace(trimmed[:colonIdx])
		val := strings.TrimSpace(trimmed[colonIdx+1:])
		current := stack[len(stack)-1]
		current.Props = append(current.Props, Prop{Key: key, Value: val})
	}

	if len(stack) > 0 {
		return nil, fmt.Errorf("graphfmt: %d unclosed node(s)", len(stack))
	}

	return roots, nil
}

// Format converts a forest of Nodes back to brace-delimited text.
// Output is indented for readability, but indentation is not required
// by the parser.
func Format(nodes []*Node) string {
	var sb strings.Builder
	for _, n := range nodes {
		formatNode(&sb, n, 0)
	}
	return sb.String()
}

func formatNode(sb *strings.Builder, n *Node, level int) {
	indent := strings.Repeat("\t", level)
	header := n.Type
	if n.ID != "" {
		header += " " + n.ID
	}
	fmt.Fprintf(sb, "%s%s {\n", indent, header)

	for _, p := range n.Props {
		fmt.Fprintf(sb, "%s\t%s: %s\n", indent, p.Key, p.Value)
	}

	for _, c := range n.Children {
		formatNode(sb, c, level+1)
	}

	fmt.Fprintf(sb, "%s}\n", indent)
}
