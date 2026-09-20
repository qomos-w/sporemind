import { describe, expect, it, vi, beforeEach } from 'vitest'
import type { WikiListCardsResp } from '../gen-types/project.wiki.part1'
import type { WikiGetCardResp } from '../gen-types/project.wiki.part1'

vi.mock('./generated-client', () => ({ client: {} }))
vi.mock('../gen-clients/workspace/client', () => ({
  wikiListCards: vi.fn(),
  wikiGetCard: vi.fn(),
  wikiCreateCard: vi.fn(),
  wikiEditCard: vi.fn(),
  wikiDeleteCard: vi.fn(),
}))

import * as wiki from '../gen-clients/workspace/client'
import { PromptCardStore } from './prompt-card-store'

const dummyClient = {} as never

function listCard(overrides: Partial<{ Id: string; Tags: string[]; Type: string; Data: Record<string, unknown>; Raw: string; Source: string; Modified: string }> = {}) {
  return {
    Id: overrides.Id ?? 'prompt:fragment:test',
    Tags: overrides.Tags ?? ['component', 'prompt', 'fragment'],
    List: [],
    Modified: overrides.Modified ?? '2026-01-01T00:00:00Z',
    Type: overrides.Type ?? 'wiki',
    Source: overrides.Source ?? 'project',
    Storage: 'cardstore',
    Visibility: 'wiki',
    Protected: false,
    Editable: true,
    Deletable: true,
    Created: '2026-01-01T00:00:00Z',
    Data: overrides.Data ?? { componentKind: 'prompt', kind: 'instructions', scope: 'project', role: 'coder', promptKey: 'test' },
    Raw: overrides.Raw ?? '',
  }
}

function fullCardRaw(id: string, body: string, data?: Record<string, unknown>): string {
  const dataBlock = data ? `\ndata:\n${Object.entries(data).map(([k, v]) => `  ${k}: ${JSON.stringify(v)}`).join('\n')}\n` : ''
  return `---\nid: ${id}\ntype: prompt\ntags: [component, prompt, fragment]\n${dataBlock}---\n${body}`
}

describe('PromptCardStore', () => {
  let store: PromptCardStore

  beforeEach(() => {
    vi.clearAllMocks()
    store = new PromptCardStore(wiki as any)
  })

  it('passes IncludeRaw:true to wikiListCards so fragmentFromCard can parse Content from list-level raw', async () => {
    const raw = fullCardRaw('prompt:fragment:test', 'This is the fragment body.')
    ;(wiki.wikiListCards as any).mockResolvedValueOnce({
      Cards: [listCard({ Id: 'prompt:fragment:test', Raw: raw })],
      Tree: '',
      Nodes: [],
      Total: 1,
    } as WikiListCardsResp)
    // cards() also fetches each card via wikiGetCard — provide the same raw.
    ;(wiki.wikiGetCard as any).mockResolvedValue({ Raw: raw } as WikiGetCardResp)

    const resp = await store.listFragments(dummyClient, { Scope: 'project', Role: 'coder' })

    expect(wiki.wikiListCards).toHaveBeenCalledWith(expect.anything(), { Flat: true, IncludeRaw: true, Limit: -1 })
    expect(resp.Items).toHaveLength(1)
    expect(resp.Items?.[0]?.Content).toBe('This is the fragment body.')
  })

  it('fragmentFromCard Content is non-empty when raw is available', async () => {
    const raw = fullCardRaw('prompt:fragment:coder-instructions', 'You are a code reviewer.\nReview every change carefully.')
    ;(wiki.wikiListCards as any).mockResolvedValueOnce({
      Cards: [listCard({ Id: 'prompt:fragment:coder-instructions', Raw: raw })],
      Tree: '',
      Nodes: [],
      Total: 1,
    } as WikiListCardsResp)
    ;(wiki.wikiGetCard as any).mockResolvedValue({ Raw: raw } as WikiGetCardResp)

    const resp = await store.listFragments(dummyClient, { Scope: 'project', Role: 'coder' })

    expect(resp.Items?.[0]?.Content).toBe('You are a code reviewer.\nReview every change carefully.')
    expect(resp.Items?.[0]?.Content).not.toBe('')
  })

  it('profileFromCard RolePrompt is non-empty when raw is available', async () => {
    const raw = `---\nid: prompt:profile:coder\ntype: prompt\ntags: [component, prompt, profile]\ndata:\n  componentKind: prompt\n  scope: project\n  role: coder\n  promptKey: coder\n---\nYou are an expert coder profile.`
    ;(wiki.wikiListCards as any).mockResolvedValueOnce({
      Cards: [listCard({
        Id: 'prompt:profile:coder',
        Tags: ['component', 'prompt', 'profile'],
        Raw: raw,
        Data: { componentKind: 'prompt', scope: 'project', role: 'coder', promptKey: 'coder' },
      })],
      Tree: '',
      Nodes: [],
      Total: 1,
    } as WikiListCardsResp)
    ;(wiki.wikiGetCard as any).mockResolvedValue({ Raw: raw } as WikiGetCardResp)

    const resp = await store.listProfiles(dummyClient)

    expect(resp.Items).toHaveLength(1)
    expect(resp.Items?.[0]?.RolePrompt).toBe('You are an expert coder profile.')
    expect(resp.Items?.[0]?.RolePrompt).not.toBe('')
  })
})