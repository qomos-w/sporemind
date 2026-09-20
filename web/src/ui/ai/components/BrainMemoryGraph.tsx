import { memo, useEffect, useMemo, useRef, useState } from 'react'
import { Network } from 'vis-network/standalone'
import { client } from '../../../application/generated-client'
import * as memoryClient from '../../../gen-clients/local/client'
import { getTimelineManager } from '../hooks/useTimelineManager'
import type { MemoryEdge, MemoryNode, MemorySnapshotResp } from '../../../gen-clients/system/types'
import type { AgentInfoSnapshot } from '../hooks/agentInfoStore'
import './BrainMemoryGraph.css'

interface BrainMemoryGraphProps {
  activeAgentId?: string
  agentInfoSnapshot?: AgentInfoSnapshot
  onMemoryNodeClick?: (node: MemoryNode) => void
}

const LAYER_COLORS: Record<string, string> = {
  session: '#38bdf8',
  experience: '#a78bfa',
  ontology: '#f59e0b',
}

const EDGE_COLORS: Record<string, string> = {
  peer: '#60a5fa',
  cross: '#a78bfa',
  antagonist: '#ef4444',
}

export interface BrainVisGraph {
  nodes: Array<Record<string, unknown>>
  edges: Array<Record<string, unknown>>
}

interface VisDataSet {
  getIds(): (string | number)[]
  update(data: Record<string, unknown>[]): (string | number)[]
  remove(ids: (string | number)[]): (string | number)[]
}

function getInternalDataSets(network: Network): { nodes?: VisDataSet; edges?: VisDataSet } {
  return (network as unknown as { body: { data: { nodes?: VisDataSet; edges?: VisDataSet } } }).body.data
}

function syncDataSet(dataSet: VisDataSet, next: Array<Record<string, unknown>>): void {
  const nextIds = new Set(next.map(item => String(item.id)))
  const removed = dataSet.getIds().filter(id => !nextIds.has(String(id)))
  if (removed.length > 0) dataSet.remove(removed)
  dataSet.update(next)
}

function truncate(s: string, max: number): string {
  return s.length > max ? s.slice(0, max - 1) + '…' : s
}

function nodeTitle(node: MemoryNode): string {
  return [
    node.Head ? truncate(node.Head, 20) : '',
    truncate(node.Content, 20),
    `Layer: ${node.Layer}`,
    `Energy: ${node.Energy.toFixed(3)}`,
    `Tokens: ${node.Tokens}`,
  ].filter(Boolean).join('\n')
}

export function buildBrainVisGraph(snapshot: MemorySnapshotResp): BrainVisGraph {
  const containers = [
    { id: 'builtin:brain', label: 'Brain', color: '#e2e8f0', size: 28, x: 0, y: 0 },
    { id: 'builtin:brain:session', label: 'Session', color: LAYER_COLORS.session, size: 22, x: -240, y: 150 },
    { id: 'builtin:brain:experience', label: 'Experience', color: LAYER_COLORS.experience, size: 22, x: 0, y: -250 },
    { id: 'builtin:brain:ontology', label: 'Ontology', color: LAYER_COLORS.ontology, size: 22, x: 240, y: 150 },
  ]
  const containerNodes = containers.map(item => ({
    id: item.id,
    label: item.label,
    title: `${item.label} memory container`,
    shape: 'box',
    size: item.size,
    x: item.x,
    y: item.y,
    color: { background: item.color, border: item.color },
    font: { color: '#0f172a', bold: true },
  }))
  const containerEdges = ['session', 'experience', 'ontology'].map(layer => ({
    id: `builtin:brain:${layer}:root`,
    from: 'builtin:brain',
    to: `builtin:brain:${layer}`,
    color: { color: LAYER_COLORS[layer] },
    width: 2,
  }))
  const membershipEdges = snapshot.Nodes.map(node => ({
    id: `builtin:brain:${node.Layer}:${node.Id}`,
    from: `builtin:brain:${node.Layer}`,
    to: node.Id,
    color: { color: LAYER_COLORS[node.Layer] ?? '#64748b', opacity: 0.35 },
    dashes: [2, 5],
    width: 1,
  }))
  return {
    nodes: [...containerNodes, ...snapshot.Nodes.map(node => {
      const label = node.Head || node.Content
      return {
        id: node.Id,
        label: label.length > 48 ? `${label.slice(0, 45)}…` : label,
        title: nodeTitle(node),
        shape: 'dot',
        size: 10 + Math.min(24, Math.max(0, node.Energy) * 8),
        color: {
          background: node.MarkedForDeath ? '#64748b' : (LAYER_COLORS[node.Layer] ?? '#94a3b8'),
          border: node.MarkedForDeath ? '#ef4444' : (LAYER_COLORS[node.Layer] ?? '#94a3b8'),
        },
        borderWidth: node.MarkedForDeath ? 3 : 1,
      }
    })],
    edges: [...containerEdges, ...membershipEdges, ...snapshot.Edges.map((edge: MemoryEdge, index) => ({
      id: `${edge.From}:${edge.To}:${edge.Type}:${index}`,
      from: edge.From,
      to: edge.To,
      title: edge.Type,
      color: { color: EDGE_COLORS[edge.Type] ?? '#64748b' },
      dashes: edge.Type === 'antagonist' ? [6, 4] : false,
      width: 1,
    }))],
  }
}

export const BrainMemoryGraph = memo(function BrainMemoryGraph({ activeAgentId, agentInfoSnapshot, onMemoryNodeClick }: BrainMemoryGraphProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const networkRef = useRef<Network | null>(null)
  const [snapshot, setSnapshot] = useState<MemorySnapshotResp | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [timelineRefresh, setTimelineRefresh] = useState(0)
  const lastTargetRef = useRef<string | undefined>(undefined)
  const prevNodeIdsRef = useRef<Set<string>>(new Set())

  // Refs to avoid network recreation on callback/snapshot identity changes.
  const clickCbRef = useRef(onMemoryNodeClick)
  clickCbRef.current = onMemoryNodeClick
  const snapshotRef = useRef<MemorySnapshotResp | null>(null)
  snapshotRef.current = snapshot

  const agent = activeAgentId
    ? (agentInfoSnapshot?.byId.get(activeAgentId) ?? agentInfoSnapshot?.byActorId.get(activeAgentId))
    : undefined
  const target = agent?.ActorId ?? activeAgentId

  // Subscribe to timeline state changes so the graph re-fetches the
  // memory snapshot when a turn progresses. Debounce so a burst of
  // timeline events (message stream chunks, step events) only triggers
  // one snapshot fetch instead of dozens.
  useEffect(() => {
    if (!target) return
    const manager = getTimelineManager()
    let timer: ReturnType<typeof setTimeout> | undefined
    const bump = () => {
      clearTimeout(timer)
      timer = setTimeout(() => setTimelineRefresh(n => n + 1), 800)
    }
    const unsub = manager.subscribe(bump, target)
    return () => { clearTimeout(timer); unsub() }
  }, [target])

  useEffect(() => {
    let cancelled = false
    // Only reset the visible snapshot when the target agent changes, not on
    // every timeline bump — otherwise a turn-in-progress keeps clearing the
    // graph. The backend snapshot handler is now stateless, so refreshes land
    // immediately and the old graph can stay visible until the new one arrives.
    if (lastTargetRef.current !== target) {
      lastTargetRef.current = target
      setSnapshot(null)
      setError('')
    }
    if (!target) return () => { cancelled = true }
    setLoading(true)
    void memoryClient.memorySnapshot(client, {}, { target }).then(result => {
      if (!cancelled) setSnapshot(result)
    }).catch(err => {
      if (!cancelled) setError(err instanceof Error ? err.message : String(err))
    }).finally(() => {
      if (!cancelled) setLoading(false)
    })
    return () => { cancelled = true }
  }, [target, timelineRefresh])

  const graph = useMemo(() => snapshot ? buildBrainVisGraph(snapshot) : null, [snapshot])

  // Manage vis-network lifecycle: create once per target, update data
  // in-place. Avoids full physics re-stabilization (flicker) on every
  // snapshot refresh during a turn.
  useEffect(() => {
    // No data or empty — tear down any existing network.
    if (!graph || graph.nodes.length === 0) {
      if (networkRef.current) {
        networkRef.current.destroy()
        networkRef.current = null
      }
      prevNodeIdsRef.current = new Set()
      return
    }

    // Existing network — update data without recreating.
    const existing = networkRef.current
    if (existing) {
      const newNodeIds = new Set(graph.nodes.map(n => n.id as string))
      const dataSets = getInternalDataSets(existing)
      if (dataSets.nodes && dataSets.edges) {
        syncDataSet(dataSets.nodes, graph.nodes)
        syncDataSet(dataSets.edges, graph.edges)
      } else {
        existing.setData(graph)
      }
      prevNodeIdsRef.current = newNodeIds
      return
    }

    if (!containerRef.current) return

    // First creation for this target — full physics layout.
    const network = new Network(containerRef.current, graph, {
      nodes: {
        font: { size: 12, color: '#cbd5e1', face: 'system-ui, -apple-system, sans-serif' },
      },
      edges: {
        smooth: { enabled: true, type: 'continuous', roundness: 0.4 },
      },
      physics: {
        solver: 'forceAtlas2Based',
        forceAtlas2Based: { gravitationalConstant: -80, centralGravity: 0.005, springLength: 130, springConstant: 0.06, damping: 0.9, avoidOverlap: 0.6 },
        stabilization: { enabled: true, iterations: 300 },
        minVelocity: 1,
      },
      interaction: { hover: true, tooltipDelay: 120, zoomView: true, dragView: true },
      layout: { randomSeed: 7 },
      autoResize: true,
    })
    networkRef.current = network
    prevNodeIdsRef.current = new Set(graph.nodes.map(n => n.id as string))

    // Click handler via ref — no recreation needed on snapshot change.
    network.on('click', (params: { nodes?: string[] }) => {
      if (!params.nodes || params.nodes.length === 0) return
      const nodeId = params.nodes[0]
      if (!nodeId || nodeId.startsWith('builtin:brain')) return
      const snap = snapshotRef.current
      if (!snap) return
      const nodeMap = new Map(snap.Nodes.map(n => [n.Id, n]))
      const node = nodeMap.get(nodeId)
      if (node) clickCbRef.current?.(node)
    })
  }, [graph])

  // Cleanup on unmount.
  useEffect(() => {
    return () => {
      networkRef.current?.destroy()
      networkRef.current = null
    }
  }, [])

  if (!target) return <div className="brain-memory-state">Select an agent to view its brain.</div>
  if (loading && !snapshot) return <div className="brain-memory-state">Loading memory graph…</div>
  if (error && !snapshot) return <div className="brain-memory-state brain-memory-error">Unable to load memory graph: {error}</div>
  if (snapshot && !snapshot.Mounted) return <div className="brain-memory-state">Memory mode is not mounted for this agent.</div>
  if (snapshot && snapshot.Nodes.length === 0) return <div className="brain-memory-state">This memory graph is empty.</div>

  return (
    <div className="brain-memory-view">
      {snapshot && (
        <div className="brain-memory-stats">
          <span>{snapshot.Nodes.length} nodes</span>
          <span>{snapshot.Edges.length} edges</span>
          {snapshot.SleepDue && <span className="brain-memory-sleep">Sleep due</span>}
        </div>
      )}
      <div ref={containerRef} className="brain-memory-canvas" aria-label="Agent memory graph" />
      <div className="brain-memory-legend" aria-label="Memory layers">
        <span><i className="session" />Session</span>
        <span><i className="experience" />Experience</span>
        <span><i className="ontology" />Ontology</span>
      </div>
    </div>
  )
})
