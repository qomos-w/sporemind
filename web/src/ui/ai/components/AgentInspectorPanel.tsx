import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Eye, Pencil, FileText, MessageSquare, Activity, Settings, Database, Terminal, BarChart3, Layers, Send } from 'lucide-react'
import type { InspectRef, InspectDocument, InspectSection } from '../../../gen-clients/system/types'
import { fetchInspectorDocument } from '../../inspector/inspectorApi'
import { InspectContext } from '../../inspector/inspectContext'
import { getInspectorComponent } from '../../inspector/inspectorRegistry'
import { KVSection } from '../../inspector/sections/KVSection'
import { ListSection } from '../../inspector/sections/ListSection'
import { MarkdownSection } from '../../inspector/sections/MarkdownSection'
import { CapabilityTriangleSection } from '../../inspector/sections/CapabilityTriangleSection'
import { ContextBarSection } from '../../inspector/sections/ContextBarSection'
import { openWorkbenchTarget } from '../../../application/open-service'
import './AgentInspectorPanel.css'
import '../../inspector/InspectorDock.css'

const iconMap: Record<string, React.ReactNode> = {
  FileText: <FileText size={14} />,
  MessageSquare: <MessageSquare size={14} />,
  Activity: <Activity size={14} />,
  Settings: <Settings size={14} />,
  Database: <Database size={14} />,
  Terminal: <Terminal size={14} />,
  BarChart3: <BarChart3 size={14} />,
  Layers: <Layers size={14} />,
  Send: <Send size={14} />,
}

function resolveIcon(iconName?: string): React.ReactNode {
  if (!iconName) return null
  return iconMap[iconName] ?? null
}

function InspectorSectionRenderer({ section }: { section: InspectSection }) {
  switch (section.Type) {
    case 'kv': return <KVSection section={section} />
    case 'list': return <ListSection section={section} />
    case 'markdown': return <MarkdownSection section={section} />
    case 'capability-triangle': return <CapabilityTriangleSection section={section} />
    case 'context-bar': return <ContextBarSection section={section} />
    default: return null
  }
}

type ResolvedPage = { id: string; label: string; icon?: string; component: React.FC<{ ref: InspectRef }> }

export interface AgentInspectorPanelProps {
  agentId: string
  actorId: string
  agentTitle?: string
  agentDisplayName_?: string
}

export function AgentInspectorPanel({ agentId, actorId, agentTitle, agentDisplayName_ }: AgentInspectorPanelProps) {
  const [currentRef, setCurrentRef] = useState<InspectRef>({
    Kind: 'actor_node',
    Id: actorId,
    Scope: { actor_id: actorId, agent_id: agentId },
  })
  const [document, setDocument] = useState<InspectDocument | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [activePage, setActivePage] = useState('overview')
  const requestSeq = React.useRef(0)

  const load = useCallback(async (ref: InspectRef) => {
    const seq = ++requestSeq.current
    setLoading(true)
    setError(null)
    try {
      const doc = await fetchInspectorDocument(ref)
      if (requestSeq.current !== seq) return
      setDocument(doc)
      setActivePage('overview')
    } catch (err) {
      if (requestSeq.current !== seq) return
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      if (requestSeq.current === seq) {
        setLoading(false)
      }
    }
  }, [])

  useEffect(() => {
    void load(currentRef)
  }, [currentRef, load])

  const inspect = useCallback((ref: InspectRef) => {
    setCurrentRef(ref)
  }, [])

  const availablePages = useMemo((): ResolvedPage[] => {
    if (!document?.Pages) return []
    return document.Pages
      .map((p): ResolvedPage | null => {
        const component = getInspectorComponent(p.Id)
        if (!component) return null
        return { id: p.Id, label: p.Label, icon: p.Icon, component }
      })
      .filter((p): p is ResolvedPage => p !== null)
  }, [document?.Pages])

  const activePageDef = availablePages.find(p => p.id === activePage)
  const PageComponent = activePageDef?.component

  const title = document?.Title || agentTitle || agentDisplayName_ || 'Untitled'
  const subtitle = document?.Subtitle || agentDisplayName_ || undefined

  return (
    <InspectContext.Provider value={inspect}>
      <div className="ai-agent-inspector-panel">
        {loading && (
          <div className="inspector-loading">
            <div className="inspector-spinner" />
            <span>Loading…</span>
          </div>
        )}

        {error && <div className="inspector-error">{error}</div>}

        {document && (
          <>
            <div className="inspector-header">
              <div className="inspector-header-row">
                <h3 className="inspector-title">{title}</h3>
                {document.Actions && document.Actions.length > 0 && (
                  <div className="inspector-header-actions">
                    {document.Actions.map(action => {
                      const icon = action.Id === 'edit-agent' ? <Pencil size={13} /> : null
                      const handleClick = () => {
                        if (action.Id === 'edit-agent' && action.Target?.Params?.actorId) {
                          window.dispatchEvent(new CustomEvent('sporemind:edit-agent', { detail: action.Target.Params.actorId }))
                          return
                        }
                        if (action.Target) openWorkbenchTarget(action.Target)
                      }
                      return (
                        <button
                          key={action.Id}
                          className="inspector-action-icon-btn"
                          onClick={handleClick}
                          title={action.Label}
                        >
                          {icon ?? action.Label}
                        </button>
                      )
                    })}
                  </div>
                )}
              </div>
              {subtitle && !document.Subtitle && (
                <span className="inspector-subtitle">{subtitle}</span>
              )}
              {document.Subtitle && (
                <span className="inspector-subtitle">{document.Subtitle}</span>
              )}
              {document.Status && (
                <span className={`inspector-status inspector-status--${document.Status.State}`}>
                  {document.Status.Label}
                </span>
              )}
              {document.Summary && (
                <p className="inspector-summary">{document.Summary}</p>
              )}
            </div>

            {availablePages.length > 0 && (
              <div className="inspector-page-toolbar">
                <button
                  className={`inspector-page-btn${activePage === 'overview' ? ' active' : ''}`}
                  onClick={() => setActivePage('overview')}
                  title="Overview"
                >
                  <Eye size={14} />
                </button>
                {availablePages.map(page => (
                  <button
                    key={page.id}
                    className={`inspector-page-btn${activePage === page.id ? ' active' : ''}`}
                    onClick={() => setActivePage(page.id)}
                    title={page.label}
                  >
                    {resolveIcon(page.icon)}
                  </button>
                ))}
              </div>
            )}

            {activePage === 'overview' ? (
              <>
                <div className="inspector-sections">
                  {document.Sections?.map(section => (
                    <InspectorSectionRenderer key={section.Id} section={section} />
                  ))}
                </div>

                {document.Relations && document.Relations.length > 0 && (
                  <div className="inspector-section">
                    <h4 className="inspector-section-title">Relations</h4>
                    <div className="inspector-relations">
                      {document.Relations.map((rel, i) => (
                        <button
                          key={i}
                          className="inspector-relation-btn"
                          onClick={() => inspect(rel.Ref)}
                        >
                          <span className="inspector-relation-label">{rel.Label}</span>
                          <span className="inspector-relation-arrow">→</span>
                        </button>
                      ))}
                    </div>
                  </div>
                )}
              </>
            ) : PageComponent ? (
              <div className="inspector-page-host">
                <PageComponent ref={currentRef} />
              </div>
            ) : null}
          </>
        )}
      </div>
    </InspectContext.Provider>
  )
}
