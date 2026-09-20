import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import React from 'react'
import { MiniComposer } from './MiniComposer'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => {
  const view = {
    envelopes: [] as any[],
    isStreaming: false,
    pushUserMessage: vi.fn(() => 'temp-id'),
    replaceUserMessageId: vi.fn(),
    updateUserMessageIdx: vi.fn(),
    cancelPendingUserMessage: vi.fn(),
    dispatchLocalEvent: vi.fn(),
    stop: vi.fn(),
    reconcile: vi.fn(),
    seedActiveTurn: vi.fn(),
  }
  const submit = vi.fn(async () => ({ TurnActorId: 'turn-1', MessageId: 'msg-1', Idx: 1 }))
  return {
    t: vi.fn((key: string) => key),
    voiceAPI: { setActive: vi.fn(async () => {}) },
    view,
    submit,
  }
})

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../voice-api', () => ({
  voiceAPI: hoisted.voiceAPI,
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../gen-clients/local/client', () => ({
  chatSubmit: hoisted.submit,
}))

vi.mock('../hooks/useTimelineManager', () => ({
  useBackgroundTimeline: () => hoisted.view,
}))

vi.mock('./parts/AIStepText', () => ({
  AIStepText: ({ children }: { children: string }) =>
    React.createElement('div', { className: 'ai-step-text' }, children),
}))

describe('MiniComposer', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    hoisted.voiceAPI.setActive.mockResolvedValue(undefined)
    hoisted.view.envelopes = []
    hoisted.view.isStreaming = false
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const render = async (props: Partial<React.ComponentProps<typeof MiniComposer>> = {}) => {
    await act(async () => {
      root.render(
        <MiniComposer
          open
          anchor={{ x: 100, y: 100, width: 200, height: 80 }}
          onClose={vi.fn()}
          targetActorId="arch-1"
          {...props}
        />,
      )
    })
  }

  const typeInto = (ta: HTMLTextAreaElement, text: string) => {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')!.set!
    setter.call(ta, text)
    ta.dispatchEvent(new Event('input', { bubbles: true }))
  }

  it('renders an empty thread and a disabled send button when empty', async () => {
    await render()
    expect(document.body.querySelector('.mini-composer-empty')).toBeTruthy()
    const send = document.body.querySelector('.mini-composer-send') as HTMLButtonElement
    expect(send).toBeTruthy()
    expect(send.disabled).toBe(true)
    // No peek bar / upper info panel in the card chat.
    expect(document.body.querySelector('.mini-composer-peek')).toBeNull()
  })

  it('enables send and submits to the coder agent when clicked', async () => {
    await render()
    const ta = document.body.querySelector('.mini-composer-textarea') as HTMLTextAreaElement
    await act(async () => {
      typeInto(ta, 'hello coder')
    })
    const send = document.body.querySelector('.mini-composer-send') as HTMLButtonElement
    expect(send.disabled).toBe(false)
    await act(async () => {
      send.click()
    })
    // Flush the async submit promise.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })
    expect(hoisted.view.pushUserMessage).toHaveBeenCalledWith('hello coder')
    expect(hoisted.submit).toHaveBeenCalledWith({}, { Text: 'hello coder' }, { target: 'arch-1' })
    expect(hoisted.view.seedActiveTurn).toHaveBeenCalledWith('turn-1')
    expect(hoisted.view.reconcile).toHaveBeenCalled()
    // Composer is cleared after send.
    expect((document.body.querySelector('.mini-composer-textarea') as HTMLTextAreaElement).value).toBe('')
  })

  it('shows the stop button while streaming and calls the timeline stop', async () => {
    hoisted.view.isStreaming = true
    await render()
    const stop = document.body.querySelector('.mini-composer-stop') as HTMLButtonElement
    expect(stop).toBeTruthy()
    expect(document.body.querySelector('.mini-composer-send')).toBeNull()
    await act(async () => {
      stop.click()
    })
    expect(hoisted.view.stop).toHaveBeenCalled()
  })

  it('renders the conversation thread from envelopes', async () => {
    hoisted.view.envelopes = [
      { id: 'u1', clientKey: 'u1', role: 'user', userContent: 'what is X?', frames: [], timestamp: new Date().toISOString() },
      {
        id: 'a1',
        clientKey: 'a1',
        role: 'assistant',
        frames: [{ id: 'f1', type: 'text', content: 'X is a thing' } as any],
        timestamp: new Date().toISOString(),
        completed: true,
      },
    ]
    await render()
    const msgs = document.body.querySelectorAll('.mini-composer-msg')
    expect(msgs.length).toBe(2)
    expect(document.body.querySelector('.mini-composer-bubble-user')?.textContent).toContain('what is X?')
    expect(document.body.querySelector('.mini-composer-bubble-assistant')?.textContent).toContain('X is a thing')
  })

  it('seeds the initial value on a closed→open transition', async () => {
    await act(async () => {
      root.render(<MiniComposer open={false} onClose={vi.fn()} targetActorId="arch-1" initialValue="[[card-42]] " />)
    })
    let ta = document.body.querySelector('.mini-composer-textarea') as HTMLTextAreaElement | null
    expect(ta).toBeNull()
    await act(async () => {
      root.render(<MiniComposer open onClose={vi.fn()} targetActorId="arch-1" initialValue="[[card-42]] " />)
    })
    ta = document.body.querySelector('.mini-composer-textarea') as HTMLTextAreaElement
    expect(ta.value).toBe('[[card-42]] ')
  })
})
