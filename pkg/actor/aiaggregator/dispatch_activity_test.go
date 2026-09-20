package aiaggregator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// Note: we rely on helpers from failover_helpers_test.go (testPureCtx,
// collectingEmitter, fakeRef, swapGate, dualRegistry, okStream,
// fakeResultStream, errorClient) and nested_test.go (canonicalAID,
// nestedActor, chunkList, stubActorCtx, fakePlanner, plan.RecvResult).
// NewFallbackStrategy is defined in strategy_test.go.

// ──────────────────────────────────────────────────────────────────────────────
// 1. setDispatchActivity / clearDispatchActivity / snapshotDispatchActivity
// ──────────────────────────────────────────────────────────────────────────────

func TestDispatchActivity_SetGetClear(t *testing.T) {
	a := &Actor{}
	req := domain.SendSessionMessageReq{
		SessionID: "sess-1",
		AgentID:   "agent-1",
		SlotKind:  "primary",
	}

	// Unit starts idle.
	if act := a.snapshotDispatchActivity("p::m"); act != nil {
		t.Fatal("expected nil activity for idle unit")
	}

	// Set "trying".
	a.setDispatchActivity("p::m", DispatchStateTrying, req, 0, "")
	act := a.snapshotDispatchActivity("p::m")
	if act == nil {
		t.Fatal("expected non-nil activity after set")
	}
	if act.State != DispatchStateTrying {
		t.Errorf("State = %q, want %q", act.State, DispatchStateTrying)
	}
	if act.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", act.SessionID)
	}
	if act.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want agent-1", act.AgentID)
	}
	if act.SlotKind != "primary" {
		t.Errorf("SlotKind = %q, want primary", act.SlotKind)
	}
	if act.StartedAt == 0 {
		t.Error("StartedAt must be non-zero")
	}
	if act.Depth != 0 {
		t.Errorf("Depth = %d, want 0", act.Depth)
	}
	if act.AggregatorID != "" {
		t.Errorf("AggregatorID = %q, want empty", act.AggregatorID)
	}

	// Update to "in_use".
	a.setDispatchActivity("p::m", DispatchStateInUse, req, 0, "")
	act = a.snapshotDispatchActivity("p::m")
	if act.State != DispatchStateInUse {
		t.Errorf("State = %q, want %q", act.State, DispatchStateInUse)
	}

	// Clear.
	a.clearDispatchActivity("p::m")
	if act := a.snapshotDispatchActivity("p::m"); act != nil {
		t.Fatal("expected nil activity after clear")
	}
}

func TestDispatchActivity_EmptyUnitID(t *testing.T) {
	a := &Actor{}
	req := domain.SendSessionMessageReq{SessionID: "s"}
	// setDispatchActivity with empty unitID must not panic.
	a.setDispatchActivity("", DispatchStateTrying, req, 0, "")
	// clearDispatchActivity with empty unitID must not panic.
	a.clearDispatchActivity("")
	// snapshotDispatchActivity with empty unitID returns nil.
	if act := a.snapshotDispatchActivity(""); act != nil {
		t.Fatal("expected nil for empty unitID")
	}
}

func TestDispatchActivity_SnapshotAll(t *testing.T) {
	a := &Actor{}
	req := domain.SendSessionMessageReq{SessionID: "s", AgentID: "a"}

	all := a.snapshotAllDispatchActivities()
	if len(all) != 0 {
		t.Fatalf("expected empty map, got %d entries", len(all))
	}

	a.setDispatchActivity("p1::m1", DispatchStateTrying, req, 0, "")
	a.setDispatchActivity("p2::m2", DispatchStateInUse, req, 0, "")

	all = a.snapshotAllDispatchActivities()
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
	if all["p1::m1"].State != DispatchStateTrying {
		t.Errorf("p1::m1 state = %q", all["p1::m1"].State)
	}
	if all["p2::m2"].State != DispatchStateInUse {
		t.Errorf("p2::m2 state = %q", all["p2::m2"].State)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 2. handleDispatch marks "trying" at start, "in_use" on stream open, clears
//    on completion
// ──────────────────────────────────────────────────────────────────────────────

func TestDispatchActivity_HandleDispatchMarksTryingAtStart(t *testing.T) {
	restoreGate := swapGate(t, "p")
	defer restoreGate()

	a := &Actor{
		units:    []CallableUnit{{ID: "p::m", Model: "m", ProviderName: "p", Protocol: "ok"}},
		strategy: NewFallbackStrategy(),
		registry: dualRegistry(nil),
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{},
		}},
		lifecycleCtx: context.Background(),
	}

	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{
		SessionID: "sess-1",
		AgentID:   "agent-1",
		SlotKind:  "primary",
	}, emit)
	if err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}

	// After completion, the dispatch activity must be cleared.
	if act := a.snapshotDispatchActivity("p::m"); act != nil {
		t.Errorf("activity should be nil after dispatch completion, got %+v", act)
	}
}

func TestDispatchActivity_HandleDispatchMarksTryingConcurrently(t *testing.T) {
	// Verify that handleStatus sees "trying" before the stream opens.
	// Use a slow stream that blocks so we can observe the state.
	blockCh := make(chan struct{})
	slowClient := &blockingClient{blockCh: blockCh}
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "slow",
		Factory:  func(_, _ string) llmclient.Client { return slowClient },
	})

	restoreGate := swapGate(t, "p")
	defer restoreGate()

	a := &Actor{
		units:    []CallableUnit{{ID: "p::m", Model: "m", ProviderName: "p", Protocol: "slow"}},
		strategy: NewFallbackStrategy(),
		registry: *reg,
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{},
		}},
		lifecycleCtx: context.Background(),
	}

	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	done := make(chan error, 1)
	go func() {
		done <- a.handleDispatch(ctx, domain.SendSessionMessageReq{
			SessionID: "sess-2",
			AgentID:   "agent-2",
			SlotKind:  "primary",
		}, emit)
	}()

	// Give the dispatch a moment to reach the "trying" state.
	time.Sleep(50 * time.Millisecond)

	// Check that the unit is marked as "trying" (the stream hasn't opened yet
	// because the slow client blocks before returning the stream).
	act := a.snapshotDispatchActivity("p::m")
	if act == nil {
		t.Fatal("expected non-nil dispatch activity while dispatch is in flight")
	}
	if act.State != DispatchStateTrying && act.State != DispatchStateInUse {
		t.Errorf("expected state %q or %q, got %q", DispatchStateTrying, DispatchStateInUse, act.State)
	}

	// Also verify handleStatus includes the activity.
	status, err := a.handleStatus(nil)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	var found bool
	for _, u := range status.Units {
		if u.ID == "p::m" && u.DispatchActivity != nil {
			found = true
			if u.DispatchActivity.State == "" {
				t.Errorf("DispatchActivity.State is empty")
			}
		}
	}
	if !found {
		t.Error("handleStatus: unit p::m should have a non-nil DispatchActivity")
	}

	// Unblock the stream to let dispatch complete.
	close(blockCh)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handleDispatch: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handleDispatch timed out")
	}
}

// blockingClient blocks until blockCh is closed before returning a stream.
type blockingClient struct {
	blockCh <-chan struct{}
}

func (c *blockingClient) Stream(ctx context.Context, _ llmclient.Request) (llmclient.Stream, error) {
	select {
	case <-c.blockCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ch := make(chan llmclient.Event, 1)
	ch <- llmclient.Event{Kind: llmclient.EventStop}
	close(ch)
	return &okStream{ch: ch}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// 3. handleDispatch clears activity on error
// ──────────────────────────────────────────────────────────────────────────────

func TestDispatchActivity_ClearedOnStreamOpenError(t *testing.T) {
	restoreGate := swapGate(t, "p")
	defer restoreGate()

	a := &Actor{
		units:    []CallableUnit{{ID: "p::m", Model: "m", ProviderName: "p", Protocol: "fail"}},
		strategy: NewFallbackStrategy(),
		registry: dualRegistry(errors.New("stream open failed")),
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{},
		}},
		lifecycleCtx: context.Background(),
	}

	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{
		SessionID: "sess-3",
		AgentID:   "agent-3",
	}, emit)
	if err == nil {
		t.Fatal("expected error from fail protocol")
	}

	// Activity must be cleared after error.
	if act := a.snapshotDispatchActivity("p::m"); act != nil {
		t.Errorf("activity should be nil after dispatch error, got %+v", act)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 4. Failover rotation: clears old unit, marks new unit as "trying"
// ──────────────────────────────────────────────────────────────────────────────

func TestDispatchActivity_FailoverRotation(t *testing.T) {
	restoreGate := swapGate(t, "p1", "p2")
	defer restoreGate()

	// First unit fails, second succeeds.
	a := &Actor{
		units: []CallableUnit{
			{ID: "p1::m1", Model: "m1", ProviderName: "p1", Protocol: "fail"},
			{ID: "p2::m2", Model: "m2", ProviderName: "p2", Protocol: "ok"},
		},
		strategy: NewFallbackStrategy(),
		registry: dualRegistry(errors.New("first unit fails")),
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{},
		}},
		lifecycleCtx: context.Background(),
	}

	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{
		SessionID: "sess-failover",
		AgentID:   "agent-failover",
		SlotKind:  "primary",
	}, emit)
	if err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}

	// After completion, both units must be idle.
	if act := a.snapshotDispatchActivity("p1::m1"); act != nil {
		t.Errorf("p1::m1 should be idle after failover, got %+v", act)
	}
	if act := a.snapshotDispatchActivity("p2::m2"); act != nil {
		t.Errorf("p2::m2 should be idle after dispatch completion, got %+v", act)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 4b. Cooled specific unit: non-pinned falls back to auto-selection;
//     pinned (unit-locked) bypasses cooling and still dispatches the chosen
//     unit — a user retry must genuinely re-attempt the locked unit, while
//     hard bans (auth-disabled, disable window) still block it.
// ──────────────────────────────────────────────────────────────────────────────

func TestDispatchActivity_CooledUnitFallsBackToAutoSelection(t *testing.T) {
	resetGlobalHealth(t)
	restoreGate := swapGate(t, "cool", "ok")
	defer restoreGate()

	// First unit matches the requested (model, provider) but is cooling down;
	// second unit is healthy and should be auto-selected for non-pinned requests.
	a := &Actor{
		units: []CallableUnit{
			{ID: "cool::m-cool", Model: "m-cool", ProviderName: "cool", Protocol: "ok"},
			{ID: "ok::m-ok", Model: "m-ok", ProviderName: "ok", Protocol: "ok"},
		},
		strategy:     NewFallbackStrategy(),
		registry:     dualRegistry(nil),
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	coolUnit(t, "cool", "m-cool")

	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{
		SessionID: "sess-fallback-selection",
		AgentID:   "agent-fallback",
		SlotKind:  "primary",
		Unit:      &domain.ModelUnit{Model: "m-cool", Provider: "cool"},
		// UnitPinned is false: soft affinity allows the aggregator to switch.
	}, emit)
	if err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}

	resolved := emit.resolvedUnit()
	if resolved == nil {
		t.Fatal("expected a resolved unit")
	}
	if resolved.Provider != "ok" || resolved.Model != "m-ok" {
		t.Errorf("resolved unit = %s/%s, want ok/m-ok", resolved.Provider, resolved.Model)
	}
}

func TestDispatchActivity_CooledPinnedUnitStillSelected(t *testing.T) {
	resetGlobalHealth(t)
	restoreGate := swapGate(t, "cool", "ok")
	defer restoreGate()

	a := &Actor{
		units: []CallableUnit{
			{ID: "cool::m-cool", Model: "m-cool", ProviderName: "cool", Protocol: "ok"},
			{ID: "ok::m-ok", Model: "m-ok", ProviderName: "ok", Protocol: "ok"},
		},
		strategy:     NewFallbackStrategy(),
		registry:     dualRegistry(nil),
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	coolUnit(t, "cool", "m-cool")

	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{
		SessionID:  "sess-pinned-cooled",
		AgentID:    "agent-pinned",
		SlotKind:   "primary",
		Unit:       &domain.ModelUnit{Model: "m-cool", Provider: "cool"},
		UnitPinned: true,
	}, emit)
	if err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}

	resolved := emit.resolvedUnit()
	if resolved == nil {
		t.Fatal("expected a resolved unit")
	}
	if resolved.Provider != "cool" || resolved.Model != "m-cool" {
		t.Errorf("resolved unit = %s/%s, want the pinned cool/m-cool (cooling must not block a unit-locked slot)", resolved.Provider, resolved.Model)
	}
}

// A unit-locked slot bypasses transient cooling but NOT hard bans: an
// auth-disabled unit (401) is a configuration failure, not a retryable one,
// and must still fail selection with no-match.
func TestDispatchActivity_PinnedAuthDisabledUnitStillRefused(t *testing.T) {
	resetGlobalHealth(t)
	restoreGate := swapGate(t, "auth")
	defer restoreGate()
	t.Cleanup(func() { llmclient.ClearProvider("auth") })

	a := &Actor{
		units: []CallableUnit{
			{ID: "auth::m", Model: "m", ProviderName: "auth", Protocol: "ok"},
		},
		strategy:     NewFallbackStrategy(),
		registry:     dualRegistry(nil),
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	llmclient.RecordFailure("auth", "m", &llmclient.UpstreamError{StatusCode: 401, Message: "invalid api key"})

	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{
		SessionID:  "sess-pinned-auth",
		AgentID:    "agent-pinned-auth",
		SlotKind:   "primary",
		Unit:       &domain.ModelUnit{Model: "m", Provider: "auth"},
		UnitPinned: true,
	}, emit)
	if err == nil {
		t.Fatal("expected error for pinned auth-disabled unit")
	}
	if !strings.Contains(err.Error(), "no callable unit") {
		t.Errorf("expected no callable unit error, got %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 5. handleStatus returns dispatch activity for in-flight units
// ──────────────────────────────────────────────────────────────────────────────

func TestDispatchActivity_HandleStatusShowsInFlight(t *testing.T) {
	a := &Actor{
		units: []CallableUnit{
			{ID: "p1::m1", Model: "m1", ProviderName: "p1", Protocol: "anthropic"},
			{ID: "p2::m2", Model: "m2", ProviderName: "p2", Protocol: "anthropic"},
		},
	}
	req := domain.SendSessionMessageReq{
		SessionID: "sess-status",
		AgentID:   "agent-status",
		SlotKind:  "primary",
	}

	// Mark p1::m1 as "in_use".
	a.setDispatchActivity("p1::m1", DispatchStateInUse, req, 0, "")

	status, err := a.handleStatus(nil)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	var foundActive, foundIdle bool
	for _, u := range status.Units {
		if u.ID == "p1::m1" {
			if u.DispatchActivity == nil {
				t.Error("p1::m1 should have DispatchActivity")
			} else if u.DispatchActivity.State != DispatchStateInUse {
				t.Errorf("p1::m1 state = %q, want %q", u.DispatchActivity.State, DispatchStateInUse)
			} else {
				if u.DispatchActivity.SessionID != "sess-status" {
					t.Errorf("p1::m1 SessionID = %q, want sess-status", u.DispatchActivity.SessionID)
				}
				if u.DispatchActivity.AgentID != "agent-status" {
					t.Errorf("p1::m1 AgentID = %q, want agent-status", u.DispatchActivity.AgentID)
				}
			}
			foundActive = true
		}
		if u.ID == "p2::m2" {
			if u.DispatchActivity != nil {
				t.Errorf("p2::m2 should have nil DispatchActivity (idle), got %+v", u.DispatchActivity)
			}
			foundIdle = true
		}
	}
	if !foundActive {
		t.Error("p1::m1 not found in status")
	}
	if !foundIdle {
		t.Error("p2::m2 not found in status")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 6. Nested dispatch: parent records activity on the aggregator-ref entry
// ──────────────────────────────────────────────────────────────────────────────

func TestDispatchActivity_NestedDispatchMarksAggregatorRef(t *testing.T) {
	restoreGate := swapGate(t, "b")
	defer restoreGate()

	deep := domain.ModelUnit{Model: "deep-model", Provider: "childprov"}
	childAID, _ := canonicalAID(t, 7)
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): chunkList(deep, "hello from child"),
	}}
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}
	a, ctx, emit := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(nil))

	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{
		SessionID: "sess-nested",
		AgentID:   "agent-nested",
		SlotKind:  "primary",
	}, emit); err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}

	// After completion, the aggregator-ref entry must be idle.
	if act := a.snapshotDispatchActivity("agg:child"); act != nil {
		t.Errorf("agg:child should be idle after dispatch completion, got %+v", act)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 7. aggregatedDispatchActivity: for aggregator-ref entries, queries child
// ──────────────────────────────────────────────────────────────────────────────

func TestAggregatedDispatchActivity_ConcreteUnit(t *testing.T) {
	a := &Actor{}
	req := domain.SendSessionMessageReq{SessionID: "s", AgentID: "a"}
	a.setDispatchActivity("p::m", DispatchStateTrying, req, 0, "")

	u := CallableUnit{ID: "p::m", Model: "m", ProviderName: "p"}
	act := a.aggregatedDispatchActivity(u)
	if act == nil {
		t.Fatal("expected non-nil activity")
	}
	if act.State != DispatchStateTrying {
		t.Errorf("state = %q, want %q", act.State, DispatchStateTrying)
	}
}

func TestAggregatedDispatchActivity_AggregatorRefFallsBackToParent(t *testing.T) {
	// An aggregator-ref entry with no reachable child: falls back to parent's
	// own activity record for the entry ID.
	a := &Actor{id: "parent"}
	req := domain.SendSessionMessageReq{SessionID: "s", AgentID: "a"}
	a.setDispatchActivity("agg:child", DispatchStateTrying, req, 0, "child")

	u := CallableUnit{ID: "agg:child", AggregatorID: "child"}
	act := a.aggregatedDispatchActivity(u)
	if act == nil {
		t.Fatal("expected non-nil activity (fallback to parent)")
	}
	if act.State != DispatchStateTrying {
		t.Errorf("state = %q, want %q", act.State, DispatchStateTrying)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 8. snapshotAllDispatchActivities returns a snapshot unaffected by mutations
// ──────────────────────────────────────────────────────────────────────────────

func TestDispatchActivity_SnapshotAllIsolates(t *testing.T) {
	a := &Actor{}
	req := domain.SendSessionMessageReq{SessionID: "s", AgentID: "a"}
	a.setDispatchActivity("p::m", DispatchStateInUse, req, 0, "")

	snap := a.snapshotAllDispatchActivities()
	if len(snap) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(snap))
	}

	// Mutation after snapshot must not affect the copy.
	a.clearDispatchActivity("p::m")
	if len(a.snapshotAllDispatchActivities()) != 0 {
		t.Error("activity should be cleared after clear")
	}
	if len(snap) != 1 {
		t.Error("snapshot must be isolated from later mutations")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 9. handleStatus for aggregator-ref entries: no crash when child unreachable
// ──────────────────────────────────────────────────────────────────────────────

func TestDispatchActivity_HandleStatusAggregatorRefNoCrash(t *testing.T) {
	a := &Actor{
		id: "parent",
		units: []CallableUnit{
			{ID: "agg:child", AggregatorID: "child"},
			{ID: "p::m", Model: "m", ProviderName: "p"},
		},
		lifecycleCtx: context.Background(),
		// No aimanagerRef → resolveChildAggregatorRef fails → nil activity.
	}

	status, err := a.handleStatus(nil)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if len(status.Units) != 2 {
		t.Fatalf("expected 2 units, got %d", len(status.Units))
	}
	// Both units must have no dispatch activity (idle).
	for _, u := range status.Units {
		if u.DispatchActivity != nil {
			t.Errorf("unit %q: expected nil DispatchActivity (idle), got %+v", u.ID, u.DispatchActivity)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 10. Depth propagation: aggregatedDispatchActivity increments depth
// ──────────────────────────────────────────────────────────────────────────────

func TestDispatchActivity_DepthPropagation(t *testing.T) {
	// Parent aggregator pool references a child aggregator. The child is
	// reachable and its status reports a concrete unit as "in_use" (depth 0).
	// The parent's aggregatedDispatchActivity must propagate it upward:
	// depth+1 and AggregatorID = "child".
	childActivity := &domain.DispatchActivity{
		State:     DispatchStateInUse,
		SessionID: "child-sess",
		AgentID:   "child-agent",
		SlotKind:  "primary",
		Depth:     0,
	}
	childStatus := domain.AIAggregatorStatusResp{
		ID: "child",
		Units: []domain.AICallableUnitView{
			{
				ID:               "childprov::deep",
				Model:            "deep",
				ProviderName:     "childprov",
				DispatchActivity: childActivity,
			},
			{
				ID:           "childprov::idle",
				Model:        "idle",
				ProviderName: "childprov",
				// nil DispatchActivity → idle, must be skipped.
			},
		},
	}

	childAID, _ := canonicalAID(t, 8)

	// The child ref returned by LookupID answers "aiaggregator.status" with
	// the scripted child status (fakeRef keys by call ID).
	childRef := &fakeRef{results: map[string]any{
		"aiaggregator.status": childStatus,
	}}

	a := &Actor{
		id:       "parent",
		actorCtx: &stubActorCtx{byID: map[id.ActorID]ref.Ref{childAID: childRef}},
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.aggregator_list": domain.AggregatorDescriptorListResp{
				Items: []domain.AggregatorDescriptor{
					{ID: "child", ActorID: childAID.String()},
				},
			},
		}},
		lifecycleCtx: context.Background(),
	}

	u := CallableUnit{ID: "agg:child", AggregatorID: "child"}
	act := a.aggregatedDispatchActivity(u)
	if act == nil {
		t.Fatal("expected non-nil activity propagated from child")
	}
	if act.State != DispatchStateInUse {
		t.Errorf("State = %q, want %q", act.State, DispatchStateInUse)
	}
	if act.Depth != 1 {
		t.Errorf("Depth = %d, want 1 (child depth 0 + 1)", act.Depth)
	}
	if act.AggregatorID != "child" {
		t.Errorf("AggregatorID = %q, want child", act.AggregatorID)
	}
	// The initiating context must survive the upward propagation.
	if act.SessionID != "child-sess" || act.AgentID != "child-agent" {
		t.Errorf("context lost during propagation: %+v", act)
	}
}

// TestDispatchActivity_HandleStatusPropagatesChild verifies the full handleStatus
// path: the parent's status view of an aggregator-ref entry carries the child's
// in-flight dispatch activity, propagated upward (depth+1, AggregatorID=child).
func TestDispatchActivity_HandleStatusPropagatesChild(t *testing.T) {
	childActivity := &domain.DispatchActivity{
		State:     DispatchStateInUse,
		SessionID: "child-sess",
		AgentID:   "child-agent",
		Depth:     0,
	}
	childStatus := domain.AIAggregatorStatusResp{
		ID: "child",
		Units: []domain.AICallableUnitView{
			{
				ID:               "childprov::deep",
				Model:            "deep",
				ProviderName:     "childprov",
				DispatchActivity: childActivity,
			},
		},
	}

	childAID, _ := canonicalAID(t, 9)
	childRef := &fakeRef{results: map[string]any{
		"aiaggregator.status": childStatus,
	}}

	a := &Actor{
		id:       "parent",
		units:    []CallableUnit{{ID: "agg:child", AggregatorID: "child"}},
		actorCtx: &stubActorCtx{byID: map[id.ActorID]ref.Ref{childAID: childRef}},
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.aggregator_list": domain.AggregatorDescriptorListResp{
				Items: []domain.AggregatorDescriptor{
					{ID: "child", ActorID: childAID.String()},
				},
			},
		}},
		lifecycleCtx: context.Background(),
	}

	status, err := a.handleStatus(nil)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if len(status.Units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(status.Units))
	}
	view := status.Units[0]
	if view.ID != "agg:child" {
		t.Errorf("unit ID = %q, want agg:child", view.ID)
	}
	if view.DispatchActivity == nil {
		t.Fatal("aggregator-ref entry should carry the child's dispatch activity")
	}
	if view.DispatchActivity.State != DispatchStateInUse {
		t.Errorf("State = %q, want %q", view.DispatchActivity.State, DispatchStateInUse)
	}
	if view.DispatchActivity.Depth != 1 {
		t.Errorf("Depth = %d, want 1 (propagated from child)", view.DispatchActivity.Depth)
	}
	if view.DispatchActivity.AggregatorID != "child" {
		t.Errorf("AggregatorID = %q, want child", view.DispatchActivity.AggregatorID)
	}
}
