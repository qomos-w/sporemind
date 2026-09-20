import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { jumpToWorktreeGit, SHELL_CONTENT_MODE_EVENT, GIT_JUMP_FALLBACK_TIMEOUT_MS } from './worktreeGitJump'

// ── Mocks ────────────────────────────────────────────────────────────────────

const hoisted = vi.hoisted(() => {
  const state = {
    projectId: '',
    worktreeId: null as string | null,
    commits: [] as Array<{ Hash: string; Short: string; Message: string }>,
    loading: false,
  }
  const listeners = new Set<() => void>()
  function emit() {
    for (const fn of listeners) fn()
  }
  const setContext = vi.fn()
  const selectCommit = vi.fn(async () => [])
  return { state, listeners, emit, setContext, selectCommit }
})

vi.mock('../../panels/git-store', () => ({
  gitStore: {
    get state() { return hoisted.state },
    subscribe(fn: () => void) {
      hoisted.listeners.add(fn)
      return () => hoisted.listeners.delete(fn)
    },
    setContext: hoisted.setContext,
    selectCommit: hoisted.selectCommit,
  },
}))

const headCommit = { Hash: 'a1b2c3d4e5f60718293a4b5c6d7e8f9012345678', Short: 'a1b2c3d', Message: 'feat: badge jump' }
const otherCommit = { Hash: 'f00ba1100000000000000000000000000000000f', Short: 'f00ba11', Message: 'chore: older' }

beforeEach(() => {
  hoisted.state.projectId = ''
  hoisted.state.worktreeId = null
  hoisted.state.commits = [headCommit, otherCommit]
  hoisted.state.loading = false
  hoisted.listeners.clear()
  hoisted.setContext.mockReset()
  hoisted.selectCommit.mockReset()
})

afterEach(() => {
  vi.restoreAllMocks()
})

// ── Tests ────────────────────────────────────────────────────────────────────

describe('jumpToWorktreeGit', () => {
  it('dispatches the git content-mode event, sets context, and selects the HEAD commit after load', async () => {
    const dispatchSpy = vi.spyOn(window, 'dispatchEvent')

    // Simulate the real setContext → async refresh → loading false cycle.
    hoisted.setContext.mockImplementation(({ projectId, worktreeId }: { projectId: string; worktreeId: string }) => {
      hoisted.state.projectId = projectId
      hoisted.state.worktreeId = worktreeId
      hoisted.state.loading = true
      setTimeout(() => {
        hoisted.state.loading = false
        hoisted.emit()
      }, 0)
    })

    await jumpToWorktreeGit('proj-1', 'wt-1')

    expect(dispatchSpy).toHaveBeenCalledWith(
      expect.objectContaining({ type: SHELL_CONTENT_MODE_EVENT }),
    )
    expect(hoisted.setContext).toHaveBeenCalledWith({ projectId: 'proj-1', worktreeId: 'wt-1' })
    // Waited for the refresh to settle before selecting, so it is the head commit.
    expect(hoisted.selectCommit).toHaveBeenCalledTimes(1)
    expect(hoisted.selectCommit).toHaveBeenCalledWith(headCommit)
  })

  it('selects commits[0] without waiting when the context is already loaded', async () => {
    vi.spyOn(window, 'dispatchEvent')
    hoisted.setContext.mockImplementation(({ projectId, worktreeId }: { projectId: string; worktreeId: string }) => {
      hoisted.state.projectId = projectId
      hoisted.state.worktreeId = worktreeId
      // No refresh kicked off (context was already active) → loading stays false.
    })

    await jumpToWorktreeGit('proj-1', 'wt-1')

    expect(hoisted.selectCommit).toHaveBeenCalledWith(headCommit)
  })

  it('falls back after the timeout when the refresh never settles', async () => {
    vi.useFakeTimers()
    vi.spyOn(window, 'dispatchEvent')
    hoisted.setContext.mockImplementation(() => {
      hoisted.state.loading = true
      // Never emit / never flip loading back to false.
    })

    const pending = jumpToWorktreeGit('proj-1', 'wt-1')
    vi.advanceTimersByTime(GIT_JUMP_FALLBACK_TIMEOUT_MS)
    await pending

    expect(hoisted.selectCommit).toHaveBeenCalledWith(headCommit)
    vi.useRealTimers()
  })

  it('does nothing when projectId or worktreeId is missing', async () => {
    const dispatchSpy = vi.spyOn(window, 'dispatchEvent')
    await jumpToWorktreeGit('', 'wt-1')
    await jumpToWorktreeGit('proj-1', null)
    await jumpToWorktreeGit('proj-1', undefined)

    expect(dispatchSpy).not.toHaveBeenCalled()
    expect(hoisted.setContext).not.toHaveBeenCalled()
    expect(hoisted.selectCommit).not.toHaveBeenCalled()
  })

  it('skips selection when the worktree has no commits', async () => {
    vi.spyOn(window, 'dispatchEvent')
    hoisted.state.commits = []
    hoisted.setContext.mockImplementation(() => { hoisted.state.loading = false })

    await jumpToWorktreeGit('proj-1', 'wt-1')

    expect(hoisted.setContext).toHaveBeenCalledWith({ projectId: 'proj-1', worktreeId: 'wt-1' })
    expect(hoisted.selectCommit).not.toHaveBeenCalled()
  })
})