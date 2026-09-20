import { useRef, useLayoutEffect } from 'react'
import { Clipboard, Copy as CopyIcon, ExternalLink, FilePlus, FolderPlus, FolderOpen, Pencil, Scissors, Trash2 } from 'lucide-react'
import { useMenuDismiss } from '../hooks/useMenuDismiss'
import { useI18n } from '../../../i18n'

export interface SidebarFileMenuTarget {
  name: string
  path: string
  isDir: boolean
  x: number
  y: number
}

interface SidebarFileContextMenuProps {
  target: SidebarFileMenuTarget | null
  onClose: () => void
  onOpen: (target: SidebarFileMenuTarget) => void
  onNewFile: (dir: string) => void
  onNewFolder: (dir: string) => void
  onCopy: (target: SidebarFileMenuTarget) => void
  onCut: (target: SidebarFileMenuTarget) => void
  onPaste: (target: SidebarFileMenuTarget) => void
  canPaste: boolean
  onCopyPath: (path: string) => void
  onRename: (target: SidebarFileMenuTarget) => void
  onDelete: (target: SidebarFileMenuTarget) => void
}

export function SidebarFileContextMenu({
  target, onClose, onOpen, onNewFile, onNewFolder, onCopy, onCut, onPaste, canPaste, onCopyPath, onRename, onDelete,
}: SidebarFileContextMenuProps) {
  const menuRef = useRef<HTMLDivElement>(null)
  const { t } = useI18n()
  useMenuDismiss(menuRef, onClose, target)

  useLayoutEffect(() => {
    if (!target || !menuRef.current) return
    const menu = menuRef.current
    const rect = menu.getBoundingClientRect()
    const vw = document.documentElement.clientWidth
    const vh = document.documentElement.clientHeight
    const pad = 8
    let left = target.x
    let top = target.y
    if (left + rect.width > vw - pad) left = target.x - rect.width
    if (left < pad) left = pad
    if (top + rect.height > vh - pad) top = target.y - rect.height
    if (top < pad) top = pad
    menu.style.left = `${left}px`
    menu.style.top = `${top}px`
  }, [target])

  if (!target) return null

  const { name, path, isDir } = target
  const run = (fn: () => void) => () => { fn(); onClose() }

  return (
    <div
      ref={menuRef}
      className="fb-context-menu"
      style={{ left: target.x, top: target.y }}
      role="menu"
      tabIndex={-1}
    >
      <div className="fb-context-menu-header">
        <span className="fb-context-menu-title">{name}</span>
      </div>
      <div className="fb-context-menu-list">
        <button type="button" className="fb-context-menu-item" onClick={run(() => onOpen(target))}>
          <span className="fb-context-menu-icon">{isDir ? <FolderOpen size={13} /> : <ExternalLink size={13} />}</span>
          {t('fileBrowserContextMenu.open')}
        </button>

        {isDir && (
          <>
            <button type="button" className="fb-context-menu-item" onClick={run(() => onNewFile(path))}>
              <span className="fb-context-menu-icon"><FilePlus size={13} /></span>
              {t('fileBrowserContextMenu.newFile')}
            </button>
            <button type="button" className="fb-context-menu-item" onClick={run(() => onNewFolder(path))}>
              <span className="fb-context-menu-icon"><FolderPlus size={13} /></span>
              {t('fileBrowserContextMenu.newFolder')}
            </button>
          </>
        )}

        <button type="button" className="fb-context-menu-item" onClick={run(() => onCopy(target))}>
          <span className="fb-context-menu-icon"><CopyIcon size={13} /></span>
          {t('fileBrowserContextMenu.copy')}
        </button>
        <button type="button" className="fb-context-menu-item" onClick={run(() => onCut(target))}>
          <span className="fb-context-menu-icon"><Scissors size={13} /></span>
          {t('fileBrowserContextMenu.cut')}
        </button>
        {canPaste && (
          <button type="button" className="fb-context-menu-item" onClick={run(() => onPaste(target))}>
            <span className="fb-context-menu-icon"><Clipboard size={13} /></span>
            {t('fileBrowserContextMenu.paste')}
          </button>
        )}

        <div className="fb-context-menu-sep" />

        <button type="button" className="fb-context-menu-item" onClick={run(() => onCopyPath(path))}>
          <span className="fb-context-menu-icon"><CopyIcon size={13} /></span>
          {t('fileBrowserContextMenu.copyPath')}
        </button>

        <div className="fb-context-menu-sep" />

        <button type="button" className="fb-context-menu-item" onClick={run(() => onRename(target))}>
          <span className="fb-context-menu-icon"><Pencil size={13} /></span>
          {t('fileBrowserContextMenu.rename')}
        </button>
        <button type="button" className="fb-context-menu-item danger" onClick={run(() => onDelete(target))}>
          <span className="fb-context-menu-icon"><Trash2 size={13} /></span>
          {t('fileBrowserContextMenu.delete')}
        </button>
      </div>
    </div>
  )
}