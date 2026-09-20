import { client } from './generated-client'
import * as wiki from '../gen-clients/workspace/client'
import * as projectWiki from '../gen-clients/project/client'
import type { Skill } from '../domain/skill-types'
import type { ModelUnit } from '../domain/types'
import { formatMonoCard, parseMonoCard, splitFrontmatter, type MonoCard } from '../domain/mono-types'
import type { MonoCardListItem } from '../gen-types/project.wiki.part1'

/** Stable prefix for external (read-only, non-persisted) provider skill cards. */
export const EXTERNAL_SKILL_PREFIX = 'ext-skill:'

/**
 * Resolve a skill reference to its wiki card id. Bare skill names get the
 * legacy `skill:` prefix; namespaced ids (`skill:`, `ext-skill:`, `plugin:`,
 * ...) are already fully-qualified and pass through unchanged.
 */
function cardId(id: string): string {
  return id.includes(':') ? id : `skill:${id}`
}

/** True for external (read-only) provider skill cards such as ext-skill:... */
export function isExternalSkill(skill: Skill): boolean {
  return skill.Id.startsWith(EXTERNAL_SKILL_PREFIX)
}

function parseExternalSkillId(id: string): { source: string; name: string } | null {
  if (!id.startsWith(EXTERNAL_SKILL_PREFIX)) return null
  const rest = id.slice(EXTERNAL_SKILL_PREFIX.length)
  const idx = rest.indexOf(':')
  if (idx <= 0) return null
  const source = rest.slice(0, idx)
  const name = rest.slice(idx + 1)
  if (!source || !name) return null
  return { source, name }
}

/**
 * Resolve same-name skill collisions by priority so each skill name appears at
 * most once. Higher-priority sources override lower-priority ones.
 *
 * Priority (high → low): project internal > system internal > project external.
 * This mirrors the backend manifest authority (`fetchFilteredSkillManifest`)
 * so the slash menu, mounting and skill display stay consistent. Within the
 * same class the first-seen entry wins (stable ordering).
 *
 * `listSkills` returns every card unresolved (the display path keeps external
 * read-only cards); consumers that need a single entry per name — e.g. the
 * slash command search — apply this resolver.
 */
export function resolveSkillOverrides(skills: Skill[]): Skill[] {
  const winnerByName = new Map<string, Skill>()
  for (const skill of skills) {
    const name = skillNameKey(skill)
    const prev = winnerByName.get(name)
    if (!prev || skillPriorityClass(skill) > skillPriorityClass(prev)) {
      winnerByName.set(name, skill)
    }
  }
  // Emit winners preserving the original first-appearance order per name.
  const seen = new Set<string>()
  const resolved: Skill[] = []
  for (const skill of skills) {
    const name = skillNameKey(skill)
    if (seen.has(name)) continue
    seen.add(name)
    resolved.push(winnerByName.get(name)!)
  }
  return resolved
}

/** Stable skill name used to detect same-name collisions across sources. */
function skillNameKey(skill: Skill): string {
  if (isExternalSkill(skill)) {
    const parsed = parseExternalSkillId(skill.Id)
    return parsed ? parsed.name : skill.Id
  }
  return skill.Id
}

/**
 * Numeric priority class for same-name resolution:
 * project internal (2) > system internal (1) > project external (0).
 */
function skillPriorityClass(skill: Skill): number {
  if (isExternalSkill(skill)) return 0
  if (skill.Source === 'builtin') return 1
  return 2
}

function stringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []
}

function firstString(value: unknown): string | undefined {
  if (typeof value === 'string') return value
  if (Array.isArray(value) && typeof value[0] === 'string') return value[0]
  return undefined
}

/**
 * Extract a YAML inline-list value for a (possibly hyphenated) frontmatter key.
 * The mono-card frontmatter parser only recognises `\w+` keys, so hyphenated
 * keys like `allowed-tools` are read here directly from the raw text.
 */
function extractListField(raw: string, field: string): string[] {
  const re = new RegExp(`^${field}:\\s*\\[\\s*(.*)\\s*\\]\\s*$`, 'm')
  const m = raw.match(re)
  if (!m || m[1] === undefined) return []
  const inner = m[1].trim()
  if (!inner) return []
  return inner.split(',').map(s => s.trim().replace(/^["']|["']$/g, '')).filter(Boolean)
}

function skillFromCard(card: MonoCard, item?: MonoCardListItem): Skill {
  const data = card.data ?? {}
  const id = card.id.replace(/^skill:/, '')
  const template = card.body || ''
  const visual = (data.visual && typeof data.visual === 'object' && !Array.isArray(data.visual)) ? data.visual as Record<string, unknown> : {}
  return {
    Id: id,
    Source: item?.Source ?? (typeof data.source === 'string' ? data.source : 'user'),
    Version: typeof data.version === 'number' ? data.version : 1,
    ForkOf: typeof data.forkOf === 'string' ? data.forkOf : '',
    BuiltinKey: typeof data.builtinKey === 'string' ? data.builtinKey : '',
    Name: card.id || id,
    Description: typeof data.description === 'string' ? data.description : '',
    Tags: card.tags.filter(tag => !['component', 'skill'].includes(tag)),
    Tools: stringArray(data.tools),
    Params: stringArray(data.params),
    Context: typeof data.context === 'string' ? data.context : 'inline',
    Permission: typeof data.permission === 'string' ? data.permission : '',
    SubAgentType: typeof data.subAgentType === 'string' ? data.subAgentType : '',
    Unit: (data.unit && typeof data.unit === 'object' ? data.unit : { model: '', provider: '' }) as ModelUnit,
    Template: template,
    // When the authoritative list item is available, trust its backend-normalized
    // flags; otherwise fall back to inferring from the raw card data.
    Editable: item ? item.Editable !== false : (data.editable !== false && data.source !== 'builtin'),
    Deletable: item ? item.Deletable === true : (data.deletable !== false && data.source !== 'builtin'),
    ProjectID: typeof data.projectId === 'string' ? data.projectId : undefined,
    // Extra metadata consumed by card-grid UIs (not part of the domain type).
    CardId: card.id,
    Icon: typeof visual.icon === 'string' ? visual.icon : undefined,
  } as Skill
}

/**
 * Build a read-only Skill from an external skill's raw SKILL.md content.
 * External skills (ext-skill:<source>:<name>) are always read-only: the
 * backend rejects Save/Delete and normalizes Protected/Editable/Deletable on
 * the list item, so the authoritative flags come from `item` when available.
 */
function externalSkillFromRaw(id: string, raw: string, item?: MonoCardListItem): Skill {
  const parsed = parseExternalSkillId(id)
  const { meta } = splitFrontmatter(raw)
  const tools = extractListField(raw, 'allowed-tools')
  const params = meta && Array.isArray(meta.arguments) ? stringArray(meta.arguments) : []
  return {
    Id: id,
    Source: item?.Source || parsed?.source || 'external',
    Version: 1,
    ForkOf: '',
    BuiltinKey: '',
    Name: firstString(meta?.name) || parsed?.name || id,
    Description: firstString(meta?.description) ?? '',
    Tags: (item?.Tags ?? []).filter(tag => !['component', 'skill', 'external'].includes(tag)),
    Tools: tools,
    Params: params,
    Context: firstString(meta?.context) || 'inline',
    Permission: '',
    SubAgentType: '',
    Unit: { model: '', provider: '' },
    Template: raw,
    Editable: item ? item.Editable !== false : false,
    Deletable: item ? item.Deletable === true : false,
  }
}

export async function listSkills(_client = client, projectId?: string | null): Promise<Skill[]> {
  const [workspaceResponse, projectResponse] = await Promise.all([
    wiki.wikiListCards(_client, { Flat: true, IncludeRaw: false, Limit: -1 }),
    projectId ? projectWiki.wikiListCards(_client, { Flat: true, IncludeRaw: false, Limit: -1 }, { target: projectId }) : Promise.resolve(null),
  ])
  const entries = [
    ...(workspaceResponse.Cards ?? []).map(item => ({ item, scope: 'workspace' as const })),
    ...(projectResponse?.Cards ?? []).map(item => ({ item, scope: 'project' as const })),
  ].filter(({ item }) => item.Type === 'skill' || item.Data?.componentKind === 'skill')
  const skills = await Promise.all(entries.map(async ({ item, scope }) => {
    if (item.Id.startsWith(EXTERNAL_SKILL_PREFIX)) {
      const raw = await fetchCardRaw(_client, item.Id, scope === 'project' ? projectId : undefined) ?? ''
      return externalSkillFromRaw(item.Id, raw, item)
    }
    const card = await getParsedCard(_client, item.Id, scope === 'project' ? projectId : undefined)
    return card ? skillFromCard(card, item) : null
  }))
  return skills.filter((skill): skill is Skill => skill !== null)
}

export async function getSkill(_client: typeof client, id: string, projectId?: string | null): Promise<Skill> {
  if (id.startsWith(EXTERNAL_SKILL_PREFIX)) {
    const raw = await fetchCardRaw(_client, id, projectId)
    if (raw === null) throw new Error(`Skill ${id} not found`)
    return externalSkillFromRaw(id, raw)
  }
  const card = await getParsedCard(_client, id, projectId)
  if (!card) throw new Error(`Skill ${id} not found`)
  return skillFromCard(card)
}

async function fetchCardRaw(_client: typeof client, id: string, projectId?: string | null): Promise<string | null> {
  try {
    const response = projectId
      ? await projectWiki.wikiGetCard(_client, { Id: cardId(id) }, { target: projectId })
      : await wiki.wikiGetCard(_client, { Id: cardId(id) })
    return response.Raw || null
  } catch {
    return null
  }
}

async function getParsedCard(_client: typeof client, id: string, projectId?: string | null): Promise<MonoCard | null> {
  const raw = await fetchCardRaw(_client, id, projectId)
  if (raw === null) return null
  return parseMonoCard(cardId(id), raw)
}

type SkillSaveInput = { Id: string } & Partial<Omit<Skill, 'Id' | 'Editable' | 'Deletable'>>

export async function saveSkill(_client: typeof client, input: SkillSaveInput): Promise<Skill> {
  const { Id: id, ...draft } = input
  const idValue = cardId(id)
  let existing: MonoCard | null = null
  try {
    existing = await getParsedCard(_client, idValue)
  } catch {
    existing = null
  }
  const now = new Date().toISOString()
  const card: MonoCard = {
    id: draft.Name || idValue.replace(/^skill:/, ''),
    tags: ['component', 'skill', ...(draft.Tags ?? [])],
    list: [],
    created: existing?.created || now,
    modified: now,
    body: draft.Template || '',
    data: {
      source: draft.Source || 'user',
      version: draft.Version || 1,
      forkOf: draft.ForkOf || undefined,
      builtinKey: draft.BuiltinKey || undefined,
      description: draft.Description || '',
      tools: draft.Tools,
      params: draft.Params,
      context: draft.Context || 'inline',
      permission: draft.Permission || '',
      subAgentType: draft.SubAgentType || '',
      unit: draft.Unit,
      editable: true,
      deletable: true,
      projectId: draft.ProjectID,
    },
    raw: '',
  }
  card.raw = formatMonoCard(card)
  const response = existing
    ? await wiki.wikiEditCard(_client, { Id: idValue, Raw: card.raw })
    : await wiki.wikiCreateCard(_client, { Id: idValue, Raw: card.raw })
  const parsed = parseMonoCard(idValue, response.Card.Raw || card.raw)
  if (!parsed) throw new Error(`Skill ${idValue} could not be read after saving`)
  return skillFromCard(parsed)
}

export async function deleteSkill(_client: typeof client, id: string): Promise<void> {
  await wiki.wikiDeleteCard(_client, { Id: cardId(id) })
}
