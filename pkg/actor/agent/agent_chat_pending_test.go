package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestQueuePendingSubmit_ReturnsMessageID(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-active", Role: "assistant", State: "running"},
			},
			ActiveHead: 0,
		},
		ActiveTurnRef: "turn-active",
	}

	resp, err := a.queuePendingSubmit(ctx, domain.TurnInput{Text: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.MessageID == "" {
		t.Fatalf("MessageID is empty — frontend cannot bind tempId")
	}
	if resp.Timestamp == "" {
		t.Errorf("Timestamp is empty")
	}

	pending := a.pendingSubmits["turn-active"]
	if len(pending) != 1 {
		t.Fatalf("pendingSubmits has %d entries, want 1", len(pending))
	}
	if pending[0].ID != resp.MessageID {
		t.Errorf("pending[0].ID = %q, want resp.MessageID %q", pending[0].ID, resp.MessageID)
	}
	if pending[0].Text != "hello" {
		t.Errorf("pending[0].Text = %q, want %q", pending[0].Text, "hello")
	}
}

func TestQueuePendingSubmits_AllocatesDistinctIDs(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{
			Turns:      []domain.Turn{{ID: "turn-active", Role: "assistant", State: "running"}},
			ActiveHead: 0,
		},
		ActiveTurnRef: "turn-active",
	}

	r1, _ := a.queuePendingSubmit(ctx, domain.TurnInput{Text: "first"})
	r2, _ := a.queuePendingSubmit(ctx, domain.TurnInput{Text: "second"})

	if r1.MessageID == "" || r2.MessageID == "" {
		t.Fatalf("MessageID empty: r1=%q r2=%q", r1.MessageID, r2.MessageID)
	}
	if r1.MessageID == r2.MessageID {
		t.Errorf("distinct pending submits share MessageID %q — frontend cannot bind both tempIds", r1.MessageID)
	}

	pending := a.pendingSubmits["turn-active"]
	if len(pending) != 2 {
		t.Fatalf("pendingSubmits has %d entries, want 2", len(pending))
	}
	if pending[0].ID == pending[1].ID {
		t.Errorf("pending IDs collide: %s", pending[0].ID)
	}
}

func TestConsumePendingSubmits_UsesActiveTurnRefBeforeActiveHead(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "stale-head", Role: "assistant", State: "completed"},
				{ID: "active-ref", Role: "assistant", State: "running"},
			},
			ActiveHead: 0,
		},
		ActiveTurnRef: "active-ref",
		RawSession:    domain.RawSession{NextSeq: 1},
		pendingSubmits: map[string][]domain.PendingSubmit{
			"active-ref": {{ID: "ps-1", Text: "injected"}},
		},
	}

	messages := a.consumePendingSubmits(ctx)
	if len(messages) != 1 || messages[0].Content[0].Text != "injected" {
		t.Fatalf("consumePendingSubmits() = %#v, want active-ref pending message", messages)
	}
	if _, ok := a.pendingSubmits["active-ref"]; ok {
		t.Error("active-ref pending submissions were not cleared")
	}
	for _, ev := range ctx.EmittedEvents {
		se, ok := ev.Payload.(domain.StepEvent)
		if ok && se.TurnID != "active-ref" {
			t.Errorf("event %s TurnID=%q, want active-ref", se.Kind, se.TurnID)
		}
	}
}

func TestConsumePendingSubmits_MergesSameMeta(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-active", Role: "assistant", State: "running"},
			},
			ActiveHead: 0,
		},
		ActiveTurnRef: "turn-active",
		RawSession:    domain.RawSession{NextSeq: 1},
		pendingSubmits: map[string][]domain.PendingSubmit{
			"turn-active": {
				{ID: "ps-1", Text: "msg-one"},
				{ID: "ps-2", Text: "msg-two"},
			},
		},
	}

	messages := a.consumePendingSubmits(ctx)

	// Same (empty → "user") meta: the two submits must merge into one message.
	if len(messages) != 1 {
		t.Fatalf("consumePendingSubmits returned %d messages, want 1 (merged same meta)", len(messages))
	}
	if messages[0].Role != domain.ChatRoleUser {
		t.Errorf("messages[0].Role = %q, want user", messages[0].Role)
	}
	var got strings.Builder
	for _, b := range messages[0].Content {
		if b.Type == domain.ContentBlockText {
			got.WriteString(b.Text)
		}
	}
	wantMerged := "msg-one" + string(rune(10)) + "msg-two"
	if got.String() != wantMerged {
		t.Errorf("merged text = %q, want %q", got.String(), wantMerged)
	}

	// Exactly one user_inject step must exist, keyed by the first submit's ID.
	steps := []domain.Step{}
	for _, s := range a.steps {
		if s.Type == "user_inject" {
			steps = append(steps, s)
		}
	}
	if len(steps) != 1 {
		t.Fatalf("user_inject steps = %d, want 1", len(steps))
	}
	if steps[0].ID != "ps-1" {
		t.Errorf("merged step ID = %q, want ps-1", steps[0].ID)
	}
	var stepText strings.Builder
	for _, b := range steps[0].Content {
		if b.Text != "" {
			stepText.WriteString(b.Text)
		}
	}
	if stepText.String() != wantMerged {
		t.Errorf("step ps-1 text = %q, want %q", stepText.String(), wantMerged)
	}

	// Both pending entries must be confirmable: the primary step.opened carries
	// ps-1 as OriginMessageID, and a duplicate step.opened carries ps-2. Only
	// one step.closed is emitted for the merged group.
	openedCount := 0
	closedCount := 0
	seenStepIDs := map[string]bool{}
	confirmedIDs := map[string]bool{}
	for _, ev := range ctx.EmittedEvents {
		se, ok := ev.Payload.(domain.StepEvent)
		if !ok {
			continue
		}
		if se.StepID == "" {
			t.Errorf("event %s: empty StepID", se.Kind)
		}
		if se.TurnID != "turn-active" {
			t.Errorf("event %s: TurnID=%q, want turn-active", se.Kind, se.TurnID)
		}
		if se.OriginMessageID != "" {
			confirmedIDs[se.OriginMessageID] = true
		}
		if se.Kind == "step.opened" {
			if se.StepType != "user_inject" {
				t.Errorf("step.opened StepType=%q, want user_inject", se.StepType)
			}
			openedCount++
			seenStepIDs[se.StepID] = true
		}
		if se.Kind == "step.closed" {
			closedCount++
		}
	}
	if openedCount != 2 {
		t.Errorf("step.opened count = %d, want 2 (primary + confirmation)", openedCount)
	}
	if closedCount != 1 {
		t.Errorf("step.closed count = %d, want 1 (merged group)", closedCount)
	}
	if len(seenStepIDs) != 1 {
		t.Errorf("distinct step IDs in events = %d, want 1", len(seenStepIDs))
	}
	if !confirmedIDs["ps-1"] || !confirmedIDs["ps-2"] {
		t.Errorf("confirmed OriginMessageIDs = %v, want both ps-1 and ps-2", confirmedIDs)
	}

	if _, ok := a.pendingSubmits["turn-active"]; ok {
		t.Errorf("pendingSubmits[turn-active] not cleared after consume")
	}
}

func TestConsumePendingSubmits_KeepsDistinctMetaSeparate(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{
			Turns:      []domain.Turn{{ID: "turn-active", Role: "assistant", State: "running"}},
			ActiveHead: 0,
		},
		ActiveTurnRef: "turn-active",
		RawSession:    domain.RawSession{NextSeq: 1},
		pendingSubmits: map[string][]domain.PendingSubmit{
			"turn-active": {
				{ID: "ps-1", Text: "from-alice", Meta: "user|alice|Alice"},
				{ID: "ps-2", Text: "from-bob", Meta: "user|bob|Bob"},
			},
		},
	}

	messages := a.consumePendingSubmits(ctx)

	// Distinct meta: two separate user messages, each with its own step.
	if len(messages) != 2 {
		t.Fatalf("consumePendingSubmits returned %d messages, want 2 (distinct meta)", len(messages))
	}
	// Distinct meta: two separate user messages, each with its own step. Since
	// the peer-sender wiring each carries a structured sender annotation
	// (parseSenderMeta → applyPeerSenderPrefix), assert on the suffix text.
	if got, want := messages[0].Content[0].Text, `[This message is from user "Alice" (alice).]`+"\n\nfrom-alice"; got != want {
		t.Errorf("messages[0] text = %q, want %q", got, want)
	}
	if got, want := messages[1].Content[0].Text, `[This message is from user "Bob" (bob).]`+"\n\nfrom-bob"; got != want {
		t.Errorf("messages[1] text = %q, want %q", got, want)
	}

	stepByID := map[string]domain.Step{}
	for _, s := range a.steps {
		if s.Type == "user_inject" {
			stepByID[s.ID] = s
		}
	}
	if len(stepByID) != 2 {
		t.Fatalf("user_inject steps = %d, want 2", len(stepByID))
	}
	if stepByID["ps-1"].Meta != "user|alice|Alice" {
		t.Errorf("ps-1 meta = %q, want user|alice|Alice", stepByID["ps-1"].Meta)
	}
	if stepByID["ps-2"].Meta != "user|bob|Bob" {
		t.Errorf("ps-2 meta = %q, want user|bob|Bob", stepByID["ps-2"].Meta)
	}

	openedCount := 0
	closedCount := 0
	for _, ev := range ctx.EmittedEvents {
		se, ok := ev.Payload.(domain.StepEvent)
		if !ok {
			continue
		}
		if se.Kind == "step.opened" {
			openedCount++
		}
		if se.Kind == "step.closed" {
			closedCount++
		}
	}
	if openedCount != 2 {
		t.Errorf("step.opened count = %d, want 2 (one per distinct meta)", openedCount)
	}
	if closedCount != 2 {
		t.Errorf("step.closed count = %d, want 2", closedCount)
	}
}

func TestConsumePendingSubmits_MergesAttachmentsAndImages(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{
			Turns:      []domain.Turn{{ID: "turn-active", Role: "assistant", State: "running"}},
			ActiveHead: 0,
		},
		ActiveTurnRef: "turn-active",
		RawSession:    domain.RawSession{NextSeq: 1},
		pendingSubmits: map[string][]domain.PendingSubmit{
			"turn-active": {
				{
					ID:     "ps-img",
					Images: []gen.ImageEntry{{URL: "https://example/x.png", MimeType: "image/png"}},
				},
				{
					ID:          "ps-att",
					Attachments: []gen.AttachmentEntry{{Name: "doc.pdf", MimeType: "application/pdf"}},
				},
			},
		},
	}

	messages := a.consumePendingSubmits(ctx)

	// Same meta → merged into one message with both image and attachment blocks.
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1 (merged same meta)", len(messages))
	}

	stepByID := map[string]domain.Step{}
	for _, s := range a.steps {
		stepByID[s.ID] = s
	}

	mergedStep, ok := stepByID["ps-img"]
	if !ok {
		t.Fatalf("missing step ps-img")
	}

	hasImage := false
	hasAtt := false
	for _, b := range mergedStep.Content {
		if b.Type == domain.ContentBlockImage && b.ImageURL == "https://example/x.png" {
			hasImage = true
		}
		if b.Type == domain.ContentBlockText && strings.Contains(b.Text, "doc.pdf") {
			hasAtt = true
		}
	}
	if !hasImage {
		t.Errorf("merged step ps-img missing image block: %+v", mergedStep.Content)
	}
	if !hasAtt {
		t.Errorf("merged step ps-img missing attachment placeholder: %+v", mergedStep.Content)
	}
}

func TestConsumePendingSubmits_NoOpWithoutActiveTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session:    domain.Session{ActiveHead: -1},
		RawSession: domain.RawSession{NextSeq: 1},
		pendingSubmits: map[string][]domain.PendingSubmit{
			"orphan": {{ID: "ps-1", Text: "nope"}},
		},
	}

	messages := a.consumePendingSubmits(ctx)
	if messages != nil {
		t.Errorf("expected nil messages when no active turn, got %d", len(messages))
	}
	if len(ctx.EmittedEvents) != 0 {
		t.Errorf("emitted %d events on empty consume, want 0", len(ctx.EmittedEvents))
	}
}

func TestConsumePendingSubmits_StepRoleAssistantButChatMessageUser(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{
			Turns:      []domain.Turn{{ID: "turn-active", Role: "assistant", State: "running"}},
			ActiveHead: 0,
		},
		ActiveTurnRef:  "turn-active",
		RawSession:     domain.RawSession{NextSeq: 1},
		pendingSubmits: map[string][]domain.PendingSubmit{"turn-active": {{ID: "ps-1", Text: "hello"}}},
	}

	a.consumePendingSubmits(ctx)

	var step domain.Step
	for _, s := range a.steps {
		if s.ID == "ps-1" {
			step = s
			break
		}
	}
	if step.ID == "" {
		t.Fatalf("missing step ps-1")
	}
	if step.Role != "assistant" {
		t.Errorf("user_inject step.Role = %q, want assistant for UI timeline", step.Role)
	}
	if step.Type != "user_inject" {
		t.Errorf("user_inject step.Type = %q, want user_inject", step.Type)
	}

	msgs := a.compileMessages(false)
	var found bool
	for _, m := range msgs {
		if m.ID == "ps-1" && m.Role == "user" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("compileMessages did not remap user_inject step to user role")
	}
}

func TestConsumePendingSubmits_MetaPropagated(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{
			Turns:      []domain.Turn{{ID: "turn-active", Role: "assistant", State: "running"}},
			ActiveHead: 0,
		},
		ActiveTurnRef:  "turn-active",
		RawSession:     domain.RawSession{NextSeq: 1},
		pendingSubmits: map[string][]domain.PendingSubmit{"turn-active": {{ID: "ps-1", Text: "hello", Meta: "user|alice|Alice"}}},
	}

	a.consumePendingSubmits(ctx)

	var step domain.Step
	for _, s := range a.steps {
		if s.ID == "ps-1" {
			step = s
			break
		}
	}
	if step.Meta != "user|alice|Alice" {
		t.Errorf("user_inject step.Meta = %q, want user|alice|Alice", step.Meta)
	}
}

func TestCreateUserTurn_MetaDefaultsToUser(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session:    domain.Session{ActiveHead: -1},
		RawSession: domain.RawSession{NextSeq: 1},
	}

	_, _, turn := a.createUserTurn(ctx, domain.TurnInput{Text: "hi"})
	var step domain.Step
	for _, s := range a.steps {
		if s.ID == turn.ID {
			step = s
			break
		}
	}
	if step.Meta != "user" {
		t.Errorf("user step.Meta = %q, want default user", step.Meta)
	}
}

func TestCreateUserTurn_MetaFromTurnInput(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session:    domain.Session{ActiveHead: -1},
		RawSession: domain.RawSession{NextSeq: 1},
	}

	_, _, turn := a.createUserTurn(ctx, domain.TurnInput{Text: "hi", Meta: "user|bob|Bobby"})
	var step domain.Step
	for _, s := range a.steps {
		if s.ID == turn.ID {
			step = s
			break
		}
	}
	if step.Meta != "user|bob|Bobby" {
		t.Errorf("user step.Meta = %q, want user|bob|Bobby", step.Meta)
	}
}

// TestChatSubmit_RecoveredPausedTurnQueuesMessage reproduces the "restart then
// send while paused" bug: after a crash/lazy-load recovery the turn engine is
// gone but ActiveTurnRef still points to a paused turn. Before the fix the
// message superseded the paused turn (cancel + brand-new turn); now it must be
// queued into the paused turn's pending submits, exactly like the live-pause
// path, so turn_resume injects it in place.
func TestChatSubmit_RecoveredPausedTurnQueuesMessage(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
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
	}
	// No turnEngine stored: simulates the dead engine after app restart.

	resp, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "continue with this"})
	if err != nil {
		t.Fatalf("handleChatSubmit error: %v", err)
	}
	if len(a.Session.Turns) != 2 {
		t.Fatalf("Session.Turns grew to %d — message must not spawn a new turn while recovered-paused", len(a.Session.Turns))
	}
	if got := a.Session.Turns[1].State; got != "paused" {
		t.Errorf("paused turn state = %q, want paused (must not be cancelled)", got)
	}
	if a.ActiveTurnRef != "turn-1" {
		t.Errorf("ActiveTurnRef = %q, want turn-1", a.ActiveTurnRef)
	}
	if resp.TurnActorID != "" {
		t.Errorf("TurnActorID = %q, want empty (no new turn scheduled)", resp.TurnActorID)
	}
	pending := a.pendingSubmits["turn-1"]
	if len(pending) != 1 {
		t.Fatalf("pendingSubmits[turn-1] has %d entries, want 1", len(pending))
	}
	if pending[0].Text != "continue with this" {
		t.Errorf("pending text = %q", pending[0].Text)
	}
	if pending[0].ID != resp.MessageID {
		t.Errorf("pending ID %q != resp.MessageID %q — frontend cannot bind tempId", pending[0].ID, resp.MessageID)
	}
}

// TestPendingSubmits_SurviveMailboxRoundTrip guards persistence: messages
// queued onto a paused turn must survive an app restart so turn_resume can
// still inject them in place.
func TestPendingSubmits_SurviveMailboxRoundTrip(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	actorID := "019f30e43afd00000000000000000004"
	src := &Actor{
		actorID:       actorID,
		snapshotReady: true,
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", Seq: 1, State: "completed"},
				{ID: "t2", Role: "assistant", Seq: 2, State: "paused"},
			},
			ActiveHead: 1,
		},
		ActiveTurnRef: "t2",
		pendingSubmits: map[string][]domain.PendingSubmit{
			"t2": {{ID: "ps-1", Text: "queued while paused", Timestamp: "2026-08-07T00:00:00Z"}},
		},
	}
	src.saveMailbox(nil)

	reloaded := &Actor{actorID: actorID, snapshotReady: true}
	reloaded.loadMailbox(nil)

	pending := reloaded.pendingSubmits["t2"]
	if len(pending) != 1 {
		t.Fatalf("pendingSubmits[t2] after reload has %d entries, want 1", len(pending))
	}
	if pending[0].ID != "ps-1" || pending[0].Text != "queued while paused" {
		t.Errorf("restored pending submit = %+v", pending[0])
	}
}
