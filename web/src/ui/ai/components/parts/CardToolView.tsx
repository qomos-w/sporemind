import React, { useContext, useMemo } from 'react'
import type { ToolFrame } from '../../model/frame-types.ts'
import { parseJsonObject } from './tool-display.ts'
import { ToolBodyFrame, ToolRunning, ToolFallbackOutput, ToolCodeBlock } from './ToolViewPrimitives.tsx'
import { MonoCardInline } from '../MonoCardInline.tsx'
import { AIShellContext } from '../../context/AIShellContext'
import type { MonoCardListItem as DomainCard } from '../../../../domain/mono-types'
import type { MonoCardListItem as GenCard } from '../../../../gen-types/project.wiki'

interface CardToolViewProps {
  frame: ToolFrame
}

/** Map a gen-type PascalCase card to domain lowercase card. */
function toDomainCard(raw: GenCard): DomainCard {
  return {
    id: raw.Id,
    tags: raw.Tags ?? [],
    list: raw.List ?? [],
    modified: raw.Modified,
    due: raw.Due || undefined,
    priority: raw.Priority as DomainCard['priority'],
    status: raw.Status as DomainCard['status'],
    parent: raw.Parent || undefined,
    standalone: raw.Standalone,
    raw: raw.Raw,
    data: raw.Data,
  }
}

/**
 * Tool view for wiki/card callables.
 * Renders card data as compact MonoCardInline previews instead of raw JSON.
 *
 * Handles response shapes:
 * - { Id, Raw } — get_card, get_summary, get_constraints, edit_summary, edit_constraints
 * - { Card: {...} } — create_card, edit_card
 * - { Cards: [...] } — list_all_cards
 * - { Tree: "..." } — list_cards
 * - { OpenCards: [...] } — open_card, close_card, get_open_cards, save_open_cards
 * - { Id: "..." } — delete_card
 * - { Created: [...] } — ensure_builtins
 */
export const CardToolView: React.FC<CardToolViewProps> = ({ frame }) => {
  const aiShellCtx = useContext(AIShellContext)
  const isRunning = frame.status === 'running'

  const result = useMemo(() => {
    const parsed = parseJsonObject(frame.output || '')
    if (!parsed) return null

    // Single card from create_card/edit_card: { Card: {...} }
    if (parsed.Card && typeof parsed.Card === 'object') {
      const card = toDomainCard(parsed.Card as GenCard)
      return { type: 'single' as const, card, raw: (parsed.Card as GenCard).Raw }
    }

    // Single card from get_card/get_summary/etc: { Id, Raw }
    if (parsed.Id && typeof parsed.Id === 'string' && parsed.Raw != null && typeof parsed.Raw === 'string') {
      // Parse raw to extract card metadata
      const card = cardFromRaw(parsed.Id, parsed.Raw)
      return { type: 'single' as const, card, raw: parsed.Raw }
    }

    // Card list from list_all_cards: { Cards: [...] }
    if (Array.isArray(parsed.Cards)) {
      const cards = (parsed.Cards as GenCard[]).map(toDomainCard)
      return { type: 'list' as const, cards }
    }

    // Open cards from open_card/close_card/get_open_cards/save_open_cards
    if (Array.isArray(parsed.OpenCards)) {
      const ids = parsed.OpenCards as string[]
      return { type: 'opencards' as const, ids }
    }

    // Tree string from list_cards
    if (typeof parsed.Tree === 'string') {
      return { type: 'tree' as const, tree: parsed.Tree }
    }

    // Delete card: { Id: "..." } (no Raw)
    if (parsed.Id && typeof parsed.Id === 'string') {
      return { type: 'deleted' as const, id: parsed.Id }
    }

    // Ensure builtins: { Created: [...] }
    if (Array.isArray(parsed.Created)) {
      return { type: 'builtins' as const, created: parsed.Created as string[] }
    }

    return null
  }, [frame.output])

  const handleOpenCard = (cardId: string) => aiShellCtx?.onOpenCard?.(cardId)

  return (
    <ToolBodyFrame frame={frame}>
      {isRunning && <ToolRunning label="Working with cards…" />}

      {result?.type === 'single' && (
        <MonoCardInline
          card={result.card}
          raw={result.raw}
          onClick={() => handleOpenCard(result.card.id)}
        />
      )}

      {result?.type === 'list' && (
        <div className="ai-card-tool-list">
          {result.cards.length === 0 ? (
            <span className="ai-card-tool-empty">No cards found</span>
          ) : (
            result.cards.slice(0, 20).map(card => (
              <MonoCardInline
                key={card.id}
                card={card}
                onClick={() => handleOpenCard(card.id)}
              />
            ))
          )}
          {result.cards.length > 20 && (
            <span className="ai-card-tool-more">+{result.cards.length - 20} more cards</span>
          )}
        </div>
      )}

      {result?.type === 'opencards' && (
        <div className="ai-card-tool-opencards">
          <span className="ai-card-tool-summary">
            {result.ids.length} open card{result.ids.length === 1 ? '' : 's'}
          </span>
          {result.ids.length > 0 && (
            <div className="ai-card-tool-chips">
              {result.ids.map(id => (
                <button
                  key={id}
                  className="ai-card-tool-chip"
                  onClick={() => handleOpenCard(id)}
                  title={id}
                >
                  {id}
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      {result?.type === 'tree' && (
        <ToolCodeBlock maxLines={20}>{result.tree}</ToolCodeBlock>
      )}

      {result?.type === 'deleted' && (
        <div className="ai-card-tool-deleted">Card deleted: {result.id}</div>
      )}

      {result?.type === 'builtins' && (
        <div className="ai-card-tool-builtins">
          {result.created.length > 0
            ? `Created ${result.created.length} builtin card(s): ${result.created.join(', ')}`
            : 'All builtin cards already exist'}
        </div>
      )}

      {/* Fallback for unrecognized output */}
      {!result && !isRunning && (
        <ToolFallbackOutput frame={frame} parsed={null} />
      )}
    </ToolBodyFrame>
  )
}

/** Parse a raw markdown card to extract a MonoCardListItem (metadata + raw). */
function cardFromRaw(id: string, raw: string): DomainCard {
  const fmMatch = raw.match(/^---\n([\s\S]*?)\n---\n?/)
  if (!fmMatch) {
    return { id, tags: [], list: [], modified: '', raw }
  }
  const meta = parseSimpleYaml(fmMatch[1]!)
  const tags = Array.isArray(meta.tags) ? meta.tags : (typeof meta.tags === 'string' ? [meta.tags] : [])
  const list = Array.isArray(meta.list) ? meta.list : (typeof meta.list === 'string' ? [meta.list] : [])
  return {
    
    id: typeof meta.id === 'string' ? meta.id : id,
    tags: tags.filter((t: unknown): t is string => typeof t === 'string'),
    list: list.filter((l: unknown): l is string => typeof l === 'string'),
    modified: typeof meta.modified === 'string' ? meta.modified : '',
    due: typeof meta.due === 'string' ? meta.due : undefined,
    priority: typeof meta.priority === 'string' ? meta.priority as DomainCard['priority'] : undefined,
    status: typeof meta.status === 'string' ? meta.status as DomainCard['status'] : undefined,
    parent: typeof meta.parent === 'string' ? meta.parent : undefined,
    standalone: meta.standalone === true,
    raw,
    data: meta.data && typeof meta.data === 'object' && !Array.isArray(meta.data)
      ? meta.data as Record<string, unknown>
      : undefined,
  }
}

/** Minimal YAML parser for flat key-value pairs and inline arrays. */
function parseSimpleYaml(text: string): Record<string, unknown> {
  const result: Record<string, unknown> = {}
  const lines = text.split('\n')
  for (const line of lines) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) continue
    const colonIdx = trimmed.indexOf(':')
    if (colonIdx === -1) continue
    const key = trimmed.slice(0, colonIdx).trim()
    let value = trimmed.slice(colonIdx + 1).trim()
    // Inline array
    if (value.startsWith('[') && value.endsWith(']')) {
      const inner = value.slice(1, -1).trim()
      result[key] = inner ? inner.split(',').map(s => s.trim().replace(/^["']|["']$/g, '')) : []
      continue
    }
    // Strip quotes
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1)
    }
    if (value === 'true') { result[key] = true; continue }
    if (value === 'false') { result[key] = false; continue }
    result[key] = value
  }
  return result
}
