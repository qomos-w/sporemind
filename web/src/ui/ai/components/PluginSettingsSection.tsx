import React, { useCallback, useState, useSyncExternalStore } from 'react'
import { Loader2, Plus, FolderPlus, Puzzle, Power, PowerOff, RotateCcw, Trash2, X, ChevronDown, ShieldCheck, ExternalLink } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as appmanagerClient from '../../../gen-clients/appmanager/client'
import { appRegistry, EMPTY_APP_ENTRIES, normalizeApp } from '../../../application/app-registry'
import type { AppEntry } from '../../../application/app-registry'
import { isAppOpenable } from '../../../application/app-openable'
export { OPENABLE_APP_STATES, isAppOpenable } from '../../../application/app-openable'
import { useI18n } from '../../../i18n'
import type { I18nKey } from '../../../i18n/types'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Badge, Separator } from '../../settings/shadcn/ui'
import { useLocalAppInstall } from '../hooks/useLocalAppInstall'
import { InstallPreviewDialog } from '../../components/InstallPreviewDialog'
import { formatHashShort } from './plugin-detail-helpers'
import { getCapabilityInfo } from '../../../application/capability-catalog'

export type PluginAction = 'load' | 'unload'

export interface PluginStateView {
  /** Normal action button that is enabled in this state; null when none. */
  action: PluginAction | null
  /** All buttons disabled (in-flight request or transitional/pending state). */
  blocked: boolean
  /** Show the amber "restart required" badge for unload_pending/restart_pending. */
  restartBadge: 'unload_pending' | 'restart_pending' | null
  /** Show an in-progress spinner (unloading / protocol cleanup / cleanup). */
  busy: boolean
  /** Retry action: failed → load, unload_failed → unload. */
  retry: PluginAction | null
  /** Error label for failed/unload_failed states. */
  errorLabel: I18nKey | null
}

/**
 * Map an AppStatus.State string onto the UI surface (buttons/badges/spinner).
 * State is a free string (no schema union), so unknown values fall back to the
 * stopped-equivalent "load allowed" view — the historic behavior where any
 * non-running/non-pending plugin could be started again.
 */
export function pluginStateView(state: string): PluginStateView {
  switch (state) {
    case 'running':
      return { action: 'unload', blocked: false, restartBadge: null, busy: false, retry: null, errorLabel: null }
    case 'unload_pending':
      return { action: null, blocked: true, restartBadge: 'unload_pending', busy: false, retry: null, errorLabel: null }
    case 'restart_pending':
      return { action: null, blocked: true, restartBadge: 'restart_pending', busy: false, retry: null, errorLabel: null }
    case 'failed':
      return { action: null, blocked: false, restartBadge: null, busy: false, retry: 'load', errorLabel: 'settings.plugins.state.failed' }
    case 'unload_failed':
      return { action: null, blocked: false, restartBadge: null, busy: false, retry: 'unload', errorLabel: 'settings.plugins.state.unload_failed' }
    case 'unloading':
    case 'protocol_cleanup_pending':
    case 'cleanup_pending':
      return { action: null, blocked: true, restartBadge: null, busy: true, retry: null, errorLabel: null }
    default:
      // stopped / unloaded / registered / unknown → load allowed.
      return { action: 'load', blocked: false, restartBadge: null, busy: false, retry: null, errorLabel: null }
  }
}

/**
 * Plugins settings section: load/unload registered native plugins.
 *
 * Data source is the shared app registry (appmanager.list via
 * startAppRegistrySync); load/unload call appmanager.plugin_load /
 * plugin_unload and upsert the returned AppStatus so every surface that
 * watches the registry (including the app panel) updates immediately.
 * Spore-runtime apps are listed for visibility but have no artifact to
 * load/unload — their lifecycle is register/unregister.
 */
export interface PluginSettingsSectionProps {
  developerMode?: boolean
  onDeveloperModeChange?: (enabled: boolean) => void
  /** Open the app's primary view in the right panel (the shell injects this). */
  onOpenAppView?: (app: AppEntry) => void
}

export const PluginSettingsSection: React.FC<PluginSettingsSectionProps> = ({ developerMode, onOpenAppView }) => {
  const { t } = useI18n()
  const apps = useSyncExternalStore(
    (listener) => appRegistry.subscribe(listener),
    () => appRegistry.getAll(),
    () => EMPTY_APP_ENTRIES,
  )
  const [busyId, setBusyId] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [confirmId, setConfirmId] = useState<string | null>(null)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const localInstall = useLocalAppInstall()

  const run = useCallback(async (app: AppEntry, action: 'load' | 'unload') => {
    setBusyId(app.id)
    setError(null)
    try {
      const resp = action === 'load'
        ? await appmanagerClient.pluginLoad(client, { Id: app.id })
        : await appmanagerClient.pluginUnload(client, { Id: app.id })
      appRegistry.upsert(normalizeApp(resp.Status))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusyId(null)
    }
  }, [])

  // Unregister deletes the registration entirely (agent bindings, protocol
  // and session entries included) — irreversible, hence the inline two-step
  // confirm instead of an immediate fire.
  const runUnregister = useCallback(async (app: AppEntry) => {
    setBusyId(app.id)
    setError(null)
    try {
      await appmanagerClient.unregister(client, { Id: app.id })
      appRegistry.remove(app.id)
      setConfirmId(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusyId(null)
    }
  }, [])

  return (
    <FeatureCard
      icon={<Puzzle size={16} />}
      title={t('settings.plugins.title')}
      description={t('settings.plugins.desc')}
      action={
        <div className="flex items-center gap-1" data-guide-id="settings/plugins/install-local">
          <button
            type="button"
            className="inline-flex items-center gap-1 rounded border border-border px-2 py-1 text-xs hover:bg-accent disabled:opacity-50"
            disabled={!developerMode || localInstall.state.loading}
            title={developerMode ? t('settings.plugins.installLocalHint') : t('settings.plugins.devModeRequired')}
            onClick={() => { void localInstall.selectAndPreviewZip(client) }}
          >
            <Plus size={14} />
            {t('installPreview.installZip')}
          </button>
          <button
            type="button"
            className="inline-flex items-center gap-1 rounded border border-border px-2 py-1 text-xs hover:bg-accent disabled:opacity-50"
            disabled={!developerMode || localInstall.state.loading}
            title={developerMode ? t('settings.plugins.installLocalHint') : t('settings.plugins.devModeRequired')}
            onClick={() => { void localInstall.selectAndPreviewFolder(client) }}
          >
            <FolderPlus size={14} />
            {t('installPreview.installFolder')}
          </button>
        </div>
      }
    >
      {!developerMode && (
        <div className="text-xs text-muted-foreground">
          {t('settings.plugins.devModeRequired')}
        </div>
      )}
      {localInstall.state.success && (
        <div className="rounded border border-emerald-500/30 bg-emerald-500/10 px-2 py-1 text-xs text-emerald-600 dark:text-emerald-400">
          {localInstall.state.success}
        </div>
      )}
      {apps.length === 0 ? (
        <div className="text-sm text-muted-foreground">{t('settings.plugins.empty')}</div>
      ) : (
        <div className="flex flex-col gap-2">
          {apps.map((app) => {
            const native = app.runtime === 'native'
            const view = pluginStateView(app.state)
            const inFlight = busyId === app.id
            const dotClass = app.state === 'running'
              ? 'bg-primary'
              : view.restartBadge
                ? 'bg-amber-500'
                : view.errorLabel
                  ? 'bg-destructive'
                  : 'bg-muted-foreground'
            const expanded = expandedId === app.id
            const openable = isAppOpenable(app)
            const pkgHash = formatHashShort(app.packageHash)
            const artifactHash = formatHashShort(app.artifactHash ?? app.packageHash)
            return (
              <div key={app.id} className="rounded-lg border border-border">
                <div className="flex items-center gap-3 px-3 py-2.5">
                  <button
                    type="button"
                    aria-expanded={expanded}
                    aria-label={t('settings.plugins.detail.expand')}
                    className="shrink-0 rounded p-1 text-muted-foreground hover:bg-accent disabled:opacity-50"
                    disabled={inFlight}
                    onClick={() => setExpandedId(expanded ? null : app.id)}
                  >
                    <ChevronDown size={16} className={`transition-transform ${expanded ? 'rotate-180' : ''}`} />
                  </button>
                  <span className={`size-2 shrink-0 rounded-full ${dotClass}`} />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-medium" title={app.id}>{app.id}</div>
                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                      <span className="truncate">{app.namespace || app.runtime}</span>
                      <span>v{app.version}</span>
                      <span>{app.state}</span>
                      {view.restartBadge === 'unload_pending' && (
                        <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-amber-600 dark:text-amber-400">
                          {t('settings.plugins.restartHint')}
                        </span>
                      )}
                      {view.restartBadge === 'restart_pending' && (
                        <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-amber-600 dark:text-amber-400">
                          {t('settings.plugins.state.restart_pending')}
                        </span>
                      )}
                      {view.busy && <Loader2 size={12} className="animate-spin" />}
                      {view.errorLabel ? (
                        <span className="truncate text-destructive" title={app.error}>
                          {t(view.errorLabel)}{app.error ? ` — ${app.error}` : ''}
                        </span>
                      ) : app.error ? (
                        <span className="truncate text-destructive" title={app.error}>{app.error}</span>
                      ) : null}
                    </div>
                  </div>
                  {onOpenAppView && openable && (
                    <button
                      type="button"
                      className="rounded border border-border px-2 py-1 text-xs hover:bg-accent disabled:opacity-50"
                      disabled={inFlight}
                      title={t('settings.plugins.openHint')}
                      onClick={() => onOpenAppView(app)}
                    >
                      <span className="inline-flex items-center gap-1"><ExternalLink size={12} />{t('settings.plugins.open')}</span>
                    </button>
                  )}
                  {native && (
                    <div className="flex items-center gap-1">
                      {view.retry ? (
                        <button
                          type="button"
                          className="rounded border border-border px-2 py-1 text-xs hover:bg-accent disabled:opacity-50"
                          disabled={inFlight || view.blocked}
                          onClick={() => run(app, view.retry!)}
                        >
                          <span className="inline-flex items-center gap-1"><RotateCcw size={12} />{t('settings.plugins.state.retry')}</span>
                        </button>
                      ) : (
                        <>
                          <button
                            type="button"
                            className="rounded border border-border px-2 py-1 text-xs hover:bg-accent disabled:opacity-50"
                            disabled={inFlight || view.blocked || view.action !== 'load'}
                            onClick={() => run(app, 'load')}
                          >
                            <span className="inline-flex items-center gap-1"><Power size={12} />{t('settings.plugins.load')}</span>
                          </button>
                          <button
                            type="button"
                            className="rounded border border-border px-2 py-1 text-xs hover:bg-accent disabled:opacity-50"
                            disabled={inFlight || view.blocked || view.action !== 'unload'}
                            onClick={() => run(app, 'unload')}
                          >
                            <span className="inline-flex items-center gap-1"><PowerOff size={12} />{t('settings.plugins.unload')}</span>
                          </button>
                        </>
                      )}
                    </div>
                  )}
                  {confirmId === app.id ? (
                    <div className="flex items-center gap-1">
                      <button
                        type="button"
                        className="rounded border border-destructive/40 bg-destructive/10 px-2 py-1 text-xs text-destructive hover:bg-destructive/20 disabled:opacity-50"
                        disabled={inFlight}
                        onClick={() => runUnregister(app)}
                      >
                        <span className="inline-flex items-center gap-1">
                          {inFlight ? <Loader2 size={12} className="animate-spin" /> : <Trash2 size={12} />}
                          {t('settings.plugins.unregisterConfirm')}
                        </span>
                      </button>
                      <button
                        type="button"
                        className="rounded border border-border px-2 py-1 text-xs hover:bg-accent disabled:opacity-50"
                        disabled={inFlight}
                        onClick={() => setConfirmId(null)}
                      >
                        <span className="inline-flex items-center gap-1"><X size={12} />{t('settings.plugins.unregisterCancel')}</span>
                      </button>
                    </div>
                  ) : (
                    <button
                      type="button"
                      className="rounded border border-border px-2 py-1 text-xs text-destructive hover:bg-destructive/10 disabled:opacity-50"
                      disabled={inFlight}
                      title={t('settings.plugins.unregisterHint')}
                      onClick={() => setConfirmId(app.id)}
                    >
                      <span className="inline-flex items-center gap-1"><Trash2 size={12} />{t('settings.plugins.unregister')}</span>
                    </button>
                  )}
                </div>
                {expanded && (
                  <div className="border-t border-border px-4 py-4 space-y-4">
                    <div>
                      <h4 className="text-xs font-semibold text-muted-foreground uppercase tracking-wide mb-2">
                        {t('settings.plugins.detail.meta')}
                      </h4>
                      <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
                        <dt className="text-muted-foreground">{t('settings.plugins.detail.id')}</dt>
                        <dd className="truncate font-mono" title={app.id}>{app.id}</dd>
                        <dt className="text-muted-foreground">{t('settings.plugins.detail.namespace')}</dt>
                        <dd className="truncate font-mono" title={app.namespace}>{app.namespace ?? '—'}</dd>
                        <dt className="text-muted-foreground">{t('settings.plugins.detail.version')}</dt>
                        <dd className="truncate font-mono">{app.version}</dd>
                        <dt className="text-muted-foreground">{t('settings.plugins.detail.runtime')}</dt>
                        <dd className="truncate font-mono">{app.runtime}</dd>
                        <dt className="text-muted-foreground">{t('settings.plugins.detail.isolation')}</dt>
                        <dd className="truncate font-mono">{app.isolation ?? '—'}</dd>
                        <dt className="text-muted-foreground">{t('settings.plugins.detail.trustClass')}</dt>
                        <dd className="truncate font-mono">{app.trustClass ?? '—'}</dd>
                        <dt className="text-muted-foreground">{t('settings.plugins.detail.packageHash')}</dt>
                        <dd className="truncate font-mono" title={pkgHash.full}>{pkgHash.short || '—'}</dd>
                        <dt className="text-muted-foreground">{t('settings.plugins.detail.artifactHash')}</dt>
                        <dd className="truncate font-mono" title={artifactHash.full}>{artifactHash.short || '—'}</dd>
                      </dl>
                    </div>
                    <Separator />
                    <div>
                      <h4 className="text-xs font-semibold text-muted-foreground uppercase tracking-wide mb-2">
                        {t('settings.plugins.detail.callables')}
                      </h4>
                      {app.callables && app.callables.length > 0 ? (
                        <ul className="space-y-1.5">
                          {app.callables.map((callable) => (
                            <li key={callable.Id} className="flex items-center justify-between gap-2 text-xs">
                              <code className="truncate font-mono" title={callable.Id}>{callable.Id}</code>
                              {callable.Permission ? (
                                <Badge variant="secondary" className="shrink-0 text-[10px]">
                                  {callable.Permission}
                                </Badge>
                              ) : (
                                <span className="text-muted-foreground">—</span>
                              )}
                            </li>
                          ))}
                        </ul>
                      ) : (
                        <p className="text-xs text-muted-foreground">{t('settings.plugins.detail.noCallables')}</p>
                      )}
                    </div>
                    <Separator />
                    <div>
                      <h4 className="text-xs font-semibold text-muted-foreground uppercase tracking-wide mb-2">
                        {t('settings.plugins.detail.entrypoints')}
                      </h4>
                      {app.entrypoints && app.entrypoints.length > 0 ? (
                        <ul className="space-y-1.5">
                          {app.entrypoints.map((entry) => (
                            <li key={entry.id} className="grid grid-cols-[1fr_auto] gap-2 text-xs">
                              <span className="truncate font-mono" title={entry.id}>{entry.id}</span>
                              <div className="flex items-center gap-2 shrink-0">
                                <Badge variant="outline" className="text-[10px]">{entry.kind}</Badge>
                                {entry.route && <span className="text-muted-foreground" title={entry.route}>{entry.route}</span>}
                              </div>
                            </li>
                          ))}
                        </ul>
                      ) : (
                        <p className="text-xs text-muted-foreground">{t('settings.plugins.detail.noEntrypoints')}</p>
                      )}
                    </div>
                    <Separator />
                    <div>
                      <div className="flex items-center justify-between mb-2">
                        <h4 className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                          {t('settings.plugins.detail.capabilities')}
                        </h4>
                        <span className="text-[10px] text-muted-foreground">
                          {t('settings.plugins.detail.globalEffect')}
                        </span>
                      </div>
                      {app.permissions && app.permissions.length > 0 ? (
                        <ul className="space-y-2">
                          {app.permissions.map((capabilityId) => {
                            const info = getCapabilityInfo(capabilityId)
                            return (
                              <li key={capabilityId} className="flex items-center gap-2 rounded-md border border-border px-3 py-2">
                                <ShieldCheck size={14} className="shrink-0 text-emerald-500" />
                                <div className="min-w-0">
                                  <div className="truncate text-xs font-medium" title={capabilityId}>
                                    {info ? t(info.titleKey as I18nKey) : capabilityId}
                                  </div>
                                  {info && (
                                    <div className="truncate text-[10px] text-muted-foreground" title={t(info.descriptionKey as I18nKey)}>
                                      {t(info.descriptionKey as I18nKey)}
                                    </div>
                                  )}
                                </div>
                              </li>
                            )
                          })}
                        </ul>
                      ) : (
                        <p className="text-xs text-muted-foreground">{t('settings.plugins.detail.noCapabilities')}</p>
                      )}
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
      {error && (
        <div className="rounded border border-destructive/30 bg-destructive/10 px-2 py-1 text-xs text-destructive" title={error}>
          {error}
        </div>
      )}
      {localInstall.state.error && (
        <div className="rounded border border-destructive/30 bg-destructive/10 px-2 py-1 text-xs text-destructive" title={localInstall.state.error}>
          {localInstall.state.error}
        </div>
      )}
      <input
        ref={localInstall.fileInputRef}
        type="file"
        accept=".zip"
        style={{ display: 'none' }}
        onChange={async (e) => {
          const file = e.target.files?.[0]
          if (file) {
            await localInstall.handleBrowserFileSelect(file)
          }
          if (localInstall.fileInputRef.current) {
            localInstall.fileInputRef.current.value = ''
          }
        }}
      />
      <InstallPreviewDialog
        open={localInstall.state.previewOpen}
        manifest={localInstall.state.previewManifest}
        loading={localInstall.state.loading}
        error={localInstall.state.error}
        onConfirm={() => { void localInstall.confirmInstall(client) }}
        onCancel={localInstall.closePreview}
      />
    </FeatureCard>
  )
}