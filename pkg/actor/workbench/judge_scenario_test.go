package workbench

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestScenario_JudgeMixedScoring pins the mixed-scoring acceptance contract:
// judge (小脑) joins lifecycle / step / turn / user as a fourth score source,
// the user always dominates, and judge outages are invisible on the board.
//
//	用户正在看 Notes（刚 promote 过），coder agent 在刷部署日志；小脑每轮
//	给日志卡"值得关注"，给 Notes"紧急"也无妨——但布局不得换位；小脑挂了
//	（无答案）时面板行为与从前完全一致。
func TestScenario_JudgeMixedScoring(t *testing.T) {
	a := newJudgeTestActor(t)

	// --- Stage 0: board fills on real events (lifecycle + step). ---
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
	if err := a.handleIngestStep(nil, gen.WorkbenchStepIngestReq{
		EmitterID: "agent-coder",
		Step:      gen.StepEvent{Kind: "block.appended", Delta: "deploy: pushing image"},
	}); err != nil {
		t.Fatalf("step ingest: %v", err)
	}

	// --- Stage 1: the user promotes Notes — user intent, the strongest source. ---
	if _, err := a.handlePromote(nil, gen.WorkbenchCardRefReq{ID: "app:notes"}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	notes := a.cards["app:notes"]
	log := a.cards["app:deploy-log"]
	promotedScore := notes.score
	if promotedScore <= log.score {
		t.Fatalf("promoted notes = %v must lead log = %v", promotedScore, log.score)
	}

	// --- Stage 2: a judge round max-bumps the challenger (urgent, jev). ---
	mustIngest(t, a, gen.WorkbenchJudgeIngestReq{Scores: map[string]gen.WorkbenchJudgeScore{
		"app:deploy-log": {Level: 3, Backend: "jev", Calibrated: true},
		"app:notes":      {Level: 2, Backend: "jev", Calibrated: true},
	}})
	got := orderedIDs(a.cards, nil)
	if got[0] != "app:notes" {
		t.Fatalf("user-promoted card displaced by judge: order = %v (notes=%v log=%v)",
			got, notes.score, log.score)
	}
	if log.why == "" || log.why[:len("小脑")] != "小脑" {
		t.Fatalf("log.why = %q, want judge provenance", log.why)
	}

	// --- Stage 3: judge outage — policy returns nothing. Board unchanged. ---
	before := log.score
	mustIngest(t, a, gen.WorkbenchJudgeIngestReq{Scores: map[string]gen.WorkbenchJudgeScore{}})
	if log.score != before-6 { // overlay removed, pre-judge score restored
		t.Fatalf("outage round must restore pre-judge score: %v → %v", before, log.score)
	}
	got = orderedIDs(a.cards, nil)
	if got[0] != "app:notes" {
		t.Fatalf("layout must be stable across judge outage: %v", got)
	}

	// --- Stage 4: pin beats a later max judge bump on the same card. ---
	if _, err := a.handleSetPinned(nil, gen.WorkbenchSetPinnedReq{ID: "app:deploy-log", Pinned: true}); err != nil {
		t.Fatalf("pin: %v", err)
	}
	mustIngest(t, a, gen.WorkbenchJudgeIngestReq{Scores: map[string]gen.WorkbenchJudgeScore{
		"app:deploy-log": {Level: 3, Backend: "llm", Calibrated: false},
	}})
	if a.cards["app:deploy-log"].score != log.score {
		t.Fatalf("pinned card must be immune to judge bumps: %v", a.cards["app:deploy-log"].score)
	}
	got = orderedIDs(a.cards, nil)
	if got[0] != "app:deploy-log" {
		t.Fatalf("pinned card must hold the head: %v", got)
	}
}
