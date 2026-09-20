import { describe, expect, it, vi } from 'vitest'
import {
  consumePendingWorkflowLocate,
  requestWorkflowLocate,
  subscribeWorkflowLocate,
  openWorkflowAndLocate,
} from './workflowLocateStore'

describe('workflowLocateStore', () => {
  it('stores a pending request until consumed once', () => {
    requestWorkflowLocate({ mapId: 'map-a' })
    expect(consumePendingWorkflowLocate()).toEqual({ mapId: 'map-a' })
    expect(consumePendingWorkflowLocate()).toBeNull()
  })

  it('notifies subscribers on request and stops after unsubscribe', () => {
    const cb = vi.fn()
    const unsubscribe = subscribeWorkflowLocate(cb)
    requestWorkflowLocate({ mapId: 'map-b' })
    expect(cb).toHaveBeenCalledTimes(1)
    unsubscribe()
    requestWorkflowLocate({ mapId: 'map-c' })
    expect(cb).toHaveBeenCalledTimes(1)
    // Clean up the pending request from this test.
    consumePendingWorkflowLocate()
  })

  it('keeps only the latest request', () => {
    requestWorkflowLocate({ mapId: 'map-1' })
    requestWorkflowLocate({ mapId: 'map-2' })
    expect(consumePendingWorkflowLocate()).toEqual({ mapId: 'map-2' })
    expect(consumePendingWorkflowLocate()).toBeNull()
  })

  it('preserves the selectStart flag through the store', () => {
    requestWorkflowLocate({ mapId: 'root', selectStart: true })
    expect(consumePendingWorkflowLocate()).toEqual({ mapId: 'root', selectStart: true })
  })

  it('openWorkflowAndLocate requests a selectStart locate for a known map', () => {
    const handler = vi.fn()
    window.addEventListener('sporemind:set-shell-content-mode', handler)
    try {
      openWorkflowAndLocate('root')
      expect(handler).toHaveBeenCalledTimes(1)
      expect(consumePendingWorkflowLocate()).toEqual({ mapId: 'root', selectStart: true })
    } finally {
      window.removeEventListener('sporemind:set-shell-content-mode', handler)
    }
  })

  it('openWorkflowAndLocate without a mapId opens the mode but requests no locate', () => {
    const handler = vi.fn()
    window.addEventListener('sporemind:set-shell-content-mode', handler)
    try {
      openWorkflowAndLocate(undefined)
      expect(handler).toHaveBeenCalledTimes(1)
      expect(consumePendingWorkflowLocate()).toBeNull()
    } finally {
      window.removeEventListener('sporemind:set-shell-content-mode', handler)
    }
  })
})
