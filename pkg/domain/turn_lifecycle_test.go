package domain

import (
	"reflect"
	"strings"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestIsTerminalTurnState(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{TurnStateRunning, false},
		{TurnStatePaused, false},
		{TurnStateCompleted, true},
		{TurnStateFailed, true},
		{TurnStateCancelled, true},
		{TurnStateAbandoned, true},
		{TurnStateWaiting, false},
		{TurnStateLegacyResumed, false}, // normalized to completed, not terminal by itself
		{"unknown", false},
	}
	for _, tc := range cases {
		if got := IsTerminalTurnState(tc.state); got != tc.want {
			t.Errorf("IsTerminalTurnState(%q) = %v, want %v", tc.state, got, tc.want)
		}
	}
}

func TestIsValidTurnState(t *testing.T) {
	valid := []string{TurnStateRunning, TurnStatePaused, TurnStateCompleted, TurnStateFailed, TurnStateCancelled, TurnStateAbandoned, TurnStateWaiting}
	for _, s := range valid {
		if !IsValidTurnState(s) {
			t.Errorf("IsValidTurnState(%q) = false, want true", s)
		}
	}
	if IsValidTurnState(TurnStateLegacyResumed) {
		t.Errorf("IsValidTurnState(%q) = true, want false (legacy state)", TurnStateLegacyResumed)
	}
	if IsValidTurnState("bogus") {
		t.Errorf("IsValidTurnState(%q) = true, want false", "bogus")
	}
}

func TestIsValidPauseReason(t *testing.T) {
	valid := []string{PauseReasonUser, PauseReasonInteraction, PauseReasonPermission, PauseReasonPlanApproval, PauseReasonGoalInput, PauseReasonRecovery}
	for _, r := range valid {
		if !IsValidPauseReason(r) {
			t.Errorf("IsValidPauseReason(%q) = false, want true", r)
		}
	}
	if IsValidPauseReason("custom") {
		t.Errorf("IsValidPauseReason(%q) = true, want false", "custom")
	}
}

func TestNormalizeTurnState(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{TurnStateLegacyResumed, TurnStateCompleted},
		{TurnStateRunning, TurnStateRunning},
		{"unknown", "unknown"},
	}
	for _, tc := range cases {
		if got := NormalizeTurnState(tc.in); got != tc.want {
			t.Errorf("NormalizeTurnState(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCanTransitionTurnState(t *testing.T) {
	active := []string{TurnStateRunning, TurnStatePaused, TurnStateWaiting}
	terminal := []string{TurnStateCompleted, TurnStateFailed, TurnStateCancelled, TurnStateAbandoned}
	all := append(active, terminal...)

	// Same-state is always allowed.
	for _, s := range all {
		if !CanTransitionTurnState(s, s) {
			t.Errorf("CanTransitionTurnState(%q -> %q) = false, want true", s, s)
		}
	}

	// Active -> any state is allowed.
	for _, from := range active {
		for _, to := range all {
			if from == to {
				continue
			}
			if !CanTransitionTurnState(from, to) {
				t.Errorf("CanTransitionTurnState(%q -> %q) = false, want true", from, to)
			}
		}
	}

	// Terminal -> any state is disallowed (including terminal -> terminal).
	for _, from := range terminal {
		for _, to := range all {
			if from == to {
				continue
			}
			if CanTransitionTurnState(from, to) {
				t.Errorf("CanTransitionTurnState(%q -> %q) = true, want false", from, to)
			}
		}
	}

	// Legacy resumed (normalized to completed) is terminal and cannot be revived.
	if CanTransitionTurnState(TurnStateLegacyResumed, TurnStateRunning) {
		t.Errorf("CanTransitionTurnState(resumed -> running) = true, want false")
	}

	// Four new waiting edges.
	edges := []struct{ from, to string }{
		{TurnStateRunning, TurnStateWaiting},
		{TurnStateWaiting, TurnStateCompleted},
		{TurnStateWaiting, TurnStatePaused},
		{TurnStatePaused, TurnStateWaiting},
	}
	for _, e := range edges {
		if !CanTransitionTurnState(e.from, e.to) {
			t.Errorf("CanTransitionTurnState(%q -> %q) = false, want true", e.from, e.to)
		}
	}
}

func TestLifecycleEventExpectedState(t *testing.T) {
	cases := []struct {
		kind, want string
	}{
		{TurnLifecycleStarted, TurnStateRunning},
		{TurnLifecyclePaused, TurnStatePaused},
		{TurnLifecycleResumed, TurnStateRunning},
		{TurnLifecycleCompleted, TurnStateCompleted},
		{TurnLifecycleFailed, TurnStateFailed},
		{TurnLifecycleCancelled, TurnStateCancelled},
		{TurnLifecycleAbandoned, TurnStateAbandoned},
		{TurnLifecycleWaiting, TurnStateWaiting},
		{"turn.unknown", ""},
	}
	for _, tc := range cases {
		if got := LifecycleEventExpectedState(tc.kind); got != tc.want {
			t.Errorf("LifecycleEventExpectedState(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func now() string {
	return time.Now().Format(time.RFC3339)
}

func event(kind, state string, revision, turnOrder int64) gen.TurnLifecycleEvent {
	return gen.TurnLifecycleEvent{
		Kind:      kind,
		TurnID:    "turn-1",
		State:     state,
		Revision:  revision,
		TurnOrder: turnOrder,
		StartedAt: "2026-08-09T00:00:00Z",
	}
}

func TestApplyTurnLifecycleEvent_Start(t *testing.T) {
	record := gen.Turn{ID: "turn-1"}
	ev := event(TurnLifecycleStarted, TurnStateRunning, 1, 7)
	out, err := ApplyTurnLifecycleEvent(record, ev, now())
	if err != nil {
		t.Fatalf("start error: %v", err)
	}
	if out.State != TurnStateRunning {
		t.Fatalf("state = %q, want running", out.State)
	}
	if out.Revision != 1 {
		t.Fatalf("revision = %d, want 1", out.Revision)
	}
	if out.TurnOrder != 7 {
		t.Fatalf("turnOrder = %d, want 7", out.TurnOrder)
	}
	if out.StartedAt != "2026-08-09T00:00:00Z" {
		t.Fatalf("startedAt = %q, want 2026-08-09T00:00:00Z", out.StartedAt)
	}
}

func TestApplyTurnLifecycleEvent_Waiting(t *testing.T) {
	// running -> waiting clears fields and stamps CompletedAt like a terminal event.
	record := gen.Turn{ID: "turn-1", State: TurnStateRunning, Revision: 1, TurnOrder: 7, StartedAt: "2026-08-09T00:00:00Z"}
	wait := event(TurnLifecycleWaiting, TurnStateWaiting, 2, 7)
	out, err := ApplyTurnLifecycleEvent(record, wait, now())
	if err != nil {
		t.Fatalf("waiting error: %v", err)
	}
	if out.State != TurnStateWaiting || out.CompletedAt == "" || out.Error != "" || out.PauseReason != "" || out.Revision != 2 {
		t.Fatalf("waiting result = %+v", out)
	}

	// waiting -> completed remains legal and re-stamps CompletedAt.
	completed, err := ApplyTurnLifecycleEvent(out, event(TurnLifecycleCompleted, TurnStateCompleted, 3, 7), now())
	if err != nil {
		t.Fatalf("waiting -> completed error: %v", err)
	}
	if completed.State != TurnStateCompleted || completed.CompletedAt == "" || completed.Revision != 3 {
		t.Fatalf("waiting -> completed result = %+v", completed)
	}

	// waiting -> paused requires a valid PauseReason.
	paused, err := ApplyTurnLifecycleEvent(out, event(TurnLifecyclePaused, TurnStatePaused, 3, 7), now())
	if err == nil {
		t.Fatalf("waiting -> paused without reason should error, got %+v", paused)
	}
	pause := event(TurnLifecyclePaused, TurnStatePaused, 3, 7)
	pause.PauseReason = PauseReasonUser
	paused, err = ApplyTurnLifecycleEvent(out, pause, now())
	if err != nil {
		t.Fatalf("waiting -> paused error: %v", err)
	}
	if paused.State != TurnStatePaused || paused.PauseReason != PauseReasonUser {
		t.Fatalf("waiting -> paused result = %+v", paused)
	}

	// paused -> waiting clears pause reason and stamps CompletedAt.
	resumeWait, err := ApplyTurnLifecycleEvent(paused, event(TurnLifecycleWaiting, TurnStateWaiting, 4, 7), now())
	if err != nil {
		t.Fatalf("paused -> waiting error: %v", err)
	}
	if resumeWait.State != TurnStateWaiting || resumeWait.PauseReason != "" || resumeWait.CompletedAt == "" || resumeWait.Revision != 4 {
		t.Fatalf("paused -> waiting result = %+v", resumeWait)
	}
}

func TestApplyTurnLifecycleEvent_PauseAndResume(t *testing.T) {
	record := gen.Turn{ID: "turn-1", State: TurnStateRunning, Revision: 1, TurnOrder: 7, StartedAt: "2026-08-09T00:00:00Z"}

	pause := event(TurnLifecyclePaused, TurnStatePaused, 2, 7)
	pause.PauseReason = PauseReasonInteraction
	out, err := ApplyTurnLifecycleEvent(record, pause, now())
	if err != nil {
		t.Fatalf("pause error: %v", err)
	}
	if out.State != TurnStatePaused || out.Revision != 2 || out.PauseReason != PauseReasonInteraction {
		t.Fatalf("pause result = %+v", out)
	}

	resume := event(TurnLifecycleResumed, TurnStateRunning, 3, 7)
	out, err = ApplyTurnLifecycleEvent(out, resume, now())
	if err != nil {
		t.Fatalf("resume error: %v", err)
	}
	if out.State != TurnStateRunning || out.Revision != 3 || out.PauseReason != "" {
		t.Fatalf("resume result = %+v", out)
	}
}

func TestApplyTurnLifecycleEvent_Terminal(t *testing.T) {
	record := gen.Turn{ID: "turn-1", State: TurnStateRunning, Revision: 1, TurnOrder: 7, StartedAt: "2026-08-09T00:00:00Z"}

	// Completed
	out, err := ApplyTurnLifecycleEvent(record, event(TurnLifecycleCompleted, TurnStateCompleted, 2, 7), now())
	if err != nil {
		t.Fatalf("completed error: %v", err)
	}
	if out.State != TurnStateCompleted || out.CompletedAt == "" || out.Error != "" {
		t.Fatalf("completed result = %+v", out)
	}

	// Failed
	fail := event(TurnLifecycleFailed, TurnStateFailed, 2, 7)
	fail.Error = "boom"
	out, err = ApplyTurnLifecycleEvent(record, fail, now())
	if err != nil {
		t.Fatalf("failed error: %v", err)
	}
	if out.State != TurnStateFailed || out.Error != "boom" || out.CompletedAt == "" {
		t.Fatalf("failed result = %+v", out)
	}

	// Cancelled
	out, err = ApplyTurnLifecycleEvent(record, event(TurnLifecycleCancelled, TurnStateCancelled, 2, 7), now())
	if err != nil {
		t.Fatalf("cancelled error: %v", err)
	}
	if out.State != TurnStateCancelled || !out.Cancelled || out.CompletedAt == "" {
		t.Fatalf("cancelled result = %+v", out)
	}
}

func TestApplyTurnLifecycleEvent_PauseRequiresReason(t *testing.T) {
	record := gen.Turn{ID: "turn-1", State: TurnStateRunning, Revision: 1}
	pause := event(TurnLifecyclePaused, TurnStatePaused, 2, 0)
	if _, err := ApplyTurnLifecycleEvent(record, pause, now()); err == nil {
		t.Fatalf("expected error for missing pause reason")
	}

	pause.PauseReason = "custom"
	if _, err := ApplyTurnLifecycleEvent(record, pause, now()); err == nil {
		t.Fatalf("expected error for unknown pause reason")
	}
}

func TestApplyTurnLifecycleEvent_FailedRequiresError(t *testing.T) {
	record := gen.Turn{ID: "turn-1", State: TurnStateRunning, Revision: 1}
	fail := event(TurnLifecycleFailed, TurnStateFailed, 2, 0)
	if _, err := ApplyTurnLifecycleEvent(record, fail, now()); err == nil {
		t.Fatalf("expected error for missing error")
	}
}

func TestApplyTurnLifecycleEvent_RejectsStaleRevision(t *testing.T) {
	record := gen.Turn{ID: "turn-1", State: TurnStateRunning, Revision: 5}
	old := event(TurnLifecycleCompleted, TurnStateCompleted, 4, 0)
	if _, err := ApplyTurnLifecycleEvent(record, old, now()); err == nil {
		t.Fatalf("expected error for stale revision")
	}
}

func TestApplyTurnLifecycleEvent_RejectsTerminalRevival(t *testing.T) {
	for _, from := range []string{TurnStateCompleted, TurnStateFailed, TurnStateCancelled, TurnStateAbandoned} {
		record := gen.Turn{ID: "turn-1", State: from, Revision: 1}
		resume := event(TurnLifecycleResumed, TurnStateRunning, 2, 0)
		if _, err := ApplyTurnLifecycleEvent(record, resume, now()); err == nil {
			t.Fatalf("expected error reviving %q", from)
		}
	}
}

func TestApplyTurnLifecycleEvent_RejectsTerminalToTerminal(t *testing.T) {
	record := gen.Turn{ID: "turn-1", State: TurnStateCompleted, Revision: 1}
	failed := event(TurnLifecycleFailed, TurnStateFailed, 2, 0)
	failed.Error = "boom"
	if _, err := ApplyTurnLifecycleEvent(record, failed, now()); err == nil {
		t.Fatalf("expected error for terminal-to-terminal transition")
	}
}

func TestApplyTurnLifecycleEvent_MismatchedTurnID(t *testing.T) {
	record := gen.Turn{ID: "turn-1"}
	ev := event(TurnLifecycleStarted, TurnStateRunning, 1, 0)
	ev.TurnID = "turn-2"
	if _, err := ApplyTurnLifecycleEvent(record, ev, now()); err == nil {
		t.Fatalf("expected error for turn id mismatch")
	}
}

func TestApplyTurnLifecycleEvent_MismatchedKindAndState(t *testing.T) {
	record := gen.Turn{ID: "turn-1", State: TurnStateRunning, Revision: 1}
	ev := event(TurnLifecycleStarted, TurnStatePaused, 2, 0) // started cannot be paused
	ev.PauseReason = PauseReasonUser
	if _, err := ApplyTurnLifecycleEvent(record, ev, now()); err == nil {
		t.Fatalf("expected error for kind/state mismatch")
	}
}

func TestApplyTurnLifecycleEvent_LegacyResumedNormalized(t *testing.T) {
	record := gen.Turn{ID: "turn-1", State: TurnStateLegacyResumed, Revision: 0, TurnOrder: 3}
	normalized := NormalizeTurnForLifecycle(record)
	if normalized.State != TurnStateCompleted {
		t.Fatalf("normalized state = %q, want completed", normalized.State)
	}
	if normalized.TurnOrder != 3 {
		t.Fatalf("normalized turnOrder = %d, want 3", normalized.TurnOrder)
	}
}

func TestApplyTurnLifecycleEvent_IdempotentSameState(t *testing.T) {
	record := gen.Turn{ID: "turn-1", State: TurnStateRunning, Revision: 5, TurnOrder: 7, StartedAt: "2026-08-09T00:00:00Z"}
	ev := event(TurnLifecycleStarted, TurnStateRunning, 5, 7)
	out, err := ApplyTurnLifecycleEvent(record, ev, now())
	if err != nil {
		t.Fatalf("same-state error: %v", err)
	}
	if out.Revision != 5 {
		t.Fatalf("same-state revision changed to %d", out.Revision)
	}
}

func TestApplyTurnLifecycleEvent_ClearsErrorOnResume(t *testing.T) {
	record := gen.Turn{ID: "turn-1", State: TurnStatePaused, Revision: 2, TurnOrder: 7, Error: "stale", PauseReason: PauseReasonUser}
	resume := event(TurnLifecycleResumed, TurnStateRunning, 3, 7)
	out, err := ApplyTurnLifecycleEvent(record, resume, now())
	if err != nil {
		t.Fatalf("resume error: %v", err)
	}
	if out.Error != "" || out.PauseReason != "" {
		t.Fatalf("resume did not clear error/pause reason: %+v", out)
	}
}

func TestApplyTurnLifecycleEvent_Abandon(t *testing.T) {
	record := gen.Turn{ID: "turn-1", State: TurnStateRunning, Revision: 1, TurnOrder: 7, StartedAt: "2026-08-09T00:00:00Z"}
	abandon := gen.TurnLifecycleEvent{
		Kind:      TurnLifecycleAbandoned,
		TurnID:    "turn-1",
		State:     TurnStateAbandoned,
		Revision:  2,
		TurnOrder: 7,
		StartedAt: "2026-08-09T00:00:00Z",
	}
	out, err := ApplyTurnLifecycleEvent(record, abandon, now())
	if err != nil {
		t.Fatalf("abandon error: %v", err)
	}
	if out.State != TurnStateAbandoned || out.CompletedAt == "" {
		t.Fatalf("abandon result = %+v", out)
	}
}

func TestTurnLifecycleEventSchemaSurface(t *testing.T) {
	var ev gen.TurnLifecycleEvent
	rt := reflect.TypeOf(ev)

	required := map[string]reflect.Kind{
		"Kind":      reflect.String,
		"TurnID":    reflect.String,
		"State":     reflect.String,
		"Revision":  reflect.Int64,
		"TurnOrder": reflect.Int64,
		"StartedAt": reflect.String,
	}
	for name, wantKind := range required {
		f, ok := rt.FieldByName(name)
		if !ok {
			t.Fatalf("TurnLifecycleEvent missing required field %q", name)
		}
		if f.Type.Kind() != wantKind {
			t.Fatalf("TurnLifecycleEvent.%s kind = %v, want %v", name, f.Type.Kind(), wantKind)
		}
		tag := f.Tag.Get("json")
		if strings.HasSuffix(tag, ",omitempty") {
			t.Fatalf("TurnLifecycleEvent.%s must be required (no omitempty), got tag %q", name, tag)
		}
	}

	optional := map[string]reflect.Kind{
		"CompletedAt": reflect.String,
		"Error":       reflect.String,
		"PauseReason": reflect.String,
		"Payload":     reflect.Map,
	}
	for name, wantKind := range optional {
		f, ok := rt.FieldByName(name)
		if !ok {
			t.Fatalf("TurnLifecycleEvent missing optional field %q", name)
		}
		if f.Type.Kind() != wantKind {
			t.Fatalf("TurnLifecycleEvent.%s kind = %v, want %v", name, f.Type.Kind(), wantKind)
		}
		tag := f.Tag.Get("json")
		if !strings.HasSuffix(tag, ",omitempty") {
			t.Fatalf("TurnLifecycleEvent.%s must be optional (omitempty), got tag %q", name, tag)
		}
	}
}

func TestTurnRecordHasLifecycleFields(t *testing.T) {
	var tr gen.Turn
	rt := reflect.TypeOf(tr)

	want := []string{"Revision", "PauseReason", "ResumeDescriptor"}
	for _, name := range want {
		if _, ok := rt.FieldByName(name); !ok {
			t.Fatalf("Turn missing lifecycle field %q", name)
		}
	}

	state, ok := rt.FieldByName("State")
	if !ok || !strings.HasSuffix(state.Tag.Get("json"), ",omitempty") {
		t.Fatalf("Turn.State must be optional with omitempty")
	}
}
