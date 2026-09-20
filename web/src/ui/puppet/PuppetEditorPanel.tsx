import { useCallback, useEffect, useMemo, useState } from 'react'
import { Layers, Box, Settings2, SlidersHorizontal, Play, Info, RefreshCw } from 'lucide-react'
import { client } from '../../application/generated-client'
import * as puppetDocument from '../../gen-clients/puppet.document/client'
import * as puppetEdit from '../../gen-clients/puppet/client'
import type { PuppetDocumentSnapshot } from '../../gen-types/puppet'
import { useI18n } from '../../i18n'
import { PuppetCanvas } from './PuppetCanvas'
import { findNode, InspectorPanel, NodeTreePanel } from './PuppetDocumentPanels'
import { StagedAssetsPanel } from './StagedAssetsPanel'
import { collectNodeTextures, type ResolvedTexture, type TextureResolver } from './snapshotConverter'
import './PuppetEditorPanel.css'

export function PuppetEditorPanel() {
  const { t } = useI18n()
  const [snapshot, setSnapshot] = useState<PuppetDocumentSnapshot | null>(null)
  const [selectedGuid, setSelectedGuid] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [editError, setEditError] = useState<string | null>(null)
  const [textureMap, setTextureMap] = useState<Map<string, ResolvedTexture>>(() => new Map())

  const loadSnapshot = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const response = await puppetDocument.snapshot(client, {})
      setSnapshot(response.Snapshot)
      setSelectedGuid(current => findNode(response.Snapshot.Document.Root, current)?.Guid ?? response.Snapshot.Document.Root.Guid)
      // Load node textures through the generated puppet.asset.read client.
      // Individual asset failures are skipped inside collectNodeTextures;
      // an overall transport failure keeps the previous textures.
      try {
        setTextureMap(await collectNodeTextures(response.Snapshot, client))
      } catch {
        setTextureMap(new Map())
      }
    } catch (err) {
      setSnapshot(null)
      setSelectedGuid(null)
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadSnapshot()
  }, [loadSnapshot])

  const textureResolver = useCallback<TextureResolver>(
    assetId => textureMap.get(assetId),
    [textureMap],
  )

  const selectedNode = useMemo(
    () => snapshot ? findNode(snapshot.Document.Root, selectedGuid) : null,
    [snapshot, selectedGuid],
  )

  const handleEdit = useCallback(async (kind: string, params: Record<string, string>): Promise<boolean> => {
    if (!selectedGuid) return false
    setEditing(true)
    setEditError(null)
    try {
      const response = await puppetEdit.edit(client, {
        Command: {
          Kind: kind,
          TargetGuid: selectedGuid,
          Author: '',
          Params: params,
        },
      })
      if (!response.Applied) {
        setEditError(response.Detail ?? 'Edit rejected')
        return false
      }
      await loadSnapshot()
      return true
    } catch (err) {
      setEditError(err instanceof Error ? err.message : String(err))
      return false
    } finally {
      setEditing(false)
    }
  }, [selectedGuid, loadSnapshot])

  return (
    <div className="puppet-editor">
      <div className="puppet-editor-toolbar">
        <div className="puppet-editor-title">
          <Layers size={14} />
          {t('puppetEditor.title')}
        </div>
        <button
          type="button"
          className="puppet-editor-refresh"
          onClick={() => void loadSnapshot()}
          disabled={loading}
          aria-label={t('puppetEditor.refresh')}
          title={t('puppetEditor.refresh')}
        >
          <RefreshCw size={14} className={loading ? 'spinning' : ''} />
        </button>
      </div>

      {loading && <div className="puppet-editor-notice" aria-live="polite"><Info size={12} />{t('puppetEditor.loading')}</div>}
      {error && <div className="puppet-editor-notice puppet-editor-error" role="alert">{t('puppetEditor.loadError')}: {error}</div>}

      <div className="puppet-editor-grid">
        <section className="puppet-editor-card puppet-editor-canvas-card">
          <h4 className="puppet-editor-card-title"><Box size={13} /> {t('puppetEditor.canvas')}</h4>
          {snapshot ? <PuppetCanvas documentSnapshot={snapshot} textureResolver={textureResolver} /> : <div className="puppet-editor-placeholder-box">{t('puppetEditor.canvasPlaceholder')}</div>}
        </section>

        <section className="puppet-editor-card">
          <h4 className="puppet-editor-card-title"><Layers size={13} /> {t('puppetEditor.nodeTree')}</h4>
          {snapshot ? <NodeTreePanel root={snapshot.Document.Root} selectedGuid={selectedGuid} onSelect={setSelectedGuid} /> : !loading && !error && <div className="puppet-document-empty">{t('puppetEditor.emptyDocument')}</div>}
        </section>

        <section className="puppet-editor-card">
          <h4 className="puppet-editor-card-title"><Settings2 size={13} /> {t('puppetEditor.inspector')}</h4>
          {snapshot ? <InspectorPanel node={selectedNode} onEdit={handleEdit} editing={editing} editError={editError} /> : !loading && !error && <div className="puppet-document-empty">{t('puppetEditor.emptyDocument')}</div>}
        </section>

        <section className="puppet-editor-card">
          <h4 className="puppet-editor-card-title"><SlidersHorizontal size={13} /> {t('puppetEditor.parameters')}</h4>
          <div className="puppet-editor-placeholder-box">{t('puppetEditor.parametersPlaceholder')}</div>
        </section>

        <section className="puppet-editor-card">
          <h4 className="puppet-editor-card-title"><Play size={13} /> {t('puppetEditor.simulation')}</h4>
          <div className="puppet-editor-placeholder-box">{t('puppetEditor.simulationPlaceholder')}</div>
        </section>

        {snapshot && <StagedAssetsPanel root={snapshot.Document.Root} onRefreshDocument={loadSnapshot} />}
      </div>
    </div>
  )
}
