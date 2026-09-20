import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ShellProviderSettings } from './ShellProviderSettings'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  configure: vi.fn().mockResolvedValue([]),
  reload: vi.fn().mockResolvedValue([]),
  remove: vi.fn().mockResolvedValue([]),
  exportConfig: vi.fn().mockResolvedValue({ Data: '{"providers":[]}' }),
  importConfig: vi.fn().mockResolvedValue({}),
  providerResetHealth: vi.fn().mockResolvedValue({ Ok: true, Error: '' }),
  providerSetDisabled: vi.fn().mockResolvedValue({ Ok: true, Error: '' }),
  providerRecordProbe: vi.fn().mockResolvedValue({ Ok: true, Error: '' }),
  providerFetchModels: vi.fn(),
  aggregatorList: vi.fn(),
  aggregatorSetDisabled: vi.fn().mockResolvedValue({ Ok: true, Error: '' }),
  probeTokens: vi.fn(),
  modelDefaultsGet: vi.fn().mockResolvedValue({ Items: [] }),
  modelDefaultsSet: vi.fn().mockResolvedValue({ Ok: true, Error: '' }),
  providers: [] as any[],
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../gen-clients/aimanager/client', () => ({
  configExport: hoisted.exportConfig,
  configImport: hoisted.importConfig,
  providerResetHealth: hoisted.providerResetHealth,
  providerSetDisabled: hoisted.providerSetDisabled,
  providerRecordProbe: hoisted.providerRecordProbe,
  providerFetchModels: hoisted.providerFetchModels,
  aggregatorList: hoisted.aggregatorList,
  aggregatorSetDisabled: hoisted.aggregatorSetDisabled,
  aggregatorGet: vi.fn().mockResolvedValue({ Units: [] }),
  aggregatorConfigure: vi.fn().mockResolvedValue({}),
  modelDefaultsGet: hoisted.modelDefaultsGet,
  modelDefaultsSet: hoisted.modelDefaultsSet,
}))

vi.mock('../../../gen-clients/aiaggregator/client', () => ({
  status: vi.fn().mockResolvedValue({ Units: [] }),
  probeTokens: hoisted.probeTokens,
}))

vi.mock('../hooks/useProviderConfigs', () => ({
  useProviderConfigs: () => ({
    providers: hoisted.providers,
    loading: false,
    error: null,
    reload: hoisted.reload,
    configure: hoisted.configure,
    remove: hoisted.remove,
  }),
}))

vi.mock('./ShellAggregatorDialog', () => ({
  ShellAggregatorDialog: () => null,
}))

const ADD_PROVIDER_KEY = 'settings.provider.addProvider'
const ADD_MODEL_KEY = 'settings.provider.dialog.addModel'
const SAVE_KEY = 'settings.provider.dialog.save'

describe('ShellProviderSettings manual model editing', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.aggregatorList.mockResolvedValue({ Items: [] })
    hoisted.probeTokens.mockResolvedValue({ Usage: { InputTokens: 1 } })
    hoisted.providerFetchModels.mockResolvedValue({ Models: [] })
    hoisted.providers = []
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  const renderAndOpenDialog = async () => {
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })
    const addButton = container.querySelector('[data-testid="provider-add-btn"]') as HTMLButtonElement | null
      ?? Array.from(container.querySelectorAll('button')).find(b => b.textContent === ADD_PROVIDER_KEY)
    if (!addButton) {
      // eslint-disable-next-line no-console
      console.log('container HTML:', container.innerHTML)
      throw new Error('Add provider button not found')
    }
    await act(async () => {
      addButton.click()
    })
  }

  const findModelRows = () => container.querySelectorAll('.shell-provider-model-row')

  const findAddModelButton = () =>
    Array.from(container.querySelectorAll('button')).find(b => b.textContent === ADD_MODEL_KEY)

  const findSaveButton = () =>
    Array.from(container.querySelectorAll('button')).find(b => b.textContent === SAVE_KEY)

  it('renders providers section by default', async () => {
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })

    expect(container.querySelector('[data-testid="provider-models-panel"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="provider-aggregators-panel"]')).toBeNull()
  })

  it('renders aggregators section when section prop is set', async () => {
    await act(async () => {
      root.render(<ShellProviderSettings section="aggregators" />)
    })

    expect(container.querySelector('[data-testid="provider-models-panel"]')).toBeNull()
    expect(container.querySelector('[data-testid="provider-aggregators-panel"]')).not.toBeNull()
  })

  it('adds a new manual model row when the add-model button is clicked', async () => {
    await renderAndOpenDialog()
    const rowsBefore = findModelRows()
    expect(rowsBefore.length).toBeGreaterThan(0)

    const addModelButton = findAddModelButton()
    if (!addModelButton) throw new Error('Add model button not found')
    await act(async () => {
      addModelButton.click()
    })

    const rowsAfter = findModelRows()
    expect(rowsAfter.length).toBe(rowsBefore.length + 1)
  })

  it('removes a model row when the remove button is clicked', async () => {
    await renderAndOpenDialog()
    const rowsBefore = findModelRows()
    expect(rowsBefore.length).toBeGreaterThan(1)

    const firstRow = rowsBefore[0]
    if (!firstRow) throw new Error('First model row not found')
    const firstRemove = firstRow.querySelector('.shell-provider-model-remove') as HTMLButtonElement
    if (!firstRemove) throw new Error('Remove model button not found')
    await act(async () => {
      firstRemove.click()
    })

    const rowsAfter = findModelRows()
    expect(rowsAfter.length).toBe(rowsBefore.length - 1)
  })

  it('persists manual model list changes on save', async () => {
    await renderAndOpenDialog()
    const rowsBefore = findModelRows()
    expect(rowsBefore.length).toBeGreaterThan(1)

    const firstRow = rowsBefore[0]
    if (!firstRow) throw new Error('First model row not found')
    const firstRemove = firstRow.querySelector('.shell-provider-model-remove') as HTMLButtonElement
    if (!firstRemove) throw new Error('Remove model button not found')
    await act(async () => {
      firstRemove.click()
    })

    const saveButton = findSaveButton()
    if (!saveButton) throw new Error('Save button not found')
    await act(async () => {
      saveButton.click()
    })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.Models).toHaveLength(rowsBefore.length - 1)
    expect(req.Models).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ Name: 'gpt-5.5' }),
      ]),
    )
  })

  it('includes max concurrency in configure request', async () => {
    await renderAndOpenDialog()
    const inputs = container.querySelectorAll('input')
    const concurrencyInput = Array.from(inputs).find(
      input => input.type === 'number' && input.placeholder === 'settings.provider.dialog.maxConcurrencyPlaceholder',
    )
    if (!concurrencyInput) throw new Error('Max concurrency input not found')
    await act(async () => {
      const nativeInputValueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      if (nativeInputValueSetter) {
        nativeInputValueSetter.call(concurrencyInput, '4')
      } else {
        concurrencyInput.value = '4'
      }
      concurrencyInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const saveButton = findSaveButton()
    if (!saveButton) throw new Error('Save button not found')
    await act(async () => {
      saveButton.click()
    })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.MaxConcurrency).toBe(4)
  })

  it('includes proxy in configure request', async () => {
    await renderAndOpenDialog()
    const inputs = container.querySelectorAll('input')
    const proxyInput = Array.from(inputs).find(
      input => input.type === 'text' && input.placeholder === 'settings.provider.dialog.proxyPlaceholder',
    )
    if (!proxyInput) throw new Error('Proxy input not found')
    await act(async () => {
      const nativeInputValueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      if (nativeInputValueSetter) {
        nativeInputValueSetter.call(proxyInput, 'http://127.0.0.1:7890')
      } else {
        proxyInput.value = 'http://127.0.0.1:7890'
      }
      proxyInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const saveButton = findSaveButton()
    if (!saveButton) throw new Error('Save button not found')
    await act(async () => {
      saveButton.click()
    })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.Proxy).toBe('http://127.0.0.1:7890')
  })

  it('exports all model settings as a JSON download', async () => {
    const createObjectURL = vi.fn(() => 'blob:model-settings')
    const revokeObjectURL = vi.fn()
    vi.stubGlobal('URL', { createObjectURL, revokeObjectURL })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

    await act(async () => {
      root.render(<ShellProviderSettings />)
    })
    const exportButton = container.querySelector('[data-testid="provider-export-btn"]') as HTMLButtonElement
    await act(async () => {
      exportButton.click()
    })

    expect(hoisted.exportConfig).toHaveBeenCalledTimes(1)
    expect(createObjectURL).toHaveBeenCalledWith(expect.any(Blob))
    expect(click).toHaveBeenCalledTimes(1)
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:model-settings')
  })

  it('copies a provider into the add dialog with a derived name and cleared API key', async () => {
    hoisted.providers = [
      {
        Name: 'openai-main',
        Kind: 'openai',
        Endpoint: 'https://api.openai.com/v1',
        Models: [
          { Name: 'gpt-5', MaxContextLength: 128000, MaxTokens: 16384, Protocol: 'openai' },
        ],
        HasAuthToken: true,
      },
    ]

    await act(async () => {
      root.render(<ShellProviderSettings />)
    })

    const copyButton = container.querySelector('[data-testid="provider-copy-btn-openai-main"]') as HTMLButtonElement
    if (!copyButton) throw new Error('Copy provider button not found')
    await act(async () => {
      copyButton.click()
    })

    // The dialog should be in add mode (editingName is null) with pre-filled data.
    const inputs = container.querySelectorAll('input')
    const nameInput = Array.from(inputs).find(input => input.value === 'openai-main (copy)')
    if (!nameInput) throw new Error('Copy name not pre-filled in dialog')

    // API key should be empty (cleared) rather than copied.
    const keyInput = Array.from(inputs).find(input => input.type === 'password')
    if (keyInput && keyInput.value !== '') {
      throw new Error('API key should be cleared when copying a provider')
    }

    // Save should create a new provider with the derived name and copied models.
    const saveButton = findSaveButton()
    if (!saveButton) throw new Error('Save button not found')
    await act(async () => {
      saveButton.click()
    })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.Name).toBe('openai-main (copy)')
    expect(req.Kind).toBe('openai')
    expect(req.Endpoint).toBe('https://api.openai.com/v1')
    expect(req.Models).toHaveLength(1)
    expect(req.Models[0]).toMatchObject({ Name: 'gpt-5', Protocol: 'openai' })
  })

  it('adds a disable window and submits it in the configure request', async () => {
    await renderAndOpenDialog()

    const addButton = container.querySelector('[data-testid="provider-disable-window-add"]') as HTMLButtonElement
    expect(addButton).toBeTruthy()
    await act(async () => {
      addButton.click()
    })

    const timeInputs = container.querySelectorAll('.shell-provider-disable-window-input')
    expect(timeInputs).toHaveLength(2)

    const saveButton = findSaveButton()
    if (!saveButton) throw new Error('Save button not found')
    await act(async () => {
      saveButton.click()
    })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.DisableWindows).toEqual([{ Start: '09:00', End: '17:00' }])
  })

  it('removes a disable window row and omits DisableWindows when empty', async () => {
    await renderAndOpenDialog()

    const addButton = container.querySelector('[data-testid="provider-disable-window-add"]') as HTMLButtonElement
    await act(async () => { addButton.click() })
    await act(async () => { addButton.click() })

    expect(container.querySelectorAll('.shell-provider-disable-window-row')).toHaveLength(2)

    const removeButton = container.querySelector('[data-testid="provider-disable-window-remove-0"]') as HTMLButtonElement
    await act(async () => { removeButton.click() })

    expect(container.querySelectorAll('.shell-provider-disable-window-row')).toHaveLength(1)

    const saveButton = findSaveButton()
    if (!saveButton) throw new Error('Save button not found')
    await act(async () => { saveButton.click() })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.DisableWindows).toHaveLength(1)
  })

  it('blocks save and shows an error when a disable window time is invalid', async () => {
    await renderAndOpenDialog()

    const addButton = container.querySelector('[data-testid="provider-disable-window-add"]') as HTMLButtonElement
    await act(async () => { addButton.click() })

    const startInput = container.querySelector('.shell-provider-disable-window-input') as HTMLInputElement
    const valueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      valueSetter.call(startInput, '25:99')
      startInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const saveButton = findSaveButton()
    if (!saveButton) throw new Error('Save button not found')
    await act(async () => { saveButton.click() })

    expect(hoisted.configure).not.toHaveBeenCalled()
    const errorEl = container.querySelector('[data-testid="provider-disable-window-error"]')
    expect(errorEl).toBeTruthy()
  })

  it('preserves disable windows when copying a provider', async () => {
    hoisted.providers = [
      {
        Name: 'openai-main',
        Kind: 'openai',
        Endpoint: 'https://api.openai.com/v1',
        Models: [{ Name: 'gpt-5', Protocol: 'openai' }],
        HasAuthToken: true,
        DisableWindows: [{ Start: '22:00', End: '06:00' }],
      },
    ]
    await act(async () => { root.render(<ShellProviderSettings />) })

    const copyButton = container.querySelector('[data-testid="provider-copy-btn-openai-main"]') as HTMLButtonElement
    await act(async () => { copyButton.click() })

    const saveButton = findSaveButton()
    if (!saveButton) throw new Error('Save button not found')
    await act(async () => { saveButton.click() })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.DisableWindows).toEqual([{ Start: '22:00', End: '06:00' }])
  })

  it('renders seven square day buttons per disable window row', async () => {
    await renderAndOpenDialog()

    const addButton = container.querySelector('[data-testid="provider-disable-window-add"]') as HTMLButtonElement
    await act(async () => { addButton.click() })

    const buttons = container.querySelectorAll('[data-testid^="provider-disable-window-day-0-"]')
    expect(buttons).toHaveLength(7)
    buttons.forEach(btn => {
      expect((btn as HTMLButtonElement).classList.contains('shell-provider-disable-window-day')).toBe(true)
    })
  })

  it('adds a day to the saved payload when clicked', async () => {
    await renderAndOpenDialog()

    const addButton = container.querySelector('[data-testid="provider-disable-window-add"]') as HTMLButtonElement
    await act(async () => { addButton.click() })

    const day3 = container.querySelector('[data-testid="provider-disable-window-day-0-3"]') as HTMLButtonElement
    await act(async () => { day3.click() })

    const saveButton = findSaveButton()
    if (!saveButton) throw new Error('Save button not found')
    await act(async () => { saveButton.click() })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.DisableWindows).toEqual([{ Start: '09:00', End: '17:00', Days: [3] }])
  })

  it('removes a day from the saved payload when toggled off', async () => {
    await renderAndOpenDialog()

    const addButton = container.querySelector('[data-testid="provider-disable-window-add"]') as HTMLButtonElement
    await act(async () => { addButton.click() })

    const day3 = container.querySelector('[data-testid="provider-disable-window-day-0-3"]') as HTMLButtonElement
    await act(async () => { day3.click() })
    await act(async () => { day3.click() })

    const saveButton = findSaveButton()
    if (!saveButton) throw new Error('Save button not found')
    await act(async () => { saveButton.click() })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.DisableWindows).toEqual([{ Start: '09:00', End: '17:00' }])
  })

  it('omits Days when all seven days are selected', async () => {
    await renderAndOpenDialog()

    const addButton = container.querySelector('[data-testid="provider-disable-window-add"]') as HTMLButtonElement
    await act(async () => { addButton.click() })

    for (let day = 1; day <= 7; day++) {
      const btn = container.querySelector(`[data-testid="provider-disable-window-day-0-${day}"]`) as HTMLButtonElement
      await act(async () => { btn.click() })
    }

    const saveButton = findSaveButton()
    if (!saveButton) throw new Error('Save button not found')
    await act(async () => { saveButton.click() })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.DisableWindows).toEqual([{ Start: '09:00', End: '17:00' }])
  })

  it('keeps existing models when typing an API key in the edit dialog', async () => {
    hoisted.providers = [{
      Name: 'openai-main',
      Kind: 'openai',
      Endpoint: 'https://api.openai.com/v1',
      Models: [{ Name: 'gpt-5', Protocol: 'openai', MaxContextLength: 128000, MaxTokens: 16384 }],
      HasAuthToken: true,
    }]
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })

    const editButton = container.querySelector('[data-testid="provider-edit-btn-openai-main"]') as HTMLButtonElement
    await act(async () => {
      editButton.click()
    })

    const keyInput = container.querySelector('input[type="password"]') as HTMLInputElement
    const valueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      valueSetter.call(keyInput, 'sk-new-key')
      keyInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const saveButton = findSaveButton() as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.Models).toEqual([
      { Name: 'gpt-5', Protocol: 'openai', MaxContextLength: 128000, MaxTokens: 16384 },
    ])
  })

  it('keeps existing models when editing the endpoint in the edit dialog', async () => {
    hoisted.providers = [{
      Name: 'custom-relay',
      Kind: 'openai',
      Endpoint: 'https://relay.example.com/v1',
      Models: [{ Name: 'relay-model', Protocol: 'openai', MaxContextLength: 64000 }],
      HasAuthToken: true,
    }]
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })

    const editButton = container.querySelector('[data-testid="provider-edit-btn-custom-relay"]') as HTMLButtonElement
    await act(async () => {
      editButton.click()
    })

    const endpointInput = Array.from(container.querySelectorAll('input[type="text"]'))
      .find(i => (i as HTMLInputElement).value === 'https://relay.example.com/v1') as HTMLInputElement
    const valueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      valueSetter.call(endpointInput, 'https://relay2.example.com/v1')
      endpointInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const saveButton = findSaveButton() as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.Endpoint).toBe('https://relay2.example.com/v1')
    expect(req.Models).toEqual([
      { Name: 'relay-model', Protocol: 'openai', MaxContextLength: 64000 },
    ])
  })

  it('keeps seeded models when typing an API key in the copy flow', async () => {
    hoisted.providers = [{
      Name: 'custom-relay',
      Kind: 'openai',
      Endpoint: 'https://relay.example.com/v1',
      Models: [{ Name: 'relay-model', Protocol: 'openai', MaxContextLength: 64000 }],
      HasAuthToken: true,
    }]
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })

    const copyButton = container.querySelector('[data-testid="provider-copy-btn-custom-relay"]') as HTMLButtonElement
    await act(async () => {
      copyButton.click()
    })

    const keyInput = container.querySelector('input[type="password"]') as HTMLInputElement
    const valueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      valueSetter.call(keyInput, 'sk-copy-key')
      keyInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const saveButton = findSaveButton() as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.Name).toBe('custom-relay (copy)')
    expect(req.Models).toEqual([
      { Name: 'relay-model', Protocol: 'openai', MaxContextLength: 64000 },
    ])
  })

  it('keeps existing models when fetching returns empty or fails in the edit dialog', async () => {
    hoisted.providers = [{
      Name: 'openai-main',
      Kind: 'openai',
      Endpoint: 'https://api.openai.com/v1',
      Models: [{ Name: 'gpt-5', Protocol: 'openai' }],
      HasAuthToken: true,
    }]
    hoisted.providerFetchModels.mockResolvedValueOnce({ Models: [] })
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })

    const editButton = container.querySelector('[data-testid="provider-edit-btn-openai-main"]') as HTMLButtonElement
    await act(async () => {
      editButton.click()
    })

    const fetchButton = container.querySelector('.shell-provider-fetch-btn') as HTMLButtonElement
    await act(async () => {
      fetchButton.click()
    })
    expect((container.querySelector('.shell-provider-fetch-error') as HTMLElement).textContent)
      .toBe('settings.provider.fetchError.empty')

    hoisted.providerFetchModels.mockRejectedValueOnce(new Error('401 unauthorized'))
    await act(async () => {
      fetchButton.click()
    })
    expect((container.querySelector('.shell-provider-fetch-error') as HTMLElement).textContent)
      .toBe('401 unauthorized')

    const saveButton = findSaveButton() as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0]
    expect(req.Models).toEqual([{ Name: 'gpt-5', Protocol: 'openai' }])
  })

  it('merges fetched models into the existing list in the edit dialog', async () => {
    hoisted.providers = [{
      Name: 'openai-main',
      Kind: 'openai',
      Endpoint: 'https://api.openai.com/v1',
      Models: [
        { Name: 'gpt-5', Protocol: 'openai', MaxContextLength: 128000, MaxTokens: 16384 },
        { Name: 'custom-model', Protocol: 'anthropic' },
      ],
      HasAuthToken: true,
    }]
    hoisted.providerFetchModels.mockResolvedValueOnce({
      Models: [{ Name: 'gpt-5' }, { Name: 'gpt-5-mini' }],
    })
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })

    const editButton = container.querySelector('[data-testid="provider-edit-btn-openai-main"]') as HTMLButtonElement
    await act(async () => {
      editButton.click()
    })

    const fetchButton = container.querySelector('.shell-provider-fetch-btn') as HTMLButtonElement
    await act(async () => {
      fetchButton.click()
    })

    const saveButton = findSaveButton() as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    const req = hoisted.configure.mock.calls[0]![0]
    const names = (req.Models as Array<{ Name: string }>).map(m => m.Name)
    expect(names).toEqual(['gpt-5', 'gpt-5-mini', 'custom-model'])
    const gpt5 = (req.Models as Array<Record<string, unknown>>).find(m => m['Name'] === 'gpt-5')!
    expect(gpt5['Protocol']).toBe('openai')
    expect(gpt5['MaxContextLength']).toBe(128000)
    expect(gpt5['MaxTokens']).toBe(16384)
  })

  it('updates Modality from upstream on re-fetch while keeping user-configured fields', async () => {
    hoisted.providers = [{
      Name: 'openai-main',
      Kind: 'openai',
      Endpoint: 'https://api.openai.com/v1',
      Models: [
        { Name: 'gpt-5', Protocol: 'openai', MaxContextLength: 256000, MaxTokens: 32768, Modality: 'chat' },
      ],
      HasAuthToken: true,
    }]
    hoisted.providerFetchModels.mockResolvedValueOnce({
      Models: [{ Name: 'gpt-5', Modality: 'image', Protocol: 'openai' }],
    })
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })

    const editButton = container.querySelector('[data-testid="provider-edit-btn-openai-main"]') as HTMLButtonElement
    await act(async () => {
      editButton.click()
    })

    const fetchButton = container.querySelector('.shell-provider-fetch-btn') as HTMLButtonElement
    await act(async () => {
      fetchButton.click()
    })

    const saveButton = findSaveButton() as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    const req = hoisted.configure.mock.calls[0]![0]
    const m = (req.Models as Array<Record<string, unknown>>).find((x: any) => x['Name'] === 'gpt-5')!
    // Modality updated from upstream (existing 'chat' is the inferred default).
    expect(m['Modality']).toBe('image')
    // User-configured fields preserved.
    expect(m['MaxContextLength']).toBe(256000)
    expect(m['MaxTokens']).toBe(32768)
    expect(m['Protocol']).toBe('openai')
  })

  it('preserves user-set Modality (image) on re-fetch', async () => {
    hoisted.providers = [{
      Name: 'openai-main',
      Kind: 'openai',
      Endpoint: 'https://api.openai.com/v1',
      Models: [
        { Name: 'gpt-5', Protocol: 'openai', MaxContextLength: 256000, MaxTokens: 32768, Modality: 'image' },
      ],
      HasAuthToken: true,
    }]
    hoisted.providerFetchModels.mockResolvedValueOnce({
      Models: [{ Name: 'gpt-5', Modality: 'chat', Protocol: 'openai' }],
    })
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })

    const editButton = container.querySelector('[data-testid="provider-edit-btn-openai-main"]') as HTMLButtonElement
    await act(async () => {
      editButton.click()
    })

    const fetchButton = container.querySelector('.shell-provider-fetch-btn') as HTMLButtonElement
    await act(async () => {
      fetchButton.click()
    })

    const saveButton = findSaveButton() as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    const req = hoisted.configure.mock.calls[0]![0]
    const m = (req.Models as Array<Record<string, unknown>>).find((x: any) => x['Name'] === 'gpt-5')!
    // Modality preserved (image is not the inferred default 'chat').
    expect(m['Modality']).toBe('image')
  })

  it('updates Protocol from upstream on re-fetch when local equals effective kind', async () => {
    hoisted.providers = [{
      Name: 'openai-main',
      Kind: 'openai',
      Endpoint: 'https://api.openai.com/v1',
      Models: [
        { Name: 'gpt-5', Protocol: 'openai', MaxContextLength: 128000, Modality: 'chat' },
      ],
      HasAuthToken: true,
    }]
    hoisted.providerFetchModels.mockResolvedValueOnce({
      Models: [{ Name: 'gpt-5', Modality: 'chat', Protocol: 'anthropic' }],
    })
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })

    const editButton = container.querySelector('[data-testid="provider-edit-btn-openai-main"]') as HTMLButtonElement
    await act(async () => {
      editButton.click()
    })

    const fetchButton = container.querySelector('.shell-provider-fetch-btn') as HTMLButtonElement
    await act(async () => {
      fetchButton.click()
    })

    const saveButton = findSaveButton() as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    const req = hoisted.configure.mock.calls[0]![0]
    const m = (req.Models as Array<Record<string, unknown>>).find((x: any) => x['Name'] === 'gpt-5')!
    // Protocol updated from 'openai' (matches effective kind 'openai') to 'anthropic'.
    expect(m['Protocol']).toBe('anthropic')
  })

  it('preserves user-set Protocol (gemini) on re-fetch', async () => {
    hoisted.providers = [{
      Name: 'openai-main',
      Kind: 'openai',
      Endpoint: 'https://api.openai.com/v1',
      Models: [
        { Name: 'gpt-5', Protocol: 'gemini', MaxContextLength: 128000, Modality: 'chat' },
      ],
      HasAuthToken: true,
    }]
    hoisted.providerFetchModels.mockResolvedValueOnce({
      Models: [{ Name: 'gpt-5', Modality: 'chat', Protocol: 'openai' }],
    })
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })

    const editButton = container.querySelector('[data-testid="provider-edit-btn-openai-main"]') as HTMLButtonElement
    await act(async () => {
      editButton.click()
    })

    const fetchButton = container.querySelector('.shell-provider-fetch-btn') as HTMLButtonElement
    await act(async () => {
      fetchButton.click()
    })

    const saveButton = findSaveButton() as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    const req = hoisted.configure.mock.calls[0]![0]
    const m = (req.Models as Array<Record<string, unknown>>).find((x: any) => x['Name'] === 'gpt-5')!
    // Protocol preserved (gemini ≠ effective kind 'openai').
    expect(m['Protocol']).toBe('gemini')
  })

  it('imports all model settings after destructive confirmation', async () => {
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })
    const input = container.querySelector('[data-testid="provider-import-input"]') as HTMLInputElement
    const file = new File(['{"providers":[]}'], 'settings.json', { type: 'application/json' })
    Object.defineProperty(input, 'files', { configurable: true, value: [file] })

    await act(async () => {
      input.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(hoisted.importConfig).not.toHaveBeenCalled()

    const confirmButton = document.querySelector('.confirm-dialog-btn.confirm') as HTMLButtonElement
    await act(async () => {
      confirmButton.click()
    })

    expect(hoisted.importConfig).toHaveBeenCalledWith({}, { Data: '{"providers":[]}' })
    expect(hoisted.reload).toHaveBeenCalled()
  })
})

describe('ShellProviderSettings health reset', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.aggregatorList.mockResolvedValue({ Items: [] })
    hoisted.probeTokens.mockResolvedValue({ Usage: { InputTokens: 1 } })
    hoisted.providers = []
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
  })

  const unhealthyProvider = {
    Name: 'kimi',
    Kind: 'openai',
    Endpoint: 'https://api.kimi.com',
    Models: [{ Name: 'k2' }],
    HasAuthToken: true,
    HealthState: 'disabled',
    HealthReason: 'authorization_failed',
    RecoveryMode: 'manual_or_balance_refresh',
  }

  it('shows the reset button for an unhealthy provider and calls provider_reset_health', async () => {
    hoisted.providers = [unhealthyProvider]
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })
    const btn = container.querySelector('[data-testid="provider-reset-health-btn-kimi"]') as HTMLButtonElement | null
    expect(btn).toBeTruthy()
    await act(async () => {
      btn!.click()
    })
    expect(hoisted.providerResetHealth).toHaveBeenCalledWith({}, { ProviderName: 'kimi' })
    expect(hoisted.reload).toHaveBeenCalled()
  })

  it('hides the reset button for a healthy provider', async () => {
    hoisted.providers = [{ ...unhealthyProvider, HealthState: 'healthy', HealthReason: '', RecoveryMode: '' }]
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })
    expect(container.querySelector('[data-testid="provider-reset-health-btn-kimi"]')).toBeNull()
  })
})

describe('ShellProviderSettings provider probe and expand', () => {
  let container: HTMLDivElement
  let root: Root

  const probeProvider = {
    Name: 'openai-main',
    Kind: 'openai',
    Endpoint: 'https://api.openai.com/v1',
    Models: [
      { Name: 'gpt-5', Protocol: 'openai' },
      { Name: 'gpt-5-mini', Protocol: 'openai' },
      { Name: 'gpt-image-1', Protocol: 'openai', Modality: 'image' },
    ],
    HasAuthToken: true,
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.aggregatorList.mockResolvedValue({
      Items: [{ Id: 'system', Name: 'System', ActorId: 'agg-actor-1', Strategy: 'round_robin' }],
    })
    hoisted.probeTokens.mockResolvedValue({ Usage: { InputTokens: 1 } })
    hoisted.providers = [probeProvider]
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
  })

  const renderSettings = async () => {
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })
  }

  it('expands and collapses the provider detail panel on row click', async () => {
    await renderSettings()

    const row = container.querySelector('[data-testid="provider-card-openai-main"]') as HTMLElement
    expect(row).toBeTruthy()
    expect(container.querySelectorAll('.shell-provider-unit-row')).toHaveLength(0)

    await act(async () => {
      row.click()
    })
    expect(container.querySelectorAll('.shell-provider-unit-row')).toHaveLength(3)
    expect(row.getAttribute('aria-expanded')).toBe('true')

    await act(async () => {
      row.click()
    })
    expect(container.querySelectorAll('.shell-provider-unit-row')).toHaveLength(0)
    expect(row.getAttribute('aria-expanded')).toBe('false')
  })

  it('does not expand when clicking a row action button', async () => {
    await renderSettings()

    const copyButton = container.querySelector('[data-testid="provider-copy-btn-openai-main"]') as HTMLButtonElement
    await act(async () => {
      copyButton.click()
    })

    expect(container.querySelectorAll('.shell-provider-unit-row')).toHaveLength(0)
  })

  it('probes every chat unit from the provider row button and persists results', async () => {
    hoisted.providers = [{ ...probeProvider, MaxConcurrency: 2 }]
    await renderSettings()

    const probeButton = container.querySelector('[data-testid="provider-probe-btn-openai-main"]') as HTMLButtonElement
    expect(probeButton).toBeTruthy()
    expect(probeButton.disabled).toBe(false)

    await act(async () => {
      probeButton.click()
    })

    // The panel auto-expands and each chat unit gets one probe call.
    expect(container.querySelectorAll('.shell-provider-unit-row')).toHaveLength(3)
    expect(hoisted.probeTokens).toHaveBeenCalledTimes(2)
    const calls = hoisted.probeTokens.mock.calls as unknown as Array<[unknown, Record<string, unknown>, { target: string }]>
    expect(calls[0]![2]).toEqual({ target: 'agg-actor-1' })
    expect(calls[0]![1]).toMatchObject({ Unit: { provider: 'openai-main', model: 'gpt-5' } })
    expect(calls[1]![1]).toMatchObject({ Unit: { provider: 'openai-main', model: 'gpt-5-mini' } })

    // Each completed probe outcome is persisted via aimanager.
    expect(hoisted.providerRecordProbe).toHaveBeenCalledTimes(2)
    expect(hoisted.providerRecordProbe.mock.calls[0]![1]).toMatchObject({
      ProviderName: 'openai-main', Model: 'gpt-5', Ok: true,
    })
    expect(hoisted.providerRecordProbe.mock.calls[0]![1].LatencyMs).toBeGreaterThanOrEqual(0)
    expect(hoisted.providerRecordProbe.mock.calls[1]![1]).toMatchObject({
      ProviderName: 'openai-main', Model: 'gpt-5-mini', Ok: true,
    })

    const statuses = container.querySelectorAll('.shell-provider-unit-probe-status.ok')
    expect(statuses).toHaveLength(2)

    // Health projections refresh after probing.
    expect(hoisted.reload).toHaveBeenCalled()
  })

  it('caps batch probe concurrency by the provider MaxConcurrency', async () => {
    // First two probes are launched together and both stay pending until the
    // shared barrier resolves; the third model must not start before that.
    let resolveFirst!: () => void
    const firstGate = new Promise<void>(r => { resolveFirst = r })
    let inFlight = 0
    let maxInFlight = 0
    hoisted.probeTokens.mockImplementation(async () => {
      inFlight++
      maxInFlight = Math.max(maxInFlight, inFlight)
      if (maxInFlight <= 2 && inFlight >= 2) resolveFirst()
      await firstGate
      inFlight--
      return { Usage: { InputTokens: 1 } }
    })
    hoisted.providers = [{
      ...probeProvider,
      MaxConcurrency: 2,
      Models: [
        { Name: 'm-a', Protocol: 'openai' },
        { Name: 'm-b', Protocol: 'openai' },
        { Name: 'm-c', Protocol: 'openai' },
      ],
    }]
    await renderSettings()

    const probeButton = container.querySelector('[data-testid="provider-probe-btn-openai-main"]') as HTMLButtonElement
    await act(async () => {
      probeButton.click()
    })

    expect(hoisted.probeTokens.mock.calls.length).toBeGreaterThanOrEqual(2)
    resolveFirst()
    await act(async () => { await Promise.resolve() })

    expect(hoisted.probeTokens).toHaveBeenCalledTimes(3)
    expect(maxInFlight).toBe(2)
  })

  it('shows a failed probe status and persists the error', async () => {
    hoisted.probeTokens.mockRejectedValue(new Error('aiaggregator.probe_tokens: stream open: 401 unauthorized'))
    await renderSettings()

    const probeButton = container.querySelector('[data-testid="provider-probe-btn-openai-main"]') as HTMLButtonElement
    await act(async () => {
      probeButton.click()
    })

    const failed = container.querySelector('.shell-provider-unit-probe-status.failed') as HTMLElement
    expect(failed).toBeTruthy()
    expect(failed.getAttribute('title')).toBe('stream open: 401 unauthorized')
    expect(hoisted.providerRecordProbe.mock.calls[0]![1]).toMatchObject({
      ProviderName: 'openai-main', Model: 'gpt-5', Ok: false,
      Error: 'stream open: 401 unauthorized',
    })
    expect(hoisted.reload).toHaveBeenCalled()
  })

  it('renders persisted probe state from provider list', async () => {
    hoisted.providers = [{
      ...probeProvider,
      Models: [
        { Name: 'gpt-5', Protocol: 'openai', ProbeState: 'ok', ProbeLatencyMs: 321, ProbeAt: Math.floor(Date.now() / 1000) - 60 },
        { Name: 'gpt-5-mini', Protocol: 'openai', ProbeState: 'failed', ProbeError: 'stream open: 429 rate limit', ProbeAt: Math.floor(Date.now() / 1000) - 3600 },
      ],
    }]
    await renderSettings()

    const row = container.querySelector('[data-testid="provider-card-openai-main"]') as HTMLElement
    await act(async () => {
      row.click()
    })

    expect(hoisted.probeTokens).not.toHaveBeenCalled()
    const okBadge = container.querySelector('[data-testid="provider-unit-openai-main-gpt-5"] .shell-provider-unit-probe-status.ok') as HTMLElement
    expect(okBadge).toBeTruthy()
    const failedBadge = container.querySelector('[data-testid="provider-unit-openai-main-gpt-5-mini"] .shell-provider-unit-probe-status.failed') as HTMLElement
    expect(failedBadge).toBeTruthy()
    expect(failedBadge.getAttribute('title')).toBe('stream open: 429 rate limit')
  })

  it('edits a unit inline and saves via configure', async () => {
    await renderSettings()

    const row = container.querySelector('[data-testid="provider-card-openai-main"]') as HTMLElement
    await act(async () => {
      row.click()
    })

    const editButton = container.querySelector('[data-testid="provider-unit-edit-openai-main-gpt-5"]') as HTMLButtonElement
    await act(async () => {
      editButton.click()
    })

    const nameInput = container.querySelector('[data-testid="provider-unit-name-input-openai-main"]') as HTMLInputElement
    expect(nameInput).toBeTruthy()
    expect(nameInput.value).toBe('gpt-5')

    const valueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      valueSetter.call(nameInput, 'gpt-5-renamed')
      nameInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const saveButton = container.querySelector('[data-testid="provider-unit-save-openai-main"]') as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0] as Record<string, unknown>
    expect(req['AuthToken']).toBeUndefined()
    const models = req['Models'] as Array<Record<string, unknown>>
    expect(models.map(m => m['Name'])).toEqual(['gpt-5-renamed', 'gpt-5-mini', 'gpt-image-1'])
    expect(models[0]!).toMatchObject({ Protocol: 'openai', MaxContextLength: undefined })
    expect(container.querySelector('[data-testid="provider-unit-editing-openai-main-gpt-5"]')).toBeNull()
  })

  it('removes a single unit from the provider after confirmation', async () => {
    await renderSettings()

    const row = container.querySelector('[data-testid="provider-card-openai-main"]') as HTMLElement
    await act(async () => {
      row.click()
    })

    const deleteButton = container.querySelector('[data-testid="provider-unit-delete-openai-main-gpt-5"]') as HTMLButtonElement
    expect(deleteButton).toBeTruthy()
    await act(async () => {
      deleteButton.click()
    })
    expect(hoisted.configure).not.toHaveBeenCalled()

    const confirmButton = document.querySelector('.confirm-dialog-btn.confirm') as HTMLButtonElement
    await act(async () => {
      confirmButton.click()
    })

    expect(hoisted.configure).toHaveBeenCalledTimes(1)
    const req = hoisted.configure.mock.calls[0]![0] as Record<string, unknown>
    expect(req['Name']).toBe('openai-main')
    expect(req['Kind']).toBe('openai')
    expect(req['Endpoint']).toBe('https://api.openai.com/v1')
    expect(req['AuthToken']).toBeUndefined()
    expect((req['Models'] as Array<{ Name: string }>).map(m => m.Name))
      .toEqual(['gpt-5-mini', 'gpt-image-1'])
    expect(hoisted.aggregatorList).toHaveBeenCalled()
  })

  describe('model defaults tab', () => {
    const DEFAULT_ITEMS = [
      { Prefix: 'glm-5.2', MaxContextLength: 204800 },
      { Prefix: 'gpt-5', MaxContextLength: 262144 },
    ]

    beforeEach(() => {
      hoisted.modelDefaultsGet.mockResolvedValue({ Items: DEFAULT_ITEMS })
    })

    it('renders default data from the backend on mount', async () => {
      await act(async () => { root.render(<ShellProviderSettings section="defaults" />) })
      expect(hoisted.modelDefaultsGet).toHaveBeenCalledTimes(1)
    })

    it('switches to the defaults tab and shows rows', async () => {
      await act(async () => { root.render(<ShellProviderSettings section="defaults" />) })
      expect(container.querySelector('[data-testid="provider-defaults-panel"]')).not.toBeNull()
      expect(container.querySelector('[data-testid="defaults-prefix-0"]')).toBeTruthy()
      const prefixInput = container.querySelector('[data-testid="defaults-prefix-0"]') as HTMLInputElement
      expect(prefixInput.value).toBe('glm-5.2')
    })

    it('adds a new empty row', async () => {
      await act(async () => { root.render(<ShellProviderSettings section="defaults" />) })
      // Already on defaults tab, no click needed
      const addBtn = container.querySelector('[data-testid="defaults-add-btn"]') as HTMLButtonElement
      await act(async () => { addBtn.click() })
      const rows = container.querySelectorAll('[data-testid^="defaults-prefix-"]')
      expect(rows.length).toBe(DEFAULT_ITEMS.length + 1)
    })

    it('removes a row', async () => {
      await act(async () => { root.render(<ShellProviderSettings section="defaults" />) })
      // Already on defaults tab, no click needed
      const removeBtn = container.querySelector('[data-testid="defaults-remove-0"]') as HTMLButtonElement
      await act(async () => { removeBtn.click() })
      const rows = container.querySelectorAll('[data-testid^="defaults-prefix-"]')
      expect(rows.length).toBe(DEFAULT_ITEMS.length - 1)
    })

    it('validates required prefix before save', async () => {
      hoisted.modelDefaultsGet.mockResolvedValue({ Items: [] })
      await act(async () => { root.render(<ShellProviderSettings section="defaults" />) })
      // Already on defaults tab, no click needed
      // Add a row with empty prefix — save should trigger validation
      const addBtn = container.querySelector('[data-testid="defaults-add-btn"]') as HTMLButtonElement
      await act(async () => { addBtn.click() })
      const saveBtn = container.querySelector('[data-testid="defaults-save-btn"]') as HTMLButtonElement
      await act(async () => { saveBtn.click() })
      expect(hoisted.modelDefaultsSet).not.toHaveBeenCalled()
      expect(container.querySelector('[data-testid="defaults-error"]')).toBeTruthy()
    })

    it('validates duplicate prefix before save', async () => {
      await act(async () => { root.render(<ShellProviderSettings section="defaults" />) })
      // Already on defaults tab, no click needed
      const addBtn = container.querySelector('[data-testid="defaults-add-btn"]') as HTMLButtonElement
      await act(async () => { addBtn.click() })
      // Set the new row's prefix (index 2) to the same value as row 0
      const prefixInput = container.querySelector('[data-testid="defaults-prefix-2"]') as HTMLInputElement
      const nativeSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      await act(async () => {
        nativeSetter.call(prefixInput, 'glm-5.2')
        prefixInput.dispatchEvent(new Event('input', { bubbles: true }))
      })
      const contextInput = container.querySelector('[data-testid="defaults-context-2"]') as HTMLInputElement
      await act(async () => {
        nativeSetter.call(contextInput, '131072')
        contextInput.dispatchEvent(new Event('input', { bubbles: true }))
      })
      const saveBtn = container.querySelector('[data-testid="defaults-save-btn"]') as HTMLButtonElement
      await act(async () => { saveBtn.click() })
      expect(hoisted.modelDefaultsSet).not.toHaveBeenCalled()
      expect(container.querySelector('[data-testid="defaults-error"]')).toBeTruthy()
    })

    it('calls modelDefaultsSet on save with non-builtin rows', async () => {
      hoisted.modelDefaultsGet.mockResolvedValue({ Items: [] })
      await act(async () => { root.render(<ShellProviderSettings section="defaults" />) })
      // Already on defaults tab, no click needed

      // Panel should show the empty state first
      expect(container.querySelector('[data-testid="defaults-empty"]')).toBeTruthy()

      // Add a row
      const addBtn = container.querySelector('[data-testid="defaults-add-btn"]') as HTMLButtonElement
      await act(async () => { addBtn.click() })
      expect(container.querySelector('[data-testid="defaults-empty"]')).toBeNull()

      // Fill in the row
      const prefixInput = container.querySelector('[data-testid="defaults-prefix-0"]') as HTMLInputElement
      const nativeSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      await act(async () => {
        nativeSetter.call(prefixInput, 'my-model')
        prefixInput.dispatchEvent(new Event('input', { bubbles: true }))
      })
      const contextInput = container.querySelector('[data-testid="defaults-context-0"]') as HTMLInputElement
      await act(async () => {
        nativeSetter.call(contextInput, '131072')
        contextInput.dispatchEvent(new Event('input', { bubbles: true }))
      })

      // Verify save button is enabled
      const saveBtn = container.querySelector('[data-testid="defaults-save-btn"]') as HTMLButtonElement
      expect(saveBtn.disabled).toBe(false)
      await act(async () => { saveBtn.click() })
      expect(hoisted.modelDefaultsSet).toHaveBeenCalledTimes(1)
      const req = hoisted.modelDefaultsSet.mock.calls[0]![1]
      expect(req.Items).toHaveLength(1)
      expect(req.Items[0]).toMatchObject({ Prefix: 'my-model', MaxContextLength: 131072 })
    })

    it('applies the user-editable defaults table to newly fetched models', async () => {
      hoisted.modelDefaultsGet.mockResolvedValue({
        Items: [{ Prefix: 'my-custom-', MaxContextLength: 999 }],
      })
      hoisted.providers = [{
        Name: 'openai-main',
        Kind: 'openai',
        Endpoint: 'https://api.openai.com/v1',
        Models: [],
        HasAuthToken: true,
      }]
      hoisted.providerFetchModels.mockResolvedValueOnce({
        Models: [{ Name: 'my-custom-v1' }],
      })
      await act(async () => {
        root.render(<ShellProviderSettings />)
      })

      const editButton = container.querySelector('[data-testid="provider-edit-btn-openai-main"]') as HTMLButtonElement
      await act(async () => {
        editButton.click()
      })

      const fetchButton = container.querySelector('.shell-provider-fetch-btn') as HTMLButtonElement
      await act(async () => {
        fetchButton.click()
      })

      const saveButton = Array.from(container.querySelectorAll('button'))
        .find(b => b.textContent === 'settings.provider.dialog.save') as HTMLButtonElement
      await act(async () => {
        saveButton.click()
      })

      expect(hoisted.configure).toHaveBeenCalledTimes(1)
      const req = hoisted.configure.mock.calls[0]![0]
      const model = (req.Models as Array<Record<string, unknown>>).find(m => m['Name'] === 'my-custom-v1')!
      expect(model['MaxContextLength']).toBe(999)
    })

    it('no longer renders the OpenRouter update button', async () => {
      await act(async () => { root.render(<ShellProviderSettings section="defaults" />) })
      expect(container.querySelector('[data-testid="defaults-update-btn"]')).toBeNull()
    })

    it('shows a brand icon on recognized model-default prefix rows', async () => {
      hoisted.modelDefaultsGet.mockResolvedValue({
        Items: [
          { Prefix: 'glm-5.2', MaxContextLength: 204800 },
          { Prefix: 'claude-', MaxContextLength: 200000 },
          { Prefix: 'my-custom-', MaxContextLength: 999 },
        ],
      })
      await act(async () => { root.render(<ShellProviderSettings section="defaults" />) })

      const rows = container.querySelectorAll('[data-testid^="defaults-prefix-"]')
      expect(rows).toHaveLength(3)
      const icons = Array.from(rows).map(r =>
        (r.parentElement as HTMLElement).querySelectorAll('svg').length,
      )
      expect(icons).toEqual([1, 1, 0])
    })
  })
})

describe('ShellProviderSettings disable toggles', () => {
  let container: HTMLDivElement
  let root: Root

  const toggleProvider = {
    Name: 'openai-main',
    Kind: 'openai',
    Endpoint: 'https://api.openai.com/v1',
    Models: [
      { Name: 'gpt-5', Protocol: 'openai' },
      { Name: 'gpt-5-mini', Protocol: 'openai', Disabled: true },
    ],
    HasAuthToken: true,
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.aggregatorList.mockResolvedValue({
      Items: [{ Id: 'system', Name: 'System', ActorId: 'agg-actor-1', Strategy: 'round_robin' }],
    })
    hoisted.probeTokens.mockResolvedValue({ Usage: { InputTokens: 1 } })
    hoisted.providers = [toggleProvider]
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
  })

  const renderSettings = async () => {
    await act(async () => {
      root.render(<ShellProviderSettings />)
    })
  }

  it('calls providerSetDisabled with correct args and keeps reload silent', async () => {
    hoisted.providers = [{ ...toggleProvider, Disabled: false }]
    await renderSettings()

    const switchEl = container.querySelector('[data-testid="provider-disable-switch-openai-main"]') as HTMLButtonElement
    expect(switchEl).toBeTruthy()
    await act(async () => {
      switchEl.click()
    })

    expect(hoisted.providerSetDisabled).toHaveBeenCalledTimes(1)
    expect(hoisted.providerSetDisabled).toHaveBeenCalledWith({}, { ProviderName: 'openai-main', Disabled: true })
    expect(hoisted.reload).toHaveBeenCalledWith(true)
  })

  it('marks a disabled provider card with is-disabled class', async () => {
    hoisted.providers = [{ ...toggleProvider, Disabled: true }]
    await renderSettings()

    const card = container.querySelector('[data-testid="provider-card-openai-main"]') as HTMLElement
    expect(card).toBeTruthy()
    expect(card.closest('.shell-provider-card')!.classList.contains('is-disabled')).toBe(true)
  })

  it('does not expand the provider when clicking the header switch', async () => {
    hoisted.providers = [{ ...toggleProvider, Disabled: false }]
    await renderSettings()

    const switchEl = container.querySelector('[data-testid="provider-disable-switch-openai-main"]') as HTMLButtonElement
    await act(async () => {
      switchEl.click()
    })

    expect(container.querySelectorAll('.shell-provider-unit-row')).toHaveLength(0)
  })

  it('calls providerSetDisabled for a unit and keeps reload silent', async () => {
    hoisted.providers = [{ ...toggleProvider }]
    await renderSettings()

    const header = container.querySelector('[data-testid="provider-card-openai-main"]') as HTMLElement
    await act(async () => {
      header.click()
    })

    const switchEl = container.querySelector('[data-testid="provider-unit-disable-switch-openai-main-gpt-5"]') as HTMLButtonElement
    expect(switchEl).toBeTruthy()
    await act(async () => {
      switchEl.click()
    })

    expect(hoisted.providerSetDisabled).toHaveBeenCalledTimes(1)
    expect(hoisted.providerSetDisabled).toHaveBeenCalledWith({}, {
      ProviderName: 'openai-main',
      Model: 'gpt-5',
      Disabled: true,
    })
    expect(hoisted.reload).toHaveBeenCalledWith(true)
  })

  it('marks a disabled unit row with is-disabled class', async () => {
    await renderSettings()

    const header = container.querySelector('[data-testid="provider-card-openai-main"]') as HTMLElement
    await act(async () => {
      header.click()
    })

    const row = container.querySelector('[data-testid="provider-unit-openai-main-gpt-5-mini"]') as HTMLElement
    expect(row).toBeTruthy()
    expect(row.classList.contains('is-disabled')).toBe(true)
  })
})

describe('ShellProviderSettings aggregator disable toggle', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.providers = []
    hoisted.probeTokens.mockResolvedValue({ Usage: { InputTokens: 1 } })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
  })

  const renderAggregators = async (items: any[]) => {
    hoisted.aggregatorList.mockResolvedValue({ Items: items })
    await act(async () => {
      root.render(<ShellProviderSettings section="aggregators" />)
    })
  }

  it('calls aggregatorSetDisabled with correct params for a custom aggregator', async () => {
    await renderAggregators([
      { Id: 'custom-1', Name: 'Custom A', ActorId: 'agg-a', Strategy: 'round_robin', Disabled: false },
    ])

    const switchEl = container.querySelector('[data-testid="aggregator-disable-switch-custom-1"]') as HTMLButtonElement
    expect(switchEl).toBeTruthy()
    await act(async () => {
      switchEl.click()
    })

    expect(hoisted.aggregatorSetDisabled).toHaveBeenCalledTimes(1)
    expect(hoisted.aggregatorSetDisabled).toHaveBeenCalledWith({}, { Id: 'custom-1', Disabled: true })
    expect(hoisted.aggregatorList).toHaveBeenCalled()
    expect(hoisted.reload).toHaveBeenCalledWith(true)
  })

  it('applies is-disabled class and shows disabled badge on a disabled aggregator', async () => {
    await renderAggregators([
      { Id: 'custom-1', Name: 'Custom A', ActorId: 'agg-a', Strategy: 'round_robin', Disabled: true },
    ])

    const card = container.querySelector('[data-testid="aggregator-card-custom-1"]') as HTMLElement
    expect(card.classList.contains('is-disabled')).toBe(true)
    expect(container.querySelector('[data-testid="aggregator-disabled-badge-custom-1"]')).toBeTruthy()
  })

  it('stacks brand icons recursively through nested child aggregators', async () => {
    const { status } = await import('../../../gen-clients/aiaggregator/client')
    const statusMock = vi.mocked(status)
    statusMock.mockImplementation(async (_client: unknown, opts?: { target?: string }) => {
      if (opts?.target === 'agg-parent') {
        return {
          ID: 'custom-parent', Name: 'Parent', UnitCount: 2,
          Units: [
            { Id: 'p::deepseek-chat', Model: 'deepseek-chat', Endpoint: 'https://api.deepseek.com', ProviderName: 'deepseek' },
            { Id: 'agg:custom-child', aggregatorID: 'custom-child', HealthState: 'healthy' },
          ],
        } as any
      }
      return {
        ID: 'custom-child', Name: 'Child', UnitCount: 1,
        Units: [
          { Id: 'o::gpt-5', Model: 'gpt-5', Endpoint: 'https://api.openai.com/v1', ProviderName: 'openai' },
        ],
      } as any
    })
    await renderAggregators([
      { Id: 'custom-parent', Name: 'Parent', ActorId: 'agg-parent', Strategy: 'round_robin' },
      { Id: 'custom-child', Name: 'Child', ActorId: 'agg-child', Strategy: 'round_robin' },
    ])

    const stack = container.querySelector('[data-testid="aggregator-card-custom-parent"] .shell-aggregator-icon-stack') as HTMLElement
    expect(stack).toBeTruthy()
    // Parent has 1 direct model (deepseek); a second brand icon proves the
    // child's gpt-5 was pulled in through the nested aggregator ref.
    expect(stack.querySelectorAll('svg')).toHaveLength(2)
    expect(statusMock).toHaveBeenCalledWith(expect.anything(), { target: 'agg-child' })
  })

  it('does not render the system auto aggregator card', async () => {
    await renderAggregators([
      { Id: 'system', Name: 'System', ActorId: 'agg-system', Strategy: 'round_robin', Disabled: false },
      { Id: 'custom-1', Name: 'Custom A', ActorId: 'agg-a', Strategy: 'round_robin', Disabled: false },
    ])

    expect(container.querySelector('[data-testid="aggregator-card-system"]')).toBeNull()
    expect(container.querySelector('[data-testid="aggregator-disable-switch-system"]')).toBeNull()
    expect(container.querySelector('[data-testid="aggregator-card-custom-1"]')).toBeTruthy()
  })
})
