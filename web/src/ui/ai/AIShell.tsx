import React from 'react'
import { AIShellLayout } from './AIShellLayout'
import type { ThemeState } from '@qomos/sporemind-theme'
import './AIShell.css'

interface AIShellProps {
  // 当为 true 时，AI Shell 作为 Workbench 标签页嵌入，不显示左侧 sidebar 和右侧边栏。
  embedded?: boolean
  shellVariant?: 'ai' | 'preview'
  onShellModeChange?: () => void
  theme?: ThemeState
  onThemeChange?: (theme: ThemeState) => void
}

export const AIShell: React.FC<AIShellProps> = ({
  embedded = false,
  shellVariant = 'ai',
  onShellModeChange,
  theme,
  onThemeChange,
}) => {
  return (
    <AIShellLayout
      embedded={embedded}
      shellVariant={shellVariant}
      onShellModeChange={onShellModeChange}
      theme={theme}
      onThemeChange={onThemeChange}
    />
  )
}
