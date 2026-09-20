import React, { useMemo } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { MemoryNode } from '../../../gen-clients/system/types'
import './MemoryNodePanel.css'

const LAYER_LABELS: Record<string, string> = {
  session: 'Session',
  experience: 'Experience',
  ontology: 'Ontology',
}

const LAYER_COLORS: Record<string, string> = {
  session: '#38bdf8',
  experience: '#a78bfa',
  ontology: '#f59e0b',
}

interface MemoryNodePanelProps {
  node: MemoryNode
  onClose: () => void
}

export const MemoryNodePanel: React.FC<MemoryNodePanelProps> = ({ node, onClose }) => {
  const layerLabel = LAYER_LABELS[node.Layer] ?? node.Layer
  const layerColor = LAYER_COLORS[node.Layer] ?? '#94a3b8'

  const body = useMemo(() => node.Content || '', [node.Content])

  return (
    <div className="memory-node-panel">
      <div className="memory-node-panel-header">
        <div className="memory-node-panel-title-row">
          <span className="memory-node-panel-layer-badge" style={{ background: layerColor }}>
            {layerLabel}
          </span>
          <h3 className="memory-node-panel-title">{node.Head || node.Id}</h3>
          <button className="memory-node-panel-close-btn" onClick={onClose} title="Close">
            <span style={{ fontSize: 18, lineHeight: 1 }}>×</span>
          </button>
        </div>
        <div className="memory-node-panel-meta">
          <span title="Node ID">{node.Id}</span>
          <span>Energy {node.Energy.toFixed(3)}</span>
          <span>{node.Tokens} tokens</span>
          <span title="Tick at last access">Tick {node.LastAccessTick}</span>
          {node.Protected && <span className="memory-node-panel-protected" title="Protected anchor">Protected</span>}
          {node.MarkedForDeath && <span className="memory-node-panel-death" title="Marked for deletion">Marked for death</span>}
        </div>
        <div className="memory-node-panel-dates">
          {node.CreatedAt && <span>Created {node.CreatedAt}</span>}
          {node.AccessedAt && <span>Accessed {node.AccessedAt}</span>}
        </div>
      </div>
      <div className="memory-node-panel-body markdown-content">
        <Markdown remarkPlugins={[remarkGfm]}>
          {body}
        </Markdown>
      </div>
    </div>
  )
}