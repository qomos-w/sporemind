import type { ModelUnit, ModelSlot, ModelRef } from '../../../gen-clients/system/types'

/** Config ID reserved for the system/auto aggregator (mirrors backend `systemAggID`). */
export const SYSTEM_AGGREGATOR_ID = 'system'

/**
 * A user-facing slot selection: either delegate the slot to a named aggregator,
 * or pin a concrete model unit. When the unit was picked from a named
 * aggregator's pool, `aggregatorId` records WHICH aggregator serves it (so the
 * unit dispatches through that aggregator, not the system one); absent means
 * the system aggregator serves it. Empty (undefined) means [auto] — the
 * system aggregator picks.
 *
 * The ModelRef `kind` is derived from this, never shown to the user.
 *
 * NOTE: `aggregatorId` is the aggregator's CONFIG ID (AggregatorDescriptor.Id),
 * NOT its actor ID. The backend resolves ModelRef.AggregatorID against the
 * aimanager config-ID-keyed aggregator cache, so storing the actor ID here
 * would make aggregator-kind slots unresolvable.
 */
export type SlotSelection =
  | { type: 'aggregator'; aggregatorId: string }
  | { type: 'unit'; unit: ModelUnit; aggregatorId?: string }

/** Extract the first effective candidate of a slot as a user-facing selection. */
export function slotToSelection(slot?: ModelSlot): SlotSelection | undefined {
  if (!slot || !slot.Candidates || slot.Candidates.length === 0) return undefined
  for (const c of slot.Candidates) {
    if (c.kind === 'aggregator' && c.AggregatorID) return { type: 'aggregator', aggregatorId: c.AggregatorID }
    if (c.kind === 'unit' && c.Unit && c.Unit.model && c.Unit.provider) {
      const sel: SlotSelection = { type: 'unit', unit: c.Unit }
      if (c.AggregatorID) sel.aggregatorId = c.AggregatorID
      return sel
    }
  }
  return undefined
}

/** Backwards-compatible: the first unit-kind candidate's ModelUnit (used by
 *  callers that only care about a pinned model, e.g. compaction summary). */
export function slotToUnit(slot?: ModelSlot): ModelUnit | undefined {
  if (!slot || !slot.Candidates || slot.Candidates.length === 0) return undefined
  for (const c of slot.Candidates) {
    if (c.Unit && c.Unit.model && c.Unit.provider) return c.Unit
  }
  return undefined
}

/** Build a slot from a user-facing selection.
 *  - undefined / null: return undefined (empty → [auto] at read time)
 *  - aggregator: { Candidates: [{ kind: 'aggregator', AggregatorID }] }
 *  - unit: { Candidates: [{ kind: 'unit', Unit, AggregatorID? }] } — served by
 *    the named aggregator when aggregatorId is set, else the system aggregator. */
export function selectionToSlot(sel: SlotSelection | null | undefined): ModelSlot | undefined {
  if (!sel) return undefined
  if (sel.type === 'aggregator') {
    return { Candidates: [{ kind: 'aggregator', AggregatorID: sel.aggregatorId }] }
  }
  const cand: ModelRef = { kind: 'unit', Unit: sel.unit }
  if (sel.aggregatorId) cand.AggregatorID = sel.aggregatorId
  return { Candidates: [cand] }
}

/** Build a slot wrapping a single unit-kind candidate (legacy helper for
 *  compaction SummaryUnit override, which is unit-only). */
export function unitToSlot(u: ModelUnit | null | undefined): ModelSlot | undefined {
  if (!u || !u.model || !u.provider) return undefined
  return { Candidates: [{ kind: 'unit', Unit: u }] }
}

// ── Route model (top-bar model selector) ──

/**
 * The active routing form of a slot, as the top-bar selector understands it:
 *  - auto:        [auto] — system aggregator picks (no pinned unit)
 *  - aggregator:  [aggregator Y] — route to a named aggregator's strategy
 *  - unit:        [unit X @ agg, <failover>] — pin X served by
 *                 `servingAggregatorId` (the system aggregator when the unit
 *                 carries no AggregatorID); when X is unavailable the failover
 *                 aggregator (or auto) takes over
 */
export type SlotRoute =
  | { kind: 'auto' }
  | { kind: 'aggregator'; aggregatorId: string }
  | { kind: 'unit'; unit: ModelUnit; servingAggregatorId: string; failover: 'auto' | string }

/** Project a slot into its active route for top-bar display/highlight. */
export function slotToRoute(slot?: ModelSlot): SlotRoute {
  if (!slot?.Candidates?.length) return { kind: 'auto' }
  const head = slot.Candidates[0]!
  if (head.kind === 'aggregator') {
    return { kind: 'aggregator', aggregatorId: head.AggregatorID || SYSTEM_AGGREGATOR_ID }
  }
  if (head.kind === 'unit' && head.Unit && head.Unit.model && head.Unit.provider) {
    let failover: 'auto' | string = 'auto'
    for (let i = 1; i < slot.Candidates.length; i++) {
      const c = slot.Candidates[i]!
      if (c.kind === 'aggregator' && c.AggregatorID) {
        failover = c.AggregatorID
        break
      }
      if (c.kind === 'auto') {
        failover = 'auto'
        break
      }
    }
    return { kind: 'unit', unit: head.Unit, servingAggregatorId: head.AggregatorID || SYSTEM_AGGREGATOR_ID, failover }
  }
  return { kind: 'auto' }
}

/** [auto] — pure system-aggregator routing. */
export function autoSlot(): ModelSlot {
  return { Candidates: [{ kind: 'auto' }] }
}

/** [aggregator Y] — route to a named aggregator (config ID). */
export function aggregatorSlot(aggregatorId: string): ModelSlot {
  return { Candidates: [{ kind: 'aggregator', AggregatorID: aggregatorId }] }
}

/**
 * [unit X @ agg, auto] — soft-pin concrete unit X served by
 * `servingAggregatorId` (the system aggregator when omitted or
 * SYSTEM_AGGREGATOR_ID), with an [auto] fallback tail.
 *
 * The auto tail makes this a SOFT pin: isUnitLockedSlot returns false, so the
 * dispatch carries UnitPinned=false and the serving aggregator keeps full
 * failover. This is required for a unit picked from a NESTED child aggregator:
 * the unit is absent from the serving (parent) aggregator's own pool, so the
 * parent must fall back to auto-selection (which includes the child as an
 * aggregator-ref) to reach the nested forwarding path. A hard pin (no tail)
 * sets UnitPinned=true and surfaces "no callable unit for model" even when the
 * unit is healthy in the child — the parent's matchUnits excludes
 * aggregator-ref entries under a pinned request.
 *
 * The named aggregator is recorded on the unit candidate's AggregatorID (so
 * dispatch serves the unit through it), not as a failover. slotFromUnit remains
 * the hard-pin builder for the system-served compaction-summary override.
 */
export function pinnedUnitSlot(unit: ModelUnit, servingAggregatorId?: string): ModelSlot {
  const agg = servingAggregatorId && servingAggregatorId !== SYSTEM_AGGREGATOR_ID ? servingAggregatorId : ''
  const head: ModelRef = { kind: 'unit', Unit: unit }
  if (agg) head.AggregatorID = agg
  return { Candidates: [head, { kind: 'auto' }] }
}
