import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { GoalReviewView } from './GoalReviewView'
import type { GoalReviewFrame } from '../../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string, params?: Record<string, string>) => {
    if (key === 'ai.goalReview.reviewing') return `Reviewing goal: ${params?.condition ?? ''}`
    return key
  }),
}))

vi.mock('../../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

describe('GoalReviewView', () => {
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

  const renderView = async (frame: GoalReviewFrame) => {
    await act(async () => {
      root.render(<GoalReviewView frame={frame} />)
    })
  }

  it('renders running state with target icon and reviewing label', async () => {
    const frame: GoalReviewFrame = {
      id: 'turn-1-goal-review',
      type: 'goal_review',
      status: 'running',
      condition: 'refactor the auth module',
    }
    await renderView(frame)

    const step = container.querySelector('[data-slot-id="turn-1-goal-review"]')
    expect(step).not.toBeNull()
    expect(step?.textContent).toContain('Reviewing goal: refactor the auth module')
  })

  it('renders achieved state with success icon and verdict details', async () => {
    const frame: GoalReviewFrame = {
      id: 'turn-1-goal-review',
      type: 'goal_review',
      status: 'completed',
      condition: 'refactor the auth module',
      achieved: true,
      reason: 'model signaled [GOAL_ACHIEVED]',
      turnCount: 3,
      maxTurns: 20,
      aborted: false,
    }
    await renderView(frame)

    const step = container.querySelector('[data-slot-id="turn-1-goal-review"]')
    expect(step).not.toBeNull()
    expect(step?.textContent).toContain('ai.goalReview.achieved')

    // Expand the collapsible content to verify details
    const row = container.querySelector('.ai-step-row') as HTMLElement
    expect(row).not.toBeNull()
    await act(async () => {
      row.click()
    })

    const detail = container.querySelector('.ai-goal-review-detail')
    expect(detail).not.toBeNull()
    expect(detail?.textContent).toContain('refactor the auth module')
    expect(detail?.textContent).toContain('model signaled [GOAL_ACHIEVED]')
    expect(detail?.textContent).toContain('3 / 20')
  })

  it('renders not-achieved state with warning icon', async () => {
    const frame: GoalReviewFrame = {
      id: 'turn-1-goal-review',
      type: 'goal_review',
      status: 'completed',
      condition: 'refactor the auth module',
      achieved: false,
      reason: 'still missing tests',
      turnCount: 5,
      maxTurns: 20,
      aborted: false,
    }
    await renderView(frame)

    const step = container.querySelector('[data-slot-id="turn-1-goal-review"]')
    expect(step).not.toBeNull()
    expect(step?.textContent).toContain('ai.goalReview.notAchieved')
  })

  it('renders aborted state with error icon and turn limit', async () => {
    const frame: GoalReviewFrame = {
      id: 'turn-1-goal-review',
      type: 'goal_review',
      status: 'completed',
      condition: 'refactor the auth module',
      achieved: false,
      reason: 'still missing tests',
      turnCount: 20,
      maxTurns: 20,
      aborted: true,
    }
    await renderView(frame)

    const step = container.querySelector('[data-slot-id="turn-1-goal-review"]')
    expect(step).not.toBeNull()
    expect(step?.textContent).toContain('ai.goalReview.aborted')

    const row = container.querySelector('.ai-step-row') as HTMLElement
    await act(async () => {
      row.click()
    })

    const detail = container.querySelector('.ai-goal-review-detail')
    expect(detail).not.toBeNull()
    expect(detail?.textContent).toContain('20 / 20')
  })

  it('shows live reviewer tool activity while running without requiring manual expansion', async () => {
    const frame: GoalReviewFrame = {
      id: 'turn-1-goal-review',
      type: 'goal_review',
      status: 'running',
      condition: 'refactor the auth module',
      progress: {
        phase: 'reviewing',
        searchCount: 2,
        activeWork: [
          { Type: 'git_diff', Target: 'src/auth.go' },
          { Type: 'read', Target: 'src/auth/token.ts' },
          { Type: 'search', Target: 'src/**/auth*.ts' },
        ],
      },
    }
    await renderView(frame)

    // Body auto-unfolds immediately (CSS handles any visual delay).
    await act(async () => {
      await new Promise(r => setTimeout(r, 60))
    })

    const step = container.querySelector('[data-slot-id="turn-1-goal-review"]')
    expect(step).not.toBeNull()
    // Auto-expanded while running
    expect(step?.textContent).toContain('git_diff')
    expect(step?.textContent).toContain('src/auth.go')
    expect(step?.textContent).toContain('Reading')
    expect(step?.textContent).toContain('token.ts')
    expect(step?.textContent).toContain('2 searches')
  })
})
