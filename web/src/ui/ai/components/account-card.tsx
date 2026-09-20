import { type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Check, Loader2, Pencil, Plus, Trash2 } from 'lucide-react'
import { Button } from '../../settings/shadcn/ui'
import './account-card.css'

export interface AccountKeyStatus {
  type: 'ok' | 'missing' | 'auto'
  label: string
}

export interface AccountCardRowProps {
  name: string
  providerLabel: string
  keyStatus: AccountKeyStatus
  isActive: boolean
  onActivate: () => void
  onEdit: () => void
  onDelete: () => void
  activateTitle: string
  editTitle: string
  deleteTitle: string
  activeLabel: string
  leading?: ReactNode
}

export function AccountCardRow({
  name,
  providerLabel,
  keyStatus,
  isActive,
  onActivate,
  onEdit,
  onDelete,
  activateTitle,
  editTitle,
  deleteTitle,
  activeLabel,
  leading,
}: AccountCardRowProps) {
  return (
    <div className={`sp-account-card${isActive ? ' is-active' : ''}`}>
      <button
        type="button"
        className="sp-account-main"
        onClick={onActivate}
        title={activateTitle}
      >
        {leading}
        <span className="sp-account-name">{name}</span>
        <span className="sp-account-provider">{providerLabel}</span>
        <span className={`sp-account-key ${keyStatus.type}`}>{keyStatus.label}</span>
        {isActive && (
          <span className="sp-account-active-badge">
            <Check size={11} />
            {activeLabel}
          </span>
        )}
      </button>
      <div className="sp-account-actions">
        <button type="button" className="sp-account-action" onClick={onEdit} title={editTitle}>
          <Pencil size={14} />
        </button>
        <button type="button" className="sp-account-action" onClick={onDelete} title={deleteTitle}>
          <Trash2 size={14} />
        </button>
      </div>
    </div>
  )
}

export interface AccountListSectionProps {
  error?: string
  loading?: boolean
  loadingLabel?: string
  emptyLabel?: string
  children?: ReactNode
  onAdd?: () => void
  addLabel?: string
}

export function AccountListSection({
  error,
  loading,
  loadingLabel,
  emptyLabel,
  children,
  onAdd,
  addLabel,
}: AccountListSectionProps) {
  const childCount = Array.isArray(children) ? children.length : (children ? 1 : 0)
  return (
    <>
      {error && <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>}
      {loading ? (
        <div className="flex h-[120px] items-center justify-center rounded-lg border border-dashed border-border text-sm text-muted-foreground">
          <Loader2 size={15} className="sp-account-spinner" /> {loadingLabel}
        </div>
      ) : (
        <>
          {childCount === 0 && emptyLabel && (
            <p className="text-sm text-muted-foreground">{emptyLabel}</p>
          )}
          {childCount > 0 && <div className="sp-account-list">{children}</div>}
          {onAdd && (
            <Button type="button" size="sm" className="sp-account-add-btn" onClick={onAdd}>
              <Plus size={14} />
              {addLabel}
            </Button>
          )}
        </>
      )}
    </>
  )
}

export interface AccountDialogShellProps {
  onClose: () => void
  children: ReactNode
}

export function AccountDialogShell({ onClose, children }: AccountDialogShellProps) {
  return createPortal(
    <div className="sp-account-dialog-overlay" onClick={onClose}>
      <div className="sp-account-dialog-inner" onClick={(e) => e.stopPropagation()}>
        {children}
      </div>
    </div>,
    document.body,
  )
}

export interface AccountDialogFooterProps {
  saving?: boolean
  saveDisabled?: boolean
  saveLabel: string
  savingLabel?: string
  cancelLabel: string
  onClose: () => void
  onSave: () => void
}

export function AccountDialogFooter({
  saving,
  saveDisabled,
  saveLabel,
  savingLabel,
  cancelLabel,
  onClose,
  onSave,
}: AccountDialogFooterProps) {
  return (
    <div className="sp-account-dialog-footer">
      <Button type="button" variant="outline" size="sm" onClick={onClose}>
        {cancelLabel}
      </Button>
      <Button
        type="button"
        size="sm"
        disabled={saving || saveDisabled}
        onClick={onSave}
      >
        {saving && savingLabel ? (
          <>
            <Loader2 size={14} className="sp-account-spinner" />
            {savingLabel}
          </>
        ) : (
          saveLabel
        )}
      </Button>
    </div>
  )
}
