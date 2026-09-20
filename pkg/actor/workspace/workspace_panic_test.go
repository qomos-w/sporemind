package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// capturingOracleRef records every Invoke as a serialized payload, then
// signals a channel from Close so tests can deterministically wait for the
// fire-and-forget report goroutine.
type capturingOracleRef struct {
	actorID id.ActorID
	calls   [][]byte
	closeCh chan struct{}
}

func newCapturingOracleRef() *capturingOracleRef {
	return &capturingOracleRef{
		actorID: testutil.GenActorID(),
		closeCh: make(chan struct{}, 16),
	}
}

func (c *capturingOracleRef) ID() id.ActorID        { return c.actorID }
func (*capturingOracleRef) Service() (string, bool) { return "oracle", true }
func (c *capturingOracleRef) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	if callID != "oracle.report_diagnostic" {
		return nil
	}
	var raw []byte
	switch v := payload.(type) {
	case []byte:
		raw = v
	case json.RawMessage:
		raw = v
	default:
		raw, _ = json.Marshal(payload)
	}
	c.calls = append(c.calls, raw)
	stream := &oracleFakeStream{}
	call := invoke.NewCall(invoke.CallModeUnary, stream)
	go func() {
		_ = call.Close()
		c.closeCh <- struct{}{}
	}()
	return call
}

type oracleFakeStream struct{}

func (*oracleFakeStream) Recv() (any, error)       { return nil, nil }
func (*oracleFakeStream) RecvRaw() ([]byte, error) { return nil, nil }
func (*oracleFakeStream) Close() error             { return nil }

var _ ref.Ref = (*capturingOracleRef)(nil)

// TestHandleCreateAgent_PanicPreservesRunningAgents verifies that when
// handleCreateAgent panics mid-execution, the Guard wrapper:
//  1. Returns an error containing "panic" (not propagating the panic to the
//     gospore supervisor, which would Restart the workspace and flip every
//     running agent to "paused").
//  2. Leaves existing agents' Status untouched ("running" stays "running").
//  3. Fires one oracle.report_diagnostic with Source="workspace.create_agent"
//     and Severity="error".
func TestHandleCreateAgent_PanicPreservesRunningAgents(t *testing.T) {
	a, ctx := freshActor(t)

	existingActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:        "existing#1",
		AgentKind: domain.AgentKindCoder,
		ActorID:   existingActorID.String(),
		Status:    "running",
		LoadState: "loaded",
	}}

	projectActorID := testutil.GenActorID()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectActorID.String()},
	}

	oracleRef := newCapturingOracleRef()
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "oracle" {
			return oracleRef, true
		}
		return nil, false
	}

	// Inject panic when spawnAgentViaProject resolves the project actor via
	// LookupID. Compare by string because the handler round-trips through
	// identity.ParseCanonicalID → id.From, which may produce a different
	// id.ActorID value for the same logical actor.
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == projectActorID.String() {
			panic("injected-panic-for-test")
		}
		return lookupOK(aid)
	}

	_, err := a.handleCreateAgent(ctx, domain.WorkspaceCreateAgentReq{
		ProjectID:   projectActorID.String(),
		DisplayName: "New Agent",
		AgentKind:   "coder",
	})
	if err == nil {
		t.Fatal("expected error from Guard on panic, got nil")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("error %q should contain 'panic'", err.Error())
	}
	if !strings.Contains(err.Error(), "workspace.create_agent") {
		t.Errorf("error %q should contain the source prefix", err.Error())
	}

	// Critical: existing running agent's Status must be unchanged. Without
	// Guard, supervisor Restart would re-run OnStart and rewrite Status=="running"
	// to "paused" — the "all paused" symptom the user reported.
	if a.Agents[0].Status != "running" {
		t.Errorf("existing agent Status = %q, want running (Guard should prevent supervisor Restart)", a.Agents[0].Status)
	}

	select {
	case <-oracleRef.closeCh:
	case <-time.After(2 * time.Second):
		t.Fatal("oracle.report_diagnostic never arrived")
	}
	if len(oracleRef.calls) == 0 {
		t.Fatal("no captured oracle calls")
	}
	var diag domain.OracleReportDiagnosticReq
	if err := json.Unmarshal(oracleRef.calls[len(oracleRef.calls)-1], &diag); err != nil {
		t.Fatalf("unmarshal diagnostic: %v", err)
	}
	if diag.Severity != "error" {
		t.Errorf("Severity = %q, want error", diag.Severity)
	}
	if diag.Source != "workspace.create_agent" {
		t.Errorf("Source = %q, want workspace.create_agent", diag.Source)
	}
	if !strings.Contains(diag.Message, "injected-panic-for-test") {
		t.Errorf("Message %q should contain the panic value", diag.Message)
	}
	if !strings.Contains(diag.RawData, "--- stack ---") {
		t.Errorf("RawData should contain stack section, got: %s", diag.RawData)
	}
}

// TestHandleAgentSpawnByType_PanicPreservesRunningAgents is the same regression
// test for the fork-child spawn path — the other primary spawn entry point.
func TestHandleAgentSpawnByType_PanicPreservesRunningAgents(t *testing.T) {
	a, ctx := freshActor(t)

	existingActorID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{
		ID:        "existing#1",
		AgentKind: domain.AgentKindCoder,
		ActorID:   existingActorID.String(),
		Status:    "running",
		LoadState: "loaded",
		ProjectID: testutil.GenActorID().String(),
	}}

	oracleRef := newCapturingOracleRef()
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "oracle" {
			return oracleRef, true
		}
		return nil, false
	}

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == existingActorID.String() {
			panic("injected-spawn-panic")
		}
		return lookupOK(aid)
	}

	_, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
		AgentKind:     "explorer",
		CallerAgentID: existingActorID.String(),
		Description:   "explore child",
	})
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected error containing 'panic', got %v", err)
	}
	if a.Agents[0].Status != "running" {
		t.Errorf("existing agent Status = %q, want running", a.Agents[0].Status)
	}

	select {
	case <-oracleRef.closeCh:
	case <-time.After(2 * time.Second):
		t.Fatal("oracle.report_diagnostic never arrived")
	}
	if len(oracleRef.calls) == 0 {
		t.Fatal("no captured oracle calls")
	}
	var diag domain.OracleReportDiagnosticReq
	if err := json.Unmarshal(oracleRef.calls[0], &diag); err != nil {
		t.Fatalf("unmarshal diagnostic: %v", err)
	}
	if diag.Source != "workspace.agent_spawn_by_type" {
		t.Errorf("Source = %q, want workspace.agent_spawn_by_type", diag.Source)
	}
}
