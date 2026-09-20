import { describe, expect, it } from 'vitest'
import {
  splitFrontmatter,
  parseMonoCard,
  formatMonoCard,
  toListItem,
  DATA_FIELD_TYPES,
  DATA_FIELD_CARD,
  cardReferenceId,
  makeCardReference,
  dataFieldTypeDefOf,
  dataFieldTypeDefById,
  agentCardId,
  isSystemCardId,
  repairMonoCardRaw,
  repairMonoCard,
  validateMonoCard,
  deriveMountSpecs,
  mountTargetForCard,
  userDataEntries,
} from './mono-types'
import type { MonoCard, MonoCardListItem, ReminderPriority } from './mono-types'
import { CANONICAL_TASK_STATUSES } from './task-status'

describe('splitFrontmatter', () => {
  it('extracts frontmatter and body', () => {
    const raw = '---\ntitle: Hello\ntags: [a, b]\n---\n\nSome content here.'
    const { meta, body } = splitFrontmatter(raw)
    expect(meta.title).toBe('Hello')
    expect(meta.tags).toEqual(['a', 'b'])
    expect(body.trim()).toBe('Some content here.')
  })

  it('returns empty meta when no frontmatter', () => {
    const { meta, body } = splitFrontmatter('Just markdown\nno frontmatter')
    expect(Object.keys(meta)).toHaveLength(0)
    expect(body).toBe('Just markdown\nno frontmatter')
  })

  it('handles quoted values', () => {
    const raw = '---\ntitle: "Has: colon"\n---\n\nbody'
    const { meta } = splitFrontmatter(raw)
    expect(meta.title).toBe('Has: colon')
  })
})

describe('line endings', () => {
  it('normalizes CRLF when parsing and formatting cards', () => {
    const raw = '---\r\nid: crlf\r\ntags: []\r\n---\r\n\r\nLine one\r\nLine two\r\n'
    const card = parseMonoCard('crlf', raw)!
    expect(card.body).toBe('\nLine one\nLine two\n')
    expect(formatMonoCard({ ...card, body: 'Changed\r\nBody' })).not.toContain('\r')
  })
})

describe('parseMonoCard', () => {
  it('parses a wiki card', () => {
    const raw = '---\nid: My Note\ntype: wiki\ntags: [idea, draft]\ncreated: 2025-01-01T00:00:00Z\nmodified: 2025-01-02T00:00:00Z\n---\n\n# Heading\n\nBody text.'
    const card = parseMonoCard('my-note', raw)
    expect(card).not.toBeNull()
    expect(card!.id).toBe('My Note')
    expect(card!.type).toBe('wiki')
    expect(card!.tags).toEqual(['idea', 'draft'])
    expect(card!.body).toContain('Heading')
  })

  it('parses a scheduler with due and priority', () => {
    const raw = '---\nid: Submit report\ntype: scheduler\ntags: [reminder]\ncreated: "2025-01-01"\nmodified: "2025-01-01"\ndue: "2025-06-01"\npriority: high\n---\n\nDo it.'
    const card = parseMonoCard('submit-report', raw)
    expect(card!.type).toBe('scheduler')
    expect(card!.due).toBe('2025-06-01')
    expect(card!.priority).toBe('high')
    expect(card!.id).toBe('Submit report')
  })

  it('parses a task with status', () => {
    const raw = '---\nid: Build feature\ntype: task\ntags: [kanban-task]\ncreated: "2025-01-01"\nmodified: "2025-01-01"\nstatus: doing\n---\n\nWork in progress.'
    const card = parseMonoCard('build-feature', raw)
    expect(card!.type).toBe('task')
    expect(card!.status).toBe('doing')
    expect(card!.id).toBe('Build feature')
  })

  it('repairs a frontmatter closer glued to the last field value', () => {
    // Backend writers produced "category: \"review\"---" — splitFrontmatter's
    // regex requires the closer at line start, so repairMonoCardRaw inserts
    // the missing newline before parsing.
    const raw = '---\nid: T5\ntype: task\nstatus: todo\ndata:\n  category: "review"---\n\nBody.'
    const card = parseMonoCard('t5', raw)
    expect(card).not.toBeNull()
    expect(card!.id).toBe('T5')
    expect(card!.type).toBe('task')
    expect(card!.body).toContain('Body.')
  })

  it('returns null for markdown without frontmatter', () => {
    const card = parseMonoCard('test', 'Just text, no frontmatter')
    expect(card).toBeNull()
  })

  it('defaults type to wiki when no type field is present', () => {
    const raw = '---\nid: X\ntags: []\ncreated: ""\nmodified: ""\n---\n\nbody'
    const card = parseMonoCard('x', raw)
    expect(card!.type).toBe('wiki')
    expect(card!.id).toBe('X')
  })
})

describe('formatMonoCard round-trip', () => {
  it('formats and re-parses without loss', () => {
    const raw = '---\nid: Round Trip\ntags: [one, two]\ncreated: "2025-01-01"\nmodified: "2025-01-01"\n---\n\nSome body.'
    const card = parseMonoCard('round-trip', raw)!
    const formatted = formatMonoCard(card)
    const reparsed = parseMonoCard('round-trip', formatted)!
    expect(reparsed.id).toBe('Round Trip')
    expect(reparsed.tags).toEqual(['one', 'two'])
    expect(reparsed.body.trim()).toBe('Some body.')
    expect(formatted).toContain('id: Round Trip')
  })

  it('round-trips a nested data.visual block with custom icon + background hex', () => {
    const card: MonoCard = {
      id: 'visual-rt',
      tags: [],
      list: [],
      created: '2025-01-01',
      modified: '2025-01-01',
      body: 'Body.',
      raw: '',
      data: { visual: { icon: 'rocket', accent: 'blue', color: '#2563eb', background: '#0f172a', size: 32 } },
    }
    const formatted = formatMonoCard(card)
    const reparsed = parseMonoCard('visual-rt', formatted)!
    expect(reparsed.data).toBeDefined()
    const visual = (reparsed.data as Record<string, unknown>).visual as Record<string, unknown>
    expect(visual.icon).toBe('rocket')
    expect(visual.accent).toBe('blue')
    expect(visual.color).toBe('#2563eb')
    expect(visual.background).toBe('#0f172a')
    expect(visual.size).toBe(32)
  })
})

describe('toListItem', () => {
  it('creates a list item with summary fields', () => {
    const raw = '---\nid: Task\ntags: [work, kanban-task]\ncreated: "2025-01-01"\nmodified: "2025-01-01"\nstatus: todo\nparent: parent-card\n---\n\nBody.'
    const card = parseMonoCard('task', raw)!
    const item = toListItem(card)
    expect(item.id).toBe('Task')
    expect(item.status).toBe('todo')
    expect(item.parent).toBe('parent-card')
    expect(item.tags).toEqual(['work', 'kanban-task'])
  })

  it('preserves the data block (visual) so rebuilt list items keep appearance', () => {
    const raw = '---\nid: Visual\ntags: []\ncreated: "2025-01-01"\nmodified: "2025-01-01"\ndata:\n  visual:\n    icon: rocket\n    accent: blue\n---\n\nBody.'
    const card = parseMonoCard('visual', raw)!
    const item = toListItem(card)
    expect(item.data).toBeDefined()
    const visual = (item.data as Record<string, unknown>).visual as Record<string, unknown>
    expect(visual.icon).toBe('rocket')
    expect(visual.accent).toBe('blue')
  })

  it('preserves raw so event-driven list rebuilds keep the card body', () => {
    const raw = '---\nid: scheduler:test\ntype: scheduler\ntags: [scheduler]\ncreated: "2025-01-01"\nmodified: "2025-01-01"\ndata:\n  schedule:\n    cron: "0 9 * * *"\n  schedule_type: prompt\n---\n\nMy prompt body'
    const card = parseMonoCard('scheduler:test', raw)!
    const item = toListItem(card)
    expect(item.raw).toBe(raw)
  })
})

describe('standalone cards', () => {
  it('parses and serializes standalone', () => {
    const raw = '---\nid: Standalone\nstandalone: true\n---\n\nBody.'
    const card = parseMonoCard('standalone', raw)!
    expect(card.standalone).toBe(true)
    expect(parseMonoCard('standalone', formatMonoCard(card))!.standalone).toBe(true)
  })
})

describe('multi-line YAML arrays', () => {
  it('parses multi-line tags', () => {
    const raw = '---\nid: Multi\ntags:\n  - idea\n  - draft\ncreated: "2025-01-01"\nmodified: "2025-01-01"\n---\n\nBody.'
    const card = parseMonoCard('multi', raw)
    expect(card!.tags).toEqual(['idea', 'draft'])
  })

  it('parses multi-line list', () => {
    const raw = '---\nid: Multi\nlist:\n  - one\n  - two\ncreated: "2025-01-01"\nmodified: "2025-01-01"\n---\n\nBody.'
    const card = parseMonoCard('multi-list', raw)
    expect(card!.list).toEqual(['one', 'two'])
  })
})

describe('custom data block', () => {
  it('parses string, number, boolean, and array values', () => {
    const raw = [
      '---',
      'id: Data Card',
      'created: "2025-01-01"',
      'modified: "2025-01-01"',
      'data:',
      '  author: Alice',
      '  count: 42',
      '  active: true',
      '  score: 3.14',
      '  tags: [a, b]',
      '---',
      '',
      'Body.',
    ].join('\n')
    const card = parseMonoCard('data-card', raw)!
    expect(card.data).toEqual({
      author: 'Alice',
      count: 42,
      active: true,
      score: 3.14,
      tags: ['a', 'b'],
    })
  })

  it('round-trips data through format and re-parse', () => {
    const raw = [
      '---',
      'id: Round',
      'created: "2025-01-01"',
      'modified: "2025-01-01"',
      'data:',
      '  name: Bob',
      '  count: 7',
      '  ok: false',
      '---',
      '',
      'Body.',
    ].join('\n')
    const card = parseMonoCard('round', raw)!
    const formatted = formatMonoCard(card)
    const reparsed = parseMonoCard('round', formatted)!
    expect(reparsed.data).toEqual({ name: 'Bob', count: 7, ok: false })
  })

  it('round-trips nested objects in the data block', () => {
    const card = parseMonoCard('nested', '---\nid: Nested\ntags: []\ncreated: ""\nmodified: ""\n---\n\nBody.')!
    card.data = { visual: { icon: 'rocket', iconType: 'lucide', accent: 'blue', emphasis: 'strong' }, author: 'Alice', count: 3 }
    const formatted = formatMonoCard(card)
    const reparsed = parseMonoCard('nested', formatted)!
    expect(reparsed.data).toEqual({ visual: { icon: 'rocket', iconType: 'lucide', accent: 'blue', emphasis: 'strong' }, author: 'Alice', count: 3 })
    expect(formatted).toContain('visual:')
    expect(formatted).not.toContain('[object Object]')
  })

  it('parses an inline nested data block', () => {
    const raw = [
      '---',
      'id: Nested',
      'created: "2025-01-01"',
      'modified: "2025-01-01"',
      'data:',
      '  visual:',
      '    icon: rocket',
      '    iconType: lucide',
      '    accent: blue',
      '  author: Alice',
      '---',
      '',
      'Body.',
    ].join('\n')
    const card = parseMonoCard('nested', raw)!
    expect(card.data).toEqual({ visual: { icon: 'rocket', iconType: 'lucide', accent: 'blue' }, author: 'Alice' })
  })

  it('handles values containing colons', () => {
    const raw = [
      '---',
      'id: Colon',
      'created: "2025-01-01"',
      'modified: "2025-01-01"',
      'data:',
      '  url: "https://example.com"',
      '---',
      '',
      'Body.',
    ].join('\n')
    const card = parseMonoCard('colon', raw)!
    expect(card.data!.url).toBe('https://example.com')
    const reparsed = parseMonoCard('colon', formatMonoCard(card))!
    expect(reparsed.data!.url).toBe('https://example.com')
  })

  it('omits data when block is empty', () => {
    const raw = '---\nid: No Data\ncreated: ""\nmodified: ""\n---\n\nBody.'
    const card = parseMonoCard('no-data', raw)!
    expect(card.data).toBeUndefined()
  })

  it('round-trips a JSON-string data value (agent_actions) through format and re-parse', () => {
    const json = JSON.stringify([{ action: 'pause', target: 'agent:coder' }])
    const raw = [
      '---',
      'id: scheduler:agent-action',
      'type: scheduler',
      'tags: [scheduler]',
      'created: "2026-09-10T17:48:15Z"',
      'modified: "2026-09-10T17:48:15Z"',
      'data:',
      '  schedule:',
      '    cron: "0 9 * * *"',
      '    enabled: true',
      '  schedule_type: task',
      `  agent_actions: '${json}'`,
      '---',
      '',
      'Scheduled agent action.',
    ].join('\n')
    const card = parseMonoCard('scheduler:agent-action', raw)!
    expect(card.data!.agent_actions).toBe(json)
    const formatted = formatMonoCard(card)
    const reparsed = parseMonoCard('scheduler:agent-action', formatted)!
    expect(reparsed.data!.agent_actions).toBe(json)
    // The JSON string must survive as valid JSON after the round-trip
    expect(JSON.parse(reparsed.data!.agent_actions as string)).toEqual([{ action: 'pause', target: 'agent:coder' }])
  })
})

describe('card reference data type', () => {
  it('registers a mono-card field type before text', () => {
    expect(DATA_FIELD_TYPES[0]).toBe(DATA_FIELD_CARD)
    expect(DATA_FIELD_CARD.id).toBe('mono-card')
    expect(DATA_FIELD_CARD.inputKind).toBe('card')
  })

  it('detects a card reference value', () => {
    const ref = makeCardReference('my-card')
    expect(DATA_FIELD_CARD.detect(ref)).toBe(true)
    expect(dataFieldTypeDefOf(ref).id).toBe('mono-card')
    expect(cardReferenceId(ref)).toBe('my-card')
  })

  it('coerces arbitrary values to a card reference', () => {
    expect(DATA_FIELD_CARD.coerce('my-card')).toBe(makeCardReference('my-card'))
    expect(DATA_FIELD_CARD.coerce(makeCardReference('other'))).toBe(makeCardReference('other'))
  })

  it('looks up by id and falls back to text', () => {
    expect(dataFieldTypeDefById('mono-card').id).toBe('mono-card')
    expect(dataFieldTypeDefById('unknown').id).toBe('text')
  })

  it('round-trips a card reference through the data block', () => {
    const raw = [
      '---',
      'id: Ref Card',
      'created: "2025-01-01"',
      'modified: "2025-01-01"',
      'data:',
      '  related: "mono-card:target-card"',
      '---',
      '',
      'Body.',
    ].join('\n')
    const card = parseMonoCard('ref-card', raw)!
    expect(card.data!.related).toBe('mono-card:target-card')
    const reparsed = parseMonoCard('ref-card', formatMonoCard(card))!
    expect(reparsed.data!.related).toBe('mono-card:target-card')
    expect(dataFieldTypeDefOf(reparsed.data!.related).id).toBe('mono-card')
  })
})

describe('repairMonoCardRaw', () => {
  it('normalizes task status synonyms before parsing', () => {
    const cases: { raw: string; wantStatus: string }[] = [
      { raw: '---\nid: T\ntype: task\ntags: []\nstatus: in_progress\n---\n\nBody.', wantStatus: 'doing' },
      { raw: '---\nid: T\ntype: task\ntags: []\nstatus: "in gress"\n---\n\nBody.', wantStatus: 'doing' },
      { raw: '---\nid: T\ntype: task\ntags: []\nstatus: completed\n---\n\nBody.', wantStatus: 'done' },
      { raw: '---\nid: T\ntype: task\ntags: []\nstatus: to-do\n---\n\nBody.', wantStatus: 'todo' },
      { raw: '---\nid: T\ntype: task\ntags: []\nstatus: back_log\n---\n\nBody.', wantStatus: 'backlog' },
    ]
    for (const c of cases) {
      const card = parseMonoCard('task', c.raw)!
      expect(card.status).toBe(c.wantStatus)
    }
  })

  it('normalizes task priority synonyms before parsing', () => {
    const cases: { raw: string; wantPriority: string }[] = [
      { raw: '---\nid: T\ntype: task\ntags: []\npriority: normal\n---\n\nBody.', wantPriority: 'medium' },
      { raw: '---\nid: T\ntype: task\ntags: []\npriority: "hi"\n---\n\nBody.', wantPriority: 'high' },
      { raw: '---\nid: T\ntype: task\ntags: []\npriority: asap\n---\n\nBody.', wantPriority: 'urgent' },
    ]
    for (const c of cases) {
      const card = parseMonoCard('task', c.raw)!
      expect(card.priority).toBe(c.wantPriority)
    }
  })

  it('leaves unknown status and priority values untouched', () => {
    const repaired = repairMonoCardRaw('---\nid: T\ntype: task\ntags: []\nstatus: frozen\npriority: mega\n---\n\nBody.')
    expect(repaired).toContain('status: frozen')
    expect(repaired).toContain('priority: mega')
  })

  it('repairs before validation so synonyms pass', () => {
    const card = parseMonoCard('task', '---\nid: T\ntype: task\ntags: []\nstatus: in_progress\npriority: critical\n---\n\nBody.')!
    expect(validateMonoCard(card)).toEqual([])
  })

  it('repairs experimental to doing', () => {
    const raw = '---\nid: T\ntype: task\ntags: []\nstatus: experimental\n---\n\nBody.'
    const card = parseMonoCard('task', raw)!
    expect(card.status).toBe('doing')
    expect(validateMonoCard(card)).toEqual([])
  })
})

describe('validateMonoCard task status whitelist', () => {
  it('accepts every canonical task status, including pending_review', () => {
    for (const status of CANONICAL_TASK_STATUSES) {
      const raw = `---\nid: T\ntype: task\ntags: []\nstatus: ${status}\n---\n\nBody.`
      const card = parseMonoCard('task', raw)!
      expect(validateMonoCard(card)).toEqual([])
    }
  })

  it('accepts a pending_review task card parsed from raw frontmatter', () => {
    const raw = '---\nid: ReviewTask\ntype: task\ntags: [team]\nstatus: pending_review\n---\n\nAwaiting review.'
    const card = parseMonoCard('review-task', raw)!
    expect(card.status).toBe('pending_review')
    expect(validateMonoCard(card)).toEqual([])
  })

  it('rejects an unknown task status', () => {
    const card: MonoCard = {
      id: 'X',
      type: 'task',
      tags: [],
      list: [],
      created: '',
      modified: '',
      body: '',
      raw: '',
      status: 'frozen',
    }
    const errors = validateMonoCard(card)
    expect(errors).toContain('task status "frozen" is not recognized')
  })
})

describe('repairMonoCard', () => {
  it('normalizes status and priority on a parsed card', () => {
    const card = parseMonoCard('task', '---\nid: T\ntype: task\ntags: []\nstatus: experimental\npriority: hi\n---\n\nBody.')!
    const repaired = repairMonoCard(card)
    expect(repaired.status).toBe('doing')
    expect(repaired.priority).toBe('high')
  })

  it('leaves unknown values untouched', () => {
    const card: MonoCard = {
      id: 'U',
      type: 'task',
      tags: [],
      list: [],
      created: '',
      modified: '',
      body: '',
      raw: '',
      status: 'frozen',
      priority: 'mega' as ReminderPriority,
    }
    const repaired = repairMonoCard(card)
    expect(repaired.status).toBe('frozen')
    expect(repaired.priority).toBe('mega')
  })
})

describe('system card title handling', () => {
  it('recognizes builtin ids and ids containing a colon as system cards', () => {
    expect(isSystemCardId('__builtin_skill__')).toBe(true)
    expect(isSystemCardId('agent:Architect')).toBe(true)
    expect(isSystemCardId('my-note')).toBe(false)
  })

  it('preserves frontmatter title for system builtin cards', () => {
    const raw = '---\nid: __builtin_skill__\ntags: []\ncreated: "2025-01-01"\nmodified: "2025-01-01"\n---\n\nBody.'
    const card = parseMonoCard('__builtin_skill__', raw)!
    expect(card.id).toBe('__builtin_skill__')
    const formatted = formatMonoCard(card)
    expect(formatted).toContain('id: __builtin_skill__')
  })

  it('preserves frontmatter title for ordinary cards', () => {
    const raw = '---\nid: ordinary-card\ntags: []\ncreated: "2025-01-01"\nmodified: "2025-01-01"\n---\n\nBody.'
    const card = parseMonoCard('ordinary-card', raw)!
    expect(card.id).toBe('ordinary-card')
    const formatted = formatMonoCard(card)
    expect(formatted).toContain('id: ordinary-card')
  })
})

describe('agentCardId', () => {
  it('prefixes with agent: and preserves safe characters', () => {
    expect(agentCardId('Architect')).toBe('agent:Architect')
    expect(agentCardId('my-agent_1.0~stable')).toBe('agent:my-agent_1.0~stable')
  })

  it('sanitizes unsafe characters like # the same way the backend does', () => {
    // Agent spawn names contain '#' (e.g. "Architect#a3f8"); the backend's
    // sanitizePathSegment replaces runs of unsafe chars with '-'.
    expect(agentCardId('Architect#a3f8')).toBe('agent:Architect-a3f8')
    expect(agentCardId('agent with spaces')).toBe('agent:agent-with-spaces')
    expect(agentCardId('foo/bar@baz')).toBe('agent:foo-bar-baz')
  })

  it('handles UUIDs unchanged', () => {
    expect(agentCardId('019f6a414550-0000-0000-000000000001')).toBe('agent:019f6a414550-0000-0000-000000000001')
  })
})

describe('mount routing derived from builtin cards', () => {
  function builtinMount(id: string, data: Record<string, unknown>): MonoCardListItem {
    return { id, tags: [], list: [], modified: '', data: { builtinRole: 'mount', ...data } }
  }

  // Builtin mount cards exactly as the backend registry emits them: six task
  // status nodes plus non-task catch-all nodes. This is the single source the
  // frontend reads; no hardcoded mirror table is consulted.
  const mountCards: MonoCardListItem[] = [
    builtinMount('__builtin_backlog__', { mountType: 'task', mountStatus: 'backlog', autoMount: true }),
    builtinMount('__builtin_todo__', { mountType: 'task', mountStatus: 'todo', autoMount: true }),
    builtinMount('__builtin_doing__', { mountType: 'task', mountStatus: 'doing', autoMount: true }),
    builtinMount('__builtin_done__', { mountType: 'task', mountStatus: 'done', autoMount: true }),
    builtinMount('__builtin_skill__', { mountType: 'skill', autoMount: true }),
    builtinMount('__builtin_bundle__', { mountType: 'bundle', autoMount: true }),
    builtinMount('__builtin_concept__', { mountType: 'concept', autoMount: false }),
  ]
  const specs = deriveMountSpecs(mountCards)
  // Aliases are layered by the caller (task-status module) over defaults; here
  // we pass a minimal alias set including a card-driven override.
  const aliases = { in_progress: 'doing', completed: 'done', queued: 'doing' }

  it('derives specs only from builtin mount cards', () => {
    expect(specs.map(s => s.virtualNode).sort()).toEqual(
      ['__builtin_backlog__', '__builtin_bundle__', '__builtin_concept__', '__builtin_done__', '__builtin_doing__', '__builtin_skill__', '__builtin_todo__'].sort(),
    )
  })

  it('routes task by canonical status to its derived node', () => {
    expect(mountTargetForCard('task', 'doing', specs, aliases)).toBe('__builtin_doing__')
    expect(mountTargetForCard('task', 'todo', specs, aliases)).toBe('__builtin_todo__')
  })

  it('routes legacy statuses via aliases', () => {
    expect(mountTargetForCard('task', 'in_progress', specs, aliases)).toBe('__builtin_doing__')
    expect(mountTargetForCard('task', 'completed', specs, aliases)).toBe('__builtin_done__')
    expect(mountTargetForCard('task', 'queued', specs, aliases)).toBe('__builtin_doing__')
  })

  it('falls back to backlog for empty or unknown status', () => {
    expect(mountTargetForCard('task', '', specs, aliases)).toBe('__builtin_backlog__')
    expect(mountTargetForCard('task', 'mystery', specs, aliases)).toBe('__builtin_backlog__')
  })

  it('routes non-task types to their catch-all node', () => {
    expect(mountTargetForCard('skill', undefined, specs, aliases)).toBe('__builtin_skill__')
    expect(mountTargetForCard('bundle', undefined, specs, aliases)).toBe('__builtin_bundle__')
  })

  it('opts out concept and unknown types', () => {
    expect(mountTargetForCard('concept', undefined, specs, aliases)).toBe('')
    expect(mountTargetForCard('unknown', undefined, specs, aliases)).toBe('')
  })
})

describe('userDataEntries', () => {
  it('excludes reserved keys including review_evidence', () => {
    const entries = userDataEntries({ author: 'Alice', review_evidence: '[]', visual: { icon: 'x' } })
    expect(entries.map(([k]) => k)).toEqual(['author'])
  })
})
