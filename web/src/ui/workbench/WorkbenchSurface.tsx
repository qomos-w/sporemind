import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import type { FormEvent } from 'react'
import { ArrowLeft, Copy, Gauge, LayoutGrid, MessageCircle, Mic, Minus, Moon, Snowflake, Sparkles, Square, Sun, Terminal, X } from 'lucide-react'

import { useI18n } from '../../i18n'
import { appRegistry } from '../../application/app-registry'
import { buildVersion } from '../../config/buildConfig'
import { applyWorkbenchSurface } from '../../application/workbench-surface'
import { wailsWindowController } from '../../application/window-controller'
import { voiceAPI } from '../ai/voice-api'
import { createAppCardDescriptors } from './appCard'
import { createChatCardDescriptors } from './chatCard'
import { createWorkbenchCardRegistry, useWorkbenchCardRegistry, type WorkbenchCardRegistry } from './cardRegistry'
import type { WorkbenchCardDescriptor } from './cardTypes'
import { WorkbenchCard } from './WorkbenchCard'
import { workbenchCardIcon } from './workbenchIcon'
import { useWorkbenchLayout } from './useWorkbenchLayout'
import { useWorkbenchCards } from './useWorkbenchCards'
import { useCoordinatorChat } from './useCoordinatorChat'
import { useWorkbenchAgents } from './useWorkbenchAgents'
import { projectionCardId, projectionOnlyDescriptor, withProjection } from './projectionBoard'
import {
  DEFAULT_TERMINAL_KEYWORDS,
  TERMINAL_CARD_ID,
  createTerminalCardDescriptor,
} from './terminal'
import { useTerminalPresence } from './terminal'
import './workbench-surface.css'

export interface WorkbenchSurfaceProps {
  /** Working directory handed to the terminal card's shell session. */
  projectRoot?: string
  /** Extra summon keywords merged with the terminal defaults (e.g. a title). */
  summonKeywords?: readonly string[]
  /** Card registry override (defaults to a per-surface registry). */
  registry?: WorkbenchCardRegistry
  /** Leave the board (top-left corner button); restores the previous shell pane. */
  onExit?: () => void
  /** Toggle light / dark (top-left corner button). */
  onToggleTheme?: () => void
  /** Current dark-mode flag; flips the corner button icon (moon ↔ sun). */
  isDark?: boolean
}

/* Corner / dock glyphs: lucide shapes, restyled by the wb-cbtn / wb-spark CSS. */
const ICON_EXIT = <ArrowLeft />
const ICON_FREEZE = <Snowflake />
const ICON_SCORE = <Gauge />
const ICON_MOON = <Moon />
const ICON_SUN = <Sun />
const ICON_PAD = <LayoutGrid />
const ICON_CONVO = <MessageCircle />
const ICON_SPARK = <Sparkles />
const ICON_MIC = <Mic />
const ICON_TERM = <Terminal />
const WAVE_BARS = [0, 1, 2, 3, 4].map(i => <i key={i} />)

interface ConvoRow {
  id: number
  who: 'u' | 'a'
  text: string
}

/**
 * Workbench board (spec §1–§4), fullscreen takeover mode.
 *
 * Chrome mirrors tmp/blackboard.html 1:1 — corner controls (exit / freeze /
 * theme left, launcher pad / conversation right), the expandable dock (halo +
 * spark + mic + wave + conversation history), the frost veil, and the
 * fullscreen app-launcher pad.
 *
 * Attention is owned by the workbench actor: the board renders the
 * `workbench.snapshot` projection (score / slot / pinned / hidden) and routes
 * every board click back through the lane-routed `workbench.promote` /
 * `set_pinned` / `set_hidden` callables. App and agent card metadata is declared
 * back to the actor via `workbench.upsert_card`, so the projection carries real
 * metadata.
 *
 * Render bodies (app plugin iframes, the terminal shell) come from the local
 * card descriptors; when the projection is unavailable the board falls back to
 * the local layout engine, so it still works without the actor.
 */
export function WorkbenchSurface({
  projectRoot,
  summonKeywords,
  registry: registryProp,
  onExit,
  onToggleTheme,
  isDark = false,
}: WorkbenchSurfaceProps) {
  const { t } = useI18n()
  const [draft, setDraft] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)
  const vlistRef = useRef<HTMLDivElement>(null)
  const padQueryRef = useRef<HTMLInputElement>(null)

  const [convoOpen, setConvoOpen] = useState(false)
  const [listening, setListening] = useState(false)
  const [padOpen, setPadOpen] = useState(false)
  // Debug score overlay on card titles — ephemeral by design (a debugging aid,
  // not a durable preference), so plain component state is enough.
  const [showScores, setShowScores] = useState(false)
  const [padQuery, setPadQuery] = useState('')
  const [convo, setConvo] = useState<ConvoRow[]>([])
  const convoIdRef = useRef(0)

  const pushConvo = useCallback((who: 'u' | 'a', text: string) => {
    convoIdRef.current += 1
    setConvo(prev => [...prev, { id: convoIdRef.current, who, text }])
  }, [])

  // ── Voice input (STT) ──────────────────────────────────────────
  // The mic toggles the shared voiceAPI (same driver chain as the composer:
  // Web Speech API → MediaRecorder + voice.recognize). Recognized text lands
  // in the dock input; driver errors reset the listening state.
  const toggleListening = useCallback(() => {
    setListening(prev => !prev)
    inputRef.current?.focus()
  }, [])

  useEffect(() => {
    void voiceAPI.setActive(listening, listening ? {
      onResult: (text) => {
        setDraft(prev => (prev ? `${prev} ${text}` : text))
      },
      onError: (err) => {
        console.error('[Voice]', err)
        setListening(false)
      },
    } : undefined).catch((err) => {
      console.error('[Voice] transition failed:', err)
      setListening(false)
    })
  }, [listening])

  // Leaving the surface must release the microphone.
  useEffect(() => {
    return () => { void voiceAPI.setActive(false) }
  }, [])

  // ── Window controls (desktop host only) ────────────────────────
  // The takeover hides the shell topbar, so the board carries its own
  // circular min / max-restore / close buttons. Maximized state is polled on
  // mount + resize (same pattern as AIShellLayout).
  const [hostMode, setHostMode] = useState(false)
  const [winMaximised, setWinMaximised] = useState(false)
  useEffect(() => {
    const host = wailsWindowController.isHostMode()
    setHostMode(host)
    if (!host) return
    const refresh = () => {
      void wailsWindowController.isMaximised().then(setWinMaximised).catch(() => {})
    }
    refresh()
    window.addEventListener('resize', refresh)
    return () => window.removeEventListener('resize', refresh)
  }, [])

  const toggleMaximise = useCallback(() => {
    void wailsWindowController.toggleMaximise().then(() =>
      wailsWindowController.isMaximised().then(setWinMaximised).catch(() => {}),
    )
  }, [])

  // Stage the board's ambient backdrop (aurora wash + paper grain) on the body
  // while the surface is mounted (spec §6, app-background pattern).
  useEffect(() => {
    applyWorkbenchSurface(true)
    return () => applyWorkbenchSurface(false)
  }, [])

  const fallbackRegistry = useRef<WorkbenchCardRegistry | null>(null)
  if (fallbackRegistry.current === null) fallbackRegistry.current = createWorkbenchCardRegistry()
  const registry = registryProp ?? fallbackRegistry.current

  const keywords = useMemo(
    () => (summonKeywords && summonKeywords.length ? [...DEFAULT_TERMINAL_KEYWORDS, ...summonKeywords] : DEFAULT_TERMINAL_KEYWORDS),
    [summonKeywords],
  )
  const presence = useTerminalPresence({ keywords })
  const presenceRef = useRef(presence)
  presenceRef.current = presence
  const terminalTitle = t('terminalPanel.tab.shell')

  // The dock is the coordinator conversation: the workspace-global coordinator
  // agent is the workbench controller (it mounts the workbench-attention
  // bundle). Without one, the dock falls back to local-only rows.
  const chat = useCoordinatorChat()

  const subscribeApps = useCallback((listener: () => void) => appRegistry.subscribe(listener), [])
  const getApps = useCallback(() => appRegistry.getAll(), [])
  const appsAll = useSyncExternalStore(subscribeApps, getApps, getApps)
  // The launcher shows launchable apps only: entries with a view/panel
  // entrypoint. Pure bundles (e.g. builtin.sporecall — callables only,
  // registered for the mount catalog) stay off the board; they surface in the
  // mount/bundle catalog instead.
  const apps = useMemo(
    () => appsAll.filter((a) => a.entrypoints.some((e) => e.kind === 'view' || e.kind === 'panel')),
    [appsAll],
  )
  const appCards = useMemo(() => createAppCardDescriptors(apps), [apps])

  // Attention source of truth. includeHidden=true so the full card table
  // (including retired cards) drives the board's hidden set.
  const wb = useWorkbenchCards(true)
  const agentSessions = useWorkbenchAgents()
  const projectionActive = wb.ready

  const projectedTermVisible = useMemo(
    () => wb.cards.some(card => card.Kind === 'terminal' && card.Slot !== 'hidden'),
    [wb.cards],
  )
  // The terminal card is summoned by the actor (step keyword) OR locally (dock
  // input / shell output) — either path shows it.
  const terminalVisible = !presence.hidden || projectedTermVisible

  const terminalCard = useMemo(
    () =>
      createTerminalCardDescriptor({
        title: terminalTitle,
        projectRoot,
        active: terminalVisible,
        statusText: terminalTitle,
        onActivity: presence.noteOutput,
      }),
    [terminalTitle, projectRoot, terminalVisible, presence.noteOutput],
  )

  // The terminal card is a builtin descriptor; it mounts through the registry
  // like any other card. Its descriptor is presence-driven, so it re-registers
  // as the presence controller / projection flips.
  useEffect(() => {
    registry.register(terminalCard)
    return () => registry.unregister(terminalCard.id)
  }, [registry, terminalCard])

  // Chat cards: one descriptor per projected agent conversation. Like the
  // terminal card they mount through the registry, so the board's projection
  // overlay keeps the actor's attention (score / slot / pin) while the
  // descriptor supplies the render body and session summary.
  const chatCards = useMemo(
    () => createChatCardDescriptors(wb.cards, agentSessions),
    [wb.cards, agentSessions],
  )
  useEffect(() => {
    for (const card of chatCards) registry.register(card)
    return () => {
      for (const card of chatCards) registry.unregister(card.id)
    }
  }, [registry, chatCards])

  const registeredCards = useWorkbenchCardRegistry(registry)

  // Declare app cards (manifest name / icon / color / status) to the actor so
  // the projection carries real metadata instead of the app id placeholder.
  useEffect(() => {
    if (!wb.ready) return
    for (const card of appCards) {
      void wb.upsertCard({
        Id: card.id,
        Kind: card.kind,
        Title: card.title,
        Icon: card.icon,
        Color: card.color,
        StatusText: card.compactMeta.statusText,
      })
    }
  }, [appCards, wb.ready, wb.upsertCard])

  // Fill the actor's agent-card titles from the workspace Agents projection
  // (the step/turn event envelope only carries the emitter actor id). Each
  // fill fires at most once per id until the snapshot echoes the name back —
  // otherwise a snapshot that keeps the old title would re-trigger the upsert
  // on every apply and loop forever.
  const pendingTitleFills = useRef(new Set<string>())
  useEffect(() => {
    if (!wb.ready) return
    for (const state of wb.cards) {
      if (state.Kind !== 'chat' || !state.Id.startsWith('agent:')) continue
      const name = agentSessions.get(state.Id.slice('agent:'.length))?.name
      if (!name || name === state.Title) {
        pendingTitleFills.current.delete(state.Id)
        continue
      }
      if (pendingTitleFills.current.has(state.Id)) continue
      pendingTitleFills.current.add(state.Id)
      void wb.upsertCard({ Id: state.Id, Kind: 'chat', Title: name, Icon: 'sparkles' })
    }
  }, [wb.cards, wb.ready, agentSessions, wb.upsertCard])

  // Board cards: every projected card resolved to its render descriptor plus
  // any registered descriptor the actor does not (yet) know about.
  const boardCards = useMemo(() => {
    const bodies = new Map<string, WorkbenchCardDescriptor>()
    for (const descriptor of [...appCards, ...registeredCards]) bodies.set(descriptor.id, descriptor)
    const out: WorkbenchCardDescriptor[] = []
    const seen = new Set<string>()
    for (const state of wb.cards) {
      const id = projectionCardId(state)
      if (seen.has(id)) continue
      seen.add(id)
      const base = bodies.get(id)
      out.push(base ? withProjection(base, state) : projectionOnlyDescriptor(state))
    }
    for (const body of bodies.values()) {
      if (!seen.has(body.id)) out.push(body)
    }
    return out
  }, [wb.cards, appCards, registeredCards])

  const hidden = useMemo(() => {
    const set = new Set<string>()
    for (const state of wb.cards) {
      if (state.Slot === 'hidden') set.add(projectionCardId(state))
    }
    if (terminalVisible) set.delete(TERMINAL_CARD_ID)
    else set.add(TERMINAL_CARD_ID)
    return set
  }, [wb.cards, terminalVisible])

  const projectionOrder = useMemo(
    () => wb.cards.filter(state => state.Slot !== 'hidden' && state.Kind !== 'terminal').map(projectionCardId),
    [wb.cards],
  )

  // Board clicks dispatch to the actor when the projection is live; otherwise
  // the local layout engine self-scores (fallback path).
  const mutation = useMemo(() => {
    if (!projectionActive) return undefined
    return {
      promote: (id: string) => void wb.promote(id),
      setPinned: (id: string, pinned: boolean) => void wb.setPinned(id, pinned),
      setHidden: (id: string, hiddenState: boolean) => void wb.setHidden(id, hiddenState),
      setFrozen: (frozen: boolean) => void wb.setFrozen(frozen),
      setMaximized: (id: string, reason?: string) => void wb.setMaximized(id, reason),
    }
  }, [projectionActive, wb.promote, wb.setPinned, wb.setHidden, wb.setFrozen, wb.setMaximized])

  // Layout MODE (❄ freeze / full capture) is actor-owned while the projection
  // is live — the coordinator controller can read and switch it.
  const viewState = useMemo(
    () => (projectionActive ? { frozen: wb.frozen, maximizedId: wb.maximized || null } : null),
    [projectionActive, wb.frozen, wb.maximized],
  )

  const board = useWorkbenchLayout(
    boardCards,
    projectionActive ? { hidden, order: projectionOrder, mutation, view: viewState } : { hidden },
  )

  // Hydration settle window: the board's first paint happens before the actor
  // projection answers (app/agent cards are declared in the same beat, then the
  // snapshot and its watch pushes land), so slots and the terminal strip change
  // several times in a row. Animating every one of those reflows (0.95s each)
  // made the cards visibly shuffle the moment the board opened — transitions
  // stay off until the projection has been live for a beat, with a fallback so
  // the projection-less board animation resumes too. The window also resets
  // while the projected card table is still changing (declarations landing one
  // by one): settle on stability, not a fixed timestamp.
  const [boardSettled, setBoardSettled] = useState(false)
  const projectedCardCount = wb.cards.length
  useEffect(() => {
    if (boardSettled) return
    const timer = setTimeout(() => setBoardSettled(true), projectionActive ? 900 : 1600)
    return () => clearTimeout(timer)
  }, [projectionActive, boardSettled, projectedCardCount])

  const toggleConvo = useCallback((force?: boolean) => {
    setConvoOpen(prev => force ?? !prev)
  }, [])

  const togglePad = useCallback((force?: boolean) => {
    setPadOpen(prev => force ?? !prev)
  }, [])

  // Esc chain + ⌘K / ⌘J, mirroring the blackboard keymap: pad → proposal →
  // conversation → release.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const k = e.key.toLowerCase()
      if ((e.metaKey || e.ctrlKey) && k === 'k') {
        e.preventDefault()
        inputRef.current?.focus()
        return
      }
      if ((e.metaKey || e.ctrlKey) && k === 'j') {
        e.preventDefault()
        toggleConvo()
        return
      }
      if (e.key !== 'Escape') return
      if (padOpen) {
        togglePad(false)
        return
      }
      if (board.proposal) {
        board.vetoProposal()
        return
      }
      if (convoOpen) {
        toggleConvo(false)
        return
      }
      if (board.maximizedId) board.release()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [board, convoOpen, padOpen, toggleConvo, togglePad])

  // Keep the conversation list pinned to the latest row. Local rows serve the
  // unwired / still-resolving states; once the coordinator is wired its
  // timeline rows are authoritative (they include the optimistic envelope).
  const convoRows = chat.status === 'live' || chat.status === 'error' ? chat.rows : convo
  useEffect(() => {
    const list = vlistRef.current
    if (list && convoOpen) list.scrollTop = list.scrollHeight
  }, [convoRows, convoOpen])

  // Focus the dock input when the conversation unfolds (blackboard focuses
  // after the .42s expand animation; 440ms keeps the same cadence).
  useEffect(() => {
    if (!convoOpen) return
    const id = setTimeout(() => inputRef.current?.focus(), 440)
    return () => clearTimeout(id)
  }, [convoOpen])

  useEffect(() => {
    if (!padOpen) return
    setPadQuery('')
    const id = setTimeout(() => padQueryRef.current?.focus(), 60)
    return () => clearTimeout(id)
  }, [padOpen])

  const summonTerminal = useCallback(() => {
    presence.summon('launch')
    if (projectionActive) void wb.setHidden(TERMINAL_CARD_ID, false)
    inputRef.current?.focus()
  }, [presence, projectionActive, wb.setHidden])

  const retreatTerminal = useCallback(() => {
    presence.hide()
    if (projectionActive) void wb.setHidden(TERMINAL_CARD_ID, true)
  }, [presence, projectionActive, wb.setHidden])

  const handleSubmit = useCallback(
    (e: FormEvent) => {
      e.preventDefault()
      const text = draft.trim()
      if (!text) return
      presence.noteText(text, 'user')
      setDraft('')
      if (chat.status === 'live' || chat.status === 'error') {
        // Dock input is the coordinator conversation; rows arrive from the
        // shared timeline (optimistic user envelope + streamed replies). A
        // prior error is retried on the next submit.
        void chat.submit(text)
        return
      }
      // No wired coordinator (missing or still materializing): local-only
      // rows, with the terminal-summon beat from the blackboard reference.
      pushConvo('u', text)
      setTimeout(() => {
        if (!presenceRef.current.hidden) pushConvo('a', t('workbench.surface.replyTermSummoned'))
      }, 600)
    },
    [chat.status, chat.submit, draft, presence, pushConvo, t],
  )

  // Pad tile launch (blackboard launchApp): close the pad, then summon /
  // unhide + promote the card.
  const launchCard = useCallback(
    (id: string, title: string) => {
      togglePad(false)
      if (id === TERMINAL_CARD_ID) {
        summonTerminal()
        if (chat.status === 'none' || chat.status === 'resolving') pushConvo('a', t('workbench.surface.replyTermSummoned'))
        return
      }
      if (projectionActive) {
        void wb.setHidden(id, false)
        void wb.promote(id)
      } else {
        board.select(id)
      }
      if (chat.status === 'none' || chat.status === 'resolving') pushConvo('a', t('workbench.surface.replyAppLaunched', { title }))
    },
    [board, chat.status, projectionActive, pushConvo, summonTerminal, t, togglePad, wb.promote, wb.setHidden],
  )

  const proposalCard = board.proposal ? boardCards.find(d => d.id === board.proposal!.id) : undefined

  const padApps = useMemo(() => {
    const q = padQuery.trim().toLowerCase()
    if (!q) return appCards
    return appCards.filter(card => card.title.toLowerCase().includes(q))
  }, [appCards, padQuery])

  return (
    <div className="wb-surface">
      <div
        className={`wb-board${convoOpen ? ' veiled' : ''}${boardSettled ? '' : ' settling'}`}
        onPointerDownCapture={() => board.vetoProposal()}
      >
        <div className="wb-aurora wb-aurora--1" aria-hidden="true" />
        <div className="wb-aurora wb-aurora--2" aria-hidden="true" />
        <div className="wb-aurora wb-aurora--3" aria-hidden="true" />
        <div className="wb-grain" aria-hidden="true" />

        {/* Window drag region: the board covers the shell topbar, so this strip
            keeps the native titlebar drag behavior alive along the top edge. */}
        <div className="wb-drag-region" aria-hidden="true" />

        <div className="wb-corner">
          <button
            type="button"
            className="wb-cbtn"
            title={t('workbench.surface.exit')}
            aria-label={t('workbench.surface.exit')}
            onClick={e => {
              e.stopPropagation()
              onExit?.()
            }}
          >
            {ICON_EXIT}
          </button>
          <button
            type="button"
            className={`wb-cbtn${board.frozen ? ' on' : ''}`}
            title={t('workbench.surface.freeze')}
            aria-pressed={board.frozen}
            onClick={e => {
              e.stopPropagation()
              board.toggleFrozen()
            }}
          >
            {ICON_FREEZE}
          </button>
          <button
            type="button"
            className={`wb-cbtn${showScores ? ' on' : ''}`}
            title={t('workbench.surface.scoreDebug')}
            aria-label={t('workbench.surface.scoreDebug')}
            aria-pressed={showScores}
            onClick={e => {
              e.stopPropagation()
              setShowScores(v => !v)
            }}
          >
            {ICON_SCORE}
          </button>
          <button
            type="button"
            className="wb-cbtn"
            title={t('workbench.surface.theme')}
            aria-label={t('workbench.surface.theme')}
            onClick={e => {
              e.stopPropagation()
              onToggleTheme?.()
            }}
          >
            {isDark ? ICON_SUN : ICON_MOON}
          </button>
        </div>
        <div className="wb-corner wb-corner--r">
          <button
            type="button"
            className="wb-cbtn"
            title={t('workbench.surface.pad')}
            aria-label={t('workbench.surface.pad')}
            onClick={e => {
              e.stopPropagation()
              togglePad()
            }}
          >
            {ICON_PAD}
          </button>
          <button
            type="button"
            className={`wb-cbtn${convoOpen ? ' on' : ''}`}
            title={t('workbench.surface.convo')}
            aria-label={t('workbench.surface.convo')}
            aria-pressed={convoOpen}
            onClick={e => {
              e.stopPropagation()
              toggleConvo()
            }}
          >
            {ICON_CONVO}
          </button>
          {/* The takeover hides the shell topbar, so the Windows min / max /
              close trio rides here as circular glass buttons. Desktop host
              only — the web build has no window chrome. */}
          {hostMode && (
            <>
              <span className="wb-corner-sep" aria-hidden="true" />
              <button
                type="button"
                className="wb-cbtn"
                title={t('shell.topbar.window.minimize')}
                aria-label={t('shell.topbar.window.minimize')}
                onClick={e => {
                  e.stopPropagation()
                  void wailsWindowController.minimise()
                }}
              >
                <Minus size={13} />
              </button>
              <button
                type="button"
                className="wb-cbtn"
                title={winMaximised ? t('shell.topbar.window.restore') : t('shell.topbar.window.maximize')}
                aria-label={winMaximised ? t('shell.topbar.window.restore') : t('shell.topbar.window.maximize')}
                onClick={e => {
                  e.stopPropagation()
                  toggleMaximise()
                }}
              >
                {winMaximised ? <Copy size={13} /> : <Square size={12} />}
              </button>
              <button
                type="button"
                className="wb-cbtn wb-cbtn--close"
                title={t('shell.topbar.window.close')}
                aria-label={t('shell.topbar.window.close')}
                onClick={e => {
                  e.stopPropagation()
                  void wailsWindowController.quit()
                }}
              >
                <X size={14} />
              </button>
            </>
          )}
        </div>

        <div className="wb-field">
          {showScores && (
            <div
              className="wb-debug-chip"
              title={`${t('workbench.surface.scoreDebug')} · web ${buildVersion}`}
            >
              {projectionActive ? 'attention: actor' : 'attention: local'} · web {buildVersion}
            </div>
          )}
          {proposalCard && (
            <div className="wb-tag wb-tag--proposal">
              <span className="wb-tag-flag">⚑</span>
              <span>
                {t('workbench.surface.captureProposal', {
                  title: proposalCard.title,
                  reason: board.proposal!.reason,
                })}
              </span>
              <kbd>{t('workbench.surface.captureVeto')}</kbd>
            </div>
          )}
          {board.maximizedId && (
            <div className="wb-tag wb-tag--max">
              <span className="wb-tag-flag">⚑</span>
              <span>{t('workbench.surface.captureActive')}</span>
              <kbd>{t('workbench.surface.release')}</kbd>
            </div>
          )}

          {board.cards.length === 0 ? (
            <button type="button" className="wb-launcher" onClick={summonTerminal}>
              <span className="wb-launcher-title">{terminalTitle}</span>
              <span className="wb-launcher-hint">{t('workbench.surface.launcher')}</span>
            </button>
          ) : (
            board.cards.map(view => (
              <WorkbenchCard
                key={view.card.id}
                card={view.card}
                placement={view.placement}
                expanded={view.expanded}
                focused={view.focused}
                pinned={view.pinned}
                maximized={view.maximized}
                proposing={view.proposing}
                showScore={showScores}
                attentionScore={view.score}
                attribution={view.card.attribution}
                onSelect={() => board.select(view.card.id)}
                onTogglePin={() => board.togglePin(view.card.id)}
                onClose={view.card.kind === 'terminal' ? retreatTerminal : undefined}
              />
            ))
          )}
        </div>

        <div
          className={`wb-frost${convoOpen ? ' open' : ''}`}
          onClick={() => toggleConvo(false)}
          aria-hidden="true"
        />

        <form className={`wb-dock${convoOpen ? ' open' : ''}${listening ? ' listening' : ''}`} onSubmit={handleSubmit}>
          <div className="wb-convo">
            <div className="wb-vlist" ref={vlistRef}>
              {convoRows.map(row => (
                <div key={row.id} className={`wb-vrow${row.who === 'u' ? ' u' : ''}`}>
                  {row.text}
                </div>
              ))}
              {chat.status === 'resolving' && convoRows.length === 0 && (
                <div className="wb-vrow wb-vrow--hint">{t('workbench.surface.convoConnecting')}</div>
              )}
              {chat.status === 'live' && convoRows.length === 0 && (
                <div className="wb-vrow wb-vrow--hint">{t('workbench.surface.convoGreeting')}</div>
              )}
              {chat.error && (
                <div className="wb-vrow wb-vrow--hint wb-vrow--error" title={chat.error}>
                  {t('workbench.surface.convoError')}
                </div>
              )}
              {(chat.status === 'live' || chat.status === 'error') && chat.streaming && (
                <div className="wb-vrow wb-vrow--pending" aria-hidden="true" />
              )}
            </div>
          </div>
          <div className="wb-halo">
            <div className="wb-pill">
              <span className="wb-spark" aria-hidden="true">{ICON_SPARK}</span>
              <input
                ref={inputRef}
                className="wb-dock-input"
                value={draft}
                onChange={e => setDraft(e.target.value)}
                placeholder={t('shell.workbench.commandPlaceholder')}
                autoComplete="off"
              />
              <span className="wb-wave" aria-hidden="true">{WAVE_BARS}</span>
              <button
                type="button"
                className="wb-mic"
                title={t('workbench.surface.mic')}
                aria-label={t('workbench.surface.mic')}
                aria-pressed={listening}
                onClick={e => {
                  e.stopPropagation()
                  toggleListening()
                }}
              >
                {ICON_MIC}
              </button>
            </div>
          </div>
        </form>

        <div
          className={`wb-pad${padOpen ? ' open' : ''}`}
          onClick={e => {
            if (e.target === e.currentTarget) togglePad(false)
          }}
        >
          <div className="wb-psearch">
            <span className="wb-spark" aria-hidden="true">{ICON_SPARK}</span>
            <input
              ref={padQueryRef}
              value={padQuery}
              onChange={e => setPadQuery(e.target.value)}
              placeholder={t('workbench.surface.searchApps')}
              autoComplete="off"
            />
          </div>
          <div className="wb-pgrid">
            <button type="button" className="wb-tile" onClick={() => launchCard(TERMINAL_CARD_ID, terminalTitle)}>
              <span className="wb-tile-ic wb-tile-ic--term">{ICON_TERM}</span>
              <span>{terminalTitle}</span>
            </button>
            {padApps.map(card => (
              <button key={card.id} type="button" className="wb-tile" onClick={() => launchCard(card.id, card.title)}>
                <span
                  className="wb-tile-ic"
                  style={card.color ? { background: card.color } : undefined}
                >
                  {workbenchCardIcon(card, 26)}
                </span>
                <span>{card.title}</span>
              </button>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
