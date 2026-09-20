import { keymap, type KeyBinding } from '@codemirror/view'
import type { Extension } from '@codemirror/state'
import {
  defaultKeymap,
  historyKeymap,
  moveLineUp,
  moveLineDown,
  copyLineUp,
  copyLineDown,
} from '@codemirror/commands'
import { foldKeymap } from '@codemirror/language'

export type KeymapPreset = 'vscode' | 'idea'

export const DEFAULT_KEYMAP_PRESET: KeymapPreset = 'vscode'

export function isKeymapPreset(value: unknown): value is KeymapPreset {
  return value === 'vscode' || value === 'idea'
}

// defaultKeymap 已是 VS Code 风格:Alt+上/下移行,Shift+Alt 复制行,Ctrl+Alt 加光标。
function vscodeBindings(): KeyBinding[] {
  return [...defaultKeymap, ...historyKeymap, ...foldKeymap]
}

// IntelliJ IDEA 风格:Alt+上/下不移动代码行;改用 Shift+Alt+上/下移行。
function ideaBindings(): KeyBinding[] {
  const base = defaultKeymap.filter(
    b => b.run !== moveLineUp && b.run !== moveLineDown && b.run !== copyLineUp && b.run !== copyLineDown,
  )
  const ideaOverrides: KeyBinding[] = [
    { key: 'Shift-Alt-ArrowUp', run: moveLineUp },
    { key: 'Shift-Alt-ArrowDown', run: moveLineDown },
  ]
  return [...ideaOverrides, ...base, ...historyKeymap, ...foldKeymap]
}

export function resolveKeymapBindings(preset: KeymapPreset): KeyBinding[] {
  return preset === 'idea' ? ideaBindings() : vscodeBindings()
}

export function buildEditorKeymap(preset: KeymapPreset): Extension {
  return keymap.of(resolveKeymapBindings(preset))
}
