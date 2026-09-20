import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// ---------------------------------------------------------------------------
// Mock the WebGL renderer with vi.hoisted so the mock fns exist when the
// vi.mock factory runs (vitest hoists vi.mock above all imports).
// ---------------------------------------------------------------------------

const mocks = vi.hoisted(() => {
  return {
    render: vi.fn(),
    loadTextures: vi.fn(),
    dispose: vi.fn(),
  }
})

vi.mock('./webglRenderer', () => ({
  DrawListWebGLRenderer: class MockRenderer {
    render = mocks.render
    loadTextures = mocks.loadTextures
    dispose = mocks.dispose
  },
}))

// Import after mock is set up.
import { DrawListViewer } from './DrawListViewer'
import type { RenderResult } from './webglRenderer'
import { sampleTexturedQuad, sampleWithGaps } from './sampleData'

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

const mockRenderResult: RenderResult = {
  drawnCommands: 1,
  skippedCommands: 0,
  gaps: {
    unsupportedBlendModes: [],
    maskCommands: 0,
    compositeCommands: 0,
    totalCommands: 1,
  },
}

const mockRenderWithGaps: RenderResult = {
  drawnCommands: 1,
  skippedCommands: 6,
  gaps: {
    unsupportedBlendModes: ['AddGlow'],
    maskCommands: 3,
    compositeCommands: 3,
    totalCommands: 7,
  },
}

function renderViewer(props?: Parameters<typeof DrawListViewer>[0]) {
  const container = document.createElement('div')
  const root = createRoot(container)
  act(() => {
    root.render(<DrawListViewer {...props} />)
  })
  return { root, container }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('DrawListViewer', () => {
  beforeEach(() => {
    mocks.render.mockReset()
    mocks.loadTextures.mockReset()
    mocks.dispose.mockReset()
    mocks.loadTextures.mockResolvedValue(undefined)
    mocks.render.mockReturnValue(mockRenderResult)
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it('renders title and sample selector when no snapshot provided', async () => {
    const { root, container } = renderViewer()
    expect(container.textContent).toContain('DrawList Viewer')
    expect(container.querySelector('select')).toBeTruthy()
    expect(container.querySelectorAll('option').length).toBeGreaterThanOrEqual(4)
    act(() => root.unmount())
  })

  it('renders a canvas element', async () => {
    const { root, container } = renderViewer()
    const canvas = container.querySelector('canvas')
    expect(canvas).toBeTruthy()
    expect(canvas?.width).toBe(512)
    act(() => root.unmount())
  })

  it('renders with provided snapshot (no sample selector)', async () => {
    const { root, container } = renderViewer({ snapshot: sampleTexturedQuad })
    expect(container.querySelector('select')).toBeFalsy()
    expect(container.textContent).toContain('DrawList Viewer')
    act(() => root.unmount())
  })

  it('displays render statistics after rendering', async () => {
    const { root, container } = renderViewer({ snapshot: sampleTexturedQuad })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    expect(container.textContent).toContain('Drawn')
    expect(container.textContent).toContain('Skipped')
    act(() => root.unmount())
  })

  it('shows gap warnings for unsupported features', async () => {
    mocks.render.mockReturnValue(mockRenderWithGaps)
    const { root, container } = renderViewer({ snapshot: sampleWithGaps })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    expect(container.textContent).toContain('Renderer Gaps')
    expect(container.textContent).toContain('AddGlow')
    expect(container.textContent).toContain('Mask commands')
    expect(container.textContent).toContain('Composite commands')
    act(() => root.unmount())
  })

  it('shows error when renderer throws', async () => {
    mocks.render.mockImplementation(() => {
      throw new Error('WebGL not supported')
    })
    const { root, container } = renderViewer({ snapshot: sampleTexturedQuad })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    expect(container.textContent).toContain('WebGL not supported')
    act(() => root.unmount())
  })

  it('displays snapshot details with vertex/index counts', async () => {
    const { root, container } = renderViewer({ snapshot: sampleTexturedQuad })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    const summary = container.querySelector('summary')
    expect(summary?.textContent).toContain('4 verts')
    expect(summary?.textContent).toContain('6 indices')
    expect(summary?.textContent).toContain('1 cmds')
    act(() => root.unmount())
  })

  it('disposes renderer on unmount', async () => {
    const { root } = renderViewer({ snapshot: sampleTexturedQuad })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    act(() => root.unmount())
    expect(mocks.dispose).toHaveBeenCalled()
  })
})
