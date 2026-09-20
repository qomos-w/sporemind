/**
 * Global SSH session UI layout preferences.
 *
 * All SSH session views share one preference blob so that handle positions,
 * the file view mode (list / icons), and pane expand/collapse state persist
 * across remounts and apply to every SSH session tab — not per host. Stored
 * via the account-preferences layer (backend-owned, no localStorage).
 */
import { savePreference, loadPreference } from './theme-persist'
import {
  FILE_COLUMN_DEFAULT,
  clampColumnWidth,
  type FileColumnKey,
  type FileColumnWidths,
} from '../ui/views/sshFileColumns'

const PREF_KEY = 'sshSession.ui.v1'

export type SshFileViewMode = 'list' | 'icons'
export type SshBottomTab = 'ftp' | 'commands'

export interface SshSessionUIPrefs {
  fileViewMode: SshFileViewMode
  statusWidth: number
  filesHeight: number
  filesTreeWidth: number
  commandTreeWidth: number
  colWidths: FileColumnWidths
  statusCollapsed: boolean
  filesCollapsed: boolean
  treeSidebarOpen: boolean
  bottomTab: SshBottomTab
}

export const DEFAULT_SSH_SESSION_UI_PREFS: SshSessionUIPrefs = {
  fileViewMode: 'icons',
  statusWidth: 240,
  filesHeight: 260,
  filesTreeWidth: 180,
  commandTreeWidth: 180,
  colWidths: { ...FILE_COLUMN_DEFAULT },
  statusCollapsed: false,
  filesCollapsed: false,
  treeSidebarOpen: true,
  bottomTab: 'ftp',
}

const writeVersion = { v: 0 }

function clampNum(v: unknown, min: number, max: number, fallback: number): number {
  if (typeof v !== 'number' || !Number.isFinite(v)) return fallback
  return Math.min(max, Math.max(min, Math.round(v)))
}

function parseColWidths(raw: unknown): FileColumnWidths {
  const out: FileColumnWidths = { ...FILE_COLUMN_DEFAULT }
  if (!raw || typeof raw !== 'object') return out
  const src = raw as Record<string, unknown>
  for (const key of Object.keys(out) as FileColumnKey[]) {
    const v = src[key]
    if (typeof v === 'number' && v > 0) out[key] = clampColumnWidth(key, v)
  }
  return out
}

/**
 * Read and validate the shared SSH UI prefs blob. Returns null when no blob
 * has been saved yet so callers fall back to component defaults.
 */
export async function loadSshSessionUIPrefs(): Promise<SshSessionUIPrefs | null> {
  const raw = await loadPreference(PREF_KEY)
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object') return null
    return {
      fileViewMode: parsed.fileViewMode === 'list' || parsed.fileViewMode === 'icons'
        ? parsed.fileViewMode
        : DEFAULT_SSH_SESSION_UI_PREFS.fileViewMode,
      statusWidth: clampNum(parsed.statusWidth, 160, 4000, DEFAULT_SSH_SESSION_UI_PREFS.statusWidth),
      filesHeight: clampNum(parsed.filesHeight, 120, 8000, DEFAULT_SSH_SESSION_UI_PREFS.filesHeight),
      filesTreeWidth: clampNum(parsed.filesTreeWidth, 120, 2000, DEFAULT_SSH_SESSION_UI_PREFS.filesTreeWidth),
      commandTreeWidth: clampNum(parsed.commandTreeWidth, 120, 320, DEFAULT_SSH_SESSION_UI_PREFS.commandTreeWidth),
      colWidths: parseColWidths(parsed.colWidths),
      statusCollapsed: typeof parsed.statusCollapsed === 'boolean' ? parsed.statusCollapsed : false,
      filesCollapsed: typeof parsed.filesCollapsed === 'boolean' ? parsed.filesCollapsed : false,
      treeSidebarOpen: typeof parsed.treeSidebarOpen === 'boolean' ? parsed.treeSidebarOpen : true,
      bottomTab: parsed.bottomTab === 'commands' ? 'commands' : 'ftp',
    }
  } catch {
    return null
  }
}

/**
 * Merge a partial patch into the stored prefs (read-modify-write) so that
 * independent writers — e.g. the file-list column resizer and the command
 * panel tree-width handle, which live in different components — never wipe
 * each other's field by overwriting the whole blob.
 */
export async function saveSshSessionUIPrefs(patch: Partial<SshSessionUIPrefs>): Promise<void> {
  const existing = await loadSshSessionUIPrefs()
  const merged: SshSessionUIPrefs = { ...DEFAULT_SSH_SESSION_UI_PREFS, ...existing, ...patch }
  await savePreference(PREF_KEY, JSON.stringify(merged), 'ssh-session-ui', writeVersion)
}
