import { useState, memo } from 'react'
import type { GlassRenderFrame, GlassSceneElement, GlassSceneBox, GlassSceneOption } from '../../gen-clients/system/types'

/**
 * SceneTree — collapsible tree view of the current GlassRenderFrame.
 * Nodes can be expanded/collapsed by clicking the arrow or the key label.
 */

type Node = {
  key: string
  label: string
  value?: string
  children?: Node[]
  highlight?: boolean
}

function boxNode(box: GlassSceneBox): Node {
  return {
    key: 'Box',
    label: 'Box',
    children: [
      { key: 'X', label: 'X', value: String(box.X) },
      { key: 'Y', label: 'Y', value: String(box.Y) },
      { key: 'W', label: 'W', value: String(box.W) },
      { key: 'H', label: 'H', value: String(box.H) },
    ],
  }
}

function optionNode(opt: GlassSceneOption, i: number): Node {
  return {
    key: `opt-${i}`,
    label: `Option [${i}]`,
    children: [
      { key: 'Id', label: 'Id', value: opt.Id || '(empty)' },
      { key: 'Text', label: 'Text', value: opt.Text || '(empty)' },
    ],
  }
}

function elementNode(el: GlassSceneElement, i: number): Node {
  const children: Node[] = [
    { key: 'Id', label: 'Id', value: el.Id || '(empty)' },
    { key: 'Type', label: 'Type', value: el.Type, highlight: true },
    boxNode(el.Box),
  ]
  if (el.Text !== undefined && el.Text !== '') children.push({ key: 'Text', label: 'Text', value: el.Text })
  if (el.Label !== undefined && el.Label !== '') children.push({ key: 'Label', label: 'Label', value: el.Label })
  if (el.Border) children.push({ key: 'Border', label: 'Border', value: String(el.Border) })
  if (el.Radius) children.push({ key: 'Radius', label: 'Radius', value: String(el.Radius) })
  if (el.Z) children.push({ key: 'Z', label: 'Z', value: String(el.Z) })
  if (el.Visible !== undefined) children.push({ key: 'Visible', label: 'Visible', value: String(el.Visible) })
  if (el.Selected) children.push({ key: 'Selected', label: 'Selected', value: 'true' })
  if (el.Role) children.push({ key: 'Role', label: 'Role', value: el.Role })
  if (el.Overflow) children.push({ key: 'Overflow', label: 'Overflow', value: el.Overflow })
  if (el.BreakMode) children.push({ key: 'BreakMode', label: 'BreakMode', value: el.BreakMode })
  if (el.ImageData) children.push({ key: 'ImageData', label: 'ImageData', value: `[${el.ImageData.length} chars]` })
  if (el.Options && el.Options.length > 0) {
    children.push({
      key: 'Options',
      label: `Options (${el.Options.length})`,
      children: el.Options.map((o, j) => optionNode(o, j)),
    })
  }
  return {
    key: `el-${i}`,
    label: `Element [${i}]`,
    value: `${el.Type} "${el.Id}"`,
    children,
  }
}

function frameToTree(frame: GlassRenderFrame): Node {
  const children: Node[] = []
  if (frame.Scene) {
    children.push({
      key: 'Tick',
      label: 'Tick',
      value: String(frame.Scene.Tick),
    })
    children.push({
      key: 'Elements',
      label: `Elements (${frame.Scene.Elements?.length ?? 0})`,
      children: (frame.Scene.Elements ?? []).map((el, i) => elementNode(el, i)),
    })
    if (frame.Scene.FocusId) {
      children.push({ key: 'FocusId', label: 'FocusId', value: frame.Scene.FocusId })
    }
  } else if (frame.Text) {
    children.push({ key: 'Text', label: 'Text', value: frame.Text })
  } else if (frame.ImageUrl) {
    children.push({ key: 'ImageUrl', label: 'ImageUrl', value: frame.ImageUrl })
  } else if (frame.Layout) {
    children.push({ key: 'Layout', label: 'Layout', value: frame.Layout })
  }
  return { key: 'root', label: 'Scene', children }
}

function TreeNodeView({ node, depth }: { node: Node; depth: number }) {
  const [open, setOpen] = useState(depth < 2)
  const hasChildren = node.children && node.children.length > 0

  return (
    <div className="scene-tree-node">
      <div
        className="scene-tree-row"
        style={{ paddingLeft: `${depth * 14 + 4}px` }}
        onClick={() => hasChildren && setOpen(o => !o)}
      >
        {hasChildren ? (
          <span className={`scene-tree-arrow ${open ? 'open' : ''}`}>{open ? '▾' : '▸'}</span>
        ) : (
          <span className="scene-tree-arrow-spacer" />
        )}
        <span className={`scene-tree-key ${node.highlight ? 'highlight' : ''}`}>{node.label}</span>
        {node.value !== undefined ? (
          <span className="scene-tree-value">{node.value}</span>
        ) : null}
      </div>
      {hasChildren && open ? (
        <div className="scene-tree-children">
          {node.children!.map(c => (
            <TreeNodeView key={c.key} node={c} depth={depth + 1} />
          ))}
        </div>
      ) : null}
    </div>
  )
}

export const SceneTree = memo(function SceneTree({ frame }: { frame: GlassRenderFrame | undefined }) {
  if (!frame) {
    return (
      <details className="glass-debug-json">
        <summary>Scene tree</summary>
        <div className="scene-tree-empty">no frame</div>
      </details>
    )
  }
  const tree = frameToTree(frame)
  return (
    <details className="glass-debug-json">
      <summary>Scene tree</summary>
      <div className="scene-tree">
        <TreeNodeView node={tree} depth={0} />
      </div>
    </details>
  )
})
