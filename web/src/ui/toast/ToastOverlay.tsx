import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { client } from '../../application/generated-client'
import * as toastApi from '../../gen-clients/toast/client'
import type { ToastCard } from '../../gen-clients/system/types'
import { useBrowserOverlay } from '../ai/browserOverlay'
import {
  ensureCompanionVisibleLoaded,
  getCompanionVisible,
  subscribeCompanionVisible,
} from '../../application/companion-visibility'
import './ToastOverlay.css'

// Cards beyond this count fold into a "+N more" chip (expandable).
const DISPLAY_CAP = 4

const EXIT_ANIM_MS = 260

/**
 * ToastOverlay renders the notification card stack as an overlay inside the
 * main frontend window — there is no second native companion window anymore.
 *
 * Hidden by default: nothing renders until the toast actor holds live cards
 * and the title-bar mute toggle (companion-visibility store) allows display.
 * While a card is up it registers with useBrowserOverlay so the native
 * right-panel browser windows cannot occlude it.
 */
export function ToastOverlay() {
  const [cards, setCards] = useState<ToastCard[]>([])
  const [leavingIds, setLeavingIds] = useState<Set<string>>(() => new Set())
  const [expanded, setExpanded] = useState(false)
  const timersRef = useRef<Map<string, number>>(new Map())

  const enabled = useSyncExternalStore(subscribeCompanionVisible, getCompanionVisible)
  useEffect(() => {
    void ensureCompanionVisibleLoaded()
  }, [])

  // Hydrate the live queue, then subscribe to the toast actor's events over
  // the same gateway connection the main window already uses.
  useEffect(() => {
    let cancelled = false
    const unsubs: Array<() => void> = []
    ;(async () => {
      const st = await toastApi.state(client, {}).catch(() => null)
      if (!cancelled && st && Array.isArray(st.Cards)) {
        setCards(st.Cards)
      }
      if (cancelled) return
      unsubs.push(
        toastApi.OnToastCardAdded(client, (card) => {
          if (cancelled) return
          setCards((prev) => (prev.some((c) => c.Id === card.Id) ? prev : [...prev, card]))
        }),
      )
      unsubs.push(
        toastApi.OnToastCardRemoved(client, ({ Id }) => {
          if (cancelled) return
          setLeavingIds((prev) => new Set(prev).add(Id))
          window.setTimeout(() => {
            setCards((prev) => prev.filter((c) => c.Id !== Id))
            setLeavingIds((prev) => {
              const next = new Set(prev)
              next.delete(Id)
              return next
            })
          }, EXIT_ANIM_MS)
        }),
      )
    })()
    return () => {
      cancelled = true
      unsubs.forEach((u) => u())
    }
  }, [])

  // Auto-dismiss timers for cards carrying a positive duration. Dismissal
  // flows through the toast actor so every subscriber agrees.
  useEffect(() => {
    const timers = timersRef.current
    const live = new Set(cards.map((c) => c.Id))
    for (const id of [...timers.keys()]) {
      if (!live.has(id)) {
        window.clearTimeout(timers.get(id))
        timers.delete(id)
      }
    }
    for (const card of cards) {
      const ms = card.DurationMs ?? 0
      if (ms <= 0 || timers.has(card.Id) || leavingIds.has(card.Id)) continue
      timers.set(
        card.Id,
        window.setTimeout(() => {
          timers.delete(card.Id)
          void toastApi.dismiss(client, { Id: card.Id }).catch(() => {
            // Unknown id (already removed elsewhere) — drop locally.
            setCards((prev) => prev.filter((c) => c.Id !== card.Id))
          })
        }, ms),
      )
    }
  }, [cards, leavingIds])

  useEffect(() => {
    const timers = timersRef.current
    return () => {
      for (const t of timers.values()) window.clearTimeout(t)
      timers.clear()
    }
  }, [])

  const dismissCard = useCallback((id: string) => {
    void toastApi.dismiss(client, { Id: id }).catch(() => {
      setCards((prev) => prev.filter((c) => c.Id !== id))
    })
  }, [])

  // Action-button click: report to the toast actor over the gateway. The
  // actor re-broadcasts toast.action_triggered (callable ID + args) and the
  // schema overlay modal (useToastActionEvents) owns what happens next. The
  // target callable is never executed from here.
  const triggerAction = useCallback((id: string) => {
    void toastApi.action(client, { Id: id }).catch((err) => {
      console.warn('[ToastOverlay] action trigger failed', err)
    })
  }, [])

  const visible = enabled && cards.length > 0
  useBrowserOverlay(visible)

  if (!visible) return null

  const shown = expanded ? cards : cards.slice(Math.max(0, cards.length - DISPLAY_CAP))
  const folded = cards.length - shown.length

  return (
    <div className="toast-overlay" role="region" aria-label="Notifications">
      <div className="toast-card-stack" role="list" aria-live="polite">
        {folded > 0 && (
          <button className="toast-fold-chip" onClick={() => setExpanded(true)}>
            +{folded} more
          </button>
        )}
        {shown.map((card) => (
          <article
            key={card.Id}
            role="listitem"
            className={`toast-card toast-card-${card.Kind || 'info'}${leavingIds.has(card.Id) ? ' is-leaving' : ''}`}
          >
            <div className="toast-card-main">
              <div className="toast-card-title">{card.Title}</div>
              {card.Body ? <div className="toast-card-body">{card.Body}</div> : null}
              <div className="toast-card-meta">
                {card.Source ? <span className="toast-card-source">{card.Source}</span> : null}
                <span className="toast-card-time">{formatTime(card.CreatedAt)}</span>
              </div>
              {card.ActionLabel && card.ActionCallable ? (
                <div className="toast-card-action-row">
                  <button
                    className="toast-card-action"
                    onClick={() => triggerAction(card.Id)}
                    title={card.ActionCallable}
                  >
                    {card.ActionLabel}
                  </button>
                </div>
              ) : null}
            </div>
            <button className="toast-card-close" onClick={() => dismissCard(card.Id)} aria-label="Dismiss">
              ×
            </button>
          </article>
        ))}
        {expanded && folded === 0 && cards.length > DISPLAY_CAP && (
          <button className="toast-fold-chip" onClick={() => setExpanded(false)}>
            collapse
          </button>
        )}
      </div>
    </div>
  )
}

function formatTime(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  return new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
