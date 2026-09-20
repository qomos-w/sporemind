import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { RefreshCw, ChevronRight, ChevronDown } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as agentsession from '../../../gen-clients/local/client'
import type { Turn as SessionTurn, Step } from '../../../gen-clients/system/types'
import type { InspectRef } from '../../../gen-clients/system/types'

interface TurnHistoryState {
  turns: SessionTurn[]
  steps: Step[]
  loading: boolean
  error: string
}

function roleClass(role: string) {
  return role === 'user' ? 'turn-role-user' : role === 'assistant' ? 'turn-role-assistant' : 'turn-role-other'
}

function stateClass(state?: string) {
  switch (state) {
    case 'completed': return 'turn-state-completed'
    case 'failed': return 'turn-state-failed'
    case 'cancelled': return 'turn-state-cancelled'
    case 'abandoned': return 'turn-state-abandoned'
    default: return 'turn-state-pending'
  }
}

function stepToolName(step: Step): string | undefined {
  const toolUse = (step.Content ?? []).find(b => b.Type === 'tool_use')
  return toolUse?.ToolName
}

function stepSummary(step: Step): string {
  const blocks = step.Content ?? []
  const first = blocks.find(b => b.Type === 'text' && b.Text)
  if (first?.Text) return first.Text.slice(0, 120)
  const toolUse = blocks.find(b => b.Type === 'tool_use')
  if (toolUse) {
    if (toolUse.Input) return (formatJson(toolUse.Input).split('\n')[0] ?? '').slice(0, 100)
    return step.Type
  }
  const toolResult = blocks.find(b => b.Type === 'tool_result' || b.Type === 'memory_result')
  if (toolResult) return `result: ${toolResult.Text ?? ''}`.slice(0, 120)
  return step.Type
}

function stepBadgeClass(step: Step): string {
  switch (step.Type) {
    case 'text': return 'turn-role-assistant'
    case 'tool_use': return 'turn-role-user'
    case 'tool_result': return 'turn-role-other'
    default: return 'turn-role-other'
  }
}

// stepDuration computes the wall-clock duration of a step from its
// StartedAt/CompletedAt timestamps. Returns 'unknown' when either timestamp
// is missing (e.g. a still-running step, or historical data that predates
// the stamping). No value is fabricated — the frontend renders 'unknown'
// verbatim so the inspector never lies about timing.
function stepDuration(step: Step): string {
  if (!step.StartedAt || !step.CompletedAt) {
    return 'unknown'
  }
  const start = Date.parse(step.StartedAt)
  const end = Date.parse(step.CompletedAt)
  if (Number.isNaN(start) || Number.isNaN(end) || end < start) {
    return 'unknown'
  }
  const seconds = (end - start) / 1000
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 2 : 1)}s`
  const minutes = Math.floor(seconds / 60)
  const remaining = Math.round(seconds % 60)
  return remaining > 0 ? `${minutes}m ${remaining}s` : `${minutes}m`
}

function formatJson(value: string | undefined): string {
  if (!value) return ''
  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch {
    return value
  }
}

function StepDetail({ step }: { step: Step }) {
  const blocks = step.Content ?? []
  return (
    <div className="turn-step-detail">
      {step.ReasoningContent && (
        <div className="turn-step-detail-block turn-step-detail-block--reasoning">
          <div className="turn-step-detail-block-header">
            <span className="turn-step-detail-block-type">reasoning</span>
          </div>
          <pre className="turn-step-detail-block-content">{step.ReasoningContent}</pre>
        </div>
      )}
      {step.Error && (
        <div className="turn-step-detail-block turn-step-detail-block--error">
          <div className="turn-step-detail-block-header">
            <span className="turn-step-detail-block-type">error</span>
          </div>
          <div className="turn-step-detail-block-error">{step.Error}</div>
        </div>
      )}
      {blocks.length === 0 && !step.ReasoningContent && !step.Error && (
        <div className="turn-step-detail-empty">No content</div>
      )}
      {blocks.map((block, idx) => (
        <div key={block.Id ?? idx} className={`turn-step-detail-block turn-step-detail-block--${block.Type}`}>
          <div className="turn-step-detail-block-header">
            <span className="turn-step-detail-block-type">{block.Type}</span>
            {block.ToolName && <span className="turn-step-detail-block-name">{block.ToolName}</span>}
            {block.ToolUseId && <span className="turn-step-detail-block-meta">id: {block.ToolUseId}</span>}
          </div>
          {block.Type === 'tool_use' && block.Input && (
            <pre className="turn-step-detail-block-content">{formatJson(block.Input)}</pre>
          )}
          {block.Text && (
            <pre className="turn-step-detail-block-content">{block.Text}</pre>
          )}
          {block.IsError && <div className="turn-step-detail-block-error">Error</div>}
        </div>
      ))}
      {(step.StartedAt || step.CompletedAt) && (
        <div className="turn-step-detail-meta">
          {step.StartedAt && <span className="turn-step-detail-started">started: {step.StartedAt}</span>}
          {step.CompletedAt && <span className="turn-step-detail-completed">completed: {step.CompletedAt}</span>}
          <span className="turn-step-detail-duration">duration: {stepDuration(step)}</span>
        </div>
      )}
      {step.Usage && (
        <div className="turn-step-detail-usage">
          tokens: {step.Usage.InputTokens ?? 0} in / {step.Usage.OutputTokens ?? 0} out
        </div>
      )}
    </div>
  )
}

export function TurnHistoryPage({ ref }: { ref: InspectRef }) {
  const [state, setState] = useState<TurnHistoryState>({
    turns: [],
    steps: [],
    loading: false,
    error: '',
  })
  const [expandedTurns, setExpandedTurns] = useState<Set<string>>(new Set())
  const [expandedSteps, setExpandedSteps] = useState<Set<string>>(new Set())
  const requestSeq = useRef(0)

  const actorID = ref.Scope?.actor_id ?? ref.Id
  const parentID = ref.Scope?.parent_id
  const isTurnActor = ref.Scope?.actor_type === 'turn' && !!ref.Scope?.actor_id

  const load = useCallback(async () => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setState(prev => ({ ...prev, loading: true, error: '' }))
    try {
      const targetID = (isTurnActor && parentID) ? parentID : actorID
      const resp = await agentsession.sessionFork(client, {}, { target: targetID })
      if (requestSeq.current !== seq) return
      setState({
        turns: resp.Session.Turns ?? [],
        steps: resp.Steps ?? [],
        loading: false,
        error: '',
      })
    } catch (err) {
      if (requestSeq.current !== seq) return
      setState(prev => ({
        ...prev,
        loading: false,
        error: err instanceof Error ? err.message : String(err),
      }))
    }
  }, [actorID, parentID, isTurnActor])

  useEffect(() => {
    void load()
  }, [load])

  // Default folding: assistant turns collapsed, user turns expanded.
  useEffect(() => {
    setExpandedTurns(prev => {
      const next = new Set(prev)
      for (const turn of state.turns) {
        if (!turn.Id) continue
        if (turn.Role !== 'assistant') {
          next.add(turn.Id)
        }
      }
      return next
    })
  }, [state.turns])

  const stepsByTurn = useMemo(() => {
    const map = new Map<string, Step[]>()
    for (const step of state.steps) {
      if (!step.TurnId) continue
      const list = map.get(step.TurnId) ?? []
      list.push(step)
      map.set(step.TurnId, list)
    }
    for (const list of map.values()) {
      list.sort((a, b) => (a.Seq ?? 0) - (b.Seq ?? 0))
    }
    return map
  }, [state.steps])

  const toggleTurn = useCallback((turnId: string) => {
    setExpandedTurns(prev => {
      const next = new Set(prev)
      if (next.has(turnId)) next.delete(turnId)
      else next.add(turnId)
      return next
    })
  }, [])

  const toggleStep = useCallback((stepId: string) => {
    setExpandedSteps(prev => {
      const next = new Set(prev)
      if (next.has(stepId)) next.delete(stepId)
      else next.add(stepId)
      return next
    })
  }, [])

  const hasSteps = (turn: SessionTurn) => turn.Role === 'assistant' && (stepsByTurn.get(turn.Id ?? '')?.length ?? 0) > 0

  return (
    <div className="turn-history-page">
      <div className="turn-history-toolbar">
        <span className="turn-history-count">{state.turns.length} turns</span>
        <button className="turn-history-refresh-btn" onClick={() => void load()} disabled={state.loading} title="Refresh">
          <RefreshCw size={13} />
        </button>
      </div>

      {state.error && <div className="turn-history-error">{state.error}</div>}
      {state.loading && <div className="turn-history-loading">Loading turns…</div>}

      {state.turns.length === 0 ? (
        !state.loading && !state.error && <div className="turn-history-empty">No turns in session</div>
      ) : (
        <div className="turn-history-scroll">
          <div className="turn-history-list">
            {state.turns.map((turn, i) => {
              const turnId = turn.Id ?? `turn-${i}`
              const isExpanded = expandedTurns.has(turnId)
              const steps = stepsByTurn.get(turn.Id ?? '') ?? []
              const turnHasSteps = hasSteps(turn)
              return (
                <div key={turnId} className={`turn-history-item ${roleClass(turn.Role)}`}>
                  <button
                    type="button"
                    className={`turn-history-item-header ${turnHasSteps ? 'turn-history-item-header--clickable' : ''}`}
                    onClick={() => turnHasSteps && toggleTurn(turnId)}
                    disabled={!turnHasSteps}
                    aria-expanded={isExpanded}
                  >
                    {turnHasSteps && (
                      <span className="turn-history-toggle-icon">
                        {isExpanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                      </span>
                    )}
                    <span className={`turn-role-badge ${roleClass(turn.Role)}`}>{turn.Role}</span>
                    <span className={`turn-state-badge ${stateClass(turn.State)}`}>{turn.State ?? 'unknown'}</span>
                    {turn.Cancelled && <span className="turn-cancelled-badge">cancelled</span>}
                    <span className="turn-history-step-count">{steps.length} steps</span>
                  </button>

                  {isExpanded && (
                    <div className="turn-history-item-body">
                      {turn.UserInput && (
                        <div className="turn-user-input">{turn.UserInput}</div>
                      )}
                      {turn.Error && (
                        <div className="turn-error">{turn.Error}</div>
                      )}
                      {turn.Role === 'assistant' && steps.length > 0 && (
                        <div className="turn-steps">
                          {steps.map(step => {
                            const stepExpanded = expandedSteps.has(step.Id ?? '')
                            return (
                              <div key={step.Id} className={`turn-step ${stepBadgeClass(step)}`}>
                                <button
                                  type="button"
                                  className="turn-step-header"
                                  onClick={() => toggleStep(step.Id ?? '')}
                                  aria-expanded={stepExpanded}
                                >
                                  <span className="turn-step-toggle-icon">
                                    {stepExpanded ? <ChevronDown size={11} /> : <ChevronRight size={11} />}
                                  </span>
                                  <span className="turn-step-type">{step.Type}</span>
                                  {stepToolName(step) && <span className="turn-step-tool">{stepToolName(step)}</span>}
                                  {step.Model && <span className="turn-step-model">{step.Model}</span>}
                                  <span className="turn-step-duration" title={step.StartedAt ? `started: ${step.StartedAt}${step.CompletedAt ? `\ncompleted: ${step.CompletedAt}` : ''}` : undefined}>{stepDuration(step)}</span>
                                  <span className="turn-step-summary">{stepSummary(step)}</span>
                                </button>
                                {stepExpanded && <StepDetail step={step} />}
                              </div>
                            )
                          })}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
