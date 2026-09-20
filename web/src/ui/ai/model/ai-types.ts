import type { Frame } from './frame-types'

export type ViewMode = 'mobile' | 'desktop'

export interface AIShellState {
  activeSessionId: string | null
  previousSessionId: string | null
  sidebarVisible: boolean
  composerValue: string
  selectedFrame: Frame | null
}

export type AIShellAction =
  | { type: 'SELECT_SESSION'; id: string }
  | { type: 'RESTORE_SESSION'; selectedAgentId: string | null; previousAgentId: string | null }
  | { type: 'TOGGLE_SIDEBAR' }
  | { type: 'SET_SIDEBAR_VISIBLE'; value: boolean }
  | { type: 'SET_COMPOSER_VALUE'; value: string }
  | { type: 'SEND_MESSAGE' }
  | { type: 'SELECT_FRAME'; frame: Frame | null }
