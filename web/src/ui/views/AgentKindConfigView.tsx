import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ArrowDown, ArrowUp, RotateCcw, Save, Trash2, X, MessageSquareText, Boxes, Wand2, Lightbulb, Terminal, LayoutGrid, Rows2 } from 'lucide-react'
import { client } from '../../application/generated-client'
import { buildType } from '../../config/buildConfig'
import { useI18n } from '../../i18n'
import * as workspace from '../../gen-clients/workspace/client'
import * as wiki from '../../gen-clients/workspace/client'
import * as promptCards from '../../application/prompt-card-store'
import * as skillCards from '../../application/skill-card-store'
import type { PromptProfile, PromptFragment } from '../../gen-types/prompt'
import type { Skill } from '../../domain/skill-types'
import type { AICallableUnitView, CompactionPolicy, ModelSlot, RandomNameConfig, StoragePolicy } from '../../gen-clients/system/types'
import type { AggregatorDescriptor } from '../../gen-types/aigen'
import * as aimanager from '../../gen-clients/aimanager/client'
import * as aiaggregator from '../../gen-clients/aiaggregator/client'
import { slotToSelection, selectionToSlot, SYSTEM_AGGREGATOR_ID, type SlotSelection } from '../ai/hooks/modelSlot'
import { useCardGridView, type CardGridView } from '../../application/card-grid-view'
import { useAIShellContext } from '../ai/context/AIShellContext'
import { AgentConfigCard, agentConfigCardSize, agentConfigCardSizeCompact, agentConfigCardSizeSlim } from '../ai/components/AgentConfigCard'
import { parseMonoCard } from '../../domain/mono-types'
import '../ai/components/SettingsContent.css'
import { SettingRow } from '../settings/shadcn/composites'
import { Badge, Button, Input, Switch, Textarea, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText, TabsRoot, TabsList, TabsTrigger } from '../settings/shadcn/ui'

function AkcGroup({ title, children }: { title: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <h4 className="px-0.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">{title}</h4>
      {children}
    </div>
  )
}

type ProfileCard = PromptProfile & { CardId?: string; Icon?: string; Description?: string; Tags?: string[] }
type FragmentCard = PromptFragment & { CardId?: string; Icon?: string; Description?: string; Tags?: string[] }
type SkillCard = Skill & { CardId?: string; Icon?: string }

interface BundleMeta {
  id: string
  title: string
  description: string
  tags: string[]
  tools: string[]
  icon?: string
  settingsProtected: boolean
}

interface PromptRef {
  Kind: string
  Key: string
}

interface AgentKindConfig {
  kind: string
  displayName: string
  userCreatable: boolean
  systemManaged: boolean
  rolePromptRef: PromptRef
  systemFragmentRefs: PromptRef[]
  defaultBundleIDs: string[]
  skillIDs: string[]
  autoAllowTools: string[]
  autoAllowCandidates: string[]
  primary?: ModelSlot
  fast?: ModelSlot
  execution?: ModelSlot
  review?: ModelSlot
  summary?: ModelSlot
  compactionPolicy: CompactionPolicy
  storagePolicy: StoragePolicy
  randomName?: RandomNameConfig
}

function defaultCompactionPolicy(): CompactionPolicy {
  return {
    Enabled: false,
    Strategy: '',
    TriggerKind: '',
    BudgetMode: 'percentage',
    TokenBudget: 75,
    RecentWindow: 20,
    SummaryUnit: undefined,
    MaxSummaryTokens: 4000,
  }
}

function normalizeCompactionPolicy(cp: CompactionPolicy | undefined): CompactionPolicy {
  const d = defaultCompactionPolicy()
  if (!cp) return { ...d }
  return {
    ...cp,
    BudgetMode: cp.BudgetMode ?? d.BudgetMode,
    TokenBudget: cp.TokenBudget ?? d.TokenBudget,
    RecentWindow: cp.RecentWindow ?? d.RecentWindow,
    SummaryUnit: cp.SummaryUnit ?? d.SummaryUnit,
    MaxSummaryTokens: cp.MaxSummaryTokens ?? d.MaxSummaryTokens,
  }
}

function defaultStoragePolicy(): StoragePolicy {
  return { Enabled: false, MaxSessionChars: 200000, DiscardBatchSize: 10 }
}

function normalizeStoragePolicy(sp: StoragePolicy | undefined): StoragePolicy {
  const d = defaultStoragePolicy()
  if (!sp) return { ...d }
  return {
    Enabled: sp.Enabled ?? d.Enabled,
    MaxSessionChars: sp.MaxSessionChars ?? d.MaxSessionChars,
    DiscardBatchSize: sp.DiscardBatchSize ?? d.DiscardBatchSize,
  }
}

function arraysEqual(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false
  return a.every((val, i) => val === b[i])
}

function ensureProtectedBundles(ids: string[], protectedBundleIDs: string[]): string[] {
  const set = new Set(ids)
  for (const id of protectedBundleIDs) {
    set.add(id)
  }
  return Array.from(set)
}

function promptRefId(r: PromptRef): string {
  return `${r.Kind}/${r.Key}`
}

function samePromptRef(a: PromptRef, b: PromptRef): boolean {
  return a.Kind === b.Kind && a.Key === b.Key
}

function promptRefsEqual(a: PromptRef[], b: PromptRef[]): boolean {
  if (a.length !== b.length) return false
  return a.every((val, i) => samePromptRef(val, b[i]!))
}

function promptProfileKey(profile: PromptProfile): string {
  if (profile.Key) return profile.Key
  const scope = profile.Scope || 'project'
  const role = profile.Role || ''
  return role ? `${scope}.${role}` : scope
}

function normalizePromptRef(ref: PromptRef | undefined, fallbackKey = ''): PromptRef {
  return {
    Kind: ref?.Kind || 'profile',
    Key: ref?.Key || fallbackKey,
  }
}

function sortProfiles(profiles: PromptProfile[]): PromptProfile[] {
  const sourceOrder: Record<string, number> = { builtin: 0, override: 1, user: 2 }
  return [...profiles].sort((a, b) => {
    const aSource = sourceOrder[a.Source || 'user'] ?? 99
    const bSource = sourceOrder[b.Source || 'user'] ?? 99
    return aSource - bSource || promptProfileKey(a).localeCompare(promptProfileKey(b))
  })
}

/** Extract a short preview from a role prompt (first 2 non-empty lines). */
function rolePromptPreview(rolePrompt: string | undefined): string {
  if (!rolePrompt) return ''
  const lines = rolePrompt.split('\n').filter(l => l.trim() && !l.startsWith('#'))
  return lines.slice(0, 2).join(' ').slice(0, 120)
}

/** Group callable IDs by their prefix before the first dot or last underscore. */
function randomNameEqual(a: RandomNameConfig | undefined, b: RandomNameConfig | undefined): boolean {
  if (!a && !b) return true
  if (!a || !b) return false
  if (a.Enabled !== b.Enabled) return false
  return arraysEqual(a.Prefixes ?? [], b.Prefixes ?? []) && arraysEqual(a.Suffixes ?? [], b.Suffixes ?? [])
}

function modelSlotsEqual(a: ModelSlot | undefined, b: ModelSlot | undefined): boolean {
  return JSON.stringify(a ?? {}) === JSON.stringify(b ?? {})
}

// Default-slot select encoding, identical to the New Agent dialog:
//   ""                  → [auto] (system aggregator picks)
//   "agg::<configId>"   → a named custom aggregator
//   "<provider>::<model>" → a pinned unit from the system aggregator pool
const AGG_PREFIX = 'agg::'

function defaultSlotValue(slot: ModelSlot | undefined): string {
  const sel = slotToSelection(slot)
  if (!sel) return ''
  if (sel.type === 'aggregator') return AGG_PREFIX + sel.aggregatorId
  if (sel.aggregatorId && sel.aggregatorId !== SYSTEM_AGGREGATOR_ID) {
    return AGG_PREFIX + sel.aggregatorId
  }
  return `${sel.unit.provider}::${sel.unit.model}`
}

function valueToDefaultSlot(value: string, aggregators: AggregatorDescriptor[], units: AICallableUnitView[]): ModelSlot | undefined {
  if (!value) return undefined
  let sel: SlotSelection | undefined
  if (value.startsWith(AGG_PREFIX)) {
    const configId = value.slice(AGG_PREFIX.length)
    if (!aggregators.some(a => a.Id === configId)) return undefined
    sel = { type: 'aggregator', aggregatorId: configId }
  } else {
    const unit = units.find(u => `${u.ProviderName ?? ''}::${u.Model}` === value)
    if (!unit) return undefined
    sel = { type: 'unit', unit: { model: unit.Model, provider: unit.ProviderName ?? '' } }
  }
  return selectionToSlot(sel)
}

type AgentKindConfigTab = 'role' | 'components' | 'skills' | 'fragments' | 'auto-allow' | 'advanced'

interface ToggleCardItem {
  key: string
  active: boolean
  node: ReactNode
}

interface ToggleCardSectionLabels {
  viewFlat: string
  viewSplit: string
  groupActive: string
  groupInactive: string
}

// ToggleCardSection renders a card-toggle grid with a view switcher on the
// right of the section title: flat keeps the original single grid, split
// groups active cards into a top lane and inactive cards into a bottom lane.
function ToggleCardSection({ title, desc, items, view, onViewChange, labels, footer, empty }: {
  title: string
  desc?: string
  items: ToggleCardItem[]
  view: CardGridView
  onViewChange: (view: CardGridView) => void
  labels: ToggleCardSectionLabels
  footer?: ReactNode
  empty?: ReactNode
}) {
  const activeItems = items.filter(item => item.active)
  const inactiveItems = items.filter(item => !item.active)
  return (
    <section className="flex flex-col gap-2">
      <header className="flex flex-col gap-0.5">
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm font-medium">{title}</span>
          <div className="flex items-center gap-0.5" role="group">
            <Button
              type="button"
              variant={view === 'flat' ? 'secondary' : 'ghost'}
              size="icon-sm"
              title={labels.viewFlat}
              aria-pressed={view === 'flat'}
              onClick={() => onViewChange('flat')}
            >
              <LayoutGrid />
            </Button>
            <Button
              type="button"
              variant={view === 'split' ? 'secondary' : 'ghost'}
              size="icon-sm"
              title={labels.viewSplit}
              aria-pressed={view === 'split'}
              onClick={() => onViewChange('split')}
            >
              <Rows2 />
            </Button>
          </div>
        </div>
        {desc && <p className="text-xs text-muted-foreground">{desc}</p>}
      </header>
      {view === 'flat' ? (
        <div data-slot="akc-grid" className="flex flex-wrap gap-3">
          {items.map(item => item.node)}
          {empty}
        </div>
      ) : (
        <>
          <div data-slot="akc-group-title" className="px-0.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">{labels.groupActive} · {activeItems.length}</div>
          <div data-slot="akc-grid" className="flex flex-wrap gap-3">{activeItems.map(item => item.node)}</div>
          <div data-slot="akc-group-title" className="px-0.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">{labels.groupInactive} · {inactiveItems.length}</div>
          <div data-slot="akc-grid" className="flex flex-wrap gap-3">
            {inactiveItems.map(item => item.node)}
            {empty}
          </div>
        </>
      )}
      {footer}
    </section>
  )
}

export function AgentKindConfigView({ kind, builtin, onRequestDelete }: {
  kind: string
  /** True for templates in the hard-coded built-in table; these cannot be deleted. */
  builtin?: boolean
  /** Requests deletion of this (non-built-in) template via the caller's confirmation flow. */
  onRequestDelete?: () => void
}) {
  const { t } = useI18n()
  const toggleCardSectionLabels: ToggleCardSectionLabels = {
    viewFlat: t('agentKind.viewFlat'),
    viewSplit: t('agentKind.viewSplit'),
    groupActive: t('agentKind.groupActive'),
    groupInactive: t('agentKind.groupInactive'),
  }
  const { onOpenCardForEdit } = useAIShellContext()
  const [aggregators, setAggregators] = useState<AggregatorDescriptor[]>([])
  const [availableUnits, setAvailableUnits] = useState<AICallableUnitView[]>([])
  const [config, setConfig] = useState<AgentKindConfig | null>(null)
  const [draft, setDraft] = useState<AgentKindConfig | null>(null)
  const [profiles, setProfiles] = useState<ProfileCard[]>([])
  const [fragments, setFragments] = useState<FragmentCard[]>([])
  const [skills, setSkills] = useState<SkillCard[]>([])
  const [bundleMetas, setBundleMetas] = useState<BundleMeta[]>([])
  const [activeTab, setActiveTab] = useState<AgentKindConfigTab>('role')
  const [cardGridView, setCardGridView] = useCardGridView()
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  // Aggregators + the system aggregator's unit pool: same sources the New
  // Agent dialog uses so default-slot selects offer identical options.
  useEffect(() => {
    let cancelled = false
    aimanager.aggregatorList(client)
      .then(resp => { if (!cancelled) setAggregators(resp.Items) })
      .catch(() => { if (!cancelled) setAggregators([]) })
    return () => { cancelled = true }
  }, [])

  const systemAggActorId = useMemo(
    () => aggregators.find(a => a.Id === SYSTEM_AGGREGATOR_ID)?.ActorId ?? '',
    [aggregators],
  )
  const slotAggregators = useMemo(
    () => aggregators.filter(a => a.Id !== SYSTEM_AGGREGATOR_ID),
    [aggregators],
  )

  useEffect(() => {
    if (!systemAggActorId) {
      setAvailableUnits([])
      return
    }
    let cancelled = false
    aiaggregator.status(client, { target: systemAggActorId })
      .then(resp => { if (!cancelled) setAvailableUnits(resp.Units ?? []) })
      .catch(() => { if (!cancelled) setAvailableUnits([]) })
    return () => { cancelled = true }
  }, [systemAggActorId])

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [nextConfig, profilesResp, fragmentsResp, nextSkills, allCards] = await Promise.all([
        workspace.getAgentKindConfig(client, { Kind: kind }),
        promptCards.listProfiles(client),
        promptCards.listFragments(client, { Scope: '', Role: '' }),
        skillCards.listSkills(client),
        wiki.wikiListCards(client, { Flat: true, IncludeRaw: true, Limit: -1 }),
      ])

      const sortedProfiles = sortProfiles(profilesResp.Items as ProfileCard[])
      const bundleCards = new Map<string, BundleMeta>()
      const isDevBuild = buildType === 'dev'
      for (const card of allCards.Cards ?? []) {
        if (card.Data?.settingsVisible !== true) continue
        // Dev-build-only bundles (data.devOnly, e.g. coordinator-wearable)
        // never appear in the kind-settings bundle grid outside dev builds.
        if (!isDevBuild && card.Data?.devOnly === true) continue
        const parsed = card.Raw ? parseMonoCard(card.Id, card.Raw) : null
        const rawData = parsed?.data ?? {}
        const data = { ...rawData, ...(card.Data ?? {}) } as Record<string, unknown>
        const visual = (data.visual && typeof data.visual === 'object' && !Array.isArray(data.visual)) ? data.visual as Record<string, unknown> : {}
        const body = card.Raw || ''
        const firstLine = body.split('\n').find(l => l.trim() && !l.trim().startsWith('#')) || ''
        bundleCards.set(card.Id, {
          id: card.Id,
          title: typeof data.title === 'string' ? data.title : (typeof data.name === 'string' ? data.name : card.Id),
          description: typeof data.description === 'string' ? data.description : firstLine.trim().slice(0, 160),
          tags: card.Tags ?? [],
          tools: Array.isArray(data.tools) ? data.tools.filter((tool): tool is string => typeof tool === 'string') : [],
          icon: typeof visual.icon === 'string' ? visual.icon : undefined,
          settingsProtected: data.settingsProtected === true,
        })
      }
      const protectedBundleIDs = Array.from(bundleCards.values()).filter(bundle => bundle.settingsProtected).map(bundle => bundle.id)

      const normalizedConfig: AgentKindConfig = {
        kind: nextConfig.Kind,
        displayName: nextConfig.DisplayName,
        userCreatable: nextConfig.UserCreatable,
        systemManaged: nextConfig.SystemManaged,
        rolePromptRef: normalizePromptRef(nextConfig.RolePromptRef),
        systemFragmentRefs: (nextConfig.SystemFragmentRefs || []).filter(ref => ref.Kind !== 'profile'),
        defaultBundleIDs: ensureProtectedBundles(nextConfig.DefaultBundleIDs || [], protectedBundleIDs),
        skillIDs: nextConfig.SkillIDs ?? [],
        autoAllowTools: nextConfig.AutoAllowTools ?? [],
        autoAllowCandidates: Array.from(new Set([...(nextConfig.AutoAllowCandidates ?? []), ...(nextConfig.AutoAllowTools ?? [])])),
        primary: nextConfig.Primary,
        fast: nextConfig.Fast,
        execution: nextConfig.Execution,
        review: nextConfig.Review,
        summary: nextConfig.Summary,
        compactionPolicy: normalizeCompactionPolicy(nextConfig.CompactionPolicy),
        storagePolicy: normalizeStoragePolicy(nextConfig.StoragePolicy),
        randomName: nextConfig.RandomName,
      }
      setProfiles(sortedProfiles)
      setFragments((fragmentsResp.Items || []) as FragmentCard[])
      // Resolve same-name collisions by priority (project internal >
      // system internal > project external) so each skill name appears once.
      // An uncovered external skill is now toggleable; one shadowed by a
      // same-name higher-priority skill is dropped. Same source of truth as
      // the slash search and the backend manifest.
      setSkills(skillCards.resolveSkillOverrides(nextSkills as SkillCard[]).sort((a, b) => a.Name.localeCompare(b.Name)))
      setBundleMetas(Array.from(bundleCards.values()))

      setConfig(normalizedConfig)
      setDraft(normalizedConfig)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setConfig(null)
      setDraft(null)
      setProfiles([])
      setFragments([])
      setSkills([])
      setBundleMetas([])
    } finally {
      setLoading(false)
    }
  }, [kind])

  useEffect(() => {
    void load()
  }, [load])

  const fragmentLookup = useMemo(() => {
    const m = new Map<string, FragmentCard>()
    for (const f of fragments) {
      if (f.Key) m.set(`${f.Kind}/${f.Key}`, f)
      if (f.BuiltinKey) m.set(`${f.Kind}/${f.BuiltinKey}`, f)
    }
    return m
  }, [fragments])

  const profileLookup = useMemo(() => {
    const m = new Map<string, ProfileCard>()
    for (const p of profiles) {
      m.set(promptProfileKey(p), p)
    }
    return m
  }, [profiles])

  const fragmentGridRefs = useMemo(() => {
    if (!draft) return []
    const selected = new Set(draft.systemFragmentRefs.filter(r => r.Kind !== 'profile').map(promptRefId))
    const selectedOrdered = draft.systemFragmentRefs.filter(r => r.Kind !== 'profile')
    const available = fragments
      .map(f => ({ Kind: f.Kind, Key: f.Key || f.BuiltinKey || '' }))
      .filter(ref => ref.Kind !== 'profile' && !selected.has(promptRefId(ref)))
    return [...selectedOrdered, ...available]
  }, [draft, fragments])

  const autoAllowCandidates = useMemo(() => {
    if (!draft) return []
    const candidates = new Set(draft.autoAllowCandidates)
    for (const tool of draft.autoAllowTools) candidates.add(tool)
    const configuredBundles = new Set(draft.defaultBundleIDs)
    for (const bundle of bundleMetas) {
      if (!configuredBundles.has(bundle.id)) continue
      for (const tool of bundle.tools) candidates.add(tool)
    }
    return Array.from(candidates).sort((a, b) => a.localeCompare(b))
  }, [bundleMetas, draft])

  const isDirty = useMemo(() => {
    if (!config || !draft) return false
    const cp = config.compactionPolicy
    const dp = draft.compactionPolicy
    return config.displayName !== draft.displayName
      || config.userCreatable !== draft.userCreatable
      || config.systemManaged !== draft.systemManaged
      || !samePromptRef(config.rolePromptRef, draft.rolePromptRef)
      || !promptRefsEqual(config.systemFragmentRefs, draft.systemFragmentRefs)
      || !arraysEqual(config.defaultBundleIDs, draft.defaultBundleIDs)
      || !arraysEqual(config.skillIDs, draft.skillIDs)
      || !arraysEqual(config.autoAllowTools, draft.autoAllowTools)
      || !arraysEqual(config.autoAllowCandidates, draft.autoAllowCandidates)
      || !randomNameEqual(config.randomName, draft.randomName)
      || !modelSlotsEqual(config.primary, draft.primary)
      || !modelSlotsEqual(config.fast, draft.fast)
      || !modelSlotsEqual(config.execution, draft.execution)
      || !modelSlotsEqual(config.review, draft.review)
      || !modelSlotsEqual(config.summary, draft.summary)
      || cp.Enabled !== dp.Enabled
      || cp.BudgetMode !== dp.BudgetMode
      || cp.TokenBudget !== dp.TokenBudget
      || cp.RecentWindow !== dp.RecentWindow
      || cp.SummaryUnit?.model !== dp.SummaryUnit?.model
      || cp.SummaryUnit?.provider !== dp.SummaryUnit?.provider
      || cp.MaxSummaryTokens !== dp.MaxSummaryTokens
      || config.storagePolicy.Enabled !== draft.storagePolicy.Enabled
      || config.storagePolicy.MaxSessionChars !== draft.storagePolicy.MaxSessionChars
      || config.storagePolicy.DiscardBatchSize !== draft.storagePolicy.DiscardBatchSize
  }, [config, draft])

  const handleReset = useCallback(() => {
    if (!config) return
    setDraft(config)
    setError('')
  }, [config])

  const handleSave = useCallback(async () => {
    if (!draft) return
    setSaving(true)
    setError('')
    try {
      const saved = await workspace.saveAgentKindConfig(client, {
        Kind: draft.kind,
        DisplayName: draft.displayName,
        UserCreatable: draft.userCreatable,
        SystemManaged: draft.systemManaged,
        RolePromptRef: draft.rolePromptRef,
        SystemFragmentRefs: draft.systemFragmentRefs,
        DefaultBundleIDs: draft.defaultBundleIDs,
        SkillIDs: draft.skillIDs,
        AutoAllowTools: draft.autoAllowTools,
        AutoAllowCandidates: draft.autoAllowCandidates,
        Primary: draft.primary,
        Fast: draft.fast,
        Execution: draft.execution,
        Review: draft.review,
        Summary: draft.summary,
        CompactionPolicy: draft.compactionPolicy,
        StoragePolicy: draft.storagePolicy,
        RandomName: draft.randomName,
      })
      const scp = saved.CompactionPolicy
      const normalized: AgentKindConfig = {
        kind: saved.Kind,
        displayName: saved.DisplayName,
        userCreatable: saved.UserCreatable,
        systemManaged: saved.SystemManaged,
        rolePromptRef: normalizePromptRef(saved.RolePromptRef),
        systemFragmentRefs: (saved.SystemFragmentRefs || []).filter(ref => ref.Kind !== 'profile'),
        defaultBundleIDs: ensureProtectedBundles(saved.DefaultBundleIDs || [], bundleMetas.filter(bundle => bundle.settingsProtected).map(bundle => bundle.id)),
        skillIDs: saved.SkillIDs ?? [],
        autoAllowTools: saved.AutoAllowTools ?? [],
        autoAllowCandidates: Array.from(new Set([...(saved.AutoAllowCandidates ?? []), ...(saved.AutoAllowTools ?? [])])),
        primary: saved.Primary,
        fast: saved.Fast,
        execution: saved.Execution,
        review: saved.Review,
        summary: saved.Summary,
        compactionPolicy: normalizeCompactionPolicy(scp),
        storagePolicy: normalizeStoragePolicy(saved.StoragePolicy),
        randomName: saved.RandomName,
      }
      setConfig(normalized)
      setDraft(normalized)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }, [bundleMetas, draft])

  const addFragment = useCallback((ref: PromptRef) => {
    setDraft(prev => prev ? {
      ...prev,
      systemFragmentRefs: [...prev.systemFragmentRefs, ref],
    } : prev)
  }, [])

  const removeFragment = useCallback((ref: PromptRef) => {
    setDraft(prev => prev ? {
      ...prev,
      systemFragmentRefs: prev.systemFragmentRefs.filter(r => !samePromptRef(r, ref)),
    } : prev)
  }, [])

  const moveFragment = useCallback((ref: PromptRef, direction: -1 | 1) => {
    setDraft(prev => {
      if (!prev) return prev
      const next = [...prev.systemFragmentRefs]
      const index = next.findIndex(r => samePromptRef(r, ref))
      const target = index + direction
      if (index < 0 || target < 0 || target >= next.length) return prev
      ;[next[index]!, next[target]!] = [next[target]!, next[index]!]
      return { ...prev, systemFragmentRefs: next }
    })
  }, [])

  const toggleBundle = useCallback((bundleID: string) => {
    setDraft(prev => {
      if (!prev) return prev
      const has = prev.defaultBundleIDs.includes(bundleID)
      return {
        ...prev,
        defaultBundleIDs: has
          ? prev.defaultBundleIDs.filter(id => id !== bundleID)
          : [...prev.defaultBundleIDs, bundleID],
      }
    })
  }, [])

  const toggleSkill = useCallback((skillID: string) => {
    setDraft(prev => {
      if (!prev) return prev
      const has = prev.skillIDs.includes(skillID)
      return {
        ...prev,
        skillIDs: has ? prev.skillIDs.filter(id => id !== skillID) : [...prev.skillIDs, skillID],
      }
    })
  }, [])

  const selectRoleProfile = useCallback((profileKey: string) => {
    setDraft(prev => prev ? {
      ...prev,
      rolePromptRef: { Kind: 'profile', Key: profileKey },
    } : prev)
  }, [])

  if (loading) {
    return <p className="text-sm text-muted-foreground">{t('agentKind.loading')}</p>
  }

  if (error && !draft) {
    return <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>
  }

  if (!draft) {
    return <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{t('agentKind.notFound')}</p>
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold">{draft.displayName || draft.kind}</span>
          <Badge variant="secondary">{t('agentKind.kindBadge')}</Badge>
        </div>
        <div className="flex items-center gap-2">
          {builtin === false ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={onRequestDelete}
              data-testid="agent-kind-delete"
              data-guide-id="settings/agent/delete"
            >
              <Trash2 />
              {t('settings.agent.template.delete')}
            </Button>
          ) : builtin === true ? (
            <span className="text-xs text-muted-foreground" data-testid="agent-kind-builtin-hint">
              {t('settings.agent.template.builtinUndeletable')}
            </span>
          ) : null}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={handleReset}
            disabled={!isDirty || saving}
            data-guide-id="settings/agent/reset"
          >
            <RotateCcw />
            {t('agentKind.reset')}
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={() => void handleSave()}
            disabled={!isDirty || saving}
            data-guide-id="settings/agent/save"
          >
            <Save />
            {saving ? t('agentKind.saving') : t('agentKind.save')}
          </Button>
        </div>
      </div>

      {error && <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>}

      <TabsRoot value={activeTab} onValueChange={v => setActiveTab(v as AgentKindConfigTab)}>
        <TabsList>
          {(['role', 'components', 'skills', 'fragments', 'auto-allow', 'advanced'] as AgentKindConfigTab[]).map(tab => (
            <TabsTrigger key={tab} value={tab} data-guide-id={`settings/agent/tab/${tab}`}>
              {tab === 'role' && t('agentKind.tabRole')}
              {tab === 'components' && t('agentKind.componentsSection')}
              {tab === 'skills' && t('agentKind.skillsSection')}
              {tab === 'fragments' && t('agentKind.fragmentsSection')}
              {tab === 'auto-allow' && t('agentKind.autoAllowTools')}
              {tab === 'advanced' && t('agentKind.advanced')}
            </TabsTrigger>
          ))}
        </TabsList>
      </TabsRoot>

      <div className="flex flex-col gap-4">
        {/* Role */}
        {activeTab === 'role' && (
        <section className="flex flex-col gap-2">
          <header className="flex flex-col gap-0.5">
            <span className="text-sm font-medium">{t('agentKind.roleSection')}</span>
            <p className="text-xs text-muted-foreground">{t('agentKind.identityDesc')}</p>
          </header>
          <div data-slot="akc-grid" className="flex flex-wrap gap-3">
            {profiles.map(profile => {
              const key = promptProfileKey(profile)
              const isSelected = draft.rolePromptRef.Key === key
              return (
                <AgentConfigCard
                  className={agentConfigCardSize}
                  key={key}
                  icon={profile.Icon}
                  iconFallback={<MessageSquareText size={16} />}
                  title={profile.Role || key}
                  description={profile.Description || rolePromptPreview(profile.RolePrompt)}
                  tags={[profile.Source || 'user', ...(profile.Tags ?? [])]}
                  selected={isSelected}
                  onClick={() => selectRoleProfile(key)}
                  onEdit={profile.CardId && onOpenCardForEdit ? () => onOpenCardForEdit(profile.CardId!) : undefined}
                />
              )
            })}
          </div>
        </section>
        )}

        {/* Components */}
        {activeTab === 'components' && (
        <ToggleCardSection
          title={t('agentKind.componentsSection')}
          desc={t('agentKind.capabilitiesDesc')}
          view={cardGridView}
          onViewChange={setCardGridView}
          labels={toggleCardSectionLabels}
          items={bundleMetas.map(bundle => {
            const checked = draft.defaultBundleIDs.includes(bundle.id)
            const isProtected = bundle.settingsProtected
            return {
              key: bundle.id,
              active: checked,
              node: (
                <AgentConfigCard
                  className={agentConfigCardSizeCompact}
                  key={bundle.id}
                  icon={bundle.icon}
                  iconFallback={<Boxes size={16} />}
                  title={bundle.title}
                  description={bundle.description}
                  tags={bundle.tags}
                  checked={checked}
                  disabled={isProtected}
                  onToggle={() => toggleBundle(bundle.id)}
                  onEdit={onOpenCardForEdit ? () => onOpenCardForEdit(bundle.id) : undefined}
                />
              ),
            }
          })}
        />
        )}

        {/* Skills */}
        {activeTab === 'skills' && (
        <ToggleCardSection
          title={t('agentKind.skillsSection')}
          view={cardGridView}
          onViewChange={setCardGridView}
          labels={toggleCardSectionLabels}
          items={skills.map(skill => {
            const checked = draft.skillIDs.includes(skill.Id)
            return {
              key: skill.Id,
              active: checked,
              node: (
                <AgentConfigCard
                  className={agentConfigCardSize}
                  key={skill.Id}
                  icon={skill.Icon}
                  iconFallback={<Wand2 size={16} />}
                  title={skill.Name}
                  description={skill.Description}
                  tags={skill.Tags}
                  checked={checked}
                  onToggle={() => toggleSkill(skill.Id)}
                  onEdit={skill.CardId && onOpenCardForEdit ? () => onOpenCardForEdit(skill.CardId!) : undefined}
                />
              ),
            }
          })}
        />
        )}

        {/* Fragments */}
        {activeTab === 'fragments' && (
        <ToggleCardSection
          title={t('agentKind.fragmentsSection')}
          desc={t('agentKind.promptFragmentsDesc')}
          view={cardGridView}
          onViewChange={setCardGridView}
          labels={toggleCardSectionLabels}
          empty={fragmentGridRefs.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t('agentKind.noFragmentsSelected')}</p>
          ) : undefined}
          items={fragmentGridRefs.map(ref => {
              const id = promptRefId(ref)
              const frag = fragmentLookup.get(id)
              const profile = ref.Kind === 'profile' ? profileLookup.get(ref.Key) : undefined
              const selected = draft.systemFragmentRefs.some(r => samePromptRef(r, ref))
              const index = draft.systemFragmentRefs.findIndex(r => samePromptRef(r, ref))
              const first = index === 0
              const last = index === draft.systemFragmentRefs.length - 1
              const title = frag?.Name || profile?.Role || ref.Key
              const description = frag?.Description || profile?.Description || rolePromptPreview(frag?.Content || profile?.RolePrompt)
              const tags = frag?.Tags || profile?.Tags || []
              const cardId = frag?.CardId || profile?.CardId
              const icon = frag?.Icon || profile?.Icon
              return {
                key: id,
                active: selected,
                node: (
                <AgentConfigCard
                  className={agentConfigCardSize}
                  key={id}
                  icon={icon}
                  iconFallback={<Lightbulb size={16} />}
                  title={title}
                  description={description}
                  tags={[ref.Kind, ...tags]}
                  checked={selected}
                  onToggle={() => { if (selected) removeFragment(ref); else addFragment(ref) }}
                  onEdit={cardId && onOpenCardForEdit ? () => onOpenCardForEdit(cardId) : undefined}
                  footer={selected && (
                    <div className="flex items-center gap-0.5">
                      <Button type="button" variant="ghost" size="icon-sm" onClick={(e) => { e.stopPropagation(); moveFragment(ref, -1) }} disabled={first} title={t('agentKind.moveUp')}>
                        <ArrowUp />
                      </Button>
                      <Button type="button" variant="ghost" size="icon-sm" onClick={(e) => { e.stopPropagation(); moveFragment(ref, 1) }} disabled={last} title={t('agentKind.moveDown')}>
                        <ArrowDown />
                      </Button>
                      <Button type="button" variant="ghost" size="icon-sm" onClick={(e) => { e.stopPropagation(); removeFragment(ref) }} title={t('agentKind.remove')}>
                        <X />
                      </Button>
                    </div>
                  )}
                />
                ),
              }
            })}
        />
        )}

        {/* Auto-allow tools */}
        {activeTab === 'auto-allow' && (
        <ToggleCardSection
          title={t('agentKind.autoAllowTools')}
          desc={t('agentKind.autoAllowToolsDesc')}
          view={cardGridView}
          onViewChange={setCardGridView}
          labels={toggleCardSectionLabels}
          items={autoAllowCandidates.map(tool => {
            const selected = draft.autoAllowTools.includes(tool)
            const category = tool.includes('.') ? tool.slice(0, tool.indexOf('.')) : ''
            return {
              key: tool,
              active: selected,
              node: (
                <AgentConfigCard
                  key={tool}
                  className={agentConfigCardSizeSlim}
                  iconFallback={<Terminal size={14} />}
                  title={tool}
                  tags={category ? [category] : []}
                  checked={selected}
                  onToggle={() => setDraft(prev => prev ? {
                    ...prev,
                    autoAllowTools: selected
                      ? prev.autoAllowTools.filter(item => item !== tool)
                      : [...prev.autoAllowTools, tool],
                  } : prev)}
                />
              ),
            }
          })}
          footer={(
            <div className="w-64">
              <Input
                placeholder={t('agentKind.autoAllowToolsPlaceholder')}
                data-guide-id="settings/agent/auto-allow/add"
                onKeyDown={event => {
                  if (event.key !== 'Enter') return
                  const input = event.target as HTMLInputElement
                  const value = input.value.trim()
                  if (!value) return
                  setDraft(prev => prev ? {
                    ...prev,
                    autoAllowCandidates: prev.autoAllowCandidates.includes(value)
                      ? prev.autoAllowCandidates
                      : [...prev.autoAllowCandidates, value],
                    autoAllowTools: prev.autoAllowTools.includes(value)
                      ? prev.autoAllowTools
                      : [...prev.autoAllowTools, value],
                  } : prev)
                  input.value = ''
                }}
              />
            </div>
          )}
        />
        )}

        {/* Advanced */}
        {activeTab === 'advanced' && (
        <section className="flex flex-col gap-2">
          {/* Model slots */}
          <AkcGroup title={t('agentKind.defaultModelUnits')}>
            {([
              ['primary', t('agentKind.slot.primary')],
              ['fast', t('agentKind.slot.fast')],
              ['execution', t('agentKind.slot.execution')],
              ['review', t('agentKind.slot.review')],
              ['summary', t('agentKind.slot.summary')],
            ] as const).map(([slot, label]) => (
              <SettingRow
                key={slot}
                label={label}
                control={
                  <SelectRoot
                    aria-label={t('agentKind.slot.defaultModelUnitAria', { label })}
                    value={defaultSlotValue(draft[slot])}
                    onValueChange={(v) => setDraft(prev => prev ? {
                      ...prev,
                      [slot]: valueToDefaultSlot(v as string, slotAggregators, availableUnits),
                    } : prev)}
                    items={[
                      { value: '', label: t('agentKind.slot.auto') },
                      ...slotAggregators.map(a => ({ value: `${AGG_PREFIX}${a.Id}`, label: t('agentKind.slot.aggregatorOption', { name: a.Name || a.ActorId }) })),
                      ...availableUnits.map(u => ({ value: `${u.ProviderName ?? ''}::${u.Model}`, label: `${u.Model} (${u.ProviderName})` })),
                    ]}
                  >
                    <SelectTrigger
                      className="w-64"
                      aria-label={t('agentKind.slot.defaultModelUnitAria', { label })}
                      data-guide-id={`settings/agent/advanced/slot-${slot}`}
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="" data-guide-id={`settings/agent/advanced/slot-${slot}/auto`}>
                        <SelectItemText>{t('agentKind.slot.auto')}</SelectItemText>
                      </SelectItem>
                      {slotAggregators.map(a => (
                        <SelectItem key={`agg-${a.Id}`} value={`${AGG_PREFIX}${a.Id}`} data-guide-id={`settings/agent/advanced/slot-${slot}/agg-${a.Id}`}>
                          <SelectItemText>{t('agentKind.slot.aggregatorOption', { name: a.Name || a.ActorId })}</SelectItemText>
                        </SelectItem>
                      ))}
                      {availableUnits.map(u => (
                        <SelectItem key={`m-${u.Model}-${u.ProviderName}`} value={`${u.ProviderName ?? ''}::${u.Model}`} data-guide-id={`settings/agent/advanced/slot-${slot}/unit-${u.ProviderName}-${u.Model}`}>
                          <SelectItemText>{`${u.Model} (${u.ProviderName})`}</SelectItemText>
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </SelectRoot>
                }
              />
            ))}
          </AkcGroup>

          {/* Compaction */}
          <AkcGroup title={t('agentKind.compaction')}>
            <SettingRow
              label={t('agentKind.compactionEnabled')}
              control={
                <Switch
                  checked={draft.compactionPolicy.Enabled}
                  onCheckedChange={(checked) => setDraft(prev => prev ? {
                    ...prev,
                    compactionPolicy: { ...prev.compactionPolicy, Enabled: checked },
                  } : prev)}
                  data-guide-id="settings/agent/advanced/compaction-enabled"
                />
              }
            />
            {draft.compactionPolicy.Enabled && (
              <>
                <SettingRow
                  label={t('agentKind.budgetMode')}
                  control={
                    <SelectRoot
                      value={draft.compactionPolicy.BudgetMode || 'absolute'}
                      onValueChange={(v) => setDraft(prev => prev ? {
                        ...prev,
                        compactionPolicy: { ...prev.compactionPolicy, BudgetMode: v as string },
                      } : prev)}
                      items={[
                        { value: 'absolute', label: t('agentKind.budgetModeAbsolute') },
                        { value: 'percentage', label: t('agentKind.budgetModePercentage') },
                      ]}
                    >
                      <SelectTrigger className="w-48" data-guide-id="settings/agent/advanced/budget-mode">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="absolute" data-guide-id="settings/agent/advanced/budget-mode/absolute">
                          <SelectItemText>{t('agentKind.budgetModeAbsolute')}</SelectItemText>
                        </SelectItem>
                        <SelectItem value="percentage" data-guide-id="settings/agent/advanced/budget-mode/percentage">
                          <SelectItemText>{t('agentKind.budgetModePercentage')}</SelectItemText>
                        </SelectItem>
                      </SelectContent>
                    </SelectRoot>
                  }
                />
                <SettingRow
                  label={draft.compactionPolicy.BudgetMode === 'percentage' ? t('agentKind.tokenBudgetPercentage') : t('agentKind.tokenBudget')}
                  control={
                    <Input
                      type="number"
                      className="w-32"
                      value={draft.compactionPolicy.TokenBudget ?? ''}
                      onChange={event => setDraft(prev => prev ? {
                        ...prev,
                        compactionPolicy: { ...prev.compactionPolicy, TokenBudget: Number(event.target.value) },
                      } : prev)}
                      data-guide-id="settings/agent/advanced/token-budget"
                    />
                  }
                />
                <SettingRow
                  label={t('agentKind.recentWindow')}
                  control={
                    <Input
                      type="number"
                      className="w-32"
                      value={draft.compactionPolicy.RecentWindow ?? ''}
                      onChange={event => setDraft(prev => prev ? {
                        ...prev,
                        compactionPolicy: { ...prev.compactionPolicy, RecentWindow: Number(event.target.value) },
                      } : prev)}
                      data-guide-id="settings/agent/advanced/recent-window"
                    />
                  }
                />
                <SettingRow
                  label={t('agentKind.summaryModel')}
                  control={
                    <Input
                      className="w-64"
                      value={draft.compactionPolicy.SummaryUnit?.model || ''}
                      onChange={event => setDraft(prev => prev ? {
                        ...prev,
                        compactionPolicy: { ...prev.compactionPolicy, SummaryUnit: event.target.value ? { model: event.target.value, provider: '' } : undefined },
                      } : prev)}
                      placeholder={t('agentKind.summaryModelPlaceholder')}
                      data-guide-id="settings/agent/advanced/summary-model"
                    />
                  }
                />
                <SettingRow
                  label={t('agentKind.maxSummaryTokens')}
                  control={
                    <Input
                      type="number"
                      className="w-32"
                      value={draft.compactionPolicy.MaxSummaryTokens ?? ''}
                      onChange={event => setDraft(prev => prev ? {
                        ...prev,
                        compactionPolicy: { ...prev.compactionPolicy, MaxSummaryTokens: Number(event.target.value) },
                      } : prev)}
                      data-guide-id="settings/agent/advanced/max-summary-tokens"
                    />
                  }
                />
              </>
            )}
          </AkcGroup>

          {/* Storage */}
          <AkcGroup title={t('agentKind.storagePolicy')}>
            <SettingRow
              label={t('agentKind.storageEnabled')}
              control={
                <Switch
                  checked={draft.storagePolicy.Enabled}
                  onCheckedChange={(checked) => setDraft(prev => prev ? {
                    ...prev,
                    storagePolicy: { ...prev.storagePolicy, Enabled: checked },
                  } : prev)}
                  data-guide-id="settings/agent/advanced/storage-enabled"
                />
              }
            />
            {draft.storagePolicy.Enabled && (
              <>
                <SettingRow
                  label={t('agentKind.maxSessionChars')}
                  control={
                    <Input
                      type="number"
                      className="w-32"
                      value={draft.storagePolicy.MaxSessionChars ?? ''}
                      onChange={event => setDraft(prev => prev ? {
                        ...prev,
                        storagePolicy: { ...prev.storagePolicy, MaxSessionChars: Number(event.target.value) },
                      } : prev)}
                      data-guide-id="settings/agent/advanced/max-session-chars"
                    />
                  }
                />
                <SettingRow
                  label={t('agentKind.discardBatchSize')}
                  control={
                    <Input
                      type="number"
                      className="w-32"
                      value={draft.storagePolicy.DiscardBatchSize ?? ''}
                      onChange={event => setDraft(prev => prev ? {
                        ...prev,
                        storagePolicy: { ...prev.storagePolicy, DiscardBatchSize: Number(event.target.value) },
                      } : prev)}
                      data-guide-id="settings/agent/advanced/discard-batch-size"
                    />
                  }
                />
              </>
            )}
          </AkcGroup>

          {/* Random Name */}
          <AkcGroup title={t('agentKind.randomName')}>
            <SettingRow
              label={t('agentKind.enableRandomName')}
              control={
                <Switch
                  checked={draft.randomName?.Enabled ?? false}
                  onCheckedChange={(checked) => setDraft(prev => prev ? {
                    ...prev,
                    randomName: {
                      ...(prev.randomName ?? { Prefixes: [], Suffixes: [] }),
                      Enabled: checked,
                    },
                  } : prev)}
                  data-guide-id="settings/agent/advanced/random-name-enabled"
                />
              }
            />
            {draft.randomName?.Enabled && (
              <>
                <SettingRow label={t('agentKind.prefixes')}>
                  <Textarea
                    rows={2}
                    value={(draft.randomName?.Prefixes ?? []).join(', ')}
                    onChange={event => setDraft(prev => prev ? {
                      ...prev,
                      randomName: {
                        ...(prev.randomName ?? { Enabled: true, Suffixes: [] }),
                        Prefixes: event.target.value.split(',').map(s => s.trim()).filter(Boolean),
                      },
                    } : prev)}
                    placeholder={t('agentKind.prefixesPlaceholder')}
                    data-guide-id="settings/agent/advanced/random-name-prefixes"
                  />
                </SettingRow>
                <SettingRow label={t('agentKind.suffixes')}>
                  <Textarea
                    rows={2}
                    value={(draft.randomName?.Suffixes ?? []).join(', ')}
                    onChange={event => setDraft(prev => prev ? {
                      ...prev,
                      randomName: {
                        ...(prev.randomName ?? { Enabled: true, Prefixes: [] }),
                        Suffixes: event.target.value.split(',').map(s => s.trim()).filter(Boolean),
                      },
                    } : prev)}
                    placeholder={t('agentKind.suffixesPlaceholder')}
                    data-guide-id="settings/agent/advanced/random-name-suffixes"
                  />
                </SettingRow>
              </>
            )}
          </AkcGroup>

          {/* Agent Flags */}
          <AkcGroup title={t('agentKind.agentFlags')}>
            <SettingRow
              label={t('agentKind.userCreatable')}
              control={
                <Switch
                  checked={draft.userCreatable}
                  onCheckedChange={(checked) => setDraft(prev => prev ? { ...prev, userCreatable: checked } : prev)}
                  data-guide-id="settings/agent/advanced/user-creatable"
                />
              }
            />
            <SettingRow
              label={t('agentKind.systemManaged')}
              control={
                <Switch
                  checked={draft.systemManaged}
                  disabled
                  onCheckedChange={() => {}}
                />
              }
            />
          </AkcGroup>
        </section>
        )}
      </div>
    </div>
  )
}
