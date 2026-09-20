import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AgentReviewToolView, parseAgentReview, resolveEffectiveApproved, isFeedbackOverflow } from './AgentReviewToolView'
import type { ToolFrame } from '../../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// Identity translator: returns the key so assertions verify the component
// wires the correct i18n keys (mirrors the GoalReviewView test pattern).
vi.mock('../../../../i18n/provider', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('AgentReviewToolView', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderView = async (frame: ToolFrame) => {
    await act(async () => {
      root.render(<AgentReviewToolView frame={frame} />)
    })
  }

  const baseFrame = (overrides: Partial<ToolFrame> = {}): ToolFrame => ({
    id: 'ar1',
    type: 'tool',
    toolName: 'workspace.agent_review',
    status: 'completed',
    input: '',
    ...overrides,
  })

  it('renders approved result with badge, target agent and task card', async () => {
    const frame = baseFrame({
      input: JSON.stringify({
        AgentActorId: 'agent-worker-1',
        TaskCardId: '实现登录',
        Decision: 'approve',
      }),
      output: JSON.stringify({ Approved: true, CardStatus: 'done' }),
    })
    await renderView(frame)

    expect(container.querySelector('.ai-agent-review-badge--approved')).not.toBeNull()
    expect(container.textContent).toContain('ai.tool.agentReview.approved')
    expect(container.textContent).toContain('agent-worker-1')
    expect(container.textContent).toContain('实现登录')
    expect(container.textContent).toContain('done')
    // Approved → no feedback section.
    expect(container.querySelector('.ai-agent-review-feedback')).toBeNull()
  })

  it('renders rejected result with badge and feedback summary', async () => {
    const frame = baseFrame({
      input: JSON.stringify({
        AgentActorId: 'agent-worker-2',
        Decision: 'reject',
        Feedback: 'Tests are missing for the new handler.',
      }),
      output: JSON.stringify({ Approved: false, CardStatus: 'doing' }),
    })
    await renderView(frame)

    expect(container.querySelector('.ai-agent-review-badge--rejected')).not.toBeNull()
    expect(container.textContent).toContain('ai.tool.agentReview.rejected')
    expect(container.textContent).toContain('Tests are missing for the new handler.')
    expect(container.textContent).toContain('doing')
  })

  it('truncates long feedback with an expand toggle and reveals full text', async () => {
    const longFeedback = Array.from({ length: 12 }, (_, i) => `line ${i + 1}`).join('\n')
    const frame = baseFrame({
      input: JSON.stringify({
        AgentActorId: 'agent-worker-3',
        Decision: 'reject',
        Feedback: longFeedback,
      }),
      output: JSON.stringify({ Approved: false, CardStatus: 'doing' }),
    })
    await renderView(frame)

    // Collapsed by default.
    const text = container.querySelector('.ai-agent-review-feedback-text')
    expect(text?.classList.contains('ai-agent-review-feedback-collapsed')).toBe(true)
    const toggle = container.querySelector('.ai-tool-code-expander') as HTMLButtonElement
    expect(toggle).not.toBeNull()
    expect(toggle.getAttribute('aria-expanded')).toBe('false')

    // Expand.
    await act(async () => {
      toggle.click()
    })
    const textAfter = container.querySelector('.ai-agent-review-feedback-text')
    expect(textAfter?.classList.contains('ai-agent-review-feedback-collapsed')).toBe(false)
    const toggleAfter = container.querySelector('.ai-tool-code-expander') as HTMLButtonElement
    expect(toggleAfter.getAttribute('aria-expanded')).toBe('true')
    expect(toggleAfter.textContent).toContain('ai.tool.agentReview.collapse')
  })

  it('renders reviewing spinner while running and surfaces the target agent', async () => {
    const frame = baseFrame({
      status: 'running',
      input: JSON.stringify({ AgentActorId: 'agent-worker-4', Decision: 'approve' }),
    })
    await renderView(frame)

    expect(container.querySelector('.ai-agent-review--running')).not.toBeNull()
    expect(container.textContent).toContain('ai.tool.agentReview.reviewing')
    expect(container.textContent).toContain('agent-worker-4')
    // No result badge while running.
    expect(container.querySelector('.ai-agent-review-badge')).toBeNull()
  })

  it('renders failure indicator and raw error output on tool error', async () => {
    const frame = baseFrame({
      status: 'error',
      input: JSON.stringify({ AgentActorId: 'agent-worker-5', Decision: 'approve' }),
      output: 'workspace.agent.review: agent "agent-worker-5" not found',
    })
    await renderView(frame)

    expect(container.querySelector('.ai-agent-review--failed')).not.toBeNull()
    expect(container.textContent).toContain('ai.tool.agentReview.failed')
    const err = container.querySelector('.ai-tool-code.error')
    expect(err?.textContent).toContain('not found')
  })

  it('renders unknown badge for old history with missing decision and no result', async () => {
    const frame = baseFrame({
      input: JSON.stringify({ AgentActorId: 'agent-worker-6' }),
      // Old persisted frame: no structured output, no Decision field.
      output: '',
    })
    await renderView(frame)

    expect(container.querySelector('.ai-agent-review-badge--unknown')).not.toBeNull()
    expect(container.textContent).toContain('agent-worker-6')
    // No feedback when approval is unknown.
    expect(container.querySelector('.ai-agent-review-feedback')).toBeNull()
  })

  it('infers approval from requested Decision when result output is unparseable', async () => {
    const frame = baseFrame({
      input: JSON.stringify({ AgentActorId: 'a7', Decision: 'reject' }),
      output: 'ok',
    })
    await renderView(frame)
    expect(container.querySelector('.ai-agent-review-badge--rejected')).not.toBeNull()
  })

  // ── Hooks-stability: status transitions on the same mounted component ──
  // These verify React.useState is called unconditionally (before early
  // returns) so the hook count is stable across running → completed/error.

  it('rerenders running → completed approve without a hooks violation', async () => {
    const input = JSON.stringify({ AgentActorId: 'agent-rt', Decision: 'approve' })
    // First render: running (early return, no badge).
    await renderView(baseFrame({ status: 'running', input }))
    expect(container.querySelector('.ai-agent-review--running')).not.toBeNull()
    expect(container.querySelector('.ai-agent-review-badge')).toBeNull()

    // Second render: completed approve (uses the useState hook for feedback).
    await renderView(baseFrame({
      status: 'completed',
      input,
      output: JSON.stringify({ Approved: true, CardStatus: 'done' }),
    }))
    expect(container.querySelector('.ai-agent-review-badge--approved')).not.toBeNull()
    expect(container.textContent).toContain('done')
  })

  it('rerenders running → completed reject without a hooks violation', async () => {
    const input = JSON.stringify({
      AgentActorId: 'agent-rt2',
      Decision: 'reject',
      Feedback: Array.from({ length: 10 }, (_, i) => `line ${i + 1}`).join('\n'),
    })
    await renderView(baseFrame({ status: 'running', input }))
    expect(container.querySelector('.ai-agent-review--running')).not.toBeNull()

    await renderView(baseFrame({
      status: 'completed',
      input,
      output: JSON.stringify({ Approved: false, CardStatus: 'doing' }),
    }))
    expect(container.querySelector('.ai-agent-review-badge--rejected')).not.toBeNull()
    expect(container.querySelector('.ai-agent-review-feedback')).not.toBeNull()
    // Toggle is present (feedback overflows by line count).
    expect(container.querySelector('.ai-tool-code-expander')).not.toBeNull()
  })

  it('rerenders running → error without a hooks violation', async () => {
    const input = JSON.stringify({ AgentActorId: 'agent-rt3', Decision: 'approve' })
    await renderView(baseFrame({ status: 'running', input }))
    expect(container.querySelector('.ai-agent-review--running')).not.toBeNull()

    await renderView(baseFrame({
      status: 'error',
      input,
      output: 'agent "agent-rt3" not found',
    }))
    expect(container.querySelector('.ai-agent-review--failed')).not.toBeNull()
    expect(container.textContent).toContain('ai.tool.agentReview.failed')
  })

  it('rerenders completed → error → completed without a hooks violation', async () => {
    const input = JSON.stringify({ AgentActorId: 'agent-rt4', Decision: 'approve' })
    // completed
    await renderView(baseFrame({
      status: 'completed',
      input,
      output: JSON.stringify({ Approved: true, CardStatus: 'done' }),
    }))
    expect(container.querySelector('.ai-agent-review-badge--approved')).not.toBeNull()

    // error (different hook path)
    await renderView(baseFrame({ status: 'error', input, output: 'boom' }))
    expect(container.querySelector('.ai-agent-review--failed')).not.toBeNull()

    // back to completed
    await renderView(baseFrame({
      status: 'completed',
      input,
      output: JSON.stringify({ Approved: true, CardStatus: 'done' }),
    }))
    expect(container.querySelector('.ai-agent-review-badge--approved')).not.toBeNull()
  })

  it('shows expand toggle for a single long-line feedback that has no newlines', async () => {
    const longSingleLine = 'A'.repeat(300)
    const frame = baseFrame({
      input: JSON.stringify({
        AgentActorId: 'agent-overflow',
        Decision: 'reject',
        Feedback: longSingleLine,
      }),
      output: JSON.stringify({ Approved: false, CardStatus: 'doing' }),
    })
    await renderView(frame)

    // Even though there are no newlines, the char threshold triggers overflow.
    const text = container.querySelector('.ai-agent-review-feedback-text')
    expect(text?.classList.contains('ai-agent-review-feedback-collapsed')).toBe(true)
    const toggle = container.querySelector('.ai-tool-code-expander') as HTMLButtonElement
    expect(toggle).not.toBeNull()

    // Expand and verify full text is visible.
    await act(async () => {
      toggle.click()
    })
    const textAfter = container.querySelector('.ai-agent-review-feedback-text')
    expect(textAfter?.classList.contains('ai-agent-review-feedback-collapsed')).toBe(false)
  })
})

// ── Pure conversion helpers ──

describe('parseAgentReview', () => {
  it('reads input fields and result facts', () => {
    const data = parseAgentReview(
      JSON.stringify({ AgentActorId: 'a1', TaskCardId: '卡片', Decision: 'reject', Feedback: 'fix it' }),
      JSON.stringify({ Approved: false, CardStatus: 'doing' }),
    )
    expect(data).toEqual({
      agentActorId: 'a1',
      taskCardId: '卡片',
      decision: 'reject',
      feedback: 'fix it',
      approved: false,
      cardStatus: 'doing',
    })
  })

  it('returns undefined for missing/empty input and output', () => {
    expect(parseAgentReview('', '')).toEqual({
      agentActorId: undefined,
      taskCardId: undefined,
      decision: undefined,
      feedback: undefined,
      approved: undefined,
      cardStatus: undefined,
    })
  })

  it('handles unparseable output gracefully', () => {
    const data = parseAgentReview(JSON.stringify({ AgentActorId: 'a2', Decision: 'approve' }), 'not json')
    expect(data.approved).toBeUndefined()
    expect(data.cardStatus).toBeUndefined()
    expect(data.decision).toBe('approve')
  })
})

describe('resolveEffectiveApproved', () => {
  it('prefers the authoritative result Approved flag', () => {
    expect(resolveEffectiveApproved({ approved: true }, 'completed')).toBe(true)
    expect(resolveEffectiveApproved({ approved: false, decision: 'approve' }, 'completed')).toBe(false)
  })

  it('infers from requested decision when completed but no result', () => {
    expect(resolveEffectiveApproved({ decision: 'approve' }, 'completed')).toBe(true)
    expect(resolveEffectiveApproved({ decision: 'reject' }, 'completed')).toBe(false)
  })

  it('returns undefined while running or when decision is unknown', () => {
    expect(resolveEffectiveApproved({ decision: 'approve' }, 'running')).toBeUndefined()
    expect(resolveEffectiveApproved({}, 'completed')).toBeUndefined()
  })
})

describe('isFeedbackOverflow', () => {
  it('returns false for short feedback within both limits', () => {
    expect(isFeedbackOverflow('short text')).toBe(false)
  })

  it('returns false for undefined or empty feedback', () => {
    expect(isFeedbackOverflow(undefined)).toBe(false)
    expect(isFeedbackOverflow('')).toBe(false)
  })

  it('returns true for multi-line feedback exceeding the line threshold', () => {
    const multiline = Array.from({ length: 5 }, () => 'x').join('\n')
    expect(isFeedbackOverflow(multiline)).toBe(true)
  })

  it('returns true for a single long line exceeding the char threshold', () => {
    expect(isFeedbackOverflow('A'.repeat(201))).toBe(true)
    // exactly at the threshold → not overflow
    expect(isFeedbackOverflow('A'.repeat(200))).toBe(false)
  })
})
