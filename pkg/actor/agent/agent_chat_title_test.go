package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestDecodeIntentResult(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  string
	}{
		{"value", domain.SummarizeResp{Text: "fix login"}, "fix login"},
		{"pointer", &domain.SummarizeResp{Text: "refactor auth"}, "refactor auth"},
		{"string", "plain title", "plain title"},
		{"bytes", []byte(`{"text":"from bytes"}`), "from bytes"},
		{"raw_json", json.RawMessage(`{"text":"from raw"}`), "from raw"},
		{"map", map[string]any{"text": "from map"}, "from map"},
		{"empty_value", domain.SummarizeResp{}, ""},
		{"nil", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decodeIntentResult(c.input)
			if got != c.want {
				t.Errorf("decodeIntentResult(%#v) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestHandleSetTitle_NotifiesWorkspace(t *testing.T) {
	actorID := testutil.GenActorID()
	a := &Actor{
		actorID:     actorID.String(),
		DisplayName: "Test Agent",
	}

	var gotReq gen.WorkspaceAgentStatusUpdateReq
	var got bool
	done := make(chan struct{})
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID == "workspace.agent_status_update" {
			switch v := payload.(type) {
			case gen.WorkspaceAgentStatusUpdateReq:
				gotReq = v
			case *gen.WorkspaceAgentStatusUpdateReq:
				if v != nil {
					gotReq = *v
				}
			}
			got = true
			close(done)
		}
		return nil
	})

	ctx := testutil.HumanCtx(actorID)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}

	// Status notifications arm a single-flight sender lazily on first notify.
	defer a.stopStatusNotifications()

	if err := a.handleSetTitle(ctx, setTitleReq{Title: "fix login"}); err != nil {
		t.Fatalf("handleSetTitle failed: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for workspace.agent_status_update")
	}

	if !got {
		t.Fatal("workspace.agent_status_update was not invoked")
	}
	if gotReq.Title != "fix login" {
		t.Errorf("title = %q, want %q", gotReq.Title, "fix login")
	}
	if gotReq.AgentActorID != actorID.String() {
		t.Errorf("AgentActorID = %q, want %q", gotReq.AgentActorID, actorID.String())
	}
}

func TestShouldInferTitle(t *testing.T) {
	cases := []struct {
		name        string
		kind        string
		turnTitle   string
		cachedTitle string
		text        string
		hasAgg      bool
		want        bool
	}{
		{"coordinator_skipped", domain.AgentKindCoordinator, "", "", "hello", true, false},
		{"coordinator_skipped_no_agg", domain.AgentKindCoordinator, "", "", "hello", false, false},
		{"coder_first_message", domain.AgentKindCoder, "", "", "hello", true, true},
		{"explicit_turn_title", domain.AgentKindCoder, "given", "", "hello", true, false},
		{"cached_title", domain.AgentKindCoder, "", "cached", "hello", true, false},
		{"no_aggregator", domain.AgentKindCoder, "", "", "hello", false, false},
		{"empty_text", domain.AgentKindCoder, "", "", "", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldInferTitle(c.kind, c.turnTitle, c.cachedTitle, c.text, c.hasAgg)
			if got != c.want {
				t.Errorf("shouldInferTitle(kind=%q, turnTitle=%q, cachedTitle=%q, text=%q, hasAgg=%v) = %v, want %v",
					c.kind, c.turnTitle, c.cachedTitle, c.text, c.hasAgg, got, c.want)
			}
		})
	}
}

func TestHandleChatSubmit_ClearStopsActiveTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		title:         "Inferred Title",
		ActiveTurnRef: "turn-1",
		Session: domain.Session{
			Turns:      []domain.Turn{{ID: "user-turn-1", Role: "user", State: "completed"}},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{NextSeq: 1},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-1", Closed: false, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-1",
			State:          "running",
			StartStepCount: 1,
		},
	}

	_, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "/clear"})
	if err != nil {
		t.Fatalf("handleChatSubmit /clear failed: %v", err)
	}

	if a.ActiveTurnRef != "" {
		t.Errorf("ActiveTurnRef = %q, want empty", a.ActiveTurnRef)
	}
	if len(a.Session.Turns) != 0 {
		t.Errorf("Session.Turns len = %d, want 0", len(a.Session.Turns))
	}
	if a.title != "" {
		t.Errorf("title = %q, want empty", a.title)
	}
}

func TestHandleSetTitle(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:     "agent-1",
		DisplayName: "Test Agent",
	}

	// First call sets the title.
	if err := a.handleSetTitle(ctx, setTitleReq{Title: "fix login"}); err != nil {
		t.Fatalf("handleSetTitle failed: %v", err)
	}
	if a.title != "fix login" {
		t.Errorf("title = %q, want %q", a.title, "fix login")
	}

	// Second call with different title is ignored because title is already set.
	if err := a.handleSetTitle(ctx, setTitleReq{Title: "other title"}); err != nil {
		t.Fatalf("handleSetTitle failed: %v", err)
	}
	if a.title != "fix login" {
		t.Errorf("title = %q, want %q", a.title, "fix login")
	}

	// Empty title is ignored.
	a.title = ""
	if err := a.handleSetTitle(ctx, setTitleReq{Title: ""}); err != nil {
		t.Fatalf("handleSetTitle failed: %v", err)
	}
	if a.title != "" {
		t.Errorf("title = %q, want empty", a.title)
	}
}

func TestHandleSetTitle_TruncatesLongTitle(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:     "agent-1",
		DisplayName: "Test Agent",
	}

	// Title well over the hard ceiling, mixing ASCII and CJK runes so we also
	// assert multi-byte characters are not split mid-codepoint.
	long := strings.Repeat("a", 50) + strings.Repeat("中", 50) // 100 runes
	if err := a.handleSetTitle(ctx, setTitleReq{Title: long}); err != nil {
		t.Fatalf("handleSetTitle failed: %v", err)
	}
	want := string([]rune(long)[:MaxTitleLen])
	if a.title != want {
		t.Errorf("title = %q, want %q", a.title, want)
	}
	if a.Title != want {
		t.Errorf("Title component = %q, want %q", a.Title, want)
	}
	if got := utf8.RuneCountInString(a.title); got != MaxTitleLen {
		t.Errorf("title rune count = %d, want %d", got, MaxTitleLen)
	}

	// A title exactly at the ceiling is left untouched.
	a.title = ""
	a.Title = ""
	exact := strings.Repeat("b", MaxTitleLen)
	if err := a.handleSetTitle(ctx, setTitleReq{Title: exact}); err != nil {
		t.Fatalf("handleSetTitle failed: %v", err)
	}
	if a.title != exact {
		t.Errorf("title = %q, want unchanged exact-length title", a.title)
	}
}

func TestHandleConfigure_OverwritesTitle(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:     "agent-1",
		DisplayName: "Test Agent",
	}

	// Seed an inferred title the way agent.title.set would.
	if err := a.handleSetTitle(ctx, setTitleReq{Title: "inferred"}); err != nil {
		t.Fatalf("handleSetTitle failed: %v", err)
	}
	if a.title != "inferred" {
		t.Fatalf("seed title = %q, want %q", a.title, "inferred")
	}

	// LLM inference (agent.title.set) cannot overwrite an existing title.
	if err := a.handleSetTitle(ctx, setTitleReq{Title: "llm override"}); err != nil {
		t.Fatalf("handleSetTitle failed: %v", err)
	}
	if a.title != "inferred" {
		t.Errorf("set-once violated: title = %q, want %q", a.title, "inferred")
	}

	// An explicit user edit via agent.configure overwrites the inferred title.
	if err := a.handleConfigure(ctx, domain.AgentConfigureReq{
		Title: "edited",
	}); err != nil {
		t.Fatalf("handleConfigure failed: %v", err)
	}
	if a.title != "edited" {
		t.Errorf("title = %q, want %q (explicit edit should overwrite)", a.title, "edited")
	}
	if a.Title != "edited" {
		t.Errorf("Title component = %q, want %q", a.Title, "edited")
	}

	// Explicit edits are subject to the same hard truncation ceiling.
	long := strings.Repeat("a", 50) + strings.Repeat("中", 50)
	if err := a.handleConfigure(ctx, domain.AgentConfigureReq{
		Title: long,
	}); err != nil {
		t.Fatalf("handleConfigure failed: %v", err)
	}
	want := string([]rune(long)[:MaxTitleLen])
	if a.title != want {
		t.Errorf("title = %q, want truncated %q", a.title, want)
	}

	// An empty Title in configure leaves the existing title untouched.
	if err := a.handleConfigure(ctx, domain.AgentConfigureReq{}); err != nil {
		t.Fatalf("handleConfigure failed: %v", err)
	}
	if a.title != want {
		t.Errorf("title = %q, want unchanged %q", a.title, want)
	}
}

// TestHandleChatSubmit_InferTitleEndToEnd locks the full wiring of the async
// title-inference chain: handleChatSubmit must resolve the fast-slot
// aggregator, invoke aiaggregator.intent off-loop, decode the SummarizeResp,
// and deliver title_set back to the agent so a.title is assigned. The fake
// self-ref JSON round-trips the payload, mirroring cross-actor serialization.
func TestHandleChatSubmit_InferTitleEndToEnd(t *testing.T) {
	actorID := testutil.GenActorID()
	var intentCalls atomic.Int32
	var delivered atomic.Pointer[setTitleReq]

	aggRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID != "aiaggregator.intent" {
			return nil
		}
		intentCalls.Add(1)
		return domain.SummarizeResp{Text: "fix login bug"}
	})
	var agentPtr *Actor
	ctx := testutil.HumanCtx(actorID)
	selfRef := testutil.NewFakeRef(actorID, func(callID string, payload any) any {
		if callID != "title_set" {
			return nil
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		var req setTitleReq
		if err := json.Unmarshal(body, &req); err != nil {
			return err
		}
		delivered.Store(&req)
		// Mirror the real cell dispatching the delivered frame to the
		// registered handler.
		return agentPtr.handleSetTitle(ctx, req)
	})

	ctx.SelfRef = selfRef

	a := &Actor{
		actorID:       actorID.String(),
		agentKind:     "coder",
		ActiveTurnRef: "turn-1",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-1", Role: "assistant", State: "paused", TurnOrder: 2},
			},
			ActiveHead: 1,
		},
		RawSession:    domain.RawSession{NextIdx: 1, NextSeq: 1, NextTurnOrder: 3},
		snapshotReady: true,
		aggRefCache:   map[string]ref.Ref{systemAggID: aggRef},
	}
	agentPtr = a

	if _, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "fix the login bug"}); err != nil {
		t.Fatalf("handleChatSubmit: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for a.title == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if intentCalls.Load() != 1 {
		t.Errorf("aiaggregator.intent calls = %d, want 1", intentCalls.Load())
	}
	req := delivered.Load()
	if req == nil {
		t.Fatal("title_set was never delivered to the agent self-ref")
	}
	if req.Title != "fix login bug" {
		t.Errorf("title_set payload = %q, want %q", req.Title, "fix login bug")
	}
	if a.title != "fix login bug" {
		t.Errorf("a.title = %q, want %q (handleSetTitle must apply the inferred title)", a.title, "fix login bug")
	}
}

// TestHandleChatSubmit_TitleFallsBackToSystemAggregator covers the named
// fast-aggregator failure mode seen in production: the custom aggregator's
// pool only holds nested-aggregator refs, aiaggregator.intent fails, and the
// agent must retry against the system aggregator before giving up.
func TestHandleChatSubmit_TitleFallsBackToSystemAggregator(t *testing.T) {
	actorID := testutil.GenActorID()
	var namedCalls, sysCalls atomic.Int32
	var delivered atomic.Pointer[setTitleReq]

	namedRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID != "aiaggregator.intent" {
			return nil
		}
		namedCalls.Add(1)
		return fmt.Errorf("aiaggregator.intent: unsupported protocol \"\"")
	})
	sysRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID != "aiaggregator.intent" {
			return nil
		}
		sysCalls.Add(1)
		return domain.SummarizeResp{Text: "fallback title"}
	})

	var agentPtr *Actor
	ctx := testutil.HumanCtx(actorID)
	selfRef := testutil.NewFakeRef(actorID, func(callID string, payload any) any {
		if callID != "title_set" {
			return nil
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		var req setTitleReq
		if err := json.Unmarshal(body, &req); err != nil {
			return err
		}
		delivered.Store(&req)
		return agentPtr.handleSetTitle(ctx, req)
	})
	ctx.SelfRef = selfRef

	a := &Actor{
		actorID:   actorID.String(),
		agentKind: "coder",
		fast: domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindAggregator, AggregatorID: "custom-failing"},
		}},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-1", Role: "assistant", State: "paused", TurnOrder: 2},
			},
			ActiveHead: 1,
		},
		RawSession:    domain.RawSession{NextIdx: 1, NextSeq: 1, NextTurnOrder: 3},
		snapshotReady: true,
		aggRefCache: map[string]ref.Ref{
			"custom-failing": namedRef,
			systemAggID:      sysRef,
		},
	}
	agentPtr = a

	if _, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "classify these mushrooms"}); err != nil {
		t.Fatalf("handleChatSubmit: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for a.title == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if namedCalls.Load() != 1 {
		t.Errorf("named aggregator intent calls = %d, want 1", namedCalls.Load())
	}
	if sysCalls.Load() != 1 {
		t.Errorf("system aggregator fallback calls = %d, want 1", sysCalls.Load())
	}
	req := delivered.Load()
	if req == nil {
		t.Fatal("title_set was never delivered")
	}
	if req.Title != "fallback title" {
		t.Errorf("title_set payload = %q, want %q", req.Title, "fallback title")
	}
	if a.title != "fallback title" {
		t.Errorf("a.title = %q, want %q", a.title, "fallback title")
	}
}

func TestSlotPrimaryAggID(t *testing.T) {
	cases := []struct {
		name string
		slot domain.ModelSlot
		want string
	}{
		{"empty", domain.ModelSlot{}, systemAggID},
		{"auto", domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: modelRefKindAuto}}}, systemAggID},
		{"aggregator_named", domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: modelRefKindAggregator, AggregatorID: "custom-x"}}}, "custom-x"},
		{"aggregator_unnamed", domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: modelRefKindAggregator}}}, systemAggID},
		{"unit_via_named", domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "m", Provider: "p"}, AggregatorID: "custom-x"}}}, "custom-x"},
		{"unit_system", domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "m", Provider: "p"}}}}, systemAggID},
	}
	for _, tc := range cases {
		if got := slotPrimaryAggID(tc.slot); got != tc.want {
			t.Errorf("%s: slotPrimaryAggID = %q, want %q", tc.name, got, tc.want)
		}
	}
}
