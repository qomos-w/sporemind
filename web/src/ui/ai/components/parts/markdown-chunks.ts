export interface MarkdownChunk {
  content: string
  finalized: boolean
}

/** A markdown chunk plus the raw character span (in the source text) it was
 *  built from. `rawEnd` is exclusive and clamped to the text length. */
export interface MarkdownChunkSpan extends MarkdownChunk {
  rawStart: number
  rawEnd: number
}

/**
 * Split Markdown text into chunks at block boundaries, tracking the raw
 * source span of every chunk. Boundary semantics are identical to
 * `splitMarkdownChunks`; this variant lets incremental consumers cache the
 * stable prefix without re-scanning the whole text each reveal tick.
 *
 * - Code blocks (```) are atomic: only finalized when the closing fence arrives.
 * - Paragraphs are split on empty lines; the trailing paragraph without a
 *   following empty line is marked streaming (not finalized).
 *
 * Finalized chunks can be rendered with <Markdown> and cached; the streaming
 * tail is rendered as plain text so delta updates don't trigger full AST
 * re-parses.
 */
export function splitMarkdownChunksWithSpans(text: string): MarkdownChunkSpan[] {
  if (!text) return []

  const lines = text.split('\n')
  const chunks: MarkdownChunkSpan[] = []
  let buffer = ''
  let bufferStart = 0
  let inCodeBlock = false
  let codeFence = ''
  const textLen = text.length

  let offset = 0
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!
    const lineStart = offset
    offset += line.length + 1
    if (buffer === '') bufferStart = lineStart
    const trimmed = line.trimStart()

    // ── Code-block fence ──
    const fenceMatch = trimmed.match(/^(```+)(.*)$/)
    if (fenceMatch && !inCodeBlock) {
      // Open fence
      if (buffer.trim()) {
        chunks.push({ content: buffer.trimEnd(), finalized: true, rawStart: bufferStart, rawEnd: lineStart })
        buffer = ''
      }
      inCodeBlock = true
      codeFence = fenceMatch[1] ?? '```'
      buffer = line + '\n'
      bufferStart = lineStart
      continue
    }

    if (inCodeBlock && trimmed.startsWith(codeFence)) {
      // Close fence
      buffer += line + '\n'
      chunks.push({ content: buffer.trimEnd(), finalized: true, rawStart: bufferStart, rawEnd: Math.min(offset, textLen) })
      buffer = ''
      inCodeBlock = false
      codeFence = ''
      continue
    }

    buffer += line + '\n'

    // ── Paragraph boundary (outside code blocks) ──
    if (!inCodeBlock && line === '' && buffer.trim()) {
      const nextLine = lines[i + 1]
      if (!nextLine || !isSameBlockType(buffer, nextLine)) {
        chunks.push({ content: buffer.trimEnd(), finalized: true, rawStart: bufferStart, rawEnd: Math.min(offset, textLen) })
        buffer = ''
      }
    }
  }

  // Trailing buffer is always streaming — it may still be growing.
  // Trim leading whitespace so blank lines after a closed block don't leak
  // into the next paragraph.
  if (buffer) {
    chunks.push({ content: buffer.trimStart(), finalized: false, rawStart: bufferStart, rawEnd: Math.min(offset, textLen) })
  }

  return chunks
}

/**
 * Split Markdown text into chunks at block boundaries (no span tracking).
 */
export function splitMarkdownChunks(text: string): MarkdownChunk[] {
  return splitMarkdownChunksWithSpans(text).map(({ rawStart: _rs, rawEnd: _re, ...chunk }) => chunk)
}

function isTableRow(line: string): boolean {
  const trimmed = line.trimStart()
  // Support blockquote-prefixed table rows like "> | a | b |".
  const content = trimmed.replace(/^>\s*/, '')
  const pipeCount = (content.match(/\|/g) ?? []).length
  if (pipeCount < 2) return false
  return content.startsWith('|') || content.endsWith('|')
}

function isTableDelimiterRow(line: string): boolean {
  const trimmed = line.trimStart()
  const content = trimmed.replace(/^>\s*/, '')
  return /^[|:\-\s]+$/.test(content) && (content.match(/\|/g) ?? []).length >= 2
}

/**
 * Heuristic: does the text look like a self-contained GFM table?
 *
 * Supports blockquote-prefixed tables (e.g. lines starting with "> ").
 * Used during streaming to render a complete trailing table as Markdown
 * even before the next block boundary arrives.
 */
export function looksLikeCompleteTable(text: string): boolean {
  const nonEmpty = text.split('\n').filter(line => line.trim() !== '')
  if (nonEmpty.length < 2) return false
  if (!nonEmpty.every(isTableRow)) return false
  return nonEmpty.some(isTableDelimiterRow)
}

function isSameBlockType(buffer: string, nextLine: string | undefined): boolean {
  if (!nextLine) return false
  const lines = buffer.trimEnd().split('\n')
  const last = lines[lines.length - 1]?.trimStart() || ''
  const next = nextLine.trimStart()

  // List items
  if (/^[-*+]\s/.test(last) && /^[-*+]\s/.test(next)) return true
  if (/^\d+\.\s/.test(last) && /^\d+\.\s/.test(next)) return true

  // Blockquote
  if (last.startsWith('>') && next.startsWith('>')) return true

  // Table rows (including blockquote-prefixed table rows); keeps a table intact
  // even if the LLM emits blank lines between header, delimiter and body rows.
  if (isTableRow(last) && isTableRow(next)) return true

  // Indented code block (legacy)
  if (last.startsWith('    ') && next.startsWith('    ')) return true

  return false
}
