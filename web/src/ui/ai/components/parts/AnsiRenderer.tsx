import React from 'react'

const ANSI_FG: Record<number, string> = {
  30: 'var(--ansi-black)', 31: 'var(--ansi-red)', 32: 'var(--ansi-green)', 33: 'var(--ansi-yellow)',
  34: 'var(--ansi-blue)', 35: 'var(--ansi-magenta)', 36: 'var(--ansi-cyan)', 37: 'var(--ansi-white)',
  90: 'var(--ansi-bright-black)', 91: 'var(--ansi-bright-red)', 92: 'var(--ansi-bright-green)', 93: 'var(--ansi-bright-yellow)',
  94: 'var(--ansi-bright-blue)', 95: 'var(--ansi-bright-magenta)', 96: 'var(--ansi-bright-cyan)', 97: 'var(--ansi-bright-white)',
}

const ANSI_BG: Record<number, string> = {
  40: 'var(--ansi-black)', 41: 'var(--ansi-red)', 42: 'var(--ansi-green)', 43: 'var(--ansi-yellow)',
  44: 'var(--ansi-blue)', 45: 'var(--ansi-magenta)', 46: 'var(--ansi-cyan)', 47: 'var(--ansi-white)',
  100: 'var(--ansi-bright-black)', 101: 'var(--ansi-bright-red)', 102: 'var(--ansi-bright-green)', 103: 'var(--ansi-bright-yellow)',
  104: 'var(--ansi-bright-blue)', 105: 'var(--ansi-bright-magenta)', 106: 'var(--ansi-bright-cyan)', 107: 'var(--ansi-bright-white)',
}

interface AnsiSpan {
  text: string
  fg?: string
  bg?: string
  bold?: boolean
}

// Active SGR style at a position in the stream. Text that has not yet been
// assigned to a span renders with this style.
interface AnsiStyleState {
  fg?: string
  bg?: string
  bold?: boolean
}

// Incremental parse cache. Everything before `length` is already parsed:
// finalized spans live in `spans`, the span still accepting text is `open`,
// and `state` is the SGR style at `length`. `head`/`tail` are samples of the
// parsed region so the next render can verify the new text is a pure append
// before resuming (mismatch → full reparse).
interface AnsiParseCache {
  length: number
  state: AnsiStyleState
  spans: AnsiSpan[]
  open: AnsiSpan | null
  head: string
  tail: string
}

const HEAD_SAMPLE = 64
const TAIL_SAMPLE = 64

function applySgr(state: AnsiStyleState, codes: number[]): void {
  for (const code of codes) {
    if (code === 0) {
      state.fg = undefined
      state.bg = undefined
      state.bold = undefined
    } else if (code === 1) {
      state.bold = true
    } else if (code >= 30 && code <= 37) {
      state.fg = ANSI_FG[code]
    } else if (code >= 40 && code <= 47) {
      state.bg = ANSI_BG[code]
    } else if (code >= 90 && code <= 97) {
      state.fg = ANSI_FG[code]
    } else if (code >= 100 && code <= 107) {
      state.bg = ANSI_BG[code]
    }
  }
}

// Parse text[cache.length ..] into the cache. An incomplete trailing ESC
// sequence (no terminating 'm', or a lone ESC at the very end) is left
// unparsed: cache.length stops at the ESC so the next chunk re-parses the
// whole sequence with complete data instead of caching a truncated state.
function parseAppend(text: string, cache: AnsiParseCache): void {
  const n = text.length
  let i = cache.length
  let open = cache.open
  while (i < n) {
    if (text.charCodeAt(i) === 0x1b) {
      if (i + 1 < n && text[i + 1] === '[') {
        const end = text.indexOf('m', i + 2)
        if (end === -1) break
        if (open) {
          if (open.text) cache.spans.push(open)
          open = null
        }
        const codes = text.slice(i + 2, end).split(';').map(Number).filter(v => !Number.isNaN(v))
        applySgr(cache.state, codes)
        i = end + 1
        continue
      }
      if (i + 1 === n) break
      // ESC not followed by '[': fall through and treat it as literal text,
      // matching the previous whole-buffer parser.
    }
    // Batch-copy the run of plain text up to the next ESC (indexOf scan)
    // instead of per-character concat.
    const nextEsc = text.indexOf('\x1b', i)
    let stop = nextEsc === -1 ? n : nextEsc
    if (stop === i) stop = i + 1
    if (!open) open = { text: '', ...cache.state }
    open.text += text.slice(i, stop)
    i = stop
  }
  cache.open = open
  cache.length = i
}

function snapshotSamples(text: string, cache: AnsiParseCache): void {
  cache.head = text.slice(0, HEAD_SAMPLE)
  const tailStart = Math.max(cache.head.length, cache.length - TAIL_SAMPLE)
  cache.tail = text.slice(tailStart, cache.length)
}

function AnsiTextInner({ text }: { text: string }): React.ReactNode {
  const cacheRef = React.useRef<AnsiParseCache | null>(null)
  let cache = cacheRef.current
  if (
    !cache ||
    text.length <= cache.length || // truncated or same-length replacement → reparse
    !text.startsWith(cache.head) ||
    text.slice(cache.length - cache.tail.length, cache.length) !== cache.tail
  ) {
    cache = { length: 0, state: {}, spans: [], open: null, head: '', tail: '' }
  }
  if (cache.length < text.length) {
    parseAppend(text, cache)
    snapshotSamples(text, cache)
  }
  cacheRef.current = cache

  if (!text) return null
  const spans = cache.open && cache.open.text ? [...cache.spans, cache.open] : cache.spans
  if (spans.length === 0) return null
  return (
    <>
      {spans.map((span, idx) => {
        const style: React.CSSProperties = {}
        if (span.fg) style.color = span.fg
        if (span.bg) style.backgroundColor = span.bg
        if (span.bold) style.fontWeight = 'bold'
        return <span key={idx} className={span.bg ? 'ansi-bg' : undefined} style={style}>{span.text}</span>
      })}
    </>
  )
}

export const AnsiText = React.memo(AnsiTextInner)
