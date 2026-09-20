import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import type { McpServerView } from '../../../gen-types/mcp'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/mcp/client', () => ({
  listServers: vi.fn(),
  discoverTools: vi.fn(),
  connect: vi.fn(),
  disconnect: vi.fn(),
  addServer: vi.fn(),
  updateServer: vi.fn(),
  removeServer: vi.fn(),
}))
vi.mock('../../../gen-clients/mcpmanager/client', () => ({
  OnMcpServerStatus: vi.fn(() => () => {}),
}))
vi.mock('../../../i18n', () => ({ useI18n: () => ({ t: (k: string) => k }) }))
import * as mcpClient from '../../../gen-clients/mcp/client'
import { ShellMcpSettings } from './ShellMcpSettings'

function server(partial: Partial<McpServerView> & Pick<McpServerView, 'Id' | 'Name'>): McpServerView {
  return {
    Transport: 'http',
    Http: { Url: 'https://mcp.example.com/mcp' } as McpServerView['Http'],
    Enabled: true,
    Status: { Id: partial.Id, Connected: false, ToolCount: 0 },
    ...partial,
  } as McpServerView
}

describe('ShellMcpSettings server card tool expansion', () => {
  let container: HTMLDivElement | null = null
  let root: Root | null = null

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    if (root && container) {
      act(() => {
        root!.unmount()
      })
      container.remove()
    }
    root = null
    container = null
    vi.clearAllMocks()
  })

  async function render() {
    await act(async () => {
      root!.render(<ShellMcpSettings />)
    })
    await act(async () => {})
    await act(async () => {})
  }

  function toggleButton(): HTMLButtonElement {
    const btn = container!.querySelector<HTMLButtonElement>('button[title="settings.mcp.toolsToggle"]')
    expect(btn).not.toBeNull()
    return btn!
  }

  it('fetches and renders tool name + description when a connected server is expanded', async () => {
    vi.mocked(mcpClient.listServers).mockResolvedValue({
      Items: [server({ Id: 'srv-0', Name: 'deepwiki', Status: { Id: 'srv-0', Connected: true, ToolCount: 2 } })],
    } as never)
    vi.mocked(mcpClient.discoverTools).mockResolvedValue({
      Servers: [
        {
          Id: 'srv-0',
          Name: 'deepwiki',
          Tools: [
            { Name: 'ask_question', Description: 'Ask about a repo', InputSchema: '{}' },
            { Name: 'read_wiki', Description: 'Read wiki contents', InputSchema: '{}' },
          ],
        },
        { Id: 'srv-1', Name: 'other', Tools: [{ Name: 'unrelated', Description: 'not this server', InputSchema: '{}' }] },
      ],
    } as never)

    await render()
    await act(async () => {
      toggleButton().dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {})

    expect(mcpClient.discoverTools).toHaveBeenCalledTimes(1)
    const text = container!.textContent ?? ''
    expect(text).toContain('ask_question')
    expect(text).toContain('Ask about a repo')
    expect(text).toContain('read_wiki')
    expect(text).not.toContain('unrelated')
  })

  it('shows the not-connected hint without calling discoverTools for a disconnected server', async () => {
    vi.mocked(mcpClient.listServers).mockResolvedValue({
      Items: [server({ Id: 'srv-0', Name: 'deepwiki', Status: { Id: 'srv-0', Connected: false, ToolCount: 0 } })],
    } as never)

    await render()
    await act(async () => {
      toggleButton().dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {})

    expect(mcpClient.discoverTools).not.toHaveBeenCalled()
    expect(container!.textContent).toContain('settings.mcp.toolsNotConnected')
  })

  it('collapses the tool list on a second toggle', async () => {
    vi.mocked(mcpClient.listServers).mockResolvedValue({
      Items: [server({ Id: 'srv-0', Name: 'deepwiki', Status: { Id: 'srv-0', Connected: true, ToolCount: 1 } })],
    } as never)
    vi.mocked(mcpClient.discoverTools).mockResolvedValue({
      Servers: [{ Id: 'srv-0', Name: 'deepwiki', Tools: [{ Name: 'ask_question', Description: 'Ask', InputSchema: '{}' }] }],
    } as never)

    await render()
    await act(async () => {
      toggleButton().dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {})
    expect(container!.textContent).toContain('ask_question')

    await act(async () => {
      toggleButton().dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {})
    expect(container!.textContent).not.toContain('ask_question')
  })

  it('shows the error hint when discoverTools rejects', async () => {
    vi.mocked(mcpClient.listServers).mockResolvedValue({
      Items: [server({ Id: 'srv-0', Name: 'deepwiki', Status: { Id: 'srv-0', Connected: true, ToolCount: 1 } })],
    } as never)
    vi.mocked(mcpClient.discoverTools).mockRejectedValue(new Error('boom'))

    await render()
    await act(async () => {
      toggleButton().dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {})

    expect(container!.textContent).toContain('settings.mcp.toolsError')
  })

  it('shows the empty hint when the connected server exposes no tools', async () => {
    vi.mocked(mcpClient.listServers).mockResolvedValue({
      Items: [server({ Id: 'srv-0', Name: 'deepwiki', Status: { Id: 'srv-0', Connected: true, ToolCount: 0 } })],
    } as never)
    vi.mocked(mcpClient.discoverTools).mockResolvedValue({ Servers: [] } as never)

    await render()
    await act(async () => {
      toggleButton().dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {})

    expect(container!.textContent).toContain('settings.mcp.toolsEmpty')
  })
})
