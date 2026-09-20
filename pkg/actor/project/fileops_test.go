package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// --- collapseWhitespace / collapseWhitespaceWithMap ---

func TestCollapseWhitespace(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no whitespace", "hello", "hello"},
		{"single space", "hello world", "hello world"},
		{"multiple spaces", "hello   world", "hello world"},
		{"tabs", "hello\t\tworld", "hello world"},
		{"mixed tabs and spaces", "hello \t \tworld", "hello world"},
		{"leading whitespace", "\t\t  hello", " hello"},
		{"trailing whitespace", "hello  \t\t", "hello "},
		{"all whitespace", "\t \t ", " "},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collapseWhitespace(tt.in)
			if got != tt.want {
				t.Errorf("collapseWhitespace(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCollapseWhitespaceWithMap_PreservesNonWhitespacePositions(t *testing.T) {
	s := "a\t\tbcd"
	out, idxMap := collapseWhitespaceWithMap(s)
	// "a\t\tbcd" → "a bcd" (4 chars collapsed to 1)
	// idxMap should map: a→0, space→1, b→3, c→4, d→5
	if out != "a bcd" {
		t.Fatalf("got %q, want %q", out, "a bcd")
	}
	expected := []int{0, 1, 3, 4, 5}
	if len(idxMap) != len(expected) {
		t.Fatalf("idxMap length %d, want %d", len(idxMap), len(expected))
	}
	for i, v := range expected {
		if idxMap[i] != v {
			t.Errorf("idxMap[%d] = %d, want %d", i, idxMap[i], v)
		}
	}
}

// --- findWhitespaceTolerant ---

func TestFindWhitespaceTolerant(t *testing.T) {
	tests := []struct {
		name    string
		content string
		search  string
		want    string
		ok      bool
	}{
		{
			name:    "content tabs, search spaces",
			content: "func foo() {\n\t\tcase \"type\":\n\t\t\treturn nil\n}",
			search:  "    case \"type\":",
			want:    "\t\tcase \"type\":",
			ok:      true,
		},
		{
			name:    "content spaces, search tabs",
			content: "    case \"type\":",
			search:  "\t\tcase \"type\":",
			want:    "    case \"type\":",
			ok:      true,
		},
		{
			name:    "different tab count still matches",
			content: "\t\t\t\tdeep indent",
			search:  "\tdeep indent",
			want:    "\t\t\t\tdeep indent",
			ok:      true,
		},
		{
			name:    "mixed whitespace in middle",
			content: "foo\t \tbar",
			search:  "foo bar",
			want:    "foo\t \tbar",
			ok:      true,
		},
		{
			name:    "exact match preserved",
			content: "hello world",
			search:  "hello world",
			want:    "hello world",
			ok:      true,
		},
		{
			name:    "substring within larger content",
			content: "package main\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n",
			search:  "	fmt.Println(\"hi\")",
			want:    "\tfmt.Println(\"hi\")",
			ok:      true,
		},
		{
			name:    "no match",
			content: "hello world",
			search:  "goodbye",
			want:    "",
			ok:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findWhitespaceTolerant(tt.content, tt.search)
			if ok != tt.ok {
				t.Fatalf("findWhitespaceTolerant ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("findWhitespaceTolerant got %q, want %q", got, tt.want)
			}
		})
	}
}

// --- findActualString ---

func TestFindActualString(t *testing.T) {
	tests := []struct {
		name    string
		content string
		search  string
		want    string
		ok      bool
	}{
		{
			name:    "exact match",
			content: "hello world",
			search:  "hello",
			want:    "hello",
			ok:      true,
		},
		{
			name:    "curly quotes normalized",
			content: `say "hi"`,
			search:  "say \u201chi\u201d",
			want:    `say "hi"`,
			ok:      true,
		},
		{
			name:    "tabs in content, spaces in search",
			content: "\t\tcase \"type\":",
			search:  "    case \"type\":",
			want:    "\t\tcase \"type\":",
			ok:      true,
		},
		{
			name:    "spaces in content, tabs in search",
			content: "    case \"type\":",
			search:  "\t\tcase \"type\":",
			want:    "    case \"type\":",
			ok:      true,
		},
		{
			name:    "literal backslash-t in search matches real tab",
			content: "\thello",
			search:  "\\thello",
			want:    "\thello",
			ok:      true,
		},
		{
			name:    "literal backslash-n in search matches newline",
			content: "line one\nline two",
			search:  "line one\\nline two",
			want:    "line one\nline two",
			ok:      true,
		},
		{
			name:    "no match at all",
			content: "hello world",
			search:  "missing",
			want:    "",
			ok:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findActualString(tt.content, tt.search)
			if ok != tt.ok {
				t.Fatalf("findActualString ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("findActualString got %q, want %q", got, tt.want)
			}
		})
	}
}

// --- unescapeC ---

func TestUnescapeC(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		changed bool
	}{
		{"no escapes", "no escapes", "no escapes", false},
		{"tab", "\\t", "\t", true},
		{"newline", "\\n", "\n", true},
		{"carriage return", "\\r", "\r", true},
		{"quote", "\\\"", "\"", true},
		{"backslash", "\\\\", "\\", true},
		{"mixed", "foo\\tbar\\n", "foo\tbar\n", true},
		{"unrecognized escape preserved", "foo\\xbar", "foo\\xbar", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := unescapeC(tt.in)
			if changed != tt.changed {
				t.Errorf("unescapeC(%q) changed = %v, want %v", tt.in, changed, tt.changed)
			}
			if got != tt.want {
				t.Errorf("unescapeC(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// --- handleFileEdit integration: tabs vs spaces ---

func TestHandleFileEdit_TabsInContentSpacesInSearch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tabs.go")
	content := "package main\n\nfunc main() {\n\t\tcase \"type\":\n\t\t\treturn nil\n\t}\n}\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileEdit(ctx, domain.FileSystemEditReq{
		Path:      "tabs.go",
		OldString: "    case \"type\":\n        return nil",
		NewString: "    case \"int\":\n        return 42",
	})
	if err != nil {
		t.Fatalf("edit should succeed with whitespace-tolerant matching: %v", err)
	}
	if resp.Replacements != 1 {
		t.Errorf("expected 1 replacement, got %d", resp.Replacements)
	}

	data, _ := os.ReadFile(path)
	expected := "package main\n\nfunc main() {\n    case \"int\":\n        return 42\n\t}\n}\n"
	if string(data) != expected {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", string(data), expected)
	}
}

func TestHandleFileEdit_SpacesInContentTabsInSearch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "spaces.go")
	content := "func main() {\n    case \"type\":\n        return nil\n}\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	resp, err := a.handleFileEdit(ctx, domain.FileSystemEditReq{
		Path:      "spaces.go",
		OldString: "\t\tcase \"type\":\n\t\t\treturn nil",
		NewString: "\t\tcase \"int\":\n\t\t\treturn 42",
	})
	if err != nil {
		t.Fatalf("edit should succeed with whitespace-tolerant matching: %v", err)
	}
	if resp.Replacements != 1 {
		t.Errorf("expected 1 replacement, got %d", resp.Replacements)
	}

	data, _ := os.ReadFile(path)
	expected := "func main() {\n\t\tcase \"int\":\n\t\t\treturn 42\n}\n"
	if string(data) != expected {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", string(data), expected)
	}
}

func TestHandleFileEdit_ExactMatchStillPreferred(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "exact.txt")
	content := "func foo() {\n\tbar\n}\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	a, ctx := freshProject(t, root)
	_, err := a.handleFileEdit(ctx, domain.FileSystemEditReq{
		Path:      "exact.txt",
		OldString: "\tbar",
		NewString: "\tbaz",
	})
	if err != nil {
		t.Fatalf("exact match should work: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "func foo() {\n\tbaz\n}\n" {
		t.Errorf("unexpected content: %q", string(data))
	}
}
