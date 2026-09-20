import { describe, expect, it, vi, beforeEach } from 'vitest'
import type { WikiListCardsResp, MonoCardListItem, WikiGetCardResp } from '../gen-types/project.wiki.part1'
import type { Skill } from '../domain/skill-types'

// Mock the workspace and project wiki clients used by the store.
vi.mock('../gen-clients/workspace/client', () => ({
  wikiListCards: vi.fn(),
  wikiGetCard: vi.fn(),
}))
vi.mock('../gen-clients/project/client', () => ({
  wikiListCards: vi.fn(),
  wikiGetCard: vi.fn(),
}))

import * as wiki from '../gen-clients/workspace/client'
import * as projectWiki from '../gen-clients/project/client'
import { listSkills, getSkill, isExternalSkill, resolveSkillOverrides, EXTERNAL_SKILL_PREFIX } from './skill-card-store'

const dummyClient = {} as never

function listItem(partial: Partial<MonoCardListItem> & Pick<MonoCardListItem, 'Id'>): MonoCardListItem {
  return {
    Type: 'skill',
    Source: 'user',
    Storage: 'cardstore',
    Visibility: 'component',
    Tags: [],
    List: [],
    Created: '',
    Modified: '',
    Protected: false,
    Editable: true,
    Deletable: true,
    Raw: '',
    ...partial,
  }
}

const projectRaw = `---
id: skill:my-skill
type: skill
tags: [component, skill]
data:
  source: user
  description: A user skill
  tools: [t1]
  editable: true
  deletable: true
---

Body of my-skill.
`

const externalRaw = `---
name: lint
description: Lint the codebase
allowed-tools: [eslint, prettier]
arguments: [target]
---

Lint instructions here.
`

function setList(items: MonoCardListItem[]) {
  vi.mocked(wiki.wikiListCards).mockResolvedValue({ Cards: items } as WikiListCardsResp)
}

function setGet(map: Record<string, string>) {
  vi.mocked(wiki.wikiGetCard).mockImplementation(async (_c, req: { Id: string }): Promise<WikiGetCardResp> => {
    const raw = map[req.Id]
    if (raw === undefined) throw new Error(`card ${req.Id} not found`)
    return { Id: req.Id, Raw: raw }
  })
}

describe('skill-card-store external (read-only) skill integration', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('surfaces external skills as read-only and parses name/description from SKILL.md frontmatter', async () => {
    setList([
      listItem({ Id: 'skill:my-skill', Source: 'user', Editable: true, Deletable: true }),
      listItem({
        Id: 'ext-skill:claude:lint',
        Source: 'claude',
        Storage: 'external',
        Protected: true,
        Editable: false,
        Deletable: false,
        Tags: ['skill', 'component', 'external'],
      }),
    ])
    setGet({ 'skill:my-skill': projectRaw, 'ext-skill:claude:lint': externalRaw })

    const skills = await listSkills(dummyClient)
    const ext = skills.find(s => s.Id === 'ext-skill:claude:lint')!

    expect(ext).toBeDefined()
    expect(ext.Editable).toBe(false)
    expect(ext.Deletable).toBe(false)
    expect(ext.Source).toBe('claude')
    expect(ext.Name).toBe('lint')
    expect(ext.Description).toBe('Lint the codebase')
    expect(ext.Tools).toEqual(['eslint', 'prettier'])
    expect(ext.Params).toEqual(['target'])
    expect(ext.Template).toBe(externalRaw)
    // Built-in external/system tags are stripped from the user-facing tag list.
    expect(ext.Tags).not.toContain('external')
  })

  it('passes the ext-skill: id through to wikiGetCard unchanged (no skill: prefix mangling)', async () => {
    setList([listItem({ Id: 'ext-skill:claude:lint', Source: 'claude', Storage: 'external', Editable: false, Deletable: false })])
    setGet({ 'ext-skill:claude:lint': externalRaw })

    await listSkills(dummyClient)

    const ids = vi.mocked(wiki.wikiGetCard).mock.calls.map(c => (c[1] as { Id: string }).Id)
    expect(ids).toContain('ext-skill:claude:lint')
    expect(ids.some(id => id.startsWith('skill:ext-skill:'))).toBe(false)
  })

  it('merges current-project skills so they can reach the slash menu', async () => {
    setList([])
    vi.mocked(projectWiki.wikiListCards).mockResolvedValue({
      Cards: [listItem({ Id: 'skill:project-only', Source: 'user' })],
    } as WikiListCardsResp)
    vi.mocked(projectWiki.wikiGetCard).mockResolvedValue({
      Id: 'skill:project-only',
      Raw: projectRaw.replace(/my-skill/g, 'project-only'),
    })

    const skills = await listSkills(dummyClient, 'project-1')

    expect(skills.map(skill => skill.Id)).toEqual(['project-only'])
    expect(projectWiki.wikiListCards).toHaveBeenCalledWith(dummyClient, { Flat: true, IncludeRaw: false, Limit: -1 }, { target: 'project-1' })
    expect(projectWiki.wikiGetCard).toHaveBeenCalledWith(dummyClient, { Id: 'skill:project-only' }, { target: 'project-1' })
  })

  it('keeps project/user skills editable and deletable per backend list-item flags', async () => {
    setList([listItem({ Id: 'skill:my-skill', Source: 'user', Editable: true, Deletable: true })])
    setGet({ 'skill:my-skill': projectRaw })

    const skills = await listSkills(dummyClient)
    const project = skills.find(s => s.Id === 'my-skill')!

    expect(project).toBeDefined()
    expect(project.Editable).toBe(true)
    expect(project.Deletable).toBe(true)
    expect(project.Source).toBe('user')
  })

  it('honours authoritative read-only flags for non-external cards too', async () => {
    setList([listItem({ Id: 'skill:locked', Source: 'project', Editable: false, Deletable: false })])
    setGet({ 'skill:locked': projectRaw.replace(/my-skill/g, 'locked') })

    const skills = await listSkills(dummyClient)
    const locked = skills.find(s => s.Id === 'locked')!

    expect(locked.Editable).toBe(false)
    expect(locked.Deletable).toBe(false)
  })

  it('getSkill returns a read-only external skill by ext-skill: id', async () => {
    setGet({ 'ext-skill:codex:refactor': externalRaw.replace('lint', 'refactor').replace('Lint the codebase', 'Refactor helper') })

    const skill = await getSkill(dummyClient, 'ext-skill:codex:refactor')

    expect(skill.Id).toBe('ext-skill:codex:refactor')
    expect(skill.Editable).toBe(false)
    expect(skill.Deletable).toBe(false)
    expect(skill.Source).toBe('codex')
    expect(skill.Name).toBe('refactor')
  })

  it('getSkill returns an editable project skill by bare id', async () => {
    setGet({ 'skill:my-skill': projectRaw })

    const skill = await getSkill(dummyClient, 'my-skill')

    expect(skill.Id).toBe('my-skill')
    expect(skill.Editable).toBe(true)
    expect(skill.Deletable).toBe(true)
  })

  it('falls back to an id-derived name when an external skill has no frontmatter', async () => {
    setList([listItem({ Id: 'ext-skill:claude:plain', Source: 'claude', Storage: 'external', Editable: false, Deletable: false })])
    setGet({ 'ext-skill:claude:plain': 'Just a body with no frontmatter.\n' })

    const skills = await listSkills(dummyClient)
    const plain = skills.find(s => s.Id === 'ext-skill:claude:plain')!

    expect(plain.Name).toBe('plain')
    expect(plain.Editable).toBe(false)
    expect(plain.Source).toBe('claude')
  })

  it('isExternalSkill / EXTERNAL_SKILL_PREFIX classify external skills', () => {
    expect(EXTERNAL_SKILL_PREFIX).toBe('ext-skill:')
    expect(isExternalSkill({ Id: 'ext-skill:claude:lint' } as never)).toBe(true)
    expect(isExternalSkill({ Id: 'my-skill' } as never)).toBe(false)
  })
})

describe('resolveSkillOverrides', () => {
  function skill(Id: string, Source = 'user'): Skill {
    return { Id, Source } as Skill
  }

  it('keeps an external skill with no same-name competitor', () => {
    const result = resolveSkillOverrides([
      skill('review', 'user'),
      skill('ext-skill:claude:lint', 'claude'),
    ])
    expect(result.map(s => s.Id)).toEqual(['review', 'ext-skill:claude:lint'])
  })

  it('drops an external skill overridden by a same-name project-internal skill', () => {
    const result = resolveSkillOverrides([
      skill('ext-skill:claude:lint', 'claude'),
      skill('lint', 'user'),
    ])
    expect(result.map(s => s.Id)).toEqual(['lint'])
  })

  it('drops an external skill overridden by a same-name system (builtin) skill', () => {
    const result = resolveSkillOverrides([
      skill('ext-skill:claude:plan', 'claude'),
      skill('plan', 'builtin'),
    ])
    expect(result.map(s => s.Id)).toEqual(['plan'])
    expect(result[0]!.Source).toBe('builtin')
  })

  it('prefers a project-internal skill over a same-name system builtin', () => {
    const result = resolveSkillOverrides([
      skill('plan', 'builtin'),
      skill('plan', 'user'),
    ])
    expect(result.map(s => s.Id)).toEqual(['plan'])
    expect(result[0]!.Source).toBe('user')
  })

  it('resolves all three layers at once: project-internal > system > external', () => {
    const result = resolveSkillOverrides([
      skill('ext-skill:claude:lint', 'claude'),
      skill('lint', 'builtin'),
      skill('lint', 'user'),
    ])
    expect(result.map(s => s.Id)).toEqual(['lint'])
    expect(result[0]!.Source).toBe('user')
  })

  it('falls back to system builtin when project-internal is absent', () => {
    const result = resolveSkillOverrides([
      skill('ext-skill:claude:lint', 'claude'),
      skill('lint', 'builtin'),
    ])
    expect(result.map(s => s.Id)).toEqual(['lint'])
    expect(result[0]!.Source).toBe('builtin')
  })

  it('keeps all three layers when their names are distinct', () => {
    const result = resolveSkillOverrides([
      skill('alpha', 'user'),
      skill('beta', 'builtin'),
      skill('ext-skill:claude:gamma', 'claude'),
    ])
    expect(result.map(s => s.Id)).toEqual(['alpha', 'beta', 'ext-skill:claude:gamma'])
  })

  it('keeps only one entry when two external sources share a name (first-seen wins)', () => {
    const result = resolveSkillOverrides([
      skill('ext-skill:claude:lint', 'claude'),
      skill('ext-skill:codex:lint', 'codex'),
    ])
    expect(result.map(s => s.Id)).toEqual(['ext-skill:claude:lint'])
  })

  it('keeps all entries with distinct names and preserves original order', () => {
    const result = resolveSkillOverrides([
      skill('a', 'user'),
      skill('ext-skill:claude:b', 'claude'),
      skill('c', 'builtin'),
    ])
    expect(result.map(s => s.Id)).toEqual(['a', 'ext-skill:claude:b', 'c'])
  })
})
