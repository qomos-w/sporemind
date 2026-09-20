import { client } from '../../application/generated-client'
import * as wikiClient from '../../gen-clients/project/client'
import * as workspaceWikiClient from '../../gen-clients/workspace/client'
import type { WikiValidateCardResp, TemplateRunRecord, ModelSlot } from '../../gen-clients/system/types'
import type { ProjectGraphEnvelopeResp } from '../../gen-clients/system/types'
import { OnCardChanged, OnFileChanged, OnGraphChanged } from '../../gen-clients/project/client'
import { watchProjection } from '../../gen-clients/projections'
import {
  parseMonoCard,
  formatMonoCard,
  validateMonoCard,
  cardIdFromTitle,
  toListItem,
  type MonoCard,
  type MonoCardListItem,
  type ReminderPriority,
} from '../../domain/mono-types'
import { normalizeTaskStatus, resolveStatusAliases } from '../../domain/task-status'
import { workflowFoldStore, buildWorkflowFilter } from '../ai/components/workflowFoldStore'
import { getAgentListSnapshot, prefetchAgentList } from '../ai/hooks/agentListStore'

/** Card IDs that live in the workspace (system meta-project) store, not the
 *  project store. These are seeded by the workspace actor and surfaced in
 *  model settings (skills, prompt profiles/fragments, builtin components).
 *  Agent and plugin cards are external providers on the project store, so
 *  they are excluded. */
function isWorkspaceCardId(id: string): boolean {
  return id.startsWith('skill:') || id.startsWith('prompt:') || id.startsWith('builtin:')
}

export interface MonoStoreState {
  projectId: string | null
  cards: MonoCardListItem[]
  openCards: string[]
  loading: boolean
  error: string | null
  cardRefreshKey: Record<string, number>
  /** Invalidation counter per graph cache key (`${graphKind}/${id}`), bumped
   *  on every graph_changed event so graph-consuming panels re-pull. */
  graphRefreshKey: Record<string, number>
}

type Listener = (state: MonoStoreState) => void

const initialState: MonoStoreState = {
  projectId: null,
  cards: [],
  openCards: [],
  loading: false,
  error: null,
  cardRefreshKey: {},
  graphRefreshKey: {},
}

/** Cache key for a project graph — mirrors the backend's graphKey
 *  (`pkg/actor/project/project.go graphKey`) so frontend invalidation
 *  addresses exactly the graph the graph_changed event names. */
export function graphCacheKey(graphKind: string, id: string): string {
  return `${graphKind}/${id}`
}

class MonoStore {
  private state: MonoStoreState = { ...initialState }
  private listeners: Set<Listener> = new Set()
  private currentProjectId: string | null = null
  private cardCache: Map<string, MonoCard> = new Map()
  private graphCache: Map<string, ProjectGraphEnvelopeResp> = new Map()
  private workspaceCardIds: Set<string> = new Set()
  private cardChangeBuffer = new Map<string, 'upsert' | 'delete'>()
  private cardFlushTimer: ReturnType<typeof setTimeout> | null = null
  private readonly CARD_BATCH_WINDOW_MS = 75
  /** File-watcher storms (atomic saves fan out write/rename/remove events)
   *  refill the batch window continuously, so flushes can fire every 75ms for
   *  seconds. An open MonoCardPanel refetches and re-parses on every
   *  cardRefreshKey bump — throttle bumps per card, with a trailing bump so
   *  the final state always lands. */
  private readonly CARD_REFRESH_MIN_INTERVAL_MS = 500
  private lastRefreshBumpAt = new Map<string, number>()
  private trailingBumpTimers = new Map<string, ReturnType<typeof setTimeout>>()
  private lastFlushLogAt = 0

  private setState(partial: Partial<MonoStoreState>) {
    this.state = { ...this.state, ...partial }
    this.emit()
  }

  private emit() {
    for (const listener of this.listeners) {
      listener(this.state)
    }
  }

  private target() {
    return this.currentProjectId ? { target: this.currentProjectId } : undefined
  }

  /** True when an explicit-project operation targets a project other than the
   *  singleton store's current project. Foreign operations must not touch the
   *  singleton caches or card list — those are keyed to the current project. */
  private isForeignProject(projectId?: string | null): boolean {
    return !!projectId && !!this.currentProjectId && projectId !== this.currentProjectId
  }

  private isWorkspaceCard(title: string): boolean {
    return isWorkspaceCardId(title) || this.workspaceCardIds.has(title)
  }

  private normalizeStatus(status?: string): string | undefined {
    if (!status) return status
    const aliases = resolveStatusAliases(this.state.cards)
    return normalizeTaskStatus(status, aliases)
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener)
    listener(this.state)
    return () => { this.listeners.delete(listener) }
  }

  getState(): MonoStoreState {
    return this.state
  }

  setProjectId(id: string | null) {
    if (this.currentProjectId === id) return
    if (this.cardFlushTimer) {
      clearTimeout(this.cardFlushTimer)
      this.cardFlushTimer = null
    }
    for (const timer of this.trailingBumpTimers.values()) clearTimeout(timer)
    this.trailingBumpTimers.clear()
    this.lastRefreshBumpAt.clear()
    this.cardChangeBuffer.clear()
    this.currentProjectId = id
    this.cardCache.clear()
    this.graphCache.clear()
    this.workspaceCardIds.clear()
    this.setState({ ...initialState, projectId: id })
  }

  async load() {
    if (!this.currentProjectId) return
    this.setState({ loading: true, error: null })
    try {
      // Fold-driven server-side filtering (plan C): wait for the workflow
      // fold preferences so hidden maps/tasks never ship in the first list;
      // fold changes trigger a reload via watchFoldChanges. When prefs fail
      // to load, buildWorkflowFilter still returns an all-expanded filter.
      //
      // In-progress classification keys on live agent ids; the agent-list
      // fetch runs in parallel with the fold prefs so the two independent
      // network round-trips don't serialize (each can take up to 6-10s on a
      // cold WebSocket, so serial await made the card list stall for 16s).
      await Promise.all([
        workflowFoldStore.ensureLoaded(),
        prefetchAgentList().catch(() => {}),
      ])
      const agentIds = new Set(getAgentListSnapshot().items.map(a => a.ActorId))
      const workflowFilter = buildWorkflowFilter(workflowFoldStore.getSnapshot(), agentIds)
      // Lightweight list mode: request metadata-only (IncludeRaw=false) so the
      // topology panel and card lists don't pay the cost of serializing every
      // card body. Consumers that need raw (detail panels, source editor) use
      // getCard() which fetches the full card on demand with caching.
      // IncludeBuiltin: the topology graph and sidebar derive type-driven
      // mount specs from the __builtin_*__ virtual node rows, so this caller
      // must keep receiving them even though the server strips them by default.
      // Limit: -1 = unlimited (former list_all_cards contract): the graph must
      // render every card, not the 50 most recently modified.
      const resp = await wikiClient.wikiListCards(client, { Flat: true, IncludeRaw: false, IncludeBuiltin: true, Limit: -1, ...(workflowFilter ? { WorkflowFilter: workflowFilter } : {}) }, this.target())
      const cards = resp.Cards.map(mapServerListItem)
      this.setState({ cards, loading: false })
    } catch (err) {
      this.setState({ loading: false, error: err instanceof Error ? err.message : String(err) })
    }
  }

  /** Reload the card list whenever the workflow fold preferences change:
   *  the server filters hidden maps/task cards by fold visibility, so a fold
   *  toggle must re-fetch the list (expanding pulls the newly visible cards,
   *  folding drops them from memory). */
  watchFoldChanges(signal: AbortSignal): () => void {
    const cancel = workflowFoldStore.subscribe(() => {
      if (signal.aborted || !this.currentProjectId) return
      void this.load()
    })
    signal.addEventListener('abort', cancel, { once: true })
    return cancel
  }

  async fetchCardList(): Promise<MonoCardListItem[]> {
    await this.load()
    return this.state.cards
  }

  async loadOpenCards(): Promise<string[]> {
    if (!this.currentProjectId) return []
    try {
      const resp = await wikiClient.wikiGetOpenCards(client, {}, this.target())
      const openCards = sanitizeOpenCards(resp.OpenCards ?? [])
      this.setState({ openCards })
      return openCards
    } catch (err) {
      // Ignore: open-cards state may not exist yet
      return []
    }
  }

  async watchOpenCards(projectId: string, signal: AbortSignal): Promise<void> {
    try {
      for await (const openCards of watchProjection<string[]>(client, {
        actorPath: projectId,
        component: 'OpenCards',
        schemaId: 0,
      })) {
        if (signal.aborted) return
        this.setState({ openCards: sanitizeOpenCards(openCards ?? []) })
      }
    } catch (err) {
      if (!signal.aborted) {
        // eslint-disable-next-line no-console
        console.warn('[mono-store] watchOpenCards stream ended:', err)
      }
    }
  }

  /** Resolve the card id from a file-path slug by matching against
   *  known cards. Falls back to the raw slug when no match is found. */
  private idFromSlug(slug: string): string {
    const match = this.state.cards.find(c => c.id === slug)
    return match ? match.id : slug
  }

  /** Idempotently upsert a card list item: replace any existing entry with the
   *  same id, or prepend if new. The two independent watchers (card-changed and
   *  file-changed) can both fire for the same write and race their `await`s, so
   *  this always dedups by id against the current list — it never produces a
   *  list with two equal ids, which would crash the topology vis DataSet. */
  private upsertListItem(listItem: MonoCardListItem, list: MonoCardListItem[] = this.state.cards): MonoCardListItem[] {
    const next: MonoCardListItem[] = []
    let replaced = false
    for (const c of list) {
      if (c.id === listItem.id) {
        if (!replaced) { next.push(listItem); replaced = true }
      } else {
        next.push(c)
      }
    }
    return replaced ? next : [listItem, ...list]
  }

  /** Buffer card-changed and file-changed events in a short window so rapid
   *  edits (e.g. both watchers firing for one save, or a burst of renames)
   *  are deduped by id and fetched in a single Promise.all batch, producing
   *  one setState update instead of many. */
  private scheduleCardFlush() {
    if (this.cardFlushTimer) return
    this.cardFlushTimer = setTimeout(() => {
      this.cardFlushTimer = null
      void this.flushCardChanges()
    }, this.CARD_BATCH_WINDOW_MS)
  }

  private queueCardChange(slug: string, kind: 'upsert' | 'delete') {
    this.cardChangeBuffer.set(slug, kind)
    this.scheduleCardFlush()
  }

  /** Bump a card's refresh key immediately when due, else schedule a single
   *  trailing bump at the end of the throttle window. Explicit user saves
   *  bypass this and bump inline. */
  private bumpCardRefresh(cardRefreshKey: Record<string, number>, id: string): Record<string, number> {
    const now = Date.now()
    const last = this.lastRefreshBumpAt.get(id) ?? 0
    if (now - last >= this.CARD_REFRESH_MIN_INTERVAL_MS) {
      this.lastRefreshBumpAt.set(id, now)
      return { ...cardRefreshKey, [id]: (cardRefreshKey[id] ?? 0) + 1 }
    }
    if (!this.trailingBumpTimers.has(id)) {
      const delay = this.CARD_REFRESH_MIN_INTERVAL_MS - (now - last)
      const timer = setTimeout(() => {
        this.trailingBumpTimers.delete(id)
        const key = { ...this.state.cardRefreshKey }
        key[id] = (key[id] ?? 0) + 1
        this.lastRefreshBumpAt.set(id, Date.now())
        this.setState({ cardRefreshKey: key })
      }, delay)
      this.trailingBumpTimers.set(id, timer)
    }
    return cardRefreshKey
  }

  private async flushCardChanges() {
    const batch = new Map(this.cardChangeBuffer)
    this.cardChangeBuffer.clear()

    // Normalize slugs to resolved ids, deduplicate.
    const ids = new Set<string>()
    for (const slug of batch.keys()) {
      ids.add(this.idFromSlug(slug))
    }

    if (ids.size === 0) return

    const now = Date.now()
    if (now - this.lastFlushLogAt > 200) {
      this.lastFlushLogAt = now
      const sample = [...ids].slice(0, 3).join('|').slice(0, 120)
      console.info(`[jitter-diag] card-flush ids=${ids.size} sample=${sample}`)
    }

    // Events carry fresh state — never serve a stale cache entry for an id in
    // the batch.
    for (const id of ids) {
      this.cardCache.delete(id)
    }

    let nextCards = this.state.cards
    let cardRefreshKey = { ...this.state.cardRefreshKey }

    // Event kinds are advisory, not authoritative: an atomic save writes via
    // temp-file + rename, which the file watcher surfaces as remove/rename for
    // a card that still exists. Blindly dropping on 'delete' made actively
    // written workflow map cards vanish from the list until a later event
    // re-added them. Verify against the store instead: fetch every id
    // concurrently, keep the ones that still resolve, drop only real misses.
    const cards = await Promise.all([...ids].map(id => this.getCard(id)))
    for (const [i, id] of [...ids].entries()) {
      const card = cards[i]
      if (card) {
        nextCards = this.upsertListItem(toListItem(card), nextCards)
      } else {
        this.cardCache.delete(id)
        nextCards = nextCards.filter(c => c.id !== id)
      }
      cardRefreshKey = this.bumpCardRefresh(cardRefreshKey, id)
    }

    this.setState({ cards: nextCards, cardRefreshKey })
  }

  watchCardChanges(projectId: string, signal: AbortSignal): () => void {
    const cancel = OnCardChanged(client, projectId, (payload) => {
      if (signal.aborted) return
      const slug = payload.Id
      if (!slug) return
      this.queueCardChange(slug, 'upsert')
    })
    signal.addEventListener('abort', () => {
      if (this.cardFlushTimer) {
        clearTimeout(this.cardFlushTimer)
        this.cardFlushTimer = null
      }
      this.cardChangeBuffer.clear()
      cancel()
    }, { once: true })
    return cancel
  }

  watchFileChanges(projectId: string, signal: AbortSignal): () => void {
    const cancel = OnFileChanged(client, projectId, (payload) => {
      if (signal.aborted) return
      const match = payload.Path.match(/^\.sporecode\/wiki\/(.+)\.md$/)
      if (!match) return
      const slug = match[1]
      if (!slug) return
      if (payload.Kind === 'remove' || payload.Kind === 'rename') {
        this.queueCardChange(slug, 'delete')
        return
      }
      this.queueCardChange(slug, 'upsert')
    })
    signal.addEventListener('abort', () => {
      if (this.cardFlushTimer) {
        clearTimeout(this.cardFlushTimer)
        this.cardFlushTimer = null
      }
      this.cardChangeBuffer.clear()
      cancel()
    }, { once: true })
    return cancel
  }

  /** Fetch a project graph snapshot (cache-first). Graphs are keyed
   *  `${graphKind}/${id}` like the backend. Returns null when no project is
   *  active or the graph does not exist; errors degrade to null instead of
   *  throwing so panels can fall back to card-derived data. */
  async getGraph(graphKind: string, id: string): Promise<ProjectGraphEnvelopeResp | null> {
    if (!this.currentProjectId) return null
    const key = graphCacheKey(graphKind, id)
    const cached = this.graphCache.get(key)
    if (cached) return cached
    try {
      const resp = await wikiClient.graphGet(
        client,
        { ProjectId: this.currentProjectId, GraphKind: graphKind, Id: id },
        this.target(),
      )
      this.graphCache.set(key, resp)
      return resp
    } catch {
      return null
    }
  }

  /** Invalidate a graph's cached snapshot and re-pull it if it was in use.
   *  Always bumps graphRefreshKey[key] so graph-consuming panels re-render and
   *  re-read even when the graph was not cached locally. If the event revision
   *  matches the cached revision, the cache is preserved and no re-pull happens. */
  async refreshGraph(graphKind: string, id: string, eventRevision?: string): Promise<void> {
    const key = graphCacheKey(graphKind, id)
    const cached = this.graphCache.get(key)

    // Same revision as the cached snapshot: just bump the refresh key so panels
    // re-check, without deleting the cache or paying for a full re-pull. When the
    // event revision is missing or empty, fall back to the old full-refresh path.
    if (cached && eventRevision !== undefined && eventRevision !== '' && cached.Meta?.Revision === eventRevision) {
      this.setState({
        graphRefreshKey: { ...this.state.graphRefreshKey, [key]: (this.state.graphRefreshKey[key] ?? 0) + 1 },
      })
      return
    }

    const wasCached = this.graphCache.delete(key)
    this.setState({
      graphRefreshKey: { ...this.state.graphRefreshKey, [key]: (this.state.graphRefreshKey[key] ?? 0) + 1 },
    })
    if (wasCached) {
      // Re-warm the cache immediately so the next panel read is fresh, not a
      // cache-miss round-trip.
      await this.getGraph(graphKind, id)
    }
  }

  /** Subscribe to the project actor's graph_changed events (emitted by
   *  project.graph_save, including from other clients/machines). Each event
   *  invalidates the named graph's local cache and re-pulls it — same pattern
   *  as watchCardChanges/watchFileChanges. */
  watchGraphChanges(projectId: string, signal: AbortSignal): () => void {
    const cancel = OnGraphChanged(client, projectId, (payload) => {
      if (signal.aborted) return
      if (!payload.GraphKind || !payload.Id) return
      void this.refreshGraph(payload.GraphKind, payload.Id, payload.Revision)
    })
    signal.addEventListener('abort', cancel, { once: true })
    return cancel
  }

  async saveOpenCards(openCards: string[]) {
    if (!this.currentProjectId) return
    const sanitized = sanitizeOpenCards(openCards)
    this.setState({ openCards: sanitized })
    try {
      await wikiClient.wikiSaveOpenCards(client, { OpenCards: sanitized }, this.target())
    } catch (err) {
      this.setState({ error: err instanceof Error ? err.message : String(err) })
    }
  }

  /** Fetch a card. Without opts the call follows the singleton store's current
   *  project with its cache; with an explicit projectId it pins the target
   *  (used by right-panel card tabs opened from another project) and bypasses
   *  the singleton cache. */
  async getCard(title: string, opts?: { projectId?: string | null }): Promise<MonoCard | null> {
    const projectId = opts?.projectId ?? this.currentProjectId
    if (!projectId) return null
    const foreign = this.isForeignProject(opts?.projectId)
    if (!foreign) {
      const cached = this.cardCache.get(title)
      if (cached) return cached
    }
    let card: MonoCard | null = null
    try {
      const resp = await wikiClient.wikiGetCard(client, { Id: title }, { target: projectId })
      card = parseMonoCard(title, resp.Raw)
    } catch {
      // Project store miss; try workspace below.
    }
    if (!card) {
      try {
        const wsResp = await workspaceWikiClient.wikiGetCard(client, { Id: title })
        card = parseMonoCard(title, wsResp.Raw)
        if (card) this.workspaceCardIds.add(title)
      } catch {
        // Not found in either store.
      }
    }
    if (card && !foreign) this.cardCache.set(title, card)
    return card
  }

  async createCard(input: {
    name: string
    body: string
    type?: string
    tags?: string[]
    list?: string[]
    due?: string
    priority?: ReminderPriority
    status?: string
    parent?: string
    standalone?: boolean
    data?: Record<string, unknown>
  }): Promise<MonoCard> {
    if (!this.currentProjectId) throw new Error('WikiStore: no project selected')
    const slug = this.uniqueSlug(input.name)
    const now = new Date().toISOString()
    const card: MonoCard = {
      id: input.name,
      type: input.type || 'wiki',
      tags: input.tags || [],
      list: input.list || [],
      created: now,
      modified: now,
      body: input.body,
      due: input.due,
      priority: input.priority,
      status: input.status,
      parent: input.parent,
      data: input.data,
      raw: '',
    }
    card.status = this.normalizeStatus(card.status)
    const raw = formatMonoCard(card)
    // Pass the title as the server's identifier (the server resolves it)
    const resp = await wikiClient.wikiCreateCard(client, { Id: slug, Raw: raw }, this.target())
    const listItem = mapServerListItem(resp.Card)
    this.setState({ cards: this.upsertListItem(listItem) })
    this.cardCache.set(card.id, { ...card, raw })
    return { ...card, raw }
  }

  async updateCard(title: string, updates: Partial<MonoCard>, opts?: { projectId?: string | null }): Promise<MonoCard | null> {
    const foreign = this.isForeignProject(opts?.projectId)
    const projectId = opts?.projectId ?? this.currentProjectId
    if (!projectId) return null
    let existing: MonoCard | null = foreign ? null : (this.cardCache.get(title) ?? null)
    if (!existing) {
      existing = await this.getCard(title, { projectId })
    }
    if (!existing) return null
    const card: MonoCard = { ...existing, ...updates, modified: new Date().toISOString() }
    if (!foreign) card.status = this.normalizeStatus(card.status)
    const raw = formatMonoCard(card)
    const resp = (!foreign && this.isWorkspaceCard(title))
      ? await workspaceWikiClient.wikiEditCard(client, { Id: title, Raw: raw })
      : await wikiClient.wikiEditCard(client, { Id: title, Raw: raw }, { target: projectId })
    if (foreign) return { ...card, raw }
    const listItem = mapServerListItem(resp.Card)
    this.setState({
      cards: this.upsertListItem(listItem),
      cardRefreshKey: { ...this.state.cardRefreshKey, [title]: (this.state.cardRefreshKey[title] ?? 0) + 1 },
    })
    this.cardCache.set(title, { ...card, raw })
    return { ...card, raw }
  }

  async updateCardRaw(title: string, raw: string, opts?: { projectId?: string | null }): Promise<MonoCard | null> {
    const foreign = this.isForeignProject(opts?.projectId)
    const projectId = opts?.projectId ?? this.currentProjectId
    if (!projectId) return null
    const resp = (!foreign && this.isWorkspaceCard(title))
      ? await workspaceWikiClient.wikiEditCard(client, { Id: title, Raw: raw })
      : await wikiClient.wikiEditCard(client, { Id: title, Raw: raw }, { target: projectId })
    if (foreign) return this.getCard(title, { projectId })
    const listItem = mapServerListItem(resp.Card)
    this.cardCache.delete(title)
    this.setState({
      cards: this.upsertListItem(listItem),
      cardRefreshKey: { ...this.state.cardRefreshKey, [title]: (this.state.cardRefreshKey[title] ?? 0) + 1 },
    })
    return this.getCard(title)
  }

  async validateCard(title: string, raw: string, opts?: { projectId?: string | null }): Promise<WikiValidateCardResp> {
    const projectId = opts?.projectId ?? this.currentProjectId
    if (!projectId) return { Valid: true, Errors: [] }
    // The workspace store has no wiki_validate_card callable; validate
    // system cards on the frontend instead.
    if (!this.isForeignProject(projectId) && this.isWorkspaceCard(title)) {
      const parsed = parseMonoCard(title, raw)
      if (!parsed) return { Valid: false, Errors: [{ Code: 'parse', Field: 'raw', Message: 'Invalid frontmatter' }] }
      const errors = validateMonoCard(parsed)
      return { Valid: errors.length === 0, Errors: errors.map(msg => ({ Code: 'frontend', Field: '', Message: msg })) }
    }
    return wikiClient.wikiValidateCard(client, { Id: title, Raw: raw }, { target: projectId })
  }

  async addCardTag(title: string, tag: string): Promise<MonoCard | null> {
    const existing = await this.getCard(title)
    if (!existing) return null
    const normalized = tag.trim()
    if (!normalized || existing.tags.includes(normalized)) return existing
    return this.updateCard(title, { tags: [...existing.tags, normalized] })
  }

  async triggerTimerCard(title: string): Promise<boolean> {
    if (!this.currentProjectId) return false
    await wikiClient.wikiTriggerTimerCard(client, { Id: title }, this.target())
    return true
  }

  /** List all workflow template map cards (data.template:true). Feeds the
   *  scheduler editor's Workflow Template selector dropdown. */
  async listTemplates(): Promise<MonoCardListItem[]> {
    if (!this.currentProjectId) return []
    const resp = await wikiClient.wikiListTemplates(client, {}, this.target())
    return (resp.Templates ?? []).map(mapServerListItem)
  }

  /** Snapshot a workflow map as a template and bind it to a scheduler card.
   *  The backend writes the template id into the scheduler card's data block
   *  (workflow_template field). Returns the bound template id, or null if no
   *  project is selected. */
  async automationBind(mapId: string, schedulerCardId: string): Promise<string | null> {
    if (!this.currentProjectId) return null
    const resp = await wikiClient.wikiAutomationBind(
      client,
      { MapId: mapId, SchedulerCardId: schedulerCardId },
      this.target(),
    )
    const scheduler = mapServerListItem(resp.SchedulerCard)
    this.cardCache.delete(schedulerCardId)
    this.setState({
      cards: this.upsertListItem(scheduler),
      cardRefreshKey: { ...this.state.cardRefreshKey, [schedulerCardId]: (this.state.cardRefreshKey[schedulerCardId] ?? 0) + 1 },
    })
    return resp.TemplateId
  }

  /** Instantiate a workflow template into a new runnable instance map.
   *  Generates a unique instance map id following the backend convention
   *  (inst::<UTC timestamp yyyyMMddHHmmss>::<templateMapId> from
   *  wiki_automation.go:25). On success, refreshes the card list and returns
   *  the new instance map card. */
  async instantiateWorkflow(templateMapId: string): Promise<MonoCardListItem | null> {
    if (!this.currentProjectId) return null
    const now = new Date()
    const ts = now.getUTCFullYear().toString() +
      String(now.getUTCMonth() + 1).padStart(2, '0') +
      String(now.getUTCDate()).padStart(2, '0') +
      String(now.getUTCHours()).padStart(2, '0') +
      String(now.getUTCMinutes()).padStart(2, '0') +
      String(now.getUTCSeconds()).padStart(2, '0')
    const instanceMapId = `inst::${ts}::${templateMapId}`
    const resp = await wikiClient.wikiTemplateInstantiate(
      client,
      {
        TemplateMapId: templateMapId,
        InstanceMapId: instanceMapId,
        Source: 'manual',
      },
      this.target(),
    )
    const instance = mapServerListItem(resp.InstanceMap)
    this.setState({ cards: this.upsertListItem(instance) })
    return instance
  }

  /** Schedule type options for createSchedulerCard. */
  createSchedulerCardOpts?: {
    /** Unified task (default) or an agent pause/resume action card. */
    type: 'task' | 'agent_action'
    /** agent_action: pause | resume. */
    agentAction?: string
    /** agent_action: agent ref to act on. */
    targetAgent?: string
    /** task: agent kind to create per fire (backend defaults to coder when
     *  empty). */
    agentKind?: string
    /** task: primary model slot for the created agent (JSON-encoded under the
     *  card data's model_slots.primary). */
    agentSlot?: ModelSlot
  }

  /** Create a scheduler card (template card right-click "New scheduled task"
   *  and the scheduled view's new-task menu). The id must start with
   *  "scheduler:" and carry data.schedule.cron — the backend's wikiCreateCard
   *  auto-registers it with the scheduler service (timer_sync.go), which makes
   *  it appear in the Scheduled view. Defaults to daily 09:00. */
  /** templateMapId = null creates a prompt-mode task (body is the prompt, no
   *  workflow_template); a tpl::<mapId> id creates a template-bound task (body
   *  may stay empty — the template flow supplies the work). Pass opts to pick
   *  the created agent's kind/model or to create an agent_action card; the
   *  latter is stored as a unified task carrying an `agent_actions` list. */
  async createSchedulerCard(templateMapId: string | null, opts?: this['createSchedulerCardOpts']): Promise<MonoCardListItem | null> {
    if (!this.currentProjectId) return null
    const isAgentAction = opts?.type === 'agent_action'
    const tplId = isAgentAction ? '' : String(templateMapId ?? '')
    const tplMapId = tplId.startsWith('tpl::') ? tplId.slice('tpl::'.length) : tplId
    const base = tplMapId || (isAgentAction ? 'agent-action' : 'prompt-task')
    const taken = (candidate: string) => this.state.cards.some(c => c.id === candidate)
    let id = `scheduler:${base}`
    let suffix = 2
    while (taken(id)) id = `scheduler:${base}-${suffix++}`
    const now = new Date().toISOString()
    let dataBlock = `data:\n  schedule:\n    cron: "0 9 * * *"\n    enabled: true\n  schedule_type: task\n`
    if (isAgentAction) {
      const action = opts?.agentAction === 'resume' ? 'resume' : 'pause'
      const target = opts?.targetAgent || 'agent:coder'
      dataBlock += `  agent_actions: '${JSON.stringify([{ action, target }])}'\n`
    } else {
      if (opts?.agentKind) dataBlock += `  agent_kind: ${opts.agentKind}\n`
      if (opts?.agentSlot) dataBlock += `  model_slots: '${JSON.stringify({ primary: opts.agentSlot })}'\n`
      if (tplId) dataBlock += `  workflow_template: "${tplId}"\n`
    }
    // Template-bound tasks run by the template flow — the prompt/body may stay
    // empty; prompt-mode tasks keep a non-empty placeholder (the prompt is
    // required when no template is bound). Agent-action cards carry a short
    // description instead of an executable prompt.
    const body = isAgentAction
      ? `Scheduled agent action: ${opts?.agentAction || 'pause'} → ${opts?.targetAgent || 'agent:coder'}.`
      : tplId
        ? ''
        : 'Describe what this scheduled task should do — this body is the prompt sent to the agent created for each fire.'
    const raw =
      `---\nid: ${id}\ntype: scheduler\ntags: [scheduler]\n` +
      `created: "${now}"\nmodified: "${now}"\n` +
      dataBlock +
      `---\n\n${body}`
    const resp = await wikiClient.wikiCreateCard(client, { Id: id, Raw: raw }, this.target())
    const card = mapServerListItem(resp.Card)
    this.setState({ cards: this.upsertListItem(card) })
    return card
  }

  /** List the instance maps spawned from a workflow template (newest first).
   *  Powers the template card context menu's "View Instances" submenu. */
  async listTemplateRuns(templateMapId: string, limit = 20): Promise<TemplateRunRecord[]> {
    if (!this.currentProjectId) return []
    const resp = await wikiClient.wikiListTemplateRuns(
      client,
      { TemplateMapId: templateMapId, Limit: limit },
      this.target(),
    )
    return resp.Runs ?? []
  }

  /** Save a workflow map as a reusable template (id = tpl::<mapId>). Used by
   *  the topology card context menu's "Save as Template" action. */
  async saveWorkflowTemplate(mapId: string): Promise<MonoCardListItem | null> {
    if (!this.currentProjectId) return null
    const templateId = `tpl::${mapId}`
    const resp = await wikiClient.wikiTemplateSave(
      client,
      { MapId: mapId, TemplateId: templateId },
      this.target(),
    )
    const template = mapServerListItem(resp.TemplateMap)
    this.setState({ cards: this.upsertListItem(template) })
    return template
  }

  async deleteCard(title: string, opts?: { projectId?: string | null }): Promise<void> {
    const foreign = this.isForeignProject(opts?.projectId)
    const projectId = opts?.projectId ?? this.currentProjectId
    if (!projectId) return
    if (!foreign && this.isWorkspaceCard(title)) {
      await workspaceWikiClient.wikiDeleteCard(client, { Id: title })
    } else {
      await wikiClient.wikiDeleteCard(client, { Id: title }, { target: projectId })
    }
    if (foreign) return
    this.cardCache.delete(title)
    this.setState({ cards: this.state.cards.filter(c => c.id !== title) })
  }

  async openCardAtTop(title: string) {
    // Opening an already visible card is navigation, not a request to reorder
    // the story. Callers are responsible for scrolling it into view.
    if (isDraftId(title) || this.state.openCards.includes(title)) return
    await this.saveOpenCards([title, ...this.state.openCards])
  }

  async closeCard(title: string, opts?: { projectId?: string | null }) {
    if (isDraftId(title)) return
    // Foreign-project close uses the single-card callable so the other
    // project's persisted open-cards list is updated without touching this
    // store's singleton openCards state.
    if (this.isForeignProject(opts?.projectId) && opts?.projectId) {
      await wikiClient.wikiCloseCard(client, { Id: title }, { target: opts.projectId })
      return
    }
    const next = this.state.openCards.filter(c => c !== title)
    await this.saveOpenCards(next)
  }

  async closeAllCards() {
    await this.saveOpenCards([])
  }

  uniqueSlug(name: string): string {
    const base = cardIdFromTitle(name) || 'card'
    if (!this.state.cards.some(c => c.id === name)) return base
    let suffix = 1
    while (this.state.cards.some(c => cardIdFromTitle(c.id) === `${base}-${suffix}`)) {
      suffix++
    }
    return `${base}-${suffix}`
  }
}

export function mapServerListItem(item: {
  Id: string
  Type?: string
  Source?: string
  Storage?: string
  Visibility?: string
  Protected?: boolean
  Editable?: boolean
  Deletable?: boolean
  Tags: string[]
  List: string[]
  Modified: string
  Created?: string
  Due?: string
  Priority?: string
  Status?: string
  Parent?: string
  Standalone?: boolean
  Raw: string
  Data?: Record<string, unknown>
}): MonoCardListItem {
  return {
    id: item.Id,
    type: item.Type,
    source: item.Source,
    storage: item.Storage,
    visibility: item.Visibility,
    protected: item.Protected,
    editable: item.Editable,
    deletable: item.Deletable,
    tags: item.Tags || [],
    list: item.List || [],
    modified: item.Modified,
    created: item.Created,
    due: item.Due,
    priority: item.Priority as MonoCardListItem['priority'],
    status: item.Status ? normalizeTaskStatus(item.Status) : undefined,
    parent: item.Parent,
    standalone: item.Standalone,
    raw: item.Raw,
    data: item.Data,
  }
}



/** Draft card IDs are ephemeral UI state and must never be persisted. */
function isDraftId(id: string): boolean {
  return id.startsWith('draft-')
}

/** Deduplicate and strip draft IDs from the persisted open-cards list. */
function sanitizeOpenCards(openCards: string[]): string[] {
  return [...new Set(openCards)].filter(id => !isDraftId(id))
}

export const monoStore = new MonoStore()
