package project

import "testing"

// Empirical probe: does saveTimerState corrupt a nested schedule block or
// bound_agent when it writes run_status into the data: block?
func TestSaveTimerState_PreservesScheduleAndBoundAgent(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	// Realistic scheduler card: nested schedule (NOT 9:00) + bound_agent.
	seed := "---\n" +
		"id: scheduler:probe\n" +
		"type: scheduler\n" +
		"tags: [scheduler]\n" +
		"created: \"2026-09-01T00:00:00Z\"\n" +
		"modified: \"2026-09-01T00:00:00Z\"\n" +
		"data:\n" +
		"  schedule:\n" +
		"    cron: \"*/5 * * * *\"\n" +
		"    enabled: true\n" +
		"  schedule_type: agent_task\n" +
		"  bind_mode: bound\n" +
		"  bound_agent: \"agent:coder\"\n" +
		"---\n\nBody."

	card := &CardRecord{Title: "scheduler:probe", Raw: seed}
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed: %v", err)
	}
	card, err := a.store.Get("scheduler:probe")
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

	saved, err := a.store.Get("scheduler:probe")
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}

	// schedule must survive verbatim.
	sched := parseScheduleFromFrontmatter(saved.Raw)
	if sched.Cron != "*/5 * * * *" {
		t.Errorf("schedule cron after save = %q, want */5 * * * *", sched.Cron)
	}
	if !sched.Enabled {
		t.Errorf("schedule enabled after save = false, want true")
	}
	// bound_agent must survive.
	if got := cardDataString(saved, "bound_agent"); got != "agent:coder" {
		t.Errorf("bound_agent after save = %q, want agent:coder", got)
	}
	// run_status must be set.
	if got := timerState(saved); got != timerStatusRunning {
		t.Errorf("run_status after save = %q, want running", got)
	}
}
