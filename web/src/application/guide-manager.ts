/**
 * Guide manager — singleton tour state for the guide overlay.
 *
 * Usage:
 *   import { guideManager } from './guide-manager'
 *   guideManager.showGuide(steps)  // starts/resumes a tour
 *   guideManager.hideGuide()       // dismisses
 *
 * The manager is consumed by GuideOverlay inside AIShellLayout.
 *
 * Action gating (D1): a step may declare `ExpectedInteraction` (e.g.
 * `click:composer.permission-mode`, `text:/workflow`, `submit`, or a
 * comma-separated union). While unfulfilled the Next button is disabled;
 * when the matching interaction is observed via the in-process telemetry
 * stream the tour auto-advances. Skip stays available so a missing gate
 * can never deadlock the user.
 *
 * Capability reporting (D1): every completed step reports its
 * `CapabilityKind` semantic domain to the coordinator guidance profile
 * (permission/conversation/workflow/tools), and the tour itself persists a
 * top-level `onboarding.tour` entry (completed or skipped) so OnboardingFlow
 * can decide whether to auto-trigger on later mounts.
 */
import type { GuideStep } from '../gen-types/interfacemanager'
import { client } from './generated-client'
import * as guidanceClient from '../gen-clients/local/client'
import * as interfaceClient from '../gen-clients/interfacemanager/client'
import { onInteraction, type InteractionPayload } from './telemetry'

type GuideListener = (state: GuideState) => void
type TourFinishedListener = () => void

export interface GuideState {
  steps: GuideStep[]
  currentIndex: number
  visible: boolean
  /** Whether the current step's ExpectedInteraction has been observed. */
  gateMet: boolean
  /** Whether this guide is the onboarding tour (affects persistence). */
  isTour: boolean
  /** Category label for the tutorial (shown in the step counter). */
  categoryLabel?: string
  /** Whether finishing this tour should trigger the post-tour tool guide. */
  triggerToolGuide?: boolean
  /** Guide origin: builtin (static onboarding/tutorials) vs agent-driven external. */
  source?: 'builtin' | 'external'
}

/** Tour persistence capability name (see D1 progress persistence). */
export const TOUR_CAPABILITY = 'onboarding.tour'

/**
 * Pure gate matcher. A step's `ExpectedInteraction` may be a single spec or a
 * comma-separated union; ANY alternative matching the event satisfies the gate.
 * Spec forms:
 *  - `click:<guide-id>`  — a click interaction on that guide id
 *  - `text:<substring>`  — submit/input whose text contains the substring
 *  - `submit`            — any submit interaction
 * Returns true when the spec is absent/empty (ungated step).
 */
export function matchesExpectedInteraction(
  spec: string | undefined,
  ev: InteractionPayload
): boolean {
  if (!spec || spec.trim() === '') return true
  const alts = spec.split(',')
  for (const alt of alts) {
    const idx = alt.indexOf(':')
    const type = idx >= 0 ? alt.slice(0, idx) : alt
    const value = idx >= 0 ? alt.slice(idx + 1) : ''
    switch (type) {
      case 'click':
        if (ev.kind === 'click' && ev.guideId === value) return true
        break
      case 'text':
        if (
          (ev.kind === 'submit' || ev.kind === 'input') &&
          (ev.text ?? '').includes(value)
        ) {
          return true
        }
        break
      case 'submit':
        if (ev.kind === 'submit') return true
        break
    }
  }
  return false
}

class GuideManager {
  private state: GuideState = {
    steps: [],
    currentIndex: 0,
    visible: false,
    gateMet: true,
    isTour: false,
  }
  private listeners = new Set<GuideListener>()
  private tourFinishedListeners = new Set<TourFinishedListener>()
  private telemetryUnsub: (() => void) | null = null

  private emit() {
    for (const l of this.listeners) l(this.state)
  }

  subscribe(listener: GuideListener): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  getState(): Readonly<GuideState> {
    return this.state
  }

  /** Fire a guidance-profile increment (fire-and-forget). */
  private reportCapability(capability: string, signal: string, detail?: string): void {
    guidanceClient
      .coordinatorGuidanceProfileIncrement(client, {
        Account: '',
        Capability: capability,
        Signal: signal,
        Evidence: detail,
      })
      .catch((err) => {
        console.warn(`[guide-manager] failed to report capability ${capability}:`, err)
      })
  }

  /**
   * Report external-guide progress into the interaction ring buffer
   * (fire-and-forget) so a coordinator can pull it via query_interactions.
   * Only agent-driven (external) guides report; builtin tutorials keep their
   * existing capability/onboarding.tour semantics untouched.
   */
  private reportGuideProgress(signal: 'step_completed' | 'tour_completed' | 'tour_skipped'): void {
    if (this.state.source !== 'external' || !this.state.visible) return
    const step = this.state.steps[this.state.currentIndex]
    if (!step) return
    interfaceClient.reportInteraction(client, {
      Kind: 'guide_progress',
      GuideId: step.TargetGuideId,
      Detail: `${this.state.currentIndex + 1}/${this.state.steps.length} ${step.TargetGuideId} ${signal}`,
    }).catch((err) => {
      console.warn('[guide-manager] failed to report guide progress:', err)
    })
  }

  /** Lazily attach the in-process interaction listener once. */
  private ensureTelemetryListener(): void {
    if (this.telemetryUnsub) return
    this.telemetryUnsub = onInteraction((ev) => this.handleInteraction(ev))
  }

  private handleInteraction(ev: InteractionPayload): void {
    if (!this.state.visible) return
    if (this.state.gateMet) return
    const step = this.state.steps[this.state.currentIndex]
    if (!step) return
    if (matchesExpectedInteraction(step.ExpectedInteraction, ev)) {
      // Gate satisfied: mark it met and auto-advance.
      this.state = { ...this.state, gateMet: true }
      this.emit()
      this.nextStep()
    }
  }

  private recomputeGate(): void {
    const step = this.state.steps[this.state.currentIndex]
    this.state = {
      ...this.state,
      gateMet: !step?.ExpectedInteraction || step.ExpectedInteraction.trim() === '',
    }
  }

  showGuide(steps: GuideStep[], isTour = false, categoryLabel?: string, opts?: { triggerToolGuide?: boolean; source?: 'builtin' | 'external' }) {
    if (steps.length === 0) return
    this.ensureTelemetryListener()
    this.state = {
      steps,
      currentIndex: 0,
      visible: true,
      gateMet: true,
      isTour,
      categoryLabel,
      triggerToolGuide: opts?.triggerToolGuide,
      source: opts?.source ?? 'builtin',
    }
    this.recomputeGate()
    this.emit()
  }

  /**
   * Replay the tour from the settings panel. Identical to showGuide with the
   * tour flag on — it does NOT touch the `onboarding.tour` profile entry, so
   * the user can re-watch the tour as often as they like (D1 replay
   * requirement).
   */
  replayGuide(steps: GuideStep[], categoryLabel?: string) {
    this.showGuide(steps, true, categoryLabel)
  }

  /**
   * Register a one-shot-style callback that fires when a tour marked with
   * `triggerToolGuide` finishes (completed or skipped). Used by AIShellLayout
   * to auto-open the full-screen tool guide after the onboarding tour.
   */
  onTourFinished(listener: TourFinishedListener): () => void {
    this.tourFinishedListeners.add(listener)
    return () => {
      this.tourFinishedListeners.delete(listener)
    }
  }

  private emitTourFinished(): void {
    if (!this.state.isTour || !this.state.triggerToolGuide) return
    for (const l of this.tourFinishedListeners) {
      try {
        l()
      } catch (err) {
        console.warn('[guide-manager] tour-finished listener error:', err)
      }
    }
  }

  hideGuide() {
    if (!this.state.visible) return
    this.state = { ...this.state, visible: false }
    this.emit()
  }

  /**
   * Mark the current guide as completed (user reached the last step).
   * Reports the final step's capability domain and persists the tour-level
   * `onboarding.tour` completion entry to the coordinator guidance profile.
   */
  async completeGuide(): Promise<void> {
    // External-guide progress goes into the interaction ring buffer before
    // the guide is hidden (visibility is part of the report precondition).
    this.reportGuideProgress('tour_completed')
    if (!this.state.isTour) {
      this.hideGuide()
      return
    }
    const current = this.state.steps[this.state.currentIndex]
    if (current?.CapabilityKind) {
      this.reportCapability(current.CapabilityKind, 'guide_completed', `step:${current.TargetGuideId}`)
    }
    this.hideGuide()
    this.reportCapability(TOUR_CAPABILITY, 'guide_completed')
    this.emitTourFinished()
  }

  /**
   * Dismiss the tour (Skip). Persists tour-level `onboarding.tour` dismissal
   * so OnboardingFlow won't auto-trigger it again.
   */
  async skipGuide(): Promise<void> {
    // Report before hiding: visibility is part of the report precondition.
    this.reportGuideProgress('tour_skipped')
    this.hideGuide()
    if (this.state.isTour) {
      this.reportCapability(TOUR_CAPABILITY, 'guide_skipped')
      this.emitTourFinished()
    }
  }

  nextStep() {
    if (!this.state.visible) return
    const current = this.state.steps[this.state.currentIndex]
    // Per-step capability reporting (D1): only for the onboarding tour and
    // only when the step has a semantic concept domain.
    if (this.state.isTour && current?.CapabilityKind) {
      this.reportCapability(current.CapabilityKind, 'step_completed', `step:${current.TargetGuideId}`)
    }
    this.reportGuideProgress('step_completed')
    const next = this.state.currentIndex + 1
    if (next < this.state.steps.length) {
      this.state = { ...this.state, currentIndex: next }
      this.recomputeGate()
      this.emit()
    } else {
      // Reached the end — signal completion
      this.completeGuide()
    }
  }

  prevStep() {
    if (!this.state.visible) return
    if (this.state.currentIndex > 0) {
      this.state = { ...this.state, currentIndex: this.state.currentIndex - 1 }
      this.recomputeGate()
      this.emit()
    }
  }

  /**
   * Report that a target guide-id is not (yet) visible in the DOM.
   * Called by GuideOverlay when a step's target is missing.
   *
   * Reported as `guide_progress`, not `guide_error`: a hidden target is
   * normal pacing while a sequenced tutorial waits for the user to open
   * the container (collapsed sidebar, closed panel, unopened menu) — the
   * overlay tells the user what to do and re-resolves until it appears.
   * Reporting it as an error made coordinators treat normal pacing as
   * failure and nag the user about "not opening the panel".
   */
  async reportTargetMissing(guideId: string, stepTitle: string): Promise<void> {
    try {
      await interfaceClient.reportInteraction(client, {
        Kind: 'guide_progress',
        GuideId: guideId,
        Detail: `target_missing: guide target "${guideId}" not visible yet (step: ${stepTitle}) — waiting for the user to open the container`,
      })
    } catch (err) {
      console.warn('[guide-manager] failed to report target missing:', err)
    }
  }
}

export const guideManager = new GuideManager()