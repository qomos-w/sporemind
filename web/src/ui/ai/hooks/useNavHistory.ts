import { useCallback, useMemo, useRef, useState } from 'react'
import type { ContentMode } from '../components/AIShellSidebar'

const CURSOR_RING_CAPACITY = 8

export interface RightTabSnapshot {
  id: string
  type: string
  label: string
  payload: unknown
}

export interface NavHistoryEntry {
  contentMode: ContentMode
  rightTabs: RightTabSnapshot[]
  activeBrowserTabId: string | null
  rightPanelOpen: boolean
  filePath: string | null
  cursorPos: number | null
  selectionFrom: number | null
  selectionTo: number | null
  folderPath: string | null
  selectedFilePath: string | null
  activeAgentId: string | null
}

function rightTabsEqual(a: RightTabSnapshot[], b: RightTabSnapshot[]): boolean {
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) {
    const ai = a[i]
    const bi = b[i]
    if (!ai || !bi) return false
    if (ai.id !== bi.id || ai.type !== bi.type || ai.label !== bi.label) return false
  }
  return true
}

function entriesEqual(a: NavHistoryEntry, b: NavHistoryEntry): boolean {
  return (
    a.contentMode === b.contentMode &&
    a.activeBrowserTabId === b.activeBrowserTabId &&
    a.rightPanelOpen === b.rightPanelOpen &&
    a.filePath === b.filePath &&
    a.folderPath === b.folderPath &&
    a.selectedFilePath === b.selectedFilePath &&
    a.activeAgentId === b.activeAgentId &&
    rightTabsEqual(a.rightTabs, b.rightTabs)
  )
}

export interface UseNavHistoryResult {
  push: (entry: NavHistoryEntry) => void
  updateCursor: (cursor: { pos: number; from: number; to: number }) => void
  canGoBack: boolean
  canGoForward: boolean
  goBack: () => NavHistoryEntry | null
  goForward: () => NavHistoryEntry | null
  removeTab: (tabId: string) => void
  backPastTab: (tabId: string) => NavHistoryEntry | null
}

export function useNavHistory(): UseNavHistoryResult {
  const [canGoBack, setCanGoBack] = useState(false)
  const [canGoForward, setCanGoForward] = useState(false)

  const historyRef = useRef<{
    back: NavHistoryEntry[]
    current: NavHistoryEntry | null
    forward: NavHistoryEntry[]
    cursorRing: Array<{ pos: number; from: number; to: number }>
    cursorRingIndex: number
  }>({
    back: [],
    current: null,
    forward: [],
    cursorRing: [],
    cursorRingIndex: 0,
  })

  const updateFlags = useCallback(() => {
    const h = historyRef.current
    setCanGoBack(h.back.length > 0)
    setCanGoForward(h.forward.length > 0)
  }, [])

  const push = useCallback(
    (entry: NavHistoryEntry) => {
      const h = historyRef.current

      // Skip duplicate entries (ignoring cursor fields).
      if (h.current && entriesEqual(h.current, entry)) {
        // Still record cursor if provided.
        if (entry.cursorPos !== null) {
          const ring = h.cursorRing
          ring[h.cursorRingIndex % CURSOR_RING_CAPACITY] = {
            pos: entry.cursorPos,
            from: entry.selectionFrom ?? entry.cursorPos,
            to: entry.selectionTo ?? entry.cursorPos,
          }
          h.cursorRingIndex = (h.cursorRingIndex + 1) % CURSOR_RING_CAPACITY
          h.current.cursorPos = entry.cursorPos
          h.current.selectionFrom = entry.selectionFrom
          h.current.selectionTo = entry.selectionTo
        }
        // Update current with the latest tab payloads (e.g. viewMode) so
        // in-place mutations are captured without creating a new back entry.
        h.current.rightTabs = entry.rightTabs
        h.current.activeBrowserTabId = entry.activeBrowserTabId
        return
      }

      if (h.current) {
        h.back.push(h.current)
      }
      h.current = {
        ...entry,
        cursorPos: null,
        selectionFrom: null,
        selectionTo: null,
      }
      h.forward = []
      h.cursorRing = []
      h.cursorRingIndex = 0
      updateFlags()
    },
    [updateFlags],
  )

  const updateCursor = useCallback(
    (cursor: { pos: number; from: number; to: number }) => {
      const h = historyRef.current
      if (!h.current) return
      const ring = h.cursorRing
      ring[h.cursorRingIndex % CURSOR_RING_CAPACITY] = cursor
      h.cursorRingIndex = (h.cursorRingIndex + 1) % CURSOR_RING_CAPACITY
      h.current.cursorPos = cursor.pos
      h.current.selectionFrom = cursor.from
      h.current.selectionTo = cursor.to
    },
    [],
  )

  const goBack = useCallback((): NavHistoryEntry | null => {
    const h = historyRef.current
    if (h.back.length === 0 || !h.current) return null
    h.forward.push(h.current)
    const prev = h.back.pop()!
    h.current = prev
    updateFlags()
    return prev
  }, [updateFlags])

  const goForward = useCallback((): NavHistoryEntry | null => {
    const h = historyRef.current
    if (h.forward.length === 0) return null
    // h.current can be null after closing the last trajectory-relevant tab
    // (backPastTab); forward must stay usable from that state.
    if (h.current) h.back.push(h.current)
    const next = h.forward.pop()!
    h.current = next
    updateFlags()
    return next
  }, [updateFlags])

  // Strip a closed tab from a single entry. Returns false when the entry no
  // longer selects a live tab and must be dropped: the tab's backing resource
  // (browser session, SSH shell) is destroyed on close, so no trajectory point
  // may keep referencing it as open or active.
  const stripTabFromEntry = useCallback((entry: NavHistoryEntry, tabId: string): boolean => {
    if (entry.rightTabs.some(t => t.id === tabId)) {
      entry.rightTabs = entry.rightTabs.filter(t => t.id !== tabId)
    }
    if (entry.activeBrowserTabId === tabId) entry.activeBrowserTabId = null
    const active = entry.activeBrowserTabId
    return active !== null && entry.rightTabs.some(t => t.id === active)
  }, [])

  // Purge a closed background tab from the whole trajectory without moving.
  // Entries whose active tab was the closed one are dropped entirely.
  const removeTab = useCallback((tabId: string) => {
    const h = historyRef.current
    h.back = h.back.filter(e => stripTabFromEntry(e, tabId))
    h.forward = h.forward.filter(e => stripTabFromEntry(e, tabId))
    if (h.current) stripTabFromEntry(h.current, tabId)
    updateFlags()
  }, [stripTabFromEntry, updateFlags])

  // Closing the active tab retreats into the recorded trajectory: the closed
  // tab is purged from every entry (trajectory points that only existed while
  // it was active are dropped), then the nearest surviving entry becomes the
  // restored current state. Back/forward can never resurrect the closed tab.
  // Returns the entry to restore, or null when the trajectory holds no usable
  // predecessor — the caller then falls back to plain tab-removal semantics.
  const backPastTab = useCallback((tabId: string): NavHistoryEntry | null => {
    const h = historyRef.current
    h.forward = h.forward.filter(e => stripTabFromEntry(e, tabId))
    h.back = h.back.filter(e => stripTabFromEntry(e, tabId))
    const entry = h.back.pop() ?? null
    h.current = entry
    updateFlags()
    return entry
  }, [stripTabFromEntry, updateFlags])

  // Memoized: a fresh object per render churns every consumer's useCallback /
  // useEffect deps (notably the ssh event subscription effect in
  // AIShellLayout), re-subscribing on each render during streaming.
  return useMemo(
    () => ({ push, updateCursor, canGoBack, canGoForward, goBack, goForward, removeTab, backPastTab }),
    [push, updateCursor, canGoBack, canGoForward, goBack, goForward, removeTab, backPastTab],
  )
}