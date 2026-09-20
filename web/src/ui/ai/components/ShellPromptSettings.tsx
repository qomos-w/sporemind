import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { MessageSquareText, Plus, Puzzle, RefreshCw, Search, X, FileCode } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as promptCards from '../../../application/prompt-card-store'
import * as workspace from '../../../gen-clients/workspace/client'
import type { PromptFragment, PromptProfile } from '../../../gen-types/prompt'
import { useAIShellContext } from '../context/AIShellContext'
import { useI18n, type I18nKey } from '../../../i18n'
import { AgentConfigCard, agentConfigCardSize } from './AgentConfigCard'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Badge, Button, Field, FieldLabel, Input, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from '../../settings/shadcn/ui'

function PromptGroup({ title, children }: { title: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <h4 className="px-0.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">{title}</h4>
      {children}
    </div>
  )
}

/** Runtime card metadata attached by prompt-card-store (not on the generated types). */
type FragmentCardMeta = PromptFragment & { CardId?: string }
type ProfileCardMeta = PromptProfile & { CardId?: string }

interface PromptAssetItem {
  assetType: 'fragment' | 'profile'
  name: string
  cardId?: string
  pathKey: string
  source?: string
  kind: string
  nodeName: string
  treePath: string
  fileToken: string
  scope?: string
  role?: string
  priority?: number
  fragment?: PromptFragment
  profile?: PromptProfile
  editable: boolean
  deletable: boolean
}

const NEW_PROMPT_DEFAULTS = {
  kind: 'instructions',
  priority: 100,
  content: '',
  scope: '',
  role: '',
} as const

const PROMPT_KIND_OPTIONS = [
  'system',
  'role',
  'environment',
  'git',
  'mounts',
  'intent',
  'tools',
  'instructions',
  'extra',
] as const

const PROMPT_SOURCE_KEYS = {
  builtin: 'prompt.source.builtin',
  override: 'prompt.source.override',
  user: 'prompt.source.user',
  custom: 'prompt.source.custom',
} as const

const PROMPT_KIND_LABEL_KEYS: Record<string, I18nKey> = {
  system: 'prompt.kind.system',
  role: 'prompt.kind.role',
  environment: 'prompt.kind.environment',
  git: 'prompt.kind.git',
  mounts: 'prompt.kind.mounts',
  intent: 'prompt.kind.intent',
  tools: 'prompt.kind.tools',
  instructions: 'prompt.kind.instructions',
  extra: 'prompt.kind.extra',
}

interface NewPromptDraft {
  mode: 'fragment' | 'profile'
  name: string
  kind: string
  priority: number
  scope: string
  role: string
}

function promptSourceKey(source?: string): keyof typeof PROMPT_SOURCE_KEYS {
  if (source === 'builtin' || source === 'override' || source === 'user') return source
  return 'custom'
}

function fragmentAssetPath(fragment: PromptFragment, index: number): string {
  const id = fragment.Key || fragment.Id || [fragment.Kind, fragment.Name, fragment.Scope || '', fragment.Role || '', fragment.Path || '', index].join(':')
  return `prompt-fragment:${encodeURIComponent(id)}`
}

function promptProfileKey(profile: PromptProfile): string {
  if (profile.Key) return profile.Key
  const scope = profile.Scope || 'project'
  const role = profile.Role || ''
  return role ? `${scope}.${role}` : scope
}

function sortProfiles(profiles: PromptProfile[]): PromptProfile[] {
  const sourceOrder: Record<string, number> = { builtin: 0, override: 1, user: 2 }
  return [...profiles].sort((a, b) => {
    const aSource = sourceOrder[a.Source || 'user'] ?? 99
    const bSource = sourceOrder[b.Source || 'user'] ?? 99
    return aSource - bSource || promptProfileKey(a).localeCompare(promptProfileKey(b))
  })
}

function profileNodeName(profile: PromptProfile): string {
  return profile.Role || profile.Key || 'profile'
}

function profileAssetPath(profile: PromptProfile): string {
  const key = profile.Key || `${profile.Scope || ''}.${profile.Role || ''}`
  return `prompt-profile:${encodeURIComponent(['role', key].join(':'))}`
}

function profileFragment(profile: PromptProfile): PromptFragment {
  return {
    Key: profile.Key,
    BuiltinKey: profile.BuiltinKey,
    Name: profileNodeName(profile),
    Kind: 'role',
    Priority: 0,
    Content: profile.RolePrompt,
    Scope: profile.Scope,
    Role: profile.Role,
    Source: profile.Source,
    Path: profile.Key,
    Editable: profile.Editable,
    Deletable: profile.Deletable,
  }
}

function buildPromptAssets(fragments: PromptFragment[], profiles: PromptProfile[]): PromptAssetItem[] {
  const fragmentItems = fragments.map((fragment, index) => ({
    assetType: 'fragment' as const,
    name: fragment.Name,
    cardId: (fragment as FragmentCardMeta).CardId,
    pathKey: fragmentAssetPath(fragment, index),
    source: fragment.Source,
    kind: fragment.Kind,
    nodeName: fragment.Name,
    treePath: fragment.Path || '',
    fileToken: fragmentAssetPath(fragment, index),
    scope: fragment.Scope,
    role: fragment.Role,
    priority: fragment.Priority,
    fragment,
    editable: fragment.Editable,
    deletable: fragment.Deletable,
  }))

  const profileItems: PromptAssetItem[] = []
  profiles.forEach(profile => {
    if (profile.RolePrompt) {
      const fragment = profileFragment(profile)
      profileItems.push({
        assetType: 'profile',
        name: fragment.Name,
        cardId: (profile as ProfileCardMeta).CardId,
        pathKey: profileAssetPath(profile),
        source: profile.Source,
        kind: 'role',
        nodeName: fragment.Name,
        treePath: profile.Key || '',
        fileToken: profileAssetPath(profile),
        scope: profile.Scope,
        role: profile.Role,
        profile,
        fragment,
        editable: profile.Editable,
        deletable: profile.Deletable,
      })
    }
  })

  return [...profileItems, ...fragmentItems]
}

function createNewPromptDraft(fragments: PromptFragment[], defaultName: string): NewPromptDraft {
  return {
    mode: 'fragment',
    name: uniquePromptName(fragments, defaultName),
    kind: NEW_PROMPT_DEFAULTS.kind,
    priority: NEW_PROMPT_DEFAULTS.priority,
    scope: NEW_PROMPT_DEFAULTS.scope,
    role: NEW_PROMPT_DEFAULTS.role,
  }
}

function normalizePromptPriority(value: string): number {
  const parsed = Number.parseInt(value, 10)
  return Number.isFinite(parsed) ? parsed : NEW_PROMPT_DEFAULTS.priority
}

function uniquePromptName(fragments: PromptFragment[], defaultName: string): string {
  const names = new Set(fragments.map(fragment => fragment.Name))
  if (!names.has(defaultName)) return defaultName
  let suffix = 2
  while (names.has(`${defaultName} ${suffix}`)) suffix += 1
  return `${defaultName} ${suffix}`
}

function preview(content?: string): string {
  if (!content) return ''
  const lines = content.split('\n')
  for (const line of lines) {
    const trimmed = line.trim()
    if (trimmed && !trimmed.startsWith('#')) {
      return trimmed.length > 120 ? trimmed.slice(0, 120) + '…' : trimmed
    }
  }
  return ''
}

export function ShellPromptSettings() {
  const { t } = useI18n()
  const { onOpenCardForEdit } = useAIShellContext()
  const sourceLabel = useCallback(
    (source?: string): string => t(PROMPT_SOURCE_KEYS[promptSourceKey(source)]),
    [t],
  )
  const kindLabel = (kind: string): string => {
    const key = PROMPT_KIND_LABEL_KEYS[kind]
    return key ? t(key) : kind
  }
  const [fragments, setFragments] = useState<PromptFragment[]>([])
  const [profiles, setProfiles] = useState<PromptProfile[]>([])
  const [filterRaw, setFilterRaw] = useState('')
  const [filter, setFilter] = useState('')
  useEffect(() => {
    const timer = setTimeout(() => setFilter(filterRaw), 150)
    return () => clearTimeout(timer)
  }, [filterRaw])
  const [showCreateForm, setShowCreateForm] = useState(false)
  const [newPrompt, setNewPrompt] = useState<NewPromptDraft>(() => createNewPromptDraft([], t('prompt.create.defaultName')))
  const [creating, setCreating] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [nextFragments, kindConfigsResp] = await Promise.all([
        promptCards.listFragments(client, { Scope: '', Role: '' }),
        workspace.listAgentKindConfigs(client),
      ])

      const profileKeys = new Set<string>()
      nextFragments.Items.forEach(fragment => {
        if ((fragment.Kind !== 'system' && fragment.Kind !== 'role') || !fragment.Role) return
        profileKeys.add(`${fragment.Scope || ''}.${fragment.Role}`)
      })
      kindConfigsResp.Items.forEach(config => {
        const ref = config.RolePromptRef
        if (ref?.Kind === 'profile' && ref.Key) profileKeys.add(ref.Key)
      })

      const loadedProfiles = (await Promise.all(
        Array.from(profileKeys)
          .map((key) => {
            const lastDot = key.lastIndexOf('.')
            if (lastDot <= 0 || lastDot === key.length - 1) return null
            return {
              Scope: key.slice(0, lastDot),
              Role: key.slice(lastDot + 1),
            }
          })
          .filter((pair): pair is { Scope: string, Role: string } => pair !== null)
          .map(async pair => {
            // A profile key derived from a kind config or fragment may not yet
            // have a backing profile card; skip it rather than aborting the
            // whole panel load.
            try {
              return await promptCards.getProfile(client, pair)
            } catch {
              return null
            }
          })
      )).filter((profile): profile is PromptProfile => profile !== null)

      const sortedProfiles = sortProfiles(loadedProfiles)
      setFragments(nextFragments.Items)
      setProfiles(sortedProfiles)
      return { fragments: nextFragments.Items, profiles: sortedProfiles }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return null
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const promptAssets = useMemo(() => buildPromptAssets(fragments, profiles), [fragments, profiles])
  const builtinCount = useMemo(() => promptAssets.filter(item => item.source === 'builtin').length, [promptAssets])
  const overrideCount = useMemo(() => promptAssets.filter(item => item.source === 'override').length, [promptAssets])
  const userCount = useMemo(() => promptAssets.filter(item => item.source === 'user' || !item.source).length, [promptAssets])
  const profileCount = useMemo(() => promptAssets.filter(item => item.assetType === 'profile').length, [promptAssets])
  const fragmentCount = useMemo(() => promptAssets.filter(item => item.assetType === 'fragment').length, [promptAssets])

  const filteredAssets = useMemo(() => {
    if (!filter) return promptAssets
    const lc = filter.toLowerCase()
    return promptAssets.filter(item =>
      item.name.toLowerCase().includes(lc) ||
      item.kind.toLowerCase().includes(lc) ||
      (item.source || '').toLowerCase().includes(lc) ||
      sourceLabel(item.source).toLowerCase().includes(lc) ||
      preview(item.fragment?.Content || item.profile?.RolePrompt).toLowerCase().includes(lc)
    )
  }, [promptAssets, filter, sourceLabel])

  const profileAssets = useMemo(() => filteredAssets.filter(item => item.assetType === 'profile'), [filteredAssets])
  const fragmentAssets = useMemo(() => filteredAssets.filter(item => item.assetType === 'fragment'), [filteredAssets])

  const openCreatePromptForm = useCallback(() => {
    setError('')
    setNewPrompt(createNewPromptDraft(fragments, t('prompt.create.defaultName')))
    setShowCreateForm(true)
  }, [fragments, t])

  const closeCreatePromptForm = useCallback(() => {
    setShowCreateForm(false)
    setCreating(false)
  }, [])

  const handleCreatePrompt = useCallback(async () => {
    setError('')
    setCreating(true)
    try {
      const trimmedScope = newPrompt.scope.trim()
      const trimmedRole = newPrompt.role.trim()
      if (newPrompt.mode === 'profile') {
        if (!trimmedRole) {
          setError(t('prompt.create.roleRequired'))
          return
        }
        const savedProfile = await promptCards.saveProfile(client, {
          Key: '',
          BuiltinKey: '',
          Scope: trimmedScope,
          Role: trimmedRole,
          RolePrompt: '',
        })
        const next = await load()
        setShowCreateForm(false)
        const matchedProfile = next?.profiles.find(profile => (savedProfile.Key && profile.Key === savedProfile.Key)
          || ((profile.Scope || '') === (savedProfile.Scope || trimmedScope) && (profile.Role || '') === (savedProfile.Role || trimmedRole)))
        const createdCardId = (savedProfile as ProfileCardMeta).CardId || (matchedProfile as ProfileCardMeta | undefined)?.CardId
        if (createdCardId) {
          onOpenCardForEdit?.(createdCardId)
        }
        return
      }

      const created = await promptCards.saveFragment(client, {
        Key: '',
        Name: newPrompt.name.trim() || uniquePromptName(fragments, t('prompt.create.defaultName')),
        Kind: newPrompt.kind,
        Priority: newPrompt.priority,
        Content: NEW_PROMPT_DEFAULTS.content,
        Scope: trimmedScope,
        Role: trimmedRole,
      })
      await load()
      setShowCreateForm(false)
      const createdFragmentCardId = (created as FragmentCardMeta).CardId
      if (createdFragmentCardId) {
        onOpenCardForEdit?.(createdFragmentCardId)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setCreating(false)
    }
  }, [fragments, load, newPrompt, onOpenCardForEdit, t])

  return (
    <FeatureCard
      icon={<FileCode size={16} />}
      title={t('settings.prompts.title')}
      description={t('settings.prompts.desc')}
      action={
        <div className="flex items-center gap-1">
          <Button type="button" size="icon-sm" onClick={openCreatePromptForm} title={t('prompt.create.title')} data-guide-id="settings/prompts/create">
            <Plus size={14} />
          </Button>
          <Button type="button" variant="ghost" size="icon-sm" onClick={() => void load()} disabled={loading} title={t('common.refresh')} data-guide-id="settings/prompts/refresh">
            <RefreshCw size={14} />
          </Button>
        </div>
      }
    >
      <div className="flex flex-wrap items-center gap-1.5 text-sm">
        <span className="font-medium">{promptAssets.length}</span>
        <Badge variant="secondary">{t('prompt.section.profiles')} {profileCount}</Badge>
        <Badge variant="secondary">{t('agentKind.fragmentsSection')} {fragmentCount}</Badge>
        <Badge variant="secondary">{t('prompt.source.builtin')} {builtinCount}</Badge>
        <Badge variant="secondary">{t('prompt.source.override')} {overrideCount}</Badge>
        <Badge variant="secondary">{t('prompt.source.user')} {userCount}</Badge>
      </div>

      <div className="relative">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input className="pl-8" value={filterRaw} onChange={event => setFilterRaw(event.target.value)} placeholder={t('prompt.placeholder.filter')} data-guide-id="settings/prompts/filter" />
      </div>

      {showCreateForm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={closeCreatePromptForm}>
          <div
            className="flex w-[560px] max-w-[92vw] flex-col rounded-xl border border-border bg-background shadow-lg"
            onClick={event => event.stopPropagation()}
          >
            <div className="flex items-center justify-between border-b border-border px-4 py-3">
              <span className="text-sm font-semibold">{t('prompt.create.title')}</span>
              <Button type="button" variant="ghost" size="icon-sm" onClick={closeCreatePromptForm} aria-label={t('common.cancel')}>
                <X />
              </Button>
            </div>

            <div className="flex flex-col gap-4 px-4 py-4">
              <Field>
                <FieldLabel>{t('prompt.create.type')}</FieldLabel>
                <SelectRoot
                  value={newPrompt.mode}
                  onValueChange={(v) => setNewPrompt(prev => ({
                    ...prev,
                    mode: v === 'profile' ? 'profile' : 'fragment',
                    kind: v === 'profile' ? 'system' : prev.kind,
                  }))}
                  items={[
                    { value: 'fragment', label: t('prompt.create.typeFragment') },
                    { value: 'profile', label: t('prompt.create.typeProfile') },
                  ]}
                >
                  <SelectTrigger data-guide-id="settings/prompts/create-type">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="fragment" data-guide-id="settings/prompts/create-type/fragment">
                      <SelectItemText>{t('prompt.create.typeFragment')}</SelectItemText>
                    </SelectItem>
                    <SelectItem value="profile" data-guide-id="settings/prompts/create-type/profile">
                      <SelectItemText>{t('prompt.create.typeProfile')}</SelectItemText>
                    </SelectItem>
                  </SelectContent>
                </SelectRoot>
              </Field>
              {newPrompt.mode === 'fragment' && (
                <Field>
                  <FieldLabel>{t('prompt.create.name')}</FieldLabel>
                  <Input
                    value={newPrompt.name}
                    onChange={event => setNewPrompt(prev => ({ ...prev, name: event.target.value }))}
                    placeholder={t('prompt.placeholder.name')}
                    data-guide-id="settings/prompts/create-name"
                  />
                </Field>
              )}
              {newPrompt.mode === 'fragment' && (
                <Field>
                  <FieldLabel>{t('prompt.create.kind')}</FieldLabel>
                  <SelectRoot
                    value={newPrompt.kind}
                    onValueChange={(v) => setNewPrompt(prev => ({ ...prev, kind: v as string }))}
                    items={PROMPT_KIND_OPTIONS.map(kind => ({ value: kind, label: kindLabel(kind) }))}
                  >
                    <SelectTrigger data-guide-id="settings/prompts/create-kind">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {PROMPT_KIND_OPTIONS.map(kind => (
                        <SelectItem key={kind} value={kind} data-guide-id={`settings/prompts/create-kind/${kind}`}>
                          <SelectItemText>{kindLabel(kind)}</SelectItemText>
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </SelectRoot>
                </Field>
              )}
              {newPrompt.mode === 'fragment' && (
                <Field>
                  <FieldLabel>{t('prompt.create.priority')}</FieldLabel>
                  <Input
                    type="number"
                    value={String(newPrompt.priority)}
                    onChange={event => setNewPrompt(prev => ({ ...prev, priority: normalizePromptPriority(event.target.value) }))}
                    data-guide-id="settings/prompts/create-priority"
                  />
                </Field>
              )}
              <Field>
                <FieldLabel>{t('prompt.create.scope')}</FieldLabel>
                <Input
                  value={newPrompt.scope}
                  onChange={event => setNewPrompt(prev => ({ ...prev, scope: event.target.value }))}
                  placeholder={t('prompt.placeholder.project')}
                  data-guide-id="settings/prompts/create-scope"
                />
              </Field>
              <Field>
                <FieldLabel>{t('prompt.create.role')}</FieldLabel>
                <Input
                  value={newPrompt.role}
                  onChange={event => setNewPrompt(prev => ({ ...prev, role: event.target.value }))}
                  placeholder={t('prompt.placeholder.coder')}
                  data-guide-id="settings/prompts/create-role"
                  onKeyDown={event => {
                    if (event.key === 'Enter' && !creating) void handleCreatePrompt()
                  }}
                />
              </Field>

              {error && <p className="text-sm text-destructive">{error}</p>}
            </div>

            <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
              <Button type="button" variant="outline" size="sm" onClick={closeCreatePromptForm} disabled={creating}>{t('common.cancel')}</Button>
              <Button type="button" size="sm" onClick={() => void handleCreatePrompt()} disabled={creating} data-guide-id="settings/prompts/create-submit">
                {creating ? t('common.creating') : t('common.create')}
              </Button>
            </div>
          </div>
        </div>
      )}

      {error && !showCreateForm && <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>}

      <div className="flex flex-col gap-4">
        {loading && promptAssets.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t('prompt.loading')}</p>
        ) : filteredAssets.length === 0 ? (
          <p className="text-sm text-muted-foreground">{filter ? t('prompt.emptyFilter') : t('prompt.empty')}</p>
        ) : (
          <>
            {profileAssets.length > 0 && (
              <PromptGroup
                title={
                  <>
                    {t('prompt.section.profiles')}
                    {' '}
                    <Badge variant="secondary">{profileAssets.length}</Badge>
                  </>
                }
              >
                <div className="flex flex-wrap gap-3">
                  {profileAssets.map(asset => {
                    const openInRightPanel = asset.cardId ? () => onOpenCardForEdit?.(asset.cardId!) : undefined
                    return (
                      <AgentConfigCard
                        key={asset.fileToken}
                        iconFallback={<MessageSquareText size={16} />}
                        title={asset.name}
                        description={preview(asset.fragment?.Content || asset.profile?.RolePrompt)}
                        tags={[sourceLabel(asset.source), ...(asset.role ? [asset.role] : [])]}
                        className={agentConfigCardSize}
                        onClick={openInRightPanel}
                        onEdit={openInRightPanel}
                      />
                    )
                  })}
                </div>
              </PromptGroup>
            )}
            {fragmentAssets.length > 0 && (
              <PromptGroup
                title={
                  <>
                    {t('agentKind.fragmentsSection', { defaultValue: 'Fragments' })}
                    {' '}
                    <Badge variant="secondary">{fragmentAssets.length}</Badge>
                  </>
                }
              >
                <div className="flex flex-wrap gap-3">
                  {fragmentAssets.map(asset => {
                    const openInRightPanel = asset.cardId ? () => onOpenCardForEdit?.(asset.cardId!) : undefined
                    return (
                      <AgentConfigCard
                        key={asset.fileToken}
                        iconFallback={<Puzzle size={16} />}
                        title={asset.name}
                        description={preview(asset.fragment?.Content || asset.profile?.RolePrompt)}
                        tags={[sourceLabel(asset.source), kindLabel(asset.kind)]}
                        className={agentConfigCardSize}
                        onClick={openInRightPanel}
                        onEdit={openInRightPanel}
                      />
                    )
                  })}
                </div>
              </PromptGroup>
            )}
          </>
        )}
      </div>
    </FeatureCard>
  )
}

