import { describe, expect, it } from 'vitest'
import { splitDebugInput } from './debugInput'

describe('splitDebugInput', () => {
  it('returns [] for empty and whitespace-only lines', () => {
    expect(splitDebugInput('')).toEqual([])
    expect(splitDebugInput('   ')).toEqual([])
    expect(splitDebugInput('\t \n')).toEqual([])
  })

  it('splits a single bare token', () => {
    expect(splitDebugInput('help')).toEqual(['help'])
  })

  it('splits bare tokens on whitespace, collapsing runs of spaces', () => {
    expect(splitDebugInput('help --flag value')).toEqual(['help', '--flag', 'value'])
    expect(splitDebugInput('a   b\tc')).toEqual(['a', 'b', 'c'])
  })

  it('keeps a double-quoted segment as one arg, stripping the quotes', () => {
    expect(splitDebugInput('echo "hello world"')).toEqual(['echo', 'hello world'])
    expect(splitDebugInput('"quoted only"')).toEqual(['quoted only'])
  })

  it('mixes quoted and bare tokens', () => {
    expect(splitDebugInput('run --flag "a b" c "d e f"')).toEqual(['run', '--flag', 'a b', 'c', 'd e f'])
  })

  it('keeps an empty quoted segment as an empty arg', () => {
    expect(splitDebugInput('say ""')).toEqual(['say', ''])
    expect(splitDebugInput('""')).toEqual([''])
  })

  it('splits a quote-adjacent bare token after a quoted segment', () => {
    expect(splitDebugInput('m "a"b')).toEqual(['m', 'a', 'b'])
  })

  it('treats an unterminated quote as a bare token', () => {
    expect(splitDebugInput('cmd "unterminated')).toEqual(['cmd', '"unterminated'])
  })

  it('does not mutate its input or keep state between calls', () => {
    const line = 'echo "hello world" again'
    expect(splitDebugInput(line)).toEqual(['echo', 'hello world', 'again'])
    // A second call (including one with no matches) yields the same result.
    expect(splitDebugInput(line)).toEqual(['echo', 'hello world', 'again'])
    expect(splitDebugInput('   ')).toEqual([])
    expect(splitDebugInput(line)).toEqual(['echo', 'hello world', 'again'])
  })
})
