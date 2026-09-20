package workbench

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestScenario_CoordinatorControlsBoard simulates the coordinator (the
// workbench controller agent, mounting builtin:bundle:workbench-attention)
// driving the board through a real user beat. Each numbered step issues the
// exact callable the coordinator would issue, in order, and asserts the
// awareness + control surface behaves as the bundle card promises.
//
//	用户在工作台对 coordinator 说："部署跑完了，别让终端刷屏——聚焦在
//	Notes 上，我只想看这个。"
func TestScenario_CoordinatorControlsBoard(t *testing.T) {
	a := &Actor{cards: map[string]*cardState{}}

	// --- Stage 0: the board fills up on its own (system activity). ---
	if err := a.handleIngestLifecycle(nil, gen.WorkbenchLifecycleIngestReq{
		Event: gen.AppLifecycleEvent{Kind: "running", ID: "notes", State: "running"},
	}); err != nil {
		t.Fatalf("lifecycle notes: %v", err)
	}
	if err := a.handleIngestLifecycle(nil, gen.WorkbenchLifecycleIngestReq{
		Event: gen.AppLifecycleEvent{Kind: "running", ID: "deploy-log", State: "running"},
	}); err != nil {
		t.Fatalf("lifecycle deploy-log: %v", err)
	}
	for _, step := range []string{"go build ./...", "deploy: pushing image", "deploy: done"} {
		if err := a.handleIngestStep(nil, gen.WorkbenchStepIngestReq{
			EmitterID: "agent-coder", Step: gen.StepEvent{Kind: "block.appended", Delta: step},
		}); err != nil {
			t.Fatalf("step ingest: %v", err)
		}
	}
	if err := a.handleIngestTurn(nil, gen.WorkbenchTurnIngestReq{EmitterID: "agent-coder", Turn: gen.TurnEvent{}}); err != nil {
		t.Fatalf("turn ingest: %v", err)
	}

	// --- Stage 1: awareness. The coordinator reads attention_report first. ---
	report, err := a.handleAttentionReport(nil, gen.WorkbenchAttentionReportReq{LimitEvents: 6})
	if err != nil {
		t.Fatalf("attention report: %v", err)
	}
	if len(report.Cards) == 0 {
		t.Fatal("report must carry the board")
	}
	if report.Frozen || report.Maximized != "" {
		t.Fatalf("board should be live and un-captured, got frozen=%v maximized=%q", report.Frozen, report.Maximized)
	}
	sawDeploy := false
	for _, ev := range report.Recent {
		if ev.Kind == attentionKindStep && strings.Contains(ev.Summary, "deploy") {
			sawDeploy = true
		}
	}
	if !sawDeploy {
		t.Fatalf("coordinator must see the deploy steps in the ring: %+v", report.Recent)
	}

	// --- Stage 2: control. Quiet the deploy-log card, focus Notes. ---
	if _, err := a.handleSetHidden(nil, gen.WorkbenchSetHiddenReq{ID: appCardPrefix + "deploy-log", Hidden: true}); err != nil {
		t.Fatalf("hide deploy-log: %v", err)
	}
	focusSnap, err := a.handleSetMaximized(nil, gen.WorkbenchSetMaximizedReq{
		ID:     appCardPrefix + "notes",
		Reason: "用户只想看 Notes",
	})
	if err != nil {
		t.Fatalf("capture notes: %v", err)
	}
	if focusSnap.Maximized != appCardPrefix+"notes" {
		t.Fatalf("capture must take the whole field: maximized=%q", focusSnap.Maximized)
	}
	// Unknown card capture is rejected — a hallucinated id must not blank the board.
	if _, err := a.handleSetMaximized(nil, gen.WorkbenchSetMaximizedReq{ID: appCardPrefix + "ghost"}); err == nil {
		t.Fatal("capture of unknown card must fail")
	}

	// --- Stage 3: re-awareness. The coordinator verifies its own changes. ---
	report, err = a.handleAttentionReport(nil, gen.WorkbenchAttentionReportReq{})
	if err != nil {
		t.Fatalf("second attention report: %v", err)
	}
	if report.Maximized != appCardPrefix+"notes" {
		t.Fatalf("report maximized = %q, want notes", report.Maximized)
	}
	deployHidden := false
	for _, c := range report.Cards {
		if c.ID == appCardPrefix+"deploy-log" && c.Slot == slotHidden {
			deployHidden = true
		}
	}
	if !deployHidden {
		t.Fatal("deploy-log must be retired after set_hidden")
	}
	// Newest ring entries: the capture (with reason) then the hide.
	if len(report.Recent) < 2 ||
		!strings.Contains(report.Recent[0].Summary, "capture "+appCardPrefix+"notes") ||
		!strings.Contains(report.Recent[1].Summary, "hide "+appCardPrefix+"deploy-log") {
		t.Fatalf("ring should record the control ops newest-first: %+v", report.Recent[:min(2, len(report.Recent))])
	}

	// --- Stage 4: the user asks for stillness → freeze, then leave. ---
	if _, err := a.handleSetFrozen(nil, gen.WorkbenchSetFrozenReq{Frozen: true}); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if _, err := a.handleSetMaximized(nil, gen.WorkbenchSetMaximizedReq{ID: ""}); err != nil {
		t.Fatalf("release capture: %v", err)
	}
	// Score churn under freeze: the projection still updates, frozen flag stays.
	if err := a.handleIngestStep(nil, gen.WorkbenchStepIngestReq{
		EmitterID: "agent-coder", Step: gen.StepEvent{Kind: "block.appended", Delta: "post-deploy cleanup"},
	}); err != nil {
		t.Fatalf("step under freeze: %v", err)
	}
	snap, err := a.handleSnapshot(nil, gen.WorkbenchSnapshotReq{})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !snap.Frozen || snap.Maximized != "" {
		t.Fatalf("board must stay frozen and released: frozen=%v maximized=%q", snap.Frozen, snap.Maximized)
	}

	// --- Stage 5: unfreeze returns control to the scores. ---
	if _, err := a.handleSetFrozen(nil, gen.WorkbenchSetFrozenReq{Frozen: false}); err != nil {
		t.Fatalf("unfreeze: %v", err)
	}
	snap, _ = a.handleSnapshot(nil, gen.WorkbenchSnapshotReq{})
	if snap.Frozen {
		t.Fatal("board must unfreeze")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Compile-time guards: the layout-mode handlers keep the stateful lane shape
// (they mutate board state under the score lane at runtime).
var (
	_ func(actor.Context, gen.WorkbenchSetFrozenReq) (gen.WorkbenchSnapshot, error)    = (*Actor)(nil).handleSetFrozen
	_ func(actor.Context, gen.WorkbenchSetMaximizedReq) (gen.WorkbenchSnapshot, error) = (*Actor)(nil).handleSetMaximized
)
