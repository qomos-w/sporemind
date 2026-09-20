import { describe, it, expect } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useNavHistory, type NavHistoryEntry } from './useNavHistory'

function makeEntry(overrides: Partial<NavHistoryEntry> = {}): NavHistoryEntry {
  return {
    contentMode: 'conversation',
    rightTabs: [],
    activeBrowserTabId: null,
    rightPanelOpen: false,
    filePath: null,
    cursorPos: null,
    selectionFrom: null,
    selectionTo: null,
    folderPath: null,
    selectedFilePath: null,
    activeAgentId: null,
    ...overrides,
  }
}

describe('useNavHistory', () => {
  it('initialises with no back/forward history', () => {
    const { result } = renderHook(() => useNavHistory())
    expect(result.current.canGoBack).toBe(false)
    expect(result.current.canGoForward).toBe(false)
  })

  it('pushes entries and enables back navigation', () => {
    const { result } = renderHook(() => useNavHistory())

    act(() => result.current.push(makeEntry({ contentMode: 'conversation' })))
    expect(result.current.canGoBack).toBe(false)

    act(() => result.current.push(makeEntry({ contentMode: 'topology' })))
    expect(result.current.canGoBack).toBe(true)
    expect(result.current.canGoForward).toBe(false)
  })

  it('navigates back and forward preserving the forward stack', () => {
    const { result } = renderHook(() => useNavHistory())

    act(() => result.current.push(makeEntry({ contentMode: 'conversation' })))
    act(() => result.current.push(makeEntry({ contentMode: 'topology' })))
    act(() => result.current.push(makeEntry({ contentMode: 'workflow' })))

    expect(result.current.canGoBack).toBe(true)

    // Back: workflow -> topology
    let prev: NavHistoryEntry | null = null
    act(() => { prev = result.current.goBack() })
    expect(prev!.contentMode).toBe('topology')
    expect(result.current.canGoForward).toBe(true)

    // Forward: topology -> workflow
    let next: NavHistoryEntry | null = null
    act(() => { next = result.current.goForward() })
    expect(next!.contentMode).toBe('workflow')
    expect(result.current.canGoForward).toBe(false)
    expect(result.current.canGoBack).toBe(true)
  })

  it('clears forward stack on new push after going back', () => {
    const { result } = renderHook(() => useNavHistory())

    act(() => result.current.push(makeEntry({ contentMode: 'conversation' })))
    act(() => result.current.push(makeEntry({ contentMode: 'topology' })))
    act(() => result.current.push(makeEntry({ contentMode: 'workflow' })))

    act(() => result.current.goBack()) // -> topology
    expect(result.current.canGoForward).toBe(true)

    // New push should clear forward
    act(() => result.current.push(makeEntry({ contentMode: 'files' })))
    expect(result.current.canGoForward).toBe(false)
    expect(result.current.canGoBack).toBe(true)
  })

  it('skips duplicate entries (ignoring cursor fields)', () => {
    const { result } = renderHook(() => useNavHistory())

    act(() => result.current.push(makeEntry({ contentMode: 'conversation' })))
    act(() => result.current.push(makeEntry({ contentMode: 'conversation', cursorPos: 42 })))

    // Should not create a new back entry since only cursor changed
    expect(result.current.canGoBack).toBe(false)
  })

  it('records cursor position via updateCursor', () => {
    const { result } = renderHook(() => useNavHistory())

    act(() => result.current.push(makeEntry({ contentMode: 'conversation' })))
    act(() => result.current.updateCursor({ pos: 100, from: 90, to: 110 }))

    // Push a new entry so we can go back to the one with the cursor
    act(() => result.current.push(makeEntry({ contentMode: 'topology' })))

    let prev: NavHistoryEntry | null = null
    act(() => { prev = result.current.goBack() })
    expect(prev!.cursorPos).toBe(100)
    expect(prev!.selectionFrom).toBe(90)
    expect(prev!.selectionTo).toBe(110)
  })

  it('records cursor from duplicate push with cursor fields', () => {
    const { result } = renderHook(() => useNavHistory())

    act(() => result.current.push(makeEntry({ contentMode: 'conversation' })))
    // Same entry but with cursor — should update current's cursor, not create new back entry
    act(() => result.current.push(makeEntry({ contentMode: 'conversation', cursorPos: 50, selectionFrom: 40, selectionTo: 60 })))

    expect(result.current.canGoBack).toBe(false)

    // Go back should still return the entry with the cursor
    act(() => result.current.push(makeEntry({ contentMode: 'topology' })))
    let prev: NavHistoryEntry | null = null
    act(() => { prev = result.current.goBack() })
    expect(prev!.cursorPos).toBe(50)
  })

  it('returns null when going back with empty back stack', () => {
    const { result } = renderHook(() => useNavHistory())
    let prev: NavHistoryEntry | null = makeEntry()
    act(() => { prev = result.current.goBack() })
    expect(prev).toBeNull()
  })

  it('returns null when going forward with empty forward stack', () => {
    const { result } = renderHook(() => useNavHistory())
    let next: NavHistoryEntry | null = makeEntry()
    act(() => { next = result.current.goForward() })
    expect(next).toBeNull()
  })

  it('preserves rightTabs in entries', () => {
    const { result } = renderHook(() => useNavHistory())

    const tabs = [
      { id: 'file-a', type: 'file', label: 'a.ts', payload: { filePath: 'a.ts' } },
      { id: 'file-b', type: 'file', label: 'b.ts', payload: { filePath: 'b.ts' } },
    ]

    act(() => result.current.push(makeEntry({
      contentMode: 'conversation',
      rightTabs: tabs,
      activeBrowserTabId: 'file-a',
      rightPanelOpen: true,
    })))

    act(() => result.current.push(makeEntry({ contentMode: 'topology' })))

    let prev: NavHistoryEntry | null = null
    act(() => { prev = result.current.goBack() })
    expect(prev!.rightTabs.length).toBe(2)
    expect(prev!.rightTabs[0]?.id).toBe('file-a')
    expect(prev!.activeBrowserTabId).toBe('file-a')
    expect(prev!.rightPanelOpen).toBe(true)
  })

  it('updates current entry in-place when file tab viewMode changes', () => {
    const { result } = renderHook(() => useNavHistory())

    const fileTab = (viewMode?: string) => ({
      id: 'file-x',
      type: 'file',
      label: 'x.ts',
      payload: { filePath: 'x.ts', viewMode: viewMode ?? 'source' },
    })

    act(() => result.current.push(makeEntry({
      contentMode: 'conversation',
      rightTabs: [fileTab('source')],
      activeBrowserTabId: 'file-x',
      rightPanelOpen: true,
    })))

    // Same file, switch to diff mode — should NOT create a new back entry
    act(() => result.current.push(makeEntry({
      contentMode: 'conversation',
      rightTabs: [fileTab('diff')],
      activeBrowserTabId: 'file-x',
      rightPanelOpen: true,
    })))

    expect(result.current.canGoBack).toBe(false)

    // Push a genuinely different entry (topology), then go back.
    // The restored entry should have the latest viewMode ('diff').
    act(() => result.current.push(makeEntry({ contentMode: 'topology' })))
    expect(result.current.canGoBack).toBe(true)

    let prev: NavHistoryEntry | null = null
    act(() => { prev = result.current.goBack() })
    expect(prev!.contentMode).toBe('conversation')
    expect((prev!.rightTabs[0]!.payload as { viewMode: string }).viewMode).toBe('diff')
  })

  it('records agent switches as distinct navigation entries', () => {
    const { result } = renderHook(() => useNavHistory())

    act(() => result.current.push(makeEntry({ contentMode: 'conversation', activeAgentId: 'agent-1' })))
    act(() => result.current.push(makeEntry({ contentMode: 'conversation', activeAgentId: 'agent-2' })))

    expect(result.current.canGoBack).toBe(true)

    let prev: NavHistoryEntry | null = null
    act(() => { prev = result.current.goBack() })
    expect(prev!.activeAgentId).toBe('agent-1')

    let next: NavHistoryEntry | null = null
    act(() => { next = result.current.goForward() })
    expect(next!.activeAgentId).toBe('agent-2')
  })

  it('does not create a back entry when the agent is unchanged', () => {
    const { result } = renderHook(() => useNavHistory())

    act(() => result.current.push(makeEntry({ contentMode: 'conversation', activeAgentId: 'agent-1' })))
    // Same agent, different cursor — deduplicated
    act(() => result.current.push(makeEntry({ contentMode: 'conversation', activeAgentId: 'agent-1', cursorPos: 10 })))

    expect(result.current.canGoBack).toBe(false)
  })

  it('null and string agent ids are distinct entries', () => {
    const { result } = renderHook(() => useNavHistory())

    act(() => result.current.push(makeEntry({ contentMode: 'conversation', activeAgentId: null })))
    act(() => result.current.push(makeEntry({ contentMode: 'conversation', activeAgentId: 'agent-1' })))

    expect(result.current.canGoBack).toBe(true)
  })

  describe('tab close trajectory', () => {
    const tab = (id: string) => ({ id, type: 'file', label: `${id}.ts`, payload: { filePath: `${id}.ts` } })

    it('backPastTab retreats to the previous trajectory entry and strips the closed tab', () => {
      const { result } = renderHook(() => useNavHistory())

      act(() => result.current.push(makeEntry({ contentMode: 'topology', rightTabs: [tab('file-a')], activeBrowserTabId: 'file-a', rightPanelOpen: true })))
      act(() => result.current.push(makeEntry({ rightTabs: [tab('file-a'), tab('file-b')], activeBrowserTabId: 'file-b', rightPanelOpen: true })))

      let restored: NavHistoryEntry | null = null
      act(() => { restored = result.current.backPastTab('file-b') })
      expect(restored!.contentMode).toBe('topology')
      expect(restored!.activeBrowserTabId).toBe('file-a')
      expect(restored!.rightTabs.map(t => t.id)).toEqual(['file-a'])
      expect(result.current.canGoBack).toBe(false)
      expect(result.current.canGoForward).toBe(false)
    })

    it('backPastTab drops trajectory points recorded while the closed tab was active', () => {
      const { result } = renderHook(() => useNavHistory())

      act(() => result.current.push(makeEntry({ contentMode: 'topology', rightTabs: [tab('file-a')], activeBrowserTabId: 'file-a' })))
      act(() => result.current.push(makeEntry({ contentMode: 'conversation', rightTabs: [tab('file-a'), tab('file-b')], activeBrowserTabId: 'file-b' })))
      act(() => result.current.push(makeEntry({ contentMode: 'files', rightTabs: [tab('file-a'), tab('file-b')], activeBrowserTabId: 'file-b' })))

      let restored: NavHistoryEntry | null = null
      act(() => { restored = result.current.backPastTab('file-b') })
      // Skips the two file-b-active points, landing on the topology entry.
      expect(restored!.contentMode).toBe('topology')
      expect(restored!.activeBrowserTabId).toBe('file-a')
      expect(result.current.canGoBack).toBe(false)
    })

    it('backPastTab returns null without a usable predecessor and keeps forward resurrection-free', () => {
      const { result } = renderHook(() => useNavHistory())

      act(() => result.current.push(makeEntry({ rightTabs: [tab('file-a'), tab('file-b')], activeBrowserTabId: 'file-b' })))
      act(() => result.current.push(makeEntry({ rightTabs: [tab('file-a'), tab('file-b'), tab('file-c')], activeBrowserTabId: 'file-c' })))
      act(() => result.current.goBack()) // current: file-b active, forward holds file-c entry

      let restored: NavHistoryEntry | null = makeEntry()
      act(() => { restored = result.current.backPastTab('file-b') })
      expect(restored).toBeNull()

      let fwd: NavHistoryEntry | null = null
      act(() => { fwd = result.current.goForward() })
      expect(fwd!.activeBrowserTabId).toBe('file-c')
      expect(fwd!.rightTabs.map(t => t.id)).toEqual(['file-a', 'file-c'])
    })

    it('backPastTab keeps older valid entries reachable after the retreat', () => {
      const { result } = renderHook(() => useNavHistory())

      act(() => result.current.push(makeEntry({ rightTabs: [tab('file-a')], activeBrowserTabId: 'file-a' })))
      act(() => result.current.push(makeEntry({ contentMode: 'files', rightTabs: [tab('file-a'), tab('file-b')], activeBrowserTabId: 'file-b' })))
      act(() => result.current.push(makeEntry({ contentMode: 'workflow', rightTabs: [tab('file-a'), tab('file-b')], activeBrowserTabId: 'file-a' })))
      act(() => result.current.push(makeEntry({ rightTabs: [tab('file-a'), tab('file-b')], activeBrowserTabId: 'file-b' })))

      let restored: NavHistoryEntry | null = null
      act(() => { restored = result.current.backPastTab('file-b') })
      // Nearest surviving point: the entry where file-a was active again,
      // purged of file-b.
      expect(restored!.activeBrowserTabId).toBe('file-a')
      expect(restored!.rightTabs.map(t => t.id)).toEqual(['file-a'])

      // The oldest entry also survives, purged of the closed tab.
      let prev: NavHistoryEntry | null = null
      act(() => { prev = result.current.goBack() })
      expect(prev!.activeBrowserTabId).toBe('file-a')
      expect(prev!.rightTabs.map(t => t.id)).toEqual(['file-a'])
    })

    it('removeTab purges a background tab from the whole trajectory without moving', () => {
      const { result } = renderHook(() => useNavHistory())

      act(() => result.current.push(makeEntry({ rightTabs: [tab('file-a'), tab('file-b')], activeBrowserTabId: 'file-a' })))
      act(() => result.current.push(makeEntry({ rightTabs: [tab('file-a'), tab('file-b'), tab('file-c')], activeBrowserTabId: 'file-c' })))

      act(() => result.current.removeTab('file-b'))
      expect(result.current.canGoBack).toBe(true)

      let prev: NavHistoryEntry | null = null
      act(() => { prev = result.current.goBack() })
      expect(prev!.rightTabs.map(t => t.id)).toEqual(['file-a'])
      expect(prev!.activeBrowserTabId).toBe('file-a')

      let fwd: NavHistoryEntry | null = null
      act(() => { fwd = result.current.goForward() })
      expect(fwd!.rightTabs.map(t => t.id)).toEqual(['file-a', 'file-c'])
      expect(fwd!.activeBrowserTabId).toBe('file-c')
    })
  })
})
