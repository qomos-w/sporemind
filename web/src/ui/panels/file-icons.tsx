import React from 'react'
import { atomFileIcons, atomFolderIcons, atomFolderOpenIcons, atomExtMap, atomNameMap, atomFolderNameMap } from './atom-icons/iconData'

const S = 22

type IconData = { viewBox: string; inner: string }

// Clean white file background (no text lines)
const WHITE_FILE_BASE = `<path fill="#ffffff" stroke="#b0bec5" stroke-width="0.5"
  d="M6,2H14L20,8V20A2,2 0 0,1 18,22H6C4.89,22 4,21.1 4,20V4C4,2.89 4.89,2 6,2Z"/>
  <path fill="#cfd8dc" d="M14,2L20,8H16A2,2 0 0,1 14,6V2Z"/>`

// White default file with text lines (for unknown types)
const DEFAULT_FILE_INNER = WHITE_FILE_BASE +
  `<path fill="#b0bec5" opacity="0.4" d="M7,12H17V13H7Z M7,15H17V16H7Z M7,18H14V19H7Z"/>`

// Yellow default folder icons (Windows Explorer style)
const DEFAULT_FOLDER_INNER = `<path fill="#dcb67a"
  d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>`

const DEFAULT_FOLDER_OPEN_INNER = `<path fill="#dcb67a"
  d="M20,18H4V8H20M20,6H12L10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6Z"/>`

function renderSvg(inner: string, viewBox = '0 0 24 24'): React.ReactNode {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={S}
      height={S}
      viewBox={viewBox}
      dangerouslySetInnerHTML={{ __html: inner }}
    />
  )
}

function renderIconData(data: IconData): React.ReactNode {
  return renderSvg(data.inner, data.viewBox)
}

function getExt(fileName: string): string {
  const lower = fileName.toLowerCase()
  const dotIdx = lower.lastIndexOf('.')
  if (dotIdx < 0) return ''
  const parts = lower.split('.')
  for (let i = 1; i < parts.length; i++) {
    const candidate = parts.slice(i).join('.')
    if (atomExtMap[candidate]) return candidate
  }
  return lower.slice(dotIdx + 1)
}

function lookupFileIcon(name: string): IconData | null {
  const lower = name.toLowerCase()
  const nameIcon = atomNameMap[lower]
  if (nameIcon) {
    const data = atomFileIcons[nameIcon]
    if (data) return data
  }
  const ext = getExt(name)
  const iconName = atomExtMap[ext]
  if (iconName) {
    const data = atomFileIcons[iconName]
    if (data) return data
  }
  return null
}

/** Standalone type icon (tree view) */
export function getFileIcon(name: string): React.ReactNode {
  const data = lookupFileIcon(name)
  if (data) return renderIconData(data)
  return renderSvg(DEFAULT_FILE_INNER)
}

/** White file base + type icon overlay (list/grid view) */
export function getComposedFileIcon(name: string): React.ReactNode {
  const data = lookupFileIcon(name)
  if (!data) return renderSvg(DEFAULT_FILE_INNER)
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width={S} height={S} viewBox="0 0 24 24">
      <g dangerouslySetInnerHTML={{ __html: WHITE_FILE_BASE }} />
      <svg x="5" y="7" width="13" height="13" viewBox={data.viewBox}
           preserveAspectRatio="xMidYMid meet"
           dangerouslySetInnerHTML={{ __html: data.inner }} />
    </svg>
  )
}

/** Blank white file icon (empty document, no text lines) */
export function getEmptyFileIcon(): React.ReactNode {
  return renderSvg(WHITE_FILE_BASE)
}

export function getFolderIcon(open: boolean, name?: string): React.ReactNode {
  if (name) {
    const lower = name.toLowerCase()
    const iconName = atomFolderNameMap[lower]
    if (iconName) {
      const data = open ? atomFolderOpenIcons[iconName] : atomFolderIcons[iconName]
      if (data) return renderIconData(data)
    }
  }
  return renderSvg(open ? DEFAULT_FOLDER_OPEN_INNER : DEFAULT_FOLDER_INNER)
}
