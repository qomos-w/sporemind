import { goLanguage } from '@codemirror/lang-go'
import type { SyntaxNode } from '@lezer/common'

export interface SyntaxSpan {
  text: string
  color?: string
}

// lezer-go names keyword/operator/punctuation tokens by their literal text and
// other tokens by category. Colors mirror projectHighlight in CodeMirrorViewer
// so popup snippets match the editor, theme-adaptive via app CSS vars.
const KEYWORDS = new Set([
  'break', 'case', 'chan', 'const', 'continue', 'default', 'defer', 'else',
  'fallthrough', 'for', 'func', 'go', 'goto', 'if', 'import', 'interface',
  'map', 'package', 'range', 'return', 'select', 'struct', 'switch', 'type', 'var',
])
const OPERATORS = new Set([':=', '=', '+', '-', '*', '/', '%', '<', '>', '!', '&', '|', '^', '<<', '>>', '&&', '||', '<-', '...', '+=', '-=', '*=', '/=', '==', '!=', '<=', '>='])
const PUNCTUATION = new Set(['(', ')', '{', '}', '[', ']', ',', ';', '.'])

const NODE_COLOR: Record<string, string> = {
  LineComment: 'var(--text-tertiary)',
  BlockComment: 'var(--text-tertiary)',
  String: 'var(--status-success)',
  Rune: 'var(--status-success)',
  Number: 'var(--syntax-type)',
  TypeName: 'var(--syntax-type)',
  DefName: 'var(--accent-primary)',
  VariableName: 'var(--syntax-variable)',
  FieldName: 'var(--syntax-variable)',
  LabelName: 'var(--syntax-variable)',
  BuiltinName: 'var(--syntax-constant)',
  Boolean: 'var(--syntax-constant)',
  ArithOp: 'var(--syntax-operator)',
  LogicOp: 'var(--syntax-operator)',
  CompareOp: 'var(--syntax-operator)',
  AssignOp: 'var(--syntax-operator)',
  UpdateOp: 'var(--syntax-operator)',
  BitOp: 'var(--syntax-operator)',
}

function nodeColor(name: string): string | undefined {
  if (KEYWORDS.has(name)) return 'var(--syntax-keyword)'
  if (OPERATORS.has(name)) return 'var(--syntax-operator)'
  if (PUNCTUATION.has(name)) return 'var(--text-secondary)'
  if (name === 'true' || name === 'false' || name === 'nil' || name === 'iota') return 'var(--syntax-constant)'
  return NODE_COLOR[name]
}

const parser = goLanguage.parser

/**
 * Tokenize a single Go source line into colored spans using the lezer-go
 * parser. Runs standalone (no editor state) so popup rows render with the
 * same syntax colors as the editor, theme-adaptive via CSS vars.
 */
export function highlightGoLine(line: string): SyntaxSpan[] {
  const spans: SyntaxSpan[] = []
  if (!line) return spans
  const text = line.replace(/\t/g, '  ')

  const push = (from: number, to: number, color?: string): void => {
    const slice = text.slice(from, to)
    if (!slice) return
    const last = spans[spans.length - 1]
    if (last && (last.color ?? undefined) === (color ?? undefined)) {
      last.text += slice
    } else {
      spans.push({ text: slice, color })
    }
  }

  const collect = (node: SyntaxNode, inherited?: string): void => {
    const own = nodeColor(node.name) ?? inherited
    if (!node.firstChild) {
      push(node.from, node.to, own)
      return
    }
    let pos = node.from
    let ch: SyntaxNode | null = node.firstChild
    while (ch) {
      if (ch.from > pos) push(pos, ch.from, own) // whitespace gap between tokens
      collect(ch, own)
      pos = ch.to
      ch = ch.nextSibling
    }
    if (node.to > pos) push(pos, node.to, own)
  }

  collect(parser.parse(text).topNode, undefined)
  if (spans.length === 0) spans.push({ text })
  return spans
}
