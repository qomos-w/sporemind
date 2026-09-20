import { useEffect, useRef, useState } from 'react'
import type { AgentRef } from '../../../gen-clients/system/types'
import { client } from '../../../application/generated-client'
import * as guidanceClient from '../../../gen-clients/local/client'
import * as workspaceClient from '../../../gen-clients/workspace/client'
import { useProviderConfigs } from '../hooks/useProviderConfigs'
import { useI18n } from '../../../i18n'
import { ProviderOnboardingModal } from './ProviderOnboardingModal'
import { CoordinatorSetupModal } from './CoordinatorSetupModal'
import { buildBasicsTutorial } from '../../../application/onboarding-guide'
import type { TutorialContext } from '../../../application/tutorial-registry'
import { guideManager, TOUR_CAPABILITY } from '../../../application/guide-manager'

/**
 * Onboarding phases, walked in order:
 *   provider    → configure a model provider (modal self-hides once one exists)
 *   coordinator → create the global coordinator agent
 *   guide       → coordinator just created — fire the post-coordinator tour
 *   complete    → onboarding finished (tour fired or skipped)
 */
type OnboardingPhase = 'provider' | 'coordinator' | 'guide' | 'complete'

interface OnboardingFlowProps {
  /** True when a global coordinator agent already exists. */
  hasCoordinator: boolean
  /** AgentId (e.g. "W#12") of the global coordinator; used to load it before
   *  routing the guidance-profile query to its actor. */
  coordinatorAgentId?: string
  /** True while the agent list is still loading from the backend. */
  agentsLoading: boolean
  /** True when at least one project exists (workflow requires a project). */
  hasProjects: boolean
  /** True when at least one non-system agent exists. */
  hasAgents: boolean
  /** True when at least one installed app surfaces a launcher tile. */
  hasApps: boolean
  /** Called once the coordinator is created; lands the user on its chat. */
  onCoordinatorCreated: (agent: AgentRef) => void
}

/**
 * Orchestrates first-run onboarding as a multi-step state machine:
 *   1. ProviderOnboardingModal — renders while no provider is configured.
 *   2. CoordinatorSetupModal   — renders once a provider exists but no global
 *                                coordinator agent has been created yet.
 *   3. Post-coordinator guide tour (Steps 3-5) — fired exactly once via
 *      guideManager.showGuide after the coordinator is created.
 *
 * The guide tour is rendered by GuideOverlay in AIShellLayout, which subscribes
 * to guideManager, so OnboardingFlow only needs to call showGuide once.
 */
export function OnboardingFlow({ hasCoordinator, coordinatorAgentId, agentsLoading, hasProjects, hasAgents, hasApps, onCoordinatorCreated }: OnboardingFlowProps) {
  const { providers, loading } = useProviderConfigs()
  const { t } = useI18n()

  const [phase, setPhase] = useState<OnboardingPhase>('provider')
  const [dismissed, setDismissed] = useState(false)
  // Tour-shown flag — React state only (no localStorage). The persisted tour
  // state lives in the coordinator guidance profile; this flag just prevents
  // re-triggering the tour within a single mount.
  const [tourShown, setTourShown] = useState(false)
  // Reads persisted tour state from the backend so a completed/skipped tour is
  // not auto-triggered again after a reload. Defaults to false (auto-trigger)
  // while the query is in flight or when it fails.
  const [tourCompleted, setTourCompleted] = useState(false)
  const tourCompletedQueried = useRef(false)

  // Phase 0: query the coordinator guidance profile once for persisted tour
  // state (D1 进度持久化). A capability entry `onboarding.tour` means the user
  // already completed or skipped the tour — do not auto-trigger it again.
  // The callable only exists on live coordinator-kind agents. Agents are
  // lazily loaded, so the query first ensures the coordinator actor is spawned
  // (workspace.load_agent is idempotent) and then targets the returned
  // ActorId — an untargeted call lands on a non-coordinator cell, and a target
  // whose actor is not in the tree fails with "service not found".
  useEffect(() => {
    if (agentsLoading || tourCompletedQueried.current) return
    tourCompletedQueried.current = true
    if (!hasCoordinator || !coordinatorAgentId) {
      // No coordinator → no persisted profile; auto-trigger stays the default.
      return
    }
    workspaceClient
      .loadAgent(client, { AgentId: coordinatorAgentId })
      .then((ref) => {
        if (!ref.ActorId) return null
        return guidanceClient.coordinatorGuidanceProfileQuery(client, { Account: '' }, {
          target: ref.ActorId,
        })
      })
      .then((resp) => {
        if (!resp) return
        const done = (resp?.Profile?.Capabilities ?? []).some(
          (c) => c.Capability === TOUR_CAPABILITY,
        )
        setTourCompleted(done)
      })
      .catch((err) => {
        // Query failure must not block onboarding — treat as not completed
        // (auto-trigger stays the default).
        console.warn('[onboarding-flow] failed to query tour progress:', err)
      })
  }, [agentsLoading, hasCoordinator, coordinatorAgentId])

  // Phase 1 → 2: once a provider is configured, advance to coordinator setup.
  useEffect(() => {
    if (phase === 'provider' && !loading && !dismissed && providers.length > 0) {
      setPhase('coordinator')
    }
  }, [phase, loading, dismissed, providers])

  // Phase 3 → 4: fire the post-coordinator guide tour exactly once. The tour
  // targets DOM anchors that render in the main shell (unmounted while a modal
  // is up), so it is started after coordinator creation rather than from
  // within the modal. Skipped when the profile says the tour was already
  // completed or dismissed.
  useEffect(() => {
    if (phase !== 'guide' || tourShown) return
    setTourShown(true)
    setPhase('complete')
    if (tourCompleted) return
    const ctx: TutorialContext = { hasProjects, hasAgents, hasApps }
    guideManager.showGuide(buildBasicsTutorial(t, ctx), true, t('onboarding.tutorial.category.basics.label'), { triggerToolGuide: true })
  }, [phase, tourShown, t, tourCompleted, hasProjects, hasAgents, hasApps])

  // Re-arm the provider onboarding modal on demand: when a guard (opening an
  // agent / sending a message) fires 'sporemind:show-provider-onboarding',
  // reset to the provider phase and clear any prior dismissal. The modal
  // itself self-hides once a provider is configured, so spurious re-arms are
  // invisible when providers already exist.
  useEffect(() => {
    const handler = () => { setDismissed(false); setPhase('provider') }
    window.addEventListener('sporemind:show-provider-onboarding', handler)
    return () => window.removeEventListener('sporemind:show-provider-onboarding', handler)
  }, [])

  const showCoordinator =
    phase === 'coordinator' &&
    !dismissed &&
    !loading &&
    !agentsLoading &&
    providers.length > 0 &&
    !hasCoordinator

  return (
    <>
      {!dismissed && phase === 'provider' && (
        <ProviderOnboardingModal onDismiss={() => setDismissed(true)} />
      )}
      {showCoordinator && (
        <CoordinatorSetupModal
          onDismiss={() => setDismissed(true)}
          onCreated={(agent) => {
            setPhase('guide')
            onCoordinatorCreated(agent)
          }}
        />
      )}
    </>
  )
}
