/**
 * Incremental streaming-markdown pipeline.
 *
 * During a smooth-streamed reply the displayed string grows every reveal
 * tick (~16ms). Running the full pipeline — `processWikiWords` (multi-pass
 * regex) plus `splitMarkdownChunks` — over the whole string on every tick is
 * O(n) per tick and O(n²) over the stream, which is what makes a single very
 * long text frame stutter. This hook caches everything up to the last
 * finalized block boundary and re-processes only the active tail each tick.
 *
 * Finalized chunk content strings keep their reference identity across ticks,
 * so `MemoizedMarkdown` children compare by pointer instead of re-parsing.
 */

import { useMemo, useRef } from 'react'
import { processWikiWords } from './wikiword'
import { splitMarkdownChunks, splitMarkdownChunksWithSpans, type MarkdownChunk } from './markdown-chunks'

interface ChunkCache {
  /** Raw text covered by the cached finalized chunks. */
  rawPrefix: string
  /** Wiki-processed finalized chunks for `rawPrefix`. */
  chunks: MarkdownChunk[]
}

const EMPTY_CACHE: ChunkCache = { rawPrefix: '', chunks: [] }

const OPEN_DIAGRAM_FENCE = /^```mermaid\b/i

/**
 * True when a (necessarily unfinalized) chunk starts with an open mermaid
 * code fence. Callers render such a tail as a fixed-height placeholder
 * instead of streaming the raw source: a diagram's source is often hundreds
 * of lines, and revealing it line-by-line grows the stream on every tick,
 * which the scroll-follow engine pins to — dragging the whole stream down
 * for the entire generation (and again through the reveal drain).
 */
export function isOpenDiagramSource(content: string): boolean {
  return OPEN_DIAGRAM_FENCE.test(content.trimStart())
}

/**
 * Split smooth-streamed text into markdown chunks incrementally.
 *
 * Returns `null` when the content is settled (caller falls back to the single
 * full `<Markdown>` pass). While the content is still GROWING — actively
 * streaming OR still draining the smooth-reveal backlog after the frame
 * stopped running — the chunked path must be used at ANY length: an unclosed
 * code fence (e.g. a half-streamed mermaid block) must stay plain text in the
 * tail instead of being fed to full Markdown re-parsing, which would remount
 * diagram/error renderers on every reveal tick.
 *
 * Semantically equivalent to
 * `splitMarkdownChunks(processWikiWords(content))` for growing content.
 */
export function useStreamingMarkdownChunks(content: string, growing: boolean): MarkdownChunk[] | null {
  const cacheRef = useRef<ChunkCache>(EMPTY_CACHE)

  return useMemo(() => {
    if (!growing) return null

    let cache = cacheRef.current
    if (!content.startsWith(cache.rawPrefix)) {
      // Non-append content (history swap, regeneration): rebuild from scratch.
      cache = cacheRef.current = { rawPrefix: '', chunks: [] }
    }

    // Split the raw tail to locate newly finalized blocks. Boundary rules
    // are line-based, and both wiki processing and chunk splitting preserve
    // them, so re-splitting the processed absorbed region yields exactly the
    // chunks the full pipeline would produce.
    const tail = content.slice(cache.rawPrefix.length)
    const tailSpans = splitMarkdownChunksWithSpans(tail)

    let absorb = 0
    while (absorb < tailSpans.length && tailSpans[absorb]!.finalized) absorb += 1

    // A finalized span ending exactly at the slice end WITHOUT a trailing
    // newline had its rawEnd clamped (e.g. a closing fence revealed as the
    // last characters). Absorbing it would put the boundary mid-line, so the
    // next tick's active region starts with that line's own newline and the
    // following chunk gains an extra blank line versus the full pipeline.
    // Keep such a span active; it absorbs next tick once the line completes.
    if (absorb > 0 && tailSpans[absorb - 1]!.rawEnd === tail.length && !tail.endsWith('\n')) {
      absorb -= 1
    }

    let activeStart = 0
    if (absorb > 0) {
      activeStart = tailSpans[absorb - 1]!.rawEnd
      const absorbedRaw = tail.slice(0, activeStart)
      cache.rawPrefix += absorbedRaw
      // The splitter can emit an empty trailing chunk when the absorbed span
      // ends on blank lines; drop it — it would sit forever in the cache.
      const absorbedChunks = splitMarkdownChunks(processWikiWords(absorbedRaw)).filter(c => c.content !== '')
      cache.chunks = cache.chunks.concat(absorbedChunks)
    }

    // Only the active (growing) region pays the regex + split cost per tick.
    const activeRaw = tail.slice(activeStart)
    const activeChunks = splitMarkdownChunks(processWikiWords(activeRaw)).filter(c => c.content !== '')
    return activeChunks.length > 0 ? cache.chunks.concat(activeChunks) : cache.chunks.slice()
  }, [content, growing])
}
