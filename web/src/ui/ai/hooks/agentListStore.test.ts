import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { AgentListItem, AgentRuntimeState } from '../../../gen-clients/system/types'

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../application/backend-ready', () => ({ waitForBackendReady: () => Promise.resolve() }))

const { agentListState } = vi.hoisted(() => ({ agentListState: vi.fn() }))
vi.mock('../../../gen-clients/workspace/client', () => ({
  agentListState,
  OnAgentListState: () => () => {},
}))

const { mergeState, getAgentListSnapshot, getAgentListItems, resetAgentListStore } =
  await import('./agentListStore')

function item(id: string, state: string): AgentListItem {
  return {
    Id: id,
    ActorId: 'actor-' + id,
    DisplayName: 'Agent ' + id,
    AgentKind: 'coder',
    ProjectId: 'p1',
    CanDelete: true,
    Runtime: { State: state } as AgentRuntimeState,
  } as AgentListItem
}

describe('mergeState', () => {
  beforeEach(() => {
    resetAgentListStore()
    agentListState.mockReset()
  })

  it('accepts a Full snapshot that rolls back version (actor-restart recovery)', () => {
    // Run the counter up to 50 as if the workspace actor has been alive.
    mergeState({ Version: 50, Full: true, Items: [item('a', 'running')] })
    expect(getAgentListSnapshot().version).toBe(50)
    expect(getAgentListItems()).toHaveLength(1)

    // Workspace actor restarts: the non-persisted version counter resets to 1
    // and OnStart emits a fresh Full snapshot.
    mergeState({ Version: 1, Full: true, Items: [item('a', 'idle'), item('b', 'idle')] })

    // Before the fix this was dropped at the version guard (1 < 50) and the
    // frontend froze on pre-restart state forever.
    expect(getAgentListSnapshot().version).toBe(1)
    expect(getAgentListItems()).toHaveLength(2)
    expect(getAgentListItems().map(i => i.Id)).toEqual(['a', 'b'])
  })

  it('drops a stale lower-version diff only when its Items are an identical replay', () => {
    mergeState({ Version: 10, Full: true, Items: [item('a', 'running')] })
    // Same content, lower version: a re-delivered stale event — no-op.
    mergeState({ Version: 5, Full: false, Items: [item('a', 'running')] })
    expect(getAgentListSnapshot().version).toBe(10)
    const items = getAgentListItems()
    expect(items[0]?.Runtime?.State).toBe('running')
  })

  it('accepts a differing lower-version diff after the version counter reset (restart resync)', () => {
    // A long session ran the counter up to 50; the backend restarted
    // mid-session, resetting the counter to single digits. The agent then
    // completed a turn and pushed its new state at the reset version.
    mergeState({ Version: 50, Full: true, Items: [item('a', 'running')] })
    mergeState({ Version: 3, Full: false, Items: [item('a', 'completed')] })

    // Before the fix this was dropped at the version guard (3 < 50), so the
    // list froze on pre-restart state and the unread-completion badge never
    // appeared for the finished agent.
    expect(getAgentListSnapshot().version).toBe(3)
    expect(getAgentListItems().find(i => i.Id === 'a')!.Runtime!.State).toBe('completed')
  })

  it('applies diff-patches carrying the full agent list (per-status updates)', () => {
    mergeState({ Version: 10, Full: true, Items: [item('a', 'running'), item('b', 'idle')] })
    // The backend rebuilds every event from the complete agent set, so a
    // status-only change arrives as a diff-patch at the next version with
    // every agent present. It must update the sidebar without a Full event.
    mergeState({ Version: 11, Full: false, Items: [item('a', 'completed'), item('b', 'idle')] })
    const items = getAgentListItems()
    expect(items).toHaveLength(2)
    expect(items.find(i => i.Id === 'a')!.Runtime!.State).toBe('completed')
    expect(items.find(i => i.Id === 'b')!.Runtime!.State).toBe('idle')
  })

  it('applies a higher-version diff directly instead of falling back to fetchFull', () => {
    mergeState({ Version: 10, Full: true, Items: [item('a', 'running')] })
    // Before the fix this triggered fetchFull (which pulled a Full=false
    // snapshot at the same version and deadlocked); the sidebar only updated
    // on a Full event. Now the diff applies directly.
    mergeState({ Version: 15, Full: false, Items: [item('a', 'completed')] })
    expect(getAgentListSnapshot().version).toBe(15)
    expect(agentListState).not.toHaveBeenCalled()
    expect(getAgentListItems().find(i => i.Id === 'a')!.Runtime!.State).toBe('completed')
  })
})
