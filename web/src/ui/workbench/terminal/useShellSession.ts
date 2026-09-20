import { useCallback, useEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'
import { Terminal as XtermTerminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'

import { client } from '../../../application/generated-client'
import * as shellClient from '../../../gen-clients/shell/client'
import type { ShellSessionOutputEvent } from '../../../gen-clients/system/types'

/**
 * Reusable shell-session data plane for the workbench terminal card.
 *
 * Reuses the existing interactive shell execution flow with zero new protocol:
 *   subscribe shell.session_output (service event, filtered by SessionId)
 *   BEFORE opening so no prompt output is lost →
 *   shell.session_open(cols/rows/workingDir) →
 *   shell.session_resize(fitted geometry) →
 *   shell.session_fetch(0) replay of the per-session ring buffer, applied
 *   idempotently by the monotonic Idx →
 *   keystrokes → shell.session_write; resize → shell.session_resize;
 *   unmount → unsubscribe + shell.session_close + dispose.
 *
 * The identical lifecycle is implemented (with extra panel chrome) by
 * ShellConsoleTab; this hook factors the transport/protocol half out so the
 * workbench card can render its own skin over the same session stream.
 */

/** Below these sizes the container is collapsed mid-transition; fitting then
 *  clamps cols to 2 and lossy-reflows the buffer. */
const MIN_FIT_WIDTH_PX = 64
const MIN_FIT_HEIGHT_PX = 24

/** Maximum events buffered while the session id is still unknown. */
const MAX_PENDING_EVENTS = 64

const STDERR_ANSI_OPEN = '\x1b[31m'
const STDERR_ANSI_CLOSE = '\x1b[0m'
const DIM_ANSI_OPEN = '\x1b[90m'
const DIM_ANSI_CLOSE = '\x1b[0m'

export type ShellSessionPhase = 'connecting' | 'ready' | 'exited' | 'error'

export interface ShellSessionState {
  phase: ShellSessionPhase
  exitCode?: number
  error?: string
}

export interface UseShellSessionOptions {
  /** Working directory handed to shell.session_open. Omitted → backend default. */
  projectRoot?: string
  /** Open a session while true; closing the panel (false) closes the session. */
  enabled?: boolean
  /** Called for every newly applied output chunk (dedup by Idx already applied). */
  onOutput?: (ev: ShellSessionOutputEvent) => void
  /** Initial geometry before the FitAddon refines it. Defaults 80×24. */
  cols?: number
  rows?: number
}

export interface UseShellSessionResult {
  containerRef: RefObject<HTMLDivElement | null>
  state: ShellSessionState
  sessionId: string | null
  /** Write raw bytes to the session stdin (dropped before open / after exit). */
  send: (data: string) => void
  /** Clear the terminal scrollback. */
  clear: () => void
  focus: () => void
}

export function useShellSession(options: UseShellSessionOptions = {}): UseShellSessionResult {
  const { projectRoot, enabled = true } = options

  const onOutputRef = useRef(options.onOutput)
  onOutputRef.current = options.onOutput

  const containerRef = useRef<HTMLDivElement | null>(null)
  const termRef = useRef<XtermTerminal | null>(null)
  const sessionIdRef = useRef<string | null>(null)
  const exitedRef = useRef(false)
  const [state, setState] = useState<ShellSessionState>({ phase: 'connecting' })
  const [sessionId, setSessionId] = useState<string | null>(null)

  useEffect(() => {
    if (!enabled) return
    const container = containerRef.current
    if (!container) return

    let cancelled = false
    let unsubscribe: (() => void) | null = null
    exitedRef.current = false
    sessionIdRef.current = null
    setSessionId(null)
    setState({ phase: 'connecting' })

    const term = new XtermTerminal({
      cols: options.cols ?? 80,
      rows: options.rows ?? 24,
      cursorBlink: true,
      fontFamily: 'Menlo, Consolas, "DejaVu Sans Mono", monospace',
      fontSize: 12,
      scrollback: 5000,
      // Blend with the card background so the panel/theme shows through.
      allowTransparency: true,
      theme: { background: 'rgba(0, 0, 0, 0)' },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(container)
    termRef.current = term

    const containerTooSmall = () =>
      container.clientWidth < MIN_FIT_WIDTH_PX || container.clientHeight < MIN_FIT_HEIGHT_PX
    if (!containerTooSmall()) {
      try {
        fit.fit()
      } catch {
        /* container gone */
      }
    }

    const markExited = (ev: ShellSessionOutputEvent) => {
      exitedRef.current = true
      const code = ev.ExitCode ?? 0
      setState({ phase: 'exited', exitCode: code })
      term.write(`\r\n${DIM_ANSI_OPEN}[session exited (${code})]${DIM_ANSI_CLOSE}\r\n`)
    }

    // Idempotent application: each session assigns a monotonically increasing
    // Idx; replay and live events are de-duplicated by it.
    let lastIdx = -1
    const pendingEvents: ShellSessionOutputEvent[] = []

    const applyEvent = (ev: ShellSessionOutputEvent) => {
      if (ev.SessionId !== sessionIdRef.current) return
      if (typeof ev.Idx !== 'number' || ev.Idx <= lastIdx) return
      lastIdx = ev.Idx
      onOutputRef.current?.(ev)
      if (ev.Kind === 'stdout') {
        term.write(ev.Data)
      } else if (ev.Kind === 'stderr') {
        term.write(`${STDERR_ANSI_OPEN}${ev.Data}${STDERR_ANSI_CLOSE}`)
      } else if (ev.Kind === 'exit') {
        markExited(ev)
      }
    }

    const flushPendingEvents = () => {
      const sid = sessionIdRef.current
      if (!sid) return
      for (const ev of pendingEvents) {
        if (ev.SessionId === sid) applyEvent(ev)
      }
      pendingEvents.length = 0
    }

    const onOutputEvent = (ev: ShellSessionOutputEvent) => {
      if (sessionIdRef.current === null) {
        // The session id is not known yet (subscribed before session_open).
        // Buffer a small window and drain once the id arrives; fetch(0) covers
        // the bulk of history.
        pendingEvents.push(ev)
        if (pendingEvents.length > MAX_PENDING_EVENTS) pendingEvents.shift()
        return
      }
      applyEvent(ev)
    }

    // Subscribe *before* opening so no window-of-creation output is lost.
    unsubscribe = shellClient.OnShellSessionOutput(client, onOutputEvent)

    void (async () => {
      try {
        const resp = await shellClient.sessionOpen(client, {
          Cols: term.cols,
          Rows: term.rows,
          WorkingDirectory: projectRoot,
        })
        if (cancelled) {
          void shellClient.sessionClose(client, { SessionId: resp.SessionId }).catch(() => {})
          return
        }
        sessionIdRef.current = resp.SessionId
        setSessionId(resp.SessionId)
        void shellClient.sessionResize(client, {
          SessionId: resp.SessionId,
          Cols: term.cols,
          Rows: term.rows,
        }).catch(() => { /* session may be closing */ })
        const replay = await shellClient.sessionFetch(client, {
          SessionId: resp.SessionId,
          FromIdx: 0,
        })
        if (cancelled) return
        for (const chunk of replay.Chunks) applyEvent(chunk)
        flushPendingEvents()
        setState((prev) => (prev.phase === 'exited' ? prev : { phase: 'ready' }))
      } catch (e) {
        if (cancelled) return
        const msg = e instanceof Error ? e.message : String(e)
        setState({ phase: 'error', error: msg })
        term.write(`\r\n${STDERR_ANSI_OPEN}${msg}${STDERR_ANSI_CLOSE}\r\n`)
        if (unsubscribe) {
          unsubscribe()
          unsubscribe = null
        }
      }
    })()

    // Forward keystrokes to session stdin (dropped before open / after exit).
    const inputDisp = term.onData((data) => {
      const sid = sessionIdRef.current
      if (!sid || exitedRef.current) return
      void shellClient.sessionWrite(client, { SessionId: sid, Data: data }).catch(() => {})
    })

    // Debounced resize → fit → session_resize, skipping identical geometry.
    let resizeTimer: ReturnType<typeof setTimeout> | null = null
    let lastSentCols = term.cols
    let lastSentRows = term.rows
    const resizeObs = new ResizeObserver(() => {
      if (containerTooSmall()) return
      if (resizeTimer) clearTimeout(resizeTimer)
      resizeTimer = setTimeout(() => {
        resizeTimer = null
        if (containerTooSmall()) return
        try {
          fit.fit()
          if (
            term.cols >= 10 && term.rows >= 2 &&
            (term.cols !== lastSentCols || term.rows !== lastSentRows)
          ) {
            lastSentCols = term.cols
            lastSentRows = term.rows
            const sid = sessionIdRef.current
            if (sid) {
              void shellClient.sessionResize(client, {
                SessionId: sid,
                Cols: term.cols,
                Rows: term.rows,
              }).catch(() => { /* session may be closing */ })
            }
          }
        } catch {
          /* container gone */
        }
      }, 80)
    })
    resizeObs.observe(container)

    term.focus()

    return () => {
      cancelled = true
      inputDisp.dispose()
      if (resizeTimer) clearTimeout(resizeTimer)
      resizeObs.disconnect()
      if (unsubscribe) {
        unsubscribe()
        unsubscribe = null
      }
      const sid = sessionIdRef.current
      if (sid) {
        void shellClient.sessionClose(client, { SessionId: sid }).catch(() => {})
      }
      sessionIdRef.current = null
      setSessionId(null)
      try {
        term.dispose()
      } catch {
        /* already gone */
      }
      termRef.current = null
    }
  }, [enabled, projectRoot])

  const send = useCallback((data: string) => {
    const sid = sessionIdRef.current
    if (!sid || exitedRef.current) return
    void shellClient.sessionWrite(client, { SessionId: sid, Data: data }).catch(() => {})
  }, [])

  const clear = useCallback(() => {
    termRef.current?.clear()
  }, [])

  const focus = useCallback(() => {
    termRef.current?.focus()
  }, [])

  return { containerRef, state, sessionId, send, clear, focus }
}
