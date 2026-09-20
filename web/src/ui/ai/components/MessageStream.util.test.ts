import { describe, it, expect } from 'vitest'
import { layoutAssistantFrames } from './MessageStream.util'
import type { Frame, TextFrame, GoalReviewFrame } from '../model/frame-types'

function textFrame(id: string, content: string): TextFrame {
  return { id, type: 'text', status: 'completed', content }
}

function goalReviewFrame(id: string): GoalReviewFrame {
  return { id, type: 'goal_review', status: 'completed', condition: 'refactor auth', achieved: true, reason: 'tests pass' }
}

describe('layoutAssistantFrames', () => {
  it('folds all but the last frame when the last frame is a normal step', () => {
    const frames: Frame[] = [
      textFrame('f1', 'Step one'),
      textFrame('f2', 'Step two'),
      textFrame('f3', 'Final summary'),
    ]
    const layout = layoutAssistantFrames(frames, true)
    expect(layout.shouldFold).toBe(true)
    expect(layout.foldFrames.map(f => f.id)).toEqual(['f1', 'f2'])
    expect(layout.keptFrames.map(f => f.id)).toEqual(['f3'])
  })

  it('keeps the last two frames when the last frame is a fork reviewer verdict (goal_review)', () => {
    const frames: Frame[] = [
      textFrame('f1', 'Step one'),
      textFrame('f2', 'Summary before review'),
      goalReviewFrame('f3'),
    ]
    const layout = layoutAssistantFrames(frames, true)
    expect(layout.shouldFold).toBe(true)
    expect(layout.foldFrames.map(f => f.id)).toEqual(['f1'])
    expect(layout.keptFrames.map(f => f.id)).toEqual(['f2', 'f3'])
  })

  it('does not fold when there are not enough frames', () => {
    const frames: Frame[] = [textFrame('f1', 'Only frame')]
    const layout = layoutAssistantFrames(frames, true)
    expect(layout.shouldFold).toBe(false)
    expect(layout.keptFrames.map(f => f.id)).toEqual(['f1'])
  })

  it('does not fold when the turn is still running', () => {
    const frames: Frame[] = [
      textFrame('f1', 'Step one'),
      textFrame('f2', 'Step two'),
      goalReviewFrame('f3'),
    ]
    const layout = layoutAssistantFrames(frames, false)
    expect(layout.shouldFold).toBe(false)
    expect(layout.keptFrames.map(f => f.id)).toEqual(['f1', 'f2', 'f3'])
  })

  it('filters out empty text frames and allowed permission requests', () => {
    const frames: Frame[] = [
      textFrame('f1', ''),
      { id: 'p1', type: 'permission_request', status: 'completed', allowed: true, callableId: 'x', input: '' } as unknown as Frame,
      textFrame('f2', 'Visible'),
    ]
    const layout = layoutAssistantFrames(frames, true)
    expect(layout.visible.map(f => f.id)).toEqual(['f2'])
  })
})
