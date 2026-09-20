import type { InspectRow, InspectSection } from '../../../gen-clients/system/types'
import { useInspect } from '../inspectContext'
import { useState } from 'react'

const GROUP_BADGES = new Set(['Q', 'R', 'S'])

function KVRow({ row, depth = 0 }: { row: InspectRow; depth?: number }) {
  const inspect = useInspect()
  const [expanded, setExpanded] = useState(false)
  const hasChildren = (row.ExpandedRows?.length ?? 0) > 0
  const isClickable = !!row.Ref
  const isGroup = hasChildren && !!row.Badge && GROUP_BADGES.has(row.Badge)
  const groupClass = isGroup ? ` inspector-kv-row--group inspector-kv-row--group-${row.Label.toLowerCase()}` : ''
  const toggle = (e: React.MouseEvent) => {
    e.stopPropagation()
    setExpanded(!expanded)
  }
  const handleRowClick = () => {
    if (isClickable) {
      inspect?.(row.Ref!)
    } else if (hasChildren) {
      setExpanded(!expanded)
    }
  }
  return (
    <div className="inspector-kv-row-wrapper">
      <div
        className={`inspector-kv-row${isClickable ? ' inspector-kv-row--clickable' : ''}${hasChildren ? ' inspector-kv-row--expandable' : ''}${groupClass}`}
        style={{ paddingLeft: `${depth * 22}px` }}
        onClick={handleRowClick}
        title={row.Tooltip}
      >
        <span className={`inspector-kv-label${isGroup ? ' inspector-kv-label--schema' : ''}`}>
          {isGroup && row.Badge && (
            <span className={`inspector-kv-badge inspector-kv-badge--${row.Badge.toLowerCase()}`}>
              {row.Badge}
            </span>
          )}
          {isGroup ? row.Value : row.Label}
        </span>
        <span className={`inspector-kv-value${row.Mono ? ' inspector-kv-mono' : ''}`}>
          {!isGroup && row.Badge && (
            <span className={`inspector-kv-badge${row.Badge.length === 1 ? ` inspector-kv-badge--${row.Badge.toLowerCase()}` : ''}`} title={row.Tooltip}>
              {row.Badge}
            </span>
          )}
          {!isGroup && row.Value}
          {hasChildren && (
            <span className={`inspector-kv-chevron${expanded ? ' inspector-kv-chevron--expanded' : ''}`} onClick={toggle} />
          )}
        </span>
      </div>
      {expanded && hasChildren && (
        <div className="inspector-kv-children">
          {row.ExpandedRows!.map((child, i) => (
            <KVRow key={i} row={child} depth={depth + 1} />
          ))}
        </div>
      )}
    </div>
  )
}

export function KVSection({ section }: { section: InspectSection }) {
  const rows = section.Rows!
  return (
    <div className="inspector-section">
      <h4 className="inspector-section-title">{section.Title}</h4>
      <div className="inspector-kv-rows">
        {rows.map((row, i) => (
          <KVRow key={i} row={row} />
        ))}
      </div>
    </div>
  )
}
