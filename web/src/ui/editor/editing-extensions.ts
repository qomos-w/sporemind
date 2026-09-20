import type { Extension } from '@codemirror/state'
import { Prec } from '@codemirror/state'
import { keymap, type KeyBinding } from '@codemirror/view'
import { indentWithTab } from '@codemirror/commands'
import {
  search,
  searchKeymap,
  highlightSelectionMatches,
} from '@codemirror/search'
import {
  closeBrackets,
  closeBracketsKeymap,
  autocompletion,
} from '@codemirror/autocomplete'
import { lintKeymap } from '@codemirror/lint'
import type { FindReplaceOpener } from './FindReplacePanel'

export type { FindReplaceMode, FindReplaceOpener } from './FindReplacePanel'

/** Editor-side hooks for the custom find/replace panel. */
export interface FindReplaceHandlers {
  /** Ctrl+F / Ctrl+R — open the panel in find or replace mode. */
  onOpen: FindReplaceOpener
  /** Escape inside the editor — close the panel; return true when it was open. */
  onClose: () => boolean
}

/** searchKeymap without Mod-f: when a custom opener is provided, the
 * FindReplacePanel owns the find/replace UI and the built-in panel (and its
 * Mod-f → openSearchPanel binding) must never activate. */
const searchKeymapWithoutOpenPanel = (): KeyBinding[] =>
  searchKeymap.filter(binding => binding.key !== 'Mod-f')

/**
 * Keybindings that complement defaultKeymap: tab-indent, the custom find /
 * replace panel shortcuts (Ctrl+F / Ctrl+R / Escape, when handlers are
 * provided) or the stock CM search panel (Ctrl+F), match navigation
 * (F3 / Ctrl+G / Ctrl+Shift+G), occurrence selection (Ctrl+D /
 * Ctrl+Shift+L), goto-line (Ctrl+Alt+G), bracket-pair delete on Backspace,
 * and diagnostic navigation (F8 / Ctrl+Shift+M). Completion keys are bound
 * by the autocompletion() extension itself and are not listed here.
 */
export function editingKeymapBindings(findReplace?: FindReplaceHandlers): KeyBinding[] {
  const findReplaceBindings: KeyBinding[] = findReplace
    ? [
        {
          key: 'Mod-f',
          preventDefault: true,
          run: () => {
            findReplace.onOpen('find')
            return true
          },
        },
        {
          key: 'Mod-r',
          preventDefault: true,
          run: () => {
            findReplace.onOpen('replace')
            return true
          },
        },
        {
          // Runs before searchKeymap's Escape→closeSearchPanel; returning
          // false (panel closed) lets other Escape bindings (completion
          // dismissal, etc.) still fire.
          key: 'Escape',
          run: () => findReplace.onClose(),
        },
      ]
    : []
  return [
    indentWithTab,
    ...findReplaceBindings,
    ...(findReplace ? searchKeymapWithoutOpenPanel() : searchKeymap),
    ...closeBracketsKeymap,
    ...lintKeymap,
  ]
}

export interface EditingExtensionsOptions {
  /** Enable typing-companions (bracket auto-pairing, completions). Search and keymaps work read-only too. */
  editable?: boolean
  /**
   * Hooks the custom floating find/replace panel into the editor keymap
   * (Ctrl+F / Ctrl+R open, Escape closes). When provided, the built-in
   * CodeMirror search panel is fully suppressed — it can never render, even
   * if openSearchPanel is invoked — while the SearchQuery state stays under
   * the custom panel's control.
   */
  findReplace?: FindReplaceHandlers
}

/**
 * Standard editor companions missing from the bare defaultKeymap wiring:
 * search state + keymaps, selection-match highlighting, tab indent,
 * bracket auto-pairing, and manual completions (Ctrl+Space only —
 * activateOnTyping stays off so popups never interrupt plain typing).
 */
export function editingExtensions({ editable = false, findReplace }: EditingExtensionsOptions = {}): Extension[] {
  const exts: Extension[] = [
    // The default CM search panel is replaced by the IDEA-style
    // FindReplacePanel. Supplying createPanel guarantees the stock panel
    // never renders; the SearchQuery state and the find/replace commands
    // remain fully available to the custom panel via setSearchQuery etc.
    search({ createPanel: () => ({ dom: document.createElement('div') }) }),
    highlightSelectionMatches(),
    // Prec.high so bracket-pair Backspace wins over the preset keymap's
    // plain deleteCharBackward (deleteBracketPair returns false when not
    // applicable, so other keys fall through unchanged).
    Prec.high(keymap.of(editingKeymapBindings(findReplace))),
  ]
  if (editable) {
    exts.push(closeBrackets(), autocompletion({ activateOnTyping: false }))
  }
  return exts
}
