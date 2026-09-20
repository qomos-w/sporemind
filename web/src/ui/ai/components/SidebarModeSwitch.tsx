import React from 'react'
import { Code2, LayoutDashboard, Rocket } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { buildType } from '../../../config/buildConfig'
import { GuideIds } from '../guide-ids'
import './SidebarModeSwitch.css'

export type LauncherMode = 'code' | 'app' | 'workbench'

interface SidebarModeSwitchProps {
  value: LauncherMode
  onChange: (next: LauncherMode) => void
}

/**
 * Launcher (home) pane variant switcher: 构建 / 应用 / 工作台. Styled after
 * the theme-switch control (AIShellSidebar.css .theme-switch): a soft pill
 * track with options; the active option gets an elevated pill background.
 * Desktop parks it in the shell topbar, left of the back button (compact
 * height via AIShellTopbar.css); mobile keeps it atop the sidebar mode group
 * (code mode) and in the launcher header (app mode). 工作台 is the fullscreen
 * board takeover — selecting it opens the WorkbenchSurface overlay (the
 * switch itself disappears underneath; exit from the board's corner button).
 * No popup, so it never registers with useBrowserOverlay.
 */
export function SidebarModeSwitch({ value, onChange }: SidebarModeSwitchProps) {
  const { t } = useI18n()
  const title = t('shell.sidebar.launcherMode.title')
  const showWorkbench = buildType === 'dev'
  const options: Array<{ id: LauncherMode; label: string; icon: React.ReactNode }> = [
    { id: 'code', label: t('shell.sidebar.launcherMode.code'), icon: <Code2 size={14} /> },
    { id: 'app', label: t('shell.sidebar.launcherMode.app'), icon: <Rocket size={14} /> },
    ...(showWorkbench ? [{ id: 'workbench' as const, label: t('shell.sidebar.launcherMode.workbench'), icon: <LayoutDashboard size={14} /> }] : []),
  ]

  return (
    <div className="ai-sidebar-mode-switch" role="tablist" aria-label={title} title={title} data-guide-id={GuideIds.launcher_mode_switch}>
      {options.map(option => (
        <button
          key={option.id}
          type="button"
          role="tab"
          aria-selected={value === option.id}
          className={`ai-sidebar-mode-switch-segment${value === option.id ? ' active' : ''}`}
          onClick={() => onChange(option.id)}
        >
          {option.icon}
          <span className="ai-sidebar-mode-switch-segment-label">{option.label}</span>
        </button>
      ))}
    </div>
  )
}
