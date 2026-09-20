import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { PendingMessageQueue } from './pending-message-queue'
import { AgentSession } from './agent-session'

describe('PendingMessageQueue', () => {
  let queue: PendingMessageQueue

  beforeEach(() => {
    vi.useFakeTimers()
    queue = new PendingMessageQueue()
  })

  afterEach(() => {
    queue.clear()
    vi.useRealTimers()
  })

  // ── D1: enqueue ──

  it('D1: enqueue adds a pending entry to snapshot', () => {
    queue.enqueue('c1', 'hello')
    const snap = queue.snapshot()
    expect(snap).toHaveLength(1)
    expect(snap[0]).toMatchObject({ clientId: 'c1', text: 'hello', state: 'pending' })
  })

  it('D1: enqueue notifies subscribers', () => {
    const cb = vi.fn()
    queue.subscribe(cb)
    queue.enqueue('c1', 'hi')
    expect(cb).toHaveBeenCalledTimes(1)
  })

  // ── D2: bindMessageId ──

  it('D2: bindMessageId sets messageId and keeps state pending', () => {
    queue.enqueue('c1', 'hello')
    queue.bindMessageId('c1', 'm1')
    const snap = queue.snapshot()
    expect(snap[0]).toMatchObject({ clientId: 'c1', messageId: 'm1', state: 'pending' })
  })

  it('D2: bindMessageId notifies subscribers', () => {
    queue.enqueue('c1', 'hello')
    const cb = vi.fn()
    queue.subscribe(cb)
    queue.bindMessageId('c1', 'm1')
    expect(cb).toHaveBeenCalledTimes(1)
  })

  it('D2: bindMessageId is a no-op for unknown clientId', () => {
    queue.bindMessageId('nonexistent', 'm1')
    expect(queue.snapshot()).toHaveLength(0)
  })

  it('D2: bindMessageId cancels the TTL — backend-confirmed entries stay pending past TTL', () => {
    // A queued PendingSubmit waits for the running turn's next step boundary,
    // which can exceed the 30s TTL. Once the backend accepted the message the
    // entry must not be locally failed (otherwise the optimistic queued
    // bubble vanishes until the real step arrives).
    queue.enqueue('c1', 'hello')
    queue.bindMessageId('c1', 'ps-1')
    vi.advanceTimersByTime(120_001)
    const snap = queue.snapshot()
    expect(snap).toHaveLength(1)
    expect(snap[0]).toMatchObject({ clientId: 'c1', messageId: 'ps-1', state: 'pending' })
  })

  // ── D3: confirmByMessageId (primary path) ──

  it('D3: confirmByMessageId removes the entry and returns true', () => {
    queue.enqueue('c1', 'hello')
    queue.bindMessageId('c1', 'm1')
    const ok = queue.confirmByMessageId('m1')
    expect(ok).toBe(true)
    expect(queue.snapshot()).toHaveLength(0)
  })

  it('D3: confirmByMessageId returns false for unknown messageId', () => {
    const ok = queue.confirmByMessageId('unknown')
    expect(ok).toBe(false)
  })

  it('D3: confirmByMessageId notifies subscribers', () => {
    queue.enqueue('c1', 'hello')
    queue.bindMessageId('c1', 'm1')
    const cb = vi.fn()
    queue.subscribe(cb)
    queue.confirmByMessageId('m1')
    expect(cb).toHaveBeenCalledTimes(1)
  })

  // ── D4: FIFO behavior ──

  it('D4: confirmByMessageId removes oldest when two entries share the same messageId', () => {
    // Both entries are bound to the same messageId (unusual but possible).
    // confirmByMessageId should remove the first (oldest) match.
    queue.enqueue('c1', 'first')
    queue.enqueue('c2', 'second')
    queue.bindMessageId('c1', 'shared')
    queue.bindMessageId('c2', 'shared')
    expect(queue.size).toBe(2)

    const ok = queue.confirmByMessageId('shared')
    expect(ok).toBe(true)
    expect(queue.size).toBe(1)
    expect(queue.snapshot()[0]!.clientId).toBe('c2')
  })

  // ── D5: confirmByExactText (fallback) ──

  it('D5: confirmByExactText removes entry with matching text and no messageId', () => {
    queue.enqueue('c1', 'hello world')
    const ok = queue.confirmByExactText('hello world')
    expect(ok).toBe(true)
    expect(queue.snapshot()).toHaveLength(0)
  })

  it('D5: confirmByExactText matches entries even with a messageId binding (orphan rescue)', () => {
    // When the backend promotes an orphaned PendingSubmit into a new user
    // turn via createUserTurn, the step's OriginMessageID is "user-turn-N"
    // — not the original ps.ID that was bound. confirmByMessageId fails on
    // the mismatch, so the text fallback must rescue the confirmation.
    queue.enqueue('c1', 'hello')
    queue.bindMessageId('c1', 'm1')
    const ok = queue.confirmByExactText('hello')
    expect(ok).toBe(true)
    expect(queue.size).toBe(0)
  })

  it('D5: confirmByExactText returns false for non-matching text', () => {
    queue.enqueue('c1', 'hello')
    const ok = queue.confirmByExactText('goodbye')
    expect(ok).toBe(false)
  })

  // ── D6: TTL expiry ──

  it('D6: entry state becomes failed after TTL, still in snapshot', () => {
    queue.enqueue('c1', 'hello')
    vi.advanceTimersByTime(30_001)
    const snap = queue.snapshot()
    expect(snap).toHaveLength(1)
    expect(snap[0]!.state).toBe('failed')
  })

  it('D6: TTL does not fire before expiry', () => {
    queue.enqueue('c1', 'hello')
    vi.advanceTimersByTime(20_000)
    expect(queue.snapshot()[0]!.state).toBe('pending')
  })

  it('uses custom TTL from constructor', () => {
    const short = new PendingMessageQueue({ ttlMs: 5000 })
    short.enqueue('c1', 'hello')
    vi.advanceTimersByTime(5001)
    expect(short.snapshot()[0]!.state).toBe('failed')
    short.clear()
  })

  // ── D7: retry after failure ──

  it('D7: enqueue after failure creates a new entry, not reusing the old one', () => {
    queue.enqueue('c1', 'hello')
    vi.advanceTimersByTime(30_001)
    expect(queue.snapshot()[0]!.state).toBe('failed')

    // Re-enqueue with same clientId — should create fresh entry.
    queue.enqueue('c1', 'hello again')
    const snap = queue.snapshot()
    expect(snap).toHaveLength(1)
    expect(snap[0]).toMatchObject({ clientId: 'c1', text: 'hello again', state: 'pending' })
  })

  // ── D8: clear ──

  it('D8: clear empties the queue and stops pending timers', () => {
    queue.enqueue('c1', 'hello')
    queue.enqueue('c2', 'world')
    queue.clear()
    expect(queue.snapshot()).toHaveLength(0)
    // Timers should be cleared — advance time should not trigger markFailed.
    vi.advanceTimersByTime(30_001)
    expect(queue.snapshot()).toHaveLength(0)
  })

  it('D8: clear notifies subscribers', () => {
    queue.enqueue('c1', 'hello')
    const cb = vi.fn()
    queue.subscribe(cb)
    queue.clear()
    expect(cb).toHaveBeenCalledTimes(1)
  })

  // ── markFailed ──

  it('markFailed changes state to failed and cancels TTL timer', () => {
    queue.enqueue('c1', 'hello')
    queue.markFailed('c1')
    expect(queue.snapshot()[0]!.state).toBe('failed')
    // TTL timer was cleared — advancing should not cause double-fail notify.
    const cb = vi.fn()
    queue.subscribe(cb)
    vi.advanceTimersByTime(30_001)
    // state already failed, no additional notification from markFailed timer.
    expect(queue.snapshot()[0]!.state).toBe('failed')
  })

  it('markFailed is a no-op for unknown clientId', () => {
    queue.markFailed('nope')
    expect(queue.snapshot()).toHaveLength(0)
  })
})

// ── D9/D10/D11: Integration tests with AgentSession ──

describe('PendingMessageQueue — AgentSession integration', () => {
  let session: AgentSession

  beforeEach(() => {
    session = new AgentSession('agent-1')
  })

  afterEach(() => {
    session.release()
  })

  // D.10: pushUserMessage returns clientId that matches the queue entry

  it('D10: enqueue via addPendingUserMessage stores entry with matching clientId', () => {
    session.addPendingUserMessage('c1', 'hello')
    const entries = session.getPendingUserMessages()
    expect(entries.length).toBe(1)
    expect(entries[0]!.clientId).toBe('c1')
    expect(entries[0]!.text).toBe('hello')
  })

  // D.11: replaceUserMessageId should bind and confirm

  it('D11: confirmUserMessage removes the pending entry', () => {
    session.addPendingUserMessage('c1', 'hello')
    session.confirmUserMessage('c1')
    expect(session.getPendingUserMessages().length).toBe(0)
  })

  // D.9: step event with OriginMessageId auto-confirms

  it('D9: step.opened event with OriginMessageId confirms the pending entry', () => {
    session.addPendingUserMessage('c1', 'hello')
    session.bindMessageId('c1', 'msg-real-123')
    session.applyStepEvents([{
      Kind: 'step.opened',
      StepId: 'u1',
      TurnId: 't1',
      StepType: 'text',
      Role: 'user',
      Block: { Type: 'text', Text: 'hello' },
      OriginMessageId: 'msg-real-123',
    }])
    expect(session.getPendingUserMessages().length).toBe(0)
  })

  // OriginMessageId absent: falls back to text matching

  it('confirms by exact text when OriginMessageId is absent', () => {
    session.addPendingUserMessage('c1', 'hello')
    session.applyStepEvents([{
      Kind: 'step.opened',
      StepId: 'u1',
      TurnId: 't1',
      StepType: 'text',
      Role: 'user',
      Block: { Type: 'text', Text: 'hello' },
    }])
    expect(session.getPendingUserMessages().length).toBe(0)
  })

  // Summary / reconcile path: live event stream missed the step.opened
  // (e.g. buffer cap dropped it — AUDIT 2.1). Server step arrives via
  // setSteps(mergeSteps(...)). _confirmPendingUserMessagesFromSteps walks
  // _steps and uses step.Id == ps.ID to confirm via message-id path.

  it('confirms via step.Id when summary path brings server step (no live event)', () => {
    session.addPendingUserMessage('c1', 'hello')
    session.bindMessageId('c1', 'ps-1')

    const serverStep = {
      Id: 'ps-1',
      Role: 'assistant' as const,
      Type: 'user_inject',
      Content: [{ Type: 'text', Text: 'hello' }],
      Closed: true,
      Timestamp: new Date().toISOString(),
      TurnId: 'turn-active',
      ContentStatus: 'stable' as const,
      ExecutionStatus: 'idle' as const,
      InteractionStatus: 'none' as const,
      Seq: 1,
    }
    session.setSteps([serverStep])

    expect(session.getPendingUserMessages().length).toBe(0)
  })

  it('applySummaryToState bottom-line: confirms via existing steps when summary has no server steps', () => {
    // AUDIT 1.5: serverSteps empty used to mean _confirmPendingUserMessagesFromSteps
    // never ran. After fix, setSteps is always called from applySummaryToState.
    session.addPendingUserMessage('c1', 'first')
    session.bindMessageId('c1', 'ps-existing')

    // Pre-existing step that should match via message-id path.
    const existingStep = {
      Id: 'ps-existing',
      Role: 'assistant' as const,
      Type: 'user_inject',
      Content: [{ Type: 'text', Text: 'first' }],
      Closed: true,
      Timestamp: new Date().toISOString(),
      TurnId: 'turn-active',
      ContentStatus: 'stable' as const,
      ExecutionStatus: 'idle' as const,
      InteractionStatus: 'none' as const,
      Seq: 1,
    }
    session.setSteps([existingStep])

    // Simulate applySummaryToState with no serverSteps: steps ref unchanged.
    // Before fix, this branch skipped setSteps and confirm never ran.
    session.setSteps(session.steps, false)

    expect(session.getPendingUserMessages().length).toBe(0)
  })
})

// ── Wave 6 / AUDIT 1.4: confirmByExactText ambiguity ──

describe('PendingMessageQueue — AUDIT 1.4 ambiguity guard', () => {
  it('does NOT confirm when multiple entries share the same text', () => {
    const q = new PendingMessageQueue({ ttlMs: 60_000 })
    q.enqueue('c1', 'hello')
    q.enqueue('c2', 'hello')
    // Both entries match "hello" — text path refuses to guess.
    expect(q.confirmByExactText('hello')).toBe(false)
    expect(q.size).toBe(2)
  })

  it('confirms when exactly one entry matches the text', () => {
    const q = new PendingMessageQueue({ ttlMs: 60_000 })
    q.enqueue('c1', 'hello')
    q.enqueue('c2', 'world')
    expect(q.confirmByExactText('hello')).toBe(true)
    expect(q.size).toBe(1)
  })
})

// ── Wave 6 / AUDIT 1.7: failed entry GC ──

describe('PendingMessageQueue — AUDIT 1.7 failed GC', () => {
  it('markFailed schedules GC so failed entries do not accumulate', () => {
    vi.useFakeTimers()
    const q = new PendingMessageQueue({ ttlMs: 60_000, failedGcMs: 5_000 })
    q.enqueue('c1', 'first')
    q.enqueue('c2', 'second')
    expect(q.size).toBe(2)

    q.markFailed('c1')
    expect(q.size).toBe(2) // still present right after markFailed

    vi.advanceTimersByTime(6_000) // past failedGcMs
    expect(q.size).toBe(1) // c1 reaped, c2 still pending

    q.clear()
    vi.useRealTimers()
  })

  it('clear cancels all GC timers', () => {
    vi.useFakeTimers()
    const q = new PendingMessageQueue({ ttlMs: 60_000, failedGcMs: 5_000 })
    q.enqueue('c1', 'first')
    q.markFailed('c1')
    q.clear()
    // Advancing time should not throw and size stays 0.
    vi.advanceTimersByTime(20_000)
    expect(q.size).toBe(0)
    vi.useRealTimers()
  })
})

// ── Wave 6 / AUDIT 1.8: markAllFailed ──

describe('PendingMessageQueue — AUDIT 1.8 markAllFailed', () => {
  it('marks every pending entry as failed', () => {
    const q = new PendingMessageQueue({ ttlMs: 60_000, failedGcMs: 60_000 })
    q.enqueue('c1', 'a')
    q.enqueue('c2', 'b')
    q.enqueue('c3', 'c')
    q.markAllFailed()
    const snap = q.snapshot()
    expect(snap.every(e => e.state === 'failed')).toBe(true)
    expect(snap.length).toBe(3)
    q.clear()
  })
})
