import React, { useState, useEffect, useRef, useCallback } from 'react'
import { Pin, X } from 'lucide-react'
import { detectDockZone } from '../lib/dock-utils'
import type { DockZone } from '../types/layout'

interface DockZonePanelProps {
  zone: DockZone
  activeTabId: string | null
  panelTitle: string
  panelSize?: { w: number; h: number }
  canvasAreaRef: React.RefObject<HTMLDivElement>
  bottomHeight: number
  getPreviewStyle: (zone: DockZone, panelSize: { w: number; h: number }) => React.CSSProperties
  onDock?: (id: string, zone: DockZone) => void
  onTogglePin?: (id: string) => void
  onHide?: (id: string) => void
  renderPanel: (id: string) => React.ReactNode
}

export const DockZonePanel: React.FC<DockZonePanelProps> = ({
  zone,
  activeTabId,
  panelTitle,
  panelSize,
  canvasAreaRef,
  bottomHeight,
  getPreviewStyle,
  onDock,
  onTogglePin,
  onHide,
  renderPanel,
}) => {
  const [dragging, setDragging] = useState(false)
  const [dockPreview, setDockPreview] = useState<DockZone>(null)
  const dragStateRef = useRef<{ startX: number; startY: number } | null>(null)

  const onTitlePointerDown = useCallback((e: React.PointerEvent) => {
    if ((e.target as HTMLElement).closest('.dl-zone-actions')) return
    e.preventDefault()
    dragStateRef.current = { startX: e.clientX, startY: e.clientY }
    setDragging(true)
  }, [])

  useEffect(() => {
    if (!dragging) return
    const onPointerMove = (e: PointerEvent) => {
      if (!dragStateRef.current || !canvasAreaRef.current) return
      const rect = canvasAreaRef.current.getBoundingClientRect()
      setDockPreview(detectDockZone(e.clientX, e.clientY, rect, bottomHeight))
    }
    const onPointerUp = () => {
      if (dockPreview && dockPreview !== zone && activeTabId) {
        onDock?.(activeTabId, dockPreview)
      }
      dragStateRef.current = null
      setDragging(false)
      setDockPreview(null)
    }
    document.addEventListener('pointermove', onPointerMove)
    document.addEventListener('pointerup', onPointerUp)
    return () => {
      document.removeEventListener('pointermove', onPointerMove)
      document.removeEventListener('pointerup', onPointerUp)
    }
  }, [dragging, dockPreview, zone, activeTabId, canvasAreaRef, onDock])

  if (!activeTabId) return null

  return (
    <>
      <div className="dl-zone">
        <div
          className={`dl-zone-header${dragging ? ' dragging' : ''}`}
          onPointerDown={onTitlePointerDown}
        >
          <span className="dl-zone-title">{panelTitle}</span>
          <div className="dl-zone-actions">
            <button
              className="dl-docked-panel-btn"
              onClick={() => onTogglePin?.(activeTabId)}
              onPointerDown={e => e.stopPropagation()}
              title="Undock (float)"
            >
              <Pin size={10} opacity={0.5} />
            </button>
            <button
              className="dl-docked-panel-btn"
              onClick={() => onHide?.(activeTabId)}
              onPointerDown={e => e.stopPropagation()}
              title="Hide"
            >
              <X size={8} />
            </button>
          </div>
        </div>
        <div className="dl-zone-body">
          {renderPanel(activeTabId)}
        </div>
      </div>
      {dragging && dockPreview && panelSize && (
        <div className="dl-dock-preview" style={getPreviewStyle(dockPreview, panelSize)} />
      )}
    </>
  )
}
