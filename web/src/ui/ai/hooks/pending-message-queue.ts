export interface PendingEntry {
  clientId: string
  text: string
  messageId?: string | undefined
  state: 'pending' | 'failed'
  timestamp: string
  /** ISO time when the entry transitioned to 'failed'. Set by markFailed so
   *  the GC sweep can drop it after a grace period (AUDIT 1.7). */
  failedAt?: string
}

const DEFAULT_TTL_MS = 30_000
/** How long a failed entry stays visible before being garbage-collected.
 *  Gives the UI a window to show "failed" badge, then clears the slot so
 *  long sessions don't accumulate dead entries (AUDIT 1.7). */
const DEFAULT_FAILED_GC_MS = 60_000

export class PendingMessageQueue {
  private _entries = new Map<string, PendingEntry>()
  private _listeners = new Set<() => void>()
  private _ttlMs: number
  private _failedGcMs: number
  private _timers = new Map<string, ReturnType<typeof setTimeout>>()
  private _snapshotCache: readonly PendingEntry[] | null = null

  constructor(opts?: { ttlMs?: number; failedGcMs?: number }) {
    this._ttlMs = opts?.ttlMs ?? DEFAULT_TTL_MS
    this._failedGcMs = opts?.failedGcMs ?? DEFAULT_FAILED_GC_MS
  }

  enqueue(clientId: string, text: string): void {
    const entry: PendingEntry = {
      clientId,
      text,
      state: 'pending',
      timestamp: new Date().toISOString(),
    }
    this._entries.set(clientId, entry)
    this._startTTL(clientId)
    this._notify()
  }

  bindMessageId(clientId: string, messageId: string): void {
    const entry = this._entries.get(clientId)
    if (!entry) return
    entry.messageId = messageId
    // The backend accepted the message (chat_submit returned a MessageID).
    // A queued PendingSubmit can legitimately wait for the rest of a long
    // running turn before its user_inject step arrives, so the local failure
    // TTL no longer applies; cleanup comes from step-event confirmation,
    // explicit stop (markAllFailed), or clear/reset.
    this._clearTimer(clientId)
    this._notify()
  }

  confirmByMessageId(messageId: string): boolean {
    for (const [clientId, entry] of this._entries) {
      if (entry.messageId === messageId) {
        this._entries.delete(clientId)
        this._clearTimer(clientId)
        this._notify()
        return true
      }
    }
    return false
  }

  confirmByExactText(text: string): boolean {
    // AUDIT 1.4: previously returned the first insertion-order match. When
    // two entries shared the same text (e.g. user resent the same message)
    // and their steps arrived out of order, the wrong entry was confirmed.
    // Now: collect ALL matching entries; if there's exactly one, confirm it.
    // If multiple match, defer to the messageId-based path (which is
    // unambiguous) and let TTL clean up if that never arrives.
    //
    // Note: we no longer skip entries with a bound messageId. When the
    // backend promotes an orphaned PendingSubmit into a new user turn via
    // createUserTurn, the step's OriginMessageID is "user-turn-N" — not the
    // original ps.ID. confirmByMessageId fails on the mismatch, so the
    // text fallback must be allowed to rescue the confirmation.
    const matches: string[] = []
    for (const [clientId, entry] of this._entries) {
      if (entry.text === text) {
        matches.push(clientId)
      }
    }
    if (matches.length !== 1) return false
    const clientId = matches[0]!
    this._entries.delete(clientId)
    this._clearTimer(clientId)
    this._notify()
    return true
  }

  removeByClientId(clientId: string): boolean {
    const entry = this._entries.get(clientId)
    if (!entry) return false
    this._entries.delete(clientId)
    this._clearTimer(clientId)
    this._notify()
    return true
  }

  markFailed(clientId: string): void {
    const entry = this._entries.get(clientId)
    if (!entry || entry.state === 'failed') return
    entry.state = 'failed'
    entry.failedAt = new Date().toISOString()
    this._clearTimer(clientId)
    // AUDIT 1.7: schedule GC so failed entries don't accumulate. The UI has
    // a full minute (default) to render the failure badge before the slot
    // is reclaimed.
    const gc = setTimeout(() => {
      this._entries.delete(clientId)
      this._clearTimer(clientId)
      this._notify()
    }, this._failedGcMs)
    this._timers.set(clientId, gc)
    this._notify()
  }

  /** Mark all currently-pending entries as failed. Used by timeline.stop()
   *  so the UI immediately reflects that the in-flight send was cancelled
   *  instead of waiting 30s for TTL (AUDIT 1.8). */
  markAllFailed(): void {
    for (const clientId of Array.from(this._entries.keys())) {
      this.markFailed(clientId)
    }
  }

  snapshot(): readonly PendingEntry[] {
    if (!this._snapshotCache) {
      this._snapshotCache = Array.from(this._entries.values())
    }
    return this._snapshotCache
  }

  subscribe(cb: () => void): () => void {
    this._listeners.add(cb)
    return () => { this._listeners.delete(cb) }
  }

  clear(): void {
    for (const timer of this._timers.values()) {
      clearTimeout(timer)
    }
    this._timers.clear()
    this._entries.clear()
    this._notify()
  }

  /** Number of entries (useful for tests). */
  get size(): number {
    return this._entries.size
  }

  private _startTTL(clientId: string): void {
    this._clearTimer(clientId)
    const timer = setTimeout(() => {
      this.markFailed(clientId)
    }, this._ttlMs)
    this._timers.set(clientId, timer)
  }

  private _clearTimer(clientId: string): void {
    const timer = this._timers.get(clientId)
    if (timer != null) {
      clearTimeout(timer)
      this._timers.delete(clientId)
    }
  }

  private _notify(): void {
    this._snapshotCache = null
    for (const l of this._listeners) l()
  }
}
