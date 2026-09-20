package project

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// agentActionWorkspaceRef responds to workspace.list_agents with a fixed agent
// list and counts workspace.agent_pause/workspace.agent_resume calls so tests
// can assert target and CallerAgentID.
type agentActionWorkspaceRef struct {
	actorID string
	agents  []gen.AgentRef

	mu           sync.Mutex
	pauseCalls   int
	resumeCalls  int
	lastTargetID string
	lastCallerID string
}

func (r *agentActionWorkspaceRef) ID() id.ActorID          { return id.ActorID{} }
func (r *agentActionWorkspaceRef) Service() (string, bool) { return "workspace", true }
func (r *agentActionWorkspaceRef) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	switch callID {
	case "workspace.list_agents":
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: gen.AgentRefListResp{Items: r.agents}})
	case "workspace.agent_pause", "workspace.agent_resume":
		r.mu.Lock()
		if callID == "workspace.agent_pause" {
			r.pauseCalls++
		} else {
			r.resumeCalls++
		}
		switch req := payload.(type) {
		case gen.AgentPauseReq:
			r.lastTargetID = req.ToAgentID
			r.lastCallerID = req.CallerAgentID
		case gen.AgentResumeReq:
			r.lastTargetID = req.ToAgentID
			r.lastCallerID = req.CallerAgentID
		}
		r.mu.Unlock()
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: gen.AgentPauseResp{Sent: true}})
	}
	return nil
}

func (r *agentActionWorkspaceRef) calls() (pause, resume int, target, caller string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pauseCalls, r.resumeCalls, r.lastTargetID, r.lastCallerID
}

// agentActionCtx builds a FakeCtx whose LookupService resolves only "workspace"
// to the given mock ref.
func agentActionCtx(wsRef *agentActionWorkspaceRef) *testutil.FakeCtx {
	return &testutil.FakeCtx{
		LookupServiceFn: func(name string) (ref.Ref, bool) {
			if name == "workspace" && wsRef != nil {
				return wsRef, true
			}
			return nil, false
		},
	}
}

const agentActionTestAgentID = "019efa0a000000000000000000000001"

func TestExecuteAgentActionTimerCard_Pause(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	wsRef := &agentActionWorkspaceRef{
		agents: []gen.AgentRef{{ID: "coder", ActorID: agentActionTestAgentID, LoadState: "loaded"}},
	}
	ctx := agentActionCtx(wsRef)

	card := schedulerCard("scheduler:actn",
		"  schedule_type: agent_action\n  agent_action: pause\n  target_agent: agent:coder", "Body.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	if err := a.executeAgentActionTimerCard(ctx, card); err != nil {
		t.Fatalf("executeAgentActionTimerCard: %v", err)
	}

	pauseCalls, resumeCalls, target, caller := wsRef.calls()
	if pauseCalls != 1 || resumeCalls != 0 {
		t.Fatalf("pause/resume calls = %d/%d, want 1/0", pauseCalls, resumeCalls)
	}
	if target != agentActionTestAgentID {
		t.Fatalf("target = %q, want %q", target, agentActionTestAgentID)
	}
	// Self-authorization: CallerAgentID carries the target's own ActorID so the
	// workspace requireOwnerOrSelf check passes for internal invocations.
	if caller != agentActionTestAgentID {
		t.Fatalf("CallerAgentID = %q, want %q", caller, agentActionTestAgentID)
	}

	saved, err := a.store.Get("scheduler:actn")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusIdle {
		t.Fatalf("run_status = %q, want idle", got)
	}
	if got := cardDataString(saved, "last_run"); got == "" {
		t.Fatal("last_run not set")
	}
	if got := cardDataInt(saved, "run_count"); got != 1 {
		t.Fatalf("run_count = %d, want 1", got)
	}
}

func TestExecuteAgentActionTimerCard_Resume(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	wsRef := &agentActionWorkspaceRef{
		agents: []gen.AgentRef{{ID: "coder", ActorID: agentActionTestAgentID, LoadState: "loaded"}},
	}
	ctx := agentActionCtx(wsRef)

	card := schedulerCard("scheduler:actn",
		"  schedule_type: agent_action\n  agent_action: resume\n  target_agent: agent:coder", "Body.")

	if err := a.executeAgentActionTimerCard(ctx, card); err != nil {
		t.Fatalf("executeAgentActionTimerCard: %v", err)
	}

	pauseCalls, resumeCalls, target, _ := wsRef.calls()
	if resumeCalls != 1 || pauseCalls != 0 {
		t.Fatalf("pause/resume calls = %d/%d, want 0/1", pauseCalls, resumeCalls)
	}
	if target != agentActionTestAgentID {
		t.Fatalf("target = %q, want %q", target, agentActionTestAgentID)
	}
}

func TestExecuteAgentActionTimerCard_RawActorIDTarget(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	// No agents registered: a raw ActorID target bypasses name resolution.
	wsRef := &agentActionWorkspaceRef{}
	ctx := agentActionCtx(wsRef)

	card := schedulerCard("scheduler:actn",
		fmt.Sprintf("  schedule_type: agent_action\n  agent_action: pause\n  target_agent: %s", agentActionTestAgentID), "Body.")

	if err := a.executeAgentActionTimerCard(ctx, card); err != nil {
		t.Fatalf("executeAgentActionTimerCard: %v", err)
	}

	pauseCalls, _, target, _ := wsRef.calls()
	if pauseCalls != 1 {
		t.Fatalf("pause calls = %d, want 1", pauseCalls)
	}
	if target != agentActionTestAgentID {
		t.Fatalf("target = %q, want %q", target, agentActionTestAgentID)
	}
}

func TestExecuteAgentActionTimerCard_InvalidActionRejected(t *testing.T) {
	a := newTestActor(t.TempDir())
	card := schedulerCard("scheduler:actn",
		"  schedule_type: agent_action\n  agent_action: restart\n  target_agent: agent:coder", "Body.")

	err := a.executeAgentActionTimerCard(agentActionCtx(&agentActionWorkspaceRef{}), card)
	if err == nil || !strings.Contains(err.Error(), "must be pause or resume") {
		t.Fatalf("error = %v, want containing 'must be pause or resume'", err)
	}
}

func TestExecuteAgentActionTimerCard_MissingTargetRejected(t *testing.T) {
	a := newTestActor(t.TempDir())
	card := schedulerCard("scheduler:actn",
		"  schedule_type: agent_action\n  agent_action: pause", "Body.")

	err := a.executeAgentActionTimerCard(agentActionCtx(&agentActionWorkspaceRef{}), card)
	if err == nil || !strings.Contains(err.Error(), "target_agent is required") {
		t.Fatalf("error = %v, want containing 'target_agent is required'", err)
	}
}

func TestExecuteAgentActionTimerCard_UnknownTargetRejected(t *testing.T) {
	a := newTestActor(t.TempDir())
	// Workspace knows no agents → agent:<name> resolution fails.
	wsRef := &agentActionWorkspaceRef{}
	card := schedulerCard("scheduler:actn",
		"  schedule_type: agent_action\n  agent_action: pause\n  target_agent: agent:ghost", "Body.")

	err := a.executeAgentActionTimerCard(agentActionCtx(wsRef), card)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v, want containing 'not found'", err)
	}
}

func TestExecuteAgentActionTimerCard_NoWorkspaceService(t *testing.T) {
	a := newTestActor(t.TempDir())
	card := schedulerCard("scheduler:actn",
		fmt.Sprintf("  schedule_type: agent_action\n  agent_action: pause\n  target_agent: %s", agentActionTestAgentID), "Body.")

	// Raw ActorID resolves without workspace; failure comes at service lookup.
	err := a.executeAgentActionTimerCard(agentActionCtx(nil), card)
	if err == nil || !strings.Contains(err.Error(), "workspace service unavailable") {
		t.Fatalf("error = %v, want containing 'workspace service unavailable'", err)
	}
}

func TestSchedulerValidator_AgentAction(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		wantErr []string // substrings that must appear among reported errors
	}{
		{
			name: "valid pause card",
			data: "  schedule_type: agent_action\n  agent_action: pause\n  target_agent: agent:coder",
		},
		{
			name: "valid resume card with raw actor id",
			data: fmt.Sprintf("  schedule_type: agent_action\n  agent_action: resume\n  target_agent: %s", agentActionTestAgentID),
		},
		{
			name:    "missing action rejected",
			data:    "  schedule_type: agent_action\n  target_agent: agent:coder",
			wantErr: []string{"agent_action"},
		},
		{
			name:    "invalid action rejected",
			data:    "  schedule_type: agent_action\n  agent_action: kill\n  target_agent: agent:coder",
			wantErr: []string{"must be pause or resume"},
		},
		{
			name:    "missing target rejected",
			data:    "  schedule_type: agent_action\n  agent_action: pause",
			wantErr: []string{"target_agent is required"},
		},
		{
			name:    "executor conflict rejected",
			data:    "  schedule_type: agent_action\n  agent_action: pause\n  target_agent: agent:coder\n  executor: agent:other",
			wantErr: []string{"executor is not used by agent_action cards"},
		},
		{
			name:    "executor not required for agent_action",
			data:    "  schedule_type: agent_action\n  agent_action: pause\n  target_agent: agent:coder",
			wantErr: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := schedulerCard("scheduler:vld", tc.data, "Body.")
			errs := schedulerValidator{}.Validate(card)
			if len(tc.wantErr) == 0 {
				if len(errs) != 0 {
					t.Fatalf("unexpected validation errors: %+v", errs)
				}
				return
			}
			for _, want := range tc.wantErr {
				found := false
				for _, e := range errs {
					if strings.Contains(e.Message, want) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("errors %+v missing message containing %q", errs, want)
				}
			}
		})
	}
}