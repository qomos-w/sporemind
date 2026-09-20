import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ChevronLeft, ChevronRight, RefreshCw } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as agent from '../../../gen-clients/local/client'
import * as promptCards from '../../../application/prompt-card-store'
import type {
  PromptArtifact,
  PromptContextSegment,
  PromptFragment,
  PromptProfile,
} from '../../../gen-types/prompt'
import type { InspectRef } from '../../../gen-clients/system/types'
import './AgentPromptPage.css'

interface PromptPageState {
  registeredFragments: PromptFragment[]
  artifact: PromptArtifact | null
  profile: PromptProfile | null
  loading: boolean
  error: string
}

function segmentColor(fragment: PromptFragment | null): string {
  if (fragment?.Kind === 'message' && fragment.Name?.toLowerCase().startsWith('user')) {
    return '#60a5fa'
  }
  const colors: Record<string, string> = {
    system: '#7c3aed',
    instructions: '#a855f7',
    role: '#0ea5e9',
    environment: '#06b6d4',
    git: '#84cc16',
    mounts: '#b45309',
    intent: '#f97316',
    config: '#f59e0b',
    tools: '#b91c1c',
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
  fragments: PromptFragment[]
): PromptFragment | null {
  if (!segment?.FragmentId) return null
  return fragments.find(f => f.Id === segment.FragmentId) ?? null
}

function segmentContent(
  segment: PromptContextSegment | null,
  artifact: PromptArtifact | null
): string {
  const fragment = fragmentFor(segment, artifact?.Fragments ?? [])
  return fragment?.Content ?? ''
}

function segmentMeta(fragment: PromptFragment | null): Array<[string, string | number | boolean | undefined]> {
  if (!fragment) return []
  const values: Array<[string, string | number | boolean | undefined]> = [
    ['kind', fragment.Kind],
    ['priority', fragment.Priority],
    ['source', fragment.Source],
    ['path', fragment.Path],
    ['editable', fragment.Editable],
    ['range', fragment.SourceRange],
  ]
  return values.filter(([, value]) => value !== undefined && value !== '')
}

export function AgentPromptPage({ ref }: { ref: InspectRef }) {
  const [selectedSegmentID, setSelectedSegmentID] = useState('')
  const [viewMode, setViewMode] = useState<'content' | 'raw'>('content')
  const [editMode, setEditMode] = useState(false)
  const [editContent, setEditContent] = useState('')
  const [saving, setSaving] = useState(false)
  const [state, setState] = useState<PromptPageState>({
    registeredFragments: [],
    artifact: null,
    profile: null,
    loading: false,
    error: '',
  })
  const requestSeq = useRef(0)

  const role = ref.Scope?.role ?? ref.Scope?.agent_kind ?? ref.Scope?.actor_type ?? ''
  const actorID = ref.Scope?.actor_id ?? ref.Id
  const parentID = ref.Scope?.parent_id
  const isAgentActor = ref.Kind === 'actor_node' && !!ref.Scope?.actor_id
  const isTurnActor = ref.Scope?.actor_type === 'turn' && !!ref.Scope?.actor_id

  const load = useCallback(async () => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setState(prev => ({ ...prev, loading: true, error: '' }))
    try {
      if (isTurnActor && parentID) {
        const artifact = await agent.promptArtifact(client, { target: parentID })
        if (requestSeq.current !== seq) return
        setSelectedSegmentID(artifact.ContextSegments?.[0]?.Id ?? '')
        setState({ registeredFragments: [], artifact, profile: null, loading: false, error: '' })
        return
      }

      if (isAgentActor) {
        const artifact = await agent.promptArtifact(client, { target: actorID })
        if (requestSeq.current !== seq) return
        setSelectedSegmentID(artifact.ContextSegments?.[0]?.Id ?? '')
        setState({ registeredFragments: [], artifact, profile: null, loading: false, error: '' })
        return
      }

      const fallbackQuery = {
        intent: '',
        scope: actorID,
        role,
      }

      const [registeredFragmentsResp, artifact, profile] = await Promise.all([
        promptCards.listFragments(client, { Scope: fallbackQuery.scope, Role: fallbackQuery.role }),
        promptCards.getArtifact(client, { Intent: fallbackQuery.intent, Scope: fallbackQuery.scope, Role: fallbackQuery.role }),
        promptCards.getProfile(client, { Scope: fallbackQuery.scope, Role: fallbackQuery.role }),
      ])
      const registeredFragments = registeredFragmentsResp.Items
      if (requestSeq.current !== seq) return
      setSelectedSegmentID(artifact.ContextSegments?.[0]?.Id ?? '')
      setState({ registeredFragments, artifact, profile, loading: false, error: '' })
    } catch (err) {
      if (requestSeq.current !== seq) return
      setState(prev => ({
        ...prev,
        loading: false,
        error: err instanceof Error ? err.message : String(err),
      }))
    }
  }, [actorID, parentID, role, isAgentActor, isTurnActor])

  useEffect(() => {
    void load()
  }, [load])

  const artifact = state.artifact
  const segments = artifact?.ContextSegments ?? []
  const totalChars = Math.max(segments.reduce((sum, segment) => sum + Math.max(segment.Chars, 1), 0), 1)
  const selectedSegment = segments.find(segment => segment.Id === selectedSegmentID) ?? segments[0] ?? null
  const selectedSegmentIndex = selectedSegment ? segments.findIndex(segment => segment.Id === selectedSegment.Id) : -1
  const selectedContent = segmentContent(selectedSegment, artifact)
  const rawArtifactText = useMemo(() => {
    if (!selectedSegment) return ''
    const fragment = artifact?.Fragments.find(f => f.Id === selectedSegment.FragmentId)
    return JSON.stringify({
      segment: selectedSegment,
      fragment: fragment ?? null,
    }, null, 2)
  }, [selectedSegment, artifact?.Fragments])

  const selectSegmentOffset = useCallback((offset: number) => {
    if (segments.length === 0) return
    const baseIndex = selectedSegmentIndex >= 0 ? selectedSegmentIndex : 0
    const nextIndex = Math.min(Math.max(baseIndex + offset, 0), segments.length - 1)
    const nextSegment = segments[nextIndex]
    if (nextSegment) setSelectedSegmentID(nextSegment.Id)
  }, [segments, selectedSegmentIndex])

  const selectedFragment = selectedSegment
    ? artifact?.Fragments.find(f => f.Id === selectedSegment.FragmentId) ?? null
    : null

  const enterEdit = useCallback(() => {
    setEditContent(selectedContent)
    setEditMode(true)
  }, [selectedContent])

  const cancelEdit = useCallback(() => {
    setEditMode(false)
    setEditContent('')
  }, [])

  const saveEdit = useCallback(async () => {
    if (!selectedFragment) return
    setSaving(true)
    try {
      await promptCards.saveFragment(client, {
        Key: selectedFragment.Key || '',
        Name: selectedFragment.Name,
        Kind: selectedFragment.Kind,
        Priority: selectedFragment.Priority,
        Content: editContent,
        Scope: selectedFragment.Scope ?? '',
        Role: selectedFragment.Role ?? '',
      })
      setEditMode(false)
      await load()
    } catch (err) {
      setState(prev => ({
        ...prev,
        error: err instanceof Error ? err.message : String(err),
      }))
    } finally {
      setSaving(false)
    }
  }, [selectedFragment, editContent, load])

  return (
    <div className="agent-prompt-page">
      <div className="agent-prompt-toolbar">
        <div className="agent-prompt-artifact-inline">
          <span>Prompt Context Snapshot</span>
          <div className="agent-prompt-context-nav">
            <button
              className="agent-prompt-nav-btn"
              onClick={() => selectSegmentOffset(-1)}
              disabled={selectedSegmentIndex <= 0}
              title="Select previous block"
            >
              <ChevronLeft size={13} />
            </button>
            <strong>{selectedSegmentIndex >= 0 ? selectedSegmentIndex + 1 : 0}/{segments.length}</strong>
            <button
              className="agent-prompt-nav-btn"
              onClick={() => selectSegmentOffset(1)}
              disabled={selectedSegmentIndex < 0 || selectedSegmentIndex >= segments.length - 1}
              title="Select next block"
            >
              <ChevronRight size={13} />
            </button>
          </div>
          <span>{totalChars} chars</span>
          <div className="agent-prompt-view-toggle">
            <button
              className={viewMode === 'content' ? 'active' : ''}
              onClick={() => setViewMode('content')}
            >Content</button>
            <button
              className={viewMode === 'raw' ? 'active' : ''}
              onClick={() => setViewMode('raw')}
            >Raw</button>
          </div>
        </div>
        <div className="agent-prompt-actions">
          {!editMode && selectedFragment?.Editable && (
            <button className="agent-prompt-icon-btn" onClick={enterEdit} title="Edit fragment">
              Edit
            </button>
          )}
          <button className="agent-prompt-icon-btn" onClick={() => void load()} disabled={state.loading} title="Refresh">
            <RefreshCw size={13} />
          </button>
        </div>
      </div>

      {state.error && <div className="agent-prompt-error">{state.error}</div>}
      {state.loading && <div className="agent-prompt-loading">Loading context...</div>}

      {segments.length === 0 ? (
        <div className="agent-prompt-empty">No context segments returned</div>
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
              {(() => {
                const fragment = fragmentFor(selectedSegment, artifact?.Fragments ?? [])
                return (
                  <>
                    <div className="agent-prompt-selected-header">
                      <span className="agent-prompt-card-title">{fragment?.Name ?? ''}</span>
                      <span className="agent-prompt-kind">{selectedSegment.Chars} chars</span>
                    </div>
                    <div className="agent-prompt-meta agent-prompt-selected-meta">
                      {segmentMeta(fragment).map(([label, value]) => (
                        <span key={label}><strong>{label}</strong> {String(value)}</span>
                      ))}
                    </div>
                  </>
                )
              })()}
              {editMode ? (
                <>
                  <textarea
                    className="agent-prompt-editor"
                    value={editContent}
                    onChange={e => setEditContent(e.target.value)}
                    rows={12}
                  />
                  <div className="agent-prompt-edit-actions">
                    <button className="agent-prompt-save-btn" onClick={() => void saveEdit()} disabled={saving}>
                      {saving ? 'Saving...' : 'Save'}
                    </button>
                    <button className="agent-prompt-cancel-btn" onClick={cancelEdit} disabled={saving}>Cancel</button>
                  </div>
                </>
              ) : viewMode === 'content' ? (
                selectedContent ? (
                  <pre className="agent-prompt-block-content">{selectedContent}</pre>
                ) : (
                  <div className="agent-prompt-empty-inline">No content captured for this block</div>
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
