import React from 'react'
import { renderSettingsContent, categories } from './settings-data'
import { useI18n } from '../../../i18n'
import type { UiExpandAction, TurnTailMetricsVisible, ComposerExtrasVisible } from '../../../application/workspace-ui-state'
import type { DbProfileView } from '../../../gen-types/dbmanager'
import type { AppEntry } from '../../../application/app-registry'
import './SettingsContent.css'

interface SettingsContentProps {
  activeIds: string[]
  theme: import('@qomos/sporemind-theme').ThemeState
  onThemeChange: (theme: import('@qomos/sporemind-theme').ThemeState) => void
  configBadgesVisible?: boolean
  onConfigBadgesVisibleChange?: (visible: boolean) => void
  turnTailMetrics?: TurnTailMetricsVisible
  onTurnTailMetricsChange?: (metrics: TurnTailMetricsVisible) => void
  composerExtras?: ComposerExtrasVisible
  onComposerExtrasChange?: (extras: ComposerExtrasVisible) => void
  smoothStream?: boolean
  onSmoothStreamChange?: (enabled: boolean) => void
  developerMode?: boolean
  onDeveloperModeChange?: (enabled: boolean) => void
  providerUserAgentVisible?: boolean
  onProviderUserAgentVisibleChange?: (visible: boolean) => void
  expansionModes?: Record<string, UiExpandAction>
  onExpansionModeChange?: (kind: string, action: UiExpandAction) => void
  expandDurationMs?: number
  onExpandDurationChange?: (ms: number) => void
  /**
   * Wired to DbManagerSettings "Open client" so settings can hand a profile
   * off to AIShellLayout without leaking right-tab internals into the
   * settings layer.
   */
  onOpenDbClient?: (profile: DbProfileView) => void
  /**
   * Wired to the Plugins settings section "Open" button: opens the app's
   * primary view in the right panel, mirroring the launcher tile.
   */
  onOpenAppView?: (app: AppEntry) => void
}

export const SettingsContent: React.FC<SettingsContentProps> = ({ activeIds, theme, onThemeChange, configBadgesVisible, onConfigBadgesVisibleChange, turnTailMetrics, onTurnTailMetricsChange, composerExtras, onComposerExtrasChange, smoothStream, onSmoothStreamChange, developerMode, onDeveloperModeChange, providerUserAgentVisible, onProviderUserAgentVisibleChange, expansionModes, onExpansionModeChange, expandDurationMs, onExpandDurationChange, onOpenDbClient, onOpenAppView }) => {
  const { t } = useI18n()
  return (
    <div className="sp-settings-content shadcn-scope">
      {activeIds.map((id) => {
        const cat = categories.find(c => c.id === id)
        const title = cat ? t(cat.labelKey) : id
        return (
          <div key={id} className="sp-settings-content-block">
            <div className="sp-settings-panel-header">
              <h2 className="sp-settings-panel-title">{title}</h2>
            </div>
            {renderSettingsContent(id, theme, onThemeChange, configBadgesVisible, onConfigBadgesVisibleChange, turnTailMetrics, onTurnTailMetricsChange, composerExtras, onComposerExtrasChange, smoothStream, onSmoothStreamChange, developerMode, onDeveloperModeChange, providerUserAgentVisible, onProviderUserAgentVisibleChange, expansionModes, onExpansionModeChange, expandDurationMs, onExpandDurationChange, onOpenDbClient, onOpenAppView)}
          </div>
        )
      })}
    </div>
  )
}
