import React, { useCallback, useRef } from 'react'
import type { SplitTreeNode, TabItem, ViewType } from '../types/layout'
import type { TabBadgeProvider } from '../adapters/tab-badge'
import type { TabContextMenuLocale } from '../i18n/tab-context-menu'
import { TabContainer } from './TabContainer'
import './SplitContainer.css'

interface SplitContainerProps {
  node: SplitTreeNode
  renderTabContent: (tab: TabItem) => React.ReactNode
  renderViewToolbar?: (viewType: ViewType) => React.ReactNode
  getTabIcon?: (tab: TabItem) => React.ReactNode
  badgeProvider?: TabBadgeProvider
  locale?: TabContextMenuLocale
  onResizeSplit?: (splitId: string, ratio: number) => void
  onCloseTab?: (leafId: string, tabId: string) => void
  onSetActiveTab?: (leafId: string, index: number) => void
  onMoveTab?: (fromLeafId: string, tabId: string, toLeafId: string, insertIndex?: number) => void
  onMoveTabToEdge?: (fromLeafId: string, tabId: string, targetLeafId: string, edge: 'top' | 'bottom' | 'left' | 'right') => void
}

export const SplitContainer: React.FC<SplitContainerProps> = ({
  node,
  renderTabContent,
  renderViewToolbar,
  getTabIcon,
  badgeProvider,
  locale,
  onResizeSplit,
  onCloseTab,
  onSetActiveTab,
  onMoveTab,
  onMoveTabToEdge,
}) => {
  if (node.type === 'leaf') {
    return (
      <TabContainer
        leaf={node}
        renderTabContent={renderTabContent}
        renderViewToolbar={renderViewToolbar}
        getTabIcon={getTabIcon}
        badgeProvider={badgeProvider}
        locale={locale}
        onCloseTab={onCloseTab}
        onSetActiveTab={onSetActiveTab}
        onMoveTab={onMoveTab}
        onMoveTabToEdge={onMoveTabToEdge}
      />
    )
  }

  const dir = node.direction === 'horizontal' ? 'row' : 'column'
  const firstSize = `${node.ratio * 100}%`

  return (
    <div className="split-container" style={{ flexDirection: dir }}>
      <div className="split-pane" style={{ flexBasis: firstSize, flexGrow: 0, flexShrink: 0 }}>
        <SplitContainer
          node={node.first}
          renderTabContent={renderTabContent}
          renderViewToolbar={renderViewToolbar}
          getTabIcon={getTabIcon}
          badgeProvider={badgeProvider}
          locale={locale}
          onResizeSplit={onResizeSplit}
          onCloseTab={onCloseTab}
          onSetActiveTab={onSetActiveTab}
          onMoveTab={onMoveTab}
          onMoveTabToEdge={onMoveTabToEdge}
        />
      </div>
      <SplitDivider direction={node.direction} splitId={node.id} onResize={onResizeSplit} />
      <div className="split-pane" style={{ flex: 1 }}>
        <SplitContainer
          node={node.second}
          renderTabContent={renderTabContent}
          renderViewToolbar={renderViewToolbar}
          getTabIcon={getTabIcon}
          badgeProvider={badgeProvider}
          locale={locale}
          onResizeSplit={onResizeSplit}
          onCloseTab={onCloseTab}
          onSetActiveTab={onSetActiveTab}
          onMoveTab={onMoveTab}
          onMoveTabToEdge={onMoveTabToEdge}
        />
      </div>
    </div>
  )
}

// ── Split Divider ──
// Uses setPointerCapture so pointer events are always delivered to this element
// while dragging, regardless of what content (Monaco, DAG canvas, etc.) is underneath.

const SplitDivider: React.FC<{
  direction: 'horizontal' | 'vertical'
  splitId: string
  onResize?: (splitId: string, ratio: number) => void
}> = ({ direction, splitId, onResize }) => {
  // Captured at pointer-down; valid for the entire drag gesture.
  const drag = useRef<{ totalSize: number; startOffset: number } | null>(null)

  const onPointerDown = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault()
    e.stopPropagation()

    const parent = e.currentTarget.parentElement
    if (!parent) return

    // Capture the pointer so all subsequent pointer events are routed to this
    // element, even when the cursor moves over Monaco, DAG canvas, iframes, etc.
    e.currentTarget.setPointerCapture(e.pointerId)

    const rect = parent.getBoundingClientRect()
    drag.current = {
      totalSize:   direction === 'horizontal' ? rect.width  : rect.height,
      startOffset: direction === 'horizontal' ? rect.left   : rect.top,
    }
  }, [direction])

  const onPointerMove = useCallback((e: React.PointerEvent) => {
    if (!drag.current || !onResize) return
    const pos = direction === 'horizontal' ? e.clientX : e.clientY
    const newRatio = (pos - drag.current.startOffset) / drag.current.totalSize
    onResize(splitId, newRatio)
  }, [direction, splitId, onResize])

  const onPointerUp = useCallback(() => {
    drag.current = null
  }, [])

  return (
    <div
      className={`split-divider ${direction}`}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
    />
  )
}
