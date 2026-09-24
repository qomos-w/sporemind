import { useState, useEffect, useRef, memo } from 'react'
import type { GlassRenderFrame, GlassSceneElement } from '../../gen-clients/system/types'
import { G2_PROFILE } from './render-spec/profiles/g2'
import { TextMeasurer } from './render-spec/measurer/TextMeasurer'
import { TextWrapper } from './render-spec/wrapper/TextWrapper'
import type { BreakMode } from './render-spec/wrapper/types'

/**
 * GlassScenePreview — DOM-based debug preview of the Glass display.
 *
 * The preview is rendered at an integer multiple of the native 576×288 canvas
 * so that the grid and element positions align to CSS pixels and no sub-pixel
 * blurring occurs. The largest integer scale that fits the container is chosen
 * automatically via ResizeObserver.
 */
const CANVAS_W = 576
const CANVAS_H = 288
// Real G2 firmware metrics: 26px bitmap glyph inside a 40px text container.
const LINE_HEIGHT_PX = 40
const FONT_SIZE_PX = 26
// Device-side compositing constants (MentraOS rendering contract, 54f627c):
// the HUD bar is drawn by the device (glass.hud.update), reserves the top
// 40px and shifts scene content down; the firmware text-container pool holds
// 6 elements total and the HUD occupies slots in front of the scene.
const HUD_HEIGHT_PX = 40
const DEVICE_TEXT_POOL = 6
const HUD_POOL_COST = 4
const DEVICE_IMAGE_POOL = 4
const VERTICAL_PADDING_PX = 7
const HORIZONTAL_PADDING_PX = 4
const PREVIEW_FONT = '"GlassesMirror", "Cascadia Code", "Consolas", "Courier New", monospace'

function px(v: number, scale: number): number {
  return Math.round(v * scale)
}

// Focus ring for the element the device would route gestures to (Scene.FocusId).
// Preview tool only — the device itself gives no FocusId visual feedback.
const focusOutline = (focused: boolean): React.CSSProperties =>
  focused ? { outline: '2px dashed #22d3ee', outlineOffset: 1 } : {}

const overBudgetStyle = (over: boolean): React.CSSProperties =>
  over ? { opacity: 0.25, outline: '2px dashed #f43f5e', outlineOffset: 1 } : {}

// Phone-side text pipeline (verbatim port under ./render-spec): the device
// receives PRE-WRAPPED text, so wrap points here are exact per the glyph
// table — only the displayed font shape remains approximate.
const measurer = new TextMeasurer(G2_PROFILE)
const textWrapper = new TextWrapper(measurer)

function pipelineLines(
  text: string,
  widthPx: number,
  heightPx: number,
  breakMode?: string,
  overflow?: string,
): string[] {
  const maxLines = Math.max(1, Math.floor(heightPx / LINE_HEIGHT_PX))
  const mode: BreakMode =
    breakMode === 'character' || breakMode === 'word' ? breakMode : 'character-no-hyphen'
  const res = textWrapper.wrap(text, { maxWidthPx: widthPx, maxLines, breakMode: mode })
  const lines = res.lines.slice()
  if (overflow === 'ellipsis' && res.truncated && lines.length > 0) {
    let last = lines[lines.length - 1] ?? ''
    while (last.length > 0 && measurer.measureText(last + '…') > widthPx) {
      last = last.slice(0, -1)
    }
    lines[lines.length - 1] = last + '…'
  }
  return lines
}

function renderElement(
  el: GlassSceneElement,
  index: number,
  scale: number,
  focusId?: string,
  yOffset = 0,
  overBudget = false,
) {
  if (el.Visible === false) return null
  const focused = focusId !== undefined && el.Id === focusId

  const baseStyle: React.CSSProperties = {
    position: 'absolute',
    left: px(el.Box.X, scale),
    top: px(el.Box.Y + yOffset, scale),
    width: px(el.Box.W, scale),
    height: px(el.Box.H, scale),
    boxSizing: 'border-box',
    zIndex: el.Z ?? 0,
    ...overBudgetStyle(overBudget),
  }

  switch (el.Type) {
      case 'rect':
        return (
          <div
            key={el.Id || index}
            style={{
              ...baseStyle,
              border: `${Math.max(1, (el.Border ?? 1) * scale)}px solid #0f0`,
              borderRadius: el.Radius ? px(el.Radius, scale) : undefined,
              ...focusOutline(focused),
            }}
          />
        )

    case 'text':
    case 'button': {
      const lines = pipelineLines(
        el.Text ?? el.Label ?? '',
        el.Box.W,
        el.Box.H,
        el.BreakMode,
        el.Overflow,
      )
      return (
        <div
          key={el.Id || index}
          style={{
            ...baseStyle,
            display: 'flex',
            alignItems: 'flex-start',
            justifyContent: 'flex-start',
            color: '#0f0',
            fontSize: FONT_SIZE_PX * scale,
            lineHeight: `${LINE_HEIGHT_PX * scale}px`,
            fontFamily: PREVIEW_FONT,
            textAlign: 'left',
            whiteSpace: 'pre',
            overflow: 'hidden',
            padding: `${VERTICAL_PADDING_PX * scale}px ${HORIZONTAL_PADDING_PX * scale}px`,
            outline: '1px dashed rgba(74, 222, 128, 0.35)',
            background: 'rgba(74, 222, 128, 0.04)',
            ...(el.Border
              ? { border: `${el.Border * scale}px solid #0f0`, borderRadius: el.Radius ? px(el.Radius, scale) : undefined }
              : {}),
            ...focusOutline(focused),
          }}
        >
          {lines.join('\n')}
        </div>
      )
    }

    case 'list': {
      const options = el.Options ?? []
      // The device folds a list into a single text element (1 pool slot):
      // selected item prefixed '> ', others two spaces; selection starts at 0.
      const folded = options
        .map((opt, i) => (i === 0 && el.Selected ? '> ' : '  ') + opt.Text)
        .join('\n')
      const lines = pipelineLines(folded, el.Box.W, el.Box.H, el.BreakMode, el.Overflow)
      return (
        <div
          key={el.Id || index}
          style={{
            ...baseStyle,
            color: '#0f0',
            fontSize: FONT_SIZE_PX * scale,
            lineHeight: `${LINE_HEIGHT_PX * scale}px`,
            fontFamily: PREVIEW_FONT,
            whiteSpace: 'pre',
            overflow: 'hidden',
            padding: `${VERTICAL_PADDING_PX * scale}px ${HORIZONTAL_PADDING_PX * scale}px`,
            outline: '1px dashed rgba(74, 222, 128, 0.35)',
            background: 'rgba(74, 222, 128, 0.04)',
            ...(el.Border
              ? { border: `${el.Border * scale}px solid #0f0`, borderRadius: el.Radius ? px(el.Radius, scale) : undefined }
              : {}),
            ...focusOutline(focused),
          }}
        >
          {lines.join('\n')}
        </div>
      )
    }

    case 'image':
      return (
        <div
          key={el.Id || index}
          style={{
            ...baseStyle,
            background: el.ImageData ? `url(${el.ImageData}) center/contain no-repeat` : '#111',
            filter: 'sepia(1) hue-rotate(50deg) saturate(3)',
          }}
        />
      )

    default:
      return null
  }
}

const MAX_SCALE = 8

export const GlassScenePreview = memo(function GlassScenePreview({
  frame,
  online,
}: {
  frame: GlassRenderFrame | undefined
  online?: boolean
}) {
  const [showGrid, setShowGrid] = useState(false)
  const [autoScale, setAutoScale] = useState(true)
  const [userScale, setUserScale] = useState(1)
  const [fitScale, setFitScale] = useState(1)
  const wrapperRef = useRef<HTMLDivElement>(null)
  const elements = frame?.Scene?.Elements
  const sorted = elements ? [...elements].sort((a, b) => (a.Z ?? 0) - (b.Z ?? 0)) : []
  const [showDeviceHUD, setShowDeviceHUD] = useState(true)

  // Device text-container pool: 6 slots shared by text/rect/list/button
  // (a whole list folds into ONE text slot); images use a separate pool of 4.
  // The HUD bar (3 text + 1 separator rect) consumes 4 slots in front of the
  // scene. Budget consumption follows Z-ascending order, so the TOPMOST-Z
  // elements drop first; Visible=false skips counting entirely.
  const sceneBudget = showDeviceHUD ? DEVICE_TEXT_POOL - HUD_POOL_COST : DEVICE_TEXT_POOL
  const overBudgetIds = new Set<string>()
  if (elements) {
    const byZ = [...elements].sort((a, b) => (a.Z ?? 0) - (b.Z ?? 0))
    let textUsed = 0
    let imageUsed = 0
    for (const el of byZ) {
      if (el.Visible === false) continue
      if (el.Type === 'image') {
        // Image boxes are never scaled to fit: a box the canvas would clamp is
        // dropped whole on the device.
        const clamped =
          el.Box.X < 0 ||
          el.Box.Y < 0 ||
          el.Box.X + el.Box.W > CANVAS_W ||
          el.Box.Y + el.Box.H > CANVAS_H
        imageUsed++
        if (clamped || imageUsed > DEVICE_IMAGE_POOL) overBudgetIds.add(el.Id)
      } else {
        textUsed++
        if (textUsed > sceneBudget) overBudgetIds.add(el.Id)
      }
    }
  }
  const yOffset = showDeviceHUD ? HUD_HEIGHT_PX : 0

  useEffect(() => {
    const el = wrapperRef.current
    if (!el) return
    const update = () => {
      const w = el.clientWidth
      const s = Math.max(1, Math.floor(w / CANVAS_W))
      setFitScale(s)
    }
    update()
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const scale = autoScale ? fitScale : userScale
  const scaledW = CANVAS_W * scale
  const scaledH = CANVAS_H * scale

  const zoomIn = () => {
    setAutoScale(false)
    setUserScale(s => Math.min(MAX_SCALE, s + 1))
  }
  const zoomOut = () => {
    setAutoScale(false)
    setUserScale(s => Math.max(1, s - 1))
  }
  const zoomFit = () => {
    setAutoScale(true)
    setUserScale(fitScale)
  }

  return (
    <div className="glass-canvas-wrapper" ref={wrapperRef}>
      <div className="glass-preview-toolbar">
        <span className="glass-preview-label" title="G2 firmware-style preview: 26px glyph, 40px line, 7px vertical + 4px horizontal padding. Font shape is approximate (firmware bitmap font unavailable).">
          Display preview (G2 firmware style)
        </span>
        <span className="glass-preview-scale">{CANVAS_W}×{CANVAS_H} @ {scale}x</span>
        {frame?.Scene?.FocusId ? (
          <span className="glass-preview-scale" title="Scene.FocusId — element the device routes gestures to (preview tool; the device itself shows no focus feedback)">
            focus: {frame.Scene.FocusId}
          </span>
        ) : null}
        {overBudgetIds.size > 0 ? (
          <span
            className="glass-preview-scale"
            style={{ color: '#f43f5e' }}
            title="Elements beyond the firmware pools (6 text slots, HUD uses 4; images separate pool of 4). The device drops them silently — shown ghosted here."          >
            {overBudgetIds.size} over budget
          </span>
        ) : null}
        <div className="glass-preview-zoom">
          <button className="glass-preview-zoom-btn" onClick={zoomOut} title="Zoom out">-</button>
          <button className="glass-preview-zoom-btn" onClick={zoomFit} title="Fit to width">fit</button>
          <button className="glass-preview-zoom-btn" onClick={zoomIn} title="Zoom in">+</button>
        </div>
        <button
          className={`glass-preview-toggle ${showDeviceHUD ? 'active' : ''}`}
          onClick={() => setShowDeviceHUD(v => !v)}
          title="Device-drawn HUD bar (glass.hud.update): reserves top 40px, shifts scene down, and consumes 4 of the 6 text-container slots. Values are placeholders except connection."
        >
          HUD
        </button>
        <button
          className={`glass-preview-toggle ${showGrid ? 'active' : ''}`}
          onClick={() => setShowGrid(v => !v)}
          title="Toggle glasses grid"
        >
          Grid
        </button>
      </div>
      <div
        className="glass-scene"
        style={{
          position: 'relative',
          width: scaledW,
          height: scaledH,
          margin: '0 auto',
          background: '#000',
          overflow: 'hidden',
          borderRadius: '4px',
        }}
      >
        {showGrid ? <GlassesGrid scale={scale} /> : null}
        {showDeviceHUD ? (
          <DeviceHudBar scale={scale} online={online} />
        ) : null}
        {sorted.length > 0 ? (
          sorted.map((el, i) =>
            renderElement(el, i, scale, frame?.Scene?.FocusId, yOffset, overBudgetIds.has(el.Id)),
          )
        ) : frame?.Text ? (
          <div
            style={{
              position: 'absolute',
              inset: scale,
              color: '#0f0',
              fontSize: FONT_SIZE_PX * scale,
              lineHeight: `${LINE_HEIGHT_PX * scale}px`,
              fontFamily: PREVIEW_FONT,
              whiteSpace: 'pre',
              overflow: 'hidden',
            }}
          >
            {pipelineLines(frame.Text, CANVAS_W, CANVAS_H).join('\n')}
          </div>
        ) : (
          <div
            style={{
              position: 'absolute',
              inset: 0,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: '#444',
              fontSize: FONT_SIZE_PX * scale,
              lineHeight: `${LINE_HEIGHT_PX * scale}px`,
              fontFamily: PREVIEW_FONT,
            }}
          >
            no frame
          </div>
        )}
      </div>
    </div>
  )
})

/**
 * GlassesGrid — CSS pixel-aligned grid overlay for the G2 canvas (576×288).
 * Lines are drawn at integer multiples of the chosen scale so they land on
 * actual device/CSS pixels and stay sharp.
 */
function GlassesGrid({ scale }: { scale: number }) {
  const minorStep = 8 * scale
  const majorStep = 32 * scale
  const width = CANVAS_W * scale
  const height = CANVAS_H * scale

  const minorV: number[] = []
  const minorH: number[] = []
  const majorV: number[] = []
  const majorH: number[] = []

  for (let x = minorStep; x < width; x += minorStep) minorV.push(x)
  for (let y = minorStep; y < height; y += minorStep) minorH.push(y)
  for (let x = majorStep; x < width; x += majorStep) majorV.push(x)
  for (let y = majorStep; y < height; y += majorStep) majorH.push(y)

  return (
    <svg
      width={width}
      height={height}
      style={{
        position: 'absolute',
        inset: 0,
        pointerEvents: 'none',
        zIndex: 999,
      }}
    >
      {minorV.map(x => (
        <line key={`mv${x}`} x1={x + 0.5} y1={0} x2={x + 0.5} y2={height} stroke="#1a1a2a" strokeWidth={1} />
      ))}
      {minorH.map(y => (
        <line key={`mh${y}`} x1={0} y1={y + 0.5} x2={width} y2={y + 0.5} stroke="#1a1a2a" strokeWidth={1} />
      ))}
      {majorV.map(x => (
        <line key={`Mv${x}`} x1={x + 0.5} y1={0} x2={x + 0.5} y2={height} stroke="#2a2a4a" strokeWidth={1} />
      ))}
      {majorH.map(y => (
        <line key={`Mh${y}`} x1={0} y1={y + 0.5} x2={width} y2={y + 0.5} stroke="#2a2a4a" strokeWidth={1} />
      ))}
      <rect x={0.5} y={0.5} width={width - 1} height={height - 1} fill="none" stroke="#3a3a5a" strokeWidth={1} />
      <line x1={width / 2 + 0.5} y1={0} x2={width / 2 + 0.5} y2={height} stroke="#3a3a5a" strokeWidth={1} strokeDasharray={`${4 * scale} ${4 * scale}`} />
      <line x1={0} y1={height / 2 + 0.5} x2={width} y2={height / 2 + 0.5} stroke="#3a3a5a" strokeWidth={1} strokeDasharray={`${4 * scale} ${4 * scale}`} />
    </svg>
  )
}

/**
 * DeviceHudBar — simulation of the device-drawn HUD bar (glass.hud.update,
 * NOT frame content): left connection glyph, middle agent indicator, right
 * battery, separator rect at y=39, and scene content shifted down by 40px.
 * Connection reflects the real session; agent and battery are placeholders
 * until those values flow into the debug snapshot.
 */
function DeviceHudBar({ scale, online }: { scale: number; online?: boolean }) {
  const h = px(HUD_HEIGHT_PX, scale)
  const hudStyle: React.CSSProperties = {
    position: 'absolute',
    top: 0,
    left: 0,
    width: CANVAS_W * scale,
    height: h,
    boxSizing: 'border-box',
    color: '#0f0',
    fontSize: FONT_SIZE_PX * scale,
    lineHeight: `${h}px`,
    fontFamily: PREVIEW_FONT,
    whiteSpace: 'pre',
    overflow: 'hidden',
    zIndex: 998,
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
    padding: `0 ${px(4, scale)}px`,
  }
  return (
    <div style={hudStyle} title="Device-drawn HUD (glass.hud.update). Server-frame hudLine text is ignored on the device.">
      <span>{online ? '● ok' : '○ off'}</span>
      <span>agent off</span>
      <span>BAT --</span>
      <div
        style={{
          position: 'absolute',
          left: 0,
          top: px(HUD_HEIGHT_PX - 1, scale),
          width: CANVAS_W * scale,
          height: scale,
          background: '#0f0',
          opacity: 0.6,
        }}
      />
    </div>
  )
}
