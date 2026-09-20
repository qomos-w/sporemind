import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Check, ChevronDown, Loader2, Package, Plug, X } from 'lucide-react'
import { client } from '../../../application/generated-client'
import { buildType } from '../../../config/buildConfig'
import * as projectClient from '../../../gen-clients/project/client'
import * as workspaceWikiClient from '../../../gen-clients/workspace/client'
import * as appmanagerComponentClient from '../../../gen-clients/appmanager/client'
import type { MonoCardListItem } from '../../../gen-clients/system/types'
import { useI18n } from '../../../i18n'
import { projectDisplayName } from '../../../application/project-adapter'
import { Modal } from '../../components/Modal'
import { isDevBuildOnlyBundleCard } from '../hooks/useAgentComponentMounts'
import { localizedComponentLabel, localizedComponentDescription } from './omniboxLabels'
import type { ProjectMenuProject } from './ProjectContextMenu'
import './ProjectPropertiesOverlay.css'

interface ProjectPropertiesOverlayProps {
  project: ProjectMenuProject | null
  onClose: () => void
}

/**
 * Project properties overlay, opened from the sidebar project context menu
 * ("Properties"). The only property today is the project-level default extra
 * bundle list: state lives entirely on the project actor's config card
 * (project.default_bundles_get / _set, routed with { target: projectId });
 * the frontend keeps nothing durable. The bundle catalog is the same merge
 * the composer mount picker uses — workspace system wiki + project card store
 * + appmanager virtual app bundles — restricted to mountable bundles.
 */
export function ProjectPropertiesOverlay({ project, onClose }: ProjectPropertiesOverlayProps) {
  const { t } = useI18n()
  const projectId = project?.ProjectID ?? null
  const [bundleCards, setBundleCards] = useState<MonoCardListItem[]>([])
  const [selected, setSelected] = useState<string[]>([])
  const [saved, setSaved] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!projectId) return
    let active = true
    setLoading(true)
    setError('')
    const loads: Promise<MonoCardListItem[]>[] = [
      workspaceWikiClient.wikiListCards(client, { Flat: true, IncludeRaw: false, IncludeBuiltin: true, Limit: -1 })
        .then(r => r.Cards ?? []),
      projectClient.wikiListCards(client, { Flat: true, IncludeRaw: false, IncludeBuiltin: true, Limit: -1 }, { target: projectId })
        .then(r => r.Cards ?? [])
        .catch(() => [] as MonoCardListItem[]),
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
            tools: (d.Tools ?? []).map(tool => tool.CallableId),
          },
        } as MonoCardListItem)))
        .catch(() => [] as MonoCardListItem[]),
    ]
    Promise.all([
      Promise.all(loads),
      projectClient.defaultBundlesGet(client, {}, { target: projectId }),
    ]).then(([lists, defaultsResp]) => {
      if (!active) return
      const byId = new Map<string, MonoCardListItem>()
      lists.forEach(cards => cards.forEach(card => { if (card.Id) byId.set(card.Id, card) }))
      const isDevBuild = buildType === 'dev'
      // Normalize component kind from Data.componentKind (builtin convention)
      // or the frontmatter Type (app-published cards), same as
      // useAgentComponentMounts; then keep only independently mountable
      // bundles — modes and modeManaged bundles are kind-config driven.
      const bundles = [...byId.values()]
        .map(card => {
          const declared = card.Data?.componentKind
          const kind = declared === 'bundle' || declared === 'mode'
            ? declared
            : (card.Type === 'bundle' || card.Type === 'mode' ? card.Type : undefined)
          if (!kind) return null
          if (declared === kind) return card
          return { ...card, Data: { ...card.Data, componentKind: kind } }
        })
        .filter((card): card is MonoCardListItem => card !== null)
        .filter(card => card.Data?.componentKind === 'bundle')
        .filter(card => card.Data?.modeManaged !== true)
        .filter(card => isDevBuild || !isDevBuildOnlyBundleCard(card))
      setBundleCards(bundles)
      const defaults = defaultsResp.BundleIDs ?? []
      setSelected(defaults)
      setSaved(defaults)
    }).catch(err => {
      if (!active) return
      setError(err instanceof Error ? err.message : String(err))
    }).finally(() => {
      if (active) setLoading(false)
    })
    return () => { active = false }
  }, [projectId])

  const sortedBundles = useMemo(
    () => [...bundleCards].sort((a, b) => localizedComponentLabel(a, t).localeCompare(localizedComponentLabel(b, t))),
    [bundleCards, t],
  )

  const bundleOptions = useMemo(
    () => sortedBundles.map(card => ({
      id: card.Id,
      title: localizedComponentLabel(card, t),
      description: localizedComponentDescription(card, t) ?? undefined,
      icon: typeof card.Data?.icon === 'string' ? card.Data.icon : undefined,
    })),
    [sortedBundles, t],
  )

  const toggle = useCallback((id: string) => {
    setSelected(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id])
  }, [])

  const dirty = useMemo(
    () => JSON.stringify(selected) !== JSON.stringify(saved),
    [selected, saved],
  )

  const handleSave = useCallback(async () => {
    if (!projectId || saving) return
    setSaving(true)
    setError('')
    try {
      const resp = await projectClient.defaultBundlesSet(client, { BundleIDs: selected }, { target: projectId })
      setSaved(resp.BundleIDs ?? [])
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }, [projectId, saving, selected, onClose])

  if (!project) return null

  return (
    <Modal
      open
      title={
        <>
          <span className="project-properties-title">{t('projectProperties.title')}</span>
          <span className="project-properties-subtitle">{projectDisplayName(project, t)}</span>
        </>
      }
      onClose={onClose}
      disableClose={saving}
      size="md"
      className="project-properties-overlay"
      footer={
        <>
          <button type="button" className="modal-action modal-action--secondary" onClick={onClose} disabled={saving}>
            {t('common.cancel')}
          </button>
          <button
            type="button"
            className="modal-action modal-action--primary"
            onClick={() => { void handleSave() }}
            disabled={!dirty || saving}
          >
            {t('common.save')}
          </button>
        </>
      }
    >
      <section className="flex flex-col gap-3">
        <header className="flex flex-col gap-0.5">
          <span className="text-sm font-medium">{t('projectProperties.defaultBundles')}</span>
          <span className="text-xs text-muted-foreground">{t('projectProperties.defaultBundlesDesc')}</span>
        </header>
        {error && (
          <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {t('projectProperties.error', { error })}
          </div>
        )}
        {loading ? (
          <div className="flex items-center justify-center gap-2 py-8 text-sm text-muted-foreground">
            <Loader2 size={16} className="animate-spin" />
            {t('common.loading')}
          </div>
        ) : (
          <BundleMultiSelect
            options={bundleOptions}
            selected={selected}
            onToggle={toggle}
            disabled={saving}
          />
        )}
      </section>
    </Modal>
  )
}

interface BundleOption {
  id: string
  title: string
  description?: string
  icon?: string
}

interface BundleMultiSelectProps {
  options: BundleOption[]
  selected: string[]
  onToggle: (id: string) => void
  disabled?: boolean
}

// MCP external cards carry data.icon="plug" — map it to the plug glyph so
// MCP servers are visually distinct from builtin/app bundles in the list.
function bundleOptionIcon(icon: string | undefined, size: number) {
  return icon === 'plug' ? <Plug size={size} /> : <Package size={size} />
}

/**
 * Compact multi-select for the default-extra-bundles list: selected bundles
 * render as removable chips, the checklist expands in flow under the trigger
 * (a floating popover would be clipped by .modal-body's overflow-y:auto).
 * Selected ids that no longer resolve to a catalog option still render as
 * chips (raw id + "unknown" hint) so stale references stay visible and
 * removable.
 */
function BundleMultiSelect({ options, selected, onToggle, disabled = false }: BundleMultiSelectProps) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      // Collapse the list first; only a second Escape reaches the Modal's
      // close handler (capture phase beats the Modal's bubble listener).
      e.preventDefault()
      e.stopPropagation()
      setOpen(false)
    }
    const handlePointerDown = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('keydown', handleKeyDown, true)
    document.addEventListener('mousedown', handlePointerDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown, true)
      document.removeEventListener('mousedown', handlePointerDown)
    }
  }, [open])

  const optionById = useMemo(() => new Map(options.map(o => [o.id, o])), [options])

  return (
    <div className="pp-bundle-select" ref={containerRef}>
      {selected.length > 0 && (
        <div className="pp-bundle-chips">
          {selected.map(id => {
            const option = optionById.get(id)
            const label = option?.title ?? id
            const hint = option?.description ?? (option ? undefined : t('projectProperties.unknownBundle'))
            return (
              <span key={id} className="pp-bundle-chip" title={hint ?? label}>
                <span className="pp-bundle-chip-icon">{bundleOptionIcon(option?.icon, 11)}</span>
                <span className="pp-bundle-chip-label">{label}</span>
                <button
                  type="button"
                  className="pp-bundle-chip-x"
                  onClick={() => onToggle(id)}
                  disabled={disabled}
                  aria-label={label}
                >
                  <X size={10} />
                </button>
              </span>
            )
          })}
        </div>
      )}
      <button
        type="button"
        className="pp-bundle-trigger"
        onClick={() => { setOpen(v => !v) }}
        disabled={disabled}
        aria-expanded={open}
      >
        <span>
          {selected.length > 0
            ? t('projectProperties.selectedCount', { count: selected.length })
            : t('projectProperties.selectBundles')}
        </span>
        <ChevronDown size={14} className={open ? 'pp-bundle-chevron pp-bundle-chevron--open' : 'pp-bundle-chevron'} />
      </button>
      {open && (
        <div className="pp-bundle-list" role="listbox" aria-multiselectable="true">
          {options.length === 0 && (
            <div className="pp-bundle-empty">{t('projectProperties.noBundles')}</div>
          )}
          {options.map(option => {
            const checked = selected.includes(option.id)
            return (
              <button
                key={option.id}
                type="button"
                role="option"
                aria-selected={checked}
                className={checked ? 'pp-bundle-option pp-bundle-option--checked' : 'pp-bundle-option'}
                onClick={() => onToggle(option.id)}
                disabled={disabled}
              >
                <span className="pp-bundle-option-check">{checked && <Check size={13} />}</span>
                <span className="pp-bundle-option-icon">{bundleOptionIcon(option.icon, 13)}</span>
                <span className="pp-bundle-option-text">
                  <span className="pp-bundle-option-title">{option.title}</span>
                  {option.description && (
                    <span className="pp-bundle-option-desc" title={option.description}>{option.description}</span>
                  )}
                </span>
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
