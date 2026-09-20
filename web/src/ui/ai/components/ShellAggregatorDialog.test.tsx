import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ShellAggregatorDialog } from './ShellAggregatorDialog'
import { I18nProvider } from '../../../i18n'
import * as aimanagerAggregator from '../../../gen-clients/aimanager/client'
import type { Provider } from '../../../gen-clients/system/types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// The dialog passes the shared client as the first positional arg to the
// generated callables; since we mock the callables themselves, a plain stub is
// enough.
vi.mock('../../../application/generated-client', () => ({ client: {} }))

const aggregatorConfig = vi.hoisted(() => ({
  get: { Name: 'Custom', Strategy: 'smart', Units: [] as unknown[] },
  configs: {} as Record<string, { Name?: string; Strategy?: string; Units?: unknown[] }>,
  items: [] as { Id: string; Name: string; ActorId: string }[],
}))

vi.mock('../../../gen-clients/aimanager/client', () => ({
  aggregatorGet: vi.fn(async (_client: unknown, req: { Id: string }) => {
    const cfg = aggregatorConfig.configs[req.Id]
    return cfg !== undefined ? { ...cfg } : { ...aggregatorConfig.get }
  }),
  aggregatorConfigure: vi.fn(async () => ({})),
  aggregatorList: vi.fn(async () => ({ Items: aggregatorConfig.items })),
  providerResetHealth: vi.fn(async () => ({ Ok: true, Error: '' })),
}))

const providers: Provider[] = [
  {
    Name: 'openai',
    Kind: 'openai',
    Endpoint: 'https://api.openai.com',
    Models: [{ Name: 'gpt-4o' }],
  } as Provider,
]

const multiProviders: Provider[] = [
  {
    Name: 'openai',
    Kind: 'openai',
    Endpoint: 'https://api.openai.com',
    Models: [{ Name: 'm-a' }, { Name: 'm-b' }, { Name: 'm-c' }],
  } as Provider,
]

const changeSelect = async (select: HTMLSelectElement, value: string) => {
  const descriptor = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')
  descriptor?.set?.call(select, value)
  await act(async () => {
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

describe('ShellAggregatorDialog strategy options', () => {
  let container: HTMLDivElement
  let root: Root

  const renderDialog = async (props: Partial<React.ComponentProps<typeof ShellAggregatorDialog>> = {}) => {
    const onSaved = props.onSaved ?? vi.fn()
    const onClose = props.onClose ?? vi.fn()
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <ShellAggregatorDialog
            open
            aggregatorId={null}
            providers={providers}
            onSaved={onSaved}
            onClose={onClose}
            {...props}
          />
        </I18nProvider>,
      )
    })
    return { onSaved, onClose }
  }

  // The strategy <select> is the only sad-select that carries a round_robin option.
  const getStrategySelect = () =>
    Array.from(container.querySelectorAll<HTMLSelectElement>('select.sad-select')).find(s =>
      Array.from(s.options).some(o => o.value === 'round_robin'),
    ) as HTMLSelectElement

  const getProviderSelect = () =>
    Array.from(container.querySelectorAll<HTMLSelectElement>('select.sad-select')).find(s =>
      Array.from(s.options).some(o => o.value === 'openai'),
    ) as HTMLSelectElement

  const getModelSelect = () =>
    Array.from(container.querySelectorAll<HTMLSelectElement>('select.sad-select')).find(s =>
      Array.from(s.options).some(o => o.value === 'gpt-4o'),
    ) as HTMLSelectElement

  const getAddButton = () =>
    Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b =>
      b.classList.contains('sad-add-btn'),
    ) as HTMLButtonElement

  beforeEach(() => {
    aggregatorConfig.get = { Name: 'Custom', Strategy: 'smart', Units: [] }
    aggregatorConfig.configs = {}
    aggregatorConfig.items = []
    vi.mocked(aimanagerAggregator.aggregatorGet).mockClear()
    vi.mocked(aimanagerAggregator.aggregatorConfigure).mockClear()
    vi.mocked(aimanagerAggregator.aggregatorList).mockClear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    // clearAllMocks preserves the vi.mock factory implementations.
    vi.clearAllMocks()
  })

  it('exposes fallback, round_robin, smart, and standard in priority order', async () => {
    await renderDialog()

    const select = getStrategySelect()
    const values = Array.from(select.options).map(o => o.value)
    expect(values).toEqual(['fallback', 'round_robin', 'smart', 'standard'])
  })

  it('defaults strategy to round_robin in create mode', async () => {
    await renderDialog()

    expect(getStrategySelect().value).toBe('round_robin')
  })

  it('reflects the fetched smart strategy in edit mode', async () => {
    aggregatorConfig.get = { Name: 'Custom', Strategy: 'smart', Units: [] }
    await renderDialog({ aggregatorId: 'agg-1' })

    // Wait for the async aggregatorGet to settle, then the select mirrors smart.
    await act(async () => {})
    expect(getStrategySelect().value).toBe('smart')
  })

  it('saves with the selected smart strategy', async () => {
    const { onClose } = await renderDialog()

    // Add a unit so the save button is enabled.
    await changeSelect(getProviderSelect(), 'openai')
    await changeSelect(getModelSelect(), 'gpt-4o')
    await act(async () => {
      getAddButton().click()
    })
    // Select smart.
    await changeSelect(getStrategySelect(), 'smart')

    const saveButton = container.querySelector('button.modal-action--primary') as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    expect(aimanagerAggregator.aggregatorConfigure).toHaveBeenCalledTimes(1)
    const cmd = vi.mocked(aimanagerAggregator.aggregatorConfigure).mock.calls[0]![1] as { Strategy: string }
    expect(cmd.Strategy).toBe('smart')
    expect(onClose).toHaveBeenCalled()
  })

  it('saves with the selected standard strategy', async () => {
    const { onClose } = await renderDialog()

    // Add a unit so the save button is enabled.
    await changeSelect(getProviderSelect(), 'openai')
    await changeSelect(getModelSelect(), 'gpt-4o')
    await act(async () => {
      getAddButton().click()
    })
    await changeSelect(getStrategySelect(), 'standard')

    const saveButton = container.querySelector('button.modal-action--primary') as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    expect(aimanagerAggregator.aggregatorConfigure).toHaveBeenCalledTimes(1)
    const cmd = vi.mocked(aimanagerAggregator.aggregatorConfigure).mock.calls[0]![1] as { Strategy: string }
    expect(cmd.Strategy).toBe('standard')
    expect(onClose).toHaveBeenCalled()
  })
})

describe('ShellAggregatorDialog unit ordering', () => {
  let container: HTMLDivElement
  let root: Root

  const seededUnits = [
    { Model: 'm-a', Endpoint: 'https://api.openai.com', ProviderName: 'openai', Protocol: 'openai' },
    { Model: 'm-b', Endpoint: 'https://api.openai.com', ProviderName: 'openai', Protocol: 'openai' },
    { Model: 'm-c', Endpoint: 'https://api.openai.com', ProviderName: 'openai', Protocol: 'openai' },
  ]

  const renderEditDialog = async () => {
    aggregatorConfig.get = {
      Name: 'Custom',
      Strategy: 'round_robin',
      Units: seededUnits as unknown as typeof aggregatorConfig.get.Units,
    }
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <ShellAggregatorDialog
            open
            aggregatorId="agg-1"
            providers={multiProviders}
            onSaved={vi.fn()}
            onClose={vi.fn()}
          />
        </I18nProvider>,
      )
    })
    // Wait for the async aggregatorGet to populate the unit list.
    await act(async () => {
      await Promise.resolve()
    })
  }

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
    vi.clearAllMocks()
  })

  const unitLabels = () =>
    Array.from(container.querySelectorAll('.sad-unit-model')).map(el => el.textContent?.trim())

  const clickMove = async (index: number, dir: -1 | 1, shiftKey = false) => {
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('button.sad-unit-move'))
    const button = buttons[index * 2 + (dir === -1 ? 0 : 1)]!
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true, shiftKey }))
    })
  }

  it('moves a unit one step on plain click', async () => {
    await renderEditDialog()
    expect(unitLabels()).toEqual(['m-a', 'm-b', 'm-c'])

    await clickMove(1, -1)
    expect(unitLabels()).toEqual(['m-b', 'm-a', 'm-c'])
  })

  it('moves the last unit to top with shift+up', async () => {
    await renderEditDialog()
    expect(unitLabels()).toEqual(['m-a', 'm-b', 'm-c'])

    await clickMove(2, -1, true)
    expect(unitLabels()).toEqual(['m-c', 'm-a', 'm-b'])
  })

  it('moves the first unit to bottom with shift+down', async () => {
    await renderEditDialog()
    expect(unitLabels()).toEqual(['m-a', 'm-b', 'm-c'])

    await clickMove(0, 1, true)
    expect(unitLabels()).toEqual(['m-b', 'm-c', 'm-a'])
  })

  it('keeps the boundary arrows enabled for shift jumps', async () => {
    await renderEditDialog()
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('button.sad-unit-move'))
    expect(buttons[0]!.disabled).toBe(false)
    expect(buttons[buttons.length - 1]!.disabled).toBe(false)
  })
})

describe('ShellAggregatorDialog health badges', () => {
  let container: HTMLDivElement
  let root: Root

  const buildHealthUnits = () => [
    {
      Model: 'm-cooling',
      Endpoint: 'https://api.openai.com',
      ProviderName: 'openai',
      Protocol: 'openai',
      HealthState: 'cooling_down',
      HealthReason: 'rate_limit',
      CooldownUntil: Math.floor(Date.now() / 1000) + 120,
    },
    {
      Model: 'm-disabled',
      Endpoint: 'https://api.openai.com',
      ProviderName: 'openai',
      Protocol: 'openai',
      HealthState: 'disabled',
      HealthReason: 'quota_exhausted',
      RecoveryMode: 'manual_or_balance_refresh',
    },
    {
      Model: 'm-healthy',
      Endpoint: 'https://api.openai.com',
      ProviderName: 'openai',
      Protocol: 'openai',
      HealthState: 'healthy',
    },
  ]

  const renderHealthDialog = async () => {
    aggregatorConfig.get = {
      Name: 'Custom',
      Strategy: 'round_robin',
      Units: buildHealthUnits() as unknown as typeof aggregatorConfig.get.Units,
    }
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <ShellAggregatorDialog
            open
            aggregatorId="agg-health"
            providers={multiProviders}
            onSaved={vi.fn()}
            onClose={vi.fn()}
          />
        </I18nProvider>,
      )
    })
    // Wait for the async aggregatorGet to populate the unit list.
    await act(async () => {
      await Promise.resolve()
    })
  }

  const unitRow = (model: string) => {
    const label = Array.from(container.querySelectorAll<HTMLElement>('.sad-unit-model'))
      .find(el => el.textContent === model)
    return label?.closest('.sad-unit') as HTMLElement | null
  }

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
    vi.clearAllMocks()
  })

  it('shows a cooldown badge with countdown for cooling_down units', async () => {
    await renderHealthDialog()
    const row = unitRow('m-cooling')!
    const badge = row.querySelector('.sad-unit-badge--cooling')
    expect(badge).not.toBeNull()
    expect(badge!.textContent).toMatch(/^\d{2}:\d{2}$/)
    expect(row.className).toContain('is-cooldown')
  })

  it('shows a disabled badge with reason and recovery description for disabled units', async () => {
    await renderHealthDialog()
    const row = unitRow('m-disabled')!
    const badge = row.querySelector('.sad-unit-badge--disabled')
    expect(badge).not.toBeNull()
    expect(badge!.textContent).toContain('Quota exhausted')
    expect(badge!.getAttribute('title')).toContain('Manual recovery or balance refresh required')
    expect(row.className).toContain('is-disabled')
  })

  it('does not render a health badge for healthy units', async () => {
    await renderHealthDialog()
    const row = unitRow('m-healthy')!
    expect(row.querySelector('.sad-unit-badge')).toBeNull()
    expect(row.className).not.toContain('is-cooldown')
    expect(row.className).not.toContain('is-disabled')
  })
})

describe('ShellAggregatorDialog aggregator references', () => {
  let container: HTMLDivElement
  let root: Root

  const refUnit = (aggregatorID: string) => ({
    Model: '', Endpoint: '', ProviderName: '', Protocol: '', aggregatorID,
  })
  const concreteUnit = (model: string) => ({
    Model: model, Endpoint: 'https://api.openai.com', ProviderName: 'openai', Protocol: 'openai',
  })

  // Graph loading chains aggregatorList → per-item aggregatorGet → edges with
  // several promise hops, so flush a few microtask rounds after rendering.
  const flush = async () => {
    for (let i = 0; i < 5; i++) {
      await act(async () => { await Promise.resolve() })
    }
  }

  const renderRefDialog = async (
    props: Partial<React.ComponentProps<typeof ShellAggregatorDialog>> = {},
  ) => {
    const onSaved = props.onSaved ?? vi.fn()
    const onClose = props.onClose ?? vi.fn()
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <ShellAggregatorDialog
            open
            aggregatorId={null}
            providers={multiProviders}
            onSaved={onSaved}
            onClose={onClose}
            {...props}
          />
        </I18nProvider>,
      )
    })
    await flush()
    return { onSaved, onClose }
  }

  const getRefSelect = () =>
    Array.from(container.querySelectorAll<HTMLSelectElement>('select.sad-select')).find(s =>
      Array.from(s.options).some(o => o.value.startsWith('agg-')),
    ) as HTMLSelectElement

  const getRefAddButton = () =>
    Array.from(container.querySelectorAll<HTMLButtonElement>('button.sad-add-btn'))[1] as HTMLButtonElement

  const saveButton = () =>
    container.querySelector('button.modal-action--primary') as HTMLButtonElement

  beforeEach(() => {
    aggregatorConfig.get = { Name: 'Custom', Strategy: 'smart', Units: [] }
    aggregatorConfig.configs = {}
    aggregatorConfig.items = []
    vi.mocked(aimanagerAggregator.aggregatorGet).mockClear()
    vi.mocked(aimanagerAggregator.aggregatorConfigure).mockClear()
    vi.mocked(aimanagerAggregator.aggregatorList).mockClear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('adds an aggregator reference and saves the reference-only payload', async () => {
    aggregatorConfig.items = [
      { Id: 'agg-child-a', Name: 'Child A', ActorId: 'a2' },
      { Id: 'agg-other', Name: 'Other Pool', ActorId: 'a4' },
    ]
    await renderRefDialog()

    const refSelect = getRefSelect()
    expect(Array.from(refSelect.options).map(o => o.value)).toEqual(['', 'agg-child-a', 'agg-other'])
    await changeSelect(refSelect, 'agg-other')
    await act(async () => {
      getRefAddButton().click()
    })

    // The reference renders as a dedicated row with the referenced name.
    const refRow = container.querySelector('.sad-unit--ref') as HTMLElement
    expect(refRow).not.toBeNull()
    expect(refRow.textContent).toContain('Other Pool')
    expect(refRow.textContent).toContain('Reference')

    await act(async () => {
      saveButton().click()
    })
    expect(aimanagerAggregator.aggregatorConfigure).toHaveBeenCalledTimes(1)
    const cmd = vi.mocked(aimanagerAggregator.aggregatorConfigure).mock.calls[0]![1] as { Units: unknown[] }
    expect(cmd.Units).toEqual([
      { Model: '', Endpoint: '', ProviderName: '', Protocol: '', aggregatorID: 'agg-other' },
    ])
  })

  it('renders stored reference rows distinctly from concrete unit rows', async () => {
    aggregatorConfig.items = [
      { Id: 'agg-self', Name: 'Self Pool', ActorId: 'a1' },
      { Id: 'agg-child-a', Name: 'Child A', ActorId: 'a2' },
    ]
    aggregatorConfig.configs['agg-self'] = {
      Name: 'Self Pool',
      Strategy: 'round_robin',
      Units: [refUnit('agg-child-a'), concreteUnit('m-a')],
    }
    await renderRefDialog({ aggregatorId: 'agg-self' })

    const refRows = container.querySelectorAll('.sad-unit--ref')
    expect(refRows.length).toBe(1)
    expect(refRows[0]!.textContent).toContain('Child A')
    expect(refRows[0]!.textContent).toContain('Reference')

    // The concrete unit keeps its normal row form (no reference badge).
    const unitRow = Array.from(container.querySelectorAll<HTMLElement>('.sad-unit'))
      .find(r => r.classList.contains('sad-unit') && !r.classList.contains('sad-unit--ref'))!
    expect(unitRow.querySelector('.sad-unit-badge--ref')).toBeNull()
    expect(unitRow.textContent).toContain('m-a')
  })

  it('excludes self, already-referenced, and descendant aggregators from the selector', async () => {
    aggregatorConfig.items = [
      { Id: 'agg-self', Name: 'Self Pool', ActorId: 'a1' },
      { Id: 'agg-child-a', Name: 'Child A', ActorId: 'a2' },
      { Id: 'agg-grandchild', Name: 'Grandchild', ActorId: 'a3' },
      { Id: 'agg-other', Name: 'Other Pool', ActorId: 'a4' },
    ]
    // agg-self references agg-child-a (referenced); agg-child-a references
    // agg-grandchild (so grandchild is a descendant of agg-self).
    aggregatorConfig.configs['agg-self'] = {
      Name: 'Self Pool',
      Strategy: 'round_robin',
      Units: [refUnit('agg-child-a'), concreteUnit('m-a')],
    }
    aggregatorConfig.configs['agg-child-a'] = {
      Name: 'Child A',
      Strategy: 'round_robin',
      Units: [refUnit('agg-grandchild')],
    }
    await renderRefDialog({ aggregatorId: 'agg-self' })

    const refSelect = getRefSelect()
    expect(Array.from(refSelect.options).map(o => o.value)).toEqual(['', 'agg-other'])
  })

  it('removes a reference row alongside its shared row controls', async () => {
    aggregatorConfig.items = [
      { Id: 'agg-self', Name: 'Self Pool', ActorId: 'a1' },
      { Id: 'agg-child-a', Name: 'Child A', ActorId: 'a2' },
      { Id: 'agg-other', Name: 'Other Pool', ActorId: 'a4' },
    ]
    aggregatorConfig.configs['agg-self'] = {
      Name: 'Self Pool',
      Strategy: 'round_robin',
      Units: [refUnit('agg-child-a'), concreteUnit('m-a')],
    }
    const { onClose } = await renderRefDialog({ aggregatorId: 'agg-self' })

    const refRemove = container.querySelector('.sad-unit--ref .sad-unit-remove') as HTMLButtonElement
    await act(async () => {
      refRemove.click()
    })
    expect(container.querySelectorAll('.sad-unit--ref').length).toBe(0)
    expect(container.querySelectorAll('.sad-unit').length).toBe(1)

    await act(async () => {
      saveButton().click()
    })
    const cmd = vi.mocked(aimanagerAggregator.aggregatorConfigure).mock.calls[0]![1] as { Units: unknown[] }
    expect(cmd.Units).toEqual([concreteUnit('m-a')])
    expect(onClose).toHaveBeenCalled()
  })
})

describe('ShellAggregatorDialog unit health reset', () => {
  let container: HTMLDivElement
  let root: Root

  const renderDialog = async (
    props: Partial<React.ComponentProps<typeof ShellAggregatorDialog>> = {},
  ) => {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <ShellAggregatorDialog
            open
            aggregatorId="agg-1"
            providers={[]}
            onSaved={vi.fn()}
            onClose={vi.fn()}
            {...props}
          />
        </I18nProvider>,
      )
    })
    await act(async () => {})
  }

  const resetBtn = (model: string) =>
    container.querySelector(`[data-testid="aggregator-unit-reset-health-btn-openai-${model}"]`) as HTMLButtonElement

  beforeEach(() => {
    aggregatorConfig.get = { Name: 'Custom', Strategy: 'smart', Units: [] }
    aggregatorConfig.configs = {}
    aggregatorConfig.items = []
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('shows reset buttons only on unhealthy unit rows and clears them after reset', async () => {
    aggregatorConfig.configs['agg-1'] = {
      Name: 'Custom',
      Strategy: 'round_robin',
      Units: [
        {
          Model: 'm-a', Endpoint: 'https://api.openai.com', ProviderName: 'openai', Protocol: 'openai',
          HealthState: 'disabled', HealthReason: 'rate_limit', RecoveryMode: 'manual_or_balance_refresh',
        },
        {
          Model: 'm-b', Endpoint: 'https://api.openai.com', ProviderName: 'openai', Protocol: 'openai',
          HealthState: 'cooling_down', CooldownUntil: Math.floor(Date.now() / 1000) + 300,
        },
        {
          Model: 'm-c', Endpoint: 'https://api.openai.com', ProviderName: 'openai', Protocol: 'openai',
        },
      ],
    }
    await renderDialog()

    expect(container.querySelector('.sad-unit-badge--disabled')).not.toBeNull()
    expect(container.querySelector('.sad-unit-badge--cooling')).not.toBeNull()
    expect(resetBtn('m-a')).not.toBeNull()
    expect(resetBtn('m-b')).not.toBeNull()
    expect(resetBtn('m-c')).toBeNull()

    await act(async () => {
      resetBtn('m-a').click()
    })
    expect(aimanagerAggregator.providerResetHealth).toHaveBeenCalledTimes(1)
    expect(vi.mocked(aimanagerAggregator.providerResetHealth).mock.calls[0]![1]).toEqual({ ProviderName: 'openai' })

    // The reset is provider-scoped: both unhealthy rows clear, badges vanish.
    expect(container.querySelector('.sad-unit-badge--disabled')).toBeNull()
    expect(container.querySelector('.sad-unit-badge--cooling')).toBeNull()
    expect(resetBtn('m-a')).toBeNull()
    expect(resetBtn('m-b')).toBeNull()
  })

  it('surfaces the backend error without clearing badges', async () => {
    vi.mocked(aimanagerAggregator.providerResetHealth).mockResolvedValueOnce({ Ok: false, Error: 'boom' })
    aggregatorConfig.configs['agg-1'] = {
      Name: 'Custom',
      Strategy: 'round_robin',
      Units: [
        {
          Model: 'm-a', Endpoint: 'https://api.openai.com', ProviderName: 'openai', Protocol: 'openai',
          HealthState: 'disabled', HealthReason: 'rate_limit',
        },
      ],
    }
    await renderDialog()

    await act(async () => {
      resetBtn('m-a').click()
    })
    expect(container.querySelector('.sad-unit-badge--disabled')).not.toBeNull()
    expect(container.querySelector('.sad-error')?.textContent).toContain('boom')
  })
})
