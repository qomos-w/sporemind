package toast

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

type stubLogger struct{}

func (stubLogger) Debug(string, ...any) {}
func (stubLogger) Info(string, ...any)  {}
func (stubLogger) Warn(string, ...any)  {}
func (stubLogger) Error(string, ...any) {}

// stubContext records emitted events so handler tests can assert on the
// toast.card_added / toast.card_removed fan-out.
type stubContext struct {
	actor.Context
	events []struct {
		kind    string
		payload any
	}
}

func (c *stubContext) Logger() actor.Logger { return stubLogger{} }

func (c *stubContext) EmitEvent(kind string, payload any) error {
	c.events = append(c.events, struct {
		kind    string
		payload any
	}{kind, payload})
	return nil
}

func TestShowEmitsCardAdded(t *testing.T) {
	a := &Actor{}
	ctx := &stubContext{}

	resp, err := a.handleShow(ctx, gen.ToastShowReq{Title: "hello", Kind: "success", DurationMs: 1000})
	if err != nil {
		t.Fatalf("handleShow: %v", err)
	}
	if resp.ID == "" {
		t.Fatal("expected non-empty card id")
	}

	st, err := a.handleState(nil, gen.ToastStateReq{})
	if err != nil {
		t.Fatalf("handleState: %v", err)
	}
	if len(st.Cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(st.Cards))
	}
	card := st.Cards[0]
	if card.Title != "hello" || card.Kind != "success" || card.DurationMs != 1000 || card.CreatedAt == "" {
		t.Fatalf("unexpected card: %+v", card)
	}

	if len(ctx.events) != 1 || ctx.events[0].kind != EventCardAdded {
		t.Fatalf("expected one %s event, got %+v", EventCardAdded, ctx.events)
	}
	if ev := ctx.events[0].payload.(gen.ToastCard); ev.ID != resp.ID {
		t.Fatalf("event id %q != resp id %q", ev.ID, resp.ID)
	}
}

func TestShowValidation(t *testing.T) {
	a := &Actor{}
	if _, err := a.handleShow(&stubContext{}, gen.ToastShowReq{}); err == nil {
		t.Fatal("expected error for empty title")
	}
	if _, err := a.handleShow(&stubContext{}, gen.ToastShowReq{Title: "x", Kind: "sparkly"}); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestShowDefaults(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleShow(&stubContext{}, gen.ToastShowReq{Title: "  trimmed  "})
	if err != nil {
		t.Fatalf("handleShow: %v", err)
	}
	st, _ := a.handleState(nil, gen.ToastStateReq{})
	if st.Cards[0].Kind != kindInfo {
		t.Fatalf("expected default kind %q, got %q", kindInfo, st.Cards[0].Kind)
	}
	if st.Cards[0].Title != "trimmed" {
		t.Fatalf("expected trimmed title, got %q", st.Cards[0].Title)
	}
	if st.Cards[0].ID != resp.ID {
		t.Fatal("state card id mismatch")
	}
}

func TestDismissRemovesAndEmits(t *testing.T) {
	a := &Actor{}
	ctx := &stubContext{}
	resp, _ := a.handleShow(ctx, gen.ToastShowReq{Title: "bye"})

	if _, err := a.handleDismiss(ctx, gen.ToastDismissReq{ID: "missing"}); err == nil {
		t.Fatal("expected error for unknown id")
	}

	dctx := &stubContext{}
	if _, err := a.handleDismiss(dctx, gen.ToastDismissReq{ID: resp.ID}); err != nil {
		t.Fatalf("handleDismiss: %v", err)
	}
	if len(dctx.events) != 1 || dctx.events[0].kind != EventCardRemoved {
		t.Fatalf("expected one %s event, got %+v", EventCardRemoved, dctx.events)
	}
	if ev := dctx.events[0].payload.(gen.ToastCardRemovedEvent); ev.ID != resp.ID {
		t.Fatalf("event id %q != %q", ev.ID, resp.ID)
	}

	st, _ := a.handleState(nil, gen.ToastStateReq{})
	if len(st.Cards) != 0 {
		t.Fatalf("expected empty queue after dismiss, got %d", len(st.Cards))
	}
}

func TestOverflowEvictsOldest(t *testing.T) {
	a := &Actor{cards: make([]gen.ToastCard, 0, maxActive)}
	for i := 0; i < maxActive; i++ {
		a.cards = append(a.cards, gen.ToastCard{ID: string(rune('a' + i)), Title: "x"})
	}
	ctx := &stubContext{}
	if _, err := a.handleShow(ctx, gen.ToastShowReq{Title: "new"}); err != nil {
		t.Fatalf("handleShow: %v", err)
	}

	st, _ := a.handleState(nil, gen.ToastStateReq{})
	if len(st.Cards) != maxActive {
		t.Fatalf("expected queue capped at %d, got %d", maxActive, len(st.Cards))
	}
	if st.Cards[0].ID == "a" {
		t.Fatal("expected oldest card evicted")
	}
	if !strings.Contains(st.Cards[len(st.Cards)-1].Title, "new") {
		t.Fatalf("expected newest card present, got %+v", st.Cards[len(st.Cards)-1])
	}

	removed := 0
	for _, ev := range ctx.events {
		if ev.kind == EventCardRemoved {
			removed++
		}
	}
	if removed != 1 {
		t.Fatalf("expected 1 overflow card_removed event, got %d", removed)
	}
}

func TestStateReturnsCopy(t *testing.T) {
	a := &Actor{}
	_, _ = a.handleShow(&stubContext{}, gen.ToastShowReq{Title: "orig"})

	st, _ := a.handleState(nil, gen.ToastStateReq{})
	st.Cards[0].Title = "mutated"

	st2, _ := a.handleState(nil, gen.ToastStateReq{})
	if st2.Cards[0].Title != "orig" {
		t.Fatalf("handleState must return a copy, got %q", st2.Cards[0].Title)
	}
}

func TestShowActionValidation(t *testing.T) {
	a := &Actor{}
	cases := []struct {
		name string
		req  gen.ToastShowReq
	}{
		{"label without callable", gen.ToastShowReq{Title: "x", ActionLabel: "go"}},
		{"args without callable", gen.ToastShowReq{Title: "x", ActionArgs: map[string]any{"k": "v"}}},
		{"schema id without callable", gen.ToastShowReq{Title: "x", ActionSchemaID: 6216}},
		{"callable without label", gen.ToastShowReq{Title: "x", ActionCallable: "workflow.gate.approve"}},
		{"non-dotted callable", gen.ToastShowReq{Title: "x", ActionCallable: "bare", ActionLabel: "go"}},
	}
	for _, tc := range cases {
		if _, err := a.handleShow(&stubContext{}, tc.req); err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestShowPassesThroughActionPayload(t *testing.T) {
	a := &Actor{}
	ctx := &stubContext{}
	args := map[string]any{"gateId": "g-1", "decision": "approve"}
	resp, err := a.handleShow(ctx, gen.ToastShowReq{
		Title:          "gate pending",
		ActionLabel:    "打开输入",
		ActionCallable: "workflow.gate.approve",
		ActionArgs:     args,
		ActionSchemaID: 4453,
	})
	if err != nil {
		t.Fatalf("handleShow: %v", err)
	}

	st, _ := a.handleState(nil, gen.ToastStateReq{})
	card := st.Cards[0]
	if card.ID != resp.ID || card.ActionLabel != "打开输入" || card.ActionCallable != "workflow.gate.approve" || card.ActionSchemaID != 4453 {
		t.Fatalf("unexpected action payload on card: %+v", card)
	}
	if card.ActionArgs["gateId"] != "g-1" || card.ActionArgs["decision"] != "approve" {
		t.Fatalf("unexpected action args: %+v", card.ActionArgs)
	}
	if ev := ctx.events[0].payload.(gen.ToastCard); ev.ActionCallable != "workflow.gate.approve" || ev.ActionArgs["gateId"] != "g-1" {
		t.Fatalf("card_added event must carry the action payload: %+v", ev)
	}
}

func TestActionRelaysTriggeredEvent(t *testing.T) {
	a := &Actor{}
	args := map[string]any{"gateId": "g-7"}
	resp, err := a.handleShow(&stubContext{}, gen.ToastShowReq{
		Title:          "approval needed",
		ActionLabel:    "review",
		ActionCallable: "workflow.gate.approve",
		ActionArgs:     args,
		ActionSchemaID: 4453,
	})
	if err != nil {
		t.Fatalf("handleShow: %v", err)
	}

	ctx := &stubContext{}
	if _, err := a.handleAction(ctx, gen.ToastActionReq{ID: resp.ID}); err != nil {
		t.Fatalf("handleAction: %v", err)
	}
	if len(ctx.events) != 1 || ctx.events[0].kind != EventActionTriggered {
		t.Fatalf("expected one %s event, got %+v", EventActionTriggered, ctx.events)
	}
	ev := ctx.events[0].payload.(gen.ToastActionTriggeredEvent)
	if ev.ID != resp.ID || ev.ActionCallable != "workflow.gate.approve" || ev.ActionLabel != "review" || ev.ActionSchemaID != 4453 || ev.ActionArgs["gateId"] != "g-7" {
		t.Fatalf("unexpected action_triggered payload: %+v", ev)
	}

	// The card stays in the queue: dismissal is a separate decision owned by
	// the main window.
	st, _ := a.handleState(nil, gen.ToastStateReq{})
	if len(st.Cards) != 1 {
		t.Fatalf("action must not dismiss the card, queue=%d", len(st.Cards))
	}
}

func TestActionValidation(t *testing.T) {
	a := &Actor{}
	if _, err := a.handleAction(&stubContext{}, gen.ToastActionReq{}); err == nil {
		t.Fatal("expected error for empty id")
	}
	if _, err := a.handleAction(&stubContext{}, gen.ToastActionReq{ID: "missing"}); err == nil {
		t.Fatal("expected error for unknown id")
	}

	// A card without an action payload must not be triggerable.
	resp, _ := a.handleShow(&stubContext{}, gen.ToastShowReq{Title: "plain"})
	if _, err := a.handleAction(&stubContext{}, gen.ToastActionReq{ID: resp.ID}); err == nil {
		t.Fatal("expected error for action-less card")
	}
}
