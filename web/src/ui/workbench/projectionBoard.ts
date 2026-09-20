import { createElement } from 'react'
import type { WorkbenchCardState } from '../../gen-clients/system/types'
import { AppCardFallbackBody } from './appCard'
import type { WorkbenchCardDescriptor, WorkbenchCardKind } from './cardTypes'
import { TERMINAL_CARD_ID } from './terminal/ids'

const KNOWN_KINDS: readonly WorkbenchCardKind[] = ['app', 'plugin', 'terminal', 'chat', 'file', 'note']

/** Coerce a wire card kind into the card-base kind union (unknown → note). */
export function workbenchCardKind(kind: string): WorkbenchCardKind {
  return (KNOWN_KINDS as readonly string[]).includes(kind) ? (kind as WorkbenchCardKind) : 'note'
}

/**
 * Map a projected card to its frontend descriptor id. The actor shares the
 * descriptor id vocabulary (`app:<id>`, `agent:<id>`, `terminal`), so this is
 * the identity today; the mapping lives here so a future divergence has one
 * home. The actor reports the terminal card with `Kind === "terminal"` rather
 * than by id, so kind is authoritative for it.
 */
export function projectionCardId(state: WorkbenchCardState): string {
  return state.Kind === 'terminal' ? TERMINAL_CARD_ID : state.Id
}

/** Hover attribution line, mirroring the reference `.attr` (score · why). */
export function projectionAttribution(state: WorkbenchCardState): string {
  const score = String(Math.round(state.Score))
  return state.Why ? `${score} · ${state.Why}` : score
}

/**
 * Overlay the actor's attention (score / pinned flag / attribution) onto a
 * registered render descriptor. Presentation (title / icon / body) stays with
 * the descriptor; attention is always the projection's.
 */
export function withProjection(
  base: WorkbenchCardDescriptor,
  state: WorkbenchCardState,
): WorkbenchCardDescriptor {
  return {
    ...base,
    ...(state.Color ? { color: state.Color } : {}),
    score: state.Score,
    pinned: state.Pinned,
    attribution: projectionAttribution(state),
  }
}

/**
 * Synthesize a descriptor for a projected card that has no registered render
 * body (agent / chat / file cards the actor knows about). These render as
 * compact host-metadata tiles; the expanded body degrades to a status panel
 * (AppCardFallbackBody) instead of nothing — a promoted card must never show
 * an empty main slot.
 */
export function projectionOnlyDescriptor(state: WorkbenchCardState): WorkbenchCardDescriptor {
  return {
    id: projectionCardId(state),
    kind: workbenchCardKind(state.Kind),
    title: state.Title || state.Id,
    icon: state.Icon || '',
    ...(state.Color ? { color: state.Color } : {}),
    score: state.Score,
    pinned: state.Pinned,
    attribution: projectionAttribution(state),
    compactMeta: { statusText: state.StatusText ?? '' },
    render: expanded =>
      expanded
        ? createElement(AppCardFallbackBody, {
            state: state.StatusText || state.Title || state.Id,
          })
        : null,
  }
}
