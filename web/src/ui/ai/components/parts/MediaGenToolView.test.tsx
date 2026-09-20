import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { MediaGenToolView, extractMediaPath, isVideoExt } from './MediaGenToolView'
import { getToolKind } from './tool-display'
import type { ToolFrame } from '../../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../../application/generated-client', () => ({ client: {} }))

const readBase64Mock = vi.fn(async () => ({ Content: btoa('fake-media-bytes') }))
vi.mock('../../../../gen-clients/filesystem/client', () => ({
  readBase64: (...args: unknown[]) => readBase64Mock(...(args as [])),
}))

vi.mock('../ImageViewer.tsx', () => ({
  base64ToBytes: (b64: string) => {
    const bin = atob(b64)
    const out = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i) & 0xff
    return out
  },
}))

const onOpenFileMock = vi.fn()
vi.mock('../../context/AIShellContext', () => ({
  useAIShellContext: () => ({ onOpenFile: onOpenFileMock }),
}))

function makeFrame(toolName: string, output: string, status: ToolFrame['status'] = 'completed'): ToolFrame {
  return {
    id: 'mg1',
    type: 'tool',
    toolName,
    status,
    input: JSON.stringify({ Prompt: 'a cat' }),
    output,
  } as unknown as ToolFrame
}

describe('extractMediaPath', () => {
  it('extracts Windows absolute image path', () => {
    expect(extractMediaPath('D:\\dev\\sporemind\\assets\\generated\\img_123.png'))
      .toBe('D:\\dev\\sporemind\\assets\\generated\\img_123.png')
  })

  it('extracts Unix absolute video path', () => {
    expect(extractMediaPath('/tmp/gen/clip_001.mp4')).toBe('/tmp/gen/clip_001.mp4')
  })

  it('returns null for non-media extension', () => {
    expect(extractMediaPath('D:\\logs\\out.txt')).toBeNull()
  })

  it('returns null for relative path', () => {
    expect(extractMediaPath('assets/img.png')).toBeNull()
  })

  it('returns null for multi-line output', () => {
    expect(extractMediaPath('D:\\a\\b.png\nsecond line')).toBeNull()
  })

  it('returns null for empty', () => {
    expect(extractMediaPath('')).toBeNull()
    expect(extractMediaPath(undefined)).toBeNull()
  })

  it('detects video extensions', () => {
    expect(isVideoExt('a.MP4')).toBe(true)
    expect(isVideoExt('a.webm')).toBe(true)
    expect(isVideoExt('a.png')).toBe(false)
  })
})

describe('getToolKind media_gen', () => {
  it('classifies generate_image as media_gen', () => {
    expect(getToolKind('generate_image')).toBe('media_gen')
  })

  it('classifies image_generate as media_gen', () => {
    expect(getToolKind('image_generate')).toBe('media_gen')
  })

  it('classifies generate_video as media_gen', () => {
    expect(getToolKind('generate_video')).toBe('media_gen')
  })

  it('classifies video_generate as media_gen', () => {
    expect(getToolKind('video_generate')).toBe('media_gen')
  })
})

describe('MediaGenToolView', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    onOpenFileMock.mockClear()
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  const renderView = async (frame: ToolFrame) => {
    await act(async () => { root.render(<MediaGenToolView frame={frame} />) })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
  }

  it('renders <img> thumbnail for generated image path', async () => {
    await renderView(makeFrame('generate_image', 'D:\\dev\\sporemind\\assets\\generated\\img_1.png'))
    const img = container.querySelector('img.ai-media-gen-image') as HTMLImageElement
    expect(img).toBeTruthy()
    expect(img.getAttribute('src')).toBeTruthy()
    expect(img.getAttribute('src')).not.toContain('file://')
    expect(readBase64Mock).toHaveBeenCalledWith(expect.anything(), { Path: 'D:\\dev\\sporemind\\assets\\generated\\img_1.png' })
  })

  it('renders <video controls> for generated video path', async () => {
    await renderView(makeFrame('generate_video', 'D:\\dev\\sporemind\\assets\\generated\\vid_2.mp4'))
    const video = container.querySelector('video.ai-media-gen-video') as HTMLVideoElement
    expect(video).toBeTruthy()
    expect(video.hasAttribute('controls')).toBe(true)
    expect(container.querySelector('img.ai-media-gen-image')).toBeNull()
  })

  it('falls back to code block for non-media output', async () => {
    await renderView(makeFrame('generate_image', 'model list refreshed'))
    expect(container.querySelector('img.ai-media-gen-image')).toBeNull()
    expect(container.querySelector('video.ai-media-gen-video')).toBeNull()
    const blocks = Array.from(container.querySelectorAll('pre.ai-tool-code'))
    expect(blocks.some(pre => pre.textContent?.includes('model list refreshed'))).toBe(true)
  })

  it('falls back to code block on error output', async () => {
    await renderView(makeFrame('generate_image', 'mediagen: post http 400: bad model', 'error'))
    const blocks = Array.from(container.querySelectorAll('pre.ai-tool-code.error'))
    expect(blocks.some(pre => pre.textContent?.includes('bad model'))).toBe(true)
    expect(container.querySelector('img.ai-media-gen-image')).toBeNull()
  })

  it('shows running state without preview while executing', async () => {
    await renderView(makeFrame('generate_image', '', 'running'))
    expect(container.querySelector('.ai-tool-running')).toBeTruthy()
    expect(container.querySelector('img.ai-media-gen-image')).toBeNull()
  })

  it('clicking image calls onOpenFile with the media path', async () => {
    await renderView(makeFrame('generate_image', 'D:\\dev\\sporemind\\assets\\generated\\img_3.png'))
    onOpenFileMock.mockClear()
    const preview = container.querySelector('.ai-media-gen-preview') as HTMLDivElement
    expect(preview).toBeTruthy()
    await act(async () => { preview.click() })
    expect(onOpenFileMock).toHaveBeenCalledWith('D:\\dev\\sporemind\\assets\\generated\\img_3.png')
  })

  it('shows load error message when readBase64 fails', async () => {
    readBase64Mock.mockRejectedValueOnce(new Error('file not found'))
    await renderView(makeFrame('generate_image', 'D:\\x\\gone.png'))
    const pending = container.querySelector('.ai-media-gen-pending')
    expect(pending?.textContent).toContain('file not found')
    readBase64Mock.mockReset()
    readBase64Mock.mockImplementation(async () => ({ Content: btoa('x') }))
  })
})
