import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import type { GitFileStatus, GitCommitInfo, GitShowFile, GitBranchInfo } from '../../../gen-clients/system/types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// ── Mocks ──

const state = {
  projects: [],
  selectedProjectId: '',
  projectId: 'project-1',
  worktreeId: 'wt-1',
  branch: 'main',
  // 'all' = git log --all scope; else branch name (see git-store.logBranch)
  logBranch: 'all',
  files: [
    { Path: 'src/a.ts', Staging: 'M', Worktree: 'M' },
    { Path: 'src/b.ts', Staging: ' ', Worktree: 'M' },
  ] as GitFileStatus[],
  commits: [
    // Full-length hash, as the backend gitLog returns (%H).
    { Hash: 'a1b2c3d4e5f60718293a4b5c6d7e8f9012345678', Short: 'a1b2c3d', Message: 'feat: x', Author: 'a', Email: 'a@x', When: '1767225600' },
  ] as GitCommitInfo[],
  branches: [
    // Short hashes, as the backend gitBranch returns (7 chars) — matching the
    // real full-vs-short mismatch the badge renderer must tolerate.
    { Name: 'main', Current: true, Hash: 'a1b2c3d', IsRemote: false, Remote: '' },
    { Name: 'feature', Current: false, Hash: 'f00ba11', IsRemote: false, Remote: '' },
    { Name: 'origin/main', Current: false, Hash: 'a1b2c3d', IsRemote: true, Remote: 'origin' },
    { Name: 'origin/feature', Current: false, Hash: 'f00ba11', IsRemote: true, Remote: 'origin' },
  ] as GitBranchInfo[],
  selectedCommit: null as GitCommitInfo | null,
  selectedFile: null as GitFileStatus | null,
  diff: [] as string[],
  commitDiff: [] as string[],
  commitFiles: [] as GitShowFile[],
  loading: false,
  error: null,
}

const listeners = new Set<() => void>()
let version = 1
function emit() {
  version++
  for (const fn of listeners) fn()
}

const checkout = vi.fn()
const setLogBranch = vi.fn()
const fetchFn = vi.fn()
const commit = vi.fn()
const pull = vi.fn()
const push = vi.fn()
const stageAll = vi.fn()
const refresh = vi.fn()
const stageFileFn = vi.fn()
const unstageFileFn = vi.fn()
const selectCommitFn = vi.fn((c: GitCommitInfo | null) => {
  state.selectedCommit = c
  state.commitFiles = c ? [{ Path: 'src/a.ts', Status: 'M' }] : []
  emit()
  return []
})

vi.mock('../../panels/git-store', () => ({
  gitStore: {
    get state() { return state },
    subscribe(fn: () => void) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
    getVersion: () => version,
    refresh,
    selectFile: vi.fn((f: GitFileStatus | null) => {
      state.selectedFile = f
      state.diff = f ? ['diff line'] : []
      emit()
      return state.diff
    }),
    selectCommit: selectCommitFn,
    loadCommitFileDiffByPath: vi.fn(async (path: string) => {
      state.selectedFile = state.files.find(f => f.Path === path) ?? { Path: path, Staging: ' ', Worktree: ' ' }
      state.diff = ['@@ -1,1 +1,2 @@']
      emit()
      return state.diff
    }),
    stageAll,
    commit,
    pull,
    push,
    fetch: fetchFn,
    checkout,
    setLogBranch,
    loadMoreHistory: vi.fn(async () => undefined),
    stageFile: stageFileFn,
    unstageFile: unstageFileFn,
  },
}))
vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const gitDiff = vi.fn(async (..._args: any[]) => ({ Diff: ['+line'] }))
vi.mock('../../../gen-clients/workspace/client', () => ({
  gitDiff: (...args: any[]) => gitDiff(...args),
}))

const invoke = vi.fn(async (..._args: any[]) => ({ Text: 'feat: generated message' }))
vi.mock('../../../application/generated-client', () => ({
  client: { invoke: (...args: any[]) => invoke(...args) },
}))

let completeMessageResult: unknown = { Text: 'feat: generated message' }
vi.mock('../../../gen-clients/local/client', () => ({
  completeMessage: async (_client: unknown, req: { UserText: string }, opts?: { target?: string }) => {
    invoke('complete_message', req, opts)
    return completeMessageResult
  },
}))

const { GitPanel } = await import('./GitPanel')

function setNativeValue(input: HTMLInputElement, value: string) {
  const descriptor = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')
  if (descriptor && typeof descriptor.set === 'function') {
    descriptor.set.call(input, value)
  } else {
    input.value = value
  }
}

function render(projectId: string | null = 'project-1', onOpenFile = vi.fn(), extra: Record<string, unknown> = {}) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(<GitPanel projectId={projectId} onOpenFile={onOpenFile} {...extra} />)
  })
  return { container, unmount: () => { root.unmount(); container.remove() } }
}

describe('GitPanel', () => {
  beforeEach(() => {
    listeners.clear()
    version = 1
    state.projectId = 'project-1'
    state.worktreeId = 'wt-1'
    state.branch = 'main'
    state.diff = []
    state.commitDiff = []
    state.commitFiles = []
    state.loading = false
    state.error = null
    state.selectedFile = null
    state.selectedCommit = null
    vi.clearAllMocks()
  })

  it('shows no-project placeholder without a projectId', () => {
    const { container, unmount } = render(null)
    expect(container.querySelector('.git-panel-empty')).toBeTruthy()
    unmount()
  })

  it('shows the history scope (All) on the branch button, WT badge and counts', () => {
    const { container, unmount } = render()
    expect(container.querySelector('.git-panel-branch')?.textContent).toContain('gitPanel.branchAll')
    expect(container.querySelector('.git-panel-branch')?.getAttribute('title')).toBe('main')
    expect(container.querySelector('.git-panel-wt-badge')).toBeTruthy()
    expect(container.querySelector('.git-panel-count-staged')).toBeTruthy()
    expect(container.querySelector('.git-panel-count-unstaged')).toBeTruthy()
    unmount()
  })

  it('renders the working-tree node above history with the dual pane open by default', () => {
    const { container, unmount } = render()
    const wtRow = container.querySelector('.git-panel-wt-row') as HTMLElement
    expect(wtRow).toBeTruthy()
    expect(wtRow.classList.contains('active')).toBe(true)
    expect(wtRow.querySelector('.git-panel-commit-msg')?.textContent).toContain('gitPanel.workingTree')
    // First row in the list is the working-tree node.
    expect(container.querySelector('.git-panel-list .git-panel-commit-row')).toBe(wtRow)
    // Dual pane: staged (a.ts) / unstaged (a.ts + b.ts).
    expect(container.querySelector('.git-panel-wt-detail')).toBeTruthy()
    const panes = container.querySelectorAll('.git-panel-wt-pane')
    expect(panes.length).toBe(2)
    const stagedRows = panes[0]!.querySelectorAll('.git-panel-file-row')
    const unstagedRows = panes[1]!.querySelectorAll('.git-panel-file-row')
    expect(stagedRows.length).toBe(1)
    expect(stagedRows[0]!.textContent).toContain('src/a.ts')
    expect(unstagedRows.length).toBe(2)
    expect(unstagedRows[0]!.textContent).toContain('src/a.ts')
    expect(unstagedRows[1]!.textContent).toContain('src/b.ts')
    // Commit detail hidden while the working tree is selected.
    expect(container.querySelector('.git-panel-commit-detail')).toBeNull()
    unmount()
  })

  it('stages/unstages a file from the dual pane action buttons', () => {
    const { container, unmount } = render()
    const panes = container.querySelectorAll('.git-panel-wt-pane')
    const unstageBtn = panes[0]!.querySelector('.git-panel-file-action') as HTMLElement
    const stageBtn = panes[1]!.querySelectorAll('.git-panel-file-action')[0] as HTMLElement
    act(() => { unstageBtn.click() })
    act(() => { stageBtn.click() })
    expect(unstageFileFn).toHaveBeenCalledWith('src/a.ts')
    expect(stageFileFn).toHaveBeenCalledWith('src/a.ts')
    unmount()
  })

  it('selects a changed file and opens its diff in the right panel', async () => {
    const onOpenFile = vi.fn()
    const { container, unmount } = render('project-1', onOpenFile)
    await act(async () => {
      ;(container.querySelector('.git-panel-wt-pane .git-panel-file-row') as HTMLElement).click()
    })
    expect(state.selectedFile).toBe(state.files[0])
    expect(onOpenFile).toHaveBeenCalled()
    unmount()
  })

  it('shows the history list below the working-tree node', () => {
    const { container, unmount } = render()
    expect(container.querySelector('.git-panel-commit-row:not(.git-panel-wt-row)')).toBeTruthy()
    expect(container.querySelector('.git-panel-commit-row:not(.git-panel-wt-row) .git-panel-commit-msg')?.textContent).toContain('feat: x')
    unmount()
  })

  it('hides the working-tree node when there are no pending changes', () => {
    const savedFiles = state.files
    state.files = []
    const { container, unmount } = render()
    expect(container.querySelector('.git-panel-wt-row')).toBeNull()
    expect(container.querySelector('.git-panel-commit-row:not(.git-panel-wt-row)')).toBeTruthy()
    unmount()
    state.files = savedFiles
  })

  it('renders a column header with draggable dividers above history rows', () => {
    const { container, unmount } = render()
    const head = container.querySelector('.git-panel-history-head')
    expect(head).toBeTruthy()
    expect(head!.querySelectorAll('.git-panel-history-head-cell').length).toBe(5)
    expect(head!.querySelectorAll('.git-panel-history-head-divider').length).toBe(4)
    expect(head!.querySelector('.git-panel-col-author')?.textContent).toContain('gitPanel.colAuthor')
    expect(head!.querySelector('.git-panel-col-date')?.textContent).toContain('gitPanel.colDate')
    // Row cells carry matching column classes.
    expect(container.querySelector('.git-panel-commit-row:not(.git-panel-wt-row) .git-panel-col-hash')).toBeTruthy()
    expect(container.querySelector('.git-panel-commit-row:not(.git-panel-wt-row) .git-panel-col-author')?.textContent).toContain('a')
    expect(container.querySelector('.git-panel-commit-row:not(.git-panel-wt-row) .git-panel-col-date')?.textContent).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
    unmount()
  })

  it('shows the branch badge at the start of the commit message cell despite short branch hashes', () => {
    const { container, unmount } = render()
    const msgCell = container.querySelector('.git-panel-commit-row:not(.git-panel-wt-row) .git-panel-col-msg') as HTMLElement
    expect(msgCell).toBeTruthy()
    const badge = msgCell.firstElementChild as HTMLElement
    expect(badge.classList.contains('git-panel-branch-ref')).toBe(true)
    expect(badge.querySelector('.git-panel-branch-ref-dot')).toBeTruthy()
    expect(badge.textContent).toContain('main')
    expect(msgCell.lastElementChild?.classList.contains('git-panel-commit-msg')).toBe(true)
    unmount()
  })

  it('marks the current branch badge with a current indicator', () => {
    const { container, unmount } = render()
    const badge = container.querySelector('.git-panel-branch-ref.current') as HTMLElement
    expect(badge).toBeTruthy()
    expect(badge.querySelector('.git-panel-branch-ref-current')).toBeTruthy()
    expect(badge.textContent).toContain('main')
    unmount()
  })

  it('resizes a history column by dragging its header divider', async () => {
    const { container, unmount } = render()
    const head = container.querySelector('.git-panel-history-head') as HTMLElement
    const hashHeadCell = head.querySelector('.git-panel-col-hash') as HTMLElement
    expect(hashHeadCell.style.width).toBe('64px')
    const divider = hashHeadCell.querySelector('.git-panel-history-head-divider') as HTMLElement
    await act(async () => {
      divider.dispatchEvent(new MouseEvent('pointerdown', { clientX: 100, bubbles: true }))
    })
    expect(document.body.classList.contains('git-panel-col-resizing')).toBe(true)
    await act(async () => {
      window.dispatchEvent(new MouseEvent('pointermove', { clientX: 164 }))
    })
    expect(hashHeadCell.style.width).toBe('128px')
    // Row cell follows the header width.
    const rowCell = container.querySelector('.git-panel-commit-row .git-panel-col-hash') as HTMLElement
    expect(rowCell.style.width).toBe('128px')
    await act(async () => {
      window.dispatchEvent(new MouseEvent('pointerup'))
    })
    expect(document.body.classList.contains('git-panel-col-resizing')).toBe(false)
    unmount()
  })

  it('pins the flexible msg column before resizing so its divider actually moves', async () => {
    const rectSpy = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const base = { left: 0, right: 0, top: 0, bottom: 0, height: 0, x: 0, y: 0, toJSON: () => ({}) }
      return { ...base, width: this.classList.contains('git-panel-col-msg') ? 488 : 0 } as DOMRect
    })
    const { container, unmount } = render()
    const msgCell = container.querySelector('.git-panel-history-head .git-panel-col-msg') as HTMLElement
    // Auto mode: msg flexes to fill, no fixed width of its own.
    expect(msgCell.style.flexGrow).toBe('1')
    const divider = msgCell.querySelector('.git-panel-history-head-divider') as HTMLElement
    await act(async () => {
      divider.dispatchEvent(new MouseEvent('pointerdown', { clientX: 100, bubbles: true }))
    })
    // First drag pins msg to its rendered width instead of flex-absorbing.
    expect(msgCell.style.width).toBe('488px')
    expect(msgCell.style.flex).toBe('0 0 488px')
    await act(async () => {
      window.dispatchEvent(new MouseEvent('pointermove', { clientX: 130 }))
    })
    expect(msgCell.style.width).toBe('518px')
    await act(async () => {
      window.dispatchEvent(new MouseEvent('pointerup'))
    })
    rectSpy.mockRestore()
    unmount()
  })

  it('dragging the divider in front of the date column resizes the author column in the drag direction', async () => {
    const rectSpy = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const base = { left: 0, right: 0, top: 0, bottom: 0, height: 0, x: 0, y: 0, toJSON: () => ({}) }
      return { ...base, width: this.classList.contains('git-panel-col-msg') ? 488 : 0 } as DOMRect
    })
    const { container, unmount } = render()
    const authorCell = container.querySelector('.git-panel-history-head .git-panel-col-author') as HTMLElement
    const divider = authorCell.querySelector('.git-panel-history-head-divider') as HTMLElement
    await act(async () => {
      divider.dispatchEvent(new MouseEvent('pointerdown', { clientX: 200, bubbles: true }))
    })
    // msg got pinned so it no longer re-absorbs the author resize.
    const msgCell = container.querySelector('.git-panel-history-head .git-panel-col-msg') as HTMLElement
    expect(msgCell.style.flex).toBe('0 0 488px')
    await act(async () => {
      window.dispatchEvent(new MouseEvent('pointermove', { clientX: 225 }))
    })
    // Dragging right widens author: the boundary follows the cursor.
    expect(authorCell.style.width).toBe('125px')
    await act(async () => {
      window.dispatchEvent(new MouseEvent('pointerup'))
    })
    rectSpy.mockRestore()
    unmount()
  })

  it('shows commit info and changed files when a commit is selected in history', async () => {
    const { container, unmount } = render()
    await act(async () => {
      ;(container.querySelector('.git-panel-commit-row:not(.git-panel-wt-row)') as HTMLElement).click()
    })
    expect(state.selectedCommit).toBe(state.commits[0])
    expect(container.querySelector('.git-panel-commit-detail')).toBeTruthy()
    expect(container.querySelector('.git-panel-commit-detail-msg')?.textContent).toContain('feat: x')
    expect(container.querySelector('.git-panel-commit-detail-file')).toBeTruthy()
    // Dual pane is hidden while a commit is selected.
    expect(container.querySelector('.git-panel-wt-detail')).toBeNull()
    unmount()
  })

  it('clicking the working-tree node after a commit restores the dual pane and clears the selection', async () => {
    const { container, unmount } = render()
    await act(async () => {
      ;(container.querySelector('.git-panel-commit-row:not(.git-panel-wt-row)') as HTMLElement).click()
    })
    expect(container.querySelector('.git-panel-commit-detail')).toBeTruthy()
    await act(async () => {
      ;(container.querySelector('.git-panel-wt-row') as HTMLElement).click()
    })
    expect(selectCommitFn).toHaveBeenCalledWith(null)
    expect(container.querySelector('.git-panel-commit-detail')).toBeNull()
    expect(container.querySelector('.git-panel-wt-detail')).toBeTruthy()
    unmount()
  })

  it('opens a commit file diff in the right panel when a commit file is clicked', async () => {
    const onOpenFile = vi.fn()
    const { container, unmount } = render('project-1', onOpenFile)
    await act(async () => {
      ;(container.querySelector('.git-panel-commit-row:not(.git-panel-wt-row)') as HTMLElement).click()
    })
    await act(async () => {
      ;(container.querySelector('.git-panel-commit-detail-file') as HTMLElement).click()
    })
    expect(onOpenFile).toHaveBeenCalled()
    unmount()
  })

  describe('branch selector', () => {
    it('lists all branches grouped by local/remote with All selected at the top by default', () => {
      const { container, unmount } = render()
      act(() => {
        ;(container.querySelector('.git-panel-branch-selector') as HTMLElement).click()
      })
      const dropdown = container.querySelector('.git-panel-branch-dropdown')
      expect(dropdown).toBeTruthy()
      const sections = dropdown!.querySelectorAll('.git-panel-branch-section')
      expect(sections.length).toBe(2)
      expect(sections[0]!.textContent).toContain('gitPanel.branchLocal')
      expect(sections[1]!.textContent).toContain('gitPanel.branchRemote')
      const items = dropdown!.querySelectorAll('.git-panel-branch-item')
      expect(items.length).toBe(5) // All + 2 local + 2 remote
      const active = dropdown!.querySelector('.git-panel-branch-item.active')
      expect(active?.textContent).toContain('gitPanel.branchAll')
      expect(active?.textContent).not.toContain('main')
      unmount()
    })

    it('shows every branch badge in history when All is selected (default)', () => {
      const { container, unmount } = render()
      const refs = container.querySelectorAll('.git-panel-branch-ref')
      expect(refs.length).toBe(2)
      expect(refs[0]!.textContent).toContain('main')
      expect(refs[1]!.textContent).toContain('origin/main')
      unmount()
    })

    it('filters history badges to the selected branch and reflects it on the button', () => {
      const { container, unmount } = render()
      act(() => {
        ;(container.querySelector('.git-panel-branch-selector') as HTMLElement).click()
      })
      const mainItem = Array.from(container.querySelectorAll('.git-panel-branch-item'))
        .find(el => el.querySelector('.git-panel-branch-item-name')?.textContent === 'main') as HTMLElement
      act(() => {
        mainItem.click()
      })
      const active = container.querySelector('.git-panel-branch-item.active')
      expect(active?.textContent).toContain('main')
      expect(setLogBranch).toHaveBeenCalledWith('main')
      // The branch button now shows the selected scope instead of the current branch.
      expect(container.querySelector('.git-panel-branch')?.textContent).toContain('main')
      const refs = container.querySelectorAll('.git-panel-branch-ref')
      expect(refs.length).toBe(1)
      expect(refs[0]!.textContent).toContain('main')
      unmount()
    })

    it('selecting All again restores every branch badge and the All label', () => {
      const { container, unmount } = render()
      act(() => {
        ;(container.querySelector('.git-panel-branch-selector') as HTMLElement).click()
      })
      // The remote item renders as "main" (remote prefix stripped): it is the
      // second item named "main" in the dropdown (local first, remote after).
      const remoteMainItem = Array.from(container.querySelectorAll('.git-panel-branch-item'))
        .filter(el => el.querySelector('.git-panel-branch-item-name')?.textContent === 'main')[1] as HTMLElement
      act(() => {
        remoteMainItem.click()
      })
      // The dropdown stays open after selecting a branch filter.
      const allItem = container.querySelector('.git-panel-branch-item') as HTMLElement
      act(() => {
        allItem.click()
      })
      expect(setLogBranch).toHaveBeenCalledWith('all')
      expect(container.querySelector('.git-panel-branch')?.textContent).toContain('gitPanel.branchAll')
      const refs = container.querySelectorAll('.git-panel-branch-ref')
      expect(refs.length).toBe(2)
      unmount()
    })
  })

  describe('quick menu', () => {
    it('renders stage-all, commit, fetch, pull and push actions', () => {
      const { container, unmount } = render()
      const menu = container.querySelector('.git-panel-quick-menu')
      expect(menu).toBeTruthy()
      const buttons = menu!.querySelectorAll('.git-panel-quick-btn')
      expect(buttons.length).toBeGreaterThanOrEqual(4)
      expect(menu?.textContent).toContain('gitPanel.stageAll')
      expect(menu?.textContent).toContain('gitPanel.fetch')
      expect(menu?.textContent).toContain('gitPanel.pull')
      expect(menu?.textContent).toContain('gitPanel.push')
      expect(menu?.querySelector('.git-panel-quick-commit')).toBeTruthy()
      unmount()
    })

    it('calls stageAll from the quick menu', () => {
      const { container, unmount } = render()
      const stageBtn = Array.from(container.querySelectorAll('.git-panel-quick-btn')).find(el => el.textContent?.includes('gitPanel.stageAll'))
      act(() => {
        ;(stageBtn as HTMLElement).click()
      })
      expect(stageAll).toHaveBeenCalled()
      unmount()
    })

    it('calls fetch, pull and push from the quick menu', () => {
      const { container, unmount } = render()
      const fetchBtn = Array.from(container.querySelectorAll('.git-panel-quick-btn')).find(el => el.textContent?.includes('gitPanel.fetch'))
      const pullBtn = Array.from(container.querySelectorAll('.git-panel-quick-btn')).find(el => el.textContent?.includes('gitPanel.pull'))
      const pushBtn = Array.from(container.querySelectorAll('.git-panel-quick-btn')).find(el => el.textContent?.includes('gitPanel.push'))
      act(() => {
        ;(fetchBtn as HTMLElement).click()
        ;(pullBtn as HTMLElement).click()
        ;(pushBtn as HTMLElement).click()
      })
      expect(fetchFn).toHaveBeenCalled()
      expect(pull).toHaveBeenCalled()
      expect(push).toHaveBeenCalled()
      unmount()
    })

    it('commits from the quick menu input', () => {
      const { container, unmount } = render()
      const input = container.querySelector('.git-panel-quick-commit-input') as HTMLInputElement
      act(() => {
        setNativeValue(input, 'wip')
        input.dispatchEvent(new Event('input', { bubbles: true }))
      })
      act(() => {
        container.querySelector('.git-panel-quick-commit')?.dispatchEvent(new Event('submit', { bubbles: true }))
      })
      expect(commit).toHaveBeenCalledWith('wip')
      unmount()
    })
  })

  describe('commit provider dropdown + auto-generate', () => {
    const providers = [
      { id: 'p1::m1', label: 'm1', subtitle: 'p1', unit: { model: 'm1', provider: 'p1' } },
      { id: 'p2::m2', label: 'm2', subtitle: 'p2', unit: { model: 'm2', provider: 'p2' } },
    ]

    beforeEach(() => {
      invoke.mockClear()
      gitDiff.mockClear()
    })

    it('hides provider dropdown and generate button without an active agent', () => {
      const { container, unmount } = render('project-1', vi.fn(), { providers })
      expect(container.querySelector('.git-panel-commit-provider')).toBeNull()
      expect(container.querySelector('.git-panel-commit-gen-btn')).toBeNull()
      unmount()
    })

    it('shows the provider dropdown defaulting to the active provider', () => {
      const { container, unmount } = render('project-1', vi.fn(), {
        providers,
        activeProviderId: 'p2::m2',
        agentActorId: 'agent-1',
      })
      const label = container.querySelector('.git-panel-commit-provider-label')
      expect(label?.textContent).toBe('m2')
      act(() => {
        ;(container.querySelector('.git-panel-commit-provider-btn') as HTMLElement).click()
      })
      const items = container.querySelectorAll('.git-panel-commit-provider-item')
      expect(items.length).toBe(2)
      expect(container.querySelector('.git-panel-commit-provider-item.active')?.textContent).toContain('m2')
      unmount()
    })

    it('selects another provider from the dropdown', () => {
      const { container, unmount } = render('project-1', vi.fn(), {
        providers,
        activeProviderId: 'p1::m1',
        agentActorId: 'agent-1',
      })
      act(() => {
        ;(container.querySelector('.git-panel-commit-provider-btn') as HTMLElement).click()
      })
      const second = container.querySelectorAll('.git-panel-commit-provider-item')[1] as HTMLElement
      act(() => {
        second.click()
      })
      expect(container.querySelector('.git-panel-commit-provider-label')?.textContent).toBe('m2')
      unmount()
    })

    it('fills the commit input from complete_message with the selected unit', async () => {
      const { container, unmount } = render('project-1', vi.fn(), {
        providers,
        activeProviderId: 'p1::m1',
        agentActorId: 'agent-1',
        agentName: 'Worker',
      })
      const genBtn = container.querySelector('.git-panel-commit-gen-btn') as HTMLElement
      expect(genBtn).toBeTruthy()
      await act(async () => {
        genBtn.click()
      })
      const input = container.querySelector('.git-panel-quick-commit-input') as HTMLInputElement
      expect(input.value).toBe('feat: generated message')
      expect(invoke).toHaveBeenCalledTimes(1)
      const [method, payload, opts] = invoke.mock.calls[0] as unknown as [string, Record<string, unknown>, Record<string, unknown>]
      expect(method).toBe('complete_message')
      expect(payload.Unit).toEqual({ model: 'm1', provider: 'p1' })
      expect(payload.UserText).toContain('src/a.ts')
      expect(opts.target).toBe('agent-1')
      unmount()
    })

    it('shows aiEmpty instead of crashing when complete_message resolves undefined', async () => {
      completeMessageResult = undefined
      try {
        const { container, unmount } = render('project-1', vi.fn(), {
          providers,
          activeProviderId: 'p1::m1',
          agentActorId: 'agent-1',
        })
        const genBtn = container.querySelector('.git-panel-commit-gen-btn') as HTMLElement
        await act(async () => {
          genBtn.click()
        })
        const input = container.querySelector('.git-panel-quick-commit-input') as HTMLInputElement
        expect(input.value).toBe('')
        expect(input.placeholder).toBe('dialog.commit.aiEmpty')
        unmount()
      } finally {
        completeMessageResult = { Text: 'feat: generated message' }
      }
    })
  })
})
