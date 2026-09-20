package agent

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// helper to count step events of a given Kind in e.stepEvents.
func countStepEventsByKind(events []domain.StepEvent, kind string) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

// findStepEventsByKind returns all buffered step events matching kind.
func findStepEventsByKind(events []domain.StepEvent, kind string) []domain.StepEvent {
	var out []domain.StepEvent
	for _, ev := range events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// TestProcessBatchResults_EmitsFileChangesEventOnSuccessfulEdit verifies that a
// successful tool result carrying fileChanges emits a step.file_changes event
// with the correct StepID, TurnID, and FileChanges payload, and that the same
// changes are merged into the turn-level accumulator.
func TestProcessBatchResults_EmitsFileChangesEventOnSuccessfulEdit(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.openToolCalls = 1

	changes := []domain.TurnFileChange{
		{Path: "web/src/app.tsx", Additions: 12, Deletions: 3, DiffContent: "@@ ... @@"},
	}
	results := []toolExecutionResult{
		{
			call: pendingToolCall{
				ID:          "tu-1",
				CallableID:  "project.edit",
				Input:       `{"path":"web/src/app.tsx"}`,
				ServiceName: "project",
			},
			out:         `{"Hunks":[...]}`,
			isErr:       false,
			fileChanges: changes,
		},
	}
	batchSteps := []domain.TurnAction{
		{ID: "step-1", Kind: string(domain.TurnActionToolCall), State: "running", CallableID: "project.edit"},
	}

	if err := e.processBatchResults(ctx, "turn-1", results, batchSteps); err != nil {
		t.Fatalf("processBatchResults returned error: %v", err)
	}

	fileChangeEvents := findStepEventsByKind(e.stepEvents, "step.file_changes")
	if len(fileChangeEvents) != 1 {
		t.Fatalf("expected 1 step.file_changes event, got %d (all events: %+v)", len(fileChangeEvents), e.stepEvents)
	}
	ev := fileChangeEvents[0]
	if ev.StepID != "step-1" {
		t.Errorf("StepID = %q, want step-1", ev.StepID)
	}
	if ev.TurnID != "turn-1" {
		t.Errorf("TurnID = %q, want turn-1", ev.TurnID)
	}
	if len(ev.FileChanges) != 1 {
		t.Fatalf("FileChanges length = %d, want 1", len(ev.FileChanges))
	}
	fc := ev.FileChanges[0]
	if fc.Path != "web/src/app.tsx" {
		t.Errorf("FileChanges[0].Path = %q, want web/src/app.tsx", fc.Path)
	}
	if fc.Additions != 12 {
		t.Errorf("FileChanges[0].Additions = %d, want 12", fc.Additions)
	}
	if fc.Deletions != 3 {
		t.Errorf("FileChanges[0].Deletions = %d, want 3", fc.Deletions)
	}
	if fc.DiffContent != "@@ ... @@" {
		t.Errorf("FileChanges[0].DiffContent = %q, want '@@ ... @@'", fc.DiffContent)
	}
	if ev.EventSeq == 0 {
		t.Error("EventSeq should be assigned (non-zero) by emitStepEvent")
	}

	if len(e.fileChanges) != 1 || e.fileChanges[0].Path != "web/src/app.tsx" {
		t.Errorf("turn-level fileChanges accumulator not updated: %+v", e.fileChanges)
	}
}

// TestProcessBatchResults_NoEmitWhenFileChangesEmpty verifies that a successful
// tool result without fileChanges does NOT emit step.file_changes (e.g. shell
// commands, file_read, ls, grep, etc.).
func TestProcessBatchResults_NoEmitWhenFileChangesEmpty(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.openToolCalls = 1

	results := []toolExecutionResult{
		{
			call: pendingToolCall{
				ID:          "tu-1",
				CallableID:  "project.read",
				Input:       `{"path":"README.md"}`,
				ServiceName: "project",
			},
			out:         "file contents",
			isErr:       false,
			fileChanges: nil,
		},
	}
	batchSteps := []domain.TurnAction{
		{ID: "step-1", Kind: string(domain.TurnActionToolCall), State: "running", CallableID: "project.read"},
	}

	if err := e.processBatchResults(ctx, "turn-1", results, batchSteps); err != nil {
		t.Fatalf("processBatchResults returned error: %v", err)
	}

	if got := countStepEventsByKind(e.stepEvents, "step.file_changes"); got != 0 {
		t.Fatalf("expected 0 step.file_changes events, got %d", got)
	}
}

// TestProcessBatchResults_NoEmitOnToolFailure verifies that an errored tool
// result (isErr=true) carrying fileChanges still does NOT emit step.file_changes.
// File changes only land on success — a failed file_edit has no disk effect.
func TestProcessBatchResults_NoEmitOnToolFailure(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.openToolCalls = 1

	results := []toolExecutionResult{
		{
			call: pendingToolCall{
				ID:          "tu-1",
				CallableID:  "project.edit",
				Input:       `{"path":"web/src/app.tsx"}`,
				ServiceName: "project",
			},
			out:         "permission denied",
			isErr:       true,
			fileChanges: []domain.TurnFileChange{{Path: "web/src/app.tsx", Additions: 5, Deletions: 0}},
		},
	}
	batchSteps := []domain.TurnAction{
		{ID: "step-1", Kind: string(domain.TurnActionToolCall), State: "running", CallableID: "project.edit"},
	}

	if err := e.processBatchResults(ctx, "turn-1", results, batchSteps); err != nil {
		t.Fatalf("processBatchResults returned error: %v", err)
	}

	if got := countStepEventsByKind(e.stepEvents, "step.file_changes"); got != 0 {
		t.Fatalf("expected 0 step.file_changes events on tool failure, got %d", got)
	}
	if len(e.fileChanges) != 0 {
		t.Errorf("turn-level fileChanges should be empty on failure, got %+v", e.fileChanges)
	}
}

// TestProcessBatchResults_DistinctPathsProduceDistinctEvents verifies that two
// successful tool calls in the same batch (e.g. file_write + file_rm) each
// emit their own step.file_changes event with distinct paths.
func TestProcessBatchResults_DistinctPathsProduceDistinctEvents(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.openToolCalls = 2

	results := []toolExecutionResult{
		{
			call: pendingToolCall{
				ID:          "tu-1",
				CallableID:  "project.write",
				Input:       `{"path":"web/src/new.ts"}`,
				ServiceName: "project",
			},
			out:         `{}`,
			isErr:       false,
			fileChanges: []domain.TurnFileChange{{Path: "web/src/new.ts", Additions: 50, Deletions: 0, DiffContent: "+new file"}},
		},
		{
			call: pendingToolCall{
				ID:          "tu-2",
				CallableID:  "project.rm",
				Input:       `{"path":"web/src/old.ts"}`,
				ServiceName: "project",
			},
			out:         `{"Removed":["web/src/old.ts"]}`,
			isErr:       false,
			fileChanges: []domain.TurnFileChange{{Path: "web/src/old.ts", Additions: 0, Deletions: 30, DiffContent: "-deleted"}},
		},
	}
	batchSteps := []domain.TurnAction{
		{ID: "step-1", Kind: string(domain.TurnActionToolCall), State: "running", CallableID: "project.write"},
		{ID: "step-2", Kind: string(domain.TurnActionToolCall), State: "running", CallableID: "project.rm"},
	}

	if err := e.processBatchResults(ctx, "turn-1", results, batchSteps); err != nil {
		t.Fatalf("processBatchResults returned error: %v", err)
	}

	events := findStepEventsByKind(e.stepEvents, "step.file_changes")
	if len(events) != 2 {
		t.Fatalf("expected 2 step.file_changes events, got %d", len(events))
	}

	byStep := map[string]domain.StepEvent{}
	for _, ev := range events {
		byStep[ev.StepID] = ev
	}
	if _, ok := byStep["step-1"]; !ok {
		t.Errorf("missing step.file_changes event for step-1: %+v", byStep)
	}
	if _, ok := byStep["step-2"]; !ok {
		t.Errorf("missing step.file_changes event for step-2: %+v", byStep)
	}

	if byStep["step-1"].FileChanges[0].Path != "web/src/new.ts" {
		t.Errorf("step-1 path = %q, want web/src/new.ts", byStep["step-1"].FileChanges[0].Path)
	}
	if byStep["step-2"].FileChanges[0].Path != "web/src/old.ts" {
		t.Errorf("step-2 path = %q, want web/src/old.ts", byStep["step-2"].FileChanges[0].Path)
	}

	if len(e.fileChanges) != 2 {
		t.Errorf("turn-level accumulator should have 2 entries, got %d", len(e.fileChanges))
	}
}

// TestProcessBatchResults_FileRmRecursiveEmitsAllEntries verifies that a single
// recursive file_rm producing N TurnFileChange entries (one per removed file)
// emits them all in a single step.file_changes event.
func TestProcessBatchResults_FileRmRecursiveEmitsAllEntries(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.openToolCalls = 1

	recursiveChanges := []domain.TurnFileChange{
		{Path: "build/a.txt", Additions: 0, Deletions: 10},
		{Path: "build/b.txt", Additions: 0, Deletions: 5},
		{Path: "build/sub/c.txt", Additions: 0, Deletions: 20},
	}
	results := []toolExecutionResult{
		{
			call: pendingToolCall{
				ID:          "tu-1",
				CallableID:  "project.rm",
				Input:       `{"path":"build","recursive":true}`,
				ServiceName: "project",
			},
			out:         `{"Removed":["build/a.txt","build/b.txt","build/sub/c.txt"]}`,
			isErr:       false,
			fileChanges: recursiveChanges,
		},
	}
	batchSteps := []domain.TurnAction{
		{ID: "step-1", Kind: string(domain.TurnActionToolCall), State: "running", CallableID: "project.rm"},
	}

	if err := e.processBatchResults(ctx, "turn-1", results, batchSteps); err != nil {
		t.Fatalf("processBatchResults returned error: %v", err)
	}

	events := findStepEventsByKind(e.stepEvents, "step.file_changes")
	if len(events) != 1 {
		t.Fatalf("expected 1 step.file_changes event, got %d", len(events))
	}
	if len(events[0].FileChanges) != 3 {
		t.Fatalf("expected 3 FileChanges entries, got %d", len(events[0].FileChanges))
	}
	paths := map[string]bool{}
	for _, fc := range events[0].FileChanges {
		paths[fc.Path] = true
	}
	for _, want := range []string{"build/a.txt", "build/b.txt", "build/sub/c.txt"} {
		if !paths[want] {
			t.Errorf("missing path %q in event FileChanges: %+v", want, events[0].FileChanges)
		}
	}
}

// TestProcessBatchResults_SamePathTwiceEmitsTwoEvents verifies that two
// successful edits to the same path emit two distinct step.file_changes events
// (the frontend merges by path last-write-wins; the backend must emit both so
// the live timeline sees the intermediate state).
func TestProcessBatchResults_SamePathTwiceEmitsTwoEvents(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.openToolCalls = 2

	results := []toolExecutionResult{
		{
			call: pendingToolCall{
				ID:          "tu-1",
				CallableID:  "project.edit",
				Input:       `{"path":"README.md"}`,
				ServiceName: "project",
			},
			out:         `{}`,
			isErr:       false,
			fileChanges: []domain.TurnFileChange{{Path: "README.md", Additions: 2, Deletions: 0, DiffContent: "v1"}},
		},
		{
			call: pendingToolCall{
				ID:          "tu-2",
				CallableID:  "project.edit",
				Input:       `{"path":"README.md"}`,
				ServiceName: "project",
			},
			out:         `{}`,
			isErr:       false,
			fileChanges: []domain.TurnFileChange{{Path: "README.md", Additions: 0, Deletions: 1, DiffContent: "v2"}},
		},
	}
	batchSteps := []domain.TurnAction{
		{ID: "step-1", Kind: string(domain.TurnActionToolCall), State: "running", CallableID: "project.edit"},
		{ID: "step-2", Kind: string(domain.TurnActionToolCall), State: "running", CallableID: "project.edit"},
	}

	if err := e.processBatchResults(ctx, "turn-1", results, batchSteps); err != nil {
		t.Fatalf("processBatchResults returned error: %v", err)
	}

	events := findStepEventsByKind(e.stepEvents, "step.file_changes")
	if len(events) != 2 {
		t.Fatalf("expected 2 step.file_changes events for 2 edits, got %d", len(events))
	}
	if events[0].EventSeq >= events[1].EventSeq {
		t.Errorf("EventSeq should be monotonically increasing: seq0=%d seq1=%d", events[0].EventSeq, events[1].EventSeq)
	}
	// Turn-level accumulator collapses to one entry by path (last-write-wins).
	if len(e.fileChanges) != 1 {
		t.Errorf("turn accumulator should have 1 merged entry, got %d: %+v", len(e.fileChanges), e.fileChanges)
	}
	if e.fileChanges[0].DiffContent != "v2" {
		t.Errorf("turn accumulator should hold last write, got DiffContent=%q", e.fileChanges[0].DiffContent)
	}
}

// TestProcessBatchResults_ToolFailureDiagnosticCarriesFullToolcallMetadata
// verifies the onReportDiagnostic callback receives TargetService/ToolUseId/Input/Output
// when a tool call fails. This is what populates the Oracle Problems panel for
// tool_call diagnostics — without these fields the panel only shows Message.
func TestProcessBatchResults_ToolFailureDiagnosticCarriesFullToolcallMetadata(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.openToolCalls = 1

	var gotDiag domain.OracleReportDiagnosticReq
	var diagCalls int
	e.onReportDiagnostic = func(_ actor.Context, req domain.OracleReportDiagnosticReq) {
		gotDiag = req
		diagCalls++
	}

	results := []toolExecutionResult{
		{
			call: pendingToolCall{
				ID:          "tu-toolcall-1",
				CallableID:  "project.edit",
				Input:       `{"path":"web/src/app.tsx","content":"x"}`,
				ServiceName: "project",
				LLMName:     "file_edit",
				RawToolCall: `{"id":"tu-toolcall-1","type":"function","function":{"name":"file_edit"}}`,
			},
			out:   "permission denied: cannot write outside project root",
			isErr: true,
		},
	}
	batchSteps := []domain.TurnAction{
		{ID: "step-toolcall-1", Kind: string(domain.TurnActionToolCall), State: "running", CallableID: "project.edit"},
	}

	if err := e.processBatchResults(ctx, "turn-toolcall-1", results, batchSteps); err != nil {
		t.Fatalf("processBatchResults returned error: %v", err)
	}

	if diagCalls != 1 {
		t.Fatalf("expected exactly 1 onReportDiagnostic call, got %d", diagCalls)
	}

	// Source must be "tool_call" so the Problems panel routes the entry correctly.
	if gotDiag.Source != "tool_call" {
		t.Errorf("Source = %q, want \"tool_call\"", gotDiag.Source)
	}
	if gotDiag.CallableID != "project.edit" {
		t.Errorf("CallableID = %q, want \"project.edit\"", gotDiag.CallableID)
	}
	if gotDiag.TargetService != "project" {
		t.Errorf("TargetService = %q, want \"project\" (was previously dropped)", gotDiag.TargetService)
	}
	if gotDiag.ToolUseID != "tu-toolcall-1" {
		t.Errorf("ToolUseID = %q, want \"tu-toolcall-1\" (was previously dropped)", gotDiag.ToolUseID)
	}
	if gotDiag.Input != `{"path":"web/src/app.tsx","content":"x"}` {
		t.Errorf("Input = %q, want the tool call input JSON (was previously dropped)", gotDiag.Input)
	}
	if gotDiag.Output != "permission denied: cannot write outside project root" {
		t.Errorf("Output = %q, want the failure message (was previously dropped)", gotDiag.Output)
	}
	if gotDiag.StepID != "step-toolcall-1" {
		t.Errorf("StepID = %q, want \"step-toolcall-1\"", gotDiag.StepID)
	}
	if gotDiag.TurnID != "turn-toolcall-1" {
		t.Errorf("TurnID = %q, want \"turn-toolcall-1\"", gotDiag.TurnID)
	}
}

// TestProcessBatchResults_ToolFailureDiagnosticTruncatesLargePayload verifies
// that very large Input/Output strings are truncated by truncateForDiagnostic
// before reaching the Oracle ring (which holds at most maxDiagnostics entries
// in memory). Without this cap, a single big file_read failure could OOM the
// oracle actor.
func TestProcessBatchResults_ToolFailureDiagnosticTruncatesLargePayload(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.openToolCalls = 1

	var gotDiag domain.OracleReportDiagnosticReq
	e.onReportDiagnostic = func(_ actor.Context, req domain.OracleReportDiagnosticReq) {
		gotDiag = req
	}

	// 100 KB input/output — well past truncateForDiagnostic's 2 KB cap.
	huge := "x"
	for i := 0; i < 17; i++ {
		huge = huge + huge
	}
	results := []toolExecutionResult{
		{
			call: pendingToolCall{
				ID:          "tu-big",
				CallableID:  "project.read",
				Input:       `{"path":"` + huge[:200] + `"}`,
				RawToolCall: `{"id":"tu-big","type":"function"}`,
			},
			out:   huge,
			isErr: true,
		},
	}
	batchSteps := []domain.TurnAction{
		{ID: "step-big", Kind: string(domain.TurnActionToolCall), State: "running", CallableID: "project.read"},
	}

	if err := e.processBatchResults(ctx, "turn-1", results, batchSteps); err != nil {
		t.Fatalf("processBatchResults returned error: %v", err)
	}

	if len(gotDiag.Output) > 2100 {
		t.Errorf("Output should be truncated to ~2 KB, got %d bytes", len(gotDiag.Output))
	}
	if len(gotDiag.Input) > 2100 {
		t.Errorf("Input should be truncated to ~2 KB, got %d bytes", len(gotDiag.Input))
	}
}
