import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { MediaSettingsPanel } from './MediaSettingsPanel'
import type { MediaAccountView, Provider } from '../../../gen-clients/system/types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  listAccounts: vi.fn(),
  createAccount: vi.fn(),
  updateAccount: vi.fn(),
  deleteAccount: vi.fn(),
  activateAccount: vi.fn(),
  providerList: vi.fn(),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../gen-clients/media/client', () => ({
  listAccounts: hoisted.listAccounts,
  createAccount: hoisted.createAccount,
  updateAccount: hoisted.updateAccount,
  deleteAccount: hoisted.deleteAccount,
  activateAccount: hoisted.activateAccount,
}))

vi.mock('../../../gen-clients/aimanager/client', () => ({
  providerList: hoisted.providerList,
}))

const openaiLlm: Provider = {
  Name: 'openai',
  Kind: 'llm',
  Endpoint: 'https://api.openai.com/v1',
  Models: [],
  HasAuthToken: true,
}

const arkLlm: Provider = {
  Name: 'ark-cn',
  Kind: 'llm',
  Endpoint: 'https://ark.cn-beijing.volces.com/api/v3',
  Models: [{ Name: 'dreamina-seedance-2-0-260128' }],
  HasAuthToken: true,
}

const openaiAccount = (overrides: Partial<MediaAccountView> = {}): MediaAccountView => ({
  Id: 'ma_openai',
  Kind: 'image',
  Name: 'OpenAI',
  Provider: 'openai',
  HasApiKey: true,
  Model: 'gpt-image-2',
  BaseUrl: 'https://api.openai.com/v1',
  ...overrides,
})

describe('MediaSettingsPanel credential auto-import', () => {
  let container: HTMLDivElement
  let root: Root
  // Accounts returned by list_accounts, keyed by kind; mutations simulate the
  // backend having persisted an auto-created account.
  let imageAccounts: MediaAccountView[]
  let videoAccounts: MediaAccountView[]

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    imageAccounts = []
    videoAccounts = []
    hoisted.listAccounts.mockImplementation((_client: any, req: any) => {
      const kind = req?.Kind || 'image'
      const items = kind === 'video' ? videoAccounts : imageAccounts
      return Promise.resolve({ Items: items, ActiveId: items[0]?.Id || '' })
    })
    hoisted.createAccount.mockImplementation((_client: any, req: any) => {
      const acc: MediaAccountView = {
        Id: `ma_${req.Provider}`,
        Kind: req.Kind,
        Name: req.Name,
        Provider: req.Provider,
        HasApiKey: false,
        Model: req.Model,
        BaseUrl: req.BaseUrl,
      }
      if (req.Kind === 'video') videoAccounts = [...videoAccounts, acc]
      else imageAccounts = [...imageAccounts, acc]
      return Promise.resolve({ Account: acc })
    })
    hoisted.providerList.mockResolvedValue({ Items: [openaiLlm] })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
  })

  it('auto-creates an empty-key account for a media provider sharing an LLM domain', async () => {
    await act(async () => {
      root.render(<MediaSettingsPanel />)
    })

    // openai matches the LLM key on api.openai.com and gets an account; the
    // request must never carry the LLM token.
    const imageCreate = hoisted.createAccount.mock.calls.find(call => call[1].Kind === 'image')
    expect(imageCreate).toBeTruthy()
    expect(imageCreate![1]).toMatchObject({
      Kind: 'image',
      Provider: 'openai',
      BaseUrl: 'https://api.openai.com/v1',
      Model: 'gpt-image-2',
    })
    expect(imageCreate![1]).not.toHaveProperty('ApiKey')

    // openai_custom has no well-known endpoint and must not be auto-created.
    const customCreate = hoisted.createAccount.mock.calls.find(call => call[1].Provider === 'openai_custom')
    expect(customCreate).toBeUndefined()

    // The auto-created account is rendered with the auto-key badge.
    expect(container.textContent).toContain('settings.media.provider.openai')
    expect(container.textContent).toContain('settings.media.keyAuto')
  })

  it('never overwrites or resets an existing account', async () => {
    imageAccounts = [openaiAccount(), openaiAccount({ Id: 'ma_openai_empty', HasApiKey: false, Name: 'OpenAI placeholder' })]
    await act(async () => {
      root.render(<MediaSettingsPanel />)
    })

    // Both an existing keyed account and an existing empty-key placeholder are
    // left untouched: no create call at all.
    expect(hoisted.createAccount).not.toHaveBeenCalled()
    expect(container.textContent).toContain('settings.media.keySet')
    expect(container.textContent).toContain('settings.media.keyAuto')
  })

  it('does not auto-create when the LLM provider has no token', async () => {
    hoisted.providerList.mockResolvedValue({ Items: [{ ...openaiLlm, HasAuthToken: false }] })
    await act(async () => {
      root.render(<MediaSettingsPanel />)
    })

    expect(hoisted.createAccount).not.toHaveBeenCalled()
    expect(container.textContent).toContain('settings.media.accountEmpty')
  })

  it('treats a missing aimanager as best-effort and still renders', async () => {
    hoisted.providerList.mockRejectedValue(new Error('aimanager unavailable'))
    await act(async () => {
      root.render(<MediaSettingsPanel />)
    })

    expect(hoisted.createAccount).not.toHaveBeenCalled()
    expect(container.textContent).toContain('settings.media.accountEmpty')
  })

  it('auto-creates an empty-key ark (Seedance) video account when a volcengine.com LLM provider has a token', async () => {
    hoisted.providerList.mockResolvedValue({ Items: [arkLlm] })

    await act(async () => {
      root.render(<MediaSettingsPanel />)
    })

    // The video ark provider must be auto-created with the Seedance 2.0
    // preset model and the China endpoint; the request must never carry the
    // LLM token because the runtime resolves it through aimanager.
    const videoCreate = hoisted.createAccount.mock.calls.find(call => call[1].Provider === 'ark')
    expect(videoCreate).toBeTruthy()
    expect(videoCreate![1]).toMatchObject({
      Kind: 'video',
      Provider: 'ark',
      BaseUrl: 'https://ark.cn-beijing.volces.com/api/v3',
      Model: 'dreamina-seedance-2-0-260128',
    })
    expect(videoCreate![1]).not.toHaveProperty('ApiKey')

    // The doubao image provider shares the Ark endpoint, so its empty-key
    // image account is auto-created too; no other image provider matches
    // volcengine.com.
    const imageCreates = hoisted.createAccount.mock.calls.filter(call => call[1].Kind === 'image')
    expect(imageCreates).toHaveLength(1)
    const imageCreate = imageCreates[0]
    expect(imageCreate![1]).toMatchObject({
      Kind: 'image',
      Provider: 'doubao',
      BaseUrl: 'https://ark.cn-beijing.volces.com/api/v3',
      Model: 'doubao-seedream-3.0-t2i',
    })
    expect(imageCreate![1]).not.toHaveProperty('ApiKey')

    // The auto-key badge surfaces so users can pick the account without
    // manually entering the shared key.
    expect(container.textContent).toContain('settings.media.keyAuto')
  })

  it('does not auto-create an ark account when the volcengine.com LLM provider has no token', async () => {
    hoisted.providerList.mockResolvedValue({ Items: [{ ...arkLlm, HasAuthToken: false }] })

    await act(async () => {
      root.render(<MediaSettingsPanel />)
    })

    const videoCreate = hoisted.createAccount.mock.calls.find(call => call[1].Provider === 'ark')
    expect(videoCreate).toBeUndefined()
    expect(container.textContent).toContain('settings.media.accountEmpty')
  })

  it('auto-creates a doubao (Seedance 2.0) video account sharing the Ark endpoint', async () => {
    hoisted.providerList.mockResolvedValue({ Items: [arkLlm] })

    await act(async () => {
      root.render(<MediaSettingsPanel />)
    })

    // Doubao rides the same Ark backend as ark, so an LLM (doubao) key on
    // ark.cn-beijing.volces.com auto-imports it with the Doubao Seedance 2.0
    // model and never copies the token.
    const doubaoCreate = hoisted.createAccount.mock.calls.find(call => call[1].Kind === 'video' && call[1].Provider === 'doubao')
    expect(doubaoCreate).toBeTruthy()
    expect(doubaoCreate![1]).toMatchObject({
      Kind: 'video',
      Provider: 'doubao',
      BaseUrl: 'https://ark.cn-beijing.volces.com/api/v3',
      Model: 'doubao-seedance-2.0-720p-video',
    })
    expect(doubaoCreate![1]).not.toHaveProperty('ApiKey')
    expect(container.textContent).toContain('settings.media.provider.doubao')
  })

  it('auto-creates qwen (DashScope) image and video accounts sharing the DashScope endpoint', async () => {
    const dashscopeLlm: Provider = {
      Name: 'qwen',
      Kind: 'llm',
      Endpoint: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
      Models: [],
      HasAuthToken: true,
    }
    hoisted.providerList.mockResolvedValue({ Items: [dashscopeLlm] })

    await act(async () => {
      root.render(<MediaSettingsPanel />)
    })

    // Qwen requires native DashScope endpoints for both modalities, so the
    // image and video providers each auto-import an empty-key account with
    // their default model; the LLM token is never copied in.
    const imageCreate = hoisted.createAccount.mock.calls.find(call => call[1].Kind === 'image' && call[1].Provider === 'qwen')
    expect(imageCreate).toBeTruthy()
    expect(imageCreate![1]).toMatchObject({
      Kind: 'image',
      Provider: 'qwen',
      BaseUrl: 'https://dashscope.aliyuncs.com',
      Model: 'qwen-image-3.0-pro',
    })
    expect(imageCreate![1]).not.toHaveProperty('ApiKey')

    const videoCreate = hoisted.createAccount.mock.calls.find(call => call[1].Kind === 'video' && call[1].Provider === 'qwen')
    expect(videoCreate).toBeTruthy()
    expect(videoCreate![1]).toMatchObject({
      Kind: 'video',
      Provider: 'qwen',
      BaseUrl: 'https://dashscope.aliyuncs.com',
      Model: 'wan-2.5-t2v-720p',
    })
    expect(videoCreate![1]).not.toHaveProperty('ApiKey')
  })
})