import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { ChevronDown, ChevronRight, FolderOpen, RotateCw, Terminal } from 'lucide-react'
import { appRegistry, type AppEntry } from '../../application/app-registry'
import { client } from '../../application/generated-client'
import { isWails } from '../../application/runtime'
import * as desktopApp from '../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import { appdataUsage } from '../../gen-clients/pluginhost/client'
import * as projectClient from '../../gen-clients/project/client'
import { gitStore } from '../panels/git-store'
import type { AppBundle, AppCallableDescriptor, AppEventDescriptor, AppObjectDescriptor, AppTypeDescriptor } from '../../gen-types/app'
import { useI18n } from '../../i18n'
import { Badge, Separator } from '../settings/shadcn/ui'
import { formatHashShort } from '../ai/components/plugin-detail-helpers'
import {
  getLocalFileDragActive,
  getLocalFileDragPayload,
  subscribeLocalFileDrag,
} from '../file-dnd'
import { OS_DROP_TARGET_ATTR } from '../os-file-dnd'
import { PluginIframe } from './PluginIframe'
import { PluginLogPanel } from './PluginLogPanel'
import { HoverTip } from './HoverTip'
import './PluginToolbar.css'

type ToolbarTone = 'ok' | 'warn' | 'err' | 'idle' | 'pending'

interface ToolbarStatus {
  tone: ToolbarTone
  label: string
  detail?: string
}

function statusFor(entry: AppEntry | undefined): ToolbarStatus {
  if (!entry) return { tone: 'pending', label: 'Connecting…' }
  const detail = entry.error || undefined
  switch (entry.state) {
    case 'running': return { tone: 'ok', label: 'Running', detail }
    case 'restart_pending': return { tone: 'warn', label: 'Restarting…', detail }
    case 'unload_pending': return { tone: 'warn', label: 'Unloading…', detail }
    case 'crashed': return { tone: 'err', label: 'Crashed', detail }
    case 'failed': return { tone: 'err', label: 'Failed', detail }
    case 'stopped': return { tone: 'idle', label: 'Stopped', detail }
    default: return { tone: 'idle', label: entry.state || 'Unknown', detail }
  }
}

export interface PluginToolbarProps {
  entry: AppEntry | undefined
  refreshing: boolean
  canRefresh: boolean
  expanded: boolean
  showLogs: boolean
  canOpenStorage: boolean
  onToggleDetails: () => void
  onRefresh: () => void
  onToggleLogs: () => void
  onOpenStorage: () => void
}

export function PluginToolbar({ entry, refreshing, canRefresh, expanded, showLogs, canOpenStorage, onToggleDetails, onRefresh, onToggleLogs, onOpenStorage }: PluginToolbarProps) {
  const { t } = useI18n()
  const status = statusFor(entry)
  const programName = entry?.namespace || entry?.id || ''
  return (
    <div className="plugin-toolbar" data-testid="plugin-toolbar">
      <div className="plugin-toolbar-left">
        <button
          type="button"
          className="plugin-toolbar-btn plugin-toolbar-toggle"
          title={String(t('settings.plugins.detail.expand'))}
          aria-label={String(t('settings.plugins.detail.expand'))}
          aria-expanded={expanded}
          data-testid="plugin-toolbar-toggle"
          onClick={onToggleDetails}
        >
          {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        </button>
        <span className="plugin-toolbar-name" data-testid="plugin-toolbar-name" title={entry?.id}>
          {programName}
        </span>
        {entry?.runtime && <span className="plugin-toolbar-badge">{entry.runtime}</span>}
        {entry?.version && <span className="plugin-toolbar-version">v{entry.version}</span>}
        <div className="plugin-toolbar-status" title={status.detail}>
          <span className={`plugin-toolbar-dot ${status.tone}`} aria-hidden />
          <span className="plugin-toolbar-label" data-testid="plugin-toolbar-label">{status.label}</span>
        </div>
      </div>
      <div className="plugin-toolbar-actions">
        <button
          type="button"
          className="plugin-toolbar-btn"
          title="Reload plugin view"
          aria-label="Reload plugin view"
          disabled={refreshing || !canRefresh}
          onClick={onRefresh}
        >
          <RotateCw size={14} className={refreshing ? 'plugin-toolbar-spin' : undefined} />
        </button>
        <button
          type="button"
          className="plugin-toolbar-btn"
          title={String(t('settings.plugins.openStorage'))}
          aria-label={String(t('settings.plugins.openStorage'))}
          disabled={!canOpenStorage}
          data-testid="plugin-toolbar-storage"
          onClick={onOpenStorage}
        >
          <FolderOpen size={14} />
        </button>
        <button
          type="button"
          className={`plugin-toolbar-btn${showLogs ? ' plugin-toolbar-btn-active' : ''}`}
          title={String(t('settings.plugins.logs'))}
          aria-label={String(t('settings.plugins.logs'))}
          aria-pressed={showLogs}
          data-testid="plugin-toolbar-logs"
          onClick={onToggleLogs}
        >
          <Terminal size={14} />
        </button>
      </div>
    </div>
  )
}

function typeToString(t?: AppTypeDescriptor): string {
  if (!t) return 'any'
  if (t.Key && t.Value) return `map<${typeToString(t.Key)}, ${typeToString(t.Value)}>`
  if (t.Element) return `${typeToString(t.Element)}[]`
  return t.Name || t.ClassName || (t.ClassId !== undefined ? `#${t.ClassId}` : 'any')
}

/**
 * Renders a schema name that, on hover, shows the matching object schema
 * (fields + types) in a fixed-position tooltip.
 */
function SchemaName({ name, descriptor }: { name: string; descriptor?: AppObjectDescriptor }) {
  return (
    <HoverTip
      testId={`schema-tip-${name}`}
      disabled={!descriptor}
      className={`plugin-schema-name${descriptor ? ' has-schema' : ''}`}
      tip={descriptor && (
        <>
          <div className="plugin-schema-tip-title">{descriptor.Name}</div>
          {descriptor.Fields.length === 0 ? (
            <div className="plugin-schema-tip-empty">empty object</div>
          ) : descriptor.Fields.map((f) => (
            <div className="plugin-schema-tip-field" key={f.Name}>
              <span className="plugin-schema-tip-field-name">{f.Name}{f.Optional ? '?' : ''}</span>
              <span className="plugin-schema-tip-field-type">{typeToString(f.Type)}</span>
              {f.Description && <span className="plugin-schema-tip-field-desc">{f.Description}</span>}
            </div>
          ))}
        </>
      )}
    >
      {name}
    </HoverTip>
  )
}

function CallableRow({ c, flash, schemas }: { c: AppCallableDescriptor; flash: boolean; schemas?: Map<string, AppObjectDescriptor> }) {
  return (
    <li className={`plugin-detail-row flex items-start justify-between gap-2 text-xs${flash ? ' flash' : ''}`} data-callable-id={c.Id}>
      <div className="flex min-w-0 items-baseline gap-2">
        {/* Description lives in the hover tip, not an inline line: long text
            would otherwise double every callable row's height. */}
        <HoverTip
          testId={`callable-tip-${c.Id}`}
          disabled={!c.Description}
          className={`plugin-callable-id${c.Description ? ' has-desc' : ''}`}
          tip={<div className="plugin-schema-tip-desc">{c.Description}</div>}
        >
          <code className="font-mono">{c.Id}</code>
        </HoverTip>
        <span className="truncate font-mono text-muted-foreground">
          <SchemaName name={c.RequestSchema} descriptor={schemas?.get(c.RequestSchema)} />
          {' → '}
          <SchemaName name={c.ResponseSchema} descriptor={schemas?.get(c.ResponseSchema)} />
        </span>
      </div>
      <span className="flex shrink-0 items-center gap-1">
        {c.Effect && <Badge variant="outline" className="text-[10px]">{c.Effect}</Badge>}
        {c.Streaming && <Badge variant="outline" className="text-[10px]">stream</Badge>}
        {c.Permission
          ? <Badge variant="secondary" className="text-[10px]">{c.Permission}</Badge>
          : <span className="text-muted-foreground">—</span>}
      </span>
    </li>
  )
}

function EventRow({ e }: { e: AppEventDescriptor }) {
  return (
    <li className="flex items-center justify-between gap-2 text-xs">
      <span className="flex min-w-0 items-baseline gap-2">
        <code className="truncate font-mono" title={e.Id}>{e.Id}</code>
        <span className="truncate font-mono text-muted-foreground">{e.PayloadSchema}</span>
      </span>
      {e.Permission
        ? <Badge variant="secondary" className="text-[10px]">{e.Permission}</Badge>
        : <span className="text-muted-foreground">—</span>}
    </li>
  )
}

function BundleRow({ b, onLocate }: { b: AppBundle; onLocate: (callableId: string) => void }) {
  return (
    <div className="space-y-1">
      <div className="flex items-baseline gap-2 text-xs">
        <span className="truncate font-medium" title={b.Title}>{b.Title}</span>
        {b.Description && <span className="truncate text-muted-foreground" title={b.Description}>{b.Description}</span>}
      </div>
      <div className="plugin-detail-tools">
        {(b.Tools ?? []).map((tool) => (
          <button
            key={tool.CallableId}
            type="button"
            className="plugin-tool-badge"
            title={`Locate callable “${tool.CallableId}”`}
            onClick={() => onLocate(tool.CallableId)}
          >
            <span className="plugin-tool-badge-name">{tool.ToolName || tool.CallableId}</span>
            {tool.Effect && <span className="plugin-tool-badge-tag">{tool.Effect}</span>}
            {tool.Stream && <span className="plugin-tool-badge-tag">stream</span>}
          </button>
        ))}
      </div>
    </div>
  )
}

function DetailSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div>
      <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{title}</h4>
      {children}
    </div>
  )
}

export function PluginDetails({ entry }: { entry: AppEntry }) {
  const { t } = useI18n()
  const callables = entry.callables ?? []
  const events = entry.events ?? []
  const bundles = entry.bundles ?? []
  const pkgHash = formatHashShort(entry.packageHash)
  const artHash = formatHashShort(entry.artifactHash)
  const containerRef = useRef<HTMLDivElement>(null)
  const [flash, setFlash] = useState<{ id: string; nonce: number } | null>(null)

  // SchemaDescriptors is keyed by schema id; index by both key and object
  // name so a callable's request/response schema reference resolves.
  const schemasByName = useMemo(() => {
    const m = new Map<string, AppObjectDescriptor>()
    for (const [key, d] of Object.entries(entry.schemaDescriptors ?? {})) {
      if (!d) continue
      m.set(key, d)
      if (d.Name) m.set(d.Name, d)
    }
    return m
  }, [entry.schemaDescriptors])

  // A bundle tool references a callable by ID: scroll the matching callable
  // row into view and flash it so the link between the two is visible.
  const locateCallable = (callableId: string) => {
    const el = containerRef.current?.querySelector(`[data-callable-id="${CSS.escape(callableId)}"]`)
    if (!el) return
    el.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
    setFlash({ id: callableId, nonce: Date.now() })
  }

  useEffect(() => {
    if (!flash) return
    const timer = setTimeout(() => setFlash(null), 1600)
    return () => clearTimeout(timer)
  }, [flash])

  return (
    <div className="plugin-details space-y-4" data-testid="plugin-details" ref={containerRef}>
      <DetailSection title={t('settings.plugins.detail.meta')}>
        <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
          <dt className="text-muted-foreground">{t('settings.plugins.detail.id')}</dt>
          <dd className="truncate font-mono" title={entry.id}>{entry.id}</dd>
          <dt className="text-muted-foreground">{t('settings.plugins.detail.namespace')}</dt>
          <dd className="truncate font-mono" title={entry.namespace}>{entry.namespace ?? '—'}</dd>
          <dt className="text-muted-foreground">{t('settings.plugins.detail.version')}</dt>
          <dd className="truncate font-mono">{entry.version}</dd>
          <dt className="text-muted-foreground">{t('settings.plugins.detail.runtime')}</dt>
          <dd className="truncate font-mono">{entry.runtime}</dd>
          <dt className="text-muted-foreground">{t('settings.plugins.detail.isolation')}</dt>
          <dd className="truncate font-mono">{entry.isolation ?? '—'}</dd>
          <dt className="text-muted-foreground">{t('settings.plugins.detail.trustClass')}</dt>
          <dd className="truncate font-mono">{entry.trustClass ?? '—'}</dd>
          <dt className="text-muted-foreground">{t('settings.plugins.detail.packageHash')}</dt>
          <dd className="truncate font-mono" title={pkgHash.full}>{pkgHash.short || '—'}</dd>
          <dt className="text-muted-foreground">{t('settings.plugins.detail.artifactHash')}</dt>
          <dd className="truncate font-mono" title={artHash.full}>{artHash.short || '—'}</dd>
        </dl>
      </DetailSection>
      <Separator />
      <DetailSection title={t('settings.plugins.detail.callables')}>
        {callables.length > 0 ? (
          <ul className="space-y-1.5">
            {callables.map((c) => (
              // Remount on each flash so the highlight animation replays on repeat clicks.
              <CallableRow key={flash?.id === c.Id ? `${c.Id}:${flash.nonce}` : c.Id} c={c} flash={flash?.id === c.Id} schemas={schemasByName} />
            ))}
          </ul>
        ) : (
          <p className="text-xs text-muted-foreground">{t('settings.plugins.detail.noCallables')}</p>
        )}
      </DetailSection>
      <Separator />
      <DetailSection title={t('settings.plugins.detail.events')}>
        {events.length > 0 ? (
          <ul className="space-y-1.5">
            {events.map((e) => <EventRow key={e.Id} e={e} />)}
          </ul>
        ) : (
          <p className="text-xs text-muted-foreground">{t('settings.plugins.detail.noEvents')}</p>
        )}
      </DetailSection>
      <Separator />
      <DetailSection title={t('settings.plugins.detail.bundles')}>
        {bundles.length > 0 ? (
          <div className="space-y-2">
            {bundles.map((b) => <BundleRow key={b.Title} b={b} onLocate={locateCallable} />)}
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">{t('settings.plugins.detail.noBundles')}</p>
        )}
      </DetailSection>
    </div>
  )
}

export interface PluginTabViewProps {
  pluginID: string
  viewID: string
  route: string
  title?: string
  // Whether this tab is the visible one (and the panel is open). Passed
  // through to PluginIframe so hidden keepAlive tabs suspend their event
  // connections instead of holding streams in the background.
  active?: boolean
}

/**
 * Host chrome for a plugin view tab: a status/refresh toolbar above the
 * PluginIframe. Owns the refresh nonce — bumping it remounts the iframe,
 * forcing the app frontend to re-fetch its document.
 */
/**
 * Forward one FileBrowser drag payload into the plugin panel as file bytes.
 *
 * The panel iframe cannot see host-page drags while the overlay covers it, so
 * the overlay reads the project file and hands the bytes over by postMessage.
 * The panel acks ("sporemind:file-drop-ack") when it accepts the file; without
 * an ack the caller falls back to the read-only right-panel preview, so panels
 * that do not handle file drops keep the old behaviour. Remote (SSH) drags and
 * directories are not forwarded — no project bytes path for them.
 *
 * The drag payload's path is PROJECT-RELATIVE; a plugin process cannot resolve
 * it (its working directory is not the project root), so the message carries
 * the absolute filesystem path joined from the project's RootPath. Panels that
 * only want the bytes ignore it; path-aware panels (ebitor) open by path so
 * Save writes back in place.
 */
async function forwardFileDropToPlugin(
  frameEl: HTMLElement | null,
  payload: { name: string; isDir: boolean; projectId?: string; path?: string },
): Promise<boolean> {
  if (!frameEl || !frameEl.parentElement || payload.isDir || !payload.projectId || !payload.path) return false
  const iframe = frameEl.parentElement.querySelector<HTMLIFrameElement>('iframe')
  if (!iframe?.contentWindow) return false
  const resp = await projectClient
    .readBase64(client, { Path: payload.path }, { target: payload.projectId })
    .catch(() => null)
  if (!resp?.Content) return false
  const root = gitStore.state.projects.find((p) => p.ProjectID === payload.projectId)?.RootPath
  const absPath = root
    ? `${root.replace(/\\/g, '/').replace(/\/$/, '')}/${payload.path}`
    : payload.path
  return new Promise((resolve) => {
    const id = 'fwd-' + Math.random().toString(36).slice(2, 10)
    const timer = setTimeout(() => {
      window.removeEventListener('message', onAck)
      resolve(false)
    }, 1500)
    const onAck = (ev: MessageEvent) => {
      const d = ev.data as { type?: string; id?: string } | null
      if (d?.type === 'sporemind:file-drop-ack' && d.id === id) {
        clearTimeout(timer)
        window.removeEventListener('message', onAck)
        resolve(true)
      }
    }
    window.addEventListener('message', onAck)
    iframe.contentWindow!.postMessage(
      { type: 'sporemind:file-drop', id, name: payload.name, path: absPath, dataBase64: resp.Content },
      '*',
    )
  })
}

export function PluginTabView({ pluginID, viewID, route, title, active = true }: PluginTabViewProps) {
  const [nonce, setNonce] = useState(0)
  const [refreshing, setRefreshing] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const [showLogs, setShowLogs] = useState(false)
  const { t } = useI18n()
  // An in-app FileBrowser drag never delivers dragover/drop to the host page
  // while the cursor sits over the iframe document, so while the session is
  // live we cover the frame with a transparent drop-catching overlay.
  const localFileDrag = useSyncExternalStore(subscribeLocalFileDrag, getLocalFileDragActive)
  const entry = useSyncExternalStore(
    (listener) => appRegistry.subscribe(listener),
    () => appRegistry.get(pluginID),
  )
  // Refreshing only completes when a fresh iframe document loads; that never
  // happens while the app is down (no iframe is mounted), so clear the spin
  // and disable the button in every non-running state.
  const running = entry?.state === 'running' || entry?.state === 'restart_pending'
  useEffect(() => {
    if (!running) setRefreshing(false)
  }, [running])

  const handleRefresh = () => {
    if (!running) return
    setRefreshing(true)
    setNonce((n) => n + 1)
  }

  // Opens the plugin's app.data storage directory in the host file manager.
  // The grant dir comes from pluginhost (host-side truth); the reveal itself
  // is a Wails binding, so the button stays disabled in browser/mobile modes.
  const desktop = isWails()
  const handleOpenStorage = useCallback(async () => {
    if (!desktop) return
    try {
      const resp = await appdataUsage(client, { PluginId: pluginID })
      const dir = resp.Items?.find((item) => item.PluginId === pluginID)?.DataDir
      if (dir) await desktopApp.OpenDirectory(dir)
    } catch { /* best-effort: no grant or no file manager */ }
  }, [desktop, pluginID])

  return (
    <>
      <PluginToolbar
        entry={entry}
        refreshing={refreshing}
        canRefresh={running}
        expanded={expanded}
        showLogs={showLogs}
        canOpenStorage={desktop}
        onToggleDetails={() => setExpanded((v) => !v)}
        onRefresh={handleRefresh}
        onToggleLogs={() => setShowLogs((v) => !v)}
        onOpenStorage={() => { void handleOpenStorage() }}
      />
      {expanded && entry && <PluginDetails entry={entry} />}
      <div className="plugin-tab-frame">
        <PluginIframe
          pluginID={pluginID}
          viewID={viewID}
          route={route}
          title={title}
          refreshNonce={nonce}
          onFrameLoad={() => setRefreshing(false)}
          active={active}
        />
        {localFileDrag && (
          <div
            className="plugin-tab-dnd-overlay"
            data-testid="plugin-tab-dnd-overlay"
            {...{ [OS_DROP_TARGET_ATTR]: '' }}
            onDragOver={(e) => {
              e.preventDefault()
              e.dataTransfer.dropEffect = 'copy'
            }}
            onDrop={(e) => {
              const payload = getLocalFileDragPayload(e.dataTransfer)
              if (!payload) return
              e.preventDefault()
              e.stopPropagation()
              // Panels that handle file drops receive the bytes; everything
              // else keeps the read-only right-panel preview.
              void forwardFileDropToPlugin(e.currentTarget as HTMLElement, payload).then((handled) => {
                if (!handled) {
                  window.dispatchEvent(new CustomEvent('sporemind:file-preview-open', { detail: payload }))
                }
              })
            }}
          >
            <span className="plugin-tab-dnd-hint">{t('fileBrowser.dropToPreview')}</span>
          </div>
        )}
      </div>
      <PluginLogPanel pluginID={pluginID} visible={showLogs} />
    </>
  )
}
