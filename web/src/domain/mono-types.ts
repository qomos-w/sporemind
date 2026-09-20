// MonoCards domain types — TiddlyWiki-style Markdown cards persisted as .md files.

import { CANONICAL_TASK_STATUSES, type CanonicalTaskStatus, DEFAULT_STATUS_ALIASES, repairTaskStatus } from './task-status'

export type MonoCardType =
  | 'agent'
  | 'prompt'
  | 'skill'
  | 'concept'
  | 'callable'
  | 'capability_module'
  | 'scheduler'
  | 'task'
  | 'wiki'
  | 'workflow'
  | 'bundle'

export type ReminderPriority = 'low' | 'medium' | 'high' | 'urgent'

export interface MonoCardMeta {
  id: string
  tags: string[]
  list: string[]
  created: string
  modified: string
  due?: string
  priority?: ReminderPriority
  /** Free-form status string (e.g. backlog, todo, doing, done, blocked, cancelled); kanban columns derive from it. */
  status?: string
  parent?: string
  standalone?: boolean
  /** Custom key-value pairs; values are type-aware (string/number/boolean/null/array). */
  data?: Record<string, unknown>
}

export interface MonoCard extends MonoCardMeta {
  type?: string
  source?: string
  storage?: string
  visibility?: string
  protected?: boolean
  editable?: boolean
  deletable?: boolean
  /** Raw Markdown body (after frontmatter). */
  body: string
  /** Original raw file content including frontmatter. */
  raw: string
}

export interface MonoCardListItem {
  id: string
  type?: string
  source?: string
  storage?: string
  visibility?: string
  protected?: boolean
  editable?: boolean
  deletable?: boolean
  tags: string[]
  list: string[]
  modified: string
  created?: string
  due?: string
  priority?: ReminderPriority
  status?: string
  parent?: string
  standalone?: boolean
  raw?: string
  data?: Record<string, unknown>
}

function normalizeLineEndings(value: string): string {
  return value.replace(/\r\n?/g, '\n')
}

/** Parse the YAML frontmatter block and body from raw markdown text. */
export interface ParsedMarkdown {
  meta: Record<string, unknown>
  body: string
}

export function splitFrontmatter(raw: string): ParsedMarkdown {
  raw = normalizeLineEndings(raw)
  const fmMatch = raw.match(/^---\n([\s\S]*?)\n---\n?/)
  if (!fmMatch) {
    return { meta: {}, body: raw }
  }
  const meta = parseYamlBlock(fmMatch[1]!)
  const body = raw.slice(fmMatch[0].length)
  return { meta, body }
}

/** Minimal YAML parser for flat key: value pairs (no nesting beyond arrays). */
function parseYamlBlock(text: string): Record<string, unknown> {
  const result: Record<string, unknown> = {}
  const lines = text.split(/\r?\n/)
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    if (line === undefined) continue
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) continue
    const colonIdx = trimmed.indexOf(':')
    if (colonIdx === -1) continue
    const key = trimmed.slice(0, colonIdx).trim()
    let rawValue = trimmed.slice(colonIdx + 1).trim()
    // Support multi-line YAML arrays: key:\n  - item
    if (rawValue === '') {
      const collected = collectListItems(lines, i + 1)
      if (collected.items) {
        result[key] = collected.items
        i = collected.next - 1
        continue
      }
      // Support nested YAML maps: key:\n  sub: value
      const collectedMap = collectMapItems(lines, i + 1)
      if (collectedMap.map) {
        result[key] = collectedMap.map
        i = collectedMap.next - 1
        continue
      }
    }
    result[key] = parseYamlValue(rawValue)
  }
  return result
}

/** Read consecutive "- item" lines starting at start; return items and next index. */
function collectListItems(lines: string[], start: number): { items: string[] | null; next: number } {
  const items: string[] = []
  let i = start
  for (; i < lines.length; i++) {
    const line = lines[i]
    if (line === undefined) break
    const trimmed = line.trim()
    if (trimmed === '') continue
    if (!trimmed.startsWith('- ') && trimmed !== '-') break
    const item = unquote(trimmed.replace(/^-\s*/, '').trim())
    if (item !== '') items.push(item)
  }
  return { items: items.length > 0 ? items : null, next: i }
}

function indentWidth(line: string): number {
  return line.length - line.trimStart().length
}

/**
 * Read consecutive indented "key: value" lines starting at start; supports
 * arbitrary nesting by recursing when a key has no inline value and is
 * followed by a deeper-indented block. Returns map and next index.
 */
function collectMapItems(lines: string[], start: number): { map: Record<string, unknown> | null; next: number } {
  const map: Record<string, unknown> = {}
  let baseIndent = -1
  for (let p = start; p < lines.length; p++) {
    const line = lines[p]
    if (line === undefined) break
    if (line.trim() === '') continue
    baseIndent = indentWidth(line)
    break
  }
  // No indented block found (baseIndent 0 means a top-level key, not a child).
  if (baseIndent <= 0) return { map: null, next: start }

  let i = start
  for (; i < lines.length; i++) {
    const line = lines[i]
    if (line === undefined) break
    if (line.trim() === '') continue
    const indent = indentWidth(line)
    // A line at a shallower indent ends this block.
    if (indent < baseIndent) break
    // A deeper-indented line is owned by a recursive call; skip defensively.
    if (indent > baseIndent) break
    const trimmed = line.trim()
    const colonIdx = trimmed.indexOf(':')
    if (colonIdx === -1) break
    const key = trimmed.slice(0, colonIdx).trim()
    const rawValue = trimmed.slice(colonIdx + 1).trim()
    if (rawValue !== '') {
      map[key] = parseScalarValue(rawValue)
      continue
    }
    // Empty inline value: try a nested list, then a nested map.
    const listCollected = collectListItems(lines, i + 1)
    if (listCollected.items) {
      map[key] = listCollected.items
      i = listCollected.next - 1
      continue
    }
    const mapCollected = collectMapItems(lines, i + 1)
    if (mapCollected.map) {
      map[key] = mapCollected.map
      i = mapCollected.next - 1
      continue
    }
    map[key] = ''
  }
  return { map: Object.keys(map).length > 0 ? map : null, next: i }
}

/**
 * Extensible registry of custom-data field types.
 * Each entry knows how to detect, coerce, and render a value.
 * To add a future type (date, enum, color…): append an entry here and,
 * if it needs a new widget, extend `inputKind` in the UI layer.
 */
export interface DataFieldTypeDef {
  /** Stable id stored only transiently (the persisted YAML literal carries the type). */
  id: string
  /** Human-readable label shown in the type selector. */
  label: string
  /** Whether a parsed JS value is naturally this type. */
  detect: (value: unknown) => boolean
  /** Coerce an arbitrary value into this type. */
  coerce: (value: unknown) => unknown
  /** Default value when a field of this type is created. */
  defaultValue: unknown
  /** UI hint telling the editor which widget to use. */
  inputKind: 'text' | 'number' | 'boolean' | 'card'
}

export const DATA_FIELD_TEXT: DataFieldTypeDef = {
  id: 'text',
  label: 'Text',
  detect: v => typeof v === 'string',
  coerce: v => String(v),
  defaultValue: '',
  inputKind: 'text',
}

export const CARD_REF_PREFIX = 'mono-card:' as const

export function isCardReferenceValue(value: unknown): value is string {
  return typeof value === 'string' && value.startsWith(CARD_REF_PREFIX)
}

export function cardReferenceId(value: unknown): string | null {
  return isCardReferenceValue(value) ? value.slice(CARD_REF_PREFIX.length) : null
}

export function makeCardReference(id: string): string {
  return `${CARD_REF_PREFIX}${id}`
}

export const DATA_FIELD_CARD: DataFieldTypeDef = {
  id: 'mono-card',
  label: 'Mono Card',
  detect: isCardReferenceValue,
  coerce: v => makeCardReference(isCardReferenceValue(v) ? cardReferenceId(v)! : String(v)),
  defaultValue: makeCardReference(''),
  inputKind: 'card',
}

export const DATA_FIELD_TYPES: DataFieldTypeDef[] = [
  DATA_FIELD_CARD,
  DATA_FIELD_TEXT,
  {
    id: 'number',
    label: 'Number',
    detect: v => typeof v === 'number' && !isNaN(v),
    coerce: v => {
      const n = typeof v === 'number' ? v : Number(String(v))
      return isNaN(n) ? 0 : n
    },
    defaultValue: 0,
    inputKind: 'number',
  },
  {
    id: 'boolean',
    label: 'Boolean',
    detect: v => typeof v === 'boolean',
    coerce: v => String(v) === 'true' || String(v) === '1',
    defaultValue: false,
    inputKind: 'boolean',
  },
]

/** Resolve the type definition for a value; falls back to 'text'. */
export function dataFieldTypeDefOf(value: unknown): DataFieldTypeDef {
  return DATA_FIELD_TYPES.find(def => def.detect(value)) ?? DATA_FIELD_TEXT
}

/** Resolve a type definition by id; falls back to 'text'. */
export function dataFieldTypeDefById(id: string): DataFieldTypeDef {
  return DATA_FIELD_TYPES.find(def => def.id === id) ?? DATA_FIELD_TEXT
}

function parseYamlValue(value: string): unknown {
  if (value === '') return ''
  // Inline array: [a, b, c]
  if (value.startsWith('[') && value.endsWith(']')) {
    const inner = value.slice(1, -1).trim()
    if (!inner) return []
    return inner.split(',').map(s => unquote(s.trim()))
  }
  // Quoted string or scalar
  const unquoted = unquote(value)
  if (unquoted === 'true') return true
  if (unquoted === 'false') return false
  if (unquoted === 'null') return null
  return unquoted
}

/** Type-aware scalar parsing for data-block values: boolean/null/number/array/string. */
function parseScalarValue(value: string): unknown {
  if (value === '') return ''
  if (value.startsWith('[') && value.endsWith(']')) {
    const inner = value.slice(1, -1).trim()
    if (!inner) return []
    return inner.split(',').map(s => parseScalarValue(s.trim()))
  }
  if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
    return unquote(value)
  }
  if (value === 'true') return true
  if (value === 'false') return false
  if (value === 'null') return null
  if (/^-?\d+$/.test(value)) return parseInt(value, 10)
  if (/^-?\d+\.\d+$/.test(value)) return parseFloat(value)
  return value
}

function unquote(s: string): string {
  if ((s.startsWith('"') && s.endsWith('"')) || (s.startsWith("'") && s.endsWith("'"))) {
    return s.slice(1, -1)
  }
  return s
}

/** Recursively serialise a nested object into indented "key: value" YAML lines. */
function formatObjectLines(obj: Record<string, unknown>, indent: number): string[] {
  const pad = '  '.repeat(indent)
  const lines: string[] = []
  for (const [key, value] of Object.entries(obj)) {
    if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
      lines.push(`${pad}${key}:`)
      lines.push(...formatObjectLines(value as Record<string, unknown>, indent + 1))
    } else {
      lines.push(`${pad}${key}: ${formatDataValue(value)}`)
    }
  }
  return lines
}

/** Serialise a meta map back into a YAML frontmatter block. */
export function formatFrontmatter(meta: Record<string, unknown>): string {
  const lines: string[] = ['---']
  for (const [key, value] of Object.entries(meta)) {
    if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
      lines.push(`${key}:`)
      lines.push(...formatObjectLines(value as Record<string, unknown>, 1))
    } else {
      lines.push(`${key}: ${formatYamlValue(value)}`)
    }
  }
  lines.push('---')
  return lines.join('\n')
}

function formatYamlValue(value: unknown): string {
  if (Array.isArray(value)) {
    if (value.length === 0) return '[]'
    return `[${value.map(v => quoteIfNeeded(String(v))).join(', ')}]`
  }
  if (value === undefined || value === null) return '""'
  return quoteIfNeeded(String(value))
}

/** Format a data-block value preserving its JS type in YAML. */
function formatDataValue(value: unknown): string {
  if (value === null) return 'null'
  if (typeof value === 'boolean') return value ? 'true' : 'false'
  if (typeof value === 'number') return String(value)
  if (Array.isArray(value)) {
    if (value.length === 0) return '[]'
    return `[${value.map(v => formatDataValue(v)).join(', ')}]`
  }
  return quoteIfNeeded(String(value))
}

function quoteIfNeeded(s: string): string {
  if (s === '' || s.includes(':') || s.includes(',') || s.includes('"')) {
    if (s.includes("'")) {
      return `"${s.replace(/"/g, '\\"')}"`
    }
    return `'${s}'`
  }
  return s
}

export function isSystemCardId(id: string): boolean {
  return id.includes(':') || id.startsWith('__builtin_')
}

export function isVirtualMountNode(id: string): boolean {
  return id.startsWith('__builtin_')
}

const CANONICAL_MONO_CARD_TYPES: ReadonlySet<string> = new Set([
  'agent',
  'prompt',
  'skill',
  'concept',
  'callable',
  'capability_module',
  'crawl',
  'scheduler',
  'task',
  'wiki',
  'workflow',
  'bundle',
])

/** Mirrors pkg/actor/project normalizeCardType: legacy types collapse into the
 *  canonical set and the empty string defaults to "wiki". Unknown values are
 *  returned lowercased so validateMonoCard can reject them. */
export function normalizeMonoCardType(t: string | undefined): string {
  const s = (t ?? '').trim().toLowerCase()
  switch (s) {
    case '':
    case 'note':
    case 'comment':
    case 'knowledge':
      return 'wiki'
    case 'reminder':
      return 'scheduler'
    case 'plan':
    case 'goal':
    case 'kanban-task':
      return 'task'
  }
  return s
}

export function isCanonicalMonoCardType(t: string | undefined): boolean {
  return CANONICAL_MONO_CARD_TYPES.has(normalizeMonoCardType(t))
}

/** A type-driven virtual mount rule. The backend emits one builtin card per
 *  rule carrying these fields in its `data` block (builtinRole=mount); the
 *  frontend derives the rule table from those cards instead of hardcoding it,
 *  so the backend registry stays the single source of truth. */
export interface MountSpec {
  virtualNode: string
  mountType: string
  /** Canonical task status this node receives; undefined for catch-all types. */
  mountStatus?: string
  autoMount: boolean
}

/** Derive the mount spec table from the builtin virtual-node cards present in
 *  the card list. Each builtin mount node carries data.builtinRole="mount" plus
 *  mountType / mountStatus / autoMount. This is the frontend's only view of the
 *  routing rules — there is no hardcoded mirror of the backend table. */
export function deriveMountSpecs(cards: readonly MonoCardListItem[]): MountSpec[] {
  const specs: MountSpec[] = []
  for (const c of cards) {
    if (c.data?.builtinRole !== 'mount') continue
    const mountStatus = c.data.mountStatus
    specs.push({
      virtualNode: c.id,
      mountType: String(c.data.mountType ?? ''),
      mountStatus: typeof mountStatus === 'string' && mountStatus !== '' ? mountStatus : undefined,
      autoMount: c.data.autoMount !== false,
    })
  }
  return specs
}

/** The virtual node id for a canonical task status, derived from the mount
 *  specs. Falls back to undefined when the node is absent from the card list. */
function taskStatusNode(specs: readonly MountSpec[]): Record<string, string> {
  const node: Record<string, string> = {}
  for (const s of specs) {
    if (s.mountType === 'task' && s.mountStatus) node[s.mountStatus] = s.virtualNode
  }
  return node
}

/** Mirrors pkg/actor/project mountTargetForCard. Returns the virtual builtin
 *  node id a card auto-mounts to by type (and status for tasks), or '' when the
 *  type opts out (concept) or is unknown. Mount rules come from `specs`
 *  (derived from builtin cards via deriveMountSpecs); task status aliases come
 *  from `aliases` (resolved from the status-map card via the task-status
 *  module), layered over the built-in defaults. Anything unrecognized,
 *  including empty status, defaults to backlog. */
export function mountTargetForCard(
  cardType: string | undefined,
  cardStatus: string | undefined,
  specs: readonly MountSpec[],
  aliases: Record<string, string>,
): string {
  const t = normalizeMonoCardType(cardType)
  if (t === 'task') {
    const node = taskStatusNode(specs)
    let status = (cardStatus ?? '').trim().toLowerCase()
    if (!(status in node)) {
      status = aliases[status] ?? 'backlog'
    }
    return node[status] ?? node.backlog ?? ''
  }
  // Non-task types: use the first matching catch-all spec (no mountStatus).
  for (const s of specs) {
    if (!s.autoMount || s.mountType !== t || s.mountStatus) continue
    return s.virtualNode
  }
  return ''
}

type MonoCardValidator = (card: MonoCard) => string[]

const MONO_CARD_VALIDATORS: Record<MonoCardType, MonoCardValidator> = {
  agent: () => [],
  wiki: () => [],
  task: validateTaskMonoCard,
  scheduler: validateSchedulerMonoCard,
  prompt: validatePromptMonoCard,
  skill: validateSkillMonoCard,
  concept: () => [],
  callable: validateCallableMonoCard,
  capability_module: validateCapabilityModuleMonoCard,
  workflow: () => [],
  bundle: () => [],
}

/** Repair common misspellings and synonyms in raw frontmatter before parsing.
 *  This is intentionally conservative: it only rewrites well-known status and
 *  priority variants, never inventing data. It runs before `splitFrontmatter` so
 *  validators see canonical values. */
/** Insert a newline before a frontmatter closing --- that was glued onto the
 *  last field's value (e.g. `category: "code"---`). Backend writers produced
 *  such files; splitFrontmatter requires the closer at line start, so without
 *  this repair those cards parse as body-only text and open as "Card not
 *  found". Mirrors the backend's normalizeFrontmatterCloser. */
function repairGluedFrontmatterCloser(raw: string): string {
  if (!raw.startsWith('---\n')) return raw
  const idx = raw.indexOf('---', 4)
  if (idx < 0 || raw[idx - 1] === '\n') return raw
  return raw.slice(0, idx) + '\n' + raw.slice(idx)
}

export function repairMonoCardRaw(raw: string): string {
  let repaired = repairGluedFrontmatterCloser(normalizeLineEndings(raw))
  repaired = repaired.replace(/^status:\s*["']?([^"'\n]+)["']?\s*$/gim, (_, value: string) => {
    const canonical = repairTaskStatus(value, DEFAULT_STATUS_ALIASES)
    return canonical === value ? `status: ${value}` : `status: ${canonical}`
  })
  repaired = repaired
    .replace(/^priority:\s*["']?(normal|moderate|mid)["']?\s*$/gim, 'priority: medium')
    .replace(/^priority:\s*["']?hi["']?\s*$/gim, 'priority: high')
    .replace(/^priority:\s*["']?(critical|crit|asap|blocking|blocker)["']?\s*$/gim, 'priority: urgent')
  return repaired
}

/** Repair a parsed card in place. Maps well-known status and priority aliases
 *  to canonical values, leaving unknown values untouched for validation to catch. */
export function repairMonoCard(card: MonoCard): MonoCard {
  if (card.status) {
    const repaired = repairTaskStatus(card.status, DEFAULT_STATUS_ALIASES)
    if (repaired !== card.status) {
      card = { ...card, status: repaired }
    }
  }
  if (card.priority) {
    const p = (card.priority as string).trim().toLowerCase()
    let repaired: string | undefined
    switch (p) {
      case 'normal':
      case 'moderate':
      case 'mid':
        repaired = 'medium'
        break
      case 'hi':
        repaired = 'high'
        break
      case 'critical':
      case 'crit':
      case 'asap':
      case 'blocking':
      case 'blocker':
        repaired = 'urgent'
        break
    }
    if (repaired && repaired !== card.priority) {
      card = { ...card, priority: repaired as ReminderPriority }
    }
  }
  return card
}

function validateTaskMonoCard(card: MonoCard): string[] {
  const errors: string[] = []
  const status = (card.status ?? '').trim().toLowerCase()
  if (status && !CANONICAL_TASK_STATUSES.includes(status as CanonicalTaskStatus)) {
    errors.push(`task status "${card.status}" is not recognized`)
  }
  if (card.due?.trim()) {
    if (Number.isNaN(Date.parse(card.due))) {
      errors.push(`task due date "${card.due}" must be ISO8601/RFC3339`)
    }
  }
  const priority = ((card.priority as string | undefined) ?? '').trim().toLowerCase()
  if (priority && !['low', 'medium', 'high', 'urgent'].includes(priority)) {
    errors.push(`task priority "${card.priority}" is not recognized`)
  }
  return errors
}

function validateSchedulerMonoCard(card: MonoCard): string[] {
  const errors: string[] = []
  const data = (card.data ?? {}) as Record<string, unknown>
  const schedule = (data.schedule ?? {}) as Record<string, unknown>
  const cron = String(schedule.cron ?? '').trim()
  const expression = String(schedule.expression ?? '').trim()
  if (cron && cron.split(/\s+/).length !== 5) {
    errors.push('scheduler: cron must contain 5 fields')
  }
  if (cron && expression) {
    errors.push('scheduler: set either cron or expression, not both')
  }
  const executor = String(data.executor ?? '').trim()
  if (!executor) {
    errors.push('scheduler: executor is required')
  }
  return errors
}

function validatePromptMonoCard(card: MonoCard): string[] {
  const errors: string[] = []
  const data = (card.data ?? {}) as Record<string, unknown>
  const placement = String(data.placement ?? '').trim().toLowerCase()
  if (placement && !['role', 'policy', 'system', 'context', 'prompt'].includes(placement)) {
    errors.push(`prompt placement "${data.placement}" is not recognized`)
  }
  return errors
}

function validateSkillMonoCard(card: MonoCard): string[] {
  return validateReferenceFields(card, 'skill', ['requires', 'tools', 'callable'])
}

function validateCallableMonoCard(card: MonoCard): string[] {
  const data = (card.data ?? {}) as Record<string, unknown>
  const callable = String(data.callable ?? '').trim()
  if (!callable) {
    return ['callable: callable id is required']
  }
  return []
}

function validateCapabilityModuleMonoCard(card: MonoCard): string[] {
  const errors: string[] = []
  const data = (card.data ?? {}) as Record<string, unknown>
  const source = String(data.source ?? '').trim()
  if (!source) {
    errors.push('capability_module: source is required')
  }
  errors.push(...validateReferenceFields(card, 'capability_module', ['requires', 'tools', 'callable']))
  return errors
}

function validateReferenceFields(
  card: MonoCard,
  typeName: string,
  fields: string[],
): string[] {
  const errors: string[] = []
  const data = (card.data ?? {}) as Record<string, unknown>
  for (const field of fields) {
    const value = data[field]
    if (value === undefined || value === null || value === '') continue
    const items = Array.isArray(value) ? value : String(value).split(/[,\s]+/)
    for (const item of items) {
      if (String(item).trim() === '') {
        errors.push(`${typeName}: ${field} reference must not be empty`)
      }
    }
  }
  return errors
}

/** Validate a parsed card against the monocard type-driven rules. Mirrors the
 *  backend validateCard so the UI can detect cards that are already invalid on
 *  disk and present them in a read-only repair mode. Returns a list of human
 *  readable error strings (empty when the card is valid). */
export function validateMonoCard(card: MonoCard): string[] {
  const errors: string[] = []
  card = repairMonoCard(card)
  if (!isCanonicalMonoCardType(card.type)) {
    errors.push(`invalid type "${card.type ?? ''}"`)
  }
  // System cards (builtin virtual nodes, namespaced ids) may carry reserved
  // tags and are exempt from the user-card structural constraints.
  if (isSystemCardId(card.id)) {
    return errors
  }
  for (const tag of card.tags) {
    if (tag === 'builtin' || tag.startsWith('__builtin_')) {
      errors.push(`forbidden tag "${tag}" (builtin tags are reserved)`)
    }
  }
  if (card.parent && card.parent !== card.id && !card.tags.includes(card.parent)) {
    errors.push(`parent "${card.parent}" must be one of the card's tags`)
  }
  const normalizedType = normalizeMonoCardType(card.type)
  const validator = MONO_CARD_VALIDATORS[normalizedType as MonoCardType]
  if (validator) {
    errors.push(...validator(card))
  }
  return errors
}

/** Parse a raw .md file into a MonoCard. Returns null if frontmatter is missing. */
export function parseMonoCard(id: string, raw: string): MonoCard | null {
  const { meta, body } = splitFrontmatter(repairMonoCardRaw(raw))
  if (Object.keys(meta).length === 0) {
    return null
  }
  const tags = toStringArray(meta.tags)
  const list = toStringArray(meta.list)
  const data = meta.data && typeof meta.data === 'object' && !Array.isArray(meta.data)
    ? meta.data as Record<string, unknown>
    : undefined
  const card: MonoCard = {
    id: str(meta.id) || id,
    type: str(meta.type) || str(data?.type) || str(data?.componentKind) || 'wiki',
    source: str(meta.source) || str(data?.source),
    storage: str(meta.storage) || str(data?.storage),
    visibility: str(meta.visibility) || str(data?.visibility),
    protected: meta.protected === true || data?.protected === true,
    editable: typeof meta.editable === 'boolean' ? meta.editable : data?.editable !== false,
    deletable: typeof meta.deletable === 'boolean' ? meta.deletable : data?.deletable !== false,
    tags,
    list,
    created: str(meta.created),
    modified: str(meta.modified),
    body,
    raw,
  }
  if (meta.due) card.due = str(meta.due)
  if (meta.priority) card.priority = reminderPriority(meta.priority)
  if (meta.status) card.status = cardStatus(meta.status)
  if (meta.parent) card.parent = str(meta.parent)
  if (meta.standalone === true) card.standalone = true
  if (meta.data && typeof meta.data === 'object' && !Array.isArray(meta.data)) {
    const data: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(meta.data as Record<string, unknown>)) {
      data[k] = v
    }
    if (Object.keys(data).length > 0) card.data = data
  }
  return card
}

/** Serialise a MonoCard back to raw markdown with frontmatter. */
export function formatMonoCard(card: MonoCard): string {
  const meta: Record<string, unknown> = {
    id: card.id,
    type: card.type || 'wiki',
    tags: card.tags,
    list: card.list,
    created: card.created,
    modified: card.modified,
  }
  if (card.due) meta.due = card.due
  if (card.priority) meta.priority = card.priority
  if (card.status) meta.status = card.status
  if (card.parent) meta.parent = card.parent
  if (card.standalone) meta.standalone = true
  if (card.data && Object.keys(card.data).length > 0) meta.data = card.data
  return `${formatFrontmatter(meta)}\n\n${normalizeLineEndings(card.body).replace(/^\n+/, '')}\n`
}

/**
 * Keys inside `card.data` that carry system/UI metadata rather than
 * user-facing custom fields. They must be excluded from the generic data
 * editor and the read-only data panel so they are never shown as plain rows.
 */
export const RESERVED_DATA_KEYS = new Set(['visual', 'review_evidence'])

/** Return only the user-facing entries of a card's data block. */
export function userDataEntries(data: Record<string, unknown> | undefined): Array<[string, unknown]> {
  if (!data) return []
  return Object.entries(data).filter(([key]) => !RESERVED_DATA_KEYS.has(key))
}

// Mirrors pkg/actor/project/external_card.go sanitizePathSegment: replaces
// any run of characters outside [a-zA-Z0-9._~-] with a single '-'.
const UNSAFE_PATH_SEGMENT_RE = /[^a-zA-Z0-9._~-]+/g
function sanitizePathSegment(name: string): string {
  return name.replace(UNSAFE_PATH_SEGMENT_RE, '-')
}

/**
 * Builds an external agent card ID from a raw agent ID, applying the same
 * path-segment sanitization the backend (agentCardID) uses. Agent spawn
 * names may contain '#' (e.g. "Architect#a3f8"), which the backend sanitizes
 * to "Architect-a3f8" before forming the card ID "agent:Architect-a3f8".
 * The frontend must apply the same transformation when constructing card IDs
 * for navigation, otherwise the ID won't match the card in the list.
 */
export function agentCardId(agentId: string): string {
  return 'agent:' + sanitizePathSegment(agentId)
}

/** Derive a stable card ID from a user-facing title.
 *  Mirrors the slugification used by the project wiki store so that a card
 *  created with a given title can later be resolved by its title or slug. */
export function cardIdFromTitle(title: string): string {
  const base = title.trim().toLowerCase().replace(/[^a-z0-9\u4e00-\u9fa5]+/g, '-').replace(/^-|-$/g, '')
  if (!base) return 'untitled'
  return base.slice(0, 60)
}

function reminderPriority(v: unknown): ReminderPriority | undefined {
  const s = str(v)
  if (s === 'low' || s === 'medium' || s === 'high' || s === 'urgent') return s
  return undefined
}

function cardStatus(v: unknown): string | undefined {
  const s = str(v)
  return s === '' ? undefined : s
}

function str(v: unknown): string {
  if (v === undefined || v === null) return ''
  return String(v)
}

function toStringArray(v: unknown): string[] {
  if (Array.isArray(v)) return v.map(s => String(s))
  if (typeof v === 'string') return v ? v.split(',').map(s => s.trim()) : []
  return []
}

export function toListItem(card: MonoCard): MonoCardListItem {
  const item: MonoCardListItem = {
    id: card.id,
    type: card.type || 'wiki',
    source: card.source || 'project',
    storage: card.storage || 'cardstore',
    visibility: card.visibility || 'wiki',
    protected: card.protected === true,
    editable: card.editable !== false,
    deletable: card.deletable !== false,
    tags: card.tags,
    list: card.list,
    modified: card.modified,
    created: card.created,
  }
  if (card.due) item.due = card.due
  if (card.priority) item.priority = card.priority
  if (card.status) item.status = card.status
  if (card.parent) item.parent = card.parent
  if (card.standalone) item.standalone = true
  // Preserve the data block (carries UI metadata such as `visual`) so list
  // items rebuilt from a fetched card — e.g. by the file watcher after a
  // save — keep the same appearance as the authoritative server list item.
  if (card.data && Object.keys(card.data).length > 0) item.data = { ...card.data }
  if (card.raw) item.raw = card.raw
  return item
}
