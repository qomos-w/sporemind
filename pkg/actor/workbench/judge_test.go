package workbench

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func newJudgeTestActor(t *testing.T) *Actor {
	t.Helper()
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	a := &Actor{
		cards:      map[string]*cardState{},
		judgeBumps: map[string]float64{},
	}
	return a
}

func TestJudgeBump(t *testing.T) {
	cases := []struct {
		level      int32
		calibrated bool
		want       float64
	}{
		{0, true, 0}, {1, true, 2}, {2, true, 4}, {3, true, 6},
		{0, false, 0}, {1, false, 1}, {3, false, 3},
		{-1, true, 0}, {9, true, 0},
	}
	for _, c := range cases {
		got := judgeBump(gen.WorkbenchJudgeScore{Level: c.level, Calibrated: c.calibrated})
		if got != c.want {
			t.Errorf("bump(level=%d, calibrated=%v) = %v, want %v", c.level, c.calibrated, got, c.want)
		}
	}
}

// TestJudgeIngestOverlayLifecycle: bump applies, re-bumps replace (not
// accumulate), and an empty round removes every overlay (score returns to its
// pre-judge base) — the overlay is a bounded, refreshable signal.
func TestJudgeIngestOverlayLifecycle(t *testing.T) {
	a := newJudgeTestActor(t)
	base := 10.0
	a.cards["x"] = &cardState{id: "x", kind: kindApp, title: "X", score: base, seq: 1}

	// round 1: urgent calibrated → +6
	mustIngest(t, a, gen.WorkbenchJudgeIngestReq{Scores: map[string]gen.WorkbenchJudgeScore{
		"x": {Level: 3, Backend: "jev", Calibrated: true},
	}})
	if got := a.cards["x"].score; got != base+6 {
		t.Fatalf("round1 score = %v, want %v", got, base+6)
	}
	if !strings.Contains(a.cards["x"].why, "小脑") || !strings.Contains(a.cards["x"].why, "jev") {
		t.Fatalf("why = %q, want judge provenance", a.cards["x"].why)
	}

	// round 2: routine uncalibrated (llm) → +1 (replaces +6, no accumulation)
	mustIngest(t, a, gen.WorkbenchJudgeIngestReq{Scores: map[string]gen.WorkbenchJudgeScore{
		"x": {Level: 1, Backend: "llm", Calibrated: false},
	}})
	if got := a.cards["x"].score; got != base+1 {
		t.Fatalf("round2 score = %v, want %v (overlay replaced)", got, base+1)
	}

	// round 3: no verdicts → overlay fully removed
	mustIngest(t, a, gen.WorkbenchJudgeIngestReq{Scores: map[string]gen.WorkbenchJudgeScore{}})
	if got := a.cards["x"].score; got != base {
		t.Fatalf("round3 score = %v, want base %v", got, base)
	}

	// round 4: quiet verdict → no bump, stale judge note cleared
	a.cards["x"].why = "小脑: 紧急（jev）"
	mustIngest(t, a, gen.WorkbenchJudgeIngestReq{Scores: map[string]gen.WorkbenchJudgeScore{
		"x": {Level: 0, Backend: "jev", Calibrated: true},
	}})
	if got := a.cards["x"].score; got != base {
		t.Fatalf("round4 score = %v, want base", got)
	}
	if a.cards["x"].why != "" {
		t.Fatalf("quiet verdict must clear judge note, got %q", a.cards["x"].why)
	}
}

// TestJudgeIngestRespectsUserIntent: pinned, hidden and terminal cards are
// never judge-bumped even when a verdict is present.
func TestJudgeIngestRespectsUserIntent(t *testing.T) {
	a := newJudgeTestActor(t)
	a.cards["pinned"] = &cardState{id: "pinned", kind: kindApp, score: 20, pinned: true, seq: 1}
	a.cards["hidden"] = &cardState{id: "hidden", kind: kindApp, score: 20, hidden: true, seq: 2}
	a.cards["term"] = &cardState{id: "term", kind: kindTerminal, score: 20, seq: 3}
	verdict := gen.WorkbenchJudgeScore{Level: 3, Backend: "jev", Calibrated: true}
	mustIngest(t, a, gen.WorkbenchJudgeIngestReq{Scores: map[string]gen.WorkbenchJudgeScore{
		"pinned": verdict, "hidden": verdict, "term": verdict,
	}})
	for id, c := range a.cards {
		if c.score != 20 {
			t.Errorf("%s bumped to %v — user intent must dominate", id, c.score)
		}
	}
}

// TestJudgeBumpNeverSwapsSlots: with judgeMaxBump (6) < Hysteresis (15), a
// max-strength verdict on the challenger alone must never displace the
// incumbent — the hysteresis contract survives the judge.
func TestJudgeBumpNeverSwapsSlots(t *testing.T) {
	a := newJudgeTestActor(t)
	inc := &cardState{id: "inc", kind: kindApp, title: "incumbent", score: 30, seq: 1}
	chl := &cardState{id: "chl", kind: kindApp, title: "challenger", score: 10, seq: 2}
	a.cards["inc"] = inc
	a.cards["chl"] = chl

	mustIngest(t, a, gen.WorkbenchJudgeIngestReq{Scores: map[string]gen.WorkbenchJudgeScore{
		"chl": {Level: 3, Backend: "jev", Calibrated: true},
	}})
	if chl.score != 16 {
		t.Fatalf("challenger = %v, want 16", chl.score)
	}
	got := orderedIDs(a.cards, []string{"inc", "chl"})
	if got[0] != "inc" {
		t.Fatalf("judge bump alone must not swap slots: order = %v", got)
	}
}

// TestBuildJudgeDecideReq: state mentions card titles; questions exist only
// for eligible cards; keys map back to card ids.
func TestBuildJudgeDecideReq(t *testing.T) {
	a := newJudgeTestActor(t)
	a.cards["ok1"] = &cardState{id: "ok1", kind: kindApp, title: "Demo App", score: 12, seq: 1}
	a.cards["ok2"] = &cardState{id: "ok2", kind: kindChat, title: "Chat Agent", score: 8, seq: 2}
	a.cards["pin"] = &cardState{id: "pin", kind: kindApp, title: "PinnedOne", score: 40, pinned: true, seq: 3}
	a.cards["hid"] = &cardState{id: "hid", kind: kindApp, title: "HiddenOne", score: 5, hidden: true, seq: 4}
	a.cards["term"] = &cardState{id: "term", kind: kindTerminal, title: "term", score: 5, seq: 5}
	a.appendRecentLocked("agent:1", attentionKindStep, "ran a tool")

	jb := a.judgeBoardSnapshot()
	if jb.frozen || jb.maximized {
		t.Fatal("board not frozen")
	}
	if len(jb.judgeable) != 2 {
		t.Fatalf("judgeable = %d, want 2", len(jb.judgeable))
	}
	// highest score first
	if jb.judgeable[0].id != "ok1" {
		t.Fatalf("judgeable order = %v", jb.judgeable)
	}

	req := buildJudgeDecideReq(jb)
	if !strings.Contains(req.State, "Demo App") || !strings.Contains(req.State, "ran a tool") {
		t.Errorf("state missing board/recent content: %q", req.State)
	}
	if strings.Contains(req.State, "PinnedOne") {
		// board text includes pinned card for context — that is fine; only the
		// QUESTION set must exclude it.
		_ = req.State
	}
	if len(req.Questions) != 2 {
		t.Fatalf("questions = %d, want 2", len(req.Questions))
	}
	for id, key := range jb.keys {
		q := req.Questions[key]
		if q.Type != "score" || len(q.Levels) != len(judgeLevels) {
			t.Fatalf("question %q malformed: %+v", key, q)
		}
		if id != "ok1" && id != "ok2" {
			t.Fatalf("key mapped to ineligible card %q", id)
		}
	}
}

// TestJudgeBoardSnapshotSkipsFrozenAndCaptured: frozen layout or a full
// capture holds placement — no judge round is built.
func TestJudgeBoardSnapshotSkipsFrozenAndCaptured(t *testing.T) {
	a := newJudgeTestActor(t)
	a.cards["x"] = &cardState{id: "x", kind: kindApp, title: "X", score: 10, seq: 1}

	a.frozen = true
	if jb := a.judgeBoardSnapshot(); len(jb.judgeable) != 0 {
		t.Fatal("frozen board must skip judge")
	}
	a.frozen = false
	a.maximizedID = "x"
	if jb := a.judgeBoardSnapshot(); len(jb.judgeable) != 0 {
		t.Fatal("maximized board must skip judge")
	}
}

func mustIngest(t *testing.T, a *Actor, req gen.WorkbenchJudgeIngestReq) {
	t.Helper()
	if err := a.handleJudgeIngest(nil, req); err != nil {
		t.Fatalf("judge ingest: %v", err)
	}
}
