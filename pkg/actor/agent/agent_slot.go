package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// systemAggID is the config ID of the auto/system aggregator spawned by
// aimanager (mirrors aimanager.autoAggregatorID = "system").
const (
	systemAggID                = "system"
	aggregatorDiscoveryTimeout = 250 * time.Millisecond
	// minAggRefRefreshInterval throttles cache-miss driven refreshes of the
	// aggregator ref cache. Explicit refreshes (config apply, startup) bypass
	// the throttle by calling refreshAggRefs directly.
	minAggRefRefreshInterval = time.Second
)

const (
	modelRefKindUnit       = "unit"
	modelRefKindAggregator = "aggregator"
	modelRefKindAuto       = "auto"
)

// dispatchTarget is one resolved candidate for an LLM dispatch: the aggregator
// actor ref to call, plus an optional concrete unit. An empty unit means the
// aggregator's own pool strategy (fast random pick) selects the model.
type dispatchTarget struct {
	aggRef ref.Ref
	unit   domain.ModelUnit
}

// refreshAggRefs discovers all aggregator actor refs via the aimanager and
// caches them keyed by aggregator config ID ("system" or a named id). Safe to
// call repeatedly; idempotent. When the aimanager is not yet available the
// cache is left untouched and callers should retry later.
func (a *Actor) refreshAggRefs(ctx actor.PureContext) {
	aimgrRef := a.findAimanagerRef(ctx)
	if aimgrRef == nil {
		return
	}
	planner := ctx.Planner()
	if planner == nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), aggregatorDiscoveryTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, aimgrRef, "aimanager.aggregator_list", nil).Await()
	if err != nil || result == nil {
		return
	}
	var resp domain.AggregatorDescriptorListResp
	switch v := result.(type) {
	case domain.AggregatorDescriptorListResp:
		resp = v
	default:
		body, _ := json.Marshal(result)
		if err := json.Unmarshal(body, &resp); err != nil {
			return
		}
	}
	cache := make(map[string]ref.Ref, len(resp.Items))
	for _, d := range resp.Items {
		if d.ActorID == "" {
			continue
		}
		cid, err := identity.ParseCanonicalID(d.ActorID)
		if err != nil {
			continue
		}
		if r, ok := ctx.LookupID(id.From(cid)); ok {
			cache[d.ID] = r
		}
	}
	a.aggRefMu.Lock()
	a.aggRefCache = cache
	a.aggRefMu.Unlock()
}

// findAimanagerRef locates the aimanager actor via the topology snapshot.
func (a *Actor) findAimanagerRef(ctx actor.PureContext) ref.Ref {
	if a.topo == nil {
		return nil
	}
	for _, node := range a.topo.Snapshot() {
		if node.Kind != "aimanager" {
			continue
		}
		cid, err := identity.ParseCanonicalID(node.ID)
		if err != nil {
			continue
		}
		if r, ok := ctx.LookupID(id.From(cid)); ok {
			return r
		}
	}
	return nil
}

// cachedAggRef returns the cached ref for a config ID, refreshing once if the
// cache is empty or lacks the requested ID. Returns nil when the aggregator
// cannot be resolved.
func (a *Actor) cachedAggRef(ctx actor.PureContext, configID string) ref.Ref {
	a.aggRefMu.RLock()
	r := a.aggRefCache[configID]
	a.aggRefMu.RUnlock()
	if r != nil {
		return r
	}
	// Cache miss (empty or stale): throttled refresh and re-check. A refresh
	// that succeeds but still lacks the id must not re-invoke
	// aimanager.aggregator_list on the next miss.
	a.refreshAggRefsThrottled(ctx)
	a.aggRefMu.RLock()
	defer a.aggRefMu.RUnlock()
	return a.aggRefCache[configID]
}

// refreshAggRefsThrottled runs refreshAggRefs at most once per
// minAggRefRefreshInterval; refreshes outside the interval are skipped
// (the cache keeps its previous contents).
func (a *Actor) refreshAggRefsThrottled(ctx actor.PureContext) {
	a.aggRefMu.Lock()
	if time.Since(a.aggRefRefreshAt) < minAggRefRefreshInterval {
		a.aggRefMu.Unlock()
		return
	}
	a.aggRefRefreshAt = time.Now()
	a.aggRefMu.Unlock()
	a.refreshAggRefs(ctx)
}

func (a *Actor) aggRefCacheEmpty() bool {
	a.aggRefMu.RLock()
	defer a.aggRefMu.RUnlock()
	return len(a.aggRefCache) == 0
}

// effectiveCandidates returns the slot's candidate list, treating an empty slot
// as [auto] (system aggregator fast-pick).
func effectiveCandidates(slot domain.ModelSlot) []domain.ModelRef {
	if len(slot.Candidates) == 0 {
		return []domain.ModelRef{{Kind: modelRefKindAuto}}
	}
	return slot.Candidates
}

// resolveTarget resolves the first available candidate of a slot into a
// dispatch target. Returns an error when no candidate can be resolved.
func (a *Actor) resolveTarget(ctx actor.Context, slot domain.ModelSlot) (ref.Ref, domain.ModelUnit, error) {
	targets := a.resolveTargets(ctx, slot)
	if len(targets) == 0 {
		return nil, domain.ModelUnit{}, fmt.Errorf("agent: no model target available for slot")
	}
	return targets[0].aggRef, targets[0].unit, nil
}

// resolveTargets resolves every available candidate of a slot into dispatch
// targets, preserving fallback order. Candidates whose aggregator is not yet
// resolvable are skipped.
func (a *Actor) resolveTargets(ctx actor.PureContext, slot domain.ModelSlot) []dispatchTarget {
	var out []dispatchTarget
	for _, cand := range effectiveCandidates(slot) {
		if t, ok := a.resolveCandidate(ctx, cand); ok {
			out = append(out, t)
		}
	}
	return out
}

// resolveCandidate maps a single ModelRef to a dispatch target.
func (a *Actor) resolveCandidate(ctx actor.PureContext, r domain.ModelRef) (dispatchTarget, bool) {
	switch r.Kind {
	case modelRefKindUnit:
		if r.Unit == nil || r.Unit.Model == "" || r.Unit.Provider == "" {
			return dispatchTarget{}, false
		}
		// A unit candidate may carry an AggregatorID to pin which aggregator
		// serves the concrete model (e.g. a unit picked from a named
		// aggregator's pool). Absent means the system aggregator serves it.
		aggID := r.AggregatorID
		if aggID == "" {
			aggID = systemAggID
		}
		aggRef := a.cachedAggRef(ctx, aggID)
		if aggRef == nil {
			return dispatchTarget{}, false
		}
		return dispatchTarget{aggRef: aggRef, unit: *r.Unit}, true
	case modelRefKindAggregator:
		id := r.AggregatorID
		if id == "" {
			id = systemAggID
		}
		aggRef := a.cachedAggRef(ctx, id)
		if aggRef == nil {
			return dispatchTarget{}, false
		}
		return dispatchTarget{aggRef: aggRef}, true
	case modelRefKindAuto, "":
		aggRef := a.cachedAggRef(ctx, systemAggID)
		if aggRef == nil {
			return dispatchTarget{}, false
		}
		return dispatchTarget{aggRef: aggRef}, true
	default:
		return dispatchTarget{}, false
	}
}

// slotPrimaryAggID reports the aggregator id the slot's first effective
// candidate resolves through. Unit/auto candidates default to the system
// aggregator; aggregator candidates carry their own id.
func slotPrimaryAggID(slot domain.ModelSlot) string {
	cands := effectiveCandidates(slot)
	if len(cands) == 0 {
		return systemAggID
	}
	c := cands[0]
	if c.Kind == modelRefKindAggregator || c.Kind == modelRefKindUnit {
		if c.AggregatorID != "" {
			return c.AggregatorID
		}
	}
	return systemAggID
}

// slotFirstUnit returns the concrete unit of the first unit-kind candidate in a
// slot, or the zero value when none exists. Used where a single concrete unit
// is needed without dispatching and without aggregator lookups (e.g. pure
// functions, test assertions, NewChildActor initial status). For aggregator/auto
// candidates the unit is the aggregator's internal routing decision and cannot
// be known without dispatching — this function intentionally does not query
// aggregators.
func slotFirstUnit(slot domain.ModelSlot) domain.ModelUnit {
	for _, r := range effectiveCandidates(slot) {
		if r.Kind == modelRefKindUnit && r.Unit != nil && r.Unit.Model != "" && r.Unit.Provider != "" {
			return *r.Unit
		}
	}
	return domain.ModelUnit{}
}

// slotFirstUnitResolved traverses the candidate chain and resolves the first
// concrete unit, including aggregator/auto candidates. For unit-kind
// candidates the unit is returned directly. For aggregator/auto candidates the
// aggregator's status is queried to find the first healthy, non-cooled-down
// pooled unit. This is the tree-walking variant of slotFirstUnit: it walks the
// flat candidate list and, for each aggregator/auto candidate, expands it by
// querying the aggregator actor for its pooled units. Returns the zero value
// when no unit can be resolved (all candidates are unit-kind with empty
// models, or all aggregator/auto candidates have no available units).
//
// This function is used where a concrete unit projection is needed without
// triggering a dispatch — e.g. status projection, turn startup, thinking-level
// lookup. It does NOT pin the unit for dispatch; the turn engine's
// resolveTargets/resolveCandidate path still runs at dispatch time to
// re-resolve with live health and pool strategy.
func (a *Actor) slotFirstUnitResolved(ctx actor.PureContext, slot domain.ModelSlot) domain.ModelUnit {
	for _, r := range effectiveCandidates(slot) {
		switch r.Kind {
		case modelRefKindUnit:
			if r.Unit != nil && r.Unit.Model != "" && r.Unit.Provider != "" {
				return *r.Unit
			}
		case modelRefKindAggregator, modelRefKindAuto, "":
			aggID := r.AggregatorID
			if aggID == "" {
				aggID = systemAggID
			}
			if u, ok := a.firstAggregatorUnit(ctx, aggID); ok {
				return u
			}
		}
	}
	return domain.ModelUnit{}
}

// firstAggregatorUnit queries an aggregator's status and returns the first
// healthy, non-disabled, non-cooled-down pooled unit. Returns false when the
// aggregator cannot be reached or has no available units.
func (a *Actor) firstAggregatorUnit(ctx actor.PureContext, aggID string) (domain.ModelUnit, bool) {
	aggRef := a.cachedAggRef(ctx, aggID)
	if aggRef == nil {
		return domain.ModelUnit{}, false
	}
	planner := ctx.Planner()
	if planner == nil {
		return domain.ModelUnit{}, false
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), aggregatorDiscoveryTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, aggRef, "aiaggregator.status", nil).Await()
	if err != nil || result == nil {
		return domain.ModelUnit{}, false
	}
	var status gen.AIAggregatorStatusResp
	switch v := result.(type) {
	case gen.AIAggregatorStatusResp:
		status = v
	default:
		body, _ := json.Marshal(result)
		if err := json.Unmarshal(body, &status); err != nil {
			return domain.ModelUnit{}, false
		}
	}
	now := time.Now().Unix()
	for _, u := range status.Units {
		if u.Model == "" || u.ProviderName == "" {
			continue
		}
		if u.HealthState == "disabled" {
			continue
		}
		if u.HealthState == "cooling_down" && u.CooldownUntil > now {
			continue
		}
		return domain.ModelUnit{Model: u.Model, Provider: u.ProviderName}, true
	}
	return domain.ModelUnit{}, false
}

// slotIsEmpty reports whether a slot has no configured candidates (i.e. [auto]).
func slotIsEmpty(slot domain.ModelSlot) bool {
	return len(slot.Candidates) == 0
}

// slotFromUnit builds a hard-locked slot from a concrete unit: the unit is the
// sole candidate, no aggregator/auto fallback. This pins the model so dispatch
// surfaces failures instead of rotating. An empty unit yields an empty slot
// (equivalent to [auto]).
func slotFromUnit(u domain.ModelUnit) domain.ModelSlot {
	if u.Model == "" || u.Provider == "" {
		return domain.ModelSlot{}
	}
	return domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &u},
	}}
}

// slotPtr returns a pointer to the slot, or nil for an empty slot so the
// frontend treats absence as the default [auto].
func slotPtr(slot domain.ModelSlot) *gen.ModelSlot {
	if slotIsEmpty(slot) {
		return nil
	}
	v := gen.ModelSlot(slot)
	return &v
}

// isUnitLockedSlot reports whether every effective candidate of the slot pins a
// concrete model unit (unit-kind). Such a slot has no aggregator fallback, so
// its only recovery path on a transient failure is to retry the same endpoint.
// An empty slot ([auto]) is aggregator-backed, not locked.
func isUnitLockedSlot(slot domain.ModelSlot) bool {
	cands := effectiveCandidates(slot)
	if len(cands) == 0 {
		return false
	}
	for _, c := range cands {
		if c.Kind != modelRefKindUnit || c.Unit == nil || c.Unit.Model == "" || c.Unit.Provider == "" {
			return false
		}
	}
	return true
}

// routeChainMaxUnits caps the number of unit-kind candidates recorded in a
// route chain. Older entries are evicted from the tail of the unit segment;
// the aggregator/auto fallback tail is always preserved as the last
// candidate so dispatch can degrade when every recorded unit is uncallable.
const routeChainMaxUnits = 3

// unitCandidateRef reports whether a candidate is a valid unit pin (unit kind
// with a full model+provider), independent of any aggregator/auto fallback.
func unitCandidateRef(c domain.ModelRef) bool {
	return c.Kind == modelRefKindUnit && c.Unit != nil && c.Unit.Model != "" && c.Unit.Provider != ""
}

// sameRouteUnit reports whether a candidate pins the given (model, provider).
func sameRouteUnit(c domain.ModelRef, u domain.ModelUnit) bool {
	return unitCandidateRef(c) && c.Unit.Model == u.Model && c.Unit.Provider == u.Provider
}

// promoteRouteUnit moves a resolved unit to the head of the route chain so the
// next dispatch prefers the unit that actually executed. Only acts when the
// slot already contains a unit-kind candidate matching the resolved unit
// (hoisting it to the front). When the slot is aggregator-backed or [auto],
// the resolved unit is the aggregator's internal routing decision — the slot
// must not capture it, or the aggregator's pool strategy is bypassed. The chain
// is capped at routeChainMaxUnits unit candidates. Returns the updated slot and
// whether the chain actually changed.
func promoteRouteUnit(slot domain.ModelSlot, unit domain.ModelUnit) (domain.ModelSlot, bool) {
	if unit.Model == "" || unit.Provider == "" {
		return slot, false
	}
	cands := effectiveCandidates(slot)
	if len(cands) > 0 && sameRouteUnit(cands[0], unit) {
		return slot, false
	}
	// Only hoist an existing matching unit candidate; never create a new one.
	// A resolved unit from an aggregator/auto slot is the aggregator's
	// internal routing — recording it as a head unit would bypass the
	// aggregator's pool strategy on the next dispatch.
	found := -1
	for i, c := range cands {
		if sameRouteUnit(c, unit) {
			found = i
			break
		}
	}
	if found < 0 {
		return slot, false
	}
	head := cands[found]
	rest := make([]domain.ModelRef, 0, len(cands)-1)
	rest = append(rest, cands[:found]...)
	rest = append(rest, cands[found+1:]...)
	out := make([]domain.ModelRef, 0, len(rest)+1)
	out = append(out, head)
	for _, c := range rest {
		if routeChainHeadUnits(out) >= routeChainMaxUnits && unitCandidateRef(c) {
			continue
		}
		out = append(out, c)
	}
	if slotEqual(cands, out) {
		return slot, false
	}
	return domain.ModelSlot{Candidates: out}, true
}

// routeChainHeadUnits counts the leading unit-kind candidates in the chain.
func routeChainHeadUnits(cands []domain.ModelRef) int {
	n := 0
	for _, c := range cands {
		if !unitCandidateRef(c) {
			break
		}
		n++
	}
	return n
}

// demoteRouteUnit moves a unit that failed to open its stream behind the other
// unit candidates (just before the aggregator/auto fallback tail), so the next
// dispatch prefers units that have not just failed while still allowing the
// demoted unit to be retried later once it recovers. A unit not present in the
// chain is a no-op. The chain keeps its fallback tail. Returns the updated slot
// and whether the chain actually changed.
func demoteRouteUnit(slot domain.ModelSlot, unit domain.ModelUnit) (domain.ModelSlot, bool) {
	if unit.Model == "" || unit.Provider == "" {
		return slot, false
	}
	cands := effectiveCandidates(slot)
	idx := -1
	for i, c := range cands {
		if sameRouteUnit(c, unit) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return slot, false
	}
	// Find the tail boundary (first non-unit candidate); the demoted unit goes
	// right before it, after any remaining unit candidates.
	tailAt := len(cands)
	for i, c := range cands {
		if !unitCandidateRef(c) {
			tailAt = i
			break
		}
	}
	if idx == tailAt-1 {
		return slot, false // already last unit in the segment
	}
	demoted := cands[idx]
	out := make([]domain.ModelRef, 0, len(cands))
	out = append(out, cands[:idx]...)
	out = append(out, cands[idx+1:tailAt]...)
	out = append(out, demoted)
	out = append(out, cands[tailAt:]...)
	if slotEqual(cands, out) {
		return slot, false
	}
	return domain.ModelSlot{Candidates: out}, true
}

// recordRouteUnit promotes a successfully dispatched unit to the head of the
// agent's live primary route chain and, on a real change, arms a one-shot
// Primary report so the workspace persists the chain and re-pushes it after
// restart. This records the agent's observed routing: the head unit is the one
// that actually executed most recently, and the auto tail guarantees fallback.
func (a *Actor) recordRouteUnit(ctx actor.Context, unit domain.ModelUnit) bool {
	a.slotMu.Lock()
	updated, changed := promoteRouteUnit(a.primary, unit)
	if changed {
		a.primary = updated
	}
	a.slotMu.Unlock()
	if changed {
		a.armRouteReport(ctx)
	}
	return changed
}

// demoteFailedRouteUnit moves a unit whose stream failed to open behind the
// other unit candidates in the primary route chain, so the next dispatch
// prefers units that have not just failed. Arms the one-shot Primary report on
// a real change so the demotion persists.
func (a *Actor) demoteFailedRouteUnit(ctx actor.Context, unit domain.ModelUnit) bool {
	a.slotMu.Lock()
	updated, changed := demoteRouteUnit(a.primary, unit)
	if changed {
		a.primary = updated
	}
	a.slotMu.Unlock()
	if changed {
		a.armRouteReport(ctx)
	}
	return changed
}

// armRouteReport flags the next workspace status update to carry the current
// primary route chain and fires the update. Called only after a chain mutation.
func (a *Actor) armRouteReport(ctx actor.Context) {
	a.routeReportPending.Store(true)
	a.notifyWorkspaceStatus(ctx)
}

// slotEqual reports whether two candidate lists are identical in order and
// content, used to suppress no-op route-chain writes.
func slotEqual(a, b []domain.ModelRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].AggregatorID != b[i].AggregatorID {
			return false
		}
		if (a[i].Unit == nil) != (b[i].Unit == nil) {
			return false
		}
		if a[i].Unit != nil && *a[i].Unit != *b[i].Unit {
			return false
		}
	}
	return true
}
