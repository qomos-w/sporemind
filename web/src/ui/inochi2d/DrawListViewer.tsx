/**
 * DrawListViewer — minimal WebGL DrawList viewer component.
 *
 * Accepts a DrawListSnapshot and renders it on a transparent WebGL canvas.
 * Displays render statistics and gap warnings for unsupported features
 * (blend modes, masks, composites).
 *
 * This is a spike to validate that inochi2d-go DrawList output can be consumed
 * by a WebGL frontend. It is not a full Inochi2D renderer.
 */

import { useEffect, useRef, useState, useCallback } from 'react'
import type { DrawListSnapshot } from './types'
import { DrawListWebGLRenderer, type RenderResult } from './webglRenderer'
import { ALL_SAMPLES } from './sampleData'
import './DrawListViewer.css'

interface DrawListViewerProps {
  /** Snapshot to render. If omitted, uses built-in sample selector. */
  snapshot?: DrawListSnapshot
  /** Canvas width in CSS pixels. */
  width?: number
  /** Canvas height in CSS pixels. */
  height?: number
  /** Flip Y axis (top-left origin → clip-space up). */
  flipY?: boolean
}

export function DrawListViewer({
  snapshot,
  width = 512,
  height = 512,
  flipY = false,
}: DrawListViewerProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const rendererRef = useRef<DrawListWebGLRenderer | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<RenderResult | null>(null)
  const [selectedSample, setSelectedSample] = useState(0)
  const [loading, setLoading] = useState(false)

  // Determine which snapshot to use.
  const activeSnapshot = snapshot ?? ALL_SAMPLES[selectedSample]?.data

  const renderSnapshot = useCallback(
    async (data: DrawListSnapshot | undefined) => {
      const canvas = canvasRef.current
      if (!canvas || !data) return

      try {
        setLoading(true)
        setError(null)

        // Create or reuse renderer.
        if (!rendererRef.current) {
          rendererRef.current = new DrawListWebGLRenderer(canvas)
        }
        const renderer = rendererRef.current

        // Load textures.
        await renderer.loadTextures(data)

        // Render.
        const res = renderer.render(data, flipY)
        setResult(res)
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
        setResult(null)
      } finally {
        setLoading(false)
      }
    },
    [flipY],
  )

  // Re-render when snapshot or flipY changes.
  useEffect(() => {
    void renderSnapshot(activeSnapshot)
  }, [activeSnapshot, renderSnapshot])

  // Cleanup on unmount.
  useEffect(() => {
    return () => {
      rendererRef.current?.dispose()
      rendererRef.current = null
    }
  }, [])

  const hasGaps = result && (
    result.gaps.unsupportedBlendModes.length > 0 ||
    result.gaps.maskCommands > 0 ||
    result.gaps.compositeCommands > 0
  )

  return (
    <div className="drawlist-viewer">
      <div className="drawlist-viewer-header">
        <h3 className="drawlist-viewer-title">Inochi2D DrawList Viewer (spike)</h3>
        {!snapshot && (
          <select
            className="drawlist-viewer-select"
            value={selectedSample}
            onChange={(e) => setSelectedSample(Number(e.target.value))}
          >
            {ALL_SAMPLES.map((s, i) => (
              <option key={s.label} value={i}>{s.label}</option>
            ))}
          </select>
        )}
        <label className="drawlist-viewer-flip">
          <input
            type="checkbox"
            checked={flipY}
            readOnly
          />
          Flip Y
        </label>
      </div>

      <div className="drawlist-viewer-canvas-container">
        <canvas
          ref={canvasRef}
          width={width}
          height={height}
          className="drawlist-viewer-canvas"
          style={{ width, height }}
        />
        {loading && <div className="drawlist-viewer-loading">Loading textures…</div>}
        {error && <div className="drawlist-viewer-error">{error}</div>}
      </div>

      {result && (
        <div className="drawlist-viewer-stats">
          <div className="drawlist-viewer-stat">
            <span className="drawlist-viewer-stat-value">{result.drawnCommands}</span>
            <span className="drawlist-viewer-stat-label">Drawn</span>
          </div>
          <div className="drawlist-viewer-stat">
            <span className="drawlist-viewer-stat-value">{result.skippedCommands}</span>
            <span className="drawlist-viewer-stat-label">Skipped</span>
          </div>
          <div className="drawlist-viewer-stat">
            <span className="drawlist-viewer-stat-value">{result.gaps.totalCommands}</span>
            <span className="drawlist-viewer-stat-label">Total</span>
          </div>
        </div>
      )}

      {hasGaps && (
        <div className="drawlist-viewer-gaps">
          <h4 className="drawlist-viewer-gaps-title">⚠ Renderer Gaps (spike limitations)</h4>
          {result!.gaps.unsupportedBlendModes.length > 0 && (
            <div className="drawlist-viewer-gap-item">
              <span className="drawlist-viewer-gap-label">Blend modes (fallback to Normal):</span>
              <span className="drawlist-viewer-gap-value">{result!.gaps.unsupportedBlendModes.join(', ')}</span>
            </div>
          )}
          {result!.gaps.maskCommands > 0 && (
            <div className="drawlist-viewer-gap-item">
              <span className="drawlist-viewer-gap-label">Mask commands (not implemented):</span>
              <span className="drawlist-viewer-gap-value">{result!.gaps.maskCommands}</span>
            </div>
          )}
          {result!.gaps.compositeCommands > 0 && (
            <div className="drawlist-viewer-gap-item">
              <span className="drawlist-viewer-gap-label">Composite commands (not implemented):</span>
              <span className="drawlist-viewer-gap-value">{result!.gaps.compositeCommands}</span>
            </div>
          )}
        </div>
      )}

      {activeSnapshot && (
        <details className="drawlist-viewer-details">
          <summary>Snapshot data ({activeSnapshot.vertices.length} verts, {activeSnapshot.indices.length} indices, {activeSnapshot.commands.length} cmds)</summary>
          <pre className="drawlist-viewer-json">
            {JSON.stringify(activeSnapshot, null, 2)}
          </pre>
        </details>
      )}
    </div>
  )
}
