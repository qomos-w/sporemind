package agent

import (
	"context"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/actor/agent/memory"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"sync"
	"testing"
	"time"
)

// captureRef records the last Invoke call it receives.
type captureRef struct {
	mu          sync.Mutex
	actorID     id.ActorID
	lastCallID  string
	lastPayload any
}

func (r *captureRef) ID() id.ActorID          { return r.actorID }
func (r *captureRef) Service() (string, bool) { return "workspace", true }
func (r *captureRef) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	r.mu.Lock()
	r.lastCallID = callID
	r.lastPayload = payload
	r.mu.Unlock()
	return invoke.NewCall(invoke.CallModeTell, nil)
}

func TestNotifyWorkspaceStatus_PushesStatusUpdate(t *testing.T) {
	ws := &captureRef{actorID: testutil.GenActorID()}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return ws, true
		}
		return nil, false
	}

	a := &Actor{
		actorID:         "agent-actor-1",
		DisplayName:     "Test Agent",
		agentKind:       "coder",
		pendingApproval: true,
		pendingAskUser:  false,
		ActiveTurnRef:   "turn-42",
		status: turnStatus{
			State: "running",
			Unit:  domain.ModelUnit{Model: "claude-sonnet"},
		},
	}
	a.Session.Turns = []domain.Turn{
		{ID: "turn-1", StartedAt: "2026-06-15T10:00:00Z", CompletedAt: "2026-06-15T10:05:00Z"},
		{ID: "turn-42", StartedAt: "2026-06-15T11:00:00Z"},
	}
	a.RawSession.Tasks = []gen.TurnTask{{
		ID:      "task-1",
		Subject: "refactor login",
		Status:  "in_progress",
	}}

	// Status notifications arm a single-flight sender lazily on first notify.
	defer a.stopStatusNotifications()

	a.notifyWorkspaceStatus(ctx)

	// Wait for the drain goroutine to process the update.
	time.Sleep(100 * time.Millisecond)

	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.lastCallID != "workspace.agent_status_update" {
		t.Fatalf("callID = %q, want workspace.agent_status_update", ws.lastCallID)
	}
	req, ok := ws.lastPayload.(gen.WorkspaceAgentStatusUpdateReq)
	if !ok {
		t.Fatalf("payload type = %T, want WorkspaceAgentStatusUpdateReq", ws.lastPayload)
	}
	if req.AgentActorID != "agent-actor-1" {
		t.Errorf("agentActorID = %q, want agent-actor-1", req.AgentActorID)
	}
	if req.State != "running" {
		t.Errorf("state = %q, want running", req.State)
	}
	if req.ActiveTurnRef != "turn-42" {
		t.Errorf("activeTurnRef = %q, want turn-42", req.ActiveTurnRef)
	}
	if !req.ApprovalPending {
		t.Error("expected ApprovalPending true")
	}
	if req.CurrentTaskSummary != "refactor login" {
		t.Errorf("summary = %q, want 'refactor login'", req.CurrentTaskSummary)
	}
	if req.LastTurnCompletedAt != "2026-06-15T10:05:00Z" {
		t.Errorf("LastTurnCompletedAt = %q, want 2026-06-15T10:05:00Z", req.LastTurnCompletedAt)
	}
	if req.MemoryMounted {
		t.Error("expected MemoryMounted false when memory graph is nil")
	}
}

// TestNotifyWorkspaceStatus_PushesMemoryMounted verifies that the memory mount
// state is reflected in the status update so the workspace/sidebar can show a
// memory indicator without per-agent polling.
func TestNotifyWorkspaceStatus_PushesMemoryMounted(t *testing.T) {
	ws := &captureRef{actorID: testutil.GenActorID()}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return ws, true
		}
		return nil, false
	}

	a := &Actor{
		actorID:     "agent-actor-mem",
		DisplayName: "Memory Agent",
		agentKind:   "coder",
		Graph:       memory.New(memory.Config{}),
	}
	defer a.stopStatusNotifications()

	a.notifyWorkspaceStatus(ctx)

	time.Sleep(100 * time.Millisecond)

	ws.mu.Lock()
	defer ws.mu.Unlock()
	req, ok := ws.lastPayload.(gen.WorkspaceAgentStatusUpdateReq)
	if !ok {
		t.Fatalf("payload type = %T, want WorkspaceAgentStatusUpdateReq", ws.lastPayload)
	}
	if !req.MemoryMounted {
		t.Error("expected MemoryMounted true when memory graph is active")
	}
}

func TestTakeSnapshot_PublishesTurnProjectionFields(t *testing.T) {
	a := &Actor{
		status: turnStatus{State: "running"},
		RawSession: domain.RawSession{
			Tasks: []gen.TurnTask{{ID: "task-1", Subject: "refactor login", Status: "in_progress"}},
		},
	}

	a.takeSnapshot()
	if a.TurnState != "running" {
		t.Errorf("TurnState = %q, want running", a.TurnState)
	}
	if a.TurnPauseKind != "" {
		t.Errorf("TurnPauseKind = %q, want empty", a.TurnPauseKind)
	}

	a.status.State = "paused"
	a.takeSnapshot()
	if a.TurnState != "paused" {
		t.Errorf("TurnState = %q, want paused", a.TurnState)
	}
	if a.TurnPauseKind != "task" {
		t.Errorf("TurnPauseKind = %q, want task", a.TurnPauseKind)
	}
}

func TestTakeSnapshotDebounced_RateLimitsWithinInterval(t *testing.T) {
	a := &Actor{
		status: turnStatus{State: "running"},
	}

	// First call on a fresh actor always rebuilds (zero timestamp).
	a.takeSnapshotDebounced()
	v1 := a.snapshot.Load()
	if v1 == nil {
		t.Fatal("first takeSnapshotDebounced did not build a snapshot")
	}

	// Within the interval the rebuild is skipped — the stored snapshot pointer
	// must not change even though state moved on.
	a.status.State = "paused"
	a.takeSnapshotDebounced()
	if got := a.snapshot.Load(); got != v1 {
		t.Fatal("takeSnapshotDebounced rebuilt within snapshotStreamMinInterval")
	}

	// Boundary callers bypass the debounce via takeSnapshot directly.
	a.takeSnapshot()
	v2 := a.snapshot.Load()
	if v2 == nil || v2 == v1 {
		t.Fatal("direct takeSnapshot did not rebuild")
	}
	if v2.statusState != "paused" {
		t.Errorf("rebuilt snapshot statusState = %q, want paused", v2.statusState)
	}
}

func TestNotifyWorkspaceStatus_NoWorkspaceService(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(string) (ref.Ref, bool) { return nil, false }

	a := &Actor{actorID: "agent-actor-1"}
	// Should not panic and should not send anything.
	a.notifyWorkspaceStatus(ctx)
}

// TestMarkStarted_ClosesOnStartDone locks down the cold-start signal: markStarted
// must close onStartDone exactly once and remain safe on repeated calls (the
// OnStart defer + possible restart recreate paths). The pure session.summary
// handler waits on this channel during cold start.
func TestMarkStarted_ClosesOnStartDone(t *testing.T) {
	a := &Actor{onStartDone: make(chan struct{})}
	select {
	case <-a.onStartDone:
		t.Fatal("onStartDone closed before markStarted")
	default:
	}
	a.markStarted()
	select {
	case <-a.onStartDone:
	default:
		t.Fatal("onStartDone not closed after markStarted")
	}
	// Idempotent: a second call must not panic on close-of-closed-channel.
	a.markStarted()
}

func TestPureHandlers_ReturnEmptyWhenNoCache(t *testing.T) {
	a := &Actor{}

	// prompt_artifact returns empty when cache is nil.
	artifact, err := a.handlePromptArtifact(nil)
	if err != nil {
		t.Fatalf("handlePromptArtifact (pure) failed: %v", err)
	}
	if len(artifact.Fragments) != 0 {
		t.Fatalf("expected empty artifact, got %d fragments", len(artifact.Fragments))
	}

	// compiled_prompt returns empty when cache is nil.
	compiled, err := a.handleCompiledPrompt(nil)
	if err != nil {
		t.Fatalf("handleCompiledPrompt (pure) failed: %v", err)
	}
	if len(compiled.Fragments) != 0 {
		t.Fatalf("expected empty compiled prompt, got %d fragments", len(compiled.Fragments))
	}

	// message.list returns empty when snapshot is nil.
	msgResp, err := a.handleMessageList(nil)
	if err != nil {
		t.Fatalf("handleMessageList (pure) failed: %v", err)
	}
	if len(msgResp.Items) != 0 {
		t.Fatalf("expected empty message list, got %d items", len(msgResp.Items))
	}

	// messages.list returns empty when snapshot is nil.
	msgsResp, err := a.handleMessagesList(nil, domain.AgentMessagesListReq{Limit: 10})
	if err != nil {
		t.Fatalf("handleMessagesList (pure) failed: %v", err)
	}
	if len(msgsResp.Messages) != 0 {
		t.Fatalf("expected empty messages list, got %d messages", len(msgsResp.Messages))
	}
}

func TestPureHandlers_ReturnCachedDataAfterStatefulBuild(t *testing.T) {
	a := &Actor{}
	a.snapshotReady = true

	// Seed mailbox snapshot and verify message.list reads it.
	a.mailbox = []domain.AgentMessage{
		{ID: "msg-1", Text: "hello"},
	}
	a.takeSnapshot()

	msgResp, err := a.handleMessageList(nil)
	if err != nil {
		t.Fatalf("handleMessageList failed: %v", err)
	}
	if len(msgResp.Items) != 1 || msgResp.Items[0].Text != "hello" {
		t.Fatalf("expected 1 message with subject 'hello', got %+v", msgResp.Items)
	}

	// Seed steps snapshot and verify messages.list reads from snapshot.
	a.steps = []domain.Step{
		{ID: "s1", Role: "user", Type: "text", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
	}
	a.takeSnapshot()

	msgsResp, err := a.handleMessagesList(nil, domain.AgentMessagesListReq{Limit: 10})
	if err != nil {
		t.Fatalf("handleMessagesList failed: %v", err)
	}
	if len(msgsResp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgsResp.Messages))
	}

	// Seed prompt artifact cache and verify pure handler returns it.
	promptArtifact := domain.PromptArtifact{
		Fragments: []domain.PromptFragment{
			{ID: "frag-1", Name: "Config", Kind: "config", Content: "Model: test"},
		},
	}
	a.cachedPromptArtifact.Store(&promptArtifact)

	artifact, err := a.handlePromptArtifact(nil)
	if err != nil {
		t.Fatalf("handlePromptArtifact failed: %v", err)
	}
	if len(artifact.Fragments) != 1 || artifact.Fragments[0].Name != "Config" {
		t.Fatalf("expected 1 config fragment, got %+v", artifact.Fragments)
	}

	// Seed compiled prompt cache and verify pure handler returns it.
	compiledArtifact := domain.PromptArtifact{
		Fragments: []domain.PromptFragment{
			{ID: "frag-2", Name: "user #1", Kind: "message", Content: "hello"},
		},
	}
	a.cachedCompiledPrompt.Store(&compiledArtifact)

	compiled, err := a.handleCompiledPrompt(nil)
	if err != nil {
		t.Fatalf("handleCompiledPrompt failed: %v", err)
	}
	if len(compiled.Fragments) != 1 || compiled.Fragments[0].Name != "user #1" {
		t.Fatalf("expected 1 message fragment, got %+v", compiled.Fragments)
	}
}

// TestNewActor_PermissionMode verifies that the spawn-time permission mode is
// stored on the actor and normalized: valid modes are kept, empty/unknown
// values fall back to "permission" (confirm per call).
func TestNewActor_PermissionMode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"yolo", "yolo"},
		{"allow-all", "allow-all"},
		{"auto", "auto"},
		{"autopilot", "autopilot"},
		{"", "permission"},
		{"garbage", "permission"},
	}
	for _, tc := range cases {
		a := NewActor("", "worker", "",
			domain.ModelSlot{}, domain.ModelSlot{}, domain.ModelSlot{},
			domain.ModelSlot{}, domain.ModelSlot{}, nil, tc.in, "", nil)().(*Actor)
		if a.permissionMode != tc.want {
			t.Errorf("NewActor(permissionMode=%q): permissionMode = %q, want %q", tc.in, a.permissionMode, tc.want)
		}
	}
}
