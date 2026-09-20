import { describe, it, expect } from 'vitest'
import { splitMarkdownChunks, looksLikeCompleteTable } from './markdown-chunks'

describe('splitMarkdownChunks', () => {
  it('returns empty array for empty string', () => {
    expect(splitMarkdownChunks('')).toEqual([])
  })

  it('single paragraph → one streaming chunk', () => {
    const chunks = splitMarkdownChunks('Hello world')
    expect(chunks).toEqual([
      { content: 'Hello world\n', finalized: false },
    ])
  })

  it('two paragraphs separated by empty line → first finalized, second streaming', () => {
    const chunks = splitMarkdownChunks('Para one\n\nPara two')
    expect(chunks).toEqual([
      { content: 'Para one', finalized: true },
      { content: 'Para two\n', finalized: false },
    ])
  })

  it('code block is atomic and finalized when closed', () => {
    const text = 'Some intro\n\n```go\nfunc main() {}\n```\n\nOutro'
    const chunks = splitMarkdownChunks(text)
    expect(chunks).toHaveLength(3)
    expect(chunks[0]!).toEqual({ content: 'Some intro', finalized: true })
    expect(chunks[1]!).toEqual({
      content: '```go\nfunc main() {}\n```',
      finalized: true,
    })
    expect(chunks[2]!).toEqual({ content: 'Outro\n', finalized: false })
  })

  it('unclosed code block → single streaming chunk', () => {
    // split('\n') on trailing \n produces an extra empty line inside the code block
    const chunks = splitMarkdownChunks('```go\nfunc main() {}\n')
    expect(chunks).toEqual([
      { content: '```go\nfunc main() {}\n\n', finalized: false },
    ])
  })

  it('code block followed by empty line then paragraph', () => {
    const text = '```\ncode\n```\n\nNext para'
    const chunks = splitMarkdownChunks(text)
    expect(chunks).toHaveLength(2)
    expect(chunks[0]!).toEqual({ content: '```\ncode\n```', finalized: true })
    expect(chunks[1]!).toEqual({ content: 'Next para\n', finalized: false })
  })

  it('list items with blank line between them stay as one chunk', () => {
    const text = '- item 1\n\n- item 2\n\n- item 3'
    const chunks = splitMarkdownChunks(text)
    expect(chunks).toHaveLength(1)
    expect(chunks[0]!.finalized).toBe(false)
    expect(chunks[0]!.content).toBe('- item 1\n\n- item 2\n\n- item 3\n')
  })

  it('blockquote continuation', () => {
    const text = '> line 1\n\n> line 2'
    const chunks = splitMarkdownChunks(text)
    expect(chunks).toHaveLength(1)
    expect(chunks[0]!.content).toBe('> line 1\n\n> line 2\n')
  })

  it('paragraph followed by list splits', () => {
    const text = 'Some text\n\n- item\n- item'
    const chunks = splitMarkdownChunks(text)
    expect(chunks).toHaveLength(2)
    expect(chunks[0]!).toEqual({ content: 'Some text', finalized: true })
    expect(chunks[1]!.finalized).toBe(false)
    expect(chunks[1]!.content).toBe('- item\n- item\n')
  })

  it('trailing empty lines become streaming chunk', () => {
    // The trailing \n\n produces an empty chunk after trimStart()
    const chunks = splitMarkdownChunks('Para one\n\n')
    expect(chunks).toEqual([
      { content: 'Para one', finalized: true },
      { content: '', finalized: false },
    ])
  })

  it('multi-paragraph text with mixed blocks', () => {
    const text = 'First para.\n\nSecond para with **bold**.\n\n```\ncode line 1\ncode line 2\n```\n\nFinal para'
    const chunks = splitMarkdownChunks(text)
    expect(chunks).toHaveLength(4)
    expect(chunks[0]!).toEqual({ content: 'First para.', finalized: true })
    expect(chunks[1]!).toEqual({ content: 'Second para with **bold**.', finalized: true })
    expect(chunks[2]!).toEqual({
      content: '```\ncode line 1\ncode line 2\n```',
      finalized: true,
    })
    expect(chunks[3]!).toEqual({ content: 'Final para\n', finalized: false })
  })

  it('table with blank lines between rows stays as one chunk', () => {
    const text = '| a | b |\n\n| --- | --- |\n\n| 1 | 2 |'
    const chunks = splitMarkdownChunks(text)
    expect(chunks).toHaveLength(1)
    expect(chunks[0]!.content).toBe('| a | b |\n\n| --- | --- |\n\n| 1 | 2 |\n')
    expect(chunks[0]!.finalized).toBe(false)
  })

  it('table cells containing blockquote symbol do not break the table apart', () => {
    const text = '| op | cmd |\n| --- | --- |\n| redirect | echo x \u003e file.txt |'
    const chunks = splitMarkdownChunks(text)
    expect(chunks).toHaveLength(1)
    expect(chunks[0]!.content).toBe('| op | cmd |\n| --- | --- |\n| redirect | echo x \u003e file.txt |\n')
    expect(chunks[0]!.finalized).toBe(false)
  })

  it('blockquote-wrapped table with blank lines stays as one chunk', () => {
    const text = '\u003e | a | b |\n\n\u003e | --- | --- |\n\u003e | 1 | 2 |'
    const chunks = splitMarkdownChunks(text)
    expect(chunks).toHaveLength(1)
    expect(chunks[0]!.content).toBe('\u003e | a | b |\n\n\u003e | --- | --- |\n\u003e | 1 | 2 |\n')
    expect(chunks[0]!.finalized).toBe(false)
  })
})

describe('looksLikeCompleteTable', () => {
  it('returns true for a simple GFM table', () => {
    expect(looksLikeCompleteTable('| a | b |\n| --- | --- |\n| 1 | 2 |')).toBe(true)
  })

  it('returns true for a blockquote-wrapped table', () => {
    expect(looksLikeCompleteTable('\u003e | a | b |\n\u003e | --- | --- |\n\u003e | 1 | 2 |')).toBe(true)
  })

  it('returns false for a table with only a header row', () => {
    expect(looksLikeCompleteTable('| a | b |')).toBe(false)
  })

  it('returns false for plain paragraph', () => {
    expect(looksLikeCompleteTable('Hello world')).toBe(false)
  })

  it('returns false for a blockquote that contains a table plus extra text', () => {
    expect(
      looksLikeCompleteTable('\u003e | a | b |\n\u003e | --- | --- |\n\u003e | 1 | 2 |\n\u003e note')
    ).toBe(false)
  })

  it('returns true for a table whose cell contains a \u003e symbol', () => {
    expect(looksLikeCompleteTable('| op | cmd |\n| --- | --- |\n| redirect | echo x \u003e file.txt |')).toBe(true)
  })
})
