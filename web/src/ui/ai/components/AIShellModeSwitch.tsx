import React from 'react'
import { Circle } from 'lucide-react'
import { GuideIds } from '../guide-ids'

interface AIShellModeSwitchProps {
  onToggleSidebar?: () => void
}

export const AIShellModeSwitch: React.FC<AIShellModeSwitchProps> = ({ onToggleSidebar }) => {
  return (
    <button
      className="ai-shell-sidebar-toggle"
      data-guide-id={GuideIds.topbar_sidebar_toggle}
      type="button"
      onClick={onToggleSidebar}
      title="Toggle sidebar"
      aria-label="Toggle sidebar"
    >
      <Circle size={14} />
    </button>
  )
}
