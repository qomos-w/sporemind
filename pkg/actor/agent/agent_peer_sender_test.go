package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// parseSenderMeta must accept the three-part agent/user meta forms and reject
// everything else (bare markers, unrelated metas).
func TestParseSenderMeta(t *testing.T) {
	cases := []struct {
		meta             string
		wantKind         string
		wantID           string
		wantName         string
		wantOK           bool
	}{
		{meta: "agent|Coder#0001|Bob the Builder", wantKind: "agent", wantID: "Coder#0001", wantName: "Bob the Builder", wantOK: true},
		{meta: "agent||Bob the Builder", wantKind: "agent", wantID: "", wantName: "Bob the Builder", wantOK: true},
		{meta: "agent|Coder#0001|", wantKind: "agent", wantID: "Coder#0001", wantName: "", wantOK: true},
		{meta: "user|alice|Alice", wantKind: "user", wantID: "alice", wantName: "Alice", wantOK: true},
		{meta: "user|admin|admin", wantOK: false},
		{meta: "agent", wantOK: false},
		{meta: "user", wantOK: false},
		{meta: "agent||", wantOK: false},
		{meta: "goal", wantOK: false},
		{meta: "workflow", wantOK: false},
		{meta: "", wantOK: false},
	}
	for _, tc := range cases {
		kind, sid, name, ok := parseSenderMeta(tc.meta)
		if ok != tc.wantOK || kind != tc.wantKind || sid != tc.wantID || name != tc.wantName {
			t.Errorf("parseSenderMeta(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
				tc.meta, kind, sid, name, ok, tc.wantKind, tc.wantID, tc.wantName, tc.wantOK)
		}
	}
}

// applyPeerSenderPrefix must prepend the sender annotation to the first text
// block, creating one when the message has none.
func TestApplyPeerSenderPrefix(t *testing.T) {
	msg := domain.ChatMessage{
		Role: domain.ChatRoleUser,
		Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: "please continue"},
		},
	}
	applyPeerSenderPrefix(&msg, "agent", "Coder#0001", "Bob the Builder")
	if len(msg.Content) != 1 {
		t.Fatalf("content blocks = %d, want 1 (prefixed in place)", len(msg.Content))
	}
	got := msg.Content[0].Text
	if !strings.HasPrefix(got, `[This message is from agent "Bob the Builder" (Coder#0001), not the human operator.]`) {
		t.Errorf("prefixed text = %q, want agent sender annotation prefix", got)
	}
	if !strings.HasSuffix(got, "\n\nplease continue") {
		t.Errorf("prefixed text = %q, want original text preserved after blank line", got)
	}

	// Name-only meta: no dangling id part.
	msg2 := domain.ChatMessage{Role: domain.ChatRoleUser}
	applyPeerSenderPrefix(&msg2, "user", "", "Alice")
	if msg2.Content[0].Text != "[This message is from user \"Alice\".]\n\n" {
		t.Errorf("name-only prefix = %q", msg2.Content[0].Text)
	}
}

// consumePendingSubmits must annotate peer-agent submits with the sender
// prefix so a mid-turn peer message reaches the LLM with its origin labeled.
func TestConsumePendingSubmits_PeerSubmitCarriesSenderPrefix(t *testing.T) {
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
				{ID: "ps-1", Text: "peer says hi", Meta: "agent|Worker#0007|Bob the Builder"},
			},
		},
	}

	messages := a.consumePendingSubmits(ctx)
	if len(messages) != 1 {
		t.Fatalf("consumePendingSubmits returned %d messages, want 1", len(messages))
	}
	text := ""
	for _, b := range messages[0].Content {
		if b.Type == domain.ContentBlockText {
			text = b.Text
			break
		}
	}
	if !strings.HasPrefix(text, `[This message is from agent "Bob the Builder" (Worker#0007), not the human operator.]`) {
		t.Errorf("merged peer submit text = %q, want sender annotation prefix", text)
	}
	if !strings.HasSuffix(text, "peer says hi") {
		t.Errorf("merged peer submit text = %q, want original text preserved", text)
	}

	// The user_inject step itself must keep the raw meta (frontend parses it
	// for the avatar); only the LLM-bound message gets the prefix.
	var injectStep *domain.Step
	for i := range a.steps {
		if a.steps[i].Type == "user_inject" {
			injectStep = &a.steps[i]
			break
		}
	}
	if injectStep == nil {
		t.Fatal("no user_inject step created")
	}
	if injectStep.Meta != "agent|Worker#0007|Bob the Builder" {
		t.Errorf("user_inject step Meta = %q, want raw peer meta preserved for the frontend", injectStep.Meta)
	}
}

// compileMessagesWithSource must annotate user steps carrying the peer meta,
// including user_inject steps (role-normalized to user).
func TestCompileMessages_PeerMetaAnnotated(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "user"},
				{ID: "turn-2", Role: "assistant"},
			},
			ActiveHead: 1,
		},
		steps: []domain.Step{
			{ID: "s-1", TurnID: "turn-1", Role: "user", Type: "text", Closed: true, Meta: "agent|Worker#0007|Bob the Builder",
				Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "peer msg"}}},
			{ID: "s-2", TurnID: "turn-2", Role: "assistant", Type: "user_inject", Closed: true, Meta: "user|alice|Alice",
				Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "injected msg"}}},
		},
	}

	msgs := a.compileMessagesWithSource(false)
	if len(msgs) != 2 {
		t.Fatalf("compileMessagesWithSource returned %d messages, want 2", len(msgs))
	}
	first := textOf(t, msgs[0].msg)
	if !strings.HasPrefix(first, `[This message is from agent "Bob the Builder" (Worker#0007), not the human operator.]`) {
		t.Errorf("user step text = %q, want agent sender annotation", first)
	}
	second := textOf(t, msgs[1].msg)
	if !strings.HasPrefix(second, `[This message is from user "Alice" (alice).]`) {
		t.Errorf("user_inject step text = %q, want user sender annotation", second)
	}

	// Normal operator messages (bare "user" meta) must stay unprefixed.
	a2 := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "user"},
			},
			ActiveHead: 0,
		},
		steps: []domain.Step{
			{ID: "s-1", TurnID: "turn-1", Role: "user", Type: "text", Closed: true, Meta: "user",
				Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "operator msg"}}},
		},
	}
	plain := a2.compileMessagesWithSource(false)
	if len(plain) != 1 || textOf(t, plain[0].msg) != "operator msg" {
		t.Errorf("bare user meta must stay unprefixed, got %q", textOf(t, plain[0].msg))
	}
}

func textOf(t *testing.T, msg domain.ChatMessage) string {
	t.Helper()
	for _, b := range msg.Content {
		if b.Type == domain.ContentBlockText {
			return b.Text
		}
	}
	return ""
}
