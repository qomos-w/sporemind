import type { KeyBinding } from '@codemirror/view'
import type { Command } from '@codemirror/view'
import {
  moveLineUp,
  moveLineDown,
  copyLineUp,
  copyLineDown,
  addCursorAbove,
  addCursorBelow,
  deleteLine,
  indentMore,
  indentLess,
  selectLine,
  toggleComment,
  undo,
  redo,
  cursorCharLeft,
  cursorCharRight,
  cursorGroupLeft,
  cursorGroupRight,
  cursorLineUp,
  cursorLineDown,
  cursorPageUp,
  cursorPageDown,
  cursorLineBoundaryBackward,
  cursorLineBoundaryForward,
  cursorDocStart,
  cursorDocEnd,
  cursorMatchingBracket,
  selectCharLeft,
  selectCharRight,
  selectGroupLeft,
  selectGroupRight,
  selectLineUp,
  selectLineDown,
  selectPageUp,
  selectPageDown,
  selectLineBoundaryBackward,
  selectLineBoundaryForward,
  selectDocStart,
  selectDocEnd,
  selectAll,
  deleteCharBackward,
  deleteCharForward,
  deleteGroupBackward,
  deleteGroupForward,
  deleteToLineStart,
  deleteToLineEnd,
  insertNewlineAndIndent,
  insertBlankLine,
} from '@codemirror/commands'
import { foldCode, unfoldCode } from '@codemirror/language'
import { keyName } from 'w3c-keyname'
import { resolveKeymapBindings, isKeymapPreset, DEFAULT_KEYMAP_PRESET, type KeymapPreset } from './keymap-presets'

export type StockSchemeId = KeymapPreset

export type BindActionCategory = 'movement' | 'selection' | 'editing' | 'lines' | 'misc'

export const BIND_CATEGORIES: readonly BindActionCategory[] = ['movement', 'selection', 'editing', 'lines', 'misc']

export type BindActionId =
  | 'cursorCharLeft' | 'cursorCharRight'
  | 'cursorGroupLeft' | 'cursorGroupRight'
  | 'cursorLineUp' | 'cursorLineDown'
  | 'cursorPageUp' | 'cursorPageDown'
  | 'cursorLineBoundaryBackward' | 'cursorLineBoundaryForward'
  | 'cursorDocStart' | 'cursorDocEnd'
  | 'cursorMatchingBracket'
  | 'selectCharLeft' | 'selectCharRight'
  | 'selectGroupLeft' | 'selectGroupRight'
  | 'selectLineUp' | 'selectLineDown'
  | 'selectPageUp' | 'selectPageDown'
  | 'selectLineBoundaryBackward' | 'selectLineBoundaryForward'
  | 'selectDocStart' | 'selectDocEnd'
  | 'selectAll'
  | 'deleteCharBackward' | 'deleteCharForward'
  | 'deleteGroupBackward' | 'deleteGroupForward'
  | 'deleteToLineStart' | 'deleteToLineEnd'
  | 'insertNewlineAndIndent' | 'insertBlankLine'
  | 'moveLineUp' | 'moveLineDown'
  | 'copyLineUp' | 'copyLineDown'
  | 'addCursorAbove' | 'addCursorBelow'
  | 'deleteLine' | 'indentMore' | 'indentLess' | 'selectLine'
  | 'toggleComment' | 'undo' | 'redo'
  | 'foldCode' | 'unfoldCode'

export interface BindAction {
  id: BindActionId
  category: BindActionCategory
  labelKey: string
  run: Command
  /** 配对的选择动作:覆盖移动动作时,Shift-新键自动触发对应选择。 */
  selectPair?: BindActionId
}

export const BIND_ACTIONS: readonly BindAction[] = [
  // 光标移动
  { id: 'cursorCharLeft', category: 'movement', labelKey: 'settings.keymap.action.cursorCharLeft', run: cursorCharLeft, selectPair: 'selectCharLeft' },
  { id: 'cursorCharRight', category: 'movement', labelKey: 'settings.keymap.action.cursorCharRight', run: cursorCharRight, selectPair: 'selectCharRight' },
  { id: 'cursorGroupLeft', category: 'movement', labelKey: 'settings.keymap.action.cursorGroupLeft', run: cursorGroupLeft, selectPair: 'selectGroupLeft' },
  { id: 'cursorGroupRight', category: 'movement', labelKey: 'settings.keymap.action.cursorGroupRight', run: cursorGroupRight, selectPair: 'selectGroupRight' },
  { id: 'cursorLineUp', category: 'movement', labelKey: 'settings.keymap.action.cursorLineUp', run: cursorLineUp, selectPair: 'selectLineUp' },
  { id: 'cursorLineDown', category: 'movement', labelKey: 'settings.keymap.action.cursorLineDown', run: cursorLineDown, selectPair: 'selectLineDown' },
  { id: 'cursorPageUp', category: 'movement', labelKey: 'settings.keymap.action.cursorPageUp', run: cursorPageUp, selectPair: 'selectPageUp' },
  { id: 'cursorPageDown', category: 'movement', labelKey: 'settings.keymap.action.cursorPageDown', run: cursorPageDown, selectPair: 'selectPageDown' },
  { id: 'cursorLineBoundaryBackward', category: 'movement', labelKey: 'settings.keymap.action.cursorLineBoundaryBackward', run: cursorLineBoundaryBackward, selectPair: 'selectLineBoundaryBackward' },
  { id: 'cursorLineBoundaryForward', category: 'movement', labelKey: 'settings.keymap.action.cursorLineBoundaryForward', run: cursorLineBoundaryForward, selectPair: 'selectLineBoundaryForward' },
  { id: 'cursorDocStart', category: 'movement', labelKey: 'settings.keymap.action.cursorDocStart', run: cursorDocStart, selectPair: 'selectDocStart' },
  { id: 'cursorDocEnd', category: 'movement', labelKey: 'settings.keymap.action.cursorDocEnd', run: cursorDocEnd, selectPair: 'selectDocEnd' },
  { id: 'cursorMatchingBracket', category: 'movement', labelKey: 'settings.keymap.action.cursorMatchingBracket', run: cursorMatchingBracket },

  // 选择
  { id: 'selectCharLeft', category: 'selection', labelKey: 'settings.keymap.action.selectCharLeft', run: selectCharLeft },
  { id: 'selectCharRight', category: 'selection', labelKey: 'settings.keymap.action.selectCharRight', run: selectCharRight },
  { id: 'selectGroupLeft', category: 'selection', labelKey: 'settings.keymap.action.selectGroupLeft', run: selectGroupLeft },
  { id: 'selectGroupRight', category: 'selection', labelKey: 'settings.keymap.action.selectGroupRight', run: selectGroupRight },
  { id: 'selectLineUp', category: 'selection', labelKey: 'settings.keymap.action.selectLineUp', run: selectLineUp },
  { id: 'selectLineDown', category: 'selection', labelKey: 'settings.keymap.action.selectLineDown', run: selectLineDown },
  { id: 'selectPageUp', category: 'selection', labelKey: 'settings.keymap.action.selectPageUp', run: selectPageUp },
  { id: 'selectPageDown', category: 'selection', labelKey: 'settings.keymap.action.selectPageDown', run: selectPageDown },
  { id: 'selectLineBoundaryBackward', category: 'selection', labelKey: 'settings.keymap.action.selectLineBoundaryBackward', run: selectLineBoundaryBackward },
  { id: 'selectLineBoundaryForward', category: 'selection', labelKey: 'settings.keymap.action.selectLineBoundaryForward', run: selectLineBoundaryForward },
  { id: 'selectDocStart', category: 'selection', labelKey: 'settings.keymap.action.selectDocStart', run: selectDocStart },
  { id: 'selectDocEnd', category: 'selection', labelKey: 'settings.keymap.action.selectDocEnd', run: selectDocEnd },
  { id: 'selectAll', category: 'selection', labelKey: 'settings.keymap.action.selectAll', run: selectAll },

  // 编辑与删除
  { id: 'deleteCharBackward', category: 'editing', labelKey: 'settings.keymap.action.deleteCharBackward', run: deleteCharBackward },
  { id: 'deleteCharForward', category: 'editing', labelKey: 'settings.keymap.action.deleteCharForward', run: deleteCharForward },
  { id: 'deleteGroupBackward', category: 'editing', labelKey: 'settings.keymap.action.deleteGroupBackward', run: deleteGroupBackward },
  { id: 'deleteGroupForward', category: 'editing', labelKey: 'settings.keymap.action.deleteGroupForward', run: deleteGroupForward },
  { id: 'deleteToLineStart', category: 'editing', labelKey: 'settings.keymap.action.deleteToLineStart', run: deleteToLineStart },
  { id: 'deleteToLineEnd', category: 'editing', labelKey: 'settings.keymap.action.deleteToLineEnd', run: deleteToLineEnd },
  { id: 'insertNewlineAndIndent', category: 'editing', labelKey: 'settings.keymap.action.insertNewlineAndIndent', run: insertNewlineAndIndent },
  { id: 'insertBlankLine', category: 'editing', labelKey: 'settings.keymap.action.insertBlankLine', run: insertBlankLine },

  // 行与缩进
  { id: 'moveLineUp', category: 'lines', labelKey: 'settings.keymap.action.moveLineUp', run: moveLineUp },
  { id: 'moveLineDown', category: 'lines', labelKey: 'settings.keymap.action.moveLineDown', run: moveLineDown },
  { id: 'copyLineUp', category: 'lines', labelKey: 'settings.keymap.action.copyLineUp', run: copyLineUp },
  { id: 'copyLineDown', category: 'lines', labelKey: 'settings.keymap.action.copyLineDown', run: copyLineDown },
  { id: 'addCursorAbove', category: 'lines', labelKey: 'settings.keymap.action.addCursorAbove', run: addCursorAbove },
  { id: 'addCursorBelow', category: 'lines', labelKey: 'settings.keymap.action.addCursorBelow', run: addCursorBelow },
  { id: 'deleteLine', category: 'lines', labelKey: 'settings.keymap.action.deleteLine', run: deleteLine },
  { id: 'indentMore', category: 'lines', labelKey: 'settings.keymap.action.indentMore', run: indentMore },
  { id: 'indentLess', category: 'lines', labelKey: 'settings.keymap.action.indentLess', run: indentLess },
  { id: 'selectLine', category: 'lines', labelKey: 'settings.keymap.action.selectLine', run: selectLine },

  // 注释与历史
  { id: 'toggleComment', category: 'misc', labelKey: 'settings.keymap.action.toggleComment', run: toggleComment },
  { id: 'undo', category: 'misc', labelKey: 'settings.keymap.action.undo', run: undo },
  { id: 'redo', category: 'misc', labelKey: 'settings.keymap.action.redo', run: redo },
  { id: 'foldCode', category: 'misc', labelKey: 'settings.keymap.action.foldCode', run: foldCode },
  { id: 'unfoldCode', category: 'misc', labelKey: 'settings.keymap.action.unfoldCode', run: unfoldCode },
]

const ACTION_BY_ID: ReadonlyMap<BindActionId, BindAction> = new Map(BIND_ACTIONS.map(a => [a.id, a]))

export function getBindAction(id: BindActionId): BindAction | undefined {
  return ACTION_BY_ID.get(id)
}

export interface CustomScheme {
  id: string
  name: string
  base: StockSchemeId
  overrides: Partial<Record<BindActionId, string>>
}

export interface KeymapStateV2 {
  version: 2
  selected: string
  customs: CustomScheme[]
}

export function defaultKeymapState(): KeymapStateV2 {
  return { version: 2, selected: DEFAULT_KEYMAP_PRESET, customs: [] }
}

export function findCustomScheme(id: string, customs: CustomScheme[]): CustomScheme | undefined {
  return customs.find(c => c.id === id)
}

/** 从股票预设或另一个自定义方案复制出新的自定义方案(扁平快照,复制后互不影响)。 */
export function createCustomScheme(
  id: string,
  name: string,
  source: string,
  customs: CustomScheme[],
): CustomScheme {
  const src = findCustomScheme(source, customs)
  if (!src) {
    const base: StockSchemeId = isKeymapPreset(source) ? source : DEFAULT_KEYMAP_PRESET
    return { id, name, base, overrides: {} }
  }
  return { id, name, base: src.base, overrides: { ...src.overrides } }
}

/** 为移动动作加 Shift- 前缀得到其选择绑定显示键。 */
function withShift(key: string): string {
  return key.startsWith('Shift-') ? key : `Shift-${key}`
}

/**
 * 解析方案最终绑定。覆盖语义遵循 IDE 习惯:
 * - 覆盖移动动作 A 到键 K:旧键上 A 与其配对选择一并失效,新绑定 {key: K, run: A, shift: 配对}
 *   (Shift+K 即选择)。若配对也被单独覆盖,则只发 {key: K, run: A}。
 * - 仅覆盖选择动作 S 到键 K:旧键保留移动,选择失效,新发 {key: K, run: S}。
 */
export function resolveSchemeBindings(selected: string, customs: CustomScheme[]): KeyBinding[] {
  const custom = findCustomScheme(selected, customs)
  if (!custom) {
    const preset: StockSchemeId = isKeymapPreset(selected) ? selected : DEFAULT_KEYMAP_PRESET
    return resolveKeymapBindings(preset)
  }
  const overridesByRun = new Map<Command, string>()
  for (const a of BIND_ACTIONS) {
    const key = custom.overrides[a.id]
    if (key) overridesByRun.set(a.run, key)
  }
  const base = resolveKeymapBindings(custom.base)
  const newBindings: KeyBinding[] = []
  const surviving: KeyBinding[] = []
  for (const b of base) {
    if (!b.key || !b.run) continue
    const runKey = overridesByRun.get(b.run)
    const shiftKey = b.shift ? overridesByRun.get(b.shift) : undefined
    if (runKey) continue
    if (shiftKey) { surviving.push({ key: b.key, run: b.run }); continue }
    surviving.push(b)
  }
  for (const a of BIND_ACTIONS) {
    const key = custom.overrides[a.id]
    if (!key) continue
    const pair = a.selectPair ? ACTION_BY_ID.get(a.selectPair) : undefined
    if (pair && !custom.overrides[pair.id]) newBindings.push({ key, run: a.run, shift: pair.run })
    else newBindings.push({ key, run: a.run })
  }
  return [...newBindings, ...surviving]
}

/** 动作在最终绑定中的全部按键(含 Shift- 选择变体)。 */
export function effectiveKeysForAction(
  actionId: BindActionId,
  bindings: KeyBinding[],
): string[] {
  const action = ACTION_BY_ID.get(actionId)
  if (!action) return []
  const keys: string[] = []
  for (const b of bindings) {
    if (!b.key) continue
    if (b.run === action.run) keys.push(b.key)
    else if (b.shift === action.run) keys.push(withShift(b.key))
  }
  return keys
}

/** 键冲突检测:该键当前被哪个其他动作占用(含 Shift- 变体),供 UI 提示。 */
export function findActionHoldingKey(
  key: string,
  bindings: KeyBinding[],
  excludeActionId: BindActionId,
): BindActionId | null {
  const exclude = ACTION_BY_ID.get(excludeActionId)
  for (const action of BIND_ACTIONS) {
    if (action.run === exclude?.run) continue
    if (effectiveKeysForAction(action.id, bindings).includes(key)) return action.id
  }
  return null
}

const MODIFIER_KEYS = new Set(['Shift', 'Control', 'Ctrl', 'Alt', 'Meta', 'OS', 'Dead', 'Unidentified'])

/**
 * 键盘事件 → CodeMirror 规范键串(如 Shift-Alt-ArrowUp / Mod-k)。
 * 主修饰键统一写为 Mod(mac=Cmd,其余=Ctrl),跨平台一致;纯修饰键按压返回 null。
 */
export function keyEventToBinding(event: KeyboardEvent, isMac: boolean): string | null {
  let name = keyName(event as unknown as Event)
  if (!name || MODIFIER_KEYS.has(name)) {
    const key = event.key
    if (!key || MODIFIER_KEYS.has(key)) return null
    name = key
  }
  const mods: string[] = []
  if (event.shiftKey) mods.push('Shift')
  const primary = isMac ? event.metaKey : event.ctrlKey
  const secondary = isMac ? event.ctrlKey : event.metaKey
  if (primary) mods.push('Mod')
  if (secondary) mods.push(isMac ? 'Ctrl' : 'Cmd')
  if (event.altKey) mods.push('Alt')
  return [...mods, name].join('-')
}

function sanitizeOverrides(raw: unknown): Partial<Record<BindActionId, string>> {
  if (!raw || typeof raw !== 'object') return {}
  const out: Partial<Record<BindActionId, string>> = {}
  for (const action of BIND_ACTIONS) {
    const v = (raw as Record<string, unknown>)[action.id]
    if (typeof v === 'string' && v.length > 0 && v.length <= 64) out[action.id] = v
  }
  return out
}

function sanitizeCustom(raw: unknown): CustomScheme | null {
  if (!raw || typeof raw !== 'object') return null
  const r = raw as Record<string, unknown>
  if (typeof r.id !== 'string' || !r.id || typeof r.name !== 'string' || !r.name) return null
  const base: StockSchemeId = isKeymapPreset(r.base) ? r.base : DEFAULT_KEYMAP_PRESET
  return { id: r.id, name: r.name, base, overrides: sanitizeOverrides(r.overrides) }
}

/** 解析持久化 JSON:V2 校验净化;V1 {preset} 自动迁移;任何损坏回退默认。 */
export function parseKeymapState(raw: string | undefined): KeymapStateV2 {
  if (!raw) return defaultKeymapState()
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return defaultKeymapState()
  }
  if (!parsed || typeof parsed !== 'object') return defaultKeymapState()
  const obj = parsed as Record<string, unknown>
  if (isKeymapPreset(obj.preset) && obj.version !== 2) {
    return { version: 2, selected: obj.preset, customs: [] }
  }
  const customs = Array.isArray(obj.customs)
    ? obj.customs.map(sanitizeCustom).filter((c): c is CustomScheme => !!c)
    : []
  let selected = typeof obj.selected === 'string' ? obj.selected : DEFAULT_KEYMAP_PRESET
  if (selected !== 'vscode' && selected !== 'idea' && !customs.some(c => c.id === selected)) {
    selected = DEFAULT_KEYMAP_PRESET
  }
  return { version: 2, selected, customs }
}
