// Package toast implements the global toast notification actor. It owns the
// active toast card queue and exposes the generic toast API consumed by the
// frontend toast overlay and Go-side emitters such as the workflow gate
// executor.
package toast

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	CallableShow    = "toast.show"
	CallableDismiss = "toast.dismiss"
	CallableState   = "toast.state"
	CallableAction  = "toast.action"

	EventCardAdded      = "toast.card_added"
	EventCardRemoved    = "toast.card_removed"
	EventActionTriggered = "toast.action_triggered"

	kindInfo    = "info"
	kindSuccess = "success"
	kindWarning = "warning"
	kindError   = "error"

	// maxActive bounds the queue. The overlay folds beyond its own
	// display cap ("+N more"); this cap only stops unbounded growth.
	maxActive = 20
)

// Actor is the global toast notification queue. Cards are transient runtime
// state — nothing is persisted here; the overlay visibility preference
// lives in workspace account preferences.
type Actor struct {
	mu    sync.Mutex
	cards []gen.ToastCard
}

// NewActor returns the actor factory for the actorset registry.
func NewActor() func() actor.Actor {
	return func() actor.Actor { return &Actor{} }
}

func (a *Actor) Type() string { return "toast" }

func (a *Actor) OnStart(ctx actor.Context) error {
	if err := ctx.Register(CallableShow, a.handleShow, actor.Public(),
		actor.WithDescription("Queue one toast card and broadcast toast.card_added to subscribed windows (frontend toast overlay).")); err != nil {
		return fmt.Errorf("toast: register show: %w", err)
	}
	if err := ctx.Register(CallableDismiss, a.handleDismiss, actor.Public(),
		actor.WithDescription("Remove one toast card by id and broadcast toast.card_removed.")); err != nil {
		return fmt.Errorf("toast: register dismiss: %w", err)
	}
	if err := ctx.Register(CallableState, a.handleState, actor.Public(),
		actor.WithDescription("Return the active toast card queue (oldest first) so a freshly connected window can hydrate.")); err != nil {
		return fmt.Errorf("toast: register state: %w", err)
	}
	if err := ctx.Register(CallableAction, a.handleAction, actor.Public(),
		actor.WithDescription("Report an action-button click on a live card; re-broadcasts the card's action payload as toast.action_triggered to subscribed windows (the target callable is NOT executed here).")); err != nil {
		return fmt.Errorf("toast: register action: %w", err)
	}
	if err := ctx.RegisterEventKind(EventCardAdded, gen.ToastCard{}, actor.Public()); err != nil {
		return fmt.Errorf("toast: register event card_added: %w", err)
	}
	if err := ctx.RegisterEventKind(EventCardRemoved, gen.ToastCardRemovedEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("toast: register event card_removed: %w", err)
	}
	if err := ctx.RegisterEventKind(EventActionTriggered, gen.ToastActionTriggeredEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("toast: register event action_triggered: %w", err)
	}
	if err := ctx.RegisterDomain("toast").Expose(); err != nil {
		return fmt.Errorf("toast: expose domain: %w", err)
	}
	return nil
}

func (a *Actor) OnStop(_ actor.Context) error {
	return nil
}

func (a *Actor) handleShow(ctx actor.Context, req gen.ToastShowReq) (gen.ToastShowResp, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return gen.ToastShowResp{}, fmt.Errorf("%s: title is required", CallableShow)
	}
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = kindInfo
	}
	switch kind {
	case kindInfo, kindSuccess, kindWarning, kindError:
	default:
		return gen.ToastShowResp{}, fmt.Errorf("%s: unknown kind %q", CallableShow, req.Kind)
	}
	if err := validateActionPayload(req.ActionCallable, req.ActionLabel, req.ActionArgs, req.ActionSchemaID); err != nil {
		return gen.ToastShowResp{}, fmt.Errorf("%s: %w", CallableShow, err)
	}

	card := gen.ToastCard{
		ID:             uuid.NewString(),
		Title:          title,
		Body:           strings.TrimSpace(req.Body),
		Kind:           kind,
		Source:         strings.TrimSpace(req.Source),
		ActionLabel:    strings.TrimSpace(req.ActionLabel),
		ActionCallable: strings.TrimSpace(req.ActionCallable),
		ActionArgs:     req.ActionArgs,
		ActionSchemaID: req.ActionSchemaID,
		DurationMs:     req.DurationMs,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
	}

	a.mu.Lock()
	a.cards = append(a.cards, card)
	var evicted []string
	if overflow := len(a.cards) - maxActive; overflow > 0 {
		evicted = make([]string, 0, overflow)
		for _, c := range a.cards[:overflow] {
			evicted = append(evicted, c.ID)
		}
		a.cards = a.cards[overflow:]
	}
	a.mu.Unlock()

	if err := ctx.EmitEvent(EventCardAdded, card); err != nil {
		ctx.Logger().Error("toast: emit card_added", "err", err)
	}
	for _, id := range evicted {
		if err := ctx.EmitEvent(EventCardRemoved, gen.ToastCardRemovedEvent{ID: id}); err != nil {
			ctx.Logger().Error("toast: emit card_removed (overflow)", "err", err, "id", id)
		}
	}
	return gen.ToastShowResp{ID: card.ID}, nil
}

func (a *Actor) handleDismiss(ctx actor.Context, req gen.ToastDismissReq) (gen.ToastDismissResp, error) {
	if strings.TrimSpace(req.ID) == "" {
		return gen.ToastDismissResp{}, fmt.Errorf("%s: id is required", CallableDismiss)
	}
	a.mu.Lock()
	found := false
	kept := a.cards[:0]
	for _, c := range a.cards {
		if c.ID == req.ID {
			found = true
			continue
		}
		kept = append(kept, c)
	}
	a.cards = kept
	a.mu.Unlock()

	if !found {
		return gen.ToastDismissResp{}, fmt.Errorf("%s: unknown id %q", CallableDismiss, req.ID)
	}
	if err := ctx.EmitEvent(EventCardRemoved, gen.ToastCardRemovedEvent{ID: req.ID}); err != nil {
		ctx.Logger().Error("toast: emit card_removed", "err", err, "id", req.ID)
	}
	return gen.ToastDismissResp{}, nil
}

func (a *Actor) handleState(_ actor.PureContext, _ gen.ToastStateReq) (gen.ToastStateResp, error) {
	a.mu.Lock()
	cards := make([]gen.ToastCard, len(a.cards))
	copy(cards, a.cards)
	a.mu.Unlock()
	return gen.ToastStateResp{Cards: cards}, nil
}

// validateActionPayload enforces the action contract up front so every
// rendered action button is guaranteed to carry a dispatchable target:
//   - ActionCallable is the anchor: when present it must be a dotted callable
//     ID ("workflow.gate.approve") and ActionLabel must name the button.
//   - ActionArgs / ActionSchemaID only make sense alongside a callable.
func validateActionPayload(callable, label string, args map[string]any, schemaID uint64) error {
	callable = strings.TrimSpace(callable)
	label = strings.TrimSpace(label)
	hasExtras := label != "" || len(args) > 0 || schemaID != 0
	if callable == "" {
		if hasExtras {
			return fmt.Errorf("action_label/action_args/action_schema_id require action_callable")
		}
		return nil
	}
	if !strings.Contains(callable, ".") {
		return fmt.Errorf("action_callable %q must be a dotted callable ID (e.g. \"workflow.gate.approve\")", callable)
	}
	if label == "" {
		return fmt.Errorf("action_callable %q requires action_label (button text)", callable)
	}
	return nil
}

// handleAction relays a toast-overlay action-button click. It deliberately
// does NOT execute the card's ActionCallable: the click is re-broadcast as
// toast.action_triggered (callable ID + args) so the main window owns what
// happens next. The card stays in the queue; dismissal is a separate call.
func (a *Actor) handleAction(ctx actor.Context, req gen.ToastActionReq) (gen.ToastActionResp, error) {
	if strings.TrimSpace(req.ID) == "" {
		return gen.ToastActionResp{}, fmt.Errorf("%s: id is required", CallableAction)
	}
	a.mu.Lock()
	var ev gen.ToastActionTriggeredEvent
	found, hasAction := false, false
	for i := range a.cards {
		if a.cards[i].ID == req.ID {
			c := a.cards[i] // copy: the slice is compacted in place by dismiss
			found = true
			hasAction = c.ActionCallable != ""
			ev = gen.ToastActionTriggeredEvent{
				ID:             c.ID,
				ActionCallable: c.ActionCallable,
				ActionLabel:    c.ActionLabel,
				ActionArgs:     c.ActionArgs,
				ActionSchemaID: c.ActionSchemaID,
			}
			break
		}
	}
	a.mu.Unlock()

	if !found {
		return gen.ToastActionResp{}, fmt.Errorf("%s: unknown id %q", CallableAction, req.ID)
	}
	if !hasAction {
		return gen.ToastActionResp{}, fmt.Errorf("%s: card %q carries no action", CallableAction, req.ID)
	}
	if err := ctx.EmitEvent(EventActionTriggered, ev); err != nil {
		ctx.Logger().Error("toast: emit action_triggered", "err", err, "id", req.ID)
	}
	return gen.ToastActionResp{}, nil
}
