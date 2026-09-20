package graphfmt

import (
	"strings"
	"testing"
)

func TestParseBasic(t *testing.T) {
	input := strings.Join([]string{
		"Concept GraphDiffPlanner {",
		"	desc: Groups semantic diffs into minimal moves",
		"	state: planned",
		"}",
		"Concept AlignmentMovePlanner {",
		"	desc: Plans alignment moves from diff",
		"	depends_on: GraphDiffPlanner",
		"}",
	}, "\n")

	roots, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("expected 2 root nodes, got %d", len(roots))
	}

	n := roots[0]
	if n.Type != "Concept" || n.ID != "GraphDiffPlanner" {
		t.Fatalf("unexpected node: %+v", n)
	}
	if n.Get("desc") != "Groups semantic diffs into minimal moves" {
		t.Fatalf("unexpected desc: %q", n.Get("desc"))
	}
	if n.Get("state") != "planned" {
		t.Fatalf("unexpected state: %q", n.Get("state"))
	}
}

func TestParseNoIndent(t *testing.T) {
	input := strings.Join([]string{
		"Concept GraphDiffPlanner {",
		"desc: Groups semantic diffs into minimal moves",
		"state: planned",
		"}",
	}, "\n")

	roots, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(roots))
	}
	if roots[0].Get("desc") != "Groups semantic diffs into minimal moves" {
		t.Fatalf("unexpected desc: %q", roots[0].Get("desc"))
	}
}

func TestParseNestedChildren(t *testing.T) {
	input := strings.Join([]string{
		"Concept WorkspaceMounting {",
		"	desc: Owns mount/unmount lifecycle",
		"	state: real",
		"	FileAnchor {",
		"		path: pkg/actor/workspace/workspace.go",
		"		role: primary",
		"	}",
		"	Concept MountCommandHandling {",
		"		desc: Handles mount commands",
		"		part_of: WorkspaceMounting",
		"	}",
		"}",
	}, "\n")

	roots, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(roots))
	}

	top := roots[0]
	if len(top.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(top.Children))
	}

	anchor := top.Children[0]
	if anchor.Type != "FileAnchor" {
		t.Fatalf("expected FileAnchor, got %s", anchor.Type)
	}
	if anchor.Get("path") != "pkg/actor/workspace/workspace.go" {
		t.Fatalf("unexpected path: %q", anchor.Get("path"))
	}

	child := top.Children[1]
	if child.Type != "Concept" || child.ID != "MountCommandHandling" {
		t.Fatalf("unexpected child: %+v", child)
	}
}

func TestParseGetList(t *testing.T) {
	input := "Capability SemanticEvolutionKanban {\n\tconcepts: GraphDiffPlanner, AlignmentMovePlanner, KanbanCardEmitter\n}\n"

	roots, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	list := roots[0].GetList("concepts")
	if len(list) != 3 {
		t.Fatalf("expected 3 concepts, got %d", len(list))
	}
	if list[0] != "GraphDiffPlanner" || list[2] != "KanbanCardEmitter" {
		t.Fatalf("unexpected list: %v", list)
	}
}

func TestParseEmptyLines(t *testing.T) {
	input := "Concept A {\n\n\n\tdesc: hello\n\n}\n\nConcept B {\n}\n"

	roots, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(roots))
	}
	if roots[0].Get("desc") != "hello" {
		t.Fatalf("unexpected desc: %q", roots[0].Get("desc"))
	}
}

func TestParsePropertyBeforeNode(t *testing.T) {
	_, err := Parse("desc: orphan property")
	if err == nil {
		t.Fatal("expected error for property before any node")
	}
}

func TestParseUnexpectedClose(t *testing.T) {
	_, err := Parse("}")
	if err == nil {
		t.Fatal("expected error for unexpected }")
	}
}

func TestParseMissingColon(t *testing.T) {
	_, err := Parse("Concept A {\n\tno colon here\n}")
	if err == nil {
		t.Fatal("expected error for property without colon")
	}
}

func TestParseUnclosedNode(t *testing.T) {
	_, err := Parse("Concept A {\n\tdesc: hello\n")
	if err == nil {
		t.Fatal("expected error for unclosed node")
	}
}

func TestFormatRoundTrip(t *testing.T) {
	original := &Node{
		Type: "Concept",
		ID:   "GraphDiffPlanner",
		Props: []Prop{
			{Key: "desc", Value: "Groups semantic diffs into minimal moves"},
			{Key: "state", Value: "planned"},
		},
		Children: []*Node{
			{
				Type: "FileAnchor",
				Props: []Prop{
					{Key: "path", Value: "pkg/graphfmt/test.go"},
					{Key: "role", Value: "primary"},
				},
			},
		},
	}

	formatted := Format([]*Node{original})
	parsed, err := Parse(formatted)
	if err != nil {
		t.Fatalf("round-trip parse failed: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 root after round-trip, got %d", len(parsed))
	}
	if parsed[0].ID != "GraphDiffPlanner" {
		t.Fatalf("unexpected id: %q", parsed[0].ID)
	}
	if len(parsed[0].Children) != 1 {
		t.Fatalf("expected 1 child after round-trip, got %d", len(parsed[0].Children))
	}
	anchor := parsed[0].Children[0]
	if anchor.Get("path") != "pkg/graphfmt/test.go" {
		t.Fatalf("unexpected path: %q", anchor.Get("path"))
	}
}

func TestFindByType(t *testing.T) {
	input := strings.Join([]string{
		"Concept A {",
		"	FileAnchor {",
		"		path: a.go",
		"	}",
		"}",
		"Concept B {",
		"	FileAnchor {",
		"		path: b.go",
		"	}",
		"}",
	}, "\n")

	roots, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}

	concepts := FindByType(roots, "Concept")
	if len(concepts) != 2 {
		t.Fatalf("expected 2 concepts, got %d", len(concepts))
	}

	anchors := FindByType(roots, "FileAnchor")
	if len(anchors) != 2 {
		t.Fatalf("expected 2 anchors, got %d", len(anchors))
	}
}
