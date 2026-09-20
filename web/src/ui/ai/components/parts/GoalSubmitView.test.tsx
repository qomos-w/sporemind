import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { GoalSubmitView } from './GoalSubmitView'
import type { GoalSubmitFrame } from '../../model/frame-types'

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  submitGoalApproval: vi.fn(),
}))

vi.mock('../../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../hooks/useStepInteraction', () => ({
  useStepInteraction: () => ({
    submitGoalApproval: hoisted.submitGoalApproval,
    submitting: false,
  }),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('GoalSubmitView', () => {
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

  const renderView = async (frame: GoalSubmitFrame) => {
    await act(async () => {
      root.render(<GoalSubmitView frame={frame} />)
    })
  }

  it('renders interpreted goal as markdown with wikiword links', async () => {
    const frame: GoalSubmitFrame = {
      id: 'goal-submit-1',
      type: 'goal_submit',
      status: 'pending',
      approvalStatus: 'pending',
      requestId: 'req-1',
      condition: 'fix the goal UI',
      interpretedGoal: 'Use **AIStepText** for [[GoalSubmitView]] rendering',
    }
    await renderView(frame)

    const interpreted = container.querySelector('.ai-goal-submit-interpreted')
    expect(interpreted).not.toBeNull()

    const strong = interpreted!.querySelector('strong')
    expect(strong).not.toBeNull()
    expect(strong!.textContent).toBe('AIStepText')

    const link = interpreted!.querySelector('a.wiki-word-link')
    expect(link).not.toBeNull()
    expect(link!.textContent).toBe('GoalSubmitView')
  })

  it('renders condition as markdown with wikiword links', async () => {
    const frame: GoalSubmitFrame = {
      id: 'goal-submit-2',
      type: 'goal_submit',
      status: 'pending',
      approvalStatus: 'pending',
      requestId: 'req-2',
      condition: 'Check [[ProjectSummary]] before proceeding.',
      interpretedGoal: '',
    }
    await renderView(frame)

    const original = container.querySelector('.ai-goal-submit-original')
    expect(original).not.toBeNull()

    const link = original!.querySelector('a.wiki-word-link')
    expect(link).not.toBeNull()
    expect(link!.textContent).toBe('ProjectSummary')
  })

  it('displays approve and reject actions for pending status', async () => {
    const frame: GoalSubmitFrame = {
      id: 'goal-submit-3',
      type: 'goal_submit',
      status: 'pending',
      approvalStatus: 'pending',
      requestId: 'req-3',
      condition: 'test',
      interpretedGoal: 'test goal',
    }
    await renderView(frame)

    const actions = container.querySelector('.ai-goal-submit-actions')
    expect(actions).not.toBeNull()
    expect(actions!.textContent).toContain('ai.goalSubmit.confirm')
    expect(actions!.textContent).toContain('ai.goalSubmit.reject')
  })
})
