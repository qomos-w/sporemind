package project

import (
	"fmt"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestProjectGraphSaveCASContention exercises the optimistic-concurrency guard
// in handleGraphSave: two goroutines racing with the same ExpectedRevision must
// produce exactly one winner, and the stored graph must match the winner's
// payload. A follow-up sequential round verifies a stale revision always loses.
func TestProjectGraphSaveCASContention(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())

	first, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
		ProjectID:    "project-1",
		GraphKind:    "target",
		ID:           "target",
		EnvelopeText: ``,
		Meta:         gen.GraphSnapshotMeta{SchemaVersion: "project.graph.v1", Source: "user"},
	})
	if err != nil {
		t.Fatal(err)
	}

	const rounds = 8
	current := first.Meta.Revision
	for round := 0; round < rounds; round++ {
		envA := fmt.Sprintf(`{"round":%d,"writer":"a"}`, round)
		envB := fmt.Sprintf(`{"round":%d,"writer":"b"}`, round)

	type result struct {
		writer string
		env    string
		resp   gen.ProjectGraphEnvelopeResp
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, w := range []struct {
		name string
		env  string
	}{{"a", envA}, {"b", envB}} {
		go func(name, env string) {
			<-start // maximize overlap: both goroutines launch together
			resp, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
				ProjectID:        "project-1",
				GraphKind:        "target",
				ID:               "target",
				ExpectedRevision: current,
				EnvelopeText:     env,
			})
			results <- result{writer: name, env: env, resp: resp, err: err}
		}(w.name, w.env)
	}
		close(start)

		winners := make([]result, 0, 2)
		for i := 0; i < 2; i++ {
			r := <-results
			if r.err == nil {
				winners = append(winners, r)
			} else if !strings.Contains(r.err.Error(), "revision") {
				t.Fatalf("round %d: loser %q error should mention revision, got: %v", round, r.writer, r.err)
			}
		}
		if len(winners) != 1 {
			t.Fatalf("round %d: expected exactly 1 successful CAS write, got %d", round, len(winners))
		}
		winner := winners[0]
		if winner.resp.EnvelopeText != winner.env {
			t.Fatalf("round %d: winner response envelope %q does not match request %q", round, winner.resp.EnvelopeText, winner.env)
		}

		got, err := a.handleGraphGet(ctx, gen.ProjectGraphGetReq{GraphKind: "target", ID: "target"})
		if err != nil {
			t.Fatal(err)
		}
		if got.EnvelopeText != winner.resp.EnvelopeText {
			t.Fatalf("round %d: stored envelope %q does not match winner %q", round, got.EnvelopeText, winner.resp.EnvelopeText)
		}
		if got.Meta.Revision != winner.resp.Meta.Revision || got.Meta.Revision == current {
			t.Fatalf("round %d: revision did not advance past %q: got %q", round, current, got.Meta.Revision)
		}
		current = got.Meta.Revision
	}

	// Sequential CAS: the original revision is now stale and must lose, while
	// the current revision still wins.
	if _, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
		ProjectID:        "project-1",
		GraphKind:        "target",
		ID:               "target",
		ExpectedRevision: first.Meta.Revision,
		EnvelopeText:     `{"stale":true}`,
	}); err == nil {
		t.Fatal("expected stale-revision save to fail")
	} else if !strings.Contains(err.Error(), "revision") {
		t.Fatalf("stale-save error should mention revision, got: %v", err)
	}
	fresh, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
		ProjectID:        "project-1",
		GraphKind:        "target",
		ID:               "target",
		ExpectedRevision: current,
		EnvelopeText:     `{"seq":"current"}`,
	})
	if err != nil {
		t.Fatalf("expected current-revision save to succeed: %v", err)
	}
	got, err := a.handleGraphGet(ctx, gen.ProjectGraphGetReq{GraphKind: "target", ID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if got.EnvelopeText != `{"seq":"current"}` || got.Meta.Revision != fresh.Meta.Revision {
		t.Fatalf("sequential CAS save not reflected: envelope %q revision %q", got.EnvelopeText, got.Meta.Revision)
	}
}

func TestProjectGraphSaveGet(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())

	saved, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
		ProjectID:    "project-1",
		GraphKind:    "target",
		ID:           "target",
		EnvelopeText: ``,
		Meta: gen.GraphSnapshotMeta{
			SchemaVersion: "project.graph.v1",
			Source:        "user",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Meta.ProjectID != "project-1" {
		t.Fatalf("expected project id to be filled, got %q", saved.Meta.ProjectID)
	}
	if saved.Meta.Revision == "" {
		t.Fatal("expected revision")
	}
	if saved.EnvelopeText != `` {
		t.Fatalf("unexpected envelope: %s", saved.EnvelopeText)
	}

	got, err := a.handleGraphGet(ctx, gen.ProjectGraphGetReq{GraphKind: "target", ID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.Revision != saved.Meta.Revision {
		t.Fatalf("expected revision %q, got %q", saved.Meta.Revision, got.Meta.Revision)
	}
}

func TestProjectGraphConceptsRemainOnDemandReadable(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())
	concepts := `{"concepts":[{"id":"card-ref","desc":"canonical card reference"}]}`
	if _, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
		ProjectID: "project-1", GraphKind: "target", ID: "target", EnvelopeText: concepts,
		Meta: gen.GraphSnapshotMeta{SchemaVersion: "project.graph.v1", Source: "user"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := a.handleGraphGet(ctx, gen.ProjectGraphGetReq{GraphKind: "target", ID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if got.EnvelopeText != concepts {
		t.Fatalf("concept truth source changed: %q", got.EnvelopeText)
	}
}
func TestProjectGraphConceptGetReadsOneConcept(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())
	envelope := `{"Concepts":[{"Id":"card-ref","Desc":"canonical reference","Capabilities":[],"Constraints":[],"Checks":[],"State":"real"}]}`
	if _, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{ProjectID: "project-1", GraphKind: "target", ID: "target", EnvelopeText: envelope}); err != nil {
		t.Fatal(err)
	}
	got, err := a.handleGraphConceptGet(ctx, gen.ProjectGraphConceptGetReq{ProjectID: "project-1", GraphKind: "target", ID: "target", ConceptID: "card-ref"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Concept.ID != "card-ref" || got.Concept.Desc != "canonical reference" {
		t.Fatalf("unexpected concept: %+v", got.Concept)
	}
}
func TestProjectGraphSaveExpectedRevision(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())

	first, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
		ProjectID:    "project-1",
		GraphKind:    "target",
		ID:           "target",
		EnvelopeText: ``,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
		ProjectID:        "project-1",
		GraphKind:        "target",
		ID:               "target",
		ExpectedRevision: "wrong",
		EnvelopeText:     ``,
	}); err == nil {
		t.Fatal("expected revision mismatch")
	}

	if _, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
		ProjectID:        "project-1",
		GraphKind:        "target",
		ID:               "target",
		ExpectedRevision: first.Meta.Revision,
		EnvelopeText:     `Concept test {\n\tdesc: ok\n}`,
	}); err != nil {
		t.Fatal(err)
	}
}

func graphChangedEvents(ctx *testutil.FakeCtx) []gen.GraphChangedEvent {
	var out []gen.GraphChangedEvent
	// Snapshot under FakeCtx.mu: the file watcher goroutine may append
	// card_changed events concurrently with this read.
	for _, ev := range ctx.EmittedEventsSnapshot() {
		if ev.Kind != "graph_changed" {
			continue
		}
		payload, ok := ev.Payload.(gen.GraphChangedEvent)
		if !ok {
			continue
		}
		out = append(out, payload)
	}
	return out
}

func TestProjectGraphSaveEmitsGraphChanged(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())

	saved, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
		ProjectID:    "project-1",
		GraphKind:    "target",
		ID:           "target",
		EnvelopeText: ``,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := graphChangedEvents(ctx)
	if len(events) != 1 {
		t.Fatalf("expected 1 graph_changed event, got %d", len(events))
	}
	if events[0] != (gen.GraphChangedEvent{GraphKind: "target", ID: "target", Revision: saved.Meta.Revision}) {
		t.Fatalf("unexpected graph_changed payload: %+v", events[0])
	}

	// Second save emits again with the new revision.
	updated, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
		ProjectID:        "project-1",
		GraphKind:        "target",
		ID:               "target",
		ExpectedRevision: saved.Meta.Revision,
		EnvelopeText:     `Concept test {\n\tdesc: ok\n}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	events = graphChangedEvents(ctx)
	if len(events) != 2 {
		t.Fatalf("expected 2 graph_changed events, got %d", len(events))
	}
	if events[1] != (gen.GraphChangedEvent{GraphKind: "target", ID: "target", Revision: updated.Meta.Revision}) {
		t.Fatalf("unexpected graph_changed payload after update: %+v", events[1])
	}

	// Failed save (revision mismatch) must not emit.
	if _, err := a.handleGraphSave(ctx, gen.ProjectGraphSaveReq{
		ProjectID:        "project-1",
		GraphKind:        "target",
		ID:               "target",
		ExpectedRevision: "stale",
		EnvelopeText:     ``,
	}); err == nil {
		t.Fatal("expected revision mismatch error")
	}
	events = graphChangedEvents(ctx)
	if len(events) != 2 {
		t.Fatalf("expected no graph_changed event on failed save, got %d", len(events))
	}
}
