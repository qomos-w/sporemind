import React, { useEffect, useLayoutEffect, useRef } from 'react'
import { FileText, Pencil, Tag, Copy, Link, Trash2, Filter, GitBranch, GitFork, MessageCircle, Target, Bookmark, LocateFixed, Play, Eye, ArrowLeftRight, CalendarPlus, ChevronsDownUp, ChevronsUpDown } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { Palette } from 'lucide-react'
import { useBrowserOverlay } from '../browserOverlay'
import type { MonoCardListItem } from '../../../domain/mono-types'
import { normalizeMonoCardType } from '../../../domain/mono-types'
import { normalizeTaskStatus } from '../../../domain/task-status'
import './TopologyCardContextMenu.css'

export interface CardMenuTarget {
  card: MonoCardListItem
  x: number
  y: number
}

interface TopologyCardContextMenuProps {
  target: CardMenuTarget | null
  /** Where the menu is shown. 'workflow' hides topology-specific items
   *  (filter subtree / lineage). Default: 'topology'. */
  context?: 'topology' | 'workflow'
  onClose: () => void
  onOpenCard: (card: MonoCardListItem) => void
  onRename: (card: MonoCardListItem) => void
  onAddTag: (card: MonoCardListItem) => void
  onAddChild: (card: MonoCardListItem) => void
  onSetVisual: (card: MonoCardListItem) => void
  onDuplicate: (card: MonoCardListItem) => void
  onCopyPath: (card: MonoCardListItem) => void
  onDelete: (card: MonoCardListItem) => void
  onFilterSubtree: (card: MonoCardListItem) => void
  onFilterLineage: (card: MonoCardListItem) => void
  onChat: (card: MonoCardListItem) => void
  onAssignGoal: (card: MonoCardListItem) => void
  onSaveAsTemplate?: (card: MonoCardListItem) => void
  /** Zoom-to-fit the template an instance map was spawned from. Only shown
   *  for workflow cards carrying data.instance_of. */
  onLocateTemplate?: (card: MonoCardListItem) => void
  /** Instantiate a new runnable workflow instance from a template card
   *  (data.template === true). */
  onInstantiateFromTemplate?: (card: MonoCardListItem) => void
  /** Create a scheduler card bound to this template (data.template === true)
   *  — lands in the Scheduled view; cron/executor edited on the card. */
  onCreateScheduledTask?: (card: MonoCardListItem) => void
  /** Show the instance maps spawned from a template card (data.template).
   *  When implemented as an inline list, the fetched runs are supplied via
   *  instanceRuns/onOpenInstanceMap. */
  onViewInstances?: (card: MonoCardListItem) => void
  /** Instance map ids spawned from the template card currently targeted by
   *  onViewInstances; renders as clickable sub-items when present. */
  instanceRuns?: string[]
  /** Zoom-to-fit the given instance map (spawned from the targeted template). */
  onOpenInstanceMap?: (instanceMapId: string) => void
  /** Trigger a timer card immediately (U2 — scheduler cards, tags contain
   *  "scheduler"). */
  onRunNow?: (card: MonoCardListItem) => void
  /** Locate the scheduler card that spawned an instance map (N5 — only shown
   *  when the instance card carries data.scheduler_card_id). */
  onLocateScheduler?: (card: MonoCardListItem) => void
  /** Fold/unfold a workflow map's child task cards (cards topology view).
   *  Only shown for workflow cards; toggling the shared fold store re-fetches
   *  the card list with the server-side fold filter, hiding the map's tasks. */
  onToggleFold?: (card: MonoCardListItem) => void
  /** Currently folded workflow map ids — drives the fold item's label/icon. */
  foldedWorkflows?: ReadonlySet<string>
}

interface MenuItem {
  key: string
  label: string
  icon: React.ReactNode
  danger?: boolean
  disabled?: boolean
  /** When true, clicking the item does not close the menu (used for the
   *  "View Instances" flow, which shows the fetched runs as sub-items). */
  keepOpen?: boolean
  run: (card: MonoCardListItem) => void
}

export const TopologyCardContextMenu: React.FC<TopologyCardContextMenuProps> = ({
  target,
  context = 'topology',
  onClose,
  onOpenCard,
  onRename,
  onAddTag,
  onAddChild,
  onSetVisual,
  onDuplicate,
  onCopyPath,
  onDelete,
  onFilterSubtree,
  onFilterLineage,
  onChat,
  onAssignGoal,
  onSaveAsTemplate,
  onLocateTemplate,
  onInstantiateFromTemplate,
  onCreateScheduledTask,
  onViewInstances,
  instanceRuns,
  onOpenInstanceMap,
  onRunNow,
  onLocateScheduler,
  onToggleFold,
  foldedWorkflows,
}) => {
  const { t } = useI18n()
  const menuRef = useRef<HTMLDivElement>(null)
  const [viewingRuns, setViewingRuns] = React.useState(false)

  // The context menu is a floating HTML overlay: register it with the overlay
  // manager so any native browser windows underneath are hidden while open.
  useBrowserOverlay(!!target)

  // Reset the "View Instances" sub-menu state whenever the menu (re)opens.
  useEffect(() => {
    if (!target) setViewingRuns(false)
  }, [target])

  useEffect(() => {
    if (!target) return
    const handleMouseDown = (event: MouseEvent) => {
      if (event.button !== 0) return
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        onClose()
      }
    }
    const handleContextMenu = (event: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        onClose()
      }
    }
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    document.addEventListener('mousedown', handleMouseDown)
    document.addEventListener('contextmenu', handleContextMenu, true)
    document.addEventListener('keydown', handleKey)
    return () => {
      document.removeEventListener('mousedown', handleMouseDown)
      document.removeEventListener('contextmenu', handleContextMenu, true)
      document.removeEventListener('keydown', handleKey)
    }
  }, [target, onClose])

  useLayoutEffect(() => {
    if (!target || !menuRef.current) return
    const menu = menuRef.current
    const rect = menu.getBoundingClientRect()
    const viewportW = document.documentElement.clientWidth
    const viewportH = document.documentElement.clientHeight
    const padding = 8

    let left = target.x
    let top = target.y

    if (left + rect.width > viewportW - padding) {
      left = target.x - rect.width
    }
    if (left < padding) {
      left = padding
    }

    if (top + rect.height > viewportH - padding) {
      top = target.y - rect.height
    }
    if (top < padding) {
      top = padding
    }

    menu.style.left = `${left}px`
    menu.style.top = `${top}px`
  }, [target])

  if (!target) return null

  const { card } = target

  const isWorkflow = normalizeMonoCardType(card.type) === 'workflow'
  const isTemplateCard = card.data?.template === true
  const instanceOf = typeof card.data?.instance_of === 'string' ? card.data.instance_of : undefined
  const schedulerCardId = typeof card.data?.scheduler_card_id === 'string' ? card.data.scheduler_card_id : undefined
  const isSchedulerCard = Array.isArray(card.tags) && card.tags.includes('scheduler')

  const items: MenuItem[] = [
    { key: 'open', label: t('topologyCardContextMenu.openCard'), icon: <FileText size={13} />, run: onOpenCard },
    { key: 'chat', label: t('topologyCardContextMenu.chat'), icon: <MessageCircle size={13} />, run: onChat },
    ...(normalizeMonoCardType(card.type) === 'task' && normalizeTaskStatus(card.status ?? '') !== 'done'
      ? [{ key: 'assignGoal', label: t('topologyCardContextMenu.assignGoal'), icon: <Target size={13} />, run: onAssignGoal }]
      : []),
    ...(context === 'topology'
      ? [
          { key: 'rename', label: t('topologyCardContextMenu.rename'), icon: <Pencil size={13} />, disabled: card.editable === false, run: onRename },
          { key: 'tag', label: t('topologyCardContextMenu.addTag'), icon: <Tag size={13} />, disabled: card.editable === false, run: onAddTag },
          { key: 'addChild', label: t('topologyCardContextMenu.addChild'), icon: <GitFork size={13} />, disabled: card.editable === false, run: onAddChild },
          { key: 'visual', label: t('topologyCardContextMenu.visual'), icon: <Palette size={13} />, disabled: card.editable === false, run: onSetVisual },
          { key: 'duplicate', label: t('topologyCardContextMenu.duplicate'), icon: <Copy size={13} />, disabled: card.editable === false, run: onDuplicate },
          { key: 'copyPath', label: t('topologyCardContextMenu.copyPath'), icon: <Link size={13} />, run: onCopyPath },
        ]
      : []),
    ...(isWorkflow && onSaveAsTemplate
      ? [{ key: 'saveAsTemplate', label: t('topologyCardContextMenu.saveAsTemplate'), icon: <Bookmark size={13} />, disabled: card.editable === false, run: onSaveAsTemplate }]
      : []),
    ...(isTemplateCard && onInstantiateFromTemplate
      ? [{ key: 'instantiateFromTemplate', label: t('topologyCardContextMenu.instantiateFromTemplate'), icon: <Play size={13} />, disabled: card.editable === false, run: onInstantiateFromTemplate }]
      : []),
    ...(isTemplateCard && onCreateScheduledTask
      ? [{ key: 'newScheduledTask', label: t('topologyCardContextMenu.newScheduledTask'), icon: <CalendarPlus size={13} />, disabled: card.editable === false, run: onCreateScheduledTask }]
      : []),
    ...(isTemplateCard && onViewInstances
      ? [{ key: 'viewInstances', label: viewingRuns ? t('topologyCardContextMenu.loadingInstances') : t('topologyCardContextMenu.viewInstances'), icon: <Eye size={13} />, disabled: viewingRuns, keepOpen: true, run: (c: MonoCardListItem) => { setViewingRuns(true); onViewInstances(c) } }]
      : []),
    ...(isWorkflow && instanceOf !== undefined && onLocateTemplate
      ? [{ key: 'locateTemplate', label: t('topologyCardContextMenu.locateTemplate'), icon: <LocateFixed size={13} />, run: onLocateTemplate }]
      : []),
    ...(isWorkflow && instanceOf !== undefined && schedulerCardId !== undefined && onLocateScheduler
      ? [{ key: 'locateScheduler', label: t('topologyCardContextMenu.locateScheduler'), icon: <ArrowLeftRight size={13} />, run: onLocateScheduler }]
      : []),
    ...(isSchedulerCard && onRunNow
      ? [{ key: 'runNow', label: t('topologyCardContextMenu.runNow'), icon: <Play size={13} />, disabled: card.editable === false, run: onRunNow }]
      : []),
    ...(isWorkflow && context === 'topology' && onToggleFold
      ? [{
          key: 'toggleFold',
          label: foldedWorkflows?.has(card.id)
            ? t('topologyCardContextMenu.unfoldChildren')
            : t('topologyCardContextMenu.foldChildren'),
          icon: foldedWorkflows?.has(card.id)
            ? <ChevronsUpDown size={13} />
            : <ChevronsDownUp size={13} />,
          run: onToggleFold,
        }]
      : []),
    ...(context === 'topology'
      ? [
          { key: 'filterSubtree', label: t('topologyCardContextMenu.filterSubtree'), icon: <Filter size={13} />, run: onFilterSubtree },
          { key: 'filterLineage', label: t('topologyCardContextMenu.filterLineage'), icon: <GitBranch size={13} />, run: onFilterLineage },
        ]
      : []),
    {
      key: 'delete',
      label: normalizeMonoCardType(card.type) === 'workflow'
        ? t('topologyCardContextMenu.deleteWorkflow')
        : t('topologyCardContextMenu.delete'),
      icon: <Trash2 size={13} />,
      danger: true,
      disabled: card.deletable === false,
      run: onDelete,
    },
  ]

  return (
    <div
      ref={menuRef}
      className="topology-card-context-menu"
      style={{ left: target.x, top: target.y }}
      role="menu"
      tabIndex={-1}
    >
      <div className="topology-card-context-menu-header">
        <span className="topology-card-context-menu-title">{card.id}</span>
      </div>
      <div className="topology-card-context-menu-list">
        {items.map(item => (
          <button
            key={item.key}
            type="button"
            className={[
              'topology-card-context-menu-item',
              item.danger ? 'danger' : '',
              item.disabled ? 'disabled' : '',
            ].join(' ')}
            role="menuitem"
            disabled={item.disabled}
            onClick={() => {
              if (!item.disabled) {
                item.run(card)
                if (!item.keepOpen) onClose()
              }
            }}
          >
            <span className="topology-card-context-menu-icon">{item.icon}</span>
            <span className="topology-card-context-menu-label">{item.label}</span>
          </button>
        ))}
        {viewingRuns && instanceRuns !== undefined && (
          <>
            <div className="topology-card-context-menu-separator" />
            {instanceRuns.length === 0 ? (
              <div className="topology-card-context-menu-empty">{t('topologyCardContextMenu.noInstances')}</div>
            ) : instanceRuns.map(instanceMapId => (
              <button
                key={instanceMapId}
                type="button"
                className="topology-card-context-menu-item topology-card-context-menu-instance"
                role="menuitem"
                onClick={() => {
                  onOpenInstanceMap?.(instanceMapId)
                  onClose()
                }}
              >
                <span className="topology-card-context-menu-icon"><Eye size={13} /></span>
                <span className="topology-card-context-menu-label" title={instanceMapId}>{instanceMapId}</span>
              </button>
            ))}
          </>
        )}
      </div>
    </div>
  )
}
