import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AIConversationComposer } from './AIConversationComposer'
import { AIShellContext } from '../context/AIShellContext'
import { requestModeMount } from './modeMountStore'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  mount: vi.fn(),
  unmount: vi.fn(),
  mounts: [] as Array<{ CardId: string; Kind?: string; Enabled?: boolean; Scope?: string; Title?: string; Icon?: unknown }>,
  composerProps: null as any,
  componentCards: [] as Array<{ Id: string; Data?: Record<string, unknown> }>,
  componentList: vi.fn(async () => ({ Items: [] as Array<{ CardId: string }> })),
  componentMount: vi.fn(async () => ({})),
  listBrowserWindows: vi.fn(async () => [] as Array<{ Config: { Id: string; Name: string; Url: string }; Status: { Url: string; Open: boolean } }>),
  insiderAccess: true,
}))

vi.mock('../../../application/generated-client', () => ({ client: {} }))

vi.mock('../../../gen-clients/local/client', () => ({
  componentList: (...args: unknown[]) => hoisted.componentList(...(args as [])),
  componentMount: (...args: unknown[]) => hoisted.componentMount(...(args as [])),
}))

vi.mock('../../../application/browser-manager', () => ({
  listBrowserWindows: (...args: unknown[]) => hoisted.listBrowserWindows(...(args as [])),
  onBrowserManagerEvent: () => () => {},
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

// The gates under test live in the slot props AIConversationComposer passes to
// AIComposer; stub AIComposer with a thin slot renderer instead of mounting the
// full editor surface. The last-received props are captured on `hoisted` so
// tests can invoke the mention handlers directly.
vi.mock('./AIComposer', () => ({
  AIComposer: (props: any) => {
    hoisted.composerProps = props
    return (
      <div>
        <div data-testid="above-left">{props.aboveLeftSlot}</div>
        <div data-testid="above-right">{props.aboveRightSlot}</div>
        <div data-testid="avatar-bar">{props.avatarBarSlot}</div>
        {props.children}
      </div>
    )
  },
}))

// These hooks hit the backend on mount; the composer extras tests don't need them.
vi.mock('../hooks/useAgentComponentMounts', () => ({
  useAgentComponentMounts: () => ({
    mounts: hoisted.mounts,
    componentCards: hoisted.componentCards,
    componentTools: [],
    callablesById: new Map(),
    mount: hoisted.mount,
    unmount: hoisted.unmount,
    mcpStatusById: {},
  }),
  isDeveloperOnlyBundleCard: (card: { Id: string; Source?: string }) =>
    card.Id === 'builtin:bundle:debug' || card.Source === 'appmanager',
  isDevBuildOnlyBundleCard: (card: { Id: string; Type?: string; Data?: Record<string, unknown> }) =>
    (card.Data?.componentKind === 'bundle' || card.Type === 'bundle') && card.Data?.devOnly === true,
  isInsiderOnlyBundleCard: (card: { Id: string }) =>
    card.Id === 'builtin:bundle:browser-crawl',
}))

vi.mock('../hooks/useInsiderAccess', () => ({
  useInsiderAccess: () => hoisted.insiderAccess,
}))

vi.mock('../hooks/useComposerHistory', () => ({
  useComposerHistory: () => ({ history: [], setHistory: () => {} }),
  COMPOSER_HISTORY_SCOPE: 'chat',
}))

const shellContextValue = {
  providers: [],
  groups: [],
  activeRoute: null,
  currentUnit: null,
  activeProviderId: null,
  onProviderChange: () => {},
  onSelectRoute: () => {},
  onSelectUnit: () => {},
  activeThinkingLevel: null,
  onThinkingLevelChange: () => {},
} as any

const project = { ProjectID: 'p1', Name: 'Project One' } as any
const activeAgent = { ActorId: 'a1', Title: 'architect', DisplayName: 'Atlas' } as any

function baseProps(overrides: Record<string, unknown> = {}) {
  return {
    value: '',
    onChange: () => {},
    onSend: () => {},
    projects: [project],
    activeProjectId: 'p1',
    onProjectChange: () => {},
    projectSwitchingId: null,
    contextLoading: false,
    contextError: null,
    conversations: [],
    activeConversationId: null,
    onConversationChange: () => {},
    activeAgent,
    ...overrides,
  }
}

describe('AIConversationComposer composer extras', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderComposer = async (props: Record<string, unknown> = {}) => {
    await act(async () => {
      root.render(
        <AIShellContext.Provider value={shellContextValue}>
          <AIConversationComposer {...(baseProps(props) as any)} />
        </AIShellContext.Provider>,
      )
    })
  }

  it('shows the quick git bar and agent name label by default', async () => {
    await renderComposer()
    expect(container.querySelector('.ai-conversation-quickbar-git')).not.toBeNull()
    expect(container.querySelector('.ai-conversation-mobile-context-agent')).not.toBeNull()
    expect(container.querySelector('.ai-conversation-mobile-context-agent')?.textContent).toContain('architect')
  })

  it('hides the quick git bar when quickGitVisible is false', async () => {
    await renderComposer({ quickGitVisible: false })
    expect(container.querySelector('.ai-conversation-quickbar-git')).toBeNull()
    expect(container.querySelector('.ai-conversation-mobile-context-agent')).not.toBeNull()
  })

  it('hides the agent name label when mobileContextAgentVisible is false', async () => {
    await renderComposer({ mobileContextAgentVisible: false })
    expect(container.querySelector('.ai-conversation-quickbar-git')).not.toBeNull()
    expect(container.querySelector('.ai-conversation-mobile-context-agent')).toBeNull()
  })
})

describe('AIConversationComposer mode-mount requests', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderComposer = async (props: Record<string, unknown> = {}) => {
    await act(async () => {
      root.render(
        <AIShellContext.Provider value={shellContextValue}>
          <AIConversationComposer {...(baseProps(props) as any)} />
        </AIShellContext.Provider>,
      )
    })
  }

  it('mounts the requested builtin mode when the request targets its agent', async () => {
    await renderComposer()
    hoisted.mount.mockClear()
    await act(async () => {
      requestModeMount('builtin:mode:workflow', 'a1')
    })
    expect(hoisted.mount).toHaveBeenCalledWith('builtin:mode:workflow')
  })

  it('preserves the composer draft when a quick-start card mounts a mode', async () => {
    const onChange = vi.fn()
    await renderComposer({ onChange })
    hoisted.mount.mockClear()
    await act(async () => {
      requestModeMount('builtin:mode:goal', 'a1')
    })
    expect(hoisted.mount).toHaveBeenCalledWith('builtin:mode:goal')
    expect(onChange).not.toHaveBeenCalled()
  })

  it('ignores mode-mount requests addressed to another agent', async () => {
    await renderComposer()
    hoisted.mount.mockClear()
    await act(async () => {
      requestModeMount('builtin:mode:workflow', 'someone-else')
    })
    expect(hoisted.mount).not.toHaveBeenCalled()
  })

  it('ignores mode-mount requests when no agent is active', async () => {
    await renderComposer({ activeAgent: null })
    hoisted.mount.mockClear()
    await act(async () => {
      requestModeMount('builtin:mode:goal', 'a1')
    })
    expect(hoisted.mount).not.toHaveBeenCalled()
  })
})

describe('AIConversationComposer mounted mode card ids', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.mounts = []
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderComposer = async (props: Record<string, unknown> = {}) => {
    await act(async () => {
      root.render(
        <AIShellContext.Provider value={shellContextValue}>
          <AIConversationComposer {...(baseProps(props) as any)} />
        </AIShellContext.Provider>,
      )
    })
  }

  it('reports only enabled non-dependency mounts that carry a title and icon', async () => {
    hoisted.mounts = [
      { CardId: 'builtin:mode:workflow', Enabled: true, Title: 'Workflow', Icon: 'flow' },
      { CardId: 'builtin:mode:goal', Enabled: true, Title: 'Goal', Icon: 'target' },
      { CardId: 'dep-1', Enabled: true, Scope: 'dependency', Title: 'Dep', Icon: 'd' },
      { CardId: 'disabled-1', Enabled: false, Title: 'Off', Icon: 'o' },
      { CardId: 'no-icon-1', Enabled: true, Title: 'NoIcon' },
    ]
    const onMountedModeCardIdsChange = vi.fn()
    await renderComposer({ onMountedModeCardIdsChange })
    expect(onMountedModeCardIdsChange).toHaveBeenCalledWith([
      'builtin:mode:workflow',
      'builtin:mode:goal',
    ])
  })

  it('reports an empty list when nothing qualifies', async () => {
    hoisted.mounts = [
      { CardId: 'dep-1', Enabled: true, Scope: 'dependency', Title: 'Dep', Icon: 'd' },
    ]
    const onMountedModeCardIdsChange = vi.fn()
    await renderComposer({ onMountedModeCardIdsChange })
    expect(onMountedModeCardIdsChange).toHaveBeenCalledWith([])
  })
})

describe('AIConversationComposer agent mention mounts', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.mounts = []
    hoisted.composerProps = null
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderComposer = async (props: Record<string, unknown> = {}) => {
    await act(async () => {
      root.render(
        <AIShellContext.Provider value={shellContextValue}>
          <AIConversationComposer {...(baseProps(props) as any)} />
        </AIShellContext.Provider>,
      )
    })
  }

  it('mounts the agent-chat tag when the composer reports an agent mention', async () => {
    await renderComposer()
    await act(async () => {
      hoisted.composerProps?.onMentionAgent?.('agent-2', 'Beta', 'actor-2')
    })
    expect(hoisted.mount).toHaveBeenCalledWith('agent-chat:agent-2')
  })

  it('skips mounting when the agent-chat tag is already mounted', async () => {
    hoisted.mounts = [{ CardId: 'agent-chat:agent-2', Enabled: true, Title: 'Beta', Icon: 'bot' }]
    await renderComposer()
    await act(async () => {
      hoisted.composerProps?.onMentionAgent?.('agent-2', 'Beta', 'actor-2')
    })
    expect(hoisted.mount).not.toHaveBeenCalled()
    // A re-mention of an already-linked agent adds nothing — no reverse-link
    // prompt either.
    expect(container.querySelector('.confirm-dialog')).toBeNull()
  })

  it('switches to the linked agent when the agent-chat mount badge is clicked', async () => {
    hoisted.mounts = [{ CardId: 'agent-chat:agent-2', Enabled: true, Scope: 'user', Title: 'Beta', Icon: 'bot' }]
    const beta = { Id: 'agent-2', ActorId: 'actor-2', ProjectId: 'p1', DisplayName: 'Beta', Title: 'Beta', LoadState: 'loaded' } as any
    const onAgentAvatarClick = vi.fn()
    await renderComposer({ allAgents: [beta], onAgentAvatarClick })
    const badge = (hoisted.composerProps.badges ?? []).find((b: any) => b.title === 'Beta')
    expect(badge?.onClick).toBeTypeOf('function')
    badge.onClick()
    expect(onAgentAvatarClick).toHaveBeenCalledWith(beta)
  })

  it('resolves the agent-chat badge target by ActorId fallback', async () => {
    hoisted.mounts = [{ CardId: 'agent-chat:actor-2', Enabled: true, Scope: 'user', Title: 'Beta', Icon: 'bot' }]
    const beta = { Id: 'agent-2', ActorId: 'actor-2', ProjectId: 'p1', DisplayName: 'Beta', Title: 'Beta', LoadState: 'loaded' } as any
    const onAgentAvatarClick = vi.fn()
    await renderComposer({ allAgents: [beta], onAgentAvatarClick })
    const badge = (hoisted.composerProps.badges ?? []).find((b: any) => b.title === 'Beta')
    badge.onClick()
    expect(onAgentAvatarClick).toHaveBeenCalledWith(beta)
  })

  it('leaves the agent-chat badge inert when the linked agent cannot be resolved', async () => {
    hoisted.mounts = [{ CardId: 'agent-chat:gone', Enabled: true, Scope: 'user', Title: 'Gone', Icon: 'bot' }]
    await renderComposer({ allAgents: [] })
    const badge = (hoisted.composerProps.badges ?? []).find((b: any) => b.title === 'Gone')
    expect(badge?.onClick).toBeUndefined()
  })

  it('renders a stale red agent-chat badge when the target agent is unloaded', async () => {
    // Restart case: descriptor cache gone — Title falls back to the ref id,
    // Icon empty.
    hoisted.mounts = [{ CardId: 'agent-chat:dead-agent', Enabled: true, Scope: 'user' }]
    const dead = { Id: 'dead-agent', ActorId: 'actor-dead', DisplayName: 'Dead', Title: 'Dead Agent', LoadState: 'unloaded' } as any
    await renderComposer({ allAgents: [dead] })
    const staleBadge = (hoisted.composerProps.badges ?? []).find((b: any) => b.stale === true)
    expect(staleBadge).toBeDefined()
    expect(staleBadge.title).toBe('Dead')
    expect(staleBadge.icon).toBe('alert-circle')
    expect(staleBadge.onClick).toBeTypeOf('function')
  })

  it('clicking a stale badge for an unloaded target switches to and lazy-loads that agent', async () => {
    hoisted.mounts = [{ CardId: 'agent-chat:agent-2', Enabled: true, Scope: 'user', Title: 'Chat: Beta', Icon: 'bot' }]
    const beta = { Id: 'agent-2', ActorId: 'actor-2', ProjectId: 'p1', DisplayName: 'Beta', Title: 'Beta', LoadState: 'unloaded' } as any
    const onAgentAvatarClick = vi.fn()
    await renderComposer({ allAgents: [beta], onAgentAvatarClick })
    const staleBadge = (hoisted.composerProps.badges ?? []).find((b: any) => b.stale === true)
    expect(staleBadge).toBeDefined()
    await act(async () => {
      staleBadge.onClick?.()
    })
    expect(onAgentAvatarClick).toHaveBeenCalledWith(beta)
  })

  it('keeps the stale badge inert when the target agent no longer exists', async () => {
    hoisted.mounts = [{ CardId: 'agent-chat:gone-agent', Enabled: true, Scope: 'user' }]
    const other = { Id: 'agent-9', ActorId: 'actor-9', DisplayName: 'Other', LoadState: 'loaded' } as any
    await renderComposer({ allAgents: [other] })
    const staleBadge = (hoisted.composerProps.badges ?? []).find((b: any) => b.stale === true)
    expect(staleBadge).toBeDefined()
    expect(staleBadge.title).toBe('gone-agent')
    expect(staleBadge.onClick).toBeUndefined()
  })

  it('marks an agent-chat badge stale via LoadState even when cached visuals survive', async () => {
    // In-memory case: mount still carries the cached Title/Icon from mount
    // time, but the live agent snapshot reports the target as unloaded.
    hoisted.mounts = [{ CardId: 'agent-chat:agent-2', Enabled: true, Scope: 'user', Title: 'Chat: Beta', Icon: 'bot' }]
    const beta = { Id: 'agent-2', ActorId: 'actor-2', DisplayName: 'Beta', Title: 'Beta', LoadState: 'unloaded' } as any
    await renderComposer({ allAgents: [beta] })
    const staleBadge = (hoisted.composerProps.badges ?? []).find((b: any) => b.stale === true)
    expect(staleBadge).toBeDefined()
    expect(staleBadge.title).toBe('Beta')
  })

  it('unmounts the stale agent-chat channel when its badge close is invoked', async () => {
    hoisted.mounts = [{ CardId: 'agent-chat:dead-agent', Enabled: true, Scope: 'user' }]
    const dead = { Id: 'dead-agent', ActorId: 'actor-dead', DisplayName: 'Dead', LoadState: 'unloaded' } as any
    await renderComposer({ allAgents: [dead] })
    const staleBadge = (hoisted.composerProps.badges ?? []).find((b: any) => b.stale === true)
    expect(staleBadge).toBeDefined()
    await act(async () => {
      staleBadge.onClose?.()
    })
    expect(hoisted.unmount).toHaveBeenCalledWith('agent-chat:dead-agent')
  })

  it('opens the badge context menu and the details overlay for bundle badges', async () => {
    hoisted.mounts = [{ CardId: 'builtin:bundle:file-tools', Kind: 'bundle', Enabled: true, Scope: 'user', Title: 'file-tools', Icon: 'files' }]
    await renderComposer()
    const badge = (hoisted.composerProps.badges ?? []).find((b: any) => b.title === 'file-tools')
    expect(badge?.onContextMenu).toBeTypeOf('function')

    await act(async () => { badge.onContextMenu(10, 20) })
    const menu = document.querySelector('.component-badge-context-menu') as HTMLElement
    expect(menu).not.toBeNull()
    expect(menu.textContent).toContain('composer.badge.contextMenu.details')

    const item = menu.querySelector('.component-badge-context-menu-item') as HTMLElement
    await act(async () => { item.click() })
    expect(document.querySelector('.component-badge-context-menu')).toBeNull()

    const overlay = document.querySelector('[data-testid="component-detail-overlay"]') as HTMLElement
    expect(overlay).not.toBeNull()
    expect(overlay.textContent).toContain('builtin:bundle:file-tools')

    // Escape closes the overlay.
    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    })
    expect(document.querySelector('[data-testid="component-detail-overlay"]')).toBeNull()
  })

  it('omits the context menu from non-component badges (agent-chat)', async () => {
    hoisted.mounts = [{ CardId: 'agent-chat:agent-2', Kind: 'agent-chat', Enabled: true, Scope: 'user', Title: 'Beta', Icon: 'bot' }]
    const beta = { Id: 'agent-2', ActorId: 'actor-2', ProjectId: 'p1', DisplayName: 'Beta', Title: 'Beta', LoadState: 'loaded' } as any
    await renderComposer({ allAgents: [beta] })
    const badge = (hoisted.composerProps.badges ?? []).find((b: any) => b.title === 'Beta')
    expect(badge?.onContextMenu).toBeUndefined()
  })

  it('opens the reverse-link confirm after a new one-way link and mounts the reverse card on confirm', async () => {
    await renderComposer({ activeAgent: { ...activeAgent, Id: 'Atlas#1' } })
    await act(async () => {
      hoisted.composerProps?.onMentionAgent?.('agent-2', 'Beta', 'actor-2')
    })
    expect(container.querySelector('.confirm-dialog')).not.toBeNull()
    expect(container.querySelector('.confirm-dialog-title')?.textContent).toContain('dialog.agentReverseLink.title')

    const confirm = container.querySelector('.confirm-dialog-btn.confirm') as HTMLButtonElement
    await act(async () => {
      confirm.click()
    })
    // Reverse card addresses the target agent and references the current
    // agent's ref Id.
    expect(hoisted.componentMount).toHaveBeenCalledWith(
      {},
      { CardId: 'agent-chat:Atlas#1', Scope: 'user' },
      { target: 'actor-2' },
    )
    expect(container.querySelector('.confirm-dialog')).toBeNull()
  })

  it('suppresses the reverse-link confirm when the target already has the reverse link', async () => {
    hoisted.componentList.mockResolvedValueOnce({ Items: [{ CardId: 'agent-chat:a1' }] })
    await renderComposer()
    await act(async () => {
      hoisted.composerProps?.onMentionAgent?.('agent-2', 'Beta', 'actor-2')
    })
    expect(hoisted.componentList).toHaveBeenCalledWith({}, {}, { target: 'actor-2' })
    expect(container.querySelector('.confirm-dialog')).toBeNull()
    expect(hoisted.componentMount).not.toHaveBeenCalled()
  })

  it('keeps the link one-way when the reverse-link confirm is cancelled', async () => {
    await renderComposer()
    await act(async () => {
      hoisted.composerProps?.onMentionAgent?.('agent-2', 'Beta', 'actor-2')
    })
    const cancel = container.querySelector('.confirm-dialog-btn.cancel') as HTMLButtonElement
    await act(async () => {
      cancel.click()
    })
    expect(hoisted.componentMount).not.toHaveBeenCalled()
    expect(container.querySelector('.confirm-dialog')).toBeNull()
  })
})

describe('AIConversationComposer browser mention mounts', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.mounts = []
    hoisted.composerProps = null
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderComposer = async (props: Record<string, unknown> = {}) => {
    await act(async () => {
      root.render(
        <AIShellContext.Provider value={shellContextValue}>
          <AIConversationComposer {...(baseProps(props) as any)} />
        </AIShellContext.Provider>,
      )
    })
  }

  it('mounts the browser-chat tag when the composer reports a browser mention', async () => {
    await renderComposer()
    await act(async () => {
      hoisted.composerProps?.onMentionBrowser?.('win-1', 'Docs', 'https://docs.example')
    })
    expect(hoisted.mount).toHaveBeenCalledWith('browser-chat:win-1')
  })

  it('skips mounting when the browser-chat tag is already mounted', async () => {
    hoisted.mounts = [{ CardId: 'browser-chat:win-1', Enabled: true, Scope: 'user', Title: 'Browser: win-1', Icon: 'globe' }]
    await renderComposer()
    await act(async () => {
      hoisted.composerProps?.onMentionBrowser?.('win-1', 'Docs', 'https://docs.example')
    })
    expect(hoisted.mount).not.toHaveBeenCalled()
  })

  it('renders no browser badge when the agent has no browser-chat mount, even after a mention', async () => {
    // The reported bug: a %-mention on one agent leaked its badge onto every
    // other agent (incl. newly created ones) because the badge came from
    // composer-local state. Badge presence must be a reflection of the
    // per-agent backend mount list only.
    await renderComposer()
    await act(async () => {
      hoisted.composerProps?.onMentionBrowser?.('win-1', 'Docs', 'https://docs.example')
    })
    expect((hoisted.composerProps.badges ?? []).some((b: any) => b.icon === 'globe')).toBe(false)
  })

  it('renders the browser badge from the backend mount with live instance metadata', async () => {
    hoisted.listBrowserWindows.mockResolvedValue([
      { Config: { Id: 'win-1', Name: 'Docs', Url: 'https://docs.example' }, Status: { Url: 'https://docs.example/live', Open: true } },
    ])
    hoisted.mounts = [{ CardId: 'browser-chat:win-1', Enabled: true, Scope: 'user' }]
    const onOpenBrowserTab = vi.fn()
    await renderComposer({ onOpenBrowserTab })
    const badge = (hoisted.composerProps.badges ?? []).find((b: any) => b.icon === 'globe')
    expect(badge).toBeDefined()
    expect(badge.label).toBe('Docs')
    badge.onClick()
    expect(onOpenBrowserTab).toHaveBeenCalledWith('win-1', 'Docs', 'https://docs.example/live')
    await act(async () => {
      badge.onClose?.()
    })
    expect(hoisted.unmount).toHaveBeenCalledWith('browser-chat:win-1')
  })

  it('falls back to the mount title when the browser window no longer exists', async () => {
    hoisted.mounts = [{ CardId: 'browser-chat:win-gone', Enabled: true, Scope: 'user', Title: 'Browser: win-gone', Icon: 'globe' }]
    const onOpenBrowserTab = vi.fn()
    await renderComposer({ onOpenBrowserTab })
    const badge = (hoisted.composerProps.badges ?? []).find((b: any) => b.icon === 'globe')
    expect(badge).toBeDefined()
    expect(badge.label).toBe('Browser: win-gone')
    expect(badge.onClick).toBeUndefined()
    await act(async () => {
      badge.onClose?.()
    })
    expect(hoisted.unmount).toHaveBeenCalledWith('browser-chat:win-gone')
  })
})

describe('AIConversationComposer component slash actions', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.mounts = []
    hoisted.componentCards = []
    hoisted.composerProps = null
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderComposer = async (props: Record<string, unknown> = {}) => {
    await act(async () => {
      root.render(
        <AIShellContext.Provider value={shellContextValue}>
          <AIConversationComposer {...(baseProps(props) as any)} />
        </AIShellContext.Provider>,
      )
    })
  }

  it('localizes builtin bundles and modes and shows name, slash command and intro', async () => {
    // The mocked t() returns the key itself, so the localized values surface
    // as their component.* catalog keys.
    hoisted.componentCards = [
      { Id: 'builtin:bundle:web-search', Data: { componentKind: 'bundle', title: 'Web Search' } },
      { Id: 'builtin:mode:goal', Data: { componentKind: 'mode', title: 'Goal Mode' } },
    ]
    await renderComposer()
    const actions = hoisted.composerProps.componentActions ?? []
    const bundle = actions.find((a: any) => a.id === 'component:builtin:bundle:web-search')
    expect(bundle).toMatchObject({
      section: 'bundle',
      label: 'component.bundle.web-search.name',
      description: 'component.bundle.web-search.desc',
      shortcut: '/web-search',
    })
    expect(bundle.keywords).toContain('Web Search')
    const mode = actions.find((a: any) => a.id === 'component:builtin:mode:goal')
    expect(mode).toMatchObject({
      section: 'componentMode',
      label: 'component.mode.goal.name',
      description: 'component.mode.goal.desc',
      shortcut: '/goal',
    })
  })

  it('falls back to the card title and mount hint for non-builtin cards', async () => {
    hoisted.componentCards = [
      { Id: 'mcp:my-server', Data: { componentKind: 'bundle', title: 'My Server' } },
    ]
    await renderComposer()
    const action = (hoisted.composerProps.componentActions ?? [])
      .find((a: any) => a.id === 'component:mcp:my-server')
    expect(action).toMatchObject({
      label: 'My Server',
      description: 'omnibox.mountHint',
    })
    // Namespaced third-party cards get no slash chip.
    expect(action.shortcut).toBeUndefined()
  })

  it('hides the browser-crawl bundle from the picker without Insider access', async () => {
    hoisted.insiderAccess = false
    hoisted.componentCards = [
      { Id: 'builtin:bundle:web-search', Data: { componentKind: 'bundle', title: 'Web Search' } },
      { Id: 'builtin:bundle:browser-crawl', Data: { componentKind: 'bundle', title: 'Browser Crawl' } },
    ]
    await renderComposer()
    const ids = (hoisted.composerProps.componentActions ?? []).map((a: any) => a.id)
    expect(ids).toContain('component:builtin:bundle:web-search')
    expect(ids).not.toContain('component:builtin:bundle:browser-crawl')

    hoisted.insiderAccess = true
    await renderComposer()
    const idsAfter = (hoisted.composerProps.componentActions ?? []).map((a: any) => a.id)
    expect(idsAfter).toContain('component:builtin:bundle:browser-crawl')
  })
})
