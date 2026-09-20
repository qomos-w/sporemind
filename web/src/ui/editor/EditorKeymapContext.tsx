import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { Extension } from '@codemirror/state'
import { keymap } from '@codemirror/view'
import { loadEditorKeymap, persistEditorKeymap } from '../../application/editor-keymap-persist'
import {
  BIND_ACTIONS,
  createCustomScheme,
  defaultKeymapState,
  resolveSchemeBindings,
  type BindActionId,
  type CustomScheme,
  type KeymapStateV2,
} from './keymap-schemes'

interface EditorKeymapContextValue {
  state: KeymapStateV2
  customs: CustomScheme[]
  keymapExtension: Extension
  selectScheme: (schemeId: string) => void
  /** 从股票预设或另一自定义方案复制创建,返回新方案 id。 */
  createCustom: (name: string, sourceId: string) => string
  deleteCustom: (id: string) => void
  renameCustom: (id: string, name: string) => void
  /** key=null 表示清除覆盖、恢复继承 base。 */
  setBinding: (customId: string, actionId: BindActionId, key: string | null) => void
}

const EditorKeymapContext = createContext<EditorKeymapContextValue | null>(null)

function newSchemeId(): string {
  return typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `km-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

function uniqueCustomName(base: string, customs: CustomScheme[]): string {
  const taken = new Set(customs.map(c => c.name))
  if (!taken.has(base)) return base
  for (let i = 2; ; i++) {
    const candidate = `${base} ${i}`
    if (!taken.has(candidate)) return candidate
  }
}

export const EditorKeymapProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [state, setState] = useState<KeymapStateV2>(defaultKeymapState)

  useEffect(() => {
    let cancelled = false
    loadEditorKeymap()
      .then(next => { if (!cancelled) setState(next) })
      .catch(() => {})
    return () => { cancelled = true }
  }, [])

  const update = useCallback((mutate: (prev: KeymapStateV2) => KeymapStateV2) => {
    setState(prev => {
      const next = mutate(prev)
      void persistEditorKeymap(next).catch(() => {})
      return next
    })
  }, [])

  const selectScheme = useCallback((schemeId: string) => {
    update(prev => ({ ...prev, selected: schemeId }))
  }, [update])

  const createCustom = useCallback((name: string, sourceId: string) => {
    const id = newSchemeId()
    update(prev => {
      const trimmed = name.trim() || uniqueCustomName(sourceId, prev.customs)
      const scheme = createCustomScheme(id, uniqueCustomName(trimmed, prev.customs), sourceId, prev.customs)
      return { ...prev, customs: [...prev.customs, scheme], selected: id }
    })
    return id
  }, [update])

  const deleteCustom = useCallback((id: string) => {
    update(prev => {
      const customs = prev.customs.filter(c => c.id !== id)
      const selected = prev.selected === id ? prev.customs.find(c => c.id === id)?.base ?? 'vscode' : prev.selected
      return { ...prev, customs, selected }
    })
  }, [update])

  const renameCustom = useCallback((id: string, name: string) => {
    const trimmed = name.trim()
    if (!trimmed) return
    update(prev => ({
      ...prev,
      customs: prev.customs.map(c => (c.id === id ? { ...c, name: trimmed } : c)),
    }))
  }, [update])

  const setBinding = useCallback((customId: string, actionId: BindActionId, key: string | null) => {
    if (!BIND_ACTIONS.some(a => a.id === actionId)) return
    update(prev => ({
      ...prev,
      customs: prev.customs.map(c => {
        if (c.id !== customId) return c
        const overrides = { ...c.overrides }
        if (key === null) delete overrides[actionId]
        else overrides[actionId] = key
        return { ...c, overrides }
      }),
    }))
  }, [update])

  const keymapExtension = useMemo(
    () => keymap.of(resolveSchemeBindings(state.selected, state.customs)),
    [state.selected, state.customs],
  )

  const value = useMemo(() => ({
    state,
    customs: state.customs,
    keymapExtension,
    selectScheme,
    createCustom,
    deleteCustom,
    renameCustom,
    setBinding,
  }), [state, keymapExtension, selectScheme, createCustom, deleteCustom, renameCustom, setBinding])

  return (
    <EditorKeymapContext.Provider value={value}>
      {children}
    </EditorKeymapContext.Provider>
  )
}

export function useEditorKeymap(): EditorKeymapContextValue {
  const ctx = useContext(EditorKeymapContext)
  if (!ctx) {
    throw new Error('useEditorKeymap must be used within EditorKeymapProvider')
  }
  return ctx
}
