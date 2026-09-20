import { StrictMode } from 'react'
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { WorkbenchAgentSession } from './useWorkbenchAgents'

const agentsState = vi.hoisted(() => ({
  sessions: new Map<string, WorkbenchAgentSession>(),
}))

const chatState = vi.hoisted(() => ({
  view: {
    envelopes: [] as unknown[],
    isStreaming: false,
    loading: false,
    pushUserMessage: vi.fn().mockReturnValue('temp-1'),
    replaceUserMessageId: vi.fn(),
    updateUserMessageIdx: vi.fn(),
    seedActiveTurn: vi.fn(),
    reconcile: vi.fn(),
  },
  actorId: null as string | null,
}))

const chatSubmit = vi.hoisted(() => vi.fn().mockResolvedValue({ TurnActorId: 'turn-9', MessageId: 'm-9', Idx: 2 }))
const loadAgent = vi.hoisted(() => vi.fn().mockResolvedValue({}))

vi.mock('./useWorkbenchAgents', () => ({
  useWorkbenchAgents: () => agentsState.sessions,
}))

vi.mock('../ai/hooks/useTimelineManager', () => ({
  useBackgroundTimeline: (actorId: string | null) => {
    chatState.actorId = actorId
    return chatState.view
  },
}))

vi.mock('../../gen-clients/local/client', () => ({
  chatSubmit: (...a: unknown[]) => chatSubmit(...a),
}))

vi.mock('../../gen-clients/workspace/client', () => ({
  loadAgent: (...a: unknown[]) => loadAgent(...a),
}))

vi.mock('../../application/generated-client', () => ({ client: {} }))

const { useCoordinatorChat } = await import('./useCoordinatorChat')

function session(over: Partial<WorkbenchAgentSession>): WorkbenchAgentSession {
  return {
    id: 'ag-1', actorId: 'actor-1', name: 'Coordinator', projectId: '', kind: 'coordinator',
    loadState: 'loaded', status: '', title: '', lastActivity: '', ...over,
  }
}

describe('useCoordinatorChat (workbench dock → coordinator)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    agentsState.sessions = new Map()
    chatState.view.envelopes = []
    chatState.view.isStreaming = false
    chatState.view.loading = false
    chatState.view.pushUserMessage.mockReset().mockReturnValue('temp-1')
    chatState.view.replaceUserMessageId.mockReset()
    chatState.view.updateUserMessageIdx.mockReset()
    chatState.view.seedActiveTurn.mockReset()
    chatState.view.reconcile.mockReset()
    chatState.actorId = null
    chatSubmit.mockReset().mockResolvedValue({ TurnActorId: 'turn-9', MessageId: 'm-9', Idx: 2 })
    loadAgent.mockReset().mockResolvedValue({})
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('resolves the workspace-global coordinator, not a project agent', () => {
    agentsState.sessions = new Map([
      ['actor-proj', session({ id: 'ag-proj', actorId: 'actor-proj', projectId: 'p1', kind: 'coordinator' })],
      ['actor-coord', session({ id: 'ag-coord', actorId: 'actor-coord' })],
      ['actor-coder', session({ id: 'ag-c', actorId: 'actor-c', kind: 'coder' })],
    ])
    const { result } = renderHook(() => useCoordinatorChat(), { wrapper: StrictMode })
    expect(result.current.coordinator?.actorId).toBe('actor-coord')
    expect(result.current.status).toBe('live')
  })

  it('falls back to local-only rows when no coordinator exists', () => {
    const { result } = renderHook(() => useCoordinatorChat(), { wrapper: StrictMode })
    expect(result.current.coordinator).toBeNull()
    expect(result.current.status).toBe('none')
    expect(result.current.rows).toEqual([])
    expect(chatState.actorId).toBeNull()
  })

  it('holds the timeline unwired and reports resolving while the agent is unloaded', async () => {
    agentsState.sessions = new Map([['actor-1', session({ loadState: 'unloaded' })]])
    const { result } = renderHook(() => useCoordinatorChat(), { wrapper: StrictMode })
    // The timeline must NOT subscribe to an unloaded agent's actor id — a
    // failed subscribe leaves a dead timeline the dock can never land on.
    expect(chatState.actorId).toBeNull()
    expect(result.current.status).toBe('resolving')
    let ok = true
    await act(async () => { ok = await result.current.submit('hi') })
    expect(ok).toBe(false)
    expect(chatSubmit).not.toHaveBeenCalled()
  })

  it('retries loadAgent until the projection reports the agent loaded', async () => {
    agentsState.sessions = new Map([['actor-1', session({ loadState: 'unloaded' })]])
    const { result, rerender } = renderHook(() => useCoordinatorChat(), { wrapper: StrictMode })
    await act(async () => { await vi.advanceTimersByTimeAsync(6000) })
    expect(loadAgent.mock.calls.length).toBeGreaterThanOrEqual(3)
    expect(loadAgent).toHaveBeenCalledWith({}, { AgentId: 'ag-1' })

    // The workspace materializes the agent: the projection flips LoadState.
    agentsState.sessions = new Map([['actor-1', session({ loadState: 'loaded' })]])
    act(() => rerender())
    expect(result.current.status).toBe('live')
    expect(chatState.actorId).toBe('actor-1')
    const calls = loadAgent.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(6000) })
    expect(loadAgent.mock.calls.length).toBe(calls) // retries stop once loaded
  })

  it('flattens envelopes into dock rows: user bubbles and assistant text frames only', () => {
    agentsState.sessions = new Map([['actor-1', session({})]])
    chatState.view.envelopes = [
      { id: 'e1', role: 'user', userContent: '调起终端', frames: [] },
      {
        id: 'e2', role: 'assistant', frames: [
          { type: 'reasoning', content: 'thinking…' },
          { type: 'text', content: '好的，' },
          { type: 'text', content: '已召回终端。' },
          { type: 'tool', name: 'workbench.promote' },
        ],
      },
      { id: 'e3', role: 'user', userContent: '', frames: [] },
    ]
    const { result } = renderHook(() => useCoordinatorChat(), { wrapper: StrictMode })
    expect(result.current.rows).toEqual([
      { id: 'u:e1', who: 'u', text: '调起终端' },
      { id: 'a:e2', who: 'a', text: '好的，\n已召回终端。' },
    ])
  })

  it('caps the dock rows at the latest 40', () => {
    agentsState.sessions = new Map([['actor-1', session({})]])
    chatState.view.envelopes = Array.from({ length: 60 }, (_, i) => ({
      id: `e${i}`, role: 'user' as const, userContent: `m${i}`, frames: [],
    }))
    const { result } = renderHook(() => useCoordinatorChat(), { wrapper: StrictMode })
    expect(result.current.rows).toHaveLength(40)
    expect(result.current.rows[0]).toMatchObject({ id: 'u:e20', text: 'm20' })
    expect(result.current.rows[39]).toMatchObject({ id: 'u:e59', text: 'm59' })
  })

  it('submits through chat_submit with the coordinator as target', async () => {
    agentsState.sessions = new Map([['actor-1', session({})]])
    const { result } = renderHook(() => useCoordinatorChat(), { wrapper: StrictMode })
    let ok = false
    await act(async () => { ok = await result.current.submit('把终端收起来') })
    expect(ok).toBe(true)
    expect(chatState.view.pushUserMessage).toHaveBeenCalledWith('把终端收起来')
    expect(chatSubmit).toHaveBeenCalledWith({}, { Text: '把终端收起来' }, { target: 'actor-1' })
    expect(chatState.view.seedActiveTurn).toHaveBeenCalledWith('turn-9')
    expect(chatState.view.replaceUserMessageId).toHaveBeenCalledWith('temp-1', 'm-9')
    expect(chatState.view.updateUserMessageIdx).toHaveBeenCalledWith('m-9', 2)
    expect(chatState.view.reconcile).toHaveBeenCalled()
    expect(result.current.status).toBe('live')
    expect(result.current.error).toBeNull()
  })

  it('surfaces submit failures instead of going silent', async () => {
    agentsState.sessions = new Map([['actor-1', session({})]])
    chatSubmit.mockRejectedValueOnce(new Error('agent mailbox closed'))
    const { result } = renderHook(() => useCoordinatorChat(), { wrapper: StrictMode })
    let ok = true
    await act(async () => { ok = await result.current.submit('hi') })
    expect(ok).toBe(false)
    expect(result.current.status).toBe('error')
    expect(result.current.error).toContain('mailbox closed')
    // The next submit retries rather than latching the error forever.
    await act(async () => { await result.current.submit('again') })
    expect(result.current.status).toBe('live')
    expect(result.current.error).toBeNull()
  })

  it('reports resolving while the wired timeline is still loading', () => {
    agentsState.sessions = new Map([['actor-1', session({})]])
    chatState.view.loading = true
    const { result } = renderHook(() => useCoordinatorChat(), { wrapper: StrictMode })
    expect(result.current.status).toBe('resolving')
  })
})
