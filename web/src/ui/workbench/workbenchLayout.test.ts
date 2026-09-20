import { describe, expect, it } from 'vitest'
import { selectWorkbenchLayout } from './workbenchLayout'
import type { WorkbenchCardState } from '../../gen-clients/system/types'

function card(id: string, slot: string): WorkbenchCardState {
  return { Id: id, Kind: 'app', Title: id, Icon: '', Score: 10, Slot: slot, Pinned: false }
}

describe('selectWorkbenchLayout', () => {
  it('groups cards by their assigned slot', () => {
    const layout = selectWorkbenchLayout([
      card('a', 'main'),
      card('b', 'side'),
      card('c', 'side'),
      card('t', 'term'),
      card('r', 'rail'),
      card('h', 'hidden'),
    ])
    expect(layout.main?.Id).toBe('a')
    expect(layout.sides.map(c => c.Id)).toEqual(['b', 'c'])
    expect(layout.terminal?.Id).toBe('t')
    expect(layout.railed.map(c => c.Id)).toEqual(['r'])
    expect(layout.hidden.map(c => c.Id)).toEqual(['h'])
  })

  it('demotes duplicate main/term cards to the side band', () => {
    const layout = selectWorkbenchLayout([
      card('a', 'main'),
      card('b', 'main'),
      card('t', 'term'),
      card('u', 'term'),
    ])
    expect(layout.main?.Id).toBe('a')
    expect(layout.terminal?.Id).toBe('t')
    expect(layout.sides.map(c => c.Id)).toEqual(['b', 'u'])
  })

  it('treats an unknown slot as a side card', () => {
    const layout = selectWorkbenchLayout([card('x', 'mystery')])
    expect(layout.sides.map(c => c.Id)).toEqual(['x'])
    expect(layout.main).toBeNull()
    expect(layout.terminal).toBeNull()
  })
})
