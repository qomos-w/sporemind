import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ToolCallBlock } from './ToolCallBlock'
import { TimelineIconSlot } from '../timeline/TimelineIconSlot'
import { SmoothStreamContext } from '../../context/SmoothStreamContext'
import { DEFAULT_UI_EXPANSION_MODES } from '../../../../application/workspace-ui-state'
import type { ToolFrame } from '../../model/frame-types'

vi.mock('../../../../i18n/provider', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

let container: HTMLElement
let root: Root

function makeReadFrame(id: string, status: ToolFrame['status']): ToolFrame {
  return {
    id,
    type: 'tool',
    status,
    toolName: 'project.read',
    input: JSON.stringify({ Path: 'src/main.go' }),
    output: 'package main\n\nfunc main() {}',
    version: 1,
  } as ToolFrame
}

function makeSearchFrame(id: string, status: ToolFrame['status']): ToolFrame {
  return {
    id,
    type: 'tool',
    status,
    toolName: 'project.grep',
    input: JSON.stringify({ Pattern: 'main', Path: 'src' }),
    output: '{"Files":["src/main.go"]}',
    version: 1,
  } as ToolFrame
}

const ctx = (streaming: boolean) => (
  { smoothStream: true, isStreaming: streaming, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }
)

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.restoreAllMocks()
})

/**
 * Regression: ToolFoldCard must pass forceHovered to TimelineIconSlot so the
 * left icon turns into an arrow on hover (matching TimelineStep / reasoning).
 * happy-dom cannot synthesize React onMouseEnter from dispatched mouseover, so
 * the two layers are exercised separately:
 *   (1) TimelineIconSlot with forceHovered={true} renders the right chevron.
 *   (2) ToolFoldCard-based tool cards render an expandable, collapsed row whose
 *       icon slot starts in the 'icon' arrow-state, proving the wiring is in
 *       place for hover to drive it.
 */
describe('ToolFoldCard hover wiring', () => {
  it('TimelineIconSlot shows the right arrow when forceHovered', () => {
    act(() => {
      root.render(
        <TimelineIconSlot
          status="completed"
          icon={<span data-testid="ic">x</span>}
          expandable
          expanded={false}
          iconPhase="idle"
          forceHovered
        />
      )
    })
    const slot = container.querySelector('.ai-step-icon-slot') as HTMLElement
    expect(slot).toBeTruthy()
    expect(slot.getAttribute('data-expandable')).toBe('true')
    // forceHovered drives effectiveHovered: the icon flips to the right chevron.
    expect(slot.getAttribute('data-arrow-state')).toBe('right')
    // The right chevron arrow span is present.
    expect(slot.querySelector('.ai-step-icon-arrow-right')).toBeTruthy()
  })

  it('TimelineIconSlot keeps the tool icon at rest while expanded', () => {
    act(() => {
      root.render(
        <TimelineIconSlot
          status="completed"
          icon={<span data-testid="ic">x</span>}
          expandable
          expanded
          iconPhase="idle"
        />
      )
    })
    const slot = container.querySelector('.ai-step-icon-slot') as HTMLElement
    expect(slot).toBeTruthy()
    // Regression: an expanded step must rest in the 'icon' arrow-state so the
    // tool icon stays visible; the chevron is a hover-only affordance.
    expect(slot.getAttribute('data-arrow-state')).toBe('icon')
    expect(slot.querySelector('.ai-step-icon-current [data-testid="ic"]')).toBeTruthy()
  })

  it('TimelineIconSlot shows the down chevron when hovering an expanded step', () => {
    act(() => {
      root.render(
        <TimelineIconSlot
          status="completed"
          icon={<span data-testid="ic">x</span>}
          expandable
          expanded
          iconPhase="idle"
          forceHovered
        />
      )
    })
    const slot = container.querySelector('.ai-step-icon-slot') as HTMLElement
    expect(slot).toBeTruthy()
    // Hover on an expanded row hints collapse: the down chevron replaces the icon.
    expect(slot.getAttribute('data-arrow-state')).toBe('down')
    expect(slot.querySelector('.ai-step-icon-arrow-down')).toBeTruthy()
  })

  it('read/search tool cards render an expandable collapsed row with arrow-state=icon', async () => {
    const read = makeReadFrame('f-read', 'completed')
    const search = makeSearchFrame('f-search', 'completed')
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={ctx(false) as never}>
          <ToolCallBlock frame={read} connectorMode="none" />
          <ToolCallBlock frame={search} connectorMode="none" />
        </SmoothStreamContext.Provider>
      )
    })

    const rows = container.querySelectorAll('.ai-step-row')
    expect(rows.length).toBe(2)

    const readSlot = rows[0]!.querySelector('.ai-step-icon-slot') as HTMLElement
    const searchSlot = rows[1]!.querySelector('.ai-step-icon-slot') as HTMLElement

    // Both cards are expandable (completed => hasBody) and collapsed
    // (history-loaded fold-expand-collapse). The arrow-state starts at 'icon',
    // ready to flip to 'right' on row hover via forceHovered.
    expect(readSlot.getAttribute('data-expandable')).toBe('true')
    expect(readSlot.getAttribute('data-arrow-state')).toBe('icon')
    expect(readSlot.hasAttribute('data-hovered')).toBe(false)

    expect(searchSlot.getAttribute('data-expandable')).toBe('true')
    expect(searchSlot.getAttribute('data-arrow-state')).toBe('icon')
    expect(searchSlot.hasAttribute('data-hovered')).toBe(false)

    // Neither card carries the sibling's hovered state.
    expect(readSlot.hasAttribute('data-hovered')).toBe(false)
    expect(searchSlot.hasAttribute('data-hovered')).toBe(false)
  })
})

/**
 * Regression: when switching agents, completed frames from a non-streaming
 * turn must mount at their terminal state (collapsed for fold-expand-collapse)
 * even when the session-level SmoothStreamContext.isStreaming is true (because
 * another agent is still streaming). The per-envelope turnStreaming prop
 * overrides the global context, preventing animation replay.
 */
describe('per-envelope turnStreaming override', () => {
  it('completed frame mounts collapsed when turnStreaming=false even if context isStreaming=true', async () => {
    const frame = makeReadFrame('f-read', 'completed')
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={ctx(true) as never}>
          <ToolCallBlock frame={frame} connectorMode="none" turnStreaming={false} />
        </SmoothStreamContext.Provider>
      )
    })
    // historyLoaded = true (turnStreaming false, frame not active):
    // fold-expand-collapse terminal state is COLLAPSED.
    const collapsible = container.querySelector('.ai-collapsible') as HTMLElement
    expect(collapsible).toBeTruthy()
    expect(collapsible.classList.contains('open')).toBe(false)
    // No enter animation class on the icon.
    const iconEnter = container.querySelector('.ai-step-icon-enter')
    expect(iconEnter).toBeNull()
  })

  it('completed frame animates when turnStreaming=true (same turn streaming)', async () => {
    const frame = makeReadFrame('f-read', 'completed')
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={ctx(false) as never}>
          <ToolCallBlock frame={frame} connectorMode="none" turnStreaming={true} />
        </SmoothStreamContext.Provider>
      )
    })
    // turnStreaming true: historyLoaded is false, so the mount effect arms
    // the title-first delay. Body should be collapsed right after mount.
    const collapsible = container.querySelector('.ai-collapsible') as HTMLElement
    expect(collapsible).toBeTruthy()
    expect(collapsible.classList.contains('open')).toBe(false)
  })
})
