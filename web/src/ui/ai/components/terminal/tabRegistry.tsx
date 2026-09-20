import React from 'react'
import { SquareTerminal, FileText, MonitorSmartphone, Bug } from 'lucide-react'
import type { I18nKey } from '../../../../i18n/types'
import { ShellConsoleTab } from './ShellConsoleTab'
import { LogViewerTab } from './LogViewerTab'
import { DebugConsoleTab } from './DebugConsoleTab'
import { AppConsoleTab } from './AppConsoleTab'

/**
 * Props handed to a terminal tab body component (placeholder or real
 * implementation). `tab` carries the tab identity/title so a body can react
 * to its own tab; `onSetTabTitle` lets a body annotate its tab (e.g. append
 * an "exited" suffix when the session dies).
 */
export interface TerminalTabBodyProps {
  /** The tab this body renders for (id, type, title). */
  tab: { id: string; type: string; title?: string }
  /** Update this tab's title; pass undefined to fall back to the label key. */
  onSetTabTitle?: (tabId: string, title: string | undefined) => void
  /** Open a file at a line in the right-panel file viewer (e.g. log caller jump). */
  onOpenFile?: (filePath: string, line?: number) => void
  projectRoot?: string
}

/**
 * Terminal tab type registry — the extension point for terminal tab kinds.
 *
 * TerminalPanel consumes ONLY this registry: the tab bar, the "+" new-tab
 * dropdown (menu order follows registry key insertion order) and the tab
 * body all resolve through it. A task that implements a real tab type just:
 *   1. adds its own component file, and
 *   2. registers one entry here (`'my-type': { ... }`).
 * No shared TerminalPanel/TerminalPanel.css changes are needed.
 */
export interface TerminalTabTypeDef {
  /** i18n label key for the tab type (tab title + "+" menu label). */
  labelKey: I18nKey
  /** Icon shown on the tab and in the "+" menu. */
  icon: React.ReactNode
  /** Tab body component — the real implementation, or a placeholder until one lands. */
  component: React.ComponentType<TerminalTabBodyProps>
  /** Backend capability flags this tab type needs (future feature gating). */
  backendCapabilities: string[]
}

/** Open registry: keys are tab-type ids, values carry everything the panel needs. */
export type TerminalTabTypeRegistry = Record<string, TerminalTabTypeDef>

/** Built-in terminal tab types. Insertion order drives the "+" menu order. */
export const terminalTabRegistry: TerminalTabTypeRegistry = {
  shell: {
    labelKey: 'terminalPanel.tab.shell',
    icon: <SquareTerminal size={14} />,
    component: ShellConsoleTab,
    backendCapabilities: ['shell'],
  },
  'backend-log': {
    labelKey: 'terminalPanel.tab.backendLog',
    icon: <FileText size={14} />,
    component: LogViewerTab,
    backendCapabilities: ['log'],
  },
  'app-console': {
    labelKey: 'terminalPanel.tab.appConsole',
    icon: <MonitorSmartphone size={14} />,
    component: AppConsoleTab,
    backendCapabilities: ['console'],
  },
  debug: {
    labelKey: 'terminalPanel.tab.debug',
    icon: <Bug size={14} />,
    component: DebugConsoleTab,
    backendCapabilities: ['debug'],
  },
}

/** Ordered type ids for the "+" menu (registry insertion order). */
export const terminalTabTypeOrder: string[] = Object.keys(terminalTabRegistry)

/** Convenience type for a registry key. */
export type TerminalTabType = keyof typeof terminalTabRegistry