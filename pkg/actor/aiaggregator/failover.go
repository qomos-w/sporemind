package aiaggregator

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// Health state constants for projection. These mirror
// llmclient.HealthState* so the aggregator's status projection is consistent
// with the process-wide health layer without importing the values indirectly.
const (
	healthStateHealthy     = llmclient.HealthStateHealthy
	healthStateCoolingDown = llmclient.HealthStateCoolingDown
	healthStateDisabled    = llmclient.HealthStateDisabled
)

// Dispatch state constants for DispatchActivity.State. These describe the
// in-flight state of a callable unit during an aggregator dispatch. Empty
// State ("") or an absent DispatchActivity means the unit is idle.
const (
	// DispatchStateWaiting means the unit is queued behind the currently
	// attempted candidate in a failover selection and has not yet been tried.
	DispatchStateWaiting = "waiting"
	// DispatchStateTrying means an attempt to open a stream on this unit is
	// in flight.
	DispatchStateTrying = "trying"
	// DispatchStateInUse means the unit won selection and an active session
	// is streaming through it.
	DispatchStateInUse = "in_use"
)

// childStatusTimeout is the per-child timeout for querying a nested
// aggregator's status in handleStatus. Each child query is independent and
// bounded; a timeout yields nil (no activity propagated from that child).
const childStatusTimeout = 3 * time.Second

// stopRetryBudget governs stop-class (400/404) rotation within a single
// dispatch or summarize. Some 400s are unit-specific — context-window overflow,
// parameter incompatibility — so the first stop-class failure rotates to
// another unit; a subsequent stop-class failure terminates only when its HTTP
// status code matches the first (the same code on two units signals a
// request-level problem, not a unit quirk). A different stop code rotates
// again. Caller cancellation never rotates.
type stopRetryBudget struct {
	firstStatus int
	hasFirst    bool
}

// consume reports whether a stop-class failure may rotate to the next unit.
// Cancellation always terminates immediately; the first stop-class failure
// records its status and rotates; a repeat of that status terminates, while a
// different status keeps rotating.
func (b *stopRetryBudget) consume(classification llmclient.StreamOpenClassification, err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if !b.hasFirst {
		b.hasFirst = true
		b.firstStatus = classification.StatusCode
		return true
	}
	return classification.StatusCode != b.firstStatus
}

// unitHealthFromSnapshot returns the llmclient.UnitHealthSnapshot for the given
// provider::model pair from the snapshot, or a zero-value (healthy) snapshot
// when the pair has never been recorded.
func unitHealthFromSnapshot(snap map[string]llmclient.UnitHealthSnapshot, provider, model string) llmclient.UnitHealthSnapshot {
	if h, ok := snap[provider+"::"+model]; ok {
		return h
	}
	return llmclient.UnitHealthSnapshot{
		Provider: provider,
		Model:    model,
		State:    llmclient.HealthStateHealthy,
	}
}

// unitAvailable reports whether the unit is currently available for selection,
// reading the global llmclient health snapshot. Aggregator-ref entries
// (AggregatorID != "") consult the aggregator-level health registry instead —
// a child whose whole pool is registered unavailable is hard-skipped so the
// parent does not fire a doomed nested dispatch. Disable-window units are
// always unavailable (operator policy, not health).
func unitAvailable(u CallableUnit, snap map[string]llmclient.UnitHealthSnapshot, now time.Time) bool {
	if u.AggregatorID != "" {
		return llmclient.IsAggregatorAvailable(u.AggregatorID, now)
	}
	if unitInDisableWindow(u, now.Unix()) {
		return false
	}
	h := unitHealthFromSnapshot(snap, u.ProviderName, u.Model)
	switch h.State {
	case llmclient.HealthStateDisabled:
		return false
	case llmclient.HealthStateCoolingDown:
		return !now.Before(h.CooldownUntil)
	default:
		return true
	}
}

// pinnedUnitSelectable is the pinned-selection variant of unitAvailable: a
// hard user selection (unit-locked slot) bypasses transient cooling — the
// user's retry must genuinely re-attempt the chosen unit — but hard bans still
// apply: auth-disabled health state, the operator's daily disable window, and
// child aggregators registered unavailable.
func pinnedUnitSelectable(u CallableUnit, snap map[string]llmclient.UnitHealthSnapshot, now time.Time) bool {
	if u.AggregatorID != "" {
		return llmclient.IsAggregatorAvailable(u.AggregatorID, now)
	}
	if unitInDisableWindow(u, now.Unix()) {
		return false
	}
	h := unitHealthFromSnapshot(snap, u.ProviderName, u.Model)
	return h.State != llmclient.HealthStateDisabled
}

// healthRankSnapshot returns 0 (healthy) or 1 (cooling_down) or 2 (disabled)
// based on the llmclient health snapshot. Aggregator-ref entries are healthy
// (rank 0) while the child is available and disabled-severity (rank 2) while
// the child is registered unavailable. Lower rank = healthier.
func healthRankSnapshot(u CallableUnit, snap map[string]llmclient.UnitHealthSnapshot) int {
	if u.AggregatorID != "" {
		if !llmclient.IsAggregatorAvailable(u.AggregatorID, time.Now()) {
			return 2
		}
		return 0
	}
	h := unitHealthFromSnapshot(snap, u.ProviderName, u.Model)
	switch h.State {
	case llmclient.HealthStateDisabled:
		return 2
	case llmclient.HealthStateCoolingDown:
		if h.Remaining > 0 {
			return 1
		}
		return 0
	default:
		return 0
	}
}

// preferHealthy partitions units into healthy and unhealthy groups based on the
// llmclient health snapshot. If any healthy units exist, only they are returned
// (the strategy picks from the healthy group). If all units are unhealthy, all
// are returned — a unit's dispatch ability is never revoked. This is advisory
// ordering, not filtering. Within each group, units are sorted by remaining
// cooldown duration ascending so the unit closest to recovery comes first.
func preferHealthy(units []CallableUnit) []CallableUnit {
	snap := llmclient.HealthSnapshot()
	healthy := make([]CallableUnit, 0, len(units))
	unhealthy := make([]CallableUnit, 0, len(units))
	for _, u := range units {
		if healthRankSnapshot(u, snap) == 0 {
			healthy = append(healthy, u)
		} else {
			unhealthy = append(unhealthy, u)
		}
	}
	if len(healthy) > 0 {
		return healthy
	}
	// Sort unhealthy by remaining cooldown ascending (closest to recovery first).
	sort.SliceStable(unhealthy, func(i, j int) bool {
		ri := unitHealthFromSnapshot(snap, unhealthy[i].ProviderName, unhealthy[i].Model).Remaining
		rj := unitHealthFromSnapshot(snap, unhealthy[j].ProviderName, unhealthy[j].Model).Remaining
		return ri < rj
	})
	return unhealthy
}

// applyStreamOpenFailure records the failure to the llmclient health layer and
// sends a structured report to aimanager. No local aggregator state is written
// — the llmclient ProviderHealth is the single source of truth for unit-level
// cooldown/disabled state. The classification's Rotatable field drives the
// failover loop's decision to try the next candidate.
//
// Oracle diagnostic bypass: after the existing failure handling, a single
// oracle.report_diagnostic is emitted when this failure is what degraded the
// unit (healthy → cooling_down/disabled transition in the llmclient health
// snapshot taken before/after RecordFailure). The cooldown/failover policy
// itself is untouched.
func (a *Actor) applyStreamOpenFailure(unit *CallableUnit, err error, c llmclient.StreamOpenClassification) {
	beforeHealth := llmclient.HealthSnapshot()
	if unit != nil && unit.ProviderName != "" && err != nil {
		llmclient.RecordFailure(unit.ProviderName, unit.Model, err)
	}
	a.reportStructuredFailure(unit, err)
	a.reportOwnPoolHealth()
	if unit != nil && err != nil && firstDegradationTransition(beforeHealth, llmclient.HealthSnapshot(), unit.ProviderName, unit.Model) {
		a.reportOracleDiagnostic(unit, err, c)
	}
}

// diagnosticRawDataLimit caps Message/RawData payloads before they are sent to
// oracle or written to logs: upstream response bodies are unbounded and must
// never cross an actor boundary verbatim (openai respSnippet lesson).
const diagnosticRawDataLimit = 512

// truncateSnippet caps s at limit bytes with a visible marker when truncated.
func truncateSnippet(s string, limit int) string {
	if len(s) > limit {
		return s[:limit] + "...(truncated)"
	}
	return s
}

// diagnosticSeverity maps an HTTP status to an oracle severity: client-class
// failures (4xx — auth, quota, rate limit) are operator-actionable warnings;
// server/network/unknown failures are errors.
func diagnosticSeverity(httpStatus int) string {
	if httpStatus >= 400 && httpStatus < 500 {
		return "warning"
	}
	return "error"
}

// firstDegradationTransition reports whether provider::model was healthy in
// the before snapshot and is cooling_down or disabled in the after snapshot —
// i.e. this exact failure crossed the unit into a degraded state. ClassStop
// errors never move state (RecordFailure no-ops on them), so they never
// report; ClassFailover reports only when AvailabilityThreshold is crossed;
// already-cooling/disabled units never re-report.
func firstDegradationTransition(before, after map[string]llmclient.UnitHealthSnapshot, provider, model string) bool {
	if provider == "" || model == "" {
		return false
	}
	if unitHealthFromSnapshot(before, provider, model).State != llmclient.HealthStateHealthy {
		return false
	}
	switch unitHealthFromSnapshot(after, provider, model).State {
	case llmclient.HealthStateCoolingDown, llmclient.HealthStateDisabled:
		return true
	default:
		return false
	}
}

// reportOracleDiagnostic sends a one-shot diagnostic to the global oracle
// actor via oracle.report_diagnostic so stream-open degradations surface as
// problems. Fire-and-forget: resolution/invoke failures are logged and never
// affect dispatch or failover. Message and RawData are truncated before send;
// RawData carries the full error string (which for UpstreamError embeds the
// status line and message).
func (a *Actor) reportOracleDiagnostic(unit *CallableUnit, err error, c llmclient.StreamOpenClassification) {
	if unit == nil || unit.ProviderName == "" || err == nil {
		return
	}

	httpStatus := c.StatusCode
	message := ""
	var upstreamErr *llmclient.UpstreamError
	if errors.As(err, &upstreamErr) {
		httpStatus = upstreamErr.StatusCode
		message = upstreamErr.Message
	} else if s := llmclient.ExtractHTTPStatus(err); s > 0 {
		httpStatus = s
	}
	if message == "" {
		message = err.Error()
	}

	req := domain.OracleReportDiagnosticReq{
		Severity:   diagnosticSeverity(httpStatus),
		Source:     "aiaggregator",
		Message:    truncateSnippet("["+c.Class.String()+"/"+c.Reason+"] "+message, diagnosticRawDataLimit),
		Unit:       &domain.ModelUnit{Provider: unit.ProviderName, Model: unit.Model},
		HttpStatus: int32(httpStatus),
		RawData:    truncateSnippet(err.Error(), diagnosticRawDataLimit),
	}

	ref, resolveErr := a.resolveOracleRef()
	if resolveErr != nil {
		slog.Debug("aiaggregator: resolve oracle ref failed", "error", resolveErr)
		return
	}
	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, domain.DefaultInvokeTimeout)
	defer cancel()
	call := ref.Invoke(callCtx, "oracle.report_diagnostic", req)
	_ = call.Close()
}

// reportOwnPoolHealth inspects this aggregator's own pool after a selection or
// a failure handling and registers (or clears) its aggregator-level health in
// the llmclient registry. The event is low-frequency by design: only a fully
// unavailable pool (every unit cooling/disabled per the llmclient snapshot and
// no on-demand fallback) is registered; any available unit clears the
// registration. The system aggregator never self-reports — it retains the
// on-demand resolution fallback for pinned (provider, model) requests, so its
// pool is never "fully unavailable" in the reportable sense (and as the root
// aggregator it has no parent to inform anyway).
func (a *Actor) reportOwnPoolHealth() {
	key := a.aggHealthKey()
	if key == "" || key == systemAggregatorID {
		return
	}
	now := time.Now()
	if a.ownPoolUnavailable(now) {
		// Register with the pool's recovery estimate — the longest remaining
		// unit cooldown — floored at the default TTL. A plain TTL registration
		// would expire after 30s while the pool is still cooling for minutes,
		// so parents would re-probe (and re-fail) the aggregator every 30s at
		// dispatch rate. Units disabled by auth failures carry no deadline;
		// for those the default TTL still bounds the skip window.
		until := now.Add(llmclient.DefaultAggregatorUnavailableTTL)
		if horizon := a.poolRecoveryHorizon(now); horizon.After(until) {
			until = horizon
		}
		llmclient.RegisterAggregatorUnavailableUntil(key, until)
		return
	}
	llmclient.RegisterAggregatorHealth(key, llmclient.AggregatorHealthAvailable)
}

// poolRecoveryHorizon returns the latest recovery deadline among this pool's
// currently unavailable entries: concrete units' remaining cooldowns and
// nested aggregator refs' registered unavailable-until deadlines. It is the
// earliest time the pool could plausibly serve again — used as the skip
// horizon parents honor. The zero time (no timed entry) means no estimate.
func (a *Actor) poolRecoveryHorizon(now time.Time) time.Time {
	pool := a.chatUnits()
	snap := llmclient.HealthSnapshot()
	aggSnap := llmclient.AggregatorHealthSnapshot()
	var horizon time.Time
	for _, u := range pool {
		if u.AggregatorID != "" {
			if e, ok := aggSnap[u.AggregatorID]; ok && e.UnavailableUntil.After(horizon) {
				horizon = e.UnavailableUntil
			}
			continue
		}
		h := unitHealthFromSnapshot(snap, u.ProviderName, u.Model)
		if h.State == llmclient.HealthStateCoolingDown && h.CooldownUntil.After(now) && h.CooldownUntil.After(horizon) {
			horizon = h.CooldownUntil
		}
	}
	return horizon
}

// ownPoolUnavailable reports whether every candidate unit in this aggregator's
// pool is currently unavailable: concrete units are cooling/disabled per the
// llmclient unit-level snapshot and aggregator-ref entries are themselves
// registered unavailable. An empty pool is fully unavailable — a config-loaded
// aggregator with no servable unit can never open a stream. Disable-window and
// image-modality units are excluded from the candidate pool by chatUnits.
func (a *Actor) ownPoolUnavailable(now time.Time) bool {
	pool := a.chatUnits()
	if len(pool) == 0 {
		return true
	}
	snap := llmclient.HealthSnapshot()
	for _, u := range pool {
		// Must mirror selectUnit's selection predicate exactly
		// (tokenPlanExhausted || !unitAvailable): any unit that selectUnit
		// filters out but this loop counts as available would register this
		// aggregator "available" while every selection fails — parents then
		// re-probe it at dispatch rate.
		if !tokenPlanExhausted(u) && unitAvailable(u, snap, now) {
			return false
		}
	}
	return true
}

// reportStructuredFailure sends a structured failure report to aimanager via
// aimanager.provider_report_failure. Unlike the legacy reportProviderFailure,
// this method sends StatusCode/Code/RetryAfter/Message extracted from the
// UpstreamError, and does NOT gate on isProviderFailure — the aimanager side
// classifies the report and decides the appropriate provider-level treatment.
func (a *Actor) reportStructuredFailure(unit *CallableUnit, err error) {
	if unit == nil || unit.ProviderName == "" || err == nil {
		return
	}

	// The JSON tags match aimanager.reportProviderFailureReq so the structured
	// payload is wire-compatible with the legacy {providerName} format (absent
	// fields decode to zero values).
	type structuredFailureReq struct {
		ProviderName string `json:"providerName"`
		Model        string `json:"model,omitempty"`
		StatusCode   int32  `json:"statusCode,omitempty"`
		Code         string `json:"code,omitempty"`
		Type         string `json:"type,omitempty"`
		RetryAfter   int32  `json:"retryAfter,omitempty"` // seconds
		Message      string `json:"message,omitempty"`
	}

	req := structuredFailureReq{
		ProviderName: unit.ProviderName,
		Model:        unit.Model,
	}

	var upstreamErr *llmclient.UpstreamError
	if errors.As(err, &upstreamErr) {
		req.StatusCode = int32(upstreamErr.StatusCode)
		req.Code = upstreamErr.Code
		req.Type = upstreamErr.Type
		req.Message = upstreamErr.Message
		if upstreamErr.RetryAfter > 0 {
			req.RetryAfter = int32(upstreamErr.RetryAfter.Seconds())
		}
	} else {
		status := llmclient.ExtractHTTPStatus(err)
		if status > 0 {
			req.StatusCode = int32(status)
		}
		typ, code := llmclient.ExtractErrorCodeType(err)
		req.Code = code
		req.Type = typ
		req.Message = err.Error()
	}

	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, domain.DefaultInvokeTimeout)
	defer cancel()
	call := a.aimanagerRef.Invoke(callCtx, "aimanager.provider_report_failure", req)
	_ = call.Close()
}

// reportProviderAssign sends a fire-and-forget provider-assignment update to
// aimanager after a unit is selected for dispatch. It records which provider
// a given (agent, slot) is currently using so all aggregators can load-balance
// new/fallback picks via provider_assignments.
func (a *Actor) reportProviderAssign(agentID, slotKind, providerName string) {
	if agentID == "" || providerName == "" {
		return
	}
	req := struct {
		AgentID      string `json:"agentId"`
		SlotKind     string `json:"slotKind"`
		ProviderName string `json:"providerName"`
	}{
		AgentID:      agentID,
		SlotKind:     slotKind,
		ProviderName: providerName,
	}
	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, domain.DefaultInvokeTimeout)
	defer cancel()
	call := a.aimanagerRef.Invoke(callCtx, "aimanager.provider_assign", req)
	_ = call.Close()
}

// selectUnitExcluding selects the next candidate unit from the pool, excluding
// any unit whose ID appears in excludeIDs. This is used by the stream-open
// candidate loop to skip units that already failed in the current dispatch.
// It applies the same token-plan hard filter as selectUnit. On-demand
// resolution is never attempted here — the candidate loop only considers pool
// units, so failover stays within the aggregator's configured pool.
func (a *Actor) selectUnitExcluding(req SelectRequest, excludeIDs map[string]bool) (*CallableUnit, error) {
	pool := a.chatUnits()
	var matched []CallableUnit
	now := time.Now()
	for _, u := range matchUnits(req.Unit, pool) {
		if excludeIDs[u.ID] {
			continue
		}
		if a.isSelfAggregatorRef(u) {
			continue
		}
		// An aggregator-ref entry whose child pool is registered unavailable is
		// hard-skipped: the nested dispatch would fail (child pool exhausted)
		// with no chance of success, so skip it like an open failure without
		// consuming quota or sending a request.
		if u.AggregatorID != "" && !llmclient.IsAggregatorAvailable(u.AggregatorID, now) {
			continue
		}
		matched = append(matched, u)
	}
	if len(matched) == 0 {
		return nil, noMatchError(req.Unit)
	}

	tokenFiltered := make([]CallableUnit, 0, len(matched))
	for _, u := range matched {
		if tokenPlanExhausted(u) {
			continue
		}
		tokenFiltered = append(tokenFiltered, u)
	}
	if len(tokenFiltered) == 0 {
		return nil, errUnitsTokenPlanExhausted
	}

	tokenFiltered = preferHealthy(tokenFiltered)
	return a.strategy.Select(req, tokenFiltered)
}

// recoveryModeForHealth maps a health state to a RecoveryMode value surfaced
// via AICallableUnitView.RecoveryMode. Cooling-down units auto-recover when
// the cooldown expires ("cooldown"). Disabled units require explicit recovery
// action ("manual_or_balance_refresh"). Healthy units return "".
func recoveryModeForHealth(state string) string {
	switch state {
	case healthStateCoolingDown:
		return "cooldown"
	case healthStateDisabled:
		return "manual_or_balance_refresh"
	default:
		return ""
	}
}

// tryDispatchUnit resolves a single unit's credentials, acquires the provider
// concurrency gate, and opens a stream. On success it returns the open stream,
// the gate release function, and nil. On any failure it releases the gate
// (when acquired) and returns the error — the caller decides whether to
// failover. This is the per-candidate attempt inside the stream-open candidate
// loop in handleDispatch.
func (a *Actor) tryDispatchUnit(ctx actor.PureContext, unit CallableUnit, req domain.SendSessionMessageReq, streamCtx context.Context) (llmclient.Stream, func(), error) {
	authToken, err := a.resolveUnitToken(unit)
	if err != nil {
		return nil, nil, err
	}

	ctx.Logger().Debug("aiaggregator: dispatch",
		"session", req.SessionID,
		"intent", req.Title,
		"unit", unit.Model,
		"endpoint", unit.Endpoint,
		"provider", unit.ProviderName,
		"protocol", unit.Protocol,
		"hasToken", authToken != "",
	)

	client, err := a.registry.NewClient(unit.Protocol, unit.Endpoint, authToken, unit.Proxy, 0)
	if err != nil {
		return nil, nil, err
	}

	gateSnap := llmclient.DefaultProviderGate.Snapshot()
	usage := gateSnap[unit.ProviderName]
	ctx.Logger().Debug("aiaggregator: dispatch gate.Acquire BEGIN",
		"provider", unit.ProviderName,
		"unit", unit.ID,
		"session", req.SessionID,
		"inflight", usage.Inflight,
		"max", usage.Max,
	)
	release, err := llmclient.DefaultProviderGate.Acquire(streamCtx, unit.ProviderName)
	if err != nil {
		ctx.Logger().Warn("aiaggregator: dispatch gate.Acquire FAILED",
			"provider", unit.ProviderName, "error", err.Error())
		return nil, nil, err
	}
	ctx.Logger().Debug("aiaggregator: dispatch gate.Acquire GRANTED",
		"provider", unit.ProviderName, "unit", unit.ID)

	// Learned text-only unit proactively substitutes its image blocks with
	// recognition text BEFORE the wire request is built (cache-first: repeated
	// images are answered from the LRU without a vision call). Without this,
	// a request carrying new screenshots would ship image blocks, eat a 400,
	// and every image after the first would degrade to the omitted-placeholder
	// — recognition must not be a one-shot per unit (2026-09-12 live trace).
	// Per-attempt copy: rotating to a different unit re-dispatches the original
	// req, so an unmarked vision-capable unit still receives real image blocks.
	if a.unitNeedsImageSubstitution(unit) && requestHasImageBlocks(req) {
		req, _ = a.substituteImagesForTextOnlyRetry(ctx, req)
	}

	stream, err := client.Stream(streamCtx, a.buildLLMRequest(unit, req))
	if err != nil {
		release()
		return nil, nil, err
	}
	return stream, release, nil
}
