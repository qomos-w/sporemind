import React, { useEffect, useState } from 'react'
import { AnchoredOverlay } from '../overlay/AnchoredOverlay'
import { Spotlight } from '../overlay/Spotlight'
import { resolveAnchor, useAnchorRect } from '../overlay/anchor'
import type { Rect } from '../overlay/placement'
import type { GuideStep } from '../../../gen-types/interfacemanager'
import { guideManager } from '../../../application/guide-manager'
import { useI18n } from '../../../i18n'
import { useBrowserOverlay } from '../browserOverlay'
import './GuideOverlay.css'

export interface GuideOverlayProps {
  /** Guide steps for the current tour. */
  steps: GuideStep[]
  /** Current step index. */
  currentIndex: number
  /** Whether the overlay is visible. */
  visible: boolean
  /** Whether the current step's action gate has been satisfied. */
  gateMet?: boolean
  /** Category label for the tutorial (shown in the step counter). */
  categoryLabel?: string
  /** Called when the user advances to the next step. */
  onNext?: () => void
  /** Called when the user goes back a step. */
  onPrev?: () => void
  /** Called when the user completes the tour (clicks Done on the last step). */
  onComplete?: () => void
  /** Called when the user skips/cancels the tour. */
  onSkip?: () => void
}

function getSide(
  placement: string | undefined,
): 'top' | 'bottom' | 'left' | 'right' {
  switch (placement) {
    case 'top':
      return 'top'
    case 'bottom':
      return 'bottom'
    case 'left':
      return 'left'
    case 'right':
      return 'right'
    default:
      // auto: let the placement engine pick/flip from a bottom default.
      return 'bottom'
  }
}

/**
 * Extract the guide id of a gated click target from ExpectedInteraction.
 * Tokens look like `click:<guideId>` (comma-separated unions allowed).
 * When the gate points at an element OTHER than the step target, the
 * target is a container trigger (e.g. a dropdown whose items are gated):
 * the card must not sit where the container expands.
 */
function gateChildId(step: GuideStep): string | null {
  const gate = step.ExpectedInteraction
  if (!gate) return null
  for (const token of gate.split(',')) {
    const m = token.trim().match(/^click:(.+)$/)
    if (m && m[1] && m[1] !== step.TargetGuideId) {
      return m[1]
    }
  }
  return null
}

export const GuideOverlay: React.FC<GuideOverlayProps> = ({
  steps,
  currentIndex,
  visible,
  gateMet = true,
  categoryLabel,
  onNext,
  onPrev,
  onComplete,
  onSkip,
}) => {
  const { t } = useI18n()
  const [targetMissing, setTargetMissing] = useState(false)
  const [targetEl, setTargetEl] = useState<HTMLElement | null>(null)

  // Register with the browser overlay manager so native browser windows
  // are hidden while the guide card is visible (ref-counted).
  useBrowserOverlay(visible)

  /** Resolve the step title: prefer the I18nTitleKey, fall back to raw Title. */
  const resolveTitle = (step: GuideStep): string => {
    if (step.I18nTitleKey) {
      return t(step.I18nTitleKey as any)
    }
    return step.Title
  }

  /** Resolve the step body: prefer the I18nBodyKey, fall back to raw Body. */
  const resolveBody = (step: GuideStep): string => {
    if (step.I18nBodyKey) {
      return t(step.I18nBodyKey as any)
    }
    return step.Body
  }

  const currentStep =
    !visible || steps.length === 0 || currentIndex < 0 || currentIndex >= steps.length
      ? null
      : steps[currentIndex]

  // Asynchronously resolve the anchor target for telemetry and missing-target UI.
  // A timeout resolves to null, which triggers the existing targetMissing path.
  // Resolution retries every 2s while the anchor stays unresolved: targets
  // inside collapsed containers (sidebar, panels) or unmounted views can
  // appear mid-step, and the ring + card should jump to them when they do.
  useEffect(() => {
    if (!currentStep) {
      setTargetMissing(false)
      setTargetEl(null)
      return
    }
    let cancelled = false
    let retry: ReturnType<typeof setInterval> | null = null
    let missingReported = false
    setTargetMissing(false)
    setTargetEl(null)
    const attempt = () => {
      resolveAnchor(`guide:${currentStep.TargetGuideId}`).then((el) => {
        if (cancelled) return
        if (el) {
          if (retry) {
            clearInterval(retry)
            retry = null
          }
          setTargetEl(el)
          setTargetMissing(false)
          return
        }
        if (!missingReported) {
          missingReported = true
          setTargetMissing(true)
          void guideManager.reportTargetMissing(
            currentStep.TargetGuideId,
            resolveTitle(currentStep),
          )
        }
        if (!retry) {
          retry = setInterval(attempt, 2000)
        }
      })
    }
    attempt()
    return () => {
      cancelled = true
      if (retry) {
        clearInterval(retry)
      }
    }
  }, [currentStep, t])

  // Live geometry of the highlighted target — drives the Spotlight ring.
  // useAnchorRect tracks resize/scroll, so a target that becomes visible
  // mid-step (sidebar expanded, panel opened) is picked up automatically.
  const targetRect = useAnchorRect(targetEl)
  const targetVisible = targetRect.width > 0 || targetRect.height > 0
  // Anchored = resolved AND laid out. An element hidden inside a collapsed
  // container resolves but reports a zero rect: it must be treated as
  // unreachable, not anchored at (0,0).
  const anchored = !targetMissing && targetVisible
  const spotlightOpen = visible && anchored
  // When not anchored, place the card at the bottom-center of the viewport
  // instead of letting a zero rect clamp it into the top-left corner.
  const fallbackRect = anchored
    ? null
    : {
        x: window.innerWidth / 2,
        y: window.innerHeight - 16,
        width: 0,
        height: 0,
      }

  // Container-expansion avoidance: when the gate waits for a click inside a
  // container (dropdown/panel) that expands from the target, track that
  // child's live rect and place the card beside the target+container union
  // — never underneath the expanding menu. Polled rather than resolved
  // once: menus mount/unmount as they open and close, and the card should
  // follow both directions.
  const expandChild = currentStep ? gateChildId(currentStep) : null
  const [avoidRect, setAvoidRect] = useState<Rect | null>(null)
  useEffect(() => {
    if (!expandChild) {
      setAvoidRect(null)
      return
    }
    let cancelled = false
    let last = ''
    const poll = () => {
      if (cancelled) return
      const el = document.querySelector<HTMLElement>(
        `[data-guide-id="${expandChild}"]`,
      )
      const r = el ? el.getBoundingClientRect() : null
      const next = r
        ? `${r.x},${r.y},${r.width},${r.height}`
        : ''
      if (next !== last) {
        last = next
        setAvoidRect(
          r && (r.width > 0 || r.height > 0)
            ? { x: r.x, y: r.y, width: r.width, height: r.height }
            : null,
        )
      }
    }
    poll()
    const timer = setInterval(poll, 400)
    return () => {
      cancelled = true
      clearInterval(timer)
    }
  }, [expandChild])
  // Gate-on-child means the target spawns an expanding container (usually
  // downward): side placement keeps the card out of the expansion zone.
  const cardSide = expandChild
    ? 'right'
    : getSide(currentStep?.Placement)

  if (!currentStep) {
    return null
  }

  const step = currentStep
  const isLast = currentIndex === steps.length - 1
  const hasMultiple = steps.length > 1
  const progress = hasMultiple ? ((currentIndex + 1) / steps.length) * 100 : 100
  const gated = !isLast && !!step.ExpectedInteraction && !gateMet

  return (
    <>
      {/* Highlight the target with a non-blocking ring. The ring is
          paint-only, so the user can click the target — and anything
          else — freely; the guide observes completion via interaction
          gating, not by trapping clicks. */}
      <Spotlight open={spotlightOpen} target={targetRect} />
      <AnchoredOverlay
      open={visible}
      anchor={`guide:${step.TargetGuideId}`}
      side={cardSide}
      align="center"
      offset={8}
      viewportPadding={8}
      fallbackRect={fallbackRect}
      avoidRect={avoidRect}
      className={`guide-card${anchored ? '' : ' guide-card--missing'}`}
    >
      {/* Progress bar */}
      {hasMultiple && (
        <div
          className="guide-card-progress"
          role="progressbar"
          aria-valuenow={currentIndex + 1}
          aria-valuemin={1}
          aria-valuemax={steps.length}
        >
          <div className="guide-card-progress-fill" style={{ width: `${progress}%` }} />
        </div>
      )}

      <div className="guide-card-header">
        <span className="guide-card-title">{resolveTitle(step)}</span>
        {hasMultiple && (
          <span className="guide-card-counter">
            {categoryLabel ? `${categoryLabel} · ` : ''}
            {t('onboarding.guide.step', { step: currentIndex + 1, total: steps.length })}
          </span>
        )}
      </div>

      <p className="guide-card-body">{resolveBody(step)}</p>

      {!anchored && (
        <p className="guide-card-target-missing">
          {t('onboarding.guide.targetMissing', { guideId: step.TargetGuideId })}
        </p>
      )}

      {gated && (
        <p className="guide-card-gate-hint">
          {t('onboarding.guide.gateHint')}
        </p>
      )}

      <div className="guide-card-actions">
        {currentIndex > 0 && (
          <button
            type="button"
            className="guide-btn guide-btn--secondary"
            onClick={onPrev}
          >
            {t('onboarding.guide.back')}
          </button>
        )}
        {isLast ? (
          <button
            type="button"
            className="guide-btn guide-btn--primary"
            onClick={onComplete ?? onSkip}
          >
            {t('onboarding.guide.done')}
          </button>
        ) : (
          <button
            type="button"
            className="guide-btn guide-btn--primary"
            onClick={onNext}
            disabled={gated}
            title={gated ? t('onboarding.guide.gateHint') : undefined}
          >
            {t('onboarding.guide.next')}
          </button>
        )}
        <button
          type="button"
          className="guide-btn guide-btn--skip"
          onClick={onSkip}
        >
          {t('onboarding.guide.skip')}
        </button>
      </div>
    </AnchoredOverlay>
    </>
  )
}
