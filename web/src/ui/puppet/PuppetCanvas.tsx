/**
 * PuppetCanvas — wraps the approved DrawListViewer with Puppet-editor controls.
 *
 * Adds zoom, grid toggle, and a converter from schema-generated
 * PuppetDocumentSnapshot to DrawListSnapshot. The first slice renders from
 * static or backend-supplied snapshot data — no 30 fps polling, animation
 * playback, or full blend/mask (those remain DrawListViewer spike gaps).
 *
 * See [[接入DrawList查看器到Puppet画布]] and [[Inochi2D编辑器实施]].
 */

import { useState, useCallback, useMemo, useLayoutEffect, useRef } from 'react'
import { ZoomIn, ZoomOut, Grid3x3, RotateCcw, Triangle } from 'lucide-react'
import { useI18n } from '../../i18n'
import { DrawListViewer } from '../inochi2d/DrawListViewer'
import type { DrawListSnapshot } from '../inochi2d/types'
import type { PuppetDocumentSnapshot } from '../../gen-types/puppet'
import {
  convertPuppetDocumentToDrawList,
  countRenderableNodes,
  extractWireframeSegments,
  type TextureResolver,
} from './snapshotConverter'
import './PuppetCanvas.css'

export interface PuppetCanvasProps {
  /** Pre-built DrawListSnapshot (static / backend data). Takes priority over documentSnapshot. */
  snapshot?: DrawListSnapshot
  /** Schema-generated document snapshot to convert into a DrawListSnapshot. */
  documentSnapshot?: PuppetDocumentSnapshot
  /** Maps texture asset IDs to image data URLs for document-snapshot rendering. */
  textureResolver?: TextureResolver
  /** Canvas width in CSS pixels (default 512). */
  width?: number
  /** Canvas height in CSS pixels (default 512). */
  height?: number
  /** Flip Y axis for model-space Y-down convention (default true). */
  flipY?: boolean
  /** Initial zoom level, 1.0 = 100 % (default 1). */
  initialZoom?: number
  /** Show grid overlay initially (default false). */
  initialShowGrid?: boolean
  /** Show mesh wireframe overlay initially (default false). */
  initialShowMeshWireframe?: boolean
}

const MIN_ZOOM = 0.25
const MAX_ZOOM = 4
const ZOOM_STEP = 0.25

/** Measured placement of the WebGL canvas inside the stage (unscaled px). */
interface OverlayRect {
  left: number
  top: number
  width: number
  height: number
}

export function PuppetCanvas({
  snapshot,
  documentSnapshot,
  textureResolver,
  width = 512,
  height = 512,
  flipY = true,
  initialZoom = 1,
  initialShowGrid = false,
  initialShowMeshWireframe = false,
}: PuppetCanvasProps) {
  const { t } = useI18n()
  const [zoom, setZoom] = useState(initialZoom)
  const [showGrid, setShowGrid] = useState(initialShowGrid)
  const [showMeshWireframe, setShowMeshWireframe] = useState(initialShowMeshWireframe)
  const stageRef = useRef<HTMLDivElement>(null)
  const [overlayRect, setOverlayRect] = useState<OverlayRect | null>(null)

  const zoomIn = useCallback(() => {
    setZoom(z => Math.min(MAX_ZOOM, Math.round((z + ZOOM_STEP) / ZOOM_STEP) * ZOOM_STEP))
  }, [])
  const zoomOut = useCallback(() => {
    setZoom(z => Math.max(MIN_ZOOM, Math.round((z - ZOOM_STEP) / ZOOM_STEP) * ZOOM_STEP))
  }, [])
  const resetZoom = useCallback(() => setZoom(initialZoom), [initialZoom])
  const toggleGrid = useCallback(() => setShowGrid(g => !g), [])
  const toggleMeshWireframe = useCallback(() => setShowMeshWireframe(w => !w), [])

  // Convert schema-generated document to DrawListSnapshot when provided.
  const convertedSnapshot = useMemo(() => {
    if (!documentSnapshot) return undefined
    return convertPuppetDocumentToDrawList(documentSnapshot, { textureResolver })
  }, [documentSnapshot, textureResolver])

  const activeSnapshot = snapshot ?? convertedSnapshot
  const partCount = documentSnapshot
    ? countRenderableNodes(documentSnapshot)
    : undefined

  // Unique wireframe edges of the rendered geometry, in overlay space.
  const wireframeSegments = useMemo(
    () => (activeSnapshot ? extractWireframeSegments(activeSnapshot, flipY) : []),
    [activeSnapshot, flipY],
  )

  // Locate the WebGL canvas inside the stage so the wireframe overlay lines up
  // with it (the viewer renders its own header above the canvas). Offsets are
  // measured in the current (scaled) coordinate space and divided by zoom
  // because the overlay itself lives inside the zoom-transformed stage.
  useLayoutEffect(() => {
    if (!showMeshWireframe || !activeSnapshot) {
      setOverlayRect(null)
      return
    }
    const stage = stageRef.current
    const canvas = stage?.querySelector('canvas')
    if (!stage || !canvas) {
      setOverlayRect(null)
      return
    }
    const stageRect = stage.getBoundingClientRect()
    const canvasRect = canvas.getBoundingClientRect()
    const scale = zoom || 1
    setOverlayRect({
      left: (canvasRect.left - stageRect.left) / scale,
      top: (canvasRect.top - stageRect.top) / scale,
      width: canvasRect.width / scale,
      height: canvasRect.height / scale,
    })
  }, [showMeshWireframe, activeSnapshot, zoom])

  return (
    <div
      className="puppet-canvas"
      data-show-grid={showGrid}
      style={{ '--puppet-canvas-zoom': zoom } as React.CSSProperties}
    >
      <div className="puppet-canvas-toolbar">
        <button
          type="button"
          className="puppet-canvas-btn"
          onClick={zoomOut}
          disabled={zoom <= MIN_ZOOM}
          aria-label={t('puppetCanvas.zoomOut')}
          title={t('puppetCanvas.zoomOut')}
        >
          <ZoomOut size={14} />
        </button>
        <span className="puppet-canvas-zoom-value">{Math.round(zoom * 100)}%</span>
        <button
          type="button"
          className="puppet-canvas-btn"
          onClick={zoomIn}
          disabled={zoom >= MAX_ZOOM}
          aria-label={t('puppetCanvas.zoomIn')}
          title={t('puppetCanvas.zoomIn')}
        >
          <ZoomIn size={14} />
        </button>
        <button
          type="button"
          className="puppet-canvas-btn"
          onClick={resetZoom}
          disabled={zoom === initialZoom}
          aria-label={t('puppetCanvas.resetZoom')}
          title={t('puppetCanvas.resetZoom')}
        >
          <RotateCcw size={14} />
        </button>
        <button
          type="button"
          className={`puppet-canvas-btn ${showGrid ? 'active' : ''}`}
          onClick={toggleGrid}
          aria-label={t('puppetCanvas.toggleGrid')}
          title={t('puppetCanvas.toggleGrid')}
          aria-pressed={showGrid}
        >
          <Grid3x3 size={14} />
        </button>
        <button
          type="button"
          className={`puppet-canvas-btn ${showMeshWireframe ? 'active' : ''}`}
          onClick={toggleMeshWireframe}
          aria-label={t('puppetCanvas.toggleMeshWireframe')}
          title={t('puppetCanvas.toggleMeshWireframe')}
          aria-pressed={showMeshWireframe}
        >
          <Triangle size={14} />
        </button>
        {partCount !== undefined && (
          <span className="puppet-canvas-part-count">
            {partCount} {t('puppetCanvas.parts')}
          </span>
        )}
      </div>

      <div className={`puppet-canvas-viewport ${showGrid ? 'with-grid' : ''}`}>
        <div
          className="puppet-canvas-stage"
          ref={stageRef}
          style={{ transform: `scale(${zoom})` }}
        >
          <DrawListViewer
            snapshot={activeSnapshot}
            width={width}
            height={height}
            flipY={flipY}
          />
          {showMeshWireframe && activeSnapshot && overlayRect && (() => {
            const [minX, minY, maxX, maxY] = activeSnapshot.bounds
            const vbW = maxX - minX || 1
            const vbH = maxY - minY || 1
            return (
              <svg
                className="puppet-mesh-wireframe"
                data-testid="mesh-wireframe"
                aria-hidden="true"
                style={{
                  left: overlayRect.left,
                  top: overlayRect.top,
                  width: overlayRect.width,
                  height: overlayRect.height,
                }}
                viewBox={`${minX} ${minY} ${vbW} ${vbH}`}
                preserveAspectRatio="none"
              >
                {wireframeSegments.map((seg, i) => (
                  <line
                    key={i}
                    x1={seg.x1}
                    y1={seg.y1}
                    x2={seg.x2}
                    y2={seg.y2}
                  />
                ))}
              </svg>
            )
          })()}
        </div>
      </div>
    </div>
  )
}
