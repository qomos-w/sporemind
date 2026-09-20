import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ChevronLeft, ChevronRight, RefreshCw } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as agent from '../../../gen-clients/local/client'
import type {
  PromptArtifact,
  PromptContextSegment,
  PromptFragment,
} from '../../../gen-clients/system/types'
import type { InspectRef } from '../../../gen-clients/system/types'
import './AgentPromptPage.css'

interface CompiledPromptState {
  artifact: PromptArtifact | null
  loading: boolean
  error: string
}

function segmentColor(fragment: PromptFragment | null): string {
  if (fragment?.Kind === 'message' && fragment.Name?.toLowerCase().startsWith('user')) {
    return '#60a5fa'
  }
  const colors: Record<string, string> = {
    config: '#f59e0b',
    system: '#7c3aed',
    tool: '#dc2626',
    message: '#2563eb',
    summary: '#ec4899',
    hotcontext: '#059669',
    extra: '#64748b',
    'memory-ontology': '#f59e0b',
    'memory-experience': '#a78bfa',
    'memory-session': '#38bdf8',
  }
  return colors[fragment?.Kind ?? ''] ?? '#64748b'
}

function fragmentFor(
  segment: PromptContextSegment | null,
  fragments: PromptFragment[],
): PromptFragment | null {
  if (!segment?.FragmentId) return null
  return fragments.find(f => f.Id === segment.FragmentId) ?? null
}

export function CompiledPromptPage({ ref }: { ref: InspectRef }) {
  const [selectedSegmentID, setSelectedSegmentID] = useState('')
  const [viewMode, setViewMode] = useState<'content' | 'raw'>('content')
  const [state, setState] = useState<CompiledPromptState>({
    artifact: null,
    loading: false,
    error: '',
  })
  const requestSeq = useRef(0)

  const actorID = ref.Scope?.actor_id ?? ref.Id
  const parentID = ref.Scope?.parent_id
  const isAgentActor = ref.Kind === 'actor_node' && !!ref.Scope?.actor_id
  const isTurnActor = ref.Scope?.actor_type === 'turn' && !!ref.Scope?.actor_id

  const load = useCallback(async () => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setState(prev => ({ ...prev, loading: true, error: '' }))
    try {
      let artifact: PromptArtifact
      if (isTurnActor && parentID) {
        artifact = await agent.compiledPrompt(client, { target: parentID })
      } else if (isAgentActor) {
        artifact = await agent.compiledPrompt(client, { target: actorID })
      } else {
        setState({ artifact: null, loading: false, error: 'Not an agent actor' })
        return
      }
      if (requestSeq.current !== seq) return
      setSelectedSegmentID(artifact.ContextSegments?.[0]?.Id ?? '')
      setState({ artifact, loading: false, error: '' })
    } catch (err) {
      if (requestSeq.current !== seq) return
      setState(prev => ({
        ...prev,
        loading: false,
        error: err instanceof Error ? err.message : String(err),
      }))
    }
  }, [actorID, parentID, isAgentActor, isTurnActor])

  useEffect(() => {
    void load()
  }, [load])

  const artifact = state.artifact
  const segments = artifact?.ContextSegments ?? []
  const totalChars = Math.max(segments.reduce((sum, s) => sum + Math.max(s.Chars, 1), 0), 1)
  const selectedSegment = segments.find(s => s.Id === selectedSegmentID) ?? segments[0] ?? null
  const selectedSegmentIndex = selectedSegment
    ? segments.findIndex(s => s.Id === selectedSegment.Id)
    : -1
  const selectedFragment = selectedSegment
    ? fragmentFor(selectedSegment, artifact?.Fragments ?? [])
    : null
  const selectedContent = selectedFragment?.Content ?? ''

  const rawArtifactText = useMemo(() => {
    if (!selectedSegment) return ''
    const fragment = artifact?.Fragments.find(f => f.Id === selectedSegment.FragmentId)
    return JSON.stringify({ segment: selectedSegment, fragment: fragment ?? null }, null, 2)
  }, [selectedSegment, artifact?.Fragments])

  const selectSegmentOffset = useCallback(
    (offset: number) => {
      if (segments.length === 0) return
      const baseIndex = selectedSegmentIndex >= 0 ? selectedSegmentIndex : 0
      const nextIndex = Math.min(Math.max(baseIndex + offset, 0), segments.length - 1)
      const nextSegment = segments[nextIndex]
      if (nextSegment) setSelectedSegmentID(nextSegment.Id)
    },
    [segments, selectedSegmentIndex],
  )

  return (
    <div className="agent-prompt-page">
      <div className="agent-prompt-toolbar">
        <div className="agent-prompt-artifact-inline">
          <span>Compiled Prompt</span>
          <div className="agent-prompt-context-nav">
            <button
              className="agent-prompt-nav-btn"
              onClick={() => selectSegmentOffset(-1)}
              disabled={selectedSegmentIndex <= 0}
              title="Previous segment"
            >
              <ChevronLeft size={13} />
            </button>
            <strong>
              {selectedSegmentIndex >= 0 ? selectedSegmentIndex + 1 : 0}/{segments.length}
            </strong>
            <button
              className="agent-prompt-nav-btn"
              onClick={() => selectSegmentOffset(1)}
              disabled={selectedSegmentIndex < 0 || selectedSegmentIndex >= segments.length - 1}
              title="Next segment"
            >
              <ChevronRight size={13} />
            </button>
          </div>
          <span>{totalChars} chars</span>
          <div className="agent-prompt-view-toggle">
            <button
              className={viewMode === 'content' ? 'active' : ''}
              onClick={() => setViewMode('content')}
            >
              Content
            </button>
            <button
              className={viewMode === 'raw' ? 'active' : ''}
              onClick={() => setViewMode('raw')}
            >
              Raw
            </button>
          </div>
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
      {state.loading && <div className="agent-prompt-loading">Loading compiled prompt...</div>}

      {segments.length === 0 ? (
        <div className="agent-prompt-empty">
          {isTurnActor
            ? 'No compiled messages available for this turn'
            : 'No compiled messages available'}
        </div>
      ) : (
        <>
          <div className="agent-prompt-context-bar">
            {segments.map(segment => {
              const fragment = fragmentFor(segment, artifact?.Fragments ?? [])
              return (
                <button
                  key={segment.Id}
                  className={`agent-prompt-segment${segment.Id === selectedSegment?.Id ? ' selected' : ''}`}
                  style={{
                    flexBasis: 7,
                    flexGrow: Math.max(segment.Chars, 1),
                    flexShrink: 0,
                    background: segmentColor(fragment),
                  }}
                  title={`${fragment?.Name ?? ''} · ${segment.Chars} chars`}
                  onClick={() => setSelectedSegmentID(segment.Id)}
                />
              )
            })}
          </div>

          {selectedSegment && (
            <div className="agent-prompt-selected">
              <div className="agent-prompt-selected-header">
                <span className="agent-prompt-card-title">{selectedFragment?.Name ?? ''}</span>
                <span className="agent-prompt-kind">{selectedSegment.Chars} chars</span>
              </div>
              {selectedFragment?.Kind && (
                <div className="agent-prompt-meta agent-prompt-selected-meta">
                  <span>
                    <strong>kind</strong> {selectedFragment.Kind}
                  </span>
                  <span>
                    <strong>source</strong> {selectedFragment.Source}
                  </span>
                  {selectedFragment.SourceRange && (
                    <span>
                      <strong>range</strong> {selectedFragment.SourceRange}
                    </span>
                  )}
                </div>
              )}
              {viewMode === 'content' ? (
                selectedContent ? (
                  <pre className="agent-prompt-block-content">{selectedContent}</pre>
                ) : (
                  <div className="agent-prompt-empty-inline">No content for this segment</div>
                )
              ) : (
                <div className="agent-prompt-raw-frame">
                  <pre>{rawArtifactText}</pre>
                </div>
              )}
            </div>
          )}
        </>
      )}
    </div>
  )
}
