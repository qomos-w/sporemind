import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { QuickStartActions } from './QuickStartActions'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  requestModeMount: vi.fn(),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

// The row emits its mode-mount request through the module-level
// modeMountStore. Mock the store so we can assert the request directly without
// coupling to the store's internal pending-request state.
vi.mock('./modeMountStore', () => ({
  requestModeMount: hoisted.requestModeMount,
}))

describe('QuickStartActions', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderRow = async () => {
    await act(async () => {
      root.render(<QuickStartActions actorId="actor-1" />)
    })
  }

  const cards = () => Array.from(container.querySelectorAll<HTMLButtonElement>('.ai-quick-start-action'))

  it('renders workflow, goal, and plugin cards with labels and hints', async () => {
    await renderRow()
    expect(cards().some(b => b.textContent?.includes('quickStart.workflow.label'))).toBe(true)
    expect(cards().some(b => b.textContent?.includes('quickStart.workflow.hint'))).toBe(true)
    expect(cards().some(b => b.textContent?.includes('quickStart.goal.label'))).toBe(true)
    expect(cards().some(b => b.textContent?.includes('quickStart.goal.hint'))).toBe(true)
    expect(cards().some(b => b.textContent?.includes('quickStart.plugin.label'))).toBe(true)
    expect(cards().some(b => b.textContent?.includes('quickStart.plugin.hint'))).toBe(true)
  })

  it('clicking the workflow card requests a mode mount', async () => {
    await renderRow()
    expect(hoisted.requestModeMount).not.toHaveBeenCalled()
    const btn = cards().find(b => b.textContent?.includes('quickStart.workflow.label'))
    if (!btn) throw new Error('workflow card not found')
    await act(async () => {
      btn.click()
    })
    expect(hoisted.requestModeMount).toHaveBeenCalledWith('builtin:mode:workflow', 'actor-1')
  })

  it('clicking the goal card requests the goal mode mount', async () => {
    await renderRow()
    const btn = cards().find(b => b.textContent?.includes('quickStart.goal.label'))
    if (!btn) throw new Error('goal card not found')
    await act(async () => {
      btn.click()
    })
    expect(hoisted.requestModeMount).toHaveBeenCalledWith('builtin:mode:goal', 'actor-1')
  })

  it('clicking the plugin card requests the plugin-dev bundle mount', async () => {
    await renderRow()
    const btn = cards().find(b => b.textContent?.includes('quickStart.plugin.label'))
    if (!btn) throw new Error('plugin card not found')
    await act(async () => {
      btn.click()
    })
    expect(hoisted.requestModeMount).toHaveBeenCalledWith('builtin:bundle:plugin-dev', 'actor-1')
  })
})
