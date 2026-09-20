import React from 'react'
import { Hash } from 'lucide-react'
import type { MonoCardListItem } from '../../../domain/mono-types'
import { CARD_ICON_DEFINITIONS } from './cardIconLibrary'
import { CARD_ACCENTS, CARD_ACCENT_PRESET_HEX, CARD_NODE_SIZE_MAX, CARD_NODE_SIZE_MIN, cardVisual, defaultCardVisual, type CardAccent } from './cardVisual'
import { CardIcon } from './CardIcon'
import { useBrowserOverlay } from '../browserOverlay'
import { useI18n } from '../../../i18n'
import './CardVisualDialog.css'

// Solid colour swatches shown in each colour row. 'slate' (the default accent)
// is excluded here and surfaced as a dedicated "clear" button at the first
// position instead, so the default reads as an action rather than masquerading
// as a grey colour choice.
const COLOR_PRESETS = CARD_ACCENTS.filter(value => value !== 'slate')

export function CardVisualDialog({ card, onClose, onSave }: { card: MonoCardListItem | null; onClose: () => void; onSave: (data: Record<string, unknown>) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = React.useState('')
  const [icon, setIcon] = React.useState('')
  const [accent, setAccent] = React.useState<CardAccent>('slate')
  // iconColor: when set, overrides the accent-derived icon color so background
  // and icon can be tuned independently. null = follow accent.
  const [iconColor, setIconColor] = React.useState<string | null>(null)
  // background: when set, overrides the accent-derived background. null = use
  // the selected accent. Mirrors the icon-color model so both rows share the
  // same editing logic (# custom + 8 presets).
  const [background, setBackground] = React.useState<string | null>(null)
  // Node size slider (topology only). Defaults to the minimum.
  const [nodeSize, setNodeSize] = React.useState<number>(CARD_NODE_SIZE_MIN)
  const iconColorInputRef = React.useRef<HTMLInputElement>(null)
  const bgColorInputRef = React.useRef<HTMLInputElement>(null)

  useBrowserOverlay(!!card)

  React.useEffect(() => {
    if (!card) return
    const visual = cardVisual(card)
    const defaults = defaultCardVisual()
    setIcon(visual.icon ?? '')
    setAccent(visual.accent ?? defaults.accent)
    setIconColor(visual.color ?? null)
    setBackground(visual.background ?? null)
    setNodeSize(typeof visual.size === 'number' ? visual.size : CARD_NODE_SIZE_MIN)
    setQuery('')
  }, [card])

  if (!card) return null
  const gridIconColor = iconColor ?? '#64748b'
  const filtered = CARD_ICON_DEFINITIONS.filter(definition => {
    const text = [definition.name, definition.label, ...definition.keywords].join(' ').toLowerCase()
    return text.includes(query.trim().toLowerCase())
  })
  const reset = () => {
    const defaults = defaultCardVisual()
    setIcon(''); setAccent(defaults.accent); setIconColor(null); setBackground(null); setNodeSize(CARD_NODE_SIZE_MIN)
  }
  const handleSave = () => {
    // Persist accent and, when set, the icon (+ optional custom icon color /
    // background). iconType is derived at read time from whether the name lives
    // in the lucide library, so an unset icon leaves no `icon` field at all.
    const visual: Record<string, unknown> = { size: nodeSize }
    if (icon) visual.icon = icon
    if (accent && accent !== 'slate') visual.accent = accent
    if (iconColor) visual.color = iconColor
    if (background) visual.background = background
    onSave({ visual })
  }
  return <div className="card-visual-dialog-backdrop" onMouseDown={event => { if (event.target === event.currentTarget) onClose() }}>
    <div className="card-visual-dialog" role="dialog" aria-modal="true" aria-label="Card visual properties">
      <div className="card-visual-body">
        <input className="card-visual-search" value={query} onChange={event => setQuery(event.target.value)} placeholder={t('cardVisual.searchIcon')} />
        <div className="card-visual-field"><span>{t('cardVisual.iconColor')}</span><div className="card-visual-color-row">
          <CustomColorSwatch
            selected={iconColor !== null && !CARD_ACCENTS.some(a => CARD_ACCENT_PRESET_HEX[a] === iconColor)}
            color={iconColor ?? undefined}
            inputRef={iconColorInputRef}
            onPick={setIconColor}
            label="Custom icon color"
          />
          <button type="button" className={`card-visual-clear${iconColor === null ? ' selected' : ''}`} title={t('cardVisual.clear')} aria-label={t('cardVisual.clearIconColor')} onClick={() => setIconColor(null)}></button>
          {COLOR_PRESETS.map(value => <button key={value} type="button" className={`card-visual-accent card-visual-accent--${value}${iconColor === CARD_ACCENT_PRESET_HEX[value] ? ' selected' : ''}`} aria-label={value} onClick={() => setIconColor(CARD_ACCENT_PRESET_HEX[value])} />)}
        </div></div>
        <div className="card-visual-icon-grid">
          {filtered.map(definition => <button key={definition.name} type="button" title={definition.label} className={icon === definition.name ? 'selected' : ''} onClick={() => setIcon(icon === definition.name ? '' : definition.name)}><CardIcon name={definition.name} size={20} color={gridIconColor} /></button>)}
        </div>
        <div className="card-visual-field"><span>{t('cardVisual.backgroundColor')}</span><div className="card-visual-color-row">
          <CustomColorSwatch
            selected={background !== null}
            color={background ?? undefined}
            inputRef={bgColorInputRef}
            onPick={setBackground}
            label="Custom background color"
          />
          <button type="button" className={`card-visual-clear${background === null && accent === 'slate' ? ' selected' : ''}`} title={t('cardVisual.clear')} aria-label={t('cardVisual.clearBackgroundColor')} onClick={() => { setAccent('slate'); setBackground(null) }}></button>
          {COLOR_PRESETS.map(value => <button key={value} type="button" className={`card-visual-accent card-visual-accent--${value}${background === null && accent === value ? ' selected' : ''}`} aria-label={value} onClick={() => { setAccent(value); setBackground(null) }} />)}
        </div></div>
        <div className="card-visual-field"><span>{t('cardVisual.nodeSize')}</span><input className="card-visual-slider" type="range" min={CARD_NODE_SIZE_MIN} max={CARD_NODE_SIZE_MAX} value={nodeSize} onChange={event => setNodeSize(Number(event.target.value))} /></div>
      </div>
      <div className="card-visual-dialog-actions"><button type="button" onClick={reset}>{t('cardVisual.reset')}</button><span /><button type="button" onClick={onClose}>{t('cardVisual.cancel')}</button><button type="button" className="primary" onClick={handleSave}>{t('cardVisual.apply')}</button></div>
    </div>
  </div>
}

// Shared "#" custom-color swatch used by both the icon-color and
// background-color rows so they share identical editing logic. Renders a
// circular swatch filled with the chosen color (or a neutral hash glyph when
// unset) and opens the native color picker on click.
function CustomColorSwatch({ selected, color, inputRef, onPick, label }: {
  selected: boolean
  color?: string
  inputRef: React.RefObject<HTMLInputElement | null>
  onPick: (value: string) => void
  label: string
}) {
  return (
    <button
      type="button"
      className={`card-visual-color-custom${selected ? ' selected' : ''}`}
      style={color ? { background: color } : undefined}
      onClick={() => inputRef.current?.click()}
      aria-label={label}
    >
      <Hash size={14} />
      <input
        ref={inputRef}
        type="color"
        value={color ?? '#64748b'}
        onChange={event => onPick(event.target.value)}
        aria-hidden="true"
        tabIndex={-1}
      />
    </button>
  )
}
