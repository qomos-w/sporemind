import { describe, it, expect, vi } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { CompactMessageStream } from './CompactMessageStream'
import { InteractionDispatchContext } from '../hooks/useStepInteraction'
import type { TurnEnvelope, ToolFrame, TextFrame, PlanFrame, AskUserQuestionFrame, PermissionRequestFrame, GoalSubmitFrame } from '../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// Mock useI18n
vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

// ── Test helpers ──

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
  return {
    id,
    type: 'text',
    status: 'completed',
    content,
  }
}

function makeUserEnvelope(content: string, id = 'env-user-1'): TurnEnvelope {
  return {
    id,
    role: 'user',
    userContent: content,
    frames: [],
    timestamp: new Date().toISOString(),
  }
}

function makeAssistantEnvelope(frames: TurnEnvelope['frames'], id = 'env-asst-1'): TurnEnvelope {
  return {
    id,
    role: 'assistant',
    frames,
    timestamp: new Date().toISOString(),
    completed: true,
  }
}

function renderToDOM(envelopes: TurnEnvelope[], opts?: { onFrameSelect?: (f: any) => void; maxEnvelopes?: number; dispatch?: (event: any) => Promise<void> }) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  let root: Root
  const stream = (
    <CompactMessageStream
      envelopes={envelopes}
      onFrameSelect={opts?.onFrameSelect}
      maxEnvelopes={opts?.maxEnvelopes}
    />
  )
  act(() => {
    root = createRoot(container)
    root.render(
      opts?.dispatch
        ? <InteractionDispatchContext.Provider value={opts.dispatch}>{stream}</InteractionDispatchContext.Provider>
        : stream
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

function makeGoalSubmitFrame(overrides: Partial<GoalSubmitFrame> = {}): GoalSubmitFrame {
  return {
    id: 'goal-1',
    type: 'goal_submit',
    status: 'running',
    condition: 'ship it',
    interpretedGoal: 'Ship the feature',
    requestId: 'req-goal',
    approvalStatus: 'pending',
    ...overrides,
  }
}

// ── Tests ──

describe('CompactMessageStream', () => {
  it('renders empty state when no envelopes', () => {
    const { container, unmount } = renderToDOM([])
    expect(container.textContent).toContain('No messages yet')
    unmount()
  })

  it('renders user message as right-aligned bubble', () => {
    const { container, unmount } = renderToDOM([makeUserEnvelope('Hello world')])
    const bubble = container.querySelector('.compact-user-bubble')
    expect(bubble).toBeTruthy()
    expect(bubble!.textContent).toBe('Hello world')
    unmount()
  })

  it('renders a system continuation as a compact system bubble instead of a user bubble', () => {
    const env: TurnEnvelope = {
      id: 'env-goal',
      role: 'user',
      userContent: "We'll continue working toward the active goal. (turn 3 of 10)",
      frames: [],
      timestamp: new Date().toISOString(),
      systemOrigin: 'goal',
    }
    const { container, unmount } = renderToDOM([env])
    expect(container.querySelector('.compact-system-continuation-row')).toBeTruthy()
    expect(container.querySelector('.ai-system-continuation-goal')).toBeTruthy()
    expect(container.querySelector('.compact-user-bubble')).toBeNull()
    // Goal bubbles show no summary text (name only when the goal is attached).
    expect(container.querySelector('.ai-system-continuation-summary')).toBeNull()
    unmount()
  })

  it('renders the goal name on a goal continuation in compact', () => {
    const env: TurnEnvelope = {
      id: 'env-goal-named',
      role: 'user',
      userContent: "We'll continue working toward the active goal. (turn 4 of 10)",
      frames: [],
      timestamp: new Date().toISOString(),
      systemOrigin: 'goal',
      goal: { Condition: '构建登录页', InterpretedGoal: '实现带会话保持的登录页面', MaxTurns: 10, TurnCount: 4 },
    }
    const { container, unmount } = renderToDOM([env])
    expect(container.querySelector('.ai-system-continuation-summary')!.textContent).toBe('实现带会话保持的登录页面')
    unmount()
  })

  it('renders a workflow continuation with extracted frontier summary in compact', () => {
    const env: TurnEnvelope = {
      id: 'env-wf',
      role: 'user',
      userContent: 'Map [[m1]] has 5 frontier tickets ready:\n\n- [[c1]]\n\nCheck: ...',
      frames: [],
      timestamp: new Date().toISOString(),
      systemOrigin: 'workflow',
    }
    const { container, unmount } = renderToDOM([env])
    expect(container.querySelector('.ai-system-continuation-workflow')).toBeTruthy()
    expect(container.querySelector('.ai-system-continuation-summary')!.textContent).toBe('5 frontier tickets ready')
    unmount()
  })

  it('expands a compact system bubble to show full text', () => {
    const full = 'No frontier tasks or active workers remain. Assess whether...'
    const env: TurnEnvelope = {
      id: 'env-wf2',
      role: 'user',
      userContent: full,
      frames: [],
      timestamp: new Date().toISOString(),
      systemOrigin: 'workflow',
    }
    const { container, unmount } = renderToDOM([env])
    expect(container.querySelector('.ai-system-continuation-body')).toBeNull()
    act(() => {
      (container.querySelector('.ai-system-continuation-row') as HTMLElement)!.click()
    })
    const body = container.querySelector('.ai-system-continuation-body')
    expect(body).toBeTruthy()
    expect(body!.textContent).toContain(full)
    unmount()
  })

  it('keeps non-system user envelopes on the compact user bubble path', () => {
    const { container, unmount } = renderToDOM([makeUserEnvelope('plain user')])
    expect(container.querySelector('.compact-user-bubble')).toBeTruthy()
    expect(container.querySelector('.ai-system-continuation')).toBeNull()
    unmount()
  })

  it('renders assistant text as left-aligned bubble', () => {
    const { container, unmount } = renderToDOM([
      makeAssistantEnvelope([makeTextFrame('I can help with that')]),
    ])
    const bubble = container.querySelector('.compact-text-bubble')
    expect(bubble).toBeTruthy()
    expect(bubble!.textContent).toBe('I can help with that')
    unmount()
  })

  it('renders tool call with title-only (no expandable body)', () => {
    const { container, unmount } = renderToDOM([
      makeAssistantEnvelope([makeToolFrame({
        id: 'tool-test',
        toolName: 'file_read',
        input: JSON.stringify({ file_path: 'src/app.ts' }),
      })]),
    ])
    const toolBubble = container.querySelector('.compact-tool-bubble')
    expect(toolBubble).toBeTruthy()
    // Subtitle should show file path
    const subtitle = toolBubble!.querySelector('.compact-tool-subtitle')
    expect(subtitle?.textContent).toBe('src/app.ts')
    // No expandable body content
    expect(toolBubble?.querySelector('.compact-tool-body')).toBeNull()
    unmount()
  })

  it('renders running tool with spinner', () => {
    const { container, unmount } = renderToDOM([
      makeAssistantEnvelope([makeToolFrame({
        id: 'tool-running',
        toolName: 'shell_exec',
        input: JSON.stringify({ command: 'npm test' }),
        status: 'running',
      })]),
    ])
    const spinner = container.querySelector('.compact-tool-spinner')
    expect(spinner).toBeTruthy()
    unmount()
  })

  it('renders completed tool with check icon', () => {
    const { container, unmount } = renderToDOM([
      makeAssistantEnvelope([makeToolFrame({ id: 'tool-done', status: 'completed' })]),
    ])
    const check = container.querySelector('.compact-tool-check')
    expect(check).toBeTruthy()
    unmount()
  })

  it('shows duration for completed tools', () => {
    const { container, unmount } = renderToDOM([
      makeAssistantEnvelope([makeToolFrame({ id: 'tool-dur', durationSeconds: 5, status: 'completed' })]),
    ])
    const durationEl = container.querySelector('.compact-tool-duration')
    expect(durationEl?.textContent).toBe('5s')
    unmount()
  })

  it('shows +/- stats for edit tools with structured output', () => {
    const { container, unmount } = renderToDOM([
      makeAssistantEnvelope([makeToolFrame({
        id: 'tool-edit',
        toolName: 'file_edit',
        input: JSON.stringify({ file_path: 'src/app.ts' }),
        output: JSON.stringify({ Replacements: 1, Additions: 3, Deletions: 6, Hunks: [] }),
        status: 'completed',
      })]),
    ])
    const stats = container.querySelector('.compact-tool-stats')
    expect(stats).toBeTruthy()
    const add = stats!.querySelector('.compact-tool-stat.add')
    const del = stats!.querySelector('.compact-tool-stat.del')
    expect(add?.textContent).toBe('+3')
    expect(del?.textContent).toBe('−6')
    unmount()
  })

  it('falls back to fileChanges for edit stats when output has none', () => {
    const { container, unmount } = renderToDOM([
      makeAssistantEnvelope([makeToolFrame({
        id: 'tool-edit-fc',
        toolName: 'project_edit',
        input: JSON.stringify({ file_path: 'src/app.ts' }),
        output: JSON.stringify({ Replacements: 1, Hunks: [] }),
        fileChanges: [{ filename: 'app.ts', filepath: 'src/app.ts', additions: 5, deletions: 0 }],
        status: 'completed',
      })]),
    ])
    const stats = container.querySelector('.compact-tool-stats')
    expect(stats).toBeTruthy()
    expect(stats!.querySelector('.compact-tool-stat.add')?.textContent).toBe('+5')
    // deletions=0 should not render a badge
    expect(stats!.querySelector('.compact-tool-stat.del')).toBeNull()
    unmount()
  })

  it('shows no stats for non-edit tools', () => {
    const { container, unmount } = renderToDOM([
      makeAssistantEnvelope([makeToolFrame({
        id: 'tool-read',
        toolName: 'file_read',
        input: JSON.stringify({ file_path: 'src/app.ts' }),
        output: JSON.stringify({ Additions: 3, Deletions: 1 }),
        status: 'completed',
      })]),
    ])
    expect(container.querySelector('.compact-tool-stats')).toBeNull()
    unmount()
  })

  it('calls onFrameSelect when a tool bubble is clicked', () => {
    const onFrameSelect = vi.fn()
    const frame = makeToolFrame({ id: 'tool-click', toolName: 'file_read' })
    const { container, unmount } = renderToDOM(
      [makeAssistantEnvelope([frame])],
      { onFrameSelect }
    )
    const bubble = container.querySelector('.compact-tool-bubble') as HTMLElement
    expect(bubble.getAttribute('role')).toBe('button')
    act(() => { bubble.click() })
    expect(onFrameSelect).toHaveBeenCalledTimes(1)
    expect(onFrameSelect).toHaveBeenCalledWith(frame)
    unmount()
  })

  it('filters out empty text frames', () => {
    const { container, unmount } = renderToDOM([
      makeAssistantEnvelope([
        makeTextFrame('   ', 'empty-text'),
        makeTextFrame('visible', 'real-text'),
      ]),
    ])
    const bubbles = container.querySelectorAll('.compact-text-bubble')
    expect(bubbles).toHaveLength(1)
    expect(bubbles[0]!.textContent).toBe('visible')
    unmount()
  })

  it('renders mixed user + assistant conversation', () => {
    const { container, unmount } = renderToDOM([
      makeUserEnvelope('Fix the bug'),
      makeAssistantEnvelope([
        makeTextFrame('Looking into it', 't1'),
        makeToolFrame({ id: 'tool-mixed', toolName: 'file_read', input: '{}' }),
        makeTextFrame('Found the issue', 't2'),
      ]),
    ])
    expect(container.querySelector('.compact-user-bubble')).toBeTruthy()
    expect(container.querySelectorAll('.compact-text-bubble').length).toBe(2)
    expect(container.querySelector('.compact-tool-bubble')).toBeTruthy()
    unmount()
  })

  it('calls onFrameSelect when tool bubble is clicked', () => {
    const onFrameSelect = vi.fn()
    const toolFrame = makeToolFrame({ id: 'tool-click' })
    const { container, unmount } = renderToDOM(
      [makeAssistantEnvelope([toolFrame])],
      { onFrameSelect }
    )
    const toolBubble = container.querySelector('.compact-tool-bubble') as HTMLElement
    expect(toolBubble.getAttribute('role')).toBe('button')
    act(() => {
      toolBubble.click()
    })
    expect(onFrameSelect).toHaveBeenCalledWith(toolFrame)
    unmount()
  })

  it('limits to maxEnvelopes most recent', () => {
    const envs: TurnEnvelope[] = []
    for (let i = 0; i < 10; i++) {
      envs.push(makeUserEnvelope(`Message ${i}`, `env-${i}`))
    }
    const { container, unmount } = renderToDOM(envs, { maxEnvelopes: 3 })
    const bubbles = container.querySelectorAll('.compact-user-bubble')
    expect(bubbles.length).toBe(3)
    expect(bubbles[2]!.textContent).toBe('Message 9')
    expect(bubbles[0]!.textContent).toBe('Message 7')
    unmount()
  })

  // ── Interactive frames ──

  it('renders a pending ask_user as the interactive question block', () => {
    const { container, unmount } = renderToDOM([makeAssistantEnvelope([makeAskFrame()])])
    const form = container.querySelector('.ai-ask-form')
    expect(form).toBeTruthy()
    expect(form!.textContent).toContain('Which approach?')
    expect(form!.querySelectorAll('.ai-ask-option')).toHaveLength(2)
    unmount()
  })

  it('answers ask_user through the interaction dispatch context', () => {
    const dispatch = vi.fn().mockResolvedValue(undefined)
    const { container, unmount } = renderToDOM([makeAssistantEnvelope([makeAskFrame()])], { dispatch })
    const options = container.querySelectorAll('.ai-ask-option')
    act(() => { (options[0] as HTMLButtonElement).click() })
    expect(dispatch).toHaveBeenCalledWith({ kind: 'ai.ask_answered', requestId: 'req-ask', answers: { 0: 'Option A' } })
    unmount()
  })

  it('renders an answered ask_user without the form', () => {
    const { container, unmount } = renderToDOM([makeAssistantEnvelope([
      makeAskFrame({ status: 'completed', answers: { 0: 'Option A' } }),
    ])])
    expect(container.querySelector('.ai-ask-form')).toBeNull()
    expect(container.textContent).toContain('ai.step.answered')
    unmount()
  })

  it('renders a pending permission_request with allow/deny actions', () => {
    const { container, unmount } = renderToDOM([makeAssistantEnvelope([makePermissionFrame()])])
    const actions = container.querySelector('.ai-perm-actions')
    expect(actions).toBeTruthy()
    expect(actions!.textContent).toContain('ai.permission.allow')
    expect(actions!.textContent).toContain('ai.permission.deny')
    unmount()
  })

  it('renders a pending goal_submit with approval actions', () => {
    const { container, unmount } = renderToDOM([makeAssistantEnvelope([makeGoalSubmitFrame()])])
    const actions = container.querySelector('.ai-goal-submit-actions')
    expect(actions).toBeTruthy()
    unmount()
  })

  it('does not render auto-approved permission frames', () => {
    const { container, unmount } = renderToDOM([makeAssistantEnvelope([
      makePermissionFrame({ allowed: true, status: 'completed' }),
    ])])
    expect(container.querySelector('.ai-perm-actions')).toBeNull()
    unmount()
  })

  it('renders a pending plan as PlanBlock with approval actions', () => {
    const planFrame: PlanFrame = {
      id: 'plan-1',
      type: 'plan',
      status: 'running',
      content: '## Ship it\n1. do the thing',
      requestId: 'req-plan',
      approvalStatus: 'pending',
    }
    const dispatch = vi.fn().mockResolvedValue(undefined)
    const { container, unmount } = renderToDOM([makeAssistantEnvelope([planFrame])], { dispatch })
    expect(container.querySelector('.ai-plan-approval-actions')).toBeTruthy()
    unmount()
  })

  it('restores plan edit draft after unmount/remount (input-loss bug)', () => {
    const planFrame: PlanFrame = {
      id: 'plan-draft',
      type: 'plan',
      status: 'running',
      content: '## Original',
      requestId: 'req-plan-draft',
      approvalStatus: 'pending',
      editable: true,
    }
    const dispatch = vi.fn().mockResolvedValue(undefined)
    const first = renderToDOM([makeAssistantEnvelope([planFrame])], { dispatch })
    const editBtn = first.container.querySelector<HTMLButtonElement>('button[title="Edit plan"]')
    expect(editBtn).toBeTruthy()
    act(() => { editBtn!.click() })
    const editor = first.container.querySelector<HTMLTextAreaElement>('.ai-plan-editor')
    expect(editor).toBeTruthy()
    act(() => {
      const setNative = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
      setNative.call(editor!, '## Revised by user')
      editor!.dispatchEvent(new Event('input', { bubbles: true }))
    })
    first.unmount()

    const second = renderToDOM([makeAssistantEnvelope([planFrame])], { dispatch })
    const editor2 = second.container.querySelector<HTMLTextAreaElement>('.ai-plan-editor')
    // editing mode and the typed draft both survive the remount
    expect(editor2).toBeTruthy()
    expect(editor2!.value).toBe('## Revised by user')
    second.unmount()
  })

  it('renders an approved plan as a compact text bubble', () => {
    const planFrame: PlanFrame = {
      id: 'plan-2',
      type: 'plan',
      status: 'completed',
      content: '## Done plan',
      approvalStatus: 'approved',
    }
    const { container, unmount } = renderToDOM([makeAssistantEnvelope([planFrame])])
    expect(container.querySelector('.ai-plan-approval-actions')).toBeNull()
    expect(container.textContent).toContain('📋')
    unmount()
  })
})

// ── Auto-scroll gating ──
// The projection rebuilds envelope objects on every session notify (identical
// content, new references) — e.g. every transport reconnect's summary
// reconcile. The idle stream must not scroll on that churn; only a real
// content change or streaming may move the viewport.

describe('CompactMessageStream auto-scroll', () => {
  const SCROLL_HEIGHT = 600

  function renderScrollHarness(initialEnvelopes: TurnEnvelope[], initialStreaming = false) {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const render = (envelopes: TurnEnvelope[], isStreaming: boolean) => {
      act(() => {
        root.render(<CompactMessageStream envelopes={envelopes} isStreaming={isStreaming} />)
      })
    }
    render(initialEnvelopes, initialStreaming)
    const streamEl = container.querySelector('.compact-message-stream') as HTMLElement
    Object.defineProperty(streamEl, 'scrollHeight', { configurable: true, value: SCROLL_HEIGHT })
    return {
      streamEl,
      render,
      unmount: () => { root.unmount(); container.remove() },
    }
  }

  it('does not scroll on envelope reference churn while idle (reconnect reconcile)', () => {
    const env = makeUserEnvelope('Hello', 'env-1')
    const h = renderScrollHarness([env])
    h.streamEl.scrollTop = 120 // user scrolled up to read
    h.render([{ ...env }], false) // same content, new object references
    expect(h.streamEl.scrollTop).toBe(120)
    h.unmount()
  })

  it('scrolls to bottom when a new envelope arrives while idle', () => {
    const env = makeUserEnvelope('Hello', 'env-1')
    const h = renderScrollHarness([env])
    h.streamEl.scrollTop = 120
    h.render([{ ...env }, makeUserEnvelope('World', 'env-2')], false)
    expect(h.streamEl.scrollTop).toBe(SCROLL_HEIGHT)
    h.unmount()
  })

  it('keeps following the bottom while streaming, including reference churn', () => {
    const env = makeAssistantEnvelope([makeTextFrame('partial')], 'env-a')
    const h = renderScrollHarness([env], true)
    h.streamEl.scrollTop = 120
    h.render([{ ...env, frames: [...env.frames] }], true)
    expect(h.streamEl.scrollTop).toBe(SCROLL_HEIGHT)
    h.unmount()
  })

  it('jumps to bottom when streaming starts, but not when it ends', () => {
    const env = makeAssistantEnvelope([makeTextFrame('done')], 'env-a')
    const h = renderScrollHarness([env], false)
    h.streamEl.scrollTop = 120
    h.render([env], true)
    expect(h.streamEl.scrollTop).toBe(SCROLL_HEIGHT)

    h.streamEl.scrollTop = 120
    h.render([env], false)
    expect(h.streamEl.scrollTop).toBe(120)
    h.unmount()
  })
})
