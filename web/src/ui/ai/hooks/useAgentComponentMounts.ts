import { useEffect, useState, useCallback, useRef, useMemo } from 'react'
import type { AgentComponentMount, ComponentToolContribution } from '../../../gen-types/component'
import type { CallableInterface } from '../../../gen-types/observation'
import type { MonoCardListItem } from '../../../gen-clients/system/types'
import type { McpServerStatus } from '../../../gen-clients/system/types'
import { client } from '../../../application/generated-client'
import * as agentComponentClient from '../../../gen-clients/local/client'
import * as agentClient from '../../../gen-clients/local/client'
import * as workspaceWikiClient from '../../../gen-clients/workspace/client'
import * as projectWikiClient from '../../../gen-clients/project/client'
import * as appmanagerComponentClient from '../../../gen-clients/appmanager/client'
import * as mcpClient from '../../../gen-clients/mcp/client'
import { OnMcpServerStatus } from '../../../gen-clients/mcpmanager/client'

const POLL_INTERVAL_MS = 2000

/**
 * Bundles surfaced only in developer mode: the builtin debug/introspection
 * bundle and the plugin bundles projected from the appmanager registry.
 * They are hidden from the composer slash panel unless developer mode is on.
 */
export function isDeveloperOnlyBundleCard(card: MonoCardListItem): boolean {
  if (card.Data?.componentKind !== 'bundle' && card.Type !== 'bundle') return false
  return card.Id === 'builtin:bundle:debug' || card.Source === 'appmanager'
}

/**
 * Bundles gated to dev builds (BuildType=dev): the frontend counterpart of
 * agentkit.DevOnlyBundleIDs — the card definition carries `data.devOnly`.
 * Hidden from the composer slash panel and the kind-settings bundle grid
 * outside dev builds.
 */
export function isDevBuildOnlyBundleCard(card: MonoCardListItem): boolean {
  if (card.Data?.componentKind !== 'bundle' && card.Type !== 'bundle') return false
  return card.Data?.devOnly === true
}

/**
 * Bundles gated to Insider-and-above accounts (active Insider pass or
 * Ultimate): browser-crawl. Hidden from the composer slash panel / mount
 * picker unless useInsiderAccess() reports access.
 */
export function isInsiderOnlyBundleCard(card: MonoCardListItem): boolean {
  if (card.Data?.componentKind !== 'bundle' && card.Type !== 'bundle') return false
  return card.Id === 'builtin:bundle:browser-crawl'
}

/** Content equality for component mounts. The 2s componentList poll returns a
 *  fresh array on every tick even when nothing changed; a new `mounts`
 *  reference would refire the [agentActorId, mounts] effect below and
 *  refetch the ~100KB list_callables registry + component snapshot on every
 *  poll — the recurring SLOW wails.onmessage batches (50-85ms jank every
 *  2s per mounted hook instance). Comparing by value keeps the reference
 *  stable across no-op polls. */
function mountsEqual(a: AgentComponentMount[], b: AgentComponentMount[]): boolean {
  if (a === b) return true
  if (a.length !== b.length) return false
  const key = (m: AgentComponentMount) =>
    `${m.MountId}\u0000${m.CardId}\u0000${m.Kind ?? ''}\u0000${m.Enabled ? 1 : 0}\u0000${m.Order ?? 0}\u0000${m.Scope ?? ''}\u0000${m.Title ?? ''}\u0000${m.Icon ?? ''}\u0000${m.Version ?? 0}\u0000${m.UpdatedAt ?? ''}`
  const ka = a.map(key).sort()
  const kb = b.map(key).sort()
  for (let i = 0; i < ka.length; i++) {
    if (ka[i] !== kb[i]) return false
  }
  return true
}

export function useAgentComponentMounts(agentActorId: string | null | undefined, projectId?: string | null) {
  const [mounts, setMounts] = useState<AgentComponentMount[]>([])
  const [allComponentCards, setAllComponentCards] = useState<MonoCardListItem[]>([])
  const [componentTools, setComponentTools] = useState<ComponentToolContribution[]>([])
  const [callablesById, setCallablesById] = useState<Map<string, CallableInterface>>(new Map())
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // mcpStatusById holds the live connection state of every configured MCP
  // server, keyed by server ID. MCP bundle cards (Data.mcpServerId) read it
  // so the picker can show connected/disconnected state.
  const [mcpStatusById, setMcpStatusById] = useState<Record<string, McpServerStatus>>({})
  const refreshRef = useRef<() => Promise<void>>(async () => {})

  const refresh = useCallback(async () => {
    if (!agentActorId) {
      setMounts([])
      return
    }
    setLoading(true)
    try {
      const resp = await agentComponentClient.componentList(client, {}, { target: agentActorId })
      const next = resp.Items ?? []
      // Reference-stable update: a fresh array per poll must not count as a
      // change — see mountsEqual. Keeps the [agentActorId, mounts] effect
      // (list_callables + componentSnapshot refetch) from firing on every
      // no-op poll tick.
      setMounts(prev => (mountsEqual(prev, next) ? prev : next))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [agentActorId])

  refreshRef.current = refresh

  useEffect(() => {
    refresh()
  }, [refresh])

  useEffect(() => {
    let active = true
    // Component cards come from two sources: the workspace system wiki
    // (builtin:* and mcp:* cards) and the active project's card store
    // (user/app-published cards like app-bundle:*). Project cards shadow
    // workspace cards with the same id, matching the backend catalog merge.
    // IncludeBuiltin: keeps mcp:* external cards in the listing — the
    // backend drops them by default (filterMcpExternalCards) and only this
    // opt-in surfaces mountable MCP bundle cards in the catalog.
    const loads: Promise<MonoCardListItem[]>[] = [
      workspaceWikiClient.wikiListCards(client, { Flat: true, IncludeRaw: false, IncludeBuiltin: true, Limit: -1 }).then(r => r.Cards ?? []),
    ]
    if (projectId) {
      loads.push(
        projectWikiClient.wikiListCards(client, { Flat: true, IncludeRaw: false, IncludeBuiltin: true, Limit: -1 }, { target: projectId })
          .then(r => r.Cards ?? [])
          .catch(() => [] as MonoCardListItem[]),
      )
    }
    // Virtual app bundles: projected host-wide from the appmanager registry
    // (bundle definitions embedded in loaded artifacts). No card entity
    // exists anywhere, so they are synthesized into the card shape here.
    loads.push(
      appmanagerComponentClient.componentList(client, {})
        .then(r => (r.Items ?? []).map(d => ({
          Id: d.Ref?.CardId ?? '',
          Type: d.Ref?.Kind ?? 'bundle',
          Source: 'appmanager',
          Storage: 'virtual',
          Visibility: 'public',
          Tags: ['component', 'bundle'],
          List: [],
          Created: '',
          Modified: '',
          Protected: false,
          Editable: false,
          Deletable: false,
          Raw: '',
          Data: {
            componentKind: d.Ref?.Kind ?? 'bundle',
            title: d.Title,
            source: 'appmanager',
            icon: d.Icon,
            visual: d.Visual,
            tools: (d.Tools ?? []).map(t => t.CallableId),
          },
        } as MonoCardListItem)))
        .catch(() => [] as MonoCardListItem[]),
    )
    Promise.all(loads).then(lists => {
      if (!active) return
      const byId = new Map<string, MonoCardListItem>()
      lists.forEach(cards => cards.forEach(card => byId.set(card.Id, card)))
      // Component kind is canonical in Data.componentKind (builtin convention),
      // but app-bundle cards published by appmanager carry the kind in the
      // frontmatter type field (card.Type). Normalize both into Data so the
      // downstream filters/labels read a single field.
      const normalized = [...byId.values()].map(card => {
        const declared = card.Data?.componentKind
        const kind = declared === 'bundle' || declared === 'mode'
          ? declared
          : (card.Type === 'bundle' || card.Type === 'mode' ? card.Type : undefined)
        if (!kind) return null
        if (declared === kind) return card
        return { ...card, Data: { ...card.Data, componentKind: kind } }
      }).filter((card): card is NonNullable<typeof card> => card !== null)
      setAllComponentCards(normalized)
    })
    return () => { active = false }
  }, [projectId])

  // Bundles marked modeManaged are never independently mountable from the
  // slash panel — they enter the mount set via agent kind config or as a
  // dependency of a mounted mode (the backend mount handler auto-mounts a
  // mode's requires). Modes that share a flow group are mutually exclusive:
  // hide a mode whose flow conflicts with an already-mounted mode.
  const mountedCardIds = useMemo(
    () => new Set(mounts.map(m => m.CardId)),
    [mounts],
  )

  const activeFlows = useMemo(() => {
    const flows = new Set<string>()
    for (const card of allComponentCards) {
      if (mountedCardIds.has(card.Id) && typeof card.Data?.flow === 'string') {
        flows.add(card.Data.flow)
      }
    }
    return flows
  }, [allComponentCards, mountedCardIds])

  const componentCards = useMemo(
    () => allComponentCards.filter(card => {
      if (card.Data?.modeManaged === true) return false
      if (typeof card.Data?.flow === 'string' && card.Data.flow !== '') {
        if (activeFlows.has(card.Data.flow) && !mountedCardIds.has(card.Id)) return false
      }
      return true
    }),
    [allComponentCards, mountedCardIds, activeFlows],
  )

  useEffect(() => {
    if (!agentActorId) return
    const interval = setInterval(() => {
      refreshRef.current()
    }, POLL_INTERVAL_MS)
    return () => clearInterval(interval)
  }, [agentActorId])

  // MCP bundle picker status: load the server list once and keep it live via
  // mcp.server_status events (mcpinstance emits on every connect/disconnect /
  // tool-list change). Best-effort — a missing mcpmanager just leaves the map
  // empty and the picker renders MCP cards without a status chip.
  useEffect(() => {
    const applyStatus = (items: Array<{ Id: string; Status: McpServerStatus }>) => {
      const map: Record<string, McpServerStatus> = {}
      for (const item of items ?? []) {
        if (item.Id) map[item.Id] = item.Status
      }
      setMcpStatusById(map)
    }
    mcpClient.listServers(client).then((resp) => applyStatus(resp.Items ?? [])).catch(() => { /* best-effort */ })
    return OnMcpServerStatus(client, (payload) => {
      setMcpStatusById(prev => {
        const status = payload.Status
        if (!status?.Id) return prev
        const next = { ...prev }
        next[status.Id] = status
        return next
      })
    })
  }, [])

  // Component tools come from the agent's component snapshot (grouped per
  // bundle by their CardId) and full callable schemas from list_callables.
  // Both only change when the mounted bundle set changes, so refetch when the
  // mount set shifts rather than on every poll.
  useEffect(() => {
    if (!agentActorId) {
      setComponentTools([])
      setCallablesById(new Map())
      return
    }
    let active = true
    Promise.all([
      agentComponentClient.componentSnapshot(client, {}, { target: agentActorId }),
      agentClient.listCallables(client, { Limit: 1000 }, { target: agentActorId }),
    ]).then(([snapResp, callResp]) => {
      if (!active) return
      setComponentTools(snapResp.Snapshot?.Tools ?? [])
      const map = new Map<string, CallableInterface>()
      for (const ci of callResp.Items ?? []) map.set(ci.Name, ci)
      setCallablesById(map)
    }).catch(() => { /* tooltip is best-effort */ })
    return () => { active = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentActorId, mounts])

  const mount = useCallback(async (cardId: string) => {
    if (!agentActorId) return
    await agentComponentClient.componentMount(client, { CardId: cardId, Scope: 'user' }, { target: agentActorId })
    await refreshRef.current()
  }, [agentActorId])

  // unmount removes a mounted card. `confirm` acknowledges destructive
  // side effects server-side (builtin:mode:worktree discards the worktree);
  // the backend rejects the unmount without it when a worktree is bound.
  const unmount = useCallback(async (cardId: string, confirm = false) => {
    if (!agentActorId) return
    await agentComponentClient.componentUnmount(client, { CardId: cardId, Confirm: confirm }, { target: agentActorId })
    await refreshRef.current()
  }, [agentActorId])

  return { mounts, componentCards, componentTools, callablesById, loading, error, refresh, mount, unmount, mcpStatusById }
}
