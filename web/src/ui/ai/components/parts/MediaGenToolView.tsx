import React, { useEffect, useState } from 'react'
import type { ToolFrame } from '../../model/frame-types.ts'
import { ToolBodyFrame, ToolCodeBlock, ToolSection, ToolRunning } from './ToolViewPrimitives.tsx'
import { isImageExt, mimeFromExt } from './image-utils.ts'
import { base64ToBytes } from '../ImageViewer.tsx'
import * as filesystemClient from '../../../../gen-clients/filesystem/client'
import { client } from '../../../../application/generated-client'
import { useAIShellContext } from '../../context/AIShellContext'

const VIDEO_EXTS = new Set(['mp4', 'webm', 'mov', 'm4v', 'avi', 'mkv'])

function extOf(path: string): string {
  return path.split('.').pop()?.toLowerCase() ?? ''
}

export function isVideoExt(path: string): boolean {
  return VIDEO_EXTS.has(extOf(path))
}

/**
 * Extract a generated-media absolute file path from a tool output string.
 * The output of generate_image / generate_video is a bare absolute path
 * (e.g. D:\dev\sporemind\assets\generated\img_123.png). Returns null when
 * the output is not a single media file path.
 */
export function extractMediaPath(output: string | undefined): string | null {
  if (!output) return null
  const trimmed = output.trim()
  if (!trimmed || trimmed.includes('\n')) return null
  // Absolute Windows (D:\...) or Unix (/...) path to a media file.
  if (!/^(?:[a-zA-Z]:[\\/]|\/)/.test(trimmed)) return null
  if (!isImageExt(trimmed) && !isVideoExt(trimmed)) return null
  return trimmed
}

interface MediaGenToolViewProps {
  frame: ToolFrame
}

/**
 * Dedicated view for generate_image / generate_video tool results.
 * When the output is an absolute path to an image or video file, the media is
 * rendered inline (image thumbnail / video player) via readBase64 → blob URL,
 * so the agent never needs to call show_page_thumbnail afterwards.
 */
export const MediaGenToolView: React.FC<MediaGenToolViewProps> = ({ frame }) => {
  const isRunning = frame.status === 'running'
  const output = frame.output
  const mediaPath = isRunning ? null : extractMediaPath(output)
  const isVideo = mediaPath != null && isVideoExt(mediaPath)
  const shell = useAIShellContext()

  const [blobUrl, setBlobUrl] = useState('')
  const [loadError, setLoadError] = useState('')

  useEffect(() => {
    if (!mediaPath) {
      setBlobUrl('')
      setLoadError('')
      return
    }
    let cancelled = false
    let created = ''
    // file:// is blocked by WebView2 cross-scheme policy — load through the
    // filesystem service and hand the browser a blob URL instead.
    filesystemClient.readBase64(client, { Path: mediaPath })
      .then(resp => {
        if (cancelled || !resp.Content) return
        const bytes = base64ToBytes(resp.Content)
        const blob = new Blob([bytes], { type: mimeFromExt(mediaPath) })
        created = URL.createObjectURL(blob)
        setBlobUrl(created)
      })
      .catch(err => {
        if (cancelled) return
        setLoadError(err instanceof Error ? err.message : String(err))
        setBlobUrl('')
      })
    return () => {
      cancelled = true
      if (created) URL.revokeObjectURL(created)
    }
  }, [mediaPath])

  const handleOpen = () => {
    if (mediaPath && shell.onOpenFile) shell.onOpenFile(mediaPath)
  }

  return (
    <ToolBodyFrame frame={frame}>
      <ToolSection label="Input">
        <ToolCodeBlock>{frame.input}</ToolCodeBlock>
      </ToolSection>

      {isRunning && <ToolRunning label={<>Running {frame.toolName}…</>} />}

      {mediaPath && !isRunning && (
        <ToolSection label="Output">
          <div className="ai-media-gen-result">
            <div
              className="ai-media-gen-preview"
              role={isVideo ? undefined : 'button'}
              tabIndex={isVideo ? undefined : 0}
              title={mediaPath}
              onClick={isVideo ? undefined : handleOpen}
              onKeyDown={isVideo ? undefined : e => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  handleOpen()
                }
              }}
            >
              {blobUrl ? (
                isVideo ? (
                  <video className="ai-media-gen-video" src={blobUrl} controls preload="metadata" />
                ) : (
                  <img className="ai-media-gen-image" src={blobUrl} alt="" loading="lazy" />
                )
              ) : (
                <div className="ai-media-gen-pending">{loadError || '…'}</div>
              )}
            </div>
            <div className="ai-media-gen-path">{mediaPath}</div>
          </div>
        </ToolSection>
      )}

      {!mediaPath && output && !isRunning && (
        <ToolSection label={frame.status === 'error' ? 'Error' : 'Output'}>
          <ToolCodeBlock error={frame.status === 'error'}>{output}</ToolCodeBlock>
        </ToolSection>
      )}
    </ToolBodyFrame>
  )
}
