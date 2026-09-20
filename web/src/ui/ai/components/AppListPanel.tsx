import React from 'react'
import type { AppEntry } from '../../../application/app-registry'
import { isAppOpenable } from '../../../application/app-openable'
import { useI18n } from '../../../i18n'
import { resolveAppIcon } from './appIconResolver'
import './AppListPanel.css'

export interface AppListPanelProps {
  /** Registered apps (the raw app-registry snapshot); the panel filters to
   *  the openable ones itself, mirroring the launcher tile gating. */
  apps: readonly AppEntry[]
  /** Invoked when the user picks an app. The shell opens its primary view and
   *  retires this picker tab. */
  onSelect: (app: AppEntry) => void
}

/**
 * Right-panel app picker: a clickable list of every openable registered app.
 * Picking one opens its primary view (the shell handles open + auto-close) —
 * the right-panel counterpart to the sidebar launcher's app grid.
 */
export const AppListPanel: React.FC<AppListPanelProps> = ({ apps, onSelect }) => {
  const { t } = useI18n()
  const openable = apps.filter(isAppOpenable)

  if (openable.length === 0) {
    return <div className="ai-app-list-empty">{t('rightPanel.appsEmpty')}</div>
  }

  return (
    <div className="ai-app-list-scroll">
      <div className="ai-app-list" data-testid="right-panel-app-list">
        {openable.map(app => (
          <button
            key={app.id}
            type="button"
            className="ai-app-list-tile"
            data-app-id={app.id}
            onClick={() => onSelect(app)}
          >
            <span className="ai-app-list-tile-icon">{resolveAppIcon(app.icon, app.id, 20, app.color)}</span>
            <span className="ai-app-list-tile-label" title={app.name || app.id}>{app.name || app.id}</span>
          </button>
        ))}
      </div>
    </div>
  )
}
