import React, { useRef } from 'react'
import { Copy, ExternalLink, FolderOpen, Pencil, RefreshCw, Rocket, SlidersHorizontal, UserPlus, XCircle } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { projectDisplayName } from '../../../application/project-adapter'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import './ProjectContextMenu.css'

export interface ProjectMenuProject {
  ProjectID: string
  Name: string
  RootPath: string
  System?: boolean
  AppKind?: string
}

export interface ProjectMenuTarget {
  project: ProjectMenuProject
  x: number
  y: number
}

interface ProjectContextMenuProps {
  target: ProjectMenuTarget | null
  onClose: () => void
  onOpen?: (project: ProjectMenuProject) => void
  onRename?: (project: ProjectMenuProject) => void
  onCopyPath?: (project: ProjectMenuProject) => void
  onOpenInSystem?: (project: ProjectMenuProject) => void
  onCloseProject?: (project: ProjectMenuProject) => void
  onCreateAgent?: (project: ProjectMenuProject) => void
  onRegisterApp?: (project: ProjectMenuProject) => void
  onReloadApp?: (project: ProjectMenuProject) => void
  onProperties?: (project: ProjectMenuProject) => void
}

interface MenuItem {
  key: string
  hotkey: string
  label: string
  icon: React.ReactNode
  danger?: boolean
  run: () => void
}

export const ProjectContextMenu: React.FC<ProjectContextMenuProps> = ({
  target,
  onClose,
  onOpen,
  onRename,
  onCopyPath,
  onOpenInSystem,
  onCloseProject,
  onCreateAgent,
  onRegisterApp,
  onReloadApp,
  onProperties,
}) => {
  const menuRef = useRef<HTMLDivElement>(null)
  const { t } = useI18n()

  useMenuDismiss(menuRef, onClose, target)

  if (!target) return null

  const { project, x, y } = target
  const viewportW = typeof window !== 'undefined' ? window.innerWidth : 1024
  const viewportH = typeof window !== 'undefined' ? window.innerHeight : 768
  const estimateW = 240
  const left = Math.min(x, viewportW - estimateW - 8)

  const isAppProject = !!project.AppKind

  const items: MenuItem[] = [
    onOpen && { key: 'open', hotkey: 'o', label: t('projectContextMenu.open'), icon: <FolderOpen size={13} />, run: () => onOpen(project) },
    onCreateAgent && { key: 'agent', hotkey: 'a', label: t('projectContextMenu.addAgent'), icon: <UserPlus size={13} />, run: () => onCreateAgent(project) },
    onProperties && { key: 'properties', hotkey: 'p', label: t('projectContextMenu.properties'), icon: <SlidersHorizontal size={13} />, run: () => onProperties(project) },
    onRename && !project.System && { key: 'rename', hotkey: 'r', label: t('projectContextMenu.rename'), icon: <Pencil size={13} />, run: () => onRename(project) },
    onCopyPath && { key: 'copy', hotkey: 'c', label: t('projectContextMenu.copyPath'), icon: <Copy size={13} />, run: () => onCopyPath(project) },
    onOpenInSystem && { key: 'openInSystem', hotkey: 'e', label: t('projectContextMenu.openInSystem'), icon: <ExternalLink size={13} />, run: () => onOpenInSystem(project) },
    isAppProject && onRegisterApp && { key: 'register', hotkey: 'g', label: t('projectContextMenu.registerApp'), icon: <Rocket size={13} />, run: () => onRegisterApp(project) },
    isAppProject && onReloadApp && { key: 'reload', hotkey: 'l', label: t('projectContextMenu.reloadApp'), icon: <RefreshCw size={13} />, run: () => onReloadApp(project) },
    onCloseProject && !project.System && { key: 'close', hotkey: 'x', label: t('projectContextMenu.close'), icon: <XCircle size={13} />, danger: true, run: () => onCloseProject(project) },
  ].filter(Boolean) as MenuItem[]

  const estimateH = items.length * 28 + 48
  const top = Math.min(y, viewportH - estimateH - 8)

  const handleKey = (event: React.KeyboardEvent) => {
    const key = event.key.toLowerCase()
    const match = items.find(item => item.hotkey === key)
    if (match) {
      event.preventDefault()
      match.run()
      onClose()
    }
  }

  return (
    <div
      ref={menuRef}
      className="project-context-menu"
      style={{ left, top }}
      role="menu"
      tabIndex={-1}
      onKeyDown={handleKey}
    >
      <div className="project-context-menu-header">
        <span className="project-context-menu-prompt">$</span>
        <span className="project-context-menu-title">{projectDisplayName(project, t)}</span>
      </div>
      <div className="project-context-menu-list">
        {items.map(item => (
          <button
            key={item.key}
            type="button"
            className={`project-context-menu-item${item.danger ? ' danger' : ''}`}
            onClick={() => { item.run(); onClose() }}
          >
            <span className="project-context-menu-hotkey">[{item.hotkey}]</span>
            <span className="project-context-menu-icon">{item.icon}</span>
            <span className="project-context-menu-label">{item.label}</span>
          </button>
        ))}
      </div>
    </div>
  )
}
