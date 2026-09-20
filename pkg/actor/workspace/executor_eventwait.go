package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// The event_wait executor is the reactive counterpart of toolcall: instead of
// pushing a call outward, a card blocks until a matching event lands and the
// event payload becomes the card's task_outputs for downstream bindings.
//
// Event source (research conclusion, see the task card's 调研 point):
// the App-scoped EventBus (gospore internal/cell/eventbus.go) routes events
// by (actorId | serviceName, kind). Runtime cross-actor subscription is the
// streaming callable "gospore.events.subscribe_service" registered on the App
// root (gospore app/eventbus_callable.go) — the only subscription surface that
// accepts dynamically declared (service, kind) pairs, since
// Registrar.SubscribeEventKind is OnStart-only and cannot take card-declared
// kinds. Known emit faces: workspace's own events ("mounts",
// "agents_changed", ...) under the exposed "workspace" service, toast's
// "toast.card_added" under "toast", and the gateway / app.emit fan-out
// ("app_event") under "appmanager". Subscription semantics are
// forward-looking: events emitted before activation are not replayed.
//
// Activation is asynchronous (sub_map pattern), NOT blocking (crawl pattern):
// handleAgentSpawnAssign runs as the owner agent's turn-engine tool, and an
// event may legitimately take minutes or days to arrive, so Execute arms a
// per-wait subscription goroutine and returns immediately. The card stays
// "doing" while waiting; the goroutine performs the terminal flip:
//
//   - event arrives   → task_outputs {event, event_kind, event_service} + CAS doing→done
//   - deadline passes → task_outputs {error} + CAS doing→failed
//   - stream fails    → bounded resubscribe attempts, then task_outputs {error} + CAS doing→failed
//   - CAS miss        → the card left "doing" some other way (user cancel);
//     the wait is dropped without overriding the card's new state
//
// Active waits are persisted (workspace <actorID>/reventwaits card) and
// re-armed OnStart so a workspace restart does not strand a doing card.

const (
	// eventSubscribeServiceCallID is the App-root streaming callable that
	// subscribes the caller to every event of (serviceName, kind).
	eventSubscribeServiceCallID = "gospore.events.subscribe_service"

	// eventWaitDefaultService is the emitting service assumed when the card
	// does not declare data.exec.event_service: the workspace's own event
	// face (mounts / agents_changed / agent_list_state / workspace.log).
	eventWaitDefaultService = "workspace"

	// eventWaitMaxTimeoutSec caps data.exec.timeout_s (30 days) so a typo
	// cannot pin a wait forever in the persisted state; 0 (absent) means
	// wait indefinitely.
	eventWaitMaxTimeoutSec = 2592000
)

// eventWaitResubscribeDelay backs off a failed subscription attempt
// before retrying. A misconfigured (service, kind) fails fast and
// deterministically; a transient root hiccup recovers silently.
// (var, not const: tests shrink it to keep retry paths snappy.)
var eventWaitResubscribeDelay = 2 * time.Second

const (
	// eventWaitMaxResubscribes bounds stream re-establishment attempts
	// before the wait is declared failed onto the card.
	eventWaitMaxResubscribes = 5

	// eventWaitFinishAttempts bounds the outputs+flip retries when the
	// project actor is briefly unreachable at completion time.
	eventWaitFinishAttempts = 3
	eventWaitFinishRetry    = time.Second
)

// eventWaitConfig holds the parsed data.exec fields for an event_wait card.
type eventWaitConfig struct {
	EventKind    string
	EventService string
	// TimeoutSec bounds the wait; 0 = no deadline.
	TimeoutSec int
}

// eventWaitRecord is the persisted shape of one active wait. It round-trips
// through the workspace state card <actorID>/reventwaits; only active waits
// are persisted (terminal records are pruned at save time — the card itself
// carries the outcome).
type eventWaitRecord struct {
	ProjectID     string `json:"project_id"`
	TaskCardID    string `json:"task_card_id"`
	CallerAgentID string `json:"caller_agent_id,omitempty"`
	EventKind     string `json:"event_kind"`
	EventService  string `json:"event_service"`
	TimeoutSec    int    `json:"timeout_s,omitempty"`
	// ActivatedAt / Deadline are RFC3339 UTC strings; Deadline is empty
	// when the wait has no timeout. The deadline survives restarts, so a
	// re-armed wait keeps the ORIGINAL clock, not a fresh one.
	ActivatedAt string `json:"activated_at"`
	Deadline    string `json:"deadline,omitempty"`
}

// eventWaitState is the in-memory activation: the persisted record plus the
// computed deadline. Pointer identity doubles as the generation guard — a
// goroutine only completes a wait while its state pointer is still the one
// registered under the wait key (re-claim replaces the record and orphans
// the previous goroutine).
type eventWaitState struct {
	rec      eventWaitRecord
	deadline time.Time // zero = no deadline
}

func eventWaitKey(projectID, cardID string) string {
	return projectID + "/" + cardID
}

// eventWaitStream is the subscription handle consumed by a wait goroutine.
// *invoke.Call satisfies it in production (streaming invoke of
// gospore.events.subscribe_service on the App root); tests substitute
// channel-backed fakes as the fake event source.
type eventWaitStream interface {
	Next(ctx context.Context) (any, error)
	Cancel()
}

var _ eventWaitStream = (*invoke.Call)(nil)

// eventWaitExecutor implements the event_wait execKind.
type eventWaitExecutor struct {
	a *Actor
}

// newEventWaitExecutor wires the executor back to its hosting workspace.
func newEventWaitExecutor(a *Actor) *eventWaitExecutor {
	return &eventWaitExecutor{a: a}
}

// Kind returns "event_wait".
func (e *eventWaitExecutor) Kind() ExecKind {
	return ExecKindEventWait
}

// Preflight validates the event_wait declaration before the dispatcher claims
// the parent task card. Failures here leave the card untouched.
//
// Required configuration from the bound card's data.exec block:
//   - event_kind: the event kind to wait for (matched against the emitting
//     service's registered kinds; v1 filters by kind only).
//
// Optional:
//   - event_service: emitting actor's exposed service name (default
//     "workspace"); e.g. "toast" for toast.card_added, "appmanager" for the
//     gateway/app.emit fan-out kind "app_event".
//   - timeout_s: seconds until the card fails; 0 = wait indefinitely.
func (e *eventWaitExecutor) Preflight(ctx actor.PureContext, req ClaimReq) (PreflightResult, error) {
	if _, err := requireEventWaitConfig(req); err != nil {
		return PreflightResult{}, err
	}
	if _, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID); err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.event_wait: %w", err)
	}
	if err := requireActiveWorkflow(ctx, "workspace.executor.event_wait", req.CallerAgentID); err != nil {
		return PreflightResult{}, err
	}
	return PreflightResult{
		KindConfig:  domain.AgentKindConfig{Kind: domain.AgentKindWorker, DisplayName: "EventWaitExecutor"},
		DisplayName: "EventWaitExecutor",
		SpawnName:   "event-wait-" + req.BoundTaskCardID,
	}, nil
}

// Execute arms the wait and returns immediately. The card remains doing until
// the armed goroutine performs the terminal flip (done / failed) or a
// concurrent status change CAS-misses. There is no spawned agent, so
// ExecResp.AgentActorID is empty and the dispatcher skips owner stamping
// (same as toolcall).
func (e *eventWaitExecutor) Execute(ctx actor.PureContext, req ClaimReq) (ExecResp, error) {
	return panicprobe.Guard(ctx, "workspace.executor.event_wait", req, func() (ExecResp, error) {
		if req.Preflight == nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.event_wait: Preflight result is required (dispatcher contract)")
		}
		cfg, err := requireEventWaitConfig(req)
		if err != nil {
			return ExecResp{}, err
		}
		projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
		if err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.event_wait: %w", err)
		}
		projectRef, ok := lookupProjectRef(ctx, projectID)
		if !ok {
			return ExecResp{}, fmt.Errorf("workspace.executor.event_wait: project actor unavailable")
		}

		now := time.Now().UTC()
		state := &eventWaitState{rec: eventWaitRecord{
			ProjectID:     projectID,
			TaskCardID:    req.BoundTaskCardID,
			CallerAgentID: req.CallerAgentID,
			EventKind:     cfg.EventKind,
			EventService:  cfg.EventService,
			TimeoutSec:    cfg.TimeoutSec,
			ActivatedAt:   now.Format(time.RFC3339),
		}}
		if cfg.TimeoutSec > 0 {
			state.deadline = now.Add(time.Duration(cfg.TimeoutSec) * time.Second)
			state.rec.Deadline = state.deadline.UTC().Format(time.RFC3339)
		}
		e.a.armEventWait(ctx, projectRef, state)

		return ExecResp{
			DisplayName: "EventWaitExecutor",
			Goal: gen.GoalSummary{
				Condition:       fmt.Sprintf("Wait for event %s.%s to land on task card %q", cfg.EventService, cfg.EventKind, req.BoundTaskCardID),
				Status:          "active",
				Confirmed:       true,
				BoundTaskCardID: req.BoundTaskCardID,
				MaxTurns:        req.MaxTurns,
			},
		}, nil
	})
}

// requireEventWaitConfig parses the data.exec block. Used by Preflight and
// Execute. Card frontmatter scalars decode as strings, so timeout_s accepts
// both numeric and string forms (same coercion as toolcall).
func requireEventWaitConfig(req ClaimReq) (eventWaitConfig, error) {
	card := project.ParseCardRaw(req.BoundTaskCardID, req.CardRaw)
	execBlock := project.CardDataMap(card, "exec")
	eventKind, _ := execBlock["event_kind"].(string)
	eventKind = strings.TrimSpace(eventKind)
	if eventKind == "" {
		return eventWaitConfig{}, fmt.Errorf("workspace.executor.event_wait: data.exec.event_kind is required")
	}
	eventService, _ := execBlock["event_service"].(string)
	eventService = strings.TrimSpace(eventService)
	if eventService == "" {
		eventService = eventWaitDefaultService
	}
	timeoutSec := 0
	switch raw := execBlock["timeout_s"].(type) {
	case float64:
		timeoutSec = int(raw)
	case int:
		timeoutSec = raw
	case string:
		if parsed, perr := strconv.Atoi(strings.TrimSpace(raw)); perr == nil {
			timeoutSec = parsed
		}
	}
	if timeoutSec != 0 {
		if timeoutSec < 1 {
			timeoutSec = 1
		}
		if timeoutSec > eventWaitMaxTimeoutSec {
			timeoutSec = eventWaitMaxTimeoutSec
		}
	}
	return eventWaitConfig{EventKind: eventKind, EventService: eventService, TimeoutSec: timeoutSec}, nil
}

// lookupProjectRef resolves the project actor's ref from its canonical ID.
func lookupProjectRef(ctx actor.PureContext, projectID string) (ref.Ref, bool) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return nil, false
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	return projectRef, ok && projectRef != nil
}

// ---------------------------------------------------------------------------
// Wait lifecycle (actor-level; shared by Execute and the OnStart re-arm)
// ---------------------------------------------------------------------------

// armEventWait registers the wait in the actor's tracking map (replacing any
// previous wait for the same card — the orphaned goroutine's generation guard
// makes it abandon silently), persists the snapshot, and spawns the
// subscription goroutine.
//
// Only immutable PureContext reads (Root, Lifecycle, Logger) are captured;
// the per-invocation context itself is never retained past Execute.
func (a *Actor) armEventWait(ctx actor.PureContext, projectRef ref.Ref, state *eventWaitState) {
	key := eventWaitKey(state.rec.ProjectID, state.rec.TaskCardID)
	a.eventWaitMu.Lock()
	if a.eventWaits == nil {
		a.eventWaits = make(map[string]*eventWaitState)
	}
	a.eventWaits[key] = state
	snapshot := a.eventWaitsSnapshotLocked()
	a.eventWaitMu.Unlock()
	_ = a.saveEventWaitsSnapshot(snapshot)

	root := ctx.Root()
	lifecycle := ctx.Lifecycle()
	logger := ctx.Logger()
	subscribe := a.eventWaitSubscribeFn
	factory := func() eventWaitStream {
		if subscribe != nil {
			return subscribe(state.rec.EventService, state.rec.EventKind)
		}
		if root == nil {
			return nil
		}
		call := root.Invoke(lifecycle, eventSubscribeServiceCallID, map[string]string{
			"serviceName": state.rec.EventService,
			"kind":        state.rec.EventKind,
		})
		if call == nil {
			return nil
		}
		return call
	}
	go a.runEventWait(lifecycle, logger, projectRef, factory, state)
}

// eventWaitCurrent reports whether state is still the registered wait for its
// key (generation guard). A re-claim replaces the map entry, so the previous
// goroutine sees false and abandons without touching the card.
func (a *Actor) eventWaitCurrent(state *eventWaitState) bool {
	key := eventWaitKey(state.rec.ProjectID, state.rec.TaskCardID)
	a.eventWaitMu.RLock()
	defer a.eventWaitMu.RUnlock()
	return a.eventWaits[key] == state
}

// removeEventWait drops the wait from the tracking map and persists the
// pruned snapshot. Terminal for the wait's lifecycle.
func (a *Actor) removeEventWait(state *eventWaitState) {
	key := eventWaitKey(state.rec.ProjectID, state.rec.TaskCardID)
	a.eventWaitMu.Lock()
	if a.eventWaits[key] == state {
		delete(a.eventWaits, key)
	}
	snapshot := a.eventWaitsSnapshotLocked()
	a.eventWaitMu.Unlock()
	_ = a.saveEventWaitsSnapshot(snapshot)
}

// runEventWait is the per-wait subscription loop. It consumes the first
// matching event (retrying the subscription on transient stream errors),
// races it against the deadline and the actor lifecycle, and performs the
// terminal card flip. The loop exits (cancelling its stream) as soon as the
// state is no longer current.
func (a *Actor) runEventWait(lifecycle context.Context, logger actor.Logger, projectRef ref.Ref, factory func() eventWaitStream, state *eventWaitState) {
	if logger == nil {
		logger = noOpEventWaitLogger{}
	}
	var timerCh <-chan time.Time
	if !state.deadline.IsZero() {
		d := time.Until(state.deadline)
		if d <= 0 {
			a.finishEventWaitTimeout(lifecycle, logger, projectRef, state)
			return
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		timerCh = timer.C
	}

	sub := factory()
	if sub != nil {
		defer sub.Cancel()
	}
	resubscribes := 0
	var lastStreamErr error
	for {
		if !a.eventWaitCurrent(state) {
			return
		}
		if sub == nil {
			resubscribes++
			if resubscribes > eventWaitMaxResubscribes {
				err := lastStreamErr
				if err == nil {
					err = errors.New("event subscription unavailable")
				}
				a.finishEventWaitError(lifecycle, logger, projectRef, state, err)
				return
			}
			if !sleepEventWait(lifecycle, timerCh, eventWaitResubscribeDelay) {
				a.finishEventWaitTimeout(lifecycle, logger, projectRef, state)
				return
			}
			sub = factory()
			if sub != nil {
				defer sub.Cancel()
			}
			continue
		}

		type recv struct {
			val any
			err error
		}
		ch := make(chan recv, 1)
		go func() {
			val, err := sub.Next(lifecycle)
			ch <- recv{val: val, err: err}
		}()
		select {
		case <-lifecycle.Done():
			return
		case <-timerCh:
			a.finishEventWaitTimeout(lifecycle, logger, projectRef, state)
			return
		case r := <-ch:
			if r.err == nil {
				a.finishEventWaitEvent(lifecycle, logger, projectRef, state, r.val)
				return
			}
			// Stream error or io.EOF (server ended the stream).
			// Re-establish the subscription while attempts remain;
			// exhausted, fail the wait onto the card with the last
			// stream error so misconfiguration is diagnosable.
			lastStreamErr = r.err
			sub.Cancel()
			sub = nil
			if !errors.Is(r.err, io.EOF) && !errors.Is(r.err, context.Canceled) {
				logger.Warn("workspace: event_wait stream error",
					"card", state.rec.TaskCardID, "kind", state.rec.EventKind, "error", r.err)
			}
		}
	}
}

// sleepEventWait waits for d, returning false when the deadline or lifecycle
// ended the wait first.
func sleepEventWait(lifecycle context.Context, timerCh <-chan time.Time, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-lifecycle.Done():
		return false
	case <-timerCh:
		return false
	case <-t.C:
		return true
	}
}

// finishEventWaitEvent writes the matched payload as task_outputs and CASes
// the card doing→done.
func (a *Actor) finishEventWaitEvent(lifecycle context.Context, logger actor.Logger, projectRef ref.Ref, state *eventWaitState, payload any) {
	outputs := map[string]any{
		"event":         payload,
		"event_kind":    state.rec.EventKind,
		"event_service": state.rec.EventService,
	}
	a.finishEventWait(lifecycle, logger, projectRef, state, outputs, "done")
}

// finishEventWaitTimeout fails the card with a timeout note (CAS doing→failed).
func (a *Actor) finishEventWaitTimeout(lifecycle context.Context, logger actor.Logger, projectRef ref.Ref, state *eventWaitState) {
	note := fmt.Sprintf("event_wait timed out after %ds waiting for %s.%s", state.rec.TimeoutSec, state.rec.EventService, state.rec.EventKind)
	a.finishEventWait(lifecycle, logger, projectRef, state, map[string]any{"error": note}, "failed")
}

// finishEventWaitError fails the card with the terminal stream error (CAS
// doing→failed).
func (a *Actor) finishEventWaitError(lifecycle context.Context, logger actor.Logger, projectRef ref.Ref, state *eventWaitState, err error) {
	note := fmt.Sprintf("event_wait for %s.%s failed: %v", state.rec.EventService, state.rec.EventKind, err)
	a.finishEventWait(lifecycle, logger, projectRef, state, map[string]any{"error": note}, "failed")
}

// finishEventWait performs the terminal card transition: outputs first, then
// the status CAS with ExpectedStatus=doing. A CAS miss means the card left
// doing some other way (user cancel / concurrent completion) — the wait is
// dropped without overriding the card. Output write failures do not block the
// flip; both steps retry a bounded number of times against a briefly
// unreachable project actor, after which the wait is removed and the error
// logged (the doing card remains recoverable by the orchestrator's
// orphan-reclaim path).
func (a *Actor) finishEventWait(lifecycle context.Context, logger actor.Logger, projectRef ref.Ref, state *eventWaitState, outputs map[string]any, status string) {
	if !a.eventWaitCurrent(state) {
		return
	}
	var lastErr error
	for attempt := 0; attempt < eventWaitFinishAttempts; attempt++ {
		if attempt > 0 && !sleepEventWait(lifecycle, nil, eventWaitFinishRetry) {
			break
		}
		if err := eventWaitSetOutputs(lifecycle, projectRef, state.rec.TaskCardID, outputs); err != nil {
			lastErr = fmt.Errorf("write task outputs: %w", err)
			continue
		}
		casErr := eventWaitSetStatus(lifecycle, projectRef, state.rec.TaskCardID, status, "doing")
		if casErr == nil {
			a.removeEventWait(state)
			return
		}
		if isEventWaitCASMiss(casErr) {
			// The card is no longer doing; someone else owns its
			// terminal state. Drop the wait without touching it.
			logger.Info("workspace: event_wait card left doing before completion",
				"card", state.rec.TaskCardID, "target", status)
			a.removeEventWait(state)
			return
		}
		lastErr = fmt.Errorf("set task card %s: %w", status, casErr)
	}
	logger.Error("workspace: event_wait completion failed",
		"card", state.rec.TaskCardID, "status", status, "error", lastErr)
	a.removeEventWait(state)
}

// isEventWaitCASMiss reports whether err is the project-side expected-status
// mismatch produced by wiki_set_status when the card's live status differs
// from ExpectedStatus. String-matching the stable phrasing is the established
// pattern here (see isClaimStatusMiss / isDependencyBlockError).
func isEventWaitCASMiss(err error) bool {
	return err != nil && strings.Contains(err.Error(), "expected status")
}

// eventWaitSetOutputs writes outputs to the bound task card via
// project.wiki_set_task_outputs.
func eventWaitSetOutputs(lifecycle context.Context, projectRef ref.Ref, cardID string, outputs map[string]any) error {
	ctx, cancel := context.WithTimeout(lifecycle, 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(ctx, "project.wiki_set_task_outputs", domain.WikiSetTaskOutputsReq{
		CardID:  cardID,
		Outputs: outputs,
	})
	if call == nil {
		return fmt.Errorf("project.wiki_set_task_outputs invoke returned nil")
	}
	_, err := call.Final(ctx)
	return err
}

// eventWaitSetStatus updates the bound task card status via
// project.wiki_set_status with ExpectedStatus as the CAS guard. Mirrors
// casTaskCardStatus (workspace_workflow.go) but invokes a pre-resolved
// project ref with the actor lifecycle context — the wait goroutine cannot
// retain the per-invocation PureContext.
func eventWaitSetStatus(lifecycle context.Context, projectRef ref.Ref, cardID, status, expected string) error {
	ctx, cancel := context.WithTimeout(lifecycle, 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(ctx, "project.wiki_set_status", gen.WikiSetStatusReq{
		ID:             cardID,
		Status:         status,
		ExpectedStatus: expected,
	})
	if call == nil {
		return fmt.Errorf("project.wiki_set_status invoke returned nil")
	}
	_, err := call.Final(ctx)
	return err
}

// rearmEventWaits re-arms every persisted active wait after a workspace
// restart. Deadlines keep their original clock; a deadline already past at
// re-arm time completes the wait as timed out immediately. A project actor
// that cannot be resolved yet keeps its record for the next restart (logged).
func (a *Actor) rearmEventWaits(ctx actor.Context) {
	a.eventWaitMu.RLock()
	states := make([]*eventWaitState, 0, len(a.eventWaits))
	for _, state := range a.eventWaits {
		states = append(states, state)
	}
	a.eventWaitMu.RUnlock()
	for _, state := range states {
		projectRef, ok := lookupProjectRef(ctx, state.rec.ProjectID)
		if !ok {
			ctx.Logger().Error("workspace: event_wait re-arm skipped (project actor unavailable)",
				"card", state.rec.TaskCardID, "project", state.rec.ProjectID)
			continue
		}
		s := state
		// Refresh the deadline clock from the persisted record.
		if s.rec.Deadline != "" {
			if d, err := time.Parse(time.RFC3339, s.rec.Deadline); err == nil {
				s.deadline = d
			}
		}
		a.armEventWait(ctx, projectRef, s)
	}
}

// noOpEventWaitLogger guards against a nil logger (tests may construct the
// actor without a context).
type noOpEventWaitLogger struct{}

func (noOpEventWaitLogger) Debug(string, ...any) {}
func (noOpEventWaitLogger) Info(string, ...any)  {}
func (noOpEventWaitLogger) Warn(string, ...any)  {}
func (noOpEventWaitLogger) Error(string, ...any) {}

// eventWaitsSnapshotLocked captures the persisted record list. Caller holds
// eventWaitMu.
func (a *Actor) eventWaitsSnapshotLocked() []eventWaitRecord {
	out := make([]eventWaitRecord, 0, len(a.eventWaits))
	for _, state := range a.eventWaits {
		out = append(out, state.rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectID != out[j].ProjectID {
			return out[i].ProjectID < out[j].ProjectID
		}
		return out[i].TaskCardID < out[j].TaskCardID
	})
	return out
}

// loadEventWaitRecords replaces the in-memory wait map from a persisted
// record list (Load path). Deadlines are parsed; unparseable deadline strings
// degrade to "no deadline" rather than dropping the wait.
func (a *Actor) loadEventWaitRecords(records []eventWaitRecord) {
	waits := make(map[string]*eventWaitState, len(records))
	for _, rec := range records {
		state := &eventWaitState{rec: rec}
		if rec.Deadline != "" {
			if d, err := time.Parse(time.RFC3339, rec.Deadline); err == nil {
				state.deadline = d
			}
		}
		waits[eventWaitKey(rec.ProjectID, rec.TaskCardID)] = state
	}
	a.eventWaits = waits
}

// Compile-time interface checks.
var _ Executor = (*eventWaitExecutor)(nil)
var _ actor.Logger = noOpEventWaitLogger{}
