import { useEffect, useSyncExternalStore } from 'react'
import { loadPreference, savePreference } from './theme-persist'

// Global view mode for card-toggle grids in settings views (flat vs split by
// active status). Persisted through the actor-owned workspace preferences —
// never localStorage — so it survives restarts and applies to every view that
// reads this store.
export type CardGridView = 'flat' | 'split'

const PREFERENCE_KEY = 'ui.cardGridView.v1'

let current: CardGridView = 'flat'
let loaded = false
const version = { v: 0 }
const listeners = new Set<() => void>()

function emit(): void {
  for (const listener of listeners) listener()
}

function isCardGridView(raw: string | undefined): raw is CardGridView {
  return raw === 'flat' || raw === 'split'
}

export function getCardGridView(): CardGridView {
  return current
}

export async function ensureCardGridViewLoaded(): Promise<void> {
  if (loaded) return
  loaded = true
  const raw = await loadPreference(PREFERENCE_KEY)
  if (isCardGridView(raw) && raw !== current) {
    current = raw
    emit()
  }
}

export function setCardGridView(view: CardGridView): void {
  if (view === current) return
  current = view
  emit()
  void savePreference(PREFERENCE_KEY, view, 'card-grid-view', version).catch(() => {})
}

export function useCardGridView(): [CardGridView, (view: CardGridView) => void] {
  useEffect(() => {
    void ensureCardGridViewLoaded()
  }, [])
  const view = useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
    () => current,
  )
  return [view, setCardGridView]
}
