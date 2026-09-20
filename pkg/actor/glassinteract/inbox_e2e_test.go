package glassinteract_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/actor/glassinteract"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// fakeCoordinatorActor stands in for the process-unique Coordinator agent. It
// exposes the agent.status surface the updater gates on and the explicit
// coordinator.ingest_glass_event intake contract. With autoComplete=true it
// additionally completes each delivered event back through
// glass_interact.event.complete (fire-and-forget), mirroring the real
// Coordinator intake handler.
type fakeCoordinatorActor struct {
	actor.Host
	mu           sync.Mutex
	delivered    []gen.GlassEventDeliverReq
	autoComplete bool
}

func (f *fakeCoordinatorActor) Type() string { return "agent" }

func (f *fakeCoordinatorActor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("agent_status", func(_ actor.PureContext) (gen.AgentStatusResp, error) {
		return gen.AgentStatusResp{State: "idle"}, nil
	}, actor.Public()); err != nil {
		return err
	}
	if err := ctx.Register("coordinator_ingest_glass_event", func(c actor.Context, req gen.GlassEventDeliverReq) (gen.GlassEventDeliverResp, error) {
		f.mu.Lock()
		f.delivered = append(f.delivered, req)
		f.mu.Unlock()
		if f.autoComplete {
			// Mirror the real Coordinator intake: ack receipt, then complete the
			// outcome back through glass_interact.event.complete asynchronously
			// so the delivery await on the glassinteract owner loop never
			// deadlocks.
			lifecycle := c.Lifecycle()
			go func() {
				if glassRef, ok := c.LookupService("glass_interact"); ok {
					call := glassRef.Invoke(lifecycle, "glass_interact.event_complete", gen.GlassEventCompleteReq{EventID: req.EventID, Outcome: "handled"})
					_, _ = call.Final(lifecycle)
				}
			}()
		}
		return gen.GlassEventDeliverResp{EventID: req.EventID, Received: true}, nil
	}, actor.Internal()); err != nil {
		return err
	}
	return ctx.RegisterDomain("coordinator").Expose()
}

func (f *fakeCoordinatorActor) deliveredSnapshot() []gen.GlassEventDeliverReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gen.GlassEventDeliverReq(nil), f.delivered...)
}

// fakeWorkspaceCoordinator resolves the coordinator actor id dynamically so
// the delivery path reaches the real fake coordinator.
type fakeWorkspaceCoordinator struct {
	actor.Host
}

func (f *fakeWorkspaceCoordinator) Type() string { return "workspace" }

func (f *fakeWorkspaceCoordinator) OnStart(ctx actor.Context) error {
	if err := ctx.Register("workspace.coordinator_lookup", func(_ actor.PureContext) (gen.WorkspaceCoordinatorLookupResp, error) {
		ref, ok := ctx.LookupService("coordinator")
		if !ok {
			return gen.WorkspaceCoordinatorLookupResp{}, nil
		}
		return gen.WorkspaceCoordinatorLookupResp{Found: true, Nickname: "小明", ActorID: ref.ID().String()}, nil
	}, actor.Internal()); err != nil {
		return err
	}
	return ctx.RegisterDomain("workspace").Expose()
}

// TestGlassInboxEndToEnd drives the Stage 5 inbox over the real runtime:
// bootstrap + claim, enqueue, the session-gated updater's immediate check
// delivering exactly one event to the Coordinator intake, the Coordinator
// receipt completing the event, and the debug/inbox projections.
func TestGlassInboxEndToEnd(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	t.Setenv("SPOREMIND_GLASS_KEY", "e2e-key")

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	coord := &fakeCoordinatorActor{}
	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{Name: "glassinteract", Factory: func() actor.Actor { return &glassinteract.Actor{} }, RequirePersistent: true, Role: "system", Planner: true},
			{Name: "coordinator", Factory: func() actor.Actor { return coord }, Role: "system"},
			{Name: "workspace", Factory: func() actor.Actor { return &fakeWorkspaceCoordinator{} }, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()
	time.Sleep(500 * time.Millisecond)

	// Bootstrap + claim a glass session.
	raw, err := invokeCall(t, handle, "glass_interact.bootstrap", map[string]any{
		"Key":      "e2e-key",
		"DeviceId": "dev-e2e",
	}, "", "")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	var boot struct {
		SessionID string `json:"SessionId"`
	}
	if err := json.Unmarshal(raw, &boot); err != nil {
		t.Fatalf("bootstrap parse: %v", err)
	}
	if _, err := invokeCall(t, handle, "glass_interact.session_claim", map[string]any{
		"SessionId": boot.SessionID,
		"DeviceId":  "dev-e2e",
	}, "glass", boot.SessionID); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Enqueue an event; the immediate updater check delivers it synchronously.
	enqRaw, err := invokeCall(t, handle, "glass_interact.event_enqueue", map[string]any{
		"Source": "turn", "Kind": "complete", "Priority": "normal", "Payload": `{"turn":"t1"}`,
	}, "", "")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var enq struct {
		Accepted bool   `json:"Accepted"`
		EventID  string `json:"EventId"`
	}
	if err := json.Unmarshal(enqRaw, &enq); err != nil {
		t.Fatalf("enqueue parse: %v raw=%s", err, enqRaw)
	}
	if !enq.Accepted || enq.EventID == "" {
		t.Fatalf("enqueue resp = %+v", enq)
	}

	// The Coordinator received exactly one delivery with the event payload.
	delivered := coord.deliveredSnapshot()
	if len(delivered) != 1 {
		t.Fatalf("coordinator deliveries = %d, want 1", len(delivered))
	}
	if delivered[0].EventID != enq.EventID || delivered[0].Payload != `{"turn":"t1"}` || delivered[0].Source != "turn" {
		t.Fatalf("delivered payload = %+v", delivered[0])
	}

	// The inbox reports the event in_flight awaiting the receipt.
	inboxRaw, err := invokeCall(t, handle, "glass_interact.inbox_state", map[string]any{}, "", "")
	if err != nil {
		t.Fatalf("inbox.state: %v", err)
	}
	var inbox struct {
		Snapshot struct {
			Total    int64 `json:"Total"`
			Pending  int64 `json:"Pending"`
			InFlight int64 `json:"InFlight"`
		} `json:"Snapshot"`
	}
	if err := json.Unmarshal(inboxRaw, &inbox); err != nil {
		t.Fatalf("inbox.state parse: %v raw=%s", err, inboxRaw)
	}
	if inbox.Snapshot.Total != 1 || inbox.Snapshot.InFlight != 1 || inbox.Snapshot.Pending != 0 {
		t.Fatalf("inbox after delivery = %+v", inbox.Snapshot)
	}

	// Coordinator receipt: handled -> terminal.
	compRaw, err := invokeCall(t, handle, "glass_interact.event_complete", map[string]any{
		"EventId": enq.EventID, "Outcome": "handled",
	}, "", "")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	var comp struct {
		State string `json:"State"`
	}
	if err := json.Unmarshal(compRaw, &comp); err != nil {
		t.Fatalf("complete parse: %v raw=%s", err, compRaw)
	}
	if comp.State != "completed" {
		t.Fatalf("complete state = %q, want completed", comp.State)
	}

	// Debug state exposes the inbox snapshot + HUD whitelist projection.
	dbgRaw, err := invokeCall(t, handle, "glass_interact.debug_state", map[string]any{}, "", "")
	if err != nil {
		t.Fatalf("debug.state: %v", err)
	}
	var dbg struct {
		State struct {
			Inbox *struct {
				Completed int64 `json:"Completed"`
			} `json:"Inbox"`
			Hud *struct {
				Connection   string `json:"Connection"`
				AgentRunning bool   `json:"AgentRunning"`
			} `json:"Hud"`
			Deliveries []struct {
				EventID string `json:"EventId"`
				Success bool   `json:"Success"`
			} `json:"Deliveries"`
		} `json:"State"`
	}
	if err := json.Unmarshal(dbgRaw, &dbg); err != nil {
		t.Fatalf("debug parse: %v raw=%s", err, dbgRaw)
	}
	if dbg.State.Inbox == nil || dbg.State.Inbox.Completed != 1 {
		t.Fatalf("debug inbox = %+v", dbg.State.Inbox)
	}
	if dbg.State.Hud == nil || dbg.State.Hud.Connection != "online" || dbg.State.Hud.AgentRunning {
		t.Fatalf("debug hud = %+v", dbg.State.Hud)
	}
	if len(dbg.State.Deliveries) != 1 || !dbg.State.Deliveries[0].Success || dbg.State.Deliveries[0].EventID != enq.EventID {
		t.Fatalf("debug deliveries = %+v", dbg.State.Deliveries)
	}
}

// TestGlassInboxEndToEndCoordinatorCompletes drives the full Stage 5 loop over
// the real runtime with a Coordinator that behaves like the real intake
// handler: it acks the delivery and then completes the event back through
// glass_interact.event.complete on its own. The inbox must reach a terminal
// completed state without the test calling complete manually.
func TestGlassInboxEndToEndCoordinatorCompletes(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	t.Setenv("SPOREMIND_GLASS_KEY", "e2e-key")

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	coord := &fakeCoordinatorActor{autoComplete: true}
	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{Name: "glassinteract", Factory: func() actor.Actor { return &glassinteract.Actor{} }, RequirePersistent: true, Role: "system", Planner: true},
			{Name: "coordinator", Factory: func() actor.Actor { return coord }, Role: "system"},
			{Name: "workspace", Factory: func() actor.Actor { return &fakeWorkspaceCoordinator{} }, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()
	time.Sleep(500 * time.Millisecond)

	// Bootstrap + claim a glass session.
	raw, err := invokeCall(t, handle, "glass_interact.bootstrap", map[string]any{
		"Key":      "e2e-key",
		"DeviceId": "dev-e2e",
	}, "", "")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	var boot struct {
		SessionID string `json:"SessionId"`
	}
	if err := json.Unmarshal(raw, &boot); err != nil {
		t.Fatalf("bootstrap parse: %v", err)
	}
	if _, err := invokeCall(t, handle, "glass_interact.session_claim", map[string]any{
		"SessionId": boot.SessionID,
		"DeviceId":  "dev-e2e",
	}, "glass", boot.SessionID); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Enqueue an event; the immediate updater check delivers it and the
	// Coordinator completes it asynchronously.
	enqRaw, err := invokeCall(t, handle, "glass_interact.event_enqueue", map[string]any{
		"Source": "turn", "Kind": "complete", "Priority": "normal", "Payload": `{"turn":"t1"}`,
	}, "", "")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var enq struct {
		Accepted bool   `json:"Accepted"`
		EventID  string `json:"EventId"`
	}
	if err := json.Unmarshal(enqRaw, &enq); err != nil {
		t.Fatalf("enqueue parse: %v raw=%s", err, enqRaw)
	}
	if !enq.Accepted || enq.EventID == "" {
		t.Fatalf("enqueue resp = %+v", enq)
	}

	// The Coordinator received exactly one delivery.
	delivered := coord.deliveredSnapshot()
	if len(delivered) != 1 {
		t.Fatalf("coordinator deliveries = %d, want 1", len(delivered))
	}
	if delivered[0].EventID != enq.EventID || delivered[0].Payload != `{"turn":"t1"}` {
		t.Fatalf("delivered payload = %+v", delivered[0])
	}

	// Poll: the Coordinator's own completion must drive the inbox to a terminal
	// completed state.
	deadline := time.Now().Add(5 * time.Second)
	for {
		inboxRaw, err := invokeCall(t, handle, "glass_interact.inbox_state", map[string]any{}, "", "")
		if err != nil {
			t.Fatalf("inbox.state: %v", err)
		}
		var inbox struct {
			Snapshot struct {
				Total     int64 `json:"Total"`
				Pending   int64 `json:"Pending"`
				InFlight  int64 `json:"InFlight"`
				Completed int64 `json:"Completed"`
			} `json:"Snapshot"`
		}
		if err := json.Unmarshal(inboxRaw, &inbox); err != nil {
			t.Fatalf("inbox.state parse: %v raw=%s", err, inboxRaw)
		}
		if inbox.Snapshot.Completed == 1 && inbox.Snapshot.InFlight == 0 && inbox.Snapshot.Pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("inbox never reached completed: %+v", inbox.Snapshot)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
