import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type { GuideStep } from '../gen-types/interfacemanager'
import type { InteractionPayload } from './telemetry'

const hoisted = vi.hoisted(() => ({
  invoke: vi.fn(async (_req: unknown, _meta?: unknown) => ({
    Profile: {
      Account: '',
      Capabilities: [] as Array<{ Capability: string }>,
      UpdatedAt: new Date().toISOString(),
    },
  })),
  onInteractionListeners: [] as Array<(payload: InteractionPayload) => void>,
  reportInteraction: vi.fn(async (_client: unknown, _req: unknown) => ({})),
}))

vi.mock('./generated-client', () => ({
  client: { invoke: hoisted.invoke },
}))

vi.mock('./telemetry', () => ({
  onInteraction: (listener: (payload: InteractionPayload) => void) => {
    hoisted.onInteractionListeners.push(listener)
    return () => {
      hoisted.onInteractionListeners = hoisted.onInteractionListeners.filter((l) => l !== listener)
    }
  },
  reportInteraction: vi.fn(),
}))

vi.mock('../gen-clients/interfacemanager/client', () => ({
  reportInteraction: hoisted.reportInteraction,
}))

import { guideManager, matchesExpectedInteraction, TOUR_CAPABILITY } from './guide-manager'

describe('guide-manager', () => {
  beforeEach(() => {
    hoisted.invoke.mockClear()
    hoisted.reportInteraction.mockClear()
    guideManager.hideGuide()
  })

  afterEach(() => {
    guideManager.hideGuide()
  })

  describe('matchesExpectedInteraction', () => {
    it('matches a click spec', () => {
      expect(
        matchesExpectedInteraction('click:composer.permission-mode', {
          kind: 'click',
          guideId: 'composer.permission-mode',
        }),
      ).toBe(true)
      expect(
        matchesExpectedInteraction('click:composer.permission-mode', {
          kind: 'click',
          guideId: 'composer.send',
        }),
      ).toBe(false)
    })

    it('matches a text substring in submit/input', () => {
      expect(
        matchesExpectedInteraction('text:/workflow', {
          kind: 'submit',
          guideId: 'composer.input',
          text: '/workflow my goal',
        }),
      ).toBe(true)
      expect(
        matchesExpectedInteraction('text:/workflow', {
          kind: 'input',
          guideId: 'composer.input',
          text: '/workflow',
        }),
      ).toBe(true)
      expect(
        matchesExpectedInteraction('text:/workflow', {
          kind: 'submit',
          guideId: 'composer.input',
          text: 'hello',
        }),
      ).toBe(false)
    })

    it('matches any submit for the submit spec', () => {
      expect(
        matchesExpectedInteraction('submit', {
          kind: 'submit',
          guideId: 'composer.input',
        }),
      ).toBe(true)
      expect(
        matchesExpectedInteraction('submit', {
          kind: 'click',
          guideId: 'composer.input',
        }),
      ).toBe(false)
    })

    it('matches any alternative in a comma-separated union', () => {
      expect(
        matchesExpectedInteraction('click:topbar.omnibox-trigger,text:/', {
          kind: 'click',
          guideId: 'topbar.omnibox-trigger',
        }),
      ).toBe(true)
      expect(
        matchesExpectedInteraction('click:topbar.omnibox-trigger,text:/', {
          kind: 'submit',
          guideId: 'composer.input',
          text: '/goal',
        }),
      ).toBe(true)
    })

    it('returns true for absent/empty spec', () => {
      expect(matchesExpectedInteraction(undefined, { kind: 'click', guideId: 'x' })).toBe(true)
      expect(matchesExpectedInteraction('', { kind: 'click', guideId: 'x' })).toBe(true)
    })
  })

  describe('gated tour flow', () => {
    const fire = (p: InteractionPayload) => {
      for (const l of hoisted.onInteractionListeners) l(p)
    }
    const steps: GuideStep[] = [
      {
        TargetGuideId: 'composer.permission-mode',
        Title: 'Permission',
        Body: 'Body',
        ExpectedInteraction: 'click:composer.permission-mode',
        CapabilityKind: 'permission',
      },
      {
        TargetGuideId: 'composer.input',
        Title: 'Conversation',
        Body: 'Body',
        ExpectedInteraction: 'submit',
        CapabilityKind: 'conversation',
      },
    ]

    it('starts at step 0 with the gate unmet', () => {
      let state = guideManager.getState()
      expect(state.visible).toBe(false)

      guideManager.showGuide(steps, true)
      state = guideManager.getState()
      expect(state.visible).toBe(true)
      expect(state.currentIndex).toBe(0)
      expect(state.gateMet).toBe(false)
      expect(state.steps).toBe(steps)
    })

    it('auto-advances when the gate interaction is observed', () => {
      guideManager.showGuide(steps, true)

      fire({ kind: 'click', guideId: 'composer.permission-mode' })

      const state = guideManager.getState()
      expect(state.currentIndex).toBe(1)
      expect(state.gateMet).toBe(false)
    })

    it('does not auto-advance for non-matching interactions', () => {
      guideManager.showGuide(steps, true)

      fire({ kind: 'click', guideId: 'composer.send' })

      expect(guideManager.getState().currentIndex).toBe(0)
    })

    it('reports the step capability on manual next and on tour completion', () => {
      guideManager.showGuide(steps, true)
      // First step is gated; satisfy it.
      fire({ kind: 'click', guideId: 'composer.permission-mode' })
      expect(hoisted.invoke).toHaveBeenCalledWith(
        'coordinator_guidance_profile_increment',
        expect.objectContaining({ Capability: 'permission', Signal: 'step_completed' }),
        expect.any(Object),
      )

      // Second step is gated by submit; satisfy it to reach the end.
      fire({ kind: 'submit', guideId: 'composer.input' })
      expect(hoisted.invoke).toHaveBeenCalledWith(
        'coordinator_guidance_profile_increment',
        expect.objectContaining({ Capability: 'conversation', Signal: 'guide_completed' }),
        expect.any(Object),
      )
      expect(hoisted.invoke).toHaveBeenCalledWith(
        'coordinator_guidance_profile_increment',
        expect.objectContaining({ Capability: TOUR_CAPABILITY, Signal: 'guide_completed' }),
        expect.any(Object),
      )
    })

    it('persists skipped state for a tour', () => {
      guideManager.showGuide(steps, true)
      guideManager.skipGuide()

      expect(hoisted.invoke).toHaveBeenCalledWith(
        'coordinator_guidance_profile_increment',
        expect.objectContaining({ Capability: TOUR_CAPABILITY, Signal: 'guide_skipped' }),
        expect.any(Object),
      )
    })

    it('does not persist tour state for non-tour guides', () => {
      guideManager.showGuide(steps, false)
      guideManager.skipGuide()
      guideManager.showGuide(steps, false)
      guideManager.completeGuide()

      const tourCalls = hoisted.invoke.mock.calls.filter(
        (call) => ((call[1] ?? {}) as { Capability?: string }).Capability === TOUR_CAPABILITY,
      )
      expect(tourCalls).toHaveLength(0)
    })
  })

  describe('external guide progress reporting (T3)', () => {
    const steps: GuideStep[] = [
      { TargetGuideId: 'guide.a', Title: 'A', Body: 'Body' },
      { TargetGuideId: 'guide.b', Title: 'B', Body: 'Body' },
    ]

    it('reports guide_progress on step advance and tour completion', () => {
      guideManager.showGuide(steps, false, undefined, { source: 'external' })
      guideManager.nextStep()

      expect(hoisted.reportInteraction).toHaveBeenCalledTimes(1)
      expect(hoisted.reportInteraction).toHaveBeenCalledWith(expect.anything(), {
        Kind: 'guide_progress',
        GuideId: 'guide.a',
        Detail: '1/2 guide.a step_completed',
      })

      guideManager.completeGuide()
      expect(hoisted.reportInteraction).toHaveBeenCalledTimes(2)
      expect(hoisted.reportInteraction).toHaveBeenCalledWith(expect.anything(), {
        Kind: 'guide_progress',
        GuideId: 'guide.b',
        Detail: '2/2 guide.b tour_completed',
      })
    })

    it('reports guide_progress on tour skip', () => {
      guideManager.showGuide(steps, false, undefined, { source: 'external' })
      guideManager.skipGuide()

      expect(hoisted.reportInteraction).toHaveBeenCalledTimes(1)
      expect(hoisted.reportInteraction).toHaveBeenCalledWith(expect.anything(), {
        Kind: 'guide_progress',
        GuideId: 'guide.a',
        Detail: '1/2 guide.a tour_skipped',
      })
    })

    it('does not report guide_progress for builtin guides', () => {
      guideManager.showGuide(steps)
      guideManager.nextStep()

      guideManager.showGuide(steps)
      guideManager.completeGuide()

      guideManager.showGuide(steps)
      guideManager.skipGuide()

      expect(hoisted.reportInteraction).not.toHaveBeenCalled()
    })
  })

  describe('onTourFinished', () => {
    const steps: GuideStep[] = [
      { TargetGuideId: 'composer.permission-mode', Title: 'Permission', Body: 'Body' },
      { TargetGuideId: 'composer.input', Title: 'Conversation', Body: 'Body' },
    ]

    beforeEach(() => {
      guideManager.hideGuide()
    })

    it('fires when a triggerToolGuide tour is completed', () => {
      const listener = vi.fn()
      guideManager.onTourFinished(listener)
      guideManager.showGuide(steps, true, 'test', { triggerToolGuide: true })
      guideManager.completeGuide()
      expect(listener).toHaveBeenCalledTimes(1)
    })

    it('fires when a triggerToolGuide tour is skipped', () => {
      const listener = vi.fn()
      guideManager.onTourFinished(listener)
      guideManager.showGuide(steps, true, 'test', { triggerToolGuide: true })
      guideManager.skipGuide()
      expect(listener).toHaveBeenCalledTimes(1)
    })

    it('does not fire for a replay tour without triggerToolGuide', () => {
      const listener = vi.fn()
      guideManager.onTourFinished(listener)
      guideManager.showGuide(steps, true, 'test')
      guideManager.completeGuide()
      expect(listener).not.toHaveBeenCalled()
    })

    it('does not fire for non-tour guides', () => {
      const listener = vi.fn()
      guideManager.onTourFinished(listener)
      guideManager.showGuide(steps, false, 'test', { triggerToolGuide: true })
      guideManager.completeGuide()
      expect(listener).not.toHaveBeenCalled()
    })
  })
})
