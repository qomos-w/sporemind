package project

import (
	"reflect"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestCardToListItemCarriesData(t *testing.T) {
	item := cardToListItem(&CardRecord{Title: "card", Data: map[string]any{"executor": "agent:coder"}})
	if item.Data["executor"] != "agent:coder" {
		t.Fatalf("data = %#v", item.Data)
	}
}

// TestParseDataBlockScalarTypes verifies bare YAML booleans decode as Go
// bools while quoted scalars stay strings. The workflow view's template band
// and topology menu guards use strict boolean checks (data.template === true),
// so a disk reload that yields the string "true" makes templates reclassify
// out of the template band after an app restart.
func TestParseDataBlockScalarTypes(t *testing.T) {
	raw := "---\ndata:\n  template: true\n  instance_of: \"tpl::X\"\n  enabled: false\n  plain: hello\n  schedule:\n    cron: \"0 9 * * *\"\n    enabled: true\n---\n\nbody"
	card := decodeCard("tpl::X", raw)
	if card.Data["template"] != true {
		t.Errorf("template = %#v (%T), want bool true", card.Data["template"], card.Data["template"])
	}
	if card.Data["instance_of"] != "tpl::X" {
		t.Errorf("instance_of = %#v, want tpl::X", card.Data["instance_of"])
	}
	if card.Data["enabled"] != false {
		t.Errorf("enabled = %#v (%T), want bool false", card.Data["enabled"], card.Data["enabled"])
	}
	if card.Data["plain"] != "hello" {
		t.Errorf("plain = %#v, want hello", card.Data["plain"])
	}
	schedule, ok := card.Data["schedule"].(map[string]any)
	if !ok {
		t.Fatalf("schedule missing: %#v", card.Data["schedule"])
	}
	if schedule["enabled"] != true {
		t.Errorf("nested schedule.enabled = %#v (%T), want bool true", schedule["enabled"], schedule["enabled"])
	}

	// The list item must carry the bool through cardToListItem so the
	// metadata-only list path (post-restart) preserves the type.
	item := cardToListItem(&CardRecord{Title: "tpl::X", Data: card.Data})
	if item.Data["template"] != true {
		t.Errorf("list item template = %#v (%T), want bool true", item.Data["template"], item.Data["template"])
	}
}

var _ domain.MonoCardListItem = domain.MonoCardListItem{}

func TestParseScalarValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want any
	}{
		{"bare string", "hello", "hello"},
		{"single-quoted stays string", `'[a, b]'`, "[a, b]"},
		{"double-quoted stays string", `"[a, b]"`, "[a, b]"},
		{"bare true", "true", true},
		{"bare false", "false", false},
		{"flow array", "[a, b, c]", []any{"a", "b", "c"}},
		{"flow array with quoted items", `["open:todo", 'x:y']`, []any{"open:todo", "x:y"}},
		{"empty flow array", "[]", []any{}},
		{"flow array trims spaces", "[ a , b ]", []any{"a", "b"}},
		{"unbalanced bracket stays string", "[a, b", "[a, b"},
		{"bracket-only text stays string", "[", "["},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseScalarValue(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseScalarValue(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

// TestDecodeCardFlowInclude: hand-authored plan cards sometimes carry
// data.scope.include in flow style; it must decode as []any so
// workflowTaskIDs resolves children instead of silently dropping them.
func TestDecodeCardFlowInclude(t *testing.T) {
	raw := "---\nid: map-a\ntype: workflow\ndata:\n  scope:\n    include: [Task A, Task B]\n---\nbody\n"
	card := ParseCardRaw("map-a", raw)
	scope, ok := card.Data["scope"].(map[string]any)
	if !ok {
		t.Fatalf("data.scope = %#v, want map[string]any", card.Data["scope"])
	}
	inc, ok := scope["include"].([]any)
	if !ok {
		t.Fatalf("scope.include = %#v, want []any", scope["include"])
	}
	if !reflect.DeepEqual(inc, []any{"Task A", "Task B"}) {
		t.Fatalf("scope.include = %#v, want [Task A Task B]", inc)
	}
	ids := workflowTaskIDs(cardToListItem(card), nil)
	if !reflect.DeepEqual(ids, []string{"Task A", "Task B"}) {
		t.Fatalf("workflowTaskIDs = %#v, want [Task A Task B]", ids)
	}
}
