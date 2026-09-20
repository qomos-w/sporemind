package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/timer"
)


type Actor struct {
	actor.Host
	store           persist.Persist

	mu      sync.Mutex
	actorID string
	entries map[string]*scheduledEntry

	actorCtx    actor.Context
	timeWheel   timer.Timer
	timeHandler timer.TimeHandler
}

// scheduledEntry is the in-memory form: a registered schedule plus its live
// timer node. It is NOT the persisted shape — see persistedEntry/persistedSchedule.
type scheduledEntry struct {
	ProjectID string
	CardID    string
	Schedule  gen.TimerSchedule
	node      timer.TimeNoder
}

func (e *scheduledEntry) key() string { return e.ProjectID + ":" + e.CardID }

// persistedSchedule is the on-disk form. Enabled is a pointer because the
// generated wire type serializes bool with omitempty: a written `false` is
// indistinguishable from "field absent" once round-tripped through JSON. Using
// *bool lets Load tell a legacy file that predates the Enabled field (nil →
// historical default: enabled) apart from an entry the user explicitly disabled
// (non-nil false). Without this, disabling a scheduled task never survived a
// restart — the persisted false was dropped and re-read as enabled.
type persistedSchedule struct {
	Cron       string `json:"Cron,omitempty"`
	Expression string `json:"Expression,omitempty"`
	Timezone   string `json:"Timezone,omitempty"`
	Enabled    *bool  `json:"Enabled"`
}

type persistedEntry struct {
	ProjectID string            `json:"projectId"`
	CardID    string            `json:"cardId"`
	Schedule  persistedSchedule `json:"schedule"`
}

type schedulerState struct {
	Entries []*persistedEntry `json:"entries"`
}

// enabledOrDefault maps the persisted Enabled pointer to the in-memory bool:
// nil means the historical default (a state file written before the field
// existed → enabled), while an explicit false stays false.
//
// The register path shares this "explicit false disables" rule but cannot use a
// pointer: the generated wire type carries a plain bool, so an omitted Enabled
// arrives as false and cannot be told apart from an explicit false (declared
// optional in the schema; see the scheduler-schema-contract audit — changing
// that shape is a codegen change outside this card). Callers that must disable
// a schedule use set_enabled, which round-trips an explicit false through both
// this helper and the persisted form.
func enabledOrDefault(enabled *bool) bool {
	return enabled == nil || *enabled
}

var _ persist.Persistent = (*Actor)(nil)

func (a *Actor) Save() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("scheduler"))
		if err != nil {
			return err
		}
	}
	st := schedulerState{}
	for _, e := range a.entries {
		enabled := e.Schedule.Enabled
		st.Entries = append(st.Entries, &persistedEntry{
			ProjectID: e.ProjectID,
			CardID:    e.CardID,
			Schedule: persistedSchedule{
				Cron:       e.Schedule.Cron,
				Expression: e.Schedule.Expression,
				Timezone:   e.Schedule.Timezone,
				Enabled:    &enabled,
			},
		})
	}
	return a.store.Save(a.actorID, st)
}

func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("scheduler"))
		if err != nil {
			return err
		}
	}
	var st schedulerState
	if err := persist.LoadOrZero(a.store, a.actorID, &st); err != nil {
		return fmt.Errorf("scheduler: load state: %w", err)
	}
	for _, pe := range st.Entries {
		enabled := enabledOrDefault(pe.Schedule.Enabled)
		a.entries[pe.ProjectID+":"+pe.CardID] = &scheduledEntry{
			ProjectID: pe.ProjectID,
			CardID:    pe.CardID,
			Schedule: gen.TimerSchedule{
				Cron:       pe.Schedule.Cron,
				Expression: pe.Schedule.Expression,
				Timezone:   pe.Schedule.Timezone,
				Enabled:    enabled,
			},
		}
	}
	return nil
}

func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("scheduler"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	a.entries = make(map[string]*scheduledEntry)
	a.timeWheel = timer.NewTimer()
	a.timeHandler = a.timeWheel.NewHandler()
	if err := a.Load(); err != nil {
		ctx.Logger().Error("scheduler: load state failed", "error", err)
	}
	return nil
}

func (a *Actor) OnStart(ctx actor.Context) error {
	a.actorCtx = ctx
	ctx.Logger().Info("scheduler: starting", "id", a.actorID, "entries", len(a.entries))

	if err := ctx.Register("scheduler.register", a.handleRegister, actor.Internal()); err != nil {
		return fmt.Errorf("scheduler: register register: %w", err)
	}
	if err := ctx.Register("scheduler.unregister", a.handleUnregister, actor.Internal()); err != nil {
		return fmt.Errorf("scheduler: register unregister: %w", err)
	}
	if err := ctx.Register("scheduler.fire", a.handleFire, actor.Internal()); err != nil {
		return fmt.Errorf("scheduler: register fire: %w", err)
	}
	if err := ctx.Register("scheduler.list", a.handleList, actor.Internal()); err != nil {
		return fmt.Errorf("scheduler: register list: %w", err)
	}
	if err := ctx.Register("scheduler.set_enabled", a.handleSetEnabled, actor.Internal()); err != nil {
		return fmt.Errorf("scheduler: register set_enabled: %w", err)
	}

	// Expose the scheduler as an App-level service so the project actor can
	// resolve it via ctx.LookupService("scheduler") for timer listing/toggling.
	if err := ctx.RegisterDomain("scheduler").Expose(); err != nil {
		return fmt.Errorf("scheduler: expose service: %w", err)
	}

	a.timeWheel.Start()
	a.mu.Lock()
	for _, entry := range a.entries {
		if entry.Schedule.Cron != "" || entry.Schedule.Expression != "" {
			// A legacy/corrupt entry must not abort OnStart (that would restart
			// the actor on every supervisor cycle): log and leave it without a
			// node so it simply never fires.
			if err := a.scheduleEntryLocked(entry); err != nil {
				ctx.Logger().Error("scheduler: skipping invalid loaded schedule",
					"project", entry.ProjectID, "card", entry.CardID, "error", err)
			}
		}
	}
	a.mu.Unlock()
	return nil
}

func (a *Actor) OnStop(_ actor.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.timeWheel.Stop()
	return a.Save()
}

func (a *Actor) handleRegister(ctx actor.Context, req gen.SchedulerRegisterReq) error {
	ctx.Logger().Info("scheduler: register", "project", req.ProjectID, "card", req.CardID)

	a.mu.Lock()
	defer a.mu.Unlock()

	entry := &scheduledEntry{
		ProjectID: req.ProjectID,
		CardID:    req.CardID,
		Schedule:  req.Schedule,
	}

	// Validate and build the timer node BEFORE mutating a.entries. A bad
	// schedule now returns an error instead of inserting a half-registered entry
	// (and previously panicking inside the timer library, which restarted the
	// actor and left memory/persisted state diverged — #1 schedule audit).
	if entry.Schedule.Cron != "" || entry.Schedule.Expression != "" {
		if err := a.scheduleEntryLocked(entry); err != nil {
			if entry.node != nil {
				entry.node.Stop()
			}
			return err
		}
	}

	if previous, ok := a.entries[entry.key()]; ok && previous.node != nil {
		previous.node.Stop()
	}
	a.entries[entry.key()] = entry

	return a.Save()
}

func (a *Actor) handleUnregister(ctx actor.Context, req gen.SchedulerUnregisterReq) error {
	ctx.Logger().Info("scheduler: unregister", "project", req.ProjectID, "card", req.CardID)

	a.mu.Lock()
	defer a.mu.Unlock()

	key := req.ProjectID + ":" + req.CardID
	if entry, ok := a.entries[key]; ok && entry.node != nil {
		entry.node.Stop()
	}
	delete(a.entries, key)
	return a.Save()
}

// scheduleEntryLocked (re)creates the timer node for an entry.
//
// It first unregisters any existing node: set_enabled(true) on an already
// enabled entry used to overwrite entry.node while the old node stayed live in
// the wheel, so the project was fired twice (#2 schedule audit). Stop() is
// idempotent, so this is also safe for the disable path.
//
// A schedule that cannot be parsed is reported as an error and leaves the entry
// without a node. There is no silent fallback to an hourly timer (#7): a user
// who never asked for an hourly schedule must not get one.
func (a *Actor) scheduleEntryLocked(entry *scheduledEntry) error {
	if entry.node != nil {
		entry.node.Stop()
		entry.node = nil
	}
	if !entry.Schedule.Enabled {
		return nil
	}

	cb := func(_ timer.TimeNoder) {
		_ = a.actorCtx.After(0, "scheduler.fire", gen.ProjectExecuteTimerCardReq{
			ProjectID: entry.ProjectID,
			CardID:    entry.CardID,
		})
	}

	if entry.Schedule.Cron != "" {
		loc, err := resolveLocation(entry.Schedule.Timezone)
		if err != nil {
			return err
		}
		s, m, h, d, mon, wd, err := parseCron6(entry.Schedule.Cron)
		if err != nil {
			return fmt.Errorf("scheduler: cron %q: %w", entry.Schedule.Cron, err)
		}
		node, err := a.timeHandler.Cron(s, m, h, d, mon, wd, cb, timer.WithLocation(loc))
		if err != nil {
			return fmt.Errorf("scheduler: cron %q: %w", entry.Schedule.Cron, err)
		}
		entry.node = node
		return nil
	}

	if entry.Schedule.Expression != "" {
		loc, err := resolveLocation(entry.Schedule.Timezone)
		if err != nil {
			return err
		}
		interval, err := parseNaturalDuration(entry.Schedule.Expression, loc)
		if err != nil {
			return fmt.Errorf("scheduler: expression %q: %w", entry.Schedule.Expression, err)
		}
		if interval <= 0 {
			return fmt.Errorf("scheduler: expression %q: non-positive interval", entry.Schedule.Expression)
		}
		entry.node = a.timeHandler.Schedule(interval, cb, expressionTimerOpts(entry.Schedule.Expression)...)
		return nil
	}

	return fmt.Errorf("scheduler: entry %s has neither cron nor expression", entry.key())
}

// expressionTimerOpts returns the TimerOptions for a natural-language
// expression. "at <timestamp>" is one-shot: without WithLoop(1) loopMax stays
// 0, which the wheel treats as "repeat forever", turning a one-shot into a
// permanent timer (#3 schedule audit). Repeating expressions get no options.
func expressionTimerOpts(expr string) []timer.TimerOption {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(expr)), "at ") {
		return []timer.TimerOption{timer.WithLoop(1)}
	}
	return nil
}

// resolveLocation turns a schedule timezone name into a *time.Location. An
// empty name means "use the server default" (nil, which the wheel maps to
// time.Local). An unknown name is an explicit error, never a silent fallback.
func resolveLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("scheduler: bad timezone %q: %w", name, err)
	}
	return loc, nil
}

// handleFire is dispatched from the time wheel callback via ctx.After.
// It looks up the project ref and invokes project.execute_timer_card.
func (a *Actor) handleFire(ctx actor.Context, req gen.ProjectExecuteTimerCardReq) error {
	ctx.Logger().Info("scheduler: fire", "project", req.ProjectID, "card", req.CardID)

	// Verify the entry still exists (may have been unregistered) and is enabled.
	a.mu.Lock()
	entry, ok := a.entries[req.ProjectID+":"+req.CardID]
	enabled := ok && entry.Schedule.Enabled
	a.mu.Unlock()
	if !ok {
		return nil
	}
	if !enabled {
		ctx.Logger().Info("scheduler: fire skipped (disabled)", "project", req.ProjectID, "card", req.CardID)
		return nil
	}

	cid, err := identity.ParseCanonicalID(req.ProjectID)
	if err != nil {
		ctx.Logger().Error("scheduler: invalid project id", "project", req.ProjectID, "error", err)
		return nil
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok {
		ctx.Logger().Error("scheduler: project not found", "project", req.ProjectID)
		return nil
	}

	call := projectRef.Invoke(ctx.Lifecycle(), "project.execute_timer_card", gen.ProjectExecuteTimerCardReq{
		CardID:    req.CardID,
		ProjectID: req.ProjectID,
	})
	if call != nil {
		_ = call.Close()
	}
	return nil
}

// handleList returns all registered scheduled entries with their next fire time.
func (a *Actor) handleList(_ actor.Context, _ gen.SchedulerListReq) (gen.SchedulerListResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	resp := gen.SchedulerListResp{Entries: make([]gen.SchedulerEntryInfo, 0, len(a.entries))}
	for _, entry := range a.entries {
		info := gen.SchedulerEntryInfo{
			ProjectID: entry.ProjectID,
			CardID:    entry.CardID,
			Schedule:  entry.Schedule,
		}
		if entry.node != nil {
			next := entry.node.Next()
			if !next.IsZero() {
				info.NextFireAt = next.Format(time.RFC3339)
			}
		}
		resp.Entries = append(resp.Entries, info)
	}
	return resp, nil
}

// handleSetEnabled toggles whether an entry is allowed to fire.
func (a *Actor) handleSetEnabled(ctx actor.Context, req gen.SchedulerSetEnabledReq) (gen.SchedulerSetEnabledResp, error) {
	ctx.Logger().Info("scheduler: set_enabled", "project", req.ProjectID, "card", req.CardID, "enabled", req.Enabled)

	a.mu.Lock()
	defer a.mu.Unlock()

	key := req.ProjectID + ":" + req.CardID
	entry, ok := a.entries[key]
	if !ok {
		return gen.SchedulerSetEnabledResp{}, fmt.Errorf("scheduler: entry not found: %s", key)
	}
	entry.Schedule.Enabled = req.Enabled
	if err := a.scheduleEntryLocked(entry); err != nil {
		// Keep the in-memory state consistent with "no live node": stay
		// disabled rather than reporting enabled with nothing scheduled.
		entry.Schedule.Enabled = false
		return gen.SchedulerSetEnabledResp{}, fmt.Errorf("scheduler: set_enabled: %w", err)
	}
	if err := a.Save(); err != nil {
		return gen.SchedulerSetEnabledResp{}, fmt.Errorf("scheduler: save: %w", err)
	}
	return gen.SchedulerSetEnabledResp{Enabled: entry.Schedule.Enabled}, nil
}

// parseCron6 converts a standard 5-field cron into 6 gospore timer fields.
// gospore requires one of day/weekday to be "?".
func parseCron6(cron string) (second, minute, hour, day, month, weekday string, err error) {
	parts := strings.Fields(cron)
	if len(parts) != 5 {
		return "", "", "", "", "", "", fmt.Errorf("cron: expected 5 fields, got %d", len(parts))
	}
	m, h, d, mon, wd := parts[0], parts[1], parts[2], parts[3], parts[4]

	second = "0"
	minute = m
	hour = h
	month = mon

	if d == "*" && wd == "*" {
		day = "*"
		weekday = "?"
	} else if d != "*" && wd != "*" {
		day = d
		weekday = "?"
	} else if d == "*" {
		day = "?"
		weekday = wd
	} else {
		day = d
		weekday = "?"
	}
	return second, minute, hour, day, month, weekday, nil
}

// parseNaturalDuration recognizes common duration phrases. loc is the
// schedule's timezone and is used to interpret absolute "at <timestamp>"
// expressions; a nil loc means the server's time.Local.
func parseNaturalDuration(expr string, loc *time.Location) (time.Duration, error) {
	expr = strings.ToLower(strings.TrimSpace(expr))

	if strings.HasPrefix(expr, "every ") {
		return parseEvery(expr)
	}
	if strings.HasPrefix(expr, "at ") {
		return parseAt(expr, loc)
	}
	return 0, fmt.Errorf("scheduler: unrecognised expression: %q", expr)
}

func parseEvery(expr string) (time.Duration, error) {
	rest := strings.TrimPrefix(expr, "every ")
	if n, unit, ok := parseNumberAndUnit(rest); ok {
		switch unit {
		case "second", "seconds":
			return time.Duration(n) * time.Second, nil
		case "minute", "minutes":
			return time.Duration(n) * time.Minute, nil
		case "hour", "hours":
			return time.Duration(n) * time.Hour, nil
		case "day", "days":
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	return 0, fmt.Errorf("scheduler: unsupported expression: %q", expr)
}

func parseAt(expr string, loc *time.Location) (time.Duration, error) {
	if loc == nil {
		loc = time.Local
	}
	ts := strings.TrimPrefix(expr, "at ")
	t, err := time.ParseInLocation("2006-01-02 15:04:05", ts, loc)
	if err != nil {
		return 0, fmt.Errorf("scheduler: bad time %q: %w", ts, err)
	}
	d := time.Until(t)
	if d <= 0 {
		return 0, fmt.Errorf("scheduler: time %s is in the past", ts)
	}
	return d, nil
}

func parseNumberAndUnit(s string) (int, string, bool) {
	parts := strings.Fields(strings.TrimSpace(s))
	if len(parts) < 2 {
		return 0, "", false
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", false
	}
	return n, parts[1], true
}
