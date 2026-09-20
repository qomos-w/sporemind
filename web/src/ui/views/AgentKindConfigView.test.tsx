import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AgentKindConfigView } from './AgentKindConfigView'
import { I18nProvider } from '../../i18n'
import { AIShellContext } from '../ai/context/AIShellContext'
import * as workspace from '../../gen-clients/workspace/client'
import * as skillCards from '../../application/skill-card-store'
import type { Skill } from '../../domain/skill-types'

(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../application/generated-client', () => ({ client: {} }))
vi.mock('../../application/prompt-card-store', () => ({
  listProfiles: vi.fn(async () => ({ Items: [] })),
  listFragments: vi.fn(async () => ({ Items: [] })),
}))
vi.mock('../../application/skill-card-store', async () => {
  const actual = await vi.importActual<typeof import('../../application/skill-card-store')>(
    '../../application/skill-card-store',
  )
  // Use the real `resolveSkillOverrides`; only `listSkills` is stubbed so tests
  // can inject skill fixtures.
  return { ...actual, listSkills: vi.fn(async () => []) }
})
vi.mock('../../gen-clients/workspace/client', () => ({
  getAgentKindConfig: vi.fn(),
  saveAgentKindConfig: vi.fn(),
  wikiListCards: vi.fn(async () => ({ Cards: [] })),
}))
vi.mock('../../gen-clients/aimanager/client', () => ({
  aggregatorList: vi.fn(async () => ({
    Items: [
      { Id: 'system', ActorId: 'system-actor', Name: 'Auto' },
      { Id: 'fast-pool', ActorId: 'fast-pool-actor', Name: 'Fast Pool' },
    ],
  })),
}))
vi.mock('../../gen-clients/aiaggregator/client', () => ({
  status: vi.fn(async () => ({
    Units: [{ Model: 'claude-sonnet-4-6', ProviderName: 'anthropic' }],
  })),
}))

const contextValue = {
  providers: [],
  groups: [],
  activeUnit: null,
  activeProviderId: '',
  onProviderChange: () => {},
  thinkingLevels: new Map(),
  activeThinkingLevel: null,
  onThinkingLevelChange: () => {},
}

async function renderAndOpenAdvancedTab(container: HTMLDivElement, root: Root) {
  await act(async () => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <AIShellContext.Provider value={contextValue as any}>
          <AgentKindConfigView kind="coder" />
        </AIShellContext.Provider>
      </I18nProvider>,
    )
  })
  const tabs = Array.from(container.querySelectorAll('[data-slot="tabs-trigger"]')) as HTMLButtonElement[]
  await act(async () => { tabs[5]?.click() })
}

async function chooseSelectOption(triggerAriaLabel: string, itemGuideId: string) {
  const trigger = document.querySelector(`button[aria-label="${triggerAriaLabel}"]`) as HTMLButtonElement
  expect(trigger, `trigger: ${triggerAriaLabel}`).toBeTruthy()
  await act(async () => { trigger.click() })
  const item = document.querySelector(`[data-guide-id="${itemGuideId}"]`) as HTMLElement
  expect(item, `item: ${itemGuideId}`).toBeTruthy()
  await act(async () => { item.click() })
}

async function renderAndOpenSkillsTab(container: HTMLDivElement, root: Root) {
  await act(async () => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <AIShellContext.Provider value={contextValue as any}>
          <AgentKindConfigView kind="coder" />
        </AIShellContext.Provider>
      </I18nProvider>,
    )
  })
  const tabs = Array.from(container.querySelectorAll('[data-slot="tabs-trigger"]')) as HTMLButtonElement[]
  // tab order: role(0) components(1) skills(2) fragments(3) auto-allow(4) advanced(5)
  await act(async () => { tabs[2]?.click() })
}

function renderView() {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  return { container, root }
}

describe('AgentKindConfigView', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.mocked(workspace.getAgentKindConfig).mockResolvedValue({
      Kind: 'coder',
      DisplayName: 'Coder',
      UserCreatable: true,
      SystemManaged: false,
      RolePromptRef: { Kind: 'profile', Key: 'coder' },
      CompactionPolicy: { Enabled: true, Strategy: 'sliding', BudgetMode: 'percentage', TokenBudget: 75, RecentWindow: 20, MaxSummaryTokens: 4000 },
    } as any)
    vi.mocked(workspace.saveAgentKindConfig).mockImplementation(async (_client, req: any) => req)
    ;({ container, root } = renderView())
  })

  afterEach(() => {
    root.unmount()
    container.remove()
    vi.restoreAllMocks()
  })

  it('renders saved defaults and saves changed model slot', async () => {
    await renderAndOpenAdvancedTab(container, root)

    const trigger = container.querySelector('button[aria-label="Primary default model unit"]') as HTMLButtonElement
    expect(trigger).toBeTruthy()
    await act(async () => { trigger.click() })
    expect(document.querySelector('[data-guide-id="settings/agent/advanced/slot-primary/agg-fast-pool"]')?.textContent).toContain('Fast Pool')
    await act(async () => { (document.querySelector('[data-guide-id="settings/agent/advanced/slot-primary/unit-anthropic-claude-sonnet-4-6"]') as HTMLElement).click() })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save')) as HTMLButtonElement
    await act(async () => { saveButton.click() })

    expect(workspace.saveAgentKindConfig).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({
      Primary: { Candidates: [{ kind: 'unit', Unit: { model: 'claude-sonnet-4-6', provider: 'anthropic' } }] },
      CompactionPolicy: expect.objectContaining({ Enabled: true }),
    }))
  })

  it('binds a custom aggregator as the default slot like the new-agent dialog', async () => {
    await renderAndOpenAdvancedTab(container, root)

    await chooseSelectOption('Fast default model unit', 'settings/agent/advanced/slot-fast/agg-fast-pool')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save')) as HTMLButtonElement
    await act(async () => { saveButton.click() })

    expect(workspace.saveAgentKindConfig).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({
      Fast: { Candidates: [{ kind: 'aggregator', AggregatorID: 'fast-pool' }] },
    }))
  })

  it('re-renders a saved aggregator-kind default as the aggregator option', async () => {
    vi.mocked(workspace.getAgentKindConfig).mockResolvedValue({
      Kind: 'coder',
      DisplayName: 'Coder',
      UserCreatable: true,
      SystemManaged: false,
      RolePromptRef: { Kind: 'profile', Key: 'coder' },
      Primary: { Candidates: [{ kind: 'aggregator', AggregatorID: 'fast-pool' }] },
      CompactionPolicy: { Enabled: true, Strategy: 'sliding', BudgetMode: 'percentage', TokenBudget: 75, RecentWindow: 20, MaxSummaryTokens: 4000 },
    } as any)
    await renderAndOpenAdvancedTab(container, root)

    const trigger = container.querySelector('button[aria-label="Primary default model unit"]') as HTMLButtonElement
    expect(trigger.textContent).toContain('Fast Pool')
  })

  it('shows an uncovered external skill as a toggleable skill card', async () => {
    const skills: Skill[] = [
      { Id: 'review', Name: 'Review changes', Description: 'Review the diff', Source: 'user' } as Skill,
      { Id: 'ext-skill:claude:lint', Name: 'Lint', Description: 'External linter', Source: 'claude' } as Skill,
    ]
    vi.mocked(skillCards.listSkills).mockResolvedValue(skills)
    await renderAndOpenSkillsTab(container, root)

    // The external `lint` skill has no same-name internal competitor, so it now
    // appears in the toggleable list (previously blanket-filtered out).
    const titles = Array.from(container.querySelectorAll('.agent-config-card-title')).map(el => el.textContent)
    expect(titles).toContain('Review changes')
    expect(titles).toContain('Lint')
  })

  it('split view groups active cards above inactive cards', async () => {
    vi.mocked(workspace.getAgentKindConfig).mockResolvedValue({
      Kind: 'coder',
      DisplayName: 'Coder',
      UserCreatable: true,
      SystemManaged: false,
      RolePromptRef: { Kind: 'profile', Key: 'coder' },
      SkillIDs: ['lint'],
      CompactionPolicy: { Enabled: true, Strategy: 'sliding', BudgetMode: 'percentage', TokenBudget: 75, RecentWindow: 20, MaxSummaryTokens: 4000 },
    } as any)
    const skills: Skill[] = [
      { Id: 'review', Name: 'Review changes', Description: 'Review the diff', Source: 'user' } as Skill,
      { Id: 'lint', Name: 'Lint', Description: 'Linter', Source: 'user' } as Skill,
    ]
    vi.mocked(skillCards.listSkills).mockResolvedValue(skills)
    await renderAndOpenSkillsTab(container, root)

    // Default flat view: no group headers.
    expect(container.querySelectorAll('[data-slot="akc-group-title"]').length).toBe(0)

    const splitButton = container.querySelector('button[title="Group by status"]') as HTMLButtonElement
    expect(splitButton).toBeTruthy()
    await act(async () => { splitButton.click() })

    const groupTitles = Array.from(container.querySelectorAll('[data-slot="akc-group-title"]')).map(el => el.textContent)
    expect(groupTitles).toEqual(['Active · 1', 'Inactive · 1'])

    const grids = Array.from(container.querySelectorAll('[data-slot="akc-grid"]'))
    expect(grids[0]?.textContent).toContain('Lint')
    expect(grids[0]?.textContent).not.toContain('Review changes')
    expect(grids[1]?.textContent).toContain('Review changes')
    expect(grids[1]?.textContent).not.toContain('Lint')

    // Switching back restores the flat grid without group headers.
    const flatButton = container.querySelector('button[title="Flat grid"]') as HTMLButtonElement
    await act(async () => { flatButton.click() })
    expect(container.querySelectorAll('[data-slot="akc-group-title"]').length).toBe(0)
  })

  it('hides an external skill overridden by a same-name internal skill', async () => {
    const skills: Skill[] = [
      { Id: 'lint', Name: 'Lint', Description: 'Internal linter', Source: 'user' } as Skill,
      { Id: 'ext-skill:claude:lint', Name: 'Lint', Description: 'External linter', Source: 'claude' } as Skill,
    ]
    vi.mocked(skillCards.listSkills).mockResolvedValue(skills)
    await renderAndOpenSkillsTab(container, root)

    // Same name `lint`: the internal skill wins, only one card remains.
    const titles = Array.from(container.querySelectorAll('.agent-config-card-title')).map(el => el.textContent)
    expect(titles).toEqual(['Lint'])
  })

  it('shows a delete action for a non-built-in template and invokes the callback', async () => {
    const onRequestDelete = vi.fn()
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <AIShellContext.Provider value={contextValue as any}>
            <AgentKindConfigView kind="my-agent" builtin={false} onRequestDelete={onRequestDelete} />
          </AIShellContext.Provider>
        </I18nProvider>,
      )
    })

    const deleteButton = container.querySelector('[data-testid="agent-kind-delete"]') as HTMLButtonElement
    expect(deleteButton).toBeTruthy()
    expect(container.querySelector('[data-testid="agent-kind-builtin-hint"]')).toBeNull()

    await act(async () => { deleteButton.click() })
    expect(onRequestDelete).toHaveBeenCalledTimes(1)
  })

  it('hides the delete action and shows the built-in hint for a built-in template', async () => {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <AIShellContext.Provider value={contextValue as any}>
            <AgentKindConfigView kind="coder" builtin />
          </AIShellContext.Provider>
        </I18nProvider>,
      )
    })

    expect(container.querySelector('[data-testid="agent-kind-delete"]')).toBeNull()
    const hint = container.querySelector('[data-testid="agent-kind-builtin-hint"]')
    expect(hint?.textContent).toContain('Built-in templates cannot be deleted')
  })
})
