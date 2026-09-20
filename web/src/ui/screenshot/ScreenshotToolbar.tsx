import React from 'react'
import { useI18n } from '../../i18n'

type Tool = 'rect' | 'ellipse' | 'line' | 'brush' | 'number' | null

interface Props {
  rect: { x: number; y: number; w: number; h: number }
  tool: Tool
  onToolChange: (t: Tool) => void
  onDownload: () => void
  onConfirm: () => void
  onExit: () => void
}

const toolDefs: { id: Exclude<Tool, null>; labelKey: 'screenshot.toolbar.rect' | 'screenshot.toolbar.ellipse' | 'screenshot.toolbar.line' | 'screenshot.toolbar.brush' | 'screenshot.toolbar.number'; icon: string }[] = [
  { id: 'rect', labelKey: 'screenshot.toolbar.rect', icon: '\u25AD' },
  { id: 'ellipse', labelKey: 'screenshot.toolbar.ellipse', icon: '\u25CB' },
  { id: 'line', labelKey: 'screenshot.toolbar.line', icon: '\u2571' },
  { id: 'brush', labelKey: 'screenshot.toolbar.brush', icon: '\u270E' },
  { id: 'number', labelKey: 'screenshot.toolbar.number', icon: 'N' },
]

const btnStyle: React.CSSProperties = {
  width: 36, height: 32, display: 'flex', alignItems: 'center', justifyContent: 'center',
  border: '2px solid transparent', borderRadius: 4, cursor: 'pointer', fontSize: 16,
  background: 'rgba(40,40,40,0.85)', color: '#ddd',
}

const btnActive: React.CSSProperties = {
  ...btnStyle, background: 'transparent', borderColor: '#1a73e8', color: '#1a73e8',
}

export function ScreenshotToolbar({ rect, tool, onToolChange, onDownload, onConfirm, onExit }: Props) {
  const { t } = useI18n()
  const tbHeight = 48
  const margin = 8

  // position toolbar below the rect, or above if not enough space
  let top = rect.y + rect.h + margin
  if (top + tbHeight > window.innerHeight) {
    top = rect.y - tbHeight - margin
    if (top < 0) top = rect.y + rect.h + margin
  }
  let left = rect.x
  if (left + 520 > window.innerWidth) {
    left = Math.max(0, window.innerWidth - 530)
  }

  return (
    <div style={{
      position: 'absolute', left, top, zIndex: 20,
      display: 'flex', gap: 6, alignItems: 'center', padding: '6px 10px',
      borderRadius: 8, background: 'rgba(20,20,20,0.9)', boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
      fontFamily: 'system-ui, sans-serif',
    }}>
      {/* dimensions display */}
      <span style={{ color: '#999', fontSize: 13, marginRight: 4, fontFamily: 'monospace', whiteSpace: 'nowrap' }}>
        {rect.w} x {rect.h}
      </span>

      {/* tool buttons */}
      {toolDefs.map((td) => (
        <button key={td.id} title={t(td.labelKey)} style={tool === td.id ? btnActive : btnStyle}
          onClick={() => onToolChange(tool === td.id ? null : td.id)}>
          {td.icon}
        </button>
      ))}

      <div style={{ width: 1, height: 24, background: '#555', margin: '0 4px' }} />

      {/* action buttons */}
      <button style={{ ...btnStyle, width: 60, fontSize: 13, background: 'rgba(60,60,60,0.85)' }}
        onClick={onDownload} title={t('screenshot.toolbar.download')}>
        {'\u2B07'} {t('screenshot.toolbar.downloadLabel')}
      </button>
      <button style={{ ...btnStyle, width: 60, fontSize: 13 }}
        onClick={onExit} title={t('screenshot.toolbar.exit')}>
        {'\u2715'} {t('screenshot.toolbar.exitLabel')}
      </button>
      <button style={{ ...btnStyle, width: 60, fontSize: 13, background: '#1a73e8', color: '#fff' }}
        onClick={onConfirm} title={t('screenshot.toolbar.confirm')}>
        {'\u2713'} {t('screenshot.toolbar.confirmLabel')}
      </button>
    </div>
  )
}
