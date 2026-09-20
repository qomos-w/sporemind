/**
 * Workbench layout engine (spec §3).
 *
 * Pure slot math ported from `blackboard-reference.html`: a centred main card,
 * a two-column side grid, a full-width terminal strip, and a right-edge rail
 * used while a card is captured (⚑). The board renders only a few canonical
 * layouts ({@link SIDE_SLOT_ROWS_MAX} side rows max) — see {@link BOARD_CAPACITY}.
 * Placement is stable across score drift by
 * a hysteresis threshold (a challenger must lead by more than
 * {@link LAYOUT_HYSTERESIS} to swap), and pinned cards always take the main
 * slot.
 *
 * Every coordinate is a percentage of the board field, exactly like the
 * reference implementation; the DOM layer turns them into absolute positions.
 */

export interface WorkbenchSlot {
  x: number
  y: number
  w: number
  h: number
}

export type WorkbenchSlotZone = 'main' | 'side' | 'term' | 'rail' | 'max'

/** Centred main card while the terminal strip is present (bottom 29% reserved). */
export const MAIN_SLOT: WorkbenchSlot = { x: 26, y: 3, w: 48, h: 61 }
/** Centred main card while the terminal strip is retreated (full height). */
export const MAIN_SLOT_NO_TERM: WorkbenchSlot = { x: 26, y: 3, w: 48, h: 93 }
/** Terminal strip: full width, always the bottom 29%. */
export const TERMINAL_SLOT: WorkbenchSlot = { x: 3, y: 67, w: 94, h: 29 }
/** Full-board capture with the terminal retreated. */
export const MAX_SLOT: WorkbenchSlot = { x: 0, y: 0, w: 82, h: 100 }
/** Full-board capture with the terminal present (compressed to the upper band). */
export const MAX_SLOT_WITH_TERM: WorkbenchSlot = { x: 0, y: 3, w: 82, h: 61 }

/** A challenger must lead by more than this to displace the current order. */
export const LAYOUT_HYSTERESIS = 15

/** Peer decay per promote (attention is shared), mirroring the actor's promoteDecay. */
export const LAYOUT_PROMOTE_DECAY = 8

/** Attention floor, mirroring the actor's ScoreFloor. */
export const LAYOUT_SCORE_FLOOR = 5

/**
 * Canonical board layouts. The board only ever renders one of a few fixed
 * compositions: a centred main card plus 0–{@link SIDE_SLOT_ROWS_MAX} full
 * two-column side rows (the terminal strip is always its own fixed slot). The
 * row height is a constant per row count, so the board swaps between these
 * layouts instead of rescaling card by card.
 *
 * Cards past the capacity are left unplaced by {@link computeLayout} — the
 * renderer drops unplaced cards. Appending one more would shrink every existing
 * card and break the page, so the board never grows an extra row.
 */
export const SIDE_SLOT_COLUMNS = 2
export const SIDE_SLOT_ROWS_MAX = 3
export const SIDE_SLOT_CAPACITY = SIDE_SLOT_COLUMNS * SIDE_SLOT_ROWS_MAX
/** Total non-terminal cards a board can show (main + side rows). */
export const BOARD_CAPACITY = 1 + SIDE_SLOT_CAPACITY
/** Same ceiling for the capture rail: cards past it stay off the board. */
export const RAIL_SLOT_CAPACITY = 6

/**
 * Two-column side grid filling the band. Rows are sized so `count` cards fit
 * the vertical span `band` with a 3% gutter, matching the reference; the row
 * count is capped at {@link SIDE_SLOT_ROWS_MAX} so the grid stays one of the
 * canonical layouts (extra cards are simply not placed).
 */
export function sideSlots(count: number, band: number): WorkbenchSlot[] {
  const n = Math.min(Math.max(0, count), SIDE_SLOT_CAPACITY)
  if (n === 0) return []
  const rows = Math.max(1, Math.ceil(n / SIDE_SLOT_COLUMNS))
  const h = (band - 3 * (rows - 1)) / rows
  const out: WorkbenchSlot[] = []
  for (let i = 0; i < n; i++) {
    out.push({
      x: i % SIDE_SLOT_COLUMNS === 0 ? 3 : 77,
      y: 3 + Math.floor(i / SIDE_SLOT_COLUMNS) * (h + 3),
      w: 20,
      h,
    })
  }
  return out
}

/** Right-edge rail slot: `count` cards share the vertical span `span`. */
export function railSlot(index: number, count: number, top: number, span: number): WorkbenchSlot {
  const step = span / Math.max(count, 1)
  return { x: 84, y: top + index * step, w: 16, h: Math.max(step - 3, 8) }
}

export interface StableOrderCard {
  id: string
  score: number
  pinned: boolean
}

/**
 * Hysteresis-stable ordering: keep the previous order, swapping adjacent
 * entries only when the lower one leads by more than {@link LAYOUT_HYSTERESIS}.
 * A pinned card is pulled to the front (and therefore into the main slot).
 */
export function stableOrder(
  cards: readonly StableOrderCard[],
  prevOrder: readonly string[] | null | undefined,
): string[] {
  const ids = cards.map(c => c.id)
  const byId = new Map(cards.map(c => [c.id, c] as const))
  const scoreOf = (id: string) => byId.get(id)?.score ?? 0

  const sorted = [...ids].sort((a, b) => scoreOf(b) - scoreOf(a))
  let order = prevOrder ? prevOrder.filter(id => ids.includes(id)) : []
  ids.forEach(id => {
    if (!order.includes(id)) order.push(id)
  })
  if (!order.length) order = sorted

  // Hysteresis-stable ordering: keep the previous order, swapping adjacent
  // entries only when the lower one leads by more than {@link LAYOUT_HYSTERESIS}.
  // Repeat until no swap fires so a promoted card reaches the front in one
  // layout pass instead of advancing a single position. Each swap strictly
  // decreases Σ score(order[j])·j, so the loop terminates.
  for (let changed = true; changed; ) {
    changed = false
    for (let i = 0; i < order.length - 1; i++) {
      if (scoreOf(order[i + 1]!) > scoreOf(order[i]!) + LAYOUT_HYSTERESIS) {
        const swapped = order[i + 1]!
        order[i + 1] = order[i]!
        order[i] = swapped
        changed = true
      }
    }
  }

  const pinned = ids.find(id => byId.get(id)?.pinned)
  if (pinned) {
    order = order.filter(id => id !== pinned)
    order.unshift(pinned)
  }
  return order
}

/** Layout input: the subset of a card descriptor the engine cares about. */
export interface LayoutCard {
  id: string
  score: number
  pinned: boolean
  /** Retreated cards (e.g. the idle terminal) are excluded from placement. */
  hidden?: boolean
  /** The terminal is placed in its dedicated strip, never in the side grid. */
  terminal?: boolean
}

export interface WorkbenchPlacement {
  id: string
  slot: WorkbenchSlot
  zone: WorkbenchSlotZone
  /** 0 = focused/main; higher = further from attention. */
  rank: number
}

export interface WorkbenchLayoutResult {
  placements: Record<string, WorkbenchPlacement>
  /** Hysteresis-stable order of the non-terminal visible cards (empty while captured). */
  order: string[]
  maximizedId: string | null
  /** Whether the terminal strip is on the board. */
  visibleTerminal: boolean
}

export interface ComputeLayoutOptions {
  /** Currently captured card (⚑ full capture); null = normal board. */
  maximizedId?: string | null
  /** Previous stable order, carried forward for hysteresis. */
  prevOrder?: readonly string[] | null
}

/**
 * Resolve every visible card to a slot. Mirrors the reference `layout()`:
 * captured card fills the board (or the upper band when the terminal is up),
 * the rest of the cards collapse onto the rail; otherwise the stable-order
 * leader takes the main slot, the remainder fill the side grid, and the
 * terminal docks to the bottom strip.
 */
export function computeLayout(
  cards: readonly LayoutCard[],
  options: ComputeLayoutOptions = {},
): WorkbenchLayoutResult {
  const byId = new Map(cards.map(c => [c.id, c] as const))
  const terminal = cards.find(c => c.terminal)
  const hasTerm = !!terminal && !terminal.hidden
  const visibleIds = cards.filter(c => !c.hidden).map(c => c.id)

  const requested = options.maximizedId ?? null
  const maximizedId = requested && visibleIds.includes(requested) ? requested : null

  const placements: Record<string, WorkbenchPlacement> = {}
  const place = (id: string, slot: WorkbenchSlot, zone: WorkbenchSlotZone, rank: number) => {
    placements[id] = { id, slot, zone, rank }
  }

  if (maximizedId) {
    place(maximizedId, hasTerm ? MAX_SLOT_WITH_TERM : MAX_SLOT, 'max', 0)
    // Rail capacity is capped like the side grid: a capture never shrinks the
    // rail cards past the canonical layout, extras stay off the board.
    const others = visibleIds.filter(id => id !== maximizedId && id !== terminal?.id).slice(0, RAIL_SLOT_CAPACITY)
    if (hasTerm) {
      others.forEach((id, i) => place(id, railSlot(i, others.length, 3, 61), 'rail', i + 1))
      if (maximizedId !== terminal!.id) place(terminal!.id, TERMINAL_SLOT, 'term', 1)
    } else {
      const all = visibleIds.filter(id => id !== maximizedId).slice(0, RAIL_SLOT_CAPACITY)
      all.forEach((id, i) => place(id, railSlot(i, all.length, 0, 100), 'rail', i + 1))
    }
    return { placements, order: [], maximizedId, visibleTerminal: hasTerm }
  }

  const rest = visibleIds
    .filter(id => id !== terminal?.id)
    .map(id => byId.get(id)!)
  const order = stableOrder(rest, options.prevOrder)

  if (order[0]) place(order[0], hasTerm ? MAIN_SLOT : MAIN_SLOT_NO_TERM, 'main', 0)
  // Canonical side grid: the first {@link SIDE_SLOT_CAPACITY} cards take the
  // available slots, the rest are deliberately left unplaced (no extra row).
  const sides = order.slice(1, 1 + SIDE_SLOT_CAPACITY)
  const band = hasTerm ? 61 : 93
  sideSlots(sides.length, band).forEach((slot, i) => place(sides[i]!, slot, 'side', i + 1))
  if (hasTerm) place(terminal!.id, TERMINAL_SLOT, 'term', 1)

  return { placements, order, maximizedId: null, visibleTerminal: hasTerm }
}
