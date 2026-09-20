package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHandleSchedulerBind_MountsModeAndRecordsEntry verifies the happy path:
// binding a scheduler card mounts builtin:mode:scheduler (badge surface) and
// records the AcitiveScheduler entry on the agent.
func TestHandleSchedulerBind_MountsModeAndRecordsEntry(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{cardRefs: []gen.CardRef{}}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleSchedulerBind(ctx, gen.AgentSchedulerBindReq{
		SchedulerCardID: "scheduler:daily-sync",
		SchedulerName:   "Daily Sync",
	})
	if err != nil {
		t.Fatalf("handleSchedulerBind: %v", err)
	}
	if len(resp.ActiveScheduler) != 1 || resp.ActiveScheduler[0].SchedulerCardID != "scheduler:daily-sync" {
		t.Fatalf("ActiveScheduler = %+v, want one daily-sync entry", resp.ActiveScheduler)
	}
	if !a.cardRefEnabled(schedulerModeCardID) {
		t.Fatal("expected builtin:mode:scheduler mounted after bind")
	}
	if len(a.RawSession.ActiveScheduler) != 1 {
		t.Fatalf("RawSession.ActiveScheduler = %+v, want 1 entry", a.RawSession.ActiveScheduler)
	}
}

// TestHandleSchedulerBind_UpsertMultipleCards verifies one agent can hold
// several scheduler bindings and re-binding the same card refreshes in place.
func TestHandleSchedulerBind_UpsertMultipleCards(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{cardRefs: []gen.CardRef{}}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	for _, ent := range []gen.AgentSchedulerBindReq{
		{SchedulerCardID: "scheduler:a", SchedulerName: "A"},
		{SchedulerCardID: "scheduler:b", SchedulerName: "B"},
		{SchedulerCardID: "scheduler:a", SchedulerName: "A-v2"}, // refresh
	} {
		if _, err := a.handleSchedulerBind(ctx, ent); err != nil {
			t.Fatalf("handleSchedulerBind(%q): %v", ent.SchedulerCardID, err)
		}
	}
	got := a.RawSession.ActiveScheduler
	if len(got) != 2 {
		t.Fatalf("ActiveScheduler len = %d, want 2: %+v", len(got), got)
	}
	for _, e := range got {
		if e.SchedulerCardID == "scheduler:a" && e.SchedulerName != "A-v2" {
			t.Fatalf("scheduler:a name = %q, want refreshed A-v2", e.SchedulerName)
		}
	}
}

// TestHandleSchedulerUnbind_RemovesOneEntryKeepsMode verifies unbinding one of
// several cards keeps the mode mounted and the other bindings intact.
func TestHandleSchedulerUnbind_RemovesOneEntryKeepsMode(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveScheduler: []gen.ActiveSchedulerEntry{
				{SchedulerCardID: "scheduler:a"},
				{SchedulerCardID: "scheduler:b"},
			},
		},
		cardRefs: []gen.CardRef{{ID: schedulerModeCardID, Scope: "user"}},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleSchedulerUnbind(ctx, gen.AgentSchedulerUnbindReq{SchedulerCardID: "scheduler:a"})
	if err != nil {
		t.Fatalf("handleSchedulerUnbind: %v", err)
	}
	if len(resp.ActiveScheduler) != 1 || resp.ActiveScheduler[0].SchedulerCardID != "scheduler:b" {
		t.Fatalf("ActiveScheduler = %+v, want only scheduler:b", resp.ActiveScheduler)
	}
	if !a.cardRefEnabled(schedulerModeCardID) {
		t.Fatal("expected scheduler mode to stay mounted while other bindings remain")
	}
}

// TestHandleSchedulerUnbind_LastBindingUnmountsMode verifies the mode is
// unmounted (and the binding list cleared) when the last binding is removed.
func TestHandleSchedulerUnbind_LastBindingUnmountsMode(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveScheduler: []gen.ActiveSchedulerEntry{
				{SchedulerCardID: "scheduler:a"},
			},
		},
		cardRefs: []gen.CardRef{{ID: schedulerModeCardID, Scope: "user"}},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleSchedulerUnbind(ctx, gen.AgentSchedulerUnbindReq{SchedulerCardID: "scheduler:a"})
	if err != nil {
		t.Fatalf("handleSchedulerUnbind: %v", err)
	}
	if len(resp.ActiveScheduler) != 0 {
		t.Fatalf("ActiveScheduler = %+v, want empty", resp.ActiveScheduler)
	}
	if a.cardRefEnabled(schedulerModeCardID) {
		t.Fatal("expected scheduler mode unmounted after last binding removed")
	}
	if a.RawSession.ActiveScheduler != nil {
		t.Fatalf("RawSession.ActiveScheduler = %+v, want nil", a.RawSession.ActiveScheduler)
	}
}

// TestHandleSchedulerUnbind_UnknownCardIsNoOp verifies unbinding a card that is
// not bound leaves the other bindings and the mode untouched.
func TestHandleSchedulerUnbind_UnknownCardIsNoOp(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveScheduler: []gen.ActiveSchedulerEntry{
				{SchedulerCardID: "scheduler:b"},
			},
		},
		cardRefs: []gen.CardRef{{ID: schedulerModeCardID, Scope: "user"}},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleSchedulerUnbind(ctx, gen.AgentSchedulerUnbindReq{SchedulerCardID: "scheduler:nope"})
	if err != nil {
		t.Fatalf("handleSchedulerUnbind: %v", err)
	}
	if len(resp.ActiveScheduler) != 1 {
		t.Fatalf("ActiveScheduler = %+v, want scheduler:b intact", resp.ActiveScheduler)
	}
}

func TestHandleSchedulerBind_RequiresCardID(t *testing.T) {
	a := &Actor{cardRefs: []gen.CardRef{}}
	_, err := a.handleSchedulerBind(testutil.AnonCtx(testutil.GenActorID()), gen.AgentSchedulerBindReq{})
	if err == nil || !strings.Contains(err.Error(), "SchedulerCardId is required") {
		t.Fatalf("error = %v, want 'SchedulerCardId is required'", err)
	}
}

func TestHandleSchedulerUnbind_RequiresCardID(t *testing.T) {
	a := &Actor{cardRefs: []gen.CardRef{}}
	_, err := a.handleSchedulerUnbind(testutil.AnonCtx(testutil.GenActorID()), gen.AgentSchedulerUnbindReq{})
	if err == nil || !strings.Contains(err.Error(), "SchedulerCardId is required") {
		t.Fatalf("error = %v, want 'SchedulerCardId is required'", err)
	}
}

// TestHandleSchedulerUnbind_UnmountFailureStillFlushesState pins the failure
// path fixed by wiring #5: when removing the last binding tries to unmount
// builtin:mode:scheduler but the unmount is refused (here the card resolves as
// a protected builtin), the handler must not early-return. It must still flush
// the mailbox and notify the workspace so the in-memory binding list and the
// persisted session never diverge — a failed unmount degrades to a log line.
func TestHandleSchedulerUnbind_UnmountFailureStillFlushesState(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())
	// Redirect the package-level agent store into the test data dir so the
	// mailbox flush is observable without touching the real state dir.
	oldStore := agentStore
	agentStore = persist.NewFSPersist(filepath.Join(config.ActorDataDir(), "agent"))
	t.Cleanup(func() { agentStore = oldStore })

	notified := make(chan string, 4)
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		notified <- callID
		return nil
	})
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	// Resolve builtin:mode:scheduler as a protected builtin descriptor so the
	// unmount is refused, forcing the degraded path under test.
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
			if callID == "workspace.component_get" {
				return domain.ProjectComponentGetResp{Component: domain.ComponentDescriptor{
					Ref:       domain.ComponentRef{CardID: schedulerModeCardID},
					Protected: true,
				}}, nil
			}
			return nil, nil
		}}
	}

	a := &Actor{
		actorID: "sched-unbind-fail",
		RawSession: domain.RawSession{
			ActiveScheduler: []gen.ActiveSchedulerEntry{
				{SchedulerCardID: "scheduler:a", SchedulerName: "A"},
			},
		},
		cardRefs: []gen.CardRef{{ID: schedulerModeCardID, Scope: "builtin"}},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	// Seed the persisted mailbox with the binding so the test can prove the
	// failure path flushes the cleared list rather than leaving it stale.
	a.saveMailbox(ctx)
	var seeded struct {
		RawSession domain.RawSession `json:"rawSession"`
	}
	if err := agentStore.Load(a.actorID, &seeded); err != nil {
		t.Fatalf("seed load: %v", err)
	}
	if len(seeded.RawSession.ActiveScheduler) != 1 {
		t.Fatalf("seed persisted ActiveScheduler = %+v, want 1 entry", seeded.RawSession.ActiveScheduler)
	}

	resp, err := a.handleSchedulerUnbind(ctx, gen.AgentSchedulerUnbindReq{SchedulerCardID: "scheduler:a"})
	if err != nil {
		t.Fatalf("handleSchedulerUnbind must degrade a refused unmount to a log, got: %v", err)
	}
	if len(resp.ActiveScheduler) != 0 {
		t.Fatalf("resp.ActiveScheduler = %+v, want empty", resp.ActiveScheduler)
	}
	if a.RawSession.ActiveScheduler != nil {
		t.Fatalf("in-memory ActiveScheduler = %+v, want nil", a.RawSession.ActiveScheduler)
	}
	// The refused unmount leaves the mode mounted.
	if !a.cardRefEnabled(schedulerModeCardID) {
		t.Fatal("expected scheduler mode to stay mounted after a refused unmount")
	}

	// saveMailbox must have run despite the refused unmount: the persisted
	// binding list matches the cleared in-memory one.
	var persisted struct {
		RawSession domain.RawSession `json:"rawSession"`
	}
	if err := agentStore.Load(a.actorID, &persisted); err != nil {
		t.Fatalf("agentStore.Load: %v", err)
	}
	if len(persisted.RawSession.ActiveScheduler) != 0 {
		t.Fatalf("persisted ActiveScheduler = %+v, want empty (memory/persistence fork)", persisted.RawSession.ActiveScheduler)
	}

	// notifyWorkspaceStatus must also have run (it is the async follow-up to
	// the flush).
	select {
	case callID := <-notified:
		if callID != "workspace.agent_status_update" {
			t.Fatalf("notified call = %q, want workspace.agent_status_update", callID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notifyWorkspaceStatus did not run on the unmount-failure path")
	}
}
