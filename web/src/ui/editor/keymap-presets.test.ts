import { describe, expect, it } from 'vitest'
import {
  defaultKeymap,
  historyKeymap,
  moveLineUp,
  moveLineDown,
  copyLineUp,
  copyLineDown,
} from '@codemirror/commands'
import { foldKeymap } from '@codemirror/language'
import {
  buildEditorKeymap,
  isKeymapPreset,
  resolveKeymapBindings,
  DEFAULT_KEYMAP_PRESET,
  type KeymapPreset,
} from './keymap-presets'

const find = (bindings: ReturnType<typeof resolveKeymapBindings>, key: string) =>
  bindings.find(b => b.key === key)

describe('keymap-presets', () => {
  describe('isKeymapPreset', () => {
    it('accepts vscode and idea, rejects others', () => {
      expect(isKeymapPreset('vscode')).toBe(true)
      expect(isKeymapPreset('idea')).toBe(true)
      expect(isKeymapPreset('sublime')).toBe(false)
      expect(isKeymapPreset(undefined)).toBe(false)
    })

    it('defaults to vscode', () => {
      expect(DEFAULT_KEYMAP_PRESET).toBe<KeymapPreset>('vscode')
    })
  })

  describe('VS Code preset', () => {
    const bindings = resolveKeymapBindings('vscode')

    it('keeps the full default keymap + history + fold (no filtering)', () => {
      expect(bindings.length).toBe(defaultKeymap.length + historyKeymap.length + foldKeymap.length)
    })

    it('moves lines on Alt+Up/Down (VS Code behavior)', () => {
      expect(find(bindings, 'Alt-ArrowUp')?.run).toBe(moveLineUp)
      expect(find(bindings, 'Alt-ArrowDown')?.run).toBe(moveLineDown)
    })

    it('copies lines on Shift+Alt+Up/Down', () => {
      expect(find(bindings, 'Shift-Alt-ArrowUp')?.run).toBe(copyLineUp)
      expect(find(bindings, 'Shift-Alt-ArrowDown')?.run).toBe(copyLineDown)
    })
  })

  describe('IntelliJ IDEA preset', () => {
    const bindings = resolveKeymapBindings('idea')

    it('does NOT move lines on Alt+Up/Down', () => {
      const up = find(bindings, 'Alt-ArrowUp')
      const down = find(bindings, 'Alt-ArrowDown')
      expect(up?.run === moveLineUp).toBe(false)
      expect(down?.run === moveLineDown).toBe(false)
    })

    it('removes the copy-line bindings (Shift+Alt+Up/Down are reassigned)', () => {
      expect(bindings.some(b => b.run === copyLineUp)).toBe(false)
      expect(bindings.some(b => b.run === copyLineDown)).toBe(false)
    })

    it('moves lines on Shift+Alt+Up/Down (IDEA behavior)', () => {
      expect(find(bindings, 'Shift-Alt-ArrowUp')?.run).toBe(moveLineUp)
      expect(find(bindings, 'Shift-Alt-ArrowDown')?.run).toBe(moveLineDown)
    })

    it('keeps the rest of default + history + fold intact', () => {
      const kept = defaultKeymap.filter(
        b => b.run !== moveLineUp && b.run !== moveLineDown && b.run !== copyLineUp && b.run !== copyLineDown,
      )
      expect(bindings.length).toBe(2 + kept.length + historyKeymap.length + foldKeymap.length)
    })

    it('places IDEA overrides before the filtered defaults (override wins on conflict)', () => {
      const firstOverride = bindings[0]!
      const secondOverride = bindings[1]!
      expect(firstOverride.key).toBe('Shift-Alt-ArrowUp')
      expect(secondOverride.key).toBe('Shift-Alt-ArrowDown')
    })
  })

  describe('buildEditorKeymap', () => {
    it('wraps the resolved bindings in a keymap extension', () => {
      const ext = buildEditorKeymap('idea')
      expect(ext).toBeTruthy()
      expect(typeof ext).toBe('object')
    })
  })
})
