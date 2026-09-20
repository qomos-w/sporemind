import { useEffect, useMemo, useState } from 'react'
import { client } from '../../../application/generated-client'
import * as projectWikiClient from '../../../gen-clients/project/client'
import type { MonoCardListItem } from '../../../gen-types/project.wiki.part1'
import { useAgentInfoList, type AgentInfo } from './agentInfoStore'
import { listBrowserWindows, onBrowserManagerEvent } from '../../../application/browser-manager'

/** Max candidates surfaced in the mention dropdown. */
export const MENTION_SEARCH_LIMIT = 10
/** Debounce window (ms) for the search triggered while typing a mention query. */
export const MENTION_SEARCH_DEBOUNCE_MS = 250

/**
 * Detects whether the input currently ends in an active prefix-token mention.
 *
 * Active when `prefix` begins a whitespace-delimited token AND that token is
 * the trailing (last) one — i.e. the user has typed '<prefix>query' with no
 * space after it yet. The prefix must sit at a token boundary (start of
 * string or preceded by whitespace); an embedded prefix like 'a@b' does not
 * count.
 *
 * Returns the start index of the prefix char and the text after it (the
 * query), or null when no active mention is present. Pure — extracted for
 * unit testing.
 */
export function parsePrefixToken(value: string, prefix: string): { start: number; query: string } | null {
  const re = new RegExp(`(?:^|\\s)${prefix.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(\\S*)$`)
  const m = value.match(re)
  if (!m || m.index === undefined) return null
  // m.index points at the start of the whole match: when the leading
  // alternative was '^', the match begins with the prefix; when it was '\s',
  // it begins with the whitespace char and the prefix is one further in.
  const atStart = m.index + (m[0].startsWith(prefix) ? 0 : 1)
  return { start: atStart, query: m[1] ?? '' }
}

/** Card mention trigger: '#'. */
export function parseCardMention(value: string): { start: number; query: string } | null {
  return parsePrefixToken(value, '#')
}

/** File mention trigger: '$'. */
export function parseFileMention(value: string): { start: number; query: string } | null {
  return parsePrefixToken(value, '$')
}

/** Agent mention trigger: '@'. */
export function parseAgentMention(value: string): { start: number; query: string } | null {
  return parsePrefixToken(value, '@')
}

/** Browser mention trigger: '%'. */
export function parseBrowserMention(value: string): { start: number; query: string } | null {
  return parsePrefixToken(value, '%')
}

export interface UseCardMentionResult {
  results: MonoCardListItem[]
  loading: boolean
}

export interface UseFileMentionResult {
  results: string[]
  loading: boolean
}

/** An independent browser instance surfaced by the %-mention dropdown. */
export interface BrowserMentionItem {
  /** Browser manager instance ID (Config.Id) — the browser-chat tag suffix. */
  instanceId: string
  /** Display name (Config.Name, falling back to URL / ID). */
  name: string
  /** Live URL (Status.Url, falling back to Config.Url). */
  url: string
}

export interface UseBrowserMentionResult {
  results: BrowserMentionItem[]
  loading: boolean
}

/**
 * Re-rank #-mention candidates for a non-empty query: entries that match the
 * query earlier (prefix matches first) sort ahead of later matches, except
 * that cards with neither a Created nor a Modified stamp always rank last —
 * the dropdown is a "recent cards" list and stamp-less cards are its lowest
 * priority regardless of match quality. The input order is preserved within
 * the same rank via a stable sort, so ties fall back to the backend order.
 * An empty query returns the input untouched. Pure — extracted for unit
 * testing.
 */
export function rankMentionResults(cards: MonoCardListItem[], query: string): MonoCardListItem[] {
  const q = query.trim().toLowerCase()
  if (!q) return cards
  const timeless = (card: MonoCardListItem) => (!card.Created && !card.Modified ? 1 : 0)
  const rankOf = (id: string) => {
    const idx = id.toLowerCase().indexOf(q)
    return idx < 0 ? Number.MAX_SAFE_INTEGER : idx
  }
  return [...cards].sort((a, b) => (timeless(a) - timeless(b)) || (rankOf(a.Id) - rankOf(b.Id)))
}

/**
 * Re-rank file candidates for a non-empty query: paths whose basename matches
 * the query earlier sort ahead. Same stable-sort semantics as
 * rankMentionResults. Pure — extracted for unit testing.
 */
export function rankFileMentionResults(paths: string[], query: string): string[] {
  const q = query.trim().toLowerCase()
  if (!q) return paths
  const rankOf = (path: string) => {
    const base = path.split(/[\\/]/).pop() ?? path
    const idx = base.toLowerCase().indexOf(q)
    return idx < 0 ? Number.MAX_SAFE_INTEGER : idx
  }
  return [...paths].sort((a, b) => rankOf(a) - rankOf(b))
}

/**
 * Initials of a display name: the first letter of every word, where a word
 * boundary is a non-letter run or a lower→upper camelCase transition —
 * 'Syntax Blob', 'syntax blob' and 'SyntaxBlob' all yield 'sb'. Pure —
 * extracted for unit testing.
 */
export function nameInitials(name: string): string {
  let out = ''
  for (let i = 0; i < name.length; i++) {
    const ch = name[i] ?? ''
    if (!/[A-Za-z]/.test(ch)) continue
    const prev = name[i - 1] ?? ''
    const boundary = !prev || !/[A-Za-z]/.test(prev) || (/[a-z]/.test(prev) && /[A-Z]/.test(ch))
    if (boundary) out += ch
  }
  return out.toLowerCase()
}

/**
 * Re-rank @-mention agent candidates for a non-empty query: agents whose
 * DisplayName, Id or DisplayName initials (first letters of each word, e.g.
 * 'Syntax Blob' → 'sb') match the query earlier sort ahead of later matches;
 * the earliest match position across the three keys wins and the input order
 * (backend list order) is preserved as the stable tie-break. Unloaded agents
 * and the current agent itself are always filtered out — an unloaded target
 * cannot resolve the agent-chat tag at mount time, and self-mention is
 * meaningless for inter-agent chat. An empty query returns the filtered list
 * untouched (a bare '@' surfaces every loaded, non-self agent). Pure —
 * extracted for unit testing. Same stable-sort semantics as
 * rankMentionResults.
 */
export function rankAgentMentionResults(items: AgentInfo[], query: string, currentAgentId?: string | null): AgentInfo[] {
  const q = query.trim().toLowerCase()
  const filtered = items.filter(
    (a) => a.LoadState === 'loaded' && a.Id !== currentAgentId && a.ActorId !== currentAgentId,
  )
  if (!q) return filtered
  const rankOf = (a: AgentInfo) => {
    const name = (a.DisplayName || a.Title || '').toLowerCase()
    const nameIdx = name.indexOf(q)
    const idIdx = a.Id.toLowerCase().indexOf(q)
    const initialsIdx = nameInitials(a.DisplayName || a.Title || '').indexOf(q)
    return Math.min(
      nameIdx < 0 ? Number.MAX_SAFE_INTEGER : nameIdx,
      idIdx < 0 ? Number.MAX_SAFE_INTEGER : idIdx,
      initialsIdx < 0 ? Number.MAX_SAFE_INTEGER : initialsIdx,
    )
  }
  return [...filtered].sort((a, b) => rankOf(a) - rankOf(b))
}

/**
 * Append wikiword card links (`[[CardId]]`) to the message text. Multiple card
 * IDs are space-joined and placed on a new line after the existing text. When
 * the text is empty the links stand alone. Pure — extracted for unit testing.
 */
export function formatCardLinks(text: string, cardIds: string[]): string {
  const ids = cardIds.filter(id => id.length > 0)
  if (ids.length === 0) return text
  const links = ids.map(id => `[[${id}]]`).join(' ')
  return text.trim() ? `${text.trimEnd()}\n${links}` : links
}

/**
 * Append file references (project-relative paths in backticks) to the message
 * text, same layout as formatCardLinks. Pure — extracted for unit testing.
 */
export function formatFileRefs(text: string, paths: string[]): string {
  const list = paths.filter(p => p.length > 0)
  if (list.length === 0) return text
  const refs = list.map(p => `\`${p}\``).join(' ')
  return text.trim() ? `${text.trimEnd()}\n${refs}` : refs
}

/**
 * Debounced project-card search for the composer #-mention mode. Only fires
 * while `enabled` (an active #-token is present); the query — possibly empty
 * when the user has typed only '#' — scopes the search. `projectId` routes the
 * call to the project actor (invoke `target`, same as mono-store) — without it
 * the request does not reach the project's card store. Agent cards (synthetic
 * external `type: agent` items backed by workspace agents) are filtered out —
 * the dropdown offers knowledge cards to link, not agents. Candidates always
 * come back latest-modified first; a non-empty query additionally re-ranks by
 * match position (rankMentionResults) so prefix matches surface first.
 */
export function useCardMention(
  enabled: boolean,
  query: string,
  projectId?: string | null,
): UseCardMentionResult {
  const [results, setResults] = useState<MonoCardListItem[]>([])
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!enabled) {
      setResults([])
      setLoading(false)
      return
    }
    let active = true
    setLoading(true)
    const timer = setTimeout(() => {
      projectWikiClient
        .wikiListCards(
          client,
          { Flat: true, Query: query, OrderBy: '-modified', Limit: MENTION_SEARCH_LIMIT },
          projectId ? { target: projectId } : undefined,
        )
        .then((resp) => {
          if (!active) return
          const cards = (resp.Cards ?? []).filter((c) => c.Type !== 'agent')
          setResults(rankMentionResults(cards, query))
          setLoading(false)
        })
        .catch(() => {
          if (!active) return
          setResults([])
          setLoading(false)
        })
    }, MENTION_SEARCH_DEBOUNCE_MS)
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [enabled, query, projectId])

  return { results, loading }
}

/**
 * Debounced project-file search for the composer $-mention mode. Only fires
 * while `enabled` AND the query is non-empty — a bare '$' lists nothing
 * (globbing the whole project tree would flood the dropdown). The query is
 * embedded in a `**\/*<query>*` glob so it matches against file names anywhere
 * in the tree; the request orders matches latest-mtime first and gitignored
 * entries are filtered server-side (same rules as the file browser). Results
 * are re-ranked by basename match position via a stable sort — recency
 * survives as the tie-break — and capped at MENTION_SEARCH_LIMIT.
 */
export function useFileMention(
  enabled: boolean,
  query: string,
  projectId?: string | null,
): UseFileMentionResult {
  const [results, setResults] = useState<string[]>([])
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    // Strip glob/regex metacharacters: the backend treats any pattern with
    // '\' as a regex (regexp.Compile of a glob string fails → no results),
    // and '*?[]' would turn the user query into a glob expression.
    const cleaned = query.replace(/[*?[\]\\]/g, '')
    if (!enabled || !cleaned) {
      setResults([])
      setLoading(false)
      return
    }
    let active = true
    setLoading(true)
    const timer = setTimeout(() => {
      // A bare term (no slashes) already gets the backend's recursive
      // substring fallback ("**/*term*"); slash queries need explicit
      // wildcard framing.
      const pattern = cleaned.includes('/') ? `**/*${cleaned}*` : cleaned
      projectWikiClient
        .glob(
          client,
          { Pattern: pattern, Order_by: '-modified' },
          projectId ? { target: projectId } : undefined,
        )
        .then((resp) => {
          if (!active) return
          setResults(rankFileMentionResults(resp.Files ?? [], cleaned).slice(0, MENTION_SEARCH_LIMIT))
          setLoading(false)
        })
        .catch(() => {
          if (!active) return
          setResults([])
          setLoading(false)
        })
    }, MENTION_SEARCH_DEBOUNCE_MS)
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [enabled, query, projectId])

  return { results, loading }
}

/**
 * Loaded workspace agents for the composer @-mention surface. Unlike the card
 * and file searches this is a synchronous client-side snapshot (the enriched
 * agentInfoStore already tracks the full agent list plus project names), so
 * there is no debounce or backend round-trip: the results are re-ranked on
 * every render via the pure rankAgentMentionResults helper. `currentAgentId`
 * (the composer's own agent ActorId) filters self out of the dropdown.
 * Disabled when no '@'-token is active, in which case the empty list hides
 * the agent section entirely.
 */
export function useAgentMention(enabled: boolean, query: string, currentAgentId?: string | null): AgentInfo[] {
  const { items } = useAgentInfoList()
  return useMemo(
    () => (enabled ? rankAgentMentionResults(items, query, currentAgentId) : []),
    [enabled, items, query, currentAgentId],
  )
}

/**
 * Re-rank independent-browser candidates for a non-empty query: names that
 * match the query earlier sort ahead; non-matches keep the input order after
 * matches (same stable-sort semantics as rankAgentMentionResults). An empty
 * query returns the input untouched. Pure — extracted for unit testing.
 */
export function rankBrowserMentionResults(items: BrowserMentionItem[], query: string): BrowserMentionItem[] {
  const q = query.trim().toLowerCase()
  if (!q) return items
  const rankOf = (b: BrowserMentionItem) => {
    const idx = b.name.toLowerCase().indexOf(q)
    return idx < 0 ? Number.MAX_SAFE_INTEGER : idx
  }
  return [...items].sort((a, b) => rankOf(a) - rankOf(b))
}

/**
 * Open independent browser instances for the composer %-mention mode. Lists
 * browser-manager instances (debounced while typing, like useCardMention) and
 * keeps only tab-mode instances that are currently open — the same filter the
 * right-panel restore uses (AIShellLayout), since only those have a live
 * browser-chat target. (All user instances are tab-mode now; the filter still
 * excludes internal crawl-engine instances.) A browser-manager event
 * (open/close/update) bypasses the debounce and refreshes immediately so the
 * dropdown reflects a tab closing while it is open. Disabled (empty results)
 * when no '%'-token is active.
 */
export function useBrowserMention(enabled: boolean, query: string): UseBrowserMentionResult {
  const [results, setResults] = useState<BrowserMentionItem[]>([])
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!enabled) {
      setResults([])
      setLoading(false)
      return
    }
    let active = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const load = () => {
      setLoading(true)
      listBrowserWindows()
        .then((instances) => {
          if (!active) return
          const items = instances
            .filter((inst) => inst.Config.Mode === 'tab' && inst.Status.Open)
            .map((inst) => ({
              instanceId: inst.Config.Id,
              name: inst.Config.Name || inst.Config.Url || inst.Config.Id,
              url: inst.Status.Url || inst.Config.Url,
            }))
          setResults(rankBrowserMentionResults(items, query).slice(0, MENTION_SEARCH_LIMIT))
          setLoading(false)
        })
        .catch(() => {
          if (!active) return
          setResults([])
          setLoading(false)
        })
    }
    timer = setTimeout(load, MENTION_SEARCH_DEBOUNCE_MS)
    const unsubscribe = onBrowserManagerEvent(() => {
      if (timer) clearTimeout(timer)
      timer = undefined
      load()
    })
    return () => {
      active = false
      if (timer) clearTimeout(timer)
      unsubscribe()
    }
  }, [enabled, query])

  return { results, loading }
}
