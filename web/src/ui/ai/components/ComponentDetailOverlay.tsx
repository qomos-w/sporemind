import React, { useEffect, useState } from 'react'
import { X, Wrench, Link2, BookOpen } from 'lucide-react'
import type { ComponentDescriptor } from '../../../gen-types/component'
import { client } from '../../../application/generated-client'
import * as projectComponentClient from '../../../gen-clients/project/client'
import * as workspaceComponentClient from '../../../gen-clients/workspace/client'
import * as appmanagerComponentClient from '../../../gen-clients/appmanager/client'
import { CardIcon } from './CardIcon'
import { localizedCardTitle } from './omniboxLabels'
import { useBrowserOverlay } from '../browserOverlay'
import { useI18n } from '../../../i18n'
import { HoverTip } from '../../components/HoverTip'
import './ComponentDetailOverlay.css'

/** Resolve a component descriptor for the overlay: project catalog first
 *  (project cards shadow workspace cards), then the workspace system catalog
 *  (builtin cards), then virtual appmanager app bundles. First hit wins. */
async function fetchDescriptor(cardId: string, projectId: string | null): Promise<ComponentDescriptor> {
  const attempts: Array<() => Promise<{ Component: ComponentDescriptor }>> = []
  if (projectId) {
    attempts.push(() => projectComponentClient.componentGet(client, { CardId: cardId }, { target: projectId }))
  }
  attempts.push(() => workspaceComponentClient.componentGet(client, { CardId: cardId }))
  attempts.push(() => appmanagerComponentClient.componentGet(client, { CardId: cardId }))
  let lastError: unknown = null
  for (const attempt of attempts) {
    try {
      const resp = await attempt()
      if (resp?.Component) return resp.Component
    } catch (err) {
      lastError = err
    }
  }
  throw lastError ?? new Error(`component not found: ${cardId}`)
}

/** Friendly display name derived from a callable id ("project.wiki_frontier"
 *  → "Wiki Frontier"), matching the badge tooltip naming. */
function prettyCallable(id: string): string {
  return id.replace(/^.*[:.]/, '').replace(/[-_]/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
}

/**
 * Capability details overlay for a composer mode/bundle badge (opened from the
 * badge right-click menu). Shows everything the card contributes: its tool
 * callables, its dependencies and its guidance prompts, fetched via
 * component_get from the project → workspace → appmanager catalogs.
 *
 * Registers with `useBrowserOverlay(open)` so the native right-panel browser
 * window is hidden while the overlay is visible.
 */
export const ComponentDetailOverlay: React.FC<{
  open: boolean
  cardId: string | null
  projectId?: string | null
  titleHint?: string
  iconHint?: string
  colorHint?: string
  onClose: () => void
}> = ({ open, cardId, projectId, titleHint, iconHint, colorHint, onClose }) => {
  const { t } = useI18n()
  const [descriptor, setDescriptor] = useState<ComponentDescriptor | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  useBrowserOverlay(open)

  useEffect(() => {
    if (!open || !cardId) return
    let active = true
    setLoading(true)
    setError(null)
    setDescriptor(null)
    fetchDescriptor(cardId, projectId ?? null)
      .then(d => { if (active) setDescriptor(d) })
      .catch(err => { if (active) setError(err instanceof Error ? err.message : String(err)) })
      .finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [open, cardId, projectId])

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  const title = cardId
    ? localizedCardTitle(cardId, descriptor?.Title || titleHint || cardId, t)
    : ''
  const icon = descriptor?.Icon || iconHint || 'package'
  const color = descriptor?.Visual?.Color || colorHint
  const tools = descriptor?.Tools ?? []
  const dependencies = descriptor?.Dependencies ?? []
  const prompts = descriptor?.Prompts ?? []

  return (
    <div className="component-detail-overlay-root" role="dialog" aria-modal="true" data-testid="component-detail-overlay">
      <div className="component-detail-overlay-backdrop" onClick={onClose} />

      <div className="component-detail-overlay-panel">
        <div className="component-detail-overlay-header">
          <span className="component-detail-overlay-head-icon"><CardIcon name={icon} size={18} color={color} /></span>
          <span className="component-detail-overlay-title">{title}</span>
          {descriptor?.Ref?.Kind && <span className="component-detail-overlay-kind">{descriptor.Ref.Kind}</span>}
          <code className="component-detail-overlay-card-id">{cardId}</code>
          <button
            type="button"
            className="component-detail-overlay-close"
            onClick={onClose}
            aria-label={t('common.close')}
          >
            <X size={18} />
          </button>
        </div>

        <div className="component-detail-overlay-body">
          {loading && <div className="component-detail-overlay-status">{t('composer.badgeDetail.loading')}</div>}
          {!loading && error && (
            <div className="component-detail-overlay-status component-detail-overlay-status--error">
              {t('composer.badgeDetail.loadError')}: {error}
            </div>
          )}
          {!loading && !error && (
            <>
              <section className="component-detail-overlay-section" data-testid="component-detail-capabilities">
                <h3 className="component-detail-overlay-section-title">
                  <Wrench size={13} />
                  {t('composer.badgeDetail.capabilities')}
                  <span className="component-detail-overlay-count">{tools.length}</span>
                </h3>
                {tools.length === 0 ? (
                  <div className="component-detail-overlay-empty">{t('composer.badgeDetail.noTools')}</div>
                ) : (
                  <ul className="component-detail-overlay-tools">
                    {tools.map(tool => (
                      <li key={tool.Id || tool.CallableId} className="component-detail-overlay-tool">
                        <div className="component-detail-overlay-tool-name">
                          {tool.Name || prettyCallable(tool.CallableId)}
                        </div>
                        {/* Description lives in the hover tip on the callable
                            id (same treatment as plugin-details rows), not an
                            inline line that stretches every tool row. */}
                        <HoverTip
                          testId={`component-tool-tip-${tool.CallableId}`}
                          disabled={!tool.Description}
                          className={`component-detail-overlay-tool-id${tool.Description ? ' has-desc' : ''}`}
                          tip={<div className="plugin-schema-tip-desc">{tool.Description}</div>}
                        >
                          <span>{tool.CallableId}</span>
                        </HoverTip>
                      </li>
                    ))}
                  </ul>
                )}
              </section>

              {dependencies.length > 0 && (
                <section className="component-detail-overlay-section" data-testid="component-detail-dependencies">
                  <h3 className="component-detail-overlay-section-title">
                    <Link2 size={13} />
                    {t('composer.badgeDetail.dependencies')}
                    <span className="component-detail-overlay-count">{dependencies.length}</span>
                  </h3>
                  <ul className="component-detail-overlay-deps">
                    {dependencies.map(dep => (
                      <li key={dep.CardId} className="component-detail-overlay-dep">
                        <code className="component-detail-overlay-tool-id">{dep.CardId}</code>
                        {dep.Required && <span className="component-detail-overlay-chip">{t('composer.badgeDetail.required')}</span>}
                        {dep.Reason && <span className="component-detail-overlay-tool-desc">{dep.Reason}</span>}
                      </li>
                    ))}
                  </ul>
                </section>
              )}

              {prompts.length > 0 && (
                <section className="component-detail-overlay-section" data-testid="component-detail-guidance">
                  <h3 className="component-detail-overlay-section-title">
                    <BookOpen size={13} />
                    {t('composer.badgeDetail.guidance')}
                  </h3>
                  {prompts.map(prompt => (
                    <div key={prompt.Id} className="component-detail-overlay-prompt">
                      {prompt.Title && <div className="component-detail-overlay-tool-name">{prompt.Title}</div>}
                      <pre className="component-detail-overlay-prompt-text">{prompt.Text}</pre>
                    </div>
                  ))}
                </section>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
