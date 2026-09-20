import type { ReactElement } from 'react'
import type { AppEntry } from '../../application/app-registry'
import { useI18n } from '../../i18n'
import { PluginIframe } from '../components/PluginIframe'
import type { WorkbenchCardDescriptor } from './cardTypes'

/** Registry id for an appmanager app's workbench card. */
export function appCardId(appId: string): string {
  return `app:${appId}`
}

/**
 * Expanded body for apps without a mountable entrypoint (no view, no panel):
 * a status panel instead of an iframe. The card degrades, it never disappears.
 */
export function AppCardFallbackBody({ state, error }: { state: string; error?: string }) {
  const { t } = useI18n()
  return (
    <div className="wb-app-fallback" data-state={state}>
      <span className="wb-app-fallback-status">{state}</span>
      <p className="wb-app-fallback-hint">{t('workbench.surface.appNoView')}</p>
      {error && (
        <p className="wb-app-fallback-error" title={error}>
          {error}
        </p>
      )}
    </div>
  )
}

/**
 * Workbench card descriptor for a registered app/plugin (spec §2).
 *
 * The compact body is host-side metadata only (icon + color + title +
 * statusText) — an iframe cannot be thumbnailed, so the compact form never
 * mounts it. Expanding the card (fitting the main slot / capture) mounts the
 * app's primary `view` entrypoint full-size through {@link PluginIframe}, which
 * speaks the host gateway data path.
 *
 * Fallback chain: an app without a `view` entrypoint mounts its first `panel`
 * entrypoint (panels are routes on the same gateway origin), and an app with
 * no mountable entrypoint at all keeps a metadata-only card whose expanded
 * body is {@link AppCardFallbackBody} — every registered app stays on the
 * board.
 */
export function createAppCardDescriptor(entry: AppEntry): WorkbenchCardDescriptor {
  // Only a running app serves its gateway routes: a stopped/failed instance
  // with a view entrypoint still gets a card, but the expanded body is the
  // status panel — mounting the iframe would just render a dead route.
  const mountable =
    entry.state === 'running'
      ? entry.entrypoints.find(e => e.kind === 'view') ?? entry.entrypoints.find(e => e.kind === 'panel')
      : undefined

  const title = entry.name?.trim() || entry.id
  const statusParts = [entry.state]
  if (entry.version) statusParts.push(`v${entry.version}`)

  return {
    id: appCardId(entry.id),
    kind: 'app',
    title,
    icon: entry.icon ?? '',
    ...(entry.color ? { color: entry.color } : {}),
    // Fallback attention only: when the workbench projection is live the actor's
    // score overlays this (see projectionBoard.withProjection).
    score: 10,
    pinned: false,
    compactMeta: { statusText: statusParts.filter(Boolean).join(' · ') },
    render: (expanded: boolean): ReactElement | null =>
      expanded
        ? mountable
          ? <PluginIframe pluginID={entry.id} viewID={mountable.id} route={mountable.route ?? '/'} title={title} />
          : <AppCardFallbackBody state={entry.state} error={entry.error} />
        : null,
  }
}

/** Build descriptors for every registered app. */
export function createAppCardDescriptors(entries: readonly AppEntry[]): WorkbenchCardDescriptor[] {
  return entries.map(createAppCardDescriptor)
}
