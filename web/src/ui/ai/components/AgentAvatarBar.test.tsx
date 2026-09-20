import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AgentAvatarBar } from './AgentAvatarBar'
import type { AgentInfo } from '../hooks/agentInfoStore'
import type { AgentChildRef } from '../../../gen-clients/system/types'
import { SHELL_CONTENT_MODE_EVENT } from '../hooks/worktreeGitJump'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: 'en' }),
}))

vi.mock('../../../i18n/provider', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: 'en' }),
}))

// ── git-store mock (worktree badge jump) ────────────────────────────────────
const gitMock = vi.hoisted(() => {
  const gitState = {
    projectId: '',
    worktreeId: null as string | null,
    commits: [{ Hash: 'head-1', Short: 'head1', Message: 'head commit' }],
    loading: false,
  }
  const gitListeners = new Set<() => void>()
  function gitEmit() {
    for (const fn of gitListeners) fn()
  }
  const gitSetContext = vi.fn()
  const gitSelectCommit = vi.fn(async () => [])
  return { gitState, gitListeners, gitEmit, gitSetContext, gitSelectCommit }
})

vi.mock('../../panels/git-store', () => ({
  gitStore: {
    get state() { return gitMock.gitState },
    subscribe(fn: () => void) {
      gitMock.gitListeners.add(fn)
      return () => gitMock.gitListeners.delete(fn)
    },
    setContext: gitMock.gitSetContext,
    selectCommit: gitMock.gitSelectCommit,
  },
}))

const baseAgent: AgentInfo = {
  Id: 'agent-1',
  ActorId: 'actor-1',
  Title: 'Test',
  DisplayName: 'Test Agent',
  AgentKind: 'coder',
  ProjectId: 'proj-1',
  ProjectName: 'Test Project',
  Status: 'idle',
  IsWorking: false,
  IsError: false,
  IsPlanApproval: false, IsGoalSubmit: false,
  IsAskUser: false,
  IsAskPermission: false,
  CanDelete: true,
} as unknown as AgentInfo

const agent = { ...baseAgent }

describe('AgentAvatarBar mobile long press', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('opens the menu on long press without selecting the agent', () => {
    const onAgentClick = vi.fn()
    const onAgentMenuOpen = vi.fn()
    const nowSpy = vi.spyOn(performance, 'now')
    let t = 0
    nowSpy.mockImplementation(() => t)

    vi.useFakeTimers()
    act(() => {
      root.render(
        <AgentAvatarBar
          agents={[agent]}
          activeAgentId={null}
          onAgentClick={onAgentClick}
          onAgentMenuOpen={onAgentMenuOpen}
          showOverflow={false}
        />,
      )
    })

    const button = container.querySelector('.agent-avatar-item') as HTMLButtonElement
    expect(button).not.toBeNull()

    // Start touch hold.
    act(() => {
      t = 0
      button.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerType: 'touch', clientX: 10, clientY: 10 }))
    })

    // Advance past the long-press threshold.
    act(() => {
      vi.advanceTimersByTime(500)
      t = 500
    })

    expect(onAgentMenuOpen).toHaveBeenCalledTimes(1)
    expect(onAgentClick).not.toHaveBeenCalled()

    // Release the touch and synthesize the trailing click.
    act(() => {
      button.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerType: 'touch' }))
      button.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0 }))
      button.dispatchEvent(new MouseEvent('click', { bubbles: true, button: 0 }))
    })

    expect(onAgentClick).not.toHaveBeenCalled()

    nowSpy.mockRestore()
    vi.useRealTimers()
  })
})

describe('AgentAvatarBar dropdown tree + status icons', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  const parent: AgentInfo = {
    ...baseAgent,
    Id: 'parent-1',
    ActorId: 'parent-actor',
    DisplayName: 'Parent Agent',
    ProjectId: 'proj-1',
    ActiveWorkflowMapCardId: 'wf-card-1',
    WorktreeID: 'wt-1',
    WorktreeName: 'feature-branch',
    Children: [{ Id: 'child-1', ActorId: 'child-actor' } as AgentChildRef],
  } as unknown as AgentInfo

  const child: AgentInfo = {
    ...baseAgent,
    Id: 'child-1',
    ActorId: 'child-actor',
    DisplayName: 'Child Worker',
    AgentKind: 'worker',
    Title: 'Fix the bug',
    ParentAgentId: 'parent-actor',
    LifecycleScope: 'workflow',
    BoundTaskCardId: 'task-card-1',
  } as unknown as AgentInfo

  it('renders parent-child tree with guides and status icons in the dropdown', () => {
    act(() => {
      root.render(
        <AgentAvatarBar
          agents={[parent, child]}
          activeAgentId={null}
          onAgentClick={vi.fn()}
          showOverflow
        />,
      )
    })

    // Open the dropdown.
    const moreBtn = container.querySelector('.agent-avatar-more') as HTMLButtonElement
    expect(moreBtn).not.toBeNull()
    act(() => {
      moreBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const items = container.querySelectorAll('.agent-avatar-dropdown-item')
    expect(items.length).toBe(2)

    // Parent (depth 0) has no tree guides; child (depth 1) has guides.
    expect(items[0]!.querySelector('.agent-avatar-dropdown-guides')).toBeNull()
    expect(items[1]!.querySelector('.agent-avatar-dropdown-guides')).not.toBeNull()

    // Parent shows workflow icon (waypoints) and worktree icon.
    const parentIcons = items[0]!.querySelectorAll('.agent-avatar-dropdown-icon')
    expect(parentIcons.length).toBe(2)
    expect(items[0]!.querySelector('.agent-avatar-dropdown-icon.waypoints')).not.toBeNull()
    expect(items[0]!.querySelector('.agent-avatar-dropdown-icon.worktree')).not.toBeNull()

    // Child shows bound-task icon (target).
    const childIcons = items[1]!.querySelectorAll('.agent-avatar-dropdown-icon')
    expect(childIcons.length).toBe(1)
    expect(items[1]!.querySelector('.agent-avatar-dropdown-icon.target')).not.toBeNull()

    // Child label is the Title (worker kind shows Title first).
    const childName = items[1]!.querySelector('.agent-avatar-dropdown-name')
    expect(childName!.textContent).toBe('Fix the bug')

    // Child has a tree connector (last child = L-corner).
    expect(items[1]!.querySelector('.agent-avatar-dropdown-connector.last')).not.toBeNull()
  })
})

describe('AgentAvatarBar lazy-load grayscale', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  const loadedAgent: AgentInfo = { ...baseAgent, LoadState: 'loaded' } as AgentInfo
  const unloadedAgent: AgentInfo = {
    ...baseAgent,
    Id: 'agent-2',
    ActorId: 'actor-2',
    DisplayName: 'Lazy Agent',
    LoadState: 'unloaded',
  } as AgentInfo

  it('marks unloaded agents grayscale in the bar and dropdown, loaded ones stay colored', () => {
    act(() => {
      root.render(
        <AgentAvatarBar
          agents={[loadedAgent, unloadedAgent]}
          activeAgentId={null}
          onAgentClick={vi.fn()}
          showOverflow
        />,
      )
    })

    const initials = container.querySelectorAll('.agent-avatar-initials')
    expect(initials.length).toBe(2)
    expect(initials[0]!.classList.contains('unloaded')).toBe(false)
    expect(initials[1]!.classList.contains('unloaded')).toBe(true)

    // Open the dropdown and verify the same rule there.
    const moreBtn = container.querySelector('.agent-avatar-more') as HTMLButtonElement
    act(() => {
      moreBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const dropdownAvatars = container.querySelectorAll('.agent-avatar-dropdown-avatar')
    expect(dropdownAvatars.length).toBe(2)
    expect(dropdownAvatars[0]!.classList.contains('unloaded')).toBe(false)
    expect(dropdownAvatars[1]!.classList.contains('unloaded')).toBe(true)
  })
})

describe('AgentAvatarBar wheel horizontal scroll', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  function mockScrollMetrics(el: HTMLElement, scrollWidth: number, clientWidth: number) {
    Object.defineProperty(el, 'scrollWidth', { value: scrollWidth, configurable: true })
    Object.defineProperty(el, 'clientWidth', { value: clientWidth, configurable: true })
  }

  it('translates vertical wheel into horizontal scroll and prevents default', () => {
    act(() => {
      root.render(
        <AgentAvatarBar
          agents={[agent]}
          activeAgentId={null}
          onAgentClick={vi.fn()}
          showOverflow={false}
        />,
      )
    })

    const scroller = container.querySelector('.agent-avatar-scroll') as HTMLDivElement
    expect(scroller).not.toBeNull()
    mockScrollMetrics(scroller, 500, 100)

    const evt = new WheelEvent('wheel', { deltaY: 120, cancelable: true })
    scroller.dispatchEvent(evt)
    expect(evt.defaultPrevented).toBe(true)
    expect(scroller.scrollLeft).toBe(120)
  })

  it('lets the event through at the scroll edges and when not scrollable', () => {
    act(() => {
      root.render(
        <AgentAvatarBar
          agents={[agent]}
          activeAgentId={null}
          onAgentClick={vi.fn()}
          showOverflow={false}
        />,
      )
    })

    const scroller = container.querySelector('.agent-avatar-scroll') as HTMLDivElement

    // Not scrollable: content fits.
    mockScrollMetrics(scroller, 100, 100)
    let evt = new WheelEvent('wheel', { deltaY: 120, cancelable: true })
    scroller.dispatchEvent(evt)
    expect(evt.defaultPrevented).toBe(false)

    // At the end edge: scrolling further right passes through.
    mockScrollMetrics(scroller, 500, 100)
    scroller.scrollLeft = 400
    evt = new WheelEvent('wheel', { deltaY: 120, cancelable: true })
    scroller.dispatchEvent(evt)
    expect(evt.defaultPrevented).toBe(false)
    expect(scroller.scrollLeft).toBe(400)
  })
})

describe('AgentAvatarBar mouse drag-to-pan', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  function mockScrollMetrics(el: HTMLElement, scrollWidth: number, clientWidth: number) {
    Object.defineProperty(el, 'scrollWidth', { value: scrollWidth, configurable: true })
    Object.defineProperty(el, 'clientWidth', { value: clientWidth, configurable: true })
  }

  function renderBar(onAgentClick: () => void) {
    act(() => {
      root.render(
        <AgentAvatarBar
          agents={[agent]}
          activeAgentId={null}
          onAgentClick={onAgentClick}
          showOverflow={false}
        />,
      )
    })
  }

  it('pans the strip horizontally on mouse drag and suppresses the trailing click', () => {
    const onAgentClick = vi.fn()
    renderBar(onAgentClick)

    const scroller = container.querySelector('.agent-avatar-scroll') as HTMLDivElement
    mockScrollMetrics(scroller, 500, 100)
    scroller.scrollLeft = 100

    act(() => {
      scroller.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerId: 1, pointerType: 'mouse', button: 0, clientX: 200, clientY: 10 }))
    })
    // Below the engage threshold: no pan yet.
    act(() => {
      scroller.dispatchEvent(new PointerEvent('pointermove', { bubbles: true, pointerId: 1, pointerType: 'mouse', clientX: 196, clientY: 10 }))
    })
    expect(scroller.classList.contains('panning')).toBe(false)
    expect(scroller.scrollLeft).toBe(100)

    // Past the threshold: engaged, drags 1:1 with the pointer.
    act(() => {
      scroller.dispatchEvent(new PointerEvent('pointermove', { bubbles: true, pointerId: 1, pointerType: 'mouse', clientX: 150, clientY: 10 }))
    })
    expect(scroller.classList.contains('panning')).toBe(true)
    expect(scroller.scrollLeft).toBe(150)

    act(() => {
      scroller.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerId: 1, pointerType: 'mouse' }))
    })
    expect(scroller.classList.contains('panning')).toBe(false)

    // The click synthesized after the drag must not select the avatar.
    const button = container.querySelector('.agent-avatar-item') as HTMLButtonElement
    act(() => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onAgentClick).not.toHaveBeenCalled()
  })

  it('keeps plain clicks working when there is no drag', () => {
    const onAgentClick = vi.fn()
    renderBar(onAgentClick)

    const button = container.querySelector('.agent-avatar-item') as HTMLButtonElement
    act(() => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onAgentClick).toHaveBeenCalledTimes(1)
  })

  it('ignores touch pointers so native overflow scrolling stays in charge', () => {
    const onAgentClick = vi.fn()
    renderBar(onAgentClick)

    const scroller = container.querySelector('.agent-avatar-scroll') as HTMLDivElement
    mockScrollMetrics(scroller, 500, 100)
    scroller.scrollLeft = 0

    act(() => {
      scroller.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerId: 2, pointerType: 'touch', button: 0, clientX: 200, clientY: 10 }))
      scroller.dispatchEvent(new PointerEvent('pointermove', { bubbles: true, pointerId: 2, pointerType: 'touch', clientX: 100, clientY: 10 }))
      scroller.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerId: 2, pointerType: 'touch' }))
    })
    expect(scroller.classList.contains('panning')).toBe(false)
    expect(scroller.scrollLeft).toBe(0)
  })
})

describe('AgentAvatarBar worktree badge jump', () => {
  let container: HTMLDivElement
  let root: Root

  const wtAgent: AgentInfo = {
    ...baseAgent,
    Id: 'wt-agent',
    ActorId: 'wt-actor',
    DisplayName: 'Worktree Agent',
    ProjectId: 'proj-1',
    WorktreeID: 'wt-1',
    WorktreeName: 'feature-branch',
  } as unknown as AgentInfo

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    gitMock.gitListeners.clear()
    gitMock.gitSetContext.mockReset()
    gitMock.gitSelectCommit.mockReset()
    gitMock.gitState.projectId = ''
    gitMock.gitState.worktreeId = null
    gitMock.gitState.commits = [{ Hash: 'head-1', Short: 'head1', Message: 'head commit' }]
    gitMock.gitState.loading = false
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.restoreAllMocks()
  })

  it('clicking the dropdown worktree badge jumps to git mode without selecting the agent', async () => {
    const dispatchSpy = vi.spyOn(window, 'dispatchEvent')
    const onAgentClick = vi.fn()
    gitMock.gitSetContext.mockImplementation(({ projectId, worktreeId }: { projectId: string; worktreeId: string }) => {
      gitMock.gitState.projectId = projectId
      gitMock.gitState.worktreeId = worktreeId
      gitMock.gitState.loading = true
      setTimeout(() => {
        gitMock.gitState.loading = false
        gitMock.gitEmit()
      }, 0)
    })

    act(() => {
      root.render(
        <AgentAvatarBar
          agents={[wtAgent]}
          activeAgentId={null}
          onAgentClick={onAgentClick}
          showOverflow
        />,
      )
    })

    const moreBtn = container.querySelector('.agent-avatar-more') as HTMLButtonElement
    act(() => {
      moreBtn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const badge = container.querySelector('.agent-avatar-dropdown-icon.worktree') as HTMLElement
    expect(badge).not.toBeNull()

    await act(async () => {
      badge.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })

    expect(dispatchSpy).toHaveBeenCalledWith(
      expect.objectContaining({ type: SHELL_CONTENT_MODE_EVENT }),
    )
    expect(gitMock.gitSetContext).toHaveBeenCalledWith({ projectId: 'proj-1', worktreeId: 'wt-1' })
    expect(gitMock.gitSelectCommit).toHaveBeenCalledWith(gitMock.gitState.commits[0])
    expect(onAgentClick).not.toHaveBeenCalled()
  })
})
