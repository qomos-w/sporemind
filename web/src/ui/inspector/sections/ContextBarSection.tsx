import { useMemo, useState } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import type { InspectContextSegment, InspectSection } from '../../../gen-clients/system/types'

function segmentColor(kind: string): string {
  const colors: Record<string, string> = {
    system: '#7c3aed',
    role: '#2563eb',
    environment: '#0891b2',
    git: '#16a34a',
    mounts: '#ca8a04',
    intent: '#ea580c',
    tools: '#dc2626',
    instructions: '#9333ea',
    extra: '#64748b',
  }
  return colors[kind] ?? '#64748b'
}

function segmentMeta(segment: InspectContextSegment): Array<[string, string | number | boolean | undefined]> {
  const values: Array<[string, string | number | boolean | undefined]> = [
    ['kind', segment.Kind],
    ['chars', segment.Chars],
    ['priority', segment.Priority],
    ['source', segment.Source],
    ['path', segment.Path],
    ['editable', segment.Editable],
  ]
  return values.filter(([, value]) => value !== undefined && value !== '')
}

export function ContextBarSection({ section }: { section: InspectSection }) {
  const segments = section.Segments!
  const [selectedID, setSelectedID] = useState(segments[0]?.Id ?? '')
  const totalChars = useMemo(
    () => Math.max(segments.reduce((sum, segment) => sum + Math.max(segment.Chars, 1), 0), 1),
    [segments]
  )
  const selected = segments.find(segment => segment.Id === selectedID) ?? segments[0]
  const selectedIndex = selected ? segments.findIndex(segment => segment.Id === selected.Id) : -1

  function selectOffset(offset: number) {
    if (segments.length === 0) return
    const baseIndex = selectedIndex >= 0 ? selectedIndex : 0
    const nextIndex = Math.min(Math.max(baseIndex + offset, 0), segments.length - 1)
    const nextSegment = segments[nextIndex]
    if (nextSegment) setSelectedID(nextSegment.Id)
  }

  return (
    <div className="inspector-section">
      <div className="inspector-context-title-row">
        <h4 className="inspector-section-title">{section.Title}</h4>
        <div className="inspector-context-nav">
          <button
            className="inspector-context-nav-btn"
            onClick={() => selectOffset(-1)}
            disabled={selectedIndex <= 0}
            title="Select previous block"
          >
            <ChevronLeft size={12} />
          </button>
          <span>{selectedIndex >= 0 ? selectedIndex + 1 : 0}/{segments.length} · {totalChars} chars</span>
          <button
            className="inspector-context-nav-btn"
            onClick={() => selectOffset(1)}
            disabled={selectedIndex < 0 || selectedIndex >= segments.length - 1}
            title="Select next block"
          >
            <ChevronRight size={12} />
          </button>
        </div>
      </div>
      {segments.length === 0 ? (
        <div className="inspector-context-empty">No context segments</div>
      ) : (
        <>
          <div className="inspector-context-bar" aria-label={`${totalChars} context chars`}>
            {segments.map(segment => (
              <button
                key={segment.Id}
                className={`inspector-context-segment${segment.Id === selected?.Id ? ' selected' : ''}`}
                style={{ flexGrow: Math.max(segment.Chars, 1), background: segmentColor(segment.Kind) }}
                title={`${segment.Label} · ${segment.Chars} chars`}
                onClick={() => setSelectedID(segment.Id)}
              />
            ))}
          </div>
          {selected && (
            <div className="inspector-context-detail">
              <div className="inspector-context-detail-title">
                <span>{selected.Label}</span>
                <span>{selected.Chars} chars</span>
              </div>
              <div className="inspector-context-meta">
                {segmentMeta(selected).map(([label, value]) => (
                  <span key={label}><strong>{label}</strong> {String(value)}</span>
                ))}
              </div>
              {selected.Content ? (
                <pre className="inspector-context-pre">{selected.Content}</pre>
              ) : (
                <div className="inspector-context-empty">No content captured for this segment</div>
              )}
            </div>
          )}
        </>
      )}
    </div>
  )
}
