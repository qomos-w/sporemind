import { describe, it, expect } from 'vitest'
import { parseListText } from './list-text.ts'

describe('parseListText', () => {
  it('parses plain entries with trailing slash marking dirs', () => {
    const r = parseListText('CLAUDE.md\npkg/\nweb/')
    expect(r.entries).toEqual([
      { Name: 'CLAUDE.md', IsDir: false, Size: 0, ModTime: '' },
      { Name: 'pkg', IsDir: true, Size: 0, ModTime: '' },
      { Name: 'web', IsDir: true, Size: 0, ModTime: '' },
    ])
    expect(r.truncated).toBe(false)
    expect(r.extraLines).toEqual([])
  })

  it('parses detail lines with mtime and size, right-anchored', () => {
    const r = parseListText(
      'PUBLIC_VERSION 2026-09-01 23:01:06 +08:00 Size:5\n' +
      'assets/ 2026-09-01 23:01:09 +08:00\n' +
      'my file v2.txt 2026-09-02 06:53:42 -03:00 Size:1024\n' +
      'weird 2026-09-01 23:01:06 +08:00 2026-09-02 10:00:00 +08:00 Size:7\n',
    )
    expect(r.entries[0]).toMatchObject({ Name: 'PUBLIC_VERSION', IsDir: false, Size: 5 })
    expect(r.entries[1]).toMatchObject({ Name: 'assets', IsDir: true, Size: 0 })
    expect(r.entries[2]).toMatchObject({ Name: 'my file v2.txt', IsDir: false, Size: 1024 })
    // File name that itself contains a date-like tail: greedy prefix keeps it.
    expect(r.entries[3]).toMatchObject({ Name: 'weird 2026-09-01 23:01:06 +08:00', Size: 7 })
    expect(r.entries[0]?.ModTime).toBe(new Date('2026-09-01T23:01:06+08:00').toISOString())
  })

  it('collects markers as extra lines and flags truncation', () => {
    const r = parseListText('a.go 2026-09-01 23:01:06 +08:00 Size:1\n\n[truncated: showing first 5000 entries]\n\n[worktree note] X is outside your bound worktree.')
    expect(r.entries).toHaveLength(1)
    expect(r.truncated).toBe(true)
    expect(r.extraLines).toHaveLength(2)
  })

  it('treats empty-directory marker as extra, not an entry', () => {
    const r = parseListText('(empty directory)')
    expect(r.entries).toEqual([])
    expect(r.extraLines).toEqual(['(empty directory)'])
  })
})
