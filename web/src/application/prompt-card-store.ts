import { client } from './generated-client'
import * as wikiClient from '../gen-clients/workspace/client'
import type { PromptArtifact, PromptFragment, PromptProfile, PromptFragmentListResp, PromptProfileListResp } from '../gen-types/prompt'
import type { GosporeClient, InvokeOptions } from '@qomos/gospore-client'
import type { WikiListCardsResp, MonoCardListItem as WireCardListItem } from '../gen-clients/system/types'
import { formatMonoCard, parseMonoCard, type MonoCard, type MonoCardListItem } from '../domain/mono-types'

type PromptStoreApi = Pick<typeof wikiClient, 'wikiListCards' | 'wikiGetCard' | 'wikiCreateCard' | 'wikiEditCard' | 'wikiDeleteCard'>

type FragmentRequest = { Scope?: string; Role?: string; Key?: string; Name: string; Kind: string; Priority: number; Content?: string; }
type ProfileRequest = { Scope?: string; Role?: string; Key?: string; BuiltinKey?: string; RolePrompt: string }

function data(card: MonoCardListItem): Record<string, unknown> {
  return card.data ?? {}
}

function sourceState(card: MonoCardListItem): Pick<PromptFragment, 'Source' | 'Editable' | 'Deletable'> {
  const source = card.source || (typeof data(card).source === 'string' ? data(card).source as string : undefined)
  return { Source: source, Editable: card.editable !== false, Deletable: card.deletable !== false }
}

function firstBodyLine(body: string | undefined): string {
  if (!body) return ''
  const line = body.split('\n').find(l => l.trim() && !l.trim().startsWith('#'))
  return (line || '').trim().slice(0, 160)
}

function fragmentFromCard(card: MonoCardListItem): PromptFragment {
  const meta = data(card)
  const state = sourceState(card)
  const body = parseMonoCard(card.id, card.raw || '')?.body || ''
  const visual = (meta.visual && typeof meta.visual === 'object' && !Array.isArray(meta.visual)) ? meta.visual as Record<string, unknown> : {}
  const description = typeof meta.description === 'string' ? meta.description : firstBodyLine(body)
  return {
    Id: card.id,
    Key: typeof meta.promptKey === 'string' ? meta.promptKey : card.id,
    BuiltinKey: typeof meta.promptKey === 'string' ? meta.promptKey.replace(/^builtin\//, '') : undefined,
    Name: card.id,
    Kind: typeof meta.kind === 'string' ? meta.kind : 'instructions',
    Priority: typeof meta.priority === 'number' ? meta.priority : 0,
    Scope: typeof meta.scope === 'string' ? meta.scope : undefined,
    Role: typeof meta.role === 'string' ? meta.role : undefined,
    Content: body,
    Path: card.id,
    ...state,
    // Extra metadata consumed by card-grid UIs (not part of the generated type).
    CardId: card.id,
    Icon: typeof visual.icon === 'string' ? visual.icon : undefined,
    Description: description,
    Tags: card.tags,
  } as PromptFragment
}

function profileFromCard(card: MonoCardListItem): PromptProfile {
  const meta = data(card)
  const state = sourceState(card)
  const body = parseMonoCard(card.id, card.raw || '')?.body || ''
  const visual = (meta.visual && typeof meta.visual === 'object' && !Array.isArray(meta.visual)) ? meta.visual as Record<string, unknown> : {}
  const description = typeof meta.description === 'string' ? meta.description : firstBodyLine(body)
  return {
    Key: typeof meta.promptKey === 'string' ? meta.promptKey : card.id.replace(/^prompt:profile:/, ''),
    BuiltinKey: typeof meta.promptKey === 'string' ? meta.promptKey.replace(/^builtin\//, '') : undefined,
    Scope: typeof meta.scope === 'string' ? meta.scope : undefined,
    Role: typeof meta.role === 'string' ? meta.role : undefined,
    RolePrompt: body,
    ...state,
    UpdatedAt: card.modified,
    // Extra metadata consumed by card-grid UIs (not part of the generated type).
    CardId: card.id,
    Icon: typeof visual.icon === 'string' ? visual.icon : undefined,
    Description: description,
    Tags: card.tags,
  } as PromptProfile
}

function isPrompt(card: MonoCardListItem): boolean {
  const tags = new Set(card.tags.map(tag => tag.toLowerCase()))
  const kind = typeof data(card).componentKind === 'string' ? data(card).componentKind : card.type
  return kind === 'prompt' || tags.has('prompt')
}

function isProfile(card: MonoCardListItem): boolean {
  const tags = new Set(card.tags.map(tag => tag.toLowerCase()))
  return tags.has('profile') || tags.has('prompt/profile')
}

function cardId(key: string, profile: boolean): string {
  return profile ? `prompt:profile:${key.replace(/[^a-zA-Z0-9._-]+/g, '-')}` : `prompt:fragment:${key.replace(/^builtin\//, '').replace(/[^a-zA-Z0-9._/-]+/g, '-')}`
}

export class PromptCardStore {
  constructor(private readonly api: PromptStoreApi = wikiClient) {}

  private async cards(): Promise<MonoCardListItem[]> {
    const response: WikiListCardsResp = await this.api.wikiListCards(client, { Flat: true, IncludeRaw: true, Limit: -1 })
    const listed = (response.Cards ?? []).map((card: WireCardListItem) => ({
      id: card.Id,
      type: card.Type || 'wiki',
      source: card.Source || 'project',
      storage: card.Storage || 'cardstore',
      visibility: card.Visibility || 'wiki',
      protected: card.Protected === true,
      editable: card.Editable !== false,
      deletable: card.Deletable !== false,
      tags: card.Tags ?? [],
      list: card.List ?? [],
      modified: card.Modified,
      raw: card.Raw,
      data: card.Data,
    })).filter(isPrompt)
    return Promise.all(listed.map(async card => {
      const response = await this.api.wikiGetCard(client, { Id: card.id })
      const parsed = parseMonoCard(card.id, response.Raw)
      return parsed ? { ...card, ...parsed, source: card.source, type: card.type, storage: card.storage, visibility: card.visibility, protected: card.protected, editable: card.editable, deletable: card.deletable } : card
    }))
  }

  async listFragments(_client: GosporeClient, req: { Scope: string; Role: string }, _opts?: InvokeOptions): Promise<PromptFragmentListResp> {
    const cards = await this.cards()
    return { Items: cards.filter(card => !isProfile(card) && (!req.Scope || data(card).scope === req.Scope) && (!req.Role || data(card).role === req.Role)).map(fragmentFromCard) }
  }

  async listProfiles(_client: GosporeClient, _opts?: InvokeOptions): Promise<PromptProfileListResp> {
    return { Items: (await this.cards()).filter(isProfile).map(profileFromCard) }
  }

  async getProfile(_client: GosporeClient, req: { Scope: string; Role: string }, _opts?: InvokeOptions): Promise<PromptProfile> {
    const card = (await this.cards()).find(card => isProfile(card) && data(card).scope === req.Scope && data(card).role === req.Role)
    if (!card) throw new Error(`Profile ${req.Scope}.${req.Role} not found`)
    return profileFromCard(card)
  }

  async saveProfile(_client: GosporeClient, req: ProfileRequest, _opts?: InvokeOptions): Promise<PromptProfile> {
    const key = req.Key || `${req.Scope || 'project'}.${req.Role || 'profile'}`
    return profileFromCard(await this.save(key, req.RolePrompt, { componentKind: 'prompt', source: 'user', scope: req.Scope, role: req.Role, promptKey: key }, true, req.BuiltinKey))
  }

  async saveFragment(_client: GosporeClient, req: FragmentRequest, _opts?: InvokeOptions): Promise<PromptFragment> {
    const key = req.Key || req.Name
    return fragmentFromCard(await this.save(key, req.Content || '', { componentKind: 'prompt', source: 'user', scope: req.Scope, role: req.Role, kind: req.Kind, priority: req.Priority, promptKey: key }, false))
  }

  async deleteProfile(_client: GosporeClient, req: { Key: string }, _opts?: InvokeOptions): Promise<void> { await this.remove(req.Key, true) }
  async deleteFragment(_client: GosporeClient, req: { Key: string }, _opts?: InvokeOptions): Promise<void> { await this.remove(req.Key, false) }

  async getArtifact(_client: GosporeClient, req: { Intent: string; Scope: string; Role: string }, _opts?: InvokeOptions): Promise<PromptArtifact> {
    const fragments = (await this.listFragments(client, { Scope: req.Scope, Role: req.Role })).Items
    return { Sections: fragments.map(fragment => fragment.Content || ''), Fragments: fragments.map(fragment => ({ ...fragment, Id: fragment.Id || fragment.Key || '' })) }
  }

  private async save(key: string, body: string, cardData: Record<string, unknown>, profile: boolean, builtinKey?: string): Promise<MonoCardListItem> {
    const id = cardId(key, profile)
    const now = new Date().toISOString()
    let existing: MonoCard | null = null
    try {
      const response = await this.api.wikiGetCard(client, { Id: id })
      existing = parseMonoCard(id, response.Raw)
    } catch { /* create below */ }
    const card: MonoCard = { id: profile ? key : String(cardData.promptKey || key), tags: ['component', 'prompt', profile ? 'profile' : 'fragment'], list: [], created: existing?.created || now, modified: now, body, data: { ...existing?.data, ...cardData, ...(builtinKey ? { builtinKey } : {}) }, raw: '' }
    card.raw = formatMonoCard(card)
    const response = existing
      ? await this.api.wikiEditCard(client, { Id: id, Raw: card.raw })
      : await this.api.wikiCreateCard(client, { Id: id, Raw: card.raw })
    const savedCard = response.Card
    return { id: savedCard.Id, tags: savedCard.Tags ?? [], list: savedCard.List ?? [], modified: savedCard.Modified, raw: savedCard.Raw, data: savedCard.Data }
  }

  private async remove(key: string, profile: boolean): Promise<void> {
    await this.api.wikiDeleteCard(client, { Id: cardId(key, profile) })
  }
}

export const promptCardStore = new PromptCardStore()

export const listFragments = (client: GosporeClient, req: { Scope: string; Role: string }, opts?: InvokeOptions) => promptCardStore.listFragments(client, req, opts)
export const listProfiles = (client: GosporeClient, opts?: InvokeOptions) => promptCardStore.listProfiles(client, opts)
export const getProfile = (client: GosporeClient, req: { Scope: string; Role: string }, opts?: InvokeOptions) => promptCardStore.getProfile(client, req, opts)
export const saveProfile = (client: GosporeClient, req: ProfileRequest, opts?: InvokeOptions) => promptCardStore.saveProfile(client, req, opts)
export const saveFragment = (client: GosporeClient, req: FragmentRequest, opts?: InvokeOptions) => promptCardStore.saveFragment(client, req, opts)
export const deleteProfile = (client: GosporeClient, req: { Key: string }, opts?: InvokeOptions) => promptCardStore.deleteProfile(client, req, opts)
export const deleteFragment = (client: GosporeClient, req: { Key: string }, opts?: InvokeOptions) => promptCardStore.deleteFragment(client, req, opts)
export const getArtifact = (client: GosporeClient, req: { Intent: string; Scope: string; Role: string }, opts?: InvokeOptions) => promptCardStore.getArtifact(client, req, opts)
