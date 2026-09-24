import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act, type ComponentProps } from 'react'
import { AIComposer, routeModelLabels, type ComposerBadge, type ProviderGroup, type ProviderOption, type ProviderItem, type RefOption } from './AIComposer'
import { AIShellContext } from '../context/AIShellContext'
import type { DispatchActivity } from '../../../gen-clients/system/types'
import { rankAgentMentionResults, nameInitials } from '../hooks/useCardMention'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  useViewportMode: vi.fn(() => 'desktop'),
  useBrowserOverlay: vi.fn(() => {}),
  voiceAPI: { stop: vi.fn(), setActive: vi.fn() },
  dbgLog: vi.fn(),
  startScreenshot: vi.fn(),
  eventsOn: vi.fn(() => () => {}),
  slashCommands: [{ Name: 'clear', ShortHelp: 'Clear session' }],
  slashSkills: [{ Name: 'review', ShortHelp: 'Review current changes' }],
  builtinModes: [{ CardId: 'builtin:mode:goal', Name: 'goal', Title: 'Goal Mode', Icon: 'target' }],
  mentionResults: [] as Array<{ Id: string; Type: string; Source: string; Storage: string; Visibility: string; Tags: string[]; List: string[]; Created: string; Modified: string; Protected: boolean; Editable: boolean; Deletable: boolean; Raw: string }>,
  fileResults: [] as string[],
  agentMentionResults: [] as any[],
  browserMentionResults: [] as Array<{ instanceId: string; name: string; url: string }>,
  insiderAccess: true,
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/useViewportMode', () => ({
  useViewportMode: hoisted.useViewportMode,
}))

vi.mock('../browserOverlay', () => ({
  useBrowserOverlay: hoisted.useBrowserOverlay,
}))

vi.mock('../hooks/useSlashCommands', () => ({
  useSlashCommands: () => ({ commands: hoisted.slashCommands, skills: hoisted.slashSkills, modes: hoisted.builtinModes, loading: false, error: null }),
}))

vi.mock('../hooks/useCardMention', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../hooks/useCardMention')>()
  return {
    ...actual,
    useCardMention: () => ({ results: hoisted.mentionResults, loading: false }),
    useFileMention: () => ({ results: hoisted.fileResults, loading: false }),
    useAgentMention: () => hoisted.agentMentionResults,
    useBrowserMention: () => ({ results: hoisted.browserMentionResults, loading: false }),
  }
})

vi.mock('../hooks/useInsiderAccess', () => ({
  useInsiderAccess: () => hoisted.insiderAccess,
}))

vi.mock('../voice-api', () => ({
  voiceAPI: hoisted.voiceAPI,
  dbgLog: hoisted.dbgLog,
}))

vi.mock('../../../application/wails-bridge', () => ({
  startScreenshot: hoisted.startScreenshot,
}))

vi.mock('@wailsio/runtime', async (importOriginal) => {
  const runtime = await importOriginal<typeof import('@wailsio/runtime')>()
  return {
    ...runtime,
    Events: { ...runtime.Events, On: hoisted.eventsOn },
  }
})

function mcard(Id: string) {
  return { Id, Type: 'wiki', Source: 'project', Storage: 'file', Visibility: 'public', Tags: [] as string[], List: [] as string[], Created: '', Modified: '', Protected: false, Editable: true, Deletable: true, Raw: '' }
}

// Minimal AgentInfo-shaped fixture consumed by the useAgentMention mock.
function magent(over: Record<string, unknown> = {}) {
  return {
    Id: 'agent-1',
    ActorId: 'actor-1',
    ProjectId: 'proj-1',
    ProjectName: 'Project One',
    DisplayName: 'Agent One',
    Title: 'Agent One',
    HasTitle: false,
    AgentKind: 'coder',
    LoadState: 'loaded',
    ...over,
  }
}

describe('AIComposer badges', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.insiderAccess = true
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderComposer = async (badges: ComposerBadge[] = [], configBadgesVisible = false) => {
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          badges={badges}
          configBadgesVisible={configBadgesVisible}
        />,
      )
    })
  }

  it('renders regular badges by default', async () => {
    const badges: ComposerBadge[] = [
      { icon: 'target', color: '#d97706', title: 'Goal Mode', onClose: vi.fn() },
    ]
    await renderComposer(badges)

    const panel = container.querySelector('.ai-composer-badge-drawer-panel')
    expect(panel).not.toBeNull()

    const badge = container.querySelector('.ai-composer-badge')
    expect(badge).not.toBeNull()
    expect(badge?.querySelector('svg')).not.toBeNull()
    expect(badge?.textContent).toContain('Goal Mode')
  })

  it('hides config badge group by default', async () => {
    const badges: ComposerBadge[] = [
      { icon: 'target', color: '#d97706', title: 'Bundle', configGroup: true, onClose: vi.fn() },
    ]
    await renderComposer(badges)
    expect(container.querySelector('.ai-composer-badge-group')).toBeNull()
  })

  it('renders config badge group when enabled', async () => {
    const badges: ComposerBadge[] = [
      { icon: 'target', color: '#d97706', title: 'Bundle', configGroup: true, onClose: vi.fn() },
    ]
    await renderComposer(badges, true)

    expect(container.querySelector('.ai-composer-badge-group')).not.toBeNull()
  })

  it('calls onClose when badge close button is clicked', async () => {
    const onClose = vi.fn()
    const badges: ComposerBadge[] = [
      { icon: 'target', color: '#d97706', title: 'Goal Mode', onClose },
    ]
    await renderComposer(badges)

    const closeBtn = container.querySelector('.ai-composer-badge-close')
    expect(closeBtn).not.toBeNull()
    await act(async () => {
      ;(closeBtn as HTMLElement).click()
    })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('renders no badges when the list is empty', async () => {
    await renderComposer([])
    expect(container.querySelector('.ai-composer-badge')).toBeNull()
  })

  it('renders a parent badge with the parent name and is clickable', async () => {
    // Mirrors the parent badge produced by AIConversationComposer when the
    // active agent has a ParentAgentId: a clickable badge (onClick set, no
    // onClose) showing the parent's display name.
    const onClick = vi.fn()
    const badges: ComposerBadge[] = [
      { icon: 'corner-up-left', title: 'Parent Agent', onClick },
    ]
    await renderComposer(badges)

    const badge = container.querySelector('.ai-composer-badge')
    expect(badge).not.toBeNull()
    expect(badge?.classList.contains('ai-composer-badge--clickable')).toBe(true)
    expect(badge?.textContent).toContain('Parent Agent')

    await act(async () => {
      ;(badge as HTMLElement).click()
    })
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('forwards badge right-clicks to onContextMenu and suppresses the native menu', async () => {
    const onContextMenu = vi.fn()
    const badges: ComposerBadge[] = [
      { icon: 'target', color: '#d97706', title: 'Goal Mode', onContextMenu },
    ]
    await renderComposer(badges)

    const badge = container.querySelector('.ai-composer-badge') as HTMLElement
    expect(badge).not.toBeNull()
    const event = new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 12, clientY: 34 })
    badge.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
    expect(onContextMenu).toHaveBeenCalledTimes(1)
    expect(onContextMenu).toHaveBeenCalledWith(12, 34)
  })

  it('forwards config-group icon right-clicks to onContextMenu', async () => {
    const onContextMenu = vi.fn()
    const badges: ComposerBadge[] = [
      { icon: 'target', title: 'Bundle', configGroup: true, onContextMenu },
    ]
    await renderComposer(badges, true)

    const icon = container.querySelector('.ai-composer-badge-group .ai-composer-badge-icon') as HTMLElement
    expect(icon).not.toBeNull()
    const event = new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 5, clientY: 6 })
    icon.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
    expect(onContextMenu).toHaveBeenCalledWith(5, 6)
  })

  it('adds clipboard images to the preview', async () => {
    await renderComposer([])
    const image = new File(['image'], 'clipboard.png', { type: 'image/png' })
    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    const paste = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(paste, 'clipboardData', {
      value: {
        items: [{ kind: 'file', type: 'image/png', getAsFile: () => image }],
      },
    })

    await act(async () => {
      textarea.dispatchEvent(paste)
      await new Promise(resolve => setTimeout(resolve, 20))
    })

    const preview = container.querySelector('.ai-composer-preview-img') as HTMLImageElement
    expect(paste.defaultPrevented).toBe(true)
    expect(preview).not.toBeNull()
    expect(preview.alt).toBe('clipboard.png')
  })

  it('clicking a preview thumbnail opens the image in the right-panel viewer', async () => {
    const onOpenImage = vi.fn()
    await act(async () => {
      root.render(
        <AIShellContext.Provider value={{ onOpenImage } as any}>
          <AIComposer value="" onChange={() => {}} onSend={() => {}} />
        </AIShellContext.Provider>,
      )
    })
    const image = new File(['image'], 'clipboard.png', { type: 'image/png' })
    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    const paste = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(paste, 'clipboardData', {
      value: {
        items: [{ kind: 'file', type: 'image/png', getAsFile: () => image }],
      },
    })
    await act(async () => {
      textarea.dispatchEvent(paste)
      for (let i = 0; i < 50 && !container.querySelector('.ai-composer-preview-img'); i++) {
        await new Promise(resolve => setTimeout(resolve, 20))
      }
    })

    const preview = container.querySelector('.ai-composer-preview-img') as HTMLImageElement
    expect(preview).not.toBeNull()
    await act(async () => {
      preview.click()
    })
    expect(onOpenImage).toHaveBeenCalledTimes(1)
    expect(onOpenImage).toHaveBeenCalledWith(preview.src, 'clipboard.png')
  })

  it('does not intercept text-only paste', async () => {
    await renderComposer([])
    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    const paste = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(paste, 'clipboardData', {
      value: {
        items: [{ kind: 'string', type: 'text/plain', getAsFile: () => null }],
      },
    })

    await act(async () => {
      textarea.dispatchEvent(paste)
    })

    expect(paste.defaultPrevented).toBe(false)
    expect(container.querySelector('.ai-composer-preview-img')).toBeNull()
  })

  it('binds images to the agent composer they were pasted into', async () => {
    const renderWithAgent = async (agentActorId: string | null) => {
      await act(async () => {
        root.render(
          <AIComposer value="" onChange={() => {}} onSend={() => {}} agentActorId={agentActorId} />,
        )
      })
    }
    await renderWithAgent('actor-media-a')
    const image = new File(['image'], 'clipboard.png', { type: 'image/png' })
    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    const paste = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(paste, 'clipboardData', {
      value: {
        items: [{ kind: 'file', type: 'image/png', getAsFile: () => image }],
      },
    })
    await act(async () => {
      textarea.dispatchEvent(paste)
      await new Promise(resolve => setTimeout(resolve, 20))
    })
    expect(container.querySelector('.ai-composer-preview-img')).not.toBeNull()

    // Switching to another agent's composer must not carry the image over.
    await renderWithAgent('actor-media-b')
    expect(container.querySelector('.ai-composer-preview-img')).toBeNull()

    // Switching back restores the image in the composer it belongs to.
    await renderWithAgent('actor-media-a')
    const restored = container.querySelector('.ai-composer-preview-img') as HTMLImageElement
    expect(restored).not.toBeNull()
    expect(restored.alt).toBe('clipboard.png')

    // The workspace composer (no agent) also starts empty.
    await renderWithAgent(null)
    expect(container.querySelector('.ai-composer-preview-img')).toBeNull()
  })

  it('shows only slash commands, skills, bundles, and component modes', async () => {
    const onChange = vi.fn()
    await act(async () => {
      root.render(
        <AIComposer
          value="/"
          onChange={onChange}
          onSend={() => {}}
          componentActions={[
            { id: 'component:team', section: 'bundle', label: 'Team Bundle', keywords: ['team'], score: 0, run: vi.fn() },
            { id: 'component:plan', section: 'componentMode', label: 'Plan Mode', keywords: ['plan'], score: 0, run: vi.fn() },
          ]}
        />,
      )
    })

    const results = container.querySelector('[aria-label="Omnibox results"]')
    expect(results?.textContent).toContain('omnibox.sectionCommand')
    expect(results?.textContent).toContain('/clear')
    expect(results?.textContent).toContain('omnibox.sectionSkill')
    expect(results?.textContent).toContain('review')
    expect(results?.textContent).toContain('omnibox.sectionBundle')
    expect(results?.textContent).toContain('Team Bundle')
    expect(results?.textContent).toContain('omnibox.sectionComponentMode')
    expect(results?.textContent).toContain('Plan Mode')
    expect(results?.textContent).not.toContain('omnibox.sectionSettings')
    expect(results?.textContent).not.toContain('omnibox.sectionProject')

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    textarea.focus()
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })))

    expect(onChange).toHaveBeenCalledWith('/clear ')
    expect(document.activeElement).toBe(textarea)
  })

  it('mounts an exact builtin mode with Enter before slash-menu command selection', async () => {
    const onChange = vi.fn()
    const onEnterMode = vi.fn()
    await act(async () => {
      root.render(
        <AIComposer
          value="/goal"
          onChange={onChange}
          onSend={() => {}}
          onEnterMode={onEnterMode}
        />,
      )
    })

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })))

    expect(onEnterMode).toHaveBeenCalledWith('builtin:mode:goal')
    expect(onChange).not.toHaveBeenCalled()
  })

  it('runs a selected component action without inserting slash text', async () => {
    const onChange = vi.fn()
    const mount = vi.fn()
    await act(async () => {
      root.render(
        <AIComposer
          value="/team"
          onChange={onChange}
          onSend={() => {}}
          componentActions={[
            { id: 'component:team', section: 'bundle', label: 'Team Bundle', keywords: ['team'], score: 0, run: mount },
          ]}
        />,
      )
    })

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))

    expect(mount).toHaveBeenCalledTimes(1)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('matches a spaced-title bundle via its slug keywords on concatenated slash input', async () => {
    // Mirrors AIConversationComposer's componentActions keywords for a card
    // with id builtin:bundle:web-search and title "Web Search": the hyphenated
    // slug and compact form must let "/websearch" find the bundle.
    const mount = vi.fn()
    await act(async () => {
      root.render(
        <AIComposer
          value="/websearch"
          onChange={() => {}}
          onSend={() => {}}
          componentActions={[
            { id: 'component:builtin:bundle:web-search', section: 'bundle', label: 'Web Search', keywords: ['Web Search', 'web-search', 'websearch'], score: 0, run: mount },
          ]}
        />,
      )
    })

    const results = container.querySelector('[aria-label="Omnibox results"]')
    expect(results?.textContent).toContain('Web Search')

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))

    expect(mount).toHaveBeenCalledTimes(1)
  })

  it('keeps the slash menu open across spaces so spaced bundle names match', async () => {
    const mount = vi.fn()
    await act(async () => {
      root.render(
        <AIComposer
          value="/web search"
          onChange={() => {}}
          onSend={() => {}}
          componentActions={[
            { id: 'component:builtin:bundle:web-search', section: 'bundle', label: 'Web Search', keywords: ['Web Search', 'web-search', 'websearch'], score: 0, run: mount },
          ]}
        />,
      )
    })

    const results = container.querySelector('[aria-label="Omnibox results"]')
    expect(results?.textContent).toContain('Web Search')
  })

  it('cancels a pending slash-mode interception when the active agent changes', async () => {
    // A deferred mode-enter (the 250ms settle) must be scoped to the agent that
    // was active when the mode token was typed. onEnterMode rebinds to the
    // current agent on every render, so a pending interception that survives an
    // agent switch would mount the mode on the NEW agent's backend — leaking
    // workflow/goal mode across agents.
    vi.useFakeTimers()
    try {
      const onEnterMode = vi.fn()
      // Agent A: type "/goal" → interception timer armed (fires at 250ms).
      await act(async () => {
        root.render(
          <AIComposer
            value="/goal"
            onChange={() => {}}
            onSend={() => {}}
            onEnterMode={onEnterMode}
            agentActorId="agent-A"
          />,
        )
      })

      // Partway through the settle, switch to agent B while the input still
      // carries the mode token (the race window: onEnterMode has rebound to B
      // but the draft has not yet reset). Without scoping, agent A's timer
      // keeps running and fires onEnterMode against agent B.
      await act(async () => { vi.advanceTimersByTime(200) })
      await act(async () => {
        root.render(
          <AIComposer
            value="/goal"
            onChange={() => {}}
            onSend={() => {}}
            onEnterMode={onEnterMode}
            agentActorId="agent-B"
          />,
        )
      })

      // Past the ORIGINAL 250ms schedule. With agent-scoping the timer armed for
      // agent A was cancelled on the switch, so the enter never fires.
      await act(async () => { vi.advanceTimersByTime(60) })
      expect(onEnterMode).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })

  it('renders the # mention dropdown for matching cards', async () => {
    hoisted.mentionResults = [mcard('ProjectSummary')]
    await act(async () => {
      root.render(<AIComposer value="#proj" onChange={() => {}} onSend={() => {}} />)
    })

    const results = container.querySelector('[aria-label="Omnibox results"]')
    expect(results?.textContent).toContain('omnibox.sectionCard')
    expect(results?.textContent).toContain('ProjectSummary')
  })

  it('does not open the card mention menu when # is not at a token boundary', async () => {
    hoisted.mentionResults = [mcard('Embedded')]
    await act(async () => {
      root.render(<AIComposer value="a#b" onChange={() => {}} onSend={() => {}} />)
    })
    expect(container.querySelector('[aria-label="Omnibox results"]')).toBeNull()
  })

  it('does not open the card mention menu for @ tokens (those are agent mentions)', async () => {
    hoisted.mentionResults = [mcard('ProjectSummary')]
    await act(async () => {
      root.render(<AIComposer value="@proj" onChange={() => {}} onSend={() => {}} />)
    })
    expect(container.querySelector('[aria-label="Omnibox results"]')).toBeNull()
  })

  it('selecting a mention removes the #query fragment and reports the card', async () => {
    hoisted.mentionResults = [mcard('ProjectSummary')]
    const onChange = vi.fn()
    const onMentionCard = vi.fn()
    await act(async () => {
      root.render(<AIComposer value="hello #proj" onChange={onChange} onSend={() => {}} onMentionCard={onMentionCard} />)
    })

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })))

    expect(onChange).toHaveBeenCalledWith('hello')
    expect(onMentionCard).toHaveBeenCalledWith('ProjectSummary', 'ProjectSummary')
  })

  it('arrow keys move the selection and Enter confirms the highlighted card', async () => {
    hoisted.mentionResults = [mcard('Alpha'), mcard('Beta')]
    const onChange = vi.fn()
    const onMentionCard = vi.fn()
    await act(async () => {
      root.render(<AIComposer value="#a" onChange={onChange} onSend={() => {}} onMentionCard={onMentionCard} />)
    })

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true })))
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })))

    expect(onMentionCard).toHaveBeenCalledWith('Beta', 'Beta')
    expect(onChange).toHaveBeenCalledWith('')
  })

  it('Escape dismisses the mention menu without selecting', async () => {
    hoisted.mentionResults = [mcard('Alpha')]
    const onChange = vi.fn()
    const onMentionCard = vi.fn()
    await act(async () => {
      root.render(<AIComposer value="#a" onChange={onChange} onSend={() => {}} onMentionCard={onMentionCard} />)
    })

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })))

    expect(onMentionCard).not.toHaveBeenCalled()
    expect(onChange).not.toHaveBeenCalled()
    expect(container.querySelector('[aria-label="Omnibox results"]')).toBeNull()
  })

  it('renders the $ file mention dropdown for matching files', async () => {
    hoisted.fileResults = ['cmd/main.go']
    await act(async () => {
      root.render(<AIComposer value="$main" onChange={() => {}} onSend={() => {}} />)
    })

    const results = container.querySelector('[aria-label="Omnibox results"]')
    expect(results?.textContent).toContain('omnibox.sectionFile')
    expect(results?.textContent).toContain('cmd/main.go')
  })

  it('does not open the file mention menu when $ is not at a token boundary', async () => {
    hoisted.fileResults = ['ab.go']
    await act(async () => {
      root.render(<AIComposer value="a$b" onChange={() => {}} onSend={() => {}} />)
    })
    expect(container.querySelector('[aria-label="Omnibox results"]')).toBeNull()
  })

  it('selecting a file removes the $query fragment and reports the path', async () => {
    hoisted.fileResults = ['cmd/main.go']
    const onChange = vi.fn()
    const onMentionFile = vi.fn()
    await act(async () => {
      root.render(<AIComposer value="hello $main" onChange={onChange} onSend={() => {}} onMentionFile={onMentionFile} />)
    })

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })))

    expect(onChange).toHaveBeenCalledWith('hello')
    expect(onMentionFile).toHaveBeenCalledWith('cmd/main.go')
  })

  it('arrow keys move the file selection and Enter confirms the highlighted file', async () => {
    hoisted.fileResults = ['a.go', 'b.go']
    const onChange = vi.fn()
    const onMentionFile = vi.fn()
    await act(async () => {
      root.render(<AIComposer value="$x" onChange={onChange} onSend={() => {}} onMentionFile={onMentionFile} />)
    })

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true })))
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })))

    expect(onMentionFile).toHaveBeenCalledWith('b.go')
    expect(onChange).toHaveBeenCalledWith('')
  })

  it('Escape dismisses the file mention menu without selecting', async () => {
    hoisted.fileResults = ['a.go']
    const onChange = vi.fn()
    const onMentionFile = vi.fn()
    await act(async () => {
      root.render(<AIComposer value="$a" onChange={onChange} onSend={() => {}} onMentionFile={onMentionFile} />)
    })

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })))

    expect(onMentionFile).not.toHaveBeenCalled()
    expect(onChange).not.toHaveBeenCalled()
    expect(container.querySelector('[aria-label="Omnibox results"]')).toBeNull()
  })

  it('renders the @ agent mention section without file results', async () => {
    hoisted.agentMentionResults = [magent({
      Id: 'agent-alpha', ActorId: 'actor-alpha', DisplayName: 'Alpha Agent',
      ProjectName: 'Sporemind', Title: 'Alpha Agent', HasTitle: false,
    })]
    hoisted.fileResults = ['cmd/main.go']
    await act(async () => {
      root.render(<AIComposer value="@a" onChange={() => {}} onSend={() => {}} />)
    })

    const results = container.querySelector('[aria-label="Omnibox results"]')
    expect(results?.textContent).toContain('omnibox.sectionAgent')
    expect(results?.textContent).toContain('Alpha Agent')
    // The agent row must surface the project name so agents sharing a display
    // name across projects are disambiguable.
    expect(results?.textContent).toContain('Sporemind')
    // '@' lists agents only — files moved to the '$' trigger.
    expect(results?.textContent).not.toContain('omnibox.sectionFile')
  })

  it('shows the agent title when the agent has one distinct from its display name', async () => {
    hoisted.agentMentionResults = [magent({
      Id: 'agent-beta', ActorId: 'actor-beta', DisplayName: 'Beta',
      ProjectName: 'Project X', Title: 'Ship the conversable tag', HasTitle: true,
    })]
    hoisted.fileResults = []
    await act(async () => {
      root.render(<AIComposer value="@b" onChange={() => {}} onSend={() => {}} />)
    })

    const results = container.querySelector('[aria-label="Omnibox results"]')
    expect(results?.textContent).toContain('Beta')
    expect(results?.textContent).toContain('Project X')
    expect(results?.textContent).toContain('Ship the conversable tag')
  })

  it('selecting an agent removes the @query fragment and reports the agent', async () => {
    hoisted.agentMentionResults = [magent({ Id: 'agent-alpha', ActorId: 'actor-alpha', DisplayName: 'Alpha' })]
    hoisted.fileResults = []
    const onChange = vi.fn()
    const onMentionAgent = vi.fn()
    await act(async () => {
      root.render(<AIComposer value="hello @alp" onChange={onChange} onSend={() => {}} onMentionAgent={onMentionAgent} />)
    })

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })))

    expect(onChange).toHaveBeenCalledWith('hello')
    expect(onMentionAgent).toHaveBeenCalledWith('agent-alpha', 'Alpha', 'actor-alpha')
    // Selecting a mention must not auto-submit.
    expect(onChange).toHaveBeenCalledTimes(1)
  })

  it('rankAgentMentionResults keeps only loaded agents and excludes the current agent', () => {
    const items = [
      magent({ Id: 'u1', ActorId: 'actor-u', DisplayName: 'Unloaded', LoadState: 'unloaded' }),
      magent({ Id: 's1', ActorId: 'actor-self', DisplayName: 'SelfAgent', LoadState: 'loaded' }),
      magent({ Id: 'a1', ActorId: 'actor-a', DisplayName: 'Alpha', LoadState: 'loaded' }),
    ]
    const ranked = rankAgentMentionResults(items as any, '', 'actor-self')
    expect(ranked.map(a => a.Id)).toEqual(['a1'])
  })

  it('rankAgentMentionResults ranks DisplayName/Id matches by earliest position', () => {
    const items = [
      magent({ Id: 'id-late', ActorId: 'actor-l', DisplayName: 'Zebra', LoadState: 'loaded' }),
      magent({ Id: 'AgentAlpha#1', ActorId: 'actor-a', DisplayName: 'Agent Alpha', LoadState: 'loaded' }),
      magent({ Id: 'id-early', ActorId: 'actor-e', DisplayName: 'Alpine', LoadState: 'loaded' }),
    ]
    const ranked = rankAgentMentionResults(items as any, 'alp', 'actor-self')
    // 'Alpine' matches at position 0; 'AgentAlpha#1' matches 'alpha' in its Id.
    expect(ranked.map(a => a.Id)).toEqual(['id-early', 'AgentAlpha#1', 'id-late'])
  })

  it('rankAgentMentionResults surfaces initial-matching agents first (SB → Syntax Blob)', () => {
    const items = [
      magent({ Id: 'id-suffix', ActorId: 'actor-x', DisplayName: 'Debug Buddy', LoadState: 'loaded' }),
      magent({ Id: 'agent-sb', ActorId: 'actor-sb', DisplayName: 'Syntax Blob', LoadState: 'loaded' }),
    ]
    const ranked = rankAgentMentionResults(items as any, 'sb', 'actor-self')
    // 'Syntax Blob' initials 'sb' match at 0; 'Debug Buddy' has no match and keeps input order after.
    expect(ranked.map(a => a.Id)).toEqual(['agent-sb', 'id-suffix'])
  })

  it('nameInitials extracts word-initial letters across spaces and camelCase', () => {
    expect(nameInitials('Syntax Blob')).toBe('sb')
    expect(nameInitials('syntax blob')).toBe('sb')
    expect(nameInitials('SyntaxBlob')).toBe('sb')
    expect(nameInitials('Coordinator 与 Glass')).toBe('cg')
    expect(nameInitials('GPT-4 Agent')).toBe('ga')
    expect(nameInitials('')).toBe('')
  })

  it('renders the % browser mention section without agent results', async () => {
    hoisted.browserMentionResults = [{ instanceId: 'inst-1', name: 'Docs Browser', url: 'https://example.com' }]
    hoisted.agentMentionResults = [magent({ Id: 'agent-alpha', ActorId: 'actor-alpha', DisplayName: 'Alpha' })]
    await act(async () => {
      root.render(<AIComposer value="%doc" onChange={() => {}} onSend={() => {}} />)
    })

    const results = container.querySelector('[aria-label="Omnibox results"]')
    expect(results?.textContent).toContain('omnibox.sectionBrowser')
    expect(results?.textContent).toContain('Docs Browser')
    expect(results?.textContent).toContain('https://example.com')
    // '%' lists browsers only — agents stay on the '@' trigger.
    expect(results?.textContent).not.toContain('Alpha')
  })

  it('selecting a browser removes the %query fragment and reports the instance', async () => {
    hoisted.browserMentionResults = [{ instanceId: 'inst-1', name: 'Docs Browser', url: 'https://example.com' }]
    const onChange = vi.fn()
    const onMentionBrowser = vi.fn()
    await act(async () => {
      root.render(<AIComposer value="check %doc" onChange={onChange} onSend={() => {}} onMentionBrowser={onMentionBrowser} />)
    })

    const textarea = container.querySelector('.ai-composer-textarea') as HTMLTextAreaElement
    await act(async () => textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })))

    expect(onChange).toHaveBeenCalledWith('check')
    expect(onMentionBrowser).toHaveBeenCalledWith('inst-1', 'Docs Browser', 'https://example.com')
    // Selecting a mention must not auto-submit.
    expect(onChange).toHaveBeenCalledTimes(1)
  })

  it('does not open the browser mention menu when % is not at a token boundary', async () => {
    hoisted.browserMentionResults = [{ instanceId: 'inst-1', name: 'Docs Browser', url: 'https://example.com' }]
    await act(async () => {
      root.render(<AIComposer value="100%doc" onChange={() => {}} onSend={() => {}} />)
    })

    expect(container.querySelector('[aria-label="Omnibox results"]')).toBeNull()
  })

  it('keeps the % browser mention trigger inert without Insider access', async () => {
    hoisted.insiderAccess = false
    hoisted.browserMentionResults = [{ instanceId: 'inst-1', name: 'Docs Browser', url: 'https://example.com' }]
    await act(async () => {
      root.render(<AIComposer value="%doc" onChange={() => {}} onSend={() => {}} />)
    })

    expect(container.querySelector('[aria-label="Omnibox results"]')).toBeNull()
  })

  it('calls onClick when a clickable badge body is clicked', async () => {
    const onClick = vi.fn()
    const onClose = vi.fn()
    const badges: ComposerBadge[] = [
      { icon: 'file-text', title: 'ProjectSummary', label: 'ProjectSummary', onClick, onClose },
    ]
    await renderComposer(badges)

    const badge = container.querySelector('.ai-composer-badge--clickable') as HTMLElement
    expect(badge).not.toBeNull()
    await act(async () => { badge.click() })
    expect(onClick).toHaveBeenCalledTimes(1)
    // Clicking the body must not trigger the close handler.
    expect(onClose).not.toHaveBeenCalled()
  })

  it('badge close button stops propagation so onClose fires without onClick', async () => {
    const onClick = vi.fn()
    const onClose = vi.fn()
    const badges: ComposerBadge[] = [
      { icon: 'file-text', title: 'ProjectSummary', label: 'ProjectSummary', onClick, onClose },
    ]
    await renderComposer(badges)

    const closeBtn = container.querySelector('.ai-composer-badge-close') as HTMLElement
    expect(closeBtn).not.toBeNull()
    await act(async () => { closeBtn.click() })
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(onClick).not.toHaveBeenCalled()
  })
})

describe('AIComposer permission dropdown tabs', () => {
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

  const openPermissionDropdown = async (extra?: Record<string, unknown>) => {
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          permissionMode="permission"
          onPermissionModeChange={vi.fn()}
          globalPermissionMode="allow-all"
          onGlobalPermissionModeChange={vi.fn()}
          {...extra}
        />,
      )
    })

    const btn = container.querySelector('.ai-composer-permission-btn') as HTMLElement
    expect(btn).not.toBeNull()
    await act(async () => { btn.click() })
  }

  it('shows tab bar when global props are provided', async () => {
    await openPermissionDropdown()
    const switchEl = container.querySelector('.ai-composer-permission-source-switch')
    expect(switchEl).not.toBeNull()
    const tabs = switchEl!.querySelectorAll('button')
    expect(tabs).toHaveLength(2)
    expect(tabs[0]!.textContent).toContain('composer.permission.tabCurrent')
    expect(tabs[1]!.textContent).toContain('composer.permission.tabGlobal')
  })

  it('hides tab bar when global props are not provided', async () => {
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          permissionMode="permission"
          onPermissionModeChange={vi.fn()}
        />,
      )
    })

    const btn = container.querySelector('.ai-composer-permission-btn') as HTMLElement
    expect(btn).not.toBeNull()
    await act(async () => { btn.click() })

    expect(container.querySelector('.ai-composer-permission-source-switch')).toBeNull()
    expect(container.querySelector('.ai-composer-permission-source-desc')).toBeNull()
  })

  it('defaults to Current tab and highlights the current agent mode', async () => {
    await openPermissionDropdown({ permissionMode: 'yolo' })
    const switchEl = container.querySelector('.ai-composer-permission-source-switch')!
    const tabs = switchEl.querySelectorAll('button')
    expect(tabs[0]!.classList.contains('active')).toBe(true)
    expect(tabs[1]!.classList.contains('active')).toBe(false)

    // The active item should be 'yolo' (the current agent mode)
    const activeItem = container.querySelector('.ai-composer-permission-item.active')
    expect(activeItem).not.toBeNull()
    expect(activeItem!.textContent).toContain('composer.permission.yolo')
  })

  it('switching to Global tab highlights the global default mode without closing dropdown', async () => {
    await openPermissionDropdown({ permissionMode: 'permission', globalPermissionMode: 'allow-all' })
    const switchEl = container.querySelector('.ai-composer-permission-source-switch')!
    const globalTab = switchEl.querySelectorAll('button')[1]!

    await act(async () => { globalTab.click() })

    expect(globalTab.classList.contains('active')).toBe(true)
    // Dropdown should still be open
    expect(container.querySelector('.ai-composer-permission-dropdown')).not.toBeNull()

    // Active item should now be 'allow-all' (the global mode)
    const activeItem = container.querySelector('.ai-composer-permission-item.active')
    expect(activeItem).not.toBeNull()
    expect(activeItem!.textContent).toContain('composer.permission.allowAll')
  })

  it('switching to a dangerous mode on Current tab requires confirmation', async () => {
    const onPermissionModeChange = vi.fn()
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          permissionMode="permission"
          onPermissionModeChange={onPermissionModeChange}
          globalPermissionMode="allow-all"
          onGlobalPermissionModeChange={vi.fn()}
        />,
      )
    })

    const btn = container.querySelector('.ai-composer-permission-btn') as HTMLElement
    await act(async () => { btn.click() })

    // Default tab is "current" — click the 'yolo' item (dangerous)
    const items = container.querySelectorAll('.ai-composer-permission-item')
    const yoloItem = Array.from(items).find(el => el.textContent?.includes('composer.permission.yolo')) as HTMLElement
    expect(yoloItem).not.toBeNull()
    await act(async () => { yoloItem.click() })

    // Confirmation dialog appears (portaled to body) and the change is not applied yet.
    const confirmEl = document.querySelector('.confirm-dialog') as HTMLElement
    expect(confirmEl).not.toBeNull()
    expect(onPermissionModeChange).not.toHaveBeenCalled()

    const confirmBtn = confirmEl.querySelector('.confirm-dialog-btn.confirm') as HTMLElement
    await act(async () => { confirmBtn.click() })
    expect(onPermissionModeChange).toHaveBeenCalledWith('yolo')
  })

  it('switching to a safe mode on Current tab applies immediately without confirmation', async () => {
    const onPermissionModeChange = vi.fn()
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          permissionMode="permission"
          onPermissionModeChange={onPermissionModeChange}
          globalPermissionMode="allow-all"
          onGlobalPermissionModeChange={vi.fn()}
        />,
      )
    })

    const btn = container.querySelector('.ai-composer-permission-btn') as HTMLElement
    await act(async () => { btn.click() })

    const items = container.querySelectorAll('.ai-composer-permission-item')
    const autopilotItem = Array.from(items).find(el => el.textContent?.includes('composer.permission.autopilot')) as HTMLElement
    expect(autopilotItem).not.toBeNull()
    await act(async () => { autopilotItem.click() })

    expect(document.querySelector('.confirm-dialog')).toBeNull()
    expect(onPermissionModeChange).toHaveBeenCalledWith('autopilot')
  })

  it('cancelling the dangerous-mode confirmation applies nothing', async () => {
    const onPermissionModeChange = vi.fn()
    await openPermissionDropdown({ onPermissionModeChange })

    const items = container.querySelectorAll('.ai-composer-permission-item')
    const allowAllItem = Array.from(items).find(el => el.textContent?.includes('composer.permission.allowAll')) as HTMLElement
    expect(allowAllItem).not.toBeNull()
    await act(async () => { allowAllItem.click() })

    const confirmEl = document.querySelector('.confirm-dialog') as HTMLElement
    expect(confirmEl).not.toBeNull()
    const cancelBtn = confirmEl.querySelector('.confirm-dialog-btn.cancel') as HTMLElement
    await act(async () => { cancelBtn.click() })
    expect(onPermissionModeChange).not.toHaveBeenCalled()
    expect(document.querySelector('.confirm-dialog')).toBeNull()
  })

  it('switching to a dangerous mode on Global tab requires confirmation', async () => {
    const onGlobalPermissionModeChange = vi.fn()
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          permissionMode="permission"
          onPermissionModeChange={vi.fn()}
          globalPermissionMode="allow-all"
          onGlobalPermissionModeChange={onGlobalPermissionModeChange}
        />,
      )
    })

    const btn = container.querySelector('.ai-composer-permission-btn') as HTMLElement
    await act(async () => { btn.click() })

    // Switch to Global tab
    const switchEl = container.querySelector('.ai-composer-permission-source-switch')!
    const globalTab = switchEl.querySelectorAll('button')[1]!
    await act(async () => { globalTab.click() })

    // Click the 'yolo' item (dangerous)
    const items = container.querySelectorAll('.ai-composer-permission-item')
    const yoloItem = Array.from(items).find(el => el.textContent?.includes('composer.permission.yolo')) as HTMLElement
    expect(yoloItem).not.toBeNull()
    await act(async () => { yoloItem.click() })

    const confirmEl = document.querySelector('.confirm-dialog') as HTMLElement
    expect(confirmEl).not.toBeNull()
    expect(onGlobalPermissionModeChange).not.toHaveBeenCalled()

    const confirmBtn = confirmEl.querySelector('.confirm-dialog-btn.confirm') as HTMLElement
    await act(async () => { confirmBtn.click() })
    expect(onGlobalPermissionModeChange).toHaveBeenCalledWith('yolo')
  })

  it('button body always shows the current agent mode regardless of active tab', async () => {
    await openPermissionDropdown({ permissionMode: 'auto', globalPermissionMode: 'yolo' })

    // Switch to Global tab
    const switchEl = container.querySelector('.ai-composer-permission-source-switch')!
    const globalTab = switchEl.querySelectorAll('button')[1]!
    await act(async () => { globalTab.click() })

    // Button should still show 'auto' (the agent's actual mode)
    const btn = container.querySelector('.ai-composer-permission-btn') as HTMLElement
    expect(btn.textContent).toContain('composer.permission.bypass')
  })
})

describe('AIComposer reference rows', () => {
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

  const buildItems = (models: ProviderOption[], refs?: ProviderGroup['refs']): ProviderItem[] => {
    const items: ProviderItem[] = models.map(m => ({ kind: 'model', option: m }))
    if (refs) for (const r of refs) items.push({ kind: 'ref', option: r })
    return items
  }

  const makeGroups = (overrides?: {
    parentRefs?: ProviderGroup['refs']
    includeChild?: boolean
    refsOnly?: boolean
  }): ProviderGroup[] => {
    const childModels: ProviderOption[] = [
      {
        id: 'provider-a::model-x',
        label: 'model-x',
        subtitle: 'provider-a',
        unit: { model: 'model-x', provider: 'provider-a' },
        healthState: 'healthy',
      } as ProviderOption,
    ]
    const childRefs: ProviderGroup['refs'] = undefined
    const childGroup: ProviderGroup = {
      routeId: 'child-agg',
      label: 'Child Aggregator',
      isAuto: false,
      isRoot: true,
      models: childModels,
      items: buildItems(childModels, childRefs),
    }
    const parentModels: ProviderOption[] = overrides?.refsOnly
      ? []
      : [
          {
            id: 'provider-b::model-y',
            label: 'model-y',
            subtitle: 'provider-b',
            unit: { model: 'model-y', provider: 'provider-b' },
            healthState: 'healthy',
          } as ProviderOption,
        ]
    const parentRefs: ProviderGroup['refs'] = overrides?.parentRefs ?? [
      {
        aggregatorId: 'child-agg',
        label: 'Child Aggregator',
        healthState: 'healthy',
      },
    ]
    const parentGroup: ProviderGroup = {
      routeId: 'parent-agg',
      label: 'Parent Aggregator',
      isAuto: false,
      models: parentModels,
      refs: parentRefs,
      items: buildItems(parentModels, parentRefs),
    }
    const autoGroup: ProviderGroup = {
      routeId: 'system',
      label: 'Auto',
      isAuto: true,
      models: [],
      items: buildItems([], undefined),
    }
    const groups = [parentGroup]
    if (overrides?.includeChild !== false) groups.push(childGroup)
    groups.push(autoGroup)
    return groups
  }

  it('routeModelLabels recurses into nested child aggregators', () => {
    const groups = makeGroups()
    // Parent has model-y directly; child-agg contributes model-x through the ref.
    expect(routeModelLabels(groups, 'parent-agg')).toEqual(['model-y', 'model-x'])
    // Leaf route yields only its own models.
    expect(routeModelLabels(groups, 'child-agg')).toEqual(['model-x'])
  })

  it('routeModelLabels is cycle-safe', () => {
    const groups = makeGroups({
      parentRefs: [
        { aggregatorId: 'parent-agg', label: 'Self', healthState: 'healthy' },
      ],
    })
    expect(routeModelLabels(groups, 'parent-agg')).toEqual(['model-y'])
  })

  it('routeModelLabels handles a refs-only parent', () => {
    const groups = makeGroups({ refsOnly: true })
    expect(routeModelLabels(groups, 'parent-agg')).toEqual(['model-x'])
  })

  const openProviderDropdown = async (groups: ProviderGroup[], activeRoute: any = { kind: 'auto' }, onSelectUnit?: (routeId: string, option: ProviderOption) => void) => {
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          groups={groups}
          activeRoute={activeRoute}
          onSelectRoute={vi.fn()}
          onSelectUnit={onSelectUnit ?? vi.fn()}
        />,
      )
    })
    const btn = container.querySelector('.ai-composer-provider-btn') as HTMLElement
    expect(btn).not.toBeNull()
    await act(async () => { btn.click() })
  }

  const expandParentGroup = async () => {
    // The parent group should be auto-expanded when it's the active route,
    // but when active route is auto, we need to manually expand.
    const toggle = container.querySelector('.ai-composer-provider-route-toggle') as HTMLElement
    if (toggle) {
      await act(async () => { toggle.click() })
    }
  }

  it('renders reference rows inside expanded parent group', async () => {
    const groups = makeGroups()
    await openProviderDropdown(groups)
    await expandParentGroup()

    const refRows = container.querySelectorAll('.ai-composer-provider-ref-row')
    expect(refRows.length).toBe(1)
    expect(refRows[0]?.textContent).toContain('Child Aggregator')
  })

  it('reference row click soft-pins the child aggregator first available unit, not the route', async () => {
    const onSelectRoute = vi.fn()
    const onSelectUnit = vi.fn()
    const groups = makeGroups()
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          groups={groups}
          activeRoute={{ kind: 'auto' } as any}
          onSelectRoute={onSelectRoute}
          onSelectUnit={onSelectUnit}
        />,
      )
    })
    const btn = container.querySelector('.ai-composer-provider-btn') as HTMLElement
    await act(async () => { btn.click() })
    await expandParentGroup()

    const refRow = container.querySelector('.ai-composer-provider-ref-row') as HTMLElement
    expect(refRow).not.toBeNull()
    expect(refRow.tagName).not.toBe('BUTTON') // It's a div, not a button

    await act(async () => { refRow.click() })
    // Clicking a child-aggregator ref soft-pins the child's first available
    // unit through the ROOT aggregator — no route switch.
    expect(onSelectRoute).not.toHaveBeenCalled()
    expect(onSelectUnit).toHaveBeenCalledTimes(1)
    const [routeId, option] = onSelectUnit.mock.calls[0]!
    expect(routeId).toBe('parent-agg')
    expect(option.unit).toEqual({ model: 'model-x', provider: 'provider-a' })
  })

  it('reference row click does not expand the inline tree and pins the first available unit', async () => {
    const onSelectUnit = vi.fn()
    const groups = makeGroups()
    await openProviderDropdown(groups, { kind: 'auto' }, onSelectUnit)
    await expandParentGroup()

    const refRow = container.querySelector('.ai-composer-provider-ref-row') as HTMLElement
    expect(refRow).not.toBeNull()

    await act(async () => { refRow.click() })

    // The inline tree stays collapsed (expansion is via the chevron only);
    // the click pinned the child's unit instead of switching routes.
    expect(container.querySelectorAll('.ai-composer-provider-tree-node').length).toBe(0)
    expect(onSelectUnit).toHaveBeenCalledTimes(1)
    expect(onSelectUnit.mock.calls[0]![0]).toBe('parent-agg')
  })

  it('reference row click skips cooling and disabled units', async () => {
    const futureCooldown = Math.floor(Date.now() / 1000) + 300
    const childModels: ProviderOption[] = [
      {
        id: 'provider-a::model-cooling',
        label: 'model-cooling',
        subtitle: 'provider-a',
        unit: { model: 'model-cooling', provider: 'provider-a' },
        healthState: 'cooling_down',
        cooldownUntil: futureCooldown,
      } as ProviderOption,
      {
        id: 'provider-a::model-disabled',
        label: 'model-disabled',
        subtitle: 'provider-a',
        unit: { model: 'model-disabled', provider: 'provider-a' },
        healthState: 'disabled',
      } as ProviderOption,
      {
        id: 'provider-a::model-healthy',
        label: 'model-healthy',
        subtitle: 'provider-a',
        unit: { model: 'model-healthy', provider: 'provider-a' },
        healthState: 'healthy',
      } as ProviderOption,
    ]
    const childGroup: ProviderGroup = {
      routeId: 'child-agg',
      label: 'Child Aggregator',
      isAuto: false,
      isRoot: true,
      models: childModels,
      items: childModels.map(m => ({ kind: 'model', option: m })),
    }
    const ref: RefOption = { aggregatorId: 'child-agg', label: 'Child Aggregator', healthState: 'healthy' }
    const parentGroup: ProviderGroup = {
      routeId: 'parent-agg',
      label: 'Parent Aggregator',
      isAuto: false,
      models: [],
      refs: [ref],
      items: [{ kind: 'ref', option: ref }],
    }
    const onSelectUnit = vi.fn()
    await openProviderDropdown([parentGroup, childGroup], { kind: 'auto' }, onSelectUnit)
    await expandParentGroup()

    const refRow = container.querySelector('.ai-composer-provider-ref-row') as HTMLElement
    await act(async () => { refRow.click() })

    expect(onSelectUnit).toHaveBeenCalledTimes(1)
    expect(onSelectUnit.mock.calls[0]![1].unit).toEqual({ model: 'model-healthy', provider: 'provider-a' })
  })

  it('reference row click does nothing when the child has no available unit', async () => {
    const futureCooldown = Math.floor(Date.now() / 1000) + 300
    const childModels: ProviderOption[] = [
      {
        id: 'provider-a::model-cooling',
        label: 'model-cooling',
        subtitle: 'provider-a',
        unit: { model: 'model-cooling', provider: 'provider-a' },
        healthState: 'cooling_down',
        cooldownUntil: futureCooldown,
      } as ProviderOption,
      {
        id: 'provider-a::model-disabled',
        label: 'model-disabled',
        subtitle: 'provider-a',
        unit: { model: 'model-disabled', provider: 'provider-a' },
        healthState: 'disabled',
      } as ProviderOption,
    ]
    const childGroup: ProviderGroup = {
      routeId: 'child-agg',
      label: 'Child Aggregator',
      isAuto: false,
      isRoot: true,
      models: childModels,
      items: childModels.map(m => ({ kind: 'model', option: m })),
    }
    const refOption: RefOption = { aggregatorId: 'child-agg', label: 'Child Aggregator', healthState: 'healthy' }
    const parentGroup: ProviderGroup = {
      routeId: 'parent-agg',
      label: 'Parent Aggregator',
      isAuto: false,
      models: [],
      refs: [refOption],
      items: [{ kind: 'ref', option: refOption }],
    }
    const onSelectUnit = vi.fn()
    await openProviderDropdown([parentGroup, childGroup], { kind: 'auto' }, onSelectUnit)
    await expandParentGroup()

    const refRow = container.querySelector('.ai-composer-provider-ref-row') as HTMLElement
    await act(async () => { refRow.click() })

    expect(onSelectUnit).not.toHaveBeenCalled()
  })

  it('renders nested aggregator as an independent top-level route row', async () => {
    const groups = makeGroups()
    await openProviderDropdown(groups)

    const routeLabels = Array.from(container.querySelectorAll('.ai-composer-provider-route-label'))
      .map(el => el.textContent)
    expect(routeLabels).toContain('Parent Aggregator')
    expect(routeLabels).toContain('Child Aggregator')
  })

  it('shows stale label when child aggregator is not in the group list', async () => {
    const groups = makeGroups({ includeChild: false })
    await openProviderDropdown(groups)
    await expandParentGroup()

    const refRow = container.querySelector('.ai-composer-provider-ref-row') as HTMLElement
    expect(refRow).not.toBeNull()
    expect(refRow.classList.contains('is-stale')).toBe(true)
    expect(refRow.textContent).toContain('composer.ref.stale')
  })

  it('does not produce empty-label model options for reference entries', async () => {
    const groups = makeGroups()
    await openProviderDropdown(groups)
    await expandParentGroup()

    // All model options should have non-empty labels
    const items = container.querySelectorAll('.ai-composer-provider-item-label')
    for (const item of items) {
      const text = item.textContent ?? ''
      expect(text.length).toBeGreaterThan(0)
    }
  })

  it('renders health badge for cooling-down reference', async () => {
    const futureCooldown = Math.floor(Date.now() / 1000) + 300
    const groups = makeGroups({
      parentRefs: [{
        aggregatorId: 'child-agg',
        label: 'Child Aggregator',
        healthState: 'cooling_down',
        cooldownUntil: futureCooldown,
      }],
    })
    await openProviderDropdown(groups)
    await expandParentGroup()

    const refRow = container.querySelector('.ai-composer-provider-ref-row') as HTMLElement
    expect(refRow).not.toBeNull()
    const badge = refRow.querySelector('.ai-composer-provider-ref-badge.is-cooling')
    expect(badge).not.toBeNull()
  })

  it('renders health badge for disabled reference', async () => {
    const groups = makeGroups({
      parentRefs: [{
        aggregatorId: 'child-agg',
        label: 'Child Aggregator',
        healthState: 'disabled',
        healthReason: 'availability',
      }],
    })
    await openProviderDropdown(groups)
    await expandParentGroup()

    const refRow = container.querySelector('.ai-composer-provider-ref-row') as HTMLElement
    expect(refRow).not.toBeNull()
    const badge = refRow.querySelector('.ai-composer-provider-ref-badge.is-disabled')
    expect(badge).not.toBeNull()
  })

  it('renders dispatch activity badge on reference row', async () => {
    const groups = makeGroups({
      parentRefs: [{
        aggregatorId: 'child-agg',
        label: 'Child Aggregator',
        healthState: 'healthy',
        dispatchActivity: { State: 'in_use', SessionId: 's1', AgentId: 'a1', Depth: 1 },
      } as any],
    })
    await openProviderDropdown(groups)
    await expandParentGroup()

    const refRow = container.querySelector('.ai-composer-provider-ref-row') as HTMLElement
    expect(refRow).not.toBeNull()
    const badge = refRow.querySelector('.ai-composer-provider-activity.is-in_use')
    expect(badge).not.toBeNull()
    expect(badge?.textContent).toContain('composer.activity.inUse')
  })

  it('marks a reference row with a check when its aggregator is on the active dispatch chain', async () => {
    const groups = makeGroups({
      parentRefs: [{
        aggregatorId: 'child-agg',
        label: 'Child Aggregator',
        healthState: 'healthy',
        dispatchActivity: { State: 'in_use', SessionId: 's1', AgentId: 'a1', Depth: 1 },
      } as any],
    })
    await openProviderDropdown(groups)
    await expandParentGroup()

    const refRow = container.querySelector('.ai-composer-provider-ref-row') as HTMLElement
    expect(refRow).not.toBeNull()
    expect(refRow!.classList.contains('is-active')).toBe(true)
    expect(refRow!.querySelector('.ai-composer-provider-ref-check')).not.toBeNull()
  })

  it('does not check a reference row with no dispatch activity', async () => {
    const groups = makeGroups() // parent refs carry no dispatchActivity
    await openProviderDropdown(groups)
    await expandParentGroup()

    const refRow = container.querySelector('.ai-composer-provider-ref-row') as HTMLElement
    expect(refRow).not.toBeNull()
    expect(refRow!.classList.contains('is-active')).toBe(false)
    expect(refRow!.querySelector('.ai-composer-provider-ref-check')).toBeNull()
  })

  it('shows an expand toggle for a refs-only parent group and reveals refs when unselected', async () => {
    const groups = makeGroups({ refsOnly: true })
    // activeRoute is auto, so the parent group is unselected
    await openProviderDropdown(groups)

    // The refs-only group must still expose an expand entry.
    const toggle = container.querySelector('.ai-composer-provider-route-toggle') as HTMLElement
    expect(toggle).not.toBeNull()

    // Collapsed and unselected: refs content is not rendered yet.
    expect(container.querySelectorAll('.ai-composer-provider-ref-row').length).toBe(0)

    // Expanding the group reveals the refs content.
    await act(async () => { toggle.click() })
    const refRows = container.querySelectorAll('.ai-composer-provider-ref-row')
    expect(refRows.length).toBe(1)
    expect(refRows[0]?.textContent).toContain('Child Aggregator')
    expect(container.querySelectorAll('.ai-composer-provider-item').length).toBe(0)
  })

  it('auto-expands a refs-only parent group when it is the selected route', async () => {
    const groups = makeGroups({ refsOnly: true })
    await openProviderDropdown(
      groups,
      { kind: 'aggregator', aggregatorId: 'parent-agg' } as any,
    )

    // Selected route defaults to expanded, so refs are visible without a click.
    const refRows = container.querySelectorAll('.ai-composer-provider-ref-row')
    expect(refRows.length).toBe(1)
    expect(refRows[0]?.textContent).toContain('Child Aggregator')
  })

  it('expands a ref into an inline tree and shows the child models', async () => {
    const groups = makeGroups()
    await openProviderDropdown(groups)
    await expandParentGroup()

    const refToggle = container.querySelector('.ai-composer-provider-ref-toggle') as HTMLElement
    expect(refToggle).not.toBeNull()
    // Before expanding, the child model is not rendered inline.
    expect(container.querySelectorAll('.ai-composer-provider-tree-node').length).toBe(0)

    await act(async () => { refToggle.click() })
    const treeNodes = container.querySelectorAll('.ai-composer-provider-tree-node')
    expect(treeNodes.length).toBe(1)
    expect(treeNodes[0]?.textContent).toContain('model-x')
  })

  it('nested unit selection passes the root route ID, not the child route ID', async () => {
    const groups = makeGroups()
    const onSelectUnit = vi.fn()
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          groups={groups}
          activeRoute={{ kind: 'auto' }}
          onSelectRoute={vi.fn()}
          onSelectUnit={onSelectUnit}
        />,
      )
    })
    const btn = container.querySelector('.ai-composer-provider-btn') as HTMLElement
    await act(async () => { btn.click() })

    // Expand parent group.
    const parentToggle = container.querySelector('.ai-composer-provider-route-toggle') as HTMLElement
    await act(async () => { parentToggle.click() })

    // Expand child ref.
    const refToggle = container.querySelector('.ai-composer-provider-ref-toggle') as HTMLElement
    await act(async () => { refToggle.click() })

    // Click the nested model — should pass 'parent-agg' (root), not 'child-agg'.
    const treeNode = container.querySelector('.ai-composer-provider-tree-node') as HTMLElement
    expect(treeNode).not.toBeNull()
    const option = treeNode.querySelector('[role="button"], button, .ai-composer-provider-option') as HTMLElement
    const clickTarget = option || treeNode
    await act(async () => { clickTarget.click() })

    expect(onSelectUnit).toHaveBeenCalledTimes(1)
    const [routeId, ] = onSelectUnit.mock.calls[0]!
    expect(routeId).toBe('parent-agg')
  })

  it('checks the child aggregator ref row when its nested unit is pinned', async () => {
    const groups = makeGroups()
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          groups={groups}
          activeRoute={{
            kind: 'unit',
            unit: { model: 'model-x', provider: 'provider-a' },
            servingAggregatorId: 'parent-agg',
            failover: 'auto',
          } as any}
          onSelectRoute={vi.fn()}
          onSelectUnit={vi.fn()}
        />,
      )
    })
    const btn = container.querySelector('.ai-composer-provider-btn') as HTMLElement
    await act(async () => { btn.click() })

    // Expand parent group to reveal the child ref row.
    const parentToggle = container.querySelector('.ai-composer-provider-route-toggle') as HTMLElement
    await act(async () => { parentToggle.click() })

    const refRow = container.querySelector('.ai-composer-provider-ref-row') as HTMLElement
    expect(refRow).not.toBeNull()
    expect(refRow!.classList.contains('is-active')).toBe(true)
    expect(refRow!.querySelector('.ai-composer-provider-ref-check')).not.toBeNull()
  })

  it('clicking the root aggregator route row while soft-pinned emits the release route selection', async () => {
    const onSelectRoute = vi.fn()
    const onSelectUnit = vi.fn()
    const groups = makeGroups()
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          groups={groups}
          activeRoute={{
            kind: 'unit',
            unit: { model: 'model-x', provider: 'provider-a' },
            servingAggregatorId: 'parent-agg',
            failover: 'auto',
          } as any}
          onSelectRoute={onSelectRoute}
          onSelectUnit={onSelectUnit}
        />,
      )
    })
    const btn = container.querySelector('.ai-composer-provider-btn') as HTMLElement
    await act(async () => { btn.click() })

    // The soft-pinned serving aggregator row is already active; clicking it
    // again must still fire onSelectRoute so the handler can replace the
    // [unit@agg] pin with a pure [aggregator] slot (release the soft pin).
    const routeRows = Array.from(container.querySelectorAll<HTMLButtonElement>('.ai-composer-provider-route'))
    const parentRow = routeRows.find(r => r.textContent?.includes('Parent Aggregator'))
    expect(parentRow).toBeDefined()
    expect(parentRow!.classList.contains('active')).toBe(true)

    await act(async () => { parentRow!.click() })
    expect(onSelectRoute).toHaveBeenCalledTimes(1)
    expect(onSelectRoute).toHaveBeenCalledWith('parent-agg')
    expect(onSelectUnit).not.toHaveBeenCalled()
  })

  it('expands nested refs multiple levels deep', async () => {
    const grandchildModels: ProviderOption[] = [
      {
        id: 'provider-c::model-z',
        label: 'model-z',
        subtitle: 'provider-c',
        unit: { model: 'model-z', provider: 'provider-c' },
        healthState: 'healthy',
      } as ProviderOption,
    ]
    const grandchildGroup: ProviderGroup = {
      routeId: 'grandchild-agg',
      label: 'Grandchild Aggregator',
      isAuto: false,
      models: grandchildModels,
      items: buildItems(grandchildModels, undefined),
    }
    const childModels: ProviderOption[] = [
      {
        id: 'provider-a::model-x',
        label: 'model-x',
        subtitle: 'provider-a',
        unit: { model: 'model-x', provider: 'provider-a' },
        healthState: 'healthy',
      } as ProviderOption,
    ]
    const childRefs = [{ aggregatorId: 'grandchild-agg', label: 'Grandchild Aggregator', healthState: 'healthy' }] as const
    const childGroup: ProviderGroup = {
      routeId: 'child-agg',
      label: 'Child Aggregator',
      isAuto: false,
      models: childModels,
      refs: [...childRefs],
      items: buildItems(childModels, [...childRefs]),
    }
    const parentRefs = [{ aggregatorId: 'child-agg', label: 'Child Aggregator', healthState: 'healthy' }] as const
    const parentGroup: ProviderGroup = {
      routeId: 'parent-agg',
      label: 'Parent Aggregator',
      isAuto: false,
      models: [],
      refs: [...parentRefs],
      items: buildItems([], [...parentRefs]),
    }
    const groups = [parentGroup, childGroup, grandchildGroup]
    await openProviderDropdown(groups)
    await expandParentGroup()

    // Expand parent ref → child inline.
    const parentRefToggle = container.querySelector('.ai-composer-provider-ref-toggle') as HTMLElement
    await act(async () => { parentRefToggle.click() })
    expect(container.querySelectorAll('.ai-composer-provider-tree-node').length).toBe(1)
    expect(container.querySelectorAll('.ai-composer-provider-ref-toggle').length).toBe(2)

    // Expand child ref → grandchild inline.
    const childRefToggle = Array.from(container.querySelectorAll('.ai-composer-provider-ref-toggle'))[1] as HTMLElement
    await act(async () => { childRefToggle.click() })
    const treeNodes = container.querySelectorAll('.ai-composer-provider-tree-node')
    expect(treeNodes.length).toBe(2)
    expect(treeNodes[1]?.textContent).toContain('model-z')
  })

  it('stops expansion on a circular ref and shows a cycle indicator', async () => {
    const childModels: ProviderOption[] = [
      {
        id: 'provider-a::model-x',
        label: 'model-x',
        subtitle: 'provider-a',
        unit: { model: 'model-x', provider: 'provider-a' },
        healthState: 'healthy',
      } as ProviderOption,
    ]
    const childRefs = [{ aggregatorId: 'parent-agg', label: 'Parent Aggregator', healthState: 'healthy' }] as const
    const childGroup: ProviderGroup = {
      routeId: 'child-agg',
      label: 'Child Aggregator',
      isAuto: false,
      models: childModels,
      refs: [...childRefs],
      items: buildItems(childModels, [...childRefs]),
    }
    const parentRefs = [{ aggregatorId: 'child-agg', label: 'Child Aggregator', healthState: 'healthy' }] as const
    const parentGroup: ProviderGroup = {
      routeId: 'parent-agg',
      label: 'Parent Aggregator',
      isAuto: false,
      models: [],
      refs: [...parentRefs],
      items: buildItems([], [...parentRefs]),
    }
    const groups = [parentGroup, childGroup]
    await openProviderDropdown(groups)
    await expandParentGroup()

    // Expand parent ref → child inline.
    const parentRefToggle = container.querySelector('.ai-composer-provider-ref-toggle') as HTMLElement
    await act(async () => { parentRefToggle.click() })

    // The child's ref back to parent should be marked as a cycle and not expandable.
    const cycleRow = container.querySelector('.ai-composer-provider-ref-row.is-cycle') as HTMLElement
    expect(cycleRow).not.toBeNull()
    expect(cycleRow.textContent).toContain('composer.tree.cycle')
    expect(cycleRow.querySelector('.ai-composer-provider-ref-toggle')).toBeNull()

    // No further tree branch should be rendered for the cycle.
    expect(container.querySelectorAll('.ai-composer-provider-tree-node').length).toBe(1)
  })

  it('renders dispatch activity markers for waiting/trying/in_use units', async () => {
    const activityOption = (state: string): ProviderOption => ({
      id: `provider-a::model-${state}`,
      label: `model-${state}`,
      subtitle: 'provider-a',
      unit: { model: `model-${state}`, provider: 'provider-a' },
      healthState: 'healthy',
      dispatchActivity: { State: state } as DispatchActivity,
    })
    const groupModels: ProviderOption[] = [
      activityOption('waiting'),
      activityOption('trying'),
      activityOption('in_use'),
    ]
    const group: ProviderGroup = {
      routeId: 'custom-agg',
      label: 'Custom Aggregator',
      isAuto: false,
      models: groupModels,
      items: buildItems(groupModels, undefined),
    }
    const groups = [group]
    await openProviderDropdown(groups)

    // Expand the custom group so all models are visible.
    const routeToggle = container.querySelector('.ai-composer-provider-route-toggle') as HTMLElement
    await act(async () => { routeToggle.click() })

    const items = container.querySelectorAll('.ai-composer-provider-item')
    expect(items.length).toBe(3)
    expect(items[0]?.querySelector('.ai-composer-provider-activity.is-waiting')).not.toBeNull()
    expect(items[1]?.querySelector('.ai-composer-provider-activity.is-trying')).not.toBeNull()
    expect(items[2]?.querySelector('.ai-composer-provider-activity.is-in_use')).not.toBeNull()
  })

  it('renders bubbled dispatch activity badge on route row', async () => {
    const groups = makeGroups()
    const parent = groups.find(g => g.routeId === 'parent-agg')!
    parent.bubbledActivity = { State: 'trying', SessionId: 's1', AgentId: 'a1', Depth: 1 } as DispatchActivity
    await openProviderDropdown(groups)

    const routeRow = container.querySelector('.ai-composer-provider-route-row') as HTMLElement
    expect(routeRow).not.toBeNull()
    const badge = routeRow.querySelector('.ai-composer-provider-activity.is-trying')
    expect(badge).not.toBeNull()
    expect(badge?.textContent).toContain('composer.activity.trying')
  })
})

describe('AIComposer pause controls', () => {
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

  const renderComposer = async (props: Partial<ComponentProps<typeof AIComposer>> = {}) => {
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          {...props}
        />,
      )
    })
  }

  it('shows pause alongside the history button while waiting with empty input', async () => {
    const onPause = vi.fn()
    await renderComposer({ isWaiting: true, onPause })

    const group = container.querySelector('.ai-composer-right-group')
    expect(group).not.toBeNull()
    const buttons = group!.querySelectorAll('button')
    expect(buttons.length).toBe(2)
    expect(buttons[0]?.getAttribute('title')).toBe('ai.pauseAll')
    expect(buttons[1]?.className).toBe('ai-composer-history-btn')

    await act(async () => {
      buttons[0]?.click()
    })
    expect(onPause).toHaveBeenCalledTimes(1)
  })

  it('shows pause alongside send while waiting and submits new input', async () => {
    const onPause = vi.fn()
    const onSend = vi.fn()
    await renderComposer({ isWaiting: true, onPause, onSend, value: 'hello' })

    const sendButton = container.querySelector('.ai-composer-send')
    expect(sendButton).not.toBeNull()

    await act(async () => {
      sendButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onSend).toHaveBeenCalledTimes(1)
    expect(onPause).not.toHaveBeenCalled()
  })

  it('prefers the paused resume controls when both isWaiting and isPaused are set', async () => {
    const onResume = vi.fn()
    await renderComposer({ isWaiting: true, isPaused: true, onResume })

    const group = container.querySelector('.ai-composer-right-group')
    expect(group).not.toBeNull()
    const resumeButton = Array.from(group!.querySelectorAll('button'))
      .find(b => b.getAttribute('title') === 'Resume')
    expect(resumeButton).toBeTruthy()
    expect(Array.from(group!.querySelectorAll('button')).some(b => b.getAttribute('title') === 'ai.pauseAll')).toBe(false)

    await act(async () => {
      resumeButton!.click()
    })
    expect(onResume).toHaveBeenCalledTimes(1)
  })

  it('prefers the pausing spinner over the waiting pause button', async () => {
    const onPause = vi.fn()
    await renderComposer({ isWaiting: true, isPausing: true, onPause })

    const group = container.querySelector('.ai-composer-right-group')
    expect(group).not.toBeNull()
    const spinner = group!.querySelector('button.ai-composer-stop-pending')
    expect(spinner).not.toBeNull()
    expect(spinner?.getAttribute('title')).toBe('Pausing...')
    expect(group!.querySelectorAll('button').length).toBe(1)
    expect(onPause).not.toHaveBeenCalled()
  })

  it('clears the stop spinner when stopping an already non-streaming (paused) turn', async () => {
    const onStop = vi.fn()
    await renderComposer({ isPaused: true, onStop })

    const group = container.querySelector('.ai-composer-right-group')
    expect(group).not.toBeNull()
    const stopBtn = Array.from(group!.querySelectorAll('button')).find(
      b => b.getAttribute('title') === 'Stop',
    )
    expect(stopBtn).toBeTruthy()

    await act(async () => {
      stopBtn!.click()
    })
    expect(onStop).toHaveBeenCalledTimes(1)

    // isStreaming was already false (crash-recovery paused turn), so the
    // isStreaming true→false edge never fires — the reset must come from the
    // isStopping flip itself or the composer sticks on a disabled spinner.
    const spinner = container.querySelector('button.ai-composer-stop-pending')
    expect(spinner).toBeNull()
  })
})

describe('AIComposer provider dropdown routing state', () => {
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

  const opt = (model: string, provider: string): ProviderOption => ({
    id: `${provider}::${model}`,
    label: model,
    subtitle: provider,
    unit: { model, provider },
  })

  const autoGroup: ProviderGroup = {
    routeId: 'system',
    label: 'Auto',
    isAuto: true,
    models: [opt('gpt-x', 'openai'), opt('claude-y', 'anthropic')],
    items: [
      { kind: 'model', option: opt('gpt-x', 'openai') },
      { kind: 'model', option: opt('claude-y', 'anthropic') },
    ],
  }

  const customGroup: ProviderGroup = {
    routeId: 'agg-1',
    label: 'Fast Pool',
    isAuto: false,
    models: [opt('fast-m', 'p2')],
    items: [{ kind: 'model', option: opt('fast-m', 'p2') }],
  }

  const openDropdown = async (props: Partial<ComponentProps<typeof AIComposer>> = {}) => {
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          groups={[customGroup, autoGroup]}
          activeRoute={props.activeRoute}
          currentUnit={props.currentUnit}
          onSelectRoute={props.onSelectRoute ?? vi.fn()}
          onSelectUnit={vi.fn()}
          {...props}
        />,
      )
    })
    const btn = container.querySelector('.ai-composer-provider-btn') as HTMLElement
    expect(btn).not.toBeNull()
    await act(async () => {
      btn.click()
    })
    expect(container.querySelector('.ai-composer-provider-dropdown')).not.toBeNull()
  }

  it('marks the Auto route header active with a check under [auto] routing', async () => {
    const onSelectRoute = vi.fn()
    await openDropdown({
      activeRoute: { kind: 'auto' },
      currentUnit: { model: 'claude-y', provider: 'anthropic' },
      onSelectRoute,
    })

    const autoWrap = container.querySelector('.ai-composer-provider-auto-group')
    expect(autoWrap).not.toBeNull()
    const header = autoWrap!.querySelector('.ai-composer-provider-route') as HTMLElement
    expect(header).not.toBeNull()
    expect(header.classList.contains('active')).toBe(true)
    expect(header.querySelector('svg')).not.toBeNull() // route Check

    // The custom route header is inactive under [auto] routing.
    expect(header.textContent).toContain('dialog.newAgent.autoFirst')
    const customHeader = Array.from(container.querySelectorAll('.ai-composer-provider-route'))
      .find(b => b.textContent?.includes('Fast Pool'))
    expect(customHeader).toBeTruthy()
    expect(customHeader!.classList.contains('active')).toBe(false)

    // Clicking the Auto header selects the system route.
    await act(async () => {
      header.click()
    })
    expect(onSelectRoute).toHaveBeenCalledWith('system')
  })

  it('checks the β-resolved unit inside the Auto pool under [auto] routing', async () => {
    await openDropdown({
      activeRoute: { kind: 'auto' },
      currentUnit: { model: 'claude-y', provider: 'anthropic' },
    })

    const activeRows = Array.from(container.querySelectorAll('.ai-composer-provider-auto-group .ai-composer-provider-item.active'))
    expect(activeRows.length).toBe(1)
    expect(activeRows[0]!.textContent).toContain('claude-y')
  })

  it('marks the serving aggregator header active when a unit is pinned through it', async () => {
    await openDropdown({
      activeRoute: { kind: 'unit', unit: { model: 'fast-m', provider: 'p2' }, servingAggregatorId: 'agg-1', failover: 'auto' },
      currentUnit: null,
    })

    const activeHeaders = Array.from(container.querySelectorAll('.ai-composer-provider-route.active'))
    expect(activeHeaders.length).toBe(1)
    expect(activeHeaders[0]!.textContent).toContain('Fast Pool')

    const autoHeader = container.querySelector('.ai-composer-provider-auto-group .ai-composer-provider-route')
    expect(autoHeader?.classList.contains('active')).toBe(false)
  })

  it('shows the β-resolved unit as the button subtitle under [auto] routing', async () => {
    await act(async () => {
      root.render(
        <AIComposer
          value=""
          onChange={() => {}}
          onSend={() => {}}
          groups={[customGroup, autoGroup]}
          activeRoute={{ kind: 'auto' }}
          currentUnit={{ model: 'claude-y', provider: 'anthropic' }}
        />,
      )
    })

    const subtitle = container.querySelector('.ai-composer-provider-subtitle')
    expect(subtitle).not.toBeNull()
    expect(subtitle!.textContent).toContain('anthropic · claude-y')
  })
})