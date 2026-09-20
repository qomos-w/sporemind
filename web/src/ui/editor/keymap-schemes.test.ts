import { describe, expect, it } from 'vitest'
import {
  moveLineUp,
  moveLineDown,
  cursorLineUp,
  selectLineUp,
  deleteCharBackward,
  selectLine,
} from '@codemirror/commands'
import { resolveKeymapBindings } from './keymap-presets'
import {
  BIND_ACTIONS,
  BIND_CATEGORIES,
  createCustomScheme,
  defaultKeymapState,
  effectiveKeysForAction,
  findActionHoldingKey,
  keyEventToBinding,
  parseKeymapState,
  resolveSchemeBindings,
  type CustomScheme,
} from './keymap-schemes'

const mk = (id: string, name: string, base: 'vscode' | 'idea', overrides: CustomScheme['overrides']): CustomScheme =>
  ({ id, name, base, overrides })

describe('keymap-schemes', () => {
  describe('action catalog', () => {
    it('has unique ids with commands and valid categories', () => {
      const ids = BIND_ACTIONS.map(a => a.id)
      expect(new Set(ids).size).toBe(ids.length)
      expect(BIND_ACTIONS.length).toBe(49)
      for (const a of BIND_ACTIONS) {
        expect(typeof a.run).toBe('function')
        expect(a.labelKey).toMatch(/^settings\.keymap\.action\./)
        expect(BIND_CATEGORIES).toContain(a.category)
      }
    })

    it('movement selectPairs resolve to selection actions', () => {
      for (const a of BIND_ACTIONS) {
        if (!a.selectPair) continue
        const pair = BIND_ACTIONS.find(p => p.id === a.selectPair)
        expect(pair, `${a.id} pair`).toBeDefined()
        expect(pair!.category).toBe('selection')
      }
    })
  })

  describe('resolveSchemeBindings', () => {
    it('stock scheme resolves to the preset builder output', () => {
      expect(resolveSchemeBindings('vscode', [])).toEqual(resolveKeymapBindings('vscode'))
      expect(resolveSchemeBindings('idea', [])).toEqual(resolveKeymapBindings('idea'))
    })

    it('unknown scheme falls back to default preset', () => {
      expect(resolveSchemeBindings('nope', [])).toEqual(resolveKeymapBindings('vscode'))
    })

    it('custom override replaces the action bindings and is prepended', () => {
      const custom = mk('c1', 'Mine', 'idea', { moveLineUp: 'Ctrl-Alt-u' })
      const bindings = resolveSchemeBindings('c1', [custom])
      expect(bindings.find(b => b.key === 'Ctrl-Alt-u')?.run).toBe(moveLineUp)
      expect(bindings.find(b => b.key === 'Shift-Alt-ArrowUp')?.run === moveLineUp).toBe(false)
      expect(bindings.find(b => b.key === 'Shift-Alt-ArrowDown')?.run).toBe(moveLineDown)
      expect(bindings[0]?.key).toBe('Ctrl-Alt-u')
    })

    it('override removes ALL keys of that action from base', () => {
      const custom = mk('c1', 'Mine', 'vscode', { selectLine: 'Mod-Alt-l' })
      const bindings = resolveSchemeBindings('c1', [custom])
      expect(bindings.some(b => b.run === selectLine && b.key !== 'Mod-Alt-l')).toBe(false)
      expect(bindings.find(b => b.key === 'Mod-Alt-l')?.run).toBe(selectLine)
    })

    it('overriding a movement action moves its Shift-select along', () => {
      const custom = mk('c1', 'Mine', 'vscode', { cursorLineUp: 'Mod-Alt-u' })
      const bindings = resolveSchemeBindings('c1', [custom])
      const newBinding = bindings.find(b => b.key === 'Mod-Alt-u')
      expect(newBinding?.run).toBe(cursorLineUp)
      expect(newBinding?.shift).toBe(selectLineUp)
      // 旧键 ArrowUp 上的移动与 Shift-选择均失效
      expect(bindings.some(b => b.key === 'ArrowUp')).toBe(false)
      expect(effectiveKeysForAction('selectLineUp', bindings)).toContain('Shift-Mod-Alt-u')
      expect(effectiveKeysForAction('selectLineUp', bindings)).not.toContain('Shift-ArrowUp')
    })

    it('overriding only the select action keeps movement on the old key', () => {
      const custom = mk('c1', 'Mine', 'vscode', { selectLineUp: 'Mod-Alt-s' })
      const bindings = resolveSchemeBindings('c1', [custom])
      const arrow = bindings.find(b => b.key === 'ArrowUp')
      expect(arrow?.run).toBe(cursorLineUp)
      expect(arrow?.shift).toBeUndefined()
      expect(bindings.find(b => b.key === 'Mod-Alt-s')?.run).toBe(selectLineUp)
    })

    it('overriding both sides emits two independent bindings', () => {
      const custom = mk('c1', 'Mine', 'vscode', { cursorLineUp: 'Mod-Alt-u', selectLineUp: 'Mod-Alt-s' })
      const bindings = resolveSchemeBindings('c1', [custom])
      expect(bindings.find(b => b.key === 'Mod-Alt-u')?.shift).toBeUndefined()
      expect(effectiveKeysForAction('cursorLineUp', bindings)).toEqual(['Mod-Alt-u'])
      expect(effectiveKeysForAction('selectLineUp', bindings)).toEqual(['Mod-Alt-s'])
    })

    it('bindings sharing run and shift (Backspace) are fully replaced', () => {
      const custom = mk('c1', 'Mine', 'vscode', { deleteCharBackward: 'Mod-Shift-Backspace' })
      const bindings = resolveSchemeBindings('c1', [custom])
      expect(bindings.some(b => b.key === 'Backspace' && b.run === deleteCharBackward)).toBe(false)
      expect(bindings.find(b => b.key === 'Mod-Shift-Backspace')?.run).toBe(deleteCharBackward)
    })
  })

  describe('effectiveKeysForAction', () => {
    it('lists base keys including Shift- select variants', () => {
      const vscode = resolveKeymapBindings('vscode')
      expect(effectiveKeysForAction('selectLineUp', vscode)).toContain('Shift-ArrowUp')
      expect(effectiveKeysForAction('cursorLineBoundaryForward', vscode)).toContain('End')
      expect(effectiveKeysForAction('selectLineBoundaryForward', vscode)).toContain('Shift-End')
    })

    it('ignores mac-only bindings and unknown actions', () => {
      const vscode = resolveKeymapBindings('vscode')
      const keys = effectiveKeysForAction('cursorPageUp', vscode)
      expect(keys).toContain('PageUp')
      expect(keys.every(k => !!k)).toBe(true)
      expect(effectiveKeysForAction('nope' as never, [])).toEqual([])
    })
  })

  describe('findActionHoldingKey', () => {
    it('detects a key held by another action, including Shift- variants', () => {
      const vscode = resolveKeymapBindings('vscode')
      expect(findActionHoldingKey('Alt-ArrowDown', vscode, 'moveLineUp')).toBe('moveLineDown')
      expect(findActionHoldingKey('Shift-ArrowDown', vscode, 'selectLineDown')).toBeNull()
      expect(findActionHoldingKey('Shift-ArrowDown', vscode, 'moveLineUp')).toBe('selectLineDown')
    })

    it('returns null when the key is free', () => {
      expect(findActionHoldingKey('Shift-Alt-F13', resolveKeymapBindings('vscode'), 'moveLineUp')).toBeNull()
    })
  })

  describe('createCustomScheme', () => {
    it('copies from a stock preset with empty overrides', () => {
      const c = createCustomScheme('id1', 'From VS', 'vscode', [])
      expect(c).toEqual({ id: 'id1', name: 'From VS', base: 'vscode', overrides: {} })
    })

    it('copies from another custom as a flattened snapshot', () => {
      const src = mk('src', 'Src', 'idea', { moveLineUp: 'Ctrl-Alt-u', undo: 'Mod-u' })
      const copy = createCustomScheme('id2', 'Copy', 'src', [src])
      expect(copy.base).toBe('idea')
      expect(copy.overrides).toEqual({ moveLineUp: 'Ctrl-Alt-u', undo: 'Mod-u' })
      copy.overrides.moveLineUp = 'x'
      expect(src.overrides.moveLineUp).toBe('Ctrl-Alt-u')
    })

    it('falls back to default preset when the source is unknown', () => {
      expect(createCustomScheme('id3', 'X', 'ghost', []).base).toBe('vscode')
    })
  })

  describe('parseKeymapState', () => {
    it('returns defaults for empty/invalid input', () => {
      expect(parseKeymapState(undefined)).toEqual(defaultKeymapState())
      expect(parseKeymapState('not-json')).toEqual(defaultKeymapState())
      expect(parseKeymapState('42')).toEqual(defaultKeymapState())
    })

    it('migrates v1 {preset} payloads', () => {
      expect(parseKeymapState('{"preset":"idea"}')).toEqual({ version: 2, selected: 'idea', customs: [] })
    })

    it('accepts and sanitizes v2 payloads', () => {
      const raw = JSON.stringify({
        version: 2,
        selected: 'c1',
        customs: [{ id: 'c1', name: 'Mine', base: 'idea', overrides: { moveLineUp: 'Ctrl-Alt-u', bogus: 'zzz' } }],
      })
      const state = parseKeymapState(raw)
      expect(state.selected).toBe('c1')
      expect(state.customs[0]?.overrides).toEqual({ moveLineUp: 'Ctrl-Alt-u' })
    })

    it('drops malformed customs and repairs invalid selections', () => {
      const state = parseKeymapState(JSON.stringify({
        version: 2,
        selected: 'missing',
        customs: [{ id: 'c1', name: 'Ok', base: 'idea', overrides: {} }, { broken: true }],
      }))
      expect(state.customs.map(c => c.id)).toEqual(['c1'])
      expect(state.selected).toBe('vscode')
    })
  })

  describe('keyEventToBinding', () => {
    const ev = (over: Partial<KeyboardEvent>) => over as KeyboardEvent

    it('combines modifiers in canonical order', () => {
      expect(keyEventToBinding(ev({ key: 'ArrowUp', keyCode: 38, shiftKey: true, altKey: true } as never), false))
        .toBe('Shift-Alt-ArrowUp')
    })

    it('maps the platform primary modifier to Mod', () => {
      expect(keyEventToBinding(ev({ key: 'k', keyCode: 75, ctrlKey: true } as never), false)).toBe('Mod-k')
      expect(keyEventToBinding(ev({ key: 'k', keyCode: 75, metaKey: true } as never), true)).toBe('Mod-k')
      // 次修饰键保留原名:mac 上 Ctrl、win 上 Meta(即 Cmd)
      expect(keyEventToBinding(ev({ key: 'k', keyCode: 75, ctrlKey: true } as never), true)).toBe('Ctrl-k')
      expect(keyEventToBinding(ev({ key: 'k', keyCode: 75, metaKey: true } as never), false)).toBe('Cmd-k')
    })

    it('returns null for pure modifier presses and unknown keys', () => {
      expect(keyEventToBinding(ev({ key: 'Shift', keyCode: 16, shiftKey: true } as never), false)).toBeNull()
      expect(keyEventToBinding(ev({ key: 'Control', keyCode: 17, ctrlKey: true } as never), false)).toBeNull()
      expect(keyEventToBinding(ev({} as never), false)).toBeNull()
    })

    it('supports four-modifier combos', () => {
      expect(keyEventToBinding(ev({ key: 'ArrowDown', keyCode: 40, shiftKey: true, ctrlKey: true, altKey: true } as never), false))
        .toBe('Shift-Mod-Alt-ArrowDown')
    })
  })
})
