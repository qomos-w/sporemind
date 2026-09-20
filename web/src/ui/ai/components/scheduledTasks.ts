// Pure logic for the "已安排" (scheduled-tasks) view: cron → human-readable
// classification, status filtering, and search. Locale-free — all wording is
// produced by the component from i18n keys; these helpers only classify and
// extract structural values.

import type { WikiTimerListItem } from '../../../gen-types/project.wiki.part1'
import type { AgentListItem } from '../../../gen-clients/system/types'

export type CronKind = 'everyDay' | 'weekdays' | 'weekly' | 'monthly' | 'custom'

/** Resolve a card agent ref ("agent:<id>" or a raw id) to the bare id. */
export function agentRefId(ref: string | null | undefined): string {
  if (!ref) return ''
  return ref.startsWith('agent:') ? ref.slice(6) : ref
}

/** Find the agent list item matching a card agent ref by Id or ActorId. */
export function findAgentByRef(agentItems: AgentListItem[], ref: string | null | undefined): AgentListItem | undefined {
  const id = agentRefId(ref)
  if (!id) return undefined
  return agentItems.find(a => a.Id === id || a.ActorId === id)
}

export interface CronSummary {
  /** Structural classification of the cron expression. */
  kind: CronKind
  /** "H:mm" / "HH:mm" display time, or '' when not a single time-of-day. */
  time: string
  /** Resolved day-of-week index (1..7, Sunday=7) for the weekly case. */
  dow?: number
  /** Day-of-month number for the monthly case. */
  dom?: number
}

/** Parse "0 8 * * 1-5" style 5-field cron into a structural summary. */
export function describeCron(cron: string): CronSummary {
  const parts = cron.trim().split(/\s+/)
  if (parts.length !== 5) return { kind: 'custom', time: '' }
  const [m = '', h = '', dom = '', mon = '', dow = ''] = parts
  const time = formatTime(h, m)
  // All structured kinds (daily/weekdays/weekly/monthly) describe a single
  // time-of-day; a step/list/range minute or hour has no clean time.
  if (!time) return { kind: 'custom', time: '' }
  const everyDom = dom === '*'
  const everyMon = mon === '*'

  // Weekdays: Mon–Fri. `1-5` (cron) or `1,2,3,4,5`.
  if (isWeekdays(dow) && everyDom && everyMon) return { kind: 'weekdays', time }

  // Every day: all wildcards except the time.
  if (everyDom && everyMon && dow === '*') return { kind: 'everyDay', time }

  // Single day-of-week → weekly.
  const dowNum = singleDow(dow)
  if (dowNum !== null && everyDom && everyMon) return { kind: 'weekly', time, dow: dowNum }

  // Specific day-of-month each month.
  if (!everyDom && everyMon && dow === '*') {
    const domNum = parseInt(dom, 10)
    if (Number.isFinite(domNum) && domNum >= 1 && domNum <= 31) return { kind: 'monthly', time, dom: domNum }
  }

  return { kind: 'custom', time }
}

/** Format the hour:minute fields of a cron expression. */
function formatTime(h: string, m: string): string {
  const hh = parseInt(h, 10)
  const mm = parseInt(m, 10)
  if (!Number.isFinite(hh) || !Number.isFinite(mm) || hh < 0 || hh > 23 || mm < 0 || mm > 59) return ''
  // cron allows lists/ranges; only a single exact minute/hour is a clean time.
  if (h.includes(',') || h.includes('-') || h.includes('/') || h.includes('*')) return ''
  if (m.includes(',') || m.includes('-') || m.includes('/') || m.includes('*')) return ''
  const hour = String(hh)
  const minute = String(mm).padStart(2, '0')
  return `${hour}:${minute}`
}

function isWeekdays(dow: string): boolean {
  return dow === '1-5' || dow === '1,2,3,4,5' || dow === '2,3,4,5,1'
}

function singleDow(dow: string): number | null {
  if (dow === '0' || dow === '7') return 7
  const n = parseInt(dow, 10)
  if (dow === String(n) && n >= 1 && n <= 6) return n
  return null
}

export type TimerStatusFilter = 'all' | 'enabled' | 'paused'

export function filterTimersByStatus(
  timers: WikiTimerListItem[],
  filter: TimerStatusFilter,
): WikiTimerListItem[] {
  if (filter === 'all') return timers
  return timers.filter(t => (filter === 'enabled' ? t.Enabled : !t.Enabled))
}

export function searchTimers(
  items: { timer: WikiTimerListItem; title: string }[],
  query: string,
): { timer: WikiTimerListItem; title: string }[] {
  const q = query.trim().toLowerCase()
  if (!q) return items
  return items.filter(({ title, timer }) => title.toLowerCase().includes(q) || timer.Id.toLowerCase().includes(q))
}

/* ===================== scope (global / per-project browsing) ===================== */

export type ScheduledScopeKind = 'active' | 'project' | 'global'

export interface ResolvedScheduledScope {
  kind: ScheduledScopeKind
  /** Pinned foreign project id when kind === 'project'. */
  projectId?: string
}

/** Resolve the persisted scheduled-view scope value against the active project:
 *  '' (or a value equal to the active project) follows the active project,
 *  'global' spans every user project, anything else pins that project. */
export function resolveScheduledScope(
  scopeValue: string | undefined,
  activeProjectId: string | undefined,
): ResolvedScheduledScope {
  if (scopeValue === 'global') return { kind: 'global' }
  if (scopeValue && scopeValue !== activeProjectId) return { kind: 'project', projectId: scopeValue }
  return { kind: 'active' }
}

/** Task mode — the runtime flavor of a unified task: inline prompt body or
 *  a bound workflow template. Derived from template binding (see resolveTaskMode). */
export type TaskMode = 'prompt' | 'template'

/**
 * Unified schedule type. Scheduler cards converge to a single "task" type;
 * legacy workflow/prompt values map here. The workflow/prompt distinction lives
 * in task mode; pause/resume agent-state actions are tracked separately via
 * AgentStateAction.
 */
export type ScheduleType = 'task'

/**
 * Agent-state actions scheduled by a timer: pause or resume the target agent.
 * Kept separate from ScheduleType because agent-state cards are not tasks.
 */
export type AgentStateAction = 'pause' | 'resume'

/**
 * Derive the schedule type from explicit data.schedule_type. Every scheduler
 * card is a unified task; legacy workflow/prompt/agent_action values are
 * normalized away.
 */
export function resolveScheduleType(_explicit?: string): ScheduleType {
  return 'task'
}

/**
 * Resolve an explicit agent action. Legacy cards encoded the action via
 * schedule_type === 'agent_action'; in the unified model it is stored in
 * data.agent_action. Returns undefined for ordinary task cards.
 */
export function resolveAgentStateAction(
  explicit: string | undefined,
  scheduleType?: string | undefined,
): AgentStateAction | undefined {
  if (explicit === 'pause' || explicit === 'resume') return explicit
  if (scheduleType === 'agent_action') return 'pause'
  return undefined
}

/** One agent-state action entry on a unified scheduler card. */
export interface AgentActionItem {
  action: AgentStateAction
  targetAgent: string
}

function isAgentStateAction(value: unknown): value is AgentStateAction {
  return value === 'pause' || value === 'resume'
}

function normalizeAgentActionItem(value: unknown): AgentActionItem | undefined {
  if (value === null || typeof value !== 'object') return undefined
  const v = value as Record<string, unknown>
  const action = v.action ?? v.agent_action
  const target = v.targetAgent ?? v.target ?? v.target_agent
  if (!isAgentStateAction(action) || typeof target !== 'string' || target === '') return undefined
  return { action, targetAgent: target }
}

/**
 * Parse the agent-state actions stored on a scheduler card data block.
 *
 * The unified model stores them as a JSON string under `data.agent_actions`
 * (an array of `{ action, target }` objects — the key the backend timer
 * dispatch parses). For backwards compatibility this also recognises legacy
 * `target_agent`/`targetAgent` keys and a single-action card encoded via
 * `data.agent_action` + `data.target_agent` (or `schedule_type: agent_action`).
 */
export function agentActionsOf(cardData: Record<string, unknown> | undefined): AgentActionItem[] {
  if (!cardData) return []
  const raw = cardData.agent_actions
  let parsed: unknown
  if (typeof raw === 'string' && raw.trim() !== '') {
    try {
      parsed = JSON.parse(raw)
    } catch {
      parsed = undefined
    }
  } else if (Array.isArray(raw)) {
    parsed = raw
  }
  if (parsed !== undefined) {
    const list = Array.isArray(parsed) ? parsed : [parsed]
    const items = list.map(normalizeAgentActionItem).filter((x): x is AgentActionItem => x !== undefined)
    if (items.length > 0) return items
  }
  const singleAction = resolveAgentStateAction(
    typeof cardData.agent_action === 'string' ? cardData.agent_action : undefined,
    typeof cardData.schedule_type === 'string' ? cardData.schedule_type : undefined,
  )
  const singleTarget = typeof cardData.target_agent === 'string' ? cardData.target_agent : ''
  if (singleAction && singleTarget) return [{ action: singleAction, targetAgent: singleTarget }]
  return []
}

/** Derive the task mode from an explicit TaskMode value (preferred, surfaced
 *  on WikiTimerListItem) or fall back to template binding: a bound template id
 *  means template mode, otherwise prompt mode. */
export function resolveTaskMode(
  explicit: string | undefined,
  templateId: string | undefined,
): TaskMode {
  if (explicit === 'template') return 'template'
  return templateId ? 'template' : 'prompt'
}

/** First non-empty line of a card body, trimmed ('' when the body is blank). */
export function firstBodyLine(body: string): string {
  for (const line of body.split('\n')) {
    const t = line.trim()
    if (t) return t
  }
  return ''
}

/** Collapse internal whitespace and truncate to max chars for preview badges. */
export function truncatePreview(value: string, max: number): string {
  const collapsed = value.replace(/\s+/g, ' ').trim()
  if (collapsed.length <= max) return collapsed
  return `${collapsed.slice(0, max)}…`
}

/** "32 分前" style relative time (zh-leaning); returns '' for empty/invalid. */
export function relativeTime(iso: string, now: Date = new Date()): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  if (!Number.isFinite(t)) return ''
  const diffMs = now.getTime() - t
  if (diffMs < 0) return ''
  const sec = Math.floor(diffMs / 1000)
  if (sec < 60) return ''
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr} 小时`
  const day = Math.floor(hr / 24)
  return `${day} 天`
}

/** Editable schedule form state — the inverse of CronSummary. */
export interface ScheduleDraft {
  kind: CronKind
  /** "HH:mm" for the time input. */
  time: string
  /** Day-of-week for weekly: 0=Sunday … 6=Saturday (cron numbering). */
  dow: number
  /** Day-of-month for monthly: 1..31. */
  dom: number
  /** Raw cron text for the custom kind. */
  custom: string
}

/** Normalize "9:05" → "09:05" for <input type="time"> values. */
function padTime(time: string): string {
  const m = /^(\d{1,2}):(\d{2})$/.exec(time.trim())
  if (!m) return ''
  const h = Number(m[1])
  const min = Number(m[2])
  if (h > 23 || min > 59) return ''
  return `${String(h).padStart(2, '0')}:${String(min).padStart(2, '0')}`
}

/** Initialize an editable draft from the current card's cron state. An unscheduled
 *  card (no cron/expression) defaults to every day at 09:00 — matching the
 *  default cron createSchedulerCard writes — so the editor never opens on the
 *  opaque "custom" kind for a fresh task. */
export function cronToDraft(cron: string, expression: string): ScheduleDraft {
  if (!cron && !expression) {
    return { kind: 'everyDay', time: '09:00', dow: 1, dom: 1, custom: '' }
  }
  const summary = describeCron(expression || cron)
  return {
    kind: summary.kind,
    time: padTime(summary.time) || '09:00',
    // CronSummary uses 1..7 with Sunday=7; the draft uses cron numbering 0..6.
    dow: summary.dow === 7 ? 0 : (summary.dow ?? 1),
    dom: summary.dom ?? 1,
    // The effective schedule is `expression || cron` (expression wins when
    // both are present — the same precedence describeCron and the row display
    // use). Seeding the custom field from either field alone would desync the
    // editor's structural kind from its raw text and silently overwrite one
    // with the other on save.
    custom: expression || cron,
  }
}

/** Build the 5-field cron string from an editable draft. Returns '' when invalid. */
export function draftToCron(draft: ScheduleDraft): string {
  if (draft.kind === 'custom') {
    const parts = draft.custom.trim().split(/\s+/)
    return parts.length === 5 ? draft.custom.trim() : ''
  }
  const time = padTime(draft.time)
  if (!time) return ''
  // Strip the input's zero-padding ("09:05" → m=5 h=9) so stored crons stay
  // canonical/unpadded, matching what describeCron round-trips.
  const h = String(Number(time.split(':')[0]))
  const m = String(Number(time.split(':')[1]))
  switch (draft.kind) {
    case 'everyDay':
      return `${m} ${h} * * *`
    case 'weekdays':
      return `${m} ${h} * * 1-5`
    case 'weekly': {
      const dow = Math.trunc(draft.dow)
      if (dow < 0 || dow > 6) return ''
      return `${m} ${h} * * ${dow}`
    }
    case 'monthly': {
      const dom = Math.trunc(draft.dom)
      if (dom < 1 || dom > 31) return ''
      return `${m} ${h} ${dom} * *`
    }
  }
  return ''
}

/** True when the draft's rebuilt cron differs from the stored schedule. The
 *  baseline is derived from `expression || cron` — the effective schedule when
 *  a card carries both fields — so seeding the draft from the expression and
 *  comparing against the raw cron can never show a phantom "modified" state.
 *  Both sides pass through cronToDraft + draftToCron, so formatting-only
 *  differences (extra spaces, zero-padded hours) don't count as modifications —
 *  the save button only appears for a real schedule change. An empty original
 *  (fresh card) compares against the every-day-09:00 default the draft was
 *  seeded with, so an untouched editor is never dirty. */
export function scheduleDraftIsDirty(draft: ScheduleDraft, originalCron: string, originalExpression = ''): boolean {
  const baseline = draftToCron(cronToDraft(originalCron, originalExpression))
  return baseline !== '' && draftToCron(draft) !== baseline
}
