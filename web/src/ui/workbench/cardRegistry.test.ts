import { describe, it, expect, vi } from 'vitest'
import { createWorkbenchCardRegistry } from './cardRegistry'
import type { WorkbenchCardDescriptor } from './cardTypes'

function descriptor(id: string): WorkbenchCardDescriptor {
  return {
    id,
    kind: 'note',
    title: id,
    icon: 'file-text',
    score: 1,
    pinned: false,
    compactMeta: { statusText: 'idle' },
    render: () => null,
  }
}

describe('WorkbenchCardRegistry', () => {
  it('registers, reads and unregisters descriptors by id', () => {
    const registry = createWorkbenchCardRegistry()
    registry.register(descriptor('a'))
    registry.register(descriptor('b'))
    expect(registry.list().map(c => c.id)).toEqual(['a', 'b'])
    expect(registry.get('a')?.title).toBe('a')

    registry.unregister('a')
    expect(registry.list().map(c => c.id)).toEqual(['b'])
  })

  it('replaces an existing descriptor in place (plugin reload)', () => {
    const registry = createWorkbenchCardRegistry()
    registry.register(descriptor('a'))
    registry.register({ ...descriptor('a'), title: 'reloaded' })
    expect(registry.list()).toHaveLength(1)
    expect(registry.get('a')?.title).toBe('reloaded')
  })

  it('keeps a stable snapshot identity until the set changes', () => {
    const registry = createWorkbenchCardRegistry()
    registry.register(descriptor('a'))
    const first = registry.getSnapshot()
    expect(registry.getSnapshot()).toBe(first)
    registry.register(descriptor('b'))
    expect(registry.getSnapshot()).not.toBe(first)
  })

  it('notifies subscribers and stops after unsubscribe', () => {
    const registry = createWorkbenchCardRegistry()
    const listener = vi.fn()
    const off = registry.subscribe(listener)
    registry.register(descriptor('a'))
    expect(listener).toHaveBeenCalledTimes(1)
    off()
    registry.register(descriptor('b'))
    expect(listener).toHaveBeenCalledTimes(1)
  })

  it('ignores empty ids and no-op unregisters', () => {
    const registry = createWorkbenchCardRegistry()
    const listener = vi.fn()
    registry.subscribe(listener)
    registry.register({ ...descriptor(''), id: '' })
    registry.unregister('missing')
    expect(registry.list()).toEqual([])
    expect(listener).not.toHaveBeenCalled()
  })
})
