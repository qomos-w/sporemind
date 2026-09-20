import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ZoomIn,
  ZoomOut,
  Maximize2,
  RotateCcw,
  RotateCw,
  FlipHorizontal2,
  FlipVertical2,
  Eye,
  Binary,
  Save,
} from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as filesystem from '../../../gen-clients/filesystem/client'
import { mimeFromExt } from './parts/image-utils'
import { useI18n } from '../../../i18n'
import { HexView, type HexViewLabels } from './HexView'
import './ImageViewer.css'

// ---------------------------------------------------------------------------
// Pure helpers (exported for unit testing)
// ---------------------------------------------------------------------------

/** Max characters fed into a single `String.fromCharCode(...)` call to stay
 *  well under engine argument limits even for large images. */
const B64_CHUNK = 0x8000

/** Decode a base64 string (with or without padding/whitespace) into bytes. */
export function base64ToBytes(b64: string): Uint8Array {
  const clean = b64.replace(/\s+/g, '')
  const bin = atob(clean)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i) & 0xff
  return out
}

/** Encode bytes into a base64 string. */
export function bytesToBase64(bytes: Uint8Array): string {
  let bin = ''
  for (let i = 0; i < bytes.length; i += B64_CHUNK) {
    bin += String.fromCharCode(...bytes.subarray(i, i + B64_CHUNK))
  }
  return btoa(bin)
}

/** Structural byte equality for dirty detection (null-safe). */
export function bytesEqual(a: Uint8Array | null, b: Uint8Array | null): boolean {
  if (a === b) return true
  if (!a || !b) return false
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false
  }
  return true
}

export interface ParsedDataUrl {
  mime: string
  bytes: Uint8Array
}

/** Parse a `data:<mime>;base64,<payload>` (or percent-encoded) URL into bytes.
 *  Returns null when `url` is not a data URL (e.g. a remote http(s) URL). */
export function parseDataUrl(url: string): ParsedDataUrl | null {
  const m = /^data:([^;,]+)?(;base64)?,(.*)$/is.exec(url)
  if (!m) return null
  const mime = (m[1] ?? 'application/octet-stream').toLowerCase()
  const isBase64 = !!m[2]
  const data = m[3] ?? ''
  if (isBase64) return { mime, bytes: base64ToBytes(data) }
  let decoded = data
  try {
    decoded = decodeURIComponent(data)
  } catch {
    // keep raw payload if it is not valid percent-encoding
  }
  return { mime, bytes: new TextEncoder().encode(decoded) }
}

// ---------------------------------------------------------------------------
// Internal canvas helpers (not exported — exercised in-browser only)
// ---------------------------------------------------------------------------

function loadImageElement(src: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve(img)
    img.onerror = () => reject(new Error('Failed to decode image'))
    img.src = src
  })
}

function canvasToBlob(canvas: HTMLCanvasElement, type = 'image/png'): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob(
      b => (b ? resolve(b) : reject(new Error('Canvas export failed'))),
      type,
    )
  })
}

interface TransformSpec {
  /** Output canvas dimensions derived from the decoded image. */
  size: (img: HTMLImageElement) => { w: number; h: number }
  /** Draw the decoded image onto a freshly sized, cleared canvas. */
  draw: (ctx: CanvasRenderingContext2D, img: HTMLImageElement) => void
}

const ROTATE_LEFT: TransformSpec = {
  size: img => ({ w: img.naturalHeight, h: img.naturalWidth }),
  draw: (ctx, img) => {
    ctx.translate(0, img.naturalWidth)
    ctx.rotate(-Math.PI / 2)
    ctx.drawImage(img, 0, 0)
  },
}

const ROTATE_RIGHT: TransformSpec = {
  size: img => ({ w: img.naturalHeight, h: img.naturalWidth }),
  draw: (ctx, img) => {
    ctx.translate(img.naturalHeight, 0)
    ctx.rotate(Math.PI / 2)
    ctx.drawImage(img, 0, 0)
  },
}

const FLIP_H: TransformSpec = {
  size: img => ({ w: img.naturalWidth, h: img.naturalHeight }),
  draw: (ctx, img) => {
    ctx.translate(img.naturalWidth, 0)
    ctx.scale(-1, 1)
    ctx.drawImage(img, 0, 0)
  },
}

const FLIP_V: TransformSpec = {
  size: img => ({ w: img.naturalWidth, h: img.naturalHeight }),
  draw: (ctx, img) => {
    ctx.translate(0, img.naturalHeight)
    ctx.scale(1, -1)
    ctx.drawImage(img, 0, 0)
  },
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface ImageViewerProps {
  /** Project-relative or absolute file path; loaded via filesystem.read_base64. */
  filePath?: string
  /** Absolute root of the owning project. When set, a relative `filePath` is
   *  resolved against it so reads/writes hit the project's own file rather than
   *  the primary mount the global filesystem actor defaults to. */
  projectRootPath?: string
  /** Direct image source (data URL). Takes precedence over filePath. */
  src?: string
  /** Accessible label for direct image sources. */
  alt?: string
  /** Compact rendering for inline conversation content. */
  inline?: boolean
}

const ZOOM_STEP = 1.25
const ZOOM_MIN = 0.05
const ZOOM_MAX = 16

type ViewerMode = 'preview' | 'hex'

export const ImageViewer: React.FC<ImageViewerProps> = ({ filePath, projectRootPath, src, alt, inline = false }) => {
  const { t } = useI18n()

  // Single source of truth: decoded image bytes (null until loaded).
  const [bytes, setBytes] = useState<Uint8Array | null>(null)
  // Snapshot used for dirty detection and hex edit highlighting.
  const [originalBytes, setOriginalBytes] = useState<Uint8Array | null>(null)
  const [mime, setMime] = useState<string>('image/png')
  // Fallback preview source when `src` is not a decodable data URL.
  const [directSrc, setDirectSrc] = useState<string | null>(null)
  const [mode, setMode] = useState<ViewerMode>('preview')
  // null zoom = fit-to-panel mode.
  const [zoom, setZoom] = useState<number | null>(null)
  const [naturalWidth, setNaturalWidth] = useState(0)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [savedFlash, setSavedFlash] = useState(false)
  const [previewUrl, setPreviewUrl] = useState<string | null>(null)
  const savedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // The global filesystem actor resolves relative paths against the primary
  // mount only; a browser-opened image in a sibling project would otherwise
  // read/write the wrong same-named file. Resolve against this tab's root.
  const resolvedPath = useMemo(() => {
    if (!filePath || !projectRootPath) return filePath
    if (filePath.startsWith('/') || /^[a-zA-Z]:[\\/]/.test(filePath)) return filePath
    return `${projectRootPath.replace(/[\\/]+$/, '')}/${filePath}`
  }, [filePath, projectRootPath])

  // ---- load -------------------------------------------------------------
  useEffect(() => {
    setZoom(null)
    setNaturalWidth(0)
    setLoadError(null)
    setSaveError(null)
    setDirectSrc(null)
    if (inline) {
      setBytes(null)
      setOriginalBytes(null)
      return
    }
    if (src) {
      const parsed = parseDataUrl(src)
      if (parsed) {
        setBytes(parsed.bytes)
        setOriginalBytes(parsed.bytes)
        setMime(parsed.mime)
      } else {
        // Remote / non-data URL: preview only, no byte editing.
        setBytes(null)
        setOriginalBytes(null)
        setDirectSrc(src)
      }
      return
    }
    if (!filePath || !resolvedPath) {
      setBytes(null)
      setOriginalBytes(null)
      return
    }
    let cancelled = false
    setLoading(true)
    setBytes(null)
    setOriginalBytes(null)
    const initialMime = mimeFromExt(filePath)
    filesystem
      .readBase64(client, { Path: resolvedPath })
      .then(resp => {
        if (cancelled) return
        const b = base64ToBytes(resp.Content)
        setBytes(b)
        setOriginalBytes(b)
        setMime(initialMime)
        setLoading(false)
      })
      .catch(e => {
        if (cancelled) return
        setLoadError(e instanceof Error ? e.message : String(e))
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [resolvedPath, src, inline])

  // ---- preview url derived from current bytes --------------------------
  useEffect(() => {
    if (inline || !bytes) {
      setPreviewUrl(null)
      return
    }
    const blob = new Blob([bytes], { type: mime || 'image/png' })
    const url = URL.createObjectURL(blob)
    setPreviewUrl(url)
    return () => URL.revokeObjectURL(url)
  }, [bytes, mime, inline])

  const dirty = useMemo(
    () => bytes != null && !bytesEqual(bytes, originalBytes),
    [bytes, originalBytes],
  )
  const canSave = !!filePath && dirty && !busy && !saving

  // ---- clear saved-flash timer on unmount -------------------------------
  useEffect(() => {
    return () => {
      if (savedTimer.current) clearTimeout(savedTimer.current)
    }
  }, [])

  // ---- image transform (async + busy) ----------------------------------
  const applyTransform = useCallback(
    async (spec: TransformSpec) => {
      if (!bytes || busy) return
      setBusy(true)
      setLoadError(null)
      const blobUrl = URL.createObjectURL(new Blob([bytes], { type: mime || 'image/png' }))
      try {
        const img = await loadImageElement(blobUrl)
        const canvas = document.createElement('canvas')
        const { w, h } = spec.size(img)
        canvas.width = Math.max(1, w)
        canvas.height = Math.max(1, h)
        const ctx = canvas.getContext('2d')
        if (!ctx) throw new Error('Canvas 2D context unavailable')
        spec.draw(ctx, img)
        const outBlob = await canvasToBlob(canvas)
        const arr = new Uint8Array(await outBlob.arrayBuffer())
        setBytes(arr)
        setMime('image/png')
      } catch (e) {
        setLoadError(e instanceof Error ? e.message : String(e))
      } finally {
        setBusy(false)
        URL.revokeObjectURL(blobUrl)
      }
    },
    [bytes, mime, busy],
  )

  // ---- save -------------------------------------------------------------
  const handleSave = useCallback(async () => {
    if (!filePath || !resolvedPath || !bytes || !dirty || saving) return
    setSaving(true)
    setSaveError(null)
    try {
      await filesystem.writeBase64(client, { Path: resolvedPath, Content: bytesToBase64(bytes) })
      setOriginalBytes(bytes)
      setSavedFlash(true)
      if (savedTimer.current) clearTimeout(savedTimer.current)
      savedTimer.current = setTimeout(() => setSavedFlash(false), 2000)
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }, [resolvedPath, bytes, dirty, saving])

  const applyZoom = (next: number) => setZoom(Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, next)))
  const baseWidth = naturalWidth || 800
  const name = filePath?.split('/').pop() ?? ''
  const imgSrc = previewUrl ?? directSrc
  const hexLabels = useMemo<HexViewLabels>(
    () => ({ pageOf: t('ai.imageViewer.hexPage') }),
    [t],
  )

  // ---- inline mode: unchanged direct render ----------------------------
  if (inline) {
    return (
      <div className="ai-image-viewer inline">
        <div className="ai-image-viewer-body">
          {src && <img src={src} alt={alt ?? name} className="ai-image-viewer-img fit" />}
        </div>
      </div>
    )
  }

  return (
    <div className="ai-image-viewer">
      <div className="ai-image-viewer-toolbar">
        <div className="ai-image-viewer-mode-group">
          <button
            type="button"
            className={`ai-image-viewer-mode-btn${mode === 'preview' ? ' active' : ''}`}
            title={t('ai.imageViewer.modePreview')}
            aria-pressed={mode === 'preview'}
            onClick={() => setMode('preview')}
          >
            <Eye size={14} />
          </button>
          <button
            type="button"
            className={`ai-image-viewer-mode-btn${mode === 'hex' ? ' active' : ''}`}
            title={t('ai.imageViewer.modeHex')}
            aria-pressed={mode === 'hex'}
            onClick={() => setMode('hex')}
          >
            <Binary size={14} />
          </button>
          {dirty && (
            <span
              className="ai-image-viewer-dirty-dot"
              title={t('ai.imageViewer.unsaved')}
              aria-label={t('ai.imageViewer.unsaved')}
            />
          )}
        </div>
        <span className="ai-image-viewer-name" title={filePath ?? ''}>{name}</span>
        <div className="ai-image-viewer-actions">
          {mode === 'preview' && (
            <>
              <button type="button" title={t('ai.imageViewer.zoomOut')} onClick={() => applyZoom((zoom ?? 1) / ZOOM_STEP)}>
                <ZoomOut size={14} />
              </button>
              <span className="ai-image-viewer-zoom">
                {zoom == null ? t('ai.imageViewer.fit') : `${Math.round(zoom * 100)}%`}
              </span>
              <button type="button" title={t('ai.imageViewer.zoomIn')} onClick={() => applyZoom((zoom ?? 1) * ZOOM_STEP)}>
                <ZoomIn size={14} />
              </button>
              <button type="button" title={t('ai.imageViewer.actualSize')} onClick={() => setZoom(1)}>
                1:1
              </button>
              <button type="button" title={t('ai.imageViewer.fit')} onClick={() => setZoom(null)}>
                <Maximize2 size={14} />
              </button>
              <span className="ai-image-viewer-divider" />
              <button
                type="button"
                title={t('ai.imageViewer.rotateLeft')}
                disabled={busy || !bytes}
                onClick={() => applyTransform(ROTATE_LEFT)}
              >
                <RotateCcw size={14} />
              </button>
              <button
                type="button"
                title={t('ai.imageViewer.rotateRight')}
                disabled={busy || !bytes}
                onClick={() => applyTransform(ROTATE_RIGHT)}
              >
                <RotateCw size={14} />
              </button>
              <button
                type="button"
                title={t('ai.imageViewer.flipH')}
                disabled={busy || !bytes}
                onClick={() => applyTransform(FLIP_H)}
              >
                <FlipHorizontal2 size={14} />
              </button>
              <button
                type="button"
                title={t('ai.imageViewer.flipV')}
                disabled={busy || !bytes}
                onClick={() => applyTransform(FLIP_V)}
              >
                <FlipVertical2 size={14} />
              </button>
            </>
          )}
          <button
            type="button"
            className={`ai-image-viewer-save${savedFlash ? ' saved' : ''}`}
            title={t('ai.imageViewer.save')}
            disabled={!canSave}
            onClick={handleSave}
          >
            <Save size={14} />
            <span>{savedFlash ? t('ai.imageViewer.saved') : t('ai.imageViewer.save')}</span>
          </button>
        </div>
      </div>
      <div className={`ai-image-viewer-body${mode === 'hex' ? ' hex-mode' : ''}`}>
        {loadError && (
          <div className="ai-image-viewer-error">
            {t('ai.imageViewer.loadError')}: {loadError}
          </div>
        )}
        {saveError && (
          <div className="ai-image-viewer-save-error">
            {t('ai.imageViewer.saveError')}: {saveError}
          </div>
        )}
        {mode === 'hex' ? (
          <div className="ai-image-viewer-hex">
            <HexView
              bytes={bytes ?? new Uint8Array(0)}
              originalBytes={originalBytes ?? undefined}
              onChange={next => setBytes(next)}
              readOnly={!bytes}
              labels={hexLabels}
            />
          </div>
        ) : (
          <>
            {!loadError && !imgSrc && (
              <div className="ai-image-viewer-loading">
                {loading ? t('common.loading') : ''}
              </div>
            )}
            {imgSrc && (
              <img
                src={imgSrc}
                alt={alt ?? name}
                className={zoom == null ? 'ai-image-viewer-img fit' : 'ai-image-viewer-img'}
                style={zoom == null ? undefined : { width: Math.round(baseWidth * zoom) }}
                onLoad={e => setNaturalWidth(e.currentTarget.naturalWidth)}
              />
            )}
            {busy && <div className="ai-image-viewer-busy">{t('common.loading')}</div>}
          </>
        )}
      </div>
    </div>
  )
}
