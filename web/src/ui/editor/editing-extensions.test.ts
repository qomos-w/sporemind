import { describe, it, expect, vi } from 'vitest'
import { indentWithTab } from '@codemirror/commands'
import { openSearchPanel } from '@codemirror/search'
import { editingKeymapBindings, editingExtensions, type FindReplaceHandlers, type FindReplaceMode } from './editing-extensions'

const keys = (bindings = editingKeymapBindings()) => bindings.map(b => b.key)

function makeHandlers() {
  const onOpen = vi.fn<(mode: FindReplaceMode) => void>()
  const onClose = vi.fn<() => boolean>(() => false)
  return { handlers: { onOpen, onClose } satisfies FindReplaceHandlers, onOpen, onClose }
}

describe('editingKeymapBindings', () => {
  it('binds tab indent', () => {
    expect(editingKeymapBindings()).toContainEqual(indentWithTab)
    expect(keys()).toContain('Tab')
  })

  it('binds the stock search keymap when no custom handlers are provided (Ctrl+F still opens find)', () => {
    const ks = keys()
    expect(ks).toContain('Mod-f')
    expect(ks).toContain('F3')
    expect(ks).toContain('Mod-g')
    expect(ks).toContain('Mod-d')
    expect(ks).toContain('Mod-Shift-l')
    expect(ks).toContain('Mod-Alt-g')
  })

  it('binds bracket-pair Backspace and lint navigation (F8, Ctrl+Shift+M)', () => {
    const ks = keys()
    expect(ks).toContain('Backspace')
    expect(ks).toContain('F8')
    expect(ks).toContain('Mod-Shift-m')
  })

  it('binds Ctrl+F/Ctrl+R to the custom panel and removes the default openSearchPanel binding', () => {
    const { handlers, onOpen } = makeHandlers()
    const bindings = editingKeymapBindings(handlers)
    const ks = keys(bindings)
    expect(ks).toContain('Mod-f')
    expect(ks).toContain('Mod-r')
    expect(ks).toContain('Escape')

    const modF = bindings.find(b => b.key === 'Mod-f')
    const modR = bindings.find(b => b.key === 'Mod-r')
    expect(modF).toBeDefined()
    expect(modR).toBeDefined()
    expect(modF!.run).not.toBe(openSearchPanel)
    expect(modR!.run).not.toBe(openSearchPanel)

    // The run handlers ignore the view argument and just call the hooks.
    expect(modF!.run?.(null as unknown as never)).toBe(true)
    expect(onOpen).toHaveBeenLastCalledWith('find')
    expect(modR!.run?.(null as unknown as never)).toBe(true)
    expect(onOpen).toHaveBeenLastCalledWith('replace')
  })

  it('routes Escape to the custom close hook and lets it fall through when closed', () => {
    const { handlers, onClose } = makeHandlers()
    const bindings = editingKeymapBindings(handlers)
    const escape = bindings.find(b => b.key === 'Escape')

    onClose.mockReturnValueOnce(false)
    expect(escape!.run?.(null as unknown as never)).toBe(false)

    onClose.mockReturnValueOnce(true)
    expect(escape!.run?.(null as unknown as never)).toBe(true)
  })

  it('keeps match navigation keys when custom handlers are provided', () => {
    const ks = keys(editingKeymapBindings(makeHandlers().handlers))
    expect(ks).toContain('F3')
    expect(ks).toContain('Mod-g')
  })
})

describe('editingExtensions', () => {
  it('always provides search extensions and the keymap, even read-only', () => {
    expect(editingExtensions({ editable: false }).length).toBeGreaterThan(0)
  })

  it('adds typing companions (closeBrackets, autocompletion) only when editable', () => {
    const ro = editingExtensions({ editable: false })
    const ed = editingExtensions({ editable: true })
    expect(ed.length).toBe(ro.length + 2)
  })

  it('defaults to read-only', () => {
    expect(editingExtensions()).toHaveLength(editingExtensions({ editable: false }).length)
  })

  it('still returns the same extension shape when custom handlers are supplied', () => {
    const without = editingExtensions({ editable: false })
    const withHandlers = editingExtensions({ editable: false, findReplace: makeHandlers().handlers })
    expect(withHandlers.length).toBe(without.length)
  })
})
