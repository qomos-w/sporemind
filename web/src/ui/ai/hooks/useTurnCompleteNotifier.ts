import { useEffect, useRef } from 'react'
import { getTimelineManager } from './useTimelineManager'
import { getAgentInfoSnapshot, subscribeAgentInfoStore } from './agentInfoStore'
import type { AgentInfo } from './agentInfoStore'
import type {
  AskUserQuestionFrame,
  Frame,
  GoalCardSubmitFrame,
  GoalSubmitFrame,
  PermissionRequestFrame,
  PlanFrame,
  TurnEnvelope,
} from '../model/frame-types'
import {
  loadNotifyConfig,
  playAllCompleteSound,
  playCompleteSound,
  playErrorSound,
  playInteractionSound,
  unlockAudio,
} from '../notify-sound'
import type { VoiceNotifyConfig } from '../../../gen-clients/system/types'

/**
 * Confirmation delay applied after a turn appears to finish.
 *
 * Goal-driven turns are autonomous cycles: when one assistant turn completes
 * with the budget unused, the backend (`startNextGoalTurn`) **synchronously**
 * starts the next turn and emits `turn.started` right after `turn.completed`.
 * On the frontend, SSE delivery + rAF batching can land those two events on
 * different frames, producing a brief `isStreaming=false` window between them.
 * Playing the notification during that window would fire a chime on every
 * intermediate goal turn.
 *
 * This debounce absorbs that inter-frame gap. The backend emits the next
 * turn.started within milliseconds; waiting `GOAL_DEBOUNCE_MS` and re-checking
 * `isStreaming` reliably distinguishes a mid-goal pause (resumes streaming,
 * notification cancelled) from a real stop (`complete_candidate`, turn budget
 * exhausted, or goal submit) which stays idle.
 */
const GOAL_DEBOUNCE_MS = 800

/**
 * Watches the active agent's timeline for two kinds of attention-worthy
 * transitions and plays the configured notification sound:
 *
 * 1. **Turn truly stops** (streaming → idle, not paused). A failed/cancelled
 *    turn plays the error tone when OnError is enabled; a successful turn
 *    plays the selected completion sound. Mid-goal intermediate turns are
 *    suppressed by the confirmation debounce above.
 *
 * 2. **Interaction requested** (ask_user, plan_submit, goal_submit,
 *    permission). Plays a distinct attention tone (OnInteraction) the moment
 *    a new pending interaction frame appears — the backend is blocked waiting
 *    for the user, so no debounce is needed or wanted.
 *
 * The notification config is loaded once from the voice actor (cached,
 * backend-owned — never localStorage).
 *
 * Additionally, a background watcher derived from agentInfoStore state
 * transitions covers every NON-active agent: interaction requested, turn
 * failed and turn completed all play the corresponding sound even when the
 * agent is not selected (the active agent is excluded there to avoid double
 * chimes with the timeline path).
 */
export function useTurnCompleteNotifier(activeAgentActorId: string | null): void {
  // Mirror of the active agent id readable from store callbacks without
  // resubscribing.
  const activeRef = useRef<string | null>(activeAgentActorId)
  activeRef.current = activeAgentActorId
  // Per-agent record of the last observed streaming/paused state, so we only
  // fire on a true transition and never on initial mount.
  const prevRef = useRef<{ streaming: boolean; paused: boolean }>({
    streaming: false,
    paused: false,
  })
  // Pending confirmation timer armed when a turn appears to finish.
  const pendingPlayRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Set of interaction frame ids we have already played a sound for, so we
  // fire once per interaction request, not on every timeline re-render.
  const playedInteractionIdsRef = useRef<Set<string>>(new Set())
  // Whether the initial snapshot for the current agent selection has been
  // observed with its load settled. Pending frames that already exist at
  // selection are historical — the background watcher chimed when they
  // appeared — so they are seeded into playedInteractionIds silently instead
  // of re-chiming on every click of the agent.
  const interactionsSeededRef = useRef(false)
  // Timestamp of the last all-complete cue, so two agents finishing within
  // the same debounce window don't stack two arpeggios.
  const allCompleteAtRef = useRef(0)

  /**
   * Dispatch the completion sound for a finished agent: when nothing else is
   * active (no working agent, no active workflow) the all-complete cue
   * replaces the per-agent sound.
   */
  const playCompletionCue = (cfg: VoiceNotifyConfig, completingActorId: string) => {
    if (cfg.OnAllComplete && isSystemAllComplete(getAgentInfoSnapshot().items, completingActorId)) {
      const now = Date.now()
      if (now - allCompleteAtRef.current < 2000) return
      allCompleteAtRef.current = now
      playAllCompleteSound(cfg)
      return
    }
    playCompleteSound(cfg)
  }

  useEffect(() => {
    if (!activeAgentActorId) {
      prevRef.current = { streaming: false, paused: false }
      playedInteractionIdsRef.current = new Set()
      interactionsSeededRef.current = false
      if (pendingPlayRef.current) {
        clearTimeout(pendingPlayRef.current)
        pendingPlayRef.current = null
      }
      return
    }
    // Reset transition tracking when switching agents so we don't fire a
    // notification for an agent that was already idle / had pending
    // interactions before we selected it.
    prevRef.current = { streaming: false, paused: false }
    playedInteractionIdsRef.current = new Set()
    interactionsSeededRef.current = false
    if (pendingPlayRef.current) {
      clearTimeout(pendingPlayRef.current)
      pendingPlayRef.current = null
    }

    // Load the user's notification config (cached, backend-owned).
    void loadNotifyConfig()

    const manager = getTimelineManager()

    const fireIfStillIdle = () => {
      // Re-check state at confirmation time: a mid-goal turn may have started
      // a new turn in the meantime (backend startNextGoalTurn), or the user
      // may have paused.
      const cur = manager.getSnapshot(activeAgentActorId)
      if (cur.isStreaming || cur.isPaused) return
      loadNotifyConfig().then(cfg => {
        const failed = lastAssistantTurnFailed(cur.envelopes)
        if (failed) playErrorSound(cfg)
        else playCompletionCue(cfg, activeAgentActorId)
      }).catch(() => {})
    }

    const check = () => {
      const snap = manager.getSnapshot(activeAgentActorId)

      // ── Interaction detection ──
      // Scan every envelope for pending interaction frames. A pending frame is
      // one that is still waiting for the user: ask_user without answers,
      // permission_request without a decision, plan/goal_submit with
      // approvalStatus 'pending'. Play the attention tone the first time we
      // see a given frame id in the pending state.
      const pending = collectPendingInteractionFrames(snap.envelopes)
      const played = playedInteractionIdsRef.current
      if (!interactionsSeededRef.current) {
        // First observation for this selection: seed pre-existing pending
        // frames silently. Stay unseeded while the timeline is still loading
        // so frames that arrive with the initial fetch are seeded too; only
        // frames appearing after the load settles trigger the tone.
        for (const f of pending) played.add(f.id)
        if (!snap.loading) interactionsSeededRef.current = true
      } else {
        let hasNew = false
        for (const f of pending) {
          if (!played.has(f.id)) {
            played.add(f.id)
            hasNew = true
          }
        }
        if (hasNew) {
          loadNotifyConfig().then(cfg => playInteractionSound(cfg)).catch(() => {})
        }
      }

      // ── Turn completion detection ──
      // If streaming resumed (next goal turn started), cancel any pending
      // notification from the previous turn's completion frame.
      if (snap.isStreaming) {
        if (pendingPlayRef.current) {
          clearTimeout(pendingPlayRef.current)
          pendingPlayRef.current = null
        }
        prevRef.current = { streaming: true, paused: snap.isPaused }
        return
      }
      const prev = prevRef.current
      const justFinished =
        prev.streaming && !snap.isStreaming && !snap.isPaused
      prevRef.current = { streaming: snap.isStreaming, paused: snap.isPaused }
      if (!justFinished) return
      // A turn just finished. Wait out the goal-debounce window before
      // playing, so a mid-goal cycle that immediately starts the next turn
      // gets a chance to flip isStreaming back to true and cancel this.
      if (pendingPlayRef.current) clearTimeout(pendingPlayRef.current)
      pendingPlayRef.current = setTimeout(() => {
        pendingPlayRef.current = null
        fireIfStillIdle()
      }, GOAL_DEBOUNCE_MS)
    }
    // Seed prevRef with the current state on subscribe.
    check()
    const unsub = manager.subscribe(check, activeAgentActorId)
    return () => {
      unsub()
      if (pendingPlayRef.current) {
        clearTimeout(pendingPlayRef.current)
        pendingPlayRef.current = null
      }
    }
  }, [activeAgentActorId])

  // Background watcher for every agent EXCEPT the active one (the active
  // agent's richer timeline-based detection above already covers it).
  // Derived from agentInfoStore state transitions so interactions, failures
  // and completions from non-selected agents still play a sound.
  useEffect(() => {
    void loadNotifyConfig()
    // null until the first snapshot seeds it, so app start never chimes for
    // agents that were already waiting before the UI loaded.
    let prev: Map<string, BackgroundNotifyState> | null = null
    const timers = new Map<string, ReturnType<typeof setTimeout>>()

    const unsub = subscribeAgentInfoStore(() => {
      const next = new Map<string, BackgroundNotifyState>()
      for (const a of getAgentInfoSnapshot().items) {
        next.set(a.ActorId, backgroundNotifyState(a))
      }
      const prevMap = prev
      prev = next
      if (!prevMap) return
      for (const t of detectBackgroundTransitions(prevMap, next, activeRef.current)) {
        if (t.kind === 'error') {
          loadNotifyConfig().then(cfg => playErrorSound(cfg)).catch(() => {})
        } else if (t.kind === 'interaction') {
          loadNotifyConfig().then(cfg => playInteractionSound(cfg)).catch(() => {})
        } else {
          // Same mid-goal debounce as the timeline path: the backend may
          // synchronously start the next goal turn, flipping working back on.
          const existing = timers.get(t.actorId)
          if (existing) clearTimeout(existing)
          const timer = setTimeout(() => {
            timers.delete(t.actorId)
            const still = getAgentInfoSnapshot().byActorId.get(t.actorId)
            if (!still || still.IsWorking || still.IsError || still.Status === 'paused') return
            loadNotifyConfig().then(cfg => playCompletionCue(cfg, t.actorId)).catch(() => {})
          }, GOAL_DEBOUNCE_MS)
          timers.set(t.actorId, timer)
        }
      }
    })
    return () => {
      unsub()
      for (const t of timers.values()) clearTimeout(t)
      timers.clear()
    }
  }, [])

  // Unlock the AudioContext on the first user interaction anywhere in the
  // shell (browsers require a gesture before audio can play).
  useEffect(() => {
    const onGesture = () => {
      unlockAudio()
      window.removeEventListener('pointerdown', onGesture)
      window.removeEventListener('keydown', onGesture)
    }
    window.addEventListener('pointerdown', onGesture)
    window.addEventListener('keydown', onGesture)
    return () => {
      window.removeEventListener('pointerdown', onGesture)
      window.removeEventListener('keydown', onGesture)
    }
  }, [])
}

/** @internal exported for testing */
export interface BackgroundNotifyState {
  waiting: boolean
  error: boolean
  working: boolean
  paused: boolean
}

/** @internal exported for testing */
export function backgroundNotifyState(a: AgentInfo): BackgroundNotifyState {
  return {
    waiting: a.IsAskUser || a.IsAskPermission || a.IsPlanApproval || a.IsGoalSubmit,
    error: a.IsError,
    working: a.IsWorking,
    paused: a.Status === 'paused',
  }
}

/**
 * Pure global check: no agent is working and no agent holds an active
 * workflow map. `completingActorId` is treated as already finished for its
 * working state (the timeline path can fire before the agent list catches
 * up), but an active workflow it owns still blocks.
 *
 * @internal exported for testing
 */
export function isSystemAllComplete(
  items: AgentInfo[],
  completingActorId?: string | null,
): boolean {
  for (const a of items) {
    if (a.ActorId === completingActorId) {
      if (a.ActiveWorkflowMapCardId) return false
      continue
    }
    if (a.IsWorking || a.ActiveWorkflowMapCardId) return false
  }
  return true
}

export interface BackgroundTransition {
  actorId: string
  kind: 'interaction' | 'error' | 'complete'
}

/**
 * Pure transition detector for the background (non-active agent) watcher.
 * `complete` is a candidate: the caller debounces it because a goal-driven
 * agent may synchronously start its next turn (working flips back on).
 *
 * @internal exported for testing
 */
export function detectBackgroundTransitions(
  prev: Map<string, BackgroundNotifyState>,
  next: Map<string, BackgroundNotifyState>,
  excludeActorId: string | null,
): BackgroundTransition[] {
  const out: BackgroundTransition[] = []
  for (const [actorId, cur] of next) {
    if (actorId === excludeActorId) continue
    const p = prev.get(actorId)
    if (!p) continue
    if (cur.error && !p.error) {
      out.push({ actorId, kind: 'error' })
    } else if (cur.waiting && !p.waiting) {
      out.push({ actorId, kind: 'interaction' })
    } else if (p.working && !cur.working && !cur.waiting && !cur.error && !cur.paused) {
      out.push({ actorId, kind: 'complete' })
    }
  }
  return out
}

/** @internal exported for testing */
export function lastAssistantTurnFailed(envelopes: TurnEnvelope[]): boolean {
  for (let i = envelopes.length - 1; i >= 0; i--) {
    const env = envelopes[i]
    if (!env || env.role !== 'assistant') continue
    const ts = env.metadata?.turnState
    return ts === 'failed' || ts === 'cancelled'
  }
  return false
}

/**
 * Collect every currently-pending interaction frame across all envelopes.
 *
 * A frame is "pending" when the backend is blocked waiting for the user:
 *  - ask_user: no answers recorded yet
 *  - permission_request: no allowed decision yet
 *  - plan / goal_submit: approvalStatus === 'pending'
 *
 * Resolved / cancelled / approved / rejected frames are excluded — they no
 * longer need the user's attention.
 *
 * @internal exported for testing
 */
export function collectPendingInteractionFrames(envelopes: TurnEnvelope[]): Frame[] {
  const out: Frame[] = []
  for (const env of envelopes) {
    if (!env || env.role !== 'assistant') continue
    for (const f of env.frames ?? []) {
      if (isPendingInteractionFrame(f)) out.push(f)
    }
  }
  return out
}

function isPendingInteractionFrame(f: Frame): boolean {
  // Only live frames still block on the user. Resolved or cancelled
  // interactions render status 'completed' (answered via local response or
  // parsed history, or step Closed without resolution) — counting those as
  // pending re-chimes on every agent selection after a timeline reload.
  // Mirrors isLiveStatus in model/pending-interaction.ts.
  const live = f.status === 'running' || f.status === 'pending'
  switch (f.type) {
    case 'ask_user': {
      const af = f as AskUserQuestionFrame
      return live && af.answers == null
    }
    case 'permission_request': {
      const pf = f as PermissionRequestFrame
      return live && pf.allowed == null
    }
    case 'plan': {
      const plf = f as PlanFrame
      return plf.approvalStatus === 'pending'
    }
    case 'goal_submit': {
      const gf = f as GoalSubmitFrame
      return gf.approvalStatus === 'pending'
    }
    case 'goal_card_submit': {
      const gf = f as GoalCardSubmitFrame
      return gf.approvalStatus === 'pending'
    }
    default:
      return false
  }
}
