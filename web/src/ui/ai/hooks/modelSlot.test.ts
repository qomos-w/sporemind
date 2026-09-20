import { describe, it, expect } from 'vitest'
import {
  pinnedUnitSlot,
  aggregatorSlot,
  slotToSelection,
  selectionToSlot,
  slotToRoute,
  slotToUnit,
  unitToSlot,
  SYSTEM_AGGREGATOR_ID,
} from './modelSlot'
import type { ModelUnit, ModelSlot } from '../../../gen-clients/system/types'

const unit: ModelUnit = { model: 'gpt-5', provider: 'openai' }
const nakedUnit: ModelUnit = { model: 'gpt-5', provider: '' }

describe('pinnedUnitSlot — soft pin with auto fallback tail', () => {
  it('pins a system-served unit (no AggregatorID) with an auto fallback tail', () => {
    const slot = pinnedUnitSlot(unit)
    expect(slot.Candidates).toEqual([
      { kind: 'unit', Unit: unit },
      { kind: 'auto' },
    ])
  })

  it('records the named aggregator as the serving aggregator on the unit candidate', () => {
    const slot = pinnedUnitSlot(unit, 'named-agg')
    expect(slot.Candidates).toEqual([
      { kind: 'unit', Unit: unit, AggregatorID: 'named-agg' },
      { kind: 'auto' },
    ])
  })

  it('normalizes SYSTEM_AGGREGATOR_ID to a system-served unit (no AggregatorID)', () => {
    const slot = pinnedUnitSlot(unit, SYSTEM_AGGREGATOR_ID)
    expect(slot.Candidates[0]).toEqual({ kind: 'unit', Unit: unit })
    expect(slot.Candidates[0]!.AggregatorID).toBeUndefined()
    expect(slot.Candidates[1]).toEqual({ kind: 'auto' })
  })

  // The auto tail is the whole point: it makes isUnitLockedSlot (backend)
  // return false so UnitPinned stays false and the serving aggregator keeps
  // failover — required for a unit that lives in a nested child aggregator.
  it('always carries a non-unit fallback candidate (not unit-locked)', () => {
    expect(pinnedUnitSlot(unit).Candidates.some(c => c.kind !== 'unit')).toBe(true)
    expect(pinnedUnitSlot(unit, 'named-agg').Candidates.some(c => c.kind !== 'unit')).toBe(true)
  })
})

describe('slotToSelection — round-trips serving aggregator', () => {
  it('reads the serving AggregatorID off a unit candidate', () => {
    const slot = pinnedUnitSlot(unit, 'named-agg')
    expect(slotToSelection(slot)).toEqual({ type: 'unit', unit, aggregatorId: 'named-agg' })
  })

  it('omits aggregatorId for a system-served unit', () => {
    const slot = pinnedUnitSlot(unit)
    expect(slotToSelection(slot)).toEqual({ type: 'unit', unit })
  })
})

describe('selectionToSlot — writes serving aggregator', () => {
  it('writes AggregatorID onto the unit candidate when aggregatorId is set', () => {
    const slot = selectionToSlot({ type: 'unit', unit, aggregatorId: 'named-agg' })
    expect(slot!.Candidates).toEqual([{ kind: 'unit', Unit: unit, AggregatorID: 'named-agg' }])
  })

  it('writes a plain unit candidate when aggregatorId is absent', () => {
    const slot = selectionToSlot({ type: 'unit', unit })
    expect(slot!.Candidates).toEqual([{ kind: 'unit', Unit: unit }])
  })
})

describe('slotToRoute — serving aggregator projection', () => {
  it('projects servingAggregatorId from a unit candidate carrying AggregatorID', () => {
    const route = slotToRoute(pinnedUnitSlot(unit, 'named-agg'))
    expect(route).toEqual({ kind: 'unit', unit, servingAggregatorId: 'named-agg', failover: 'auto' })
  })

  it('defaults servingAggregatorId to system for a plain unit candidate', () => {
    const route = slotToRoute(pinnedUnitSlot(unit))
    expect(route).toEqual({ kind: 'unit', unit, servingAggregatorId: SYSTEM_AGGREGATOR_ID, failover: 'auto' })
  })
})

describe('aggregatorSlot — soft-pin release semantics', () => {
  it('selecting the root aggregator yields a pure aggregator slot with no pinned unit', () => {
    const slot = aggregatorSlot('named-agg')
    expect(slot.Candidates).toEqual([{ kind: 'aggregator', AggregatorID: 'named-agg' }])
    // The released slot projects as an aggregator route — the unit pin is gone.
    expect(slotToRoute(slot)).toEqual({ kind: 'aggregator', aggregatorId: 'named-agg' })
    expect(slotToUnit(slot)).toBeUndefined()
  })
})

describe('Unit invariant — provider required for unit-kind', () => {
  const nakedSlot: ModelSlot = { Candidates: [{ kind: 'unit', Unit: nakedUnit }] }

  it('slotToSelection skips a unit candidate without provider', () => {
    expect(slotToSelection(nakedSlot)).toBeUndefined()
  })

  it('slotToRoute falls back to auto for a unit candidate without provider', () => {
    expect(slotToRoute(nakedSlot)).toEqual({ kind: 'auto' })
  })

  it('slotToUnit returns undefined for a unit candidate without provider', () => {
    expect(slotToUnit(nakedSlot)).toBeUndefined()
  })

  it('unitToSlot returns undefined for a unit without provider', () => {
    expect(unitToSlot(nakedUnit)).toBeUndefined()
  })

  it('unitToSlot returns undefined for a unit without model', () => {
    expect(unitToSlot({ model: '', provider: 'openai' })).toBeUndefined()
  })
})
