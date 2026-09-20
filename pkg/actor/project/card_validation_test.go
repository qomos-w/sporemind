package project

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/scriptcard"
)

func TestValidateBuiltinCardSchedulerCron(t *testing.T) {
	raw := "---\ntype: scheduler\ntags: [scheduler]\ndata:\n  executor: agent:test\n  schedule:\n    cron: \"0 9 * * *\"\n---\n\nTask"
	if err := validateBuiltinCard("scheduler:test", raw); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBuiltinCardRejectsInvalidCron(t *testing.T) {
	raw := "---\ntype: scheduler\ntags: [scheduler]\ndata:\n  executor: agent:test\n  schedule:\n    cron: \"0 9 *\"\n---\n\nTask"
	if err := validateBuiltinCard("scheduler:test", raw); err == nil {
		t.Fatal("expected invalid cron error")
	}
}

func TestValidateCardRejectsUnknownType(t *testing.T) {
	raw := "---\nid: my-card\ntype: mystery\ntags: []\n---\n\nBody."
	if err := validateCard("my-card", raw); err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestValidateCardAcceptsCanonicalAndLegacyTypes(t *testing.T) {
	known := knownStructForTest(t)
	cases := map[string]string{
		"agent":             "---\nid: agent:c-agent\ntype: agent\ntags: [agent]\n---\n\nBody.",
		"wiki":              "---\nid: c-wiki\ntype: wiki\ntags: []\n---\n\nBody.",
		"task":              "---\nid: c-task\ntype: task\ntags: []\nstatus: todo\n---\n\nBody.",
		"scheduler":         "---\nid: c-scheduler\ntype: scheduler\ntags: []\ndata:\n  executor: agent:test\n  schedule:\n    cron: \"0 9 * * *\"\n---\n\nBody.",
		"concept":           "---\nid: c-concept\ntype: concept\ntags: []\n---\n\nBody.",
		"prompt":            "---\nid: c-prompt\ntype: prompt\ntags: []\n---\n\nBody.",
		"skill":             "---\nid: c-skill\ntype: skill\ntags: []\n---\n\nBody.",
		"callable":          "---\nid: c-callable\ntype: callable\ntags: []\ndata:\n  callable: my.callable\n---\n\nBody.",
		"capability_module": "---\nid: c-capability_module\ntype: capability_module\ntags: []\ndata:\n  source: src\n---\n\nBody.",
		"crawl":             "---\nid: c-crawl\ntype: crawl\ntags: []\ndata:\n  seeds:\n    - https://example.com\n  max_depth: 2\n  max_pages: 100\n  extract:\n    schema: " + known + "\n  mode: hidden_window\n---\n\nBody.",
		"workflow":          "---\nid: c-workflow\ntype: workflow\ntags: []\n---\n\nBody.",
		"bundle":            "---\nid: c-bundle\ntype: bundle\ntags: []\n---\n\nBody.",
	}
	for tp, raw := range cases {
		if err := validateCard("c-"+tp, raw); err != nil {
			t.Errorf("canonical type %q rejected: %v", tp, err)
		}
	}
	// Legacy types are normalized and accepted.
	legacyCases := map[string]string{
		"note":        "---\nid: legacy-note\ntype: note\ntags: []\n---\n\nBody.",
		"comment":     "---\nid: legacy-comment\ntype: comment\ntags: []\n---\n\nBody.",
		"knowledge":   "---\nid: legacy-knowledge\ntype: knowledge\ntags: []\n---\n\nBody.",
		"reminder":    "---\nid: legacy-reminder\ntype: reminder\ntags: []\ndata:\n  executor: agent:test\n---\n\nBody.",
		"plan":        "---\nid: legacy-plan\ntype: plan\ntags: []\n---\n\nBody.",
		"goal":        "---\nid: legacy-goal\ntype: goal\ntags: []\n---\n\nBody.",
		"kanban-task": "---\nid: legacy-kanban-task\ntype: kanban-task\ntags: []\nstatus: todo\n---\n\nBody.",
	}
	for tp, raw := range legacyCases {
		if err := validateCard("legacy-"+tp, raw); err != nil {
			t.Errorf("legacy type %q rejected: %v", tp, err)
		}
	}
}

func TestValidateCardDefaultsEmptyTypeToWiki(t *testing.T) {
	raw := "---\nid: no-type\ntags: []\n---\n\nBody."
	if err := validateCard("no-type", raw); err != nil {
		t.Fatalf("empty type should default to wiki: %v", err)
	}
}

func TestValidateCardRejectsBuiltinTags(t *testing.T) {
	for _, tag := range []string{"builtin", "__builtin_skill__", "__builtin_todo__"} {
		raw := "---\nid: user-card\ntags: [" + tag + "]\n---\n\nBody."
		if err := validateCard("user-card", raw); err == nil {
			t.Errorf("expected error for forbidden tag %q", tag)
		}
	}
}

func TestValidateCardExemptsSystemCardsFromBuiltinTagRule(t *testing.T) {
	// Builtin virtual nodes and namespaced ids may carry reserved tags.
	raw := "---\nid: __builtin_skill__\ntags: [__builtin_skill__]\n---\n\nBody."
	if err := validateCard("__builtin_skill__", raw); err != nil {
		t.Errorf("system card rejected for builtin tag: %v", err)
	}
	if err := validateCard("agent:Architect", raw); err != nil {
		t.Errorf("namespaced system card rejected for builtin tag: %v", err)
	}
}

func TestValidateCardRejectsParentNotInTags(t *testing.T) {
	raw := "---\nid: c\ntags: [group]\nparent: other\n---\n\nBody."
	if err := validateCard("c", raw); err == nil {
		t.Fatal("expected error when parent is not in tags")
	}
}

func TestValidateCardAcceptsParentInTags(t *testing.T) {
	raw := "---\nid: c\ntags: [group, parent-card]\nparent: parent-card\n---\n\nBody."
	if err := validateCard("c", raw); err != nil {
		t.Fatalf("parent in tags should be accepted: %v", err)
	}
}

func TestEnsureCardMetaAutoFillsParentFromFirstTag(t *testing.T) {
	raw := "---\nid: c\ntype: task\ntags: [work, urgent]\n---\n\nBody."
	out := ensureCardMeta("c", raw, "2026-07-27T00:00:00Z")
	card := decodeCard("c", out)
	if card.Parent != "work" {
		t.Errorf("auto-filled parent = %q, want work", card.Parent)
	}
	// Auto-filled parent must satisfy parent∈tags.
	if !containsString(card.Tags, card.Parent) {
		t.Errorf("auto-filled parent %q not in tags %v", card.Parent, card.Tags)
	}
}

func TestEnsureCardMetaLeavesParentEmptyWithoutTags(t *testing.T) {
	raw := "---\nid: c\ntype: wiki\ntags: []\n---\n\nBody."
	out := ensureCardMeta("c", raw, "2026-07-27T00:00:00Z")
	if decodeCard("c", out).Parent != "" {
		t.Error("parent should remain empty when there are no tags")
	}
}

func TestValidateCardStructuredReturnsAllErrors(t *testing.T) {
	raw := "---\nid: user-card\ntype: mystery\ntags: [__builtin_skill__]\nparent: missing-parent\n---\n\nBody."
	errs := validateCardStructured("user-card", raw)
	if len(errs) != 3 {
		t.Fatalf("expected 3 errors, got %d: %+v", len(errs), errs)
	}
	want := []string{"invalid_type", "forbidden_tag", "parent_not_in_tags"}
	for i, code := range want {
		if errs[i].Code != code {
			t.Errorf("errs[%d].Code = %q, want %q", i, errs[i].Code, code)
		}
	}
}

func TestValidateCardStructuredMissingFrontmatter(t *testing.T) {
	errs := validateCardStructured("no-fm", "# No frontmatter\n\nBody.")
	if len(errs) != 1 || errs[0].Code != "missing_frontmatter" {
		t.Fatalf("expected missing_frontmatter error, got %+v", errs)
	}
}

func TestValidateCardStructuredValidCard(t *testing.T) {
	raw := "---\nid: user-card\ntype: task\ntags: [parent-card]\nparent: parent-card\n---\n\nBody."
	errs := validateCardStructured("user-card", raw)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %+v", errs)
	}
}

// TestValidateCardExecKind covers the data.exec.kind frontmatter gate.
// Cards may omit data.exec (default = worker_task at dispatch), declare
// worker_task explicitly, or be rejected when an unknown kind is named.
// crawl / sub_map are NOT in the allowlist yet — they are reserved for
// future kinds and must land here in lockstep with the workspace
// executor registry.
func TestValidateCardExecKind(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantErr  bool
		wantCode string
	}{
		{
			name:    "no data.exec block",
			raw:     "---\nid: t\ntype: task\ntags: []\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.exec absent, other data present",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  depends_on:\n    - upstream\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.exec.kind = worker_task",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  exec:\n    kind: worker_task\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.exec.kind empty",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  exec:\n    kind: \"\"\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.exec.kind = crawl (registered)",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  exec:\n    kind: crawl\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.exec.kind = sub_map (registered)",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  exec:\n    kind: sub_map\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.exec.kind = event_wait (registered)",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  exec:\n    kind: event_wait\n    event_kind: toast.card_added\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.exec.kind = gate (registered)",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  exec:\n    kind: gate\n    prompt: Approve the deploy?\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.exec.kind = bogus",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  exec:\n    kind: bogus\n---\n\nBody.",
			wantErr:  true,
			wantCode: "task_exec_kind_unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateCardStructured("t", tc.raw)
			if tc.wantErr {
				found := false
				for _, e := range errs {
					if e.Code == tc.wantCode {
						found = true
						if e.Field != "data.exec.kind" {
							t.Errorf("expected field data.exec.kind, got %q", e.Field)
						}
						break
					}
				}
				if !found {
					t.Fatalf("expected error code %q, got %+v", tc.wantCode, errs)
				}
				return
			}
			for _, e := range errs {
				if e.Code == "task_exec_kind_unknown" {
					t.Fatalf("did not expect exec kind error, got %+v", e)
				}
			}
		})
	}
}

// TestValidateCardIO covers the data.io frontmatter gate (turn-end forced
// toolcall contract). Cards may omit data.io entirely; when declared it must
// be a mapping with a non-empty string callable, an optional string
// description, and must not be combined with deterministic (non-agent) exec
// kinds, whose executors never run agent turns.
func TestValidateCardIO(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantErr  bool
		wantCode string
	}{
		{
			name:    "no data.io block",
			raw:     "---\nid: t\ntype: task\ntags: []\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "callable + description",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  io:\n    callable: app.emit_state\n    description: emit the turn summary\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "callable only",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  io:\n    callable: project.report\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "io with worker_task exec kind is fine",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  exec:\n    kind: worker_task\n  io:\n    callable: project.report\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "missing callable",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  io:\n    description: no callable\n---\n\nBody.",
			wantErr:  true,
			wantCode: "task_io_callable_missing",
		},
		{
			name: "blank callable",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  io:\n    callable: \"  \"\n---\n\nBody.",
			wantErr:  true,
			wantCode: "task_io_callable_missing",
		},
		{
			name: "io is a scalar",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  io: project.report\n---\n\nBody.",
			wantErr:  true,
			wantCode: "task_io_invalid",
		},
		{
			name: "description not a string",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  io:\n    callable: project.report\n    description: true\n---\n\nBody.",
			wantErr:  true,
			wantCode: "task_io_description_invalid",
		},
		{
			name: "io combined with crawl exec kind",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  exec:\n    kind: crawl\n  io:\n    callable: project.report\n---\n\nBody.",
			wantErr:  true,
			wantCode: "task_io_exec_conflict",
		},
		{
			name: "io combined with toolcall exec kind",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  exec:\n    kind: toolcall\n  io:\n    callable: project.report\n---\n\nBody.",
			wantErr:  true,
			wantCode: "task_io_exec_conflict",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateCardStructured("t", tc.raw)
			if tc.wantErr {
				found := false
				for _, e := range errs {
					if e.Code == tc.wantCode {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected error code %q, got %+v", tc.wantCode, errs)
				}
				return
			}
			for _, e := range errs {
				if strings.HasPrefix(e.Code, "task_io_") {
					t.Fatalf("did not expect io error, got %+v", e)
				}
			}
		})
	}
}

// TestValidateCardCategory covers the data.category frontmatter gate.
// Cards may omit data.category (backward compatible), declare any of the
// five canonical categories, or be rejected when an unknown category is named.
func TestValidateCardCategory(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantErr  bool
		wantCode string
	}{
		{
			name:    "no data.category",
			raw:     "---\nid: t\ntype: task\ntags: []\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.category absent, other data present",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  depends_on:\n    - upstream\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.category = research",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  category: research\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.category = explore",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  category: explore\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.category = execute",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  category: execute\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.category = code",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  category: code\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.category = review",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  category: review\n---\n\nBody.",
			wantErr: false,
		},
		{
			name: "data.category = bogus",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  category: bogus\n---\n\nBody.",
			wantErr:  true,
			wantCode: "task_category_unknown",
		},
		{
			name: "data.category empty",
			raw: "---\nid: t\ntype: task\ntags: []\n" +
				"data:\n  category: \"\"\n---\n\nBody.",
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateCardStructured("t", tc.raw)
			if tc.wantErr {
				found := false
				for _, e := range errs {
					if e.Code == tc.wantCode {
						found = true
						if e.Field != "data.category" {
							t.Errorf("expected field data.category, got %q", e.Field)
						}
						break
					}
				}
				if !found {
					t.Fatalf("expected error code %q, got %+v", tc.wantCode, errs)
				}
				return
			}
			for _, e := range errs {
				if e.Code == "task_category_unknown" {
					t.Fatalf("did not expect category error, got %+v", e)
				}
			}
		})
	}
}

func TestTaskValidatorRejectsInvalidStatusDueAndPriority(t *testing.T) {
	raw := "---\nid: user-card\ntype: task\ntags: []\nstatus: frozen\ndue: not-a-date\npriority: mega\n---\n\nBody."
	errs := validateCardStructured("user-card", raw)
	want := map[string]bool{
		"task_status_invalid":   true,
		"task_due_invalid":      true,
		"task_priority_invalid": true,
	}
	got := map[string]bool{}
	for _, e := range errs {
		got[e.Code] = true
	}
	for code := range want {
		if !got[code] {
			t.Errorf("expected error code %q, got %+v", code, errs)
		}
	}
}

func TestSchedulerValidatorRejectsMissingExecutorAndInvalidCron(t *testing.T) {
	raw := "---\nid: user-card\ntype: scheduler\ntags: []\ndata:\n  schedule:\n    cron: \"0 9 *\"\n---\n\nBody."
	errs := validateCardStructured("user-card", raw)
	want := map[string]bool{
		"scheduler_cron_invalid":     true,
		"scheduler_executor_missing": true,
	}
	got := map[string]bool{}
	for _, e := range errs {
		got[e.Code] = true
	}
	for code := range want {
		if !got[code] {
			t.Errorf("expected error code %q, got %+v", code, errs)
		}
	}
}

func TestSchedulerValidatorExpressionSemantics(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{name: "every minutes valid", expr: "every 30 minutes"},
		{name: "every singular hour valid", expr: "every 1 hour"},
		{name: "every days valid", expr: "every 2 days"},
		{name: "at future valid", expr: "at 2999-01-01 00:00:00"},
		{name: "zero interval rejected", expr: "every 0 minutes", wantErr: true},
		{name: "negative interval rejected", expr: "every -5 minutes", wantErr: true},
		{name: "unknown unit rejected", expr: "every frog", wantErr: true},
		{name: "unrecognised rejected", expr: "sometimes", wantErr: true},
		{name: "past at rejected", expr: "at 2020-01-01 00:00:00", wantErr: true},
		{name: "malformed at rejected", expr: "at tomorrow", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := "---\nid: user-card\ntype: scheduler\ntags: []\ndata:\n  executor: agent:coder\n  schedule:\n    expression: \"" + tc.expr + "\"\n---\n\nBody."
			got := errorCodes(validateCardStructured("user-card", raw))
			if tc.wantErr && !got["scheduler_expression_invalid"] {
				t.Fatalf("expected scheduler_expression_invalid for %q, got %v", tc.expr, got)
			}
			if !tc.wantErr && got["scheduler_expression_invalid"] {
				t.Fatalf("did not expect scheduler_expression_invalid for %q", tc.expr)
			}
		})
	}
}

func TestCallableValidatorRequiresCallableID(t *testing.T) {
	raw := "---\nid: user-card\ntype: callable\ntags: []\n---\n\nBody."
	errs := validateCardStructured("user-card", raw)
	if len(errs) != 1 || errs[0].Code != "callable_id_missing" {
		t.Fatalf("expected callable_id_missing, got %+v", errs)
	}
}

func TestCapabilityModuleValidatorRequiresSource(t *testing.T) {
	raw := "---\nid: user-card\ntype: capability_module\ntags: []\n---\n\nBody."
	errs := validateCardStructured("user-card", raw)
	if len(errs) != 1 || errs[0].Code != "capability_module_source_missing" {
		t.Fatalf("expected capability_module_source_missing, got %+v", errs)
	}
}

func TestTaskRepairerNormalizesStatusAndPriority(t *testing.T) {
	cases := []struct {
		status, priority string
		wantStatus       string
		wantPriority     string
	}{
		{"in_progress", "critical", "doing", "urgent"},
		{"in gress", "asap", "doing", "urgent"},
		{"complete", "hi", "done", "high"},
		{"to-do", "normal", "todo", "medium"},
		{"back_log", "low", "backlog", "low"},
		{"approved", "medium", "todo", "medium"},
		{"rejected", "low", "cancelled", "low"},
		{"canceled", "low", "cancelled", "low"},
		{"completed", "high", "done", "high"},
	}
	for _, c := range cases {
		raw := "---\nid: user-card\ntype: task\ntags: []\nstatus: " + c.status + "\npriority: " + c.priority + "\n---\n\nBody."
		errs := validateCardStructured("user-card", raw)
		if len(errs) != 0 {
			t.Errorf("status=%q priority=%q: expected repaired values to be valid, got %+v", c.status, c.priority, errs)
		}
	}
}

func TestTaskRepairerDoesNotTouchUnknownValues(t *testing.T) {
	raw := "---\nid: user-card\ntype: task\ntags: []\nstatus: frozen\npriority: mega\n---\n\nBody."
	errs := validateCardStructured("user-card", raw)
	got := map[string]bool{}
	for _, e := range errs {
		got[e.Code] = true
	}
	if !got["task_status_invalid"] || !got["task_priority_invalid"] {
		t.Fatalf("expected unknown status/priority to remain invalid, got %+v", errs)
	}
}

func TestNormalizeCardStatusInRaw(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{
			"---\nid: T\ntype: task\ntags: []\nstatus: in_progress\n---\n\nBody.",
			"---\nid: T\ntype: task\ntags: []\nstatus: doing\n---\n\nBody.",
		},
		{
			"---\nid: T\ntype: task\ntags: []\nstatus: completed\n---\n\nBody.",
			"---\nid: T\ntype: task\ntags: []\nstatus: done\n---\n\nBody.",
		},
		{
			"---\nid: T\ntype: task\ntags: []\nstatus: doing\n---\n\nBody.",
			"---\nid: T\ntype: task\ntags: []\nstatus: doing\n---\n\nBody.",
		},
		{
			"---\nid: T\ntype: wiki\ntags: []\n---\n\nBody.",
			"---\nid: T\ntype: wiki\ntags: []\n---\n\nBody.",
		},
		{
			"id: T\nstatus: in_progress\n",
			"id: T\nstatus: in_progress\n",
		},
	}
	for _, c := range cases {
		got := normalizeCardStatusInRaw(c.input)
		if got != c.want {
			t.Errorf("normalizeCardStatusInRaw(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// crawlCardRaw builds a raw crawl card for tests. Extra data lines are appended
// after the fixed required fields.
func crawlCardRaw(t *testing.T, extra string) string {
	t.Helper()
	known := knownStructForTest(t)
	return "---\nid: crawl-test\ntype: crawl\ntags: []\ndata:\n" +
		"  seeds:\n    - https://example.com\n" +
		"  max_depth: 2\n" +
		"  max_pages: 100\n" +
		"  extract:\n    schema: " + known + "\n" +
		"  mode: hidden_window\n" +
		extra +
		"---\n\nBody."
}

func TestValidateCrawlCardAcceptsValidDefinition(t *testing.T) {
	raw := crawlCardRaw(t, "")
	if err := validateCard("crawl-test", raw); err != nil {
		t.Fatalf("valid crawl card rejected: %v", err)
	}
}

func TestValidateCrawlCardAcceptsOptionalFields(t *testing.T) {
	raw := crawlCardRaw(t, "  same_domain: true\n  rate_limit: 500ms\n  profile: default\n")
	if err := validateCard("crawl-test", raw); err != nil {
		t.Fatalf("crawl card with optional fields rejected: %v", err)
	}
}

func TestValidateCrawlCardRejectsMissingRequiredFields(t *testing.T) {
	base := "---\nid: crawl-test\ntype: crawl\ntags: []\ndata:\n---\n\nBody."
	err := validateCard("crawl-test", base)
	if err == nil {
		t.Fatal("expected errors for missing required fields")
	}
	want := map[string]bool{
		"crawl_seeds_missing":          true,
		"crawl_max_depth_missing":      true,
		"crawl_max_pages_missing":      true,
		"crawl_extract_schema_missing": true,
		"crawl_mode_missing":           true,
	}
	got := errorCodes(validateCardStructured("crawl-test", base))
	for code := range want {
		if !got[code] {
			t.Errorf("expected error code %q, got %+v", code, got)
		}
	}
}

func TestValidateCrawlCardRejectsEmptySeeds(t *testing.T) {
	raw := crawlCardRaw(t, "  seeds: []\n")
	errs := validateCardStructured("crawl-test", raw)
	if !errorCodes(errs)["crawl_seeds_empty"] {
		t.Fatalf("expected crawl_seeds_empty, got %+v", errs)
	}
}

func TestValidateCrawlCardRejectsInvalidBounds(t *testing.T) {
	cases := []struct {
		extra, want string
	}{
		{"  max_depth: -1\n", "crawl_max_depth_invalid"},
		{"  max_pages: 0\n", "crawl_max_pages_invalid"},
	}
	for _, c := range cases {
		raw := crawlCardRaw(t, c.extra)
		errs := validateCardStructured("crawl-test", raw)
		if !errorCodes(errs)[c.want] {
			t.Errorf("extra %q: expected %q, got %+v", c.extra, c.want, errs)
		}
	}
}

func TestValidateCrawlCardRejectsInvalidMode(t *testing.T) {
	raw := crawlCardRaw(t, "  mode: stealth\n")
	errs := validateCardStructured("crawl-test", raw)
	if !errorCodes(errs)["crawl_mode_invalid"] {
		t.Fatalf("expected crawl_mode_invalid, got %+v", errs)
	}
}

func TestValidateCrawlCardRejectsUnknownExtractSchema(t *testing.T) {
	raw := crawlCardRaw(t, "  extract:\n    schema: NoSuchStruct\n")
	errs := validateCardStructured("crawl-test", raw)
	if !errorCodes(errs)["crawl_extract_schema_unknown"] {
		t.Fatalf("expected crawl_extract_schema_unknown, got %+v", errs)
	}
}

func TestValidateCrawlCardRejectsInvalidRateLimit(t *testing.T) {
	for _, extra := range []string{"  rate_limit: not-a-duration\n", "  rate_limit: -1s\n"} {
		raw := crawlCardRaw(t, extra)
		errs := validateCardStructured("crawl-test", raw)
		if !errorCodes(errs)["crawl_rate_limit_invalid"] {
			t.Errorf("extra %q: expected crawl_rate_limit_invalid, got %+v", extra, errs)
		}
	}
}

func TestValidateCrawlCardRejectsOrchestrationFields(t *testing.T) {
	cases := []struct {
		name, extra string
	}{
		{"data.depends_on", "  depends_on:\n    - other\n"},
		{"data.workflow", "  workflow: map\n"},
		{"data.step", "  step: 1\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := crawlCardRaw(t, c.extra)
			errs := validateCardStructured("crawl-test", raw)
			if !errorCodes(errs)["crawl_orchestration_field_forbidden"] {
				t.Fatalf("expected crawl_orchestration_field_forbidden, got %+v", errs)
			}
		})
	}
}

func TestValidateCrawlCardRejectsTopLevelOrchestrationFields(t *testing.T) {
	known := knownStructForTest(t)
	for _, key := range []string{"depends_on", "workflow", "step"} {
		raw := "---\nid: crawl-test\ntype: crawl\ntags: []\n" + key + ":\n  - other\ndata:\n" +
			"  seeds:\n    - https://example.com\n" +
			"  max_depth: 2\n" +
			"  max_pages: 100\n" +
			"  extract:\n    schema: " + known + "\n" +
			"  mode: hidden_window\n" +
			"---\n\nBody."
		errs := validateCardStructured("crawl-test", raw)
		if !errorCodes(errs)["crawl_orchestration_field_forbidden"] {
			t.Errorf("top-level %q: expected crawl_orchestration_field_forbidden, got %+v", key, errs)
		}
	}
}

func TestSchedulerValidatorScheduleType(t *testing.T) {
	cases := []struct {
		name     string
		data     string
		wantCode string
		wantErr  bool
	}{
		{name: "explicit workflow with template valid", data: "  schedule_type: workflow\n  workflow_template: tpl::a\n", wantErr: false},
		{name: "explicit prompt valid", data: "  schedule_type: prompt\n", wantErr: false},
		{name: "prompt with stale workflow_template valid", data: "  schedule_type: prompt\n  workflow_template: tpl::stale\n", wantErr: false},
		{name: "explicit task with template valid", data: "  schedule_type: task\n  workflow_template: tpl::a\n", wantErr: false},
		{name: "explicit task without template valid", data: "  schedule_type: task\n", wantErr: false},
		{name: "empty schedule_type with template valid (legacy workflow)", data: "  workflow_template: tpl::a\n", wantErr: false},
		{name: "empty schedule_type without template valid (legacy prompt)", data: "", wantErr: false},
		{name: "invalid schedule_type rejected", data: "  schedule_type: bogus\n", wantCode: "scheduler_schedule_type_invalid", wantErr: true},
		{name: "workflow without template rejected", data: "  schedule_type: workflow\n", wantCode: "scheduler_workflow_template_missing", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := "---\nid: user-card\ntype: scheduler\ntags: []\ndata:\n" +
				tc.data +
				"  executor: agent:coder\n" +
				"---\n\nBody."
			errs := validateCardStructured("user-card", raw)
			got := errorCodes(errs)
			if !tc.wantErr {
				if got["scheduler_schedule_type_invalid"] || got["scheduler_workflow_template_missing"] {
					t.Fatalf("did not expect schedule_type error, got %+v", errs)
				}
				return
			}
			if !got[tc.wantCode] {
				t.Fatalf("expected error code %q, got %+v", tc.wantCode, errs)
			}
		})
	}
}

// TestValidateCardScript covers the data.exec.kind = script static
// gate. The validator only checks shape: capabilities must be a
// non-empty list of dotted callable IDs; inputs (optional) must be a
// string→string map; outputs (optional) must be a single string;
// budget (optional) bounds timeout_sec to [1, 300] and max_host_calls
// to [1, 64]; gate_card (optional) must be a single string. The card
// body must contain at least one ```spore fenced block. data.io is
// rejected when combined with kind = script (the script executor
// never runs agent turns, mirroring the existing crawl / toolcall
// conflict).
func TestValidateCardScript(t *testing.T) {
	// buildScriptCard returns a task card with the script kind set and
	// the body carrying a canonical ```spore fenced block. Tests
	// override fields by replacing the marker lines.
	buildScriptCard := func(execBody, bodyLines string) string {
		execBlock := "data:\n  exec:\n    kind: script\n" + execBody
		body := "Body.\n\n```spore\nexport fun run(inputs: any): any { return inputs }\n```\n"
		if bodyLines != "" {
			body = bodyLines
		}
		return "---\nid: script-test\ntype: task\ntags: []\n" +
			execBlock + "---\n\n" + body
	}

	capabilities := func(bullets string) string {
		return "    capabilities:\n" + bullets
	}

	t.Run("missing capabilities", func(t *testing.T) {
		raw := buildScriptCard("", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_capabilities_missing"] {
			t.Fatalf("expected task_exec_script_capabilities_missing, got %+v", errs)
		}
	})

	t.Run("capabilities present but not a list", func(t *testing.T) {
		raw := buildScriptCard("    capabilities: app.emit\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_capabilities_invalid"] {
			t.Fatalf("expected task_exec_script_capabilities_invalid, got %+v", errs)
		}
	})

	t.Run("capabilities empty list", func(t *testing.T) {
		raw := buildScriptCard(capabilities(""), "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_capabilities_empty"] {
			t.Fatalf("expected task_exec_script_capabilities_empty, got %+v", errs)
		}
	})

	t.Run("capabilities with illegal ID", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - bare\n"), "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_capability_invalid"] {
			t.Fatalf("expected task_exec_script_capability_invalid, got %+v", errs)
		}
	})

	t.Run("capabilities with empty segment", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app..emit\n"), "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_capability_invalid"] {
			t.Fatalf("expected task_exec_script_capability_invalid, got %+v", errs)
		}
	})

	t.Run("capabilities with whitespace", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - \"app .emit\"\n"), "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_capability_invalid"] {
			t.Fatalf("expected task_exec_script_capability_invalid, got %+v", errs)
		}
	})

	t.Run("capabilities with leading dot", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - \".app.emit\"\n"), "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_capability_invalid"] {
			t.Fatalf("expected task_exec_script_capability_invalid, got %+v", errs)
		}
	})

	t.Run("capabilities valid block list", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app.emit_state\n      - project.report\n"), "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("did not expect any script error, got %+v", errs)
		}
	})

	t.Run("capabilities valid flow array", func(t *testing.T) {
		raw := buildScriptCard("    capabilities: [app.emit_state, project.report]\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("did not expect any script error, got %+v", errs)
		}
	})

	t.Run("capabilities three segments valid", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - workflow.gate.approve\n"), "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("did not expect any script error, got %+v", errs)
		}
	})

	t.Run("budget max_duration_sec zero uses default", func(t *testing.T) {
		// Per the canonical contract, 0 is "fall back to default" —
		// not "below minimum" — so the validator does not flag it.
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      max_duration_sec: 0\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("max_duration_sec=0 should be accepted (uses default), got %+v", errs)
		}
	})

	t.Run("budget max_duration_sec over max", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      max_duration_sec: 301\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_budget_invalid"] {
			t.Fatalf("expected task_exec_script_budget_invalid, got %+v", errs)
		}
	})

	t.Run("budget max_duration_sec negative", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      max_duration_sec: -1\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_budget_invalid"] {
			t.Fatalf("expected task_exec_script_budget_invalid, got %+v", errs)
		}
	})

	t.Run("budget max_duration_sec non-integer", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      max_duration_sec: \"thirty\"\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_budget_invalid"] {
			t.Fatalf("expected task_exec_script_budget_invalid, got %+v", errs)
		}
	})

	t.Run("budget max_duration_sec valid boundary 1 and 300", func(t *testing.T) {
		for _, n := range []int{1, 300} {
			raw := buildScriptCard(
				capabilities("      - app.emit\n")+fmt.Sprintf("    budget:\n      max_duration_sec: %d\n", n), "")
			errs := validateCardStructured("script-test", raw)
			if hasScriptError(errs) {
				t.Errorf("max_duration_sec=%d should be valid, got %+v", n, errs)
			}
		}
	})

	t.Run("budget max_host_calls zero disabled", func(t *testing.T) {
		// 0 means "limit disabled" per the canonical contract.
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      max_host_calls: 0\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("max_host_calls=0 should be accepted (disabled), got %+v", errs)
		}
	})

	t.Run("budget max_host_calls over max", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      max_host_calls: 65\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_budget_invalid"] {
			t.Fatalf("expected task_exec_script_budget_invalid, got %+v", errs)
		}
	})

	t.Run("budget max_host_calls valid boundary 1 and 64", func(t *testing.T) {
		for _, n := range []int{1, 64} {
			raw := buildScriptCard(
				capabilities("      - app.emit\n")+fmt.Sprintf("    budget:\n      max_host_calls: %d\n", n), "")
			errs := validateCardStructured("script-test", raw)
			if hasScriptError(errs) {
				t.Errorf("max_host_calls=%d should be valid, got %+v", n, errs)
			}
		}
	})

	t.Run("budget max_instructions valid", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      max_instructions: 4096\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("max_instructions=4096 should be accepted, got %+v", errs)
		}
	})

	t.Run("budget max_instructions zero disabled", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      max_instructions: 0\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("max_instructions=0 should be accepted (disabled), got %+v", errs)
		}
	})

	t.Run("budget max_instructions below min", func(t *testing.T) {
		// Below-min means strictly less than MinMaxInstructions (1);
		// -1 is a non-zero value below the floor.
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      max_instructions: -1\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_budget_invalid"] {
			t.Fatalf("expected task_exec_script_budget_invalid, got %+v", errs)
		}
	})

	t.Run("budget max_output_bytes valid", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      max_output_bytes: 65536\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("max_output_bytes=65536 should be accepted, got %+v", errs)
		}
	})

	t.Run("budget max_output_bytes below min", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      max_output_bytes: -1\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_budget_invalid"] {
			t.Fatalf("expected task_exec_script_budget_invalid, got %+v", errs)
		}
	})

	t.Run("budget legacy timeout_sec silently ignored", func(t *testing.T) {
		// The deprecated timeout_sec key is accepted wide — the canonical
		// contract uses max_duration_sec, but existing cards that still
		// author timeout_sec (which scriptcard.ParseConfig treats as an
		// unknown key) must not be rejected by the static layer.
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget:\n      timeout_sec: 30\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("legacy timeout_sec should be ignored, got %+v", errs)
		}
	})

	t.Run("budget both canonical fields valid", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    budget:\n      max_duration_sec: 30\n      max_host_calls: 8\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("did not expect any script error, got %+v", errs)
		}
	})

	t.Run("budget all four fields valid", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    budget:\n      max_duration_sec: 30\n      max_host_calls: 8\n      max_instructions: 4096\n      max_output_bytes: 65536\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("did not expect any script error, got %+v", errs)
		}
	})

	t.Run("budget is scalar rejected", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    budget: 30\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_budget_invalid"] {
			t.Fatalf("expected task_exec_script_budget_invalid, got %+v", errs)
		}
	})

	t.Run("inputs scalar rejected", func(t *testing.T) {
		// inputs must be a list of strings; a bare scalar is rejected
		// (a single string is not a positional sequence).
		raw := buildScriptCard(capabilities("      - app.emit\n")+"    inputs: \"not a list\"\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_inputs_invalid"] {
			t.Fatalf("expected task_exec_script_inputs_invalid, got %+v", errs)
		}
	})

	t.Run("inputs map shape rejected", func(t *testing.T) {
		// The legacy shape `{name: type}` is no longer valid; scripts
		// bind positional parameters by name and the spore VM does
		// not consult a spore type table for them.
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    inputs:\n      repo: string\n      branch: string\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_inputs_invalid"] {
			t.Fatalf("expected task_exec_script_inputs_invalid for map-shaped inputs, got %+v", errs)
		}
	})

	t.Run("inputs list accepted", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    inputs:\n      - repo\n      - branch\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("did not expect any script error, got %+v", errs)
		}
	})

	t.Run("inputs flow array accepted", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    inputs: [repo, branch]\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("did not expect any script error, got %+v", errs)
		}
	})

	t.Run("inputs duplicate entry rejected", func(t *testing.T) {
		// The canonical rule: positional inputs must be unique; a
		// duplicate name double-binds a single parameter and is
		// rejected at parse time.
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    inputs:\n      - repo\n      - branch\n      - repo\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_inputs_invalid"] {
			t.Fatalf("expected task_exec_script_inputs_invalid for duplicate entry, got %+v", errs)
		}
	})

	t.Run("inputs duplicate entry in flow array rejected", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    inputs: [repo, branch, repo]\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_inputs_invalid"] {
			t.Fatalf("expected task_exec_script_inputs_invalid for duplicate flow array entry, got %+v", errs)
		}
	})

	t.Run("outputs valid single string", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    outputs: project.report\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("did not expect any script error, got %+v", errs)
		}
	})

	t.Run("outputs empty rejected", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    outputs: \"\"\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_outputs_empty"] {
			t.Fatalf("expected task_exec_script_outputs_empty, got %+v", errs)
		}
	})

	t.Run("outputs non-string rejected", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    outputs: [a, b]\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_outputs_invalid"] {
			t.Fatalf("expected task_exec_script_outputs_invalid, got %+v", errs)
		}
	})

	t.Run("gate_card valid", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    gate_card: gate-card-id\n", "")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("did not expect any script error, got %+v", errs)
		}
	})

	t.Run("gate_card empty rejected", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    gate_card: \"\"\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_gate_card_empty"] {
			t.Fatalf("expected task_exec_script_gate_card_empty, got %+v", errs)
		}
	})

	t.Run("gate_card non-string rejected", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n")+"    gate_card: [a, b]\n", "")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_gate_card_invalid"] {
			t.Fatalf("expected task_exec_script_gate_card_invalid, got %+v", errs)
		}
	})

	t.Run("data.io combined with script kind rejected", func(t *testing.T) {
		raw := "---\nid: script-test\ntype: task\ntags: []\n" +
			"data:\n  exec:\n    kind: script\n    capabilities:\n      - app.emit\n" +
			"  io:\n    callable: project.report\n" +
			"---\n\nBody.\n\n```spore\nexport fun run(inputs: any): any { return inputs }\n```\n"
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_io_exec_conflict"] {
			t.Fatalf("expected task_io_exec_conflict, got %+v", errs)
		}
	})

	t.Run("body missing spore fence rejected", func(t *testing.T) {
		raw := buildScriptCard(capabilities("      - app.emit\n"), "Just a regular body.\n")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_body_missing_fence"] {
			t.Fatalf("expected task_exec_script_body_missing_fence, got %+v", errs)
		}
	})

	t.Run("body with non-spore fence rejected", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\n\n```python\nprint('hi')\n```\n")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_body_missing_fence"] {
			t.Fatalf("expected task_exec_script_body_missing_fence, got %+v", errs)
		}
	})

	t.Run("body with longer fence rejected", func(t *testing.T) {
		// With the canonical rule the opener and closer must be the
		// same length; a 5-backtick opener followed by a 3-backtick
		// closer (or vice-versa) does NOT close the block.
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\n\n`````spore\nexport fun run(): int { return 0 }\n```\n")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_body_missing_fence"] {
			t.Fatalf("expected task_exec_script_body_missing_fence for mismatched fence length, got %+v", errs)
		}
	})

	t.Run("body with longer fence matched length accepted", func(t *testing.T) {
		// The canonical rule is strict length pairing — a 5-backtick
		// opener with a 5-backtick closer is a valid block.
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\n\n`````spore\nexport fun run(): int { return 0 }\n`````\n")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("matched-length longer fence should be accepted, got %+v", errs)
		}
	})

	t.Run("body with spore fence preceded by leading whitespace accepted", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\n\n   ```spore\nexport fun run(): int { return 0 }\n```\n")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("indented spore fence should be accepted, got %+v", errs)
		}
	})

	t.Run("body with mixed-case Spore fence accepted", func(t *testing.T) {
		// Canonical rule is case-insensitive: ```Spore / ```SPORE all match.
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\n\n```Spore\nexport fun run(): int { return 0 }\n```\n")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("mixed-case spore fence should be accepted, got %+v", errs)
		}
	})

	t.Run("body with extra info token after spore accepted", func(t *testing.T) {
		// Canonical rule: the first whitespace-separated token of the
		// info string must EqualFold "spore" — extra metadata like
		// "spore title=..." is allowed. ```spore1 (single token) is NOT.
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\n\n```spore title=\"draft\"\nexport fun run(): int { return 0 }\n```\n")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("extra info token after spore should be accepted, got %+v", errs)
		}
	})

	t.Run("body with sporescript fence rejected", func(t *testing.T) {
		// First info token "sporescript" is not EqualFold "spore".
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\n\n```sporescript\nexport fun run(): int { return 0 }\n```\n")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_body_missing_fence"] {
			t.Fatalf("expected task_exec_script_body_missing_fence for sporescript fence, got %+v", errs)
		}
	})

	t.Run("body with spore1 fence rejected", func(t *testing.T) {
		// First info token "spore1" is not EqualFold "spore" — the
		// legacy ```spore1 rule that the validator previously accepted
		// is no longer valid.
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\n\n```spore1\nexport fun run(): int { return 0 }\n```\n")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_body_missing_fence"] {
			t.Fatalf("expected task_exec_script_body_missing_fence for spore1 fence, got %+v", errs)
		}
	})

	t.Run("body with CRLF line endings normalized", func(t *testing.T) {
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\r\n\r\n```spore\r\nexport fun run(): int { return 0 }\r\n```\r\n")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("CRLF-normalized spore fence should be accepted, got %+v", errs)
		}
	})

	t.Run("fully valid script card accepted", func(t *testing.T) {
		raw := "---\nid: script-test\ntype: task\ntags: []\n" +
			"data:\n  exec:\n    kind: script\n" +
			"    capabilities:\n      - app.emit_state\n      - project.report\n" +
			"    inputs:\n      - repo\n      - branch\n" +
			"    outputs: project.report\n" +
			"    budget:\n      max_duration_sec: 30\n      max_host_calls: 8\n" +
			"    gate_card: gate-card-id\n" +
			"---\n\nBody.\n\n```spore\nexport fun run(inputs: any): any { return inputs }\n```\n"
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("fully valid script card rejected, got %+v", errs)
		}
	})

	t.Run("script importing host.invoke compiles clean", func(t *testing.T) {
		// The compile probe binds a no-op host.invoke so a script
		// that imports `invoke` from "host" links without ever
		// executing. A clean result here proves the probe binds the
		// same surface the executor does.
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\n\n```spore\nimport { invoke } from \"host\"\n\nexport fun run(): any {\n\treturn invoke(\"app.emit\", {})\n}\n```\n")
		errs := validateCardStructured("script-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("compilable script with host.invoke rejected, got %+v", errs)
		}
	})

	t.Run("script syntax error rejected at save", func(t *testing.T) {
		// A body that fails the spore parser must surface as a
		// compile failure here, not at dispatch-time preflight. The
		// unterminated function body is unambiguously a parse error.
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\n\n```spore\nexport fun run(): int { return 0\n```\n")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_compile_failed"] {
			t.Fatalf("expected task_exec_script_compile_failed, got %+v", errs)
		}
	})

	t.Run("empty spore fence rejected at save", func(t *testing.T) {
		// A recognised fence whose body is empty extracts to "" and
		// the VM rejects an empty module source — the card author
		// sees the same failure at save instead of claim time.
		raw := buildScriptCard(
			capabilities("      - app.emit\n"),
			"Body.\n\n```spore\n\n```\n")
		errs := validateCardStructured("script-test", raw)
		if !errorCodes(errs)["task_exec_script_compile_failed"] {
			t.Fatalf("expected task_exec_script_compile_failed for empty source, got %+v", errs)
		}
	})

	t.Run("non-script kinds are unaffected", func(t *testing.T) {
		// A worker_task card with no capabilities / body fence must
		// continue to validate cleanly — the script validator only
		// fires when data.exec.kind = script.
		raw := "---\nid: task-test\ntype: task\ntags: []\nstatus: todo\n" +
			"data:\n  exec:\n    kind: worker_task\n" +
			"---\n\nJust a regular body.\n"
		errs := validateCardStructured("task-test", raw)
		if hasScriptError(errs) {
			t.Fatalf("script validator fired on non-script kind, got %+v", errs)
		}
	})

	t.Run("multiple errors reported together", func(t *testing.T) {
		raw := "---\nid: script-test\ntype: task\ntags: []\n" +
			"data:\n  exec:\n    kind: script\n" +
			"    capabilities: []\n" +
			"    budget:\n      max_duration_sec: 999\n" +
			"    outputs: \"\"\n" +
			"---\n\nJust text.\n"
		errs := validateCardStructured("script-test", raw)
		got := errorCodes(errs)
		for _, code := range []string{
			"task_exec_script_capabilities_empty",
			"task_exec_script_budget_invalid",
			"task_exec_script_outputs_empty",
			"task_exec_script_body_missing_fence",
		} {
			if !got[code] {
				t.Errorf("expected error code %q, got %+v", code, errs)
			}
		}
	})
}

// hasScriptError reports whether errs contains any code in the
// task_exec_script_* family. Used by sub-tests that exercise only
// shape-level rejection and must not be sensitive to incidental
// failures in unrelated fields.
func hasScriptError(errs []gen.CardValidationError) bool {
	for _, e := range errs {
		if strings.HasPrefix(e.Code, "task_exec_script_") {
			return true
		}
	}
	return false
}

// TestIsDottedCallableID locks the static-layer shape check for
// capability IDs. Existence in the topology registry is resolved at
// runtime; this is only the syntactic gate.
func TestIsDottedCallableID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// accepted
		{"app.emit", true},
		{"app.emit_state", true},
		{"project.report", true},
		{"workflow.gate.approve", true},
		{"a.b", true},
		{"mcp.foo.bar", true},
		{"x9.y_0", true},
		{"foo-bar.baz_qux", true},
		// rejected
		{"", false},
		{"   ", false},
		{"bare", false},
		{".app.emit", false},
		{"app.emit.", false},
		{"app..emit", false},
		{"app .emit", false},
		{"app.emit\n", false},
		{"app\temit", false},
		{"1app.emit", false},   // segment must start with a letter
		{"app.1emit", false},   // segment must start with a letter
		{"app.em!t", false},    // unsupported character
		{"a", false},           // single segment
		{".", false},           // lone dot
	}
	for _, c := range cases {
		got := isDottedCallableID(c.in)
		if got != c.want {
			t.Errorf("isDottedCallableID(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestScriptFenceReusesScriptcard pins the validator's fence
// existence check to the canonical scriptcard.ExtractSporeBlock
// function. The validator no longer owns a private bodyContainsSporeFence
// helper; instead it consults the shared package so the static layer
// and the workspace-side executor agree on the same opener / closer
// rules (strict length pairing, case-insensitive language tag, CRLF
// normalization, first-block-wins).
func TestScriptFenceReusesScriptcard(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		// accepted — canonical opener + closer
		{"```spore\nx\n```", true},
		{"preamble\n\n```spore\nx\n```\n", true},
		{"   ```spore\nx\n```", true}, // leading whitespace tolerated
		{"```spore \nx\n```", true},    // info string with trailing whitespace
		{"```SPORE\nx\n```", true},    // case-insensitive
		{"```Spore\nx\n```", true},
		// matched-length longer fence (canonical rule)
		{"`````spore\nx\n`````", true},
		// rejected — non-spore info string
		{"```python\nx\n```", false},
		{"plain body", false},
		{"", false},
		// rejected — opener alone does not close; the canonical rule
		// requires a same-length closer. The legacy "opener alone
		// suffices" shortcut is no longer valid.
		{"```spore", false},
		// rejected — opener/closer length mismatch
		{"`````spore\nx\n```\n", false},
		// rejected — first info token not EqualFold "spore"
		{"```spore1\nx\n```", false},
		{"```sporescript\nx\n```", false},
	}
	for _, c := range cases {
		_, got := scriptcard.ExtractSporeBlock(c.body)
		if got != c.want {
			t.Errorf("scriptcard.ExtractSporeBlock(%q) ok = %v, want %v", c.body, got, c.want)
		}
	}
}
