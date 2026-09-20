import { describe, it, expect } from 'vitest'
import { StringStream } from '@codemirror/language'
import { sporeParser, sporeLanguageDescription } from './sporeLanguage'

function tokenize(code: string): Array<[string, string | null]> {
  const state = sporeParser.startState!(2)!
  const out: Array<[string, string | null]> = []
  for (const line of code.split('\n')) {
    const stream = new StringStream(line, 2, 2)
    while (!stream.eol()) {
      const before = stream.pos
      const tag = sporeParser.token(stream, state)
      expect(stream.pos).toBeGreaterThan(before) // tokenizer must always advance
      out.push([line.slice(before, stream.pos), tag])
    }
  }
  return out
}

describe('sporeLanguage tokenizer', () => {
  it('classifies declaration keywords, call identifiers and brackets', () => {
    expect(tokenize('fun main() {')).toEqual([
      ['fun', 'definitionKeyword'],
      [' ', null],
      ['main', 'variableName.function'],
      ['(', 'bracket'],
      [')', 'bracket'],
      [' ', null],
      ['{', 'bracket'],
    ])
  })

  it('classifies all control-flow keywords', () => {
    const words = 'if else for while return break continue when case in try catch defer yield'.split(' ')
    const tokens = tokenize(words.join(' '))
    expect(tokens.filter(([, tag]) => tag === 'controlKeyword')).toHaveLength(words.length)
  })

  it('classifies builtin types and boolean/null literals', () => {
    expect(tokenize('int string media bool')).toEqual([
      ['int', 'typeName'],
      [' ', null],
      ['string', 'typeName'],
      [' ', null],
      ['media', 'typeName'],
      [' ', null],
      ['bool', 'typeName'],
    ])
    expect(tokenize('true false null')).toEqual([
      ['true', 'bool'],
      [' ', null],
      ['false', 'bool'],
      [' ', null],
      ['null', 'null'],
    ])
  })

  it('tokenizes strings with escapes across lines', () => {
    const tokens = tokenize('var s = "a\\"b\nc" + s')
    expect(tokens).toEqual([
      ['var', 'definitionKeyword'],
      [' ', null],
      ['s', null],
      [' ', null],
      ['=', 'operator'],
      [' ', null],
      ['"', 'string'],
      ['a\\"b', 'string'],
      ['c"', 'string'],
      [' ', null],
      ['+', 'operator'],
      [' ', null],
      ['s', null],
    ])
  })

  it('tokenizes line and block comments', () => {
    expect(tokenize('var x = 1 // tail')).toEqual([
      ['var', 'definitionKeyword'],
      [' ', null],
      ['x', null],
      [' ', null],
      ['=', 'operator'],
      [' ', null],
      ['1', 'number'],
      [' ', null],
      ['// tail', 'lineComment'],
    ])
    expect(tokenize('/* multi\nline */ var y')).toEqual([
      ['/* multi', 'blockComment'],
      ['line */', 'blockComment'],
      [' ', null],
      ['var', 'definitionKeyword'],
      [' ', null],
      ['y', null],
    ])
  })

  it('tokenizes multi-char operators and annotations', () => {
    expect(tokenize('a ?? b?.c -> d => e .. f @entry')).toEqual([
      ['a', null],
      [' ', null],
      ['??', 'operator'],
      [' ', null],
      ['b', null],
      ['?.', 'operator'],
      ['c', null],
      [' ', null],
      ['->', 'operator'],
      [' ', null],
      ['d', null],
      [' ', null],
      ['=>', 'operator'],
      [' ', null],
      ['e', null],
      [' ', null],
      ['..', 'operator'],
      [' ', null],
      ['f', null],
      [' ', null],
      ['@entry', 'meta'],
    ])
  })

  it('exposes a loadable markdown language description', async () => {
    const support = await sporeLanguageDescription.load()
    expect(support).toBeDefined()
  })
})
