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

// slowGateCoordinatorActor stands in for the Coordinator. Its agent.status is
// flippable between running and idle (to gate the updater off the inline
// enqueue path) and its ingest_glass_event intake blocks in delivery until
// released, signalling started when a delivery begins.
type slowGateCoordinatorActor struct {
	actor.Host
	mu      sync.Mutex
	running bool
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *slowGateCoordinatorActor) Type() string { return "agent" }

func (c *slowGateCoordinatorActor) setRunning(v bool) {
	c.mu.Lock()
	c.running = v
	c.mu.Unlock()
}

func (c *slowGateCoordinatorActor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("agent_status", func(_ actor.PureContext) (gen.AgentStatusResp, error) {
		c.mu.Lock()
		running := c.running
		c.mu.Unlock()
		state := "idle"
		if running {
			state = "running"
		}
		return gen.AgentStatusResp{State: state}, nil
	}, actor.Public()); err != nil {
		return err
	}
	if err := ctx.Register("coordinator_ingest_glass_event", func(cx actor.Context, req gen.GlassEventDeliverReq) (gen.GlassEventDeliverResp, error) {
		c.once.Do(func() { close(c.started) })
		select {
		case <-c.release:
		case <-cx.Lifecycle().Done():
		}
		return gen.GlassEventDeliverResp{EventID: req.EventID, Received: true}, nil
	}, actor.Internal()); err != nil {
		return err
	}
	return ctx.RegisterDomain("coordinator").Expose()
}

// invokeWithinBudget invokes callID in-process and reports whether the call
// completed within budget, regardless of handler-level errors. It probes
// owner-lane responsiveness while another lane is blocked.
func invokeWithinBudget(t *testing.T, h *runtime.Handle, callID string, payload any, budget time.Duration) bool {
	t.Helper()
	ref, ok := h.App().LookupService("glass_interact")
	if !ok {
		t.Fatal("glassinteract service not found")
	}
	call := ref.Invoke(context.Background(), callID, payload)
	if call == nil {
		t.Fatalf("invoke %s returned nil call", callID)
	}
	defer call.Close()
	done := make(chan struct{})
	go func() {
		_, _ = call.RecvRaw()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(budget):
		return false
	}
}

// TestUpdaterTickOnDedicatedLaneKeepsOwnerLaneFree proves the session-gated
// updater runs on the glass_updater lane: while the scheduled tick is blocked
// inside a slow coordinator delivery, an owner-lane callable still answers
// promptly. With the tick pinned to the owner lane the probe would queue behind
// the delivery and time out.
func TestUpdaterTickOnDedicatedLaneKeepsOwnerLaneFree(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	t.Setenv(glassinteract.BootstrapKeyEnv, "e2e-key")

	coord := &slowGateCoordinatorActor{
		running: true, // gate the updater off the inline enqueue path
		started: make(chan struct{}),
		release: make(chan struct{}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{Name: "glassinteract", Factory: func() actor.Actor { return &glassinteract.Actor{} }, RequirePersistent: true, Role: "system", Planner: true},
			{Name: "coordinator", Factory: func() actor.Actor { return coord }, Role: "system"},
			{Name: "workspace", Factory: func() actor.Actor { return &fakeWorkspaceCoordinator{} }, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap runtime: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()
	time.Sleep(500 * time.Millisecond)

	// Bootstrap + claim. The immediate claim check is gated out (running).
	raw, err := invokeCall(t, handle, "glass_interact.bootstrap", map[string]any{
		"Key": "e2e-key", "DeviceId": "dev-lane",
	}, "", "")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	var boot struct {
		SessionID string `json:"SessionId"`
	}
	if err := json.Unmarshal(raw, &boot); err != nil {
		t.Fatalf("bootstrap parse: %v raw=%s", err, raw)
	}
	if _, err := invokeCall(t, handle, "glass_interact.session_claim", map[string]any{
		"SessionId": boot.SessionID, "DeviceId": "dev-lane",
	}, "glass", boot.SessionID); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Enqueue one event while the coordinator reports running: the inline
	// immediate check is gated out, leaving the event pending for the scheduled
	// tick.
	if _, err := invokeCall(t, handle, "glass_interact.event_enqueue", map[string]any{
		"Source": "turn", "Kind": "complete", "Priority": "normal", "Payload": `{"turn":"t1"}`,
	}, "", ""); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Flip the gate idle so the next scheduled updater tick (glass_updater
	// lane) delivers the pending event.
	coord.setRunning(false)

	select {
	case <-coord.started:
	case <-time.After(12 * time.Second):
		t.Fatal("scheduled updater never delivered the pending event")
	}

	// The delivery is now blocked inside ingest_glass_event on the
	// glass_updater lane. An owner-lane callable must still answer promptly;
	// a bogus event_complete is a no-mutation owner-lane probe.
	if !invokeWithinBudget(t, handle, "glass_interact.event_complete", map[string]any{
		"EventId": "missing-owner-lane-probe", "Outcome": "handled",
	}, 1500*time.Millisecond) {
		t.Fatal("owner lane blocked while the updater delivery was in flight")
	}
	close(coord.release)
}
