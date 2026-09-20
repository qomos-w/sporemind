import { describe, it, expect } from 'vitest'

import type { WorkbenchCardState } from '../../gen-clients/system/types'
import {
  projectionAttribution,
  projectionCardId,
  projectionOnlyDescriptor,
  withProjection,
  workbenchCardKind,
} from './projectionBoard'
import type { WorkbenchCardDescriptor } from './cardTypes'

function state(overrides: Partial<WorkbenchCardState> = {}): WorkbenchCardState {
  return { Id: 'app:x', Kind: 'app', Title: 'X', Icon: '', Score: 20, Slot: 'side', Pinned: false, ...overrides }
}

describe('projectionCardId', () => {
  it('maps the terminal card to the descriptor id by kind', () => {
    expect(projectionCardId(state({ Id: 'terminal', Kind: 'terminal' }))).toBe('terminal')
  })

  it('keeps app and agent ids as-is', () => {
    expect(projectionCardId(state({ Id: 'app:notes' }))).toBe('app:notes')
    expect(projectionCardId(state({ Id: 'agent:abc', Kind: 'chat' }))).toBe('agent:abc')
  })
})

describe('workbenchCardKind', () => {
  it('passes known kinds through and falls back to note', () => {
    expect(workbenchCardKind('chat')).toBe('chat')
    expect(workbenchCardKind('made-up')).toBe('note')
  })
})

describe('projectionAttribution', () => {
  it('formats the score and why', () => {
    expect(projectionAttribution(state({ Score: 12.4, Why: 'attention' }))).toBe('12 · attention')
    expect(projectionAttribution(state({ Score: 30 }))).toBe('30')
  })
})

describe('withProjection', () => {
  it('overlays the actor attention while keeping descriptor presentation', () => {
    const base: WorkbenchCardDescriptor = {
      id: 'app:x',
      kind: 'app',
      title: 'X',
      icon: 'i',
      score: 10,
      pinned: false,
      compactMeta: { statusText: 's' },
      render: () => null,
    }
    const merged = withProjection(base, state({ Score: 33, Pinned: true, Why: '点击' }))
    expect(merged.score).toBe(33)
    expect(merged.pinned).toBe(true)
    expect(merged.attribution).toBe('33 · 点击')
    expect(merged.title).toBe('X')
    expect(merged.render).toBe(base.render)
  })
})

describe('projectionOnlyDescriptor', () => {
  it('synthesizes a descriptor for a projected card with no render body', () => {
    const descriptor = projectionOnlyDescriptor(
      state({ Id: 'agent:abc', Kind: 'chat', Title: 'Ada', Icon: 'bot', Score: 12 }),
    )
    expect(descriptor.id).toBe('agent:abc')
    expect(descriptor.kind).toBe('chat')
    expect(descriptor.title).toBe('Ada')
    expect(descriptor.icon).toBe('bot')
    expect(descriptor.score).toBe(12)
    // The expanded body degrades to a status panel, never an empty main slot.
    expect(descriptor.render(false)).toBeNull()
    expect(descriptor.render(true)).toBeTruthy()
  })
})
