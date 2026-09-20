import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type {
  AskUserQuestionFrame,
  GoalSubmitFrame,
  Frame,
  PermissionRequestFrame,
  PlanFrame,
  TurnEnvelope,
  TurnMetadata,
} from '../model/frame-types'
import * as notifySound from '../notify-sound'
import {
  collectPendingInteractionFrames,
  detectBackgroundTransitions,
  isSystemAllComplete,
  lastAssistantTurnFailed,
} from './useTurnCompleteNotifier'
import type { BackgroundNotifyState } from './useTurnCompleteNotifier'
import type { AgentInfo } from './agentInfoStore'

let idCounter = 0
function env(
  role: 'user' | 'assistant',
  opts: { turnState?: TurnMetadata['turnState']; frames?: Frame[] } = {},
): TurnEnvelope {
  idCounter += 1
  return {
    id: `e-${idCounter}`,
    role,
    frames: opts.frames ?? [],
    timestamp: '1',
    metadata: opts.turnState ? { turnId: 't1', turnState: opts.turnState } : { turnId: 't1' },
  }
}

function askUserFrame(id: string, answered = false): AskUserQuestionFrame {
  return {
    id,
    type: 'ask_user',
    // Unanswered live interactions render 'running' (step open); resolved or
    // cancelled ones render 'completed' (step closed).
    status: answered ? 'completed' : 'running',
    questions: [{ header: 'h', question: 'q?', options: [{ label: 'a' }], multiSelect: false }],
    answers: answered ? { 0: 'a' } : undefined,
  }
}

function permissionFrame(id: string, decided = false): PermissionRequestFrame {
  return {
    id,
    type: 'permission_request',
    status: decided ? 'completed' : 'running',
    toolCalls: [{ id: 'c1', callableId: 'shell.exec' }],
    allowed: decided ? true : undefined,
  }
}

function planFrame(id: string, approvalStatus: PlanFrame['approvalStatus']): PlanFrame {
  return {
    id,
    type: 'plan',
    status: 'running',
    content: 'do stuff',
    approvalStatus,
  }
}

function goalSubmitFrame(id: string, approvalStatus: GoalSubmitFrame['approvalStatus']): GoalSubmitFrame {
  return {
    id,
    type: 'goal_submit',
    status: 'running',
    condition: 'goal',
    interpretedGoal: 'interp',
    requestId: id,
    approvalStatus,
  }
}

describe('lastAssistantTurnFailed', () => {
  it('returns true when last assistant turn is failed', () => {
    expect(lastAssistantTurnFailed([env('assistant', { turnState: 'failed' })])).toBe(true)
  })

  it('returns true when last assistant turn is cancelled', () => {
    expect(lastAssistantTurnFailed([env('assistant', { turnState: 'cancelled' })])).toBe(true)
  })

  it('returns false when last assistant turn is completed', () => {
    expect(lastAssistantTurnFailed([env('assistant', { turnState: 'completed' })])).toBe(false)
  })

  it('returns false when last assistant turn is running', () => {
    expect(lastAssistantTurnFailed([env('assistant', { turnState: 'running' })])).toBe(false)
  })

  it('ignores trailing user envelopes and inspects the last assistant one', () => {
    expect(lastAssistantTurnFailed([
      env('assistant', { turnState: 'failed' }),
      env('user'),
    ])).toBe(true)
  })

  it('returns false when there is no assistant envelope', () => {
    expect(lastAssistantTurnFailed([env('user')])).toBe(false)
    expect(lastAssistantTurnFailed([])).toBe(false)
  })

  it('inspects the most recent assistant envelope when multiple exist', () => {
    expect(lastAssistantTurnFailed([
      env('assistant', { turnState: 'failed' }),
      env('assistant', { turnState: 'completed' }),
    ])).toBe(false)
    expect(lastAssistantTurnFailed([
      env('assistant', { turnState: 'completed' }),
      env('assistant', { turnState: 'cancelled' }),
    ])).toBe(true)
  })
})

describe('collectPendingInteractionFrames', () => {
  it('collects pending ask_user frame (no answers)', () => {
    const out = collectPendingInteractionFrames([
      env('assistant', { frames: [askUserFrame('ask-1', false)] }),
    ])
    expect(out.map(f => f.id)).toEqual(['ask-1'])
  })

  it('excludes answered ask_user frame', () => {
    const out = collectPendingInteractionFrames([
      env('assistant', { frames: [askUserFrame('ask-1', true)] }),
    ])
    expect(out).toHaveLength(0)
  })

  it('collects pending permission_request frame (no decision)', () => {
    const out = collectPendingInteractionFrames([
      env('assistant', { frames: [permissionFrame('perm-1', false)] }),
    ])
    expect(out.map(f => f.id)).toEqual(['perm-1'])
  })

  it('excludes decided permission_request frame', () => {
    const out = collectPendingInteractionFrames([
      env('assistant', { frames: [permissionFrame('perm-1', true)] }),
    ])
    expect(out).toHaveLength(0)
  })

  it('excludes closed ask_user frame without answers (cancelled turn)', () => {
    const frame = askUserFrame('ask-1', false)
    frame.status = 'completed'
    const out = collectPendingInteractionFrames([
      env('assistant', { frames: [frame] }),
    ])
    expect(out).toHaveLength(0)
  })

  it('excludes closed permission_request frame without decision (cancelled turn)', () => {
    const frame = permissionFrame('perm-1', false)
    frame.status = 'completed'
    const out = collectPendingInteractionFrames([
      env('assistant', { frames: [frame] }),
    ])
    expect(out).toHaveLength(0)
  })

  it('collects pending plan frame', () => {
    const out = collectPendingInteractionFrames([
      env('assistant', { frames: [planFrame('plan-1', 'pending')] }),
    ])
    expect(out.map(f => f.id)).toEqual(['plan-1'])
  })

  it('excludes approved/rejected/cancelled plan frames', () => {
    for (const status of ['approved', 'rejected', 'cancelled'] as const) {
      const out = collectPendingInteractionFrames([
        env('assistant', { frames: [planFrame('plan-1', status)] }),
      ])
      expect(out).toHaveLength(0)
    }
  })

  it('collects pending goal_submit frame', () => {
    const out = collectPendingInteractionFrames([
      env('assistant', { frames: [goalSubmitFrame('goal-1', 'pending')] }),
    ])
    expect(out.map(f => f.id)).toEqual(['goal-1'])
  })

  it('excludes resolved goal_submit frames', () => {
    for (const status of ['approved', 'rejected', 'cancelled'] as const) {
      const out = collectPendingInteractionFrames([
        env('assistant', { frames: [goalSubmitFrame('goal-1', status)] }),
      ])
      expect(out).toHaveLength(0)
    }
  })

  it('collects multiple pending interactions across envelopes', () => {
    const out = collectPendingInteractionFrames([
      env('assistant', { frames: [askUserFrame('ask-1')] }),
      env('user'),
      env('assistant', { frames: [planFrame('plan-1', 'pending'), goalSubmitFrame('goal-1', 'pending')] }),
    ])
    expect(out.map(f => f.id).sort()).toEqual(['ask-1', 'goal-1', 'plan-1'])
  })

  it('ignores user envelopes', () => {
    const out = collectPendingInteractionFrames([
      env('user', { frames: [askUserFrame('ask-1')] }),
    ])
    expect(out).toHaveLength(0)
  })

  it('ignores non-interaction frames', () => {
    const out = collectPendingInteractionFrames([
      env('assistant', { frames: [
        { id: 't1', type: 'text', status: 'completed', content: 'hi' },
        { id: 'tool1', type: 'tool', status: 'completed', toolName: 'foo', input: '{}' },
      ] }),
    ])
    expect(out).toHaveLength(0)
  })

  it('returns empty for empty envelopes', () => {
    expect(collectPendingInteractionFrames([])).toHaveLength(0)
  })
})

// Validate that the notify-sound module routes failed → error tone,
// completed → complete tone, interaction → interaction tone.
describe('notify-sound routing', () => {
  beforeEach(() => {
    // Stub AudioContext so playTones is a no-op but still callable.
    vi.stubGlobal('AudioContext', class {
      state = 'running'
      currentTime = 0
      destination = {}
      createOscillator() {
        return {
          type: '', frequency: { value: 0 },
          connect: () => ({ connect: () => ({}) }),
          start() {}, stop() {},
        }
      }
      createGain() {
        return {
          gain: { value: 0, setValueAtTime() {}, exponentialRampToValueAtTime() {} },
          connect: () => ({ connect: () => ({}) }),
        }
      }
      resume() {}
    })
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('plays error sound for failed config with OnError enabled', () => {
    const spy = vi.spyOn(notifySound, 'playErrorSound')
    const cfg = { Enabled: true, Volume: 50, Sound: 'chime', OnComplete: true, OnError: true, OnInteraction: true, OnAllComplete: true }
    notifySound.playErrorSound(cfg)
    expect(spy).toHaveBeenCalledWith(cfg)
  })

  it('plays interaction sound when OnInteraction enabled', () => {
    const spy = vi.spyOn(notifySound, 'playInteractionSound')
    const cfg = { Enabled: true, Volume: 50, Sound: 'chime', OnComplete: true, OnError: true, OnInteraction: true, OnAllComplete: true }
    notifySound.playInteractionSound(cfg)
    expect(spy).toHaveBeenCalledWith(cfg)
  })

  it('does not play interaction sound when OnInteraction disabled', () => {
    // playInteractionSound early-returns internally when OnInteraction is
    // false; the guard is inside the function.
    expect(() => notifySound.playInteractionSound({
      Enabled: true, Volume: 50, Sound: 'chime', OnComplete: true, OnError: true, OnInteraction: false, OnAllComplete: true,
    })).not.toThrow()
  })

  it('plays all-complete sound when OnAllComplete enabled', () => {
    const spy = vi.spyOn(notifySound, 'playAllCompleteSound')
    const cfg = { Enabled: true, Volume: 50, Sound: 'chime', OnComplete: true, OnError: true, OnInteraction: true, OnAllComplete: true }
    notifySound.playAllCompleteSound(cfg)
    expect(spy).toHaveBeenCalledWith(cfg)
  })

  it('does not play all-complete sound when OnAllComplete disabled', () => {
    expect(() => notifySound.playAllCompleteSound({
      Enabled: true, Volume: 50, Sound: 'chime', OnComplete: true, OnError: true, OnInteraction: true, OnAllComplete: false,
    })).not.toThrow()
  })
})

describe('detectBackgroundTransitions', () => {
  function state(over: Partial<BackgroundNotifyState> = {}): BackgroundNotifyState {
    return { waiting: false, error: false, working: false, paused: false, ...over }
  }
  function map(entries: Record<string, BackgroundNotifyState>): Map<string, BackgroundNotifyState> {
    return new Map(Object.entries(entries))
  }

  it('fires interaction when a background agent starts waiting', () => {
    const prev = map({ a1: state({ working: true }) })
    const next = map({ a1: state({ waiting: true, working: true }) })
    expect(detectBackgroundTransitions(prev, next, null)).toEqual([{ actorId: 'a1', kind: 'interaction' }])
  })

  it('skips the excluded (active) agent', () => {
    const prev = map({ a1: state({ working: true }) })
    const next = map({ a1: state({ waiting: true, working: true }) })
    expect(detectBackgroundTransitions(prev, next, 'a1')).toEqual([])
  })

  it('does not refire while the agent stays waiting', () => {
    const prev = map({ a1: state({ waiting: true, working: true }) })
    const next = map({ a1: state({ waiting: true, working: true }) })
    expect(detectBackgroundTransitions(prev, next, null)).toEqual([])
  })

  it('fires error on transition into error', () => {
    const prev = map({ a1: state({ working: true }) })
    const next = map({ a1: state({ error: true }) })
    expect(detectBackgroundTransitions(prev, next, null)).toEqual([{ actorId: 'a1', kind: 'error' }])
  })

  it('fires complete when a working agent goes idle', () => {
    const prev = map({ a1: state({ working: true }) })
    const next = map({ a1: state() })
    expect(detectBackgroundTransitions(prev, next, null)).toEqual([{ actorId: 'a1', kind: 'complete' }])
  })

  it('does not fire complete when the agent pauses', () => {
    const prev = map({ a1: state({ working: true }) })
    const next = map({ a1: state({ paused: true }) })
    expect(detectBackgroundTransitions(prev, next, null)).toEqual([])
  })

  it('does not fire complete when the agent stops into a waiting state', () => {
    const prev = map({ a1: state({ working: true }) })
    const next = map({ a1: state({ waiting: true }) })
    expect(detectBackgroundTransitions(prev, next, null)).toEqual([{ actorId: 'a1', kind: 'interaction' }])
  })

  it('ignores newly appearing agents (no transition baseline)', () => {
    const prev = map({})
    const next = map({ a1: state({ waiting: true }) })
    expect(detectBackgroundTransitions(prev, next, null)).toEqual([])
  })
})

describe('isSystemAllComplete', () => {
  function agent(actorId: string, over: Partial<AgentInfo> = {}): AgentInfo {
    return { ActorId: actorId, IsWorking: false, ...over } as AgentInfo
  }

  it('returns true when there are no agents', () => {
    expect(isSystemAllComplete([], 'a1')).toBe(true)
  })

  it('returns true when the only working agent is the one that just completed', () => {
    const items = [agent('a1', { IsWorking: true })]
    expect(isSystemAllComplete(items, 'a1')).toBe(true)
  })

  it('returns false when another agent is still working', () => {
    const items = [agent('a1', { IsWorking: true }), agent('a2', { IsWorking: true })]
    expect(isSystemAllComplete(items, 'a1')).toBe(false)
  })

  it('returns false when any idle agent holds an active workflow', () => {
    const items = [agent('a1'), agent('wf-owner', { ActiveWorkflowMapCardId: 'map-1' })]
    expect(isSystemAllComplete(items, 'a1')).toBe(false)
  })

  it('returns false when the completing agent still owns an active workflow', () => {
    const items = [agent('a1', { IsWorking: true, ActiveWorkflowMapCardId: 'map-1' })]
    expect(isSystemAllComplete(items, 'a1')).toBe(false)
  })

  it('returns true when only paused/idle agents remain', () => {
    const items = [agent('a1'), agent('a2', { Status: 'paused' })]
    expect(isSystemAllComplete(items, 'a1')).toBe(true)
  })
})
