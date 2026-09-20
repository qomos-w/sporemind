import React, { useState, useCallback } from 'react'
import { ChevronRight, ChevronDown, Loader2 } from 'lucide-react'
import { getFileIcon, getFolderIcon } from './file-icons'
import { normalizePath } from '../ai/components/parts/tool-display.ts'
import './FileTree.css'

export interface FileTreeNode {
  name: string
  path: string
  kind: 'file' | 'directory'
  children?: FileTreeNode[]
  loaded?: boolean
  isMountRoot?: boolean
  gitStatus?: 'M' | 'U' | 'A' | 'D' | 'R'
  actions?: React.ReactNode
}

interface FileTreeProps {
  tree: FileTreeNode[]
  filter?: string
  selectedPath?: string | null
  onFileSelect?: (path: string) => void
  onExpandDir?: (path: string) => Promise<FileTreeNode[]>
  onContextMenu?: (e: React.MouseEvent, node: FileTreeNode) => void
  loadingPaths?: Set<string>
  expandedPaths?: Set<string>
  onExpandedPathsChange?: (paths: Set<string>) => void
}

interface FileTreeRowProps {
  node: FileTreeNode
  depth: number
  expanded: boolean
  selected: boolean
  loading: boolean
  onToggle: () => void
  onSelect: () => void
  onContextMenu?: (e: React.MouseEvent) => void
}

const gitBadgeClass: Record<string, string> = {
  M: 'ft-badge ft-badge-modified',
  U: 'ft-badge ft-badge-untracked',
  A: 'ft-badge ft-badge-added',
  D: 'ft-badge ft-badge-deleted',
  R: 'ft-badge ft-badge-renamed',
}

function FileTreeRow({ node, depth, expanded, selected, loading, onToggle, onSelect, onContextMenu }: FileTreeRowProps) {
  const isDir = node.kind === 'directory'
  const handleClick = useCallback(() => {
    if (isDir) {
      onToggle()
    } else {
      onSelect()
    }
  }, [isDir, onToggle, onSelect])

  return (
    <div
      className={`ft-row${selected ? ' ft-row-selected' : ''}${node.isMountRoot ? ' ft-row-mount' : ''}`}
      style={{ paddingLeft: depth * 16 + 4 }}
      onClick={handleClick}
      onContextMenu={onContextMenu}
      title={normalizePath(node.path) ?? node.path}
    >
      <span className={`ft-chevron${isDir ? '' : ' ft-chevron-hidden'}`}>
        {isDir && (loading ? (
          <Loader2 size={14} className="ft-spin" />
        ) : expanded ? (
          <ChevronDown size={14} />
        ) : (
          <ChevronRight size={14} />
        ))}
      </span>
      <span className="ft-icon">
        {isDir ? getFolderIcon(expanded) : getFileIcon(node.name)}
      </span>
      <span className="ft-name">{node.name}</span>
      {node.gitStatus && (
        <span className={gitBadgeClass[node.gitStatus]}>{node.gitStatus}</span>
      )}
      {node.actions && (
        <span className="ft-actions" onClick={e => e.stopPropagation()}>
          {node.actions}
        </span>
      )}
    </div>
  )
}

function matchesFilter(node: FileTreeNode, filter: string): boolean {
  if (node.name.toLowerCase().includes(filter)) return true
  if (node.kind === 'directory' && node.children) {
    return node.children.some(child => matchesFilter(child, filter))
  }
  return false
}

export function useFileTree({ tree, filter, selectedPath, onFileSelect, onExpandDir, onContextMenu, loadingPaths, expandedPaths: controlledExpandedPaths, onExpandedPathsChange }: FileTreeProps) {
  const [internalExpandedPaths, setInternalExpandedPaths] = useState<Set<string>>(new Set())

  const expandedPaths = controlledExpandedPaths ?? internalExpandedPaths
  const setExpandedPaths = useCallback((next: Set<string>) => {
    if (onExpandedPathsChange) {
      onExpandedPathsChange(next)
    } else {
      setInternalExpandedPaths(next)
    }
  }, [onExpandedPathsChange])

  const toggle = useCallback((path: string, node: FileTreeNode) => {
    const isExpanded = expandedPaths.has(path)
    if (isExpanded) {
      const next = new Set(expandedPaths)
      next.delete(path)
      setExpandedPaths(next)
    } else {
      // Expanding: if directory hasn't loaded children yet, fetch them
      if (node.kind === 'directory' && !node.loaded && onExpandDir) {
        const next = new Set(expandedPaths)
        next.add(path)
        setExpandedPaths(next)
        onExpandDir(path).catch(() => {})
      } else {
        const next = new Set(expandedPaths)
        next.add(path)
        setExpandedPaths(next)
      }
    }
  }, [expandedPaths, onExpandDir, setExpandedPaths])

  const collapseAll = useCallback(() => {
    setExpandedPaths(new Set())
  }, [setExpandedPaths])

  const expandAll = useCallback(() => {
    const all = new Set<string>()
    function walk(nodes: FileTreeNode[]) {
      for (const n of nodes) {
        if (n.kind === 'directory') {
          all.add(n.path)
          if (n.children) walk(n.children)
        }
      }
    }
    walk(tree)
    setExpandedPaths(all)
  }, [tree, setExpandedPaths])

  const normalizedFilter = (filter ?? '').toLowerCase()

  const renderNodes = useCallback(
    (nodes: FileTreeNode[], depth: number): React.ReactNode[] => {
      const result: React.ReactNode[] = []
      const sortedNodes = [...nodes].sort((a, b) => {
        if (a.kind === b.kind) return a.name.localeCompare(b.name)
        return a.kind === 'directory' ? -1 : 1
      })
      for (const node of sortedNodes) {
        if (normalizedFilter && !matchesFilter(node, normalizedFilter)) continue

        const isExpanded = expandedPaths.has(node.path)
        const isSelected = selectedPath === node.path
        const isLoading = loadingPaths?.has(node.path) ?? false

        result.push(
          <FileTreeRow
            key={node.path}
            node={node}
            depth={depth}
            expanded={isExpanded}
            selected={isSelected}
            loading={isLoading}
            onToggle={() => toggle(node.path, node)}
            onSelect={() => onFileSelect?.(node.path)}
            onContextMenu={onContextMenu ? (e) => onContextMenu(e, node) : undefined}
          />
        )

        if (isDir(node) && isExpanded && node.children) {
          result.push(...renderNodes(node.children, depth + 1))
        }
      }
      return result
    },
    [expandedPaths, selectedPath, toggle, onFileSelect, onContextMenu, normalizedFilter, loadingPaths]
  )

  return { renderNodes, collapseAll, expandAll }
}

function isDir(node: FileTreeNode): boolean {
  return node.kind === 'directory'
}
