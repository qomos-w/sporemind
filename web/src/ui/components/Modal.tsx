import { useEffect, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { useBrowserOverlay } from '../../ui/ai/browserOverlay'
import './Modal.css'

export interface ModalProps {
  open: boolean
  title: ReactNode
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  headerExtra?: ReactNode
  size?: 'sm' | 'md' | 'lg'
  closeOnOverlayClick?: boolean
  closeOnEscape?: boolean
  disableClose?: boolean
  dragWindow?: boolean
  /** Keep the overlay below the window titlebar so the topbar stays draggable
      and the window controls remain clickable while the modal is open. */
  belowTitlebar?: boolean
  className?: string
}

export function Modal({
  open,
  title,
  onClose,
  children,
  footer,
  headerExtra,
  size = 'md',
  closeOnOverlayClick = false,
  closeOnEscape = true,
  disableClose = false,
  dragWindow = false,
  belowTitlebar = false,
  className,
}: ModalProps) {
  const dialogRef = useRef<HTMLDivElement>(null)
  useBrowserOverlay(open)

  useEffect(() => {
    if (!open || !closeOnEscape) return
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !disableClose) {
        e.preventDefault()
        onClose()
      }
    }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [open, closeOnEscape, disableClose, onClose])

  useEffect(() => {
    if (!open || !closeOnOverlayClick) return
    const handlePointerDown = (e: MouseEvent) => {
      if (disableClose) return
      if (dialogRef.current && !dialogRef.current.contains(e.target as Node)) {
        onClose()
      }
    }
    document.addEventListener('mousedown', handlePointerDown)
    return () => document.removeEventListener('mousedown', handlePointerDown)
  }, [open, closeOnOverlayClick, disableClose, onClose])

  if (!open) return null

  return (
    <div className={`modal-overlay modal-overlay--${size}${belowTitlebar ? ' modal-overlay--below-titlebar' : ''}`}>
      <div
        ref={dialogRef}
        className={`modal-dialog modal-dialog--${size}${className ? ` ${className}` : ''}`}
        role="dialog"
        aria-modal="true"
      >
        <div className={`modal-header${dragWindow ? ' modal-header--drag-window' : ''}`}>
          <div className="modal-title-group">{title}</div>
          <button
            type="button"
            className={`modal-close${dragWindow ? ' modal-close--no-drag' : ''}`}
            onClick={onClose}
            disabled={disableClose}
            aria-label="Close"
          >
            <X size={16} />
          </button>
        </div>

        {headerExtra && <div className="modal-header-extra">{headerExtra}</div>}

        <div className="modal-body">{children}</div>

        {footer && <div className="modal-footer">{footer}</div>}
      </div>
    </div>
  )
}
