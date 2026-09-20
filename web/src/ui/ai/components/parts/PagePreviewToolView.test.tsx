import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { PagePreviewToolView } from './PagePreviewToolView'
import type { ToolFrame } from '../../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../../i18n/provider', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('../../../../application/generated-client', () => ({ client: {} }))

const openGlobalBrowserMock = vi.hoisted(() => vi.fn(async () => ({ Opened: true, Url: '', Error: '' })))
vi.mock('../../../../gen-clients/local/client', () => ({
  openGlobalBrowser: openGlobalBrowserMock,
}))

// Minimal SVG as base64 for blob mock
const SVG_B64 = btoa('<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>')

vi.mock('../../../../gen-clients/filesystem/client', () => ({
  readBase64: vi.fn(async (_c: unknown, { Path }: { Path: string }) => ({
    Content: Path.endsWith('.svg') ? btoa('<svg xmlns="http://www.w3.org/2000/svg"/>') : SVG_B64,
  })),
  read: vi.fn(async (_c: unknown, { Path }: { Path: string }) => {
    if (Path.endsWith('.css')) return { Content: 'body{color:red}' }
    if (Path.endsWith('.svg')) return { Content: '<svg xmlns="http://www.w3.org/2000/svg"/>' }
    return { Content: '<!DOCTYPE html><html><head><link rel="stylesheet" href="style.css"></head><body><h1>hi</h1></body></html>' }
  }),
}))

vi.mock('../ImageViewer.tsx', () => ({
  base64ToBytes: (b64: string) => {
    const bin = atob(b64)
    const out = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i) & 0xff
    return out
  },
}))

function makeFrame(url: string, filePath?: string, status: ToolFrame['status'] = 'completed'): ToolFrame {
  const output = filePath
    ? JSON.stringify({ Url: url, FilePath: filePath, Title: '', Error: '' })
    : ''
  return {
    id: 'pp1',
    type: 'tool',
    toolName: 'show_page_thumbnail',
    status,
    input: JSON.stringify({ Url: url, Title: '' }),
    output,
  } as unknown as ToolFrame
}

describe('PagePreviewToolView', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  const renderView = async (frame: ToolFrame, agentActorId?: string) => {
    await act(async () => { root.render(<PagePreviewToolView frame={frame} agentActorId={agentActorId} />) })
    // Allow async blob resolution to settle
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
  }

  // ── Local files render as a full-width render-only embed ──

  it('renders full-width embed <img> with blob URL for local PNG', async () => {
    await renderView(makeFrame('file:///F:/dev/sporemind/test.png', 'F:/dev/sporemind/test.png'))
    // No thumbnail card for local files.
    expect(container.querySelector('.ai-page-preview-card')).toBeNull()
    expect(container.querySelector('.ai-page-preview-viewport')).toBeNull()
    const img = container.querySelector('img.ai-page-embed-image') as HTMLImageElement
    expect(img).toBeTruthy()
    expect(img.getAttribute('src')).toBeTruthy()
    expect(img.getAttribute('src')).not.toContain('file://')
    expect(container.querySelector('iframe')).toBeNull()
  })

  it('renders full-width embed <img> with blob URL for local SVG', async () => {
    await renderView(makeFrame('file:///F:/dev/sporemind/assets/icon.svg', 'F:/dev/sporemind/assets/icon.svg'))
    const img = container.querySelector('img.ai-page-embed-image') as HTMLImageElement
    expect(img).toBeTruthy()
    expect(img.getAttribute('src')).toBeTruthy()
    expect(img.getAttribute('src')).not.toContain('file://')
    expect(container.querySelector('iframe')).toBeNull()
  })

  it('renders render-only sandboxed <iframe> with blob src for file:// HTML URLs', async () => {
    await renderView(makeFrame('file:///F:/dev/sporemind/index.html', 'F:/dev/sporemind/index.html'))
    const iframe = container.querySelector('iframe.ai-page-embed-frame') as HTMLIFrameElement
    expect(iframe).toBeTruthy()
    // file:// is blocked cross-scheme; content is served from a blob URL read
    // through the filesystem actor instead.
    expect(iframe.getAttribute('src')).toMatch(/^blob:/)
    // Scripts enabled so JS-rendered pages paint; interaction is disabled via
    // pointer-events:none in CSS, so no form/modal/popups permissions needed.
    expect(iframe.getAttribute('sandbox')).toBe('allow-scripts')
    expect(container.querySelector('img.ai-page-embed-image')).toBeNull()
    expect(container.querySelector('.ai-page-preview-card')).toBeNull()
  })

  it('shows file chrome (name + dir) for local file embeds', async () => {
    await renderView(makeFrame('file:///F:/dev/sporemind/index.html', 'F:/dev/sporemind/index.html'))
    const name = container.querySelector('.ai-page-embed-name')
    expect(name?.textContent).toBe('index.html')
    const dir = container.querySelector('.ai-page-embed-dir')
    expect(dir?.textContent).toBe('F:/dev/sporemind')
  })

  it('has no action buttons; the embed itself is the click surface', async () => {
    await renderView(makeFrame('file:///F:/dev/sporemind/index.html', 'F:/dev/sporemind/index.html'))
    expect(container.querySelector('.ai-page-embed-btn')).toBeNull()
    expect(container.querySelector('.ai-page-embed-actions')).toBeNull()
    const embed = container.querySelector('.ai-page-embed') as HTMLDivElement
    expect(embed.getAttribute('role')).toBe('button')
    expect(embed.getAttribute('title')).toBe('ai.tool.openInBrowser')
  })

  it('materializes relative subresources (css/js/img) into the blob html', async () => {
    const readMock = (await import('../../../../gen-clients/filesystem/client')).read as ReturnType<typeof vi.fn>
    await renderView(makeFrame('file:///F:/dev/sporemind/index.html', 'F:/dev/sporemind/index.html'))
    const iframe = container.querySelector('iframe.ai-page-embed-frame') as HTMLIFrameElement
    expect(iframe).toBeTruthy()
    expect(iframe.getAttribute('src')).toMatch(/^blob:/)
    // The stylesheet next to index.html was fetched through the fs client.
    expect(readMock).toHaveBeenCalledWith({}, { Path: 'F:/dev/sporemind/style.css' })
  })

  // ── Remote / web URLs keep the clickable thumbnail card ──

  it('shows favicon + hostname for remote web URLs, no iframe (X-Frame-Options)', async () => {
    await renderView(makeFrame('https://github.com'))
    expect(container.querySelector('iframe')).toBeNull()
    expect(container.querySelector('.ai-page-preview-viewport')).toBeNull()
    expect(container.querySelector('.ai-page-embed')).toBeNull()
    const meta = container.querySelector('.ai-page-preview-meta')
    expect(meta).toBeTruthy()
    const favicon = container.querySelector('img.ai-page-preview-favicon')
    expect(favicon).toBeTruthy()
    expect(favicon!.getAttribute('src')).toContain('google.com/s2/favicons')
    expect(favicon!.getAttribute('src')).toContain('github.com')
    const label = container.querySelector('.ai-page-preview-label')
    expect(label?.textContent).toBe('github.com')
  })

  it('renders full-width embed iframe for localhost dev server URLs', async () => {
    await renderView(makeFrame('http://localhost:3000'))
    const iframe = container.querySelector('iframe.ai-page-embed-frame') as HTMLIFrameElement
    expect(iframe).toBeTruthy()
    // Loads directly over http; render-only via pointer-events:none.
    expect(iframe.getAttribute('src')).toBe('http://localhost:3000')
    expect(iframe.getAttribute('sandbox')).toBe('allow-scripts')
    expect(container.querySelector('.ai-page-preview-card')).toBeNull()
    const name = container.querySelector('.ai-page-embed-name')
    expect(name?.textContent).toBe('localhost:3000')
  })

  it('has no openInBrowser line and no title text', async () => {
    await renderView(makeFrame('https://github.com'))
    expect(container.querySelector('.ai-page-preview-open')).toBeNull()
    expect(container.querySelector('.ai-page-preview-title')).toBeNull()
  })

  // ── Action wiring ──

  it('clicking the local embed opens it in the right-side global browser tab', async () => {
    const frame = makeFrame('file:///F:/dev/sporemind/index.html', 'F:/dev/sporemind/index.html')
    await renderView(frame, 'agent-a')
    openGlobalBrowserMock.mockClear()
    const embed = container.querySelector('.ai-page-embed') as HTMLDivElement
    await act(async () => { embed.click() })
    expect(openGlobalBrowserMock).toHaveBeenCalledTimes(1)
    expect(openGlobalBrowserMock).toHaveBeenCalledWith({}, { Url: 'file:///F:/dev/sporemind/index.html' }, { target: 'agent-a' })
  })

  it('clicking remote thumbnail card does not call openGlobalBrowser without an agent', async () => {
    const frame = makeFrame('https://github.com')
    await renderView(frame)
    openGlobalBrowserMock.mockClear()
    const card = container.querySelector('.ai-page-preview-card') as HTMLDivElement
    if (card) await act(async () => { card.click() })
    expect(openGlobalBrowserMock).not.toHaveBeenCalled()
  })
})
