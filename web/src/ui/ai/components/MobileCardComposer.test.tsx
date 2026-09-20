import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { MobileCardComposer } from './MobileCardComposer'

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  setActive: vi.fn(),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../voice-api', () => ({
  voiceAPI: {
    setActive: hoisted.setActive,
  },
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('MobileCardComposer', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.setActive.mockResolvedValue(undefined)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderComposer = async (props: {
    open: boolean
    value?: string
    onChange?: (v: string) => void
    onSubmit?: () => void
    onClose?: () => void
  }) => {
    await act(async () => {
      root.render(
        <MobileCardComposer
          open={props.open}
          value={props.value ?? ''}
          onChange={props.onChange ?? (() => {})}
          onSubmit={props.onSubmit ?? (() => {})}
          onClose={props.onClose ?? (() => {})}
        />,
      )
    })
  }

  it('renders nothing when closed', async () => {
    await renderComposer({ open: false })
    expect(document.body.querySelector('.mobile-card-composer')).toBeNull()
  })

  it('renders composer panel in a portal when open', async () => {
    await renderComposer({ open: true })
    const panel = document.body.querySelector('.mobile-card-composer')
    expect(panel).not.toBeNull()
    expect(panel?.getAttribute('role')).toBe('dialog')
  })

  it('calls onChange when typing in textarea', async () => {
    const onChange = vi.fn()
    await renderComposer({ open: true, onChange })
    const textarea = document.body.querySelector('.mobile-card-composer-textarea') as HTMLTextAreaElement
    expect(textarea).not.toBeNull()

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')!.set!
      setter.call(textarea, 'hello')
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })

    expect(onChange).toHaveBeenCalledWith('hello')
  })

  it('calls onSubmit when submit button is clicked', async () => {
    const onSubmit = vi.fn()
    await renderComposer({ open: true, value: 'hello', onSubmit })
    const submit = document.body.querySelector('.mobile-card-composer-submit') as HTMLButtonElement
    expect(submit).not.toBeNull()

    await act(async () => {
      submit.click()
    })

    expect(onSubmit).toHaveBeenCalled()
  })

  it('disables submit when value is empty', async () => {
    await renderComposer({ open: true, value: '' })
    const submit = document.body.querySelector('.mobile-card-composer-submit') as HTMLButtonElement
    expect(submit.disabled).toBe(true)
  })

  it('calls onClose when close button is clicked', async () => {
    const onClose = vi.fn()
    await renderComposer({ open: true, onClose })
    const close = document.body.querySelector('.mobile-card-composer-close') as HTMLButtonElement
    expect(close).not.toBeNull()

    await act(async () => {
      close.click()
    })

    expect(onClose).toHaveBeenCalled()
  })

  it('calls onChange with empty string when clear button is clicked', async () => {
    const onChange = vi.fn()
    await renderComposer({ open: true, value: 'hello', onChange })
    const clear = document.body.querySelector('.mobile-card-composer-action[aria-label="mobileCardComposer.clear"]') as HTMLButtonElement
    expect(clear).not.toBeNull()

    await act(async () => {
      clear.click()
    })

    expect(onChange).toHaveBeenCalledWith('')
  })

  it('activates and deactivates voice when the voice button is toggled', async () => {
    hoisted.setActive.mockResolvedValue(undefined)
    await renderComposer({ open: true })
    const voice = document.body.querySelector('.mobile-card-composer-action[aria-label="mobileCardComposer.voice"]') as HTMLButtonElement
    expect(voice).not.toBeNull()
    hoisted.setActive.mockClear()

    await act(async () => {
      voice.click()
    })
    expect(hoisted.setActive).toHaveBeenCalledWith(true, expect.objectContaining({
      onResult: expect.any(Function),
      onError: expect.any(Function),
      onStop: expect.any(Function),
    }))

    await act(async () => {
      voice.click()
    })
    expect(hoisted.setActive).toHaveBeenLastCalledWith(false, undefined)
    // Exactly one activation — the ref guard prevents duplicate transitions.
    expect(hoisted.setActive.mock.calls.filter(c => c[0] === true)).toHaveLength(1)
  })

  it('push-to-talk: long-press activates voice, release deactivates it', async () => {
    vi.useFakeTimers()
    hoisted.setActive.mockResolvedValue(undefined)
    await renderComposer({ open: true })
    const textarea = document.body.querySelector('.mobile-card-composer-textarea') as HTMLTextAreaElement
    hoisted.setActive.mockClear()

    await act(async () => {
      textarea.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerType: 'touch' }))
    })
    await act(async () => {
      vi.advanceTimersByTime(500)
    })
    expect(hoisted.setActive.mock.calls.filter(c => c[0] === true)).toHaveLength(1)

    await act(async () => {
      textarea.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerType: 'touch' }))
    })
    expect(hoisted.setActive).toHaveBeenLastCalledWith(false, undefined)
    vi.useRealTimers()
  })

  it('does not start voice on long-press when the textarea is already focused', async () => {
    vi.useFakeTimers()
    hoisted.setActive.mockResolvedValue(undefined)
    await renderComposer({ open: true })
    const textarea = document.body.querySelector('.mobile-card-composer-textarea') as HTMLTextAreaElement
    hoisted.setActive.mockClear()

    await act(async () => {
      textarea.focus()
      textarea.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerType: 'touch' }))
    })
    await act(async () => {
      vi.advanceTimersByTime(500)
    })

    expect(hoisted.setActive.mock.calls.filter(c => c[0] === true)).toHaveLength(0)
    vi.useRealTimers()
  })

  it('deactivates voice when the sheet closes while recording', async () => {
    hoisted.setActive.mockResolvedValue(undefined)
    await renderComposer({ open: true })
    const voice = document.body.querySelector('.mobile-card-composer-action[aria-label="mobileCardComposer.voice"]') as HTMLButtonElement

    await act(async () => {
      voice.click()
    })
    hoisted.setActive.mockClear()

    await renderComposer({ open: false })
    expect(hoisted.setActive).toHaveBeenLastCalledWith(false, undefined)
  })
})
