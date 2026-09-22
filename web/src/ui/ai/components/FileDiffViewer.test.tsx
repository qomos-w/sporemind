import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { I18nProvider } from '../../../i18n'
import { FileDiffViewer } from './FileDiffViewer'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../application/generated-client', () => ({ client: {} }))

// Default (no project) fallback path — kept mocked so importing the real
// generated client never happens in tests.
vi.mock('../../../gen-clients/filesystem/client', () => ({
  read: vi.fn(),
  write: vi.fn(),
}))

const readMock = vi.fn()
const writeMock = vi.fn()
const onFileChangedMock = vi.fn()
const watchFileMock = vi.fn()

vi.mock('../../../gen-clients/project/client', () => ({
  OnFileChanged: (...args: unknown[]) => onFileChangedMock(...args),
  watchFile: (...args: unknown[]) => watchFileMock(...args),
  read: (...args: unknown[]) => readMock(...args),
  write: (...args: unknown[]) => writeMock(...args),
}))

vi.mock('../../editor/HexViewer', () => ({
  HexViewer: ({ filePath }: { filePath: string }) => <div data-testid="hex-viewer">{filePath}</div>,
}))

vi.mock('./CodeMirrorViewer', () => ({
  CodeMirrorViewer: ({ content }: { content: string }) => <div data-testid="codemirror-viewer">{content}</div>,
}))

function renderFDV(props: Record<string, unknown> = {}) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <FileDiffViewer filePath="/test/file.txt" projectId="proj-1" {...props} />
      </I18nProvider>,
    )
  })
  return { container, root, unmount: () => act(() => { root.unmount(); document.body.removeChild(container) }) }
}

async function flush() {
  await act(async () => { await new Promise(r => setTimeout(r, 0)) })
}

describe('FileDiffViewer project-scoped reads', () => {
  beforeEach(() => {
    readMock.mockReset()
    writeMock.mockReset()
    onFileChangedMock.mockReset()
    watchFileMock.mockReset()

    readMock.mockResolvedValue({ Content: 'hello world this is text content' })
    onFileChangedMock.mockReturnValue(() => {})
    watchFileMock.mockResolvedValue(undefined)
  })

  it('reads the file through the owning project actor so a relative path respects the project root', async () => {
    const { container, unmount } = renderFDV({ filePath: 'src/foo.ts' })
    await flush()

    expect(readMock).toHaveBeenCalledTimes(1)
    expect(readMock).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Path: 'src/foo.ts' }),
      expect.objectContaining({ target: 'proj-1' }),
    )
    expect(container.querySelector('[data-testid="codemirror-viewer"]')?.textContent).toBe('hello world this is text content')
    unmount()
  })

  it('falls back to hex mode when the read reports a binary file', async () => {
    readMock.mockRejectedValue(new Error('binary file: content contains NUL byte'))

    const { container, unmount } = renderFDV()
    await flush()

    expect(readMock).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[data-testid="hex-viewer"]')).toBeTruthy()
    unmount()
  })

  it('discards a stale load when the file path changes mid-flight', async () => {
    // File A's read stays pending while we switch to file B.
    let resolveFirstRead!: (v: unknown) => void
    const firstRead = new Promise(r => { resolveFirstRead = r })
    readMock.mockImplementationOnce(() => firstRead)
    readMock.mockResolvedValueOnce({ Content: 'content of file B' })

    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <FileDiffViewer filePath="/a.txt" projectId="proj-1" />
        </I18nProvider>,
      )
    })
    act(() => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <FileDiffViewer filePath="/b.txt" projectId="proj-1" />
        </I18nProvider>,
      )
    })
    await flush()

    expect(readMock).toHaveBeenCalledTimes(2)
    expect(container.querySelector('[data-testid="codemirror-viewer"]')?.textContent).toBe('content of file B')

    // Resolving the old file's read must not clobber file B's content.
    resolveFirstRead({ Content: 'stale content of file A' })
    await flush()

    expect(container.querySelector('[data-testid="codemirror-viewer"]')?.textContent).toBe('content of file B')

    act(() => { root.unmount(); document.body.removeChild(container) })
  })
})

describe('FileDiffViewer HTML preview', () => {
  beforeEach(() => {
    readMock.mockReset()
    writeMock.mockReset()
    onFileChangedMock.mockReset()
    watchFileMock.mockReset()

    readMock.mockImplementation(async (_c: unknown, req: { Path: string }) => {
      if (req.Path === 'page/index.html') {
        return { Content: '<html><head><link rel="stylesheet" href="style.css"></head><body><script src="app.js"></script></body></html>' }
      }
      if (req.Path === 'page/style.css') return { Content: 'body{color:red}' }
      if (req.Path === 'page/app.js') return { Content: 'console.log(1)' }
      throw new Error(`unexpected read: ${req.Path}`)
    })
    onFileChangedMock.mockReturnValue(() => {})
    watchFileMock.mockResolvedValue(undefined)
  })

  it('runs scripts (allow-scripts sandbox) and inlines relative subresources as data: URLs', async () => {
    const { container, unmount } = renderFDV({ filePath: 'page/index.html' })
    await flush()

    const previewBtn = container.querySelectorAll<HTMLButtonElement>('.fdv-mode-switch button')[2]
    expect(previewBtn?.textContent).toBe('Preview')
    act(() => { previewBtn?.click() })
    await flush()

    const iframe = container.querySelector('iframe.fdv-html-preview')
    expect(iframe).toBeTruthy()
    expect(iframe?.getAttribute('sandbox')).toBe('allow-scripts')
    const doc = iframe?.getAttribute('srcdoc') ?? ''
    expect(doc).toContain('data:text/css;base64,')
    expect(doc).toContain('data:text/javascript;base64,')
    // Sibling assets were read through the owning project actor.
    expect(readMock).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ Path: 'page/style.css' }), expect.anything())
    expect(readMock).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ Path: 'page/app.js' }), expect.anything())
    unmount()
  })
})
