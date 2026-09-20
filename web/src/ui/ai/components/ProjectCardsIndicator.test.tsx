import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ProjectCardsIndicator } from './ProjectCardsIndicator'
import type { ProjectCardState } from '../../../application/project-card-client'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

let currentState: ProjectCardState = { projectId: 'project-1', refs: [], loading: false, error: null }
const listeners = new Set<(state: ProjectCardState) => void>()

vi.mock('../../../application/project-card-client', () => ({
  projectCardStore: {
    getState: () => currentState,
    subscribe: (listener: (state: ProjectCardState) => void) => {
      listeners.add(listener)
      listener(currentState)
      return () => listeners.delete(listener)
    },
  },
}))

function renderIndicator() {
  const container = document.createElement('div')
  document.body.appendChild(container)
  let root: Root
  act(() => {
    root = createRoot(container)
    root.render(<ProjectCardsIndicator />)
  })
  return { container, unmount: () => { act(() => root!.unmount()); container.remove() } }
}

afterEach(() => {
  listeners.clear()
  currentState = { projectId: 'project-1', refs: [], loading: false, error: null }
})

describe('ProjectCardsIndicator', () => {
  it('renders nothing for an empty project card list', () => {
    const view = renderIndicator()
    expect(view.container.textContent).toBe('')
    view.unmount()
  })

  it('renders the mounted card count', () => {
    currentState = { ...currentState, refs: [{ Id: 'builtin:mode:goal' }] }
    const view = renderIndicator()
    expect(view.container.textContent).toBe('Cards: 1')
    view.unmount()
  })

  it('renders the load error', () => {
    currentState = { ...currentState, error: 'load failed' }
    const view = renderIndicator()
    expect(view.container.textContent).toBe('Cards: load failed')
    view.unmount()
  })
})
