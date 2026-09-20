import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useCallback } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { useComposerHistory, COMPOSER_HISTORY_SCOPE, __resetComposerHistoryStoreForTests } from './useComposerHistory'
import type { ComposerHistoryItem } from '../components/AIComposer'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const { getMock, saveMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  saveMock: vi.fn(),
}))

vi.mock('../../../gen-clients/workspace/client', () => ({
  preferencesGet: (...args: unknown[]) => getMock(...args),
  preferencesSave: (...args: unknown[]) => saveMock(...args),
}))

function TestComponent({ onCapture }: { onCapture: (state: { history: ComposerHistoryItem[]; setHistory: (items: ComposerHistoryItem[]) => void }) => void }) {
  const { history, setHistory } = useComposerHistory('test')
  const capture = useCallback(() => {
    onCapture({ history, setHistory })
  }, [history, setHistory, onCapture])
  return (
    <button type="button" onClick={capture}>
      capture
    </button>
  )
}

describe('useComposerHistory', () => {
  let container: HTMLDivElement
  let root: Root
  let captured: { history: ComposerHistoryItem[]; setHistory: (items: ComposerHistoryItem[]) => void } | null = null

  beforeEach(() => {
    captured = null
    __resetComposerHistoryStoreForTests()
    getMock.mockReset()
    saveMock.mockReset()
    saveMock.mockResolvedValue({})
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

  const renderComponent = async () => {
    await act(async () => {
      root.render(<TestComponent onCapture={(state) => { captured = state }} />)
    })
  }

  const capture = async () => {
    const button = container.querySelector('button') as HTMLButtonElement
    await act(async () => {
      button.click()
    })
  }

  it('starts empty while loading', async () => {
    getMock.mockResolvedValue({ Preferences: {} })
    await renderComponent()
    await capture()
    expect(captured!.history).toEqual([])
  })

  it('loads persisted history from workspace preferences', async () => {
    const items: ComposerHistoryItem[] = [{ id: '2', text: 'world', timestamp: 2, isFavorite: true }]
    getMock.mockResolvedValue({ Preferences: { 'ai-composer-history.test.v1': JSON.stringify(items) } })

    await renderComponent()
    await act(async () => {})
    await capture()

    expect(captured!.history).toEqual(items)
  })

  it('saves history to workspace preferences', async () => {
    getMock.mockResolvedValue({ Preferences: {}, Version: 1, AccountId: 'root' })
    await renderComponent()
    await capture()

    const items: ComposerHistoryItem[] = [{ id: '1', text: 'hello', timestamp: 1, isFavorite: false }]
    await act(async () => {
      captured!.setHistory(items)
    })

    expect(saveMock).toHaveBeenCalledTimes(1)
    const command = saveMock.mock.calls[0]?.[1]
    expect(command?.Preferences['ai-composer-history.test.v1']).toEqual(JSON.stringify(items))
  })

  it('isolates scopes in workspace preferences', async () => {
    getMock.mockResolvedValue({ Preferences: { 'ai-composer-history.test.v1': JSON.stringify([{ id: 'a', text: 'a', timestamp: 1, isFavorite: false }]) } })

    function OtherComponent() {
      const { history } = useComposerHistory('other')
      return <div>{JSON.stringify(history)}</div>
    }

    await act(async () => {
      root.render(<OtherComponent />)
    })

    await act(async () => {})

    const text = container.textContent
    expect(text).toContain('[]')
    expect(text).not.toContain('a')
  })

  it('uses the shared composer scope constant', () => {
    expect(COMPOSER_HISTORY_SCOPE).toBe('chat')
  })

  it('shares history across instances using the same scope', async () => {
    const items: ComposerHistoryItem[] = [
      { id: '1', text: 'hello', timestamp: 1, isFavorite: false },
      { id: '2', text: 'world', timestamp: 2, isFavorite: true },
    ]
    getMock.mockResolvedValue({ Preferences: { 'ai-composer-history.shared.v1': JSON.stringify(items) } })

    function First() {
      const { history } = useComposerHistory('shared')
      return <div data-testid="first">{JSON.stringify(history)}</div>
    }
    function Second() {
      const { history } = useComposerHistory('shared')
      return <div data-testid="second">{JSON.stringify(history)}</div>
    }
    function Both() {
      return (
        <>
          <First />
          <Second />
        </>
      )
    }

    await act(async () => {
      root.render(<Both />)
    })
    await act(async () => {})

    const first = container.querySelector('[data-testid="first"]')?.textContent
    const second = container.querySelector('[data-testid="second"]')?.textContent
    expect(first).toEqual(JSON.stringify(items))
    expect(second).toEqual(JSON.stringify(items))
  })

  it('propagates a write from one instance to another mounted instance of the same scope', async () => {
    getMock.mockResolvedValue({ Preferences: {} })

    function Writer() {
      const { setHistory } = useComposerHistory('shared-write')
      return (
        <button
          type="button"
          onClick={() => setHistory([{ id: '1', text: 'hello', timestamp: 1, isFavorite: true }])}
        >
          write
        </button>
      )
    }
    function Reader() {
      const { history } = useComposerHistory('shared-write')
      return <div data-testid="reader">{JSON.stringify(history)}</div>
    }

    await act(async () => {
      root.render(
        <>
          <Writer />
          <Reader />
        </>
      )
    })
    await act(async () => {})

    expect(container.querySelector('[data-testid="reader"]')?.textContent).toEqual('[]')

    const button = container.querySelector('button') as HTMLButtonElement
    await act(async () => {
      button.click()
    })

    expect(container.querySelector('[data-testid="reader"]')?.textContent)
      .toEqual(JSON.stringify([{ id: '1', text: 'hello', timestamp: 1, isFavorite: true }]))
  })

  it('saves to the shared composer key when using the default scope', async () => {
    getMock.mockResolvedValue({ Preferences: {}, Version: 1, AccountId: 'root' })

    function SharedComponent() {
      const { setHistory } = useComposerHistory(COMPOSER_HISTORY_SCOPE)
      return (
        <button
          type="button"
          onClick={() => setHistory([{ id: '1', text: 'hi', timestamp: 1, isFavorite: false }])}
        >
          save
        </button>
      )
    }

    await act(async () => {
      root.render(<SharedComponent />)
    })

    const button = container.querySelector('button') as HTMLButtonElement
    await act(async () => {
      button.click()
    })

    expect(saveMock).toHaveBeenCalledTimes(1)
    const command = saveMock.mock.calls[0]?.[1]
    expect(command?.Preferences['ai-composer-history.chat.v1']).toEqual(JSON.stringify([{ id: '1', text: 'hi', timestamp: 1, isFavorite: false }]))
  })
})
