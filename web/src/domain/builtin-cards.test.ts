import { describe, expect, it } from 'vitest'
import {
  isBuiltinCard,
  isBuiltinTag,
  makeBuiltinId,
  getBuiltinCanonical,
  resolveBuiltinAlias,
  resolveCardId,
  tagMatchesCard,
  BUILTIN_TAG_CANONICALS,
  BUILTIN_CARD_DEFS,
  overrideBuiltinTitle,
} from './builtin-cards'
import type { I18nKey } from '../i18n/types'

const fakeT = (key: I18nKey) => {
  const map: Record<string, string> = {
    'shell.sidebar.cards.toc': '目录',
    'shell.sidebar.cards.skill': '技能',
    'shell.sidebar.cards.task': '任务',
    'shell.sidebar.cards.backlog': '待办池',
    'shell.sidebar.cards.workflow': '工作流',
  }
  return map[key as string] ?? key
}

describe('makeBuiltinId', () => {
  it('returns the id unchanged when it is already a builtin id', () => {
    expect(makeBuiltinId('__builtin_skill__')).toBe('__builtin_skill__')
  })

  it('returns the registered id for canonical tags, including the special root toc', () => {
    expect(makeBuiltinId('toc')).toBe('toc')
    expect(makeBuiltinId('skill')).toBe('__builtin_skill__')
  })

  it('wraps an arbitrary name when it is not a registered builtin id or canonical tag', () => {
    expect(makeBuiltinId('hello')).toBe('__builtin_hello__')
  })
})

describe('getBuiltinCanonical', () => {
  it('extracts canonical tag from a builtin id', () => {
    expect(getBuiltinCanonical('__builtin_skill__')).toBe('skill')
  })

  it('returns null for non-builtin ids, including the unprefixed toc root', () => {
    expect(getBuiltinCanonical('toc')).toBeNull()
    expect(getBuiltinCanonical('ordinary')).toBeNull()
  })
})

describe('isBuiltinTag', () => {
  it('accepts short canonical tags only', () => {
    expect(isBuiltinTag('toc')).toBe(true)
    expect(isBuiltinTag('skill')).toBe(true)
    expect(isBuiltinTag('__builtin_toc__')).toBe(false)
    // summary/constraints/project_info are no longer builtin tags.
    expect(isBuiltinTag('summary')).toBe(false)
    // Mount/status nodes are display-only, not tags.
    expect(isBuiltinTag('task')).toBe(false)
    expect(isBuiltinTag('backlog')).toBe(false)
    expect(isBuiltinTag('concept')).toBe(false)
    expect(isBuiltinTag('workflow')).toBe(false)
  })
})

describe('BUILTIN_TAG_CANONICALS', () => {
  it('contains only short canonicals, not full ids', () => {
    expect(BUILTIN_TAG_CANONICALS).not.toContain('__builtin_toc__')
    expect(BUILTIN_TAG_CANONICALS).toContain('toc')
  })
})

describe('BUILTIN_CARD_DEFS', () => {
  it('covers builtin ids and canonicals consistently, with toc as the unprefixed root', () => {
    for (const def of BUILTIN_CARD_DEFS) {
      if (def.id === 'toc') {
        expect(isBuiltinCard(def.id)).toBe(false)
      } else {
        expect(isBuiltinCard(def.id)).toBe(true)
        expect(def.id).toBe(`__builtin_${def.canonical}__`)
      }
    }
  })

  it('no longer treats summary/constraints/project_info as builtin cards', () => {
    expect(BUILTIN_CARD_DEFS.find(d => d.canonical === 'summary')).toBeUndefined()
    expect(BUILTIN_CARD_DEFS.find(d => d.canonical === 'constraints')).toBeUndefined()
    expect(BUILTIN_CARD_DEFS.find(d => d.canonical === 'project_info')).toBeUndefined()
  })

  it('covers all backend virtual mount and status nodes', () => {
    const mountIds = [
      '__builtin_task__', '__builtin_backlog__', '__builtin_todo__',
      '__builtin_doing__', '__builtin_pending_review__', '__builtin_done__',
      '__builtin_blocked__', '__builtin_cancelled__', '__builtin_concept__',
      '__builtin_callable__', '__builtin_capability_module__',
      '__builtin_scheduler__', '__builtin_workflow__',
    ]
    for (const id of mountIds) {
      expect(BUILTIN_CARD_DEFS.find(d => d.id === id), `missing ${id}`).toBeDefined()
    }
  })
})

describe('resolveBuiltinAlias', () => {
  it('resolves full builtin ids to themselves', () => {
    expect(resolveBuiltinAlias('__builtin_skill__', fakeT)).toBe('__builtin_skill__')
  })

  it('resolves the toc canonical to the unprefixed root id', () => {
    expect(resolveBuiltinAlias('toc', fakeT)).toBe('toc')
    expect(resolveBuiltinAlias('目录', fakeT)).toBe('toc')
  })

  it('resolves other short canonical tags to full builtin ids', () => {
    expect(resolveBuiltinAlias('skill', fakeT)).toBe('__builtin_skill__')
    expect(resolveBuiltinAlias('技能', fakeT)).toBe('__builtin_skill__')
  })

  it('returns null for unknown aliases', () => {
    expect(resolveBuiltinAlias('nope', fakeT)).toBeNull()
  })
})

describe('resolveCardId', () => {
  const cards = [
    { id: 'ordinary' },
    { id: '目录' },
  ]

  it('matches exact titles first', () => {
    expect(resolveCardId('ordinary', cards, fakeT)).toBe('ordinary')
  })

  it('matches builtin ids even when they are not in the ordinary card list', () => {
    expect(resolveCardId('__builtin_skill__', cards, fakeT)).toBe('__builtin_skill__')
  })

  it('resolves builtin aliases', () => {
    expect(resolveCardId('skill', cards, fakeT)).toBe('__builtin_skill__')
  })

  it('resolves the toc root by canonical or localized name', () => {
    expect(resolveCardId('toc', cards, fakeT)).toBe('toc')
    expect(resolveCardId('目录', cards, fakeT)).toBe('目录')
  })

  it('returns null for unknown references', () => {
    expect(resolveCardId('missing-card', cards, fakeT)).toBeNull()
  })
})

describe('tagMatchesCard', () => {
  it('matches ordinary card titles', () => {
    expect(tagMatchesCard('ordinary', { id: 'ordinary' })).toBe(true)
    expect(tagMatchesCard('other', { id: 'ordinary' })).toBe(false)
  })

  it('matches builtin canonicals, not title', () => {
    expect(tagMatchesCard('skill', { id: '__builtin_skill__' })).toBe(true)
    expect(tagMatchesCard('技能', { id: '__builtin_skill__' })).toBe(false)
  })

  it('matches the toc root by its canonical tag', () => {
    expect(tagMatchesCard('toc', { id: 'toc' })).toBe(true)
  })
})

describe('overrideBuiltinTitle', () => {
  it('overrides title for builtin cards using i18n', () => {
    const card = { id: '__builtin_skill__' }
    expect(overrideBuiltinTitle(card, fakeT).title).toBe('技能')
  })

  it('leaves the unprefixed toc root unchanged because it is not a builtin card', () => {
    const card = { id: 'toc' }
    expect(overrideBuiltinTitle(card, fakeT)).toBe(card)
  })

  it('leaves ordinary cards unchanged', () => {
    const card = { id: 'ordinary' }
    expect(overrideBuiltinTitle(card, fakeT)).toBe(card)
  })

  it('overrides title for task aggregator and status nodes', () => {
    expect(overrideBuiltinTitle({ id: '__builtin_task__' }, fakeT).title).toBe('任务')
    expect(overrideBuiltinTitle({ id: '__builtin_backlog__' }, fakeT).title).toBe('待办池')
    expect(overrideBuiltinTitle({ id: '__builtin_workflow__' }, fakeT).title).toBe('工作流')
  })
})
