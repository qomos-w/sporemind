import React, { useEffect, useRef, useState } from 'react'
import { Check, ChevronDown } from 'lucide-react'
import type { ModelSlot } from '../../../gen-clients/system/types'
import type { I18nKey } from '../../../i18n/types'
import type { ProviderGroup, ProviderOption } from './AIComposer'
import {
  aggregatorSlot,
  autoSlot,
  pinnedUnitSlot,
  slotToSelection,
  SYSTEM_AGGREGATOR_ID,
} from '../hooks/modelSlot'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import { useDropdownVerticalFit } from '../hooks/useDropdownVerticalFit'
import { useBrowserOverlay } from '../browserOverlay'
import { useI18n } from '../../../i18n'
import './ProviderSlotMenu.css'

/** The five agent model slots, as named on `AgentStatusResp`. */
export type SlotName = 'Primary' | 'Fast' | 'Execution' | 'Review' | 'Summary'

/** A full slot snapshot for the five slots, matching `AgentStatusResp`. */
export interface ProviderSlotMenuSlots {
  Primary: ModelSlot
  Fast: ModelSlot
  Execution: ModelSlot
  Review: ModelSlot
  Summary: ModelSlot
}

export interface ProviderSlotMenuProps {
  /** Whether the menu is open (anchored above the composer's provider button). */
  open: boolean
  onClose: () => void
  /** Current value of each slot; rendered as the per-row summary and the active mark. */
  slots: ProviderSlotMenuSlots
  /** Grouped provider options (composer dropdown source). Used when non-empty. */
  groups?: ProviderGroup[]
  /** Flat provider options (composer legacy source). Used when `groups` is empty. */
  options?: ProviderOption[]
  /**
   * Called with the slot name and the new slot value whenever a candidate is
   * picked. The component never writes state itself — the parent decides whether
   * to persist and whether to close.
   */
  onSelectSlot: (slot: SlotName, slotValue: ModelSlot) => void
}

/** Fixed display order (mirrors the `AgentStatusResp` field order). */
export const SLOT_ORDER: readonly SlotName[] = ['Primary', 'Fast', 'Execution', 'Review', 'Summary']

/** Slot row labels — same i18n keys NewAgentDialog uses for its slot selects. */
const SLOT_LABEL_KEY: Record<SlotName, I18nKey> = {
  Primary: 'dialog.newAgent.primaryModel',
  Fast: 'dialog.newAgent.fastModel',
  Execution: 'dialog.newAgent.executionModel',
  Review: 'dialog.newAgent.reviewModel',
  Summary: 'dialog.newAgent.summaryModel',
}

function sameUnit(a: { model: string; provider: string }, b: { model: string; provider?: string }): boolean {
  return a.model === b.model && (a.provider ?? '') === (b.provider ?? '')
}

/**
 * ProviderSlotMenu — a pure, controlled pop-over that lists the five agent model
 * slots (Primary / Fast / Execution / Review / Summary), shows each slot's
 * current value (auto / named aggregator route / pinned unit), and lets the user
 * re-select a slot from the composer's provider options.
 *
 * It owns no global state and performs no RPC: it reads `slots` and the provider
 * option props and reports every pick through `onSelectSlot`. Styling reuses the
 * composer dropdown visual language via a sibling `.ai-composer-slot-menu` class.
 *
 * Menu mechanics mirror the composer's left-click provider dropdown
 * (`.ai-composer-provider-dropdown`): it is `position: absolute` within
 * `.ai-composer-provider`, opens upward, and is clamped/flipped by
 * `useDropdownVerticalFit`. It also shares `useMenuDismiss` outside-click/Escape
 * handling and `useBrowserOverlay` registration so embedded native browser
 * windows are hidden beneath the HTML overlay.
 */
export const ProviderSlotMenu: React.FC<ProviderSlotMenuProps> = ({
  open,
  onClose,
  slots,
  groups,
  options,
  onSelectSlot,
}) => {
  const { t } = useI18n()
  const menuRef = useRef<HTMLDivElement>(null)
  const [expandedSlot, setExpandedSlot] = useState<SlotName | null>(null)

  useMenuDismiss(menuRef, onClose, open ? 1 : null)
  useBrowserOverlay(open)
  // Keep the menu inside the vertical viewport, flipping downward when there is
  // no room above — the same fit the left-click dropdown uses.
  useDropdownVerticalFit(open, menuRef)

  // Collapse any inline candidate list when the menu re-opens.
  useEffect(() => {
    setExpandedSlot(null)
  }, [open])

  if (!open) return null

  const groupList = groups ?? []
  const optionList = options ?? []
  const hasGroups = groupList.length > 0
  const autoGroup = hasGroups ? groupList.find(g => g.isAuto) : undefined
  const customGroups = hasGroups ? groupList.filter(g => !g.isAuto) : []

  /** Route display name: the system pool reads "Auto", else the group label. */
  const routeLabel = (routeId: string): string => {
    if (routeId === SYSTEM_AGGREGATOR_ID) return t('dialog.newAgent.auto')
    return groupList.find(g => g.routeId === routeId)?.label ?? routeId
  }

  /** One-line summary of a slot's current value (auto / route / unit). */
  const summarize = (slotName: SlotName): { label: string; meta: string } => {
    const sel = slotToSelection(slots[slotName])
    if (!sel) return { label: t('dialog.newAgent.auto'), meta: '' }
    if (sel.type === 'aggregator') return { label: routeLabel(sel.aggregatorId), meta: '' }
    const servedByRoute = !!sel.aggregatorId && sel.aggregatorId !== SYSTEM_AGGREGATOR_ID
    return {
      label: sel.unit.model,
      meta: servedByRoute ? routeLabel(sel.aggregatorId!) : (sel.unit.provider ?? ''),
    }
  }

  const renderCandidates = (slotName: SlotName): React.ReactNode => {
    const sel = slotToSelection(slots[slotName])
    const currentUnit = sel?.type === 'unit' ? sel.unit : null
    const currentAgg = sel?.type === 'aggregator' ? sel.aggregatorId : null
    const nodes: React.ReactNode[] = []

    // Auto — delegate the slot to the system aggregator (empty ≡ [auto]).
    const autoRow = (
      <button
        key="auto"
        type="button"
        role="menuitemradio"
        aria-checked={!sel}
        className={`ai-composer-slot-option${!sel ? ' active' : ''}`}
        onClick={() => onSelectSlot(slotName, autoSlot())}
      >
        <span className="ai-composer-slot-option-label is-badge">{t('dialog.newAgent.auto')}</span>
        {!sel && <Check size={13} />}
      </button>
    )

    const unitRow = (opt: ProviderOption, servingRouteId: string, key: string): React.ReactNode => {
      const unit = opt.unit
      if (!unit) return null
      const isSystem = servingRouteId === SYSTEM_AGGREGATOR_ID
      const active = !!currentUnit
        && sameUnit(unit, currentUnit)
        && (sel?.type === 'unit' ? (sel.aggregatorId ?? '') : '') === (isSystem ? '' : servingRouteId)
      return (
        <button
          key={key}
          type="button"
          role="menuitemradio"
          aria-checked={active}
          className={`ai-composer-slot-option${active ? ' active' : ''}`}
          onClick={() => onSelectSlot(slotName, pinnedUnitSlot(unit, isSystem ? undefined : servingRouteId))}
        >
          <span className="ai-composer-slot-option-label" title={opt.label}>{opt.label}</span>
          {opt.subtitle && <span className="ai-composer-slot-option-subtitle">{opt.subtitle}</span>}
          {active && <Check size={13} />}
        </button>
      )
    }

    /** A selectable aggregator route header (route to its strategy). */
    const routeRow = (routeId: string, label: string, key: string): React.ReactNode => {
      const active = currentAgg === routeId
      return (
        <button
          key={key}
          type="button"
          role="menuitemradio"
          aria-checked={active}
          className={`ai-composer-slot-option is-route${active ? ' active' : ''}`}
          onClick={() => onSelectSlot(slotName, aggregatorSlot(routeId))}
        >
          <span className="ai-composer-slot-option-label is-badge">{label}</span>
          {active && <Check size={13} />}
        </button>
      )
    }

    if (hasGroups) {
      // Each custom aggregator is a selectable route; its units pin through it.
      // Custom aggregators are listed before Auto, mirroring the composer dropdown.
      for (const g of customGroups) {
        nodes.push(routeRow(g.routeId, g.label, `route:${g.routeId}`))
        for (const opt of g.models) {
          const node = unitRow(opt, g.routeId, `route:${g.routeId}:${opt.id}`)
          if (node) nodes.push(node)
        }
      }
      // Auto comes last (empty ≡ [auto]); its pool's concrete units stay selectable
      // as system-served soft pins right below it.
      nodes.push(autoRow)
      for (const opt of autoGroup?.models ?? []) {
        const node = unitRow(opt, SYSTEM_AGGREGATOR_ID, `auto:${opt.id}`)
        if (node) nodes.push(node)
      }
    } else {
      nodes.push(autoRow)
      for (const opt of optionList) {
        const node = unitRow(opt, SYSTEM_AGGREGATOR_ID, `opt:${opt.id}`)
        if (node) nodes.push(node)
      }
    }

    return nodes
  }

  return (
    <div
      ref={menuRef}
      className="ai-composer-slot-menu"
      role="menu"
      tabIndex={-1}
    >
      {SLOT_ORDER.map(slotName => {
        const expanded = expandedSlot === slotName
        const summary = summarize(slotName)
        return (
          <div key={slotName} className="ai-composer-slot-entry">
            <button
              type="button"
              role="menuitem"
              className={`ai-composer-slot-row${expanded ? ' open' : ''}`}
              aria-haspopup="true"
              aria-expanded={expanded}
              onClick={() => setExpandedSlot(prev => (prev === slotName ? null : slotName))}
            >
              <span className="ai-composer-slot-row-name">{t(SLOT_LABEL_KEY[slotName])}</span>
              <span className="ai-composer-slot-row-value" title={summary.meta ? `${summary.label} · ${summary.meta}` : summary.label}>
                {summary.label}
                {summary.meta && <span className="ai-composer-slot-row-meta"> · {summary.meta}</span>}
              </span>
              <ChevronDown size={12} className={expanded ? 'expanded' : ''} />
            </button>
            {expanded && (
              <div className="ai-composer-slot-candidates" role="group">
                {renderCandidates(slotName)}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
