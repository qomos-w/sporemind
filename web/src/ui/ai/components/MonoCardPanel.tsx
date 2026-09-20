import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { CloudUpload, PanelLeft, MessageCircle, Code, X, Check, Target } from 'lucide-react'
import { monoStore } from '../../panels/mono-store'
import { useMonoStore } from '../hooks/useMonoStore'
import { MonoCardDetail, DraftCardEditor } from './MonoCard'
import { isBuiltinCard, BUILTIN_TAG_CANONICALS, overrideBuiltinTitle } from '../../../domain/builtin-cards'
import { useI18n } from '../../../i18n'
import type { MonoCard, MonoCardListItem } from '../../../domain/mono-types'
import { normalizeMonoCardType } from '../../../domain/mono-types'
import { normalizeTaskStatus } from '../../../domain/task-status'
import { cardVisual, getCardVisualStyle } from './cardVisual'
import { workflowTaskIds } from './workflowLayout'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { CodeMirrorMarkdownEditor } from '../../editor/CodeMirrorMarkdownEditor'
import { parseMonoCard, repairMonoCardRaw, validateMonoCard } from '../../../domain/mono-types'
import type { CardValidationError } from '../../../gen-clients/system/types'
import './MonoCardPanel.css'

/** Auto-save debounce: a save fires at most this long after the last edit. */
const AUTO_SAVE_DEBOUNCE_MS = 1000
/** DOM elements a double-click must never treat as "blank space" (each is an
 *  interactive or content-bearing region where dblclick means select/copy or
 *  is handled by a child control). */
const DBLCLICK_IGNORE_SELECTOR = [
  'a',
  'button',
  'input',
  'select',
  'textarea',
  'option',
  'label',
  'code',
  'pre',
  'img',
  'svg',
  'iframe',
  'video',
  'audio',
  'canvas',
  'table',
  'mark',
  '.ai-file-ref',
  '.wiki-word-link',
  '.katex',
  '.mermaid',
].join(',')

interface MonoCardPanelProps {
  cardId: string
  projectId?: string | null
  onOpenInNotes?: (cardId: string, projectId?: string | null) => void
  onCloseTab?: (cardId: string) => void
  onChat?: (cardId: string) => void
  onWikiWord?: (word: string) => void
  initialEditMode?: boolean
  /** Assign this task card's goal to a new or existing agent. */
  onAssignGoal?: (card: MonoCardListItem) => void
  /** Zoom-to-fit a workflow map id (scheduler current_instance navigation). */
  onNavigateToMap?: (mapId: string) => void
}

const noopOne = (_id: string) => {}
const noopTag = (_id: string, _tag: string, _e: React.MouseEvent) => {}

export const MonoCardPanel: React.FC<MonoCardPanelProps> = ({ cardId, projectId, onOpenInNotes, onCloseTab, onChat, onWikiWord, initialEditMode, onAssignGoal, onNavigateToMap }) => {
  const { t } = useI18n()
  const state = useMonoStore()
  const [card, setCard] = useState<MonoCard | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [isEditing, setIsEditing] = useState(Boolean(initialEditMode))
  const [editMeta, setEditMeta] = useState<Partial<MonoCard>>({})
  const [editBody, setEditBody] = useState('')
  const [deleted, setDeleted] = useState(false)
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false)
  const [sourceMode, setSourceMode] = useState(false)
  const [sourceRaw, setSourceRaw] = useState('')
  const [sourceError, setSourceError] = useState<string | null>(null)
  const [sourceSaving, setSourceSaving] = useState(false)
  const [backendErrors, setBackendErrors] = useState<CardValidationError[]>([])
  const [backendValidating, setBackendValidating] = useState(false)
  /** True while an auto-save RPC is in flight (drives the saving indicator). */
  const [autosaveSaving, setAutosaveSaving] = useState(false)
  /** True when the edit buffer differs from the last persisted content. */
  const [autosaveDirty, setAutosaveDirty] = useState(false)
  /** Error surfaced by the last auto-save attempt. */
  const [autosaveError, setAutosaveError] = useState<string | null>(null)

  // The active edit session: which card it edits, on which project, and how it
  // started. Held in refs (not read from props) so a flush triggered by unmount
  // or a card switch still lands on the card that was being edited, even though
  // the props already point at the next card.
  const editSessionRef = useRef<{
    cardId: string
    projectOpts: { projectId: string } | undefined
    readonly: boolean
  } | null>(null)
  // Snapshot taken when edit mode starts; cancel restores the card to it.
  const editSnapshotRef = useRef<MonoCard | null>(null)
  // Mirrors of the edit buffers for the stable auto-save timer/flush callbacks.
  const editMetaRef = useRef<Partial<MonoCard>>({})
  const editBodyRef = useRef('')
  const autosaveDirtyRef = useRef(false)
  const autosaveErrorRef = useRef<string | null>(null)
  // Incremented on every edit; lets an in-flight save know whether newer edits
  // landed while it was writing (see persistEdit).
  const dirtyEpochRef = useRef(0)
  // Stable entry point for the debounced save; always points at the latest
  // persist closure so the timer never closes over stale content.
  const persistRef = useRef<() => Promise<void>>(async () => {})
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Monotonic guard against out-of-order auto-saves racing each other: a save
  // that started later must win, an older save must never land after it.
  const saveSeqRef = useRef(0)
  // Form-state mirrors read inside async callbacks (which must not close over
  // stale render values).
  const isEditingRef = useRef(false)
  // Tracks whether the current card has been loaded yet (first load vs.
  // card_changed refreshes). The initial fetch may show a loading state; the
  // later in-place refreshes never may (no spinner, no scroll jump, no form
  // change — requirement 6/7).
  const initialLoadDoneRef = useRef(false)

  const prevCardIdRef = useRef(cardId)
  const prevEditModeRef = useRef(initialEditMode)

  // Keep refs that back the stable auto-save/flush callbacks in sync with the
  // render-state edit buffers.
  isEditingRef.current = isEditing
  editMetaRef.current = editMeta
  editBodyRef.current = editBody
  autosaveDirtyRef.current = autosaveDirty
  autosaveErrorRef.current = autosaveError

  // Ensure the global card store is initialized for the current project.
  // A card tab opened from another project (projectId ≠ the store's project)
  // must never hijack the singleton store — its reads/writes go through
  // explicit-project store calls instead.
  useEffect(() => {
    if (!projectId) return
    if (state.projectId && state.projectId !== projectId) return
    if (state.projectId !== projectId) {
      monoStore.setProjectId(projectId)
      monoStore.load()
    }
  }, [projectId, state.projectId])

  // Reset edit state only when the card really changes, and enter edit mode when
  // the tab payload requests it (even for an already-open card).
  useEffect(() => {
    const cardChanged = prevCardIdRef.current !== cardId
    const editModeChanged = prevEditModeRef.current !== initialEditMode
    prevCardIdRef.current = cardId
    prevEditModeRef.current = initialEditMode

    if (cardChanged) {
      // A real tab/card switch: flush any pending auto-save of the previous
      // card (editSessionRef still points at it), then reset all per-card state
      // for the new one.
      flushForCardSwitch()
      initialLoadDoneRef.current = false
      editSessionRef.current = null
      editSnapshotRef.current = null
      autosaveDirtyRef.current = false
      setIsEditing(Boolean(initialEditMode))
      setEditMeta({})
      setEditBody('')
      setAutosaveError(null)
      setAutosaveDirty(false)
      setDeleted(false)
      setSourceMode(false)
      setSourceRaw('')
      setSourceError(null)
      setSourceSaving(false)
    } else if (initialEditMode && editModeChanged) {
      setIsEditing(true)
    }
  }, [cardId, initialEditMode])

  // The card's project may differ from the singleton store's project (a
  // right-panel tab kept open across a project switch): foreign cards bypass
  // the store gate and fetch with an explicit target.
  const foreignProject = !!projectId && state.projectId !== projectId

  // Load the card. The first load for a card may show a loading state; every
  // later run is an in-place refresh driven by card_changed echoes (own
  // auto-saves, agent/other editors) and must preserve the current UI form:
  // never flip the spinner (which would remount the article and reset scroll
  // — requirement 7), never kick the user out of an active edit or source
  // mode (requirement 6), and never overwrite the live edit buffers
  // (requirement 3). The store still gets the fresh snapshot via its own
  // watcher — only this panel's buffers and form are protected.
  useEffect(() => {
    const thisCardId = cardId
    const thisProjectOpts = foreignProject ? { projectId: projectId! } : undefined
    let cancelled = false

    const run = async () => {
      if (!initialLoadDoneRef.current) {
        // First load for this card.
        setLoading(true)
        setError(null)
      }
      try {
        const loaded = await monoStore.getCard(thisCardId, thisProjectOpts)
        if (cancelled) return
        if (!loaded) {
          setError('Card not found')
          setCard(null)
          return
        }
        const titled = overrideBuiltinTitle(loaded, t)
        if (!initialLoadDoneRef.current) {
          // First load for this card: populate the view card and, when the
          // tab payload asked for edit mode, seed the buffers.
          initialLoadDoneRef.current = true
          setCard(titled)
          if (isEditingRef.current) {
            editSessionRef.current = {
              cardId: titled.id,
              projectOpts: thisProjectOpts,
              readonly: titled.protected === true || titled.editable === false,
            }
            editSnapshotRef.current = titled
            editMetaRef.current = { ...titled }
            editBodyRef.current = titled.body
            setEditMeta({ ...titled })
            setEditBody(titled.body)
          }
        } else {
          // In-place refresh (view, edit, or source mode): content refresh
          // only — never changes isEditing/sourceMode/fold state, never
          // remounts (no spinner), and never overwrites the live edit buffers
          // or sourceRaw. The editor keeps rendering its own buffer; the store
          // already holds the freshest content for the other views.
          setCard(titled)
          // Edge: editing was requested (initialEditMode) before the first
          // load had seeded the buffers (store not ready at mount). Seed now
          // that we have content — only when the user has not typed yet.
          if (isEditingRef.current && editMetaRef.current && Object.keys(editMetaRef.current).length === 0 && editBodyRef.current === '') {
            editSessionRef.current = {
              cardId: titled.id,
              projectOpts: thisProjectOpts,
              readonly: titled.protected === true || titled.editable === false,
            }
            editSnapshotRef.current = titled
            editMetaRef.current = { ...titled }
            editBodyRef.current = titled.body
            setEditMeta({ ...titled })
            setEditBody(titled.body)
          }
        }
      } catch (err) {
        if (cancelled) return
        setError(err instanceof Error ? err.message : String(err))
      } finally {
        if (!cancelled) setLoading(false)
      }
    }

    if (!foreignProject && (!projectId || state.projectId !== projectId || state.loading)) {
      // Store not ready yet; the effect re-runs when state.loading flips.
      setLoading(true)
      return
    }
    void run()
    return () => { cancelled = true }
  }, [cardId, projectId, foreignProject, state.projectId, state.loading, state.cardRefreshKey[cardId], t])

  /** Route store mutations to the card's own project when it differs from the
   *  singleton store's project. */
  const projectOpts = useMemo(
    () => (foreignProject && projectId ? { projectId } : undefined),
    [foreignProject, projectId],
  )

  const allCards: MonoCardListItem[] = useMemo(() => state.cards, [state.cards])

  // Read-only repair mode: cards that are already invalid on disk (bad type,
  // forbidden tags, parent∉tags) are opened in source-edit mode so the user
  // can fix the raw markdown. Mirrors the backend validateCard rules.
  const validationErrors = useMemo(() => (card ? validateMonoCard(card) : []), [card])
  const repairMode = validationErrors.length > 0

  useEffect(() => {
    if (repairMode && card && !sourceMode) {
      setSourceMode(true)
      setSourceRaw(card.raw)
      setSourceError(null)
    }
  }, [repairMode, card, sourceMode])

  // Authoritative backend validation for repair mode and live source edits.
  useEffect(() => {
    let cancelled = false
    if (!card) {
      setBackendErrors([])
      return
    }
    const rawToValidate = sourceMode ? sourceRaw : card.raw
    if (!rawToValidate) {
      setBackendErrors([])
      return
    }
    setBackendValidating(true)
    monoStore.validateCard(card.id, rawToValidate, projectOpts).then((resp) => {
      if (cancelled) return
      setBackendErrors(resp.Errors)
      setBackendValidating(false)
    }).catch(() => {
      if (cancelled) return
      setBackendErrors([])
      setBackendValidating(false)
    })
    return () => { cancelled = true }
  }, [card, sourceMode, sourceRaw, projectOpts])

  const availableTags = useMemo(() => {
    const set = new Set<string>(BUILTIN_TAG_CANONICALS)
    for (const c of state.cards) {
      for (const tag of c.tags) set.add(tag)
    }
    return Array.from(set)
  }, [state.cards])

  const handleStartEdit = useCallback(() => {
    if (!card) return
    if (card.protected === true || card.editable === false) {
      // Read-only cards are never edited in place; source/repair is the path.
      return
    }
    editSessionRef.current = {
      cardId: card.id,
      projectOpts,
      readonly: false,
    }
    editSnapshotRef.current = card
    editMetaRef.current = { ...card }
    editBodyRef.current = card.body
    autosaveDirtyRef.current = false
    setEditMeta({ ...card })
    setEditBody(card.body)
    setAutosaveError(null)
    setAutosaveDirty(false)
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }
    setIsEditing(true)
  }, [card, projectOpts])

  /** Double-click on the card's rendered content while in view mode enters
   *  edit mode — but only when the double-click lands on "blank space".
   *  Interactive/content elements (links, buttons, code/pre, media, tables,
   *  wiki words) and text selections must never trigger it. The handler lives
   *  on the panel wrapper and receives the event bubbled from the card detail. */
  const handleDetailDoubleClick = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    if (!card || isEditing || sourceMode) return
    const target = e.target as HTMLElement
    if (target.closest(DBLCLICK_IGNORE_SELECTOR)) return
    const sel = window.getSelection()
    if (sel && sel.toString().trim().length > 0) return
    e.preventDefault()
    handleStartEdit()
  }, [card, isEditing, sourceMode, handleStartEdit])

  /** Persist the current edit buffer (body + meta) through the existing
   *  wiki_edit_card pipe. Guards: monotonic seq (out-of-order saves lose),
   *  dirty epoch (an edit landing mid-save keeps dirty true so a follow-up
   *  save runs), and read-only cards are never written. The title is never
   *  renamed here: the id is the on-disk identity and there is no rename
   *  protocol, so auto-saves force the original id and persist only the
   *  non-title fields; handleSaveEdit applies an edited title at confirm. */
  const persistEdit = useCallback(async (): Promise<void> => {
    const session = editSessionRef.current
    if (!session) return
    if (session.readonly) {
      if (autosaveErrorRef.current == null) setAutosaveError(t('monoCardPanel.readonlyHint'))
      return
    }
    const seq = ++saveSeqRef.current
    const meta = editMetaRef.current
    const body = editBodyRef.current
    const id = session.cardId
    const dirtyEpoch = dirtyEpochRef.current
    setAutosaveSaving(true)
    setAutosaveError(null)
    try {
      // Preserve the on-disk identity on every auto-save (edits to the title
      // input are applied once at confirm time, mirroring the old Save path).
      await monoStore.updateCard(id, { ...meta, id, body }, session.projectOpts)
      if (seq === saveSeqRef.current && dirtyEpoch === dirtyEpochRef.current) {
        autosaveDirtyRef.current = false
        setAutosaveDirty(false)
      }
    } catch (err) {
      if (seq === saveSeqRef.current) {
        const msg = err instanceof Error ? err.message : String(err)
        if (autosaveErrorRef.current == null) {
          setAutosaveError(msg)
        }
      }
    } finally {
      if (seq === saveSeqRef.current) {
        setAutosaveSaving(false)
      }
    }
  }, [t])

  persistRef.current = persistEdit

  const scheduleAutoSave = useCallback(() => {
    dirtyEpochRef.current++
    setAutosaveError(null)
    setAutosaveDirty(true)
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current)
    saveTimerRef.current = setTimeout(() => {
      saveTimerRef.current = null
      void persistRef.current()
    }, AUTO_SAVE_DEBOUNCE_MS)
  }, [])

  /** Leave edit mode, persisting any pending changes first. Called by the
   *  confirm button and externally (tab close / card switch / source toggle). */
  const leaveEditWithFlush = useCallback(async () => {
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }
    ++saveSeqRef.current
    const session = editSessionRef.current
    const meta = editMetaRef.current
    const body = editBodyRef.current
    const hasDirty = autosaveDirtyRef.current
    if (session && hasDirty && !autosaveErrorRef.current && !session.readonly) {
      const id = session.cardId
      // At confirm/exit time the edited title is applied — the old manual Save
      // semantics were that meta.id becomes the frontmatter id. For builtin
      // cards the id is fixed.
      const metaToSave = isBuiltinCard(id)
        ? { ...meta, id }
        : { ...meta, id: (meta.id as string) || id }
      try {
        await monoStore.updateCard(id, { ...metaToSave, body }, session.projectOpts)
      } catch {
        // Fall through and exit edit mode anyway; the next open re-reads the
        // persisted content.
      }
    }
    autosaveDirtyRef.current = false
    editSessionRef.current = null
    editSnapshotRef.current = null
    setIsEditing(false)
    setEditMeta({})
    setEditBody('')
    setAutosaveError(null)
    setAutosaveDirty(false)
    const updated = await monoStore.getCard(cardId, projectOpts)
    if (updated) {
      setCard(overrideBuiltinTitle(updated, t))
    }
  }, [cardId, projectOpts, t])

  /** Leave edit mode without persisting; restore the enter-edit snapshot once
   *  (only when the user actually changed something — a pristine cancel stays
   *  local and does not touch the store). */
  const leaveEditWithRestore = useCallback(async () => {
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }
    ++saveSeqRef.current
    const session = editSessionRef.current
    const snapshot = editSnapshotRef.current
    const meta = editMetaRef.current
    const body = editBodyRef.current
    const pristine = !autosaveDirtyRef.current
      && (!snapshot || (body === snapshot.body
        && Object.keys(meta).every(k => (meta as Record<string, unknown>)[k] === (snapshot as unknown as Record<string, unknown>)[k])))
    autosaveDirtyRef.current = false
    editSessionRef.current = null
    editSnapshotRef.current = null
    setIsEditing(false)
    setEditMeta({})
    setEditBody('')
    setAutosaveError(null)
    setAutosaveDirty(false)
    if (session && snapshot && !pristine) {
      // Restore the card to its enter-edit state in the store, then reload the
      // view card. Fires-and-forgets the write.
      void monoStore.updateCard(session.cardId, { ...snapshot, body: snapshot.body }, session.projectOpts)
        .then(() => monoStore.getCard(session.cardId, session.projectOpts))
        .then((updated) => {
          if (updated) setCard(overrideBuiltinTitle(updated, t))
        })
        .catch(() => {})
    }
  }, [t])

  // Flush pending auto-save work when the panel unmounts (tab close / switch).
  // Uses the edit-session refs, so the write still lands on the card that was
  // being edited even though the props may already point elsewhere.
  useEffect(() => {
    return () => {
      if (saveTimerRef.current) {
        clearTimeout(saveTimerRef.current)
        saveTimerRef.current = null
      }
      if (autosaveDirtyRef.current) {
        void persistRef.current()
      }
    }
  }, [])

  const flushForCardSwitch = useCallback(() => {
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }
    if (autosaveDirtyRef.current) {
      void persistRef.current()
    }
  }, [])

  const handleCancelEdit = useCallback(() => {
    void leaveEditWithRestore()
  }, [leaveEditWithRestore])

  const handleSaveEdit = useCallback(() => {
    void leaveEditWithFlush()
  }, [leaveEditWithFlush])

  // Esc while editing exits edit mode through the flush path (pending
  // auto-save work is persisted first). Events already handled inside an
  // editor (e.g. CodeMirror's find/replace Escape) are left alone.
  useEffect(() => {
    if (!isEditing) return
    const onKeydown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return
      e.preventDefault()
      e.stopPropagation()
      void leaveEditWithFlush()
    }
    document.addEventListener('keydown', onKeydown)
    return () => document.removeEventListener('keydown', onKeydown)
  }, [isEditing, leaveEditWithFlush])

  const handleDelete = useCallback(() => {
    if (isBuiltinCard(cardId)) return
    setDeleteConfirmOpen(true)
  }, [cardId])

  // Deleting a workflow card deletes the whole workflow: map card + scoped tasks.
  // Foreign-project maps scope to their own include list only — the singleton
  // card list belongs to another project.
  const deleteTaskIds = useMemo(
    () => (card && card.type === 'workflow' ? workflowTaskIds(card, foreignProject ? [] : state.cards) : []),
    [card, state.cards, foreignProject],
  )

  const handleConfirmDelete = useCallback(async () => {
    setDeleteConfirmOpen(false)
    try {
      for (const taskId of deleteTaskIds) {
        await monoStore.deleteCard(taskId, projectOpts)
      }
      await monoStore.deleteCard(cardId, projectOpts)
      await monoStore.closeCard(cardId, projectOpts)
      setDeleted(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }, [cardId, deleteTaskIds, projectOpts])

  const handleMetaChange = useCallback((_id: string, meta: Partial<MonoCard>) => {
    editMetaRef.current = { ...editMetaRef.current, ...meta }
    setEditMeta(prev => ({ ...prev, ...meta }))
    scheduleAutoSave()
  }, [scheduleAutoSave])

  const handleBodyChange = useCallback((_id: string, body: string) => {
    editBodyRef.current = body
    setEditBody(body)
    scheduleAutoSave()
  }, [scheduleAutoSave])

  const handleToggleSource = useCallback(() => {
    if (!card) return
    if (sourceMode) {
      // Leaving source mode: clear source-only state.
      setSourceMode(false)
      setSourceRaw('')
      setSourceError(null)
    } else {
      // Entering source mode while an edit is open: flush unsaved edits so the
      // raw view reflects them, then leave edit mode.
      if (autosaveDirtyRef.current) {
        void persistRef.current()
      }
      if (saveTimerRef.current) {
        clearTimeout(saveTimerRef.current)
        saveTimerRef.current = null
      }
      editSessionRef.current = null
      editSnapshotRef.current = null
      setIsEditing(false)
      setEditMeta({})
      setEditBody('')
      setAutosaveError(null)
      setAutosaveDirty(false)
      autosaveDirtyRef.current = false
      setSourceMode(true)
      setSourceRaw(repairMonoCardRaw(card.raw))
      setSourceError(null)
    }
  }, [card, sourceMode])

  const handleSourceChange = useCallback((value: string) => {
    setSourceRaw(value)
    setSourceError(null)
  }, [])

  const handleCancelSource = useCallback(() => {
    setSourceMode(false)
    setSourceRaw('')
    setSourceError(null)
  }, [])

  const handleSaveSource = useCallback(async () => {
    if (!card) return
    const parsed = parseMonoCard(cardId, sourceRaw)
    if (!parsed) {
      setSourceError(t('monoCardPanel.sourceInvalid'))
      return
    }
    setSourceSaving(true)
    try {
      await monoStore.updateCardRaw(cardId, repairMonoCardRaw(sourceRaw), projectOpts)
      setSourceMode(false)
      setSourceRaw('')
      setSourceError(null)
    } catch (err) {
      setSourceError(err instanceof Error ? err.message : String(err))
    } finally {
      setSourceSaving(false)
    }
  }, [card, cardId, sourceRaw, t, projectOpts])

  if (deleted) {
    return <div className="mono-card-panel mono-card-panel--deleted">Card deleted</div>
  }

  if (loading) {
    return <div className="mono-card-panel mono-card-panel--loading">Loading…</div>
  }
  if (error || !card) {
    return <div className="mono-card-panel mono-card-panel--error">{error ?? 'Card not found'}</div>
  }

  const panelVisual = cardVisual(card)
  const panelHasVisual = !!panelVisual.icon || !!panelVisual.accent || !!panelVisual.emphasis || !!panelVisual.background || !!panelVisual.border
  const panelStyle = panelHasVisual ? (() => {
    const vs = getCardVisualStyle(panelVisual)
    return { background: vs.background, borderColor: vs.border, borderWidth: vs.borderWidth }
  })() : undefined

  const sourceToolbar = (
    <>
      <button
        className={`mono-card-detail-top-btn${sourceMode ? ' primary' : ''}`}
        onClick={handleToggleSource}
        title={t('monoCardPanel.source')}
      >
        <Code size={15} />
      </button>
      {onChat ? (
        <button
          className="mono-card-detail-top-btn"
          onClick={() => onChat(cardId)}
          title={t('topologyCardContextMenu.chat')}
        >
          <MessageCircle size={15} />
        </button>
      ) : null}
      {onAssignGoal && normalizeMonoCardType(card.type) === 'task' && normalizeTaskStatus(card.status ?? '') !== 'done' ? (
        <button
          className="mono-card-detail-top-btn"
          onClick={() => onAssignGoal(card)}
          title={t('topologyCardContextMenu.assignGoal')}
        >
          <Target size={15} />
        </button>
      ) : null}
      {onOpenInNotes ? (
        <button
          className="mono-card-detail-top-btn"
          onClick={() => onOpenInNotes(cardId, projectId)}
          title="Open in notes"
        >
          <PanelLeft size={15} />
        </button>
      ) : null}
    </>
  )

  return (
    <div className="mono-card-panel" data-card-id={card.id} style={panelStyle} onDoubleClick={handleDetailDoubleClick}>
      {!sourceMode && !isEditing ? (
        <div className="mono-card-dblclick-hint" aria-hidden="true">
          {t('monoCardPanel.doubleClickToEdit')}
        </div>
      ) : null}
      {isEditing ? (
        <div className="mono-card-autosave-status" role="status" aria-live="polite">
          {autosaveError ? (
            <span className="mono-card-autosave-error" title={autosaveError}>{t('monoCardPanel.autosaveFailed')}</span>
          ) : autosaveSaving ? (
            <><CloudUpload size={12} /><span>{t('fileDiff.saving')}</span></>
          ) : autosaveDirty ? (
            <span className="mono-card-autosave-dirty">{t('monoCardPanel.autosavePending')}</span>
          ) : null}
        </div>
      ) : null}
      {sourceMode ? (
        <div className="mono-card-source">
          <div className="mono-card-source-header">
            <h3 className="mono-card-source-title">{t('monoCardPanel.source')}: {card.id}</h3>
            <div className="mono-card-detail-top-actions">{sourceToolbar}</div>
          </div>
          {repairMode ? (
            <div className="mono-card-repair-banner">
              <div className="mono-card-repair-banner-title">{t('monoCardPanel.repairMode')}</div>
              <ul className="mono-card-repair-banner-errors">
                {backendErrors.length > 0
                  ? backendErrors.map((e, i) => (
                      <li key={i}>
                        <code className="mono-card-repair-banner-code">{e.Code}</code>
                        {e.Field && e.Field !== '-' ? <span className="mono-card-repair-banner-field">({e.Field})</span> : null}
                        {' '}{e.Message}
                      </li>
                    ))
                  : validationErrors.map((e, i) => <li key={i}>{e}</li>)}
                {backendValidating ? <li>{t('monoCardPanel.repairValidating')}</li> : null}
              </ul>
              <div className="mono-card-repair-banner-hint">{t('monoCardPanel.repairHint')}</div>
            </div>
          ) : null}
          <div className="mono-card-source-actions">
            <button
              className="mono-card-source-btn primary"
              onClick={handleSaveSource}
              disabled={sourceSaving}
            >
              <Check size={14} />
              <span>{t('common.save')}</span>
            </button>
            <button
              className="mono-card-source-btn"
              onClick={handleCancelSource}
              disabled={sourceSaving}
            >
              <X size={14} />
              <span>{t('common.cancel')}</span>
            </button>
          </div>
          {sourceError ? (
            <div className="mono-card-source-error">{sourceError}</div>
          ) : null}
          <div className="mono-card-source-body">
            <CodeMirrorMarkdownEditor value={sourceRaw} onChange={handleSourceChange} />
          </div>
        </div>
      ) : (
        <MonoCardDetail
          card={card}
          isEditing={isEditing}
          editMeta={editMeta}
          editBody={editBody}
          isFolded={false}
          isEntering={false}
          isRemoving={false}
          isPendingDelete={deleteConfirmOpen}
          borderless
          showTopActions
          showMenu={false}
          singleExitEdit
          topRightAction={sourceToolbar}
          onMetaChange={handleMetaChange}
          onBodyChange={handleBodyChange}
          onStartEdit={handleStartEdit}
          onSaveEdit={handleSaveEdit}
          onCancelEdit={handleCancelEdit}
          onDelete={handleDelete}
          onClose={onCloseTab ?? noopOne}
          onToggleFold={noopOne}
          onFoldOthers={noopOne}
          onCloseOthers={noopOne}
          onClone={noopOne}
          onNewHere={noopOne}
          onNewJournalHere={noopOne}
          onExport={noopOne}
          onTriggerTimerCard={async (id) => { await monoStore.triggerTimerCard(id) }}
          onNavigateToMap={onNavigateToMap}
          onUpdateScheduleCron={async (cardId, cron) => {
            const card = await monoStore.getCard(cardId)
            if (!card) return false
            const schedule = { ...(card.data?.schedule as Record<string, unknown> | undefined ?? {}) }
            schedule.cron = cron
            delete schedule.expression
            const data = { ...(card.data ?? {}), schedule }
            const updated = await monoStore.updateCard(cardId, { data })
            return !!updated
          }}
          onOpenCard={(id) => onWikiWord?.(id)}
          onWikiWord={(_id, word) => onWikiWord?.(word)}
          onTagClick={(_id, tag) => onWikiWord?.(tag)}
          onTagContextMenu={noopTag}
          availableTags={availableTags}
          allCards={allCards}
        />
      )}

      <ConfirmDialog
        open={deleteConfirmOpen}
        title={deleteTaskIds.length > 0
          ? t('topologyCardContextMenu.deleteWorkflowTitle')
          : card?.type === 'task'
            ? t('topologyCardContextMenu.deleteTaskTitle')
            : t('topologyCardContextMenu.deleteTitle')}
        description={deleteTaskIds.length > 0
          ? t('topologyCardContextMenu.deleteWorkflowMessage', {
              name: cardId,
              count: deleteTaskIds.length,
            })
          : card?.type === 'task'
            ? t('topologyCardContextMenu.deleteTaskMessage', { name: cardId })
            : t('topologyCardContextMenu.deleteMessage', { name: cardId })}
        confirmLabel={t('common.delete')}
        cancelLabel={t('common.cancel')}
        danger
        onCancel={() => setDeleteConfirmOpen(false)}
        onConfirm={() => { void handleConfirmDelete() }}
      />
    </div>
  )
}

interface MonoCardDraftPanelProps {
  draftTitle: string
  projectId?: string | null
  /** Pre-filled body (e.g. text saved from the composer). */
  initialBody?: string
  onSaved: (cardId: string) => void
  onCancel: () => void
  onWikiWord?: (word: string) => void
}

export const MonoCardDraftPanel: React.FC<MonoCardDraftPanelProps> = ({ draftTitle, projectId, initialBody, onSaved, onCancel, onWikiWord }) => {
  const state = useMonoStore()
  const [draftCardId] = useState(() => `draft-${Date.now()}`)
  const [meta, setMeta] = useState<Partial<MonoCard>>({ id: draftTitle, tags: [] })
  const [body, setBody] = useState(initialBody ?? '')

  useEffect(() => {
    if (!projectId) return
    if (state.projectId !== projectId) {
      monoStore.setProjectId(projectId)
      monoStore.load()
    }
  }, [projectId, state.projectId])

  const handleMetaChange = useCallback((partial: Partial<MonoCard>) => {
    setMeta(prev => ({ ...prev, ...partial }))
  }, [])

  const handleSave = useCallback(async () => {
    const card = await monoStore.createCard({
      name: (meta.id || draftTitle || 'Untitled').trim(),
      body,
      tags: meta.tags || [],
      due: meta.due,
      priority: meta.priority,
      status: meta.status,
      parent: meta.parent,
      data: meta.data,
    })
    await monoStore.load()
    onSaved(card.id)
  }, [meta, body, draftTitle, onSaved])

  const availableTags = useMemo(() => {
    const set = new Set<string>(BUILTIN_TAG_CANONICALS)
    for (const c of state.cards) {
      for (const tag of c.tags) set.add(tag)
    }
    return Array.from(set)
  }, [state.cards])

  return (
    <div className="mono-card-panel">
      <DraftCardEditor
        cardId={draftCardId}
        meta={meta}
        body={body}
        onMetaChange={handleMetaChange}
        onBodyChange={setBody}
        onSave={handleSave}
        onCancel={onCancel}
        availableTags={availableTags}
        allCards={state.cards}
        isEntering={false}
        isRemoving={false}
        onWikiWordClick={(_id, word) => onWikiWord?.(word)}
      />
    </div>
  )
}
