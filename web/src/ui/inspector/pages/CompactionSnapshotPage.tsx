import { useCallback, useEffect, useRef, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as agent from '../../../gen-clients/local/client'
import type {
  PromptArtifact,
  SummarySegment,
} from '../../../gen-clients/system/types'
import type { InspectRef } from '../../../gen-clients/system/types'
import './AgentPromptPage.css'

function formatTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return String(n)
}

export function CompactionSnapshotPage({ ref }: { ref: InspectRef }) {
  const [artifact, setArtifact] = useState<PromptArtifact | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const requestSeq = useRef(0)

  const actorID = ref.Scope?.actor_id ?? ref.Id

  const load = useCallback(async () => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setLoading(true)
    setError('')
    try {
      const art = await agent.promptArtifact(client, { target: actorID })
      if (requestSeq.current !== seq) return
      setArtifact(art)
      setLoading(false)
    } catch (err) {
      if (requestSeq.current !== seq) return
      setError(err instanceof Error ? err.message : String(err))
      setLoading(false)
    }
  }, [actorID])

  useEffect(() => {
    void load()
  }, [load])

  const segments: SummarySegment[] = artifact?.SummarySegments ?? []
  const windowSize = artifact?.ContextWindowSize ?? 0
  const tokenBudget = artifact?.TokenBudget ?? 0

  const budgetRatio = windowSize > 0 && tokenBudget > 0
    ? Math.min(1, tokenBudget / windowSize)
    : 0

  return (
    <div className="agent-prompt-page">
      <div className="agent-prompt-toolbar">
        <div className="agent-prompt-artifact-inline">
          <span>Compaction Snapshot</span>
          {segments.length > 0 && <span>{segments.length} summaries</span>}
        </div>
        <div className="agent-prompt-actions">
          <button
            className="agent-prompt-icon-btn"
            onClick={() => void load()}
            disabled={loading}
            title="Refresh"
          >
            <RefreshCw size={13} />
          </button>
        </div>
      </div>

      {error && <div className="agent-prompt-error">{error}</div>}
      {loading && <div className="agent-prompt-loading">Loading compaction data…</div>}

      {!loading && !error && (
        <div className="agent-prompt-scroll">
          <div className="agent-prompt-selected">
            <div className="agent-prompt-selected-header">
              <span className="agent-prompt-card-title">Context Window</span>
            </div>
            <div className="agent-prompt-meta agent-prompt-selected-meta">
              <span><strong>window</strong> {windowSize > 0 ? formatTokens(windowSize) : '—'}</span>
              <span><strong>budget</strong> {tokenBudget > 0 ? formatTokens(tokenBudget) : '—'}</span>
              {budgetRatio > 0 && (
                <div className="ctx-budget-mini-bar">
                  <div className="ctx-budget-mini-fill" style={{ width: `${budgetRatio * 100}%` }} />
                </div>
              )}
            </div>
          </div>

          {segments.length === 0 ? (
            <div className="agent-prompt-empty">No compaction summaries yet</div>
          ) : (
            <div className="turn-history-list">
              {segments.map((seg, i) => (
                <div key={i} className="turn-history-item">
                  <div className="turn-history-item-header">
                    <span className="turn-role-badge turn-role-other">L{seg.Level}</span>
                    <span className="turn-state-badge turn-state-completed">
                      {formatTokens(seg.InputTokens)} → {formatTokens(seg.OutputTokens)}
                    </span>
                    {seg.CreatedAt && (
                      <span className="turn-cancelled-badge">{new Date(seg.CreatedAt).toLocaleString()}</span>
                    )}
                  </div>
                  <pre className="agent-prompt-block-content">{seg.Text}</pre>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
