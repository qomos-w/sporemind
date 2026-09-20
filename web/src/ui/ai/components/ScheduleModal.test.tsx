import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { MonoCardListItem } from '../../../domain/mono-types'
import type { AgentKindInfo, AICallableUnitView } from '../../../gen-clients/system/types'
import type { AggregatorDescriptor } from '../../../gen-types/aigen'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const workspaceMocks = vi.hoisted(() => ({
  listAgentKinds: vi.fn(),
}))

const aiaggregatorMocks = vi.hoisted(() => ({
  status: vi.fn(),
}))

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/workspace/client', () => workspaceMocks)
vi.mock('../../../gen-clients/aiaggregator/client', () => aiaggregatorMocks)
vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('../browserOverlay', () => ({ useBrowserOverlay: () => {} }))

import { ScheduleModal, type SchedulerAgentSelection } from './ScheduleModal'
import type { ScheduleDraft } from './scheduledTasks'

function mkCard(overrides: Partial<MonoCardListItem> = {}): MonoCardListItem {
  return {
    id: 'sched:test',
    type: 'scheduler',
    tags: [],
    list: [],
    created: '',
    modified: '',
    data: {},
    ...overrides,
  } as MonoCardListItem
}

const defaultKinds: AgentKindInfo[] = [
  { Kind: 'coder', DisplayName: 'Coder', UserCreatable: true, SystemManaged: false, Builtin: true },
  { Kind: 'reviewer', DisplayName: 'Reviewer', UserCreatable: true, SystemManaged: false, Builtin: true },
]

const defaultUnits: AICallableUnitView[] = [
  { Model: 'gpt-5', ProviderName: 'openai', Id: 'unit-gpt5', Endpoint: '', Protocol: '' },
  { Model: 'claude-opus', ProviderName: 'anthropic', Id: 'unit-opus', Endpoint: '', Protocol: '' },
]

const systemAgg: AggregatorDescriptor = { Id: 'system', Name: 'System', ActorId: 'actor:system' }
const customAgg: AggregatorDescriptor = { Id: 'custom-1', Name: 'Custom', ActorId: 'actor:custom-1' }

let container: HTMLDivElement
let root: Root

async function selectOption(triggerGuideId: string, itemGuideId: string) {
  const trigger = document.querySelector(`[data-guide-id="${triggerGuideId}"]`) as HTMLElement | null
  expect(trigger).not.toBeNull()
  await act(async () => { await userEvent.click(trigger!) })
  const item = document.querySelector(`[data-guide-id="${itemGuideId}"]`) as HTMLElement | null
  expect(item).not.toBeNull()
  await act(async () => { await userEvent.click(item!) })
}

describe('ScheduleModal', () => {
  beforeEach(() => {
    workspaceMocks.listAgentKinds.mockResolvedValue({ Items: defaultKinds })
    aiaggregatorMocks.status.mockResolvedValue({ Units: defaultUnits })
  })

  afterEach(async () => {
    vi.clearAllMocks()
    await act(async () => { root.unmount() })
    container.remove()
  })

  function renderModal(props: {
    card?: MonoCardListItem | null
    draft?: ScheduleDraft | null
    onApply?: (agent: SchedulerAgentSelection) => void
    onClose?: () => void
    aggregators?: AggregatorDescriptor[]
  } = {}) {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    const onApply = props.onApply ?? vi.fn()
    const onClose = props.onClose ?? vi.fn()
    const onDraftChange = vi.fn()
    const draft = props.draft ?? { kind: 'everyDay', time: '09:00' } as ScheduleDraft
    act(() => {
      root.render(
        <ScheduleModal
          card={props.card ?? mkCard()}
          draft={draft}
          onDraftChange={onDraftChange}
          onApply={onApply}
          onClose={onClose}
          aggregators={props.aggregators ?? [systemAgg, customAgg]}
        />,
      )
    })
  }

  async function flush() {
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
  }

  it('renders a shadcn Select for agent kind', async () => {
    renderModal()
    await flush()
    expect(container.querySelector('[data-slot="select-trigger"]')).toBeTruthy()
  })

  it('lists all agent kinds from the workspace registry', async () => {
    renderModal()
    await flush()
    await selectOption('schedule-kind-trigger', 'schedule-kind-coder')
    expect(container.querySelector('[data-slot="select-value"]')?.textContent).toBe('Coder')
  })

  it('shows the unified model selector after a kind is chosen', async () => {
    renderModal()
    await flush()
    expect(container.querySelector('[data-guide-id="schedule-model-trigger"]')).toBeNull()
    await selectOption('schedule-kind-trigger', 'schedule-kind-coder')
    expect(container.querySelector('[data-guide-id="schedule-model-trigger"]')).toBeTruthy()
  })

  it('does not render an existing-agent / bound-agent option', async () => {
    renderModal()
    await flush()
    await selectOption('schedule-kind-trigger', 'schedule-kind-coder')
    const modelTrigger = container.querySelector('[data-guide-id="schedule-model-trigger"]') as HTMLElement | null
    expect(modelTrigger).not.toBeNull()
    await act(async () => { await userEvent.click(modelTrigger!) })
    const items = document.querySelectorAll('[data-slot="select-item"]')
    const texts = Array.from(items).map(i => i.textContent)
    expect(texts).not.toContain('scheduled.mode.boundAgent')
  })

  it('applies create-new selection with a pinned unit', async () => {
    const onApply = vi.fn()
    renderModal({ onApply })
    await flush()

    await selectOption('schedule-kind-trigger', 'schedule-kind-coder')
    await selectOption('schedule-model-trigger', 'schedule-model-openai::gpt-5')

    const save = container.querySelector<HTMLButtonElement>('.scheduled-editor-btn.primary')!
    await act(async () => { fireEvent.click(save) })

    expect(onApply).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'create',
      kind: 'coder',
      selection: {
        type: 'unit',
        unit: { model: 'gpt-5', provider: 'openai' },
      },
    }))
  })

  it('applies create-new selection with a custom aggregator', async () => {
    const onApply = vi.fn()
    renderModal({ onApply })
    await flush()

    await selectOption('schedule-kind-trigger', 'schedule-kind-coder')
    await selectOption('schedule-model-trigger', 'schedule-model-agg::custom-1')

    const save = container.querySelector<HTMLButtonElement>('.scheduled-editor-btn.primary')!
    await act(async () => { fireEvent.click(save) })

    expect(onApply).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'create',
      kind: 'coder',
      selection: {
        type: 'aggregator',
        aggregatorId: 'custom-1',
      },
    }))
  })

  it('seeds selection from card agent_kind and model_slots', async () => {
    const onApply = vi.fn()
    const card = mkCard({
      data: {
        agent_kind: 'reviewer',
        model_slots: JSON.stringify({
          primary: {
            Candidates: [{ kind: 'unit', Unit: { model: 'claude-opus', provider: 'anthropic' } }],
          },
        }),
      },
    })
    renderModal({ card, onApply })
    await flush()

    expect(container.querySelector('[data-slot="select-value"]')?.textContent).toBe('Reviewer')

    const save = container.querySelector<HTMLButtonElement>('.scheduled-editor-btn.primary')!
    await act(async () => { fireEvent.click(save) })

    expect(onApply).toHaveBeenCalledWith({
      mode: 'create',
      kind: 'reviewer',
      selection: {
        type: 'unit',
        unit: { model: 'claude-opus', provider: 'anthropic' },
      },
    })
  })

  it('ignores a stale bound_agent and seeds unset mode', async () => {
    const onApply = vi.fn()
    const card = mkCard({ data: { bound_agent: 'agent:legacy' } })
    renderModal({ card, onApply })
    await flush()

    const save = container.querySelector<HTMLButtonElement>('.scheduled-editor-btn.primary')!
    await act(async () => { fireEvent.click(save) })

    expect(onApply).toHaveBeenCalledWith({ mode: 'unset' })
  })
})
