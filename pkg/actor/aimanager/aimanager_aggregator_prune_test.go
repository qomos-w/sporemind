package aimanager

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestPruneDanglingAggregatorRefs_RemovesStaleKeepsValid(t *testing.T) {
	aggs := map[string]domain.AIManagerAggregatorGetResp{
		"B": {ID: "B", Name: "Agg B", Units: []domain.ManualCallableUnit{
			{ProviderName: "kimi", Model: "moonshot"},
		}},
		"A": {ID: "A", Name: "Agg A", Units: []domain.ManualCallableUnit{
			{ProviderName: "openai", Model: "gpt-4o"},
			{AggregatorID: "B"},
			{AggregatorID: "ghost"},
			// The auto aggregator is absent from the map during Load but its
			// references must survive.
			{AggregatorID: autoAggregatorID},
		}},
	}

	changed := pruneDanglingAggregatorRefs(aggs)

	if len(changed) != 1 || changed[0] != "A" {
		t.Fatalf("expected only A to change, got %v", changed)
	}
	units := aggs["A"].Units
	if len(units) != 3 {
		t.Fatalf("expected 3 units after prune, got %+v", units)
	}
	if units[1].AggregatorID != "B" {
		t.Fatalf("valid reference must survive, got %+v", units[1])
	}
	if units[2].AggregatorID != autoAggregatorID {
		t.Fatalf("auto reference must survive, got %+v", units[2])
	}
}

func TestPruneDanglingAggregatorRefs_CascadesToNamelessEmpty(t *testing.T) {
	// C references B; B (nameless) references A. Removing A strands B's only
	// unit, which empties B, which in turn strands C's reference — the prune
	// must cascade to a fixpoint.
	aggs := map[string]domain.AIManagerAggregatorGetResp{
		"C": {ID: "C", Name: "Agg C", Units: []domain.ManualCallableUnit{{AggregatorID: "B"}}},
		"B": {ID: "B", Units: []domain.ManualCallableUnit{{AggregatorID: "A"}}},
	}

	changed := pruneDanglingAggregatorRefs(aggs)

	if _, ok := aggs["B"]; ok {
		t.Fatal("empty nameless aggregator B must be cascade-deleted")
	}
	if _, ok := aggs["C"]; !ok {
		t.Fatal("named aggregator C must survive the cascade")
	}
	if len(aggs["C"].Units) != 0 {
		t.Fatalf("C's dangling reference must be pruned, got %+v", aggs["C"].Units)
	}
	joined := strings.Join(changed, ",")
	if !strings.Contains(joined, "B") || !strings.Contains(joined, "C") {
		t.Fatalf("changed must include B and C, got %v", changed)
	}
}

func TestPruneAggregatorRefsLocked_CleansDeletedBookkeeping(t *testing.T) {
	ghostID := testutil.GenActorID()
	a := &Actor{
		actorID: "test-aimanager",
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			// "inner" was already deleted; "outer" references it.
			"outer": {ID: "outer", Name: "Outer", Units: []domain.ManualCallableUnit{
				{ProviderName: "openai", Model: "gpt-4o"},
				{AggregatorID: "inner"},
			}},
			// Nameless aggregator whose only unit is the dangling reference.
			"inner-shell": {ID: "inner-shell", Units: []domain.ManualCallableUnit{
				{AggregatorID: "inner"},
			}},
		},
		aggRefs:     map[string]ref.Ref{"inner-shell": testutil.NewFakeRef(ghostID, nil)},
		aggActorIDs: map[string]string{"inner-shell": ghostID.String()},
	}

	toNotify, toStop := a.pruneAggregatorRefsLocked()

	if _, ok := a.aggregators["inner-shell"]; ok {
		t.Fatal("inner-shell must be cascade-deleted (empty + nameless)")
	}
	if len(toStop) != 1 {
		t.Fatalf("expected 1 ref to stop, got %d", len(toStop))
	}
	if _, ok := a.aggRefs["inner-shell"]; ok {
		t.Fatal("aggRefs entry for deleted aggregator must be cleaned")
	}
	if _, ok := a.aggActorIDs["inner-shell"]; ok {
		t.Fatal("aggActorIDs entry for deleted aggregator must be cleaned")
	}
	units := a.aggregators["outer"].Units
	if len(units) != 1 || units[0].Model != "gpt-4o" {
		t.Fatalf("outer must keep only its concrete unit, got %+v", units)
	}
	if len(toNotify) != 1 || toNotify[0] != "outer" {
		t.Fatalf("outer must be notified, got %v", toNotify)
	}
}

// TestHandleAggregatorConfigure_DeleteChildPrunesParentRefs is the end-to-end
// guard for the reported bug: deleting a child aggregator left the parent's
// reference entry behind in persisted state, and re-saving the parent was then
// rejected by validation ("references unknown aggregator").
func TestHandleAggregatorConfigure_DeleteChildPrunesParentRefs(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}
	a := &Actor{
		actorID:     "test-aimanager",
		aggregators: map[string]domain.AIManagerAggregatorGetResp{},
		aggRefs:     map[string]ref.Ref{},
		aggActorIDs: map[string]string{},
	}
	a.store = ms
	ctx := testutil.AdminCtx(testutil.GenActorID())

	if _, err := a.handleAggregatorConfigure(ctx, domain.AIManagerAggregatorConfigureReq{
		ID: "child", Name: "Child",
		Units: []domain.ManualCallableUnit{{ProviderName: "openai", Model: "gpt-4o"}},
	}); err != nil {
		t.Fatalf("configure child: %v", err)
	}
	if _, err := a.handleAggregatorConfigure(ctx, domain.AIManagerAggregatorConfigureReq{
		ID: "parent", Name: "Parent",
		Units: []domain.ManualCallableUnit{
			{ProviderName: "kimi", Model: "moonshot"},
			{AggregatorID: "child"},
		},
	}); err != nil {
		t.Fatalf("configure parent with reference to child: %v", err)
	}

	if _, err := a.handleAggregatorConfigure(ctx, domain.AIManagerAggregatorConfigureReq{ID: "child"}); err != nil {
		t.Fatalf("delete child: %v", err)
	}

	cfg, ok := a.aggregators["parent"]
	if !ok {
		t.Fatal("parent must survive child deletion")
	}
	if len(cfg.Units) != 1 || cfg.Units[0].AggregatorID != "" || cfg.Units[0].Model != "moonshot" {
		t.Fatalf("dangling reference must be pruned from parent, got %+v", cfg.Units)
	}

	// The pruned state must be what was persisted: reload from the same store
	// and verify no healing is needed and no dangling reference reappears.
	reloaded := &Actor{actorID: "test-aimanager", store: ms}
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.prunedOnLoad) != 0 {
		t.Fatalf("persisted state must already be clean, heal needed for %v", reloaded.prunedOnLoad)
	}
	if got := reloaded.aggregators["parent"].Units; len(got) != 1 || got[0].Model != "moonshot" {
		t.Fatalf("reloaded parent units wrong, got %+v", got)
	}
}

// TestLoad_PrunesDanglingAggregatorRefs guards the restart path: persisted
// state written before this fix (or by hand) carries references to deleted
// aggregators; Load must heal them in memory.
func TestLoad_PrunesDanglingAggregatorRefs(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}
	seed := persistState{
		Aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"B": {ID: "B", Name: "Agg B", Units: []domain.ManualCallableUnit{
				{ProviderName: "openai", Model: "gpt-4o"},
				{AggregatorID: "ghost"},
				{AggregatorID: autoAggregatorID},
			}},
			"shell": {ID: "shell", Units: []domain.ManualCallableUnit{{AggregatorID: "ghost"}}},
		},
	}
	raw, _ := json.Marshal(seed)
	ms.data["test-aimanager"] = raw

	a := &Actor{actorID: "test-aimanager", store: ms}
	if err := a.Load(); err != nil {
		t.Fatalf("load failed: %v", err)
	}

	units := a.aggregators["B"].Units
	if len(units) != 2 {
		t.Fatalf("expected concrete + auto reference to survive, got %+v", units)
	}
	if _, ok := a.aggregators["shell"]; ok {
		t.Fatal("nameless shell referencing only a ghost must be deleted on load")
	}
	if len(a.prunedOnLoad) != 2 {
		t.Fatalf("expected B and shell recorded as healed, got %v", a.prunedOnLoad)
	}
}

// TestSave_PreservesAggActorIDsBeforeSpawn guards the Save change: the OnInit
// dangling-ref heal persists state before any aggregator is spawned, and that
// save must not wipe the durable actor-ID map (agents bind to these IDs).
func TestSave_PreservesAggActorIDsBeforeSpawn(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}
	autoID := testutil.GenActorID()
	liveID := testutil.GenActorID()
	deadID := testutil.GenActorID()

	a := &Actor{
		actorID: "test-aimanager",
		store:   ms,
		// Pre-spawn state: no refs yet.
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"live": {ID: "live", Name: "Live"},
		},
		aggActorIDs: map[string]string{
			autoAggregatorID: autoID.String(), // auto config not persisted, ID must survive
			"live":           liveID.String(), // aggregator exists, ID must survive
			"dead":           deadID.String(), // aggregator already deleted, ID must be dropped
		},
		aggRefs: map[string]ref.Ref{
			"live": testutil.NewFakeRef(liveID, nil),
		},
	}

	if err := a.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	var state persistState
	if err := json.Unmarshal(ms.data["test-aimanager"], &state); err != nil {
		t.Fatalf("unmarshal saved state: %v", err)
	}
	if state.AggActorIDs[autoAggregatorID] != autoID.String() {
		t.Fatalf("auto aggregator actor ID must survive pre-spawn save, got %v", state.AggActorIDs)
	}
	if state.AggActorIDs["live"] != liveID.String() {
		t.Fatalf("surviving aggregator actor ID must survive pre-spawn save, got %v", state.AggActorIDs)
	}
	if _, ok := state.AggActorIDs["dead"]; ok {
		t.Fatalf("actor ID of deleted aggregator must be dropped, got %v", state.AggActorIDs)
	}
}
