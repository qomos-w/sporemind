package project

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestDataExecBlock pins the exact frontmatter fragment the handler injects for
// data.exec: exec: at the two-space data child indent, nested maps +2, string
// lists as block bullets, keys sorted (deterministic output), strings quoted.
// Both frontmatter parsers (Go parseDataBlock / TS collectMapItems) support this
// nesting: exec → budget → scalar is exactly the depth data.exec needs.
func TestDataExecBlock(t *testing.T) {
	// nil / empty → omitted, so callers that never pass Exec keep the legacy
	// raw byte-for-byte.
	if got := dataExecBlock(nil); got != "" {
		t.Fatalf("dataExecBlock(nil) = %q, want empty", got)
	}
	if got := dataExecBlock(map[string]any{}); got != "" {
		t.Fatalf("dataExecBlock(empty) = %q, want empty", got)
	}

	got := dataExecBlock(map[string]any{
		"kind":         "script",
		"capabilities": []any{"app.emit_state", "project.report"},
		"budget":       map[string]any{"max_duration_sec": float64(30), "max_host_calls": 4},
		"gate_card":    "gate-1",
	})
	want := "  exec:\n" +
		"    budget:\n" +
		"      max_duration_sec: 30\n" +
		"      max_host_calls: 4\n" +
		"    capabilities:\n" +
		"      - \"app.emit_state\"\n" +
		"      - \"project.report\"\n" +
		"    gate_card: \"gate-1\"\n" +
		"    kind: \"script\"\n"
	if got != want {
		t.Fatalf("dataExecBlock mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestDataExecBlockRoundTrip proves the emitted fragment survives the inline
// frontmatter parser the validator and runtime share (data.exec must decode back
// to the declared shape, not a string or a truncated map).
func TestDataExecBlockRoundTrip(t *testing.T) {
	raw := "---\nid: rt\ntype: task\ntags: [m]\nstatus: todo\nparent: m\ndata:\n  depends_on: []\n" +
		dataExecBlock(map[string]any{
			"kind":         "script",
			"capabilities": []any{"project.wiki_list_cards"},
			"budget":       map[string]any{"max_duration_sec": float64(30)},
		}) + "---\n\nbody"
	card := decodeCard("rt", raw)
	execBlock, ok := card.Data["exec"].(map[string]any)
	if !ok {
		t.Fatalf("decoded data.exec = %#v, want map", card.Data["exec"])
	}
	if execBlock["kind"] != "script" {
		t.Fatalf("decoded exec.kind = %#v, want script", execBlock["kind"])
	}
	if caps := toStringSlice(execBlock["capabilities"]); !reflect.DeepEqual(caps, []string{"project.wiki_list_cards"}) {
		t.Fatalf("decoded exec.capabilities = %#v", execBlock["capabilities"])
	}
	budget, ok := execBlock["budget"].(map[string]any)
	if !ok {
		t.Fatalf("decoded exec.budget = %#v, want map", execBlock["budget"])
	}
	// The Go parser keeps bare integers as digit strings; scriptcard accepts
	// both that and a decoded float, so the value must normalise to 30.
	if got := fmt.Sprint(budget["max_duration_sec"]); got != "30" {
		t.Fatalf("decoded budget.max_duration_sec = %v, want 30", budget["max_duration_sec"])
	}
}

// TestHandleWikiCreateTaskCard_InjectsExec is the end-to-end path: a caller
// passes a structured script Exec, the handler serializes it into data.exec,
// and the save-time validateCard gate (script shape + compile probe) accepts it.
func TestHandleWikiCreateTaskCard_InjectsExec(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-exec"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	body := "Compute the product.\n\n```spore\nexport fun run(): any {\n\treturn 42\n}\n```\n"
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "wf-exec",
		Title:    "script-exec-task",
		Question: body,
		Category: "execute",
		Exec: map[string]any{
			"kind":         "script",
			"capabilities": []any{"project.wiki_list_cards"},
			"budget":       map[string]any{"max_duration_sec": float64(30), "max_host_calls": float64(4)},
		},
	}); err != nil {
		t.Fatalf("create task with Exec: %v", err)
	}

	card, err := a.store.Get("script-exec-task")
	if err != nil {
		t.Fatalf("get created card: %v", err)
	}
	if !strings.Contains(card.Raw, "\n  exec:\n") {
		t.Fatalf("raw frontmatter missing data.exec block:\n%s", card.Raw)
	}
	execBlock, ok := card.Data["exec"].(map[string]any)
	if !ok {
		t.Fatalf("data.exec = %#v, want map", card.Data["exec"])
	}
	if execBlock["kind"] != "script" {
		t.Fatalf("exec.kind = %#v, want script", execBlock["kind"])
	}
	if caps := toStringSlice(execBlock["capabilities"]); !reflect.DeepEqual(caps, []string{"project.wiki_list_cards"}) {
		t.Fatalf("exec.capabilities = %#v", execBlock["capabilities"])
	}
	budget, ok := execBlock["budget"].(map[string]any)
	if !ok {
		t.Fatalf("exec.budget = %#v, want map", execBlock["budget"])
	}
	if got := fmt.Sprint(budget["max_duration_sec"]); got != "30" {
		t.Fatalf("budget.max_duration_sec = %v, want 30", budget["max_duration_sec"])
	}
	if !strings.Contains(card.Body, "```spore") {
		t.Fatalf("card body lost the spore fence:\n%s", card.Body)
	}

	// The card is in the map's scope include, so the workflow picked it up.
	if !containsString(scopeIncludeIDs(mustGetCard(t, a, "wf-exec")), "script-exec-task") {
		t.Fatalf("task id not appended to map scope.include")
	}
}

// TestHandleWikiCreateTaskCard_RejectsBadExec verifies the contract single-source
// rule: the schema carries no per-kind fields, so a malformed Exec must be
// rejected by the save-time validateCard gate (never persisted, so it can never
// reach a claim).
func TestHandleWikiCreateTaskCard_RejectsBadExec(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-bad"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	t.Run("unknown exec kind", func(t *testing.T) {
		_, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
			MapID: "wf-bad", Title: "bad-kind", Question: "Q",
			Exec: map[string]any{"kind": "definitely_not_a_kind"},
		})
		if err == nil || !strings.Contains(err.Error(), "not registered") {
			t.Fatalf("expected unknown-kind rejection, got %v", err)
		}
		if _, getErr := a.store.Get("bad-kind"); getErr == nil {
			t.Fatalf("rejected card was persisted")
		}
	})

	t.Run("script compile probe failure", func(t *testing.T) {
		// Unterminated function body — the compile-only probe must reject the
		// card at save rather than at claim-time preflight.
		_, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
			MapID:    "wf-bad",
			Title:    "bad-script",
			Question: "Body.\n\n```spore\nexport fun run(): int { return 0\n```\n",
			Exec:     map[string]any{"kind": "script", "capabilities": []any{"app.emit"}},
		})
		if err == nil || !strings.Contains(err.Error(), "failed to compile") {
			t.Fatalf("expected script compile rejection, got %v", err)
		}
		if _, getErr := a.store.Get("bad-script"); getErr == nil {
			t.Fatalf("rejected script card was persisted")
		}
	})

	t.Run("script missing capabilities", func(t *testing.T) {
		_, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
			MapID:    "wf-bad",
			Title:    "bad-script-caps",
			Question: "Body.\n\n```spore\nexport fun run(): any { return 1 }\n```\n",
			Exec:     map[string]any{"kind": "script"},
		})
		if err == nil || !strings.Contains(err.Error(), "capabilities is required") {
			t.Fatalf("expected missing-capabilities rejection, got %v", err)
		}
	})
}

// TestHandleWikiCreateTaskCard_NoExecProducesLegacyRaw locks the backward-
// compatible default: omitting Exec yields frontmatter with no exec block.
func TestHandleWikiCreateTaskCard_NoExecProducesLegacyRaw(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-plain"}); err != nil {
		t.Fatalf("create map: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "wf-plain", Title: "plain-task", Question: "Q",
	}); err != nil {
		t.Fatalf("create plain task: %v", err)
	}
	card, err := a.store.Get("plain-task")
	if err != nil {
		t.Fatalf("get plain card: %v", err)
	}
	if strings.Contains(card.Raw, "exec:") {
		t.Fatalf("no-Exec card unexpectedly carries an exec block:\n%s", card.Raw)
	}
	if _, ok := card.Data["exec"]; ok {
		t.Fatalf("no-Exec card decoded data.exec = %#v, want absent", card.Data["exec"])
	}
}

func mustGetCard(t *testing.T, a *Actor, id string) *CardRecord {
	t.Helper()
	card, err := a.store.Get(id)
	if err != nil {
		t.Fatalf("get card %q: %v", id, err)
	}
	return card
}
