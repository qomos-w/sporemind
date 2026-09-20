import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const mocks = vi.hoisted(() => ({
  list: vi.fn().mockImplementation((_client: unknown, _req: unknown, opts: { target?: string } | undefined) => {
    if (opts?.target === 'agent-3') {
      return Promise.resolve({ Items: [{ CardId: 'builtin:bundle:workflow-tools' }] })
    }
    if (opts?.target === 'agent-4') {
      return Promise.resolve({ Items: [{ CardId: 'builtin:bundle:goal' }, { CardId: 'builtin:bundle:workflow-tools' }, { CardId: 'builtin:mode:goal' }] })
    }
    return Promise.resolve({ Items: [] })
  }),
  mount: vi.fn().mockResolvedValue({ Mount: {} }),
  unmount: vi.fn().mockResolvedValue({ Title: '' }),
  snapshot: vi.fn().mockResolvedValue({ Snapshot: { Tools: [] } }),
  listCallables: vi.fn().mockResolvedValue({ Items: [] }),
  listAllCards: vi.fn().mockResolvedValue({
    Cards: [
      { Id: 'builtin:bundle:file-tools', Tags: [], Data: { componentKind: 'bundle' } },
      { Id: 'mcp:srv-1', Tags: [], Data: { componentKind: 'bundle', mcpServerId: 'srv-1', title: 'File Server' } },
      { Id: 'builtin:mode:goal', Tags: [], Data: { componentKind: 'mode', lifecycleManaged: true, requires: ['builtin:bundle:goal'], flow: 'orchestration' } },
      { Id: 'builtin:mode:workflow', Tags: [], Data: { componentKind: 'mode', lifecycleManaged: true, requires: ['builtin:bundle:workflow-tools'], flow: 'orchestration' } },
      { Id: 'builtin:mode:worktree', Tags: [], Data: { componentKind: 'mode', lifecycleManaged: true, requires: ['builtin:bundle:worktree'] } },
      { Id: 'builtin:mode:memory', Tags: [], Data: { componentKind: 'mode' } },
      { Id: 'builtin:bundle:goal', Tags: [], Data: { componentKind: 'bundle', modeManaged: true } },
      { Id: 'builtin:bundle:workflow-tools', Tags: [], Data: { componentKind: 'bundle', modeManaged: true } },
      { Id: 'builtin:bundle:worktree', Tags: [], Data: { componentKind: 'bundle', modeManaged: true } },
      { Id: 'skill:review', Tags: [], Data: { componentKind: 'skill' } },
    ],
  }),
  // Project-scoped card store: source of appmanager-published app bundles,
  // where the kind lives in the frontmatter type field (card.Type), not in
  // Data.componentKind.
  listProjectCards: vi.fn().mockResolvedValue({
    Cards: [
      { Id: 'app-bundle:app.authenticator:authenticator-tools', Tags: [], Type: 'bundle', Data: { title: 'Authenticator Tools' } },
    ],
  }),
  listServers: vi.fn().mockResolvedValue({
    Items: [
      { Id: 'srv-1', Name: 'File Server', Transport: 'stdio', Enabled: true, Status: { Id: 'srv-1', Connected: true, ToolCount: 3 } },
    ],
  }),
  onStatus: vi.fn().mockReturnValue(() => {}),
}))

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/local/client', () => ({
  componentList: mocks.list,
  componentMount: mocks.mount,
  componentUnmount: mocks.unmount,
  componentSnapshot: mocks.snapshot,
  listCallables: mocks.listCallables,
}))
vi.mock('../../../gen-clients/workspace/client', () => ({ wikiListCards: mocks.listAllCards }))
vi.mock('../../../gen-clients/project/client', () => ({ wikiListCards: mocks.listProjectCards }))
vi.mock('../../../gen-clients/mcp/client', () => ({ listServers: mocks.listServers }))
vi.mock('../../../gen-clients/mcpmanager/client', () => ({ OnMcpServerStatus: mocks.onStatus }))

import { useAgentComponentMounts } from './useAgentComponentMounts'

describe('useAgentComponentMounts', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it('lists only bundle and mode cards and mounts against the active agent', async () => {
    let captured: ReturnType<typeof useAgentComponentMounts> | null = null
    function Harness() {
      captured = useAgentComponentMounts('agent-1', 'proj-1')
      return null
    }

    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(captured!.componentCards.map(card => card.Id)).toEqual([
      'builtin:bundle:file-tools',
      'mcp:srv-1',
      'builtin:mode:goal',
      'builtin:mode:workflow',
      'builtin:mode:worktree',
      'builtin:mode:memory',
      'app-bundle:app.authenticator:authenticator-tools',
    ])
    // The app-bundle card's kind is normalized from card.Type into Data so
    // downstream section/label routing reads a single field.
    const appBundle = captured!.componentCards.find(card => card.Id === 'app-bundle:app.authenticator:authenticator-tools')
    expect(appBundle?.Data?.componentKind).toBe('bundle')
    // MCP bundle cards are listed and carry live connection state.
    expect(captured!.mcpStatusById['srv-1']?.Connected).toBe(true)
    // The catalog listing must opt into IncludeBuiltin — the backend drops
    // mcp:* external cards from wiki listings without it, which hides every
    // MCP bundle from the slash menu / mount picker.
    expect(mocks.listAllCards).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ IncludeBuiltin: true }))

    await act(async () => captured!.mount('builtin:bundle:file-tools'))
    expect(mocks.mount).toHaveBeenCalledWith({}, { CardId: 'builtin:bundle:file-tools', Scope: 'user' }, { target: 'agent-1' })
  })

  it('excludes modeManaged bundles but keeps lifecycle modes visible without their bundle', async () => {
    let captured: ReturnType<typeof useAgentComponentMounts> | null = null
    function Harness() {
      captured = useAgentComponentMounts('agent-2', 'proj-1')
      return null
    }

    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
      await Promise.resolve()
    })

    const ids = captured!.componentCards.map(card => card.Id)
    expect(ids).not.toContain('builtin:bundle:goal')
    expect(ids).not.toContain('builtin:bundle:workflow-tools')
    expect(ids).not.toContain('builtin:bundle:worktree')
    // lifecycle modes stay visible without their bundle: the backend mount
    // handler auto-mounts requires as dependencies
    expect(ids).toContain('builtin:mode:goal')
    expect(ids).toContain('builtin:mode:workflow')
    expect(ids).toContain('builtin:mode:worktree')
    expect(ids).toEqual([
      'builtin:bundle:file-tools',
      'mcp:srv-1',
      'builtin:mode:goal',
      'builtin:mode:workflow',
      'builtin:mode:worktree',
      'builtin:mode:memory',
      'app-bundle:app.authenticator:authenticator-tools',
    ])
  })

  it('keeps modeManaged bundles hidden even when mounted as a dependency', async () => {
    let captured: ReturnType<typeof useAgentComponentMounts> | null = null
    function Harness() {
      captured = useAgentComponentMounts('agent-3', 'proj-1')
      return null
    }

    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
      await Promise.resolve()
    })

    const ids = captured!.componentCards.map(card => card.Id)
    expect(ids).toContain('builtin:mode:workflow')
    // modeManaged bundles are never shown even when mounted
    expect(ids).not.toContain('builtin:bundle:workflow-tools')
  })

  it('hides conflicting flow mode when a same-flow mode is already mounted', async () => {
    let captured: ReturnType<typeof useAgentComponentMounts> | null = null
    function Harness() {
      captured = useAgentComponentMounts('agent-4', 'proj-1')
      return null
    }

    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
      await Promise.resolve()
    })

    const ids = captured!.componentCards.map(card => card.Id)
    // goal mode is mounted (flow: orchestration) → workflow mode hidden
    expect(ids).not.toContain('builtin:mode:workflow')
    // goal mode itself stays visible (it is mounted, not hidden)
    expect(ids).toContain('builtin:mode:goal')
  })
})
