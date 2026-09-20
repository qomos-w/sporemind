import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('useSlashCommands', () => {
  let container: HTMLDivElement | null = null
  let root: Root | null = null

  beforeEach(() => {
    vi.resetModules()
    vi.clearAllMocks()
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
  })

  // Renders a harness that calls the (dynamically imported) hook and flushes
  // the async fetch. vi.resetModules + dynamic import isolates the module-level
  // cache so each test starts fresh.
  async function renderHook(
    enabled: boolean,
    commands: { Name: string; ShortHelp: string }[],
    skills: { Id: string; Name: string; Description: string; Source?: string }[] = [],
    modes: { CardId: string; Name: string; Title: string; Icon: string }[] = [],
  ) {
    vi.doMock('../../../gen-clients/workspace/client', () => ({
      slashCommandsList: vi.fn().mockResolvedValue({ Commands: commands }),
      builtinModesList: vi.fn().mockResolvedValue({ Modes: modes }),
    }))
    vi.doMock('../../../application/generated-client', () => ({ client: {} }))
    // Use the real skill-card-store module so `resolveSkillOverrides` runs for
    // real; only `listSkills` is stubbed with the test fixtures.
    vi.doMock('../../../application/skill-card-store', async () => {
      const actual = await vi.importActual<typeof import('../../../application/skill-card-store')>(
        '../../../application/skill-card-store',
      )
      return { ...actual, listSkills: vi.fn().mockResolvedValue(skills) }
    })
    const { useSlashCommands } = await import('./useSlashCommands')
    const React = await import('react')

    let captured: ReturnType<typeof useSlashCommands> | null = null
    const Harness = () => {
      captured = useSlashCommands(enabled)
      return null
    }
    await act(async () => {
      root!.render(React.createElement(Harness))
    })
    // flush the async fetch microtask + resulting setState
    await act(async () => {})
    await act(async () => {})
    return { captured: captured! }
  }

  it('fetches and returns commands when enabled', async () => {
    const cmds = [
      { Name: 'clear', ShortHelp: 'Clear session' },
      { Name: 'help', ShortHelp: 'Show help' },
    ]
    const { captured } = await renderHook(true, cmds)
    expect(captured.commands).toHaveLength(2)
    expect(captured.commands.map((c) => c.Name)).toEqual(['clear', 'help'])
    expect(captured.loading).toBe(false)
    expect(captured.error).toBeNull()
  })

  it('includes skills alongside commands and keeps command entries on name collisions', async () => {
    const commands = [{ Name: 'clear', ShortHelp: 'Clear session' }]
    const skills = [
      { Id: 'review', Name: 'Review changes', Description: 'Review the current diff' },
      { Id: 'plan', Name: 'Plan module', Description: '' },
      { Id: 'clear', Name: 'Clear skill', Description: 'Duplicate name' },
    ]
    const { captured } = await renderHook(true, commands, skills)

    expect(captured.commands).toEqual([
      { Name: 'clear', ShortHelp: 'Clear session' },
    ])
    expect(captured.skills).toEqual([
      { Name: 'review', ShortHelp: 'Review the current diff' },
      { Name: 'plan', ShortHelp: 'Plan module' },
    ])
  })

  it('includes external skills not overridden by a same-name internal skill', async () => {
    const commands = [{ Name: 'clear', ShortHelp: 'Clear session' }]
    const skills = [
      { Id: 'review', Name: 'Review changes', Description: 'Review the current diff', Source: 'user' },
      { Id: 'ext-skill:claude:lint', Name: 'Lint', Description: 'External linter skill' },
    ]
    const { captured } = await renderHook(true, commands, skills)

    // The external skill `lint` has no same-name internal competitor, so it now
    // surfaces in the slash search (previously blanket-filtered out).
    expect(captured.skills).toEqual([
      { Name: 'review', ShortHelp: 'Review the current diff' },
      { Name: 'ext-skill:claude:lint', ShortHelp: 'External linter skill' },
    ])
  })

  it('hides an external skill overridden by a same-name internal skill', async () => {
    const commands = [{ Name: 'clear', ShortHelp: 'Clear session' }]
    const skills = [
      { Id: 'lint', Name: 'Lint', Description: 'Internal linter', Source: 'user' },
      { Id: 'ext-skill:claude:lint', Name: 'Lint', Description: 'External linter skill' },
    ]
    const { captured } = await renderHook(true, commands, skills)

    // Same name `lint`: the internal skill wins, the external entry is dropped.
    expect(captured.skills).toEqual([{ Name: 'lint', ShortHelp: 'Internal linter' }])
  })

  it('does not fetch when disabled', async () => {
    const cmds = [{ Name: 'clear', ShortHelp: 'Clear session' }]
    const { captured } = await renderHook(false, cmds)
    expect(captured.commands).toHaveLength(0)
    expect(captured.skills).toHaveLength(0)
    expect(captured.loading).toBe(false)
  })

  it('returns builtin modes for the slash-mode interception filter', async () => {
    const modes = [
      { CardId: 'builtin:mode:goal', Name: 'goal', Title: 'Goal Mode', Icon: 'target' },
      { CardId: 'builtin:mode:memory', Name: 'memory', Title: 'Memory Mode', Icon: 'database' },
    ]
    const { captured } = await renderHook(true, [], [], modes)
    expect(captured.modes).toHaveLength(2)
    expect(captured.modes.map((m) => m.Name)).toEqual(['goal', 'memory'])
  })
})
