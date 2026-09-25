import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { MonoCardListItem } from '../../../domain/mono-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// ── Hoisted mocks ──────────────────────────────────────────────────

const projectMocks = vi.hoisted(() => ({
  wikiListTimers: vi.fn(),
  wikiToggleTimer: vi.fn(),
  wikiTriggerTimerCard: vi.fn(),
  wikiListTemplateRuns: vi.fn(),
  wikiListCards: vi.fn(),
}))

// Preference persistence (scheduled scope) is actor-owned backend state; stub
// it so tests never touch the workspace preferences client.
const prefMocks = vi.hoisted(() => ({
  loadPreference: vi.fn(),
  savePreference: vi.fn(),
}))

// The workspace client backs the agent-kind picker's listAgentKinds call fired
// on every mount; stub it so no real client is touched.
const workspaceMocks = vi.hoisted(() => ({
  listAgentKinds: vi.fn(),
  createAgent: vi.fn(),
  loadAgent: vi.fn(),
}))

// Real agentListStore would hit the (mocked) client and throw unhandled
// rejections per mount; stub it so the executor/bound-agent pickers get a
// deterministic, controllable agent list instead.
const agentStore = vi.hoisted<{ agents: import('../../../gen-clients/system/types').AgentListItem[] }>(() => ({ agents: [] }))

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/project/client', () => projectMocks)
vi.mock('../../../gen-clients/workspace/client', () => workspaceMocks)
vi.mock('../../../application/theme-persist', () => prefMocks)
vi.mock('../hooks/agentListStore', () => ({
  subscribeAgentListStore: () => () => {},
  getAgentListItems: () => agentStore.agents,
}))
vi.mock('../../../i18n', () => ({
  // Return the key verbatim; params are ignored so assertions stay on keys.
  useI18n: () => ({ t: (key: string) => key }),
}))

import { ScheduledView } from './ScheduledView'

function mkSchedulerCard(id: string, schedule: Record<string, unknown>, templateId = '', raw = ''): MonoCardListItem {
  return {
    id,
    type: 'scheduler',
    tags: [],
    list: [],
    created: '',
    modified: '',
    data: { schedule, workflow_template: templateId },
    raw,
  } as MonoCardListItem
}

function mkTimer(id: string, enabled: boolean, next = '', last = '', status = '', currentInstance = '') {
  return { Id: id, NextFireAt: next, LastRunAt: last, LastStatus: status, Enabled: enabled, CurrentInstance: currentInstance }
}

let container: HTMLDivElement
let root: Root

function renderView(
  cards: MonoCardListItem[],
  projectId?: string,
  props?: { onLocateMap?: (mapId: string) => void; onOpenCard?: (cardId: string, projectId?: string) => void; onCreateScheduler?: (templateCardId: string | null) => Promise<string | null>; onUpdateScheduleCron?: (cardId: string, cron: string, projectId?: string) => Promise<boolean>; onUpdateCardBody?: (cardId: string, body: string, projectId?: string) => Promise<boolean>; onDeleteTimer?: (cardId: string, projectId?: string) => Promise<boolean>; onUpdateAgentActions?: (cardId: string, actions: { action: 'pause' | 'resume'; targetAgent: string }[], projectId?: string) => Promise<boolean>; onUpdateExecutor?: (cardId: string, executor: string, projectId?: string) => Promise<boolean>; onUpdateAgentKind?: (cardId: string, kind: string, projectId?: string) => Promise<boolean>; onUpdateBoundAgent?: (cardId: string, agentRef: string, projectId?: string) => Promise<boolean>; onUpdateTitle?: (cardId: string, title: string, projectId?: string) => Promise<boolean>; isMobile?: boolean; projects?: { ProjectID: string; Name: string; System?: boolean }[]; agentOrder?: string[]; sidebarOrder?: string[] },
) {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  act(() => { root.render(<ScheduledView cards={cards} projectId={projectId} {...props} />) })
}

beforeEach(() => {
  agentStore.agents = []
  projectMocks.wikiListTimers.mockReset()
  projectMocks.wikiToggleTimer.mockReset()
  projectMocks.wikiTriggerTimerCard.mockReset()
  projectMocks.wikiListTemplateRuns.mockReset()
  projectMocks.wikiListCards.mockReset()
  projectMocks.wikiListTimers.mockResolvedValue({ Timers: [] })
  projectMocks.wikiListTemplateRuns.mockResolvedValue({ Runs: [] })
  projectMocks.wikiListCards.mockResolvedValue({ Cards: [], Total: 0 })
  projectMocks.wikiToggleTimer.mockResolvedValue({ Id: '', Enabled: true })
  projectMocks.wikiTriggerTimerCard.mockResolvedValue({ Id: '' })
  prefMocks.loadPreference.mockReset()
  prefMocks.loadPreference.mockResolvedValue(undefined)
  prefMocks.savePreference.mockReset()
  prefMocks.savePreference.mockResolvedValue(undefined)
  workspaceMocks.listAgentKinds.mockReset()
  workspaceMocks.listAgentKinds.mockResolvedValue({ Items: [
    { Kind: 'coder', DisplayName: 'Coder' },
    { Kind: 'dreamer', DisplayName: 'Dreamer' },
  ] })
})

afterEach(async () => {
  await act(async () => { root.unmount() })
  container.remove()
})

async function flush() {
  await act(async () => { await new Promise(r => setTimeout(r, 0)) })
}

async function selectOption(triggerGuideId: string, itemGuideId: string) {
  const trigger = document.querySelector(`[data-guide-id="${triggerGuideId}"]`) as HTMLElement | null
  expect(trigger).not.toBeNull()
  await act(async () => { await userEvent.click(trigger!) })
  const item = document.querySelector(`[data-guide-id="${itemGuideId}"]`) as HTMLElement | null
  expect(item).not.toBeNull()
  await act(async () => { await userEvent.click(item!) })
}

describe('ScheduledView', () => {
  it('renders the timer list with stripped titles and summaries', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({
      Timers: [
        mkTimer('scheduler:每日简报', true),
        mkTimer('scheduler:每周回顾', false),
      ],
    })
    renderView([
      mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }),
      mkSchedulerCard('scheduler:每周回顾', { cron: '0 9 * * 1' }),
    ])
    await flush()

    const items = container.querySelectorAll('.scheduled-item')
    expect(items).toHaveLength(2)
    expect(items[0]!.querySelector('.scheduled-item-title')!.textContent).toBe('每日简报')
    expect(items[0]!.querySelector('.scheduled-item-summary')!.textContent).toContain('scheduled.repeat.everyDay')
    expect(items[0]!.querySelector('.scheduled-item-summary')!.textContent).toContain('8:00')
    // first row auto-selected → detail shows its weekdays-style sibling via tabs
    expect(container.querySelector('.scheduled-detail-title')!.textContent).toBe('每日简报')
  })

  it('filters by the enabled tab', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({
      Timers: [mkTimer('scheduler:a', true), mkTimer('scheduler:b', false)],
    })
    renderView([
      mkSchedulerCard('scheduler:a', { cron: '0 8 * * *' }),
      mkSchedulerCard('scheduler:b', { cron: '0 9 * * 1' }),
    ])
    await flush()
    expect(container.querySelectorAll('.scheduled-item')).toHaveLength(2)

    // Switch to the "paused" tab (3rd tab, label 'scheduled.tab.paused').
    const pausedTab = [...container.querySelectorAll('[role="tab"]')]
      .find(b => b.textContent === 'scheduled.tab.paused')!
    await act(async () => { fireEvent.click(pausedTab) })
    await flush()
    // Only the paused timer remains under the "paused" tab.
    const items = container.querySelectorAll('.scheduled-item')
    expect(items).toHaveLength(1)
    expect(items[0]!.querySelector('.scheduled-item-title')!.textContent).toBe('b')
  })

  it('search narrows the list by title', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({
      Timers: [mkTimer('scheduler:每日简报', true), mkTimer('scheduler:每周回顾', true)],
    })
    renderView([
      mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }),
      mkSchedulerCard('scheduler:每周回顾', { cron: '0 9 * * 1' }),
    ])
    await flush()
    const input = container.querySelector<HTMLInputElement>('.scheduled-search input')!
    await act(async () => { fireEvent.change(input, { target: { value: '简报' } }) })
    await flush()
    const items = container.querySelectorAll('.scheduled-item')
    expect(items).toHaveLength(1)
    expect(items[0]!.querySelector('.scheduled-item-title')!.textContent).toBe('每日简报')
  })

  it('shows run history for the selected timer bound to a template', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    projectMocks.wikiListTemplateRuns.mockResolvedValue({
      Runs: [{ InstanceMapId: 'inst::1', StartedAt: '2026-08-22T00:00:00Z', Source: 'scheduler' }],
    })
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }, 'tpl-daily')])
    await flush()
    await flush() // second flush for the runs effect
    const runItems = container.querySelectorAll('.scheduled-run-item')
    expect(runItems).toHaveLength(1)
    expect(runItems[0]!.querySelector('.scheduled-run-id')!.textContent).toBe('inst::1')
  })

  it('renders run-history ids as locate buttons and fires onLocateMap with the instance id', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    projectMocks.wikiListTemplateRuns.mockResolvedValue({
      Runs: [{ InstanceMapId: 'inst::1', StartedAt: '2026-08-22T00:00:00Z', Source: 'scheduler' }],
    })
    const onLocateMap = vi.fn()
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }, 'tpl-daily')], undefined, { onLocateMap })
    await flush()
    await flush() // runs effect
    const btn = container.querySelector<HTMLButtonElement>('.scheduled-run-btn')!
    expect(btn.textContent).toBe('inst::1')
    await act(async () => { fireEvent.click(btn) })
    expect(onLocateMap).toHaveBeenCalledWith('inst::1')
  })

  it('open-card and locate-template buttons fire their callbacks', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const onLocateMap = vi.fn()
    const onOpenCard = vi.fn()
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }, 'tpl-daily')], undefined, { onLocateMap, onOpenCard })
    await flush()

    // Template id is shown in the detail area; locate button passes it through.
    expect(container.querySelector('.scheduled-template-id')!.textContent).toBe('tpl-daily')
    const locateTpl = container.querySelector<HTMLButtonElement>('.scheduled-template-locate')!
    expect(locateTpl).toBeTruthy()
    await act(async () => { fireEvent.click(locateTpl) })
    expect(onLocateMap).toHaveBeenCalledWith('tpl-daily')

    // Open-card button sits in the detail header next to the pause/run actions.
    const openCard = container.querySelector<HTMLButtonElement>('.scheduled-open-card')!
    expect(openCard).toBeTruthy()
    await act(async () => { fireEvent.click(openCard) })
    expect(onOpenCard).toHaveBeenCalledWith('scheduler:每日简报')
  })

  it('hides open-card / locate-template buttons when the callbacks are omitted', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }, 'tpl-daily')])
    await flush()
    expect(container.querySelector('.scheduled-open-card')).toBeNull()
    expect(container.querySelector('.scheduled-template-locate')).toBeNull()
    // The template id itself stays visible without a locate entry.
    expect(container.querySelector('.scheduled-template-id')!.textContent).toBe('tpl-daily')
  })

  it('new-scheduled-task picker lists prompt entry plus template cards', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [] })
    const onCreateScheduler = vi.fn().mockResolvedValue('scheduler:MyFlow')
    const onOpenCard = vi.fn()
    const tpl = { id: 'tpl::MyFlow', type: 'workflow', tags: [], list: [], created: '', modified: '', data: { template: true } } as MonoCardListItem
    const plain = { id: 'MapA', type: 'workflow', tags: [], list: [], created: '', modified: '', data: {} } as MonoCardListItem
    renderView([tpl, plain], undefined, { onCreateScheduler, onOpenCard })
    await flush()

    // Button hidden without templates prop? No — it is present because onCreateScheduler is given.
    const newBtn = container.querySelector<HTMLButtonElement>('.scheduled-new-task button.scheduled-icon-btn')!
    expect(newBtn).toBeTruthy()
    await act(async () => { fireEvent.click(newBtn) })
    // The prompt and agent-action entries are always present, plus one item per
    // template card.
    const promptEntry = container.querySelector('.scheduled-new-task-prompt')!
    expect(promptEntry).toBeTruthy()
    const items = container.querySelectorAll('.scheduled-new-task-item')
    expect(items).toHaveLength(3)
    expect(items[1]!.textContent).toContain('scheduled.newTask.agentActionEntry')
    expect(items[2]!.textContent).toContain('tpl::MyFlow')
    await act(async () => { fireEvent.click(items[2]!) })
    expect(onCreateScheduler).toHaveBeenCalledWith('tpl::MyFlow', undefined)
    // On success the view opens the created card for editing.
    await flush()
    expect(onOpenCard).toHaveBeenCalledWith('scheduler:MyFlow')
  })

  it('new-scheduled-task button stays enabled without templates and creates a prompt task', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [] })
    const onCreateScheduler = vi.fn().mockResolvedValue('scheduler:prompt-task')
    const onOpenCard = vi.fn()
    renderView([], undefined, { onCreateScheduler, onOpenCard })
    await flush()

    const newBtn = container.querySelector<HTMLButtonElement>('.scheduled-new-task button.scheduled-icon-btn')!
    expect(newBtn.disabled).toBe(false)
    await act(async () => { fireEvent.click(newBtn) })
    const promptEntry = container.querySelector<HTMLButtonElement>('.scheduled-new-task-prompt')!
    expect(promptEntry).toBeTruthy()
    expect(container.querySelector('.scheduled-new-task-empty')).toBeTruthy()
    await act(async () => { fireEvent.click(promptEntry) })
    expect(onCreateScheduler).toHaveBeenCalledWith(null, undefined)
    await flush()
    expect(onOpenCard).toHaveBeenCalledWith('scheduler:prompt-task')
  })

  it('new-scheduled-task button is absent when onCreateScheduler is omitted', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [] })
    renderView([])
    await flush()
    expect(container.querySelector('.scheduled-new-task')).toBeNull()
  })

  it('new-scheduled-task menu closes on outside click but not on inside click', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [] })
    renderView([], undefined, { onCreateScheduler: vi.fn() })
    await flush()

    const newBtn = container.querySelector<HTMLButtonElement>('.scheduled-new-task button.scheduled-icon-btn')!
    await act(async () => { fireEvent.click(newBtn) })
    expect(container.querySelector('.scheduled-new-task-menu')).toBeTruthy()

    // Clicking inside the menu must not close it (mousedown precedes the item click).
    await act(async () => {
      fireEvent.mouseDown(container.querySelector('.scheduled-new-task-menu')!)
    })
    expect(container.querySelector('.scheduled-new-task-menu')).toBeTruthy()

    // Clicking anywhere outside closes it.
    await act(async () => {
      fireEvent.mouseDown(container.querySelector('.scheduled-list') ?? document.body)
    })
    expect(container.querySelector('.scheduled-new-task-menu')).toBeNull()
  })

  it('schedule editor always visible and saves the rebuilt cron via onUpdateScheduleCron', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const onUpdateScheduleCron = vi.fn().mockResolvedValue(true)
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }, 'tpl-daily')], undefined, { onUpdateScheduleCron })
    await flush()

    const editor = container.querySelector('.scheduled-schedule-editor')!
    expect(editor).toBeTruthy()

    // Draft initialized from the card cron: repeat=everyDay, time=08:00.
    const repeatValue = editor.querySelector('[data-slot="select-value"]')!
    expect(repeatValue.textContent).toBe('scheduled.repeat.everyDay')
    const time = editor.querySelector<HTMLInputElement>('input[type="time"]')!
    expect(time.value).toBe('08:00')

    // Untouched draft: no modifications → the save button stays hidden.
    expect(editor.querySelector('.scheduled-editor-btn.primary')).toBeNull()

    // Change to weekly Friday 10:30 → cron "30 10 * * 5".
    await selectOption('scheduled-editor-repeat-trigger', 'scheduled-editor-repeat-weekly')
    await selectOption('scheduled-editor-dow-trigger', 'scheduled-editor-dow-5')
    await act(async () => { fireEvent.change(time, { target: { value: '10:30' } }) })
    const save = editor.querySelector<HTMLButtonElement>('.scheduled-editor-btn.primary')!
    expect(save).toBeTruthy()
    await act(async () => { fireEvent.click(save) })
    expect(onUpdateScheduleCron).toHaveBeenCalledWith('scheduler:每日简报', '30 10 * * 5')
    // Timers reload after a successful save.
    expect(projectMocks.wikiListTimers).toHaveBeenCalledTimes(2)
  })

  it('schedule editor shows an error without saving when the custom cron is malformed', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const onUpdateScheduleCron = vi.fn().mockResolvedValue(true)
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '*/5 * * * *' }, '')], undefined, { onUpdateScheduleCron })
    await flush()

    const editor = container.querySelector('.scheduled-schedule-editor')!
    await selectOption('scheduled-editor-repeat-trigger', 'scheduled-editor-repeat-custom')
    const cronInput = editor.querySelector<HTMLInputElement>('.scheduled-editor-cron input')!
    await act(async () => { fireEvent.change(cronInput, { target: { value: 'not a cron' } }) })
    await act(async () => { fireEvent.click(editor.querySelector<HTMLButtonElement>('.scheduled-editor-btn.primary')!) })
    expect(onUpdateScheduleCron).not.toHaveBeenCalled()
    expect(container.querySelector('.scheduled-editor-error')).toBeTruthy()
  })

  it('seeds the cron editor and dirty baseline from expression || cron when both are set', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:双字段', true)] })
    const onUpdateScheduleCron = vi.fn().mockResolvedValue(true)
    // The card carries both fields; expression is the effective schedule.
    renderView(
      [mkSchedulerCard('scheduler:双字段', { cron: '0 8 * * *', expression: '*/5 * * * *' }, '')],
      undefined,
      { onUpdateScheduleCron },
    )
    await flush()

    const editor = container.querySelector('.scheduled-schedule-editor')!
    // Custom-kind segments are seeded from the expression, not the raw cron.
    const fields = [...editor.querySelectorAll<HTMLInputElement>('[data-guide-id^="scheduled-cron-field-"]')]
    expect(fields.map(f => f.value)).toEqual(['*/5', '*', '*', '*', '*'])
    // Seeding and baseline share the same precedence → no phantom modification.
    expect(editor.querySelector('.scheduled-editor-btn.primary')).toBeNull()
  })

  it('flags a non-5-field custom cron instead of silently padding a "*" field', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:坏任务', true)] })
    const onUpdateScheduleCron = vi.fn().mockResolvedValue(true)
    renderView([mkSchedulerCard('scheduler:坏任务', { cron: '0 8 * *' }, '')], undefined, { onUpdateScheduleCron })
    await flush()

    const editor = container.querySelector('.scheduled-schedule-editor')!
    // The editor reports the malformed schedule up front; it does not fabricate
    // a '*' to fill the missing slot.
    expect(editor.querySelector('.scheduled-editor-error')!.textContent).toBe('scheduled.edit.invalid')
    const fields = [...editor.querySelectorAll<HTMLInputElement>('[data-guide-id^="scheduled-cron-field-"]')]
    expect(fields.map(f => f.value)).toEqual(['0', '8', '*', '*', ''])
  })

  it('custom cron shows five labeled segments with hover help and edits rebuild it positionally', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:自定义任务', true)] })
    const onUpdateScheduleCron = vi.fn().mockResolvedValue(true)
    renderView([mkSchedulerCard('scheduler:自定义任务', { cron: '*/5 9 * * *' }, '')], undefined, { onUpdateScheduleCron })
    await flush()

    const segments = [...container.querySelectorAll('.scheduled-editor-cron label')]
    expect(segments.map(s => s.querySelector('.text-xs')!.textContent)).toEqual([
      'scheduled.cron.minute', 'scheduled.cron.hour', 'scheduled.cron.dom', 'scheduled.cron.month', 'scheduled.cron.dow',
    ])
    // Every segment carries a hover explanation (native title tooltip).
    segments.forEach(s => expect(s.getAttribute('title')).toMatch(/^scheduled\.cron\.\w+\.help$/))

    const fields = () => [...container.querySelectorAll<HTMLInputElement>('[data-guide-id^="scheduled-cron-field-"]')]
    expect(fields().map(f => f.value)).toEqual(['*/5', '9', '*', '*', '*'])

    // Editing one segment rebuilds the cron positionally without shifting the rest.
    await act(async () => { fireEvent.change(fields()[0]!, { target: { value: '15' } }) })
    expect(fields().map(f => f.value)).toEqual(['15', '9', '*', '*', '*'])

    await act(async () => { fireEvent.click(container.querySelector<HTMLButtonElement>('.scheduled-editor-btn.primary')!) })
    expect(onUpdateScheduleCron).toHaveBeenCalledWith('scheduler:自定义任务', '15 9 * * *')
  })

  it('schedule editor is hidden when onUpdateScheduleCron is omitted', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }, '')])
    await flush()
    expect(container.querySelector('.scheduled-schedule-editor')).toBeNull()
  })

  it('prompt body is always editable and saves via onUpdateCardBody', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const onUpdateCardBody = vi.fn().mockResolvedValue(true)
    const raw = '---\nid: scheduler:每日简报\n---\n你好，生成简报'
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }, '', raw)], undefined, { onUpdateCardBody })
    await flush()

    const textarea = container.querySelector<HTMLTextAreaElement>('.scheduled-prompt-textarea')!
    expect(textarea).toBeTruthy()
    expect(textarea.value).toBe('你好，生成简报')

    fireEvent.change(textarea, { target: { value: '更新后的提示词' } })
    const save = container.querySelector<HTMLButtonElement>('.scheduled-prompt-editor .scheduled-editor-btn.primary')!
    expect(save).toBeTruthy()
    await act(async () => { fireEvent.click(save) })
    expect(onUpdateCardBody).toHaveBeenCalledWith('scheduler:每日简报', '更新后的提示词')
    // Timers reload after a successful save; the textarea stays visible.
    expect(projectMocks.wikiListTimers).toHaveBeenCalledTimes(2)
    expect(container.querySelector('.scheduled-prompt-textarea')).toBeTruthy()
  })

  it('prompt editor omits the save button when onUpdateCardBody is omitted', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const raw = '---\nid: scheduler:每日简报\n---\n你好'
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }, '', raw)])
    await flush()
    expect(container.querySelector('.scheduled-prompt-textarea')).toBeTruthy()
    expect(container.querySelector('.scheduled-prompt-editor .scheduled-editor-btn.primary')).toBeNull()
  })

  it('mobile mode lands on the list, then the back button returns from the detail', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }, '')], undefined, { isMobile: true })
    await flush()

    const root = container.querySelector('.scheduled-view')!
    // No auto-select on mobile: the list panel is the landing state.
    expect(root.classList.contains('mobile')).toBe(true)
    expect(root.classList.contains('selected')).toBe(false)
    expect(container.querySelector('.scheduled-back')).toBeNull()

    await act(async () => { fireEvent.click(container.querySelector<HTMLButtonElement>('.scheduled-item')!) })
    expect(root.classList.contains('selected')).toBe(true)

    const back = container.querySelector<HTMLButtonElement>('.scheduled-back')!
    await act(async () => { fireEvent.click(back) })
    expect(root.classList.contains('selected')).toBe(false)
  })

  it('shows the empty state when there are no timers', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [] })
    renderView([])
    await flush()
    expect(container.querySelector('.scheduled-empty-title')!.textContent).toBe('scheduled.empty.title')
    expect(container.querySelector('.scheduled-detail-empty')).toBeTruthy()
  })

  it('toggles a timer and reloads the list', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })], 'proj-1')
    await flush()
    await act(async () => { fireEvent.click(container.querySelector('.scheduled-icon-btn[title="scheduled.runNow"]')!) })
    await flush()
    // The run-now call is routed to the project actor via { target: projectId }.
    expect(projectMocks.wikiTriggerTimerCard).toHaveBeenCalledWith({}, { Id: 'scheduler:每日简报' }, { target: 'proj-1' })
  })

  it('surfaces a toggle failure instead of letting the rejection escape', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    projectMocks.wikiToggleTimer.mockRejectedValue(new Error('toggle boom'))
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })], 'proj-1')
    await flush()

    // The header toggle (enabled timer → the title advertises pausing).
    const toggle = container.querySelector<HTMLButtonElement>('.scheduled-icon-btn[title="scheduled.status.paused"]')!
    expect(toggle).toBeTruthy()
    await act(async () => { fireEvent.click(toggle) })
    await flush()

    expect(container.querySelector('.scheduled-error')!.textContent).toBe('toggle boom')
    // Failed toggle does not reload the list (only the initial load ran).
    expect(projectMocks.wikiListTimers).toHaveBeenCalledTimes(1)
  })

  it('delete button shows confirm dialog, on confirm calls onDeleteTimer and clears selection', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const onDeleteTimer = vi.fn().mockResolvedValue(true)
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })], undefined, { onDeleteTimer })
    await flush()
    // After the first loadTimers call, set up the mock to return empty so
    // the auto-select effect does not re-select the deleted row.
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [] })

    // Delete button is in the detail header.
    const deleteBtn = container.querySelector<HTMLButtonElement>('.scheduled-delete-timer')!
    expect(deleteBtn).toBeTruthy()
    await act(async () => { fireEvent.click(deleteBtn) })

    // Confirm dialog appears.
    const dialog = container.querySelector('.confirm-dialog')!
    expect(dialog).toBeTruthy()
    expect(dialog.querySelector('.confirm-dialog-title')!.textContent).toBe('scheduled.delete.title')

    // Click confirm.
    const confirmBtn = dialog.querySelector<HTMLButtonElement>('.confirm-dialog-btn.confirm')!
    await act(async () => { fireEvent.click(confirmBtn) })
    await flush()
    expect(onDeleteTimer).toHaveBeenCalledWith('scheduler:每日简报')
    // Selection cleared (detail panel shows the empty state).
    expect(container.querySelector('.scheduled-detail-empty')).toBeTruthy()
    // Timers reloaded.
    expect(projectMocks.wikiListTimers).toHaveBeenCalledTimes(2)
  })

  it('keeps the delete dialog open and reports the error when deletion rejects', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const onDeleteTimer = vi.fn().mockRejectedValue(new Error('delete boom'))
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })], undefined, { onDeleteTimer })
    await flush()

    await act(async () => { fireEvent.click(container.querySelector('.scheduled-delete-timer')!) })
    const dialog = container.querySelector('.confirm-dialog')!
    await act(async () => { fireEvent.click(dialog.querySelector('.confirm-dialog-btn.confirm')!) })
    await flush()

    expect(onDeleteTimer).toHaveBeenCalledWith('scheduler:每日简报')
    // Dialog stays open for a retry, the selection is kept, and the failure is shown.
    expect(container.querySelector('.confirm-dialog')).toBeTruthy()
    expect(container.querySelector('.scheduled-detail-title')!.textContent).toBe('每日简报')
    expect(container.querySelector('.scheduled-error')!.textContent).toBe('delete boom')
  })

  it('reports a false delete result without clearing the selection', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const onDeleteTimer = vi.fn().mockResolvedValue(false)
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })], undefined, { onDeleteTimer })
    await flush()

    await act(async () => { fireEvent.click(container.querySelector('.scheduled-delete-timer')!) })
    const dialog = container.querySelector('.confirm-dialog')!
    await act(async () => { fireEvent.click(dialog.querySelector('.confirm-dialog-btn.confirm')!) })
    await flush()

    expect(container.querySelector('.confirm-dialog')).toBeTruthy()
    expect(container.querySelector('.scheduled-error')!.textContent).toBe('scheduled.edit.saveFailed')
    // Failed (ok===false) delete does not reload the timers.
    expect(projectMocks.wikiListTimers).toHaveBeenCalledTimes(1)
  })

  it('right-click on a row opens the context menu', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const onOpenCard = vi.fn()
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })], undefined, { onOpenCard })
    await flush()

    const item = container.querySelector<HTMLButtonElement>('.scheduled-item')!
    await act(async () => { fireEvent.contextMenu(item) })
    const menu = container.querySelector('.scheduled-context-menu')!
    expect(menu).toBeTruthy()
    // Title in the menu header.
    expect(menu.querySelector('.scheduled-context-menu-title')!.textContent).toBe('每日简报')
  })

  it('context menu delete item opens the confirm dialog', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const onDeleteTimer = vi.fn().mockResolvedValue(true)
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })], undefined, { onDeleteTimer })
    await flush()

    // Right-click the row.
    const item = container.querySelector<HTMLButtonElement>('.scheduled-item')!
    await act(async () => { fireEvent.contextMenu(item) })
    const menu = container.querySelector('.scheduled-context-menu')!
    expect(menu).toBeTruthy()

    // Find the delete menu item (danger).
    const deleteItem = menu.querySelector<HTMLButtonElement>('.scheduled-context-menu-item-danger')!
    expect(deleteItem).toBeTruthy()
    await act(async () => { fireEvent.click(deleteItem) })
    await flush()

    // Confirm dialog appears.
    const dialog = container.querySelector('.confirm-dialog')!
    expect(dialog).toBeTruthy()
    expect(dialog.querySelector('.confirm-dialog-title')!.textContent).toBe('scheduled.delete.title')

    // Click confirm.
    const confirmBtn = dialog.querySelector<HTMLButtonElement>('.confirm-dialog-btn.confirm')!
    await act(async () => { fireEvent.click(confirmBtn) })
    await flush()
    expect(onDeleteTimer).toHaveBeenCalledWith('scheduler:每日简报')
  })

  it('shows the current instance with a locate button when the timer is running', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({
      Timers: [mkTimer('scheduler:每日简报', true, '', '', 'running', 'inst::2026-08-24::tpl::my-map')],
    })
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }, 'tpl::my-map')])
    await flush()
    // The current-instance section is visible. Both the template and the
    // current-instance rows reuse .scheduled-template-id, so resolve the one
    // whose text is the instance id (order-independent).
    const instanceIds = [...container.querySelectorAll('.scheduled-detail-section .scheduled-template-id')]
      .map(el => el.textContent)
      .filter(t => t?.startsWith('inst::'))
    expect(instanceIds).toEqual(['inst::2026-08-24::tpl::my-map'])
    // The run-now button is disabled when the instance is running.
    const runBtn = container.querySelector<HTMLButtonElement>('.scheduled-icon-btn[aria-label="scheduled.runNow"]')!
    expect(runBtn.disabled).toBe(true)
  })

  it('shows a task type badge with template task mode in the list row', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' }, 'tpl-daily')])
    await flush()
    const typeBadge = container.querySelector('.scheduled-type-badge')!
    expect(typeBadge.classList.contains('task')).toBe(true)
    const modeBadge = container.querySelector('.scheduled-task-mode-badge')!
    expect(modeBadge.classList.contains('template')).toBe(true)
  })

  it('shows a task type badge with prompt task mode when no template is bound', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:提示测试', true)] })
    renderView([mkSchedulerCard('scheduler:提示测试', { cron: '0 9 * * *' })])
    await flush()
    const typeBadge = container.querySelector('.scheduled-type-badge')!
    expect(typeBadge.classList.contains('task')).toBe(true)
    const modeBadge = container.querySelector('.scheduled-task-mode-badge')!
    expect(modeBadge.classList.contains('prompt')).toBe(true)
  })

  it('prompt type detail shows the prompt section heading and always-editable textarea', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:提示测试', true)] })
    const card = mkSchedulerCard('scheduler:提示测试', { cron: '0 9 * * *' })
    card.data!.schedule_type = 'prompt'
    renderView([card])
    await flush()
    const promptSection = container.querySelector('.scheduled-prompt-editor')!.closest('.scheduled-detail-section')!
    expect(promptSection.querySelector('.scheduled-section-heading')!.textContent).toContain('scheduled.section.prompt')
    expect(promptSection.querySelector('.scheduled-prompt-textarea')).toBeTruthy()
  })

  it('prompt detail section has no edit button — editing is always inline', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:提示测试', true)] })
    const card = mkSchedulerCard('scheduler:提示测试', { cron: '0 9 * * *' })
    card.data!.schedule_type = 'prompt'
    const onOpenCard = vi.fn()
    renderView([card], undefined, { onOpenCard })
    await flush()
    const promptSection = container.querySelector('.scheduled-prompt-editor')!.closest('.scheduled-detail-section')!
    expect(promptSection.querySelector('button.scheduled-edit-schedule')).toBeNull()
    expect(promptSection.querySelector('.scheduled-prompt-textarea')).toBeTruthy()
  })

  it('maps legacy schedule_type=prompt to task with template mode when a template is bound', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:模板化提示', true)] })
    const card = mkSchedulerCard('scheduler:模板化提示', { cron: '0 9 * * *' }, 'tpl::x')
    card.data!.schedule_type = 'prompt'
    renderView([card])
    await flush()
    // Legacy prompt value converges to the unified task type; the task mode is
    // derived from template binding (template).
    const typeBadge = container.querySelector('.scheduled-type-badge')!
    expect(typeBadge.classList.contains('task')).toBe(true)
    const modeBadge = container.querySelector('.scheduled-task-mode-badge')!
    expect(modeBadge.classList.contains('template')).toBe(true)
  })

  it('adds an agent-state action with an empty target and does not persist an invalid kind', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:agent任务', true)] })
    const card = mkSchedulerCard('scheduler:agent任务', { cron: '0 8 * * *' })
    card.data!.agent_actions = JSON.stringify([{ action: 'pause', targetAgent: 'agent:a1' }])
    const onUpdateAgentActions = vi.fn().mockResolvedValue(true)
    renderView([card], undefined, { onUpdateAgentActions })
    await flush()

    expect(container.querySelectorAll('.scheduled-agent-action-row')).toHaveLength(1)

    const addBtn = container.querySelector<HTMLButtonElement>('.scheduled-agent-actions-footer button')!
    await act(async () => { fireEvent.click(addBtn) })
    const rows = container.querySelectorAll('.scheduled-agent-action-row')
    expect(rows).toHaveLength(2)
    // The new row defaults to no target — never the 'agent:coder' kind template,
    // which is not a concrete, resolvable agent instance.
    expect(rows[1]!.textContent).not.toContain('agent:coder')

    // Saving filters the still-empty target out rather than persisting an
    // unresolvable reference.
    const saveBtn = container.querySelector<HTMLButtonElement>('.scheduled-agent-actions-footer button:last-child')!
    await act(async () => { fireEvent.click(saveBtn) })
    await flush()
    expect(onUpdateAgentActions).toHaveBeenCalledWith('scheduler:agent任务', [
      { action: 'pause', targetAgent: 'agent:a1' },
    ])
  })

  it('deletes an agent-state action and saves an empty action list', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:agent任务', true)] })
    const card = mkSchedulerCard('scheduler:agent任务', { cron: '0 8 * * *' })
    card.data!.agent_actions = JSON.stringify([{ action: 'resume', targetAgent: 'agent:a1' }])
    const onUpdateAgentActions = vi.fn().mockResolvedValue(true)
    renderView([card], undefined, { onUpdateAgentActions })
    await flush()

    expect(container.querySelectorAll('.scheduled-agent-action-row')).toHaveLength(1)

    const deleteBtn = container.querySelector<HTMLButtonElement>('.scheduled-agent-action-row button[title="common.delete"]')!
    await act(async () => { fireEvent.click(deleteBtn) })
    expect(container.querySelectorAll('.scheduled-agent-action-row')).toHaveLength(0)

    const saveBtn = container.querySelector<HTMLButtonElement>('.scheduled-agent-actions-footer button:last-child')!
    await act(async () => { fireEvent.click(saveBtn) })
    await flush()
    expect(onUpdateAgentActions).toHaveBeenCalledWith('scheduler:agent任务', [])
  })

  it('selects a located task from a pending scheduled locate request', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({
      Timers: [mkTimer('scheduler:甲', true), mkTimer('scheduler:乙', true)],
    })
    renderView([
      mkSchedulerCard('scheduler:甲', { cron: '0 8 * * *' }),
      mkSchedulerCard('scheduler:乙', { cron: '0 9 * * 1' }),
    ])
    await flush()

    // Default desktop selection is the first row.
    expect(container.querySelector('.scheduled-detail-title')!.textContent).toBe('甲')

    // A locate request for the second row switches the selection to it.
    const { requestScheduledLocate } = await import('./scheduledLocateStore')
    await act(async () => { requestScheduledLocate({ cardId: 'scheduler:乙' }) })
    await flush()
    expect(container.querySelector('.scheduled-detail-title')!.textContent).toBe('乙')
    expect(container.querySelector('.scheduled-item.selected .scheduled-item-title')!.textContent).toBe('乙')
  })

  it('prompt-mode task shows the agent kind picker and saves data.agent_kind', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [{ ...mkTimer('scheduler:每日简报', true), AgentKind: 'coder' }] })
    const onUpdateAgentKind = vi.fn().mockResolvedValue(true)
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })], undefined, { onUpdateAgentKind })
    await flush()
    await flush() // agent kinds fetch

    // Prompt-mode task (no template) renders the kind picker instead of the
    // existing-agent executor select; kinds come from workspace.listAgentKinds.
    expect(workspaceMocks.listAgentKinds).toHaveBeenCalled()
    expect(container.querySelector('[data-guide-id="scheduled-agent-kind-trigger"]')).toBeTruthy()
    expect(container.querySelector('[data-guide-id="scheduled-agent-kind-trigger"]')!.textContent).toContain('Coder')
    expect(container.querySelector('.scheduled-executor-row')!.textContent).toContain('scheduled.agent.kindHint')

    await selectOption('scheduled-agent-kind-trigger', 'scheduled-agent-kind-dreamer')
    expect(onUpdateAgentKind).toHaveBeenCalledWith('scheduler:每日简报', 'dreamer')
  })

  it('prompt-mode picker also lists existing agents and binds one via onUpdateBoundAgent', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    agentStore.agents = [
      { Id: 'id-驻留', ActorId: 'actor-驻留', DisplayName: '驻留Agent', ProjectId: 'proj-1', AgentKind: 'coder', LoadState: 'loaded' },
      { Id: 'id-休眠', ActorId: 'actor-休眠', DisplayName: '休眠Agent', ProjectId: 'proj-1', AgentKind: 'coder', LoadState: 'unloaded' },
    ] as never
    const onUpdateBoundAgent = vi.fn().mockResolvedValue(true)
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })], 'proj-1', { onUpdateBoundAgent })
    await flush()
    await flush() // agent kinds fetch

    // Unbound task: the kind shows as the current value, kind hint visible.
    expect(container.querySelector('[data-guide-id="scheduled-agent-kind-trigger"]')!.textContent).toContain('Coder')
    expect(container.querySelector('.scheduled-executor-row')!.textContent).toContain('scheduled.agent.kindHint')

    // Selecting an existing agent from the second group writes the bound contract.
    await selectOption('scheduled-agent-kind-trigger', 'scheduled-agent-existing-id-驻留')
    expect(onUpdateBoundAgent).toHaveBeenCalledWith('scheduler:每日简报', 'agent:id-驻留')
  })

  it('agent pickers follow the sidebar agent order from agentOrder', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:排序', true)] })
    agentStore.agents = [
      { Id: 'id-b', ActorId: 'actor-b', DisplayName: 'AgentB', ProjectId: 'proj-1', AgentKind: 'coder', LoadState: 'loaded' },
      { Id: 'id-c', ActorId: 'actor-c', DisplayName: 'AgentC', ProjectId: 'proj-1', AgentKind: 'coder', LoadState: 'loaded' },
      { Id: 'id-a', ActorId: 'actor-a', DisplayName: 'AgentA', ProjectId: 'proj-1', AgentKind: 'coder', LoadState: 'loaded' },
    ] as never
    renderView(
      [mkSchedulerCard('scheduler:排序', { cron: '0 8 * * *' })],
      'proj-1',
      { onUpdateBoundAgent: vi.fn().mockResolvedValue(true), agentOrder: ['id-a', 'id-b', 'id-c'] },
    )
    await flush()
    await flush()

    // Dropdown content renders in a body portal — query document, not container.
    await act(async () => {
      await userEvent.click(container.querySelector('[data-guide-id="scheduled-agent-kind-trigger"]')!)
    })
    const ids = [...document.querySelectorAll('[data-guide-id^="scheduled-agent-existing-"]')]
      .map(el => (el as HTMLElement).dataset.guideId!.replace('scheduled-agent-existing-', ''))
    expect(ids).toEqual(['id-a', 'id-b', 'id-c'])
  })

  it('agent picker options show the sidebar avatar and runtime status', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:头像', true)] })
    agentStore.agents = [
      { Id: 'id-跑', ActorId: 'actor-跑', DisplayName: '跑动Agent', ProjectId: 'proj-1', AgentKind: 'coder', LoadState: 'loaded', Runtime: { State: 'running' } },
    ] as never
    renderView(
      [mkSchedulerCard('scheduler:头像', { cron: '0 8 * * *' })],
      'proj-1',
      { onUpdateBoundAgent: vi.fn().mockResolvedValue(true) },
    )
    await flush()
    await flush()

    await act(async () => {
      await userEvent.click(container.querySelector('[data-guide-id="scheduled-agent-kind-trigger"]')!)
    })
    const option = document.querySelector('[data-guide-id="scheduled-agent-existing-id-跑"]')!
    expect(option.querySelector('.ai-sidebar-session-avatar')).not.toBeNull()
    // working spinner class + the runtime status label
    expect(option.querySelector('.ai-sidebar-session-avatar.working')).not.toBeNull()
    expect(option.querySelector('.scheduled-agent-status')!.textContent).toBe('running')
  })

  it('agent pickers exclude unloaded agents', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:过滤', true)] })
    agentStore.agents = [
      { Id: 'id-驻留', ActorId: 'actor-驻留', DisplayName: '驻留Agent', ProjectId: 'proj-1', AgentKind: 'coder', LoadState: 'loaded' },
      { Id: 'id-休眠', ActorId: 'actor-休眠', DisplayName: '休眠Agent', ProjectId: 'proj-1', AgentKind: 'coder', LoadState: 'unloaded' },
    ] as never
    renderView(
      [mkSchedulerCard('scheduler:过滤', { cron: '0 8 * * *' })],
      'proj-1',
      { onUpdateBoundAgent: vi.fn().mockResolvedValue(true) },
    )
    await flush()
    await flush()

    await act(async () => {
      await userEvent.click(container.querySelector('[data-guide-id="scheduled-agent-kind-trigger"]')!)
    })
    const ids = [...document.querySelectorAll('[data-guide-id^="scheduled-agent-existing-"]')]
      .map(el => (el as HTMLElement).dataset.guideId!.replace('scheduled-agent-existing-', ''))
    expect(ids).toEqual(['id-驻留'])
    expect(document.body.textContent).not.toContain('休眠Agent')
  })

  it('bound prompt task shows the bound agent name and bound hint', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:绑定任务', true)] })
    agentStore.agents = [
      { Id: 'id-驻留', ActorId: 'actor-驻留', DisplayName: '驻留Agent', ProjectId: 'proj-1', AgentKind: 'coder', LoadState: 'loaded' },
    ] as never
    const onUpdateBoundAgent = vi.fn().mockResolvedValue(true)
    // The dual-mode contract lives on the card data top level (like
    // executor/agent_kind), not inside the nested schedule object.
    const card = mkSchedulerCard('scheduler:绑定任务', { cron: '0 8 * * *' })
    const cardData = card.data as Record<string, unknown>
    cardData.bind_mode = 'bound'
    cardData.bound_agent = 'agent:id-驻留'
    renderView([card], 'proj-1', { onUpdateBoundAgent })
    await flush()
    await flush()

    const row = container.querySelector('.scheduled-executor-row')!
    expect(row.textContent).toContain('驻留Agent')
    expect(row.textContent).toContain('scheduled.agent.boundHint')
  })

  it('template-mode task keeps the existing-agent executor picker', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:模板任务', true)] })
    agentStore.agents = [
      { Id: 'id-1', ActorId: 'actor-1', DisplayName: '驻留Agent', ProjectId: 'proj-1', AgentKind: 'coder', LoadState: 'loaded' },
    ] as never
    const onUpdateExecutor = vi.fn().mockResolvedValue(true)
    renderView([mkSchedulerCard('scheduler:模板任务', { cron: '0 8 * * *' }, 'tpl-daily')], 'proj-1', { onUpdateExecutor })
    await flush()
    await flush()

    // Template-bound tasks run the workflow on an existing agent — no kind picker.
    expect(container.querySelector('[data-guide-id="scheduled-agent-kind-trigger"]')).toBeNull()
    const row = container.querySelector('.scheduled-executor-row')!
    expect(row.querySelector('button')).toBeTruthy()
    expect(row.textContent).not.toContain('scheduled.agent.kindHint')
  })

  it('data.title overrides the stripped card id in list and detail', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const card = mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })
    ;(card.data as Record<string, unknown>).title = '自定义名'
    renderView([card])
    await flush()

    expect(container.querySelector('.scheduled-item-title')!.textContent).toBe('自定义名')
    expect(container.querySelector('.scheduled-detail-title')!.textContent).toBe('自定义名')
  })

  it('renames the task in place: click title, edit, Enter commits', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const onUpdateTitle = vi.fn().mockResolvedValue(true)
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })], undefined, { onUpdateTitle })
    await flush()

    await act(async () => { fireEvent.click(container.querySelector('.scheduled-detail-title')!) })
    const input = container.querySelector<HTMLInputElement>('.scheduled-detail-title-input')!
    expect(input.value).toBe('每日简报')

    await act(async () => { fireEvent.change(input, { target: { value: '新标题' } }) })
    await act(async () => { fireEvent.keyDown(input, { key: 'Enter' }) })
    await flush()

    expect(onUpdateTitle).toHaveBeenCalledWith('scheduler:每日简报', '新标题')
    // Input closes after commit; the heading renders again (still the old
    // title here — the renamed value arrives via the cards prop).
    expect(container.querySelector('.scheduled-detail-title-input')).toBeNull()
    expect(container.querySelector('.scheduled-detail-title')!.textContent).toBe('每日简报')
  })

  it('escape cancels the in-place rename without calling back', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    const onUpdateTitle = vi.fn().mockResolvedValue(true)
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })], undefined, { onUpdateTitle })
    await flush()

    await act(async () => { fireEvent.click(container.querySelector('.scheduled-detail-title')!) })
    const input = container.querySelector<HTMLInputElement>('.scheduled-detail-title-input')!
    await act(async () => { fireEvent.change(input, { target: { value: '改一半' } }) })
    await act(async () => { fireEvent.keyDown(input, { key: 'Escape' }) })

    expect(container.querySelector('.scheduled-detail-title-input')).toBeNull()
    expect(onUpdateTitle).not.toHaveBeenCalled()
    expect(container.querySelector('.scheduled-detail-title')!.textContent).toBe('每日简报')
  })

  it('title stays static when no onUpdateTitle callback is given', async () => {
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [mkTimer('scheduler:每日简报', true)] })
    renderView([mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })])
    await flush()

    const title = container.querySelector('.scheduled-detail-title')!
    expect(title.className).not.toContain('editable')
    await act(async () => { fireEvent.click(title) })
    expect(container.querySelector('.scheduled-detail-title-input')).toBeNull()
  })

  // ── Scope picker: global / per-project browsing ──────────────────────

  const scopeProjects = [
    { ProjectID: 'proj-1', Name: 'Alpha' },
    { ProjectID: 'proj-2', Name: 'Beta' },
    { ProjectID: 'sys', Name: 'System', System: true },
  ]

  function mkServerCard(id: string, data: Record<string, unknown>, raw = '') {
    return { Id: id, Type: 'scheduler', Tags: [], List: [], Modified: '', Raw: raw, Data: data }
  }

  async function openScopeMenuAndPick(index: number) {
    await act(async () => { fireEvent.click(container.querySelector<HTMLButtonElement>('.scheduled-scope-btn')!) })
    const items = container.querySelectorAll('.scheduled-scope-item')
    expect(items.length).toBeGreaterThan(index)
    await act(async () => { fireEvent.click(items[index]!) })
    await flush()
  }

  it('scope menu lists the active project, global, and user projects', async () => {
    renderView([], 'proj-1', { projects: scopeProjects })
    await flush()

    await act(async () => { fireEvent.click(container.querySelector<HTMLButtonElement>('.scheduled-scope-btn')!) })
    const items = container.querySelectorAll('.scheduled-scope-item')
    // Active entry, global entry, one per user project (system project filtered out).
    expect(items).toHaveLength(4)
    expect(items[1]!.textContent).toContain('scheduled.scope.global')
    // [2] = Alpha (also the active project, tagged as current), [3] = Beta.
    expect(items[2]!.textContent).toContain('Alpha')
    expect(items[2]!.textContent).toContain('scheduled.scope.current')
    expect(items[3]!.textContent).toContain('Beta')
    // Active scope loads exactly one project target.
    expect(projectMocks.wikiListTimers).toHaveBeenCalledTimes(1)
    expect(projectMocks.wikiListTimers).toHaveBeenCalledWith({}, { target: 'proj-1' })
  })

  it('global scope fans out timers per user project and badges rows with the origin project', async () => {
    projectMocks.wikiListTimers.mockImplementation(async (_c: unknown, opts?: { target?: string }) =>
      opts?.target === 'proj-2'
        ? { Timers: [mkTimer('scheduler:foreign', true)] }
        : { Timers: [mkTimer('scheduler:每日简报', true)] })
    projectMocks.wikiListCards.mockResolvedValue({
      Cards: [mkServerCard('scheduler:foreign', { schedule: { cron: '0 8 * * *' } })],
      Total: 1,
    })
    const onOpenCard = vi.fn()
    const onUpdateScheduleCron = vi.fn().mockResolvedValue(true)
    renderView(
      [mkSchedulerCard('scheduler:每日简报', { cron: '0 8 * * *' })],
      'proj-1',
      { projects: scopeProjects, onOpenCard, onUpdateScheduleCron, onCreateScheduler: vi.fn().mockResolvedValue(null) },
    )
    await flush()

    // New-task button exists in the active scope.
    expect(container.querySelector('.scheduled-new-task')).toBeTruthy()

    // items[0] = active entry, items[1] = global, items[2..] = projects.
    await openScopeMenuAndPick(1)

    // One listTimers call per user project (system project excluded).
    const targets = projectMocks.wikiListTimers.mock.calls.map(c => c[1])
    expect(targets).toContainEqual({ target: 'proj-1' })
    expect(targets).toContainEqual({ target: 'proj-2' })
    // Foreign project scheduler cards fetched for the join.
    expect(projectMocks.wikiListCards).toHaveBeenCalledWith(
      {}, { Flat: true, IncludeRaw: true, Limit: -1, Type: 'scheduler' }, { target: 'proj-2' },
    )
    expect(projectMocks.wikiListCards).toHaveBeenCalledWith(
      {}, { Flat: true, Limit: -1, Type: 'workflow' }, { target: 'proj-2' },
    )

    const items = container.querySelectorAll('.scheduled-item')
    expect(items).toHaveLength(2)
    // Global view badges every row with its origin project.
    const badges = [...container.querySelectorAll('.scheduled-item-project')]
    expect(badges).toHaveLength(2)
    expect(badges.map(b => b.getAttribute('title'))).toContain('Beta')
    expect(badges.map(b => b.getAttribute('title'))).toContain('Alpha')

    // Creation is gated to the active scope.
    expect(container.querySelector('.scheduled-new-task')).toBeNull()
  })

  it('foreign rows route toggle and update callbacks with their origin project', async () => {
    projectMocks.wikiListTimers.mockImplementation(async (_c: unknown, opts?: { target?: string }) =>
      opts?.target === 'proj-2'
        ? { Timers: [mkTimer('scheduler:foreign', true)] }
        : { Timers: [] })
    projectMocks.wikiListCards.mockResolvedValue({
      Cards: [mkServerCard('scheduler:foreign', { schedule: { cron: '0 8 * * *' } })],
      Total: 1,
    })
    const onUpdateScheduleCron = vi.fn().mockResolvedValue(true)
    renderView(
      [],
      'proj-1',
      { projects: scopeProjects, onUpdateScheduleCron },
    )
    await flush()
    await openScopeMenuAndPick(1)

    // Select the foreign row.
    const row = [...container.querySelectorAll('.scheduled-item')]
      .find(el => el.getAttribute('data-timer-id') === 'scheduler:foreign')!
    await act(async () => { fireEvent.click(row) })
    await flush()

    // Foreign detail shows the project badge.
    expect(container.querySelector('.scheduled-detail-project')!.textContent).toContain('Beta')

    // Toggling routes the timer call to the foreign project's actor.
    const toggleBtn = container.querySelector<HTMLButtonElement>('.scheduled-detail-actions .scheduled-icon-btn')!
    await act(async () => { fireEvent.click(toggleBtn) })
    await flush()
    expect(projectMocks.wikiToggleTimer).toHaveBeenCalledWith(
      {}, { Id: 'scheduler:foreign', Enabled: false }, { target: 'proj-2' },
    )

    // Cron save carries the foreign projectId; active rows keep the 2-arg shape.
    const timeInput = container.querySelector<HTMLInputElement>('.scheduled-schedule-editor input[type="time"]')!
    await act(async () => { fireEvent.change(timeInput, { target: { value: '07:30' } }) })
    const applyBtn = container.querySelector<HTMLButtonElement>('.scheduled-schedule-editor .scheduled-editor-btn.primary')!
    await act(async () => { fireEvent.click(applyBtn) })
    await flush()
    expect(onUpdateScheduleCron).toHaveBeenCalledWith('scheduler:foreign', '30 7 * * *', 'proj-2')
  })

  it('pinned project scope loads only that project and persists the choice', async () => {
    renderView([], 'proj-1', { projects: scopeProjects })
    await flush()

    // items[3] = Beta (pinned foreign project entry).
    await openScopeMenuAndPick(3)
    expect(projectMocks.wikiListTimers).toHaveBeenLastCalledWith({}, { target: 'proj-2' })
    expect(prefMocks.savePreference).toHaveBeenCalledWith('scheduled.scope.v1', 'proj-2', 'scheduled-scope', { v: 0 })
  })

  it('restores a persisted scope on mount', async () => {
    prefMocks.loadPreference.mockResolvedValue('global')
    projectMocks.wikiListTimers.mockResolvedValue({ Timers: [] })
    renderView([], 'proj-1', { projects: scopeProjects })
    await flush()
    await flush()

    const targets = projectMocks.wikiListTimers.mock.calls.map(c => c[1])
    expect(targets).toContainEqual({ target: 'proj-1' })
    expect(targets).toContainEqual({ target: 'proj-2' })
  })
})
