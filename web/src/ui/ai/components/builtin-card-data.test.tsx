import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import React from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { fireEvent } from '@testing-library/react'
import { act } from 'react'
import { I18nProvider } from '../../../i18n'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../application/generated-client', () => ({
  default: {},
}))

vi.mock('../../panels/mono-store', () => ({
  monoStore: {
    getCard: vi.fn(),
    getCards: vi.fn(),
    updateCard: vi.fn(),
    createCard: vi.fn(),
    deleteCard: vi.fn(),
    searchCards: vi.fn(),
    openCards: [],
    subscribe: vi.fn(() => () => {}),
    openCard: vi.fn(),
    closeCard: vi.fn(),
    focusCard: vi.fn(),
    cards: [],
    selectedProjectId: null,
    selectedProjectName: '',
    currentLocale: 'en-US',
    setLocale: vi.fn(),
    projectCards: [],
    allProjectCards: [],
    activeProjectCards: [],
    builtinMountCards: [],
    workflowMountCards: [],
    loadProject: vi.fn(),
    loadCards: vi.fn(),
    triggerTimerCard: vi.fn(),
    refreshProjectCards: vi.fn(),
    listTemplates: vi.fn().mockResolvedValue([]),
    automationBind: vi.fn().mockResolvedValue(null),
  },
}))

vi.mock('../hooks/useMonoStore', () => ({
  useMonoStore: (): Record<string, unknown> => ({}),
}))

vi.mock('../hooks/agentListStore', () => ({
  subscribeAgentListStore: (): (() => void) => () => {},
  getAgentListItems: (): Array<Record<string, unknown>> => [],
}))

const BuiltinCardDataPanel = (await import('./builtin-card-data')).BuiltinCardDataPanel

describe('BuiltinCardDataPanel task evidence', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderPanel = async (props: Partial<React.ComponentProps<typeof BuiltinCardDataPanel>> = {}) => {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <BuiltinCardDataPanel
            tags={['task']}
            type="task"
            data={{}}
            {...props}
          />
        </I18nProvider>,
      )
    })
  }

  it('renders review evidence as file path badges', async () => {
    await renderPanel({
      data: { review_evidence: JSON.stringify(['src/foo.ts', 'src/bar.ts']) },
    })
    const panel = container.querySelector('[data-builtin-card-kind="task"]')
    expect(panel).not.toBeNull()
    expect(panel?.textContent).toContain('Review evidence')
    const items = Array.from(container.querySelectorAll('.mono-card-builtin-task-evidence-item'))
    expect(items.map(el => el.textContent)).toEqual(['src/foo.ts', 'src/bar.ts'])
  })

  it('renders nothing when review evidence is empty', async () => {
    await renderPanel({ data: { review_evidence: JSON.stringify([]) } })
    expect(container.querySelector('[data-builtin-card-kind="task"]')).toBeNull()
  })

  it('renders nothing when review evidence is missing', async () => {
    await renderPanel({ data: {} })
    expect(container.querySelector('[data-builtin-card-kind="task"]')).toBeNull()
  })

  it('renders nothing when review evidence is malformed', async () => {
    await renderPanel({ data: { review_evidence: 'not-json' } })
    expect(container.querySelector('[data-builtin-card-kind="task"]')).toBeNull()
  })

  it('renders nothing when review evidence is not an array', async () => {
    await renderPanel({ data: { review_evidence: JSON.stringify({ path: 'src/foo.ts' }) } })
    expect(container.querySelector('[data-builtin-card-kind="task"]')).toBeNull()
  })

  it('skips non-string entries inside the array', async () => {
    await renderPanel({
      data: { review_evidence: JSON.stringify(['src/foo.ts', 123, null, 'src/bar.ts']) },
    })
    const items = Array.from(container.querySelectorAll('.mono-card-builtin-task-evidence-item'))
    expect(items.map(el => el.textContent)).toEqual(['src/foo.ts', 'src/bar.ts'])
  })

  it('renders nothing when type is not task even with review_evidence present', async () => {
    await renderPanel({ type: 'wiki', data: { review_evidence: JSON.stringify(['src/foo.ts']) } })
    expect(container.querySelector('[data-builtin-card-kind="task"]')).toBeNull()
  })
})

describe('BuiltinCardDataPanel scheduler edit schedule button', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderPanel = async (props: Partial<React.ComponentProps<typeof BuiltinCardDataPanel>> = {}) => {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <BuiltinCardDataPanel
            tags={['scheduler']}
            type="scheduler"
            data={{}}
            {...props}
          />
        </I18nProvider>,
      )
    })
  }

  it('renders edit schedule button when onUpdateScheduleCron is provided in non-editing mode', async () => {
    const onUpdate = vi.fn().mockResolvedValue(true)
    await renderPanel({ cardId: 'scheduler:test', editing: false, onUpdateScheduleCron: onUpdate })
    const btn = container.querySelector('.scheduled-edit-schedule') as HTMLButtonElement | null
    expect(btn).not.toBeNull()
    expect(btn?.textContent).toContain('Edit schedule')
  })

  it('does not render edit schedule button when onUpdateScheduleCron is omitted', async () => {
    await renderPanel({ cardId: 'scheduler:test', editing: false })
    expect(container.querySelector('.scheduled-edit-schedule')).toBeNull()
  })

  it('does not render edit schedule button in editing mode', async () => {
    const onUpdate = vi.fn().mockResolvedValue(true)
    await renderPanel({ cardId: 'scheduler:test', editing: true, onUpdateScheduleCron: onUpdate })
    expect(container.querySelector('.scheduled-edit-schedule')).toBeNull()
  })

  it('opens ScheduleCronEditor when edit schedule button is clicked', async () => {
    const onUpdate = vi.fn().mockResolvedValue(true)
    await renderPanel({ cardId: 'scheduler:test', editing: false, data: { schedule: { cron: '0 9 * * *' } }, onUpdateScheduleCron: onUpdate })
    const btn = container.querySelector('.scheduled-edit-schedule') as HTMLButtonElement
    expect(btn).not.toBeNull()
    await act(async () => { btn.click() })
    expect(container.querySelector('.scheduled-schedule-editor')).not.toBeNull()
  })

  it('calls onUpdateScheduleCron with rebuilt cron on save', async () => {
    const onUpdate = vi.fn().mockResolvedValue(true)
    await renderPanel({ cardId: 'scheduler:test', editing: false, data: { schedule: { cron: '0 9 * * *' } }, onUpdateScheduleCron: onUpdate })
    // Open the editor
    const btn = container.querySelector('.scheduled-edit-schedule') as HTMLButtonElement
    await act(async () => { btn.click() })
    // Untouched draft: the save button stays hidden until a real change.
    expect(container.querySelector('.scheduled-editor-btn.primary')).toBeNull()
    // Change the time to 10:00 → cron "0 10 * * *", which surfaces the button.
    const time = container.querySelector<HTMLInputElement>('.scheduled-schedule-editor input[type="time"]')!
    await act(async () => { fireEvent.change(time, { target: { value: '10:00' } }) })
    const saveBtn = container.querySelector('.scheduled-editor-btn.primary') as HTMLButtonElement
    expect(saveBtn).not.toBeNull()
    // Click save — the onApply handler is async, so we wait for the mock to be called
    await act(async () => { saveBtn.click() })
    await vi.waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith('scheduler:test', '0 10 * * *')
    })
  })
})

describe('BuiltinCardDataPanel scheduler current_instance (N3)', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderPanel = async (props: Partial<React.ComponentProps<typeof BuiltinCardDataPanel>> = {}) => {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <BuiltinCardDataPanel
            tags={['scheduler']}
            type="scheduler"
            data={{}}
            {...props}
          />
        </I18nProvider>,
      )
    })
  }

  it('renders current_instance as a clickable row when the field is present', async () => {
    const onNavigateToMap = vi.fn()
    await renderPanel({
      data: { current_instance: 'inst::123::tpl::MyFlow', run_status: 'running' },
      onNavigateToMap,
    })
    const panel = container.querySelector('[data-builtin-card-kind="scheduler"]')
    expect(panel).not.toBeNull()
    const link = panel?.querySelector('.mono-card-builtin-data-navigate') as HTMLAnchorElement | null
    expect(link).not.toBeNull()
    expect(link?.textContent).toContain('inst::123::tpl::MyFlow')
    await act(async () => {
      link!.click()
    })
    expect(onNavigateToMap).toHaveBeenCalledWith('inst::123::tpl::MyFlow')
  })

  it('omits the current_instance row when absent (startup/idle scheduler)', async () => {
    await renderPanel({ data: { run_status: 'idle' } })
    const panel = container.querySelector('[data-builtin-card-kind="scheduler"]')
    expect(panel?.querySelector('.mono-card-builtin-data-navigate')).toBeNull()
    expect(panel?.textContent).not.toContain('currentInstance')
  })

  it('keeps current_instance as plain text when no onNavigateToMap callback is provided', async () => {
    await renderPanel({ data: { current_instance: 'inst::9::tpl::MyFlow' } })
    const panel = container.querySelector('[data-builtin-card-kind="scheduler"]')
    expect(panel?.querySelector('.mono-card-builtin-data-navigate')).toBeNull()
    expect(panel?.textContent).toContain('inst::9::tpl::MyFlow')
  })
})

describe('BuiltinCardDataPanel scheduler type selector', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderPanel = async (props: Partial<React.ComponentProps<typeof BuiltinCardDataPanel>> = {}) => {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <BuiltinCardDataPanel
            tags={['scheduler']}
            type="scheduler"
            data={{}}
            {...props}
          />
        </I18nProvider>,
      )
    })
  }

  it('renders task and agent_action radios in editing mode', async () => {
    await renderPanel({ editing: true })
    const radios = Array.from(container.querySelectorAll('.mono-card-builtin-data-type input[type="radio"]'))
    expect(radios).toHaveLength(2)
    expect(radios.map(r => (r as HTMLInputElement).value)).toEqual(['task', 'agent_action'])
  })

  it('does not render the type selector in read-only mode', async () => {
    await renderPanel({ editing: false })
    expect(container.querySelector('.mono-card-builtin-data-type')).toBeNull()
  })

  it('shows the template selector for a template-bound task', async () => {
    await renderPanel({ editing: true, data: { schedule_type: 'workflow', workflow_template: 'tpl::x' } })
    const panel = container.querySelector('[data-builtin-card-kind="scheduler"]')
    expect(panel?.querySelector('select')).not.toBeNull()
    // Tasks show both the template section and the prompt preview.
    expect(panel?.querySelector('.mono-card-builtin-data-prompt')).not.toBeNull()
  })

  it('legacy card with a template derives task and shows the template selector', async () => {
    await renderPanel({ editing: true, data: { workflow_template: 'tpl::x' } })
    const panel = container.querySelector('[data-builtin-card-kind="scheduler"]')
    expect(panel?.querySelector('select')).not.toBeNull()
    expect(panel?.querySelector('.mono-card-builtin-data-prompt')).not.toBeNull()
  })

  it('shows the template selector and prompt preview for a prompt-mode task', async () => {
    await renderPanel({ editing: true, data: { schedule_type: 'prompt' }, body: 'Check daily blockers.\nSecond line.' })
    const panel = container.querySelector('[data-builtin-card-kind="scheduler"]')
    // The unified task radio shows the template section alongside the prompt.
    expect(panel?.querySelector('select')).not.toBeNull()
    const prompt = panel?.querySelector('.mono-card-builtin-data-prompt')
    expect(prompt).not.toBeNull()
    expect(prompt?.querySelector('.mono-card-builtin-data-prompt-body')?.textContent).toContain('Check daily blockers. Second line.')
    expect(prompt?.querySelector('.mono-card-builtin-data-prompt-hint')?.textContent).toBeTruthy()
  })

  it('truncates the prompt preview to ~200 characters', async () => {
    await renderPanel({ editing: true, data: { schedule_type: 'prompt' }, body: 'x'.repeat(500) })
    const text = container.querySelector('.mono-card-builtin-data-prompt-body')?.textContent ?? ''
    expect(text).toHaveLength(201)
    expect(text.endsWith('…')).toBe(true)
  })

  it('writes schedule_type: task and clears agent_action when switching type', async () => {
    const onChange = vi.fn()
    await renderPanel({ editing: true, data: { schedule_type: 'agent_action' }, onChange })
    const taskRadio = container.querySelector('input[value="task"]') as HTMLInputElement
    await act(async () => { taskRadio.click() })
    expect(onChange).toHaveBeenCalledWith({ schedule_type: 'task', agent_action: '', target_agent: '', agent_actions: '' })
  })

  it('detects an agent-action card from its unified agent_actions list', async () => {
    await renderPanel({
      editing: true,
      data: { schedule_type: 'task', agent_actions: JSON.stringify([{ action: 'resume', target: 'agent:x' }]) },
    })
    const agentRadio = container.querySelector('input[value="agent_action"]') as HTMLInputElement
    expect(agentRadio.checked).toBe(true)
    expect(container.querySelector('.mono-card-builtin-data-prompt')).toBeNull()
    expect(container.querySelector('.mono-card-builtin-data-agent-action')).not.toBeNull()
  })

  it('switching to agent_action writes the unified agent_actions list, not legacy fields', async () => {
    const onChange = vi.fn()
    await renderPanel({ editing: true, data: { schedule_type: 'task' }, onChange })
    const agentRadio = container.querySelector('input[value="agent_action"]') as HTMLInputElement
    await act(async () => { agentRadio.click() })
    expect(onChange).toHaveBeenCalledTimes(1)
    const written = onChange.mock.calls[0]![0] as Record<string, unknown>
    expect(written.schedule_type).toBe('task')
    expect(written.agent_action).toBe('')
    expect(written.target_agent).toBe('')
    expect(JSON.parse(String(written.agent_actions))).toEqual([{ action: 'pause', target: '' }])
  })

  it('editing an agent-action target persists the unified list without dropping other rows', async () => {
    const onChange = vi.fn()
    const initial = JSON.stringify([
      { action: 'resume', target: 'agent:a' },
      { action: 'pause', target: 'agent:b' },
    ])
    await renderPanel({
      editing: true,
      data: { schedule_type: 'task', agent_actions: initial },
      onChange,
    })
    const target = container.querySelectorAll('.mono-card-builtin-data-agent-action input')[1] as HTMLInputElement
    await act(async () => {
      fireEvent.change(target, { target: { value: 'agent:c' } })
    })
    const written = onChange.mock.calls[0]![0] as Record<string, unknown>
    expect(JSON.parse(String(written.agent_actions))).toEqual([
      { action: 'resume', target: 'agent:a' },
      { action: 'pause', target: 'agent:c' },
    ])
  })

  it('shows the derived type row in read-only summary', async () => {
    await renderPanel({ editing: false, data: { schedule_type: 'workflow', workflow_template: 'tpl::x' } })
    const panel = container.querySelector('[data-builtin-card-kind="scheduler"]')!
    const rows = Array.from(panel.querySelectorAll('dt')).map(dt => dt.textContent)
    const typeIdx = rows.findIndex(dt => dt === 'Type')
    expect(typeIdx).toBeGreaterThan(-1)
    const dd = panel.querySelectorAll('dd')[typeIdx]
    expect(dd?.textContent).toBe('Task')
  })

  it('derives task in read-only summary when no template is bound', async () => {
    await renderPanel({ editing: false, data: {} })
    const panel = container.querySelector('[data-builtin-card-kind="scheduler"]')!
    const rows = Array.from(panel.querySelectorAll('dt')).map(dt => dt.textContent)
    const typeIdx = rows.findIndex(dt => dt === 'Type')
    expect(typeIdx).toBeGreaterThan(-1)
    expect(panel.querySelectorAll('dd')[typeIdx]?.textContent).toBe('Task')
  })

  it('renders a textarea for a task with onChangeBody in editing mode', async () => {
    const onChangeBody = vi.fn()
    await renderPanel({ editing: true, data: { schedule_type: 'prompt' }, body: 'Initial prompt', onChangeBody })
    const textarea = container.querySelector('.mono-card-builtin-data-prompt textarea') as HTMLTextAreaElement | null
    expect(textarea).not.toBeNull()
    expect(textarea?.value).toBe('Initial prompt')
    expect(container.querySelector('.mono-card-builtin-data-prompt-body')).toBeNull()
    // The task prompt textarea carries the "optional when a template is bound" hint.
    expect(container.querySelector('.mono-card-builtin-data-prompt-hint')).not.toBeNull()
  })

  it('calls onChangeBody when the prompt textarea changes', async () => {
    const onChangeBody = vi.fn()
    await renderPanel({ editing: true, data: { schedule_type: 'prompt' }, body: 'Initial', onChangeBody })
    const textarea = container.querySelector('.mono-card-builtin-data-prompt textarea') as HTMLTextAreaElement
    expect(textarea).not.toBeNull()
    await act(async () => {
      fireEvent.change(textarea, { target: { value: 'Updated prompt' } })
    })
    expect(onChangeBody).toHaveBeenCalledWith('Updated prompt')
  })

  it('falls back to the read-only prompt preview when onChangeBody is not provided', async () => {
    await renderPanel({ editing: true, data: { schedule_type: 'prompt' }, body: 'Preview body' })
    expect(container.querySelector('.mono-card-builtin-data-prompt textarea')).toBeNull()
    expect(container.querySelector('.mono-card-builtin-data-prompt-body')?.textContent).toContain('Preview body')
    expect(container.querySelector('.mono-card-builtin-data-prompt-hint')?.textContent).toBeTruthy()
  })

  it('renders a prompt textarea for template-bound tasks too (unified task)', async () => {
    await renderPanel({ editing: true, data: { schedule_type: 'workflow', workflow_template: 'tpl::x' }, onChangeBody: vi.fn() })
    expect(container.querySelector('.mono-card-builtin-data-prompt textarea')).not.toBeNull()
  })

  it('does not render a textarea for agent_action type', async () => {
    await renderPanel({ editing: true, data: { schedule_type: 'agent_action', agent_action: 'pause' }, onChangeBody: vi.fn() })
    expect(container.querySelector('.mono-card-builtin-data-prompt textarea')).toBeNull()
  })
})
