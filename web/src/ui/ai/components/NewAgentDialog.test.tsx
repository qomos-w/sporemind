import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { NewAgentDialog } from './NewAgentDialog'
import { I18nProvider } from '../../../i18n'
import type { AgentInfo } from '../hooks/agentInfoStore'
import * as workspace_preferences from '../../../gen-clients/workspace/client'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// Mocked account-preferences store for the preset feature (useModelPresets).
const prefsMock = vi.hoisted(() => ({
  Preferences: {} as Record<string, string>,
  Version: 1,
}))
vi.mock('../../../gen-clients/workspace/client', () => ({
  preferencesGet: vi.fn(async () => ({ AccountId: 'root', Version: prefsMock.Version, Preferences: { ...prefsMock.Preferences } })),
  preferencesSave: vi.fn(async (_c: unknown, cmd: { Version: number; Preferences: Record<string, string> }) => {
    prefsMock.Preferences = { ...cmd.Preferences }
    prefsMock.Version = cmd.Version + 1
  }),
}))
// Mocked aiaggregator.status: returns the units registered below, keyed by target.
const statusUnits = vi.hoisted(() => new Map<string, unknown[]>())
vi.mock('../../../gen-clients/aiaggregator/client', () => ({
  status: vi.fn(async (_c: unknown, req: { target: string }) => ({ Units: statusUnits.get(req.target) ?? [] })),
}))
function registerAggregatorUnits(target: string, units: unknown[]) {
  statusUnits.set(target, units)
}
function resetPrefs() {
  prefsMock.Preferences = {}
  prefsMock.Version = 1
}

const aggregators = [
  { Id: 'agg-1', Name: 'anthropic', ActorId: 'agg-1', Path: 'providers/anthropic' },
  { Id: 'agg-2', Name: 'openai', ActorId: 'agg-2', Path: 'providers/openai' },
]

const projects = [
  { Id: 'p1', ActorId: 'p1', Name: 'Project 1', Path: '/tmp/p1', Root: false },
  { Id: 'p2', ActorId: 'p2', Name: 'Project 2', Path: '/tmp/p2', Root: false },
]

const agentKinds = [
  {
    kind: 'coder',
    displayName: 'Coder',
    randomName: { Enabled: true, Prefixes: ['Byte'], Suffixes: ['Duck'] },
  },
]

const existingAgents: AgentInfo[] = []

describe('NewAgentDialog', () => {
  let container: HTMLDivElement
  let root: Root

  const renderDialog = async (props: Partial<React.ComponentProps<typeof NewAgentDialog>> = {}) => {
    const onCancel = props.onCancel ?? vi.fn()
    const onCreate = props.onCreate ?? vi.fn()

    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <NewAgentDialog
            open
            projects={projects}
            aggregators={aggregators}
            agentKinds={agentKinds}
            agents={existingAgents}
            defaultAgentKind="coder"
            creating={false}
            error=""
            onCancel={onCancel}
            onCreate={onCreate}
            {...props}
          />
        </I18nProvider>,
      )
    })

    return { onCancel, onCreate }
  }

  const getInput = () => Array.from(container.querySelectorAll('label input.new-agent-dialog-input')).pop() as HTMLInputElement
  const getSessionTitleInput = () => container.querySelector('label input.new-agent-dialog-input') as HTMLInputElement
  const getSelects = () => Array.from(container.querySelectorAll('select.new-agent-dialog-select')) as HTMLSelectElement[]
  const getButtons = () => Array.from(container.querySelectorAll('button')) as HTMLButtonElement[]
  const getCreateButton = () => container.querySelector('button.modal-action--primary') as HTMLButtonElement
  const getCancelButton = () => getButtons().find(button => button.textContent?.includes('Cancel')) as HTMLButtonElement
  const changeInput = async (value: string) => {
    const descriptor = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')
    descriptor?.set?.call(getInput(), value)
    await act(async () => {
      getInput().dispatchEvent(new Event('input', { bubbles: true }))
    })
  }
  const changeSelect = async (select: HTMLSelectElement, value: string) => {
    const descriptor = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')
    descriptor?.set?.call(select, value)
    await act(async () => {
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
  }

  beforeEach(() => {
    resetPrefs()
    // afterEach runs restoreAllMocks, which strips the factory implementations;
    // re-establish them (and clear call history) before each preset test.
    vi.mocked(workspace_preferences.preferencesGet).mockImplementation(async () => ({ AccountId: 'root', Version: prefsMock.Version, Preferences: { ...prefsMock.Preferences } }))
    vi.mocked(workspace_preferences.preferencesSave).mockImplementation(async (_c, cmd) => {
      prefsMock.Preferences = { ...cmd.Preferences }
      prefsMock.Version = cmd.Version + 1
      return { AccountId: 'root', Version: prefsMock.Version, Preferences: { ...prefsMock.Preferences } }
    })
    vi.mocked(workspace_preferences.preferencesGet).mockClear()
    vi.mocked(workspace_preferences.preferencesSave).mockClear()
    statusUnits.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
  })

  it('disables creation and shows empty-state error when no creatable kind exists', async () => {
    await renderDialog({ agentKinds: [], defaultAgentKind: '' })

    expect(container.textContent).toContain('No user-creatable agent kind is available yet')
    expect(getCreateButton().disabled).toBe(true)
  })

  it('prefills agent kind from defaultAgentKind prop', async () => {
    await renderDialog({ defaultAgentKind: 'coder', defaultProjectId: 'p2' })

    const [kindSelect] = getSelects()
    expect(kindSelect).toBeTruthy()
    expect(kindSelect!.value).toBe('coder')
  })

  it('prefills project from defaultProjectId prop', async () => {
    await renderDialog({ defaultAgentKind: 'coder', defaultProjectId: 'p2' })

    const [, projectSelect] = getSelects()
    expect(projectSelect).toBeTruthy()
    expect(projectSelect!.value).toBe('p2')
  })

  it('submits a per-slot aggregator selection with trimmed display name', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    await renderDialog({ onCreate })

    const selects = getSelects()
    // selects: [kind, project, primary, fast, execution, review, summary]
    const primarySelect = selects[2]
    expect(primarySelect).toBeTruthy()

    await changeInput('  Custom Agent  ')
    await changeSelect(primarySelect!, 'agg::agg-2')

    await act(async () => {
      getCreateButton().click()
    })

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      displayName: 'Custom Agent',
      agentKind: 'coder',
      projectId: 'p1',
      primarySelection: { type: 'aggregator', aggregatorId: 'agg-2' },
    }))
  })

  const getPresetField = () => container.querySelector('input.new-agent-dialog-preset-field') as HTMLInputElement
  const setPresetField = async (value: string) => {
    const input = getPresetField()
    const descriptor = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!
    descriptor.set!.call(input, value)
    await act(async () => { input.dispatchEvent(new Event('input', { bubbles: true })) })
  }
  // Pick a preset from the dropdown list by name (focus to open list, click the row).
  const pickPresetFromList = async (name: string) => {
    await act(async () => { getPresetField().focus() })
    const btn = Array.from(container.querySelectorAll<HTMLButtonElement>('.new-agent-dialog-preset-list button'))
      .find(b => b.textContent === name)
    if (!btn) throw new Error(`preset list row "${name}" not found`)
    await act(async () => { btn.click() })
  }
  const clickIconButton = async (title: string) => {
    const btn = container.querySelector<HTMLButtonElement>(`button[title="${title}"]`)!
    await act(async () => { btn.click() })
  }
  const lastSavePrefs = (): Record<string, string> => {
    const calls = vi.mocked(workspace_preferences.preferencesSave).mock.calls
    return calls.at(-1)![1]!.Preferences
  }

  it('saves current slot selections as a named preset', async () => {
    await renderDialog()
    const selects = getSelects()
    // selects: [kind, project, primary, fast, execution, review, summary]
    await changeSelect(selects[2]!, 'agg::agg-2') // primary slot

    await setPresetField('My Preset')
    await clickIconButton('Save as model preset')

    const stored = JSON.parse(lastSavePrefs()['model-presets.v1'] as string)
    expect(stored).toHaveLength(1)
    expect(stored[0].name).toBe('My Preset')
    expect(stored[0].primary).toEqual({ type: 'aggregator', aggregatorId: 'agg-2' })
  })

  // Summary is a 5th model slot, persisted like the others (aggregator or unit).
  // These fixtures must include the system aggregator so availableModels is populated.
  const aggregatorsWithSystem = [
    ...aggregators,
    { Id: 'system', Name: 'system', ActorId: 'system-agg', Path: 'system' },
  ]
  const summaryUnit = { Id: 'u1', Model: 'gpt-4o', ProviderName: 'openai' }
  const summaryValue = 'openai::gpt-4o'

  it('saves the summary slot in the preset', async () => {
    registerAggregatorUnits('system-agg', [summaryUnit])
    await renderDialog({ aggregators: aggregatorsWithSystem })
    const selects = getSelects()
    // selects: [kind, project, primary, fast, execution, review, summary]
    await changeSelect(selects[6]!, summaryValue)

    await setPresetField('With Summary')
    await clickIconButton('Save as model preset')

    const stored = JSON.parse(lastSavePrefs()['model-presets.v1'] as string)
    expect(stored[0].summary).toEqual({ type: 'unit', unit: { provider: 'openai', model: 'gpt-4o' } })
  })

  it('restores a legacy bare-unit summary (normalized) when applying a preset', async () => {
    // Legacy preset stored summary as a bare ModelUnit; the loader normalizes it
    // into a unit-kind PresetSlot so it still applies.
    prefsMock.Preferences['model-presets.v1'] = JSON.stringify([{
      name: 'Has Summary',
      summary: { provider: 'openai', model: 'gpt-4o' },
    }])
    registerAggregatorUnits('system-agg', [summaryUnit])
    await renderDialog({ aggregators: aggregatorsWithSystem })
    const selects = getSelects()
    const summarySelect = selects[6]
    expect(summarySelect!.value).toBe('') // [auto] before apply
    await pickPresetFromList('Has Summary')
    expect(summarySelect!.value).toBe(summaryValue)
  })

  it('applies a saved preset to the slot selections', async () => {
    prefsMock.Preferences['model-presets.v1'] = JSON.stringify([{
      name: 'Fast Setup',
      primary: { type: 'aggregator', aggregatorId: 'agg-2' },
    }])
    await renderDialog()
    const selects = getSelects()
    const primarySelect = selects[2]
    expect(primarySelect!.value).toBe('') // [auto] before apply
    await pickPresetFromList('Fast Setup')
    expect(primarySelect!.value).toBe('agg::agg-2')
  })

  it('deletes the selected preset after confirm', async () => {
    prefsMock.Preferences['model-presets.v1'] = JSON.stringify([{
      name: 'To Delete',
      primary: { type: 'aggregator', aggregatorId: 'agg-1' },
    }])
    vi.stubGlobal('confirm', () => true)
    await renderDialog()
    await pickPresetFromList('To Delete')
    await clickIconButton('Delete model preset')
    expect(JSON.parse(lastSavePrefs()['model-presets.v1'] as string)).toEqual([])
    vi.unstubAllGlobals()
  })

  it('disables clone when the source agent kind is no longer available', async () => {
    const cloneSource = {
      displayName: 'Original Agent',
      agentKind: 'missing-kind',
      projectId: 'p1',
    }
    await renderDialog({ mode: 'clone', cloneSource })

    expect(container.textContent).toContain('Select an agent kind first')
    expect(getCreateButton().disabled).toBe(true)
  })

  it('refills random name on re-open after previous manual edit', async () => {
    // First open: should auto-fill random name
    await renderDialog()
    expect(getInput().value).toBe('Byte Duck')

    // User edits the display name
    await changeInput('Custom Agent')
    expect(getInput().value).toBe('Custom Agent')

    // Close dialog by setting open=false
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <NewAgentDialog
            open={false}
            projects={projects}
            aggregators={aggregators}
            agentKinds={agentKinds}
            agents={existingAgents}
            defaultAgentKind="coder"
            creating={false}
            error=""
            onCancel={vi.fn()}
            onCreate={vi.fn()}
          />,
        </I18nProvider>
)
    })

    // Re-open dialog: should refill random name even after manual edit
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <NewAgentDialog
            open
            projects={projects}
            aggregators={aggregators}
            agentKinds={agentKinds}
            agents={existingAgents}
            defaultAgentKind="coder"
            creating={false}
            error=""
            onCancel={vi.fn()}
            onCreate={vi.fn()}
          />,
        </I18nProvider>
)
    })

    expect(getInput().value).toBe('Byte Duck')
  })
  it('does not close when overlay is clicked', async () => {
    const onCancel = vi.fn()
    await renderDialog({ onCancel })

    const overlay = container.querySelector('.modal-overlay') as HTMLDivElement
    await act(async () => {
      overlay.click()
    })

    expect(onCancel).not.toHaveBeenCalled()
    expect(getCancelButton()).toBeTruthy()
  })

  it('auto-selects the default project when projects load after mount in project scope', async () => {
    await renderDialog({ projects: [] })

    // Initially no projects are available, so creation is blocked.
    expect(container.textContent).toContain('Select a project first')
    expect(getCreateButton().disabled).toBe(true)

    // Projects arrive later (simulating async load).
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <NewAgentDialog
            open
            projects={projects}
            aggregators={aggregators}
            agentKinds={agentKinds}
            agents={existingAgents}
            defaultAgentKind="coder"
            defaultProjectId="p2"
            creating={false}
            error=""
            onCancel={vi.fn()}
            onCreate={vi.fn()}
          />,
        </I18nProvider>
)
    })

    const selects = getSelects()
    const projectSelect = selects[1]
    expect(projectSelect).toBeTruthy()
    expect(projectSelect!.value).toBe('p2')
    expect(container.textContent).not.toContain('Select a project first')
    expect(getCreateButton().disabled).toBe(false)
  })

  it('shows session title when it differs from display name in edit mode', async () => {
    const editAgent = {
      Id: 'agent-a',
      ActorId: 'actor-a',
      DisplayName: 'coder-1',
      Title: 'Project Assistant',
      AgentKind: 'coder',
      ProjectId: 'p1',
    }
    await renderDialog({ mode: 'edit', editAgent })

    expect(container.textContent).toContain('Session title')
    expect(getSessionTitleInput().value).toBe('Project Assistant')
    expect(getInput().value).toBe('coder-1')
  })

  it('resets form fields when switching edit agent without closing dialog', async () => {
    const editAgentA = {
      Id: 'agent-a',
      ActorId: 'actor-a',
      DisplayName: 'Agent A',
      AgentKind: 'coder',
      ProjectId: 'p1',
    }
    await renderDialog({ mode: 'edit', editAgent: editAgentA })

    expect(getInput().value).toBe('Agent A')

    const editAgentB = {
      Id: 'agent-b',
      ActorId: 'actor-b',
      DisplayName: 'Agent B',
      AgentKind: 'coder',
      ProjectId: 'p1',
    }
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <NewAgentDialog
            open
            mode="edit"
            editAgent={editAgentB}
            projects={projects}
            aggregators={aggregators}
            agentKinds={agentKinds}
            agents={existingAgents}
            defaultAgentKind="coder"
            creating={false}
            error=""
            onCancel={vi.fn()}
            onCreate={vi.fn()}
          />,
        </I18nProvider>
)
    })

    expect(getInput().value).toBe('Agent B')
  })

  it('falls back to the first available kind when the default is unavailable', async () => {
    await renderDialog({ defaultAgentKind: 'missing-kind', defaultProjectId: 'p2' })

    const [kindSelect] = getSelects()
    expect(kindSelect!.value).toBe('coder')
  })

  it('prefills the 5 model slots from the last submitted selection in create mode', async () => {
    prefsMock.Preferences['model-recent.v1'] = JSON.stringify({
      primary: { type: 'aggregator', aggregatorId: 'agg-2' },
      fast: { type: 'aggregator', aggregatorId: 'agg-1' },
      summary: { type: 'unit', unit: { provider: 'openai', model: 'gpt-4o' } },
    })
    registerAggregatorUnits('system-agg', [summaryUnit])
    await renderDialog({ aggregators: aggregatorsWithSystem })
    const selects = getSelects()
    // selects in create mode: [kind, project, primary, fast, execution, review, summary]
    expect(selects[2]!.value).toBe('agg::agg-2')
    expect(selects[3]!.value).toBe('agg::agg-1')
    expect(selects[6]!.value).toBe(summaryValue)
  })

  it('does not clobber a manual slot choice when recent loads after the user edits', async () => {
    prefsMock.Preferences['model-recent.v1'] = JSON.stringify({
      primary: { type: 'aggregator', aggregatorId: 'agg-2' },
    })
    registerAggregatorUnits('system-agg', [summaryUnit])
    // Delay preferences get so recent loads after the user interaction.
    let resolveGet!: () => void
    vi.mocked(workspace_preferences.preferencesGet).mockImplementationOnce(
      () => new Promise(r => { resolveGet = () => r({ AccountId: 'root', Version: prefsMock.Version, Preferences: { ...prefsMock.Preferences } }) }),
    )
    await renderDialog({ aggregators: aggregatorsWithSystem })
    const selects = getSelects()
    // User picks agg-1 on primary before recent (agg-2) loads.
    await changeSelect(selects[2]!, 'agg::agg-1')
    // Now recent loads.
    await act(async () => { resolveGet() })
    expect(selects[2]!.value).toBe('agg::agg-1')
  })

  it('persists the last submitted selection as recent', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    registerAggregatorUnits('system-agg', [summaryUnit])
    await renderDialog({ onCreate, aggregators: aggregatorsWithSystem })
    const selects = getSelects()
    await changeInput('Recent Agent')
    await changeSelect(selects[2]!, 'agg::agg-2') // primary
    await changeSelect(selects[6]!, summaryValue) // summary
    await act(async () => { getCreateButton().click() })
    // saveRecent is fire-and-forget after submit resolves; flush its async chain.
    await act(async () => { await new Promise(r => setTimeout(r)) })
    const stored = JSON.parse(lastSavePrefs()['model-recent.v1'] as string)
    expect(stored.primary).toEqual({ type: 'aggregator', aggregatorId: 'agg-2' })
    expect(stored.summary).toEqual({ type: 'unit', unit: { provider: 'openai', model: 'gpt-4o' } })
  })

  it('submits a summary aggregator selection like any other slot', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    await renderDialog({ onCreate })
    const selects = getSelects()
    // selects: [kind, project, primary, fast, execution, review, summary]
    await changeInput('Agg Summary')
    await changeSelect(selects[6]!, 'agg::agg-1') // summary → named aggregator
    await act(async () => { getCreateButton().click() })
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      summarySelection: { type: 'aggregator', aggregatorId: 'agg-1' },
    }))
  })

  it('migrates a legacy SummaryUnit override into the summary slot in edit mode', async () => {
    const editAgent = { Id: 'e1', ActorId: 'e1', DisplayName: 'Edit', AgentKind: 'coder', ProjectId: 'p1' }
    const compactionPolicy = { Enabled: false, Strategy: '', TriggerKind: '', BudgetMode: 'percentage', TokenBudget: 75, RecentWindow: 20, SummaryUnit: { provider: 'openai', model: 'gpt-4o' }, MaxSummaryTokens: 4000 }
    registerAggregatorUnits('system-agg', [summaryUnit])
    await renderDialog({ mode: 'edit', editAgent, compactionPolicy, aggregators: aggregatorsWithSystem })
    const selects = getSelects()
    // edit mode selects: [primary, fast, execution, review, summary]
    expect(selects[4]!.value).toBe('openai::gpt-4o')
  })

  it('does not prefill recent in edit mode', async () => {
    prefsMock.Preferences['model-recent.v1'] = JSON.stringify({
      primary: { type: 'aggregator', aggregatorId: 'agg-2' },
    })
    registerAggregatorUnits('system-agg', [summaryUnit])
    const editAgent = { Id: 'e1', ActorId: 'e1', DisplayName: 'Edit', AgentKind: 'coder', ProjectId: 'p1' }
    await renderDialog({ mode: 'edit', editAgent, aggregators: aggregatorsWithSystem })
    const selects = getSelects()
    // edit mode selects: [primary, fast, execution, review, summary] (no kind/project select)
    expect(selects[0]!.value).toBe('') // [auto], not recent's agg-2
  })

  it('resolves recent aggregator selections by config id when it differs from actor id', async () => {
    const aggregatorsDistinctIds = [
      { Id: 'cfg-anthropic', Name: 'anthropic', ActorId: 'actor-anthropic', Path: 'providers/anthropic' },
      { Id: 'cfg-openai', Name: 'openai', ActorId: 'actor-openai', Path: 'providers/openai' },
    ]
    prefsMock.Preferences['model-recent.v1'] = JSON.stringify({
      primary: { type: 'aggregator', aggregatorId: 'cfg-openai' },
    })
    await renderDialog({ aggregators: aggregatorsDistinctIds })
    const selects = getSelects()
    // create mode selects: [kind, project, primary, fast, execution, review, summary]
    // primary's encoded value is the aggregator CONFIG id; recent stored cfg-openai,
    // so the option must highlight agg::cfg-openai (not the actor id).
    expect(selects[2]!.value).toBe('agg::cfg-openai')
  })

  it('preserves the serving aggregator of an edit-mode unit slot when saved unchanged', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    registerAggregatorUnits('system-agg', [{ Id: 'gpt5', Model: 'gpt-5', ProviderName: 'openai' }])
    const editAgent = {
      Id: 'e1',
      ActorId: 'e1',
      DisplayName: 'Edit',
      AgentKind: 'coder',
      ProjectId: 'p1',
      Primary: { Candidates: [{ kind: 'unit', Unit: { model: 'gpt-5', provider: 'openai' }, AggregatorID: 'agg-2' }] },
    }
    await renderDialog({ mode: 'edit', editAgent, onSubmit, aggregators: aggregatorsWithSystem })
    await act(async () => { getCreateButton().click() })
    // Saving without touching the primary slot must keep the unit routed through
    // its original serving aggregator (agg-2), not demote it to a system-served unit.
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({
      primarySelection: { type: 'unit', unit: { model: 'gpt-5', provider: 'openai' }, aggregatorId: 'agg-2' },
    }))
  })
})
