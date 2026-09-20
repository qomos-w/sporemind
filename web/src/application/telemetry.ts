/**
 * Telemetry collector — captures user interactions annotated with data-guide-id.
 *
 * Usage:
 *   import { initTelemetry, reportInteraction } from './telemetry'
 *
 *   // Initialize once at app startup:
 *   initTelemetry()
 *
 *   // Manual reporting (e.g., navigation):
 *   reportInteraction('navigate', '', { view: 'topology', detail: 'topology' })
 */
import { client } from './generated-client'
import { reportInteraction as rpc } from '../gen-clients/interfacemanager/client'

/** Label truncation limit for privacy */
const LABEL_MAX_LENGTH = 50

export type InteractionKind = 'click' | 'input' | 'submit' | 'navigate' | 'guide_error' | 'remote_ack'

export interface InteractionPayload {
  kind: InteractionKind
  guideId: string
  label?: string
  view?: string
  detail?: string
  /**
   * Interaction text (e.g. submitted composer content). Only ever delivered to
   * in-process listeners — never sent to the backend. Optional, and callers
   * should usually keep it short (e.g. the first token of a slash command).
   */
  text?: string
}

interface ReportExtra {
  label?: string
  view?: string
  detail?: string
}

/**
 * Extract the label from an element:
 * - Prefer aria-label
 * - Form elements (input/textarea/select/contenteditable): never use
 *   textContent — it can hold user-typed content. Fall back to title or
 *   placeholder instead.
 * - Other elements: fall back to truncated textContent (max 50 chars)
 */
function extractLabel(el: Element): string {
  const aria = el.getAttribute('aria-label')
  if (aria && aria.trim().length > 0) return aria.trim().slice(0, LABEL_MAX_LENGTH)

  const tag = el.tagName.toLowerCase()
  const isFormField =
    tag === 'input' || tag === 'textarea' || tag === 'select' || (el as HTMLElement).isContentEditable
  if (isFormField) {
    const fallback = el.getAttribute('title') ?? el.getAttribute('placeholder') ?? ''
    return fallback.trim().slice(0, LABEL_MAX_LENGTH)
  }

  const text = el.textContent?.trim() ?? ''
  return text.slice(0, LABEL_MAX_LENGTH)
}

/**
 * Determine the control type for change events.
 * Returns the HTML tag name in lowercase, or the input type.
 * Does NOT return the value — values are intentionally excluded for privacy.
 */
function extractChangeDetail(el: Element): string {
  const tag = el.tagName.toLowerCase()
  if (tag === 'input') {
    return (el as HTMLInputElement).type ?? 'text'
  }
  if (tag === 'select') return 'select'
  if (tag === 'textarea') return 'textarea'
  return tag
}

/** Track the current content view for telemetry context */
let currentView: string = ''

/**
 * Set the current view context.
 * Called by AIShellLayout when the content mode changes.
 */
export function setTelemetryView(view: string): void {
  currentView = view
}

/**
 * In-process interaction listeners (used by guide-manager to auto-advance
 * gated tour steps). Payloads observed here never leave the process — user
 * text is delivered to these listeners only, never to the backend.
 */
type InteractionListener = (payload: InteractionPayload) => void
const interactionListeners = new Set<InteractionListener>()

/** Subscribe to in-process interaction events. Returns an unsubscribe fn. */
export function onInteraction(listener: InteractionListener): () => void {
  interactionListeners.add(listener)
  return () => interactionListeners.delete(listener)
}

function notifyListeners(payload: InteractionPayload): void {
  for (const listener of interactionListeners) {
    try {
      listener(payload)
    } catch {
      // A listener must never break telemetry collection.
    }
  }
}

/**
 * Emit a local-only interaction event. Delivered to in-process listeners (for
 * tour gating) but NOT sent to the backend — use for events that carry user
 * text (e.g. submitted composer content) which must stay private.
 */
export function emitInteraction(payload: InteractionPayload): void {
  notifyListeners(payload)
}

/**
 * Manually report an interaction.
 * Used for navigate events and by guide-manager for guide errors.
 */
export function reportInteraction(
  kind: InteractionKind,
  guideId: string,
  extra?: ReportExtra & { text?: string }
): void {
  const { text, ...safe } = extra ?? {}
  notifyListeners({ kind, guideId, text, ...safe })
  rpc(client, {
    Kind: kind,
    GuideId: guideId,
    Label: safe.label,
    View: safe.view ?? currentView,
    Detail: safe.detail,
  }).catch(() => {
    // Fire-and-forget — silent drop on failure
  })
}

/** Remove event listeners on cleanup */
let clickCleanup: (() => void) | null = null
let changeCleanup: (() => void) | null = null

/**
 * Initialize global telemetry listeners.
 * Attaches capture-phase listeners to document for click and change events.
 * Call once at app startup (e.g., from AIShellLayout).
 */
export function initTelemetry(): void {
  if (clickCleanup) return // Already initialized

  // Capture phase: runs before bubble, so we catch events before React handlers
  const handleClick = (e: MouseEvent) => {
    const target = e.target as Element | null
    if (!target) return
    const annotated = target.closest('[data-guide-id]')
    if (!annotated) return // No annotation = drop (noise reduction)

    const guideId = annotated.getAttribute('data-guide-id') ?? ''
    const label = extractLabel(annotated)

    reportInteraction('click', guideId, { label })
  }

  const handleChange = (e: Event) => {
    const target = e.target as Element | null
    if (!target) return
    const annotated = target.closest('[data-guide-id]')
    if (!annotated) return

    const guideId = annotated.getAttribute('data-guide-id') ?? ''
    const label = extractLabel(annotated)
    const detail = extractChangeDetail(target)

    reportInteraction('input', guideId, { label, detail })
  }

  document.addEventListener('click', handleClick, true)
  document.addEventListener('change', handleChange, true)

  clickCleanup = () => document.removeEventListener('click', handleClick, true)
  changeCleanup = () => document.removeEventListener('change', handleChange, true)
}

/**
 * Cleanup telemetry listeners.
 * Primarily for testing; not required in production.
 */
export function destroyTelemetry(): void {
  clickCleanup?.()
  changeCleanup?.()
  clickCleanup = null
  changeCleanup = null
  currentView = ''
  interactionListeners.clear()
}
