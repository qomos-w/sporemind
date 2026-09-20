import { useCallback, useEffect, useRef, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as agent from '../../../gen-clients/local/client'
import type { TurnContextBudgetPayload } from '../../../gen-clients/system/types'
import type { InspectRef } from '../../../gen-clients/system/types'
import './AgentPromptPage.css'

function formatTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return String(n)
}

export function ContextBudgetPage({ ref }: { ref: InspectRef }) {
  const [budget, setBudget] = useState<TurnContextBudgetPayload | null>(null)
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
      const resp = await agent.contextBudget(client, { target: actorID })
      if (requestSeq.current !== seq) return
      setBudget(resp)
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

  const estimated = budget?.EstimatedTokens ?? 0
  const windowSize = budget?.ContextWindowSize ?? 0
  const tokenBudget = budget?.TokenBudget ?? 0

  const usageRatio = windowSize > 0 ? Math.min(1, estimated / windowSize) : 0
  const budgetRatio = windowSize > 0 && tokenBudget > 0
    ? Math.min(1, tokenBudget / windowSize)
    : 0

  return (
    <div className="agent-prompt-page">
      <div className="agent-prompt-toolbar">
        <div className="agent-prompt-artifact-inline">
          <span>Context Budget</span>
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
      {loading && <div className="agent-prompt-loading">Loading context budget…</div>}

      {!loading && !error && (
        <div className="agent-prompt-selected">
          <div className="agent-prompt-selected-header">
            <span className="agent-prompt-card-title">Token Usage</span>
            <span className="agent-prompt-kind">
              {formatTokens(estimated)} / {windowSize > 0 ? formatTokens(windowSize) : '—'}
            </span>
          </div>

          {windowSize > 0 && (
            <div className="ctx-budget-full-bar">
              <div className="ctx-budget-full-window" style={{ width: '100%' }}>
                <div className="ctx-budget-full-used" style={{ width: `${usageRatio * 100}%` }} />
              </div>
              {budgetRatio > 0 && (
                <div
                  className="ctx-budget-full-marker"
                  style={{ left: `${budgetRatio * 100}%` }}
                  title={`compaction at ${formatTokens(tokenBudget)}`}
                />
              )}
            </div>
          )}

          <div className="agent-prompt-meta agent-prompt-selected-meta">
            <span><strong>estimated</strong> {formatTokens(estimated)}</span>
            <span><strong>window</strong> {windowSize > 0 ? formatTokens(windowSize) : '—'}</span>
            <span><strong>budget</strong> {tokenBudget > 0 ? formatTokens(tokenBudget) : '—'}</span>
          </div>

          {windowSize === 0 && estimated === 0 && (
            <div className="agent-prompt-empty-inline">No context budget data available</div>
          )}
        </div>
      )}
    </div>
  )
}
