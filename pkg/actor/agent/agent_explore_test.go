package agent

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestOnStartChildSeedsExplorerBundlesAndSkillUse(t *testing.T) {
	a := &Actor{
		agentKind: "explorer",
		child:     childState{Mode: true},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	if err := a.onStartChild(ctx); err != nil {
		t.Fatalf("onStartChild: %v", err)
	}
	if _, ok := ctx.Regs["skill_use"]; !ok {
		t.Fatal("child agent.skill.use was not registered")
	}
	if _, ok := ctx.Regs["permission_mode_set"]; !ok {
		t.Fatal("child permission_mode_set was not registered")
	}
	if _, ok := ctx.Regs["turn_answer"]; !ok {
		t.Fatal("child turn_answer was not registered")
	}
	for _, mount := range a.ComponentMounts {
		if mount.CardID == "builtin:bundle:file-tools" && mount.Enabled {
			return
		}
	}
	t.Fatal("explorer child did not mount builtin:bundle:file-tools")
}

func TestChildTurnAnswerDeliversAskUserResponse(t *testing.T) {
	a := &Actor{
		child:          childState{Mode: true},
		ActiveTurnRef:  "child-turn",
		pendingAskUser: true,
	}
	eng := &turnEngine{
		resumeCh:        make(chan struct{}, 1),
		resumeRequestID: "ask-request",
	}
	a.turnEngineStore(eng)

	if err := a.handleTurnAnswer(testutil.HumanCtx(testutil.GenActorID()), domain.TurnAnswerReq{
		RequestID:   "ask-request",
		AnswersJSON: `{"answers":[{"text":"continue"}]}`,
	}); err != nil {
		t.Fatalf("handleTurnAnswer: %v", err)
	}
	if a.pendingAskUser {
		t.Fatal("pendingAskUser remains true after answer")
	}
	if eng.resumeAnswer != `{"answers":[{"text":"continue"}]}` {
		t.Fatalf("resumeAnswer = %q", eng.resumeAnswer)
	}
	select {
	case <-eng.resumeCh:
	default:
		t.Fatal("child engine was not resumed")
	}
}

func TestOnExploreToolExecuted_GeneralChildSetsProgress(t *testing.T) {
	a := &Actor{
		agentKind: "coder",
		child: childState{
			Mode:     true,
			AgentRef: testutil.NewFakeRef(testutil.GenActorID(), nil),
			StepRef:  "step-1",
		},
	}

	a.onExploreToolExecuted(testutil.HumanCtx(testutil.GenActorID()), "project.shell_exec", `{"command":"go test ./..."}`)

	if a.child.LastProgressFlush.IsZero() {
		t.Errorf("LastProgressFlush is zero, want a flush after tool execution")
	}
	if a.child.Phase != "searching" {
		t.Errorf("Phase = %q, want searching", a.child.Phase)
	}
	if len(a.child.ActiveWork) != 1 {
		t.Fatalf("ActiveWork = %d items, want 1", len(a.child.ActiveWork))
	}
	if a.child.ActiveWork[0].Type != "tool" {
		t.Errorf("ActiveWork[0].Type = %q, want tool", a.child.ActiveWork[0].Type)
	}
	if a.child.ActiveWork[0].Target != "go test ./..." {
		t.Errorf("ActiveWork[0].Target = %q, want extracted command", a.child.ActiveWork[0].Target)
	}
}

func TestOnExploreToolExecuted_ExplorerChildCountsReads(t *testing.T) {
	a := &Actor{
		agentKind: "explorer",
		child: childState{
			Mode:     true,
			AgentRef: testutil.NewFakeRef(testutil.GenActorID(), nil),
			StepRef:  "step-1",
		},
	}

	a.onExploreToolExecuted(testutil.HumanCtx(testutil.GenActorID()), "project.read", `{"path":"/tmp/main.go"}`)

	if a.child.ReadCnt != 1 {
		t.Errorf("ReadCnt = %d, want 1", a.child.ReadCnt)
	}
	if a.child.ActiveWork[0].Type != "read" {
		t.Errorf("ActiveWork[0].Type = %q, want read", a.child.ActiveWork[0].Type)
	}
	if a.child.ActiveWork[0].Target != "/tmp/main.go" {
		t.Errorf("ActiveWork[0].Target = %q, want /tmp/main.go", a.child.ActiveWork[0].Target)
	}
}

func TestHandleExploreProgress_IgnoresStaleChild(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	eng := &turnEngine{
		childProgressCh: make(chan struct{}, 1),
	}
	a := &Actor{ActiveTurnRef: "new-turn"}
	a.turnEngineStore(eng)

	if err := a.handleExploreProgress(ctx, domain.ForkChildProgress{
		StepID:      "old-step",
		SummaryText: "stale progress",
	}); err != nil {
		t.Fatalf("handleExploreProgress: %v", err)
	}
	if _, ok := eng.childProgressAt.Load("old-step"); ok {
		t.Fatal("stale child progress updated the new engine deadline")
	}
	select {
	case <-eng.childProgressCh:
		t.Fatal("stale child progress signaled the new engine")
	default:
	}
}

// TestHandleExploreProgress_RefreshesChildLivenessAndSignals verifies that a
// progress for a pending child refreshes the child's activity timestamp and
// wakes executeWaitChildren immediately. This is the wired-in path that the
// stale test above guards the negative side of.
func TestHandleExploreProgress_RefreshesChildLivenessAndSignals(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	eng := &turnEngine{
		childProgressCh: make(chan struct{}, 1),
		stepEventMu:     sync.Mutex{},
		openStepEvents:  map[string][]domain.StepEvent{},
		logger:          newNopActorLogger(),
	}
	eng.pendingChildSteps.Store("step-1", true)
	a := &Actor{ActiveTurnRef: "turn-1"}
	a.turnEngineStore(eng)

	before := time.Now()
	if err := a.handleExploreProgress(ctx, domain.ForkChildProgress{
		StepID:      "step-1",
		SummaryText: "still exploring",
	}); err != nil {
		t.Fatalf("handleExploreProgress: %v", err)
	}

	v, ok := eng.childProgressAt.Load("step-1")
	if !ok {
		t.Fatal("valid child progress did not refresh childProgressAt")
	}
	if ts := time.Unix(0, v.(int64)); ts.Before(before) {
		t.Fatalf("childProgressAt timestamp %v before progress was sent %v", ts, before)
	}

	select {
	case <-eng.childProgressCh:
	default:
		t.Fatal("valid child progress did not signal childProgressCh")
	}
}

// TestTailWindowSummary covers the live-progress tail window: short text
// passes through unchanged; long text is cut to the byte budget on a rune
// boundary (multibyte UTF-8 must never be split) and marked with an ellipsis.
func TestTailWindowSummary(t *testing.T) {
	short := "line one\nline two"
	if got := tailWindowSummary(short, 4096); got != short {
		t.Fatalf("short text changed: %q", got)
	}
	if got := tailWindowSummary("", 4096); got != "" {
		t.Fatalf("empty text changed: %q", got)
	}

	long := strings.Repeat("a", 5000)
	got := tailWindowSummary(long, 4096)
	if !strings.HasPrefix(got, "…\n") {
		t.Fatalf("truncated text missing ellipsis prefix: %q", got[:10])
	}
	if len(got) > 4096+len("…\n") {
		t.Fatalf("truncated text exceeds budget: %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "a") {
		t.Fatalf("tail window lost the tail: %q", got[len(got)-10:])
	}

	// Multibyte runes (3 bytes each): the window must start on a rune boundary.
	multibyte := strings.Repeat("界", 2000) // 6000 bytes
	got = tailWindowSummary(multibyte, 4096)
	if !utf8.ValidString(got) {
		t.Fatal("truncated text is not valid UTF-8 (rune split)")
	}
	body := strings.TrimPrefix(got, "…\n")
	if len(body)%3 != 0 {
		t.Fatalf("window starts mid-rune: %d bytes of 界", len(body))
	}
}

// TestHandleExploreProgress_ReplacesProgressInOpenStepEvents verifies that
// snapshot-style child progress replaces (not appends to) the step's previous
// execution_progress entries in openStepEvents, while every update still flows
// to the live stepEvents buffer. This is the regression guard for the fork
// progress freeze: unbounded openStepEvents growth made per-message snapshot
// cost quadratic and saturated the parent owner queue (cap 64), after which
// progress deliveries were dropped and the UI froze.
func TestHandleExploreProgress_ReplacesProgressInOpenStepEvents(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	eng := &turnEngine{
		childProgressCh: make(chan struct{}, 1),
		openStepEvents: map[string][]domain.StepEvent{
			"step-1": {{Kind: "step.opened", StepID: "step-1"}},
		},
		logger: newNopActorLogger(),
	}
	eng.pendingChildSteps.Store("step-1", true)
	a := &Actor{ActiveTurnRef: "turn-1"}
	a.turnEngineStore(eng)

	for i, summary := range []string{"first", "second", "third"} {
		if err := a.handleExploreProgress(ctx, domain.ForkChildProgress{
			StepID:      "step-1",
			SummaryText: summary,
		}); err != nil {
			t.Fatalf("handleExploreProgress #%d: %v", i, err)
		}
		// Drain the wake signal so the next send does not block on a full chan.
		select {
		case <-eng.childProgressCh:
		default:
		}
	}

	// Live stream keeps every update (flushEvents drains the buffer into EmitEvent).
	progressEmitted := 0
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind == "step" {
			if se, ok := ev.Payload.(domain.StepEvent); ok && se.Kind == "step.execution_progress" {
				progressEmitted++
			}
		}
	}
	if progressEmitted != 3 {
		t.Fatalf("emitted progress count = %d, want 3 (every live update must flow)", progressEmitted)
	}

	// Reconnect replay keeps only the latest snapshot per step.
	replay := eng.openStepEvents["step-1"]
	progressInReplay := 0
	for _, ev := range replay {
		if ev.Kind == "step.execution_progress" {
			progressInReplay++
			if !strings.Contains(ev.Progress, "third") {
				t.Fatalf("replayed progress is stale: %s", ev.Progress)
			}
		}
	}
	if progressInReplay != 1 {
		t.Fatalf("openStepEvents progress count = %d, want 1 (only latest snapshot)", progressInReplay)
	}
	// Non-progress events are preserved.
	if replay[0].Kind != "step.opened" {
		t.Fatalf("openStepEvents[0].Kind = %q, want step.opened preserved", replay[0].Kind)
	}
}

// TestSnapshotAfterChildProgress_Throttled verifies the per-message snapshot is
// throttled to one per second.
func TestSnapshotAfterChildProgress_Throttled(t *testing.T) {
	a := &Actor{}
	a.snapshotAfterChildProgress()
	first := a.exploreProgressSnapshotAt
	if first.IsZero() {
		t.Fatal("first call must take a snapshot and record the time")
	}
	a.snapshotAfterChildProgress()
	if !a.exploreProgressSnapshotAt.Equal(first) {
		t.Fatal("second call within 1s must be throttled (timestamp must not advance)")
	}
	a.exploreProgressSnapshotAt = time.Now().Add(-2 * time.Second)
	a.snapshotAfterChildProgress()
	if a.exploreProgressSnapshotAt.Before(time.Now().Add(-time.Second)) {
		t.Fatal("call after the interval must take a fresh snapshot")
	}
}

func TestSpawnChildAndExploreComplete_ConcurrentActiveChildren(t *testing.T) {
	const n = 50
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "project" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				if callID == "project.spawn_agent" {
					return domain.ProjectSpawnAgentResp{ActorID: testutil.GenActorID().String()}
				}
				return nil
			}), true
		}
		return nil, false
	}

	// Concurrently spawn n children, then serially complete all of them.
	// This exercises the activeChildren mutex without racing other actor state
	// that is only mutated on the actor loop in production.
	var spawnWg sync.WaitGroup
	spawnWg.Add(n)
	for i := 0; i < n; i++ {
		toolUseID := fmt.Sprintf("tool-%d", i)
		go func(id string) {
			defer spawnWg.Done()
			_, _ = a.spawnChild(ctx, childSpawnOpts{
				Kind:          "general",
				Description:   "concurrent task",
				Prompt:        "do work",
				Slot:          slotFromUnit(domain.ModelUnit{Model: "test-model", Provider: "test-provider"}),
				MaxIterations: 5,
				ToolUseID:     id,
				NamePrefix:    "general",
			})
		}(toolUseID)
	}
	spawnWg.Wait()

	for i := 0; i < n; i++ {
		toolUseID := fmt.Sprintf("tool-%d", i)
		a.handleExploreComplete(ctx, exploreCompleteReq{ToolUseID: toolUseID})
	}

	a.activeChildrenMu.Lock()
	lenActive := len(a.activeChildren)
	a.activeChildrenMu.Unlock()
	if lenActive != 0 {
		t.Fatalf("expected activeChildren to be empty after concurrent spawn/complete, got %d", lenActive)
	}
}

// TestSpawnDreamChild_SpawnsIsolatedChild verifies the internal dreamer spawn
// path (spawnDreamChild): it spawns a child under the memory-sleep toolUseID
// with a dream name prefix, tracked in activeChildren so the result can flow back
// through finishMemorySleep. This is the agent-local spawn used for memory
// consolidation; all other fork children are created by workspace.agent_spawn_by_type.
func TestSpawnDreamChild_SpawnsIsolatedChild(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	var capturedSpawnReq domain.ProjectSpawnAgentReq
	// Provide a project service ref that responds to project.spawn_agent.
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "project" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				if callID == "project.spawn_agent" {
					if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
						capturedSpawnReq = req
					}
					return domain.ProjectSpawnAgentResp{ActorID: testutil.GenActorID().String()}
				}
				return nil
			}), true
		}
		return nil, false
	}

	a := &Actor{
		fast: domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "fast-model", Provider: "openai"}},
		}},
		primary: domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "primary-model", Provider: "openai"}},
		}},
		aggRefCache: map[string]ref.Ref{
			systemAggID: fakeCompactionRef{tag: "system"},
		},
	}

	_, err := a.spawnDreamChild(ctx, "dream prompt")
	if err != nil {
		t.Fatalf("spawnDreamChild: %v", err)
	}

	spawnedName := capturedSpawnReq.SpawnName
	if !strings.HasPrefix(spawnedName, "dream-") || !strings.HasSuffix(spawnedName, "memory-sleep") {
		t.Fatalf("spawned name = %q, want dream-*-memory-sleep", spawnedName)
	}
	if capturedSpawnReq.AgentKind != domain.AgentKindDreamer {
		t.Errorf("expected AgentKind %q, got %q", domain.AgentKindDreamer, capturedSpawnReq.AgentKind)
	}
	if capturedSpawnReq.ChildConfig == nil {
		t.Fatal("expected ChildConfig to be set")
	}
	if capturedSpawnReq.ChildConfig.ParentToolUseID != "memory-sleep" {
		t.Errorf("expected ChildConfig.ParentToolUseID %q, got %q", "memory-sleep", capturedSpawnReq.ChildConfig.ParentToolUseID)
	}
	a.activeChildrenMu.Lock()
	_, tracked := a.activeChildren["memory-sleep"]
	a.activeChildrenMu.Unlock()
	if !tracked {
		t.Fatal("memory-sleep child not tracked in activeChildren")
	}
}

func TestWorkItemTarget(t *testing.T) {
	cases := []struct {
		name       string
		callableID string
		input      string
		want       string
	}{
		{"path key", "project.read", `{"path":"foo/bar.go"}`, "foo/bar.go"},
		{"Path key", "project.edit", `{"Path":"foo/bar.go"}`, "foo/bar.go"},
		{"pattern key", "project.grep", `{"pattern":"TODO"}`, "TODO"},
		{"command key", "project.shell_exec", `{"command":"go test ./..."}`, "go test ./..."},
		{"no known key falls back to short name", "project.wiki_get_card", `{"Id":"toc"}`, "wiki_get_card"},
		{"invalid json falls back to short name", "project.list", `not-json`, "list"},
		{"empty value falls back to short name", "project.glob", `{}`, "glob"},
		{"no dot in callable id", "standalone", `{}`, "standalone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := workItemTarget(tc.callableID, tc.input); got != tc.want {
				t.Fatalf("workItemTarget(%q, %q) = %q, want %q", tc.callableID, tc.input, got, tc.want)
			}
		})
	}
}

// TestTrackForkChild_RegistersInActiveChildren verifies that when a fork child is
// spawned by workspace.agent_spawn_by_type (not by the agent-local spawnChild),
// the parent agent can still track it via trackForkChild so cancellation,
// liveness checks, and explore_complete routing work.
func TestTrackForkChild_RegistersInActiveChildren(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	childActorID := testutil.GenActorID().String()
	childRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == childActorID {
			return childRef, true
		}
		return nil, false
	}

	a := &Actor{}
	a.trackForkChild(ctx, "toolu-1", childActorID)

	a.activeChildrenMu.Lock()
	gotRef, ok := a.activeChildren["toolu-1"]
	a.activeChildrenMu.Unlock()
	if !ok {
		t.Fatal("expected toolu-1 to be tracked in activeChildren")
	}
	if gotRef == nil {
		t.Fatal("expected non-nil ref for toolu-1")
	}
	if gotRef.ID().String() != childRef.ID().String() {
		t.Errorf("activeChildren ref ID = %q, want %q", gotRef.ID().String(), childRef.ID().String())
	}
}

// TestCancelChild_ResetsMemorySleepingForDreamer verifies that cancelChild
// (timeout path) resets memorySleeping when the cancelled child is the dreamer.
// The dreamer self-destructs on turn_cancel without sending explore_complete,
// so finishMemorySleep never fires. Without this reset, memorySleeping leaks
// true forever after a dreamer timeout — blocking maybeAutoStartTurn.
func TestCancelChild_ResetsMemorySleepingForDreamer(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	dreamerRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a := &Actor{
		memorySleeping:  true,
		memorySleepTurn: "turn-1",
		activeChildren:  map[string]ref.Ref{"memory-sleep": dreamerRef},
	}

	a.cancelChild(ctx, "memory-sleep")

	if a.memorySleeping {
		t.Fatal("memorySleeping = true after cancelChild, want false")
	}
	if a.memorySleepTurn != "" {
		t.Fatalf("memorySleepTurn = %q, want empty", a.memorySleepTurn)
	}
	if _, ok := a.activeChildren["memory-sleep"]; ok {
		t.Fatal("memory-sleep child still tracked after cancelChild")
	}
}

// TestCancelChild_LeavesMemorySleepingForNonDreamer verifies that cancelling a
// non-dreamer child does NOT touch memorySleeping state.
func TestCancelChild_LeavesMemorySleepingForNonDreamer(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	exploreRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a := &Actor{
		memorySleeping:  true,
		memorySleepTurn: "turn-1",
		activeChildren:  map[string]ref.Ref{"explore-1": exploreRef},
	}

	a.cancelChild(ctx, "explore-1")

	if !a.memorySleeping {
		t.Fatal("memorySleeping = false after cancelling non-dreamer, want true (unchanged)")
	}
}
