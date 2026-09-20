import { StreamLanguage, LanguageSupport, LanguageDescription, StringStream } from '@codemirror/language'
import type { StreamParser } from '@codemirror/language'
import type { LanguageFn } from 'highlight.js'

// Keyword sets mirror the upstream spore lexer (spore/internal/script/frontend/token.go).

const controlKeywords = new Set([
  'if', 'else', 'for', 'while', 'return', 'break', 'continue',
  'when', 'case', 'in', 'try', 'catch', 'defer', 'yield',
])

const definitionKeywords = new Set([
  'fun', 'export', 'class', 'struct', 'enum', 'type', 'var',
  'package', 'import', 'stream', 'interface',
])

const typeKeywords = new Set([
  'void', 'any', 'bool', 'byte', 'short', 'ushort', 'uint', 'long',
  'ulong', 'double', 'bytes', 'array', 'map', 'int', 'float', 'string', 'media',
])

const modifierKeywords = new Set([
  'super', 'constructor', 'open', 'override', 'public', 'private',
  'static', 'optional', 'is', 'as', 'from', 'this', 'new', 'async', 'await',
])

interface SporeState {
  inString: boolean
  inComment: boolean
}

// Stream tokens return lezer tag *names* (see StreamParser.token docs):
// plain names resolve to tags.<name>, dotted names apply modifiers.
export const sporeParser: StreamParser<SporeState> = {
  name: 'spore',
  startState: () => ({ inString: false, inComment: false }),
  token(stream: StringStream, state: SporeState): string | null {
    if (stream.eatSpace()) return null

    if (state.inComment) {
      if (stream.match(/^.*?\*\//)) state.inComment = false
      else stream.skipToEnd()
      return 'blockComment'
    }

    if (state.inString) {
      let escaped = false
      while (!stream.eol()) {
        const ch = stream.next()
        if (escaped) escaped = false
        else if (ch === '\\') escaped = true
        else if (ch === '"') {
          state.inString = false
          break
        }
      }
      return 'string'
    }

    if (stream.match('//')) {
      stream.skipToEnd()
      return 'lineComment'
    }
    if (stream.match('/*')) {
      state.inComment = true
      if (stream.match(/^.*?\*\//)) state.inComment = false
      else stream.skipToEnd()
      return 'blockComment'
    }
    if (stream.eat('"')) {
      state.inString = true
      return 'string'
    }

    const start = stream.pos
    if (stream.match(/^[A-Za-z_][A-Za-z0-9_]*/)) {
      const name = stream.string.slice(start, stream.pos)
      if (controlKeywords.has(name)) return 'controlKeyword'
      if (definitionKeywords.has(name)) return 'definitionKeyword'
      if (typeKeywords.has(name)) return 'typeName'
      if (name === 'true' || name === 'false') return 'bool'
      if (name === 'null') return 'null'
      if (modifierKeywords.has(name)) return 'keyword'
      if (stream.peek() === '(') return 'variableName.function'
      return null
    }

    if (stream.match(/^[0-9]+(\.[0-9]+)?/)) return 'number'
    if (stream.match(/^@[A-Za-z_][A-Za-z0-9_]*/)) return 'meta'
    if (stream.match(/^(?:\?\?|\?\.|->|=>|==|!=|<=|>=|&&|\|\||\.\.)/)) return 'operator'
    if (stream.match(/^[(){}[\]]/)) return 'bracket'
    if (stream.match(/^[,;:.@]/)) return 'punctuation'
    if (stream.eat(/^[+\-*/%=<>!?]/)) return 'operator'

    stream.next()
    return null
  },
  languageData: {
    commentTokens: { line: '//', block: { open: '/*', close: '*/' } },
  },
}

export const sporeStreamParser = StreamLanguage.define(sporeParser)

export function spore(): LanguageSupport {
  return new LanguageSupport(sporeStreamParser)
}

export const sporeLanguageDescription = LanguageDescription.of({
  name: 'spore',
  alias: ['sporescript'],
  extensions: ['spore'],
  load: async () => spore(),
})

/** Same grammar expressed for highlight.js, so the lowlight-based read-only
 * renderers (diff viewer, markdown code fences) reuse one keyword source. */
export const sporeHighlightJs: LanguageFn = hljs => ({
  name: 'Spore',
  keywords: {
    keyword: [
      ...controlKeywords, ...definitionKeywords, ...modifierKeywords,
    ].join(' '),
    type: [...typeKeywords].join(' '),
    literal: 'true false null',
  },
  contains: [
    hljs.C_LINE_COMMENT_MODE,
    hljs.C_BLOCK_COMMENT_MODE,
    hljs.QUOTE_STRING_MODE,
    { scope: 'number', begin: '\\b\\d+(\\.\\d+)?\\b' },
  ],
})
