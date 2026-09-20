package agent

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestTruncateDispatchError(t *testing.T) {
	if got := truncateDispatchError(nil); got != "" {
		t.Fatalf("nil error = %q, want empty", got)
	}
	short := errors.New("http 429: quota exceeded")
	if got := truncateDispatchError(short); got != short.Error() {
		t.Fatalf("short error = %q, want passthrough", got)
	}
	long := errors.New(strings.Repeat("x", 500))
	got := truncateDispatchError(long)
	if len(got) != 203 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long error len = %d, want 203 with ellipsis suffix: %q", len(got), got)
	}
	// Multi-byte UTF-8 must not split mid-rune.
	mbLong := errors.New(strings.Repeat("配额超限", 150)) // 600 runes
	got = truncateDispatchError(mbLong)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated multi-byte error is not valid UTF-8: %q", got)
	}
	if rc := utf8.RuneCountInString(got); rc != 201 {
		t.Fatalf("truncated multi-byte rune count = %d, want 201: %q", rc, got)
	}
}

func TestHandleToolUseStartChunk_UnknownToolBecomesPendingError(t *testing.T) {
	e := &turnEngine{}
	s := &runDispatchLoopState{
		turnID:      "turn-1",
		llmStep:     &domain.TurnAction{},
		pendingByID: make(map[string]*pendingToolCall),
		toolByName:  map[string]domain.ToolSpec{},
		toolEffects: map[string]domain.EffectKind{},
		flushDeltas: func() error { return nil },
	}

	err := e.handleToolUseStartChunk(nil, domain.AggregatorChunk{
		ToolName:  "read",
		ToolUseID: "tool-1",
	}, s)
	if err != nil {
		t.Fatalf("unknown tool should not fail dispatch: %v", err)
	}
	if len(s.pending) != 1 {
		t.Fatalf("pending calls = %d, want 1", len(s.pending))
	}
	call := s.pending[0]
	if !call.UnknownTool || call.LLMName != "read" || call.ID != "tool-1" {
		t.Fatalf("pending call = %+v, want unknown tool with original name and ID", call)
	}
}

func TestHandleToolUseStartChunk_CaseInsensitiveToolName(t *testing.T) {
	e := &turnEngine{}
	s := &runDispatchLoopState{
		turnID:      "turn-1",
		llmStep:     &domain.TurnAction{},
		pendingByID: make(map[string]*pendingToolCall),
		toolByName: map[string]domain.ToolSpec{
			"file_read": {CallableID: "project.read", ServiceName: "project"},
		},
		toolEffects: map[string]domain.EffectKind{
			"file_read": domain.EffectReversible,
		},
		flushDeltas: func() error { return nil },
	}

	err := e.handleToolUseStartChunk(nil, domain.AggregatorChunk{
		ToolName:  "File_Read",
		ToolUseID: "tool-1",
	}, s)
	if err != nil {
		t.Fatalf("case-insensitive tool should not fail dispatch: %v", err)
	}
	if len(s.pending) != 1 {
		t.Fatalf("pending calls = %d, want 1", len(s.pending))
	}
	call := s.pending[0]
	if call.UnknownTool {
		t.Fatalf("expected tool to be recognized, got UnknownTool")
	}
	if call.CallableID != "project.read" {
		t.Fatalf("CallableID = %q, want project.read", call.CallableID)
	}
	if call.EffectKind != domain.EffectReversible {
		t.Fatalf("EffectKind = %q, want reversible", call.EffectKind)
	}
	if call.LLMName != "File_Read" {
		t.Fatalf("LLMName should preserve original casing, got %q", call.LLMName)
	}
}

func TestBuildToolIndex_CaseInsensitiveKeys(t *testing.T) {
	e := &turnEngine{
		tools: []domain.ToolSpec{
			{Name: "FileRead", CallableID: "project.read", EffectKind: string(domain.EffectReversible)},
		},
	}
	effects, byName := e.buildToolIndex()

	for _, key := range []string{"fileread", "project.read", "project_read", "read"} {
		if _, ok := byName[key]; !ok {
			t.Fatalf("missing expected lowercase key %q in byName", key)
		}
	}
	for _, key := range []string{"fileread", "project.read", "read"} {
		if _, ok := effects[key]; !ok {
			t.Fatalf("missing expected lowercase key %q in effects", key)
		}
	}
}

func TestBuildDispatchRequest_AppendsHotContextToSystemPrompt(t *testing.T) {
	e := &turnEngine{
		startReq: domain.TurnStartReq{CompiledContext: domain.CompiledContext{
			Instructions: &domain.CompiledInstructions{
				Base:     []string{"base instruction"},
				Resolved: []string{"resolved instruction"},
			},
			HotContext: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "goal block"},
				{Type: domain.ContentBlockText, Text: "task board"},
			},
		}},
	}
	req := e.buildDispatchRequest(nil, "turn-1", nil)
	if req.System == "" {
		t.Fatalf("expected non-empty system prompt")
	}
	wantSubstrings := []string{"base instruction", "resolved instruction", "goal block", "task board"}
	for _, want := range wantSubstrings {
		if !strings.Contains(req.System, want) {
			t.Fatalf("system prompt missing %q: %s", want, req.System)
		}
	}
	if len(req.Messages) != 0 {
		t.Fatalf("expected no messages, got %d", len(req.Messages))
	}
	if len(req.SystemBlocks) != 4 {
		t.Fatalf("expected 4 system blocks, got %d", len(req.SystemBlocks))
	}
	last := req.SystemBlocks[len(req.SystemBlocks)-1]
	if last.CacheControl != "ephemeral" {
		t.Fatalf("expected last system block to be ephemeral, got %q", last.CacheControl)
	}
}

func TestBuildDispatchRequest_MemoryLayerOrder(t *testing.T) {
	e := &turnEngine{
		startReq: domain.TurnStartReq{CompiledContext: domain.CompiledContext{
			Instructions: &domain.CompiledInstructions{
				Base:     []string{"base role", "ontology full", "experience heads"},
				Resolved: []string{"plan context"},
			},
			HotContext: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "active goal"},
				{Type: domain.ContentBlockText, Text: "session full"},
			},
		}},
	}
	req := e.buildDispatchRequest(nil, "turn-memory-order", nil)
	ordered := []string{"base role", "ontology full", "experience heads", "plan context", "active goal", "session full"}
	previous := -1
	for _, text := range ordered {
		index := strings.Index(req.System, text)
		if index <= previous {
			t.Fatalf("%q is out of order in system prompt: %s", text, req.System)
		}
		previous = index
	}
}

func TestBuildDispatchRequest_HotContextOnlyWhenNoInstructions(t *testing.T) {
	e := &turnEngine{
		startReq: domain.TurnStartReq{CompiledContext: domain.CompiledContext{
			HotContext: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "hot context"},
			},
		}},
	}
	req := e.buildDispatchRequest(nil, "turn-1", nil)
	if !strings.Contains(req.System, "hot context") {
		t.Fatalf("expected system prompt to contain hot context")
	}
	if len(req.SystemBlocks) != 1 {
		t.Fatalf("expected 1 system block, got %d", len(req.SystemBlocks))
	}
}

func TestBuildDispatchRequest_RefreshesGoalBlock(t *testing.T) {
	freshGoal := domain.ContentBlock{Type: domain.ContentBlockText, Text: goalBlockPrefix + "\nfresh goal"}
	e := &turnEngine{
		startReq: domain.TurnStartReq{CompiledContext: domain.CompiledContext{
			Instructions: &domain.CompiledInstructions{Base: []string{"base"}},
			HotContext: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: goalBlockPrefix + "\nstale goal"},
				{Type: domain.ContentBlockText, Text: "task board"},
			},
		}},
		onGoalBlockRefresh: func(ctx actor.Context) *domain.ContentBlock {
			return &freshGoal
		},
	}
	req := e.buildDispatchRequest(nil, "turn-1", nil)
	if strings.Contains(req.System, "stale goal") {
		t.Fatalf("expected stale goal to be replaced")
	}
	if !strings.Contains(req.System, "fresh goal") {
		t.Fatalf("expected fresh goal in system prompt")
	}
}

func TestBuildDispatchHistory_DoesNotInjectHotContext(t *testing.T) {
	e := &turnEngine{
		logger: newNopActorLogger(),
		history: []domain.ChatMessage{
			{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "request"}}},
			{Role: domain.ChatRoleAssistant, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "partial"}}},
		},
		startReq: domain.TurnStartReq{CompiledContext: domain.CompiledContext{
			HotContext: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hot context"}},
		}},
	}

	got := e.buildDispatchHistory(nil, 0)
	if len(got) != 2 {
		t.Fatalf("messages = %d, want 2: %+v", len(got), got)
	}
	for _, m := range got {
		for _, b := range m.Content {
			if b.Text == "hot context" {
				t.Fatalf("hot context should not appear in messages: %+v", got)
			}
		}
	}
}

func TestBuildDispatchHistory_PreservesSummaryPrefix(t *testing.T) {
	e := &turnEngine{
		logger: newNopActorLogger(),
		history: []domain.ChatMessage{
			{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "request"}}},
		},
		summarySegments: []domain.SummarySegment{{Level: 1, Text: "summary content"}},
	}

	got := e.buildDispatchHistory(nil, 0)
	if len(got) != 3 {
		t.Fatalf("expected summary prefix + history, got %d messages", len(got))
	}
}

func TestBuildHotContextMessage_FiltersEmptyBlocks(t *testing.T) {
	hot := []domain.ContentBlock{
		{Type: domain.ContentBlockText, Text: ""},
		{Type: domain.ContentBlockText, Text: "ctx"},
		{Type: domain.ContentBlockText, Text: ""},
	}
	got := buildHotContextMessage(hot)
	if got == nil {
		t.Fatalf("expected non-nil message, got nil")
	}
	if got.Role != domain.ChatRoleUser {
		t.Fatalf("expected user role, got %s", got.Role)
	}
	if len(got.Content) != 1 || got.Content[0].Text != "ctx" {
		t.Fatalf("expected single non-empty block, got %+v", got.Content)
	}
}

func TestBuildHotContextMessage_NilOrAllEmptyReturnsNil(t *testing.T) {
	if got := buildHotContextMessage(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %+v", got)
	}
	if got := buildHotContextMessage([]domain.ContentBlock{
		{Type: domain.ContentBlockText, Text: ""},
	}); got != nil {
		t.Fatalf("expected nil for all-empty input, got %+v", got)
	}
}

func TestBuildHotContextMessage_PreservesAllNonEmpty(t *testing.T) {
	hot := []domain.ContentBlock{
		{Type: domain.ContentBlockText, Text: "git: main"},
		{Type: domain.ContentBlockText, Text: "plan: do X"},
	}
	got := buildHotContextMessage(hot)
	if got == nil {
		t.Fatalf("expected non-nil message")
	}
	if len(got.Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(got.Content))
	}
}

// TestSanitizeMessages_OrphanPlanSubmitThenCancelMarker reproduces the user's
// reported scenario: a cancelled turn persists an assistant(tool_use) step
// for plan.submit with no tool_result, followed by a cancel-marker assistant
// step. compileMessages produces two consecutive assistant messages, and
// sanitizeMessages must strip the orphan tool_use so the next dispatch does
// not bounce with HTTP 400 "tool_call_ids did not have response messages".
func TestSanitizeMessages_OrphanPlanSubmitThenCancelMarker(t *testing.T) {
	msgs := []domain.ChatMessage{
		{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "refactor shell detection"}}},
		{Role: domain.ChatRoleAssistant, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "I'll start by inspecting the code."}}},
		{Role: domain.ChatRoleAssistant, Content: []domain.ContentBlock{
			{Type: domain.ContentBlockToolUse, ToolUseID: "plan_submit:26", ToolName: "plan_submit", Input: `{}`},
		}},
		{Role: domain.ChatRoleAssistant, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "用户停止了生成。"}}},
	}

	got := sanitizeMessages(msgs)
	for i, m := range got {
		for _, b := range m.Content {
			if b.Type == domain.ContentBlockToolUse {
				t.Fatalf("orphan tool_use survived sanitize at index %d: %+v", i, b)
			}
		}
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages after sanitize (orphan assistant stripped), got %d: %+v", len(got), got)
	}
	if got[2].Role != domain.ChatRoleAssistant || got[2].Content[0].Text != "用户停止了生成。" {
		t.Fatalf("cancel marker must survive, got %+v", got[2])
	}
}

func TestDefaultDispatchRetryStrategy_SerializedHTTPStatus(t *testing.T) {
	strategy := defaultDispatchRetryStrategy()

	cases := []struct {
		msg       string
		retryable bool
	}{
		{"gospore.handler.error: aiaggregator.dispatch: stream open: http 429: rate limit", true},
		{"gospore.handler.error: aiaggregator.dispatch: stream open: http 502: bad gateway", true},
		{"gospore.handler.error: aiaggregator.dispatch: stream open: http 503: service unavailable", true},
		{"gospore.handler.error: aiaggregator.dispatch: stream open: http 504: gateway timeout", true},
		{"gospore.handler.error: aiaggregator.dispatch: stream open: http 400: bad request", false},
		{"gospore.handler.error: aiaggregator.dispatch: stream open: http 499: client closed", false},
		{"context canceled", false},
		{"gospore.handler.error: aiaggregator.dispatch: stream: llmclient: stream idle timeout", true},
		{"gospore.handler.error: aiaggregator.dispatch: stream open: Post: timeout awaiting response headers", true},
		{"context deadline exceeded", false},
		// Unit-attributed variants (aggregator suffix / engine-side wrap): the
		// sentinel text survives the attribution suffix, so serialized forms
		// crossing the actor boundary stay classifiable.
		{"gospore.handler.error: aiaggregator.dispatch: stream [provider=kimi-2 model=k3 endpoint=https://api.example.com/v1]: llmclient: stream idle timeout", true},
		{"llmclient: stream idle timeout [provider=kimi-2 model=k3]", true},
		{"llmclient: stream idle timeout [provider=kimi-2 model=k3, awaiting stream open]", true},
	}

	for _, tc := range cases {
		err := errors.New(tc.msg)
		shouldRetry, _ := strategy(0, err)
		if shouldRetry != tc.retryable {
			t.Errorf("strategy(%q) retryable=%v, want %v", tc.msg, shouldRetry, tc.retryable)
		}
	}
}

// TestDefaultDispatchRetryStrategy_StreamClosed verifies a mid-stream cut
// (ErrStreamClosed, chained through the aggregator's %w wrap) is retryable so
// the turn re-dispatches instead of failing after partial output. Also covers
// the serialized form that crosses the gospore actor boundary, where the Go
// error chain is lost and only the message text survives.
func TestDefaultDispatchRetryStrategy_StreamClosed(t *testing.T) {
	strategy := defaultDispatchRetryStrategy()

	cut := fmt.Errorf("aiaggregator.dispatch: stream: %w", llmclient.ErrStreamClosed)
	shouldRetry, backoff := strategy(0, cut)
	if !shouldRetry {
		t.Fatal("mid-stream cut should be retryable")
	}
	if backoff <= 0 {
		t.Errorf("backoff = %v, want > 0", backoff)
	}

	if shouldRetry, _ := strategy(0, llmclient.ErrStreamClosed); !shouldRetry {
		t.Fatal("bare ErrStreamClosed should be retryable")
	}

	serialized := errors.New("gospore.handler.error: aiaggregator.dispatch: stream: llmclient: stream closed unexpectedly")
	if shouldRetry, _ := strategy(0, serialized); !shouldRetry {
		t.Fatal("serialized gospore.handler.error mid-stream cut should be retryable via string fallback")
	}
	if shouldRetry, _ := strategy(0, errors.New("dispatch stream closed before stream open")); shouldRetry {
		t.Fatal("open-phase close (different sentinel) should not match the cut fallback")
	}
	if shouldRetry, _ := strategy(0, errors.New("some other stream closed for real reason")); shouldRetry {
		t.Fatal("partial-word match must not trigger the cut fallback")
	}
}

// TestIsStreamCutError verifies the classification helper used by the
// openToolCalls gate and the step-reset trigger: typed chain OR serialized
// message, never unrelated errors.
func TestIsStreamCutError(t *testing.T) {
	if !isStreamCutError(llmclient.ErrStreamClosed) {
		t.Error("bare sentinel should match")
	}
	if !isStreamCutError(fmt.Errorf("wrap: %w", llmclient.ErrStreamClosed)) {
		t.Error("chained sentinel should match")
	}
	if !isStreamCutError(errors.New("gospore.handler.error: aiaggregator.dispatch: stream: llmclient: stream closed unexpectedly")) {
		t.Error("serialized form should match via string fallback")
	}
	if !isStreamCutError(errors.New("gospore.handler.error: aiaggregator.dispatch: nested stream: gospore.handler.error: aiaggregator.dispatch: stream: llmclient: stream closed unexpectedly")) {
		t.Error("nested serialized form should match")
	}
	if isStreamCutError(nil) {
		t.Error("nil should not match")
	}
	if isStreamCutError(errors.New("connection reset by peer")) {
		t.Error("unrelated network error should not match")
	}
}

func TestDefaultDispatchRetryStrategy_StreamIdleTimeout(t *testing.T) {
	strategy := defaultDispatchRetryStrategy()
	shouldRetry, _ := strategy(0, llmclient.ErrStreamIdleTimeout)
	if !shouldRetry {
		t.Fatal("stream idle timeout should be retryable")
	}
}

// TestIsPoolExhaustedError verifies the sentinel detection for the
// "whole pool exhausted" condition, including the serialized forms that
// cross the gospore actor boundary and the nested-aggregator double-exhausted
// case (both parent and child pools exhausted).
func TestIsPoolExhaustedError(t *testing.T) {
	if isPoolExhaustedError(nil) {
		t.Error("nil should not match")
	}
	if isPoolExhaustedError(errors.New("http 429: rate limit")) {
		t.Error("plain 429 without pool exhaustion should not match")
	}

	// Direct unit failover exhausted (aggregator wraps errPoolExhausted).
	direct := fmt.Errorf("aiaggregator.dispatch: stream open: %w: %w",
		llmclient.HTTPError{StatusCode: 429}, errors.New("aiaggregator: pool exhausted, no failover candidates"))
	if !isPoolExhaustedError(direct) {
		t.Error("direct unit pool-exhausted error should match")
	}

	// Serialized form crossing the actor wire.
	serialized := errors.New("gospore.handler.error: aiaggregator.dispatch: stream open: http 429: rate limit: aiaggregator: pool exhausted, no failover candidates")
	if !isPoolExhaustedError(serialized) {
		t.Error("serialized pool-exhausted error should match")
	}

	// Nested aggregator: both child and parent exhausted.
	nested := errors.New("aiaggregator.dispatch: nested stream: child exhausted: aiaggregator: pool exhausted, no failover candidates: aiaggregator: pool exhausted, no failover candidates")
	if !isPoolExhaustedError(nested) {
		t.Error("nested double-exhausted error should match")
	}

	// Nested stream cut with parent pool exhausted.
	nestedCut := errors.New("aiaggregator.dispatch: nested stream: llmclient: stream closed unexpectedly: aiaggregator: pool exhausted, no failover candidates")
	if !isPoolExhaustedError(nestedCut) {
		t.Error("nested cut pool-exhausted error should match")
	}
}

// TestDefaultDispatchRetryStrategy_PoolExhausted verifies that a
// pool-exhausted error (429 that exhausted the entire aggregator pool) is
// NOT retried, even though the underlying HTTP status is 429 (which would
// normally be retryable). The sentinel overrides the status-based retry.
func TestDefaultDispatchRetryStrategy_PoolExhausted(t *testing.T) {
	strategy := defaultDispatchRetryStrategy()

	// Direct unit: 429 + pool exhausted sentinel.
	direct := fmt.Errorf("aiaggregator.dispatch: stream open: %w: %w",
		llmclient.HTTPError{StatusCode: 429}, errors.New("aiaggregator: pool exhausted, no failover candidates"))
	shouldRetry, _ := strategy(0, direct)
	if shouldRetry {
		t.Fatal("pool-exhausted 429 should not be retried")
	}

	// Serialized across actor wire.
	serialized := errors.New("gospore.handler.error: aiaggregator.dispatch: stream open: http 429: rate limit: aiaggregator: pool exhausted, no failover candidates")
	shouldRetry, _ = strategy(0, serialized)
	if shouldRetry {
		t.Fatal("serialized pool-exhausted 429 should not be retried")
	}
}

// TestResetStreamedSteps_EmitsResetAndClearsLLMStep verifies the mid-stream
// retry reset helper: step.reset events are emitted for both the LLM step and
// the reasoning step, and the engine-side llmStep text is cleared while the
// step itself stays (State untouched, same IDs reused).
func TestResetStreamedSteps_EmitsResetAndClearsLLMStep(t *testing.T) {
	e := newReconcileTestEngine(nil)
	turnID := "turn-1"

	llmStep := domain.TurnAction{ID: "llm-1", Kind: string(domain.TurnActionLLMCall), State: "running", Text: "partial text"}
	e.upsertStep(llmStep)
	rs := domain.TurnAction{ID: "rs-1", Kind: string(domain.TurnActionReasoning), State: "running"}
	e.upsertStep(rs)

	e.resetStreamedSteps(turnID, &llmStep, "rs-1")

	if llmStep.Text != "" {
		t.Errorf("llmStep.Text = %q, want cleared", llmStep.Text)
	}
	if got := e.stepByID["llm-1"]; got.Text != "" {
		t.Errorf("engine step llm-1 Text = %q, want cleared", got.Text)
	}
	if got := e.stepByID["llm-1"]; got.State != "running" {
		t.Errorf("step state = %q, want running (step stays open)", got.State)
	}

	resets := 0
	for _, ev := range e.stepEvents {
		if ev.Kind == "step.reset" {
			resets++
			if ev.TurnID != turnID {
				t.Errorf("reset event TurnID = %q, want %q", ev.TurnID, turnID)
			}
		}
	}
	if resets != 2 {
		t.Fatalf("step.reset events = %d, want 2 (llm + reasoning)", resets)
	}
}

// TestResetStreamedSteps_SkipsUnopenedSteps verifies that with no steps opened
// (e.g. cut during reasoning-only streaming with no llm step yet) nothing is
// emitted for the missing llm step and no panic occurs.
func TestResetStreamedSteps_SkipsUnopenedSteps(t *testing.T) {
	e := newReconcileTestEngine(nil)
	var llmStep domain.TurnAction // never opened (ID == "")

	e.resetStreamedSteps("turn-1", &llmStep, "rs-1")

	for _, ev := range e.stepEvents {
		if ev.Kind == "step.reset" && ev.StepID != "rs-1" {
			t.Errorf("unexpected step.reset for %q", ev.StepID)
		}
	}
}

// TestRateLimitTerminatesUnitLockedSlot verifies the dispatch-stop policy: a
// 429 against a unit-locked slot (no aggregator fallback) terminates dispatch
// immediately, while the same 429 against an aggregator-backed slot does NOT
// (it retries so the cooled-down unit is skipped in favor of the next one).
// Non-429 errors never trigger this path.
func TestRateLimitTerminatesUnitLockedSlot(t *testing.T) {
	unitSlot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "gpt-4o", Provider: "openai"}},
	}}
	aggSlot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindAggregator, AggregatorID: "named-agg"},
	}}

	if !rateLimitTerminatesUnitLockedSlot(unitSlot, &llmclient.HTTPError{StatusCode: 429}) {
		t.Error("429 on unit-locked slot should terminate dispatch")
	}
	if rateLimitTerminatesUnitLockedSlot(aggSlot, &llmclient.HTTPError{StatusCode: 429}) {
		t.Error("429 on aggregator-backed slot must NOT terminate (retry to rotate units)")
	}
	if rateLimitTerminatesUnitLockedSlot(unitSlot, &llmclient.HTTPError{StatusCode: 503}) {
		t.Error("503 must not be treated as a rate-limit termination")
	}
	if !rateLimitTerminatesUnitLockedSlot(unitSlot, errors.New("aiaggregator.dispatch: stream open: http 429: rate limit")) {
		t.Error("serialized 429 string should terminate unit-locked slot")
	}
}

func TestExtractHTTPStatus_SerializedError(t *testing.T) {
	err := fmt.Errorf("gospore.handler.error: aiaggregator.dispatch: stream open: http 400: bad request")
	if got := extractHTTPStatus(err); got != 400 {
		t.Fatalf("expected 400, got %d", got)
	}
}

func TestExtractHTTPStatus_StructuredHTTPError(t *testing.T) {
	inner := llmclient.HTTPError{StatusCode: 429, Body: "rate limit"}
	err := fmt.Errorf("aiaggregator.dispatch: stream open: %w", inner)
	if got := extractHTTPStatus(err); got != 429 {
		t.Fatalf("expected 429, got %d", got)
	}
}

func TestExtractHTTPStatus_NoHTTPStatus(t *testing.T) {
	err := errors.New("no callable units available")
	if got := extractHTTPStatus(err); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// TestRecordDispatchSuccess_BudgetEstimateFallbackWhenNoUsage verifies that
// when a provider omits usage entirely, the budget bar is still driven by a
// tiktoken estimate (onContextBudgetEstimate) so EstimatedTokens stays non-zero
// and visible. Real usage must take the onContextBudgetUpdated path instead.
func TestRecordDispatchSuccess_BudgetEstimateFallbackWhenNoUsage(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	var realCall int
	var estimateCalled int
	var estimateValue int64
	e := &turnEngine{
		logger:   newNopActorLogger(),
		turnID:   "turn-1",
		stepByID: map[string]domain.TurnAction{},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				Instructions: &domain.CompiledInstructions{
					Base: []string{"You are a helpful coding assistant with detailed instructions."},
				},
			},
		},
		onContextBudgetUpdated: func(*domain.UsageData) { realCall++ },
		onContextBudgetEstimate: func(estimated int64) {
			estimateCalled++
			estimateValue = estimated
		},
	}

	e.recordDispatchSuccess(ctx, "turn-1", domain.TurnAction{ID: "llm-step-1"}, dispatchResult{
		iterText:   "Here is the answer.",
		stopReason: "end_turn",
		// iterUsage deliberately nil: provider reported no usage.
	})

	if realCall != 0 {
		t.Fatalf("onContextBudgetUpdated must not fire when no real usage, got %d calls", realCall)
	}
	if estimateCalled != 1 {
		t.Fatalf("expected onContextBudgetEstimate called once, got %d", estimateCalled)
	}
	if estimateValue <= 0 {
		t.Fatalf("expected positive tiktoken estimate, got %d", estimateValue)
	}
}

// TestRecordDispatchSuccess_RealUsageBypassesEstimate verifies that when the
// provider reports real usage, the estimate path is NOT taken — real data wins.
func TestRecordDispatchSuccess_RealUsageBypassesEstimate(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	var realUsage *domain.UsageData
	var estimateCalled int
	e := &turnEngine{
		logger:   newNopActorLogger(),
		turnID:   "turn-1",
		stepByID: map[string]domain.TurnAction{},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				Instructions: &domain.CompiledInstructions{
					Base: []string{"You are a helpful coding assistant."},
				},
			},
		},
		onContextBudgetUpdated:  func(u *domain.UsageData) { realUsage = u },
		onContextBudgetEstimate: func(int64) { estimateCalled++ },
	}

	e.recordDispatchSuccess(ctx, "turn-1", domain.TurnAction{ID: "llm-step-1"}, dispatchResult{
		iterText:   "answer",
		stopReason: "end_turn",
		iterUsage:  &domain.UsageData{InputTokens: 4321},
	})

	if realUsage == nil || realUsage.InputTokens != 4321 {
		t.Fatalf("expected onContextBudgetUpdated with real InputTokens=4321, got %+v", realUsage)
	}
	if estimateCalled != 0 {
		t.Fatalf("estimate path must not fire when real usage present, got %d calls", estimateCalled)
	}
}

// TestRecordIdleTimeout_SubmitsTimeoutRecord verifies that when the turn
// engine's idle timer fires it records an aistats record classified as a
// timeout, carrying the model unit and any partial usage observed before the
// stall. This is the path that compensates for the aggregator being unable to
// tell a mid-stream cancel (its own timeout vs a user pause) apart.
func TestRecordIdleTimeout_SubmitsTimeoutRecord(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	var got domain.AIStatsRecord
	var called int
	e := &turnEngine{
		turnID:      "turn-1",
		workspaceID: "ws-1",
		projectID:   "proj-1",
		agentID:     "ag-1",
		logger:      newNopActorLogger(),
		startReq: domain.TurnStartReq{
			Input: domain.TurnInput{
				Unit: &domain.ModelUnit{Provider: "openai", Model: "gpt-4o"},
			},
		},
		onStatsRecord: func(_ actor.Context, r domain.AIStatsRecord) {
			got = r
			called++
		},
	}
	s := &runDispatchLoopState{
		iterUsage: &domain.UsageData{InputTokens: 100, OutputTokens: 20},
	}

	e.recordIdleTimeout(ctx, s)

	if called != 1 {
		t.Fatalf("expected onStatsRecord called once, got %d", called)
	}
	if got.ErrorCode != llmclient.ErrorCodeTimeout {
		t.Errorf("ErrorCode: got %q, want %q", got.ErrorCode, llmclient.ErrorCodeTimeout)
	}
	if got.StopReason != llmclient.StopReasonError {
		t.Errorf("StopReason: got %q, want %q", got.StopReason, llmclient.StopReasonError)
	}
	if got.Provider != "openai" || got.Model != "gpt-4o" {
		t.Errorf("provider/model: got %s/%s", got.Provider, got.Model)
	}
	if got.ErrorMessage != llmclient.ErrStreamIdleTimeout.Error() {
		t.Errorf("ErrorMessage: got %q", got.ErrorMessage)
	}
	if got.Usage == nil || got.Usage.InputTokens != 100 {
		t.Errorf("partial usage not carried over: %+v", got.Usage)
	}
	if got.WorkspaceID != "ws-1" || got.AgentID != "ag-1" || got.TurnID != "turn-1" || got.SessionID != "turn-1" {
		t.Errorf("ids: ws=%s ag=%s turn=%s session=%s", got.WorkspaceID, got.AgentID, got.TurnID, got.SessionID)
	}
}

// TestRecordIdleTimeout_NoopWithoutCallback verifies the method is safe to
// call when no stats sink is wired (e.g. test harnesses).
func TestRecordIdleTimeout_NoopWithoutCallback(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := &turnEngine{turnID: "turn-1", workspaceID: "ws-1", logger: newNopActorLogger()}
	e.recordIdleTimeout(ctx, &runDispatchLoopState{}) // must not panic
}

// TestRecordIdleStreamFailure_AttributesResolvedUnit verifies the turn engine
// records an availability failure against the unit actually serving the
// dispatch — the aggregator-resolved deep unit — when its own idle timer fires.
// Without this, the cancelled stream leaves no cooldown footprint and every
// retry re-selects the same stalled unit.
func TestRecordIdleStreamFailure_AttributesResolvedUnit(t *testing.T) {
	orig := llmclient.DefaultProviderHealth
	t.Cleanup(func() { llmclient.DefaultProviderHealth = orig })
	llmclient.DefaultProviderHealth = llmclient.NewProviderHealth()

	e := &turnEngine{
		turnID: "turn-idle",
		logger: newNopActorLogger(),
		startReq: domain.TurnStartReq{
			Input: domain.TurnInput{
				Unit: &domain.ModelUnit{Provider: "reqprov", Model: "req-model"},
			},
		},
	}
	// The aggregator resolved a deep unit; it must win over the request unit.
	s := &runDispatchLoopState{
		resolvedUnit: domain.ModelUnit{Provider: "deep", Model: "deep-model"},
	}

	e.recordIdleStreamFailure(s)

	snap := llmclient.HealthSnapshot()
	entry, ok := snap["deep::deep-model"]
	if !ok {
		t.Fatal("expected failure recorded for resolved unit deep::deep-model")
	}
	if entry.ConsecutiveFailures != 1 {
		t.Errorf("consecutiveFailures = %d, want 1", entry.ConsecutiveFailures)
	}
	if _, ok := snap["reqprov::req-model"]; ok {
		t.Error("request-unit fallback must not be recorded when a deep resolved unit exists")
	}
}

// TestRecordIdleStreamFailure_FallsBackToRequestUnit verifies that when no
// aggregator-resolved unit is known (e.g. open-timeout), the failure is still
// attributed to the request unit so a unit-locked slot's cooldown advances.
func TestRecordIdleStreamFailure_FallsBackToRequestUnit(t *testing.T) {
	orig := llmclient.DefaultProviderHealth
	t.Cleanup(func() { llmclient.DefaultProviderHealth = orig })
	llmclient.DefaultProviderHealth = llmclient.NewProviderHealth()

	e := &turnEngine{
		turnID: "turn-idle",
		logger: newNopActorLogger(),
		startReq: domain.TurnStartReq{
			Input: domain.TurnInput{
				Unit: &domain.ModelUnit{Provider: "locked", Model: "locked-model"},
			},
		},
	}

	e.recordIdleStreamFailure(&runDispatchLoopState{})

	snap := llmclient.HealthSnapshot()
	entry, ok := snap["locked::locked-model"]
	if !ok {
		t.Fatal("expected failure recorded for request unit locked::locked-model")
	}
	if entry.ConsecutiveFailures != 1 {
		t.Errorf("consecutiveFailures = %d, want 1", entry.ConsecutiveFailures)
	}
}

// TestRecordIdleStreamFailure_NoopWithoutUnit verifies the method is safe and
// records nothing when neither a resolved nor a request unit is available
// (e.g. auto-aggregator dispatch that never opened).
func TestRecordIdleStreamFailure_NoopWithoutUnit(t *testing.T) {
	orig := llmclient.DefaultProviderHealth
	t.Cleanup(func() { llmclient.DefaultProviderHealth = orig })
	llmclient.DefaultProviderHealth = llmclient.NewProviderHealth()

	e := &turnEngine{turnID: "turn-idle", logger: newNopActorLogger()}
	e.recordIdleStreamFailure(&runDispatchLoopState{}) // must not panic

	if len(llmclient.HealthSnapshot()) != 0 {
		t.Errorf("expected no health entries, got %+v", llmclient.HealthSnapshot())
	}
}

// TestUnitAttributedIdleTimeout verifies the engine-side idle-timeout wrap:
// the error names the unit actually serving the dispatch (resolved unit wins
// over the requested one), the sentinel stays chained for retry
// classification, and an unknown unit degrades to the bare sentinel.
func TestUnitAttributedIdleTimeout(t *testing.T) {
	e := &turnEngine{
		turnID: "turn-idle",
		logger: newNopActorLogger(),
		startReq: domain.TurnStartReq{
			Input: domain.TurnInput{
				Unit: &domain.ModelUnit{Provider: "reqprov", Model: "req-model"},
			},
		},
	}

	err := e.unitAttributedIdleTimeout(&runDispatchLoopState{
		resolvedUnit: domain.ModelUnit{Provider: "deep", Model: "deep-model"},
	})
	if !errors.Is(err, llmclient.ErrStreamIdleTimeout) {
		t.Fatalf("wrapped idle timeout must keep the sentinel chained, got %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "provider=deep") || !strings.Contains(msg, "model=deep-model") {
		t.Fatalf("error must attribute the resolved unit, got %q", msg)
	}

	// No resolved unit: fall back to the request unit.
	err = e.unitAttributedIdleTimeout(&runDispatchLoopState{})
	if msg := err.Error(); !strings.Contains(msg, "provider=reqprov") || !strings.Contains(msg, "model=req-model") {
		t.Fatalf("error must fall back to the request unit, got %q", msg)
	}

	// No unit at all: bare sentinel so downstream matching is unchanged.
	bare := (&turnEngine{logger: newNopActorLogger()}).unitAttributedIdleTimeout(&runDispatchLoopState{})
	if !errors.Is(bare, llmclient.ErrStreamIdleTimeout) || bare.Error() != llmclient.ErrStreamIdleTimeout.Error() {
		t.Fatalf("unknown unit must degrade to the bare sentinel, got %v", bare)
	}
}

// TestDiagUnitPrefersResolvedUnit verifies dispatch diagnostics attribute to
// the aggregator-resolved serving unit when reported, the requested unit
// otherwise, and nil when neither is known (auto-aggregator pre-open).
func TestDiagUnitPrefersResolvedUnit(t *testing.T) {
	e := &turnEngine{
		logger: newNopActorLogger(),
		startReq: domain.TurnStartReq{
			Input: domain.TurnInput{
				Unit: &domain.ModelUnit{Provider: "reqprov", Model: "req-model"},
			},
		},
	}

	if u := e.diagUnit(); u == nil || u.Provider != "reqprov" || u.Model != "req-model" {
		t.Fatalf("diagUnit = %+v, want request unit", u)
	}

	e.dispatchResolvedUnit = domain.ModelUnit{Provider: "deep", Model: "deep-model"}
	if u := e.diagUnit(); u == nil || u.Provider != "deep" || u.Model != "deep-model" {
		t.Fatalf("diagUnit = %+v, want resolved unit", u)
	}

	if u := (&turnEngine{logger: newNopActorLogger()}).diagUnit(); u != nil {
		t.Fatalf("diagUnit = %+v, want nil without any unit", u)
	}
}

// TestHasInteractionSubmitTool verifies that the dispatch idle-timeout
// extension is triggered only when plan_submit or goal_submit is among the
// available tools.
func TestHasInteractionSubmitTool(t *testing.T) {
	e := &turnEngine{logger: newNopActorLogger()}
	tests := []struct {
		name  string
		tools map[string]domain.ToolSpec
		want  bool
	}{
		{
			name:  "empty tools",
			tools: map[string]domain.ToolSpec{},
			want:  false,
		},
		{
			name: "unrelated tools only",
			tools: map[string]domain.ToolSpec{
				"file_read": {CallableID: "project.read"},
				"file_edit": {CallableID: "project.edit"},
			},
			want: false,
		},
		{
			name: "has plan_submit",
			tools: map[string]domain.ToolSpec{
				"file_read":   {CallableID: "project.read"},
				"plan_submit": {CallableID: "plan_submit"},
			},
			want: true,
		},
		{
			name: "has goal_submit",
			tools: map[string]domain.ToolSpec{
				"file_read":   {CallableID: "project.read"},
				"goal_submit": {CallableID: "goal_submit"},
			},
			want: true,
		},
		{
			name: "has goal_card_submit",
			tools: map[string]domain.ToolSpec{
				"file_read":        {CallableID: "project.read"},
				"goal_card_submit": {CallableID: "goal_card_submit"},
			},
			want: true,
		},
		{
			name: "has both",
			tools: map[string]domain.ToolSpec{
				"plan_submit": {CallableID: "plan_submit"},
				"goal_submit": {CallableID: "goal_submit"},
			},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := e.hasInteractionSubmitTool(tc.tools); got != tc.want {
				t.Errorf("hasInteractionSubmitTool = %v, want %v", got, tc.want)
			}
		})
	}
}
