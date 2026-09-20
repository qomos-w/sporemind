import { describe, it, expect } from 'vitest'
import { extractPendingInteraction, envelopeHasPendingInteraction } from './pending-interaction'
import type {
  TurnEnvelope,
  Frame,
  AskUserQuestionFrame,
  PermissionRequestFrame,
  GoalSubmitFrame,
  GoalCardSubmitFrame,
  PlanFrame,
  TextFrame,
} from './frame-types'

function makeAssistantEnvelope(frames: Frame[], id = 'env-a'): TurnEnvelope {
  return { id, role: 'assistant', frames, timestamp: new Date().toISOString() }
}

function makeUserEnvelope(content: string, id = 'env-u'): TurnEnvelope {
  return { id, role: 'user', userContent: content, frames: [], timestamp: new Date().toISOString() }
}

function makeAskFrame(overrides: Partial<AskUserQuestionFrame> = {}): AskUserQuestionFrame {
  return {
    id: 'ask-1',
    type: 'ask_user',
    status: 'running',
    questions: [{ header: 'Choice', question: 'Pick one', options: [{ label: 'A' }, { label: 'B' }], multiSelect: false }],
    requestId: 'req-1',
    ...overrides,
  }
}

function makePermissionFrame(overrides: Partial<PermissionRequestFrame> = {}): PermissionRequestFrame {
  return {
    id: 'perm-1',
    type: 'permission_request',
    status: 'running',
    toolCalls: [{ id: 'tc-1', callableId: 'project.write' }],
    requestId: 'req-2',
    ...overrides,
  }
}

function makeGoalSubmitFrame(overrides: Partial<GoalSubmitFrame> = {}): GoalSubmitFrame {
  return {
    id: 'goal-1',
    type: 'goal_submit',
    status: 'running',
    condition: 'fix bug',
    interpretedGoal: 'Fix the bug',
    requestId: 'req-3',
    approvalStatus: 'pending',
    ...overrides,
  }
}

function makeGoalCardSubmitFrame(overrides: Partial<GoalCardSubmitFrame> = {}): GoalCardSubmitFrame {
  return {
    id: 'goal-card-1',
    type: 'goal_card_submit',
    status: 'running',
    cardId: 'card-1',
    requestId: 'req-4',
    approvalStatus: 'pending',
    ...overrides,
  }
}

function makePlanFrame(overrides: Partial<PlanFrame> = {}): PlanFrame {
  return {
    id: 'plan-1',
    type: 'plan',
    status: 'running',
    content: '## Plan',
    approvalStatus: 'pending',
    ...overrides,
  }
}

function makeTextFrame(id = 'text-1'): TextFrame {
  return { id, type: 'text', status: 'completed', content: 'done' }
}

describe('extractPendingInteraction', () => {
  it('returns null for empty envelopes', () => {
    expect(extractPendingInteraction([])).toBeNull()
  })

  it('envelopeHasPendingInteraction mirrors detection for virtualization exemption', () => {
    expect(envelopeHasPendingInteraction(makeAssistantEnvelope([makePlanFrame()]))).toBe(true)
    expect(envelopeHasPendingInteraction(makeAssistantEnvelope([makeAskFrame()]))).toBe(true)
    expect(envelopeHasPendingInteraction(makeAssistantEnvelope([makePermissionFrame()]))).toBe(true)
    expect(envelopeHasPendingInteraction(makeAssistantEnvelope([makeGoalSubmitFrame()]))).toBe(true)
    expect(envelopeHasPendingInteraction(makeAssistantEnvelope([makeTextFrame()]))).toBe(false)
    expect(envelopeHasPendingInteraction(makeAssistantEnvelope([makePlanFrame({ approvalStatus: 'approved', status: 'completed' })]))).toBe(false)
    expect(envelopeHasPendingInteraction(makeUserEnvelope('hi'))).toBe(false)
  })

  it('returns null when only text/tool frames exist', () => {
    const envs = [makeAssistantEnvelope([makeTextFrame()])]
    expect(extractPendingInteraction(envs)).toBeNull()
  })

  it('detects an unresolved ask_user', () => {
    const envs = [makeAssistantEnvelope([makeAskFrame()])]
    const result = extractPendingInteraction(envs)
    expect(result?.kind).toBe('ask_user')
    expect(result?.frame.id).toBe('ask-1')
  })

  it('ignores an answered ask_user', () => {
    const envs = [makeAssistantEnvelope([makeAskFrame({ status: 'completed', answers: { 0: 'A' } })])]
    expect(extractPendingInteraction(envs)).toBeNull()
  })

  it('ignores a cancelled ask_user even without answers', () => {
    const envs = [makeAssistantEnvelope([makeAskFrame({ status: 'cancelled' })])]
    expect(extractPendingInteraction(envs)).toBeNull()
  })

  it('detects an unresolved permission_request', () => {
    const envs = [makeAssistantEnvelope([makePermissionFrame()])]
    const result = extractPendingInteraction(envs)
    expect(result?.kind).toBe('permission_request')
  })

  it('ignores an allowed permission_request', () => {
    const envs = [makeAssistantEnvelope([makePermissionFrame({ allowed: true, status: 'completed' })])]
    expect(extractPendingInteraction(envs)).toBeNull()
  })

  it('detects a pending goal_submit', () => {
    const envs = [makeAssistantEnvelope([makeGoalSubmitFrame()])]
    const result = extractPendingInteraction(envs)
    expect(result?.kind).toBe('goal_submit')
  })

  it('ignores an approved goal_submit', () => {
    const envs = [makeAssistantEnvelope([makeGoalSubmitFrame({ approvalStatus: 'approved', status: 'completed' })])]
    expect(extractPendingInteraction(envs)).toBeNull()
  })

  it('detects a pending goal_card_submit', () => {
    const envs = [makeAssistantEnvelope([makeGoalCardSubmitFrame()])]
    const result = extractPendingInteraction(envs)
    expect(result?.kind).toBe('goal_card_submit')
  })

  it('detects a plan awaiting approval', () => {
    const envs = [makeAssistantEnvelope([makePlanFrame()])]
    const result = extractPendingInteraction(envs)
    expect(result?.kind).toBe('plan')
  })

  it('ignores an approved plan', () => {
    const envs = [makeAssistantEnvelope([makePlanFrame({ approvalStatus: 'approved', status: 'completed' })])]
    expect(extractPendingInteraction(envs)).toBeNull()
  })

  it('returns the latest pending interaction when multiple exist', () => {
    const envs = [
      makeAssistantEnvelope([makePermissionFrame()], 'env-1'),
      makeAssistantEnvelope([makeAskFrame({ id: 'ask-2' })], 'env-2'),
    ]
    const result = extractPendingInteraction(envs)
    expect(result?.kind).toBe('ask_user')
    expect(result?.frame.id).toBe('ask-2')
  })

  it('skips resolved interactions and finds an older pending one', () => {
    const envs = [
      makeAssistantEnvelope([makeAskFrame({ id: 'ask-old' })], 'env-1'),
      makeAssistantEnvelope([makeGoalSubmitFrame({ approvalStatus: 'approved', status: 'completed' })], 'env-2'),
    ]
    const result = extractPendingInteraction(envs)
    expect(result?.kind).toBe('ask_user')
    expect(result?.frame.id).toBe('ask-old')
  })

  it('still detects a pending interaction when a newer user envelope exists', () => {
    const envs = [
      makeAssistantEnvelope([makeAskFrame()], 'env-1'),
      makeUserEnvelope('still thinking', 'env-2'),
    ]
    const result = extractPendingInteraction(envs)
    expect(result?.kind).toBe('ask_user')
  })
})
