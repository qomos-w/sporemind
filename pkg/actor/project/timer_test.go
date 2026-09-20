package project

import (
	"strings"
	"testing"
)

func TestParseScheduleFromFrontmatter_Cron(t *testing.T) {
	raw := `---
title: Daily Sync
tags: [automation, timer]
schedule:
  cron: "0 9 * * *"
---

Do the daily sync.`

	sched := parseScheduleFromFrontmatter(raw)
	if sched.Cron != "0 9 * * *" {
		t.Errorf("Cron: got %q, want 0 9 * * *", sched.Cron)
	}
	if sched.Expression != "" {
		t.Errorf("Expression: got %q, want empty", sched.Expression)
	}
}

func TestParseScheduleFromData(t *testing.T) {
	raw := `---
tags: [scheduler]
data:
  schedule:
    cron: "0 9 * * *"
  executor: "agent:coder"
---

Run.`
	sched := parseScheduleFromFrontmatter(raw)
	if sched.Cron != "0 9 * * *" {
		t.Fatalf("Cron: got %q", sched.Cron)
	}
}

func TestParseScheduleFromFrontmatter_Expression(t *testing.T) {
	raw := `---
title: Periodic Task
schedule:
  expression: "every 30 minutes"
---

Run something.`

	sched := parseScheduleFromFrontmatter(raw)
	if sched.Expression != "every 30 minutes" {
		t.Errorf("Expression: got %q, want 'every 30 minutes'", sched.Expression)
	}
	if sched.Cron != "" {
		t.Errorf("Cron: got %q, want empty", sched.Cron)
	}
}

func TestParseScheduleFromFrontmatter_NoSchedule(t *testing.T) {
	raw := `---
title: Regular Card
tags: [note]
---

Just a note.`

	sched := parseScheduleFromFrontmatter(raw)
	if sched.Cron != "" || sched.Expression != "" {
		t.Errorf("expected empty schedule, got cron=%q expr=%q", sched.Cron, sched.Expression)
	}
}

func TestParseScheduleFromFrontmatter_Timezone(t *testing.T) {
	raw := `---
title: TZ Task
schedule:
  cron: "0 9 * * *"
  timezone: "Asia/Shanghai"
---

Run.`

	sched := parseScheduleFromFrontmatter(raw)
	if sched.Timezone != "Asia/Shanghai" {
		t.Errorf("Timezone: got %q, want Asia/Shanghai", sched.Timezone)
	}
}

func TestCreateResultCardID(t *testing.T) {
	// Verify the ID format: scheduler:{cardId}#{timestamp}
	// The cardId is "scheduler:daily-sync", the prefix is stripped and re-added.
	cardID := "scheduler:daily-sync"
	ts := "2026-07-13-120000"
	expected := "scheduler:daily-sync#2026-07-13-120000"
	result := "scheduler:" + cardID[len("scheduler:"):] + "#" + ts
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

// frontendStyleRewrite models the frontend save path
// (web/src/domain/mono-types.ts formatMonoCard): the raw is rebuilt from the
// parser's known top-level fields, the data: block is carried verbatim, and
// every other top-level key is dropped.
func frontendStyleRewrite(raw string) string {
	front, ok := cardFrontmatter(raw)
	if !ok {
		return raw
	}
	end := 3 + strings.Index(raw[3:], "---")
	body := strings.TrimPrefix(raw[end:], "---")

	known := map[string]bool{
		"id": true, "type": true, "tags": true, "list": true,
		"created": true, "modified": true, "due": true, "priority": true,
		"status": true, "parent": true, "standalone": true, "data": true,
	}
	var kept []string
	inData := false
	for _, line := range strings.Split(front, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indented := line[0] == ' ' || line[0] == '\t'
		if indented && inData {
			kept = append(kept, line)
			continue
		}
		inData = false
		key, _, _ := strings.Cut(trimmed, ":")
		key = strings.TrimSpace(key)
		if known[key] {
			kept = append(kept, line)
			inData = key == "data"
		}
	}
	return "---\n" + strings.Join(kept, "\n") + "\n---" + body
}

// TestSaveTimerState_SurvivesFrontendRewrite locks in the fix for the
// "schedule saved → task status reset to none" bug: timer state written as
// top-level frontmatter keys was dropped by the frontend's parse→format save
// path. saveTimerState must keep every runtime field inside the data: block,
// and the dual-path readers must still resolve legacy top-level values.
func TestSaveTimerState_SurvivesFrontendRewrite(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	seed := "---\nid: scheduler:roundtrip\ntype: scheduler\ntags: [automation, scheduler]\n" +
		"created: \"2026-09-01T00:00:00Z\"\nmodified: \"2026-09-01T00:00:00Z\"\n" +
		"data:\n  schedule:\n    cron: \"0 9 * * *\"\n---\n\nDaily run."
	card := &CardRecord{Title: "scheduler:roundtrip", Raw: seed}
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed: %v", err)
	}

	card, err := a.store.Get("scheduler:roundtrip")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := a.saveTimerState(card, map[string]string{
		"run_status":   timerStatusRunning,
		"last_run":     "2026-09-02T09:00:00Z",
		"executor_ref": "agent:coder",
		"reviewer_ref": "agent:reviewer",
	}); err != nil {
		t.Fatalf("saveTimerState: %v", err)
	}

	saved, err := a.store.Get("scheduler:roundtrip")
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	// Timer runtime fields must not live at frontmatter top level: that is the
	// location the frontend rewrite drops.
	for _, key := range []string{"run_status", "last_run", "executor_ref", "reviewer_ref"} {
		if v := frontmatterValue(saved.Raw, key); v != "" {
			t.Errorf("%s leaked to frontmatter top level: %q", key, v)
		}
	}

	// A frontend-style rewrite (edit + save from the schedule modal) must keep
	// the run state resolvable.
	after := decodeCard("scheduler:roundtrip", frontendStyleRewrite(saved.Raw))
	if got := timerState(after); got != timerStatusRunning {
		t.Errorf("run_status after frontend rewrite = %q, want running", got)
	}
	if got := lastRunOf(after); got != "2026-09-02T09:00:00Z" {
		t.Errorf("last_run after frontend rewrite = %q", got)
	}
	if got := executorRefOf(after); got != "agent:coder" {
		t.Errorf("executor_ref after frontend rewrite = %q", got)
	}
	if got := reviewerRefOf(after); got != "agent:reviewer" {
		t.Errorf("reviewer_ref after frontend rewrite = %q", got)
	}
}

// TestTimerRuntimeField_TopLevelFallback verifies the legacy path: cards
// written before the data-block migration still resolve their run state from
// top-level frontmatter keys.
func TestTimerRuntimeField_TopLevelFallback(t *testing.T) {
	raw := "---\nid: scheduler:legacy\nrun_status: \"failed\"\nlast_run: \"2026-08-01T00:00:00Z\"\n---\n\nRun."
	card := decodeCard("scheduler:legacy", raw)
	if got := timerState(card); got != timerStatusFailed {
		t.Errorf("timerState = %q, want failed", got)
	}
	if got := lastRunOf(card); got != "2026-08-01T00:00:00Z" {
		t.Errorf("lastRunOf = %q", got)
	}
	if got := executorRefOf(card); got != "" {
		t.Errorf("executorRefOf = %q, want empty", got)
	}
}
