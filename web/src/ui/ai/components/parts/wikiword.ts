/**
 * Shared wikiword processing utilities.
 *
 * Supports wikiword syntaxes, all converted to markdown links with the `wiki:`
 * protocol so react-markdown can parse them. The wiki target is URL-encoded to
 * survive CommonMark link-destination parsing (which truncates at whitespace):
 * 1. `[[CardId]]` and `[[display|target]]` — explicit double-bracket links
 * 2. `[display](wiki:target)` — markdown-style links with the `wiki:` protocol
 * 3. CamelCase auto-detection in prose text (e.g. "see ProjectSummary for details")
 * 4. `` `CamelCase` `` — single-backtick CamelCase becomes a wiki link.
 *    Backtick content WITH a file extension (e.g. `` `file.go` ``) is left
 *    untouched for the file-reference rehype plugin.
 *
 * The consuming component's `a` renderer intercepts `wiki:` links and decodes
 * the target with `decodeURIComponent` to navigate.
 */

import { defaultUrlTransform } from 'react-markdown'
import { isCodeFileExt } from './file-reference'

// ── URL transform ──

/**
 * Preserve `wiki:` protocol URLs; pass everything else through default transform.
 */
export function wikiUrlTransform(url: string): string {
  return url.startsWith('wiki:') ? url : defaultUrlTransform(url)
}

// ── Pattern constants ──

/** CamelCase pattern: at least two [A-Z][a-z]+ segments, e.g. ProjectSummary */
const CAMEL_CASE_RE = /\b((?:[A-Z][a-z]+){2,})\b/g

/** Strict CamelCase for backtick detection (entire string must match) */
const STRICT_CAMEL_CASE_RE = /^(?:[A-Z][a-z]+){2,}$/

/** Already-converted markdown link [text](url) — protect from auto-detection */
const MD_LINK_RE = /^\[[^\]]*]\([^)]*\)$/

/** [[...]] explicit link pattern */
const BRACKET_LINK_RE = /\[\[([^\]]+)\]\]/g

/** Escape markdown emphasis/bracket characters so they render literally in link text */
function escapeMdLinkText(s: string): string {
  return s.replace(/([_*~\[\]])/g, '\\$1')
}

/** Convert a [[...]] match to a markdown link string */
function bracketToMarkdownLink(raw: string): string {
  const pipe = raw.indexOf('|')
  if (pipe >= 0) {
    const display = raw.slice(0, pipe).trim()
    const target = raw.slice(pipe + 1).trim()
    return `[${escapeMdLinkText(display)}](wiki:${encodeURIComponent(target || display)})`
  }
  const word = raw.trim()
  return `[${escapeMdLinkText(word)}](wiki:${encodeURIComponent(word)})`
}

/** Re-encode the target of an existing `[display](wiki:target)` link so that
 *  spaces and other characters survive CommonMark URL parsing (which truncates
 *  at whitespace). Non-wiki links are returned unchanged.
 *  Idempotent: decodes first so links already encoded by [[...]] expansion
 *  are not double-encoded. */
function reencodeWikiMarkdownLink(seg: string): string {
  const m = seg.match(/^\[([^\]]*)\]\(wiki:([^)]*)\)$/)
  if (!m) return seg
  const display = m[1] ?? ''
  const rawTarget = (m[2] ?? '').trim()
  let target = rawTarget
  try { target = decodeURIComponent(rawTarget) } catch { /* not encoded, use raw */ }
  return `[${display}](wiki:${encodeURIComponent(target)})`
}

/**
 * Check whether a backtick-delimited string should be treated as a file reference
 * (has a known code file extension) rather than a wikiword.
 */
function looksLikeFileReference(text: string): boolean {
  const dotIdx = text.lastIndexOf('.')
  if (dotIdx < 0) return false
  const ext = text.slice(dotIdx + 1)
  return isCodeFileExt(ext)
}

// ── Core processing ──

/** Extract the wiki link targets referenced in a card body without transforming
 *  it. Recognizes [[CardId]], [[display|target]], CamelCase in prose, and
 *  `CamelCase` in backticks (excluding file references). Used to derive weak
 *  wikiword graph edges between cards linked by body references. */
export function extractWikiWordTargets(body: string): string[] {
  const targets = new Set<string>()
  const lines = body.split('\n')
  let inCodeBlock = false
  for (const line of lines) {
    if (line.trim().startsWith('```')) {
      inCodeBlock = !inCodeBlock
      continue
    }
    if (inCodeBlock) continue
    for (const m of line.matchAll(BRACKET_LINK_RE)) {
      const raw = (m[1] ?? '').trim()
      if (!raw) continue
      const pipe = raw.indexOf('|')
      targets.add(pipe >= 0 ? (raw.slice(pipe + 1).trim() || raw.slice(0, pipe).trim()) : raw)
    }
    const parts = line.split(/(`[^`]+`)/g)
    for (const part of parts) {
      if (part.startsWith('`')) {
        if (part.length > 2 && part.endsWith('`')) {
          const inner = part.slice(1, -1)
          if (STRICT_CAMEL_CASE_RE.test(inner) && !looksLikeFileReference(inner)) targets.add(inner)
        }
        continue
      }
      const segments = part.split(/(\[[^\]]*]\([^)]*\))/g)
      for (const seg of segments) {
        if (MD_LINK_RE.test(seg)) {
          const wikiMatch = seg.match(/^\[([^\]]*)\]\(wiki:([^)]*)\)$/)
          if (wikiMatch) {
            const rawTarget = (wikiMatch[2] ?? '').trim()
            if (rawTarget) targets.add(rawTarget)
          }
          continue
        }
        for (const m of seg.matchAll(CAMEL_CASE_RE)) {
          if (m[1]) targets.add(m[1])
        }
      }
    }
  }
  return Array.from(targets)
}

/**
 * Process a markdown string to expand wikiword syntaxes into `wiki:` links.
 *
 * - `[[CardId]]` → `[CardId](wiki:CardId)` (link text escaped for safety)
 * - `[[display|target]]` → `[display](wiki:target)`
 * - `[display](wiki:target)` → target URL-encoded to survive CommonMark parsing
 * - CamelCase in prose → `[CamelCase](wiki:CamelCase)`
 * - `` `CamelCase` `` → `` [`CamelCase`](wiki:CamelCase) `` (code-styled link)
 *
 * All wiki targets are URL-encoded with `encodeURIComponent` so that spaces
 * and other special characters (common in card titles) don't break CommonMark
 * link-destination parsing, which truncates URLs at whitespace. The consuming
 * `a` renderer decodes with `decodeURIComponent`.
 *
 * Left untouched: fenced code blocks, file-like inline code (`` `file.go` ``),
 * non-CamelCase inline code, and existing non-wiki markdown links.
 */
export function processWikiWords(body: string): string {
  const lines = body.split('\n')
  const processedLines: string[] = []
  let inCodeBlock = false
  for (const line of lines) {
    if (line.trim().startsWith('```')) {
      inCodeBlock = !inCodeBlock
      processedLines.push(line)
      continue
    }
    if (inCodeBlock) {
      processedLines.push(line)
      continue
    }

    // 1. Expand [[...]] explicit links (per-line so code blocks are safe)
    const expanded = line.replace(BRACKET_LINK_RE, (_, raw) => bracketToMarkdownLink(raw))

    // 2. Process inline code spans and CamelCase
    const parts = expanded.split(/(`[^`]+`)/g)
    const processedParts = parts.map(part => {
      if (part.startsWith('`')) {
        if (part.length > 2 && part.endsWith('`')) {
          const inner = part.slice(1, -1)
          // CamelCase without file extension → wiki link
          // File-like content → leave for file-reference rehype plugin
          if (STRICT_CAMEL_CASE_RE.test(inner) && !looksLikeFileReference(inner)) {
            return `[\`${inner}\`](wiki:${encodeURIComponent(inner)})`
          }
        }
        return part // non-CamelCase or file reference: keep as inline code
      }

      // Prose text: re-encode wiki markdown links, auto-detect CamelCase
      const segments = part.split(/(\[[^\]]*]\([^)]*\))/g)
      return segments.map(seg =>
        MD_LINK_RE.test(seg)
          ? reencodeWikiMarkdownLink(seg)
          : seg.replace(CAMEL_CASE_RE, (_, w) => `[${w}](wiki:${encodeURIComponent(w)})`)
      ).join('')
    })
    processedLines.push(processedParts.join(''))
  }
  return processedLines.join('\n')
}
