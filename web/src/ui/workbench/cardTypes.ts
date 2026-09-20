import type { ReactNode } from 'react'

/**
 * Workbench card base contract (spec §2).
 *
 * The workbench renders every surface (app / plugin / terminal / chat / file /
 * note) through a single descriptor contract: a compact host-metadata card plus
 * an expanded body. React has no inheritance, so the "common parent" is
 * enforced by this contract + the {@link WorkbenchCardRegistry} — builtin and
 * plugin cards both register the same shape.
 */
export type WorkbenchCardKind = 'app' | 'plugin' | 'terminal' | 'chat' | 'file' | 'note'

/** Non-focus card metadata: a one-line status summary. */
export interface WorkbenchCardCompactMeta {
  statusText: string
}

export interface WorkbenchCardDescriptor {
  id: string
  kind: WorkbenchCardKind
  title: string
  /** Host icon name (see appmanager.icon_names / cardIconLibrary). */
  icon: string
  /** Optional accent color override for the tile ring. */
  color?: string
  /** Attention score (spec §3/§4). */
  score: number
  /** Pinned cards never get displaced by the hysteresis ordering. */
  pinned: boolean
  /**
   * Optional hover attribution line (score / why), mirroring the reference
   * `.attr`. Populated from the workbench projection when the board is
   * projection-driven; omitted for locally-derived descriptors.
   */
  attribution?: string
  compactMeta: WorkbenchCardCompactMeta
  /** Render the card body. `expanded` true = focused/main slot, false = compact. */
  render: (expanded: boolean) => ReactNode
}
