// Package workbench implements the workbench attention-scoring actor.
//
// The actor is the single owner of per-card attention scores and the derived
// layout slots the workbench frontend renders. It ingests three score sources:
//
//   - app lifecycle events (app_lifecycle) → app cards appear / retire
//   - agent activity (step / turn events) → the emitting agent's card rises,
//     and terminal keywords summon the terminal card
//   - user click promotion (workbench.promote) → the clicked card is promoted
//     to max score + HYSTERESIS + 1
//
// Ordering mirrors web/src/ui/workbench/blackboard-reference.html: HYSTERESIS = 15,
// pinned cards forced to the head (main slot), attention floor 5 / ceiling 40.
//
// Concurrency contract (CLAUDE.md 高频 callable 注册模式 + Owner Lane 禁阻塞):
// every read (workbench.snapshot) is a PureContext handler; every write runs on
// the dedicated workbench_score stateful lane, including the ingress path — the
// bus-event subscriptions and the drift ticker self-invoke lane-routed internal
// callables (fire-and-forget) rather than touching state inline. Routing all
// mutation through the single lane also means the projection is re-published
// promptly, since gospore fingerprints the component field after each invoke.
package workbench

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// Compile-time assertion that Actor implements persist.Persistent, required by
// the actorset's RequirePersistent flag so the runtime preserves the actor's
// canonical ID across restarts.
var _ persist.Persistent = (*Actor)(nil)

const (
	// Hysteresis is the re-ranking threshold: a challenger must lead the
	// incumbent by more than this to swap. It must stay equal to HYSTERESIS in
	// web/src/ui/workbench/blackboard-reference.html.
	Hysteresis = 15.0
	// ScoreFloor and ScoreCap bound the attention scale, matching the
	// blackboard reference (floor 5, ceiling 40).
	ScoreFloor = 5.0
	ScoreCap   = 40.0
	// DefaultScore seeds a newly declared card.
	DefaultScore = 10.0

	// laneScore is the dedicated stateful lane carrying every score mutation.
	laneScore = "workbench_score"

	domainName = "workbench"

	// Public callables.
	callSnapshot        = "workbench.snapshot"
	callPromote         = "workbench.promote"
	callSetHidden       = "workbench.set_hidden"
	callSetPinned       = "workbench.set_pinned"
	callUpsertCard      = "workbench.upsert_card"
	callAttentionReport = "workbench.attention_report"
	callSetFrozen       = "workbench.set_frozen"
	callSetMaximized    = "workbench.set_maximized"

	// Internal, lane-routed ingress callables.
	callIngestLifecycle = "workbench.ingest_lifecycle"
	callIngestStep      = "workbench.ingest_step"
	callIngestTurn      = "workbench.ingest_turn"
	callIngestAgentList = "workbench.ingest_agent_list"
	callDriftTick       = "workbench.drift_tick"

	// terminalCardID must match web/src/ui/workbench/terminal/terminalCard.tsx
	// (TERMINAL_CARD_ID): the frontend resolves a projected terminal card to its
	// descriptor by this id, so the two sides share one card-id vocabulary.
	terminalCardID  = "terminal"
	appCardPrefix   = "app:"
	agentCardPrefix = "agent:"

	// Score deltas.
	summonBoost = 12.0

	// Attention modes for agent cards, derived from the workspace agent list
	// (workbench.ingest_agent_list). Running only establishes presence at the
	// mode's base score — agent chatter never climbs a card above its base, so
	// the board moves only on user / coordinator action (promote, pin).
	modeCoordinator = "coordinator"
	modeWorkflow    = "workflow"
	modeWorker      = "worker"
	modeNormal      = "normal"

	// Presence base per mode: the global Coordinator conversation is the
	// default stage (highest); a normal chat agent sits at DefaultScore; a
	// workflow owner and its workers rank below — they are machinery, not
	// conversations.
	baseCoordinator = 12.0
	baseWorkflow    = 8.0
	baseWorker      = 6.0
	// promoteDecay is withdrawn from the promoted card's visible peers:
	// attention is a shared scale, so granting it to one card must reclaim it
	// from the others. Without decay every card accumulates at ScoreCap and
	// the score stops being a relative signal. Half the promote gap; uniform
	// linear decay preserves the peers' relative order. Mirrors the intent of
	// drift() in blackboard-reference.html (deterministic reallocation
	// replacing random drift). Pinned peers are exempt (reference: !locked).
	promoteDecay = 8.0

	// Periodic board housekeeping: the terminal card retreats after its idle
	// window. The synthetic attention drift from the blackboard reference is
	// intentionally not ported — a random score walker reshuffles the layout
	// forever, and real boards should move on real events only.
	driftInterval = 6 * time.Second

	// terminalRetreatAfter retires a summoned terminal card back to hidden once
	// no new output has arrived for this long (spec §5 自动退避).
	terminalRetreatAfter = 30 * time.Second
)

// Actor owns the workbench attention state.
type Actor struct {
	actor.Host

	// Snapshot is the attention/layout projection. The cell fingerprints and
	// publishes it after every invoke while the projection store has
	// subscribers, so the frontend's watchSnapshot() stream tracks layout.
	Snapshot gen.WorkbenchSnapshot `gospore:"component,public"`

	mu         sync.RWMutex
	actorID    string
	store      persist.Persist
	cards      map[string]*cardState
	order      []string
	generation int64
	nextSeq    int64
	// Layout MODE state the actor owns so the coordinator controller can read
	// and switch it: frozen is the ❄ layout freeze (persisted); maximizedID is
	// the full-capture card (ephemeral — never persisted, restart releases it).
	frozen      bool
	maximizedID string
	// recent is the bounded global-awareness ring behind
	// workbench.attention_report (in-memory only, never persisted).
	recent []gen.WorkbenchAttentionEvent
	// agentModes caches the latest workspace agent-list projection per actor
	// id (attention mode + display name). Rebuilt from agent_list_state events;
	// never persisted — the next list event restores it.
	agentModes map[string]gen.WorkbenchAgentModeInfo

	self       ref.Ref
	lifecycle  context.Context
	cancelSubs []func()
	driftDone  chan struct{}
}

// NewActor returns an actor constructor for the global workbench actor.
func NewActor() func() actor.Actor {
	return func() actor.Actor { return &Actor{} }
}

// Type returns the actor type identifier.
func (a *Actor) Type() string { return domainName }

// OnInit builds the persist store and restores the last persisted layout.
func (a *Actor) OnInit(ctx actor.Context) error {
	a.actorID = ctx.Self().ID().String()
	a.self = ctx.Self()
	a.lifecycle = ctx.Lifecycle()
	a.cards = make(map[string]*cardState)

	store, err := persist.New(config.PersistConfig(domainName))
	if err != nil {
		return fmt.Errorf("workbench: persist init: %w", err)
	}
	a.store = store
	if err := a.Load(); err != nil {
		ctx.Logger().Error("workbench: load failed", "err", err)
	}

	a.mu.Lock()
	a.seedLocked()
	a.refreshLocked()
	a.mu.Unlock()
	return nil
}

// OnStart registers the score lane, the read/write callables and the event
// ingress, then arms the attention drift ticker.
func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("workbench: starting", "id", a.actorID)

	// Dedicated lane first: Register validates that every WithLoop target is
	// already declared. All score mutation (public callables + ingress) routes
	// here, so the owner lane is never held by workbench state changes.
	if err := ctx.RegisterLoop(laneScore, actor.ModeStateful); err != nil {
		return fmt.Errorf("workbench: register loop %s: %w", laneScore, err)
	}

	// Ingress: bus events are forwarded to these lane-routed internal callables
	// (fire-and-forget), keeping mutation serialized and off the owner lane.
	if err := ctx.Register(callIngestLifecycle, a.handleIngestLifecycle, actor.Internal(), actor.WithLoop(laneScore)); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callIngestLifecycle, err)
	}
	if err := ctx.Register(callIngestStep, a.handleIngestStep, actor.Internal(), actor.WithLoop(laneScore)); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callIngestStep, err)
	}
	if err := ctx.Register(callIngestTurn, a.handleIngestTurn, actor.Internal(), actor.WithLoop(laneScore)); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callIngestTurn, err)
	}
	if err := ctx.Register(callIngestAgentList, a.handleIngestAgentList, actor.Internal(), actor.WithLoop(laneScore)); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callIngestAgentList, err)
	}
	if err := ctx.Register(callDriftTick, a.handleDriftTick, actor.Internal(), actor.WithLoop(laneScore)); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callDriftTick, err)
	}

	// Read: PureContext (forked goroutine, never the owner lane).
	if err := ctx.Register(callSnapshot, a.handleSnapshot, actor.Public(),
		actor.WithDescription("Return the current workbench attention/layout snapshot: every card with its id, kind, attention score and assigned slot (main/side/term/rail/hidden). Set IncludeHidden to keep retired cards.")); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callSnapshot, err)
	}

	// Writes: dedicated lane. Card operations are reversible (scores decay,
	// pins unpin, hidden cards summon back).
	if err := ctx.Register(callPromote, a.handlePromote, actor.Public(), actor.WithLoop(laneScore),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Promote a workbench card to the top of the attention ordering (user click / explicit promotion). Score becomes max + HYSTERESIS + 1.")); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callPromote, err)
	}
	if err := ctx.Register(callSetHidden, a.handleSetHidden, actor.Public(), actor.WithLoop(laneScore),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Retire or restore a workbench card (terminal summon / retreat). Restoring bumps the card's attention score.")); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callSetHidden, err)
	}
	if err := ctx.Register(callSetPinned, a.handleSetPinned, actor.Public(), actor.WithLoop(laneScore),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Pin or unpin a workbench card. A pinned card is forced to the head of the ordering (main slot).")); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callSetPinned, err)
	}
	if err := ctx.Register(callUpsertCard, a.handleUpsertCard, actor.Public(), actor.WithLoop(laneScore),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Declare or update a workbench card's static metadata (title, kind, icon, color, status text, visual, hidden). The actor owns score and slot.")); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callUpsertCard, err)
	}
	// Layout-mode writes: freeze and full capture are MODE, not scores — the
	// controller (and the ❄ / maximize UI) switches them through here.
	if err := ctx.Register(callSetFrozen, a.handleSetFrozen, actor.Public(), actor.WithLoop(laneScore),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Freeze or unfreeze the workbench layout (the ❄ mode). While frozen, score/hidden/descriptor churn no longer reflows the board — placement is held until unfrozen.")); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callSetFrozen, err)
	}
	if err := ctx.Register(callSetMaximized, a.handleSetMaximized, actor.Public(), actor.WithLoop(laneScore),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Enter or release a full capture: with a card id the card takes the whole field until released (empty id releases). Use when the user asks to focus one thing completely.")); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callSetMaximized, err)
	}

	// Global awareness read for the workbench controller bundle
	// (builtin:bundle:workbench-attention, coordinator kind): full board plus
	// the recent activity ring collected from every agent / app / user op.
	if err := ctx.Register(callAttentionReport, a.handleAttentionReport, actor.Public(),
		actor.WithDescription("Global awareness report: the full workbench board (every card, attention score, slot and why — hidden cards included) plus the most recent system-wide activity events (agent steps/turns, app lifecycle, user board operations). Use this to understand where the user's attention currently is before promoting/pinning/hiding cards.")); err != nil {
		return fmt.Errorf("workbench: register %s: %w", callAttentionReport, err)
	}

	if err := ctx.RegisterDomain(domainName).Expose(); err != nil {
		return fmt.Errorf("workbench: expose service: %w", err)
	}

	if err := a.subscribeEvents(ctx); err != nil {
		return err
	}
	a.startDriftLoop(ctx)
	return nil
}

// OnStop unsubscribes, stops the ticker, and persists the final layout.
func (a *Actor) OnStop(_ actor.Context) error {
	for _, cancel := range a.cancelSubs {
		cancel()
	}
	a.cancelSubs = nil
	a.stopDriftLoop()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked()
}

// ---------------------------------------------------------------------------
// Persistence — implements persist.Persistent
// ---------------------------------------------------------------------------

type persistedState struct {
	Cards      []persistedCard `json:"cards"`
	Order      []string        `json:"order"`
	Generation int64           `json:"generation"`
	NextSeq    int64           `json:"nextSeq"`
	// Frozen is persisted so the ❄ layout freeze survives restarts; the
	// full-capture maximized card is ephemeral and always starts released.
	Frozen bool `json:"frozen"`
}

type persistedCard struct {
	ID         string               `json:"id"`
	Kind       string               `json:"kind"`
	Title      string               `json:"title"`
	Icon       string               `json:"icon"`
	Color      string               `json:"color"`
	Status     string               `json:"statusText"`
	Why        string               `json:"why"`
	Visual     *gen.ComponentVisual `json:"visual,omitempty"`
	Score      float64              `json:"score"`
	Pinned     bool                 `json:"pinned"`
	Hidden     bool                 `json:"hidden"`
	Seq        int64                `json:"seq"`
	LastActive int64                `json:"lastActive,omitempty"`
	Mode       string               `json:"mode,omitempty"`
}

// Save persists the layout. Implements persist.Persistent.
func (a *Actor) Save() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked()
}

func (a *Actor) saveLocked() error {
	if a.store == nil {
		return nil
	}
	return a.store.Save(a.actorID, a.persistedLocked())
}

// Load restores the layout. Implements persist.Persistent.
func (a *Actor) Load() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store == nil {
		return nil
	}
	var st persistedState
	if err := persist.LoadOrZero(a.store, a.actorID, &st); err != nil {
		return err
	}
	a.applyPersistedLocked(st)
	return nil
}

func (a *Actor) persistedLocked() persistedState {
	st := persistedState{
		Order:      append([]string(nil), a.order...),
		Generation: a.generation,
		NextSeq:    a.nextSeq,
		Frozen:     a.frozen,
	}
	for _, id := range a.order {
		c := a.cards[id]
		if c == nil {
			continue
		}
		st.Cards = append(st.Cards, persistedCard{
			ID: c.id, Kind: c.kind, Title: c.title, Icon: c.icon, Color: c.color,
			Status: c.status, Why: c.why, Visual: c.visual, Score: c.score,
			Pinned: c.pinned, Hidden: c.hidden, Seq: c.seq, LastActive: c.lastActive,
			Mode: c.mode,
		})
	}
	return st
}

func (a *Actor) applyPersistedLocked(st persistedState) {
	if a.cards == nil {
		a.cards = make(map[string]*cardState)
	}
	a.cards = make(map[string]*cardState, len(st.Cards))
	for _, pc := range st.Cards {
		if pc.ID == "" {
			continue
		}
		a.cards[pc.ID] = &cardState{
			id: pc.ID, kind: pc.Kind, title: pc.Title, icon: pc.Icon, color: pc.Color,
			status: pc.Status, why: pc.Why, visual: pc.Visual, score: pc.Score,
			pinned: pc.Pinned, hidden: pc.Hidden, seq: pc.Seq, lastActive: pc.LastActive,
			mode: pc.Mode,
		}
	}
	a.order = append([]string(nil), st.Order...)
	a.generation = st.Generation
	a.nextSeq = st.NextSeq
	a.frozen = st.Frozen
	a.maximizedID = ""
}

// ---------------------------------------------------------------------------
// State helpers
// ---------------------------------------------------------------------------

func (a *Actor) newCardLocked(id, kind string) *cardState {
	a.nextSeq++
	c := &cardState{id: id, kind: kind, title: id, score: DefaultScore, seq: a.nextSeq}
	a.cards[id] = c
	return c
}

// seedLocked ensures the always-present terminal card exists (retired by
// default until summoned by a keyword match).
func (a *Actor) seedLocked() {
	if _, ok := a.cards[terminalCardID]; ok {
		return
	}
	c := a.newCardLocked(terminalCardID, kindTerminal)
	c.title = "终端"
	c.icon = "terminal"
	c.hidden = true
	c.why = "初始"
}

// refreshLocked recomputes the derived order + wire projection. Generation is
// bumped only when the projection actually changed, so an idle tick does not
// publish a spurious snapshot.
func (a *Actor) refreshLocked() {
	a.order = orderedIDs(a.cards, a.order)
	next := buildCards(a.cards, a.order)
	if reflect.DeepEqual(next, a.Snapshot.Cards) && a.Snapshot.Frozen == a.frozen && a.Snapshot.Maximized == a.maximizedID {
		return
	}
	a.generation++
	a.Snapshot = gen.WorkbenchSnapshot{Cards: next, Generation: a.generation, Frozen: a.frozen, Maximized: a.maximizedID}
}

func (a *Actor) snapshotLocked() gen.WorkbenchSnapshot {
	return gen.WorkbenchSnapshot{
		Cards:      append([]gen.WorkbenchCardState(nil), a.Snapshot.Cards...),
		Generation: a.Snapshot.Generation,
		Frozen:     a.Snapshot.Frozen,
		Maximized:  a.Snapshot.Maximized,
	}
}

func (a *Actor) maxScoreLocked() float64 {
	top := 0.0
	for _, c := range a.cards {
		if c.score > top {
			top = c.score
		}
	}
	return top
}

// dispatch forwards a payload to a lane-routed internal callable as a
// fire-and-forget self-invoke. Used by the bus-event subscriptions and the
// drift ticker so no state is mutated outside the score lane.
func (a *Actor) dispatch(callID string, payload any) {
	if a.self == nil || a.lifecycle == nil {
		return
	}
	if call := a.self.Invoke(a.lifecycle, callID, payload); call != nil {
		_ = call.Close()
	}
}

// ---------------------------------------------------------------------------
// Callables
// ---------------------------------------------------------------------------

// handleSnapshot is the stateless read surface.
func (a *Actor) handleSnapshot(_ actor.PureContext, req gen.WorkbenchSnapshotReq) (gen.WorkbenchSnapshot, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	snap := gen.WorkbenchSnapshot{
		Generation: a.Snapshot.Generation,
		Frozen:     a.Snapshot.Frozen,
		Maximized:  a.Snapshot.Maximized,
		Cards:      []gen.WorkbenchCardState{},
	}
	if req.IncludeHidden {
		snap.Cards = append(snap.Cards, a.Snapshot.Cards...)
		return snap, nil
	}
	for _, c := range a.Snapshot.Cards {
		if c.Slot == slotHidden {
			continue
		}
		snap.Cards = append(snap.Cards, c)
	}
	return snap, nil
}

func (a *Actor) handlePromote(_ actor.Context, req gen.WorkbenchCardRefReq) (gen.WorkbenchSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.cards[req.ID]
	if c == nil {
		// The board rendered a card the actor has not been told about (frontend
		// descriptor not yet declared). Create it on demand — a promote click
		// must never be a silent no-op; the follow-up upsert fills metadata.
		c = a.newCardLocked(req.ID, kindApp)
		c.title = req.ID
	}
	// Attention reallocation: decay the promoted card's visible peers first
	// (attention is shared), then compute the promote target from the decayed
	// board — the target stays Hysteresis+1 above the new top and the cap is
	// under less pressure. Pinned/hidden/terminal peers are exempt.
	now := time.Now().UnixNano()
	for _, cc := range a.cards {
		if cc == c || cc.hidden || cc.kind == kindTerminal || cc.pinned {
			continue
		}
		cc.score = capScore(cc.score - promoteDecay)
	}
	// Score squeeze: when the promote target would exceed ScoreCap, shift all
	// visible non-terminal peers down by the overflow before clamping. This
	// preserves relative gaps and guarantees the promoted card is strictly
	// highest — no cap-collision tie that would leave the incumbent in main.
	top := a.maxScoreLocked()
	desired := promoteScore(top)
	if desired > ScoreCap {
		shift := desired - ScoreCap
		for _, cc := range a.cards {
			if cc.hidden || cc.kind == kindTerminal {
				continue
			}
			cc.score = capScore(cc.score - shift)
		}
		desired = ScoreCap
	}
	c.score = desired
	c.hidden = false
	c.lastActive = now
	c.why = "你点击提权"
	a.appendRecentLocked("", attentionKindUser, "promote "+req.ID)
	a.refreshLocked()
	if err := a.saveLocked(); err != nil {
		return gen.WorkbenchSnapshot{}, fmt.Errorf("workbench.promote: save: %w", err)
	}
	return a.snapshotLocked(), nil
}

func (a *Actor) handleSetHidden(_ actor.Context, req gen.WorkbenchSetHiddenReq) (gen.WorkbenchSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c := a.cards[req.ID]; c != nil {
		c.hidden = req.Hidden
		if req.Hidden {
			c.why = "已退避"
			a.appendRecentLocked("", attentionKindUser, "hide "+req.ID)
		} else {
			c.score = capScore(c.score + summonBoost)
			c.lastActive = time.Now().UnixNano()
			c.why = "召唤回归"
			a.appendRecentLocked("", attentionKindUser, "summon "+req.ID)
		}
		a.refreshLocked()
		if err := a.saveLocked(); err != nil {
			return gen.WorkbenchSnapshot{}, fmt.Errorf("workbench.set_hidden: save: %w", err)
		}
	}
	return a.snapshotLocked(), nil
}

func (a *Actor) handleSetPinned(_ actor.Context, req gen.WorkbenchSetPinnedReq) (gen.WorkbenchSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.cards[req.ID]
	if c == nil {
		// Unknown id: create on demand (see handlePromote) — pinning a rendered
		// card must not be a silent no-op.
		c = a.newCardLocked(req.ID, kindApp)
		c.title = req.ID
	}
	c.pinned = req.Pinned
	// Pin toggling is a user interaction: refresh lastActive so the
	// equal-score tie-break never drops a card the user is actively clicking
	// below an agent-fed peer (repeated main-card clicks would otherwise
	// shuffle the board on every toggle).
	c.lastActive = time.Now().UnixNano()
	if req.Pinned {
		c.why = "已钉住"
		a.appendRecentLocked("", attentionKindUser, "pin "+req.ID)
	} else {
		c.why = "已解除钉住"
		a.appendRecentLocked("", attentionKindUser, "unpin "+req.ID)
	}
	a.refreshLocked()
	if err := a.saveLocked(); err != nil {
		return gen.WorkbenchSnapshot{}, fmt.Errorf("workbench.set_pinned: save: %w", err)
	}
	return a.snapshotLocked(), nil
}

// handleSetFrozen toggles the ❄ layout freeze (persisted). While frozen the
// projection keeps publishing score/slot churn, but the frontend holds its
// placement until unfrozen.
func (a *Actor) handleSetFrozen(_ actor.Context, req gen.WorkbenchSetFrozenReq) (gen.WorkbenchSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.frozen != req.Frozen {
		a.frozen = req.Frozen
		if req.Frozen {
			a.appendRecentLocked("", attentionKindUser, "freeze layout")
		} else {
			a.appendRecentLocked("", attentionKindUser, "unfreeze layout")
		}
		a.refreshLocked()
		if err := a.saveLocked(); err != nil {
			return gen.WorkbenchSnapshot{}, fmt.Errorf("workbench.set_frozen: save: %w", err)
		}
	}
	return a.snapshotLocked(), nil
}

// handleSetMaximized enters (or releases, empty id) a full capture. Ephemeral
// by design: never persisted, so a restart always comes back to the board.
func (a *Actor) handleSetMaximized(_ actor.Context, req gen.WorkbenchSetMaximizedReq) (gen.WorkbenchSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if req.ID != "" && a.cards[req.ID] == nil {
		return gen.WorkbenchSnapshot{}, fmt.Errorf("workbench.set_maximized: unknown card %q", req.ID)
	}
	if a.maximizedID != req.ID {
		a.maximizedID = req.ID
		if req.ID == "" {
			a.appendRecentLocked("", attentionKindUser, "release capture")
		} else if req.Reason != "" {
			a.appendRecentLocked("", attentionKindUser, "capture "+req.ID+" — "+req.Reason)
		} else {
			a.appendRecentLocked("", attentionKindUser, "capture "+req.ID)
		}
		a.refreshLocked()
	}
	return a.snapshotLocked(), nil
}

func (a *Actor) handleUpsertCard(_ actor.Context, req gen.WorkbenchUpsertCardReq) (gen.WorkbenchSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if req.ID == "" {
		return a.snapshotLocked(), nil
	}
	c := a.cards[req.ID]
	if c == nil {
		kind := req.Kind
		if kind == "" {
			kind = kindApp
		}
		c = a.newCardLocked(req.ID, kind)
	}
	if req.Kind != "" {
		c.kind = req.Kind
	}
	if req.Title != "" {
		c.title = req.Title
	}
	if req.Icon != "" {
		c.icon = req.Icon
	}
	if req.Color != "" {
		c.color = req.Color
	}
	if req.StatusText != "" {
		c.status = req.StatusText
	}
	if req.Visual != nil {
		c.visual = req.Visual
	}
	if req.Mode != "" {
		applyModeLocked(c, req.Mode)
	}
	if req.Score > 0 {
		c.score = capScore(req.Score)
		c.lastActive = time.Now().UnixNano()
	}
	if req.Hidden {
		c.hidden = true
	}
	a.refreshLocked()
	if err := a.saveLocked(); err != nil {
		return gen.WorkbenchSnapshot{}, fmt.Errorf("workbench.upsert_card: save: %w", err)
	}
	return a.snapshotLocked(), nil
}

// handleDriftTick retires a stale terminal card on the periodic tick. The
// synthetic score drift from the blackboard reference (one card up / one down
// every few seconds) is deliberately NOT ported: real boards move on real
// events (agent activity, app lifecycle, user clicks), and a random walker
// made the layout reshuffle forever.
func (a *Actor) handleDriftTick(_ actor.Context, _ gen.WorkbenchSnapshotReq) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now().UnixNano()

	if t := a.cards[terminalCardID]; t != nil && !t.hidden {
		if terminalShouldRetreat(now, t.lastActive) {
			t.hidden = true
			t.why = "终端退避（无新输出）"
			a.refreshLocked()
		}
	}
	return nil
}

// startDriftLoop arms the periodic housekeeping ticker (terminal retreat).
// The tick lands on the score lane via the lane-routed callDriftTick
// registration.
func (a *Actor) startDriftLoop(ctx actor.Context) {
	lifecycle := ctx.Lifecycle()
	done := make(chan struct{})
	a.driftDone = done
	go func() {
		ticker := time.NewTicker(driftInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-lifecycle.Done():
				return
			case <-ticker.C:
				a.dispatch(callDriftTick, gen.WorkbenchSnapshotReq{})
			}
		}
	}()
}

func (a *Actor) stopDriftLoop() {
	if a.driftDone != nil {
		close(a.driftDone)
		a.driftDone = nil
	}
}
