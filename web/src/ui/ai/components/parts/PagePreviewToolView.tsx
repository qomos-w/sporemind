import React, { useState, useEffect } from 'react'
import { FileText, Globe } from 'lucide-react'
import type { ToolFrame } from '../../model/frame-types.ts'
import type { AgentShowPageThumbnailReq, AgentShowPageThumbnailResp } from '../../../../gen-types/agent'
import { parseJsonObject } from './tool-display.ts'
import { ToolBodyFrame, ToolRunning } from './ToolViewPrimitives.tsx'
import * as agentClient from '../../../../gen-clients/local/client'
import * as filesystemClient from '../../../../gen-clients/filesystem/client'
import { client } from '../../../../application/generated-client'
import { useI18n } from '../../../../i18n/provider'
import { isImageExt } from './image-utils'
import { base64ToBytes } from '../ImageViewer.tsx'
import { materializeHtml, fsAssetIO } from './local-html-embed.ts'

const PREVIEW_SCALE = 0.25

const IMAGE_EXTS = /\.(png|jpe?g|gif|webp|svg|bmp|ico|avif)(\?|#|$)/i

function isImageURL(url: string): boolean {
  return IMAGE_EXTS.test(url)
}

function isLocalFileURL(url: string): boolean {
  return /^file:/i.test(url)
}

function isWebURL(url: string): boolean {
  return /^https?:\/\//i.test(url)
}

function hostnameOf(url: string): string {
  try { return new URL(url).hostname } catch { return url }
}

function faviconURL(url: string): string {
  try {
    const u = new URL(url)
    return `https://www.google.com/s2/favicons?domain=${u.hostname}&sz=64`
  } catch { return '' }
}

function isLocalhostURL(url: string): boolean {
  try {
    const u = new URL(url)
    return u.hostname === 'localhost' || u.hostname === '127.0.0.1' || u.hostname === '0.0.0.0'
  } catch { return false }
}

// Local-file target: the previewed content is a local file (backend resolved it
// to file:// or returned FilePath). These render as a full-width non-interactive
// embed; clicking opens the page in the right-side global browser tab.
function isLocalFileTarget(url: string, filePath: string): boolean {
  return isLocalFileURL(url) || !!filePath
}

// Basename / directory of a local file path, e.g. "F:/dev/sporemind/docs/a.html".
function splitFilePath(filePath: string): { name: string; dir: string } {
  const norm = filePath.split('\\').join('/')
  const idx = norm.lastIndexOf('/')
  if (idx < 0) return { name: norm, dir: '' }
  return { name: norm.slice(idx + 1), dir: norm.slice(0, idx) }
}

function mimeFromExt(url: string): string {
  const m = url.match(IMAGE_EXTS)
  if (!m) return 'image/png'
  const ext = m[1]?.toLowerCase()
  if (!ext) return 'image/png'
  const map: Record<string, string> = {
    png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg',
    gif: 'image/gif', webp: 'image/webp', svg: 'image/svg+xml',
    bmp: 'image/bmp', ico: 'image/x-icon', avif: 'image/avif',
  }
  return map[ext] || 'image/png'
}

interface PagePreviewToolViewProps {
  frame: ToolFrame
  agentActorId?: string
}

export const PagePreviewToolView: React.FC<PagePreviewToolViewProps> = ({ frame, agentActorId }) => {
  const { t } = useI18n()
  const input = parseJsonObject(frame.input) as AgentShowPageThumbnailReq | null
  const output = frame.output ? (parseJsonObject(frame.output) as AgentShowPageThumbnailResp | null) : null

  const url = output?.Url || input?.Url || ''
  const filePath = output?.FilePath || ''
  const toolError = output?.Error || (frame.status === 'error' ? frame.output ?? '' : '')

  const [opening, setOpening] = useState(false)
  const [openError, setOpenError] = useState('')
  const [blobUrl, setBlobUrl] = useState('')
  const [loadFailed, setLoadFailed] = useState(false)

  const isImage = isImageURL(url) || isImageExt(filePath)
  const isHtml = /\.s?html?(\?|#|$)/i.test(url) || /\.s?html?$/i.test(filePath)

  // file:// URLs cannot be loaded from an http(s)/wails page (Chromium blocks
  // cross-scheme subresource loads), so local files are read through the
  // filesystem actor and rendered from a blob URL instead.
  useEffect(() => {
    if (!filePath || !(isImage || isHtml)) {
      setBlobUrl('')
      setLoadFailed(false)
      return
    }
    let cancelled = false
    let created = ''
    const done = (blob: Blob | null) => {
      if (cancelled) return
      if (blob) {
        created = URL.createObjectURL(blob)
        setBlobUrl(created)
      } else {
        setLoadFailed(true)
      }
    }
    if (isImage) {
      filesystemClient.readBase64(client, { Path: filePath })
        .then(resp => {
          if (cancelled || !resp.Content) return done(null)
          const bytes = base64ToBytes(resp.Content)
          done(new Blob([bytes], { type: mimeFromExt(url) }))
        })
        .catch(() => done(null))
    } else {
      filesystemClient.read(client, { Path: filePath })
        .then(async resp => {
          if (cancelled || !resp.Content) return done(null)
          // Rewrite relative subresources (css/js/img) to data: URLs — the
          // blob page has no path, and sandboxed iframes cannot fetch blob:
          // subresources cross-origin; without this the preview renders
          // unstyled.
          const dir = splitFilePath(filePath).dir
          const res = await materializeHtml(resp.Content, dir, fsAssetIO)
          if (cancelled) return
          done(new Blob([res.html], { type: 'text/html;charset=utf-8' }))
        })
        .catch(() => done(null))
    }
    return () => {
      cancelled = true
      if (created) URL.revokeObjectURL(created)
    }
  }, [filePath, url, isImage, isHtml])

  const handleOpen = async () => {
    if (!url || opening) return
    if (!agentActorId) {
      window.open(url, '_blank', 'noopener,noreferrer')
      return
    }
    setOpening(true)
    setOpenError('')
    try {
      const resp = await agentClient.openGlobalBrowser(client, { Url: url }, { target: agentActorId })
      if (!resp.Opened) setOpenError(resp.Error || t('ai.tool.openInBrowser'))
    } catch (err) {
      setOpenError(err instanceof Error ? err.message : String(err))
    } finally {
      setOpening(false)
    }
  }

  const openInTabProps = {
    role: 'button' as const,
    tabIndex: 0,
    title: t('ai.tool.openInBrowser'),
    onClick: handleOpen,
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        handleOpen()
      }
    },
  }

  // Full-width embed targets: local files and local dev servers (localhost /
  // 127.0.0.1 pages load fine cross-origin in an iframe, unlike public sites
  // with X-Frame-Options).
  const isLocalFile = isLocalFileTarget(url, filePath)
  const isLocalDev = isWebURL(url) && isLocalhostURL(url)

  if ((isLocalFile || isLocalDev) && url) {
    // Full-width non-interactive render; the whole surface is a click target
    // that opens the page in the right-side global browser tab.
    const fileSplit = splitFilePath(filePath || url)
    const chrome = isLocalDev
      ? (() => {
          try {
            const u = new URL(url)
            return { name: `${u.hostname}${u.port ? `:${u.port}` : ''}`, dir: u.pathname + u.search }
          } catch { return { name: url, dir: '' } }
        })()
      : { name: fileSplit.name, dir: fileSplit.dir }
    const loadingText = loadFailed ? t('ai.tool.pagePreviewLoadFailed') : t('ai.tool.pagePreviewLoading')
    return (
      <ToolBodyFrame frame={frame}>
        <div className="ai-page-embed" {...openInTabProps}>
          <div className="ai-page-embed-chrome">
            {isLocalDev ? (
              <Globe size={14} className="ai-page-embed-file-icon" aria-hidden />
            ) : (
              <FileText size={14} className="ai-page-embed-file-icon" aria-hidden />
            )}
            <div className="ai-page-embed-titles">
              <span className="ai-page-embed-name" title={chrome.name}>{chrome.name}</span>
              {chrome.dir && <span className="ai-page-embed-dir" title={chrome.dir}>{chrome.dir}</span>}
            </div>
          </div>
          {isImage ? (
            <div className="ai-page-embed-image-wrap">
              {blobUrl ? (
                <img className="ai-page-embed-image" src={blobUrl} alt={chrome.name} />
              ) : filePath ? (
                <span className="ai-page-embed-loading">{loadingText}</span>
              ) : null}
            </div>
          ) : isLocalDev ? (
            // Dev server pages are served over http and load directly; render
            // is non-interactive (pointer-events:none in CSS).
            <div className="ai-page-embed-frame-wrap">
              <iframe className="ai-page-embed-frame" src={url} sandbox="allow-scripts" title={chrome.name} />
            </div>
          ) : (
            <div className="ai-page-embed-frame-wrap">
              {blobUrl ? (
                // Non-interactive render-only viewport (pointer-events:none in
                // CSS); scripts stay enabled so JS-rendered pages still paint.
                <iframe
                  className="ai-page-embed-frame"
                  src={blobUrl}
                  sandbox="allow-scripts"
                  title={chrome.name}
                />
              ) : filePath ? (
                <div className="ai-page-embed-loading">{loadingText}</div>
              ) : (
                <iframe className="ai-page-embed-frame" src={url} title={chrome.name} />
              )}
            </div>
          )}
        </div>
        {(toolError || openError) && (
          <div className="ai-page-preview-error">{toolError || openError}</div>
        )}
        {frame.status === 'running' && <ToolRunning />}
      </ToolBodyFrame>
    )
  }

  // Fallback: clickable thumbnail card (public web URLs with X-Frame-Options).
  const showViewport = isImageURL(url)

  return (
    <ToolBodyFrame frame={frame}>
      {url && (
        <div
          className="ai-page-preview-card"
          role="button"
          tabIndex={0}
          title={url}
          onClick={handleOpen}
          onKeyDown={openInTabProps.onKeyDown}
        >
          {showViewport && (
            <div className="ai-page-preview-viewport">
              {isImageURL(url) ? (
                <img
                  className="ai-page-preview-image"
                  src={blobUrl || undefined}
                  alt=""
                  loading="lazy"
                  tabIndex={-1}
                  aria-hidden="true"
                />
              ) : (
                <iframe
                  className="ai-page-preview-frame"
                  src={url}
                  loading="lazy"
                  tabIndex={-1}
                  aria-hidden="true"
                  style={{ transform: `scale(${PREVIEW_SCALE})` }}
                />
              )}
            </div>
          )}
          {isWebURL(url) && (
            <div className="ai-page-preview-meta">
              <span className="ai-page-preview-link">
                <img className="ai-page-preview-favicon" src={faviconURL(url)} alt="" onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = 'none' }} />
                <span className="ai-page-preview-label">{hostnameOf(url)}</span>
              </span>
            </div>
          )}
        </div>
      )}
      {(toolError || openError) && (
        <div className="ai-page-preview-error">{toolError || openError}</div>
      )}
      {frame.status === 'running' && <ToolRunning />}
    </ToolBodyFrame>
  )
}
