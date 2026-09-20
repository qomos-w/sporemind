import type { MonoCardListItem } from './mono-types'

/** Canonical task statuses shown as fixed swimlane columns. */
export const CANONICAL_TASK_STATUSES = ['backlog', 'todo', 'doing', 'pending_review', 'done', 'blocked', 'cancelled', 'failed'] as const

export type CanonicalTaskStatus = (typeof CANONICAL_TASK_STATUSES)[number]

/** Default aliases: observed custom status values -> canonical status.
 *  Keep in sync with pkg/actor/project/card_mounting.go defaultTaskStatusAliases. */
export const DEFAULT_STATUS_ALIASES: Record<string, CanonicalTaskStatus> = {
  // todo
  open: 'todo',
  draft: 'todo',
  approved: 'todo',
  'to-do': 'todo',
  to_do: 'todo',
  pending: 'todo',
  wait: 'todo',
  waiting: 'todo',
  // doing
  in_progress: 'doing',
  'in progress': 'doing',
  inprogress: 'doing',
  'in-progress': 'doing',
  in_gress: 'doing',
  'in gress': 'doing',
  wip: 'doing',
  progress: 'doing',
  active: 'doing',
  ongoing: 'doing',
  experimental: 'doing',
  // done
  completed: 'done',
  complete: 'done',
  finished: 'done',
  finish: 'done',
  closed: 'done',
  resolved: 'done',
  // backlog
  'back log': 'backlog',
  back_log: 'backlog',
  // cancelled
  rejected: 'cancelled',
  canceled: 'cancelled',
  aborted: 'cancelled',
  withdrawn: 'cancelled',
}

export const STATUS_MAP_CARD_ID = '__builtin_task_status_map__'
export const STATUS_MAP_CARD_TAG = '__builtin_task_status_map__'
export const STATUS_MAP_CARD_TITLE = 'Task status map'

export function findStatusMapCard(cards: readonly MonoCardListItem[]): MonoCardListItem | undefined {
  return cards.find(c => c.id === STATUS_MAP_CARD_TITLE || c.tags.includes(STATUS_MAP_CARD_TAG))
}

export function parseStatusAliases(data: Record<string, unknown> | undefined): Record<string, string> {
  const aliases: Record<string, string> = {}
  if (!data) return aliases
  const raw = data.aliases
  if (Array.isArray(raw)) {
    for (const entry of raw) {
      if (typeof entry !== 'string') continue
      const sep = entry.indexOf(':')
      if (sep === -1) continue
      const from = entry.slice(0, sep).trim()
      const to = entry.slice(sep + 1).trim()
      if (from && to) aliases[from] = to
    }
  } else if (typeof raw === 'string') {
    for (const line of raw.split(/\r?\n/)) {
      const sep = line.indexOf(':')
      if (sep === -1) continue
      const from = line.slice(0, sep).trim()
      const to = line.slice(sep + 1).trim()
      if (from && to) aliases[from] = to
    }
  }
  return aliases
}

export function buildStatusMapCardData(aliases: Record<string, string>): Record<string, unknown> {
  const entries = Object.entries(aliases)
    .filter(([from, to]) => from && to)
    .map(([from, to]) => `${from}:${to}`)
  return { aliases: entries }
}

export function resolveStatusAliases(cards: readonly MonoCardListItem[]): Record<string, string> {
  const map = { ...DEFAULT_STATUS_ALIASES }
  const mapCard = findStatusMapCard(cards)
  if (mapCard?.data) {
    Object.assign(map, parseStatusAliases(mapCard.data))
  }
  return map
}

export function normalizeTaskStatus(
  status: string,
  aliases: Record<string, string> = DEFAULT_STATUS_ALIASES,
): CanonicalTaskStatus {
  const s = status.trim().toLowerCase()
  if (CANONICAL_TASK_STATUSES.includes(s as CanonicalTaskStatus)) return s as CanonicalTaskStatus
  const mapped = aliases[s]
  if (mapped && CANONICAL_TASK_STATUSES.includes(mapped as CanonicalTaskStatus)) return mapped as CanonicalTaskStatus
  return 'backlog'
}

/** Repair a status value: canonical values pass through, known aliases are
 *  mapped to their canonical target, and unknown values are left untouched so
 *  validation can still report them. This is the conservative auto-corrector
 *  used when reading or saving a card. */
export function repairTaskStatus(
  status: string,
  aliases: Record<string, string> = DEFAULT_STATUS_ALIASES,
): string {
  const s = status.trim().toLowerCase()
  if (CANONICAL_TASK_STATUSES.includes(s as CanonicalTaskStatus)) return s
  const mapped = aliases[s]
  if (mapped && CANONICAL_TASK_STATUSES.includes(mapped as CanonicalTaskStatus)) return mapped
  return status
}
