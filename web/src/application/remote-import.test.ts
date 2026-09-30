import { describe, it, expect, vi, beforeEach } from 'vitest'

// The desktop host bindings and the local gateway clients are the only inputs
// of the remote-import data layer; mock all three so the tests exercise the
// mapping and the two-hop orchestration without a live host.
vi.mock('../bindings/github.com/qomos-w/sporemind/pkg/desktop/app', () => ({
  ConnectionsList: vi.fn(),
  RemoteAgentList: vi.fn(),
  RemoteAgentContextExport: vi.fn(),
}))

vi.mock('./generated-client', () => ({
  client: { __marker: 'local-client' },
}))

vi.mock('../gen-clients/local/client', () => ({
  sessionImport: vi.fn(),
}))

// The UI refresh is a dynamic import of the timeline singleton; mock it so the
// data layer is tested without pulling in React.
vi.mock('../ui/ai/hooks/useTimelineManager', () => ({
  getTimelineManager: vi.fn(),
}))

import * as desktopApp from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import * as localClient from '../gen-clients/local/client'
import { getTimelineManager } from '../ui/ai/hooks/useTimelineManager'
import { client } from './generated-client'
import { listRemoteConnections, listRemoteAgents, remoteImportReplaceContext } from './remote-import'

const mocks = {
  connectionsList: vi.mocked(desktopApp.ConnectionsList),
  remoteAgentList: vi.mocked(desktopApp.RemoteAgentList),
  contextExport: vi.mocked(desktopApp.RemoteAgentContextExport),
  sessionImport: vi.mocked(localClient.sessionImport),
  getTimelineManager: vi.mocked(getTimelineManager),
}

const reconcileSpy = vi.fn()

// A fork response as the host binding delivers it: PascalCase domain fields
// plus a Goal that the replace path must drop.
function forkResponse() {
  return {
    Session: { Turns: [{ Id: 't1', Role: 'user' }], ActiveHead: 0 },
    SummarySegments: [{ Id: 's1' }],
    ExploreResults: [{ TurnID: 't1' }],
    Steps: [{ Id: 'st1' }],
    Goal: { Condition: 'cross-instance goal', MaxTurns: 5, TurnCount: 1 },
    NextIdx: 7,
    NextSeq: 11,
    NextTurnOrder: 3,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.getTimelineManager.mockReturnValue({ reconcile: reconcileSpy } as never)
})

describe('listRemoteConnections', () => {
  it('projects the host connection list', async () => {
    mocks.connectionsList.mockResolvedValue([
      { id: 'c1', name: 'home', host: '10.0.0.5', port: 18080 },
    ] as never)

    await expect(listRemoteConnections()).resolves.toEqual([
      { id: 'c1', name: 'home', host: '10.0.0.5', port: 18080 },
    ])
  })
})

describe('listRemoteAgents', () => {
  it('maps the host camelCase briefs to the UI PascalCase shape', async () => {
    mocks.remoteAgentList.mockResolvedValue([
      {
        actorId: 'ra1',
        displayName: 'Remote One',
        agentKind: 'coder',
        status: 'running',
        projectName: 'proj',
        lastActivity: '2026-01-01T00:00:00Z',
      },
    ] as never)

    await expect(listRemoteAgents('c1')).resolves.toEqual([
      {
        ActorId: 'ra1',
        DisplayName: 'Remote One',
        AgentKind: 'coder',
        Status: 'running',
        ProjectName: 'proj',
        LastActivity: '2026-01-01T00:00:00Z',
      },
    ])
    expect(mocks.remoteAgentList).toHaveBeenCalledWith('c1')
  })
})

describe('remoteImportReplaceContext', () => {
  it('exports the remote snapshot and imports it into the target local agent, dropping Goal', async () => {
    mocks.contextExport.mockResolvedValue(forkResponse() as never)
    mocks.sessionImport.mockResolvedValue({ AcceptedTurns: 1, ActiveHead: 0 } as never)

    const result = await remoteImportReplaceContext('c1', 'ra1', 'local-9')

    expect(result).toEqual({ acceptedTurns: 1 })
    // Hop 1: host fetches the remote snapshot for the chosen connection/agent.
    expect(mocks.contextExport).toHaveBeenCalledWith('c1', 'ra1')
    // Hop 2: local write scoped to the target agent, via the generated client.
    expect(mocks.sessionImport).toHaveBeenCalledTimes(1)
    const [calledClient, req, opts] = mocks.sessionImport.mock.calls[0]!
    expect(calledClient).toBe(client)
    expect(opts).toEqual({ target: 'local-9' })
    expect(req).toEqual({
      Session: { Turns: [{ Id: 't1', Role: 'user' }], ActiveHead: 0 },
      SummarySegments: [{ Id: 's1' }],
      ExploreResults: [{ TurnID: 't1' }],
      Steps: [{ Id: 'st1' }],
      NextIdx: 7,
      NextSeq: 11,
      NextTurnOrder: 3,
    })
    expect(req).not.toHaveProperty('Goal')
    // The local agent's timeline is re-fetched so an already-open conversation
    // converges to the imported history.
    expect(reconcileSpy).toHaveBeenCalledWith('local-9')
  })

  it('still resolves when the timeline refresh fails', async () => {
    mocks.contextExport.mockResolvedValue(forkResponse() as never)
    mocks.sessionImport.mockResolvedValue({ AcceptedTurns: 2, ActiveHead: 0 } as never)
    reconcileSpy.mockImplementationOnce(() => { throw new Error('no timeline') })
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    await expect(remoteImportReplaceContext('c1', 'ra1', 'local-9')).resolves.toEqual({ acceptedTurns: 2 })
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it('coerces a missing AcceptedTurns to 0', async () => {
    mocks.contextExport.mockResolvedValue(forkResponse() as never)
    mocks.sessionImport.mockResolvedValue({} as never)

    await expect(remoteImportReplaceContext('c1', 'ra1', 'local-9')).resolves.toEqual({ acceptedTurns: 0 })
  })

  it('propagates a remote fetch failure without writing locally', async () => {
    mocks.contextExport.mockRejectedValue(new Error('remote login rejected'))

    await expect(remoteImportReplaceContext('c1', 'ra1', 'local-9')).rejects.toThrow('remote login rejected')
    expect(mocks.sessionImport).not.toHaveBeenCalled()
  })
})
