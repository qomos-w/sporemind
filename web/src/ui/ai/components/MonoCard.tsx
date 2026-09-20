import React, { useState, useEffect, useLayoutEffect, useCallback, useContext, useRef, useMemo } from 'react'
import { createPortal } from 'react-dom'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { processWikiWords, wikiUrlTransform } from './parts/wikiword'
import { makeRehypePlugins, useFileReference, resolveFileReference } from './parts/file-reference.tsx'
import { AIShellContext } from '../context/AIShellContext'
import {
  Check,
  CheckSquare,
  Plus,
  X,
  ChevronRight,
  ChevronDown,
  ChevronUp,
  Tag as TagIcon,
  Calendar,
  AlertCircle,
  FilePlus,
  BookOpen,
  Copy,
  Download,
  Trash2,
  PanelTop,
  Minimize2,
  Maximize2,
  MessageCircle,
  Search,
  Eye,
  EyeOff,
} from 'lucide-react'
import { useMonoStore } from '../hooks/useMonoStore'
import { RichCardEditor } from '../../editor'
import { computeUnifiedDiff } from '../../editor/card-diff'
import { DiffBlock } from './parts/DiffBlock.tsx'
import {
  userDataEntries,
  RESERVED_DATA_KEYS,
  type MonoCard as MonoCardModel,
  type MonoCardListItem,
  type MonoCardType,
  type ReminderPriority,
  DATA_FIELD_TYPES,
  DATA_FIELD_TEXT,
  dataFieldTypeDefOf,
  dataFieldTypeDefById,
  cardReferenceId,
  makeCardReference,
} from '../../../domain/mono-types'
import { PRIORITY_META, STATUS_META, getCardTypeMeta, TYPE_META, CARD_TYPES } from './mono-card-meta.tsx'
import { CardIcon } from './CardIcon'
import { CardVisualDialog } from './CardVisualDialog'
import { cardVisual, getCardVisualStyle } from './cardVisual'
import {
  isBuiltinCard,
  isBuiltinTag,
  tagMatchesCard,
  resolveBuiltinAlias,
  getBuiltinTagDisplayName,
} from '../../../domain/builtin-cards'
import { useI18n } from '../../../i18n'
import type { I18nKey } from '../../../i18n/types'
import './MonoCard.css'
import '../../markdown-content.css'
import { BuiltinCardDataPanel } from './builtin-card-data'


const REMARK_PLUGINS = [remarkGfm]

const CardBody: React.FC<{
  body: string
  cardId: string
  onWikiWordClick: (cardId: string, word: string) => void
}> = React.memo(({ body, cardId, onWikiWordClick }) => {
  const fileRefCtx = useFileReference()
  const aiShellCtx = useContext(AIShellContext)
  const processed = useMemo(() => processWikiWords(body), [body])
  const rehypePlugins = useMemo(
    () => makeRehypePlugins({
      projectId: fileRefCtx?.projectId ?? null,
      projectRoot: fileRefCtx?.projectRoot ?? null,
    }),
    [fileRefCtx?.projectId, fileRefCtx?.projectRoot],
  )
  const components = useMemo(() => ({
    table: ({ children, ...props }: React.ComponentProps<'table'>) => (
      <div className="table-wrapper">
        <table {...props}>{children}</table>
      </div>
    ),
    a: ({ href, children, className, ...props }: React.ComponentProps<'a'>) => {
      if (href?.startsWith('wiki:')) {
        return (
          <a
            className="wiki-word-link"
            href="#"
            onClick={(e) => { e.preventDefault(); onWikiWordClick(cardId, decodeURIComponent(href.slice(5))) }}
          >
            {children}
          </a>
        )
      }
      if (typeof className === 'string' && className.includes('ai-file-ref')) {
        return <a {...props} className={className}>{children}</a>
      }
      return <a href={href} target="_blank" rel="noreferrer">{children}</a>
    },
  }), [cardId, onWikiWordClick])

  const handleClick = async (e: React.MouseEvent) => {
    const target = e.target as HTMLElement
    const fileRef = target.closest('.ai-file-ref') as HTMLElement | null
    if (!fileRef) return
    const filePath = fileRef.dataset.aiFilePath
    if (!filePath) return

    e.preventDefault()
    e.stopPropagation()

    const lineStr = fileRef.dataset.aiFileLine
    const line = lineStr ? parseInt(lineStr, 10) : undefined
    const lineEndStr = fileRef.dataset.aiFileLineEnd
    const lineEnd = lineEndStr ? parseInt(lineEndStr, 10) : undefined

    if (aiShellCtx?.onOpenFile) {
      const ref = {
        raw: filePath,
        path: filePath,
        ext: filePath.split('.').pop()?.toLowerCase() ?? '',
        line,
        lineEnd,
      }
      const resolved = await resolveFileReference(ref, fileRefCtx?.projectRoot ?? null)
      if (!resolved) return
      aiShellCtx.onOpenFile(resolved.filePath, undefined, resolved.line ?? line, resolved.lineEnd ?? lineEnd)
    }
  }

  return (
    <div onClick={(e) => { void handleClick(e) }}>
      <Markdown
        remarkPlugins={REMARK_PLUGINS}
        rehypePlugins={rehypePlugins}
        urlTransform={wikiUrlTransform}
        components={components}
      >
        {processed}
      </Markdown>
    </div>
  )
})



const TagSearchInput: React.FC<{
  availableTags: string[]
  selectedTags: string[]
  onAdd: (tag: string) => void
}> = ({ availableTags, selectedTags, onAdd }) => {
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null)
  const { t } = useI18n()

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        if (dropdownRef.current && dropdownRef.current.contains(e.target as Node)) return
        setOpen(false)
        setQuery('')
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  useLayoutEffect(() => {
    if (!open || !containerRef.current) return
    setPos(clampDropdownToCard(containerRef.current, 200))
  }, [open])

  useEffect(() => {
    if (open) inputRef.current?.focus()
  }, [open])

  const q = query.trim().toLowerCase()
  const tagDisplay = (tag: string) => isBuiltinTag(tag) ? getBuiltinTagDisplayName(tag, t) : tag
  const matching = availableTags
    .filter(tag => !selectedTags.includes(tag))
    .filter(tag => q === '' || tag.toLowerCase().includes(q) || tagDisplay(tag).toLowerCase().includes(q))
    .slice(0, 8)
  const aliasResolved = q !== '' ? resolveBuiltinAlias(query.trim(), t) : null
  const exactExists = availableTags.some(tag => tag.toLowerCase() === q) || aliasResolved !== null
  const canCreate = q !== '' && !exactExists && !selectedTags.includes(query.trim())

  const add = (tag: string) => {
    const resolved = resolveBuiltinAlias(tag, t) ?? tag.trim()
    onAdd(resolved)
    setQuery('')
    setOpen(false)
  }

  return (
    <div className="mono-card-detail-editor-tag-add" ref={containerRef}>
      {open ? (
        <input
          ref={inputRef}
          className="mono-card-detail-editor-tag-add-input"
          value={query}
          onChange={e => setQuery(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter') {
              e.preventDefault()
              if (matching.length > 0) add(matching[0]!)
              else if (canCreate) add(query.trim())
            } else if (e.key === 'Escape') {
              setOpen(false)
              setQuery('')
            }
          }}
          placeholder={t('storyRiver.placeholder.searchTags')}
        />
      ) : (
        <button type="button" className="mono-card-detail-editor-tag-add-trigger" onClick={() => setOpen(true)} title={t('storyRiver.placeholder.searchTags')}>
          <Plus size={10} />
        </button>
      )}
      {open && pos && (matching.length > 0 || canCreate) && createPortal(
        <div ref={dropdownRef} className="mono-card-detail-editor-tag-dropdown" style={{ top: pos.top, left: pos.left, position: 'fixed' }}>
          {matching.map(tag => (
            <button
              key={tag}
              type="button"
              className="mono-card-detail-editor-tag-option"
              onClick={() => add(tag)}
            >
              <TagIcon size={10} />
              <span>{tagDisplay(tag)}</span>
            </button>
          ))}
          {canCreate && (
            <button
              type="button"
              className="mono-card-detail-editor-tag-option create"
              onClick={() => add(query.trim())}
            >
              <Plus size={10} />
              <span>Create "{query.trim()}"</span>
            </button>
          )}
        </div>,
        document.body
      )}
    </div>
  )
}

const CardReferenceInput: React.FC<{
  value: string
  onChange: (id: string) => void
}> = ({ value, onChange }) => {
  const cards = useMonoStore(s => s.cards)
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const selectedId = cardReferenceId(value) ?? ''
  const selectedCard = useMemo(() => cards.find(c => c.id === selectedId), [cards, selectedId])

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const q = query.trim().toLowerCase()
  const matching = useMemo(() => {
    return cards
      .filter(c => c.id !== selectedId)
      .filter(c => q === '' || c.id.toLowerCase().includes(q) || c.id.toLowerCase().includes(q))
      .slice(0, 8)
  }, [cards, selectedId, q])

  const select = (id: string) => {
    onChange(id)
    setQuery('')
    setOpen(false)
  }

  return (
    <div className="mono-card-detail-editor-tag-search mono-card-detail-editor-card-ref" ref={containerRef}>
      {selectedId ? (
        <span className="mono-card-detail-editor-card-ref-chip">
          <MessageCircle size={10} />
          <span>{selectedCard?.id || selectedId}</span>
          <button type="button" onClick={() => select('')} title="Clear">×</button>
        </span>
      ) : (
        <>
          <Search size={11} className="mono-card-detail-editor-tag-search-icon" />
          <input
            className="mono-card-detail-editor-tag-search-input"
            value={query}
            onChange={e => { setQuery(e.target.value); setOpen(true) }}
            onFocus={() => setOpen(true)}
            onKeyDown={e => {
              if (e.key === 'Enter') {
                e.preventDefault()
                if (matching.length > 0) select(matching[0]!.id)
              } else if (e.key === 'Escape') {
                setOpen(false)
                setQuery('')
              }
            }}
            placeholder={t('storyRiver.placeholder.searchCards')}
          />
        </>
      )}
      {open && matching.length > 0 && !selectedId && (
        <div className="mono-card-detail-editor-tag-dropdown" style={{ position: 'absolute', top: 'calc(100% + 2px)', left: 0, right: 0, minWidth: 200 }}>
          {matching.map(c => (
            <button key={c.id} type="button" className="mono-card-detail-editor-tag-option" onClick={() => select(c.id)}>
              <span className="mono-card-detail-editor-tag-option-type">{t(`monoCard.type.${c.type || 'wiki'}` as I18nKey)}</span>
              <span>{c.id}</span>
              <span className="mono-card-detail-editor-tag-option-id">{c.id}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

const clampDropdownToCard = (triggerEl: HTMLElement, dropdownWidth: number): { top: number; left: number } => {
  const rect = triggerEl.getBoundingClientRect()
  const card = triggerEl.closest('.mono-card-detail, .mono-card-detail-editor')
  const minX = card ? card.getBoundingClientRect().left : 0
  const maxRight = card ? card.getBoundingClientRect().right : window.innerWidth
  let left = rect.left
  if (left + dropdownWidth > maxRight) left = Math.max(minX, maxRight - dropdownWidth)
  return { top: rect.bottom + 4, left }
}

const InlineSelect: React.FC<{
  value: string
  options: { value: string; label: string; icon?: React.ReactNode }[]
  onChange: (value: string | undefined) => void
  allowNone?: boolean
  children: React.ReactNode
}> = ({ value, options, onChange, allowNone, children }) => {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLSpanElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null)

  useLayoutEffect(() => {
    if (!open || !ref.current) return
    setPos(clampDropdownToCard(ref.current, 200))
  }, [open])

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      const target = e.target as Node
      if (ref.current && ref.current.contains(target)) return
      if (dropdownRef.current && dropdownRef.current.contains(target)) return
      setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  return (
    <span ref={ref} className="mono-card-inline-select">
      <span className="mono-card-inline-edit-trigger" onClick={() => setOpen(!open)}>{children}</span>
      {open && pos && createPortal(
        <div ref={dropdownRef} className="mono-card-detail-menu-dropdown mono-card-inline-select-dropdown" style={{ top: pos.top, left: pos.left, position: 'fixed' }}>
          {allowNone && (
            <button onClick={() => { onChange(undefined); setOpen(false) }}>None</button>
          )}
          {options.map(opt => (
            <button key={opt.value} className={opt.value === value ? 'active' : ''} onClick={() => { onChange(opt.value); setOpen(false) }}>
              {opt.icon}
              <span>{opt.label}</span>
            </button>
          ))}
        </div>,
        document.body
      )}
    </span>
  )
}

const InlineDueEdit: React.FC<{
  value?: string
  onChange: (value: string | undefined) => void
  children: React.ReactNode
}> = ({ value, onChange, children }) => {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLSpanElement>(null)
  const popoverRef = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null)

  const dueLocal = useMemo(() => {
    if (!value) return ''
    const d = new Date(value)
    if (Number.isNaN(d.getTime())) return ''
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
  }, [value])

  useLayoutEffect(() => {
    if (!open || !ref.current) return
    setPos(clampDropdownToCard(ref.current, 240))
  }, [open])

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      const target = e.target as Node
      if (ref.current && ref.current.contains(target)) return
      if (popoverRef.current && popoverRef.current.contains(target)) return
      setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  return (
    <span ref={ref} className="mono-card-inline-due">
      <span className="mono-card-inline-edit-trigger" onClick={() => setOpen(!open)}>{children}</span>
      {open && pos && createPortal(
        <div ref={popoverRef} className="mono-card-inline-popover" style={{ top: pos.top, left: pos.left, position: 'fixed' }}>
          <input
            type="datetime-local"
            value={dueLocal}
            autoFocus
            onChange={e => onChange(e.target.value ? new Date(e.target.value).toISOString() : undefined)}
          />
          {value && (
            <button onClick={() => { onChange(undefined); setOpen(false) }}>Clear</button>
          )}
        </div>,
        document.body
      )}
    </span>
  )
}

interface DraftCardEditorProps {
  cardId: string
  meta: Partial<MonoCardModel>
  body: string
  onMetaChange: (meta: Partial<MonoCardModel>) => void
  onBodyChange: (body: string) => void
  onSave: () => void
  onCancel: () => void
  availableTags?: string[]
  allCards?: MonoCardListItem[]
  isEntering?: boolean
  isRemoving?: boolean
  onWikiWordClick?: (cardId: string, word: string) => void
}

const noop = () => {}

export const DraftCardEditor: React.FC<DraftCardEditorProps> = React.memo(({ cardId, meta, body, onMetaChange, onBodyChange, onSave, onCancel, availableTags, allCards, isEntering, isRemoving, onWikiWordClick }) => {
  const card = useMemo(() => ({
    id: cardId,
    type: meta.type || 'wiki',
    tags: meta.tags || [],
    list: meta.list || [],
    created: meta.created || '',
    modified: meta.modified || '',
    body,
    raw: '',
    data: meta.data,
  } as MonoCardModel), [cardId, meta, body])

  return (
    <MonoCardDetail
      card={card}
      isEditing={true}
      editMeta={meta}
      editBody={body}
      isFolded={false}
      isEntering={isEntering || false}
      isRemoving={isRemoving || false}
      onMetaChange={(_, m) => onMetaChange(m)}
      onBodyChange={(_, b) => onBodyChange(b)}
      onStartEdit={noop}
      onSaveEdit={() => onSave()}
      onCancelEdit={() => onCancel()}
      onDelete={noop}
      onClose={noop}
      onToggleFold={noop}
      onFoldOthers={noop}
      onCloseOthers={noop}
      onClone={noop}
      onNewHere={noop}
      onNewJournalHere={noop}
      onExport={noop}
      onTriggerTimerCard={async () => {}}
      onOpenCard={noop}
      onWikiWord={onWikiWordClick ?? noop}
      onTagClick={noop}
      availableTags={availableTags}
      allCards={allCards}
      showMenu={false}
    />
  )
})

function cardHasContent(card: MonoCardModel): boolean {
  return Boolean(
    card.body.trim() ||
    card.tags.length > 0 ||
    card.priority ||
    card.status ||
    card.due ||
    card.parent ||
    userDataEntries(card.data).length > 0
  )
}

interface MonoCardDetailProps {
  card: MonoCardModel
  isEditing: boolean
  editMeta: Partial<MonoCardModel>
  editBody: string
  originalBody?: string
  isFolded: boolean
  isEntering: boolean
  isRemoving: boolean
  isPendingDelete?: boolean
  borderless?: boolean
  showTopActions?: boolean
  showMenu?: boolean
  /** Auto-save mode (right panel): replace the Cancel/Save pair with a single
   *  exit button that flushes via onSaveEdit. Default keeps both buttons. */
  singleExitEdit?: boolean
  topRightAction?: React.ReactNode
  onMetaChange: (id: string, meta: Partial<MonoCardModel>) => void
  onBodyChange: (id: string, body: string) => void
  onStartEdit: (id: string) => void
  onSaveEdit: (id: string) => void
  onCancelEdit: (id: string) => void
  onDelete: (id: string) => void
  onClose: (id: string) => void
  onToggleFold: (id: string) => void
  onFoldOthers: (id: string) => void
  onCloseOthers: (id: string) => void
  onClone: (id: string) => void
  onNewHere: (id: string) => void
  onNewJournalHere: (id: string) => void
  onExport: (id: string) => void
  onTriggerTimerCard: (id: string) => Promise<void>
  onOpenCard: (id: string) => void
  /** Zoom-to-fit a workflow map id (scheduler current_instance navigation). */
  onNavigateToMap?: (mapId: string) => void
  /** Update the cron expression for a scheduler card (non-editing mode only). */
  onUpdateScheduleCron?: (cardId: string, cron: string) => Promise<boolean>
  onOpenCardInRightPanel?: (id: string, label: string) => void
  onWikiWord: (cardId: string, word: string) => void
  onTagClick: (id: string, tag: string, e: React.MouseEvent) => void
  onTagContextMenu?: (id: string, tag: string, e: React.MouseEvent) => void
  availableTags?: string[]
  allCards?: MonoCardListItem[]
}

export const MonoCardDetail: React.FC<MonoCardDetailProps> = React.memo(({
  card,
  isEditing,
  editMeta,
  editBody,
  originalBody,
  isFolded,
  isEntering,
  isRemoving,
  isPendingDelete = false,
  borderless,
  showTopActions = true,
  showMenu = true,
  singleExitEdit = false,
  topRightAction,
  onMetaChange,
  onBodyChange,
  onStartEdit,
  onSaveEdit,
  onCancelEdit,
  onDelete,
  onClose,
  onToggleFold,
  onFoldOthers,
  onCloseOthers,
  onClone,
  onNewHere,
  onNewJournalHere,
  onExport,
  onTriggerTimerCard,
  onOpenCard,
  onOpenCardInRightPanel,
  onNavigateToMap,
  onUpdateScheduleCron,
  onWikiWord,
  onTagClick,
  onTagContextMenu,
  availableTags,
  allCards,
}) => {
  const [menuOpen, setMenuOpen] = useState(false)
  const [visualOpen, setVisualOpen] = useState(false)
  const [showDiff, setShowDiff] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const [menuPos, setMenuPos] = useState<{ top: number; left: number } | null>(null)
  const { t } = useI18n()

  const meta = useMemo(() =>
    isEditing ? { ...card, ...editMeta } as MonoCardModel : card,
    [card, editMeta, isEditing]
  )
  const displayBody = isEditing ? editBody : card.body
  const isDraft = card.id.startsWith('draft-')
  const isOverdue = meta.due && new Date(meta.due) < new Date()
  const canDelete = !isBuiltinCard(card.id) && !isDraft

  const visual = cardVisual(meta)
  const visualStyle = getCardVisualStyle(visual)
  const hasVisual = !!visual.icon || !!visual.accent || !!visual.emphasis || !!visual.background || !!visual.border
  const detailTypeMeta = getCardTypeMeta(meta.type as MonoCardType | undefined)

  const handleStartEdit = useCallback(() => onStartEdit(card.id), [onStartEdit, card.id])
  const handleClose = useCallback(() => onClose(card.id), [onClose, card.id])
  const handleDelete = useCallback(() => onDelete(card.id), [onDelete, card.id])
  const handleToggleFold = useCallback(() => onToggleFold(card.id), [onToggleFold, card.id])
  const handleFoldOthers = useCallback(() => onFoldOthers(card.id), [onFoldOthers, card.id])
  const handleCloseOthers = useCallback(() => onCloseOthers(card.id), [onCloseOthers, card.id])
  const handleClone = useCallback(() => onClone(card.id), [onClone, card.id])
  const handleNewHere = useCallback(() => onNewHere(card.id), [onNewHere, card.id])
  const handleNewJournalHere = useCallback(() => onNewJournalHere(card.id), [onNewJournalHere, card.id])
  const handleExport = useCallback(() => onExport(card.id), [onExport, card.id])
  const handleOpenInRightPanel = useCallback(() => onOpenCardInRightPanel?.(card.id, card.id), [onOpenCardInRightPanel, card.id])
  const handleTagClick = useCallback((tag: string, e: React.MouseEvent) => onTagClick(card.id, tag, e), [onTagClick, card.id])
  const handleMetaChange = useCallback((m: Partial<MonoCardModel>) => onMetaChange(card.id, m), [onMetaChange, card.id])
  const handleBodyChange = useCallback((b: string) => onBodyChange(card.id, b), [onBodyChange, card.id])
  const handleSaveEdit = useCallback(() => onSaveEdit(card.id), [onSaveEdit, card.id])
  const handleCancelEdit = useCallback(() => onCancelEdit(card.id), [onCancelEdit, card.id])

  const selectedTags = meta.tags || []
  const addTag = useCallback((tag: string) => {
    const tg = tag.trim()
    if (!tg || selectedTags.includes(tg)) return
    handleMetaChange({ tags: [...selectedTags, tg] })
  }, [selectedTags, handleMetaChange])
  const removeTag = useCallback((tag: string) => {
    handleMetaChange({ tags: selectedTags.filter(t => t !== tag) })
  }, [selectedTags, handleMetaChange])

  // Workflow maps render their child-card list; the former opt-in "children"
  // tag is retired (it leaked into parent via the parent∈tags invariant).
  const showChildren = meta.type === 'workflow'
  const childCards = useMemo(() => {
    if (meta.standalone || !allCards) return []
    const self = { id: meta.id }
    const children = allCards.filter(c => !c.standalone && c.id !== meta.id && c.tags.some(tg => tagMatchesCard(tg, self)))
    const list = meta.list || []
    const ordered = list.map(id => children.find(c => c.id === id)).filter((c): c is MonoCardListItem => c !== undefined)
    const rest = children.filter(c => !list.includes(c.id))
    return [...ordered, ...rest]
  }, [meta.standalone, meta.id, meta.list, allCards])

  const moveChild = (id: string, dir: -1 | 1) => {
    const ids = childCards.map(c => c.id)
    const idx = ids.indexOf(id)
    if (idx === -1) return
    const target = idx + dir
    if (target < 0 || target >= ids.length) return
    ;[ids[idx], ids[target]] = [ids[target]!, ids[idx]!]
    handleMetaChange({ list: ids })
  }

  const removeChild = (id: string) => {
    handleMetaChange({ list: (meta.list || []).filter(x => x !== id) })
  }

  useLayoutEffect(() => {
    if (!menuOpen || !buttonRef.current) return
    const rect = buttonRef.current.getBoundingClientRect()
    setMenuPos({ top: rect.bottom + 4, left: rect.right - 180 })
  }, [menuOpen])

  useEffect(() => {
    if (!menuOpen) return
    const handler = (e: MouseEvent) => {
      const target = e.target as Node
      if (buttonRef.current && buttonRef.current.contains(target)) return
      if (dropdownRef.current && dropdownRef.current.contains(target)) return
      setMenuOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [menuOpen])

  const pendingDeleteClass = isPendingDelete ? ' mono-card-detail--pending-delete' : ''
  const articleClass = isEditing
    ? `mono-card-detail mono-card-detail--editing mono-card-detail--${meta.type || 'wiki'}${isEntering ? ' entering' : ''}${isRemoving ? ' leaving' : ''}${borderless ? ' mono-card-detail--borderless' : ''}${pendingDeleteClass}`
    : `mono-card-detail mono-card-detail--${card.type || 'wiki'}${isFolded ? ' folded' : ''}${isEntering ? ' entering' : ''}${isRemoving ? ' leaving' : ''}${borderless ? ' mono-card-detail--borderless' : ''}${pendingDeleteClass}`

  return (
    <article className={articleClass} id={`mono-card-${card.id}`} data-card-id={card.id} style={!borderless && hasVisual ? { background: visualStyle.background, boxShadow: visualStyle.shadow } : undefined}>
      <div className="mono-card-detail-header">
        <div className="mono-card-detail-title-row">
          {isEditing ? (
            <button type="button" className="mono-card-detail-type-icon mono-card-detail-editor-type-icon" style={{ color: visualStyle.icon }} onClick={() => setVisualOpen(true)} title="Card visual">{visual.icon ? <CardIcon name={visual.icon} size={18} color={visualStyle.icon} /> : detailTypeMeta.icon}</button>
          ) : (
            <span className="mono-card-detail-type-icon" style={{ color: visualStyle.icon }}>{visual.icon ? <CardIcon name={visual.icon} size={18} color={visualStyle.icon} /> : detailTypeMeta.icon}</span>
          )}
          {isEditing ? (
            <input className="mono-card-detail-title mono-card-detail-editor-title" value={meta.id || ''} onChange={e => handleMetaChange({ id: e.target.value })} placeholder={t('storyRiver.placeholder.untitled')} />
          ) : (
            <h3 className="mono-card-detail-title">{card.id}</h3>
          )}
          {(showTopActions || topRightAction) && (
            <div className="mono-card-detail-top-actions">
              {topRightAction}
              {showTopActions && (
                <>
                  {isEditing ? (
                    <>
                      {canDelete && (
                        <button className="mono-card-detail-top-btn danger" onClick={handleDelete} title="Delete"><Trash2 size={16} /></button>
                      )}
                      {originalBody != null && (
                        <button type="button" className={`mono-card-detail-top-btn${showDiff ? ' active' : ''}`} onClick={() => setShowDiff(s => !s)} title={showDiff ? t('storyRiver.editor.hideDiff') : t('storyRiver.editor.showDiff')}>
                          {showDiff ? <EyeOff size={16} /> : <Eye size={16} />}
                        </button>
                      )}
                      {singleExitEdit ? (
                        <button className="mono-card-detail-top-btn primary" onClick={handleSaveEdit} title={t('monoCardPanel.exitEdit')}><X size={16} /></button>
                      ) : (
                        <>
                          <button className="mono-card-detail-top-btn" onClick={handleCancelEdit} title="Cancel"><X size={16} /></button>
                          <button className="mono-card-detail-top-btn primary" onClick={handleSaveEdit} title="Save"><Check size={16} /></button>
                        </>
                      )}
                    </>
                  ) : (
                    <>
                      {showMenu ? (
                        <div className="mono-card-detail-menu" ref={menuRef}>
                          <button ref={buttonRef} className={`mono-card-detail-top-btn mono-card-detail-menu-btn${menuOpen ? ' open' : ''}`} onClick={() => setMenuOpen(!menuOpen)} title="Actions">
                            <ChevronDown size={16} />
                          </button>
                          {menuOpen && menuPos && createPortal(
                            <div ref={dropdownRef} className="mono-card-detail-menu-dropdown" style={{ top: menuPos.top, left: menuPos.left, position: 'fixed' }}>
                              <button onClick={() => { setMenuOpen(false); handleNewHere() }}>
                                <FilePlus size={12} /><span>New here</span>
                              </button>
                              <button onClick={() => { setMenuOpen(false); handleNewJournalHere() }}>
                                <BookOpen size={12} /><span>New journal here</span>
                              </button>
                              <button onClick={() => { setMenuOpen(false); handleClone() }}>
                                <Copy size={12} /><span>Clone</span>
                              </button>
                              <button onClick={() => { setMenuOpen(false); handleExport() }}>
                                <Download size={12} /><span>Export</span>
                              </button>
                              <div className="mono-card-detail-menu-divider" />
                              <button className={canDelete ? '' : 'disabled'} disabled={!canDelete} onClick={() => { if (canDelete) { setMenuOpen(false); handleDelete() } }}>
                                <Trash2 size={12} /><span>Delete</span>
                              </button>
                              <button onClick={() => { setMenuOpen(false); handleCloseOthers() }}>
                                <X size={12} /><span>Close others</span>
                              </button>
                              <button onClick={() => { setMenuOpen(false); handleFoldOthers() }}>
                                <PanelTop size={12} /><span>Fold others</span>
                              </button>
                              <div className="mono-card-detail-menu-divider" />
                              {cardHasContent(card) && (
                                <button onClick={() => { setMenuOpen(false); handleToggleFold() }}>
                                  {isFolded ? <ChevronDown size={12} /> : <Minimize2 size={12} />}
                                  <span>{isFolded ? 'Unfold' : 'Fold'}</span>
                                </button>
                              )}
                            </div>,
                            document.body
                          )}
                        </div>
                      ) : (
                        <button className={`mono-card-detail-top-btn danger${canDelete ? '' : ' disabled'}`} disabled={!canDelete} onClick={handleDelete} title="Delete">
                          <Trash2 size={16} />
                        </button>
                      )}
                      {onOpenCardInRightPanel && (
                        <button className="mono-card-detail-top-btn" onClick={handleOpenInRightPanel} title="Open in right panel">
                          <Maximize2 size={15} />
                        </button>
                      )}
                      <button className="mono-card-detail-top-btn" onClick={handleStartEdit} title="Edit">
                        <PencilIcon />
                      </button>
                      <button className="mono-card-detail-top-btn" onClick={handleClose} title="Close">
                        <X size={16} />
                      </button>
                    </>
                  )}
                </>
              )}
            </div>
          )}
        </div>
        <div className="mono-card-detail-subtitle">
          <span className="mono-card-detail-date">{meta.modified ? `${t('monoCard.lastModified')}: ${formatFullDate(meta.modified)}` : ''}</span>

          {isEditing && (
            <InlineSelect value={meta.type || 'wiki'} onChange={v => handleMetaChange({ type: (v as MonoCardType) || 'wiki' })} options={CARD_TYPES.map(tp => ({ value: tp, label: TYPE_META[tp].label, icon: TYPE_META[tp].icon }))}>
              <span className="mono-card-detail-tag mono-card-inline-type-chip">{detailTypeMeta.icon}{detailTypeMeta.label}<ChevronDown size={10} /></span>
            </InlineSelect>
          )}

          {isEditing ? (
            <InlineSelect value={meta.priority || ''} onChange={v => handleMetaChange({ priority: v as ReminderPriority | undefined })} allowNone options={(['low', 'medium', 'high', 'urgent'] as ReminderPriority[]).map(p => ({ value: p, label: PRIORITY_META[p].label }))}>
              <span className="mono-card-detail-priority mono-card-inline-edit-chip" style={{ color: meta.priority ? PRIORITY_META[meta.priority]?.color ?? 'var(--text-tertiary)' : 'var(--text-tertiary)' }}>
                <AlertCircle size={10} />{meta.priority ? PRIORITY_META[meta.priority]?.label ?? meta.priority : '+ Priority'}
              </span>
            </InlineSelect>
          ) : (
            meta.priority && (
              <span className="mono-card-detail-priority" style={{ color: PRIORITY_META[meta.priority]?.color ?? 'var(--text-tertiary)' }}>
                <AlertCircle size={10} />{PRIORITY_META[meta.priority]?.label ?? meta.priority}
              </span>
            )
          )}

          {isEditing ? (
            <InlineSelect value={meta.status || ''} onChange={v => handleMetaChange({ status: v || undefined })} allowNone options={Object.entries(STATUS_META).map(([s, m]) => ({ value: s, label: m.label, icon: m.icon }))}>
              <span className="mono-card-detail-status mono-card-inline-edit-chip" style={{ '--wiki-status-color': meta.status ? STATUS_META[meta.status]?.color ?? 'var(--text-tertiary)' : 'var(--text-tertiary)' } as React.CSSProperties}>
                {meta.status ? (STATUS_META[meta.status]?.icon ?? <CheckSquare size={10} />) : <Plus size={10} />}{meta.status ? STATUS_META[meta.status]?.label ?? meta.status : '+ Status'}
              </span>
            </InlineSelect>
          ) : (
            meta.status && (
              <span className="mono-card-detail-status" style={{ '--wiki-status-color': STATUS_META[meta.status]?.color ?? 'var(--text-tertiary)' } as React.CSSProperties}>
                {STATUS_META[meta.status]?.icon ?? <CheckSquare size={10} />}{STATUS_META[meta.status]?.label ?? meta.status}
              </span>
            )
          )}

          {isEditing ? (
            <InlineDueEdit value={meta.due} onChange={v => handleMetaChange({ due: v })}>
              <span className={`mono-card-detail-due mono-card-inline-edit-chip${isOverdue ? ' overdue' : ''}`}>
                <Calendar size={10} />{meta.due ? formatFullDate(meta.due) : '+ Due'}
              </span>
            </InlineDueEdit>
          ) : (
            meta.due && (
              <span className={`mono-card-detail-due${isOverdue ? ' overdue' : ''}`}>
                <Calendar size={10} />{formatFullDate(meta.due)}
              </span>
            )
          )}

          {isEditing && (
            <button className={`mono-card-detail-tag mono-card-inline-standalone${meta.standalone ? ' active' : ''}`} onClick={() => handleMetaChange({ standalone: !meta.standalone || undefined })} title="Standalone (template card)">
              <CheckSquare size={10} />Standalone
            </button>
          )}
        </div>
      </div>

      {(!isFolded || isEditing) && (
        <>
          {(selectedTags.length > 0 || isEditing) && (
            <div className="mono-card-detail-tags-row">
              {isEditing ? (
                <>
                  {selectedTags.map(tag => (
                    <span key={tag} className={`mono-card-detail-editor-tag${availableTags && !availableTags.includes(tag) ? ' draft' : ''}${isBuiltinTag(tag) ? ' mono-card-detail-editor-tag--builtin' : ''}`}>
                      {isBuiltinTag(tag) ? getBuiltinTagDisplayName(tag, t) : tag}
                      <button onClick={() => removeTag(tag)}>×</button>
                    </span>
                  ))}
                  <TagSearchInput availableTags={availableTags || []} selectedTags={selectedTags} onAdd={addTag} />
                </>
              ) : (
                meta.tags.map(tag => (
                  <button key={tag} className={`mono-card-detail-tag${isBuiltinTag(tag) ? ' mono-card-detail-tag--builtin' : ''}`} onClick={(e) => handleTagClick(tag, e)} onContextMenu={(e) => onTagContextMenu?.(card.id, tag, e)} title={isBuiltinTag(tag) ? getBuiltinTagDisplayName(tag, t) : `Open "${tag}"`}>
                    <TagIcon size={10} />
                    {isBuiltinTag(tag) ? getBuiltinTagDisplayName(tag, t) : tag}
                  </button>
                ))
              )}
            </div>
          )}

          {isEditing ? (
            <div className="mono-card-detail-editor-body">
              <RichCardEditor value={displayBody} onChange={handleBodyChange} placeholder={t('storyRiver.placeholder.writeSomething')} onWikiWordClick={(word) => onWikiWord(card.id, word)} showCursorPosition cursorPositionFormatter={(pos) => t('storyRiver.editor.position', { line: pos.line, column: pos.column })} />
            </div>
          ) : (
            <div className="mono-card-detail-body markdown-content"><CardBody body={card.body} cardId={card.id} onWikiWordClick={onWikiWord} /></div>
          )}

          {isEditing && showDiff && originalBody != null && (
            <div className="mono-card-detail-editor-diff">
              <div className="mono-card-detail-editor-diff-label">{t('storyRiver.editor.changes')}</div>
              <DiffBlock diffContent={computeUnifiedDiff(originalBody, displayBody, `${card.id}.md`)} filePath={`${card.id}.md`} />
            </div>
          )}

          <BuiltinCardDataPanel type={meta.type} tags={meta.tags || []} data={meta.data || {}} body={isEditing ? displayBody : card.body} cardId={card.id} editing={isEditing || undefined} onChange={isEditing ? (data => handleMetaChange({ data })) : undefined} onChangeBody={isEditing ? handleBodyChange : undefined} onTrigger={isEditing ? undefined : onTriggerTimerCard} onNavigateToMap={isEditing ? undefined : onNavigateToMap} onUpdateScheduleCron={isEditing ? undefined : onUpdateScheduleCron} />

          {isEditing ? (
            <CustomDataEditor data={meta.data || {}} onMetaChange={handleMetaChange} />
          ) : (
            userDataEntries(card.data).length > 0 && (
              <div className="mono-card-detail-data">
                {userDataEntries(card.data).map(([name, value]) => (
                  <div key={name} className="mono-card-detail-data-row">
                    <span className="mono-card-detail-data-name">{name}</span>
                    <span className="mono-card-detail-data-value">
                      <DataValueDisplay value={value} allCards={allCards || []} onOpenCard={onOpenCard} />
                    </span>
                  </div>
                ))}
              </div>
            )
          )}

          {isEditing && !meta.standalone && childCards.length > 0 && (
            <div className="mono-card-detail-editor-children">
              <div className="mono-card-detail-editor-children-label">Child cards order</div>
              {childCards.map((child, idx) => (
                <div key={child.id} className="mono-card-detail-editor-child-row">
                  <span className="mono-card-detail-editor-child-index">{idx + 1}</span>
                  <span className="mono-card-detail-editor-child-title">
                    {isBuiltinTag(child.id) ? getBuiltinTagDisplayName(child.id, t) : child.id}
                  </span>
                  <button type="button" className="mono-card-detail-editor-child-btn" onClick={() => moveChild(child.id, -1)} disabled={idx === 0} title="Move up"><ChevronUp size={12} /></button>
                  <button type="button" className="mono-card-detail-editor-child-btn" onClick={() => moveChild(child.id, 1)} disabled={idx === childCards.length - 1} title="Move down"><ChevronDown size={12} /></button>
                  <button type="button" className="mono-card-detail-editor-child-btn" onClick={() => removeChild(child.id)} title="Remove from order"><X size={12} /></button>
                </div>
              ))}
            </div>
          )}

          {!isEditing && showChildren && (
            <div className="mono-card-detail-children">
              <div className="mono-card-detail-children-label">{t('storyRiver.children.label')}</div>
              {childCards.length === 0 ? (
                <div className="mono-card-detail-children-empty">{t('storyRiver.children.empty')}</div>
              ) : (
                childCards.map(child => (
                  <a key={child.id} className="mono-card-detail-child" onClick={() => onOpenCard(child.id)}>
                    {isBuiltinTag(child.id) ? getBuiltinTagDisplayName(child.id, t) : child.id}
                  </a>
                ))
              )}
            </div>
          )}

          {meta.parent ? (
            <div className="mono-card-detail-parent">
              <ChevronRight size={11} />
              {isEditing ? (
                <CardReferenceInput value={meta.parent || ''} onChange={id => handleMetaChange({ parent: id || undefined })} />
              ) : (
                <a onClick={() => onOpenCard(meta.parent!)}>{meta.parent}</a>
              )}
            </div>
          ) : isEditing && (
            <div className="mono-card-detail-parent">
              <ChevronRight size={11} />
              <CardReferenceInput value="" onChange={id => handleMetaChange({ parent: id || undefined })} />
            </div>
          )}
        </>
      )}

      {!isEditing && isFolded && cardHasContent(card) && (
        <button className="mono-card-detail-fold-indicator" onClick={handleToggleFold} title="Unfold">
          <ChevronDown size={16} />
        </button>
      )}

      {isEditing && visualOpen && (
        <CardVisualDialog
          card={{ id: card.id, data: meta.data || {} } as MonoCardListItem}
          onClose={() => setVisualOpen(false)}
          onSave={(data) => {
            handleMetaChange({ data: { ...(meta.data ?? {}), visual: data.visual } })
            setVisualOpen(false)
          }}
        />
      )}
    </article>
  )
})

const PencilIcon = () => (
  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M12 20h9" /><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4Z" />
  </svg>
)

function formatFullDate(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  try {
    return d.toLocaleString(undefined, {
      year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'
    })
  } catch {
    return iso
  }
}

function formatDataDisplay(value: unknown): string {
  if (value === null) return 'null'
  if (value === undefined) return ''
  if (Array.isArray(value)) return value.map(formatDataDisplay).join(', ')
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

const DataValueDisplay: React.FC<{
  value: unknown
  allCards: MonoCardListItem[]
  onOpenCard: (id: string) => void
}> = ({ value, allCards, onOpenCard }) => {
  const id = cardReferenceId(value)
  if (id !== null) {
    const target = allCards.find(c => c.id === id)
    return (
      <button className="mono-card-detail-data-card-ref" onClick={() => onOpenCard(id)} title={id}>
        <MessageCircle size={10} />
        <span>{target?.id || id}</span>
      </button>
    )
  }
  return <>{formatDataDisplay(value)}</>
}

const CustomDataEditor: React.FC<{
  data: Record<string, unknown>
  onMetaChange: (meta: Partial<MonoCardModel>) => void
}> = ({ data, onMetaChange }) => {
  const entries = Object.entries(data)

  const rebuild = (updater: (list: Array<[string, unknown]>) => Array<[string, unknown]>) => {
    const next: Record<string, unknown> = {}
    for (const [k, v] of updater(entries.map(e => [e[0], e[1]] as [string, unknown]))) {
      next[k] = v
    }
    onMetaChange({ data: next })
  }

  const update = (idx: number, fn: (entry: [string, unknown]) => [string, unknown]) => {
    rebuild(list => {
      list[idx] = fn(list[idx]!)
      return list
    })
  }

  const changeName = (idx: number, newName: string) => {
    update(idx, ([, v]) => [newName, v])
  }

  const changeType = (idx: number, typeId: string) => {
    const def = dataFieldTypeDefById(typeId)
    update(idx, ([k, v]) => [k, def.coerce(v)])
  }

  const changeValueText = (idx: number, text: string) => {
    update(idx, ([k, v]) => {
      const def = dataFieldTypeDefOf(v)
      return [k, def.inputKind === 'number' ? def.coerce(text) : text]
    })
  }

  const changeValueBool = (idx: number, val: boolean) => {
    update(idx, ([k]) => [k, val])
  }

  const removeEntry = (idx: number) => {
    rebuild(list => list.filter((_, i) => i !== idx))
  }

  const addEntry = () => {
    let key = 'field'
    let n = 1
    while (data[key] !== undefined) key = `field-${++n}`
    onMetaChange({ data: { ...data, [key]: DATA_FIELD_TEXT.defaultValue } })
  }

  return (
    <div className="mono-card-detail-editor-data">
      <div className="mono-card-detail-editor-data-label">Custom data</div>
      {entries.map(([name, value], idx) => {
        // Reserved keys (e.g. `visual`) are system metadata, not editable
        // rows. Skip rendering but keep their index so rebuild preserves them.
        if (RESERVED_DATA_KEYS.has(name)) return null
        const def = dataFieldTypeDefOf(value)
        return (
          <div key={idx} className="mono-card-detail-editor-data-row">
            <input
              className="mono-card-detail-editor-data-name"
              value={name}
              onChange={e => changeName(idx, e.target.value)}
            />
            <select
              className="mono-card-detail-editor-data-type"
              value={def.id}
              onChange={e => changeType(idx, e.target.value)}
            >
              {DATA_FIELD_TYPES.map(t => (
                <option key={t.id} value={t.id}>{t.label}</option>
              ))}
            </select>
            {def.inputKind === 'card' ? (
              <CardReferenceInput
                value={String(value)}
                onChange={id => update(idx, ([k]) => [k, makeCardReference(id)])}
              />
            ) : def.inputKind === 'boolean' ? (
              <select
                className="mono-card-detail-editor-data-value"
                value={value ? 'true' : 'false'}
                onChange={e => changeValueBool(idx, e.target.value === 'true')}
              >
                <option value="true">true</option>
                <option value="false">false</option>
              </select>
            ) : (
              <input
                className="mono-card-detail-editor-data-value"
                type={def.inputKind === 'number' ? 'number' : 'text'}
                value={formatDataDisplay(value)}
                onChange={e => changeValueText(idx, e.target.value)}
              />
            )}
            <button
              type="button"
              className="mono-card-detail-editor-data-remove"
              onClick={() => removeEntry(idx)}
              title="Remove"
            >
              <X size={12} />
            </button>
          </div>
        )
      })}
      <button type="button" className="mono-card-detail-editor-data-add" onClick={addEntry}>
        <Plus size={12} />
        <span>Add data</span>
      </button>
    </div>
  )
}


