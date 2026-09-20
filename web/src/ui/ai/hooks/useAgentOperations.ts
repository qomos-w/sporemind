import { useCallback, useState } from 'react'
import type { AgentRef } from '../../../gen-clients/system/types'
import type { AggregatorDescriptor } from '../../../gen-types/aigen'
import type { TurnEnvelope } from '../model/frame-types'
import type { AgentDialogInput } from '../components/NewAgentDialog'
import { client } from '../../../application/generated-client'
import * as workspace from '../../../gen-clients/workspace/client'
import { getTimelineManager } from './useTimelineManager'
import { selectionToSlot } from './modelSlot'

export interface AgentOperationsState {
  creating: boolean
  error: string | null
}

export interface AgentOperations {
  state: AgentOperationsState
  createAgent: (
    input: AgentDialogInput,
    options: { agentKinds: { kind: string }[]; aggregators: AggregatorDescriptor[] }
  ) => Promise<AgentRef | null>
  updateAgent: (
    input: AgentDialogInput,
    options: { editAgent: AgentRef; aggregators: AggregatorDescriptor[] }
  ) => Promise<void>
  cloneAgent: (
    input: AgentDialogInput,
    options: { sourceAgent: AgentRef; aggregators: AggregatorDescriptor[] }
  ) => Promise<AgentRef | null>
  forkFromTurn: (
    input: AgentDialogInput,
    options: { sourceAgent: AgentRef; turnId: string; aggregators: AggregatorDescriptor[] }
  ) => Promise<AgentRef | null>
  deleteAgent: (agent: AgentRef) => Promise<void>
  exportHistory: (envelopes: TurnEnvelope[], agentName?: string) => void
  importHistory: (file: File, agentActorId: string) => Promise<void>
  clearError: () => void
}

export function useAgentOperations(): AgentOperations {
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const clearError = useCallback(() => setError(null), [])

  const createAgent = useCallback(async (
    input: AgentDialogInput,
    { agentKinds }: { agentKinds: { kind: string }[]; aggregators: AggregatorDescriptor[] }
  ): Promise<AgentRef | null> => {
    if (!agentKinds.some(option => option.kind === input.agentKind)) {
      setError('Select a valid agent kind')
      return null
    }

    setCreating(true)
    setError(null)
    try {
      const agent = await workspace.createAgent(client, {
        ProjectId: input.projectId,
        DisplayName: input.displayName,
        AgentKind: input.agentKind,
        Primary: selectionToSlot(input.primarySelection),
        Fast: selectionToSlot(input.fastSelection),
        Execution: selectionToSlot(input.executionSelection),
        Review: selectionToSlot(input.reviewSelection),
        Summary: selectionToSlot(input.summarySelection),
        CompactionPolicy: input.compactionPolicy,
      })
      return agent
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to create agent'
      setError(message)
      return null
    } finally {
      setCreating(false)
    }
  }, [])

  const updateAgent = useCallback(async (
    input: AgentDialogInput,
    { editAgent }: { editAgent: AgentRef; aggregators: AggregatorDescriptor[] }
  ): Promise<void> => {
    setCreating(true)
    setError(null)
    try {
      await workspace.updateAgent(client, {
        AgentId: editAgent.Id,
        DisplayName: input.displayName,
        Title: input.title,
        Primary: selectionToSlot(input.primarySelection),
        Fast: selectionToSlot(input.fastSelection),
        Execution: selectionToSlot(input.executionSelection),
        Review: selectionToSlot(input.reviewSelection),
        Summary: selectionToSlot(input.summarySelection),
        CompactionPolicy: input.compactionPolicy,
      })
    } catch (error) {
      setError(error instanceof Error ? error.message : 'Failed to update agent')
    } finally {
      setCreating(false)
    }
  }, [])

  const cloneAgent = useCallback(async (
    input: AgentDialogInput,
    { sourceAgent }: { sourceAgent: AgentRef; aggregators: AggregatorDescriptor[] }
  ): Promise<AgentRef | null> => {
    setCreating(true)
    setError(null)
    try {
      const created = await workspace.cloneAgent(
        client,
        {
          SourceAgentId: sourceAgent.Id,
          DisplayName: input.displayName,
          Primary: selectionToSlot(input.primarySelection),
          Fast: selectionToSlot(input.fastSelection),
          Execution: selectionToSlot(input.executionSelection),
          Review: selectionToSlot(input.reviewSelection),
          Summary: selectionToSlot(input.summarySelection),
          CompactionPolicy: input.compactionPolicy,
        },
        { timeoutMs: 120_000 },
      )
      return created
    } catch (error) {
      setError(error instanceof Error ? error.message : 'Failed to clone agent')
      return null
    } finally {
      setCreating(false)
    }
  }, [])

  const forkFromTurn = useCallback(async (
    input: AgentDialogInput,
    { sourceAgent, turnId }: { sourceAgent: AgentRef; turnId: string; aggregators: AggregatorDescriptor[] }
  ): Promise<AgentRef | null> => {
    setCreating(true)
    setError(null)
    try {
      // Single server-side call: clone agent config + fork session at turnId.
      const created = await workspace.cloneAgent(
        client,
        {
          SourceAgentId: sourceAgent.Id,
          DisplayName: input.displayName,
          Primary: selectionToSlot(input.primarySelection),
          Fast: selectionToSlot(input.fastSelection),
          Execution: selectionToSlot(input.executionSelection),
          Review: selectionToSlot(input.reviewSelection),
          Summary: selectionToSlot(input.summarySelection),
          CompactionPolicy: input.compactionPolicy,
          ForkAtTurnId: turnId,
        },
        { timeoutMs: 120_000 },
      )
      return created
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to fork agent'
      setError(message)
      return null
    } finally {
      setCreating(false)
    }
  }, [])

  const deleteAgent = useCallback(async (agent: AgentRef): Promise<void> => {
    try {
      if (agent.ParentAgentId) {
        // Child agents (fork children, workflow workers) are system-managed
        // kinds: workspace.delete_agent rejects them as built-in.
        // workspace.agent_terminate is the sanctioned child teardown (same
        // cascadeDelete server-side) and authorizes developer/admin callers.
        await workspace.agentTerminate(client, { AgentActorId: agent.ActorId })
      } else {
        await workspace.deleteAgent(client, { AgentId: agent.Id })
      }
      getTimelineManager().release(agent.ActorId)
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to delete agent'
      setError(message)
      throw new Error(message)
    }
  }, [])

  const exportHistory = useCallback((envelopes: TurnEnvelope[], agentName?: string) => {
    if (!envelopes.length) return
    const blob = new Blob([JSON.stringify(envelopes, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    const name = agentName || 'session'
    a.download = `${name}-history-${new Date().toISOString().slice(0, 10)}.json`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  }, [])

  const importHistory = useCallback(async (file: File, agentActorId: string): Promise<void> => {
    try {
      const text = await file.text()
      const parsed = JSON.parse(text)
      if (!Array.isArray(parsed)) {
        console.error('[useAgentOperations] import: expected array of envelopes')
        return
      }
      getTimelineManager().importHistory(parsed, agentActorId)
    } catch (err) {
      console.error('[useAgentOperations] import failed:', err)
      throw err
    }
  }, [])

  return {
    state: { creating, error },
    createAgent,
    updateAgent,
    cloneAgent,
    forkFromTurn,
    deleteAgent,
    exportHistory,
    importHistory,
    clearError,
  }
}
