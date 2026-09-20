import React from 'react'
import './ViewToolbar.css'

interface ViewToolbarProps {
  children: React.ReactNode
}

export const ViewToolbar: React.FC<ViewToolbarProps> = ({ children }) => (
  <div className="view-toolbar">{children}</div>
)

interface VTBtnProps {
  title?: string
  active?: boolean
  disabled?: boolean
  onClick: () => void
  children: React.ReactNode
}

export const VTBtn: React.FC<VTBtnProps> = ({ title, active, disabled, onClick, children }) => (
  <button
    className={`vt-btn${active ? ' active' : ''}`}
    title={title}
    disabled={disabled}
    onClick={onClick}
  >
    {children}
  </button>
)

export const VTSep: React.FC = () => <div className="vt-sep" />
