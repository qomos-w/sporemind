import { useCallback, useEffect, useMemo, useState } from 'react'
import { Check, ImageOff, Send, X } from 'lucide-react'
import { client } from '../../application/generated-client'
import * as puppetAsset from '../../gen-clients/puppet.asset/client'
import * as puppetRevision from '../../gen-clients/puppet.revision/client'
import type { PuppetNode, PuppetRevision, PuppetStagedAsset } from '../../gen-types/puppet'
import { useI18n } from '../../i18n'

export interface StagedAssetsPanelProps {
  root: PuppetNode
  onRefreshDocument: () => Promise<void>
}

interface NodeOption {
  Guid: string
  Name: string
}

function collectNodes(node: PuppetNode, nodes: NodeOption[] = []): NodeOption[] {
  nodes.push({ Guid: node.Guid, Name: node.Name })
  node.Children.forEach(child => collectNodes(child, nodes))
  return nodes
}

function assetLabel(asset: PuppetStagedAsset): string {
  return asset.Name || asset.DataRef || asset.Id
}

function formatDate(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function StagedAssetsPanel({ root, onRefreshDocument }: StagedAssetsPanelProps) {
  const { t } = useI18n()
  const [assets, setAssets] = useState<PuppetStagedAsset[]>([])
  const [revisions, setRevisions] = useState<PuppetRevision[]>([])
  const [targetGuid, setTargetGuid] = useState('')
  const [reason, setReason] = useState('')
  const [loading, setLoading] = useState(true)
  const [actionAssetId, setActionAssetId] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [thumbnailFailures, setThumbnailFailures] = useState<Set<string>>(() => new Set())
  const nodeOptions = useMemo(() => collectNodes(root), [root])

  const loadAssets = useCallback(async () => {
    const response = await puppetAsset.list(client, {})
    setAssets(response.Assets)
  }, [])

  const loadRevisions = useCallback(async () => {
    const response = await puppetRevision.log(client, { Limit: 10 })
    setRevisions(response.Revisions)
  }, [])

  const refresh = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      await Promise.all([loadAssets(), loadRevisions()])
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }, [loadAssets, loadRevisions])

  useEffect(() => {
    void refresh()
  }, [refresh])

  useEffect(() => {
    if (targetGuid && !nodeOptions.some(node => node.Guid === targetGuid)) setTargetGuid('')
  }, [nodeOptions, targetGuid])

  const refreshAfterReview = useCallback(async () => {
    await Promise.all([onRefreshDocument(), loadAssets(), loadRevisions()])
  }, [loadAssets, loadRevisions, onRefreshDocument])

  const commitAsset = useCallback(async (asset: PuppetStagedAsset) => {
    if (!targetGuid) {
      setError(t('puppetEditor.assetTargetRequired'))
      return
    }
    setActionAssetId(asset.Id)
    setError(null)
    try {
      await puppetAsset.commit(client, { AssetId: asset.Id, NodeGuid: targetGuid })
      await refreshAfterReview()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setActionAssetId(null)
    }
  }, [refreshAfterReview, t, targetGuid])

  const rejectAsset = useCallback(async (asset: PuppetStagedAsset) => {
    if (!reason.trim()) {
      setError(t('puppetEditor.assetReasonRequired'))
      return
    }
    setActionAssetId(asset.Id)
    setError(null)
    try {
      await puppetAsset.reject(client, { AssetId: asset.Id, Reason: reason.trim() })
      setReason('')
      await refreshAfterReview()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setActionAssetId(null)
    }
  }, [reason, refreshAfterReview, t])

  return (
    <section className="puppet-editor-card puppet-staged-assets" aria-label={t('puppetEditor.stagedAssets')}>
      <h4 className="puppet-editor-card-title"><Send size={13} /> {t('puppetEditor.stagedAssets')}</h4>
      {error && <div className="puppet-editor-notice puppet-editor-error" role="alert">{error}</div>}
      {loading ? <div className="puppet-document-empty">{t('puppetEditor.assetsLoading')}</div> : (
        <div className="puppet-asset-list">
          {assets.length === 0 && <div className="puppet-document-empty">{t('puppetEditor.noAssets')}</div>}
          {assets.map(asset => {
            const isStaged = asset.State === 'staged'
            const busy = actionAssetId === asset.Id
            const showThumbnail = !thumbnailFailures.has(asset.Id)
            return (
              <article className="puppet-asset" key={asset.Id} data-asset-state={asset.State}>
                <div className="puppet-asset-thumbnail">
                  {showThumbnail ? <img src={asset.DataRef} alt="" onError={() => setThumbnailFailures(current => new Set(current).add(asset.Id))} /> : <ImageOff size={16} aria-label={t('puppetEditor.assetPreviewUnavailable')} />}
                </div>
                <div className="puppet-asset-details">
                  <strong>{assetLabel(asset)}</strong>
                  <span>{t('puppetEditor.assetSource')}: {asset.SourceRef || t('puppetEditor.notSet')}</span>
                  <span>{t('puppetEditor.assetCreatedAt')}: {formatDate(asset.CreatedAt)}</span>
                  {asset.NodeGuid && <span>{t('puppetEditor.assetTarget')}: {asset.NodeGuid}</span>}
                  {asset.ReviewReason && <span>{t('puppetEditor.assetReason')}: {asset.ReviewReason}</span>}
                </div>
                <span className={`puppet-asset-state ${asset.State}`}>{asset.State}</span>
                {isStaged && <div className="puppet-asset-actions">
                  <button type="button" onClick={() => void commitAsset(asset)} disabled={busy || !targetGuid} title={t('puppetEditor.assetCommit')} aria-label={`${t('puppetEditor.assetCommit')} ${assetLabel(asset)}`}><Check size={13} /></button>
                  <button type="button" onClick={() => void rejectAsset(asset)} disabled={busy || !reason.trim()} title={t('puppetEditor.assetReject')} aria-label={`${t('puppetEditor.assetReject')} ${assetLabel(asset)}`}><X size={13} /></button>
                </div>}
              </article>
            )
          })}
        </div>
      )}
      <div className="puppet-asset-review-controls">
        <label>
          <span>{t('puppetEditor.assetTarget')}</span>
          <select value={targetGuid} onChange={event => setTargetGuid(event.target.value)} aria-label={t('puppetEditor.assetTarget')}>
            <option value="">{t('puppetEditor.assetSelectTarget')}</option>
            {nodeOptions.map(node => <option value={node.Guid} key={node.Guid}>{node.Name} ({node.Guid})</option>)}
          </select>
        </label>
        <label>
          <span>{t('puppetEditor.assetReason')}</span>
          <input value={reason} onChange={event => setReason(event.target.value)} placeholder={t('puppetEditor.assetReasonPlaceholder')} aria-label={t('puppetEditor.assetReason')} />
        </label>
      </div>
      <div className="puppet-revision-log" aria-label={t('puppetEditor.revisionLog')}>
        <h5>{t('puppetEditor.revisionLog')}</h5>
        {revisions.length === 0 ? <span>{t('puppetEditor.noRevisions')}</span> : revisions.map(revision => <span key={revision.Id} data-revision-id={revision.Id}>#{revision.Sequence} {revision.CommandKind}</span>)}
      </div>
    </section>
  )
}
