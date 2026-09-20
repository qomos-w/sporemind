import { describe, it, expect, vi, beforeEach } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { SshSessionView } from './SshSessionView'
import { I18nProvider } from '../../i18n'
import { savePreference } from '../../application/theme-persist'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// happy-dom has no ResizeObserver; SshSessionView/xterm observe terminal sizing.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
;(globalThis as any).ResizeObserver = ResizeObserverStub

vi.mock('../../application/generated-client', () => ({ client: {} }))

// xterm.js cannot run in happy-dom (no canvas); stub the terminal + addons.
function disposable() { return { dispose() {} } }
const xtermInstances: Array<{ writes: string[]; resets: number }> = []
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    options: Record<string, unknown> = {}
    writes: string[] = []
    resets = 0
    constructor() { xtermInstances.push(this) }
    loadAddon() {}
    open() {}
    onData() { return disposable() }
    write(d: string) { this.writes.push(String(d)) }
    reset() { this.resets++ }
    focus() {}
    dispose() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))
vi.mock('@xterm/addon-search', () => ({
  SearchAddon: class { onDidChangeResults() { return disposable() } },
}))

const fileListMock = vi.fn()
const fileRenameMock = vi.fn()
const fileMkdirMock = vi.fn()
const fileWriteBase64Mock = vi.fn()
const archiveImportMock = vi.fn()
const projectArchiveExportMock = vi.fn()
const statusGetMock = vi.fn()
const shellStreamMock = vi.fn()
// Declared via vi.hoisted so the hoisted sshmanager client mock can reference it.
const fileDeleteMock = vi.hoisted(() => vi.fn().mockResolvedValue({}))

// Desktop availability seam for the Alt+drag-out branch.
const isWailsMock = vi.fn<() => boolean>().mockReturnValue(false)
const startOsDragOutMock = vi.fn().mockResolvedValue({ ok: true })

vi.mock('../../application/runtime', () => ({
  isWails: (..._a: unknown[]) => isWailsMock(),
}))

// Keep the real drop normalization (collectOsDroppedEntries/bytesToBase64);
// only the desktop drag-out call is substituted.
vi.mock('../os-file-dnd', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  return {
    ...actual,
    startOsFileDragOut: (...a: unknown[]) => startOsDragOutMock(...a),
  }
})

vi.mock('../../gen-clients/sshmanager/client', () => ({
  fileList: (...a: unknown[]) => fileListMock(...a),
  fileRename: (...a: unknown[]) => fileRenameMock(...a),
  fileMkdir: (...a: unknown[]) => fileMkdirMock(...a),
  fileWriteBase64: (...a: unknown[]) => fileWriteBase64Mock(...a),
  archiveImport: (...a: unknown[]) => archiveImportMock(...a),
  archiveExport: vi.fn(),
  fileDelete: fileDeleteMock,
  fileWrite: vi.fn().mockResolvedValue({}),
  fileDownload: vi.fn(),
  statusGet: (...a: unknown[]) => statusGetMock(...a),
  statusList: vi.fn(),
  shellStream: (...a: unknown[]) => shellStreamMock(...a),
  shellInput: vi.fn().mockResolvedValue({}),
  shellResize: vi.fn().mockResolvedValue({}),
  shellOpen: vi.fn().mockResolvedValue({ SessionId: 'sess-1' }),
  shellClose: vi.fn().mockResolvedValue({}),
  historyList: vi.fn().mockResolvedValue({ Items: [] }),
  exec: vi.fn().mockResolvedValue({ Stdout: '', Stderr: '', ExitCode: 0, Truncated: false, DurationMs: 0 }),
}))

vi.mock('../../gen-clients/project/client', () => ({
  archiveExport: (...a: unknown[]) => projectArchiveExportMock(...a),
}))

// Mock the preference layer so prefsLoaded resolves synchronously in jsdom.
vi.mock('../../application/theme-persist', () => ({
  savePreference: vi.fn().mockResolvedValue(undefined),
  loadPreference: vi.fn().mockResolvedValue(undefined),
}))

function sshEntry(name: string, isDir: boolean, size = 0): import('../../gen-types/sshmanager').SshFileEntry {
  return { Name: name, FullPath: '/' + name, IsDir: isDir, Size: size, Mode: '-rw-r--r--', Modified: '2026-01-01' }
}

function renderSsh(props?: Partial<Parameters<typeof SshSessionView>[0]>) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <SshSessionView sessionId="sess-1" hostId="host-1" hostName="host" {...props} />
      </I18nProvider>,
    )
  })
  return { container, root }
}

async function flush() {
  // Two micro-task rounds: the first resolves the loadPreference promise
  // (prefsLoaded → true), the second lets the gated loadFiles effect fire.
  await act(async () => { await new Promise(r => setTimeout(r, 0)) })
  await act(async () => { await new Promise(r => setTimeout(r, 0)) })
}

/** Minimal DataTransfer stand-in for happy-dom (no native DnD). */
function makeDataTransfer() {
  const store: Record<string, string> = {}
  return {
    setData: (k: string, v: string) => { store[k] = v },
    getData: (k: string) => store[k] ?? '',
    get types() { return Object.keys(store) },
    effectAllowed: 'none' as string,
    dropEffect: 'none' as string,
  } as unknown as DataTransfer
}

function fireDrag(el: Element, type: string, dt?: DataTransfer) {
  const evt = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperty(evt, 'dataTransfer', { value: dt ?? makeDataTransfer(), configurable: true })
  act(() => { el.dispatchEvent(evt) })
  return evt
}

/** dragstart with a modifier flag (React copies altKey off the native event). */
function fireDragStart(el: Element, opts: { altKey?: boolean; dt?: DataTransfer }) {
  const evt = new Event('dragstart', { bubbles: true, cancelable: true })
  Object.defineProperty(evt, 'dataTransfer', { value: opts.dt ?? makeDataTransfer(), configurable: true })
  if (opts.altKey) Object.defineProperty(evt, 'altKey', { value: true, configurable: true })
  act(() => { el.dispatchEvent(evt) })
  return evt
}

/* ---- OS-level drop plumbing (webkitGetAsEntry DataTransfer stand-in) ---- */

const osEnc = new TextEncoder()

function osFileEntry(name: string, text: string): FileSystemEntry {
  const buffer = osEnc.encode(text).slice().buffer
  const file = { name, size: osEnc.encode(text).length, arrayBuffer: async () => buffer } as unknown as File
  return {
    name,
    isFile: true,
    isDirectory: false,
    file: (cb: (f: File) => void) => cb(file),
  } as unknown as FileSystemEntry
}

function osDirEntry(name: string, children: FileSystemEntry[]): FileSystemEntry {
  const queue = [children, []]
  return {
    name,
    isFile: false,
    isDirectory: true,
    createReader: () => ({
      readEntries: (cb: (entries: FileSystemEntry[]) => void) => {
        cb(queue.shift() ?? [])
      },
    }),
  } as unknown as FileSystemEntry
}

/** DataTransfer advertising native OS files ('Files' type + webkit items). */
function makeOsDataTransfer(items: FileSystemEntry[]): DataTransfer {
  const store: Record<string, string> = {}
  const list = items.map((entry) => ({
    kind: 'file',
    webkitGetAsEntry: () => entry,
    getAsFile: () => null,
  }))
  return {
    setData: (k: string, v: string) => { store[k] = v },
    getData: (k: string) => store[k] ?? '',
    get types() { return ['Files', ...Object.keys(store)] },
    get items() { return list as unknown as DataTransferItemList },
    effectAllowed: 'copy' as string,
    dropEffect: 'none' as string,
  } as unknown as DataTransfer
}

/** Find the interactive hit element of an FTP row (list or icon view) whose
 *  name cell contains `name`. */
function rowByName(container: HTMLElement, name: string): HTMLElement {
  const rows = Array.from(container.querySelectorAll<HTMLElement>('.ssh-file-row, .ssh-file-icon-item'))
  const row = rows.find(r => (r.querySelector('.ssh-file-row-name, .ssh-file-icon-name')?.textContent ?? '').includes(name))
  expect(row, `FTP row for "${name}" exists`).toBeTruthy()
  return row!.querySelector<HTMLElement>('.ssh-item-hit') ?? row!
}

/** Whether an element or its containing FTP row/icon tile carries the selected state. */
function isSelected(el: HTMLElement): boolean {
  return el.classList.contains('selected') || el.closest('.ssh-file-row.selected, .ssh-file-icon-item.selected') !== null
}

/** Read the outer row/tile title (the Alt drag-out hint lives on the visual wrapper). */
function rowTitle(el: HTMLElement): string | null | undefined {
  return el.closest('.ssh-file-row, .ssh-file-icon-item')?.getAttribute('title')
}

const css = readFileSync(resolve(__dirname, 'SshSessionView.css'), 'utf-8')

function ruleBody(selector: string): string {
  const idx = css.indexOf(selector)
  expect(idx, `rule ${selector} exists`).toBeGreaterThan(-1)
  const start = css.indexOf('{', idx)
  const end = css.indexOf('}', start)
  return css.slice(start, end)
}

describe('SshSessionView icon view density', () => {
  it('icon grid uses fixed-width columns (no 1fr stretch gaps)', () => {
    const body = ruleBody('.ssh-files-icons')
    expect(body).toContain('repeat(auto-fill, 84px)')
    expect(body).not.toContain('1fr')
    expect(body).toContain('justify-content: start')
  })
})

describe('SshSessionView multi-select CSS', () => {
  it('highlights selected rows and icons', () => {
    expect(ruleBody('.ssh-file-row.selected')).toContain('var(--accent-primary-dim)')
    expect(ruleBody('.ssh-file-icon-item.selected')).toContain('var(--accent-primary-dim)')
  })

  it('dims cut-source rows and icons with dashed borders', () => {
    const rowBody = ruleBody('.ssh-file-row.cut-source')
    expect(rowBody).toContain('opacity')
    expect(rowBody).toContain('dashed')
    const iconBody = ruleBody('.ssh-file-icon-item.cut-source')
    expect(iconBody).toContain('opacity')
    expect(iconBody).toContain('dashed')
  })
})

describe('SshSessionView terminal search overlay', () => {
  it('terminal body is a positioning context for the overlay', () => {
    const body = ruleBody('.ssh-terminal-body')
    expect(body).toContain('position: relative')
  })

  it('overlay is absolutely positioned in the top-right corner', () => {
    const body = ruleBody('.ssh-search-overlay')
    expect(body).toContain('position: absolute')
    expect(body).toContain('top:')
    expect(body).toContain('right:')
  })

  it('overlay floats bare over the stream; the frosted pill lives on the input', () => {
    const body = ruleBody('.ssh-search-overlay')
    // Chrome-less overlay: no background of its own, so the terminal stream
    // stays visible around the compact search input.
    expect(body).not.toContain('background:')
    expect(body).not.toContain('backdrop-filter:')
    const input = ruleBody('.ssh-search-input')
    expect(input).toMatch(/color-mix|rgba|transparent|hsla/)
    expect(input).toContain('backdrop-filter')
    expect(input).toContain('blur')
  })
})

describe('SshSessionView translucent command & search fields', () => {
  it('command input floats on a translucent frosted plate (no opaque solid)', () => {
    const body = ruleBody('.ssh-cmd-input')
    expect(body).toMatch(/color-mix|rgba|transparent|hsla/)
    expect(body).toContain('backdrop-filter')
    expect(body).toContain('blur')
    // the opaque input background token must be gone
    expect(body).not.toContain('var(--bg-input)')
  })

  it('search input floats on a translucent frosted plate (no opaque solid)', () => {
    const body = ruleBody('.ssh-search-input')
    expect(body).toMatch(/color-mix|rgba|transparent|hsla/)
    expect(body).toContain('backdrop-filter')
    expect(body).toContain('blur')
    expect(body).not.toContain('var(--bg-input)')
  })
})

describe('SshSessionView command bar icon buttons', () => {
  beforeEach(() => {
    fileListMock.mockReset()
    statusGetMock.mockReset()
    shellStreamMock.mockReset()
    fileListMock.mockResolvedValue({ Path: '/', Entries: [] })
    statusGetMock.mockResolvedValue({ Status: null })
    shellStreamMock.mockReturnValue({
      async *[Symbol.asyncIterator]() { /* no chunks */ },
    })
  })

  it('render icons at 16px inside 28px bare hit targets', async () => {
    const { container, root } = renderSsh()
    await flush()
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('.ssh-cmdbar-btn'))
    // history / reconnect / copy / paste / search
    expect(buttons.length).toBeGreaterThanOrEqual(5)
    for (const btn of buttons) {
      const svg = btn.querySelector('svg')
      expect(svg, 'command bar button has an icon').toBeTruthy()
      expect(svg!.getAttribute('width')).toBe('16')
      expect(svg!.getAttribute('height')).toBe('16')
    }
    const btnRule = ruleBody('.ssh-cmdbar-btn')
    expect(btnRule).toContain('width: 28px')
    expect(btnRule).toContain('height: 28px')
    await act(async () => { root.unmount() })
  })

  it('command bar floats bare over the canvas; only the text field is a plate', () => {
    const body = ruleBody('.ssh-cmd-bar')
    expect(body).toContain('position: absolute')
    expect(body).toContain('bottom:')
    // No outer plate: buttons float as bare icons on the terminal canvas.
    expect(body).not.toContain('background:')
    expect(body).not.toContain('backdrop-filter:')
  })

  it('terminal canvas reserves bottom space so the floating bar covers no output', () => {
    const body = ruleBody('.ssh-terminal-canvas')
    expect(body).toMatch(/bottom:\s*44px/)
  })

  it('command bar buttons stay borderless with visible contrast', () => {
    const body = ruleBody('.ssh-cmdbar-btn')
    // enlarged icons, but the control itself has no border
    expect(body).toContain('border: 0')
    expect(body).toContain('color:')
  })
})

describe('SshSessionView terminal buffer snapshot', () => {
  beforeEach(() => {
    fileListMock.mockReset()
    statusGetMock.mockReset()
    shellStreamMock.mockReset()
    fileListMock.mockResolvedValue({ Path: '/', Entries: [] })
    statusGetMock.mockResolvedValue({ Status: null })
    shellStreamMock.mockReturnValue({
      async *[Symbol.asyncIterator]() { /* no chunks */ },
    })
  })

  it('resets once and replays the backend buffer before live output', async () => {
    shellStreamMock.mockReturnValue({
      async *[Symbol.asyncIterator]() {
        yield { Replay: true, Data: 'REPLAYED-PROMPT' }
        yield { Replay: true, Data: 'REPLAYED-MORE' }
        yield { Data: 'LIVE-OUTPUT' }
      },
    })
    const { root } = renderSsh()
    await flush()
    const term = xtermInstances[xtermInstances.length - 1]!
    // Buffer persistence is backend-driven: the stream replays the session
    // buffer (Replay chunks) before live output. The client resets exactly
    // once — before the first replay chunk — so repeated replays across
    // open/close cycles never accumulate in the scrollback.
    expect(term.resets).toBe(1)
    expect(term.writes).toContain('REPLAYED-PROMPT')
    expect(term.writes).toContain('REPLAYED-MORE')
    expect(term.writes).toContain('LIVE-OUTPUT')
    await act(async () => { root.unmount() })
  })

  it('live-only sessions never reset the terminal', async () => {
    shellStreamMock.mockReturnValue({
      async *[Symbol.asyncIterator]() {
        yield { Data: 'LIVE-ONLY' }
      },
    })
    const before = xtermInstances.length
    const { root } = renderSsh()
    await flush()
    expect(xtermInstances[before]!.resets).toBe(0)
    expect(xtermInstances[before]!.writes).toContain('LIVE-ONLY')
    await act(async () => { root.unmount() })
  })
})

describe('SshSessionView file-list column resizing', () => {
  it('header and rows share one CSS variable so columns stay aligned', () => {
    const header = ruleBody('.ssh-files-header')
    const row = ruleBody('.ssh-file-row')
    expect(header).toContain('var(--ssh-file-grid-cols')
    expect(row).toContain('var(--ssh-file-grid-cols')
  })

  it('grid template ends with a flexible track so the list always fills its width', () => {
    const header = ruleBody('.ssh-files-header')
    // the fallback value ends in 1fr
    expect(header).toMatch(/1fr\s*\)/)
  })

  it('resize handle uses a col-resize cursor', () => {
    const body = ruleBody('.ssh-col-resizer')
    expect(body).toContain('cursor: col-resize')
  })

  it('resize handle is absolutely positioned to straddle the column boundary', () => {
    const body = ruleBody('.ssh-col-resizer')
    expect(body).toContain('position: absolute')
    expect(body).toContain('right:')
  })

  it('resize handle disables text selection and touch scrolling to avoid drag conflicts', () => {
    const body = ruleBody('.ssh-col-resizer')
    expect(body).toContain('user-select: none')
    expect(body).toContain('touch-action: none')
  })

  it('suppresses text selection across the document while a column is dragged', () => {
    const body = ruleBody('body.ssh-col-resizing')
    expect(body).toContain('user-select: none')
    expect(body).toContain('cursor: col-resize')
  })
})

describe('SshSessionView cross-panel & internal drag-and-drop', () => {
  beforeEach(() => {
    fileListMock.mockReset()
    fileRenameMock.mockReset()
    archiveImportMock.mockReset()
    projectArchiveExportMock.mockReset()
    statusGetMock.mockReset()
    shellStreamMock.mockReset()
    // Root listing: a folder + a file.
    fileListMock.mockResolvedValue({
      Path: '/',
      Entries: [sshEntry('projects', true), sshEntry('readme.txt', false, 12)],
    })
    statusGetMock.mockResolvedValue({ Status: null })
    // Terminal stream yields nothing and completes immediately.
    shellStreamMock.mockReturnValue({
      async *[Symbol.asyncIterator]() { /* no chunks */ },
    })
    fileRenameMock.mockResolvedValue({})
    projectArchiveExportMock.mockResolvedValue({ Content: 'QkFTRTY0', Size: 8, NumEntries: 2 })
    archiveImportMock.mockResolvedValue({ NumEntries: 2, BytesWritten: 16 })
  })

  it('marks the FTP tree and explorer as wails drop targets so OS drags show the copy cursor', async () => {
    const { container, root } = renderSsh()
    await flush()

    expect(container.querySelector('.ssh-files-tree')!.hasAttribute('data-file-drop-target')).toBe(true)
    expect(container.querySelector('.ssh-files-explorer')!.hasAttribute('data-file-drop-target')).toBe(true)

    await act(async () => { root.unmount() })
  })

  it('uploads a local file to a remote directory via tar.gz archive transfer', async () => {
    const { container, root } = renderSsh()
    await flush()

    const target = rowByName(container, 'projects')
    // Drop a local-origin payload onto the remote directory.
    const dt = makeDataTransfer()
    dt.setData('application/x-sporemind-file-local', JSON.stringify({
      origin: 'local', projectId: 'proj-1', path: 'src/app.go', name: 'app.go', isDir: false,
    }))
    fireDrag(target, 'dragover', dt)
    fireDrag(target, 'drop', dt)
    await flush()

    // Export packs the local source; import extracts into the drop target dir.
    // The archive carries the entry name, so import Path is the target dir itself.
    expect(projectArchiveExportMock).toHaveBeenCalledWith({}, { Path: 'src/app.go' }, { target: 'proj-1' })
    expect(archiveImportMock).toHaveBeenCalledWith(
      {}, { SessionId: 'sess-1', Path: '/projects', Content: 'QkFTRTY0' },
    )

    await act(async () => { root.unmount() })
  })

  it('uploads a local directory to a remote directory via tar.gz archive transfer', async () => {
    const { container, root } = renderSsh()
    await flush()

    const target = rowByName(container, 'projects')
    const dt = makeDataTransfer()
    dt.setData('application/x-sporemind-file-local', JSON.stringify({
      origin: 'local', projectId: 'proj-1', path: 'src', name: 'src', isDir: true,
    }))
    fireDrag(target, 'dragover', dt)
    fireDrag(target, 'drop', dt)
    await flush()

    // Directories are now supported (no longer rejected): routed through archive.
    expect(projectArchiveExportMock).toHaveBeenCalledWith({}, { Path: 'src' }, { target: 'proj-1' })
    expect(archiveImportMock).toHaveBeenCalledWith(
      {}, { SessionId: 'sess-1', Path: '/projects', Content: 'QkFTRTY0' },
    )

    await act(async () => { root.unmount() })
  })

  it('shows a cancel button on a running transfer and aborts it', async () => {
    const { container, root } = renderSsh()
    await flush()

    // Make the export hang until we resolve it, so the transfer stays running.
    let resolveExport!: (v: { Content: string; Size: number; NumEntries: number }) => void
    projectArchiveExportMock.mockReturnValue(new Promise(resolve => { resolveExport = resolve }))

    const target = rowByName(container, 'projects')
    const dt = makeDataTransfer()
    dt.setData('application/x-sporemind-file-local', JSON.stringify({
      origin: 'local', projectId: 'proj-1', path: 'src/app.go', name: 'app.go', isDir: false,
    }))
    fireDrag(target, 'dragover', dt)
    fireDrag(target, 'drop', dt)
    await flush()

    // The running transfer row has a cancel button.
    const cancelBtn = container.querySelector('.ssh-transfer-item.running .ssh-transfer-dismiss') as HTMLButtonElement
    expect(cancelBtn).toBeTruthy()
    act(() => { cancelBtn.click() })
    await flush()

    // After cancel, the item shows the cancelled state and dismisses the AbortController.
    const item = container.querySelector('.ssh-transfer-item') as HTMLElement
    expect(item.className).toContain('cancelled')
    expect(item.textContent).toContain('Cancelled')

    // Let the hung export finish; runTransfer's catch sees the aborted signal and
    // keeps the cancelled state rather than flipping to done/error.
    resolveExport({ Content: 'QkFTRTY0', Size: 8, NumEntries: 2 })
    await flush()

    await act(async () => { root.unmount() })
  })

  it('opens the file context menu on a sidebar tree node right-click', async () => {
    const { container, root } = renderSsh()
    await flush()

    // The root '/' row has no context menu; the first real directory node does.
    const rows = Array.from(container.querySelectorAll('.ssh-file-tree-row'))
    const treeRow = rows.find(r => r.textContent?.includes('projects')) as HTMLElement
    expect(treeRow).toBeTruthy()
    act(() => {
      treeRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }))
    })
    await flush()

    const menu = container.querySelector('.ssh-context-menu') as HTMLElement
    expect(menu).toBeTruthy()
    // Tree nodes are directories: New File / New Folder / Copy Path / Rename / Delete offered.
    const labels = menu.textContent ?? ''
    expect(labels).toContain('New File')
    expect(labels).toContain('New Folder')
    expect(labels).toContain('Rename')

    await act(async () => { root.unmount() })
  })

  it('copies a file via the context menu and pastes it into a directory', async () => {
    const { container, root } = renderSsh()
    await flush()

    // Right-click the readme.txt file row.
    const fileRow = rowByName(container, 'readme.txt')
    act(() => {
      fileRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }))
    })
    await flush()

    const menu = container.querySelector('.ssh-context-menu') as HTMLElement
    expect(menu).toBeTruthy()
    // Click "Copy" in the menu.
    const copyBtn = Array.from(menu.querySelectorAll('button'))
      .find(b => b.textContent?.includes('Copy') && !b.textContent?.includes('Path')) as HTMLButtonElement
    expect(copyBtn).toBeTruthy()
    act(() => { copyBtn.click() })
    await flush()

    // Right-click the projects directory row, then paste.
    const dirRow = rowByName(container, 'projects')
    act(() => {
      dirRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }))
    })
    await flush()
    const menu2 = container.querySelector('.ssh-context-menu') as HTMLElement
    const pasteBtn = Array.from(menu2.querySelectorAll('button'))
      .find(b => b.textContent?.includes('Paste')) as HTMLButtonElement
    expect(pasteBtn).toBeTruthy()
    act(() => { pasteBtn.click() })
    await flush()

    // exec should have been called with cp -r.
    const { exec } = await import('../../gen-clients/sshmanager/client')
    expect(exec).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ HostId: 'host-1', Command: expect.stringContaining('cp -r') }),
    )

    await act(async () => { root.unmount() })
  })

  it('toggles the tree sidebar open and closed', async () => {
    const { container, root } = renderSsh()
    await flush()

    // Tree is visible by default.
    expect(container.querySelector('.ssh-files-tree')).toBeTruthy()

    // Click the tree toggle button.
    const toggleBtn = Array.from(container.querySelectorAll('.ssh-files-toolbar button'))
      .find(b => (b as HTMLButtonElement).title === 'Toggle directory tree') as HTMLButtonElement
    expect(toggleBtn).toBeTruthy()
    act(() => { toggleBtn.click() })
    await flush()

    // Tree is now hidden.
    expect(container.querySelector('.ssh-files-tree')).toBeNull()

    // Toggle back on.
    act(() => { toggleBtn.click() })
    await flush()
    expect(container.querySelector('.ssh-files-tree')).toBeTruthy()

    await act(async () => { root.unmount() })
  })

  it('persists the tree sidebar toggle to the shared SSH UI prefs blob', async () => {
    const saveMock = vi.mocked(savePreference)
    saveMock.mockClear()
    const { container, root } = renderSsh()
    await flush()

    const toggleBtn = Array.from(container.querySelectorAll('.ssh-files-toolbar button'))
      .find(b => (b as HTMLButtonElement).title === 'Toggle directory tree') as HTMLButtonElement
    act(() => { toggleBtn.click() })
    await flush()

    expect(saveMock).toHaveBeenCalled()
    const entry = saveMock.mock.calls[saveMock.mock.calls.length - 1]
    expect(entry).toBeDefined()
    const [key, value] = entry!
    expect(key).toBe('sshSession.ui.v1')
    const blob = JSON.parse(value as string)
    expect(blob.treeSidebarOpen).toBe(false)

    await act(async () => { root.unmount() })
  })

  it('persists the list/icon view switch to the shared SSH UI prefs blob', async () => {
    const saveMock = vi.mocked(savePreference)
    saveMock.mockClear()
    const { container, root } = renderSsh()
    await flush()

    const listBtn = Array.from(container.querySelectorAll('.ssh-files-toolbar button'))
      .find(b => (b as HTMLButtonElement).title === 'List view') as HTMLButtonElement
    act(() => { listBtn.click() })
    await flush()

    const entry = saveMock.mock.calls[saveMock.mock.calls.length - 1]
    expect(entry).toBeDefined()
    const [key, value] = entry!
    expect(key).toBe('sshSession.ui.v1')
    const blob = JSON.parse(value as string)
    expect(blob.fileViewMode).toBe('list')

    await act(async () => { root.unmount() })
  })

  it('moves a remote file into a remote folder via internal drag-and-drop (no regression)', async () => {
    const { container, root } = renderSsh()
    await flush()

    const fileRow = rowByName(container, 'readme.txt')
    const folderRow = rowByName(container, 'projects')

    // Internal move: dragstart sets the dragged entry, drop invokes fileRename.
    fireDrag(fileRow, 'dragstart')
    fireDrag(folderRow, 'dragover')
    fireDrag(folderRow, 'drop')
    await flush()

    expect(fileRenameMock).toHaveBeenCalledWith(
      {}, { SessionId: 'sess-1', From: '/readme.txt', To: '/projects/readme.txt' },
    )

    await act(async () => { root.unmount() })
  })

  it('refuses to move a remote folder into itself (no regression)', async () => {
    const { container, root } = renderSsh()
    await flush()

    const folderRow = rowByName(container, 'projects')

    fireDrag(folderRow, 'dragstart')
    fireDrag(folderRow, 'dragover')
    fireDrag(folderRow, 'drop')
    await flush()

    expect(fileRenameMock).not.toHaveBeenCalled()

    await act(async () => { root.unmount() })
  })
})

describe('SshSessionView multi-select', () => {
  beforeEach(() => {
    fileListMock.mockReset()
    // Path-aware listing: the root fixture contains the multi-select rows; any
    // other path returns a leaf listing so navigating into a directory never
    // yields a node that lists itself (which would make the tree recurse).
    fileListMock.mockImplementation((_client: unknown, req: { Path?: string }) => {
      if (req.Path && req.Path !== '/') {
        return { Path: req.Path, Entries: [sshEntry('inner.txt', false, 1)] }
      }
      return {
        Path: '/',
        Entries: [
          sshEntry('alpha.txt', false, 1),
          sshEntry('beta.txt', false, 2),
          sshEntry('gamma.txt', false, 3),
          sshEntry('projects', true),
        ],
      }
    })
    fileDeleteMock.mockReset()
    fileDeleteMock.mockResolvedValue({})
  })

  it('selects a single entry on plain click', async () => {
    const { container, root } = renderSsh()
    await flush()

    const alpha = rowByName(container, 'alpha.txt')
    act(() => { alpha.click() })
    await flush()

    expect(isSelected(alpha)).toBe(true)

    await act(async () => { root.unmount() })
  })

  it('toggles selection with Ctrl+click', async () => {
    const { container, root } = renderSsh()
    await flush()

    const alpha = rowByName(container, 'alpha.txt')
    const beta = rowByName(container, 'beta.txt')

    act(() => { alpha.click() })
    act(() => { beta.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    expect(isSelected(alpha)).toBe(true)
    expect(isSelected(beta)).toBe(true)

    act(() => { alpha.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    expect(isSelected(alpha)).toBe(false)
    expect(isSelected(beta)).toBe(true)

    await act(async () => { root.unmount() })
  })

  it('selects a range with Shift+click', async () => {
    const { container, root } = renderSsh()
    await flush()

    const alpha = rowByName(container, 'alpha.txt')
    const gamma = rowByName(container, 'gamma.txt')

    act(() => { alpha.click() })
    act(() => { gamma.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, shiftKey: true })) })
    await flush()

    expect(isSelected(rowByName(container, 'alpha.txt'))).toBe(true)
    expect(isSelected(rowByName(container, 'beta.txt'))).toBe(true)
    expect(isSelected(rowByName(container, 'gamma.txt'))).toBe(true)

    await act(async () => { root.unmount() })
  })

  it('clears selection on directory navigation', async () => {
    const { container, root } = renderSsh()
    await flush()

    act(() => { rowByName(container, 'alpha.txt').click() })
    await flush()
    expect(isSelected(rowByName(container, 'alpha.txt'))).toBe(true)

    const projects = rowByName(container, 'projects')
    act(() => { projects.click() })
    await flush()
    // The folder is now open; its (leaf) listing is shown and the previous
    // selection is fully cleared — no row carries the selected class.
    expect(container.querySelector('.ssh-file-row.selected')).toBeNull()

    await act(async () => { root.unmount() })
  })

  it('keeps selection when right-clicking an already selected entry', async () => {
    const { container, root } = renderSsh()
    await flush()

    const alpha = rowByName(container, 'alpha.txt')
    const beta = rowByName(container, 'beta.txt')

    act(() => { alpha.click() })
    act(() => { beta.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    act(() => { beta.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })) })
    await flush()

    expect(isSelected(alpha)).toBe(true)
    expect(isSelected(beta)).toBe(true)

    await act(async () => { root.unmount() })
  })

  it('shows bulk download label for multiple selected entries', async () => {
    const { container } = renderSsh()
    await flush()

    const alpha = rowByName(container, 'alpha.txt')
    const beta = rowByName(container, 'beta.txt')

    act(() => { alpha.click() })
    act(() => { beta.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    act(() => { beta.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })) })
    await flush()

    const menu = container.querySelector('.ssh-context-menu')
    expect(menu).toBeTruthy()
    expect([...menu!.querySelectorAll('button.ssh-context-menu-item')].some(b => b.textContent?.includes('Download 2 items (.tar.gz)'))).toBe(true)
  })

  it('removes an entry from the selection with Alt+click (no re-add)', async () => {
    const { container, root } = renderSsh()
    await flush()

    const alpha = rowByName(container, 'alpha.txt')
    const beta = rowByName(container, 'beta.txt')
    const gamma = rowByName(container, 'gamma.txt')

    act(() => { alpha.click() })
    act(() => { beta.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()
    expect(isSelected(alpha)).toBe(true)
    expect(isSelected(beta)).toBe(true)

    // Alt+click removes the entry from the selection.
    act(() => { beta.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, altKey: true })) })
    await flush()
    expect(isSelected(beta)).toBe(false)
    expect(isSelected(alpha)).toBe(true)

    // Alt+click on a non-selected entry leaves the selection untouched.
    act(() => { gamma.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, altKey: true })) })
    await flush()
    expect(isSelected(gamma)).toBe(false)
    expect(isSelected(alpha)).toBe(true)

    await act(async () => { root.unmount() })
  })

  it('Ctrl+click on a directory joins the selection without navigating', async () => {
    const { container, root } = renderSsh()
    await flush()

    const projects = rowByName(container, 'projects')
    const pathInput = container.querySelector<HTMLInputElement>('.ssh-files-path')

    act(() => { projects.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    expect(isSelected(projects)).toBe(true)
    // Did not navigate into the folder: the path bar still shows the root.
    expect(pathInput?.value).toBe('/')

    await act(async () => { root.unmount() })
  })

  it('Shift+click range includes directories', async () => {
    const { container, root } = renderSsh()
    await flush()

    act(() => { rowByName(container, 'alpha.txt').click() })
    act(() => { rowByName(container, 'projects').dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, shiftKey: true })) })
    await flush()

    for (const n of ['alpha.txt', 'beta.txt', 'gamma.txt', 'projects']) {
      expect(isSelected(rowByName(container, n))).toBe(true)
    }

    await act(async () => { root.unmount() })
  })

  it('Ctrl+click / Alt+click multi-select a directory-tree node', async () => {
    const { container, root } = renderSsh()
    await flush()

    // Expand the root so the "projects" folder appears as a tree child.
    const rootRow = container.querySelector<HTMLElement>('.ssh-file-tree-row')!
    const rootChevron = rootRow.querySelector<HTMLElement>('.ssh-file-tree-chevron--clickable')!
    act(() => { rootChevron.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true })) })
    await flush()

    const treeRows = Array.from(container.querySelectorAll<HTMLElement>('.ssh-file-tree-row'))
    const treeProjects = treeRows.find(r => r.querySelector('.ssh-file-tree-name')?.textContent === 'projects')!
    expect(treeProjects).toBeTruthy()

    act(() => { treeProjects.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()
    expect(isSelected(treeProjects)).toBe(true)

    act(() => { treeProjects.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, altKey: true })) })
    await flush()
    expect(isSelected(treeProjects)).toBe(false)

    await act(async () => { root.unmount() })
  })

  it('deletes multiple selected entries with one confirm (per-path file_delete)', async () => {
    const { container, root } = renderSsh()
    await flush()

    const alpha = rowByName(container, 'alpha.txt')
    const beta = rowByName(container, 'beta.txt')

    act(() => { alpha.click() })
    act(() => { beta.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    // Right-click a selected entry and choose the multi-delete action.
    act(() => { beta.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })) })
    await flush()
    const menu = container.querySelector('.ssh-context-menu')!
    const delBtn = [...menu.querySelectorAll<HTMLButtonElement>('button.ssh-context-menu-item')].find(b => b.textContent?.includes('Delete 2 items'))!
    expect(delBtn).toBeTruthy()
    act(() => { delBtn.click() })
    await flush()

    // The confirmation dialog lists the count, not a single name.
    const msg = container.querySelector('.ssh-dialog-message')
    expect(msg?.textContent).toContain('Are you sure you want to delete 2 items?')

    // Confirm the deletion.
    const confirmBtn = [...container.querySelectorAll<HTMLButtonElement>('.ssh-dialog-actions button')].find(b => b.textContent?.includes('Delete'))!
    act(() => { confirmBtn.click() })
    await flush()

    expect(fileDeleteMock).toHaveBeenCalledWith(expect.anything(), { SessionId: 'sess-1', Path: '/alpha.txt' })
    expect(fileDeleteMock).toHaveBeenCalledWith(expect.anything(), { SessionId: 'sess-1', Path: '/beta.txt' })
    expect(isSelected(alpha)).toBe(false)
    expect(isSelected(beta)).toBe(false)

    await act(async () => { root.unmount() })
  })

  it('clears deleted cut-sources from the FTP clipboard so paste disappears', async () => {
    const { container, root } = renderSsh()
    await flush()

    const alpha = rowByName(container, 'alpha.txt')
    const beta = rowByName(container, 'beta.txt')

    act(() => { alpha.click() })
    act(() => { beta.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    // Cut the two selected entries via the context menu.
    act(() => { alpha.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })) })
    await flush()
    const cutBtn = [...container.querySelector('.ssh-context-menu')!.querySelectorAll<HTMLButtonElement>('button.ssh-context-menu-item')].find(b => b.textContent?.includes('Cut 2 items'))!
    act(() => { cutBtn.click() })
    await flush()

    const hasPaste = () => [...container.querySelectorAll('button.ssh-context-menu-item')].some(b => b.textContent?.trim() === 'Paste')

    // Paste is available right after cutting (right-click a still-selected row
    // so the multi-selection is preserved in the menu).
    act(() => { alpha.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })) })
    await flush()
    expect(hasPaste()).toBe(true)

    // Delete both cut sources.
    act(() => { alpha.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })) })
    await flush()
    const delBtn = [...container.querySelector('.ssh-context-menu')!.querySelectorAll<HTMLButtonElement>('button.ssh-context-menu-item')].find(b => b.textContent?.includes('Delete 2 items'))!
    act(() => { delBtn.click() })
    await flush()
    const confirmBtn = [...container.querySelectorAll<HTMLButtonElement>('.ssh-dialog-actions button')].find(b => b.textContent?.includes('Delete'))!
    act(() => { confirmBtn.click() })
    await flush()

    // Clipboard no longer references the deleted paths → Paste is hidden.
    act(() => { alpha.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })) })
    await flush()
    expect(hasPaste()).toBe(false)

    await act(async () => { root.unmount() })
  })

  it('rubber-band marquee over the file list replaces the selection', async () => {
    const { container, root } = renderSsh()
    await flush()

    // The default view is icons; switch to list so the marquee container mounts.
    const listViewBtn = [...container.querySelectorAll('button')].find(b => b.getAttribute('title') === 'List view')!
    act(() => { listViewBtn.click() })
    await flush()

    const list = container.querySelector<HTMLElement>('.ssh-files-list')!
    const rows = Array.from(list.querySelectorAll<HTMLElement>('.ssh-item-hit'))

    const domRect = (left: number, top: number, width: number, height: number): DOMRect =>
      ({ left, top, right: left + width, bottom: top + height, width, height, x: left, y: top, toJSON: () => ({}) }) as DOMRect
    vi.spyOn(list, 'getBoundingClientRect').mockReturnValue(domRect(0, 0, 400, 300))
    Object.defineProperty(list, 'clientWidth', { value: 400, configurable: true })
    Object.defineProperty(list, 'clientHeight', { value: 300, configurable: true })
    rows.forEach((el, i) => vi.spyOn(el, 'getBoundingClientRect').mockReturnValue(domRect(0, i * 24, 400, 24)))

    const fire = (type: string, target: EventTarget, init: MouseEventInit = {}) => {
      act(() => { target.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, ...init })) })
    }
    // Drag from the empty area below the rows (y=200) up to y=70: the marquee
    // box spans y∈[70,200], covering rows 2-3 (gamma, projects) but not 0-1.
    fire('mousedown', list, { clientX: 10, clientY: 200 })
    fire('mousemove', document, { clientX: 300, clientY: 70 })
    fire('mouseup', document, { clientX: 300, clientY: 70 })
    await flush()

    expect(isSelected(rows[0]!)).toBe(false)
    expect(isSelected(rows[1]!)).toBe(false)
    expect(isSelected(rows[2]!)).toBe(true)
    expect(isSelected(rows[3]!)).toBe(true)

    await act(async () => { root.unmount() })
  })
})

describe('SshSessionView OS-level file drop', () => {
  beforeEach(() => {
    fileListMock.mockReset()
    fileRenameMock.mockReset()
    fileMkdirMock.mockReset()
    fileWriteBase64Mock.mockReset()
    archiveImportMock.mockReset()
    projectArchiveExportMock.mockReset()
    statusGetMock.mockReset()
    shellStreamMock.mockReset()
    isWailsMock.mockReset().mockReturnValue(false)
    startOsDragOutMock.mockClear()
    startOsDragOutMock.mockResolvedValue({ ok: true })
    fileListMock.mockResolvedValue({
      Path: '/',
      Entries: [sshEntry('projects', true), sshEntry('readme.txt', false, 12)],
    })
    statusGetMock.mockResolvedValue({ Status: null })
    shellStreamMock.mockReturnValue({
      async *[Symbol.asyncIterator]() { /* no chunks */ },
    })
    fileRenameMock.mockResolvedValue({})
    fileMkdirMock.mockResolvedValue({})
    fileWriteBase64Mock.mockResolvedValue({})
  })

  it('uploads dropped OS files and directories to a remote folder row (fileMkdir + fileWriteBase64)', async () => {
    const { container, root } = renderSsh()
    await flush()

    const target = rowByName(container, 'projects')
    // docs/ (dir, contains b.md "# doc") + top-level a.txt ("hello")
    const dt = makeOsDataTransfer([
      osDirEntry('docs', [osFileEntry('b.md', '# doc')]),
      osFileEntry('a.txt', 'hello'),
    ])
    fireDrag(target, 'dragover', dt)
    expect(dt.dropEffect).toBe('copy')
    fireDrag(target, 'drop', dt)
    await flush()
    // Two flushes: one for entry expansion, one for the upload chain.
    await flush()

    // The directory entry is created exactly once (backend MkdirAll handles
    // nesting); its child file lands under the created directory.
    expect(fileMkdirMock).toHaveBeenCalledTimes(1)
    expect(fileMkdirMock).toHaveBeenCalledWith({}, { SessionId: 'sess-1', Path: '/projects/docs' })
    expect(fileWriteBase64Mock).toHaveBeenCalledWith(
      {}, { SessionId: 'sess-1', Path: '/projects/docs/b.md', Content: 'IyBkb2M=' },
    )
    // Top-level file goes straight into the target dir (no parent mkdir).
    expect(fileWriteBase64Mock).toHaveBeenCalledWith(
      {}, { SessionId: 'sess-1', Path: '/projects/a.txt', Content: 'aGVsbG8=' },
    )
    // An OS drop is not an in-app move.
    expect(fileRenameMock).not.toHaveBeenCalled()

    await act(async () => { root.unmount() })
  })

  it('uploads a dropped OS file onto the explorer background and refreshes the listing', async () => {
    const { container, root } = renderSsh()
    await flush()
    const initialListCalls = fileListMock.mock.calls.length

    const explorer = container.querySelector<HTMLElement>('.ssh-files-explorer')!
    expect(explorer, 'files explorer exists').toBeTruthy()
    const dt = makeOsDataTransfer([osFileEntry('notes.txt', 'n')])
    fireDrag(explorer, 'dragover', dt)
    expect(dt.dropEffect).toBe('copy')
    fireDrag(explorer, 'drop', dt)
    await flush()
    await flush()

    expect(fileWriteBase64Mock).toHaveBeenCalledWith(
      {}, { SessionId: 'sess-1', Path: '/notes.txt', Content: 'bg==' },
    )
    // The current listing was refreshed after the upload.
    expect(fileListMock.mock.calls.length).toBeGreaterThan(initialListCalls)

    await act(async () => { root.unmount() })
  })

  it('keeps in-app drop semantics when the drag carries no OS files', async () => {
    const { container, root } = renderSsh()
    await flush()

    const fileRow = rowByName(container, 'readme.txt')
    const folderRow = rowByName(container, 'projects')

    // Internal move: dragstart sets the dragged entry, drop invokes fileRename.
    fireDrag(fileRow, 'dragstart')
    fireDrag(folderRow, 'dragover')
    fireDrag(folderRow, 'drop')
    await flush()

    expect(fileRenameMock).toHaveBeenCalledWith(
      {}, { SessionId: 'sess-1', From: '/readme.txt', To: '/projects/readme.txt' },
    )
    expect(fileMkdirMock).not.toHaveBeenCalled()
    expect(fileWriteBase64Mock).not.toHaveBeenCalled()

    await act(async () => { root.unmount() })
  })
})

describe('SshSessionView Alt+drag OS drag-out', () => {
  beforeEach(() => {
    fileListMock.mockReset()
    fileRenameMock.mockReset()
    fileMkdirMock.mockReset()
    fileWriteBase64Mock.mockReset()
    archiveImportMock.mockReset()
    projectArchiveExportMock.mockReset()
    statusGetMock.mockReset()
    shellStreamMock.mockReset()
    isWailsMock.mockReset().mockReturnValue(false)
    startOsDragOutMock.mockClear()
    startOsDragOutMock.mockResolvedValue({ ok: true })
    fileListMock.mockResolvedValue({
      Path: '/',
      Entries: [sshEntry('projects', true), sshEntry('readme.txt', false, 12)],
    })
    statusGetMock.mockResolvedValue({ Status: null })
    shellStreamMock.mockReturnValue({
      async *[Symbol.asyncIterator]() { /* no chunks */ },
    })
    fileRenameMock.mockResolvedValue({})
    fileMkdirMock.mockResolvedValue({})
    fileWriteBase64Mock.mockResolvedValue({})
  })

  it('starts an OS drag-out on Alt+dragstart when on desktop and suppresses the in-app drag', async () => {
    isWailsMock.mockReturnValue(true)
    const { container, root } = renderSsh()
    await flush()

    const fileRow = rowByName(container, 'readme.txt')
    const evt = fireDragStart(fileRow, { altKey: true })
    await flush()

    expect(evt.defaultPrevented).toBe(true)
    expect(startOsDragOutMock).toHaveBeenCalledWith([
      { kind: 'remote', sessionId: 'sess-1', remotePath: '/readme.txt' },
    ])

    // The in-app drag payload was not armed: a subsequent drop must not move.
    const folderRow = rowByName(container, 'projects')
    fireDrag(folderRow, 'dragover')
    fireDrag(folderRow, 'drop')
    await flush()
    expect(fileRenameMock).not.toHaveBeenCalled()

    await act(async () => { root.unmount() })
  })

  it('falls back to the in-app drag on Alt+dragstart outside desktop', async () => {
    const { container, root } = renderSsh()
    await flush()

    const fileRow = rowByName(container, 'readme.txt')
    fireDragStart(fileRow, { altKey: true })
    await flush()

    expect(startOsDragOutMock).not.toHaveBeenCalled()

    // In-app semantics still work after the Alt attempt.
    const folderRow = rowByName(container, 'projects')
    fireDrag(folderRow, 'dragover')
    fireDrag(folderRow, 'drop')
    await flush()
    expect(fileRenameMock).toHaveBeenCalledWith(
      {}, { SessionId: 'sess-1', From: '/readme.txt', To: '/projects/readme.txt' },
    )

    await act(async () => { root.unmount() })
  })

  it('shows the Alt drag-out hint as row title on desktop only', async () => {
    isWailsMock.mockReturnValue(true)
    const desktop = renderSsh()
    await flush()
    const desktopRow = rowByName(desktop.container, 'readme.txt')
    expect(rowTitle(desktopRow)).toContain('Alt')
    await act(async () => { desktop.root.unmount() })

    isWailsMock.mockReturnValue(false)
    const web = renderSsh()
    await flush()
    const webRow = rowByName(web.container, 'readme.txt')
    expect(rowTitle(webRow)).toBeFalsy()
    await act(async () => { web.root.unmount() })
  })
})
