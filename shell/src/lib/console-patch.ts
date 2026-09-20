/**
 * console-patch — intercepts console.log/warn/error/debug, stores entries in a
 * local ring buffer, and forwards them to the host console bridge if present.
 *
 * Call installConsolePatch() once before mounting React.
 */

export interface ConsoleEntry {
  level: string   // "info" | "warn" | "error" | "debug"
  message: string
  time: number    // unix milliseconds
  location?: string // file:line
}

// ── Local in-memory store ─────────────────────────────────────────────────────

const MAX_ENTRIES = 2000

let _entries: ConsoleEntry[] = []
const _listeners = new Set<() => void>()
let _persistSink: ((entry: ConsoleEntry) => void) | null = null

function _notify() {
  for (const cb of _listeners) _listeners.has(cb) && cb()
}

function _push(entry: ConsoleEntry) {
  _entries = [..._entries, entry]
  if (_entries.length > MAX_ENTRIES) _entries = _entries.slice(-1000)
  _notify()
  _persistSink?.(entry)
}

/** Subscribe to console log changes. Returns an unsubscribe function. */
export function subscribeConsoleLogs(cb: () => void): () => void {
  _listeners.add(cb)
  return () => { _listeners.delete(cb) }
}

/** Returns the current snapshot of console log entries. */
export function getConsoleLogs(): ConsoleEntry[] {
  return _entries
}

/** Clears all stored entries. */
export function clearConsoleLogs(): void {
  _entries = []
  _notify()
}

/** Registers a callback called for every new console entry so it can be persisted. */
export function setConsoleLogSink(sink: ((entry: ConsoleEntry) => void) | null): void {
  _persistSink = sink
}

/** Prepend historical entries loaded from disk without triggering the sink. */
export function loadConsoleLogs(entries: ConsoleEntry[]): void {
  if (entries.length === 0) return
  _entries = entries.concat(_entries)
  if (_entries.length > MAX_ENTRIES) _entries = _entries.slice(-MAX_ENTRIES)
  _notify()
}

// ── Console patch ─────────────────────────────────────────────────────────────

let _patched = false

/** Install the console patch. Safe to call multiple times (idempotent). */
export function installConsolePatch(): void {
  if (_patched) return
  _patched = true

  const origLog   = console.log.bind(console)
  const origWarn  = console.warn.bind(console)
  const origError = console.error.bind(console)
  const origDebug = console.debug.bind(console)

  function _capture(level: string, args: unknown[], location: string, _file: string, _lineNum: number) {
    try {
      const message = args.map(a => {
        if (typeof a === 'string') return a
        try {
          return JSON.stringify(a, (_key, val) => {
            if (typeof val === 'function') return '[Function]'
            if (val instanceof Error) return val.toString()
            return val
          })
        } catch {
          return String(a)
        }
      }).join(' ')

      const entry: ConsoleEntry = {
        level,
        message,
        time: Date.now(),
        location: location || undefined,
      }
      _push(entry)
      // Don't forward console logs to backend — they'd appear as duplicate
      // backend entries alongside the real Go logs. Console source is sufficient.
    } catch {
      // Silently ignore capture errors to avoid breaking console
    }
  }

  const _hook = (origMethod: (...args: unknown[]) => void, level: string) => {
    return function(this: Console, ...args: unknown[]) {
      origMethod.apply(this, args)
      // Debug-level entries skip the stack capture: new Error().stack costs
      // real time per call, and streaming pipelines can emit hundreds of
      // debug lines per second. Message-only capture keeps them cheap.
      if (level === 'debug') {
        queueMicrotask(() => _capture(level, args, '', '', 0))
        return
      }
      try {
        const stack = new Error().stack || ''
        const stackLine = stack.split('\n')[2] || ''
        // Vite dev appends ?t=... cache-busters; strip query strings before matching.
        const cleaned = stackLine.replace(/\?[^:)\s]*/g, '')
        const match = cleaned.match(/\/([^\/]+\/[^\/]+\.[jt]sx?):(\d+):(\d+)/)
        const file = match ? match[1]! : ''
        const lineNum = match ? parseInt(match[2]!, 10) : 0
        const location = file ? `${file}:${lineNum}` : ''
        queueMicrotask(() => _capture(level, args, location, file, lineNum))
      } catch {}
    }
  }

  console.log   = _hook(origLog, 'info')
  console.warn  = _hook(origWarn, 'warn')
  console.error = _hook(origError, 'error')
  console.debug = _hook(origDebug, 'debug')

  // Expose for backend pull via Wails WindowEval or event roundtrip.
  ;(window as any).__MYXOS_GET_CONSOLE_LOGS__ = (): string => {
    return JSON.stringify(_entries)
  }
}
