import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { GoalCardSubmitView } from './GoalCardSubmitView'
import type { GoalCardSubmitFrame } from '../../model/frame-types'

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  submitGoalCardApproval: vi.fn(),
}))

vi.mock('../../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../hooks/useStepInteraction', () => ({
  useStepInteraction: () => ({
    submitGoalCardApproval: hoisted.submitGoalCardApproval,
    submitting: false,
  }),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('GoalCardSubmitView', () => {
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

  const renderView = async (frame: GoalCardSubmitFrame) => {
    await act(async () => {
      root.render(<GoalCardSubmitView frame={frame} />)
    })
  }

  it('renders the card id and interpreted goal', async () => {
    const frame: GoalCardSubmitFrame = {
      id: 'goal-card-submit-1',
      type: 'goal_card_submit',
      status: 'pending',
      approvalStatus: 'pending',
      requestId: 'req-1',
      cardId: 'Fix the assign dialog',
      interpretedGoal: 'Ship the assign flow',
    }
    await renderView(frame)

    const rows = container.querySelectorAll('.ai-goal-submit-row')
    expect(rows.length).toBe(2)
    expect(rows[0]!.textContent).toContain('ai.goalCardSubmit.cardId')
    expect(rows[0]!.textContent).toContain('Fix the assign dialog')
    expect(rows[1]!.textContent).toContain('ai.goalCardSubmit.interpreted')
  })

  it('renders title as markdown with wikiword links', async () => {
    const frame: GoalCardSubmitFrame = {
      id: 'goal-card-submit-2',
      type: 'goal_card_submit',
      status: 'pending',
      approvalStatus: 'pending',
      requestId: 'req-2',
      cardId: 'Check [[ProjectSummary]] before proceeding',
    }
    await renderView(frame)

    const original = container.querySelector('.ai-goal-submit-original')
    expect(original).not.toBeNull()

    const link = original!.querySelector('a.wiki-word-link')
    expect(link).not.toBeNull()
    expect(link!.textContent).toBe('ProjectSummary')
  })

  it('displays approve and reject actions for pending status', async () => {
    const frame: GoalCardSubmitFrame = {
      id: 'goal-card-submit-3',
      type: 'goal_card_submit',
      status: 'pending',
      approvalStatus: 'pending',
      requestId: 'req-3',
      cardId: 'Task card',
    }
    await renderView(frame)

    const actions = container.querySelector('.ai-goal-submit-actions')
    expect(actions).not.toBeNull()
    expect(actions!.textContent).toContain('ai.goalCardSubmit.confirm')
    expect(actions!.textContent).toContain('ai.goalCardSubmit.reject')
  })

  it('submits an approval when the confirm button is clicked', async () => {
    const frame: GoalCardSubmitFrame = {
      id: 'goal-card-submit-4',
      type: 'goal_card_submit',
      status: 'pending',
      approvalStatus: 'pending',
      requestId: 'req-4',
      cardId: 'Task card',
    }
    await renderView(frame)

    const approve = container.querySelector('.ai-goal-submit-btn-approve') as HTMLButtonElement
    expect(approve).not.toBeNull()
    await act(async () => {
      approve.click()
    })
    expect(hoisted.submitGoalCardApproval).toHaveBeenCalledWith('approve')
  })

  it('hides actions after approval', async () => {
    const frame: GoalCardSubmitFrame = {
      id: 'goal-card-submit-5',
      type: 'goal_card_submit',
      status: 'completed',
      approvalStatus: 'approved',
      requestId: 'req-5',
      cardId: 'Task card',
    }
    await renderView(frame)

    expect(container.querySelector('.ai-goal-submit-actions')).toBeNull()
    expect(container.textContent).toContain('ai.goalCardSubmit.confirmed')
  })
})
