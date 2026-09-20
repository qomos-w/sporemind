import { setConsoleLogSink, getConsoleLogs } from '@qomos/sporemind-shell'
import { isWails } from './runtime'
import { WriteConsoleLog } from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'

// Console entries can carry whole serialized objects; shipping multi-KB
// strings through the Wails IPC on every log call risks wedging the binding
// (and violates the long-field truncation rule). 4KB keeps enough context.
const MAX_MESSAGE_BYTES = 4096

const HEARTBEAT_INTERVAL_MS = 60_000

function truncate(message: string): string {
  if (message.length <= MAX_MESSAGE_BYTES) return message
  return message.slice(0, MAX_MESSAGE_BYTES) + '...(truncated)'
}

let failures = 0
let lastError = ''
let heartbeatTimer: ReturnType<typeof setInterval> | null = null

function ship(level: string, message: string, location: string, time: number): void {
  WriteConsoleLog(level, truncate(message), location, time).catch(() => {
    // One retry for transient IPC hiccups; a persistent failure must be
    // visible in the next heartbeat instead of silently swallowing.
    setTimeout(() => {
      WriteConsoleLog(level, truncate(message), location, time).catch((err: unknown) => {
        failures++
        lastError = err instanceof Error ? err.message : String(err)
      })
    }, 500)
  })
}

function startHeartbeat(): void {
  if (heartbeatTimer !== null) return
  heartbeatTimer = setInterval(() => {
    // Liveness probe for the whole capture pipeline: if this line stops
    // appearing in logs/console/<date>.jsonl, the patch→sink→Wails→store
    // chain is dead (2026-09-14 incident: capture froze silently at
    // 09:08:17 with zero diagnostics on either side).
    ship(
      'info',
      `[console-patch] heartbeat buffered=${getConsoleLogs().length} failures=${failures}` +
        (lastError ? ` last_error=${lastError.slice(0, 200)}` : ''),
      'console-log-persist',
      Date.now(),
    )
  }, HEARTBEAT_INTERVAL_MS)
}

export function installConsoleLogPersist(): void {
  if (!isWails()) return
  if (heartbeatTimer !== null) clearInterval(heartbeatTimer)
  heartbeatTimer = null
  failures = 0
  lastError = ''
  setConsoleLogSink((entry) => {
    // Debug entries stay in the in-app ring buffer only — shipping each one
    // to the backend is a Wails IPC round-trip, and streaming pipelines emit
    // them at high frequency.
    if (entry.level === 'debug') return
    ship(entry.level, entry.message, entry.location ?? '', entry.time)
  })
  startHeartbeat()
}
