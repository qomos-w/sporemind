import React from 'react'
import { ChevronDown } from 'lucide-react'
import type { TurnEnvelope, Frame } from '../model/frame-types'
import { MessageDrawer } from './MessageDrawer'
import './ComposerFrame.css'

export interface ComposerFrameProps {
  /** Whether to show the message drawer (topology/multiconsole/right-panel modes) */
  showDrawer: boolean
  /** Conversation envelopes for the drawer */
  envelopes: TurnEnvelope[]
  /** Whether the agent is currently streaming */
  isStreaming?: boolean
  /** Identity of the conversation whose envelopes are shown. A change means
      the envelope list was swapped (e.g. agent switch), not a new message. */
  conversationId?: string | null
  /** Frame select callback for tool bubbles in drawer */
  onFrameSelect?: (frame: Frame) => void
  /** Cancels a pending message submit from the collapsed drawer */
  onCancelPendingSubmit?: (clientId: string) => void
  /** Above bar content (project/git dropdowns) — rendered above the drawer.
      When provided, children should have hideAboveSlots=true. */
  aboveBarSlot?: React.ReactNode
  /** Called whenever the outer frame height changes (for dynamic bottom padding) */
  onHeightChange?: (height: number) => void
  /** Called whenever the composer card top offset (from content bottom) changes */
  onCardTopChange?: (top: number) => void
  /** Desktop-only: expanded drawer top edge becomes a drag handle resizing the stream. */
  drawerResizable?: boolean
  /** Persisted expanded drawer stream height (px). */
  drawerHeight?: number
  onDrawerHeightChange?: (height: number) => void
  /** The composer element (AIConversationComposer or AIComposer) */
  children: React.ReactNode
}

/**
 * ComposerFrame — the universal base frame that hosts a single AIComposer
 * instance and optionally a MessageDrawer peeking behind it.
 *
 * Layout when drawer is active:
 *
 *   ┌─── above bar (project/git) ───┐  ← follows drawer top
 *   ├─── drawer card ───────────────┤
 *   │  peek content (z=1)           │  ← visible above composer
 *   │  ┌── composer card (z=2) ──┐  │  ← covers drawer bottom
 *   │  │ input area              │  │
 *   │  └─────────────────────────┘  │
 *   └───────────────────────────────┘
 *
 * Three edges (left/right/bottom) of drawer and composer overlap.
 * Composer z=2 > drawer z=1.
 *
 * When showDrawer=false (conversation mode), only children render.
 *
 * Collapsed state (composer-collapsed on the drawer card): the composer card
 * is display:none and the drawer reserves no bottom padding, so only the
 * drawer peek bar remains. The peek's up-arrow restores the composer first
 * and expands the drawer on the second click.
 */
export const ComposerFrame: React.FC<ComposerFrameProps> = ({
  showDrawer,
  envelopes,
  isStreaming = false,
  conversationId = null,
  onFrameSelect,
  onCancelPendingSubmit,
  aboveBarSlot,
  onHeightChange,
  onCardTopChange,
  drawerResizable = false,
  drawerHeight,
  onDrawerHeightChange,
  children,
}) => {
  const [drawerExpanded, setDrawerExpanded] = React.useState(false)
  // When true the composer card is hidden and only the drawer remains.
  const [composerCollapsed, setComposerCollapsed] = React.useState(false)

  // Collapse handle (composer top-right): an expanded drawer collapses first;
  // the next click hides the composer card itself.
  const handleCollapseToggle = () => {
    if (drawerExpanded) {
      setDrawerExpanded(false)
      return
    }
    setComposerCollapsed(true)
  }

  // Drawer up-arrow: while the composer is collapsed, the first click restores
  // the composer (drawer stays collapsed); the second click expands the drawer.
  const handleDrawerToggle = () => {
    if (composerCollapsed) {
      setComposerCollapsed(false)
      return
    }
    setDrawerExpanded(prev => !prev)
  }

  // Auto-collapse drawer when new message is sent
  const prevEnvelopeCount = React.useRef(envelopes.length)
  const prevConversationId = React.useRef(conversationId)
  // Set when the conversation swapped while envelopes were still empty; the
  // count stays rebased until the swapped history actually arrives.
  const awaitingSwappedHistory = React.useRef(false)
  React.useEffect(() => {
    if (prevConversationId.current !== conversationId) {
      prevConversationId.current = conversationId
      prevEnvelopeCount.current = envelopes.length
      awaitingSwappedHistory.current = envelopes.length === 0
      return
    }
    if (awaitingSwappedHistory.current) {
      if (envelopes.length === 0) return
      awaitingSwappedHistory.current = false
      prevEnvelopeCount.current = envelopes.length
      return
    }
    if (envelopes.length > prevEnvelopeCount.current && !isStreaming && drawerExpanded) {
      setDrawerExpanded(false)
    }
    prevEnvelopeCount.current = envelopes.length
  }, [envelopes.length, isStreaming, drawerExpanded, conversationId])

  // Measure composer height for drawer padding-bottom
  const composerRef = React.useRef<HTMLDivElement>(null)
  const [composerH, setComposerH] = React.useState(0)

  React.useLayoutEffect(() => {
    if (!showDrawer || !composerRef.current) return
    const el = composerRef.current
    // Keep the last real height while the card is display:none (collapsed),
    // so restoring gets the correct padding instantly.
    const update = () => { if (el.offsetHeight > 0) setComposerH(el.offsetHeight) }
    update()
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [showDrawer])

  // Measure the outer frame height so parents can reserve bottom padding.
  // Also report the composer card's top offset from the frame bottom, so the
  // gradient fade can land exactly at the card top.
  const frameRef = React.useRef<HTMLDivElement>(null)
  const onHeightChangeRef = React.useRef(onHeightChange)
  onHeightChangeRef.current = onHeightChange
  const onCardTopChangeRef = React.useRef(onCardTopChange)
  onCardTopChangeRef.current = onCardTopChange

  const lastReportedH = React.useRef<number | null>(null)
  const lastReportedTop = React.useRef<number | null>(null)

  React.useLayoutEffect(() => {
    if (!frameRef.current) return
    const el = frameRef.current
    // The observed element changes with drawer mode; always report the first
    // measurement of a fresh observation.
    lastReportedH.current = null
    lastReportedTop.current = null
    const update = () => {
      const h = el.offsetHeight
      const card = el.querySelector<HTMLElement>('.ai-composer')
      const cardTop = card ? h - card.offsetTop : h
      // Hysteresis: ±1px offsetHeight/offsetTop rounding noise must not reach
      // --composer-frame-h/--composer-card-top — the stream derives its bottom
      // padding from them (AIConversationPage.css), so a flapping variable
      // pumps scrollHeight ±1px and re-triggers the scroll-follow ResizeObserver.
      if (lastReportedH.current === null || Math.abs(h - lastReportedH.current) >= 2) {
        lastReportedH.current = h
        onHeightChangeRef.current?.(h)
      }
      if (lastReportedTop.current === null || Math.abs(cardTop - lastReportedTop.current) >= 2) {
        lastReportedTop.current = cardTop
        onCardTopChangeRef.current?.(cardTop)
      }
    }
    update()
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [showDrawer])

  // In drawer mode, keep the drawer layout even when envelopes are temporarily empty
  // (e.g. switching agents). This prevents the composer from collapsing/remounting
  // between drawer and no-drawer modes.
  const shouldShowDrawer = showDrawer

  // ── No drawer: simple wrapper ──
  if (!shouldShowDrawer) {
    return (
      <div className="composer-frame no-drawer" ref={frameRef}>
        {children}
      </div>
    )
  }

  // ── Drawer active: above-bar + drawer-card(drawer-content + composer) ──
  return (
    <div className="composer-frame has-drawer" ref={frameRef}>
      {/* Above bar — transparent, sits above drawer, follows when drawer expands */}
      {aboveBarSlot && (
        <div className="composer-frame-above-bar">
          {aboveBarSlot}
        </div>
      )}

      {/* Drawer card — contains peek/stream + composer.
          In normal flow; height = drawer-content + composer.
          Grows when expanded, pushing above-bar up. */}
      <div
        className={`composer-frame-drawer-card${composerCollapsed ? ' composer-collapsed' : ''}`}
        style={{ '--composer-h': `${composerH}px` } as React.CSSProperties}
      >
        {/* Drawer content (peek/stream) — z=1, at top of card */}
        <div className="composer-frame-drawer-content">
          <MessageDrawer
            envelopes={envelopes}
            isStreaming={isStreaming}
            expanded={drawerExpanded}
            onToggle={handleDrawerToggle}
            onFrameSelect={onFrameSelect}
            onCancelPendingSubmit={onCancelPendingSubmit}
            resizable={drawerResizable}
            streamHeight={drawerHeight}
            onStreamHeightChange={onDrawerHeightChange}
          />
        </div>

        {/* Composer card — absolute, bottom-aligned, z=2.
            Covers the drawer's bottom portion (the padding-bottom area). */}
        <div className="composer-frame-composer-card" ref={composerRef}>
          <button
            type="button"
            className="composer-frame-collapse-btn"
            onClick={handleCollapseToggle}
            aria-label="Collapse composer panel"
            title="Collapse composer panel"
          >
            <ChevronDown size={14} />
          </button>
          {children}
        </div>
      </div>
    </div>
  )
}
