import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../application/theme-persist', () => ({
  loadPreference: vi.fn(async (key: string) => prefStore[key]),
  savePreference: vi.fn(async (key: string, value: string) => { prefStore[key] = value }),
}))

import { loadPreference, savePreference } from '../../../application/theme-persist'
import { buildWorkflowFilter, workflowFoldStore } from './workflowFoldStore'

const prefStore: Record<string, string> = {}

describe('workflowFoldStore', () => {
  beforeEach(() => {
    for (const k of Object.keys(prefStore)) delete prefStore[k]
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.resetModules()
  })

  it('loads persisted fold preferences once and exposes them in the snapshot', async () => {
    prefStore['workflowGraph.foldedWorkflows.v1'] = JSON.stringify(['w-1'])
    prefStore['workflowGraph.foldedCategories.v1'] = JSON.stringify(['to-start'])
    prefStore['workflowGraph.archivedExpanded.v1'] = JSON.stringify(['__epic_bucket__:date:2026-08-04'])

    await workflowFoldStore.ensureLoaded()
    const snap = workflowFoldStore.getSnapshot()

    expect(snap.loaded).toBe(true)
    expect([...snap.foldedWorkflows]).toEqual(['w-1'])
    expect([...snap.foldedCategories]).toEqual(['to-start'])
    expect([...snap.expandedBuckets]).toEqual(['__epic_bucket__:date:2026-08-04'])
    await workflowFoldStore.ensureLoaded()
    expect(loadPreference).toHaveBeenCalledTimes(3)
  })

  it('notifies subscribers and persists on set*', () => {
    const seen: string[] = []
    const unsub = workflowFoldStore.subscribe(s => seen.push(`${[...s.foldedWorkflows].join(',')}|${[...s.foldedCategories].join(',')}|${[...s.expandedBuckets].join(',')}`))

    workflowFoldStore.setFoldedWorkflows(new Set(['w-2']))
    workflowFoldStore.setFoldedCategories(new Set(['archived']))
    workflowFoldStore.setExpandedBuckets(new Set(['b-1']))

    expect(seen[seen.length - 1]).toBe('w-2|archived|b-1')
    expect(savePreference).toHaveBeenCalledWith('workflowGraph.foldedWorkflows.v1', '["w-2"]', 'workflow-graph-expanded', expect.anything())
    expect(savePreference).toHaveBeenCalledWith('workflowGraph.foldedCategories.v1', '["archived"]', 'workflow-graph-folded-categories', expect.anything())
    expect(savePreference).toHaveBeenCalledWith('workflowGraph.archivedExpanded.v1', '["b-1"]', 'workflow-graph-archived-buckets', expect.anything())
    unsub()
  })
})

describe('buildWorkflowFilter', () => {
  it('returns null until the fold preferences settle', () => {
    const filter = buildWorkflowFilter(
      { loaded: false, foldedWorkflows: new Set(), foldedCategories: new Set(), expandedBuckets: new Set() },
      new Set(['agent-1']),
    )
    expect(filter).toBeNull()
  })

  it('serializes snapshot + agent ids into the wire filter', () => {
    const filter = buildWorkflowFilter(
      {
        loaded: true,
        foldedWorkflows: new Set(['w-1']),
        foldedCategories: new Set(['to-start'] as never),
        expandedBuckets: new Set(['b-1']),
      },
      new Set(['agent-1', 'agent-2']),
    )
    expect(filter).toEqual({
      FoldedWorkflows: ['w-1'],
      FoldedCategories: ['to-start'],
      ExpandedBuckets: ['b-1'],
      AgentIds: expect.arrayContaining(['agent-1', 'agent-2']),
    })
  })
})
