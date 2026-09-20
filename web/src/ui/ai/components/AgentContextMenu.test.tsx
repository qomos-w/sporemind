import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AgentContextMenu, type AgentMenuTarget } from './AgentContextMenu'
import type { AgentInfo } from '../hooks/agentInfoStore'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const agent: AgentInfo = {
  Id: 'agent-1',
  ActorId: 'actor-1',
  Title: 'Test',
  DisplayName: 'Test Agent',
  ProjectId: 'proj-1',
  Status: 'idle',
  IsWorking: false,
  IsError: false,
  CanDelete: true,
} as unknown as AgentInfo

function makeTarget(): AgentMenuTarget {
  return { agent, x: 10, y: 10 }
}

function dispatchDown(type: 'mousedown' | 'contextmenu', button: number) {
  document.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, button }))
}

describe('AgentContextMenu outside-click handling', () => {
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

  function render(target: AgentMenuTarget | null, onClose: () => void) {
    act(() => {
      root.render(
        <AgentContextMenu
          target={target}
          onClose={onClose}
          onOpenChat={() => {}}
          onEdit={() => {}}
          onClone={() => {}}
          onDelete={() => {}}
        />,
      )
    })
  }

  it('closes on a left-click (button 0) outside the menu', () => {
    const nowSpy = vi.spyOn(performance, 'now')
    let t = 0
    nowSpy.mockImplementation(() => t)
    const onClose = vi.fn()
    render(makeTarget(), onClose) // menu opens, open-guard stamped at t=0
    t = 300 // advance past the 200ms open-guard
    dispatchDown('mousedown', 0)
    expect(onClose).toHaveBeenCalledTimes(1)
    nowSpy.mockRestore()
  })

  it('does NOT close on a right-click mousedown (button 2) outside the menu', () => {
    const onClose = vi.fn()
    render(makeTarget(), onClose)
    vi.useFakeTimers()
    act(() => { vi.advanceTimersByTime(250) })
    vi.useRealTimers()
    // A right-click must be handled by the contextmenu listener, not the
    // mousedown listener, otherwise the menu is torn down during mousedown and
    // re-opened by contextmenu on a separate task (the "click twice" flicker).
    dispatchDown('mousedown', 2)
    expect(onClose).not.toHaveBeenCalled()
  })

  it('closes on a right-click contextmenu event outside the menu', () => {
    const nowSpy = vi.spyOn(performance, 'now')
    let t = 0
    nowSpy.mockImplementation(() => t)
    const onClose = vi.fn()
    render(makeTarget(), onClose) // openedAt stamped at t=0
    t = 500 // advance past the 400ms open-guard
    dispatchDown('contextmenu', 2)
    expect(onClose).toHaveBeenCalledTimes(1)
    nowSpy.mockRestore()
  })

  it('does NOT close on a contextmenu that trails the open within the guard (touch long-press flicker)', () => {
    // On a touch WebView a long-press opens the menu, then the browser
    // synthesises a contextmenu ~80–200ms later for the same gesture. That
    // synthesised event must NOT close the freshly-opened menu.
    const nowSpy = vi.spyOn(performance, 'now')
    let t = 0
    nowSpy.mockImplementation(() => t)
    const onClose = vi.fn()
    render(makeTarget(), onClose) // openedAt stamped at t=0
    t = 150 // within the 400ms guard
    dispatchDown('contextmenu', 2)
    expect(onClose).not.toHaveBeenCalled()
    // ...but once the guard elapses, a later outside contextmenu still closes.
    t = 500
    dispatchDown('contextmenu', 2)
    expect(onClose).toHaveBeenCalledTimes(1)
    nowSpy.mockRestore()
  })

  it('does not close when clicking inside the menu', () => {
    const onClose = vi.fn()
    render(makeTarget(), onClose)
    const menuEl = container.querySelector('.agent-context-menu') as HTMLElement
    vi.useFakeTimers()
    act(() => { vi.advanceTimersByTime(250) })
    vi.useRealTimers()
    const evt = new MouseEvent('mousedown', { bubbles: true, cancelable: true, button: 0 })
    Object.defineProperty(evt, 'target', { value: menuEl })
    document.dispatchEvent(evt)
    expect(onClose).not.toHaveBeenCalled()
  })
})

describe('AgentContextMenu delete visibility', () => {
  let container: HTMLDivElement
  let root: Root
  let onDelete: ReturnType<typeof vi.fn<(agent: AgentInfo) => void>>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    onDelete = vi.fn()
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  function renderWithAgent(agent: AgentInfo) {
    act(() => {
      root.render(
        <AgentContextMenu
          target={{ agent, x: 10, y: 10 }}
          onClose={() => {}}
          onOpenChat={() => {}}
          onEdit={() => {}}
          onClone={() => {}}
          onDelete={onDelete}
        />,
      )
    })
    return Array.from(container.querySelectorAll('.agent-context-menu-item'))
      .map(el => el.textContent ?? '')
  }

  it('shows delete for a top-level deletable agent (CanDelete)', () => {
    const labels = renderWithAgent({ ...agent, CanDelete: true, ParentAgentId: '' })
    expect(labels.some(l => l.includes('contextMenu.delete'))).toBe(true)
  })

  it('shows delete for a child agent even when CanDelete is false (fork children / workflow workers)', () => {
    const child = { ...agent, CanDelete: false, ParentAgentId: 'parent-1' } as AgentInfo
    const labels = renderWithAgent(child)
    expect(labels.some(l => l.includes('contextMenu.delete'))).toBe(true)
  })

  it('hides delete for a non-deletable top-level built-in agent', () => {
    const builtin = { ...agent, CanDelete: false, ParentAgentId: '' } as AgentInfo
    const labels = renderWithAgent(builtin)
    expect(labels.some(l => l.includes('contextMenu.delete'))).toBe(false)
  })

  it('invokes onDelete when the delete item is clicked for a child agent', () => {
    const child = { ...agent, Id: 'child-1', CanDelete: false, ParentAgentId: 'parent-1' } as AgentInfo
    renderWithAgent(child)
    const deleteBtn = Array.from(container.querySelectorAll('.agent-context-menu-item'))
      .find(el => (el.textContent ?? '').includes('contextMenu.delete')) as HTMLElement
    act(() => { deleteBtn.click() })
    expect(onDelete).toHaveBeenCalledWith(child)
  })
})
