import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// ---------------------------------------------------------------------------
// Mock the WebGL renderer so the DrawListViewer inside PuppetCanvas doesn't
// need a real WebGL context. Same pattern as DrawListViewer.test.tsx.
// ---------------------------------------------------------------------------

const mocks = vi.hoisted(() => {
  return {
    render: vi.fn(),
    loadTextures: vi.fn(),
    dispose: vi.fn(),
  }
})

vi.mock('../inochi2d/webglRenderer', () => ({
  DrawListWebGLRenderer: class MockRenderer {
    render = mocks.render
    loadTextures = mocks.loadTextures
    dispose = mocks.dispose
  },
}))

// Import after mock is set up.
import { PuppetCanvas } from './PuppetCanvas'
import { I18nProvider } from '../../i18n'
import type { RenderResult } from '../inochi2d/webglRenderer'
import { sampleTexturedQuad, sampleWithGaps } from '../inochi2d/sampleData'
import type { DrawListSnapshot } from '../inochi2d/types'
import type { PuppetDocumentSnapshot } from '../../gen-types/puppet'

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

function makeDocumentSnapshot(): PuppetDocumentSnapshot {
  return {
    Document: {
      Id: 'doc-1',
      Revision: 1,
      Name: 'Test',
      Root: {
        Guid: 'root',
        Name: 'Root',
        Kind: 'group',
        Enabled: true,
        Children: [
          {
            Guid: 'part-1',
            Name: 'Body',
            Kind: 'part',
            Enabled: true,
            Z: 1,
            Children: [],
          },
          {
            Guid: 'part-2',
            Name: 'Head',
            Kind: 'part',
            Enabled: true,
            Z: 2,
            Children: [],
          },
        ],
      },
      CreatedAt: '2026-01-01T00:00:00Z',
      UpdatedAt: '2026-01-01T00:00:00Z',
    },
    StagedAssetCount: 0,
    RevisionCount: 1,
  }
}

// ---------------------------------------------------------------------------
// Render helper
// ---------------------------------------------------------------------------

function renderCanvas(props?: Parameters<typeof PuppetCanvas>[0]) {
  const container = document.createElement('div')
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider>
        <PuppetCanvas {...props} />
      </I18nProvider>
    )
  })
  return { root, container }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('PuppetCanvas', () => {
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

  // --- Snapshot updates --------------------------------------------------

  it('renders a canvas with a provided snapshot', () => {
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad })
    const canvas = container.querySelector('canvas')
    expect(canvas).toBeTruthy()
    expect(canvas?.width).toBe(512)
    act(() => root.unmount())
  })

  it('passes the snapshot through to DrawListViewer (renderer called)', async () => {
    const { root } = renderCanvas({ snapshot: sampleTexturedQuad })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    expect(mocks.render).toHaveBeenCalled()
    act(() => root.unmount())
  })

  it('updates when snapshot prop changes', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(
        <I18nProvider>
          <PuppetCanvas snapshot={sampleTexturedQuad} />
        </I18nProvider>
      )
    })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    const callsAfterFirst = mocks.render.mock.calls.length

    const otherSnapshot: DrawListSnapshot = {
      ...sampleWithGaps,
    }
    act(() => {
      root.render(
        <I18nProvider>
          <PuppetCanvas snapshot={otherSnapshot} />
        </I18nProvider>
      )
    })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    expect(mocks.render.mock.calls.length).toBeGreaterThan(callsAfterFirst)
    act(() => root.unmount())
  })

  it('converts a PuppetDocumentSnapshot and renders it', async () => {
    const { root } = renderCanvas({ documentSnapshot: makeDocumentSnapshot() })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })

    // 2 part nodes → 2 draw commands → renderer called with 8 vertices.
    expect(mocks.render).toHaveBeenCalledTimes(1)
    const snapshotArg = mocks.render.mock.calls[0]![0] as DrawListSnapshot
    expect(snapshotArg.commands).toHaveLength(2)
    expect(snapshotArg.vertices).toHaveLength(8)
    act(() => root.unmount())
  })

  it('shows part count for document snapshots', () => {
    const { root, container } = renderCanvas({ documentSnapshot: makeDocumentSnapshot() })
    expect(container.textContent).toContain('2')
    act(() => root.unmount())
  })

  // --- Error state -------------------------------------------------------

  it('shows error message when renderer throws', async () => {
    mocks.render.mockImplementation(() => {
      throw new Error('WebGL not supported')
    })
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    expect(container.textContent).toContain('WebGL not supported')
    act(() => root.unmount())
  })

  // --- Gap hints ---------------------------------------------------------

  it('shows gap hints when snapshot contains unsupported features', async () => {
    mocks.render.mockReturnValue(mockRenderWithGaps)
    const { root, container } = renderCanvas({ snapshot: sampleWithGaps })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    expect(container.textContent).toContain('Renderer Gaps')
    expect(container.textContent).toContain('AddGlow')
    expect(container.textContent).toContain('Mask commands')
    expect(container.textContent).toContain('Composite commands')
    act(() => root.unmount())
  })

  it('does not show gap hints when there are no gaps', async () => {
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    expect(container.textContent).not.toContain('Renderer Gaps')
    act(() => root.unmount())
  })

  // --- Zoom controls -----------------------------------------------------

  it('renders zoom toolbar with initial zoom displayed', () => {
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad, initialZoom: 1.5 })
    expect(container.textContent).toContain('150%')
    act(() => root.unmount())
  })

  it('increases zoom on zoom-in button click', () => {
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad })
    const buttons = container.querySelectorAll('.puppet-canvas-btn')
    // Buttons: [0]=zoomOut, [1]=zoomIn, [2]=reset, [3]=grid
    const zoomInBtn = buttons[1]! as HTMLButtonElement
    act(() => { zoomInBtn.click() })
    expect(container.textContent).toContain('125%')
    act(() => root.unmount())
  })

  it('decreases zoom on zoom-out button click', () => {
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad })
    const buttons = container.querySelectorAll('.puppet-canvas-btn')
    const zoomOutBtn = buttons[0]! as HTMLButtonElement
    act(() => { zoomOutBtn.click() })
    expect(container.textContent).toContain('75%')
    act(() => root.unmount())
  })

  it('resets zoom to initialZoom on reset button click', () => {
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad, initialZoom: 2 })
    expect(container.textContent).toContain('200%')

    // Zoom out to change away from initial.
    const buttons = container.querySelectorAll('.puppet-canvas-btn')
    const zoomOutBtn = buttons[0]! as HTMLButtonElement
    act(() => { zoomOutBtn.click() })
    expect(container.textContent).toContain('175%')

    // Reset should go back to initialZoom (200%).
    const resetBtn = buttons[2]! as HTMLButtonElement
    act(() => { resetBtn.click() })
    expect(container.textContent).toContain('200%')
    act(() => root.unmount())
  })

  it('applies zoom as CSS transform on the stage', () => {
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad, initialZoom: 2 })
    const stage = container.querySelector('.puppet-canvas-stage') as HTMLElement
    expect(stage.style.transform).toContain('scale(2)')
    act(() => root.unmount())
  })

  it('disables zoom-out at minimum zoom', () => {
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad, initialZoom: 0.25 })
    const buttons = container.querySelectorAll('.puppet-canvas-btn')
    const zoomOutBtn = buttons[0]! as HTMLButtonElement
    expect(zoomOutBtn.disabled).toBe(true)
    act(() => root.unmount())
  })

  // --- Grid toggle -------------------------------------------------------

  it('toggles grid overlay on grid button click', () => {
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad })
    const viewport = container.querySelector('.puppet-canvas-viewport') as HTMLElement
    expect(viewport.classList.contains('with-grid')).toBe(false)

    const buttons = container.querySelectorAll('.puppet-canvas-btn')
    const gridBtn = buttons[3]! as HTMLButtonElement
    act(() => { gridBtn.click() })
    expect(viewport.classList.contains('with-grid')).toBe(true)

    act(() => { gridBtn.click() })
    expect(viewport.classList.contains('with-grid')).toBe(false)
    act(() => root.unmount())
  })

  it('marks grid button as active when grid is on', () => {
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad, initialShowGrid: true })
    const buttons = container.querySelectorAll('.puppet-canvas-btn')
    const gridBtn = buttons[3]! as HTMLButtonElement
    expect(gridBtn.classList.contains('active')).toBe(true)
    expect(gridBtn.getAttribute('aria-pressed')).toBe('true')
    act(() => root.unmount())
  })

  // --- Mesh wireframe overlay ---------------------------------------------

  it('toggles the mesh wireframe overlay on wireframe button click', () => {
    const { root, container } = renderCanvas({ snapshot: sampleTexturedQuad })
    // Buttons: [0]=zoomOut, [1]=zoomIn, [2]=reset, [3]=grid, [4]=wireframe
    const buttons = container.querySelectorAll('.puppet-canvas-btn')
    const wireframeBtn = buttons[4]! as HTMLButtonElement
    expect(container.querySelector('[data-testid="mesh-wireframe"]')).toBeNull()

    act(() => { wireframeBtn.click() })
    expect(container.querySelector('[data-testid="mesh-wireframe"]')).toBeTruthy()

    act(() => { wireframeBtn.click() })
    expect(container.querySelector('[data-testid="mesh-wireframe"]')).toBeNull()
    act(() => root.unmount())
  })

  it('marks the wireframe button active when initially enabled', () => {
    const { root, container } = renderCanvas({
      snapshot: sampleTexturedQuad,
      initialShowMeshWireframe: true,
    })
    const buttons = container.querySelectorAll('.puppet-canvas-btn')
    const wireframeBtn = buttons[4]! as HTMLButtonElement
    expect(wireframeBtn.classList.contains('active')).toBe(true)
    expect(wireframeBtn.getAttribute('aria-pressed')).toBe('true')
    act(() => root.unmount())
  })

  it('renders wireframe line segments matching the converted geometry', async () => {
    // 2 implicit-quad parts → 2 × 5 unique edges = 10 overlay segments.
    const { root, container } = renderCanvas({
      documentSnapshot: makeDocumentSnapshot(),
      initialShowMeshWireframe: true,
    })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })

    const overlay = container.querySelector('[data-testid="mesh-wireframe"]') as SVGSVGElement
    expect(overlay).toBeTruthy()
    expect(overlay.querySelectorAll('line')).toHaveLength(10)
    act(() => root.unmount())
  })

  // --- Cleanup -----------------------------------------------------------

  it('disposes renderer on unmount', async () => {
    const { root } = renderCanvas({ snapshot: sampleTexturedQuad })
    await act(async () => { await new Promise(r => setTimeout(r, 10)) })
    act(() => root.unmount())
    expect(mocks.dispose).toHaveBeenCalled()
  })
})
