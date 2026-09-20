package im

// Reply-pipeline tests. The state machine test drives the full
// submit→poll→reply flow against fakes: a fake agent ref answering chat_submit
// and session_summary, a fake planner answering workspace.agent_list_state,
// and a fake provider recording Send calls. Truncation, markdown
// simplification and step extraction are covered by table tests.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeAgentBackend answers the two agent callables the pipeline uses.
type fakeAgentBackend struct {
	mu        sync.Mutex
	submits   []gen.AgentChatSubmitReq
	summaries int
	steps     []gen.Step
	nextTurn  string
}

func (f *fakeAgentBackend) invoke(callID string, payload any) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch callID {
	case "chat_submit":
		req, _ := payload.(gen.AgentChatSubmitReq)
		f.submits = append(f.submits, req)
		turn := f.nextTurn
		if turn == "" {
			turn = "turn-1"
		}
		return gen.AgentChatSubmitResp{TurnActorID: turn, MessageID: "m1"}
	case "session_summary":
		f.summaries++
		return gen.AgentSessionSummaryResp{Steps: f.steps}
	}
	return fmt.Errorf("fake agent: unexpected call %s", callID)
}

func (f *fakeAgentBackend) snapshotSubmits() []gen.AgentChatSubmitReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]gen.AgentChatSubmitReq, len(f.submits))
	copy(out, f.submits)
	return out
}

func (f *fakeAgentBackend) setSteps(steps []gen.Step) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = steps
}

func (f *fakeAgentBackend) summaryCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.summaries
}

// fakeSendProvider records outbound Send calls.
type fakeSendProvider struct {
	mu    sync.Mutex
	sends []fakeSend
}

type fakeSend struct {
	ChatID string
	Text   string
}

func (p *fakeSendProvider) ID() string                                 { return "faketest" }
func (p *fakeSendProvider) Name() string                               { return "FakeTest" }
func (p *fakeSendProvider) Probe(context.Context, gen.ImAccount) error { return nil }
func (p *fakeSendProvider) Run(context.Context, gen.ImAccount, chan<- InboundMessage) error {
	return nil
}
func (p *fakeSendProvider) Send(_ context.Context, _ gen.ImAccount, chatID, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sends = append(p.sends, fakeSend{ChatID: chatID, Text: text})
	return nil
}

func (p *fakeSendProvider) sent() []fakeSend {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]fakeSend, len(p.sends))
	copy(out, p.sends)
	return out
}

// fakeWsState controls what workspace.agent_list_state reports: the agent
// items plus a visibility flag (invisible → empty list).
type fakeWsState struct {
	mu      sync.Mutex
	items   []gen.AgentListItem
	visible bool
}

func (w *fakeWsState) set(state, errMsg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.items) > 0 {
		if w.items[0].Runtime == nil {
			w.items[0].Runtime = &gen.AgentRuntimeState{}
		}
		w.items[0].Runtime.State, w.items[0].Runtime.Error = state, errMsg
	}
}

func (w *fakeWsState) setVisible(v bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.visible = v
}

func (w *fakeWsState) setItems(items []gen.AgentListItem) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.items = append([]gen.AgentListItem(nil), items...)
}

// snapshot returns the visible items (nil when invisible).
func (w *fakeWsState) snapshot() []gen.AgentListItem {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.visible {
		return nil
	}
	return append([]gen.AgentListItem(nil), w.items...)
}

// replyTestEnv wires a FakeCtx to the fakes and returns the pieces the test
// drives.
type replyTestEnv struct {
	ctx      *testutil.FakeCtx
	agent    *fakeAgentBackend
	ws       *fakeWsState
	send     *fakeSendProvider
	agentHex string
	afterMu  sync.Mutex
	afters   []string
}

func newReplyTestEnv(t *testing.T) *replyTestEnv {
	t.Helper()
	agentID := testutil.GenActorID()
	env := &replyTestEnv{
		agent: &fakeAgentBackend{},
		ws: &fakeWsState{
			visible: true,
			items: []gen.AgentListItem{{
				ActorID:     agentID.String(),
				DisplayName: "工作台",
				Runtime:     &gen.AgentRuntimeState{State: "completed"},
			}},
		},
		send: &fakeSendProvider{},
	}
	env.agentHex = agentID.String()
	RegisterProvider("faketest", func() Provider { return env.send })

	agentRef := testutil.NewFakeRef(agentID, env.agent.invoke)

	planner := &fakePlanner{fn: func(callID string, _ any) (any, error) {
		if callID != "workspace.agent_list_state" {
			return nil, fmt.Errorf("unexpected planner call %s", callID)
		}
		return gen.WorkspaceAgentListState{Items: env.ws.snapshot()}, nil
	}}

	env.ctx = routeCtx(planner)
	env.ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) { return agentRef, true }
	env.ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		env.afterMu.Lock()
		defer env.afterMu.Unlock()
		env.afters = append(env.afters, callID)
		return nil
	}
	return env
}

// tickCount returns how many poll ticks were armed.
func (e *replyTestEnv) tickCount() int {
	e.afterMu.Lock()
	defer e.afterMu.Unlock()
	n := 0
	for _, id := range e.afters {
		if id == callableReplyPollTick {
			n++
		}
	}
	return n
}

// setupBoundAccount creates an enabled faketest account and binds chat 42 to
// the fake agent.
func (e *replyTestEnv) setupBoundAccount(t *testing.T, a *Actor) gen.ImAccount {
	t.Helper()
	acc, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{
		Name: "mybot", Provider: "faketest", Token: "t", AllowUsers: []string{"10001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleRouteSet(nil, gen.ImRouteSetReq{
		AccountID: acc.Account.ID, AgentActorID: e.agentHex,
	}); err != nil {
		t.Fatal(err)
	}
	st, _ := a.loadStore()
	return st.Accounts[0]
}

// ---------------------------------------------------------------------------
// State machine: submit → running → completed → reply delivered
// ---------------------------------------------------------------------------

func TestReplyPipelineSubmitToDelivery(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	// Submit phase.
	if err := a.handleInbound(env.ctx, InboundMessage{
		AccountID: acc.ID, ChatID: "42", FromUser: "10001", Text: "你好，帮我看看构建",
	}); err != nil {
		t.Fatal(err)
	}

	submits := env.agent.snapshotSubmits()
	if len(submits) != 1 {
		t.Fatalf("expected 1 chat_submit, got %d", len(submits))
	}
	if submits[0].Text != "你好，帮我看看构建" {
		t.Fatalf("chat_submit text = %q", submits[0].Text)
	}
	if submits[0].Meta != "im:faketest" {
		t.Fatalf("chat_submit meta = %q, want im:faketest", submits[0].Meta)
	}
	if len(a.pendingReplies) != 1 || a.pendingReplies["turn-1"] == nil {
		t.Fatalf("pending reply not recorded: %+v", a.pendingReplies)
	}
	if got := env.tickCount(); got != 1 {
		t.Fatalf("poll tick armed %d times after submit, want 1", got)
	}
	if sends := env.send.sent(); len(sends) != 0 {
		t.Fatalf("no reply may leave before completion, got %+v", sends)
	}

	// Poll phase 1: agent still running → keep waiting, re-arm.
	env.ws.set("running", "")
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatal(err)
	}
	if len(a.pendingReplies) != 1 {
		t.Fatalf("pending reply dropped while running")
	}
	if got := env.tickCount(); got != 2 {
		t.Fatalf("poll tick re-armed %d times, want 2", got)
	}
	if env.agent.summaryCalls() != 0 {
		t.Fatalf("session_summary must not be called while running, got %d", env.agent.summaryCalls())
	}

	// Poll phase 2: completed with the turn's assistant step → deliver.
	env.ws.set("completed", "")
	env.agent.setSteps([]gen.Step{
		{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []gen.ContentBlock{{Type: "text", Text: "你好，帮我看看构建"}}},
		{ID: "t1", Role: "assistant", Type: "tool_call", TurnID: "turn-1", Content: []gen.ContentBlock{{Type: "tool_call", ToolName: "shell"}}},
		{ID: "a1", Role: "assistant", Type: "reasoning", TurnID: "turn-1", Content: []gen.ContentBlock{{Type: "reasoning", Text: "thinking..."}}},
		{ID: "a2", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []gen.ContentBlock{
			{Type: "text", Text: "构建失败的原因是 **main.go** 里的语法错误"},
			{Type: "text", Text: "详见 `make build` 输出"},
		}},
	})
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatal(err)
	}

	sends := env.send.sent()
	if len(sends) != 1 {
		t.Fatalf("expected 1 delivered reply, got %+v", sends)
	}
	if sends[0].ChatID != "42" {
		t.Fatalf("reply chat = %q, want 42", sends[0].ChatID)
	}
	want := "构建失败的原因是 main.go 里的语法错误\n\n详见 make build 输出"
	if sends[0].Text != want {
		t.Fatalf("reply text = %q, want %q", sends[0].Text, want)
	}
	if len(a.pendingReplies) != 0 {
		t.Fatalf("pending reply not cleared after delivery: %+v", a.pendingReplies)
	}
	if env.agent.summaryCalls() != 1 {
		t.Fatalf("session_summary calls = %d, want 1", env.agent.summaryCalls())
	}

	// No pending replies but a live mount keeps the reconcile tick armed.
	before := env.tickCount()
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatal(err)
	}
	if after := env.tickCount(); after != before+1 {
		t.Fatalf("tick must stay armed while mounts exist (%d → %d)", before, after)
	}
	// Unmount the account: with no pending replies and no mounts the tick
	// stops re-arming.
	st, _ := a.loadStore()
	if idx := findRoute(st, acc.ID); idx >= 0 {
		if _, err := a.handleRouteDelete(nil, gen.ImRouteDeleteReq{ID: st.Routes[idx].ID}); err != nil {
			t.Fatal(err)
		}
	}
	before = env.tickCount()
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatal(err)
	}
	if after := env.tickCount(); after != before {
		t.Fatalf("tick re-armed with no pending replies and no mounts (%d → %d)", before, after)
	}
}

func TestReplyPipelineKeepsWaitingWhenStepNotLanded(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	// Completed but the turn's assistant step has not landed yet.
	env.agent.setSteps([]gen.Step{
		{ID: "old", Role: "assistant", Type: "text", TurnID: "turn-0", Content: []gen.ContentBlock{{Type: "text", Text: "previous"}}},
	})
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatal(err)
	}
	if len(a.pendingReplies) != 1 {
		t.Fatalf("pending reply dropped before its step landed")
	}
	if sends := env.send.sent(); len(sends) != 0 {
		t.Fatalf("nothing may be sent before the step lands, got %+v", sends)
	}
}

func TestReplyPipelineAgentTurnFailed(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	env.ws.set("failed", "aggregator pool exhausted: all accounts disabled")
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatal(err)
	}
	sends := env.send.sent()
	if len(sends) != 1 {
		t.Fatalf("expected failure notice, got %+v", sends)
	}
	if !strings.HasPrefix(sends[0].Text, "⚠️ agent 执行失败") {
		t.Fatalf("failure notice = %q", sends[0].Text)
	}
	if !strings.Contains(sends[0].Text, "aggregator pool exhausted") {
		t.Fatalf("failure notice missing error detail: %q", sends[0].Text)
	}
	if len(a.pendingReplies) != 0 {
		t.Fatalf("pending reply not cleared after failure")
	}
}

func TestReplyPipelineWaitDeadlineTimeout(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	// Agent still running but the deadline has passed.
	env.ws.set("running", "")
	if p := a.pendingReplies["turn-1"]; p != nil {
		p.SubmittedAt = time.Now().Add(-replyWaitDeadline - time.Second)
	}
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatal(err)
	}
	sends := env.send.sent()
	if len(sends) != 1 || !strings.Contains(sends[0].Text, "超时") {
		t.Fatalf("expected timeout notice, got %+v", sends)
	}
	if len(a.pendingReplies) != 0 {
		t.Fatalf("pending reply not cleared after timeout")
	}
}

func TestReplyPipelineAgentInvisibleKeepsWaiting(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	env.ws.setVisible(false)
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatal(err)
	}
	if len(a.pendingReplies) != 1 {
		t.Fatalf("pending reply dropped while agent invisible")
	}
	if sends := env.send.sent(); len(sends) != 0 {
		t.Fatalf("nothing sent while agent invisible, got %+v", sends)
	}
}

func TestReplyPipelineDropsJunkInbound(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	for _, msg := range []InboundMessage{
		{AccountID: "", ChatID: "42", Text: "hi"},
		{AccountID: acc.ID, ChatID: "", Text: "hi"},
		{AccountID: acc.ID, ChatID: "42", Text: "   "},
		{AccountID: "ia_missing", ChatID: "42", Text: "hi"},
	} {
		if err := a.handleInbound(env.ctx, msg); err != nil {
			t.Fatalf("handleInbound(%+v) error: %v", msg, err)
		}
	}
	if len(env.agent.snapshotSubmits()) != 0 || len(env.send.sent()) != 0 {
		t.Fatal("junk inbound must be dropped silently")
	}

	// Disabled accounts are dropped too.
	st, _ := a.loadStore()
	st.Accounts[0].Enabled = false
	if err := a.saveStore(st); err != nil {
		t.Fatal(err)
	}
	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(env.agent.snapshotSubmits()) != 0 {
		t.Fatal("disabled account must not submit")
	}
}

func TestReplyPipelineUnboundChatFallsBackToCoordinator(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "b", Provider: "faketest", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}

	coordHex := testutil.GenActorID().String()
	planner := &fakePlanner{fn: func(callID string, _ any) (any, error) {
		switch callID {
		case "workspace.coordinator_lookup":
			return gen.WorkspaceCoordinatorLookupResp{Found: true, ActorID: coordHex}, nil
		case "workspace.agent_list_state":
			return gen.WorkspaceAgentListState{}, nil
		}
		return nil, fmt.Errorf("unexpected call %s", callID)
	}}
	env.ctx.PlannerFn = func() actor.Planner { return planner }

	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.Account.ID, ChatID: "7", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	// The unmounted-account notice goes out alongside the coordinator submit.
	if sends := env.send.sent(); len(sends) != 1 || !strings.Contains(sends[0].Text, "未挂载") || !strings.Contains(sends[0].Text, "/bind") {
		t.Fatalf("unmounted account must be nudged to /bind, got %+v", sends)
	}
	submits := env.agent.snapshotSubmits()
	if len(submits) != 1 {
		t.Fatalf("unbound chat must submit via coordinator default, got %d submits", len(submits))
	}
	if p := a.pendingReplies["turn-1"]; p == nil || p.AgentActorID != coordHex {
		t.Fatalf("pending reply agent = %+v, want coordinator %s", a.pendingReplies["turn-1"], coordHex)
	}
}

// ---------------------------------------------------------------------------
// Command interception
// ---------------------------------------------------------------------------

func TestReplyCommandStatusAndBind(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	// /status answers account + routing summary.
	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "/status"}); err != nil {
		t.Fatal(err)
	}
	sends := env.send.sent()
	if len(sends) != 1 {
		t.Fatalf("/status must answer once, got %+v", sends)
	}
	for _, want := range []string{"mybot", "faketest", env.agentHex} {
		if !strings.Contains(sends[0].Text, want) {
			t.Fatalf("/status reply %q missing %q", sends[0].Text, want)
		}
	}
	if len(env.agent.snapshotSubmits()) != 0 {
		t.Fatal("/status must not submit a turn")
	}

	// /bind to a different agent is rejected: exclusive mounts must unmount
	// first. (GenActorID is deterministic — mint a distinct canonical id.)
	newAgent := id.NewCanonical(1, 0, func() uint64 { return 2 }).Next().String()
	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "/bind " + newAgent}); err != nil {
		t.Fatal(err)
	}
	sends = env.send.sent()
	if len(sends) != 2 || !strings.Contains(sends[1].Text, "账户已挂载") {
		t.Fatalf("/bind retarget must be rejected with 账户已挂载, got %+v", sends)
	}
	st, _ := a.loadStore()
	if idx := findRoute(st, acc.ID); idx < 0 || st.Routes[idx].AgentActorID != env.agentHex {
		t.Fatalf("rejected /bind must not change the mount: %+v", st.Routes)
	}

	// /bind of the already-mounted agent is an idempotent remount.
	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "/bind " + env.agentHex}); err != nil {
		t.Fatal(err)
	}
	sends = env.send.sent()
	if len(sends) != 3 || !strings.Contains(sends[2].Text, env.agentHex) {
		t.Fatalf("idempotent /bind must confirm the binding, got %+v", sends)
	}

	// Unknown commands get a usage notice.
	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "/nope"}); err != nil {
		t.Fatal(err)
	}
	sends = env.send.sent()
	if len(sends) != 4 || !strings.Contains(sends[3].Text, "/bind") {
		t.Fatalf("unknown command must answer usage, got %+v", sends)
	}

	// /bind without a valid canonical id is rejected with a notice.
	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "/bind not-an-id"}); err != nil {
		t.Fatal(err)
	}
	sends = env.send.sent()
	if len(sends) != 5 || !strings.Contains(sends[4].Text, "无效") {
		t.Fatalf("invalid /bind arg must be rejected, got %+v", sends)
	}
	st, _ = a.loadStore()
	if idx := findRoute(st, acc.ID); idx < 0 || st.Routes[idx].AgentActorID != env.agentHex {
		t.Fatalf("invalid /bind must not change the route: %+v", st.Routes)
	}
}

// ---------------------------------------------------------------------------
// Mount menu, /unmount and cascade release
// ---------------------------------------------------------------------------

// TestBindMenuListsUnoccupiedAgents covers /bind without arguments: the menu
// numbers only workspace agents not occupied by any mount, "/bind <n>" mounts
// the numbered entry, and a successful mount removes the agent from the next
// menu.
func TestBindMenuListsUnoccupiedAgents(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)

	acc1, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "menu1", Provider: "faketest", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	acc2, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "menu2", Provider: "faketest", Token: "t2"})
	if err != nil {
		t.Fatal(err)
	}

	betaHex := id.NewCanonical(1, 0, func() uint64 { return 2 }).Next().String()
	gammaHex := id.NewCanonical(1, 0, func() uint64 { return 3 }).Next().String()
	env.ws.setItems([]gen.AgentListItem{
		{ActorID: env.agentHex, DisplayName: "工作台"},
		{ActorID: betaHex, DisplayName: "Beta"},
		{ActorID: gammaHex, DisplayName: "Gamma"},
	})
	// acc2 occupies beta, so the menu may only offer alpha and gamma.
	if _, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: acc2.Account.ID, AgentActorID: betaHex}); err != nil {
		t.Fatal(err)
	}

	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc1.Account.ID, ChatID: "42", Text: "/bind"}); err != nil {
		t.Fatal(err)
	}
	sends := env.send.sent()
	if len(sends) != 1 {
		t.Fatalf("/bind menu must answer once, got %+v", sends)
	}
	menu := sends[0].Text
	for _, want := range []string{"工作台", env.agentHex, gammaHex, "1.", "2.", "/bind <编号或 id>"} {
		if !strings.Contains(menu, want) {
			t.Fatalf("menu %q missing %q", menu, want)
		}
	}
	if strings.Contains(menu, betaHex) {
		t.Fatalf("menu must exclude the occupied agent %s: %q", betaHex, menu)
	}

	// /bind 2 mounts gamma (the second menu entry).
	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc1.Account.ID, ChatID: "42", Text: "/bind 2"}); err != nil {
		t.Fatal(err)
	}
	sends = env.send.sent()
	if len(sends) != 2 || !strings.Contains(sends[1].Text, gammaHex) {
		t.Fatalf("/bind 2 must mount the second menu entry, got %+v", sends)
	}
	st, _ := a.loadStore()
	if idx := findRoute(st, acc1.Account.ID); idx < 0 || st.Routes[idx].AgentActorID != gammaHex {
		t.Fatalf("/bind 2 route = %+v, want gamma %s", st.Routes, gammaHex)
	}

	// A fresh menu no longer lists the newly occupied agent.
	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc1.Account.ID, ChatID: "42", Text: "/bind"}); err != nil {
		t.Fatal(err)
	}
	sends = env.send.sent()
	if len(sends) != 3 || strings.Contains(sends[2].Text, gammaHex) {
		t.Fatalf("occupied agent must vanish from the next menu, got %+v", sends)
	}

	// Out-of-range numbers are rejected without touching the mount.
	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc1.Account.ID, ChatID: "42", Text: "/bind 9"}); err != nil {
		t.Fatal(err)
	}
	sends = env.send.sent()
	if len(sends) != 4 || !strings.Contains(sends[3].Text, "编号无效") {
		t.Fatalf("out-of-range number must be rejected, got %+v", sends)
	}
	st, _ = a.loadStore()
	if idx := findRoute(st, acc1.Account.ID); idx < 0 || st.Routes[idx].AgentActorID != gammaHex {
		t.Fatalf("invalid number must not change the mount: %+v", st.Routes)
	}
	if len(env.agent.snapshotSubmits()) != 0 {
		t.Fatal("/bind conversations must not submit turns")
	}
}

// TestUnmountCommand covers /unmount on a mounted and on an unmounted
// account.
func TestUnmountCommand(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "/unmount"}); err != nil {
		t.Fatal(err)
	}
	st, _ := a.loadStore()
	if findRoute(st, acc.ID) >= 0 {
		t.Fatalf("mount must be gone after /unmount: %+v", st.Routes)
	}
	sends := env.send.sent()
	if len(sends) != 1 || !strings.Contains(sends[0].Text, env.agentHex) {
		t.Fatalf("/unmount must confirm the released agent, got %+v", sends)
	}

	// Unmounting again answers the not-mounted notice.
	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "/unmount"}); err != nil {
		t.Fatal(err)
	}
	sends = env.send.sent()
	if len(sends) != 2 || !strings.Contains(sends[1].Text, "当前账户未挂载") {
		t.Fatalf("second /unmount must answer 当前账户未挂载, got %+v", sends)
	}
	if len(env.agent.snapshotSubmits()) != 0 {
		t.Fatal("/unmount must not submit turns")
	}
}

// TestTickReleasesGoneAgentMount covers the tick-side cascade release: a
// mount whose agent vanished from the workspace is deleted, freeing the agent
// for remounting; a live agent keeps its mount.
func TestTickReleasesGoneAgentMount(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	// Agent alive → mount survives the tick.
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatal(err)
	}
	st, _ := a.loadStore()
	if findRoute(st, acc.ID) < 0 {
		t.Fatal("mount must survive while the agent is alive")
	}

	// Agent gone → the tick releases the mount.
	otherHex := id.NewCanonical(1, 0, func() uint64 { return 9 }).Next().String()
	env.ws.setItems([]gen.AgentListItem{{ActorID: otherHex}})
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatal(err)
	}
	st, _ = a.loadStore()
	if findRoute(st, acc.ID) >= 0 {
		t.Fatalf("mount must be released when the agent is gone: %+v", st.Routes)
	}

	// The released agent can be mounted again.
	if _, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: acc.ID, AgentActorID: env.agentHex}); err != nil {
		t.Fatalf("released agent must be remountable: %v", err)
	}
}

// TestTickReconcileFailureDoesNotBlock covers the non-blocking rule: when the
// workspace state fetch fails, the tick still returns nil, the mount and the
// pending reply keep waiting, and nothing is sent.
func TestTickReconcileFailureDoesNotBlock(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	env.ctx.PlannerFn = func() actor.Planner {
		return &fakePlanner{fn: func(string, any) (any, error) {
			return nil, fmt.Errorf("workspace down")
		}}
	}
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatalf("reconcile failure must not fail the tick: %v", err)
	}
	st, _ := a.loadStore()
	if findRoute(st, acc.ID) < 0 {
		t.Fatal("mount must survive an unavailable workspace state")
	}
	if len(a.pendingReplies) != 1 {
		t.Fatalf("pending reply must keep waiting, got %+v", a.pendingReplies)
	}
	if sends := env.send.sent(); len(sends) != 0 {
		t.Fatalf("nothing may be sent on reconcile failure, got %+v", sends)
	}
}

// TestInboundReleasesGoneAgentMount covers the inbound-side cascade release:
// a message from a mounted account whose agent no longer exists releases the
// mount first, notifies the sender, and falls back to the coordinator.
func TestInboundReleasesGoneAgentMount(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	coordHex := testutil.GenActorID().String()
	survivor := id.NewCanonical(1, 0, func() uint64 { return 2 }).Next().String()
	env.ws.setItems([]gen.AgentListItem{{ActorID: survivor}})
	planner := &fakePlanner{fn: func(callID string, _ any) (any, error) {
		switch callID {
		case "workspace.agent_list_state":
			return gen.WorkspaceAgentListState{Items: env.ws.snapshot()}, nil
		case "workspace.coordinator_lookup":
			return gen.WorkspaceCoordinatorLookupResp{Found: true, ActorID: coordHex}, nil
		}
		return nil, fmt.Errorf("unexpected planner call %s", callID)
	}}
	env.ctx.PlannerFn = func() actor.Planner { return planner }

	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "还在吗"}); err != nil {
		t.Fatal(err)
	}
	st, _ := a.loadStore()
	if findRoute(st, acc.ID) >= 0 {
		t.Fatalf("stale mount must be released on inbound: %+v", st.Routes)
	}
	sends := env.send.sent()
	if len(sends) != 1 || !strings.Contains(sends[0].Text, "已自动卸载") {
		t.Fatalf("sender must be told about the auto-release, got %+v", sends)
	}
	if p := a.pendingReplies["turn-1"]; p == nil || p.AgentActorID != coordHex {
		t.Fatalf("message must fall back to the coordinator after release, got %+v", a.pendingReplies["turn-1"])
	}
}

// TestStatusShowsMountDetails covers the /status enhancement: the mounted
// agent plus its MountedAt rendered in local time; unmounted accounts report
// no mount time.
func TestStatusShowsMountDetails(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)
	st, _ := a.loadStore()
	route := st.Routes[findRoute(st, acc.ID)]

	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "/status"}); err != nil {
		t.Fatal(err)
	}
	sends := env.send.sent()
	if len(sends) != 1 {
		t.Fatalf("/status must answer once, got %+v", sends)
	}
	for _, want := range []string{"挂载 agent：" + env.agentHex, "挂载时间：" + localTimeText(route.MountedAt)} {
		if !strings.Contains(sends[0].Text, want) {
			t.Fatalf("/status %q missing %q", sends[0].Text, want)
		}
	}

	// After unmounting, no mount time is shown.
	if _, err := a.handleRouteDelete(nil, gen.ImRouteDeleteReq{ID: route.ID}); err != nil {
		t.Fatal(err)
	}
	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "/status"}); err != nil {
		t.Fatal(err)
	}
	sends = env.send.sent()
	if len(sends) != 2 || !strings.Contains(sends[1].Text, "未挂载") || strings.Contains(sends[1].Text, "挂载时间") {
		t.Fatalf("unmounted /status must drop the mount line, got %+v", sends)
	}
}

// ---------------------------------------------------------------------------
// Step extraction
// ---------------------------------------------------------------------------

func TestLastAssistantText(t *testing.T) {
	steps := []gen.Step{
		// Wrong turn, wrong role, wrong type: ignored.
		{ID: "u0", Role: "user", Type: "text", TurnID: "turn-1", Content: []gen.ContentBlock{{Type: "text", Text: "question"}}},
		{ID: "x0", Role: "assistant", Type: "text", TurnID: "turn-0", Content: []gen.ContentBlock{{Type: "text", Text: "other turn"}}},
		{ID: "r0", Role: "assistant", Type: "reasoning", TurnID: "turn-1", Content: []gen.ContentBlock{{Type: "reasoning", Text: "hidden"}}},
		// Earlier assistant text of the right turn: superseded by the last.
		{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []gen.ContentBlock{{Type: "text", Text: "earlier"}}},
		// Discarded step never wins even when last.
		{ID: "a2", Role: "assistant", Type: "text", TurnID: "turn-1", Discarded: true, Content: []gen.ContentBlock{{Type: "text", Text: "discarded"}}},
		// Last live assistant text step: multiple text blocks joined.
		{ID: "a3", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []gen.ContentBlock{
			{Type: "text", Text: "final"},
			{Type: "tool_call", ToolName: "shell"},
			{Type: "text", Text: "blocks"},
		}},
	}
	if got := lastAssistantText(steps, "turn-1"); got != "final\n\nblocks" {
		t.Fatalf("lastAssistantText = %q, want %q", got, "final\n\nblocks")
	}
	if got := lastAssistantText(steps, "turn-404"); got != "" {
		t.Fatalf("lastAssistantText unknown turn = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// Truncation
// ---------------------------------------------------------------------------

func TestTruncateReply(t *testing.T) {
	// Short text passes through untouched.
	short := "hello 世界"
	if got := truncateReply(short); got != short {
		t.Fatalf("truncateReply(short) = %q", got)
	}
	// Exactly at the cap: untouched.
	exact := strings.Repeat("a", maxReplyRunes)
	if got := truncateReply(exact); got != exact {
		t.Fatalf("text at cap must not be truncated (len=%d)", len(got))
	}
	// One rune over: cut with the marker inside the budget.
	over := strings.Repeat("b", maxReplyRunes+1)
	got := truncateReply(over)
	if n := len([]rune(got)); n != maxReplyRunes {
		t.Fatalf("truncated length = %d runes, want %d", n, maxReplyRunes)
	}
	if !strings.HasSuffix(got, replyTruncMark) {
		t.Fatalf("truncated text must end with %q", replyTruncMark)
	}
	// Multi-byte text is never split mid-rune (invalid UTF-8 check).
	cjk := strings.Repeat("界", maxReplyRunes+50)
	got = truncateReply(cjk)
	if n := len([]rune(got)); n != maxReplyRunes {
		t.Fatalf("cjk truncated length = %d runes, want %d", n, maxReplyRunes)
	}
	for _, r := range got[:len(got)-len(replyTruncMark)] {
		if r == 0xFFFD {
			t.Fatal("truncation split a multi-byte rune")
		}
	}
}

// ---------------------------------------------------------------------------
// Markdown simplification
// ---------------------------------------------------------------------------

func TestSimplifyMarkdown(t *testing.T) {
	in := "# 标题\n" +
		"这是 **加粗** 和 *斜体* 与 `代码`。\n" +
		"链接 [文档](https://example.com/docs) 在这里。\n" +
		"snake_case_name 保持不变，__下划线加粗__ 消失。\n" +
		"```\n" +
		"code **not stripped** here\n" +
		"```\n" +
		"尾部"
	want := "标题\n" +
		"这是 加粗 和 斜体 与 代码。\n" +
		"链接 文档 (https://example.com/docs) 在这里。\n" +
		"snake_case_name 保持不变，下划线加粗 消失。\n" +
		"code **not stripped** here\n" +
		"尾部"
	if got := simplifyMarkdown(in); got != want {
		t.Fatalf("simplifyMarkdown =\n%q\nwant\n%q", got, want)
	}
}

func TestDeliverReplyTruncatesLongAssistantText(t *testing.T) {
	env := newReplyTestEnv(t)
	a := newTestActor(t)
	acc := env.setupBoundAccount(t, a)

	if err := a.handleInbound(env.ctx, InboundMessage{AccountID: acc.ID, ChatID: "42", Text: "写一篇长文"}); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("段", 6000)
	env.agent.setSteps([]gen.Step{
		{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []gen.ContentBlock{{Type: "text", Text: long}}},
	})
	if err := a.handleReplyPollTick(env.ctx); err != nil {
		t.Fatal(err)
	}
	sends := env.send.sent()
	if len(sends) != 1 {
		t.Fatalf("expected one delivered reply, got %+v", sends)
	}
	if n := len([]rune(sends[0].Text)); n != maxReplyRunes {
		t.Fatalf("delivered reply length = %d runes, want %d", n, maxReplyRunes)
	}
	if !strings.HasSuffix(sends[0].Text, replyTruncMark) {
		t.Fatal("delivered reply must carry the truncation marker")
	}
}
