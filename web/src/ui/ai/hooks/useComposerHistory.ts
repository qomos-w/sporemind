import { useCallback, useEffect, useState } from 'react'
import type { ComposerHistoryItem } from '../components/AIComposer'
import { client } from '../../../application/generated-client'
import * as workspace_preferences from '../../../gen-clients/workspace/client'
import type { AccountPreferencesSnapshot } from '../../../gen-clients/system/types'
import type { SaveAccountPreferencesCommand } from '../../../gen-types/workspace'

function historyKey(scope: string): string {
  return `ai-composer-history.${scope}.v1`
}

export const COMPOSER_HISTORY_SCOPE = 'chat'

let writeVersion = 0

async function loadHistory(scope: string): Promise<ComposerHistoryItem[]> {
  try {
    const snapshot = await workspace_preferences.preferencesGet(client)
    const raw = snapshot.Preferences?.[historyKey(scope)]
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    if (Array.isArray(parsed)) return parsed as ComposerHistoryItem[]
  } catch {
    // ignore load errors
  }
  return []
}

async function saveHistory(scope: string, items: ComposerHistoryItem[]): Promise<void> {
  const snapshot = await workspace_preferences.preferencesGet(client).catch(() => null) as AccountPreferencesSnapshot | null
  const preferences = { ...(snapshot?.Preferences ?? {}), [historyKey(scope)]: JSON.stringify(items) }
  const command: SaveAccountPreferencesCommand = {
    RequestId: `ai-composer-history-${scope}-${++writeVersion}`,
    AccountId: snapshot?.AccountId ?? 'root',
    Version: snapshot?.Version ?? 1,
    Preferences: preferences,
  }
  await workspace_preferences.preferencesSave(client, command)
}

/** Shared per-scope store: every useComposerHistory mount of the same scope
 * sees the same items, so writes (send / favorite / remove) propagate to all
 * mounted consumers — e.g. the AIShellLayout welcome-row chips react to a
 * favorite toggled inside the conversation composer — instead of living in
 * per-instance useState silos. */
interface ScopeState {
  items: ComposerHistoryItem[]
  loaded: boolean
  /** Bumped on every setHistory; a load resolving after a concurrent write is
   * dropped instead of clobbering the write. */
  revision: number
  listeners: Set<() => void>
}

const scopes = new Map<string, ScopeState>()

function scopeState(scope: string): ScopeState {
  let s = scopes.get(scope)
  if (!s) {
    s = { items: [], loaded: false, revision: 0, listeners: new Set() }
    scopes.set(scope, s)
  }
  return s
}

export function useComposerHistory(scope: string): {
  history: ComposerHistoryItem[]
  setHistory: (items: ComposerHistoryItem[]) => void
} {
  const [history, setHistoryState] = useState<ComposerHistoryItem[]>(() => scopeState(scope).items)

  useEffect(() => {
    const s = scopeState(scope)
    const notify = () => setHistoryState(s.items)
    s.listeners.add(notify)
    if (!s.loaded) {
      s.loaded = true
      const revisionAtLoad = s.revision
      loadHistory(scope).then(items => {
        if (s.revision === revisionAtLoad) {
          s.items = items
          s.listeners.forEach(l => l())
        }
      })
    } else {
      setHistoryState(s.items)
    }
    return () => {
      s.listeners.delete(notify)
    }
  }, [scope])

  const setHistory = useCallback((items: ComposerHistoryItem[]) => {
    const s = scopeState(scope)
    s.items = items
    s.revision++
    s.listeners.forEach(l => l())
    saveHistory(scope, items).catch(() => {})
  }, [scope])

  return { history, setHistory }
}

export function __resetComposerHistoryStoreForTests(): void {
  scopes.clear()
}
