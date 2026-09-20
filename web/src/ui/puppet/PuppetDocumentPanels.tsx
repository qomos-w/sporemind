import { useEffect, useState } from 'react'
import type { PuppetNode } from '../../gen-types/puppet'
import { useI18n } from '../../i18n'
import { summarizeMesh } from './snapshotConverter'

export interface NodeTreePanelProps {
  root: PuppetNode
  selectedGuid: string | null
  onSelect: (guid: string) => void
}

export function NodeTreePanel({ root, selectedGuid, onSelect }: NodeTreePanelProps) {
  const { t } = useI18n()

  return (
    <div className="puppet-node-tree" role="tree" aria-label={t('puppetEditor.nodeTree')}>
      <TreeNode node={root} depth={0} isRoot selectedGuid={selectedGuid} onSelect={onSelect} />
    </div>
  )
}

function TreeNode({
  node,
  depth,
  isRoot = false,
  selectedGuid,
  onSelect,
}: {
  node: PuppetNode
  depth: number
  isRoot?: boolean
  selectedGuid: string | null
  onSelect: (guid: string) => void
}) {
  const { t } = useI18n()
  const label = isRoot ? t('puppetEditor.root') : node.Name

  return (
    <div role="treeitem" aria-selected={node.Guid === selectedGuid} aria-level={depth + 1}>
      <button
        type="button"
        className={`puppet-node-tree-item${node.Guid === selectedGuid ? ' selected' : ''}`}
        style={{ paddingInlineStart: `${8 + depth * 16}px` }}
        onClick={() => onSelect(node.Guid)}
      >
        <span className="puppet-node-tree-name">{label}</span>
        <span className="puppet-node-tree-kind">{node.Kind}</span>
      </button>
      {node.Children.map(child => (
        <TreeNode key={child.Guid} node={child} depth={depth + 1} selectedGuid={selectedGuid} onSelect={onSelect} />
      ))}
    </div>
  )
}

export interface InspectorPanelProps {
  node: PuppetNode | null
  onEdit?: (kind: string, params: Record<string, string>) => Promise<boolean>
  editing?: boolean
  editError?: string | null
}

export function InspectorPanel({ node, onEdit, editing = false, editError = null }: InspectorPanelProps) {
  const { t } = useI18n()
  const [draftName, setDraftName] = useState('')
  const [draftEnabled, setDraftEnabled] = useState(false)
  const [draftZ, setDraftZ] = useState('')

  const committedZ = node?.Z !== undefined ? String(node.Z) : ''

  useEffect(() => {
    if (node) {
      setDraftName(node.Name)
      setDraftEnabled(node.Enabled)
      setDraftZ(node.Z !== undefined ? String(node.Z) : '')
    }
  }, [node?.Guid, node?.Name, node?.Enabled, node?.Z])

  if (!node) {
    return <div className="puppet-document-empty">{t('puppetEditor.noNodeSelected')}</div>
  }

  const showEditForm = !!onEdit
  const nameDirty = draftName.trim() !== '' && draftName !== node.Name
  const enabledDirty = draftEnabled !== node.Enabled
  const zDirty = draftZ !== committedZ

  // Mesh summary (vertex / triangle counts) for nodes carrying an explicit mesh.
  const meshSummary = summarizeMesh(node.Mesh)
  const meshField: Array<[string, string]> = meshSummary
    ? [[t('puppetEditor.mesh'), t('puppetEditor.meshSummary', {
        vertices: meshSummary.vertexCount,
        triangles: meshSummary.triangleCount,
      })]]
    : []

  // Read-only fields always shown. When the edit form is active, Enabled and Z
  // move into editable controls, so they are excluded from the read-only list.
  const readOnlyFields: Array<[string, string | number | boolean | undefined]> = showEditForm
    ? [
        [t('puppetEditor.guid'), node.Guid],
        [t('puppetEditor.kind'), node.Kind],
        [t('puppetEditor.textureAssetId'), node.TextureAssetId],
        ...meshField,
      ]
    : [
        [t('puppetEditor.guid'), node.Guid],
        [t('puppetEditor.kind'), node.Kind],
        [t('puppetEditor.enabled'), node.Enabled ? t('puppetEditor.yes') : t('puppetEditor.no')],
        [t('puppetEditor.z'), node.Z],
        [t('puppetEditor.textureAssetId'), node.TextureAssetId],
        ...meshField,
      ]

  return (
    <div className="puppet-inspector" data-selected-guid={node.Guid}>
      {editError && <div className="puppet-editor-notice puppet-editor-error" role="alert">{editError}</div>}
      {showEditForm && (
        <div className="puppet-inspector-edit">
          <div className="puppet-inspector-edit-field">
            <label>
              <span>{t('puppetEditor.nodeName')}</span>
              <span className="puppet-inspector-edit-control">
                <input value={draftName} onChange={e => setDraftName(e.target.value)} disabled={editing} aria-label={t('puppetEditor.nodeName')} data-edit-field="name" />
                <button type="button" onClick={() => void onEdit?.('set_node_name', { name: draftName })} disabled={editing || !nameDirty} aria-label={t('puppetEditor.saveName')} data-edit-action="set_node_name">{t('puppetEditor.save')}</button>
              </span>
            </label>
          </div>
          <div className="puppet-inspector-edit-field">
            <label>
              <span>{t('puppetEditor.enabled')}</span>
              <span className="puppet-inspector-edit-control">
                <input type="checkbox" checked={draftEnabled} onChange={e => setDraftEnabled(e.target.checked)} disabled={editing} aria-label={t('puppetEditor.enabled')} data-edit-field="enabled" />
                <button type="button" onClick={() => void onEdit?.('set_node_enabled', { enabled: String(draftEnabled) })} disabled={editing || !enabledDirty} aria-label={t('puppetEditor.saveEnabled')} data-edit-action="set_node_enabled">{t('puppetEditor.save')}</button>
              </span>
            </label>
          </div>
          <div className="puppet-inspector-edit-field">
            <label>
              <span>{t('puppetEditor.z')}</span>
              <span className="puppet-inspector-edit-control">
                <input type="number" value={draftZ} onChange={e => setDraftZ(e.target.value)} disabled={editing} aria-label={t('puppetEditor.z')} data-edit-field="z" />
                <button type="button" onClick={() => void onEdit?.('set_node_z', { z: draftZ })} disabled={editing || !zDirty} aria-label={t('puppetEditor.saveZ')} data-edit-action="set_node_z">{t('puppetEditor.save')}</button>
              </span>
            </label>
          </div>
        </div>
      )}
      <dl className="puppet-inspector-fields">
        {readOnlyFields.map(([label, value]) => (
          <div key={label} className="puppet-inspector-field">
            <dt>{label}</dt>
            <dd>{value === undefined || value === '' ? t('puppetEditor.notSet') : value}</dd>
          </div>
        ))}
      </dl>
      <div className="puppet-inspector-params">
        <h5>{t('puppetEditor.params')}</h5>
        {Object.entries(node.Params ?? {}).length === 0 ? (
          <div className="puppet-document-empty">{t('puppetEditor.noParams')}</div>
        ) : (
          <dl className="puppet-inspector-fields">
            {Object.entries(node.Params ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => (
              <div key={key} className="puppet-inspector-field">
                <dt>{key}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        )}
      </div>
    </div>
  )
}

export function findNode(root: PuppetNode, guid: string | null): PuppetNode | null {
  if (!guid) return null
  if (root.Guid === guid) return root
  for (const child of root.Children) {
    const match = findNode(child, guid)
    if (match) return match
  }
  return null
}
