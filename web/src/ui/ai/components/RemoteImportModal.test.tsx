import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { RemoteImportModal, type RemoteImportAgentRef } from './RemoteImportModal'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// The modal's only application dependency is the remote-import data layer.
const hoisted = vi.hoisted(() => ({
  listRemoteConnections: vi.fn(),
  listRemoteAgents: vi.fn(),
  remoteImportReplaceContext: vi.fn(),
}))

vi.mock('../../../application/remote-import', () => ({
  listRemoteConnections: hoisted.listRemoteConnections,
  listRemoteAgents: hoisted.listRemoteAgents,
  remoteImportReplaceContext: hoisted.remoteImportReplaceContext,
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key),
  }),
}))

const LOCAL_AGENT: RemoteImportAgentRef = { actorId: 'local-1', displayName: 'Local A' }

const CONNECTION = { id: 'c1', name: 'Conn A', host: '10.0.0.5', port: 18080 }
const REMOTE_AGENT = { ActorId: 'ra1', DisplayName: 'Remote A', ProjectName: 'Proj' }

describe('RemoteImportModal', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    hoisted.listRemoteConnections.mockReset().mockResolvedValue([CONNECTION])
    hoisted.listRemoteAgents.mockReset().mockResolvedValue([REMOTE_AGENT])
    hoisted.remoteImportReplaceContext.mockReset().mockResolvedValue({ acceptedTurns: 3 })
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  const byTestId = (id: string) => container.querySelector(`[data-testid="${id}"]`) as HTMLElement | null

  async function flush() {
    await act(async () => {
      await new Promise(resolve => setTimeout(resolve, 0))
    })
  }

  async function render(open = true, agent: RemoteImportAgentRef | null = LOCAL_AGENT) {
    await act(async () => {
      root.render(<RemoteImportModal open={open} agent={agent} onClose={() => {}} />)
    })
    await flush()
  }

  async function click(el: HTMLElement | null) {
    expect(el).not.toBeNull()
    await act(async () => { el!.click() })
    await flush()
  }

  it('renders nothing while closed', async () => {
    await render(false)
    expect(byTestId('remote-import-modal')).toBeNull()
  })

  it('lists saved connections and drives the connection -> agent -> confirm flow', async () => {
    await render()

    // Step 1: saved connection listed.
    expect(hoisted.listRemoteConnections).toHaveBeenCalledTimes(1)
    expect(byTestId('remote-import-connection-c1')).not.toBeNull()

    // Step 2: picking a connection loads remote agents for that connection.
    await click(byTestId('remote-import-connection-c1'))
    expect(hoisted.listRemoteAgents).toHaveBeenCalledWith('c1')
    expect(byTestId('remote-import-agent-ra1')).not.toBeNull()

    // Step 3: confirm page shows the replacement warning naming the local agent.
    await click(byTestId('remote-import-agent-ra1'))
    const warning = byTestId('remote-import-warning')
    expect(warning).not.toBeNull()
    expect(warning!.textContent).toContain('remoteImport.replaceWarning')
    expect(warning!.textContent).toContain('"agent":"Local A"')

    // Confirming calls the replace with (connId, remoteAgentActorId, localAgentActorId).
    await click(byTestId('remote-import-confirm'))
    expect(hoisted.remoteImportReplaceContext).toHaveBeenCalledWith('c1', 'ra1', 'local-1')
    expect(byTestId('remote-import-success')!.textContent).toContain('remoteImport.success')
  })

  it('shows the add-a-connection guidance when no connections are saved', async () => {
    hoisted.listRemoteConnections.mockResolvedValue([])
    await render()
    expect(byTestId('remote-import-no-connections')).not.toBeNull()
    expect(byTestId('remote-import-no-connections')!.textContent).toContain('remoteImport.noConnectionsHint')
  })

  it('reports permission denial when the remote agent list returns 403', async () => {
    hoisted.listRemoteAgents.mockRejectedValue(new Error('Remote returned status 403'))
    await render()
    await click(byTestId('remote-import-connection-c1'))
    const error = byTestId('remote-import-error')
    expect(error).not.toBeNull()
    expect(error!.textContent).toContain('remoteImport.permissionDenied')
  })

  it('renders a generic error when loading remote agents fails', async () => {
    hoisted.listRemoteAgents.mockRejectedValue(new Error('network down'))
    await render()
    await click(byTestId('remote-import-connection-c1'))
    const error = byTestId('remote-import-error')
    expect(error).not.toBeNull()
    expect(error!.textContent).toContain('remoteImport.failed')
  })

  it('renders an error when the replace call fails and does not show success', async () => {
    hoisted.remoteImportReplaceContext.mockRejectedValue(new Error('boom'))
    await render()
    await click(byTestId('remote-import-connection-c1'))
    await click(byTestId('remote-import-agent-ra1'))
    await click(byTestId('remote-import-confirm'))
    expect(byTestId('remote-import-error')!.textContent).toContain('remoteImport.failed')
    expect(byTestId('remote-import-success')).toBeNull()
  })

  it('renders an error when listing connections fails', async () => {
    hoisted.listRemoteConnections.mockRejectedValue(new Error('host offline'))
    await render()
    expect(byTestId('remote-import-error')).not.toBeNull()
  })
})
