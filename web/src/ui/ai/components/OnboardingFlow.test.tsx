import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import React from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { OnboardingFlow } from './OnboardingFlow'
import { buildBasicsTutorial } from '../../../application/onboarding-guide'
import { GuideIds } from '../guide-ids'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// Hoisted mocks so vi.mock factories (which run before the module body) can
// reference stable spy/fixture references.
const hoisted = vi.hoisted(() => ({
  // t echoes the raw i18n key, matching onboarding-guide.test.ts — this makes
  // the "correct steps" assertion deterministic without a locale catalog.
  t: vi.fn((key: string) => key),
  showGuide: vi.fn(),
  hideGuide: vi.fn(),
  onCoordinatorCreated: vi.fn(),
  providers: [{ Name: 'openai' }] as any[],
  // Default guidance-profile query returns an empty profile → tour auto-triggers.
  invoke: vi.fn(
    async (_req?: unknown, _meta?: unknown): Promise<{ Profile?: { Capabilities?: Array<{ Capability: string }> } }> => ({
      Profile: { Capabilities: [] },
    }),
  ),
  // workspace.load_agent resolves the coordinator's live ActorId.
  loadAgent: vi.fn(
    async (_client: unknown, req: { AgentId: string }): Promise<{ Id: string; ActorId: string }> => ({
      Id: req.AgentId,
      ActorId: 'actor-1',
    }),
  ),
  mockAgent: {
    Id: 'coord-1',
    ActorId: 'actor-1',
    ProjectId: '',
    DisplayName: 'Coordinator',
    AgentKind: 'coordinator',
  } as any,
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../hooks/useProviderConfigs', () => ({
  useProviderConfigs: () => ({
    providers: hoisted.providers,
    loading: false,
    error: null,
    reload: vi.fn(),
    configure: vi.fn(),
    remove: vi.fn(),
  }),
}))

vi.mock('../../../application/guide-manager', () => ({
  guideManager: {
    showGuide: hoisted.showGuide,
    hideGuide: hoisted.hideGuide,
    nextStep: vi.fn(),
    prevStep: vi.fn(),
    completeGuide: vi.fn(),
    subscribe: vi.fn(() => () => {}),
    getState: vi.fn(() => ({ steps: [], currentIndex: 0, visible: false })),
    reportTargetMissing: vi.fn(),
    skipGuide: vi.fn(),
  },
  TOUR_CAPABILITY: 'onboarding.tour',
}))

// Mock the RPC client so the guidance-profile query (进度持久化) is driven by
// the hoisted `invoke` spy, and workspace.load_agent by the hoisted
// `loadAgent` spy: empty capabilities → auto-trigger, presence of the
// `onboarding.tour` capability → suppress auto-trigger.
vi.mock('../../../application/generated-client', () => ({
  client: { invoke: hoisted.invoke },
}))

vi.mock('../../../gen-clients/workspace/client', () => ({
  loadAgent: hoisted.loadAgent,
}))

// Mock CoordinatorSetupModal: expose buttons to drive onCreated / onDismiss.
vi.mock('./CoordinatorSetupModal', () => ({
  CoordinatorSetupModal: ({ onCreated, onDismiss }: any) => (
    <div data-testid="coordinator-setup">
      <button data-testid="coordinator-create" onClick={() => onCreated(hoisted.mockAgent)}>
        Create
      </button>
      <button data-testid="coordinator-dismiss" onClick={onDismiss}>
        Dismiss
      </button>
    </div>
  ),
}))

// Mock ProviderOnboardingModal: with providers.length > 0 it never renders in
// our flow, but mock it so the real component (and its imports) never load.
vi.mock('./ProviderOnboardingModal', () => ({
  ProviderOnboardingModal: ({ onDismiss }: any) => (
    <div data-testid="provider-onboarding">
      <button data-testid="provider-dismiss" onClick={onDismiss}>
        Dismiss
      </button>
    </div>
  ),
}))

describe('OnboardingFlow', () => {
  let container: HTMLDivElement
  let root: Root

  const defaultProps = {
    hasCoordinator: false,
    agentsLoading: false,
    hasProjects: false,
    hasAgents: false,
    hasApps: false,
    onCoordinatorCreated: hoisted.onCoordinatorCreated,
  }

  const render = async (props: Partial<React.ComponentProps<typeof OnboardingFlow>> = {}) => {
    await act(async () => {
      root = createRoot(container)
      root.render(<OnboardingFlow {...defaultProps} {...props} />)
    })
    // Flush the provider → coordinator phase advance.
    await act(async () => {})
    await act(async () => {})
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    hoisted.showGuide.mockClear()
    hoisted.hideGuide.mockClear()
    hoisted.onCoordinatorCreated.mockClear()
    hoisted.t.mockClear()
    hoisted.invoke.mockClear()
    hoisted.invoke.mockResolvedValue({ Profile: { Capabilities: [] } })
    hoisted.loadAgent.mockClear()
  })

  afterEach(() => {
    act(() => root?.unmount())
    document.body.removeChild(container)
  })

  describe('post-coordinator guide tour', () => {
    it('calls guideManager.showGuide with the correct steps after coordinator creation', async () => {
      await render()
      // provider → coordinator phase should be active (provider configured).
      expect(container.querySelector('[data-testid="coordinator-setup"]')).toBeTruthy()

      // Simulate successful coordinator creation.
      await act(async () => {
        ;(container.querySelector('[data-testid="coordinator-create"]') as HTMLButtonElement).click()
      })
      await act(async () => {}) // flush guide → complete phase advance

      // showGuide fired exactly once, and the coordinator landed on its chat.
      expect(hoisted.showGuide).toHaveBeenCalledOnce()
      expect(hoisted.onCoordinatorCreated).toHaveBeenCalledWith(hoisted.mockAgent)

      // The steps passed to showGuide must equal the real buildBasicsTutorial(t, ctx)
      // output (t echoes keys), proving the wired tour is the canonical one.
      const expected = buildBasicsTutorial((k: string) => k, { hasProjects: false, hasAgents: false, hasApps: false })
      const steps = hoisted.showGuide.mock.calls[0]![0]
      expect(steps).toEqual(expected)

      // Spot-check the tour structure: starts at the sidebar toggle (B0),
      // includes the permission-mode gate, and ends on the finish step inside
      // the now-open settings panel (B7).
      expect(steps[0].TargetGuideId).toBe(GuideIds.topbar_sidebar_toggle)
      expect(steps.some((s: any) => s.TargetGuideId === GuideIds.sidebar_new_chat)).toBe(true)
      expect(steps[steps.length - 1].TargetGuideId).toBe(GuideIds.settings_sidebar)
    })

    it('does not auto-trigger the tour when the profile marks it as completed/skipped', async () => {
      // 进度持久化: a persisted `onboarding.tour` capability in the guidance
      // profile must suppress auto-triggering the tour on a later mount.
      hoisted.invoke.mockResolvedValue({
        Profile: { Capabilities: [{ Capability: 'onboarding.tour' }] },
      })

      await render({ hasCoordinator: true, coordinatorAgentId: 'coord-1' })
      await act(async () => {})

      // The coordinator actor is ensured live before the query runs; the query
      // must target the ActorId returned by workspace.load_agent, not the
      // session default (lazy agents would otherwise fail "service not found").
      expect(hoisted.loadAgent).toHaveBeenCalledWith(expect.anything(), { AgentId: 'coord-1' })
      expect(hoisted.invoke).toHaveBeenCalledWith(
        'coordinator_guidance_profile_query',
        { Account: '' },
        expect.objectContaining({ target: 'actor-1' }),
      )
      expect(hoisted.showGuide).not.toHaveBeenCalled()
    })

    it('skips the profile query entirely when no coordinator exists', async () => {
      // The callable only lives on coordinator-kind agents; querying without
      // one errors ("call ID not registered") — the regression the user hit.
      await render()
      await act(async () => {})

      expect(hoisted.loadAgent).not.toHaveBeenCalled()
      expect(hoisted.invoke).not.toHaveBeenCalled()
    })

    it('does not re-trigger the tour on re-render', async () => {
      await render()
      await act(async () => {
        ;(container.querySelector('[data-testid="coordinator-create"]') as HTMLButtonElement).click()
      })
      await act(async () => {})
      expect(hoisted.showGuide).toHaveBeenCalledOnce()

      // Force a re-render with the same props. The tourShown flag persists
      // (same component instance / position), so the tour must NOT fire again.
      await act(async () => {
        root.render(<OnboardingFlow {...defaultProps} />)
      })
      await act(async () => {})
      await act(async () => {})

      expect(hoisted.showGuide).toHaveBeenCalledOnce()
    })
  })

  describe('dismissal / skip', () => {
    it('dismisses the coordinator setup without starting the tour', async () => {
      await render()
      expect(container.querySelector('[data-testid="coordinator-setup"]')).toBeTruthy()

      await act(async () => {
        ;(container.querySelector('[data-testid="coordinator-dismiss"]') as HTMLButtonElement).click()
      })
      await act(async () => {})

      // Coordinator modal unmounted; tour never fired.
      expect(container.querySelector('[data-testid="coordinator-setup"]')).toBeNull()
      expect(hoisted.showGuide).not.toHaveBeenCalled()
    })

    it('skips the coordinator setup entirely when a coordinator already exists', async () => {
      await render({ hasCoordinator: true })
      await act(async () => {})

      expect(container.querySelector('[data-testid="coordinator-setup"]')).toBeNull()
      expect(container.querySelector('[data-testid="provider-onboarding"]')).toBeNull()
      expect(hoisted.showGuide).not.toHaveBeenCalled()
    })
  })
})
