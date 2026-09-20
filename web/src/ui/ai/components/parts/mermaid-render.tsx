import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { LocateFixed, Maximize2, X } from 'lucide-react'
import { useBrowserOverlay } from '../../browserOverlay'

export interface MermaidRenderProps {
  code: string
}

interface MermaidCanvasProps {
  code: string
  /** Renders the toolbar "expand" button that opens the floating overlay. */
  onExpand?: () => void
  /** Renders a toolbar "close" button (used inside the expand overlay). */
  onClose?: () => void
}

/** While `code` keeps changing (streaming deltas into an unclosed fence),
 * re-renders wait for this much code stability before calling mermaid. */
const RERENDER_DEBOUNCE_MS = 250

// Supported diagram first-line prefixes (lowercased)
const SUPPORTED_PREFIXES = [
  'graph ',
  'flowchart ',
  'sequencediagram',
  'classdiagram',
  'statediagram',
  'erdiagram',
]

/** Fixed, deliberately large base font for every diagram so all rendered SVGs
 * share one dpi / label size / node sizing — neither too small nor too big at
 * the source level. The display scale then brings it down to the message-stream
 * text size, keeping the ratio identical across diagrams. */
const MERMAID_FONT_SIZE = 16

function isSupported(code: string): boolean {
  const first = code.trim().split('\n')[0]?.trim().toLowerCase() ?? ''
  return SUPPORTED_PREFIXES.some((p) => first.startsWith(p))
}

function getMermaidTheme(): 'default' | 'dark' {
  if (typeof document === 'undefined') return 'default'
  return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'default'
}

/** The actual rendered text size of the surrounding message-stream body, so
 * the diagram's displayed labels match it. Falls back to --text-base (13px). */
function readStreamFontSize(host: Element | null): number {
  if (host) {
    const fs = getComputedStyle(host as HTMLElement).fontSize
    const n = parseFloat(fs)
    if (Number.isFinite(n) && n > 0) return Math.round(n)
  }
  const root = document.documentElement
  const v = getComputedStyle(root).getPropertyValue('--text-base').trim()
  const n = parseFloat(v)
  if (Number.isFinite(n) && n > 0) return v.endsWith('rem') ? Math.round(n * 16) : Math.round(n)
  return 13
}

let idCounter = 0

const clamp = (v: number, min: number, max: number) => Math.min(Math.max(v, min), max)

/** Horizontal anchor: LEFT when the scaled image overflows the canvas (a wide
 * horizontal diagram shows its opening columns instead of an arbitrary middle
 * band); CENTER when it fits (a narrow diagram floats centered in the stream).
 * The vertical anchor is always the top (see resetView). */
const anchorX = (vw: number, W: number, z: number): number =>
  W * z > vw ? 0 : (vw - W * z) / 2

/** Remount height reserve. A message holding a mermaid canvas is virtualized
 * (ViewportSlot) once it scrolls past the 1000px off-screen margin; coming back,
 * MermaidRender remounts and its canvas collapses to 0 until the async
 * mermaid.render resolves, then snaps to full height — shifting everything
 * below and making the stream jump while dragging. Caching the last canvas
 * height per code lets the remount reserve it synchronously (before paint) so
 * layout stays stable across the off-screen/remount cycle. */
const canvasHeightCache = new Map<string, number>()

export function clearMermaidCanvasHeightCache() {
  canvasHeightCache.clear()
}

const MermaidCanvas: React.FC<MermaidCanvasProps> = ({ code, onExpand, onClose }) => {
  const [svg, setSvg] = useState<string>('')
  const [error, setError] = useState<string | null>(null)
  const [themeVersion, setThemeVersion] = useState(0)
  const initializedRef = useRef(false)
  /** First render for this mount runs immediately; later code/theme changes
   * are debounced so a growing (partial) code block doesn't re-render the
   * diagram — and swap error/SVG states — on every streaming tick. */
  const mountedOnceRef = useRef(false)

  const viewportRef = useRef<HTMLDivElement>(null)
  const stageRef = useRef<HTMLDivElement>(null)
  const transformRef = useRef({ tx: 0, ty: 0, z: 1 })
  const dimsRef = useRef({ W: 0, H: 0 })
  /** Uniform display ratio: stream text size / mermaid source font. Identical
   * for every diagram (depends only on font sizes, not diagram width). */
  const baseRef = useRef(1)
  const dragRef = useRef<{ x: number; y: number; tx: number; ty: number } | null>(null)
  /** Cache key for the remount height reserve (below). Kept in a ref so the
   * stable syncHeight callback can record under the current code without
   * re-creating (and invalidating its dependents) on every code change. */
  const codeRef = useRef(code)
  codeRef.current = code

  const supported = isSupported(code)

  // Observe data-theme changes on <html>
  useEffect(() => {
    if (!supported) return
    const el = document.documentElement
    const observer = new MutationObserver(() => {
      setThemeVersion((v) => v + 1)
    })
    observer.observe(el, { attributes: true, attributeFilter: ['data-theme'] })
    return () => observer.disconnect()
  }, [supported])

  // Render on code or theme change. Source SVGs always use the fixed large
  // font so their intrinsic dpi/label size is uniform across diagrams.
  useEffect(() => {
    if (!supported) {
      setSvg('')
      setError(null)
      return
    }

    let cancelled = false
    const id = `ai-mermaid-${++idCounter}`

    const run = async () => {
      try {
        const { default: mermaid } = await import('mermaid')
        if (cancelled) return
        if (!initializedRef.current) {
          initializedRef.current = true
          mermaid.initialize({
            startOnLoad: false,
            theme: getMermaidTheme(),
            securityLevel: 'loose',
            // Mermaid's render() throws BEFORE removing its measuring div when
            // parsing fails unless this is set — each failed render would leak
            // a visible in-flow SVG container into <body>, stacking up per
            // streaming tick and shifting the page layout.
            suppressErrorRendering: true,
            themeVariables: { fontSize: `${MERMAID_FONT_SIZE}px` },
          })
        } else {
          mermaid.initialize({
            theme: getMermaidTheme(),
            themeVariables: { fontSize: `${MERMAID_FONT_SIZE}px` },
          })
        }

        const { svg: result } = await mermaid.render(id, code)
        if (!cancelled) {
          setSvg(result)
          setError(null)
        }
      } catch (e) {
        if (!cancelled) {
          setSvg('')
          setError(e instanceof Error ? e.message : 'Rendering failed')
        }
      }
    }

    // Immediate on first mount (static content paints without delay);
    // debounced afterwards so streaming code changes coalesce into one render.
    let timer: ReturnType<typeof setTimeout> | undefined
    if (!mountedOnceRef.current) {
      mountedOnceRef.current = true
      void run()
    } else {
      timer = setTimeout(() => { void run() }, RERENDER_DEBOUNCE_MS)
    }

    return () => {
      cancelled = true
      if (timer !== undefined) clearTimeout(timer)
    }
  }, [code, themeVersion, supported])

  /** Pin the canvas height to the scaled image height (capped by the CSS
   * max-height). Records the value so the resize observer can distinguish our
   * own height writes from external layout changes (collapse/expand, window
   * resize). Reading clientHeight afterwards reflects the clamped value. */
  const lastSetHeightRef = useRef(0)
  const syncHeight = useCallback((z: number) => {
    const vp = viewportRef.current
    if (vp && dimsRef.current.H) {
      const h = Math.round(dimsRef.current.H * z)
      vp.style.height = `${h}px`
      lastSetHeightRef.current = h
      canvasHeightCache.set(codeRef.current, h)
    }
  }, [])

  const applyTransform = useCallback(() => {
    const s = stageRef.current
    if (!s) return
    const { tx, ty, z } = transformRef.current
    s.style.transform = `translate(${tx}px, ${ty}px) scale(${z})`
    syncHeight(z)
  }, [syncHeight])

  const clampView = useCallback(({ tx, ty, z }: { tx: number; ty: number; z: number }) => {
    const vp = viewportRef.current
    const { W, H } = dimsRef.current
    if (!vp || !W || !H) return { tx, ty }
    syncHeight(z)
    const vw = vp.clientWidth
    const vh = vp.clientHeight
    // One uniform rule per axis: the image may occupy any position between
    // "left/top edge flush" and "right/bottom edge flush" — i.e. the ordered
    // range [min(0, view-content), max(0, view-content)]. A larger image pans
    // through its overflow; a smaller image slides freely edge-to-edge inside
    // the canvas. Either way it can never be dragged entirely off-screen.
    const xMin = Math.min(0, vw - W * z)
    const xMax = Math.max(0, vw - W * z)
    const yMin = Math.min(0, vh - H * z)
    const yMax = Math.max(0, vh - H * z)
    return { tx: clamp(tx, xMin, xMax), ty: clamp(ty, yMin, yMax) }
  }, [syncHeight])

  /** Idempotent: read the svg's viewBox and pin the STAGE's layout size to
   * it, then record the dims. Mermaid emits svgs with width="100%" /
   * max-width styles (or no size attributes), so their LAYOUT size does not
   * match the viewBox — every translate/scale/height computation would drift
   * and the image would collapse to a tiny shrink-to-fit box.
   * The pin goes on the stage (a React-owned element), NOT on the svg:
   * markdown re-parses (e.g. "Show more" expanding a truncated frame) swap
   * the svg via dangerouslySetInnerHTML without re-running our layout effect,
   * and any inline style on the old svg would be lost with it. CSS sizes
   * whatever svg is inside to fill the stage, so a swapped svg re-fills
   * automatically. */
  const measureAndPin = useCallback((): boolean => {
    const stage = stageRef.current
    if (!stage) return false
    const svgEl = stage.querySelector('svg') as SVGSVGElement | null
    if (!svgEl) return false
    let W = 0
    let H = 0
    const vb = svgEl.viewBox?.baseVal
    if (vb && vb.width && vb.height) {
      W = vb.width
      H = vb.height
    } else {
      const attr = svgEl.getAttribute('viewBox')
      const parts = attr ? attr.trim().split(/[\s,]+/) : []
      if (parts.length === 4) {
        W = parseFloat(parts[2] ?? '') || 0
        H = parseFloat(parts[3] ?? '') || 0
      }
    }
    if (!W || !H) {
      W = parseFloat(svgEl.getAttribute('width') ?? '') || 0
      H = parseFloat(svgEl.getAttribute('height') ?? '') || 0
    }
    if (!W || !H) return false
    stage.style.width = `${W}px`
    stage.style.height = `${H}px`
    dimsRef.current = { W, H }
    return true
  }, [])

  const resetView = useCallback(() => {
    // Heal a lost stage size pin before computing.
    if (stageRef.current && !stageRef.current.style.width) measureAndPin()
    const vp = viewportRef.current
    const { W, H } = dimsRef.current
    const base = baseRef.current
    // Zero-size canvas = hidden (right-panel expand sets display:none, fold
    // mid-mount). Centering on a zero box writes garbage that may outlive the
    // hidden phase; skip — the resize observer recenters on re-show.
    if (!vp || !W || !H || !vp.clientWidth) {
      if (vp && W && H) {
        transformRef.current = { ...transformRef.current, z: base }
        applyTransform()
      }
      return
    }
    // Default (uniform) ratio = base. Anchor the image at the START edges:
    // vertically flush with the top, horizontally LEFT when the image
    // overflows the canvas (see anchorX). The diagram's opening rows and
    // columns (root node / title) are what a reader needs first; centering
    // on clipped diagrams showed an arbitrary middle band instead.
    const z = base
    const anchored = clampView({
      tx: anchorX(vp.clientWidth, W, z),
      ty: 0,
      z,
    })
    transformRef.current = { ...anchored, z }
    applyTransform()
  }, [applyTransform, clampView, measureAndPin])

  // Mount-only: reserve the last known canvas height for this code before the
  // async mermaid render resolves. Without this, a virtualized remount (the
  // message scrolled off-screen and back) collapses the canvas to 0 and snaps
  // back to full height, shifting everything below and making the stream jump
  // while dragging. Runs in useLayoutEffect so the reserved height is applied
  // before the first paint of the remounted content.
  useLayoutEffect(() => {
    const vp = viewportRef.current
    if (!vp) return
    const cached = canvasHeightCache.get(codeRef.current)
    if (cached) {
      vp.style.height = `${cached}px`
      lastSetHeightRef.current = cached
    }
  }, [])

  // Measure the rendered svg, compute the uniform ratio, and (re)center
  // whenever a new diagram paints (first render, theme switch, code settle).
  useLayoutEffect(() => {
    if (!svg || !viewportRef.current || !stageRef.current) return
    if (!measureAndPin()) return
    const streamFont = readStreamFontSize(
      viewportRef.current.closest('.markdown-content, .ai-step-text, .ai-text-content') ?? null,
    )
    // Display ratio = bring the large source font down to the stream text
    // size (the portal-root fallback --text-base matches it, so the overlay
    // canvas draws at the SAME scale as the inline one — just on a much
    // bigger viewport). Width-independent, so every diagram shares it.
    baseRef.current = streamFont / MERMAID_FONT_SIZE
    resetView()
  }, [svg, resetView, measureAndPin])

  // Re-center through ANY external layout change (collapse/expand remounts,
  // window resize, panel drags — including the transient zero-size states
  // those produce, which previously left a stale top-left transform). A change
  // is external when the width moved, or when the height differs from the
  // height we ourselves wrote (CSS max-height clamp / outer container). Our
  // own height writes are skipped so zooming doesn't discard a dragged
  // position, and there is no observer feedback loop.
  useEffect(() => {
    const vp = viewportRef.current
    if (!vp) return
    let lastW = vp.clientWidth
    const ro = new ResizeObserver(() => {
      // Self-heal: if the stage lost its size pin (HMR-stale DOM etc.),
      // re-pin and reset — otherwise every transform computation collapses.
      if (stageRef.current && !stageRef.current.style.width) {
        if (measureAndPin()) {
          lastW = vp.clientWidth
          resetView()
          return
        }
      }
      const vw = vp.clientWidth
      const vh = vp.clientHeight
      // Zero-size = hidden (display:none ancestor). Do NOT recenter onto a
      // zero box — keep the last good view; the re-show resize will recenter.
      if (vw === 0 || vh === 0) { lastW = vw; return }
      const widthChanged = vw !== lastW
      const heightExternal = vh !== lastSetHeightRef.current
      if (!widthChanged && !heightExternal) return
      lastW = vw
      const { W } = dimsRef.current
      const z = transformRef.current.z
      // Same START-edge anchor as resetView (x via anchorX, y at the top),
      // so an external resize never flips the anchor.
      const anchored = clampView({
        tx: anchorX(vw, W, z),
        ty: 0,
        z,
      })
      transformRef.current = { ...anchored, z }
      applyTransform()
    })
    ro.observe(vp)
    return () => ro.disconnect()
  }, [applyTransform, clampView, measureAndPin, resetView])

  const onPointerDown = (e: React.PointerEvent) => {
    if (e.button !== 0) return
    if ((e.target as Element).closest('.ai-mermaid-toolbar')) return
    e.preventDefault()
    const vp = viewportRef.current
    if (!vp) return
    // Heal a lost stage size pin so the drag math and clamping use true dims.
    if (stageRef.current && !stageRef.current.style.width) measureAndPin()
    try { vp.setPointerCapture(e.pointerId) } catch { /* noop */ }
    const t = transformRef.current
    dragRef.current = { x: e.clientX, y: e.clientY, tx: t.tx, ty: t.ty }
    vp.classList.add('dragging')
  }
  const onPointerMove = (e: React.PointerEvent) => {
    const d = dragRef.current
    if (!d) return
    const t = transformRef.current
    const clamped = clampView({
      tx: d.tx + (e.clientX - d.x),
      ty: d.ty + (e.clientY - d.y),
      z: t.z,
    })
    transformRef.current = { ...clamped, z: t.z }
    applyTransform()
  }
  const endDrag = (e: React.PointerEvent) => {
    const vp = viewportRef.current
    if (!vp) return
    if (dragRef.current) {
      dragRef.current = null
      vp.classList.remove('dragging')
    }
    try { vp.releasePointerCapture(e.pointerId) } catch { /* noop */ }
  }

  if (!supported) {
    return (
      <pre className="ai-mermaid-fallback">
        <code>{code}</code>
      </pre>
    )
  }

  if (error) {
    return (
      <div className="ai-mermaid-error">
        <pre className="ai-mermaid-fallback">
          <code>{code}</code>
        </pre>
        <div className="ai-mermaid-error-msg">{error}</div>
      </div>
    )
  }

  return (
    <div
      className="ai-mermaid-canvas"
      ref={viewportRef}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerLeave={endDrag}
    >
      {svg ? (
        <div
          className="ai-mermaid-stage"
          ref={stageRef}
          dangerouslySetInnerHTML={{ __html: svg }}
        />
      ) : null}
      {svg ? (
        <div className="ai-mermaid-toolbar" onPointerDown={(e) => e.stopPropagation()}>
          <button type="button" title="Recenter" onClick={(e) => { e.stopPropagation(); resetView() }}>
            <LocateFixed size={14} />
          </button>
          {onExpand ? (
            <button type="button" title="Expand" onClick={(e) => { e.stopPropagation(); onExpand() }}>
              <Maximize2 size={14} />
            </button>
          ) : null}
          {onClose ? (
            <button type="button" title="Close" onClick={(e) => { e.stopPropagation(); onClose() }}>
              <X size={14} />
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

/** Floating overlay hosting a second MermaidCanvas of the same diagram on a
 * much larger viewport — same display scale as the stream (no enlargement),
 * so wide/tall graphs show far more of the picture before panning. Registers
 * with `useBrowserOverlay` (project constraint) so the native right-panel
 * browser windows hide while the overlay is up. Closes via its toolbar Close
 * button, the backdrop, or Escape. */
const MermaidExpandOverlay: React.FC<{ code: string; onClose: () => void }> = ({ code, onClose }) => {
  useBrowserOverlay(true)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  return createPortal(
    <div className="ai-mermaid-expand-root" role="dialog" aria-modal="true" aria-label="Diagram">
      <div className="ai-mermaid-expand-backdrop" onClick={onClose} />
      <div className="ai-mermaid-expand-panel">
        <MermaidCanvas code={code} onClose={onClose} />
      </div>
    </div>,
    document.body,
  )
}

export const MermaidRender: React.FC<MermaidRenderProps> = ({ code }) => {
  const [expanded, setExpanded] = useState(false)
  return (
    <>
      <MermaidCanvas code={code} onExpand={() => setExpanded(true)} />
      {expanded ? (
        <MermaidExpandOverlay code={code} onClose={() => setExpanded(false)} />
      ) : null}
    </>
  )
}
