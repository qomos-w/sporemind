import { describe, it, expect, vi } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { MessageDrawer } from './MessageDrawer'
import { InteractionDispatchContext } from '../hooks/useStepInteraction'
import type { TurnEnvelope, ToolFrame, TextFrame, AskUserQuestionFrame, PermissionRequestFrame, GoalSubmitFrame } from '../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

// ── Helpers ──

function makeToolFrame(overrides: Partial<ToolFrame> = {}): ToolFrame {
  return {
    id: 'tool-1',
    type: 'tool',
    status: 'completed',
    toolName: 'file_read',
    input: JSON.stringify({ file_path: 'src/main.go' }),
    durationSeconds: 2,
    ...overrides,
  }
}

function makeTextFrame(content: string, id = 'text-1'): TextFrame {
  return { id, type: 'text', status: 'completed', content }
}

function makeUserEnvelope(content: string): TurnEnvelope {
  return { id: 'env-u', role: 'user', userContent: content, frames: [], timestamp: new Date().toISOString() }
}

function makeAssistantEnvelope(frames: TurnEnvelope['frames'], completed = true): TurnEnvelope {
  return { id: 'env-a', role: 'assistant', frames, timestamp: new Date().toISOString(), completed }
}

function renderDrawer(props: Partial<Parameters<typeof MessageDrawer>[0]> & { envelopes: TurnEnvelope[]; dispatch?: (event: any) => Promise<void> }) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  let root: Root
  const drawer = (
    <MessageDrawer
      envelopes={props.envelopes}
      isStreaming={props.isStreaming}
      expanded={props.expanded ?? false}
      onToggle={props.onToggle ?? vi.fn()}
      onFrameSelect={props.onFrameSelect}
      onCancelPendingSubmit={props.onCancelPendingSubmit}
      resizable={props.resizable}
      streamHeight={props.streamHeight}
      onStreamHeightChange={props.onStreamHeightChange}
    />
  )
  act(() => {
    root = createRoot(container)
    root.render(
      props.dispatch
        ? <InteractionDispatchContext.Provider value={props.dispatch}>{drawer}</InteractionDispatchContext.Provider>
        : drawer
    )
  })
  return { container, unmount: () => { root!.unmount(); container.remove() } }
}

function makeAskFrame(overrides: Partial<AskUserQuestionFrame> = {}): AskUserQuestionFrame {
  return {
    id: 'ask-1',
    type: 'ask_user',
    status: 'running',
    questions: [{ header: 'Approach', question: 'Which approach?', options: [{ label: 'Option A' }, { label: 'Option B' }], multiSelect: false }],
    requestId: 'req-ask',
    ...overrides,
  }
}

function makePermissionFrame(overrides: Partial<PermissionRequestFrame> = {}): PermissionRequestFrame {
  return {
    id: 'perm-1',
    type: 'permission_request',
    status: 'running',
    toolCalls: [{ id: 'tc-1', callableId: 'project.write' }],
    requestId: 'req-perm',
    ...overrides,
  }
}

// ── Tests ──

describe('MessageDrawer', () => {
  it('shows empty placeholder when no envelopes', () => {
    const { container, unmount } = renderDrawer({ envelopes: [] })
    const drawer = container.querySelector('.message-drawer')
    expect(drawer).toBeTruthy()
    const peek = container.querySelector('.message-drawer-peek')
    expect(peek).toBeTruthy()
    expect(peek?.textContent).toContain('No messages')
    // Empty state should not have a toggle chevron
    expect(container.querySelector('.message-drawer-peek-chevron')).toBeNull()
    unmount()
  })

  it('renders collapsed peek bar showing last tool status', () => {
    const envs = [makeAssistantEnvelope([makeToolFrame({ status: 'completed', toolName: 'file_read' })])]
    const { container, unmount } = renderDrawer({ envelopes: envs, expanded: false })
    const drawer = container.querySelector('.message-drawer')
    expect(drawer).toBeTruthy()
    expect(drawer?.classList.contains('collapsed')).toBe(true)
    const peek = container.querySelector('.message-drawer-peek')
    expect(peek).toBeTruthy()
    unmount()
  })

  it('shows spinner in peek when tool is running', () => {
    const envs = [makeAssistantEnvelope([makeToolFrame({ status: 'running' })], false)]
    const { container, unmount } = renderDrawer({ envelopes: envs, isStreaming: true })
    const spinner = container.querySelector('.message-drawer-peek-spinner')
    expect(spinner).toBeTruthy()
    unmount()
  })

  it('renders running dot as the first peek element when streaming', () => {
    const envs = [makeAssistantEnvelope([makeToolFrame({ status: 'running' })], false)]
    const { container, unmount } = renderDrawer({ envelopes: envs, isStreaming: true })
    const content = container.querySelector('.message-drawer-peek-content')
    expect(content).toBeTruthy()
    const dot = content!.querySelector('.message-drawer-peek-running-dot')
    expect(dot).toBeTruthy()
    // Running indicator must lead the peek bar
    expect(content!.firstElementChild).toBe(dot)
    // Order: dot → spinner → title → subtitle
    const classOrder = Array.from(content!.children).map(el => el.className)
    const dotIdx = classOrder.findIndex(c => c.includes('message-drawer-peek-running-dot'))
    const titleIdx = classOrder.findIndex(c => c.includes('message-drawer-peek-title'))
    const subtitleIdx = classOrder.findIndex(c => c.includes('message-drawer-peek-subtitle'))
    expect(dotIdx).toBeGreaterThanOrEqual(0)
    expect(titleIdx).toBeGreaterThan(dotIdx)
    expect(subtitleIdx).toBeGreaterThan(titleIdx)
    unmount()
  })

  it('does not render running dot when not streaming', () => {
    const envs = [makeAssistantEnvelope([makeToolFrame({ status: 'completed' })])]
    const { container, unmount } = renderDrawer({ envelopes: envs, isStreaming: false })
    expect(container.querySelector('.message-drawer-peek-running-dot')).toBeNull()
    unmount()
  })

  it('renders pending submits below collapsed peek content and cancels them', () => {
    const onCancelPendingSubmit = vi.fn()
    const envs = [
      {
        ...makeAssistantEnvelope([], false),
        metadata: { pendingSubmits: [{ id: 'pending-1', text: 'Queue this message', timestamp: new Date().toISOString() }] },
      },
    ]
    const { container, unmount } = renderDrawer({ envelopes: envs, onCancelPendingSubmit })
    const peek = container.querySelector('.message-drawer-peek')
    const pending = container.querySelector('.message-drawer-pending-submits')
    expect(peek?.nextElementSibling).toBe(pending)
    expect(pending?.textContent).toContain('Queue this message')
    act(() => { (pending!.querySelector('.ai-pending-submit-cancel') as HTMLButtonElement).click() })
    expect(onCancelPendingSubmit).toHaveBeenCalledWith('pending-1')
    unmount()
  })

  it('calls onToggle when peek bar is clicked', () => {
    const onToggle = vi.fn()
    const envs = [makeUserEnvelope('Hello')]
    const { container, unmount } = renderDrawer({ envelopes: envs, onToggle })
    const peek = container.querySelector('.message-drawer-peek') as HTMLElement
    act(() => { peek.click() })
    expect(onToggle).toHaveBeenCalledTimes(1)
    unmount()
  })

  it('renders expanded view with CompactMessageStream', () => {
    const envs = [
      makeUserEnvelope('Fix the bug'),
      makeAssistantEnvelope([makeTextFrame('Done')]),
    ]
    const { container, unmount } = renderDrawer({ envelopes: envs, expanded: true })
    const drawer = container.querySelector('.message-drawer')
    expect(drawer?.classList.contains('expanded')).toBe(true)
    expect(container.querySelector('.compact-message-stream')).toBeTruthy()
    unmount()
  })

  it('shows user message in peek when no assistant frames', () => {
    const envs = [makeUserEnvelope('What is the status?')]
    const { container, unmount } = renderDrawer({ envelopes: envs })
    const title = container.querySelector('.message-drawer-peek-title')
    expect(title?.textContent).toContain('What is the status?')
    unmount()
  })

  it('shows assistant text in peek for last text frame', () => {
    const envs = [makeAssistantEnvelope([makeTextFrame('All done! Everything works.')])]
    const { container, unmount } = renderDrawer({ envelopes: envs })
    const title = container.querySelector('.message-drawer-peek-title')
    expect(title?.textContent).toContain('All done!')
    unmount()
  })

  it('renders resize handle and applies persisted height when resizable and expanded', () => {
    const envs = [makeUserEnvelope('Hello')]
    const { container, unmount } = renderDrawer({ envelopes: envs, expanded: true, resizable: true, streamHeight: 420 })
    const handle = container.querySelector('.message-drawer-resize-handle')
    expect(handle).toBeTruthy()
    const wrap = container.querySelector('.message-drawer-stream-wrap') as HTMLElement
    expect(wrap.classList.contains('resizable')).toBe(true)
    expect(wrap.style.maxHeight).toBe('420px')
    unmount()
  })

  it('omits resize handle when not resizable (mobile keeps original sizing)', () => {
    const envs = [makeUserEnvelope('Hello')]
    const { container, unmount } = renderDrawer({ envelopes: envs, expanded: true })
    expect(container.querySelector('.message-drawer-resize-handle')).toBeNull()
    const wrap = container.querySelector('.message-drawer-stream-wrap') as HTMLElement
    expect(wrap.classList.contains('resizable')).toBe(false)
    expect(wrap.style.maxHeight).toBe('')
    unmount()
  })

  it('omits resize handle when collapsed even if resizable', () => {
    const envs = [makeUserEnvelope('Hello')]
    const { container, unmount } = renderDrawer({ envelopes: envs, expanded: false, resizable: true })
    expect(container.querySelector('.message-drawer-resize-handle')).toBeNull()
    unmount()
  })

  // ── Collapsed peek interaction bar ──

  it('shows interaction bar with option chips for a pending ask_user when collapsed', () => {
    const dispatch = vi.fn().mockResolvedValue(undefined)
    const envs = [makeAssistantEnvelope([makeAskFrame()], false)]
    const { container, unmount } = renderDrawer({ envelopes: envs, dispatch })
    const bar = container.querySelector('.peek-interaction-bar')
    expect(bar).toBeTruthy()
    // Normal single-line peek is replaced by the interaction bar
    expect(container.querySelector('.message-drawer-peek')).toBeNull()
    expect(bar!.querySelector('.peek-interaction-title')?.textContent).toContain('Approach')
    const chips = bar!.querySelectorAll('.peek-interaction-chip')
    expect(chips).toHaveLength(2)
    act(() => { (chips[0] as HTMLButtonElement).click() })
    expect(dispatch).toHaveBeenCalledWith({ kind: 'ai.ask_answered', requestId: 'req-ask', answers: { 0: 'Option A' } })
    unmount()
  })

  it('answers a pending permission_request inline when collapsed', () => {
    const dispatch = vi.fn().mockResolvedValue(undefined)
    const envs = [makeAssistantEnvelope([makePermissionFrame()], false)]
    const { container, unmount } = renderDrawer({ envelopes: envs, dispatch })
    const bar = container.querySelector('.peek-interaction-bar')
    expect(bar).toBeTruthy()
    expect(bar!.querySelector('.peek-interaction-title')?.textContent).toContain('project.write')
    const buttons = Array.from(bar!.querySelectorAll('.peek-interaction-btn')) as HTMLButtonElement[]
    const allow = buttons.find(b => b.textContent === 'ai.permission.allow')!
    const deny = buttons.find(b => b.textContent === 'ai.permission.deny')!
    expect(allow).toBeTruthy()
    expect(deny).toBeTruthy()
    act(() => { allow.click() })
    expect(dispatch).toHaveBeenCalledWith({ kind: 'ai.permission_answered', requestId: 'req-perm', allowed: true, allowInProject: undefined })
    unmount()
  })

  it('falls back to the normal peek line when the interaction is resolved', () => {
    const envs = [makeAssistantEnvelope([
      makeAskFrame({ status: 'completed', answers: { 0: 'Option A' } }),
      makeTextFrame('Continuing'),
    ])]
    const { container, unmount } = renderDrawer({ envelopes: envs })
    expect(container.querySelector('.peek-interaction-bar')).toBeNull()
    expect(container.querySelector('.message-drawer-peek')).toBeTruthy()
    unmount()
  })

  it('shows an expand affordance instead of chips for multi-question ask_user', () => {
    const onToggle = vi.fn()
    const envs = [makeAssistantEnvelope([makeAskFrame({
      questions: [
        { header: 'Q1', question: 'First?', options: [{ label: 'A' }], multiSelect: false },
        { header: 'Q2', question: 'Second?', options: [{ label: 'B' }], multiSelect: false },
      ],
    })], false)]
    const { container, unmount } = renderDrawer({ envelopes: envs, onToggle })
    const bar = container.querySelector('.peek-interaction-bar')!
    expect(bar).toBeTruthy()
    expect(bar.querySelectorAll('.peek-interaction-chip')).toHaveLength(0)
    expect(bar.querySelector('.peek-interaction-meta')?.textContent).toContain('1 / 2')
    act(() => { (bar.querySelector('.peek-interaction-header') as HTMLElement).click() })
    expect(onToggle).toHaveBeenCalledTimes(1)
    unmount()
  })

  it('does not render the interaction bar when expanded', () => {
    const envs = [makeAssistantEnvelope([makeAskFrame()], false)]
    const { container, unmount } = renderDrawer({ envelopes: envs, expanded: true })
    expect(container.querySelector('.peek-interaction-bar')).toBeNull()
    unmount()
  })

  it('approves a pending goal_submit inline when collapsed', () => {
    const dispatch = vi.fn().mockResolvedValue(undefined)
    const goalFrame: GoalSubmitFrame = {
      id: 'goal-1',
      type: 'goal_submit',
      status: 'running',
      condition: 'ship it',
      interpretedGoal: 'Ship the feature',
      requestId: 'req-goal',
      approvalStatus: 'pending',
    }
    const envs = [makeAssistantEnvelope([goalFrame], false)]
    const { container, unmount } = renderDrawer({ envelopes: envs, dispatch })
    const bar = container.querySelector('.peek-interaction-bar')
    expect(bar).toBeTruthy()
    expect(bar!.querySelector('.peek-interaction-title')?.textContent).toContain('Ship the feature')
    const buttons = Array.from(bar!.querySelectorAll('.peek-interaction-btn')) as HTMLButtonElement[]
    // i18n is mocked to return keys
    const approve = buttons.find(b => b.textContent === 'ai.goalSubmit.confirm')!
    act(() => { approve.click() })
    expect(dispatch).toHaveBeenCalledWith({ kind: 'ai.goal_submit_answered', requestId: 'req-goal', decision: 'approve', feedback: undefined })
    unmount()
  })
})
