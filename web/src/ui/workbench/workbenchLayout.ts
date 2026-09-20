import type { WorkbenchCardState } from '../../gen-clients/system/types'

/**
 * Slot-grouped view of the workbench snapshot. The layout engine (card base)
 * renders main / sides / terminal; `railed` is the frontend-assigned holding
 * rail used while a card is maximized, and `hidden` holds retired cards.
 */
export interface WorkbenchLayout {
  main: WorkbenchCardState | null
  sides: WorkbenchCardState[]
  terminal: WorkbenchCardState | null
  railed: WorkbenchCardState[]
  hidden: WorkbenchCardState[]
}

/**
 * Group the attention snapshot's cards by their assigned slot. Pure and
 * React-free so the frontend layout contract is unit-testable in isolation.
 * The backend assigns every slot except `rail` (the frontend marks cards as
 * railed while another card is maximized/captured).
 */
export function selectWorkbenchLayout(cards: WorkbenchCardState[]): WorkbenchLayout {
  const layout: WorkbenchLayout = { main: null, sides: [], terminal: null, railed: [], hidden: [] }
  for (const card of cards) {
    switch (card.Slot) {
      case 'main':
        if (layout.main === null) layout.main = card
        else layout.sides.push(card)
        break
      case 'term':
        if (layout.terminal === null) layout.terminal = card
        else layout.sides.push(card)
        break
      case 'rail':
        layout.railed.push(card)
        break
      case 'hidden':
        layout.hidden.push(card)
        break
      default:
        layout.sides.push(card)
    }
  }
  return layout
}
