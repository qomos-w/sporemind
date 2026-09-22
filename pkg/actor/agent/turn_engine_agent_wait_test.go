package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ── forkInputIsAsync ──

func TestForkInputIsAsync(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"async true", `{"Description":"d","Prompt":"p","Async":true}`, true},
		{"async false", `{"Description":"d","Prompt":"p","Async":false}`, false},
		{"omitted", `{"Description":"d","Prompt":"p"}`, false},
		{"invalid json", `{not json`, false},
		{"empty", ``, false},
	}
	for _, tc := range cases {
		if got := forkInputIsAsync(tc.input); got != tc.want {
			t.Errorf("%s: forkInputIsAsync = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// ── hasSyncPendingChildren ──

func TestHasSyncPendingChildren(t *testing.T) {
	e := &turnEngine{}
	if e.hasSyncPendingChildren() {
		t.Error("empty engine must report no sync children")
	}
	e.pendingChildren = []pendingChild{{ToolUseID: "a", Async: true}}
	if e.hasSyncPendingChildren() {
		t.Error("async-only pending must not count as sync")
	}
	e.pendingChildren = append(e.pendingChildren, pendingChild{ToolUseID: "b"})
	if !e.hasSyncPendingChildren() {
		t.Error("one sync child should count")
	}
}

// ── trackForkChildren: async vs sync routing ──

func TestTrackForkChildren_AsyncRoutedToAsyncPending(t *testing.T) {
	e := &turnEngine{
		logger:            newNopActorLogger(),
		suppressEventEmit: true,
		stepByID:          map[string]domain.TurnAction{},
	}
	batch := toolExecutionBatch{calls: []pendingToolCall{
		{ID: "tool-async", LLMName: "fork_explore", CallableID: "workspace.agent_spawn_by_type", ForkAsync: true},
		{ID: "tool-sync", LLMName: "fork_explore", CallableID: "workspace.agent_spawn_by_type"},
	}}
	results := []toolExecutionResult{
		{call: batch.calls[0], out: `{"ChildActorID":"agent-async"}`},
		{call: batch.calls[1], out: `{"ChildAgentID":"agent-sync"}`},
	}
	e.batchSteps = []domain.TurnAction{
		{ID: "step-async", ToolUseID: "tool-async"},
		{ID: "step-sync", ToolUseID: "tool-sync"},
	}

	e.trackForkChildren(nil, batch, results)

	if len(e.pendingChildren) != 1 || e.pendingChildren[0].ToolUseID != "tool-sync" {
		t.Fatalf("pendingChildren = %+v, want only tool-sync", e.pendingChildren)
	}
	if len(e.asyncPending) != 1 || e.asyncPending[0].ToolUseID != "tool-async" || e.asyncPending[0].AgentID != "agent-async" {
		t.Fatalf("asyncPending = %+v, want tool-async/agent-async", e.asyncPending)
	}
	if _, ok := e.pendingChildSteps.Load("step-sync"); !ok {
		t.Error("sync fork step should be registered in pendingChildSteps")
	}
	if _, ok := e.pendingChildSteps.Load("step-async"); ok {
		t.Error("async fork step must NOT be registered in pendingChildSteps (step already closed)")
	}
	if !strings.Contains(results[0].out, "agent-async") || !strings.Contains(results[0].out, "AsyncSpawned") {
		t.Errorf("async result out should be the spawn ack naming the child, got %q", results[0].out)
	}
	if strings.Contains(results[1].out, "AsyncSpawned") {
		t.Errorf("sync result out must stay the raw workspace response, got %q", results[1].out)
	}
}

// ── handleChildResult: async outcome recording ──

func TestHandleChildResult_AsyncRecordsOutcome(t *testing.T) {
	e := &turnEngine{
		logger:            newNopActorLogger(),
		suppressEventEmit: true,
		stepByID:          map[string]domain.TurnAction{},
		turnID:            "turn-1",
	}
	e.asyncPending = []pendingChild{{ToolUseID: "tool-async", StepID: "step-async", AgentID: "agent-x"}}
	e.history = []domain.ChatMessage{
		{Role: "tool", Content: []domain.ContentBlock{{Type: domain.ContentBlockToolResult, ToolUseID: "tool-async", Text: "spawn ack"}}},
	}

	e.handleChildResult(nil, "turn-1", childResult{
		ToolUseID: "tool-async",
		Result:    domain.ForkResult{Summary: "found 3 items", InputTokens: 100, OutputTokens: 20},
	})

	if len(e.asyncPending) != 0 {
		t.Fatalf("asyncPending = %+v, want empty after completion", e.asyncPending)
	}
	outcome, ok := e.asyncChildResults["tool-async"]
	if !ok || outcome.Status != "completed" || outcome.AgentID != "agent-x" {
		t.Fatalf("asyncChildResults[tool-async] = %+v, want completed/agent-x", outcome)
	}
	if outcome.Result.Summary != "found 3 items" {
		t.Errorf("outcome summary = %q", outcome.Result.Summary)
	}
	// The spawn ack history block must NOT be rewritten (unlike sync forks).
	if e.history[0].Content[0].Text != "spawn ack" {
		t.Errorf("async completion must not rewrite the spawn ack, got %q", e.history[0].Content[0].Text)
	}
	if e.totalUsage.InputTokens != 100 {
		t.Errorf("totalUsage.InputTokens = %d, want 100", e.totalUsage.InputTokens)
	}
}

// ── executeWaitAgents: harvest, timeout, clamp ──

func newAgentWaitTestEngine(t *testing.T) (*turnEngine, actor.Context) {
	t.Helper()
	ctx := testutil.AnonCtx(testutil.GenActorID())
	e := &turnEngine{
		logger:            newNopActorLogger(),
		suppressEventEmit: true,
		stepByID:          map[string]domain.TurnAction{},
		turnID:            "turn-1",
		childDoneCh:       make(chan childResult, 16),
		done:              make(chan struct{}),
	}
	return e, ctx
}

func TestExecuteWaitAgents_HarvestsDeliveredResults(t *testing.T) {
	e, actx := newAgentWaitTestEngine(t)
	e.asyncPending = []pendingChild{
		{ToolUseID: "tool-a", StepID: "step-a", AgentID: "agent-a"},
		{ToolUseID: "tool-b", StepID: "step-b", AgentID: "agent-b"},
	}
	e.batchSteps = []domain.TurnAction{{ID: "step-wait", ToolUseID: "tool-wait"}}
	waitCall := &pendingToolCall{ID: "tool-wait", LLMName: "agent_wait", CallableID: "agent_wait", Input: `{"AgentIds":["agent-a","agent-b"]}`}
	// Prime one child result; agent-b stays running until timeout — but with a
	// 0 timeout the loop never waits for stragglers... it must: agent-b is a
	// running target. Deliver it too so the wait completes via childDoneCh.
	e.childDoneCh <- childResult{ToolUseID: "tool-a", Result: domain.ForkResult{Summary: "a done"}}
	e.childDoneCh <- childResult{ToolUseID: "tool-b", Result: domain.ForkResult{Summary: "b done"}}

	if err := e.executeWaitAgents(actx, "turn-1", waitCall); err != nil {
		t.Fatalf("executeWaitAgents: %v", err)
	}

	var out agentWaitOutput
	last := e.history[len(e.history)-1]
	if last.Role != "tool" {
		t.Fatalf("last history message role = %q, want tool", last.Role)
	}
	if err := json.Unmarshal([]byte(last.Content[0].Text), &out); err != nil {
		t.Fatalf("tool result not valid JSON: %v (%q)", err, last.Content[0].Text)
	}
	if out.TimedOut {
		t.Error("TimedOut should be false when all targets complete")
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %+v, want 2 rows", out.Results)
	}
	byAgent := map[string]agentWaitOutcomeRow{}
	for _, r := range out.Results {
		byAgent[r.AgentID] = r
	}
	if byAgent["agent-a"].Status != "completed" || byAgent["agent-a"].Summary != "a done" {
		t.Errorf("agent-a row = %+v", byAgent["agent-a"])
	}
	if byAgent["agent-b"].Status != "completed" || byAgent["agent-b"].Summary != "b done" {
		t.Errorf("agent-b row = %+v", byAgent["agent-b"])
	}
	// The wait step must be closed.
	if step := e.stepByID["step-wait"]; step.State != "completed" {
		t.Errorf("wait step state = %q, want completed", step.State)
	}
}

func TestExecuteWaitAgents_TimeoutReturnsPartial(t *testing.T) {
	e, actx := newAgentWaitTestEngine(t)
	// Compress the floor so the timeout fires fast in tests.
	prevMin := agentWaitMinTimeout
	agentWaitMinTimeout = 50 * time.Millisecond
	defer func() { agentWaitMinTimeout = prevMin }()

	e.asyncPending = []pendingChild{
		{ToolUseID: "tool-a", StepID: "step-a", AgentID: "agent-a"},
		{ToolUseID: "tool-slow", StepID: "step-slow", AgentID: "agent-slow"},
	}
	e.batchSteps = []domain.TurnAction{{ID: "step-wait", ToolUseID: "tool-wait"}}
	waitCall := &pendingToolCall{ID: "tool-wait", LLMName: "agent_wait", CallableID: "agent_wait", Input: `{"TimeoutMs":10}`}
	e.childDoneCh <- childResult{ToolUseID: "tool-a", Result: domain.ForkResult{Summary: "a done"}}

	if err := e.executeWaitAgents(actx, "turn-1", waitCall); err != nil {
		t.Fatalf("executeWaitAgents: %v", err)
	}

	var out agentWaitOutput
	last := e.history[len(e.history)-1]
	if err := json.Unmarshal([]byte(last.Content[0].Text), &out); err != nil {
		t.Fatalf("tool result not valid JSON: %v (%q)", err, last.Content[0].Text)
	}
	if !out.TimedOut {
		t.Error("TimedOut should be true")
	}
	byAgent := map[string]agentWaitOutcomeRow{}
	for _, r := range out.Results {
		byAgent[r.AgentID] = r
	}
	if byAgent["agent-a"].Status != "completed" {
		t.Errorf("agent-a row = %+v, want completed", byAgent["agent-a"])
	}
	if byAgent["agent-slow"].Status != "running" {
		t.Errorf("agent-slow row = %+v, want running", byAgent["agent-slow"])
	}
	// The timed-out child stays in asyncPending for a later re-wait.
	if len(e.asyncPending) != 1 || e.asyncPending[0].ToolUseID != "tool-slow" {
		t.Fatalf("asyncPending = %+v, want only tool-slow", e.asyncPending)
	}
	if step := e.stepByID["step-wait"]; step.State != "completed" {
		t.Errorf("wait step state = %q — timeout must still close the step (not an error)", step.State)
	}
	if last.Content[0].IsError {
		t.Error("timeout must not be an error result")
	}
}

func TestExecuteWaitAgents_HardCapRejected(t *testing.T) {
	e, actx := newAgentWaitTestEngine(t)
	e.batchSteps = []domain.TurnAction{{ID: "step-wait", ToolUseID: "tool-wait"}}
	waitCall := &pendingToolCall{ID: "tool-wait", LLMName: "agent_wait", CallableID: "agent_wait", Input: `{"TimeoutMs":7200000}`}

	if err := e.executeWaitAgents(actx, "turn-1", waitCall); err != nil {
		t.Fatalf("executeWaitAgents: %v", err)
	}
	last := e.history[len(e.history)-1]
	if !last.Content[0].IsError || !strings.Contains(last.Content[0].Text, "hard cap") {
		t.Errorf("over-cap timeout should produce an error result, got %q", last.Content[0].Text)
	}
}

func TestExecuteWaitAgents_AlreadyFinishedAndCrossTurnLookup(t *testing.T) {
	e, actx := newAgentWaitTestEngine(t)
	e.asyncChildResults = map[string]asyncChildOutcome{
		"tool-done": {AgentID: "agent-done", Status: "completed", Result: domain.ForkResult{Summary: "same turn"}},
	}
	e.onExploreResultLookup = func(agentID string) (domain.ExploreResult, bool) {
		if agentID == "agent-old" {
			return domain.ExploreResult{Summary: "previous turn", AgentID: agentID}, true
		}
		return domain.ExploreResult{}, false
	}
	waitCall := &pendingToolCall{ID: "tool-wait", LLMName: "agent_wait", CallableID: "agent_wait", Input: `{"AgentIds":["agent-done","agent-old","agent-ghost"]}`}
	e.batchSteps = []domain.TurnAction{{ID: "step-wait", ToolUseID: "tool-wait"}}

	if err := e.executeWaitAgents(actx, "turn-1", waitCall); err != nil {
		t.Fatalf("executeWaitAgents: %v", err)
	}
	var out agentWaitOutput
	last := e.history[len(e.history)-1]
	if err := json.Unmarshal([]byte(last.Content[0].Text), &out); err != nil {
		t.Fatalf("tool result not valid JSON: %v", err)
	}
	if len(out.Results) != 3 {
		t.Fatalf("results = %+v, want 3 rows", out.Results)
	}
	byAgent := map[string]agentWaitOutcomeRow{}
	for _, r := range out.Results {
		byAgent[r.AgentID] = r
	}
	if byAgent["agent-done"].Summary != "same turn" {
		t.Errorf("agent-done row = %+v", byAgent["agent-done"])
	}
	if byAgent["agent-old"].Summary != "previous turn" {
		t.Errorf("agent-old row = %+v", byAgent["agent-old"])
	}
	if byAgent["agent-ghost"].Status != "unknown" {
		t.Errorf("agent-ghost row = %+v, want unknown", byAgent["agent-ghost"])
	}
}

func TestExecuteWaitAgents_CancelInterrupts(t *testing.T) {
	e, actx := newAgentWaitTestEngine(t)
	e.asyncPending = []pendingChild{{ToolUseID: "tool-slow", StepID: "step-slow", AgentID: "agent-slow"}}
	waitCall := &pendingToolCall{ID: "tool-wait", LLMName: "agent_wait", CallableID: "agent_wait", Input: `{"TimeoutMs":10000}`}
	close(e.done)
	err := e.executeWaitAgents(actx, "turn-1", waitCall)
	if err != context.Canceled {
		t.Fatalf("executeWaitAgents err = %v, want context.Canceled", err)
	}
	if len(e.asyncPending) != 1 {
		t.Errorf("cancel must not affect async children, asyncPending = %+v", e.asyncPending)
	}
}
