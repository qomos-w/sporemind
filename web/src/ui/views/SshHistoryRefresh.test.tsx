import { describe, it, expect, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { SshSessionView } from './SshSessionView'
import { I18nProvider } from '../../i18n'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

class ResizeObserverStub { observe() {} unobserve() {} disconnect() {} }
;(globalThis as any).ResizeObserver = ResizeObserverStub

vi.mock('../../application/generated-client', () => ({ client: {} }))

function disposable() { return { dispose() {} } }
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80; rows = 24; options: Record<string, unknown> = {}
    loadAddon() {} open() {}
    onData() { return disposable() }
    write() {} focus() {} dispose() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))
vi.mock('@xterm/addon-search', () => ({
  SearchAddon: class { onDidChangeResults() { return disposable() } },
}))
vi.mock('@xterm/addon-serialize', () => ({
  SerializeAddon: class { serialize() { return '' } },
}))

const historyListMock = vi.fn()
const shellInputMock = vi.fn()

vi.mock('../../gen-clients/sshmanager/client', () => ({
  fileList: vi.fn().mockResolvedValue({ Items: [] }),
  statusGet: vi.fn().mockResolvedValue({ Status: null }),
  shellOpen: vi.fn().mockResolvedValue({ SessionId: 'sess-1' }),
  shellClose: vi.fn().mockResolvedValue({}),
  shellResize: vi.fn().mockResolvedValue({}),
  shellStream: vi.fn().mockReturnValue({ async *[Symbol.asyncIterator]() {} }),
  shellInput: (...a: unknown[]) => shellInputMock(...a),
  historyList: (...a: unknown[]) => historyListMock(...a),
  exec: vi.fn().mockResolvedValue({ Stdout: '', Stderr: '', ExitCode: 0, Truncated: false, DurationMs: 0 }),
  fileRename: vi.fn(), fileMkdir: vi.fn(), fileWriteBase64: vi.fn(),
  archiveImport: vi.fn(), fileReadBase64: vi.fn(), fileDownload: vi.fn(),
  shellRun: vi.fn(), statusList: vi.fn(),
}))

vi.mock('../../application/theme-persist', () => ({
  savePreference: vi.fn().mockResolvedValue(undefined),
  loadPreference: vi.fn().mockResolvedValue(undefined),
}))

function renderSsh() {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <SshSessionView sessionId="sess-1" hostId="host-1" hostName="host" />
      </I18nProvider>,
    )
  })
  return { container, root }
}

async function flush() {
  await act(async () => { await new Promise(r => setTimeout(r, 0)) })
  await act(async () => { await new Promise(r => setTimeout(r, 0)) })
}

function click(el: Element) {
  act(() => { el.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
}

describe('history dropdown records sent commands', () => {
  it('refreshes the open list after sending a command', async () => {
    // First open: one prior command. After send: two commands.
    historyListMock.mockResolvedValueOnce({ Items: ['uptime'] })
      .mockResolvedValueOnce({ Items: ['ls -la', 'uptime'] })
    shellInputMock.mockResolvedValue({})

    const { container, root } = renderSsh()
    await flush()

    // Open the history dropdown.
    const btn = [...container.querySelectorAll('button')].find(
      b => b.title === 'History',
    ) as HTMLButtonElement
    click(btn)
    await flush()
    expect(historyListMock).toHaveBeenCalledTimes(1)
    expect(container.querySelectorAll('.ssh-cmd-dropdown-item').length).toBe(1)

    // Type and send a command via the input bar (Enter).
    const input = container.querySelector('.ssh-cmd-input') as HTMLInputElement
    act(() => {
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    ;(input as HTMLInputElement).value = 'ls -la'
    act(() => {
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      input.dispatchEvent(
        new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }),
      )
    })
    await flush()

    // Backend received the command with a trailing newline (the recording
    // trigger) and the open dropdown re-fetched the list.
    expect(shellInputMock).toHaveBeenCalledWith(expect.anything(), {
      SessionId: 'sess-1',
      Data: 'ls -la\n',
    })
    expect(historyListMock).toHaveBeenCalledTimes(2)
    const items = container.querySelectorAll('.ssh-cmd-dropdown-item')
    expect(items.length).toBe(2)
    expect(items[0]!.textContent).toBe('ls -la')

    act(() => { root.unmount() })
    container.remove()
  })

  it('css: dropdown width is fixed, not a percentage of the tiny button wrap', () => {
    const css = readFileSync(resolve(__dirname, 'SshSessionView.css'), 'utf8')
    const start = css.indexOf('.ssh-cmd-dropdown {')
    expect(start).toBeGreaterThan(-1)
    const open = css.indexOf('{', start)
    const close = css.indexOf('}', open)
    const body = css.slice(open, close)
    expect(body).toContain('width: 360px')
    expect(body).not.toMatch(/width:.*%/)
  })
})
