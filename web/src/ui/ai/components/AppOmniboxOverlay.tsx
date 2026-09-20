import React, { useEffect, useMemo, useRef, useState, useCallback, useLayoutEffect } from 'react'
import { Search, Command, Trash2 } from 'lucide-react'
import type { AppOmniboxAction, AppOmniboxSection } from '../hooks/useAppOmnibox'
import { useI18n } from '../../../i18n'
import type { I18nKey } from '../../../i18n/types'
import { useDropdownVerticalFit } from '../hooks/useDropdownVerticalFit'
import './AppOmniboxOverlay.css'

export interface AppOmniboxOverlayProps {
  open: boolean
  onClose: () => void
  actions: AppOmniboxAction[]
  onSelect?: (action: AppOmniboxAction) => void
  isMobile?: boolean
  /** Position of the overlay. 'top' keeps it below the topbar (default);
   *  'bottom' anchors it above the composer toolbar. */
  position?: 'top' | 'bottom'
}

export interface AppOmniboxResultsProps {
  actions: AppOmniboxAction[]
  selectedIndex: number
  onSelectedIndexChange: (index: number) => void
  onSelect: (action: AppOmniboxAction) => void
  onDelete?: (action: AppOmniboxAction) => void
  dropdown?: boolean
}

// Localized section header keys (rendered via the i18n catalog).
const SECTION_LABEL_KEYS: Record<AppOmniboxSection, I18nKey> = {
  command: 'omnibox.sectionCommand',
  skill: 'omnibox.sectionSkill',
  bundle: 'omnibox.sectionBundle',
  componentMode: 'omnibox.sectionComponentMode',
  recent: 'omnibox.sectionRecent',
  project: 'omnibox.sectionProject',
  agent: 'omnibox.sectionAgent',
  browser: 'omnibox.sectionBrowser',
  mode: 'omnibox.sectionView',
  settings: 'omnibox.sectionSettings',
  session: 'omnibox.sectionSession',
  card: 'omnibox.sectionCard',
  file: 'omnibox.sectionFile',
}

const SECTION_ORDER: AppOmniboxSection[] = ['command', 'componentMode', 'bundle', 'recent', 'session', 'project', 'agent', 'browser', 'mode', 'settings', 'card', 'file', 'skill']

export function filterOmniboxActions(actions: AppOmniboxAction[], query: string): AppOmniboxAction[] {
  if (!query.trim()) return actions
  const q = query.toLowerCase().trim()
  return actions
    .map((action) => {
      const haystack = [action.label, ...action.keywords]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      let score = 0
      if (haystack === q) score = 1000
      else if (haystack.startsWith(q)) score = 500
      else if (haystack.includes(' ' + q)) score = 300
      else if (haystack.includes(q)) score = 100
      return { action, score }
    })
    .filter((item) => item.score > 0)
    .sort((a, b) => b.score - a.score)
    .map((item) => item.action)
}

export function orderOmniboxActions(actions: AppOmniboxAction[]): AppOmniboxAction[] {
  return SECTION_ORDER.flatMap((section) => actions.filter((action) => action.section === section))
}

export function AppOmniboxResults({
  actions,
  selectedIndex,
  onSelectedIndexChange,
  onSelect,
  onDelete,
  dropdown = false,
}: AppOmniboxResultsProps): React.ReactElement {
  const listRef = useRef<HTMLDivElement>(null)
  const { t } = useI18n()
  useDropdownVerticalFit(dropdown, listRef, 'up')

  const grouped = useMemo(() => {
    return SECTION_ORDER.map((section) => [section, actions.filter((action) => action.section === section)] as const)
      .filter(([, items]) => items.length > 0)
  }, [actions])

  useEffect(() => {
    listRef.current?.querySelector('[data-omnibox-selected="true"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [selectedIndex])

  let index = 0
  return (
    <div
      ref={listRef}
      className={`app-omnibox-list ${dropdown ? 'app-omnibox-results-dropdown' : ''}`}
      role="listbox"
      aria-label="Omnibox results"
    >
      {actions.length === 0 ? (
        <div className="app-omnibox-empty">{t('omnibox.noResults')}</div>
      ) : grouped.map(([section, items]) => (
        <div key={section} className="app-omnibox-section">
          <div className="app-omnibox-section-title">{t(SECTION_LABEL_KEYS[section])}</div>
          {items.map((action) => {
            const itemIndex = index++
            const selected = itemIndex === selectedIndex
            return (
              <div
                key={action.id}
                role="option"
                aria-selected={selected}
                className={`app-omnibox-item app-omnibox-item-${section} ${action.id.startsWith('project:') ? 'app-omnibox-item-project' : ''} ${selected ? 'selected' : ''}`}
                data-omnibox-selected={selected}
                onMouseDown={(event) => {
                  event.preventDefault()
                  onSelect(action)
                }}
                onMouseEnter={() => onSelectedIndexChange(itemIndex)}
              >
                {action.icon && <span className="app-omnibox-item-icon">{action.icon}</span>}
                <span className="app-omnibox-item-label">{action.label}</span>
                {action.description && <span className="app-omnibox-item-description">{action.description}</span>}
                {action.shortcut && (
                  <span className={`app-omnibox-item-shortcut ${action.onDelete ? 'app-omnibox-item-shortcut--with-delete' : ''}`}>
                    {action.shortcut}
                  </span>
                )}
                {action.onDelete && (
                  <button
                    type="button"
                    className="app-omnibox-item-delete"
                    aria-label={t('common.delete')}
                    onMouseDown={(event) => {
                      event.preventDefault()
                      event.stopPropagation()
                      onDelete?.(action)
                    }}
                  >
                    <Trash2 size={14} />
                  </button>
                )}
              </div>
            )
          })}
        </div>
      ))}
    </div>
  )
}

export function AppOmniboxOverlay({ open, onClose, actions, onSelect, isMobile, position = 'top' }: AppOmniboxOverlayProps): React.ReactElement | null {
  const [query, setQuery] = useState('')
  const [selectedIndex, setSelectedIndex] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null)
  const { t } = useI18n()

  // Anchor the panel centered on the message stream (.ai-shell-content).
  // 'top' places it just below the topbar (legacy trigger position); 'bottom'
  // places it just above the composer toolbar so the composer stays usable.
  // On mobile the virtual keyboard can resize the visual viewport, so we keep
  // the panel inside the visible area by reacting to visualViewport changes.
  useLayoutEffect(() => {
    if (!open) { setPos(null); return }
    const viewport = window.visualViewport ?? null
    const viewportHeight = () => viewport ? viewport.height : window.innerHeight

    const apply = () => {
      const content = document.querySelector('.ai-shell-content') as HTMLElement | null
      const cRect = content?.getBoundingClientRect()
      const w = containerRef.current?.offsetWidth ?? 560
      const h = containerRef.current?.offsetHeight ?? 360
      let top: number
      let centerX: number
      if (cRect) {
        centerX = cRect.left + cRect.width / 2
        if (position === 'bottom' && !isMobile) {
          const composer = document.querySelector('.ai-composer-actions-frame, .ai-composer-toolbar') as HTMLElement | null
          const composerRect = composer?.getBoundingClientRect()
          top = composerRect ? composerRect.top - h - 8 : cRect.bottom - h - 8
        } else {
          const topbar = document.querySelector('.ai-shell-topbar') as HTMLElement | null
          const tRect = topbar?.getBoundingClientRect()
          top = tRect ? tRect.bottom + 6 : 80
        }
      } else {
        top = 80
        centerX = window.innerWidth / 2
      }
      const vh = viewportHeight()
      top = Math.max(16, Math.min(top, Math.max(16, vh - h - 16)))
      let left = centerX - w / 2
      left = Math.max(16, Math.min(left, window.innerWidth - w - 16))
      setPos({ top, left })
    }

    apply()
    const vv = window.visualViewport
    if (vv) {
      vv.addEventListener('resize', apply)
      vv.addEventListener('scroll', apply)
    }
    window.addEventListener('resize', apply)
    return () => {
      if (vv) {
        vv.removeEventListener('resize', apply)
        vv.removeEventListener('scroll', apply)
      }
      window.removeEventListener('resize', apply)
    }
  }, [open, position, isMobile])

  useEffect(() => {
    if (open) {
      setQuery('')
      setSelectedIndex(0)
    }
  }, [open])

  useEffect(() => {
    if (open) inputRef.current?.focus()
  }, [open])

  const visibleActions = useMemo(() => orderOmniboxActions(filterOmniboxActions(actions, query)), [actions, query])

  useEffect(() => {
    setSelectedIndex(0)
  }, [query])

  const execute = useCallback((action: AppOmniboxAction) => {
    onSelect?.(action)
    onClose()
  }, [onSelect, onClose])

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
      return
    }
    if (visibleActions.length === 0) return
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSelectedIndex((i) => (i + 1) % visibleActions.length)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSelectedIndex((i) => (i - 1 + visibleActions.length) % visibleActions.length)
    } else if (e.key === 'Home') {
      e.preventDefault()
      setSelectedIndex(0)
    } else if (e.key === 'End') {
      e.preventDefault()
      setSelectedIndex(visibleActions.length - 1)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      const action = visibleActions[selectedIndex]
      if (action) execute(action)
    }
  }, [visibleActions, selectedIndex, execute, onClose])

  if (!open) return null

  return (
    <div className="app-omnibox-backdrop" onClick={onClose}>
      <div
        ref={containerRef}
        className={`app-omnibox-container ${isMobile ? 'mobile' : ''}`}
        style={pos ? { top: pos.top, left: pos.left } : { visibility: 'hidden' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="app-omnibox-input-row">
          <Search size={18} className="app-omnibox-search-icon" />
          <input
            ref={inputRef}
            className="app-omnibox-input"
            type="text"
            placeholder={t('omnibox.searchPlaceholder')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={handleKeyDown}
          />
          <span className="app-omnibox-hint">Esc</span>
        </div>
        <AppOmniboxResults
          actions={visibleActions}
          selectedIndex={selectedIndex}
          onSelectedIndexChange={setSelectedIndex}
          onSelect={execute}
          onDelete={(action) => {
            action.onDelete?.()
            onClose()
          }}
        />
        <div className="app-omnibox-footer">
          <div className="app-omnibox-footer-kbd"><Command size={12} /><span>K</span></div>
          <span className="app-omnibox-footer-hint">{t('omnibox.hintOpen')}</span>
          <span className="app-omnibox-footer-divider" />
          <span className="app-omnibox-footer-kbd">↑↓</span>
          <span className="app-omnibox-footer-hint">{t('omnibox.hintSelect')}</span>
          <span className="app-omnibox-footer-divider" />
          <span className="app-omnibox-footer-kbd">↵</span>
          <span className="app-omnibox-footer-hint">{t('omnibox.hintRun')}</span>
        </div>
      </div>
    </div>
  )
}
