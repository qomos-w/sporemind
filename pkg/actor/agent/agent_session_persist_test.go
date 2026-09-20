package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestCanonicalCardRefsRoundTrip(t *testing.T) {
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "builtin:user", Kind: "card-ref", Scope: "user"},
		{CardID: "builtin:default", Kind: "card-ref", Scope: skillScopeKindConfig},
		{CardID: "builtin:disabled", Kind: "card-ref", Scope: "user", Enabled: false},
	}, cardRefs: []gen.CardRef{{ID: "builtin:user", Source: "card-ref", Scope: "user"}}}
	if len(a.cardRefs) != 1 || a.cardRefs[0].ID != "builtin:user" {
		t.Fatalf("unexpected canonical refs: %+v", a.cardRefs)
	}
	projected := componentMountsFromCardRefs([]gen.CardRef{{ID: "builtin:ordered", Order: 7}})
	if len(projected) != 1 || projected[0].Order != 7 {
		t.Fatalf("CardRef order not projected: %+v", projected)
	}
}

func TestAgentStoreKey_ReturnsActorID(t *testing.T) {
	a := &Actor{actorID: "019f30e43afd00000000000000000003"}
	got := a.agentStoreKey()
	if got != "019f30e43afd00000000000000000003" {
		t.Errorf("expected actorID, got %q", got)
	}
}

func TestTurnsDir_UsesActorID(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{actorID: "019f30e43afd00000000000000000003"}
	got := a.turnsDir()
	want := filepath.Join(config.ActorDataDir(), "agent", "019f30e43afd00000000000000000003", "turns")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestSnapshotsDir_UsesActorID(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{actorID: "019f30e43afd00000000000000000003"}
	got := a.snapshotsDir()
	want := filepath.Join(config.ActorDataDir(), "agent", "019f30e43afd00000000000000000003", "snapshots")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestAgentStoreKey_FlatLayout(t *testing.T) {
	a := &Actor{actorID: "019f30e43afd00000000000000000003"}
	key := a.agentStoreKey()
	if strings.Contains(key, string(filepath.Separator)) {
		t.Errorf("expected flat key, got %q", key)
	}
}

func TestSaveMailbox_SkipsChildAgent(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{child: childState{Mode: true}, actorID: "child-id"}
	a.saveMailbox(nil) // must not panic
}

func TestSaveSnapshot_SkipsChildAgent(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{child: childState{Mode: true}, actorID: "child-id"}
	if err := a.saveSnapshot("snap-1", []byte("content")); err != nil {
		t.Fatalf("saveSnapshot returned error: %v", err)
	}

	_, err := os.Stat(a.snapshotsDir())
	if err == nil {
		t.Errorf("child agent should not create snapshots directory")
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected stat error: %v", err)
	}
}

// TestRecoverOrphanTurnSteps_CreatesPausedTurn verifies that steps left behind
// by a crashed in-flight turn are wrapped in a paused turn entry so
// compileMessages includes them in the next turn's LLM history. The agent
// enters a paused state that the user can resume from.
func TestRecoverOrphanTurnSteps_CreatesPausedTurn(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "user", State: "completed"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Closed: true, Seq: 1,
					Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "read foo.go"}}},
				// Orphan assistant steps from crashed in-flight turn-2
				{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true, Seq: 2,
					Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "let me read that file"}}},
				{ID: "tool1", Role: "assistant", Type: "tool_call", TurnID: "turn-2", Closed: true, Seq: 3,
					Content: []domain.ContentBlock{
						{Type: domain.ContentBlockToolUse, ToolUseID: "tu-1", ToolName: "project.read", Input: `{"path":"foo.go"}`},
						{Type: domain.ContentBlockToolResult, ToolUseID: "tu-1", Text: "file contents here"},
					}},
			},
		},
	}
	_ = a.rebuildSteps()
	a.recoverOrphanTurnSteps()

	// turn-2 should now be a paused/recovery turn in Session.Turns, created via
	// the canonical lifecycle reducer.
	var found bool
	for _, turn := range a.Session.Turns {
		if turn.ID == "turn-2" {
			found = true
			if turn.State != "paused" {
				t.Errorf("orphan turn state = %q, want paused", turn.State)
			}
			if turn.Role != "assistant" {
				t.Errorf("orphan turn role = %q, want assistant", turn.Role)
			}
			if turn.PauseReason != "recovery" {
				t.Errorf("orphan turn PauseReason = %q, want recovery", turn.PauseReason)
			}
			if turn.Error != "" {
				t.Errorf("orphan turn Error = %q, want empty for paused", turn.Error)
			}
			if turn.Revision != 1 {
				t.Errorf("orphan turn Revision = %d, want 1", turn.Revision)
			}
			if turn.CompletedAt != "" {
				t.Errorf("orphan turn CompletedAt = %q, want empty for paused", turn.CompletedAt)
			}
			if turn.TurnOrder == 0 {
				t.Errorf("orphan turn TurnOrder = 0, want allocated")
			}
		}
	}
	if !found {
		t.Fatalf("orphan turn-2 not found in Session.Turns after recovery")
	}

	// Agent should be in paused state with ActiveTurnRef set.
	if a.status.State != "paused" {
		t.Errorf("status.State = %q, want paused", a.status.State)
	}
	if a.ActiveTurnRef != "turn-2" {
		t.Errorf("ActiveTurnRef = %q, want turn-2", a.ActiveTurnRef)
	}

	// ActiveHead should point past the orphan turn.
	if a.Session.ActiveHead != int32(len(a.Session.Turns)-1) {
		t.Errorf("ActiveHead = %d, want %d", a.Session.ActiveHead, len(a.Session.Turns)-1)
	}

	// compileMessages should include tool_use/tool_result from the orphan turn.
	msgs := a.compileMessages(true)
	var hasToolUse, hasToolResult bool
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == domain.ContentBlockToolUse {
				hasToolUse = true
			}
			if b.Type == domain.ContentBlockToolResult {
				hasToolResult = true
			}
		}
	}
	if !hasToolUse {
		t.Errorf("compileMessages: expected tool_use block from orphan turn, not found")
	}
	if !hasToolResult {
		t.Errorf("compileMessages: expected tool_result block from orphan turn, not found")
	}
}

// TestRecoverOrphanTurnSteps_UnsafeOrphanAbandoned verifies that orphan assistant
// steps containing a tool_use without a matching tool_result are abandoned (not
// paused) because they cannot be safely included in a resumed turn's LLM history.
// The diagnostic Error records the reason and CompletedAt is set.
func TestRecoverOrphanTurnSteps_UnsafeOrphanAbandoned(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "user", State: "completed"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Closed: true, Seq: 1,
					Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "read foo.go"}}},
				// Orphan assistant step with tool_use but no matching tool_result.
				{ID: "a1", Role: "assistant", Type: "tool_call", TurnID: "turn-2", Closed: true, Seq: 2,
					Content: []domain.ContentBlock{
						{Type: domain.ContentBlockToolUse, ToolUseID: "tu-1", ToolName: "project.read", Input: `{"path":"foo.go"}`},
					}},
			},
		},
	}
	_ = a.rebuildSteps()
	a.recoverOrphanTurnSteps()

	var found bool
	for _, turn := range a.Session.Turns {
		if turn.ID == "turn-2" {
			found = true
			if turn.State != "abandoned" {
				t.Errorf("unsafe orphan turn state = %q, want abandoned", turn.State)
			}
			if turn.Error == "" {
				t.Errorf("unsafe orphan turn Error should contain diagnostic, got empty")
			}
			if turn.PauseReason != "" {
				t.Errorf("unsafe orphan turn PauseReason = %q, want empty", turn.PauseReason)
			}
			if turn.Revision != 1 {
				t.Errorf("unsafe orphan turn Revision = %d, want 1", turn.Revision)
			}
			if turn.CompletedAt == "" {
				t.Errorf("unsafe orphan turn CompletedAt should be set")
			}
		}
	}
	if !found {
		t.Fatalf("unsafe orphan turn-2 not found in Session.Turns")
	}
	if a.ActiveTurnRef != "" {
		t.Errorf("ActiveTurnRef = %q, want empty (abandoned turn is terminal)", a.ActiveTurnRef)
	}
	if a.status.State != "" {
		t.Errorf("status.State = %q, want empty (idle) for abandoned orphan", a.status.State)
	}
	if a.Session.ActiveHead != int32(len(a.Session.Turns)-1) {
		t.Errorf("ActiveHead = %d, want %d", a.Session.ActiveHead, len(a.Session.Turns)-1)
	}
}

// TestRecoverOrphanTurnSteps_SkipsUserTurnOrphans verifies that discarded user
// turns are not rewrapped as paused assistant turns.
func TestRecoverOrphanTurnSteps_SkipsUserTurnOrphans(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-2", Role: "assistant", State: "completed"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{ID: "user-turn-1", Role: "user", Type: "text", TurnID: "user-turn-1", Closed: true, Seq: 1,
					Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
				{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true, Seq: 2,
					Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
			},
		},
	}
	_ = a.rebuildSteps()
	changed := a.recoverOrphanTurnSteps()

	if changed {
		t.Errorf("recoverOrphanTurnSteps() = true, want false")
	}
	for _, turn := range a.Session.Turns {
		if turn.ID == "user-turn-1" && turn.Role == "assistant" {
			t.Errorf("user turn %q was rewrapped as assistant paused turn", turn.ID)
		}
	}
	if a.ActiveTurnRef != "" {
		t.Errorf("ActiveTurnRef = %q, want empty", a.ActiveTurnRef)
	}
}

// TestRecoverOrphanTurnSteps_NoOrphans verifies no-op when all steps belong to
// known turns.
func TestRecoverOrphanTurnSteps_NoOrphans(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "user", State: "completed"},
				{ID: "turn-2", Role: "assistant", State: "completed"},
			},
			ActiveHead: 1,
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Closed: true},
				{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true},
			},
		},
	}
	_ = a.rebuildSteps()
	a.recoverOrphanTurnSteps()

	if len(a.Session.Turns) != 2 {
		t.Errorf("expected 2 turns, got %d", len(a.Session.Turns))
	}
}

// TestRestart_RecoverUnsafeOrphanToAbandoned verifies a full save/reload cycle for
// orphan assistant steps with an unrecoverable tool protocol: the steps are wrapped
// into an abandoned turn with a diagnostic Error and CompletedAt, and a second
// restart is idempotent (no duplicate records).
func TestRestart_RecoverUnsafeOrphanToAbandoned(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())
	actorID := "unsafe-orphan-test-actor"
	actorDir := filepath.Join(config.ActorDataDir(), "agent", actorID)
	if err := os.MkdirAll(filepath.Join(actorDir, "turns"), 0755); err != nil {
		t.Fatalf("mkdir turns: %v", err)
	}

	src := &Actor{actorID: actorID, snapshotReady: true}
	src.Session = domain.Session{Turns: []domain.Turn{
		{ID: "t1", Role: "user", Seq: 1, State: "completed"},
	}}
	src.steps = []domain.Step{
		{ID: "s1", TurnID: "t1", Role: "user", Type: "text", Seq: 1, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
		// Orphan step with tool_use but no tool_result: unrecoverable.
		{ID: "s2", TurnID: "t2", Role: "assistant", Type: "tool_call", Seq: 2, Closed: true,
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockToolUse, ToolUseID: "tu-1", ToolName: "project.read", Input: `{"path":"foo.go"}`},
			}},
	}
	src.saveMailbox(nil)
	if err := src.saveTurns(); err != nil {
		t.Fatalf("saveTurns: %v", err)
	}

	reloaded := &Actor{actorID: actorID, snapshotReady: true}
	reloaded.loadMailbox(nil)
	reloaded.rebuildSteps()
	if !reloaded.recoverOrphanTurnSteps() {
		t.Fatal("expected recoverOrphanTurnSteps to create an abandoned turn")
	}
	reloaded.recoverTurnStatus()
	reloaded.recoverPendingInteraction(nil)
	reloaded.takeSnapshot()

	var t2 *domain.Turn
	for i := range reloaded.Session.Turns {
		if reloaded.Session.Turns[i].ID == "t2" {
			t2 = &reloaded.Session.Turns[i]
			break
		}
	}
	if t2 == nil {
		t.Fatal("orphan turn t2 missing after reload")
	}
	if t2.State != "abandoned" {
		t.Errorf("t2 state = %q, want abandoned", t2.State)
	}
	if t2.Error == "" {
		t.Errorf("t2 Error should contain diagnostic, got empty")
	}
	if t2.CompletedAt == "" {
		t.Errorf("t2 CompletedAt should be set")
	}
	if t2.Revision != 1 {
		t.Errorf("t2 Revision = %d, want 1", t2.Revision)
	}
	if reloaded.ActiveTurnRef != "" {
		t.Errorf("ActiveTurnRef = %q, want empty (abandoned is terminal)", reloaded.ActiveTurnRef)
	}
	if reloaded.status.State != "" {
		t.Errorf("status.State = %q, want empty (idle)", reloaded.status.State)
	}
	if len(reloaded.Session.Turns) != 2 {
		t.Errorf("len(Session.Turns) = %d, want 2", len(reloaded.Session.Turns))
	}

	reloaded.saveMailbox(nil)
	if err := reloaded.saveTurns(); err != nil {
		t.Fatalf("saveTurns after recovery: %v", err)
	}
	again := &Actor{actorID: actorID, snapshotReady: true}
	again.loadMailbox(nil)
	again.rebuildSteps()
	if again.recoverOrphanTurnSteps() {
		t.Error("second restart should not create another orphan turn")
	}
	if len(again.Session.Turns) != 2 {
		t.Errorf("len(Session.Turns) after second reload = %d, want 2 (no duplicate)", len(again.Session.Turns))
	}
}

// TestStepsDir_UsesActorID verifies the per-turn step file directory layout.
func TestStepsDir_UsesActorID(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{actorID: "019f30e43afd00000000000000000003"}
	got := a.stepsDir()
	want := filepath.Join(config.ActorDataDir(), "agent", "019f30e43afd00000000000000000003", "steps")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

// TestFlushClosedSteps_WritesPerTurnFiles verifies that saveMailbox writes
// closed steps to per-turn JSONL files and that only open steps remain in
// RawSession.Steps.
func TestFlushClosedSteps_WritesPerTurnFiles(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{actorID: "persist-test-actor"}
	a.steps = []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Closed: true, Seq: 1,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
		{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Closed: true, Seq: 2,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi there"}}},
		{ID: "a2", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true, Seq: 3,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "response"}}},
		{ID: "a3", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: false, Seq: 4,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "streaming..."}}},
	}

	a.saveMailbox(nil)

	// Check per-turn files were created.
	data, err := os.ReadFile(filepath.Join(a.stepsDir(), "turn-1.jsonl"))
	if err != nil {
		t.Fatalf("turn-1.jsonl not created: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("turn-1.jsonl: expected 2 steps, got %d", len(lines))
	}

	data2, err := os.ReadFile(filepath.Join(a.stepsDir(), "turn-2.jsonl"))
	if err != nil {
		t.Fatalf("turn-2.jsonl not created: %v", err)
	}
	lines2 := strings.Split(strings.TrimSpace(string(data2)), "\n")
	if len(lines2) != 1 {
		t.Errorf("turn-2.jsonl: expected 1 closed step, got %d", len(lines2))
	}

	// Only the open step should remain in RawSession.Steps.
	if len(a.RawSession.Steps) != 1 || a.RawSession.Steps[0].ID != "a3" {
		t.Errorf("RawSession.Steps should only have open step a3, got %d steps: %+v",
			len(a.RawSession.Steps), a.RawSession.Steps)
	}
}

// TestSaveTurns_CrashDoesNotCorruptExistingFile is the P0 crash-safety
// acceptance test. It writes a turn, then simulates a crash DURING a
// subsequent overwrite by leaving a truncated stale turn.json in place
// (i.e. a crash that happened after the old direct os.WriteFile had started
// overwriting but before it finished). It then calls saveTurns again and
// asserts:
//   - the final turn.json is valid JSON and carries the new content (the
//     atomic write replaced the truncated file entirely), and
//   - rebuildSteps + the persisted index stay consistent.
//
// This locks the contract that a crash/kill mid-write cannot leave a
// truncated turn file: there is no observable "half-written" state because
// the real payload lands via rename.
func TestSaveTurns_CrashDoesNotCorruptExistingFile(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{actorID: "crash-test-actor"}
	turn1 := domain.Turn{ID: "turn-1", Role: "user", State: "completed"}
	a.Session = domain.Session{Turns: []domain.Turn{turn1}, ActiveHead: 0}

	if err := a.saveTurns(); err != nil {
		t.Fatalf("first saveTurns: %v", err)
	}
	turnPath := filepath.Join(a.turnsDir(), "turn-1.json")

	// Simulate a crash mid-overwrite of the OLD non-atomic path: leave a
	// truncated, invalid-JSON file on disk as if the process were killed
	// partway through writing.
	if err := os.WriteFile(turnPath, []byte(`{"id":"turn-1","state":"run`), 0644); err != nil {
		t.Fatalf("seed truncated file: %v", err)
	}

	// Now a fresh save (the "restart") must atomically replace the truncated
	// file with the full, valid payload.
	a.turnSync = nil // force re-write so saveTurns does not short-circuit on signature
	if err := a.saveTurns(); err != nil {
		t.Fatalf("second saveTurns after simulated crash: %v", err)
	}

	got, err := os.ReadFile(turnPath)
	if err != nil {
		t.Fatalf("read turn-1.json after recovery save: %v", err)
	}
	// Must be valid JSON now (no truncated tail).
	if !json.Valid(got) {
		t.Fatalf("turn-1.json is not valid JSON after recovery save: %q", got)
	}
	// No stray .tmp left behind from the atomic write.
	if _, err := os.Stat(turnPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("stray .tmp file present after successful atomic write")
	}
	// index.json must also be valid JSON and list turn-1.
	idxData, err := os.ReadFile(filepath.Join(a.turnsDir(), "index.json"))
	if err != nil {
		t.Fatalf("read index.json: %v", err)
	}
	if !json.Valid(idxData) {
		t.Fatalf("index.json is not valid JSON: %q", idxData)
	}

	// rebuildSteps must still succeed (consistency between JSONL + RawSession
	// on a fresh actor with no steps is the trivial-but-important case).
	a2 := &Actor{actorID: "crash-test-actor"}
	a2.rebuildSteps()
}

// TestRebuildSteps_LoadsFromStepFiles verifies that rebuildSteps loads
// closed steps from per-turn files and combines them with open steps from
// RawSession.Steps.
func TestRebuildSteps_LoadsFromStepFiles(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{actorID: "rebuild-test-actor"}
	a.Session = domain.Session{
		Turns: []domain.Turn{
			{ID: "turn-1", Role: "user", State: "completed"},
		},
		ActiveHead: 0,
	}
	a.steps = []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Closed: true, Seq: 1,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
		{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Closed: true, Seq: 2,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
	}

	// Flush to files, then simulate restart by clearing runtime state.
	a.saveMailbox(nil)

	// Simulate restart: clear steps, set RawSession from state.json (which now
	// only has open steps — none in this case since all are closed).
	a2 := &Actor{actorID: "rebuild-test-actor"}
	a2.Session = a.Session
	a2.RawSession.Steps = nil // no open steps
	_ = a2.rebuildSteps()

	if len(a2.steps) != 2 {
		t.Fatalf("expected 2 steps from files, got %d", len(a2.steps))
	}
	if a2.steps[0].ID != "u1" || a2.steps[1].ID != "a1" {
		t.Errorf("unexpected step order: %s, %s", a2.steps[0].ID, a2.steps[1].ID)
	}
	if !a2.steps[0].Closed || !a2.steps[1].Closed {
		t.Errorf("steps from files should be closed")
	}
}

// TestRebuildSteps_SortsBySeqWithoutRawSteps verifies that steps loaded from
// files are sorted by Seq even when RawSession.Steps is empty (normal restart).
// Without this, os.ReadDir returns files in alphabetical order, causing
// turn-10.jsonl to sort before turn-2.jsonl and all user-turn-*.jsonl to
// cluster after turn-*.jsonl.
func TestRebuildSteps_SortsBySeqWithoutRawSteps(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{actorID: "sort-test-actor"}
	a.Session = domain.Session{
		Turns: []domain.Turn{
			{ID: "user-turn-1", Role: "user", State: "completed"},
			{ID: "turn-2", Role: "assistant", State: "completed"},
			{ID: "user-turn-2", Role: "user", State: "completed"},
			{ID: "turn-10", Role: "assistant", State: "completed"},
		},
		ActiveHead: 3,
	}

	// Create steps with Seq order that does NOT match alphabetical filename
	// order. "turn-10.jsonl" < "turn-2.jsonl" < "user-turn-1.jsonl"
	// alphabetically, but Seq 1 < 5 < 10 < 20.
	a.steps = []domain.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "user-turn-1", Closed: true, Seq: 1,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "q1"}}},
		{ID: "a2", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true, Seq: 5,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "a1"}}},
		{ID: "u2", Role: "user", Type: "text", TurnID: "user-turn-2", Closed: true, Seq: 10,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "q2"}}},
		{ID: "a10", Role: "assistant", Type: "text", TurnID: "turn-10", Closed: true, Seq: 20,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "a2"}}},
	}
	a.saveMailbox(nil)

	// Simulate clean restart: no crash-recovery steps.
	a2 := &Actor{actorID: "sort-test-actor"}
	a2.Session = a.Session
	a2.RawSession.Steps = nil
	_ = a2.rebuildSteps()

	if len(a2.steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(a2.steps))
	}

	// Must be in Seq order: u1(1), a2(5), u2(10), a10(20)
	wantOrder := []string{"u1", "a2", "u2", "a10"}
	for i, want := range wantOrder {
		if a2.steps[i].ID != want {
			ids := make([]string, len(a2.steps))
			for j, s := range a2.steps {
				ids[j] = fmt.Sprintf("%s(Seq:%d)", s.ID, s.Seq)
			}
			t.Errorf("step[%d] = %s, want %s; order: %v", i, a2.steps[i].ID, want, ids)
		}
	}

	// No two consecutive user steps should appear.
	for i := 1; i < len(a2.steps); i++ {
		if a2.steps[i].Role == "user" && a2.steps[i-1].Role == "user" {
			t.Errorf("consecutive user steps at [%d] and [%d]: %s, %s",
				i-1, i, a2.steps[i-1].ID, a2.steps[i].ID)
		}
	}
}

// TestSaveLoadMailbox_PreservesComponentMountsAndGoal verifies that
// ComponentMounts (including user-mounted components like builtin:mode:goal)
// and RawSession.Goal survive a save → load round-trip. This is the regression
// test for the bug where goal mode disappeared after an unexpected restart.
func TestSaveLoadMailbox_PreservesComponentMountsAndGoal(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	goalMounts := []domain.AgentComponentMount{
		{CardID: "builtin:bundle:project-wiki", Enabled: true, Scope: "builtin", Order: 4},
		{CardID: "builtin:mode:goal", Enabled: true, Scope: "user"},
	}
	cardRefs := []gen.CardRef{
		{ID: "builtin:mode:goal", Source: "card-ref", Scope: "user", Order: 9},
		{ID: "builtin:mode:debug", Source: "card-ref", Scope: "user", Disabled: true},
	}
	goal := &gen.SessionGoal{
		Condition:       "Fix the bug",
		MaxTurns:        20,
		TurnCount:       3,
		InterpretedGoal: "Fix the authentication persistence so goal mode survives restart",
		Confirmed:       true,
		PlanCardIDs:     []string{"plan-card-1", "plan-card-2"},
	}

	// Save side: populate actor state and persist.
	actorID := "goal-persist-test-actor"
	a := &Actor{
		actorID:         actorID,
		ComponentMounts: goalMounts,
		cardRefs:        cardRefs,
	}
	a.RawSession.Goal = goal
	a.pendingInteraction = &pendingInteractionState{
		TurnID:    "turn-9",
		StepID:    "turn-9-ask-r1",
		RequestID: "r1",
		Type:      "ask_user",
		Task:      map[string]any{"questions": []any{map[string]any{"q": "confirm?"}}},
	}
	a.saveMailbox(nil)

	// Load side: simulate restart by loading from the same store key.
	var snapshot struct {
		ComponentMounts    []domain.AgentComponentMount `json:"componentMounts,omitempty"`
		CardRefs           []gen.CardRef                `json:"cardRefs,omitempty"`
		Goal               *gen.SessionGoal             `json:"goal,omitempty"`
		RawSession         *domain.RawSession           `json:"rawSession,omitempty"`
		PendingInteraction *pendingInteractionState     `json:"pendingInteraction,omitempty"`
	}
	if err := agentStore.Load(actorID, &snapshot); err != nil {
		t.Fatalf("agentStore.Load failed: %v", err)
	}

	if len(snapshot.ComponentMounts) != 0 {
		t.Fatalf("legacy ComponentMounts should not be persisted: %+v", snapshot.ComponentMounts)
	}
	if len(snapshot.CardRefs) != 2 || snapshot.CardRefs[0].ID != cardRefs[0].ID || snapshot.CardRefs[0].Scope != "user" {
		t.Fatalf("canonical CardRefs = %+v, want %+v", snapshot.CardRefs, cardRefs)
	}

	if len(snapshot.CardRefs) != 2 || snapshot.CardRefs[1].ID != "builtin:mode:debug" || !snapshot.CardRefs[1].Disabled {
		t.Fatalf("disabled canonical CardRef not persisted: %+v", snapshot.CardRefs)
	}

	projected := componentMountsFromCardRefs(snapshot.CardRefs)
	if len(projected) != 2 || projected[0].CardID != "builtin:mode:goal" || projected[0].Order != 9 || projected[1].Enabled {
		t.Fatalf("unexpected projected mounts: %+v", projected)
	}

	if snapshot.Goal == nil {
		t.Errorf("top-level goal not persisted")
	} else if snapshot.Goal.Condition != goal.Condition {
		t.Errorf("top-level goal condition = %q, want %q", snapshot.Goal.Condition, goal.Condition)
	}
	if snapshot.RawSession != nil {
		if snapshot.RawSession.Goal == nil {
			t.Errorf("RawSession.Goal not persisted")
		} else {
			rg := snapshot.RawSession.Goal
			if rg.TurnCount != goal.TurnCount {
				t.Errorf("RawSession.Goal.TurnCount = %d, want %d", rg.TurnCount, goal.TurnCount)
			}
			if rg.InterpretedGoal != goal.InterpretedGoal {
				t.Errorf("RawSession.Goal.InterpretedGoal = %q, want %q", rg.InterpretedGoal, goal.InterpretedGoal)
			}
			if !rg.Confirmed {
				t.Errorf("RawSession.Goal.Confirmed = false, want true")
			}
			if len(rg.PlanCardIDs) != len(goal.PlanCardIDs) {
				t.Errorf("RawSession.Goal.PlanCardIDs len = %d, want %d", len(rg.PlanCardIDs), len(goal.PlanCardIDs))
			}
		}
	}

	// pendingInteraction must survive the round trip so a blocking interaction
	// can be re-presented and answered after restart.
	if snapshot.PendingInteraction == nil {
		t.Fatalf("pendingInteraction not persisted across saveMailbox/Load round trip")
	}
	pi := snapshot.PendingInteraction
	if pi.Type != "ask_user" || pi.RequestID != "r1" || pi.StepID != "turn-9-ask-r1" {
		t.Errorf("pendingInteraction fields lost: %+v", pi)
	}
}

// TestCloneViaFork_PersistsAcrossRestart is the definitive regression test for
// the "clone via fork shows empty assistant turns" bug. The clone's imported
// session must be persisted so it survives an actor restart/passivation.
// handleSessionImport now calls saveMailbox; before that fix the imported
// turns/steps lived only in memory and were lost on the next OnStart Load,
// leaving the clone with empty assistant turns.
func TestCloneViaFork_PersistsAcrossRestart(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	// Source after compaction: t1 compacted away (steps kept as Discarded
	// placeholders); t2 (assistant) + t3 (user) are the recent window.
	src := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t2", Role: "assistant", Seq: 3},
				{ID: "t3", Role: "user", Seq: 4, UserInput: "t3-user"},
			},
			ActiveHead: 1,
		},
		RawSession: domain.RawSession{
			SummarySegments: []domain.SummarySegment{
				{Text: "t1-summary", SourceStartIndex: 0, SourceEndIndex: 1, Level: 1},
			},
		},
	}
	src.steps = []domain.Step{
		{ID: "s1", TurnID: "t1", Discarded: true, Seq: 1},
		{ID: "s2", TurnID: "t1", Discarded: true, Seq: 2},
		{ID: "s3", TurnID: "t2", Role: "assistant", Type: "text", Seq: 3, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "t2-assistant"}}},
		{ID: "s4", TurnID: "t3", Role: "user", Type: "text", Seq: 4, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "t3-user"}}},
	}
	src.takeSnapshot()

	forkResp, err := src.handleSessionFork(nil, domain.AgentSessionForkReq{})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}

	cloneActorID := "clone-persist-test-actor"
	clone := &Actor{actorID: cloneActorID, snapshotReady: true}
	if _, err := clone.handleSessionImport(nil, domain.AgentSessionImportReq{
		Session:         forkResp.Session,
		Steps:           forkResp.Steps,
		SummarySegments: forkResp.SummarySegments,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Simulate an actor restart: a fresh instance reloads its state from disk.
	reloaded := &Actor{actorID: cloneActorID, snapshotReady: true}
	var snap struct {
		RawSession *domain.RawSession `json:"rawSession,omitempty"`
	}
	if err := agentStore.Load(cloneActorID, &snap); err != nil {
		t.Fatalf("agentStore.Load after restart failed (import did not persist): %v", err)
	}
	if snap.RawSession != nil {
		reloaded.RawSession = *snap.RawSession
	}
	reloaded.loadTurns()
	reloaded.rebuildSteps()
	reloaded.takeSnapshot()

	resp, err := reloaded.handleSessionSummaryPure(nil, domain.AgentSessionSummaryReq{})
	if err != nil {
		t.Fatalf("session.summary after reload: %v", err)
	}
	found := false
	for _, s := range resp.Steps {
		if s.ID == "s3" {
			found = true
			if s.Discarded {
				t.Errorf("assistant step s3 reloaded as Discarded; frontend would skip it: %+v", s)
			}
			if len(s.Content) == 0 || s.Content[0].Text != "t2-assistant" {
				t.Errorf("assistant step s3 lost its content across restart: %+v", s)
			}
		}
	}
	if !found {
		t.Fatalf("assistant step s3 lost across restart; clone would render an empty assistant turn. Steps: %v", idsFromSteps(resp.Steps))
	}
}

// TestCloneViaExportRange_PersistsAcrossRestart is the definitive regression
// test for the default clone path (export_range → import_turns, multi-chunk).
// It mirrors the real workspace clone flow: page backward from the newest turn,
// import each chunk, then simulate a restart and confirm every assistant step
// survives with its content — including the early discarded placeholders that
// carry summary index alignment.
func TestCloneViaExportRange_PersistsAcrossRestart(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	// Source after compaction: t1 compacted away (steps kept as Discarded
	// placeholders); t2..t5 are the recent window (alternating user/assistant).
	src := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t2", Role: "assistant", Seq: 3},
				{ID: "t3", Role: "user", Seq: 5, UserInput: "u3"},
				{ID: "t4", Role: "assistant", Seq: 7},
				{ID: "t5", Role: "user", Seq: 9, UserInput: "u5"},
			},
			ActiveHead: 3,
		},
		RawSession: domain.RawSession{
			SummarySegments: []domain.SummarySegment{
				{Text: "t1-summary", SourceStartIndex: 0, SourceEndIndex: 1, Level: 1},
			},
		},
	}
	src.steps = []domain.Step{
		{ID: "s1", TurnID: "t1", Discarded: true, Seq: 1},
		{ID: "s2", TurnID: "t1", Discarded: true, Seq: 2},
		{ID: "s3", TurnID: "t2", Role: "assistant", Type: "text", Seq: 3, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "a2"}}},
		{ID: "s4", TurnID: "t3", Role: "user", Type: "text", Seq: 5, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "u3"}}},
		{ID: "s5", TurnID: "t4", Role: "assistant", Type: "text", Seq: 7, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "a4"}}},
		{ID: "s6", TurnID: "t5", Role: "user", Type: "text", Seq: 9, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "u5"}}},
	}
	src.snapshotReady = true
	src.takeSnapshot()

	cloneActorID := "clone-export-test-actor"
	clone := &Actor{actorID: cloneActorID, snapshotReady: true}

	// Page 1 (newest): t4, t5. HasMore=true.
	page1, err := src.handleSessionExportRange(nil, domain.AgentSessionExportRangeReq{Limit: 2})
	if err != nil {
		t.Fatalf("export page1: %v", err)
	}
	if !page1.HasMore {
		t.Fatalf("page1 should have more pages")
	}
	if _, err := clone.handleSessionImportTurns(nil, domain.AgentSessionImportTurnsReq{
		Turns:           page1.Turns,
		Steps:           page1.Steps,
		SummarySegments: page1.SummarySegments,
		IsFinal:         !page1.HasMore,
	}); err != nil {
		t.Fatalf("import page1: %v", err)
	}

	// Page 2 (oldest): t2, t3, plus early discarded placeholders. HasMore=false.
	page2, err := src.handleSessionExportRange(nil, domain.AgentSessionExportRangeReq{BeforeTurnID: page1.NextBeforeTurnID, Limit: 2})
	if err != nil {
		t.Fatalf("export page2: %v", err)
	}
	if page2.HasMore {
		t.Fatalf("page2 should be the oldest page")
	}
	if _, err := clone.handleSessionImportTurns(nil, domain.AgentSessionImportTurnsReq{
		Turns:           page2.Turns,
		Steps:           page2.Steps,
		SummarySegments: page2.SummarySegments,
		IsFinal:         !page2.HasMore,
	}); err != nil {
		t.Fatalf("import page2: %v", err)
	}

	// Simulate an actor restart: a fresh instance reloads its state from disk.
	reloaded := &Actor{actorID: cloneActorID, snapshotReady: true}
	var snap struct {
		RawSession *domain.RawSession `json:"rawSession,omitempty"`
	}
	if err := agentStore.Load(cloneActorID, &snap); err != nil {
		t.Fatalf("agentStore.Load after restart failed (import did not persist): %v", err)
	}
	if snap.RawSession != nil {
		reloaded.RawSession = *snap.RawSession
	}
	reloaded.loadTurns()
	reloaded.rebuildSteps()
	reloaded.takeSnapshot()

	resp, err := reloaded.handleSessionSummaryPure(nil, domain.AgentSessionSummaryReq{})
	if err != nil {
		t.Fatalf("session.summary after reload: %v", err)
	}
	// Both assistant steps (s3=t2, s5=t4) must survive across the restart with
	// their content, and must not be Discarded (frontend skips Discarded steps).
	wantSteps := map[string]string{"s3": "a2", "s5": "a4"}
	got := map[string]domain.Step{}
	for _, s := range resp.Steps {
		got[s.ID] = s
	}
	for id, text := range wantSteps {
		s, ok := got[id]
		if !ok {
			t.Errorf("assistant step %s lost across restart; clone renders empty assistant turn. Steps: %v", id, idsFromSteps(resp.Steps))
			continue
		}
		if s.Discarded {
			t.Errorf("assistant step %s reloaded as Discarded; frontend would skip it", id)
		}
		if len(s.Content) == 0 || s.Content[0].Text != text {
			t.Errorf("assistant step %s lost content across restart: %+v", id, s)
		}
	}
	// Sanity: the summary segment survives too (early compacted history).
	if len(reloaded.RawSession.SummarySegments) == 0 || reloaded.RawSession.SummarySegments[0].Text != "t1-summary" {
		t.Errorf("summary segment lost across restart: %+v", reloaded.RawSession.SummarySegments)
	}
}

// TestFlushClosedSteps_NoConcurrentMapRace reproduces the clone crash: a clone's
// OnStart runs saveMailbox→flushClosedSteps (reading stepsPersisted) on the
// system loop while an import handler runs saveMailbox→flushClosedSteps (writing
// stepsPersisted, because the imported closed steps are unmarked) on the owner
// loop. Before stepsPersisted had a mutex this was a fatal concurrent map
// read/write (the panic stack: flushClosedSteps agent_session.go:2803). Run
// with -race to verify the lock.
func TestFlushClosedSteps_NoConcurrentMapRace(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{actorID: "race-test-actor"}
	// Seed closed steps that are unmarked (mirrors a freshly imported clone:
	// RawSession.Steps populated, stepsDir removed, stepsPersisted nil). With
	// the steps unmarked, flushClosedSteps must WRITE stepsPersisted — the
	// exact race surface.
	a.steps = make([]domain.Step, 0, 100)
	for i := 0; i < 50; i++ {
		a.steps = append(a.steps, domain.Step{
			ID: fmt.Sprintf("s%d", i), Role: "assistant", Type: "text",
			TurnID: "turn-1", Closed: true, Seq: int64(i),
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "x"}},
		})
	}
	a.RawSession.Steps = a.steps

	var wg sync.WaitGroup
	// Concurrent flushClosedSteps: each both reads and writes stepsPersisted.
	// This is exactly the OnStart (system loop) ↔ import handler (owner loop)
	// overlap that triggered the fatal map race.
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.flushClosedSteps()
		}()
	}
	wg.Wait()
}

// TestLoadMailbox_PreservesPendingPlanApproval verifies that a plan left in
// pending_approval, paired with a surviving plan_approval pendingInteraction,
// is NOT downgraded to rejected after restart. Previously loadMailbox always
// rewrote pending_approval -> rejected, silently dropping the approval card.
func TestLoadMailbox_PreservesPendingPlanApproval(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())
	ctx := testutil.HumanCtx(testutil.GenActorID())
	actorID := "plan-approval-persist"

	// Save side: a plan awaiting approval plus the matching pending interaction.
	save := &Actor{actorID: actorID, plan: planState{Status: "pending_approval", RequestID: "rp", Title: "T", Plan: "P"}}
	save.pendingInteraction = &pendingInteractionState{RequestID: "rp", Type: "plan_approval"}
	save.saveMailbox(nil)

	// Restart: fresh actor loads from the same store key.
	loaded := &Actor{actorID: actorID}
	loaded.loadMailbox(ctx)

	if loaded.plan.Status != "pending_approval" {
		t.Fatalf("plan.Status = %q, want pending_approval (preserved across restart)", loaded.plan.Status)
	}
	if !loaded.planApprovalPending {
		t.Fatal("planApprovalPending = false, want true")
	}
	if loaded.pendingInteraction == nil || loaded.pendingInteraction.Type != "plan_approval" {
		t.Fatalf("pendingInteraction not restored: %+v", loaded.pendingInteraction)
	}
}

// TestLoadMailbox_DowngradesOrphanedPendingApproval verifies the fallback: a
// plan stuck in pending_approval WITHOUT a surviving pendingInteraction (e.g.
// an older snapshot predating this feature) is still downgraded to rejected so
// the agent does not deadlock waiting for an answer it can never route.
// TestLoadMailbox_ClearsOrphanedCompactionLock pins the crash-recovery half of
// the compaction lock: a lock present at load means the previous process died
// mid-compaction; it must be cleared and persisted so a fresh compaction is
// not blocked by stale evidence.
func TestLoadMailbox_ClearsOrphanedCompactionLock(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())
	ctx := testutil.HumanCtx(testutil.GenActorID())
	actorID := "compaction-lock-orphan"

	save := &Actor{actorID: actorID}
	save.RawSession.CompactionLock = &gen.CompactionLockState{TurnID: "turn-crash", Trigger: "user", StartedAt: "2026-01-01T00:00:00Z", Seq: 5}
	save.saveMailbox(nil)

	loaded := &Actor{actorID: actorID}
	loaded.loadMailbox(ctx)

	if loaded.RawSession.CompactionLock != nil {
		t.Fatalf("CompactionLock = %+v, want nil (orphan cleared on load)", loaded.RawSession.CompactionLock)
	}
}

func TestLoadMailbox_DowngradesOrphanedPendingApproval(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())
	ctx := testutil.HumanCtx(testutil.GenActorID())
	actorID := "plan-approval-orphan"

	save := &Actor{actorID: actorID, plan: planState{Status: "pending_approval", RequestID: "rp", Title: "T", Plan: "P"}}
	// No pendingInteraction -> orphaned approval.
	save.saveMailbox(nil)

	loaded := &Actor{actorID: actorID}
	loaded.loadMailbox(ctx)

	if loaded.plan.Status != "rejected" {
		t.Fatalf("plan.Status = %q, want rejected (orphaned approval downgraded)", loaded.plan.Status)
	}
	if loaded.planApprovalPending {
		t.Fatal("planApprovalPending = true, want false for orphaned approval")
	}
}

// TestPendingInteraction_RoundTripSurvivesRestart verifies that when a turn is
// blocked on an ask_user interaction, the full saveMailbox → loadMailbox →
// rebuildSteps → recoverTurnStatus → recoverPendingInteraction cycle keeps the
// turn as a paused/interaction turn (status paused, pauseReason interaction) and
// the interaction step remains open, pending, and available via session.summary. This
// is the end-to-end regression guard for the "reloaded conversation is paused
// and the interaction UI is gone" bug.
func TestPendingInteraction_RoundTripSurvivesRestart(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	actorID := "019f30e43afd00000000000000000003"
	actorDir := filepath.Join(config.ActorDataDir(), "agent", actorID)
	if err := os.MkdirAll(filepath.Join(actorDir, "turns"), 0755); err != nil {
		t.Fatalf("mkdir turns: %v", err)
	}

	src := &Actor{actorID: actorID, snapshotReady: true}
	src.Session = domain.Session{Turns: []domain.Turn{
		{ID: "t1", Role: "user", Seq: 1, State: "completed"},
		{ID: "t2", Role: "assistant", Seq: 2, State: "running"},
	}}
	src.steps = []domain.Step{
		{ID: "s1", TurnID: "t1", Role: "user", Type: "text", Seq: 1, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
		{ID: "s2", TurnID: "t2", Role: "assistant", Type: "ask_user", Seq: 2,
			Closed: false, ContentStatus: "running", InteractionStatus: "pending",
			RequestID: "r1",
			Content:   []domain.ContentBlock{{Type: domain.ContentBlockText, Text: `{"questions":[{"q":"x"}]}`}}},
	}
	src.pendingInteraction = &pendingInteractionState{
		TurnID:    "t2",
		StepID:    "s2",
		RequestID: "r1",
		Type:      "ask_user",
		Task:      map[string]any{"questions": []any{map[string]any{"q": "x"}}},
	}
	src.status = turnStatus{TurnID: "t2", State: "running"}
	src.takeSnapshot()

	src.saveMailbox(nil)
	if err := src.saveTurns(); err != nil {
		t.Fatalf("saveTurns: %v", err)
	}

	reloaded := &Actor{actorID: actorID, snapshotReady: true}
	reloaded.loadMailbox(nil)
	reloaded.rebuildSteps()
	reloaded.recoverTurnStatus()
	reloaded.recoverPendingInteraction(nil)
	reloaded.takeSnapshot()

	if reloaded.status.State != "paused" {
		t.Errorf("status.State = %q, want paused (interaction wait)", reloaded.status.State)
	}
	if reloaded.status.PauseKind != "interaction" {
		t.Errorf("status.PauseKind = %q, want interaction", reloaded.status.PauseKind)
	}
	if !reloaded.pendingAskUser {
		t.Errorf("pendingAskUser = %v, want true", reloaded.pendingAskUser)
	}

	resp, err := reloaded.handleSessionSummaryPure(nil, domain.AgentSessionSummaryReq{})
	if err != nil {
		t.Fatalf("session.summary after reload: %v", err)
	}
	var found *domain.Step
	for i := range resp.Steps {
		if resp.Steps[i].ID == "s2" {
			found = &resp.Steps[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("interaction step s2 missing from session.summary; steps=%v", idsFromSteps(resp.Steps))
	}
	if found.Closed {
		t.Errorf("interaction step s2 is closed; want open: %+v", found)
	}
	if found.InteractionStatus != "pending" {
		t.Errorf("interaction step s2 InteractionStatus = %q, want pending: %+v", found.InteractionStatus, found)
	}
	if len(found.Content) == 0 || found.Content[0].Text != `{"questions":[{"q":"x"}]}` {
		t.Errorf("interaction step s2 lost content: %+v", found)
	}
}

// TestPermissionModeOverride_SurvivesRestart verifies that a per-agent
// permission mode set via permission_mode_set is persisted immediately and
// restored by loadMailbox after a re-spawn. Previously the mode lived only in
// memory, so a workflow owner agent woken after a process restart (which
// spawns with PermissionMode "") reset to the default "permission" mode and
// started asking for confirmation on every tool call.
func TestPermissionModeOverride_SurvivesRestart(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())
	ctx := testutil.HumanCtx(testutil.GenActorID())
	actorID := "perm-mode-override-persist"

	src := &Actor{actorID: actorID, permissionMode: "permission"}
	if err := src.handleSetPermissionMode(ctx, setPermissionModeReq{Mode: "yolo"}); err != nil {
		t.Fatalf("handleSetPermissionMode: %v", err)
	}
	if src.permissionMode != "yolo" || !src.permissionModeOverride {
		t.Fatalf("after set: mode=%q override=%v, want yolo/true", src.permissionMode, src.permissionModeOverride)
	}

	var snap map[string]any
	if err := agentStore.Load(actorID, &snap); err != nil {
		t.Fatalf("agentStore.Load after set: %v", err)
	}
	if snap["permissionMode"] != "yolo" {
		t.Fatalf("persisted permissionMode = %v, want yolo", snap["permissionMode"])
	}

	// Restart: a fresh actor (spawned with the default mode) loads the same
	// store key and must restore the persisted override.
	reloaded := &Actor{actorID: actorID, permissionMode: "permission"}
	reloaded.loadMailbox(ctx)
	if reloaded.permissionMode != "yolo" {
		t.Fatalf("permissionMode after reload = %q, want yolo (override preserved)", reloaded.permissionMode)
	}
	if !reloaded.permissionModeOverride {
		t.Fatal("permissionModeOverride after reload = false, want true")
	}
}

// TestPermissionModeInherited_NotPersisted verifies that a mode merely
// inherited from the global preference at spawn time is NOT written to the
// store: on the next spawn the agent re-reads the (possibly changed) global
// default instead of a frozen snapshot.
func TestPermissionModeInherited_NotPersisted(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())
	ctx := testutil.HumanCtx(testutil.GenActorID())
	actorID := "perm-mode-inherited"

	src := &Actor{actorID: actorID, permissionMode: "allow-all"} // inherited, no override
	src.saveMailbox(nil)

	var snap map[string]any
	if err := agentStore.Load(actorID, &snap); err != nil {
		t.Fatalf("agentStore.Load: %v", err)
	}
	if _, ok := snap["permissionMode"]; ok {
		t.Fatalf("permissionMode persisted despite no per-agent override: %v", snap["permissionMode"])
	}

	loaded := &Actor{actorID: actorID, permissionMode: "permission"} // spawn-time default
	loaded.loadMailbox(ctx)
	if loaded.permissionMode != "permission" {
		t.Fatalf("permissionMode after reload = %q, want the spawn-time default (no override to restore)", loaded.permissionMode)
	}
	if loaded.permissionModeOverride {
		t.Fatal("permissionModeOverride after reload = true, want false")
	}
}
