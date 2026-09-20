import React, { useCallback, useEffect, useRef, useState } from 'react'
import type { WindowInfo } from '../../application/wails-bridge'
import { getScreenshotData, refreshScreenshotForWindow, closeScreenshotWindow, saveScreenshotDialog, setClipboardText, showScreenshotWindow, setClipboardImage } from '../../application/wails-bridge'
import { Events } from '@wailsio/runtime'
import { useI18n } from '../../i18n'
import { ScreenshotToolbar } from './ScreenshotToolbar'

type Point = { x: number; y: number }
type Rect = { x: number; y: number; w: number; h: number }

type Annotation =
  | { type: 'rect'; rect: Rect }
  | { type: 'ellipse'; rect: Rect }
  | { type: 'line'; p1: Point; p2: Point }
  | { type: 'brush'; points: Point[] }
  | { type: 'number'; x: number; y: number; num: number }

type Tool = 'rect' | 'ellipse' | 'line' | 'brush' | 'number' | null

export default function ScreenshotOverlay() {
  const { t } = useI18n()
  const imageBase64Ref = useRef('')
  const imageRef = useRef<HTMLImageElement | null>(null)
  const windowsRef = useRef<WindowInfo[]>([])
  const [ready, setReady] = useState(false)
  const [error, setError] = useState('')
  const [mousePos, setMousePos] = useState<Point>({ x: 0, y: 0 })
  const [hoveredWindow, setHoveredWindow] = useState<WindowInfo | null>(null)
  const [selectedArea, setSelectedArea] = useState<Rect | null>(null) // confirmed annotation area
  const [tool, setTool] = useState<Tool>(null)
  const [annotations, setAnnotations] = useState<Annotation[]>([])
  // pending drag/annotation state
  const [isDragging, setIsDragging] = useState(false)
  const [dragStart, setDragStart] = useState<Point | null>(null)
  const [dragCurrent, setDragCurrent] = useState<Point | null>(null)
  const [currentAnnotation, setCurrentAnnotation] = useState<Annotation | null>(null)
  const [showToolbar, setShowToolbar] = useState(false)
  const [magnifierColor, setMagnifierColor] = useState('#000000')
  const [numberCounter, setNumberCounter] = useState(1)

  const overlayRef = useRef<HTMLCanvasElement>(null)
  const magRef = useRef<HTMLCanvasElement>(null)
  const offscreenRef = useRef<HTMLCanvasElement | null>(null)

  const applyScreenshot = useCallback((d: { ImageBase64: string; Windows: WindowInfo[] }) => {
    setReady(false)
    imageBase64Ref.current = d.ImageBase64
    windowsRef.current = d.Windows ?? []
    const img = new Image()
    img.onerror = () => setError('Failed to load screenshot image.')
    img.onload = () => {
      imageRef.current = img
      const oc = document.createElement('canvas')
      oc.width = img.naturalWidth
      oc.height = img.naturalHeight
      const octx = oc.getContext('2d')!
      octx.drawImage(img, 0, 0)
      offscreenRef.current = oc
      setReady(true)
    }
    img.src = d.ImageBase64
  }, [])

  // ---------- load data ----------
  useEffect(() => {
    document.documentElement.style.background = 'transparent'
    document.body.style.background = 'transparent'

    getScreenshotData().then((d) => {
      if (!d) {
        setError('No screenshot data available.')
        return
      }
      applyScreenshot(d)
    })
  }, [applyScreenshot])

  // reveal window after first paint with screenshot image
  useEffect(() => {
    if (!ready) return
    requestAnimationFrame(() => {
      showScreenshotWindow()
    })
  }, [ready])

  // ---------- sync canvas size ----------
  useEffect(() => {
    if (!ready) return
    const c = overlayRef.current
    if (!c) return
    c.width = window.innerWidth
    c.height = window.innerHeight
    drawOverlay()
  }, [ready])

  // ---------- draw overlay ----------
  const drawOverlay = useCallback(() => {
    const c = overlayRef.current
    if (!c) return
    const ctx = c.getContext('2d')
    if (!ctx) return
    ctx.clearRect(0, 0, c.width, c.height)
    // hovered window yellow border
    if (hoveredWindow && !isDragging && !selectedArea && !tool) {
      ctx.strokeStyle = '#FFD700'
      ctx.lineWidth = 3
      ctx.setLineDash([])
      ctx.strokeRect(hoveredWindow.X, hoveredWindow.Y, hoveredWindow.Width, hoveredWindow.Height)
    }
    // selection area during drag
    if (isDragging && dragStart && dragCurrent) {
      const x = Math.min(dragStart.x, dragCurrent.x)
      const y = Math.min(dragStart.y, dragCurrent.y)
      const w = Math.abs(dragCurrent.x - dragStart.x)
      const h = Math.abs(dragCurrent.y - dragStart.y)
      ctx.strokeStyle = '#4da6ff'
      ctx.lineWidth = 2
      ctx.setLineDash([6, 4])
      ctx.strokeRect(x, y, w, h)
      ctx.setLineDash([])
      ctx.save()
      ctx.font = '14px monospace'
      const tw = ctx.measureText(`${w} x ${h}`).width
      ctx.fillStyle = 'rgba(0,0,0,0.6)'
      ctx.fillRect(x + 4, y - 22, tw + 12, 20)
      ctx.fillStyle = '#fff'
      ctx.fillText(`${w} x ${h}`, x + 10, y - 6)
      ctx.restore()
    }
    // confirmed area
    if (selectedArea && !isDragging) {
      ctx.strokeStyle = '#00c853'
      ctx.lineWidth = 2
      ctx.setLineDash([6, 4])
      ctx.strokeRect(selectedArea.x, selectedArea.y, selectedArea.w, selectedArea.h)
      ctx.setLineDash([])
      ctx.save()
      ctx.font = '13px monospace'
      const tw = ctx.measureText(`${selectedArea.w} x ${selectedArea.h}`).width
      ctx.fillStyle = 'rgba(0,0,0,0.6)'
      ctx.fillRect(selectedArea.x + 4, selectedArea.y - 22, tw + 12, 20)
      ctx.fillStyle = '#fff'
      ctx.fillText(`${selectedArea.w} x ${selectedArea.h}`, selectedArea.x + 10, selectedArea.y - 6)
      ctx.restore()
    }
    // annotations
    for (const a of annotations) drawAnnotation(ctx, a)
    if (currentAnnotation) drawAnnotation(ctx, currentAnnotation)
  }, [hoveredWindow, isDragging, dragStart, dragCurrent, selectedArea, annotations, currentAnnotation, tool])

  useEffect(() => {
    if (!ready) return
    drawOverlay()
  }, [drawOverlay])

  // ---------- magnifier ----------
  const updateMagnifier = useCallback((px: number, py: number) => {
    const oc = offscreenRef.current
    const mc = magRef.current
    if (!oc || !mc) return
    const mctx = mc.getContext('2d')
    if (!mctx) return
    const zoom = 8
    const half = 10
    const sz = half * 2
    mc.width = sz * zoom + 2
    mc.height = sz * zoom + 2
    mctx.imageSmoothingEnabled = false
    const sx = Math.max(0, Math.min(oc.width - sz, px - half))
    const sy = Math.max(0, Math.min(oc.height - sz, py - half))
    mctx.drawImage(oc, sx, sy, sz, sz, 1, 1, sz * zoom, sz * zoom)
    // crosshair
    const cx = mc.width / 2
    const cy = mc.height / 2
    mctx.strokeStyle = 'rgba(255,0,0,0.7)'
    mctx.lineWidth = 1
    mctx.beginPath(); mctx.moveTo(cx, 0); mctx.lineTo(cx, mc.height); mctx.stroke()
    mctx.beginPath(); mctx.moveTo(0, cy); mctx.lineTo(mc.width, cy); mctx.stroke()
    // border
    mctx.strokeStyle = '#333'; mctx.lineWidth = 2
    mctx.strokeRect(1, 1, sz * zoom, sz * zoom)
    // colour
    if (px >= 0 && py >= 0 && px < oc.width && py < oc.height) {
      const pd = oc.getContext('2d')!.getImageData(px, py, 1, 1).data
      const hex = '#' + [pd[0]!, pd[1]!, pd[2]!].map(v => v.toString(16).padStart(2, '0')).join('')
      setMagnifierColor(hex)
    }
  }, [])

  // ---------- mouse handlers ----------
  const getCanvasPos = useCallback((e: React.MouseEvent<HTMLCanvasElement>): Point => {
    const r = e.currentTarget.getBoundingClientRect()
    return { x: e.clientX - r.left, y: e.clientY - r.top }
  }, [])

  const handleMouseMove = useCallback((e: React.MouseEvent<HTMLCanvasElement>) => {
    const p = getCanvasPos(e)
    setMousePos(p)
    updateMagnifier(Math.round(p.x), Math.round(p.y))

    if (isDragging && dragStart) {
      setDragCurrent(p)
      if (tool && currentAnnotation) {
        setCurrentAnnotation(updateCurrentAnn(currentAnnotation, p))
      }
      return
    }

    // hover window detection (only when not dragging / no tool active)
    if (!isDragging && !selectedArea && !tool) {
      const w = windowsRef.current.find(
        (w) => p.x >= w.X && p.x <= w.X + w.Width && p.y >= w.Y && p.y <= w.Y + w.Height
      )
      setHoveredWindow(w ?? null)
    }
  }, [getCanvasPos, updateMagnifier, isDragging, dragStart, tool, currentAnnotation, selectedArea])

  const handleMouseDown = useCallback((e: React.MouseEvent<HTMLCanvasElement>) => {
    const p = getCanvasPos(e)
    if (tool) {
      // start annotation — don't touch selection or toolbar
      setIsDragging(true)
      setDragStart(p)
      setDragCurrent(p)
      if (tool === 'number') {
        const ann: Annotation = { type: 'number', x: p.x, y: p.y, num: numberCounter }
        setAnnotations(prev => [...prev, ann])
        setNumberCounter(n => n + 1)
        setIsDragging(false)
        return
      }
      if (tool === 'brush') {
        setCurrentAnnotation({ type: 'brush', points: [p] })
      } else {
        setCurrentAnnotation({ type: tool as 'rect' | 'ellipse' | 'line', rect: { x: p.x, y: p.y, w: 0, h: 0 }, p1: p, p2: p } as Annotation)
      }
    } else {
      // Start potential selection drag. Whether it becomes a window click or
      // a free-form rect is decided on mouse-up based on distance moved.
      setIsDragging(true)
      setDragStart(p)
      setDragCurrent(p)
      setSelectedArea(null)
      setShowToolbar(false)
    }
  }, [getCanvasPos, tool, numberCounter])

  const handleMouseUp = useCallback(async () => {
    if (isDragging && dragStart && dragCurrent && tool && currentAnnotation) {
      const final = finaliseAnnotation(currentAnnotation, dragCurrent)
      if (final) {
        setAnnotations(prev => [...prev, final])
      }
      setCurrentAnnotation(null)
    } else if (isDragging && dragStart && dragCurrent && !tool) {
      const dx = Math.abs(dragCurrent.x - dragStart.x)
      const dy = Math.abs(dragCurrent.y - dragStart.y)
      if (dx < 5 && dy < 5) {
        // click (no meaningful drag) — try to pick a window
        const w = windowsRef.current.find(
          (w) => dragStart.x >= w.X && dragStart.x <= w.X + w.Width &&
                 dragStart.y >= w.Y && dragStart.y <= w.Y + w.Height
        )
        if (w) {
          try {
            const refreshed = await refreshScreenshotForWindow(w.Handle)
            if (refreshed) {
              applyScreenshot(refreshed)
              const activeWindow = refreshed.Windows.find(candidate => candidate.Handle === w.Handle)
              if (activeWindow) {
                setSelectedArea({ x: activeWindow.X, y: activeWindow.Y, w: activeWindow.Width, h: activeWindow.Height })
                setShowToolbar(true)
              }
            }
          } catch (err) {
            setError(String(err))
          }
        }
      } else {
        // drag → free-form selection rect
        const x = Math.min(dragStart.x, dragCurrent.x)
        const y = Math.min(dragStart.y, dragCurrent.y)
        const w = Math.abs(dragCurrent.x - dragStart.x)
        const h = Math.abs(dragCurrent.y - dragStart.y)
        if (w >= 5 && h >= 5) {
          setSelectedArea({ x, y, w, h })
          setShowToolbar(true)
        }
      }
    }
    setIsDragging(false)
    setDragStart(null)
    setDragCurrent(null)
  }, [isDragging, dragStart, dragCurrent, tool, currentAnnotation, applyScreenshot])

  // ---------- keyboard ----------
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        closeScreenshotWindow()
        return
      }
      if (e.key === 'c' || e.key === 'C') {
        setClipboardText(magnifierColor)
        return
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [magnifierColor])

  // ---------- toolbar actions ----------
  const handleDownload = useCallback(async () => {
    const dataUrl = produceResultImage()
    const d = new Date()
    const name = `screenshot_${d.getFullYear()}${pad(d.getMonth()+1)}${pad(d.getDate())}_${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}.png`
    await saveScreenshotDialog(dataUrl, name)
    closeScreenshotWindow()
  }, [selectedArea, annotations])

  const handleConfirm = useCallback(async () => {
    const dataUrl = produceResultImage()
    // copy image to clipboard (prefers Go backend on Wails)
    await setClipboardImage(dataUrl)
    // send to main window via Wails events (async — must await before closing)
    await (Events.Emit as any)('screenshot-completed', dataUrl)
    closeScreenshotWindow()
  }, [selectedArea, annotations])

  const handleExit = useCallback(() => {
    closeScreenshotWindow()
  }, [])

  // ---------- produce final image ----------
  const produceResultImage = useCallback((): string => {
    const img = imageRef.current
    if (!img) return imageBase64Ref.current
    const w = img.naturalWidth
    const h = img.naturalHeight
    const c = document.createElement('canvas')

    // If user selected an area, crop to it; otherwise export full image.
    const crop = selectedArea
    let ox = 0, oy = 0
    if (crop) {
      ox = crop.x; oy = crop.y
      c.width = crop.w; c.height = crop.h
    } else {
      c.width = w; c.height = h
    }

    const ctx = c.getContext('2d')!
    ctx.drawImage(img, -ox, -oy)
    // draw annotations (offset when cropping)
    ctx.save()
    if (crop) ctx.translate(-ox, -oy)
    for (const a of annotations) drawAnnotation(ctx, a)
    ctx.restore()
    return c.toDataURL('image/png')
  }, [selectedArea, annotations])

  // ---------- render ----------
  if (!ready && !error) return <div style={{ background: 'transparent', width: '100vw', height: '100vh' }} />
  if (error) return (
    <div style={{ background: '#111', color: '#fff', width: '100vw', height: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', flexDirection: 'column' }}>
      <div style={{ fontSize: 18, marginBottom: 12 }}>{error}</div>
      <button onClick={() => closeScreenshotWindow()} style={{ padding: '8px 20px', background: '#e53935', color: '#fff', border: 'none', borderRadius: 4, cursor: 'pointer' }}>
        Close
      </button>
    </div>
  )

  return (
    <div style={{ position: 'fixed', inset: 0, background: 'transparent', cursor: tool ? 'crosshair' : 'default' }}>
      <img src={imageBase64Ref.current} style={{ position: 'absolute', top: 0, left: 0, width: '100%', height: '100%', objectFit: 'fill' }} />
      <canvas ref={overlayRef} style={{ position: 'absolute', top: 0, left: 0, width: '100%', height: '100%' }}
        onMouseMove={handleMouseMove}
        onMouseDown={handleMouseDown}
        onMouseUp={handleMouseUp}
      />
      {/* magnifier — hidden when toolbar is shown */}
      {!showToolbar && (() => {
        const MW = 170 // estimated magnifier block width
        const MH = 240 // estimated magnifier block height
        const gap = 24
        let ml = mousePos.x + gap
        let mt = mousePos.y + gap
        if (ml + MW > window.innerWidth) ml = mousePos.x - MW - gap
        if (mt + MH > window.innerHeight) mt = mousePos.y - MH - gap
        return (
        <div style={{ position: 'absolute', left: Math.max(0, ml), top: Math.max(0, mt), pointerEvents: 'none', zIndex: 10 }}>
          <canvas ref={magRef} style={{ display: 'block', borderRadius: 4, boxShadow: '0 2px 8px rgba(0,0,0,0.5)' }} />
          <div style={{ background: '#fff', color: '#111', fontFamily: 'monospace', fontSize: 13, padding: '2px 8px', borderRadius: 3, marginTop: 2, textAlign: 'center' }}>
            {magnifierColor}
          </div>
          <div style={{ color: '#aaa', fontSize: 11, textAlign: 'center', marginTop: 1 }}>{t('screenshot.copyColorHint')}</div>
        </div>
        )
      })()}
      {/* toolbar */}
      {showToolbar && selectedArea && (
        <ScreenshotToolbar
          rect={selectedArea}
          tool={tool}
          onToolChange={setTool}
          onDownload={handleDownload}
          onConfirm={handleConfirm}
          onExit={handleExit}
        />
      )}
      {/* hint */}
      {!showToolbar && !isDragging && (
        <div style={{ position: 'absolute', bottom: 16, left: '50%', transform: 'translateX(-50%)', background: 'rgba(0,0,0,0.65)', color: '#ccc', fontFamily: 'monospace', fontSize: 13, padding: '6px 16px', borderRadius: 6, pointerEvents: 'none' }}>
          {t('screenshot.toolbarHint')}
        </div>
      )}
    </div>
  )
}

// ---------- canvas drawing helpers ----------
function drawAnnotation(ctx: CanvasRenderingContext2D, a: Annotation) {
  ctx.save()
  ctx.strokeStyle = '#e53935'
  ctx.fillStyle = '#e53935'
  ctx.lineWidth = 2
  ctx.setLineDash([])
  switch (a.type) {
    case 'rect': {
      const { x, y, w, h } = a.rect
      ctx.strokeRect(x, y, w, h)
      break
    }
    case 'ellipse': {
      const { x, y, w, h } = a.rect
      const cx = x + w / 2; const cy = y + h / 2; const rx = Math.abs(w) / 2; const ry = Math.abs(h) / 2
      ctx.beginPath(); ctx.ellipse(cx, cy, rx, ry, 0, 0, Math.PI * 2); ctx.stroke()
      break
    }
    case 'line':
      ctx.beginPath(); ctx.moveTo(a.p1.x, a.p1.y); ctx.lineTo(a.p2.x, a.p2.y); ctx.stroke()
      break
    case 'brush':
      if (a.points.length === 1) {
        const { x, y } = a.points[0]!; ctx.fillRect(x - 1, y - 1, 3, 3)
      } else {
        ctx.beginPath(); ctx.moveTo(a.points[0]!.x, a.points[0]!.y)
        for (let i = 1; i < a.points.length; i++) ctx.lineTo(a.points[i]!.x, a.points[i]!.y)
        ctx.stroke()
      }
      break
    case 'number': {
      ctx.beginPath(); ctx.arc(a.x, a.y, 14, 0, Math.PI * 2); ctx.fill()
      ctx.fillStyle = '#fff'; ctx.font = 'bold 14px monospace'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle'
      ctx.fillText(String(a.num), a.x, a.y)
      break
    }
  }
  ctx.restore()
}

function updateCurrentAnn(curr: Annotation, p: Point): Annotation {
  switch (curr.type) {
    case 'rect':
    case 'ellipse': {
      const x = Math.min(curr.rect.x, p.x)
      const y = Math.min(curr.rect.y, p.y)
      return { ...curr, rect: { x, y, w: Math.abs(p.x - curr.rect.x), h: Math.abs(p.y - curr.rect.y) } }
    }
    case 'line':
      return { ...curr, p2: p }
    case 'brush':
      return { ...curr, points: [...curr.points, p] }
  }
  return curr
}

function finaliseAnnotation(curr: Annotation, _p: Point): Annotation | null {
  switch (curr.type) {
    case 'rect':
    case 'ellipse':
      if (Math.abs(curr.rect.w) < 5 && Math.abs(curr.rect.h) < 5) return null
      return curr
    case 'line':
      if (Math.abs(curr.p2.x - curr.p1.x) < 3 && Math.abs(curr.p2.y - curr.p1.y) < 3) return null
      return curr
    case 'brush':
      return curr
  }
  return curr
}

function pad(n: number) { return n.toString().padStart(2, '0') }
