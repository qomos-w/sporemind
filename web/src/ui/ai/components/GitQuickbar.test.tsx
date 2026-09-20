import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import type { GitFileStatus, GitBranchInfo } from '../../../gen-clients/system/types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// ── Mocks ──

const state = {
  projects: [],
  selectedProjectId: '',
  projectId: 'project-1',
  worktreeId: 'wt-1',
  branch: 'main',
  files: [
    { Path: 'src/a.ts', Staging: 'M', Worktree: 'M' },
    { Path: 'src/b.ts', Staging: '?', Worktree: '?' },
  ] as GitFileStatus[],
  commits: [] as any[],
  branches: [
    { Name: 'main', Current: true, Hash: 'abc123', IsRemote: false, Remote: '' },
    { Name: 'feature', Current: false, Hash: 'def456', IsRemote: false, Remote: '' },
    { Name: 'origin/main', Current: false, Hash: 'abc123', IsRemote: true, Remote: 'origin' },
    { Name: 'origin/feature', Current: false, Hash: 'def456', IsRemote: true, Remote: 'origin' },
    { Name: 'upstream/release', Current: false, Hash: '789abc', IsRemote: true, Remote: 'upstream' },
  ] as GitBranchInfo[],
  selectedCommit: null as any,
  selectedFile: null as GitFileStatus | null,
  diff: [] as string[],
  commitDiff: [] as string[],
  commitFiles: [] as any[],
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
const refresh = vi.fn()
const selectFile = vi.fn((f: GitFileStatus | null) => {
  state.selectedFile = f
  state.diff = f ? ['@@ -1,1 +1,2 @@'] : []
  emit()
  return state.diff
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
    setContext: vi.fn(),
    selectFile,
    checkout,
  },
}))
vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const { GitQuickbar } = await import('./GitQuickbar')

function render(
  projectId: string | null = 'project-1',
  onOpenDiff = vi.fn(),
  onOpenGitMode = vi.fn(),
) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(<GitQuickbar projectId={projectId} onOpenDiff={onOpenDiff} onOpenGitMode={onOpenGitMode} />)
  })
  return { container, unmount: () => { root.unmount(); container.remove() } }
}

function openDropdown(container: HTMLElement) {
  act(() => {
    ;(container.querySelector('.ai-conversation-quickbar-git-btn') as HTMLElement).click()
  })
}

describe('GitQuickbar', () => {
  beforeEach(() => {
    listeners.clear()
    version = 1
    state.projectId = 'project-1'
    state.worktreeId = 'wt-1'
    state.branch = 'main'
    state.diff = []
    state.selectedFile = null
    state.loading = false
    state.error = null
    vi.clearAllMocks()
  })

  it('renders nothing without a projectId', () => {
    const { container, unmount } = render(null)
    expect(container.querySelector('.ai-conversation-quickbar-git')).toBeFalsy()
    unmount()
  })

  it('renders branch button with WT badge and file count badge', () => {
    const { container, unmount } = render()
    const btn = container.querySelector('.ai-conversation-quickbar-git-btn')
    expect(btn).toBeTruthy()
    expect(btn?.textContent).toContain('main')
    expect(container.querySelector('.ai-conversation-quickbar-git-wt-badge')).toBeTruthy()
    expect(container.querySelector('.ai-conversation-quickbar-git-badge')).toBeTruthy()
    unmount()
  })

  it('opens dropdown when the branch button is clicked', () => {
    const { container, unmount } = render()
    openDropdown(container)
    expect(container.querySelector('.ai-conversation-quickbar-git-dropdown')).toBeTruthy()
    expect(container.querySelector('.ai-conversation-quickbar-git-backdrop')).toBeTruthy()
    unmount()
  })

  describe('branch dropdown — own branches only', () => {
    it('lists own branches without local/remote labels, trunk or remotes', () => {
      const { container, unmount } = render()
      openDropdown(container)
      const sectionTexts = Array.from(container.querySelectorAll('.ai-conversation-quickbar-git-section'))
        .map(s => s.textContent)
      expect(sectionTexts.some(text => text?.includes('gitPanel.branchLocal'))).toBe(false)
      expect(container.querySelectorAll('.ai-conversation-quickbar-git-remote-label')).toHaveLength(0)
      const labels = Array.from(container.querySelectorAll('.ai-conversation-quickbar-git-item .ai-conversation-quickbar-git-item-label'))
        .map(el => el.textContent)
      expect(labels).toContain('feature')
      expect(labels).not.toContain('main')
      expect(labels).not.toContain('origin/feature')
      expect(labels).not.toContain('upstream/release')
      unmount()
    })

    it('lists own branches with hash', () => {
      const { container, unmount } = render()
      openDropdown(container)
      const featureItem = Array.from(container.querySelectorAll('.ai-conversation-quickbar-git-item'))
        .find(el => el.querySelector('.ai-conversation-quickbar-git-item-label')?.textContent === 'feature')
      expect(featureItem).toBeTruthy()
      expect(featureItem?.textContent).toContain('def456')
      expect(featureItem?.classList.contains('active')).toBe(false)
      unmount()
    })

    it('calls gitStore.checkout when a non-current branch is clicked', () => {
      const { container, unmount } = render()
      openDropdown(container)
      const featureItem = Array.from(container.querySelectorAll('.ai-conversation-quickbar-git-item'))
        .find(el => el.querySelector('.ai-conversation-quickbar-git-item-label')?.textContent === 'feature')
      expect(featureItem).toBeTruthy()
      act(() => {
        ;(featureItem as HTMLElement).click()
      })
      expect(checkout).toHaveBeenCalledWith('feature')
      unmount()
    })
  })

  describe('file diff — reuses gitStore.selectFile', () => {
    it('calls onOpenDiff with diff content from gitStore.selectFile', async () => {
      const onOpenDiff = vi.fn()
      const { container, unmount } = render('project-1', onOpenDiff)
      openDropdown(container)
      const fileItem = container.querySelector('.ai-conversation-quickbar-git-file-item')
      expect(fileItem).toBeTruthy()
      await act(async () => {
        ;(fileItem as HTMLElement).click()
      })
      expect(selectFile).toHaveBeenCalled()
      expect(onOpenDiff).toHaveBeenCalledWith('src/a.ts', expect.stringContaining('@@'))
      unmount()
    })
  })

  describe('refresh and open git mode', () => {
    it('calls gitStore.refresh on refresh button click', () => {
      const { container, unmount } = render()
      openDropdown(container)
      const refreshBtn = Array.from(container.querySelectorAll('.ai-conversation-quickbar-git-item')).find(
        el => el.textContent?.includes('Refresh')
      )
      expect(refreshBtn).toBeTruthy()
      act(() => {
        ;(refreshBtn as HTMLElement).click()
      })
      expect(refresh).toHaveBeenCalled()
      unmount()
    })

    it('calls onOpenGitMode when Open Git Mode is clicked', () => {
      const onOpenGitMode = vi.fn()
      const { container, unmount } = render('project-1', vi.fn(), onOpenGitMode)
      openDropdown(container)
      const openBtn = Array.from(container.querySelectorAll('.ai-conversation-quickbar-git-item')).find(
        el => el.textContent?.includes('Open Git Mode')
      )
      expect(openBtn).toBeTruthy()
      act(() => {
        ;(openBtn as HTMLElement).click()
      })
      expect(onOpenGitMode).toHaveBeenCalled()
      unmount()
    })
  })
})