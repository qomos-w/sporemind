import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ProviderOnboardingModal } from './ProviderOnboardingModal'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => {
  const dict: Record<string, string> = {
    'onboarding.provider.apiKey.label': 'API Key',
    'onboarding.provider.apiKey.getKey': 'Get an API key from the {provider} console',
    'onboarding.provider.intro.cta': 'Get started',
  }
  return {
    t: vi.fn((key: string, params?: Record<string, string | number>) => {
      let message = dict[key] ?? key
      if (params) {
        for (const [name, value] of Object.entries(params)) {
          message = message.replace(`{${name}}`, String(value))
        }
      }
      return message
    }),
    fetchModels: vi.fn(async () => ({ Models: [{ Name: 'gpt-5', Modality: '' }] })),
    configure: vi.fn(async () => []),
  }
})

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../gen-clients/aimanager/client', () => ({
  providerFetchModels: hoisted.fetchModels,
}))

vi.mock('../hooks/useProviderConfigs', () => ({
  useProviderConfigs: () => ({ providers: [], loading: false, configure: hoisted.configure }),
}))

function inputAt(label: string): HTMLInputElement {
  const labels = Array.from(document.querySelectorAll('.onboarding-label'))
  const labelEl = labels.find(el => el.textContent === label)
  if (!labelEl) throw new Error(`label not found: ${label}`)
  const section = labelEl.closest('.onboarding-section') as HTMLElement
  return section.querySelector('input') as HTMLInputElement
}

const nativeInputValueSetter = Object.getOwnPropertyDescriptor(
  HTMLInputElement.prototype, 'value',
)!.set!

describe('ProviderOnboardingModal', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  const render = async () => {
    await act(async () => {
      root.render(<ProviderOnboardingModal onDismiss={vi.fn()} />)
    })
  }

  // Enter the form phase by clicking the intro CTA.
  const enterForm = async () => {
    await act(async () => {
      const cta = Array.from(container.querySelectorAll('button'))
        .find(b => b.textContent === 'Get started') as HTMLButtonElement
      cta.click()
    })
  }

  const selectPreset = async (label: string) => {
    await act(async () => {
      const chip = Array.from(document.querySelectorAll('.onboarding-chip'))
        .find(el => el.textContent === label) as HTMLElement
      chip.click()
    })
  }

  const setKey = async (value: string) => {
    await act(async () => {
      const input = inputAt('API Key')
      nativeInputValueSetter.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  it('shows the intro page first with no form fields', async () => {
    await render()
    expect(container.querySelector('.onboarding-intro')).not.toBeNull()
    expect(container.querySelector('.onboarding-chips')).toBeNull()
  })

  it('transitions to the form on CTA click', async () => {
    await render()
    await enterForm()
    expect(container.querySelector('.onboarding-chips')).not.toBeNull()
    expect(container.querySelector('.onboarding-intro')).toBeNull()
  })

  it('auto-fetches models after endpoint and key are filled', async () => {
    await render()
    await enterForm()
    await selectPreset('DeepSeek')
    await setKey('sk-test-123')
    await act(async () => { await new Promise(r => setTimeout(r, 800)) })
    expect(hoisted.fetchModels).toHaveBeenCalledTimes(1)
  })

  it('shows a validation error for an env-line paste and blocks auto-fetch', async () => {
    await render()
    await enterForm()
    await selectPreset('DeepSeek')
    await setKey('DEEPSEEK_API_KEY=sk-abc')
    expect(container.querySelector('.onboarding-field-error')).not.toBeNull()
    await act(async () => { await new Promise(r => setTimeout(r, 800)) })
    expect(hoisted.fetchModels).not.toHaveBeenCalled()
  })

  it('shows the provider console link for a selected preset', async () => {
    await render()
    await enterForm()
    await selectPreset('OpenAI')
    const link = container.querySelector('.onboarding-key-link') as HTMLAnchorElement
    expect(link).not.toBeNull()
    expect(link.getAttribute('href')).toBe('https://platform.openai.com/api-keys')
    expect(link.textContent).toContain('OpenAI')
  })

  it('offers configure-later dismissal from the intro page', async () => {
    const onDismiss = vi.fn()
    await act(async () => {
      root.render(<ProviderOnboardingModal onDismiss={onDismiss} />)
    })
    const later = container.querySelector('.onboarding-later') as HTMLButtonElement
    expect(later).not.toBeNull()
    await act(async () => { later.click() })
    expect(onDismiss).toHaveBeenCalledTimes(1)
  })
})