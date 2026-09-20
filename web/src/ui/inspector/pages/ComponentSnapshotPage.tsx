import { useCallback, useEffect, useRef, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as agentcomponent from '../../../gen-clients/local/client'
import type {
  AgentComponentSnapshot,
  AgentComponentSnapshotResp,
} from '../../../gen-clients/system/types'
import type { InspectRef } from '../../../gen-clients/system/types'
import './AgentPromptPage.css'

interface ComponentState {
  snapshot: AgentComponentSnapshot | null
  loading: boolean
  error: string
}

export function ComponentSnapshotPage({ ref }: { ref: InspectRef }) {
  const [state, setState] = useState<ComponentState>({
    snapshot: null,
    loading: false,
    error: '',
  })
  const requestSeq = useRef(0)

  const actorID = ref.Scope?.actor_id ?? ref.Id

  const load = useCallback(async () => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setState(prev => ({ ...prev, loading: true, error: '' }))
    try {
      const resp: AgentComponentSnapshotResp = await agentcomponent.componentSnapshot(client, {}, { target: actorID })
      if (requestSeq.current !== seq) return
      setState({ snapshot: resp.Snapshot, loading: false, error: '' })
    } catch (err) {
      if (requestSeq.current !== seq) return
      setState(prev => ({
        ...prev,
        loading: false,
        error: err instanceof Error ? err.message : String(err),
      }))
    }
  }, [actorID])

  useEffect(() => {
    void load()
  }, [load])

  const snap = state.snapshot

  return (
    <div className="agent-prompt-page">
      <div className="agent-prompt-toolbar">
        <div className="agent-prompt-artifact-inline">
          <span>Components</span>
          {snap && <span>{snap.Mounts?.length ?? 0} mounted</span>}
        </div>
        <div className="agent-prompt-actions">
          <button
            className="agent-prompt-icon-btn"
            onClick={() => void load()}
            disabled={state.loading}
            title="Refresh"
          >
            <RefreshCw size={13} />
          </button>
        </div>
      </div>

      {state.error && <div className="agent-prompt-error">{state.error}</div>}
      {state.loading && <div className="agent-prompt-loading">Loading components…</div>}

      {!state.loading && !state.error && (
        <div className="agent-prompt-scroll">
          {(snap?.Mounts?.length ?? 0) === 0 && (
            <div className="agent-prompt-empty">No components mounted</div>
          )}

          {(snap?.Mounts?.length ?? 0) > 0 && (
            <div className="turn-history-list">
              {snap!.Mounts.map((m) => (
                <div key={m.MountId} className={`turn-history-item ${m.Enabled === false ? 'turn-state-pending' : 'turn-role-assistant'}`}>
                  <div className="turn-history-item-header">
                    <span className={`turn-role-badge ${m.Enabled === false ? 'turn-role-other' : 'turn-role-assistant'}`}>
                      {m.Kind ?? 'component'}
                    </span>
                    {m.Enabled === false && <span className="turn-cancelled-badge">disabled</span>}
                    {m.Version && <span className="turn-state-badge turn-state-completed">v{m.Version}</span>}
                  </div>
                  <div className="turn-user-input">{m.Title ?? m.CardId}</div>
                  <div className="agent-prompt-meta agent-prompt-selected-meta">
                    <span><strong>card</strong> {m.CardId}</span>
                    {m.Scope && <span><strong>scope</strong> {m.Scope}</span>}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
