package glassinteract_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/actor/glassinteract"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// slowWorkspaceActor is a workspace service stub whose workspace.agent_list_state
// takes sleep time to answer, simulating a slow/busy workspace RPC that the
// old inline Await held the glassinteract owner lane on.
type slowWorkspaceActor struct {
	actor.Host
	mu    sync.Mutex
	calls int
	sleep time.Duration
}

func (w *slowWorkspaceActor) Type() string { return "workspace" }

func (w *slowWorkspaceActor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("workspace.agent_list_state", w.handleAgentListState, actor.Public()); err != nil {
		return err
	}
	return ctx.RegisterDomain("workspace").Expose()
}

func (w *slowWorkspaceActor) handleAgentListState(actor.PureContext) (gen.WorkspaceAgentListState, error) {
	w.mu.Lock()
	w.calls++
	w.mu.Unlock()
	time.Sleep(w.sleep)
	return gen.WorkspaceAgentListState{
		Full: true,
		Items: []gen.AgentListItem{{
			ActorID:     "ag-1",
			DisplayName: "E2E Agent",
			AgentKind:   "worker",
			Runtime:     &gen.AgentRuntimeState{State: "running"},
		}},
	}, nil
}

func (w *slowWorkspaceActor) callCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

// invokeWithTimeout performs an in-process ref.Invoke and waits for the final
// response with an explicit budget; errors are reported via errOut.
func invokeWithTimeout(t *testing.T, h *runtime.Handle, callID string, payload any, budget time.Duration) ([]byte, error) {
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

	type result struct {
		raw []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		raw, err := call.RecvRaw()
		done <- result{raw, err}
	}()
	select {
	case r := <-done:
		return r.raw, r.err
	case <-time.After(budget):
		t.Fatalf("%s did not complete within %s — the glassinteract owner lane is blocked", callID, budget)
		return nil, nil
	}
}

// TestAgentPollTickDoesNotBlockBootstrapOrSessionList verifies the P1
// acceptance: while the 5s workspace agent poll is in flight (fetch stuck for
// ~2.5s), bootstrap and a session-read/list-class callable still answer fast.
// The old implementation Awaited workspace.agent_list_state inline on the
// owner lane, so both probes would have queued behind the fetch.
func TestAgentPollTickDoesNotBlockBootstrapOrSessionList(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	t.Setenv(glassinteract.BootstrapKeyEnv, "e2e-key")

	ws := &slowWorkspaceActor{sleep: 2500 * time.Millisecond}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{Name: "workspace", Factory: func() actor.Actor { return ws }, Role: "system"},
			{
				Name:              "glassinteract",
				Factory:           func() actor.Actor { return &glassinteract.Actor{} },
				RequirePersistent: true,
				Role:              "system",
				Planner:           true,
			},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap runtime: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()

	// Wait until the first agent poll tick has fired and its workspace fetch
	// is in flight (the stub answers only after sleeping).
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if ws.callCount() >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ws.callCount() < 1 {
		t.Fatal("agent poll tick never fired")
	}
	// The fetch is still sleeping for ~2s at this point.

	// bootstrap (stateless) must answer promptly while the fetch is pending.
	raw, err := invokeWithTimeout(t, handle, "glass_interact.bootstrap", map[string]any{
		"Key": "e2e-key", "DeviceId": "e2e-dev",
	}, 1500*time.Millisecond)
	if err != nil {
		t.Fatalf("bootstrap during poll fetch: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("bootstrap returned empty response")
	}

	// A session-read (owner-lane list/read-class) call must also answer
	// promptly — the poll's workspace fetch is still in flight.
	if _, err := invokeWithTimeout(t, handle, "glass_interact.session_get_state", nil, 1500*time.Millisecond); err != nil {
		t.Fatalf("session_get_state during poll fetch: %v", err)
	}

	// The tick chain must survive: a second poll fires after the interval.
	secondDeadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(secondDeadline) {
		if ws.callCount() >= 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("agent poll tick did not re-arm (calls=%d)", ws.callCount())
}
