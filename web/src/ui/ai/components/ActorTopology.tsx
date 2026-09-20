import { memo, useEffect, useMemo, useRef, useState } from 'react'
import { Network } from 'vis-network/standalone'
import { observationStore } from '../../observation/observationStore'
import type { UnifiedGraph, UnifiedGraphNode } from '../../../gen-types/observation'
import { avatarHue, agentAvatarLabel } from '../lib/agent-avatar'
import { sizeAndMassForChildCount } from '../lib/node-sizing'
import { useAgentInfoList, type AgentInfoSnapshot } from '../hooks/agentInfoStore'
import './ActorTopology.css'

/** Minimal interface for vis-network's internal DataSet (accessed via network.body.data). */
interface VisDataSet {
  getIds(): (string | number)[]
  update(data: Record<string, unknown> | Record<string, unknown>[]): (string | number)[]
  remove(id: string | number | (string | number)[]): (string | number)[]
}

function getInternalDataSets(network: Network): { nodes?: VisDataSet; edges?: VisDataSet } {
  return (network as unknown as { body: { data: { nodes?: VisDataSet; edges?: VisDataSet } } }).body.data
}

function getCssVar(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback
}

/** Map UnifiedGraph status string to a state colour. */
export function stateColor(status: string): { bg: string; border: string } {
  switch (status) {
    case 'running':
    case 'connected':
      // "connected" is the live state of an mcp-server node; it shares the
      // green used for running actors.
      return { bg: getCssVar('--status-running', 'hsl(150 60% 45%)'), border: getCssVar('--status-running', 'hsl(150 60% 45%)') }
    case 'paused':
      return { bg: getCssVar('--status-paused', 'hsl(45 90% 50%)'), border: getCssVar('--status-paused', 'hsl(45 90% 50%)') }
    case 'failed':
    case 'error':
      // "error" is the failed-connection state of an mcp-server node.
      return { bg: getCssVar('--status-error', 'hsl(0 70% 55%)'), border: getCssVar('--status-error', 'hsl(0 70% 55%)') }
    case 'stopped':
    case 'completed':
    case 'cancelled':
    case 'disconnected':
      // "disconnected" is the idle state of an mcp-server node.
      return { bg: getCssVar('--text-tertiary', '#888'), border: getCssVar('--text-tertiary', '#888') }
    default:
      return { bg: getCssVar('--accent-primary', '#3b82f6'), border: getCssVar('--accent-primary', '#3b82f6') }
  }
}

/** Determine node kind bucket for shape/colour decisions. */
export function nodeKindBucket(node: UnifiedGraphNode): 'project' | 'agent' | 'aggregator' | 'executor' | 'mcp-server' | 'other' {
  const kind = node.Kind.includes('.') ? node.Kind.split('.')[0]! : node.Kind
  if (kind === 'project') return 'project'
  if (kind === 'mcp-server') return 'mcp-server'
  if (kind === 'actor') {
    if (node.ActorType === 'agent') return 'agent'
    if (node.ActorType === 'aggregator') return 'aggregator'
    if (node.ActorType === 'executor') return 'executor'
    return 'other'
  }
  return 'other'
}

function agentAvatarSvg(id: string, displayName: string | undefined, agentKind: string | undefined): string {
  const hue = avatarHue(id)
  const isDark = document.documentElement.getAttribute('data-theme') === 'dark'
  const bg = isDark ? `hsl(${hue} 45% 22%)` : `hsl(${hue} 70% 92%)`
  const fg = isDark ? `hsl(${hue} 75% 88%)` : `hsl(${hue} 64% 36%)`
  const label = agentAvatarLabel(displayName, agentKind)
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="28" height="28"><circle cx="14" cy="14" r="14" fill="${bg}"/><text x="14" y="14" text-anchor="middle" dominant-baseline="central" font-family="system-ui, -apple-system, sans-serif" font-size="11" font-weight="600" fill="${fg}">${label}</text></svg>`
  return `data:image/svg+xml,${encodeURIComponent(svg)}`
}

interface ActorTopologyProps {
  agentInfoSnapshot?: AgentInfoSnapshot
  onNodeClick?: (nodeId: string, label: string, node: UnifiedGraphNode) => void
  onNodeContext?: (nodeId: string, label: string, node: UnifiedGraphNode, x: number, y: number) => void
}

/** vis-network edge produced by buildTopologyEdges. */
export interface TopologyVisEdge {
  id: string
  from: string
  to: string
  arrows: string
  color: { color: string; highlight: string }
  dashes?: boolean
  title?: string
}

/**
 * Build vis-network edges from a UnifiedGraph. Explicit typed edges (from
 * graph.Edges) are processed FIRST so that agent_child and call edges receive
 * their distinct styling. Parent-child edges derived from node ParentId are
 * added as fallback for nodes without an explicit connecting edge.
 *
 * Edge kind styling:
 *  - "agent_child": logical parent→child agent (purple accent, solid)
 *  - "call": agent→mcp-server tool call (neutral, dashed)
 *  - "child": physical actor tree parent→child (neutral, solid)
 *  - other: accent colour
 *
 * Exported for unit testing the deduplication and kind-routing logic.
 */
export function buildTopologyEdges(graph: UnifiedGraph): { edges: TopologyVisEdge[] } {
  const bc = getCssVar('--border-default', '#475569')
  const ac = getCssVar('--accent-primary', '#3b82f6')
  const pc = getCssVar('--accent-secondary', 'hsl(280 60% 55%)')

  const edgeSet = new Set<string>()
  const edges: TopologyVisEdge[] = []

  const addEdge = (from: string, to: string, kind: string) => {
    if (from === to) return
    const id = `${from}→${to}`
    if (edgeSet.has(id)) return
    edgeSet.add(id)

    let color = { color: bc, highlight: ac }
    let dashes: boolean | undefined
    let title: string | undefined

    if (kind === 'agent_child') {
      color = { color: pc, highlight: pc }
      title = 'agent child (logical parent)'
    } else if (kind === 'call') {
      dashes = true
      title = 'tool call'
    } else if (kind !== 'child') {
      color = { color: ac, highlight: ac }
    }

    const edge: TopologyVisEdge = { id, from, to, arrows: 'to', color }
    if (dashes) edge.dashes = dashes
    if (title) edge.title = title
    edges.push(edge)
  }

  // Explicit typed edges first — their styling takes priority over the
  // ParentId-derived fallback.
  for (const e of graph.Edges) {
    addEdge(e.From, e.To, e.Kind)
  }
  // Parent-child hierarchy edges as fallback for nodes without an explicit edge.
  for (const n of graph.Nodes) {
    if (n.ParentId) addEdge(n.ParentId, n.Id, 'child')
  }

  return { edges }
}

export const ActorTopology = memo(function ActorTopology({ agentInfoSnapshot, onNodeClick, onNodeContext }: ActorTopologyProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const networkRef = useRef<Network | null>(null)
  const onNodeClickRef = useRef(onNodeClick)
  onNodeClickRef.current = onNodeClick
  const onNodeContextRef = useRef(onNodeContext)
  onNodeContextRef.current = onNodeContext

  // Subscribe to observationStore graph updates.
  const [topoVersion, setTopoVersion] = useState(0)
  useEffect(() => {
    const unsub = observationStore.subscribeGraph(() => setTopoVersion(observationStore.getVersion()))
    return () => { unsub() }
  }, [])

  const liveSnapshot = useAgentInfoList()

  // Reactively track theme changes so colours can be pushed into the live network.
  const [themeKey, setThemeKey] = useState(0)
  useEffect(() => {
    const observer = new MutationObserver(() => setThemeKey(k => k + 1))
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    return () => observer.disconnect()
  }, [])

  // Build vis-network nodes and edges from the UnifiedGraph.
  const { nodes, edges } = useMemo(() => {
    void topoVersion
    const graph: UnifiedGraph | null = observationStore.getActiveGraph()
    const labelColor = getCssVar('--text-primary', '#e2e8f0')
    const accentColor = getCssVar('--accent-primary', '#3b82f6')
    const snapshot = agentInfoSnapshot ?? liveSnapshot

    if (!graph) return { nodes: [], edges: [] }

    // Count how many nodes each node parents (via ParentId) so hub nodes can
    // render larger and carry more mass in the force layout.
    const childCounts = new Map<string, number>()
    for (const n of graph.Nodes) {
      if (n.ParentId) childCounts.set(n.ParentId, (childCounts.get(n.ParentId) ?? 0) + 1)
    }
    const childCountOf = (id: string): number => childCounts.get(id) ?? 0

    const visNodes = graph.Nodes.map(n => {
      const bucket = nodeKindBucket(n)
      const sc = stateColor(n.Status)

      if (bucket === 'agent') {
        // Agent nodes use circularImage avatars, matching TopologyGraph style.
        const agentId = n.ActorId ?? n.Id
        const liveAgent = snapshot.byId.get(agentId) ?? snapshot.byActorId.get(agentId)
        const displayName = liveAgent?.DisplayName
        const agentLabel = displayName || n.Label
        const liveStatus = liveAgent?.Status ?? n.Status
        const liveSC = stateColor(liveStatus)
        const { size, mass } = sizeAndMassForChildCount(childCountOf(n.Id), { size: 28, mass: 3 })
        return {
          id: n.Id,
          label: agentLabel,
          title: `${n.Label} (${n.ActorType ?? 'agent'})`,
          shape: 'circularImage' as const,
          image: agentAvatarSvg(agentId, displayName, liveAgent?.AgentKind ?? n.ActorType),
          size,
          mass,
          borderWidth: 2,
          color: {
            background: 'transparent',
            border: liveSC.border,
            highlight: { background: 'transparent', border: liveSC.border },
          },
          font: { size: 12, color: labelColor, face: 'system-ui, -apple-system, sans-serif' },
        }
      }

      // Non-agent nodes: use dot shape with type-based colouring.
      let bgColor = accentColor
      if (bucket === 'project') bgColor = getCssVar('--accent-secondary', 'hsl(280 60% 55%)')
      else if (bucket === 'aggregator') bgColor = getCssVar('--accent-tertiary', 'hsl(190 60% 50%)')
      else if (bucket === 'executor') bgColor = 'hsl(30 70% 55%)'
      else if (bucket === 'mcp-server') bgColor = 'hsl(165 65% 45%)'

      const { size, mass } = sizeAndMassForChildCount(
        childCountOf(n.Id),
        bucket === 'project' ? { size: 18, mass: 5 } : bucket === 'mcp-server' ? { size: 15, mass: 2 } : { size: 12, mass: 1 },
      )
      return {
        id: n.Id,
        label: n.Label,
        title: bucket === 'mcp-server' ? `${n.Label} (mcp-server${n.Status ? ` · ${n.Status}` : ''})` : `${n.Label} (${bucket})`,
        shape: 'dot' as const,
        size,
        mass,
        color: {
          background: n.Status === 'running' ? bgColor : sc.bg,
          border: sc.border,
          highlight: { background: bgColor, border: sc.border },
        },
        font: { size: 12, color: labelColor, face: 'system-ui, -apple-system, sans-serif' },
      }
    })

    // Build edges: explicit typed edges first (so agent_child styling takes
    // priority), then parent-child edges as fallback for nodes without an
    // explicit connecting edge.
    const { edges: visEdges } = buildTopologyEdges(graph)

    return { nodes: visNodes, edges: visEdges }
  }, [topoVersion, themeKey, agentInfoSnapshot, liveSnapshot])

  const nodesRef = useRef(nodes)
  nodesRef.current = nodes

  const labelColor = getCssVar('--text-primary', '#e2e8f0')

  // Create the network once and wire up event listeners.
  useEffect(() => {
    if (!containerRef.current) return

    const network = new Network(
      containerRef.current,
      { nodes, edges },
      {
        nodes: {
          shape: 'dot',
          size: 12,
          borderWidth: 1,
          font: { size: 12, color: labelColor, face: 'system-ui, -apple-system, sans-serif' },
          shadow: false,
        },
        edges: {
          width: 1,
          color: { color: getCssVar('--border-default', '#475569'), highlight: getCssVar('--accent-primary', '#3b82f6') },
          arrows: { to: { scaleFactor: 0.6 } },
          smooth: { enabled: true, type: 'continuous', roundness: 0.5 },
        },
        physics: {
          enabled: true,
          solver: 'forceAtlas2Based',
          // forceAtlas2Based never converges on its own: with the default
          // damping (0.4) and minVelocity (0.5) the system's kinetic energy
          // rarely drops below the stop threshold on multi-node graphs, so it
          // keeps oscillating. Higher damping dissipates energy fast, avoidOverlap
          // stops mutual node-pushing, and a higher minVelocity lets the engine
          // declare "stable" sooner and halt the live simulation.
          forceAtlas2Based: {
            gravitationalConstant: -50,
            centralGravity: 0.005,
            springLength: 100,
            springConstant: 0.08,
            damping: 0.9,
            avoidOverlap: 0.5,
          },
          maxVelocity: 30,
          minVelocity: 1.0,
          timestep: 0.35,
          stabilization: { enabled: true, iterations: 1000 },
        },
        layout: { randomSeed: 2 },
        interaction: { hover: true, tooltipDelay: 200, zoomView: true, dragView: true },
        autoResize: false,
      },
    )

    networkRef.current = network

    // Manual resize handling.
    const resizeObserver = new ResizeObserver(() => {
      network.redraw()
    })
    resizeObserver.observe(containerRef.current)

    network.on('click', (params) => {
      if (params.nodes && params.nodes.length > 0) {
        const id = params.nodes[0] as string
        const graph = observationStore.getActiveGraph()
        const node = graph?.Nodes.find(n => n.Id === id)
        const label = nodesRef.current.find(n => n.id === id)?.label ?? id
        onNodeClickRef.current?.(id, label, node as UnifiedGraphNode)
      }
    })

    // Right-click context menu: find the node under cursor and dispatch
    network.on('oncontext', (params) => {
      // params.pointer.DOM contains the canvas-space coordinates; we need screen coords
      const nodeId = network.getNodeAt(params.pointer.DOM)
      if (nodeId == null) return
      const graph = observationStore.getActiveGraph()
      const node = graph?.Nodes.find(n => n.Id === nodeId)
      if (!node) return
      const nodeIdStr = String(nodeId)
      const label = nodesRef.current.find(n => n.id === nodeIdStr)?.label ?? nodeIdStr
      // params.event is the native event
      const evt = params.event as MouseEvent
      evt.preventDefault()
      onNodeContextRef.current?.(nodeIdStr, label, node, evt.clientX, evt.clientY)
    })

    return () => {
      resizeObserver.disconnect()
      network.destroy()
      networkRef.current = null
    }
  }, [])

  // Sync data (node/edge add/remove/update) and theme-dependent colours.
  useEffect(() => {
    const network = networkRef.current
    if (!network) return

    const lc = getCssVar('--text-primary', '#e2e8f0')
    const bc = getCssVar('--border-default', '#475569')
    const ac = getCssVar('--accent-primary', '#3b82f6')

    network.setOptions({
      nodes: { font: { size: 12, color: lc, face: 'system-ui, -apple-system, sans-serif' } },
      edges: { color: { color: bc, highlight: ac } },
    })

    const { nodes: nodesDS, edges: edgesDS } = getInternalDataSets(network)

    if (nodesDS) {
      const newNodeIds = new Set(nodes.map(n => n.id))
      const oldNodeIds = nodesDS.getIds() as string[]
      const toRemove = oldNodeIds.filter(id => !newNodeIds.has(id))
      if (toRemove.length > 0) nodesDS.remove(toRemove)
      nodesDS.update(nodes as unknown as Record<string, unknown>[])
    }

    if (edgesDS) {
      const newEdgeIds = new Set(edges.map(e => e.id))
      const oldEdgeIds = edgesDS.getIds() as string[]
      const toRemove = oldEdgeIds.filter(id => !newEdgeIds.has(id))
      if (toRemove.length > 0) edgesDS.remove(toRemove)
      edgesDS.update(edges as unknown as Record<string, unknown>[])
    }
  }, [nodes, edges])

  return <div className="actor-topology" ref={containerRef} />
})
