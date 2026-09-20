import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act, useState } from 'react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { FileBrowser } from './FileBrowser'
import { FileClipboardProvider } from './file-clipboard-context'
import { I18nProvider } from '../../../i18n'
import { savePreference, loadPreference } from '../../../application/theme-persist'
import { beginLocalFileDragSession, endLocalFileDragSession, getLocalFileDragActive } from '../../file-dnd'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../application/generated-client', () => ({ client: {} }))

const listMock = vi.fn()
const writeMock = vi.fn()
const rmMock = vi.fn()
const shellExecMock = vi.fn()
const sshFileReadMock = vi.fn()
const projectArchiveImportMock = vi.fn()
const sshArchiveExportMock = vi.fn()
const projectInfoMock = vi.fn()
const fsWriteBase64Mock = vi.fn()
const collectOsDropMock = vi.fn()
const osDragOutMock = vi.fn()
const isWailsMock = vi.fn()

function asyncIterator(chunks: Array<{ ExitCode?: number; Stderr?: string }>) {
  return {
    async *[Symbol.asyncIterator]() {
      for (const c of chunks) yield c
    },
  }
}

vi.mock('../../../gen-clients/project/client', () => ({
  list: (...args: unknown[]) => listMock(...args),
  write: (...args: unknown[]) => writeMock(...args),
  rm: (...args: unknown[]) => rmMock(...args),
  shellExec: (...args: unknown[]) => shellExecMock(...args),
  archiveImport: (...args: unknown[]) => projectArchiveImportMock(...args),
  info: (...args: unknown[]) => projectInfoMock(...args),
}))

vi.mock('../../../gen-clients/sshmanager/client', () => ({
  fileRead: (...args: unknown[]) => sshFileReadMock(...args),
  archiveExport: (...args: unknown[]) => sshArchiveExportMock(...args),
}))

vi.mock('../../../gen-clients/filesystem/client', () => ({
  writeBase64: (...args: unknown[]) => fsWriteBase64Mock(...args),
}))
vi.mock('../../../application/theme-persist', () => ({
  savePreference: vi.fn().mockResolvedValue(undefined),
  loadPreference: vi.fn().mockResolvedValue(undefined),
}))
vi.mock('../../../application/runtime', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../application/runtime')>()
  return { ...actual, isWails: () => isWailsMock() }
})

vi.mock('../../os-file-dnd', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../os-file-dnd')>()
  return {
    ...actual,
    collectOsDroppedEntries: (...args: unknown[]) => collectOsDropMock(...args),
    startOsFileDragOut: (...args: unknown[]) => osDragOutMock(...args),
  }
})

function entry(name: string, isDir: boolean, size = 0) {
  return { Name: name, IsDir: isDir, Size: size, ModTime: '2026-01-01T00:00:00Z' }
}

// Mirrors the backend detail-line contract: `name[/] date time offset [Size:N]`.
function detailLine(e: ReturnType<typeof entry>): string {
  const name = e.IsDir ? `${e.Name}/` : e.Name
  return `${name} 2026-01-01 00:00:00 +00:00${e.IsDir ? '' : ` Size:${e.Size}`}`
}

function setupDirMap(dirs: Record<string, ReturnType<typeof entry>[]>) {
  listMock.mockImplementation((_client: unknown, req: { Path: string }) =>
    Promise.resolve((dirs[req.Path] ?? []).map(detailLine).join('\n')),
  )
}

function renderUI(props?: Partial<Parameters<typeof FileBrowser>[0]>) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <FileClipboardProvider>
          <FileBrowser projectId="proj-1" {...props} />
        </FileClipboardProvider>
      </I18nProvider>,
    )
  })
  return { container, root }
}

/** Controlled FileBrowser mirroring AIShellLayout: selectedPath state + echo via onSelectionChange. */
function renderControlled() {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  let setSel: ((v: string | null) => void) | null = null
  function Harness() {
    const [sel, setSelInner] = useState<string | null>(null)
    setSel = setSelInner
    return (
      <I18nProvider initialLocale="en-US">
        <FileClipboardProvider>
          <FileBrowser projectId="proj-1" selectedPath={sel} onSelectionChange={setSelInner} />
        </FileClipboardProvider>
      </I18nProvider>
    )
  }
  act(() => { root.render(<Harness />) })
  return { container, root, setSelection: (v: string | null) => act(() => { setSel?.(v) }) }
}

async function flush() {
  await act(async () => { await new Promise(r => setTimeout(r, 0)) })
}

function clickView(container: HTMLElement, title: string) {
  const btn = container.querySelector(`[title="${title}"]`) as HTMLElement
  expect(btn).toBeTruthy()
  act(() => { btn.click() })
}

/** Minimal DataTransfer stand-in for jsdom (no native DnD). */
function makeDataTransfer() {
  const store: Record<string, string> = {}
  return {
    setData: (k: string, v: string) => { store[k] = v },
    getData: (k: string) => store[k] ?? '',
    get types() { return Object.keys(store) },
    effectAllowed: 'none' as string,
    dropEffect: 'none' as string,
  }
}

function fireDrag(el: Element, type: string) {
  const evt = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperty(evt, 'dataTransfer', { value: makeDataTransfer(), configurable: true })
  act(() => { el.dispatchEvent(evt) })
}

function rowByTitle(container: HTMLElement, selector: string, title: string): HTMLElement {
  const row = container.querySelector(`${selector}[title="${title}"]`) as HTMLElement
  expect(row).toBeTruthy()
  return row.querySelector('.fb-item-hit') ?? row
}

/** Minimal DOMRect stub for marquee hit-testing in the layout-less DOM. */
const domRect = (left: number, top: number, width: number, height: number): DOMRect =>
  ({
    left, top, right: left + width, bottom: top + height, width, height, x: left, y: top,
    toJSON: () => ({}),
  }) as DOMRect

/**
 * Marquee layout stub: `.fb-main-content` is 400x300 at the origin; every list
 * row is a 400x24 band starting 40px down (a: 40-64, b: 64-88, c: 88-112),
 * leaving genuine empty area above/below rows for press points.
 */
function stubMarqueeLayout(container: HTMLElement) {
  const main = container.querySelector('.fb-main-content') as HTMLElement
  expect(main).toBeTruthy()
  vi.spyOn(main, 'getBoundingClientRect').mockReturnValue(domRect(0, 0, 400, 300))
  Object.defineProperty(main, 'clientWidth', { value: 400, configurable: true })
  Object.defineProperty(main, 'clientHeight', { value: 300, configurable: true })
  Array.from(container.querySelectorAll<HTMLElement>('.fb-item-hit')).forEach((el, i) => {
    vi.spyOn(el, 'getBoundingClientRect').mockReturnValue(domRect(0, 40 + i * 24, 400, 24))
  })
  return main
}

function marqueeDrag(main: HTMLElement, from: { x: number; y: number }, to: { x: number; y: number }, mods: { alt?: boolean; ctrl?: boolean; shift?: boolean } = {}) {
  act(() => {
    main.dispatchEvent(new MouseEvent('mousedown', {
      bubbles: true, cancelable: true, button: 0,
      clientX: from.x, clientY: from.y,
      altKey: !!mods.alt, ctrlKey: !!mods.ctrl, shiftKey: !!mods.shift,
    }))
  })
  act(() => {
    document.dispatchEvent(new MouseEvent('mousemove', {
      bubbles: true, cancelable: true, clientX: to.x, clientY: to.y,
      altKey: !!mods.alt, ctrlKey: !!mods.ctrl, shiftKey: !!mods.shift,
    }))
  })
  act(() => {
    document.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, cancelable: true }))
  })
}

/** DataTransfer that looks like an OS file drag ('Files' in types). */
function osFileDataTransfer() {
  const dt = makeDataTransfer()
  dt.setData('Files', '')
  return dt
}

/** Dispatch a drag event with an explicit DataTransfer (and optional altKey). */
function fireDragEvent(el: Element, type: string, dt: ReturnType<typeof makeDataTransfer>, opts?: { altKey?: boolean }) {
  const evt = new MouseEvent(type, { bubbles: true, cancelable: true, altKey: opts?.altKey ?? false })
  Object.defineProperty(evt, 'dataTransfer', { value: dt, configurable: true })
  act(() => { el.dispatchEvent(evt) })
  return evt
}

/** A dropped OS entry stand-in (mirrors os-file-dnd OsDroppedEntry). */
function osEntry(relPath: string, isDir: boolean, bytes?: Uint8Array) {
  return {
    name: relPath.split('/').pop() ?? relPath,
    relPath,
    isDir,
    size: bytes?.length ?? 0,
    readBytes: () => Promise.resolve(bytes ?? new Uint8Array(0)),
  }
}

describe('FileBrowser', () => {
  beforeEach(() => {
    listMock.mockReset()
    writeMock.mockReset()
    rmMock.mockReset()
    shellExecMock.mockReset()
    sshFileReadMock.mockReset()
    projectArchiveImportMock.mockReset()
    sshArchiveExportMock.mockReset()
    projectInfoMock.mockReset()
    fsWriteBase64Mock.mockReset()
    collectOsDropMock.mockReset()
    osDragOutMock.mockReset()
    isWailsMock.mockReset().mockReturnValue(false)
    listMock.mockResolvedValue('')
    shellExecMock.mockReturnValue(asyncIterator([{ ExitCode: 0 }]))
    projectInfoMock.mockResolvedValue({ Roots: [{ Name: 'default', Path: 'D:\\proj' }] })
    fsWriteBase64Mock.mockResolvedValue({})
    collectOsDropMock.mockResolvedValue([])
    osDragOutMock.mockResolvedValue({ ok: true })
    vi.mocked(savePreference).mockClear()
    vi.mocked(loadPreference).mockReset().mockResolvedValue(undefined)
    endLocalFileDragSession()
  })

  it('renders in grid view by default with a toggleable tree sidebar', async () => {
    setupDirMap({ '.': [entry('README.md', false, 100), entry('src', true)] })
    const { container, root } = renderUI()
    await flush()

    const buttons = container.querySelectorAll('.fb-view-switch .fb-view-btn')
    expect(buttons.length).toBe(3)
    expect(container.querySelector('[title="Toggle tree sidebar"]')).toBeTruthy()
    expect(container.querySelector('.fb-tree-sidebar')).toBeTruthy()
    expect(container.querySelector('.fb-grid')).toBeTruthy()
    expect(container.querySelector('.fb-toolbar-path')).toBeTruthy()

    await act(async () => { root.unmount() })
  })

  it('carries the wails drop-target attribute so OS file drags show the copy cursor', async () => {
    setupDirMap({ '.': [] })
    const { container, root } = renderUI()
    await flush()

    expect(container.querySelector('.file-browser')!.hasAttribute('data-file-drop-target')).toBe(true)

    await act(async () => { root.unmount() })
  })

  it('switches to list view and can toggle the tree sidebar off', async () => {
    setupDirMap({ '.': [entry('a.ts', false, 42), entry('pkg', true)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    expect(container.querySelector('.fb-list')).toBeTruthy()
    expect(container.querySelector('.fb-tree-sidebar')).toBeTruthy()
    expect(container.querySelector('.fb-toolbar-path')).toBeTruthy()

    // Toggle the tree sidebar off
    const toggle = container.querySelector('[title="Toggle tree sidebar"]') as HTMLElement
    expect(toggle).toBeTruthy()
    act(() => { toggle.click() })
    await flush()
    expect(container.querySelector('.fb-tree-sidebar')).toBeNull()

    await act(async () => { root.unmount() })
  })

  it('clicking a tree directory navigates the main content and keeps the row selected', async () => {
    setupDirMap({ '.': [entry('README.md', false, 100), entry('src', true)], src: [entry('a.ts', false, 42)] })
    const { container, root } = renderUI()
    await flush()

    const treeRow = rowByTitle(container, '.fb-tree-node-row', 'src')
    await act(async () => { treeRow.click() })
    await flush()

    // Main content now lists src's contents, breadcrumb reflects the path.
    expect(container.querySelector('.fb-grid-name')?.textContent).toBe('a.ts')
    const crumbs = Array.from(container.querySelectorAll('.fb-crumb'))
    expect(crumbs.at(-1)?.textContent).toBe('src')
    // The clicked tree row stays selected.
    expect(treeRow.className).toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('clicking the tree chevron only toggles expansion without navigating or selecting', async () => {
    setupDirMap({ '.': [entry('README.md', false, 100), entry('src', true)], src: [entry('a.ts', false, 42)] })
    const { container, root } = renderUI()
    await flush()

    const treeRow = rowByTitle(container, '.fb-tree-node-row', 'src')
    const chevron = treeRow.querySelector('.fb-tree-chevron') as HTMLElement
    expect(chevron).toBeTruthy()
    await act(async () => { chevron.click() })
    await flush()

    // Child rows are revealed...
    expect(rowByTitle(container, '.fb-tree-node-row', 'src/a.ts')).toBeTruthy()
    // ...but the main view stays at the project root and the row is not selected.
    const crumbs = Array.from(container.querySelectorAll('.fb-crumb'))
    expect(crumbs.at(-1)?.textContent).not.toBe('src')
    const gridNames = Array.from(container.querySelectorAll('.fb-grid-name')).map(el => el.textContent)
    expect(gridNames).toContain('README.md')
    expect(gridNames).not.toContain('a.ts')
    expect(treeRow.className).not.toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('switches to grid view', async () => {
    setupDirMap({ '.': [entry('x.go', false, 10)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'Grid view')
    await flush()

    expect(container.querySelector('.fb-grid')).toBeTruthy()

    await act(async () => { root.unmount() })
  })

  it('left-clicks a file in list view to open it in the right panel', async () => {
    setupDirMap({ '.': [entry('hello.ts', false, 50)] })
    const onOpenFile = vi.fn()
    const { container, root } = renderUI({ onOpenFile })
    await flush()

    clickView(container, 'List view')
    await flush()

    const row = container.querySelector('.fb-list-row .fb-item-hit') as HTMLElement
    expect(row).toBeTruthy()
    await act(async () => { row.click() })

    expect(onOpenFile).toHaveBeenCalledWith('hello.ts', 'proj-1')

    await act(async () => { root.unmount() })
  })

  it('left-clicks a folder in list view to navigate into it and shows breadcrumb path', async () => {
    setupDirMap({ '.': [entry('sub', true)], sub: [entry('deep.md', false, 1)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const folderRow = container.querySelector('.fb-list-row .fb-item-hit') as HTMLElement
    expect(folderRow).toBeTruthy()
    await act(async () => { folderRow.click() })
    await flush()

    expect(listMock).toHaveBeenCalledWith(
      expect.anything(), { Path: 'sub', Depth: 0, All: false, No_ignore: false, Detail: true }, { target: 'proj-1' },
    )

    const crumbs = container.querySelectorAll('.fb-crumb')
    expect(crumbs.length).toBeGreaterThanOrEqual(2)
    expect(crumbs[crumbs.length - 1]!.textContent).toBe('sub')

    await act(async () => { root.unmount() })
  })

  it('toggling show-hidden requests hidden entries (All + No_ignore)', async () => {
    setupDirMap({ '.': [entry('node_modules', true)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'Show hidden files and folders')
    await flush()

    expect(listMock).toHaveBeenCalledWith(
      expect.anything(), { Path: '.', Depth: 0, All: true, No_ignore: true, Detail: true }, { target: 'proj-1' },
    )

    await act(async () => { root.unmount() })
  })

  it('opens context menu on right-click in list view', async () => {
    setupDirMap({ '.': [entry('doc.md', false, 5)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const row = container.querySelector('.fb-list-row .fb-item-hit') as HTMLElement
    expect(row).toBeTruthy()
    await act(async () => {
      row.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })

    const menu = container.querySelector('.fb-context-menu')
    expect(menu).toBeTruthy()
    const items = menu!.querySelectorAll('.fb-context-menu-item')
    const labels = Array.from(items).map(b => b.textContent?.trim())
    expect(labels).toContain('Open')
    expect(labels).toContain('Copy Path')
    expect(labels).toContain('Rename')
    expect(labels).toContain('Delete')

    await act(async () => { root.unmount() })
  })

  it('favorites an entry from the context menu: star badge, favorites bar, persistence', async () => {
    setupDirMap({ '.': [entry('README.md', false, 5), entry('src', true)] })
    const { container, root } = renderUI()
    await flush()

    // No favorites yet: no bar, no badges.
    expect(container.querySelector('.fb-fav-bar')).toBeNull()
    expect(container.querySelector('.fb-star-badge')).toBeNull()

    const row = rowByTitle(container, '.fb-grid-item', 'src')
    await act(async () => {
      row.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })
    let menu = container.querySelector('.fb-context-menu')!
    const labels = () => Array.from(menu.querySelectorAll('.fb-context-menu-item')).map(b => b.textContent?.trim())
    expect(labels()).toContain('Favorite')
    expect(labels()).not.toContain('Unfavorite')

    const favBtn = Array.from(menu.querySelectorAll('.fb-context-menu-item'))
      .find(b => b.textContent?.trim() === 'Favorite') as HTMLElement
    act(() => { favBtn.click() })
    await flush()

    // Star badge in the top-right corner of the item + chips in the favorites bar.
    const badge = container.querySelector('.fb-grid-item .fb-star-badge') as HTMLElement
    expect(badge).toBeTruthy()
    expect(container.querySelector('.fb-fav-bar')).toBeTruthy()
    expect(container.querySelector('.fb-fav-chip-name')?.textContent).toBe('src')
    expect(vi.mocked(savePreference)).toHaveBeenCalledWith(
      'fileBrowser.favorites.proj-1.v1',
      JSON.stringify([{ path: 'src', isDir: true }]),
      'fb-fav-proj-1',
      expect.anything(),
    )

    // The badge click unfavorites; the bar disappears when the last chip is gone.
    act(() => { badge.click() })
    await flush()
    expect(container.querySelector('.fb-star-badge')).toBeNull()
    expect(container.querySelector('.fb-fav-bar')).toBeNull()

    await act(async () => { root.unmount() })
  })

  it('favorites bar chips navigate directories and open files; remove button unfavorites', async () => {
    setupDirMap({ '.': [entry('docs', true), entry('README.md', false, 3)], docs: [entry('guide.md', false, 2)] })
    vi.mocked(loadPreference).mockImplementation((key: string) =>
      key === 'fileBrowser.favorites.proj-1.v1'
        ? Promise.resolve(JSON.stringify([{ path: 'docs', isDir: true }, { path: 'README.md', isDir: false }]))
        : Promise.resolve(undefined))
    const onOpenFile = vi.fn()
    const { container, root } = renderUI({ onOpenFile })
    await flush()

    const chips = container.querySelectorAll<HTMLElement>('.fb-fav-chip-open')
    expect(chips.length).toBe(2)

    // Directory chip navigates into the folder.
    act(() => { chips[0]!.click() })
    await flush()
    expect(listMock.mock.calls.some(([, req]) => (req as { Path: string }).Path === 'docs')).toBe(true)

    // File chip opens the file in the right panel.
    act(() => { chips[1]!.click() })
    expect(onOpenFile).toHaveBeenCalledWith('README.md', 'proj-1')

    // Chip remove button unfavorites the path.
    const remove = container.querySelectorAll<HTMLElement>('.fb-fav-chip-remove')[0]!
    act(() => { remove.click() })
    await flush()
    expect(container.querySelectorAll('.fb-fav-chip-open').length).toBe(1)
    expect(vi.mocked(savePreference)).toHaveBeenCalledWith(
      'fileBrowser.favorites.proj-1.v1',
      JSON.stringify([{ path: 'README.md', isDir: false }]),
      'fb-fav-proj-1',
      expect.anything(),
    )

    await act(async () => { root.unmount() })
  })

  it('collapses favorite chips beyond the bar width into an overflow menu', async () => {
    // jsdom has neither layout nor ResizeObserver — provide both. The fake
    // observer records its callbacks so the test can fire them once the bar
    // width has been stubbed, mimicking a real container resize.
    const roCallbacks: Array<() => void> = []
    class FakeResizeObserver {
      constructor(cb: () => void) { roCallbacks.push(cb) }
      observe() {}
      unobserve() {}
      disconnect() {}
    }
    vi.stubGlobal('ResizeObserver', FakeResizeObserver)
    try {
      setupDirMap({ '.': [entry('a.txt', false, 1), entry('b.txt', false, 1), entry('c.txt', false, 1)] })
      vi.mocked(loadPreference).mockImplementation((key: string) =>
        key === 'fileBrowser.favorites.proj-1.v1'
          ? Promise.resolve(JSON.stringify([{ path: 'a.txt', isDir: false }, { path: 'b.txt', isDir: false }]))
          : Promise.resolve(undefined))
      const onOpenFile = vi.fn()
      const { container, root } = renderUI({ onOpenFile })
      await flush()

      // Unmeasured (zero widths) → everything visible, no overflow button.
      expect(container.querySelectorAll('.fb-fav-chip').length).toBe(2)
      expect(container.querySelector('.fb-fav-more')).toBeNull()

      // Give the bar and chips real sizes, then trigger a re-measure by
      // favoriting another entry.
      const bar = container.querySelector('.fb-fav-bar') as HTMLElement
      Object.defineProperty(bar, 'clientWidth', { value: 200, configurable: true })
      container.querySelectorAll('.fb-fav-chip').forEach((chip) => {
        Object.defineProperty(chip, 'offsetWidth', { value: 80, configurable: true })
      })
      act(() => { roCallbacks.forEach(fire => fire()) })
      await flush()

      const row = rowByTitle(container, '.fb-grid-item', 'c.txt')
      await act(async () => {
        row.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 10, clientY: 10 }))
      })
      const favBtn = Array.from(container.querySelectorAll('.fb-context-menu-item'))
        .find(b => b.textContent?.trim() === 'Favorite') as HTMLElement
      act(() => { favBtn.click() })
      await flush()

      // budget = 200 - reserve(75) = 125 → only one 80px chip fits; rest fold.
      expect(container.querySelectorAll('.fb-fav-chip').length).toBe(1)
      const more = container.querySelector('.fb-fav-more') as HTMLElement
      expect(more.querySelector('.fb-fav-more-count')?.textContent).toBe('2')

      // The fold menu lists the hidden favorites; clicking one opens it.
      act(() => { more.click() })
      await flush()
      const items = container.querySelectorAll('.fb-fav-more-item')
      expect(items.length).toBe(2)
      act(() => { (items[1] as HTMLElement).click() })
      expect(onOpenFile).toHaveBeenCalledWith('c.txt', 'proj-1')

      await act(async () => { root.unmount() })
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('reloads per-project favorites when projectId changes without remount', async () => {
    setupDirMap({ '.': [entry('docs', true), entry('pkg', true)] })
    vi.mocked(loadPreference).mockImplementation((key: string) =>
      key === 'fileBrowser.favorites.proj-1.v1'
        ? Promise.resolve(JSON.stringify([{ path: 'docs', isDir: true }]))
        : key === 'fileBrowser.favorites.proj-2.v1'
          ? Promise.resolve(JSON.stringify([{ path: 'pkg', isDir: true }]))
          : Promise.resolve(undefined))

    // Mirror AIShellLayout: same component position, projectId prop swap.
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <FileClipboardProvider>
            <FileBrowser projectId="proj-1" />
          </FileClipboardProvider>
        </I18nProvider>,
      )
    })
    await flush()
    expect(container.querySelector('.fb-fav-chip-name')?.textContent).toBe('docs')

    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <FileClipboardProvider>
            <FileBrowser projectId="proj-2" />
          </FileClipboardProvider>
        </I18nProvider>,
      )
    })
    await flush()
    expect(container.querySelector('.fb-fav-chip-name')?.textContent).toBe('pkg')

    // A project with no persisted favorites clears the bar instead of
    // leaking the previous project's list.
    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <FileClipboardProvider>
            <FileBrowser projectId="proj-3" />
          </FileClipboardProvider>
        </I18nProvider>,
      )
    })
    await flush()
    expect(container.querySelector('.fb-fav-bar')).toBeNull()

    await act(async () => { root.unmount() })
  })

  it('self-managed mode resolves the persisted project and lists its root', async () => {
    setupDirMap({ '.': [entry('alpha.txt', false, 1)] })
    vi.mocked(loadPreference).mockImplementation((key: string) =>
      key === 'fileBrowser.activeProject.v1' ? Promise.resolve('proj-2') : Promise.resolve(undefined))

    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <FileClipboardProvider>
            <FileBrowser projects={[
              { ProjectID: 'proj-1', Name: 'Alpha', IsOpen: true },
              { ProjectID: 'proj-2', Name: 'Beta' },
            ]} />
          </FileClipboardProvider>
        </I18nProvider>,
      )
    })
    await flush()

    // Persisted choice wins over the first-open fallback.
    expect(container.querySelector('.fb-project-name')?.textContent).toBe('Beta')
    expect(listMock.mock.calls.some(([, , opts]) => (opts as { target?: string } | undefined)?.target === 'proj-2')).toBe(true)

    await act(async () => { root.unmount() })
  })

  it('self-managed mode falls back to the first open project when nothing valid is persisted', async () => {
    setupDirMap({ '.': [entry('a.txt', false, 1)] })
    // Persisted id points at a project no longer in the list.
    vi.mocked(loadPreference).mockImplementation((key: string) =>
      key === 'fileBrowser.activeProject.v1' ? Promise.resolve('deleted-proj') : Promise.resolve(undefined))

    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <FileClipboardProvider>
            <FileBrowser projects={[
              { ProjectID: 'proj-1', Name: 'Closed' },
              { ProjectID: 'proj-2', Name: 'OpenOne', IsOpen: true },
            ]} />
          </FileClipboardProvider>
        </I18nProvider>,
      )
    })
    await flush()

    expect(container.querySelector('.fb-project-name')?.textContent).toBe('OpenOne')
    expect(listMock.mock.calls.some(([, , opts]) => (opts as { target?: string } | undefined)?.target === 'proj-2')).toBe(true)

    await act(async () => { root.unmount() })
  })

  it('toolbar project picker retargets the browser, persists the choice and notifies the host', async () => {
    setupDirMap({ '.': [entry('docs', true), entry('pkg', true)] })
    vi.mocked(loadPreference).mockImplementation((key: string) =>
      key === 'fileBrowser.favorites.proj-2.v1'
        ? Promise.resolve(JSON.stringify([{ path: 'pkg', isDir: true }]))
        : Promise.resolve(undefined))

    const onProjectChange = vi.fn()
    const onOpenFile = vi.fn()
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <FileClipboardProvider>
            <FileBrowser
              projects={[
                { ProjectID: 'proj-1', Name: 'Alpha', IsOpen: true },
                { ProjectID: 'proj-2', Name: 'Beta' },
              ]}
              onProjectChange={onProjectChange}
              onOpenFile={onOpenFile}
            />
          </FileClipboardProvider>
        </I18nProvider>,
      )
    })
    await flush()
    expect(container.querySelector('.fb-project-name')?.textContent).toBe('Alpha')

    // Open the picker (menu portals to document.body).
    act(() => { (container.querySelector('.fb-project-btn') as HTMLElement).click() })
    await flush()
    const items = document.querySelectorAll<HTMLElement>('.fb-project-item')
    expect(items.length).toBe(2)

    // Switch to Beta.
    act(() => { items[1]!.click() })
    await flush()

    expect(onProjectChange).toHaveBeenCalledWith('proj-2')
    expect(vi.mocked(savePreference)).toHaveBeenCalledWith(
      'fileBrowser.activeProject.v1',
      'proj-2',
      'fb-project',
      expect.anything(),
    )
    expect(container.querySelector('.fb-project-name')?.textContent).toBe('Beta')
    expect(listMock.mock.calls.some(([, , opts]) => (opts as { target?: string } | undefined)?.target === 'proj-2')).toBe(true)
    // Per-project favorites of the newly selected project load.
    expect(container.querySelector('.fb-fav-chip-name')?.textContent).toBe('pkg')
    // Initial resolution must not have fired the host callback — only the switch.
    expect(onProjectChange).toHaveBeenCalledTimes(1)

    await act(async () => { root.unmount() })
  })

  it('self-managed mode shows a placeholder when the projects list is empty', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <FileClipboardProvider>
            <FileBrowser projects={[]} />
          </FileClipboardProvider>
        </I18nProvider>,
      )
    })
    await flush()

    expect(container.querySelector('.fb-no-project-hint')?.textContent).toBe('Open a project to browse its files')
    expect(listMock).not.toHaveBeenCalled()

    await act(async () => { root.unmount() })
  })

  it('shows the loading hint instead of the no-project placeholder while the host projects list is loading', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <FileClipboardProvider>
            <FileBrowser projects={[]} projectsLoading />
          </FileClipboardProvider>
        </I18nProvider>,
      )
    })
    await flush()

    expect(container.querySelector('.fb-no-project-hint')?.textContent).toBe('Loading…')
    expect(listMock).not.toHaveBeenCalled()

    await act(async () => { root.unmount() })
  })

  it('shows the loading hint while a non-empty projects list is still resolving its target', async () => {
    setupDirMap({ '.': [entry('a.txt', false, 1)] })
    let resolvePref: ((v: string | undefined) => void) | null = null
    vi.mocked(loadPreference).mockImplementation((key: string) => {
      if (key === 'fileBrowser.activeProject.v1') {
        return new Promise<string | undefined>(resolve => { resolvePref = resolve })
      }
      return Promise.resolve(undefined)
    })

    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <FileClipboardProvider>
            <FileBrowser projects={[{ ProjectID: 'proj-1', Name: 'Alpha', IsOpen: true }]} />
          </FileClipboardProvider>
        </I18nProvider>,
      )
    })
    await flush()

    // Persisted target unresolved: loading hint, never the no-project prompt.
    expect(container.querySelector('.fb-no-project-hint')?.textContent).toBe('Loading…')
    expect(container.querySelector('.fb-project-name')).toBeNull()
    expect(listMock).not.toHaveBeenCalled()

    await act(async () => { resolvePref?.('proj-1') })
    await flush()

    expect(container.querySelector('.fb-no-project-hint')).toBeNull()
    expect(container.querySelector('.fb-project-name')?.textContent).toBe('Alpha')
    expect(listMock).toHaveBeenCalled()

    await act(async () => { root.unmount() })
  })

  it('controlled mode renders no project picker', async () => {
    setupDirMap({ '.': [entry('a.txt', false, 1)] })
    const { container, root } = renderUI()
    await flush()
    expect(container.querySelector('.fb-project-picker')).toBeNull()
    await act(async () => { root.unmount() })
  })

  it('brackets an in-app drag session so iframe hosts can mount drop overlays', async () => {
    setupDirMap({ '.': [entry('a.txt', false, 1)] })
    const { container, root } = renderUI()
    await flush()

    const row = container.querySelector('.fb-grid-item .fb-item-hit') as HTMLElement
    fireDrag(row, 'dragstart')
    expect(getLocalFileDragActive()).toBe(true)

    fireDrag(row, 'dragend')
    expect(getLocalFileDragActive()).toBe(false)

    // The session store is global: manual begin/end also flips the flag.
    beginLocalFileDragSession()
    expect(getLocalFileDragActive()).toBe(true)
    endLocalFileDragSession()
    expect(getLocalFileDragActive()).toBe(false)

    await act(async () => { root.unmount() })
  })

  it('applies composer-space bottom padding via CSS rule', () => {
    const css = readFileSync(resolve(__dirname, 'FileBrowser.css'), 'utf-8')
    expect(css).toContain('composer-card-top')
    expect(css).toContain('composer-frame-h')
    expect(css).toMatch(/padding-bottom:\s*calc\(/)
  })

  it('moves a file into a folder via drag-and-drop in tree view', async () => {
    setupDirMap({ '.': [entry('src', true), entry('a.txt', false)] })
    const { container, root } = renderUI()
    await flush()

    const fileRow = rowByTitle(container, '.fb-tree-node-row', 'a.txt')
    const folderRow = rowByTitle(container, '.fb-tree-node-row', 'src')

    fireDrag(fileRow, 'dragstart')
    fireDrag(folderRow, 'dragover')
    fireDrag(folderRow, 'drop')
    await flush()

    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'mv', Args: ['a.txt', 'src/a.txt'] }),
      { target: 'proj-1' },
    )

    await act(async () => { root.unmount() })
  })

  it('moves a file into a folder via drag-and-drop in list view', async () => {
    setupDirMap({ '.': [entry('pkg', true), entry('note.md', false, 8)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const fileRow = rowByTitle(container, '.fb-list-row', 'note.md')
    const folderRow = rowByTitle(container, '.fb-list-row', 'pkg')

    fireDrag(fileRow, 'dragstart')
    fireDrag(folderRow, 'dragover')
    fireDrag(folderRow, 'drop')
    await flush()

    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'mv', Args: ['note.md', 'pkg/note.md'] }),
      { target: 'proj-1' },
    )

    await act(async () => { root.unmount() })
  })

  it('moves a file into a parent directory via breadcrumb drop', async () => {
    setupDirMap({ '.': [entry('sub', true)], sub: [entry('deep.md', false, 2)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    // navigate into sub
    const folderRow = rowByTitle(container, '.fb-list-row', 'sub')
    await act(async () => { folderRow.click() })
    await flush()

    const fileRow = rowByTitle(container, '.fb-list-row', 'sub/deep.md')
    const rootCrumb = container.querySelector('.fb-crumb[title=""], .fb-crumb') as HTMLElement
    expect(rootCrumb).toBeTruthy()

    fireDrag(fileRow, 'dragstart')
    fireDrag(rootCrumb, 'dragover')
    fireDrag(rootCrumb, 'drop')
    await flush()

    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'mv', Args: ['sub/deep.md', 'deep.md'] }),
      { target: 'proj-1' },
    )

    await act(async () => { root.unmount() })
  })

  it('does not move a folder into itself', async () => {
    setupDirMap({ '.': [entry('src', true)] })
    const { container, root } = renderUI()
    await flush()

    const folderRow = rowByTitle(container, '.fb-tree-node-row', 'src')

    fireDrag(folderRow, 'dragstart')
    fireDrag(folderRow, 'dragover')
    fireDrag(folderRow, 'drop')
    await flush()

    expect(shellExecMock).not.toHaveBeenCalled()
    const toast = container.querySelector('.fb-toast')
    expect(toast?.textContent).toContain('Cannot move here')

    await act(async () => { root.unmount() })
  })

  it('does not move a folder into its own descendant', async () => {
    setupDirMap({ '.': [entry('parent', true)], parent: [entry('child', true)] })
    const { container, root } = renderUI()
    await flush()

    // expand parent to reveal child
    const parentRow = rowByTitle(container, '.fb-tree-node-row', 'parent')
    await act(async () => { parentRow.click() })
    await flush()

    const childRow = rowByTitle(container, '.fb-tree-node-row', 'parent/child')

    fireDrag(parentRow, 'dragstart')
    fireDrag(childRow, 'dragover')
    fireDrag(childRow, 'drop')
    await flush()

    expect(shellExecMock).not.toHaveBeenCalled()
    const toast = container.querySelector('.fb-toast')
    expect(toast?.textContent).toContain('Cannot move here')

    await act(async () => { root.unmount() })
  })

  it('downloads a remote file via cross-panel drag-and-drop (tar.gz archive transfer)', async () => {
    setupDirMap({ '.': [entry('target', true)] })
    sshArchiveExportMock.mockResolvedValue({ Content: 'QkFTRTY0', Size: 6, NumEntries: 1 })
    projectArchiveImportMock.mockResolvedValue({ NumEntries: 1, BytesWritten: 4 })
    const { container, root } = renderUI()
    await flush()

    const folderRow = rowByTitle(container, '.fb-tree-node-row', 'target')

    // Simulate dropping a remote-origin payload (same DataTransfer for dragover+drop)
    const dt = makeDataTransfer()
    dt.setData('application/x-sporemind-file-remote', JSON.stringify({
      origin: 'remote',
      sessionId: 'sess-1',
      path: '/remote/file.txt',
      name: 'file.txt',
      isDir: false,
    }))
    for (const type of ['dragover', 'drop'] as const) {
      const evt = new Event(type, { bubbles: true, cancelable: true })
      Object.defineProperty(evt, 'dataTransfer', { value: dt, configurable: true })
      act(() => { folderRow.dispatchEvent(evt) })
    }
    await flush()

    // Export packs the remote source; import extracts into the drop target dir.
    // The archive carries the entry name, so import Path is the target dir itself.
    expect(sshArchiveExportMock).toHaveBeenCalledWith({}, { SessionId: 'sess-1', Path: '/remote/file.txt' })
    expect(projectArchiveImportMock).toHaveBeenCalledWith(
      {}, { Path: 'target', Content: 'QkFTRTY0' }, { target: 'proj-1' },
    )
    // The legacy text read/write path must NOT be used.
    expect(sshFileReadMock).not.toHaveBeenCalled()
    expect(writeMock).not.toHaveBeenCalled()

    await act(async () => { root.unmount() })
  })

  it('downloads a remote directory via cross-panel drag-and-drop (tar.gz archive transfer)', async () => {
    setupDirMap({ '.': [entry('target', true)] })
    sshArchiveExportMock.mockResolvedValue({ Content: 'QkFTRTY0=', Size: 8, NumEntries: 3 })
    projectArchiveImportMock.mockResolvedValue({ NumEntries: 3, BytesWritten: 12 })
    const { container, root } = renderUI()
    await flush()

    const folderRow = rowByTitle(container, '.fb-tree-node-row', 'target')

    const dt = makeDataTransfer()
    dt.setData('application/x-sporemind-file-remote', JSON.stringify({
      origin: 'remote', sessionId: 'sess-1', path: '/remote/dir', name: 'dir', isDir: true,
    }))
    for (const type of ['dragover', 'drop'] as const) {
      const evt = new Event(type, { bubbles: true, cancelable: true })
      Object.defineProperty(evt, 'dataTransfer', { value: dt, configurable: true })
      act(() => { folderRow.dispatchEvent(evt) })
    }
    await flush()

    // Directories are now supported: routed through archive export/import.
    expect(sshArchiveExportMock).toHaveBeenCalledWith({}, { SessionId: 'sess-1', Path: '/remote/dir' })
    expect(projectArchiveImportMock).toHaveBeenCalledWith(
      {}, { Path: 'target', Content: 'QkFTRTY0=' }, { target: 'proj-1' },
    )
    // No "unsupported" toast — the directory is accepted.
    expect(sshFileReadMock).not.toHaveBeenCalled()
    expect(writeMock).not.toHaveBeenCalled()
    const toast = container.querySelector('.fb-toast')
    expect(toast?.textContent ?? '').not.toContain('not supported')

    await act(async () => { root.unmount() })
  })

  it('imports an OS-dropped file into the current directory (list view)', async () => {
    setupDirMap({ '.': [entry('a.ts', false, 42)] })
    collectOsDropMock.mockResolvedValue([osEntry('pic.png', false, new Uint8Array([1, 2, 3, 4]))])
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const content = container.querySelector('.fb-content') as HTMLElement
    expect(content).toBeTruthy()

    const dt = osFileDataTransfer()
    fireDragEvent(content, 'dragover', dt)
    expect(dt.dropEffect).toBe('copy')
    const dropEvt = fireDragEvent(content, 'drop', dt)
    expect(dropEvt.defaultPrevented).toBe(true)
    await flush()

    // Entry normalized by collectOsDroppedEntries, root resolved via project.info,
    // bytes written through filesystem.writeBase64 with the absolute path.
    expect(collectOsDropMock).toHaveBeenCalledWith(dt)
    expect(projectInfoMock).toHaveBeenCalledWith({}, { target: 'proj-1' })
    expect(fsWriteBase64Mock).toHaveBeenCalledWith(
      {}, { Path: 'D:\\proj\\pic.png', Content: btoa('\u0001\u0002\u0003\u0004') }, // eslint-disable-line no-control-regex
    )
    // View refreshed (list re-issued for the current dir).
    const listCalls = listMock.mock.calls.filter(
      (c) => (c[1] as { Path: string }).Path === '.',
    )
    expect(listCalls.length).toBeGreaterThanOrEqual(2)
    // Success toast mentions the imported count.
    const toast = container.querySelector('.fb-toast')
    expect(toast?.textContent).toContain('1')

    await act(async () => { root.unmount() })
  })

  it('recursively creates directories and writes nested OS-dropped files (tree view, folder row drop)', async () => {
    setupDirMap({ '.': [entry('target', true)] })
    collectOsDropMock.mockResolvedValue([
      osEntry('docs', true),
      osEntry('docs/img', true),
      osEntry('docs/img/c.png', false, new Uint8Array([0x89, 0x50])),
    ])
    const { container, root } = renderUI()
    await flush()

    const folderRow = rowByTitle(container, '.fb-tree-node-row', 'target')

    const dt = osFileDataTransfer()
    const overEvt = fireDragEvent(folderRow, 'dragover', dt)
    expect(overEvt.defaultPrevented).toBe(true)
    expect(dt.dropEffect).toBe('copy')
    expect(folderRow.className).toContain('drag-over')
    fireDragEvent(folderRow, 'drop', dt)
    await flush()

    // Directories created depth-first via project shell mkdir -p.
    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'mkdir', Args: ['-p', 'target/docs'] }),
      { target: 'proj-1' },
    )
    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'mkdir', Args: ['-p', 'target/docs/img'] }),
      { target: 'proj-1' },
    )
    // Nested file written under the dropped folder with the absolute path.
    expect(fsWriteBase64Mock).toHaveBeenCalledWith(
      {}, { Path: 'D:\\proj\\target\\docs\\img\\c.png', Content: expect.any(String) },
    )
    // No internal move happened.
    expect(shellExecMock).not.toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'mv' }),
      expect.anything(),
    )

    await act(async () => { root.unmount() })
  })

  it('reports failures instead of aborting when an OS-dropped file write fails', async () => {
    setupDirMap({ '.': [entry('a.ts', false, 42)] })
    collectOsDropMock.mockResolvedValue([
      osEntry('ok.txt', false, new Uint8Array([111])),
      osEntry('bad.bin', false, new Uint8Array([1])),
    ])
    fsWriteBase64Mock.mockImplementation((_c: unknown, req: { Path: string }) =>
      req.Path.endsWith('bad.bin') ? Promise.reject(new Error('write denied')) : Promise.resolve({}),
    )
    const { container, root } = renderUI()
    await flush()

    const content = container.querySelector('.fb-content') as HTMLElement
    fireDragEvent(content, 'drop', osFileDataTransfer())
    await flush()

    expect(fsWriteBase64Mock).toHaveBeenCalledTimes(2)
    const toast = container.querySelector('.fb-toast')
    expect(toast?.textContent).toContain('1')

    await act(async () => { root.unmount() })
  })

  it('Alt+drag on desktop starts the native OS drag-out and suppresses the HTML5 drag', async () => {
    isWailsMock.mockReturnValue(true)
    setupDirMap({ '.': [entry('a.txt', false), entry('src', true)] })
    const { container, root } = renderUI()
    await flush()

    // Desktop rows advertise the Alt drag-out hint in their tooltip.
    const fileRow = container.querySelector('.fb-tree-node-row[title^="a.txt"]') as HTMLElement
    expect(fileRow).toBeTruthy()
    expect(fileRow.getAttribute('title')).toContain('Alt')

    const dt = makeDataTransfer()
    const evt = fireDragEvent(fileRow, 'dragstart', dt, { altKey: true })
    await flush()

    expect(evt.defaultPrevented).toBe(true)
    expect(osDragOutMock).toHaveBeenCalledWith([{ kind: 'local', projectId: 'proj-1', relPath: 'a.txt' }])
    // In-app HTML5 payload was not attached (no internal move semantics).
    expect(dt.types).toEqual([])
    expect(dt.effectAllowed).not.toBe('copyMove')

    await act(async () => { root.unmount() })
  })

  it('Alt+drag off desktop keeps the normal in-app drag semantics', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('src', true)] })
    const { container, root } = renderUI()
    await flush()

    const fileRow = rowByTitle(container, '.fb-tree-node-row', 'a.txt')
    // No desktop hint in the tooltip.
    expect(fileRow.getAttribute('title')).toBe('a.txt')

    const dt = makeDataTransfer()
    const evt = fireDragEvent(fileRow, 'dragstart', dt, { altKey: true })
    await flush()

    expect(evt.defaultPrevented).toBe(false)
    expect(osDragOutMock).not.toHaveBeenCalled()
    // Normal in-app payload still attached.
    expect(dt.types).toContain('text/plain')
    expect(dt.types).toContain('application/x-sporemind-file-local')

    await act(async () => { root.unmount() })
  })

  it('context menu shows Copy, Cut, and Paste items (Paste appears after clipboard is populated)', async () => {
    setupDirMap({ '.': [entry('note.md', false, 8), entry('sub', true)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    // Right-click a file → Copy and Cut present, but Paste absent (clipboard empty)
    const fileRow = rowByTitle(container, '.fb-list-row', 'note.md')
    await act(async () => {
      fileRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })
    let menu = container.querySelector('.fb-context-menu')!
    let labels = Array.from(menu.querySelectorAll('.fb-context-menu-item')).map(b => b.textContent?.trim())
    expect(labels).toContain('Copy')
    expect(labels).toContain('Cut')
    expect(labels).not.toContain('Paste')

    // Click Cut → menu closes, clipboard now has the entry
    const cutBtn = Array.from(menu.querySelectorAll('.fb-context-menu-item')).find(b => b.textContent?.trim() === 'Cut') as HTMLElement
    act(() => { cutBtn.click() })
    await flush()
    expect(container.querySelector('.fb-context-menu')).toBeNull() // menu closed

    // Right-click a folder → Paste now visible (canPaste: isDir=true, clipboard non-null)
    const folderRow = rowByTitle(container, '.fb-list-row', 'sub')
    await act(async () => {
      folderRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 200, clientY: 200 }))
    })
    menu = container.querySelector('.fb-context-menu')!
    labels = Array.from(menu.querySelectorAll('.fb-context-menu-item')).map(b => b.textContent?.trim())
    expect(labels).toContain('Copy')
    expect(labels).toContain('Cut')
    expect(labels).toContain('Paste')

    await act(async () => { root.unmount() })
  })

  it('copy then paste triggers cp -r', async () => {
    setupDirMap({ '.': [entry('note.md', false, 8), entry('sub', true)] })
    const { container, root } = renderUI()
    await flush()

    // Make test -e return non-zero (dest doesn't exist) so paste proceeds
    shellExecMock.mockImplementation((_c: unknown, req: { Command: string }) => {
      if (req.Command === 'test') return asyncIterator([{ ExitCode: 1 }])
      return asyncIterator([{ ExitCode: 0 }])
    })

    clickView(container, 'List view')
    await flush()

    // Right-click note.md → Copy
    const fileRow = rowByTitle(container, '.fb-list-row', 'note.md')
    await act(async () => {
      fileRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })
    const copyBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Copy',
    ) as HTMLElement
    act(() => { copyBtn.click() })
    await flush()

    // Right-click sub → Paste
    const folderRow = rowByTitle(container, '.fb-list-row', 'sub')
    await act(async () => {
      folderRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 200, clientY: 200 }))
    })
    const pasteBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Paste',
    ) as HTMLElement
    act(() => { pasteBtn.click() })
    await flush()

    // Pasted file is the source basename under the target dir: note.md → sub/note.md
    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'cp', Args: ['-r', 'note.md', 'sub/note.md'] }),
      { target: 'proj-1' },
    )

    await act(async () => { root.unmount() })
  })

  it('cut then paste triggers mv', async () => {
    setupDirMap({ '.': [entry('note.md', false, 8), entry('sub', true)] })
    const { container, root } = renderUI()
    await flush()

    shellExecMock.mockImplementation((_c: unknown, req: { Command: string }) => {
      if (req.Command === 'test') return asyncIterator([{ ExitCode: 1 }])
      return asyncIterator([{ ExitCode: 0 }])
    })

    clickView(container, 'List view')
    await flush()

    // Right-click note.md → Cut
    const fileRow = rowByTitle(container, '.fb-list-row', 'note.md')
    await act(async () => {
      fileRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })
    const cutBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Cut',
    ) as HTMLElement
    act(() => { cutBtn.click() })
    await flush()

    // Right-click sub → Paste
    const folderRow = rowByTitle(container, '.fb-list-row', 'sub')
    await act(async () => {
      folderRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 200, clientY: 200 }))
    })
    const pasteBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Paste',
    ) as HTMLElement
    act(() => { pasteBtn.click() })
    await flush()

    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'mv', Args: ['note.md', 'sub/note.md'] }),
      { target: 'proj-1' },
    )

    await act(async () => { root.unmount() })
  })

  it('calls onPathChange with initial path on mount', async () => {
    setupDirMap({ '.': [entry('doc.md', false, 5)] })
    const onPathChange = vi.fn()
    const { root } = renderUI({ onPathChange })
    await flush()
    expect(onPathChange).toHaveBeenCalledWith('.')
    await act(async () => { root.unmount() })
  })

  it('calls onPathChange when navigating into a folder', async () => {
    setupDirMap({ '.': [entry('sub', true)], sub: [entry('deep.md', false, 2)] })
    const onPathChange = vi.fn()
    const { container, root } = renderUI({ onPathChange })
    await flush()
    onPathChange.mockClear() // clear initial mount call

    clickView(container, 'List view')
    await flush()
    onPathChange.mockClear() // clear any mount-related calls

    const folderRow = container.querySelector('.fb-list-row .fb-item-hit') as HTMLElement
    expect(folderRow).toBeTruthy()
    await act(async () => { folderRow.click() })
    await flush()

    expect(onPathChange).toHaveBeenCalledWith('sub')
    await act(async () => { root.unmount() })
  })

  it('calls onSelectionChange when selecting a file', async () => {
    setupDirMap({ '.': [entry('hello.ts', false, 50)] })
    const onSelectionChange = vi.fn()
    const { container, root } = renderUI({ onSelectionChange })
    await flush()
    onSelectionChange.mockClear() // clear initial mount call with null

    clickView(container, 'List view')
    await flush()
    onSelectionChange.mockClear()

    const row = container.querySelector('.fb-list-row .fb-item-hit') as HTMLElement
    expect(row).toBeTruthy()
    await act(async () => { row.click() })

    // Selecting a file calls onSelectionChange with the file path
    expect(onSelectionChange).toHaveBeenCalledWith('hello.ts')
    await act(async () => { root.unmount() })
  })

  it('calls onSelectionChange when clicking a folder and then selecting null', async () => {
    setupDirMap({ '.': [entry('sub', true)], sub: [entry('deep.md', false, 2)] })
    const onSelectionChange = vi.fn()
    const { container, root } = renderUI({ onSelectionChange })
    await flush()
    onSelectionChange.mockClear() // clear initial mount call with null

    clickView(container, 'List view')
    await flush()
    onSelectionChange.mockClear()

    const folderRow = container.querySelector('.fb-list-row .fb-item-hit') as HTMLElement
    expect(folderRow).toBeTruthy()
    await act(async () => { folderRow.click() })
    await flush()

    // Clicking a folder first selects it, then navigateToWithHistory resets to null
    // The final call should be null
    expect(onSelectionChange).toHaveBeenLastCalledWith(null)
    await act(async () => { root.unmount() })
  })

  it('cut row shows the cut-source visual hint (opacity: 0.5)', async () => {
    setupDirMap({ '.': [entry('note.md', false, 8), entry('sub', true)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    // Right-click note.md → Cut
    const fileRow = rowByTitle(container, '.fb-list-row', 'note.md')
    const listRow = fileRow.closest('.fb-list-row') as HTMLElement
    // Row should not yet have cut-source class
    expect(listRow.className).not.toContain('cut-source')

    await act(async () => {
      fileRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })
    const cutBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Cut',
    ) as HTMLElement
    act(() => { cutBtn.click() })
    await flush()

    // After cut, the row is now marked with cut-source class
    expect(listRow.className).toContain('cut-source')

    // The CSS rule applies opacity: 0.5 to cut-source rows
    const css = readFileSync(resolve(__dirname, 'FileBrowser.css'), 'utf-8')
    expect(css).toContain('.fb-list-row.cut-source')
    expect(css).toContain('opacity: 0.5')

    await act(async () => { root.unmount() })
  })

  /* ===================== marquee + modifier multi-select ===================== */

  it('marquee drag over rows selects the intersecting rows (replace)', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false), entry('c.txt', false)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rows = Array.from(container.querySelectorAll('.fb-list-row')) as HTMLElement[]
    expect(rows.length).toBe(3)
    // rows carry the marquee opt-in attribute with the full path
    expect(rows[0]!.querySelector('.fb-item-hit')?.getAttribute('data-mq-path')).toBe('a.txt')

    const main = stubMarqueeLayout(container)
    // drag from empty area below the rows up over all three rows
    marqueeDrag(main, { x: 10, y: 200 }, { x: 300, y: 12 })
    await flush()

    expect(rows[0]!.className).toContain('selected')
    expect(rows[1]!.className).toContain('selected')
    expect(rows[2]!.className).toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('marquee click on empty area clears the selection', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rows = Array.from(container.querySelectorAll('.fb-list-row')) as HTMLElement[]
    const main = stubMarqueeLayout(container)
    marqueeDrag(main, { x: 10, y: 200 }, { x: 300, y: 12 })
    await flush()
    expect(rows[0]!.className).toContain('selected')

    // plain click (no move) on the empty area below the rows
    marqueeDrag(main, { x: 10, y: 200 }, { x: 10, y: 200 })
    await flush()
    expect(rows[0]!.className).not.toContain('selected')
    expect(rows[1]!.className).not.toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('Alt+marquee subtracts the hit rows from the current selection', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false), entry('c.txt', false)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rows = Array.from(container.querySelectorAll('.fb-list-row')) as HTMLElement[]
    const main = stubMarqueeLayout(container)

    // select all three via replace marquee…
    marqueeDrag(main, { x: 10, y: 200 }, { x: 300, y: 12 })
    await flush()
    expect(rows.every(r => r.className.includes('selected'))).toBe(true)

    // …then Alt+marquee over rows b (64-88) and c (88-112) to subtract them
    marqueeDrag(main, { x: 10, y: 100 }, { x: 300, y: 64 }, { alt: true })
    await flush()

    expect(rows[0]!.className).toContain('selected')
    expect(rows[1]!.className).not.toContain('selected')
    expect(rows[2]!.className).not.toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('Ctrl+marquee unions the hit rows with the current selection', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false), entry('c.txt', false)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rows = Array.from(container.querySelectorAll('.fb-list-row')) as HTMLElement[]
    const main = stubMarqueeLayout(container)

    // select a only (drag over the 40-64 band of row a)
    marqueeDrag(main, { x: 10, y: 40 }, { x: 300, y: 64 })
    await flush()
    expect(rows[0]!.className).toContain('selected')
    expect(rows[1]!.className).not.toContain('selected')

    // Ctrl+marquee over c (88-112) adds it
    marqueeDrag(main, { x: 10, y: 88 }, { x: 300, y: 112 }, { ctrl: true })
    await flush()

    expect(rows[0]!.className).toContain('selected')
    expect(rows[1]!.className).not.toContain('selected')
    expect(rows[2]!.className).toContain('selected')

    await act(async () => { root.unmount() })
  })

  /* ===================== controlled mode (AIShellLayout wiring) ===================== */

  it('controlled: marquee from an empty selection keeps the multi-selection (no anchor-echo collapse)', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false), entry('c.txt', false)] })
    const { container, root } = renderControlled()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rows = Array.from(container.querySelectorAll('.fb-list-row')) as HTMLElement[]
    const main = stubMarqueeLayout(container)

    // marquee from NO prior selection — the onSelectionChange echo must not
    // collapse the fresh multi-selection back to the single anchor path
    marqueeDrag(main, { x: 10, y: 200 }, { x: 300, y: 12 })
    await flush()

    expect(rows[0]!.className).toContain('selected')
    expect(rows[1]!.className).toContain('selected')
    expect(rows[2]!.className).toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('controlled: Ctrl+click union survives the anchor echo', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false)] })
    const { container, root } = renderControlled()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rows = Array.from(container.querySelectorAll('.fb-list-row')) as HTMLElement[]
    const hitA = rows[0]!.querySelector('.fb-item-hit') as HTMLElement
    const hitB = rows[1]!.querySelector('.fb-item-hit') as HTMLElement

    act(() => { hitA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true })) })
    await flush()
    expect(rows[0]!.className).toContain('selected')

    act(() => { hitB.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    expect(rows[0]!.className).toContain('selected')
    expect(rows[1]!.className).toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('controlled: plain click on empty area clears the entire multi-selection', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false), entry('c.txt', false)] })
    const { container, root } = renderControlled()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rows = Array.from(container.querySelectorAll('.fb-list-row')) as HTMLElement[]
    const hits = rows.map(r => r.querySelector('.fb-item-hit') as HTMLElement)
    const main = stubMarqueeLayout(container)

    // build a multi-selection: ctrl+click a then b
    act(() => { hits[0]!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true })) })
    await flush()
    act(() => { hits[1]!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()
    expect(rows[0]!.className).toContain('selected')
    expect(rows[1]!.className).toContain('selected')

    // plain click on empty area below the rows must clear ALL
    marqueeDrag(main, { x: 10, y: 200 }, { x: 10, y: 200 })
    await flush()

    expect(rows[0]!.className).not.toContain('selected')
    expect(rows[1]!.className).not.toContain('selected')
    expect(rows[2]!.className).not.toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('list rows show the last-modified time converted from UTC to local time', async () => {
    setupDirMap({ '.': [entry('a.txt', false, 10), entry('b', true)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const mtimes = Array.from(container.querySelectorAll('.fb-list-mtime')) as HTMLElement[]
    expect(mtimes.length).toBe(2)
    expect(mtimes[0]!.textContent).toBe(new Date('2026-01-01T00:00:00Z').toLocaleString(
      undefined,
      { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' },
    ))
    expect(mtimes[0]!.textContent).not.toMatch(/:\d\d:\d\d/)
    // mtime column sits right of the size column on the file row
    const sizeEl = container.querySelector('.fb-list-size') as HTMLElement
    const fileRow = sizeEl.closest('.fb-list-row') as HTMLElement
    const fileMtime = fileRow.querySelector('.fb-list-mtime') as HTMLElement
    expect(fileMtime).toBeTruthy()
    expect(sizeEl.compareDocumentPosition(fileMtime)).toBe(Node.DOCUMENT_POSITION_FOLLOWING)

    await act(async () => { root.unmount() })
  })

  it('ctrl+click on the size column/right side of a row selects the item (no dead zone)', async () => {
    setupDirMap({ '.': [entry('a.txt', false, 10), entry('b.txt', false, 20)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rows = Array.from(container.querySelectorAll('.fb-list-row')) as HTMLElement[]
    const hitA = rows[0]!.querySelector('.fb-item-hit') as HTMLElement
    const sizeB = rows[1]!.querySelector('.fb-list-size') as HTMLElement
    expect(sizeB).toBeTruthy()

    act(() => { hitA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true })) })
    await flush()

    // the size column is inside the hit element: ctrl+click unions, it must not
    // fall through to the empty-area marquee handler that clears the selection
    act(() => { sizeB.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    expect(rows[0]!.className).toContain('selected')
    expect(rows[1]!.className).toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('press+release on a row (size column) does not start a marquee or clear the selection', async () => {
    setupDirMap({ '.': [entry('a.txt', false, 10), entry('b.txt', false, 20)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rows = Array.from(container.querySelectorAll('.fb-list-row')) as HTMLElement[]
    const hitA = rows[0]!.querySelector('.fb-item-hit') as HTMLElement
    const sizeB = rows[1]!.querySelector('.fb-list-size') as HTMLElement

    act(() => { hitA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true })) })
    await flush()
    expect(rows[0]!.className).toContain('selected')

    // mousedown+mouseup on the right part of row b (no movement): must be inert,
    // not treated as an empty-area click that wipes the selection
    act(() => { sizeB.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true, button: 0, clientX: 380, clientY: 70 })) })
    act(() => { document.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, cancelable: true })) })
    await flush()

    expect(rows[0]!.className).toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('Alt+click removes one row from the multi-selection', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false), entry('c.txt', false)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rows = Array.from(container.querySelectorAll('.fb-list-row')) as HTMLElement[]
    const rowA = rowByTitle(container, '.fb-list-row', 'a.txt')
    const rowB = rowByTitle(container, '.fb-list-row', 'b.txt')

    // Ctrl+click a and b
    act(() => { rowA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    act(() => { rowB.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()
    expect(rows[0]!.className).toContain('selected')
    expect(rows[1]!.className).toContain('selected')

    // Alt+click a subtracts it
    act(() => { rowA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, altKey: true })) })
    await flush()
    expect(rows[0]!.className).not.toContain('selected')
    expect(rows[1]!.className).toContain('selected')
    expect(rows[2]!.className).not.toContain('selected')

    await act(async () => { root.unmount() })
  })

  /* ===================== multi-select delete ===================== */

  it('multi-select delete asks once with a count and removes every selected path', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false), entry('sub', true)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rowA = rowByTitle(container, '.fb-list-row', 'a.txt')
    const rowB = rowByTitle(container, '.fb-list-row', 'b.txt')
    act(() => { rowA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    act(() => { rowB.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    // Right-click one of the selected rows → Delete
    await act(async () => {
      rowA.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })
    const delBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Delete',
    ) as HTMLElement
    act(() => { delBtn.click() })
    await flush()

    // Dialog is portaled to body and carries the multi-select count
    const dialog = document.querySelector('.fb-dialog') as HTMLElement
    expect(dialog).toBeTruthy()
    expect(dialog.textContent).toContain('Delete 2 Items')
    expect(dialog.textContent).toContain('a.txt')
    expect(dialog.textContent).toContain('other')

    // Confirm → every selected path is removed with Recursive semantics
    rmMock.mockResolvedValue({})
    await act(async () => {
      (dialog as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })
    await flush()

    expect(rmMock).toHaveBeenCalledTimes(2)
    const paths = rmMock.mock.calls.map(c => (c[1] as { Path: string }).Path)
    expect(paths).toEqual(expect.arrayContaining(['a.txt', 'b.txt']))
    for (const call of rmMock.mock.calls) {
      expect((call[1] as { Recursive: boolean }).Recursive).toBe(true)
      expect((call[1] as { Confirm: boolean }).Confirm).toBe(true)
      expect((call[1] as { Force: boolean }).Force).toBe(true)
    }

    // selection was pruned
    const rows = Array.from(container.querySelectorAll('.fb-list-row')) as HTMLElement[]
    expect(rows[0]!.className).not.toContain('selected')
    expect(rows[1]!.className).not.toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('single-entry delete keeps the name-based dialog and passes the single path', async () => {
    setupDirMap({ '.': [entry('solo.txt', false)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const row = rowByTitle(container, '.fb-list-row', 'solo.txt')
    await act(async () => {
      row.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 50, clientY: 50 }))
    })
    const delBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Delete',
    ) as HTMLElement
    act(() => { delBtn.click() })
    await flush()

    const dialog = document.querySelector('.fb-dialog') as HTMLElement
    expect(dialog).toBeTruthy()
    expect(dialog.querySelector('.fb-dialog-title')?.textContent).toBe('Delete')
    expect(dialog.textContent).toContain('solo.txt')

    await act(async () => {
      (dialog as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })
    await flush()

    expect(rmMock).toHaveBeenCalledTimes(1)
    expect((rmMock.mock.calls[0]![1] as { Path: string }).Path).toBe('solo.txt')

    await act(async () => { root.unmount() })
  })

  it('new-folder failure surfaces the mkdir stderr detail in the toast', async () => {
    setupDirMap({ '.': [entry('apps', false)] })
    const { container, root } = renderUI()
    await flush()

    // Toolbar "New Folder" opens the prompt for the current dir.
    clickView(container, 'New Folder')
    await flush()

    const dialog = document.querySelector('.fb-dialog') as HTMLElement
    expect(dialog).toBeTruthy()
    const input = dialog.querySelector('.fb-dialog-input') as HTMLInputElement
    const setInput = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    act(() => { setInput.call(input, 'apps') })
    act(() => { input.dispatchEvent(new Event('input', { bubbles: true })) })

    // mkdir -p apps fails because a same-name file already exists.
    shellExecMock.mockReturnValue(asyncIterator([
      { ExitCode: 1, Stderr: "mkdir: cannot create directory 'apps': File exists" },
    ]))
    await act(async () => {
      (dialog as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })
    await flush()

    const toast = container.querySelector('.fb-toast')
    expect(toast?.textContent).toContain('Cannot create folder')
    expect(toast?.textContent).toContain('File exists')

    await act(async () => { root.unmount() })
  })

  it('deleting cut sources clears their cut-source highlight and clipboard entries', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false), entry('keep.txt', false)] })
    const { container, root } = renderUI()
    await flush()

    clickView(container, 'List view')
    await flush()

    const rowA = rowByTitle(container, '.fb-list-row', 'a.txt')
    const rowB = rowByTitle(container, '.fb-list-row', 'b.txt')
    const listRowA = rowA.closest('.fb-list-row') as HTMLElement
    const listRowB = rowB.closest('.fb-list-row') as HTMLElement
    act(() => { rowA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    act(() => { rowB.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    // Cut both → both rows highlighted as cut sources
    await act(async () => {
      rowA.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })
    const cutBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Cut',
    ) as HTMLElement
    act(() => { cutBtn.click() })
    await flush()
    expect(listRowA.className).toContain('cut-source')
    expect(listRowB.className).toContain('cut-source')

    // Delete the cut sources (multi) → cut-source highlight must disappear even
    // though the (mocked) listing still renders the rows
    await act(async () => {
      rowA.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })
    const delBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Delete',
    ) as HTMLElement
    act(() => { delBtn.click() })
    await flush()
    await act(async () => {
      (document.querySelector('form.fb-dialog') as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })
    await flush()

    expect(listRowA.className).not.toContain('cut-source')
    expect(listRowB.className).not.toContain('cut-source')

    await act(async () => { root.unmount() })
  })

  /* ===================== multi-select copy/cut + paste ===================== */

  it('multi cut then paste moves each selected path into the target directory', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false), entry('sub', true)] })
    const { container, root } = renderUI()
    await flush()

    shellExecMock.mockImplementation((_c: unknown, req: { Command: string }) => {
      if (req.Command === 'test') return asyncIterator([{ ExitCode: 1 }])
      return asyncIterator([{ ExitCode: 0 }])
    })

    clickView(container, 'List view')
    await flush()

    const rowA = rowByTitle(container, '.fb-list-row', 'a.txt')
    const rowB = rowByTitle(container, '.fb-list-row', 'b.txt')
    act(() => { rowA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    act(() => { rowB.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    await act(async () => {
      rowA.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })
    const cutBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Cut',
    ) as HTMLElement
    act(() => { cutBtn.click() })
    await flush()

    // Right-click the directory (not part of the selection) → Paste
    const folderRow = rowByTitle(container, '.fb-list-row', 'sub')
    await act(async () => {
      folderRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 200, clientY: 200 }))
    })
    const pasteBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Paste',
    ) as HTMLElement
    act(() => { pasteBtn.click() })
    await flush()

    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'mv', Args: ['a.txt', 'sub/a.txt'] }),
      { target: 'proj-1' },
    )
    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'mv', Args: ['b.txt', 'sub/b.txt'] }),
      { target: 'proj-1' },
    )

    await act(async () => { root.unmount() })
  })

  it('multi copy then paste copies each selected path (files + directories)', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('pkg', true), entry('sub', true)] })
    const { container, root } = renderUI()
    await flush()

    shellExecMock.mockImplementation((_c: unknown, req: { Command: string }) => {
      if (req.Command === 'test') return asyncIterator([{ ExitCode: 1 }])
      return asyncIterator([{ ExitCode: 0 }])
    })

    clickView(container, 'List view')
    await flush()

    const rowA = rowByTitle(container, '.fb-list-row', 'a.txt')
    const rowPkg = rowByTitle(container, '.fb-list-row', 'pkg')
    act(() => { rowA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    act(() => { rowPkg.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    await act(async () => {
      rowA.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })
    const copyBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Copy',
    ) as HTMLElement
    act(() => { copyBtn.click() })
    await flush()

    const folderRow = rowByTitle(container, '.fb-list-row', 'sub')
    await act(async () => {
      folderRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 200, clientY: 200 }))
    })
    const pasteBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Paste',
    ) as HTMLElement
    act(() => { pasteBtn.click() })
    await flush()

    // both the file and the directory are copied with -r
    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'cp', Args: ['-r', 'a.txt', 'sub/a.txt'] }),
      { target: 'proj-1' },
    )
    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'cp', Args: ['-r', 'pkg', 'sub/pkg'] }),
      { target: 'proj-1' },
    )

    await act(async () => { root.unmount() })
  })

  it('multi paste continues past a per-item failure and reports the failed names', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false), entry('sub', true)] })
    const { container, root } = renderUI()
    await flush()

    // b.txt fails to copy; test -e says "no conflict"; everything else succeeds
    shellExecMock.mockImplementation((_c: unknown, req: { Command: string; Args: string[] }) => {
      if (req.Command === 'test') return asyncIterator([{ ExitCode: 1 }])
      if (req.Command === 'cp' && req.Args[1] === 'b.txt') return asyncIterator([{ ExitCode: 1 }])
      return asyncIterator([{ ExitCode: 0 }])
    })

    clickView(container, 'List view')
    await flush()

    const rowA = rowByTitle(container, '.fb-list-row', 'a.txt')
    const rowB = rowByTitle(container, '.fb-list-row', 'b.txt')
    act(() => { rowA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    act(() => { rowB.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    await act(async () => {
      rowA.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 }))
    })
    const copyBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Copy',
    ) as HTMLElement
    act(() => { copyBtn.click() })
    await flush()

    const folderRow = rowByTitle(container, '.fb-list-row', 'sub')
    await act(async () => {
      folderRow.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 200, clientY: 200 }))
    })
    const pasteBtn = Array.from(container.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Paste',
    ) as HTMLElement
    act(() => { pasteBtn.click() })
    await flush()

    // a.txt copied, b.txt attempted but failed → the batch did not abort
    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'cp', Args: ['-r', 'a.txt', 'sub/a.txt'] }),
      { target: 'proj-1' },
    )
    expect(shellExecMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Command: 'cp', Args: ['-r', 'b.txt', 'sub/b.txt'] }),
      { target: 'proj-1' },
    )
    const toast = container.querySelector('.fb-toast')
    expect(toast?.textContent).toContain('b.txt')

    await act(async () => { root.unmount() })
  })

  /* ===================== tree multi-select ===================== */

  it('Ctrl+click in the tree extends the selection without navigating', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false), entry('sub', true)] })
    const { container, root } = renderUI()
    await flush()

    const treeA = rowByTitle(container, '.fb-tree-node-row', 'a.txt')
    const treeB = rowByTitle(container, '.fb-tree-node-row', 'b.txt')

    act(() => { treeA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    act(() => { treeB.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    expect(treeA.className).toContain('selected')
    expect(treeB.className).toContain('selected')
    // No navigation happened: breadcrumb still at root
    const crumbs = Array.from(container.querySelectorAll('.fb-crumb'))
    expect(crumbs.at(-1)?.textContent).toBe('~')

    await act(async () => { root.unmount() })
  })

  it('Alt+click in the tree subtracts from the selection', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false)] })
    const { container, root } = renderUI()
    await flush()

    const treeA = rowByTitle(container, '.fb-tree-node-row', 'a.txt')
    const treeB = rowByTitle(container, '.fb-tree-node-row', 'b.txt')

    act(() => { treeA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    act(() => { treeB.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()
    expect(treeA.className).toContain('selected')
    expect(treeB.className).toContain('selected')

    act(() => { treeA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, altKey: true })) })
    await flush()
    expect(treeA.className).not.toContain('selected')
    expect(treeB.className).toContain('selected')

    await act(async () => { root.unmount() })
  })

  it('tree multi-selection feeds the context menu count and multi-delete', async () => {
    setupDirMap({ '.': [entry('a.txt', false), entry('b.txt', false)] })
    const { container, root } = renderUI()
    await flush()

    const treeA = rowByTitle(container, '.fb-tree-node-row', 'a.txt')
    const treeB = rowByTitle(container, '.fb-tree-node-row', 'b.txt')
    act(() => { treeA.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    act(() => { treeB.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true })) })
    await flush()

    await act(async () => {
      treeA.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 80, clientY: 80 }))
    })
    const menu = container.querySelector('.fb-context-menu')!
    expect(menu.querySelector('.fb-context-menu-title')?.textContent).toContain('a.txt (+1)')

    const delBtn = Array.from(menu.querySelectorAll('.fb-context-menu-item')).find(
      b => b.textContent?.trim() === 'Delete',
    ) as HTMLElement
    act(() => { delBtn.click() })
    await flush()

    const dialog = document.querySelector('.fb-dialog') as HTMLElement
    expect(dialog).toBeTruthy()
    expect(dialog.textContent).toContain('Delete 2 Items')

    rmMock.mockResolvedValue({})
    await act(async () => {
      (dialog as HTMLFormElement).dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })
    await flush()

    expect(rmMock).toHaveBeenCalledTimes(2)
    const paths = rmMock.mock.calls.map(c => (c[1] as { Path: string }).Path)
    expect(paths).toEqual(expect.arrayContaining(['a.txt', 'b.txt']))

    await act(async () => { root.unmount() })
  })
})
