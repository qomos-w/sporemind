import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { AgentRef } from '../../../gen-clients/system/types'

const releaseSpy = vi.hoisted(() => vi.fn())
const workspaceMock = vi.hoisted(() => ({
  createAgent: vi.fn(),
  updateAgent: vi.fn(),
  cloneAgent: vi.fn(),
  deleteAgent: vi.fn(),
  agentTerminate: vi.fn(),
}))

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/workspace/client', () => workspaceMock)
vi.mock('./useTimelineManager', () => ({
  getTimelineManager: () => ({ release: releaseSpy }),
}))
vi.mock('./modelSlot', () => ({
  selectionToSlot: vi.fn(),
}))

import { useAgentOperations } from './useAgentOperations'

const deleteAgentMock = workspaceMock.deleteAgent
const terminateMock = workspaceMock.agentTerminate

describe('useAgentOperations.deleteAgent', () => {
  beforeEach(() => {
    deleteAgentMock.mockReset()
    deleteAgentMock.mockResolvedValue(undefined)
    terminateMock.mockReset()
    terminateMock.mockResolvedValue(undefined)
    releaseSpy.mockReset()
  })

  it('deletes through workspace.deleteAgent and releases the timeline', async () => {
    const { result } = renderHook(() => useAgentOperations())
    const agent = { Id: 'agent-1', ActorId: 'actor-1' } as AgentRef

    await act(async () => {
      await result.current.deleteAgent(agent)
    })

    expect(deleteAgentMock).toHaveBeenCalledWith({}, { AgentId: 'agent-1' })
    expect(releaseSpy).toHaveBeenCalledWith('actor-1')
    expect(result.current.state.error).toBeNull()
  })

  it('records an error and rethrows when the delete call fails', async () => {
    deleteAgentMock.mockRejectedValue(new Error('boom'))
    const { result } = renderHook(() => useAgentOperations())

    await act(async () => {
      await expect(result.current.deleteAgent({ Id: 'agent-2', ActorId: 'actor-2' } as AgentRef))
        .rejects.toThrow('boom')
    })

    expect(result.current.state.error).toBe('boom')
  })

  it('routes child agents (ParentAgentId set) through workspace.agentTerminate', async () => {
    const { result } = renderHook(() => useAgentOperations())
    const child = { Id: 'child-1', ActorId: 'child-actor-1', ParentAgentId: 'parent-1' } as AgentRef

    await act(async () => {
      await result.current.deleteAgent(child)
    })

    expect(terminateMock).toHaveBeenCalledWith({}, { AgentActorId: 'child-actor-1' })
    expect(deleteAgentMock).not.toHaveBeenCalled()
    expect(releaseSpy).toHaveBeenCalledWith('child-actor-1')
  })

  it('terminates through agentTerminate when a child agent fails to terminate', async () => {
    terminateMock.mockRejectedValue(new Error('built-in'))
    const { result } = renderHook(() => useAgentOperations())
    const child = { Id: 'child-2', ActorId: 'child-actor-2', ParentAgentId: 'parent-1' } as AgentRef

    await act(async () => {
      await expect(result.current.deleteAgent(child)).rejects.toThrow('built-in')
    })

    expect(result.current.state.error).toBe('built-in')
    expect(releaseSpy).not.toHaveBeenCalled()
  })
})
