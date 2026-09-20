import React from 'react'
import type { Frame, TimelineConnectorMode } from '../../model/frame-types'

export function frameHasTimelineIcon(frame: Frame): boolean {
  return frame.type !== 'text'
}

export function connectorModeForFrame(
  frames: Frame[],
  index: number,
): TimelineConnectorMode {
  const frame = frames[index]
  const nextFrame = frames[index + 1]
  if (!frame || !nextFrame) return 'none'
  if (!frameHasTimelineIcon(frame) || !frameHasTimelineIcon(nextFrame)) return 'none'
  return 'to-next-icon'
}

export function formatTimestamp(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  return `${month}-${day} ${hours}:${minutes}`
}

export function formatTimestampFull(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  const seconds = String(date.getSeconds()).padStart(2, '0')
  return `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`
}

export function formatTokenCount(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}m`
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return String(n)
}

export function formatDurationBadge(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const remaining = seconds % 60
  return remaining > 0 ? `${minutes}m ${remaining}s` : `${minutes}m`
}

export function truncateLabel(text: string, max = 80): string {
  if (max <= 0) return text
  const chars = Array.from(text)
  if (chars.length <= max) return text
  return chars.slice(0, max).join('') + '…'
}

export function splitTextToWaveChars(node: React.ReactNode, staggerMs = 50): React.ReactNode {
  if (typeof node === 'string') {
    return node.split('').map((char, i) => (
      <span
        key={i}
        className="ai-step-label-char"
        style={{ animationDelay: `${i * staggerMs}ms` }}
      >
        {char === ' ' ? ' ' : char}
      </span>
    ))
  }

  if (typeof node === 'number') {
    return String(node).split('').map((char, i) => (
      <span key={i} className="ai-step-label-char" style={{ animationDelay: `${i * staggerMs}ms` }}>
        {char}
      </span>
    ))
  }

  if (React.isValidElement(node)) {
    const children = (node.props as { children?: React.ReactNode }).children
    if (children == null) return node
    const processed = Array.isArray(children)
      ? children.map((c) => splitTextToWaveChars(c, staggerMs))
      : splitTextToWaveChars(children, staggerMs)
    return React.cloneElement(node, undefined, processed)
  }

  if (Array.isArray(node)) {
    return node.map((child, i) => (
      <React.Fragment key={i}>{splitTextToWaveChars(child, staggerMs)}</React.Fragment>
    ))
  }

  return node
}
