import { StateEffect, type EditorState, type Extension } from '@codemirror/state'
import {
  ViewPlugin,
  Decoration,
  type DecorationSet,
  EditorView,
  type ViewUpdate,
} from '@codemirror/view'
import { syntaxTree } from '@codemirror/language'
import { uriToPath, pathToUri, type LspClient, type Location } from './lspClient'

export interface RefsPopupAnchor {
  x: number
  y: number
}

export interface RefsPopupQueryPos {
  uri: string
  line: number
  character: number
}

export interface ShowReferencesPayload {
  locations: Location[]
  anchor: RefsPopupAnchor
  queryPos: RefsPopupQueryPos
}

export interface LspGotoDefinitionOptions {
  client: LspClient
  filePath: string
  onGotoDefinition: (filePath: string, line: number) => void
  onShowReferences?: (locations: Location[], anchor: RefsPopupAnchor, queryPos: RefsPopupQueryPos) => void
}

function isAtDefinition(filePath: string, lspLine: number, location: Location): boolean {
  return uriToPath(location.uri) === filePath && location.range.start.line === lspLine
}

const ctrlEffect = StateEffect.define<boolean>()
const hoverEffect = StateEffect.define<number | null>()

// Upper bound for one goto-definition round trip. The backend LSP engine
// has its own 15s request timeout; this races past it so the busy chip and
// the re-entrancy guard are always released even if a promise never settles.
export const LSP_REQUEST_BUDGET_MS = 20_000

const BUSY_CHIP_CLASS = 'cm-lsp-busy'
const BUSY_CURSOR_CLASS = 'cm-lsp-busy-cursor'

function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('lsp request timed out')), ms)
    p.then(
      (v) => { clearTimeout(timer); resolve(v) },
      (e) => { clearTimeout(timer); reject(e) },
    )
  })
}

const gotoDecoration = Decoration.mark({
  attributes: { style: 'text-decoration: underline; cursor: pointer;' },
})

function isIdentifierNode(name: string): boolean {
  return /(?:Name|Identifier|Definition)$/i.test(name) && !/Punctuation|Operator|String|Number|Comment|LineBreak|Space|Keyword/i.test(name)
}

function getIdentifierRange(
  state: EditorState,
  pos: number,
): { from: number; to: number } | null {
  const tree = syntaxTree(state)
  if (tree.length === 0) return null

  let node = tree.resolveInner(pos, -1)
  if (!isIdentifierNode(node.name)) {
    node = tree.resolveInner(pos, 1)
  }
  if (!isIdentifierNode(node.name)) return null
  return { from: node.from, to: node.to }
}

export function lspGotoDefinition(options: LspGotoDefinitionOptions): Extension {
  class GotoDefinitionPluginValue {
    ctrlHeld = false
    hoverPos: number | null = null
    decorations: DecorationSet = Decoration.none
    /** One in-flight request at a time: guards both the LSP call and the busy chip. */
    pending = false
    private busyEl: HTMLElement | null = null

    constructor(readonly view: EditorView) {}

    destroy() {
      this.clearBusy()
    }

    /** Attach a transient busy chip near the click point inside the editor DOM.
     *  Plain DOM on purpose: no React state, no re-renders, removed in finally. */
    private showBusy(event: MouseEvent) {
      if (this.busyEl) return
      const el = document.createElement('div')
      el.className = BUSY_CHIP_CLASS
      const rect = this.view.dom.getBoundingClientRect()
      el.style.left = `${Math.max(event.clientX - rect.left + 8, 0)}px`
      el.style.top = `${Math.max(event.clientY - rect.top + 14, 0)}px`
      this.view.dom.appendChild(el)
      this.busyEl = el
      this.view.dom.classList.add(BUSY_CURSOR_CLASS)
    }

    private clearBusy() {
      this.busyEl?.remove()
      this.busyEl = null
      this.view.dom.classList.remove(BUSY_CURSOR_CLASS)
    }

    update(update: ViewUpdate) {
      let changed = false
      for (const tr of update.transactions) {
        for (const effect of tr.effects) {
          if (effect.is(ctrlEffect)) {
            this.ctrlHeld = effect.value
            changed = true
          } else if (effect.is(hoverEffect)) {
            this.hoverPos = effect.value
            changed = true
          }
        }
      }
      if (changed || update.docChanged) {
        this.recomputeDecorations()
      }
    }

    private recomputeDecorations() {
      if (!this.ctrlHeld || this.hoverPos == null) {
        this.decorations = Decoration.none
        return
      }
      const range = getIdentifierRange(this.view.state, this.hoverPos)
      if (!range) {
        this.decorations = Decoration.none
        return
      }
      this.decorations = Decoration.set([gotoDecoration.range(range.from, range.to)])
    }

    handleMouseMove(event: MouseEvent) {
      const ctrl = event.ctrlKey
      const effects: StateEffect<unknown>[] = []
      if (ctrl !== this.ctrlHeld) {
        effects.push(ctrlEffect.of(ctrl))
      }
      if (ctrl) {
        const pos = this.view.posAtCoords({ x: event.clientX, y: event.clientY })
        if (pos != null && pos !== this.hoverPos) {
          effects.push(hoverEffect.of(pos))
        }
      }
      if (effects.length > 0) {
        this.view.dispatch({ effects })
      }
    }

    handleKeyToggle(event: KeyboardEvent, pressed: boolean) {
      const key = event.key
      if (key !== 'Control') return
      if (pressed === this.ctrlHeld) return
      this.view.dispatch({ effects: ctrlEffect.of(pressed) })
    }

    async handleClick(event: MouseEvent) {
      if (!this.ctrlHeld) return
      // Re-entrancy: ignore clicks while a previous definition request is
      // still in flight (e.g. engine warming up). Prevents stacking parallel
      // LSP calls and overlapping chips.
      if (this.pending) return
      event.preventDefault()

      const pos = this.view.posAtCoords({ x: event.clientX, y: event.clientY })
      if (pos == null) return

      const line = this.view.state.doc.lineAt(pos)
      const lspLine = line.number - 1
      const character = pos - line.from

      this.pending = true
      this.showBusy(event)
      try {
        const location = await withTimeout(
          options.client.definition(options.filePath, lspLine, character),
          LSP_REQUEST_BUDGET_MS,
        )
        if (!location) return

        const targetPath = uriToPath(location.uri)
        const targetLine = location.range.start.line

        // When the click lands on the definition itself (same file, same line),
        // show a references search popup instead of jumping.
        if (options.onShowReferences && isAtDefinition(options.filePath, lspLine, location)) {
          const locations = await withTimeout(
            options.client.references(options.filePath, lspLine, character),
            LSP_REQUEST_BUDGET_MS,
          )
          options.onShowReferences(
            locations,
            { x: event.clientX, y: event.clientY },
            { uri: pathToUri(options.filePath), line: lspLine, character },
          )
          return
        }

        options.onGotoDefinition(targetPath, targetLine)
      } catch (err) {
        // Swallow the UI effect, but log so TS/other engines don't fail silently.
        console.warn('[lsp] goto-definition failed', err)
      } finally {
        this.pending = false
        this.clearBusy()
      }
    }
  }

  return ViewPlugin.fromClass(GotoDefinitionPluginValue, {
    decorations: (value) => value.decorations,
    provide: (plugin) => [
      EditorView.domEventHandlers({
        mousemove(event, view) {
          view.plugin(plugin)?.handleMouseMove(event)
          return false
        },
        click(event, view) {
          const p = view.plugin(plugin)
          if (!p?.ctrlHeld) return false
          event.preventDefault()
          void p.handleClick(event)
          return true
        },
        keydown(event, view) {
          view.plugin(plugin)?.handleKeyToggle(event, true)
          return false
        },
        keyup(event, view) {
          view.plugin(plugin)?.handleKeyToggle(event, false)
          return false
        },
      }),
    ],
  })
}
