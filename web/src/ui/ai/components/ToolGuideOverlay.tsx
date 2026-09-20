import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { useI18n } from '../../../i18n'
import { useBrowserOverlay } from '../browserOverlay'
import { toolGuideManager } from './tool-guide-manager'
import { toolGuideEntries, type ToolGuideEntry } from './tool-guide-data'
import './ToolGuideOverlay.css'

/**
 * Full-screen tool guide overlay.
 *
 * Displays a list of collapsible tool cards over a semi-transparent backdrop.
 * Each card shows the tool title by default; clicking expands the description.
 * A close button (top-right) dismisses the overlay entirely.
 *
 * Because this overlay covers the native browser window area, it registers
 * with `useBrowserOverlay(open)` so the BrowserOverlayManager hides the
 * native window while the overlay is visible.
 *
 * Visibility is driven by `toolGuideManager` (subscribe + getState). External
 * callers use `toolGuideManager.showToolGuide()` / `hideToolGuide()`.
 */
export const ToolGuideOverlay: React.FC = () => {
  const { t } = useI18n()
  const [visible, setVisible] = useState(false)
  const [expandedId, setExpandedId] = useState<string | null>(null)

  // Subscribe to the tool guide manager.
  useEffect(() => {
    return toolGuideManager.subscribe((s) => {
      setVisible(s.visible)
    })
  }, [])

  // Initialise from current state (covers the case where the manager was
  // already showing before this component mounted).
  useEffect(() => {
    setVisible(toolGuideManager.getState().visible)
  }, [])

  // Register with the browser overlay manager so native windows are hidden
  // while this overlay is open.
  useBrowserOverlay(visible)

  const handleClose = useCallback(() => {
    toolGuideManager.hideToolGuide()
  }, [])

  const toggleCard = useCallback((id: string) => {
    setExpandedId((prev) => (prev === id ? null : id))
  }, [])

  // Reset expanded card when the overlay closes.
  useEffect(() => {
    if (!visible) {
      setExpandedId(null)
    }
  }, [visible])

  const entries = useMemo(() => toolGuideEntries, [])

  if (!visible) return null

  return (
    <div className="tool-guide-overlay-root" role="dialog" aria-modal="true" aria-label={t('toolGuide.title')}>
      {/* Semi-transparent backdrop — click closes the overlay */}
      <div className="tool-guide-overlay-backdrop" onClick={handleClose} />

      {/* Card panel */}
      <div className="tool-guide-overlay-panel">
        <div className="tool-guide-overlay-header">
          <h2 className="tool-guide-overlay-title">{t('toolGuide.title')}</h2>
          <button
            type="button"
            className="tool-guide-overlay-close"
            onClick={handleClose}
            aria-label={t('toolGuide.close')}
          >
            <svg width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true">
              <path
                d="M5 5 L15 15 M15 5 L5 15"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>

        <div className="tool-guide-overlay-list">
          {entries.map((entry: ToolGuideEntry) => {
            const isExpanded = expandedId === entry.id
            return (
              <div
                key={entry.id}
                className={`tool-guide-card${isExpanded ? ' tool-guide-card--expanded' : ''}`}
              >
                <button
                  type="button"
                  className="tool-guide-card-header"
                  onClick={() => toggleCard(entry.id)}
                  aria-expanded={isExpanded}
                  aria-controls={`tool-guide-card-content-${entry.id}`}
                >
                  <span className="tool-guide-card-title">{t(entry.titleKey)}</span>
                  <span className={`tool-guide-card-chevron${isExpanded ? ' tool-guide-card-chevron--open' : ''}`}>
                    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                      <path
                        d="M4 6 L8 10 L12 6"
                        stroke="currentColor"
                        strokeWidth="2"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                      />
                    </svg>
                  </span>
                </button>
                {isExpanded && (
                  <div
                    id={`tool-guide-card-content-${entry.id}`}
                    className="tool-guide-card-body"
                  >
                    {t(entry.descriptionKey)}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}
