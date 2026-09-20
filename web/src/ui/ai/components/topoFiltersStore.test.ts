import { describe, expect, it } from 'vitest'
import { topoFiltersStore } from './topoFiltersStore'

describe('topoFiltersStore', () => {
  it('adds a property-based custom filter and returns its id', () => {
    const id = topoFiltersStore.add('status', 'blocked')
    expect(id).toBeTruthy()
    expect(topoFiltersStore.getState()).toContainEqual({ id, field: 'status', value: 'blocked' })
    topoFiltersStore.remove(id)
  })

  it('updates an existing filter in place', () => {
    const id = topoFiltersStore.add('tag', 'sprint-1')
    topoFiltersStore.update(id, 'tag', 'sprint-2')
    expect(topoFiltersStore.getState()).toContainEqual({ id, field: 'tag', value: 'sprint-2' })
    topoFiltersStore.remove(id)
  })

  it('removes a filter by id', () => {
    const id = topoFiltersStore.add('type', 'agent')
    expect(topoFiltersStore.getState().some(f => f.id === id)).toBe(true)
    topoFiltersStore.remove(id)
    expect(topoFiltersStore.getState().some(f => f.id === id)).toBe(false)
  })

  it('notifies subscribers on mutation', () => {
    let calls = 0
    const unsub = topoFiltersStore.subscribe(() => { calls++ })
    const id = topoFiltersStore.add('source', 'project')
    topoFiltersStore.update(id, 'source', 'cardstore')
    topoFiltersStore.remove(id)
    expect(calls).toBe(3)
    unsub()
  })
})
