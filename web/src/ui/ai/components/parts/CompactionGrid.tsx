import React, { useEffect, useRef, useState } from 'react'
import type { CompactionFrame, ContextSegment, ContextKind, CompactedRange } from '../../model/frame-types.ts'

export const KIND_COLORS: Record<ContextKind, string> = {
  cold: 'hsl(210, 55%, 55%)',
  hot: 'hsl(32, 80%, 56%)',
  summary: 'hsl(265, 55%, 62%)',
  message: 'hsl(45, 78%, 58%)',
}

const FREE_COLOR = 'hsl(220, 10%, 18%, 0.3)'

const FLASH_DURATION = 1200
const COMPRESS_DURATION = 600
const REARRANGE_DURATION = 300
const CELL_SCALE = 200

interface RenderSeg {
  kind: ContextKind
  tokens: number
  flashing: boolean
  annealing: boolean
}

function segmentFlex(tokens: number, contextWindowSize: number): number {
  return Math.max(1, Math.round(tokens / contextWindowSize * CELL_SCALE))
}

/** Split segments at compacted boundaries — flashing portions are extracted. */
function splitForFlashing(
  layout: ContextSegment[],
  ranges: CompactedRange[],
): RenderSeg[] {
  // Build a map: segmentIndex → total tokens to compact
  const compactMap = new Map<number, number>()
  for (const r of ranges) {
    compactMap.set(r.segmentIndex, (compactMap.get(r.segmentIndex) ?? 0) + r.tokens)
  }

  const result: RenderSeg[] = []
  for (let i = 0; i < layout.length; i++) {
    const seg = layout[i]!
    const compactTokens = compactMap.get(i)
    if (compactTokens && compactTokens < seg.tokens) {
      // Split: flashing portion + remainder
      result.push({ kind: seg.kind as ContextKind, tokens: compactTokens, flashing: true, annealing: false })
      result.push({ kind: seg.kind as ContextKind, tokens: seg.tokens - compactTokens, flashing: false, annealing: false })
    } else if (compactTokens) {
      // Entire segment flashes
      result.push({ kind: seg.kind as ContextKind, tokens: seg.tokens, flashing: true, annealing: false })
    } else {
      result.push({ kind: seg.kind as ContextKind, tokens: seg.tokens, flashing: false, annealing: false })
    }
  }
  return mergeAdjacent(result)
}

function layoutToRender(layout: ContextSegment[], annealingKinds: Set<ContextKind>): RenderSeg[] {
  const raw = layout.map(seg => ({
    kind: seg.kind as ContextKind,
    tokens: seg.tokens,
    flashing: false,
    annealing: annealingKinds.has(seg.kind as ContextKind),
  }))
  return mergeAdjacent(raw)
}

/** Merge adjacent RenderSegs with the same kind and state. */
function mergeAdjacent(segs: RenderSeg[]): RenderSeg[] {
  if (segs.length === 0) return segs
  const out: RenderSeg[] = [{ ...segs[0]! }]
  for (let i = 1; i < segs.length; i++) {
    const prev = out[out.length - 1]!
    const curr = segs[i]!
    if (prev.kind === curr.kind && prev.flashing === curr.flashing && prev.annealing === curr.annealing) {
      prev.tokens += curr.tokens
    } else {
      out.push({ ...curr })
    }
  }
  return out
}

interface CompactionGridProps {
  frame: CompactionFrame
}

export const CompactionGrid: React.FC<CompactionGridProps> = ({ frame }) => {
  const { contextWindowSize, beforeLayout, afterLayout, rounds } = frame

  // Stable refs so the animation effect doesn't restart on every frame re-projection.
  const afterRef = useRef(afterLayout)
  afterRef.current = afterLayout
  const beforeRef = useRef(beforeLayout)
  beforeRef.current = beforeLayout

  // Render state: either pre-split (flashing) or plain layout
  const [segments, setSegments] = useState<RenderSeg[]>(() =>
    layoutToRender(beforeLayout, new Set()),
  )

  const timeoutRef = useRef<number | undefined>(undefined)
  const initialRoundIndex = frame.status === 'running' ? Math.max(0, rounds.length - 1) : rounds.length
  const initialLayout = frame.status === 'running'
    ? rounds[initialRoundIndex - 1]?.afterLayout ?? beforeLayout
    : frame.status === 'completed'
      ? afterLayout
      : beforeLayout
  const animatedCountRef = useRef(initialRoundIndex)
  const currentLayoutRef = useRef<ContextSegment[]>(initialLayout)

  useEffect(() => {
    const clearTimers = () => {
      if (timeoutRef.current !== undefined) window.clearTimeout(timeoutRef.current)
    }

    if (frame.status === 'error') {
      clearTimers()
      setSegments(layoutToRender(beforeRef.current, new Set()))
      animatedCountRef.current = 0
      currentLayoutRef.current = beforeRef.current
      return
    }

    if (rounds.length === 0) {
      clearTimers()
      setSegments(layoutToRender(afterRef.current, new Set(['summary', 'message'])))
      animatedCountRef.current = 0
      currentLayoutRef.current = afterRef.current
      return
    }

    const hasMeaningfulRounds = rounds.some(r => (r.compactedRanges?.length ?? 0) > 0)
    if (!hasMeaningfulRounds) {
      clearTimers()
      setSegments(layoutToRender(afterRef.current, new Set(['summary', 'message'])))
      currentLayoutRef.current = afterRef.current
      return
    }

    const startIdx = animatedCountRef.current
    const endIdx = rounds.length

    // Already caught up — ensure final static state when compaction is done.
    if (startIdx >= endIdx) {
      if (frame.status === 'completed') {
        clearTimers()
        setSegments(layoutToRender(afterRef.current, new Set()))
        currentLayoutRef.current = afterRef.current
      }
      return
    }

    let roundIdx = startIdx
    let cancelled = false

    const finish = () => {
      animatedCountRef.current = endIdx
      const targetLayout = frame.status === 'completed' ? afterRef.current : currentLayoutRef.current
      setSegments(layoutToRender(targetLayout, new Set()))
      currentLayoutRef.current = targetLayout
    }

    const runRound = () => {
      if (cancelled) return

      if (roundIdx >= endIdx) {
        finish()
        return
      }

      const round = rounds[roundIdx]!
      const ranges = round.compactedRanges ?? []

      // Rounds with no compacted ranges don't need a flashing animation.
      if (ranges.length === 0) {
        currentLayoutRef.current = round.afterLayout
        roundIdx++
        animatedCountRef.current = roundIdx
        runRound()
        return
      }

      const startLayout = currentLayoutRef.current

      // Phase 1: split & flash the portions being compacted.
      setSegments(splitForFlashing(startLayout, ranges))

      timeoutRef.current = window.setTimeout(() => {
        if (cancelled) return
        currentLayoutRef.current = round.afterLayout

        // Phase 2: anneal newly created summary segments.
        const annealingKinds = new Set<ContextKind>(['summary'])
        setSegments(layoutToRender(currentLayoutRef.current, annealingKinds))

        // Phase 3: after compress/rearrange, advance to the next round.
        timeoutRef.current = window.setTimeout(() => {
          if (cancelled) return
          roundIdx++
          animatedCountRef.current = roundIdx
          const delay = roundIdx < endIdx ? 400 : 0
          timeoutRef.current = window.setTimeout(() => {
            if (cancelled) return
            runRound()
          }, delay)
        }, COMPRESS_DURATION + REARRANGE_DURATION)
      }, FLASH_DURATION)
    }

    runRound()

    return () => {
      cancelled = true
      clearTimers()
    }
  }, [frame.status, rounds.length])

  const usedTokens = segments.reduce((s, seg) => s + seg.tokens, 0)
  const freeRatio = Math.max(0, 1 - usedTokens / contextWindowSize)

  if (frame.status === 'error' || frame.error) {
    return (
      <div className="ai-compaction-grid-wrap error">
        <div className="ai-compaction-grid">
          {segments.map((seg, i) => (
            <div
              key={`seg-${i}`}
              className={[
                'ai-compaction-cell',
                `kind-${seg.kind}`,
                seg.flashing ? 'flashing' : '',
                seg.annealing ? 'annealing' : '',
              ].filter(Boolean).join(' ')}
              style={{
                backgroundColor: KIND_COLORS[seg.kind],
                flex: segmentFlex(seg.tokens, contextWindowSize),
              }}
            />
          ))}
          {freeRatio > 0 && (
            <div
              className="ai-compaction-cell free"
              style={{
                backgroundColor: FREE_COLOR,
                flex: Math.max(1, Math.round(freeRatio * CELL_SCALE)),
              }}
            />
          )}
        </div>
        <div className="ai-compaction-error">
          <span className="ai-compaction-error-icon">⚠</span>
          <span>Context compaction failed: {frame.error}</span>
        </div>
      </div>
    )
  }

  return (
    <div className="ai-compaction-grid-wrap">
      <div className="ai-compaction-grid">
        {segments.map((seg, i) => (
          <div
            key={`seg-${i}`}
            className={[
              'ai-compaction-cell',
              `kind-${seg.kind}`,
              seg.flashing ? 'flashing' : '',
              seg.annealing ? 'annealing' : '',
            ].filter(Boolean).join(' ')}
            style={{
              backgroundColor: KIND_COLORS[seg.kind],
              flex: segmentFlex(seg.tokens, contextWindowSize),
            }}
          />
        ))}
        {freeRatio > 0 && (
          <div
            className="ai-compaction-cell free"
            style={{
              backgroundColor: FREE_COLOR,
              flex: Math.max(1, Math.round(freeRatio * CELL_SCALE)),
            }}
          />
        )}
      </div>
      <div className="ai-compaction-grid-labels">
        {(['cold', 'hot', 'summary', 'message'] as const).map(kind => {
          const total = segments.filter(s => s.kind === kind).reduce((s, seg) => s + seg.tokens, 0)
          if (total === 0) return null
          return (
            <span key={kind} className="ai-compaction-grid-label">
              <span className="ai-compaction-grid-dot" style={{ backgroundColor: KIND_COLORS[kind] }} />
              {kindLabel(kind)}
            </span>
          )
        })}
      </div>
    </div>
  )
}

function kindLabel(kind: ContextKind): string {
  switch (kind) {
    case 'cold': return 'System'
    case 'hot': return 'Hot'
    case 'summary': return 'Summary'
    case 'message': return 'Messages'
  }
}
