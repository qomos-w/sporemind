import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { I18nProvider } from '../../../../i18n'
import { ShellConsoleTab } from './ShellConsoleTab'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// happy-dom has no ResizeObserver; ShellConsoleTab observes terminal sizing.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
;(globalThis as any).ResizeObserver = ResizeObserverStub

vi.mock('../../../../application/generated-client', () => ({ client: {} }))

interface OutputEvent {
  SessionId: string
  Idx: number
  Kind: string
  Data: string
  ExitCode?: number
  Mode?: string
}

const shellState = vi.hoisted(() => ({
  sessionOpen: vi.fn(),
  sessionFetch: vi.fn(),
  sessionWrite: vi.fn(),
  sessionResize: vi.fn(),
  sessionClose: vi.fn(),
  unsubscribe: vi.fn(),
  outputHandler: null as null | ((ev: OutputEvent) => void),
}))

vi.mock('../../../../gen-clients/shell/client', () => ({
  sessionOpen: (...a: unknown[]) => shellState.sessionOpen(...a),
  sessionFetch: (...a: unknown[]) => shellState.sessionFetch(...a),
  sessionWrite: (...a: unknown[]) => shellState.sessionWrite(...a),
  sessionResize: (...a: unknown[]) => shellState.sessionResize(...a),
  sessionClose: (...a: unknown[]) => shellState.sessionClose(...a),
  OnShellSessionOutput: (_c: unknown, h: (ev: unknown) => void) => {
    shellState.outputHandler = h as never
    return () => {
      shellState.outputHandler = null
      shellState.unsubscribe()
    }
  },
}))

// xterm.js cannot run in happy-dom (no canvas); stub the terminal + addons.
interface XtermStub {
  cols: number
  rows: number
  options: Record<string, unknown>
  writes: string[]
  onDataHandler: null | ((data: string) => void)
  disposed: boolean
  loadAddon(): void
  open(): void
  onData(h: (data: string) => void): { dispose(): void }
  attachCustomKeyEventHandler(h: (ev: KeyboardEvent) => boolean): void
  write(d: string): void
  focus(): void
  getSelection(): string
  clearSelection(): void
  selectAll(): void
  clear(): void
  dispose(): void
}

const xtermState = vi.hoisted(() => ({
  instances: [] as XtermStub[],
  onDataDisposed: vi.fn(),
}))

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    options: Record<string, unknown> = {}
    writes: string[] = []
    onDataHandler: null | ((data: string) => void) = null
    disposed = false
    constructor() {
      xtermState.instances.push(this as unknown as XtermStub)
    }
    loadAddon() {}
    open() {}
    attachCustomKeyEventHandler() {}
    onData(h: (data: string) => void) {
      this.onDataHandler = h
      return {
        dispose: () => {
          xtermState.onDataDisposed()
          this.onDataHandler = null
        },
      }
    }
    write(d: string) {
      this.writes.push(String(d))
    }
    focus() {}
    getSelection() { return '' }
    clearSelection() {}
    selectAll() {}
    clear() {}
    dispose() {
      this.disposed = true
    }
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))
vi.mock('@xterm/addon-search', () => ({
  SearchAddon: class {
    findNext = vi.fn()
    findPrevious = vi.fn()
    clearDecorations = vi.fn()
    constructor() {
      searchState.instances.push(this)
    }
    onDidChangeResults() { return { dispose() {} } }
  },
}))

const searchState = vi.hoisted(() => ({
  instances: [] as Array<{ findNext: ReturnType<typeof vi.fn>; findPrevious: ReturnType<typeof vi.fn>; clearDecorations: ReturnType<typeof vi.fn> }>,
}))

beforeEach(() => {
  shellState.sessionOpen.mockReset()
  shellState.sessionFetch.mockReset().mockResolvedValue({ Chunks: [], NextIdx: 0, Truncated: false })
  shellState.sessionWrite.mockReset().mockResolvedValue({})
  shellState.sessionResize.mockReset().mockResolvedValue({})
  shellState.sessionClose.mockReset().mockResolvedValue({})
  shellState.unsubscribe.mockReset()
  shellState.outputHandler = null
  xtermState.instances.length = 0
  xtermState.onDataDisposed.mockReset()
  searchState.instances.length = 0
})

function renderShell(onSetTabTitle: ReturnType<typeof vi.fn>) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <ShellConsoleTab tab={{ id: 'tab-1', type: 'shell' }} onSetTabTitle={onSetTabTitle as (tabId: string, title: string | undefined) => void} />
      </I18nProvider>,
    )
  })
  return { root, container }
}

describe('ShellConsoleTab', () => {
  it('subscribes before opening the session and replays fetch(0)', async () => {
    shellState.sessionOpen.mockResolvedValue({ SessionId: 'sess-1', Mode: 'pty' })
    const onSetTabTitle = vi.fn()
    renderShell(onSetTabTitle)

    // Subscription is set up synchronously on mount, before the async open.
    expect(shellState.outputHandler).toBeTypeOf('function')
    await act(async () => {})

    expect(shellState.sessionOpen).toHaveBeenCalledWith({}, { Cols: 80, Rows: 24 })
    expect(shellState.sessionFetch).toHaveBeenCalledWith({}, { SessionId: 'sess-1', FromIdx: 0 })
    expect(shellState.sessionResize).toHaveBeenCalledWith({}, { SessionId: 'sess-1', Cols: 80, Rows: 24 })
    expect(onSetTabTitle).toHaveBeenCalledWith('tab-1', undefined)
  })

  it('replays buffered chunks from fetch(0) and deduplicates by Idx', async () => {
    shellState.sessionOpen.mockResolvedValue({ SessionId: 'sess-1', Mode: 'pty' })
    shellState.sessionFetch.mockResolvedValue({
      Chunks: [
        { SessionId: 'sess-1', Idx: 0, Kind: 'stdout', Data: 'replay-0' },
        { SessionId: 'sess-1', Idx: 2, Kind: 'stdout', Data: 'replay-2' },
      ],
      NextIdx: 3,
      Truncated: false,
    })
    renderShell(vi.fn())
    await act(async () => {})
    const term = xtermState.instances[0]!

    expect(term.writes).toContain('replay-0')
    expect(term.writes).toContain('replay-2')
    expect(term.writes).not.toContain('replay-1')

    // A live event with an already-seen Idx is ignored (idempotent write).
    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 0, Kind: 'stdout', Data: 'duplicate' })
    })
    expect(term.writes).not.toContain('duplicate')

    // A new Idx is written.
    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 3, Kind: 'stdout', Data: 'live' })
    })
    expect(term.writes).toContain('live')
  })

  it('ignores out-of-order and lower-Idx events after replay', async () => {
    shellState.sessionOpen.mockResolvedValue({ SessionId: 'sess-1', Mode: 'pty' })
    shellState.sessionFetch.mockResolvedValue({
      Chunks: [{ SessionId: 'sess-1', Idx: 5, Kind: 'stdout', Data: 'last' }],
      NextIdx: 6,
      Truncated: false,
    })
    renderShell(vi.fn())
    await act(async () => {})
    const term = xtermState.instances[0]!

    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 3, Kind: 'stdout', Data: 'old' })
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 5, Kind: 'stdout', Data: 'again' })
    })
    expect(term.writes).not.toContain('old')
    expect(term.writes).not.toContain('again')
    expect(term.writes).toContain('last')
  })

  it('drains pre-open events that match the opened session', async () => {
    shellState.sessionOpen.mockImplementation(async () => {
      // Simulate a live event racing in before the open response.
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 0, Kind: 'stdout', Data: 'raced' })
      return { SessionId: 'sess-1', Mode: 'pty' }
    })
    renderShell(vi.fn())
    await act(async () => {})
    const term = xtermState.instances[0]!

    expect(term.writes).toContain('raced')
  })

  it('forwards keystrokes to session_write', async () => {
    shellState.sessionOpen.mockResolvedValue({ SessionId: 'sess-1', Mode: 'pty' })
    renderShell(vi.fn())
    await act(async () => {})
    const term = xtermState.instances[0]!

    act(() => {
      term.onDataHandler!('echo hi\r')
    })
    expect(shellState.sessionWrite).toHaveBeenCalledWith({}, { SessionId: 'sess-1', Data: 'echo hi\r' })
  })

  it('writes stdout events to the terminal and ignores other sessions', async () => {
    shellState.sessionOpen.mockResolvedValue({ SessionId: 'sess-1', Mode: 'pty' })
    renderShell(vi.fn())
    await act(async () => {})
    const term = xtermState.instances[0]!

    act(() => {
      shellState.outputHandler!({ SessionId: 'other', Idx: 0, Kind: 'stdout', Data: 'ignored' })
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 0, Kind: 'stdout', Data: 'hello' })
    })
    expect(term.writes).not.toContain('ignored')
    expect(term.writes).toContain('hello')
  })

  it('decorates pipe-mode stderr chunks in red', async () => {
    shellState.sessionOpen.mockResolvedValue({ SessionId: 'sess-1', Mode: 'pipe' })
    renderShell(vi.fn())
    await act(async () => {})
    const term = xtermState.instances[0]!

    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 0, Kind: 'stderr', Data: 'boom' })
    })
    expect(term.writes).toContain('\x1b[31mboom\x1b[0m')
  })

  it('marks the tab exited on kind=exit: ANSI banner, HTML banner, title suffix, writes suppressed', async () => {
    shellState.sessionOpen.mockResolvedValue({ SessionId: 'sess-1', Mode: 'pipe' })
    const onSetTabTitle = vi.fn()
    const { container } = renderShell(onSetTabTitle)
    await act(async () => {})
    const term = xtermState.instances[0]!

    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 0, Kind: 'exit', Data: 'session exited', ExitCode: 3, Mode: 'pipe' })
    })
    expect(term.writes.join('')).toContain('Session exited (code 3)')
    expect(onSetTabTitle).toHaveBeenLastCalledWith('tab-1', 'Shell (exited)')
    expect(container.querySelector('.terminal-shell-exit')).toBeTruthy()
    expect(container.querySelector('.terminal-shell-exit-hint')!.textContent).toContain('Reopening this tab')

    // writes are dropped once the session has exited
    act(() => {
      term.onDataHandler!('x')
    })
    expect(shellState.sessionWrite).not.toHaveBeenCalled()
  })

  it('cleans up on unmount: unsubscribes, disposes onData and closes the session', async () => {
    shellState.sessionOpen.mockResolvedValue({ SessionId: 'sess-1', Mode: 'pty' })
    const { root } = renderShell(vi.fn())
    await act(async () => {})
    const term = xtermState.instances[0]!

    act(() => {
      root.unmount()
    })
    expect(shellState.unsubscribe).toHaveBeenCalled()
    expect(xtermState.onDataDisposed).toHaveBeenCalled()
    expect(term.disposed).toBe(true)
    expect(shellState.sessionClose).toHaveBeenCalledWith({}, { SessionId: 'sess-1' })
  })

  it('reopening an exited tab starts a brand-new session and clears the suffix', async () => {
    shellState.sessionOpen.mockResolvedValueOnce({ SessionId: 'sess-1', Mode: 'pipe' })
    const onSetTabTitle = vi.fn()
    const { root } = renderShell(onSetTabTitle)
    await act(async () => {})

    // Session dies → tab marked exited.
    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 0, Kind: 'exit', Data: 'session exited', ExitCode: 0, Mode: 'pipe' })
    })
    expect(onSetTabTitle).toHaveBeenLastCalledWith('tab-1', 'Shell (exited)')

    // Closing the tab closes the session.
    shellState.sessionOpen.mockResolvedValueOnce({ SessionId: 'sess-2', Mode: 'pty' })
    act(() => {
      root.unmount()
    })
    expect(shellState.sessionClose).toHaveBeenCalledWith({}, { SessionId: 'sess-1' })

    // Reopening mounts a fresh console → new session, suffix cleared.
    const second = renderShell(onSetTabTitle)
    await act(async () => {})
    expect(shellState.sessionOpen).toHaveBeenLastCalledWith({}, { Cols: 80, Rows: 24 })
    expect(onSetTabTitle).toHaveBeenLastCalledWith('tab-1', undefined)

    // Output for the new session flows again.
    const term = xtermState.instances[1]!
    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-2', Idx: 0, Kind: 'stdout', Data: 'fresh' })
    })
    expect(term.writes).toContain('fresh')

    act(() => {
      second.root.unmount()
    })
  })

  it('shows an error banner and unsubscribes when open fails', async () => {
    shellState.sessionOpen.mockRejectedValue(new Error('boom'))
    const { container } = renderShell(vi.fn())
    await act(async () => {})

    expect(container.querySelector('.terminal-shell-error')).toBeTruthy()
    expect(shellState.outputHandler).toBeNull()
    expect(shellState.unsubscribe).toHaveBeenCalled()
  })

  it('search overlay: context-menu Find opens it, typing is incremental, Enter/Shift+Enter step, Escape closes', async () => {
    shellState.sessionOpen.mockResolvedValue({ SessionId: 'sess-1', Mode: 'pty' })
    const { container } = renderShell(vi.fn())
    await act(async () => {})
    const addon = searchState.instances[0]!

    // No overlay until opened.
    expect(container.querySelector('.terminal-shell-search-overlay')).toBeNull()

    // Right-click → context menu → click "Find".
    const body = container.querySelector('.terminal-shell-body')!
    act(() => {
      body.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }))
    })
    const menu = container.querySelector('.terminal-shell-context-menu')!
    const findBtn = Array.from(menu.querySelectorAll('button'))
      .find(b => b.textContent === 'Find')!
    act(() => {
      findBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const overlay = container.querySelector('.terminal-shell-search-overlay')!
    const input = overlay.querySelector('input')!

    // Typing runs an incremental search; clearing clears decorations.
    act(() => {
      Object.defineProperty(input, 'value', { value: 'err', configurable: true })
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(addon.findNext).toHaveBeenCalledWith('err', expect.objectContaining({ incremental: true }))
    act(() => {
      Object.defineProperty(input, 'value', { value: '', configurable: true })
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(addon.clearDecorations).toHaveBeenCalled()
    addon.findNext.mockClear()

    // Enter = next, Shift+Enter = previous, Escape closes and clears.
    act(() => {
      Object.defineProperty(input, 'value', { value: 'err', configurable: true })
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    addon.findNext.mockClear()
    act(() => {
      input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    })
    expect(addon.findNext).toHaveBeenCalledTimes(1)
    act(() => {
      input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', shiftKey: true, bubbles: true, cancelable: true }))
    })
    expect(addon.findPrevious).toHaveBeenCalledTimes(1)
    act(() => {
      input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    })
    expect(container.querySelector('.terminal-shell-search-overlay')).toBeNull()
    expect(addon.clearDecorations).toHaveBeenCalledTimes(2)
  })
})
