import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '../../../../i18n'
import { DebugConsoleTab } from './DebugConsoleTab'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const { listMock, execMock } = vi.hoisted(() => ({
  listMock: vi.fn(),
  execMock: vi.fn(),
}))

vi.mock('../../../../gen-clients/workspace/client', () => ({
  debugCommandsList: (...args: unknown[]) => listMock(...args),
  debugCommandExec: (...args: unknown[]) => execMock(...args),
}))

vi.mock('../../../../application/generated-client', () => ({
  client: {},
}))

const COMMANDS = [
  { Name: 'help', ShortHelp: 'Show available commands' },
  { Name: 'status', ShortHelp: 'Show runtime status' },
  { Name: 'mem', ShortHelp: 'Dump memory stats' },
]

describe('DebugConsoleTab', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    listMock.mockReset()
    execMock.mockReset()
    listMock.mockResolvedValue({ Commands: COMMANDS })
    execMock.mockResolvedValue({ Output: 'ok output' })
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

  async function renderTab() {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <DebugConsoleTab />
        </I18nProvider>,
      )
    })
    // Flush the commands-list fetch promise.
    await act(async () => {})
  }

  function inputEl(): HTMLInputElement {
    const el = container.querySelector('.debug-console-input')
    if (!el) throw new Error('debug console input not found')
    return el as HTMLInputElement
  }

  async function type(value: string) {
    const el = inputEl()
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    act(() => {
      setter.call(el, value)
      el.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  function pressKey(key: string) {
    act(() => {
      inputEl().dispatchEvent(
        new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }),
      )
    })
  }

  /** Press Enter and flush the exec promise so the record settles. */
  async function pressEnter() {
    pressKey('Enter')
    await act(async () => {})
  }

  it('fetches the command list once on mount and shows the initial hint', async () => {
    await renderTab()
    expect(listMock).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Type a command and press Enter')
  })

  it('opens the completion menu on "/" and lists name + shortHelp', async () => {
    await renderTab()
    await type('/')
    const items = container.querySelectorAll('.debug-console-completion-item')
    expect(items.length).toBe(3)
    expect(items[0]!.textContent).toContain('help')
    expect(items[0]!.textContent).toContain('Show available commands')
    expect(items[1]!.textContent).toContain('status')
  })

  it('filters the completion menu by the typed prefix', async () => {
    await renderTab()
    await type('/st')
    const items = container.querySelectorAll('.debug-console-completion-item')
    expect(items.length).toBe(1)
    expect(items[0]!.textContent).toContain('status')
  })

  it('shows a no-match hint when the prefix matches nothing', async () => {
    await renderTab()
    await type('/zzz')
    expect(container.querySelectorAll('.debug-console-completion-item').length).toBe(0)
    expect(container.textContent).toContain('No matching command')
  })

  it('completes with Tab (cycling) and executes on Enter', async () => {
    await renderTab()
    await type('/he')
    // First Tab fills the first match.
    pressKey('Tab')
    expect(inputEl().value).toBe('help')
    // Second Tab cycles to the next prefix match (none besides "help" here).
    pressKey('Tab')
    expect(inputEl().value).toBe('help')
    await pressEnter()
    expect(execMock).toHaveBeenCalledTimes(1)
    expect(execMock).toHaveBeenCalledWith(expect.anything(), { Name: 'help', Args: [] })
  })

  it('navigates the open menu with ArrowDown/ArrowUp, live-filling the input', async () => {
    await renderTab()
    await type('/')
    pressKey('ArrowDown')
    expect(inputEl().value).toBe('help')
    pressKey('ArrowDown')
    expect(inputEl().value).toBe('status')
    pressKey('ArrowUp')
    expect(inputEl().value).toBe('help')
  })

  it('closes the completion menu with Escape and on outside mousedown', async () => {
    await renderTab()
    await type('/')
    expect(container.querySelector('.debug-console-completion')).toBeTruthy()
    pressKey('Escape')
    expect(container.querySelector('.debug-console-completion')).toBeNull()

    await type('/st')
    expect(container.querySelector('.debug-console-completion')).toBeTruthy()
    // Outside mousedown closes the menu.
    act(() => {
      document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    })
    expect(container.querySelector('.debug-console-completion')).toBeNull()
  })

  it('executes a quoted arg line via splitDebugInput and shows prompt + output', async () => {
    await renderTab()
    await type('echo "hello world"')
    await pressEnter()
    expect(execMock).toHaveBeenCalledTimes(1)
    expect(execMock).toHaveBeenCalledWith(expect.anything(), {
      Name: 'echo',
      Args: ['hello world'],
    })
    const prompt = container.querySelector('.debug-console-prompt')
    expect(prompt?.textContent).toContain('> echo "hello world"')
    const block = container.querySelector('.debug-console-output-block')
    expect(block?.textContent).toContain('ok output')
  })

  it('shows a pending indicator while executing', async () => {
    await renderTab()
    let resolveExec!: (v: { Output: string }) => void
    execMock.mockReturnValue(new Promise((res) => { resolveExec = res }))
    await type('help')
    pressKey('Enter')
    expect(container.querySelector('.debug-console-pending')).toBeTruthy()
    await act(async () => {
      resolveExec({ Output: 'done' })
    })
    expect(container.querySelector('.debug-console-pending')).toBeNull()
    expect(container.querySelector('.debug-console-output-block')?.textContent).toContain('done')
  })

  it('does not execute an empty input', async () => {
    await renderTab()
    pressKey('Enter')
    expect(execMock).not.toHaveBeenCalled()
    expect(container.querySelectorAll('.debug-console-record').length).toBe(0)
  })

  it('navigates history with ArrowUp/ArrowDown, restoring the staged draft', async () => {
    await renderTab()
    await type('help')
    await pressEnter()
    await type('status')
    await pressEnter()

    pressKey('ArrowUp')
    expect(inputEl().value).toBe('status')
    pressKey('ArrowUp')
    expect(inputEl().value).toBe('help')
    pressKey('ArrowDown')
    expect(inputEl().value).toBe('status')
    // Back at the bottom: the draft staged before leaving the fresh line.
    pressKey('ArrowDown')
    expect(inputEl().value).toBe('')

    // A non-empty draft is restored instead of being lost.
    await type('draft text')
    pressKey('ArrowUp')
    expect(inputEl().value).toBe('status')
    pressKey('ArrowDown')
    expect(inputEl().value).toBe('draft text')
  })

  it('records a consecutive duplicate command only once', async () => {
    await renderTab()
    await type('help')
    await pressEnter()
    await type('help')
    await pressEnter()
    // Two executions happened, but history holds a single entry.
    expect(execMock).toHaveBeenCalledTimes(2)
    pressKey('ArrowUp')
    expect(inputEl().value).toBe('help')
    pressKey('ArrowUp')
    expect(inputEl().value).toBe('help')
  })

  it('shows a failed execution as a red error record', async () => {
    await renderTab()
    execMock.mockRejectedValueOnce(new Error('boom'))
    await type('help')
    await pressEnter()
    const record = container.querySelector('.debug-console-record')
    expect(record?.className).toContain('error')
    expect(record?.textContent).toContain('boom')
    const block = container.querySelector('.debug-console-output-block')
    expect(block?.className).toContain('debug-console-output-block')
  })

  it('fills the input and closes the menu when a completion item is clicked', async () => {
    await renderTab()
    await type('/')
    const items = container.querySelectorAll('.debug-console-completion-item')
    act(() => {
      ;(items[1] as HTMLButtonElement).dispatchEvent(
        new MouseEvent('mousedown', { bubbles: true, cancelable: true }),
      )
    })
    expect(inputEl().value).toBe('status')
    expect(container.querySelector('.debug-console-completion')).toBeNull()
  })

  it('cleans up on unmount: no state updates from in-flight work', async () => {
    await renderTab()
    let resolveExec!: (v: { Output: string }) => void
    execMock.mockReturnValue(new Promise((res) => { resolveExec = res }))
    await type('help')
    pressKey('Enter')
    await act(async () => {})
    // Unmount with an in-flight exec and a pending list fetch; resolving them
    // afterwards must not throw or update state (alive guard).
    const pendingList = listMock.mockResolvedValueOnce({ Commands: [] })
    await act(async () => {
      root.unmount()
    })
    container.remove()
    await act(async () => {
      resolveExec({ Output: 'late' })
      await pendingList
    })
  })
})
