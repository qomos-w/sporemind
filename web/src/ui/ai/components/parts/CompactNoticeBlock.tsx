import React from 'react'
import { Zap, Terminal } from 'lucide-react'
import type { CompactionFrame, CompactionRound, TimelineConnectorMode } from '../../model/frame-types.ts'
import { TimelineStep, formatTokenCount } from '../timeline'
import { CompactionGrid } from './CompactionGrid.tsx'

interface CompactNoticeBlockProps {
  frame: CompactionFrame
  connectorMode?: TimelineConnectorMode
}

export function roundLabel(round: CompactionRound): string {
  const { round: num, level, sourceStartIndex, sourceEndIndex } = round
  if (level <= 1) {
    return `Round ${num}: summarize ${sourceStartIndex}-${sourceEndIndex} (L${level})`
  }
  return `Round ${num}: merge L${level - 1} → L${level}`
}

function CompactionRounds({ frame }: { frame: CompactionFrame }) {
  const meaningful = frame.rounds.filter(r => (r.compactedRanges?.length ?? 0) > 0)
  if (meaningful.length === 0) return null

  const lastRoundIndex = meaningful.length - 1
  const isRunning = frame.status === 'running'

  return (
    <div className="ai-compaction-rounds">
      {meaningful.map((r, idx) => (
        <div
          key={r.round}
          className={[
            'ai-compaction-round',
            isRunning && idx === lastRoundIndex ? 'ai-compaction-round-active' : '',
          ].filter(Boolean).join(' ')}
        >
          {roundLabel(r)}
        </div>
      ))}
    </div>
  )
}

export const CompactNoticeBlock: React.FC<CompactNoticeBlockProps> = ({ frame, connectorMode = 'none' }) => {
  const isAuto = frame.trigger === 'auto'
  const saved = Math.max(0, frame.beforeTokens - frame.afterTokens)
  const savedPercent = frame.beforeTokens > 0 ? Math.round((saved / frame.beforeTokens) * 100) : 0

  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={
        isAuto
          ? <Zap size={12} className="ai-step-icon" />
          : <Terminal size={12} className="ai-step-icon" />
      }
      label={
        <span className="ai-step-label">
          {frame.status === 'completed'
            ? 'Context compressed'
            : frame.status === 'error'
              ? 'Compression failed'
              : 'Compressing…'}
          <span className={`ai-compaction-badge ${isAuto ? 'auto' : 'user'}`}>
            {isAuto ? 'auto' : '/compact'}
          </span>
        </span>
      }
      connectorMode={connectorMode}
      alwaysVisible={
        <div className="ai-compaction-body">
          <CompactionGrid frame={frame} />
          <CompactionRounds frame={frame} />
          <div className="ai-compaction-stats">
            <span className="ai-compaction-tokens">
              {frame.rounds.length === 0
                ? `${formatTokenCount(frame.beforeTokens)} tokens`
                : `${formatTokenCount(frame.beforeTokens)} → ${formatTokenCount(frame.afterTokens)} tokens`
              }
            </span>
            {frame.rounds.length > 0 && saved > 0 && (
              <span className="ai-compaction-saved">-{formatTokenCount(saved)} ({savedPercent}%)</span>
            )}

          </div>
        </div>
      }
    />
  )
}
