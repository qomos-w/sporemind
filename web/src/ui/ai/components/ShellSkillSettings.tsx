import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  Plus,
  RefreshCw,
  Search,
  Sparkles,
  X,
  Wrench,
} from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as skillCards from '../../../application/skill-card-store'
import * as workspace from '../../../gen-clients/workspace/client'
import type { Skill } from '../../../domain/skill-types'
import { AgentConfigCard, agentConfigCardSize } from './AgentConfigCard'
import { useAIShellContext } from '../context/AIShellContext'
import { useI18n } from '../../../i18n'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Badge, Button, Field, FieldLabel, Input, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from '../../settings/shadcn/ui'

function SkillGroup({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <h4 className="px-0.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">{title}</h4>
      {children}
    </div>
  )
}

const SOURCE_ORDER: Record<string, number> = {
  builtin: 0,
  project: 1,
  user: 2,
  xdg: 3,
  override: 4,
  external: 5,
}

/** Group a skill into a source bucket for display. External (read-only)
 * provider skills (ext-skill:...) are always bucketed as 'external'. */
function sourceBucket(skill: Skill): string {
  if (skill.Id.startsWith('ext-skill:')) return 'external'
  return SOURCE_ORDER[skill.Source] !== undefined ? skill.Source : 'user'
}

function sortSkills(skills: Skill[]): Skill[] {
  return [...skills].sort((a, b) => {
    const order = (SOURCE_ORDER[sourceBucket(a)] ?? 99) - (SOURCE_ORDER[sourceBucket(b)] ?? 99)
    return order || a.Name.localeCompare(b.Name)
  })
}

type CreateMode = 'blank' | 'creator'

/** Runtime card metadata attached by skill-card-store (not on the domain type). */
type SkillCardMeta = Skill & { CardId?: string }

/** Resolve the mono-card id backing a skill so edit can open it in the right panel. */
function skillCardId(skill: Skill): string {
  const cardId = (skill as SkillCardMeta).CardId
  if (cardId) return cardId
  return skill.Id.includes(':') ? skill.Id : `skill:${skill.Id}`
}

function blankTemplate(name: string): string {
  return `---
name: ${name}
description:
arguments: []
allowed-tools: []
---

`
}

export function ShellSkillSettings() {
  const { t } = useI18n()
  const { onOpenCardForEdit } = useAIShellContext()
  const sourceLabels: Record<string, string> = useMemo(() => ({
    builtin: t('skill.source.builtin'),
    project: t('skill.source.project'),
    user: t('skill.source.user'),
    xdg: t('skill.source.xdg'),
    override: t('skill.source.override'),
    external: t('skill.source.external'),
  }), [t])
  const sourceLabel = useCallback(
    (source?: string): string => sourceLabels[source || ''] || (source || t('skill.source.unknown')),
    [sourceLabels, t],
  )
  const [skills, setSkills] = useState<Skill[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [filterRaw, setFilterRaw] = useState('')
  const [filter, setFilter] = useState('')
  useEffect(() => {
    const timer = setTimeout(() => setFilter(filterRaw), 150)
    return () => clearTimeout(timer)
  }, [filterRaw])

  const [showCreateModal, setShowCreateModal] = useState(false)
  const [createMode, setCreateMode] = useState<CreateMode>('blank')
  const [createName, setCreateName] = useState(() => t('skill.create.defaultName'))
  const [creating, setCreating] = useState(false)
  const [projectNameById, setProjectNameById] = useState<Map<string, string>>(new Map())

  useEffect(() => {
    workspace.listProject(client).then(resp => {
      const m = new Map<string, string>()
      for (const p of resp.Items ?? []) {
        if (p.ActorId) m.set(p.ActorId, p.Name)
      }
      setProjectNameById(m)
    }).catch(() => {/* best-effort */})
  }, [])

  const load = useCallback(async (): Promise<Skill[]> => {
    setLoading(true)
    setError('')
    try {
      // Resolve same-name overrides (project-internal > system-internal >
      // project-external) so the display list shows only the winning skill per
      // name, consistent with Slash / AgentKindConfig / manifest. Uncovered
      // external (read-only) skills remain visible and click-through-able.
      const sorted = sortSkills(skillCards.resolveSkillOverrides(await skillCards.listSkills(client)))
      setSkills(sorted)
      return sorted
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return []
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const counts = useMemo(() => {
    const count = (bucket: string) => skills.filter(s => sourceBucket(s) === bucket).length
    return {
      builtin: count('builtin'),
      project: count('project'),
      user: count('user'),
      xdg: count('xdg'),
      override: count('override'),
      external: count('external'),
    }
  }, [skills])

  const openCreateModal = useCallback(() => {
    setError('')
    setCreateMode('blank')
    setCreateName(t('skill.create.defaultName'))
    setShowCreateModal(true)
  }, [t])

  const closeCreateModal = useCallback(() => {
    setShowCreateModal(false)
    setCreating(false)
  }, [])

  const handleCreate = useCallback(async () => {
    setError('')
    setCreating(true)
    const oldIds = new Set(skills.map(s => s.Id))
    try {
      if (createMode === 'blank') {
        const name = createName.trim() || t('skill.create.defaultName')
        await skillCards.saveSkill(client, {
          Id: '',
          Name: name,
          Description: '',
          Tags: [],
          Tools: [],
          Params: [],
          Context: 'inline',
          Permission: '',
          SubAgentType: '',
          Unit: { model: '', provider: '' },
          Template: blankTemplate(name),
        })
      } else if (createMode === 'creator') {
        const creator = await skillCards.getSkill(client, 'skill-creator')
        await skillCards.saveSkill(client, {
          Id: '',
          Name: t('skill.create.creatorCopyName', { name: creator.Name || 'skill-creator' }),
          Description: creator.Description,
          Tags: creator.Tags,
          Tools: creator.Tools,
          Params: creator.Params,
          Context: creator.Context || 'inline',
          Permission: creator.Permission,
          SubAgentType: creator.SubAgentType,
          Unit: creator.Unit,
          Template: creator.Template,
        })
      }

      const next = await load()
      setShowCreateModal(false)

      const added = next.find(s => !oldIds.has(s.Id))
      const target = added ?? next[0]
      if (target) {
        onOpenCardForEdit?.(skillCardId(target))
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setCreating(false)
    }
  }, [createMode, createName, load, onOpenCardForEdit, skills, t])

  const filteredSkills = useMemo(() => {
    if (!filter) return skills
    const lower = filter.toLowerCase()
    return skills.filter(skill =>
      skill.Name.toLowerCase().includes(lower) ||
      (skill.Description || '').toLowerCase().includes(lower) ||
      (skill.Tags || []).join(' ').toLowerCase().includes(lower) ||
      (skill.Source || '').toLowerCase().includes(lower) ||
      sourceLabel(skill.Source).toLowerCase().includes(lower)
    )
  }, [skills, filter, sourceLabel])

  const groupedSources = useMemo(() => {
    const bySource = new Map<string, Skill[]>()
    for (const skill of filteredSkills) {
      const bucket = sourceBucket(skill)
      const items = bySource.get(bucket) ?? []
      items.push(skill)
      bySource.set(bucket, items)
    }
    const result: { source: string; items: Skill[] }[] = []
    for (const key of Object.keys(SOURCE_ORDER)) {
      const items = bySource.get(key)
      if (items && items.length > 0) {
        result.push({ source: key, items })
      }
    }
    return result
  }, [filteredSkills])

  return (
    <FeatureCard
      icon={<Wrench size={16} />}
      title={t('settings.skills.title')}
      description={t('settings.skills.desc')}
      action={
        <div className="flex items-center gap-1">
          <Button type="button" size="icon-sm" onClick={openCreateModal} title={t('skill.create.title')} data-guide-id="settings/skills/create">
            <Plus size={14} />
          </Button>
          <Button type="button" variant="ghost" size="icon-sm" onClick={() => void load()} disabled={loading} title={t('common.refresh')} data-guide-id="settings/skills/refresh">
            <RefreshCw size={14} />
          </Button>
        </div>
      }
    >
      <div className="flex flex-wrap items-center gap-1.5 text-sm">
        <span className="font-medium">{skills.length}</span>
        <Badge variant="secondary">{sourceLabels.builtin} {counts.builtin}</Badge>
        <Badge variant="secondary">{sourceLabels.project} {counts.project}</Badge>
        <Badge variant="secondary">{sourceLabels.user} {counts.user}</Badge>
        <Badge variant="secondary">{sourceLabels.xdg} {counts.xdg}</Badge>
        <Badge variant="secondary">{sourceLabels.override} {counts.override}</Badge>
        {counts.external > 0 && <Badge variant="secondary">{sourceLabels.external} {counts.external}</Badge>}
      </div>

      <div className="relative">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input className="pl-8" value={filterRaw} onChange={e => setFilterRaw(e.target.value)} placeholder={t('skill.placeholder.filter')} data-guide-id="settings/skills/filter" />
      </div>

      {showCreateModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={closeCreateModal}>
          <div
            className="flex w-[420px] max-w-[92vw] flex-col rounded-xl border border-border bg-background shadow-lg"
            onClick={e => e.stopPropagation()}
          >
            <div className="flex items-center justify-between border-b border-border px-4 py-3">
              <span className="text-sm font-semibold">{t('skill.create.title')}</span>
              <Button type="button" variant="ghost" size="icon-sm" onClick={closeCreateModal} aria-label={t('common.cancel')}>
                <X />
              </Button>
            </div>

            <div className="flex flex-col gap-4 px-4 py-4">
              <Field>
                <FieldLabel>{t('skill.create.mode')}</FieldLabel>
                <SelectRoot
                  value={createMode}
                  onValueChange={(v) => setCreateMode(v as CreateMode)}
                  items={[
                    { value: 'blank', label: t('skill.create.modeBlank') },
                    { value: 'creator', label: t('skill.create.modeCreator') },
                  ]}
                >
                  <SelectTrigger data-guide-id="settings/skills/create-mode">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="blank" data-guide-id="settings/skills/create-mode/blank">
                      <SelectItemText>{t('skill.create.modeBlank')}</SelectItemText>
                    </SelectItem>
                    <SelectItem value="creator" data-guide-id="settings/skills/create-mode/creator">
                      <SelectItemText>{t('skill.create.modeCreator')}</SelectItemText>
                    </SelectItem>
                  </SelectContent>
                </SelectRoot>
              </Field>

              {createMode === 'blank' && (
                <Field>
                  <FieldLabel>{t('skill.create.name')}</FieldLabel>
                  <Input
                    value={createName}
                    onChange={e => setCreateName(e.target.value)}
                    placeholder={t('skill.placeholder.name')}
                    data-guide-id="settings/skills/create-name"
                    onKeyDown={e => {
                      if (e.key === 'Enter' && !creating) void handleCreate()
                    }}
                  />
                </Field>
              )}

              {error && <p className="text-sm text-destructive">{error}</p>}
            </div>

            <div className="flex justify-end gap-2 border-t border-border px-4 py-3">
              <Button type="button" variant="outline" size="sm" onClick={closeCreateModal} disabled={creating}>{t('common.cancel')}</Button>
              <Button type="button" size="sm" onClick={() => void handleCreate()} disabled={creating} data-guide-id="settings/skills/create-submit">
                {creating ? t('common.creating') : t('common.create')}
              </Button>
            </div>
          </div>
        </div>
      )}

      {error && !showCreateModal && <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>}

      <div className="flex flex-col gap-4">
        {loading && skills.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t('skill.loading')}</p>
        ) : filteredSkills.length === 0 ? (
          <p className="text-sm text-muted-foreground">{filter ? t('skill.emptyFilter') : t('skill.empty')}</p>
        ) : (
          groupedSources.map(({ source, items }) => (
            <SkillGroup key={source} title={sourceLabel(source)}>
              <div className="flex flex-wrap gap-3">
                {items.map(skill => {
                  const openInRightPanel = () => onOpenCardForEdit?.(skillCardId(skill))
                  return (
                    <AgentConfigCard
                      key={skill.Id}
                      iconFallback={<Sparkles size={16} />}
                      title={skill.Name}
                      description={skill.Description}
                      tags={[
                        sourceLabel(skill.Source),
                        ...(!skill.Editable ? [t('skill.tag.readOnly')] : []),
                        ...(source === 'project' && skill.ProjectID && projectNameById.get(skill.ProjectID)
                          ? [projectNameById.get(skill.ProjectID)!] : []),
                        ...skill.Tags,
                      ]}
                      className={agentConfigCardSize}
                      onClick={openInRightPanel}
                      onEdit={openInRightPanel}
                    />
                  )
                })}
              </div>
            </SkillGroup>
          ))
        )}
      </div>
    </FeatureCard>
  )
}
